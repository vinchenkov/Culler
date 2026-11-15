package verbs

import (
	"reflect"
	"testing"
)

const (
	avClaudeRef = "11111111-1111-4111-8111-111111111111"
	avCodexRef  = "22222222-2222-4222-8222-222222222222"
)

func TestRunListCarriesTheAgentsViewSessionOfEachMainTrace(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `INSERT INTO runs (id, tier, role, binding, native_session_ref, trace_filename)
		VALUES ('r-claude', 'pipeline', 'worker', 'claude-sdk/claude', ?, 'native.jsonl')`, avClaudeRef)
	dvExec(t, db, `INSERT INTO runs (id, tier, role, binding, native_session_ref, trace_filename)
		VALUES ('r-codex', 'pipeline', 'verifier', 'codex/chatgpt', ?, ?)`,
		avCodexRef, "2026/09/29/rollout-2026-09-29T10-00-00-"+avCodexRef+".jsonl")
	dvExec(t, db, `INSERT INTO runs (id, tier, role, binding) VALUES ('r-merge', 'pipeline', 'worker', 'mechanical')`)
	dvExec(t, db, `INSERT INTO runs (id, tier, role, binding) VALUES ('r-pending', 'pipeline', 'worker', 'claude-sdk/claude')`)
	dvExec(t, db, `INSERT INTO runs (id, tier, role, binding, native_session_ref, trace_filename)
		VALUES ('r-fake', 'pipeline', 'worker', 'fake/fake', ?, 'native.jsonl')`, avClaudeRef)
	dvExec(t, db, `INSERT INTO runs (id, tier) VALUES ('r-unbound', 'pipeline')`)

	res, err := RunList(db, RunSelection{})
	if err != nil {
		t.Fatal(err)
	}
	rows := res.(map[string]any)["runs"].([]map[string]any)
	want := map[any]any{
		"r-claude": map[string]any{"session": avClaudeRef, "path": "/sessions/" + avClaudeRef + "?msg=last"},
		"r-codex": map[string]any{
			"session": "codex:" + avCodexRef, "path": "/sessions/codex/" + avCodexRef + "?msg=last",
		},
		"r-merge": nil, "r-pending": nil, "r-fake": nil, "r-unbound": nil,
	}
	if len(rows) != len(want) {
		t.Fatalf("run list = %#v", rows)
	}
	for _, row := range rows {
		got, present := row["agentsview"]
		if !present || !reflect.DeepEqual(got, want[row["id"]]) {
			t.Errorf("run %v agentsview = %#v, want %#v", row["id"], got, want[row["id"]])
		}
		if update, present := row["wiki_update"]; !present || update != nil {
			t.Errorf("run %v wiki_update = %#v, want a null value for a run with no wiki update", row["id"], update)
		}
		if len(row) != 18 {
			t.Errorf("run %v has %d keys, want the 16 columns, agentsview, and wiki_update: %#v", row["id"], len(row), row)
		}
	}
	if rows[0]["binding"] != "claude-sdk/claude" || rows[0]["native_session_ref"] != avClaudeRef ||
		rows[0]["trace_filename"] != "native.jsonl" {
		t.Fatalf("the computed key changed the locator columns: %#v", rows[0])
	}
}

func TestCollieListCarriesTheAgentsViewSessionOfEachMainTrace(t *testing.T) {
	db := dvSpine(t)
	hwSessionBound(t, db, "h-claude", "claude-sdk/claude")
	if _, err := RegisterSession(db, &RunIdentity{Tier: "collie", RunID: "h-claude"}, "h-claude", avClaudeRef, "native.jsonl"); err != nil {
		t.Fatal(err)
	}
	hwSessionBound(t, db, "h-codex", "codex/chatgpt")
	if _, err := RegisterSession(db, &RunIdentity{Tier: "collie", RunID: "h-codex"}, "h-codex", avCodexRef,
		"2026/09/29/rollout-2026-09-29T10-00-00-"+avCodexRef+".jsonl"); err != nil {
		t.Fatal(err)
	}
	hwSessionBound(t, db, "h-pending", "claude-sdk/claude")
	hwSessionBound(t, db, "h-fake", "fake/fake")
	if _, err := RegisterSession(db, &RunIdentity{Tier: "collie", RunID: "h-fake"}, "h-fake", avClaudeRef, "native.jsonl"); err != nil {
		t.Fatal(err)
	}

	listed, err := CollieList(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := listed.(map[string]any)["sessions"].([]map[string]any)
	want := map[any]any{
		"h-claude": map[string]any{"session": avClaudeRef, "path": "/sessions/" + avClaudeRef + "?msg=last"},
		"h-codex": map[string]any{
			"session": "codex:" + avCodexRef, "path": "/sessions/codex/" + avCodexRef + "?msg=last",
		},
		"h-pending": nil, "h-fake": nil,
	}
	if len(rows) != len(want) {
		t.Fatalf("collie list = %#v", rows)
	}
	for _, row := range rows {
		got, present := row["agentsview"]
		if !present || !reflect.DeepEqual(got, want[row["id"]]) {
			t.Errorf("session %v agentsview = %#v, want %#v", row["id"], got, want[row["id"]])
		}
		if row["binding"] == nil || row["session_path"] != "sessions/"+row["id"].(string) {
			t.Errorf("the computed key changed the session row: %#v", row)
		}
	}
}
