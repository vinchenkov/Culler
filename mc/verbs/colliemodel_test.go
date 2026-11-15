package verbs

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func modelChange(t *testing.T, db *sql.DB, previous, request string) CollieStartResult {
	t.Helper()
	result, err := CollieStart(db, nil, CollieStartArgs{From: "dashboard:model-test", Previous: previous, Binding: "endpoint", Request: request})
	if err != nil {
		t.Fatal(err)
	}
	return result.(CollieStartResult)
}

func TestCollieModelReplacesEmptyAndReusesRequest(t *testing.T) {
	db := dvSpine(t)
	previous := startedSession(t, db, "dashboard:previous")
	first := modelChange(t, db, previous, "request-model-0001")
	if first.Replaced != previous || first.Preserved || first.Binding != "claude-sdk/endpoint" {
		t.Fatalf("result=%+v", first)
	}
	second := modelChange(t, db, previous, "request-model-0001")
	if second != first {
		t.Fatalf("retry=%+v first=%+v", second, first)
	}
	var count int
	hwGet(t, db, `SELECT count(*) FROM collie_sessions WHERE deleted_at IS NULL`, &count)
	if count != 1 {
		t.Fatalf("visible=%d", count)
	}
	if _, err := CollieResume(db, nil, previous, "dashboard:previous"); err == nil {
		t.Fatal("replaced conversation resumed")
	}
	if _, err := CollieSend(db, nil, CollieSendArgs{Session: previous, From: "dashboard:previous", Body: "Late input"}); err == nil {
		t.Fatal("replaced conversation accepted input")
	}
	result, err := CollieSend(db, nil, CollieSendArgs{Session: first.SessionID, From: first.From, Body: "New input"})
	if err != nil {
		t.Fatalf("new send=%+v: %v", result, err)
	}
	var binding string
	if err := db.QueryRow(`SELECT binding FROM collie_sessions WHERE id=?`, first.SessionID).Scan(&binding); err != nil || binding != first.Binding {
		t.Fatalf("binding=%s: %v", binding, err)
	}
}

func TestCollieModelKeepsMessagesAndTitle(t *testing.T) {
	for _, direction := range []string{"inbound", "reply"} {
		t.Run(direction, func(t *testing.T) {
			db := dvSpine(t)
			previous := startedSession(t, db, "dashboard:previous")
			dvExec(t, db, `UPDATE collie_sessions SET title='Existing conversation' WHERE id=?`, previous)
			dvExec(t, db, `INSERT INTO conversation_messages(session_id,seq,direction,surface,body,attachments) VALUES (?,1,'inbound','dashboard','','["attachment"]')`, previous)
			if direction == "reply" {
				dvExec(t, db, `INSERT INTO conversation_messages(session_id,seq,direction,surface,body,reply_to) SELECT session_id,2,'reply','collie','A reply',id FROM conversation_messages WHERE session_id=? AND seq=1`, previous)
			}
			result := modelChange(t, db, previous, "request-model-0002")
			if !result.Preserved || result.Replaced != "" {
				t.Fatalf("result=%+v", result)
			}
			var title, binding string
			if err := db.QueryRow(`SELECT title,binding FROM collie_sessions WHERE id=? AND deleted_at IS NULL`, previous).Scan(&title, &binding); err != nil || title != "Existing conversation" || binding != dvCollieBinding(t) {
				t.Fatalf("old=%s %s: %v", title, binding, err)
			}
			var count int
			hwGet(t, db, `SELECT count(*) FROM collie_sessions WHERE deleted_at IS NULL`, &count)
			if count != 2 {
				t.Fatalf("visible=%d", count)
			}
		})
	}
}

func TestCollieModelConcurrentRequests(t *testing.T) {
	db := dvSpine(t)
	previous := startedSession(t, db, "dashboard:previous")
	var group sync.WaitGroup
	results := make(chan CollieStartResult, 6)
	for i := 0; i < 6; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := CollieStart(db, nil, CollieStartArgs{From: "dashboard:model-test", Previous: previous, Binding: "endpoint", Request: "request-model-0003"})
			if err != nil {
				t.Error(err)
				return
			}
			results <- result.(CollieStartResult)
		}()
	}
	group.Wait()
	close(results)
	first := ""
	for result := range results {
		if first == "" {
			first = result.SessionID
		}
		if result.SessionID != first {
			t.Fatal("duplicate request created another conversation")
		}
	}
	if first == "" {
		t.Fatal("no request succeeded")
	}
	if _, err := CollieStart(db, nil, CollieStartArgs{From: "dashboard:model-test", Previous: previous, Binding: "endpoint", Request: "request-model-0004"}); err == nil {
		t.Fatal("a stale request replaced a deleted conversation")
	}
}

