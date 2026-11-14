package domain_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"mc/domain"
)

var (
	bootA     = domain.VMClock{BootID: "boot-a", Uptime: time.Hour}
	spineNow  = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	retryTime = "2026-09-28 12:01:00"
)

func after(clock domain.VMClock, d time.Duration) domain.VMClock {
	return domain.VMClock{BootID: clock.BootID, Uptime: clock.Uptime + d}
}

func registerOwner(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var owner int64
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		var err error
		owner, err = domain.RegisterOwner(ctx, q)
		return err
	})
	return owner
}

// execution describes one execution of the fixture Project. The merge
// candidate step and the merge have the 10-minute deadline.
func execution(run string, task int64, operation, target string, owner int64, clock domain.VMClock) domain.ExecutionStart {
	deadline := 5 * time.Minute
	if operation == "prepare" || operation == "apply" {
		deadline = 10 * time.Minute
	}
	return domain.ExecutionStart{RunID: run, Project: "ws", TaskID: task, Operation: operation, Target: target,
		Owner: owner, Effect: fmt.Sprintf(`{"run_id":%q}`, run), Deadline: deadline, Clock: clock}
}

func startExecution(t *testing.T, db *sql.DB, s domain.ExecutionStart) {
	t.Helper()
	mustTx(t, db, func(ctx context.Context, q domain.Q) error { return domain.StartExecution(ctx, q, s) })
}

func endExecution(t *testing.T, db *sql.DB, end domain.ExecutionEnd) domain.ExecutionEndResult {
	t.Helper()
	var result domain.ExecutionEndResult
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		var err error
		result, err = domain.EndExecution(ctx, q, end)
		return err
	})
	return result
}

func sweep(t *testing.T, db *sql.DB, owner int64, clock domain.VMClock) []string {
	t.Helper()
	var ended []string
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		var err error
		ended, err = domain.SweepExecutions(ctx, q, owner, clock, spineNow)
		return err
	})
	return ended
}

func heartbeat(t *testing.T, db *sql.DB, owner int64, runs []string, clock domain.VMClock) []string {
	t.Helper()
	var renewed []string
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		var err error
		renewed, err = domain.HeartbeatExecutions(ctx, q, owner, runs, clock)
		return err
	})
	return renewed
}

func confirmStops(t *testing.T, db *sql.DB, owner int64, runs []string, clock domain.VMClock) []string {
	t.Helper()
	var confirmed []string
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		var err error
		confirmed, err = domain.ConfirmStops(ctx, q, owner, runs, clock, spineNow)
		return err
	})
	return confirmed
}

func executionStr(t *testing.T, db *sql.DB, run, col string) string {
	t.Helper()
	return oneStr(t, db, `SELECT `+col+` FROM repository_executions WHERE run_id=?`, run)
}

func wantRuns(t *testing.T, name string, got []string, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

// wantEnd checks the recorded end of an execution and of its run.
func wantEnd(t *testing.T, db *sql.DB, run, reason, retry string, charged int64, outcome, runReason string) {
	t.Helper()
	if got := executionStr(t, db, run, "end_reason"); got != reason {
		t.Fatalf("end reason = %s, want %s", got, reason)
	}
	if got := executionStr(t, db, run, "retry_after"); got != retry {
		t.Fatalf("retry time = %s, want %s", got, retry)
	}
	if got := oneInt(t, db, `SELECT charged FROM repository_executions WHERE run_id=?`, run); got != charged {
		t.Fatalf("charged = %d, want %d", got, charged)
	}
	if got := oneStr(t, db, `SELECT outcome || ':' || COALESCE(end_reason,'') FROM runs WHERE id=? AND ended_at IS NOT NULL`, run); got != outcome+":"+runReason {
		t.Fatalf("run end = %s, want %s:%s", got, outcome, runReason)
	}
}

func failureCount(t *testing.T, db *sql.DB, run string) int64 {
	t.Helper()
	return oneInt(t, db, `SELECT COUNT(*) FROM infrastructure_failures WHERE run_id=?`, run)
}

func TestStaleOwnerCodeIsTheWireLiteral(t *testing.T) {
	if domain.CodeStaleOwner != "stale-owner" {
		t.Fatalf("CodeStaleOwner = %q, want the wire literal \"stale-owner\"", domain.CodeStaleOwner)
	}
}

func TestStartExecutionRefusesEarlierOwnerAndOtherProject(t *testing.T) {
	db := openSpine(t)
	mkProject(t, db, "ws2")
	task := mkTask(t, db, "task", "seeded")
	earlier := registerOwner(t, db)
	current := registerOwner(t, db)
	if current != earlier+1 {
		t.Fatalf("owners = %d, %d, want increasing numbers", earlier, current)
	}
	for _, owner := range []int64{earlier, current + 1} {
		wantCode(t, db, domain.CodeStaleOwner, func(ctx context.Context, q domain.Q) error {
			return domain.RequireCurrentOwner(ctx, q, owner)
		})
	}
	mustTx(t, db, func(ctx context.Context, q domain.Q) error { return domain.RequireCurrentOwner(ctx, q, current) })
	wantCode(t, db, domain.CodeStaleOwner, func(ctx context.Context, q domain.Q) error {
		return domain.StartExecution(ctx, q, execution("earlier", task, "capture", "producer", earlier, bootA))
	})
	other := execution("other-project", task, "capture", "producer", current, bootA)
	other.Project = "ws2"
	wantCode(t, db, domain.CodeProjectMismatch, func(ctx context.Context, q domain.Q) error {
		return domain.StartExecution(ctx, q, other)
	})
	if got := oneInt(t, db, `SELECT COUNT(*) FROM runs WHERE binding='mechanical'`); got != 0 {
		t.Fatalf("refused starts wrote %d runs", got)
	}
}

// The start is the first heartbeat. The execution stays active through a
// tick longer than 60 seconds while the resident renews it.
func TestStartExecutionRecordsFirstHeartbeat(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	owner := registerOwner(t, db)
	startExecution(t, db, execution("capture", task, "capture", "producer", owner, bootA))
	if got := executionStr(t, db, "capture", "heartbeat_uptime_ms"); got != fmt.Sprint(bootA.Uptime.Milliseconds()) {
		t.Fatalf("first heartbeat = %s, want the start uptime", got)
	}
	if got := oneInt(t, db, `SELECT deadline_uptime_ms-heartbeat_uptime_ms FROM repository_executions WHERE run_id='capture'`); got != 300000 {
		t.Fatalf("deadline = start + %d ms, want 5 minutes", got)
	}
	if got := oneStr(t, db, `SELECT boot_id||':'||owner||':'||target||':'||effect FROM repository_executions WHERE run_id='capture'`); got != fmt.Sprintf(`boot-a:%d:producer:{"run_id":"capture"}`, owner) {
		t.Fatalf("execution = %s", got)
	}
	if got := oneStr(t, db, `SELECT tier||':'||binding||':'||subject||':'||COALESCE(ended_at,'active') FROM runs WHERE id='capture'`); got != fmt.Sprintf("pipeline:mechanical:%d:active", task) {
		t.Fatalf("run = %s", got)
	}
	if got := oneStr(t, db, `SELECT run_id FROM leases WHERE project='ws'`); got != "<NULL>" {
		t.Fatalf("the execution claimed the Project lease: %s", got)
	}
	var fenced domain.ActiveExecution
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		var err error
		fenced, err = domain.FenceExecution(ctx, q, "capture")
		return err
	})
	if fenced.RunID != "capture" || fenced.Project != "ws" || fenced.Operation != "capture" || fenced.Target != "producer" ||
		fenced.TaskID != task || fenced.Owner != owner || fenced.StartedAt.IsZero() {
		t.Fatalf("fenced execution = %+v", fenced)
	}

	wantRuns(t, "ended before a heartbeat", sweep(t, db, owner, after(bootA, 89*time.Second)))
	wantRuns(t, "renewed", heartbeat(t, db, owner, []string{"capture"}, after(bootA, 80*time.Second)), "capture")
	wantRuns(t, "ended during a long tick", sweep(t, db, owner, after(bootA, 120*time.Second)))
	if got := executionStr(t, db, "capture", "ended_at"); got != "<NULL>" {
		t.Fatalf("the renewed execution ended at %s", got)
	}
}

