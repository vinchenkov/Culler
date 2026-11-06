package verbs

import (
	"database/sql"
	"strings"
	"sync"
	"testing"
)

func threadSession(t *testing.T, db *sql.DB, thread string) string {
	t.Helper()
	value, err := CollieStart(db, nil, CollieStartArgs{From: "discord:" + thread, DiscordGuild: "1", DiscordParent: "2"})
	if err != nil {
		t.Fatal(err)
	}
	return value.(CollieStartResult).SessionID
}

func TestCollieDiscordThreadContinuity(t *testing.T) {
	db := dvSpine(t)
	session := threadSession(t, db, "100")
	if again := threadSession(t, db, "100"); again != session {
		t.Fatal("The thread created another session.")
	}
	if other := threadSession(t, db, "101"); other == session {
		t.Fatal("Two threads share a session.")
	}
	id := &RunIdentity{Tier: "collie", RunID: session}
	if _, err := RegisterSession(db, id, session, "native-thread", "native.jsonl"); err != nil {
		t.Fatal(err)
	}
	send := func(from, body, source string) {
		t.Helper()
		if _, err := CollieSend(db, nil, CollieSendArgs{Session: session, From: from, Body: body, SourceMessage: source}); err != nil {
			t.Fatal(err)
		}
	}
	send("discord:100", "First Discord message", "100")
	send("discord:100", "First Discord message", "100")
	send("dashboard:web", "Dashboard reply", "")
	if _, err := CollieEnd(db, nil, session, "The session is idle."); err != nil {
		t.Fatal(err)
	}
	if _, err := CollieResume(db, nil, session, "dashboard:web"); err != nil {
		t.Fatal(err)
	}
	send("dashboard:web", "Dashboard reply after idle", "")
	if _, err := CollieEnd(db, nil, session, "The session is idle."); err != nil {
		t.Fatal(err)
	}
	send("discord:100", "Discord reply after idle", "102")
	var messages, echoes, active int
	if err := db.QueryRow(`SELECT count(*) FROM conversation_messages WHERE session_id=?`, session).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM outbox WHERE session_id=? AND surface='discord' AND channel_ref='100'`, session).Scan(&echoes); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM collie_bindings WHERE session_id=? AND surface='discord' AND channel_ref='100' AND active=1`, session).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if messages != 4 || echoes != 2 || active != 1 {
		t.Fatalf("messages=%d echoes=%d active=%d", messages, echoes, active)
	}
	var mode string
	if err := db.QueryRow(`SELECT resume_mode FROM collie_sessions WHERE id=?`, session).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "native" {
		t.Fatalf("resume mode=%s", mode)
	}
	if _, err := CollieSend(db, nil, CollieSendArgs{Session: session, From: "discord:100", Body: "Changed replay", SourceMessage: "100"}); err == nil {
		t.Fatal("A changed replay succeeded.")
	}
}

func TestCollieDiscordThreadRetirementAndDeletion(t *testing.T) {
	db := dvSpine(t)
	session := threadSession(t, db, "100")
	if _, err := RegisterSession(db, &RunIdentity{Tier: "collie", RunID: session}, session, "native-thread", "native.jsonl"); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscordApply(db, DiscordProjection{Connection: strings.Repeat("a", 64), Retire: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := CollieEnd(db, nil, session, "The session is idle."); err != nil {
		t.Fatal(err)
	}
	if _, err := CollieResume(db, nil, session, "dashboard:web"); err != nil {
		t.Fatal(err)
	}
	var active int
	if err := db.QueryRow(`SELECT count(*) FROM collie_bindings WHERE session_id=? AND surface='discord' AND active=1`, session).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatal("The dashboard restored a retired destination.")
	}
	if _, err := CollieStart(db, nil, CollieStartArgs{From: "discord:100", DiscordGuild: "1", DiscordParent: "2"}); err == nil {
		t.Fatal("A retired thread started again.")
	}
	other := threadSession(t, db, "101")
	if _, err := CollieDelete(db, nil, other); err != nil {
		t.Fatal(err)
	}
	if _, err := CollieStart(db, nil, CollieStartArgs{From: "discord:101", DiscordGuild: "1", DiscordParent: "2"}); err == nil {
		t.Fatal("A deleted conversation started again.")
	}
}

func TestCollieDiscordThreadConcurrentStartAndReplay(t *testing.T) {
	db := dvSpine(t)
	var group sync.WaitGroup
	ids := make(chan string, 6)
	for i := 0; i < 6; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			value, err := CollieStart(db, nil, CollieStartArgs{From: "discord:100", DiscordGuild: "1", DiscordParent: "2"})
			if err != nil {
				t.Error(err)
				return
			}
			ids <- value.(CollieStartResult).SessionID
		}()
	}
	group.Wait()
	close(ids)
	session := ""
	for id := range ids {
		if session != "" && session != id {
			t.Fatal("Concurrent starts created separate sessions.")
		}
		session = id
	}
	for i := 0; i < 6; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := CollieSend(db, nil, CollieSendArgs{Session: session, From: "discord:100", Body: "One message", SourceMessage: "100"}); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM conversation_messages WHERE source_message_id='100'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("The replay produced %d messages.", count)
	}
}

func TestDiscordOutboxCursorPreservesPendingRows(t *testing.T) {
	db := dvSpine(t)
	for i := 0; i < 101; i++ {
		dvExec(t, db, `INSERT INTO outbox(kind,surface,channel_ref,payload) VALUES ('health','discord','100','{"body":"Alert"}')`)
	}
	first, err := OutboxPoll(db, nil, "discord", 100)
	if err != nil {
		t.Fatal(err)
	}
	rows := first.(map[string]any)["rows"].([]map[string]any)
	if len(rows) != 100 {
		t.Fatalf("First page has %d rows.", len(rows))
	}
	after := rows[99]["id"].(int64)
	next, err := OutboxPoll(db, nil, "discord", 100, after)
	if err != nil {
		t.Fatal(err)
	}
	remaining := next.(map[string]any)["rows"].([]map[string]any)
	if len(remaining) != 1 || remaining[0]["id"].(int64) <= after {
		t.Fatalf("Unexpected next page: %v", remaining)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM outbox WHERE delivered_at IS NULL`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 101 {
		t.Fatal("A read changed delivery state.")
	}
	if _, err := OutboxPoll(db, nil, "discord", 100, -1); err == nil {
		t.Fatal("A negative cursor succeeded.")
	}
}
