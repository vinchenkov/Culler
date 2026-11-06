//go:build !test_fake_routing

package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mc/deployment"
	"mc/onboarding"
)

type setupAgentPrompt struct {
	title    string
	choices  []onboarding.Choice
	selected int
}

type setupAgentUI struct {
	*setupTestUI
	prompts   []setupAgentPrompt
	inputs    []string
	stages    []int
	completed [][]string
}

func (ui *setupAgentUI) Stage(index int, title string, completed []string) {
	ui.stages = append(ui.stages, index)
	ui.completed = append(ui.completed, slices.Clone(completed))
	ui.setupTestUI.Stage(index, title, completed)
}

func (ui *setupAgentUI) Choose(prompt string, choices []onboarding.Choice, selected int) (string, error) {
	ui.prompts = append(ui.prompts, setupAgentPrompt{title: prompt, choices: slices.Clone(choices), selected: selected})
	return ui.setupTestUI.Choose(prompt, choices, selected)
}

func (ui *setupAgentUI) Input(prompt, _ string, _ bool) (string, error) {
	ui.inputs = append(ui.inputs, prompt)
	return "", onboarding.ErrExit
}

func setupAgentFixture(t *testing.T, answers ...string) (*setupWizard, *setupAgentUI) {
	t.Helper()
	w, basicUI := setupTestWizard(t, answers...)
	ui := &setupAgentUI{setupTestUI: basicUI}
	w.ui = ui
	return w, ui
}

// setupFakeSignIn replaces the connection test with check.
// An account has an identity only after its connection test passes.
func setupFakeSignIn(w *setupWizard, check func(setupConnection) error) {
	signedIn := map[string]bool{}
	w.deps.identity = func(_, credential string) (string, error) {
		if signedIn[credential] {
			return strings.Repeat("a", 64), nil
		}
		return "", nil
	}
	w.deps.connect = func(w *setupWizard, connection setupConnection) error {
		if err := check(connection); err != nil {
			return err
		}
		cfg, err := w.catalog()
		if err != nil {
			return err
		}
		signedIn[cfg.Runtimes.Bindings[connection.Binding].Credential] = true
		return nil
	}
}

