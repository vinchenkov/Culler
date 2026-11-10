package dispatch

import (
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// starts renders each selected operation as "operation:task".
func starts(ops []RepositoryOperation) []string {
	out := []string{}
	for _, op := range ops {
		out = append(out, string(op.Operation)+":"+strconv.FormatInt(op.TaskID, 10))
	}
	return out
}

func selectOne(rec Records, lease Lease) []string {
	return starts(SelectStarts([]ProjectInput{{Records: rec, Lease: lease}}, base, false))
}

func prepareDue(id int64, mods ...tmod) Task {
	task := tk(id, StatusPackaged, mods...)
	task.Delivery.CandidateID, task.Delivery.CandidateMerged = 0, false
	return task
}

func approvedTask(id int64) Task {
	return tk(id, StatusPackaged, decided(DecisionApproved, at(-5)))
}

func execution(run string, op Operation, task int64, target string) Execution {
	return Execution{RunID: run, Operation: op, TaskID: task, Target: target}
}

func TestDecideHoldsForCapture(t *testing.T) {
	worked := tk(1, StatusWorked)
	worked.Delivery.CaptureRun = "worker-run"
	rec := recs([]Task{worked, tk(2, StatusSeeded)}, nil)
	if got := decide(t, rec, freeLock()); got.Idle != IdleRepositoryOperation {
		t.Fatalf("A due capture did not hold the agent runs: %+v", got)
	}
	rec.Retries = []Retry{{Target: "worker-run", Operation: OperationCapture, TaskID: 1, Until: at(1)}}
	if got := decide(t, rec, freeLock()); got.Idle != IdleRepositoryOperation {
		t.Fatalf("A capture in its retry time did not hold the agent runs: %+v", got)
	}
	rec.Retries = nil
	rec.Executions = []Execution{execution("capture-run", OperationCapture, 1, "worker-run")}
	if got := decide(t, rec, freeLock()); got.Idle != IdleRepositoryOperation {
		t.Fatalf("An active capture did not hold the agent runs: %+v", got)
	}
	rec.Tasks[0].Stalled = true
	wantSpawn(t, decide(t, rec, freeLock()), RoleWorker, pi64(2))
	rec.Executions = nil
	wantSpawn(t, decide(t, rec, freeLock()), RoleWorker, pi64(2))
}

func TestDecideHoldsForPrepare(t *testing.T) {
	rec := recs([]Task{prepareDue(1), tk(2, StatusSeeded)}, []Packet{pk(1)})
	if got := decide(t, rec, freeLock()); got.Idle != IdleRepositoryOperation {
		t.Fatalf("A due merge candidate step did not hold the agent runs: %+v", got)
	}
	rec.Retries = []Retry{{Target: "task/1", Operation: OperationPrepare, TaskID: 1, Until: at(1)}}
	if got := decide(t, rec, freeLock()); got.Idle != IdleRepositoryOperation {
		t.Fatalf("A merge candidate step in its retry time did not hold the agent runs: %+v", got)
	}
	rec.Retries = nil
	rec.Executions = []Execution{execution("prepare-run", OperationPrepare, 1, "task/1")}
	if got := decide(t, rec, freeLock()); got.Idle != IdleRepositoryOperation {
		t.Fatalf("An active merge candidate step did not hold the agent runs: %+v", got)
	}
	for name, change := range map[string]func(*Task){
		"blocked": func(task *Task) { task.Blocked = true },
		"stalled": func(task *Task) { task.Stalled = true },
	} {
		t.Run(name, func(t *testing.T) {
			held := deepCopyRecords(rec)
			change(&held.Tasks[0])
			wantSpawn(t, decide(t, held, freeLock()), RoleWorker, pi64(2))
			held.Executions = nil
			wantSpawn(t, decide(t, held, freeLock()), RoleWorker, pi64(2))
		})
	}
}

func TestDecideIgnoresCleanupObserveApplyReconcile(t *testing.T) {
	observed := tk(1, StatusPackaged)
	observed.Delivery.WaitUntil = pt(at(-1))
	recovered := approvedTask(3)
	recovered.Delivery.RecoveryRun, recovered.Delivery.RecoveryOperation = "apply-run", "apply"
	recovered.Delivery.ApplyPending = true
	parent := tk(9, StatusSeeded)
	for _, tc := range []struct {
		name string
		rec  Records
		want string
	}{
		{name: "cleanup", rec: Records{Cleanups: []Cleanup{{TaskID: 4, RunID: "worker-run"}}}, want: "cleanup:4"},
		{name: "observe", rec: Records{Tasks: []Task{observed}}, want: "observe:1"},
		{name: "apply", rec: Records{Tasks: []Task{parent, approvedTask(2)}}, want: "apply:2"},
		{name: "reconcile", rec: Records{Tasks: []Task{parent, recovered}}, want: "reconcile:3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := tc.rec
			rec.Project, rec.VisionReady = "ws", true
			rec.Tasks = append(rec.Tasks, tk(4, StatusVerified, prio(-1)))
			if got := selectOne(rec, freeLock()); !reflect.DeepEqual(got, []string{tc.want}) {
				t.Fatalf("The start request selected %v", got)
			}
			if AgentRunHold(rec, base) {
				t.Fatal("The repository operation held a new agent run.")
			}
			for i, c := range Candidates(rec, base) {
				rec.Executions = append(rec.Executions, execution("run-"+strconv.Itoa(i), c.Operation, c.TaskID, c.Target()))
			}
			if len(rec.Executions) != 1 {
				t.Fatalf("The fixture has the executions %+v", rec.Executions)
			}
			if AgentRunHold(rec, base) {
				t.Fatal("The repository operation held a new agent run.")
			}
		})
	}
}

func TestDecideIgnoresWaitingPrepareRecovery(t *testing.T) {
	interrupted := tk(1, StatusPackaged)
	interrupted.Delivery.CandidateID, interrupted.Delivery.CandidateMerged = 0, false
	interrupted.Delivery.RecoveryRun, interrupted.Delivery.RecoveryOperation = "prepare-run", "prepare"
	interrupted.Delivery.RecoveryStopPending = true
	rec := recs([]Task{interrupted, tk(2, StatusSeeded)}, []Packet{pk(1)})
	candidates := Candidates(rec, base)
	if len(candidates) != 1 || candidates[0].State != CandidateWaiting || candidates[0].At != nil || candidates[0].RunID != "prepare-run" {
		t.Fatalf("The prepare recovery does not wait for the stop confirmation: %+v", candidates)
	}
	wantSpawn(t, decide(t, rec, freeLock()), RoleWorker, pi64(2))
	if got := selectOne(rec, freeLock()); len(got) != 0 {
		t.Fatalf("The prepare recovery started before the stop confirmation: %v", got)
	}
	rec.Tasks[0].Delivery.RecoveryStopPending = false
	if got := decide(t, rec, freeLock()); got.Idle != IdleRepositoryOperation {
		t.Fatalf("The due prepare recovery did not hold the agent runs: %+v", got)
	}
	if got := SelectStarts([]ProjectInput{{Records: rec}}, base, false); len(got) != 1 || got[0].RunID != "prepare-run" || got[0].Target() != "task/1" {
		t.Fatalf("The prepare recovery did not start: %+v", got)
	}
}

func TestDecideSkipsSpawnOfTaskWithExecution(t *testing.T) {
	// A record race can leave an active execution of the task that Decide selects.
	rec := recs([]Task{tk(1, StatusSeeded)}, nil)
	rec.Executions = []Execution{execution("observe-run", OperationObserve, 1, "task/1")}
	if got := decide(t, rec, freeLock()); got.Idle != IdleRepositoryOperation {
		t.Fatalf("A spawn overlapped an execution of its task: %+v", got)
	}
	rec.Executions = []Execution{execution("cleanup-run", OperationCleanup, 1, "worker-run")}
	wantSpawn(t, decide(t, rec, freeLock()), RoleWorker, pi64(1))
}

func TestSelectStartsCleanupRoundRobinLimit(t *testing.T) {
	project := func(name string, runs ...string) ProjectInput {
		rec := recs(nil, nil)
		rec.Project = name
		for i, run := range runs {
			rec.Cleanups = append(rec.Cleanups, Cleanup{TaskID: int64(i + 1), RunID: run})
		}
		return ProjectInput{Records: rec}
	}
	projects := []ProjectInput{
		project("p3", "c1", "c2"),
		project("p1", "a1", "a2", "a3"),
		project("p2", "b1", "b2"),
	}
	runs := func(ops []RepositoryOperation) []string {
		out := []string{}
		for _, op := range ops {
			if op.Operation != OperationCleanup {
				t.Fatalf("The start request selected %+v", op)
			}
			out = append(out, op.RunID)
		}
		return out
	}
	if got := runs(SelectStarts(projects, base, false)); !reflect.DeepEqual(got, []string{"a1", "b1", "c1", "a2"}) {
		t.Fatalf("The cleanups were not selected round-robin up to the limit: %v", got)
	}
	// Active cleanups in any Project, also with an artifact expiry, use the limit.
	projects[0].Records.ExpiryActive = true
	projects[0].Records.Executions = []Execution{execution("x1", OperationCleanup, 1, "c0")}
	projects[1].Records.Executions = []Execution{execution("x2", OperationCleanup, 1, "a0")}
	if got := runs(SelectStarts(projects, base, false)); !reflect.DeepEqual(got, []string{"a1", "b1"}) {
		t.Fatalf("Active cleanups did not use the limit: %v", got)
	}
	projects[2].Records.Executions = []Execution{execution("x3", OperationCleanup, 1, "b0"), execution("x4", OperationCleanup, 2, "b9")}
	if got := runs(SelectStarts(projects, base, false)); len(got) != 0 {
		t.Fatalf("The start request exceeded %d cleanups: %v", MaxCleanups, got)
	}
}

