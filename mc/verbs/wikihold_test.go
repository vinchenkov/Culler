package verbs

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"mc/dispatch"
	"mc/domain"
)

// packager runs the Packager run of a verified task to its end. The task is
// then packaged with no merge candidate.
func (f *deliveryFixture) packager(task int64) {
	f.t.Helper()
	run, root, id := f.run(task, "packager")
	packet := f.artifact(run, "The contribution satisfies the task criteria.")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "packaged", Outputs: packet, Thesis: "The contribution satisfies the task criteria."}); err != nil {
		f.t.Fatal(err)
	}
	f.cleanup(task, run, root)
}

// whSpawn requires that the tick starts the agent run of the role for the
// task, and that no repository operation holds the agent runs.
func whSpawn(t *testing.T, db *sql.DB, state string, role dispatch.Role, task int64) {
	t.Helper()
	d := wmDecide(t, db)
	spawn := d.action.Spawn
	if d.hold || d.action.Kind != dispatch.KindSpawn || spawn.Role != role || spawn.SubjectID == nil || *spawn.SubjectID != task {
		t.Fatalf("%s: tick results = %+v with the spawn %+v and the agent run hold %v; want the %s run of task %d and no hold",
			state, d.action, spawn, d.hold, role, task)
	}
}

// whHeld requires that a repository operation holds the agent runs, so that
// the tick starts no agent run. fact is the receipt recovery fact of the task records.
func whHeld(t *testing.T, db *sql.DB, state string, fact bool) {
	t.Helper()
	d := wmDecide(t, db)
	if !d.hold || d.action.Kind != dispatch.KindIdle || d.action.Idle != dispatch.IdleRepositoryOperation || d.rec.WikiReceiptRecovery != fact {
		t.Fatalf("%s: tick results = %+v with the agent run hold %v and the receipt recovery fact %v; want idle for a repository operation with the hold and the fact %v",
			state, d.action, d.hold, d.rec.WikiReceiptRecovery, fact)
	}
}

