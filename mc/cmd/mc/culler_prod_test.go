//go:build !test_fake_routing

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mc/deployment"
	"mc/distribution"
	"mc/onboarding"
	"mc/verbs"
)

func TestCullerInstallCommandRetainsSourceInstallation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CULLER_SOURCE_INSTALLER", "")
	if !strings.HasPrefix(cullerInstallCommand(home), "curl -fsSL ") {
		t.Fatal("The published installation lost its recovery command.")
	}
	root := filepath.Join(t.TempDir(), "source checkout's files")
	build := filepath.Join(root, "release", "opencode", "build.sh")
	if err := os.MkdirAll(filepath.Dir(build), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(build, []byte("#!/bin/sh\n"), 0600); err != nil {
		t.Fatal(err)
	}
	want := "sh " + cullerQuote(filepath.Join(root, "scripts", "install-source.sh")) + " --mc-home " + cullerQuote(home)
	t.Setenv("CULLER_SOURCE_INSTALLER", filepath.Join(root, "scripts", "install-source.sh"))
	if got := cullerInstallCommand(home); got != want {
		t.Fatalf("The initial source setup command differs: %s", got)
	}
	t.Setenv("CULLER_SOURCE_INSTALLER", "")
	if err := deployment.ConfigureRuntimeSource(home, filepath.Join(root, "runner"), strings.Repeat("a", 40)); err != nil {
		t.Fatal(err)
	}
	if got := cullerInstallCommand(home); got != want {
		t.Fatalf("The installed source recovery command differs: %s", got)
	}
}

func TestCullerSourceRecoveryCommandRemainsExact(t *testing.T) {
	home, err := deployment.CanonicalHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CULLER_SOURCE_INSTALLER", "/Users/operator/source checkout/scripts/install-source.sh")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, width := range []string{"40", "80"} {
		t.Run(width, func(t *testing.T) {
			t.Setenv("COLUMNS", width)
			var out bytes.Buffer
			if code := runCuller([]string{"version", "--mc-home", home}, nil, &out, &out); code != 1 {
				t.Fatalf("The incomplete installation returned %d", code)
			}
			if !strings.Contains(out.String(), cullerInstallCommand(home)+"\n") {
				t.Fatalf("The recovery command contains inserted line breaks:\n%s", out.String())
			}
		})
	}
}

func TestCullerHelpAliasesAndGuidance(t *testing.T) {
	for _, command := range cullerCommands {
		for _, args := range [][]string{{command.name, "--help"}, {"help", command.name}} {
			var out, diagnostic bytes.Buffer
			if code := runCuller(args, nil, &out, &diagnostic); code != 0 {
				t.Fatalf("%v: %d %s", args, code, diagnostic.String())
			}
			for _, line := range []string{cullerLifecycleGuidance, cullerConfigurationGuidance} {
				if strings.Count(out.String(), line) != 1 {
					t.Fatalf("%v: %s", args, out.String())
				}
			}
			if strings.Contains(out.String(), "\x1b") {
				t.Fatal("redirected help contains terminal controls")
			}
		}
	}
	var first string
	for _, args := range [][]string{nil, {"help"}, {"--help"}} {
		var out bytes.Buffer
		runCuller(args, nil, &out, &out)
		if first != "" && first != out.String() {
			t.Fatal("main help aliases differ")
		}
		first = out.String()
	}
}

func TestCullerRejectsInvalidOptionsBeforeStateAccess(t *testing.T) {
	t.Setenv("MC_HOME", "relative")
	for _, args := range [][]string{{"start", "other"}, {"stop", "resident", "dashboard"}, {"status", "--check"}, {"logs", "--tail", "0"}, {"logs", "--tail", "1001"}, {"logs", "--tail", "one"}, {"version", "--tail", "2"}, {"config"}, {"__onboard"}, {"--mc-home"}, {"update", "--reset-database"}, {"stop", "--force"}} {
		var out bytes.Buffer
		if code := runCuller(args, nil, &out, &out); code != 2 {
			t.Fatalf("%v: %d %s", args, code, out.String())
		}
		if strings.Contains(out.String(), "absolute path") {
			t.Fatalf("state validation preceded usage validation: %v", args)
		}
	}
	for _, args := range [][]string{{"--mc-home", "/example", "--no-color", "logs", "dashboard", "--tail", "12"}, {"logs", "--tail", "12", "dashboard", "--mc-home", "/example", "--no-color"}} {
		o, err := parseCuller(args)
		if err != nil || o.service != "dashboard" || o.tail != 12 || o.home != "/example" || !o.noColor {
			t.Fatalf("%+v: %v", o, err)
		}
	}
}

