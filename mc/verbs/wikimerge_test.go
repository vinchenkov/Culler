package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"mc/dispatch"
	"mc/domain"
	"mc/repository"
)

// wmUpdate is one wiki update of the Project ws-test. With a delivery fixture,
// the Scribe run has an isolated repository and each operation executes its
// host work. With no delivery fixture, the commits are fixture values and
// each result is the record that the host work returns.
type wmUpdate struct {
	t           *testing.T
	db          *sql.DB
	f           *deliveryFixture
	run         string // the run of the wiki update
	startCommit string
	commit      string // the wiki commit, after submit
}

// The end of a wiki update, as one line of wmUpdate.end.
const wmActive = "result=<nil>|cause=<nil>|counted=1|destination_commit=<nil>|merged_commit=<nil>|\n"

func wmFailed(cause string) string {
	return "result=failed|cause=" + cause + "|counted=1|destination_commit=<nil>|merged_commit=<nil>|\n"
}

func wmSucceeded(destination, merged string) string {
	return "result=succeeded|cause=<nil>|counted=1|destination_commit=" + destination + "|merged_commit=" + merged + "|\n"
}

// wmReady records the repository of the Scribe run of a wiki update at its
// start commit, as the resident records it before the run starts.
func wmReady(t *testing.T, db *sql.DB, run, start string) {
	t.Helper()
	if _, err := RepositoryRecord(db, nil, "ready", run, RepositoryResult{WikiUpdate: run, InputCommit: start, DestinationCommit: start}); err != nil {
		t.Fatalf("record the repository of the Scribe run %s: %v", run, err)
	}
}

// wmFixtureUpdate starts a wiki update of ws-test with fixture commits. No
// Git repository holds them.
func wmFixtureUpdate(t *testing.T, db *sql.DB) *wmUpdate {
	t.Helper()
	u := &wmUpdate{t: t, db: db, run: wrNewRun(t), startCommit: wrCommitA}
	wrFresh(t, db, "ws-test", u.run, u.startCommit)
	wmReady(t, db, u.run, u.startCommit)
	return u
}

// wmRestartResident registers a new resident owner. Its first heartbeat ends
// each execution of the earlier owner with no result. It returns the ended runs.
func wmRestartResident(t *testing.T, db *sql.DB) []string {
	t.Helper()
	resp, err := DeliveryHeartbeat(db, nil, HeartbeatRequest{Owner: stRegister(t, db)})
	if err != nil {
		t.Fatal(err)
	}
	return resp.Ended
}

// wmStart sends one start request of the current owner. It requires exactly
// the started operations, as operation/task or operation/wiki, and no failed
// start. Each stopped run is a stop that the owner confirms.
func wmStart(t *testing.T, db *sql.DB, stopped []string, want ...string) StartResponse {
	t.Helper()
	resp := stStart(t, db, testOwner(t, db), stopped...)
	stWantStarted(t, db, resp, want...)
	if len(resp.Failed) != 0 {
		t.Fatalf("the start request failed the starts %+v", resp.Failed)
	}
	return resp
}

// wmDecision holds each result that a tick computes from the task records of ws-test.
type wmDecision struct {
	rec    dispatch.Records
	lease  dispatch.Lease
	action dispatch.Action
	hold   bool // the agent run hold
}

func wmDecide(t *testing.T, db *sql.DB) wmDecision {
	t.Helper()
	var d wmDecision
	if err := inTx(db, func(ctx context.Context, q Q) error {
		sel, err := selectFromSpine(ctx, q, nil, "ws-test")
		if err != nil {
			return err
		}
		d = wmDecision{rec: sel.rec, lease: sel.lease, action: sel.action,
			hold: dispatch.AgentRunHold(sel.rec, sel.now)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return d
}

func wmTask(t *testing.T, db *sql.DB) {
	t.Helper()
	dvInsertTask(t, db, dvTask(2, dispatch.ScopeTask, dispatch.StatusPackaged, 1))
}

// wmPipelineFacts reads the pipeline facts of wrPipelineFacts without the run
// of each execution. Each repository operation of a wiki update adds such a run.
func wmPipelineFacts(t *testing.T, db *sql.DB) string {
	t.Helper()
	return strings.Join([]string{
		wrDump(t, db, `SELECT * FROM tasks ORDER BY id`),
		wrDump(t, db, `SELECT * FROM leases ORDER BY project`),
		wrDump(t, db, `SELECT id, status, setup_status, infrastructure_failure_count, infrastructure_next_retry_at,
			infrastructure_last_failed_run_id, infrastructure_condition, connection_wait, connection_wait_task FROM projects ORDER BY id`),
		wrDump(t, db, `SELECT project, COUNT(*) AS attention FROM tasks WHERE archived=0 AND (stalled=1 OR blocked=1) GROUP BY project`),
		wrDump(t, db, `SELECT * FROM infrastructure_failures ORDER BY run_id`),
		wrDump(t, db, `SELECT * FROM proposal_decisions ORDER BY id`),
		wrDump(t, db, `SELECT id, role, subject, ended_at, outcome FROM runs WHERE role IS NOT 'scribe' AND binding IS NOT 'mechanical' ORDER BY id`),
	}, "\n")
}

// wmRecords loads the repository facts of the wiki update of ws-test.
func wmRecords(t *testing.T, db *sql.DB) dispatch.WikiMergeRecords {
	t.Helper()
	var rec dispatch.WikiMergeRecords
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		rec, err = loadWikiMergeRecords(ctx, q, "ws-test")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return rec
}

// wmFailedStart starts one operation as a start request starts it. It
// requires a failed start and returns its failure.
func wmFailedStart(t *testing.T, db *sql.DB, op dispatch.RepositoryOperation) StartFailure {
	t.Helper()
	owner := testOwner(t, db)
	var resp StartResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		now, err := spineClock(ctx, q)
		if err != nil {
			return err
		}
		clock, err := vmClock()
		if err != nil {
			return err
		}
		return startOne(ctx, q, now, clock, owner, 1, op, &resp)
	}); err != nil {
		t.Fatal(err)
	}
	if len(resp.Started) != 0 || len(resp.Failed) != 1 {
		t.Fatalf("start of %s = %+v, want one failure", op.Operation, resp)
	}
	return resp.Failed[0]
}

// wmRecordFailedStart records a failed start of the operation with the
// condition, as a start request records it. It returns the run of the failed
// execution.
func wmRecordFailedStart(t *testing.T, db *sql.DB, op dispatch.RepositoryOperation, condition string) string {
	t.Helper()
	owner := testOwner(t, db)
	var run string
	if err := inTx(db, func(ctx context.Context, q Q) error {
		now, err := spineClock(ctx, q)
		if err != nil {
			return err
		}
		clock, err := vmClock()
		if err != nil {
			return err
		}
		run, err = recordFailedStart(ctx, q, now, clock, owner, op, condition)
		return err
	}); err != nil {
		t.Fatalf("record the failed start of %s: %v", op.Operation, err)
	}
	return run
}

// wikiUpdate starts a wiki update at the commit of culler with the isolated
// repository of its Scribe run.
func (f *deliveryFixture) wikiUpdate() *wmUpdate {
	f.t.Helper()
	u := &wmUpdate{t: f.t, db: f.db, f: f, run: wrNewRun(f.t), startCommit: srcGit(f.t, f.workspace, "rev-parse", "culler")}
	wrFresh(f.t, f.db, "ws-test", u.run, u.startCommit)
	u.allocate()
	return u
}

// wikiRecords loads the repository facts of the wiki update of the Project.
func (f *deliveryFixture) wikiRecords() dispatch.WikiMergeRecords {
	f.t.Helper()
	return wmRecords(f.t, f.db)
}

// request sends one start request and requires exactly the started
// operations. It executes and records each one as the resident does, and
// returns the result of each operation.
func (f *deliveryFixture) request(want ...string) []RepositoryResult {
	f.t.Helper()
	var results []RepositoryResult
	for _, run := range wmStart(f.t, f.db, nil, want...).Started {
		results = append(results, f.runExecution(executionEffect(f.t, f.db, run)))
	}
	return results
}

// archive archives the Project and applies the wiki facts of a start request.
// That step ends the active wiki update of an archived Project as failed.
func (f *deliveryFixture) archive() {
	f.t.Helper()
	if _, err := ProjectSetStatus(f.db, nil, "ws-test", "archived"); err != nil {
		f.t.Fatal(err)
	}
	f.transaction(func(ctx context.Context, q Q) error { return recordStartWikiFacts(ctx, q, nil) })
}

// allocate builds and records the isolated repository of the Scribe run as
// the resident does.
func (u *wmUpdate) allocate() {
	f := u.f
	f.t.Helper()
	envelope := RepositoryEnvelope{Operation: "allocate", WikiUpdate: u.run, RunID: u.run, WikiOnly: true, WorkspaceRoot: f.workspace,
		RunRoot: f.wikiRoot(u.run), InputCommit: u.startCommit, DestinationBranch: "culler"}
	f.record("ready", u.run, f.native(envelope))
	envelope.Operation = "ready"
	f.record("ready", u.run, f.native(envelope))
}

// commitPage commits one new wiki page in the repository of the Scribe run
// and returns the commit.
func (u *wmUpdate) commitPage() string {
	t := u.t
	t.Helper()
	repo := filepath.Join(u.f.wikiRoot(u.run), "repo")
	writeFile(t, filepath.Join(repo, "openwiki/flow.md"), "---\ntitle: Request flow\ntype: Component\n---\nA generated explanation.\n")
	srcGit(t, repo, "add", "openwiki")
	srcGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "Update the Project wiki")
	return srcGit(t, repo, "rev-parse", "HEAD")
}

// submit ends the Scribe run with a wiki commit. With a delivery fixture, the
// wiki commit adds one page in the repository of the run.
func (u *wmUpdate) submit() {
	u.t.Helper()
	u.commit = wrCommitB
	if u.f != nil {
		u.commit = u.commitPage()
	}
	wrEndRun(u.t, u.db, u.run, domain.WikiRunEnd{Outcome: "completed", WikiCommit: u.commit})
}

// operation describes one repository operation of the wiki update.
func (u *wmUpdate) operation(operation dispatch.Operation, run string) dispatch.RepositoryOperation {
	return dispatch.RepositoryOperation{Project: "ws-test", WikiUpdate: u.run, Operation: operation, RunID: run}
}

// start starts one repository operation of the wiki update and returns the
// stored effect.
func (u *wmUpdate) start(operation dispatch.Operation, run string) map[string]any {
	u.t.Helper()
	return startOperation(u.t, u.db, u.operation(operation, run))
}

// startRun starts one repository operation of the wiki update and returns the
// run of its execution.
func (u *wmUpdate) startRun(operation dispatch.Operation, run string) string {
	u.t.Helper()
	return u.start(operation, run)["run_id"].(string)
}

// startError returns the error of a refused start. The refused start rolls back.
func (u *wmUpdate) startError(operation dispatch.Operation, run string) error {
	t := u.t
	t.Helper()
	owner := testOwner(t, u.db)
	clock, err := vmClock()
	if err != nil {
		t.Fatal(err)
	}
	var refusal error
	_ = inTx(u.db, func(ctx context.Context, q Q) error {
		_, refusal = startRepositoryOperation(ctx, q, clock, owner, u.operation(operation, run))
		return refusal
	})
	if refusal == nil {
		t.Fatalf("the start of %s succeeded, want a refusal", operation)
	}
	return refusal
}

// record records one result of an operation of the wiki update.
func (u *wmUpdate) record(operation, run string, result RepositoryResult) {
	u.t.Helper()
	result.WikiUpdate = u.run
	if _, err := RepositoryRecord(u.db, nil, operation, run, result); err != nil {
		u.t.Fatalf("the %s result of the wiki update failed: %v", operation, err)
	}
}

// capture starts the capture of the wiki commit and records its result.
func (u *wmUpdate) capture() {
	u.t.Helper()
	effect := u.start(dispatch.OperationCapture, u.run)
	if u.f != nil {
		u.f.runExecution(effect)
		return
	}
	u.record("capture", effect["run_id"].(string), RepositoryResult{InputCommit: u.startCommit, ResultCommit: u.commit, RetainedRef: "refs/mc/wiki/commits/" + u.run})
}

// clean starts the cleanup of one temporary repository of the wiki update and
// records its result.
func (u *wmUpdate) clean(run string) {
	u.t.Helper()
	effect := u.start(dispatch.OperationCleanup, run)
	if u.f != nil {
		u.f.runExecution(effect)
		return
	}
	u.record("cleanup", effect["run_id"].(string), RepositoryResult{})
}

// storeCandidate executes one started merge candidate step, records its
// result, and returns the merged candidate.
func (u *wmUpdate) storeCandidate(effect map[string]any) repository.Candidate {
	u.t.Helper()
	run := effect["run_id"].(string)
	result := RepositoryResult{RetainedRef: "refs/mc/wiki/candidates/" + run, Candidate: &repository.Candidate{
		SubmittedCommit: u.commit, DestinationBranch: "culler", DestinationCommit: u.startCommit, Commit: wrCommitC, Status: "merged"}}
	if u.f != nil {
		result = u.f.nativeExecution(effect)
	}
	if result.Candidate == nil || result.Candidate.Status != "merged" || result.Status != "" {
		u.t.Fatalf("merge candidate step = %+v, want a merged candidate", result)
	}
	u.record("prepare", run, result)
	return *result.Candidate
}

// candidate takes a new wiki update to a merged candidate. It cleans each
// temporary repository, so that a start request starts no cleanup.
func (u *wmUpdate) candidate() repository.Candidate {
	u.t.Helper()
	u.submit()
	u.capture()
	u.clean(u.run)
	effect := u.start(dispatch.OperationPrepare, "")
	candidate := u.storeCandidate(effect)
	if u.f != nil {
		u.clean(effect["run_id"].(string))
	}
	return candidate
}

// applied is the result of a merge that the authorization proves.
func (u *wmUpdate) applied(authorization string, candidate repository.Candidate) RepositoryResult {
	return RepositoryResult{WikiUpdate: u.run, Applied: &repository.Applied{Authorization: authorization, Commit: candidate.Commit}}
}

// wmResult builds the result of a receipt recovery from the merge
// authorization and its candidate.
type wmResult func(u *wmUpdate, authorization string, candidate repository.Candidate) RepositoryResult

// wmApplied is the result of a receipt recovery that proves the merge.
func wmApplied(u *wmUpdate, authorization string, candidate repository.Candidate) RepositoryResult {
	return u.applied(authorization, candidate)
}

// wmNotApplied is the result of a receipt recovery that proves no merge. The
// destination is the live commit of culler.
func wmNotApplied(destination string) wmResult {
	return func(*wmUpdate, string, repository.Candidate) RepositoryResult {
		return RepositoryResult{Status: "not-applied", DestinationCommit: destination}
	}
}

// row reads the wiki update as one line.
func (u *wmUpdate) row() string {
	u.t.Helper()
	return wrDump(u.t, u.db, `SELECT * FROM wiki_updates WHERE run_id=?`, u.run)
}

// end reads the end of the wiki update as one line.
func (u *wmUpdate) end() string {
	u.t.Helper()
	return wrDump(u.t, u.db, `SELECT result,cause,counted,destination_commit,merged_commit FROM wiki_updates WHERE run_id=?`, u.run)
}

// wmExecution reads the subject, the target, and the end of one execution as one line.
func wmExecution(t *testing.T, db *sql.DB, run string) string {
	t.Helper()
	return wrDump(t, db, `SELECT operation,target,task_id,wiki_update,end_reason,charged,retry_after IS NOT NULL AS retry,end_condition
		FROM repository_executions WHERE run_id=?`, run)
}

