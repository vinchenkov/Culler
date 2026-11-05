//go:build !test_fake_routing

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"mc/deployment"
)

type uninstallFixture struct {
	root, user, project, binary string
	homes, volumes              []string
}

func uninstallWrite(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func newUninstallFixture(t *testing.T) uninstallFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := uninstallFixture{root: root, user: filepath.Join(root, "operator"), project: filepath.Join(root, "Project with spaces")}
	f.binary = filepath.Join(f.user, ".local/bin/mc")
	uninstallWrite(t, f.binary, "installed program", 0o700)
	uninstallWrite(t, filepath.Join(f.project, "source.txt"), "operator work", 0o600)
	uninstallWrite(t, filepath.Join(f.project, ".mc-artifacts", "data"), "task data", 0o600)
	var helpers, labels []string
	for _, name := range []string{".culler", ".mission-control"} {
		home := filepath.Join(f.user, name)
		f.homes = append(f.homes, home)
		for _, file := range []string{"config.toml", "deployment.uuid", "refresh-grants/fixture.json", "backups/history.db", "state/projects/fixture/home/.local/bin/tool", "state/projects/fixture/home/.config/tool.conf", "release/program", "bin/mc", "bun/1.3.12/bin/bun"} {
			uninstallWrite(t, filepath.Join(home, file), "retained data", 0o600)
		}
		names, _ := deployment.RuntimeNames(home)
		f.volumes = append(f.volumes, names.Volume)
		helpers = append(helpers, names.Helper)
		roots, _ := json.Marshal(map[string]any{"workspace_roots": []string{f.project}})
		uninstallWrite(t, filepath.Join(root, "roots-"+names.Helper), string(roots), 0o600)
		label := "com." + strings.TrimPrefix(name, ".") + "." + names.Suffix + ".resident"
		labels = append(labels, label)
		plist := fmt.Sprintf(`<?xml version="1.0"?><plist version="1.0"><dict><key>EnvironmentVariables</key><dict><key>MC_HOME</key><string>%s</string></dict></dict></plist>`, home)
		uninstallWrite(t, filepath.Join(f.user, "Library/LaunchAgents", label+".plist"), plist, 0o600)
	}
	uninstallWrite(t, filepath.Join(root, "containers"), strings.Join(helpers, "\n")+"\n", 0o600)
	uninstallWrite(t, filepath.Join(root, "volumes"), strings.Join(f.volumes, "\n")+"\n", 0o600)
	uninstallWrite(t, filepath.Join(root, "labels"), strings.Join(labels, "\n")+"\n", 0o600)
	docker := `#!/bin/sh
set -eu
cd "$UNINSTALL_FIXTURE"
printf '%s\n' "$*" >>calls
case "$1 $2" in
 'ps -a') while read -r n; do [ -f "removed-$n" ] || echo "$n"; done <containers ;;
 'volume ls') while read -r n; do [ -f "removed-$n" ] || echo "$n"; done <volumes ;;
 'image ls') if [ "$3" = --filter ]; then [ ! -f project-images ] || cat project-images; elif [ "$5" = mc-prod ] && [ ! -f removed-image ]; then echo mc-prod:latest; fi ;;
 'image inspect') cat project-image-labels ;;
 'image rm') [ ! -f fail-image ] || exit 1; touch removed-image ;;
 'container rm') [ ! -f fail-container ] || exit 1; touch "removed-$4" ;;
 'volume rm') [ ! -f fail-volume ] || exit 1; touch "removed-$3" ;;
 exec*) [ ! -f "removed-$2" ] || exit 1; cat "roots-$2" ;;
 *) exit 90 ;;
esac
`
	uninstallWrite(t, filepath.Join(root, "tools/docker"), docker, 0o700)
	launchctl := `#!/bin/sh
set -eu
cd "$UNINSTALL_FIXTURE"
case "$1" in
 list) while read -r n; do [ -f "stopped-$n" ] || printf '100\t0\t%s\n' "$n"; done <labels ;;
 print) label=${2##*/}; if [ -f "stopped-$label" ]; then echo 'Could not find service' >&2; exit 113; fi ;;
 bootout) [ ! -f fail-stop ] || exit 1; label=${2##*/}; touch "stopped-$label" ;;
 *) exit 90 ;;
esac
`
	uninstallWrite(t, filepath.Join(root, "tools/launchctl"), launchctl, 0o700)
	t.Setenv("HOME", f.user)
	t.Setenv("MC_HOME", "")
	t.Setenv("UNINSTALL_FIXTURE", root)
	t.Setenv("PATH", filepath.Join(root, "tools")+string(os.PathListSeparator)+os.Getenv("PATH"))
	return f
}

