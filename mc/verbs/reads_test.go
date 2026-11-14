package verbs

import (
	"database/sql"
	"maps"
	"slices"
	"testing"
	"time"
)

// rlSeed builds a pool with one archived row, two live ones at different
// stages, and a second Project — enough to pin the default filter, the
// ordering, and the Project restriction.
func rlSeed(t *testing.T) *sql.DB {
	t.Helper()
	db := dvSpine(t)
	dvExec(t, db, `INSERT INTO sandbox_profiles (id, workspace_root) VALUES ('other-profile', '/workspaces/other')`)
	dvExec(t, db, `INSERT INTO projects (id, title, sandbox_profile) VALUES ('other', 'Other', 'other-profile')`)
	dvExec(t, db, `INSERT INTO tasks (id, title, scope, priority, created_at, status, origin, project)
		VALUES (11,'live proposal','task',2,'2026-07-25 09:00:00','proposed','user',?)`, "ws-test")
	dvExec(t, db, `INSERT INTO tasks (id, title, scope, priority, created_at, status, origin, project)
		VALUES (12,'further along','task',2,'2026-07-25 08:00:00','proposed','user',?)`, "ws-test")
	dvExec(t, db, `UPDATE tasks SET status='seeded' WHERE id=12`)
	dvExec(t, db, `INSERT INTO tasks (id, title, scope, priority, created_at, status, origin, project)
		VALUES (13,'elsewhere','task',2,'2026-07-25 07:00:00','proposed','user','other')`)
	dvExec(t, db, `INSERT INTO tasks (id, title, scope, priority, created_at, status, origin, project)
		VALUES (14,'finished','task',2,'2026-07-20 09:00:00','proposed','user',?)`, "ws-test")
	// A task cannot be archived until it has been decided, and an approval
	// additionally requires a checked submitted version. Cancellation is the
	// archival path that needs no packet, so that is how the fixture retires
	// this row.
	dvExec(t, db, `UPDATE tasks SET decision='cancelled', decided_at='2026-07-20 10:00:00', archived=1 WHERE id=14`)
	return db
}

func rlIDs(t *testing.T, res any, key string) []int64 {
	t.Helper()
	rows, ok := res.(map[string]any)[key].([]map[string]any)
	if !ok {
		t.Fatalf("%s is not a row list: %#v", key, res)
	}
	var ids []int64
	for _, row := range rows {
		id, ok := row["id"].(int64)
		if !ok {
			t.Fatalf("row has no int64 id: %#v", row)
		}
		ids = append(ids, id)
	}
	return ids
}

func TestRunListUsesCreationSequenceAtTheSameTimestamp(t *testing.T) {
	db := dvSpine(t)
	for _, id := range []string{"z-older", "a-newer"} {
		dvExec(t, db, `INSERT INTO runs (id, tier, created_at)
			VALUES (?, 'pipeline', '2026-08-06 12:00:00')`, id)
	}
	res, err := RunList(db, RunSelection{})
	if err != nil {
		t.Fatal(err)
	}
	rows := res.(map[string]any)["runs"].([]map[string]any)
	if len(rows) != 2 || rows[0]["id"] != "z-older" || rows[1]["id"] != "a-newer" {
		t.Fatalf("same-second run order = %#v, want causal insertion order", rows)
	}
}

func TestRunListCarriesTheVerifierVerdict(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `INSERT INTO runs (id, tier, role, verdict_outcome) VALUES ('v-correct', 'pipeline', 'verifier', 'correct')`)
	dvExec(t, db, `INSERT INTO runs (id, tier, role) VALUES ('w-plain', 'pipeline', 'worker')`)
	res, err := RunList(db, RunSelection{})
	if err != nil {
		t.Fatal(err)
	}
	rows := res.(map[string]any)["runs"].([]map[string]any)
	verdicts := map[any]any{}
	for _, row := range rows {
		verdicts[row["id"]] = row["verdict_outcome"]
	}
	if verdicts["v-correct"] != "correct" || verdicts["w-plain"] != nil {
		t.Fatalf("run list verdicts = %#v, want the correct verdict and a null verdict", verdicts)
	}
}