func TestSelectStartsOneMergeStepPerProject(t *testing.T) {
	first := recs([]Task{prepareDue(4), prepareDue(6), approvedTask(8), tk(9, StatusSeeded)}, nil)
	first.Project = "p1"
	second := recs([]Task{prepareDue(5)}, nil)
	second.Project = "p2"
	projects := []ProjectInput{{Records: second}, {Records: first}}
	if got := starts(SelectStarts(projects, base, false)); !reflect.DeepEqual(got, []string{"apply:8", "prepare:5"}) {
		t.Fatalf("The start request selected %v", got)
	}
	projects[1].Records.Tasks[2].Archived = true
	if got := starts(SelectStarts(projects, base, false)); !reflect.DeepEqual(got, []string{"prepare:4", "prepare:5"}) {
		t.Fatalf("The start request selected %v", got)
	}
	projects[1].Records.Executions = []Execution{execution("observe-run", OperationObserve, 12, "task/12")}
	if got := starts(SelectStarts(projects, base, false)); !reflect.DeepEqual(got, []string{"prepare:5"}) {
		t.Fatalf("An active merge step did not fill the place of its Project: %v", got)
	}
	// The priority task precedes a prepare recovery.
	recovering := tk(7, StatusPackaged)
	recovering.Delivery.RecoveryRun, recovering.Delivery.RecoveryOperation = "prepare-run", "prepare"
	projects[1].Records.Executions = nil
	projects[1].Records.Tasks[2].Archived = false
	projects[1].Records.Tasks = append(projects[1].Records.Tasks, recovering)
	got := SelectStarts(projects, base, false)
	if starts(got)[0] != "apply:8" {
		t.Fatalf("The priority task did not precede the prepare recovery: %+v", got)
	}
	applied := approvedTask(10)
	applied.Delivery.RecoveryRun, applied.Delivery.RecoveryOperation = "apply-run", "apply"
	projects[1].Records.Tasks = append(projects[1].Records.Tasks, applied)
	if got := starts(SelectStarts(projects, base, false)); !reflect.DeepEqual(got, []string{"reconcile:10", "prepare:5"}) {
		t.Fatalf("The receipt recovery did not precede the other merge steps: %v", got)
	}
}

func TestCandidatesMergeRules(t *testing.T) {
	correction := func(id int64) Task {
		task := tk(id, StatusSeeded)
		task.Delivery.WorkStarted = true
		return task
	}
	recovery := func(task Task, pending bool) Task {
		task.Delivery.RecoveryRun, task.Delivery.RecoveryOperation = "apply-run", "apply"
		task.Delivery.ApplyPending, task.Delivery.RecoveryStopPending = true, pending
		return task
	}
	approved := func(id int64, mods ...tmod) Task {
		return tk(id, StatusPackaged, append([]tmod{decided(DecisionApproved, at(-1))}, mods...)...)
	}
	parent := tk(20, StatusSeeded)
	for _, tc := range []struct {
		name   string
		tasks  []Task
		change func(*Records)
		want   []string
		held   []string // the result when the task holds the Project lease
	}{
		{name: "1 receipt recovery precedes each other merge step", tasks: []Task{parent, recovery(approvedTask(3), false), approvedTask(2), prepareDue(5)},
			want: []string{"reconcile:3"}},
		{name: "2 receipt recovery in its retry time holds the merge steps", tasks: []Task{parent, recovery(approvedTask(3), false), prepareDue(5)},
			change: func(r *Records) {
				r.Retries = []Retry{{Target: "apply-run", Operation: OperationReconcile, TaskID: 3, Until: at(1)}}
			}, want: []string{}},
		{name: "3 an approval merges before a correction", tasks: []Task{approved(2), correction(6)}, want: []string{"apply:2"}},
		{name: "4 a correction holds no merge step of another task", tasks: []Task{prepareDue(3), prepareDue(6, func(t *Task) { t.Delivery.WorkStarted = true })},
			want: []string{"prepare:3"}},
		{name: "5 released correction frees the merge steps", tasks: []Task{prepareDue(3), tk(6, StatusPackaged)}, want: []string{"prepare:3"}},
		{name: "6 approved tasks merge in task id order", tasks: []Task{parent, approvedTask(8), approvedTask(4)}, want: []string{"apply:4"}},
		{name: "7 the merge of the priority task precedes another merge candidate step", tasks: []Task{prepareDue(1), approved(2)}, want: []string{"apply:2"}},
		{name: "7 the merge of the priority task precedes a prepare recovery", tasks: []Task{approved(2), tk(1, StatusPackaged, func(t *Task) {
			t.Delivery.RecoveryRun, t.Delivery.RecoveryOperation = "prepare-run", "prepare"
		})}, want: []string{"apply:2"}},
		{name: "7 another task receives its merge candidate step beside the priority task", tasks: []Task{approved(2), prepareDue(5)},
			change: func(r *Records) {
				r.Retries = []Retry{{Target: "task/2", Operation: OperationApply, TaskID: 2, Until: at(1)}}
			}, want: []string{"prepare:5"}},
		{name: "7 another approved task waits with its new merge candidate", tasks: []Task{approved(2), approved(3, func(t *Task) {
			t.Delivery.CandidateID, t.Delivery.CandidateMerged = 0, false
		})}, change: func(r *Records) {
			r.Retries = []Retry{{Target: "task/2", Operation: OperationApply, TaskID: 2, Until: at(1)}}
		}, want: []string{}},
		{name: "7 cancelled approval releases the Project priority", tasks: []Task{approved(2), prepareDue(5)},
			change: func(r *Records) { r.Tasks[0].Decision, r.Tasks[0].Archived = DecisionCancelled, true }, want: []string{"prepare:5"}},
		{name: "9 blocked task receives no merge step", tasks: []Task{prepareDue(3, blocked()), prepareDue(5)}, want: []string{"prepare:5"}},
		{name: "9 blocked priority task holds later merges", tasks: []Task{approved(2), parent, approvedTask(4)},
			change: func(r *Records) { r.Tasks[0].Stalled = true }, want: []string{}},
		{name: "9 blocked priority task admits the merge candidate steps of other tasks", tasks: []Task{approved(2, blocked()), prepareDue(5), approved(6)},
			want: []string{"prepare:5"}},
		{name: "9 blocked correction holds no merge step", tasks: []Task{parent, prepareDue(11),
			tk(10, StatusSeeded, blocked(), func(t *Task) {
				t.Delivery.WorkStarted = true
			})}, want: []string{"prepare:11"}},
		{name: "10 changed destination waits for the quiet interval", tasks: []Task{approved(2)},
			change: func(r *Records) { r.Tasks[0].Delivery.WaitUntil = pt(at(1)) }, want: []string{}},
		{name: "10 changed destination is observed after the quiet interval", tasks: []Task{approved(2)},
			change: func(r *Records) { r.Tasks[0].Delivery.WaitUntil = pt(at(0)) }, want: []string{"observe:2"}},
		{name: "11 approved task receives a new merge candidate", tasks: []Task{approved(2)},
			change: func(r *Records) { r.Tasks[0].Delivery.CandidateID, r.Tasks[0].Delivery.CandidateMerged = 0, false }, want: []string{"prepare:2"}},
		{name: "11 approval stays valid for the new merge candidate", tasks: []Task{approved(2)}, want: []string{"apply:2"}},
		{name: "explicit pause", tasks: []Task{approved(2), prepareDue(5)}, change: func(r *Records) { r.ProjectStatus = "paused" }, want: []string{}},
		{name: "attention threshold", tasks: []Task{approved(2)}, change: func(r *Records) { r.AttentionTaskIDs = []int64{11, 12, 13} }, want: []string{}},
		{name: "missing vision", tasks: []Task{approved(2)}, change: func(r *Records) { r.VisionReady = false }, want: []string{}},
		{name: "preventions keep receipt recovery", tasks: []Task{parent, recovery(approvedTask(3), false)},
			change: func(r *Records) { r.ProjectStatus, r.VisionReady = "paused", false }, want: []string{"reconcile:3"}},
		{name: "authorization hold", tasks: []Task{parent, recovery(approvedTask(9), false), approvedTask(7)},
			change: func(r *Records) { r.Tasks[1].Blocked = true }, want: []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := recs(append(tc.tasks, tk(99, StatusSeeded)), nil)
			if tc.change != nil {
				tc.change(&rec)
			}
			if got := selectOne(rec, freeLock()); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("The free lease selected %v, want %v", got, tc.want)
			}
			if got := selectOne(rec, freshLock(pi64(99))); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("The held lease selected %v, want %v", got, tc.want)
			}
			held := tc.want
			if tc.held != nil {
				held = tc.held
			}
			if got := selectOne(rec, freshLock(pi64(20))); !reflect.DeepEqual(got, held) {
				t.Fatalf("The task lease selected %v, want %v", got, held)
			}
		})
	}
	// Rule 8: an approved task holds the agent runs of unrelated work.
	rec := recs([]Task{approved(2), tk(5, StatusVerified, prio(-1))}, nil)
	if got := decide(t, rec, freeLock()); got.Idle != "priority-task-wait" {
		t.Fatalf("The approval permitted unrelated work: %+v", got)
	}
}

func TestRecoveryRetryHoldsMergeSteps(t *testing.T) {
	recovering := approvedTask(3)
	recovering.Delivery.RecoveryRun, recovering.Delivery.RecoveryOperation, recovering.Delivery.ApplyPending = "apply-run", "apply", true
	rec := recs([]Task{tk(20, StatusSeeded), recovering, prepareDue(5)}, nil)
	// The retry time of the interrupted merge holds the first receipt recovery.
	rec.Retries = []Retry{{Target: "task/3", Operation: OperationApply, TaskID: 3, Until: at(1)}}
	candidates := Candidates(rec, base)
	if len(candidates) != 1 || candidates[0].Operation != OperationReconcile || candidates[0].State != CandidateRetry || !candidates[0].At.Equal(at(1)) {
		t.Fatalf("The receipt recovery ignored the retry time of the merge: %+v", candidates)
	}
	if got := selectOne(rec, freeLock()); len(got) != 0 {
		t.Fatalf("A merge step started during the retry time of receipt recovery: %v", got)
	}
	if due := DueTime([]ProjectInput{{Records: rec}}, base); due == nil || !due.Equal(at(1)) {
		t.Fatalf("The due time is %v", due)
	}
	if state := MergeStateOf(rec, WikiMergeRecords{}, 3, base); state.Kind != MergeStateRecovery || state.RelatedTask != 0 {
		t.Fatalf("The merge state is %+v", state)
	}
	// A failed receipt recovery receives its own retry time.
	rec.Retries = []Retry{{Target: "apply-run", Operation: OperationReconcile, TaskID: 3, Until: at(2)}}
	if got := selectOne(rec, freeLock()); len(got) != 0 {
		t.Fatalf("A merge step started during the retry time of receipt recovery: %v", got)
	}
	if got := SelectStarts([]ProjectInput{{Records: rec}}, at(2), false); !reflect.DeepEqual(starts(got), []string{"reconcile:3"}) || got[0].Target() != "apply-run" {
		t.Fatalf("The receipt recovery did not start after its retry time: %+v", got)
	}
}

