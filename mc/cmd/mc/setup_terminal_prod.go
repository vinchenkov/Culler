//go:build !test_fake_routing

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	operatorconfig "mc/config"
	"mc/deployment"
	"mc/onboarding"
	"mc/verbs"
)

type setupTerminal interface {
	Stage(int, string, []string)
	Panel(string, []string)
	Choose(string, []onboarding.Choice, int) (string, error)
	Input(string, string, bool) (string, error)
	Activity(string, func() error) error
	ActivityProgress(string, func(func(string)) error) error
}

type setupDependencies struct {
	assessCandidate func() (repairs, waits []string, err error)
	finishExisting  func() error
	beforeChange    func(string, verbs.OnboardArgs, func(string)) error
	saveSource      func(string, verbs.OnboardArgs) error
	agentsView      func(string) error
	rebuildImage    func(context.Context, string, string, func(string)) error
	stop            func(string) error
	serviceStates   func(string) (map[string]bool, error)
	inspect         func(string, verbs.OnboardArgs) (setupDeployment, error)
	publish         func() error
	docker          func(context.Context) onboarding.DockerStatus
	image           func(context.Context, string, string, func(string)) error
	section         func(string, verbs.OnboardArgs) error
	connect         func(*setupWizard, setupConnection) error
	saveKey         func(string, deployment.AgentConnectionRequest) (deployment.AgentProfileCatalog, error)
	removeKey       func(string, string) error
	recordTest      func(string, string, bool) error
	openCodeStatus  func(string) *deployment.AgentProfileOperation
	openCodeStart   func(string, bool) (deployment.AgentProfileOperation, error)
	open            func(string) error
	ready           func(string) ([]string, error)
	runtimeReady    func(string, []string) error
	identity        func(string, string) (string, error)
}

type setupWizard struct {
	rebuildImage        bool
	ui                  setupTerminal
	home                string
	operatorHome        string
	input               io.Reader
	output              io.Writer
	answers             verbs.OnboardArgs
	session             onboarding.Session
	deps                setupDependencies
	servicesStopped     bool
	servicesTouched     bool
	serviceStates       map[string]bool
	existingAgentAccess bool
	existingSections    map[string]bool
	services            []string
	agentChoiceRequired bool
}

var setupStageTitles = []string{
	"Welcome", "Prepare your Mac", "Connect an agent",
	"Review your setup", "Start Culler", "Open your dashboard",
}

var setupStageIDs = []string{"welcome", "prepare", "agents", "review", "start", "dashboard"}

func setupChoice(id, label, detail string) onboarding.Choice {
	return onboarding.Choice{ID: id, Label: label, Detail: detail}
}