// The task records of a Project hold one fact of a wiki update: receipt
// recovery checks the merge of a wiki commit. Each other repository fact of a
// wiki update is in the wiki records only.
func TestLoadRecordsHoldsOnlyTheReceiptRecoveryOfAWikiCommit(t *testing.T) {
	f := newDeliveryFixture(t)
	f.pass(f.add("Deliver the source change"), "feature.txt", "complete feature\n")
	// A second task has a submitted version, the retry time of a failed
	// capture, and an active capture.
	task := f.add("Deliver the second change")
	worker, root, id := f.run(task, "worker")
	repo := filepath.Join(root, "repo")
	writeFile(t, filepath.Join(repo, "second.txt"), "second feature\n")
	srcGit(t, repo, "add", ".")
	srcGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "Complete the contribution")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: worker, Status: "worked", Commit: srcGit(t, repo, "rev-parse", "HEAD")}); err != nil {
		t.Fatal(err)
	}
	capture := dispatch.RepositoryOperation{Project: "ws-test", TaskID: task, Operation: dispatch.OperationCapture, RunID: worker}
	stFail(t, f.db, startOperation(t, f.db, capture)["run_id"].(string), false)
	startOperation(t, f.db, capture)

	records := func() dispatch.Records {
		t.Helper()
		var rec dispatch.Records
		f.transaction(func(ctx context.Context, q Q) error {
			var err error
			rec, err = loadRecords(ctx, q, "ws-test")
			return err
		})
		return rec
	}
	before := records()
	if len(before.Executions) != 1 || len(before.Retries) != 1 || len(before.Tasks) != 2 || before.WikiReceiptRecovery {
		t.Fatalf("task records of the fixture = %d executions, %d retry times, %d tasks, receipt recovery fact %v; want 1, 1, 2, and no fact",
			len(before.Executions), len(before.Retries), len(before.Tasks), before.WikiReceiptRecovery)
	}
	same := func(state string) dispatch.WikiMergeRecords {
		t.Helper()
		if after := records(); !reflect.DeepEqual(after, before) {
			t.Fatalf("%s: the task records changed:\n%+v\nwant\n%+v", state, after, before)
		}
		return f.wikiRecords()
	}

	u := f.wikiUpdate()
	if wiki := same("a wiki update with an active run"); wiki.Update != nil || len(wiki.Executions)+len(wiki.Retries)+len(wiki.Cleanups) != 0 {
		t.Fatalf("wiki records beside an active Scribe run = %+v, want no fact", wiki)
	}
	u.submit()
	if wiki := same("a wiki commit that waits for its capture"); wiki.Update == nil || !wiki.Update.CaptureDue {
		t.Fatalf("wiki records after the end of the run = %+v, want a due capture", wiki.Update)
	}
	captured := u.start(dispatch.OperationCapture, u.run)
	if wiki := same("an active capture of the wiki commit"); !reflect.DeepEqual(wiki.Executions,
		[]dispatch.WikiExecution{{RunID: captured["run_id"].(string), Operation: dispatch.OperationCapture, Target: u.run}}) {
		t.Fatalf("active executions of the wiki update = %+v, want the capture", wiki.Executions)
	}
	f.record("capture", captured["run_id"].(string), f.nativeExecution(captured))
	// A failed cleanup leaves a retry time on the repository of the Scribe run.
	cleanup := u.start(dispatch.OperationCleanup, u.run)
	f.record("cleanup", cleanup["run_id"].(string), RepositoryResult{WikiUpdate: u.run, CleanupError: "The folder is busy."})
	wiki := same("a failed cleanup of the wiki update")
	if len(wiki.Retries) != 1 || wiki.Retries[0].Target != u.run || wiki.Retries[0].Operation != dispatch.OperationCleanup || wiki.Retries[0].Until.IsZero() {
		t.Fatalf("retry times of the wiki update = %+v, want the cleanup of %s", wiki.Retries, u.run)
	}
	if !reflect.DeepEqual(wiki.Cleanups, []dispatch.WikiCleanup{{WikiUpdate: u.run, RunID: u.run}}) || !wiki.Update.Captured {
		t.Fatalf("wiki records after the capture = %+v with the cleanups %+v, want the captured repository of the Scribe run", wiki.Update, wiki.Cleanups)
	}
	step := u.start(dispatch.OperationPrepare, "")
	if wiki := same("an active merge candidate step of the wiki commit"); !reflect.DeepEqual(wiki.Executions,
		[]dispatch.WikiExecution{{RunID: step["run_id"].(string), Operation: dispatch.OperationPrepare, Target: "wiki/" + u.run}}) {
		t.Fatalf("active executions of the wiki update = %+v, want the merge candidate step", wiki.Executions)
	}
	u.storeCandidate(step)
	if wiki := same("a merge candidate of the wiki commit"); !wiki.Update.Candidate || wiki.Update.ApplyRun != "" {
		t.Fatalf("wiki records after the merge candidate step = %+v, want a candidate and no merge authorization", wiki.Update)
	}
	merge := u.startRun(dispatch.OperationApply, "")
	if wiki := same("an unfinished merge authorization of the wiki commit"); wiki.Update.ApplyRun != merge || wiki.Update.ApplyEnded {
		t.Fatalf("wiki records beside the active merge = %+v, want the merge authorization %s", wiki.Update, merge)
	}
	// The merge ends with no result. The fact is then the only change of the task records.
	stFail(t, f.db, merge, false)
	ended := records()
	fact := ended.WikiReceiptRecovery
	ended.WikiReceiptRecovery = false
	if wiki := f.wikiRecords(); !fact || !reflect.DeepEqual(ended, before) || wiki.Update.ApplyRun != merge || !wiki.Update.ApplyEnded {
		t.Fatalf("after the merge with no result: receipt recovery fact %v with the task records\n%+v\nand the wiki records %+v; want the fact, no other change of\n%+v\nand the ended merge authorization %s",
			fact, ended, wiki.Update, before, merge)
	}
	// Receipt recovery proves that the merge did not run. The fact ends with the merge authorization.
	if result := f.runExecution(u.start(dispatch.OperationReconcile, merge)); result.Status != "not-applied" {
		t.Fatalf("receipt recovery = %+v, want not-applied", result)
	}
	if wiki := same("the result of the merge"); wiki.Update != nil {
		t.Fatalf("wiki records after the result = %+v, want no active wiki update", wiki.Update)
	}
}

// The start of each repository operation of a wiki update stores the effect
// that the resident executes, and writes the rows of the operation. The wiki
// records state each step of the merge.
func TestWikiOperationEffects(t *testing.T) {
	f := newDeliveryFixture(t)
	clocks := fixClocks(t, f.db)
	u := f.wikiUpdate()
	u.submit()
	limit := dfInt(t, f.db, `SELECT max_temporary_bytes FROM repository_limits WHERE project='ws-test'`)
	// check compares a stored effect with the base effect and the fields of
	// the operation. It also compares the execution row. It returns the run.
	check := func(effect map[string]any, operation, target string, fields map[string]any) string {
		t.Helper()
		run, _ := effect["run_id"].(string)
		want := map[string]any{"run_id": run, "operation": operation, "wiki_update": u.run, "project": "ws-test",
			"workspace_root": f.workspace, "destination_branch": "culler"}
		for key, value := range fields {
			want[key] = value
		}
		if !validLowercaseHex(run, 16) || !reflect.DeepEqual(effect, want) {
			t.Fatalf("%s effect = %v, want %v", operation, effect, want)
		}
		if got, want := wmExecution(t, f.db, run), "operation="+operation+"|target="+target+"|task_id=<nil>|wiki_update="+u.run+
			"|end_reason=<nil>|charged=0|retry=0|end_condition=<nil>|\n"; got != want {
			t.Fatalf("%s execution = %swant %s", operation, got, want)
		}
		return run
	}
	merges := func() string {
		t.Helper()
		return wrDump(t, f.db, `SELECT run_id,operation,task_id,version_id,packet_id,candidate_id,destination_branch,finished,result
			FROM merge_operations WHERE wiki_update=? ORDER BY rowid`, u.run)
	}
	merge := func(run, operation string, candidate any, finished int, result any) string {
		return "run_id=" + run + "|operation=" + operation + "|task_id=<nil>|version_id=<nil>|packet_id=<nil>|candidate_id=" + fmt.Sprint(candidate) +
			"|destination_branch=culler|finished=" + fmt.Sprint(finished) + "|result=" + fmt.Sprint(result) + "|\n"
	}
	// facts returns the merge facts of the active wiki update.
	facts := func() dispatch.WikiDelivery {
		t.Helper()
		wiki := f.wikiRecords().Update
		if wiki == nil || wiki.RunID != u.run {
			t.Fatalf("wiki records = %+v, want the facts of the wiki update %s", wiki, u.run)
		}
		return *wiki
	}

	// A wiki update has no destination observation.
	for _, operation := range []dispatch.Operation{dispatch.OperationObserve} {
		if err := u.startError(operation, ""); !wcRefused(err, "", "the repository operation is not valid for a wiki update") {
			t.Fatalf("start of %s = %v, want a refusal", operation, err)
		}
	}

	// Capture: the commits of the wiki update, and no transfer row.
	capture := u.start(dispatch.OperationCapture, u.run)
	check(capture, "capture", u.run, map[string]any{"input_commit": u.startCommit, "result_commit": u.commit, "producer_run": u.run})
	if n := dfInt(t, f.db, `SELECT COUNT(*) FROM repository_transfers`); n != 0 {
		t.Fatalf("the capture of a wiki commit wrote %d transfer rows, want none", n)
	}
	f.runExecution(capture)

	// Merge candidate step: the wiki commit and the start commit. The start
	// ends the wait for the destination quiet interval.
	dvExec(t, f.db, `UPDATE wiki_updates SET merge_after=datetime('now','+30 seconds') WHERE run_id=?`, u.run)
	if wiki := facts(); !wiki.Captured || wiki.WaitUntil == nil || wiki.Candidate || wiki.PrepareRun != "" || wiki.ApplyRun != "" {
		t.Fatalf("wiki records before the merge candidate step = %+v, want a captured wiki commit with a wait time", wiki)
	}
	prepare := u.start(dispatch.OperationPrepare, "")
	first := check(prepare, "prepare", "wiki/"+u.run, map[string]any{"max_temporary_bytes": limit, "submitted_commit": u.commit, "start_commit": u.startCommit})
	if got, want := merges(), merge(first, "prepare", nil, 0, nil); got != want {
		t.Fatalf("merge rows after the start of the merge candidate step = %swant %s", got, want)
	}
	if wiki := facts(); wiki.WaitUntil != nil || wiki.PrepareRun != "" {
		t.Fatalf("wiki records beside the active merge candidate step = %+v, want no wait time and no prepare recovery", wiki)
	}

	// Prepare recovery: the first restart of the resident interrupts the step
	// with no failure. The recovery names the interrupted run and finishes its row.
	if ended := wmRestartResident(t, f.db); !reflect.DeepEqual(ended, []string{first}) {
		t.Fatalf("the resident restart ended %v, want the merge candidate step %s", ended, first)
	}
	if got, want := wmExecution(t, f.db, first), "operation=prepare|target=wiki/"+u.run+"|task_id=<nil>|wiki_update="+u.run+
		"|end_reason=owner-change|charged=0|retry=1|end_condition=A resident restart ended the repository operation.|\n"; got != want {
		t.Fatalf("interrupted merge candidate step = %swant %s", got, want)
	}
	if wiki := facts(); wiki.PrepareRun != first || !wiki.PrepareStopPending {
		t.Fatalf("wiki records after the interruption = %+v, want the prepare recovery of %s with no stop confirmation", wiki, first)
	}
	// The new owner confirms the stop after the heartbeat limit of the earlier owner.
	clocks.advance(domain.HeartbeatLimit)
	if resp, err := DeliveryHeartbeat(f.db, nil, HeartbeatRequest{Owner: testOwner(t, f.db), Stopped: []string{first}}); err != nil || !reflect.DeepEqual(resp.Confirmed, []string{first}) {
		t.Fatalf("stop confirmation of the interrupted step = %+v, %v; want %s", resp.Confirmed, err, first)
	}
	if wiki := facts(); wiki.PrepareRun != first || wiki.PrepareStopPending {
		t.Fatalf("wiki records after the stop confirmation = %+v, want the prepare recovery of %s", wiki, first)
	}
	if err := u.startError(dispatch.OperationPrepare, u.run); !wcRefused(err, "", "The wiki update has no interrupted merge candidate step.") {
		t.Fatalf("prepare recovery of a run with no merge row = %v, want a refusal", err)
	}
	recovery := u.start(dispatch.OperationPrepare, first)
	second := check(recovery, "prepare", "wiki/"+u.run, map[string]any{"max_temporary_bytes": limit, "submitted_commit": u.commit,
		"start_commit": u.startCommit, "previous_run": first})
	if got, want := merges(), merge(first, "prepare", nil, 1, `{"status":"interrupted"}`)+merge(second, "prepare", nil, 0, nil); got != want {
		t.Fatalf("merge rows after the start of the prepare recovery = %swant %s", got, want)
	}

	// Merge: the current candidate with no change data, and the execution run
	// as the merge authorization.
	if err := u.startError(dispatch.OperationApply, ""); !wcRefused(err, "", "The wiki update has no merge candidate.") {
		t.Fatalf("merge with no candidate = %v, want a refusal", err)
	}
	candidate := u.storeCandidate(recovery)
	id := dfInt(t, f.db, `SELECT id FROM merge_candidates WHERE wiki_update=? AND run_id=?`, u.run, second)
	if wiki := facts(); !wiki.Candidate || wiki.PrepareRun != "" || wiki.ApplyRun != "" {
		t.Fatalf("wiki records after the merge candidate step = %+v, want a candidate and no merge authorization", wiki)
	}
	if err := u.startError(dispatch.OperationApply, second); !wcRefused(err, "", "the application requires receipt reconciliation before another run") {
		t.Fatalf("merge with a run identifier = %v, want a refusal", err)
	}
	apply := u.start(dispatch.OperationApply, "")
	authorization := apply["run_id"].(string)
	check(apply, "apply", "wiki/"+u.run, map[string]any{"max_temporary_bytes": limit, "submitted_commit": u.commit,
		"application": repository.Application{Authorization: authorization, Candidate: candidate}})
	if got, want := dfStr(t, f.db, `SELECT run_id||'/'||candidate_id||'/'||finished FROM merge_operations WHERE wiki_update=? AND operation='apply'`, u.run),
		authorization+"/"+fmt.Sprint(id)+"/0"; got != want {
		t.Fatalf("merge authorization = %s, want %s", got, want)
	}
	if wiki := facts(); wiki.ApplyRun != authorization || wiki.ApplyEnded || wiki.Repair != "" {
		t.Fatalf("wiki records beside the active merge = %+v, want the merge authorization %s", wiki, authorization)
	}

	// A failure report of an execution of a wiki update names no allowance.
	failed := stFail(t, f.db, authorization, false)
	retry, _ := failed["retry_after"].(string)
	if want := map[string]any{"run_id": authorization, "wiki_update": u.run, "charged": false, "retry_after": retry}; retry == "" || !reflect.DeepEqual(failed, want) {
		t.Fatalf("failure report of the merge = %v, want %v with a retry time", failed, want)
	}
	if wiki := facts(); wiki.ApplyRun != authorization || !wiki.ApplyEnded || wiki.ApplyStopPending || wiki.Repair != "" {
		t.Fatalf("wiki records after the merge with no result = %+v, want the ended merge authorization with no repair", wiki)
	}

	// A second unfinished merge authorization for culler is a start race.
	var race *executionStartRace
	if err := u.startError(dispatch.OperationApply, ""); !errors.As(err, &race) || !strings.Contains(err.Error(), "merge_operations.project") {
		t.Fatalf("second merge authorization = %v, want a start race of the merge authorization", err)
	}

	// Receipt recovery: the merge authorization of the ended merge.
	if err := u.startError(dispatch.OperationReconcile, second); !wcRefused(err, "", "The wiki update has no merge that waits for receipt recovery.") {
		t.Fatalf("receipt recovery of a merge candidate step = %v, want a refusal", err)
	}
	reconcile := u.start(dispatch.OperationReconcile, authorization)
	recoveryFields := map[string]any{"submitted_commit": u.commit, "previous_run": authorization,
		"application": repository.Application{Authorization: authorization, Candidate: candidate}}
	check(reconcile, "reconcile", authorization, recoveryFields)
	// The merge did not run. Receipt recovery returns the live commit of culler.
	if recovered := f.nativeExecution(reconcile); recovered.Status != "not-applied" || recovered.DestinationCommit != u.startCommit ||
		recovered.WikiUpdate != u.run || recovered.TaskID != 0 || recovered.Applied != nil {
		t.Fatalf("receipt recovery of a merge that did not run = %+v, want not-applied at %s", recovered, u.startCommit)
	}
	// A receipt recovery with no result gives the repair text of its end condition.
	stFail(t, f.db, reconcile["run_id"].(string), false)
	if repair := facts().Repair; repair != "The last check ended with no result. The repository operation failed." {
		t.Fatalf("repair text after a receipt recovery with no result = %q", repair)
	}
	// The repair text names the condition of the last receipt recovery.
	again := check(u.start(dispatch.OperationReconcile, authorization), "reconcile", authorization, recoveryFields)
	if ended := wmRestartResident(t, f.db); !reflect.DeepEqual(ended, []string{again}) {
		t.Fatalf("the resident restart ended %v, want the receipt recovery %s", ended, again)
	}
	if repair := facts().Repair; repair != "The last check ended with no result. A resident restart ended the repository operation." {
		t.Fatalf("repair text after a second receipt recovery with no result = %q", repair)
	}
	// The repair that a result names stays the repair text after a later
	// receipt recovery with no result.
	const refusal = "Culler cannot write the file .git/index.lock in the Project repository. Make sure that your user can write the .git folder."
	third := check(u.start(dispatch.OperationReconcile, authorization), "reconcile", authorization, recoveryFields)
	u.record("reconcile", third, RepositoryResult{Status: "operator-attention", OperatorAction: refusal})
	stFail(t, f.db, check(u.start(dispatch.OperationReconcile, authorization), "reconcile", authorization, recoveryFields), false)
	if repair := facts().Repair; repair != refusal {
		t.Fatalf("repair text after a result that named a repair = %q, want %q", repair, refusal)
	}
	// A result that the merge did not run finishes the merge authorization and
	// ends the repair text. The candidate that the authorization names is not
	// the current candidate. A new commit on culler keeps the wiki update active.
	f.commitMain("independent.txt", "another accepted contribution\n")
	last := u.start(dispatch.OperationReconcile, authorization)
	check(last, "reconcile", authorization, recoveryFields)
	f.runExecution(last)
	if wiki := facts(); wiki.ApplyRun != "" || wiki.ApplyEnded || wiki.Repair != "" || wiki.Candidate || !wiki.Captured {
		t.Fatalf("wiki records after the finished merge authorization = %+v, want no merge authorization, no repair, and no candidate", wiki)
	}
	if err := u.startError(dispatch.OperationApply, ""); !wcRefused(err, "", "The wiki update has no merge candidate.") {
		t.Fatalf("merge with a candidate that a finished merge names = %v, want a refusal", err)
	}
	if err := u.startError(dispatch.OperationReconcile, authorization); !wcRefused(err, "", "The wiki update has no merge that waits for receipt recovery.") {
		t.Fatalf("receipt recovery of a finished merge authorization = %v, want a refusal", err)
	}

	// Cleanup: the repository of the Scribe run.
	cleanup := u.start(dispatch.OperationCleanup, u.run)
	check(cleanup, "cleanup", u.run, map[string]any{"producer_run": u.run})

	// A result names one task or one wiki update.
	for name, result := range map[string]RepositoryResult{"no subject": {}, "two subjects": {TaskID: 1, WikiUpdate: u.run}} {
		var usage *UsageError
		if _, err := RepositoryRecord(f.db, nil, "cleanup", cleanup["run_id"].(string), result); !errors.As(err, &usage) ||
			usage.Msg != "the repository result requires its task or its wiki update" {
			t.Fatalf("result with %s = %v, want a usage error", name, err)
		}
	}
	for _, operation := range []string{"observe"} {
		if _, err := RepositoryRecord(f.db, nil, operation, cleanup["run_id"].(string), RepositoryResult{WikiUpdate: u.run}); !wcRefused(err, "", "the repository operation is not valid for a wiki update") {
			t.Fatalf("%s result of a wiki update = %v, want a refusal", operation, err)
		}
	}
}

