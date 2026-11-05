package verbs

import (
	"strings"
	"testing"

	operatorconfig "mc/config"
	"mc/routing"
)

// dvCapability is the enabled runtime catalog seam tests dispatch against: the
// shipped defaults projected exactly the way a host projects config.toml, plus
// the fake family when (and only when) the tagged build admits it.
func dvCapability(t *testing.T) PrivateRuntimeCapability {
	t.Helper()
	return shippedCapability()
}

// enableTestBindings defines the endpoint binding and enables it with both
// reference bindings. The test routing tables name this set. The template
// enables no binding until onboarding records the operator's selection.
func enableTestBindings(cfg *operatorconfig.Config) error {
	profile := cfg.Runtimes.Bindings["chatgpt"]
	profile.Model = "fixture-model"
	cfg.Runtimes.Bindings["chatgpt"] = profile
	cfg.Runtimes.Credentials["endpoint"] = cfg.Runtimes.Credentials["claude"]
	cfg.Runtimes.Bindings["endpoint"] = operatorconfig.RuntimeBinding{
		Harness: "claude-sdk", Model: "endpoint-model", Credential: "endpoint",
	}
	cfg.Runtimes.EnabledBindings = []string{"chatgpt", "claude", "endpoint"}
	return nil
}

// testBindingConfig is the shipped config with the test bindings enabled.
func testBindingConfig() operatorconfig.Config {
	cfg := operatorconfig.Default()
	_ = enableTestBindings(&cfg)
	return cfg
}

// testEndpointBinding is the endpoint binding that testBindingConfig enables.
func testEndpointBinding() routing.BindingSpec {
	return routing.EnabledBindings(testBindingConfig().Runtimes)["endpoint"]
}

// shippedCapability is the t-free form the closed private frames need.
func shippedCapability() PrivateRuntimeCapability {
	cfg := testBindingConfig()
	digest, err := operatorconfig.RuntimeCatalogDigest(cfg.Runtimes)
	if err != nil {
		panic("shipped runtime catalog has no digest: " + err.Error())
	}
	bindings := routing.EnabledBindings(cfg.Runtimes)
	if spec, ok := routing.ActiveBinding("fake"); ok && spec.Harness == "fake" {
		bindings["fake"] = spec
	}
	return runtimeAuthority{bindings: bindings, digest: digest}.capability()
}

// dvHomieBinding is the frozen historical binding a seeded test session speaks:
// the shipped Homie route, in the "<harness>/<id>" form HomieStart records.
func dvHomieBinding(t *testing.T) string {
	t.Helper()
	capability := dvCapability(t)
	for _, spec := range capability.Bindings {
		if spec.ID == "claude" {
			return spec.Harness + "/" + spec.ID
		}
	}
	t.Fatalf("shipped catalog has no claude binding: %+v", capability.Bindings)
	return ""
}

func TestRuntimeCapabilityProjectionIsOrderedAndCovered(t *testing.T) {
	capability := dvCapability(t)
	if len(capability.Bindings) == 0 {
		t.Fatal("the shipped catalog projects no enabled bindings")
	}
	if !validLowercaseHex(capability.Digest, 64) {
		t.Fatalf("capability digest is not a canonical catalog digest: %q", capability.Digest)
	}
	for i, spec := range capability.Bindings {
		if i > 0 && capability.Bindings[i-1].ID >= spec.ID {
			t.Fatalf("capability bindings are not strictly ordered: %+v", capability.Bindings)
		}
	}
	if err := validatePrivateRuntimeCapability(capability); err != nil {
		t.Fatalf("the host's own projection fails the helper boundary: %v", err)
	}
}

