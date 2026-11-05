//go:build test_fake_routing

package main

import "testing"

func TestProviderImageUsesAnIsolatedTaskImage(t *testing.T) {
	for _, image := range []string{"mc-prod", "mc-prod:latest", "mc-project", "mc-project:latest", "../image", " image"} {
		t.Setenv("CULLER_TEST_PROVIDER_IMAGE", image)
		if _, err := testProviderImage(); err == nil {
			t.Fatalf("The provider test accepted the unsafe image %q", image)
		}
	}
	t.Setenv("CULLER_TEST_PROVIDER_IMAGE", "mc-real-e2e")
	if image, err := configuredProductionImage(); err != nil || image != "mc-real-e2e" {
		t.Fatalf("The provider test lost its task image: %q %v", image, err)
	}
}
