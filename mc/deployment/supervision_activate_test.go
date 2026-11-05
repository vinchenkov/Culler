package deployment

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeLaunchd struct {
	loaded      map[string]bool
	bootstraps  int
	bootouts    int
	failAt      int
	onBootstrap func(label string)
}

func (f *fakeLaunchd) Loaded(label string) (bool, error) { return f.loaded[label], nil }

func (f *fakeLaunchd) Bootstrap(plist string) error {
	f.bootstraps++
	label := strings.TrimSuffix(filepath.Base(plist), ".plist")
	if f.failAt == f.bootstraps {
		return errors.New("injected bootstrap failure")
	}
	f.loaded[label] = true
	if f.onBootstrap != nil {
		f.onBootstrap(label)
	}
	return nil
}

func (f *fakeLaunchd) Bootout(label string) error {
	if f.loaded[label] {
		f.bootouts++
	}
	delete(f.loaded, label)
	return nil
}

func activationFixture(t *testing.T) (string, string, string, *fakeLaunchd, SupervisionActivationDeps, SupervisionSpec) {
	t.Helper()
	home, spec := supervisionFixture(t)
	_, result, err := PrepareSupervision(home, spec, func(string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatal(err)
	}
	executable := AgentsViewPathsFor(home).Executable
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	library := filepath.Join(t.TempDir(), "Library")
	if err := os.Mkdir(library, 0o700); err != nil {
		t.Fatal(err)
	}
	launchAgents := filepath.Join(library, "LaunchAgents")
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	launchd := &fakeLaunchd{loaded: map[string]bool{}}
	launchd.onBootstrap = func(label string) {
		if label != result.ResidentLabel {
			return
		}
		health := filepath.Join(home, "health")
		if err := os.Mkdir(health, 0o700); err != nil && !os.IsExist(err) {
			t.Fatal(err)
		}
		body := fmt.Sprintf("{\"version\":1,\"release_build_id\":%q,\"config_schema_version\":1,\"resident_instance_id\":\"test-resident\",\"sequence\":1,\"completed_at\":%q}\n",
			spec.ReleaseBuildID, now.Add(time.Second).Format(time.RFC3339Nano))
		if err := os.WriteFile(filepath.Join(health, "resident-tick.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	deps := SupervisionActivationDeps{Launchd: launchd, Now: func() time.Time { return now }, Sleep: func(time.Duration) {}}
	return home, launchAgents, spec.ReleaseBuildID, launchd, deps, spec
}

func TestActivateSupervisionLoadsThreeUnitsAndRequiresNewTick(t *testing.T) {
	home, launchAgents, release, launchd, deps, _ := activationFixture(t)
	status, result, err := ActivateSupervision(home, launchAgents, release, deps)
	if err != nil || status != "done" {
		t.Fatalf("activate status=%q result=%+v err=%v", status, result, err)
	}
	for _, label := range result.Labels() {
		if !launchd.loaded[label] {
			t.Fatalf("%s not loaded", label)
		}
		path := filepath.Join(launchAgents, label+".plist")
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o644 {
			t.Fatalf("installed plist %s info=%v err=%v", path, info, err)
		}
	}
}

func TestActivateAndInspectSupervisionRetainTCCGate(t *testing.T) {
	home, launchAgents, release, launchd, deps, _ := activationFixture(t)
	operatorHome := t.TempDir()
	t.Setenv("HOME", operatorHome)
	protected := filepath.Join(operatorHome, "Documents")
	if err := os.Mkdir(protected, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(protected, "mc-home")
	if err := os.Rename(home, target); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ActivateSupervision(target, launchAgents, release, deps); err == nil ||
		!strings.Contains(err.Error(), "TCC-protected") {
		t.Fatalf("protected activation error = %v", err)
	}
	if _, err := InspectActiveSupervision(
		target, launchAgents, release, launchd, deps.Now(),
	); err == nil || !strings.Contains(err.Error(), "TCC-protected") {
		t.Fatalf("protected inspection error = %v", err)
	}
	if len(launchd.loaded) != 0 {
		t.Fatalf("TCC-refused activation touched launchd: %v", launchd.loaded)
	}
}

func TestActivateSupervisionRollsBackEveryPartialFirstActivation(t *testing.T) {
	for failAt := 2; failAt <= 3; failAt++ {
		t.Run(fmt.Sprintf("bootstrap %d", failAt), func(t *testing.T) {
			home, launchAgents, release, launchd, deps, _ := activationFixture(t)
			launchd.failAt = failAt
			if _, _, err := ActivateSupervision(home, launchAgents, release, deps); err == nil || !strings.Contains(err.Error(), "rolled back") {
				t.Fatalf("err=%v", err)
			}
			if len(launchd.loaded) != 0 {
				t.Fatalf("jobs survived rollback: %v", launchd.loaded)
			}
			entries, err := os.ReadDir(launchAgents)
			if err != nil || len(entries) != 0 {
				t.Fatalf("installed plists survived rollback: %v %v", entries, err)
			}
		})
	}

	t.Run("missing tick", func(t *testing.T) {
		home, launchAgents, release, launchd, deps, _ := activationFixture(t)
		launchd.onBootstrap = nil
		if _, _, err := ActivateSupervision(home, launchAgents, release, deps); err == nil || !strings.Contains(err.Error(), "no successful release-bound tick") {
			t.Fatalf("err=%v", err)
		}
		if len(launchd.loaded) != 0 {
			t.Fatalf("jobs survived receipt timeout: %v", launchd.loaded)
		}
		entries, err := os.ReadDir(launchAgents)
		if err != nil || len(entries) != 0 {
			t.Fatalf("plists survived receipt timeout: %v %v", entries, err)
		}
	})
}

func TestActivateSupervisionRefusesPartialPreexistingState(t *testing.T) {
	for _, loaded := range [][]string{{"resident"}, {"agentsview"}, {"resident", "dashboard"}} {
		t.Run(strings.Join(loaded, "+"), func(t *testing.T) {
			home, launchAgents, release, launchd, deps, _ := activationFixture(t)
			names, _ := RuntimeNames(home)
			for _, unit := range loaded {
				launchd.loaded[UnitLabel(names, unit)] = true
			}
			if _, _, err := ActivateSupervision(home, launchAgents, release, deps); err == nil || !strings.Contains(err.Error(), "partially loaded") {
				t.Fatalf("err=%v", err)
			}
			if _, err := os.Lstat(launchAgents); !os.IsNotExist(err) {
				t.Fatalf("partial-state refusal wrote LaunchAgents: %v", err)
			}
		})
	}
}

func TestInspectActiveSupervisionRequiresEveryExactJobAndFreshReceipt(t *testing.T) {
	home, launchAgents, release, launchd, deps, _ := activationFixture(t)
	_, result, err := ActivateSupervision(home, launchAgents, release, deps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InspectActiveSupervision(home, launchAgents, release, launchd, deps.Now().Add(time.Minute)); err != nil {
		t.Fatalf("inspect active: %v", err)
	}
	for _, label := range []string{result.DashboardLabel, result.AgentsViewLabel} {
		delete(launchd.loaded, label)
		if _, err := InspectActiveSupervision(home, launchAgents, release, launchd, deps.Now().Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "not loaded") {
			t.Fatalf("missing %s accepted: %v", label, err)
		}
		launchd.loaded[label] = true
	}
	if _, err := InspectActiveSupervision(home, launchAgents, release, launchd, deps.Now().Add(4*time.Minute)); err == nil || !strings.Contains(err.Error(), "last two minutes") {
		t.Fatalf("stale receipt accepted: %v", err)
	}
}

// Changing config.toml while the units are loaded is a real operator event, and
// until now it only produced a refusal: PrepareSupervision deliberately forbids
// offline replacement of a loaded bundle, and nothing offered the operator-
// present path that would apply the change. The only correct order is boot out,
// rewrite from the new policy, then activate through the existing rollback and
// release-bound tick proof.
func TestReconfigureSupervisionAppliesAChangedPolicyToLoadedUnits(t *testing.T) {
	home, launchAgents, release, launchd, deps, loadedSpec := activationFixture(t)
	if _, _, err := ActivateSupervision(home, launchAgents, release, deps); err != nil {
		t.Fatalf("first activation: %v", err)
	}
	// The operator retunes the metronome. The loaded bundle still holds 60.
	retuned := loadedSpec
	retuned.TickIntervalS = 45
	matches, err := SupervisionPolicyMatches(home, retuned.SupervisionPolicy)
	if err != nil || matches {
		t.Fatalf("loaded bundle already matched the retuned policy: %v %v", matches, err)
	}

	status, reconfigured, err := ReconfigureSupervision(home, launchAgents, release, retuned, deps)
	if err != nil || status != "done" {
		t.Fatalf("reconfigure status=%q err=%v", status, err)
	}
	for _, label := range reconfigured.Labels() {
		if !launchd.loaded[label] {
			t.Fatalf("%s is not loaded after reconfiguration", label)
		}
	}
	matches, err = SupervisionPolicyMatches(home, retuned.SupervisionPolicy)
	if err != nil || !matches {
		t.Fatalf("reconfigured bundle does not carry the new policy: %v %v", matches, err)
	}
	// Every unit is booted out before the rewrite: replacing a loaded bundle
	// offline is exactly what PrepareSupervision refuses.
	if launchd.bootouts < 3 {
		t.Fatalf("reconfiguration rewrote the bundle without booting every unit out (%d)", launchd.bootouts)
	}
}

// A bind change rewrites the AgentsView plist. The installed plist of the
// stopped unit then differs from the prepared plist, and activation must
// replace it.
func TestReconfigureSupervisionReplacesTheInstalledPlistOfAChangedBind(t *testing.T) {
	home, launchAgents, release, launchd, deps, loadedSpec := activationFixture(t)
	if _, _, err := ActivateSupervision(home, launchAgents, release, deps); err != nil {
		t.Fatalf("first activation: %v", err)
	}
	moved := loadedSpec
	moved.AgentsViewBind = "127.0.0.1:7344"
	status, result, err := ReconfigureSupervision(home, launchAgents, release, moved, deps)
	if err != nil || status != "done" {
		t.Fatalf("reconfigure status=%q err=%v loaded=%v", status, err, launchd.loaded)
	}
	for _, label := range result.Labels() {
		if !launchd.loaded[label] {
			t.Fatalf("%s is not loaded after the bind change", label)
		}
	}
	installed, err := os.ReadFile(filepath.Join(launchAgents, result.AgentsViewLabel+".plist"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(installed), "<string>7344</string>") {
		t.Fatalf("the installed AgentsView plist keeps the old bind:\n%s", installed)
	}
	if _, err := InspectActiveSupervision(home, launchAgents, release, launchd, deps.Now()); err != nil {
		t.Fatalf("inspect after the bind change: %v", err)
	}
}

func TestActivateSupervisionRollbackRestoresAReplacedPlist(t *testing.T) {
	home, launchAgents, release, launchd, deps, _ := activationFixture(t)
	names, err := RuntimeNames(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(launchAgents, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(launchAgents, UnitLabel(names, "agentsview")+".plist")
	if err := os.WriteFile(stale, []byte("stale plist\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	launchd.failAt = 3
	if _, _, err := ActivateSupervision(home, launchAgents, release, deps); err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("err=%v", err)
	}
	body, err := os.ReadFile(stale)
	info, statErr := os.Lstat(stale)
	if err != nil || statErr != nil || string(body) != "stale plist\n" || info.Mode().Perm() != 0o600 {
		t.Fatalf("the rollback did not restore the replaced plist: %q %v %v %v", body, info, err, statErr)
	}
	if entries, err := os.ReadDir(launchAgents); err != nil || len(entries) != 1 {
		t.Fatalf("the rollback left other plists: %v %v", entries, err)
	}
}

func TestActivateSupervisionRefusesAnUnsafeInstalledPlist(t *testing.T) {
	for name, place := range map[string]func(t *testing.T, path string){
		"symbolic link": func(t *testing.T, path string) {
			target := filepath.Join(t.TempDir(), "elsewhere.plist")
			if err := os.WriteFile(target, []byte("elsewhere\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		},
		"second link": func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("linked\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(path, filepath.Join(filepath.Dir(path), "linked.plist")); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			home, launchAgents, release, launchd, deps, _ := activationFixture(t)
			names, err := RuntimeNames(home)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(launchAgents, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(launchAgents, UnitLabel(names, "agentsview")+".plist")
			place(t, path)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := ActivateSupervision(home, launchAgents, release, deps); err == nil ||
				!strings.Contains(err.Error(), "is not an owner-controlled regular file") {
				t.Fatalf("err=%v", err)
			}
			if after, err := os.ReadFile(path); err != nil || string(after) != string(before) {
				t.Fatalf("the refusal changed the installed plist: %q %v", after, err)
			}
			if len(launchd.loaded) != 0 {
				t.Fatalf("the refusal loaded units: %v", launchd.loaded)
			}
		})
	}
}
