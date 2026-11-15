package deployment

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeSignInPreservesAnotherDeploymentSourceDirectory(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	homes := []string{t.TempDir(), t.TempDir()}
	for _, home := range homes {
		if err := os.Chmod(home, 0700); err != nil {
			t.Fatal(err)
		}
		writeSignInCatalogConfig(t, home)
	}
	writeAuth := func(_ string, _ []string, environment []string, _ io.Reader, _ io.Writer) error {
		return os.WriteFile(filepath.Join(envValue(environment, "CODEX_HOME"), "auth.json"), []byte(signInCodexFixture), 0600)
	}
	outer, err := AcquireRuntimeAuth(homes[0], []string{"chatgpt"}, nil, io.Discard, []string{"PATH=/bin"},
		func(binary string, args, environment []string, stdin io.Reader, log io.Writer) error {
			if err := requireOwnerOnlyDirectory(NativeCredentialSourceRoot(), "shared credential directory"); err != nil {
				return err
			}
			inner, err := AcquireRuntimeAuth(homes[1], []string{"chatgpt"}, nil, io.Discard, []string{"PATH=/bin"}, writeAuth)
			if err != nil {
				return err
			}
			if err := inner.Cleanup(); err != nil {
				return err
			}
			return writeAuth(binary, args, environment, stdin, log)
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := outer.Cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeAuthSourceRootStaysOutsideDeploymentMounts(t *testing.T) {
	home := t.TempDir()
	root, err := prepareRuntimeAuthSourceRoot(home)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalHome(home)
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(canonical, root)
	if err != nil || relative == "." || !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		t.Fatal("The native credential directory entered the deployment home")
	}
	t.Setenv("TMPDIR", home)
	if _, err := prepareRuntimeAuthSourceRoot(home); err == nil {
		t.Fatal("The native credential directory accepted a deployment mount")
	}
}

// The login seals the deployment credential directory as read-only. Some macOS
// releases refuse to rename a read-only directory, so the replacing process first
// restores write access, as a process of the same user can.
func moveSealedDirectory(path string) error {
	if err := os.Chmod(path, 0700); err != nil {
		return err
	}
	return os.Rename(path, path+"-moved")
}

func TestNativeSignInRemovesOnlyItsCredentialFilesAfterAncestorReplacement(t *testing.T) {
	for _, ancestor := range []string{"deployment", "shared"} {
		t.Run(ancestor, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			home := t.TempDir()
			if err := os.Chmod(home, 0700); err != nil {
				t.Fatal(err)
			}
			writeSignInCatalogConfig(t, home)
			var files []string
			var unrelated string
			_, err := AcquireRuntimeAuth(home, []string{"chatgpt"}, nil, io.Discard, []string{"PATH=/bin"},
				func(_ string, _ []string, environment []string, _ io.Reader, _ io.Writer) error {
					config := envValue(environment, "CODEX_HOME")
					flow := filepath.Dir(filepath.Dir(config))
					credentialDirectory := filepath.Dir(filepath.Dir(flow))
					if ancestor == "shared" {
						credentialDirectory = filepath.Dir(credentialDirectory)
					}
					if err := moveSealedDirectory(credentialDirectory); err != nil {
						return err
					}
					for _, path := range []string{"chatgpt/codex/auth.json", "claude/config/.credentials.json",
						"claude/home/Library/Keychains/culler.keychain-db", "claude/home/Library/Keychains/culler.keychain-db-wal",
						"claude/home/Library/Keychains/culler.keychain-db-shm"} {
						file := filepath.Join(flow, path)
						if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
							return err
						}
						if err := os.WriteFile(file, []byte("synthetic-credential"), 0600); err != nil {
							return err
						}
						files = append(files, file)
					}
					unrelated = filepath.Join(config, "keep.json")
					return os.WriteFile(unrelated, []byte("unrelated"), 0600)
				})
			if err == nil {
				t.Fatal("The replaced credential directory was accepted.")
			}
			if len(files) != 5 {
				t.Fatalf("The fixture did not replace the credential directory: %v", err)
			}
			for _, path := range files {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("A rejected native login retained a replacement credential file.")
				}
			}
			if body, err := os.ReadFile(unrelated); err != nil || string(body) != "unrelated" {
				t.Fatal("The credential cleanup changed an unrelated file.")
			}

		})
	}
}

func TestNativeSignInReplacementCleanupPreservesLinkedTargets(t *testing.T) {
	for _, link := range []string{"directory", "file", "hard"} {
		t.Run(link, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			home, outside := t.TempDir(), t.TempDir()
			if err := os.Chmod(home, 0700); err != nil {
				t.Fatal(err)
			}
			writeSignInCatalogConfig(t, home)
			target := filepath.Join(outside, "auth.json")
			if err := os.WriteFile(target, []byte("preserved"), 0600); err != nil {
				t.Fatal(err)
			}
			attempted := false
			_, err := AcquireRuntimeAuth(home, []string{"chatgpt"}, nil, io.Discard, []string{"PATH=/bin"},
				func(_ string, _ []string, environment []string, _ io.Reader, _ io.Writer) error {
					config := envValue(environment, "CODEX_HOME")
					flow := filepath.Dir(filepath.Dir(config))
					credentialDirectory := filepath.Dir(filepath.Dir(flow))
					if err := moveSealedDirectory(credentialDirectory); err != nil {
						return err
					}
					if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
						return err
					}
					attempted = true
					if link == "directory" {
						return os.Symlink(outside, config)
					}
					if err := os.Mkdir(config, 0700); err != nil {
						return err
					}
					if link == "hard" {
						return os.Link(target, filepath.Join(config, "auth.json"))
					}
					return os.Symlink(target, filepath.Join(config, "auth.json"))
				})
			if err == nil || !attempted {
				t.Fatalf("The fixture did not reject the replacement link: %v", err)
			}
			if body, err := os.ReadFile(target); err != nil || string(body) != "preserved" {
				t.Fatal("The replacement cleanup changed a linked target.")
			}
		})
	}
}
