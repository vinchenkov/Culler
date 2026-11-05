package verbs

import (
	"crypto/sha256"
	"fmt"
	"mc/dispatch"
	"strings"
	"testing"
)

func TestWorkerEditPreservesOtherRoleInput(t *testing.T) {
	// Digests cover the directive, skill pointers, and generated explanations at destination a7292cce before the Worker edits.
	want := map[dispatch.Role]string{
		dispatch.RoleStrategist: "870905254d2b9d0ed86edda4d4cb929a19663b99a6a57f022495f0a72e75d2a5",
		dispatch.RoleArbiter:    "6c1773e3f1904fb34759c02e4777c0094753c0ee1416b94d857f423eee48f922",
		dispatch.RoleScribe:     "fa3ee90978439eaaf2244123e1a957cd231f9eba3a7e448b2916f14c01c9701e",
		dispatch.RoleVerifier:   "4d52ba15ff7d52c114f9f7b3ed1d0bf581e344e905821ec5a2338ca52d4c7715",
		dispatch.RolePackager:   "12a7e92e3a8bddf987ba7f07a20143ef4188414e659213bbc0f1ca44b72168ff",
	}
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	for _, role := range allBriefRoles() {
		if role == dispatch.RoleWorker {
			continue
		}
		in := briefInputs{}
		if role == dispatch.RoleScribe {
			in = scribeBriefInputs(wrCommitB)
		}
		brief := briefFor(t, db, role, briefSubject(role, 1), in)
		var input strings.Builder
		input.WriteString(brief.Directive)
		for _, part := range brief.Parts {
			fmt.Fprintf(&input, "\n%s\n%s", part.Heading, part.Explanation)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256([]byte(input.String()))); got != want[role] {
			t.Errorf("%s input changed: got %s, want %s", role, got, want[role])
		}
	}
}
