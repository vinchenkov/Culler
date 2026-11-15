package verbs

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"mc/dispatch"
	"mc/domain"
)

func TestSystemStatusIdentifiesSharedProposalBlocks(t *testing.T) {
	const question = "Which requirement applies? Keep all evidence in this text."
	for _, test := range []struct {
		name      string
		before    string
		after     string
		ownsFirst bool
		stalled   bool
		archived  bool
	}{
		{name: "shared block", ownsFirst: true},
		{name: "existing independent block", before: `UPDATE tasks SET blocked=1,blocked_kind='operator',blocked_reason='Independent question' WHERE id=1`},
		{name: "existing block with identical text", before: `UPDATE tasks SET blocked=1,blocked_kind='proposal',blocked_reason='` + question + `' WHERE id=1`},
		{name: "replaced reason", after: `UPDATE tasks SET blocked_reason='Independent question' WHERE id=1`},
		{name: "replaced kind", after: `UPDATE tasks SET blocked_kind='operator' WHERE id=1`},
		{name: "independent stall", after: `UPDATE tasks SET dispatch_retries=0 WHERE id=1`, ownsFirst: true, stalled: true},
		{name: "archived proposal", after: `UPDATE tasks SET archived=1,decision='cancelled',decided_at=datetime('now') WHERE id=1`, archived: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := dvSpine(t)
			for task := int64(1); task <= 3; task++ {
				dvInsertTask(t, db, dvTask(task, dispatch.ScopeTask, dispatch.StatusProposed, 2))
				dvExec(t, db, `UPDATE tasks SET title=? WHERE id=?`, fmt.Sprintf("Proposal %d", task), task)
			}
			if test.before != "" {
				dvExec(t, db, test.before)
			}
			decision, err := domain.RecordProjectProposalDecision(context.Background(), db, "ws-test", "", question, []int64{1, 2})
			if err != nil {
				t.Fatal(err)
			}
			// Equal text does not establish a shared decision.
			dvExec(t, db, `UPDATE tasks SET blocked=1,blocked_kind='proposal',blocked_reason=? WHERE id=3`, question)
			if test.after != "" {
				dvExec(t, db, test.after)
			}
			conditions := ssConditions(t, db)
			wantCount := 4
			if test.archived {
				wantCount--
			}
			if len(conditions) != wantCount {
				t.Fatalf("The status has %d conditions, want %d", len(conditions), wantCount)
			}
			parent := conditions[0]
			if parent.ProposalDecisionID != decision || parent.Condition != question {
				t.Fatalf("The decision lost its identity or complete text: %+v", parent)
			}
			want := []SystemAffectedTask{{TaskID: 1, Title: "Proposal 1", OwnsBlock: test.ownsFirst}, {TaskID: 2, Title: "Proposal 2", OwnsBlock: true}}
			if test.archived {
				want = want[1:]
			}
			if !reflect.DeepEqual(parent.AffectedTasks, want) {
				t.Fatalf("The affected tasks are %+v, want %+v", parent.AffectedTasks, want)
			}
			for _, condition := range conditions[1:] {
				var wantDecision int64
				if condition.TaskID == 2 || condition.TaskID == 1 && test.ownsFirst {
					wantDecision = decision
				}
				if condition.BlockDecisionID != wantDecision {
					t.Fatalf("The task block belongs to decision %d, want %d: %+v", condition.BlockDecisionID, wantDecision, condition)
				}
				if condition.Stalled != (condition.TaskID == 1 && test.stalled) {
					t.Fatalf("The task lost its independent stall: %+v", condition)
				}
			}
			if err := domain.ResolveProposalDecision(context.Background(), db, "ws-test", decision, "Use the Project requirements."); err != nil {
				t.Fatal(err)
			}
			for _, condition := range ssConditions(t, db) {
				if condition.ProposalDecisionID != 0 || condition.BlockDecisionID != 0 || len(condition.AffectedTasks) != 0 {
					t.Fatalf("A resolved decision retained its issue: %+v", condition)
				}
			}
		})
	}
}

// ssConditions returns the conditions of a system status with no connection requirement.
func ssConditions(t *testing.T, db *sql.DB) []SystemCondition {
	t.Helper()
	raw, err := SystemStatus(db, nil, SystemStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return raw.(SystemStatusResult).Conditions
}

// ssWikiMerge is the condition of the Project ws-test with the title Acme
// for a merge of a wiki commit that has no proven result.
func ssWikiMerge(action string) []SystemCondition {
	return []SystemCondition{{ID: "wiki-merge:ws-test", Group: "execution", State: "stopped", ProjectID: "ws-test",
		Name:      "Acme · Project wiki",
		Condition: "Culler checks the result of an interrupted merge of the wiki commit. Each other merge step of the Project waits for this result.",
		Action:    action}}
}

// ssWant requires exactly the conditions.
func ssWant(t *testing.T, db *sql.DB, state string, want []SystemCondition) {
	t.Helper()
	got := ssConditions(t, db)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: system conditions = %+v, want %+v", state, got, want)
	}
}

