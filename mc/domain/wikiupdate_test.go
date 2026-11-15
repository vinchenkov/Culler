// Wiki update suite: the accepted commit, the start with no lease, the one
// end of the run and of the wiki update, the recovery rule, the fence, and
// the wiki state.
package domain_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"mc/domain"
)

var (
	wikiA = strings.Repeat("a", 40)
	wikiB = strings.Repeat("b", 40)
	wikiC = strings.Repeat("c", 40)
	wikiD = strings.Repeat("d", 40)
)

const testNowText = "2026-07-12 10:00:00"

// startWiki starts one wiki update of the Project at the commit.
func startWiki(t *testing.T, db *sql.DB, project, run, commit string) {
	t.Helper()
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		return domain.StartWikiUpdate(ctx, q, testNow, domain.WikiStart{
			Project: project, RunID: run, StartCommit: commit,
			SessionPath: "sessions/" + run, Binding: "fake/fake", HardDeadlineMinutes: 240,
		})
	})
}

// endWikiRun ends the run of a wiki update and returns the result of the function.
func endWikiRun(t *testing.T, db *sql.DB, run string, end domain.WikiRunEnd) bool {
	t.Helper()
	var ended bool
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		var err error
		ended, err = domain.EndWikiRun(ctx, q, run, end)
		return err
	})
	return ended
}

// endWikiUpdate ends a wiki update whose run has an end.
func endWikiUpdate(t *testing.T, db *sql.DB, run string, end domain.WikiEnd) {
	t.Helper()
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		return domain.EndWikiUpdate(ctx, q, run, end)
	})
}

// endRunOnly ends the run and leaves its wiki update with no end and no wiki
// commit. No function of the domain writes this state.
func endRunOnly(t *testing.T, db *sql.DB, run string) {
	t.Helper()
	mustExec(t, db, `UPDATE runs SET ended_at = datetime('now'), outcome = 'interrupted', end_reason = 'interrupted' WHERE id = ?`, run)
}

// commitTx runs fn under BEGIN IMMEDIATE and commits each write of fn, also
// after an error of fn. A write before the error thus stays visible.
func commitTx(t *testing.T, db *sql.DB, fn func(ctx context.Context, q domain.Q) error) error {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("begin immediate: %v", err)
	}
	result := fn(ctx, conn)
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return result
}

func wikiState(t *testing.T, db *sql.DB, project string) domain.WikiState {
	t.Helper()
	var state domain.WikiState
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		var err error
		state, err = domain.WikiStateOf(ctx, q, project)
		return err
	})
	return state
}

// wikiStr reads one column of the wiki update of the run.
func wikiStr(t *testing.T, db *sql.DB, run, col string) string {
	t.Helper()
	return oneStr(t, db, `SELECT `+col+` FROM wiki_updates WHERE run_id = ?`, run)
}

func runStr(t *testing.T, db *sql.DB, run, col string) string {
	t.Helper()
	return oneStr(t, db, `SELECT `+col+` FROM runs WHERE id = ?`, run)
}

