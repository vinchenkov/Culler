//go:build !test_fake_routing

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	operatorconfig "mc/config"
	"mc/deployment"
	"mc/distribution"
	"mc/onboarding"
	"mc/verbs"
)

type cullerService struct {
	Name, State, Detail string
	Stopped             bool
}
type cullerStatus struct {
	Home, Release, Version, CLI, Address, Overall, Detail, Operation string
	Services                                                         []cullerService
	Next, NextCommand                                                string
	OK                                                               bool
}

type cullerRuntime struct {
	Release string `json:"releaseBuildId"`
	Home    string `json:"mcHome"`
	Program string `json:"mcPath"`
	Docker  string `json:"dockerPath"`
	Image   string `json:"image"`
}

func cullerBuild() string {
	if !validReleaseBuildID(releaseBuildID) || releaseBuildID == "dev" {
		return "development"
	}
	return cullerReleaseIdentity(releaseBuildID)
}

// cullerReleaseLabel names a release identity for a person: the version
// and the short commit. A state such as "development" or "Not installed"
// stays unchanged.
func cullerReleaseLabel(id, version string) string {
	if len(id) != 40 || strings.Trim(id, "0123456789abcdef") != "" {
		return id
	}
	return distribution.Label(version, id)
}

// cullerInstalledVersion returns the version of the installed release id.
func cullerInstalledVersion(home, id string) string {
	for _, path := range []string{distribution.InstalledRecord, filepath.Join("state", "pending-release.json")} {
		if r, err := distribution.Load(filepath.Join(home, path)); err == nil && r.Commit == id {
			return r.Version
		}
	}
	if id == releaseBuildID {
		return releaseVersion
	}
	return ""
}

func cullerReleaseIdentity(id string) string {
	if id == "dev" {
		return "development"
	}
	return id
}

func cullerInstalled(home string) (string, error) {
	r, err := distribution.Load(filepath.Join(home, distribution.InstalledRecord))
	if err == nil {
		return r.Commit, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "Unknown", fmt.Errorf("The installed release record is unavailable: %w\n%s", err, cullerInstallCommand(home))
	}
	runtime, err := readCullerRuntime(home)
	if err == nil {
		return cullerReleaseIdentity(runtime.Release), nil
	}
	if errors.Is(err, os.ErrNotExist) {
		for _, name := range []string{"config.toml", "deployment.uuid", "supervision"} {
			if _, check := os.Lstat(filepath.Join(home, name)); check == nil {
				return "Unknown", fmt.Errorf("Setup is incomplete. Use the installer to resume setup.\n%s", cullerInstallCommand(home))
			} else if !errors.Is(check, os.ErrNotExist) {
				return "Unknown", check
			}
		}
		return "Not installed", nil
	}
	return "Unknown", err
}

func readCullerRuntime(home string) (cullerRuntime, error) {
	var r cullerRuntime
	body, err := os.ReadFile(filepath.Join(home, "supervision", "resident.json"))
	if err != nil {
		return r, err
	}
	if json.Unmarshal(body, &r) != nil || !validReleaseBuildID(r.Release) || r.Home != home || !filepath.IsAbs(r.Program) || !filepath.IsAbs(r.Docker) {
		return r, errors.New("The service configuration has an invalid deployment identity.")
	}
	return r, nil
}

func inspectCullerCommandFiles(home string) error {
	command, err := deployment.ReadCLIInstallation(home)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	r, err := readCullerRuntime(home)
	if err != nil {
		return err
	}
	if command.Release != r.Release || !command.Verified {
		return errors.New("The command requires release and shell checks. Use the installer to complete setup.")
	}
	for _, path := range []string{command.Path, r.Program} {
		digest, err := deployment.CLIFileDigest(path)
		if err != nil || digest != command.SHA256 {
			return errors.New("The command files differ from the verified release. Use the installer to repair the release.")
		}
	}
	return nil
}