// One response holds each run of the deployment, and that list only grows. A
// reader that needs the runs of one task or of one Project selects them.
func TestRunListSelectsTheRunsOfOneTaskOrOneProject(t *testing.T) {
	db := rlSeed(t)
	dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject, created_at)
		VALUES ('worker-11', 'pipeline', 'worker', 'ws-test', 11, '2026-07-25 10:00:00')`)
	dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject, created_at)
		VALUES ('verifier-11', 'pipeline', 'verifier', 'ws-test', 11, '2026-07-25 11:00:00')`)
	dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject, created_at)
		VALUES ('worker-12', 'pipeline', 'worker', 'ws-test', 12, '2026-07-25 12:00:00')`)
	dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject, created_at)
		VALUES ('worker-13', 'pipeline', 'worker', 'other', 13, '2026-07-25 13:00:00')`)
	dvExec(t, db, `INSERT INTO runs (id, tier, role, project, created_at)
		VALUES ('strategist-other', 'pipeline', 'strategist', 'other', '2026-07-25 14:00:00')`)

	rows := func(selection RunSelection) []map[string]any {
		t.Helper()
		res, err := RunList(db, selection)
		if err != nil {
			t.Fatalf("RunList(%+v): %v", selection, err)
		}
		return res.(map[string]any)["runs"].([]map[string]any)
	}
	ids := func(selection RunSelection) []string {
		t.Helper()
		var out []string
		for _, row := range rows(selection) {
			out = append(out, row["id"].(string))
		}
		return out
	}
	for _, tc := range []struct {
		name      string
		selection RunSelection
		want      []string
	}{
		{"no selection", RunSelection{}, []string{"worker-11", "verifier-11", "worker-12", "worker-13", "strategist-other"}},
		{"one task", RunSelection{Task: 11}, []string{"worker-11", "verifier-11"}},
		{"one Project", RunSelection{Project: "other"}, []string{"worker-13", "strategist-other"}},
		{"a task and its Project", RunSelection{Task: 12, Project: "ws-test"}, []string{"worker-12"}},
		{"a task of another Project", RunSelection{Task: 13, Project: "ws-test"}, nil},
		{"an absent task", RunSelection{Task: 999}, nil},
	} {
		if got := ids(tc.selection); !slices.Equal(got, tc.want) {
			t.Errorf("%s: runs = %v, want %v", tc.name, got, tc.want)
		}
	}

	// A selection changes the set of rows only. Each row keeps its fields.
	fields := func(row map[string]any) []string {
		keys := slices.Collect(maps.Keys(row))
		slices.Sort(keys)
		return keys
	}
	all, selected := rows(RunSelection{}), rows(RunSelection{Task: 11})
	if !slices.Equal(fields(all[0]), fields(selected[0])) {
		t.Fatalf("selected run fields = %v, want %v", fields(selected[0]), fields(all[0]))
	}
}

// The default is LIVE work. Archived tasks are never deleted and accumulate
// for the deployment's lifetime, and the Board polls this verb — an unfiltered
// default would grow without bound under a tab that refreshes every 10s.
func TestTaskListReturnsLiveWorkByDefault(t *testing.T) {
	db := rlSeed(t)
	res, err := TaskList(db, false, "")
	if err != nil {
		t.Fatalf("TaskList: %v", err)
	}
	for _, id := range rlIDs(t, res, "tasks") {
		if id == 14 {
			t.Fatal("the archived task is in the default listing")
		}
	}
}

func TestTaskListAllIncludesArchived(t *testing.T) {
	db := rlSeed(t)
	res, err := TaskList(db, true, "")
	if err != nil {
		t.Fatalf("TaskList: %v", err)
	}
	found := false
	for _, id := range rlIDs(t, res, "tasks") {
		found = found || id == 14
	}
	if !found {
		t.Fatal("--all did not include the archived task")
	}
}

