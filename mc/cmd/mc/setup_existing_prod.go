//go:build !test_fake_routing

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	operatorconfig "mc/config"
	"mc/deployment"
	"mc/onboarding"
	"mc/verbs"
)

type setupDeployment struct {
	Exists         bool
	Active         bool
	Update         bool
	Checks         []verbs.DoctorFinding
	Services       map[string]bool
	Stops          deployment.ServiceStops
	Release        setupRelease
	Blocked        map[string]string
	DiagnosticPath string
}

func (state setupDeployment) ready(check string) bool {
	return slices.ContainsFunc(state.Checks, func(f verbs.DoctorFinding) bool { return f.Check == check && f.Status == "ok" })
}

var setupRequiredChecks = []string{"release", "mc-home", "spine", "deployment-identity", "routing", "runtime-auth", "container-runtime", "surfaces", "supervision", "agentsview", "wiki", "dashboard"}

// setupServices names each supervised unit for the operator, in stop order.
var setupServices = []struct{ unit, label string }{
	{"resident", "Resident service"}, {"dashboard", "Dashboard service"}, {"agentsview", "AgentsView service"},
}

func setupServiceMap(active bool) map[string]bool {
	states := map[string]bool{}
	for _, service := range setupServices {
		states[service.unit] = active
	}
	return states
}

func (state setupDeployment) complete() bool {
	for _, check := range setupRequiredChecks {
		if !state.ready(check) {
			return false
		}
	}
	return !state.Update
}

func inspectSetupDeployment(home string, args verbs.OnboardArgs) (setupDeployment, error) {
	state := setupDeployment{}
	for _, name := range []string{"config.toml", "deployment.uuid", "supervision", "release"} {
		if _, err := os.Lstat(filepath.Join(home, name)); err == nil {
			state.Exists = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return state, err
		}
	}
	if !state.Exists {
		return state, nil
	}
	var err error
	state.Services, err = setupServiceStates(home)
	if err != nil {
		return state, err
	}
	state.Stops, err = deployment.ReadServiceStops(home)
	if err != nil {
		return state, err
	}
	for _, active := range state.Services {
		state.Active = state.Active || active
	}
	docker := onboarding.DetectDocker(context.Background())
	if docker.CommandPath != "" {
		if err := os.Setenv("PATH", filepath.Dir(docker.CommandPath)+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
			return state, err
		}
	}
	state.Release = inspectSetupRelease(home, args, docker)
	state.Update = state.Release.differs()
	add := func(check string, ok bool, detail string) {
		status := "fail"
		if ok {
			status = "ok"
		}
		state.Checks = append(state.Checks, verbs.DoctorFinding{Check: check, Status: status, Detail: detail})
	}
	add("release", state.Release.matches(), "The installed components must match the proposed release.")
	resolver := func() (string, error) {
		if docker.State != onboarding.DockerReady {
			return "", errors.New(docker.Detail)
		}
		manager, err := productionHelperManager(execDockerRunner{})
		if err != nil {
			return "", err
		}
		return manager.inspectExisting()
	}
	report, err := productionDoctorWithHelper(resolver, false)
	if err != nil {
		return state, err
	}
	state.Checks = append(state.Checks, report.(map[string]any)["findings"].([]verbs.DoctorFinding)...)
	cfg, configErr := operatorconfig.Load(filepath.Join(home, "config.toml"))
	var authErr error
	if configErr != nil {
		authErr = configErr
	} else if len(cfg.Runtimes.EnabledBindings) == 0 {
		authErr = errors.New("Select an agent connection.")
	} else {
		authErr = deployment.ValidateRuntimeGrantStore(home, cfg.Runtimes.EnabledBindings, os.Environ())
	}
	for i := range state.Checks {
		if state.Checks[i].Check == "runtime-auth" {
			state.Checks[i].Status, state.Checks[i].Detail = "ok", "Agent credentials are configured."
			if authErr != nil {
				state.Checks[i].Status, state.Checks[i].Detail = "fail", authErr.Error()
			}
		}
	}
	dashboardErr := configErr
	if dashboardErr == nil {
		if state.Update || !state.ready("supervision") || !state.ready("spine") || !state.ready("container-runtime") {
			dashboardErr = errors.New("not checked: the dashboard access check requires compatible, active services")
		} else {
			dashboardErr = setupCheckDashboard(home)
		}
	}
	detail := "The dashboard answered its access check."
	if dashboardErr != nil {
		detail = dashboardErr.Error()
		var failure *setupFailure
		if errors.As(dashboardErr, &failure) && len(failure.details) > 0 {
			detail += "\n" + strings.Join(failure.details, "\n")
		}
	}
	add("dashboard", dashboardErr == nil, detail)
	if strings.HasPrefix(detail, "not checked:") {
		state.Blocked = map[string]string{"dashboard": "services"}
	}
	state.blockViewerChecks()
	if configErr == nil && state.ready("supervision") {
		policy, policyErr := verbs.LoadOnboardHostConfig()
		matches := false
		if policyErr == nil {
			matches, policyErr = deployment.SupervisionPolicyMatches(home, policy.SupervisionPolicy)
		}
		if policyErr != nil || !matches {
			for i := range state.Checks {
				if state.Checks[i].Check == "supervision" {
					state.Checks[i].Status, state.Checks[i].Detail = "fail", "The service configuration differs from the current choices."
					if policyErr != nil {
						state.Checks[i].Detail = policyErr.Error()
					}
				}
			}
		}
	}
	state.acceptExplicitStops(home)
	state.normalizeChecks()
	return state, nil
}

