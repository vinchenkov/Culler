//go:build darwin || linux

package onboarding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSessionBeforeDeployment(t *testing.T) {
	home := filepath.Join(t.TempDir(), "new-parent", "control")
	got, err := LoadSession(home)
	if err != nil || !reflect.DeepEqual(got, Session{}) {
		t.Fatalf("absent session = %#v, %v", got, err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("a read created the deployment home: %v", err)
	}
	want := Session{
		Stage: 3, Completed: []string{"welcome", "prepare", "agents"},
		WorkspaceRoot: filepath.Join(t.TempDir(), "project"), Project: "project",
		GitAuthorName: "Project Operator", GitAuthorEmail: "operator@example.test",
		ProjectGitAuthorName: "Project Agent", ProjectGitAuthorEmail: "agent@example.test",
		Vision: "later", VisionDeferred: true, SandboxAccepted: true, ProjectConfirmed: true,
		RuntimeBindings: []string{"primary"}, RuntimeVerified: map[string]bool{"primary": false},
		RuntimeIdentities: map[string]string{"primary": strings.Repeat("a", 64)},
		RoutingFile:       filepath.Join(t.TempDir(), "roles.md"), DashboardBind: "127.0.0.1:7333",
		Tunables: map[string]int{"tick_seconds": 30}, ReleaseID: "release-1",
	}
	if err := SaveSession(home, want); err != nil {
		t.Fatal(err)
	}
	got, err = LoadSession(home)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("restored session = %#v, %v; want %#v", got, err, want)
	}
	for _, name := range []string{filepath.Dir(home), home, filepath.Join(home, "setup")} {
		info, err := os.Stat(name)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("private directory %q = %v, %v", name, info, err)
		}
	}
	info, err := os.Stat(filepath.Join(home, "setup", sessionName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private record = %v, %v", info, err)
	}
}

func TestSessionPreservesDeploymentDirectory(t *testing.T) {
	home := filepath.Join(t.TempDir(), "control")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "operator-config")
	if err := os.WriteFile(marker, []byte("keep this configuration"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveSession(home, Session{Stage: 1}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(home)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("the deployment directory changed: %v, %v", info, err)
	}
	body, err := os.ReadFile(marker)
	if err != nil || string(body) != "keep this configuration" {
		t.Fatalf("the operator configuration changed: %q, %v", body, err)
	}
}

func TestSessionReplacementKeepsCompleteRecords(t *testing.T) {
	home := filepath.Join(t.TempDir(), "control")
	if err := SaveSession(home, Session{Stage: 1}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "setup", sessionName)
	previous, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer previous.Close()
	if err := os.WriteFile(filepath.Join(home, "setup", ".session-interrupted"), []byte(`{"stage":`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSession(home)
	if err != nil || got.Stage != 1 {
		t.Fatalf("an interrupted write replaced the record: %#v, %v", got, err)
	}
	if err := SaveSession(home, Session{Stage: 2}); err != nil {
		t.Fatal(err)
	}
	var old Session
	if err := json.NewDecoder(previous).Decode(&old); err != nil || old.Stage != 1 {
		t.Fatalf("the old record changed in place: %#v, %v", old, err)
	}
	got, err = LoadSession(home)
	if err != nil || got.Stage != 2 {
		t.Fatalf("the new record is incomplete: %#v, %v", got, err)
	}
}

func TestSessionRefusesSymlinks(t *testing.T) {
	for _, target := range []string{"home", "setup", "record"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "control")
			if err := SaveSession(home, Session{Stage: 1}); err != nil {
				t.Fatal(err)
			}
			var source string
			switch target {
			case "home":
				source = home
			case "setup":
				source = filepath.Join(home, "setup")
			case "record":
				source = filepath.Join(home, "setup", sessionName)
			}
			moved := filepath.Join(root, "protected")
			if err := os.Rename(source, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, source); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadSession(home); err == nil {
				t.Fatal("the read followed a symlink")
			}
			if err := SaveSession(home, Session{Stage: 2}); err == nil {
				t.Fatal("the write followed a symlink")
			}
			var protected string
			switch target {
			case "home":
				protected = filepath.Join(moved, "setup", sessionName)
			case "setup":
				protected = filepath.Join(moved, sessionName)
			case "record":
				protected = moved
			}
			body, err := os.ReadFile(protected)
			if err != nil || !strings.Contains(string(body), `"stage": 1`) {
				t.Fatalf("the symlink target changed: %q, %v", body, err)
			}
		})
	}
}

func TestSessionRefusesUnsafeRecords(t *testing.T) {
	for _, kind := range []string{"public file", "public directory", "hard link", "pipe", "unknown field", "extra data", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "control")
			if err := SaveSession(home, Session{Stage: 1}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, "setup", sessionName)
			var err error
			switch kind {
			case "public file":
				err = os.Chmod(path, 0o644)
			case "public directory":
				err = os.Chmod(filepath.Dir(path), 0o755)
			case "hard link":
				err = os.Link(path, filepath.Join(home, "copy"))
			case "pipe":
				if err = os.Remove(path); err == nil {
					err = unix.Mkfifo(path, 0o600)
				}
			case "unknown field":
				err = os.WriteFile(path, []byte(`{"stage":1,"token":"fixture-secret"}`), 0o600)
			case "extra data":
				err = os.WriteFile(path, []byte(`{"stage":1} {"stage":2}`), 0o600)
			case "oversized":
				err = os.WriteFile(path, []byte(strings.Repeat(" ", maxSessionBytes+1)), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := LoadSession(home); err == nil {
				t.Fatal("the read accepted an unsafe record")
			} else if strings.Contains(err.Error(), "fixture-secret") {
				t.Fatal("the error exposed the rejected secret")
			}
		})
	}
}

func TestSessionRejectsSecretBearingChoices(t *testing.T) {
	for name, session := range map[string]Session{
		"vision content":        {Vision: "the fixture-secret"},
		"unsupported stage":     {Stage: 7},
		"relative path":         {WorkspaceRoot: "relative/project"},
		"terminal control":      {GitAuthorName: "Operator\x1b[31m"},
		"credential identity":   {RuntimeIdentities: map[string]string{"primary": "fixture-secret"}},
		"invalid stage marker":  {Completed: []string{"the fixture-secret"}},
		"invalid configuration": {RuntimeBindings: []string{"primary=fixture-secret"}},
	} {
		t.Run(name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "control")
			if err := SaveSession(home, session); err == nil {
				t.Fatal("the record accepted an unsafe choice")
			} else if strings.Contains(err.Error(), "fixture-secret") {
				t.Fatal("the error exposed the rejected secret")
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatalf("the invalid record created a directory: %v", err)
			}
		})
	}
}
