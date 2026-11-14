package main

import (
	"fmt"
	"io"
	"time"

	"mc/verbs"
)

func runPrivateAcceptanceSnapshot(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return writeVerbError(stdout, stderr, verbs.Usagef("usage: mc __acceptance-snapshot"))
	}
	if !privateHelperScopeOK() {
		return writeVerbError(stdout, stderr,
			verbs.Domainf("private acceptance snapshot requires the helper's fixed spine scope"))
	}
	// AcceptanceRuntimeSnapshot only reads, so it opens the spine through the
	// one shared read-only path.
	db, err := verbs.OpenSpineRead(helperSpinePath())
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	defer db.Close()
	result, err := verbs.AcceptanceRuntimeSnapshot(db, time.Now())
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := writeJSON(stdout, result); err != nil {
		fmt.Fprintln(stderr, "mc:", err)
		return 2
	}
	return 0
}
