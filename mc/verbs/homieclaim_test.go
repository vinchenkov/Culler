package verbs

import (
	"errors"
	"testing"

	"mc/domain"
)

// The runner polls `mc homie claim` for the life of its container. A refusal
// on an ended session must carry a stable code, so the runner can end on it
// instead of polling an inert session until the container is removed.
func TestHomieClaimOnAnEndedSessionCarriesTheInactiveCode(t *testing.T) {
	db := dvSpine(t)
	session := startedSession(t, db, "cli:driver")
	id := &RunIdentity{Tier: "homie", RunID: session}
	if _, err := HomieClaim(db, id, session); err != nil {
		t.Fatalf("claim on the active session: %v", err)
	}
	if _, err := HomieEnd(db, nil, session, "exited"); err != nil {
		t.Fatal(err)
	}

	_, err := HomieClaim(db, id, session)
	var rejection *DomainError
	if !errors.As(err, &rejection) {
		t.Fatalf("claim on the ended session: err = %v, want a DomainError", err)
	}
	if rejection.Code != domain.CodeSessionInactive {
		t.Fatalf("code = %q, want %q", rejection.Code, domain.CodeSessionInactive)
	}
	if rejection.Msg == "" {
		t.Fatal("the rejection carries no message")
	}
}
