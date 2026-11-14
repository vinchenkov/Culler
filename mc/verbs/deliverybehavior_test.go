package verbs

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc/dispatch"
	"mc/domain"
	"mc/repository"
)

func TestDeliveryConfiguredRepositoryLimitRefusesTheCopy(t *testing.T) {
	f := newDeliveryFixture(t)
	limit := RepositoryLimits{QuietSeconds: 45, TemporaryBytes: 1024}
	if _, err := ConfigureRepositoryLimits(f.db, nil, "ws-test", limit); err != nil {
		t.Fatal(err)
	}
	if _, err := ConfigureRepositoryLimits(f.db, nil, "ws-test", RepositoryLimits{}); err != nil {
		t.Fatal(err)
	}
	var retained RepositoryLimits
	if err := f.db.QueryRow(`SELECT destination_quiet_seconds,max_temporary_bytes FROM repository_limits WHERE project='ws-test'`).Scan(&retained.QuietSeconds, &retained.TemporaryBytes); err != nil || retained != limit {
		t.Fatalf("the omitted limits changed the configuration: %+v, %v", retained, err)
	}
	task := f.add("Respect the repository limit")
	value, err := dvDispatchOne(f.db)
	if err != nil {
		t.Fatal(err)
	}
	effect := value.(map[string]any)
	plan := effect["mount_plan"].(*PrivateDispatchMountPlan).Repository
	if plan.MaxTemporaryBytes != limit.TemporaryBytes {
		t.Fatal("the dispatch plan omitted the repository limit")
	}
	run := effect["run_id"].(string)
	root := filepath.Join(f.root, run)
	_, err = executeRepository(context.Background(), RepositoryEnvelope{Operation: "allocate", TaskID: task, RunID: run, WorkspaceRoot: f.workspace, RunRoot: root, DestinationBranch: "culler", MaxTemporaryBytes: plan.MaxTemporaryBytes})
	if err == nil {
		t.Fatal("the oversized repository copy passed its limit")
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("the refused allocation created a run folder: %v", err)
	}
}

func TestDeliveryMergedCorrectionConcludesWhenMainChangesDuringPreparation(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Count the completed correction")
	f.pass(task, "answer.txt", "worker\n")
	f.commitMain("answer.txt", "main\n")
	if candidate := f.merge(task, "prepare"); candidate.Status != "conflict" {
		t.Fatalf("the controlled Git conflict did not occur: %+v", candidate)
	}
	f.pass(task, "answer.txt", "the combined answer\n")
	f.afterPrepare = func(repository.Candidate) { f.commitMain("independent.txt", "accepted\n") }
	f.merge(task, "prepare")
	var infra, open int
	if err := f.db.QueryRow(`SELECT t.infrastructure_failure_count,t.correction_open FROM tasks t WHERE t.id=?`, task).Scan(&infra, &open); err != nil || infra != 0 || open != 0 {
		t.Fatalf("the completed correction has counts %d, %d, %v", infra, open, err)
	}
	f.afterPrepare = nil
	f.merge(task, "prepare")
	f.approve(task)
	f.merge(task, "apply")
}

