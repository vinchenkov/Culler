package verbs

import (
	"sync"
	"testing"
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