// Furthest-first, then oldest-first — the order a board reads top to bottom,
// and stable across polls so cards do not shuffle under the operator.
func TestTaskListOrdersFurthestFirstThenOldest(t *testing.T) {
	db := rlSeed(t)
	res, err := TaskList(db, false, "")
	if err != nil {
		t.Fatalf("TaskList: %v", err)
	}
	got := rlIDs(t, res, "tasks")
	// 12 is seeded (rank 2); 11 and 13 are proposed (rank 1), oldest first.
	want := []int64{12, 13, 11}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestTaskListRestrictsToOneProject(t *testing.T) {
	db := rlSeed(t)
	res, err := TaskList(db, false, "other")
	if err != nil {
		t.Fatalf("TaskList: %v", err)
	}
	got := rlIDs(t, res, "tasks")
	if len(got) != 1 || got[0] != 13 {
		t.Fatalf("project filter returned %v, want just [13]", got)
	}
}

func alSeed(t *testing.T) *sql.DB {
	t.Helper()
	db := dvSpine(t)
	for _, row := range []struct {
		created, actor, kind, detail string
	}{
		{"2026-07-25 09:00:00", "resident", "dispatch.health", `{"code":"mount.denied"}`},
		{"2026-07-25 09:01:00", "strategist", "daily.briefing", "delivered"},
		{"2026-07-25 09:02:00", "arbiter", "task.promoted", "8,9"},
	} {
		dvExec(t, db, `INSERT INTO activity (created_at, actor, kind, detail) VALUES (?,?,?,?)`,
			row.created, row.actor, row.kind, row.detail)
	}
	return db
}

// Newest first: an activity feed is read from the top, and the tail is the
// part an operator scrolls to, not the part they wait for.
func TestActivityListReturnsNewestFirst(t *testing.T) {
	db := alSeed(t)
	res, err := ActivityList(db, "", 0, "")
	if err != nil {
		t.Fatalf("ActivityList: %v", err)
	}
	rows := res.(map[string]any)["activity"].([]map[string]any)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if rows[0]["kind"] != "task.promoted" {
		t.Fatalf("first row is %v, want the newest (task.promoted)", rows[0]["kind"])
	}
}

// `kind` is a WHERE clause, not a classification. The dashboard's separate
// error stream is the dispatch.health kind, and deciding which kinds are
// errors belongs to the caller — the verb only filters.
func TestActivityListFiltersByKind(t *testing.T) {
	db := alSeed(t)
	res, err := ActivityList(db, "dispatch.health", 0, "")
	if err != nil {
		t.Fatalf("ActivityList: %v", err)
	}
	rows := res.(map[string]any)["activity"].([]map[string]any)
	if len(rows) != 1 || rows[0]["kind"] != "dispatch.health" {
		t.Fatalf("kind filter returned %#v", rows)
	}
}

// The limit is clamped, not trusted: the table is written from a dozen sites
// and never pruned, so an absent or absurd limit must not become "all rows".
func TestActivityListClampsItsLimit(t *testing.T) {
	db := alSeed(t)
	for _, limit := range []int{0, -5} {
		res, err := ActivityList(db, "", limit, "")
		if err != nil {
			t.Fatalf("ActivityList(%d): %v", limit, err)
		}
		if got := res.(map[string]any)["limit"]; got != activityListDefaultLimit {
			t.Fatalf("limit %d became %v, want the default %d", limit, got, activityListDefaultLimit)
		}
	}
	res, err := ActivityList(db, "", activityListMaxLimit*10, "")
	if err != nil {
		t.Fatalf("ActivityList(huge): %v", err)
	}
	if got := res.(map[string]any)["limit"]; got != activityListMaxLimit {
		t.Fatalf("an oversized limit became %v, want the ceiling %d", got, activityListMaxLimit)
	}
}

func TestActivityListHonoursAnExplicitLimit(t *testing.T) {
	db := alSeed(t)
	res, err := ActivityList(db, "", 2, "")
	if err != nil {
		t.Fatalf("ActivityList: %v", err)
	}
	if rows := res.(map[string]any)["activity"].([]map[string]any); len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
}

func TestSettingsGetCarriesTheReviewWIPCap(t *testing.T) {
	db := dvSpine(t)
	res, err := SettingsGet(db)
	if err != nil {
		t.Fatal(err)
	}
	row := res.(map[string]any)
	cap, ok := row["review_wip_cap"].(int64)
	if !ok || cap < 1 {
		t.Fatalf("settings get review_wip_cap = %#v, want a positive count (the Pipeline pane's backpressure cap)", row["review_wip_cap"])
	}
}

// mc lease list is one row per Project in id order, held or free, with
// null fields on a free lease.
func TestLeaseListIsOneRowPerProjectInIDOrder(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `INSERT INTO sandbox_profiles (id, workspace_root) VALUES ('a-first-profile', '/workspaces/a-first')`)
	dvExec(t, db, `INSERT INTO projects (id, title, sandbox_profile, status) VALUES ('a-first', 'A first', 'a-first-profile', 'paused')`)
	dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject) VALUES ('held-run', 'pipeline', 'worker', 'ws-test', NULL)`)
	dvHold(t, db, "ws-test", "held-run", "worker", nil, 0, nil, time.Hour)
	res, err := LeaseList(db)
	if err != nil {
		t.Fatal(err)
	}
	rows := res.(map[string]any)["leases"].([]map[string]any)
	if len(rows) != 2 {
		t.Fatalf("lease list rows = %d, want one per Project", len(rows))
	}
	if rows[0]["project"] != "a-first" || rows[0]["status"] != "free" || rows[0]["run_id"] != nil {
		t.Fatalf("first row = %v, want the free a-first lease", rows[0])
	}
	if rows[1]["project"] != "ws-test" || rows[1]["status"] != "held" || rows[1]["run_id"] != "held-run" || rows[1]["owner"] != "worker" {
		t.Fatalf("second row = %v, want the held ws-test lease", rows[1])
	}
}

func TestActivityProjectFilterPrecedesLimit(t *testing.T) {
	db := rlSeed(t)
	dvExec(t, db, `INSERT INTO activity(actor,kind,subject,detail) VALUES ('operator','task.test','11','selected')`)
	for range 12 {
		dvExec(t, db, `INSERT INTO activity(actor,kind,subject,detail) VALUES ('operator','task.test','13','other')`)
	}
	result, err := ActivityList(db, "task.test", 1, "ws-test")
	if err != nil {
		t.Fatal(err)
	}
	rows := result.(map[string]any)["activity"].([]map[string]any)
	if len(rows) != 1 || rows[0]["detail"] != "selected" || rows[0]["project"] != "ws-test" {
		t.Fatalf("selected rows: %#v", rows)
	}
}

func TestActivityProjectAssociation(t *testing.T) {
	db := rlSeed(t)
	dvExec(t, db, `INSERT INTO runs(id,tier,project) VALUES ('run-source','pipeline','ws-test')`)
	for _, row := range []struct {
		kind, subject, actor, result string
		own                          bool
	}{
		{"task.test", "11", "operator", "", true},
		{"task.test", "14", "operator", "", true},
		{"project.self_archived", "ws-test", "system", "", true},
		{"dispatch.result", "", "dispatch", `{"project":"ws-test"}`, true},
		{"run.test", "run-source", "system", "", true},
		{"run.test", "", "run-source", "", true},
		{"collie.test", "11", "operator", "", false},
		{"task.test", "11junk", "operator", "", false},
		{"dispatch.health", "", "dispatch", "", false},
		{"task.test", "13", "operator", "", false},
	} {
		t.Run(row.kind+row.subject+row.actor, func(t *testing.T) {
			res, err := db.Exec(`INSERT INTO activity(actor,kind,subject,dispatch_result,dispatch_request_id) VALUES (?,?,?,?,?)`, row.actor, row.kind, nullIfEmpty(row.subject), nullIfEmpty(row.result), func() any {
				if row.result != "" {
					return "0123456789abcdef"
				}
				return nil
			}())
			if err != nil {
				t.Fatal(err)
			}
			id, _ := res.LastInsertId()
			listed, err := ActivityList(db, "", 1000, "ws-test")
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, item := range listed.(map[string]any)["activity"].([]map[string]any) {
				if item["id"] == id {
					found = true
				}
			}
			if found != row.own {
				t.Fatalf("included=%v, want %v", found, row.own)
			}
		})
	}
}
