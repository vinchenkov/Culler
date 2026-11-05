package substrate_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	sqlite3 "modernc.org/sqlite/lib"
)

// mkOwner registers the next resident owner and returns its number.
func mkOwner(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	res := mustExec(t, db, `INSERT INTO execution_owners DEFAULT VALUES`)
	owner, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return owner
}

// mkMechanicalRun inserts the active run of one execution.
func mkMechanicalRun(t *testing.T, db *sql.DB, run string, task int64) {
	t.Helper()
	mustExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,session_path,binding)
		SELECT ?,'pipeline','worker',project,id,'sessions/'||?,'mechanical' FROM tasks WHERE id=?`, run, run, task)
}

const insertExecution = `INSERT INTO repository_executions
	(run_id,project,task_id,operation,target,owner,effect,boot_id,heartbeat_uptime_ms,deadline_uptime_ms)
	SELECT ?,project,id,?,?,(SELECT MAX(owner) FROM execution_owners),'{}','boot',0,300000 FROM tasks WHERE id=?`

// mkExecution inserts the run and the active execution of the current owner.
func mkExecution(t *testing.T, db *sql.DB, run string, task int64, operation, target string) {
	t.Helper()
	mkMechanicalRun(t, db, run, task)
	mustExec(t, db, insertExecution, run, operation, target, task)
}

// endExecution ends the run and the execution with a result and a stop.
func endExecution(t *testing.T, db *sql.DB, run string) {
	t.Helper()
	mustExec(t, db, `UPDATE runs SET ended_at=datetime('now'),outcome='completed' WHERE id=?`, run)
	mustExec(t, db, `UPDATE repository_executions SET ended_at=datetime('now'),end_reason='result',stopped_at=datetime('now') WHERE run_id=?`, run)
}

// holdLease gives the Project lease to an agent run of the task.
func holdLease(t *testing.T, db *sql.DB, run string, task int64) {
	t.Helper()
	mustExec(t, db, `UPDATE leases SET run_id=?,subject=?,owner='worker',acquired_at=datetime('now'),
		boot_id='boot',acquired_uptime_ms=0,deadline_uptime_ms=1 WHERE project=(SELECT project FROM tasks WHERE id=?)`, run, task, task)
	mustExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,binding)
		SELECT ?,'pipeline','worker',project,id,'fake' FROM tasks WHERE id=?`, run, task)
}

func freeLease(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `UPDATE leases SET run_id=NULL,subject=NULL,owner=NULL,acquired_at=NULL,last_heartbeat_at=NULL,
		boot_id=NULL,acquired_uptime_ms=NULL,heartbeat_uptime_ms=NULL,deadline_uptime_ms=NULL WHERE project='ws'`)
}

func wantMessage(t *testing.T, db *sql.DB, message, q string, args ...any) {
	t.Helper()
	wantSQLiteError(t, db, sqlite3.SQLITE_CONSTRAINT, message, q, args...)
}

// Only agent runs hold the Project lease. A claim updates the lease before
// it inserts the run, so each order has its own trigger.
func TestLeaseRefusesMechanicalRun(t *testing.T) {
	t.Run("run_before_lease", func(t *testing.T) {
		db := openSpine(t)
		task := mkTask(t, db, "task", "seeded")
		mkOwner(t, db)
		mkMechanicalRun(t, db, "mechanical", task)
		wantMessage(t, db, "only an agent run holds the Project lease",
			`UPDATE leases SET run_id='mechanical',owner='worker',acquired_at=datetime('now'),
			boot_id='boot',acquired_uptime_ms=0,deadline_uptime_ms=1 WHERE project='ws'`)
		if got := oneStr(t, db, `SELECT run_id FROM leases WHERE project='ws'`); got != "<NULL>" {
			t.Fatalf("lease run = %s, want a free lease", got)
		}
	})
	t.Run("lease_before_run", func(t *testing.T) {
		db := openSpine(t)
		task := mkTask(t, db, "task", "seeded")
		mustExec(t, db, `UPDATE leases SET run_id='claimed',owner='worker',acquired_at=datetime('now'),
			boot_id='boot',acquired_uptime_ms=0,deadline_uptime_ms=1 WHERE project='ws'`)
		wantMessage(t, db, "only an agent run holds the Project lease",
			`INSERT INTO runs(id,tier,role,project,subject,binding) VALUES('claimed','pipeline','worker','ws',?,'mechanical')`, task)
		mustExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,binding) VALUES('claimed','pipeline','worker','ws',?,'fake')`, task)
	})
	t.Run("agent_run_and_execution_together", func(t *testing.T) {
		db := openSpine(t)
		task := mkTask(t, db, "task", "seeded")
		other := mkTask(t, db, "task", "seeded")
		holdLease(t, db, "agent", task)
		mkOwner(t, db)
		mkExecution(t, db, "capture", other, "capture", "producer-run")
		if got := oneStr(t, db, `SELECT run_id FROM leases WHERE project='ws'`); got != "agent" {
			t.Fatalf("lease run = %s, want the agent run", got)
		}
	})
}

func TestExecutionIndexesRefuseSecondMergeStep(t *testing.T) {
	db := openSpine(t)
	mkProject(t, db, "ws2")
	first := mkTask(t, db, "task", "seeded")
	second := mkTask(t, db, "task", "seeded")
	elsewhere := mkTaskIn(t, db, "ws2", "task", "seeded")
	mkOwner(t, db)
	mkExecution(t, db, "prepare-1", first, "prepare", "task/1")
	for _, operation := range []string{"observe", "prepare", "apply", "reconcile"} {
		mkMechanicalRun(t, db, "second-"+operation, second)
		wantMessage(t, db, "UNIQUE constraint failed: repository_executions.project", insertExecution, "second-"+operation, operation, "second-target", second)
	}
	// Other operations of the Project and merge steps of another Project start.
	mkExecution(t, db, "capture-2", second, "capture", "producer-2")

	mkExecution(t, db, "cleanup-2", second, "cleanup", "repository-2")
	mkExecution(t, db, "apply-elsewhere", elsewhere, "apply", "task/elsewhere")
	endExecution(t, db, "prepare-1")
	mkExecution(t, db, "observe-2", second, "observe", "task/second")
}

func TestExecutionIndexesRefuseSecondTargetExecution(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	mkOwner(t, db)
	mkExecution(t, db, "capture-1", task, "capture", "producer")
	for _, operation := range []string{"capture", "cleanup"} {
		mkMechanicalRun(t, db, "second-"+operation, task)
		wantMessage(t, db, "UNIQUE constraint failed: repository_executions.target", insertExecution, "second-"+operation, operation, "producer", task)
	}
	endExecution(t, db, "capture-1")
	mkExecution(t, db, "cleanup-1", task, "cleanup", "producer")
	if got := oneInt(t, db, `SELECT COUNT(*) FROM repository_executions WHERE target='producer'`); got != 2 {
		t.Fatalf("executions of the target = %d, want the ended capture and the active cleanup", got)
	}
}

func TestExecutionRequiresAssignedActiveRunAndCurrentOwner(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	other := mkTask(t, db, "task", "seeded")
	earlier := mkOwner(t, db)
	mkOwner(t, db)
	const message = "an execution requires its assigned active run and the current owner"

	mkMechanicalRun(t, db, "earlier-owner", task)
	wantMessage(t, db, message, `INSERT INTO repository_executions
		(run_id,project,task_id,operation,target,owner,effect,boot_id,heartbeat_uptime_ms,deadline_uptime_ms)
		VALUES('earlier-owner','ws',?,'capture','producer',?,'{}','boot',0,1)`, task, earlier)

	mustExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,binding) VALUES('agent','pipeline','worker','ws',?,'fake')`, task)
	wantMessage(t, db, message, insertExecution, "agent", "capture", "producer", task)

	mkMechanicalRun(t, db, "other-subject", other)
	wantMessage(t, db, message, insertExecution, "other-subject", "capture", "producer", task)

	mkMechanicalRun(t, db, "ended", task)
	mustExec(t, db, `UPDATE runs SET ended_at=datetime('now'),outcome='completed' WHERE id='ended'`)
	wantMessage(t, db, message, insertExecution, "ended", "capture", "producer", task)

	mkMechanicalRun(t, db, "born-ended", task)
	wantMessage(t, db, message, `INSERT INTO repository_executions
		(run_id,project,task_id,operation,target,owner,effect,boot_id,heartbeat_uptime_ms,deadline_uptime_ms,ended_at,end_reason)
		SELECT 'born-ended','ws',?,'capture','producer',MAX(owner),'{}','boot',0,1,datetime('now'),'result' FROM execution_owners`, task)

	mkExecution(t, db, "assigned", task, "capture", "producer")
}

