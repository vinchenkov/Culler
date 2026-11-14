//go:build !test_fake_routing

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"mc/deployment"
	"mc/verbs"
)

// withWholeWizardFakes supplies supported connections and isolates each host operation.
func withWholeWizardFakes(t *testing.T, authReady bool) *[]string {
	t.Helper()
	home := t.TempDir()
	writeEndpointFixtureConfig(t, home)
	t.Setenv("MC_HOME", home)
	originalRun := productionWholeRunSection
	originalAuth := productionWholeRuntimeAuthReady
	originalInteractive := productionRuntimeAuthInteractive
	originalAcceptance := productionWholeAcceptance
	originalSpendPrompt := productionLiveSpendPrompt

	originalDisable := productionWholeDisableBinding
	t.Cleanup(func() {
		productionWholeRunSection = originalRun
		productionWholeRuntimeAuthReady = originalAuth
		productionRuntimeAuthInteractive = originalInteractive
		productionWholeAcceptance = originalAcceptance
		productionLiveSpendPrompt = originalSpendPrompt

		productionWholeDisableBinding = originalDisable
	})
	calls := []string{}
	productionWholeAcceptance = func(_, binding string) (deployment.LiveProviderResult, error) {
		calls = append(calls, "acceptance "+binding)
		return deployment.LiveProviderResult{Status: "verified", Binding: binding, AttemptID: strings.Repeat("a", 64)}, nil
	}
	productionWholeDisableBinding = func(binding string) ([]string, error) {
		calls = append(calls, "disable "+binding)
		return []string{"strategist", "worker"}, nil
	}
	productionLiveSpendPrompt = func(string, io.Reader, io.Writer) (bool, error) {
		return false, errors.New("the spend prompt must not run without a terminal")
	}
	productionWholeRuntimeAuthReady = func(string, []string) error {
		if authReady {
			return nil
		}
		return errors.New("no canonical grants")
	}
	productionWholeRunSection = func(args []string, _ io.Reader, stdout, _ io.Writer) int {
		section := args[1]
		calls = append(calls, strings.Join(args[1:], " "))
		_ = json.NewEncoder(stdout).Encode(map[string]any{
			"ok":       true,
			"sections": []map[string]any{{"section": section, "status": "ok", "detail": "fixture"}},
		})
		return 0
	}
	return &calls
}

func wholeResult(t *testing.T, args ...string) (int, map[string]any, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := brokerWholeOnboard(append([]string{"onboard"}, args...), strings.NewReader(""), &stdout, &stderr)
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode whole result %q: %v", stdout.String(), err)
	}
	return code, result, stderr.String()
}

func wholeCoreResult(t *testing.T, args ...string) (int, map[string]any, string) {
	t.Helper()
	core := []string{
		"--runtime-bindings", "chatgpt,claude",
		"--accept-default-routing", "--accept-default-tunables",
		"--accept-default-git-identity", "--accept-default-dashboard-bind",
		"--discord-mode", "disabled",
		"--project", "primary", "--workspace-root", "/workspace",
		"--accept-generated-sandbox",
	}
	return wholeResult(t, append(core, args...)...)
}

func TestWholeWizardInteractiveMissingAuthEntersProviderAcquisition(t *testing.T) {
	calls := withWholeWizardFakes(t, false)
	productionRuntimeAuthInteractive = func(io.Reader) bool { return true }
	productionLiveSpendPrompt = func(string, io.Reader, io.Writer) (bool, error) { return false, nil }
	code, result, _ := wholeCoreResult(t)
	if code != 1 || result["stopped_at"] != "supervision" {
		t.Fatalf("code=%d result=%v", code, result)
	}
	if got := strings.Join((*calls)[:3], "|"); got != "preflight|home|runtime-auth --runtime-bindings chatgpt,claude --acquire" {
		t.Fatalf("initial sections=%v", *calls)
	}
}

