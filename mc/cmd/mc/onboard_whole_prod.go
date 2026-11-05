//go:build !test_fake_routing

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"mc/deployment"
	"mc/routing"
	"mc/verbs"
)

var productionWholeRunSection func(args []string, stdin io.Reader, stdout, stderr io.Writer) int

var productionWholeRuntimeAuthReady = func(home string, bindings []string) error {
	return deployment.ValidateRuntimeGrantStore(home, bindings, os.Environ())
}

// productionWholeAcceptance runs the live acceptance of one enabled binding.
var productionWholeAcceptance = func(home, binding string) (deployment.LiveProviderResult, error) {
	return executeProviderAcceptance(home, releaseBuildID, "run", binding, "", productionProviderAcceptanceDeps())
}

// productionWholeDisableBinding removes a binding that failed the acceptance
// from the enabled list and moves its roles. It returns the moved roles.
var productionWholeDisableBinding = verbs.DisableOnboardRuntimeBinding

const wholeAcceptanceStopDetail = "the live acceptance failed for one binding; the binding stays disabled"

// wholeWantsOAuthLogin reports whether a selected agent profile needs sign-in.
func wholeWantsOAuthLogin(bindings []string) bool {
	if home, err := configuredCanonicalHome(); err == nil {
		if catalog, err := deployment.LoadRuntimeCatalog(home); err == nil {
			wantsCodex, wantsClaude, err := catalog.OAuthKindsOf(bindings)
			if err == nil {
				return wantsCodex || wantsClaude
			}
		}
	}
	for _, binding := range bindings {
		if spec, ok := routing.ProductionBinding(binding); ok && strings.HasSuffix(spec.AuthKind, "-oauth") {
			return true
		}
	}
	return false
}

func wholeSectionArgs(section string, a verbs.OnboardArgs) []string {
	args := []string{"__onboard", section}
	switch section {
	case "home":
		if a.RestoreLatest {
			args = append(args, "--restore-latest")
		}
		if a.ReleaseSource != "" {
			args = append(args, "--release-source", a.ReleaseSource)
		}
		if a.HostReleaseSource != "" {
			args = append(args, "--host-release-source", a.HostReleaseSource)
		}
	case "runtime-auth":
		if a.RuntimeBindings != "" {
			args = append(args, "--runtime-bindings", a.RuntimeBindings)
		}
		if a.AcquireRuntimeAuth {
			args = append(args, "--acquire")
		}
	case "routing":
		if a.RuntimeBindings != "" {
			args = append(args, "--runtime-bindings", a.RuntimeBindings)
		}
		if a.AcceptDefaultRouting {
			args = append(args, "--accept-default-routing")
		}
		if a.RoutingFile != "" {
			args = append(args, "--routing-file", a.RoutingFile)
		}
	case "project":
		if a.Project != "" {
			args = append(args, "--project", a.Project)
		}
		if a.WorkspaceRoot != "" {
			args = append(args, "--workspace-root", a.WorkspaceRoot)
		}
		if a.ProjectVisionAnswer != "" {
			args = append(args, "--project-vision", a.ProjectVisionAnswer)
		}
		if a.ProjectGitAuthorName != "" {
			args = append(args,
				"--project-git-author-name", a.ProjectGitAuthorName,
				"--project-git-author-email", a.ProjectGitAuthorEmail)
		}
		if a.AcceptGeneratedSandbox {
			args = append(args, "--accept-generated-sandbox")
		}
		if a.ProjectTitle != "" {
			args = append(args, "--title", a.ProjectTitle)
		}
		if a.ProjectPaused {
			args = append(args, "--paused")
		}
		if a.ProjectVisionSetup {
			args = append(args, "--project-vision-setup")
		}
	case "tunables":
		if a.ReviewWIPCap != 0 {
			args = append(args, "--review-wip-cap", fmt.Sprint(a.ReviewWIPCap))
		}
		for _, item := range []struct {
			name  string
			value int
		}{
			{"timeout-minutes", a.TimeoutMinutes}, {"grace-minutes", a.GraceMinutes},
			{"heartbeat-interval-s", a.HeartbeatIntervalS}, {"spawn-grace-s", a.SpawnGraceS},
			{"hard-deadline-minutes", a.HardDeadlineMinutes},
			{"homie-idle-timeout-s", a.HomieIdleTimeoutS},
		} {
			if item.value != 0 {
				args = append(args, "--"+item.name, fmt.Sprint(item.value))
			}
		}
		if a.TunablesConfirmed {
			args = append(args, "--accept-default-tunables")
		}
		if a.AcceptDefaultGitIdentity {
			args = append(args, "--accept-default-git-identity")
		}
		if a.GitAuthorName != "" {
			args = append(args, "--git-author-name", a.GitAuthorName,
				"--git-author-email", a.GitAuthorEmail)
		}
	case "surfaces":
		if a.AcceptDefaultDashboardBind {
			args = append(args, "--accept-default-dashboard-bind")
		}
		if a.DashboardBind != "" {
			args = append(args, "--dashboard-bind", a.DashboardBind)
		}
		if a.DashboardAuthTokenFile != "" {
			args = append(args, "--dashboard-auth-token-file", a.DashboardAuthTokenFile)
		}
		if a.DiscordMode != "" {
			args = append(args, "--discord-mode", a.DiscordMode)
		}
		if a.DiscordBotTokenFile != "" {
			args = append(args, "--discord-bot-token-file", a.DiscordBotTokenFile,
				"--discord-guild-id", a.DiscordGuildID,
				"--discord-channel-id", a.DiscordChannelID,
				"--discord-operator-ids", a.DiscordOperatorIDs)
		}
		if a.DiscordTestConfirmed {
			args = append(args, "--confirm-discord-test")
		}
	case "supervision":
		if a.ActivateSupervision {
			args = append(args, "--activate")
		}
		if a.ReconfigureSupervision {
			args = append(args, "--reconfigure")
		}
	}
	return args
}

