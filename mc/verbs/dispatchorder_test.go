package verbs

import (
	"context"
	"testing"

	"mc/dispatch"
	"mc/domain"
)

// The dispatch records state the two task facts of the agent run order: the
// task started work, and its open merge repair followed an approval.
func TestDispatchRecordsStateTheOrderFacts(t *testing.T) {
	f := newDeliveryFixture(t)
	want := func(step string, task int64, started, repair bool) {
		t.Helper()
		rec := dispatch.Records{Project: "ws-test", Tasks: []dispatch.Task{{ID: task}}}
		f.transaction(func(ctx context.Context, q Q) error { return loadDeliveryRecords(ctx, q, &rec) })
		if d := rec.Tasks[0].Delivery; d.WorkStarted != started || d.RepairAfterApproval != repair {
			t.Fatalf("%s: started %v, repair after approval %v; want %v, %v", step, d.WorkStarted, d.RepairAfterApproval, started, repair)
		}
	}
	conflict := func(step string, task int64) {
		t.Helper()
		if candidate := f.merge(task, "prepare"); candidate.Status != "conflict" {
			t.Fatalf("%s: the controlled Git conflict did not occur: %+v", step, candidate)
		}
	}

	task := f.add("Preserve the required result")
	want("a task without a Worker run", task, false, false)
	f.pass(task, "answer.txt", "worker 0\n")
	f.merge(task, "prepare")
	want("a packaged task", task, true, false)

	f.approve(task)
	f.commitMain("answer.txt", "main 1\n")
	f.merge(task, "apply")
	conflict("after the approval", task)
	want("a merge repair after an approval", task, true, true)

	f.pass(task, "answer.txt", "worker 1\n")
	f.commitMain("answer.txt", "main 2\n")
	conflict("in the merge repair", task)
	want("a second conflict of the same merge repair", task, true, true)

	f.pass(task, "answer.txt", "worker 2\n")
	if candidate := f.merge(task, "prepare"); candidate.Status != "merged" {
		t.Fatalf("the repair did not merge: %+v", candidate)
	}
	want("a repaired task", task, true, false)

	f.transaction(func(ctx context.Context, q Q) error {
		return domain.RequestPacketRevision(ctx, q, task, "Change the result.")
	})
	want("an operator packet revision", task, true, false)

	other := f.add("Preserve the other result")
	f.pass(other, "other.txt", "worker 0\n")
	f.commitMain("other.txt", "main 1\n")
	conflict("before an approval", other)
	want("a merge repair before an approval", other, true, false)
}
