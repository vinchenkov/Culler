package main_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestArbiterInvalidCriteriaRejectsPool(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprintf("mixed_%t", mixed), func(t *testing.T) {
			spine := initSpine(t)
			db := openDB(t, spine)
			var duplicate int64
			if mixed {
				duplicate = proposalTask(t, spine, "prior work")
				if _, err := db.Exec(`UPDATE tasks SET status='seeded', blocked=1, blocked_kind='report', blocked_reason='Operator decision' WHERE id=?`, duplicate); err != nil {
					t.Fatal(err)
				}
			}
			t1 := proposalTask(t, spine, "suite criterion")
			t2 := proposalTask(t, spine, "host criterion")
			run := dispatchExpect(t, spine, "spawn")["run_id"].(string)
			env := runJSONEnv(t, spine, run, "pipeline", "arbiter")
			second := fmt.Sprintf(`{"task":%d,"decision":"reject","reason":"The criterion requires macOS outside the agent container.","rejection_category":"invalid_criteria"}`, t2)
			secondReason := "The criterion requires macOS outside the agent container."
			if mixed {
				second = fmt.Sprintf(`{"task":%d,"decision":"reject","reason":"The proposal duplicates prior work.","duplicate_of":%d}`, t2, duplicate)
				secondReason = "The proposal duplicates prior work."
			}
			batch := fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"reject","reason":"The criterion requires whole-suite success.","rejection_category":"invalid_criteria"},%s]}`, t1, second)
			res := runMC(t, env, batch, "arbiter", "decide", "--run", run, "--batch", "-")
			if res.code != 0 {
				t.Fatalf("the rejection failed: %s", res.stderr)
			}
			for task, reason := range map[int64]string{t1: "The criterion requires whole-suite success.", t2: secondReason} {
				if got := queryStr(t, db, `SELECT decision || '/' || archived || '/' || blocked FROM tasks WHERE id=?`, task); got != "rejected/1/0" {
					t.Fatalf("task %d = %q, want rejected/1/0", task, got)
				}
				if got := queryStr(t, db, `SELECT detail FROM activity WHERE actor='arbiter' AND kind='task.rejected' AND subject=?`, task); got != reason {
					t.Fatalf("task %d reason = %q, want %q", task, got, reason)
				}
			}
			if got := queryInt(t, db, `SELECT COUNT(*) FROM proposal_decisions`); got != 0 {
				t.Fatalf("the rejection created %d operator decisions", got)
			}
			if got := queryStr(t, db, `SELECT outcome FROM runs WHERE id=?`, run); got != "completed" {
				t.Fatalf("the run outcome = %q", got)
			}
			if got := queryStr(t, db, `SELECT COALESCE(run_id,'') FROM leases WHERE project='ws-test'`); got != "" {
				t.Fatalf("the rejection retained the lease: %s", got)
			}
			if eff := dispatchExpect(t, spine, "spawn"); eff["role"] != "strategist(propose)" {
				t.Fatalf("the next role = %v, want strategist(propose)", eff["role"])
			}
		})
	}
}

func TestArbiterInvalidCriteriaRefusesMalformedBatch(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	t1 := proposalTask(t, spine, "first proposal")
	t2 := proposalTask(t, spine, "second proposal")
	run := dispatchExpect(t, spine, "spawn")["run_id"].(string)
	env := runJSONEnv(t, spine, run, "pipeline", "arbiter")
	first := fmt.Sprintf(`{"task":%d,"decision":"reject","reason":"The criterion requires whole-suite success.","rejection_category":"invalid_criteria"}`, t1)
	for _, tc := range []struct {
		name   string
		second string
		want   string
	}{
		{"promotion", `"decision":"promote","rejection_category":"invalid_criteria"`, "requires a reject decision"},
		{"unknown_category", `"decision":"reject","reason":"The task has a low rank.","rejection_category":"low_rank"`, "unknown rejection_category"},
		{"empty_category", `"decision":"reject","reason":"The task has a low rank.","rejection_category":""`, "unknown rejection_category"},
		{"category_type", `"decision":"reject","reason":"The task has a low rank.","rejection_category":true`, "bad batch payload"},
		{"absent_reason", `"decision":"reject","rejection_category":"invalid_criteria"`, "requires a reason"},
		{"empty_reason", `"decision":"reject","reason":"","rejection_category":"invalid_criteria"`, "requires a reason"},
		{"blank_reason", `"decision":"reject","reason":" \n\t ","rejection_category":"invalid_criteria"`, "requires a reason"},
		{"two_exceptions", fmt.Sprintf(`"decision":"reject","reason":"The criterion requires macOS.","rejection_category":"invalid_criteria","duplicate_of":%d`, t1), "mutually exclusive"},
		{"no_exception", `"decision":"reject","reason":"The task has a low rank."`, "supplies neither exception"},
		{"duplicate_inside_pool", fmt.Sprintf(`"decision":"reject","reason":"The proposal duplicates a pool member.","duplicate_of":%d`, t1), "member of its own pool"},
		{"absent_duplicate", `"decision":"reject","reason":"The proposal duplicates prior work.","duplicate_of":99999`, "not existing work"},
		{"invalid_duplicate", `"decision":"reject","reason":"The proposal duplicates prior work.","duplicate_of":0`, "must name a task id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			batch := fmt.Sprintf(`{"verdicts":[%s,{"task":%d,%s}]}`, first, t2, tc.second)
			res := runMC(t, env, batch, "arbiter", "decide", "--run", run, "--batch", "-")
			if res.code != 1 || !strings.Contains(res.stderr, tc.want) {
				t.Fatalf("exit = %d, stderr = %q, want refusal with %q", res.code, res.stderr, tc.want)
			}
		})
	}
	for _, batch := range []string{
		fmt.Sprintf(`{"verdicts":[%s]}`, first),
		fmt.Sprintf(`{"verdicts":[%s,%s]}`, first, first),
		fmt.Sprintf(`{"verdicts":[%s,{"task":99999,"decision":"reject","reason":"The criterion requires macOS.","rejection_category":"invalid_criteria"}]}`, first),
	} {
		res := runMC(t, env, batch, "arbiter", "decide", "--run", run, "--batch", "-")
		if res.code != 1 || !strings.Contains(res.stderr, "exactly") {
			t.Fatalf("the invalid pool passed: exit = %d, stderr = %q", res.code, res.stderr)
		}
	}
	valid := fmt.Sprintf(`{"verdicts":[%s,{"task":%d,"decision":"reject","reason":"The criterion requires macOS.","rejection_category":"invalid_criteria"}]}`, first, t2)
	wrongRole := runJSONEnv(t, spine, run, "pipeline", "strategist(propose)")
	if res := runMC(t, wrongRole, valid, "arbiter", "decide", "--run", run, "--batch", "-"); res.code != 1 {
		t.Fatalf("the command accepted another role: exit = %d, stderr = %q", res.code, res.stderr)
	}
	if res := runMC(t, env, valid, "arbiter", "decide", "--run", "another-run", "--batch", "-"); res.code != 1 {
		t.Fatalf("the command accepted another run: exit = %d, stderr = %q", res.code, res.stderr)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM tasks WHERE status='proposed' AND archived=0 AND blocked=0 AND decision IS NULL`); got != 2 {
		t.Fatalf("the refused batches changed the proposals: %d remain", got)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM activity WHERE kind='task.rejected'`); got != 0 {
		t.Fatalf("the refused batches wrote %d rejection records", got)
	}
	if got := queryStr(t, db, `SELECT COALESCE(run_id,'') FROM leases WHERE project='ws-test'`); got != run {
		t.Fatalf("the refused batches changed the lease: %q", got)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM runs WHERE id=? AND ended_at IS NULL`, run); got != 1 {
		t.Fatal("a refused batch ended the run")
	}
}