func (state *setupDeployment) acceptExplicitStops(home string) {
	userHome, err := os.UserHomeDir()
	if err != nil || state.Release.differs() {
		return
	}
	for _, unit := range deployment.SupervisedUnits {
		if !state.Stops[unit] || state.Services[unit] {
			continue
		}
		if err := deployment.InspectServiceDefinition(home, filepath.Join(userHome, "Library", "LaunchAgents"), unit, releaseBuildID); err != nil {
			continue
		}
		for i := range state.Checks {
			check := &state.Checks[i]
			if unit == "agentsview" && check.Check == "agentsview" || unit == "dashboard" && (check.Check == "dashboard" || check.Check == "wiki") {
				check.Status, check.Detail = "ok", "The installed files passed their check. The explicit service stop remains in effect."
				delete(state.Blocked, check.Check)
			}
		}
	}
}

// blockViewerChecks keeps a stopped service from also failing the viewer
// checks. The services check names that failure once.
func (state *setupDeployment) blockViewerChecks() {
	if state.ready("supervision") {
		return
	}
	for i := range state.Checks {
		check := &state.Checks[i]
		if (check.Check == "agentsview" || check.Check == "wiki") && check.Status != "ok" {
			check.Detail = "not checked: the viewer checks require active services"
			if state.Blocked == nil {
				state.Blocked = map[string]string{}
			}
			state.Blocked[check.Check] = "services"
		}
	}
}

func setupServiceStates(home string) (map[string]bool, error) {
	names, err := deployment.RuntimeNames(home)
	if err != nil {
		return nil, err
	}
	states := map[string]bool{}
	for _, unit := range deployment.SupervisedUnits {
		loaded, err := launchdLabelLoaded(deployment.UnitLabel(names, unit))
		if err != nil {
			return states, err
		}
		states[unit] = loaded
	}
	return states, nil
}

func (m *helperManager) inspectExisting() (string, error) {
	image, exists, err := inspectJSON[helperImageInspect](m.docker, "image", m.imageRef)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", errors.New("The agent image is absent.")
	}
	container, exists, err := inspectJSON[helperContainerInspect](m.docker, "container", m.names.Helper)
	if err != nil {
		return "", err
	}
	if !exists || !container.State.Running || !validContainerID(container.ID) || !m.helperExact(container, image.ID) {
		return "", errors.New("The deployment helper requires preparation.")
	}
	return container.ID, nil
}

var setupCheckNames = map[string]string{
	"release": "Release files", "mc-home": "Deployment files", "spine": "Database",
	"deployment-identity": "Deployment identity", "routing": "Agent assignments",
	"runtime-auth": "Agent credentials", "container-runtime": "Agent containers",
	"surfaces": "Dashboard alerts", "supervision": "Services", "dashboard": "Dashboard access",
	"agentsview": "AgentsView", "wiki": "Wiki address",
}