func TestHeartbeatRenewsOwnActiveExecutionsOnCurrentBoot(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	earlier := registerOwner(t, db)
	startExecution(t, db, execution("earlier", task, "cleanup", "repository-1", earlier, bootA))
	owner := registerOwner(t, db)
	startExecution(t, db, execution("current", task, "capture", "producer", owner, bootA))
	startExecution(t, db, execution("ended", task, "cleanup", "repository-2", owner, bootA))
	endExecution(t, db, domain.ExecutionEnd{RunID: "ended", Reason: "result", Stopped: true, Now: spineNow})
	renewed := heartbeat(t, db, owner, []string{"current", "current", "earlier", "ended", "absent"}, after(bootA, time.Minute))
	wantRuns(t, "renewed", renewed, "current")
	if got := executionStr(t, db, "current", "heartbeat_uptime_ms"); got != fmt.Sprint(after(bootA, time.Minute).Uptime.Milliseconds()) {
		t.Fatalf("heartbeat = %s", got)
	}
	if got := executionStr(t, db, "current", "deadline_uptime_ms"); got != fmt.Sprint((bootA.Uptime + 5*time.Minute).Milliseconds()) {
		t.Fatalf("the heartbeat moved the deadline: %s", got)
	}
	other := domain.VMClock{BootID: "boot-b", Uptime: time.Minute}
	wantRuns(t, "renewed on another boot", heartbeat(t, db, owner, []string{"current"}, other))
}

func TestEndExecutionRecordsResult(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	mkEndedRun(t, db, "producer", "worker", task, "base")
	owner := registerOwner(t, db)
	startExecution(t, db, execution("capture", task, "capture", "producer", owner, bootA))
	mustExec(t, db, `INSERT INTO repository_transfers(run_id,producer_run,task_id) VALUES('capture','producer',?)`, task)
	wantCode(t, db, domain.CodeInvalidRequest, func(ctx context.Context, q domain.Q) error {
		_, err := domain.EndExecution(ctx, q, domain.ExecutionEnd{RunID: "capture", Reason: "result", Outcome: "infra-failed", Now: spineNow})
		return err
	})
	wantCode(t, db, domain.CodeInvalidRequest, func(ctx context.Context, q domain.Q) error {
		_, err := domain.EndExecution(ctx, q, domain.ExecutionEnd{RunID: "capture", Reason: "reaped", Now: spineNow})
		return err
	})
	result := endExecution(t, db, domain.ExecutionEnd{RunID: "capture", Reason: "result", Stopped: true, Now: spineNow})
	if result.Charged || result.RetryAfter != nil || result.TaskID != task || result.Remaining != 3 || result.Stalled {
		t.Fatalf("result = %+v", result)
	}
	wantEnd(t, db, "capture", "result", "<NULL>", 0, "completed", "")
	if got := executionStr(t, db, "capture", "stopped_at"); got != "2026-09-28 12:00:00" {
		t.Fatalf("stop = %s", got)
	}
	if got := oneInt(t, db, `SELECT finished FROM repository_transfers WHERE run_id='capture'`); got != 1 {
		t.Fatal("the capture end left its transfer unfinished")
	}
	for _, fn := range []func(ctx context.Context, q domain.Q) error{
		func(ctx context.Context, q domain.Q) error {
			_, err := domain.EndExecution(ctx, q, domain.ExecutionEnd{RunID: "capture", Reason: "result", Now: spineNow})
			return err
		},
		func(ctx context.Context, q domain.Q) error {
			_, err := domain.FenceExecution(ctx, q, "capture")
			return err
		},
	} {
		wantCode(t, db, domain.CodeStaleRun, fn)
	}

	startExecution(t, db, execution("prepare", task, "prepare", "task/1", owner, bootA))
	endExecution(t, db, domain.ExecutionEnd{RunID: "prepare", Reason: "result", Outcome: "obsolete", Now: spineNow})
	wantEnd(t, db, "prepare", "result", "<NULL>", 0, "obsolete", "")
	if got := executionStr(t, db, "prepare", "stopped_at"); got != "<NULL>" {
		t.Fatalf("an end without a stop recorded the stop at %s", got)
	}
}

