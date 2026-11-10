//go:build !test_fake_routing && (darwin || linux)

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"mc/deployment"
	"mc/onboarding"
)

func TestTerminalSetupRejectedAPIKeyReplacementScreens(t *testing.T) {
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
				master, slave := setupTerminalPair(t)
				if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(columns), Row: 24}); err != nil {
					t.Fatal(err)
				}
				w, _ := setupTestWizard(t)
				identityRoot := filepath.Join(w.home, "agent-profile-operations")
				if err := os.MkdirAll(identityRoot, 0700); err != nil {
					t.Fatal(err)
				}
				identity := []byte(`{"id":"` + strings.Repeat("a", 64) + `"}`)
				identityPath := filepath.Join(identityRoot, "connection-codex-api-key.identity")
				if err := os.WriteFile(identityPath, identity, 0600); err != nil {
					t.Fatal(err)
				}
				finishCheck := make(chan struct{})
				defer func() {
					select {
					case <-finishCheck:
					default:
						close(finishCheck)
					}
				}()
				checks, modelTests := 0, 0
				w.deps.saveKey = func(_ string, request deployment.AgentConnectionRequest) (deployment.AgentProfileCatalog, error) {
					checks++
					if request.APIKey != "synthetic-rejected-key" || request.AuthorizationID != strings.Repeat("a", 64) {
						t.Error("The replacement lost its key or authorization revision.")
					}
					<-finishCheck
					return deployment.AgentProfileCatalog{}, &deployment.APIKeyReplacementError{Status: 401}
				}
				w.deps.connect = func(_ *setupWizard, connection setupConnection) error {
					modelTests++
					if connection.Mode != "api_key" {
						t.Error("The saved API key selected native sign-in.")
					}
					return nil
				}
				terminal := onboarding.NewTerminal(slave, slave)
				terminal.SetStageTitles(setupStageTitles)
				w.ui, w.input, w.output = terminal, slave, terminal
				w.session.Stage, w.session.Completed, w.session.ReleaseID = 2, []string{"welcome", "prepare"}, releaseBuildID
				done := make(chan error, 1)
				go func() { done <- w.run() }()
				session := &setupScreenSession{t: t, master: master, screen: newSetupScreen(columns, 24)}
				var evidence strings.Builder
				capture := func(title, text string) []string {
					lines := session.wait(text)
					fmt.Fprintf(&evidence, "%s\n%s\n\n", title, strings.Join(lines, "\n"))
					return lines
				}
				capture("Connection choices", "q exit")
				session.send("\x1b[B\x1b[B\r")
				capture("Saved key choices", "Choose the API key")
				session.send("\x1b[B\r")
				capture("Masked replacement key", "Your input stays concealed.")
				session.send("synthetic-rejected-key\r")
				capture("Replacement check in progress", "Checking replacement key")
				close(finishCheck)
				failure := strings.Join(capture("Rejected replacement and retry", "Resume this step"), "\n")
				if !strings.Contains(failure, "previous key remains saved") || strings.Contains(failure, "API key saved") || strings.Contains(failure, "✓ Check replacement key") {
					t.Fatal("The failure concealed preservation or reported completion.")
				}
				session.send("\r")
				capture("Retry permits another key", "Your input stays concealed.")
				session.send(":back\r")
				capture("Return to connection choices", "Choose how to connect")
				session.send("\x1b[B\x1b[B\r")
				capture("Keep the previous key", "Choose the API key")
				session.send("\r")
				capture("Choose the model", "Model")
				session.send("fixture-cheap-model\r")
				capture("Explicit model test", "Test this connection?")
				session.send("\r")
				capture("The previous key remains usable", "Start with these choices?")
				session.send("q")
				select {
				case err := <-done:
					if !errors.Is(err, onboarding.ErrExit) {
						t.Fatalf("The setup did not stop before activation: %v", err)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("The setup did not finish.")
				}
				currentIdentity, err := os.ReadFile(identityPath)
				if err != nil || string(currentIdentity) != string(identity) || checks != 1 || modelTests != 1 || len(w.session.RuntimeBindings) != 1 {
					t.Fatal("The rejected replacement changed the saved identity or prevented its explicit test.")
				}
				if strings.Contains(session.raw.String(), "synthetic-rejected-key") {
					t.Fatal("The terminal exposed the replacement key.")
				}
				if root := os.Getenv("MC_SETUP_EVIDENCE_DIR"); root != "" {
					if err := os.MkdirAll(root, 0700); err != nil {
						t.Fatal(err)
					}
					name := fmt.Sprintf("api-key-rejected-%d-color-%t", columns, color)
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

func TestTerminalSetupAPIKeyScreens(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	for _, columns := range []int{80, 40} {
		for _, color := range []bool{true, false} {
			for _, retry := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d-color-%t-retry-%t", columns, color, retry), func(t *testing.T) {
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
					saved := false
					w.deps.identity = func(string, string) (string, error) {
						if saved {
							return strings.Repeat("a", 64), nil
						}
						return "", nil
					}
					w.deps.saveKey = func(_ string, request deployment.AgentConnectionRequest) (deployment.AgentProfileCatalog, error) {
						if request.APIKey != "synthetic-key-for-screen" {
							t.Error("The terminal changed the key")
						}
						saved = true
						return deployment.AgentProfileCatalog{}, nil
					}
					attempts := 0
					w.deps.connect = func(_ *setupWizard, connection setupConnection) error {
						attempts++
						if retry && attempts == 1 {
							return setupRuntimeFailure(errors.New("fixture: the provider closed the connection"), setupModelTestFailure)
						}
						if retry && connection.Mode != "saved" {
							t.Error("The retry did not use the saved session")
						}
						return nil
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
					// The concealed-input hint appears after the terminal disables echo.
					capture("Masked API key prompt", "Your input stays concealed.")
					session.send("synthetic-key-for-screen\r")
					capture("Saved key and model prompt", "Model")
					session.send("fixture-cheap-model\r")
					capture("Explicit model test", "Test this connection?")
					session.send("\r")
					if retry {
						capture("Failed test and saved connection retry", "Retry Codex")
						session.send("\r")
						capture("Test the saved connection", "Test this connection?")
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
					fmt.Fprintf(&evidence, "Successful model test\n%s\n", strings.Join(session.screen.lines(), "\n"))
					if strings.Contains(session.raw.String(), "synthetic-key-for-screen") {
						t.Fatal("The terminal exposed the API key")
					}
					if len(w.session.RuntimeBindings) != 1 {
						t.Fatal("The setup did not retain the verified connection")
					}
					if root := os.Getenv("MC_SETUP_EVIDENCE_DIR"); root != "" {
						if err := os.MkdirAll(root, 0700); err != nil {
							t.Fatal(err)
						}
						name := fmt.Sprintf("api-key-%d-color-%t-retry-%t", columns, color, retry)
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
