package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"mc/dispatch"
	"mc/domain"
	"mc/wikitest"
)

const (
	wsRunText     = "The Scribe updates the Project wiki."
	wsMergeText   = "Culler merges the wiki commit."
	wsPausedText  = "The Project is paused. Culler merges the wiki commit after the Project is active."
	wsReceiptText = "Culler checks the result of an interrupted merge of the wiki commit. Each other merge step of the Project waits for this result."
	wsMergedText  = "Culler merged the wiki commit."
	wsBehindText  = "The Project wiki is behind the accepted commit."
	wsDueText     = "The next wiki update is due."
)

// The two failed ends of a Scribe run. The first counts for the interval of
// the Project, and the second does not.
var (
	wsTimeLimit = domain.WikiRunEnd{Outcome: "reaped", EndReason: "hard-deadline", Result: "failed",
		Cause: "The Scribe run reached its time limit."}
	wsRefused = domain.WikiRunEnd{Outcome: "infra-failed", EndReason: "connection", Result: "failed",
		Cause: "The connection changed or became unavailable before launch.", NotCounted: true}
)

// wsStates returns the rows of the wiki state read.
func wsStates(t *testing.T, db *sql.DB) []map[string]any {
	t.Helper()
	result, err := WikiStateList(db)
	if err != nil {
		t.Fatalf("wiki state read: %v", err)
	}
	return result.(map[string]any)["wiki_states"].([]map[string]any)
}

// wsState returns the row of the Project in the wiki state read.
func wsState(t *testing.T, db *sql.DB, project string) map[string]any {
	t.Helper()
	for _, row := range wsStates(t, db) {
		if row["project"] == project {
			return row
		}
	}
	t.Fatalf("the wiki state read holds no row of %s", project)
	return nil
}

// wsRunUpdate returns the wiki_update value of the run in the run list.
func wsRunUpdate(t *testing.T, db *sql.DB, run string) any {
	t.Helper()
	result, err := RunList(db, RunSelection{})
	if err != nil {
		t.Fatalf("run list: %v", err)
	}
	for _, row := range result.(map[string]any)["runs"].([]map[string]any) {
		if row["id"] == run {
			update, ok := row["wiki_update"]
			if !ok {
				t.Fatalf("the row of the run %s holds no wiki_update key: %v", run, row)
			}
			return update
		}
	}
	t.Fatalf("the run list holds no run %s", run)
	return nil
}

// wsAccept records the accepted commit of the Project.
func wsAccept(t *testing.T, db *sql.DB, project, commit string) {
	t.Helper()
	if err := inTx(db, func(ctx context.Context, q Q) error {
		return domain.RecordAcceptedCommit(ctx, q, project, commit)
	}); err != nil {
		t.Fatalf("record the accepted commit of %s: %v", project, err)
	}
}

// wsStart starts a wiki update of the Project at the time and returns its run.
func wsStart(t *testing.T, db *sql.DB, project string, at time.Time) string {
	t.Helper()
	run := wrNewRun(t)
	if err := inTx(db, func(ctx context.Context, q Q) error {
		return domain.StartWikiUpdate(ctx, q, at, domain.WikiStart{Project: project, RunID: run, StartCommit: wrCommitA,
			SessionPath: "sessions/" + run, Binding: "fake/fake", HardDeadlineMinutes: 240})
	}); err != nil {
		t.Fatalf("start a wiki update of %s: %v", project, err)
	}
	return run
}

// wsEnded starts a wiki update of the Project at the time and ends its run.
// stopped states that Culler owes no container stop for the run.
func wsEnded(t *testing.T, db *sql.DB, project string, at time.Time, end domain.WikiRunEnd, stopped bool) string {
	t.Helper()
	run := wsStart(t, db, project, at)
	end.Stopped = stopped
	wrEndRun(t, db, run, end)
	return run
}