// The repository record of a wiki update accepts the Scribe run at the start
// commit, and a merge candidate step at the wiki commit.
func TestWikiRepositoryRecord(t *testing.T) {
	db := dvSpine(t)
	run := wrNewRun(t)
	wrFresh(t, db, "ws-test", run, wrCommitA)
	// Another Project has an active wiki update at the same start commit.
	dvAddProject(t, db, "ws-merge", "active")
	other := wrNewRun(t)
	wrFresh(t, db, "ws-merge", other, wrCommitA)
	// A Worker run of a task is active in the Project of the wiki update.
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	if err := inTx(db, func(ctx context.Context, q Q) error {
		task := int64(1)
		_, err := domain.Claim(ctx, q, time.Now(), domain.ClaimArgs{RunID: "task-run", Project: "ws-test", Owner: "worker", SubjectID: &task,
			SessionPath: "sessions/task-run", Binding: "fake", HardDeadlineMinutes: 60})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	tasks := wrDump(t, db, `SELECT * FROM tasks`)
	rows := func() string {
		t.Helper()
		return wrDump(t, db, `SELECT run_id,task_id,wiki_update,input_commit,destination_commit,version_id,temporary_bytes,captured,cleaned
			FROM repository_runs ORDER BY rowid`)
	}
	row := func(id, input, destination string, bytes, captured int) string {
		return fmt.Sprintf("run_id=%s|task_id=<nil>|wiki_update=%s|input_commit=%s|destination_commit=%s|version_id=<nil>|temporary_bytes=%d|captured=%d|cleaned=0|\n",
			id, run, input, destination, bytes, captured)
	}
	refused := func(id string, result RepositoryResult, text string) {
		t.Helper()
		before := rows()
		if _, err := RepositoryRecord(db, nil, "ready", id, result); !wcRefused(err, "", text) {
			t.Fatalf("repository record %+v of %s = %v, want the refusal %q", result, id, err, text)
		}
		if after := rows(); after != before {
			t.Fatalf("a refused repository record changed the rows:\n%swant\n%s", after, before)
		}
	}

	// The Scribe run: the wiki update of the run, at the start commit.
	ready := RepositoryResult{WikiUpdate: run, InputCommit: wrCommitA, DestinationCommit: wrCommitC, TemporaryBytes: 10}
	refused(run, RepositoryResult{TaskID: 1, InputCommit: wrCommitA, DestinationCommit: wrCommitC}, "the repository requires its assigned run")
	refused(run, RepositoryResult{WikiUpdate: other, InputCommit: wrCommitA, DestinationCommit: wrCommitC}, "the repository requires its assigned run")
	refused("task-run", ready, "the repository requires its assigned run")
	refused(run, RepositoryResult{WikiUpdate: run, InputCommit: wrCommitB, DestinationCommit: wrCommitC}, "the run repository changed its assigned input commit")
	refused(run, RepositoryResult{WikiUpdate: run, InputCommit: wrCommitA, DestinationCommit: "culler"}, "the repository requires exact input commits")
	response, err := RepositoryRecord(db, nil, "ready", run, ready)
	if want := map[string]any{"wiki_update": run, "recorded": true}; err != nil || !reflect.DeepEqual(response, want) {
		t.Fatalf("repository record of the Scribe run = %v, %v; want %v", response, err, want)
	}
	if got, want := rows(), row(run, wrCommitA, wrCommitC, 10, 0); got != want {
		t.Fatalf("repository of the Scribe run = %swant %s", got, want)
	}
	// The second record of the resident updates the size only.
	ready.TemporaryBytes = 20
	if _, err := RepositoryRecord(db, nil, "ready", run, ready); err != nil {
		t.Fatalf("second repository record of the Scribe run: %v", err)
	}
	if got, want := rows(), row(run, wrCommitA, wrCommitC, 20, 0); got != want {
		t.Fatalf("repository of the Scribe run after the second record = %swant %s", got, want)
	}
	refused(run, RepositoryResult{WikiUpdate: run, InputCommit: wrCommitA, DestinationCommit: wrCommitB}, "the repository allocation identifies another input")
	if got := wrDump(t, db, `SELECT * FROM tasks`); got != tasks {
		t.Fatalf("the repository record of a wiki update wrote a task row:\n%s", got)
	}

	// A merge candidate step: the wiki update of its execution, at the wiki commit.
	wrEndRun(t, db, run, domain.WikiRunEnd{Outcome: "completed", WikiCommit: wrCommitB})
	operation := dispatch.RepositoryOperation{Project: "ws-test", WikiUpdate: run, RunID: run}
	operation.Operation = dispatch.OperationCapture
	capture := startOperation(t, db, operation)["run_id"].(string)
	refused(capture, RepositoryResult{WikiUpdate: run, InputCommit: wrCommitB, DestinationCommit: wrCommitC}, "the repository requires its assigned run")
	operation.Operation, operation.RunID = dispatch.OperationPrepare, ""
	step := startOperation(t, db, operation)["run_id"].(string)
	refused(step, RepositoryResult{TaskID: 1, InputCommit: wrCommitB, DestinationCommit: wrCommitC}, "the repository requires its assigned run")
	refused(step, RepositoryResult{WikiUpdate: other, InputCommit: wrCommitB, DestinationCommit: wrCommitC}, "the repository requires its assigned run")
	refused(step, RepositoryResult{WikiUpdate: run, InputCommit: wrCommitA, DestinationCommit: wrCommitC}, "the run repository changed its assigned input commit")
	if _, err := RepositoryRecord(db, nil, "ready", step, RepositoryResult{WikiUpdate: run, InputCommit: wrCommitB, DestinationCommit: wrCommitC, TemporaryBytes: 30}); err != nil {
		t.Fatalf("repository record of the merge candidate step: %v", err)
	}
	if got, want := rows(), row(run, wrCommitA, wrCommitC, 20, 0)+row(step, wrCommitB, wrCommitC, 30, 1); got != want {
		t.Fatalf("repositories of the wiki update = %swant %s", got, want)
	}

	// A wiki commit with no repository record of its Scribe run gets no capture.
	wrEndRun(t, db, other, domain.WikiRunEnd{Outcome: "completed", WikiCommit: wrCommitB})
	var wiki dispatch.WikiMergeRecords
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		wiki, err = loadWikiMergeRecords(ctx, q, "ws-merge")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if wiki.Update == nil || wiki.Update.RunID != other || wiki.Update.CaptureDue || wiki.Update.Captured || len(wiki.Executions)+len(wiki.Cleanups) != 0 {
		t.Fatalf("wiki records with no repository record = %+v with the cleanups %+v, want the wiki update %s with no capture", wiki.Update, wiki.Cleanups, other)
	}
}

// A failed start of an operation of a wiki update names the wiki update in
// the start response and records a failed execution with no charge. A start
// race records no execution.
func TestStartFailureNamesTheWikiUpdate(t *testing.T) {
	f := newDeliveryFixture(t)
	u := f.wikiUpdate()
	u.submit()
	u.capture()
	step := u.start(dispatch.OperationPrepare, "")
	u.storeCandidate(step)
	stFail(t, f.db, u.start(dispatch.OperationApply, "")["run_id"].(string), false)
	rows := func() string {
		t.Helper()
		return wrDump(t, f.db, `SELECT run_id FROM repository_executions ORDER BY id`) + wrDump(t, f.db, `SELECT run_id,finished FROM merge_operations ORDER BY rowid`) + u.row()
	}

	// A second unfinished merge authorization for culler is a start race.
	before := rows()
	race := wmFailedStart(t, f.db, u.operation(dispatch.OperationApply, ""))
	if want := (StartFailure{Project: "ws-test", WikiUpdate: u.run, Operation: "apply", Code: race.Code, Message: race.Message}); race != want ||
		!strings.Contains(race.Message, "merge_operations.project") {
		t.Fatalf("start race = %+v, want a failure of the wiki update with no recorded execution", race)
	}
	if after := rows(); after != before {
		t.Fatalf("the start race changed the records:\n%swant\n%s", after, before)
	}

	// A receipt recovery of a run that holds no merge authorization fails at
	// its start. The failure is a failed execution of the wiki update.
	const text = "The wiki update has no merge that waits for receipt recovery."
	failed := wmFailedStart(t, f.db, u.operation(dispatch.OperationReconcile, step["run_id"].(string)))
	if want := (StartFailure{Project: "ws-test", WikiUpdate: u.run, Operation: "reconcile", Recorded: true, RunID: failed.RunID, Code: "domain-rejection", Message: text}); failed != want ||
		!validLowercaseHex(failed.RunID, 16) {
		t.Fatalf("failed start = %+v, want %+v", failed, want)
	}
	body, err := json.Marshal(failed)
	if want := `{"project":"ws-test","wiki_update":"` + u.run + `","operation":"reconcile","recorded":true,"run_id":"` + failed.RunID + `","code":"domain-rejection","message":"` + text + `"}`; err != nil || string(body) != want {
		t.Fatalf("failure of the start response = %s, %v; want %s", body, err, want)
	}
	if got, want := wmExecution(t, f.db, failed.RunID), "operation=reconcile|target="+step["run_id"].(string)+"|task_id=<nil>|wiki_update="+u.run+
		"|end_reason=failure|charged=0|retry=1|end_condition="+text+"|\n"; got != want {
		t.Fatalf("failed execution = %swant %s", got, want)
	}
	if got, want := executionEffect(t, f.db, failed.RunID), (map[string]any{"run_id": failed.RunID, "operation": "reconcile", "wiki_update": u.run, "project": "ws-test"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("effect of the failed execution = %v, want %v", got, want)
	}
	if got := wrDump(t, f.db, `SELECT ended_at,result FROM wiki_updates WHERE run_id=?`, u.run); got != "ended_at=<nil>|result=<nil>|\n" {
		t.Fatalf("wiki update after the failed start of receipt recovery = %s, want an active wiki update", got)
	}
}

// The temporary repository of the Scribe run stays until the capture of the
// wiki commit. The capture record accepts only the commits of the wiki update.
func TestWikiCaptureKeepsTheRepositoryUntilCapture(t *testing.T) {
	f := newDeliveryFixture(t)
	u := f.wikiUpdate()
	root := f.wikiRoot(u.run)
	u.submit()
	if wiki := f.wikiRecords(); wiki.Update == nil || wiki.Update.RunID != u.run || !wiki.Update.CaptureDue || wiki.Update.Captured || len(wiki.Cleanups) != 0 {
		t.Fatalf("wiki records before the capture = %+v with the cleanups %+v, want a due capture and no cleanup", wiki.Update, wiki.Cleanups)
	}

	// The record of a cleanup before the capture changes no row.
	early := u.start(dispatch.OperationCleanup, u.run)["run_id"].(string)
	if _, err := RepositoryRecord(f.db, nil, "cleanup", early, RepositoryResult{WikiUpdate: u.run}); !wcRefused(err, "", "the cleanup result does not identify a repository that meets the cleanup conditions") {
		t.Fatalf("cleanup record before the capture = %v, want a refusal", err)
	}
	stFail(t, f.db, early, false)
	if got := wrDump(t, f.db, `SELECT captured,cleaned FROM repository_runs WHERE run_id=?`, u.run); got != "captured=0|cleaned=0|\n" {
		t.Fatalf("repository of the Scribe run before the capture = %s", got)
	}

	capture := u.start(dispatch.OperationCapture, u.run)
	run := capture["run_id"].(string)
	result := f.nativeExecution(capture)
	if want := (RepositoryResult{WikiUpdate: u.run, InputCommit: u.startCommit, ResultCommit: u.commit, RetainedRef: "refs/mc/wiki/commits/" + u.run}); !reflect.DeepEqual(result, want) {
		t.Fatalf("capture result = %+v, want %+v", result, want)
	}
	// A result with another commit or another reference is not the capture of the wiki commit.
	otherCommit, otherRef := result, result
	otherCommit.ResultCommit = u.startCommit
	otherRef.RetainedRef = "refs/mc/tasks/1/versions/" + u.run
	for name, changed := range map[string]RepositoryResult{"another commit": otherCommit, "another reference": otherRef} {
		if _, err := RepositoryRecord(f.db, nil, "capture", run, changed); !wcRefused(err, "", "the capture result changed the wiki commit") {
			t.Fatalf("capture record with %s = %v, want a refusal", name, err)
		}
	}
	response, err := RepositoryRecord(f.db, nil, "capture", run, result)
	if want := map[string]any{"wiki_update": u.run, "recorded": true}; err != nil || !reflect.DeepEqual(response, want) {
		t.Fatalf("capture record = %v, %v; want %v", response, err, want)
	}
	// The same record repeats with no effect. Each other record of the ended capture is stale.
	if _, err := RepositoryRecord(f.db, nil, "capture", run, result); err != nil {
		t.Fatalf("repeated capture record: %v", err)
	}
	if _, err := RepositoryRecord(f.db, nil, "capture", run, otherCommit); !rnStale(err) {
		t.Fatalf("changed record of an ended capture = %v, want a stale run refusal", err)
	}
	if got, want := wmExecution(t, f.db, run), "operation=capture|target="+u.run+"|task_id=<nil>|wiki_update="+u.run+
		"|end_reason=result|charged=0|retry=0|end_condition=<nil>|\n"; got != want {
		t.Fatalf("capture execution = %swant %s", got, want)
	}
	if got := srcGit(t, f.workspace, "rev-parse", "refs/mc/wiki/commits/"+u.run); got != u.commit {
		t.Fatalf("the reference of the wiki commit holds %s, want %s", got, u.commit)
	}
	if n := dfInt(t, f.db, `SELECT (SELECT COUNT(*) FROM version_submissions)+(SELECT COUNT(*) FROM task_versions)+(SELECT COUNT(*) FROM repository_transfers)`); n != 0 {
		t.Fatalf("the capture of a wiki commit wrote %d task rows, want none", n)
	}

	// The captured repository meets the cleanup conditions.
	wiki := f.wikiRecords()
	if wiki.Update.CaptureDue || !wiki.Update.Captured || !reflect.DeepEqual(wiki.Cleanups, []dispatch.WikiCleanup{{WikiUpdate: u.run, RunID: u.run}}) {
		t.Fatalf("wiki records after the capture = %+v with the cleanups %+v, want the cleanup of %s", wiki.Update, wiki.Cleanups, u.run)
	}
	if _, err := os.Stat(filepath.Join(root, "repo")); err != nil {
		t.Fatalf("the repository of the Scribe run before its cleanup: %v", err)
	}
	f.runExecution(u.start(dispatch.OperationCleanup, u.run))
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("the cleanup kept the repository of the Scribe run: %v", err)
	}
	if got := wrDump(t, f.db, `SELECT captured,cleaned,cleanup_error FROM repository_runs WHERE run_id=?`, u.run); got != "captured=1|cleaned=1|cleanup_error=<nil>|\n" {
		t.Fatalf("repository of the Scribe run after its cleanup = %s", got)
	}
	if wiki := f.wikiRecords(); len(wiki.Cleanups) != 0 {
		t.Fatalf("cleanups after the cleanup = %+v, want none", wiki.Cleanups)
	}
	// Each reference of the wiki update stays in the Project repository.
	if got, want := f.mcRefs(), "refs/mc/wiki/commits/"+u.run+" "+u.commit+"\nrefs/mc/wiki/inputs/"+u.run+" "+u.startCommit; got != want {
		t.Fatalf("references after the cleanup =\n%s\nwant\n%s", got, want)
	}
}

// A wiki update with an end needs no capture and no prepare recovery. Each
// temporary repository of it meets the cleanup conditions.
func TestWikiCleanupAfterAFailedWikiUpdate(t *testing.T) {
	t.Run("before the capture", func(t *testing.T) {
		f := newDeliveryFixture(t)
		u := f.wikiUpdate()
		u.submit()
		f.archive()
		if got := wrDump(t, f.db, `SELECT result,cause FROM wiki_updates WHERE run_id=?`, u.run); got != "result=failed|cause=The Project is archived.|\n" {
			t.Fatalf("wiki update of the archived Project = %s, want a failed end", got)
		}
		if wiki := f.wikiRecords(); wiki.Update != nil || !reflect.DeepEqual(wiki.Cleanups, []dispatch.WikiCleanup{{WikiUpdate: u.run, RunID: u.run}}) {
			t.Fatalf("wiki records after the failed end = %+v with the cleanups %+v, want no capture and the cleanup of %s", wiki.Update, wiki.Cleanups, u.run)
		}
		if err := u.startError(dispatch.OperationCapture, u.run); !wcRefused(err, "", "Culler found no active wiki update.") {
			t.Fatalf("capture of a failed wiki update = %v, want a refusal", err)
		}
		f.runExecution(u.start(dispatch.OperationCleanup, u.run))
		if _, err := os.Stat(f.wikiRoot(u.run)); !os.IsNotExist(err) {
			t.Fatalf("the cleanup kept the repository of the Scribe run: %v", err)
		}
		if cleaned := dfInt(t, f.db, `SELECT cleaned FROM repository_runs WHERE run_id=?`, u.run); cleaned != 1 {
			t.Fatal("the cleanup record left the repository of the Scribe run open")
		}
	})

	t.Run("with an interrupted merge candidate step", func(t *testing.T) {
		f := newDeliveryFixture(t)
		u := f.wikiUpdate()
		u.submit()
		u.capture()
		// The step built its repository. The resident restart interrupts it before its record.
		effect := u.start(dispatch.OperationPrepare, "")
		step := effect["run_id"].(string)
		f.nativeExecution(effect)
		wmRestartResident(t, f.db)
		// The active wiki update keeps the repository of the step for the prepare recovery.
		wiki := f.wikiRecords()
		if wiki.Update.PrepareRun != step || !reflect.DeepEqual(wiki.Cleanups, []dispatch.WikiCleanup{{WikiUpdate: u.run, RunID: u.run}}) {
			t.Fatalf("wiki records after the interruption = %+v with the cleanups %+v, want the prepare recovery of %s and the cleanup of %s", wiki.Update, wiki.Cleanups, step, u.run)
		}
		early := u.start(dispatch.OperationCleanup, step)["run_id"].(string)
		if _, err := RepositoryRecord(f.db, nil, "cleanup", early, RepositoryResult{WikiUpdate: u.run}); !wcRefused(err, "", "the cleanup result does not identify a repository that meets the cleanup conditions") {
			t.Fatalf("cleanup record of a repository that waits for the prepare recovery = %v, want a refusal", err)
		}
		stFail(t, f.db, early, false)

		f.archive()
		ended := u.row()
		if wiki := f.wikiRecords(); wiki.Update != nil || !reflect.DeepEqual(wiki.Cleanups,
			[]dispatch.WikiCleanup{{WikiUpdate: u.run, RunID: u.run}, {WikiUpdate: u.run, RunID: step}}) {
			t.Fatalf("wiki records after the failed end = %+v with the cleanups %+v, want both repositories", wiki.Update, wiki.Cleanups)
		}

		// A cleanup has no evaluation capture. A failed cleanup gets a retry
		// time and changes no column of the wiki update.
		failed := u.start(dispatch.OperationCleanup, step)["run_id"].(string)
		if _, err := RepositoryRecord(f.db, nil, "cleanup", failed, RepositoryResult{WikiUpdate: u.run, EvaluationPending: true}); !wcRefused(err, "", "The cleanup has no incomplete evaluation capture.") {
			t.Fatalf("cleanup record with an evaluation capture = %v, want a refusal", err)
		}
		if _, err := RepositoryRecord(f.db, nil, "cleanup", failed, RepositoryResult{TaskID: 1}); !wcRefused(err, "", "the cleanup result does not identify its repository") {
			t.Fatalf("cleanup record of a task for a repository of a wiki update = %v, want a refusal", err)
		}
		f.record("cleanup", failed, RepositoryResult{WikiUpdate: u.run, CleanupError: "The folder is busy.", TemporaryBytes: 12})
		if got, want := wmExecution(t, f.db, failed), "operation=cleanup|target="+step+"|task_id=<nil>|wiki_update="+u.run+
			"|end_reason=failure|charged=0|retry=1|end_condition=The folder is busy.|\n"; got != want {
			t.Fatalf("failed cleanup = %swant %s", got, want)
		}
		if got := wrDump(t, f.db, `SELECT cleaned,cleanup_error,temporary_bytes FROM repository_runs WHERE run_id=?`, step); got != "cleaned=0|cleanup_error=The folder is busy.|temporary_bytes=12|\n" {
			t.Fatalf("repository of the step after the failed cleanup = %s", got)
		}
		if got := u.row(); got != ended {
			t.Fatalf("the failed cleanup changed the wiki update:\n%swant\n%s", got, ended)
		}
		// The interrupted step and the failed cleanup each left a retry time on their target.
		var retries []string
		for _, retry := range f.wikiRecords().Retries {
			retries = append(retries, string(retry.Operation)+" "+retry.Target)
		}
		if want := []string{"prepare wiki/" + u.run, "cleanup " + step}; !reflect.DeepEqual(retries, want) {
			t.Fatalf("retry times after the failed cleanup = %v, want %v", retries, want)
		}

		for _, run := range []string{u.run, step} {
			f.runExecution(u.start(dispatch.OperationCleanup, run))
			if _, err := os.Stat(f.wikiRoot(run)); !os.IsNotExist(err) {
				t.Fatalf("the cleanup kept the repository of %s: %v", run, err)
			}
		}
		if got := wrDump(t, f.db, `SELECT cleaned,cleanup_error FROM repository_runs WHERE wiki_update=? ORDER BY run_id=? DESC`, u.run, u.run); got !=
			"cleaned=1|cleanup_error=<nil>|\ncleaned=1|cleanup_error=<nil>|\n" {
			t.Fatalf("repositories of the wiki update after the cleanups = %s", got)
		}
		if wiki := f.wikiRecords(); len(wiki.Cleanups) != 0 {
			t.Fatalf("cleanups after the cleanups = %+v, want none", wiki.Cleanups)
		}
		if got := u.row(); got != ended {
			t.Fatalf("the cleanups changed the wiki update:\n%swant\n%s", got, ended)
		}
	})
}

// Each read of a task returns no row of a wiki update. The reads of a task
// are equal with and without one wiki row in each operation table, and each
// task action succeeds beside those rows.
func TestTaskReadsReturnNoWikiRow(t *testing.T) {
	f := newDeliveryFixture(t)
	closed := f.add("Close the cancelled change")
	dvExec(t, f.db, `UPDATE tasks SET decision='cancelled',decided_at=datetime('now'),archived=1 WHERE id=?`, closed)
	review := f.add("Deliver the source change")
	f.pass(review, "feature.txt", "complete feature\n")
	f.merge(review, "prepare")
	packager := dfStr(t, f.db, `SELECT id FROM runs WHERE subject=? AND role='packager'`, review)
	reads := func() string {
		t.Helper()
		var out []any
		for _, read := range []func() (any, error){
			func() (any, error) { return PacketList(f.db, PacketSelection{Live: true}) },
			func() (any, error) { return PacketList(f.db, PacketSelection{}) },
			func() (any, error) { return TaskGet(f.db, review) },
			func() (any, error) { return EvaluationRecords(f.db, packager) },
		} {
			value, err := read()
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, value)
		}
		body, err := json.MarshalIndent(out, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	before := reads()

	// One wiki row in each operation table, with an active merge of the wiki commit.
	u := f.wikiUpdate()
	u.submit()
	u.capture()
	u.storeCandidate(u.start(dispatch.OperationPrepare, ""))
	merge := u.start(dispatch.OperationApply, "")["run_id"].(string)
	for _, table := range []string{"repository_runs", "repository_executions", "merge_operations", "merge_candidates"} {
		if n := dfInt(t, f.db, `SELECT COUNT(*) FROM `+table+` WHERE wiki_update IS NOT NULL`); n == 0 {
			t.Fatalf("the fixture holds no wiki row in %s", table)
		}
	}
	if after := reads(); after != before {
		t.Fatalf("the reads of a task changed with the wiki rows:\n%s\nwant\n%s", after, before)
	}

	// A task passes its Worker, Verifier, and Packager runs beside the wiki rows.
	f.pass(f.add("Deliver the second change"), "second.txt", "second feature\n")

	// A revision request waits for the capture of a submitted version. The
	// read names that wait. The active merge of the wiki commit holds no revision.
	third := f.add("Deliver the third change")
	worker, root, id := f.run(third, "worker")
	repo := filepath.Join(root, "repo")
	writeFile(t, filepath.Join(repo, "third.txt"), "third feature\n")
	srcGit(t, repo, "add", ".")
	srcGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "Complete the contribution")
	if _, err := Complete(f.db, id, CompleteArgs{Task: third, Run: worker, Status: "worked", Commit: srcGit(t, repo, "rev-parse", "HEAD")}); err != nil {
		t.Fatal(err)
	}
	if state := boardRevision(t, f.db, third, "The revised third change passes.")["revision_state"]; state != "pending" {
		t.Fatalf("revision state before the capture = %v, want pending", state)
	}
	record, err := TaskGet(f.db, third)
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := record.(map[string]any)["pending_revision"].(map[string]any)
	if pending["wait_reason"] != "Culler must save the current result before the Worker starts the revision." {
		t.Fatalf("wait reason of the revision = %v, want the capture of the run result", pending["wait_reason"])
	}
	f.captureSubmission(third, worker)
	f.transaction(func(ctx context.Context, q Q) error { return domain.ApplyPendingTaskRevisions(ctx, q) })
	if state := dfStr(t, f.db, `SELECT state FROM requirements_revisions WHERE task_id=?`, third); state != "applied" {
		t.Fatalf("revision state beside the active merge of the wiki commit = %s, want applied", state)
	}

	// The wiki rows hold no artifact. The artifacts of the closed task expire
	// after the retention period, when no execution of the Project is active.
	stFail(t, f.db, merge, false)
	f.cleanup(third, worker, root)
	now := time.Now().UTC()
	if plan, err := ArtifactExpiryPlan(f.db, nil, now); err != nil || plan.TaskID != 0 {
		t.Fatalf("expiry plan inside the retention period = %+v, %v; want none", plan, err)
	}
	if plan, err := ArtifactExpiryPlan(f.db, nil, now.AddDate(0, 0, ArtifactRetentionDays+1)); err != nil || plan.TaskID != closed {
		t.Fatalf("expiry plan after the retention period = %+v, %v; want the expiry of task %d", plan, err, closed)
	}
}

// The wiki update of an archived Project stays active while an execution of
// it is active, and while its merge authorization is unfinished. The start
// request stores its wiki facts in both states.
func TestDeliveryStartKeepsAnArchivedWikiUpdateWithAnUnfinishedMerge(t *testing.T) {
	// request sends one start request with an accepted commit observation. It
	// requires the stored observation and the active wiki update.
	request := func(t *testing.T, f *deliveryFixture, u *wmUpdate, observed string) {
		t.Helper()
		if _, err := DeliveryStart(f.db, nil, StartRequest{Owner: testOwner(t, f.db), AcceptedCommits: map[string]string{"ws-test": observed}}); err != nil {
			t.Fatalf("start request: %v", err)
		}
		if stored := wrStoredCommit(t, f.db, "ws-test"); stored != observed {
			t.Fatalf("stored accepted commit = %s, want the observation %s of the start request", stored, observed)
		}
		if got := wrDump(t, f.db, `SELECT ended_at,result,cause FROM wiki_updates WHERE run_id=?`, u.run); got != "ended_at=<nil>|result=<nil>|cause=<nil>|\n" {
			t.Fatalf("wiki update of the archived Project = %s, want an active wiki update", got)
		}
	}
	archive := func(t *testing.T, f *deliveryFixture) {
		t.Helper()
		if _, err := ProjectSetStatus(f.db, nil, "ws-test", "archived"); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("active execution", func(t *testing.T) {
		f := newDeliveryFixture(t)
		u := f.wikiUpdate()
		u.submit()
		capture := u.start(dispatch.OperationCapture, u.run)
		archive(t, f)
		request(t, f, u, wrCommitA)
		request(t, f, u, wrCommitB)

		// After the result of the capture, the next start request ends the wiki update.
		f.record("capture", capture["run_id"].(string), f.nativeExecution(capture))
		if _, err := DeliveryStart(f.db, nil, StartRequest{Owner: testOwner(t, f.db)}); err != nil {
			t.Fatalf("start request after the capture: %v", err)
		}
		if got := wrDump(t, f.db, `SELECT result,cause FROM wiki_updates WHERE run_id=?`, u.run); got != "result=failed|cause=The Project is archived.|\n" {
			t.Fatalf("wiki update after the capture = %s, want a failed end", got)
		}
	})

	t.Run("unfinished merge authorization with an ended execution", func(t *testing.T) {
		f := newDeliveryFixture(t)
		u := f.wikiUpdate()
		u.submit()
		u.capture()
		u.storeCandidate(u.start(dispatch.OperationPrepare, ""))
		merge := u.start(dispatch.OperationApply, "")["run_id"].(string)
		// A resident restart ends the merge with no result. The new owner did
		// not confirm the stop of the merge.
		if ended := wmRestartResident(t, f.db); !reflect.DeepEqual(ended, []string{merge}) {
			t.Fatalf("the resident restart ended %v, want the merge %s", ended, merge)
		}
		if wiki := f.wikiRecords(); wiki.Update.ApplyRun != merge || !wiki.Update.ApplyEnded || !wiki.Update.ApplyStopPending || len(wiki.Executions) != 0 {
			t.Fatalf("wiki records after the merge with no result = %+v with the executions %+v, want the ended merge authorization %s with no stop confirmation",
				wiki.Update, wiki.Executions, merge)
		}
		archive(t, f)
		request(t, f, u, wrCommitA)
		request(t, f, u, wrCommitB)
		if finished := dfInt(t, f.db, `SELECT finished FROM merge_operations WHERE run_id=?`, merge); finished != 0 {
			t.Fatal("a start request finished the merge authorization of the archived Project")
		}
	})
}

func TestDecideReadsOnlyTheReceiptRecoveryOfAWikiCommit(t *testing.T) {
	for _, test := range []struct {
		name    string
		setup   func(*testing.T, *sql.DB)
		kind    dispatch.Kind
		held    bool          // the agent run hold
		role    dispatch.Role // the agent run that the tick starts under the fact
		subject int64         // the task of that agent run, or 0
	}{
		{"a merge candidate step holds the agent runs", func(t *testing.T, db *sql.DB) {
			wmTask(t, db)
			dvExec(t, db, `UPDATE tasks SET candidate_id=NULL WHERE id=2`)
		}, dispatch.KindIdle, true, dispatch.RoleStrategist, 0},
		{"a seeded task gets an agent run", func(t *testing.T, db *sql.DB) {
			stTask(t, db, 1, "ws-test", dispatch.StatusSeeded)
		}, dispatch.KindSpawn, false, dispatch.RoleWorker, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := dvSpine(t)
			fixClocks(t, db)
			test.setup(t, db)
			testOwner(t, db)
			before := wmDecide(t, db)
			if before.action.Kind != test.kind || before.hold != test.held || before.rec.WikiReceiptRecovery {
				t.Fatalf("tick results of the fixture = %s, hold %v, receipt recovery fact %v; want %s, %v, and no fact",
					before.action.Kind, before.hold, before.rec.WikiReceiptRecovery, test.kind, test.held)
			}
			same := func(state string) {
				t.Helper()
				if after := wmDecide(t, db); !reflect.DeepEqual(after, before) {
					t.Fatalf("%s: the tick results changed:\n%+v\nwant\n%+v", state, after, before)
				}
			}
			// recovery requires the tick results under the fact. The fact is the
			// only change of the task records.
			recovery := func(state string) {
				t.Helper()
				after := wmDecide(t, db)
				fact := after.rec.WikiReceiptRecovery
				after.rec.WikiReceiptRecovery = false
				if !fact || !reflect.DeepEqual(after.rec, before.rec) || !reflect.DeepEqual(after.lease, before.lease) {
					t.Fatalf("%s: receipt recovery fact %v with the task records\n%+v\nwant the fact and no other change of\n%+v", state, fact, after.rec, before.rec)
				}
				var subject int64
				spawn := after.action.Spawn
				if spawn != nil && spawn.SubjectID != nil {
					subject = *spawn.SubjectID
				}
				if after.hold || after.action.Kind != dispatch.KindSpawn || spawn.Role != test.role || subject != test.subject {
					t.Fatalf("%s: tick results = %+v with the spawn %+v, hold %v; want a spawn with the role %s and the task %d and no agent run hold",
						state, after.action, spawn, after.hold, test.role, test.subject)
				}
			}

			u := wmFixtureUpdate(t, db)
			same("an active Scribe run")
			u.submit()
			same("a wiki commit that waits for its capture")
			capture := u.startRun(dispatch.OperationCapture, u.run)
			same("an active capture")
			if ended := wmRestartResident(t, db); !reflect.DeepEqual(ended, []string{capture}) {
				t.Fatalf("the resident restart ended %v, want the capture %s", ended, capture)
			}
			same("an interrupted capture")
			u.capture()
			same("a captured wiki commit")
			cleanup := u.startRun(dispatch.OperationCleanup, u.run)
			same("an active cleanup")
			u.record("cleanup", cleanup, RepositoryResult{CleanupError: "The folder is busy."})
			same("a failed cleanup")
			u.clean(u.run)
			step := u.startRun(dispatch.OperationPrepare, "")
			same("an active merge candidate step")
			if ended := wmRestartResident(t, db); !reflect.DeepEqual(ended, []string{step}) {
				t.Fatalf("the resident restart ended %v, want the merge candidate step %s", ended, step)
			}
			same("an interrupted merge candidate step")
			recovered := u.startRun(dispatch.OperationPrepare, step)
			same("an active prepare recovery")
			u.record("prepare", recovered, RepositoryResult{Status: "destination-changed", DestinationCommit: wrCommitC})
			same("a wait for the destination quiet interval")
			candidate := u.storeCandidate(u.start(dispatch.OperationPrepare, ""))
			same("a merge candidate")
			merge := u.startRun(dispatch.OperationApply, "")
			same("an active merge")
			stFail(t, db, merge, false)
			recovery("a merge with no result")
			check := u.startRun(dispatch.OperationReconcile, merge)
			recovery("an active receipt recovery")
			u.record("reconcile", check, RepositoryResult{Status: "operator-attention",
				OperatorAction: "Culler cannot write the file .git/index.lock in the Project repository. Make sure that your user can write the .git folder."})
			recovery("a receipt recovery that names a repair")
			u.record("reconcile", u.startRun(dispatch.OperationReconcile, merge), u.applied(merge, candidate))
			if got, want := u.end(), wmSucceeded(wrCommitA, wrCommitC); got != want {
				t.Fatalf("wiki update after the merge = %swant %s", got, want)
			}
			same("a merged wiki commit")

			failed := wmFixtureUpdate(t, db)
			failed.submit()
			stFail(t, db, failed.startRun(dispatch.OperationCapture, failed.run), false)
			if got, want := failed.end(), wmFailed("Culler did not capture the wiki commit. The repository operation failed."); got != want {
				t.Fatalf("wiki update after the failed capture = %swant %s", got, want)
			}
			same("a failed wiki update")
		})
	}
}

// Receipt recovery of the merge of a wiki commit waits for the retry time of
// the merge. Each receipt recovery with no proof waits one more retry time.
func TestWikiReceiptRecoveryWaitsForRetryTime(t *testing.T) {
	db := dvSpine(t)
	clocks := fixClocks(t, db)
	u := wmFixtureUpdate(t, db)
	candidate := u.candidate()
	merge := u.startRun(dispatch.OperationApply, "")
	stFail(t, db, merge, false)
	// waits requires a start request that starts no operation and returns the wait.
	waits := func(step string, want time.Duration) {
		t.Helper()
		resp := wmStart(t, db, nil)
		if resp.DueInMS == nil || *resp.DueInMS != want.Milliseconds() {
			t.Fatalf("%s: the start response is due in %v ms, want %d", step, resp.DueInMS, want.Milliseconds())
		}
		if got := u.end(); got != wmActive {
			t.Fatalf("%s: wiki update = %swant an active wiki update", step, got)
		}
	}
	// recovery requires the start of one receipt recovery of the merge.
	recovery := func(step string) string {
		t.Helper()
		resp := wmStart(t, db, nil, "reconcile/wiki")
		if effect := executionEffect(t, db, resp.Started[0]); effect["previous_run"] != merge || resp.DueInMS != nil {
			t.Fatalf("%s: receipt recovery = %v, due in %v ms; want the recovery of the merge %s and no wait", step, effect, resp.DueInMS, merge)
		}
		return resp.Started[0]
	}

	waits("the merge with no result", domain.RetryDelay)
	clocks.advance(domain.RetryDelay - time.Second)
	waits("one second before the end of the retry time", time.Second)
	clocks.advance(time.Second)
	stFail(t, db, recovery("the end of the retry time"), false)
	waits("a receipt recovery with no result", domain.RetryDelay)
	clocks.advance(domain.RetryDelay)
	u.record("reconcile", recovery("the second receipt recovery"), RepositoryResult{Status: "operator-attention",
		OperatorAction: "Culler cannot write the file .git/index.lock in the Project repository. Make sure that your user can write the .git folder."})
	waits("a receipt recovery that names a repair", domain.RetryDelay)
	clocks.advance(domain.RetryDelay)
	u.record("reconcile", recovery("the third receipt recovery"), u.applied(merge, candidate))
	if got, want := u.end(), wmSucceeded(wrCommitA, wrCommitC); got != want {
		t.Fatalf("wiki update after the proven merge = %swant %s", got, want)
	}
	if resp := wmStart(t, db, nil); resp.DueInMS != nil {
		t.Fatalf("start response after the proven merge is due in %d ms, want no wait", *resp.DueInMS)
	}
}

// A new resident owner ends the merge of the earlier owner. Receipt recovery
// starts only after the new owner confirms the stop of that merge.
func TestWikiReceiptRecoveryWaitsForTheStopConfirmation(t *testing.T) {
	db := dvSpine(t)
	clocks := fixClocks(t, db)
	u := wmFixtureUpdate(t, db)
	u.candidate()
	merge := u.startRun(dispatch.OperationApply, "")
	earlier := testOwner(t, db)
	stRegister(t, db)

	// The merge container of the earlier owner can run until its deadline.
	clocks.advance(70 * time.Second)
	resp := wmStart(t, db, nil)
	if want := []ExecutionStop{{RunID: merge, Project: "ws-test", Operation: "apply", Owner: earlier,
		WaitMS: (dispatch.OperationApply.Deadline() - 70*time.Second).Milliseconds()}}; !reflect.DeepEqual(resp.Stops, want) {
		t.Fatalf("stops after the owner change = %+v, want %+v", resp.Stops, want)
	}
	if got, want := wmExecution(t, db, merge), "operation=apply|target=wiki/"+u.run+"|task_id=<nil>|wiki_update="+u.run+
		"|end_reason=owner-change|charged=0|retry=1|end_condition=A resident restart ended the repository operation.|\n"; got != want {
		t.Fatalf("merge after the owner change = %swant %s", got, want)
	}
	// The new owner cannot confirm the stop before the heartbeat limit of the earlier owner.
	if resp := wmStart(t, db, []string{merge}); len(resp.Confirmed) != 0 {
		t.Fatalf("stop confirmations before the heartbeat limit = %v, want none", resp.Confirmed)
	}
	// After the retry time of the merge, receipt recovery waits for the stop
	// confirmation. That wait has no end time.
	clocks.advance(domain.RetryDelay)
	if resp := wmStart(t, db, nil); resp.DueInMS != nil {
		t.Fatalf("start response before the stop confirmation is due in %d ms, want no end time", *resp.DueInMS)
	}
	if wiki := wmRecords(t, db).Update; wiki == nil || wiki.ApplyRun != merge || !wiki.ApplyEnded || !wiki.ApplyStopPending {
		t.Fatalf("wiki records before the stop confirmation = %+v, want the ended merge %s with no stop confirmation", wiki, merge)
	}
	if got := u.end(); got != wmActive {
		t.Fatalf("wiki update before the stop confirmation = %swant an active wiki update", got)
	}
	// The request that confirms the stop starts receipt recovery.
	resp = wmStart(t, db, []string{merge}, "reconcile/wiki")
	if !reflect.DeepEqual(resp.Confirmed, []string{merge}) || len(resp.Stops) != 0 {
		t.Fatalf("start response with the stop = confirmed %v, stops %+v; want the confirmation of %s", resp.Confirmed, resp.Stops, merge)
	}
}

// Receipt recovery of a merge of a wiki commit repeats while no result proves
// the merge. A repeat changes no task, no allowance, and no attention count.
func TestWikiReceiptRecoveryRepeatsWithNoTaskChange(t *testing.T) {
	db := dvSpine(t)
	wrPipelineFixture(t, db)
	clocks := fixClocks(t, db)
	before := wmPipelineFacts(t, db)
	u := wmFixtureUpdate(t, db)
	u.candidate()
	merge := u.startRun(dispatch.OperationApply, "")
	stFail(t, db, merge, false)
	for repeat := 1; repeat <= 20; repeat++ {
		clocks.advance(domain.RetryDelay)
		check := wmStart(t, db, nil, "reconcile/wiki").Started[0]
		if repeat%2 == 0 {
			u.record("reconcile", check, RepositoryResult{Status: "operator-attention",
				OperatorAction: "Culler cannot write the file .git/index.lock in the Project repository. Make sure that your user can write the .git folder."})
		} else {
			stFail(t, db, check, false)
		}
		if got := wmPipelineFacts(t, db); got != before {
			t.Fatalf("receipt recovery %d changed a pipeline fact:\n%s\nbefore:\n%s", repeat, got, before)
		}
		if got := u.end(); got != wmActive {
			t.Fatalf("wiki update after receipt recovery %d = %swant an active wiki update", repeat, got)
		}
	}
	if got := wrDump(t, db, `SELECT COUNT(*) AS checks,SUM(charged) AS charged FROM repository_executions WHERE operation='reconcile' AND target=?`, merge); got != "checks=20|charged=0|\n" {
		t.Fatalf("receipt recoveries of the merge = %s, want 20 with no charge", got)
	}
}

// A host restart ends an execution of a wiki update with no failure. The wiki
// update stays active, and the operation starts again after the retry time.
func TestHostRestartInTheMergePhaseKeepsTheWikiUpdate(t *testing.T) {
	db := dvSpine(t)
	clocks := fixClocks(t, db)
	u := wmFixtureUpdate(t, db)
	u.submit()
	boots := 0
	// restart changes the boot of the host and registers the resident of the
	// new boot. The next start request ends the execution with no failure and
	// starts no operation. The resident confirms the stop of an execution of
	// an earlier boot at once.
	restart := func(run, operation, target string) {
		t.Helper()
		boots++
		clocks.vm.BootID = fmt.Sprintf("boot-%d", boots)
		stRegister(t, db)
		wmStart(t, db, nil)
		if got, want := wmExecution(t, db, run), "operation="+operation+"|target="+target+"|task_id=<nil>|wiki_update="+u.run+
			"|end_reason=vm-restart|charged=0|retry=1|end_condition=The repository operation failed.|\n"; got != want {
			t.Fatalf("%s after the host restart = %swant %s", operation, got, want)
		}
		if got := u.end(); got != wmActive {
			t.Fatalf("wiki update after the host restart in the %s = %swant an active wiki update", operation, got)
		}
		if resp := wmStart(t, db, []string{run}); !reflect.DeepEqual(resp.Confirmed, []string{run}) {
			t.Fatalf("stop confirmations after the host restart = %v, want %s", resp.Confirmed, run)
		}
		clocks.advance(domain.RetryDelay)
	}

	restart(wmStart(t, db, nil, "capture/wiki").Started[0], "capture", u.run)
	u.record("capture", wmStart(t, db, nil, "capture/wiki").Started[0], RepositoryResult{InputCommit: u.startCommit, ResultCommit: u.commit,
		RetainedRef: "refs/mc/wiki/commits/" + u.run})
	resp := wmStart(t, db, nil, "prepare/wiki", "cleanup/wiki")
	step := resp.Started[0]
	u.record("cleanup", resp.Started[1], RepositoryResult{})
	restart(step, "prepare", "wiki/"+u.run)
	// The interrupted merge candidate step starts again as a prepare recovery.
	recovery := executionEffect(t, db, wmStart(t, db, nil, "prepare/wiki").Started[0])
	if recovery["previous_run"] != step {
		t.Fatalf("merge candidate step after the host restart = %v, want the prepare recovery of %s", recovery, step)
	}
	candidate := u.storeCandidate(recovery)
	merge := wmStart(t, db, nil, "apply/wiki").Started[0]
	restart(merge, "apply", "wiki/"+u.run)
	// The merge authorization stays unfinished. Receipt recovery decides the wiki update.
	restart(wmStart(t, db, nil, "reconcile/wiki").Started[0], "reconcile", merge)
	u.record("reconcile", wmStart(t, db, nil, "reconcile/wiki").Started[0], u.applied(merge, candidate))
	if got, want := u.end(), wmSucceeded(wrCommitA, wrCommitC); got != want {
		t.Fatalf("wiki update after the proven merge = %swant %s", got, want)
	}
	if n := dfInt(t, db, `SELECT COUNT(*) FROM infrastructure_failures`); n != 0 {
		t.Fatalf("the host restarts wrote %d failure records, want none", n)
	}
}

// A failed start of an operation of a wiki update records a failed execution
// with no charge. A failed start of the capture, of the merge candidate step,
// or of the merge ends the wiki update as failed.
func TestFailedStartOfAWikiOperation(t *testing.T) {
	const (
		noRepository = "Culler found no Project repository for the wiki update."
		noStep       = "The wiki update has no interrupted merge candidate step."
		noCandidate  = "The wiki update has no merge candidate."
		noMerge      = "The wiki update has no merge that waits for receipt recovery."
	)
	// A start of a capture and a start of a cleanup do not fail for the rows
	// that the selection reads. These cases record the failed start directly.
	for _, test := range []struct {
		name      string
		operation dispatch.Operation
		// setup takes the wiki update to the state of the case. It returns the
		// run that the operation names.
		setup     func(t *testing.T, db *sql.DB, u *wmUpdate) string
		direct    bool   // record the failed start with the condition
		condition string // the start error
		target    func(u *wmUpdate, run string) string
		end       string // the end of the wiki update
		// interrupted states that the failed start finishes the unfinished row
		// of the interrupted merge candidate step.
		interrupted bool
	}{
		{name: "capture", operation: dispatch.OperationCapture,
			setup:  func(_ *testing.T, _ *sql.DB, u *wmUpdate) string { u.submit(); return u.run },
			direct: true, condition: noRepository,
			end: wmFailed("Culler did not capture the wiki commit. " + noRepository)},
		{name: "cleanup", operation: dispatch.OperationCleanup,
			setup:  func(_ *testing.T, _ *sql.DB, u *wmUpdate) string { u.submit(); u.capture(); return u.run },
			direct: true, condition: noRepository, end: wmActive},
		{name: "merge candidate step", operation: dispatch.OperationPrepare,
			setup: func(t *testing.T, db *sql.DB, u *wmUpdate) string {
				u.submit()
				u.capture()
				// A resident restart interrupted a merge candidate step. Its row is unfinished.
				u.startRun(dispatch.OperationPrepare, "")
				wmRestartResident(t, db)
				return "0000000000000000"
			},
			condition: noStep, target: func(u *wmUpdate, _ string) string { return "wiki/" + u.run },
			end: wmFailed("Culler did not prepare the merge candidate of the wiki commit. " + noStep), interrupted: true},
		{name: "merge", operation: dispatch.OperationApply,
			setup:     func(_ *testing.T, _ *sql.DB, u *wmUpdate) string { u.submit(); u.capture(); return "" },
			condition: noCandidate, target: func(u *wmUpdate, _ string) string { return "wiki/" + u.run },
			end: wmFailed("Culler did not merge the wiki commit. " + noCandidate)},
		{name: "receipt recovery", operation: dispatch.OperationReconcile,
			setup: func(t *testing.T, db *sql.DB, u *wmUpdate) string {
				u.candidate()
				stFail(t, db, u.startRun(dispatch.OperationApply, ""), false)
				return "0000000000000000"
			},
			condition: noMerge, end: wmActive},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := dvSpine(t)
			u := wmFixtureUpdate(t, db)
			named := test.setup(t, db, u)
			op := u.operation(test.operation, named)
			const mergeRows = `SELECT run_id,finished FROM merge_operations WHERE operation='apply' ORDER BY rowid`
			const stepRows = `SELECT finished,result FROM merge_operations WHERE operation='prepare' ORDER BY rowid`
			merges, steps := wrDump(t, db, mergeRows), wrDump(t, db, stepRows)
			if test.interrupted {
				if steps != "finished=0|result=<nil>|\n" {
					t.Fatalf("rows of the merge candidate step before the failed start = %swant one unfinished row", steps)
				}
				steps = "finished=1|result={\"status\":\"interrupted\"}|\n"
			}
			var run string
			if test.direct {
				run = wmRecordFailedStart(t, db, op, test.condition)
			} else {
				failure := wmFailedStart(t, db, op)
				if want := (StartFailure{Project: "ws-test", WikiUpdate: u.run, Operation: string(test.operation), Recorded: true, RunID: failure.RunID,
					Code: "domain-rejection", Message: test.condition}); failure != want {
					t.Fatalf("failed start = %+v, want %+v", failure, want)
				}
				run = failure.RunID
			}
			target := named
			if test.target != nil {
				target = test.target(u, named)
			}
			if got, want := wmExecution(t, db, run), "operation="+string(test.operation)+"|target="+target+"|task_id=<nil>|wiki_update="+u.run+
				"|end_reason=failure|charged=0|retry=1|end_condition="+test.condition+"|\n"; got != want {
				t.Fatalf("failed execution = %swant %s", got, want)
			}
			if got := u.end(); got != test.end {
				t.Fatalf("wiki update after the failed start = %swant %s", got, test.end)
			}
			// The failed start writes no merge authorization. A failed wiki update
			// keeps no unfinished row of a merge candidate step.
			if got := wrDump(t, db, mergeRows); got != merges {
				t.Fatalf("merge authorizations after the failed start = %swant %s", got, merges)
			}
			if got := wrDump(t, db, stepRows); got != steps {
				t.Fatalf("rows of the merge candidate step after the failed start = %swant %s", got, steps)
			}
			if n := dfInt(t, db, `SELECT (SELECT COUNT(*) FROM infrastructure_failures)+(SELECT infrastructure_failure_count FROM projects WHERE id='ws-test')`); n != 0 {
				t.Fatalf("the failed start used %d failure allowances, want none", n)
			}
		})
	}
}

