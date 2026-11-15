//go:build !test_fake_routing

package main

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	operatorconfig "mc/config"
	"mc/onboarding"
	"mc/verbs"
)

type setupAccessTestUI struct {
	*setupChoiceUI
	beforeConfirm func()
	prompts       []onboarding.Prompt
}

// Ask repeats the prompt until the prompt check accepts the answer, as the terminal does.
func (ui *setupAccessTestUI) Ask(prompt onboarding.Prompt) (string, error) {
	ui.prompts = append(ui.prompts, prompt)
	correction := prompt.Correction
	for {
		if correction != nil {
			ui.Problem(correction.Title, correction.Lines)
		}
		answer, err := ui.Input(prompt.Label, prompt.Value, prompt.Secret)
		if err != nil || prompt.Check == nil {
			return answer, err
		}
		if correction = prompt.Check(answer); correction == nil {
			return answer, nil
		}
	}
}

func (ui *setupAccessTestUI) Problem(title string, lines []string) {
	if title != "" {
		title = "! " + title
	}
	ui.Panel(title, lines)
}

func (ui *setupAccessTestUI) Choose(prompt string, choices []onboarding.Choice, selected int) (string, error) {
	if prompt == "Test this connection?" && ui.beforeConfirm != nil {
		ui.beforeConfirm()
	}
	return ui.setupChoiceUI.Choose(prompt, choices, selected)
}

func (ui *setupAccessTestUI) Input(prompt, value string, secret bool) (string, error) {
	return ui.setupChoiceUI.Input(prompt, value, secret)
}

func setupAccessFixture(t *testing.T, answers ...setupChoiceAnswer) (*setupWizard, *setupAccessTestUI) {
	t.Helper()
	w, _ := setupTestWizard(t)
	w.session.Stage = 2
	ui := &setupAccessTestUI{setupChoiceUI: &setupChoiceUI{t: t, answers: answers}}
	w.ui = ui
	return w, ui
}

func setupAccessReload(t *testing.T, w *setupWizard) {
	t.Helper()
	saved, err := onboarding.LoadSession(w.home)
	if err != nil {
		t.Fatal(err)
	}
	w.session, w.answers = saved, verbs.OnboardArgs{}
	w.mergeAnswers()
}

func setupAssertAccessDraftAbsent(t *testing.T, w *setupWizard, name string) {
	t.Helper()
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := cfg.Runtimes.Bindings[name]; exists {
		t.Fatal("the account draft reached the catalog before provider confirmation")
	}
}

func setupChoiceIDs(choices []onboarding.Choice) []string {
	ids := []string{}
	for _, choice := range choices {
		ids = append(ids, choice.ID)
	}
	return ids
}

func TestTerminalSetupSubscriptionResumePreservesHealthyAccount(t *testing.T) {
	w, _ := setupAccessFixture(t)
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	first := setupBindingForKind(cfg, operatorconfig.CredentialKindCodexOAuth)
	original := cfg.Runtimes.Bindings[first]
	w.session.RuntimeBindings = []string{first}
	w.session.RuntimeVerified[first] = true
	w.session.RuntimeIdentities[first] = setupAccessIdentity(cfg, first, strings.Repeat("a", 64))
	w.deps.connect = func(*setupWizard, setupConnection) error { return onboarding.ErrExit }
	if err := w.subscriptionAccess("codex"); !errors.Is(err, onboarding.ErrExit) {
		t.Fatalf("subscription exit = %v", err)
	}
	setupAccessReload(t, w)
	if w.session.Access == nil || w.session.Access.Step != "check" || w.session.Access.Binding != "codex-2" || w.session.Access.Mode != "login" {
		t.Fatal("the subscription lost its completed account choices")
	}

	w.deps.connect = func(w *setupWizard, connection setupConnection) error {
		current, err := w.catalog()
		if err != nil || current.Runtimes.Bindings[first] != original || current.Runtimes.Bindings[connection.Binding].Credential == original.Credential {
			t.Fatal("the resumed account changed the healthy configuration")
		}
		if connection.Binding != "codex-2" || connection.Mode != "login" {
			t.Fatal("the resumed account lost its credential choice")
		}
		return nil
	}
	if err := w.resumeAccess(); err != nil {
		t.Fatal(err)
	}
	if !w.connectionHealthy(first) || !w.connectionHealthy("codex-2") {
		t.Fatal("the resumed account lost a healthy result")
	}
}

