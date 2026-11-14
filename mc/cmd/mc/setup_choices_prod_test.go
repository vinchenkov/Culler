//go:build !test_fake_routing

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	operatorconfig "mc/config"
	"mc/onboarding"
	"mc/routing"
	"mc/verbs"
)

type setupChoiceAnswer struct {
	prompt, value string
	choice        string
	err           error
}

type setupChoiceUI struct {
	t            *testing.T
	answers      []setupChoiceAnswer
	panels       []string
	defaults     []string
	choices      []onboarding.Choice
	beforeChoose func([]onboarding.Choice)
}

func (u *setupChoiceUI) Stage(int, string, []string) {}
func (u *setupChoiceUI) Panel(title string, lines []string) {
	u.panels = append(u.panels, title+"\n"+strings.Join(lines, "\n"))
}
func (u *setupChoiceUI) take(prompt string) setupChoiceAnswer {
	u.t.Helper()
	if len(u.answers) == 0 {
		u.t.Fatalf("unexpected prompt %q", prompt)
	}
	answer := u.answers[0]
	u.answers = u.answers[1:]
	if answer.prompt != "" && answer.prompt != prompt {
		u.t.Fatalf("prompt = %q; want %q", prompt, answer.prompt)
	}
	return answer
}
func (u *setupChoiceUI) Choose(prompt string, choices []onboarding.Choice, selected int) (string, error) {
	u.choices = append(u.choices, choices...)
	if u.beforeChoose != nil {
		u.beforeChoose(choices)
	}
	answer := u.take(prompt)
	for _, id := range []string{answer.choice, answer.value} {
		if id != "" && !slices.ContainsFunc(choices, func(c onboarding.Choice) bool { return c.ID == id }) {
			u.t.Fatalf("%q is absent from the choices for %q", id, prompt)
		}
	}
	return answer.value, answer.err
}
func (u *setupChoiceUI) Input(prompt, value string, _ bool) (string, error) {
	u.defaults = append(u.defaults, prompt+"="+value)
	answer := u.take(prompt)
	return answer.value, answer.err
}
func (u *setupChoiceUI) Activity(_ string, action func() error) error { return action() }
func (u *setupChoiceUI) ActivityProgress(_ string, action func(func(string)) error) error {
	return action(func(string) {})
}

func setupProjectFixture(t *testing.T, answers ...setupChoiceAnswer) (*setupWizard, *setupChoiceUI, string) {
	t.Helper()
	w, _ := setupTestWizard(t)
	u := &setupChoiceUI{t: t, answers: answers}
	w.ui = u
	w.session.Stage = 3
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return w, u, root
}

func TestTerminalSetupAgentNamesPreserveDistinctAccounts(t *testing.T) {
	cfg := operatorconfig.Default()
	first := setupBindingForKind(cfg, operatorconfig.CredentialKindCodexOAuth)
	second := setupBindingForKind(cfg, operatorconfig.CredentialKindClaudeOAuth)
	cfg.Runtimes.Bindings["work-account"] = cfg.Runtimes.Bindings[first]
	names := setupAgentNames(cfg, []string{first, "work-account", second, "unavailable-account"})
	if !slices.Equal(names, []string{"Codex", "Codex (work-account)", "Claude Code", "unavailable-account"}) {
		t.Fatalf("agent names lost account identity: %v", names)
	}
}