// setupOperatorLogins writes a Codex login and a Claude Code login for the operator.
// The environment names these logins. The Codex and Claude Code programs on PATH record each execution.
func setupOperatorLogins(t *testing.T, w *setupWizard) (map[string]string, string) {
	t.Helper()
	logins := map[string]string{
		filepath.Join(w.operatorHome, ".codex", "auth.json"):          `{"auth_mode":"chatgpt","tokens":{"access_token":"operator-access","id_token":"operator-id","refresh_token":"operator-refresh","account_id":"operator-account"}}`,
		filepath.Join(w.operatorHome, ".claude", ".credentials.json"): `{"claudeAiOauth":{"accessToken":"operator-access","refreshToken":"operator-refresh","expiresAt":9999999999999,"scopes":["user:inference"]}}`,
	}
	for path, body := range logins {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	commands := filepath.Join(w.operatorHome, "commands")
	marker := filepath.Join(w.operatorHome, "executed")
	if err := os.Mkdir(commands, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"codex", "claude"} {
		script := "#!/bin/sh\necho " + name + " >> '" + marker + "'\nexit 1\n"
		if err := os.WriteFile(filepath.Join(commands, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", w.operatorHome)
	t.Setenv("PATH", commands)
	t.Setenv("CODEX_HOME", filepath.Join(w.operatorHome, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(w.operatorHome, ".claude"))
	return logins, marker
}

func setupAssertOperatorLoginsUnchanged(t *testing.T, logins map[string]string, marker string) {
	t.Helper()
	for path, body := range logins {
		if got, err := os.ReadFile(path); err != nil || string(got) != body {
			t.Fatalf("Setup changed the login of the operator at %s: %v", path, err)
		}
	}
	if executed, err := os.ReadFile(marker); !os.IsNotExist(err) {
		t.Fatalf("Setup executed an operator command: %q, %v", executed, err)
	}
}

func TestTerminalSetupAccountMenuOffersOnlySignInAndHealthyConnections(t *testing.T) {
	for _, healthy := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "selected"}[healthy], func(t *testing.T) {
			var answers []string
			if !healthy {
				answers = []string{":exit"}
			}
			w, ui := setupAgentFixture(t, answers...)
			logins, marker := setupOperatorLogins(t, w)
			w.deps.connect = func(*setupWizard, setupConnection) error {
				t.Fatal("the main menu started a connection test")
				return nil
			}
			if healthy {
				cfg, err := w.catalog()
				if err != nil {
					t.Fatal(err)
				}
				binding := setupBindingForKind(cfg, setupHarnessKind("codex"))
				if err := w.finishConnection(setupConnection{Binding: binding, Mode: "login"}, false, nil); err != nil {
					t.Fatal(err)
				}
			}
			err := w.agents()
			if healthy && err != nil || !healthy && !errors.Is(err, onboarding.ErrExit) {
				t.Fatal(err)
			}
			setupAssertOperatorLoginsUnchanged(t, logins, marker)
			if healthy {
				if len(ui.prompts) != 0 {
					t.Fatalf("the selected agent added a prompt: %#v", ui.prompts)
				}
				return
			}
			if len(ui.prompts) != 1 {
				t.Fatalf("the account menu added prompts: %#v", ui.prompts)
			}
			prompt := ui.prompts[0]
			ids := []string{}
			for _, choice := range prompt.choices {
				ids = append(ids, choice.ID)
			}
			want := []string{"login:codex", "login:claude-sdk", "api-key:codex", "api-key:claude-sdk", "api-key:opencode"}
			if prompt.title != "Choose how to connect" || !slices.Equal(ids, want) || prompt.selected != 0 {
				t.Fatalf("the account menu offers other choices or loses its primary action: %q", ids)
			}
		})
	}
}

func TestTerminalSetupSignInStoresTheNewLogin(t *testing.T) {
	for _, harness := range []string{"codex", "claude-sdk"} {
		t.Run(harness, func(t *testing.T) {
			w, ui := setupAgentFixture(t, "login:"+harness, "check")
			logins, marker := setupOperatorLogins(t, w)
			for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CODEX_ACCESS_TOKEN", "CODEX_API_KEY", "OPENAI_API_KEY"} {
				t.Setenv(key, "")
			}
			originalVerifier := productionRuntimeAuthVerifier
			t.Cleanup(func() { productionRuntimeAuthVerifier = originalVerifier })
			productionRuntimeAuthVerifier = deployment.RuntimeAuthVerifyFunc(func(string, string) error { return nil })
			useFakeSignIn(t)
			w.deps.connect = setupConnect
			w.deps.identity = deployment.AgentConnectionAuthorizationIdentity
			if err := w.agents(); err != nil {
				t.Fatal(err)
			}
			cfg, err := w.catalog()
			if err != nil {
				t.Fatal(err)
			}
			binding := setupBindingForKind(cfg, setupHarnessKind(harness))
			if !slices.Equal(w.session.RuntimeBindings, []string{binding}) || !w.connectionHealthy(binding) || len(ui.answers) != 0 {
				t.Fatalf("The sign-in did not select a healthy agent: %v", w.session.RuntimeBindings)
			}
			if !slices.Equal(ui.activities, []string{"Connect " + w.agentName(binding)}) {
				t.Fatalf("The sign-in shows other activities: %q", ui.activities)
			}
			grant, err := os.ReadFile(filepath.Join(w.home, "refresh-grants", cfg.Runtimes.Bindings[binding].Credential+".json"))
			if err != nil || !strings.Contains(string(grant), "credential_ref") || strings.Contains(string(grant), "refresh_token") {
				t.Fatalf("Culler did not store the new login: %v", err)
			}
			setupAssertOperatorLoginsUnchanged(t, logins, marker)
			entries, err := os.ReadDir(filepath.Join(w.home, "runtime-auth-sources"))
			if !os.IsNotExist(err) && (err != nil || len(entries) != 0) {
				t.Fatalf("The sign-in left credential copies: %v, %v", entries, err)
			}
		})
	}
}

func TestTerminalSetupUnfinishedAgentTestResumesAtTheConnectionChoices(t *testing.T) {
	w, ui := setupAgentFixture(t, ":exit")
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	healthy := setupBindingForKind(cfg, setupHarnessKind("claude-sdk"))
	unfinished := setupBindingForKind(cfg, setupHarnessKind("codex"))
	if err := w.finishConnection(setupConnection{Binding: healthy, Mode: "login"}, false, nil); err != nil {
		t.Fatal(err)
	}
	w.session.RuntimeIncomplete = []string{unfinished}
	w.session.Stage, w.session.StageName = 4, setupStageIDs[4]
	w.mergeAnswers()
	if w.session.Stage != 2 {
		t.Fatalf("Setup resumes at stage %d", w.session.Stage)
	}
	if err := w.agents(); !errors.Is(err, onboarding.ErrExit) {
		t.Fatalf("Setup selected an agent without a choice: %v", err)
	}
	if len(ui.prompts) != 1 || ui.prompts[0].title != "Choose how to connect" || ui.prompts[0].choices[0].ID != "saved:"+healthy {
		t.Fatalf("Setup did not resume at the connection choices: %#v", ui.prompts)
	}
}

func TestTerminalSetupHealthyAgentReturnsToTheCurrentStage(t *testing.T) {
	for stage, destination := range map[int]string{4: "Review your setup", 5: "Start Culler"} {
		t.Run(destination, func(t *testing.T) {
			w, ui := setupAgentFixture(t)
			w.session.Stage = stage
			w.session.Completed = slices.Clone(setupStageIDs[:stage])
			cfg, err := w.catalog()
			if err != nil {
				t.Fatal(err)
			}
			binding := setupBindingForKind(cfg, setupHarnessKind("codex"))
			if err := w.finishConnection(setupConnection{Binding: binding, Mode: "login"}, false, nil); err != nil {
				t.Fatal(err)
			}
			if err := w.agents(); err != nil {
				t.Fatal(err)
			}
			if len(ui.prompts) != 0 {
				t.Fatalf("the selected agent asks for a destination: %#v", ui.prompts)
			}
			if !slices.Equal(ui.stages, []int{3, stage + 1}) || w.session.Stage != stage {
				t.Fatalf("the nested account choice lost its current step: %v, %d", ui.stages, w.session.Stage)
			}
			if !slices.Equal(ui.completed[0], setupStageIDs[:stage]) || !slices.Equal(ui.completed[1], setupStageIDs[:stage]) {
				t.Fatalf("the nested account choice lost completed steps: %v", ui.completed)
			}
		})
	}
}

func TestTerminalSetupBackFromProjectAllowsAgentReplacement(t *testing.T) {
	w, ui := setupAgentFixture(t, ":exit")
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	binding := setupBindingForKind(cfg, setupHarnessKind("codex"))
	if err := w.finishConnection(setupConnection{Binding: binding, Mode: "login"}, false, nil); err != nil {
		t.Fatal(err)
	}
	w.session.Stage = 3
	if err := w.previousStage(); err != nil {
		t.Fatal(err)
	}
	if err := w.agents(); !errors.Is(err, onboarding.ErrExit) {
		t.Fatalf("the agent step ignored Back: %v", err)
	}
	if len(ui.prompts) != 1 || ui.prompts[0].choices[0].ID != "saved:"+binding || !w.connectionHealthy(binding) {
		t.Fatal("Back lost the current agent or omitted its replacement choices")
	}
}

func TestTerminalSetupManualAgentContinuesAfterSuccessfulCheck(t *testing.T) {
	for _, harness := range []string{"codex", "claude-sdk"} {
		t.Run(harness, func(t *testing.T) {
			w, ui := setupAgentFixture(t, "login:"+harness, "check", "login:"+harness, "check")
			requests := 0
			setupFakeSignIn(w, func(setupConnection) error {
				requests++
				if requests == 1 {
					return errors.New("the first request failed")
				}
				return nil
			})
			if err := w.agents(); err != nil {
				t.Fatal(err)
			}
			if requests != 2 || len(ui.answers) != 0 || len(ui.prompts) != 4 || len(w.session.RuntimeBindings) != 1 {
				t.Fatalf("setup lost the retry or requested another agent: %d, %v", requests, ui.prompts)
			}
			if !w.connectionHealthy(w.session.RuntimeBindings[0]) {
				t.Fatal("setup accepted a failed connection")
			}
			if w.agentDestination(2) != "Review your setup" {
				t.Fatal("the agent choice lost its review destination")
			}
		})
	}
}

func TestTerminalSetupRejectsMultipleAgentFlagsBeforeConnection(t *testing.T) {
	w, _ := setupTestWizard(t)
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	w.answers.RuntimeBindings = setupBindingForKind(cfg, setupHarnessKind("codex")) + "," + setupBindingForKind(cfg, setupHarnessKind("claude-sdk"))
	w.answers.AcquireRuntimeAuth = true
	w.deps.connect = func(*setupWizard, setupConnection) error {
		t.Fatal("setup connected an agent before it rejected multiple selections")
		return nil
	}
	if err := w.agents(); err == nil || !strings.Contains(err.Error(), "Setup accepts one agent. Add more agents") {
		t.Fatalf("setup accepted multiple agents: %v", err)
	}
}

func TestTerminalSetupRejectsAnUndefinedAgentFlagBeforeConnection(t *testing.T) {
	w, _ := setupTestWizard(t)
	w.answers.RuntimeBindings = "undefined-endpoint"
	w.answers.AcquireRuntimeAuth = true
	w.deps.connect = func(*setupWizard, setupConnection) error {
		t.Fatal("setup connected an agent for an undefined agent flag")
		return nil
	}
	err := w.agents()
	var failure *setupFailure
	if !errors.As(err, &failure) || failure.cause !=
		"A selected agent configuration is unavailable. Check its name with your coding harness." {
		t.Fatalf("setup accepted an undefined agent flag: %v", err)
	}
	if len(w.session.RuntimeBindings) != 0 {
		t.Fatalf("setup recorded an undefined agent: %v", w.session.RuntimeBindings)
	}
}

func TestTerminalSetupResumeUsesOneAgentFromSavedSelections(t *testing.T) {
	w, ui := setupAgentFixture(t)
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	var bindings []string
	for _, harness := range []string{"codex", "claude-sdk"} {
		binding := setupBindingForKind(cfg, setupHarnessKind(harness))
		bindings = append(bindings, binding)
		if err := w.finishConnection(setupConnection{Binding: binding, Mode: "login"}, false, nil); err != nil {
			t.Fatal(err)
		}
	}
	w.session.Stage, w.session.ReleaseID = 4, releaseBuildID
	if err := w.revalidate(); err != nil {
		t.Fatal(err)
	}
	if w.session.Stage != 2 {
		t.Fatal("resume retained multiple selected agents")
	}
	if err := w.agents(); err != nil {
		t.Fatal(err)
	}
	if len(ui.prompts) != 0 || !slices.Equal(w.session.RuntimeBindings, bindings[:1]) {
		t.Fatalf("resume lost the default agent or added a step choice: %v, %v", w.session.RuntimeBindings, ui.prompts)
	}
	for _, binding := range bindings {
		if !w.connectionHealthy(binding) {
			t.Fatal("resume removed a configured credential")
		}
	}
}

func TestTerminalSetupRefreshFailureStatesTheCauseAndTheProviderReply(t *testing.T) {
	for _, tc := range []struct {
		status      int
		code, cause string
		reply       string
	}{
		{401, "refresh_token_reused", "The provider rejected the credentials. Sign in again, or choose another way to connect.",
			"Provider reply: status 401, error code refresh_token_reused."},
		{403, "", "The provider refused access for the credentials. Sign in again, or choose another way to connect.",
			"Provider reply: status 403."},
		{400, "invalid_grant", "The provider refused the token refresh. Sign in again, or choose another way to connect.",
			"Provider reply: status 400, error code invalid_grant."},
		{429, "", "The provider could not complete the token refresh. Try again in a few minutes, or choose another way to connect.",
			"Provider reply: status 429."},
		{500, "server_error", "The provider could not complete the token refresh. Try again in a few minutes, or choose another way to connect.",
			"Provider reply: status 500, error code server_error."},
	} {
		t.Run(tc.reply, func(t *testing.T) {
			w, ui := setupAgentFixture(t, "login:codex", "check", ":exit")
			setupFakeSignIn(w, func(setupConnection) error {
				return setupRuntimeFailure(errors.Join(errors.New("fixture cleanup"), &deployment.TokenRefreshError{Status: tc.status, Code: tc.code}), "")
			})
			if err := w.agents(); !errors.Is(err, onboarding.ErrExit) {
				t.Fatalf("The failed sign-in did not return to the agent choice: %v", err)
			}
			cfg, err := w.catalog()
			if err != nil {
				t.Fatal(err)
			}
			activity := "Connect " + w.agentName(setupBindingForKind(cfg, setupHarnessKind("codex")))
			if !slices.Equal(ui.activities, []string{activity}) || ui.activityErrors[0] == nil {
				t.Fatalf("The activity line did not report the failure: %q", ui.activities)
			}
			want := "\n" + tc.cause + "\n" + tc.reply
			if !slices.Contains(ui.panels, want) || strings.Contains(strings.Join(ui.panels, "\n"), "connection failed") {
				t.Fatalf("The failure panel repeats the result or omits the cause: %q", ui.panels)
			}
			if ui.prompts[len(ui.prompts)-1].title != "Choose how to connect" {
				t.Fatal("The failure did not return to the connection choices")
			}
		})
	}
}

func TestTerminalSetupUnknownConnectionFailureSavesTheDiagnostic(t *testing.T) {
	for _, saved := range []bool{true, false} {
		t.Run(map[bool]string{true: "saved", false: "unsaved"}[saved], func(t *testing.T) {
			previous := setupDiagnosticRoot
			t.Cleanup(func() { setupDiagnosticRoot = previous })
			setupDiagnosticRoot = t.TempDir()
			if !saved {
				setupDiagnosticRoot = filepath.Join(setupDiagnosticRoot, "absent")
			}
			w, ui := setupAgentFixture(t, "login:codex", "check", ":exit")
			setupFakeSignIn(w, func(setupConnection) error {
				return setupRuntimeFailure(errors.New(`fixture parse: unknown field "fixture-field"`), "The cause is unknown.")
			})
			if err := w.agents(); !errors.Is(err, onboarding.ErrExit) {
				t.Fatalf("The failed sign-in did not return to the agent choice: %v", err)
			}
			panels := strings.Join(ui.panels, "\n")
			if strings.Contains(panels, "View error details") {
				t.Fatalf("The failure panel names a choice that the screen lacks: %q", ui.panels)
			}
			if !saved {
				if !strings.Contains(panels, "Setup cannot save the diagnostic record.\nShare these details with your coding harness:\n"+
					`Diagnostic: fixture parse: unknown field "fixture-field"`) {
					t.Fatalf("The failure panel omits the unsaved details: %q", ui.panels)
				}
				return
			}
			if strings.Contains(panels, "fixture-field") {
				t.Fatalf("The failure panel shows the raw error: %q", ui.panels)
			}
			_, record, found := strings.Cut(panels, "Share this diagnostic record with your coding harness:\n")
			record, _, _ = strings.Cut(record, "\n")
			body, err := os.ReadFile(record)
			if !found || err != nil || !strings.Contains(string(body), `unknown field \"fixture-field\"`) {
				t.Fatalf("The diagnostic record omits the underlying error: %q, %v", ui.panels, err)
			}
		})
	}
}

func TestTerminalSetupEnvironmentFailureNamesTheVariable(t *testing.T) {
	err := errors.New("forbidden ambient runtime credential OPENAI_API_KEY is set; use an owner-only source file")
	cause := setupCause(setupRuntimeFailure(err, "fallback"))
	if !strings.Contains(cause, "sets OPENAI_API_KEY.") || !strings.Contains(cause, "run ./install.sh again") {
		t.Fatalf("The cause does not name the variable and the next action: %q", cause)
	}
}
