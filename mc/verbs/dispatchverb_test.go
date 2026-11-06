package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	operatorconfig "mc/config"
	"mc/dispatch"
	"mc/domain"
	"mc/routing"
)

var (
	dvOld    = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	dvFuture = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
)

// dvWorkspaceRepo is the shared dispatch fixture's workspace: an ordinary Git
// repository at its exact canonical path.
func dvWorkspaceRepo(t *testing.T) string {
	t.Helper()
	ws := grWorkspace(t)
	srcGit(t, ws, "init", "-q", "--initial-branch=main")
	srcGit(t, ws, "config", "commit.gpgsign", "false")
	writeFile(t, filepath.Join(ws, "README.md"), "hello world\n")
	srcGit(t, ws, "add", "-A")
	srcGit(t, ws, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test",
		"commit", "-q", "-m", "seed")
	return ws
}

// dvAddProject registers a second repository Project on the fixture
// spine: its own git workspace and sandbox profile, the mount allowlist
// re-scaffolded to cover it, so a round for it can attest and spawn.
func dvAddProject(t *testing.T, db *sql.DB, id, status string) {
	t.Helper()
	workspace := dvWorkspaceRepo(t)
	writeVisionFixture(t, workspace)
	home, err := filepath.EvalSymlinks(os.Getenv("MC_HOME"))
	if err != nil {
		t.Fatalf("resolve MC_HOME: %v", err)
	}
	toolHome := ProjectToolHome(home, id)
	if err := os.MkdirAll(toolHome, 0o700); err != nil {
		t.Fatalf("create synthetic home: %v", err)
	}
	dvExec(t, db, `INSERT INTO sandbox_profiles (id, workspace_root, artifact_roots, denied_paths, tool_home_dir)
		VALUES (?, ?, '[]', '[]', ?)`, id+"-profile", workspace, toolHome)
	dvExec(t, db, `INSERT INTO projects (id, title, sandbox_profile, status)
		VALUES (?, ?, ?, ?)`, id, "Project "+id, id+"-profile", status)
	dvReadyEnvironment(t, db, id)
	if _, err := EnsureMountAllowlist(os.Getenv("MC_HOME")); err != nil {
		t.Fatalf("scaffold mount-allowlist: %v", err)
	}
}

