//go:build !test_fake_routing

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	operatorconfig "mc/config"
	"mc/deployment"
	"mc/onboarding"
	"mc/opencodecatalog"
	"mc/verbs"
)

func (w *setupWizard) mergeAnswers() {
	s, a := &w.session, w.answers
	if s.StageName == "" && s.Stage > 3 {
		s.Stage--
	}
	s.StageName = setupStageIDs[s.Stage]
	for _, pair := range []struct {
		target *string
		value  string
	}{
		{&s.Project, a.Project}, {&s.WorkspaceRoot, a.WorkspaceRoot},
		{&s.GitAuthorName, a.GitAuthorName}, {&s.GitAuthorEmail, a.GitAuthorEmail},
		{&s.ProjectGitAuthorName, a.ProjectGitAuthorName}, {&s.ProjectGitAuthorEmail, a.ProjectGitAuthorEmail},
		{&s.Vision, a.ProjectVisionAnswer}, {&s.RoutingFile, a.RoutingFile}, {&s.DashboardBind, a.DashboardBind},
	} {
		if pair.value != "" {
			*pair.target = pair.value
		}
	}

	if a.AcceptDefaultGitIdentity {
		s.GitAuthorName, s.GitAuthorEmail = "", ""
	}
	if a.AcceptDefaultRouting {
		s.RoutingFile = ""
	}
	if a.AcceptDefaultDashboardBind {
		s.DashboardBind = ""
	}
	if s.Tunables == nil || a.TunablesConfirmed {
		s.Tunables = map[string]int{}
	}
	for name, value := range setupTunableAnswers(a) {
		if value > 0 {
			s.Tunables[name] = value
		}
	}
	if s.RuntimeVerified == nil {
		s.RuntimeVerified = map[string]bool{}
	}
	if s.RuntimeIdentities == nil {
		s.RuntimeIdentities = map[string]string{}
	}
	// An unfinished agent test returns setup to the connection choices.
	if s.Stage > 2 && (a.RuntimeBindings != "" || a.AcquireRuntimeAuth || len(s.RuntimeIncomplete) > 0) {
		s.Stage = 2
	}
}

func setupTunableAnswers(a verbs.OnboardArgs) map[string]int {
	return map[string]int{
		"review-wip-cap": a.ReviewWIPCap, "timeout-minutes": a.TimeoutMinutes,
		"grace-minutes": a.GraceMinutes, "heartbeat-interval-s": a.HeartbeatIntervalS,
		"spawn-grace-s": a.SpawnGraceS, "hard-deadline-minutes": a.HardDeadlineMinutes,
		"homie-idle-timeout-s": a.HomieIdleTimeoutS,
	}
}

func (w *setupWizard) arguments() verbs.OnboardArgs {
	a, s := w.answers, w.session
	a.Project, a.WorkspaceRoot = "", ""
	a.ProjectGitAuthorName, a.ProjectGitAuthorEmail = "", ""
	a.ProjectVisionAnswer, a.AcceptGeneratedSandbox = "", false
	a.RuntimeBindings = strings.Join(s.RuntimeBindings, ",")
	a.AcceptDefaultRouting, a.RoutingFile = s.RoutingFile == "", s.RoutingFile
	a.GitAuthorName, a.GitAuthorEmail = s.GitAuthorName, s.GitAuthorEmail
	a.AcceptDefaultGitIdentity = s.GitAuthorName == ""
	a.DashboardBind, a.AcceptDefaultDashboardBind = s.DashboardBind, s.DashboardBind == ""
	if a.DiscordMode == "" && w.existingSections == nil {
		a.DiscordMode = "disabled"
	}
	a.ActivateSupervision = false
	a.TunablesConfirmed = len(s.Tunables) == 0
	a.ReviewWIPCap = s.Tunables["review-wip-cap"]
	a.TimeoutMinutes = s.Tunables["timeout-minutes"]
	a.GraceMinutes = s.Tunables["grace-minutes"]
	a.HeartbeatIntervalS = s.Tunables["heartbeat-interval-s"]
	a.SpawnGraceS = s.Tunables["spawn-grace-s"]
	a.HardDeadlineMinutes = s.Tunables["hard-deadline-minutes"]
	a.HomieIdleTimeoutS = s.Tunables["homie-idle-timeout-s"]
	return a
}

