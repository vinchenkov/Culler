//go:build !test_fake_routing && (darwin || linux)

package main

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"mc/deployment"
	"mc/onboarding"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTerminalSetupProviderProxyFailureScreens(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	for _, columns := range []int{80, 40} {
		for _, color := range []bool{true, false} {
			t.Run(fmt.Sprintf("%d-color-%t", columns, color), func(t *testing.T) {
				if color {
					if value, set := os.LookupEnv("NO_COLOR"); set {
						_ = os.Unsetenv("NO_COLOR")
						t.Cleanup(func() { _ = os.Setenv("NO_COLOR", value) })
					}
				} else {
					t.Setenv("NO_COLOR", "1")
				}
				diagnostics, err := os.MkdirTemp("/tmp", "proxy-screen-")
				if err != nil {
					t.Fatal(err)
				}
				previous := setupDiagnosticRoot
				setupDiagnosticRoot = diagnostics
				t.Cleanup(func() { setupDiagnosticRoot = previous; _ = os.RemoveAll(diagnostics) })
				master, slave := setupTerminalPair(t)
				if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(columns), Row: 24}); err != nil {
					t.Fatal(err)
				}
				w, _ := setupTestWizard(t)
				saved, attempts := false, 0
				w.deps.identity = func(string, string) (string, error) {
					if saved {
						return strings.Repeat("a", 64), nil
					}
					return "", nil
				}
				finishFailure := make(chan struct{})
				defer func() {
					select {
					case <-finishFailure:
					default:
						close(finishFailure)
					}
				}()
				w.deps.connect = func(_ *setupWizard, connection setupConnection) error {
					attempts++
					if attempts == 1 {
						if connection.Mode != "login" {
							t.Error("The first connection did not use sign-in.")
						}
						saved = true
						<-finishFailure
						return setupRuntimeFailure(&deployment.ProviderProxyStartError{Err: errors.New("The host provider proxy release is unavailable.")}, "The cause is unknown. Sign in again.")
					}
					if connection.Mode != "saved" {
						t.Error("The retry repeated sign-in instead of the saved connection test.")
					}
					return nil
				}
				terminal := onboarding.NewTerminal(slave, slave)
				terminal.SetStageTitles(setupStageTitles)
				w.ui, w.input, w.output = terminal, slave, terminal
				w.session.Stage, w.session.Completed, w.session.ReleaseID = 2, []string{"welcome", "prepare"}, releaseBuildID
				done := make(chan error, 1)
				go func() { defer close(done); done <- w.run() }()
				defer func() {
					select {
					case <-finishFailure:
					default:
						close(finishFailure)
					}
					select {
					case <-done:
						return
					default:
					}
					_, _ = master.Write([]byte("q"))
					select {
					case <-done:
					case <-time.After(2 * time.Second):
					}
				}()
				session := &setupScreenSession{t: t, master: master, screen: newSetupScreen(columns, 24)}
				var evidence strings.Builder
				capture := func(title, text string) []string {
					lines := session.wait(text)
					fmt.Fprintf(&evidence, "%s\n%s\n\n", title, strings.Join(lines, "\n"))
					return lines
				}
				capture("Connection choices", "q exit")
				session.send("\r")
				capture("Sign-in confirmation", "Sign in and connect?")
				session.send("\r")
				capture("Connection progress", "Connect Codex (")
				close(finishFailure)
				failure := strings.Join(capture("Local failure and saved connection retry", "Retry Codex"), "\n")
				if strings.Contains(failure, "cause is unknown") || strings.Contains(failure, "rejected the credentials") || strings.Contains(failure, "✓ Connect Codex") {
					t.Fatal("The terminal misclassified the local failure or reported success.")
				}
				if !strings.Contains(strings.Join(strings.Fields(failure), " "), setupProviderProxyStartCause) {
					t.Fatal("The failure screen omitted the local cause or repair action.")
				}
				session.send("\r")
				capture("Explicit test of the saved connection", "Test this connection?")
				session.send("\r")
				capture("Successful test permits review", "Start with these choices?")
				session.send("q")
				select {
				case err := <-done:
					if !errors.Is(err, onboarding.ErrExit) {
						t.Fatalf("Setup did not stop before activation: %v", err)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("Setup did not finish.")
				}
				if attempts != 2 || !saved || len(w.session.RuntimeBindings) != 1 {
					t.Fatal("The local failure lost the saved connection or repeated sign-in.")
				}
				if root := os.Getenv("MC_SETUP_EVIDENCE_DIR"); root != "" {
					if err := os.MkdirAll(root, 0700); err != nil {
						t.Fatal(err)
					}
					name := fmt.Sprintf("provider-proxy-%d-color-%t", columns, color)
					for suffix, body := range map[string]string{".txt": evidence.String(), ".ansi": session.raw.String()} {
						if err := os.WriteFile(filepath.Join(root, name+suffix), []byte(body), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
			})
		}
	}
}
