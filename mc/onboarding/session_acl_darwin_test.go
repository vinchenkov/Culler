//go:build darwin

package onboarding

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func setupACL(t *testing.T, path string, args ...string) {
	t.Helper()
	cmd := exec.Command("chmod", append(args, path)...)
	if body, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("change the access-control list: %v, %s", err, body)
	}
}

func TestSessionRejectsNativeAccessGrants(t *testing.T) {
	for _, target := range []string{"directory", "record"} {
		t.Run(target, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "control")
			if err := SaveSession(home, Session{Stage: 1}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, "setup")
			grant := "everyone allow list,search"
			if target == "record" {
				path = filepath.Join(path, sessionName)
				grant = "everyone allow read"
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			setupACL(t, path, "+a", grant)
			after, err := os.Stat(path)
			if err != nil || !os.SameFile(before, after) || before.Mode().Perm() != after.Mode().Perm() {
				t.Fatal("the fixture changed more than its access-control list")
			}
			if _, err := LoadSession(home); err == nil {
				t.Fatal("the read accepted access for another user")
			}
			if err := SaveSession(home, Session{Stage: 2}); err == nil {
				t.Fatal("the write accepted access for another user")
			}
			setupACL(t, path, "-N")
			got, err := LoadSession(home)
			if err != nil || got.Stage != 1 {
				t.Fatalf("the rejected write changed the saved record: %#v, %v", got, err)
			}
		})
	}
}

func TestSessionRejectsInheritedAccessGrantBeforePublication(t *testing.T) {
	root := t.TempDir()
	setupACL(t, root, "+a", "everyone allow read,readattr,readextattr,readsecurity,file_inherit,directory_inherit")
	home := filepath.Join(root, "control")
	if err := SaveSession(home, Session{Stage: 1}); err == nil {
		t.Fatal("the write accepted an inherited access grant")
	}
	if _, err := os.Lstat(filepath.Join(home, "setup", sessionName)); !os.IsNotExist(err) {
		t.Fatalf("the rejected setup published a record: %v", err)
	}
}
