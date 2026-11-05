package deployment

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	operatorconfig "mc/config"
)

func legacyProviderArtifacts(t *testing.T) (string, []string, []string) {
	t.Helper()
	home := agentProfileTestHome(t)
	removed := []string{"runs/0123456789abcdef.codex-auth.json", "runs/h-example-abcdef0123456789.codex-auth.json",
		"environment-runs/setup-operation/auth.json"}
	retained := []string{"runs/0123456789abcdef.json", "runs/operator-notes.codex-auth.json", "runs/repositories/project/auth.json",
		"sessions/h-example/native.jsonl", "environment-runs/setup-operation/session/native.jsonl",
		"environment-runs/setup-operation/run.json", "project-homes/project/.codex/auth.json"}
	for _, relative := range append(append([]string{}, removed...), retained...) {
		path := filepath.Join(home, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("synthetic-retained-content"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return home, removed, retained
}

func legacyCredentialGrant(t *testing.T, home string) string {
	t.Helper()
	source := operatorconfig.Default().Runtimes.Credentials["claude"]
	body, err := json.Marshal(ProviderCredentialRecord{Credential: "claude", Channel: "claude",
		TokenURL: source.TokenURL, ClientID: source.ClientID, RefreshToken: "synthetic-legacy-refresh"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "refresh-grants", "claude.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProviderArtifactRetirementMigratesBeforeContainerRemovalAndPreservesNativeContext(t *testing.T) {
	home, removed, retained := legacyProviderArtifacts(t)
	grant := legacyCredentialGrant(t, home)
	retired := false
	err := RetireProviderCredentialArtifacts(home, func() error {
		body, err := os.ReadFile(grant)
		if err != nil || strings.Contains(string(body), "synthetic-legacy-refresh") || !strings.Contains(string(body), "credential_ref") {
			t.Fatal("The container retirement preceded durable host credential migration.")
		}
		for _, path := range removed {
			if _, err := os.Stat(filepath.Join(home, path)); err != nil {
				t.Fatal("An artifact disappeared before container retirement.")
			}
		}
		retired = true
		return nil
	})
	if err != nil || !retired {
		t.Fatalf("retire provider artifacts: %v", err)
	}
	for _, path := range removed {
		if _, err := os.Lstat(filepath.Join(home, path)); !os.IsNotExist(err) {
			t.Errorf("The legacy artifact remains: %s", path)
		}
	}
	for _, path := range retained {
		body, err := os.ReadFile(filepath.Join(home, path))
		if err != nil || string(body) != "synthetic-retained-content" {
			t.Errorf("The unrelated file changed: %s", path)
		}
	}
	access, err := ReadProviderCredential(home, "claude")
	if err != nil || access.Record.RefreshToken != "synthetic-legacy-refresh" {
		t.Fatal("The host lost its recoverable credential.")
	}
	if err := RetireProviderCredentialArtifacts(home, func() error { return nil }); err != nil {
		t.Fatal("Repeated artifact retirement failed:", err)
	}
}

func TestProviderArtifactRetirementPreservesCopiesAfterMigrationOrContainerFailure(t *testing.T) {
	for _, failure := range []string{"migration", "container"} {
		t.Run(failure, func(t *testing.T) {
			home, artifacts, _ := legacyProviderArtifacts(t)
			grant := legacyCredentialGrant(t, home)
			backend := hostSecretBackend
			if failure == "migration" {
				hostSecretBackend = failedHostSecretStore{backend}
				defer func() { hostSecretBackend = backend }()
			}
			retired := false
			err := RetireProviderCredentialArtifacts(home, func() error {
				retired = true
				return errors.New("synthetic container failure")
			})
			if err == nil || retired != (failure == "container") {
				t.Fatalf("The retirement order changed: retired=%v error=%v", retired, err)
			}
			for _, artifact := range artifacts {
				if _, err := os.Stat(filepath.Join(home, artifact)); err != nil {
					t.Fatal("The incomplete upgrade deleted a recoverable artifact.")
				}
			}
			if failure == "migration" {
				body, _ := os.ReadFile(grant)
				if !strings.Contains(string(body), "synthetic-legacy-refresh") {
					t.Fatal("The failed migration removed its source credential.")
				}
			}
		})
	}
}

func TestProviderArtifactRetirementRefusesSymbolicLinksAndHardLinks(t *testing.T) {
	for _, kind := range []string{"runs directory", "run file", "setup directory", "setup file", "hard link"} {
		t.Run(kind, func(t *testing.T) {
			home := agentProfileTestHome(t)
			outside := t.TempDir()
			preserved := filepath.Join(outside, "auth.json")
			if err := os.WriteFile(preserved, []byte("preserved"), 0600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, "runs", "0123456789abcdef.codex-auth.json")
			source := preserved
			switch kind {
			case "runs directory":
				path, source = filepath.Join(home, "runs"), outside
			case "setup directory":
				path, source = filepath.Join(home, "environment-runs", "setup-test"), outside
			case "setup file":
				path = filepath.Join(home, "environment-runs", "setup-test", "auth.json")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			var err error
			if kind == "hard link" {
				err = os.Link(source, path)
			} else {
				err = os.Symlink(source, path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := RetireProviderCredentialArtifacts(home, func() error { return nil }); err == nil {
				t.Fatal("The unsafe artifact path was accepted.")
			}
			if body, err := os.ReadFile(preserved); err != nil || string(body) != "preserved" {
				t.Fatal("The unsafe artifact path changed an unrelated file.")
			}
		})
	}
}
