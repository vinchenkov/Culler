//go:build test_fake_routing

package main

import "io"

func runCuller(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	_, done, code := cullerUsage(args, stdout, stderr)
	if done {
		return code
	}
	return 2
}
