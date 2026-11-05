package verbs

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"mc/domain"
	"mc/repository"
)

type dispatchLifecycle struct {
	*deliveryFixture
	trace []string
	// started holds each started execution that next did not return yet.
	started []map[string]any
}

type lifecycleRun struct {
	task  int64
	run   string
	root  string
	input string
	role  string
	id    *RunIdentity
}

func newDispatchLifecycle(t *testing.T) *dispatchLifecycle {
	t.Helper()
	return &dispatchLifecycle{deliveryFixture: newDeliveryFixture(t)}
}

// next returns the next effect in the order that the resident receives it:
// each execution that a start request starts, then the result of one tick.
func (f *dispatchLifecycle) next() map[string]any {
	f.t.Helper()
	if len(f.started) == 0 {
		for _, run := range startRequest(f.t, f.db, false).Started {
			effect := executionEffect(f.t, f.db, run)
			effect["action"] = "repository"
			f.started = append(f.started, effect)
		}
	}
	var effect map[string]any
	if len(f.started) > 0 {
		effect, f.started = f.started[0], f.started[1:]
	} else {
		value, err := dvDispatchOne(f.db)
		if err != nil {
			f.t.Fatal(err)
		}
		effect = value.(map[string]any)
		if effect["action"] == "repository" {
			f.t.Fatalf("the tick returned a repository operation: %#v", effect)
		}
	}
	name, _ := effect["action"].(string)
	switch name {
	case "spawn":
		name += ":" + effect["role"].(string)
	case "repository":
		name += ":" + effect["operation"].(string)
	case "idle":
		name += ":" + effect["reason"].(string)
	case "reap":
		name += ":" + effect["reason"].(string)
	default:
		f.t.Fatalf("dispatch returned an unexpected effect: %#v", effect)
	}
	f.trace = append(f.trace, name)
	return effect
}

func (f *dispatchLifecycle) execute(envelope RepositoryEnvelope) RepositoryResult {
	f.t.Helper()
	result, err := executeRepository(context.Background(), envelope)
	if err != nil {
		f.t.Fatalf("the %s operation failed: %v", envelope.Operation, err)
	}
	return result
}

func (f *dispatchLifecycle) record(operation, run string, result RepositoryResult) {
	f.t.Helper()
	if _, err := RepositoryRecord(f.db, nil, operation, run, result); err != nil {
		f.t.Fatalf("the %s result failed: %v", operation, err)
	}
}

// start supplies the repository and identity from the authorized spawn effect.
func (f *dispatchLifecycle) start(effect map[string]any) lifecycleRun {
	f.t.Helper()
	if effect["action"] != "spawn" {
		f.t.Fatalf("the next effect requires an agent spawn: %#v", effect)
	}
	run := effect["run_id"].(string)
	role := effect["role"].(string)
	plan := effect["mount_plan"].(*PrivateDispatchMountPlan).Repository
	if plan == nil {
		f.t.Fatalf("the spawn lacks its repository: %#v", effect)
	}
	root := filepath.Join(f.root, run)
	envelope := RepositoryEnvelope{
		Operation: "allocate", TaskID: plan.TaskID, RunID: run, RunRoot: root,
		WorkspaceRoot: plan.WorkspaceRoot, InputCommit: plan.InputCommit,
		DestinationBranch: plan.DestinationBranch,
		VersionID:         plan.VersionID, MaxTemporaryBytes: plan.MaxTemporaryBytes,
	}
	allocated := f.execute(envelope)
	f.record("ready", run, allocated)
	envelope.Operation = "ready"
	envelope.InputCommit, envelope.DestinationCommit = allocated.InputCommit, allocated.DestinationCommit
	f.record("ready", run, f.execute(envelope))
	if err := os.Symlink(filepath.Join(root, "repo"), filepath.Join(root, "source")); err != nil {
		f.t.Fatal(err)
	}
	f.t.Setenv("MC_TASK_STORE_ROOT", root)
	plane := filepath.Join(f.artifacts, "test")
	if err := os.MkdirAll(plane, 0700); err != nil {
		f.t.Fatal(err)
	}
	return lifecycleRun{
		task: plan.TaskID, run: run, root: root, input: allocated.InputCommit, role: role,
		id: &RunIdentity{RunID: run, Tier: "pipeline", Role: role, Mounts: RunMounts{AllowWrite: []string{plane}}},
	}
}