func runUninstallFixture(t *testing.T, args ...string) (int, uninstallReport, string) {
	t.Helper()
	var out, diagnostics bytes.Buffer
	code := runUninstall(append([]string{"__uninstall"}, args...), &out, &diagnostics, []string{t.TempDir()})
	// Optional actual terminal output for the required setup review.
	if os.Getenv("CULLER_TERMINAL_REVIEW") == "1" {
		fmt.Fprint(os.Stderr, diagnostics.String())
	}
	var report uninstallReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("report: %v: %s; stderr: %s", err, out.String(), diagnostics.String())
	}
	return code, report, diagnostics.String()
}

func uninstallExists(t *testing.T, path string, want bool) {
	t.Helper()
	_, err := os.Lstat(path)
	if (err == nil) != want {
		t.Fatalf("%s exists=%v, want %v (err=%v)", path, err == nil, want, err)
	}
}

func TestUninstallProjectEnvironmentArtifacts(t *testing.T) {
	for _, fullWipe := range []bool{false, true} {
		t.Run(fmt.Sprintf("full_wipe_%t", fullWipe), func(t *testing.T) {
			f := newUninstallFixture(t)
			digest := sha256.Sum256([]byte(f.homes[0]))
			namespace := fmt.Sprintf("%x", digest[:8])
			cache := "mc-cache-" + namespace + "-0123456789abcdef01234567"
			foreignCache := "mc-cache-0000000000000000-0123456789abcdef01234567"
			image := "culler-project-" + namespace + "-project:operation-1"
			foreignImage := "culler-project-0000000000000000-project:operation-1"
			uninstallWrite(t, filepath.Join(f.root, "volumes"), strings.Join(append(f.volumes, cache, foreignCache), "\n")+"\n", 0600)
			uninstallWrite(t, filepath.Join(f.root, "project-images"), image+"\n"+foreignImage+"\nculler-project-base:shared\n", 0600)
			uninstallWrite(t, filepath.Join(f.root, "project-image-labels"), `{"com.culler.environment.operation":"operation-1"}`, 0600)
			flag := "--keep-data"
			if fullWipe {
				flag = "--full-wipe"
			}
			code, report, output := runUninstallFixture(t, "--confirm", flag)
			if code != 0 || !report.Uninstalled {
				t.Fatalf("uninstall: code %d, report %+v, output %s", code, report, output)
			}
			calls, err := os.ReadFile(filepath.Join(f.root, "calls"))
			if err != nil {
				t.Fatal(err)
			}
			for _, command := range []string{"volume rm " + cache, "image rm " + image} {
				if strings.Contains(string(calls), command) != fullWipe {
					t.Fatalf("unexpected removal for %s: %s", command, calls)
				}
			}
			if strings.Contains(string(calls), "volume rm "+foreignCache) || strings.Contains(string(calls), "image rm "+foreignImage) || strings.Contains(string(calls), "image rm culler-project-base:") {
				t.Fatal("The uninstall selected artifacts outside its deployment scope.")
			}
			if fullWipe && strings.Index(string(calls), "volume rm "+cache) > strings.Index(string(calls), "image rm "+image) {
				t.Fatal("The uninstall removed the image before its unused cache.")
			}
			uninstallExists(t, filepath.Join(f.project, "source.txt"), true)
		})
	}
}

