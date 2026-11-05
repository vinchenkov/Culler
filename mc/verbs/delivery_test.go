package verbs

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc/dispatch"
	"mc/domain"
	"mc/repository"
)

type deliveryFixture struct {
	t         *testing.T
	db        *sql.DB
	workspace string
	// root holds the temporary repositories of tasks. wiki holds the
	// temporary repositories of wiki updates.
	root         string
	wiki         string
	artifacts    string
	sequence     int
	afterCapture func()
	afterPrepare func(repository.Candidate)
}

func newDeliveryFixture(t *testing.T) *deliveryFixture {
	db := dvSpine(t)
	f := &deliveryFixture{t: t, db: db, root: t.TempDir(), wiki: filepath.Join(t.TempDir(), wikiRepositories), artifacts: t.TempDir()}
	if err := db.QueryRow(`SELECT workspace_root FROM sandbox_profiles WHERE id='default'`).Scan(&f.workspace); err != nil {
		t.Fatal(err)
	}
	srcGit(t, f.workspace, "branch", "-M", "main")
	if err := os.MkdirAll(filepath.Join(f.workspace, "openwiki"), 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.workspace, "openwiki/project.md"), "---\ntitle: Test project\ntype: project\ndocument: true\n---\nThe repository supplies a test project.\n")
	writeFile(t, filepath.Join(f.workspace, "openwiki/index.md"), "---\ntitle: Test project\ntype: Index\n---\n# Test project\n\n[Project document](./project.md)\n")
	writeFile(t, filepath.Join(f.workspace, "openwiki/.last-update.json"), `{"status":"complete"}`)
	srcGit(t, f.workspace, "add", "openwiki")
	srcGit(t, f.workspace, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "Add the accepted project wiki")
	t.Setenv("MC_ARTIFACTS_ROOT", f.artifacts)
	return f
}

func (f *deliveryFixture) transaction(fn func(context.Context, Q) error) {
	f.t.Helper()
	if err := inTx(f.db, fn); err != nil {
		f.t.Fatal(err)
	}
}

func (f *deliveryFixture) add(title string) int64 {
	f.t.Helper()
	result, err := TaskAdd(f.db, nil, title, "ws-test", "The complete change satisfies the task criteria.", nil)
	if err != nil {
		f.t.Fatal(err)
	}
	id := result.(map[string]any)["task_id"].(int64)
	return id
}

func (f *deliveryFixture) run(task int64, role string) (string, string, *RunIdentity) {
	f.t.Helper()
	f.sequence++
	run := fmt.Sprintf("delivery-%d", f.sequence)
	f.transaction(func(ctx context.Context, q Q) error {
		_, err := domain.Claim(ctx, q, time.Now(), domain.ClaimArgs{RunID: run, Project: "ws-test", Owner: baseRole(role), SubjectID: &task, SessionPath: "sessions/" + run, Binding: "fake", HardDeadlineMinutes: 60})
		return err
	})
	var input, destination string
	if err := f.db.QueryRow(`SELECT COALESCE(cp.result_commit,v.result_commit,d.input_commit,''),d.destination_branch FROM tasks d LEFT JOIN task_versions cp ON cp.id=d.checkpoint_version LEFT JOIN task_versions v ON v.id=d.submitted_version WHERE d.id=?`, task).Scan(&input, &destination); err != nil {
		f.t.Fatal(err)
	}
	root := filepath.Join(f.root, run)
	result, err := executeRepository(context.Background(), RepositoryEnvelope{Operation: "ready", TaskID: task, RunID: run, WorkspaceRoot: f.workspace, RunRoot: root, InputCommit: input, DestinationBranch: destination})
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := RepositoryRecord(f.db, nil, "ready", run, result); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "repo"), filepath.Join(root, "source")); err != nil {
		f.t.Fatal(err)
	}
	f.t.Setenv("MC_TASK_STORE_ROOT", root)
	plane := filepath.Join(f.artifacts, "test")
	if err := os.MkdirAll(plane, 0700); err != nil {
		f.t.Fatal(err)
	}
	dvExec(f.t, f.db, `UPDATE runs SET agent_started_at=datetime('now') WHERE id=?`, run)
	return run, root, &RunIdentity{RunID: run, Tier: "pipeline", Role: role, Mounts: RunMounts{AllowWrite: []string{plane}}}
}