func TestExecutionWaitsForTaskAgentRun(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	other := mkTask(t, db, "task", "seeded")
	mkOwner(t, db)
	holdLease(t, db, "agent", task)
	for _, operation := range []string{"capture", "observe", "prepare", "apply", "reconcile"} {
		mkMechanicalRun(t, db, "held-"+operation, task)
		wantMessage(t, db, "the repository operation waits for the agent run of its task",
			insertExecution, "held-"+operation, operation, "target-"+operation, task)
	}
	mkExecution(t, db, "cleanup", task, "cleanup", "old-repository")
	mkExecution(t, db, "other-capture", other, "capture", "other-producer")
	// An ended agent run holds its lease until the resident confirms the stop.
	// The repository operation of its task can start at once.
	mustExec(t, db, `UPDATE runs SET ended_at=datetime('now'),outcome='completed' WHERE id='agent'`)
	mkExecution(t, db, "capture", task, "capture", "producer")
}

func TestAgentRunWaitsForTaskExecution(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	other := mkTask(t, db, "task", "seeded")
	mkOwner(t, db)
	mkExecution(t, db, "capture", task, "capture", "producer")
	wantMessage(t, db, "the agent run waits for the repository operation of its task",
		`UPDATE leases SET run_id='agent',subject=?,owner='worker',acquired_at=datetime('now'),
		boot_id='boot',acquired_uptime_ms=0,deadline_uptime_ms=1 WHERE project='ws'`, task)
	holdLease(t, db, "other-agent", other)
	freeLease(t, db)
	endExecution(t, db, "capture")
	mkExecution(t, db, "cleanup", task, "cleanup", "producer")
	holdLease(t, db, "agent", task)
}

func TestArtifactExpiryRefusesExecution(t *testing.T) {
	db := openSpine(t)
	mkProject(t, db, "ws2")
	task := mkTask(t, db, "task", "seeded")
	closed := mkTask(t, db, "task", "seeded")
	elsewhere := mkTaskIn(t, db, "ws2", "task", "seeded")
	mkOwner(t, db)
	mustExec(t, db, `INSERT INTO artifact_retention(task_id,expires_at,token) VALUES(?,datetime('now'),'expiry')`, closed)
	mkMechanicalRun(t, db, "during-expiry", task)
	wantMessage(t, db, "artifact expiry is active; retry the repository operation",
		insertExecution, "during-expiry", "cleanup", "repository", task)
	mkExecution(t, db, "other-project", elsewhere, "cleanup", "repository-elsewhere")
	mustExec(t, db, `UPDATE artifact_retention SET token=NULL WHERE task_id=?`, closed)
	mustExec(t, db, insertExecution, "during-expiry", "cleanup", "repository", task)
}