func runTerminalSetup(raw []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if runtime.GOOS != "darwin" {
		fmt.Fprintln(stderr, "Culler setup requires a Mac.")
		return 2
	}
	if _, err := os.Lstat("/mc/run.json"); !os.IsNotExist(err) {
		fmt.Fprintln(stderr, "Culler setup requires the operator's Mac terminal.")
		return 2
	}
	a, err := parseTerminalSetupArgs(raw)
	if err != nil {
		fmt.Fprintln(stderr, "The setup options are invalid. Check the install.sh options with your coding harness.")
		return 2
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		fmt.Fprintln(stderr, "The deployment home is unavailable. Check its path with your coding harness.")
		return 2
	}
	published, err := loadPublishedSetup(home, a)
	if err != nil {
		fmt.Fprintln(stderr, setupSafeDiagnostic(err.Error()))
		return 2
	}
	if published != nil {
		defer published.close()
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		published.context = ctx
		if code, handled := published.recoverUpdate(raw, a, stdin, stdout, stderr); handled {
			return code
		}
		if slices.Contains(raw, "--restore") {
			return writeVerbError(stdout, stderr, errors.New("This deployment has no incomplete update to restore."))
		}
		if code, handled := published.offerUpdate(raw, a, stdin, stdout, stderr); handled {
			return code
		}
		if slices.Contains(raw, "--yes") {
			return published.runAutomatic(raw, a, stdin, stdout, stderr)
		}
	} else {
		lock, err := lockPublishedSetup(home)
		if err != nil {
			fmt.Fprintln(stderr, setupSafeDiagnostic(err.Error()))
			return 2
		}
		defer lock.Close()
	}
	operator, err := user.Current()
	if err != nil {
		fmt.Fprintln(stderr, "Culler cannot identify the current operator.")
		return 2
	}
	terminal := onboarding.NewTerminal(stdin, stdout)
	if !terminal.HasTerminal() {
		terminal.Problem("Setup requires a terminal", []string{"Run the installer in a terminal to resume setup.", "Your saved setup choices remain available."})
		terminal.Literal(cullerInstallCommand(home))
		return 2
	}
	terminal.SetStageTitles(setupStageTitles)
	session, err := onboarding.LoadSession(home)
	if err != nil {
		setupSavedChoicesNotice(terminal, err)
		session = onboarding.Session{}
	}
	w := &setupWizard{
		ui: terminal, home: home, operatorHome: operator.HomeDir,
		input: stdin, output: terminal, answers: a, session: session, deps: productionSetupDependencies(a),
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if published != nil {
		published.context = ctx
		published.configure(&w.deps)
	}
	w.mergeAnswers()
	if err := w.run(); err != nil {
		if ctx.Err() != nil {
			err = errors.Join(err, onboarding.ErrExit, ctx.Err())
		}
		var cleanup *deployment.RuntimeAuthCleanupError
		if errors.Is(err, onboarding.ErrExit) && !errors.As(err, &cleanup) {
			if errors.Is(err, context.Canceled) {
				if published != nil {
					if restoreErr := terminal.Activity("Restore the previous service state", published.cancelOperation); restoreErr != nil {
						w.problem("Service restoration requires attention", []string{setupSafeDiagnostic(restoreErr.Error())})
					}
				}
				w.exitNotice()
				return 130
			}
			w.exitNotice()
			return 0
		}
		w.problem("Setup requires attention", w.failureLines("Setup", err))
		w.serviceNotice()
		return 1
	}
	return 0
}

func parseTerminalSetupArgs(raw []string) (verbs.OnboardArgs, error) {
	args := []string{}
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case "--yes", "--interrupt-runs", "--restore":
			continue
		case "--mc-home", "--bin-dir":
			i++
			if i >= len(raw) {
				return verbs.OnboardArgs{}, fmt.Errorf("A setup path is absent.")
			}
		case "--acquire-runtime-auth":
			args = append(args, "--acquire")
		default:
			args = append(args, raw[i])
		}
	}
	a, err := parseOnboardArgs(args)
	if err == nil && (a.AllowDatabaseReset || a.Section != "" || a.Smoke || a.SmokeReset || a.SmokeApproveTask != 0) {
		err = fmt.Errorf("The setup flow requires its normal installation options.")
	}
	return a, err
}

func productionSetupDependencies(args verbs.OnboardArgs) setupDependencies {
	program := ""
	return setupDependencies{
		finishExisting: func() error {
			if os.Getenv("MC_BIN_DIR") == "" {
				return nil
			}
			_, err := installSetupExecutable()
			return err
		},
		saveSource:    saveSetupSource,
		agentsView:    downloadSetupAgentsView,
		inspect:       inspectSetupDeployment,
		stop:          stopSetupServices,
		serviceStates: setupServiceStates,
		publish: func() error {
			var err error
			program, err = installSetupExecutable()
			return err
		},
		docker: onboarding.DetectDocker,
		image: func(ctx context.Context, source, release string, report func(string)) error {
			if setupSourceModified(source) {
				return onboarding.RebuildImage(ctx, source, release, report)
			}
			return onboarding.PrepareImage(ctx, source, release, report)
		}, rebuildImage: onboarding.RebuildImage,
		section: func(section string, a verbs.OnboardArgs) error {
			return setupRunSectionProgram(program, section, a)
		},
		connect: setupConnect, open: setupOpenURL, ready: func(home string) ([]string, error) { return setupFinalReadiness(home, args) },
		runtimeReady: func(home string, bindings []string) error {
			return deployment.ValidateRuntimeGrantStore(home, bindings, os.Environ())
		},
		identity:   deployment.AgentConnectionAuthorizationIdentity,
		saveKey:    deployment.AgentConnectionSave,
		removeKey:  deployment.AgentConnectionRemoveCredential,
		recordTest: setupRecordVerification,
	}
}

