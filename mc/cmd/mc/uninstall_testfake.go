//go:build test_fake_routing

package main

import (
	"io"

	"mc/verbs"
)

// The fake-routing build carries no launchd, no production image, and no
// deployment-derived Docker objects, so there is nothing here for uninstall to
// remove. It refuses rather than pretending to succeed: a test build that
// reported a completed uninstall would be a fixture that passes by agreeing
// with itself, since no test could tell its success from a real removal.
//
// The host-scope fence is enforced FIRST and in this build too, deliberately.
// It is the property that stops a prompt-injected Homie from destroying the
// deployment, so it must not be reachable only through the production path —
// and enforcing it here is what lets the CLI suite, which compiles this build,
// prove the refusal without Docker.
func brokerUninstall(args []string, stdout, stderr io.Writer) int {
	id, err := verbs.LoadIdentity()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := verbs.RequireHostScope(id, "mc __uninstall"); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	return writeVerbError(stdout, stderr, verbs.Domainf("mc __uninstall is a production-build verb"))
}