// System health states one condition for a Project whose merge of a wiki
// commit has no proven result after one receipt recovery. The name holds the
// Project title. The condition is the receipt step text of the wiki state,
// and the action is the repair text of the merge. When no result names a
// repair, the action also states the delay before the next receipt recovery
// and the instruction for the operator. The condition ends with the result of
// the merge.
func TestSystemStatusStatesAWikiMergeWithNoProof(t *testing.T) {
	const refusal = "A merge into culler stopped before it updated the branch. If the file .git/index.lock exists, remove it. " +
		"Then discard each change in the checkout that you did not make, and remove the file .git/mc/applications/0123456789abcdef.json."
	for _, test := range []struct {
		name   string
		check  func(t *testing.T, db *sql.DB, u *wmUpdate, run string) // the end of one receipt recovery with no proof
		action string
		result wmResult // the result that the last receipt recovery proves
	}{
		{"a refusal text and a merged wiki commit", func(_ *testing.T, _ *sql.DB, u *wmUpdate, run string) {
			u.record("reconcile", run, RepositoryResult{Status: "operator-attention", OperatorAction: refusal})
		}, refusal, wmApplied},
		{"a check with no result and no merge", func(t *testing.T, db *sql.DB, _ *wmUpdate, run string) { stFail(t, db, run, false) },
			"The last check ended with no result. The repository operation failed. Culler repeats the check after 60 seconds. " +
				"If this condition stays, ask your coding harness to find the cause.", wmNotApplied(wrCommitA)},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := dvSpine(t)
			fixClocks(t, db)
			// A Project with no wiki update gets no condition.
			dvAddProject(t, db, "ws-other", "active")
			dvExec(t, db, `UPDATE projects SET title='Acme' WHERE id='ws-test'`)
			u := wmFixtureUpdate(t, db)
			candidate := u.candidate()
			ssWant(t, db, "a merge candidate that waits for its merge", nil)
			merge := u.startRun(dispatch.OperationApply, "")
			ssWant(t, db, "an active merge", nil)
			stFail(t, db, merge, false)
			ssWant(t, db, "a merge with no result", nil)
			check := u.startRun(dispatch.OperationReconcile, merge)
			ssWant(t, db, "the first receipt recovery", nil)
			test.check(t, db, u, check)
			ssWant(t, db, "a receipt recovery with no proof", ssWikiMerge(test.action))
			check = u.startRun(dispatch.OperationReconcile, merge)
			ssWant(t, db, "the next receipt recovery", ssWikiMerge(test.action))
			u.record("reconcile", check, test.result(u, merge, candidate))
			if got := u.end(); got == wmActive {
				t.Fatal("the result of the merge left the wiki update active")
			}
			ssWant(t, db, "the result of the merge", nil)
		})
	}
}

// The condition applies to an active Project and to a paused Project. An
// archived Project gets no condition, also while its merge authorization
// stays unfinished.
func TestSystemStatusOmitsTheWikiMergeOfAnArchivedProject(t *testing.T) {
	const refusal = "Culler cannot write the file .git/index.lock in the Project repository. Make sure that your user can write the .git folder."
	db := dvSpine(t)
	fixClocks(t, db)
	dvExec(t, db, `UPDATE projects SET title='Acme' WHERE id='ws-test'`)
	u := wmFixtureUpdate(t, db)
	u.candidate()
	merge := u.startRun(dispatch.OperationApply, "")
	stFail(t, db, merge, false)
	u.record("reconcile", u.startRun(dispatch.OperationReconcile, merge), RepositoryResult{Status: "operator-attention", OperatorAction: refusal})
	ssWant(t, db, "an active Project", ssWikiMerge(refusal))
	for _, status := range []string{"paused", "archived"} {
		if _, err := ProjectSetStatus(db, nil, "ws-test", status); err != nil {
			t.Fatal(err)
		}
		var want []SystemCondition
		if status == "paused" {
			want = ssWikiMerge(refusal)
		}
		ssWant(t, db, "a Project with the status "+status, want)
	}
	if finished := dfInt(t, db, `SELECT finished FROM merge_operations WHERE run_id=?`, merge); finished != 0 {
		t.Fatal("the archive finished the merge authorization")
	}
}
