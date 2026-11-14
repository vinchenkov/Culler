//go:build test_fake_routing

package main

import "io"

func brokerAcceptance(_ []string, _, _ io.Writer) int { return 2 }