func TestUninstallModesPreserveProjects(t *testing.T) {
	for _, mode := range []string{"", "--keep-data", "--full-wipe"} {
		for _, action := range []string{"--dry-run", "--confirm"} {
			t.Run(mode+action, func(t *testing.T) {
				f := newUninstallFixture(t)
				sharedTools := []string{
					"Applications/Docker.app/Contents/MacOS/Docker",
					".local/bin/docker", ".local/bin/git", ".local/bin/mise",
					".local/share/mise/installs/go/fixture/bin/go",
					".local/share/mise/installs/bun/fixture/bin/bun",
				}
				for _, path := range sharedTools {
					uninstallWrite(t, filepath.Join(f.user, path), "shared tool", 0o700)
				}
				privateLibrary := filepath.Join(f.homes[0], "release", "host", "node_modules", "library.js")
				uninstallWrite(t, privateLibrary, "private library", 0o600)
				code, report, diag := runUninstallFixture(t, append([]string{action}, strings.Fields(mode)...)...)
				if code != 0 {
					t.Fatalf("exit %d: %s", code, diag)
				}
				dry := action == "--dry-run"
				keep := mode == "--keep-data" || dry
				if report.Uninstalled == dry {
					t.Fatalf("wrong completion state %+v", report)
				}
				var hostBuns []string
				for _, home := range f.homes {
					hostBuns = append(hostBuns, filepath.Join(home, "bun"))
				}
				if !slices.Equal(report.HostBuns, hostBuns) {
					t.Fatalf("host Buns = %v, want %v", report.HostBuns, hostBuns)
				}
				for _, home := range f.homes {
					uninstallExists(t, filepath.Join(home, "config.toml"), keep)
					uninstallExists(t, filepath.Join(home, "refresh-grants/fixture.json"), keep)
					uninstallExists(t, filepath.Join(home, "backups/history.db"), keep)
					uninstallExists(t, filepath.Join(home, "state/projects/fixture/home/.local/bin/tool"), keep)
					uninstallExists(t, filepath.Join(home, "state/projects/fixture/home/.config/tool.conf"), keep)
					uninstallExists(t, filepath.Join(home, "release/program"), dry)
					uninstallExists(t, filepath.Join(home, "bun/1.3.12/bin/bun"), dry)
					if keep {
						body, err := os.ReadFile(filepath.Join(home, "refresh-grants/fixture.json"))
						if err != nil || string(body) != "retained data" {
							t.Fatalf("credential changed: %q %v", body, err)
						}
					}
				}
				for _, volume := range f.volumes {
					uninstallExists(t, filepath.Join(f.root, "removed-"+volume), !keep)
				}
				uninstallExists(t, f.binary, dry)
				uninstallExists(t, privateLibrary, dry)
				for _, path := range sharedTools {
					body, err := os.ReadFile(filepath.Join(f.user, path))
					if err != nil || string(body) != "shared tool" {
						t.Fatalf("shared tool %s changed: %q (%v)", path, body, err)
					}
				}
				uninstallExists(t, filepath.Join(f.project, "source.txt"), true)
				uninstallExists(t, filepath.Join(f.project, ".mc-artifacts/data"), keep)
			})
		}
	}
}

func TestUninstallKeepDataThenFullWipeWithoutHelpers(t *testing.T) {
	f := newUninstallFixture(t)
	if code, _, diag := runUninstallFixture(t, "--confirm", "--keep-data"); code != 0 {
		t.Fatal(diag)
	}
	if code, report, diag := runUninstallFixture(t, "--confirm", "--full-wipe"); code != 0 || !report.Uninstalled {
		t.Fatal(diag)
	}
	uninstallExists(t, filepath.Join(f.project, ".mc-artifacts"), false)
	uninstallExists(t, filepath.Join(f.project, "source.txt"), true)
	// Repeated full wipes are harmless, including after the host program is gone.
	if code, _, diag := runUninstallFixture(t, "--confirm", "--full-wipe"); code != 0 {
		t.Fatal(diag)
	}
}

func TestUninstallFailureKeepsDataAndRetryProgram(t *testing.T) {
	failures := []string{"container", "image", "volume"}
	if runtime.GOOS == "darwin" {
		failures = append(failures, "stop")
	}
	for _, failure := range failures {
		t.Run(failure, func(t *testing.T) {
			f := newUninstallFixture(t)
			uninstallWrite(t, filepath.Join(f.root, "fail-"+failure), "", 0o600)
			code, report, diag := runUninstallFixture(t, "--confirm", "--full-wipe")
			if code == 0 || report.Uninstalled || !strings.Contains(diag, "Uninstall incomplete") {
				t.Fatalf("false success %d: %s", code, diag)
			}
			uninstallExists(t, f.binary, true)
			for _, home := range f.homes {
				uninstallExists(t, filepath.Join(home, "config.toml"), true)
			}
			for _, volume := range f.volumes {
				uninstallExists(t, filepath.Join(f.root, "removed-"+volume), false)
			}
			if failure == "stop" {
				calls, _ := os.ReadFile(filepath.Join(f.root, "calls"))
				if strings.Contains(string(calls), " rm ") {
					t.Fatalf("removal followed failed stop: %s", calls)
				}
			}
			if err := os.Remove(filepath.Join(f.root, "fail-"+failure)); err != nil {
				t.Fatal(err)
			}
			if code, _, diag := runUninstallFixture(t, "--confirm", "--full-wipe"); code != 0 {
				t.Fatal(diag)
			}
		})
	}
}