func TestTerminalSetupFlagSignInResumesWithoutFlags(t *testing.T) {
	w, _ := setupAccessFixture(t)
	w.deps.connect = func(*setupWizard, setupConnection) error { return onboarding.ErrExit }
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	binding := setupBindingForKind(cfg, operatorconfig.CredentialKindCodexOAuth)
	w.answers.RuntimeBindings, w.answers.AcquireRuntimeAuth = binding, true
	if err := w.connectFlagSelections(); !errors.Is(err, onboarding.ErrExit) {
		t.Fatalf("flag selection exit = %v", err)
	}
	setupAccessReload(t, w)
	if w.session.Access == nil || w.session.Access.Binding != binding || w.session.Access.Mode != "login" {
		t.Fatal("the flag selection lost its sign-in")
	}
	calls := 0
	w.deps.connect = func(_ *setupWizard, connection setupConnection) error {
		calls++
		if connection.Binding != binding || connection.Mode != "login" {
			t.Fatal("the flag selection changed after exit")
		}
		return nil
	}
	if err := w.agents(); err != nil || calls != 1 {
		t.Fatalf("flag selection resume = %v, %d calls", err, calls)
	}
}

func TestTerminalSetupAccessBackFollowsEachPath(t *testing.T) {
	for _, tc := range []struct {
		name   string
		choice onboarding.AccessChoice
		want   string
	}{
		{"sign-in", onboarding.AccessChoice{Mode: "login", Step: "check"}, ""},
		{"new sign-in", onboarding.AccessChoice{Mode: "login", Step: "check", NewAccount: true}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			choice := tc.choice
			if stays := setupAccessBack(&choice); stays != (tc.want != "") || choice.Step != tc.want {
				t.Fatalf("Back = %q, %v; want %q", choice.Step, stays, tc.want)
			}
		})
	}
}

func TestTerminalSetupSubscriptionUsesReferenceBinding(t *testing.T) {
	for harness, reference := range map[string]string{"codex": "chatgpt", "claude-sdk": "claude"} {
		t.Run(harness, func(t *testing.T) {
			w, _ := setupAccessFixture(t)
			if _, err := operatorconfig.UpdateAtomic(filepath.Join(w.home, "config.toml"), func(cfg *operatorconfig.Config) error {
				cfg.Runtimes.Credentials["a-account"] = cfg.Runtimes.Credentials[reference]
				cfg.Runtimes.Bindings["a-account"] = operatorconfig.RuntimeBinding{Harness: harness, Model: "second-model", Credential: "a-account"}
				return cfg.Validate()
			}); err != nil {
				t.Fatal(err)
			}
			w.deps.identity = func(string, string) (string, error) { return "", nil }
			choice, err := w.subscriptionChoice(harness)
			if err != nil || choice.Binding != reference || choice.Template != reference || choice.NewAccount {
				t.Fatalf("first sign-in = %#v, %v", choice, err)
			}
			if err := w.prepareAccessChoice(choice); err != nil {
				t.Fatal(err)
			}
			first, err := w.catalog()
			if err != nil {
				t.Fatal(err)
			}
			base := setupDefaultBase[harness]
			if first.Runtimes.Credentials[reference].Name != base+"_connection" || first.Runtimes.Bindings[reference].Name != base+"_profile" {
				t.Fatal("The first account lacks separate default names.")
			}
			w.deps.identity = func(string, string) (string, error) { return strings.Repeat("a", 64), nil }
			choice, err = w.subscriptionChoice(harness)
			if err != nil || choice.Template != reference || !choice.NewAccount || choice.Name != harness+"-2" {
				t.Fatalf("second sign-in = %#v, %v", choice, err)
			}
			if err := w.prepareAccessChoice(choice); err != nil {
				t.Fatal(err)
			}
			cfg, err := w.catalog()
			if err != nil {
				t.Fatal(err)
			}
			created, want := cfg.Runtimes.Bindings[choice.Name], cfg.Runtimes.Bindings[reference]
			if created.Name != base+"_2_profile" || cfg.Runtimes.Credentials[choice.Name].Name != base+"_2_connection" {
				t.Fatal("The second account lacks separate unique default names.")
			}
			if created.Harness != want.Harness || created.Model != want.Model || created.Credential != choice.Name ||
				cfg.Runtimes.Credentials[choice.Name].Kind != setupHarnessKind(harness) {
				t.Fatalf("second account = %#v", created)
			}
		})
	}
}

func TestTerminalSetupNamesAnAdditionalAccountWithoutAPrompt(t *testing.T) {
	w, ui := setupAccessFixture(t)
	w.deps.connect = func(*setupWizard, setupConnection) error { return onboarding.ErrExit }
	w.deps.identity = func(string, string) (string, error) { return strings.Repeat("a", 64), nil }
	if err := w.subscriptionAccess("codex"); !errors.Is(err, onboarding.ErrExit) {
		t.Fatalf("subscription exit = %v", err)
	}
	if len(ui.answers) != 0 || len(ui.panels) != 0 || len(ui.prompts) != 0 {
		t.Fatalf("panels = %q, prompts = %#v", ui.panels, ui.prompts)
	}
	if choice := w.session.Access; choice == nil || choice.Name != "codex-2" || choice.Binding != "codex-2" || choice.Step != "check" {
		t.Fatalf("saved account = %#v", choice)
	}
}

