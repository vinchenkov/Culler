package main

import (
	"fmt"
	"os"
	"path/filepath"

	"mc/deployment"
)

// configuredCanonicalHome is shared by production brokers and the small set
// of host-local private verbs. Keep it in the always-built command surface so
// test-tag binaries enforce the same canonical MC_HOME boundary.
func configuredCanonicalHome() (string, error) {
	home := os.Getenv("MC_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve default MC_HOME: %w", err)
		}
		home = filepath.Join(userHome, ".culler")
	}
	return deployment.CanonicalHome(home)
}