func TestArbiterInvalidCriteriaRollsBackBatch(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	t1 := proposalTask(t, spine, "first proposal")
	t2 := proposalTask(t, spine, "second proposal")
	run := dispatchExpect(t, spine, "spawn")["run_id"].(string)
	env := runJSONEnv(t, spine, run, "pipeline", "arbiter")
	// The second proposal becomes blocked after the pool snapshot.
	if _, err := db.Exec(`UPDATE tasks SET blocked=1, blocked_kind='report', blocked_reason='Operator decision' WHERE id=?`, t2); err != nil {
		t.Fatal(err)
	}
	batch := fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"reject","reason":"The criterion requires macOS.","rejection_category":"invalid_criteria"},{"task":%d,"decision":"reject","reason":"The criterion requires whole-suite success.","rejection_category":"invalid_criteria"}]}`, t1, t2)
	res := runMC(t, env, batch, "arbiter", "decide", "--run", run, "--batch", "-")
	if res.code != 1 || !strings.Contains(res.stderr, "blocked") {
		t.Fatalf("the command accepted the blocked proposal: exit = %d, stderr = %q", res.code, res.stderr)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM tasks WHERE archived=0 AND decision IS NULL`); got != 2 {
		t.Fatalf("the refused batch changed the proposals: %d remain", got)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM activity WHERE kind='task.rejected'`); got != 0 {
		t.Fatalf("the refused batch wrote %d rejection records", got)
	}
	if got := queryStr(t, db, `SELECT COALESCE(run_id,'') FROM leases WHERE project='ws-test'`); got != run {
		t.Fatalf("the refused batch changed the lease: %q", got)
	}
}

func TestArbiterAllRejectWithReadyWorkNeedsNoException(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	task := proposalTask(t, spine, "assigned proposal")
	run := dispatchExpect(t, spine, "spawn")["run_id"].(string)
	env := runJSONEnv(t, spine, run, "pipeline", "arbiter")
	// A later proposal remains outside the assigned pool.
	ready := proposalTask(t, spine, "later proposal")
	batch := fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"reject","reason":"The later proposal has higher value."}]}`, task)
	res := runMC(t, env, batch, "arbiter", "decide", "--run", run, "--batch", "-")
	if res.code != 0 {
		t.Fatalf("the rejection with ready work failed: %s", res.stderr)
	}
	if got := queryStr(t, db, `SELECT decision || '/' || archived FROM tasks WHERE id=?`, task); got != "rejected/1" {
		t.Fatalf("the assigned task = %q", got)
	}
	if got := queryStr(t, db, `SELECT status || '/' || archived FROM tasks WHERE id=?`, ready); got != "proposed/0" {
		t.Fatalf("the later task = %q", got)
	}
}