func TestValidatePrivateRuntimeCapabilityRefusesMalformedCarriers(t *testing.T) {
	good := dvCapability(t)
	t.Run("a nil binding list is never an empty catalog", func(t *testing.T) {
		if err := validatePrivateRuntimeCapability(PrivateRuntimeCapability{}); err == nil {
			t.Fatal("a nil binding list was admitted")
		}
	})
	t.Run("the explicit unavailable capability is admitted", func(t *testing.T) {
		if err := validatePrivateRuntimeCapability(UnavailableRuntimeCapability()); err != nil {
			t.Fatalf("unavailable capability refused: %v", err)
		}
	})
	t.Run("bindings without a digest are refused", func(t *testing.T) {
		undigested := PrivateRuntimeCapability{Bindings: good.Bindings}
		if err := validatePrivateRuntimeCapability(undigested); err == nil {
			t.Fatal("an undigested catalog was admitted")
		}
	})
	t.Run("a malformed digest is refused", func(t *testing.T) {
		bad := PrivateRuntimeCapability{Digest: "not-a-digest", Bindings: good.Bindings}
		if err := validatePrivateRuntimeCapability(bad); err == nil {
			t.Fatal("a malformed digest was admitted")
		}
	})
	t.Run("out-of-order or duplicated bindings are refused", func(t *testing.T) {
		if len(good.Bindings) < 2 {
			t.Skip("catalog too small to reorder")
		}
		reversed := PrivateRuntimeCapability{Digest: good.Digest}
		for i := len(good.Bindings) - 1; i >= 0; i-- {
			reversed.Bindings = append(reversed.Bindings, good.Bindings[i])
		}
		if err := validatePrivateRuntimeCapability(reversed); err == nil {
			t.Fatal("a reordered catalog was admitted")
		}
		duplicated := PrivateRuntimeCapability{
			Digest:   good.Digest,
			Bindings: []PrivateRuntimeBinding{good.Bindings[0], good.Bindings[0]},
		}
		if err := validatePrivateRuntimeCapability(duplicated); err == nil {
			t.Fatal("a duplicated binding id was admitted")
		}
	})
	t.Run("a fabricated binding is refused", func(t *testing.T) {
		forged := PrivateRuntimeCapability{
			Digest: good.Digest,
			Bindings: []PrivateRuntimeBinding{{
				ID: "forged", Harness: "claude-sdk", Model: "anything",
				Channel: "claude", AuthKind: "claude-oauth", Delivery: "projection",
				ProviderCredentialKeys: []string{"ANTHROPIC_API_KEY"},
			}},
		}
		if err := validatePrivateRuntimeCapability(forged); err == nil {
			t.Fatal("a structurally incoherent binding was admitted")
		}
	})
}

func TestResolveFrozenHomieBinding(t *testing.T) {
	capability := dvCapability(t)
	binding := dvHomieBinding(t)
	harness, id, _ := strings.Cut(binding, "/")

	t.Run("an enabled frozen binding resolves to its exact capability", func(t *testing.T) {
		spec, ok := resolveFrozenHomieBinding(capability, binding)
		if !ok || spec.ID != id || spec.Harness != harness {
			t.Fatalf("resolve(%q) = %+v, %v", binding, spec, ok)
		}
	})
	t.Run("a disabled binding does not resolve", func(t *testing.T) {
		disabled := PrivateRuntimeCapability{Digest: capability.Digest}
		for _, spec := range capability.Bindings {
			if spec.ID != id {
				disabled.Bindings = append(disabled.Bindings, spec)
			}
		}
		if _, ok := resolveFrozenHomieBinding(disabled, binding); ok {
			t.Fatal("a binding absent from the enabled catalog resolved")
		}
	})
	t.Run("the harness half must agree with the catalog", func(t *testing.T) {
		if _, ok := resolveFrozenHomieBinding(capability, "codex/"+id); ok {
			t.Fatal("a binding resolved under the wrong harness family")
		}
	})
	t.Run("an unavailable capability resolves nothing", func(t *testing.T) {
		if _, ok := resolveFrozenHomieBinding(UnavailableRuntimeCapability(), binding); ok {
			t.Fatal("a binding resolved against an unavailable catalog")
		}
	})
	t.Run("malformed frozen bindings resolve nothing", func(t *testing.T) {
		for _, malformed := range []string{"", id, "/" + id, harness + "/", harness + "/" + id + "/x"} {
			if _, ok := resolveFrozenHomieBinding(capability, malformed); ok {
				t.Fatalf("malformed frozen binding %q resolved", malformed)
			}
		}
	})
}