// rowsText returns each row of the query as one line of text.
func rowsText(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i, v := range vals {
			if raw, ok := v.([]byte); ok {
				vals[i] = string(raw)
			}
		}
		fmt.Fprintln(&out, vals...)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// spineText returns each row that a wiki update function can write, and each
// row that it must leave unchanged.
func spineText(t *testing.T, db *sql.DB) string {
	t.Helper()
	var out strings.Builder
	for _, table := range []string{"runs", "wiki_updates", "accepted_commits", "activity", "leases", "projects", "tasks", "infrastructure_failures"} {
		out.WriteString(table + "\n" + rowsText(t, db, `SELECT * FROM `+table+` ORDER BY 1`))
	}
	return out.String()
}

// brokenWikiUpdates counts the wiki updates with an ended run, no end, and no wiki commit.
func brokenWikiUpdates(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	return oneInt(t, db, `SELECT COUNT(*) FROM wiki_updates w JOIN runs r ON r.id = w.run_id
		WHERE r.ended_at IS NOT NULL AND w.ended_at IS NULL AND w.wiki_commit IS NULL`)
}

// endRecords returns each end record of the run as "actor|project|detail" lines.
func endRecords(t *testing.T, db *sql.DB, run string) string {
	t.Helper()
	return rowsText(t, db, `SELECT actor || '|' || project || '|' || detail FROM activity
		WHERE kind = 'wiki.update_ended' AND subject = ? ORDER BY id`, run)
}

// mkAppliedMerge records one completed merge of a task of the fixture Project
// into culler. It returns the sequence of the run of that merge.
func mkAppliedMerge(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	task := mkTask(t, db, "task", "packaged")
	candidate := seedMergedCandidate(t, db, task)
	owner := registerOwner(t, db)
	run := fmt.Sprintf("apply-%d", task)
	mustExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,session_path,binding)
		VALUES(?,'pipeline','worker','ws',?,?,'mechanical')`, run, task, "sessions/"+run)
	mustExec(t, db, `INSERT INTO repository_executions
		(run_id,project,task_id,operation,target,owner,effect,boot_id,heartbeat_uptime_ms,deadline_uptime_ms)
		VALUES(?,'ws',?,'apply',?,?,'{}','boot',0,600000)`, run, task, fmt.Sprintf("task/%d", task), owner)
	mustExec(t, db, `INSERT INTO merge_operations(run_id,task_id,project,operation,version_id,candidate_id,destination_branch)
		SELECT ?,id,project,'apply',submitted_version,?,'culler' FROM tasks WHERE id = ?`, run, candidate, task)
	mustExec(t, db, `UPDATE merge_operations SET result = ?, finished = 1 WHERE run_id = ?`,
		`{"applied":{"commit":"`+wikiD+`"}}`, run)
	mustExec(t, db, `UPDATE runs SET ended_at = datetime('now'), outcome = 'completed' WHERE id = ?`, run)
	mustExec(t, db, `UPDATE repository_executions SET ended_at = datetime('now'), end_reason = 'result',
		stopped_at = datetime('now') WHERE run_id = ?`, run)
	return oneInt(t, db, `SELECT created_seq FROM runs WHERE id = ?`, run)
}

func TestRecordAcceptedCommit(t *testing.T) {
	db := openSpine(t)
	record := func(commit string) {
		t.Helper()
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.RecordAcceptedCommit(ctx, q, "ws", commit)
		})
	}
	merged := func(destination, commit string) {
		t.Helper()
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.RecordMergedCommit(ctx, q, "ws", destination, commit)
		})
	}
	stored := func() string {
		t.Helper()
		var commit string
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			var err error
			commit, err = domain.AcceptedCommit(ctx, q, "ws")
			return err
		})
		return commit
	}
	const past = "2020-01-01 00:00:00"
	age := func() {
		t.Helper()
		mustExec(t, db, `UPDATE accepted_commits SET observed_at = ? WHERE project = 'ws'`, past)
	}
	observed := func() string {
		t.Helper()
		return oneStr(t, db, `SELECT observed_at FROM accepted_commits WHERE project = 'ws'`)
	}

	if got := stored(); got != "" {
		t.Fatalf("accepted commit with no row = %q, want \"\"", got)
	}
	merged(wikiA, wikiB)
	record("")
	if n := oneInt(t, db, `SELECT COUNT(*) FROM accepted_commits`); n != 0 {
		t.Fatalf("rows after an empty observation and a merge with no stored value = %d, want 0", n)
	}

	record(wikiA)
	if got := stored(); got != wikiA {
		t.Fatalf("accepted commit after the first observation = %q, want %q", got, wikiA)
	}
	if _, err := time.Parse(domain.SpineTime, observed()); err != nil {
		t.Fatalf("observation time = %q, want the spine time format", observed())
	}

	age()
	record(wikiA)
	record("")
	if got := observed(); got != past {
		t.Fatalf("observation time after an equal and an empty observation = %q, want %q: the row changed", got, past)
	}

	record(wikiB)
	if got := stored(); got != wikiB || observed() == past {
		t.Fatalf("after a new observation: commit %q, time %q; want %q and a new time", got, observed(), wikiB)
	}

	// The commit of a merge replaces only the destination commit of that merge.
	age()
	merged(wikiB, wikiC)
	if got := stored(); got != wikiC || observed() == past {
		t.Fatalf("after a merge onto the stored commit: commit %q, time %q; want %q and a new time", got, observed(), wikiC)
	}
	age()
	merged(wikiB, wikiD)
	if got := stored(); got != wikiC || observed() != past {
		t.Fatalf("after a merge onto another commit: commit %q, time %q; want no change", got, observed())
	}

	for _, invalid := range []string{strings.Repeat("A", 40), strings.Repeat("a", 39), "culler"} {
		wantReject(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.RecordAcceptedCommit(ctx, q, "ws", invalid)
		})
	}
	wantReject(t, db, func(ctx context.Context, q domain.Q) error {
		return domain.RecordAcceptedCommit(ctx, q, "absent", wikiA)
	})
	if got := stored(); got != wikiC {
		t.Fatalf("accepted commit after the refused observations = %q, want %q", got, wikiC)
	}
}

func TestStartWikiUpdateClaimsNoLease(t *testing.T) {
	db := openSpine(t)
	mkProject(t, db, "other")
	marker := mkAppliedMerge(t, db)
	if marker < 1 {
		t.Fatalf("sequence of the merge run = %d, want a positive value", marker)
	}
	// An agent run holds the lease of the Project during the start.
	task := mkTask(t, db, "task", "seeded")
	if err := claim(t, db, domain.ClaimArgs{Project: "ws", RunID: "worker-1", Owner: "worker", SubjectID: &task,
		SessionPath: "sessions/worker-1", Binding: "fake", HardDeadlineMinutes: 240}); err != nil {
		t.Fatal(err)
	}
	leases := rowsText(t, db, `SELECT * FROM leases ORDER BY project`)
	activity := oneInt(t, db, `SELECT COUNT(*) FROM activity`)

	startWiki(t, db, "ws", "wiki-1", wikiA)

	if got := rowsText(t, db, `SELECT * FROM leases ORDER BY project`); got != leases {
		t.Fatalf("the start changed a lease:\nbefore:\n%s\nafter:\n%s", leases, got)
	}
	if got := oneStr(t, db, `SELECT tier || '|' || role || '|' || project || '|' || COALESCE(subject, 'none') || '|' ||
		session_path || '|' || binding || '|' || COALESCE(ended_at, 'active') || '|' || COALESCE(outcome, 'none')
		FROM runs WHERE id = 'wiki-1'`); got != "pipeline|scribe|ws|none|sessions/wiki-1|fake/fake|active|none" {
		t.Fatalf("run row = %q", got)
	}
	if got := oneStr(t, db, `SELECT project || '|' || start_commit || '|' || started_at || '|' || counted
		FROM wiki_updates WHERE run_id = 'wiki-1'`); got != "ws|"+wikiA+"|"+testNowText+"|1" {
		t.Fatalf("wiki update row = %q", got)
	}
	for _, col := range []string{"wiki_commit", "ended_at", "result", "cause", "last_heartbeat_at", "heartbeat_uptime_ms",
		"stopped_at", "merge_after", "destination_commit", "merged_commit"} {
		if got := wikiStr(t, db, "wiki-1", col); got != "<NULL>" {
			t.Fatalf("%s = %q at the start, want NULL", col, got)
		}
	}
	if got := oneInt(t, db, `SELECT merge_marker FROM wiki_updates WHERE run_id = 'wiki-1'`); got != marker {
		t.Fatalf("merge marker = %d, want %d: the sequence of the last merge of a task into culler", got, marker)
	}
	vm, err := domain.ReadVMClock()
	if err != nil {
		t.Fatal(err)
	}
	if got := wikiStr(t, db, "wiki-1", "boot_id"); got != vm.BootID || got == "" {
		t.Fatalf("boot_id = %q, want %q", got, vm.BootID)
	}
	if got := wikiStr(t, db, "wiki-1", "deadline_uptime_ms - acquired_uptime_ms"); got != "14400000" {
		t.Fatalf("deadline - start = %s ms, want 240 minutes", got)
	}
	if got := oneStr(t, db, `SELECT acquired_uptime_ms > 0 AND acquired_uptime_ms <= ? FROM wiki_updates WHERE run_id = 'wiki-1'`,
		vm.Uptime.Milliseconds()); got != "1" {
		t.Fatal("the start uptime is outside the uptime of the VM clock")
	}
	if got := oneInt(t, db, `SELECT COUNT(*) FROM activity`); got != activity {
		t.Fatalf("activity records = %d, want %d: the start writes none", got, activity)
	}

	// A Project with no merge has the marker 0. A run with no binding text has a null binding.
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		return domain.StartWikiUpdate(ctx, q, testNow, domain.WikiStart{
			Project: "other", RunID: "wiki-2", StartCommit: wikiB, SessionPath: "sessions/wiki-2", HardDeadlineMinutes: 1,
		})
	})
	if got := oneStr(t, db, `SELECT w.merge_marker || '|' || COALESCE(r.binding, 'none') || '|' ||
		(w.deadline_uptime_ms - w.acquired_uptime_ms) FROM wiki_updates w JOIN runs r ON r.id = w.run_id
		WHERE w.run_id = 'wiki-2'`); got != "0|none|60000" {
		t.Fatalf("second wiki update = %q, want the marker 0, no binding, and a deadline of 1 minute", got)
	}
	if got := rowsText(t, db, `SELECT * FROM leases ORDER BY project`); got != leases {
		t.Fatal("the second start changed a lease")
	}
}

func TestStartWikiUpdateBesideAnActiveWikiUpdateWritesNothing(t *testing.T) {
	db := openSpine(t)
	mkProject(t, db, "other")
	startWiki(t, db, "ws", "wiki-1", wikiA)
	before := spineText(t, db)

	err := commitTx(t, db, func(ctx context.Context, q domain.Q) error {
		return domain.StartWikiUpdate(ctx, q, testNow, domain.WikiStart{
			Project: "ws", RunID: "wiki-2", StartCommit: wikiB, SessionPath: "sessions/wiki-2",
			Binding: "fake/fake", HardDeadlineMinutes: 240,
		})
	})
	var refusal *domain.DomainError
	if !errors.As(err, &refusal) || refusal.Code != domain.CodeStaleRun || refusal.Msg != "the Project has an active wiki update" {
		t.Fatalf("second start = %v, want the stale-run refusal \"the Project has an active wiki update\"", err)
	}
	if got := spineText(t, db); got != before {
		t.Fatalf("the refused start wrote a row:\nbefore:\n%s\nafter:\n%s", before, got)
	}

	// The rule is for each Project. A wiki update that waits for its merge is active.
	startWiki(t, db, "other", "wiki-3", wikiA)
	if !endWikiRun(t, db, "wiki-1", domain.WikiRunEnd{Outcome: "completed", WikiCommit: wikiB}) {
		t.Fatal("the end of the run wrote nothing")
	}
	wantCode(t, db, domain.CodeStaleRun, func(ctx context.Context, q domain.Q) error {
		return domain.StartWikiUpdate(ctx, q, testNow, domain.WikiStart{
			Project: "ws", RunID: "wiki-4", StartCommit: wikiB, SessionPath: "sessions/wiki-4", HardDeadlineMinutes: 240,
		})
	})
	endWikiUpdate(t, db, "wiki-1", domain.WikiEnd{Result: "failed", Cause: "The merge failed."})
	startWiki(t, db, "ws", "wiki-5", wikiB)
	if n := oneInt(t, db, `SELECT COUNT(*) FROM wiki_updates WHERE project = 'ws'`); n != 2 {
		t.Fatalf("wiki updates of the Project = %d, want 2", n)
	}
}

func TestEndWikiRunEndsRunAndWikiUpdateTogether(t *testing.T) {
	type want struct {
		result  string // "<NULL>" while the wiki update is active
		cause   string
		commit  string
		counted int64
		stopped bool
		record  string // the detail of the end record; "" for no record
	}
	failed := func(cause string) want {
		return want{result: "failed", cause: cause, commit: "<NULL>", counted: 1, record: "The wiki update failed. " + cause}
	}
	reaped := func(reason, cause string) (domain.WikiRunEnd, want) {
		return domain.WikiRunEnd{Outcome: "reaped", EndReason: reason, Result: "failed", Cause: cause}, failed(cause)
	}
	reported := func(reason, cause string) (domain.WikiRunEnd, want) {
		return domain.WikiRunEnd{Outcome: "infra-failed", EndReason: reason, Result: "failed", Cause: cause}, failed(cause)
	}
	terminal := func(cause string) (domain.WikiRunEnd, want) {
		return domain.WikiRunEnd{Outcome: "completed", Result: "failed", Cause: cause}, failed(cause)
	}
	type testCase struct {
		name string
		end  domain.WikiRunEnd
		want want
	}
	add := func(cases []testCase, name string, end domain.WikiRunEnd, w want) []testCase {
		return append(cases, testCase{name, end, w})
	}
	var cases []testCase
	for _, c := range []struct{ name, reason, cause string }{
		{"no first heartbeat", "spawn-watchdog", "The Scribe run did not start in time."},
		{"heartbeat silence", "lease-timeout", "The heartbeat of the Scribe run stopped."},
		{"hard deadline", "hard-deadline", "The Scribe run reached its time limit."},
		{"container absent", "container-gone", "The container of the Scribe run stopped."},
		{"new boot identifier", "vm-restart", "Docker restarted during the Scribe run."},
	} {
		end, w := reaped(c.reason, c.cause)
		cases = add(cases, c.name, end, w)
	}
	cases = add(cases, "archived Project", domain.WikiRunEnd{Outcome: "interrupted", EndReason: "interrupted",
		Result: "failed", Cause: "The Project is archived."}, failed("The Project is archived."))
	for _, c := range []struct{ name, cause string }{
		{"failure report of the Scribe", "The Scribe reported a failure: The OpenWiki command failed."},
		{"customization conflict", "The custom instructions of the Scribe conflict with the Culler contract."},
	} {
		end, w := terminal(c.cause)
		cases = add(cases, c.name, end, w)
	}
	const checkText = "The wiki commit changes a path outside openwiki/: a.txt."
	checkFailed := failed(checkText)
	checkFailed.commit = wikiB
	cases = add(cases, "failed check of the wiki commit",
		domain.WikiRunEnd{Outcome: "completed", WikiCommit: wikiB, Result: "failed", Cause: checkText}, checkFailed)
	for _, c := range []struct{ name, reason, cause string }{
		{"runner report", "harness", "The agent program of the Scribe run stopped."},
		{"network error", "network", "A network error stopped the Scribe run."},
		{"provider error", "provider", "A provider error stopped the Scribe run."},
		{"launch failure", "launch", "The Scribe run did not start."},
		{"connection failure", "connection", "The connection of the Scribe run failed."},
	} {
		end, w := reported(c.reason, c.cause)
		cases = add(cases, c.name, end, w)
	}
	const refusedConnection = "The connection changed or became unavailable before launch."
	uncounted := failed(refusedConnection)
	uncounted.counted = 0
	cases = add(cases, "connection refused before launch", domain.WikiRunEnd{Outcome: "infra-failed", EndReason: "connection",
		Result: "failed", Cause: refusedConnection, NotCounted: true}, uncounted)
	const refusedStart = "Culler refused the start of the Scribe run. The Culler deployment is not ready (health.helper_unavailable)."
	refused := failed(refusedStart)
	refused.stopped = true
	cases = add(cases, "refused candidate", domain.WikiRunEnd{Outcome: "infra-failed", EndReason: "launch",
		Result: "failed", Cause: refusedStart, Stopped: true}, refused)
	cases = add(cases, "success with no wiki change", domain.WikiRunEnd{Outcome: "completed", WikiCommit: wikiA, Result: "succeeded"},
		want{result: "succeeded", cause: "<NULL>", commit: wikiA, counted: 1,
			record: "The wiki update succeeded. The Project wiki needed no change."})
	cases = add(cases, "wiki commit that waits for its merge", domain.WikiRunEnd{Outcome: "completed", WikiCommit: wikiB},
		want{result: "<NULL>", cause: "<NULL>", commit: wikiB, counted: 1})

	db := openSpine(t)
	// The fixture Project has a held lease and a used failure allowance. No end changes them.
	task := mkTask(t, db, "task", "seeded")
	if err := claim(t, db, domain.ClaimArgs{Project: "ws", RunID: "worker-1", Owner: "worker", SubjectID: &task,
		SessionPath: "sessions/worker-1", Binding: "fake", HardDeadlineMinutes: 240}); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `UPDATE projects SET infrastructure_failure_count = 2, infrastructure_next_retry_at = '2026-07-12 11:00:00',
		infrastructure_condition = 'The launch failed.' WHERE id = 'ws'`)
	unchanged := func() string {
		t.Helper()
		var out strings.Builder
		for _, table := range []string{"leases", "projects", "tasks", "infrastructure_failures", "accepted_commits"} {
			out.WriteString(table + "\n" + rowsText(t, db, `SELECT * FROM `+table+` ORDER BY 1`))
		}
		return out.String()
	}
	before := unchanged()

	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := fmt.Sprintf("wiki-%d", i)
			startWiki(t, db, "ws", run, wikiA)
			if !endWikiRun(t, db, run, c.end) {
				t.Fatal("the end of an active run wrote nothing")
			}

			wantReason := c.end.EndReason
			if wantReason == "" {
				wantReason = "<NULL>"
			}
			if got := runStr(t, db, run, "outcome"); got != c.end.Outcome {
				t.Fatalf("run outcome = %q, want %q", got, c.end.Outcome)
			}
			if got := runStr(t, db, run, "end_reason"); got != wantReason {
				t.Fatalf("run end reason = %q, want %q", got, wantReason)
			}
			if got := runStr(t, db, run, "ended_at"); got == "<NULL>" {
				t.Fatal("the run has no end")
			}
			if got := wikiStr(t, db, run, "result"); got != c.want.result {
				t.Fatalf("result = %q, want %q", got, c.want.result)
			}
			if got := wikiStr(t, db, run, "cause"); got != c.want.cause {
				t.Fatalf("cause = %q, want %q", got, c.want.cause)
			}
			if got := wikiStr(t, db, run, "wiki_commit"); got != c.want.commit {
				t.Fatalf("wiki commit = %q, want %q", got, c.want.commit)
			}
			if got := oneInt(t, db, `SELECT counted FROM wiki_updates WHERE run_id = ?`, run); got != c.want.counted {
				t.Fatalf("counted = %d, want %d", got, c.want.counted)
			}
			if got := wikiStr(t, db, run, "ended_at") != "<NULL>"; got != (c.want.result != "<NULL>") {
				t.Fatalf("wiki update has an end = %v, want %v", got, c.want.result != "<NULL>")
			}
			if got := wikiStr(t, db, run, "stopped_at") != "<NULL>"; got != c.want.stopped {
				t.Fatalf("stop confirmation recorded = %v, want %v", got, c.want.stopped)
			}
			wantRecords := ""
			if c.want.record != "" {
				wantRecords = "mc|ws|" + c.want.record + "\n"
			}
			if got := endRecords(t, db, run); got != wantRecords {
				t.Fatalf("end records = %q, want %q", got, wantRecords)
			}
			if n := brokenWikiUpdates(t, db); n != 0 {
				t.Fatalf("%d wiki updates have an ended run, no end, and no wiki commit", n)
			}
			if got := unchanged(); got != before {
				t.Fatalf("the end changed a lease, a Project, a task, or a failure record:\nbefore:\n%s\nafter:\n%s", before, got)
			}

			// The Project needs no active wiki update for the next case. The end keeps the stored wiki commit.
			if c.want.result == "<NULL>" {
				endWikiUpdate(t, db, run, domain.WikiEnd{Result: "succeeded", WikiCommit: wikiD, DestinationCommit: wikiA, MergedCommit: wikiC})
				if got := endRecords(t, db, run); got != "mc|ws|The wiki update succeeded. Culler merged the wiki commit into culler.\n" {
					t.Fatalf("end record of the merged wiki commit = %q", got)
				}
				if got := wikiStr(t, db, run, "wiki_commit || '|' || destination_commit || '|' || merged_commit"); got != wikiB+"|"+wikiA+"|"+wikiC {
					t.Fatalf("commits of the merged wiki update = %q", got)
				}
			}
		})
	}
}

func TestEndWikiRunWritesNothingAfterTheRunEnd(t *testing.T) {
	db := openSpine(t)
	mkProject(t, db, "other")
	startWiki(t, db, "ws", "ended", wikiA)
	endWikiRun(t, db, "ended", domain.WikiRunEnd{Outcome: "reaped", EndReason: "lease-timeout",
		Result: "failed", Cause: "The heartbeat of the Scribe run stopped."})
	startWiki(t, db, "other", "waits", wikiA)
	endWikiRun(t, db, "waits", domain.WikiRunEnd{Outcome: "completed", WikiCommit: wikiB})
	before := spineText(t, db)

	for _, c := range []struct {
		run string
		end domain.WikiRunEnd
	}{
		{"ended", domain.WikiRunEnd{Outcome: "completed", WikiCommit: wikiA, Result: "succeeded"}},
		{"ended", domain.WikiRunEnd{Outcome: "infra-failed", EndReason: "harness", Result: "failed", Cause: "A late report.", Stopped: true}},
		{"waits", domain.WikiRunEnd{Outcome: "infra-failed", EndReason: "harness", Result: "failed", Cause: "A late report."}},
		{"waits", domain.WikiRunEnd{Outcome: "completed", WikiCommit: wikiC}},
		{"absent", domain.WikiRunEnd{Outcome: "reaped", EndReason: "hard-deadline", Result: "failed", Cause: "The Scribe run reached its time limit."}},
	} {
		var ended bool
		err := commitTx(t, db, func(ctx context.Context, q domain.Q) error {
			var err error
			ended, err = domain.EndWikiRun(ctx, q, c.run, c.end)
			return err
		})
		if err != nil || ended {
			t.Fatalf("second end of %s = %v, %v; want false and no error", c.run, ended, err)
		}
		if got := spineText(t, db); got != before {
			t.Fatalf("the second end of %s changed a row:\nbefore:\n%s\nafter:\n%s", c.run, before, got)
		}
	}

	// A second end of the wiki update writes no row and no second end record.
	if err := commitTx(t, db, func(ctx context.Context, q domain.Q) error {
		return domain.EndWikiUpdate(ctx, q, "ended", domain.WikiEnd{Result: "succeeded", WikiCommit: wikiA})
	}); err != nil {
		t.Fatal(err)
	}
	if got := spineText(t, db); got != before {
		t.Fatal("the second end of the wiki update changed a row")
	}

	// A late failure report ends nothing. A failed run gets the evidence of each new report once.
	diagnostics := func(run string) int64 {
		t.Helper()
		return oneInt(t, db, `SELECT COUNT(*) FROM diagnostic_events WHERE run_id = ?`, run)
	}
	late := domain.InfrastructureFailureReport{Condition: "The agent program exited with code 1.", Source: "runner"}
	for _, c := range []struct {
		run      string
		evidence int64
	}{{"ended", 1}, {"ended", 1}, {"waits", 0}} {
		var result domain.InfrastructureFailureResult
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			var err error
			result, err = domain.RecordInfrastructureFailure(ctx, q, c.run, late)
			return err
		})
		if want := (domain.InfrastructureFailureResult{ProjectID: oneStr(t, db, `SELECT project FROM runs WHERE id = ?`, c.run), WikiUpdate: true}); result != want {
			t.Fatalf("result of the late report for %s = %+v, want %+v", c.run, result, want)
		}
		if got := spineText(t, db); got != before {
			t.Fatalf("the late report for %s changed a row:\nbefore:\n%s\nafter:\n%s", c.run, before, got)
		}
		if got := diagnostics(c.run); got != c.evidence {
			t.Fatalf("diagnostics of %s after the late report = %d, want %d", c.run, got, c.evidence)
		}
	}
}

// A failure report ends the run of a wiki update through the wiki update. The
// cause names the class of the failure, and the diagnostic holds the condition.
func TestFailureReportEndsTheRunOfAWikiUpdate(t *testing.T) {
	const (
		harness    = "The agent program of the Scribe run stopped."
		network    = "A network error stopped the Scribe run."
		provider   = "A provider error stopped the Scribe run."
		launch     = "The Scribe run did not start."
		connection = "The connection of the Scribe run failed."
		refused    = "The connection changed or became unavailable before launch."
	)
	status := 503
	resident := func(cause string, connectionRefused bool) domain.InfrastructureFailureReport {
		return domain.InfrastructureFailureReport{Condition: refused, Source: "resident", Cause: cause,
			Evidence: "The launch check refused the run.", ConnectionRefused: connectionRefused}
	}
	withNetworkError := resident("connection", true)
	withNetworkError.NetworkError = "connect"
	cases := []struct {
		name    string
		report  domain.InfrastructureFailureReport
		started bool
		outcome string
		reason  string
		cause   string
		counted int64
	}{
		{"failure report of the Scribe", domain.InfrastructureFailureReport{Condition: "The OpenWiki command failed.", Source: "agent"},
			true, "completed", "<NULL>", "The Scribe reported a failure: The OpenWiki command failed.", 1},
		{"runner report", domain.InfrastructureFailureReport{Condition: "The agent program exited with code 1.", Source: "runner"},
			true, "infra-failed", "harness", harness, 1},
		{"network error", domain.InfrastructureFailureReport{Condition: "The response stream disconnected.", Source: "runner", NetworkError: "reset"},
			true, "infra-failed", "network", network, 1},
		{"HTTP status", domain.InfrastructureFailureReport{Condition: "The provider returned an error.", Source: "runner", HTTPStatus: &status},
			true, "infra-failed", "provider", provider, 1},
		{"provider code", domain.InfrastructureFailureReport{Condition: "The provider returned an error.", Source: "resident", ProviderErrorCode: "overloaded"},
			false, "infra-failed", "provider", provider, 1},
		{"launch failure", domain.InfrastructureFailureReport{Condition: "The container did not start.", Source: "resident"},
			false, "infra-failed", "launch", launch, 1},
		{"credentials", resident("credentials", false), false, "infra-failed", "connection", connection, 1},
		{"connection that admits the run", resident("connection", false), false, "infra-failed", "connection", connection, 1},
		{"connection refused before launch", resident("connection", true), false, "infra-failed", "connection", refused, 0},
		{"connection refused after the process start", resident("connection", true), true, "infra-failed", "connection", connection, 1},
		{"credentials with a refused connection", resident("credentials", true), false, "infra-failed", "connection", connection, 1},
		{"network error with a refused connection", withNetworkError, false, "infra-failed", "network", network, 1},
	}

	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	if err := claim(t, db, domain.ClaimArgs{Project: "ws", RunID: "worker-1", Owner: "worker", SubjectID: &task,
		SessionPath: "sessions/worker-1", Binding: "fake", HardDeadlineMinutes: 240}); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `UPDATE projects SET infrastructure_failure_count = 2, infrastructure_next_retry_at = '2026-07-12 11:00:00',
		infrastructure_condition = 'The launch failed.' WHERE id = 'ws'`)
	unchanged := func() string {
		t.Helper()
		var out strings.Builder
		for _, table := range []string{"leases", "projects", "tasks", "infrastructure_failures"} {
			out.WriteString(table + "\n" + rowsText(t, db, `SELECT * FROM `+table+` ORDER BY 1`))
		}
		return out.String()
	}
	before := unchanged()

	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := fmt.Sprintf("wiki-%d", i)
			startWiki(t, db, "ws", run, wikiA)
			if c.started {
				mustExec(t, db, `UPDATE runs SET agent_started_at = datetime('now') WHERE id = ?`, run)
			}
			var result domain.InfrastructureFailureResult
			mustTx(t, db, func(ctx context.Context, q domain.Q) error {
				var err error
				result, err = domain.RecordInfrastructureFailure(ctx, q, run, c.report)
				return err
			})
			if want := (domain.InfrastructureFailureResult{ProjectID: "ws", WikiUpdate: true}); result != want {
				t.Fatalf("result = %+v, want %+v", result, want)
			}
			if got := runStr(t, db, run, "outcome || '|' || COALESCE(end_reason, '<NULL>')"); got != c.outcome+"|"+c.reason {
				t.Fatalf("run end = %q, want %q", got, c.outcome+"|"+c.reason)
			}
			if got := wikiStr(t, db, run, "result || '|' || cause || '|' || counted || '|' || COALESCE(stopped_at, '<NULL>')"); got != fmt.Sprintf("failed|%s|%d|<NULL>", c.cause, c.counted) {
				t.Fatalf("wiki update = %q, want the result failed, the cause %q, counted %d, and no stop confirmation", got, c.cause, c.counted)
			}
			if got := endRecords(t, db, run); got != "mc|ws|The wiki update failed. "+c.cause+"\n" {
				t.Fatalf("end records = %q", got)
			}
			if got := rowsText(t, db, `SELECT source, condition, count_before, count_after FROM diagnostic_events WHERE run_id = ?`, run); got != fmt.Sprintf("%s %s 0 0\n", c.report.Source, c.report.Condition) {
				t.Fatalf("diagnostics of the run = %q, want one record with the reported condition", got)
			}
			if got := unchanged(); got != before {
				t.Fatalf("the report changed a lease, a Project, a task, or a failure record:\nbefore:\n%s\nafter:\n%s", before, got)
			}
		})
	}

	// A liveness stop of a wiki update run has its own end. The reap of a lease run refuses the run.
	startWiki(t, db, "ws", "wiki-reap", wikiA)
	rows := spineText(t, db)
	for _, reap := range []domain.ReapArgs{
		{RunID: "wiki-reap", Reason: "lease-timeout"},
		{RunID: "wiki-reap", Reason: "interrupted", Outcome: "interrupted", NoCharge: true},
	} {
		if err := tx(t, db, func(ctx context.Context, q domain.Q) error {
			_, err := domain.ApplyReap(ctx, q, reap)
			return err
		}); err == nil || !strings.Contains(err.Error(), "ends through EndWikiRun") {
			t.Fatalf("reap %+v of a wiki update run = %v, want the refusal", reap, err)
		}
	}
	if got := spineText(t, db); got != rows {
		t.Fatal("a refused reap changed a row")
	}
}

func TestEndWikiRunRefusesARunWithNoWikiUpdate(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	if err := claim(t, db, domain.ClaimArgs{Project: "ws", RunID: "worker-1", Owner: "worker", SubjectID: &task,
		SessionPath: "sessions/worker-1", Binding: "fake", HardDeadlineMinutes: 240}); err != nil {
		t.Fatal(err)
	}
	before := spineText(t, db)
	for _, end := range []domain.WikiRunEnd{
		{Outcome: "reaped", EndReason: "lease-timeout", Result: "failed", Cause: "The heartbeat of the Scribe run stopped."},
		{Outcome: "completed", WikiCommit: wikiA, Result: "succeeded"},
		{Outcome: "completed", WikiCommit: wikiB},
	} {
		wantReject(t, db, func(ctx context.Context, q domain.Q) error {
			_, err := domain.EndWikiRun(ctx, q, "worker-1", end)
			return err
		})
	}
	if got := spineText(t, db); got != before {
		t.Fatal("a refused end changed the run of a task or its lease")
	}

	// The end of a run with no result requires a wiki commit.
	startWiki(t, db, "ws", "wiki-1", wikiA)
	before = spineText(t, db)
	if err := tx(t, db, func(ctx context.Context, q domain.Q) error {
		_, err := domain.EndWikiRun(ctx, q, "wiki-1", domain.WikiRunEnd{Outcome: "completed"})
		return err
	}); err == nil || !strings.Contains(err.Error(), "requires a wiki commit") {
		t.Fatalf("end with no result and no wiki commit = %v, want the error that names the wiki commit", err)
	}
	// A wiki commit that equals the start commit is a result, not a commit that waits for its merge.
	wantReject(t, db, func(ctx context.Context, q domain.Q) error {
		_, err := domain.EndWikiRun(ctx, q, "wiki-1", domain.WikiRunEnd{Outcome: "completed", WikiCommit: wikiA})
		return err
	})
	if got := spineText(t, db); got != before {
		t.Fatal("a refused end changed the wiki update run")
	}
}

func TestEndWikiRunClearsTheNetworkRetryOfACompletedRun(t *testing.T) {
	for _, c := range []struct {
		name string
		end  domain.WikiRunEnd
		want string
	}{
		{"completed", domain.WikiRunEnd{Outcome: "completed", WikiCommit: wikiA, Result: "succeeded"}, "ready|0"},
		{"reaped", domain.WikiRunEnd{Outcome: "reaped", EndReason: "lease-timeout", Result: "failed",
			Cause: "The heartbeat of the Scribe run stopped."}, "retry_wait|1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			db := openSpine(t)
			// A network failure holds each new run of the profile. The wiki update run is the retry.
			mustExec(t, db, `INSERT INTO connection_health(connection_id,operation,profile_id,authorization_id,state)
				VALUES('connection','connection_access','','authorization','ready')`)
			mustTx(t, db, func(ctx context.Context, q domain.Q) error {
				diagnostic, err := domain.AppendDiagnostic(ctx, q, domain.Diagnostic{Source: "runner", Operation: "model_access",
					Outcome: "failed", ConnectionID: "connection", AuthorizationID: "authorization", ProfileID: "profile",
					ProfileRevision: "revision", NetworkError: "reset", Condition: "The response stream disconnected."}, "")
				if err != nil {
					return err
				}
				return domain.RecordNetworkFailure(ctx, q, "connection", "authorization", "profile", "revision", 0, diagnostic)
			})
			startWiki(t, db, "ws", "wiki-1", wikiA)
			mustExec(t, db, `UPDATE runs SET connection_id = 'connection', authorization_id = 'authorization', profile_id = 'profile',
				profile_revision = 'revision', connection_revision = 1 WHERE id = 'wiki-1'`)
			health := func() string {
				t.Helper()
				return oneStr(t, db, `SELECT state || '|' || failure_count FROM connection_health WHERE profile_id = 'profile'`)
			}
			if got := health(); got != "retry_wait|1" {
				t.Fatalf("connection health before the end = %q, want retry_wait|1", got)
			}
			if !endWikiRun(t, db, "wiki-1", c.end) {
				t.Fatal("the end of an active run wrote nothing")
			}
			if got := health(); got != c.want {
				t.Fatalf("connection health after the end = %q, want %q", got, c.want)
			}
		})
	}
}

func TestEndWikiUpdateCutsALongCause(t *testing.T) {
	// The character at the limit has two bytes. A cut at a byte count splits it.
	limit := strings.Repeat("x", 2047) + "é"
	long := limit + strings.Repeat("y", 2952)
	if n := utf8.RuneCountInString(long); n != 5000 {
		t.Fatalf("fixture cause = %d characters, want 5000", n)
	}
	for _, c := range []struct {
		name, cause, want string
	}{
		{"long", long, limit},
		{"at the limit", limit, limit},
		{"spaces around the text", " \n\tThe check failed.\n ", "The check failed."},
		{"empty", "", "The wiki update failed."},
		{"spaces only", " \n\t ", "The wiki update failed."},
		{"spaces to the limit", strings.Repeat(" ", 2048) + "late text", "The wiki update failed."},
	} {
		t.Run(c.name, func(t *testing.T) {
			db := openSpine(t)
			startWiki(t, db, "ws", "wiki-1", wikiA)
			endRunOnly(t, db, "wiki-1")
			endWikiUpdate(t, db, "wiki-1", domain.WikiEnd{Result: "failed", Cause: c.cause})
			got := wikiStr(t, db, "wiki-1", "cause")
			if got != c.want {
				t.Fatalf("stored cause has %d characters and differs from the expected text of %d characters",
					utf8.RuneCountInString(got), utf8.RuneCountInString(c.want))
			}
			if !utf8.ValidString(got) || utf8.RuneCountInString(got) > 2048 {
				t.Fatalf("stored cause: valid text %v, %d characters; want a valid text of at most 2048 characters",
					utf8.ValidString(got), utf8.RuneCountInString(got))
			}
			if got := wikiStr(t, db, "wiki-1", "result"); got != "failed" {
				t.Fatalf("result = %q, want failed", got)
			}
			if got := endRecords(t, db, "wiki-1"); got != "mc|ws|The wiki update failed. "+c.want+"\n" {
				t.Fatal("the end record differs from \"The wiki update failed. \" and the stored cause")
			}
		})
	}

	t.Run("a successful end stores no cause", func(t *testing.T) {
		db := openSpine(t)
		startWiki(t, db, "ws", "wiki-1", wikiA)
		endRunOnly(t, db, "wiki-1")
		endWikiUpdate(t, db, "wiki-1", domain.WikiEnd{Result: "succeeded", Cause: "A text.", WikiCommit: wikiA, NotCounted: true})
		if got := wikiStr(t, db, "wiki-1", "COALESCE(cause, 'none') || '|' || counted"); got != "none|1" {
			t.Fatalf("cause and counted of a successful end = %q, want none|1", got)
		}
	})

	t.Run("an end with an unknown result is refused", func(t *testing.T) {
		db := openSpine(t)
		startWiki(t, db, "ws", "wiki-1", wikiA)
		endRunOnly(t, db, "wiki-1")
		wantReject(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.EndWikiUpdate(ctx, q, "wiki-1", domain.WikiEnd{Result: "stopped", Cause: "A text."})
		})
		if got := wikiStr(t, db, "wiki-1", "ended_at"); got != "<NULL>" {
			t.Fatalf("the refused end stored the end %q", got)
		}
	})
}

func TestRecoverWikiUpdateEndsARunWithNoResult(t *testing.T) {
	db := openSpine(t)
	for _, project := range []string{"active", "waits", "other"} {
		mkProject(t, db, project)
	}
	recoverWiki := func(project string) bool {
		t.Helper()
		var recovered bool
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			var err error
			recovered, err = domain.RecoverWikiUpdate(ctx, q, project)
			return err
		})
		return recovered
	}

	// The rule ends no wiki update with an active run and none that waits for its merge.
	startWiki(t, db, "active", "wiki-active", wikiA)
	waitingWiki(t, db, "waits", "wiki-waits")
	startWiki(t, db, "other", "wiki-other", wikiA)
	endRunOnly(t, db, "wiki-other")
	startWiki(t, db, "ws", "wiki-1", wikiA)
	endRunOnly(t, db, "wiki-1")
	if n := brokenWikiUpdates(t, db); n != 2 {
		t.Fatalf("wiki updates with an ended run, no end, and no wiki commit = %d, want 2", n)
	}
	before := spineText(t, db)
	for _, project := range []string{"active", "waits", "absent"} {
		if recoverWiki(project) {
			t.Fatalf("the rule ended a wiki update of the Project %s", project)
		}
	}
	if got := spineText(t, db); got != before {
		t.Fatal("the rule changed a wiki update with an active run or with a wiki commit")
	}

	if !recoverWiki("ws") {
		t.Fatal("the rule ended no wiki update")
	}
	if got := wikiStr(t, db, "wiki-1", "result || '|' || cause || '|' || counted || '|' || COALESCE(wiki_commit, 'none') || '|' || COALESCE(stopped_at, 'none')"); got != "failed|The Scribe run ended with no result.|1|none|none" {
		t.Fatalf("recovered wiki update = %q", got)
	}
	if got := endRecords(t, db, "wiki-1"); got != "mc|ws|The wiki update failed. The Scribe run ended with no result.\n" {
		t.Fatalf("end records = %q", got)
	}
	if got := wikiStr(t, db, "wiki-other", "ended_at"); got != "<NULL>" {
		t.Fatal("the rule ended the wiki update of another Project")
	}
	if n := brokenWikiUpdates(t, db); n != 1 {
		t.Fatalf("wiki updates with an ended run, no end, and no wiki commit = %d, want 1", n)
	}

	after := spineText(t, db)
	if recoverWiki("ws") {
		t.Fatal("a second call ended a wiki update")
	}
	if got := spineText(t, db); got != after {
		t.Fatal("a second call changed a row")
	}
}

// A wiki update with a wiki commit and no record of the repository of its
// run can get no capture. The recovery rule ends it as failed.
func TestRecoverEndsAWikiUpdateWithNoRepositoryRecord(t *testing.T) {
	db := openSpine(t)
	for _, project := range []string{"waits", "active"} {
		mkProject(t, db, project)
	}
	recoverWiki := func(project string) bool {
		t.Helper()
		var recovered bool
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			var err error
			recovered, err = domain.RecoverWikiUpdate(ctx, q, project)
			return err
		})
		return recovered
	}
	const cause = "Culler has no record of the repository of the Scribe run."

	waitingWiki(t, db, "waits", "wiki-waits")
	startWiki(t, db, "active", "wiki-active", wikiA)
	startWiki(t, db, "ws", "wiki-1", wikiA)
	endWikiRun(t, db, "wiki-1", domain.WikiRunEnd{Outcome: "completed", WikiCommit: wikiB})
	before := spineText(t, db)
	for _, project := range []string{"waits", "active"} {
		if recoverWiki(project) {
			t.Fatalf("the rule ended the wiki update of the Project %s", project)
		}
	}
	if got := spineText(t, db); got != before {
		t.Fatal("the rule changed a wiki update with a repository record or with an active run")
	}

	if !recoverWiki("ws") {
		t.Fatal("the rule ended no wiki update")
	}
	if got := wikiStr(t, db, "wiki-1", "result || '|' || cause || '|' || counted || '|' || wiki_commit || '|' || COALESCE(merged_commit, 'none')"); got != "failed|"+cause+"|1|"+wikiB+"|none" {
		t.Fatalf("recovered wiki update = %q", got)
	}
	if got := endRecords(t, db, "wiki-1"); got != "mc|ws|The wiki update failed. "+cause+"\n" {
		t.Fatalf("end records = %q", got)
	}
	if got := wikiStr(t, db, "wiki-waits", "ended_at"); got != "<NULL>" {
		t.Fatal("the rule ended a wiki update that waits for its capture")
	}

	after := spineText(t, db)
	if recoverWiki("ws") {
		t.Fatal("a second call ended a wiki update")
	}
	if got := spineText(t, db); got != after {
		t.Fatal("a second call changed a row")
	}
}

func TestEndArchivedWikiUpdates(t *testing.T) {
	db := openSpine(t)
	for _, project := range []string{"archived", "archived-active", "archived-waits"} {
		mkProject(t, db, project)
	}
	startWiki(t, db, "ws", "wiki-ws", wikiA)
	endRunOnly(t, db, "wiki-ws")
	startWiki(t, db, "archived", "wiki-archived", wikiA)
	endRunOnly(t, db, "wiki-archived")
	startWiki(t, db, "archived-active", "wiki-active", wikiA)
	startWiki(t, db, "archived-waits", "wiki-waits", wikiA)
	endWikiRun(t, db, "wiki-waits", domain.WikiRunEnd{Outcome: "completed", WikiCommit: wikiB})
	mustExec(t, db, `UPDATE projects SET status = 'archived' WHERE id LIKE 'archived%'`)

	endArchived := func() {
		t.Helper()
		mustTx(t, db, func(ctx context.Context, q domain.Q) error { return domain.EndArchivedWikiUpdates(ctx, q) })
	}
	endArchived()
	for run, want := range map[string]string{
		"wiki-archived": "failed|The Project is archived.|1|none",
		"wiki-waits":    "failed|The Project is archived.|1|" + wikiB,
		"wiki-active":   "none|none|1|none",
		"wiki-ws":       "none|none|1|none",
	} {
		if got := wikiStr(t, db, run, "COALESCE(result, 'none') || '|' || COALESCE(cause, 'none') || '|' || counted || '|' || COALESCE(wiki_commit, 'none')"); got != want {
			t.Fatalf("%s = %q, want %q", run, got, want)
		}
	}
	if got := endRecords(t, db, "wiki-archived"); got != "mc|archived|The wiki update failed. The Project is archived.\n" {
		t.Fatalf("end records = %q", got)
	}
	if got := runStr(t, db, "wiki-active", "ended_at"); got != "<NULL>" {
		t.Fatal("the function ended an active run")
	}

	after := spineText(t, db)
	endArchived()
	if got := spineText(t, db); got != after {
		t.Fatal("a second call changed a row")
	}
}

// The archived rule waits for the end of each execution of the wiki update
// and for the result of its merge.
func TestEndArchivedWikiUpdatesWaitsForExecutionAndMerge(t *testing.T) {
	db := openSpine(t)
	for _, project := range []string{"capture", "merge"} {
		mkProject(t, db, project)
	}
	owner := registerOwner(t, db)
	endArchived := func() {
		t.Helper()
		mustTx(t, db, func(ctx context.Context, q domain.Q) error { return domain.EndArchivedWikiUpdates(ctx, q) })
	}
	state := func(run string) string {
		t.Helper()
		return wikiStr(t, db, run, "COALESCE(result, 'active') || '|' || COALESCE(cause, 'none')")
	}

	// An active execution.
	waitingWiki(t, db, "capture", "wiki-capture")
	capture := wikiExecution("capture-1", "wiki-capture", "capture", "wiki-capture", owner, bootA)
	capture.Project = "capture"
	startExecution(t, db, capture)

	// An unfinished merge authorization whose execution ended with no result.
	waitingWiki(t, db, "merge", "wiki-merge")
	authorizeWikiMerge(t, db, "wiki-merge", "prepare-1", "apply-1", owner)
	endExecution(t, db, domain.ExecutionEnd{RunID: "apply-1", Reason: "heartbeat", Stopped: true, Now: spineNow})

	mustExec(t, db, `UPDATE projects SET status = 'archived' WHERE id IN ('capture', 'merge')`)
	before := spineText(t, db)
	endArchived()
	if got := spineText(t, db); got != before {
		t.Fatal("the archived rule changed a wiki update with an active execution or an unfinished merge authorization")
	}

	endExecution(t, db, domain.ExecutionEnd{RunID: "capture-1", Reason: "vm-restart", Now: spineNow})
	endArchived()
	if got := state("wiki-capture"); got != "failed|The Project is archived." {
		t.Fatalf("wiki update after the end of its execution = %q", got)
	}
	if got := state("wiki-merge"); got != "active|none" {
		t.Fatalf("wiki update with an unfinished merge authorization = %q, want an active wiki update", got)
	}

	// Receipt recovery proves that the merge authorization was not used.
	recovery := wikiExecution("reconcile-1", "wiki-merge", "reconcile", "apply-1", owner, bootA)
	recovery.Project = "merge"
	startExecution(t, db, recovery)
	mustExec(t, db, `UPDATE merge_operations SET result='{"status":"not-applied"}',finished=1 WHERE run_id='apply-1'`)
	endArchived()
	if got := state("wiki-merge"); got != "active|none" {
		t.Fatalf("wiki update with an active receipt recovery = %q, want an active wiki update", got)
	}
	endExecution(t, db, domain.ExecutionEnd{RunID: "reconcile-1", Reason: "result", Stopped: true, Now: spineNow})
	endArchived()
	if got := state("wiki-merge"); got != "failed|The Project is archived." {
		t.Fatalf("wiki update after the result of its merge = %q", got)
	}
}

func TestFenceAgentRunAcceptsLeaseOrWikiUpdate(t *testing.T) {
	db := openSpine(t)
	mkProject(t, db, "other")
	fence := func(run string) (domain.AgentRunFence, error) {
		t.Helper()
		var accepted domain.AgentRunFence
		err := tx(t, db, func(ctx context.Context, q domain.Q) error {
			var err error
			accepted, err = domain.FenceAgentRun(ctx, q, run)
			return err
		})
		return accepted, err
	}
	stale := func(run string) {
		t.Helper()
		accepted, err := fence(run)
		var refusal *domain.DomainError
		if !errors.As(err, &refusal) || refusal.Code != domain.CodeStaleRun {
			t.Fatalf("fence of %s = %+v, %v; want the stale-run refusal", run, accepted, err)
		}
	}

	// A wiki update run and a lease run of one Project are active at the same time.
	task := mkTask(t, db, "task", "seeded")
	if err := claim(t, db, domain.ClaimArgs{Project: "ws", RunID: "worker-1", Owner: "worker", SubjectID: &task,
		SessionPath: "sessions/worker-1", Binding: "fake", HardDeadlineMinutes: 240}); err != nil {
		t.Fatal(err)
	}
	startWiki(t, db, "ws", "wiki-1", wikiA)
	if err := claim(t, db, domain.ClaimArgs{Project: "other", RunID: "strategist-1", Owner: "strategist",
		SessionPath: "sessions/strategist-1", Binding: "fake", HardDeadlineMinutes: 240}); err != nil {
		t.Fatal(err)
	}

	accepted, err := fence("worker-1")
	if err != nil || accepted.Project != "ws" || accepted.TaskID == nil || *accepted.TaskID != task ||
		accepted.WikiUpdate || accepted.StartCommit != "" {
		t.Fatalf("fence of the lease run = %+v, %v", accepted, err)
	}
	accepted, err = fence("strategist-1")
	if err != nil || accepted.Project != "other" || accepted.TaskID != nil || accepted.WikiUpdate {
		t.Fatalf("fence of the lease run with no task = %+v, %v", accepted, err)
	}
	accepted, err = fence("wiki-1")
	if err != nil || accepted.Project != "ws" || accepted.TaskID != nil || !accepted.WikiUpdate || accepted.StartCommit != wikiA {
		t.Fatalf("fence of the wiki update run = %+v, %v", accepted, err)
	}

	// A run with no lease and no wiki update, and an unknown run.
	mkRun(t, db, "worker-free", "worker", task)
	stale("worker-free")
	stale("absent")

	// The run of a wiki update that waits for its merge has an end.
	startWiki(t, db, "other", "wiki-waits", wikiA)
	endWikiRun(t, db, "wiki-waits", domain.WikiRunEnd{Outcome: "completed", WikiCommit: wikiB})
	stale("wiki-waits")

	endWikiRun(t, db, "wiki-1", domain.WikiRunEnd{Outcome: "reaped", EndReason: "hard-deadline",
		Result: "failed", Cause: "The Scribe run reached its time limit."})
	stale("wiki-1")
	if accepted, err := fence("worker-1"); err != nil || accepted.WikiUpdate {
		t.Fatalf("fence of the lease run after the end of the wiki update = %+v, %v", accepted, err)
	}
}

func TestHeartbeatAgentRunStampsItsOwnRecord(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	if err := claim(t, db, domain.ClaimArgs{Project: "ws", RunID: "worker-1", Owner: "worker", SubjectID: &task,
		SessionPath: "sessions/worker-1", Binding: "fake", HardDeadlineMinutes: 240}); err != nil {
		t.Fatal(err)
	}
	startWiki(t, db, "ws", "wiki-1", wikiA)
	beat := func(run string) (string, error) {
		t.Helper()
		var stamp string
		err := tx(t, db, func(ctx context.Context, q domain.Q) error {
			var err error
			stamp, err = domain.HeartbeatAgentRun(ctx, q, run)
			return err
		})
		return stamp, err
	}

	lease := rowsText(t, db, `SELECT * FROM leases WHERE project = 'ws'`)
	deadline := wikiStr(t, db, "wiki-1", "deadline_uptime_ms")
	stamp, err := beat("wiki-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(domain.SpineTime, stamp); err != nil {
		t.Fatalf("stamp = %q, want the spine time format", stamp)
	}
	if got := wikiStr(t, db, "wiki-1", "last_heartbeat_at"); got != stamp {
		t.Fatalf("heartbeat time of the wiki update = %q, want the stamp %q", got, stamp)
	}
	vm, err := domain.ReadVMClock()
	if err != nil {
		t.Fatal(err)
	}
	if got := oneStr(t, db, `SELECT heartbeat_uptime_ms >= acquired_uptime_ms AND heartbeat_uptime_ms <= ?
		FROM wiki_updates WHERE run_id = 'wiki-1'`, vm.Uptime.Milliseconds()); got != "1" {
		t.Fatal("the heartbeat uptime is outside the time from the start to the current uptime")
	}
	if got := wikiStr(t, db, "wiki-1", "deadline_uptime_ms"); got != deadline {
		t.Fatalf("deadline = %s, want %s: a heartbeat moves no deadline", got, deadline)
	}
	if got := rowsText(t, db, `SELECT * FROM leases WHERE project = 'ws'`); got != lease {
		t.Fatal("the heartbeat of the wiki update run changed the lease of the Project")
	}

	wiki := rowsText(t, db, `SELECT * FROM wiki_updates`)
	stamp, err = beat("worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := lockStr(t, db, "last_heartbeat_at"); got != stamp {
		t.Fatalf("heartbeat time of the lease = %q, want the stamp %q", got, stamp)
	}
	if got := rowsText(t, db, `SELECT * FROM wiki_updates`); got != wiki {
		t.Fatal("the heartbeat of the lease run changed the wiki update")
	}

	endWikiRun(t, db, "wiki-1", domain.WikiRunEnd{Outcome: "completed", WikiCommit: wikiB})
	wiki = rowsText(t, db, `SELECT * FROM wiki_updates`)
	_, err = beat("wiki-1")
	var refusal *domain.DomainError
	if !errors.As(err, &refusal) || refusal.Code != domain.CodeStaleRun {
		t.Fatalf("heartbeat after the end of the run = %v, want the stale-run refusal", err)
	}
	if got := rowsText(t, db, `SELECT * FROM wiki_updates`); got != wiki {
		t.Fatal("the refused heartbeat changed the wiki update")
	}
}

func TestWikiStateOfLoadsTheRowsOfTheDueDecision(t *testing.T) {
	db := openSpine(t)
	mkProject(t, db, "other")
	record := func(commit string) {
		t.Helper()
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.RecordAcceptedCommit(ctx, q, "ws", commit)
		})
	}
	run := func(row *domain.WikiUpdateRow) string {
		if row == nil {
			return "none"
		}
		return row.RunID
	}
	check := func(step, kind, accepted, active, last, succeeded, counted string) {
		t.Helper()
		state := wikiState(t, db, "ws")
		got := strings.Join([]string{state.Kind, state.AcceptedCommit, run(state.Active), run(state.Last),
			run(state.LastSucceeded), run(state.LastCounted)}, "|")
		want := strings.Join([]string{kind, accepted, active, last, succeeded, counted}, "|")
		if got != want {
			t.Fatalf("%s: wiki state = %s, want %s", step, got, want)
		}
	}

	check("no row", "behind", "", "none", "none", "none", "none")
	record(wikiA)
	check("an accepted commit", "behind", wikiA, "none", "none", "none", "none")

	startWiki(t, db, "ws", "wiki-1", wikiA)
	check("an active wiki update", "update-active", wikiA, "wiki-1", "none", "none", "wiki-1")
	endWikiRun(t, db, "wiki-1", domain.WikiRunEnd{Outcome: "reaped", EndReason: "lease-timeout",
		Result: "failed", Cause: "The heartbeat of the Scribe run stopped."})
	check("a failure", "behind", wikiA, "none", "wiki-1", "none", "wiki-1")

	startWiki(t, db, "ws", "wiki-2", wikiA)
	endWikiRun(t, db, "wiki-2", domain.WikiRunEnd{Outcome: "infra-failed", EndReason: "connection", Result: "failed",
		Cause: "The connection changed or became unavailable before launch.", NotCounted: true})
	check("a failure outside the interval rule", "behind", wikiA, "none", "wiki-2", "none", "wiki-1")

	startWiki(t, db, "ws", "wiki-3", wikiA)
	endWikiRun(t, db, "wiki-3", domain.WikiRunEnd{Outcome: "completed", WikiCommit: wikiA, Result: "succeeded"})
	check("a success with no wiki change", "current", wikiA, "none", "wiki-3", "wiki-3", "wiki-3")

	record(wikiB)
	check("a new accepted commit", "behind", wikiB, "none", "wiki-3", "wiki-3", "wiki-3")

	startWiki(t, db, "ws", "wiki-4", wikiB)
	endWikiRun(t, db, "wiki-4", domain.WikiRunEnd{Outcome: "completed", WikiCommit: wikiC})
	check("a wiki commit that waits for its merge", "update-active", wikiB, "wiki-4", "wiki-3", "wiki-3", "wiki-4")
	endWikiUpdate(t, db, "wiki-4", domain.WikiEnd{Result: "succeeded", DestinationCommit: wikiB, MergedCommit: wikiD})
	check("a merged wiki commit", "behind", wikiB, "none", "wiki-4", "wiki-4", "wiki-4")
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		return domain.RecordMergedCommit(ctx, q, "ws", wikiB, wikiD)
	})
	check("the commit of the merge is the accepted commit", "current", wikiD, "none", "wiki-4", "wiki-4", "wiki-4")

	startWiki(t, db, "ws", "wiki-5", wikiD)
	endWikiRun(t, db, "wiki-5", domain.WikiRunEnd{Outcome: "reaped", EndReason: "hard-deadline",
		Result: "failed", Cause: "The Scribe run reached its time limit."})
	check("a failure after a success", "current", wikiD, "none", "wiki-5", "wiki-4", "wiki-5")

	// The state of a Project holds no row of another Project.
	if state := wikiState(t, db, "other"); state.Kind != "behind" || state.AcceptedCommit != "" || state.Active != nil ||
		state.Last != nil || state.LastSucceeded != nil || state.LastCounted != nil || state.Unstopped != "" {
		t.Fatalf("wiki state of a Project with no wiki update = %+v", state)
	}

	// The row holds each fact of the wiki update and three facts of its run.
	mustExec(t, db, `UPDATE runs SET agent_started_at = '2026-07-12 10:00:05' WHERE id = 'wiki-4'`)
	row := wikiState(t, db, "ws").LastSucceeded
	if row.Project != "ws" || row.RunID != "wiki-4" || row.StartCommit != wikiB || row.WikiCommit != wikiC ||
		row.StartedAt != testNowText || row.EndedAt == "" || row.Result != "succeeded" || row.Cause != "" || !row.Counted ||
		row.MergeMarker != 0 || row.BootID == "" || row.DeadlineUptimeMS-row.AcquiredUptimeMS != 14400000 ||
		row.LastHeartbeatAt != "" || row.HeartbeatUptimeMS != nil || row.StoppedAt != "" || row.MergeAfter != "" ||
		row.DestinationCommit != wikiB || row.MergedCommit != wikiD ||
		row.RunEndedAt == "" || row.RunOutcome != "completed" || row.AgentStartedAt != "2026-07-12 10:00:05" {
		t.Fatalf("row of the merged wiki update = %+v", row)
	}
	if failed := wikiState(t, db, "ws").Last; failed.Result != "failed" || failed.Cause != "The Scribe run reached its time limit." ||
		failed.WikiCommit != "" || failed.RunOutcome != "reaped" || failed.ID <= row.ID {
		t.Fatalf("row of the failed wiki update = %+v", failed)
	}
}

func TestWikiUpdateRowRecord(t *testing.T) {
	db := openSpine(t)
	if record := (*domain.WikiUpdateRow)(nil).Record(); record != nil {
		t.Fatalf("record of no row = %+v, want nil", record)
	}
	startWiki(t, db, "ws", "wiki-1", wikiA)
	active := wikiState(t, db, "ws").Active.Record()
	if active.RunID != "wiki-1" || active.StartCommit != wikiA || active.WikiCommit != "" || !active.StartedAt.Equal(testNow) ||
		active.Result != "" || !active.Counted || active.RunEnded || active.HeartbeatUptime != nil ||
		active.BootID == "" || active.DeadlineUptime-active.AcquiredUptime != 240*time.Minute || active.AcquiredUptime <= 0 {
		t.Fatalf("record of an active wiki update = %+v", active)
	}

	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		_, err := domain.HeartbeatAgentRun(ctx, q, "wiki-1")
		return err
	})
	beat := wikiState(t, db, "ws").Active.Record()
	if beat.HeartbeatUptime == nil || *beat.HeartbeatUptime < beat.AcquiredUptime || *beat.HeartbeatUptime >= beat.DeadlineUptime {
		t.Fatalf("record after a heartbeat = %+v", beat)
	}

	endWikiRun(t, db, "wiki-1", domain.WikiRunEnd{Outcome: "completed", WikiCommit: wikiB})
	waits := wikiState(t, db, "ws").Active.Record()
	if !waits.RunEnded || waits.WikiCommit != wikiB || waits.Result != "" {
		t.Fatalf("record of a wiki update that waits for its merge = %+v", waits)
	}
	endWikiUpdate(t, db, "wiki-1", domain.WikiEnd{Result: "succeeded", DestinationCommit: wikiA, MergedCommit: wikiC})
	merged := wikiState(t, db, "ws").LastSucceeded.Record()
	if merged.Result != "succeeded" || merged.DestinationCommit != wikiA || merged.MergedCommit != wikiC || !merged.RunEnded {
		t.Fatalf("record of a merged wiki update = %+v", merged)
	}

	startWiki(t, db, "ws", "wiki-2", wikiC)
	endWikiRun(t, db, "wiki-2", domain.WikiRunEnd{Outcome: "infra-failed", EndReason: "connection", Result: "failed",
		Cause: "The connection changed or became unavailable before launch.", NotCounted: true})
	if uncounted := wikiState(t, db, "ws").Last.Record(); uncounted.Counted || uncounted.Result != "failed" {
		t.Fatalf("record of a failure outside the interval rule = %+v", uncounted)
	}
}

func TestWikiStateOfReportsTheUnstoppedRun(t *testing.T) {
	db := openSpine(t)
	unstopped := func() string {
		t.Helper()
		return wikiState(t, db, "ws").Unstopped
	}
	confirm := func(run string) {
		t.Helper()
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.AcknowledgeRunStop(ctx, q, run)
		})
	}

	if got := unstopped(); got != "" {
		t.Fatalf("unstopped run with no wiki update = %q, want \"\"", got)
	}
	startWiki(t, db, "ws", "wiki-1", wikiA)
	if got := unstopped(); got != "" {
		t.Fatalf("unstopped run beside an active run = %q, want \"\"", got)
	}
	// The stop confirmation of a run with no end is stale and records nothing.
	err := commitTx(t, db, func(ctx context.Context, q domain.Q) error {
		return domain.AcknowledgeRunStop(ctx, q, "wiki-1")
	})
	var stale *domain.DomainError
	if !errors.As(err, &stale) || stale.Code != domain.CodeStaleRun {
		t.Fatalf("stop confirmation of an active run = %v, want the code %s", err, domain.CodeStaleRun)
	}
	if got := wikiStr(t, db, "wiki-1", "COALESCE(stopped_at, '')"); got != "" {
		t.Fatalf("stopped_at of an active run = %q, want no value", got)
	}
	endWikiRun(t, db, "wiki-1", domain.WikiRunEnd{Outcome: "reaped", EndReason: "lease-timeout",
		Result: "failed", Cause: "The heartbeat of the Scribe run stopped."})
	if got := unstopped(); got != "wiki-1" {
		t.Fatalf("unstopped run after the end of the run = %q, want wiki-1", got)
	}

	// The run of a wiki update that waits for its merge also needs its stop confirmation.
	startWiki(t, db, "ws", "wiki-2", wikiA)
	endWikiRun(t, db, "wiki-2", domain.WikiRunEnd{Outcome: "completed", WikiCommit: wikiB})
	if got := unstopped(); got != "wiki-1" {
		t.Fatalf("unstopped run with two open confirmations = %q, want the oldest run wiki-1", got)
	}
	confirm("wiki-1")
	if got := unstopped(); got != "wiki-2" {
		t.Fatalf("unstopped run after the first confirmation = %q, want wiki-2", got)
	}
	if got := wikiStr(t, db, "wiki-2", "COALESCE(stopped_at, '')"); got != "" {
		t.Fatalf("stopped_at of the second run after the first confirmation = %q, want no value", got)
	}
	// A recorded stop keeps its time: a second confirmation writes nothing.
	const earlier = "2026-01-01 00:00:00"
	mustExec(t, db, `UPDATE wiki_updates SET stopped_at = ? WHERE run_id = 'wiki-2'`, earlier)
	confirm("wiki-2")
	if got := wikiStr(t, db, "wiki-2", "stopped_at"); got != earlier {
		t.Fatalf("stopped_at after a second confirmation = %q, want %q", got, earlier)
	}
	if got := unstopped(); got != "" {
		t.Fatalf("unstopped run after each confirmation = %q, want \"\"", got)
	}

	// The end of a refused candidate records the stop itself.
	endWikiUpdate(t, db, "wiki-2", domain.WikiEnd{Result: "failed", Cause: "The merge failed."})
	startWiki(t, db, "ws", "wiki-3", wikiA)
	endWikiRun(t, db, "wiki-3", domain.WikiRunEnd{Outcome: "infra-failed", EndReason: "launch", Result: "failed",
		Cause: "Culler refused the start of the Scribe run. The Culler deployment is not ready (health.helper_unavailable).", Stopped: true})
	if got := unstopped(); got != "" {
		t.Fatalf("unstopped run after a refused candidate = %q, want \"\"", got)
	}
}
