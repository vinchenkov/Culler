package main_test

import (
	"encoding/json"
	"testing"
)

func TestRunAndCollieListsPrintTheAgentsViewSession(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	const claudeRef = "11111111-1111-4111-8111-111111111111"
	const codexRef = "22222222-2222-4222-8222-222222222222"
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO runs (id, tier, role, binding, native_session_ref, trace_filename)
			VALUES ('r-claude', 'pipeline', 'worker', 'claude-sdk/claude', ?, 'native.jsonl')`, []any{claudeRef}},
		{`INSERT INTO runs (id, tier, role, binding) VALUES ('r-merge', 'pipeline', 'worker', 'mechanical')`, nil},
		{`INSERT INTO collie_sessions (id, container_name, verb_allowlist, session_path, binding,
			native_session_ref, trace_filename)
			VALUES ('h-codex', 'mc-collie-h-codex', '[]', 'sessions/h-codex', 'codex/chatgpt', ?, ?)`,
			[]any{codexRef, "2026/09/29/rollout-2026-09-29T10-00-00-" + codexRef + ".jsonl"}},
		{`INSERT INTO collie_sessions (id, container_name, verb_allowlist, session_path, binding)
			VALUES ('h-pending', 'mc-collie-h-pending', '[]', 'sessions/h-pending', 'claude-sdk/claude')`, nil},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	agentsView := func(args []string, listKey string) map[string]string {
		t.Helper()
		res := runMC(t, spineEnv(spine), "", args...)
		if res.code != 0 {
			t.Fatalf("%v failed (%d): %s", args, res.code, res.stderr)
		}
		out := map[string]string{}
		for _, raw := range res.json[listKey].([]any) {
			row := raw.(map[string]any)
			// The current wiki of the fixture holds one Scribe run.
			if row["role"] == "scribe" {
				continue
			}
			value, present := row["agentsview"]
			if !present {
				t.Fatalf("%v row lacks the agentsview key: %v", args, row)
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			out[row["id"].(string)] = string(encoded)
		}
		return out
	}
	runs := agentsView([]string{"run", "list"}, "runs")
	if runs["r-claude"] != `{"path":"/sessions/`+claudeRef+`?msg=last","session":"`+claudeRef+`"}` ||
		runs["r-merge"] != "null" || len(runs) != 2 {
		t.Fatalf("run list agentsview = %v", runs)
	}
	sessions := agentsView([]string{"collie", "list"}, "sessions")
	if sessions["h-codex"] != `{"path":"/sessions/codex/`+codexRef+`?msg=last","session":"codex:`+codexRef+`"}` ||
		sessions["h-pending"] != "null" || len(sessions) != 2 {
		t.Fatalf("collie list agentsview = %v", sessions)
	}
}
