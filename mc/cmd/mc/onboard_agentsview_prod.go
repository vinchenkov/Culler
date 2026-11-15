//go:build !test_fake_routing

package main

import "mc/deployment"

func onboardAgentsViewPin() (deployment.AgentsViewPin, error) {
	return deployment.LoadAgentsViewPin()
}
