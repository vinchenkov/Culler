package dispatch

import (
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"testing"
	"time"
)

// genState varies the current control inputs, including records that require recovery.
func genState(r *rand.Rand) (Records, Lease) {
	n := r.Intn(24)
	var tasks []Task
	statuses := []Status{StatusProposed, StatusSeeded, StatusWorked, StatusVerified, StatusPackaged}

	for i := 0; i < n; i++ {
		id := int64(i + 1)
		task := Task{
			ID:                  id,
			Title:               fmt.Sprintf("task-%d", id),
			Scope:               ScopeTask,
			Priority:            r.Intn(5) - 1,
			CreatedAt:           base.Add(time.Duration(r.Intn(6)-3) * time.Minute),
			Status:              statuses[r.Intn(len(statuses))],
			DispatchRetries:     1 + r.Intn(3),
			RequirementsVersion: int64(1 + r.Intn(4)),
			Project:             "ws",
			Blocked:             r.Intn(6) == 0,
			ExecutionHeld:       r.Intn(8) == 0,
		}
		if r.Intn(8) == 0 {
			task.Stalled = true
			task.DispatchRetries = 0
		}
		switch r.Intn(8) {
		case 0:
			task.Status = StatusProposed
			task.Decision = DecisionRejected
			task.DecidedAt = pt(base.Add(time.Duration(-r.Intn(3)) * time.Minute))
			task.Archived = true
		case 1:
			task.Decision = DecisionCancelled
			task.DecidedAt = pt(base.Add(time.Duration(-r.Intn(3)) * time.Minute))
			task.Archived = true
		case 2:
			task.Status = StatusPackaged
			task.Decision = DecisionApproved
			task.DecidedAt = pt(base.Add(time.Duration(-r.Intn(3)) * time.Minute))
			task.Archived = r.Intn(2) == 0
		}
		if task.Status == StatusWorked || task.Status == StatusVerified || task.Status == StatusPackaged {
			task.Delivery.VersionID = id
		}
		if task.Status == StatusPackaged {
			if r.Intn(3) != 0 {
				task.Delivery.CandidateID = id
				task.Delivery.CandidateMerged = r.Intn(3) != 0
			}
			task.Delivery.ReviewPending = task.Decision == DecisionNone
			if r.Intn(3) == 0 {
				task.Delivery.WaitUntil = pt(base.Add(time.Duration(r.Intn(7)-3) * time.Minute))
			}
			if r.Intn(8) == 0 {
				task.Delivery.RecoveryRun = fmt.Sprintf("merge-%d", id)
				task.Delivery.RecoveryOperation = "prepare"
				if task.Decision == DecisionApproved && task.Delivery.CandidateMerged {
					task.Delivery.RecoveryOperation = "apply"
					task.Delivery.ApplyPending = true
				}
				task.Delivery.RecoveryStopPending = r.Intn(3) == 0
			}
		}
		if task.Status == StatusSeeded {
			task.Delivery.CorrectionOpen = r.Intn(3) == 0
		}
		tasks = append(tasks, task)
	}

	// Some evaluations contain unfinished capture or cleanup work.
	var cleanups []Cleanup
	for i := range tasks {
		switch r.Intn(10) {
		case 0:
			tasks[i].Delivery.CaptureRun = fmt.Sprintf("producer-%d", tasks[i].ID)
		case 1:
			cleanups = append(cleanups, Cleanup{TaskID: tasks[i].ID, RunID: fmt.Sprintf("repository-%d", tasks[i].ID)})
		}
	}
	executions, retries := genExecutions(r, tasks, cleanups)

	var packets []Packet
	for _, task := range tasks {
		if task.Status != StatusPackaged && r.Intn(3) != 0 {
			continue
		}
		if r.Intn(2) == 0 {
			continue
		}
		packets = append(packets, Packet{
			TaskID:     task.ID,
			CreatedAt:  task.CreatedAt.Add(time.Duration(r.Intn(3)) * time.Minute),
			Archived:   task.Archived,
			Superseded: r.Intn(3) == 0,
		})
	}

	rec := Records{Project: "ws", VisionReady: true, Tasks: tasks, Packets: packets,
		Executions: executions, Retries: retries, Cleanups: cleanups, ExpiryActive: r.Intn(10) == 0}
	if r.Intn(8) == 0 {
		rec.ProjectStatus = []string{"paused", "archived"}[r.Intn(2)]
	}
	for i := range rec.Tasks {
		rec.Tasks[i].ProjectStatus = rec.ProjectStatus
		if rec.Tasks[i].Blocked || rec.Tasks[i].Stalled {
			rec.AttentionTaskIDs = append(rec.AttentionTaskIDs, rec.Tasks[i].ID)
		}
	}
	if r.Intn(8) == 0 {
		rec.VisionReady = false
	}
	if r.Intn(3) == 0 {
		rec.InfrastructureFailureCount = 1 + r.Intn(3)
		if rec.InfrastructureFailureCount < 3 {
			rec.InfrastructureNextRetryAt = base.Add(time.Duration(r.Intn(7)-3) * time.Minute).Format(time.RFC3339)
		}
	}

	lease := Lease{}
	if r.Intn(3) == 0 {
		lease = Lease{
			Held:           true,
			RunID:          "run-f",
			Owner:          []string{"worker", "verifier", "packager", "strategist", "arbiter"}[r.Intn(5)],
			AcquiredUptime: up(time.Duration(-r.Intn(240)) * time.Minute),
			DeadlineUptime: up(time.Duration(r.Intn(240)-120) * time.Minute),
		}
		if len(tasks) > 0 && r.Intn(4) != 0 {
			lease.SubjectID = pi64(tasks[r.Intn(len(tasks))].ID)
		}
		if r.Intn(3) != 0 {
			lease.HeartbeatUptime = pd(up(time.Duration(-r.Intn(200)) * time.Minute))
		}
		if r.Intn(5) == 0 {
			lease.FinalOutcome = "infra-failed"
		}
	}
	return rec, lease
}

