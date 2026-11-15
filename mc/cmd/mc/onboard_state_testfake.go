//go:build test_fake_routing

package main

import "io"

func brokerOnboardState(args []string, stdout, stderr io.Writer) int {
	return delegate(args, nil, stdout, stderr)
}