// commitMain commits one file change to main in the Project repository.
func (f *deliveryFixture) commitMain(file, content string) string {
	f.t.Helper()
	writeFile(f.t, filepath.Join(f.workspace, file), content)
	srcGit(f.t, f.workspace, "add", file)
	srcGit(f.t, f.workspace, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "Advance the accepted destination")
	return srcGit(f.t, f.workspace, "rev-parse", "main")
}

// cleanup starts the cleanup of the temporary repository of a run, executes it, and records it.
func (f *deliveryFixture) cleanup(task int64, run, _ string) {
	f.t.Helper()
	f.runExecution(startOperation(f.t, f.db, dispatch.RepositoryOperation{Project: "ws-test", TaskID: task, Operation: dispatch.OperationCleanup, RunID: run}))
}

// captureSubmission starts the capture of the submitted version of a run, executes it, and records it.
func (f *deliveryFixture) captureSubmission(task int64, run string) {
	f.t.Helper()
	f.runExecution(startOperation(f.t, f.db, dispatch.RepositoryOperation{Project: "ws-test", TaskID: task, Operation: dispatch.OperationCapture, RunID: run}))
}

func (f *deliveryFixture) pass(task int64, file, content string) string {
	f.t.Helper()
	run, root, id := f.run(task, "worker")
	repo := filepath.Join(root, "repo")
	var correction bool
	if err := f.db.QueryRow(`SELECT correction_open FROM tasks WHERE id=?`, task).Scan(&correction); err != nil {
		f.t.Fatal(err)
	}
	if correction {
		var destination string
		if err := f.db.QueryRow(`SELECT destination_commit FROM repository_runs WHERE run_id=?`, run).Scan(&destination); err != nil {
			f.t.Fatal(err)
		}
		cmd := exec.Command("git", "-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "merge", "--no-commit", "--no-ff", destination)
		_, _ = cmd.CombinedOutput()
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, file)), 0700); err != nil {
		f.t.Fatal(err)
	}
	writeFile(f.t, filepath.Join(repo, file), content)
	srcGit(f.t, repo, "add", ".")
	srcGit(f.t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "Complete the contribution")
	git := repository.Git{Root: repo}
	commit, err := git.Commit(context.Background(), "HEAD")
	if err != nil {
		f.t.Fatal(err)
	}
	f.submit(task, run, root, id, commit)
	return commit
}

func (f *deliveryFixture) submit(task int64, run, root string, id *RunIdentity, commit string) {
	f.t.Helper()
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "worked", Commit: commit}); err != nil {
		f.t.Fatal(err)
	}
	f.captureSubmission(task, run)
	f.cleanup(task, run, root)
	if f.afterCapture != nil {
		f.afterCapture()
	}
	for _, role := range []string{"verifier", "packager"} {
		run, root, id = f.run(task, role)
		got, err := (repository.Git{Root: filepath.Join(root, "repo")}).Commit(context.Background(), "HEAD")
		if err != nil || got != commit {
			f.t.Fatalf("%s received %s, %v", role, got, err)
		}
		report := filepath.Join(f.artifacts, "test", run+".html")
		writeFile(f.t, report, "<html><body>The contribution satisfies the complete task criteria.</body></html>")
		if role == "verifier" {
			_, err = VerifierVerdict(f.db, id, VerdictArgs{Task: task, Run: run, Outcome: "pass", SHA: commit, Evidence: report, StoreWorktree: filepath.Join(root, "repo")})
		} else {
			_, err = Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "packaged", Outputs: report, Thesis: "The contribution satisfies the complete task criteria."})
		}
		if err != nil {
			f.t.Fatal(err)
		}
		f.cleanup(task, run, root)
	}
}

// merge starts one merge candidate step or one merge, executes it, and records
// it. It cleans the temporary repository of a merge candidate step. It
// returns the candidate of the step.
func (f *deliveryFixture) merge(task int64, operation string) repository.Candidate {
	f.t.Helper()
	effect := startOperation(f.t, f.db, dispatch.RepositoryOperation{Project: "ws-test", TaskID: task, Operation: dispatch.Operation(operation)})
	result := f.runExecution(effect)
	if operation != "prepare" {
		return effect["application"].(repository.Application).Candidate
	}
	f.cleanup(task, effect["run_id"].(string), "")
	return *result.Candidate
}

