package verbs

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc/repository"
)

func (f *dispatchLifecycle) nextWork() map[string]any {
	f.t.Helper()
	for step := 0; step < 64; step++ {
		effect := f.next()
		if effect["action"] != "repository" || effect["operation"] != "cleanup" {
			return effect
		}
		f.repository(effect)
	}
	f.t.Fatalf("repository cleanup exceeded its action limit: %v", f.trace)
	return nil
}

func (f *dispatchLifecycle) candidate(task int64, worker func(lifecycleRun)) repository.Candidate {
	f.t.Helper()
	for step := 0; step < 24; step++ {
		effect := f.nextWork()
		switch effect["action"] {
		case "spawn":
			run := f.start(effect)
			if run.task != task {
				f.t.Fatalf("task %d requires completion before task %d starts", task, run.task)
			}
			if run.role == "worker" {
				if worker == nil {
					f.t.Fatal("the unchanged version unexpectedly requires a Worker")
				}
				worker(run)
			} else {
				f.passRole(run)
			}
		case "repository":
			if effect["task_id"] != task {
				f.t.Fatalf("dispatch selected another task's repository: %#v", effect)
			}
			result := f.repository(effect)
			if effect["operation"] == "prepare" {
				return *result.Candidate
			}
		default:
			f.t.Fatalf("the task stopped before its candidate: %#v", effect)
		}
	}
	f.t.Fatalf("the candidate exceeded its action limit: %v", f.trace)
	return repository.Candidate{}
}

func (f *dispatchLifecycle) apply(task int64) RepositoryResult {
	f.t.Helper()
	effect := f.nextWork()
	if effect["action"] != "repository" || effect["operation"] != "apply" || effect["task_id"] != task {
		f.t.Fatalf("the approved task requires application: %#v", effect)
	}
	return f.repository(effect)
}

func (f *dispatchLifecycle) expeditedTask() int64 {
	f.t.Helper()
	priority := -1
	value, err := TaskAdd(f.db, nil, "Complete independent expedited work", "ws-test", "The independent contribution passes its checks.", &priority)
	if err != nil {
		f.t.Fatal(err)
	}
	return value.(map[string]any)["task_id"].(int64)
}

func (f *dispatchLifecycle) independentTask() int64 {
	f.t.Helper()
	value, err := TaskAdd(f.db, nil, "Complete independent work", "ws-test", "The independent contribution passes its checks.", nil)
	if err != nil {
		f.t.Fatal(err)
	}
	return value.(map[string]any)["task_id"].(int64)
}

func (f *dispatchLifecycle) requireHold() {
	f.t.Helper()
	effect := f.nextWork()
	if effect["action"] != "idle" {
		f.t.Fatalf("the unresolved priority task must hold its Project: %#v", effect)
	}
}

func (f *dispatchLifecycle) requireWorker(task int64) lifecycleRun {
	f.t.Helper()
	run := f.start(f.nextWork())
	if run.task != task || run.role != "worker" {
		f.t.Fatalf("task %d requires its Worker: %+v", task, run)
	}
	return run
}

func (f *dispatchLifecycle) reconcile(run lifecycleRun, file, content string) string {
	f.t.Helper()
	var destination string
	if err := f.db.QueryRow(`SELECT destination_commit FROM repository_runs WHERE run_id=?`, run.run).Scan(&destination); err != nil {
		f.t.Fatal(err)
	}
	root := filepath.Join(run.root, "repo")
	command := exec.Command("git", "-C", root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "merge", "--no-commit", "--no-ff", destination)
	if output, err := command.CombinedOutput(); err != nil && !strings.Contains(string(output), "CONFLICT") {
		f.t.Fatalf("the correction could not combine its inputs: %v: %s", err, output)
	}
	commit := f.commit(run.root, file, content)
	git := repository.Git{Root: root}
	if !git.Ancestor(context.Background(), run.input, commit) || !git.Ancestor(context.Background(), destination, commit) {
		f.t.Fatal("the correction lost one of its assigned inputs")
	}
	f.submit(run, commit)
	return commit
}