func TestFailedExecutionChargesAndStallsAfterThree(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	owner := registerOwner(t, db)
	for n := 1; n <= 3; n++ {
		run := fmt.Sprintf("prepare-%d", n)
		startExecution(t, db, execution(run, task, "prepare", "task/1", owner, bootA))
		result := endExecution(t, db, domain.ExecutionEnd{RunID: run, Reason: "failure", Condition: "The merge candidate step failed.", Stopped: true, Now: spineNow})
		if !result.Charged || result.Remaining != 3-n || result.Stalled != (n == 3) || result.RetryAfter == nil ||
			!result.RetryAfter.Equal(spineNow.Add(domain.RetryDelay)) {
			t.Fatalf("failure %d = %+v", n, result)
		}
		wantEnd(t, db, run, "failure", retryTime, 1, "infra-failed", "repository")
		if got := oneStr(t, db, `SELECT condition||':'||source||':'||cause||':'||task_id FROM infrastructure_failures WHERE run_id=?`, run); got != fmt.Sprintf("The merge candidate step failed.:resident:unknown:%d", task) {
			t.Fatalf("failure record = %s", got)
		}
		if got := oneInt(t, db, `SELECT COUNT(*) FROM diagnostic_events WHERE run_id=? AND outcome='failed'`, run); got != 1 {
			t.Fatalf("diagnostics = %d", got)
		}
	}
	if got := taskInt(t, db, task, "stalled"); got != 1 {
		t.Fatal("three charged failures did not stall the task")
	}
}

func TestEndExecutionCancelledTaskHasNoCharge(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	owner := registerOwner(t, db)
	startExecution(t, db, execution("cleanup", task, "cleanup", "repository", owner, bootA))
	mustExec(t, db, `UPDATE tasks SET decision='cancelled',decided_at=datetime('now'),archived=1 WHERE id=?`, task)
	result := endExecution(t, db, domain.ExecutionEnd{RunID: "cleanup", Reason: "failure", Condition: "The directory is busy.", Stopped: true, Now: spineNow})
	if result.Charged || result.Remaining != 3 || result.RetryAfter == nil {
		t.Fatalf("result = %+v", result)
	}
	wantEnd(t, db, "cleanup", "failure", retryTime, 0, "infra-failed", "repository")
	if got := failureCount(t, db, "cleanup"); got != 0 {
		t.Fatalf("failure records = %d", got)
	}
}

// A capture of a cancelled task repeats until it succeeds, so each failure
// charges the task and three failures stall it.
func TestCancelledTaskCaptureChargesAndStallsAfterThree(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	owner := registerOwner(t, db)
	mustExec(t, db, `UPDATE tasks SET decision='cancelled',decided_at=datetime('now'),archived=1 WHERE id=?`, task)
	for n := 1; n <= 3; n++ {
		run := fmt.Sprintf("capture-%d", n)
		startExecution(t, db, execution(run, task, "capture", "producer", owner, bootA))
		result := endExecution(t, db, domain.ExecutionEnd{RunID: run, Reason: "failure", Condition: "The capture failed.", Stopped: true, Now: spineNow})
		if !result.Charged || result.Remaining != 3-n || result.Stalled != (n == 3) {
			t.Fatalf("failure %d = %+v", n, result)
		}
		wantEnd(t, db, run, "failure", retryTime, 1, "infra-failed", "repository")
	}
	if got := taskInt(t, db, task, "stalled"); got != 1 {
		t.Fatal("three failed captures did not stall the cancelled task")
	}
}

func TestSweepEndsSilentExecution(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	owner := registerOwner(t, db)
	startExecution(t, db, execution("observe", task, "observe", "task/1", owner, bootA))
	wantRuns(t, "ended", sweep(t, db, owner, after(bootA, domain.HeartbeatLimit-time.Millisecond)))
	wantRuns(t, "ended", sweep(t, db, owner, after(bootA, domain.HeartbeatLimit)), "observe")
	wantEnd(t, db, "observe", "heartbeat", retryTime, 1, "infra-failed", "repository")
	if got := oneStr(t, db, `SELECT condition FROM infrastructure_failures WHERE run_id='observe'`); got != "The resident sent no heartbeat for 90 seconds." {
		t.Fatalf("condition = %s", got)
	}
	if got := executionStr(t, db, "observe", "stopped_at"); got != "<NULL>" {
		t.Fatalf("the sweep recorded a stop at %s", got)
	}
	if got := taskInt(t, db, task, "dispatch_retries"); got != 2 {
		t.Fatalf("allowance = %d", got)
	}
}

func TestSweepEndsMergeStepAtTenMinutes(t *testing.T) {
	for _, operation := range []string{"prepare", "apply"} {
		t.Run(operation, func(t *testing.T) {
			db := openSpine(t)
			task := mkTask(t, db, "task", "seeded")
			owner := registerOwner(t, db)
			startExecution(t, db, execution(operation, task, operation, "task/1", owner, bootA))
			for elapsed := time.Minute; elapsed < 10*time.Minute; elapsed += time.Minute {
				heartbeat(t, db, owner, []string{operation}, after(bootA, elapsed))
			}
			wantRuns(t, "ended", sweep(t, db, owner, after(bootA, 10*time.Minute-time.Millisecond)))
			wantRuns(t, "ended", sweep(t, db, owner, after(bootA, 10*time.Minute)), operation)
			wantEnd(t, db, operation, "deadline", retryTime, 1, "infra-failed", "repository")
			if got := oneStr(t, db, `SELECT condition FROM infrastructure_failures WHERE run_id=?`, operation); got != "The merge step exceeded its 10-minute limit." {
				t.Fatalf("condition = %s", got)
			}
		})
	}
}