func (w *setupWizard) revalidate() error {
	w.ui.Panel("Resume your setup", []string{"Culler will check your saved choices."})
	if w.session.ReleaseID != releaseBuildID {
		w.session.Stage = 1
		w.ui.Panel("Prepare your Mac again", []string{"This version of Culler needs a new check."})
		return w.save()
	}
	status := w.deps.docker(context.Background())
	if string(status.State) != "ready" {
		w.session.Stage = 1
		w.problem("Docker Desktop requires attention", []string{status.Detail, "Your agent choices remain saved."})
		return w.save()
	}
	if status.CommandPath != "" {
		if err := os.Setenv("PATH", filepath.Dir(status.CommandPath)+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
			return err
		}
	}
	for _, section := range []string{"preflight", "home"} {
		label := "Check " + setupSectionName(section)
		if err := w.ui.Activity(label, func() error { return w.deps.section(section, w.arguments()) }); err != nil {
			w.session.Stage = 1
			// The activity line above states the failure.
			w.problem("", append(w.failureLines(label, err), "Your completed choices remain saved."))
			return w.save()
		}
	}
	if w.session.Stage > 2 {
		valid := []string{}
		for _, binding := range w.session.RuntimeBindings {
			if w.connectionHealthy(binding) {
				valid = append(valid, binding)
			} else {
				w.session.RuntimeVerified[binding] = false
			}
		}
		if len(valid) != len(w.session.RuntimeBindings) || len(valid) != 1 {
			w.session.Stage = 2
			w.ui.Panel("Check your agent access", []string{"Your agent setup changed or its credentials are unavailable.", "Your other agent choices remain saved."})
		}
	}
	if w.session.Stage == 4 {
		// Service activation requires the operator's current consent.
		w.session.Stage = 3
	}
	return w.save()
}

func (w *setupWizard) review() error {
	for {
		cfg, err := w.catalog()
		if err != nil {
			return err
		}
		stops, err := deployment.ReadServiceStops(w.home)
		if err != nil {
			return err
		}
		lines := []string{"Agents: " + strings.Join(setupAgentNames(cfg, w.session.RuntimeBindings), ", ")}
		var active []string
		for _, unit := range deployment.SupervisedUnits {
			if !stops[unit] {
				active = append(active, unit)
			}
		}
		title, prompt, action := "Ready to start", "Start with these choices?", "Start Culler"
		if len(active) == 0 {
			title, prompt, action = "Ready to apply the release", "Apply this release?", "Apply Culler"
			lines = append(lines, "Setup preserves all service stops.", "The release checks cover the installed files.")
		} else {
			lines = append(lines, "Start enables these services:")
			lines = append(lines, active...)
			lines = append(lines, "These services stay active after you close this terminal.")
		}
		for _, unit := range deployment.SupervisedUnits {
			if stops[unit] {
				lines = append(lines, "The "+unit+" service remains stopped by your choice.")
			}
		}
		if !stops["dashboard"] {
			lines = append(lines, "Add your project and its goal in the dashboard.")
		}
		w.ui.Panel(title, lines)
		_, err = w.ui.Choose(prompt, []onboarding.Choice{setupChoice("start", action, "")}, 0)
		if err != nil {
			return err
		}
		if _, err := verbs.PreviewOnboardRouting(w.arguments()); err != nil {
			w.problem("The agent assignments require attention", []string{"Select a valid role assignment file, or use the recommended assignments."})
			if err := w.advanced(); err != nil && !errors.Is(err, onboarding.ErrBack) {
				return err
			}
			continue
		}
		return nil
	}
}