func TestExecutionPreservesAssignmentAndEnd(t *testing.T) {
	db := openSpine(t)
	mkProject(t, db, "ws2")
	task := mkTask(t, db, "task", "seeded")
	other := mkTask(t, db, "task", "seeded")
	mkOwner(t, db)
	mkExecution(t, db, "execution", task, "prepare", "task/1")
	const preserved = "the execution preserves its assignment and its end"
	for _, change := range []string{
		`project='ws2'`, `task_id=` + strconv.FormatInt(other, 10), `operation='apply'`, `target='task/2'`, `owner=owner+1`,
		`effect='{"run_id":"x"}'`, `started_at='2000-01-01 00:00:00'`, `boot_id='other'`, `deadline_uptime_ms=5`,
	} {
		wantMessage(t, db, preserved, `UPDATE repository_executions SET `+change+` WHERE run_id='execution'`)
	}
	wantAbort(t, db, `UPDATE repository_executions SET stopped_at=datetime('now') WHERE run_id='execution'`)
	wantAbort(t, db, `UPDATE repository_executions SET retry_after=datetime('now') WHERE run_id='execution'`)
	wantAbort(t, db, `UPDATE repository_executions SET ended_at=datetime('now') WHERE run_id='execution'`)
	wantAbort(t, db, `UPDATE repository_executions SET ended_at=datetime('now'),end_reason='result',retry_after=datetime('now') WHERE run_id='execution'`)
	wantAbort(t, db, `UPDATE repository_executions SET ended_at=datetime('now'),end_reason='result',charged=1 WHERE run_id='execution'`)
	mustExec(t, db, `UPDATE repository_executions SET heartbeat_uptime_ms=20000 WHERE run_id='execution'`)

	mustExec(t, db, `UPDATE repository_executions SET ended_at='2026-09-28 10:00:00',end_reason='failure',
		retry_after='2026-09-28 10:01:00',charged=1 WHERE run_id='execution'`)
	for _, change := range []string{
		`ended_at='2026-09-28 11:00:00'`, `end_reason='deadline'`, `retry_after=NULL`, `charged=0`, `heartbeat_uptime_ms=40000`,
	} {
		wantMessage(t, db, preserved, `UPDATE repository_executions SET `+change+` WHERE run_id='execution'`)
	}
	mustExec(t, db, `UPDATE repository_executions SET stopped_at='2026-09-28 10:02:00' WHERE run_id='execution'`)
	wantMessage(t, db, preserved, `UPDATE repository_executions SET stopped_at='2026-09-28 10:03:00' WHERE run_id='execution'`)
	wantMessage(t, db, "the execution preserves its history", `DELETE FROM repository_executions WHERE run_id='execution'`)
	wantMessage(t, db, "the owner record is immutable", `UPDATE execution_owners SET registered_at='2000-01-01 00:00:00'`)
	wantMessage(t, db, "the owner record is retained", `DELETE FROM execution_owners`)
}

func TestInfrastructureFailureRequiresActiveExecution(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	other := mkTask(t, db, "task", "seeded")
	mkOwner(t, db)
	mkExecution(t, db, "failed", task, "capture", "producer")
	const message = "an infrastructure failure requires its assigned active run"
	wantMessage(t, db, message, `INSERT INTO infrastructure_failures(run_id,task_id,allowance,condition,source) VALUES('failed',?,'task','The capture failed.','resident')`, other)
	mustExec(t, db, `INSERT INTO infrastructure_failures(run_id,task_id,allowance,condition,source) VALUES('failed',?,'task','The capture failed.','resident')`, task)

	mkExecution(t, db, "ended", task, "cleanup", "repository")
	mustExec(t, db, `UPDATE repository_executions SET ended_at=datetime('now'),end_reason='failure' WHERE run_id='ended'`)
	wantMessage(t, db, message, `INSERT INTO infrastructure_failures(run_id,task_id,allowance,condition,source) VALUES('ended',?,'task','The cleanup failed.','resident')`, task)
}

func TestRepositoryObservationRequiresActiveExecution(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	mkOwner(t, db)
	mkExecution(t, db, "observe", task, "observe", "task/1")
	wantMessage(t, db, "the repository observation requires its assigned active execution",
		`INSERT INTO repository_observations(run_id,task_id,operation) VALUES('observe',?,'prepare')`, task)
	mustExec(t, db, `INSERT INTO repository_observations(run_id,task_id,operation) VALUES('observe',?,'observe')`, task)
	mustExec(t, db, `UPDATE repository_observations SET result='{"commit":"a"}' WHERE run_id='observe'`)

	endExecution(t, db, "observe")
	mkExecution(t, db, "observe-late", task, "observe", "task/late")
	mustExec(t, db, `INSERT INTO repository_observations(run_id,task_id,operation) VALUES('observe-late',?,'observe')`, task)
	endExecution(t, db, "observe-late")
	wantMessage(t, db, "a repository result requires its active execution",
		`UPDATE repository_observations SET result='{"commit":"b"}' WHERE run_id='observe-late'`)
}

func TestRepositoryTransferFinishRequiresCaptureExecution(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	mkEndedRun(t, db, "producer", "worker", task, "base")
	mkOwner(t, db)
	mkExecution(t, db, "capture", task, "capture", "producer")
	mustExec(t, db, `INSERT INTO repository_transfers(run_id,producer_run,task_id) VALUES('capture','producer',?)`, task)
	mustExec(t, db, `UPDATE repository_transfers SET finished=1 WHERE run_id='capture'`)

	mkExecution(t, db, "late", task, "cleanup", "producer-late")
	mustExec(t, db, `INSERT INTO repository_transfers(run_id,producer_run,task_id) VALUES('late','producer',?)`, task)
	wantMessage(t, db, "a repository result requires its active execution",
		`UPDATE repository_transfers SET finished=1 WHERE run_id='late'`)
}