func (w *setupWizard) inspectExistingDeployment() (bool, error) {
	for {
		var state setupDeployment
		if err := w.operation("Inspect existing setup", func() error {
			var err error
			state, err = w.deps.inspect(w.home, w.answers)
			return err
		}); err != nil {
			if errors.Is(err, onboarding.ErrBack) {
				continue
			}
			return false, err
		}
		if !state.Exists {
			if w.session.Stage > 0 {
				w.session.Stage = 1
				w.session.Completed = []string{"welcome"}
			}
			return false, nil
		}
		if state.Release.Relation == "older" && state.Release.SourceState == "published" {
			w.ui.Panel("Keep the installed release", append(state.Release.lines(), "The installed release is newer. Setup left it intact."))
			return true, nil
		}
		state.normalizeChecks()
		if !state.complete() {
			path, err := writeSetupDiagnostic(w.home, "Inspect existing setup", &state, nil)
			if err != nil {
				return false, err
			}
			state.DiagnosticPath = path
		}
		requested := setupRequestedChanges(w.answers)
		if state.complete() && !requested {
			if w.deps.finishExisting != nil {
				if err := w.deps.finishExisting(); err != nil {
					return true, err
				}
			}
			lines := append(state.Release.lines(), "This release and its required services passed the current checks.")
			for _, service := range setupServices {
				status := "active"
				if state.Stops[service.unit] {
					status = "stopped by your choice"
				}
				lines = append(lines, service.label+": "+status+".")
			}
			w.ui.Panel("Culler is already set up", lines)
			if err := w.commandAvailability(); err != nil {
				return true, err
			}
			if state.Stops["dashboard"] {
				w.ui.Panel("Dashboard stopped", []string{"Your dashboard stop choice remains in effect.", "Start the dashboard when you need it."})
				if terminal, ok := w.ui.(interface{ Literal(string) }); ok {
					terminal.Literal(deployment.CLICommand(w.home, "start dashboard"))
				}
				_, err := w.ui.Choose("Finish setup", []onboarding.Choice{setupChoice("finish", "Close setup", "")}, 0)
				return true, err
			}
			choice, err := w.ui.Choose("Your setup is ready", []onboarding.Choice{
				setupChoice("open", "Open dashboard", ""), setupChoice("finish", "Close setup", ""),
			}, 0)
			if err != nil {
				if errors.Is(err, onboarding.ErrBack) {
					continue
				}
				return true, err
			}
			if choice == "open" {
				_, address, err := setupDashboardAddresses(w.home)
				if err != nil {
					if errors.Is(err, onboarding.ErrBack) {
						continue
					}
					return true, err
				}
				if err := w.operation("Open dashboard", func() error { return w.deps.open(address) }); err != nil {
					if errors.Is(err, onboarding.ErrBack) {
						continue
					}
					return true, err
				}
			}
			return true, nil
		}
		choice, err := w.chooseExistingDeployment(state, requested)
		if err != nil {
			if errors.Is(err, onboarding.ErrBack) {
				continue
			}
			return true, err
		}
		switch choice {
		case "finish":
			w.serviceNotice()
			return true, nil
		case "continue", "update":
			if w.deps.beforeChange != nil {
				if err := w.progressOperation("Prepare the release", func(report func(string)) error {
					report("Check the image and database before service changes")
					return w.deps.beforeChange(w.home, w.answers, report)
				}); err != nil {
					return false, err
				}
			}
			w.rebuildImage = state.Update
			if state.Active {
				if w.deps.stop == nil {
					return false, errors.New("The service stop operation is unavailable.")
				}
				w.servicesTouched = true
				if err := w.operation("Stop Culler services", func() error { return w.deps.stop(w.home) }); err != nil {
					if errors.Is(err, onboarding.ErrBack) {
						continue
					}
					return false, err
				}
				w.servicesStopped = true
				w.serviceStates = setupServiceMap(false)
			}
			if requested {
				state.Checks = slices.DeleteFunc(state.Checks, func(f verbs.DoctorFinding) bool {
					if setupAgentChanges(w.answers) && (f.Check == "runtime-auth" || f.Check == "routing" || f.Check == "container-runtime") {
						return true
					}
					if (w.answers.RoutingFile != "" || w.answers.AcceptDefaultRouting) && f.Check == "routing" {
						return true
					}
					return f.Check == "surfaces"
				})
			}
			if err := w.resumeCheckedDeployment(state); err != nil {
				if errors.Is(err, onboarding.ErrBack) {
					continue
				}
				return false, err
			}
			return false, nil
		}
	}
}

func (w *setupWizard) chooseExistingDeployment(state setupDeployment, requested bool) (string, error) {
	title := "Continue your setup"
	lines := []string{"Welcome back. Continue from your last completed step."}
	action := setupChoice("continue", "Continue setup", "Keep the completed steps.")
	needsRepair := false
	if state.Update {
		title = "Update Culler"
		action = setupChoice("update", "Stop and update", "Interrupt active runs and apply this release.")
	} else if requested {
		title = "Apply your requested changes"
		action = setupChoice("continue", "Apply setup changes", "Apply the requested choices.")
	}
	if state.Update {
		lines = nil
		if w.deps.assessCandidate != nil {
			disclosures, err := w.deps.assessCandidate()
			if err != nil {
				return "", err
			}
			lines = append(lines, disclosures...)
			needsRepair = len(disclosures) > 0
		}
	} else if requested {
		lines = []string{"Setup will apply your requested changes."}
	}
	lines = append(lines, state.Release.lines()...)
	if state.Active && !state.Update {
		lines = append(lines, "Culler will pause new tasks while setup updates its services.", "Active agents can finish their tasks.")
	}
	lines = append(lines, "You choose when to start Culler.")
	for {
		w.ui.Panel(title, lines)
		if state.Update {
			warning := []string{"This update stops Culler and interrupts active runs.", "Saved data remains. Interrupted work can require a retry."}
			if needsRepair {
				warning = append(warning, "Affected profiles need repair in the dashboard. Their work stays stopped.")
			}
			w.ui.Panel("Before the update", warning)
		}
		choice, err := w.ui.Choose("Choose an action", []onboarding.Choice{
			action, setupChoice("finish", "Cancel", "Keep the current deployment."),
		}, 0)
		if errors.Is(err, onboarding.ErrBack) {
			continue
		}
		return choice, err
	}
}