func TestWholeWizardPreservesAndStreamsInteractiveSectionStderr(t *testing.T) {
	originalRun := productionWholeRunSection
	t.Cleanup(func() { productionWholeRunSection = originalRun })

	var outer bytes.Buffer
	productionWholeRunSection = func(args []string, _ io.Reader, stdout, stderr io.Writer) int {
		if stderr != &outer {
			return 90
		}
		_, _ = io.WriteString(stderr, "Complete sign-in in your browser. ")
		if !strings.Contains(outer.String(), "Complete sign-in in your browser.") {
			return 91
		}
		_ = json.NewEncoder(stdout).Encode(map[string]any{
			"ok": true,
			"sections": []map[string]any{{
				"section": args[1], "status": "ok", "detail": "fixture",
			}},
		})
		return 0
	}

	rows, code := appendWholeSection(
		nil, "runtime-auth", verbs.OnboardArgs{}, strings.NewReader(""), &outer)
	if code != 0 || len(rows) != 1 {
		t.Fatalf("code=%d rows=%v stderr=%q", code, rows, outer.String())
	}
	if !strings.Contains(outer.String(), "== onboard: runtime-auth\n") {
		t.Fatalf("section progress missing from stderr=%q", outer.String())
	}
	if got := strings.Count(outer.String(), "Complete sign-in in your browser."); got != 1 {
		t.Fatalf("prompt count=%d stderr=%q", got, outer.String())
	}
}

func TestWholeWizardStopsAtRuntimeAuthWithoutSpending(t *testing.T) {
	calls := withWholeWizardFakes(t, false)
	code, result, stderr := wholeCoreResult(t)
	if code != 1 || result["stopped_at"] != "runtime-auth" || !strings.Contains(stderr, "Culler sent no provider request") {
		t.Fatalf("code=%d result=%v stderr=%q", code, result, stderr)
	}
	if strings.Join(*calls, "|") != "preflight|home" {
		t.Fatalf("sections called after auth gate: %v", *calls)
	}
	rows := result["sections"].([]any)
	if rows[2].(map[string]any)["status"] != "needs-input" {
		t.Fatalf("runtime row=%v", rows[2])
	}
}

func TestWholeWizardPreparesButNeverImplicitlyActivates(t *testing.T) {
	calls := withWholeWizardFakes(t, true)
	code, result, _ := wholeResult(t,
		"--restore-latest",
		"--runtime-bindings", "chatgpt,claude",
		"--accept-default-routing", "--accept-default-tunables",
		"--accept-default-git-identity", "--accept-default-dashboard-bind",
		"--discord-mode", "disabled",
		"--project", "primary", "--workspace-root", "/workspace",
		"--accept-generated-sandbox")
	if code != 1 || result["stopped_at"] != "supervision" {
		t.Fatalf("code=%d result=%v", code, result)
	}
	want := "preflight|home --restore-latest|routing --runtime-bindings chatgpt,claude --accept-default-routing|container|project --project primary --workspace-root /workspace --accept-generated-sandbox|tunables --accept-default-tunables --accept-default-git-identity|surfaces --accept-default-dashboard-bind --discord-mode disabled|supervision"
	if strings.Join(*calls, "|") != want {
		t.Fatalf("calls=%v", *calls)
	}
	rows := result["sections"].([]any)
	if rows[len(rows)-1].(map[string]any)["status"] != "needs-operator" {
		t.Fatalf("supervision row=%v", rows[len(rows)-1])
	}
}

func TestWholeWizardForwardsExplicitRoutingAndTunableOverrides(t *testing.T) {
	calls := withWholeWizardFakes(t, true)
	code, result, _ := wholeResult(t,
		"--runtime-bindings", "chatgpt,claude",
		"--routing-file", "/answers/routing.md",
		"--project", "primary", "--workspace-root", "/workspace",
		"--project-git-author-name", "Primary Bot",
		"--project-git-author-email", "primary@example.test",
		"--accept-generated-sandbox",
		"--accept-default-git-identity", "--accept-default-dashboard-bind",
		"--discord-mode", "disabled",
		"--review-wip-cap", "5", "--timeout-minutes", "45",
		"--grace-minutes", "10", "--heartbeat-interval-s", "30",
		"--spawn-grace-s", "40", "--activate")
	if code != 0 || result["ok"] != true {
		t.Fatalf("code=%d result=%v", code, result)
	}
	got := strings.Join(*calls, "|")
	if !strings.Contains(got,
		"routing --runtime-bindings chatgpt,claude --routing-file /answers/routing.md") {
		t.Fatalf("routing args missing from calls=%v", *calls)
	}
	if !strings.Contains(got,
		"project --project primary --workspace-root /workspace "+
			"--project-git-author-name Primary Bot "+
			"--project-git-author-email primary@example.test --accept-generated-sandbox") {
		t.Fatalf("Project Git identity args missing from calls=%v", *calls)
	}
	if !strings.Contains(got,
		"tunables --review-wip-cap 5 --timeout-minutes 45 --grace-minutes 10 --heartbeat-interval-s 30 --spawn-grace-s 40 --accept-default-git-identity") {
		t.Fatalf("tunable args missing from calls=%v", *calls)
	}
}

