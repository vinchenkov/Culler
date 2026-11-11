//go:build !test_fake_routing

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	operatorconfig "mc/config"
	"mc/onboarding"
	"mc/verbs"
)

type setupTestUI struct {
	t              *testing.T
	answers        []string
	panels         []string
	activities     []string
	activityErrors []error
	progress       []string
	choices        [][]onboarding.Choice
}

func (u *setupTestUI) Stage(_ int, title string, _ []string) { u.panels = append(u.panels, title) }
func (u *setupTestUI) Panel(title string, lines []string) {
	u.panels = append(u.panels, title+"\n"+strings.Join(lines, "\n"))
}
func (u *setupTestUI) Literal(value string) { u.panels = append(u.panels, value) }
func (u *setupTestUI) Choose(prompt string, choices []onboarding.Choice, selected int) (string, error) {
	u.t.Helper()
	u.choices = append(u.choices, slices.Clone(choices))
	if len(u.answers) == 0 {
		u.t.Fatalf("unexpected prompt %s (%v)", prompt, choices)
	}
	answer := u.answers[0]
	u.answers = u.answers[1:]
	if answer == ":back" {
		return "", onboarding.ErrBack
	}
	if answer == ":exit" {
		return "", onboarding.ErrExit
	}
	if answer == ":default" {
		return choices[selected].ID, nil
	}
	if !slices.ContainsFunc(choices, func(c onboarding.Choice) bool { return c.ID == answer }) {
		u.t.Fatalf("choice %s is absent from %s: %v", answer, prompt, choices)
	}
	return answer, nil
}
func (u *setupTestUI) Input(prompt, _ string, _ bool) (string, error) {
	u.t.Fatalf("unexpected input %s", prompt)
	return "", nil
}
func (u *setupTestUI) Activity(label string, action func() error) error {
	u.activities = append(u.activities, label)
	err := action()
	u.activityErrors = append(u.activityErrors, err)
	return err
}
func (u *setupTestUI) ActivityProgress(label string, action func(func(string)) error) error {
	u.activities = append(u.activities, label)
	return action(func(message string) { u.progress = append(u.progress, message) })
}

