//go:build !test_fake_routing

package routing

import "testing"

// Takeover finding: the CLI suite compiles mc with -tags test_fake_routing,
// so no test exercised ActiveRegistry in an untagged (production)
// compilation — a one-line regression adding the fake family to
// registry_active.go would have failed zero tests. This pins it.
func TestActiveRegistryUntaggedExcludesFake(t *testing.T) {
	reg, fakeAllowed := ActiveRegistry()
	if fakeAllowed {
		t.Fatal("untagged ActiveRegistry reports fake routes as allowed")
	}
	if _, ok := reg["fake"]; ok {
		t.Fatal("untagged ActiveRegistry resolves the fake harness family")
	}
}
