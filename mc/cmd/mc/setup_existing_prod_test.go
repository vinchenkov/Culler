//go:build !test_fake_routing

package main

import (
	"errors"
	"mc/deployment"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	operatorconfig "mc/config"
	"mc/onboarding"
	"mc/verbs"
)

func completeSetupDeployment() setupDeployment {
	state := setupDeployment{Exists: true, Active: true}
	for _, check := range []string{"release", "mc-home", "spine", "deployment-identity", "routing", "runtime-auth", "container-runtime", "surfaces", "supervision", "agentsview", "wiki", "dashboard"} {
		state.Checks = append(state.Checks, verbs.DoctorFinding{Check: check, Status: "ok"})
	}
	return state
}

func TestExistingSetupCompleteUsesCurrentChecksWithoutChanges(t *testing.T) {
	for _, saved := range []int{0, 1, 5} {
		t.Run(string(rune('0'+saved)), func(t *testing.T) {
			w, u := setupTestWizard(t, "open")
			w.session.Stage = saved
			w.session.ReleaseID = "old-release"
			w.deps.inspect = func(string, verbs.OnboardArgs) (setupDeployment, error) { return completeSetupDeployment(), nil }
			w.deps.section = func(string, verbs.OnboardArgs) error { t.Fatal("complete setup performed a write"); return nil }
			w.deps.publish = func() error { t.Fatal("complete setup replaced its executable"); return nil }
			w.deps.stop = func(string) error { t.Fatal("complete setup stopped its services"); return nil }
			opened := false
			w.deps.open = func(url string) error { opened = strings.HasSuffix(url, "#token=fixture-dashboard-access"); return nil }
			if err := w.run(); err != nil {
				t.Fatal(err)
			}
			if !opened || !strings.Contains(strings.Join(u.panels, "\n"), "already set up") {
				t.Fatal("complete setup omitted the dashboard or status")
			}
		})
	}
}

func TestExistingSetupRequiresEveryCurrentCheck(t *testing.T) {
	state := completeSetupDeployment()
	for i := range state.Checks {
		current := completeSetupDeployment()
		current.Checks[i].Status = "fail"
		if current.complete() {
			t.Fatalf("completion ignored %s", current.Checks[i].Check)
		}
	}
	state.Update = true
	if state.complete() {
		t.Fatal("completion ignored the release change")
	}
}

func TestExistingSetupUpdateStopsBeforePreparationAndPreservesChoices(t *testing.T) {
	w, u := setupTestWizard(t, "update")
	state := completeSetupDeployment()
	state.Update = true
	for i := range state.Checks {
		if state.Checks[i].Check == "release" {
			state.Checks[i].Status = "fail"
		}
	}
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	w.deps.inspect = func(string, verbs.OnboardArgs) (setupDeployment, error) { return state, nil }
	stopped := false
	w.deps.stop = func(string) error {
		if u.choices[0][0].Label != "Update Culler" || !strings.Contains(strings.Join(u.panels, "\n"), "Culler will pause new tasks") {
			t.Fatal("setup omitted the stop explanation")
		}
		stopped = true
		return nil
	}
	handled, err := w.inspectExistingDeployment()
	if err != nil || handled || !stopped {
		t.Fatalf("inspect: handled=%v stopped=%v error=%v", handled, stopped, err)
	}
	if w.session.Stage != 1 || !slices.Equal(w.session.RuntimeBindings, cfg.Runtimes.EnabledBindings) {
		t.Fatal("update lost preparation or configured agents")
	}
	if w.session.RoutingFile != filepath.Join(w.home, "routing.md") {
		t.Fatal("update lost the role assignments")
	}
	if w.existingSections["surfaces"] {
		t.Fatal("update trusted database projections before preparation")
	}
}

func TestExistingSetupStopFailurePreventsPreparation(t *testing.T) {
	w, _ := setupTestWizard(t, "update", ":exit")
	state := completeSetupDeployment()
	state.Update = true
	w.deps.inspect = func(string, verbs.OnboardArgs) (setupDeployment, error) { return state, nil }
	w.deps.stop = func(string) error { return errors.New("service remains loaded") }
	w.deps.section = func(string, verbs.OnboardArgs) error { t.Fatal("setup continued after a failed stop"); return nil }
	if err := w.run(); !errors.Is(err, onboarding.ErrExit) {
		t.Fatalf("run = %v", err)
	}
}

