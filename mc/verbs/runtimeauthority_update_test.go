package verbs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	operatorconfig "mc/config"
	"mc/dispatch"
	"mc/opencodecatalog"
)

func TestCandidateCatalogProblemStopsOnlyAffectedProfile(t *testing.T) {
	home := t.TempDir()
	cfg := testBindingConfig()
	provider := opencodecatalog.Release().Providers[0]
	cfg.Runtimes.Credentials["catalog"] = operatorconfig.CredentialSource{Kind: operatorconfig.CredentialKindOpenCodeAPIKey, CatalogEntry: provider.ID}
	cfg.Runtimes.Bindings["catalog"] = operatorconfig.RuntimeBinding{Harness: "opencode", Model: "removed-model", Credential: "catalog", Effort: "high"}
	cfg.Runtimes.EnabledBindings = append(cfg.Runtimes.EnabledBindings, "catalog")
	write := func() []byte {
		body, err := operatorconfig.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "config.toml"), body, 0600); err != nil {
			t.Fatal(err)
		}
		return body
	}
	before := write()
	authority, err := loadRuntimeAuthority(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := authority.bindings["catalog"]; ok {
		t.Fatal("invalid profile can launch work")
	}
	if authority.configuredBindings["catalog"].Model != "removed-model" || authority.registry["catalog"] != "opencode" {
		t.Fatal("invalid saved choices disappeared")
	}
	if _, ok := authority.bindings["claude"]; !ok {
		t.Fatal("valid profile cannot launch work")
	}
	after, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil || string(before) != string(after) {
		t.Fatal("the catalog check changed configuration")
	}
	profile := cfg.Runtimes.Bindings["catalog"]
	profile.Model, profile.Effort = provider.Models[0].ID, ""
	cfg.Runtimes.Bindings["catalog"] = profile
	write()
	repaired, err := loadRuntimeAuthority(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := repaired.bindings["catalog"]; !ok {
		t.Fatal("the repaired profile cannot launch work")
	}
	if repaired.digest == authority.digest {
		t.Fatal("repair retained the old authority identity")
	}
}

func TestCandidateCatalogRefusalPreservesTaskAttempts(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	home := os.Getenv("MC_HOME")
	path := filepath.Join(home, "config.toml")
	cfg, err := operatorconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	provider := opencodecatalog.Release().Providers[0]
	cfg.Runtimes.Credentials["catalog"] = operatorconfig.CredentialSource{Kind: operatorconfig.CredentialKindOpenCodeAPIKey, CatalogEntry: provider.ID}
	cfg.Runtimes.Bindings["catalog"] = operatorconfig.RuntimeBinding{Harness: "opencode", Model: "removed-model", Credential: "catalog"}
	cfg.Runtimes.EnabledBindings = append(cfg.Runtimes.EnabledBindings, "catalog")
	if _, err := operatorconfig.WriteAtomic(path, cfg); err != nil {
		t.Fatal(err)
	}
	routingPath := filepath.Join(home, "routing.md")
	body, err := os.ReadFile(routingPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(body), "| arbiter | codex | chatgpt |", "| arbiter | opencode | catalog |", 1)
	if updated == string(body) {
		t.Fatal("the fixture route differs")
	}
	if err := os.WriteFile(routingPath, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	before := dfInt(t, db, `SELECT dispatch_retries FROM tasks WHERE id=1`)
	prepared := dfPrepare(t, db, dfRequestID)
	attested, err := dispatchAttest(home, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if attested.refusal == nil {
		t.Fatal("an invalid saved profile passed attestation")
	}
	effect := dfCommit(t, db, prepared, attested)
	dfAssertInert(t, db, effect)
	if dfInt(t, db, `SELECT dispatch_retries FROM tasks WHERE id=1`) != before || dfInt(t, db, `SELECT blocked FROM tasks WHERE id=1`) != 0 {
		t.Fatal("the profile problem consumed a task attempt")
	}
	if _, _, err := resolveRoleRouteWithAuthority(home, "arbiter"); err == nil || !strings.Contains(err.Error(), "repair in the dashboard") {
		t.Fatalf("route repair: %v", err)
	}
}

// retainLiveModelsFixture writes the live models that a refresh of this release retained.
func retainLiveModelsFixture(t *testing.T, home string) string {
	t.Helper()
	models, err := os.ReadFile(filepath.Join("..", "opencodecatalog", "testdata", "live-models.json"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"version": 1, "catalog_digest": opencodecatalog.Digest(),
		"opencode_version": opencodecatalog.Release().OpenCodeVersion, "refreshed_at": "2026-01-01T00:00:00Z", "providers": json.RawMessage(models)})
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
	return file
}

func TestLaunchAuthorizationAcceptsLiveModelAndRefusesAbsentModel(t *testing.T) {
	home := t.TempDir()
	cfg := testBindingConfig()
	cfg.Runtimes.Credentials["catalog"] = operatorconfig.CredentialSource{Kind: operatorconfig.CredentialKindOpenCodeAPIKey, CatalogEntry: "moonshotai"}
	cfg.Runtimes.Bindings["live"] = operatorconfig.RuntimeBinding{Harness: "opencode", Model: "kimi-fixture-new", Credential: "catalog", Effort: "high"}
	cfg.Runtimes.Bindings["released"] = operatorconfig.RuntimeBinding{Harness: "opencode", Model: "kimi-k3", Credential: "catalog"}
	// The live model list holds this model at another origin, so the model list lacks it.
	cfg.Runtimes.Bindings["absent"] = operatorconfig.RuntimeBinding{Harness: "opencode", Model: "kimi-fixture-origin", Credential: "catalog"}
	cfg.Runtimes.Bindings["effort"] = operatorconfig.RuntimeBinding{Harness: "opencode", Model: "kimi-fixture-new", Credential: "catalog", Effort: "thinking"}
	cfg.Runtimes.EnabledBindings = append(cfg.Runtimes.EnabledBindings, "live", "released", "absent", "effort")
	body, err := operatorconfig.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), body, 0600); err != nil {
		t.Fatal(err)
	}
	retained := retainLiveModelsFixture(t, home)
	authority, err := loadRuntimeAuthority(home)
	if err != nil {
		t.Fatal(err)
	}
	for id, launch := range map[string]bool{"live": true, "released": true, "absent": false, "effort": false, "claude": true} {
		if _, ok := authority.bindings[id]; ok != launch {
			t.Fatalf("launch authorization of %s = %v, want %v", id, ok, launch)
		}
		if _, ok := authority.configuredBindings[id]; !ok {
			t.Fatalf("the saved profile %s disappeared", id)
		}
	}
	// A launch needs no network: the retained live models are the only source of this model.
	if err := os.Remove(retained); err != nil {
		t.Fatal(err)
	}
	released, err := loadRuntimeAuthority(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := released.bindings["live"]; ok {
		t.Fatal("launch authorization accepted a model that the model list lacks")
	}
	if _, ok := released.bindings["released"]; !ok || released.digest == authority.digest {
		t.Fatal("the release catalog model lost its launch authorization, or the authority kept its identity")
	}
}