// wsStored reads one column of the wiki update of the run.
func wsStored(t *testing.T, db *sql.DB, run, column string) string {
	t.Helper()
	return wrText(t, db, `SELECT `+column+` FROM wiki_updates WHERE run_id=?`, run)
}

// wsStatus sets the status of the Project as the operator does.
func wsStatus(t *testing.T, db *sql.DB, project, status string) {
	t.Helper()
	if _, err := ProjectSetStatus(db, nil, project, status); err != nil {
		t.Fatalf("set the status of %s to %s: %v", project, status, err)
	}
}

// The read returns one row for each Project that is not archived, in
// identifier order, with each state and each result text.
func TestWikiStateListRows(t *testing.T) {
	db := dvSpine(t)
	clocks := fixClocks(t, db)
	for id, status := range map[string]string{"ws-active": "active", "ws-archived": "archived", "ws-failed": "active",
		"ws-new": "active", "ws-nochange": "active", "ws-refused": "active"} {
		dvAddProject(t, db, id, status)
	}
	started := clocks.now.Add(-10 * time.Minute)
	stamp := started.Format(spineTime)
	current := func(project string) string {
		t.Helper()
		run, ok, err := wikitest.RecordCurrentWiki(context.Background(), db, project, wrCommitA, started)
		if err != nil || !ok {
			t.Fatalf("record the wiki update of %s: %v, %v", project, ok, err)
		}
		return run
	}
	// An archived Project keeps its wiki updates, and the read holds no row of it.
	current("ws-archived")
	noChange := current("ws-nochange")
	active := wsStart(t, db, "ws-active", started)
	failed := wsEnded(t, db, "ws-failed", started, wsTimeLimit, true)
	wsAccept(t, db, "ws-failed", wrCommitA)
	refused := wsEnded(t, db, "ws-refused", started, wsRefused, true)
	wsAccept(t, db, "ws-refused", wrCommitA)
	// ws-test merges a wiki commit. The merge moves the accepted commit to the merged commit.
	wsAccept(t, db, "ws-test", wrCommitA)
	u := wmFixtureUpdate(t, db)
	candidate := u.candidate()
	merge := u.startRun(dispatch.OperationApply, "")
	u.record("apply", merge, u.applied(merge, candidate))

	last := func(run, result, text string, cause, wiki, merged any) map[string]any {
		return map[string]any{"run_id": run, "started_at": wsStored(t, db, run, "started_at"), "ended_at": wsStored(t, db, run, "ended_at"),
			"result": result, "result_text": text, "cause_text": cause, "start_commit": wrCommitA, "wiki_commit": wiki, "merged_commit": merged}
	}
	want := []map[string]any{
		{"project": "ws-active", "state": "update-active", "state_text": "A wiki update is active.", "accepted_commit": nil,
			"next_start_at": nil, "next_start_text": nil, "last": nil,
			"active": map[string]any{"run_id": active, "started_at": stamp, "agent_started_at": nil, "last_heartbeat_at": nil,
				"run_ended_at": nil, "step": "run", "step_text": wsRunText, "repair_text": nil}},
		{"project": "ws-failed", "state": "behind", "state_text": wsBehindText, "accepted_commit": wrCommitA,
			"next_start_at": started.Add(time.Hour).Format(spineTime), "next_start_text": nil, "active": nil,
			"last": last(failed, "failed", "The wiki update failed.", wsTimeLimit.Cause, nil, nil)},
		{"project": "ws-new", "state": "behind", "state_text": wsBehindText, "accepted_commit": nil,
			"next_start_at": nil, "next_start_text": "Culler cannot read the main branch of the Project repository.", "active": nil, "last": nil},
		{"project": "ws-nochange", "state": "current", "state_text": "The Project wiki is current.", "accepted_commit": wrCommitA,
			"next_start_at": nil, "next_start_text": nil, "active": nil,
			"last": last(noChange, "succeeded", "The Project wiki needed no change.", nil, wrCommitA, nil)},
		{"project": "ws-refused", "state": "behind", "state_text": wsBehindText, "accepted_commit": wrCommitA,
			"next_start_at": nil, "next_start_text": wsDueText, "active": nil,
			"last": last(refused, "failed", "The connection check prevented the start of the wiki update.", wsRefused.Cause, nil, nil)},
		{"project": "ws-test", "state": "current", "state_text": "The Project wiki is current.", "accepted_commit": wrCommitC,
			"next_start_at": nil, "next_start_text": nil, "active": nil,
			"last": last(u.run, "succeeded", wsMergedText, nil, wrCommitB, wrCommitC)},
	}
	got := wsStates(t, db)
	if len(got) != len(want) {
		t.Fatalf("the read holds %d rows, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("row %d =\n%v\nwant\n%v", i, got[i], want[i])
		}
	}
	if stored := wsStored(t, db, noChange, "ended_at"); stored != stamp {
		t.Errorf("end time of the wiki update of ws-nochange = %q, want %q", stored, stamp)
	}
}

