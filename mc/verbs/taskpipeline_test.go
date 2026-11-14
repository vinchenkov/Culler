package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc/dispatch"
	"mc/domain"
	"mc/repository"
)

// submitWorkerSource completes one Worker run with a commit and a report, and
// captures the submitted version. It returns the commit.
func submitWorkerSource(t *testing.T, f *deliveryFixture, task int64) string {
	t.Helper()
	run, root, id := f.run(task, "worker")
	repo := filepath.Join(root, "repo")
	writeFile(t, filepath.Join(repo, "feature.txt"), "The feature exists.\n")
	srcGit(t, repo, "add", ".")
	srcGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "Submit the source")
	commit := srcGit(t, repo, "rev-parse", "HEAD")
	report := filepath.Join(f.artifacts, "test", run+".md")
	writeFile(t, report, "The source report contains the complete accepted findings.\n")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "worked", Commit: commit, Outputs: report}); err != nil {
		t.Fatal(err)
	}
	if got := dfStr(t, f.db, `SELECT result_commit FROM version_submissions WHERE run_id=?`, run); got != commit {
		t.Fatalf("the Worker submitted %s, not %s", got, commit)
	}
	f.capture(task, run, root)
	return commit
}

// submitVerdict completes one Verifier run on the submitted version.
func submitVerdict(t *testing.T, f *deliveryFixture, task int64, outcome string) {
	t.Helper()
	run, root, id := f.run(task, "verifier")
	commit := srcGit(t, filepath.Join(root, "repo"), "rev-parse", "HEAD")
	evidence := filepath.Join(f.artifacts, "test", run+".md")
	writeFile(t, evidence, "The evidence identifies every required result.\n")
	a := VerdictArgs{Task: task, Run: run, Outcome: outcome, Evidence: evidence, StoreWorktree: filepath.Join(root, "repo")}
	if outcome == "pass" {
		a.SHA = commit
	} else {
		a.Correction = evidence
		a.Problem = "The result misses a criterion."
		a.ExplanationEvidence = "The check failed."
		a.Resolution = "Correct the result."
	}
	if _, err := VerifierVerdict(f.db, id, a); err != nil {
		t.Fatal(err)
	}
	f.cleanup(task, run, root)
}

// requireNextRole requires that the next dispatch decision starts the role.
func requireNextRole(t *testing.T, f *deliveryFixture, want dispatch.Role) {
	t.Helper()
	records, err := loadVisionRecords(context.Background(), f.db, "ws-test")
	if err != nil {
		t.Fatal(err)
	}
	action := dispatch.Decide(records, dispatch.Lease{}, dispatch.DefaultConfig(), dispatch.Clock{Now: time.Now()})
	if action.Spawn == nil || action.Spawn.Role != want {
		t.Fatalf("the next action is %+v; want %s", action, want)
	}
}

func TestVerifiedTaskGoesToPackager(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Retain the complete report")
	source := submitWorkerSource(t, f, task)
	requireNextRole(t, f, dispatch.RoleVerifier)
	submitVerdict(t, f, task, "pass")
	requireNextRole(t, f, dispatch.RolePackager)
	var submitted, verified string
	if err := f.db.QueryRow(`SELECT v.result_commit,s.result_commit FROM tasks d JOIN task_versions v ON v.id=d.submitted_version
		JOIN task_versions s ON s.id=d.source_version WHERE d.id=? AND d.submitted_version=d.source_version`, task).Scan(&submitted, &verified); err != nil || submitted != source || verified != source {
		t.Fatalf("the submitted version is not the commit that the Verifier passed: %s %s %v", submitted, verified, err)
	}
	run, root, id := f.run(task, "packager")
	if head := srcGit(t, filepath.Join(root, "repo"), "rev-parse", "HEAD"); head != source {
		t.Fatalf("the Packager received %s, not the verified commit %s", head, source)
	}
	report := filepath.Join(f.artifacts, "test", run+".html")
	writeFile(t, report, "<html>The packet identifies the Verifier pass.</html>")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "packaged", Outputs: report, Thesis: "The deliverable satisfies the task."}); err != nil {
		t.Fatal(err)
	}
	f.cleanup(task, run, root)
	if got := dfStr(t, f.db, `SELECT status FROM tasks WHERE id=?`, task); got != "packaged" {
		t.Fatalf("the task is %s after the Packager", got)
	}
	if got := dfInt(t, f.db, `SELECT count(*) FROM runs WHERE subject=? AND role NOT IN ('worker','verifier','packager')`, task); got != 0 {
		t.Fatalf("%d runs of another role used the task", got)
	}
}