func TestCullerVersionIsOfflineAndDistinguishesUnknown(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	t.Setenv("MC_HOME", home)
	t.Setenv("PATH", "/absent")
	for _, args := range [][]string{{"version"}, {"--version"}} {
		var out bytes.Buffer
		if code := runCuller(args, nil, &out, &out); code != 0 || !strings.Contains(out.String(), "Not installed") || !strings.Contains(out.String(), cullerConfigurationGuidance) {
			t.Fatalf("%d %s", code, out.String())
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("version created a deployment")
	}
	if err := os.MkdirAll(filepath.Join(home, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, distribution.InstalledRecord), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := runCuller([]string{"version"}, nil, &out, &out); code != 1 || !strings.Contains(out.String(), "Unknown") {
		t.Fatalf("%d %s", code, out.String())
	}
}

func TestCullerStatusPreservesPartialResultsAndSpecificActions(t *testing.T) {
	s := cullerStatus{Home: "/private/tmp/a custom deployment", Release: strings.Repeat("a", 40), CLI: strings.Repeat("a", 40), Overall: "Needs attention", Services: []cullerService{
		{Name: "resident", State: "Needs attention", Detail: "Docker is unavailable."}, {Name: "dashboard", State: "Ready"}, {Name: "agentsview", State: "Stopped", Stopped: true},
	}}
	var out bytes.Buffer
	renderCullerStatus(onboarding.NewCommandTerminal(&out, false), s)
	text := out.String()
	for _, required := range []string{"dashboard · Ready", "agentsview · Stopped", "Open Docker Desktop", deployment.CLICommand(s.Home, "status")} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing %s: %s", required, text)
		}
	}
	if strings.Contains(text, " update") {
		t.Fatal("Docker failure recommends a release update")
	}
	s.Services[0] = cullerService{Name: "resident", State: "Stopped"}
	_, command := cullerNext(s)
	if command != deployment.CLICommand(s.Home, "start resident") {
		t.Fatal(command)
	}
}

type cullerControlFixture struct {
	loaded, stopped map[string]bool
	started, ended  []string
	failStart       string
	onStop          func(string)
}

func (f *cullerControlFixture) Loaded(label string) (bool, error) { return f.loaded[label], nil }
func (f *cullerControlFixture) SetStopped(label string, stop bool) error {
	f.stopped[label] = stop
	return nil
}
func (f *cullerControlFixture) Bootstrap(path string) error {
	label := strings.TrimSuffix(filepath.Base(path), ".plist")
	if label == f.failStart {
		return errors.New("The service could not start.")
	}
	f.loaded[label] = true
	f.started = append(f.started, label)
	return nil
}
func (f *cullerControlFixture) Bootout(label string) error {
	f.loaded[label] = false
	f.ended = append(f.ended, label)
	if f.onStop != nil {
		f.onStop(label)
	}
	return nil
}