func setupTestWizard(t *testing.T, answers ...string) (*setupWizard, *setupTestUI) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "deployment")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_HOME", home)
	cfg := operatorconfig.Default()
	profile := cfg.Runtimes.Bindings["chatgpt"]
	profile.Model = "fixture-model"
	cfg.Runtimes.Bindings["chatgpt"] = profile
	cfg.Surfaces.Dashboard.AuthToken = "fixture-dashboard-access"
	if _, err := operatorconfig.WriteAtomic(filepath.Join(home, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	u := &setupTestUI{t: t, answers: answers}
	w := &setupWizard{
		ui: u, home: home, operatorHome: t.TempDir(), input: strings.NewReader(""), output: io.Discard,
		answers: verbs.OnboardArgs{HostReleaseSource: "/fixture/source"},
		deps: setupDependencies{
			docker:  func(context.Context) onboarding.DockerStatus { return onboarding.DockerStatus{State: "ready"} },
			image:   func(context.Context, string, string, func(string)) error { return nil },
			section: func(string, verbs.OnboardArgs) error { return nil },
			connect: func(*setupWizard, setupConnection) error { return nil },
			open:    func(string) error { return nil },
			ready: func(string) ([]string, error) {
				return []string{"Resident service: ready", "Dashboard service: ready"}, nil
			},
			runtimeReady: func(string, []string) error { return nil },
			identity:     func(string, string) (string, error) { return strings.Repeat("a", 64), nil },
		},
	}
	w.mergeAnswers()
	return w, u
}

func TestTerminalSetupModelTestRequiresSelection(t *testing.T) {
	for _, mode := range []string{"api_key", "saved"} {
		t.Run(mode, func(t *testing.T) {
			w, _ := setupTestWizard(t, ":back")
			calls := 0
			w.deps.connect = func(*setupWizard, setupConnection) error { calls++; return nil }
			err := w.checkConnection(setupConnection{Binding: "fixture", Mode: mode})
			if !errors.Is(err, onboarding.ErrBack) || calls != 0 {
				t.Fatalf("check = %v; requests = %d", err, calls)
			}
		})
	}
}

func TestTerminalSetupFailedReplacementRetainsCurrentAccess(t *testing.T) {
	w, _ := setupTestWizard(t, "check")
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	binding := setupBindingForKind(cfg, operatorconfig.CredentialKindCodexOAuth)
	w.session.RuntimeBindings = []string{binding}
	w.session.RuntimeVerified[binding] = true
	w.session.RuntimeIdentities[binding] = setupAccessIdentity(cfg, binding, strings.Repeat("a", 64))
	w.deps.connect = func(*setupWizard, setupConnection) error { return errors.New("the replacement failed") }
	if err := w.checkConnection(setupConnection{Binding: binding, Mode: "login"}); err != nil {
		t.Fatal(err)
	}
	if !w.connectionHealthy(binding) || !slices.Contains(w.session.RuntimeIncomplete, binding) {
		t.Fatal("the failed replacement lost current access or its incomplete result")
	}
}

type setupAccountTestUI struct{ *setupTestUI }

func (u *setupAccountTestUI) Input(prompt, _ string, _ bool) (string, error) {
	u.t.Fatalf("unexpected account input %s", prompt)
	return "", nil
}

func TestTerminalSetupNewAccountPreservesCurrentConnection(t *testing.T) {
	w, ui := setupTestWizard(t, "check")
	w.ui = &setupAccountTestUI{ui}
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	first := setupBindingForKind(cfg, operatorconfig.CredentialKindCodexOAuth)
	original := cfg.Runtimes.Bindings[first]
	w.deps.connect = func(w *setupWizard, c setupConnection) error {
		current, err := w.catalog()
		if err != nil {
			return err
		}
		if c.Binding != "codex-2" || current.Runtimes.Bindings[c.Binding].Credential == original.Credential {
			t.Fatal("the new account reused the current connection")
		}
		if current.Runtimes.Bindings[first] != original {
			t.Fatal("the new account changed the current configuration")
		}
		return nil
	}
	if err := w.subscriptionAccess("codex"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(w.session.RuntimeBindings, "codex-2") {
		t.Fatal("the new account did not retain its selection")
	}
	if len(ui.choices) != 0 {
		t.Fatal("Sign-in requested another choice.")
	}
}

func TestTerminalSetupFailurePreservesHealthyAgent(t *testing.T) {
	w, u := setupTestWizard(t, "check")
	w.session.RuntimeBindings = []string{"healthy"}
	w.session.RuntimeVerified["healthy"] = true
	w.deps.connect = func(*setupWizard, setupConnection) error { return errors.New("fixture-provider-secret") }
	if err := w.checkConnection(setupConnection{Binding: "rejected", Mode: "login"}); err != nil {
		t.Fatal(err)
	}
	if !w.session.RuntimeVerified["healthy"] || w.session.RuntimeVerified["rejected"] {
		t.Fatal("the failed check changed a healthy configuration")
	}
	body, err := os.ReadFile(filepath.Join(w.home, "setup", "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "fixture-provider-secret") || strings.Contains(strings.Join(u.panels, "\n"), "fixture-provider-secret") {
		t.Fatal("the provider secret reached a record or panel")
	}
}

func TestTerminalSetupDockerHandoffPrecedesImageAndHome(t *testing.T) {
	w, u := setupTestWizard(t, "install", "check")
	checks := 0
	w.deps.docker = func(context.Context) onboarding.DockerStatus {
		checks++
		if checks < 3 {
			return onboarding.DockerStatus{State: "absent"}
		}
		return onboarding.DockerStatus{State: "ready"}
	}
	opened := ""
	w.deps.open = func(address string) error { opened = address; return nil }
	operations := []string{}
	w.deps.image = func(_ context.Context, _, _ string, report func(string)) error {
		if checks < 3 {
			t.Fatal("the image build preceded Docker readiness")
		}
		operations = append(operations, "image")
		report("Compile Culler for Linux")
		if !slices.Contains(u.progress, "Compile Culler for Linux") {
			t.Fatal("The setup omitted progress before the image operation completed.")
		}
		return nil
	}
	w.deps.section = func(section string, _ verbs.OnboardArgs) error { operations = append(operations, section); return nil }
	if err := w.prepare(); err != nil {
		t.Fatal(err)
	}
	if opened != "https://docs.docker.com/desktop/setup/install/mac-install/" || strings.Join(operations, ",") != "image,preflight,home" {
		t.Fatalf("opened=%s operations=%v", opened, operations)
	}
	if len(u.activityErrors) < 3 || !errors.Is(u.activityErrors[0], onboarding.ErrActionRequired) ||
		!errors.Is(u.activityErrors[1], onboarding.ErrActionRequired) || u.activityErrors[2] != nil {
		t.Fatal("the Docker display reports success before Docker is ready")
	}
}

func TestTerminalSetupResumeReturnsToChangedPrerequisite(t *testing.T) {
	w, _ := setupTestWizard(t)
	w.session.Stage, w.session.ReleaseID = 4, releaseBuildID
	w.session.WorkspaceRoot = "/fixture/project"
	w.session.Project = "project"
	w.deps.docker = func(context.Context) onboarding.DockerStatus {
		return onboarding.DockerStatus{State: "engine-unavailable", Detail: "Open Docker Desktop."}
	}
	w.deps.section = func(string, verbs.OnboardArgs) error {
		t.Fatal("setup reached a section before Docker readiness")
		return nil
	}
	if err := w.revalidate(); err != nil {
		t.Fatal(err)
	}
	if w.session.Stage != 1 || w.session.WorkspaceRoot != "/fixture/project" {
		t.Fatal("resume lost a project choice")
	}
}

func TestTerminalSetupResumeRequiresCurrentActivationConsent(t *testing.T) {
	w, _ := setupTestWizard(t)
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	binding := setupBindingForKind(cfg, operatorconfig.CredentialKindCodexOAuth)
	w.session.RuntimeBindings = []string{binding}
	w.session.RuntimeVerified[binding] = true
	w.session.RuntimeIdentities[binding] = setupAccessIdentity(cfg, binding, strings.Repeat("a", 64))
	w.session.WorkspaceRoot, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w.session.Project = "project"
	w.session.Stage, w.session.ReleaseID = 4, releaseBuildID
	if err := w.revalidate(); err != nil {
		t.Fatal(err)
	}
	if w.session.Stage != 3 {
		t.Fatalf("resume stage=%d, want review", w.session.Stage)
	}
}

func TestTerminalSetupResumeFailureSharesTheDiagnosticRecord(t *testing.T) {
	previous := setupDiagnosticRoot
	t.Cleanup(func() { setupDiagnosticRoot = previous })
	setupDiagnosticRoot = t.TempDir()
	w, ui := setupTestWizard(t)
	w.session.Stage, w.session.ReleaseID = 3, releaseBuildID
	w.deps.section = func(section string, _ verbs.OnboardArgs) error {
		return setupSectionResult(section, 1, []byte(`{"error":{"message":"fixture preflight failure"}}`), nil)
	}
	if err := w.revalidate(); err != nil {
		t.Fatal(err)
	}
	if w.session.Stage != 1 || !slices.Equal(ui.activities, []string{"Check Mac requirements"}) || ui.activityErrors[0] == nil {
		t.Fatalf("stage = %d; activities = %q", w.session.Stage, ui.activities)
	}
	// The activity line states the failure, so the panel has no title.
	panel := ui.panels[len(ui.panels)-1]
	title, body, _ := strings.Cut(panel, "\n")
	lines := strings.Split(body, "\n")
	if title != "" || len(lines) != 4 || lines[0] != "The cause is unknown." ||
		lines[1] != "Share this diagnostic record with your coding harness:" || lines[3] != "Your completed choices remain saved." {
		t.Fatalf("panel = %q", panel)
	}
	record, err := os.ReadFile(lines[2])
	if err != nil || !strings.Contains(string(record), "fixture preflight failure") {
		t.Fatalf("record = %q, %v", record, err)
	}
}

func TestTerminalSetupRuntimeIdentityIncludesModel(t *testing.T) {
	w, _ := setupTestWizard(t)
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	binding := setupBindingForKind(cfg, operatorconfig.CredentialKindCodexOAuth)
	w.session.RuntimeVerified[binding] = true
	w.session.RuntimeIdentities[binding] = setupAccessIdentity(cfg, binding, strings.Repeat("a", 64))
	if !w.connectionHealthy(binding) {
		t.Fatal("the initial configuration is unhealthy")
	}
	spec := cfg.Runtimes.Bindings[binding]
	spec.Model = "fixture-new-model"
	cfg.Runtimes.Bindings[binding] = spec
	if _, err := operatorconfig.WriteAtomic(filepath.Join(w.home, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	if w.connectionHealthy(binding) {
		t.Fatal("the model change reused a stale provider result")
	}
}

func TestTerminalSetupActivationExcludesPipelineTasks(t *testing.T) {
	w, _ := setupTestWizard(t)
	sections := []string{}
	w.deps.section = func(section string, a verbs.OnboardArgs) error {
		sections = append(sections, section)
		if a.Smoke || a.SmokeApproveTask != 0 {
			t.Fatal("setup requested pipeline work")
		}
		if a.ActivateSupervision != (section == "supervision") {
			t.Fatalf("unexpected activation in %s", section)
		}
		return nil
	}
	if err := w.start(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(sections, ",") != "routing,container,tunables,surfaces,supervision" {
		t.Fatalf("sections=%v", sections)
	}
	if len(w.services) == 0 {
		t.Fatal("setup omitted service readiness")
	}
}

func TestTerminalSetupDashboardPreservesAuthenticationAndRetries(t *testing.T) {
	w, u := setupTestWizard(t, "open", "open")
	w.session.Project, w.session.WorkspaceRoot = "project", t.TempDir()
	attempts := 0
	w.deps.open = func(address string) error {
		attempts++
		if !strings.HasSuffix(address, "/#token=fixture-dashboard-access") {
			t.Fatal("the browser handoff omitted authentication")
		}
		if attempts == 1 {
			return errors.New("browser unavailable")
		}
		return nil
	}
	if err := w.dashboard(); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d", attempts)
	}
	if !strings.Contains(strings.Join(u.panels, "\n"), "Add your project") {
		t.Fatal("the final summary omitted the dashboard project action")
	}
	if err := w.save(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(w.home, "setup", "session.json"))
	if err != nil || strings.Contains(string(body), "fixture-dashboard-access") {
		t.Fatal("the saved setup record exposed dashboard access")
	}
}

func TestTerminalSetupReadinessFailurePreventsCompletion(t *testing.T) {
	w, u := setupTestWizard(t, ":exit")
	w.deps.ready = func(string) ([]string, error) { return nil, errors.New("service unavailable") }
	if err := w.dashboard(); !errors.Is(err, onboarding.ErrExit) {
		t.Fatalf("dashboard=%v", err)
	}
	if strings.Contains(strings.Join(u.panels, "\n"), "Culler is ready") {
		t.Fatal("setup declared readiness before its checks passed")
	}
}

func TestTerminalSetupOptionsPreserveFrontDoorAnswers(t *testing.T) {
	a, err := parseTerminalSetupArgs([]string{"--mc-home", "/fixture/home", "--bin-dir", "/fixture/bin", "--project", "project", "--acquire-runtime-auth", "--activate"})
	if err != nil || a.Project != "project" || !a.AcquireRuntimeAuth || !a.ActivateSupervision {
		t.Fatalf("args=%+v error=%v", a, err)
	}
	if _, err := parseTerminalSetupArgs([]string{"--smoke"}); err == nil {
		t.Fatal("the terminal setup accepted a pipeline smoke")
	}
	w, _ := setupTestWizard(t)
	w.answers.DiscordMode = "enabled"
	if w.arguments().DiscordMode != "enabled" {
		t.Fatal("the terminal setup replaced an explicit service choice")
	}
}

func TestSetupUnknownConfigKeyCause(t *testing.T) {
	message := "scaffold config.toml: config.toml carries unknown key \"budgets.review_wip_caap\""
	cause := setupSectionCause(message + " fixture-secret")
	if !strings.Contains(cause, "budgets.review_wip_caap") || !strings.Contains(cause, "coding harness") ||
		strings.Contains(cause, "fixture-secret") || strings.Contains(cause, "unknown.") {
		t.Fatalf("configuration cause = %q", cause)
	}
	details := strings.Join(setupDiagnosticLines("Prepare Culler", &setupFailure{cause: cause}), "\n")
	if !strings.Contains(details, "budgets.review_wip_caap") {
		t.Fatal("the details omit the rejected key")
	}
}

func TestTerminalSetupOperationFailureStatesTheFailureOnce(t *testing.T) {
	previous := setupDiagnosticRoot
	t.Cleanup(func() { setupDiagnosticRoot = previous })
	setupDiagnosticRoot = t.TempDir()
	w, ui := setupTestWizard(t, ":exit")
	err := w.operation("Prepare Culler", func() error { return &setupFailure{cause: "The fixture preparation failed."} })
	if !errors.Is(err, onboarding.ErrExit) {
		t.Fatalf("operation = %v", err)
	}
	if !slices.Equal(ui.activities, []string{"Prepare Culler"}) || ui.activityErrors[0] == nil {
		t.Fatalf("the activity line did not report the failure: %q", ui.activities)
	}
	// The activity line states the failure. The panel adds only the cause and the diagnostic record.
	if len(ui.panels) != 1 || !strings.HasPrefix(ui.panels[0], "\nThe fixture preparation failed.\nShare this diagnostic record") ||
		strings.Contains(ui.panels[0], "Prepare Culler failed") {
		t.Fatalf("the failure panel repeats the failure line: %q", ui.panels)
	}
}
