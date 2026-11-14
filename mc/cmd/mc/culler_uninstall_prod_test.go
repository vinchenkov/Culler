//go:build !test_fake_routing

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mc/deployment"
)

func TestCullerUninstallOptions(t *testing.T) {
	for _, args := range [][]string{
		{"uninstall", "--mc-home", "/example"},
		{"uninstall", "--mc-home", ""},
		{"uninstall", "--keep-data", "--full-wipe"},
		{"uninstall", "resident"},
		{"uninstall", "--check"},
		{"uninstall", "--tail", "1"},
		{"uninstall", "--home"},
		{"uninstall", "--workspace", "relative"},
		{"uninstall", "--binary", "--yes"},
		{"status", "--keep-data"},
		{"update", "--full-wipe"},
		{"start", "--yes"},
		{"logs", "--dry-run"},
		{"version", "--home", "/example"},
	} {
		if _, err := parseCuller(args); err == nil {
			t.Fatalf("The parser accepted %q.", args)
		}
	}
	o, err := parseCuller([]string{"uninstall", "--keep-data", "--dry-run", "--yes", "--home", "/one", "--home", "/two", "--workspace", "/Project with spaces", "--binary", "/custom/mc"})
	if err != nil || !o.keepData || !o.dryRun || !o.yes || len(o.homes) != 2 || len(o.workspaces) != 1 || len(o.binaries) != 1 {
		t.Fatalf("The parser lost uninstall options: %+v, %v", o, err)
	}
}

func cullerUninstallFixture(t *testing.T) (uninstallFixture, cullerDependencies, string) {
	t.Helper()
	f := newUninstallFixture(t)
	command := filepath.Join(filepath.Dir(f.binary), "culler")
	uninstallWrite(t, command, "Culler command fixture", 0700)
	digest, err := deployment.CLIFileDigest(command)
	if err != nil {
		t.Fatal(err)
	}
	names, _ := deployment.RuntimeNames(f.homes[0])
	directory := filepath.Dir(command)
	block := "\n# Culler command " + names.Suffix + "\ncase \":$PATH:\" in\n  *:" + cullerQuote(directory) + ":*) ;;\n  *) " + deployment.CLIPathCommand(directory) + " ;;\nesac\n# End Culler command " + names.Suffix + "\n"
	shell := filepath.Join(f.user, ".zshrc")
	uninstallWrite(t, shell, "export EDITOR=vi\n"+block, 0600)
	if err := writePublishedState(filepath.Join(f.homes[0], deployment.CLIInstallRecord), deployment.CLIInstallation{
		Path: command, SHA256: digest, Release: "dev", Verified: true,
		Shells: []deployment.CLIShellEdit{{Path: shell, Block: block, Directory: directory}},
	}); err != nil {
		t.Fatal(err)
	}
	deps := cullerDependencies{uninstall: func(ctx context.Context, o cullerOptions, in io.Reader, out, diagnostic io.Writer) int {
		return cullerUninstall(ctx, o, in, out, diagnostic, []string{f.root})
	}}
	return f, deps, command
}

func TestCullerUninstallModesAndPreview(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("The public uninstall command requires the Mac host.")
	}
	for _, mode := range []string{"", "--full-wipe", "--keep-data"} {
		for _, preview := range []bool{false, true} {
			t.Run(mode+map[bool]string{true: "-preview", false: "-remove"}[preview], func(t *testing.T) {
				f, deps, command := cullerUninstallFixture(t)
				args := []string{"uninstall", "--yes"}
				if mode != "" {
					args = append(args, mode)
				}
				if preview {
					args = append(args, "--dry-run")
				}
				var out bytes.Buffer
				if code := runCullerWith(args, nil, &out, &out, deps); code != 0 {
					t.Fatalf("The uninstall failed: %d\n%s", code, out.String())
				}
				for _, home := range f.homes {
					uninstallExists(t, filepath.Join(home, "config.toml"), preview || mode == "--keep-data")
					uninstallExists(t, filepath.Join(home, "release/program"), preview)
				}
				uninstallExists(t, command, preview)
				uninstallExists(t, f.binary, preview)
				uninstallExists(t, filepath.Join(f.project, "source.txt"), true)
				uninstallExists(t, filepath.Join(f.project, ".mc-artifacts/data"), preview || mode == "--keep-data")
				shell, err := os.ReadFile(filepath.Join(f.user, ".zshrc"))
				if err != nil || !strings.Contains(string(shell), "export EDITOR=vi") || strings.Contains(string(shell), "Culler command") != preview {
					t.Fatalf("The uninstall changed unrelated shell text or retained its entry: %s, %v", shell, err)
				}
				if !strings.Contains(out.String(), "all detected Culler installations") || !strings.Contains(out.String(), command) || strings.Contains(out.String(), "\x1b") {
					t.Fatalf("The output lost the scope, command, or plain format:\n%s", out.String())
				}
				if preview && (!strings.Contains(out.String(), "Culler removed no resources.") || strings.Contains(out.String(), "Culler software and data removed")) {
					t.Fatal("The preview claimed removal.")
				}
			})
		}
	}
}