func appendWholeSection(rows []map[string]any, section string, a verbs.OnboardArgs, stdin io.Reader, stderr io.Writer) ([]map[string]any, int) {
	var out bytes.Buffer
	runner := productionWholeRunSection
	if runner == nil {
		runner = run
	}
	_, _ = fmt.Fprintf(stderr, "== onboard: %s\n", section)
	code := runner(wholeSectionArgs(section, a), stdin, &out, stderr)
	if code != 0 {
		return append(rows, map[string]any{
			"section": section, "status": "fail",
			"detail": fmt.Sprintf("section exited %d; diagnostics were written above", code),
		}), code
	}
	var result struct {
		OK       bool             `json:"ok"`
		Sections []map[string]any `json:"sections"`
	}
	dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
	dec.DisallowUnknownFields()
	var trailing any
	decodeErr := dec.Decode(&result)
	trailingErr := dec.Decode(&trailing)
	if decodeErr != nil || trailingErr != io.EOF || !result.OK || len(result.Sections) != 1 || result.Sections[0]["section"] != section {
		return append(rows, map[string]any{"section": section, "status": "fail", "detail": "section returned an invalid wizard receipt"}), 2
	}
	return append(rows, result.Sections[0]), 0
}

func writeWholeOnboardResult(stdout, stderr io.Writer, rows []map[string]any, stoppedAt, detail string, code int) int {
	result := map[string]any{"ok": code == 0, "sections": rows}
	if stoppedAt != "" {
		result["stopped_at"] = stoppedAt
		result["detail"] = detail
	}
	if err := writeJSON(stdout, result); err != nil {
		fmt.Fprintln(stderr, "mc:", err)
		return 2
	}
	if detail != "" {
		fmt.Fprintln(stderr, "mc:", detail)
	}
	return code
}

func validateWholeOnboardChoices(a verbs.OnboardArgs) ([]string, error) {
	bindings, err := selectedRuntimeBindings(a.RuntimeBindings)
	if err != nil && a.RuntimeBindings != "" {
		return nil, err
	}
	if err != nil {
		return nil, verbs.Usagef(
			"whole onboarding requires the explicit --runtime-bindings choice; one binding is enough")
	}
	if a.AcceptDefaultRouting == (a.RoutingFile != "") {
		return nil, verbs.Usagef(
			"whole onboarding requires exactly one of --accept-default-routing or --routing-file")
	}
	hasTunableChoice := a.TunablesConfirmed || a.ReviewWIPCap > 0 ||
		a.TimeoutMinutes > 0 || a.GraceMinutes > 0 ||
		a.HeartbeatIntervalS > 0 || a.SpawnGraceS > 0 ||
		a.HardDeadlineMinutes > 0
	if !hasTunableChoice {
		return nil, verbs.Usagef(
			"whole onboarding requires --accept-default-tunables or explicit tunable overrides")
	}
	if !a.AcceptDefaultGitIdentity && (a.GitAuthorName == "" || a.GitAuthorEmail == "") {
		return nil, verbs.Usagef(
			"whole onboarding requires --accept-default-git-identity or explicit Git author name/email")
	}
	if a.AcceptDefaultDashboardBind == (a.DashboardBind != "") {
		return nil, verbs.Usagef(
			"whole onboarding requires exactly one of --accept-default-dashboard-bind or --dashboard-bind")
	}
	if a.DiscordMode == "" && !publishedSurfacesConfigured() {
		return nil, verbs.Usagef(
			"whole onboarding requires the explicit --discord-mode disabled|enabled choice")
	}
	if (a.Project != "" || a.WorkspaceRoot != "" || a.AcceptGeneratedSandbox) &&
		(a.Project == "" || a.WorkspaceRoot == "" || !a.AcceptGeneratedSandbox) {
		return nil, verbs.Usagef("Project setup requires a name, workspace, and confirmed access.")
	}
	if err := verbs.CheckOnboardRuntimeBindings(strings.Join(bindings, ",")); err != nil {
		return nil, err
	}
	return bindings, nil
}

