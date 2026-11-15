//go:build test_fake_routing

package main

import (
	"io"

	"mc/verbs"
)

func runRedeployCheck(_ []string, stdout, stderr io.Writer) int {
	return writeVerbError(stdout, stderr, verbs.Usagef("The redeploy database check requires a production build."))
}