// The wiki update of an archived Project stays active while the result of its
// merge has no proof. Receipt recovery starts for the archived Project, and
// the wiki update ends after the result.
func TestArchivedProjectWaitsForTheMergeResult(t *testing.T) {
	const notMerged = "Culler did not merge the wiki commit. The repository operation failed."
	for _, test := range []struct {
		name   string
		result wmResult
		end    string // the end after the result
		next   string // the end after the next start request
	}{
		{"merged", wmApplied, wmSucceeded(wrCommitA, wrCommitC), wmSucceeded(wrCommitA, wrCommitC)},
		{"not merged", wmNotApplied(wrCommitA), wmFailed(notMerged), wmFailed(notMerged)},
		// A new commit on culler gives a new merge candidate step to an active
		// Project. An archived Project gets no step, and the wiki update ends.
		{"not merged after a new commit on culler", wmNotApplied(strings.Repeat("d", 40)), wmActive, wmFailed("The Project is archived.")},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := dvSpine(t)
			clocks := fixClocks(t, db)
			u := wmFixtureUpdate(t, db)
			candidate := u.candidate()
			merge := u.startRun(dispatch.OperationApply, "")
			stFail(t, db, merge, false)
			if _, err := ProjectSetStatus(db, nil, "ws-test", "archived"); err != nil {
				t.Fatal(err)
			}
			wmStart(t, db, nil)
			if got := u.end(); got != wmActive {
				t.Fatalf("wiki update of the archived Project in the retry time = %swant an active wiki update", got)
			}
			clocks.advance(domain.RetryDelay)
			check := wmStart(t, db, nil, "reconcile/wiki").Started[0]
			if got := u.end(); got != wmActive {
				t.Fatalf("wiki update of the archived Project beside receipt recovery = %swant an active wiki update", got)
			}
			u.record("reconcile", check, test.result(u, merge, candidate))
			if got := u.end(); got != test.end {
				t.Fatalf("wiki update after the result = %swant %s", got, test.end)
			}
			if finished := dfInt(t, db, `SELECT finished FROM merge_operations WHERE run_id=?`, merge); finished != 1 {
				t.Fatal("the result left the merge authorization unfinished")
			}
			clocks.advance(time.Hour)
			wmStart(t, db, nil)
			if got := u.end(); got != test.next {
				t.Fatalf("wiki update after the next start request = %swant %s", got, test.next)
			}
		})
	}
}

