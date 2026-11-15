package main

import (
	"io"

	"mc/verbs"
)

func runPrivateReleaseDatabase(args []string, _ io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 || !privateHelperScopeOK() {
		return writeVerbError(stdout, stderr, verbs.Usagef("The release check requires the fixed database scope."))
	}
	result, err := verbs.InspectReleaseDatabase(helperSpinePath())
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := writeJSON(stdout, result); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	return 0
}
