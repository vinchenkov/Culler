package main

import (
	"slices"
	"testing"
)

// The delegation handoff is the only carrier of the operator fence across
// the process boundary: an armed request must travel to the helper inside
// the argv itself, because the helper starts with an empty process context.
// If this rewrite ever regresses, delegated operator writes run unguarded.

func TestDelegateArgvCarriesAnArmedOperatorRequest(t *testing.T) {
	previous := activeOperatorRequest
	defer func() { activeOperatorRequest = previous }()

	activeOperatorRequest = "a22-fence"
	armed := delegateArgv("shz-helper", []string{"project", "pause", "ws-test"}, nil)
	want := []string{"exec", "shz-helper", "mc", "__operator-forward", "a22-fence", "--", "project", "pause", "ws-test"}
	if !slices.Equal(armed, want) {
		t.Fatalf("armed delegation lost the receipt: %v", armed)
	}

	activeOperatorRequest = ""
	plain := delegateArgv("shz-helper", []string{"project", "pause", "ws-test"}, nil)
	want = []string{"exec", "shz-helper", "mc", "project", "pause", "ws-test"}
	if !slices.Equal(plain, want) {
		t.Fatalf("an unarmed delegation invented a receipt: %v", plain)
	}
}
