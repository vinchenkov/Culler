//go:build !test_fake_routing

package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"

	operatorconfig "mc/config"
	"mc/deployment"
	"mc/verbs"
)

// agentsViewStartWait bounds the wait for the first AgentsView answer after an
// activation. AgentsView answers only after its startup sync of the trace
// copies.
const agentsViewStartWait = 60 * time.Second

func (state *setupDeployment) normalizeChecks() {
	if state.Blocked == nil {
		state.Blocked = map[string]string{}
	}
	helperCause := ""
	for _, f := range state.Checks {
		if f.Check == "spine" && strings.HasPrefix(f.Detail, "not checked:") {
			helperCause = "container"
			if strings.Contains(f.Detail, "identity mismatch") || strings.Contains(f.Detail, "mismatched build/schema") {
				helperCause = "release"
			}
		}
	}
	if helperCause == "release" {
		state.Update = true
	}
	if helperCause != "" && state.Blocked["dashboard"] != "" {
		state.Blocked["dashboard"] = helperCause
	}
	for i := range state.Checks {
		f := &state.Checks[i]
		if f.Status == "ok" {
			continue
		}
		switch {
		case helperCause != "" && slices.Contains([]string{"spine", "deployment-identity", "surfaces", "container-runtime", "projects"}, f.Check):
			f.Status = "deferred"
			state.Blocked[f.Check] = helperCause
		case f.Check == "supervision" && strings.Contains(f.Detail, "mismatched release/schema identity"):
			f.Status = "deferred"
			state.Blocked[f.Check] = "release"
		case strings.HasPrefix(f.Detail, "not checked:") || f.Status == "deferred":
			f.Status = "deferred"
			if state.Blocked[f.Check] == "" {
				state.Blocked[f.Check] = "unavailable"
			}
		}
	}
}

func (state setupDeployment) issueLines() []string {
	lines := []string{}
	groups := map[string][]string{}
	for _, f := range state.Checks {
		label := setupCheckNames[f.Check]
		if label == "" || f.Check == "release" || f.Status == "ok" {
			continue
		}
		if f.Status == "deferred" {
			cause := state.Blocked[f.Check]
			groups[cause] = append(groups[cause], label)
			continue
		}
		causes := map[string]string{
			"mc-home":             "The deployment files require repair.",
			"spine":               "The database check failed.",
			"deployment-identity": "The database and deployment identities differ.",
			"routing":             "The agent assignments require setup.",
			"runtime-auth":        "The agent credentials require setup.",
			"container-runtime":   "The agent container check failed.",
			"surfaces":            "The dashboard alert configuration requires setup.",
			"supervision":         "The services did not pass their health check.",
			"agentsview":          "AgentsView did not pass its health check.",
			"wiki":                "The wiki address check failed.",
			"dashboard":           "The dashboard access check failed.",
		}
		lines = append(lines, label+": failed. "+causes[f.Check])
	}
	for _, cause := range []string{"release", "container", "services", "unavailable", ""} {
		labels := groups[cause]
		if len(labels) == 0 {
			continue
		}
		switch cause {
		case "release":
			lines = append(lines, "The installed release cannot run checks for the proposed release.")
		case "container":
			lines = append(lines, "Checks require the agent container.")
		case "services":
			lines = append(lines, "These checks require active services.")
		default:
			lines = append(lines, "Some checks could not run. The cause is unknown.")
		}
		lines = append(lines, "Unchecked: "+strings.Join(labels, ", ")+".")
	}
	if state.Blocked["spine"] != "" {
		lines = append(lines, "Database health: unknown.")
	}
	if state.DiagnosticPath != "" {
		lines = append(lines, "Diagnostic record for your coding harness:", state.DiagnosticPath)
	}
	return lines
}

func setupFinalReadiness(home string, args verbs.OnboardArgs) ([]string, error) {
	if services, err := setupServiceStates(home); err == nil && services["agentsview"] {
		if cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml")); err == nil {
			ping := func(ctx context.Context, bind string) error { return deployment.PingAgentsView(ctx, home, bind) }
			awaitAgentsView(cfg.Surfaces.AgentsView.Bind, ping, time.Sleep, time.Now, agentsViewStartWait)
		}
	}
	state, err := inspectSetupDeployment(home, args)
	if err != nil {
		return nil, err
	}
	return setupReadinessResult(home, state)
}

func setupReadinessResult(home string, state setupDeployment) ([]string, error) {
	state.normalizeChecks()
	if !state.complete() {
		path, err := writeSetupDiagnostic(home, "Verify setup", &state, nil)
		if err != nil {
			return nil, err
		}
		lines := state.issueLines()
		if !state.ready("release") {
			lines = append(state.Release.lines(), lines...)
		}
		for _, check := range setupRequiredChecks {
			if !slices.ContainsFunc(state.Checks, func(f verbs.DoctorFinding) bool { return f.Check == check }) {
				lines = append(lines, setupCheckNames[check]+": the check could not run.")
			}
		}
		for _, service := range setupServices {
			status := "stopped"
			if state.Services[service.unit] {
				status = "active"
			}
			lines = append(lines, service.label+": "+status+".")
		}
		return nil, &setupFailure{cause: "Setup completes after each required check passes.", details: lines, record: path}
	}
	lines := []string{"All required checks passed."}
	for _, service := range setupServices {
		status := "active"
		if state.Stops[service.unit] {
			status = "stopped by your choice"
		}
		lines = append(lines, service.label+": "+status+".")
	}
	return lines, nil
}

// awaitAgentsView returns when AgentsView answers at bind or when the limit
// passes. The readiness checks then report the result.
func awaitAgentsView(bind string, ping func(context.Context, string) error, sleep func(time.Duration),
	now func() time.Time, limit time.Duration) {
	deadline := now().Add(limit)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), deployment.ViewerProbeTimeout)
		err := ping(ctx, bind)
		cancel()
		if err == nil || !now().Before(deadline) {
			return
		}
		sleep(time.Second)
	}
}