// The merge of a wiki commit stops after its journal write, so receipt
// recovery cannot prove its result. A task then reaches the stage packaged.
// Its merge candidate step waits and holds no agent run: each tick starts the
// next agent run, and each start request starts only receipt recovery of the
// wiki commit. After the operator repairs the checkout, receipt recovery
// proves the result, and the merge candidate step of the task starts.
func TestAgentRunsContinueWhileAWikiMergeHasNoProof(t *testing.T) {
	f := newDeliveryFixture(t)
	first := f.add("Deliver the first change")
	f.advance(first, "first.txt", "first change\n", "verified")
	second := f.add("Deliver the second change")
	clocks := fixClocks(t, f.db)
	testOwner(t, f.db)

	u := f.wikiUpdate()
	u.candidate()
	merge := wmStart(t, f.db, nil, "apply/wiki").Started[0]
	// The merge wrote its journal and reserved the index. It stopped before the update of culler.
	journal := filepath.Join(f.workspace, ".git/mc/applications", merge+".json")
	lock := filepath.Join(f.workspace, ".git/index.lock")
	body, err := json.Marshal(map[string]any{"application": executionEffect(t, f.db, merge)["application"], "index_device": 0, "index_inode": 0})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(journal), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, journal, string(body))
	writeFile(t, lock, "")
	stFail(t, f.db, merge, false)
	// unproven starts the due receipt recovery and requires a result with no proof.
	unproven := func(state string) {
		t.Helper()
		clocks.advance(domain.RetryDelay)
		if result := f.request("reconcile/wiki")[0]; result.Status != "operator-attention" {
			t.Fatalf("%s: receipt recovery = %+v, want operator-attention", state, result)
		}
	}
	unproven("the first receipt recovery")
	// System health states the repair of the checkout.
	refusal := "A merge into culler stopped before it updated the branch. If the file .git/index.lock exists, remove it. " +
		"Then discard each change in the checkout that you did not make, and remove the file .git/mc/applications/" + merge + ".json."
	if got := ssConditions(t, f.db); len(got) != 1 || got[0].ID != "wiki-merge:ws-test" || got[0].Action != refusal {
		t.Fatalf("system conditions = %+v, want one condition of the merge of the wiki commit with the repair %q", got, refusal)
	}

	// The Packager run of the first task ends. The task is then packaged with no merge candidate.
	whSpawn(t, f.db, "a verified task", dispatch.RolePackager, first)
	f.packager(first)
	for round := 1; round <= 3; round++ {
		state := fmt.Sprintf("round %d of the wait", round)
		// In the retry time of receipt recovery, the start request starts nothing.
		f.request()
		whSpawn(t, f.db, state, dispatch.RoleWorker, second)
		unproven(state)
		whSpawn(t, f.db, state, dispatch.RoleWorker, second)
	}

	// The Worker run of the second task ends during the wait. Its capture and
	// its cleanup start, and the tick then starts its Verifier run.
	run, root, id := f.run(second, "worker")
	commit := f.commit(root, "second.txt", "second change\n")
	if _, err := Complete(f.db, id, CompleteArgs{Task: second, Run: run, Status: "worked", Commit: commit}); err != nil {
		t.Fatal(err)
	}
	f.request(fmt.Sprintf("capture/%d", second))
	f.request(fmt.Sprintf("cleanup/%d", second))
	whSpawn(t, f.db, "a worked task", dispatch.RoleVerifier, second)
	if n := dfInt(t, f.db, `SELECT COUNT(*) FROM merge_operations WHERE task_id IS NOT NULL`); n != 0 {
		t.Fatalf("%d merge steps of a task started during the wait, want none", n)
	}

	// The operator repairs the checkout. Receipt recovery then proves that the merge did not run.
	for _, path := range []string{journal, lock} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	clocks.advance(domain.RetryDelay)
	if result := f.request("reconcile/wiki")[0]; result.Status != "not-applied" {
		t.Fatalf("receipt recovery after the repair = %+v, want not-applied", result)
	}
	if got, want := u.end(), wmFailed("Culler did not merge the wiki commit. The repository operation failed."); got != want {
		t.Fatalf("wiki update after the repair = %swant %s", got, want)
	}
	if got := ssConditions(t, f.db); len(got) != 0 {
		t.Fatalf("system conditions after the repair = %+v, want none", got)
	}
	// The merge candidate step of the first task is due. It holds the agent runs until it ends.
	whHeld(t, f.db, "the result of the merge", false)
	if result := f.request(fmt.Sprintf("prepare/%d", first))[0]; result.Candidate == nil || result.Candidate.Status != "merged" {
		t.Fatalf("merge candidate step after the repair = %+v, want a merged candidate", result)
	}
	whSpawn(t, f.db, "a merge candidate of the first task", dispatch.RoleVerifier, second)
}

// While the merge of a wiki commit runs, the task records hold no fact of
// it. A due merge candidate step of a task holds the agent runs for that
// time, as it does with no wiki update. After the result of the merge, the
// merge candidate step starts, and the next agent run follows it.
func TestMergeCandidateStepHoldsAgentRunsWhileAWikiMergeRuns(t *testing.T) {
	f := newDeliveryFixture(t)
	first := f.add("Deliver the first change")
	f.advance(first, "first.txt", "first change\n", "verified")
	second := f.add("Deliver the second change")
	fixClocks(t, f.db)
	testOwner(t, f.db)

	u := f.wikiUpdate()
	candidate := u.candidate()
	merge := wmStart(t, f.db, nil, "apply/wiki").Started[0]
	// The first task reaches the stage packaged while the merge runs.
	f.packager(first)
	whHeld(t, f.db, "an active merge of the wiki commit", false)
	f.request()

	if applied := f.runExecution(executionEffect(t, f.db, merge)).Applied; applied == nil || applied.Commit != candidate.Commit {
		t.Fatalf("merge of the wiki commit = %+v, want the merge at %s", applied, candidate.Commit)
	}
	if got, want := u.end(), wmSucceeded(u.startCommit, candidate.Commit); got != want {
		t.Fatalf("wiki update after the merge = %swant %s", got, want)
	}
	whHeld(t, f.db, "the result of the merge", false)
	if result := f.request(fmt.Sprintf("prepare/%d", first))[0]; result.Candidate == nil || result.Candidate.Status != "merged" {
		t.Fatalf("merge candidate step after the merge = %+v, want a merged candidate", result)
	}
	whSpawn(t, f.db, "a merge candidate of the first task", dispatch.RoleWorker, second)
}