// genExecutions varies the active executions and the retry times of the
// operation targets. A Project has a maximum of one active merge step, and
// each target has a maximum of one active execution.
func genExecutions(r *rand.Rand, tasks []Task, cleanups []Cleanup) ([]Execution, []Retry) {
	var executions []Execution
	var retries []Retry
	active := map[string]bool{}
	mergeActive := false
	add := func(task int64, op Operation, target string) {
		if r.Intn(4) == 0 {
			retries = append(retries, Retry{Target: target, Operation: op, TaskID: task, Until: base.Add(time.Duration(r.Intn(5)-2) * time.Minute)})
			return
		}
		if r.Intn(4) != 0 || active[target] || (op.MergeStep() && mergeActive) {
			return
		}
		active[target] = true
		mergeActive = mergeActive || op.MergeStep()
		executions = append(executions, Execution{RunID: fmt.Sprintf("execution-%d", len(executions)+1), Operation: op, TaskID: task, Target: target})
	}
	for _, c := range cleanups {
		add(c.TaskID, OperationCleanup, c.RunID)
	}
	for _, t := range tasks {
		d := t.Delivery
		task := taskTarget(t.ID)
		switch {
		case d.CaptureRun != "":
			add(t.ID, OperationCapture, d.CaptureRun)
		case d.RecoveryOperation == "apply":
			add(t.ID, OperationReconcile, d.RecoveryRun)
			add(t.ID, OperationApply, task)
		case d.RecoveryOperation == "prepare":
			add(t.ID, OperationPrepare, task)
		case d.WaitUntil != nil:
			add(t.ID, OperationObserve, task)
		case t.Status == StatusPackaged && d.CandidateID == 0:
			add(t.ID, OperationPrepare, task)
		case t.Status == StatusPackaged && t.Decision == DecisionApproved && d.CandidateMerged:
			add(t.ID, OperationApply, task)
		}
	}
	return executions, retries
}

func propertyConfigAndClock(r *rand.Rand, lease Lease) (Config, Clock) {
	cfg := DefaultConfig()
	cfg.ReviewWIPCap = 1 + r.Intn(5)
	cfg.TimeoutMinutes = 1 + r.Intn(90)
	cfg.GraceMinutes = r.Intn(20)
	cfg.SpawnGraceS = 15 + r.Intn(90)
	clk := clock(base.Add(time.Duration(r.Intn(7)-3) * time.Minute))
	if r.Intn(2) == 0 {
		clk.HostLiveness = &HostLiveness{
			RunID:          lease.RunID,
			Present:        r.Intn(2) == 0,
			LaunchAgeS:     r.Intn(180),
			LaunchAgeKnown: r.Intn(4) != 0,
		}
		if r.Intn(4) == 0 {
			clk.HostLiveness.RunID = "older-run"
		}
	}
	return cfg, clk
}

func propertyCopyPointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func deepCopyRecords(rec Records) Records {
	cp := rec
	cp.Tasks = slices.Clone(rec.Tasks)
	cp.Packets = slices.Clone(rec.Packets)
	cp.AttentionTaskIDs = slices.Clone(rec.AttentionTaskIDs)
	cp.Executions = slices.Clone(rec.Executions)
	cp.Retries = slices.Clone(rec.Retries)
	cp.Cleanups = slices.Clone(rec.Cleanups)
	for i := range cp.Tasks {
		cp.Tasks[i].DecidedAt = propertyCopyPointer(rec.Tasks[i].DecidedAt)
		cp.Tasks[i].Delivery.WaitUntil = propertyCopyPointer(rec.Tasks[i].Delivery.WaitUntil)
	}
	return cp
}

