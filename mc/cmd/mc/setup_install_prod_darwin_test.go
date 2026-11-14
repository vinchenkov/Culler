//go:build !test_fake_routing

package main

import (
	"mc/deployment"
	"mc/verbs"
	"os"
	"path/filepath"
	"testing"
)

func TestSetupExecutablePublicationIsDeferredAndAtomic(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MC_BIN_DIR", dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ZDOTDIR", os.Getenv("HOME"))
	t.Setenv("MC_HOME", t.TempDir())
	path := filepath.Join(dir, "mc")
	if err := os.WriteFile(path, []byte("previous executable"), 0755); err != nil {
		t.Fatal(err)
	}
	installed, err := installSetupExecutable()
	if err != nil || installed != path {
		t.Fatalf("install = %q: %v", installed, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0755 || info.Size() < 1024 {
		t.Fatalf("installed executable = %v: %v", info, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("publication left temporary files: %v", err)
	}
}

func TestOnboardCLIRequiresPrivateSourceDeployment(t *testing.T) {
	for _, test := range []struct {
		name    string
		private bool
		source  string
		install bool
	}{
		{"agent home", false, "/source", false},
		{"home without a release", true, "", false},
		{"private source release", true, "/source", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			userHome, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(userHome, "deployment")
			directory := filepath.Join(userHome, "bin")
			if err := os.Mkdir(home, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", userHome)
			t.Setenv("ZDOTDIR", userHome)
			t.Setenv("MC_HOME", home)
			t.Setenv("MC_BIN_DIR", directory)
			t.Setenv("CULLER_PARENT_PATH", "/usr/bin:/bin")
			changed, err := installOnboardCLI(test.private, verbs.OnboardArgs{HostReleaseSource: test.source})
			if err != nil || changed != test.install {
				t.Fatalf("changed=%t error=%v", changed, err)
			}
			if !test.install {
				if _, err := os.Stat(directory); !os.IsNotExist(err) {
					t.Fatal("The unrelated home operation changed the executable directory.")
				}
				return
			}
			record, err := deployment.ReadCLIInstallation(home)
			if err != nil || !record.Verified || record.Path != filepath.Join(directory, "culler") {
				t.Fatalf("record=%+v error=%v", record, err)
			}
			for _, name := range []string{"mc", "culler"} {
				digest, err := deployment.CLIFileDigest(filepath.Join(directory, name))
				if err != nil || digest != record.SHA256 {
					t.Fatalf("%s differs from the verified command: %v", name, err)
				}
			}
		})
	}
}