func setupRoleFixture(t *testing.T, w *setupWizard) string {
	t.Helper()
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	first := setupBindingForKind(cfg, operatorconfig.CredentialKindCodexOAuth)
	second := setupBindingForKind(cfg, operatorconfig.CredentialKindClaudeOAuth)
	w.session.RuntimeBindings = []string{first, second}
	registry := routing.Registry{}
	for _, id := range w.session.RuntimeBindings {
		registry[id] = cfg.Runtimes.Bindings[id].Harness
	}
	table, err := routing.UniformTable(first, registry)
	if err != nil {
		t.Fatal(err)
	}
	table, err = table.WithRoute("worker", second, registry)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "roles.md")
	if err := os.WriteFile(path, []byte(table.Markdown()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTerminalSetupAdvancedShowsSelectedValues(t *testing.T) {
	w, _, _ := setupProjectFixture(t)
	w.session.RoutingFile = setupRoleFixture(t, w)
	w.session.Tunables = map[string]int{"review-wip-cap": 9, "timeout-minutes": 17}
	w.session.DashboardBind = "localhost:7444"
	text := strings.Join(w.advancedLines(), "\n")
	for _, role := range routing.Roles() {
		label := strings.ToUpper(role[:1]) + role[1:]
		if !strings.Contains(text, label+": ") {
			t.Fatalf("the advanced panel omits %s", role)
		}
	}
	for _, field := range setupTunableFields(operatorconfig.Default()) {
		if !strings.Contains(text, field.label+": ") {
			t.Fatalf("the advanced panel omits %s", field.label)
		}
	}
	for _, want := range []string{"Maximum review packets per project: 9", "Agent lease in minutes: 17", "Dashboard: localhost:7444", "Resident interval", "Backup interval", w.session.RoutingFile} {
		if !strings.Contains(text, want) {
			t.Fatalf("the advanced panel omits %q: %s", want, text)
		}
	}
}

func TestTerminalSetupIntervalsRecoverAndBackLocally(t *testing.T) {
	w, u, _ := setupProjectFixture(t)
	fields := setupTunableFields(operatorconfig.Default())
	u.answers = []setupChoiceAnswer{
		{prompt: fields[0].label, value: "0"},
		{prompt: fields[0].label, value: "8"},
		{prompt: fields[1].label, err: onboarding.ErrBack},
		{prompt: fields[0].label, value: "7"},
	}
	for _, field := range fields[1:] {
		u.answers = append(u.answers, setupChoiceAnswer{prompt: field.label, value: strconv.Itoa(field.value + 1)})
	}
	if err := w.editTunables(); err != nil {
		t.Fatal(err)
	}
	saved, err := onboarding.LoadSession(w.home)
	if err != nil || saved.Tunables[fields[0].name] != 7 || len(saved.Tunables) != len(fields) {
		t.Fatalf("saved intervals = %#v, %v", saved.Tunables, err)
	}
	if !slices.Contains(u.defaults, fields[0].label+"=8") {
		t.Fatalf("Back lost the prior interval: %v", u.defaults)
	}
}

func TestTerminalSetupDashboardAddressRecoversLocally(t *testing.T) {
	w, u, _ := setupProjectFixture(t,
		setupChoiceAnswer{prompt: "Dashboard host and port", value: "localhost"},
		setupChoiceAnswer{prompt: "Dashboard host and port", value: "localhost:0"},
		setupChoiceAnswer{prompt: "Dashboard host and port", value: "localhost:65536"},
		setupChoiceAnswer{prompt: "Dashboard host and port", value: "localhost:7444"},
	)
	if err := w.editDashboard(); err != nil {
		t.Fatal(err)
	}
	if w.session.DashboardBind != "localhost:7444" || len(u.answers) != 0 {
		t.Fatalf("dashboard address = %q", w.session.DashboardBind)
	}
}

func TestTerminalSetupRoleFileRecoversLocally(t *testing.T) {
	w, u, _ := setupProjectFixture(t)
	path := setupRoleFixture(t, w)
	invalid := filepath.Join(t.TempDir(), "invalid.md")
	if err := os.WriteFile(invalid, []byte("This is not a role assignment table."), 0o600); err != nil {
		t.Fatal(err)
	}
	u.answers = []setupChoiceAnswer{
		{prompt: "Role assignment file path", value: "relative.md"},
		{prompt: "Role assignment file path", value: invalid},
		{prompt: "Role assignment file path", value: path},
	}
	if err := w.editRouting(); err != nil {
		t.Fatal(err)
	}
	if w.session.RoutingFile != path || len(u.answers) != 0 {
		t.Fatalf("role assignment file = %q", w.session.RoutingFile)
	}
}

func TestTerminalSetupReviewOnlyRequestsActivationConsent(t *testing.T) {
	w, u, _ := setupProjectFixture(t,
		setupChoiceAnswer{choice: "start", err: onboarding.ErrExit},
	)
	w.session.Stage = 4
	w.session.RoutingFile = setupRoleFixture(t, w)
	w.deps.section = func(string, verbs.OnboardArgs) error {
		t.Fatal("the review started Culler without activation consent")
		return nil
	}
	if err := w.review(); !errors.Is(err, onboarding.ErrExit) {
		t.Fatalf("review exit = %v", err)
	}
	if len(u.answers) != 0 || w.session.Stage != 4 {
		t.Fatal("the advanced options bypassed fresh activation consent")
	}
	if len(u.choices) != 1 || u.choices[0].ID != "start" {
		t.Fatalf("the review asks for another setup step: %#v", u.choices)
	}
}

func TestTerminalSetupProjectToDashboardCopy(t *testing.T) {
	for _, columns := range []int{80, 40} {
		t.Run(strconv.Itoa(columns), func(t *testing.T) {
			t.Setenv("COLUMNS", strconv.Itoa(columns))
			t.Setenv("NO_COLOR", "1")
			preview := setupPreviewProject(t, false)
			var input strings.Builder
			for _, step := range preview.steps {
				input.WriteString(step.Send)
			}
			var out bytes.Buffer
			terminal := onboarding.NewTerminal(strings.NewReader(input.String()), &out)
			terminal.SetStageTitles(setupStageTitles)
			preview.wizard.ui, preview.wizard.output = terminal, &out
			preview.run()
			text := out.String()
			for _, internal := range []string{"Project", "Agent configurations", "runtime control", "Tool home", "Recommended", "fixture-dashboard-access"} {
				if strings.Contains(text, internal) {
					t.Fatalf("the normal setup path exposed %q", internal)
				}
			}
			if !strings.Contains(text, "http://127.0.0.1:7333/") || !strings.Contains(text, "Add your project") {
				t.Fatal("the setup omitted the dashboard address or goal file")
			}
			t.Logf("TERMINAL CAPTURE (%d columns)\n%s", columns, text)
		})
	}
}

func TestTerminalSetupResumeUsesKnownProgress(t *testing.T) {
	preview := setupPreviewResume(t)
	var out bytes.Buffer
	terminal := onboarding.NewTerminal(strings.NewReader("q"), &out)
	terminal.SetStageTitles(setupStageTitles)
	preview.wizard.ui = terminal
	preview.run()
	if strings.Contains(out.String(), "Pending:") || !strings.Contains(out.String(), setupStageTitles[3]) {
		t.Fatal("the resume discarded known progress or omitted the saved stage")
	}
}

func TestTerminalSetupDashboardFallbackPreservesCompleteLink(t *testing.T) {
	t.Setenv("COLUMNS", "80")
	t.Setenv("NO_COLOR", "1")
	w, _ := setupTestWizard(t)
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Surfaces.Dashboard.AuthToken = strings.Repeat("fixture", 20)
	if _, err := operatorconfig.WriteAtomic(filepath.Join(w.home, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w.output = &out
	w.ui = onboarding.NewTerminal(strings.NewReader("1\n2\n"), &out)
	w.session.Project, w.session.WorkspaceRoot = "project", t.TempDir()
	w.deps.open = func(string) error { return errors.New("the browser is unavailable") }
	if err := w.dashboard(); err != nil {
		t.Fatal(err)
	}
	base, link, err := setupDashboardAddresses(w.home)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\n"+link+"\n") {
		t.Fatal("the fallback split the authenticated link")
	}
	if !strings.Contains(out.String(), "Dashboard address: "+base) || !strings.Contains(out.String(), "Select Open dashboard to retry.") {
		t.Fatal("the fallback omitted the base address or retry action")
	}
}
