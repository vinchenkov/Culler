package main_test

import (
	"os"
	"path/filepath"
	"testing"

	"mc/deployment"
)

func onboardRunnerSource(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "runner")
	// Built from the manifest itself, exactly as onboardHostSource is: this list
	// was hand-copied once and went stale the first time the manifest grew.
	for _, rel := range deployment.RunnerReleaseFiles() {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("// "+rel+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func onboardHostSource(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repo")
	// Built from the manifest itself: a second hand-copied list would keep this
	// green while the real release shipped a resident missing its imports.
	for _, rel := range deployment.HostReleaseFiles() {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("// "+rel+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestOnboardHomePublishesTheInstallerSuppliedReleases(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	spine := filepath.Join(home, "spine.db")
	env := []string{"MC_HOME=" + home, "MC_SPINE=" + spine}
	source := onboardRunnerSource(t)
	hostSource := onboardHostSource(t)
	res := runMC(t, env, "", "__onboard", "home", "--release-source", source, "--host-release-source", hostSource)
	if res.code != 0 {
		t.Fatalf("onboard home release = code %d stderr %q", res.code, res.stderr)
	}
	sections, ok := res.json["sections"].([]any)
	if !ok || len(sections) != 1 {
		t.Fatalf("home sections = %#v", res.json["sections"])
	}
	section, ok := sections[0].(map[string]any)
	if !ok {
		t.Fatalf("home section = %#v", sections[0])
	}
	if section["status"] != "done" {
		t.Fatalf("home section = %#v", section)
	}
	if err := deployment.ValidateRunnerRelease(filepath.Join(home, "release", "runner")); err != nil {
		t.Fatal(err)
	}
	if err := deployment.ValidateHostRelease(filepath.Join(home, "release", "host")); err != nil {
		t.Fatal(err)
	}
}