func TestPackagerBriefHoldsTheVerifiedVersion(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("State the verified version")
	source := submitWorkerSource(t, f, task)
	submitVerdict(t, f, task, "pass")
	repo, err := loadRepositoryProjection(context.Background(), f.db, task)
	if err != nil {
		t.Fatal(err)
	}
	brief := briefFor(t, f.db, dispatch.RolePackager, briefSubject(dispatch.RolePackager, task), briefInputs{Repository: repo})
	state := brief.part(partGitState)
	if state == nil {
		t.Fatal("the Packager run brief holds no Git state part")
	}
	var got []string
	for _, fact := range state.Facts {
		got = append(got, fact.Label+"="+fact.Value)
	}
	want := []string{
		"Start commit=" + source, "Submitted commit=" + source,
		fmt.Sprintf("Submitted version=%d", dfInt(t, f.db, `SELECT submitted_version FROM tasks WHERE id=?`, task)),
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Git state facts = %v, want %v", got, want)
	}
}

func TestWorkerCorrectionPrecedesPackager(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Correct the task deliverable")
	submitWorkerSource(t, f, task)
	submitVerdict(t, f, task, "correct")
	requireNextRole(t, f, dispatch.RoleWorker)
	submitWorkerSource(t, f, task)
	submitVerdict(t, f, task, "pass")
	requireNextRole(t, f, dispatch.RolePackager)
}

func TestPackagerRequiresVerifierPass(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Verify the source before the packet")
	submitWorkerSource(t, f, task)
	run, _, id := f.run(task, "packager")
	report := filepath.Join(f.artifacts, "test", run+".html")
	writeFile(t, report, "<html>The packet cannot replace source verification.</html>")
	_, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "packaged", Outputs: report, Thesis: "The deliverable satisfies the task."})
	if err == nil || !strings.Contains(err.Error(), "verified submitted version") {
		t.Fatalf("the Packager bypassed source verification: %v", err)
	}
	if _, err := domain.Fence(context.Background(), f.db, run); err != nil {
		t.Fatal("the rejected terminal released the lease")
	}
}

func TestPackagerLeasePreventsAnotherActivePipelineStage(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Keep one active stage")
	submitWorkerSource(t, f, task)
	submitVerdict(t, f, task, "pass")
	run, _, _ := f.run(task, "packager")
	for i := 0; i < 3; i++ {
		records, err := loadVisionRecords(context.Background(), f.db, "ws-test")
		if err != nil {
			t.Fatal(err)
		}
		lease, err := loadLease(context.Background(), f.db, "ws-test")
		if err != nil {
			t.Fatal(err)
		}
		action := dispatch.Decide(records, lease, dispatch.DefaultConfig(), dvClock(t, f.db))
		if action.Spawn != nil || !lease.Held || lease.RunID != run {
			t.Fatalf("the Packager lease admitted another stage: %+v %+v", action, lease)
		}
	}
}