func TestTerminalSetupGivesTheNameOfAnUntestedAccountToTheNewAccount(t *testing.T) {
	w, ui := setupAccessFixture(t)
	calls := 0
	w.deps.connect = func(*setupWizard, setupConnection) error {
		calls++
		if calls == 1 {
			return onboarding.ErrBack
		}
		return onboarding.ErrExit
	}
	w.deps.identity = func(string, string) (string, error) { return strings.Repeat("a", 64), nil }
	first, err := w.subscriptionChoice("codex")
	if err != nil || first.Name != "codex-2" || first.CreatedBinding != "" {
		t.Fatalf("first account = %#v, %v", first, err)
	}
	if err := w.prepareAccessChoice(first); err != nil {
		t.Fatal(err)
	}
	w.session.RuntimeCreated = append(w.session.RuntimeCreated, "other-untested")
	if err := w.subscriptionAccess("codex"); !errors.Is(err, onboarding.ErrBack) {
		t.Fatalf("Back = %v", err)
	}
	if slices.Contains(w.session.RuntimeCreated, "other-untested") {
		t.Fatal("The sign-in retained another untested connection.")
	}
	if err := w.subscriptionAccess("codex"); !errors.Is(err, onboarding.ErrExit) {
		t.Fatalf("exit = %v", err)
	}
	if choice := w.session.Access; len(ui.answers) != 0 || choice.Name != "codex-2" || choice.CreatedBinding != "codex-2" {
		t.Fatalf("saved account = %#v", choice)
	}
}

func TestTerminalSetupRenamesAnAccountWhoseNameIsTaken(t *testing.T) {
	for _, step := range []string{"check", "account-name"} {
		t.Run(step, func(t *testing.T) {
			w, ui := setupAccessFixture(t)
			w.deps.connect = func(*setupWizard, setupConnection) error { return onboarding.ErrExit }
			w.session.Access = &onboarding.AccessChoice{Mode: "login", Step: step, Harness: "codex", Template: "chatgpt", Binding: "chatgpt", Name: "claude", NewAccount: true}
			if err := w.resumeAccess(); !errors.Is(err, onboarding.ErrExit) {
				t.Fatalf("resume = %v", err)
			}
			if choice := w.session.Access; len(ui.answers) != 0 || choice.Name != "codex-2" || choice.Binding != "codex-2" || choice.Step != "check" {
				t.Fatalf("saved account = %#v", choice)
			}
		})
	}
}

func TestTerminalSetupRejectsUnsupportedConnectionBeforePreparation(t *testing.T) {
	w, _ := setupAccessFixture(t)
	w.deps.connect = func(*setupWizard, setupConnection) error {
		t.Fatal("The unsupported connection reached the provider")
		return nil
	}
	err := w.checkConnection(setupConnection{Binding: "chatgpt", Mode: "static", Prepare: func() error { t.Fatal("The unsupported connection reached configuration"); return nil }})
	if err == nil {
		t.Fatal("The unsupported connection passed")
	}
	cfg, err := w.catalog()
	if err != nil || len(cfg.Runtimes.EnabledBindings) != 0 {
		t.Fatal("The unsupported connection changed the configuration")
	}
}

// setupDefaultBase holds the snake_case provider base of each default name.
var setupDefaultBase = map[string]string{"codex": "openai", "claude-sdk": "anthropic"}

func TestTerminalSetupDefaultNames(t *testing.T) {
	cfg := operatorconfig.Default()
	for harness, base := range setupDefaultBase {
		if got := setupSnakeCase(setupProviderName(harness)); got != base {
			t.Fatalf("The %s provider base is %q.", harness, got)
		}
	}
	for _, base := range []string{"Fixture AI", "Fixture AI API key", "Fixture AI subscription", "Fixture AI connection", "fixture_ai_connection", "fixture_ai_api_key", " Fixture.AI "} {
		if got := setupDefaultName(cfg, "new", base, "connection"); got != "fixture_ai_connection" {
			t.Fatal(got)
		}
		if got := setupDefaultName(cfg, "new", base, "profile"); got != "fixture_ai_profile" {
			t.Fatal(got)
		}
	}
	if got := setupDefaultName(cfg, "new", "fixture_ai_2_connection", "profile"); got != "fixture_ai_2_profile" {
		t.Fatal(got)
	}
	cfg.Runtimes.Bindings["used"] = operatorconfig.RuntimeBinding{Name: "Fixture_AI_profile"}
	if got := setupDefaultName(cfg, "new", "Fixture AI", "profile"); got != "fixture_ai_2_profile" {
		t.Fatal(got)
	}
	cfg.Runtimes.Bindings["custom"] = operatorconfig.RuntimeBinding{Name: "my_agent"}
	if got := setupProfileDefault(cfg, "custom", "fixture_connection"); got != "my_agent" {
		t.Fatal(got)
	}
}
