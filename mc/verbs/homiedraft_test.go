package verbs

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
)

// The runner's own identity: tier homie, its own session, and the allowlist
// `mc homie start` froze for it.
func draftRunner(session string, allow []string) *RunIdentity {
	return &RunIdentity{Tier: "homie", RunID: session, VerbAllowlist: allow}
}

func draftClaim(t *testing.T, db *sql.DB, id *RunIdentity, session string) int64 {
	t.Helper()
	claimed, err := HomieClaim(db, id, session)
	if err != nil {
		t.Fatal(err)
	}
	message, ok := claimed.(map[string]any)["message"].(map[string]any)
	if !ok {
		t.Fatalf("claim returned no message: %v", claimed)
	}
	turn, err := strconv.ParseInt(message["id"].(string), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}

func draftHistory(t *testing.T, db *sql.DB, session string) []map[string]any {
	t.Helper()
	out, err := HomieHistory(db, nil, session, false)
	if err != nil {
		t.Fatal(err)
	}
	return out.(map[string]any)["messages"].([]map[string]any)
}

func TestHomieDraftReplacesTheDraftUntilTheReplyLands(t *testing.T) {
	db := dvSpine(t)
	session := startedSession(t, db, "cli:driver")
	id := draftRunner(session, MaximumHomieAgentVerbs())
	if _, err := HomieSend(db, nil, HomieSendArgs{Session: session, From: "cli:driver", Body: "board?"}); err != nil {
		t.Fatal(err)
	}
	turn := draftClaim(t, db, id, session)

	if _, err := HomieDraft(db, id, HomieDraftArgs{Session: session, To: turn, Body: "four"}); err != nil {
		t.Fatalf("first draft refused: %v", err)
	}
	if _, err := HomieDraft(db, id, HomieDraftArgs{Session: session, To: turn, Body: "four seeded"}); err != nil {
		t.Fatalf("second draft refused: %v", err)
	}
	rows := draftHistory(t, db, session)
	if len(rows) != 1 || rows[0]["draft"] != "four seeded" || rows[0]["reply_to"] != nil {
		t.Fatalf("history before the reply = %v, want the latest draft on the inbound row", rows)
	}

	if _, err := HomieReply(db, id, HomieReplyArgs{Session: session, To: turn, Body: "four seeded, one packaged"}); err != nil {
		t.Fatal(err)
	}
	rows = draftHistory(t, db, session)
	if len(rows) != 2 {
		t.Fatalf("history after the reply has %d rows, want 2", len(rows))
	}
	if rows[0]["draft"] != nil {
		t.Fatalf("inbound row still carries a draft after its reply: %v", rows[0]["draft"])
	}
	if rows[1]["reply_to"] != turn || rows[1]["draft"] != nil {
		t.Fatalf("reply row = %v, want reply_to %d and no draft", rows[1], turn)
	}
	// The reply exists: a later draft is refused.
	if _, err := HomieDraft(db, id, HomieDraftArgs{Session: session, To: turn, Body: "late"}); err == nil {
		t.Fatal("draft on a replied turn was accepted")
	}
}

func TestHomieDraftRefusals(t *testing.T) {
	db := dvSpine(t)
	session := startedSession(t, db, "cli:driver")
	id := draftRunner(session, MaximumHomieAgentVerbs())
	if _, err := HomieSend(db, nil, HomieSendArgs{Session: session, From: "cli:driver", Body: "first"}); err != nil {
		t.Fatal(err)
	}
	unclaimed := draftHistory(t, db, session)[0]["id"].(int64)
	if _, err := HomieDraft(db, id, HomieDraftArgs{Session: session, To: unclaimed, Body: "x"}); err == nil {
		t.Fatal("draft on an unclaimed turn was accepted")
	}
	turn := draftClaim(t, db, id, session)
	if turn != unclaimed {
		t.Fatalf("claimed turn %d, want %d", turn, unclaimed)
	}

	// An oversized body is a usage error, the same bound as a reply body.
	big := strings.Repeat("x", homieBodyMaxBytes+1)
	if _, err := HomieDraft(db, id, HomieDraftArgs{Session: session, To: turn, Body: big}); err == nil {
		t.Fatal("oversized draft was accepted")
	}
	if _, err := HomieReply(db, id, HomieReplyArgs{Session: session, To: turn, Body: big}); err == nil {
		t.Fatal("oversized reply was accepted")
	}
	if _, err := HomieDraft(db, id, HomieDraftArgs{Session: session, To: turn, Body: ""}); err == nil {
		t.Fatal("empty draft was accepted")
	}

	// Another session's runner cannot draft on this turn, from either side.
	other := startedSession(t, db, "cli:other")
	otherID := draftRunner(other, MaximumHomieAgentVerbs())
	if _, err := HomieDraft(db, otherID, HomieDraftArgs{Session: session, To: turn, Body: "x"}); err == nil {
		t.Fatal("a runner drafted on another session by name")
	}
	if _, err := HomieDraft(db, otherID, HomieDraftArgs{Session: other, To: turn, Body: "x"}); err == nil {
		t.Fatal("a runner drafted on another session's turn")
	}
	if _, err := HomieDraft(db, nil, HomieDraftArgs{Session: session, To: turn, Body: "x"}); err == nil {
		t.Fatal("the host drafted")
	}

	// The accept case still holds after every refusal above.
	if _, err := HomieDraft(db, id, HomieDraftArgs{Session: session, To: turn, Body: "ok"}); err != nil {
		t.Fatalf("draft refused: %v", err)
	}
}

// A session frozen before the verb existed lacks it in its allowlist. The
// runner tolerates this refusal; the verb must produce it as a domain
// rejection rather than write the draft.
func TestHomieDraftIsRefusedForASessionFrozenWithoutIt(t *testing.T) {
	db := dvSpine(t)
	allow := []string{"homie.history", "homie.list"}
	startedAny, err := HomieStart(db, nil, HomieStartArgs{From: "cli:driver", Allowlist: &allow})
	if err != nil {
		t.Fatal(err)
	}
	session := startedAny.(HomieStartResult).SessionID
	id := draftRunner(session, allow)
	if _, err := HomieSend(db, nil, HomieSendArgs{Session: session, From: "cli:driver", Body: "hi"}); err != nil {
		t.Fatal(err)
	}
	turn := draftClaim(t, db, id, session)
	_, err = HomieDraft(db, id, HomieDraftArgs{Session: session, To: turn, Body: "x"})
	if err == nil {
		t.Fatal("draft accepted on a session frozen without homie.draft")
	}
	if got := draftHistory(t, db, session)[0]["draft"]; got != nil {
		t.Fatalf("refused draft was stored: %v", got)
	}
	// The reply path is unchanged for such a session.
	if _, err := HomieReply(db, id, HomieReplyArgs{Session: session, To: turn, Body: "hello"}); err != nil {
		t.Fatalf("reply refused: %v", err)
	}
}