// The read states when the next wiki update of a Project with a wiki that is
// behind can start. The first condition that applies gives the text.
func TestWikiStateListNextStart(t *testing.T) {
	const (
		paused   = "The Project is paused. The next wiki update starts after the Project is active."
		unknown  = "Culler cannot read the main branch of the Project repository."
		stopping = "Culler stops the container of the last Scribe run. The next wiki update starts after that."
	)
	db := dvSpine(t)
	clocks := fixClocks(t, db)
	dvExec(t, db, `UPDATE settings SET wiki_update_interval_s=7200`)
	interval := 2 * time.Hour
	recent, old := clocks.now.Add(-10*time.Minute), clocks.now.Add(-3*time.Hour)
	for _, c := range []struct {
		project string
		// accepted states that the spine holds an accepted commit of the Project.
		accepted bool
		// start is the start time of the last wiki update. A zero time gives no wiki update.
		start   time.Time
		end     domain.WikiRunEnd
		stopped bool
		status  string
		at      any
		text    any
	}{
		// Each later condition also applies to the paused Project and to the
		// Project with no accepted commit.
		{project: "ws-paused", start: recent, end: wsTimeLimit, status: "paused", text: paused},
		{project: "ws-unknown", start: recent, end: wsTimeLimit, text: unknown},
		// The interval gives the time also while Culler owes the container stop.
		{project: "ws-interval", accepted: true, start: recent, end: wsTimeLimit, at: recent.Add(interval).Format(spineTime)},
		{project: "ws-second", accepted: true, start: clocks.now.Add(time.Second - interval), end: wsTimeLimit, stopped: true,
			at: clocks.now.Add(time.Second).Format(spineTime)},
		{project: "ws-edge", accepted: true, start: clocks.now.Add(-interval), end: wsTimeLimit, stopped: true, text: wsDueText},
		{project: "ws-stopping", accepted: true, start: old, end: wsTimeLimit, text: stopping},
		{project: "ws-due", accepted: true, start: old, end: wsTimeLimit, stopped: true, text: wsDueText},
		// A wiki update that does not count leaves the next start due.
		{project: "ws-uncounted", accepted: true, start: recent, end: wsRefused, stopped: true, text: wsDueText},
		{project: "ws-first", accepted: true, text: wsDueText},
	} {
		dvAddProject(t, db, c.project, "active")
		if c.accepted {
			wsAccept(t, db, c.project, wrCommitA)
		}
		if !c.start.IsZero() {
			wsEnded(t, db, c.project, c.start, c.end, c.stopped)
		}
		if c.status != "" {
			wsStatus(t, db, c.project, c.status)
		}
		row := wsState(t, db, c.project)
		if row["state"] != "behind" || row["next_start_at"] != c.at || row["next_start_text"] != c.text {
			t.Errorf("%s: state %v, next start at %v with the text %v; want behind, %v, and %v",
				c.project, row["state"], row["next_start_at"], row["next_start_text"], c.at, c.text)
		}
	}

	// A wiki that is current or has an active wiki update states no next start.
	if _, ok, err := wikitest.RecordCurrentWiki(context.Background(), db, "ws-test", wrCommitA, recent); err != nil || !ok {
		t.Fatalf("record the wiki update of ws-test: %v, %v", ok, err)
	}
	wsStart(t, db, "ws-first", clocks.now)
	for project, state := range map[string]string{"ws-test": "current", "ws-first": "update-active"} {
		row := wsState(t, db, project)
		if row["state"] != state || row["next_start_at"] != nil || row["next_start_text"] != nil {
			t.Errorf("%s: state %v, next start at %v with the text %v; want %s and no next start",
				project, row["state"], row["next_start_at"], row["next_start_text"], state)
		}
	}
}