func TestPacketKeepsVerifierEvidenceThroughAcceptance(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Publish the verified work")
	source := submitWorkerSource(t, f, task)
	submitVerdict(t, f, task, "pass")
	requireNextRole(t, f, dispatch.RolePackager)
	run, root, id := f.run(task, "packager")
	report := filepath.Join(f.artifacts, "test", run+".html")
	writeFile(t, report, "<html>The packet identifies the Verifier pass.</html>")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "packaged", Outputs: report, Thesis: "The deliverable satisfies the task."}); err != nil {
		t.Fatal(err)
	}
	f.cleanup(task, run, root)
	f.merge(task, "prepare")
	packets, err := PacketList(f.db, PacketSelection{Live: true})
	if err != nil {
		t.Fatal(err)
	}
	packet := packets.(map[string]any)["packets"].([]map[string]any)[0]
	evidence := dfStr(t, f.db, `SELECT v.evidence_path FROM version_verdicts v JOIN tasks d ON d.submitted_version=v.version_id WHERE d.id=?`, task)
	if packet["verifier_evidence"] != evidence || packet["submitted_commit"] != source {
		t.Fatalf("the packet lost the Verifier evidence of its submitted version: %v", packet)
	}
	f.approve(task)
	f.merge(task, "apply")
	var verified string
	if err := f.db.QueryRow(`SELECT (SELECT result_commit FROM task_versions WHERE id=tasks.source_version) FROM tasks WHERE id=?`, task).Scan(&verified); err != nil || verified != source {
		t.Fatal(verified, err)
	}
	if got := dfInt(t, f.db, `SELECT count(*) FROM version_verdicts v JOIN task_versions t ON t.id=v.version_id WHERE t.task_id=?`, task); got != 1 {
		t.Fatal("the task acquired another Verifier verdict", got)
	}
	if srcGit(t, f.workspace, "show", "culler:feature.txt") == "" {
		t.Fatal("the verified work did not reach the accepted repository")
	}
}

func TestMergeCorrectionRequiresNewVerification(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Correct the combined merge result")
	f.pass(task, "feature.txt", "The first result requires a correction.\n")
	f.commitMain("feature.txt", "Another accepted contribution changed this file.\n")
	if candidate := f.merge(task, "prepare"); candidate.Status != "conflict" {
		t.Fatalf("the merge did not request a correction: %+v", candidate)
	}
	requireNextRole(t, f, dispatch.RoleWorker)
	var source sql.NullInt64
	if err := f.db.QueryRow(`SELECT source_version FROM tasks WHERE id=?`, task).Scan(&source); err != nil || source.Valid {
		t.Fatalf("the merge correction kept an obsolete verified source: %v %v", source, err)
	}
	submitWorkerSource(t, f, task)
	requireNextRole(t, f, dispatch.RoleVerifier)
	submitVerdict(t, f, task, "pass")
	requireNextRole(t, f, dispatch.RolePackager)
}

func TestInfrastructureRecoveryRetainsTheVerifiedSource(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Package the verified work")
	source := submitWorkerSource(t, f, task)
	submitVerdict(t, f, task, "pass")
	report := domain.InfrastructureFailureReport{
		Condition: "The harness startup deadline expired", Source: "runner", Cause: "unknown",
		Evidence: "Outcome: startup timeout; Harness error: unknown",
	}
	for attempt := 1; attempt <= 3; attempt++ {
		id := fhClaim(t, f.db, task, fmt.Sprintf("packager-startup-%d", attempt), "packager")
		result := fhReport(t, f.db, id, report)
		if result["remaining"] != 3-attempt {
			t.Fatalf("the attempt count differs: %v", result)
		}
		if _, err := RunInfrastructureFailure(f.db, nil, id.RunID, domain.InfrastructureFailureReport{
			Condition: "The container exited", Source: "resident", Cause: "unknown",
		}); err != nil {
			t.Fatal(err)
		}
		fhReap(t, f.db, id.RunID)
	}
	raw, err := TaskGet(f.db, task)
	if err != nil {
		t.Fatal(err)
	}
	got := raw.(map[string]any)
	latest := got["latest_infrastructure_failure"].(map[string]any)
	if got["stalled"] != int64(1) || got["infrastructure_failure_count"] != int64(3) || latest["evidence"] != report.Evidence {
		t.Fatalf("the task lost its failure evidence: %v", got)
	}
	var retained, status string
	if err := f.db.QueryRow(`SELECT v.result_commit,d.status FROM tasks d
		JOIN task_versions v ON v.id=d.source_version WHERE d.id=?`, task).Scan(&retained, &status); err != nil || retained != source || status != "verified" {
		t.Fatalf("the stalled task lost its stage: %s %s %v", retained, status, err)
	}
	if _, err := TaskResume(f.db, nil, task, "The repaired runtime is available."); err != nil {
		t.Fatal(err)
	}
	requireNextRole(t, f, dispatch.RolePackager)
	run, _, _ := f.run(task, "packager")
	var input string
	if err := f.db.QueryRow(`SELECT input_commit FROM repository_runs WHERE run_id=?`, run).Scan(&input); err != nil || input != source {
		t.Fatalf("the resumed Packager received another source: %s %v", input, err)
	}
}