func propertyCopyLease(lease Lease) Lease {
	lease.SubjectID = propertyCopyPointer(lease.SubjectID)
	lease.HeartbeatUptime = propertyCopyPointer(lease.HeartbeatUptime)
	return lease
}

func propertyCopyClock(clk Clock) Clock {
	clk.HostLiveness = propertyCopyPointer(clk.HostLiveness)
	return clk
}

func TestProperty_ExactlyZeroOrOneAction_DeterministicAndPure(t *testing.T) {
	r := rand.New(rand.NewSource(6))
	for i := 0; i < 5000; i++ {
		rec, lease := genState(r)
		cfg, clk := propertyConfigAndClock(r, lease)
		recBefore, leaseBefore, clkBefore := deepCopyRecords(rec), propertyCopyLease(lease), propertyCopyClock(clk)
		recAgain, leaseAgain, clkAgain := deepCopyRecords(rec), propertyCopyLease(lease), propertyCopyClock(clk)
		cfgBefore := cfg

		a := Decide(rec, lease, cfg, clk)
		assertWellFormed(t, a)
		b := Decide(recAgain, leaseAgain, cfg, clkAgain)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("Iteration %d: identical inputs selected different actions: %+v and %+v", i, a, b)
		}
		if !reflect.DeepEqual(rec, recBefore) || !reflect.DeepEqual(recAgain, recBefore) {
			t.Fatalf("Iteration %d: Decide changed the records.", i)
		}
		if !reflect.DeepEqual(lease, leaseBefore) || !reflect.DeepEqual(leaseAgain, leaseBefore) {
			t.Fatalf("Iteration %d: Decide changed the lease.", i)
		}
		if !reflect.DeepEqual(clk, clkBefore) || !reflect.DeepEqual(clkAgain, clkBefore) || cfg != cfgBefore {
			t.Fatalf("Iteration %d: Decide changed the clock or configuration.", i)
		}
	}
}

func TestProperty_RecordOrderInvariance(t *testing.T) {
	r := rand.New(rand.NewSource(21))
	for i := 0; i < 5000; i++ {
		rec, lease := genState(r)
		cfg, clk := propertyConfigAndClock(r, lease)
		expected := Decide(deepCopyRecords(rec), propertyCopyLease(lease), cfg, propertyCopyClock(clk))
		shuffled := deepCopyRecords(rec)
		r.Shuffle(len(shuffled.Tasks), func(i, j int) { shuffled.Tasks[i], shuffled.Tasks[j] = shuffled.Tasks[j], shuffled.Tasks[i] })
		r.Shuffle(len(shuffled.Packets), func(i, j int) { shuffled.Packets[i], shuffled.Packets[j] = shuffled.Packets[j], shuffled.Packets[i] })
		r.Shuffle(len(shuffled.AttentionTaskIDs), func(i, j int) {
			shuffled.AttentionTaskIDs[i], shuffled.AttentionTaskIDs[j] = shuffled.AttentionTaskIDs[j], shuffled.AttentionTaskIDs[i]
		})
		r.Shuffle(len(shuffled.Executions), func(i, j int) {
			shuffled.Executions[i], shuffled.Executions[j] = shuffled.Executions[j], shuffled.Executions[i]
		})
		r.Shuffle(len(shuffled.Retries), func(i, j int) { shuffled.Retries[i], shuffled.Retries[j] = shuffled.Retries[j], shuffled.Retries[i] })
		if got := Decide(shuffled, propertyCopyLease(lease), cfg, propertyCopyClock(clk)); !reflect.DeepEqual(got, expected) {
			t.Fatalf("Iteration %d: record order changed the action: %+v instead of %+v", i, got, expected)
		}
	}
}

func TestProperty_StrategistRetryClockBoundary(t *testing.T) {
	r := rand.New(rand.NewSource(40))
	for i := 0; i < 1000; i++ {
		deadline := base.Add(time.Duration(r.Intn(90*24*60*60)) * time.Second)
		rec := recs(nil, nil)
		rec.InfrastructureFailureCount = 1 + r.Intn(2)
		rec.InfrastructureNextRetryAt = deadline.Format(time.RFC3339)
		cfg := DefaultConfig()
		for _, offset := range []time.Duration{-40 * 24 * time.Hour, -time.Nanosecond, 0, time.Nanosecond, 40 * 24 * time.Hour} {
			got := Decide(rec, freeLock(), cfg, clock(deadline.Add(offset)))
			assertWellFormed(t, got)
			if offset < 0 {
				if got.Kind != KindIdle || got.Idle != "project-infrastructure-retry-wait" {
					t.Fatalf("Iteration %d: the retry became eligible before its deadline: %+v", i, got)
				}
			} else if got.Kind != KindSpawn || got.Spawn.Role != RoleStrategist || got.Spawn.SubjectID != nil {
				t.Fatalf("Iteration %d: the retry stayed ineligible at or after its deadline: %+v", i, got)
			}
		}
	}
}