func TestDispatchCorrectionBeforeAndAfterApproval(t *testing.T) {
	for _, approved := range []bool{false, true} {
		t.Run(fmt.Sprintf("approved=%t", approved), func(t *testing.T) {
			f := newDispatchLifecycle(t)
			clocks := fixClocks(t, f.db)
			f.commitMain("answer.txt", "original\n")
			if _, err := ConfigureRepositoryLimits(f.db, nil, "ws-test", RepositoryLimits{QuietSeconds: 1}); err != nil {
				t.Fatal(err)
			}
			task := f.add("Reconcile the complete contribution")
			moveMain := func() string { return f.commitMain("answer.txt", "another accepted answer\n") }
			var original, destination string
			first := f.candidate(task, func(run lifecycleRun) {
				original = f.commit(run.root, "answer.txt", "submitted\n")
				f.submit(run, original)
				if !approved {
					destination = moveMain()
				}
			})
			var oldPacket, oldVersion int64
			var independent int64
			if approved {
				if first.Status != "merged" {
					t.Fatalf("the initial candidate failed: %+v", first)
				}
				oldPacket, oldVersion = f.approve(task)
				independent = f.independentTask()
				destination = moveMain()
				if result := f.apply(task); result.Status != "destination-changed" {
					t.Fatalf("application missed the changed destination: %+v", result)
				}
				f.requireHold()
				clocks.advance(time.Second)
				first = f.candidate(task, nil)
			} else {
				independent = f.independentTask()
			}
			if first.Status != "conflict" || first.SubmittedCommit != original || first.DestinationCommit != destination || len(first.ConflictPaths) == 0 {
				t.Fatalf("preparation lost the conflict inputs: %+v", first)
			}
			if approved && dfStr(t, f.db, `SELECT COALESCE(decision,'') FROM tasks WHERE id=?`, task) != "" {
				t.Fatal("the conflict kept the approval")
			}
			if approved {
				row := packetRow(t, f.db, true, task)
				if row == nil || row["review_state"] != "returned" || row["return_cause"] != "conflict" || row["decided_at"] != nil {
					t.Fatalf("the conflict left the approved packet in the wrong review state: %v", row)
				}
			}
			var corrected string
			second := f.candidate(task, func(run lifecycleRun) {
				if run.input != original {
					t.Fatalf("the correction replaced the submitted input: %s", run.input)
				}
				corrected = f.reconcile(run, "answer.txt", "corrected\n")
			})
			if second.Status != "merged" || second.SubmittedCommit != corrected || second.DestinationCommit != destination {
				t.Fatalf("the corrected candidate lacks its merged inputs: %+v", second)
			}
			var repairs, infra int
			if err := f.db.QueryRow(`SELECT (SELECT count(*) FROM activity WHERE kind='task.merge_repair' AND subject=tasks.id),infrastructure_failure_count FROM tasks WHERE id=?`, task).Scan(&repairs, &infra); err != nil || repairs != 1 || infra != 0 {
				t.Fatalf("the correction charged the wrong allowance: %d, %d, %v", repairs, infra, err)
			}
			if approved {
				if _, err := PacketDecide(f.db, nil, task, "approve", "", oldPacket, oldVersion); err == nil {
					t.Fatal("the old approval authorized the corrected version")
				}
				packet, version := f.approve(task)
				if packet == oldPacket || version == oldVersion {
					t.Fatal("the correction reused the previous review identity")
				}
				if result := f.apply(task); result.Applied == nil {
					t.Fatalf("the corrected version failed application: %+v", result)
				}
				if got := srcGit(t, f.workspace, "rev-parse", "culler"); got != second.Commit {
					t.Fatal("culler differs from the corrected candidate")
				}
			} else if got := srcGit(t, f.workspace, "rev-parse", "culler"); got != destination {
				t.Fatal("the unapproved correction changed culler")
			}
			f.requireWorker(independent)
		})
	}
}

