package main

import (
	"encoding/json"
	"fmt"
	"io"

	"mc/verbs"
)

// runPrivateOnboardSpine is the path-free Home crossing. Production reaches
// it only inside the fixed long-lived helper, whose sole data mount is the
// deployment's named spine volume. Host paths and config have no frame field.
func runPrivateOnboardSpine(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return writeVerbError(stdout, stderr, verbs.Usagef("usage: mc __onboard-spine"))
	}
	if !privateHelperScopeOK() {
		return writeVerbError(stdout, stderr,
			verbs.Domainf("private onboard-spine requires the helper's fixed spine scope"))
	}
	var req verbs.OnboardSpineRequest
	dec := json.NewDecoder(io.LimitReader(stdin, 64*1024+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("invalid private onboard-spine frame: %v", err))
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return writeVerbError(stdout, stderr, verbs.Usagef("invalid private onboard-spine frame: %v", err))
	}
	result, err := verbs.OnboardSpine(helperSpinePath(), req)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := writeJSON(stdout, result); err != nil {
		fmt.Fprintln(stderr, "mc:", err)
		return 2
	}
	return 0
}
