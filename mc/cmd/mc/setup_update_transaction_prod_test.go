//go:build !test_fake_routing

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	operatorconfig "mc/config"
	"mc/deployment"
	"mc/verbs"
)

func updateTransactionFixture(t *testing.T, phase string) *publishedSetup {
	t.Helper()
	home := t.TempDir()
	names, err := deployment.RuntimeNames(home)
	if err != nil {
		t.Fatal(err)
	}
	record := publishedTestRecord(strings.Repeat("a", 40), 20)
	j := &deployment.UpdateJournal{Version: 1, ID: strings.Repeat("a", 32), Deployment: names.Suffix, Phase: phase, Candidate: record, Previous: &record, PriorServices: map[string]bool{"resident": true}, PriorStops: deployment.ServiceStops{"dashboard": true}, BinDir: filepath.Join(home, "commands")}
	j.CandidateRoot = filepath.Join(deployment.UpdateDirectory(home, j.ID), "candidate")
	if err := deployment.SaveUpdateJournal(home, j); err != nil {
		t.Fatal(err)
	}
	return &publishedSetup{home: home, record: record, installed: record, update: j, maintenanceID: j.ID}
}

func TestUpdateSectionsPreserveConfigurationAndGateActivation(t *testing.T) {
	for _, failure := range []string{"", "home", "container", "supervision"} {
		t.Run(failure, func(t *testing.T) {
			p := updateTransactionFixture(t, "replace")
			var sections []string
			args := verbs.OnboardArgs{ReleaseSource: "/package/runner", HostReleaseSource: "/package/host", ActivateSupervision: true}
			err := p.applyUpdateSections(args, func(args []string) error {
				section := args[1]
				sections = append(sections, section)
				j, err := deployment.LoadUpdateJournal(p.home)
				if err != nil {
					t.Fatal(err)
				}
				want := "replace"
				if section == "supervision" {
					want = "verify"
				}
				if j.Phase != want {
					t.Fatalf("%s starts in %s, want %s", section, j.Phase, want)
				}
				if !j.BlocksWork() {
					t.Fatal("work became available before verification")
				}
				if section == failure {
					return errors.New("interrupted")
				}
				return nil
			})
			want := []string{"home", "container", "supervision"}
			if failure != "" {
				for i, section := range want {
					if section == failure {
						want = want[:i+1]
						break
					}
				}
			}
			if !reflect.DeepEqual(sections, want) {
				t.Fatalf("sections=%v, want %v", sections, want)
			}
			if (err != nil) != (failure != "") {
				t.Fatalf("err=%v", err)
			}
			if err := deployment.UpdateBlocksWork(p.home); err == nil {
				t.Fatal("incomplete update permits work")
			}
		})
	}
}

func TestUpdateSectionPhaseMustPersistBeforeActivation(t *testing.T) {
	p := updateTransactionFixture(t, "replace")
	called := false
	err := p.applyUpdateSections(verbs.OnboardArgs{}, func(args []string) error {
		if args[1] == "container" {
			p.update.ID = "invalid"
		}
		if args[1] == "supervision" {
			called = true
		}
		return nil
	})
	if err == nil || called {
		t.Fatalf("err=%v activation=%v", err, called)
	}
}

func TestUpdateCloseKeepsMaintenanceUntilCommit(t *testing.T) {
	for _, phase := range []string{"stop", "replace", "verify", "restore", "committed"} {
		t.Run(phase, func(t *testing.T) {
			p := updateTransactionFixture(t, phase)
			path := filepath.Join(p.home, "state", "install-maintenance.json")
			if err := writePublishedState(path, map[string]string{"id": p.update.ID}); err != nil {
				t.Fatal(err)
			}
			p.close()
			_, err := os.Stat(path)
			if (err == nil) == (phase == "committed") {
				t.Fatalf("phase=%s err=%v", phase, err)
			}
		})
	}
}

