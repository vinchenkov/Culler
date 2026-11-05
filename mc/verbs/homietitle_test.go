package verbs

import (
	"database/sql"
	"strings"
	"sync"
	"testing"
)

func TestHomieTitleScopeAndPersistence(t *testing.T) {
	db := dvSpine(t)
	session := startedSession(t, db, "dashboard:title")
	agent := draftRunner(session, MaximumHomieAgentVerbs())
	if _, err := HomieTitle(db, agent, session, "Too early"); err == nil {
		t.Fatal("named an empty conversation")
	}
	if _, err := HomieSend(db, nil, HomieSendArgs{Session: session, From: "dashboard:title", Body: "Compare two database options"}); err != nil {
		t.Fatal(err)
	}
	other := startedSession(t, db, "dashboard:other")
	for _, caller := range []*RunIdentity{
		{Tier: "pipeline", RunID: session},
		draftRunner(other, MaximumHomieAgentVerbs()),
		draftRunner(session, []string{"homie.title"}),
	} {
		if _, err := HomieTitle(db, caller, session, "Wrong caller"); err == nil {
			t.Fatalf("accepted caller %+v", caller)
		}
	}
	if _, err := db.Exec(`UPDATE homie_sessions SET last_activity_at='2000-01-01 00:00:00' WHERE id=?`, session); err != nil {
		t.Fatal(err)
	}
	var activityBefore string
	if err := db.QueryRow(`SELECT last_activity_at FROM homie_sessions WHERE id=?`, session).Scan(&activityBefore); err != nil {
		t.Fatal(err)
	}
	var unauthorizedTitle sql.NullString
	if err := db.QueryRow(`SELECT title FROM homie_sessions WHERE id=?`, session).Scan(&unauthorizedTitle); err != nil || unauthorizedTitle.Valid {
		t.Fatalf("unauthorized title: %v %v", unauthorizedTitle, err)
	}
	first, err := HomieTitle(db, agent, session, "  Compare database options  ")
	if err != nil || first.(map[string]any)["changed"] != true {
		t.Fatalf("first title: %v %v", first, err)
	}
	again, err := HomieTitle(db, agent, session, "A different topic")
	if err != nil || again.(map[string]any)["title"] != "Compare database options" || again.(map[string]any)["changed"] != false {
		t.Fatalf("repeated title: %v %v", again, err)
	}
	if _, err := HomieTitle(db, nil, session, "My database decision"); err != nil {
		t.Fatal(err)
	}
	if _, err := HomieTitle(db, agent, session, "Overwrite the operator"); err != nil {
		t.Fatal(err)
	}
	list, err := HomieList(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	row := list.(map[string]any)["sessions"].([]map[string]any)[0]
	if row["title"] != "My database decision" || row["first_message"] != "Compare two database options" || row["id"] != session {
		t.Fatalf("list: %+v", row)
	}
	var activityAfter, body, status string
	if err := db.QueryRow(`SELECT last_activity_at, status FROM homie_sessions WHERE id=?`, session).Scan(&activityAfter, &status); err != nil {
		t.Fatal(err)
	}
	if activityAfter != activityBefore || status != "active" {
		t.Fatalf("title changed activity or state: %q %q", activityAfter, status)
	}
	if err := db.QueryRow(`SELECT body FROM conversation_messages WHERE session_id=?`, session).Scan(&body); err != nil || body != "Compare two database options" {
		t.Fatalf("title changed the message: %q %v", body, err)
	}
	if _, err := HomieEnd(db, nil, session, "Done"); err != nil {
		t.Fatal(err)
	}
	if _, err := HomieTitle(db, agent, session, "Late title"); err == nil {
		t.Fatal("ended agent named its conversation")
	}
	if _, err := HomieTitle(db, nil, session, "Archived discussion"); err != nil {
		t.Fatal(err)
	}
	if _, err := HomieDelete(db, nil, session); err != nil {
		t.Fatal(err)
	}
	if _, err := HomieTitle(db, nil, session, "Deleted discussion"); err == nil {
		t.Fatal("renamed deleted conversation")
	}
}

func TestHomieTitleValidationAndNarrowPermission(t *testing.T) {
	db := dvSpine(t)
	result, err := HomieStart(db, nil, HomieStartArgs{From: "cli:narrow-title", Allowlist: &[]string{"homie.list"}})
	if err != nil {
		t.Fatal(err)
	}
	session := result.(HomieStartResult).SessionID
	if _, err := HomieTitle(db, draftRunner(session, []string{"homie.list"}), session, "No permission"); err == nil {
		t.Fatal("ignored the frozen permission")
	}
	if _, err := HomieTitle(db, nil, session, "Original title"); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"", "  ", strings.Repeat("x", 101), "a\nb", "a\x00b", "a\u2028b", string([]byte{0xff})} {
		if _, err := HomieTitle(db, nil, session, title); err == nil {
			t.Fatalf("accepted %q", title)
		}
	}
	var retained string
	if err := db.QueryRow(`SELECT title FROM homie_sessions WHERE id=?`, session).Scan(&retained); err != nil || retained != "Original title" {
		t.Fatalf("invalid title changed the record: %q %v", retained, err)
	}
	if _, err := HomieTitle(db, nil, session, strings.Repeat("界", 100)); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := db.QueryRow(`SELECT title FROM homie_sessions WHERE id=?`, session).Scan(&name); err != nil || name != strings.Repeat("界", 100) {
		t.Fatalf("unicode title: %q %v", name, err)
	}
}

func TestHomieTitleConcurrentOperatorWins(t *testing.T) {
	db := dvSpine(t)
	session := startedSession(t, db, "cli:race-title")
	if _, err := HomieSend(db, nil, HomieSendArgs{Session: session, From: "cli:race-title", Body: "Discuss options"}); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for _, caller := range []*RunIdentity{nil, draftRunner(session, MaximumHomieAgentVerbs())} {
		group.Add(1)
		go func(id *RunIdentity) {
			defer group.Done()
			title := "Operator name"
			if id != nil {
				title = "Agent name"
			}
			if _, err := HomieTitle(db, id, session, title); err != nil {
				t.Error(err)
			}
		}(caller)
	}
	group.Wait()
	var title sql.NullString
	if err := db.QueryRow(`SELECT title FROM homie_sessions WHERE id=?`, session).Scan(&title); err != nil || title.String != "Operator name" {
		t.Fatalf("race: %v %v", title, err)
	}
}
