package deployment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runnerReleaseSourceFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "runner")
	for _, rel := range runnerReleaseDirs[1:] {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range runnerReleaseFiles {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte("// "+rel+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestInstallRunnerReleasePublishesOnlyTheClosedRuntimeTree(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	source := runnerReleaseSourceFixture(t)
	if err := os.WriteFile(filepath.Join(source, "agent-runner", "adapters", "not-runtime.test.ts"), []byte("no\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, err := InstallRunnerRelease(home, source)
	if err != nil || status != "done" {
		t.Fatalf("install = %q, %v", status, err)
	}
	installed := filepath.Join(home, "release", "runner")
	if err := ValidateRunnerRelease(installed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(installed, "agent-runner", "adapters", "not-runtime.test.ts")); !os.IsNotExist(err) {
		t.Fatalf("test source entered release: %v", err)
	}
	for _, rel := range runnerReleaseFiles {
		info, err := os.Stat(filepath.Join(installed, filepath.FromSlash(rel)))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("installed %s mode = %v, %v", rel, info.Mode(), err)
		}
	}
	before, err := os.Stat(installed)
	if err != nil {
		t.Fatal(err)
	}
	status, err = InstallRunnerRelease(home, source)
	if err != nil || status != "ok" {
		t.Fatalf("replay = %q, %v", status, err)
	}
	after, err := os.Stat(installed)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("idempotent replay replaced release directory: %v", err)
	}
}

func TestInstallRunnerReleaseAtomicallyRepairsChangedOwnedTree(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	source := runnerReleaseSourceFixture(t)
	if _, err := InstallRunnerRelease(home, source); err != nil {
		t.Fatal(err)
	}
	rel := runnerReleaseFiles[0]
	if err := os.WriteFile(filepath.Join(source, filepath.FromSlash(rel)), []byte("// upgraded\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(home, "release", "runner")
	before, err := os.Stat(installed)
	if err != nil {
		t.Fatal(err)
	}
	status, err := InstallRunnerRelease(home, source)
	if err != nil || status != "done" {
		t.Fatalf("upgrade = %q, %v", status, err)
	}
	after, err := os.Stat(installed)
	if err != nil || os.SameFile(before, after) {
		t.Fatalf("changed release did not replace directory identity: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(installed, filepath.FromSlash(rel)))
	if err != nil || string(body) != "// upgraded\n" {
		t.Fatalf("upgraded body = %q, %v", body, err)
	}
}

func TestInstallRunnerReleaseRejectsSymlinkedSourceBeforePublication(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	source := runnerReleaseSourceFixture(t)
	rel := runnerReleaseFiles[0]
	target := filepath.Join(source, filepath.FromSlash(rel))
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(source, "agent-runner", "main.ts"), target); err != nil {
		t.Fatal(err)
	}
	_, err := InstallRunnerRelease(home, source)
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "release")); !os.IsNotExist(statErr) {
		t.Fatalf("refused source wrote release root: %v", statErr)
	}
}

func TestValidateRunnerReleaseRejectsUnexpectedEntry(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallRunnerRelease(home, runnerReleaseSourceFixture(t)); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(home, "release", "runner")
	if err := os.WriteFile(filepath.Join(installed, "extra.ts"), []byte("extra\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRunnerRelease(installed); err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("unexpected-entry validation = %v", err)
	}
}