func TestSweepEndsOtherOperationAtFiveMinutes(t *testing.T) {
	for _, operation := range []string{"capture", "cleanup", "observe", "reconcile"} {
		t.Run(operation, func(t *testing.T) {
			db := openSpine(t)
			task := mkTask(t, db, "task", "seeded")
			owner := registerOwner(t, db)
			startExecution(t, db, execution(operation, task, operation, "target-"+operation, owner, bootA))
			for elapsed := time.Minute; elapsed < 5*time.Minute; elapsed += time.Minute {
				heartbeat(t, db, owner, []string{operation}, after(bootA, elapsed))
			}
			wantRuns(t, "ended", sweep(t, db, owner, after(bootA, 5*time.Minute-time.Millisecond)))
			wantRuns(t, "ended", sweep(t, db, owner, after(bootA, 5*time.Minute)), operation)
			wantEnd(t, db, operation, "deadline", retryTime, 1, "infra-failed", "repository")
			if got := oneStr(t, db, `SELECT condition FROM infrastructure_failures WHERE run_id=?`, operation); got != "The repository operation exceeded its 5-minute limit." {
				t.Fatalf("condition = %s", got)
			}
		})
	}
}

func TestSweepEndsExecutionAfterBootChange(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	owner := registerOwner(t, db)
	startExecution(t, db, execution("apply", task, "apply", "task/1", owner, bootA))
	restarted := domain.VMClock{BootID: "boot-b", Uptime: time.Second}
	wantRuns(t, "ended", sweep(t, db, owner, restarted), "apply")
	wantEnd(t, db, "apply", "vm-restart", retryTime, 0, "interrupted", "vm-restart")
	if got := failureCount(t, db, "apply"); got != 0 {
		t.Fatalf("failure records = %d", got)
	}
}

func TestVMRestartDoesNotCharge(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	owner := registerOwner(t, db)
	clock := bootA
	for n := 1; n <= 4; n++ {
		run := fmt.Sprintf("capture-%d", n)
		startExecution(t, db, execution(run, task, "capture", "producer", owner, clock))
		clock = domain.VMClock{BootID: fmt.Sprintf("boot-%d", n), Uptime: time.Second}
		wantRuns(t, "ended", sweep(t, db, owner, clock), run)
		wantEnd(t, db, run, "vm-restart", retryTime, 0, "interrupted", "vm-restart")
	}
	if got := taskInt(t, db, task, "dispatch_retries"); got != 3 {
		t.Fatalf("allowance = %d, want no charge for a VM restart", got)
	}
}

// A host restart changes the owner and the boot together. Each such end is a
// VM restart without a charge, also for consecutive ends on one target.
func TestHostRestartDoesNotCharge(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	owner := registerOwner(t, db)
	clock := bootA
	for n := 1; n <= 2; n++ {
		run := fmt.Sprintf("prepare-%d", n)
		startExecution(t, db, execution(run, task, "prepare", "task/1", owner, clock))
		owner = registerOwner(t, db)
		clock = domain.VMClock{BootID: fmt.Sprintf("boot-%d", n), Uptime: time.Second}
		wantRuns(t, "ended", sweep(t, db, owner, clock), run)
		wantEnd(t, db, run, "vm-restart", retryTime, 0, "interrupted", "vm-restart")
	}
	if got := taskInt(t, db, task, "dispatch_retries"); got != 3 {
		t.Fatalf("allowance = %d, want no charge for a host restart", got)
	}
}

func TestOwnerChangeEndsEarlierExecutions(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	other := mkTask(t, db, "task", "seeded")
	earlier := registerOwner(t, db)
	startExecution(t, db, execution("capture", task, "capture", "producer", earlier, bootA))
	startExecution(t, db, execution("apply", other, "apply", "task/2", earlier, bootA))
	owner := registerOwner(t, db)
	startExecution(t, db, execution("cleanup", task, "cleanup", "repository", owner, bootA))
	wantCode(t, db, domain.CodeStaleOwner, func(ctx context.Context, q domain.Q) error {
		_, err := domain.SweepExecutions(ctx, q, earlier, bootA, spineNow)
		return err
	})
	wantRuns(t, "ended", sweep(t, db, owner, after(bootA, time.Second)), "capture", "apply")
	for _, run := range []string{"capture", "apply"} {
		wantEnd(t, db, run, "owner-change", retryTime, 0, "interrupted", "interrupted")
		if got := executionStr(t, db, run, "stopped_at"); got != "<NULL>" {
			t.Fatalf("the owner change recorded a stop at %s", got)
		}
	}
	if got := executionStr(t, db, "cleanup", "ended_at"); got != "<NULL>" {
		t.Fatalf("the owner change ended the execution of the current owner at %s", got)
	}
	wantRuns(t, "renewed", heartbeat(t, db, earlier, []string{"capture", "apply"}, after(bootA, time.Second)))
	if got := taskInt(t, db, task, "dispatch_retries") + taskInt(t, db, other, "dispatch_retries"); got != 6 {
		t.Fatalf("allowances = %d, want no charge for the first owner change", got)
	}
}

