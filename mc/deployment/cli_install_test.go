package deployment

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cliInstallFixture(t *testing.T) (string, string, string) {
	t.Helper()
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("ZDOTDIR", userHome)
	home := filepath.Join(userHome, "deployment")
	directory := filepath.Join(userHome, "custom command's bin")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "culler"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	return home, userHome, directory
}

func TestCLIPathPreservesShellTextAndRemovesOnlyOwnedFiles(t *testing.T) {
	home, userHome, directory := cliInstallFixture(t)
	profile := filepath.Join(userHome, ".bash_profile")
	original := "# My shell choices\nexport EDITOR=vi\n"
	if err := os.WriteFile(profile, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	verify := cliFixtureShellVerify
	for i := 0; i < 2; i++ {
		r, err := installCLIPath(home, userHome, directory, "release", "/usr/bin:/bin", verify)
		if err != nil || !r.Verified {
			t.Fatalf("%+v %v", r, err)
		}
		if len(r.Shells) != 4 {
			t.Fatalf("duplicate edits: %+v", r.Shells)
		}
	}
	lines, err := CLIAvailability(home, "/usr/bin:/bin")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), CLIPathCommand(directory)) || !strings.Contains(lines[len(lines)-1], home) {
		t.Fatal(lines)
	}
	if err := os.WriteFile(filepath.Join(directory, "culler"), []byte("replacement executable"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := RemoveCLIInstallation(home); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(profile)
	if err != nil || string(body) != original {
		t.Fatalf("%s %v", body, err)
	}
	body, err = os.ReadFile(filepath.Join(directory, "culler"))
	if err != nil || string(body) != "replacement executable" {
		t.Fatal("uninstall removed another executable")
	}
}

func TestCLIPathFailureCannotClaimAvailability(t *testing.T) {
	home, userHome, directory := cliInstallFixture(t)
	_, err := installCLIPath(home, userHome, directory, "release", "/bin", func(string, string, string, string, string, string) error { return errors.New("shell path failure") })
	if err == nil {
		t.Fatal("failed shell passed")
	}
	if _, err := CLIAvailability(home, "/bin"); err == nil {
		t.Fatal("unverified command is ready")
	}
}

func TestCLIUninstallCanRemoveCommandAfterDeploymentFiles(t *testing.T) {
	home, userHome, directory := cliInstallFixture(t)
	if _, err := installCLIPath(home, userHome, directory, "release", "/bin", cliFixtureShellVerify); err != nil {
		t.Fatal(err)
	}
	remove, err := PrepareCLIUninstall(home)
	if err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(directory, "culler")
	if _, err := os.Stat(command); err != nil {
		t.Fatal("The uninstall removed its command before file removal.")
	}
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	if err := remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(command); !os.IsNotExist(err) {
		t.Fatal("The uninstall retained its command after file removal.")
	}
	for _, name := range []string{".zprofile", ".zshrc", ".bash_profile", ".bashrc"} {
		body, err := os.ReadFile(filepath.Join(userHome, name))
		if err != nil || strings.Contains(string(body), "culler") {
			t.Fatalf("The uninstall retained a shell entry: %s, %v", body, err)
		}
	}
}

func TestCLIConflictPreservesUnrelatedExecutableAndSymlink(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "culler")
	if err := os.WriteFile(path, []byte("unrelated"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := CheckCLIConflict(path); err == nil {
		t.Fatal("unrelated executable accepted")
	}
	body, _ := os.ReadFile(path)
	if string(body) != "unrelated" {
		t.Fatal("conflict changed file")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/bin/sh", path); err != nil {
		t.Fatal(err)
	}
	if err := CheckCLIConflict(path); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestCLIPathDirectoryChangeRetainsRemovalOwnership(t *testing.T) {
	home, userHome, directory := cliInstallFixture(t)
	verify := cliFixtureShellVerify
	if _, err := installCLIPath(home, userHome, directory, "release", "/bin", verify); err != nil {
		t.Fatal(err)
	}
	next := filepath.Join(userHome, "next bin")
	if err := os.Mkdir(next, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(next, "culler"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := installCLIPath(home, userHome, next, "release", "/bin", verify); err != nil {
		t.Fatal(err)
	}
	if _, err := CLIAvailability(home, "/bin"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveCLIInstallation(home); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{directory, next} {
		if _, err := os.Stat(filepath.Join(dir, "culler")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	for _, name := range []string{".zprofile", ".zshrc", ".bash_profile", ".bashrc"} {
		body, err := os.ReadFile(filepath.Join(userHome, name))
		if err != nil || len(body) != 0 {
			t.Fatalf("%s: %s %v", name, body, err)
		}
	}
}

func cliFixtureShellVerify(shell, mode, path, userHome, zdot, parentPath string) error {
	name := ".bashrc"
	if shell == "/bin/zsh" {
		name = ".zshrc"
	}
	if mode == "-lic" {
		name = ".bash_profile"
		if shell == "/bin/zsh" {
			name = ".zprofile"
		}
	}
	body, err := os.ReadFile(filepath.Join(userHome, name))
	if err != nil || !strings.Contains(string(body), CLIPathCommand(filepath.Dir(path))) {
		return errors.New("The shell requires a path entry.")
	}
	if strings.Count(string(body), CLIPathCommand(filepath.Dir(path))) != 1 {
		return errors.New("The shell has duplicate path entries.")
	}
	return nil
}

func TestCLIPathPreservesShellsThatAlreadyResolveCommand(t *testing.T) {
	home, userHome, directory := cliInstallFixture(t)
	r, err := installCLIPath(home, userHome, directory, "release", directory+":/bin", func(string, string, string, string, string, string) error { return nil })
	if err != nil || !r.Verified || len(r.Shells) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	for _, name := range []string{".zprofile", ".zshrc", ".bash_profile", ".bashrc"} {
		if _, err := os.Stat(filepath.Join(userHome, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
}