func TestRetainedUpdateRunsExactCandidateOffline(t *testing.T) {
	p := updateTransactionFixture(t, "replace")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\nprintf '%s\\n' \"$CULLER_PACKAGE_ROOT\" \"$CULLER_RELEASE_RECORD\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(root, "bin", "mc"), []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	if err := deployment.RetainUpdateCandidate(p.home, p.update, root); err != nil {
		t.Fatal(err)
	}
	if err := deployment.SaveUpdateJournal(p.home, p.update); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/absent")
	var out bytes.Buffer
	code, handled := runRetainedUpdate(context.Background(), p.home, true, true, nil, &out, &out)
	if code != 0 || !handled {
		t.Fatalf("%d %v %s", code, handled, out.String())
	}
	for _, want := range []string{p.update.CandidateRoot, p.update.CandidateRoot + "/release.json", "__setup", "--restore"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s: %s", want, out.String())
		}
	}
	if err := os.WriteFile(filepath.Join(p.update.CandidateRoot, "release.json"), []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	code, handled = runRetainedUpdate(context.Background(), p.home, false, true, nil, &out, &out)
	if code == 0 || !handled || !strings.Contains(out.String(), "checksum") {
		t.Fatalf("tampered package accepted: %d %v %s", code, handled, out.String())
	}
}

func TestUpdateRestoreRefusesCommittedData(t *testing.T) {
	p := updateTransactionFixture(t, "committed")
	var out bytes.Buffer
	code, handled := p.recoverUpdate([]string{"--restore", "--yes", "--interrupt-runs"}, verbs.OnboardArgs{}, nil, &out, &out)
	if code == 0 || !handled || !strings.Contains(out.String(), "already committed") {
		t.Fatalf("%d %v %s", code, handled, out.String())
	}
	j, err := deployment.LoadUpdateJournal(p.home)
	if err != nil || j.Phase != "committed" {
		t.Fatalf("%+v %v", j, err)
	}
}

