package main

import (
	"encoding/json"
	"fmt"
	"io"

	"mc/verbs"
)

func runPrivateDoctorRuntime(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return writeVerbError(stdout, stderr, verbs.Usagef("usage: mc __doctor-runtime"))
	}
	if !privateHelperScopeOK() {
		return writeVerbError(stdout, stderr, verbs.Domainf("private doctor-runtime requires the helper's fixed spine scope"))
	}
	var req verbs.DoctorRuntimeRequest
	dec := json.NewDecoder(io.LimitReader(stdin, 64*1024+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("invalid private doctor-runtime frame: %v", err))
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return writeVerbError(stdout, stderr, verbs.Usagef("invalid private doctor-runtime frame: trailing data"))
	}
	result, err := verbs.DoctorRuntime(helperSpinePath(), req, releaseBuildID, gatewayControlVersion, configSchemaVersion)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := writeJSON(stdout, result); err != nil {
		fmt.Fprintln(stderr, "mc:", err)
		return 2
	}
	return 0
}
