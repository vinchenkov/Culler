package deployment

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func hostReleaseSourceFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repo")
	for _, rel := range hostReleaseFiles {
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

func TestInstallHostReleasePublishesOnlyTheClosedNativeGraph(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	source := hostReleaseSourceFixture(t)
	if err := os.WriteFile(filepath.Join(source, "resident", "src", "main.test.ts"), []byte("no\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, err := InstallHostRelease(home, source)
	if err != nil || status != "done" {
		t.Fatalf("install = %q, %v", status, err)
	}
	installed := filepath.Join(home, "release", "host")
	if err := ValidateHostRelease(installed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(installed, "resident", "src", "main.test.ts")); !os.IsNotExist(err) {
		t.Fatalf("test source entered host release: %v", err)
	}
	before, err := os.Stat(installed)
	if err != nil {
		t.Fatal(err)
	}
	status, err = InstallHostRelease(home, source)
	if err != nil || status != "ok" {
		t.Fatalf("replay = %q, %v", status, err)
	}
	after, err := os.Stat(installed)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("idempotent replay replaced host release: %v", err)
	}
}

func TestInstallHostReleaseAtomicallyReplacesChangedPayload(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	source := hostReleaseSourceFixture(t)
	if _, err := InstallHostRelease(home, source); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(home, "release", "host")
	before, err := os.Stat(installed)
	if err != nil {
		t.Fatal(err)
	}
	rel := "resident/src/main.ts"
	if err := os.WriteFile(filepath.Join(source, filepath.FromSlash(rel)), []byte("// next\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if status, err := InstallHostRelease(home, source); err != nil || status != "done" {
		t.Fatalf("upgrade = %q, %v", status, err)
	}
	after, err := os.Stat(installed)
	if err != nil || os.SameFile(before, after) {
		t.Fatalf("changed host release retained directory identity: %v", err)
	}
}

func TestReleaseInstallationPreservesOriginalSessions(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"type":"response_item","payload":{"type":"reasoning","id":"r1","encrypted_content":"AAAA-sk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","token":"culler_proxy_ccccccccccccccccccccccccccccccccccccccccccc"}}
{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"context","signature":"c2stYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFh"},{"type":"redacted_thinking","data":"c2stYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFh"}]}}
`)
	paths := []string{"sessions/run/native.jsonl", "sessions/run/subagents/agent-1.jsonl", "sessions/collie/rollout.jsonl", "environment-runs/setup/session/native.jsonl", "sessions/run/opencode/native.db"}
	for _, path := range paths {
		full := filepath.Join(home, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, original, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	host, runner := hostReleaseSourceFixture(t), runnerReleaseSourceFixture(t)
	for range 2 {
		if _, err := InstallHostRelease(home, host); err != nil {
			t.Fatal(err)
		}
		if _, err := InstallRunnerRelease(home, runner); err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			body, err := os.ReadFile(filepath.Join(home, path))
			if err != nil || !bytes.Equal(body, original) {
				t.Fatalf("The release changed original %s: %v", path, err)
			}
		}
	}
}