func TestDispatchBlockedMergeRepairReleasesTheProject(t *testing.T) {
	f := newDispatchLifecycle(t)
	task := f.add("Resolve contradictory requirements")
	failed := f.candidate(task, func(run lifecycleRun) {
		f.submit(run, f.commit(run.root, "answer.txt", "The answer uses the first format.\n"))
		f.commitMain("answer.txt", "The answer uses the second format.\n")
	})
	if failed.Status != "conflict" {
		t.Fatalf("the candidate missed its conflict: %+v", failed)
	}
	independent := f.independentTask()
	run := f.requireWorker(task)
	if _, err := Complete(f.db, run.id, CompleteArgs{
		Task: task, Run: run.run, NeedsOperator: true,
		Reason: "The two changes to answer.txt require incompatible formats. Select the required format.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := RunStopped(f.db, nil, run.run); err != nil {
		t.Fatal(err)
	}
	if dfInt(t, f.db, `SELECT blocked FROM tasks WHERE id=?`, task) != 1 {
		t.Fatal("the operator decision did not block the merge repair")
	}
	f.requireWorker(independent)
}

// A task with a higher priority starts before a merge repair.
func TestDispatchHigherPriorityPrecedesAMergeRepair(t *testing.T) {
	f := newDispatchLifecycle(t)
	task := f.add("Repair the conflict")
	failed := f.candidate(task, func(run lifecycleRun) {
		f.submit(run, f.commit(run.root, "answer.txt", "The answer uses the first format.\n"))
		f.commitMain("answer.txt", "The answer uses the second format.\n")
	})
	if failed.Status != "conflict" {
		t.Fatalf("the candidate missed its conflict: %+v", failed)
	}
	f.requireWorker(f.expeditedTask())
}

func TestDispatchCorrectionCancelsApprovalBeforeAnotherApplication(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepare-active=%t", prepare), func(t *testing.T) {
			f := newDispatchLifecycle(t)
			if _, err := ConfigureRepositoryLimits(f.db, nil, "ws-test", RepositoryLimits{QuietSeconds: 1}); err != nil {
				t.Fatal(err)
			}
			task := f.add("Withdraw the approved contribution")
			candidate := f.candidate(task, func(run lifecycleRun) {
				f.submit(run, f.commit(run.root, "withdrawn.txt", "The operator can withdraw this contribution.\n"))
			})
			if candidate.Status != "merged" {
				t.Fatalf("the initial candidate failed: %+v", candidate)
			}
			f.approve(task)
			independent := f.expeditedTask()
			destination := f.commitMain("accepted.txt", "Another contribution advanced culler.\n")
			if result := f.apply(task); result.Status != "destination-changed" {
				t.Fatalf("the application missed the destination change: %+v", result)
			}
			var active map[string]any
			if prepare {
				time.Sleep(time.Second)
				effect := f.nextWork()
				if effect["action"] != "repository" || effect["operation"] != "observe" || effect["task_id"] != task {
					t.Fatalf("the retry omitted destination observation: %#v", effect)
				}
				f.repository(effect)
				effect = f.nextWork()
				if effect["action"] != "repository" || effect["operation"] != "prepare" || effect["task_id"] != task {
					t.Fatalf("the retry omitted candidate preparation: %#v", effect)
				}
				active = effect
			}
			if _, err := TaskCancel(f.db, nil, task, "The operator withdrew the contribution."); err != nil {
				t.Fatal(err)
			}
			if active != nil {
				// The active step records its result, and the cancelled task receives no charge.
				f.repository(active)
				run := active["run_id"].(string)
				if got := dfStr(t, f.db, `SELECT outcome FROM runs WHERE id=?`, run); got != "obsolete" {
					t.Fatalf("the cancelled preparation ended with %q", got)
				}
				if got := dfStr(t, f.db, `SELECT end_reason FROM repository_executions WHERE run_id=?`, run); got != "result" {
					t.Fatalf("the cancelled preparation execution ended with %q", got)
				}
				if dfInt(t, f.db, `SELECT infrastructure_failure_count FROM tasks WHERE id=?`, task) != 0 {
					t.Fatal("the cancelled preparation charged the task")
				}
			}
			if got := srcGit(t, f.workspace, "rev-parse", "culler"); got != destination {
				t.Fatal("cancellation changed the accepted destination")
			}
			f.requireWorker(independent)
		})
	}
}

