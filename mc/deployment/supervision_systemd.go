package deployment

// Systemd unit rendering for the supervision bundle. The logic lives in this
// untagged file so the pure renderers are testable on any host; the Linux
// platform files are thin wrappers that expose them through the seam hooks.

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

type systemdUnit struct {
	Description      string
	ExecStart        []string
	Environment      [][2]string
	WorkingDirectory string
	StandardOutput   string
	StandardError    string
}

// systemdQuoteValue renders one unit-file quoted string. Unit files carry no
// control characters: a value containing newline/CR/NUL is refused outright
// rather than smuggled into the parser.
func systemdQuoteValue(value string) (string, error) {
	if strings.ContainsAny(value, "\n\r\x00") {
		return "", fmt.Errorf("systemd unit value contains control characters")
	}
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String(), nil
}

// renderSystemdUnit writes the service file for one supervised unit. Restart
// mirrors the launchd KeepAlive posture; RestartSec mirrors ThrottleInterval;
// append: keeps the operator-visible log destinations identical across
// platforms so doctor and repair flows read the same files.
func renderSystemdUnit(u systemdUnit) ([]byte, error) {
	if len(u.ExecStart) == 0 {
		return nil, fmt.Errorf("systemd unit requires an ExecStart")
	}
	if u.Description == "" || u.WorkingDirectory == "" {
		return nil, fmt.Errorf("systemd unit requires a description and working directory")
	}
	lines := []string{"[Unit]", "Description=" + mustQuote(u.Description)}
	execParts := make([]string, 0, len(u.ExecStart))
	for _, arg := range u.ExecStart {
		execParts = append(execParts, mustQuote(arg))
	}
	lines = append(lines, "ExecStart="+strings.Join(execParts, " "))
	lines = append(lines, "", "[Service]")
	lines = append(lines, "WorkingDirectory="+mustQuote(u.WorkingDirectory))
	for _, kv := range u.Environment {
		lines = append(lines, "Environment="+mustQuote(kv[0]+"="+kv[1]))
	}
	if u.StandardOutput != "" {
		lines = append(lines, "StandardOutput=append:"+mustQuote(u.StandardOutput))
	}
	if u.StandardError != "" {
		lines = append(lines, "StandardError=append:"+mustQuote(u.StandardError))
	}
	lines = append(lines, "Restart=always", "RestartSec=10")
	lines = append(lines, "", "[Install]", "WantedBy=default.target", "")
	return []byte(strings.Join(lines, "\n")), nil
}

// mustQuote is safe after validation because every renderer entry point runs
// systemdQuoteValue's control-character check over the same values first.
func mustQuote(value string) string {
	quoted, err := systemdQuoteValue(value)
	if err != nil {
		return "\"invalid\""
	}
	return quoted
}

func validateUnitValues(u systemdUnit) error {
	if _, err := systemdQuoteValue(u.Description); err != nil {
		return err
	}
	if _, err := systemdQuoteValue(u.WorkingDirectory); err != nil {
		return err
	}
	for _, arg := range u.ExecStart {
		if _, err := systemdQuoteValue(arg); err != nil {
			return err
		}
	}
	for _, kv := range u.Environment {
		if _, err := systemdQuoteValue(kv[0] + "=" + kv[1]); err != nil {
			return err
		}
	}
	for _, path := range []string{u.StandardOutput, u.StandardError} {
		if _, err := systemdQuoteValue(path); err != nil {
			return err
		}
	}
	return nil
}