func TestMergeOperationResultRequiresActiveExecution(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "packaged")
	candidate := seedMergedCandidate(t, db, task)
	mkOwner(t, db)
	insertMerge := `INSERT INTO merge_operations(run_id,task_id,project,operation,version_id,candidate_id,destination_branch)
		SELECT ?,id,project,?,submitted_version,?,'main' FROM tasks WHERE id=?`
	const message = "a repository result requires its active execution"

	mkExecution(t, db, "apply", task, "apply", "task/1")
	mustExec(t, db, insertMerge, "apply", "apply", candidate, task)
	endExecution(t, db, "apply")
	wantMessage(t, db, message, `UPDATE merge_operations SET result='{"applied":true}',finished=1 WHERE run_id='apply'`)
	// Receipt recovery targets the apply run and records its receipt.
	mkExecution(t, db, "reconcile", task, "reconcile", "apply")
	mustExec(t, db, `UPDATE merge_operations SET result='{"applied":true}',finished=1 WHERE run_id='apply'`)
	endExecution(t, db, "reconcile")

	mkExecution(t, db, "prepare-1", task, "prepare", "task/1")
	mustExec(t, db, insertMerge, "prepare-1", "prepare", nil, task)
	endExecution(t, db, "prepare-1")
	wantMessage(t, db, message, `UPDATE merge_operations SET finished=1 WHERE run_id='prepare-1'`)
	// A later prepare of the same task finishes the interrupted prepare row.
	mkExecution(t, db, "prepare-2", task, "prepare", "task/1")
	mustExec(t, db, `UPDATE merge_operations SET finished=1 WHERE run_id='prepare-1'`)
}

func TestCapturedVersionRequiresCaptureExecution(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	mkEndedRun(t, db, "producer", "worker", task, "base")
	mkEndedRun(t, db, "other-producer", "worker", task, "base")
	mustExec(t, db, `INSERT INTO version_submissions(run_id,task_id,input_commit,result_commit) VALUES('producer',?,'base','result')`, task)
	mustExec(t, db, `INSERT INTO task_versions(task_id,run_id,input_commit,result_commit,destination_branch,retained_ref)
		VALUES(?,'producer','base','result','main','refs/test/producer')`, task)
	version := oneInt(t, db, `SELECT id FROM task_versions WHERE run_id='producer'`)
	const message = "a repository result requires its active execution"
	capture := `UPDATE version_submissions SET captured_version=? WHERE run_id='producer'`

	wantMessage(t, db, message, capture, version)
	mkOwner(t, db)
	mkExecution(t, db, "capture-other", task, "capture", "other-producer")
	wantMessage(t, db, message, capture, version)
	mkExecution(t, db, "cleanup", task, "cleanup", "producer")
	wantMessage(t, db, message, capture, version)
	endExecution(t, db, "cleanup")
	mkExecution(t, db, "capture", task, "capture", "producer")
	mustExec(t, db, capture, version)
	endExecution(t, db, "capture")
	if got := oneInt(t, db, `SELECT captured_version FROM version_submissions WHERE run_id='producer'`); got != version {
		t.Fatalf("captured version = %d, want %d", got, version)
	}
}

func TestCleanupResultRequiresCleanupExecution(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	mustExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,ended_at,outcome,binding)
		VALUES('repository','pipeline','worker','ws',?,datetime('now'),'completed','fake')`, task)
	mustExec(t, db, `INSERT INTO repository_runs(run_id,task_id,input_commit,destination_commit,temporary_bytes,captured)
		VALUES('repository',?,'base','base',10,1)`, task)
	const message = "a repository result requires its active execution"
	clean := `UPDATE repository_runs SET cleaned=1,temporary_bytes=0 WHERE run_id='repository'`
	fail := `UPDATE repository_runs SET cleanup_error='The directory is busy.' WHERE run_id='repository'`

	wantMessage(t, db, message, clean)
	wantMessage(t, db, message, fail)
	mkOwner(t, db)
	mkExecution(t, db, "capture", task, "capture", "repository")
	wantMessage(t, db, message, clean)
	endExecution(t, db, "capture")
	mkExecution(t, db, "cleanup-1", task, "cleanup", "repository")
	mustExec(t, db, fail)
	mustExec(t, db, `UPDATE repository_executions SET ended_at=datetime('now'),end_reason='failure',retry_after=datetime('now','+60 seconds') WHERE run_id='cleanup-1'`)
	wantMessage(t, db, message, clean)
	mkExecution(t, db, "cleanup-2", task, "cleanup", "repository")
	mustExec(t, db, `UPDATE repository_runs SET cleaned=1,cleanup_error=NULL,temporary_bytes=0 WHERE run_id='repository'`)
	endExecution(t, db, "cleanup-2")
	wantMessage(t, db, message, `UPDATE repository_runs SET cleaned=0 WHERE run_id='repository'`)
}

// The requirements revision waits for each active execution of the task,
// except a cleanup.
func TestRequirementsRevisionWaitsForActiveExecution(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	mustExec(t, db, `INSERT INTO requirements_revisions(operation_id,task_id,expected_version,requirements,previous_requirements,reason,actor)
		VALUES('revision',?,1,'The new requirement passes.','','The operator changed the scope.','operator')`, task)
	revision := oneInt(t, db, `SELECT id FROM requirements_revisions WHERE operation_id='revision'`)
	mustExec(t, db, `INSERT INTO requirements_evidence(revision_id,task_id,requirements_version,status,correction_count)
		VALUES(?,?,1,'seeded',0)`, revision, task)
	apply := `UPDATE tasks SET description='The new requirement passes.',requirements_version=2 WHERE id=?`
	mkOwner(t, db)
	mkExecution(t, db, "prepare", task, "prepare", "task/1")
	wantMessage(t, db, "the requirements revision requires the previous result and a complete run lifecycle", apply, task)
	endExecution(t, db, "prepare")
	mkExecution(t, db, "cleanup", task, "cleanup", "repository")
	mustExec(t, db, apply, task)
}

// mkWikiUpdate inserts an active wiki update with its active Scribe run.
func mkWikiUpdate(t *testing.T, db *sql.DB, run, project, start string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,session_path,binding)
		VALUES(?,'pipeline','scribe',?,NULL,'sessions/'||?,'fake/fake')`, run, project, run)
	mustExec(t, db, `INSERT INTO wiki_updates(project,run_id,start_commit,started_at,boot_id,acquired_uptime_ms,deadline_uptime_ms)
		VALUES(?,?,?,datetime('now'),'boot',0,900000)`, project, run, start)
}

// endWikiRun ends the run of a wiki update and stores its wiki commit.
func endWikiRun(t *testing.T, db *sql.DB, run, wiki string) {
	t.Helper()
	mustExec(t, db, `UPDATE runs SET ended_at=datetime('now'),outcome='completed' WHERE id=?`, run)
	mustExec(t, db, `UPDATE wiki_updates SET wiki_commit=? WHERE run_id=?`, wiki, run)
}

