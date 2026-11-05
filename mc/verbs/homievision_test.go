package verbs

import "testing"

func TestHomieVisionSessionIsStableAndSeparate(t *testing.T) {
	db := dvSpine(t)
	ordinary := startedSession(t, db, "dashboard:ordinary")
	args := HomieStartArgs{From: "dashboard:vision-ws-test", VisionProject: "ws-test"}
	first, err := HomieStart(db, nil, args)
	if err != nil {
		t.Fatal(err)
	}
	second, err := HomieStart(db, nil, args)
	if err != nil {
		t.Fatal(err)
	}
	a, b := first.(HomieStartResult), second.(HomieStartResult)
	if a.SessionID == ordinary || a.SessionID != b.SessionID {
		t.Fatalf("sessions = %+v, %+v; ordinary=%s", a, b, ordinary)
	}
	var project string
	if err := db.QueryRow(`SELECT vision_project_id FROM homie_sessions WHERE id=?`, a.SessionID).Scan(&project); err != nil {
		t.Fatal(err)
	}
	if project != "ws-test" {
		t.Fatalf("project = %s", project)
	}
	if _, err := RegisterSession(db, &RunIdentity{Tier: "homie", RunID: a.SessionID}, a.SessionID, "native-vision", "native.jsonl"); err != nil {
		t.Fatal(err)
	}
	if _, err := HomieEnd(db, nil, a.SessionID, "test"); err != nil {
		t.Fatal(err)
	}
	again, err := HomieStart(db, nil, args)
	if err != nil {
		t.Fatal(err)
	}
	if again.(HomieStartResult).Status != "active" || again.(HomieStartResult).SessionID != a.SessionID {
		t.Fatalf("repeat = %+v", again)
	}
	var owed int
	if err := db.QueryRow(`SELECT resume_owed FROM homie_sessions WHERE id=?`, a.SessionID).Scan(&owed); err != nil || owed != 1 {
		t.Fatalf("native resume debt = %d, %v", owed, err)
	}
	if _, err := db.Exec(`UPDATE homie_sessions SET vision_project_id=NULL WHERE id=?`, a.SessionID); err == nil {
		t.Fatal("Project association changed")
	}
}

func TestHomieVisionClearStartsAnEmptySession(t *testing.T) {
	db := dvSpine(t)
	args := HomieStartArgs{From: "dashboard:vision-ws-test", VisionProject: "ws-test"}
	first, err := HomieStart(db, nil, args)
	if err != nil {
		t.Fatal(err)
	}
	old := first.(HomieStartResult).SessionID
	if _, err := HomieSend(db, nil, HomieSendArgs{Session: old, From: "dashboard:vision-ws-test", Body: "An old goal."}); err != nil {
		t.Fatal(err)
	}
	cleared, err := HomieStart(db, nil, HomieStartArgs{From: args.From, VisionProject: args.VisionProject, Clear: true})
	if err != nil {
		t.Fatal(err)
	}
	fresh := cleared.(HomieStartResult)
	if fresh.SessionID == old || fresh.Status != "active" {
		t.Fatalf("cleared = %+v; old=%s", fresh, old)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM homie_sessions WHERE id=?`, old).Scan(&status); err != nil || status != "ended" {
		t.Fatalf("old status = %q, %v", status, err)
	}
	var messages int
	if err := db.QueryRow(`SELECT count(*) FROM conversation_messages WHERE session_id=?`, fresh.SessionID).Scan(&messages); err != nil || messages != 0 {
		t.Fatalf("fresh messages = %d, %v", messages, err)
	}
	again, err := HomieStart(db, nil, args)
	if err != nil {
		t.Fatal(err)
	}
	if again.(HomieStartResult).SessionID != fresh.SessionID {
		t.Fatalf("repeat = %+v; want %s", again, fresh.SessionID)
	}
	if _, err := HomieStart(db, nil, HomieStartArgs{From: "dashboard:ordinary", Clear: true}); err == nil {
		t.Fatal("an ordinary session accepted --clear")
	}
}

func TestHomieVisionStartRejectsInvalidProject(t *testing.T) {
	db := dvSpine(t)
	for _, args := range []HomieStartArgs{
		{From: "dashboard:ordinary", VisionProject: "ws-test"},
		{From: "dashboard:vision-missing", VisionProject: "missing"},
	} {
		if _, err := HomieStart(db, nil, args); err == nil {
			t.Fatalf("accepted %+v", args)
		}
	}
}

func TestHomieVisionWakeCarriesProject(t *testing.T) {
	db := dvSpine(t)
	result, err := HomieStart(db, nil, HomieStartArgs{From: "dashboard:vision-ws-test", VisionProject: "ws-test"})
	if err != nil {
		t.Fatal(err)
	}
	session := result.(HomieStartResult).SessionID
	hwPendingInbound(t, db, session, 1)
	effect, handled := hwWakeRound(t, db, 1800)
	if !handled || effect["vision_project_id"] != "ws-test" || effect["session"] != session {
		t.Fatalf("wake = %+v", effect)
	}
	listed, err := HomieList(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := listed.(map[string]any)["sessions"].([]map[string]any)
	if len(rows) != 1 || rows[0]["vision_project"] != "ws-test" {
		t.Fatalf("list = %+v", rows)
	}
}

func TestHomieVisionClaimRefreshesDraftContext(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `INSERT INTO vision_drafts (project_id,content,revision,workspace_identity,workspace_revision,workspace_has_files) VALUES ('ws-test','First goal','one','root','absent',1)`)
	result, err := HomieStart(db, nil, HomieStartArgs{From: "dashboard:vision-ws-test", VisionProject: "ws-test"})
	if err != nil {
		t.Fatal(err)
	}
	session := result.(HomieStartResult).SessionID
	if _, err := HomieSend(db, nil, HomieSendArgs{Session: session, From: "dashboard:vision-ws-test", Body: "Improve the goal"}); err != nil {
		t.Fatal(err)
	}
	id := &RunIdentity{Tier: "homie", RunID: session}
	for _, content := range []string{"First goal", "Operator edit"} {
		dvExec(t, db, `UPDATE vision_drafts SET content=?,revision=? WHERE project_id='ws-test'`, content, content)
		claimed, err := HomieClaim(db, id, session)
		if err != nil {
			t.Fatal(err)
		}
		message := claimed.(map[string]any)["message"].(map[string]any)
		context := message["vision_context"].(map[string]any)
		draft := context["draft"].(VisionDraft)
		if context["project_id"] != "ws-test" || context["workspace_vision_status"] != "absent" || draft.Content != content || draft.Revision != content {
			t.Fatalf("context = %+v", context)
		}
	}
}