func brokerWholeOnboard(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	a, err := parseOnboardArgs(args[1:])
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if a.Section != "" {
		return writeVerbError(stdout, stderr, verbs.Usagef("whole onboarding received an unexpected section"))
	}
	if a.Smoke {
		if a.Project != "" || a.WorkspaceRoot != "" || a.ProjectVisionAnswer != "" ||
			a.ProjectGitAuthorName != "" || a.ProjectGitAuthorEmail != "" ||
			a.AcceptGeneratedSandbox || a.ProjectTitle != "" || a.ProjectPaused || a.ProjectVisionSetup ||
			a.RuntimeBindings != "" ||
			a.ConfirmLiveSpend || a.AcquireRuntimeAuth ||
			a.AcceptDefaultRouting || a.RoutingFile != "" ||
			a.ActivateSupervision || a.RestoreLatest || a.ReleaseSource != "" ||
			a.HostReleaseSource != "" || a.TimeoutMinutes != 0 ||
			a.GraceMinutes != 0 || a.HeartbeatIntervalS != 0 ||
			a.SpawnGraceS != 0 || a.HardDeadlineMinutes != 0 ||
			a.ReviewWIPCap != 0 || a.TunablesConfirmed ||
			a.AcceptDefaultGitIdentity ||
			a.GitAuthorName != "" || a.GitAuthorEmail != "" ||
			a.AcceptDefaultDashboardBind || a.DashboardBind != "" ||
			a.DashboardAuthTokenFile != "" || a.DiscordMode != "" ||
			a.DiscordBotTokenFile != "" || a.DiscordGuildID != "" ||
			a.DiscordChannelID != "" || a.DiscordOperatorIDs != "" ||
			a.DiscordTestConfirmed {
			return writeVerbError(stdout, stderr, verbs.Usagef(
				"mc __onboard --smoke may be combined only with --smoke-approve-task or --smoke-reset"))
		}
		return brokerOnboardSmoke(a, stdin, stdout, stderr)
	}
	bindings, err := validateWholeOnboardChoices(a)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	a.RuntimeBindings = strings.Join(bindings, ",")
	rows := []map[string]any{}
	for _, section := range []string{"preflight", "home"} {
		var code int
		rows, code = appendWholeSection(rows, section, a, stdin, stderr)
		if code != 0 {
			return writeWholeOnboardResult(stdout, stderr, rows, section, "onboarding stopped at the failing section", code)
		}
	}

	hasRuntimeInput := a.AcquireRuntimeAuth
	runtimeReady := false
	if !hasRuntimeInput {
		home, homeErr := configuredCanonicalHome()
		runtimeReady = homeErr == nil && productionWholeRuntimeAuthReady(home, bindings) == nil
	}
	if !hasRuntimeInput && !runtimeReady && productionRuntimeAuthInteractive(stdin) {
		hasRuntimeInput = true
		a.AcquireRuntimeAuth = wholeWantsOAuthLogin(bindings)
	}
	if hasRuntimeInput {
		var code int
		rows, code = appendWholeSection(rows, "runtime-auth", a, stdin, stderr)
		if code != 0 {
			return writeWholeOnboardResult(stdout, stderr, rows, "runtime-auth", "runtime authentication did not pass its live gates", code)
		}
	} else {
		if !runtimeReady {
			detail := "runtime authentication requires sign-in for each enabled agent profile; Culler sent no provider request"
			rows = append(rows, map[string]any{"section": "runtime-auth", "status": "needs-input", "detail": detail})
			return writeWholeOnboardResult(stdout, stderr, rows, "runtime-auth", detail, 1)
		}
		rows = append(rows, map[string]any{
			"section": "runtime-auth", "status": "ok",
			"detail": "canonical grant set and forbidden environment are structurally healthy; no replay token was spent",
		})
	}

	// Acceptance uses the resident token service after service activation.
	home, err := configuredCanonicalHome()
	if err != nil {
		rows = append(rows, map[string]any{
			"section": "acceptance", "status": "fail", "detail": fmt.Sprintf("resolve MC_HOME: %v", err),
		})
		return writeWholeOnboardResult(stdout, stderr, rows, "acceptance", "onboarding stopped at the failing section", 2)
	}
	for _, section := range []string{"routing", "container", "project", "tunables", "surfaces", "supervision"} {
		var code int
		rows, code = appendWholeSection(rows, section, a, stdin, stderr)
		if code != 0 {
			return writeWholeOnboardResult(stdout, stderr, rows, section, "onboarding stopped at the failing section", code)
		}
	}
	if !a.ActivateSupervision {
		detail := "supervision is prepared and verified unloaded; activation and real-tick observation require the operator-present --activate gate"
		rows[len(rows)-1]["status"] = "needs-operator"
		rows[len(rows)-1]["detail"] = detail
		return writeWholeOnboardResult(stdout, stderr, rows, "supervision", detail, 1)
	}
	rows, code := appendWholeSection(rows, "verify", a, stdin, stderr)
	if code != 0 {
		return writeWholeOnboardResult(stdout, stderr, rows, "verify", "final verification failed", code)
	}
	rows, code = appendWholeAcceptance(rows, home, bindings, a.ConfirmLiveSpend, stdin, stderr)
	if code != 0 {
		return writeWholeOnboardResult(stdout, stderr, rows, "acceptance", wholeAcceptanceStopDetail, code)
	}
	return writeWholeOnboardResult(stdout, stderr, rows, "", "", 0)
}

