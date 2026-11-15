//go:build !test_fake_routing

package routing

// ActiveRegistry is the production routing catalog.
func ActiveRegistry() (Registry, bool) { return ProductionRegistry(), false }

func ActiveBinding(id string) (BindingSpec, bool) { return ProductionBinding(id) }
func ActiveBindings() map[string]BindingSpec      { return ProductionBindings() }
