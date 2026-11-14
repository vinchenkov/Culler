package verbs

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"mc/domain"
)

func TestInterruptedMergeRecoversOnce(t *testing.T) {
	for _, boundary := range []string{"before the merge", "after the merge"} {
		t.Run(boundary, func(t *testing.T) {
			f := newDeliveryFixture(t)
			task := f.add("Recover the interrupted merge")
			f.pass(task, "interrupted.txt", "merged once\n")
			candidate := f.merge(task, "prepare")
			f.approve(task)
			clocks := fixClocks(t, f.db)
			resp := startRequest(t, f.db, false)
			stWantStarted(t, f.db, resp, fmt.Sprintf("apply/%d", task))
			apply := resp.Started[0]
			var late RepositoryResult
			if boundary == "after the merge" {
				late = f.native(f.envelope(executionEffect(t, f.db, apply)))
				if late.Applied == nil {
					t.Fatalf("the merge returned no receipt: %+v", late)
				}
			}

			// A resident restart loses the host work of the merge.
			owner := stRegister(t, f.db)
			clocks.advance(domain.HeartbeatLimit)
			stWantStarted(t, f.db, stStart(t, f.db, owner))
			if reason := dfStr(t, f.db, `SELECT end_reason FROM repository_executions WHERE run_id=?`, apply); reason != domain.EndOwnerChange {
				t.Fatalf("the new owner ended the merge with %s", reason)
			}
			if late.Applied != nil {
				var refusal *DomainError
				if _, err := RepositoryRecord(f.db, nil, "apply", apply, late); !errors.As(err, &refusal) || refusal.Code != domain.CodeStaleRun {
					t.Fatalf("the late merge record received %v", err)
				}
			}
			resp = stStart(t, f.db, owner, apply)
			stWantStarted(t, f.db, resp)
			if !reflect.DeepEqual(resp.Confirmed, []string{apply}) {
				t.Fatalf("the stop of the interrupted merge was not confirmed: %v", resp.Confirmed)
			}
			clocks.advance(domain.RetryDelay)
			resp = stStart(t, f.db, owner)
			stWantStarted(t, f.db, resp, fmt.Sprintf("reconcile/%d", task))
			reconcile := executionEffect(t, f.db, resp.Started[0])
			if reconcile["previous_run"] != apply {
				t.Fatalf("the receipt recovery names %v", reconcile["previous_run"])
			}
			recovered := f.runExecution(reconcile)
			if boundary == "before the merge" {
				if recovered.Status != "not-applied" {
					t.Fatalf("the receipt recovery found %+v", recovered)
				}
				resp = stStart(t, f.db, owner)
				stWantStarted(t, f.db, resp, fmt.Sprintf("apply/%d", task))
				f.runExecution(executionEffect(t, f.db, resp.Started[0]))
			} else if recovered.Applied == nil || !recovered.Applied.Recovered || recovered.Applied.Commit != candidate.Commit {
				t.Fatalf("the receipt recovery lost the receipt: %+v", recovered)
			}

			state := func() string {
				return dfStr(t, f.db, `SELECT json_object('archived',(SELECT archived FROM tasks WHERE id=?),
					'operations',(SELECT json_group_array(json_object('run',run_id,'finished',finished,'result',result)) FROM merge_operations WHERE task_id=?),
					'executions',(SELECT json_group_array(json_object('run',run_id,'end',end_reason)) FROM repository_executions WHERE task_id=?))`, task, task, task)
			}
			before := state()
			if _, err := RepositoryRecord(f.db, nil, "reconcile", reconcile["run_id"].(string), recovered); err != nil {
				t.Fatal(err)
			}
			if after := state(); after != before {
				t.Fatalf("the repeated receipt recovery record changed the records:\n%s\n%s", before, after)
			}
			if archived := dfInt(t, f.db, `SELECT archived FROM tasks WHERE id=?`, task); archived != 1 {
				t.Fatal("the recovered merge left the task open")
			}
			if n := dfInt(t, f.db, `SELECT COUNT(*) FROM merge_operations WHERE task_id=? AND json_extract(result,'$.applied.commit')=?`, task, candidate.Commit); n != 1 {
				t.Fatalf("the spine records the merge %d times", n)
			}
			if main := srcGit(t, f.workspace, "rev-parse", "culler"); main != candidate.Commit {
				t.Fatalf("culler is %s, want the candidate %s", main, candidate.Commit)
			}
			stWantStarted(t, f.db, stStart(t, f.db, owner))
		})
	}
}

func TestLateCaptureRecordAfterSweepIsRefused(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Refuse the late capture record")
	producer, root, id := f.run(task, "worker")
	commit := f.commit(root, "late.txt", "captured once\n")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: producer, Status: "worked", Commit: commit}); err != nil {
		t.Fatal(err)
	}
	clocks := fixClocks(t, f.db)
	resp := startRequest(t, f.db, false)
	stWantStarted(t, f.db, resp, fmt.Sprintf("capture/%d", task))
	capture := resp.Started[0]
	late := f.native(f.envelope(executionEffect(t, f.db, capture)))

	// The resident sends no heartbeat, so a sweep ends the capture before its record.
	clocks.advance(domain.HeartbeatLimit)
	stWantStarted(t, f.db, startRequest(t, f.db, false))
	if reason := dfStr(t, f.db, `SELECT end_reason FROM repository_executions WHERE run_id=?`, capture); reason != domain.EndHeartbeat {
		t.Fatalf("the sweep ended the capture with %s", reason)
	}
	wantStale := func(run string) {
		t.Helper()
		var refusal *DomainError
		result, err := RepositoryRecord(f.db, nil, "capture", run, late)
		if !errors.As(err, &refusal) || refusal.Code != domain.CodeStaleRun || result.(map[string]any)["recorded"] != false {
			t.Fatalf("the late capture record received %v, %v", result, err)
		}
	}
	wantStale(capture)
	if n := dfInt(t, f.db, `SELECT COUNT(*) FROM task_versions WHERE run_id=?`, producer); n != 0 {
		t.Fatalf("the late capture record retained %d versions", n)
	}

	// A later capture records the version. Only an exact repeat of its record succeeds.
	clocks.advance(domain.RetryDelay)
	resp = startRequest(t, f.db, false)
	stWantStarted(t, f.db, resp, fmt.Sprintf("capture/%d", task))
	retry := resp.Started[0]
	result := f.runExecution(executionEffect(t, f.db, retry))
	f.record("capture", retry, result)
	changed := result
	changed.RetainedRef += "-changed"
	var refusal *DomainError
	if _, err := RepositoryRecord(f.db, nil, "capture", retry, changed); !errors.As(err, &refusal) || refusal.Code == domain.CodeStaleRun {
		t.Fatalf("the changed repeat of the capture record received %v", err)
	}
	wantStale(capture)
	if n := dfInt(t, f.db, `SELECT COUNT(*) FROM task_versions WHERE run_id=?`, producer); n != 1 {
		t.Fatalf("the spine retains %d versions of the submission", n)
	}
}