func TestDeliveryUsesExactVersionsAndMergesBeforeOperatorReview(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Deliver the standalone change")
	var main string
	f.afterCapture = func() {
		writeFile(t, filepath.Join(f.workspace, "independent.txt"), "another accepted contribution\n")
		srcGit(t, f.workspace, "add", "independent.txt")
		srcGit(t, f.workspace, "commit", "-qm", "Advance main before verification")
		main = srcGit(t, f.workspace, "rev-parse", "main")
	}
	commit := f.pass(task, "feature.txt", "complete feature\n")
	var packets int
	if err := f.db.QueryRow(`SELECT count(*) FROM tasks t JOIN review_versions p ON p.id=t.current_review_id WHERE task_id=?`, task).Scan(&packets); err != nil {
		t.Fatal(err)
	}
	if packets != 0 {
		t.Fatal("the report became reviewable before the merge")
	}
	candidate := f.merge(task, "prepare")
	if candidate.SubmittedCommit != commit {
		t.Fatal("the candidate changed the submitted version")
	}
	if candidate.DestinationCommit != main || main == commit {
		t.Fatal("preparation omitted the independent main update")
	}
	if got := srcGit(t, f.workspace, "rev-parse", "main"); got != main {
		t.Fatal("preparation advanced main before approval")
	}
	var packet, version int64
	if err := f.db.QueryRow(`SELECT id,version_id FROM review_versions WHERE task_id=?`, task).Scan(&packet, &version); err != nil {
		t.Fatal(err)
	}
	if _, err := PacketDecide(f.db, nil, task, "approve", "", packet, version+1); err == nil {
		t.Fatal("an obsolete version received approval")
	}
	if _, err := PacketDecide(f.db, nil, task, "approve", "", packet, version); err != nil {
		t.Fatal(err)
	}
	f.merge(task, "apply")
	var archived bool
	if err := f.db.QueryRow(`SELECT archived FROM tasks WHERE id=?`, task).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	if !archived {
		t.Fatal("the accepted task remains incomplete")
	}
	main, err := (repository.Git{Root: f.workspace}).Branch(context.Background(), "main")
	if err != nil || main != candidate.Commit {
		t.Fatalf("main differs from the merged candidate: %s, %v", main, err)
	}
}

func (f *deliveryFixture) approve(task int64) (int64, int64) {
	f.t.Helper()
	var packet, version int64
	if err := f.db.QueryRow(`SELECT p.id,p.version_id FROM review_versions p JOIN tasks d ON d.submitted_version=p.version_id WHERE p.task_id=?`, task).Scan(&packet, &version); err != nil {
		f.t.Fatal(err)
	}
	if _, err := PacketDecide(f.db, nil, task, "approve", "", packet, version); err != nil {
		f.t.Fatal(err)
	}
	return packet, version
}

func TestDeliveryApprovalSurvivesOnlyMechanicalMainChanges(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Preserve the submitted change")
	version := f.pass(task, "feature.txt", "complete\n")
	f.merge(task, "prepare")
	packet, versionID := f.approve(task)
	writeFile(t, filepath.Join(f.workspace, "independent.txt"), "another accepted contribution\n")
	srcGit(t, f.workspace, "add", ".")
	srcGit(t, f.workspace, "commit", "-qm", "Advance main independently")
	f.merge(task, "apply")
	var infra int
	if err := f.db.QueryRow(`SELECT infrastructure_failure_count FROM tasks WHERE id=?`, task).Scan(&infra); err != nil {
		t.Fatal(err)
	}
	if infra != 0 {
		t.Fatal("a destination change consumed an infrastructure attempt")
	}
	next := f.merge(task, "prepare")
	if next.SubmittedCommit != version {
		t.Fatal("mechanical preparation changed the submitted version")
	}
	f.merge(task, "apply")
	var decisions int
	if err := f.db.QueryRow(`SELECT count(*) FROM review_decisions WHERE packet_id=?`, packet).Scan(&decisions); err != nil || decisions != 1 {
		t.Fatal("mechanical preparation changed the approval")
	}
	if !(repository.Git{Root: f.workspace}).Ancestor(context.Background(), version, next.Commit) {
		t.Fatal("the update lost the approved version")
	}
	if versionID < 1 {
		t.Fatal("the approval omitted its version")
	}
}

func TestDeliveryMovedDestinationGivesNewChangeData(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Show the changes of the current merge candidate")
	f.pass(task, "feature.txt", "complete\n")
	first := f.merge(task, "prepare")
	changes := func() map[string]any {
		record, _ := packetRow(t, f.db, false, task)["candidate_changes"].(map[string]any)
		return record
	}
	published := changes()
	if published["from_commit"] != first.DestinationCommit || published["to_commit"] != first.Commit || published["files"] != float64(1) {
		t.Fatalf("the packet omitted the change data of its merge candidate: %v", published)
	}
	f.approve(task)
	moved := f.commitMain("independent.txt", "another accepted contribution\n")
	f.merge(task, "apply")
	// The task has no current merge candidate until the next merge candidate step.
	if waiting := changes(); fmt.Sprint(waiting) != fmt.Sprint(published) {
		t.Fatalf("the packet lost the change data of its merge candidate: %v", waiting)
	}
	next := f.merge(task, "prepare")
	current := changes()
	if next.Commit == first.Commit || current["from_commit"] != moved || current["to_commit"] != next.Commit || current["files"] != float64(1) {
		t.Fatalf("the change data omitted the moved destination: %v", current)
	}
}