func TestDispatchMergeRepairsHaveNoLimit(t *testing.T) {
	f := newDispatchLifecycle(t)
	task := f.add("Repair each conflict")
	round := 0
	// A conflicting Worker commit meets a new culler change, so the merge conflicts.
	conflicting := func(run lifecycleRun) {
		round++
		f.submit(run, f.commit(run.root, "answer.txt", fmt.Sprintf("worker %d\n", round)))
		f.commitMain("answer.txt", fmt.Sprintf("main %d\n", round))
	}
	if candidate := f.candidate(task, conflicting); candidate.Status != "conflict" {
		t.Fatalf("the first candidate missed its conflict: %+v", candidate)
	}
	// Each merge repair starts before the task without a Worker run, without an operator authorization.
	independent := f.independentTask()
	for attempt := 1; attempt <= 3; attempt++ {
		if candidate := f.candidate(task, conflicting); candidate.Status != "conflict" {
			t.Fatalf("merge repair %d missed its conflict: %+v", attempt, candidate)
		}
		if dfInt(t, f.db, `SELECT blocked FROM tasks WHERE id=?`, task) != 0 {
			t.Fatalf("conflict %d blocked the task", attempt+1)
		}
	}
	candidate := f.candidate(task, func(run lifecycleRun) { f.reconcile(run, "answer.txt", "the combined answer\n") })
	if candidate.Status != "merged" {
		t.Fatalf("the merge repair did not merge: %+v", candidate)
	}
	f.approve(task)
	if result := f.apply(task); result.Applied == nil {
		t.Fatalf("the repaired version failed application: %+v", result)
	}
	if got := dfInt(t, f.db, `SELECT count(*) FROM activity WHERE kind='task.merge_repair' AND subject=?`, task); got != 4 {
		t.Fatalf("the task recorded %d merge repairs", got)
	}
	f.requireWorker(independent)
}

// The operator approves another task during a merge repair. That merge starts
// without a wait for the active agent run, and its change starts the next
// merge repair.
func TestDispatchApprovalMergesDuringAMergeRepair(t *testing.T) {
	f := newDispatchLifecycle(t)
	clocks := fixClocks(t, f.db)
	f.commitMain("answer.txt", "original\n")
	if _, err := ConfigureRepositoryLimits(f.db, nil, "ws-test", RepositoryLimits{QuietSeconds: 1}); err != nil {
		t.Fatal(err)
	}
	x := f.add("Answer with the first format")
	if candidate := f.candidate(x, func(run lifecycleRun) {
		f.submit(run, f.commit(run.root, "answer.txt", "x\n"))
	}); candidate.Status != "merged" {
		t.Fatalf("the first candidate of task X failed: %+v", candidate)
	}
	f.commitMain("answer.txt", "main\n")
	y := f.add("Answer with the second format")
	if candidate := f.candidate(y, func(run lifecycleRun) {
		f.submit(run, f.commit(run.root, "answer.txt", "y\n"))
	}); candidate.Status != "merged" {
		t.Fatalf("the candidate of task Y failed: %+v", candidate)
	}
	f.approve(x)
	if result := f.apply(x); result.Status != "destination-changed" {
		t.Fatalf("the merge missed the changed destination: %+v", result)
	}
	f.requireHold()
	clocks.advance(time.Second)
	if candidate := f.candidate(x, nil); candidate.Status != "conflict" {
		t.Fatalf("the new candidate of task X missed its conflict: %+v", candidate)
	}
	if dfStr(t, f.db, `SELECT COALESCE(decision,'') FROM tasks WHERE id=?`, x) != "" {
		t.Fatal("the conflict kept the approval")
	}
	run := f.requireWorker(x)
	f.approve(y)
	if result := f.apply(y); result.Applied == nil {
		t.Fatalf("the approved merge of task Y waited for the merge repair: %+v", result)
	}
	f.reconcile(run, "answer.txt", "the combined answer\n")
	if candidate := f.candidate(x, nil); candidate.Status != "conflict" {
		t.Fatalf("the change of task Y did not conflict with the repaired version: %+v", candidate)
	}
	if dfInt(t, f.db, `SELECT blocked FROM tasks WHERE id=?`, x) != 0 {
		t.Fatal("the second conflict blocked the task")
	}
	run = f.requireWorker(x)
	f.reconcile(run, "answer.txt", "the final answer\n")
	if candidate := f.candidate(x, nil); candidate.Status != "merged" {
		t.Fatalf("the second merge repair did not merge: %+v", candidate)
	}
	f.approve(x)
	if result := f.apply(x); result.Applied == nil {
		t.Fatalf("the repaired version failed application: %+v", result)
	}
}
