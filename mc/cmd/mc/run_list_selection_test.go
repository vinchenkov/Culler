package main_test

import (
	"slices"
	"strconv"
	"testing"
)

// A Collie session reads run records to find the trace of a run. The read
// needs no entry in the verb allowlist, so a read-only session has it too.
func TestCollieWithAnEmptyAllowlistSelectsRuns(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	env := collieJSONEnv(t, spine, "h-read-only", []string{})
	list := func(args ...string) []string {
		t.Helper()
		res := runMC(t, env, "", append([]string{"run", "list"}, args...)...)
		if res.code != 0 {
			t.Fatalf("run list %v failed (%d): %s", args, res.code, res.stderr)
		}
		var ids []string
		for _, raw := range res.json["runs"].([]any) {
			ids = append(ids, raw.(map[string]any)["id"].(string))
		}
		return ids
	}
	// The fixture Project can hold runs of its own, such as its wiki update.
	fixtureRuns := list()
	task := proposalTask(t, spine, "the task to explain")
	otherTask := proposalTask(t, spine, "another task")
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO sandbox_profiles (id, workspace_root) VALUES ('other-profile', '/workspaces/other')`, nil},
		{`INSERT INTO projects (id, title, sandbox_profile) VALUES ('other', 'Other', 'other-profile')`, nil},
		{`INSERT INTO runs (id, tier, role, project, subject, created_at)
			VALUES ('r-task', 'pipeline', 'worker', 'ws-test', ?, '2026-07-25 10:00:00')`, []any{task}},
		{`INSERT INTO runs (id, tier, role, project, subject, created_at)
			VALUES ('r-other-task', 'pipeline', 'worker', 'ws-test', ?, '2026-07-25 11:00:00')`, []any{otherTask}},
		{`INSERT INTO runs (id, tier, role, project, created_at)
			VALUES ('r-other-project', 'pipeline', 'strategist', 'other', '2026-07-25 12:00:00')`, nil},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}

	runs := func(args ...string) []string {
		t.Helper()
		var ids []string
		for _, id := range list(args...) {
			if !slices.Contains(fixtureRuns, id) {
				ids = append(ids, id)
			}
		}
		return ids
	}
	if all := list(); len(all) != len(fixtureRuns)+3 {
		t.Errorf("run list without a selection = %v, want each fixture run %v and three more", all, fixtureRuns)
	}
	taskID := strconv.FormatInt(task, 10)
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{nil, []string{"r-task", "r-other-task", "r-other-project"}},
		{[]string{"--task", taskID}, []string{"r-task"}},
		{[]string{"--project", "other"}, []string{"r-other-project"}},
		{[]string{"--task", taskID, "--project", "ws-test"}, []string{"r-task"}},
		{[]string{"--task", taskID, "--project", "other"}, nil},
	} {
		if got := runs(tc.args...); !slices.Equal(got, tc.want) {
			t.Errorf("run list %v = %v, want %v", tc.args, got, tc.want)
		}
	}

	for _, bad := range [][]string{
		{"--task", "abc"},
		{"--task", "-1"},
		{"unexpected"},
	} {
		res := runMC(t, env, "", append([]string{"run", "list"}, bad...)...)
		if res.code != 2 {
			t.Errorf("run list %v exited %d, want a usage error: %s", bad, res.code, res.stderr)
		}
	}
}