func TestSecondConsecutiveOwnerChangeCharges(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	owner := registerOwner(t, db)
	// changeOwner starts one execution of the target. A new owner then
	// registers, and its sweep ends the execution with an owner change.
	changeOwner := func(run string, charged int64) {
		startExecution(t, db, execution(run, task, "prepare", "task/1", owner, bootA))
		owner = registerOwner(t, db)
		wantRuns(t, "ended", sweep(t, db, owner, bootA), run)
		wantEnd(t, db, run, "owner-change", retryTime, charged, "interrupted", "interrupted")
	}
	changeOwner("first", 0)
	changeOwner("second", 1)
	if got := oneStr(t, db, `SELECT condition FROM infrastructure_failures WHERE run_id='second'`); got != "A resident restart ended the repository operation." {
		t.Fatalf("condition = %s", got)
	}
	startExecution(t, db, execution("result", task, "prepare", "task/1", owner, bootA))
	endExecution(t, db, domain.ExecutionEnd{RunID: "result", Reason: "result", Now: spineNow})
	changeOwner("after-result", 0)
	changeOwner("consecutive", 1)
	if got := taskInt(t, db, task, "dispatch_retries"); got != 1 {
		t.Fatalf("allowance = %d, want two charges", got)
	}
}

// A new owner confirms the stop of an earlier owner's execution only after the
// heartbeat limit without a heartbeat, or after a boot change.
func TestConfirmStopsAcceptsEarlierOwnerAfterSilence(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	other := mkTask(t, db, "task", "seeded")
	earlier := registerOwner(t, db)
	startExecution(t, db, execution("prepare", task, "prepare", "task/1", earlier, bootA))
	startExecution(t, db, execution("capture", other, "capture", "producer", earlier, bootA))
	owner := registerOwner(t, db)
	wantRuns(t, "ended", sweep(t, db, owner, after(bootA, time.Second)), "prepare", "capture")
	earlierRuns := []string{"prepare", "capture"}
	wantRuns(t, "confirmed before the limit", confirmStops(t, db, owner, earlierRuns, after(bootA, domain.HeartbeatLimit-time.Millisecond)))
	wantRuns(t, "confirmed at the limit", confirmStops(t, db, owner, earlierRuns, after(bootA, domain.HeartbeatLimit)), earlierRuns...)
	if got := executionStr(t, db, "prepare", "stopped_at"); got != "2026-09-28 12:00:00" {
		t.Fatalf("stop = %s", got)
	}

	startExecution(t, db, execution("cleanup", task, "cleanup", "repository", owner, bootA))
	later := registerOwner(t, db)
	wantRuns(t, "ended", sweep(t, db, later, bootA), "cleanup")
	restarted := domain.VMClock{BootID: "boot-b", Uptime: time.Second}
	wantRuns(t, "confirmed by an earlier owner", confirmStops(t, db, earlier, []string{"cleanup"}, restarted))
	wantRuns(t, "confirmed after a boot change", confirmStops(t, db, later, []string{"cleanup"}, restarted), "cleanup")

	startExecution(t, db, execution("own", task, "capture", "producer-2", later, bootA))
	wantRuns(t, "confirmed while active", confirmStops(t, db, later, []string{"own"}, bootA))
	endExecution(t, db, domain.ExecutionEnd{RunID: "own", Reason: "failure", Condition: "The capture failed.", Now: spineNow})
	wantRuns(t, "confirmed by its owner", confirmStops(t, db, later, []string{"own"}, bootA), "own")
}

func TestConfirmStopsIsIdempotent(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	owner := registerOwner(t, db)
	startExecution(t, db, execution("apply", task, "apply", "task/1", owner, bootA))
	endExecution(t, db, domain.ExecutionEnd{RunID: "apply", Reason: "result", Now: spineNow})
	wantRuns(t, "confirmed", confirmStops(t, db, owner, []string{"apply", "apply"}, bootA), "apply")
	stopped := executionStr(t, db, "apply", "stopped_at")
	var confirmed []string
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		var err error
		confirmed, err = domain.ConfirmStops(ctx, q, owner, []string{"apply"}, bootA, spineNow.Add(time.Hour))
		return err
	})
	wantRuns(t, "confirmed again", confirmed)
	if got := executionStr(t, db, "apply", "stopped_at"); got != stopped {
		t.Fatalf("a repeated confirmation moved the stop from %s to %s", stopped, got)
	}
}

// The resident reports a present container of an ended agent run. During the
// grace of that run, the report charges nothing.
func TestContainerPresentInGraceHasNoCharge(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	mkEndedRun(t, db, "producer", "worker", task, "base")
	owner := registerOwner(t, db)
	startExecution(t, db, execution("capture", task, "capture", "producer", owner, bootA))
	mustExec(t, db, `INSERT INTO repository_transfers(run_id,producer_run,task_id) VALUES('capture','producer',?)`, task)
	result := endExecution(t, db, domain.ExecutionEnd{RunID: "capture", Reason: "container-present", Stopped: true, Now: spineNow})
	if result.Charged || result.RetryAfter == nil || result.Remaining != 3 {
		t.Fatalf("result = %+v", result)
	}
	wantEnd(t, db, "capture", "container-present", retryTime, 0, "infra-failed", "repository")
	if got := failureCount(t, db, "capture"); got != 0 {
		t.Fatalf("failure records = %d", got)
	}
	if got := oneInt(t, db, `SELECT finished FROM repository_transfers WHERE run_id='capture'`); got != 1 {
		t.Fatal("the capture end left its transfer unfinished")
	}
}

func TestContainerPresentAfterGraceCharges(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	mkEndedRun(t, db, "producer", "worker", task, "base")
	mustExec(t, db, `UPDATE runs SET ended_at=datetime('now','-120 seconds') WHERE id='producer'`)
	owner := registerOwner(t, db)
	startExecution(t, db, execution("cleanup", task, "cleanup", "producer", owner, bootA))
	result := endExecution(t, db, domain.ExecutionEnd{RunID: "cleanup", Reason: "container-present",
		Condition: "A container of the ended run is present after its grace.", Stopped: true, Now: spineNow})
	if !result.Charged || result.Remaining != 2 {
		t.Fatalf("result = %+v", result)
	}
	wantEnd(t, db, "cleanup", "container-present", retryTime, 1, "infra-failed", "repository")
	if got := oneStr(t, db, `SELECT condition FROM infrastructure_failures WHERE run_id='cleanup'`); got != "A container of the ended run is present after its grace." {
		t.Fatalf("condition = %s", got)
	}
}