func cullerManagerFixture(t *testing.T) (*cullerManager, *cullerControlFixture, *verbs.ReleaseDatabase) {
	t.Helper()
	home, err := deployment.CanonicalHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_HOME", home)
	now := time.Now()
	control := &cullerControlFixture{loaded: map[string]bool{}, stopped: map[string]bool{}}
	for _, unit := range deployment.SupervisedUnits {
		control.loaded[cullerLabel(home, unit)] = true
	}
	database := &verbs.ReleaseDatabase{Schema: "fixture", Empty: true}
	m := &cullerManager{home: home, launchAgents: t.TempDir(), runtime: cullerRuntime{Release: strings.Repeat("a", 40)},
		control: func(context.Context) cullerServiceControl { return control },
		ready:   func(context.Context, string, time.Time) error { return nil },
		prepare: func(string) error { return nil }, validate: func(string) error { return nil }, quiet: func(context.Context, string) error { return nil },
		now: func() time.Time { return now }, sleep: func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() },
	}
	p := &publishedSetup{home: home, record: distribution.Record{Schema: "fixture"}, forceMaintenance: true, now: m.now,
		states: func(string) (map[string]bool, error) { return m.states(context.Background()) },
	}
	p.sleep = func(time.Duration) {
		now = now.Add(time.Minute)
		if err := writePublishedState(filepath.Join(p.home, "state", "install-maintenance-ack.json"), map[string]any{"id": p.maintenanceID, "ready": true, "observed_at": now}); err != nil {
			t.Fatal(err)
		}
	}
	p.docker = func(_ context.Context, args ...string) ([]byte, error) {
		switch args[0] {
		case "volume":
			return json.Marshal(map[string]any{"Name": args[len(args)-1], "Driver": "local"})
		case "container":
			return nil, nil
		default:
			return json.Marshal(database)
		}
	}
	m.maintenance = p
	return m, control, database
}

func TestCullerTargetedRestartPreservesOtherServices(t *testing.T) {
	m, control, _ := cullerManagerFixture(t)
	m.maintenance.docker = func(context.Context, ...string) ([]byte, error) {
		t.Fatal("dashboard restart checked active agents")
		return nil, nil
	}
	if err := m.change(context.Background(), "restart", "dashboard", func(string) {}); err != nil {
		t.Fatal(err)
	}
	label := cullerLabel(m.home, "dashboard")
	if len(control.ended) != 1 || control.ended[0] != label || len(control.started) != 1 || control.started[0] != label {
		t.Fatalf("start %v stop %v", control.started, control.ended)
	}
	for _, unit := range deployment.SupervisedUnits {
		if !control.loaded[cullerLabel(m.home, unit)] {
			t.Fatal(unit)
		}
	}
}

func TestCullerReadyStartAndRepeatedStopAreIdempotent(t *testing.T) {
	m, control, _ := cullerManagerFixture(t)
	if err := m.change(context.Background(), "start", "resident", func(string) {}); err != nil {
		t.Fatal(err)
	}
	if len(control.started)+len(control.ended) != 0 {
		t.Fatal("ready service restarted")
	}
	for _, unit := range deployment.SupervisedUnits {
		control.loaded[cullerLabel(m.home, unit)] = false
	}
	m.maintenance.docker = func(context.Context, ...string) ([]byte, error) {
		t.Fatal("already stopped services require Docker")
		return nil, nil
	}
	if err := m.change(context.Background(), "stop", "", func(string) {}); err != nil {
		t.Fatal(err)
	}
	stops, err := deployment.ReadServiceStops(m.home)
	if err != nil {
		t.Fatal(err)
	}
	for _, unit := range deployment.SupervisedUnits {
		if !stops[unit] || !control.stopped[cullerLabel(m.home, unit)] {
			t.Fatal("stop choice did not persist")
		}
	}
}

func TestCullerResidentStopWaitsForEveryWorkType(t *testing.T) {
	for _, kind := range []string{"pipeline or Scribe", "lease", "repository", "setup", "Homie"} {
		t.Run(kind, func(t *testing.T) {
			m, control, db := cullerManagerFixture(t)
			switch kind {
			case "pipeline or Scribe":
				db.Runs = 1
			case "lease":
				db.Leases = 1
			case "repository":
				db.RepositoryOperations = 1
			case "setup":
				db.EnvironmentOperations = 1
			case "Homie":
				db.HomieLaunches = 1
			}
			err := m.change(context.Background(), "stop", "resident", func(string) {})
			if err == nil || len(control.ended) != 0 {
				t.Fatalf("unsafe stop: %v %v", control.ended, err)
			}
			if _, err := os.Stat(filepath.Join(m.home, "state", "install-maintenance.json")); !os.IsNotExist(err) {
				t.Fatal("temporary maintenance remains")
			}
			if !control.loaded[cullerLabel(m.home, "resident")] {
				t.Fatal("the resident stopped")
			}
		})
	}
}

