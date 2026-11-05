//go:build test_fake_routing

package main

import "io"

func runTerminalSetup(_ []string, _ io.Reader, _, _ io.Writer) int { return 2 }