func (w *setupWizard) save() error {
	session := w.session
	if session.Stage >= 0 && session.Stage < len(setupStageIDs) {
		session.StageName = setupStageIDs[session.Stage]
	}
	return onboarding.SaveSession(w.home, session)
}

func (w *setupWizard) mark(id string) error {
	if !slices.Contains(w.session.Completed, id) {
		w.session.Completed = append(w.session.Completed, id)
	}
	ordered := make([]string, 0, len(w.session.Completed))
	for _, stage := range setupStageIDs {
		if slices.Contains(w.session.Completed, stage) {
			ordered = append(ordered, stage)
		}
	}
	for _, stage := range w.session.Completed {
		if !slices.Contains(setupStageIDs, stage) {
			ordered = append(ordered, stage)
		}
	}
	w.session.Completed = ordered
	return w.save()
}

func (w *setupWizard) run() error {
	if w.deps.inspect != nil {
		handled, err := w.inspectExistingDeployment()
		if err != nil || handled {
			return err
		}
	} else if w.session.Stage > 1 {
		if err := w.revalidate(); err != nil {
			return err
		}
	}
	stages := []func() error{w.welcome, w.prepare, w.agents, w.review, w.start, w.dashboard}
	for w.session.Stage < len(stages) {
		stage := w.session.Stage
		w.showStage(stage)
		var err error
		if stage == 2 && w.existingAgentAccess {
			w.configuredAgentNotice()
		} else {
			err = stages[stage]()
		}
		if errors.Is(err, onboarding.ErrBack) {
			if err := w.previousStage(); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			if errors.Is(err, onboarding.ErrExit) {
				return err
			}
			w.problem(setupStageTitles[stage]+" requires attention", w.failureLines(setupStageTitles[stage], err))
			choice, nextErr := w.ui.Choose("Resume this step", []onboarding.Choice{
				setupChoice("retry", "Retry: "+setupStageTitles[stage], ""), setupChoice("details", "View error details", ""),
			}, 0)
			if nextErr == nil && choice == "details" {
				w.ui.Panel("Error details", setupDiagnosticLines(setupStageTitles[stage], err))
				_, nextErr = w.ui.Choose("Resume this step", []onboarding.Choice{setupChoice("retry", "Retry: "+setupStageTitles[stage], "")}, 0)
			}
			if errors.Is(nextErr, onboarding.ErrBack) {
				if err := w.previousStage(); err != nil {
					return err
				}
				continue
			}
			if nextErr != nil {
				return nextErr
			}
			continue
		}
		if err := w.mark(setupStageIDs[stage]); err != nil {
			return err
		}
		if stage == 5 {
			if terminal, ok := w.ui.(interface{ Complete() }); ok {
				terminal.Complete()
			}
			return nil
		}
		w.session.Stage++
		if err := w.save(); err != nil {
			return err
		}
	}
	return nil
}

// problem shows a panel that names a failure or a rejected answer.
func (w *setupWizard) problem(title string, lines []string) {
	setupProblem(w.ui, title, lines)
}

func setupProblem(ui setupTerminal, title string, lines []string) {
	show := func(title string, content []string) {
		if terminal, ok := ui.(interface{ Problem(string, []string) }); ok {
			terminal.Problem(title, content)
		} else {
			ui.Panel(title, content)
		}
	}
	terminal, literal := ui.(interface{ Literal(string) })
	commands := slices.ContainsFunc(lines, setupCommandLine)
	if !literal || !commands {
		show(title, lines)
		return
	}
	var content []string
	first := true
	flush := func() {
		if first {
			show(title, content)
			first = false
		} else if len(content) > 0 {
			ui.Panel("", content)
		}
		content = nil
	}
	for _, line := range lines {
		if setupCommandLine(line) || filepath.IsAbs(line) {
			flush()
			terminal.Literal(line)
		} else {
			content = append(content, line)
		}
	}
	flush()
}

func setupCommandLine(line string) bool {
	return strings.HasPrefix(line, "curl -fsSL ") || line == onboarding.DesktopContextCommand
}

// setupLiteral shows a command on its own line, so the operator can copy it.
func setupLiteral(ui setupTerminal, command string) {
	if terminal, ok := ui.(interface{ Literal(string) }); ok {
		terminal.Literal(command)
	} else {
		ui.Panel("", []string{command})
	}
}

