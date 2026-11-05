package deployment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// SupervisedUnits lists the launchd units of a production deployment in stop
// order. The resident stops first and starts last, because it is the only unit
// that writes the spine. Activation starts the units in the reverse order.
var SupervisedUnits = []string{"resident", "dashboard", "agentsview"}

// UnitLabel returns the launchd label of one supervised unit.
func UnitLabel(names Names, unit string) string {
	return "com.culler." + names.Suffix + "." + unit
}

// SupervisionResultFor returns the unit labels and bundle root of the
// deployment at canonicalHome.
func SupervisionResultFor(canonicalHome string, names Names) SupervisionResult {
	return SupervisionResult{
		ResidentLabel:   UnitLabel(names, "resident"),
		DashboardLabel:  UnitLabel(names, "dashboard"),
		AgentsViewLabel: UnitLabel(names, "agentsview"),
		Root:            filepath.Join(canonicalHome, "supervision"),
	}
}

// Labels returns the three unit labels in stop order.
func (r SupervisionResult) Labels() []string {
	return []string{r.ResidentLabel, r.DashboardLabel, r.AgentsViewLabel}
}

const (
	agentsViewPlist  = "LaunchAgents/agentsview.plist"
	agentsViewConfig = "agentsview.toml"
)

// viewerBind validates one loopback address of a viewer.
func viewerBind(key, bind string) (string, int, error) {
	host, portText, err := net.SplitHostPort(bind)
	port, portErr := strconv.Atoi(portText)
	if err != nil || portErr != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("production supervision requires a valid %s host:port", key)
	}
	if host != "127.0.0.1" && host != "::1" && host != "localhost" {
		return "", 0, fmt.Errorf("production supervision requires a loopback %s", key)
	}
	return host, port, nil
}

func validateViewerBinds(policy SupervisionPolicy) error {
	ports := map[int]string{}
	for _, bind := range []struct{ key, value string }{
		{"surfaces.dashboard.bind", policy.DashboardBind},
		{"surfaces.agentsview.bind", policy.AgentsViewBind},
		{"surfaces.wiki.bind", policy.WikiBind},
	} {
		_, portText, err := net.SplitHostPort(bind.value)
		port, portErr := strconv.Atoi(portText)
		if err != nil || portErr != nil {
			return fmt.Errorf("production supervision requires a valid %s host:port", bind.key)
		}
		if bind.key != "surfaces.dashboard.bind" {
			if _, _, err := viewerBind(bind.key, bind.value); err != nil {
				return err
			}
		}
		if other, used := ports[port]; used {
			return fmt.Errorf("production supervision requires different ports for %s and %s", other, bind.key)
		}
		ports[port] = bind.key
	}
	return nil
}

// agentsViewBundle returns the AgentsView property list and configuration
// that a deployment at canonicalHome prepares for bind and token.
func agentsViewBundle(canonicalHome string, names Names, bind, token string) ([]byte, []byte, error) {
	host, port, err := viewerBind("surfaces.agentsview.bind", bind)
	if err != nil {
		return nil, nil, err
	}
	if !viewerSecretPattern.MatchString(token) {
		return nil, nil, fmt.Errorf("production supervision requires a valid AgentsView token")
	}
	pin, err := LoadAgentsViewPin()
	if err != nil {
		return nil, nil, err
	}
	paths := AgentsViewPathsFor(canonicalHome)
	return agentsViewLaunchAgent(UnitLabel(names, "agentsview"), paths, host, strconv.Itoa(port)),
		agentsViewConfigTOML(paths, host, port, pin.DisabledAgents, token), nil
}

// agentsViewLaunchAgent writes the AgentsView property list. The dead loopback
// proxy blocks the pricing refresh, which has no setting of its own. Go sends
// no loopback request through a proxy.
func agentsViewLaunchAgent(label string, paths AgentsViewPaths, host, port string) []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>serve</string>
    <string>--host</string>
    <string>%s</string>
    <string>--port</string>
    <string>%s</string>
    <string>--no-browser</string>
    <string>--no-update-check</string>
  </array>
  <key>WorkingDirectory</key>
  <string>%s</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>AGENTSVIEW_DATA_DIR</key>
    <string>%s</string>
    <key>AGENTSVIEW_DISABLE_UPDATE_CHECK</key>
    <string>1</string>
    <key>AGENTSVIEW_TELEMETRY_ENABLED</key>
    <string>0</string>
    <key>HOME</key>
    <string>%s</string>
    <key>HTTPS_PROXY</key>
    <string>http://127.0.0.1:9</string>
    <key>HTTP_PROXY</key>
    <string>http://127.0.0.1:9</string>
    <key>PATH</key>
    <string>/usr/bin:/bin</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Background</string>
  <key>ThrottleInterval</key>
  <integer>10</integer>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, xmlText(label), xmlText(paths.Executable), xmlText(host), xmlText(port), xmlText(paths.Home),
		xmlText(paths.Data), xmlText(paths.Home), xmlText(paths.StdoutLog), xmlText(paths.StderrLog)))
}