// The Verifier requests a correction of a task. The corrected task reaches the
// stage packaged while receipt recovery cannot prove the merge of a wiki
// commit. Its merge candidate step waits, and the open correction then holds
// no Project priority: the tick starts the agent run of the next task. After
// the result of the merge, the merge candidate step of the corrected task is
// due and holds the agent runs until it ends.
func TestPackagedCorrectionHoldsNoAgentRunWhileAWikiMergeHasNoProof(t *testing.T) {
	f := newDeliveryFixture(t)
	first := f.add("Deliver the first change")
	f.advance(first, "first.txt", "first change\n", "worked")
	run, root, id := f.run(first, "verifier")
	if _, err := VerifierVerdict(f.db, id, VerdictArgs{Task: first, Run: run, Outcome: "correct", Evidence: f.artifact(run, "The check failed."),
		Correction: f.artifact(run+"-correction", "Correct the result."), Problem: "The result misses a criterion.",
		ExplanationEvidence: "The check failed.", Resolution: "Correct the result.", StoreWorktree: filepath.Join(root, "repo")}); err != nil {
		t.Fatal(err)
	}
	f.cleanup(first, run, root)
	second := f.add("Deliver the second change")
	clocks := fixClocks(t, f.db)
	testOwner(t, f.db)

	// The merge of the wiki commit ends with no result, and receipt recovery names a repair.
	u := f.wikiUpdate()
	u.candidate()
	stFail(t, f.db, wmStart(t, f.db, nil, "apply/wiki").Started[0], false)
	clocks.advance(domain.RetryDelay)
	u.record("reconcile", wmStart(t, f.db, nil, "reconcile/wiki").Started[0], RepositoryResult{Status: "operator-attention", OperatorAction: "Repair the checkout."})

	// The corrected task needs its Worker run, and the tick starts that run.
	whSpawn(t, f.db, "a correction that needs its Worker run", dispatch.RoleWorker, first)
	f.advance(first, "first.txt", "first change, corrected\n", "packaged")
	if open := dfInt(t, f.db, `SELECT correction_open FROM tasks WHERE id=? AND status='packaged' AND candidate_id IS NULL`, first); open != 1 {
		t.Fatalf("correction_open of the packaged task = %d, want an open correction", open)
	}
	whSpawn(t, f.db, "a packaged correction", dispatch.RoleWorker, second)
	// In the retry time of receipt recovery, the start request starts nothing.
	f.request()

	// Receipt recovery proves that the merge did not run. The merge candidate step of the corrected task is then due.
	clocks.advance(domain.RetryDelay)
	if result := f.request("reconcile/wiki")[0]; result.Status != "not-applied" {
		t.Fatalf("receipt recovery = %+v, want not-applied", result)
	}
	whHeld(t, f.db, "the result of the merge", false)
	if result := f.request(fmt.Sprintf("prepare/%d", first))[0]; result.Candidate == nil || result.Candidate.Status != "merged" {
		t.Fatalf("merge candidate step of the correction = %+v, want a merged candidate", result)
	}
	whSpawn(t, f.db, "a merged candidate of the correction", dispatch.RoleWorker, second)
}