func TestCullerStopRechecksWorkAndRestoresAllServices(t *testing.T) {
	m, control, db := cullerManagerFixture(t)
	control.onStop = func(string) { db.Runs = 1 }
	err := m.change(context.Background(), "stop", "", func(string) {})
	if err == nil || !strings.Contains(err.Error(), "restored") {
		t.Fatal(err)
	}
	for _, unit := range deployment.SupervisedUnits {
		if !control.loaded[cullerLabel(m.home, unit)] || control.stopped[cullerLabel(m.home, unit)] {
			t.Fatal(unit)
		}
	}
}

func TestCullerFailedRestartRetainsRecoveryEvidence(t *testing.T) {
	m, control, _ := cullerManagerFixture(t)
	control.failStart = cullerLabel(m.home, "dashboard")
	err := m.change(context.Background(), "restart", "dashboard", func(string) {})
	if err == nil || !strings.Contains(err.Error(), "dashboard restoration failed") {
		t.Fatal(err)
	}
	op, err := readCullerOperation(m.home)
	if err != nil || op == nil || op.Phase != "recovery required" {
		t.Fatalf("%+v %v", op, err)
	}
	control.failStart = ""
	if err := m.change(context.Background(), "start", "dashboard", func(string) {}); err != nil {
		t.Fatal(err)
	}
	if !control.loaded[cullerLabel(m.home, "dashboard")] {
		t.Fatal("recovery did not restore the service")
	}
	if op, _ := readCullerOperation(m.home); op != nil {
		t.Fatal("completed recovery record remains")
	}
}

func TestCullerCancellationRestoresPriorChoices(t *testing.T) {
	m, control, _ := cullerManagerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	control.onStop = func(string) { cancel() }
	err := m.change(ctx, "stop", "dashboard", func(string) {})
	if !errors.Is(err, context.Canceled) || !control.loaded[cullerLabel(m.home, "dashboard")] || control.stopped[cullerLabel(m.home, "dashboard")] {
		t.Fatalf("%v %+v", err, control)
	}
}

func TestCullerServiceCommandsShareInstallerLock(t *testing.T) {
	m, control, _ := cullerManagerFixture(t)
	lock, err := lockPublishedSetup(m.home)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := m.change(context.Background(), "stop", "dashboard", func(string) {}); err == nil {
		t.Fatal("service command ignored installer lock")
	}
	if len(control.ended) != 0 {
		t.Fatal("conflicting command stopped a service")
	}
	other, _, _ := cullerManagerFixture(t)
	if err := other.change(context.Background(), "restart", "dashboard", func(string) {}); err != nil {
		t.Fatal("another deployment was blocked", err)
	}
}

func cullerObservationFixture(t *testing.T) (string, cullerObservations) {
	t.Helper()
	w, _ := setupTestWizard(t)
	canonical, err := deployment.CanonicalHome(w.home)
	if err != nil {
		t.Fatal(err)
	}
	w.home = canonical
	previous, previousVersion := releaseBuildID, releaseVersion
	releaseBuildID, releaseVersion = strings.Repeat("a", 40), "0.1.0"
	t.Cleanup(func() { releaseBuildID, releaseVersion = previous, previousVersion })
	record := publishedTestRecord(releaseBuildID, 1)
	record.Version, record.Tag = releaseVersion, "v"+releaseVersion
	if err := distribution.Save(filepath.Join(w.home, distribution.InstalledRecord), record); err != nil {
		t.Fatal(err)
	}
	if err := writePublishedState(filepath.Join(w.home, "supervision", "resident.json"), cullerRuntime{Release: releaseBuildID, Home: w.home, Program: "/fixture/bin/mc", Docker: "/fixture/bin/docker"}); err != nil {
		t.Fatal(err)
	}
	return w.home, cullerObservations{
		loaded: func(context.Context, string) (bool, error) { return true, nil },
		quiet:  func(context.Context, string) error { return nil },
		ready:  func(context.Context, string, string, cullerRuntime) error { return nil },
	}
}