// mkWikiMechanicalRun inserts the active run of one execution of a wiki update.
func mkWikiMechanicalRun(t *testing.T, db *sql.DB, run, project string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,session_path,binding)
		VALUES(?,'pipeline','worker',?,NULL,'sessions/'||?,'mechanical')`, run, project, run)
}

const insertWikiExecution = `INSERT INTO repository_executions
	(run_id,project,wiki_update,operation,target,owner,effect,boot_id,heartbeat_uptime_ms,deadline_uptime_ms)
	VALUES(?,?,?,?,?,(SELECT MAX(owner) FROM execution_owners),'{}','boot',0,300000)`

// mkWikiExecution inserts the run and the active execution of a wiki update.
func mkWikiExecution(t *testing.T, db *sql.DB, run, project, wiki, operation, target string) {
	t.Helper()
	mkWikiMechanicalRun(t, db, run, project)
	mustExec(t, db, insertWikiExecution, run, project, wiki, operation, target)
}

const (
	insertWikiMerge = `INSERT INTO merge_operations(run_id,wiki_update,project,operation,candidate_id,destination_branch)
		VALUES(?,?,?,?,?,'main')`
	insertWikiCandidate = `INSERT INTO merge_candidates(wiki_update,run_id,destination_branch,destination_commit,candidate_commit,result,status,retained_ref)
		VALUES(?,?,'main',?,?,'{}','merged',?)`
	failWikiUpdate    = `UPDATE wiki_updates SET ended_at=datetime('now'),result='failed',cause=? WHERE run_id=?`
	succeedWikiUpdate = `UPDATE wiki_updates SET ended_at=datetime('now'),result='succeeded',destination_commit=?,merged_commit=? WHERE run_id=?`
)

// authorizeWikiMerge records a merged candidate of the wiki update and the
// unfinished merge authorization with its active execution.
func authorizeWikiMerge(t *testing.T, db *sql.DB, project, wiki, prepare, apply, destination, commit string) {
	t.Helper()
	mkWikiExecution(t, db, prepare, project, wiki, "prepare", "wiki/"+wiki)
	mustExec(t, db, insertWikiMerge, prepare, wiki, project, "prepare", nil)
	mustExec(t, db, insertWikiCandidate, wiki, prepare, destination, commit, "refs/mc/wiki/candidates/"+prepare)
	mustExec(t, db, `UPDATE merge_operations SET result='{}',finished=1 WHERE run_id=?`, prepare)
	endExecution(t, db, prepare)
	mkWikiExecution(t, db, apply, project, wiki, "apply", "wiki/"+wiki)
	mustExec(t, db, insertWikiMerge, apply, wiki, project, "apply",
		oneInt(t, db, `SELECT id FROM merge_candidates WHERE run_id=?`, prepare))
}

// Each record of a repository operation has one subject: a task or a wiki update.
func TestWikiExecutionSubjectChecks(t *testing.T) {
	db := openSpine(t)
	mkProject(t, db, "ws2")
	c1, c2, c3 := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	const (
		check      = "CHECK constraint failed"
		repository = "the repository requires its active wiki update and its assigned commit"
		assigned   = "an execution requires its assigned active run and the current owner"
		merge      = "the merge of a wiki commit requires its active wiki update"
		candidate  = "the merge candidate of a wiki commit requires its active wiki update"
	)
	task := mkTask(t, db, "task", "seeded")
	mkOwner(t, db)
	mkWikiUpdate(t, db, "wiki", "ws", c1)
	mkWikiUpdate(t, db, "elsewhere", "ws2", c1)

	// The repository of the run of a wiki update starts at the start commit.
	const ready = `INSERT INTO repository_runs(run_id,task_id,wiki_update,input_commit,destination_commit,temporary_bytes)
		VALUES(?,?,?,?,?,0)`
	wantMessage(t, db, repository, ready, "wiki", nil, "wiki", c2, c2)
	wantMessage(t, db, repository, ready, "elsewhere", nil, "wiki", c1, c1)
	wantMessage(t, db, check, ready, "wiki", nil, nil, c1, c1)
	wantAbort(t, db, ready, "wiki", task, "wiki", c1, c1)
	wantMessage(t, db, check, `INSERT INTO repository_runs(run_id,wiki_update,input_commit,destination_commit,version_id,temporary_bytes)
		VALUES('wiki','wiki',?,?,1,0)`, c1, c1)
	mustExec(t, db, ready, "wiki", nil, "wiki", c1, c1)
	mustExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,binding) VALUES('worker','pipeline','worker','ws',?,'fake')`, task)
	mustExec(t, db, ready, "worker", task, nil, c1, c1)
	if got := oneInt(t, db, `SELECT COUNT(*) FROM artifact_holds WHERE task_id IS NULL`); got != 0 {
		t.Fatalf("artifact holds with no task = %d, want 0: the repository of a wiki update holds no task", got)
	}
	archived := mkTask(t, db, "task", "proposed")
	cancelTask(t, db, archived)
	if got := oneInt(t, db, `SELECT COUNT(*) FROM tasks WHERE id=? AND id NOT IN (SELECT task_id FROM artifact_holds)`, archived); got != 1 {
		t.Fatalf("archived tasks with no hold = %d, want 1: a row with no task must not empty the read", got)
	}

	// An execution of a wiki update waits for the end of the run.
	mkWikiMechanicalRun(t, db, "capture", "ws")
	wantMessage(t, db, "the repository operation waits for the run of its wiki update",
		insertWikiExecution, "capture", "ws", "wiki", "capture", "wiki")
	endWikiRun(t, db, "wiki", c2)
	mustExec(t, db, `UPDATE runs SET ended_at=datetime('now'),outcome='failed' WHERE id='elsewhere'`)

	const execution = `INSERT INTO repository_executions
		(run_id,project,task_id,wiki_update,operation,target,owner,effect,boot_id,heartbeat_uptime_ms,deadline_uptime_ms)
		VALUES(?,'ws',?,?,?,?,(SELECT MAX(owner) FROM execution_owners),'{}','boot',0,300000)`
	mkMechanicalRun(t, db, "task-run", task)
	wantMessage(t, db, check, execution, "task-run", task, "wiki", "capture", "wiki")
	wantMessage(t, db, check, execution, "capture", nil, nil, "capture", "wiki")
	for _, operation := range []string{"observe"} {
		wantMessage(t, db, check, execution, "capture", nil, "wiki", operation, "wiki/wiki")
	}
	wantMessage(t, db, assigned, execution, "capture", nil, "elsewhere", "capture", "elsewhere")
	wantMessage(t, db, assigned, execution, "capture", nil, "absent", "capture", "absent")
	wantMessage(t, db, assigned, execution, "task-run", nil, "wiki", "capture", "wiki")
	mustExec(t, db, execution, "capture", nil, "wiki", "capture", "wiki")
	wantMessage(t, db, "the execution preserves its assignment and its end",
		`UPDATE repository_executions SET wiki_update='elsewhere' WHERE run_id='capture'`)
	wantMessage(t, db, "the execution preserves its assignment and its end",
		`UPDATE repository_executions SET wiki_update=NULL,task_id=? WHERE run_id='capture'`, task)
	wantMessage(t, db, "an infrastructure failure requires its assigned active run",
		`INSERT INTO infrastructure_failures(run_id,task_id,allowance,condition,source)
		VALUES('capture',NULL,'project','The capture failed.','resident')`)
	mustExec(t, db, `UPDATE repository_runs SET captured=1 WHERE run_id='wiki'`)
	endExecution(t, db, "capture")

	// The merge candidate step and the merge of a wiki commit use the branch main.
	mkWikiExecution(t, db, "prepare", "ws", "wiki", "prepare", "wiki/wiki")
	const operation = `INSERT INTO merge_operations(run_id,task_id,wiki_update,project,operation,version_id,candidate_id,destination_branch,packet_id)
		VALUES(?,?,?,?,'prepare',?,NULL,?,?)`
	wantMessage(t, db, merge, operation, "prepare", nil, "wiki", "ws", nil, "develop", nil)
	wantMessage(t, db, merge, operation, "prepare", nil, "elsewhere", "ws", nil, "main", nil)
	wantMessage(t, db, merge, operation, "prepare", nil, "wiki", "ws2", nil, "main", nil)
	wantMessage(t, db, check, operation, "prepare", task, "wiki", "ws", nil, "main", nil)
	wantMessage(t, db, check, operation, "prepare", nil, nil, "ws", nil, "main", nil)
	wantMessage(t, db, check, operation, "prepare", nil, "wiki", "ws", 1, "main", nil)
	wantMessage(t, db, check, operation, "prepare", nil, "wiki", "ws", nil, "main", 1)
	mustExec(t, db, operation, "prepare", nil, "wiki", "ws", nil, "main", nil)

	const merged = `INSERT INTO merge_candidates(task_id,wiki_update,run_id,version_id,destination_branch,destination_commit,candidate_commit,result,status,retained_ref)
		VALUES(?,?,'prepare',?,?,?,?,'{}','merged','refs/mc/wiki/candidates/prepare')`
	wantMessage(t, db, candidate, merged, nil, "elsewhere", nil, "main", c1, c3)
	wantMessage(t, db, candidate, merged, nil, "wiki", nil, "develop", c1, c3)
	wantMessage(t, db, check, merged, task, "wiki", nil, "main", c1, c3)
	wantMessage(t, db, check, merged, nil, nil, nil, "main", c1, c3)
	wantMessage(t, db, check, merged, nil, "wiki", 1, "main", c1, c3)
	wantMessage(t, db, check, merged, task, nil, nil, "main", c1, c3)
	mustExec(t, db, merged, nil, "wiki", nil, "main", c1, c3)

	// The merge needs a candidate of its own wiki update.
	packaged := mkTask(t, db, "task", "packaged")
	other := seedMergedCandidate(t, db, packaged)
	mustExec(t, db, `UPDATE merge_operations SET result='{}',finished=1 WHERE run_id='prepare'`)
	endExecution(t, db, "prepare")
	mkWikiExecution(t, db, "apply", "ws", "wiki", "apply", "wiki/wiki")
	wantMessage(t, db, check, insertWikiMerge, "apply", "wiki", "ws", "apply", nil)
	wantMessage(t, db, merge, insertWikiMerge, "apply", "wiki", "ws", "apply", other)
	mustExec(t, db, insertWikiMerge, "apply", "wiki", "ws", "apply", oneInt(t, db, `SELECT id FROM merge_candidates WHERE run_id='prepare'`))
}