func TestExistingSetupOffersOnlyUpdateAndClose(t *testing.T) {
	for _, answer := range []string{"finish", ":exit", ":back"} {
		t.Run(answer, func(t *testing.T) {
			answers := []string{answer}
			if answer == ":back" {
				answers = append(answers, "finish")
			}
			w, u := setupTestWizard(t, answers...)
			state := completeSetupDeployment()
			state.Update = true
			checks := 0
			w.deps.inspect = func(string, verbs.OnboardArgs) (setupDeployment, error) { checks++; return state, nil }
			w.deps.stop = func(string) error { t.Fatal("The menu stopped the services."); return nil }
			w.deps.publish = func() error { t.Fatal("The menu replaced the executable."); return nil }
			w.deps.section = func(string, verbs.OnboardArgs) error { t.Fatal("The menu changed the deployment."); return nil }
			handled, err := w.inspectExistingDeployment()
			if !handled || checks != 1 || (answer == ":exit" && !errors.Is(err, onboarding.ErrExit)) || (answer != ":exit" && err != nil) {
				t.Fatalf("handled=%v checks=%d error=%v", handled, checks, err)
			}
			for _, choices := range u.choices {
				if len(choices) != 2 || choices[0].ID != "update" || choices[1].ID != "finish" {
					t.Fatalf("choices=%v", choices)
				}
			}
			if len(u.answers) != 0 || w.session.Stage != 0 || w.servicesStopped {
				t.Fatal("The menu changed setup progress.")
			}
		})
	}
}

func TestExistingSetupUpdateWithStoppedServices(t *testing.T) {
	w, u := setupTestWizard(t, "update")
	state := completeSetupDeployment()
	state.Update, state.Active = true, false
	w.deps.inspect = func(string, verbs.OnboardArgs) (setupDeployment, error) { return state, nil }
	w.deps.stop = func(string) error { t.Fatal("The setup repeated the service stop."); return nil }
	handled, err := w.inspectExistingDeployment()
	if err != nil || handled || w.session.Stage != 1 || w.servicesStopped {
		t.Fatalf("handled=%v stage=%d error=%v", handled, w.session.Stage, err)
	}
	if u.choices[0][0].Label != "Update Culler" || strings.Contains(strings.Join(u.panels, "\n"), "Culler will pause new tasks") {
		t.Fatal("The update menu misrepresented the service state.")
	}
}

