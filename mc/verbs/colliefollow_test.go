package verbs

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// `mc collie history --follow` reads the conversation back and needs no
// delivery loop. It is the CLI's read channel over the conversation rows.
// Nobody built it, so every caller writes the poll by hand.
//
// This is a repair, so it adds the flag and nothing beside it.

// followFast shrinks the wait so the expiry path is proven in milliseconds
// rather than two minutes. Restores on cleanup so no other test inherits it.
func followFast(t *testing.T) {
	t.Helper()
	deadline, interval := collieFollowDeadline, collieFollowInterval
	collieFollowDeadline = 150 * time.Millisecond
	collieFollowInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		collieFollowDeadline, collieFollowInterval = deadline, interval
	})
}

// Returns its error rather than calling t.Fatal: it runs on a goroutine, where
// Fatal only Goexits and would leave the test blocked on a channel that is
// never closed — a hang instead of a failure.
func followReply(db *sql.DB, session, body string) error {
	// The real order the runner uses: claim the pending turn, then reply to it.
	// A reply to an unclaimed turn is refused, so the fixture must do
	// what the runner does rather than write the row behind its back.
	id := &RunIdentity{Tier: "collie", RunID: session}
	claimed, err := CollieClaim(db, id, session)
	if err != nil {
		return err
	}
	turn, ok := claimed.(map[string]any)
	if !ok {
		return fmt.Errorf("claim returned %T, want a map", claimed)
	}
	message, ok := turn["message"].(map[string]any)
	if !ok {
		return fmt.Errorf("claimed turn carries no message: %v", turn)
	}
	// The claim renders its row through the generic map path, which hands every
	// column back as a string. Parse rather than assert a type the verb does
	// not promise.
	raw, ok := message["id"].(string)
	if !ok {
		return fmt.Errorf("claimed message id is %T: %v", message["id"], message)
	}
	inbound, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("claimed message id %q is not a decimal: %w", raw, err)
	}
	_, err = CollieReply(db, id, CollieReplyArgs{Session: session, To: inbound, Body: body})
	return err
}

func followMessages(t *testing.T, out any) []map[string]any {
	t.Helper()
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("history returned %T, want a map", out)
	}
	rows, ok := m["messages"].([]map[string]any)
	if !ok {
		t.Fatalf("history messages are %T", m["messages"])
	}
	return rows
}

// The whole point: a reply that lands DURING the wait wakes it, and the
// transcript that comes back already contains that reply. A snapshot read
// before the wait would have missed it.
func TestCollieHistoryFollowReturnsWhenTheReplyLands(t *testing.T) {
	followFast(t)
	collieFollowDeadline = 5 * time.Second // long enough that the writer wins
	db := dvSpine(t)
	session := startedSession(t, db, "cli:driver")
	if _, err := CollieSend(db, nil, CollieSendArgs{
		Session: session, From: "cli:driver", Body: "what is on the board?",
	}); err != nil {
		t.Fatal(err)
	}

	wrote := make(chan error, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		wrote <- followReply(db, session, "four seeded, one packaged")
	}()

	started := time.Now()
	out, err := CollieHistory(db, nil, session, true)
	if err != nil {
		t.Fatalf("follow refused: %v", err)
	}
	if err := <-wrote; err != nil {
		t.Fatalf("the reply the follow was waiting for could not be written: %v", err)
	}
	if waited := time.Since(started); waited >= 5*time.Second {
		t.Fatalf("follow waited %s — it ran the deadline out instead of waking", waited)
	}
	rows := followMessages(t, out)
	last := rows[len(rows)-1]
	if last["direction"] != "reply" || !strings.Contains(last["body"].(string), "four seeded") {
		t.Fatalf("last message = %v, want the reply that woke the follow", last)
	}
}

// An inbound row is the caller's OWN message. Waking on it would return
// instantly and follow nothing, which is the failure that makes a hand-written
// poll loop subtly wrong in the first place.
func TestCollieHistoryFollowIgnoresTheCallersOwnMessage(t *testing.T) {
	followFast(t)
	db := dvSpine(t)
	session := startedSession(t, db, "cli:driver")
	if _, err := CollieSend(db, nil, CollieSendArgs{
		Session: session, From: "cli:driver", Body: "first",
	}); err != nil {
		t.Fatal(err)
	}

	go func() {
		time.Sleep(20 * time.Millisecond)
		// A SECOND inbound message, not a reply. Nothing has answered yet.
		_, _ = CollieSend(db, nil, CollieSendArgs{
			Session: session, From: "cli:driver", Body: "second",
		})
	}()

	started := time.Now()
	if _, err := CollieHistory(db, nil, session, true); err != nil {
		t.Fatalf("follow refused: %v", err)
	}
	if waited := time.Since(started); waited < 100*time.Millisecond {
		t.Fatalf("follow returned after %s — an inbound row woke it", waited)
	}
}

