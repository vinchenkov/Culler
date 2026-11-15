package routing

import (
	"strings"
	"testing"

	operatorconfig "mc/config"
)

// endpointConfig enables both reference bindings and two endpoint agent
// profiles that the fixture defines.
func endpointConfig(t *testing.T) operatorconfig.Config {
	t.Helper()
	cfg := operatorconfig.Default()
	profile := cfg.Runtimes.Bindings["chatgpt"]
	profile.Model = "fixture-model"
	cfg.Runtimes.Bindings["chatgpt"] = profile
	for id, endpoint := range map[string]struct{ model, baseURL string }{
		"endpoint":        {"endpoint-model", "https://endpoint.example/anthropic"},
		"second-endpoint": {"second-model", "https://second.example/anthropic"},
	} {
		cfg.Runtimes.Credentials[id] = cfg.Runtimes.Credentials["claude"]
		cfg.Runtimes.Bindings[id] = operatorconfig.RuntimeBinding{
			Harness: "claude-sdk", Model: endpoint.model, Credential: id,
		}
	}
	cfg.Runtimes.EnabledBindings = []string{"chatgpt", "claude", "endpoint", "second-endpoint"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestConfiguredCatalogProjectsEveryBindingFromItsCredentialSource(t *testing.T) {
	cfg := endpointConfig(t)
	catalog := ConfiguredBindings(cfg.Runtimes)
	registry := RegistryFromRuntimes(cfg.Runtimes)
	for _, id := range []string{"chatgpt", "claude", "endpoint", "second-endpoint"} {
		spec, ok := catalog[id]
		if !ok || spec.ID != id || spec.Harness == "" || spec.Model == "" ||
			spec.AuthKind == "" || spec.Credential != id || registry[id] != spec.Harness {
			t.Fatalf("binding %q is incoherent: %+v registry=%q", id, spec, registry[id])
		}
		if spec.Delivery == ProviderProxy && (spec.TokenURL == "" || spec.ClientID == "" || spec.DeclaredStaticKey != "" || spec.BaseURL != "") {
			t.Fatalf("OAuth binding %q is incomplete: %+v", id, spec)
		}

	}
	if catalog["chatgpt"].Channel != "codex" || catalog["claude"].Channel != "claude" {
		t.Fatalf("OAuth channels = %q %q", catalog["chatgpt"].Channel, catalog["claude"].Channel)
	}
	if catalog["endpoint"].Delivery != ProviderProxy || catalog["second-endpoint"].Channel != "claude" {
		t.Fatal("The additional profiles do not use sign-in projection.")
	}
}

func TestReferenceCatalogHoldsOnlyTheSubscriptionBindings(t *testing.T) {
	catalog := ProductionBindings()
	if len(catalog) != 2 {
		t.Fatalf("reference catalog = %+v, want chatgpt and claude", catalog)
	}
	configured := ConfiguredBindings(endpointConfig(t).Runtimes)
	// The reference profile waits for setup to select its model.
	selected := configured["chatgpt"]
	selected.Model = ""
	configured["chatgpt"] = selected
	for _, id := range []string{"chatgpt", "claude"} {
		spec, ok := catalog[id]
		if !ok || spec.Delivery != ProviderProxy || spec.ProfileRevision != "" ||
			ProfileRevision(spec) != ProfileRevision(configured[id]) {
			t.Fatalf("reference binding %q = %+v, want %+v", id, spec, configured[id])
		}
		if single, ok := ProductionBinding(id); !ok || ProfileRevision(single) != ProfileRevision(spec) {
			t.Fatalf("ProductionBinding(%q) = %+v, %v", id, single, ok)
		}
	}
	if len(ProductionRegistry()) != 0 {
		t.Fatalf("the shipped catalog enables a binding before the operator selects it: %+v", ProductionRegistry())
	}
	delete(catalog, "chatgpt")
	if _, ok := ProductionBindings()["chatgpt"]; !ok {
		t.Fatal("caller mutation changed the production catalog")
	}
}

func TestTwoBindingsOnOneCredentialSourceProjectTheSameGrant(t *testing.T) {
	cfg := endpointConfig(t)
	cfg.Runtimes.Bindings["claude-sonnet"] = operatorconfig.RuntimeBinding{
		Harness: "claude-sdk", Model: "claude-sonnet-5", Credential: "claude",
	}
	cfg.Runtimes.EnabledBindings = append(cfg.Runtimes.EnabledBindings, "claude-sonnet")
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	catalog := EnabledBindings(cfg.Runtimes)
	opus, sonnet := catalog["claude"], catalog["claude-sonnet"]
	if opus.Credential != "claude" || sonnet.Credential != "claude" ||
		opus.TokenURL != sonnet.TokenURL || opus.ClientID != sonnet.ClientID ||
		opus.Model == sonnet.Model {
		t.Fatalf("shared credential projection = %+v %+v", opus, sonnet)
	}
}

func TestConfiguredBindingCatalogDrivesEnabledRouting(t *testing.T) {
	cfg := endpointConfig(t)
	cfg.Runtimes.EnabledBindings = []string{"chatgpt", "studio"}
	cfg.Runtimes.Credentials["studio"] = cfg.Runtimes.Credentials["claude"]
	cfg.Runtimes.Bindings["studio"] = operatorconfig.RuntimeBinding{
		Harness: "claude-sdk", Model: "studio-model",
		Credential: "studio",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	catalog := ConfiguredBindings(cfg.Runtimes)
	studio, ok := catalog["studio"]
	if !ok || studio.Harness != "claude-sdk" || studio.Channel != "claude" ||
		studio.Model != "studio-model" || studio.BaseURL != "" ||
		studio.DeclaredStaticKey != "" {
		t.Fatalf("configured studio binding = %+v", studio)
	}
	registry := RegistryFromRuntimes(cfg.Runtimes)
	if registry["studio"] != "claude-sdk" || registry["chatgpt"] != "codex" {
		t.Fatalf("configured registry = %+v", registry)
	}
	if _, ok := registry["claude"]; ok {
		t.Fatal("disabled binding entered routing registry")
	}
	if _, ok := catalog["endpoint"]; !ok {
		t.Fatal("test precondition: endpoint must remain configured but disabled")
	}
	candidate := strings.NewReplacer(
		"| strategist | claude-sdk | claude |", "| strategist | claude-sdk | studio |",
		"| collie | claude-sdk | claude |", "| collie | claude-sdk | studio |",
	).Replace(canonical)
	if _, err := Parse([]byte(candidate), registry); err == nil ||
		!strings.Contains(err.Error(), `unresolved binding "endpoint"`) {
		t.Fatalf("disabled configured binding routing error = %v", err)
	}
}

func TestEffortChangesProfileRevision(t *testing.T) {
	cfg := endpointConfig(t)
	original := ConfiguredBindings(cfg.Runtimes)["chatgpt"]
	profile := cfg.Runtimes.Bindings["chatgpt"]
	profile.Effort = "high"
	cfg.Runtimes.Bindings["chatgpt"] = profile
	changed := ConfiguredBindings(cfg.Runtimes)["chatgpt"]
	if changed.Effort != "high" || ProfileRevision(original) == ProfileRevision(changed) {
		t.Fatal("The profile revision omitted effort.")
	}
}
