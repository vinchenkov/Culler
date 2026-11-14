//go:build test_fake_routing

package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var testProviderImagePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*(?::[A-Za-z0-9_.-]+|@sha256:[0-9a-f]{64})?$`)

func testProviderImage() (string, error) {
	image := os.Getenv("CULLER_TEST_PROVIDER_IMAGE")
	if image == "" {
		return "", nil
	}
	name := strings.Split(strings.Split(image, "@")[0], ":")[0]
	if !testProviderImagePattern.MatchString(image) || name == "mc-prod" || name == "mc-project" {
		return "", fmt.Errorf("The provider test requires an isolated task image.")
	}
	return image, nil
}