func dvSpine(t *testing.T, initArgs ...func(*InitArgs)) *sql.DB {
	t.Helper()
	root := t.TempDir()
	// Every Project is a Git repository, so the shared dispatch fixture is
	// one: mount attest resolves Git controls and probes the object format of
	// the run's Project. The root is symlink-resolved because attest refuses
	// a workspace that is not its own exact canonical path.
	workspace := dvWorkspaceRepo(t)
	a := InitArgs{
		Spine:         filepath.Join(root, "spine.db"),
		Project:       "ws-test",
		WorkspaceRoot: workspace,
	}
	for _, apply := range initArgs {
		apply(&a)
	}
	writeVisionFixture(t, a.WorkspaceRoot)
	// Init creates the Project synthetic home under MC_HOME, so the fixture
	// home is set before Init runs.
	home := filepath.Dir(a.Spine)
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatalf("secure fixture home: %v", err)
	}
	t.Setenv("MC_HOME", home)
	if _, err := Init(a); err != nil {
		t.Fatalf("init spine: %v", err)
	}
	if _, err := operatorconfig.Ensure(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	if _, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), enableTestBindings); err != nil {
		t.Fatalf("enable test bindings: %v", err)
	}
	if _, err := EnsureMountAllowlist(home); err != nil {
		t.Fatalf("scaffold mount-allowlist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "routing.md"), []byte(`# test routing
| role | harness | binding |
| --- | --- | --- |
| strategist | claude-sdk | claude |
| arbiter | codex | chatgpt |
| worker | claude-sdk | endpoint |
| verifier | codex | chatgpt |
| packager | claude-sdk | endpoint |
| homie | claude-sdk | claude |
`), 0o600); err != nil {
		t.Fatalf("write routing.md: %v", err)
	}
	db, err := OpenSpine(a.Spine)
	if err != nil {
		t.Fatalf("open spine: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	for name, spec := range routing.EnabledBindings(testBindingConfig().Runtimes) {
		_ = name
		operation := "connection_access"
		if strings.HasSuffix(spec.AuthKind, "oauth") {
			operation = "token_refresh"
		}
		dvExec(t, db, `INSERT OR IGNORE INTO connection_health(connection_id,operation,authorization_id,state,verified_at) VALUES(?,?, 'unavailable','ready',datetime('now'))`, spec.Credential, operation)
	}

	// The deployment identity mirror: dispatch refuses to prepare without it
	// matching meta.deployment_uuid.
	var uuid string
	if err := db.QueryRow(`SELECT deployment_uuid FROM meta WHERE id = 1`).Scan(&uuid); err != nil {
		t.Fatalf("read deployment uuid: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "deployment.uuid"), []byte(uuid+"\n"), 0o600); err != nil {
		t.Fatalf("write deployment mirror: %v", err)
	}
	dvReadyEnvironment(t, db, a.Project)
	return db
}

func dvReadyEnvironment(t *testing.T, db *sql.DB, project string) {
	t.Helper()
	dvExec(t, db, `INSERT INTO environment_base_sources(base_digest,registry_reference,release_identity) VALUES(?,'','fixture') ON CONFLICT(base_digest,registry_reference) DO NOTHING`, environmentTestBase)
	configuration := environmentTestConfiguration()
	if err := ValidateEnvironmentConfiguration(&configuration); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	revision := "fixture-environment-" + project
	dvExec(t, db, `INSERT INTO environment_revisions(id,project_id,editor,configuration,inputs_digest) VALUES(?,?,'fixture',?,?)`, revision, project, string(body), environmentBuildDigest(configuration))
	dvExec(t, db, `INSERT INTO project_environments(project_id,setup_id,selected_base_digest,selected_revision_id,selected_image_digest,draft_revision_id,readiness,phase,authorization) VALUES(?,?,?,?,?,?,'ready','complete','change')`, project, "fixture-setup-"+project, environmentTestBase, revision, environmentTestBase, revision)
}

func dvExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func dvInsertTask(t *testing.T, db *sql.DB, task dispatch.Task) {
	t.Helper()
	dvExec(t, db, `
		INSERT INTO tasks
			(id, title, scope, priority, created_at, status,
			 dispatch_retries, origin, project)
		VALUES (?, ?, ?, ?, ?, 'proposed', ?, 'user', ?)`,
		task.ID, task.Title, string(task.Scope), task.Priority,
		task.CreatedAt.Format(spineTime), task.DispatchRetries, task.Project)
	var path []dispatch.Status
	switch task.Status {
	case dispatch.StatusProposed:
	case dispatch.StatusSeeded:
		path = []dispatch.Status{dispatch.StatusSeeded}
	case dispatch.StatusWorked:
		path = []dispatch.Status{dispatch.StatusSeeded, dispatch.StatusWorked}
	case dispatch.StatusVerified:
		path = []dispatch.Status{dispatch.StatusSeeded, dispatch.StatusWorked, dispatch.StatusVerified}
	case dispatch.StatusPackaged:
		path = []dispatch.Status{dispatch.StatusSeeded, dispatch.StatusWorked, dispatch.StatusVerified, dispatch.StatusPackaged}
	default:
		t.Fatalf("unsupported fixture status %q", task.Status)
	}
	for _, status := range path {
		if status == dispatch.StatusPackaged {
			dvCompleteDelivery(t, db, task.ID)
		}
		dvExec(t, db, `UPDATE tasks SET status=? WHERE id=?`, string(status), task.ID)
	}
	if task.Blocked {
		dvExec(t, db, `UPDATE tasks SET blocked=1, blocked_kind='operator', blocked_reason='fixture block' WHERE id=?`, task.ID)
	}
	if task.Decision == dispatch.DecisionApproved {
		dvApproveTask(t, db, task.ID, task.DecidedAt)
	} else if task.Decision != "" {
		dvExec(t, db, `UPDATE tasks SET decision=?, decided_at=? WHERE id=?`,
			string(task.Decision), nullableTime(task.DecidedAt), task.ID)
	}
	if task.Archived {
		dvExec(t, db, `UPDATE tasks SET archived=1 WHERE id=?`, task.ID)
	}
}

// dvCompleteDelivery records the delivery chain the packaged status requires:
// the Worker submission and the Verifier pass. The commits are fixture
// values; no repository holds them.
func dvCompleteDelivery(t *testing.T, db *sql.DB, task int64) {
	t.Helper()
	dvCompleteDeliveryRound(t, db, task, fmt.Sprintf("delivery-%d", task), strings.Repeat("b", 40))
}

// dvCompleteDeliveryRound records one delivery chain with runs named after
// the prefix and a Worker result commit.
func dvCompleteDeliveryRound(t *testing.T, db *sql.DB, task int64, prefix, result string) {
	t.Helper()
	input := strings.Repeat("a", 40)
	var project, branch string
	if err := db.QueryRow(`SELECT t.project, t.destination_branch FROM tasks t WHERE t.id=?`, task).
		Scan(&project, &branch); err != nil {
		t.Fatalf("read the delivery record of task %d: %v", task, err)
	}
	worker := prefix + "-worker"
	verifier := prefix + "-verifier"
	for _, run := range []struct{ id, role string }{{worker, "worker"}, {verifier, "verifier"}} {
		dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject, ended_at)
			VALUES (?, 'pipeline', ?, ?, ?, datetime('now'))`, run.id, run.role, project, task)
	}
	// Every temporary repository is captured and cleaned: the fixture leaves
	// no repository operation pending.
	dvExec(t, db, `INSERT INTO repository_runs (run_id, task_id, input_commit, destination_commit, temporary_bytes, captured, cleaned)
		VALUES (?, ?, ?, ?, 0, 1, 1)`, worker, task, input, input)
	dvExec(t, db, `UPDATE tasks SET input_commit=COALESCE(input_commit, ?) WHERE id=?`, input, task)
	dvExec(t, db, `INSERT INTO version_submissions (run_id, task_id, input_commit, result_commit)
		VALUES (?, ?, ?, ?)`, worker, task, input, result)
	dvExec(t, db, `INSERT INTO task_versions (task_id, run_id, input_commit, result_commit, destination_branch, retained_ref)
		VALUES (?, ?, ?, ?, ?, ?)`, task, worker, input, result, branch, "refs/mc/fixture/"+worker)
	version := dfInt(t, db, `SELECT id FROM task_versions WHERE run_id=?`, worker)
	dvCaptureVersion(t, db, worker, version)
	dvExec(t, db, `UPDATE repository_runs SET version_id=? WHERE run_id=?`, version, worker)
	dvExec(t, db, `UPDATE tasks SET submitted_version=? WHERE id=?`, version, task)
	dvExec(t, db, `INSERT INTO repository_runs (run_id, task_id, input_commit, destination_commit, version_id, temporary_bytes, captured, cleaned)
		VALUES (?, ?, ?, ?, ?, 0, 1, 1)`, verifier, task, result, input, version)
	// The verdict lives on the version alone: a test that proves the verdict
	// history of the run rows records its own Verifier runs.
	dvExec(t, db, `INSERT INTO version_verdicts (version_id, run_id, outcome, evidence_path)
		VALUES (?, ?, 'pass', ?)`, version, verifier, fmt.Sprintf("/mc/artifacts/%s/%d/verification/verdict.md", project, task))
	dvExec(t, db, `UPDATE tasks SET source_version=? WHERE id=?`, version, task)
	// The Packager records its packet on the delivery.
	dvExec(t, db, `UPDATE tasks SET report_path=?, report_sha256=?, thesis='fixture thesis' WHERE id=?`,
		fmt.Sprintf("/mc/artifacts/%s/%d/packet/packet.md", project, task), strings.Repeat("d", 64), task)
	// The merged candidate of the submitted version: a packaged task
	// without one owes the repository a merge preparation.
	merge := prefix + "-merge"
	dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject, ended_at)
		VALUES (?, 'pipeline', 'packager', ?, ?, datetime('now'))`, merge, project, task)
	dvExec(t, db, `INSERT INTO merge_candidates (task_id, run_id, version_id, destination_branch, destination_commit, candidate_commit, result, status, retained_ref)
		VALUES (?, ?, ?, ?, ?, ?, '{}', 'merged', ?)`,
		task, merge, version, branch, input, strings.Repeat("c", 40), "refs/mc/fixture/"+merge)
	candidate := dfInt(t, db, `SELECT id FROM merge_candidates WHERE run_id=?`, merge)
	dvExec(t, db, `UPDATE tasks SET candidate_id=? WHERE id=?`, candidate, task)
}

// dvCaptureVersion records the captured version of the producer run in one
// transaction. A capture execution records it and ends with its result, as
// the capture operation does.
func dvCaptureVersion(t *testing.T, db *sql.DB, producer string, version int64) {
	t.Helper()
	execution := "capture-" + producer
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	for _, step := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO execution_owners(owner) SELECT 1 WHERE NOT EXISTS(SELECT 1 FROM execution_owners)`, nil},
		{`INSERT INTO runs(id,tier,role,project,subject,session_path,binding)
			SELECT ?,'pipeline','worker',t.project,t.id,'sessions/'||?,'mechanical'
			FROM version_submissions s JOIN tasks t ON t.id=s.task_id WHERE s.run_id=?`, []any{execution, execution, producer}},
		{`INSERT INTO repository_executions(run_id,project,task_id,operation,target,owner,effect,boot_id,heartbeat_uptime_ms,deadline_uptime_ms)
			SELECT ?,t.project,t.id,'capture',s.run_id,(SELECT MAX(owner) FROM execution_owners),'{}','fixture',0,300000
			FROM version_submissions s JOIN tasks t ON t.id=s.task_id WHERE s.run_id=?`, []any{execution, producer}},
		{`UPDATE version_submissions SET captured_version=? WHERE run_id=?`, []any{version, producer}},
		{`UPDATE runs SET ended_at=datetime('now'), outcome='completed' WHERE id=?`, []any{execution}},
		{`UPDATE repository_executions SET ended_at=datetime('now'), end_reason='result', stopped_at=datetime('now') WHERE run_id=?`, []any{execution}},
	} {
		if _, err := tx.Exec(step.query, step.args...); err != nil {
			t.Fatalf("capture fixture:\n  %s\n  %v", step.query, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit capture fixture: %v", err)
	}
}

// dvApproveTask records the operator approval of a packaged task the way the
// schema admits it: a live review packet, an approved review decision on the
// submitted version, then the task decision.
func dvApproveTask(t *testing.T, db *sql.DB, task int64, decidedAt *time.Time) {
	t.Helper()
	var version, candidate int64
	if err := db.QueryRow(`SELECT t.submitted_version, t.candidate_id
		FROM tasks t WHERE t.id=? AND t.status='packaged'`, task).
		Scan(&version, &candidate); err != nil {
		t.Fatalf("task %d is not a packaged task with a merged candidate: %v", task, err)
	}
	{
		packet, _ := dvPublishPacket(t, db, task, "", dvOld)
		dvExec(t, db, `INSERT INTO review_decisions (packet_id, decision) VALUES (?, 'approved')`, packet)
	}
	dvExec(t, db, `UPDATE tasks SET decision='approved', decided_at=? WHERE id=?`, nullableTime(decidedAt), task)
}

// dvPublishPacket publishes the review of a packaged task: the live packet
// row, when absent, and the reviewed version of its submitted delivery. It
// returns the reviewed packet id and version id an operator decision names.
func dvPublishPacket(t *testing.T, db *sql.DB, task int64, renderPath string, createdAt time.Time) (packet, version int64) {
	t.Helper()

	dvExec(t, db, `INSERT INTO review_versions (task_id, version_id, candidate_id, render_path, report_sha256, thesis, criteria,requirements_version,created_at)
		SELECT t.id, t.submitted_version, t.candidate_id, COALESCE(?, t.report_path), t.report_sha256, t.thesis, COALESCE(t.description, ''),t.requirements_version,?
		FROM tasks t WHERE t.id=? ON CONFLICT(task_id,version_id) DO NOTHING`,
		sql.NullString{String: renderPath, Valid: renderPath != ""}, createdAt.Format(spineTime), task)
	dvExec(t, db, `UPDATE tasks SET current_review_id=(SELECT id FROM review_versions WHERE task_id=tasks.id AND version_id=tasks.submitted_version),review_slot_held=1 WHERE id=?`, task)
	if err := db.QueryRow(`SELECT p.id, p.version_id FROM review_versions p JOIN tasks d ON d.submitted_version=p.version_id WHERE p.task_id=?`, task).
		Scan(&packet, &version); err != nil {
		t.Fatalf("read the reviewed packet of task %d: %v", task, err)
	}
	return packet, version
}

func nullableTime(v *time.Time) any {
	if v == nil {
		return nil
	}
	return v.Format(spineTime)
}

func dvTask(id int64, scope dispatch.Scope, status dispatch.Status, priority int) dispatch.Task {
	task := dispatch.Task{
		ID: id, Title: fmt.Sprintf("task-%d", id), Scope: scope,
		Priority: priority, CreatedAt: dvOld, Status: status,
		DispatchRetries: 3, Project: "ws-test",
	}
	if status == dispatch.StatusPackaged {
		// The delivery dvInsertTask records for a packaged task: a verified
		// submitted version and a checked merge candidate. The version and
		// candidate ids stand for the recorded rows.
		task.Delivery.VersionID, task.Delivery.CandidateID, task.Delivery.CandidateMerged = 1, 1, true
	}
	return task
}

func dvConfig() dispatch.Config {
	return dispatch.Config{
		ReviewWIPCap: 3, TimeoutMinutes: 60, GraceMinutes: 15,
		SpawnGraceS: 60,
	}
}

func dvNow(t *testing.T, db *sql.DB) time.Time {
	t.Helper()
	now, err := spineNow(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return now
}

// dvClock is the clock that the dispatch verb reads.
func dvClock(t *testing.T, db *sql.DB) dispatch.Clock {
	t.Helper()
	vm, err := domain.ReadVMClock()
	if err != nil {
		t.Fatal(err)
	}
	return dispatch.Clock{Now: dvNow(t, db), BootID: vm.BootID, Uptime: vm.Uptime}
}

// dvHold gives the lease of project to run on the current VM clock. Each
// duration is awake time: the time since the claim, the time since the
// heartbeat (nil for no heartbeat), and the time until the hard deadline. A
// negative age puts the stamp in the future.
func dvHold(t *testing.T, db *sql.DB, project, run, owner string, subject *int64, acquired time.Duration, heartbeat *time.Duration, deadline time.Duration) dispatch.Lease {
	t.Helper()
	vm, err := domain.ReadVMClock()
	if err != nil {
		t.Fatal(err)
	}
	lease := dispatch.Lease{
		Held: true, RunID: run, Owner: owner, SubjectID: subject, BootID: vm.BootID,
		AcquiredUptime: (vm.Uptime - acquired).Truncate(time.Millisecond),
		DeadlineUptime: (vm.Uptime + deadline).Truncate(time.Millisecond),
	}
	var subjectValue, beat, beatUptime any
	if subject != nil {
		subjectValue = *subject
	}
	if heartbeat != nil {
		at := (vm.Uptime - *heartbeat).Truncate(time.Millisecond)
		lease.HeartbeatUptime = &at
		beat = time.Now().Add(-*heartbeat).UTC().Format(spineTime)
		beatUptime = at.Milliseconds()
	}
	dvExec(t, db, `UPDATE leases SET run_id=?, subject=?, owner=?, acquired_at=?, last_heartbeat_at=?,
		boot_id=?, acquired_uptime_ms=?, heartbeat_uptime_ms=?, deadline_uptime_ms=? WHERE project=?`,
		run, subjectValue, owner, time.Now().Add(-acquired).UTC().Format(spineTime), beat,
		vm.BootID, lease.AcquiredUptime.Milliseconds(), beatUptime, lease.DeadlineUptime.Milliseconds(), project)
	return lease
}

func dvAssertEffect(t *testing.T, want dispatch.Action, gotAny any) map[string]any {
	t.Helper()
	got, ok := gotAny.(map[string]any)
	if !ok {
		t.Fatalf("effect type = %T, want map", gotAny)
	}
	if got["action"] != string(want.Kind) {
		t.Fatalf("effect action = %v, pure Decide = %q (effect %v)", got["action"], want.Kind, got)
	}
	switch want.Kind {
	case dispatch.KindIdle:
		if got["reason"] != string(want.Idle) {
			t.Fatalf("idle reason = %v, want %q", got["reason"], want.Idle)
		}
	case dispatch.KindSpawn:
		if got["role"] != string(want.Spawn.Role) {
			t.Fatalf("spawn role = %v, want %q", got["role"], want.Spawn.Role)
		}
		var subject any
		if want.Spawn.SubjectID != nil {
			subject = *want.Spawn.SubjectID
		}
		if got["subject_id"] != subject {
			t.Fatalf("spawn subject = %#v, want %#v", got["subject_id"], subject)
		}
		pool := want.Spawn.ProposedPool
		if pool == nil {
			pool = []int64{}
		}
		if !reflect.DeepEqual(got["pool_ids"], pool) {
			t.Fatalf("spawn pool = %#v, want %#v", got["pool_ids"], pool)
		}
	case dispatch.KindReap:
		if got["run_id"] != want.Reap.RunID || got["reason"] != string(want.Reap.Reason) || got["stop_container"] != want.Reap.StopContainer {
			t.Fatalf("reap effect = %v, want %+v", got, want.Reap)
		}
	}
	return got
}

func dvDispatch(t *testing.T, db *sql.DB, rec dispatch.Records, lease dispatch.Lease, cfg dispatch.Config) (dispatch.Action, map[string]any) {
	t.Helper()
	if rec.Project == "" {
		rec.Project = "ws-test"
	}
	// The fixture workspace carries a vision file, so the pure oracle sees
	// the same ready vision the loader reads.
	rec.VisionReady = true
	want := dispatch.Decide(rec, lease, cfg, dvClock(t, db))
	got, err := dvDispatchOne(db)
	if err != nil {
		t.Fatalf("verbs.Dispatch: %v", err)
	}
	return want, dvAssertEffect(t, want, got)
}

// dvDispatchOne returns the active fixture Project outcome.
// Paused Projects can also report idle during the same tick.
func dvDispatchOne(db *sql.DB) (any, error) {
	raw, err := Dispatch(db)
	if err != nil {
		return nil, err
	}
	tick, ok := raw.(map[string]any)
	if !ok || tick["action"] != "tick" {
		return nil, fmt.Errorf("dispatch returned %v, want a tick object", raw)
	}
	actions, _ := tick["actions"].([]map[string]any)
	idle, _ := tick["idle"].([]map[string]any)
	activeIdle := idle[:0]
	for _, entry := range idle {
		if entry["reason"] != string(dispatch.IdleProjectInactive) || entry["project"] == "ws-test" {
			activeIdle = append(activeIdle, entry)
		}
	}
	idle = activeIdle
	switch {
	case len(actions) == 1 && len(idle) == 0:
		return actions[0], nil
	case len(actions) == 0 && len(idle) == 1:
		return map[string]any{"action": "idle", "reason": idle[0]["reason"], "project": idle[0]["project"]}, nil
	}
	return nil, fmt.Errorf("tick = %v, want exactly one pipeline outcome", raw)
}

// dvTickActions returns the tick's committed actions in tick order.
func dvTickActions(t *testing.T, raw any) []map[string]any {
	t.Helper()
	tick, ok := raw.(map[string]any)
	if !ok || tick["action"] != "tick" {
		t.Fatalf("dispatch returned %v, want a tick object", raw)
	}
	list, ok := tick["actions"].([]map[string]any)
	if !ok {
		t.Fatalf("tick actions = %#v, want a list of effects", tick["actions"])
	}
	return list
}

// dvTickIdle returns the tick's idle entries as project → reason.
func dvTickIdle(t *testing.T, raw any) map[string]string {
	t.Helper()
	tick, ok := raw.(map[string]any)
	if !ok || tick["action"] != "tick" {
		t.Fatalf("dispatch returned %v, want a tick object", raw)
	}
	list, ok := tick["idle"].([]map[string]any)
	if !ok {
		t.Fatalf("tick idle = %#v, want a list of entries", tick["idle"])
	}
	out := map[string]string{}
	for _, m := range list {
		out[fmt.Sprint(m["project"])] = fmt.Sprint(m["reason"])
	}
	return out
}

// TestDispatchSQLDifferentialActions is the SQL adapter differential:
// hand-built projections drive the frozen pure Decide oracle while equivalent
// rows drive the real SQL loader/action applier. Every Action kind must agree.
func TestDispatchSQLDifferentialActions(t *testing.T) {
	t.Run("idle_loads_nullable_last_heartbeat", func(t *testing.T) {
		db := dvSpine(t)
		task := dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2)
		dvInsertTask(t, db, task)
		dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject) VALUES ('fresh-run', 'pipeline', 'worker', 'ws-test', 1)`)
		lock := dvHold(t, db, "ws-test", "fresh-run", "worker", ptr64(1), 0, nil, time.Hour)
		want, _ := dvDispatch(t, db, dispatch.Records{Tasks: []dispatch.Task{task}}, lock, dvConfig())
		if want.Kind != dispatch.KindIdle || want.Idle != dispatch.IdleLeaseHeld {
			t.Fatalf("pure oracle = %+v", want)
		}
	})

	t.Run("spawn_arbiter_loads_null_task_fields_and_claims", func(t *testing.T) {
		db := dvSpine(t)
		task := dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2)
		dvInsertTask(t, db, task)
		want, got := dvDispatch(t, db, dispatch.Records{Tasks: []dispatch.Task{task}}, dispatch.Lease{}, dvConfig())
		if want.Kind != dispatch.KindSpawn || want.Spawn.Role != dispatch.RoleArbiter {
			t.Fatalf("pure oracle = %+v", want)
		}
		runID := got["run_id"].(string)
		var lockRun string
		var subject sql.NullInt64
		if err := db.QueryRow(`SELECT run_id, subject FROM leases WHERE project='ws-test'`).Scan(&lockRun, &subject); err != nil {
			t.Fatal(err)
		}
		if lockRun != runID || subject.Valid {
			t.Fatalf("claim lock = %q/%v, effect run=%q", lockRun, subject, runID)
		}
		var binding, pool string
		if err := db.QueryRow(`SELECT binding, pool_snapshot FROM runs WHERE id=?`, runID).Scan(&binding, &pool); err != nil {
			t.Fatal(err)
		}
		if binding != "codex/chatgpt" || pool != "[1]" {
			t.Fatalf("run binding/pool = %q/%q", binding, pool)
		}
	})

	t.Run("reap_retains_lease_until_stop_confirmation", func(t *testing.T) {
		db := dvSpine(t)
		task := dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2)
		dvInsertTask(t, db, task)
		dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject) VALUES ('dead-run', 'pipeline', 'worker', 'ws-test', 1)`)
		lock := dvHold(t, db, "ws-test", "dead-run", "worker", ptr64(1), 2*time.Hour, nil, time.Hour)
		want, _ := dvDispatch(t, db, dispatch.Records{Tasks: []dispatch.Task{task}}, lock, dvConfig())
		if want.Kind != dispatch.KindReap || want.Reap.Reason != dispatch.ReapSpawnWatchdog {
			t.Fatalf("pure oracle = %+v", want)
		}
		var retries int
		var outcome string
		if err := db.QueryRow(`SELECT dispatch_retries FROM tasks WHERE id=1`).Scan(&retries); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT outcome FROM runs WHERE id='dead-run'`).Scan(&outcome); err != nil {
			t.Fatal(err)
		}
		if retries != 3 || outcome != "reaped" {
			t.Fatalf("reap writes retries=%d outcome=%q", retries, outcome)
		}
		if count := dfInt(t, db, `SELECT infrastructure_failure_count FROM projects WHERE id='ws-test'`); count != 1 {
			t.Fatal("The unstarted Worker did not use the Project allowance")
		}
		var lockRun sql.NullString
		if err := db.QueryRow(`SELECT run_id FROM leases WHERE project='ws-test'`).Scan(&lockRun); err != nil || lockRun.String != "dead-run" {
			t.Fatalf("lease released before stop confirmation: run=%v err=%v", lockRun, err)
		}
		if _, err := RunStopped(db, nil, "dead-run"); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT run_id FROM leases WHERE project='ws-test'`).Scan(&lockRun); err != nil || lockRun.Valid {
			t.Fatalf("confirmed stop retained lease: run=%v err=%v", lockRun, err)
		}
	})
}

