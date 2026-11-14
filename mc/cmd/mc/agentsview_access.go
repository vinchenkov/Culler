package main

import (
	"io"

	"mc/verbs"
)

// runAgentsViewAccess supplies credentials to the local proxy on the host.
func runAgentsViewAccess(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		return writeVerbError(stdout, stderr, verbs.Usagef("usage: mc __agentsview-access"))
	}
	identity, err := verbs.LoadIdentity()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := verbs.RequireHostScope(identity, "mc __agentsview-access"); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("resolve MC_HOME: %v", err))
	}
	result, err := verbs.AgentsViewAccess(home)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := writeJSON(stdout, result); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	return 0
}