// The repository of a merge candidate step of a wiki commit starts at the wiki
// commit and belongs to the active execution of that step.
func TestRepositoryRunOfAWikiCandidateStep(t *testing.T) {
	db := openSpine(t)
	c1, c2 := strings.Repeat("a", 40), strings.Repeat("b", 40)
	const (
		repository = "the repository requires its active wiki update and its assigned commit"
		row        = `INSERT INTO repository_runs(run_id,wiki_update,input_commit,destination_commit,temporary_bytes,captured)
			VALUES(?,?,?,?,0,1)`
	)
	mkOwner(t, db)
	mkWikiUpdate(t, db, "wiki", "ws", c1)
	mustExec(t, db, `INSERT INTO repository_runs(run_id,wiki_update,input_commit,destination_commit,temporary_bytes)
		VALUES('wiki','wiki',?,?,0)`, c1, c1)
	endWikiRun(t, db, "wiki", c2)

	// A capture is no merge candidate step.
	mkWikiExecution(t, db, "capture", "ws", "wiki", "capture", "wiki")
	wantMessage(t, db, repository, row, "capture", "wiki", c2, c1)
	mustExec(t, db, `UPDATE repository_runs SET captured=1 WHERE run_id='wiki'`)
	endExecution(t, db, "capture")

	mkWikiMechanicalRun(t, db, "prepare", "ws")
	wantMessage(t, db, repository, row, "prepare", "wiki", c2, c1)
	mustExec(t, db, insertWikiExecution, "prepare", "ws", "wiki", "prepare", "wiki/wiki")
	wantMessage(t, db, repository, row, "prepare", "wiki", c1, c1)
	wantMessage(t, db, repository, row, "capture", "wiki", c2, c1)
	mustExec(t, db, row, "prepare", "wiki", c2, c1)
	if got := oneStr(t, db, `SELECT wiki_update || '|' || input_commit || '|' || captured FROM repository_runs WHERE run_id='prepare'`); got != "wiki|"+c2+"|1" {
		t.Fatalf("repository of the merge candidate step = %s, want the wiki update, the wiki commit, and a complete capture", got)
	}

	// An ended merge candidate step gets no repository.
	endExecution(t, db, "prepare")
	mkWikiMechanicalRun(t, db, "late", "ws")
	wantMessage(t, db, repository, row, "late", "wiki", c2, c1)

	// A cleanup removes the repository of the step. The record of a task run does not change.
	mkWikiExecution(t, db, "cleanup", "ws", "wiki", "cleanup", "prepare")
	mustExec(t, db, `UPDATE repository_runs SET cleaned=1 WHERE run_id='prepare'`)
	endExecution(t, db, "cleanup")
	task := mkTask(t, db, "task", "seeded")
	mustExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,binding) VALUES('worker','pipeline','worker','ws',?,'fake')`, task)
	mustExec(t, db, `INSERT INTO repository_runs(run_id,task_id,input_commit,destination_commit,temporary_bytes)
		VALUES('worker',?,'base','base',0)`, task)
	wantMessage(t, db, "the repository requires its assigned task",
		`INSERT INTO repository_runs(run_id,task_id,input_commit,destination_commit,temporary_bytes)
		VALUES('late',?,'base','base',0)`, task)
}

// One index counts the merge steps of tasks and of wiki commits of a Project together.
func TestMergeLaneCountsTaskAndWikiSteps(t *testing.T) {
	db := openSpine(t)
	mkProject(t, db, "ws2")
	c1, c2 := strings.Repeat("a", 40), strings.Repeat("b", 40)
	const lane = "UNIQUE constraint failed: repository_executions.project"
	task := mkTask(t, db, "task", "seeded")
	mkOwner(t, db)
	mkWikiUpdate(t, db, "wiki", "ws", c1)
	endWikiRun(t, db, "wiki", c2)
	mkWikiUpdate(t, db, "elsewhere", "ws2", c1)
	endWikiRun(t, db, "elsewhere", c2)

	// A merge step of a task is active.
	mkExecution(t, db, "task-prepare", task, "prepare", "task/1")
	for _, operation := range []string{"prepare", "apply", "reconcile"} {
		mkWikiMechanicalRun(t, db, "wiki-"+operation, "ws")
		wantMessage(t, db, lane, insertWikiExecution, "wiki-"+operation, "ws", "wiki", operation, "wiki-target-"+operation)
	}
	mkWikiExecution(t, db, "wiki-capture", "ws", "wiki", "capture", "wiki")
	mkWikiExecution(t, db, "wiki-cleanup", "ws", "wiki", "cleanup", "wiki-repository")
	mkWikiExecution(t, db, "elsewhere-prepare", "ws2", "elsewhere", "prepare", "wiki/elsewhere")
	endExecution(t, db, "wiki-capture")
	endExecution(t, db, "wiki-cleanup")
	endExecution(t, db, "task-prepare")

	// A merge step of a wiki commit is active.
	for _, operation := range []string{"prepare", "apply", "reconcile"} {
		mustExec(t, db, insertWikiExecution, "wiki-"+operation, "ws", "wiki", operation, "wiki-target-"+operation)
		for _, step := range []string{"observe", "prepare", "apply", "reconcile"} {
			run := "task-" + step + "-beside-" + operation
			mkMechanicalRun(t, db, run, task)
			wantMessage(t, db, lane, insertExecution, run, step, "task-target-"+step, task)
		}
		mkExecution(t, db, "task-capture-beside-"+operation, task, "capture", "producer")
		mkExecution(t, db, "task-cleanup-beside-"+operation, task, "cleanup", "repository")

		if got := oneInt(t, db, `SELECT COUNT(*) FROM repository_executions WHERE project='ws' AND ended_at IS NULL`); got != 3 {
			t.Fatalf("active executions beside the %s of a wiki commit = %d, want the merge step and two other operations", operation, got)
		}
		for _, run := range []string{"task-capture-beside-", "task-cleanup-beside-", "wiki-"} {
			endExecution(t, db, run+operation)
		}
	}
	mkExecution(t, db, "task-observe", task, "observe", "task/1")
}

// The end of a wiki update follows the result of its merge and the end of its
// executions. The schema keeps the condition of an end and refuses a charge.
func TestWikiEndTriggers(t *testing.T) {
	c1, c2, c3 := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	const (
		check = "CHECK constraint failed"
		// Two of the three triggers can refuse one end. Their texts start with these words.
		anyEnd         = "a wiki update "
		afterMerge     = "a wiki update ends after the result of its merge"
		afterExecution = "a wiki update fails after the end of its execution"
		mergedCommit   = "a wiki update with a merged wiki commit cannot fail"
		preserved      = "the execution preserves its assignment and its end"
		result         = "a repository result requires its active execution"
		applied        = `{"applied":{"authorization":"apply","commit":"cccccccccccccccccccccccccccccccccccccccc","recovered":true},"status":""}`
		end            = `UPDATE repository_executions SET ended_at=datetime('now'),end_reason=?,charged=?,end_condition=? WHERE run_id=?`
	)
	fixture := func(t *testing.T) *sql.DB {
		t.Helper()
		db := openSpine(t)
		mkOwner(t, db)
		mkWikiUpdate(t, db, "wiki", "ws", c1)
		endWikiRun(t, db, "wiki", c2)
		return db
	}

	t.Run("a failed end follows the end of each execution", func(t *testing.T) {
		db := fixture(t)
		mkWikiExecution(t, db, "capture", "ws", "wiki", "capture", "wiki")
		wantMessage(t, db, afterExecution, failWikiUpdate, "Culler did not capture the wiki commit.", "wiki")
		mustExec(t, db, `UPDATE runs SET ended_at=datetime('now'),outcome='failed' WHERE id='capture'`)
		mustExec(t, db, end, "failure", 0, "The capture failed.", "capture")
		mustExec(t, db, failWikiUpdate, "Culler did not capture the wiki commit.", "wiki")
	})

	t.Run("a cleanup holds no failed end", func(t *testing.T) {
		db := fixture(t)
		mkWikiExecution(t, db, "cleanup", "ws", "wiki", "cleanup", "wiki")
		mustExec(t, db, failWikiUpdate, "Culler did not capture the wiki commit.", "wiki")
		endExecution(t, db, "cleanup")
		mkWikiExecution(t, db, "second-cleanup", "ws", "wiki", "cleanup", "wiki")
	})

	t.Run("only the result of the merge ends the wiki update", func(t *testing.T) {
		db := fixture(t)
		authorizeWikiMerge(t, db, "ws", "wiki", "prepare", "apply", c1, c3)
		wantMessage(t, db, anyEnd, failWikiUpdate, "Culler did not merge the wiki commit.", "wiki")
		wantMessage(t, db, afterMerge, succeedWikiUpdate, c1, c3, "wiki")
		mustExec(t, db, `UPDATE runs SET ended_at=datetime('now'),outcome='failed' WHERE id='apply'`)
		mustExec(t, db, end, "heartbeat", 0, "The heartbeat of the merge stopped.", "apply")
		wantMessage(t, db, afterMerge, failWikiUpdate, "Culler did not merge the wiki commit.", "wiki")
		wantMessage(t, db, result, `UPDATE merge_operations SET result='{}',finished=1 WHERE run_id='apply'`)

		// Receipt recovery proves the merge. The successful end can precede the end of its execution.
		mkWikiExecution(t, db, "reconcile", "ws", "wiki", "reconcile", "apply")
		mustExec(t, db, `UPDATE merge_operations SET result=?,finished=1 WHERE run_id='apply'`, applied)
		wantMessage(t, db, anyEnd, failWikiUpdate, "Culler did not merge the wiki commit.", "wiki")
		mustExec(t, db, succeedWikiUpdate, c1, c3, "wiki")
		endExecution(t, db, "reconcile")
		mkWikiMechanicalRun(t, db, "late", "ws")
		wantMessage(t, db, "the merge of a wiki commit requires its active wiki update", insertWikiMerge, "late", "wiki", "ws", "prepare", nil)
	})

	t.Run("a wiki update with a merged wiki commit cannot fail", func(t *testing.T) {
		db := fixture(t)
		authorizeWikiMerge(t, db, "ws", "wiki", "prepare", "apply", c1, c3)
		mustExec(t, db, `UPDATE merge_operations SET result=?,finished=1 WHERE run_id='apply'`, applied)
		endExecution(t, db, "apply")
		wantMessage(t, db, mergedCommit, failWikiUpdate, "Culler did not merge the wiki commit.", "wiki")
		mustExec(t, db, succeedWikiUpdate, c1, c3, "wiki")
	})

	t.Run("a merge authorization that was not used permits the failed end", func(t *testing.T) {
		db := fixture(t)
		authorizeWikiMerge(t, db, "ws", "wiki", "prepare", "apply", c1, c3)
		mustExec(t, db, `UPDATE runs SET ended_at=datetime('now'),outcome='failed' WHERE id='apply'`)
		mustExec(t, db, end, "deadline", 0, "The merge reached its time limit.", "apply")
		wantMessage(t, db, afterMerge, failWikiUpdate, "Culler did not merge the wiki commit.", "wiki")
		mkWikiExecution(t, db, "reconcile", "ws", "wiki", "reconcile", "apply")
		mustExec(t, db, `UPDATE merge_operations SET result='{"status":"not-applied"}',finished=1 WHERE run_id='apply'`)
		endExecution(t, db, "reconcile")
		mustExec(t, db, failWikiUpdate, "Culler did not merge the wiki commit. The merge reached its time limit.", "wiki")
	})

	t.Run("the condition of an end and the charge", func(t *testing.T) {
		db := fixture(t)
		mkWikiExecution(t, db, "capture", "ws", "wiki", "capture", "wiki")
		wantMessage(t, db, check, `UPDATE repository_executions SET end_condition='early' WHERE run_id='capture'`)
		mustExec(t, db, `UPDATE runs SET ended_at=datetime('now'),outcome='failed' WHERE id='capture'`)
		wantMessage(t, db, check, end, "failure", 1, "The capture failed.", "capture")
		wantMessage(t, db, check, end, "failure", 0, "", "capture")
		wantMessage(t, db, check, end, "failure", 0, strings.Repeat("x", 2049), "capture")
		mustExec(t, db, end, "failure", 0, strings.Repeat("x", 2048), "capture")
		wantMessage(t, db, preserved, `UPDATE repository_executions SET end_condition='other' WHERE run_id='capture'`)
		wantMessage(t, db, preserved, `UPDATE repository_executions SET end_condition=NULL WHERE run_id='capture'`)

		// An execution of a task keeps its charge and can hold a condition.
		task := mkTask(t, db, "task", "seeded")
		mkExecution(t, db, "task-capture", task, "capture", "producer")
		mustExec(t, db, `UPDATE runs SET ended_at=datetime('now'),outcome='failed' WHERE id='task-capture'`)
		mustExec(t, db, end, "failure", 1, "The capture failed.", "task-capture")
		mkExecution(t, db, "task-cleanup", task, "cleanup", "repository")
		mustExec(t, db, `UPDATE runs SET ended_at=datetime('now'),outcome='completed' WHERE id='task-cleanup'`)
		mustExec(t, db, end, "result", 0, nil, "task-cleanup")
	})
}
