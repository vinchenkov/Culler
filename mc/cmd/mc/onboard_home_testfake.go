//go:build test_fake_routing

package main

import "io"

func brokerOnboardHome(args []string, _ bool, stdout, stderr io.Writer) int {
	return runLocal(args, nil, stdout, stderr)
}

func brokerOnboardContainer(args []string, stdout, stderr io.Writer) int {
	return runLocal(args, nil, stdout, stderr)
}

func brokerOnboardRuntimeAuth(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runLocal(args, nil, stdout, stderr)
}

func brokerOnboardVerify(args []string, stdout, stderr io.Writer) int {
	return runLocal(args, nil, stdout, stderr)
}
