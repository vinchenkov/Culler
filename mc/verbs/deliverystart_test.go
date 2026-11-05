package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"mc/dispatch"
	"mc/domain"
	"mc/repository"
)

// stTask inserts one task of the Project at the status.
func stTask(t *testing.T, db *sql.DB, id int64, project string, status dispatch.Status) {
	t.Helper()
	task := dvTask(id, dispatch.ScopeTask, status, 2)
	task.Project = project
	dvInsertTask(t, db, task)
}

// stEndedRun inserts one ended agent run of the task.
func stEndedRun(t *testing.T, db *sql.DB, task int64, run string) {
	t.Helper()
	dvExec(t, db, `INSERT INTO runs (id,tier,role,project,subject,ended_at)
		SELECT ?,'pipeline','worker',project,id,datetime('now') FROM tasks WHERE id=?`, run, task)
}

// stSubmission records a submitted version of an ended run of the task that
// no capture retained. It returns the producer run.
func stSubmission(t *testing.T, db *sql.DB, task int64) string {
	t.Helper()
	run := fmt.Sprintf("submission-%d", task)
	stEndedRun(t, db, task, run)
	dvExec(t, db, `INSERT INTO repository_runs (run_id,task_id,input_commit,destination_commit,temporary_bytes,captured,cleaned)
		VALUES (?,?,?,?,0,0,0)`, run, task, strings.Repeat("a", 40), strings.Repeat("a", 40))
	dvExec(t, db, `INSERT INTO version_submissions (run_id,task_id,input_commit,result_commit) VALUES (?,?,?,?)`,
		run, task, strings.Repeat("a", 40), strings.Repeat("b", 40))
	return run
}

// stCleanup records an ended run of the task whose temporary repository
// requires a cleanup.
func stCleanup(t *testing.T, db *sql.DB, task int64, run string) {
	t.Helper()
	stEndedRun(t, db, task, run)
	dvExec(t, db, `INSERT INTO repository_runs (run_id,task_id,input_commit,destination_commit,temporary_bytes,captured,cleaned)
		VALUES (?,?,?,?,0,0,0)`, run, task, strings.Repeat("a", 40), strings.Repeat("a", 40))
}

// stCandidateCommit is the merge commit of each merged fixture candidate.
var stCandidateCommit = strings.Repeat("e", 40)

// stMergedCandidate gives the packaged task a merged candidate of exact
// commits, so that its merge can start.
func stMergedCandidate(t *testing.T, db *sql.DB, task int64) {
	t.Helper()
	var branch string
	var version int64
	if err := db.QueryRow(`SELECT destination_branch,submitted_version FROM tasks WHERE id=?`, task).Scan(&branch, &version); err != nil {
		t.Fatal(err)
	}
	run := fmt.Sprintf("candidate-%d", task)
	stEndedRun(t, db, task, run)
	c := repository.Candidate{SubmittedCommit: strings.Repeat("b", 40), DestinationBranch: branch,
		DestinationCommit: strings.Repeat("a", 40), Commit: stCandidateCommit, Status: "merged"}
	body, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	dvExec(t, db, `INSERT INTO merge_candidates (task_id,run_id,version_id,destination_branch,destination_commit,candidate_commit,result,status,retained_ref)
		VALUES (?,?,?,?,?,?,?,'merged',?)`, task, run, version, branch, c.DestinationCommit, c.Commit, string(body), "refs/mc/fixture/"+run)
	dvExec(t, db, `UPDATE tasks SET candidate_id=(SELECT id FROM merge_candidates WHERE run_id=?) WHERE id=?`, run, task)
}

// stMergeable inserts a packaged task with a merged candidate of exact commits.
func stMergeable(t *testing.T, db *sql.DB, id int64) {
	t.Helper()
	stTask(t, db, id, "ws-test", dispatch.StatusPackaged)
	stMergedCandidate(t, db, id)
}

// stApprove records the operator approval of the packaged task.
func stApprove(t *testing.T, db *sql.DB, task int64) {
	t.Helper()
	at := time.Now()
	dvApproveTask(t, db, task, &at)
}

// stStart runs one start request of the owner.
func stStart(t *testing.T, db *sql.DB, owner int64, stopped ...string) StartResponse {
	t.Helper()
	resp, err := DeliveryStart(db, nil, StartRequest{Owner: owner, Stopped: stopped})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// stWantStarted fails unless the start response started exactly the operations, as operation/task.
func stWantStarted(t *testing.T, db *sql.DB, resp StartResponse, want ...string) {
	t.Helper()
	got := startedOperations(t, db, resp)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the start request started %v, want %v (failed %+v)", got, want, resp.Failed)
	}
}