// Two tasks start from one accepted commit and change different source
// files. The operator approves each task one time, and both merge with no
// conflict and no correction. The second task gets a new merge candidate at
// the commit of the first merge, with no new decision and no new agent run.
func TestTwoSourceTasksMergeWithNoCorrection(t *testing.T) {
	const quiet = 30 * time.Second
	f := newDeliveryFixture(t)
	start := f.mainCommit()
	first, second := f.add("Add the first source file"), f.add("Add the second source file")
	submitted := map[int64]string{
		first:  f.pass(first, "first.txt", "The first feature exists.\n"),
		second: f.pass(second, "second.txt", "The second feature exists.\n"),
	}
	for _, task := range []int64{first, second} {
		if candidate := f.merge(task, "prepare"); candidate.Status != "merged" || candidate.SubmittedCommit != submitted[task] || candidate.DestinationCommit != start {
			t.Fatalf("merge candidate of task %d = %+v, want the submitted commit %s merged at the start commit %s", task, candidate, submitted[task], start)
		}
	}
	if n := dfInt(t, f.db, `SELECT COUNT(*) FROM repository_runs p JOIN runs r ON r.id=p.run_id WHERE r.role='worker' AND r.binding IS NOT 'mechanical' AND p.input_commit=?`, start); n != 2 {
		t.Fatalf("%d Worker runs started at the commit %s, want both", n, start)
	}
	runs := dfInt(t, f.db, `SELECT COUNT(*) FROM runs WHERE binding IS NOT 'mechanical'`)
	clocks := fixClocks(t, f.db)
	f.approve(first)
	f.approve(second)

	name := func(operation string, task int64) string { return fmt.Sprintf("%s/%d", operation, task) }
	if applied := f.request(name("apply", first))[0].Applied; applied == nil {
		t.Fatal("the merge of the first task has no result")
	}
	afterFirst := f.mainCommit()
	if result := f.request(name("apply", second))[0]; result.Status != "destination-changed" || result.DestinationCommit != afterFirst {
		t.Fatalf("merge of the second task = %+v, want destination-changed at the commit %s of the first merge", result, afterFirst)
	}
	if resp := wmStart(t, f.db, nil); resp.DueInMS == nil || *resp.DueInMS != quiet.Milliseconds() {
		t.Fatalf("start response after the changed destination is due in %v ms, want %d", resp.DueInMS, quiet.Milliseconds())
	}
	clocks.advance(quiet)
	f.request(name("observe", second))
	if candidate := f.request(name("prepare", second))[0].Candidate; candidate == nil || candidate.Status != "merged" ||
		candidate.SubmittedCommit != submitted[second] || candidate.DestinationCommit != afterFirst {
		t.Fatalf("new merge candidate of the second task = %+v, want the submitted commit %s merged at %s", candidate, submitted[second], afterFirst)
	}
	f.request(name("apply", second), name("cleanup", second))

	for _, task := range []int64{first, second} {
		if got := wrDump(t, f.db, `SELECT archived,decision,blocked,stalled,correction_open,correction_count,infrastructure_failure_count FROM tasks WHERE id=?`, task); got !=
			"archived=1|decision=approved|blocked=0|stalled=0|correction_open=0|correction_count=0|infrastructure_failure_count=0|\n" {
			t.Fatalf("task %d after its merge = %s", task, got)
		}
		if n := dfInt(t, f.db, `SELECT COUNT(*) FROM review_decisions d JOIN review_versions p ON p.id=d.packet_id WHERE p.task_id=?`, task); n != 1 {
			t.Fatalf("%d review decisions of task %d, want the one approval", n, task)
		}
		if !(repository.Git{Root: f.workspace}).Ancestor(context.Background(), submitted[task], f.mainCommit()) {
			t.Fatalf("culler lacks the submitted commit %s of task %d", submitted[task], task)
		}
	}
	if n := dfInt(t, f.db, `SELECT (SELECT COUNT(*) FROM activity WHERE kind='task.merge_repair')+(SELECT COUNT(*) FROM merge_candidates WHERE status='conflict')`); n != 0 {
		t.Fatalf("%d merge repairs and conflicts, want none", n)
	}
	if n := dfInt(t, f.db, `SELECT COUNT(*) FROM runs WHERE binding IS NOT 'mechanical'`); n != runs {
		t.Fatalf("%d agent runs after both merges, want the %d runs before the approvals", n, runs)
	}
	for _, file := range []string{"first.txt", "second.txt"} {
		if _, err := os.Stat(filepath.Join(f.workspace, file)); err != nil {
			t.Fatalf("culler lacks %s after both merges: %v", file, err)
		}
	}
}