func setupAgentNames(cfg operatorconfig.Config, bindings []string) []string {
	defaults := operatorconfig.Default().Runtimes
	names := make([]string, 0, len(bindings))
	for _, id := range bindings {
		binding := cfg.Runtimes.Bindings[id]
		if binding.Harness == "opencode" {
			source := cfg.Runtimes.Credentials[binding.Credential]
			provider, _ := opencodecatalog.Find(source.CatalogEntry)
			name := provider.Name
			if name == "" {
				name = source.CatalogEntry
			}
			names = append(names, "OpenCode ("+name+" · "+binding.Model+")")
			continue
		}
		name := map[string]string{"codex": "Codex", "claude-sdk": "Claude Code"}[binding.Harness]
		if name == "" {
			names = append(names, id)
			continue
		}
		original, exists := defaults.Bindings[id]
		kind := cfg.Runtimes.Credentials[binding.Credential].Kind
		if !exists || original != binding || kind != setupHarnessKind(binding.Harness) {
			name += " (" + id + ")"
		}
		names = append(names, name)
	}
	return names
}

func (w *setupWizard) advanced() error {
	for {
		w.ui.Panel("Current options", w.advancedLines())
		choice, err := w.ui.Choose("Advanced options", []onboarding.Choice{
			setupChoice("done", "Return to the review", "Keep the current options"),
			setupChoice("routing", "Change agent assignments", "Choose a role assignment file"),
			setupChoice("timing", "Change service intervals", "Adjust the review limit and agent time limits"),
			setupChoice("dashboard", "Change the dashboard address", "Choose a host and port"),
		}, 0)
		if err != nil {
			return err
		}
		switch choice {
		case "done":
			return w.save()
		case "routing":
			if err := w.editRouting(); err != nil && !errors.Is(err, onboarding.ErrBack) {
				return err
			}
		case "dashboard":
			if err := w.editDashboard(); err != nil && !errors.Is(err, onboarding.ErrBack) {
				return err
			}
		case "timing":
			if err := w.editTunables(); err != nil && !errors.Is(err, onboarding.ErrBack) {
				return err
			}
		}
		if err := w.save(); err != nil {
			return err
		}
	}
}

type setupTunableField struct {
	name, label string
	value       int
}

func setupTunableFields(d operatorconfig.Config) []setupTunableField {
	return []setupTunableField{
		{"review-wip-cap", "Maximum review packets per project", d.Budgets.ReviewWIPCap},
		{"timeout-minutes", "Agent lease in minutes", d.Timing.TimeoutMinutes},
		{"grace-minutes", "Agent lease grace in minutes", d.Timing.GraceMinutes},
		{"heartbeat-interval-s", "Agent heartbeat interval in seconds", d.Timing.HeartbeatIntervalS},
		{"spawn-grace-s", "First heartbeat grace in seconds", d.Timing.SpawnGraceS},
		{"hard-deadline-minutes", "Maximum agent run in minutes", d.Timing.HardDeadlineMinutes},
		{"homie-idle-timeout-s", "Operator assistance idle limit in seconds", d.Timing.HomieIdleTimeoutS},
	}

}

func (w *setupWizard) advancedLines() []string {
	lines := []string{}
	table, err := verbs.PreviewOnboardRouting(w.arguments())
	if err != nil {
		lines = append(lines, "The role assignment file requires a valid assignment for each role.")
	} else {
		for _, row := range table.Rows() {
			role := strings.ToUpper(row.Role[:1]) + row.Role[1:]
			lines = append(lines, role+": "+row.Binding+" ("+row.Harness+")")
		}
	}
	if w.session.RoutingFile != "" {
		lines = append(lines, "Role assignment file: "+w.session.RoutingFile)
	}
	plan, err := verbs.PrepareOnboardTunablesHost(w.arguments())
	if err != nil {
		lines = append(lines, "The service intervals require a valid positive value for each interval.")
	} else {
		for _, field := range setupTunableFields(plan.Desired) {
			lines = append(lines, field.label+": "+strconv.Itoa(field.value))
		}
		lines = append(lines,
			"Resident interval in seconds: "+strconv.Itoa(plan.Desired.Timing.TickIntervalS),
			"Backup interval in seconds: "+strconv.Itoa(plan.Desired.Timing.BackupIntervalS),
			"Backup records to retain: "+strconv.Itoa(plan.Desired.Timing.BackupRetain),
		)
	}
	bind := w.session.DashboardBind
	if bind == "" {
		bind = operatorconfig.Default().Surfaces.Dashboard.Bind
	}
	return append(lines, "Dashboard: "+bind, "Every dashboard request requires an access token.")
}