func TestDispatchSQLDifferentialSubjectlessNulls(t *testing.T) {
	db := dvSpine(t)
	want, got := dvDispatch(t, db, dispatch.Records{}, dispatch.Lease{}, dvConfig())
	if want.Kind != dispatch.KindSpawn || want.Spawn.Role != dispatch.RoleStrategist || want.Spawn.SubjectID != nil {
		t.Fatalf("an empty board should spawn subjectless propose: %+v", want)
	}
	runID := got["run_id"].(string)
	var subject sql.NullInt64
	var project sql.NullString
	if err := db.QueryRow(`SELECT subject, project FROM runs WHERE id=?`, runID).Scan(&subject, &project); err != nil {
		t.Fatal(err)
	}
	if subject.Valid || got["subject_id"] != nil {
		t.Fatalf("subjectless NULLs lost: db subject=%v effect=%v", subject, got)
	}
	// A subjectless run is still bound to the round's Project: runs.project
	// and the effect both name it.
	if !project.Valid || project.String != "ws-test" || got["project"] != "ws-test" {
		t.Fatalf("subjectless propose lost its Project: db ws=%v effect=%v", project, got)
	}
}

// Each tick evaluates every Project. Paused Projects can complete active work.
func TestDispatchTickRunsOneRoundPerProjectInIDOrder(t *testing.T) {
	db := dvSpine(t)
	dvAddProject(t, db, "a-first", "active")
	dvAddProject(t, db, "ws-paused", "paused")
	dvAddProject(t, db, "ws-held", "active")
	dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject) VALUES ('held-run', 'pipeline', 'worker', 'ws-held', NULL)`)
	dvHold(t, db, "ws-held", "held-run", "worker", nil, 0, new(time.Duration), time.Hour)
	first := dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2)
	first.Project = "a-first"
	dvInsertTask(t, db, first)
	dvInsertTask(t, db, dvTask(2, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	raw, err := Dispatch(db)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	actions := dvTickActions(t, raw)
	if len(actions) != 2 {
		t.Fatalf("tick actions = %v, want two spawns", actions)
	}
	for i, want := range []struct {
		project string
		task    int64
	}{{"a-first", 1}, {"ws-test", 2}} {
		got := actions[i]
		if got["action"] != "spawn" || got["role"] != string(dispatch.RoleArbiter) || got["project"] != want.project {
			t.Fatalf("action %d = %v, want an arbiter spawn for %s", i, got, want.project)
		}
		if _, ok := got["skill_store_dir"].(string); !ok {
			t.Fatalf("action %d carries no skill_store_dir: %v", i, got)
		}
		var holder string
		var subject sql.NullInt64
		if err := db.QueryRow(`SELECT run_id, subject FROM leases WHERE project=?`, want.project).Scan(&holder, &subject); err != nil {
			t.Fatalf("lease of %s: %v", want.project, err)
		}
		if holder != got["run_id"] || subject.Valid {
			t.Fatalf("lease of %s = %s/%v, want %v with proposal %d", want.project, holder, subject, got["run_id"], want.task)
		}
		var runProject string
		if err := db.QueryRow(`SELECT project FROM runs WHERE id=?`, holder).Scan(&runProject); err != nil || runProject != want.project {
			t.Fatalf("runs.project of %s = %q err=%v", holder, runProject, err)
		}
	}
	idle := dvTickIdle(t, raw)
	if len(idle) != 2 || idle["ws-held"] != string(dispatch.IdleLeaseHeld) || idle["ws-paused"] != string(dispatch.IdleProjectInactive) {
		t.Fatalf("tick idle = %v, want the held and paused Project results", idle)
	}
}

func TestDispatchSpawnBriefCarriesClaimedState(t *testing.T) {
	t.Run("arbiter_gets_full_proposal_records", func(t *testing.T) {
		db := dvSpine(t)
		task := dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2)
		task.Title = "contrast this proposal"
		dvInsertTask(t, db, task)
		dvExec(t, db, `UPDATE tasks SET description='criterion: exact output' WHERE id=1`)
		insertHistoricalTask(t, db, 2, "ws-test", "cancelled", "2000-01-01 00:00:00")
		insertHistoricalTask(t, db, 3, "ws-test", "rejected", "2000-01-01 00:00:00")
		dvExec(t, db, `INSERT INTO activity(actor,kind,subject,detail) VALUES('operator','task.cancelled','2','Low value.')`)
		_, got := dvDispatch(t, db, dispatch.Records{Tasks: []dispatch.Task{task}}, dispatch.Lease{}, dvConfig())
		brief := fmt.Sprint(got["brief"])
		for _, want := range []string{"contrast this proposal", "criterion: exact output", "## " + partPool, "Orchestrate by default."} {
			if !strings.Contains(brief, want) {
				t.Fatalf("Arbiter brief missing %q: %s", want, brief)
			}
		}
		rendered := parseRenderedBrief(t, brief)
		var cancelled, rejected []briefHistoricalTask
		rendered.part(partCancelled).rows(t, &cancelled)
		rendered.part(partRejected).rows(t, &rejected)
		if want := []briefHistoricalTask{{ID: 2, Title: "Recorded title 2 <&>", Decision: "cancelled", Reason: "Low value."}}; !reflect.DeepEqual(cancelled, want) {
			t.Fatalf("The Arbiter cancellation history = %v, want %v", cancelled, want)
		}
		if want := []briefHistoricalTask{{ID: 3, Title: "Recorded title 3 <&>", Decision: "rejected"}}; !reflect.DeepEqual(rejected, want) {
			t.Fatalf("The Arbiter rejection history = %v, want %v", rejected, want)
		}
	})

	// "Overlap with existing work is a rejection" is the Arbiter's job, so the
	// brief must carry the existing work: the pool is `proposed` only, so a
	// proposal an earlier batch promoted is otherwise invisible to the next
	// one, and two proposals that implement the same change in the same file
	// both get promoted. The compensating `mc task list` cannot substitute — it
	// omits `description` by design, which is exactly where the overlap lives.
	t.Run("arbiter_gets_the_existing_board_it_must_not_duplicate", func(t *testing.T) {
		db := dvSpine(t)
		// Blocked at `worked` — blocking also keeps its rank-3 row from
		// out-selecting the proposal and spawning a Verifier instead of the
		// Arbiter.
		inflight := dvTask(1, dispatch.ScopeTask, dispatch.StatusWorked, 2)
		inflight.Blocked = true
		dvInsertTask(t, db, inflight)
		dvExec(t, db, `UPDATE tasks SET description=? WHERE id=1`,
			"Files touched: backend/openrouter.py; backend/tests/test_openrouter.py (new)")
		queued := dvTask(2, dispatch.ScopeTask, dispatch.StatusPackaged, 2)
		dvInsertTask(t, db, queued)
		dvExec(t, db, `UPDATE tasks SET description='queued deliverable' WHERE id=2`)
		// A queued packet is published; the repository owes it nothing more.
		dvPublishPacket(t, db, 2, "", dvOld)
		proposal := dvTask(3, dispatch.ScopeTask, dispatch.StatusProposed, 2)
		dvInsertTask(t, db, proposal)
		dvExec(t, db, `UPDATE tasks SET description='the pool member' WHERE id=3`)
		// A live row of another Project is not on this Arbiter's board: the
		// board is its Project's only.
		dvExec(t, db, `INSERT INTO sandbox_profiles (id, workspace_root) VALUES ('ws-paused-profile', '/workspaces/ws-paused')`)
		dvExec(t, db, `INSERT INTO projects (id, title, sandbox_profile, status)
			VALUES ('ws-paused', 'Paused source', 'ws-paused-profile', 'paused')`)
		elsewhere := dvTask(4, dispatch.ScopeTask, dispatch.StatusWorked, 2)
		elsewhere.Project = "ws-paused"
		dvInsertTask(t, db, elsewhere)

		rec, err := loadVisionRecords(context.Background(), db, "ws-test")
		if err != nil {
			t.Fatal(err)
		}
		action, got := dvDispatch(t, db, rec, dispatch.Lease{}, dvConfig())
		if action.Kind != dispatch.KindSpawn || action.Spawn.Role != dispatch.RoleArbiter {
			t.Fatalf("fixture must spawn an Arbiter, got %+v", action)
		}
		doc := dvParseBrief(t, fmt.Sprint(got["brief"]))
		// Asserted as two DISJOINT id sets, never as a substring of the whole
		// brief: a `Contains("backend/openrouter.py")` check passes vacuously if
		// the live row leaks into the pool instead of the new carrier.
		if doc.ExistingWork == nil {
			t.Fatal("Arbiter brief carries no the Active tasks part carrier at all")
		}
		if ids := dvBriefIDs(*doc.ExistingWork); !reflect.DeepEqual(ids, []int64{1, 2}) {
			t.Fatalf("the Active tasks part ids = %v, want [1 2]", ids)
		}
		if ids := dvBriefIDs(doc.ProposedPool); !reflect.DeepEqual(ids, []int64{3}) {
			t.Fatalf("proposed_pool ids = %v, want [3]", ids)
		}
		if !strings.Contains((*doc.ExistingWork)[0].Description, "backend/openrouter.py") {
			t.Fatalf("the Active tasks part dropped the description overlap turns on: %+v",
				(*doc.ExistingWork)[0])
		}
	})

	// An empty board is an answer. A plain slice would omit the key entirely and
	// the role could not tell "nothing else exists" from "this brief version
	// does not carry it".
	t.Run("arbiter_gets_an_empty_existing_board_as_an_empty_array", func(t *testing.T) {
		db := dvSpine(t)
		only := dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2)
		dvInsertTask(t, db, only)
		rec, err := loadVisionRecords(context.Background(), db, "ws-test")
		if err != nil {
			t.Fatal(err)
		}
		_, got := dvDispatch(t, db, rec, dispatch.Lease{}, dvConfig())
		doc := dvParseBrief(t, fmt.Sprint(got["brief"]))
		if doc.ExistingWork == nil {
			t.Fatal("empty the Active tasks part must render as [], not vanish from the document")
		}
		if len(*doc.ExistingWork) != 0 {
			t.Fatalf("the Active tasks part should be empty here, got %v", dvBriefIDs(*doc.ExistingWork))
		}
	})

	// One stage upstream of the Arbiter's board above, and the same need: a
	// Strategist that gets rejected titles and nothing else cannot
	// see work that was PROMOTED or is in flight and keeps re-pitching it.
	// Title grain, not description grain, and that is a decision rather than
	// an economy — a generator needs "already handled", which is what a title
	// says; the Arbiter needs "same file", which only a description says.
	t.Run("propose_gets_active_titles_and_no_merged_title", func(t *testing.T) {
		db := dvSpine(t)
		// Every row here is UNDISPATCHABLE, or propose never fires: `packaged`
		// ranks 0, and a blocked proposal dispatches to nobody — which is also
		// the one shape that can leave a live `proposed` row visible to this
		// role at all, and so the reason the query carries no status filter.
		queued := dvTask(1, dispatch.ScopeTask, dispatch.StatusPackaged, 2)
		queued.Title = "queued deliverable"
		dvInsertTask(t, db, queued)
		// A queued packet is published; the repository owes it nothing more.
		dvPublishPacket(t, db, 1, "", dvOld)
		stuck := dvTask(2, dispatch.ScopeTask, dispatch.StatusProposed, 2)
		stuck.Title = "blocked in the pool"
		stuck.Blocked = true
		dvInsertTask(t, db, stuck)
		landed := dvTask(3, dispatch.ScopeTask, dispatch.StatusPackaged, 2)
		landed.Title = "already shipped last week"
		dvInsertTask(t, db, landed)
		// The substrate refuses an approval without its delivery evidence and
		// a live packet, so the landed row is built the way a real one exists.
		decided := dvNow(t, db)
		dvApproveTask(t, db, 3, &decided)
		dvExec(t, db, `UPDATE tasks SET archived=1 WHERE id=3`)
		rejected := dvTask(4, dispatch.ScopeTask, dispatch.StatusProposed, 2)
		rejected.Title = "declined by the Arbiter"
		dvInsertTask(t, db, rejected)
		dvExec(t, db, `UPDATE tasks
			SET decision='rejected', decided_at=datetime('now'), archived=1 WHERE id=4`)

		rec, err := loadVisionRecords(context.Background(), db, "ws-test")
		if err != nil {
			t.Fatal(err)
		}
		action, got := dvDispatch(t, db, rec, dispatch.Lease{}, dvConfig())
		if action.Kind != dispatch.KindSpawn || action.Spawn.Role != dispatch.RoleStrategist {
			t.Fatalf("fixture must fall through to Strategist, got %+v", action)
		}
		doc := dvParseBrief(t, fmt.Sprint(got["brief"]))
		if doc.ExistingTitles == nil {
			t.Fatal("propose brief carries no the Active tasks part carrier at all")
		}
		// Asserted as ids, and as sets DISJOINT from the rejected memory: a
		// substring check over the whole brief passes vacuously if the row
		// merely leaked into dedupe_titles, which is the carrier that already
		// worked.
		ids := make([]int64, 0, 3)
		for _, row := range *doc.ExistingTitles {
			ids = append(ids, row.ID)
			if row.Title == "" || row.Status == "" {
				t.Fatalf("the Active tasks part row carries no title/status: %+v", row)
			}
		}
		// Active rows furthest-first: the pool member outranks the queued
		// packet. The merged row is absent, because the Strategist reads merged
		// work in its copy of the repository.
		if !reflect.DeepEqual(ids, []int64{2, 1}) {
			t.Fatalf("active task ids = %v, want [2 1] (blocked-in-pool, queued)", ids)
		}
		if strings.Contains(fmt.Sprint(got["brief"]), "already shipped last week") {
			t.Fatalf("the Strategist brief holds a merged task: %s", got["brief"])
		}
		if want := []briefHistoricalTask{{ID: 4, Title: "declined by the Arbiter", Decision: "rejected"}}; !reflect.DeepEqual(doc.RejectedWork, want) {
			t.Fatalf("rejected history = %v, want %v", doc.RejectedWork, want)
		}
		// The description is deliberately absent, so an assertion on the whole
		// document is what proves the grain — a field-level check on a struct
		// that has no such field can never fail.
		brief := fmt.Sprint(got["brief"])
		if strings.Contains(brief, `"description"`) {
			t.Fatalf("propose brief must carry titles, not descriptions: %s", brief)
		}
		directive, _, _ := strings.Cut(brief, "</role_directive>")
		if !strings.Contains(directive, "**"+partActiveTasks+"**") {
			t.Fatalf("the propose directive does not name the %q part: %s", partActiveTasks, directive)
		}
	})

	t.Run("propose_gets_an_empty_board_as_an_empty_array", func(t *testing.T) {
		db := dvSpine(t)
		_, got := dvDispatch(t, db, dispatch.Records{}, dispatch.Lease{}, dvConfig())
		doc := dvParseBrief(t, fmt.Sprint(got["brief"]))
		if doc.ExistingTitles == nil {
			t.Fatal("empty the Active tasks part must render as [], not vanish from the document")
		}
		if len(*doc.ExistingTitles) != 0 {
			t.Fatalf("the Active tasks part should be empty here, got %+v", *doc.ExistingTitles)
		}
	})

	t.Run("worker_gets_revision_notes_and_latest_correction", func(t *testing.T) {
		db := dvSpine(t)
		task := dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2)
		dvInsertTask(t, db, task)
		dvExec(t, db, `UPDATE tasks SET revision_notes='deepen the risk proof', correction_count=1 WHERE id=1`)
		dvExec(t, db, `INSERT INTO runs
			(id, tier, role, project, subject, verdict_outcome, evidence_path, correction_path)
			VALUES ('prior-verifier', 'pipeline', 'verifier', 'ws-test', 1, 'correct', 'evidence/e1.md', 'corrections/c1.md')`)
		_, got := dvDispatch(t, db, dispatch.Records{Tasks: []dispatch.Task{task}}, dispatch.Lease{}, dvConfig())
		brief := fmt.Sprint(got["brief"])
		for _, want := range []string{"deepen the risk proof", "corrections/c1.md", "evidence/e1.md"} {
			if !strings.Contains(brief, want) {
				t.Fatalf("Worker brief missing %q: %s", want, brief)
			}
		}
	})

	t.Run("worker_does_not_get_an_answered_correction_after_operator_revision", func(t *testing.T) {
		db := dvSpine(t)
		task := dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2)
		dvInsertTask(t, db, task)
		dvExec(t, db, `UPDATE tasks SET revision_notes='deepen the risk proof', correction_count=1 WHERE id=1`)
		dvExec(t, db, `INSERT INTO runs
			(id, tier, role, project, subject, created_at, verdict_outcome, evidence_path, correction_path)
			VALUES ('old-correct', 'pipeline', 'verifier', 'ws-test', 1,
			        '2026-01-01 00:00:00', 'correct', 'evidence/old.md', 'corrections/answered.md')`)
		dvExec(t, db, `INSERT INTO runs
			(id, tier, role, project, subject, created_at, verdict_outcome, evidence_path)
			VALUES ('later-pass', 'pipeline', 'verifier', 'ws-test', 1,
			        '2026-01-01 00:01:00', 'pass', 'evidence/pass.md')`)
		_, got := dvDispatch(t, db, dispatch.Records{Tasks: []dispatch.Task{task}}, dispatch.Lease{}, dvConfig())
		brief := fmt.Sprint(got["brief"])
		if strings.Contains(brief, "## "+partCorrection) || strings.Contains(brief, "corrections/answered.md") {
			t.Fatalf("Operator revision revived an answered correction: %s", brief)
		}
		if !strings.Contains(brief, "deepen the risk proof") {
			t.Fatalf("The current revision notes disappeared with the stale correction: %s", brief)
		}
	})

	t.Run("packager_gets_the_latest_verdict_and_no_history", func(t *testing.T) {
		// The packet states the evidence of the latest verdict only, so the
		// Packager brief carries that verdict and no history.
		db := dvSpine(t)
		task := dvTask(1, dispatch.ScopeTask, dispatch.StatusVerified, 2)
		dvInsertTask(t, db, task)
		for _, r := range []struct{ id, at, outcome, ev, corr string }{
			{"v1", "2026-01-01 00:00:00", "correct", "verification/v1.md", "corrections/c1.md"},
			{"v2", "2026-01-01 00:01:00", "correct", "verification/v2.md", "corrections/c2.md"},
			{"v3", "2026-01-01 00:02:00", "pass", "verification/v3.md", ""},
		} {
			dvExec(t, db, `INSERT INTO runs
				(id, tier, role, project, subject, created_at, verdict_outcome, evidence_path, correction_path)
				VALUES (?, 'pipeline', 'verifier', 'ws-test', 1, ?, ?, ?, NULLIF(?, ''))`,
				r.id, r.at, r.outcome, r.ev, r.corr)
		}
		packager, err := buildSpawnBrief(context.Background(), db, &dispatch.Spawn{
			Project:   "ws-test",
			Role:      dispatch.RolePackager,
			SubjectID: &task.ID,
		}, "packager-history-run")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"## " + partLatestVerdict, "verification/v3.md"} {
			if !strings.Contains(packager, want) {
				t.Fatalf("Packager brief missing %q: %s", want, packager)
			}
		}
		for _, stale := range []string{
			"verification/v1.md", "corrections/c1.md",
			"verification/v2.md", "corrections/c2.md",
		} {
			if strings.Contains(packager, stale) {
				t.Fatalf("Packager brief carries the verdict history %q: %s", stale, packager)
			}
		}
	})

	t.Run("scribe_gets_the_range_and_the_merged_tasks_of_its_wiki_update", func(t *testing.T) {
		// The commit step of the wiki round builds the run brief and starts
		// the wiki update in one transaction. The run brief and the stored
		// merge marker thus state the same range.
		db := dvSpine(t)
		briefWikiSucceeded(t, db, wrCommitA)
		briefPackagedTask(t, db, 1, "merged task", "criterion: exact output")
		merge := briefMergeTask(t, db, 1)
		effect := wrRound(t, db, "ws-test", wrCommitB, wrCommitB)
		run := wrWantSpawn(t, effect, "ws-test")
		brief := parseRenderedBrief(t, fmt.Sprint(effect["brief"]))
		want := []struct {
			heading, explanation string
			facts                map[string]string
		}{
			{partCustomization, "These operator instructions replace standard defaults within the Culler contract. A contract conflict ends the wiki update as failed.",
				map[string]string{"Project": "ws-test", "Role": "scribe", "Customization revision": "0"}},
			{partRun, "This value identifies this run. It is the `<run_id>` value of each `mc` command.", map[string]string{"Run identifier": run}},
			{"Agent credentials", "The agent profile selects the harness, model, and effort; the same role input applies to Codex, Claude, and OpenCode. Culler keeps provider credentials on the host and supplies this container with temporary proxy access.", nil},
			{partProject, "This wiki update uses a fresh source workspace at `/workspace/source` and the persistent Project home. The Scribe omits automatic dependency preparation.", map[string]string{"Identifier": "ws-test"}},
			{partGitState, "Your repository at `/workspace/source` starts at the start commit. Maintain `openwiki/` from original repository evidence and preserve each file outside that folder.",
				map[string]string{"Start commit": wrCommitB, "Start commit of the last successful wiki update": wrCommitA}},
			{partMergedTasks, "This part lists the tasks whose merge is in the range between the two commits of the **Git state** part, oldest first. " +
				"When the `Omitted tasks` fact is present, the part omits that number of older tasks.", map[string]string{}},
		}
		if len(brief.Parts) != len(want)+1 {
			t.Fatalf("sections of the Scribe brief = %v, want the directive and %d parts", brief.headings(), len(want))
		}
		for i, part := range brief.Parts[1:] {
			if part.Heading != want[i].heading || part.Explanation != want[i].explanation {
				t.Errorf("part %d = %q with the explanation %q\nwant %q with the explanation %q",
					i+1, part.Heading, part.Explanation, want[i].heading, want[i].explanation)
			}
			// The customization part also holds the digest of the standard directive.
			if i > 0 && len(part.Facts) != len(want[i].facts) {
				t.Errorf("part %q holds the facts %v, want %v", part.Heading, part.Facts, want[i].facts)
			}
			for label, value := range want[i].facts {
				if part.Facts[label] != value {
					t.Errorf("part %q: fact %q = %q, want %q", part.Heading, label, part.Facts[label], value)
				}
			}
		}
		if rows := mergedTaskRows(t, brief); !reflect.DeepEqual(rows, []map[string]any{
			{"id": float64(1), "title": "merged task", "description": "criterion: exact output"}}) {
			t.Errorf("rows of the %s part = %v, want task 1", partMergedTasks, rows)
		}
		if got, want := dfInt(t, db, `SELECT merge_marker FROM wiki_updates WHERE run_id=?`, run),
			dfInt(t, db, `SELECT created_seq FROM runs WHERE id=?`, merge); got != want {
			t.Errorf("merge marker of the wiki update = %d, want the merge run %d of task 1", got, want)
		}
	})

	t.Run("verifier_judging_a_correction_round_can_name_the_property_it_rejected", func(t *testing.T) {
		// The Verifier that judges a round answering a CORRECT could not read
		// its own prior objection: the brief attached `latest_correction` to
		// the Worker only. Measured cost, packet
		// 80: round 1 was CORRECTed for evidence that "never reopened conv-a to
		// assert the streamed reply landed there", round 2 added exactly those
		// assertions, and they pass with the defect present — the mock keys on
		// the conversation id captured at send-time, which is the same in both
		// versions. The round answered the correction in form. Nobody in the
		// pipeline held the sentence that said what the form was for.
		db := dvSpine(t)
		task := dvTask(1, dispatch.ScopeTask, dispatch.StatusWorked, 2)
		dvInsertTask(t, db, task)
		dvExec(t, db, `UPDATE tasks SET correction_count=1 WHERE id=1`)
		dvExec(t, db, `INSERT INTO runs
			(id, tier, role, project, subject, created_at, verdict_outcome, evidence_path, correction_path)
			VALUES ('round-1-verifier', 'pipeline', 'verifier', 'ws-test', 1,
			        '2026-01-01 00:00:00', 'correct',
			        '/mc/artifacts/ws-test/1/verification/round1.md',
			        '/mc/artifacts/ws-test/1/corrections/round1.md')`)
		brief, err := buildSpawnBrief(context.Background(), db, &dispatch.Spawn{
			Project:   "ws-test",
			Role:      dispatch.RoleVerifier,
			SubjectID: &task.ID,
		}, "verifier-correction-round-run")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"## " + partCorrection,
			`/mc/artifacts/ws-test/1/corrections/round1.md`,
		} {
			if !strings.Contains(brief, want) {
				t.Fatalf("Verifier brief missing %q on a correction round: %s", want, brief)
			}
		}

		// The same filter the producers get, for the same reason: a later PASS
		// or BUDGET-SPENT answered the objection, and reviving it would have the
		// Verifier disclose a claim about a rally that is closed.
		dvExec(t, db, `INSERT INTO runs
			(id, tier, role, project, subject, created_at, verdict_outcome, evidence_path)
			VALUES ('round-2-verifier', 'pipeline', 'verifier', 'ws-test', 1,
			        '2026-01-01 00:01:00', 'pass',
			        '/mc/artifacts/ws-test/1/verification/round2.md')`)
		brief, err = buildSpawnBrief(context.Background(), db, &dispatch.Spawn{
			Project:   "ws-test",
			Role:      dispatch.RoleVerifier,
			SubjectID: &task.ID,
		}, "verifier-after-pass-run")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(brief, "## "+partCorrection) || strings.Contains(brief, "/corrections/round1.md") {
			t.Fatalf("Verifier brief revived an answered correction: %s", brief)
		}
	})

	t.Run("a_first_round_verifier_carries_no_correction_and_owes_no_disclosure", func(t *testing.T) {
		// The scope condition has to be visible in the brief, not inferred: on
		// a first round there is no prior verdict naming a property, so the
		// disclosure duty must not fire. An absent key is how the directive
		// knows.
		db := dvSpine(t)
		task := dvTask(1, dispatch.ScopeTask, dispatch.StatusWorked, 2)
		dvInsertTask(t, db, task)
		brief, err := buildSpawnBrief(context.Background(), db, &dispatch.Spawn{
			Project:   "ws-test",
			Role:      dispatch.RoleVerifier,
			SubjectID: &task.ID,
		}, "verifier-first-round-run")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(brief, "## "+partCorrection) {
			t.Fatalf("first-round Verifier brief carries a correction it cannot have: %s", brief)
		}
	})

	t.Run("worker_revision_keeps_the_last_worker_output_path", func(t *testing.T) {
		db := dvSpine(t)
		task := dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2)
		dvInsertTask(t, db, task)
		dvExec(t, db, `UPDATE tasks SET revision_notes='deepen the risk proof' WHERE id=1`)
		dvExec(t, db, `INSERT INTO runs
			(id, tier, role, project, subject, created_at, output_path, outcome)
			VALUES
			('prior-worker', 'pipeline', 'worker', 'ws-test', 1,
			 '2026-01-01 00:00:00', '/mc/artifacts/ws-test/1/output/report.md', 'completed'),
			('prior-packager', 'pipeline', 'packager', 'ws-test', 1,
			 '2026-01-01 00:01:00', '/mc/artifacts/ws-test/1/packet/review.md', 'completed')`)
		_, got := dvDispatch(t, db, dispatch.Records{Tasks: []dispatch.Task{task}}, dispatch.Lease{}, dvConfig())
		brief := fmt.Sprint(got["brief"])
		if !strings.Contains(brief, "- File: /mc/artifacts/ws-test/1/output/report.md") {
			t.Fatalf("Worker revision lost the last Worker output: %s", brief)
		}
		if revision := parseRenderedBrief(t, brief).part(partRevision); revision == nil || revision.Text != "deepen the risk proof" {
			t.Fatalf("Worker revision lost the requested revision: %s", brief)
		}
	})

	t.Run("verifier_gets_latest_worker_output", func(t *testing.T) {
		db := dvSpine(t)
		task := dvTask(1, dispatch.ScopeTask, dispatch.StatusWorked, 2)
		dvInsertTask(t, db, task)
		dvExec(t, db, `INSERT INTO runs
			(id, tier, role, project, subject, output_path, ended_at, outcome)
			VALUES ('prior-worker', 'pipeline', 'worker', 'ws-test', 1,
			        'outputs/worker-report.md', datetime('now'), 'completed')`)
		_, got := dvDispatch(t, db, dispatch.Records{Tasks: []dispatch.Task{task}}, dispatch.Lease{}, dvConfig())
		brief := fmt.Sprint(got["brief"])
		if !strings.Contains(brief, "- File: outputs/worker-report.md") {
			t.Fatalf("Verifier brief lost Worker output: %s", brief)
		}
	})

	// Each Strategist brief contains only its Project's tasks and repository path.
	t.Run("strategist_gets_history_of_its_project_only", func(t *testing.T) {
		db := dvSpine(t)
		dvAddProject(t, db, "ws-other", "active")
		decided := dvOld.Add(time.Hour)
		task := dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2)
		task.Title = "do not pitch this again"
		task.Decision, task.DecidedAt, task.Archived = dispatch.DecisionRejected, &decided, true
		dvInsertTask(t, db, task)
		otherRejected := dvTask(2, dispatch.ScopeTask, dispatch.StatusProposed, 2)
		otherRejected.Title, otherRejected.Project = "rejected elsewhere", "ws-other"
		otherRejected.Decision, otherRejected.DecidedAt, otherRejected.Archived = dispatch.DecisionRejected, &decided, true
		dvInsertTask(t, db, otherRejected)
		insertHistoricalTask(t, db, 4, "ws-test", "cancelled", "2000-01-01 00:00:00")
		insertHistoricalTask(t, db, 5, "ws-other", "cancelled", "2000-01-01 00:00:00")
		otherLive := dvTask(3, dispatch.ScopeTask, dispatch.StatusPackaged, 2)
		otherLive.Title, otherLive.Project = "queued elsewhere", "ws-other"
		dvInsertTask(t, db, otherLive)
		// A queued packet is published; the repository owes it nothing more.
		dvPublishPacket(t, db, 3, "", dvOld)
		raw, err := Dispatch(db)
		if err != nil {
			t.Fatalf("Dispatch: %v", err)
		}
		briefs := map[string]string{}
		for _, eff := range dvTickActions(t, raw) {
			if eff["role"] != string(dispatch.RoleStrategist) {
				t.Fatalf("tick action = %v, want a propose spawn per Project", eff)
			}
			briefs[fmt.Sprint(eff["project"])] = fmt.Sprint(eff["brief"])
		}
		if len(briefs) != 2 {
			t.Fatalf("tick spawned proposers for %v, want ws-other and ws-test", briefs)
		}
		brief := briefs["ws-test"]
		rendered := parseRenderedBrief(t, brief)
		var rejected, cancelled []briefHistoricalTask
		rendered.part(partRejected).rows(t, &rejected)
		rendered.part(partCancelled).rows(t, &cancelled)
		project := rendered.part(partProject)
		if !reflect.DeepEqual(rejected, []briefHistoricalTask{{ID: 1, Title: "do not pitch this again", Decision: "rejected"}}) || project == nil ||
			project.Facts["Identifier"] != "ws-test" || project.Facts["Repository copy"] != "/workspace/seeding/ws-test/source" {
			t.Fatalf("Strategist brief lost its own Project state: %s", brief)
		}
		if want := []briefHistoricalTask{{ID: 4, Title: "Recorded title 4 <&>", Decision: "cancelled"}}; !reflect.DeepEqual(cancelled, want) {
			t.Fatalf("The Strategist cancellation history = %v, want %v", cancelled, want)
		}
		if active := rendered.part(partActiveTasks); active == nil || active.Rows != "[]" {
			t.Fatalf("an empty board must render as an empty list: %s", brief)
		}
		for _, leak := range []string{"rejected elsewhere", "Recorded title 5 <&>", "queued elsewhere", "/workspace/seeding/ws-other/source", "seeding_scope"} {
			if strings.Contains(brief, leak) {
				t.Fatalf("ws-test Strategist brief leaked ws-other state %q: %s", leak, brief)
			}
		}
		other := briefs["ws-other"]
		for _, want := range []string{
			"rejected elsewhere", "Recorded title 5 <&>", "queued elsewhere", "- Repository copy: /workspace/seeding/ws-other/source",
		} {
			if !strings.Contains(other, want) {
				t.Fatalf("ws-other Strategist brief lost %q: %s", want, other)
			}
		}
		if strings.Contains(other, "do not pitch this again") || strings.Contains(other, "Recorded title 4 <&>") {
			t.Fatalf("ws-other Strategist brief leaked ws-test state: %s", other)
		}
	})
}

// Run ids are random, while spine timestamps have one-second precision. A
// lexical id tie-break can therefore revive an older output or verdict when
// two causal rounds start in the same second. Run creation order is durable
// and monotonic, so both brief carriers must use it.
func TestSpawnBriefLatestRunUsesCreationSequenceAtTheSameTimestamp(t *testing.T) {
	db := dvSpine(t)
	task := dvTask(1, dispatch.ScopeTask, dispatch.StatusVerified, 1)
	dvInsertTask(t, db, task)
	const sameSecond = "2026-08-06 12:00:00"
	dvExec(t, db, `INSERT INTO runs
		(id, tier, role, project, subject, created_at, output_path)
		VALUES ('z-older-worker', 'pipeline', 'worker', 'ws-test', 1, ?,
		        '/mc/artifacts/ws-test/1/output/older.md')`, sameSecond)
	dvExec(t, db, `INSERT INTO runs
		(id, tier, role, project, subject, created_at, output_path)
		VALUES ('a-newer-worker', 'pipeline', 'worker', 'ws-test', 1, ?,
		        '/mc/artifacts/ws-test/1/output/newer.md')`, sameSecond)
	dvExec(t, db, `INSERT INTO runs
		(id, tier, role, project, subject, created_at, verdict_outcome, evidence_path)
		VALUES ('z-older-verifier', 'pipeline', 'verifier', 'ws-test', 1, ?, 'correct',
		        '/mc/artifacts/ws-test/1/verification/older.md')`, sameSecond)
	dvExec(t, db, `INSERT INTO runs
		(id, tier, role, project, subject, created_at, verdict_outcome, evidence_path)
		VALUES ('a-newer-verifier', 'pipeline', 'verifier', 'ws-test', 1, ?, 'pass',
		        '/mc/artifacts/ws-test/1/verification/newer.md')`, sameSecond)

	brief, err := buildSpawnBrief(context.Background(), db, &dispatch.Spawn{
		Project: "ws-test",
		Role:    dispatch.RolePackager, SubjectID: &task.ID,
	}, "packager-run")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"- File: /mc/artifacts/ws-test/1/output/newer.md",
		"- Outcome: pass",
		"- Evidence file: /mc/artifacts/ws-test/1/verification/newer.md",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("same-second brief recency missing %q: %s", want, brief)
		}
	}
	if strings.Contains(brief, "output/older.md") {
		t.Fatalf("same-second brief revived the older output run: %s", brief)
	}
	if strings.Contains(brief, "verification/older.md") {
		t.Fatalf("same-second Packager brief revived the older verdict: %s", brief)
	}
}

// The harness hands the model exactly one thing — run.json.brief, on stdin, as
// "the ONE top-level turn" (runner/agent-runner/main.ts). Every role directive
// then instructs it to submit `--run <run_id>`, the mandatory fencing token a
// completion is rejected without. A brief that never names the
// run id therefore leaves `<run_id>` unresolvable for EVERY role: no terminal
// can be submitted, and each lease instead runs to its timeout and is reaped.
// Live subjectless runs refused to publish for exactly this reason,
// correctly declining to invent a fencing token they had no way to know.
//
// The whole e2e suite missed it because the fake harness family resolves the
// token out of band, through harnessEnv's MC_RUN_ID — a channel its own
// comment scopes to how "a scripted behavior comprehends its brief", and which
// no real adapter reads. The fixture agreed with its own declaration instead
// of with the consumer. So this asserts against the effect's own run_id — the
// id the resident goes on to name the container and write run.json with —
// never against a literal chosen here.
func TestSpawnBriefNamesTheRunIDEveryDirectiveDemands(t *testing.T) {
	fenceable := func(t *testing.T, got map[string]any) {
		t.Helper()
		runID := fmt.Sprint(got["run_id"])
		if runID == "" || runID == "<nil>" {
			t.Fatalf("spawn effect carries no run id to fence with: %v", got)
		}
		if brief := fmt.Sprint(got["brief"]); parseRenderedBrief(t, brief).part(partRun).Facts["Run identifier"] != runID {
			t.Fatalf("%v cannot resolve the --run token its directive demands (run %s): %s",
				got["role"], runID, brief)
		}
	}

	// A subject role and a subjectless one: the token is owed to every role, so
	// neither the subject branch nor its absence may gate it.
	t.Run("subject_role", func(t *testing.T) {
		db := dvSpine(t)
		task := dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2)
		dvInsertTask(t, db, task)
		_, got := dvDispatch(t, db, dispatch.Records{Tasks: []dispatch.Task{task}}, dispatch.Lease{}, dvConfig())
		fenceable(t, got)
	})

	t.Run("subjectless_propose", func(t *testing.T) {
		db := dvSpine(t)
		_, got := dvDispatch(t, db, dispatch.Records{}, dispatch.Lease{}, dvConfig())
		if got["role"] != string(dispatch.RoleStrategist) {
			t.Fatalf("role = %v, want propose", got["role"])
		}
		fenceable(t, got)
	})
}

func ptr64(v int64) *int64 { return &v }

// dvParseBrief reads the rendered run brief by its parts, the way an agent
// reads it. A part that is absent leaves its field nil.
type dvBriefTaskRef struct {
	ID          int64  `json:"id"`
	Description string `json:"description"`
}

type dvBriefTitleRef struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type dvBriefDoc struct {
	ProposedPool   []dvBriefTaskRef
	ExistingWork   *[]dvBriefTaskRef
	ExistingTitles *[]dvBriefTitleRef
	RejectedWork   []briefHistoricalTask
}

func dvParseBrief(t *testing.T, brief string) dvBriefDoc {
	t.Helper()
	rendered := parseRenderedBrief(t, brief)
	var doc dvBriefDoc
	rendered.part(partPool).rows(t, &doc.ProposedPool)
	if active := rendered.part(partActiveTasks); active != nil {
		doc.ExistingWork = &[]dvBriefTaskRef{}
		doc.ExistingTitles = &[]dvBriefTitleRef{}
		active.rows(t, doc.ExistingWork)
		active.rows(t, doc.ExistingTitles)
	}
	rendered.part(partRejected).rows(t, &doc.RejectedWork)
	return doc
}

func dvBriefIDs(tasks []dvBriefTaskRef) []int64 {
	ids := make([]int64, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

func loadVisionRecords(ctx context.Context, q Q, project string) (dispatch.Records, error) {
	sel, err := selectFromSpine(ctx, q, nil, project)
	return sel.rec, err
}
