//go:build !test_fake_routing

package verbs

import (
	"strings"
	"testing"

	"mc/routing"
)

func TestProductionPrivateHelperRejectsExactFakeAuthority(t *testing.T) {
	digest := strings.Repeat("ab", 32)
	got := validatePrivateAttestation(PrivateDispatchAttestation{
		RuntimeDigest: digest,
		RoutingDigest: digest,
		Harness:       "fake",
		Binding:       "fake",
		RuntimeBinding: privateRuntimeBindingFromSpec(routing.BindingSpec{
			ID: "fake", Harness: "fake", Channel: "fake",
			Delivery: routing.ProviderProxy,
		}),
		MountPlan: &PrivateDispatchMountPlan{Version: 1, Entries: []PrivateDispatchMountEntry{}},
	})
	if got == nil {
		t.Fatal("production helper accepted a fake runtime authority carrier")
	}
}

// A carried capability is the operator's ENABLED CATALOG, and the helper
// validates every entry in it. Refusing the whole carrier because one entry
// names a harness this build cannot serve meant a deployment could not dispatch
// on the bindings it CAN serve — the e2e's real-image lane hit this with an
// unused `fake` entry, and an operator hits it with any catalog holding one
// unserviceable binding. Serviceability belongs at selection, where the
// attestation already applies it (TestProductionPrivateHelperRejectsExactFake-
// Authority above pins that half); the carrier owes structural validity only.
func TestProductionCarrierToleratesAnUnselectedUnserviceableBinding(t *testing.T) {
	digest := strings.Repeat("ab", 32)
	fake := privateRuntimeBindingFromSpec(routing.BindingSpec{
		ID: "fake", Harness: "fake", Channel: "fake",
		Delivery: routing.ProviderProxy,
	})
	// Derived exactly as production projects it, never hand-built: the shape
	// this validator compares against is `routing.EnabledBindings`' own output,
	// and a restated literal is how carrier comparisons quietly diverge.
	real := privateRuntimeBindingFromSpec(testEndpointBinding())
	// Strictly ordered by binding id, as the projection emits it.
	capability := PrivateRuntimeCapability{
		Digest: digest, Bindings: []PrivateRuntimeBinding{real, fake},
	}
	if err := validatePrivateRuntimeCapability(capability); err != nil {
		t.Fatalf("carrier holding an unselected unserviceable binding was refused: %v", err)
	}
	// The structural fence must survive the narrowing: a fake entry that is not
	// the canonical fake spec is still malformed, selected or not.
	bent := fake
	bent.Channel = "codex"
	if err := validatePrivateRuntimeCapability(PrivateRuntimeCapability{
		Digest: digest, Bindings: []PrivateRuntimeBinding{real, bent},
	}); err == nil {
		t.Fatal("carrier accepted a malformed fake binding")
	}
}

func TestRuntimeCapabilityRetainsAndValidatesEffort(t *testing.T) {
	spec := testEndpointBinding()
	spec.Effort = "high"
	binding := privateRuntimeBindingFromSpec(spec)
	if binding.Effort != "high" || bindingSpecFromPrivate(binding).Effort != "high" {
		t.Fatal("The capability lost effort.")
	}
	if err := validatePrivateRuntimeBindingShape(binding, binding.Harness, binding.ID); err != nil {
		t.Fatal(err)
	}
	binding.Effort = "unknown"
	if err := validatePrivateRuntimeBindingShape(binding, binding.Harness, binding.ID); err == nil {
		t.Fatal("The capability accepted invalid effort.")
	}
}