func runCuller(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runCullerWith(args, stdin, stdout, stderr, cullerDependencies{
		inspect: inspectCuller, manager: newCullerManager, release: readPublishedReleaseStatus,
		uninstall: func(ctx context.Context, o cullerOptions, in io.Reader, out, diagnostic io.Writer) int {
			return cullerUninstall(ctx, o, in, out, diagnostic, []string{"/tmp", os.TempDir()})
		},
	})
}

type cullerDependencies struct {
	inspect   func(context.Context, string) cullerStatus
	manager   func(string) (*cullerManager, error)
	release   func(context.Context, string) releaseStatus
	uninstall func(context.Context, cullerOptions, io.Reader, io.Writer, io.Writer) int
}

func runCullerWith(args []string, stdin io.Reader, stdout, stderr io.Writer, deps cullerDependencies) int {
	o, done, code := cullerUsage(args, stdout, stderr)
	if done {
		return code
	}
	out := onboarding.NewCommandTerminal(stdout, o.noColor)
	diagnostic := onboarding.NewCommandTerminal(stderr, o.noColor)
	out.CommandTitle()
	if o.command == "status" || o.command == "version" {
		defer cullerNotice(out)
	}
	diagnosticHome := ""
	fail := func(err error, code int) int {
		lines := strings.Split(setupDiagnosticAt(diagnosticHome, err.Error()), "\n")
		var details, commands []string
		for _, line := range lines {
			if strings.HasPrefix(line, "culler ") || strings.HasPrefix(line, "curl ") || strings.HasPrefix(line, "sh ") || filepath.IsAbs(line) {
				commands = append(commands, line)
			} else {
				details = append(details, line)
			}
		}
		diagnostic.Problem("Culler requires attention", details)
		for _, command := range commands {
			diagnostic.Literal(command)
		}
		return code
	}
	if o.home == "" {
		o.home = os.Getenv("MC_HOME")
	}
	if o.home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return fail(err, 2)
		}
		o.home = filepath.Join(userHome, ".culler")
	}
	home, err := deployment.CanonicalHome(o.home)
	if err != nil {
		return fail(err, 2)
	}
	diagnosticHome = home
	if o.command == "version" {
		installed, err := cullerInstalled(home)
		out.Field("CLI release", cullerReleaseLabel(cullerBuild(), releaseVersion))
		out.Field("Culler release", cullerReleaseLabel(installed, cullerInstalledVersion(home, installed)))
		if installed != "Not installed" && installed != "Unknown" && installed != cullerBuild() {
			out.Panel("Release mismatch", []string{"The command and installed Culler use different releases.", "Use the installer to repair the release:"})
			out.Literal(deployment.CLICommand(home, "update"))
		}
		if err != nil {
			return fail(err, 1)
		}
		if err := inspectCullerCommandFiles(home); err != nil {
			return fail(fmt.Errorf("%w\n%s", err, cullerInstallCommand(home)), 1)
		}
		return 0
	}
	if runtime.GOOS != "darwin" || !canInspectProjectVision() {
		return fail(errors.New("Lifecycle operations require the Culler Mac host."), 2)
	}
	if id, err := verbs.LoadIdentity(); err != nil {
		return fail(err, 2)
	} else if err := verbs.RequireHostScope(id, "Culler lifecycle operations"); err != nil {
		return fail(errors.New("Lifecycle operations require the Culler Mac host."), 2)
	}
	previous, present := os.LookupEnv("MC_HOME")
	if err := os.Setenv("MC_HOME", home); err != nil {
		return fail(err, 2)
	}
	defer func() {
		if present {
			_ = os.Setenv("MC_HOME", previous)
		} else {
			_ = os.Unsetenv("MC_HOME")
		}
	}()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	switch o.command {
	case "uninstall":
		o.home = home
		return deps.uninstall(ctx, o, stdin, stdout, stderr)
	case "status":
		ctx, end := context.WithTimeout(ctx, 9*time.Second)
		defer end()
		s := deps.inspect(ctx, home)
		if errors.Is(ctx.Err(), context.Canceled) {
			diagnostic.Panel("You cancelled the status check", nil)
			return 130
		}
		renderCullerStatus(out, s)
		if s.OK {
			return 0
		}
		return 1
	case "logs":
		unit, stream := o.service, "stderr"
		if unit == "" {
			unit = "resident"
		}
		if unit == "agentsview" {
			stream = "debug"
		}
		result, err := verbs.LogsTail(unit, stream, "text", o.tail)
		if err != nil {
			return fail(err, 1)
		}
		out.Panel("Service logs", []string{unit + " · " + stream})
		lines, _ := result.(map[string]any)["lines"].([]string)
		if len(lines) == 0 {
			out.Panel("No log records", nil)
		}
		for _, line := range lines {
			out.Literal(setupSafeDiagnostic(line))
		}
		return 0
	case "update":
		if o.check {
			ctx, end := context.WithTimeout(ctx, 25*time.Second)
			defer end()
			var s releaseStatus
			started := time.Now()
			checkErr := diagnostic.ActivityProgress("Check release availability", func(report func(string)) error {
				return cullerReportWait(ctx, func() error {
					s = deps.release(ctx, home)
					if ctx.Err() != nil {
						return ctx.Err()
					}
					if s.State == "unknown" {
						return errors.New(s.Detail)
					}
					return nil
				}, func() {
					report(fmt.Sprintf("Check release availability · %s elapsed", time.Since(started).Round(time.Second)))
				})
			})
			if errors.Is(ctx.Err(), context.Canceled) {
				diagnostic.Panel("You cancelled the release check", nil)
				return 130
			}
			if checkErr != nil {
				s.State = "unknown"
				if errors.Is(checkErr, context.DeadlineExceeded) {
					s.Detail = "The release check exceeded its time limit. Retry the release check."
				} else if s.Detail == "" {
					s.Detail = setupSafeDiagnostic(checkErr.Error())
				}
			}
			out.Field("Installed", cullerReleaseLabel(s.Installed, s.InstalledVersion))
			if s.Available != "" {
				out.Field("Available", cullerReleaseLabel(s.Available, s.AvailableVersion))
			}
			if s.State == "unknown" {
				out.CommandResult("Release availability is unknown", false)
				return fail(errors.New(s.Detail), 1)
			}
			out.Panel("Release check", []string{s.Detail})
			return 0
		}
		return cullerUpdateAction(ctx, home, o.noColor, o.restoreUpdate, stdin, stdout, stderr)
	default:
		manager, err := deps.manager(home)
		if err != nil {
			return fail(err, 1)
		}
		err = diagnostic.ActivityProgress(strings.ToUpper(o.command[:1])+o.command[1:]+" Culler services", func(report func(string)) error {
			return manager.change(ctx, o.command, o.service, report)
		})
		if err != nil {
			code := 1
			if errors.Is(err, context.Canceled) {
				code = 130
				diagnostic.Panel("The service request was cancelled", []string{setupSafeDiagnostic(strings.ReplaceAll(err.Error(), "context canceled", "The operator cancelled the request."))})
			} else {
				fail(err, code)
			}
			readCtx, end := context.WithTimeout(context.Background(), 9*time.Second)
			defer end()
			renderCullerStatus(out, deps.inspect(readCtx, home))
			return code
		}
		renderCullerChangeResult(out, o.command, o.service)
		return 0
	}
}