func TestCullerStatusUsesIndependentBoundedObservations(t *testing.T) {
	home, observe := cullerObservationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	observe.loaded = func(ctx context.Context, unit string) (bool, error) {
		if unit == "resident" {
			<-ctx.Done()
			return false, ctx.Err()
		}
		return true, nil
	}
	s := inspectCullerWith(ctx, home, observe)
	if s.OK || s.Services[0].State != "Unknown" || s.Services[1].State != "Ready" || s.Services[2].State != "Ready" {
		t.Fatalf("%+v", s)
	}
}

func TestCullerStatusRequiresExplicitStopAndRejectsOrphanProcesses(t *testing.T) {
	home, observe := cullerObservationFixture(t)
	observe.loaded = func(context.Context, string) (bool, error) { return false, nil }
	s := inspectCullerWith(context.Background(), home, observe)
	if s.OK || s.Overall != "Stopped" {
		t.Fatalf("%+v", s)
	}
	if err := deployment.WriteServiceStops(home, deployment.ServiceStops{"resident": true, "dashboard": true, "agentsview": true}); err != nil {
		t.Fatal(err)
	}
	s = inspectCullerWith(context.Background(), home, observe)
	if !s.OK || s.Overall != "Stopped" {
		t.Fatalf("%+v", s)
	}
	observe.quiet = func(context.Context, string) error { return errors.New("A process remains active.") }
	s = inspectCullerWith(context.Background(), home, observe)
	if s.OK || s.Services[0].State != "Unknown" {
		t.Fatalf("%+v", s)
	}
}

func TestCullerCommittedStopSurvivesRecovery(t *testing.T) {
	m, control, _ := cullerManagerFixture(t)
	unit := "dashboard"
	control.loaded[cullerLabel(m.home, unit)] = false
	stops := deployment.ServiceStops{unit: true}
	if err := deployment.WriteServiceStops(m.home, stops); err != nil {
		t.Fatal(err)
	}
	op := &cullerOperation{ID: strings.Repeat("a", 32), Command: "stop", Services: []string{unit}, Prior: map[string]bool{unit: true}, Stops: deployment.ServiceStops{}}
	if err := m.save(op, "complete"); err != nil {
		t.Fatal(err)
	}
	if err := m.change(context.Background(), "start", "resident", func(string) {}); err != nil {
		t.Fatal(err)
	}
	if control.loaded[cullerLabel(m.home, unit)] {
		t.Fatal("recovery reversed a committed stop")
	}
}