// An install pause request waits for the run of a wiki update, for each active
// execution, and for an unfinished merge authorization. A wiki update that
// waits for a repository operation holds no install pause. With the install
// pause, only receipt recovery starts.
func TestInstallPauseCountsOnlyTheRunOfAWikiUpdate(t *testing.T) {
	db := dvSpine(t)
	clocks := fixClocks(t, db)
	testOwner(t, db)
	path := filepath.Join(os.Getenv("MC_HOME"), "spine.db")
	// work requires the pipeline runs with no end and the repository work
	// that the release check counts.
	work := func(state string, runs, operations int) {
		t.Helper()
		got, err := InspectReleaseDatabase(path)
		if err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		if got.Runs != runs || got.RepositoryOperations != operations || got.Idle() != (runs+operations == 0) {
			t.Fatalf("%s: the release check counts %d runs and %d repository operations, idle %v; want %d and %d",
				state, got.Runs, got.RepositoryOperations, got.Idle(), runs, operations)
		}
	}
	// paused sends the start request of an install pause and requires the started operations.
	paused := func(want ...string) StartResponse {
		t.Helper()
		resp := startRequest(t, db, true)
		stWantStarted(t, db, resp, want...)
		return resp
	}

	work("no wiki update", 0, 0)
	u := wmFixtureUpdate(t, db)
	work("an active Scribe run", 1, 0)
	u.submit()
	work("a wiki commit that waits for its capture", 0, 0)
	paused()
	capture := wmStart(t, db, nil, "capture/wiki").Started[0]
	work("an active capture", 1, 1)
	u.record("capture", capture, RepositoryResult{InputCommit: u.startCommit, ResultCommit: u.commit, RetainedRef: "refs/mc/wiki/commits/" + u.run})
	work("a wiki commit that waits for its merge candidate step", 0, 0)
	paused()
	resp := wmStart(t, db, nil, "prepare/wiki", "cleanup/wiki")
	work("an active merge candidate step and an active cleanup", 2, 2)
	u.record("cleanup", resp.Started[1], RepositoryResult{})
	candidate := u.storeCandidate(executionEffect(t, db, resp.Started[0]))
	work("a merge candidate that waits for its merge", 0, 0)
	paused()
	merge := wmStart(t, db, nil, "apply/wiki").Started[0]
	work("an active merge", 1, 2)
	stFail(t, db, merge, false)
	work("a merge with no proven result", 0, 1)
	paused()
	clocks.advance(domain.RetryDelay)
	check := paused("reconcile/wiki").Started[0]
	work("an active receipt recovery", 1, 2)
	u.record("reconcile", check, u.applied(merge, candidate))
	work("a merged wiki commit", 0, 0)
	if got, want := u.end(), wmSucceeded(wrCommitA, wrCommitC); got != want {
		t.Fatalf("wiki update after the proven merge = %swant %s", got, want)
	}
}