// stRegister registers a new resident owner.
func stRegister(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	result, err := DeliveryRegister(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	return result.(map[string]any)["owner"].(int64)
}

// stFail reports the failure of an execution whose host work stopped.
func stFail(t *testing.T, db *sql.DB, run string, containerPresent bool) map[string]any {
	t.Helper()
	result, err := DeliveryFail(db, nil, run, "The repository operation failed.", containerPresent)
	if err != nil {
		t.Fatal(err)
	}
	return result.(map[string]any)
}

// stMergeState returns the merge state, its text, and the related task of the packet of the task.
func stMergeState(t *testing.T, db *sql.DB, task int64) (string, string, any) {
	t.Helper()
	result, err := PacketList(db, PacketSelection{Live: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, packet := range result.(map[string]any)["packets"].([]map[string]any) {
		if packet["task_id"] == task {
			state, _ := packet["merge_state"].(string)
			text, _ := packet["merge_state_text"].(string)
			return state, text, packet["merge_task_id"]
		}
	}
	t.Fatalf("no live packet of task %d", task)
	return "", "", nil
}

// stApplyRun returns the run of the unfinished apply of the task.
func stApplyRun(t *testing.T, db *sql.DB, task int64) string {
	t.Helper()
	return dfStr(t, db, `SELECT run_id FROM merge_operations WHERE task_id=? AND operation='apply' AND finished=0`, task)
}

func TestDeliveryStartWhileAgentRunHoldsLease(t *testing.T) {
	db := dvSpine(t)
	stTask(t, db, 1, "ws-test", dispatch.StatusSeeded)
	stTask(t, db, 2, "ws-test", dispatch.StatusSeeded)
	stTask(t, db, 3, "ws-test", dispatch.StatusPackaged)
	dvExec(t, db, `UPDATE tasks SET candidate_id=NULL WHERE id=3`)
	stCleanup(t, db, 1, "earlier-worker")
	producer := stSubmission(t, db, 2)
	subject := int64(1)
	if err := inTx(db, func(ctx context.Context, q Q) error {
		_, err := domain.Claim(ctx, q, time.Now(), domain.ClaimArgs{RunID: "active-worker", Project: "ws-test", Owner: "worker",
			SubjectID: &subject, SessionPath: "sessions/active-worker", Binding: "fake", HardDeadlineMinutes: 60})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	resp := startRequest(t, db, false)
	stWantStarted(t, db, resp, "capture/2", "prepare/3", "cleanup/1")
	if len(resp.Failed) != 0 || len(resp.Executions) != 3 {
		t.Fatalf("the start response is %+v", resp)
	}
	if got := executionEffect(t, db, resp.Started[0])["producer_run"]; got != producer {
		t.Fatalf("the capture names the producer run %v", got)
	}
	var run string
	var held int64
	if err := db.QueryRow(`SELECT run_id,subject FROM leases WHERE project='ws-test'`).Scan(&run, &held); err != nil {
		t.Fatal(err)
	}
	if run != "active-worker" || held != 1 {
		t.Fatalf("the lease changed to %s on task %d", run, held)
	}
	for _, started := range resp.Started {
		if n := dfInt(t, db, `SELECT COUNT(*) FROM leases WHERE run_id=?`, started); n != 0 {
			t.Fatalf("the repository operation %s holds a lease", started)
		}
		if binding := dfStr(t, db, `SELECT binding FROM runs WHERE id=?`, started); binding != "mechanical" {
			t.Fatalf("the repository operation %s is a %s run", started, binding)
		}
	}
}

func TestDeliveryStartCleanupLimit(t *testing.T) {
	db := dvSpine(t)
	dvAddProject(t, db, "ws-b", "active")
	dvAddProject(t, db, "ws-c", "active")
	for id, project := range map[int64]string{1: "ws-test", 2: "ws-test", 3: "ws-test", 4: "ws-b", 5: "ws-b", 6: "ws-c", 7: "ws-c"} {
		stTask(t, db, id, project, dispatch.StatusSeeded)
	}
	for _, c := range []struct {
		task int64
		run  string
	}{{1, "test-1"}, {2, "test-2"}, {3, "test-3"}, {4, "b-1"}, {5, "b-2"}, {6, "c-1"}, {7, "c-2"}} {
		stCleanup(t, db, c.task, c.run)
	}
	targets := func(resp StartResponse) []string {
		out := []string{}
		for _, run := range resp.Started {
			out = append(out, dfStr(t, db, `SELECT target FROM repository_executions WHERE run_id=?`, run))
		}
		return out
	}
	first := startRequest(t, db, false)
	if got, want := targets(first), []string{"b-1", "c-1", "test-1", "b-2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the first start request cleaned %v, want %v", got, want)
	}
	if got := targets(startRequest(t, db, false)); len(got) != 0 {
		t.Fatalf("a start request exceeded the cleanup limit: %v", got)
	}
	stFail(t, db, first.Started[0], false)
	stFail(t, db, first.Started[1], false)
	if got, want := targets(startRequest(t, db, false)), []string{"c-2", "test-2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the start request after two failures cleaned %v, want %v", got, want)
	}
	if n := dfInt(t, db, `SELECT COUNT(*) FROM repository_executions WHERE operation='cleanup' AND ended_at IS NULL`); n != dispatch.MaxCleanups {
		t.Fatalf("%d cleanups are active", n)
	}
}

func TestDeliveryStartIsolatesFailedStart(t *testing.T) {
	db := dvSpine(t)
	// The fixture candidate of task 1 has no exact commits, so its merge start fails.
	task := dvTask(1, dispatch.ScopeTask, dispatch.StatusPackaged, 2)
	task.Decision, task.DecidedAt = dispatch.DecisionApproved, &dvOld
	dvInsertTask(t, db, task)
	stTask(t, db, 2, "ws-test", dispatch.StatusSeeded)
	stTask(t, db, 3, "ws-test", dispatch.StatusSeeded)
	stSubmission(t, db, 2)
	stCleanup(t, db, 3, "worker-3")
	clocks := fixClocks(t, db)

	resp := startRequest(t, db, false)
	stWantStarted(t, db, resp, "capture/2", "cleanup/3")
	if len(resp.Failed) != 1 {
		t.Fatalf("the start response lists the failures %+v", resp.Failed)
	}
	failed := resp.Failed[0]
	if failed.Project != "ws-test" || failed.TaskID != 1 || failed.Operation != "apply" || !failed.Recorded || failed.RunID == "" ||
		failed.Code != "domain-rejection" || !strings.Contains(failed.Message, "exact commits") {
		t.Fatalf("the failed start is %+v", failed)
	}
	var reason, retry string
	var charged, stopped bool
	if err := db.QueryRow(`SELECT end_reason,retry_after,charged,stopped_at IS NOT NULL FROM repository_executions WHERE run_id=?`, failed.RunID).
		Scan(&reason, &retry, &charged, &stopped); err != nil {
		t.Fatal(err)
	}
	if reason != domain.EndFailure || !charged || !stopped || retry != clocks.now.Add(domain.RetryDelay).UTC().Format(spineTime) {
		t.Fatalf("the failed execution ended with %s, charge %v, stop %v, retry %s", reason, charged, stopped, retry)
	}
	if n := dfInt(t, db, `SELECT dispatch_retries FROM tasks WHERE id=1`); n != domain.InfraBudget-1 {
		t.Fatalf("the failed start left %d retries", n)
	}
	again := startRequest(t, db, false)
	stWantStarted(t, db, again)
	if len(again.Failed) != 0 || again.DueInMS == nil || *again.DueInMS != domain.RetryDelay.Milliseconds() {
		t.Fatalf("the merge started again during its retry time: %+v", again)
	}
}

func TestFailedStartStallsAfterThreeCharges(t *testing.T) {
	db := dvSpine(t)
	task := dvTask(1, dispatch.ScopeTask, dispatch.StatusPackaged, 2)
	task.Decision, task.DecidedAt = dispatch.DecisionApproved, &dvOld
	dvInsertTask(t, db, task)
	clocks := fixClocks(t, db)
	for charge := 1; charge <= domain.InfraBudget; charge++ {
		resp := startRequest(t, db, false)
		if len(resp.Failed) != 1 || !resp.Failed[0].Recorded {
			t.Fatalf("start %d did not record a failed execution: %+v", charge, resp.Failed)
		}
		clocks.advance(domain.RetryDelay + time.Second)
	}
	if n := dfInt(t, db, `SELECT dispatch_retries FROM tasks WHERE id=1`); n != 0 {
		t.Fatalf("three failed starts left %d retries", n)
	}
	resp := startRequest(t, db, false)
	stWantStarted(t, db, resp)
	if len(resp.Failed) != 0 || resp.DueInMS != nil {
		t.Fatalf("the stalled task received another merge start: %+v", resp)
	}
	state, text, _ := stMergeState(t, db, 1)
	if state != "blocked" || text != mergeTextStall {
		t.Fatalf("the merge state of the stalled task is %s: %s", state, text)
	}
}

func TestAuthorizationHoldStopsOtherApply(t *testing.T) {
	db := dvSpine(t)
	stMergeable(t, db, 7)
	stMergeable(t, db, 9)
	stApprove(t, db, 9)
	resp := startRequest(t, db, false)
	stWantStarted(t, db, resp, "apply/9")
	if _, err := RepositoryRecord(db, nil, "apply", resp.Started[0], RepositoryResult{TaskID: 9, Status: "operator-attention",
		OperatorAction: "The operator checkout has uncommitted changes."}); err != nil {
		t.Fatal(err)
	}
	if kind := dfStr(t, db, `SELECT blocked_kind FROM tasks WHERE id=9`); kind != domain.BlockCheckout {
		t.Fatalf("the operator checkout condition has the condition type %q", kind)
	}
	stApprove(t, db, 7)
	stWantStarted(t, db, startRequest(t, db, false))
	state, text, related := stMergeState(t, db, 7)
	if state != "after-task" || related != int64(9) || text != "Culler merges task #9 first. Task #9 needs your attention." {
		t.Fatalf("the merge state of task 7 is %s (%v): %s", state, related, text)
	}
}

func TestFailedReconcileHoldsProjectMergeSteps(t *testing.T) {
	db := dvSpine(t)
	stMergeable(t, db, 1)
	stMergeable(t, db, 2)
	clocks := fixClocks(t, db)
	stApprove(t, db, 2)
	resp := startRequest(t, db, false)
	stWantStarted(t, db, resp, "apply/2")
	apply := resp.Started[0]
	stFail(t, db, apply, false)
	stApprove(t, db, 1)
	stWantStarted(t, db, startRequest(t, db, false))
	if state, _, related := stMergeState(t, db, 1); state != "recovery" || related != int64(2) {
		t.Fatalf("the merge state of task 1 is %s (%v)", state, related)
	}

	clocks.advance(domain.RetryDelay + time.Second)
	resp = startRequest(t, db, false)
	stWantStarted(t, db, resp, "reconcile/2")
	if got := executionEffect(t, db, resp.Started[0])["previous_run"]; got != apply {
		t.Fatalf("the receipt recovery names %v", got)
	}
	stFail(t, db, resp.Started[0], false)
	resp = startRequest(t, db, false)
	stWantStarted(t, db, resp)
	if resp.DueInMS == nil || *resp.DueInMS != domain.RetryDelay.Milliseconds() {
		t.Fatalf("the retry time of the receipt recovery gives the due time %v", resp.DueInMS)
	}
	if state, text, related := stMergeState(t, db, 1); state != "recovery" || related != int64(2) || text != mergeTextRecovery {
		t.Fatalf("the merge state of task 1 is %s (%v): %s", state, related, text)
	}
	clocks.advance(domain.RetryDelay + time.Second)
	stWantStarted(t, db, startRequest(t, db, false), "reconcile/2")
}

func TestOwnerChangeStartsNoMergeStepBeforeStop(t *testing.T) {
	for _, operation := range []string{"prepare", "apply"} {
		t.Run(operation, func(t *testing.T) {
			db := dvSpine(t)
			stMergeable(t, db, 1)
			if operation == "prepare" {
				dvExec(t, db, `UPDATE tasks SET candidate_id=NULL WHERE id=1`)
			} else {
				stMergeable(t, db, 2)
				stApprove(t, db, 1)
				stApprove(t, db, 2)
			}
			clocks := fixClocks(t, db)
			earlier := testOwner(t, db)
			resp := startRequest(t, db, false)
			stWantStarted(t, db, resp, operation+"/1")
			run := resp.Started[0]

			owner := stRegister(t, db)
			clocks.advance(70 * time.Second)
			resp = stStart(t, db, owner)
			stWantStarted(t, db, resp)
			var reason string
			var charged bool
			if err := db.QueryRow(`SELECT end_reason,charged FROM repository_executions WHERE run_id=?`, run).Scan(&reason, &charged); err != nil {
				t.Fatal(err)
			}
			if reason != domain.EndOwnerChange || charged {
				t.Fatalf("the owner change ended the %s with %s and charge %v", operation, reason, charged)
			}
			wait := (10*time.Minute - 70*time.Second).Milliseconds()
			if want := []ExecutionStop{{RunID: run, Project: "ws-test", Operation: operation, Owner: earlier, WaitMS: wait}}; !reflect.DeepEqual(resp.Stops, want) {
				t.Fatalf("the stops are %+v, want %+v", resp.Stops, want)
			}
			resp = stStart(t, db, owner, run)
			stWantStarted(t, db, resp)
			if len(resp.Confirmed) != 0 {
				t.Fatalf("the stop of the earlier owner was confirmed after 70 seconds: %v", resp.Confirmed)
			}

			clocks.advance(20 * time.Second)
			resp = stStart(t, db, owner, run)
			stWantStarted(t, db, resp)
			if !reflect.DeepEqual(resp.Confirmed, []string{run}) || len(resp.Stops) != 0 {
				t.Fatalf("the stop after 90 seconds: confirmed %v, stops %+v", resp.Confirmed, resp.Stops)
			}

			clocks.advance(41 * time.Second)
			resp = stStart(t, db, owner)
			want := "reconcile/1"
			if operation == "prepare" {
				want = "prepare/1"
			}
			stWantStarted(t, db, resp, want)
			if got := executionEffect(t, db, resp.Started[0])["previous_run"]; got != run {
				t.Fatalf("the recovery names %v", got)
			}
		})
	}
}

func TestEarlierOwnerRefused(t *testing.T) {
	db := dvSpine(t)
	stTask(t, db, 1, "ws-test", dispatch.StatusSeeded)
	stSubmission(t, db, 1)
	earlier := testOwner(t, db)
	stRegister(t, db)
	refused := func(err error) {
		t.Helper()
		var refusal *DomainError
		if !errors.As(err, &refusal) || refusal.Code != domain.CodeStaleOwner {
			t.Fatalf("the earlier owner received %v", err)
		}
	}
	_, err := DeliveryStart(db, nil, StartRequest{Owner: earlier})
	refused(err)
	_, err = DeliveryHeartbeat(db, nil, HeartbeatRequest{Owner: earlier})
	refused(err)
	if n := dfInt(t, db, `SELECT COUNT(*) FROM repository_executions`); n != 0 {
		t.Fatalf("the earlier owner started %d executions", n)
	}
}

func TestStartResponseListsActiveExecutions(t *testing.T) {
	db := dvSpine(t)
	stTask(t, db, 1, "ws-test", dispatch.StatusSeeded)
	producer := stSubmission(t, db, 1)
	clocks := fixClocks(t, db)
	first := startRequest(t, db, false)
	stWantStarted(t, db, first, "capture/1")
	run := first.Started[0]

	clocks.advance(10 * time.Second)
	second := startRequest(t, db, false)
	stWantStarted(t, db, second)
	if len(second.Executions) != 1 {
		t.Fatalf("the second start response lists %d executions", len(second.Executions))
	}
	body, err := json.Marshal(second.Executions[0])
	if err != nil {
		t.Fatal(err)
	}
	entry := decodeEffect(t, body)
	ended, err := parseSpineTime(dfStr(t, db, `SELECT ended_at FROM runs WHERE id=?`, producer))
	if err != nil {
		t.Fatal(err)
	}
	grace := ended.Add(domain.ContainerGrace).Sub(clocks.now).Milliseconds()
	if entry["run_id"] != run || entry["operation"] != "capture" || entry["task_id"] != int64(1) || entry["producer_run"] != producer ||
		entry["deadline_ms"] != (5*time.Minute-10*time.Second).Milliseconds() || entry["container_grace_ms"] != max(grace, 0) {
		t.Fatalf("the listed execution is %v", entry)
	}
}

func TestHeartbeatRenewsAndListsUnstartedExecutions(t *testing.T) {
	db := dvSpine(t)
	stTask(t, db, 1, "ws-test", dispatch.StatusSeeded)
	stTask(t, db, 2, "ws-test", dispatch.StatusSeeded)
	stCleanup(t, db, 1, "worker-1")
	stCleanup(t, db, 2, "worker-2")
	clocks := fixClocks(t, db)
	owner := testOwner(t, db)
	resp := startRequest(t, db, false)
	stWantStarted(t, db, resp, "cleanup/1", "cleanup/2")
	running, unstarted := resp.Started[0], resp.Started[1]

	clocks.advance(60 * time.Second)
	beat, err := DeliveryHeartbeat(db, nil, HeartbeatRequest{Owner: owner, Running: []string{running}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beat.Renewed, []string{running}) || !reflect.DeepEqual(beat.Unstarted, []string{unstarted}) || len(beat.Ended) != 0 {
		t.Fatalf("the heartbeat response is %+v", beat)
	}
	clocks.advance(30 * time.Second)
	beat, err = DeliveryHeartbeat(db, nil, HeartbeatRequest{Owner: owner, Running: []string{running}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beat.Ended, []string{unstarted}) || !reflect.DeepEqual(beat.Renewed, []string{running}) {
		t.Fatalf("the heartbeat after 90 seconds is %+v", beat)
	}
	if reason := dfStr(t, db, `SELECT end_reason FROM repository_executions WHERE run_id=?`, unstarted); reason != domain.EndHeartbeat {
		t.Fatalf("the silent execution ended with %s", reason)
	}
	if !reflect.DeepEqual(beat.Stops, []ExecutionStop{{RunID: unstarted, Project: "ws-test", Operation: "cleanup", Owner: owner}}) {
		t.Fatalf("the heartbeat stops are %+v", beat.Stops)
	}
	beat, err = DeliveryHeartbeat(db, nil, HeartbeatRequest{Owner: owner, Running: []string{running}, Stopped: []string{unstarted}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beat.Confirmed, []string{unstarted}) || len(beat.Stops) != 0 || len(beat.Unstarted) != 0 {
		t.Fatalf("the heartbeat with the stop is %+v", beat)
	}
}

func TestHeartbeatListsContainerStops(t *testing.T) {
	db := dvSpine(t)
	stTask(t, db, 1, "ws-test", dispatch.StatusSeeded)
	clocks := fixClocks(t, db)
	owner := testOwner(t, db)
	stamp := func(d time.Duration) string { return clocks.now.Add(-d).UTC().Format(spineTime) }
	for _, run := range []struct {
		id, binding string
		ended       any
	}{
		{"ended-strategist", "fake", stamp(61 * time.Second)},
		{"grace-worker", "fake", stamp(30 * time.Second)},
		{"live-worker", "fake", nil},
		{"ended-operation", "mechanical", stamp(120 * time.Second)},
	} {
		dvExec(t, db, `INSERT INTO runs (id,tier,role,project,subject,binding,ended_at) VALUES (?,'pipeline','worker','ws-test',1,?,?)`,
			run.id, run.binding, run.ended)
	}
	beat, err := DeliveryHeartbeat(db, nil, HeartbeatRequest{Owner: owner,
		Containers: []string{"grace-worker", "ended-strategist", "live-worker", "ended-operation", "absent-run", "ended-strategist"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []ContainerStop{{RunID: "ended-strategist", Project: "ws-test"}}; !reflect.DeepEqual(beat.ContainerStops, want) {
		t.Fatalf("the container stops are %+v, want %+v", beat.ContainerStops, want)
	}
	clocks.advance(31 * time.Second)
	beat, err = DeliveryHeartbeat(db, nil, HeartbeatRequest{Owner: owner, Containers: []string{"grace-worker"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []ContainerStop{{RunID: "grace-worker", Project: "ws-test"}}; !reflect.DeepEqual(beat.ContainerStops, want) {
		t.Fatalf("the container stops after the grace are %+v", beat.ContainerStops)
	}
}

func TestFailureReportInContainerGraceHasNoCharge(t *testing.T) {
	db := dvSpine(t)
	stTask(t, db, 1, "ws-test", dispatch.StatusSeeded)
	stTask(t, db, 2, "ws-test", dispatch.StatusSeeded)
	stCleanup(t, db, 1, "recent-worker")
	dvExec(t, db, `INSERT INTO runs (id,tier,role,project,subject,ended_at) VALUES ('earlier-worker','pipeline','worker','ws-test',2,datetime('now','-120 seconds'))`)
	dvExec(t, db, `INSERT INTO repository_runs (run_id,task_id,input_commit,destination_commit,temporary_bytes,captured,cleaned)
		VALUES ('earlier-worker',2,?,?,0,0,0)`, strings.Repeat("a", 40), strings.Repeat("a", 40))
	resp := startRequest(t, db, false)
	stWantStarted(t, db, resp, "cleanup/1", "cleanup/2")
	inGrace := stFail(t, db, resp.Started[0], true)
	if inGrace["charged"] != false || inGrace["retry_after"] == nil || inGrace["remaining"] != domain.InfraBudget {
		t.Fatalf("a container in its grace charged the task: %v", inGrace)
	}
	afterGrace := stFail(t, db, resp.Started[1], true)
	if afterGrace["charged"] != true || afterGrace["retry_after"] == nil || afterGrace["remaining"] != domain.InfraBudget-1 {
		t.Fatalf("a container after its grace received no charge: %v", afterGrace)
	}
	if reason := dfStr(t, db, `SELECT end_reason FROM repository_executions WHERE run_id=?`, resp.Started[0]); reason != domain.EndContainerPresent {
		t.Fatalf("the report ended the execution with %s", reason)
	}
}

func TestStartResponseDueTime(t *testing.T) {
	quiet := func(t *testing.T, wait time.Duration) (*sql.DB, *testClocks) {
		db := dvSpine(t)
		stMergeable(t, db, 1)
		stApprove(t, db, 1)
		clocks := fixClocks(t, db)
		dvExec(t, db, `UPDATE tasks SET merge_after=? WHERE id=1`, clocks.now.Add(wait).UTC().Format(spineTime))
		return db, clocks
	}
	t.Run("quiet interval", func(t *testing.T) {
		db, clocks := quiet(t, 42*time.Second)
		resp := startRequest(t, db, false)
		stWantStarted(t, db, resp)
		if resp.DueInMS == nil || *resp.DueInMS != 42000 {
			t.Fatalf("the due time is %v", resp.DueInMS)
		}
		clocks.advance(42 * time.Second)
		stWantStarted(t, db, startRequest(t, db, false), "observe/1")
	})
	t.Run("minimum", func(t *testing.T) {
		db, clocks := quiet(t, time.Second)
		clocks.advance(500 * time.Millisecond)
		if resp := startRequest(t, db, false); resp.DueInMS == nil || *resp.DueInMS != 1000 {
			t.Fatalf("the due time is %v", resp.DueInMS)
		}
	})
	t.Run("none", func(t *testing.T) {
		db := dvSpine(t)
		stTask(t, db, 1, "ws-test", dispatch.StatusSeeded)
		if resp := startRequest(t, db, false); resp.DueInMS != nil || resp.DrainPending {
			t.Fatalf("the idle start response is %+v", resp)
		}
	})
}

// stPendingRevisionDuringPrepare starts the merge candidate step of task 1
// and requests a revision of its requirements. It returns the prepare run.
func stPendingRevisionDuringPrepare(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db := dvSpine(t)
	stTask(t, db, 1, "ws-test", dispatch.StatusPackaged)
	dvExec(t, db, `UPDATE tasks SET candidate_id=NULL WHERE id=1`)
	resp := startRequest(t, db, false)
	stWantStarted(t, db, resp, "prepare/1")
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var version int64
		if err := q.QueryRowContext(ctx, `SELECT record_version FROM tasks WHERE id=1`).Scan(&version); err != nil {
			return err
		}
		_, err := domain.RequestRequirementsRevision(ctx, q, 1, version, "revise-1", "The revised requirements.", "The operator changed the scope.", "operator")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return db, resp.Started[0]
}

func TestStartRequestSucceedsWithPendingRevisionDuringPrepare(t *testing.T) {
	db, run := stPendingRevisionDuringPrepare(t)
	clocks := fixClocks(t, db)
	resp := startRequest(t, db, false)
	stWantStarted(t, db, resp)
	if state := dfStr(t, db, `SELECT state FROM requirements_revisions WHERE task_id=1`); state != "pending" {
		t.Fatalf("the revision is %s during the merge candidate step", state)
	}
	if len(resp.Executions) != 1 {
		t.Fatalf("the merge candidate step is not active: %+v", resp.Executions)
	}
	stFail(t, db, run, false)
	startRequest(t, db, false)
	if state := dfStr(t, db, `SELECT state FROM requirements_revisions WHERE task_id=1`); state != "applied" {
		t.Fatalf("the revision is %s after the merge candidate step", state)
	}
	if status := dfStr(t, db, `SELECT status FROM tasks WHERE id=1`); status != "seeded" {
		t.Fatalf("the revised task is %s", status)
	}
	clocks.advance(domain.RetryDelay + time.Second)
	resp = startRequest(t, db, false)
	if len(resp.Failed) != 0 {
		t.Fatalf("the interrupted merge candidate step of the revised task failed again: %+v", resp.Failed)
	}
	stWantStarted(t, db, resp, "prepare/1")
	if _, err := RepositoryRecord(db, nil, "prepare", resp.Started[0], RepositoryResult{TaskID: 1, Candidate: &repository.Candidate{}}); err != nil {
		t.Fatal(err)
	}
	if outcome := dfStr(t, db, `SELECT outcome FROM runs WHERE id=?`, resp.Started[0]); outcome != "obsolete" {
		t.Fatalf("the prepare recovery of the revised task ended as %s", outcome)
	}
	stWantStarted(t, db, startRequest(t, db, false))
	if n := dfInt(t, db, `SELECT dispatch_retries FROM tasks WHERE id=1`); n != domain.InfraBudget-1 {
		t.Fatalf("the revised task has %d retries", n)
	}
}

func TestBoardWaitReasonNamesRepositoryOperation(t *testing.T) {
	db, run := stPendingRevisionDuringPrepare(t)
	record, err := TaskGet(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	revision, _ := record.(map[string]any)["pending_revision"].(map[string]any)
	if revision == nil || revision["wait_reason"] != "mc saved your revision request. A repository operation will finish before mc applies your changes." {
		t.Fatalf("the pending revision is %v", revision)
	}
	// The board shows no repository operation as a run.
	if active, ok := revision["active_run_id"]; ok {
		t.Fatalf("the pending revision names the repository operation %s (%v) as its active run", run, active)
	}
}

func TestCleanupErrorChargesAndRetries(t *testing.T) {
	db := dvSpine(t)
	stTask(t, db, 1, "ws-test", dispatch.StatusSeeded)
	stCleanup(t, db, 1, "worker-1")
	clocks := fixClocks(t, db)
	resp := startRequest(t, db, false)
	stWantStarted(t, db, resp, "cleanup/1")
	run := resp.Started[0]
	if _, err := RepositoryRecord(db, nil, "cleanup", run, RepositoryResult{TaskID: 1, CleanupError: "The directory is busy.", TemporaryBytes: 7}); err != nil {
		t.Fatal(err)
	}
	var reason, retry string
	var charged bool
	if err := db.QueryRow(`SELECT end_reason,retry_after,charged FROM repository_executions WHERE run_id=?`, run).Scan(&reason, &retry, &charged); err != nil {
		t.Fatal(err)
	}
	if reason != domain.EndFailure || !charged || retry != clocks.now.Add(domain.RetryDelay).UTC().Format(spineTime) {
		t.Fatalf("the cleanup error ended with %s, charge %v, retry %s", reason, charged, retry)
	}
	var cleaned bool
	var condition string
	if err := db.QueryRow(`SELECT cleaned,cleanup_error FROM repository_runs WHERE run_id='worker-1'`).Scan(&cleaned, &condition); err != nil {
		t.Fatal(err)
	}
	if cleaned || condition != "The directory is busy." {
		t.Fatalf("the repository is cleaned %v with the error %q", cleaned, condition)
	}
	if n := dfInt(t, db, `SELECT dispatch_retries FROM tasks WHERE id=1`); n != domain.InfraBudget-1 {
		t.Fatalf("the cleanup error left %d retries", n)
	}
	stWantStarted(t, db, startRequest(t, db, false))
	clocks.advance(domain.RetryDelay)
	stWantStarted(t, db, startRequest(t, db, false), "cleanup/1")
}

func TestCancelledTaskCaptureStallsAndReleasesAgentRuns(t *testing.T) {
	db := dvSpine(t)
	stTask(t, db, 1, "ws-test", dispatch.StatusSeeded)
	stSubmission(t, db, 1)
	stTask(t, db, 2, "ws-test", dispatch.StatusSeeded)
	if err := inTx(db, func(ctx context.Context, q Q) error {
		return domain.Cancel(ctx, q, 1, "The operator cancelled the task.", "operator")
	}); err != nil {
		t.Fatal(err)
	}
	clocks := fixClocks(t, db)
	for n := 1; n <= domain.InfraBudget; n++ {
		resp := startRequest(t, db, false)
		stWantStarted(t, db, resp, "capture/1")
		stFail(t, db, resp.Started[0], false)
		clocks.advance(domain.RetryDelay)
	}
	if n := dfInt(t, db, `SELECT stalled FROM tasks WHERE id=1`); n != 1 {
		t.Fatal("three failed captures did not stall the cancelled task")
	}
	stWantStarted(t, db, startRequest(t, db, false))
	var records dispatch.Records
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		records, err = loadVisionRecords(ctx, q, "ws-test")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	action := dispatch.Decide(records, dispatch.Lease{}, dispatch.DefaultConfig(), dispatch.Clock{Now: clocks.now})
	if action.Kind != dispatch.KindSpawn || action.Spawn.SubjectID == nil || *action.Spawn.SubjectID != 2 {
		t.Fatalf("the capture of the cancelled task holds the agent runs: %+v", action)
	}
}