func TestWholeWizardExplicitActivationContinuesToVerify(t *testing.T) {
	calls := withWholeWizardFakes(t, true)
	code, result, stderr := wholeCoreResult(t, "--activate")
	if code != 0 || result["ok"] != true ||
		!strings.Contains(stderr, "== onboard: routing\n") ||
		!strings.Contains(stderr, "== onboard: verify\n") {
		t.Fatalf("code=%d result=%v stderr=%q", code, result, stderr)
	}
	if got := (*calls)[len(*calls)-2:]; strings.Join(got, "|") != "supervision --activate|verify" {
		t.Fatalf("tail calls=%v", got)
	}
	rows := result["sections"].([]any)
	last := rows[len(rows)-1].(map[string]any)
	if len(rows) != 12 || rows[3].(map[string]any)["section"] != "routing" ||
		last["section"] != "acceptance" || last["binding"] != "claude" ||
		last["status"] != "skipped" || !strings.Contains(last["detail"].(string), "--confirm-live-spend") {
		t.Fatalf("unconfirmed acceptance rows=%v", rows)
	}
}

func TestWholeWizardConfirmedLiveSpendRunsTheAcceptanceForEachEnabledBinding(t *testing.T) {
	calls := withWholeWizardFakes(t, true)
	code, result, stderr := wholeCoreResult(t, "--activate", "--confirm-live-spend")
	if code != 0 || result["ok"] != true || !strings.Contains(stderr, "== onboard: acceptance claude\n") {
		t.Fatalf("code=%d result=%v stderr=%q", code, result, stderr)
	}
	if got := strings.Join((*calls)[:3], "|"); got != "preflight|home|routing --runtime-bindings chatgpt,claude --accept-default-routing" {
		t.Fatalf("Head calls = %v", *calls)
	}
	if got := (*calls)[len(*calls)-3:]; strings.Join(got, "|") != "verify|acceptance chatgpt|acceptance claude" {
		t.Fatalf("tail calls=%v", got)
	}
	rows := result["sections"].([]any)
	if row := rows[len(rows)-1].(map[string]any); row["status"] != "ok" || row["binding"] != "claude" {
		t.Fatalf("acceptance row=%v", row)
	}
}

func TestWholeWizardStopsWhenOneAcceptanceFails(t *testing.T) {
	calls := withWholeWizardFakes(t, true)
	productionWholeAcceptance = func(_, binding string) (deployment.LiveProviderResult, error) {
		*calls = append(*calls, "acceptance "+binding)
		if binding == "claude" {
			return deployment.LiveProviderResult{}, errors.New("provider refused the tool turn")
		}
		return deployment.LiveProviderResult{Status: "verified", Binding: binding}, nil
	}
	code, result, stderr := wholeCoreResult(t, "--activate", "--confirm-live-spend")
	if code != 1 || result["stopped_at"] != "acceptance" || !strings.Contains(stderr, "provider refused the tool turn") {
		t.Fatalf("code=%d result=%v stderr=%q", code, result, stderr)
	}
	// The failed binding leaves the enabled list at once and its roles move.
	if got := (*calls)[len(*calls)-3:]; strings.Join(got, "|") != "acceptance chatgpt|acceptance claude|disable claude" {
		t.Fatalf("tail calls=%v", got)
	}
	rows := result["sections"].([]any)
	row := rows[len(rows)-1].(map[string]any)
	if row["binding"] != "claude" || row["status"] != "fail" ||
		!strings.Contains(row["detail"].(string), "the binding stays disabled") ||
		!strings.Contains(row["detail"].(string), "roles moved to the first enabled binding: strategist, worker") {
		t.Fatalf("failed acceptance row=%v", row)
	}
}