func (w *setupWizard) showStage(stage int) {
	w.showStageUntil(stage, stage, "")
}

func (w *setupWizard) showStageUntil(stage, completedUntil int, next string) {
	title := setupStageTitles[stage]
	if stops, err := deployment.ReadServiceStops(w.home); err == nil {
		titles := slices.Clone(setupStageTitles)
		if stops["resident"] && stops["dashboard"] && stops["agentsview"] {
			titles[4] = "Apply Culler"
		}
		if stops["dashboard"] {
			titles[5] = "Finish setup"
		}
		if terminal, ok := w.ui.(interface{ SetStageTitles([]string) }); ok {
			terminal.SetStageTitles(titles)
		}
		if stops["resident"] && stops["dashboard"] && stops["agentsview"] && next == setupStageTitles[4] {
			next = "Apply Culler"
		}
		if stops["dashboard"] && next == setupStageTitles[5] {
			next = "Finish setup"
		}
		if stage == 4 && stops["resident"] && stops["dashboard"] && stops["agentsview"] {
			title = "Apply Culler"
		}
		if stage == 5 && stops["dashboard"] {
			title = "Finish setup"
		}
	}
	completed := []string{}
	for i := 0; i < completedUntil; i++ {
		if slices.Contains(w.session.Completed, setupStageIDs[i]) {
			completed = append(completed, setupStageIDs[i])
		}
	}
	if next != "" {
		if terminal, ok := w.ui.(interface {
			StageWithNext(int, string, []string, string)
		}); ok {
			terminal.StageWithNext(stage+1, title, completed, next)
			return
		}
	}
	w.ui.Stage(stage+1, title, completed)
}

func (w *setupWizard) previousStage() error {
	if w.session.Stage == 5 {
		w.session.Stage = 3
	} else if w.session.Stage > 0 {
		w.session.Stage--
	}
	w.agentChoiceRequired = w.session.Stage == 2
	return w.save()
}

func (w *setupWizard) welcome() error {
	w.ui.Panel("Let's set up Culler.", []string{
		"Connect an agent and open your dashboard.",
		"You will add your project and its goal in the dashboard.",
	})
	_, err := w.ui.Choose("Ready to begin?", []onboarding.Choice{setupChoice("continue", "Set up this Mac", "")}, 0)
	return err
}

func (w *setupWizard) operation(label string, action func() error) error {
	return w.retryOperation(label, func() error { return w.ui.Activity(label, action) })
}

func (w *setupWizard) progressOperation(label string, action func(func(string)) error) error {
	return w.retryOperation(label, func() error { return w.ui.ActivityProgress(label, action) })
}

func (w *setupWizard) retryOperation(label string, action func() error) error {
	for {
		err := action()
		if err == nil {
			return nil
		}
		if errors.Is(err, onboarding.ErrExit) {
			return err
		}
		// The activity line above states the failure.
		w.problem("", w.failureLines(label, err))
		choice, promptErr := w.ui.Choose("Resume this step", []onboarding.Choice{
			setupChoice("retry", "Retry: "+label, ""),
			setupChoice("details", "View error details", ""),
		}, 0)
		if promptErr != nil {
			return promptErr
		}
		if choice == "details" {
			w.ui.Panel("Error details", setupDiagnosticLines(label, err))
			_, promptErr = w.ui.Choose("Resume this step", []onboarding.Choice{setupChoice("retry", "Retry: "+label, "")}, 0)
			if promptErr != nil {
				return promptErr
			}
		}
	}
}

const setupProviderProxyStartCause = "Culler could not start the local service for model access. Ask your coding harness to repair Culler, then retry."

func setupCause(err error) string {
	var cleanup *deployment.RuntimeAuthCleanupError
	if errors.As(err, &cleanup) {
		return cleanup.Error()
	}
	var proxy *deployment.ProviderProxyStartError
	if errors.As(err, &proxy) {
		return setupProviderProxyStartCause
	}
	var failure *setupFailure
	if errors.As(err, &failure) {
		return failure.cause
	}
	var preparation *onboarding.PreparationError
	if errors.As(err, &preparation) {
		return preparation.Cause
	}
	if errors.Is(err, os.ErrPermission) {
		return "Culler lacks permission to read or write a required file."
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "The operation exceeded its time limit."
	}
	if errors.Is(err, os.ErrNotExist) {
		return "A required file or directory is absent."
	}
	return "The cause is unknown."
}

