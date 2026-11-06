//go:build !test_fake_routing

package main

import (
	"context"
	"encoding/json"
	"errors"
	operatorconfig "mc/config"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mc/deployment"
	"mc/onboarding"
	"mc/verbs"
)

func setupReleaseFixture(t *testing.T) (string, verbs.OnboardArgs) {
	t.Helper()
	w, _ := setupTestWizard(t)
	source := t.TempDir()
	for _, group := range []struct {
		root  string
		files []string
	}{{source, deployment.HostReleaseFiles()}, {filepath.Join(source, "runner"), deployment.RunnerReleaseFiles()}} {
		for _, name := range group.files {
			path := filepath.Join(group.root, name)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("fixture\n"), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := deployment.InstallHostRelease(w.home, source); err != nil {
		t.Fatal(err)
	}
	if _, err := deployment.InstallRunnerRelease(w.home, filepath.Join(source, "runner")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(w.home, "supervision"), 0700); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"releaseBuildId": releaseBuildID})
	if err := os.WriteFile(filepath.Join(w.home, "supervision", "resident.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	return w.home, verbs.OnboardArgs{HostReleaseSource: source, ReleaseSource: filepath.Join(source, "runner")}
}

func TestSetupReleaseSeparatesFileDifferencesAndAbsentFiles(t *testing.T) {
	home, args := setupReleaseFixture(t)
	state := inspectSetupRelease(home, args, onboarding.DockerStatus{})
	if state.differs() {
		t.Fatalf("identical files differ: %+v", state)
	}
	hostFile := "resident/src/main.ts"
	path := filepath.Join(args.HostReleaseSource, hostFile)
	if err := os.WriteFile(path, []byte("modified\n"), 0644); err != nil {
		t.Fatal(err)
	}
	state = inspectSetupRelease(home, args, onboarding.DockerStatus{})
	if !state.differs() || !strings.Contains(strings.Join(state.lines(), "\n"), "Host files differ") || state.Installed != state.Proposed {
		t.Fatalf("file difference: %+v", state)
	}
	if err := os.Remove(filepath.Join(home, "release", "host", hostFile)); err != nil {
		t.Fatal(err)
	}
	state = inspectSetupRelease(home, args, onboarding.DockerStatus{})
	if state.differs() || state.Components[1].Status != "incomplete" {
		t.Fatalf("absent files became a release update: %+v", state)
	}
}

func TestSetupReleaseReportsOlderCommitAndLocalChanges(t *testing.T) {
	home, args := setupReleaseFixture(t)
	git := func(argv ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", args.HostReleaseSource}, argv...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.test", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.test")
		body, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git: %s: %v", body, err)
		}
		return strings.TrimSpace(string(body))
	}
	writeVersion := func(version string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(args.HostReleaseSource, "VERSION"), []byte(version+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	writeVersion("0.1.0")
	git("add", ".")
	git("commit", "-qm", "First release")
	older := git("rev-parse", "HEAD")
	writeVersion("0.2.0")
	git("commit", "-qam", "Second release")
	newer := git("rev-parse", "HEAD")
	previous, previousVersion := releaseBuildID, releaseVersion
	releaseBuildID, releaseVersion = older, "0.1.0"
	t.Cleanup(func() { releaseBuildID, releaseVersion = previous, previousVersion })
	body, _ := json.Marshal(map[string]string{"releaseBuildId": newer})
	if err := os.WriteFile(filepath.Join(home, "supervision", "resident.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(args.HostReleaseSource, deployment.HostReleaseFiles()[0]), []byte("local change\n"), 0644); err != nil {
		t.Fatal(err)
	}
	state := inspectSetupRelease(home, args, onboarding.DockerStatus{})
	if state.Relation != "older" || state.SourceState != "modified" || state.Installed != newer || state.Proposed != older {
		t.Fatalf("release comparison: %+v", state)
	}
	if state.InstalledDisplay != "0.2.0 ("+newer[:12]+")" || state.ProposedDisplay != "0.1.0 ("+older[:12]+")" {
		t.Fatalf("release labels: %q %q", state.InstalledDisplay, state.ProposedDisplay)
	}
}

func TestSetupGroupsBlockedChecksAndKeepsIndependentFailure(t *testing.T) {
	state := completeSetupDeployment()
	for i := range state.Checks {
		f := &state.Checks[i]
		if slices.Contains([]string{"spine", "deployment-identity", "surfaces", "container-runtime"}, f.Check) {
			f.Status, f.Detail = "fail", "not checked: spine unavailable"
			if f.Check == "spine" {
				f.Detail = "not checked: helper capability crossing failed: private doctor-runtime build/schema identity mismatch"
			}
		}
		if f.Check == "runtime-auth" {
			f.Status, f.Detail = "fail", "credential expired"
		}
	}
	state.normalizeChecks()
	lines := strings.Join(state.issueLines(), "\n")
	for _, check := range []string{"spine", "deployment-identity", "surfaces", "container-runtime"} {
		if state.Blocked[check] != "release" || state.ready(check) {
			t.Fatalf("blocked check %s: %+v", check, state)
		}
	}
	if strings.Count(lines, "The installed release cannot run checks for the proposed release") != 1 || !strings.Contains(lines, "Agent credentials: failed") || !strings.Contains(lines, "Database health: unknown") || strings.Contains(lines, "capability") {
		t.Fatalf("explanation: %s", lines)
	}
	if !state.Update || state.complete() {
		t.Fatal("incompatible components passed")
	}
}

func TestSetupDiagnosticPreservesEvidenceAndRemovesSecrets(t *testing.T) {
	w, _ := setupTestWizard(t)
	t.Setenv("FIXTURE_API_KEY", "secret-environment-value")
	state := completeSetupDeployment()
	state.Checks[0].Status = "fail"
	state.Checks[0].Detail = "expected release=abc; observed release=def; fixture-dashboard-access; api_key=secret-environment-value"
	state.Checks[2].Status, state.Checks[2].Detail = "deferred", "not checked: helper unavailable"
	path, err := writeSetupDiagnostic(w.home, "Inspect existing setup", &state, errors.New("fixture-dashboard-access"))
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Dir(path))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"fixture-dashboard-access", "secret-environment-value"} {
		if strings.Contains(string(body), secret) {
			t.Fatal("a secret reached the diagnostic")
		}
	}
	for _, evidence := range []string{"expected release=abc", "observed release=def", "observed_at", "Inspect existing setup", "not checked: helper unavailable", "expected_runtime_identity", `"observed_runtime_identity": "unavailable"`} {
		if !strings.Contains(string(body), evidence) {
			t.Fatalf("missing %s: %s", evidence, body)
		}
	}
	for _, item := range []struct {
		path string
		mode os.FileMode
	}{{path, 0600}, {filepath.Dir(path), 0700}} {
		info, err := os.Stat(item.path)
		if err != nil || info.Mode().Perm() != item.mode {
			t.Fatalf("diagnostic access: %v %v", info, err)
		}
	}
}

func TestSetupFinalCheckFailurePreventsCompletion(t *testing.T) {
	for _, status := range []string{"fail", "deferred"} {
		for i := range completeSetupDeployment().Checks {
			w, _ := setupTestWizard(t)
			state := completeSetupDeployment()
			state.Checks[i].Status = status
			lines, err := setupReadinessResult(w.home, state)
			var failure *setupFailure
			if len(lines) != 0 || !errors.As(err, &failure) || failure.record == "" {
				t.Fatalf("completion accepted %s=%s: %v", state.Checks[i].Check, status, err)
			}
			_ = os.RemoveAll(filepath.Dir(failure.record))
		}
	}
	w, _ := setupTestWizard(t)
	lines, err := setupReadinessResult(w.home, completeSetupDeployment())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(lines, []string{"All required checks passed.", "Resident service: active.", "Dashboard service: active.", "AgentsView service: active."}) {
		t.Fatalf("readiness = %v", lines)
	}
}

func TestSetupExitReportsPartialStopAndActivation(t *testing.T) {
	for _, active := range []bool{false, true} {
		w, ui := setupTestWizard(t)
		w.servicesTouched = true
		w.deps.serviceStates = func(string) (map[string]bool, error) {
			return map[string]bool{"resident": false, "dashboard": active}, nil
		}
		w.exitNotice()
		lines := strings.Join(ui.panels, "\n")
		want := "Dashboard service: stopped."
		if active {
			want = "Dashboard service: active."
		}
		if !strings.Contains(lines, "Resident service: stopped.") || !strings.Contains(lines, want) ||
			!strings.Contains(lines, "AgentsView service: unknown.") || !strings.Contains(lines, cullerInstallCommand(w.home)) {
			t.Fatal(lines)
		}
	}
}

func TestInstallerRejectsDatabaseResetAuthorization(t *testing.T) {
	if _, err := parseTerminalSetupArgs([]string{"--allow-database-reset"}); err == nil {
		t.Fatal("the installer accepted reset authorization")
	}
	args, err := parseOnboardArgs([]string{"home", "--allow-database-reset"})
	if err != nil || !args.AllowDatabaseReset {
		t.Fatalf("deployment reset authorization: %+v %v", args, err)
	}
	if err := verbs.GateSetupOnboardArgs(args); err == nil {
		t.Fatal("public setup accepted reset authorization")
	}
	args, err = parseOnboardArgs([]string{"home"})
	if err != nil || args.AllowDatabaseReset {
		t.Fatalf("ordinary setup authorized a reset: %+v %v", args, err)
	}
}

func TestSetupRecordsLocalSourceOnlyAfterApplication(t *testing.T) {
	home, args := setupReleaseFixture(t)
	for _, argv := range [][]string{{"init", "-q"}, {"add", "."}} {
		if out, err := exec.Command("git", append([]string{"-C", args.HostReleaseSource}, argv...)...).CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	r := setupRelease{Proposed: releaseBuildID, SourceState: "modified"}
	r.compareLocalSource(home, args.HostReleaseSource)
	if !r.differs() || r.SourceDigest == "" {
		t.Fatalf("unrecorded local source passed: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(home, "release", "setup-source.json")); !os.IsNotExist(err) {
		t.Fatal("inspection changed the deployment")
	}
	if err := saveSetupSource(home, args); err != nil {
		t.Fatal(err)
	}
	r.Components = nil
	r.compareLocalSource(home, args.HostReleaseSource)
	if r.differs() {
		t.Fatal("the applied source still requires an update")
	}
	if err := os.MkdirAll(filepath.Join(args.HostReleaseSource, "mc"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(args.HostReleaseSource, "mc", "changed.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	r.compareLocalSource(home, args.HostReleaseSource)
	if !r.differs() {
		t.Fatal("local Go changes passed as the installed image")
	}
}

func TestSetupUpdateAutomaticallyRepeatsBlockedChecks(t *testing.T) {
	w, _ := setupTestWizard(t, "update", "start", "finish")
	state := completeSetupDeployment()
	state.Update = true
	state.Checks[1].Status, state.Checks[1].Detail = "fail", "not checked: private doctor-runtime build/schema identity mismatch"
	w.session.RoutingFile = setupRoleFixture(t, w)
	body, err := os.ReadFile(w.session.RoutingFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.home, "routing.md"), body, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Runtimes.EnabledBindings = slices.Clone(w.session.RuntimeBindings)
	if _, err := operatorconfig.WriteAtomic(filepath.Join(w.home, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	w.deps.inspect = func(string, verbs.OnboardArgs) (setupDeployment, error) { return state, nil }
	stopped, built, prepared, activated, checked := false, false, false, false, false
	w.deps.stop = func(string) error { stopped = true; return nil }
	w.deps.rebuildImage = func(_ context.Context, _, _ string, _ func(string)) error {
		if !stopped {
			t.Fatal("image replacement preceded the service stop")
		}
		built = true
		return nil
	}
	w.deps.section = func(section string, _ verbs.OnboardArgs) error {
		if section == "home" {
			if !built {
				t.Fatal("preparation preceded the image build")
			}
			prepared = true
		}
		if section == "supervision" {
			if !prepared {
				t.Fatal("activation preceded preparation")
			}
			activated = true
		}
		return nil
	}
	w.deps.ready = func(string) ([]string, error) {
		if !activated {
			t.Fatal("final checks preceded activation")
		}
		checked = true
		return []string{"All required checks passed."}, nil
	}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	if !checked {
		t.Fatal("the update omitted the automatic final checks")
	}
}

func TestSetupDiagnosticRecordsObservedCompatibilityIdentifiers(t *testing.T) {
	w, _ := setupTestWizard(t)
	state := setupDeployment{Checks: []verbs.DoctorFinding{{Check: "spine", Status: "deferred", Detail: "not checked: identity mismatch: expected protocol=1 release=abc; observed protocol=1 release=def control=1 schema=xyz config=1"}}}
	path, err := writeSetupDiagnostic(w.home, "Inspect existing setup", &state, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Dir(path))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Observed string `json:"observed_runtime_identity"`
	}
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatal(err)
	}
	if record.Observed != "protocol=1 release=def control=1 schema=xyz config=1" {
		t.Fatalf("observed identity: %q", record.Observed)
	}
}

func TestSetupReleaseComparesImageIdentityWithoutContainerChanges(t *testing.T) {
	home, args := setupReleaseFixture(t)
	bin := t.TempDir()
	script := "#!/bin/sh\n[ \"$1 $2 $3 $4 $5\" = 'image inspect --format {{json .}} mc-prod' ] || exit 93\nprintf '%s' \"$FIXTURE_IMAGE\"\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, tc := range []struct {
		name, revision, id, platform string
		differs                      bool
	}{
		{"same", releaseBuildID, "sha256:" + strings.Repeat("a", 64), "arm64", false},
		{"other release", "previous-release", "sha256:" + strings.Repeat("a", 64), "arm64", true},
		{"invalid image identity", releaseBuildID, "sha256:invalid", "arm64", true},
		{"other architecture", releaseBuildID, "sha256:" + strings.Repeat("a", 64), "amd64", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"Id": tc.id, "Os": "linux", "Architecture": tc.platform, "Config": map[string]any{"Labels": map[string]string{"org.opencontainers.image.revision": tc.revision}}})
			t.Setenv("FIXTURE_IMAGE", string(body))
			release := inspectSetupRelease(home, args, onboarding.DockerStatus{State: onboarding.DockerReady})
			image := release.Components[3]
			if release.differs() != tc.differs || image.Installed != tc.revision {
				t.Fatalf("image comparison: %+v", release)
			}
		})
	}
}