func (w *setupWizard) editRouting() error {
	w.ui.Panel("Agent assignments", []string{"The file assigns one selected agent configuration to each role.", "Enter :clear to use the recommended assignments."})
	for {
		value, err := w.ui.Input("Role assignment file path", w.session.RoutingFile, false)
		if err != nil {
			return err
		}
		if value == ":clear" {
			value = ""
		}
		a := w.arguments()
		a.RoutingFile, a.AcceptDefaultRouting = value, value == ""
		if value != "" && (!filepath.IsAbs(value) || filepath.Clean(value) != value) {
			w.ui.Panel("The file path is invalid", []string{"Enter the full path to the role assignment file."})
			continue
		}
		if _, err := verbs.PreviewOnboardRouting(a); err != nil {
			w.ui.Panel("The role assignment file is invalid", []string{"Use a readable file with one valid assignment for each role.", "Each assignment must name a selected agent configuration."})
			continue
		}
		w.session.RoutingFile = value
		return w.save()
	}
}

func (w *setupWizard) editDashboard() error {
	initial := w.session.DashboardBind
	if initial == "" {
		initial = operatorconfig.Default().Surfaces.Dashboard.Bind
	}
	for {
		value, err := w.ui.Input("Dashboard host and port", initial, false)
		if err != nil {
			return err
		}
		host, port, parseErr := net.SplitHostPort(value)
		number, numberErr := strconv.Atoi(port)
		if parseErr != nil || host == "" || numberErr != nil || number < 1 || number > 65535 ||
			strings.IndexFunc(value, unicode.IsSpace) >= 0 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			w.ui.Panel("The address is invalid", []string{"Use a host and a port from 1 through 65535.", "For example, use 127.0.0.1:7333."})
			continue
		}
		w.session.DashboardBind = value
		return w.save()
	}
}

func (w *setupWizard) editTunables() error {
	plan, err := verbs.PrepareOnboardTunablesHost(w.arguments())
	if err != nil {
		return err
	}
	fields := setupTunableFields(plan.Desired)
	if w.session.Tunables == nil {
		w.session.Tunables = map[string]int{}
	}
	for index := 0; index < len(fields); {
		field := fields[index]
		value := field.value
		if saved := w.session.Tunables[field.name]; saved > 0 {
			value = saved
		}
		answer, err := w.ui.Input(field.label, strconv.Itoa(value), false)
		if errors.Is(err, onboarding.ErrBack) && index > 0 {
			index--
			continue
		}
		if err != nil {
			return err
		}
		n, err := strconv.Atoi(answer)
		if err != nil || n <= 0 {
			w.ui.Panel("The value is invalid", []string{"Enter a positive whole number."})
			continue
		}
		w.session.Tunables[field.name] = n
		if err := w.save(); err != nil {
			return err
		}
		index++
	}
	return nil
}