func setupDiagnosticLines(label string, err error) []string {
	lines := []string{"Operation: " + label, setupDetailCause(setupCause(err))}
	var preparation *onboarding.PreparationError
	if errors.As(err, &preparation) {
		lines = preparation.DiagnosticDetails()
		for i := range lines {
			if strings.HasPrefix(lines[i], "Cause:") {
				lines[i] = setupDetailCause(lines[i])
			}
		}
	}
	var failure *setupFailure
	if errors.As(err, &failure) {
		if failure.diagnostic != "" {
			lines = append(lines, failure.diagnostic)
		}
		lines = append(lines, failure.details...)
	} else if preparation == nil {
		lines = append(lines, "Diagnostic: "+setupSafeDiagnostic(err.Error()))
	}
	return append(lines, "Share these details with your coding harness.")
}

type setupFailure struct {
	cause string
	// diagnostic is one line that the connection failure shows below the cause.
	diagnostic string
	details    []string
	record     string
	command    string
}

func (e *setupFailure) Error() string { return e.cause }

func setupRunSectionProgram(program, section string, a verbs.OnboardArgs) error {
	var stdout, stderr bytes.Buffer
	args := wholeSectionArgs(section, a)
	code := 0
	if program == "" {
		code = dispatchArgs(args, strings.NewReader(""), &stdout, &stderr)
	} else {
		cmd := exec.Command(program, args...)
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			code = 2
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				code = exit.ExitCode()
			} else {
				fmt.Fprintln(&stderr, err)
			}
		}
	}
	return setupSectionResult(section, code, stdout.Bytes(), stderr.Bytes())
}

func setupSectionResult(section string, code int, stdout, stderr []byte) error {
	if code != 0 {
		var envelope struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(stdout, &envelope)
		message := envelope.Error.Message
		if message == "" {
			message = strings.TrimSpace(string(stderr))
		}
		if message == "" {
			message = strings.TrimSpace(string(stdout))
		}
		return &setupFailure{
			cause:   setupSectionCause(message),
			details: []string{fmt.Sprintf("Exit code: %d", code), "Diagnostic: " + setupSafeDiagnostic(message)},
		}
	}
	var receipt struct {
		OK       bool `json:"ok"`
		Sections []struct {
			Section string `json:"section"`
			Status  string `json:"status"`
		} `json:"sections"`
	}
	if json.Unmarshal(stdout, &receipt) != nil || !receipt.OK || len(receipt.Sections) != 1 ||
		receipt.Sections[0].Section != section || (receipt.Sections[0].Status != "ok" && receipt.Sections[0].Status != "done") {
		return &setupFailure{cause: "Culler could not confirm the operation result.", details: []string{setupSafeDiagnostic(string(stdout)), setupSafeDiagnostic(string(stderr))}}
	}
	return nil
}

var setupUnknownConfigKey = regexp.MustCompile(`config.toml carries unknown key "([A-Za-z0-9_.-]+)"`)

func setupSectionCause(message string) string {
	if start := strings.Index(message, "The OpenCode "); start >= 0 && strings.Contains(message[start:], " from source failed: ") {
		return setupSafeDiagnostic(message[start:])
	}
	if match := setupUnknownConfigKey.FindStringSubmatch(message); len(match) == 2 {
		return fmt.Sprintf("The config.toml file contains an unknown key: %s. Ask your coding harness to correct this key, then retry.", match[1])
	}
	message = strings.ToLower(message)
	for _, item := range []struct{ match, cause string }{
		{"database reset requires", "Database administration requires stopped services and agents."},
		{"database schema differs", "This release cannot use the existing database. Setup stopped before database replacement."},
		{"release update requires", "The release update requires stopped Culler services. Ask your coding harness to apply this update."},
		{"invalid owner", "A setup file or directory belongs to another user. Ask your coding harness to repair its ownership, then run install.sh."},
		{"permission denied", "Culler lacks permission to access a required file."},
		{"operation not permitted", "macOS refused access to a required file or service."},
		{"address already in use", "Another process uses the selected dashboard address. Choose another port."},
		{"loaded supervision differs", "The active services use a different configuration. Ask your coding harness to apply the new setup."},
		{"mode 0700", "A setup directory requires owner-only access. Ask your coding harness to repair its permissions."},
		{"mode-0600", "A credential file requires owner-only access. Ask your coding harness to repair its permissions."},
		{"vision.md", "The project vision requires attention. Open Project details in the dashboard to inspect it."},
		{"connection refused", "A required local service is unavailable. Open Docker Desktop, then retry."},
		{"no such file", "A required file is absent. Ask your coding harness to repair this setup stage."},
		{"requires a fresh database", "This release cannot use the existing database."},
	} {
		if strings.Contains(message, item.match) {
			return item.cause
		}
	}
	return "The cause is unknown."
}

