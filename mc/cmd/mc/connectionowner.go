package main

import (
	"context"
	"io"
	"time"

	"mc/deployment"
	"mc/verbs"
)

// The resident holds stdin open until its provider exchange and storage finish.
func runConnectionOwner(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	identity, err := verbs.LoadIdentity()
	if err == nil {
		err = verbs.RequireHostScope(identity, "connection owner")
	}
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if len(args) != 2 {
		return writeVerbError(stdout, stderr, verbs.Usagef("The connection owner requires one connection."))
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	release, err := deployment.AcquireConnectionOwner(ctx, home, args[1])
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Domainf("The connection owner is unavailable."))
	}
	defer release()
	if _, err := io.WriteString(stdout, "{\"held\":true}\n"); err != nil {
		return 1
	}
	_, err = io.Copy(io.Discard, stdin)
	if err != nil {
		return 1
	}
	return 0
}