func (f *dispatchLifecycle) submit(run lifecycleRun, commit string) {
	f.t.Helper()
	if _, err := Complete(f.db, run.id, CompleteArgs{Task: run.task, Run: run.run, Status: "worked", Commit: commit}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *dispatchLifecycle) passRole(run lifecycleRun) {
	f.t.Helper()
	var err error
	report := f.artifact(run.run, "The contribution satisfies the task criteria.")
	switch run.role {
	case "verifier":
		_, err = VerifierVerdict(f.db, run.id, VerdictArgs{
			Task: run.task, Run: run.run, Outcome: "pass", SHA: run.input,
			Evidence: report, StoreWorktree: filepath.Join(run.root, "repo"),
		})
	case "packager":
		_, err = Complete(f.db, run.id, CompleteArgs{
			Task: run.task, Run: run.run, Status: "packaged", Outputs: report,
			Thesis: "The contribution satisfies the task criteria.",
		})
	default:
		f.t.Fatalf("the fixture has no result for role %q", run.role)
	}
	if err != nil {
		f.t.Fatalf("the %s terminal failed: %v", run.role, err)
	}
}

// repository executes one started execution and records its result.
func (f *dispatchLifecycle) repository(effect map[string]any) RepositoryResult {
	f.t.Helper()
	if effect["action"] != "repository" {
		f.t.Fatalf("the next effect requires a repository operation: %#v", effect)
	}
	return f.runExecution(effect)
}

func (f *dispatchLifecycle) approve(task int64) (int64, int64) {
	f.t.Helper()
	var packet, version int64
	if err := f.db.QueryRow(`SELECT id,version_id FROM review_versions WHERE task_id=? ORDER BY id DESC LIMIT 1`, task).Scan(&packet, &version); err != nil {
		f.t.Fatal(err)
	}
	if _, err := PacketDecide(f.db, nil, task, "approve", "", packet, version); err != nil {
		f.t.Fatal(err)
	}
	return packet, version
}

func TestDispatchLifecyclePreservesSubmittedVersionWhenMainMoves(t *testing.T) {
	f := newDispatchLifecycle(t)
	task := f.add("Complete the dispatched contribution")
	var submitted, movedMain string
	var candidate repository.Candidate
	completed := false
	for step := 0; step < 24; step++ {
		effect := f.next()
		switch effect["action"] {
		case "spawn":
			run := f.start(effect)
			if run.task != task {
				t.Fatalf("dispatch selected task %d instead of task %d", run.task, task)
			}
			if run.role == "worker" {
				submitted = f.commit(run.root, "feature.txt", "The task supplies this contribution.\n")
				f.submit(run, submitted)
			} else {
				if run.input != submitted {
					t.Fatalf("the %s input is %s; the submitted version is %s", run.role, run.input, submitted)
				}
				f.passRole(run)
			}
		case "repository":
			result := f.repository(effect)
			switch effect["operation"] {
			case "capture":
				if movedMain == "" {
					writeFile(t, filepath.Join(f.workspace, "independent.txt"), "Another task supplies this contribution.\n")
					srcGit(t, f.workspace, "add", "independent.txt")
					srcGit(t, f.workspace, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "Advance main after the Worker result")
					movedMain = srcGit(t, f.workspace, "rev-parse", "main")
				}
			case "prepare":
				candidate = *result.Candidate
				if candidate.Status != "merged" || candidate.SubmittedCommit != submitted || candidate.DestinationCommit != movedMain {
					t.Fatalf("the candidate lacks the exact merge inputs: %+v", candidate)
				}
				if got := srcGit(t, f.workspace, "rev-parse", "main"); got != movedMain {
					t.Fatal("preparation changed main before approval")
				}
				f.approve(task)
			case "apply":
				completed = true
			}
		default:
			t.Fatalf("the lifecycle stopped before acceptance: %#v", effect)
		}
		if completed {
			break
		}
	}
	if !completed {
		t.Fatalf("the lifecycle exceeded its action limit: %v", f.trace)
	}
	want := []string{
		"spawn:worker", "repository:capture", "spawn:verifier", "spawn:packager",
		"repository:prepare", "repository:apply",
	}
	var progression []string
	for _, action := range f.trace {
		if action != "repository:cleanup" {
			progression = append(progression, action)
		}
	}
	if !reflect.DeepEqual(progression, want) {
		t.Fatalf("the lifecycle differs: got %v; want %v", progression, want)
	}
	var archived bool
	if err := f.db.QueryRow(`SELECT archived FROM tasks WHERE id=?`, task).Scan(&archived); err != nil || !archived {
		t.Fatalf("the accepted task remains active: archived=%v, error=%v", archived, err)
	}
	if got := srcGit(t, f.workspace, "rev-parse", "main"); got != candidate.Commit {
		t.Fatalf("main is %s; the merged candidate is %s", got, candidate.Commit)
	}
	for _, file := range []string{"feature.txt", "independent.txt"} {
		if _, err := os.Stat(filepath.Join(f.workspace, file)); err != nil {
			t.Fatalf("main lacks %s: %v", file, err)
		}
	}
}

func TestDispatchLifecycleSelectsCurrentMainForANewTask(t *testing.T) {
	f := newDispatchLifecycle(t)
	task := f.add("Start from the current destination")
	writeFile(t, filepath.Join(f.workspace, "new-main.txt"), "This change precedes the first run.\n")
	srcGit(t, f.workspace, "add", "new-main.txt")
	srcGit(t, f.workspace, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "Advance main before the first run")
	main := srcGit(t, f.workspace, "rev-parse", "main")
	run := f.start(f.next())
	if run.task != task || run.role != "worker" || run.input != main {
		t.Fatalf("the first Worker received another input: %+v", run)
	}
}

func TestDispatchLifecycleApprovalMergesWhileAWorkerRuns(t *testing.T) {
	f := newDispatchLifecycle(t)
	first := f.add("Accept the first contribution")
	firstCandidate := f.candidate(first, func(run lifecycleRun) {
		f.submit(run, f.commit(run.root, "first.txt", "The first task supplies this contribution.\n"))
	})
	if firstCandidate.Status != "merged" {
		t.Fatalf("the first candidate failed: %+v", firstCandidate)
	}
	second := f.add("Preserve the active Worker contribution")
	worker := f.requireWorker(second)
	f.approve(first)
	if result := f.apply(first); result.Applied == nil || result.Applied.Commit != firstCandidate.Commit {
		t.Fatalf("the approval waited for the active Worker: %+v", result)
	}
	if lease := dfStr(t, f.db, `SELECT COALESCE(run_id,'') FROM leases WHERE project='ws-test'`); lease != worker.run {
		t.Fatalf("the merge changed the Project lease: %q", lease)
	}
	if got := srcGit(t, f.workspace, "rev-parse", "main"); got != firstCandidate.Commit {
		t.Fatal("main differs from the approved candidate")
	}
	if effect := f.next(); effect["action"] != "idle" || effect["reason"] != "lease-held" {
		t.Fatalf("the merge interrupted the active Worker: %#v", effect)
	}
	secondCommit := f.commit(worker.root, "second.txt", "The second task supplies this contribution.\n")
	f.submit(worker, secondCommit)
	capture := f.next()
	if capture["action"] != "repository" || capture["operation"] != "capture" || capture["task_id"] != second {
		t.Fatalf("the completed Worker lost its capture: %#v", capture)
	}
	f.repository(capture)
	verifier := f.start(f.nextWork())
	if verifier.task != second || verifier.role != "verifier" || verifier.input != secondCommit {
		t.Fatalf("the resumed task lost its exact submitted version: %+v", verifier)
	}
	f.passRole(verifier)
	secondCandidate := f.candidate(second, nil)
	if secondCandidate.Status != "merged" || secondCandidate.SubmittedCommit != secondCommit || secondCandidate.DestinationCommit != firstCandidate.Commit {
		t.Fatalf("the later candidate omitted the approved merge: %+v", secondCandidate)
	}
	if worker.input == firstCandidate.Commit {
		t.Fatal("the fixture started the Worker after the approved merge")
	}
	if got := srcGit(t, f.workspace, "rev-parse", "main"); got != firstCandidate.Commit {
		t.Fatal("the second task changed main before approval")
	}
}

func TestDispatchLifecycleRecoversAnAppliedMergeAfterLostAcknowledgment(t *testing.T) {
	f := newDispatchLifecycle(t)
	task := f.add("Recover the authorized branch update")
	candidate := f.candidate(task, func(run lifecycleRun) {
		f.submit(run, f.commit(run.root, "recovery.txt", "The contribution survives the lost result.\n"))
	})
	if candidate.Status != "merged" {
		t.Fatalf("the candidate failed: %+v", candidate)
	}
	f.approve(task)
	effect := f.nextWork()
	if effect["action"] != "repository" || effect["operation"] != "apply" || effect["task_id"] != task {
		t.Fatalf("the start request omitted the approved merge: %#v", effect)
	}
	original := effect["run_id"].(string)
	application := effect["application"].(repository.Application)
	version := effect["version_id"].(int64)
	applied := f.execute(RepositoryEnvelope{
		Operation: "apply", TaskID: task, RunID: original, WorkspaceRoot: f.workspace,
		DestinationBranch: "main", Application: &application,
		InputCommit: effect["submitted_commit"].(string), VersionID: &version,
	})
	if applied.Applied == nil || applied.Applied.Recovered {
		t.Fatalf("the first merge failed: %+v", applied)
	}
	clocks := fixClocks(t, f.db)
	if _, err := DeliveryFail(f.db, nil, original, "The database result connection closed after the branch update.", false); err != nil {
		t.Fatal(err)
	}
	if _, err := RepositoryRecord(f.db, nil, "apply", original, applied); err == nil {
		t.Fatal("a late merge record completed the merge after its execution ended")
	}
	if effect := f.nextWork(); effect["action"] == "repository" {
		t.Fatalf("receipt recovery started before the retry time ended: %#v", effect)
	}
	clocks.advance(domain.RetryDelay + time.Second)
	recovery := f.nextWork()
	if recovery["action"] != "repository" || recovery["operation"] != "reconcile" || recovery["task_id"] != task {
		t.Fatalf("the start request omitted receipt recovery: %#v", recovery)
	}
	if recovery["previous_run"] != original || recovery["run_id"] == original || recovery["application"].(repository.Application).Authorization != original {
		t.Fatal("receipt recovery changed the original merge authorization")
	}
	result := f.repository(recovery)
	if result.Applied == nil || !result.Applied.Recovered || result.Applied.Commit != candidate.Commit {
		t.Fatalf("receipt recovery lost the original branch receipt: %+v", result)
	}
	before := dfStr(t, f.db, `SELECT result FROM merge_operations WHERE run_id=?`, original)
	f.record("reconcile", recovery["run_id"].(string), result)
	f.record("apply", original, applied)
	if after := dfStr(t, f.db, `SELECT result FROM merge_operations WHERE run_id=?`, original); after != before {
		t.Fatalf("a repeated record changed the merge result: %s", after)
	}
	var archived bool
	if err := f.db.QueryRow(`SELECT archived FROM tasks WHERE id=?`, task).Scan(&archived); err != nil || !archived {
		t.Fatalf("recovery left the accepted task active: %v, %v", archived, err)
	}
	if got := srcGit(t, f.workspace, "rev-parse", "main"); got != candidate.Commit {
		t.Fatal("recovery replaced the accepted branch result")
	}
}