// appendWholeAcceptance runs the live acceptance for each listed binding.
// Each run needs the operator's spend confirmation: the --confirm-live-spend
// flag, or an interactive yes. A binding without confirmation is skipped and
// spends nothing. A binding that fails leaves the enabled list at once; the
// roles the routing table maps to it move to the first enabled binding. The
// run continues through the list and returns 1 when one binding failed.
func appendWholeAcceptance(rows []map[string]any, home string, bindings []string, confirmed bool, stdin io.Reader, stderr io.Writer) ([]map[string]any, int) {
	interactive := productionRuntimeAuthInteractive(stdin)
	failed := false
	for _, binding := range bindings {
		_, _ = fmt.Fprintf(stderr, "== onboard: acceptance %s\n", binding)
		run := confirmed
		if !run && interactive {
			answer, err := productionLiveSpendPrompt(binding, stdin, stderr)
			if err != nil {
				return append(rows, map[string]any{
					"section": "acceptance", "binding": binding, "status": "fail", "detail": err.Error(),
				}), 2
			}
			run = answer
		}
		if !run {
			rows = append(rows, map[string]any{
				"section": "acceptance", "binding": binding, "status": "skipped",
				"detail": "the live acceptance needs --confirm-live-spend or an interactive yes; no provider turn was spent",
			})
			continue
		}
		result, err := productionWholeAcceptance(home, binding)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "mc: live acceptance for binding %s: %v\n", binding, err)
			moved, disableErr := productionWholeDisableBinding(binding)
			if disableErr != nil {
				return append(rows, map[string]any{
					"section": "acceptance", "binding": binding, "status": "fail",
					"detail": fmt.Sprintf("the live acceptance did not pass: %v; the binding could not leave the enabled list: %v", err, disableErr),
				}), 2
			}
			detail := fmt.Sprintf("the live acceptance did not pass: %v; the binding stays disabled", err)
			if len(moved) != 0 {
				detail += "; roles moved to the first enabled binding: " + strings.Join(moved, ", ")
			}
			rows = append(rows, map[string]any{
				"section": "acceptance", "binding": binding, "status": "fail", "detail": detail,
			})
			failed = true
			continue
		}
		rows = append(rows, map[string]any{
			"section": "acceptance", "binding": binding, "status": "ok",
			"detail": fmt.Sprintf("live acceptance %s as attempt %s", result.Status, result.AttemptID),
		})
	}
	if failed {
		return rows, 1
	}
	return rows, 0
}