// The start response reports work that an install pause waits for while a
// merge authorization of a wiki commit is unfinished, also with no active execution.
func TestDrainPendingCountsAnUnfinishedWikiMerge(t *testing.T) {
	db := dvSpine(t)
	u := wmFixtureUpdate(t, db)
	candidate := u.candidate()
	pending := func(state string, want bool) {
		t.Helper()
		if n := dfInt(t, db, `SELECT COUNT(*) FROM repository_executions WHERE ended_at IS NULL`); n != 0 {
			t.Fatalf("%s: %d executions are active, want none", state, n)
		}
		if resp := startRequest(t, db, true); resp.DrainPending != want || len(resp.Started) != 0 {
			t.Fatalf("%s: the start response reports pending work %v and the starts %v; want %v and no start", state, resp.DrainPending, resp.Started, want)
		}
	}
	pending("a merge candidate that waits for its merge", false)
	merge := u.startRun(dispatch.OperationApply, "")
	stFail(t, db, merge, false)
	pending("a merge with no result", true)
	u.record("reconcile", u.startRun(dispatch.OperationReconcile, merge), RepositoryResult{Status: "operator-attention",
		OperatorAction: "Culler cannot write the file .git/index.lock in the Project repository. Make sure that your user can write the .git folder."})
	pending("a receipt recovery that names a repair", true)
	u.record("reconcile", u.startRun(dispatch.OperationReconcile, merge), u.applied(merge, candidate))
	pending("a merged wiki commit", false)
}

// The start response lists each active execution of a wiki update with the
// fields that the resident executes.
func TestStartResponseOfAWikiExecution(t *testing.T) {
	db := dvSpine(t)
	clocks := fixClocks(t, db)
	u := wmFixtureUpdate(t, db)
	u.submit()
	// The Scribe run ended 19 seconds before the start request.
	dvExec(t, db, `UPDATE runs SET ended_at=? WHERE id=?`, clocks.now.Add(-19*time.Second).Format(spineTime), u.run)
	limit := dfInt(t, db, `SELECT max_temporary_bytes FROM repository_limits WHERE project='ws-test'`)
	workspace := dfStr(t, db, `SELECT workspace_root FROM sandbox_profiles WHERE id='default'`)
	// listed requires the entry of the run in the response: the base fields of
	// a wiki execution and the fields of the operation, and no other field.
	listed := func(resp StartResponse, run, operation string, fields map[string]any) {
		t.Helper()
		want := map[string]any{"run_id": run, "operation": operation, "wiki_update": u.run, "project": "ws-test",
			"workspace_root": workspace, "destination_branch": "culler"}
		for key, value := range fields {
			want[key] = value
		}
		for _, execution := range resp.Executions {
			body, err := json.Marshal(execution)
			if err != nil {
				t.Fatal(err)
			}
			if got := decodeEffect(t, body); got["run_id"] == run {
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s in the start response = %v, want %v", operation, got, want)
				}
				return
			}
		}
		t.Fatalf("the start response lists no execution %s: %v", run, resp.Executions)
	}
	const short, long = int64(300000), int64(600000)

	resp := wmStart(t, db, nil, "capture/wiki")
	listed(resp, resp.Started[0], "capture", map[string]any{"input_commit": u.startCommit, "result_commit": u.commit, "producer_run": u.run,
		"deadline_ms": short, "container_grace_ms": int64(41000)})
	u.record("capture", resp.Started[0], RepositoryResult{InputCommit: u.startCommit, ResultCommit: u.commit, RetainedRef: "refs/mc/wiki/commits/" + u.run})

	clocks.advance(time.Minute)
	resp = wmStart(t, db, nil, "prepare/wiki", "cleanup/wiki")
	step := resp.Started[0]
	prepare := map[string]any{"max_temporary_bytes": limit, "submitted_commit": u.commit, "start_commit": u.startCommit, "deadline_ms": long}
	listed(resp, step, "prepare", prepare)
	listed(resp, resp.Started[1], "cleanup", map[string]any{"producer_run": u.run, "deadline_ms": short, "container_grace_ms": int64(0)})
	u.record("cleanup", resp.Started[1], RepositoryResult{})

	// A prepare recovery names the interrupted run.
	wmRestartResident(t, db)
	clocks.advance(domain.HeartbeatLimit)
	resp = wmStart(t, db, []string{step}, "prepare/wiki")
	prepare["previous_run"] = step
	listed(resp, resp.Started[0], "prepare", prepare)
	candidate := u.storeCandidate(executionEffect(t, db, resp.Started[0]))

	resp = wmStart(t, db, nil, "apply/wiki")
	merge := resp.Started[0]
	listed(resp, merge, "apply", map[string]any{"max_temporary_bytes": limit, "submitted_commit": u.commit, "deadline_ms": long,
		"application": repository.Application{Authorization: merge, Candidate: candidate}})
	stFail(t, db, merge, false)

	clocks.advance(domain.RetryDelay)
	resp = wmStart(t, db, nil, "reconcile/wiki")
	listed(resp, resp.Started[0], "reconcile", map[string]any{"submitted_commit": u.commit, "previous_run": merge, "deadline_ms": short,
		"application": repository.Application{Authorization: merge, Candidate: candidate}})
}

// An unfinished merge authorization of a wiki commit holds each merge step of
// a task. While the merge runs, a due merge candidate step of a task holds
// the agent runs, as it does with no wiki update. After the merge ends with
// no proven result, that step waits and holds no agent run: the tick starts
// the Worker run of the next task, and a start request starts only receipt
// recovery of the wiki commit. The result of the merge ends the wait, and the
// next start request starts the merge step of the task.
func TestWikiReceiptRecoveryHoldsMergeStepsAndNoAgentRun(t *testing.T) {
	for _, test := range []struct {
		name   string
		result wmResult
		end    string
	}{
		{"merged", wmApplied, wmSucceeded(wrCommitA, wrCommitC)},
		{"not merged", wmNotApplied(wrCommitA), wmFailed("Culler did not merge the wiki commit. The repository operation failed.")},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := dvSpine(t)
			clocks := fixClocks(t, db)
			// Task 1 needs a merge candidate step. Task 2 needs a Worker run.
			stTask(t, db, 1, "ws-test", dispatch.StatusPackaged)
			dvExec(t, db, `UPDATE tasks SET candidate_id=NULL WHERE id=1`)
			stTask(t, db, 2, "ws-test", dispatch.StatusSeeded)
			testOwner(t, db)
			before := wmDecide(t, db)
			if before.action.Kind != dispatch.KindIdle || before.action.Idle != dispatch.IdleRepositoryOperation || !before.hold || before.rec.WikiReceiptRecovery {
				t.Fatalf("tick results of the fixture = %+v, hold %v, receipt recovery fact %v; want idle for a repository operation with the agent run hold and no fact",
					before.action, before.hold, before.rec.WikiReceiptRecovery)
			}
			same := func(step string) {
				t.Helper()
				if after := wmDecide(t, db); !reflect.DeepEqual(after, before) {
					t.Fatalf("%s: the tick results changed:\n%+v\nwant\n%+v", step, after, before)
				}
			}
			// waits requires the tick results of the wait: the fact of the task
			// records, no agent run hold, and the Worker run of task 2.
			waits := func(step string) {
				t.Helper()
				d := wmDecide(t, db)
				spawn := d.action.Spawn
				if !d.rec.WikiReceiptRecovery || d.hold || d.action.Kind != dispatch.KindSpawn ||
					spawn.Role != dispatch.RoleWorker || spawn.SubjectID == nil || *spawn.SubjectID != 2 {
					t.Fatalf("%s: tick results = %+v with the spawn %+v, hold %v, receipt recovery fact %v; want the Worker run of task 2 with no agent run hold",
						step, d.action, spawn, d.hold, d.rec.WikiReceiptRecovery)
				}
			}

			// The direct starts give the state of a task that reached its merge
			// candidate step after the merge of the wiki commit started.
			u := wmFixtureUpdate(t, db)
			candidate := u.candidate()
			merge := u.startRun(dispatch.OperationApply, "")
			wmStart(t, db, nil)
			same("an active merge of the wiki commit")
			stFail(t, db, merge, false)
			wmStart(t, db, nil)
			waits("the retry time of the merge")
			clocks.advance(domain.RetryDelay)
			check := wmStart(t, db, nil, "reconcile/wiki").Started[0]
			waits("an active receipt recovery")
			// A receipt recovery with no proof keeps the wait.
			u.record("reconcile", check, RepositoryResult{Status: "operator-attention",
				OperatorAction: "Culler cannot write the file .git/index.lock in the Project repository. Make sure that your user can write the .git folder."})
			wmStart(t, db, nil)
			waits("a receipt recovery that names a repair")
			clocks.advance(domain.RetryDelay)
			check = wmStart(t, db, nil, "reconcile/wiki").Started[0]
			u.record("reconcile", check, test.result(u, merge, candidate))
			if got := u.end(); got != test.end {
				t.Fatalf("wiki update after the result = %swant %s", got, test.end)
			}
			same("the result of the merge")
			wmStart(t, db, nil, "prepare/1")
		})
	}
}

// While the result of a merge of a wiki commit has no proof, each approved
// packet states the receipt recovery and the repair of the operator checkout.
func TestPacketListStatesTheRepairOfTheWikiHold(t *testing.T) {
	const refusal = "A merge into culler stopped before it updated the branch. If the file .git/index.lock exists, remove it. " +
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
			candidate := u.candidate()
			merge := u.startRun(dispatch.OperationApply, "")
			// The operator approves two tasks while the merge of the wiki commit runs.
			for _, id := range []int64{1, 2} {
				stMergeable(t, db, id)
			}
			for _, id := range []int64{1, 2} {
				stApprove(t, db, id)
			}
			dvExec(t, db, `UPDATE tasks SET blocked=1,blocked_kind='checkout',blocked_reason='The operator must repair the checkout.' WHERE id=2`)
			// states requires the merge state of the packet of task 1. The
			// packet of the blocked task 2 keeps its own text.
			states := func(step, state, text string) {
				t.Helper()
				if got, gotText, related := stMergeState(t, db, 1); got != state || gotText != text || related != nil {
					t.Fatalf("%s: merge state of task 1 = %s, %q, related task %v; want %s, %q, and no related task", step, got, gotText, related, state, text)
				}
				if got, gotText, related := stMergeState(t, db, 2); got != "blocked" || gotText != "The operator must repair the checkout." || related != nil {
					t.Fatalf("%s: merge state of the blocked task 2 = %s, %q, related task %v; want its block", step, got, gotText, related)
				}
			}
			states("an active merge of the wiki commit", "after-merge-step", mergeTextAfterMergeStep)
			stFail(t, db, merge, false)
			states("a merge with no result", "recovery", mergeTextRecovery)
			check := u.startRun(dispatch.OperationReconcile, merge)
			states("an active receipt recovery", "recovery", mergeTextRecovery)
			test.check(t, db, u, check)
			want := "Culler checks the result of an interrupted merge. " + test.repair
			states("a receipt recovery with no proof", "recovery", want)
			check = u.startRun(dispatch.OperationReconcile, merge)
			states("the next receipt recovery", "recovery", want)
			u.record("reconcile", check, u.applied(merge, candidate))
			states("a proven merge", "merge", mergeTextMerge)
		})
	}
}

// Each failure of a wiki update in its capture and in its merge changes no
// task, no lease, no allowance, no pause state, and no attention count.
func TestFailedWikiUpdateChangesNoTaskAndNoAllowance(t *testing.T) {
	const (
		captureCause   = "Culler did not capture the wiki commit. "
		candidateCause = "Culler did not prepare the merge candidate of the wiki commit. "
		mergeCause     = "Culler did not merge the wiki commit. "
		changes        = "Local changes in the operator checkout prevent the merge into culler: openwiki/index.md. Commit, stash, or discard each of these changes."
		lock           = "The operator checkout has the file .git/index.lock. A running Git command holds it, or a stopped Git command left it. When no Git command runs, remove the file."
		container      = "A container of the ended run is still present."
	)
	db := dvSpine(t)
	wrPipelineFixture(t, db)
	clocks := fixClocks(t, db)
	// sweep ends each execution that exceeds a liveness limit. renew also
	// sends the heartbeat of the run.
	sweep := func(t *testing.T, running ...string) []string {
		t.Helper()
		resp, err := DeliveryHeartbeat(db, nil, HeartbeatRequest{Owner: testOwner(t, db), Running: running})
		if err != nil {
			t.Fatal(err)
		}
		return resp.Ended
	}
	// expire keeps the execution alive with heartbeats until its deadline.
	expire := func(t *testing.T, run string, deadline time.Duration) {
		t.Helper()
		const beat = 80 * time.Second
		passed := time.Duration(0)
		for ; passed+beat < deadline; passed += beat {
			clocks.advance(beat)
			if ended := sweep(t, run); len(ended) != 0 {
				t.Fatalf("the sweep ended %v before the deadline", ended)
			}
		}
		clocks.advance(deadline - passed)
		if ended := sweep(t, run); !reflect.DeepEqual(ended, []string{run}) {
			t.Fatalf("the sweep at the deadline ended %v, want %s", ended, run)
		}
	}
	captured := func(u *wmUpdate) { u.submit(); u.capture() }
	before := wmPipelineFacts(t, db)
	for _, c := range []struct {
		name  string
		fail  func(t *testing.T, u *wmUpdate)
		cause string
	}{
		{"merge conflict", func(_ *testing.T, u *wmUpdate) {
			captured(u)
			u.record("prepare", u.startRun(dispatch.OperationPrepare, ""), RepositoryResult{Candidate: &repository.Candidate{
				SubmittedCommit: u.commit, DestinationBranch: "culler", DestinationCommit: wrCommitC, Status: "conflict", ConflictPaths: []string{"openwiki/index.md"}}})
		}, "The wiki commit conflicts with the culler branch. Culler discarded the wiki commit."},
		{"Git refusal of the merge candidate step", func(_ *testing.T, u *wmUpdate) {
			captured(u)
			u.record("prepare", u.startRun(dispatch.OperationPrepare, ""), RepositoryResult{Status: "operator-attention", OperatorAction: changes})
		}, changes},
		{"culler without the start commit", func(_ *testing.T, u *wmUpdate) {
			captured(u)
			u.record("prepare", u.startRun(dispatch.OperationPrepare, ""), RepositoryResult{Status: "operator-attention", OperatorAction: wikiStartCommitText})
		}, "The culler branch does not hold the start commit of the wiki update. Culler discarded the wiki commit."},
		{"failed capture", func(t *testing.T, u *wmUpdate) {
			u.submit()
			stFail(t, db, u.startRun(dispatch.OperationCapture, u.run), false)
		}, captureCause + "The repository operation failed."},
		{"capture with no heartbeat", func(t *testing.T, u *wmUpdate) {
			u.submit()
			capture := u.startRun(dispatch.OperationCapture, u.run)
			clocks.advance(domain.HeartbeatLimit)
			if ended := sweep(t); !reflect.DeepEqual(ended, []string{capture}) {
				t.Fatalf("the sweep ended %v, want the capture %s", ended, capture)
			}
		}, captureCause + "The resident sent no heartbeat for 90 seconds."},
		{"capture after its deadline", func(t *testing.T, u *wmUpdate) {
			u.submit()
			expire(t, u.startRun(dispatch.OperationCapture, u.run), dispatch.OperationCapture.Deadline())
		}, captureCause + "The repository operation exceeded its 5-minute limit."},
		{"second resident restart in the capture", func(t *testing.T, u *wmUpdate) {
			u.submit()
			u.startRun(dispatch.OperationCapture, u.run)
			wmRestartResident(t, db)
			if got := u.end(); got != wmActive {
				t.Fatalf("wiki update after the first resident restart = %swant an active wiki update", got)
			}
			u.startRun(dispatch.OperationCapture, u.run)
			wmRestartResident(t, db)
		}, captureCause + "A resident restart ended the repository operation."},
		{"container of the Scribe run after the grace", func(t *testing.T, u *wmUpdate) {
			u.submit()
			present := func() {
				t.Helper()
				if _, err := DeliveryFail(db, nil, u.startRun(dispatch.OperationCapture, u.run), container, true); err != nil {
					t.Fatal(err)
				}
			}
			// A container within the grace of the ended run is no failure.
			present()
			if got := u.end(); got != wmActive {
				t.Fatalf("wiki update after a container within the grace = %swant an active wiki update", got)
			}
			dvExec(t, db, `UPDATE runs SET ended_at=datetime('now','-120 seconds') WHERE id=?`, u.run)
			present()
		}, captureCause + container},
		{"failed merge candidate step", func(t *testing.T, u *wmUpdate) {
			captured(u)
			stFail(t, db, u.startRun(dispatch.OperationPrepare, ""), false)
		}, candidateCause + "The repository operation failed."},
		{"merge candidate step with no heartbeat", func(t *testing.T, u *wmUpdate) {
			captured(u)
			step := u.startRun(dispatch.OperationPrepare, "")
			clocks.advance(domain.HeartbeatLimit)
			if ended := sweep(t); !reflect.DeepEqual(ended, []string{step}) {
				t.Fatalf("the sweep ended %v, want the merge candidate step %s", ended, step)
			}
		}, candidateCause + "The resident sent no heartbeat for 90 seconds."},
		{"merge candidate step after its deadline", func(t *testing.T, u *wmUpdate) {
			captured(u)
			expire(t, u.startRun(dispatch.OperationPrepare, ""), dispatch.OperationPrepare.Deadline())
		}, candidateCause + "The merge step exceeded its 10-minute limit."},
		{"second resident restart in the merge candidate step", func(t *testing.T, u *wmUpdate) {
			captured(u)
			step := u.startRun(dispatch.OperationPrepare, "")
			wmRestartResident(t, db)
			if got := u.end(); got != wmActive {
				t.Fatalf("wiki update after the first resident restart = %swant an active wiki update", got)
			}
			u.startRun(dispatch.OperationPrepare, step)
			wmRestartResident(t, db)
		}, candidateCause + "A resident restart ended the repository operation."},
		{"Git refusal of the merge", func(_ *testing.T, u *wmUpdate) {
			u.candidate()
			merge := u.startRun(dispatch.OperationApply, "")
			u.record("apply", merge, RepositoryResult{Status: "operator-attention", OperatorAction: lock})
			if got := u.end(); got != wmActive {
				t.Fatalf("wiki update after the Git refusal of the merge = %swant an active wiki update", got)
			}
			u.record("reconcile", u.startRun(dispatch.OperationReconcile, merge), RepositoryResult{Status: "not-applied", DestinationCommit: u.startCommit})
		}, lock},
		{"merge with no result that did not run", func(t *testing.T, u *wmUpdate) {
			u.candidate()
			merge := u.startRun(dispatch.OperationApply, "")
			stFail(t, db, merge, false)
			u.record("reconcile", u.startRun(dispatch.OperationReconcile, merge), RepositoryResult{Status: "not-applied", DestinationCommit: u.startCommit})
		}, mergeCause + "The repository operation failed."},
		{"failed start of the capture", func(t *testing.T, u *wmUpdate) {
			u.submit()
			wmRecordFailedStart(t, db, u.operation(dispatch.OperationCapture, u.run), "Culler found no Project repository for the wiki update.")
		}, captureCause + "Culler found no Project repository for the wiki update."},
		{"failed start of the merge candidate step", func(t *testing.T, u *wmUpdate) {
			captured(u)
			wmFailedStart(t, db, u.operation(dispatch.OperationPrepare, "0000000000000000"))
		}, candidateCause + "The wiki update has no interrupted merge candidate step."},
		{"failed start of the merge", func(t *testing.T, u *wmUpdate) {
			captured(u)
			wmFailedStart(t, db, u.operation(dispatch.OperationApply, ""))
		}, mergeCause + "The wiki update has no merge candidate."},
	} {
		u := wmFixtureUpdate(t, db)
		c.fail(t, u)
		same := func(step string) {
			t.Helper()
			if got := wmPipelineFacts(t, db); got != before {
				t.Fatalf("%s: %s changed a pipeline fact:\n%s\nbefore:\n%s", c.name, step, got, before)
			}
		}
		same("the failure")
		if got, want := u.end(), wmFailed(c.cause); got != want {
			t.Fatalf("%s: wiki update = %swant %s", c.name, got, want)
		}
		if n := dfInt(t, db, `SELECT COUNT(*) FROM repository_executions WHERE wiki_update=? AND (charged=1 OR ended_at IS NULL)`, u.run); n != 0 {
			t.Fatalf("%s: %d executions of the wiki update have a charge or no end, want none", c.name, n)
		}
		wrStopped(t, db, u.run)
		same("the stop confirmation")
	}
}