func TestWholeWizardStopsWhenAFailedBindingCannotLeaveTheEnabledList(t *testing.T) {
	calls := withWholeWizardFakes(t, true)
	productionWholeAcceptance = func(_, binding string) (deployment.LiveProviderResult, error) {
		*calls = append(*calls, "acceptance "+binding)
		return deployment.LiveProviderResult{}, errors.New("provider refused the tool turn")
	}
	productionWholeDisableBinding = func(string) ([]string, error) {
		return nil, errors.New("config.toml is locked")
	}
	code, result, _ := wholeCoreResult(t, "--activate", "--confirm-live-spend")
	if code != 2 || result["stopped_at"] != "acceptance" {
		t.Fatalf("code=%d result=%v", code, result)
	}
	rows := result["sections"].([]any)
	row := rows[len(rows)-1].(map[string]any)
	if !strings.Contains(row["detail"].(string), "could not leave the enabled list: config.toml is locked") {
		t.Fatalf("row=%v", row)
	}
}

func TestWholeWizardInteractiveSpendPromptGatesEachAcceptance(t *testing.T) {
	calls := withWholeWizardFakes(t, true)
	productionRuntimeAuthInteractive = func(io.Reader) bool { return true }
	asked := []string{}
	productionLiveSpendPrompt = func(binding string, _ io.Reader, _ io.Writer) (bool, error) {
		asked = append(asked, binding)
		return binding == "claude", nil
	}
	code, result, _ := wholeCoreResult(t, "--activate")
	if code != 0 || result["ok"] != true {
		t.Fatalf("code=%d result=%v", code, result)
	}
	if strings.Join(asked, ",") != "chatgpt,claude" || (*calls)[len(*calls)-1] != "acceptance claude" {
		t.Fatalf("asked=%v calls=%v", asked, *calls)
	}
}

func TestWholeWizardRefusesAnUndefinedRuntimeBindingBeforeAnySection(t *testing.T) {
	calls := withWholeWizardFakes(t, true)
	const refusal = `--runtime-bindings names "third-endpoint", an undefined binding. The defined bindings are: `
	for _, choice := range []struct {
		extra   []string
		defined string
	}{
		{[]string{"--runtime-bindings", "chatgpt,third-endpoint"}, "chatgpt, claude, endpoint. Use one of these ids."},
	} {
		args := append(choice.extra, "--accept-default-routing", "--accept-default-tunables",
			"--accept-default-git-identity", "--accept-default-dashboard-bind",
			"--discord-mode", "disabled")
		code, result, _ := wholeResult(t, args...)
		errorBody, _ := result["error"].(map[string]any)
		if code != 2 || errorBody["message"] != refusal+choice.defined {
			t.Fatalf("args %v: code=%d result=%v", choice.extra, code, result)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("sections ran for an undefined binding: %v", *calls)
	}
}

func TestWholeWizardRefusesAnEmptyRuntimeBindingIDBeforeAnySection(t *testing.T) {
	calls := withWholeWizardFakes(t, true)
	code, result, _ := wholeResult(t, "--runtime-bindings", "chatgpt,",
		"--accept-default-routing", "--accept-default-tunables",
		"--accept-default-git-identity", "--accept-default-dashboard-bind",
		"--discord-mode", "disabled")
	errorBody, _ := result["error"].(map[string]any)
	const want = "--runtime-bindings contains an empty binding id. Remove the extra comma."
	if code != 2 || errorBody["message"] != want {
		t.Fatalf("code=%d result=%v", code, result)
	}
	if len(*calls) != 0 {
		t.Fatalf("sections ran for an empty binding id: %v", *calls)
	}
}

func TestWholeWizardRefusesImplicitCoreChoices(t *testing.T) {
	withWholeWizardFakes(t, true)
	code, result, _ := wholeResult(t)
	if code != 2 || !strings.Contains(
		result["error"].(map[string]any)["message"].(string), "--runtime-bindings") {
		t.Fatalf("code=%d result=%v", code, result)
	}
}

func TestWholeOnboardForwardsVisionChoiceWithoutDirective(t *testing.T) {
	for _, answer := range []string{"create", "later"} {
		args, err := parseOnboardArgs([]string{"--project", "primary", "--workspace-root", "/workspace", "--project-vision", answer})
		if err != nil {
			t.Fatal(err)
		}
		got := strings.Join(wholeSectionArgs("project", args), " ")
		if !strings.Contains(got, "--project-vision "+answer) || strings.Contains(got, "--directive") {
			t.Fatalf("vision choice arguments = %q", got)
		}
	}
	if _, err := parseOnboardArgs([]string{"project", "--directive", "inline vision"}); err == nil {
		t.Fatal("setup accepted an inline directive")
	}
}