func (w *setupWizard) start() error {
	stops, err := deployment.ReadServiceStops(w.home)
	if err != nil {
		return err
	}
	if w.deps.publish != nil {
		if err := w.operation("Install Culler", w.deps.publish); err != nil {
			return err
		}
	}
	if w.deps.agentsView != nil {
		if err := w.operation("Install AgentsView", func() error {
			return agentsViewSetupFailure(w.deps.agentsView(w.home))
		}); err != nil {
			return err
		}
	}
	a := w.arguments()
	for _, section := range []string{"routing", "container", "tunables", "surfaces", "supervision"} {
		if w.existingSections[section] {
			continue
		}
		a.ActivateSupervision = section == "supervision"
		if a.ActivateSupervision {
			w.servicesTouched = true
		}
		label := map[string]string{
			"routing": "Assign your agents", "container": "Prepare agent access",
			"project": "Set up your project", "tunables": "Set agent limits",
			"surfaces": "Set up your dashboard", "supervision": "Start Culler",
		}[section]
		if section == "supervision" && stops["resident"] && stops["dashboard"] && stops["agentsview"] {
			label = "Check service stop choices"
		}
		if err := w.operation(label, func() error { return w.deps.section(section, a) }); err != nil {
			return err
		}
	}
	w.serviceStates = setupServiceMap(true)
	for unit, stopped := range stops {
		if stopped {
			w.serviceStates[unit] = false
		}
	}
	if err := w.operation("Check Culler", func() error {
		var err error
		w.services, err = w.deps.ready(w.home)
		return err
	}); err != nil {
		return err
	}
	w.servicesStopped = false
	return nil
}

func (w *setupWizard) dashboard() error {
	if len(w.services) == 0 {
		if err := w.operation("Check Culler", func() error { var err error; w.services, err = w.deps.ready(w.home); return err }); err != nil {
			return err
		}
	}
	cfg, err := w.catalog()
	if err != nil {
		return err
	}
	base, authenticated, err := setupDashboardAddresses(w.home)
	if err != nil {
		return err
	}
	lines := []string{
		"Agents: " + strings.Join(setupAgentNames(cfg, w.session.RuntimeBindings), ", "),
		"Dashboard: " + base,
	}
	lines = append(lines, w.services...)
	lines = append(lines, "Add your project and its goal in the dashboard.")
	stops, err := deployment.ReadServiceStops(w.home)
	if err != nil {
		return err
	}
	title := "Your dashboard is ready"
	if stops["dashboard"] {
		title = "Culler is ready with your saved service stops"
	} else {
		lines = append(lines, "Active services stay active after you close this terminal.")
	}
	w.ui.Panel(title, lines)
	if err := w.commandAvailability(); err != nil {
		return err
	}
	if stops["dashboard"] {
		w.ui.Panel("Dashboard stopped", []string{"Your dashboard stop choice remains in effect.", "Start the dashboard when you need it."})
		if terminal, ok := w.ui.(interface{ Literal(string) }); ok {
			terminal.Literal(deployment.CLICommand(w.home, "start dashboard"))
		}
		return nil
	}
	for {
		choice, err := w.ui.Choose("Finish setup", []onboarding.Choice{
			setupChoice("open", "Open dashboard", ""),
			setupChoice("finish", "Close setup", ""),
		}, 0)
		if err != nil {
			return err
		}
		if choice == "finish" {
			return nil
		}
		if err := w.deps.open(authenticated); err == nil {
			w.ui.Panel("Your dashboard is open", []string{base})
			return nil
		}
		w.ui.Panel("Open the dashboard in your browser", []string{
			"The browser did not open. Paste the private link below into your browser.",
			"Dashboard address: " + base,
			"This link grants dashboard access. Keep it private.",
			"Select Open dashboard to retry.",
		})
		fmt.Fprintln(w.output, authenticated)
		fmt.Fprintln(w.output)
	}
}

func (w *setupWizard) commandAvailability() error {
	// A preview has no executable publication dependency.
	if os.Getenv("MC_BIN_DIR") == "" {
		return nil
	}
	lines, err := deployment.CLIAvailability(w.home)
	if err != nil {
		return &setupFailure{cause: setupSafeDiagnostic(err.Error()), command: cullerInstallCommand(w.home)}
	}
	w.ui.Panel("", lines)
	return nil
}