func TestCullerDevelopmentIdentityMatchesInstalledDevelopment(t *testing.T) {
	home, _ := cullerObservationFixture(t)
	releaseBuildID = "dev"
	if err := os.Remove(filepath.Join(home, distribution.InstalledRecord)); err != nil {
		t.Fatal(err)
	}
	if err := writePublishedState(filepath.Join(home, "supervision", "resident.json"), cullerRuntime{Release: "dev", Home: home, Program: "/fixture/mc", Docker: "/fixture/docker"}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := runCuller([]string{"version", "--mc-home", home}, nil, &out, &out); code != 0 || strings.Contains(out.String(), "mismatch") || strings.Count(out.String(), "development") != 2 {
		t.Fatalf("%d %s", code, out.String())
	}
}

func TestCullerVersionShowsTheVersionAndTheShortCommit(t *testing.T) {
	home, _ := cullerObservationFixture(t)
	var out bytes.Buffer
	if code := runCuller([]string{"version", "--no-color", "--mc-home", home}, nil, &out, &out); code != 0 ||
		strings.Count(out.String(), "0.1.0 (aaaaaaaaaaaa)") != 2 || strings.Contains(out.String(), "mismatch") {
		t.Fatalf("%d %s", code, out.String())
	}
	releaseBuildID, releaseVersion = strings.Repeat("b", 40), "0.2.0"
	out.Reset()
	runCuller([]string{"version", "--no-color", "--mc-home", home}, nil, &out, &out)
	if !strings.Contains(out.String(), "0.2.0 (bbbbbbbbbbbb)") || !strings.Contains(out.String(), "0.1.0 (aaaaaaaaaaaa)") ||
		!strings.Contains(out.String(), "Release mismatch") {
		t.Fatalf("%s", out.String())
	}
	if got := cullerReleaseLabel("Not installed", "0.2.0"); got != "Not installed" {
		t.Fatalf("a state received a version: %q", got)
	}
}

func TestCullerUpdateUsesSavedInstallerAndSelectedHome(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/bash\nprintf '%s\\n' \"$@\" \"$NO_COLOR\"\nread -r answer\nprintf '%s\\n' \"$answer\"\nexit 7\n"
	if err := os.WriteFile(filepath.Join(home, "bin", "install.sh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := cullerUpdate(context.Background(), home, true, strings.NewReader("terminal input\n"), &out, &out); code != 7 || out.String() != "--mc-home\n"+home+"\n1\nterminal input\n" {
		t.Fatalf("%d %s", code, out.String())
	}
}

func TestCullerLogsSelectsHomeAndRetainsTail(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("The public log command requires the Mac host.")
	}
	home := t.TempDir()
	t.Setenv("MC_HOME", filepath.Join(t.TempDir(), "unrelated"))
	if err := os.Mkdir(filepath.Join(home, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "logs", "dashboard.stderr.log"), []byte("first\nsecond\nthird\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := runCuller([]string{"logs", "dashboard", "--tail", "2", "--mc-home", home}, nil, &out, &out); code != 0 || strings.Contains(out.String(), "first") || !strings.Contains(out.String(), "second\nthird\n") || strings.Contains(out.String(), "\x1b") {
		t.Fatalf("%d %s", code, out.String())
	}
}

func TestPublishedCancellationRestoresServicesAfterTheStop(t *testing.T) {
	manager, control, _ := cullerManagerFixture(t)
	setup := manager.maintenance
	setup.recoverOperation = func(operation *cullerOperation) error { return manager.restore(operation, func(string) {}) }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	setup.context = ctx
	if err := setup.stopAndRecheck(context.Background(), func(string) error {
		for _, unit := range deployment.SupervisedUnits {
			if err := control.Bootout(cullerLabel(manager.home, unit)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := setup.cancelOperation(); err != nil {
		t.Fatal(err)
	}
	for _, unit := range deployment.SupervisedUnits {
		if !control.loaded[cullerLabel(manager.home, unit)] {
			t.Fatal(unit)
		}
	}
}

func TestCullerReleaseDeadlineCannotReportSuccess(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("The public release check requires the Mac host.")
	}
	home := t.TempDir()
	var out bytes.Buffer
	deps := cullerDependencies{release: func(ctx context.Context, _ string) releaseStatus {
		<-ctx.Done()
		return releaseStatus{State: "available", Installed: strings.Repeat("a", 40), Available: strings.Repeat("b", 40), Detail: "A release is available."}
	}}
	code := runCullerWith([]string{"update", "--check", "--mc-home", home}, nil, &out, &out, deps)
	if code != 1 || !strings.Contains(out.String(), "exceeded its time limit") || strings.Contains(out.String(), "A release is available.") {
		t.Fatalf("%d %s", code, out.String())
	}
}

func TestCullerRejectsPartialCommandReplacement(t *testing.T) {
	home, _ := cullerObservationFixture(t)
	dir := filepath.Join(home, "bin")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	command, program := filepath.Join(dir, "culler"), filepath.Join(dir, "mc")
	for _, path := range []string{command, program} {
		if err := os.WriteFile(path, []byte("verified executable"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := deployment.CLIFileDigest(command)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePublishedState(filepath.Join(home, deployment.CLIInstallRecord), deployment.CLIInstallation{Path: command, SHA256: digest, Release: releaseBuildID, Verified: true}); err != nil {
		t.Fatal(err)
	}
	if err := writePublishedState(filepath.Join(home, "supervision", "resident.json"), cullerRuntime{Release: releaseBuildID, Home: home, Program: program, Docker: "/fixture/docker"}); err != nil {
		t.Fatal(err)
	}
	if err := inspectCullerCommandFiles(home); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program, []byte("partial update"), 0755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := runCuller([]string{"version", "--mc-home", home}, nil, &out, &out); code != 1 || !strings.Contains(out.String(), "command files differ") || !strings.Contains(out.String(), cullerInstallCommand(home)) {
		t.Fatalf("%d %s", code, out.String())
	}
}
