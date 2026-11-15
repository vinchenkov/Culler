package main

import (
	"io"
	"mc/verbs"
)

func runPrivateUpdateReconcile(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || !privateHelperScopeOK() {
		return writeVerbError(stdout, stderr, verbs.Usagef("The update requires the fixed database scope."))
	}
	result, err := verbs.ReconcileUpdate(helperSpinePath())
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := writeJSON(stdout, result); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	return 0
}