func renderCullerChangeResult(out *onboarding.Terminal, command, service string) {
	out.CommandResult("The service request completed", true)
	selected := deployment.SupervisedUnits
	if service != "" {
		selected = []string{service}
	}
	state := "Ready"
	if command == "stop" {
		state = "Stopped by your choice"
	}
	for _, unit := range selected {
		out.Field(unit, state)
	}
	if command == "stop" {
		lines := []string{"The stop choice remains in effect after a computer restart."}
		if service == "dashboard" {
			lines = append(lines, "Dashboard access is unavailable.", "The resident and active agents remain unchanged.")
		}
		if service == "agentsview" {
			lines = append(lines, "Trace access is unavailable.", "The resident and active agents remain unchanged.")
		}
		out.Panel("Service stop", lines)
	}
}

func renderCullerStatus(t *onboarding.Terminal, s cullerStatus) {
	t.CommandResult(s.Overall, s.OK && s.Overall == "Ready")
	t.Field("Home", s.Home)
	t.Field("Culler release", cullerReleaseLabel(s.Release, s.Version))
	if s.CLI != s.Release && s.Release != "Not installed" && s.Release != "Unknown" {
		t.Field("CLI release", cullerReleaseLabel(s.CLI, releaseVersion))
		t.Panel("Release mismatch", []string{"The command and Culler use different releases."})
	}
	for _, service := range s.Services {
		t.Panel(service.Name+" · "+service.State, []string{service.Detail})
	}
	if s.Address != "" {
		t.Panel("Dashboard", nil)
		t.Literal(s.Address)
	}
	if s.Operation != "" {
		t.Panel("Operation", []string{s.Operation})
	}
	if s.Detail != "" {
		t.Panel("Details", []string{s.Detail})
	}
	if !s.OK {
		next, command := cullerNext(s)
		t.Panel("Next", []string{next})
		if command != "" {
			t.Literal(command)
		}
	}
}

