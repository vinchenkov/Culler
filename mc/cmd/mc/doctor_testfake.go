//go:build test_fake_routing

package main

import "io"

func brokerDoctor(args []string, stdout, stderr io.Writer) int {
	return runLocal(args, nil, stdout, stderr)
}
