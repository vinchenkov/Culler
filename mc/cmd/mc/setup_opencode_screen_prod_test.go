//go:build !test_fake_routing && (darwin || linux)

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	operatorconfig "mc/config"
	"mc/deployment"
	"mc/onboarding"
	"mc/opencodecatalog"
)

func TestTerminalSetupOpenCodeScreens(t *testing.T) {
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
				var download atomic.Int32
				download.Store(1)
				w.deps.saveKey = func(_ string, req deployment.AgentConnectionRequest) (deployment.AgentProfileCatalog, error) {
					if req.APIKey != "synthetic-opencode-screen-key" || req.TemplateID != "opencode:moonshotai" {
						t.Error("The selected provider or key changed.")
					}
					_, err := operatorconfig.UpdateAtomic(filepath.Join(w.home, "config.toml"), func(cfg *operatorconfig.Config) error {
						cfg.Runtimes.Credentials[req.ID] = operatorconfig.CredentialSource{Name: "Moonshot AI", Kind: operatorconfig.CredentialKindOpenCodeAPIKey, CatalogEntry: "moonshotai"}
						return cfg.Validate()
					})
					return deployment.AgentProfileCatalog{}, err
				}
				w.deps.openCodeStatus = func(string) *deployment.AgentProfileOperation {
					if download.Load() == 4 {
						return nil
					}
					state, message := "running", "Download OpenCode and its web search plugin."
					if download.Load() == 1 {
						state, message = "failed", "The OpenCode download failed. Check the network connection, then retry."
					}
					if download.Load() == 3 {
						state, message = "succeeded", "OpenCode is ready."
					}
					return &deployment.AgentProfileOperation{Status: state, Message: message}
				}
				w.deps.openCodeStart = func(_ string, retry bool) (deployment.AgentProfileOperation, error) {
					if download.Load() == 1 && !retry {
						t.Error("The download did not request a retry.")
					}
					download.Store(2)
					return *w.deps.openCodeStatus(w.home), nil
				}
				tests := 0
				w.deps.connect = func(_ *setupWizard, c setupConnection) error {
					if download.Load() != 3 || (c.Mode != "api_key" && c.Mode != "saved") {
						t.Error("The model test started before OpenCode was ready.")
					}
					tests++
					if tests == 1 {
						return &setupFailure{cause: deployment.OpenCodeRefusalMessage("moonshotai", &deployment.RuntimeAuthAdapterError{Diagnostic: "HTTP 401"})}
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
				capture := func(title, text string) {
					lines := session.wait(text)
					fmt.Fprintf(&evidence, "%s\n%s\n\n", title, strings.Join(lines, "\n"))
				}
				capture("Connection choices", "q exit")
				session.send(strings.Repeat("\x1b[B", 4))
				capture("OpenCode connection choice", "OpenCode runs the agent.")
				session.send("\r")
				capture("Provider search", "Provider")
				session.send("not-a-provider-match-92836\r")
				capture("No provider result", "No provider found")
				session.send("moonshot\r")
				capture("Provider result and full address", "Choose the provider")
				session.send("\r")
				capture("Concealed key prompt", "Your input stays concealed.")
				session.send("synthetic-opencode-screen-key\r")
				capture("Model list and prices", "Choose the model")
				provider, _ := opencodecatalog.Find("moonshotai")
				index := 0
				for i, model := range provider.Models {
					if model.ID == "kimi-k3" {
						index = i
					}
				}
				session.send(strings.Repeat("\x1b[B", index))
				capture("Selected model", "Kimi K3")
				session.send("\r")
				capture("Model effort list", "Choose the effort")
				session.send("\x1b[B\x1b[B\r")
				capture("Saved download failure and retry", "Resume this step")
				session.send("\r")
				capture("OpenCode download retry", "Downloading OpenCode")
				download.Store(4)
				capture("Absent download status", "unavailable. Retry the download.")
				session.send("\r")
				capture("Retry after absent status", "Downloading OpenCode")
				download.Store(3)
				capture("Explicit agent test", "Test this connection?")
				session.send("\r")
				capture("Refused key and other catalog entries", "refused the key.")
				session.send("\r")
				capture("Explicit model test retry", "Test this connection?")
				session.send("\r")
				capture("Verified agent summary", "Start with these choices?")
				session.send("q")
				select {
				case err := <-done:
					if !errors.Is(err, onboarding.ErrExit) {
						t.Fatal(err)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("The setup did not stop before activation.")
				}
				if strings.Contains(session.raw.String(), "synthetic-opencode-screen-key") {
					t.Fatal("The terminal exposed the key.")
				}
				cfg, err := w.catalog()
				if err != nil {
					t.Fatal(err)
				}
				profile := cfg.Runtimes.Bindings["opencode-moonshotai"]
				if profile.Harness != "opencode" || profile.Model != "kimi-k3" || profile.Effort != "high" {
					t.Fatal("The selected profile changed.")
				}
				if root := os.Getenv("MC_SETUP_EVIDENCE_DIR"); root != "" {
					if err := os.MkdirAll(root, 0700); err != nil {
						t.Fatal(err)
					}
					name := fmt.Sprintf("opencode-%d-color-%t", columns, color)
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

func TestOpenCodeDownloadTimeoutCause(t *testing.T) {
	err := waitOpenCodeDownload(&deployment.AgentProfileOperation{Status: "running"}, func() *deployment.AgentProfileOperation { t.Fatal("The timeout polled the status."); return nil }, func(string) {}, time.Now().Add(-time.Second))
	var failure *setupFailure
	if !errors.As(err, &failure) || failure.cause != "The OpenCode download exceeded its time limit. Retry the download." {
		t.Fatalf("The timeout cause changed: %v", err)
	}
}