func cullerNext(s cullerStatus) (string, string) {
	if s.Next != "" || s.NextCommand != "" {
		return s.Next, s.NextCommand
	}
	if s.Release == "Not installed" || s.Release == "Unknown" {
		return "Use the installer to complete setup.", cullerInstallCommand(s.Home)
	}
	if s.Release != s.CLI || s.Operation != "" || s.Detail != "" {
		return "Use the installer to inspect the release.", deployment.CLICommand(s.Home, "update")
	}
	for _, service := range s.Services {
		if service.State == "Ready" || service.Stopped {
			continue
		}
		if strings.Contains(service.Detail, "Docker") {
			return "Open Docker Desktop, then check the deployment.", deployment.CLICommand(s.Home, "status")
		}
		if service.State == "Stopped" {
			return "Start the absent service.", deployment.CLICommand(s.Home, "start "+service.Name)
		}
		return "Inspect the service diagnostics.", deployment.CLICommand(s.Home, "logs "+service.Name)
	}
	return "Inspect the deployment through your coding harness.", ""
}

func cullerInstallCommand(home string) string {
	installer := os.Getenv("CULLER_SOURCE_INSTALLER")
	if installer == "" {
		if source, err := deployment.RuntimeSourceDirectory(home); err == nil {
			installer = filepath.Join(source, "scripts", "install-source.sh")
		}
	}
	if filepath.IsAbs(installer) {
		return "sh " + cullerQuote(installer) + " --mc-home " + cullerQuote(home)
	}
	return "curl -fsSL " + cullerInstallerURL + " | bash -s -- --mc-home " + cullerQuote(home)
}

func inspectCuller(ctx context.Context, home string) cullerStatus {
	userHome, _ := os.UserHomeDir()
	return inspectCullerWith(ctx, home, cullerObservations{
		loaded: func(ctx context.Context, unit string) (bool, error) {
			return (cullerLaunchd{ctx}).Loaded(cullerLabel(home, unit))
		},
		quiet: func(ctx context.Context, unit string) error { return cullerQuiet(ctx, home, unit) },
		ready: func(ctx context.Context, unit, release string, r cullerRuntime) error {
			if err := deployment.InspectServiceDefinition(home, filepath.Join(userHome, "Library", "LaunchAgents"), unit, release); err != nil {
				return err
			}
			return cullerReady(ctx, home, unit, r, time.Time{})
		},
	})
}

type cullerObservations struct {
	loaded func(context.Context, string) (bool, error)
	quiet  func(context.Context, string) error
	ready  func(context.Context, string, string, cullerRuntime) error
}