func TestInfrastructureFailureRefusesExecutionRun(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	owner := registerOwner(t, db)
	startExecution(t, db, execution("capture", task, "capture", "producer", owner, bootA))
	for _, fn := range []func(ctx context.Context, q domain.Q) error{
		func(ctx context.Context, q domain.Q) error {
			_, err := domain.RecordInfrastructureFailure(ctx, q, "capture", domain.InfrastructureFailureReport{Condition: "The capture failed.", Source: "resident"})
			return err
		},
		func(ctx context.Context, q domain.Q) error {
			_, err := domain.ApplyReap(ctx, q, domain.ReapArgs{RunID: "capture", Reason: "container-gone"})
			return err
		},
	} {
		err := tx(t, db, fn)
		var de *domain.DomainError
		if !errors.As(err, &de) || de.Code != domain.CodeStaleRun || de.Msg != "The run belongs to a repository operation." {
			t.Fatalf("error = %v, want the repository operation refusal", err)
		}
	}
	if got := executionStr(t, db, "capture", "ended_at"); got != "<NULL>" {
		t.Fatalf("the refused report ended the execution at %s", got)
	}
}

// A revision request during an active prepare stays pending. The next pass
// applies it after the execution ends. A cleanup does not hold the revision.
func TestRevisionWaitsForActiveExecution(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "packaged")
	owner := registerOwner(t, db)
	startExecution(t, db, execution("prepare", task, "prepare", "task/1", owner, bootA))
	startExecution(t, db, execution("cleanup", task, "cleanup", "repository", owner, bootA))
	version := taskInt(t, db, task, "record_version")
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		_, err := domain.RequestRequirementsRevision(ctx, q, task, version, "revision",
			"The revised requirement passes.", "The operator changed the scope.", "operator")
		return err
	})
	if got := oneStr(t, db, `SELECT state FROM requirements_revisions WHERE task_id=?`, task); got != "pending" {
		t.Fatalf("revision state = %s, want pending during the prepare", got)
	}
	mustTx(t, db, func(ctx context.Context, q domain.Q) error { return domain.ApplyPendingTaskRevisions(ctx, q) })
	if got := taskInt(t, db, task, "requirements_version"); got != 1 {
		t.Fatalf("requirements version = %d during the prepare", got)
	}
	endExecution(t, db, domain.ExecutionEnd{RunID: "prepare", Reason: "result", Outcome: "obsolete", Stopped: true, Now: spineNow})
	mustTx(t, db, func(ctx context.Context, q domain.Q) error { return domain.ApplyPendingTaskRevisions(ctx, q) })
	if got := oneStr(t, db, `SELECT state FROM requirements_revisions WHERE task_id=?`, task); got != "applied" {
		t.Fatalf("revision state = %s, want applied after the prepare", got)
	}
	if got := taskStr(t, db, task, "status") + ":" + taskStr(t, db, task, "description"); got != "seeded:The revised requirement passes." {
		t.Fatalf("task = %s", got)
	}
}

// wikiExecution describes one execution of a wiki update of the fixture Project.
func wikiExecution(run, wiki, operation, target string, owner int64, clock domain.VMClock) domain.ExecutionStart {
	s := execution(run, 0, operation, target, owner, clock)
	s.WikiUpdate = wiki
	return s
}