// systemdBundledUnits renders the prepared bundle's three unit files plus the
// agentsview config, from the same validated inputs the darwin renderer uses.
// Descriptions follow the CURRENT runtime identity (com.culler labels): the
// rename to S-Horizon lands with A09 as one sweep, and new artifacts must not
// run ahead of it with a mixed brand.
func systemdBundledUnits(in unitRenderInput) (map[string][]byte, error) {
	env := [][2]string{{"MC_HOME", in.CanonicalHome}, {"MC_LOG_LEVEL", supervisedLogLevel}, {"PATH", in.PathEnv}}
	out := map[string][]byte{}
	for _, item := range []struct{ unit, label, entry, working string }{
		{"resident", in.Result.ResidentLabel, filepath.Join(in.HostRoot, "resident", "src", "main.ts"), filepath.Join(in.HostRoot, "resident")},
		{"dashboard", in.Result.DashboardLabel, filepath.Join(in.HostRoot, "dashboard", "src", "main.ts"), filepath.Join(in.HostRoot, "dashboard")},
	} {
		body, err := renderSystemdUnitChecked(systemdUnit{
			Description:      "Culler " + item.unit + " (" + item.label + ")",
			ExecStart:        []string{in.BunPath, item.entry, "--config", filepath.Join(in.Result.Root, item.unit+".json")},
			Environment:      env,
			WorkingDirectory: item.working,
			StandardOutput:   filepath.Join(in.LogRoot, item.unit+".stdout.log"),
			StandardError:    filepath.Join(in.LogRoot, item.unit+".stderr.log"),
		})
		if err != nil {
			return nil, fmt.Errorf("render %s unit: %w", item.unit, err)
		}
		out["units/"+item.unit+".service"] = body
	}
	agentsView, err := agentsViewSystemdBundle(in.CanonicalHome, in.Names, in.Spec.AgentsViewBind, in.AgentsViewToken)
	if err != nil {
		return nil, err
	}
	for rel, body := range agentsView {
		out[rel] = body
	}
	return out, nil
}

func renderSystemdUnitChecked(u systemdUnit) ([]byte, error) {
	if err := validateUnitValues(u); err != nil {
		return nil, err
	}
	return renderSystemdUnit(u)
}

// agentsViewSystemdBundle renders the AgentsView unit and its shared TOML
// configuration. The environment mirrors the launchd definition exactly: the
// dead loopback proxy keeps the pricing refresh from reaching the network,
// update checks and telemetry stay off, and HOME pins the data lookup.
func agentsViewSystemdBundle(canonicalHome string, names Names, bind, token string) (map[string][]byte, error) {
	host, port, err := viewerBind("surfaces.agentsview.bind", bind)
	if err != nil {
		return nil, err
	}
	if !viewerSecretPattern.MatchString(token) {
		return nil, fmt.Errorf("production supervision requires a valid AgentsView token")
	}
	pin, err := LoadAgentsViewPin()
	if err != nil {
		return nil, err
	}
	paths := AgentsViewPathsFor(canonicalHome)
	unit, err := renderSystemdUnitChecked(systemdUnit{
		Description: "Culler AgentsView (" + UnitLabel(names, "agentsview") + ")",
		ExecStart:   []string{paths.Executable, "serve", "--host", host, "--port", strconv.Itoa(port), "--no-browser", "--no-update-check"},
		Environment: [][2]string{
			{"AGENTSVIEW_DATA_DIR", paths.Data},
			{"AGENTSVIEW_DISABLE_UPDATE_CHECK", "1"},
			{"AGENTSVIEW_TELEMETRY_ENABLED", "0"},
			{"HOME", paths.Home},
			{"HTTPS_PROXY", "http://127.0.0.1:9"},
			{"HTTP_PROXY", "http://127.0.0.1:9"},
			{"PATH", "/usr/bin:/bin"},
		},
		WorkingDirectory: paths.Home,
		StandardOutput:   paths.StdoutLog,
		StandardError:    paths.StderrLog,
	})
	if err != nil {
		return nil, fmt.Errorf("render agentsview unit: %w", err)
	}
	return map[string][]byte{
		"units/agentsview.service": unit,
		agentsViewConfig:           agentsViewConfigTOML(paths, host, port, pin.DisabledAgents, token),
	}, nil
}