func (w *setupWizard) resumeCheckedDeployment(state setupDeployment) error {
	w.session.Completed = []string{"welcome"}
	w.session.Stage = 1
	w.existingSections = map[string]bool{}
	for section, check := range map[string]string{"routing": "routing", "surfaces": "surfaces", "container": "container-runtime"} {
		w.existingSections[section] = state.ready(check) && state.ready("spine") && !state.Update
	}
	if !state.Update && state.ready("release") && state.ready("mc-home") && state.ready("spine") && state.ready("deployment-identity") {
		w.session.Stage = 2
		w.session.Completed = append(w.session.Completed, "prepare")
		w.session.ReleaseID = releaseBuildID
	}
	cfg, err := operatorconfig.Load(filepath.Join(w.home, "config.toml"))
	if err == nil {
		// Current configuration takes precedence over obsolete saved setup choices.
		w.session.DashboardBind = cfg.Surfaces.Dashboard.Bind
		w.session.GitAuthorName, w.session.GitAuthorEmail = cfg.Git.AuthorName, cfg.Git.AuthorEmail
		if state.ready("routing") {
			w.session.RoutingFile = filepath.Join(w.home, "routing.md")
		}
		w.session.Tunables = map[string]int{}
		for _, field := range setupTunableFields(cfg) {
			w.session.Tunables[field.name] = field.value
		}
		if state.ready("runtime-auth") {
			if err := w.adoptConfiguredAgents(cfg); err != nil {
				return err
			}
			if w.session.Stage == 2 {
				w.session.Stage = 3
				w.session.Completed = append(w.session.Completed, "agents")
			}
		}
	}
	w.session.StageName = setupStageIDs[w.session.Stage]
	w.mergeAnswers()
	if err := w.save(); err != nil {
		return fmt.Errorf("save setup choices: %w", err)
	}
	return nil
}

func setupRequestedChanges(args verbs.OnboardArgs) bool {
	args.ReleaseSource, args.HostReleaseSource, args.Spine = "", "", ""
	return !reflect.DeepEqual(args, verbs.OnboardArgs{})
}

func stopSetupServices(home string) error {
	names, err := deployment.RuntimeNames(home)
	if err != nil {
		return err
	}
	controller := launchdController{}
	return stopSetupServicesWith(names, controller, time.Sleep)
}

func stopSetupServicesWith(names deployment.Names, controller deployment.LaunchdController, sleep func(time.Duration)) error {
	for _, unit := range deployment.SupervisedUnits {
		label := deployment.UnitLabel(names, unit)
		loaded, err := controller.Loaded(label)
		if err != nil {
			return err
		}
		if !loaded {
			continue
		}
		if err := controller.Bootout(label); err != nil {
			return err
		}
		for attempt := 0; ; attempt++ {
			loaded, err := controller.Loaded(label)
			if err != nil {
				return err
			}
			if !loaded {
				break
			}
			if attempt == 89 {
				return fmt.Errorf("the service %s did not stop within 90 seconds", label)
			}
			sleep(time.Second)
		}
	}
	return nil
}

func setupAgentChanges(args verbs.OnboardArgs) bool {
	return args.RuntimeBindings != "" || args.AcquireRuntimeAuth
}

func (w *setupWizard) adoptConfiguredAgents(cfg operatorconfig.Config) error {
	w.existingAgentAccess = len(cfg.Runtimes.EnabledBindings) > 0
	w.session.RuntimeBindings = slices.Clone(cfg.Runtimes.EnabledBindings)
	for _, binding := range w.session.RuntimeBindings {
		identity, err := w.deps.identity(w.home, cfg.Runtimes.Bindings[binding].Credential)
		if err != nil {
			return err
		}
		w.session.RuntimeVerified[binding] = true
		w.session.RuntimeIdentities[binding] = setupAccessIdentity(cfg, binding, identity)
	}
	return nil
}

func (w *setupWizard) recheckConfiguredAgents() error {
	if w.existingSections == nil || setupAgentChanges(w.answers) {
		return nil
	}
	w.existingAgentAccess = false
	cfg, err := operatorconfig.Load(filepath.Join(w.home, "config.toml"))
	if err != nil {
		return err
	}
	if len(cfg.Runtimes.EnabledBindings) == 0 || w.deps.runtimeReady(w.home, cfg.Runtimes.EnabledBindings) != nil {
		return nil
	}
	return w.adoptConfiguredAgents(cfg)
}
