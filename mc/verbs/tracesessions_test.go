package verbs

import (
	"reflect"
	"testing"

	"mc/dispatch"
	"mc/trace"
)

func TestTraceSessionsListEachMainTraceWithItsDisplayName(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `UPDATE projects SET title = 'Culler' WHERE id = 'ws-test'`)
	dvInsertTask(t, db, dvTask(7, dispatch.ScopeTask, dispatch.StatusProposed, 1))
	dvExec(t, db, `UPDATE tasks SET title = 'Copy traces outside the tick' WHERE id = 7`)
	codexFile := "2026/09/29/rollout-2026-09-29T10-00-00-" + avCodexRef + ".jsonl"
	for _, run := range []struct {
		id, role, binding            string
		subject, native, file, ended any
		created                      string
	}{
		{"r-strategist", "strategist", "codex/chatgpt", nil, avCodexRef, codexFile, nil, "2026-09-29 10:01:00"},
		{"r-worker", "worker", "claude-sdk/claude", 7, avClaudeRef, "native.jsonl", "2026-09-29 10:05:00", "2026-09-29 10:00:00"},
		{"r-merge", "worker", "mechanical", 7, nil, nil, "2026-09-29 10:06:00", "2026-09-29 10:02:00"},
		{"r-pending", "verifier", "claude-sdk/claude", 7, nil, nil, nil, "2026-09-29 10:03:00"},
		{"r-fake", "verifier", "fake/fake", 7, avClaudeRef, "native.jsonl", nil, "2026-09-29 10:04:00"},
	} {
		dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject, created_at,
			ended_at, session_path, binding, native_session_ref, trace_filename)
			VALUES (?, 'pipeline', ?, 'ws-test', ?, ?, ?, ?, ?, ?, ?)`,
			run.id, run.role, run.subject, run.created, run.ended, "sessions/"+run.id, run.binding, run.native, run.file)
	}
	const collieClaudeRef = "33333333-3333-4333-8333-333333333333"
	const collieCodexRef = "44444444-4444-4444-8444-444444444444"
	const deletedRef = "55555555-5555-4555-8555-555555555555"
	for _, session := range []struct {
		id, binding    string
		vision, native any
		file, status   any
		deleted        any
	}{
		{"h-vision", "claude-sdk/claude", "ws-test", collieClaudeRef, "native.jsonl", "active", nil},
		{"h-plain", "codex/chatgpt", nil, collieCodexRef, "2026/09/29/rollout-" + collieCodexRef + ".jsonl", "reaped", nil},
		{"h-deleted", "claude-sdk/claude", nil, deletedRef, "native.jsonl", "ended", "2026-09-29 11:00:00"},
		{"h-pending", "claude-sdk/claude", nil, nil, nil, "active", nil},
	} {
		dvExec(t, db, `INSERT INTO collie_sessions (id, vision_project_id, status, deleted_at, container_name,
			verb_allowlist, session_path, binding, native_session_ref, trace_filename)
			VALUES (?, ?, ?, ?, ?, '[]', ?, ?, ?, ?)`,
			session.id, session.vision, session.status, session.deleted, "mc-collie-"+session.id,
			"sessions/"+session.id, session.binding, session.native, session.file)
	}

	listed, err := TraceSessions(db)
	if err != nil {
		t.Fatal(err)
	}
	got := listed.(map[string]any)["sessions"].([]TraceSession)
	want := []TraceSession{
		{Kind: "run", ID: "r-worker", SessionPath: "sessions/r-worker", Format: "claude",
			NativeSessionRef: avClaudeRef, TraceFilename: "native.jsonl", AgentsViewSession: avClaudeRef,
			DisplayName: "Culler · worker · Copy traces outside the tick · r-worker", Ended: true},
		{Kind: "run", ID: "r-strategist", SessionPath: "sessions/r-strategist", Format: "codex",
			NativeSessionRef: avCodexRef, TraceFilename: codexFile, AgentsViewSession: "codex:" + avCodexRef,
			DisplayName: "Culler · strategist · r-strategist", Ended: false},
		{Kind: "collie", ID: "h-vision", SessionPath: "sessions/h-vision", Format: "claude",
			NativeSessionRef: collieClaudeRef, TraceFilename: "native.jsonl", AgentsViewSession: collieClaudeRef,
			DisplayName: "Collie · Culler · h-vision", Ended: false},
		{Kind: "collie", ID: "h-plain", SessionPath: "sessions/h-plain", Format: "codex",
			NativeSessionRef: collieCodexRef, TraceFilename: "2026/09/29/rollout-" + collieCodexRef + ".jsonl",
			AgentsViewSession: "codex:" + collieCodexRef, DisplayName: "Collie · h-plain", Ended: true},
		{Kind: "collie", ID: "h-deleted", SessionPath: "sessions/h-deleted", Format: "claude",
			NativeSessionRef: deletedRef, TraceFilename: "native.jsonl", AgentsViewSession: deletedRef,
			DisplayName: "Collie · h-deleted", Ended: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trace sessions =\n%#v\nwant\n%#v", got, want)
	}
}

func TestOpenCodeTracesRetainNativeIdentityAcrossExecutions(t *testing.T) {
	db := dvSpine(t)
	for _, role := range []string{"packager", "scribe"} {
		dvExec(t, db, `INSERT INTO runs(id,tier,role,project,session_path,binding,native_session_ref,trace_filename)
		 VALUES (?,'pipeline',?,'ws-test',?,'opencode/catalog',?,?)`, role, role, "sessions/"+role,
			"ses_"+role, "rollout-"+trace.OpenCodeID("ses_"+role)+".jsonl")
	}
	hwSessionBound(t, db, "native-collie", "opencode/catalog")
	legacy := "rollout-" + trace.OpenCodeID("ses_collie") + ".jsonl"
	id := &RunIdentity{Tier: "collie", RunID: "native-collie"}
	if _, err := RegisterSession(db, id, id.RunID, "ses_collie", legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterSession(db, id, id.RunID, "ses_collie", trace.OpenCodeDatabase); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := db.QueryRow(`SELECT trace_filename FROM collie_sessions WHERE id=?`, id.RunID).Scan(&stored); err != nil || stored != legacy {
		t.Fatalf("retained locator changed: %q, %v", stored, err)
	}
	listed, err := TraceSessions(db)
	if err != nil {
		t.Fatal(err)
	}
	rows := listed.(map[string]any)["sessions"].([]TraceSession)
	if len(rows) != 3 {
		t.Fatalf("native traces: %+v", rows)
	}
	for _, row := range rows {
		if row.Format != "opencode" || row.TraceFilename != trace.OpenCodeDatabase || row.LegacyFilename == "" || row.AgentsViewSession != "opencode:"+row.NativeSessionRef {
			t.Fatalf("native trace: %+v", row)
		}
	}
	evaluation, err := EvaluationTrace(db, "packager")
	if err != nil || evaluation.(map[string]any)["trace_filename"] != trace.OpenCodeDatabase {
		t.Fatalf("evaluation native trace: %+v, %v", evaluation, err)
	}
}

func TestTraceSessionsListIsEmptyWithoutAMainTrace(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `INSERT INTO runs (id, tier, role, binding) VALUES ('r-merge', 'pipeline', 'worker', 'mechanical')`)
	listed, err := TraceSessions(db)
	if err != nil {
		t.Fatal(err)
	}
	sessions, ok := listed.(map[string]any)["sessions"].([]TraceSession)
	if !ok || sessions == nil || len(sessions) != 0 {
		t.Fatalf("trace sessions = %#v, want an empty list", listed)
	}
}