// recordWikiRepository records the repository of the active run of a wiki update.
func recordWikiRepository(t *testing.T, db *sql.DB, run string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO repository_runs(run_id,wiki_update,input_commit,destination_commit,temporary_bytes)
		SELECT run_id,run_id,start_commit,start_commit,0 FROM wiki_updates WHERE run_id=?`, run)
}

// waitingWiki starts a wiki update at wikiA, records the repository of its
// run, and ends the run with the wiki commit wikiB.
func waitingWiki(t *testing.T, db *sql.DB, project, run string) {
	t.Helper()
	startWiki(t, db, project, run, wikiA)
	recordWikiRepository(t, db, run)
	endWikiRun(t, db, run, domain.WikiRunEnd{Outcome: "completed", WikiCommit: wikiB})
}

// allowanceText returns each row that an end of an execution of a wiki update must leave unchanged.
func allowanceText(t *testing.T, db *sql.DB) string {
	t.Helper()
	var out strings.Builder
	for _, table := range []string{"tasks", "projects", "leases", "infrastructure_failures", "diagnostic_events", "wiki_updates", "activity", "repository_transfers"} {
		out.WriteString(table + "\n" + rowsText(t, db, `SELECT * FROM `+table+` ORDER BY 1`))
	}
	return out.String()
}

// An execution of a wiki update has no task. Its end reads no task, uses no
// failure allowance, and keeps the condition of the end.
func TestEndExecutionOfAWikiUpdate(t *testing.T) {
	db := openSpine(t)
	mkProject(t, db, "ws2")
	task := mkTask(t, db, "task", "seeded")
	waitingWiki(t, db, "ws", "wiki-1")
	owner := registerOwner(t, db)

	// The start reads the Project of the wiki update and requires one subject.
	other := wikiExecution("refused", "wiki-1", "cleanup", "wiki-1", owner, bootA)
	other.Project = "ws2"
	wantCode(t, db, domain.CodeProjectMismatch, func(ctx context.Context, q domain.Q) error { return domain.StartExecution(ctx, q, other) })
	wantCode(t, db, domain.CodeNotFound, func(ctx context.Context, q domain.Q) error {
		return domain.StartExecution(ctx, q, wikiExecution("refused", "absent", "cleanup", "absent", owner, bootA))
	})
	both := wikiExecution("refused", "wiki-1", "cleanup", "wiki-1", owner, bootA)
	both.TaskID = task
	for name, start := range map[string]domain.ExecutionStart{"two subjects": both, "no subject": execution("refused", 0, "cleanup", "wiki-1", owner, bootA)} {
		if err := tx(t, db, func(ctx context.Context, q domain.Q) error { return domain.StartExecution(ctx, q, start) }); err == nil {
			t.Fatalf("a start with %s wrote an execution", name)
		}
	}
	if got := oneInt(t, db, `SELECT COUNT(*) FROM runs WHERE binding='mechanical'`); got != 0 {
		t.Fatalf("refused starts wrote %d runs", got)
	}

	startExecution(t, db, wikiExecution("first", "wiki-1", "cleanup", "wiki-1", owner, bootA))
	if got := oneStr(t, db, `SELECT tier||':'||role||':'||binding||':'||COALESCE(subject,'none')||':'||COALESCE(ended_at,'active') FROM runs WHERE id='first'`); got != "pipeline:worker:mechanical:none:active" {
		t.Fatalf("run = %s, want a mechanical run with no task", got)
	}
	if got := oneStr(t, db, `SELECT project||':'||COALESCE(task_id,'none')||':'||wiki_update||':'||operation||':'||target FROM repository_executions WHERE run_id='first'`); got != "ws:none:wiki-1:cleanup:wiki-1" {
		t.Fatalf("execution = %s", got)
	}
	var fenced domain.ActiveExecution
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		var err error
		fenced, err = domain.FenceExecution(ctx, q, "first")
		return err
	})
	if fenced.TaskID != 0 || fenced.WikiUpdate != "wiki-1" || !fenced.Subject().Wiki() || fenced.Project != "ws" || fenced.Owner != owner {
		t.Fatalf("fenced execution = %+v", fenced)
	}
	endExecution(t, db, domain.ExecutionEnd{RunID: "first", Reason: "result", Now: spineNow})

	const restart = "A resident restart ended the repository operation."
	long := strings.Repeat("é", 3000)
	unchanged := allowanceText(t, db)
	for n, c := range []struct {
		name, operation, target, reason, condition string
		prepare                                    func()
		failed                                     bool
		stored, runEnd                             string
	}{
		{name: "a failure with a condition", operation: "cleanup", target: "wiki-1", reason: "failure", condition: " The cleanup failed. ",
			failed: true, stored: "The cleanup failed.", runEnd: "infra-failed:repository"},
		{name: "a failure with no condition", operation: "cleanup", target: "wiki-1", reason: "failure", condition: "  ",
			failed: true, stored: "The repository operation failed.", runEnd: "infra-failed:repository"},
		{name: "a failure with a long condition", operation: "cleanup", target: "wiki-1", reason: "failure", condition: long,
			failed: true, stored: strings.Repeat("é", 2048), runEnd: "infra-failed:repository"},
		{name: "a stopped heartbeat", operation: "cleanup", target: "wiki-1", reason: "heartbeat",
			failed: true, stored: "The resident sent no heartbeat for 90 seconds.", runEnd: "infra-failed:repository"},
		{name: "the deadline of a merge", operation: "apply", target: "wiki/wiki-1", reason: "deadline",
			failed: true, stored: "The merge step exceeded its 10-minute limit.", runEnd: "infra-failed:repository"},
		{name: "the deadline of receipt recovery", operation: "reconcile", target: "apply-run", reason: "deadline",
			failed: true, stored: "The repository operation exceeded its 5-minute limit.", runEnd: "infra-failed:repository"},
		{name: "a container within the grace", operation: "cleanup", target: "wiki-1", reason: "container-present",
			failed: false, stored: "A container of the ended run is present.", runEnd: "infra-failed:repository"},
		{name: "a container after the grace", operation: "cleanup", target: "wiki-1", reason: "container-present",
			prepare: func() { mustExec(t, db, `UPDATE runs SET ended_at=datetime('now','-120 seconds') WHERE id='wiki-1'`) },
			failed:  true, stored: "A container of the ended run is present.", runEnd: "infra-failed:repository"},
		{name: "a host restart", operation: "capture", target: "wiki-1", reason: "vm-restart",
			failed: false, stored: "The repository operation failed.", runEnd: "interrupted:vm-restart"},
		{name: "the first resident restart", operation: "cleanup", target: "wiki-1", reason: "owner-change",
			failed: false, stored: restart, runEnd: "interrupted:interrupted"},
		{name: "the second resident restart in sequence", operation: "cleanup", target: "wiki-1", reason: "owner-change",
			failed: true, stored: restart, runEnd: "interrupted:interrupted"},
		{name: "a result", operation: "cleanup", target: "wiki-1", reason: "result",
			failed: false, stored: "<NULL>", runEnd: "completed:"},
		{name: "a result that names a repair", operation: "apply", target: "wiki/wiki-1", reason: "result", condition: " Remove the file .git/index.lock. ",
			failed: false, stored: "Remove the file .git/index.lock.", runEnd: "completed:"},
		{name: "a result with a long repair", operation: "apply", target: "wiki/wiki-1", reason: "result", condition: long,
			failed: false, stored: strings.Repeat("é", 2048), runEnd: "completed:"},
	} {
		if c.prepare != nil {
			c.prepare()
			unchanged = allowanceText(t, db)
		}
		run := fmt.Sprintf("run-%d", n)
		startExecution(t, db, wikiExecution(run, "wiki-1", c.operation, c.target, owner, bootA))
		result := endExecution(t, db, domain.ExecutionEnd{RunID: run, Reason: c.reason, Condition: c.condition, Stopped: true, Now: spineNow})
		if result.TaskID != 0 || result.WikiUpdate != "wiki-1" || !result.Subject().Wiki() || result.Charged || result.Remaining != 0 || result.Stalled {
			t.Fatalf("%s: result = %+v, want a wiki update with no charge and no allowance", c.name, result)
		}
		if result.Failed != c.failed {
			t.Fatalf("%s: failure flag = %v, want %v", c.name, result.Failed, c.failed)
		}
		retry := retryTime
		if c.reason == "result" {
			retry = "<NULL>"
		}
		if (result.RetryAfter == nil) != (c.reason == "result") || (result.RetryAfter != nil && !result.RetryAfter.Equal(spineNow.Add(domain.RetryDelay))) {
			t.Fatalf("%s: retry time = %v", c.name, result.RetryAfter)
		}
		outcome, runReason, _ := strings.Cut(c.runEnd, ":")
		wantEnd(t, db, run, c.reason, retry, 0, outcome, runReason)
		if got := executionStr(t, db, run, "end_condition"); got != c.stored {
			t.Fatalf("%s: stored condition = %q, want %q", c.name, got, c.stored)
		}
		if got := allowanceText(t, db); got != unchanged {
			t.Fatalf("%s: the end changed a task, an allowance, a failure record, a diagnostic record, or the wiki update", c.name)
		}
	}

	// An execution of a task keeps its charge. Its record holds no condition.
	startExecution(t, db, execution("task-capture", task, "capture", "producer", owner, bootA))
	result := endExecution(t, db, domain.ExecutionEnd{RunID: "task-capture", Reason: "failure", Condition: "The capture failed.", Now: spineNow})
	if !result.Charged || !result.Failed || result.TaskID != task || result.WikiUpdate != "" || result.Subject().Wiki() || result.Remaining != 2 {
		t.Fatalf("task result = %+v", result)
	}
	if got := executionStr(t, db, "task-capture", "end_condition"); got != "<NULL>" {
		t.Fatalf("stored condition of a task execution = %q, want none", got)
	}
}

// An error at the end of one execution of a wiki update fails no sweep. The
// sweep ends each other execution, and the next sweep ends that execution.
func TestSweepKeepsEachOtherExecutionAfterAWikiError(t *testing.T) {
	db := openSpine(t)
	mkProject(t, db, "ws2")
	task := mkTask(t, db, "task", "seeded")
	waitingWiki(t, db, "ws", "wiki-1")
	waitingWiki(t, db, "ws2", "wiki-2")
	owner := registerOwner(t, db)
	startExecution(t, db, wikiExecution("wiki-apply", "wiki-1", "apply", "wiki/wiki-1", owner, bootA))
	startExecution(t, db, execution("task-capture", task, "capture", "producer", owner, bootA))
	other := wikiExecution("other-wiki", "wiki-2", "capture", "wiki-2", owner, bootA)
	other.Project = "ws2"
	startExecution(t, db, other)

	mustExec(t, db, `CREATE TRIGGER refuse_one_end BEFORE UPDATE OF ended_at ON repository_executions
		WHEN OLD.run_id='wiki-apply' BEGIN SELECT RAISE(ABORT, 'the database refuses this end'); END`)
	silent := after(bootA, domain.HeartbeatLimit)
	wantRuns(t, "ended beside the error", sweep(t, db, owner, silent), "task-capture", "other-wiki")
	wantEnd(t, db, "task-capture", "heartbeat", retryTime, 1, "infra-failed", "repository")
	wantEnd(t, db, "other-wiki", "heartbeat", retryTime, 0, "infra-failed", "repository")
	if got := oneStr(t, db, `SELECT COALESCE(x.ended_at,'active')||':'||COALESCE(r.ended_at,'active')||':'||COALESCE(r.outcome,'none')
		FROM repository_executions x JOIN runs r ON r.id=x.run_id WHERE x.run_id='wiki-apply'`); got != "active:active:none" {
		t.Fatalf("the execution with the error = %s, want an active execution and an active run", got)
	}
	// The active execution holds the merge lane of its Project.
	if err := tx(t, db, func(ctx context.Context, q domain.Q) error {
		return domain.StartExecution(ctx, q, execution("task-prepare", task, "prepare", fmt.Sprintf("task/%d", task), owner, silent))
	}); err == nil {
		t.Fatal("a merge step of a task started beside the active merge of the wiki commit")
	}

	mustExec(t, db, `DROP TRIGGER refuse_one_end`)
	wantRuns(t, "ended by the next sweep", sweep(t, db, owner, silent), "wiki-apply")
	wantEnd(t, db, "wiki-apply", "heartbeat", retryTime, 0, "infra-failed", "repository")
	if got := executionStr(t, db, "wiki-apply", "end_condition"); got != "The resident sent no heartbeat for 90 seconds." {
		t.Fatalf("stored condition = %q", got)
	}
}

// An active execution of a wiki update holds no revision request of a task.
func TestRevisionDoesNotWaitForAWikiExecution(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "packaged")
	waitingWiki(t, db, "ws", "wiki-1")
	owner := registerOwner(t, db)
	startExecution(t, db, wikiExecution("wiki-prepare", "wiki-1", "prepare", "wiki/wiki-1", owner, bootA))
	version := taskInt(t, db, task, "record_version")
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		_, err := domain.RequestRequirementsRevision(ctx, q, task, version, "revision",
			"The revised requirement passes.", "The operator changed the scope.", "operator")
		return err
	})
	mustTx(t, db, func(ctx context.Context, q domain.Q) error { return domain.ApplyPendingTaskRevisions(ctx, q) })
	if got := oneStr(t, db, `SELECT state FROM requirements_revisions WHERE task_id=?`, task); got != "applied" {
		t.Fatalf("revision state = %s, want applied beside the merge candidate step of the wiki commit", got)
	}
	if got := taskStr(t, db, task, "status") + ":" + taskStr(t, db, task, "description"); got != "seeded:The revised requirement passes." {
		t.Fatalf("task = %s", got)
	}
	if got := executionStr(t, db, "wiki-prepare", "ended_at"); got != "<NULL>" {
		t.Fatalf("the revision ended the execution of the wiki update at %s", got)
	}
}