func TestUninstallRejectsUnsafeHomesBeforeRemoval(t *testing.T) {
	for _, kind := range []string{"root", "user", "project", "unrecognized", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			f := newUninstallFixture(t)
			path := f.root
			switch kind {
			case "root":
				path = "/"
			case "user":
				path = f.user
			case "project":
				path = f.project
				uninstallWrite(t, filepath.Join(path, ".git/HEAD"), "ref: refs/heads/main", 0o600)
				uninstallWrite(t, filepath.Join(path, "config.toml"), "", 0o600)
			case "symlink":
				path = filepath.Join(f.root, "alias")
				if err := os.Symlink(f.homes[0], path); err != nil {
					t.Fatal(err)
				}
			}
			code, report, diag := runUninstallFixture(t, "--confirm", "--full-wipe", "--home", path)
			if code == 0 || report.Uninstalled {
				t.Fatalf("accepted %s: %s", path, diag)
			}
			uninstallExists(t, f.binary, true)
			calls, _ := os.ReadFile(filepath.Join(f.root, "calls"))
			if strings.Contains(string(calls), " rm ") {
				t.Fatalf("unsafe home still removed resources: %s", calls)
			}
		})
	}
}

func TestUninstallDiagnosticsRequiresOwnedRecognizableRecord(t *testing.T) {
	root := t.TempDir()
	body := `{"operation":"home","observed_at":"2026-09-21T00:00:00Z","expected_runtime_identity":{"protocol_version":1,"spine_schema_id":"fixture"}}`
	uninstallWrite(t, filepath.Join(root, "mc-setup-123/diagnostic.json"), body, 0o600)
	uninstallWrite(t, filepath.Join(root, "mc-setup-456/diagnostic.json"), `{"operator":"notes"}`, 0o600)
	uninstallWrite(t, filepath.Join(root, "mc-setup-789/diagnostic.json"), body, 0o600)
	uninstallWrite(t, filepath.Join(root, "mc-setup-789/extra"), "keep", 0o600)
	if err := os.Symlink(filepath.Join(root, "mc-setup-123"), filepath.Join(root, "mc-setup-321")); err != nil {
		t.Fatal(err)
	}
	paths := uninstallDiagnostics([]string{root, root})
	want, _ := filepath.EvalSymlinks(filepath.Join(root, "mc-setup-123"))
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("diagnostics selected: %v", paths)
	}
}

func TestUninstallPreservesProjectInsideSmokeRoot(t *testing.T) {
	f := newUninstallFixture(t)
	smoke, _ := deployment.SmokeWorkspaceBase(f.homes[0])
	project := filepath.Join(smoke, "my-project")
	uninstallWrite(t, filepath.Join(project, "source.txt"), "keep", 0o600)
	code, report, diag := runUninstallFixture(t, "--confirm", "--full-wipe", "--workspace", project)
	if code != 0 || !report.Uninstalled {
		t.Fatal(diag)
	}
	uninstallExists(t, filepath.Join(project, "source.txt"), true)
}

func TestUninstallRetainedArtifactPreventsCompletion(t *testing.T) {
	f := newUninstallFixture(t)
	artifact := filepath.Join(f.project, ".mc-artifacts")
	if err := os.RemoveAll(artifact); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.root, artifact); err != nil {
		t.Fatal(err)
	}
	code, report, diag := runUninstallFixture(t, "--confirm", "--full-wipe")
	if code == 0 || report.Uninstalled || !strings.Contains(diag, "not a plain directory") {
		t.Fatalf("false completion: %s", diag)
	}
	uninstallExists(t, f.binary, true)
	uninstallExists(t, artifact, true)
}

func TestUninstallRemovesSmokeRootWithOnlyAbsentProjects(t *testing.T) {
	f := newUninstallFixture(t)
	smoke, _ := deployment.SmokeWorkspaceBase(f.homes[0])
	uninstallWrite(t, filepath.Join(smoke, "old-marker"), "scratch", 0o600)
	code, _, diag := runUninstallFixture(t, "--confirm", "--full-wipe", "--workspace", filepath.Join(smoke, "deleted-project"))
	if code != 0 {
		t.Fatal(diag)
	}
	uninstallExists(t, smoke, false)
}

