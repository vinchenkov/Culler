//go:build !test_fake_routing

package main

import "testing"

func TestProviderImageIgnoresTheFixtureSelectorInProduction(t *testing.T) {
	t.Setenv("CULLER_TEST_PROVIDER_IMAGE", "mc-real-e2e")
	if image, err := testProviderImage(); err != nil || image != "" {
		t.Fatal("The production binary accepted the provider fixture image")
	}
}