func TestExistingSetupPartialResumesWithoutRepeatingCompletedSections(t *testing.T) {
	w, u := setupTestWizard(t, "continue", "start", "finish")
	state := completeSetupDeployment()
	state.Active = false
	for i := range state.Checks {
		if state.Checks[i].Check == "supervision" {
			state.Checks[i].Status = "fail"
		}
	}
	// The actual assignment file is required by the review.
	w.session.RoutingFile = setupRoleFixture(t, w)
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Runtimes.EnabledBindings = slices.Clone(w.session.RuntimeBindings)
	if _, err := operatorconfig.WriteAtomic(filepath.Join(w.home, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	state.Checks = slices.DeleteFunc(state.Checks, func(f verbs.DoctorFinding) bool { return f.Check == "routing" })
	w.deps.inspect = func(string, verbs.OnboardArgs) (setupDeployment, error) { return state, nil }
	calls := []string{}
	w.deps.section = func(section string, _ verbs.OnboardArgs) error { calls = append(calls, section); return nil }
	w.deps.publish = func() error { calls = append(calls, "publish"); return nil }
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(calls, "home") || slices.Contains(calls, "surfaces") || slices.Contains(calls, "container") || calls[0] != "publish" || !slices.Contains(calls, "supervision") {
		t.Fatalf("sections = %v", calls)
	}
	panels := strings.Join(u.panels, "\n")
	if !strings.Contains(panels, "Welcome back.") || !strings.Contains(panels, "You choose when to start Culler.") {
		t.Fatal("The setup omitted its next action.")
	}
}

func TestSetupSectionDiagnosticsPreserveFailuresAndRemoveSecrets(t *testing.T) {
	t.Setenv("FIXTURE_API_KEY", "fixture-private-value")
	for _, stdout := range []string{`{"error":{"message":"database reset requires com.culler.fixture.resident to be unloaded; api_key=fixture-private-value"}}`, ""} {
		err := setupSectionResult("home", 1, []byte(stdout), []byte("database reset requires com.culler.fixture.resident to be unloaded; api_key=fixture-private-value"))
		text := strings.Join(setupDiagnosticLines("Prepare Culler", err), "\n")
		if !strings.Contains(text, "resident to be unloaded") || !strings.Contains(text, "Exit code: 1") || strings.Contains(text, "fixture-private-value") || strings.Contains(text, "unknown") {
			t.Fatalf("diagnostic = %s", text)
		}
	}
}

func TestSetupRequestedChangesExcludesInstallerContext(t *testing.T) {
	args, err := parseTerminalSetupArgs([]string{"--release-source", "/fixture/runner", "--host-release-source", "/fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if setupRequestedChanges(args) {
		t.Fatal("the installer context became a setup change")
	}
	args.DashboardBind = "127.0.0.1:7888"
	if !setupRequestedChanges(args) {
		t.Fatal("the requested address change was ignored")
	}
}

func TestExistingHelperInspectionDoesNotCreateOrReplaceContainers(t *testing.T) {
	names, err := deployment.RuntimeNames("/fixture/deployment")
	if err != nil {
		t.Fatal(err)
	}
	image := helperImageInspect{ID: "sha256:" + strings.Repeat("a", 64), OS: "linux", Architecture: "arm64"}
	for _, scenario := range []string{"ready", "stopped", "stale", "absent"} {
		t.Run(scenario, func(t *testing.T) {
			helper := exactHelperFixture(names, image.ID)
			helper.State.Running = scenario != "stopped"
			if scenario == "stale" {
				helper.Image = "sha256:" + strings.Repeat("b", 64)
			}
			step := dockerStep{want: []string{"container", "inspect", "--format", "{{json .}}", names.Helper}, out: inspectBytes(t, helper)}
			if scenario == "absent" {
				step.out = "Error: No such container: " + names.Helper
				step.err = errors.New("exit status 1")
			}
			runner := &scriptedDocker{t: t, steps: []dockerStep{
				{want: []string{"image", "inspect", "--format", "{{json .}}", productionImageRef}, out: inspectBytes(t, image)}, step,
			}}
			manager := helperManager{docker: runner, names: names, imageRef: productionImageRef}
			id, err := manager.inspectExisting()
			if (err == nil) != (scenario == "ready") {
				t.Fatalf("inspect = %q: %v", id, err)
			}
			if err == nil && id != helper.ID {
				t.Fatal("inspection lost the immutable container identity")
			}
			if runner.calls != len(runner.steps) {
				t.Fatalf("calls=%d", runner.calls)
			}
		})
	}
}

type setupStopController struct {
	loaded map[string]bool
	calls  []string
}

func (c *setupStopController) Loaded(label string) (bool, error) { return c.loaded[label], nil }
func (c *setupStopController) Bootstrap(string) error {
	return errors.New("unexpected service activation")
}
func (c *setupStopController) Bootout(label string) error {
	c.calls = append(c.calls, label)
	c.loaded[label] = false
	return nil
}

func TestSetupStopsOnlyTheSelectedDeployment(t *testing.T) {
	names, err := deployment.RuntimeNames("/fixture/deployment")
	if err != nil {
		t.Fatal(err)
	}
	labels := []string{"com.culler." + names.Suffix + ".resident", "com.culler." + names.Suffix + ".dashboard", "com.culler." + names.Suffix + ".agentsview"}
	controller := &setupStopController{loaded: map[string]bool{labels[0]: true, labels[1]: true, labels[2]: true, "com.culler.other.resident": true}}
	if err := stopSetupServicesWith(names, controller, func(time.Duration) { t.Fatal("stopped service required a wait") }); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(controller.calls, labels) || !controller.loaded["com.culler.other.resident"] {
		t.Fatalf("stopped=%v", controller.calls)
	}
}

func TestExistingSetupAbsentDeploymentInvalidatesSavedCompletion(t *testing.T) {
	w, _ := setupTestWizard(t)
	w.session.Stage = 5
	w.session.Completed = slices.Clone(setupStageIDs)
	w.deps.inspect = func(string, verbs.OnboardArgs) (setupDeployment, error) { return setupDeployment{}, nil }
	handled, err := w.inspectExistingDeployment()
	if err != nil || handled || w.session.Stage != 1 || !slices.Equal(w.session.Completed, []string{"welcome"}) {
		t.Fatalf("resume: handled=%v stage=%d complete=%v err=%v", handled, w.session.Stage, w.session.Completed, err)
	}
}

func TestSetupAddressChangePreservesConfiguredAgents(t *testing.T) {
	w, _ := setupTestWizard(t, "continue")
	w.session.RoutingFile = setupRoleFixture(t, w)
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Runtimes.EnabledBindings = slices.Clone(w.session.RuntimeBindings)
	if _, err := operatorconfig.WriteAtomic(filepath.Join(w.home, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	w.answers.DashboardBind = "127.0.0.1:7444"
	w.deps.inspect = func(string, verbs.OnboardArgs) (setupDeployment, error) { return completeSetupDeployment(), nil }
	w.deps.stop = func(string) error { return nil }
	handled, err := w.inspectExistingDeployment()
	if err != nil || handled || w.session.Stage != 3 || !w.existingAgentAccess || !w.existingSections["routing"] || w.existingSections["surfaces"] {
		t.Fatalf("resume: handled=%v stage=%d sections=%v err=%v", handled, w.session.Stage, w.existingSections, err)
	}
	if w.arguments().DashboardBind != w.answers.DashboardBind {
		t.Fatal("resume lost the requested address")
	}
}

func TestSetupPreparationRechecksCredentialsAfterConversion(t *testing.T) {
	w, _ := setupTestWizard(t)
	w.session.RoutingFile = setupRoleFixture(t, w)
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Runtimes.EnabledBindings = slices.Clone(w.session.RuntimeBindings)
	if _, err := operatorconfig.WriteAtomic(filepath.Join(w.home, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	w.existingSections = map[string]bool{}
	prepared := false
	w.deps.section = func(section string, _ verbs.OnboardArgs) error {
		if section == "home" {
			prepared = true
		}
		return nil
	}
	w.deps.runtimeReady = func(string, []string) error {
		if !prepared {
			t.Fatal("credential check preceded deployment preparation")
		}
		return nil
	}
	if err := w.prepare(); err != nil {
		t.Fatal(err)
	}
	if !w.existingAgentAccess || !slices.Equal(w.session.RuntimeBindings, cfg.Runtimes.EnabledBindings) {
		t.Fatal("preparation lost the converted credentials")
	}
}

func TestSetupRetainsAgentsWithSharedCredentialNames(t *testing.T) {
	w, _ := setupTestWizard(t)
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	binding := cfg.Runtimes.Bindings[setupBindingForKind(cfg, operatorconfig.CredentialKindCodexOAuth)]
	cfg.Runtimes.Bindings["fixture-alias"] = binding
	cfg.Runtimes.EnabledBindings = []string{"fixture-alias"}
	w.deps.identity = func(_ string, id string) (string, error) {
		if id != binding.Credential {
			t.Fatalf("credential identity used the agent name: %q", id)
		}
		return strings.Repeat("a", 64), nil
	}
	if err := w.adoptConfiguredAgents(cfg); err != nil {
		t.Fatal(err)
	}
	if !w.existingAgentAccess || !w.session.RuntimeVerified["fixture-alias"] {
		t.Fatal("setup lost the configured agent")
	}
}

func TestExistingSetupBackReturnsToTheCurrentChoices(t *testing.T) {
	w, _ := setupTestWizard(t, ":back", "finish")
	w.deps.inspect = func(string, verbs.OnboardArgs) (setupDeployment, error) { return completeSetupDeployment(), nil }
	if err := w.run(); err != nil {
		t.Fatalf("back became a setup failure: %v", err)
	}
}

func TestSetupServicesNameEverySupervisedUnit(t *testing.T) {
	units := []string{}
	for _, service := range setupServices {
		units = append(units, service.unit)
	}
	if !slices.Equal(units, deployment.SupervisedUnits) {
		t.Fatalf("setup services = %v, supervised units = %v", units, deployment.SupervisedUnits)
	}
	for unit, active := range setupServiceMap(true) {
		if !active || !slices.Contains(units, unit) {
			t.Fatalf("service map = %v", setupServiceMap(true))
		}
	}
}

func TestStoppedServicesLeaveTheViewerChecksUnchecked(t *testing.T) {
	state := completeSetupDeployment()
	for i := range state.Checks {
		switch state.Checks[i].Check {
		case "supervision", "agentsview", "wiki":
			state.Checks[i].Status = "fail"
			state.Checks[i].Detail = "LaunchAgent is not loaded"
		}
	}
	state.blockViewerChecks()
	state.normalizeChecks()
	lines := strings.Join(state.issueLines(), "\n")
	for _, want := range []string{"Services: failed.", "These checks require active services.", "Unchecked: AgentsView, Wiki address."} {
		if !strings.Contains(lines, want) {
			t.Fatalf("issue lines lack %q:\n%s", want, lines)
		}
	}
	if strings.Contains(lines, "AgentsView: failed") || strings.Contains(lines, "Wiki address: failed") {
		t.Fatalf("a stopped service failed the viewer checks too:\n%s", lines)
	}

	state = completeSetupDeployment()
	for i := range state.Checks {
		if state.Checks[i].Check == "agentsview" || state.Checks[i].Check == "wiki" {
			state.Checks[i].Status = "fail"
		}
	}
	state.blockViewerChecks()
	state.normalizeChecks()
	lines = strings.Join(state.issueLines(), "\n")
	for _, want := range []string{"AgentsView: failed. AgentsView did not pass its health check.", "Wiki address: failed. The wiki address check failed."} {
		if !strings.Contains(lines, want) {
			t.Fatalf("issue lines lack %q:\n%s", want, lines)
		}
	}
	if state.complete() {
		t.Fatal("setup completed with failed viewer checks")
	}
}