func setupSectionName(section string) string {
	names := map[string]string{
		"preflight": "Mac requirements", "home": "deployment preparation", "container": "container access",
		"routing": "agent assignments", "project": "project preparation", "tunables": "service defaults",
		"surfaces": "dashboard configuration", "supervision": "service activation", "verify": "service verification",
	}
	if name := names[section]; name != "" {
		return name
	}
	return "setup"
}

func (w *setupWizard) prepare() error {
	for {
		var status onboarding.DockerStatus
		if err := w.ui.Activity("Docker Desktop", func() error {
			status = w.deps.docker(context.Background())
			if string(status.State) != "ready" {
				return onboarding.ErrActionRequired
			}
			return nil
		}); err != nil && !errors.Is(err, onboarding.ErrActionRequired) {
			return err
		}
		if string(status.State) == "ready" {
			if status.CommandPath != "" {
				if err := os.Setenv("PATH", filepath.Dir(status.CommandPath)+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
					return err
				}
			}
			break
		}
		lines := []string{status.Detail}
		choices := []onboarding.Choice{setupChoice("check", "Check Docker Desktop again", "")}
		if status.Failure != nil {
			choices = append(choices, setupChoice("details", "View error details", ""))
		}
		if string(status.State) == "absent" {
			lines = []string{
				"Docker Desktop runs your agents in containers.",
				"Install and open Docker Desktop, then return here.",
			}
			choices = append([]onboarding.Choice{setupChoice("install", "Install Docker Desktop", "Open Docker's website.")}, choices...)
		}
		w.ui.Panel("Docker Desktop", lines)
		if status.Command != "" {
			setupLiteral(w.ui, status.Command)
			// An empty panel separates the command from the choice list.
			w.ui.Panel("", nil)
		}
		choice, err := w.ui.Choose("Prepare Docker Desktop", choices, 0)
		if err != nil {
			return err
		}
		if choice == "details" && status.Failure != nil {
			w.ui.Panel("Error details", setupDiagnosticLines("Check Docker Desktop", status.Failure))
		}
		if choice == "install" {
			if err := w.deps.open("https://docs.docker.com/desktop/setup/install/mac-install/"); err != nil {
				w.ui.Panel("Open Docker's website", []string{"The browser could not open. Copy this address into your browser:", "https://docs.docker.com/desktop/setup/install/mac-install/"})
			}
		}
	}
	if err := w.progressOperation("Prepare the agent container", func(report func(string)) error {
		prepare := w.deps.image
		if w.rebuildImage && w.deps.rebuildImage != nil {
			prepare = w.deps.rebuildImage
		}
		if err := prepare(context.Background(), w.answers.HostReleaseSource, releaseBuildID, report); err != nil {
			return err
		}
		w.rebuildImage = false
		return nil
	}); err != nil {
		return err
	}
	for _, section := range []string{"preflight", "home"} {
		label := "Check " + setupSectionName(section)
		if section == "home" {
			label = "Prepare Culler"
		}
		if err := w.operation(label, func() error { return w.deps.section(section, w.arguments()) }); err != nil {
			return err
		}
	}
	if w.deps.saveSource != nil {
		if err := w.deps.saveSource(w.home, w.answers); err != nil {
			return err
		}
	}
	if err := w.recheckConfiguredAgents(); err != nil {
		return err
	}
	w.session.ReleaseID = releaseBuildID
	return w.save()
}

func setupOpenURL(address string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/open", address)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd.Run()
}