func TestProperty_DestinationQuietClockBoundary(t *testing.T) {
	r := rand.New(rand.NewSource(72))
	for i := 0; i < 1000; i++ {
		deadline := base.Add(time.Duration(r.Intn(90*24*60*60)) * time.Second)
		task := tk(int64(1+r.Intn(1000)), StatusPackaged)
		task.Delivery.WaitUntil = pt(deadline)
		rec := recs([]Task{task}, nil)
		for _, offset := range []time.Duration{-40 * 24 * time.Hour, -time.Nanosecond, 0, time.Nanosecond, 40 * 24 * time.Hour} {
			now := deadline.Add(offset)
			starts := SelectStarts([]ProjectInput{{Records: deepCopyRecords(rec)}}, now, false)
			due := DueTime([]ProjectInput{{Records: deepCopyRecords(rec)}}, now)
			if offset < 0 {
				if len(starts) != 0 || due == nil || !due.Equal(deadline) {
					t.Fatalf("Iteration %d: destination observation preceded its deadline: %+v, due %v", i, starts, due)
				}
			} else if len(starts) != 1 || starts[0].Operation != OperationObserve || starts[0].TaskID != task.ID || due != nil {
				t.Fatalf("Iteration %d: destination observation stayed ineligible at or after its deadline: %+v, due %v", i, starts, due)
			}
		}
	}
}

func TestDecideReturnsAgentRunActionsOnly(t *testing.T) {
	r := rand.New(rand.NewSource(91))
	kinds := map[Kind]int{}
	holds := 0
	for i := 0; i < 5000; i++ {
		rec, lease := genState(r)
		cfg, clk := propertyConfigAndClock(r, lease)
		a := Decide(rec, lease, cfg, clk)
		assertWellFormed(t, a)
		kinds[a.Kind]++
		switch {
		case a.Kind == KindSpawn:
			if AgentRunHold(rec, clk.Now) {
				t.Fatalf("Iteration %d: a spawn started while a repository operation held the agent runs: %+v", i, a)
			}
			if a.Spawn.SubjectID != nil && taskExecutionActive(rec, *a.Spawn.SubjectID) {
				t.Fatalf("Iteration %d: a spawn started while its task had an active execution: %+v", i, a)
			}
		case a.Idle == IdleRepositoryOperation:
			holds++
			if !AgentRunHold(rec, clk.Now) && !activeTaskExecution(rec) {
				t.Fatalf("Iteration %d: an operation without a hold stopped the agent runs: %+v", i, rec)
			}
		}
	}
	for _, kind := range []Kind{KindReap, KindSpawn, KindIdle} {
		if kinds[kind] < 100 {
			t.Fatalf("The generator produced %d %s actions.", kinds[kind], kind)
		}
	}
	if holds < 100 {
		t.Fatalf("The generator produced %d repository holds.", holds)
	}
}

func activeTaskExecution(rec Records) bool {
	for _, x := range rec.Executions {
		if x.Operation != OperationCleanup {
			return true
		}
	}
	return false
}

// Each hold of the agent runs makes progress: the start request starts an
// operation, an execution is active, or the holding operation waits for its retry time.
func TestAgentRunHoldHasProgress(t *testing.T) {
	r := rand.New(rand.NewSource(33))
	holds := 0
	for i := 0; i < 5000; i++ {
		rec, _ := genState(r)
		rec.ExpiryActive = false
		now := base.Add(time.Duration(r.Intn(7)-3) * time.Minute)
		if !AgentRunHold(rec, now) {
			continue
		}
		holds++
		if len(rec.Executions) > 0 {
			continue
		}
		started := false
		for _, op := range SelectStarts([]ProjectInput{{Records: rec}}, now, false) {
			if op.Project != rec.Project {
				t.Fatalf("Iteration %d: the start request named Project %q", i, op.Project)
			}
			started = started || op.Operation != OperationCleanup
		}
		if started {
			continue
		}
		retry := false
		for _, c := range Candidates(rec, now) {
			holding := c.Operation == OperationCapture || c.Operation == OperationPrepare
			retry = retry || (holding && c.State == CandidateRetry)
		}
		if !retry {
			t.Fatalf("Iteration %d: the hold has no progress: %+v", i, Candidates(rec, now))
		}
	}
	if holds < 200 {
		t.Fatalf("The generator produced %d holds.", holds)
	}
}