// The read states the step of an active wiki update. While the result of an
// interrupted merge has no proof, it also states the repair of the operator
// checkout. The run list states the same text for the run.
func TestWikiStateListSteps(t *testing.T) {
	const refusal = "A merge into main stopped before it updated the branch. If the file .git/index.lock exists, remove it. " +
		"Then discard each change in the checkout that you did not make, and remove the file .git/mc/applications/0123456789abcdef.json."
	for _, test := range []struct {
		name   string
		check  func(t *testing.T, db *sql.DB, u *wmUpdate, run string) // the end of one receipt recovery with no proof
		repair string
	}{
		{"a refusal text", func(_ *testing.T, _ *sql.DB, u *wmUpdate, run string) {
			u.record("reconcile", run, RepositoryResult{Status: "operator-attention", OperatorAction: refusal})
		}, refusal},
		{"a check with no result", func(t *testing.T, db *sql.DB, _ *wmUpdate, run string) { stFail(t, db, run, false) },
			"The last check ended with no result. The repository operation failed."},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := dvSpine(t)
			fixClocks(t, db)
			u := wmFixtureUpdate(t, db)
			step := func(when, want, text string, repair any) {
				t.Helper()
				row := wsState(t, db, "ws-test")
				active, _ := row["active"].(map[string]any)
				if row["state"] != "update-active" || active["step"] != want || active["step_text"] != text || active["repair_text"] != repair {
					t.Fatalf("%s: wiki state %v with the active wiki update %v; want the step %s, the text %q, and the repair text %v",
						when, row["state"], row["active"], want, text, repair)
				}
				// The run list states no text while the run is active.
				var listed any
				if want != "run" {
					listed = text
				}
				if got := wsRunUpdate(t, db, u.run); !reflect.DeepEqual(got, map[string]any{"result": nil, "result_text": listed, "cause_text": nil}) {
					t.Fatalf("%s: wiki update of the run in the run list = %v, want the text %v and no result", when, got, listed)
				}
			}
			step("an active Scribe run", "run", wsRunText, nil)
			wsStatus(t, db, "ws-test", "paused")
			step("an active Scribe run of a paused Project", "run", wsRunText, nil)
			wsStatus(t, db, "ws-test", "active")

			candidate := u.candidate()
			step("a wiki commit that waits for its merge", "merge", wsMergeText, nil)
			wsStatus(t, db, "ws-test", "paused")
			step("a paused Project", "paused", wsPausedText, nil)
			wsStatus(t, db, "ws-test", "active")
			merge := u.startRun(dispatch.OperationApply, "")
			step("an active merge", "merge", wsMergeText, nil)
			wsStatus(t, db, "ws-test", "paused")
			step("an active merge of a paused Project", "merge", wsMergeText, nil)

			// The check of the merge result comes before the pause.
			stFail(t, db, merge, false)
			step("a merge with no result in a paused Project", "receipt", wsReceiptText, nil)
			wsStatus(t, db, "ws-test", "active")
			step("a merge with no result", "receipt", wsReceiptText, nil)
			check := u.startRun(dispatch.OperationReconcile, merge)
			step("an active receipt recovery", "receipt", wsReceiptText, nil)
			test.check(t, db, u, check)
			step("a receipt recovery with no proof", "receipt", wsReceiptText, test.repair)
			check = u.startRun(dispatch.OperationReconcile, merge)
			step("the next receipt recovery", "receipt", wsReceiptText, test.repair)

			u.record("reconcile", check, u.applied(merge, candidate))
			row := wsState(t, db, "ws-test")
			last, _ := row["last"].(map[string]any)
			if row["active"] != nil || last["run_id"] != u.run || last["result_text"] != wsMergedText {
				t.Fatalf("wiki state after the proven merge = %v, want no active wiki update and the merged wiki update %s", row, u.run)
			}
		})
	}
}