func setupDashboardAddresses(home string) (string, string, error) {
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		return "", "", err
	}
	host, port, err := net.SplitHostPort(cfg.Surfaces.Dashboard.Bind)
	if err != nil || cfg.Surfaces.Dashboard.AuthToken == "" {
		return "", "", &setupFailure{cause: "The dashboard address or access token is unavailable."}
	}
	if host == "0.0.0.0" || host == "::" || host == "" {
		host = "127.0.0.1"
	}
	base := "http://" + net.JoinHostPort(host, port) + "/"
	return base, base + "#token=" + url.QueryEscape(cfg.Surfaces.Dashboard.AuthToken), nil
}

func setupCheckDashboard(home string) error {
	base, _, err := setupDashboardAddresses(home)
	if err != nil {
		return err
	}
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"api/health", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Surfaces.Dashboard.AuthToken)
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return &setupFailure{cause: "The dashboard did not answer its authenticated readiness check.", details: []string{setupSafeDiagnostic(err.Error())}}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return &setupFailure{cause: "The dashboard refused its authenticated readiness check.", details: []string{fmt.Sprintf("HTTP status: %d", res.StatusCode)}}
	}
	var health struct {
		OK       *bool             `json:"ok"`
		Findings []json.RawMessage `json:"findings"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&health); err != nil {
		return &setupFailure{cause: "The dashboard returned an incomplete readiness result.", details: []string{setupSafeDiagnostic(err.Error())}}
	}
	if health.OK == nil || len(health.Findings) == 0 {
		return &setupFailure{cause: "The dashboard returned an incomplete readiness result.", details: []string{"The health result requires an ok field and at least one check."}}
	}
	if update, err := checkUpdateDashboardIdentity(home, res.Header.Get("X-Culler-Release")); err != nil {
		return err
	} else if update {
		return nil
	}
	if !*health.OK {
		body, _ := json.Marshal(health.Findings)
		return &setupFailure{cause: "The dashboard reports a service health failure. Ask your coding harness to inspect the service diagnostics.", details: []string{setupSafeDiagnostic(string(body))}}
	}
	return nil
}

func (w *setupWizard) configuredAgentNotice() {
	w.ui.Panel("Your agent access is configured", []string{"Your saved agent credentials passed the local checks.", "Your agent choices remain intact."})
}

func (w *setupWizard) exitNotice() {
	if w.servicesTouched || w.servicesStopped {
		w.ui.Panel("Setup closed", nil)
		w.serviceNotice()
		return
	}
	w.ui.Panel("Setup closed", []string{"Your setup choices remain saved.", "Resume setup for this deployment:"})
	if terminal, ok := w.ui.(interface{ Literal(string) }); ok {
		terminal.Literal(cullerInstallCommand(w.home))
	}
}

func (w *setupWizard) serviceNotice() {
	if !w.servicesTouched && !w.servicesStopped {
		return
	}
	states := w.serviceStates
	var err error
	if w.deps.serviceStates != nil {
		states, err = w.deps.serviceStates(w.home)
	}
	lines := []string{}
	for _, service := range setupServices {
		active, known := states[service.unit]
		status := "unknown"
		if known {
			status = "stopped"
			if active {
				status = "active"
			}
		}
		lines = append(lines, service.label+": "+status+".")
	}
	if err != nil {
		lines = append(lines, "The service state check could not finish.")
	}
	if os.Getenv("CULLER_RELEASE_RECORD") != "" {
		lines = append(lines, publishedRecoveryLines(w.home)...)
	}
	w.ui.Panel("Current services", lines)
	w.ui.Panel("Resume setup for this deployment", nil)
	if terminal, ok := w.ui.(interface{ Literal(string) }); ok {
		terminal.Literal(cullerInstallCommand(w.home))
	}
}

func setupDetailCause(cause string) string {
	cause = strings.ReplaceAll(cause, " Open the error details for the diagnostic.", "")
	return strings.ReplaceAll(cause, " Open the error details, then select Retry.", "")
}

func setupSavedChoicesNotice(ui setupTerminal, err error) {
	setupProblem(ui, "Saved setup choices require attention", []string{
		"Culler will inspect the current deployment.", setupSafeDiagnostic(err.Error()),
	})
}
