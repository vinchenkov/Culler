//go:build !test_fake_routing

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	operatorconfig "mc/config"
	"mc/onboarding"
)

type setupInterruptTestUI struct{ *setupTestUI }

func (ui *setupInterruptTestUI) Panel(title string, lines []string) {
	ui.setupTestUI.Panel(title, lines)
	if title == "Sign in through your browser" {
		process, err := os.FindProcess(os.Getpid())
		if err == nil {
			err = process.Signal(os.Interrupt)
		}
		if err != nil {
			ui.t.Error(err)
		}
	}
}

func TestTerminalSetupLoginInterruptCleansTheIsolatedHome(t *testing.T) {
	const marker = "MC_SETUP_INTERRUPT_FIXTURE"
	if os.Getenv(marker) != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTerminalSetupLoginInterruptCleansTheIsolatedHome$")
		for _, entry := range os.Environ() {
			name, _, _ := strings.Cut(entry, "=")
			switch name {
			case "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CODEX_ACCESS_TOKEN", "CODEX_API_KEY":
				continue
			}
			command.Env = append(command.Env, entry)
		}
		command.Env = append(command.Env, marker+"=1")
		body, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("the interrupted setup fixture failed: %v\n%s", err, body)
		}
		return
	}
	w, ui := setupTestWizard(t, "check")
	w.ui = &setupInterruptTestUI{ui}
	w.deps.connect = setupConnect
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	binding := setupBindingForKind(cfg, operatorconfig.CredentialKindCodexOAuth)
	w.session.Stage = 2
	w.session.Access = &onboarding.AccessChoice{Mode: "login", Step: "check", Harness: "codex", Template: binding, Binding: binding}
	if err := w.save(); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	acquired := filepath.Join(bin, "acquired-home")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$CODEX_HOME\" > %q\nprintf '%%s\\n' 'https://example.test/device'\nexec /bin/sleep 20\n", acquired)
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	err = w.checkConnection(setupConnection{Binding: binding, Mode: "login"})
	if !errors.Is(err, onboarding.ErrExit) {
		t.Fatalf("the interrupted login returned %v", err)
	}
	info, err := os.Stat(w.home)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatal("the interrupted login left the deployment home sealed")
	}
	body, err := os.ReadFile(acquired)
	if err != nil {
		t.Fatal(err)
	}
	flow := filepath.Dir(filepath.Dir(strings.TrimSpace(string(body))))
	if _, err := os.Lstat(flow); !os.IsNotExist(err) {
		t.Fatal("the interrupted login retained its disposable source directory")
	}
	if _, err := os.Lstat(filepath.Join(w.home, "runtime-auth-sources")); !os.IsNotExist(err) {
		t.Fatal("The login created a credential source inside the deployment home")
	}
	saved, err := onboarding.LoadSession(w.home)
	if err != nil || saved.Access == nil || saved.Access.Binding != binding || saved.RuntimeVerified[binding] {
		t.Fatal("the interrupted login lost its pending choice or reported verified access")
	}
}
