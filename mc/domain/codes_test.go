package domain_test

import (
	"testing"

	"mc/domain"
)

// The Homie runner reads this code off the wire. runner/homie-runner/main.ts
// pins the same literal; a rename on one side fails one of the two tests.
func TestSessionInactiveCodeIsTheWireLiteral(t *testing.T) {
	if domain.CodeSessionInactive != "session-inactive" {
		t.Fatalf("CodeSessionInactive = %q, want the wire literal \"session-inactive\"", domain.CodeSessionInactive)
	}
}