func TestCollieModelConcurrentMessage(t *testing.T) {
	db := dvSpine(t)
	for i := 0; i < 8; i++ {
		from := fmt.Sprintf("dashboard:race-%d", i)
		previous := startedSession(t, db, from)
		var group sync.WaitGroup
		var sendErr, changeErr error
		group.Add(2)
		go func() {
			defer group.Done()
			_, sendErr = CollieSend(db, nil, CollieSendArgs{Session: previous, From: from, Body: "Concurrent input"})
		}()
		go func() {
			defer group.Done()
			_, changeErr = CollieStart(db, nil, CollieStartArgs{From: "dashboard:model-test", Previous: previous, Binding: "endpoint", Request: fmt.Sprintf("request-race-%04d", i)})
		}()
		group.Wait()
		if changeErr != nil {
			t.Fatal(changeErr)
		}
		var deleted sql.NullString
		if err := db.QueryRow(`SELECT deleted_at FROM collie_sessions WHERE id=?`, previous).Scan(&deleted); err != nil {
			t.Fatal(err)
		}
		if sendErr == nil && deleted.Valid {
			t.Fatal("the model change deleted a stored message")
		}
		if sendErr != nil && !deleted.Valid {
			t.Fatalf("unexpected message refusal: %v", sendErr)
		}
	}
}

func TestCollieModelFailureAndSameModel(t *testing.T) {
	db := dvSpine(t)
	previous := startedSession(t, db, "dashboard:previous")
	for _, binding := range []string{"unavailable", ""} {
		if _, err := CollieStart(db, nil, CollieStartArgs{From: "dashboard:model-test", Previous: previous, Binding: binding, Request: "request-model-0005"}); err == nil {
			t.Fatal("invalid profile succeeded")
		}
	}
	result, err := CollieStart(db, nil, CollieStartArgs{From: "dashboard:model-test", Previous: previous, Binding: strings.Split(dvCollieBinding(t), "/")[1], Request: "request-model-0006"})
	if err != nil || result.(CollieStartResult).SessionID != previous {
		t.Fatalf("same model=%+v: %v", result, err)
	}
	var count int
	hwGet(t, db, `SELECT count(*) FROM collie_sessions WHERE deleted_at IS NULL`, &count)
	if count != 1 {
		t.Fatalf("visible=%d", count)
	}
	if _, err := CollieStart(db, &RunIdentity{Tier: "collie", RunID: previous}, CollieStartArgs{From: "dashboard:model-test", Previous: previous, Binding: "endpoint", Request: "request-model-0007"}); err == nil {
		t.Fatal("agent changed the model")
	}
}

func TestCollieModelStopsOldContainerAndPreparesSelectedProfile(t *testing.T) {
	db := dvSpine(t)
	previous := startedSession(t, db, "dashboard:previous")
	launch := "ffffffffffffffff"
	dvExec(t, db, `UPDATE collie_sessions SET current_launch_id=?,current_launch_mode='fresh',current_container_id=?,launch_bound_at=datetime('now'),launch_started_at=datetime('now') WHERE id=?`, launch, hbContainer, previous)
	result := modelChange(t, db, previous, "request-model-0008")
	err := inTx(db, func(ctx context.Context, q Q) error {
		effect, handled, err := collieOrphanRound(ctx, q, []PrivateCollieContainer{{Session: previous, Launch: launch, ContainerID: hbContainer, Name: "mc-collie-" + previous, State: "running"}})
		if err != nil {
			return err
		}
		if !handled || effect["action"] != "collie-remove" || effect["container_id"] != hbContainer {
			t.Fatalf("old container effect=%+v", effect)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	effect, handled := hwWakeRound(t, db, 1800)
	if !handled || effect["action"] != "collie-wake" || effect["session"] != result.SessionID || effect["binding"] != "claude-sdk/endpoint" {
		t.Fatalf("replacement wake=%+v", effect)
	}
}

func TestCollieModelInsertFailurePreservesPrevious(t *testing.T) {
	db := dvSpine(t)
	previous := startedSession(t, db, "dashboard:previous")
	dvExec(t, db, `CREATE TRIGGER reject_model_test BEFORE INSERT ON collie_sessions BEGIN SELECT RAISE(ABORT,'test storage failure'); END`)
	if _, err := CollieStart(db, nil, CollieStartArgs{From: "dashboard:model-test", Previous: previous, Binding: "endpoint", Request: "request-model-0009"}); err == nil {
		t.Fatal("expected an insert failure")
	}
	var status string
	var deleted sql.NullString
	if err := db.QueryRow(`SELECT status,deleted_at FROM collie_sessions WHERE id=?`, previous).Scan(&status, &deleted); err != nil || deleted.Valid || status != "active" {
		t.Fatalf("previous=%s deleted=%v: %v", status, deleted, err)
	}
	dvExec(t, db, `DROP TRIGGER reject_model_test`)
	modelChange(t, db, previous, "request-model-0009")
}