func TestCullerRestoreOptionRequiresUpdate(t *testing.T) {
	for _, args := range [][]string{{"status", "--restore"}, {"update", "--restore", "--check"}, {"start", "--restore"}} {
		if _, err := parseCuller(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	options, err := parseCuller([]string{"update", "--restore"})
	if err != nil || !options.restoreUpdate {
		t.Fatalf("%+v %v", options, err)
	}
}

func init() {
	// A suffix selects the saved choices of the assessment: the repairs, and the count of profiles that wait for the live model list.
	assessments := map[string]struct {
		repair bool
		waits  int
	}{"-with-repair": {true, 0}, "-with-wait": {false, 1}, "-with-waits": {false, 2}, "-with-three-waits": {false, 3}, "-with-repair-and-wait": {true, 1}}
	names := []string{"public-update-confirm", "public-update-recovery", "public-update-restore"}
	for suffix := range assessments {
		names = append(names, "public-update-confirm"+suffix, "public-update-recovery"+suffix)
	}
	for _, name := range names {
		setupPreviewCases[name] = func(t *testing.T) setupPreviewCase {
			w, _ := setupTestWizard(t)
			p := updateTransactionFixture(t, "replace")
			p.record.Version = "1.1.0"
			for suffix, saved := range assessments {
				if strings.HasSuffix(name, suffix) {
					p.candidateAssessment = previewAssessment(t, saved.repair, saved.waits)
				}
			}
			preview := setupPreviewCase{wizard: w, steps: []setupPreviewStep{{Wait: "q exit", Send: "\r", Name: name}}}
			preview.run = func() {
				var err error
				if strings.HasPrefix(name, "public-update-confirm") {
					_, err = p.confirmUpdate(w.ui)
				} else {
					_, err = p.confirmRecovery(w.ui, name == "public-update-restore")
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			return preview
		}
	}
}

func TestUpdateCommitFailureKeepsPreviousPhase(t *testing.T) {
	p := updateTransactionFixture(t, "verify")
	if err := os.Remove(deployment.UpdateJournalPath(p.home)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(deployment.UpdateJournalPath(p.home), 0700); err != nil {
		t.Fatal(err)
	}
	if err := p.updatePhase("committed"); err == nil {
		t.Fatal("commit replaced a directory")
	}
	if p.update.Phase != "verify" || !p.update.BlocksWork() {
		t.Fatalf("failed commit published phase %s", p.update.Phase)
	}
}

func TestPublicUpdateRejectsConfigurationChanges(t *testing.T) {
	if err := validatePublicUpdateOptions(verbs.OnboardArgs{ReleaseSource: "/runner", HostReleaseSource: "/host", ActivateSupervision: true}); err != nil {
		t.Fatal(err)
	}
	for _, args := range []verbs.OnboardArgs{{AcceptDefaultRouting: true}, {RuntimeBindings: "new-profile"}, {DashboardBind: "localhost:8000"}, {TunablesConfirmed: true}} {
		if err := validatePublicUpdateOptions(args); err == nil {
			t.Fatalf("accepted configuration change: %+v", args)
		}
	}
}

func TestUpdateCleanupFailureKeepsCommitAndRetries(t *testing.T) {
	p := updateTransactionFixture(t, "committed")
	p.now = time.Now
	p.started = time.Now()
	p.installed.Image = "ghcr.io/test/culler@sha256:" + strings.Repeat("b", 64)
	if err := os.MkdirAll(p.update.CandidateRoot, 0700); err != nil {
		t.Fatal(err)
	}
	fail := true
	removals := 0
	p.docker = func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "exec" {
			return []byte(`{"digests":[]}`), nil
		}
		if args[0] == "image" && args[1] == "rm" {
			removals++
			if fail {
				return []byte("Docker refused image cleanup"), errors.New("cleanup failed")
			}
			return nil, nil
		}
		t.Fatalf("unexpected Docker command %v", args)
		return nil, nil
	}
	if err := p.complete(); err == nil || !strings.Contains(err.Error(), "retry cleanup") {
		t.Fatalf("cleanup error: %v", err)
	}
	j, err := deployment.LoadUpdateJournal(p.home)
	if err != nil || j.Phase != "committed" || j.BlocksWork() {
		t.Fatalf("failed cleanup changed commit: %+v %v", j, err)
	}
	if _, err := os.Stat(p.update.CandidateRoot); err != nil {
		t.Fatal("failed cleanup removed its recovery executable")
	}
	fail = false
	if err := p.complete(); err != nil {
		t.Fatal(err)
	}
	j, err = deployment.LoadUpdateJournal(p.home)
	if err != nil || j.Phase != "complete" {
		t.Fatalf("cleanup retry: %+v %v", j, err)
	}
	if _, err := os.Stat(p.update.CandidateRoot); !os.IsNotExist(err) {
		t.Fatal("successful cleanup kept its recovery package")
	}
	if removals != 2 {
		t.Fatalf("cleanup attempts %d", removals)
	}
}

func TestUpdateStopFailurePreservesIntentAndPreviousSoftware(t *testing.T) {
	p := updateTransactionFixture(t, "replace")
	original := publishedUpdateStopControl
	t.Cleanup(func() { publishedUpdateStopControl = original })
	publishedUpdateStopControl = func(home string, _ func(context.Context, ...string) ([]byte, error)) deployment.UpdateStopControl {
		return deployment.UpdateStopControl{Processes: func(context.Context) ([]deployment.UpdateProcess, error) {
			j, err := deployment.LoadUpdateJournal(home)
			if err != nil || j.Phase != "stop" {
				t.Fatalf("stop preceded durable intent: %+v %v", j, err)
			}
			return nil, errors.New("process inventory unavailable")
		}}
	}
	launcher := filepath.Join(p.home, "bin", "install.sh")
	if err := os.MkdirAll(filepath.Dir(launcher), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("previous launcher"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := p.beforeUpdate(context.Background(), func(string) {}); err == nil {
		t.Fatal("unknown process state permitted replacement")
	}
	p.close()
	j, err := deployment.LoadUpdateJournal(p.home)
	if err != nil || j.Phase != "stop" || j.Problem == "" {
		t.Fatalf("failed stop lost recovery: %+v %v", j, err)
	}
	body, err := os.ReadFile(launcher)
	if err != nil || string(body) != "previous launcher" {
		t.Fatal("failed stop changed previous software")
	}
	if _, err := os.Stat(filepath.Join(p.home, "state", "install-maintenance.json")); err != nil {
		t.Fatal("failed stop released maintenance")
	}
}

func TestUpdateDashboardReadinessAcceptsMaintenanceAndExplicitStops(t *testing.T) {
	for _, tc := range []struct {
		name, phase, header              string
		stopped, available, commit, want bool
	}{
		{"active resident", "verify", "match", false, true, false, true},
		{"explicit resident stop", "verify", "match", true, false, false, true},
		{"restore inactive resident", "restore", "match", true, false, false, true},
		{"missing identity", "verify", "", false, true, false, false},
		{"other release", "verify", "other", false, true, false, false},
		{"absent resident response", "verify", "match", false, false, false, false},
		{"work incorrectly available", "verify", "match", false, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := updateTransactionFixture(t, tc.phase)
			p.update.PriorServices["resident"] = !tc.stopped
			if err := deployment.SaveUpdateJournal(p.home, p.update); err != nil {
				t.Fatal(err)
			}
			if err := deployment.WriteServiceStops(p.home, deployment.ServiceStops{"resident": tc.stopped}); err != nil {
				t.Fatal(err)
			}
			if tc.available {
				now := time.Now().UTC().Format(time.RFC3339Nano)
				if err := writePublishedState(filepath.Join(p.home, "health", "resident-tick.json"), map[string]any{"version": 1, "config_schema_version": 1, "release_build_id": p.record.Commit, "resident_instance_id": "verification", "sequence": 1, "completed_at": now}); err != nil {
					t.Fatal(err)
				}
				if err := writePublishedState(filepath.Join(p.home, "health", "resident-availability.json"), map[string]any{"resident_instance_id": "verification", "observed_at": now, "can_commit": tc.commit}); err != nil {
					t.Fatal(err)
				}
			}
			cfg := operatorconfig.Default()
			cfg.Surfaces.Dashboard.AuthToken = "synthetic-token"
			server := httptest.NewServer(http.HandlerFunc(func(out http.ResponseWriter, req *http.Request) {
				if req.URL.Path != "/api/health" || req.Header.Get("Authorization") != "Bearer synthetic-token" {
					t.Error("readiness lost authentication or health endpoint")
				}
				identity := tc.header
				if identity == "match" {
					identity = p.record.Commit
				}
				if identity != "" {
					out.Header().Set("X-Culler-Release", identity)
				}
				_, _ = io.WriteString(out, `{"ok":false,"findings":[{"check":"resident-tick","status":"ok"}]}`)
			}))
			defer server.Close()
			cfg.Surfaces.Dashboard.Bind = strings.TrimPrefix(server.URL, "http://")
			if _, err := operatorconfig.WriteAtomic(filepath.Join(p.home, "config.toml"), cfg); err != nil {
				t.Fatal(err)
			}
			if err := setupCheckDashboard(p.home); (err == nil) != tc.want {
				t.Fatalf("ready=%t error=%v", tc.want, err)
			}
		})
	}
}

func TestCompletedUpdateRemovesMaintenanceWithoutCandidate(t *testing.T) {
	for _, phase := range []string{"complete", "restored"} {
		t.Run(phase, func(t *testing.T) {
			p := updateTransactionFixture(t, phase)
			if err := writePublishedState(filepath.Join(p.home, "state", "install-maintenance.json"), map[string]string{"id": p.update.ID}); err != nil {
				t.Fatal(err)
			}
			if err := deployment.UpdateBlocksWork(p.home); err == nil {
				t.Fatal("remaining maintenance permits work")
			}
			var out bytes.Buffer
			code, handled := runRetainedUpdate(context.Background(), p.home, false, true, nil, &out, &out)
			if code != 0 || handled {
				t.Fatalf("terminal recovery %d %v %s", code, handled, out.String())
			}
			if err := deployment.UpdateBlocksWork(p.home); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUpdateMaintenanceReleaseRequiresDurableTerminalJournal(t *testing.T) {
	for _, phase := range []string{"committed", "complete", "restored"} {
		t.Run(phase, func(t *testing.T) {
			p := updateTransactionFixture(t, phase)
			p.now = time.Now
			p.started = time.Now()
			if phase == "committed" {
				if err := os.MkdirAll(p.update.CandidateRoot, 0700); err != nil {
					t.Fatal(err)
				}
			}
			p.docker = func(_ context.Context, args ...string) ([]byte, error) {
				if args[0] == "exec" {
					return []byte(`{"digests":[]}`), nil
				}
				t.Fatalf("unexpected Docker command %v", args)
				return nil, nil
			}
			path := filepath.Join(p.home, "state", "install-maintenance.json")
			if err := writePublishedState(path, map[string]string{"id": p.update.ID}); err != nil {
				t.Fatal(err)
			}
			previousSave := saveUpdateActivationJournal
			t.Cleanup(func() { saveUpdateActivationJournal = previousSave })
			failure := errors.New("directory sync failed after journal rename")
			saves := 0
			saveUpdateActivationJournal = func(home string, journal *deployment.UpdateJournal) error {
				saves++
				if err := deployment.SaveUpdateJournal(home, journal); err != nil {
					return err
				}
				return failure
			}
			recover := func() error {
				if phase == "committed" {
					return p.complete()
				}
				return finishUpdateCleanup(context.Background(), p.home, p.update)
			}
			if err := recover(); !errors.Is(err, failure) {
				t.Fatalf("recovery lost the durability failure: %v", err)
			}
			journal := p.update
			p.update = nil
			p.close()
			p.update = journal
			if saves != 2 {
				t.Fatalf("recovery and deferred close made %d journal saves", saves)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("a failed journal sync removed maintenance")
			}
			if err := deployment.UpdateBlocksWork(p.home); err == nil {
				t.Fatal("a failed journal sync permitted work")
			}
			saveUpdateActivationJournal = previousSave
			if err := recover(); err != nil {
				t.Fatal(err)
			}
			if err := deployment.UpdateBlocksWork(p.home); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSetupClosePreservesIncompleteUpdateMaintenance(t *testing.T) {
	p := updateTransactionFixture(t, "verify")
	path := filepath.Join(p.home, "state", "install-maintenance.json")
	if err := writePublishedState(path, map[string]string{"id": p.update.ID}); err != nil {
		t.Fatal(err)
	}
	p.update = nil
	p.close()
	if _, err := os.Stat(path); err != nil {
		t.Fatal("setup close removed incomplete update maintenance")
	}
}

func TestUpdateLocalCredentialCheckAcceptsRemovedCatalogChoices(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	candidateProfileConfig(t, home, true)
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Runtimes.EnabledBindings = []string{"catalog"}
	if _, err := operatorconfig.WriteAtomic(filepath.Join(home, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(home, "refresh-grants")
	if err := os.Mkdir(store, 0700); err != nil {
		t.Fatal(err)
	}
	credential := []byte(`{"version":1,"type":"api_key","credential":"catalog","channel":"opencode","api_key":"synthetic-key"}`)
	if err := os.WriteFile(filepath.Join(store, "catalog.json"), credential, 0600); err != nil {
		t.Fatal(err)
	}
	if err := deployment.ValidateRuntimeGrantStore(home, cfg.Runtimes.EnabledBindings, nil); err != nil {
		t.Fatalf("a catalog refusal prevented local credential validation: %v", err)
	}
	assessment, err := assessUpdateConfiguration(home)
	if err != nil || len(assessment.Lines()) != 1 {
		t.Fatalf("missing repair disclosure: %+v %v", assessment, err)
	}
}
