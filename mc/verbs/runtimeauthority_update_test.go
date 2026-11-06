package verbs

import (
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
