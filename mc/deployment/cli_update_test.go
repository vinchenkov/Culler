package deployment

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCLIUpdatePreservesShellFilesAndRefusesPathChanges(t *testing.T) {
	home, userHome, directory := cliInstallFixture(t)
	previous, err := installCLIPath(home, userHome, directory, strings.Repeat("a", 40), "/bin", cliFixtureShellVerify)
	if err != nil {
		t.Fatal(err)
	}
	original := map[string]string{}
	for _, name := range []string{".zshrc", ".zprofile", ".bashrc", ".bash_profile"} {
		body, err := os.ReadFile(filepath.Join(userHome, name))
		if err != nil {
			t.Fatal(err)
		}
		original[name] = string(body)
	}
	for _, name := range []string{"mc", "culler"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("#!/bin/sh\nexit 7\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	verifyCount := 0
	next, err := updateCLIInstallation(home, userHome, directory, strings.Repeat("b", 40), "/bin", func(shell, mode, path, home, zdot, pathEnv string) error {
		verifyCount++
		return cliFixtureShellVerify(shell, mode, path, home, zdot, pathEnv)
	})
	if err != nil || verifyCount != 4 || next.Release != strings.Repeat("b", 40) || !next.Verified || next.SHA256 == previous.SHA256 {
		t.Fatalf("%+v checks=%d err=%v", next, verifyCount, err)
	}
	if !reflect.DeepEqual(next.Shells, previous.Shells) {
		t.Fatal("update changed shell ownership")
	}
	if _, err := updateCLIInstallation(home, userHome, filepath.Join(userHome, "new-bin"), strings.Repeat("c", 40), "/bin", cliFixtureShellVerify); err == nil {
		t.Fatal("update moved the command directory")
	}
	failed := errors.New("shell configuration requires repair")
	if _, err := updateCLIInstallation(home, userHome, directory, strings.Repeat("c", 40), "/bin", func(string, string, string, string, string, string) error { return failed }); !errors.Is(err, failed) {
		t.Fatal(err)
	}
	saved, err := ReadCLIInstallation(home)
	if err != nil || saved.Release != next.Release {
		t.Fatalf("failed check changed record: %+v %v", saved, err)
	}
	for name, want := range original {
		body, err := os.ReadFile(filepath.Join(userHome, name))
		if err != nil || string(body) != want {
			t.Fatalf("update changed %s: %v", name, err)
		}
	}
}
