package verbs

import (
	"sync"
	"testing"
)

func TestHomieEnsureSharesOneConversation(t *testing.T) {
	db := dvSpine(t)
	var group sync.WaitGroup
	ids := make(chan string, 6)
	for i := 0; i < 6; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := HomieStart(db, nil, HomieStartArgs{From: "dashboard:automatic", Ensure: true})
			if err != nil {
				t.Error(err)
				return
			}
			ids <- result.(HomieStartResult).SessionID
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
	if err := db.QueryRow(`SELECT count(*) FROM homie_sessions`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("sessions=%d, error=%v", count, err)
	}
	if _, err := HomieEnd(db, nil, first, "The session is idle."); err != nil {
		t.Fatal(err)
	}
	if _, err := HomieResume(db, nil, first, "dashboard:automatic"); err != nil {
		t.Fatal(err)
	}
	var owed int
	if err := db.QueryRow(`SELECT resume_owed FROM homie_sessions WHERE id=?`, first).Scan(&owed); err != nil || owed != 0 {
		t.Fatalf("resume debt=%d, error=%v", owed, err)
	}
}

func TestHomieDeleteStopsAndHidesConversation(t *testing.T) {
	db := dvSpine(t)
	session := startedSession(t, db, "dashboard:delete")
	if _, err := HomieSend(db, nil, HomieSendArgs{Session: session, From: "dashboard:delete", Body: "A question."}); err != nil {
		t.Fatal(err)
	}
	if _, err := HomieDelete(db, &RunIdentity{Tier: "homie", RunID: session}, session); err == nil {
		t.Fatal("agent deleted the conversation")
	}
	for i := 0; i < 2; i++ {
		if _, err := HomieDelete(db, nil, session); err != nil {
			t.Fatal(err)
		}
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM homie_sessions WHERE id=?`, session).Scan(&status); err != nil || status != "ended" {
		t.Fatalf("status=%s, error=%v", status, err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM homie_bindings WHERE session_id=? AND active=1`, session).Scan(&count); err != nil || count != 0 {
		t.Fatalf("bindings=%d, error=%v", count, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM activity WHERE kind='homie.deleted' AND subject=?`, session).Scan(&count); err != nil || count != 1 {
		t.Fatalf("deletions=%d, error=%v", count, err)
	}
	if _, err := HomieHistory(db, nil, session, false); err == nil {
		t.Fatal("deleted history remained accessible")
	}
	if _, err := HomieResume(db, nil, session, "dashboard:delete"); err == nil {
		t.Fatal("deleted session resumed")
	}
	if _, err := HomieSend(db, nil, HomieSendArgs{Session: session, From: "dashboard:delete", Body: "A late question."}); err == nil {
		t.Fatal("deleted session accepted input")
	}
	if _, err := db.Exec(`UPDATE homie_sessions SET status='active' WHERE id=?`, session); err == nil {
		t.Fatal("deleted session became active")
	}
	if _, err := db.Exec(`UPDATE homie_sessions SET deleted_at=NULL WHERE id=?`, session); err == nil {
		t.Fatal("deletion was reversed")
	}
	list, err := HomieList(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.(map[string]any)["sessions"].([]map[string]any)) != 0 {
		t.Fatalf("list=%+v", list)
	}
	next, err := HomieStart(db, nil, HomieStartArgs{From: "dashboard:automatic", Ensure: true})
	if err != nil || next.(HomieStartResult).SessionID == session {
		t.Fatalf("replacement=%+v, error=%v", next, err)
	}
	vision, err := HomieStart(db, nil, HomieStartArgs{From: "dashboard:vision-ws-test", VisionProject: "ws-test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := HomieDelete(db, nil, vision.(HomieStartResult).SessionID); err == nil {
		t.Fatal("ordinary deletion accepted a Vision conversation")
	}
}