// Each row of the run list holds the key wiki_update. It is null for a run
// with no wiki update. For a Scribe run it holds the result of the wiki update.
func TestRunListAddsTheWikiUpdate(t *testing.T) {
	db := dvSpine(t)
	fixClocks(t, db)
	update := func(result, text, cause any) map[string]any {
		return map[string]any{"result": result, "result_text": text, "cause_text": cause}
	}
	want := func(when, run string, want any) {
		t.Helper()
		if got := wsRunUpdate(t, db, run); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: wiki update of the run %s = %v, want %v", when, run, got, want)
		}
	}
	stTask(t, db, 1, "ws-test", dispatch.StatusSeeded)
	stEndedRun(t, db, 1, "worker-run")
	want("a run of a task", "worker-run", nil)

	u := wmFixtureUpdate(t, db)
	want("an active Scribe run", u.run, update(nil, nil, nil))
	candidate := u.candidate()
	want("a wiki commit that waits for its merge", u.run, update(nil, wsMergeText, nil))
	merge := u.startRun(dispatch.OperationApply, "")
	// The run of a repository operation of a wiki update is no wiki update run.
	want("the run of the merge", merge, nil)
	u.record("apply", merge, u.applied(merge, candidate))
	want("a merged wiki commit", u.run, update("succeeded", wsMergedText, nil))

	started := time.Now().Add(-time.Minute)
	for project, c := range map[string]struct {
		end  *domain.WikiRunEnd // nil for a successful wiki update with no wiki change
		want map[string]any
	}{
		"ws-nochange": {nil, update("succeeded", "The Project wiki needed no change.", nil)},
		"ws-failed":   {&wsTimeLimit, update("failed", "The wiki update failed.", wsTimeLimit.Cause)},
		"ws-refused":  {&wsRefused, update("failed", "The connection check prevented the start of the wiki update.", wsRefused.Cause)},
	} {
		dvAddProject(t, db, project, "active")
		var run string
		if c.end == nil {
			var ok bool
			var err error
			if run, ok, err = wikitest.RecordCurrentWiki(context.Background(), db, project, wrCommitA, started); err != nil || !ok {
				t.Fatalf("record the wiki update of %s: %v, %v", project, ok, err)
			}
		} else {
			run = wsEnded(t, db, project, started, *c.end, true)
		}
		want("the wiki update of "+project, run, c.want)
	}
}