func TestCullerUninstallRequiresTerminalOrExplicitConsent(t *testing.T) {
	f, _, command := cullerUninstallFixture(t)
	var out bytes.Buffer
	code := cullerUninstall(context.Background(), cullerOptions{}, strings.NewReader("wipe\n"), &out, &out, []string{f.root})
	if code != 2 || !strings.Contains(out.String(), "Confirmation requires a terminal") {
		t.Fatalf("The command accepted a piped answer: %d\n%s", code, out.String())
	}
	uninstallExists(t, command, true)
	uninstallExists(t, filepath.Join(f.root, "calls"), false)
}

func TestCullerUninstallRejectsContainerScopeBeforeRemoval(t *testing.T) {
	for _, role := range []string{"worker", "collie"} {
		t.Run(role, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "run.json")
			body := `{"run_id":"fixture","tier":"pipeline","role":"worker"}`
			if role == "collie" {
				body = `{"run_id":"fixture","tier":"collie","role":"collie"}`
			}
			uninstallWrite(t, path, body, 0600)
			t.Setenv("MC_RUN_JSON", path)
			t.Setenv("MC_HOME", filepath.Join(t.TempDir(), "absent"))
			deps := cullerDependencies{uninstall: func(context.Context, cullerOptions, io.Reader, io.Writer, io.Writer) int {
				t.Fatal("The container caller reached the uninstaller.")
				return 0
			}}
			var out bytes.Buffer
			if code := runCullerWith([]string{"uninstall", "--yes"}, nil, &out, &out, deps); code != 2 {
				t.Fatalf("The container caller returned %d: %s", code, out.String())
			}
		})
	}
}

func TestCullerUninstallFailureAndCancellationPreserveCommand(t *testing.T) {
	for _, failure := range []string{"preview", "image", "lock", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			f, _, command := cullerUninstallFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch failure {
			case "preview":
				uninstallWrite(t, filepath.Join(f.root, "volumes"), "mc-spine-0123456789ab\n", 0600)
			case "image":
				uninstallWrite(t, filepath.Join(f.root, "fail-image"), "", 0600)
			case "lock":
				lock, err := lockPublishedSetup(f.homes[0])
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
			case "cancel":
				cancel()
			}
			var out bytes.Buffer
			code := cullerUninstall(ctx, cullerOptions{yes: true, home: f.homes[0]}, nil, &out, &out, []string{f.root})
			want := 1
			if failure == "cancel" {
				want = 130
			}
			if code != want || strings.Contains(out.String(), "Culler software and data removed") {
				t.Fatalf("The failed uninstall returned %d:\n%s", code, out.String())
			}
			uninstallExists(t, command, true)
			uninstallExists(t, f.binary, true)
			for _, home := range f.homes {
				uninstallExists(t, filepath.Join(home, "config.toml"), true)
			}
			if failure != "image" {
				calls, _ := os.ReadFile(filepath.Join(f.root, "calls"))
				if strings.Contains(string(calls), " rm ") {
					t.Fatal("Removal started without a valid plan and consent.")
				}
			} else if !strings.Contains(out.String(), "curl -fsSL") || strings.Contains(out.String(), "--mc-home ''") {
				t.Fatal("The partial failure omitted a usable public recovery command.")
			}
		})
	}
}

func TestUninstallHoldsLocksThroughConfirmation(t *testing.T) {
	f, _, command := cullerUninstallFixture(t)
	called := false
	ui := uninstallPresentation{
		plan: func(uninstallReport) {}, problems: func([]string) {}, progress: func(string) {},
		confirm: func(uninstallReport) bool {
			called = true
			for _, home := range f.homes {
				if lock, err := lockPublishedSetup(home); err == nil {
					lock.Close()
					t.Fatal("Another installer acquired the deployment during confirmation.")
				}
			}
			return false
		},
	}
	code := runUninstallWith(context.Background(), []string{"__uninstall", "--confirm"}, io.Discard, io.Discard, []string{f.root}, ui)
	if code != 130 || !called {
		t.Fatal("The confirmation did not cancel removal.")
	}
	uninstallExists(t, command, true)
	for _, home := range f.homes {
		lock, err := lockPublishedSetup(home)
		if err != nil {
			t.Fatal("The cancelled uninstall retained a lock.")
		}
		lock.Close()
	}
}