func TestRecoveryWaitingHoldsMergeSteps(t *testing.T) {
	recovering := approvedTask(3)
	recovering.Delivery.RecoveryRun, recovering.Delivery.RecoveryOperation = "apply-run", "apply"
	recovering.Delivery.ApplyPending, recovering.Delivery.RecoveryStopPending = true, true
	rec := recs([]Task{tk(20, StatusSeeded), recovering, prepareDue(5), tk(6, StatusSeeded)}, nil)
	candidates := Candidates(rec, base)
	if len(candidates) != 1 || candidates[0].State != CandidateWaiting {
		t.Fatalf("The receipt recovery does not wait: %+v", candidates)
	}
	if got := selectOne(rec, freeLock()); len(got) != 0 {
		t.Fatalf("A merge step started while receipt recovery waits: %v", got)
	}
	if due := DueTime([]ProjectInput{{Records: rec}}, base); due != nil {
		t.Fatalf("A stop confirmation received a due time: %v", due)
	}
	if got := decide(t, rec, freeLock()); got.Idle != "priority-task-wait" {
		t.Fatalf("The priority task allowed another agent run: %+v", got)
	}
	if state := MergeStateOf(rec, WikiMergeRecords{}, 3, base); state.Kind != MergeStateRecovery {
		t.Fatalf("The merge state is %+v", state)
	}
}

func TestRecoveryRequiresStopConfirmation(t *testing.T) {
	recovering := approvedTask(3)
	recovering.Delivery.RecoveryRun, recovering.Delivery.RecoveryOperation = "apply-run", "apply"
	recovering.Delivery.ApplyPending, recovering.Delivery.RecoveryStopPending = true, true
	rec := recs([]Task{tk(20, StatusSeeded), recovering}, nil)
	for _, drain := range []bool{false, true} {
		if got := SelectStarts([]ProjectInput{{Records: rec}}, base, drain); len(got) != 0 {
			t.Fatalf("Receipt recovery started before the stop confirmation: %+v", got)
		}
	}
	rec.Tasks[1].Delivery.RecoveryStopPending = false
	for _, drain := range []bool{false, true} {
		got := SelectStarts([]ProjectInput{{Records: rec}}, base, drain)
		if len(got) != 1 || got[0].Operation != OperationReconcile || got[0].RunID != "apply-run" {
			t.Fatalf("Receipt recovery did not start after the stop confirmation: %+v", got)
		}
	}
	rec.Executions = []Execution{execution("reconcile-run", OperationReconcile, 3, "apply-run")}
	if got := selectOne(rec, freeLock()); len(got) != 0 {
		t.Fatalf("A second receipt recovery started: %v", got)
	}
	if state := MergeStateOf(rec, WikiMergeRecords{}, 3, base); state.Kind != MergeStateRecovery {
		t.Fatalf("The merge state is %+v", state)
	}
}

func TestRecoveryExcludesBlockedOrStalledTask(t *testing.T) {
	for name, change := range map[string]func(*Task){
		"blocked": func(task *Task) { task.Blocked = true },
		"stalled": func(task *Task) { task.Stalled = true },
	} {
		t.Run(name, func(t *testing.T) {
			recovering := approvedTask(9)
			recovering.Delivery.RecoveryRun, recovering.Delivery.RecoveryOperation, recovering.Delivery.ApplyPending = "apply-run", "apply", true
			change(&recovering)
			rec := recs([]Task{tk(20, StatusSeeded), recovering, approvedTask(7), prepareDue(12)}, nil)
			if got := selectOne(rec, freeLock()); len(got) != 0 {
				t.Fatalf("The start request selected %v", got)
			}
			if candidates := Candidates(rec, base); len(candidates) != 0 {
				t.Fatalf("The held Project admitted %+v", candidates)
			}
			if state := MergeStateOf(rec, WikiMergeRecords{}, 9, base); state.Kind != MergeStateBlocked {
				t.Fatalf("The merge state of the held task is %+v", state)
			}
			want := MergeState{Kind: MergeStateAfterTask, RelatedTask: 9, RelatedBlocked: true}
			if state := MergeStateOf(rec, WikiMergeRecords{}, 7, base); state != want {
				t.Fatalf("The merge state of the other task is %+v", state)
			}
		})
	}
}

func TestApprovedOrderRule(t *testing.T) {
	parent := tk(20, StatusSeeded)
	rec := recs([]Task{parent, approvedTask(6), approvedTask(4)}, nil)
	if got := selectOne(rec, freeLock()); !reflect.DeepEqual(got, []string{"apply:4"}) {
		t.Fatalf("The start request selected %v", got)
	}
	if state := MergeStateOf(rec, WikiMergeRecords{}, 6, base); state != (MergeState{Kind: MergeStateAfterTask, RelatedTask: 4}) {
		t.Fatalf("The merge state is %+v", state)
	}
	// A waiting merge step keeps the place of its task.
	rec.Tasks[2].Delivery.WaitUntil = pt(at(1))
	if got := selectOne(rec, freeLock()); len(got) != 0 {
		t.Fatalf("A later approval merged first: %v", got)
	}
	if state := MergeStateOf(rec, WikiMergeRecords{}, 4, base); state.Kind != MergeStateNewCandidate {
		t.Fatalf("The merge state is %+v", state)
	}
	// The priority task precedes a later prepare recovery.
	rec.Tasks[2].Delivery.WaitUntil = nil
	rec.Tasks[1].Delivery.RecoveryRun, rec.Tasks[1].Delivery.RecoveryOperation = "prepare-run", "prepare"
	if got := selectOne(rec, freeLock()); !reflect.DeepEqual(got, []string{"apply:4"}) {
		t.Fatalf("The start request selected %v", got)
	}
	// A blocked priority task holds each later approval.
	rec.Tasks[1].Delivery.RecoveryRun, rec.Tasks[1].Delivery.RecoveryOperation = "", ""
	rec.Tasks[2].Blocked = true
	if got := selectOne(rec, freeLock()); len(got) != 0 {
		t.Fatalf("The start request selected %v", got)
	}
}

func TestReviewPublicationsIgnorePrevention(t *testing.T) {
	ready := func(id int64) Task {
		task := tk(id, StatusPackaged)
		task.Delivery.ReviewPending = true
		return task
	}
	rec := recs([]Task{ready(5), ready(3), ready(4), ready(7)}, []Packet{pk(9)})
	rec.ProjectStatus = "paused"
	rec.VisionReady = false
	rec.AttentionTaskIDs = []int64{11, 12, 13}
	rec.Tasks[3].Blocked = true
	cfg := DefaultConfig()
	if got := ReviewPublications(rec, cfg); !reflect.DeepEqual(got, []int64{3, 4}) {
		t.Fatalf("The publications are %v", got)
	}
	rec.Tasks[1].Delivery.CandidateMerged = false
	if got := ReviewPublications(rec, cfg); !reflect.DeepEqual(got, []int64{4, 5}) {
		t.Fatalf("The publications are %v", got)
	}
	rec.Packets = []Packet{pk(8), pk(9), pk(10)}
	if got := ReviewPublications(rec, cfg); len(got) != 0 {
		t.Fatalf("The publications exceeded the review capacity: %v", got)
	}
}

func TestSelectStartsDrainReturnsRecoveryOnly(t *testing.T) {
	recovering := approvedTask(3)
	recovering.Delivery.RecoveryRun, recovering.Delivery.RecoveryOperation, recovering.Delivery.ApplyPending = "apply-run", "apply", true
	worked := tk(5, StatusWorked)
	worked.Delivery.CaptureRun = "worker-run"
	rec := recs([]Task{tk(20, StatusSeeded), recovering, worked}, nil)
	rec.Cleanups = []Cleanup{{TaskID: 6, RunID: "old-run"}}
	if got := starts(SelectStarts([]ProjectInput{{Records: rec}}, base, false)); !reflect.DeepEqual(got, []string{"capture:5", "reconcile:3", "cleanup:6"}) {
		t.Fatalf("The start request selected %v", got)
	}
	if got := starts(SelectStarts([]ProjectInput{{Records: rec}}, base, true)); !reflect.DeepEqual(got, []string{"reconcile:3"}) {
		t.Fatalf("The drain start request selected %v", got)
	}
}

func TestDueTimeQuietInterval(t *testing.T) {
	approved := tk(2, StatusPackaged, decided(DecisionApproved, at(-1)))
	quiet := base.Add(42 * time.Second)
	approved.Delivery.WaitUntil = &quiet
	rec := recs([]Task{approved}, nil)
	other := recs([]Task{prepareDue(7)}, nil)
	other.Project = "p2"
	other.Retries = []Retry{{Target: "task/7", Operation: OperationPrepare, TaskID: 7, Until: at(3)}}
	projects := []ProjectInput{{Records: rec}, {Records: other}}
	if due := DueTime(projects, base); due == nil || !due.Equal(quiet) {
		t.Fatalf("The due time is %v, want the end of the quiet interval", due)
	}
	if got := SelectStarts(projects, base, false); len(got) != 0 {
		t.Fatalf("The start request selected %+v", got)
	}
	if state := MergeStateOf(rec, WikiMergeRecords{}, 2, base); state.Kind != MergeStateNewCandidate {
		t.Fatalf("The merge state is %+v", state)
	}
	if due := DueTime(projects, quiet); due == nil || !due.Equal(at(3)) {
		t.Fatalf("The due time is %v, want the end of the retry time", due)
	}
	if got := starts(SelectStarts(projects, quiet, false)); !reflect.DeepEqual(got, []string{"observe:2"}) {
		t.Fatalf("The start request selected %v", got)
	}
	if due := DueTime(projects, at(3)); due != nil {
		t.Fatalf("The due time is %v", due)
	}
}

