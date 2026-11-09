//go:build !test_fake_routing && (darwin || linux)

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"mc/onboarding"
)

// TestTerminalSetupUntestedConnectionScreens captures a failed connection that setup removes after another key passes,
// and a passing test whose result setup cannot record.
func TestTerminalSetupUntestedConnectionScreens(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	for _, columns := range []int{80, 40} {
		for _, color := range []bool{true, false} {
			for _, variant := range []string{"another-key", "record"} {
				t.Run(fmt.Sprintf("%s-%d-color-%t", variant, columns, color), func(t *testing.T) {
					if color {
						if value, set := os.LookupEnv("NO_COLOR"); set {
							_ = os.Unsetenv("NO_COLOR")
							t.Cleanup(func() { _ = os.Setenv("NO_COLOR", value) })
						}
					} else {
						t.Setenv("NO_COLOR", "1")
					}
					master, slave := setupTerminalPair(t)
					if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(columns), Row: 24}); err != nil {
						t.Fatal(err)
					}
					w, _ := setupTestWizard(t)
					removed := setupKeyStore(w)
					attempts := 0
					w.deps.connect = func(*setupWizard, setupConnection) error {
						attempts++
						time.Sleep(300 * time.Millisecond)
						if attempts > 1 {
							return nil
						}
						if variant == "record" {
							return setupRecordFailure(errors.New("fixture: the database helper is unavailable"))
						}
						return setupRuntimeFailure(errors.New("fixture: provider status 401"), setupModelTestFailure)
					}
					terminal := onboarding.NewTerminal(slave, slave)
					terminal.SetStageTitles(setupStageTitles)
					w.ui, w.input, w.output = terminal, slave, terminal
					done := make(chan error, 1)
					go func() {
						w.session.Stage, w.session.Completed = 2, []string{"welcome", "prepare"}
						w.ui.Stage(3, setupStageTitles[2], w.session.Completed)
						done <- w.agents()
					}()
					session := &setupScreenSession{t: t, master: master, screen: newSetupScreen(columns, 24)}
					var evidence strings.Builder
					capture := func(title, text string) {
						lines := session.wait(text)
						fmt.Fprintf(&evidence, "%s\n%s\n\n", title, strings.Join(lines, "\n"))
					}
					capture("Connection choices", "q exit")
					session.send("\x1b[B\x1b[B\r")
					capture("Masked API key prompt", "Your input stays concealed.")
					session.send("synthetic-first-key\r")
					capture("Model prompt", "Model")
					session.send("fixture-model\r")
					capture("Explicit model test", "Test this connection?")
					session.send("\r")
					capture("Failed test and connection choices", "Retry Codex")
					if variant == "record" {
						session.send("\r")
						capture("Retry the test", "Test this connection?")
						session.send("\r")
					} else {
						session.send("\x1b[B\x1b[B\x1b[B\x1b[B\r")
						capture("Masked API key prompt for another key", "Your input stays concealed.")
						session.send("synthetic-second-key\r")
						capture("Model prompt for another key", "Model")
						session.send("fixture-model\r")
						capture("Explicit model test of another key", "Test this connection?")
						session.send("\r")
					}
					select {
					case err := <-done:
						if err != nil {
							t.Fatal(err)
						}
					case <-time.After(10 * time.Second):
						t.Fatal("The setup did not finish")
					}
					for session.read(150 * time.Millisecond) {
					}
					fmt.Fprintf(&evidence, "Selected agent\n%s\n", strings.Join(session.screen.lines(), "\n"))
					var want []string
					if variant == "another-key" {
						want = []string{"codex-api-key"}
					}
					if !slices.Equal(*removed, want) || len(w.session.RuntimeCreated) != 0 {
						t.Fatalf("Setup removed %q and retains %q", *removed, w.session.RuntimeCreated)
					}
					if root := os.Getenv("MC_SETUP_EVIDENCE_DIR"); root != "" {
						if err := os.MkdirAll(root, 0700); err != nil {
							t.Fatal(err)
						}
						name := fmt.Sprintf("untested-%s-%d-color-%t", variant, columns, color)
						if err := os.WriteFile(filepath.Join(root, name+".txt"), []byte(evidence.String()), 0600); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(root, name+".ansi"), []byte(session.raw.String()), 0600); err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		}
	}
}
