//go:build test_fake_routing

package main

import (
	"os"

	"mc/deployment"
)

// The test build can read a synthetic pin, so CLI tests install a fixture
// executable in place of the real release.
func onboardAgentsViewPin() (deployment.AgentsViewPin, error) {
	path := os.Getenv("MC_TEST_AGENTSVIEW_PIN")
	if path == "" {
		return deployment.LoadAgentsViewPin()
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return deployment.AgentsViewPin{}, err
	}
	return deployment.ParseAgentsViewPin(body)
}