// Expiry is not an error. The caller re-invokes, which is the same loop it
// would have written by hand at one query per two minutes instead of one per
// second — and a quiet session reads as an unchanged transcript, not a hang.
func TestCollieHistoryFollowExpiresIntoAnOrdinaryTranscript(t *testing.T) {
	followFast(t)
	db := dvSpine(t)
	session := startedSession(t, db, "cli:driver")
	if _, err := CollieSend(db, nil, CollieSendArgs{
		Session: session, From: "cli:driver", Body: "anyone there?",
	}); err != nil {
		t.Fatal(err)
	}

	out, err := CollieHistory(db, nil, session, true)
	if err != nil {
		t.Fatalf("an expired follow must not be an error: %v", err)
	}
	rows := followMessages(t, out)
	if len(rows) != 1 || rows[0]["direction"] != "inbound" {
		t.Fatalf("expired follow returned %v, want the transcript as it stands", rows)
	}
	if out.(map[string]any)["status"] != "active" {
		t.Fatalf("expired follow reported status %v", out.(map[string]any)["status"])
	}
}

// An ended session will never produce another reply. Waiting the deadline out
// would report a hang that is really a terminal.
func TestCollieHistoryFollowReturnsAtOnceOnAnEndedSession(t *testing.T) {
	followFast(t)
	collieFollowDeadline = 10 * time.Second // would dominate if it were consulted
	db := dvSpine(t)
	session := startedSession(t, db, "cli:driver")
	if _, err := CollieEnd(db, nil, session, "done"); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	out, err := CollieHistory(db, nil, session, true)
	if err != nil {
		t.Fatalf("follow on an ended session refused: %v", err)
	}
	if waited := time.Since(started); waited > time.Second {
		t.Fatalf("follow waited %s on an ended session", waited)
	}
	if got := out.(map[string]any)["status"]; got != "ended" {
		t.Fatalf("status = %v, want ended", got)
	}
}

// A session that ends WHILE the follow waits reports the status it observed,
// not the one it started with.
func TestCollieHistoryFollowReportsASessionThatEndsUnderIt(t *testing.T) {
	followFast(t)
	collieFollowDeadline = 5 * time.Second
	db := dvSpine(t)
	session := startedSession(t, db, "cli:driver")
	if _, err := CollieSend(db, nil, CollieSendArgs{
		Session: session, From: "cli:driver", Body: "still there?",
	}); err != nil {
		t.Fatal(err)
	}

	ended := make(chan error, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, err := CollieEnd(db, nil, session, "exited")
		ended <- err
	}()

	started := time.Now()
	out, err := CollieHistory(db, nil, session, true)
	if err != nil {
		t.Fatalf("follow refused: %v", err)
	}
	if err := <-ended; err != nil {
		t.Fatalf("the end the follow was waiting for could not be written: %v", err)
	}
	if waited := time.Since(started); waited >= 5*time.Second {
		t.Fatalf("follow waited %s instead of noticing the session end", waited)
	}
	if got := out.(map[string]any)["status"]; got != "ended" {
		t.Fatalf("status = %v, want the ended it observed", got)
	}
}

// A Collie agent following its own session would block waiting for the reply
// that only its own turn can produce — a self-deadlock that spends the turn on
// nothing. The plain read stays open to it; only the waiting is host-only.
func TestCollieHistoryFollowIsRefusedForAnAgentCaller(t *testing.T) {
	followFast(t)
	db := dvSpine(t)
	session := startedSession(t, db, "cli:driver")
	id := &RunIdentity{Tier: "collie", RunID: session}

	_, err := CollieHistory(db, id, session, true)
	if err == nil {
		t.Fatal("a Collie agent was allowed to follow its own session")
	}
	if !strings.Contains(err.Error(), "host scope") {
		t.Fatalf("refusal = %v, want it to name the scope", err)
	}
	// And the gate is specific to following. The plain read still runs the
	// agent's own allowlist checks — which this bare fixture does not satisfy —
	// but it must never fail for the follow reason.
	if _, err := CollieHistory(db, id, session, false); err != nil && strings.Contains(err.Error(), "host scope") {
		t.Fatalf("the plain agent read was refused by the follow gate: %v", err)
	}
}
