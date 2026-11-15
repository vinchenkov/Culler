//go:build !test_fake_routing

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mc/onboarding"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mc/deployment"
	"mc/substrate"
	"mc/verbs"
)

func brokerDoctor(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return writeVerbError(stdout, stderr, verbs.Usagef("usage: mc doctor"))
	}
	result, err := productionDoctorResult()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := writeJSON(stdout, result); err != nil {
		fmt.Fprintln(stderr, "mc:", err)
		return 2
	}
	return 0
}

// newSupervisionFinding starts the host supervision finding in the shape
// ComposeDoctor accepts: the check rides no onboard section, because its
// repair is release application through the front doors, not a setup section.
func newSupervisionFinding() verbs.DoctorFinding {
	return verbs.DoctorFinding{Check: "supervision", Status: "fail"}
}

func productionDoctorResult() (out any, resultErr error) {
	started := time.Now().UTC().Format(time.RFC3339Nano)
	defer func() {
		if resultErr == nil {
			out = composeSystemDoctor(out, started)
		}
	}()
	return productionDoctorWithHelper(func() (string, error) { return helperContainerName(), nil }, true)
}

func productionDoctorWithHelper(resolveHelper func() (string, error), inspectVision bool) (any, error) {
	host, err := verbs.DoctorHostFacts()
	if err != nil {
		return nil, err
	}
	supervision := newSupervisionFinding()
	home, homeErr := configuredCanonicalHome()
	userHome, userHomeErr := os.UserHomeDir()
	switch {
	case homeErr != nil:
		supervision.Detail = "not checked: MC_HOME unresolved: " + homeErr.Error()
	case userHomeErr != nil:
		supervision.Detail = "not checked: operator home unresolved: " + userHomeErr.Error()
	default:
		active, inspectErr := deployment.InspectActiveSupervision(
			home, filepath.Join(userHome, "Library", "LaunchAgents"), releaseBuildID,
			launchdController{}, time.Now(),
		)
		if inspectErr != nil {
			supervision.Detail = inspectErr.Error()
		} else {
			supervision.Status = "ok"
			supervision.Detail = fmt.Sprintf("%s, %s, and %s loaded; recent release-bound resident tick observed",
				active.ResidentLabel, active.DashboardLabel, active.AgentsViewLabel)
		}
	}
	viewers := verbs.ViewerDoctorFindings(home, homeErr, productionViewerProbes(userHome, userHomeErr, launchdController{}))
	for i := range host {
		switch host[i].Check {
		case "supervision":
			host[i] = supervision
		case "agentsview":
			host[i] = viewers[0]
		case "wiki":
			host[i] = viewers[1]
		}
	}
	req := verbs.DoctorRuntimeRequest{
		ProtocolVersion: 1, ReleaseBuildID: releaseBuildID,
		ControlVersion: gatewayControlVersion, SpineSchemaID: substrate.CurrentSchemaID,
		ConfigSchemaVersion: configSchemaVersion,
	}
	frame, err := json.Marshal(req)
	if err != nil {
		return nil, verbs.Usagef("encode private doctor-runtime frame: %v", err)
	}
	helper, err := resolveHelper()
	if err != nil {
		return verbs.ComposeDoctor(host, verbs.DoctorRuntimeUnavailable(req, "warm helper unavailable: "+err.Error()))
	}
	privateOut := boundedDoctorBuffer{limit: privateDoctorOutputLimit}
	privateErr := boundedDoctorBuffer{limit: privateDoctorOutputLimit}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", helper, "mc", "__doctor-runtime")
	cmd.Stdin = bytes.NewReader(frame)
	cmd.Stdout = &privateOut
	cmd.Stderr = &privateErr
	if err := cmd.Run(); err != nil {
		return verbs.ComposeDoctor(host, verbs.DoctorRuntimeUnavailable(req, "helper capability crossing failed: "+err.Error()+": "+doctorRuntimeDiagnostic(privateOut.Bytes(), privateErr.Bytes())))
	}
	if privateOut.overflow || privateErr.overflow {
		return verbs.ComposeDoctor(host, verbs.DoctorRuntimeUnavailable(req, "helper capability report exceeded its bound"))
	}
	var runtimeReport verbs.DoctorRuntimeReport
	dec := json.NewDecoder(bytes.NewReader(privateOut.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&runtimeReport); err != nil {
		return verbs.ComposeDoctor(host, verbs.DoctorRuntimeUnavailable(req, "helper returned an invalid capability report"))
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return verbs.ComposeDoctor(host, verbs.DoctorRuntimeUnavailable(req, "helper returned an invalid capability report"))
	}
	if runtimeReport.ProtocolVersion != req.ProtocolVersion || runtimeReport.ReleaseBuildID != req.ReleaseBuildID ||
		runtimeReport.ControlVersion != req.ControlVersion || runtimeReport.SpineSchemaID != req.SpineSchemaID ||
		runtimeReport.ConfigSchemaVersion != req.ConfigSchemaVersion {
		return verbs.ComposeDoctor(host, verbs.DoctorRuntimeUnavailable(req, fmt.Sprintf("helper returned mismatched build/schema identity: expected protocol=%d release=%s control=%d schema=%s config=%d; observed protocol=%d release=%s control=%d schema=%s config=%d", req.ProtocolVersion, req.ReleaseBuildID, req.ControlVersion, req.SpineSchemaID, req.ConfigSchemaVersion, runtimeReport.ProtocolVersion, runtimeReport.ReleaseBuildID, runtimeReport.ControlVersion, runtimeReport.SpineSchemaID, runtimeReport.ConfigSchemaVersion)))
	}
	result, err := verbs.ComposeDoctor(host, runtimeReport)
	if err != nil {
		return verbs.ComposeDoctor(host, verbs.DoctorRuntimeUnavailable(req, "helper returned an invalid capability report"))
	}
	if !inspectVision {
		return result, nil
	}
	listed, err := readHostProjectList()
	if err != nil {
		verbs.ApplyDoctorVisionUnavailable(result)
		return result, nil
	}
	verbs.ApplyDoctorProjectVision(result, verbs.InspectProjectListVision(listed))
	return result, nil
}

func productionViewerProbes(userHome string, userHomeErr error, launchd deployment.LaunchdController) verbs.ViewerDoctorProbes {
	return verbs.ViewerDoctorProbes{
		InspectAgentsView: func(home string) (string, error) {
			if userHomeErr != nil {
				return "", fmt.Errorf("operator home unresolved: %w", userHomeErr)
			}
			return deployment.InspectAgentsViewUnit(home, filepath.Join(userHome, "Library", "LaunchAgents"), launchd)
		},
		PingAgentsView: func(home, bind string) error {
			ctx, cancel := context.WithTimeout(context.Background(), deployment.ViewerProbeTimeout)
			defer cancel()
			return deployment.PingAgentsView(ctx, home, bind)
		},
		AnswerWiki: func(bind string) error {
			ctx, cancel := context.WithTimeout(context.Background(), deployment.ViewerProbeTimeout)
			defer cancel()
			return deployment.AnswerWikiAddress(ctx, bind)
		},
	}
}

func doctorRuntimeDiagnostic(output, diagnostic []byte) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(output, &envelope) == nil && envelope.Error.Message != "" {
		return onboarding.Diagnostic(envelope.Error.Message)
	}
	return onboarding.Diagnostic(strings.TrimSpace(string(diagnostic)))
}

func doctorFindings(result any) ([]verbs.DoctorFinding, error) {
	report, ok := result.(map[string]any)
	if !ok {
		return nil, verbs.Usagef("The service checks returned an incomplete result.")
	}
	body, err := json.Marshal(report["findings"])
	if err != nil {
		return nil, err
	}
	var findings []verbs.DoctorFinding
	if err := json.Unmarshal(body, &findings); err != nil || len(findings) == 0 {
		return nil, verbs.Usagef("The service checks returned an incomplete result.")
	}
	return findings, nil
}