func inspectCullerWith(ctx context.Context, home string, observe cullerObservations) cullerStatus {
	s := cullerStatus{Home: home, CLI: cullerBuild(), Overall: "Status unavailable"}
	var err error
	s.Release, err = cullerInstalled(home)
	s.Version = cullerInstalledVersion(home, s.Release)
	if err != nil {
		s.Detail = setupSafeDiagnostic(err.Error())
	}
	if s.Release == "Not installed" {
		s.Overall = "Culler is not set up"
		return s
	}
	s.Operation = cullerOperationDetail(home)
	if op, err := readCullerOperation(home); err != nil {
		s.Next, s.NextCommand = "Ask your coding harness to inspect the service recovery record:", filepath.Join(home, cullerOperationRecord)
	} else if op != nil {
		command := op.Command
		if len(op.Services) == 1 {
			command += " " + op.Services[0]
		}
		s.Next, s.NextCommand = "Repeat the interrupted service request to check recovery:", deployment.CLICommand(home, command)
	}
	r, runtimeErr := readCullerRuntime(home)
	stops, stopErr := deployment.ReadServiceStops(home)
	cfg, configErr := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if configErr == nil {
		s.Address = cullerDashboardAddress(cfg.Surfaces.Dashboard.Bind)
	}
	if runtimeErr != nil || configErr != nil || stopErr != nil {
		s.Detail = "Setup is incomplete or its local records are unavailable."
	} else if err := inspectCullerCommandFiles(home); err != nil {
		s.Detail = setupSafeDiagnostic(err.Error())
	}
	s.Services = make([]cullerService, len(deployment.SupervisedUnits))
	var wg sync.WaitGroup
	for i, unit := range deployment.SupervisedUnits {
		wg.Add(1)
		go func() {
			defer wg.Done()
			service := cullerService{Name: unit, State: "Unknown"}
			defer func() { s.Services[i] = service }()
			loaded, err := observe.loaded(ctx, unit)
			if err != nil {
				service.Detail = setupSafeDiagnostic(err.Error())
				return
			}
			if !loaded {
				if err := observe.quiet(ctx, unit); err != nil {
					service.Detail = err.Error()
					return
				}
				service.State = "Stopped"
				service.Stopped = stopErr == nil && stops[unit]
				if service.Stopped {
					service.Detail = "Your stop choice remains in effect."
				} else {
					service.Detail = "The service stopped without a saved stop choice."
				}
				return
			}
			if runtimeErr != nil || configErr != nil {
				service.Detail = "The local service configuration is unavailable."
				return
			}
			service.State = "Needs attention"
			if err := observe.ready(ctx, unit, s.Release, r); err != nil {
				service.Detail = setupSafeDiagnostic(err.Error())
				return
			}
			if stops[unit] {
				service.Detail = "The service is active despite your stop choice."
				return
			}
			service.State, service.Detail = "Ready", "The current service check passed."
		}()
	}
	wg.Wait()
	s.OK = s.Detail == "" && s.Release == s.CLI && s.Operation == ""
	allReady, allStopped := true, true
	for _, service := range s.Services {
		allReady = allReady && service.State == "Ready"
		allStopped = allStopped && service.State == "Stopped"
		if service.State != "Ready" && !(service.State == "Stopped" && service.Stopped) {
			s.OK = false
		}
	}
	s.Overall = "Needs attention"
	if allReady && s.OK {
		s.Overall = "Ready"
	} else if allStopped {
		s.Overall = "Stopped"
	} else if s.OK {
		s.Overall = "Some services are stopped"
	}
	return s
}

