package verbs

import (
	"database/sql"
	"strconv"
	"testing"

	"mc/domain"
)

func TestCollieRestartRetainsContextAndRequiresExplicitReplyRetry(t *testing.T) {
	db := dvSpine(t)
	session := startedSession(t, db, "dashboard:dash")
	const previousLaunch = "ffffffffffffffff"
	dvExec(t, db, `UPDATE collie_sessions SET current_launch_id=?,current_launch_mode='fresh',current_container_id=?,launch_bound_at=datetime('now'),launch_started_at=datetime('now'),native_session_ref='retained-native-session',trace_filename='retained.jsonl' WHERE id=?`, previousLaunch, hbContainer, session)
	if _, err := CollieSend(db, nil, CollieSendArgs{Session: session, From: "dashboard:dash", Body: "Keep this conversation context."}); err != nil {
		t.Fatal(err)
	}
	previous := &RunIdentity{Tier: "collie", RunID: session, Launch: previousLaunch, VerbAllowlist: MaximumCollieAgentVerbs()}
	claimed, err := CollieClaim(db, previous, session)
	if err != nil {
		t.Fatal(err)
	}
	message := claimed.(map[string]any)["message"].(map[string]any)
	messageID, err := strconv.ParseInt(message["id"].(string), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	previousAttempt := message["attempt_id"].(string)
	if _, err := CollieDraft(db, previous, CollieDraftArgs{Session: session, To: messageID, Attempt: previousAttempt, Body: "Retained partial reply."}); err != nil {
		t.Fatal(err)
	}
	if effect, handled := hwAbsentRound(t, db, 1800); !handled || effect["reason"] != "exited-owed-reply" {
		t.Fatalf("The absent process did not recover: %+v", effect)
	}
	var state, native, trace string
	var completed sql.NullString
	hwGet(t, db, `SELECT state FROM collie_reply_attempts WHERE attempt_id='`+previousAttempt+`'`, &state)
	hwGet(t, db, `SELECT native_session_ref,trace_filename FROM collie_sessions WHERE id='`+session+`'`, &native, &trace)
	hwGet(t, db, `SELECT completed_at FROM conversation_messages WHERE id=`+strconv.FormatInt(messageID, 10), &completed)
	if state != "failed" || completed.Valid || native != "retained-native-session" || trace != "retained.jsonl" {
		t.Fatal("The restart lost context or reported a completed reply")
	}
	history := draftHistory(t, db, session)
	if len(history) != 1 || history[0]["reply_state"] != "failed" || history[0]["draft"] != "Retained partial reply." {
		t.Fatalf("The reply history lost its retry state or draft: %+v", history)
	}
	wake, handled := hwWakeRoundInventory(t, db, 1800, []PrivateCollieContainer{})
	if !handled || wake["mode"] != "native" || wake["native_session_ref"] != native || wake["trace_filename"] != trace {
		t.Fatalf("The replacement process lost the native session: %+v", wake)
	}
	var launch string
	if err := db.QueryRow(`SELECT current_launch_id FROM collie_sessions WHERE id=?`, session).Scan(&launch); err != nil {
		t.Fatal(err)
	}
	replacement := &RunIdentity{Tier: "collie", RunID: session, Launch: launch, VerbAllowlist: MaximumCollieAgentVerbs()}
	claim, err := CollieClaim(db, replacement, session)
	if err != nil || len(claim.(map[string]any)["message"].(map[string]any)) != 0 {
		t.Fatalf("The replacement process repeated an ambiguous reply: %+v %v", claim, err)
	}
	if _, err := CollieReply(db, previous, CollieReplyArgs{Session: session, To: messageID, Attempt: previousAttempt, Body: "Late reply."}); err == nil {
		t.Fatal("The previous process completed the interrupted reply")
	}
	retryKey := domain.DiagnosticID()
	first, err := CollieReplyRetry(db, nil, session, messageID, retryKey)
	if err != nil {
		t.Fatal(err)
	}
	again, err := CollieReplyRetry(db, nil, session, messageID, retryKey)
	if err != nil || first.(map[string]any)["attempt_id"] != again.(map[string]any)["attempt_id"] {
		t.Fatal("The explicit retry created duplicate attempts")
	}
	claim, err = CollieClaim(db, replacement, session)
	if err != nil {
		t.Fatal(err)
	}
	retried := claim.(map[string]any)["message"].(map[string]any)
	if retried["id"] != message["id"] || retried["attempt_id"] == previousAttempt || retried["body"] != "Keep this conversation context." {
		t.Fatalf("The explicit retry lost the original message: %+v", retried)
	}
}
