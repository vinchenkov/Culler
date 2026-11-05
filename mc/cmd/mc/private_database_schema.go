package main

import (
	"io"

	"mc/verbs"
)

// runPrivateDatabaseSchema compares the database at the fixed helper scope
// with the schema of this build. It opens the database read-only.
func runPrivateDatabaseSchema(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return writeVerbError(stdout, stderr, verbs.Usagef("usage: mc __database-schema"))
	}
	if !privateHelperScopeOK() {
		return writeVerbError(stdout, stderr, verbs.Domainf("The database check requires the fixed database scope."))
	}
	result, err := verbs.InspectDeployedDatabase(helperSpinePath())
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := writeJSON(stdout, result); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	return 0
}