func cullerDashboardAddress(bind string) string {
	host, port, err := net.SplitHostPort(bind)
	if err != nil {
		return ""
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/"
}

func cullerReady(ctx context.Context, home, unit string, r cullerRuntime, since time.Time) error {
	if err := inspectCullerCommandFiles(home); err != nil {
		return err
	}
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	switch unit {
	case "resident":
		if err := deployment.InspectResidentReceipt(home, r.Release, since, time.Now()); err != nil {
			return err
		}
		availability := verbs.ResidentAvailability(home, time.Now())
		if !availability.Available || !availability.CanCommit {
			return errors.New("The resident cannot accept current work. Inspect its service diagnostics.")
		}
		if err := exec.CommandContext(ctx, r.Docker, "info", "--format", "{{.ServerVersion}}").Run(); err != nil {
			return errors.New("Docker is unavailable. Open Docker Desktop, then check the deployment.")
		}
		return nil
	case "agentsview":
		return deployment.PingAgentsView(ctx, home, cfg.Surfaces.AgentsView.Bind)
	case "dashboard":
		address := cullerDashboardAddress(cfg.Surfaces.Dashboard.Bind)
		host, port, err := net.SplitHostPort(cfg.Surfaces.Dashboard.Bind)
		if err != nil {
			return err
		}
		// The selected port belongs to this Mac even when the browser bind is public.
		if host != "::1" {
			host = "127.0.0.1"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/api/health", nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+cfg.Surfaces.Dashboard.AuthToken)
		client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("The dashboard did not answer at %s", address)
		}
		defer response.Body.Close()
		var health struct {
			OK       *bool             `json:"ok"`
			Findings []json.RawMessage `json:"findings"`
		}
		// The health result describes the resident. Its authenticated response proves the dashboard can serve its host command.
		if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&health) != nil || health.OK == nil || len(health.Findings) == 0 {
			return errors.New("The dashboard failed its authenticated service check.")
		}
		return nil
	}
	return errors.New("The service name is invalid.")
}

func cullerQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func cullerUpdate(ctx context.Context, home string, noColor bool, stdin io.Reader, stdout, stderr io.Writer) int {
	return cullerUpdateAction(ctx, home, noColor, false, stdin, stdout, stderr)
}

func cullerUpdateAction(ctx context.Context, home string, noColor, restore bool, stdin io.Reader, stdout, stderr io.Writer) int {
	if code, handled := runRetainedUpdate(ctx, home, restore, noColor, stdin, stdout, stderr); handled {
		return code
	}
	launcher := filepath.Join(home, "bin", "install.sh")
	info, err := os.Lstat(launcher)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		t := onboarding.NewCommandTerminal(stderr, noColor)
		t.Problem("The saved installer is unavailable", []string{"Use the installer to resume setup."})
		t.Literal(cullerInstallCommand(home))
		return 1
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Getuid()) || owner.Nlink != 1 {
		t := onboarding.NewCommandTerminal(stderr, noColor)
		t.Problem("The saved installer has an invalid owner or link", []string{"Use the installer to repair setup."})
		t.Literal(cullerInstallCommand(home))
		return 1
	}
	args := []string{launcher, "--mc-home", home}
	if restore {
		args = append(args, "--restore")
	}
	command := exec.CommandContext(ctx, "/bin/bash", args...)
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	command.Stdin, command.Stdout, command.Stderr = stdin, stdout, stderr
	command.Env = append(os.Environ(), "CULLER_PARENT_PATH="+os.Getenv("PATH"))
	if noColor {
		command.Env = append(command.Env, "NO_COLOR=1")
	}
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return 130
		}
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		onboarding.NewCommandTerminal(stderr, noColor).Problem("The installer could not start", []string{setupSafeDiagnostic(err.Error())})
		return 1
	}
	return 0
}

func cullerQuiet(ctx context.Context, home, unit string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	body, err := exec.CommandContext(ctx, "/bin/ps", "-axo", "command=").Output()
	if err != nil {
		return errors.New("The service process check is unavailable.")
	}
	marker := filepath.Join(home, "supervision", unit+".json")
	if unit == "agentsview" {
		marker = deployment.AgentsViewPathsFor(home).Executable
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.Contains(line, marker) {
			return errors.New("A service process remains active outside macOS supervision.")
		}
	}
	return nil
}