// A wiki commit enters culler after the Worker run of a task and before its
// merge candidate step. The review packet holds the commit that the Verifier
// passed, and its change data holds the source change only.
func TestReviewedChangeHoldsTheSourceChangeOnly(t *testing.T) {
	f := newDeliveryFixture(t)
	start := f.mainCommit()
	task := f.add("Deliver the source change")
	var wiki string
	f.afterCapture = func() {
		f.afterCapture = nil
		u := f.wikiUpdate()
		u.submit()
		f.request("capture/wiki")
		f.request("prepare/wiki", "cleanup/wiki")
		f.request("apply/wiki", "cleanup/wiki")
		wiki = f.mainCommit()
		if got, want := u.end(), wmSucceeded(start, wiki); got != want || wiki == start {
			t.Fatalf("wiki update = %swant %s with a new commit on culler", got, want)
		}
	}
	submitted := f.pass(task, "feature.txt", "complete feature\n")
	if changed := srcGit(t, f.workspace, "diff", "--name-only", start, wiki); changed != "openwiki/flow.md" {
		t.Fatalf("the wiki commit changes %q, want only openwiki/flow.md", changed)
	}
	candidate := f.merge(task, "prepare")
	if candidate.Status != "merged" || candidate.SubmittedCommit != submitted || candidate.DestinationCommit != wiki {
		t.Fatalf("merge candidate = %+v, want the submitted commit %s merged at the wiki commit %s", candidate, submitted, wiki)
	}

	packet := packetRow(t, f.db, true, task)
	if packet == nil || packet["submitted_commit"] != submitted {
		t.Fatalf("review packet = %v, want the commit %s that the Verifier passed", packet, submitted)
	}
	if got := dfStr(t, f.db, `SELECT v.result_commit FROM tasks d JOIN task_versions v ON v.id=d.source_version WHERE d.id=? AND d.submitted_version=d.source_version`, task); got != submitted {
		t.Fatalf("the submitted version is %s, want the commit %s that the Verifier passed", got, submitted)
	}
	changes, _ := packet["candidate_changes"].(map[string]any)
	if changes["from_commit"] != wiki || changes["to_commit"] != candidate.Commit || changes["files"] != float64(1) {
		t.Fatalf("change data of the packet = %v, want one file from the destination commit %s to the candidate commit %s", changes, wiki, candidate.Commit)
	}
	inventoryPath, _ := changes["inventory_path"].(string)
	body, err := os.ReadFile(inventoryPath)
	if err != nil {
		t.Fatalf("read the file inventory of the change data: %v", err)
	}
	var inventory repository.Comparison
	if err := json.Unmarshal(body, &inventory); err != nil {
		t.Fatalf("decode the file inventory %s: %v", body, err)
	}
	if len(inventory.Files) != 1 || inventory.Files[0].Path != "feature.txt" || inventory.Files[0].Status != "added" {
		t.Fatalf("files of the change data = %+v, want only the added source file feature.txt", inventory.Files)
	}
	patchPath, _ := changes["patch_path"].(string)
	patch, err := os.ReadFile(patchPath)
	if err != nil || !strings.Contains(string(patch), "+complete feature") || strings.Contains(string(patch), "openwiki/") {
		t.Fatalf("patch of the change data = %q, %v; want the source change with no wiki path", patch, err)
	}
	if n := dfInt(t, f.db, `SELECT COUNT(*) FROM runs WHERE subject=? AND role='scribe'`, task); n != 0 {
		t.Fatalf("%d Scribe runs have the task as their subject, want none", n)
	}
}