func TestPacketKeepsTheChangeDataOfItsVersion(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Deliver the original requirements")
	f.pass(task, "feature.txt", "The original result.\n")
	first := f.merge(task, "prepare")
	boardRevision(t, f.db, task, "Revised requirements")
	// Another task holds the only review slot, so the next version of the task gets no packet.
	if _, err := f.db.Exec(`UPDATE settings SET review_wip_cap=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	other := f.add("Hold the review slot")
	f.pass(other, "other.txt", "another contribution\n")
	f.merge(other, "prepare")
	f.pass(task, "feature.txt", "The revised result.\n")
	second := f.merge(task, "prepare")
	row := packetRow(t, f.db, false, task)
	changes, _ := row["candidate_changes"].(map[string]any)
	if second.Commit == first.Commit || row["review_state"] != "replaced" || changes["to_commit"] != first.Commit {
		t.Fatalf("the replaced packet shows the change data of a different version: %v", row)
	}
}

func TestDeliveryStartsAMergeRepairForEachConflict(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Preserve the required result")
	f.pass(task, "answer.txt", "worker 0\n")
	if candidate := f.merge(task, "prepare"); candidate.Status != "merged" {
		t.Fatalf("the first merge candidate had a conflict: %+v", candidate)
	}
	f.approve(task)
	for attempt := 1; attempt <= 3; attempt++ {
		f.commitMain("answer.txt", fmt.Sprintf("main %d\n", attempt))
		if attempt == 1 {
			f.merge(task, "apply")
		}
		if candidate := f.merge(task, "prepare"); candidate.Status != "conflict" {
			t.Fatalf("attempt %d: the controlled Git conflict did not occur: %+v", attempt, candidate)
		}
		var status, kind string
		var approved, blocked, open bool
		if err := f.db.QueryRow(`SELECT status,COALESCE(decision='approved',0),blocked,correction_open,returned_kind FROM tasks WHERE id=?`, task).
			Scan(&status, &approved, &blocked, &open, &kind); err != nil {
			t.Fatal(err)
		}
		if status != "seeded" || approved || blocked || !open || kind != "merge" {
			t.Fatalf("attempt %d: the conflict did not start a merge repair: %s, approved %v, blocked %v, open %v, %s", attempt, status, approved, blocked, open, kind)
		}
		f.pass(task, "answer.txt", fmt.Sprintf("worker %d\n", attempt))
	}
	if candidate := f.merge(task, "prepare"); candidate.Status != "merged" {
		t.Fatalf("the repair did not merge: %+v", candidate)
	}
	var open bool
	if err := f.db.QueryRow(`SELECT correction_open FROM tasks WHERE id=?`, task).Scan(&open); err != nil || open {
		t.Fatalf("the merge candidate kept the correction open: %v, %v", open, err)
	}
	f.approve(task)
	f.merge(task, "apply")
	var history int
	if err := f.db.QueryRow(`SELECT count(*) FROM activity WHERE kind='task.merge_repair' AND subject=?`, task).Scan(&history); err != nil || history != 3 {
		t.Fatalf("the activity lost a merge repair: %d, %v", history, err)
	}
}

func TestDeliveryRecoversOriginalAuthorizationAfterDatabaseReportLoss(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Recover the exact update")
	f.pass(task, "recovery.txt", "retained\n")
	f.merge(task, "prepare")
	f.approve(task)
	effect := startOperation(t, f.db, dispatch.RepositoryOperation{Project: "ws-test", TaskID: task, Operation: dispatch.OperationApply})
	original := effect["run_id"].(string)
	app := effect["application"].(repository.Application)
	applied, err := repository.Apply(context.Background(), f.workspace, app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DeliveryFail(f.db, nil, original, "The database report connection closed.", false); err != nil {
		t.Fatal(err)
	}
	effect = startOperation(t, f.db, dispatch.RepositoryOperation{Project: "ws-test", TaskID: task, Operation: dispatch.OperationReconcile, RunID: original})
	retry := effect["run_id"].(string)
	recovery := effect["application"].(repository.Application)
	if effect["previous_run"] != original || recovery.Authorization != original {
		t.Fatal("recovery changed the original authorization")
	}
	recovered, err := repository.ReconcileApplication(context.Background(), f.workspace, recovery)
	if err != nil || recovered == nil || !recovered.Recovered || recovered.Commit != applied.Commit {
		t.Fatalf("recovery lost the original receipt: %+v, %v", recovered, err)
	}
	if _, err := RepositoryRecord(f.db, nil, "reconcile", retry, RepositoryResult{TaskID: task, Applied: recovered}); err != nil {
		t.Fatal(err)
	}
	var archived bool
	if err := f.db.QueryRow(`SELECT archived FROM tasks WHERE id=?`, task).Scan(&archived); err != nil || !archived {
		t.Fatal("recovery left the accepted task incomplete")
	}
}

func TestDeliveryDispatchBuildsTheIsolatedRepositoryBrief(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Dispatch an isolated Worker")
	value, err := dvDispatchOne(f.db)
	if err != nil {
		t.Fatal(err)
	}
	effect := value.(map[string]any)
	if effect["action"] != "spawn" || effect["role"] != "worker" {
		t.Fatalf("dispatch did not select the Worker: %#v", effect)
	}
	plan := effect["mount_plan"].(*PrivateDispatchMountPlan)
	if plan.Repository == nil || plan.Repository.TaskID != task || plan.Repository.WorkspaceRoot != f.workspace {
		t.Fatalf("dispatch omitted the repository plan: %+v", plan)
	}
	if err := validatePrivateMountPlan(plan); err != nil {
		t.Fatal(err)
	}
	for _, entry := range plan.Entries {
		if strings.HasPrefix(entry.Destination, "/workspace") {
			t.Fatal("the agent received a persistent checkout")
		}
	}
	text := effect["brief"].(string)
	brief := parseRenderedBrief(t, text)
	if !strings.Contains(brief.Header, spawnBriefSchema) {
		t.Fatalf("the immutable brief omitted its schema identifier: %q", brief.Header)
	}
	if state := brief.part(partGitState); state == nil || state.Facts["Destination branch"] != "main" {
		t.Fatalf("the Worker brief omitted the destination branch: %+v", state)
	}
	for _, fact := range []string{"max_temporary_bytes", "merge_after", "report_sha256", "temporary_repositories"} {
		if strings.Contains(text, fact) {
			t.Fatalf("the Worker brief holds %q, which no agent uses:\n%s", fact, text)
		}
	}
}

func TestDeliveryCheckpointSurvivesCaptureCleanupAndGitCleanup(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Resume a retained checkpoint")
	run, root, id := f.run(task, "worker")
	repo := filepath.Join(root, "repo")
	writeFile(t, filepath.Join(repo, "checkpoint.txt"), "retained progress\n")
	srcGit(t, repo, "add", ".")
	srcGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "Save the progress")
	commit, err := (repository.Git{Root: repo}).Commit(context.Background(), "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Checkpoint: true, Commit: commit}); err != nil {
		t.Fatal(err)
	}
	effect := startOperation(t, f.db, dispatch.RepositoryOperation{Project: "ws-test", TaskID: task, Operation: dispatch.OperationCapture, RunID: run})
	if effect["submission_kind"] != "checkpoint" || effect["producer_run"] != run {
		t.Fatalf("the capture lost its checkpoint facts: %+v", effect)
	}
	facts := f.runExecution(effect)
	if facts.RetainedRef != fmt.Sprintf("refs/mc/tasks/%d/checkpoints/%s", task, run) {
		t.Fatalf("the checkpoint was retained at %q", facts.RetainedRef)
	}
	f.cleanup(task, run, root)
	srcGit(t, f.workspace, "gc", "--prune=now")
	next, nextRoot, _ := f.run(task, "worker")
	head, err := (repository.Git{Root: filepath.Join(nextRoot, "repo")}).Commit(context.Background(), "HEAD")
	if err != nil || head != commit {
		t.Fatalf("the Worker lost the retained checkpoint: %s, %v", head, err)
	}
	var status string
	if err := f.db.QueryRow(`SELECT status FROM tasks WHERE id=?`, task).Scan(&status); err != nil || status != "seeded" {
		t.Fatal("the checkpoint completed the Worker stage")
	}
	if next == run {
		t.Fatal("the retry reused its earlier run")
	}
}
