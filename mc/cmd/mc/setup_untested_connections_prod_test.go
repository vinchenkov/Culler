//go:build !test_fake_routing

package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	operatorconfig "mc/config"
	"mc/deployment"
	"mc/opencodecatalog"
	"mc/routing"
)

// setupKeyStore saves each key as the connection form does and reports an identity after the save.
func setupKeyStore(w *setupWizard) (removed *[]string) {
	saved, removals := map[string]bool{}, []string{}
	w.deps.identity = func(_, credential string) (string, error) {
		if saved[credential] {
			return strings.Repeat("a", 64), nil
		}
		return "", nil
	}
	w.deps.saveKey = func(_ string, request deployment.AgentConnectionRequest) (deployment.AgentProfileCatalog, error) {
		saved[request.ID] = true
		return deployment.AgentProfileCatalog{}, nil
	}
	w.deps.removeKey = func(_, id string) error {
		removals = append(removals, id)
		delete(saved, id)
		return nil
	}
	return &removals
}

func TestTerminalSetupRemovesAFailedConnectionWhenAnotherKeyIsSaved(t *testing.T) {
	w, ui := setupAccessFixture(t,
		setupChoiceAnswer{prompt: "API key", value: "synthetic-first-key"},
		setupChoiceAnswer{prompt: "Model", value: "fixture-model"},
		setupChoiceAnswer{prompt: "Test this connection?", value: "check"},
	)
	removed := setupKeyStore(w)
	recorded := []string{}
	w.deps.recordTest = func(_, binding string, authorization bool) error {
		if authorization {
			t.Error("An API key test recorded a sign-in.")
		}
		recorded = append(recorded, binding)
		return nil
	}
	w.deps.connect = func(_ *setupWizard, connection setupConnection) error {
		if connection.Binding == "codex-api-key" {
			return errors.New("The provider rejected the key.")
		}
		return w.testPassed(connection.Binding, false)
	}
	if err := w.apiKeyAccess("codex"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.session.RuntimeCreated, []string{"codex-api-key"}) || !slices.Equal(w.session.RuntimeIncomplete, []string{"codex-api-key"}) {
		t.Fatalf("The failed connection is not retained for a retry: %+v", w.session)
	}
	setupAccessReload(t, w)
	ui.answers = []setupChoiceAnswer{
		{prompt: "API key", value: "synthetic-second-key"},
		{prompt: "Model", value: "fixture-model"},
		{prompt: "Test this connection?", value: "check"},
	}
	if err := w.apiKeyAccess("claude-sdk"); err != nil {
		t.Fatal(err)
	}
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*removed, []string{"codex-api-key"}) {
		t.Fatalf("Setup removed %q; want the failed connection only", *removed)
	}
	if _, exists := cfg.Runtimes.Bindings["codex-api-key"]; exists {
		t.Fatal("The agent profile of the failed connection remains")
	}
	if _, exists := cfg.Runtimes.Credentials["codex-api-key"]; !exists {
		t.Fatal("Setup removed the reference connection from the configuration")
	}
	if len(w.session.RuntimeCreated) != 0 || len(w.session.RuntimeIncomplete) != 0 || !slices.Equal(w.session.RuntimeBindings, []string{"claude-api-key"}) {
		t.Fatalf("The session retains the removed connection: %+v", w.session)
	}
	if !slices.Equal(recorded, []string{"claude-api-key"}) {
		t.Fatalf("Setup recorded %q; want the passing test only", recorded)
	}
}

func TestTerminalSetupKeepsAFailedConnectionForARetry(t *testing.T) {
	w, ui := setupAccessFixture(t,
		setupChoiceAnswer{prompt: "API key", value: "synthetic-key"},
		setupChoiceAnswer{prompt: "Model", value: "fixture-model"},
		setupChoiceAnswer{prompt: "Test this connection?", value: "check"},
	)
	removed := setupKeyStore(w)
	tests := 0
	w.deps.connect = func(*setupWizard, setupConnection) error {
		tests++
		if tests == 1 {
			return errors.New("The provider is unreachable.")
		}
		return nil
	}
	if err := w.apiKeyAccess("codex"); err != nil {
		t.Fatal(err)
	}
	ui.answers = []setupChoiceAnswer{{prompt: "Test this connection?", value: "check"}}
	if err := w.checkConnection(setupConnection{Binding: "codex-api-key", Mode: "saved"}); err != nil {
		t.Fatal(err)
	}
	if err := w.selectAgent("codex-api-key"); err != nil {
		t.Fatal(err)
	}
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(*removed) != 0 || cfg.Runtimes.Bindings["codex-api-key"].Model != "fixture-model" || len(w.session.RuntimeCreated) != 0 {
		t.Fatalf("The retry lost the connection that passed: removed %q, %+v", *removed, w.session)
	}
}