func TestDeliveryGeneratedMainChangesPreserveApprovedVersion(t *testing.T) {
	for _, seed := range []int64{7, 19, 41} {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			f := newDeliveryFixture(t)
			task := f.add("Preserve the approved version")
			submitted := f.pass(task, "feature.txt", "complete\n")
			f.merge(task, "prepare")
			packet, _ := f.approve(task)
			independent := f.add("Continue independent work")
			random := rand.New(rand.NewSource(seed))
			moves := 1 + random.Intn(3)
			for step := 0; step < moves; step++ {
				path := fmt.Sprintf("independent-%d.txt", step)
				writeFile(t, filepath.Join(f.workspace, path), fmt.Sprintf("seed %d, step %d\n", seed, step))
				srcGit(t, f.workspace, "add", path)
				srcGit(t, f.workspace, "commit", "-qm", "Advance culler independently")
				t.Logf("seed %d, step %d: culler advanced before application", seed, step)
				f.merge(task, "apply")
				var rec dispatch.Records
				f.transaction(func(ctx context.Context, q Q) error {
					var err error
					rec, err = loadVisionRecords(ctx, q, "ws-test")
					return err
				})
				action := dispatch.Decide(rec, dispatch.Lease{}, dispatch.DefaultConfig(), dispatch.Clock{Now: time.Now()})
				if action.Kind != dispatch.KindIdle || action.Idle != dispatch.IdleReason("priority-task-wait") {
					t.Fatalf("The approved task lost priority during the quiet interval: %+v", action)
				}
				if dfStr(t, f.db, `SELECT status FROM tasks WHERE id=?`, independent) != "seeded" {
					t.Fatal("The independent task started before the approved merge.")
				}
				var deadline *time.Time
				for _, row := range rec.Tasks {
					if row.ID == task {
						deadline = row.Delivery.WaitUntil
					}
				}
				if deadline == nil {
					t.Fatal("the culler change omitted its retry delay")
				}
				wantStart(t, rec, deadline.Add(time.Second), dispatch.OperationObserve, task)
				effect := startOperation(t, f.db, dispatch.RepositoryOperation{Project: "ws-test", TaskID: task, Operation: dispatch.OperationObserve})
				run := effect["run_id"].(string)
				result := f.native(RepositoryEnvelope{Operation: "observe", TaskID: task, RunID: run, WorkspaceRoot: f.workspace, DestinationBranch: "culler"})
				repeats := 1 + random.Intn(3)
				for repeat := 0; repeat < repeats; repeat++ {
					if _, err := RepositoryRecord(f.db, nil, "observe", run, result); err != nil {
						t.Fatal(err)
					}
				}
				candidate := f.merge(task, "prepare")
				if candidate.SubmittedCommit != submitted {
					t.Fatal("a mechanical preparation changed the approved version")
				}
			}
			candidate := f.merge(task, "apply")
			var run, body string
			if err := f.db.QueryRow(`SELECT run_id,result FROM merge_operations WHERE task_id=? AND operation='apply' ORDER BY rowid DESC LIMIT 1`, task).Scan(&run, &body); err != nil {
				t.Fatal(err)
			}
			var result RepositoryResult
			if err := json.Unmarshal([]byte(body), &result); err != nil {
				t.Fatal(err)
			}
			if _, err := RepositoryRecord(f.db, nil, "apply", run, result); err != nil {
				t.Fatal(err)
			}
			var approvals, infra, corrections int
			if err := f.db.QueryRow(`SELECT (SELECT count(*) FROM review_decisions WHERE packet_id=?),infrastructure_failure_count,correction_count FROM tasks WHERE id=?`, packet, task).Scan(&approvals, &infra, &corrections); err != nil || approvals != 1 || infra != 0 || corrections != 0 {
				t.Fatalf("approval and allowances = %d, %d, %d, %v", approvals, infra, corrections, err)
			}
			if !(repository.Git{Root: f.workspace}).Ancestor(context.Background(), submitted, candidate.Commit) {
				t.Fatal("the final update lost the approved version")
			}
		})
	}
}

