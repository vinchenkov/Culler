package main

import (
	"encoding/json"
	"fmt"
	"io"

	"mc/verbs"
)

func decodeSpineTransferRequest(stdin io.Reader) (verbs.SpineTransferRequest, error) {
	var req verbs.SpineTransferRequest
	dec := json.NewDecoder(io.LimitReader(stdin, 64*1024+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, verbs.Usagef("invalid private spine-transfer frame: %v", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return req, verbs.Usagef("invalid private spine-transfer frame: %v", err)
	}
	return req, nil
}

func runPrivateSpineBackup(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return writeVerbError(stdout, stderr, verbs.Usagef("usage: mc __backup-spine"))
	}
	if !privateHelperScopeOK() {
		return writeVerbError(stdout, stderr, verbs.Domainf("private backup-spine requires the helper's fixed spine scope"))
	}
	req, err := decodeSpineTransferRequest(stdin)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := verbs.ExportSpineBackup(helperSpinePath(), req, releaseBuildID, gatewayControlVersion, configSchemaVersion, stdout); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	return 0
}

func runPrivateSpineRestore(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return writeVerbError(stdout, stderr, verbs.Usagef("usage: mc __restore-spine"))
	}
	if !privateHelperScopeOK() {
		return writeVerbError(stdout, stderr, verbs.Domainf("private restore-spine requires the helper's fixed spine scope"))
	}
	header, err := verbs.RestoreSpine(helperSpinePath(), releaseBuildID, gatewayControlVersion, configSchemaVersion, stdin)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := writeJSON(stdout, header); err != nil {
		fmt.Fprintln(stderr, "mc:", err)
		return 2
	}
	return 0
}
