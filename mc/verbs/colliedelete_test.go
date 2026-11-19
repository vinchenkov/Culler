package verbs

import (
	"fmt"
	"sync"
	"testing"

	"mc/domain"
)

func TestCollieEnsureSharesOneConversation(t *testing.T) {
	db := dvSpine(t)
	var group sync.WaitGroup
	ids := make(chan string, 6)
	for i := 0; i < 6; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := CollieStart(db, nil, CollieStartArgs{From: "dashboard:automatic", Ensure: true})
			if err != nil {
				t.Error(err)
				return
			}
			ids <- result.(CollieStartResult).SessionID
		}()
	}
	group.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Errorf("different session: %s", id)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM collie_sessions`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("sessions=%d, error=%v", count, err)
	}
	if _, err := CollieEnd(db, nil, first, "The session is idle."); err != nil {
		t.Fatal(err)
	}
	if _, err := CollieResume(db, nil, first, "dashboard:automatic"); err != nil {
		t.Fatal(err)
	}
	var owed int
	if err := db.QueryRow(`SELECT resume_owed FROM collie_sessions WHERE id=?`, first).Scan(&owed); err != nil || owed != 0 {
		t.Fatalf("resume debt=%d, error=%v", owed, err)
	}
}

func TestCollieDeleteStopsAndHidesConversation(t *testing.T) {
	db := dvSpine(t)
	session := startedSession(t, db, "dashboard:delete")
	if _, err := CollieSend(db, nil, CollieSendArgs{Session: session, From: "dashboard:delete", Body: "A question."}); err != nil {
		t.Fatal(err)
	}
	if _, err := CollieDelete(db, &RunIdentity{Tier: "collie", RunID: session}, session); err == nil {
		t.Fatal("agent deleted the conversation")
	}
	for i := 0; i < 2; i++ {
		if _, err := CollieDelete(db, nil, session); err != nil {
			t.Fatal(err)
		}
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM collie_sessions WHERE id=?`, session).Scan(&status); err != nil || status != "ended" {
		t.Fatalf("status=%s, error=%v", status, err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM collie_bindings WHERE session_id=? AND active=1`, session).Scan(&count); err != nil || count != 0 {
		t.Fatalf("bindings=%d, error=%v", count, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM activity WHERE kind='collie.deleted' AND subject=?`, session).Scan(&count); err != nil || count != 1 {
		t.Fatalf("deletions=%d, error=%v", count, err)
	}
	if _, err := CollieHistory(db, nil, session, false); err == nil {
		t.Fatal("deleted history remained accessible")
	}
	if _, err := CollieResume(db, nil, session, "dashboard:delete"); err == nil {
		t.Fatal("deleted session resumed")
	}
	if _, err := CollieSend(db, nil, CollieSendArgs{Session: session, From: "dashboard:delete", Body: "A late question."}); err == nil {
		t.Fatal("deleted session accepted input")
	}
	if _, err := db.Exec(`UPDATE collie_sessions SET status='active' WHERE id=?`, session); err == nil {
		t.Fatal("deleted session became active")
	}
	if _, err := db.Exec(`UPDATE collie_sessions SET deleted_at=NULL WHERE id=?`, session); err == nil {
		t.Fatal("deletion was reversed")
	}
	list, err := CollieList(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.(map[string]any)["sessions"].([]map[string]any)) != 0 {
		t.Fatalf("list=%+v", list)
	}
	next, err := CollieStart(db, nil, CollieStartArgs{From: "dashboard:automatic", Ensure: true})
	if err != nil || next.(CollieStartResult).SessionID == session {
		t.Fatalf("replacement=%+v, error=%v", next, err)
	}
	vision, err := CollieStart(db, nil, CollieStartArgs{From: "dashboard:vision-ws-test", VisionProject: "ws-test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CollieDelete(db, nil, vision.(CollieStartResult).SessionID); err == nil {
		t.Fatal("ordinary deletion accepted a Vision conversation")
	}
}

func TestCollieDeleteHidesTheReplyConditionAndRetainsItsHistory(t *testing.T) {
	db := dvSpine(t)
	session := startedSession(t, db, "dashboard:delete-failed")
	if _, err := CollieSend(db, nil, CollieSendArgs{Session: session, From: "dashboard:delete-failed", Body: "A question."}); err != nil {
		t.Fatal(err)
	}
	id := &RunIdentity{Tier: "collie", RunID: session}
	claimed, err := CollieClaim(db, id, session)
	if err != nil {
		t.Fatal(err)
	}
	attempt := claimed.(map[string]any)["message"].(map[string]any)["attempt_id"].(string)
	if _, err := CollieReplyFailure(db, id, attempt, "The harness ended without a reply."); err != nil {
		t.Fatal(err)
	}
	for _, deleted := range []bool{false, true} {
		if deleted {
			if _, err := CollieDelete(db, nil, session); err != nil {
				t.Fatal(err)
			}
		}
		status, err := SystemStatus(db, nil, SystemStatusRequest{})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, condition := range status.(SystemStatusResult).Conditions {
			if condition.SessionID == session && condition.Action == "Retry reply" {
				found = true
			}
		}
		if found == deleted {
			t.Fatalf("The reply condition has the wrong visibility: deleted=%t found=%t", deleted, found)
		}
	}
	var state string
	var diagnostics int
	if err := db.QueryRow(`SELECT a.state,COUNT(d.id) FROM collie_reply_attempts a JOIN diagnostic_events d ON d.id=a.latest_diagnostic_id WHERE a.attempt_id=?`, attempt).Scan(&state, &diagnostics); err != nil || state != "failed" || diagnostics != 1 {
		t.Fatalf("The reply history changed after deletion: state=%s diagnostics=%d error=%v", state, diagnostics, err)
	}
}

func TestOperatorDismissesAFailedCollieReplyNotice(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	session := startedSession(t, db, "dashboard:dismiss-failed")
	if _, err := CollieSend(db, nil, CollieSendArgs{Session: session, From: "dashboard:dismiss-failed", Body: "A question."}); err != nil {
		t.Fatal(err)
	}
	id := &RunIdentity{Tier: "collie", RunID: session}
	claimed, err := CollieClaim(db, id, session)
	if err != nil {
		t.Fatal(err)
	}
	attempt := claimed.(map[string]any)["message"].(map[string]any)["attempt_id"].(string)
	var message int64
	if err := db.QueryRow(`SELECT message_id FROM collie_reply_attempts WHERE attempt_id=?`, attempt).Scan(&message); err != nil {
		t.Fatal(err)
	}
	if _, err := CollieReplyDismiss(db, nil, session, message, domain.DiagnosticID()); err == nil {
		t.Fatal("Dismiss accepted a reply without a failure.")
	}
	if _, err := CollieReplyFailure(db, id, attempt, "The harness ended without a reply."); err != nil {
		t.Fatal(err)
	}
	notice := func() bool {
		t.Helper()
		status, err := SystemStatus(db, nil, SystemStatusRequest{})
		if err != nil {
			t.Fatal(err)
		}
		for _, condition := range status.(SystemStatusResult).Conditions {
			if condition.ID == fmt.Sprintf("reply:%d", message) {
				return true
			}
		}
		return false
	}
	if !notice() {
		t.Fatal("The failed reply has no notice.")
	}
	key := domain.DiagnosticID()
	for range 2 {
		if _, err := CollieReplyDismiss(db, nil, session, message, key); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := CollieReplyDismiss(db, nil, session, message, domain.DiagnosticID()); err != nil {
		t.Fatal(err)
	}
	if _, err := CollieReplyDismiss(db, nil, session, message+1, key); err == nil {
		t.Fatal("A dismissal identity applied to another message.")
	}
	if notice() {
		t.Fatal("Dismiss did not end the failed reply notice.")
	}
	var state string
	var dismissals int
	if err := db.QueryRow(`SELECT a.state,(SELECT COUNT(*) FROM diagnostic_events WHERE reply_attempt_id=a.attempt_id AND source='operator' AND outcome='cancelled') FROM collie_reply_attempts a WHERE a.attempt_id=?`, attempt).Scan(&state, &dismissals); err != nil || state != "failed" || dismissals != 1 {
		t.Fatalf("Dismiss changed the reply history: state=%s dismissals=%d error=%v", state, dismissals, err)
	}
	// The failed reply can still retry, and a new failure has its own notice.
	retried, err := CollieReplyRetry(db, nil, session, message, domain.DiagnosticID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CollieReplyFailure(db, nil, retried.(map[string]any)["attempt_id"].(string), "The harness ended without a reply."); err != nil {
		t.Fatal(err)
	}
	if !notice() {
		t.Fatal("A new reply failure after a dismissal has no notice.")
	}
}