func TestCullerUninstallEmptyInventoryNeedsNoConfirmation(t *testing.T) {
	f, _, _ := cullerUninstallFixture(t)
	clearCullerUninstallFixture(t, f)
	var out bytes.Buffer
	if code := cullerUninstall(context.Background(), cullerOptions{yes: true}, nil, &out, &out, []string{f.root}); code != 0 {
		t.Fatalf("The empty uninstall failed: %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "No Culler resources to remove") || strings.Contains(out.String(), "Culler software and data removed") {
		t.Fatalf("The empty uninstall claimed removal:\n%s", out.String())
	}
}

func clearCullerUninstallFixture(t *testing.T, f uninstallFixture) {
	t.Helper()
	if err := os.RemoveAll(f.user); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"containers", "volumes", "labels", "removed-image"} {
		uninstallWrite(t, filepath.Join(f.root, name), "", 0600)
	}
	for _, home := range f.homes {
		names, _ := deployment.RuntimeNames(home)
		uninstallWrite(t, filepath.Join(f.root, "removed-"+names.Helper), "", 0600)
	}
}

func TestUninstallRejectsInstallationPublishedBeforeLocks(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("The startup inventory requires macOS.")
	}
	f, _, command := cullerUninstallFixture(t)
	newHome := filepath.Join(f.user, "another-deployment")
	names, err := deployment.RuntimeNames(newHome)
	if err != nil {
		t.Fatal(err)
	}
	label := deployment.UnitLabel(names, "resident")
	const plist = `<?xml version="1.0"?><plist version="1.0"><dict><key>EnvironmentVariables</key><dict><key>MC_HOME</key><string>`
	uninstallWrite(t, filepath.Join(f.root, "new.plist"), plist+newHome+`</string></dict></dict></plist>`, 0600)
	tool := filepath.Join(f.root, "tools/launchctl")
	body, err := os.ReadFile(tool)
	if err != nil {
		t.Fatal(err)
	}
	addition := `
if [ "$1" = list ] && [ ! -f published ]; then
  mkdir -p "$HOME/another-deployment"
  touch "$HOME/another-deployment/config.toml" "$HOME/another-deployment/deployment.uuid" published
  cp new.plist "$HOME/Library/LaunchAgents/` + label + `.plist"
fi
`
	uninstallWrite(t, tool, strings.Replace(string(body), "case \"$1\" in", addition+"case \"$1\" in", 1), 0700)
	var out bytes.Buffer
	code := cullerUninstall(context.Background(), cullerOptions{yes: true}, nil, &out, &out, []string{f.root})
	if code != 1 || !strings.Contains(out.String(), "installation changed") {
		t.Fatalf("The changed inventory did not stop removal: %d\n%s", code, out.String())
	}
	uninstallExists(t, command, true)
	uninstallExists(t, filepath.Join(newHome, "config.toml"), true)
}

func cullerUninstallPreview(t *testing.T, scenario string) {
	t.Helper()
	f, deps, _ := cullerUninstallFixture(t)
	args, expected := []string{"uninstall"}, 0
	switch scenario {
	case "uninstall-keep":
		args = append(args, "--keep-data")
	case "uninstall-preview":
		args = append(args, "--dry-run")
	case "uninstall-keep-preview":
		args = append(args, "--keep-data", "--dry-run")
	case "uninstall-yes":
		args = append(args, "--yes")
	case "uninstall-empty":
		clearCullerUninstallFixture(t, f)
	case "uninstall-cancel", "uninstall-interrupt":
		expected = 130
	case "uninstall-failure":
		args, expected = append(args, "--yes"), 1
		uninstallWrite(t, filepath.Join(f.root, "fail-image"), "", 0600)
	case "uninstall-preview-failure":
		args, expected = append(args, "--dry-run"), 1
		uninstallWrite(t, filepath.Join(f.root, "volumes"), "mc-spine-0123456789ab\n", 0600)
	case "uninstall-noninteractive":
		expected = 2
	case "uninstall-home":
		args, expected = append(args, "--mc-home", f.homes[0]), 2
	}
	var in io.Reader = os.Stdin
	if scenario == "uninstall-noninteractive" {
		in = strings.NewReader("wipe\n")
	}
	code := runCullerWith(args, in, os.Stdout, os.Stderr, deps)
	if path := os.Getenv("CULLER_PREVIEW_RESULT"); path != "" {
		body, _ := json.Marshal(map[string]any{"cli_arguments": args, "cli_exit_status": code, "expected_cli_exit_status": expected, "fixture_success": code == expected})
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if code != expected {
		t.Fatalf("The uninstall command returned %d; expected %d.", code, expected)
	}
}
