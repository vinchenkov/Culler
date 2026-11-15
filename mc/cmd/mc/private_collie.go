package main

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"mc/verbs"
)

// runPrivateCollieStart owns the helper-side SQLite write for a host-resolved
// Collie route. It deliberately accepts only a canonical private frame, not
// normal CLI flags, so the helper never consults host config.toml or routing.md.
func runPrivateCollieStart(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 3 || args[1] != "--deadline-unix-ms" {
		return writeVerbError(stdout, stderr, verbs.Usagef("usage: mc __collie-start --deadline-unix-ms <unix-ms>"))
	}
	if !privateHelperScopeOK() {
		return writeVerbError(stdout, stderr, verbs.Domainf("private Collie start requires the helper's fixed spine scope"))
	}
	deadlineMillis, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("private Collie start deadline is invalid"))
	}
	deadlineAt := time.UnixMilli(deadlineMillis)
	remaining, ok := helperDeadlineBudget(deadlineAt, time.Now())
	if !ok {
		return privateHelperTimeoutExitCode
	}
	deadline := time.AfterFunc(remaining, func() { os.Exit(privateHelperTimeoutExitCode) })
	defer deadline.Stop()
	var request verbs.PrivateCollieStartRequest
	if err := readPrivateFrame(stdin, &request); err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("invalid private Collie start frame"))
	}
	result, err := withSpine(func(db *sql.DB) (any, error) {
		return verbs.CollieStartPrivateDB(db, request)
	})
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := writePrivateFrame(stdout, result); err != nil {
		fmt.Fprintln(stderr, "mc: write private Collie start frame:", err)
		return 2
	}
	return 0
}