func TestMergeStateOf(t *testing.T) {
	parent := tk(20, StatusSeeded)
	approved := func(id int64) Task { return tk(id, StatusPackaged, decided(DecisionApproved, at(-1))) }
	recovery := func(task Task) Task {
		task.Delivery.RecoveryRun, task.Delivery.RecoveryOperation, task.Delivery.ApplyPending = "apply-run", "apply", true
		return task
	}
	retry := Retry{Target: "task/2", Operation: OperationApply, TaskID: 2, Until: at(1)}
	for _, tc := range []struct {
		name   string
		tasks  []Task
		change func(*Records)
		want   MergeState
	}{
		{name: "merge", tasks: []Task{approved(2)}, want: MergeState{Kind: MergeStateMerge}},
		{name: "active merge", tasks: []Task{approved(2)}, change: func(r *Records) {
			r.Executions = []Execution{execution("apply-run", OperationApply, 2, "task/2")}
		}, want: MergeState{Kind: MergeStateMerge}},
		{name: "new candidate", tasks: []Task{approved(2)}, change: func(r *Records) {
			r.Tasks[0].Delivery.CandidateID, r.Tasks[0].Delivery.CandidateMerged = 0, false
		}, want: MergeState{Kind: MergeStateNewCandidate}},
		{name: "active merge during a pause", tasks: []Task{approved(2)}, change: func(r *Records) {
			r.ProjectStatus = "paused"
			r.Executions = []Execution{execution("apply-run", OperationApply, 2, "task/2")}
		}, want: MergeState{Kind: MergeStateMerge}},
		{name: "active candidate step with three attention tasks", tasks: []Task{approved(2)}, change: func(r *Records) {
			r.AttentionTaskIDs = []int64{11, 12, 13}
			r.Executions = []Execution{execution("prepare-run", OperationPrepare, 2, "task/2")}
		}, want: MergeState{Kind: MergeStateNewCandidate}},
		{name: "active observation", tasks: []Task{approved(2)}, change: func(r *Records) {
			r.Tasks[0].Delivery.WaitUntil = pt(at(-1))
			r.Executions = []Execution{execution("observe-run", OperationObserve, 2, "task/2")}
		}, want: MergeState{Kind: MergeStateNewCandidate}},
		{name: "after merge step", tasks: []Task{approved(2), prepareDue(6)}, change: func(r *Records) {
			r.Executions = []Execution{execution("prepare-run", OperationPrepare, 6, "task/6")}
		}, want: MergeState{Kind: MergeStateAfterMergeStep, RelatedTask: 6}},
		{name: "after task", tasks: []Task{approved(1), approved(2)}, want: MergeState{Kind: MergeStateAfterTask, RelatedTask: 1}},
		{name: "after blocked task", tasks: []Task{approved(1), approved(2)}, change: func(r *Records) { r.Tasks[0].Blocked = true },
			want: MergeState{Kind: MergeStateAfterTask, RelatedTask: 1, RelatedBlocked: true}},
		{name: "a merge repair after an approval", tasks: []Task{approved(1), approved(2)}, change: func(r *Records) {
			r.Tasks[0].Status, r.Tasks[0].Decision, r.Tasks[0].Delivery = StatusSeeded, DecisionNone, Delivery{WorkStarted: true}
		}, want: MergeState{Kind: MergeStateMerge}},
		{name: "a blocked merge repair", tasks: []Task{approved(2), tk(8, StatusSeeded, blocked())}, change: func(r *Records) { r.Tasks[1].Delivery.WorkStarted = true },
			want: MergeState{Kind: MergeStateMerge}},
		{name: "paused", tasks: []Task{approved(2)}, change: func(r *Records) { r.ProjectStatus = "paused" }, want: MergeState{Kind: MergeStatePaused}},
		{name: "attention", tasks: []Task{approved(2)}, change: func(r *Records) { r.AttentionTaskIDs = []int64{11, 12, 13} }, want: MergeState{Kind: MergeStateAttention}},
		{name: "vision", tasks: []Task{approved(2)}, change: func(r *Records) { r.VisionReady = false }, want: MergeState{Kind: MergeStateVision}},
		{name: "recovery of this task", tasks: []Task{recovery(approved(2))}, want: MergeState{Kind: MergeStateRecovery}},
		{name: "recovery of another task", tasks: []Task{parent, recovery(approvedTask(5)), approved(2)},
			want: MergeState{Kind: MergeStateRecovery, RelatedTask: 5}},
		{name: "retry", tasks: []Task{approved(2)}, change: func(r *Records) { r.Retries = []Retry{retry} }, want: MergeState{Kind: MergeStateRetry}},
		{name: "blocked", tasks: []Task{approved(2)}, change: func(r *Records) { r.Tasks[0].Stalled = true }, want: MergeState{Kind: MergeStateBlocked}},
		{name: "retry with a pause", tasks: []Task{approved(2)}, change: func(r *Records) {
			r.Retries, r.ProjectStatus = []Retry{retry}, "paused"
		}, want: MergeState{Kind: MergeStatePaused}},
		{name: "retry with receipt recovery", tasks: []Task{parent, recovery(approvedTask(5)), approved(2)}, change: func(r *Records) { r.Retries = []Retry{retry} },
			want: MergeState{Kind: MergeStateRecovery, RelatedTask: 5}},
		{name: "retry with another priority task", tasks: []Task{approved(1), approved(2)}, change: func(r *Records) { r.Retries = []Retry{retry} },
			want: MergeState{Kind: MergeStateAfterTask, RelatedTask: 1}},
		{name: "authorization hold", tasks: []Task{parent, recovery(approvedTask(9)), approved(2)}, change: func(r *Records) { r.Tasks[1].Blocked = true },
			want: MergeState{Kind: MergeStateAfterTask, RelatedTask: 9, RelatedBlocked: true}},
		{name: "authorization hold with a pause", tasks: []Task{parent, recovery(approvedTask(9)), approved(2)}, change: func(r *Records) {
			r.Tasks[1].Stalled, r.ProjectStatus = true, "paused"
		}, want: MergeState{Kind: MergeStateAfterTask, RelatedTask: 9, RelatedBlocked: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := recs(tc.tasks, nil)
			if tc.change != nil {
				tc.change(&rec)
			}
			if got := MergeStateOf(rec, WikiMergeRecords{}, 2, base); got != tc.want {
				t.Fatalf("The merge state is %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestOperationDeadlinesAndTargets(t *testing.T) {
	for op, want := range map[Operation]time.Duration{
		OperationCapture: 5 * time.Minute, OperationCleanup: 5 * time.Minute, OperationObserve: 5 * time.Minute,
		OperationReconcile: 5 * time.Minute,
		OperationPrepare:   10 * time.Minute, OperationApply: 10 * time.Minute,
	} {
		if got := op.Deadline(); got != want {
			t.Fatalf("The %s deadline is %v, want %v", op, got, want)
		}
		merge := op == OperationObserve || op == OperationPrepare || op == OperationApply || op == OperationReconcile
		if op.MergeStep() != merge {
			t.Fatalf("The %s merge step flag is %v", op, op.MergeStep())
		}
		target := RepositoryOperation{TaskID: 7, Operation: op, RunID: "0123456789abcdef"}.Target()
		run := op == OperationCapture || op == OperationCleanup || op == OperationReconcile
		if (run && target != "0123456789abcdef") || (!run && target != "task/7") {
			t.Fatalf("The %s target is %q", op, target)
		}
	}
}

// A repository operation has one subject. The subject gives the target of each
// operation that changes its record; the other operations keep the run as their target.
func TestWikiSubjectTargets(t *testing.T) {
	const run, other = "0123456789abcdef", "fedcba9876543210"
	task, wiki := TaskSubject(7), WikiSubject(run)
	if task.Wiki() || !wiki.Wiki() {
		t.Fatalf("Wiki() = %v for a task and %v for a wiki update, want false and true", task.Wiki(), wiki.Wiki())
	}
	if got := task.Target(); got != "task/7" {
		t.Fatalf("The target of a task is %q, want task/7", got)
	}
	if got := wiki.Target(); got != "wiki/"+run {
		t.Fatalf("The target of a wiki update is %q, want wiki/%s", got, run)
	}
	for _, c := range []struct {
		subject Subject
		valid   bool
	}{
		{task, true}, {wiki, true}, {Subject{}, false}, {Subject{TaskID: 7, WikiUpdate: run}, false},
	} {
		if c.subject.Valid() != c.valid {
			t.Fatalf("Valid() of %+v = %v, want %v", c.subject, c.subject.Valid(), c.valid)
		}
	}
	for _, op := range []Operation{OperationCapture, OperationCleanup, OperationPrepare, OperationApply, OperationReconcile} {
		operation := RepositoryOperation{Project: "p", WikiUpdate: run, Operation: op, RunID: other}
		if got := operation.Subject(); got != wiki {
			t.Fatalf("The subject of the %s is %+v, want the wiki update", op, got)
		}
		want := "wiki/" + run
		if op == OperationCapture || op == OperationCleanup || op == OperationReconcile {
			want = other
		}
		if got := operation.Target(); got != want {
			t.Fatalf("The %s target of a wiki update is %q, want %q", op, got, want)
		}
	}
	if got := (RepositoryOperation{TaskID: 7, Operation: OperationApply}).Subject(); got != task {
		t.Fatalf("The subject of a task operation is %+v, want the task", got)
	}
}

func TestSelectStartsSkipsProjectWithArtifactExpiry(t *testing.T) {
	worked := tk(1, StatusWorked)
	worked.Delivery.CaptureRun = "worker-run"
	expiring := recs([]Task{worked, prepareDue(2)}, nil)
	expiring.Project = "p1"
	expiring.Cleanups = []Cleanup{{TaskID: 3, RunID: "old-run"}}
	other := recs([]Task{prepareDue(4)}, nil)
	other.Project = "p2"
	projects := []ProjectInput{{Records: expiring}, {Records: other}}
	if got := starts(SelectStarts(projects, base, false)); !reflect.DeepEqual(got, []string{"capture:1", "prepare:2", "prepare:4", "cleanup:3"}) {
		t.Fatalf("The start request selected %v", got)
	}
	projects[0].Records.ExpiryActive = true
	if got := starts(SelectStarts(projects, base, false)); !reflect.DeepEqual(got, []string{"prepare:4"}) {
		t.Fatalf("A repository operation overlapped the artifact expiry: %v", got)
	}
}

func TestActiveExecutionIsNotStartedAgain(t *testing.T) {
	worked := tk(1, StatusWorked)
	worked.Delivery.CaptureRun = "worker-run"
	seeded := tk(2, StatusSeeded)
	rec := recs([]Task{worked, seeded, prepareDue(3)}, nil)
	rec.Cleanups = []Cleanup{{TaskID: 4, RunID: "old-run"}}
	if got := selectOne(rec, freeLock()); !reflect.DeepEqual(got, []string{"capture:1", "prepare:3", "cleanup:4"}) {
		t.Fatalf("The start request selected %v", got)
	}
	for i, c := range Candidates(rec, base) {
		rec.Executions = append(rec.Executions, execution("run-"+strconv.Itoa(i), c.Operation, c.TaskID, c.Target()))
	}
	for _, c := range Candidates(rec, base) {
		if c.State != CandidateActive || c.At != nil {
			t.Fatalf("The candidate is not active: %+v", c)
		}
	}
	if got := selectOne(rec, freeLock()); len(got) != 0 {
		t.Fatalf("The start request started an active operation again: %v", got)
	}
}

// A revision can return a task to seeded while its prepare recovery waits.
// The recovery still starts, but it holds no agent run.
func TestDecideIgnoresPrepareRecoveryOfTaskThatIsNotPackaged(t *testing.T) {
	revised := tk(1, StatusSeeded)
	revised.Delivery.CandidateID, revised.Delivery.CandidateMerged = 0, false
	revised.Delivery.RecoveryRun, revised.Delivery.RecoveryOperation = "prepare-run", "prepare"
	rec := recs([]Task{revised, tk(2, StatusSeeded, prio(0))}, nil)
	rec.Retries = []Retry{{Target: "task/1", Operation: OperationPrepare, TaskID: 1, Until: at(1)}}
	wantSpawn(t, decide(t, rec, freeLock()), RoleWorker, pi64(2))
	rec.Retries = nil
	wantSpawn(t, decide(t, rec, freeLock()), RoleWorker, pi64(2))
	if got := selectOne(rec, freeLock()); !reflect.DeepEqual(got, []string{"prepare:1"}) {
		t.Fatalf("The start request selected %v", got)
	}
	rec.Executions = []Execution{execution("recovery-run", OperationPrepare, 1, "task/1")}
	wantSpawn(t, decide(t, rec, freeLock()), RoleWorker, pi64(2))
}

// packagedTask returns a packaged task of the Project "p" with an empty delivery record.
func packagedTask(id int64, edit func(*Task)) Task {
	t := Task{ID: id, Scope: ScopeTask, Status: StatusPackaged, Project: "p"}
	if edit != nil {
		edit(&t)
	}
	return t
}

type laneTaskCase struct {
	name string
	rec  Records
	// authorization: a task of the record set has an unfinished merge authorization.
	// Each case states the value, so the test does not prove the product rule with the product rule.
	authorization bool
}

type laneWikiCase struct {
	name string
	wiki WikiMergeRecords
}

// laneTaskCases returns one record set for each task state that changes the merge lane.
func laneTaskCases() []laneTaskCase {
	past, future := base.Add(-time.Minute), base.Add(time.Minute)
	records := func(tasks ...Task) Records {
		return Records{Project: "p", ProjectStatus: "active", VisionReady: true, Tasks: tasks}
	}
	cases := []laneTaskCase{
		{"no task", records(), false},
		{"prepare due", records(packagedTask(1, nil)), false},
		{"apply due", records(packagedTask(1, func(t *Task) {
			t.Decision = DecisionApproved
			t.Delivery = Delivery{CandidateID: 7, CandidateMerged: true}
		})), false},
		{"observe due", records(packagedTask(1, func(t *Task) { t.Delivery = Delivery{CandidateID: 7, WaitUntil: &past} })), false},
		{"observe waits", records(packagedTask(1, func(t *Task) { t.Delivery = Delivery{CandidateID: 7, WaitUntil: &future} })), false},
		{"task receipt recovery due", records(packagedTask(1, func(t *Task) {
			t.Decision = DecisionApproved
			t.Delivery = Delivery{CandidateID: 7, CandidateMerged: true, ApplyPending: true, RecoveryOperation: "apply", RecoveryRun: "aaaa"}
		})), true},
		{"task receipt recovery waits for the stop", records(packagedTask(1, func(t *Task) {
			t.Decision = DecisionApproved
			t.Delivery = Delivery{CandidateID: 7, CandidateMerged: true, ApplyPending: true, RecoveryOperation: "apply", RecoveryRun: "aaaa", RecoveryStopPending: true}
		})), true},
		{"prepare recovery due", records(packagedTask(1, func(t *Task) {
			t.Delivery = Delivery{RecoveryOperation: "prepare", RecoveryRun: "bbbb"}
		})), false},
		{"authorization of a blocked task", records(packagedTask(1, func(t *Task) {
			t.Decision, t.Blocked = DecisionApproved, true
			t.Delivery = Delivery{CandidateID: 7, CandidateMerged: true, ApplyPending: true, RecoveryOperation: "apply", RecoveryRun: "aaaa"}
		}), packagedTask(2, nil)), true},

		{"two tasks, prepare and apply", records(packagedTask(1, nil), packagedTask(2, func(t *Task) {
			t.Decision = DecisionApproved
			t.Delivery = Delivery{CandidateID: 9, CandidateMerged: true}
		})), false},
	}
	// The two tasks of these record sets have an empty delivery record: no authorization.
	withExecution := func(name string, x Execution) laneTaskCase {
		rec := records(packagedTask(1, nil), packagedTask(2, nil))
		rec.Executions = []Execution{x}
		return laneTaskCase{name, rec, false}
	}
	cases = append(cases,
		withExecution("task prepare active", execution("x1", OperationPrepare, 1, "task/1")),
		withExecution("task apply active", execution("x1", OperationApply, 1, "task/1")),
		withExecution("task observe active", execution("x1", OperationObserve, 1, "task/1")),
		withExecution("task reconcile active", execution("x1", OperationReconcile, 1, "aaaa")),
		withExecution("task capture active", execution("x1", OperationCapture, 1, "cccc")),
		withExecution("task cleanup active", execution("x1", OperationCleanup, 1, "dddd")),
	)
	retry := records(packagedTask(1, nil))
	retry.Retries = []Retry{{Target: "task/1", Operation: OperationPrepare, TaskID: 1, Until: future}}
	cases = append(cases, laneTaskCase{"task prepare in its retry time", retry, false})
	cleanups := records(packagedTask(1, nil))
	cleanups.Cleanups = []Cleanup{{TaskID: 1, RunID: "eeee"}, {TaskID: 1, RunID: "ffff"}}
	cases = append(cases, laneTaskCase{"task cleanups due", cleanups, false})
	return cases
}

// laneWikiCases returns one record set for each state of the wiki update "wwww".
func laneWikiCases() []laneWikiCase {
	future := base.Add(time.Minute)
	u := func(edit func(*WikiDelivery)) *WikiDelivery {
		d := &WikiDelivery{RunID: "wwww"}
		edit(d)
		return d
	}
	authorized := func(d *WikiDelivery) {
		d.Captured, d.Candidate, d.ApplyRun, d.ApplyEnded = true, true, "qqqq", true
	}
	return []laneWikiCase{
		{"no wiki update", WikiMergeRecords{}},
		{"capture due", WikiMergeRecords{Update: u(func(d *WikiDelivery) { d.CaptureDue = true })}},
		{"capture active", WikiMergeRecords{Update: u(func(d *WikiDelivery) { d.CaptureDue = true }),
			Executions: []WikiExecution{{RunID: "y1", Operation: OperationCapture, Target: "wwww"}}}},
		{"prepare due", WikiMergeRecords{Update: u(func(d *WikiDelivery) { d.Captured = true })}},
		{"prepare waits for the quiet interval", WikiMergeRecords{Update: u(func(d *WikiDelivery) { d.Captured, d.WaitUntil = true, &future })}},
		{"prepare active", WikiMergeRecords{Update: u(func(d *WikiDelivery) { d.Captured = true }),
			Executions: []WikiExecution{{RunID: "y1", Operation: OperationPrepare, Target: "wiki/wwww"}}}},
		{"prepare recovery due", WikiMergeRecords{Update: u(func(d *WikiDelivery) { d.Captured, d.PrepareRun = true, "pppp" })}},
		{"prepare recovery waits for the stop", WikiMergeRecords{Update: u(func(d *WikiDelivery) {
			d.Captured, d.PrepareRun, d.PrepareStopPending = true, "pppp", true
		})}},
		{"apply due", WikiMergeRecords{Update: u(func(d *WikiDelivery) { d.Captured, d.Candidate = true, true })}},
		{"apply active", WikiMergeRecords{Update: u(func(d *WikiDelivery) { d.Captured, d.Candidate, d.ApplyRun = true, true, "qqqq" }),
			Executions: []WikiExecution{{RunID: "qqqq", Operation: OperationApply, Target: "wiki/wwww"}}}},
		{"receipt recovery due", WikiMergeRecords{Update: u(authorized)}},
		{"receipt recovery due with a repair", WikiMergeRecords{Update: u(func(d *WikiDelivery) {
			authorized(d)
			d.Repair = "Remove the file."
		})}},
		{"receipt recovery waits for the stop", WikiMergeRecords{Update: u(func(d *WikiDelivery) {
			authorized(d)
			d.ApplyStopPending = true
		})}},
		{"receipt recovery in the retry time of the merge", WikiMergeRecords{Update: u(authorized),
			Retries: []WikiRetry{{Target: "wiki/wwww", Operation: OperationApply, Until: future}}}},
		{"receipt recovery in its own retry time", WikiMergeRecords{Update: u(authorized),
			Retries: []WikiRetry{{Target: "qqqq", Operation: OperationReconcile, Until: future}}}},
		{"receipt recovery active", WikiMergeRecords{Update: u(authorized),
			Executions: []WikiExecution{{RunID: "y2", Operation: OperationReconcile, Target: "qqqq"}}}},
		{"cleanups due", WikiMergeRecords{Cleanups: []WikiCleanup{{WikiUpdate: "vvvv", RunID: "vvvv"}, {WikiUpdate: "vvvv", RunID: "uuuu"}}}},
		{"apply due and a cleanup active", WikiMergeRecords{Update: u(func(d *WikiDelivery) { d.Captured, d.Candidate = true, true }),
			Executions: []WikiExecution{{RunID: "y3", Operation: OperationCleanup, Target: "wwww"}}}},
	}
}

// mergeSteps returns the merge steps of tasks and the merge steps of wiki commits in a selection.
func mergeSteps(ops []RepositoryOperation) (task, wiki []RepositoryOperation) {
	for _, op := range ops {
		if !op.Operation.MergeStep() {
			continue
		}
		if op.Subject().Wiki() {
			wiki = append(wiki, op)
		} else {
			task = append(task, op)
		}
	}
	return task, wiki
}

func mergeStepActive(rec Records, wiki WikiMergeRecords) bool {
	for _, x := range rec.Executions {
		if x.Operation.MergeStep() {
			return true
		}
	}
	for _, x := range wiki.Executions {
		if x.Operation.MergeStep() {
			return true
		}
	}
	return false
}

// TestMergeLaneInvariants runs each task state with each wiki state, each Project state, each lease, and each drain value.
func TestMergeLaneInvariants(t *testing.T) {
	type projectState struct {
		name      string
		status    string
		vision    bool
		attention int
		expiry    bool
	}
	states := []projectState{
		{"active", "active", true, 0, false},
		{"paused", "paused", true, 0, false},
		{"archived", "archived", true, 0, false},
		{"no vision", "active", false, 0, false},
		{"attention threshold", "active", true, 3, false},
		{"artifact expiry", "active", true, 0, true},
	}
	leases := []Lease{{}, {Held: true, SubjectID: pi64(1)}, {Held: true, SubjectID: pi64(50)}}
	count, wikiStarts, holds, leaseHeld := 0, 0, 0, 0
	for _, tc := range laneTaskCases() {
		for _, wc := range laneWikiCases() {
			for _, st := range states {
				for _, lease := range leases {
					for _, drain := range []bool{false, true} {
						rec := tc.rec
						rec.ProjectStatus, rec.VisionReady, rec.ExpiryActive = st.status, st.vision, st.expiry
						rec.AttentionTaskIDs = make([]int64, st.attention)
						name := fmt.Sprintf("%s / %s / %s / lease=%v / drain=%v", tc.name, wc.name, st.name, lease.SubjectID != nil, drain)
						with := SelectStarts([]ProjectInput{{Records: rec, Lease: lease, Wiki: wc.wiki}}, base, drain)
						without := SelectStarts([]ProjectInput{{Records: rec, Lease: lease}}, base, drain)
						free := SelectStarts([]ProjectInput{{Records: rec}}, base, drain)
						taskSteps, wikiSteps := mergeSteps(with)
						taskOnly, _ := mergeSteps(without)
						count++
						// A merge step of a task that waits for an agent run stays due, and it does not start: the wiki step starts.
						if freeSteps, _ := mergeSteps(free); len(freeSteps) == 1 && len(taskOnly) == 0 && len(wikiSteps) == 1 {
							leaseHeld++
						}
						hold := wc.wiki.Update != nil && wc.wiki.Update.ApplyRun != ""
						// A maximum of one merge step starts.
						if len(taskSteps)+len(wikiSteps) > 1 {
							t.Fatalf("%s: more than one merge step starts: %+v", name, with)
						}
						// No merge step starts while a merge step of the Project is active.
						if mergeStepActive(rec, wc.wiki) && len(taskSteps)+len(wikiSteps) != 0 {
							t.Fatalf("%s: a merge step starts beside an active merge step: %+v", name, with)
						}
						// No merge step of a task starts under the hold.
						if hold && len(taskSteps) != 0 {
							t.Fatalf("%s: a merge step of a task starts under the hold: %+v", name, taskSteps)
						}
						if hold {
							holds++
						}
						// The merge candidate step and the merge of a wiki commit start only when no merge step of a task can start.
						// Receipt recovery of the wiki commit starts only under the hold.
						if len(wikiSteps) == 1 {
							wikiStarts++
							if wikiSteps[0].Operation == OperationReconcile {
								if !hold {
									t.Fatalf("%s: receipt recovery starts with no unfinished authorization", name)
								}
							} else if len(taskOnly) != 0 || hold {
								t.Fatalf("%s: the wiki step %+v starts beside the task step %+v that can start", name, wikiSteps[0], taskOnly)
							}
						}
						// With no hold and no active wiki merge step, a task gets the merge step that it gets with no wiki update.
						wikiMergeActive := false
						for _, x := range wc.wiki.Executions {
							wikiMergeActive = wikiMergeActive || x.Operation.MergeStep()
						}
						if !hold && !wikiMergeActive && fmt.Sprint(taskSteps) != fmt.Sprint(taskOnly) {
							t.Fatalf("%s: the task step changed: %+v, with no wiki update %+v", name, taskSteps, taskOnly)
						}
						// Each other operation of a task starts as it starts with no wiki update.
						other := func(ops []RepositoryOperation) string {
							var out []RepositoryOperation
							for _, op := range ops {
								if !op.Subject().Wiki() && !op.Operation.MergeStep() {
									out = append(out, op)
								}
							}
							return fmt.Sprint(out)
						}
						if other(with) != other(without) {
							t.Fatalf("%s: a capture, a cleanup, or another repository operation of a task changed: %v, with no wiki update %v", name, other(with), other(without))
						}
						// The merge candidate step and the merge of a wiki commit need an active Project and no task authorization.
						if len(wikiSteps) == 1 && wikiSteps[0].Operation != OperationReconcile {
							if st.status != "active" || tc.authorization {
								t.Fatalf("%s: the wiki step %+v starts in a held Project", name, wikiSteps[0])
							}
						}
						// Drain starts receipt recovery only. An artifact expiry starts nothing.
						for _, op := range with {
							if drain && op.Operation != OperationReconcile {
								t.Fatalf("%s: drain starts %+v", name, op)
							}
							if st.expiry {
								t.Fatalf("%s: an artifact expiry starts %+v", name, op)
							}
						}
						// A due receipt recovery of the wiki commit starts when the lane is free.
						u := wc.wiki.Update
						if hold && u.ApplyEnded && !u.ApplyStopPending && len(wc.wiki.Retries) == 0 && !mergeStepActive(rec, wc.wiki) && !st.expiry {
							if len(wikiSteps) != 1 || wikiSteps[0].Operation != OperationReconcile || wikiSteps[0].RunID != u.ApplyRun {
								t.Fatalf("%s: the hold has no progress: %+v", name, with)
							}
						}
						// The Vision state and the attention threshold do not hold a wiki step.
						if st.status == "active" && !st.expiry && !drain && !hold && !mergeStepActive(rec, wc.wiki) && len(taskOnly) == 0 &&
							!tc.authorization && u != nil && u.PrepareRun == "" && u.Candidate && len(wikiSteps) != 1 {
							t.Fatalf("%s: the due merge of the wiki commit does not start: %+v", name, with)
						}
					}
				}
			}
		}
	}
	if wikiStarts == 0 || holds == 0 || leaseHeld == 0 {
		t.Fatalf("The cases prove nothing: %d wiki starts, %d holds, %d lease cases", wikiStarts, holds, leaseHeld)
	}
	t.Logf("%d states, %d with a wiki merge step that starts, %d under the hold, %d with a wiki step beside a task step that waits for an agent run", count, wikiStarts, holds, leaseHeld)
}

func TestTaskStepStartsBeforeTheWikiStep(t *testing.T) {
	rec := Records{Project: "p", ProjectStatus: "active", VisionReady: true, Tasks: []Task{packagedTask(1, nil)}}
	wiki := WikiMergeRecords{Update: &WikiDelivery{RunID: "wwww", Captured: true}}
	got := SelectStarts([]ProjectInput{{Records: rec, Wiki: wiki}}, base, false)
	if len(got) != 1 || got[0].TaskID != 1 || got[0].Operation != OperationPrepare {
		t.Fatalf("The start request selected %+v", got)
	}
}

func TestWikiStepStartsOnlyWithNoTaskMergeStep(t *testing.T) {
	wiki := WikiMergeRecords{Update: &WikiDelivery{RunID: "wwww", Captured: true, Candidate: true}}
	future := base.Add(time.Minute)
	for _, c := range []struct {
		name string
		rec  Records
		want bool
	}{
		{"no task", Records{Project: "p", VisionReady: true}, true},
		{"a merge step of a task that can start", Records{Project: "p", VisionReady: true, Tasks: []Task{packagedTask(1, nil)}}, false},
		{"an active merge step of a task", Records{Project: "p", VisionReady: true,
			Executions: []Execution{execution("x1", OperationObserve, 1, "task/1")}}, false},
		{"a merge step of a task in its retry time", Records{Project: "p", VisionReady: true, Tasks: []Task{packagedTask(1, nil)},
			Retries: []Retry{{Target: "task/1", Operation: OperationPrepare, TaskID: 1, Until: future}}}, true},
		{"an active capture of a task", Records{Project: "p", VisionReady: true,
			Executions: []Execution{execution("x1", OperationCapture, 1, "cccc")}}, true},
	} {
		got := SelectStarts([]ProjectInput{{Records: c.rec, Wiki: wiki}}, base, false)
		_, wikiSteps := mergeSteps(got)
		if (len(wikiSteps) == 1) != c.want {
			t.Fatalf("%s: the start request selected %+v", c.name, got)
		}
		if c.want && (wikiSteps[0].Operation != OperationApply || wikiSteps[0].WikiUpdate != "wwww" || wikiSteps[0].Target() != "wiki/wwww") {
			t.Fatalf("%s: the merge step of the wiki commit is %+v", c.name, wikiSteps[0])
		}
	}
	// A merge step of a task that waits for the agent run of its task stays due, and it does not start.
	rec := Records{Project: "p", VisionReady: true, Tasks: []Task{packagedTask(1, nil)}}
	got := SelectStarts([]ProjectInput{{Records: rec, Lease: Lease{Held: true, SubjectID: pi64(1)}, Wiki: wiki}}, base, false)
	if taskSteps, wikiSteps := mergeSteps(got); len(taskSteps) != 0 || len(wikiSteps) != 1 {
		t.Fatalf("A merge step of a task that waits for its agent run held the wiki step: %+v", got)
	}
}

func TestWikiAuthorizationHoldsEachTaskMergeStep(t *testing.T) {
	past := base.Add(-time.Minute)
	observation := packagedTask(3, func(t *Task) { t.Delivery = Delivery{CandidateID: 7, WaitUntil: &past} })
	recovery := packagedTask(4, func(t *Task) { t.Delivery = Delivery{RecoveryOperation: "prepare", RecoveryRun: "bbbb"} })
	mainMerge := packagedTask(5, func(t *Task) {
		t.Decision = DecisionApproved
		t.Delivery = Delivery{CandidateID: 9, CandidateMerged: true}
	})

	held := WikiMergeRecords{Update: &WikiDelivery{RunID: "wwww", Captured: true, Candidate: true, ApplyRun: "qqqq", ApplyEnded: true}}
	records := func(tasks ...Task) Records {
		return Records{Project: "p", ProjectStatus: "active", VisionReady: true, Tasks: tasks}
	}

	// Each merge step of a task starts with no hold. Under the hold, only receipt recovery of the wiki commit starts.
	for _, c := range []struct {
		name string
		rec  Records
		want string
	}{
		{"the destination observation", records(observation), "observe:3"},
		{"the merge candidate step", records(packagedTask(2, nil)), "prepare:2"},
		{"the prepare recovery", records(recovery), "prepare:4"},
		{"the merge into main", records(mainMerge), "apply:5"},
	} {
		free, _ := mergeSteps(SelectStarts([]ProjectInput{{Records: c.rec}}, base, false))
		if got := starts(free); !reflect.DeepEqual(got, []string{c.want}) {
			t.Fatalf("%s: the start request selected %v with no hold, want %s", c.name, got, c.want)
		}
		taskSteps, wikiSteps := mergeSteps(SelectStarts([]ProjectInput{{Records: c.rec, Wiki: held}}, base, false))
		if len(taskSteps) != 0 || len(wikiSteps) != 1 || wikiSteps[0].Operation != OperationReconcile || wikiSteps[0].RunID != "qqqq" {
			t.Fatalf("%s: the start request selected %+v and %+v under the hold", c.name, taskSteps, wikiSteps)
		}
	}

	rec := records(
		Task{ID: 1, Scope: ScopeTask, Status: StatusSeeded, Project: "p", Delivery: Delivery{CaptureRun: "cccc"}},
		packagedTask(2, nil), observation, recovery)
	rec.Cleanups = []Cleanup{{TaskID: 1, RunID: "eeee"}}
	got := SelectStarts([]ProjectInput{{Records: rec, Wiki: held}}, base, false)
	taskSteps, wikiSteps := mergeSteps(got)
	if len(taskSteps) != 0 || len(wikiSteps) != 1 || wikiSteps[0].Operation != OperationReconcile || wikiSteps[0].RunID != "qqqq" {
		t.Fatalf("The start request selected %+v under the hold", got)
	}
	// The capture and the cleanup of a task start under the hold.
	operations := map[Operation]int{}
	for _, op := range got {
		operations[op.Operation]++
	}
	if operations[OperationCapture] != 1 || operations[OperationCleanup] != 1 {
		t.Fatalf("The start request selected %+v under the hold", got)
	}
	// The hold stays while receipt recovery waits for its retry time.
	retry := held
	retry.Retries = []WikiRetry{{Target: "qqqq", Operation: OperationReconcile, Until: base.Add(time.Minute)}}
	got = SelectStarts([]ProjectInput{{Records: rec, Wiki: retry}}, base, false)
	if taskSteps, wikiSteps = mergeSteps(got); len(taskSteps)+len(wikiSteps) != 0 {
		t.Fatalf("A merge step started in the retry time of receipt recovery: %+v", got)
	}
	if due := DueTime([]ProjectInput{{Records: rec, Wiki: retry}}, base); due == nil || !due.Equal(base.Add(time.Minute)) {
		t.Fatalf("The due time of the hold is %v", due)
	}
	// While the merge of the wiki commit runs, the task records hold no fact
	// of it: a due merge candidate step of a task holds new agent runs.
	merges := records(packagedTask(2, nil), observation, recovery)
	if !AgentRunHold(merges, base) {
		t.Fatal("The due merge candidate step of a task holds no agent run")
	}
	// After that merge ends with no proven result, the task records hold the
	// fact. Each merge step of a task then waits and holds no agent run.
	merges.WikiReceiptRecovery = true
	waiting := []string{}
	for _, c := range Candidates(merges, base) {
		if c.State != CandidateWaiting || c.At != nil {
			t.Fatalf("The merge step %+v does not wait for the receipt recovery of the wiki commit", c)
		}
		waiting = append(waiting, string(c.Operation)+":"+strconv.FormatInt(c.TaskID, 10))
	}
	if want := []string{"prepare:2", "observe:3", "prepare:4"}; !reflect.DeepEqual(waiting, want) {
		t.Fatalf("The merge steps that wait are %v, want %v", waiting, want)
	}
	if AgentRunHold(merges, base) {
		t.Fatal("A merge step of a task holds the agent runs while it waits for the receipt recovery of the wiki commit")
	}
	// The hold ends with the result: the merge step of a task starts.
	for _, ended := range []WikiMergeRecords{
		{},
		{Update: &WikiDelivery{RunID: "wwww", Captured: true, WaitUntil: pt(base.Add(30 * time.Second))}},
	} {
		got = SelectStarts([]ProjectInput{{Records: rec, Wiki: ended}}, base, false)
		if taskSteps, wikiSteps = mergeSteps(got); len(taskSteps) != 1 || len(wikiSteps) != 0 {
			t.Fatalf("The start request selected %+v after the hold", got)
		}
	}
}

// While receipt recovery checks the merge of a wiki commit, the merge
// candidate step of a task waits and holds no agent run. The tick starts the
// agent run of the next task, and a start request starts only receipt
// recovery of the wiki commit.
func TestWikiReceiptRecoveryHoldsNoAgentRun(t *testing.T) {
	rec := recs([]Task{prepareDue(1), tk(2, StatusSeeded)}, nil)
	wiki := WikiMergeRecords{Update: &WikiDelivery{RunID: "wwww", Captured: true, Candidate: true, ApplyRun: "qqqq", ApplyEnded: true}}
	if got := decide(t, rec, freeLock()); got.Idle != IdleRepositoryOperation || !AgentRunHold(rec, base) {
		t.Fatalf("With no fact, the due merge candidate step did not hold the agent runs: %+v", got)
	}

	rec.WikiReceiptRecovery = true
	candidates := Candidates(rec, base)
	if len(candidates) != 1 || candidates[0].Operation != OperationPrepare || candidates[0].TaskID != 1 ||
		candidates[0].State != CandidateWaiting || candidates[0].At != nil {
		t.Fatalf("The merge candidate step does not wait for the receipt recovery of the wiki commit: %+v", candidates)
	}
	if AgentRunHold(rec, base) {
		t.Fatal("The merge candidate step that waits holds the agent runs")
	}
	wantSpawn(t, decide(t, rec, freeLock()), RoleWorker, pi64(2))
	projects := []ProjectInput{{Records: rec, Wiki: wiki}}
	got := SelectStarts(projects, base, false)
	if len(got) != 1 || got[0].Operation != OperationReconcile || got[0].WikiUpdate != "wwww" || got[0].RunID != "qqqq" {
		t.Fatalf("The start request selected %+v, want only the receipt recovery of the wiki commit", got)
	}
	// The step that waits names no time: the start request has no due time for it.
	if due := DueTime(projects, base); due != nil {
		t.Fatalf("The due time is %v, want none", due)
	}
}

// While receipt recovery checks the merge of a wiki commit, the retry time of
// the merge candidate step of a packaged correction still holds the agent
// runs. After the retry time the step waits, the correction holds no Project
// priority, and the tick starts the agent run of the next task.
func TestWikiReceiptRecoveryKeepsTheRetryTimeHold(t *testing.T) {
	corrected := prepareDue(1)
	corrected.Delivery.WorkStarted = true
	rec := recs([]Task{corrected, tk(2, StatusSeeded)}, nil)
	rec.WikiReceiptRecovery = true
	rec.Retries = []Retry{{Target: "task/1", Operation: OperationPrepare, TaskID: 1, Until: base.Add(time.Minute)}}
	if got := decide(t, rec, freeLock()); got.Idle != IdleRepositoryOperation || !AgentRunHold(rec, base) {
		t.Fatalf("In the retry time, the merge candidate step did not hold the agent runs: %+v", got)
	}
	after := base.Add(2 * time.Minute)
	if AgentRunHold(rec, after) {
		t.Fatal("After the retry time, the merge candidate step that waits holds the agent runs")
	}
	wantSpawn(t, Decide(rec, freeLock(), DefaultConfig(), clock(after)), RoleWorker, pi64(2))
}

// An open correction holds no Project priority. A blocked or stalled
// correction releases the other tasks of the Project. While receipt recovery
// checks the merge of a wiki commit, each merge step of a task waits, so a
// packaged correction holds no agent run.
func TestOpenCorrectionHoldsNoProjectPriority(t *testing.T) {
	correction := func(id int64, mods ...tmod) Task {
		task := prepareDue(id, mods...)
		task.Delivery.WorkStarted = true
		return task
	}
	approved := tk(5, StatusPackaged, decided(DecisionApproved, base))
	next := tk(9, StatusSeeded)
	// result names the idle reason, or the role and the subject of the spawn.
	result := func(a Action) string {
		switch {
		case a.Spawn == nil:
			return string(a.Idle)
		case a.Spawn.SubjectID == nil:
			return string(a.Spawn.Role)
		}
		return string(a.Spawn.Role) + ":" + strconv.FormatInt(*a.Spawn.SubjectID, 10)
	}
	for _, c := range []struct {
		name  string
		tasks []Task
		fact  bool
		want  string
	}{
		{"a packaged correction", []Task{correction(1), next}, true, "worker:9"},
		{"a packaged correction and its merge candidate step", []Task{correction(1), next}, false, string(IdleRepositoryOperation)},

		{"an approved task", []Task{approved, next}, true, "priority-task-wait"},
		{"a correction and an approved task", []Task{correction(1), approved, next}, true, "priority-task-wait"},
		{"a blocked correction", []Task{correction(1, blocked()), next}, false, "worker:9"},
		{"a stalled correction", []Task{correction(1, func(t *Task) { t.Stalled = true }), next}, false, "worker:9"},
		{"a correction that needs its Worker run", []Task{tk(5, StatusSeeded, func(t *Task) { t.Delivery = Delivery{WorkStarted: true} }),
			tk(2, StatusSeeded)}, false, "worker:5"},
		{"a higher priority precedes a correction", []Task{tk(1, StatusSeeded, func(t *Task) { t.Delivery = Delivery{WorkStarted: true} }),
			tk(9, StatusVerified, prio(-1))}, false, "packager:9"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := recs(c.tasks, nil)
			rec.WikiReceiptRecovery = c.fact
			if got := result(decide(t, rec, freeLock())); got != c.want {
				t.Fatalf("The tick result is %s, want %s", got, c.want)
			}
		})
	}
}

func TestWikiStepIgnoresVisionAndAttention(t *testing.T) {
	wiki := WikiMergeRecords{Update: &WikiDelivery{RunID: "wwww", Captured: true}}
	rec := Records{Project: "p", ProjectStatus: "active", VisionReady: false, AttentionTaskIDs: []int64{1, 2, 3},
		Tasks: []Task{packagedTask(1, func(t *Task) { t.Blocked = true })}}
	got := SelectStarts([]ProjectInput{{Records: rec, Wiki: wiki}}, base, false)
	if len(got) != 1 || !got[0].Subject().Wiki() || got[0].Operation != OperationPrepare {
		t.Fatalf("The start request selected %+v", got)
	}
}

func TestPausedProjectHoldsTheWikiMerge(t *testing.T) {
	rec := Records{Project: "p", ProjectStatus: "paused", VisionReady: true}
	for _, u := range []WikiDelivery{{RunID: "wwww", Captured: true}, {RunID: "wwww", Captured: true, Candidate: true}} {
		if got := SelectStarts([]ProjectInput{{Records: rec, Wiki: WikiMergeRecords{Update: &u}}}, base, false); len(got) != 0 {
			t.Fatalf("The paused Project started %+v", got)
		}
		active := rec
		active.ProjectStatus = "active"
		if got := SelectStarts([]ProjectInput{{Records: active, Wiki: WikiMergeRecords{Update: &u}}}, base, false); len(got) != 1 {
			t.Fatalf("The active Project started %+v", got)
		}
	}
	capture := WikiMergeRecords{Update: &WikiDelivery{RunID: "wwww", CaptureDue: true}}
	if got := SelectStarts([]ProjectInput{{Records: rec, Wiki: capture}}, base, false); len(got) != 1 || got[0].Operation != OperationCapture {
		t.Fatalf("The paused Project did not start the capture: %+v", got)
	}
	recovery := WikiMergeRecords{Update: &WikiDelivery{RunID: "wwww", Captured: true, Candidate: true, ApplyRun: "qqqq", ApplyEnded: true}}
	if got := SelectStarts([]ProjectInput{{Records: rec, Wiki: recovery}}, base, false); len(got) != 1 || got[0].Operation != OperationReconcile {
		t.Fatalf("The paused Project did not start receipt recovery: %+v", got)
	}
}

func TestMergeStateOfATaskThatWaitsForAWikiMerge(t *testing.T) {
	approved := packagedTask(1, func(t *Task) {
		t.Decision = DecisionApproved
		t.Delivery = Delivery{CandidateID: 7, CandidateMerged: true}
	})
	rec := Records{Project: "p", ProjectStatus: "active", VisionReady: true, Tasks: []Task{approved}}
	if got := MergeStateOf(rec, WikiMergeRecords{}, 1, base); got.Kind != MergeStateMerge {
		t.Fatalf("The merge state with no wiki update is %+v", got)
	}
	active := WikiMergeRecords{Update: &WikiDelivery{RunID: "wwww", Captured: true, Candidate: true, ApplyRun: "qqqq"},
		Executions: []WikiExecution{{RunID: "qqqq", Operation: OperationApply, Target: "wiki/wwww"}}}
	if got := MergeStateOf(rec, active, 1, base); got.Kind != MergeStateAfterMergeStep || got.RelatedTask != 0 {
		t.Fatalf("The merge state beside an active merge of the wiki commit is %+v", got)
	}
	prepare := WikiMergeRecords{Update: &WikiDelivery{RunID: "wwww", Captured: true},
		Executions: []WikiExecution{{RunID: "y1", Operation: OperationPrepare, Target: "wiki/wwww"}}}
	if got := MergeStateOf(rec, prepare, 1, base); got.Kind != MergeStateAfterMergeStep || got.RelatedTask != 0 {
		t.Fatalf("The merge state beside an active merge candidate step of the wiki commit is %+v", got)
	}
	held := WikiMergeRecords{Update: &WikiDelivery{RunID: "wwww", Captured: true, Candidate: true, ApplyRun: "qqqq", ApplyEnded: true, Repair: "Remove the file."}}
	if got := MergeStateOf(rec, held, 1, base); got.Kind != MergeStateRecovery || got.RelatedTask != 0 {
		t.Fatalf("The merge state under the hold is %+v", got)
	}
	// The hold precedes the pause, the attention threshold, and the Vision state, as receipt recovery of a task does.
	for name, change := range map[string]func(*Records){
		"paused":    func(r *Records) { r.ProjectStatus = "paused" },
		"attention": func(r *Records) { r.AttentionTaskIDs = []int64{11, 12, 13} },
		"vision":    func(r *Records) { r.VisionReady = false },
	} {
		prevented := rec
		change(&prevented)
		if got := MergeStateOf(prevented, held, 1, base); got.Kind != MergeStateRecovery || got.RelatedTask != 0 {
			t.Fatalf("%s: the merge state under the hold is %+v", name, got)
		}
	}
	// A blocked task keeps its own state.
	blockedTask := approved
	blockedTask.Blocked = true
	held.Executions = nil
	if got := MergeStateOf(Records{Project: "p", ProjectStatus: "active", VisionReady: true, Tasks: []Task{blockedTask}}, held, 1, base); got.Kind != MergeStateBlocked {
		t.Fatalf("The merge state of a blocked task under the hold is %+v", got)
	}
	// A capture or a cleanup of the wiki update changes no merge state.
	capture := WikiMergeRecords{Update: &WikiDelivery{RunID: "wwww", CaptureDue: true},
		Executions: []WikiExecution{{RunID: "y1", Operation: OperationCapture, Target: "wwww"}, {RunID: "y2", Operation: OperationCleanup, Target: "vvvv"}}}
	if got := MergeStateOf(rec, capture, 1, base); got.Kind != MergeStateMerge {
		t.Fatalf("The merge state beside a capture and a cleanup of the wiki update is %+v", got)
	}
}

// Receipt recovery of the wiki commit waits for the stop confirmation of the merge and for the retry time of the merge.
func TestSelectionHoldsWikiReceiptRecovery(t *testing.T) {
	rec := Records{Project: "p", ProjectStatus: "active", VisionReady: true, Tasks: []Task{packagedTask(1, nil)}}
	ended := WikiDelivery{RunID: "wwww", Captured: true, Candidate: true, ApplyRun: "qqqq", ApplyEnded: true}
	stop := ended
	stop.ApplyStopPending = true
	until := base.Add(time.Minute)
	for name, c := range map[string]struct {
		wiki WikiMergeRecords
		due  *time.Time
	}{
		"the stop confirmation of the merge": {wiki: WikiMergeRecords{Update: &stop}},
		"the retry time of the merge": {wiki: WikiMergeRecords{Update: &ended,
			Retries: []WikiRetry{{Target: "wiki/wwww", Operation: OperationApply, Until: until}}}, due: &until},
	} {
		projects := []ProjectInput{{Records: rec, Wiki: c.wiki}}
		got := SelectStarts(projects, base, false)
		if taskSteps, wikiSteps := mergeSteps(got); len(taskSteps)+len(wikiSteps) != 0 {
			t.Fatalf("Receipt recovery did not wait for %s: %+v", name, got)
		}
		if due := DueTime(projects, base); (due == nil) != (c.due == nil) || (due != nil && !due.Equal(*c.due)) {
			t.Fatalf("The due time for %s is %v, want %v", name, due, c.due)
		}
	}
	// With the stop confirmation and after the retry time, receipt recovery starts.
	for _, wiki := range []WikiMergeRecords{
		{Update: &ended},
		{Update: &ended, Retries: []WikiRetry{{Target: "wiki/wwww", Operation: OperationApply, Until: base}}},
	} {
		got := SelectStarts([]ProjectInput{{Records: rec, Wiki: wiki}}, base, false)
		taskSteps, wikiSteps := mergeSteps(got)
		if len(taskSteps) != 0 || len(wikiSteps) != 1 || wikiSteps[0].Operation != OperationReconcile || wikiSteps[0].Target() != "qqqq" {
			t.Fatalf("Receipt recovery did not start: %+v", got)
		}
	}
}

// The prepare recovery waits for its stop confirmation, and the merge candidate step waits for the destination quiet interval.
func TestSelectionHoldsWikiCandidateStep(t *testing.T) {
	rec := Records{Project: "p", ProjectStatus: "active", VisionReady: true}
	future, past := base.Add(time.Minute), base.Add(-time.Minute)
	for _, c := range []struct {
		name string
		u    WikiDelivery
		want bool
		run  string
		due  *time.Time
	}{
		{"the stop confirmation of the merge candidate step", WikiDelivery{RunID: "wwww", Captured: true, PrepareRun: "pppp", PrepareStopPending: true}, false, "", nil},
		{"a confirmed stop of the merge candidate step", WikiDelivery{RunID: "wwww", Captured: true, PrepareRun: "pppp"}, true, "pppp", nil},
		{"the destination quiet interval", WikiDelivery{RunID: "wwww", Captured: true, WaitUntil: &future}, false, "", &future},
		{"an ended destination quiet interval", WikiDelivery{RunID: "wwww", Captured: true, WaitUntil: &past}, true, "", nil},
	} {
		u := c.u
		projects := []ProjectInput{{Records: rec, Wiki: WikiMergeRecords{Update: &u}}}
		got := SelectStarts(projects, base, false)
		_, wikiSteps := mergeSteps(got)
		if (len(wikiSteps) == 1) != c.want {
			t.Fatalf("%s: the start request selected %+v", c.name, got)
		}
		if c.want && (wikiSteps[0].Operation != OperationPrepare || wikiSteps[0].RunID != c.run || wikiSteps[0].Target() != "wiki/wwww") {
			t.Fatalf("%s: the merge candidate step is %+v", c.name, wikiSteps[0])
		}
		if due := DueTime(projects, base); (due == nil) != (c.due == nil) || (due != nil && !due.Equal(*c.due)) {
			t.Fatalf("%s: the due time is %v, want %v", c.name, due, c.due)
		}
	}
}

// An active cleanup of a wiki update counts for MaxCleanups, and the wiki cleanups follow the task cleanups.
func TestWikiCleanupCountsForMaxCleanups(t *testing.T) {
	rec := Records{Project: "p", ProjectStatus: "active", VisionReady: true, Tasks: []Task{packagedTask(1, nil)},
		Cleanups: []Cleanup{{TaskID: 1, RunID: "eeee"}}}
	var active []WikiExecution
	for i := 0; i < MaxCleanups; i++ {
		active = append(active, WikiExecution{RunID: "y" + strconv.Itoa(i), Operation: OperationCleanup, Target: "t" + strconv.Itoa(i)})
	}
	cleanups := func(wiki WikiMergeRecords) []string {
		out := []string{}
		for _, op := range SelectStarts([]ProjectInput{{Records: rec, Wiki: wiki}}, base, false) {
			if op.Operation == OperationCleanup {
				out = append(out, op.RunID)
			}
		}
		return out
	}
	if got := cleanups(WikiMergeRecords{Executions: active}); len(got) != 0 {
		t.Fatalf("The cleanups %v started above the limit", got)
	}
	if got := cleanups(WikiMergeRecords{Executions: active[:MaxCleanups-1]}); !reflect.DeepEqual(got, []string{"eeee"}) {
		t.Fatalf("The cleanups %v started with one free place", got)
	}
	due := WikiMergeRecords{Cleanups: []WikiCleanup{{WikiUpdate: "vvvv", RunID: "vvvv"}, {WikiUpdate: "vvvv", RunID: "uuuu"}}}
	if got := cleanups(due); !reflect.DeepEqual(got, []string{"eeee", "vvvv", "uuuu"}) {
		t.Fatalf("The cleanup queue is %v", got)
	}
	due.Executions = active[:MaxCleanups-2]
	if got := cleanups(due); !reflect.DeepEqual(got, []string{"eeee", "vvvv"}) {
		t.Fatalf("The cleanup queue beside two active cleanups of a wiki update is %v", got)
	}
}