func TestTerminalSetupFinishRemovesOnlyUnusedUntestedConnections(t *testing.T) {
	w, _ := setupTestWizard(t)
	removals := []string{}
	w.deps.removeKey = func(_, id string) error {
		removals = append(removals, id)
		if id == "opencode-locked" {
			return deployment.ErrHostCredentialUnavailable
		}
		return nil
	}
	provider := opencodecatalog.Release().Providers[0]
	_, err := operatorconfig.UpdateAtomic(filepath.Join(w.home, "config.toml"), func(cfg *operatorconfig.Config) error {
		for _, id := range []string{"opencode-unused", "opencode-assigned", "opencode-locked"} {
			cfg.Runtimes.Credentials[id] = operatorconfig.CredentialSource{Name: id, Kind: operatorconfig.CredentialKindOpenCodeAPIKey, CatalogEntry: provider.ID}
			cfg.Runtimes.Bindings[id] = operatorconfig.RuntimeBinding{Name: id, Harness: "opencode", Model: provider.Models[0].ID, Credential: id}
		}
		return cfg.Validate()
	})
	if err != nil {
		t.Fatal(err)
	}
	table, err := routing.UniformTable("opencode-assigned", routing.Registry{"opencode-assigned": "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.home, "routing.md"), []byte(table.Markdown()), 0o600); err != nil {
		t.Fatal(err)
	}
	w.session.RuntimeCreated = []string{"opencode-unused", "opencode-assigned", "opencode-locked"}
	w.session.RuntimeIncomplete = []string{"opencode-unused", "opencode-locked"}
	if err := w.selectAgent("chatgpt"); err != nil {
		t.Fatal(err)
	}
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(removals, []string{"opencode-unused", "opencode-locked"}) {
		t.Fatalf("Setup removed %q; want each unused connection", removals)
	}
	_, unusedCredential := cfg.Runtimes.Credentials["opencode-unused"]
	_, unusedProfile := cfg.Runtimes.Bindings["opencode-unused"]
	if unusedCredential || unusedProfile {
		t.Fatal("The unused connection remains in the configuration")
	}
	for _, id := range []string{"opencode-assigned", "opencode-locked"} {
		if _, exists := cfg.Runtimes.Bindings[id]; !exists {
			t.Fatalf("Setup removed %s from the configuration", id)
		}
	}
	if !slices.Equal(w.session.RuntimeCreated, []string{"opencode-locked"}) {
		t.Fatalf("The session lost the connection that a later step must remove: %q", w.session.RuntimeCreated)
	}
}

func TestTerminalSetupKeepsAPassingConnectionWithoutARecordedResult(t *testing.T) {
	w, ui := setupAccessFixture(t,
		setupChoiceAnswer{prompt: "API key", value: "synthetic-first-key"},
		setupChoiceAnswer{prompt: "Model", value: "fixture-model"},
		setupChoiceAnswer{prompt: "Test this connection?", value: "check"},
	)
	removed := setupKeyStore(w)
	w.deps.recordTest = func(string, string, bool) error {
		return setupRecordFailure(errors.New("fixture: the database helper is unavailable"))
	}
	w.deps.connect = func(w *setupWizard, connection setupConnection) error {
		return w.testPassed(connection.Binding, false)
	}
	if err := w.apiKeyAccess("codex"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.session.RuntimeIncomplete, []string{"codex-api-key"}) || len(w.session.RuntimeCreated) != 0 {
		t.Fatalf("The unrecorded result changed the retry state: %+v", w.session)
	}
	ui.answers = []setupChoiceAnswer{
		{prompt: "API key", value: "synthetic-second-key"},
		{prompt: "Model", value: "fixture-model"},
		{prompt: "Test this connection?", value: "check"},
	}
	if err := w.apiKeyAccess("claude-sdk"); err != nil {
		t.Fatal(err)
	}
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(*removed) != 0 || cfg.Runtimes.Bindings["codex-api-key"].Model != "fixture-model" {
		t.Fatalf("Setup removed a connection whose test passed: %q", *removed)
	}
}