// agentsViewUninstallFixture adds an AgentsView unit, its data, and wiki
// exports to the first deployment. Its property list names MC_HOME only
// through the AgentsView data folder.
func agentsViewUninstallFixture(t *testing.T, f uninstallFixture, home string) string {
	t.Helper()
	paths := deployment.AgentsViewPathsFor(home)
	uninstallWrite(t, paths.Executable, "agentsview program", 0o755)
	uninstallWrite(t, filepath.Join(paths.Data, "sessions.db"), "trace archive", 0o600)
	uninstallWrite(t, filepath.Join(paths.ClaudeTraceCopies, "session.jsonl"), "trace copy", 0o600)
	uninstallWrite(t, filepath.Join(deployment.WikiExportRoot(home), "alpha", "export.json"), "wiki export", 0o600)
	names, _ := deployment.RuntimeNames(home)
	label := deployment.UnitLabel(names, "agentsview")
	plist := fmt.Sprintf(`<?xml version="1.0"?><plist version="1.0"><dict><key>EnvironmentVariables</key><dict><key>AGENTSVIEW_DATA_DIR</key><string>%s</string></dict></dict></plist>`, paths.Data)
	uninstallWrite(t, filepath.Join(f.user, "Library/LaunchAgents", label+".plist"), plist, 0o600)
	labels, err := os.ReadFile(filepath.Join(f.root, "labels"))
	if err != nil {
		t.Fatal(err)
	}
	uninstallWrite(t, filepath.Join(f.root, "labels"), string(labels)+label+"\n", 0o600)
	return label
}

func TestUninstallStopsAgentsViewAndKeepsItsDataOnlyWithKeepData(t *testing.T) {
	f := newUninstallFixture(t)
	home := f.homes[0]
	label := agentsViewUninstallFixture(t, f, home)
	paths := deployment.AgentsViewPathsFor(home)
	plist := filepath.Join(f.user, "Library/LaunchAgents", label+".plist")
	darwin := runtime.GOOS == "darwin"

	code, report, diag := runUninstallFixture(t, "--confirm", "--keep-data")
	if code != 0 || !report.Uninstalled {
		t.Fatalf("keep-data exit %d: %s", code, diag)
	}
	if darwin && !slices.Contains(report.LaunchAgents, label) {
		t.Fatalf("keep-data did not find %s: %v", label, report.LaunchAgents)
	}
	uninstallExists(t, filepath.Join(f.root, "stopped-"+label), darwin)
	uninstallExists(t, plist, !darwin)
	uninstallExists(t, paths.Executable, false)
	uninstallExists(t, filepath.Join(paths.Data, "sessions.db"), true)
	uninstallExists(t, filepath.Join(paths.ClaudeTraceCopies, "session.jsonl"), true)
	uninstallExists(t, filepath.Join(deployment.WikiExportRoot(home), "alpha", "export.json"), true)

	if code, report, diag := runUninstallFixture(t, "--confirm", "--full-wipe"); code != 0 || !report.Uninstalled {
		t.Fatalf("full wipe exit %d: %s", code, diag)
	}
	uninstallExists(t, paths.Root, false)
	uninstallExists(t, deployment.WikiExportRoot(home), false)
	uninstallExists(t, home, false)
}

func TestUninstallDeletesProtectedCredentialsOnlyForFullWipe(t *testing.T) {
	for _, mode := range []string{"--keep-data", "--full-wipe"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newUninstallFixture(t)
			original := uninstallHostSecrets
			t.Cleanup(func() { uninstallHostSecrets = original })
			calls := 0
			uninstallHostSecrets = func(home string) error {
				calls++
				if _, err := os.Stat(filepath.Join(home, "refresh-grants", "fixture.json")); err != nil {
					t.Fatal("The uninstall removed a reference before its protected credential")
				}
				return nil
			}
			code, report, _ := runUninstallFixture(t, "--confirm", mode)
			if code != 0 || !report.Uninstalled {
				t.Fatal("The uninstall did not finish")
			}
			want := 0
			if mode == "--full-wipe" {
				want = len(fixture.homes)
			}
			if calls != want {
				t.Fatal("The uninstall selected the wrong protected credential cleanup mode")
			}
		})
	}
}

func TestUninstallKeepsReferencesWhenProtectedDeletionFails(t *testing.T) {
	fixture := newUninstallFixture(t)
	original := uninstallHostSecrets
	t.Cleanup(func() { uninstallHostSecrets = original })
	uninstallHostSecrets = func(string) error { return deployment.ErrHostCredentialUnavailable }
	code, report, _ := runUninstallFixture(t, "--confirm", "--full-wipe")
	if code == 0 || report.Uninstalled {
		t.Fatal("The uninstall hid a protected credential failure")
	}
	for _, home := range fixture.homes {
		if _, err := os.Stat(filepath.Join(home, "refresh-grants", "fixture.json")); err != nil {
			t.Fatal("The failed uninstall removed the credential reference")
		}
	}
	if _, err := os.Stat(fixture.binary); err != nil {
		t.Fatal("The failed uninstall removed its retry program")
	}
}
