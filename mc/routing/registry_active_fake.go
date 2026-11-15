//go:build test_fake_routing

package routing

// ActiveRegistry adds the deterministic fake family only to explicitly
// tagged test binaries. Production builds cannot resolve it.
func ActiveRegistry() (Registry, bool) {
	r := ProductionRegistry()
	r["fake"] = "fake"
	return r, true
}

func ActiveBinding(id string) (BindingSpec, bool) {
	if id == "fake" {
		return BindingSpec{ID: "fake", Harness: "fake", Channel: "fake", Delivery: ProviderProxy}, true
	}
	return ProductionBinding(id)
}

func ActiveBindings() map[string]BindingSpec {
	out := ProductionBindings()
	out["fake"] = BindingSpec{ID: "fake", Harness: "fake", Channel: "fake", Delivery: ProviderProxy}
	return out
}