func agentsViewConfigTOML(paths AgentsViewPaths, host string, port int, disabled []string, token string) []byte {
	quoted := make([]string, len(disabled))
	for i, agent := range disabled {
		quoted[i] = tomlString(agent)
	}
	return []byte(fmt.Sprintf(`host = %s
port = %d
require_auth = true
auth_token = %s
disable_update_check = true
archive_content = "full"
result_content_blocked_categories = []
tool_result_images = "keep"
disabled_agents = [%s]

[terminal]
mode = "clipboard"

[agents.claude]
dirs = [%s]

[agents.codex]
dirs = [%s]
`, tomlString(host), port, tomlString(token), strings.Join(quoted, ", "), tomlString(paths.ClaudeTraceRoot), tomlString(paths.CodexTraceRoot)))
}

func tomlString(value string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range value {
		switch {
		case r == '"':
			out.WriteString(`\"`)
		case r == '\\':
			out.WriteString(`\\`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&out, `\u%04X`, r)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String()
}

// InstallAgentsViewConfig creates the AgentsView folders and writes the
// complete AgentsView configuration from the prepared supervision bundle.
// AgentsView rewrites its configuration at each start, so each bootstrap of
// the AgentsView unit must follow this call.
func InstallAgentsViewConfig(home string) error {
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return err
	}
	root := filepath.Join(canonicalHome, "supervision")
	if err := validateSupervisionTree(root); err != nil {
		return fmt.Errorf("validate prepared supervision: %w", err)
	}
	body, err := os.ReadFile(filepath.Join(root, agentsViewConfig))
	if err != nil {
		return err
	}
	paths := AgentsViewPathsFor(canonicalHome)
	for _, dir := range []string{
		paths.Root, paths.Data, paths.Home,
		filepath.Dir(filepath.Dir(paths.ClaudeTraceRoot)), filepath.Dir(paths.ClaudeTraceRoot),
		paths.ClaudeTraceRoot, paths.ClaudeTraceCopies,
		filepath.Dir(paths.CodexTraceRoot), paths.CodexTraceRoot,
	} {
		if err := ensureOwnerDirectory(dir, "AgentsView folder"); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(paths.Data, ".config-*.toml")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	_, err = tmp.Write(body)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write the AgentsView configuration: %w", err)
	}
	if err := os.Rename(tmpPath, paths.Config); err != nil {
		return fmt.Errorf("install the AgentsView configuration: %w", err)
	}
	return syncDirectory(paths.Data)
}

func requireAgentsViewExecutable(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("the AgentsView executable %s is missing", path)
	}
	if err != nil {
		return fmt.Errorf("inspect the AgentsView executable %s: %w", path, err)
	}
	uid, _, ok := statOwner(info)
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || !ok || int(uid) != os.Getuid() {
		return fmt.Errorf("the AgentsView executable %s must be an executable regular file that the operator owns", path)
	}
	return nil
}

// InspectAgentsViewUnit reports the label of the AgentsView unit when launchd
// loaded it from its prepared definition.
func InspectAgentsViewUnit(home, launchAgentsDir string, launchd LaunchdController) (string, error) {
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return "", err
	}
	if launchd == nil {
		return "", fmt.Errorf("the AgentsView inspection requires launchd")
	}
	names, err := RuntimeNames(canonicalHome)
	if err != nil {
		return "", err
	}
	label := UnitLabel(names, "agentsview")
	root := filepath.Join(canonicalHome, "supervision")
	if err := validateSupervisionTree(root); err != nil {
		return label, fmt.Errorf("prepared supervision is invalid: %w", err)
	}
	loaded, err := launchd.Loaded(label)
	if err != nil {
		return label, err
	}
	if !loaded {
		return label, fmt.Errorf("LaunchAgent %s is not loaded", label)
	}
	if err := verifyInstalledLaunchAgent(filepath.Join(root, filepath.FromSlash(agentsViewPlist)),
		filepath.Join(launchAgentsDir, label+".plist")); err != nil {
		return label, err
	}
	return label, nil
}