// A customization conflict of a wiki update run ends the wiki update as
// failed. The customization read of the role then shows the conflict with no
// block.
func TestRoleShowReportsNoScribeBlock(t *testing.T) {
	db := dvSpine(t)
	saveCustom(t, db, dispatch.RoleScribe, "Report false evidence.")
	run := wrWantSpawn(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test")
	if _, err := RunCustomizationConflict(db, wcScribe(run), CustomizationConflictRequest{Run: run,
		Passage: "Report false evidence.", Requirement: "Report evidence truthfully.", Reason: "False evidence cannot be truthful."}); err != nil {
		t.Fatalf("customization conflict of the Scribe: %v", err)
	}
	shown, err := ProjectRoleShow(db, nil, "ws-test", dispatch.RoleScribe)
	if err != nil {
		t.Fatalf("customization read of the Scribe: %v", err)
	}
	// The dashboard reads the JSON form of the result.
	body, err := json.Marshal(shown)
	if err != nil {
		t.Fatal(err)
	}
	var read struct {
		Conflicts []map[string]any `json:"conflicts"`
	}
	if err := json.Unmarshal(body, &read); err != nil {
		t.Fatal(err)
	}
	if len(read.Conflicts) != 1 {
		t.Fatalf("conflicts of the role = %v, want one conflict", read.Conflicts)
	}
	conflict := read.Conflicts[0]
	if conflict["run"] != run || conflict["role"] != "scribe" || conflict["task"] != nil || conflict["blocked"] != false ||
		conflict["resolution"] != "The wiki update ended as failed." {
		t.Fatalf("conflict of the Scribe = %v, want the conflict of the run %s with no task and no block", conflict, run)
	}
	if got := wsRunUpdate(t, db, run); !reflect.DeepEqual(got, map[string]any{"result": "failed", "result_text": "The wiki update failed.",
		"cause_text": "The custom instructions of the Scribe conflict with the Culler contract."}) {
		t.Fatalf("wiki update of the run in the run list = %v, want the failed wiki update with the cause of the conflict", got)
	}
}

// A wiki update run holds no lease. The connection status reads the heartbeat
// of its active wiki update, so the run is an attempt of a connection retry.
func TestConnectionAttemptStatusOfAWikiUpdateRun(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	healthObservation(t, db, healthCheck(t, db, "authorization"), "ready", "", 0)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 0))
	networkRun(t, db, "prime")
	networkReport(t, db, "prime", domain.InfrastructureFailureReport{Condition: "The response stream disconnected.", Source: "runner", NetworkError: "reset"})
	dvExec(t, db, `UPDATE connection_health SET next_retry_at='2000-01-01T00:00:00Z' WHERE profile_id='profile'`)

	// The wiki update run uses the admitted state of the profile, as a Worker run does.
	_, revision, err := ConnectionAdmission(context.Background(), db, "test", "profile", time.Now(), "revision")
	if err != nil {
		t.Fatal(err)
	}
	run := wrNewRun(t)
	wrFresh(t, db, "ws-test", run, wrCommitA)
	dvExec(t, db, `UPDATE runs SET connection_id='test',authorization_id='authorization',connection_revision=?,profile_id='profile',profile_revision='revision' WHERE id=?`, revision, run)
	attempt := func(when, state string) {
		t.Helper()
		status := recoveryStatus(t, db)
		if status.RetryPhase != "active" || len(status.Attempts) != 1 {
			t.Fatalf("%s: retry phase %q with the attempts %+v, want the phase active and one attempt", when, status.RetryPhase, status.Attempts)
		}
		if a := status.Attempts[0]; a.RunID != run || a.Role != "scribe" || a.ProjectID != "ws-test" || a.TaskID != 0 || a.State != state {
			t.Fatalf("%s: attempt = %+v, want the Scribe run %s of ws-test with no task in the state %s", when, a, run, state)
		}
	}
	attempt("a run with no heartbeat", "starting")
	if _, err := Heartbeat(db, wcScribe(run), run); err != nil {
		t.Fatalf("heartbeat of the wiki update run: %v", err)
	}
	attempt("a run with a heartbeat", "active")
	wrOverdue(t, db, run)
	attempt("a run with an old heartbeat", "stale")

	// The run ends with a wiki commit. The wiki update stays active for its
	// merge, and the connection has no attempt.
	wrEndRun(t, db, run, domain.WikiRunEnd{Outcome: "completed", WikiCommit: wrCommitB})
	if status := recoveryStatus(t, db); len(status.Attempts) != 0 || status.RetryPhase == "active" {
		t.Fatalf("status after the end of the run: retry phase %q with the attempts %+v, want no attempt", status.RetryPhase, status.Attempts)
	}
	if active := wsState(t, db, "ws-test")["active"]; active == nil {
		t.Fatal("the wiki update ended with its run, want an active wiki update that waits for its merge")
	}
}