// mainCommit returns the commit of culler in the Project repository.
func (f *deliveryFixture) mainCommit() string {
	f.t.Helper()
	return srcGit(f.t, f.workspace, "rev-parse", "culler")
}

// A merge that updated culler and lost its result keeps the wiki update active.
// Each receipt recovery with no proof keeps it active. Only the receipt of
// the merge ends the wiki update, as successful.
func TestWikiUpdateStaysActiveUntilReceiptRecoveryDecides(t *testing.T) {
	f := newDeliveryFixture(t)
	clocks := fixClocks(t, f.db)
	u := f.wikiUpdate()
	candidate := u.candidate()
	effect := u.start(dispatch.OperationApply, "")
	merge := effect["run_id"].(string)
	// The merge updates culler. A resident restart ends its execution before the result arrives.
	late := f.nativeExecution(effect)
	if late.Applied == nil || f.mainCommit() != candidate.Commit {
		t.Fatalf("merge = %+v with culler at %s, want culler at the candidate commit %s", late, f.mainCommit(), candidate.Commit)
	}
	if ended := wmRestartResident(t, f.db); !reflect.DeepEqual(ended, []string{merge}) {
		t.Fatalf("the resident restart ended %v, want the merge %s", ended, merge)
	}
	if _, err := RepositoryRecord(f.db, nil, "apply", merge, late); !rnStale(err) {
		t.Fatalf("result of the ended merge = %v, want a stale run refusal", err)
	}
	// active requires the active wiki update with its unfinished merge authorization.
	active := func(step string) {
		t.Helper()
		if got := u.end(); got != wmActive {
			t.Fatalf("%s: wiki update = %swant an active wiki update", step, got)
		}
		if finished := dfInt(t, f.db, `SELECT finished FROM merge_operations WHERE run_id=?`, merge); finished != 0 {
			t.Fatalf("%s: the merge authorization is finished", step)
		}
	}
	active("a merge with no result")

	// The new owner confirms the stop of the merge after the heartbeat limit of the earlier owner.
	clocks.advance(domain.HeartbeatLimit)
	check := wmStart(t, f.db, []string{merge}, "reconcile/wiki").Started[0]
	active("an active receipt recovery")
	stFail(t, f.db, check, false)
	active("a receipt recovery with no result")
	clocks.advance(domain.RetryDelay)
	check = wmStart(t, f.db, nil, "reconcile/wiki").Started[0]
	u.record("reconcile", check, RepositoryResult{Status: "operator-attention",
		OperatorAction: "Culler cannot write the file .git/index.lock in the Project repository. Make sure that your user can write the .git folder."})
	active("a receipt recovery that names a repair")

	// The receipt of the merge proves the result.
	clocks.advance(domain.RetryDelay)
	recovered := f.request("reconcile/wiki")[0].Applied
	if recovered == nil || !recovered.Recovered || recovered.Authorization != merge || recovered.Commit != candidate.Commit {
		t.Fatalf("receipt recovery = %+v, want the receipt of the merge %s at %s", recovered, merge, candidate.Commit)
	}
	if got, want := u.end(), wmSucceeded(u.startCommit, candidate.Commit); got != want {
		t.Fatalf("wiki update after the proven merge = %swant %s", got, want)
	}
	if finished := dfInt(t, f.db, `SELECT finished FROM merge_operations WHERE run_id=?`, merge); finished != 1 {
		t.Fatal("the proven merge left the merge authorization unfinished")
	}
}

// A merge that stopped after its journal write and before the update of culler
// leaves receipt recovery with no proof. Receipt recovery names the repair of
// the operator checkout, the wiki update stays active, and an approved packet
// states the repair. After the repair, receipt recovery proves that the merge
// did not run, and the wiki update fails.
func TestWikiReceiptRecoveryAfterAJournalWithNoDestinationUpdate(t *testing.T) {
	f := newDeliveryFixture(t)
	clocks := fixClocks(t, f.db)
	u := f.wikiUpdate()
	u.candidate()
	effect := u.start(dispatch.OperationApply, "")
	merge := effect["run_id"].(string)
	// An approved task waits for its merge.
	stMergeable(t, f.db, 1)
	stApprove(t, f.db, 1)

	// The merge wrote its journal and reserved the index. It stopped before the update of culler.
	journal := filepath.Join(f.workspace, ".git/mc/applications", merge+".json")
	lock := filepath.Join(f.workspace, ".git/index.lock")
	body, err := json.Marshal(map[string]any{"application": effect["application"], "index_device": 0, "index_inode": 0})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(journal), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, journal, string(body))
	writeFile(t, lock, "")
	stFail(t, f.db, merge, false)

	refusal := "A merge into culler stopped before it updated the branch. If the file .git/index.lock exists, remove it. " +
		"Then discard each change in the checkout that you did not make, and remove the file .git/mc/applications/" + merge + ".json."
	clocks.advance(domain.RetryDelay)
	if result := f.request("reconcile/wiki")[0]; result.Status != "operator-attention" || result.OperatorAction != refusal {
		t.Fatalf("receipt recovery beside the journal = %+v, want the repair %q", result, refusal)
	}
	if got := u.end(); got != wmActive {
		t.Fatalf("wiki update before the repair = %swant an active wiki update", got)
	}
	if repair := f.wikiRecords().Update.Repair; repair != refusal {
		t.Fatalf("repair text = %q, want %q", repair, refusal)
	}
	if state, text, related := stMergeState(t, f.db, 1); state != "recovery" || text != mergeTextRecovery+" "+refusal || related != nil {
		t.Fatalf("merge state of the approved packet = %s, %q, related task %v; want recovery with the repair", state, text, related)
	}
	// The unfinished merge authorization holds the merge of the task.
	if resp := wmStart(t, f.db, nil); resp.DueInMS == nil || *resp.DueInMS != domain.RetryDelay.Milliseconds() {
		t.Fatalf("start response before the repair is due in %v ms, want the retry time of receipt recovery", resp.DueInMS)
	}

	// The operator repairs the checkout. Receipt recovery then finds no journal and no receipt.
	for _, path := range []string{journal, lock} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	clocks.advance(domain.RetryDelay)
	if result := f.request("reconcile/wiki")[0]; result.Status != "not-applied" || result.DestinationCommit != u.startCommit {
		t.Fatalf("receipt recovery after the repair = %+v, want not-applied at %s", result, u.startCommit)
	}
	if got, want := u.end(), wmFailed("Culler did not merge the wiki commit. The repository operation failed."); got != want {
		t.Fatalf("wiki update after the repair = %swant %s", got, want)
	}
	if got := f.mainCommit(); got != u.startCommit {
		t.Fatalf("culler holds %s after the failed wiki update, want the start commit %s", got, u.startCommit)
	}
	// The result ends the hold.
	wmStart(t, f.db, nil, "apply/1")
}

// A changed destination ends a merge step of a wiki commit with no failure.
// The next merge candidate step waits one destination quiet interval. A wiki
// update has no destination observation.
func TestWikiDestinationChangeWaitsOneQuietInterval(t *testing.T) {
	const quiet = 30 * time.Second
	f := newDeliveryFixture(t)
	clocks := fixClocks(t, f.db)
	u := f.wikiUpdate()
	u.submit()
	u.capture()
	u.clean(u.run)
	// step sends one start request, requires the one merge step, and executes
	// it. It cleans the temporary repository of a merge candidate step.
	step := func(want string) (string, RepositoryResult) {
		t.Helper()
		run := wmStart(t, f.db, nil, want).Started[0]
		result := f.runExecution(executionEffect(t, f.db, run))
		if want == "prepare/wiki" {
			u.clean(run)
		}
		return run, result
	}
	// waits requires an active wiki update with no merge step before the end
	// of the quiet interval, and a response that names that end. It moves the
	// clocks to that end.
	waits := func(state string) {
		t.Helper()
		if got := u.end(); got != wmActive {
			t.Fatalf("%s: wiki update = %swant an active wiki update", state, got)
		}
		for _, left := range []time.Duration{quiet, time.Second} {
			if resp := wmStart(t, f.db, nil); resp.DueInMS == nil || *resp.DueInMS != left.Milliseconds() {
				t.Fatalf("%s: the start response is due in %v ms, want %d", state, resp.DueInMS, left.Milliseconds())
			}
			clocks.advance(left - time.Second)
		}
		clocks.advance(time.Second)
	}
	// candidates counts the merge candidates of the wiki update.
	candidates := func() int64 {
		t.Helper()
		return dfInt(t, f.db, `SELECT COUNT(*) FROM merge_candidates WHERE wiki_update=?`, u.run)
	}

	// Main changes while the merge candidate step runs.
	var moved string
	f.afterPrepare = func(repository.Candidate) { moved = f.commitMain("first.txt", "a first accepted contribution\n") }
	first, result := step("prepare/wiki")
	f.afterPrepare = nil
	if result.Status != "destination-changed" || result.DestinationCommit != moved || candidates() != 0 {
		t.Fatalf("merge candidate step beside a change of culler = %+v with %d candidates, want destination-changed at %s and no candidate", result, candidates(), moved)
	}
	if finished := dfInt(t, f.db, `SELECT finished FROM merge_operations WHERE run_id=?`, first); finished != 1 {
		t.Fatal("the changed destination left the merge candidate step unfinished")
	}
	waits("after a merge candidate step with a changed destination")

	// Main changes between the merge candidate step and the merge.
	if _, result := step("prepare/wiki"); result.Status != "" || result.Candidate.DestinationCommit != moved || candidates() != 1 {
		t.Fatalf("second merge candidate step = %+v with %d candidates, want a merged candidate at %s", result, candidates(), moved)
	}
	moved = f.commitMain("second.txt", "a second accepted contribution\n")
	merge, result := step("apply/wiki")
	if result.Status != "destination-changed" || result.DestinationCommit != moved || result.Applied != nil {
		t.Fatalf("merge beside a change of culler = %+v, want destination-changed at %s", result, moved)
	}
	if finished := dfInt(t, f.db, `SELECT finished FROM merge_operations WHERE run_id=?`, merge); finished != 1 {
		t.Fatal("the changed destination left the merge authorization unfinished")
	}
	waits("after a merge with a changed destination")

	// Main changes while the merge has no result. Receipt recovery proves that
	// the merge did not run, and the candidate is not current.
	if _, result := step("prepare/wiki"); result.Status != "" || result.Candidate.DestinationCommit != moved || candidates() != 2 {
		t.Fatalf("third merge candidate step = %+v with %d candidates, want a merged candidate at %s", result, candidates(), moved)
	}
	merge = wmStart(t, f.db, nil, "apply/wiki").Started[0]
	moved = f.commitMain("third.txt", "a third accepted contribution\n")
	stFail(t, f.db, merge, false)
	clocks.advance(domain.RetryDelay)
	if _, result := step("reconcile/wiki"); result.Status != "not-applied" || result.DestinationCommit != moved {
		t.Fatalf("receipt recovery beside a change of culler = %+v, want not-applied at %s", result, moved)
	}
	waits("after a receipt recovery with a changed destination")
	wmStart(t, f.db, nil, "prepare/wiki")

	if got := f.mainCommit(); got != moved {
		t.Fatalf("culler holds %s, want the last accepted commit %s", got, moved)
	}
	if n := dfInt(t, f.db, `SELECT (SELECT COUNT(*) FROM repository_executions WHERE operation='observe')+(SELECT COUNT(*) FROM repository_observations)`); n != 0 {
		t.Fatalf("the wiki update has %d destination observations, want none", n)
	}
	if n := dfInt(t, f.db, `SELECT COUNT(*) FROM repository_executions WHERE wiki_update=? AND (charged=1 OR end_reason NOT IN ('result','failure'))`, u.run); n != 0 {
		t.Fatalf("%d executions of the wiki update have a charge or another end, want none", n)
	}
}

// A Git refusal of the merge candidate step fails the wiki update. A Git
// refusal of the merge keeps the wiki update active until receipt recovery
// proves that the merge did not run. The cause is the refusal text.
func TestWikiGitRefusalFailsTheWikiUpdate(t *testing.T) {
	t.Run("merge candidate step", func(t *testing.T) {
		const refusal = "Local changes in the operator checkout prevent the merge into culler: openwiki/flow.md. Commit, stash, or discard each of these changes."
		f := newDeliveryFixture(t)
		u := f.wikiUpdate()
		u.submit()
		// The operator checkout holds an untracked file at the path of the new wiki page.
		draft := filepath.Join(f.workspace, "openwiki/flow.md")
		writeFile(t, draft, "A local draft.\n")
		f.request("capture/wiki")
		if result := f.request("prepare/wiki", "cleanup/wiki")[0]; result.Status != "operator-attention" || result.OperatorAction != refusal {
			t.Fatalf("merge candidate step = %+v, want the refusal %q", result, refusal)
		}
		if got, want := u.end(), wmFailed(refusal); got != want {
			t.Fatalf("wiki update after the Git refusal = %swant %s", got, want)
		}
		if got := wrDump(t, f.db, `SELECT operation,finished FROM merge_operations WHERE wiki_update=?`, u.run); got != "operation=prepare|finished=1|\n" {
			t.Fatalf("merge rows after the Git refusal = %swant one finished merge candidate step", got)
		}
		if n := dfInt(t, f.db, `SELECT COUNT(*) FROM merge_candidates`); n != 0 {
			t.Fatalf("the refused merge candidate step wrote %d candidates, want none", n)
		}
		if body, err := os.ReadFile(draft); err != nil || string(body) != "A local draft.\n" || f.mainCommit() != u.startCommit {
			t.Fatalf("the refused step changed the operator checkout: %q, %v, culler at %s", body, err, f.mainCommit())
		}
		// The failed wiki update needs only the cleanup of the merge candidate step.
		f.request("cleanup/wiki")
		if resp := wmStart(t, f.db, nil); resp.DueInMS != nil {
			t.Fatalf("start response after the failed wiki update is due in %d ms, want no wait", *resp.DueInMS)
		}
	})
	t.Run("merge", func(t *testing.T) {
		const refusal = "The operator checkout has the file .git/index.lock. A running Git command holds it, or a stopped Git command left it. When no Git command runs, remove the file."
		f := newDeliveryFixture(t)
		u := f.wikiUpdate()
		u.candidate()
		lock := filepath.Join(f.workspace, ".git/index.lock")
		writeFile(t, lock, "")
		run := wmStart(t, f.db, nil, "apply/wiki").Started[0]
		if result := f.runExecution(executionEffect(t, f.db, run)); result.Status != "operator-attention" || result.OperatorAction != refusal {
			t.Fatalf("merge = %+v, want the refusal %q", result, refusal)
		}
		// The refusal proves no result of the merge. The merge authorization stays unfinished.
		if got, want := wmExecution(t, f.db, run), "operation=apply|target=wiki/"+u.run+"|task_id=<nil>|wiki_update="+u.run+
			"|end_reason=result|charged=0|retry=0|end_condition="+refusal+"|\n"; got != want {
			t.Fatalf("refused merge = %swant %s", got, want)
		}
		if finished := dfInt(t, f.db, `SELECT finished FROM merge_operations WHERE run_id=?`, run); finished != 0 || u.end() != wmActive {
			t.Fatalf("after the Git refusal the merge authorization is finished (%d) or the wiki update has an end: %s", finished, u.end())
		}
		if repair := f.wikiRecords().Update.Repair; repair != refusal {
			t.Fatalf("repair text = %q, want %q", repair, refusal)
		}
		// Receipt recovery starts with no wait. It finds no journal and no receipt.
		if result := f.request("reconcile/wiki")[0]; result.Status != "not-applied" || result.DestinationCommit != u.startCommit {
			t.Fatalf("receipt recovery = %+v, want not-applied at %s", result, u.startCommit)
		}
		if got, want := u.end(), wmFailed(refusal); got != want {
			t.Fatalf("wiki update after the receipt recovery = %swant %s", got, want)
		}
		if finished := dfInt(t, f.db, `SELECT finished FROM merge_operations WHERE run_id=?`, run); finished != 1 || f.mainCommit() != u.startCommit {
			t.Fatalf("after the receipt recovery the merge authorization is unfinished (%d) or culler moved to %s", finished, f.mainCommit())
		}
		if _, err := os.Stat(lock); err != nil {
			t.Fatalf("the refused merge removed the lock file of another Git command: %v", err)
		}
	})
}