func TestDeliveryReviewCapacityPreservesMergedCandidate(t *testing.T) {
	f := newDeliveryFixture(t)
	dvExec(t, f.db, `UPDATE settings SET review_wip_cap=1`)
	first := f.add("First complete change")
	f.pass(first, "first.txt", "first\n")
	f.merge(first, "prepare")
	second := f.add("Second complete change")
	f.pass(second, "second.txt", "second\n")
	candidate := f.merge(second, "prepare")
	var packets, failures int
	if err := f.db.QueryRow(`SELECT count(*) FROM tasks t JOIN review_versions p ON p.id=t.current_review_id WHERE task_id=?`, second).Scan(&packets); err != nil {
		t.Fatal(err)
	}
	if packets != 0 {
		t.Fatal("the second report exceeded review capacity")
	}
	if err := f.db.QueryRow(`SELECT infrastructure_failure_count FROM tasks WHERE id=?`, second).Scan(&failures); err != nil || failures != 0 {
		t.Fatalf("the capacity wait charged %d failures, %v", failures, err)
	}
	f.approve(first)
	f.merge(first, "apply")
	f.transaction(func(ctx context.Context, q Q) error { return domain.PublishReview(ctx, q, second) })
	var retained string
	if err := f.db.QueryRow(`SELECT c.candidate_commit FROM review_versions p JOIN tasks d ON d.current_review_id=p.id JOIN merge_candidates c ON c.id=d.candidate_id WHERE p.task_id=?`, second).Scan(&retained); err != nil || retained != candidate.Commit {
		t.Fatalf("the wait changed the candidate: %s, %v", retained, err)
	}
}

func TestDeliveryAllocationSurvivesAnInterruptedRepositoryCopy(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Preserve the assigned input")
	run := "interrupted-copy"
	f.transaction(func(ctx context.Context, q Q) error {
		_, err := domain.Claim(ctx, q, time.Now(), domain.ClaimArgs{RunID: run, Project: "ws-test", Owner: "worker", SubjectID: &task, SessionPath: "sessions/" + run, Binding: "fake", HardDeadlineMinutes: 60})
		return err
	})
	root := filepath.Join(f.root, run)
	allocated, err := executeRepository(context.Background(), RepositoryEnvelope{Operation: "allocate", TaskID: task, RunID: run, WorkspaceRoot: f.workspace, RunRoot: root, DestinationBranch: "culler"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RepositoryRecord(f.db, nil, "ready", run, allocated); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "repo"), 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "repo", "partial"), "an incomplete object transfer")
	if _, err := RunInfrastructureFailure(f.db, nil, run, domain.InfrastructureFailureReport{Condition: "The copy process stopped.", Source: "resident"}); err != nil {
		t.Fatal(err)
	}
	var rec dispatch.Records
	rec.Project = "ws-test"
	rec.Tasks = []dispatch.Task{{ID: task}}
	f.transaction(func(ctx context.Context, q Q) error { return loadDeliveryRecords(ctx, q, &rec) })
	if len(rec.Cleanups) != 1 || rec.Cleanups[0] != (dispatch.Cleanup{TaskID: task, RunID: run}) {
		t.Fatalf("the incomplete copy lost its cleanup record: %+v", rec.Cleanups)
	}
	f.cleanup(task, run, root)
	srcGit(t, f.workspace, "gc", "--prune=now")
	if got := srcGit(t, f.workspace, "rev-parse", "refs/mc/tasks/1/inputs/"+run); got != allocated.InputCommit {
		t.Fatal("the allocated input lost its retained reference")
	}
}

func TestKnowledgeDeliveryUsesRepositoryAcceptance(t *testing.T) {
	for name, body := range map[string]string{
		"docs/research/competitors.md": "# Competitor report\n\nThe report preserves its original sources and conclusions.\n",
		"kb/research/competitors.html": "<html><body>The report preserves its original sources and conclusions.</body></html>\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := newDeliveryFixture(t)
			task := f.add("Create a complete competitor report")
			f.pass(task, name, body)
			before := srcGit(t, f.workspace, "rev-parse", "culler")
			f.merge(task, "prepare")
			if srcGit(t, f.workspace, "rev-parse", "culler") != before {
				t.Fatal("the unaccepted report changed culler")
			}
			f.approve(task)
			f.merge(task, "apply")
			if got := srcGit(t, f.workspace, "show", "culler:"+name); got != strings.TrimSpace(body) {
				t.Fatal("the accepted commit lost the complete report")
			}
			if dfInt(t, f.db, `SELECT source_version IS NOT NULL AND source_version IS submitted_version FROM tasks WHERE id=?`, task) != 1 {
				t.Fatal("the accepted report is not the version that the Verifier passed")
			}
		})
	}
}