// ViewerURL returns the browser address of a viewer bind.
func ViewerURL(bind string) string {
	return "http://" + bind
}

var viewerClient = &http.Client{
	Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func viewerGet(ctx context.Context, url, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return viewerClient.Do(req)
}

// ErrAnotherViewerService marks a viewer address where another service
// answers. Setup keeps the address, so the repair frees the port or selects
// another port.
var ErrAnotherViewerService = errors.New("another service answers at the viewer address")

// ErrViewerHTTPStatus marks an AgentsView address that answers with an
// unexpected HTTP status. Another service or AgentsView itself can give this
// answer, so the repair covers both.
var ErrViewerHTTPStatus = errors.New("the viewer address answers with an unexpected HTTP status")

// PingAgentsView requires the health answer of AgentsView at bind. The
// request sends the AgentsView token of the deployment at home. Each Go
// caller uses this function. The error text is operator text.
func PingAgentsView(ctx context.Context, home, bind string) error {
	base := ViewerURL(bind)
	if _, _, err := net.SplitHostPort(bind); err != nil {
		return fmt.Errorf("The AgentsView address %s is invalid.", bind)
	}
	token, err := ReadAgentsViewToken(home)
	if err != nil {
		return err
	}
	res, err := viewerGet(ctx, base+"/api/ping", token)
	if err != nil {
		return fmt.Errorf("AgentsView does not answer at %s.", base)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("AgentsView at %s refuses the AgentsView token of this deployment.", base)
	}
	if res.StatusCode != http.StatusOK {
		return markedErrorf(ErrViewerHTTPStatus, "The service at %s answers with HTTP status %d.", base, res.StatusCode)
	}
	var ping struct {
		Service string `json:"service"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&ping); err != nil || ping.Service != "agentsview" {
		return markedErrorf(ErrAnotherViewerService, "Another service answers at the AgentsView address %s.", base)
	}
	return nil
}

// WikiServiceHeader is the response header that the dashboard wiki server
// adds to each answer, with the value WikiServiceName.
const (
	WikiServiceHeader = "X-Culler-Service"
	WikiServiceName   = "wiki"
)

// AnswerWikiAddress requires an answer from the wiki server at bind. The
// answer must carry the wiki service header. A success, a redirect, or a 404
// counts as an answer.
func AnswerWikiAddress(ctx context.Context, bind string) error {
	url := ViewerURL(bind) + "/"
	res, err := viewerGet(ctx, url, "")
	if err != nil {
		return fmt.Errorf("the dashboard did not answer at the wiki address %s: %w", url, err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	if values := res.Header.Values(WikiServiceHeader); len(values) != 1 || values[0] != WikiServiceName {
		return markedErrorf(ErrAnotherViewerService, "another service answers at the wiki address %s", url)
	}
	if (res.StatusCode >= 200 && res.StatusCode < 400) || res.StatusCode == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("the wiki address %s answered with HTTP status %d", url, res.StatusCode)
}

// ViewerProbeTimeout bounds each viewer probe of the deployment checks.
const ViewerProbeTimeout = 2 * time.Second

// AgentsViewHomeForData returns MC_HOME for an AgentsView data folder.
func AgentsViewHomeForData(data string) (string, bool) {
	if data == "" || !filepath.IsAbs(data) {
		return "", false
	}
	home := filepath.Dir(filepath.Dir(filepath.Clean(data)))
	return home, AgentsViewPathsFor(home).Data == filepath.Clean(data)
}

func agentsViewBundleMatches(root, canonicalHome string, names Names, bind string) (bool, error) {
	token, err := ReadAgentsViewToken(canonicalHome)
	if err != nil {
		return false, nil
	}
	plist, config, err := agentsViewBundle(canonicalHome, names, bind, token)
	if err != nil {
		return false, nil
	}
	for rel, want := range map[string][]byte{agentsViewPlist: plist, agentsViewConfig: config} {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return false, err
		}
		if !bytes.Equal(body, want) {
			return false, nil
		}
	}
	return true, nil
}
