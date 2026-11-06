package verbs

import (
	"database/sql"
	"strings"
	"testing"
)

// The `cli` surface participates as just another binding: `mc collie send`
// binds as `cli` and resumes the bound session if it ended. The `list` verb
// includes the ended-but-resumable sessions. The Discord surface already does
// this host-side (resident/src/discord.ts: list → resume the prior ended
// binding → send); `mc collie send` refused instead.
//
// A session ends with detail "exited" the tick after its warm container idles
// out (~60s, against a 1800s collie_idle_timeout_s), so EVERY turn after the
// first needed an explicit `mc collie resume` before its `mc collie send`.
//
// The end itself is correct and is NOT what these tests change: the Collie's
// container exits, the resident observes it, and `mc collie end --reason
// exited` records it.

func sendSessionStatus(t *testing.T, db *sql.DB, session string) string {
	t.Helper()
	var status string
	if err := db.QueryRow(`SELECT status FROM collie_sessions WHERE id = ?`, session).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func startedSession(t *testing.T, db *sql.DB, from string) string {
	t.Helper()
	startedAny, err := CollieStart(db, nil, CollieStartArgs{From: from})
	if err != nil {
		t.Fatal(err)
	}
	started, ok := startedAny.(CollieStartResult)
	if !ok || started.SessionID == "" {
		t.Fatalf("no session_id in %v", startedAny)
	}
	return started.SessionID
}

func TestCollieSendResumesTheBoundEndedSession(t *testing.T) {
	db := dvSpine(t)
	session := startedSession(t, db, "cli:driver")
	if _, err := RegisterSession(db, &RunIdentity{Tier: "collie", RunID: session},
		session, "native-session-1", "native.jsonl"); err != nil {
		t.Fatal(err)
	}
	// The container idled out and the absent round ended the session.
	if _, err := CollieEnd(db, nil, session, "exited"); err != nil {
		t.Fatal(err)
	}
	if got := sendSessionStatus(t, db, session); got != "ended" {
		t.Fatalf("precondition: status = %q, want ended", got)
	}

	// The operator simply sends their next message, from the same place.
	sent, err := CollieSend(db, nil, CollieSendArgs{
		Session: session, From: "cli:driver", Body: "the next turn",
	})
	if err != nil {
		t.Fatalf("send to an ended bound session refused: %v", err)
	}
	if got := sendSessionStatus(t, db, session); got != "active" {
		t.Fatalf("status after send = %q, want active", got)
	}
	row, _ := sent.(map[string]any)
	if row["message_id"] == nil {
		t.Fatalf("send returned no message_id: %v", sent)
	}

	// The inbound row is really there — the resume must not have swallowed it,
	// or the wake would find nothing to answer.
	var body string
	var direction string
	if err := db.QueryRow(`
		SELECT body, direction FROM conversation_messages
		WHERE session_id = ? ORDER BY seq DESC LIMIT 1`, session).Scan(&body, &direction); err != nil {
		t.Fatal(err)
	}
	if body != "the next turn" || direction != "inbound" {
		t.Fatalf("last message = %q/%q, want the inbound turn", direction, body)
	}

	// A native locator pair exists, so the resume owes a native continue —
	// the same debt an explicit `mc collie resume` creates, so the wake cannot
	// silently downgrade to a fresh session and lose the conversation.
	var resumeOwed int
	var resumeMode sql.NullString
	var launchID sql.NullString
	if err := db.QueryRow(`
		SELECT resume_owed, resume_mode, current_launch_id
		FROM collie_sessions WHERE id = ?`, session).Scan(&resumeOwed, &resumeMode, &launchID); err != nil {
		t.Fatal(err)
	}
	if resumeOwed != 1 || resumeMode.String != "native" {
		t.Fatalf("resume debt = %d/%q, want 1/native", resumeOwed, resumeMode.String)
	}
	if launchID.Valid {
		t.Fatalf("dead launch generation %q survived the implicit resume", launchID.String)
	}

	// And it is on the record as a resume, like any other.
	var kinds int
	if err := db.QueryRow(`
		SELECT count(*) FROM activity
		WHERE kind = 'collie.resumed' AND subject = ?`, session).Scan(&kinds); err != nil {
		t.Fatal(err)
	}
	if kinds != 1 {
		t.Fatalf("collie.resumed rows = %d, want 1", kinds)
	}
}

func TestCollieSendResumesWithoutANativeLocatorPair(t *testing.T) {
	// A session whose container never reached its runner has no locator pair.
	// `CollieResume` refuses that outright, but a send must not: the pair is
	// registered as soon as a harness session exists, so its absence means
	// none was ever established and there is nothing to continue. Clear the
	// dead generation, owe no resume, and let the wake start fresh.
	db := dvSpine(t)
	session := startedSession(t, db, "cli:driver")
	if _, err := CollieEnd(db, nil, session, "exited"); err != nil {
		t.Fatal(err)
	}

	if _, err := CollieSend(db, nil, CollieSendArgs{
		Session: session, From: "cli:driver", Body: "first real turn",
	}); err != nil {
		t.Fatalf("send to an ended pairless session refused: %v", err)
	}
	if got := sendSessionStatus(t, db, session); got != "active" {
		t.Fatalf("status = %q, want active", got)
	}
	var resumeOwed int
	if err := db.QueryRow(
		`SELECT resume_owed FROM collie_sessions WHERE id = ?`, session).Scan(&resumeOwed); err != nil {
		t.Fatal(err)
	}
	if resumeOwed != 0 {
		t.Fatal("owed a native continue with no locator pair to continue from")
	}
}

func TestCollieSendStillRefusesAnUnboundPlace(t *testing.T) {
	// The implicit path covers the bound session only. An operator
	// naming an unrelated ended session from a place that was never bound to
	// it keeps the explicit `mc collie resume` requirement — otherwise send
	// becomes a way to reactivate any session from anywhere.
	db := dvSpine(t)
	session := startedSession(t, db, "cli:driver")
	if _, err := CollieEnd(db, nil, session, "exited"); err != nil {
		t.Fatal(err)
	}

	_, err := CollieSend(db, nil, CollieSendArgs{
		Session: session, From: "cli:someone-else", Body: "let me in",
	})
	if err == nil {
		t.Fatal("send from a never-bound place reactivated an ended session")
	}
	if !strings.Contains(err.Error(), "resume") {
		t.Fatalf("refusal should still point at resume, got: %v", err)
	}
	if got := sendSessionStatus(t, db, session); got != "ended" {
		t.Fatalf("status = %q, want ended (the refusal must not have flipped it)", got)
	}
}
