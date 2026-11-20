package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"mc/verbs"
)

// G-F build-identity coverage for the host→helper write entries. Structured
// frames enforce release/control/schema equality child-side (pinned by the
// onboard, transfer, dispatch, and operator-transport suites); argv entries
// are preceded by the per-contact ensure() reconciliation. These cases pin
// the two seams the existing suites had not covered: the system-state gate
// and the attachment RPC frame-state boundaries.

func TestSystemStateDelegationRefusesAnUnreconciledHelper(t *testing.T) {
	t.Setenv("MC_HELPER", "mc-helper-0123456789ab")
	t.Setenv("MC_SPINE", "")
	previous := ensureSystemStateHelper
	defer func() { ensureSystemStateHelper = previous }()
	reached := false
	ensureSystemStateHelper = func() (string, error) {
		reached = true
		return "", errors.New("stale helper image")
	}
	if _, err := systemSpineCall("proxy-lifetimes", struct{}{}); err == nil || !strings.Contains(err.Error(), "ensure warm helper") {
		t.Fatalf("system-state delegation ran without the helper gate: %v", err)
	}
	if !reached {
		t.Fatal("the delegation branch skipped the helper reconciliation")
	}
}

// TestDelegationExecsSitBehindTheReconciliationGate pins the call-site order
// that the identity guarantee depends on: every path that execs into the
// warm helper first calls the ensure gate. The fake-routing build cannot
// start docker, so the order is guarded at the source.
func TestDelegationExecsSitBehindTheReconciliationGate(t *testing.T) {
	for _, probe := range []struct{ file, gate, spawn string }{
		{"systemstatus.go", "ensureSystemStateHelper()", `"docker"`},
		{"main.go", "ensureRuntimeHelper()", "delegate(args, stdin, stdout, stderr)"},
	} {
		body, err := os.ReadFile(probe.file)
		if err != nil {
			t.Fatal(err)
		}
		source := string(body)
		gate := strings.Index(source, probe.gate)
		spawn := strings.Index(source, probe.spawn)
		if gate < 0 || spawn < 0 || gate > spawn {
			t.Fatalf("%s: helper reconciliation must precede the delegation spawn (gate=%d spawn=%d)", probe.file, gate, spawn)
		}
	}
}

func TestAttachmentSpineRPCBoundaries(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	spine := filepath.Join(home, "spine.db")
	t.Setenv("MC_HOME", home)
	if _, err := verbs.Init(verbs.InitArgs{Spine: spine, Project: "ws-test", WorkspaceRoot: filepath.Join(home, "workspace")}); err != nil {
		t.Fatal(err)
	}
	var active atomic.Int64
	channel, err := attachmentPipeRPCDialer(t, spine, &active)()
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	for _, probe := range []struct {
		request attachmentSpineRPCRequest
		want    string
	}{
		{attachmentSpineRPCRequest{Version: 2, Operation: "begin"}, "version mismatch"},
		{attachmentSpineRPCRequest{Version: 1, Operation: "commit"}, "commit is malformed"},
		{attachmentSpineRPCRequest{Version: 1, Operation: "rollback"}, "rollback is malformed"},
		{attachmentSpineRPCRequest{Version: 1, Operation: "begin", Statement: "SELECT 1"}, "begin is malformed"},
	} {
		response, err := channel.Exchange(context.Background(), probe.request)
		if err != nil {
			t.Fatalf("%s: %v", probe.request.Operation, err)
		}
		if !strings.Contains(response.Error, probe.want) {
			t.Fatalf("%s boundary: error = %q, want %q", probe.request.Operation, response.Error, probe.want)
		}
	}
}