// The operator moved culler to a commit that does not hold the start commit of
// the wiki update. The merge candidate step refuses the wiki commit, and the
// wiki update fails with no merge.
func TestWikiCandidateWithoutTheStartCommitFailsTheWikiUpdate(t *testing.T) {
	f := newDeliveryFixture(t)
	earlier := f.mainCommit()
	f.commitMain("feature.txt", "a contribution that culler loses\n")
	u := f.wikiUpdate()
	u.submit()
	f.request("capture/wiki")
	srcGit(t, f.workspace, "reset", "-q", "--hard", earlier)
	destination := f.commitMain("other.txt", "another accepted contribution\n")

	const cause = "The culler branch does not hold the start commit of the wiki update. Culler discarded the wiki commit."
	if result := f.request("prepare/wiki", "cleanup/wiki")[0]; result.Status != "operator-attention" || result.OperatorAction != cause || result.RetainedRef != "" {
		t.Fatalf("merge candidate step = %+v, want the refusal %q and no retained candidate", result, cause)
	}
	if got, want := u.end(), wmFailed(cause); got != want {
		t.Fatalf("wiki update = %swant %s", got, want)
	}
	if n := dfInt(t, f.db, `SELECT (SELECT COUNT(*) FROM merge_candidates)+(SELECT COUNT(*) FROM merge_operations WHERE finished=0 OR operation<>'prepare')`); n != 0 {
		t.Fatalf("the refused wiki commit has %d candidates, unfinished steps, and merge authorizations; want none", n)
	}
	if got := f.mainCommit(); got != destination {
		t.Fatalf("culler holds %s, want the commit of the operator %s", got, destination)
	}
	if _, err := os.Stat(filepath.Join(f.workspace, "feature.txt")); !os.IsNotExist(err) {
		t.Fatalf("the refused wiki commit returned the file that culler lost: %v", err)
	}
	f.request("cleanup/wiki")
	if resp := wmStart(t, f.db, nil); resp.DueInMS != nil {
		t.Fatalf("start response after the failed wiki update is due in %d ms, want no wait", *resp.DueInMS)
	}
}

// A wiki update runs from the wiki round to its successful end: the Scribe
// run, the capture, the merge candidate step, and the merge. The wiki commit
// on culler gives a current wiki, and the wiki round starts no new wiki update.
func TestWikiMergeLeavesTheWikiCurrent(t *testing.T) {
	f := newDeliveryFixture(t)
	start := f.mainCommit()
	run := wrWantSpawn(t, wrRound(t, f.db, "ws-test", start, start), "ws-test")
	u := &wmUpdate{t: t, db: f.db, f: f, run: run, startCommit: start}
	u.allocate()

	// The Scribe commits one wiki page and reports the wiki commit with its terminal action.
	u.commit = u.commitPage()
	root := f.wikiRoot(run)
	if err := os.Symlink(filepath.Join(root, "repo"), filepath.Join(root, "source")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_TASK_STORE_ROOT", root)
	scribe := &wcRun{t: t, db: f.db, id: wcScribe(run), run: run, source: filepath.Join(root, "repo"), start: start}
	if _, err := scribe.complete(u.commit, scribe.receipt("complete")); err != nil {
		t.Fatalf("terminal action of the Scribe: %v", err)
	}
	wrStopped(t, f.db, run)
	wrWantIdle(t, wrRound(t, f.db, "ws-test", start, start), "ws-test", "wiki-update-active")

	f.request("capture/wiki")
	candidate := f.request("prepare/wiki", "cleanup/wiki")[0].Candidate
	if candidate == nil || candidate.Status != "merged" || candidate.SubmittedCommit != u.commit || candidate.DestinationCommit != start {
		t.Fatalf("merge candidate = %+v, want the wiki commit %s merged at %s", candidate, u.commit, start)
	}
	if applied := f.request("apply/wiki", "cleanup/wiki")[0].Applied; applied == nil || applied.Commit != candidate.Commit {
		t.Fatalf("merge = %+v, want the candidate commit %s", applied, candidate.Commit)
	}
	if got, want := u.end(), wmSucceeded(start, candidate.Commit); got != want {
		t.Fatalf("wiki update after the merge = %swant %s", got, want)
	}
	merged := f.mainCommit()
	if merged != candidate.Commit || wrStoredCommit(t, f.db, "ws-test") != merged {
		t.Fatalf("culler holds %s and the accepted commit is %s, want the candidate commit %s", merged, wrStoredCommit(t, f.db, "ws-test"), candidate.Commit)
	}
	if body, err := os.ReadFile(filepath.Join(f.workspace, "openwiki/flow.md")); err != nil || !strings.Contains(string(body), "A generated explanation.") {
		t.Fatalf("the operator checkout lacks the new wiki page: %q, %v", body, err)
	}
	if resp := wmStart(t, f.db, nil); resp.DueInMS != nil {
		t.Fatalf("start response after the merge is due in %d ms, want no wait", *resp.DueInMS)
	}

	// Main holds only the wiki commit after the start commit. The wiki is
	// current, also after the interval.
	wrWantIdle(t, wrRound(t, f.db, "ws-test", merged, merged), "ws-test", "wiki-current")
	wrLater(t, 2*time.Hour)
	wrWantIdle(t, wrRound(t, f.db, "ws-test", merged, merged), "ws-test", "wiki-current")
	if runs, updates := wrWikiRows(t, f.db, "ws-test"); runs != 1 || updates != 1 {
		t.Fatalf("%d Scribe runs and %d wiki updates, want one of each", runs, updates)
	}
}

// The operator approves a task while the merge of a wiki commit runs. The
// merge candidate of the task names the commit of culler before the wiki
// commit. The task gets a new merge candidate and merges with no new decision
// and no new agent run.
func TestApprovedTaskMergesAfterAWikiCommit(t *testing.T) {
	const quiet = 30 * time.Second
	f := newDeliveryFixture(t)
	task := f.add("Deliver the source change")
	submitted := f.pass(task, "feature.txt", "complete feature\n")
	f.merge(task, "prepare")
	runs := dfInt(t, f.db, `SELECT COUNT(*) FROM runs WHERE binding IS NOT 'mechanical'`)
	clocks := fixClocks(t, f.db)

	u := f.wikiUpdate()
	u.submit()
	f.request("capture/wiki")
	f.request("prepare/wiki", "cleanup/wiki")
	started := wmStart(t, f.db, nil, "apply/wiki", "cleanup/wiki").Started
	f.approve(task)
	// The active merge of the wiki commit holds the merge of the task.
	if state, text, related := stMergeState(t, f.db, task); state != "after-merge-step" || text != mergeTextAfterMergeStep || related != nil {
		t.Fatalf("merge state of the approved packet = %s, %q, related task %v; want a wait for the current merge step", state, text, related)
	}
	wmStart(t, f.db, nil)
	for _, run := range started {
		f.runExecution(executionEffect(t, f.db, run))
	}
	wiki := f.mainCommit()
	if got, want := u.end(), wmSucceeded(u.startCommit, wiki); got != want || wiki == u.startCommit {
		t.Fatalf("wiki update = %swant %s with a new commit on culler", got, want)
	}

	name := fmt.Sprintf("%d", task)
	if result := f.request("apply/" + name)[0]; result.Status != "destination-changed" || result.DestinationCommit != wiki {
		t.Fatalf("merge of the task = %+v, want destination-changed at the wiki commit %s", result, wiki)
	}
	if resp := wmStart(t, f.db, nil); resp.DueInMS == nil || *resp.DueInMS != quiet.Milliseconds() {
		t.Fatalf("start response after the changed destination is due in %v ms, want %d", resp.DueInMS, quiet.Milliseconds())
	}
	clocks.advance(quiet)
	f.request("observe/" + name)
	if candidate := f.request("prepare/" + name)[0].Candidate; candidate == nil || candidate.Status != "merged" ||
		candidate.SubmittedCommit != submitted || candidate.DestinationCommit != wiki {
		t.Fatalf("new merge candidate of the task = %+v, want the submitted commit %s merged at %s", candidate, submitted, wiki)
	}
	f.request("apply/"+name, "cleanup/"+name)

	if got := wrDump(t, f.db, `SELECT archived,decision,blocked,stalled,correction_open,infrastructure_failure_count FROM tasks WHERE id=?`, task); got !=
		"archived=1|decision=approved|blocked=0|stalled=0|correction_open=0|infrastructure_failure_count=0|\n" {
		t.Fatalf("task after its merge = %s", got)
	}
	if n := dfInt(t, f.db, `SELECT COUNT(*) FROM review_decisions`); n != 1 {
		t.Fatalf("%d review decisions, want the one approval", n)
	}
	if n := dfInt(t, f.db, `SELECT COUNT(*) FROM runs WHERE binding IS NOT 'mechanical' AND role IS NOT 'scribe'`); n != runs {
		t.Fatalf("%d agent runs of the task, want the %d runs before the wiki commit", n, runs)
	}
	for _, file := range []string{"feature.txt", "openwiki/flow.md"} {
		if _, err := os.Stat(filepath.Join(f.workspace, file)); err != nil {
			t.Fatalf("culler lacks %s after both merges: %v", file, err)
		}
	}
	if !(repository.Git{Root: f.workspace}).Ancestor(context.Background(), wiki, f.mainCommit()) {
		t.Fatal("culler lost the wiki commit")
	}
}

// A wiki commit that conflicts with culler fails the wiki update. Culler
// discards the wiki commit: no task, no correction, and no agent run follows.
func TestWikiConflictFailsTheWikiUpdate(t *testing.T) {
	f := newDeliveryFixture(t)
	testOwner(t, f.db)
	facts, decision := wmPipelineFacts(t, f.db), wmDecide(t, f.db)
	u := f.wikiUpdate()
	u.submit()
	// Main gets another content at the path of the new wiki page.
	destination := f.commitMain("openwiki/flow.md", "---\ntitle: Request flow\ntype: Component\n---\nAn explanation of the operator.\n")
	f.request("capture/wiki")
	candidate := f.request("prepare/wiki", "cleanup/wiki")[0].Candidate
	if candidate == nil || candidate.Status != "conflict" || candidate.Commit != "" || !reflect.DeepEqual(candidate.ConflictPaths, []string{"openwiki/flow.md"}) {
		t.Fatalf("merge candidate = %+v, want a conflict at openwiki/flow.md", candidate)
	}
	if got, want := u.end(), wmFailed("The wiki commit conflicts with the culler branch. Culler discarded the wiki commit."); got != want {
		t.Fatalf("wiki update after the conflict = %swant %s", got, want)
	}
	if got := wrDump(t, f.db, `SELECT status,candidate_commit,destination_commit FROM merge_candidates WHERE wiki_update=?`, u.run); got !=
		"status=conflict|candidate_commit=<nil>|destination_commit="+destination+"|\n" {
		t.Fatalf("candidate row after the conflict = %s", got)
	}
	if got := f.mainCommit(); got != destination {
		t.Fatalf("culler holds %s after the conflict, want %s", got, destination)
	}
	// The conflict gives no work to the pipeline of the Project.
	f.request("cleanup/wiki")
	if resp := wmStart(t, f.db, nil); resp.DueInMS != nil {
		t.Fatalf("start response after the conflict is due in %d ms, want no wait", *resp.DueInMS)
	}
	if got := wmPipelineFacts(t, f.db); got != facts {
		t.Fatalf("the conflict changed a pipeline fact:\n%s\nbefore:\n%s", got, facts)
	}
	if after := wmDecide(t, f.db); !reflect.DeepEqual(after, decision) {
		t.Fatalf("the conflict changed the tick results:\n%+v\nwant\n%+v", after, decision)
	}
	if n := dfInt(t, f.db, `SELECT (SELECT COUNT(*) FROM tasks)+(SELECT COUNT(*) FROM activity WHERE kind='task.merge_repair')+(SELECT COUNT(*) FROM runs WHERE binding IS NOT 'mechanical' AND role IS NOT 'scribe')`); n != 0 {
		t.Fatalf("the conflict wrote %d tasks, merge repairs, and agent runs; want none", n)
	}
}

func TestTaskMergeRecordsTheAcceptedCommitOfMain(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, db *sql.DB) int64 // returns the packaged task with a merged candidate
		want  string                               // the stored accepted commit after the merge
	}{
		{"culler", func(t *testing.T, db *sql.DB) int64 {
			stMergeable(t, db, 1)
			return 1
		}, stCandidateCommit},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := dvSpine(t)
			task := test.setup(t, db)
			stApprove(t, db, task)
			// The resident reports the commit of culler with the start request. The
			// merge candidate of the task names the same commit as its destination.
			resp, err := DeliveryStart(db, nil, StartRequest{Owner: testOwner(t, db), AcceptedCommits: map[string]string{"ws-test": wrCommitA}})
			if err != nil {
				t.Fatal(err)
			}
			stWantStarted(t, db, resp, fmt.Sprintf("apply/%d", task))
			if got := wrStoredCommit(t, db, "ws-test"); got != wrCommitA {
				t.Fatalf("accepted commit before the merge = %s, want %s", got, wrCommitA)
			}
			run := resp.Started[0]
			if _, err := RepositoryRecord(db, nil, "apply", run, RepositoryResult{TaskID: task,
				Applied: &repository.Applied{Authorization: run, Commit: stCandidateCommit}}); err != nil {
				t.Fatalf("record of the merge: %v", err)
			}
			if archived := dfInt(t, db, `SELECT archived FROM tasks WHERE id=?`, task); archived != 1 {
				t.Fatal("the merge left the task active")
			}
			if got := wrStoredCommit(t, db, "ws-test"); got != test.want {
				t.Fatalf("accepted commit after the merge = %s, want %s", got, test.want)
			}
		})
	}
}

// A Git refusal of the merge of a task blocks the task. The block reason is
// the refusal text with the instruction to unblock the task.
func TestGitRefusalOfATaskMergeBlocksTheTask(t *testing.T) {
	const refusal = "The operator checkout has the file .git/index.lock. A running Git command holds it, or a stopped Git command left it. When no Git command runs, remove the file."
	db := dvSpine(t)
	stMergeable(t, db, 1)
	stApprove(t, db, 1)
	run := wmStart(t, db, nil, "apply/1").Started[0]
	if _, err := RepositoryRecord(db, nil, "apply", run, RepositoryResult{TaskID: 1, Status: "operator-attention", OperatorAction: refusal}); err != nil {
		t.Fatal(err)
	}
	if got, want := wrDump(t, db, `SELECT blocked,blocked_reason FROM tasks WHERE id=1`), "blocked=1|blocked_reason="+refusal+" Then use Resolve decision with a reason.|\n"; got != want {
		t.Fatalf("task after the Git refusal = %swant %s", got, want)
	}
}

// A merge candidate of a wiki commit holds no change data. The record of a
// candidate with change data is refused, and the merge candidate step stays active.
func TestWikiCandidateRecordRefusesChangeData(t *testing.T) {
	db := dvSpine(t)
	u := wmFixtureUpdate(t, db)
	u.submit()
	u.capture()
	run := u.startRun(dispatch.OperationPrepare, "")
	candidate := repository.Candidate{SubmittedCommit: u.commit, DestinationBranch: "culler", DestinationCommit: u.startCommit, Commit: wrCommitC,
		Status: "merged", Changes: &repository.CandidateChanges{}}
	result := RepositoryResult{WikiUpdate: u.run, RetainedRef: "refs/mc/wiki/candidates/" + run, Candidate: &candidate}
	if _, err := RepositoryRecord(db, nil, "prepare", run, result); !wcRefused(err, "", "the merge candidate of a wiki commit has no change data") {
		t.Fatalf("record of a candidate with change data = %v, want a refusal", err)
	}
	if got, want := wrDump(t, db, `SELECT finished,result FROM merge_operations WHERE run_id=?`, run), "finished=0|result=<nil>|\n"; got != want {
		t.Fatalf("merge candidate step after the refused record = %swant %s", got, want)
	}
	if n := dfInt(t, db, `SELECT (SELECT COUNT(*) FROM merge_candidates)+(SELECT COUNT(*) FROM repository_executions WHERE run_id=? AND ended_at IS NOT NULL)`, run); n != 0 || u.end() != wmActive {
		t.Fatalf("the refused record wrote a candidate or ended the execution (%d), or the wiki update has an end: %s", n, u.end())
	}
	// The same step accepts the candidate with no change data.
	candidate.Changes = nil
	u.record("prepare", run, result)
	if n := dfInt(t, db, `SELECT COUNT(*) FROM merge_candidates WHERE wiki_update=? AND status='merged'`, u.run); n != 1 {
		t.Fatalf("%d merge candidates after the record with no change data, want one", n)
	}
}

// The result of a merge step names the subject of the step. The record
// refuses a result of a task for a merge candidate step of a wiki commit, and
// a result of a wiki update for the merge of a task.
func TestMergeResultNamesTheSubjectOfItsStep(t *testing.T) {
	const refusal = "the merge result does not identify its operation"
	db := dvSpine(t)
	stMergeable(t, db, 1)
	stApprove(t, db, 1)
	u := wmFixtureUpdate(t, db)
	u.submit()
	u.capture()

	step := u.startRun(dispatch.OperationPrepare, "")
	candidate := RepositoryResult{RetainedRef: "refs/mc/wiki/candidates/" + step, Candidate: &repository.Candidate{
		SubmittedCommit: u.commit, DestinationBranch: "culler", DestinationCommit: u.startCommit, Commit: wrCommitC, Status: "merged"}}
	ofTask := candidate
	ofTask.TaskID = 1
	if _, err := RepositoryRecord(db, nil, "prepare", step, ofTask); !wcRefused(err, "", refusal) {
		t.Fatalf("result of a task for the merge candidate step of a wiki commit = %v, want a refusal", err)
	}
	if n := dfInt(t, db, `SELECT (SELECT COUNT(*) FROM merge_candidates WHERE wiki_update IS NOT NULL)+(SELECT finished FROM merge_operations WHERE run_id=?)`, step); n != 0 {
		t.Fatal("the refused result finished the merge candidate step or wrote a candidate")
	}
	u.record("prepare", step, candidate)

	merge := startOperation(t, db, dispatch.RepositoryOperation{Project: "ws-test", TaskID: 1, Operation: dispatch.OperationApply})["run_id"].(string)
	ofWiki := RepositoryResult{WikiUpdate: u.run, Applied: &repository.Applied{Authorization: merge, Commit: stCandidateCommit}}
	if _, err := RepositoryRecord(db, nil, "apply", merge, ofWiki); !wcRefused(err, "", refusal) {
		t.Fatalf("result of a wiki update for the merge of a task = %v, want a refusal", err)
	}
	if n := dfInt(t, db, `SELECT (SELECT archived FROM tasks WHERE id=1)+(SELECT finished FROM merge_operations WHERE run_id=?)`, merge); n != 0 || u.end() != wmActive {
		t.Fatalf("the refused result archived the task or finished its merge (%d), or ended the wiki update: %s", n, u.end())
	}
}
