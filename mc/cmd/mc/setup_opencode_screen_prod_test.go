//go:build !test_fake_routing && (darwin || linux)

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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

func init() {
	// The saved model choice of an interrupted setup can name a model that the model list lacks.
	setupPreviewCases["opencode-model-absent"] = func(t *testing.T) setupPreviewCase {
		w, _ := setupTestWizard(t)
		w.session.Stage, w.session.Completed, w.session.ReleaseID = 2, []string{"welcome", "prepare"}, releaseBuildID
		w.session.Access = &onboarding.AccessChoice{Mode: "api_key", Step: "effort", Harness: "opencode", Template: "opencode:moonshotai", Binding: "opencode-moonshotai", Model: "kimi-absent"}
		return setupPreviewCase{wizard: w, steps: []setupPreviewStep{
			{Wait: "q exit", Send: "\r", Name: "Model that the connection does not offer"},
			{Wait: "q exit", Send: "q", Name: "Effort choice after the selection of another model"},
		}, run: func() {
			if err := w.run(); !errors.Is(err, onboarding.ErrExit) {
				t.Fatal(err)
			}
		}}
	}
}

func TestTerminalSetupOpenCodeScreens(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	// The model choice states one of two sources. Each source has a screen at each width and in each color mode
	// when the test writes the screens to a folder.
	type screens struct {
		columns     int
		color, live bool
	}
	cases := []screens{{80, true, true}, {80, false, false}, {40, true, false}, {40, false, true}}
	if os.Getenv("MC_SETUP_EVIDENCE_DIR") != "" {
		cases = append(cases, screens{80, true, false}, screens{80, false, true}, screens{40, true, true}, screens{40, false, false})
	}
	for _, c := range cases {
		{
			columns, color, live := c.columns, c.color, c.live
			t.Run(fmt.Sprintf("%d-color-%t-live-%t", columns, color, live), func(t *testing.T) {
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
				source := "The release catalog supplies these models."
				if live {
					retainSetupLiveModels(t, w.home, opencodecatalog.Digest())
					source = "The release catalog and the live model list of OpenCode supply these models."
				}
				var download atomic.Int32
				download.Store(1)
				w.deps.saveKey = func(_ string, req deployment.AgentConnectionRequest) (deployment.AgentProfileCatalog, error) {
					if req.APIKey != "synthetic-opencode-screen-key" || req.TemplateID != "opencode:moonshotai" || req.Name != "Moonshot AI connection" {
						t.Error("The selected provider or key changed.")
					}
					_, err := operatorconfig.UpdateAtomic(filepath.Join(w.home, "config.toml"), func(cfg *operatorconfig.Config) error {
						cfg.Runtimes.Credentials[req.ID] = operatorconfig.CredentialSource{Name: req.Name, Kind: operatorconfig.CredentialKindOpenCodeAPIKey, CatalogEntry: "moonshotai"}
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
				session.send(strings.Repeat("\x1b[B", 2))
				capture("OpenCode connection choice", "API charges apply.")
				session.send("\r")
				capture("Provider search and each provider", "Type to search the")
				// The input line ends each frame, so the screen holds the list of the complete query.
				search := func(title, keys, query string, texts ...string) {
					session.send(keys)
					capture(title, "> "+query)
					screen := strings.Join(session.screen.lines(), "\n")
					for _, text := range append(texts, "\nProvider\n") {
						if !strings.Contains(screen, text) {
							t.Fatalf("The provider search for %q lacks %q: %q", query, text, session.screen.lines())
						}
					}
				}
				search("Matches after each key", "kim", "kim", "› Kimi For Coding (kimi.com)", "  Kimi For Coding (kimi.ai)")
				search("Longest provider detail", "\x15opencode", "opencode", "› OpenCode", "  OpenCode Go")
				search("No provider result", "\x15not-a-provider-match-92836", "not-a-provider-match-92836", "! No provider found", "\nctrl+b back  ctrl+c exit\n")
				search("Provider result and full address", "\r\x15moonshot", "moonshot", "› Moonshot AI\n", "  Moonshot AI (China)")
				session.send("\r")
				capture("Selected provider and concealed key prompt", "Your input stays concealed.")
				if !strings.Contains(strings.Join(session.screen.lines(), "\n"), "› Provider: Moonshot AI\n") {
					t.Fatalf("The screen lacks the selected provider: %q", session.screen.lines())
				}
				session.send("synthetic-opencode-screen-key\r")
				capture("Model list and prices", "Choose the model")
				if text := strings.Join(strings.Fields(strings.Join(session.screen.lines(), " ")), " "); !strings.Contains(text, source+" Prices apply") {
					t.Fatalf("The model choice lacks the source of the model list: %q", session.screen.lines())
				}
				provider, _ := deployment.OpenCodeProvider(w.home, "moonshotai")
				if _, listed := provider.Model("kimi-fixture-new"); listed != live {
					t.Fatal("The model choice lists other models than the model list.")
				}
				// A live model shows the same facts as a release catalog model.
				if text := strings.Join(strings.Fields(strings.Join(session.screen.lines(), " ")), " "); live &&
					!strings.Contains(text, "Kimi Fixture Pair Fast (kimi-fixture-pair-fast) Context: 1048576 tokens. Input: $3. Output: $15.") {
					t.Fatalf("The live model shows other facts: %q", session.screen.lines())
				}
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
				if profile.Name != "Moonshot AI profile" || cfg.Runtimes.Credentials["opencode-moonshotai"].Name != "Moonshot AI connection" || profile.Harness != "opencode" || profile.Model != "kimi-k3" || profile.Effort != "high" {
					t.Fatal("The selected profile changed.")
				}
				if root := os.Getenv("MC_SETUP_EVIDENCE_DIR"); root != "" {
					if err := os.MkdirAll(root, 0700); err != nil {
						t.Fatal(err)
					}
					name := fmt.Sprintf("opencode-%d-color-%t", columns, color)
					if live {
						name += "-live"
					}
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

func TestTerminalSetupReturnsToTheModelChoiceForAModelThatTheModelListLacks(t *testing.T) {
	w, ui := setupTestWizard(t, "kimi-fixture-new", "high")
	retainSetupLiveModels(t, w.home, opencodecatalog.Digest())
	if _, err := operatorconfig.UpdateAtomic(filepath.Join(w.home, "config.toml"), func(cfg *operatorconfig.Config) error {
		cfg.Runtimes.Credentials["opencode-moonshotai"] = operatorconfig.CredentialSource{Name: "Moonshot AI", Kind: operatorconfig.CredentialKindOpenCodeAPIKey, CatalogEntry: "moonshotai"}
		return cfg.Validate()
	}); err != nil {
		t.Fatal(err)
	}
	choice := &onboarding.AccessChoice{Mode: "api_key", Step: "effort", Harness: "opencode", Template: "opencode:moonshotai", Binding: "opencode-moonshotai", Model: "kimi-absent"}
	if err := w.apiKeyStep(choice); err != nil || choice.Step != "model" {
		t.Fatalf("The model that the model list lacks gave the step %q: %v", choice.Step, err)
	}
	if got := ui.panels[len(ui.panels)-1]; got != "The saved model is unavailable\nThe connection does not offer the model kimi-absent.\nSelect another model." {
		t.Fatalf("The refusal shows %q.", got)
	}
	// The model choice accepts a live model, and the effort choice lists its effort levels.
	if err := w.apiKeyStep(choice); err != nil || choice.Step != "effort" || choice.Model != "kimi-fixture-new" {
		t.Fatalf("The model choice gave %q at the step %q: %v", choice.Model, choice.Step, err)
	}
	if err := w.apiKeyStep(choice); err != nil || choice.Step != "download" {
		t.Fatalf("The effort choice gave the step %q: %v", choice.Step, err)
	}
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	if profile := cfg.Runtimes.Bindings["opencode-moonshotai"]; profile.Model != "kimi-fixture-new" || profile.Effort != "high" {
		t.Fatalf("Setup saved the profile %+v.", profile)
	}
}

func TestOpenCodeDownloadTimeoutCause(t *testing.T) {
	err := waitOpenCodeDownload(&deployment.AgentProfileOperation{Status: "running"}, func() *deployment.AgentProfileOperation { t.Fatal("The timeout polled the status."); return nil }, func(string) {}, time.Now().Add(-time.Second))
	var failure *setupFailure
	if !errors.As(err, &failure) || failure.cause != "The OpenCode download exceeded its time limit. Retry the download." {
		t.Fatalf("The timeout cause changed: %v", err)
	}
}

func TestSetupProviderSearchMatchesTheDashboardSearch(t *testing.T) {
	labels := func(query string) []string {
		var result []string
		for _, choice := range setupProviderChoices(query) {
			result = append(result, choice.Label)
		}
		return result
	}
	if got := labels(""); len(got) != len(deployment.AgentProviderCatalog())-2 || !slices.Contains(got, "Moonshot AI") || slices.Contains(got, setupProviderName("codex")) {
		t.Fatalf("The empty search lists %q.", got)
	}
	if got := labels("kim"); !slices.Equal(got, []string{"Kimi For Coding (kimi.com)", "Kimi For Coding (kimi.ai)"}) {
		t.Fatalf("The search for kim lists %q.", got)
	}
	if got := labels("moonshotai"); !slices.Contains(got, "Moonshot AI") {
		t.Fatalf("The identifier search lists %q.", got)
	}
	if got := labels("opencode"); !slices.Contains(got, "OpenCode Go") || slices.Contains(got, "Moonshot AI") {
		t.Fatalf("The search for opencode lists %q.", got)
	}
	if got := labels("API.MOONSHOT.AI"); !slices.Equal(got, []string{"Moonshot AI"}) {
		t.Fatalf("The address search lists %q.", got)
	}
	if got := labels("openai"); len(got) < 2 || slices.Contains(got, setupProviderName("codex")) {
		t.Fatalf("The search for openai lists %q.", got)
	}
	if got := labels("open"); len(got) < 4 || !slices.ContainsFunc(got[:4], func(label string) bool { return strings.HasPrefix(label, "Open") }) ||
		slices.ContainsFunc(got[:4], func(label string) bool { return !strings.HasPrefix(label, "Open") }) {
		t.Fatalf("The first page for open lists %q.", got)
	}
}

// The redraw moves the cursor up to the first row of the previous frame, so each frame must fit 24 rows.
func TestSetupProviderSearchFitsTheScreen(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	count := len(setupProviderChoices("/"))
	if count != len(setupProviderChoices("")) {
		t.Fatalf("The search for / lists %d of %d providers.", count, len(setupProviderChoices("")))
	}
	for _, columns := range []int{80, 40} {
		t.Run(fmt.Sprint(columns), func(t *testing.T) {
			master, slave := setupTerminalPair(t)
			if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(columns), Row: 24}); err != nil {
				t.Fatal(err)
			}
			terminal := onboarding.NewTerminal(slave, slave)
			done := make(chan error, 1)
			go func() {
				_, err := terminal.Search(setupProviderSearch())
				done <- err
			}()
			var output strings.Builder
			buffer := make([]byte, 65536)
			deadline := time.Now().Add(30 * time.Second)
			for typed, finished := false, false; ; {
				if !typed && strings.Contains(output.String(), "Type to search the") {
					if _, err := master.Write([]byte("/" + strings.Repeat("\x1b[B", count) + "\x03")); err != nil {
						t.Fatal(err)
					}
					typed = true
				}
				fds := []unix.PollFd{{Fd: int32(master.Fd()), Events: unix.POLLIN}}
				ready, err := unix.Poll(fds, 100)
				if err != nil && !errors.Is(err, unix.EINTR) {
					t.Fatal(err)
				}
				if ready > 0 {
					size, err := master.Read(buffer)
					if err != nil {
						t.Fatal(err)
					}
					output.Write(buffer[:size])
					continue
				}
				if finished {
					break
				}
				select {
				case err := <-done:
					if !errors.Is(err, onboarding.ErrExit) {
						t.Fatal(err)
					}
					finished = true
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("The search did not finish.")
				}
			}
			moves := regexp.MustCompile(`\x1b\[([0-9]+)A`).FindAllStringSubmatch(output.String(), -1)
			if len(moves) < count {
				t.Fatalf("The search redrew %d of %d frames.", len(moves), count)
			}
			for _, move := range moves {
				if rows, _ := strconv.Atoi(move[1]); rows > 23 {
					t.Fatalf("A provider search frame needs %d rows above the input line.", rows)
				}
			}
		})
	}
}

// retainSetupLiveModels writes the live models that a refresh retained for a release catalog.
func retainSetupLiveModels(t *testing.T, home, catalog string) {
	t.Helper()
	models, err := os.ReadFile(filepath.Join("..", "..", "opencodecatalog", "testdata", "live-models.json"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"version": 1, "catalog_digest": catalog, "opencode_version": opencodecatalog.Release().OpenCodeVersion,
		"refreshed_at": "2026-01-01T00:00:00Z", "providers": json.RawMessage(models)})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, "state", "opencode-live-models.json")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, body, 0600); err != nil {
		t.Fatal(err)
	}
}
