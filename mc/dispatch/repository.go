package dispatch

import (
	"sort"
	"strconv"
	"time"
)

// Operation names a repository operation.
type Operation string

const (
	OperationCapture   Operation = "capture"
	OperationCleanup   Operation = "cleanup"
	OperationObserve   Operation = "observe"
	OperationPrepare   Operation = "prepare"
	OperationApply     Operation = "apply"
	OperationReconcile Operation = "reconcile"
)

// MergeStep reports destination observation, the merge candidate step, the merge, and receipt recovery.
func (o Operation) MergeStep() bool {
	switch o {
	case OperationObserve, OperationPrepare, OperationApply, OperationReconcile:
		return true
	}
	return false
}

// Deadline returns 10 minutes for the merge candidate step and the merge, and 5 minutes for the others.
func (o Operation) Deadline() time.Duration {
	if o == OperationPrepare || o == OperationApply {
		return 10 * time.Minute
	}
	return 5 * time.Minute
}

// MaxCleanups is the limit of active cleanups on the host.
const MaxCleanups = 4

// RepositoryOperation is one operation that a start request can start.
// RunID is the producer run of a capture, the repository run of a cleanup,
// the apply run of receipt recovery, and the interrupted run of a prepare recovery.
type RepositoryOperation struct {
	Project    string
	TaskID     int64
	WikiUpdate string
	Operation  Operation
	RunID      string
}

// Subject returns the subject of the operation.
func (op RepositoryOperation) Subject() Subject {
	return Subject{TaskID: op.TaskID, WikiUpdate: op.WikiUpdate}
}

// Target names the record that the operation changes.
func (op RepositoryOperation) Target() string {
	switch op.Operation {
	case OperationCapture, OperationCleanup, OperationReconcile:
		return op.RunID
	}
	return op.Subject().Target()
}

// taskTarget is the target of each operation that changes the task record.
// A run id has 16 hexadecimal characters, so the target forms cannot collide.
func taskTarget(taskID int64) string { return "task/" + strconv.FormatInt(taskID, 10) }

// Subject is the subject of one repository operation: one task or one wiki update.
type Subject struct {
	TaskID     int64  // zero for a wiki update
	WikiUpdate string // the run identifier of the wiki update run; empty for a task
}

// TaskSubject returns the subject of an operation of a task.
func TaskSubject(id int64) Subject { return Subject{TaskID: id} }

// WikiSubject returns the subject of an operation of a wiki update.
func WikiSubject(run string) Subject { return Subject{WikiUpdate: run} }

// Wiki reports whether the subject is a wiki update.
func (s Subject) Wiki() bool { return s.WikiUpdate != "" }

// Valid reports whether the subject names exactly one task or one wiki update.
func (s Subject) Valid() bool { return (s.TaskID != 0) != (s.WikiUpdate != "") }

// Target is the target of each operation that changes the record of the subject.
func (s Subject) Target() string {
	if s.Wiki() {
		return "wiki/" + s.WikiUpdate
	}
	return taskTarget(s.TaskID)
}

// Execution is one active execution.
type Execution struct {
	RunID     string
	Operation Operation
	TaskID    int64
	Target    string
}

// Retry is the retry time of the latest ended execution of one target.
type Retry struct {
	Target    string
	Operation Operation
	TaskID    int64
	Until     time.Time
}

// Cleanup is one temporary repository that meets the cleanup conditions in the records.
type Cleanup struct {
	TaskID int64
	RunID  string
}

// WikiMergeRecords holds the repository facts of the wiki update of one Project.
// WikiRecords is another type: the input of DecideWiki.
type WikiMergeRecords struct {
	Update     *WikiDelivery   // nil with no active wiki update, and while the run of the wiki update is active
	Executions []WikiExecution // each active execution with a wiki subject
	Retries    []WikiRetry     // the retry time of the last ended execution of each wiki target
	Cleanups   []WikiCleanup   // each temporary repository of a wiki update that meets the cleanup conditions
}

// WikiDelivery holds the capture and merge facts of the active wiki update.
type WikiDelivery struct {
	RunID                           string     // the run of the wiki update
	CaptureDue, Captured, Candidate bool       // Candidate: a merged candidate waits for its merge
	PrepareRun                      string     // the run of an unfinished prepare row whose execution ended
	PrepareStopPending              bool       // the owner of PrepareRun did not confirm the stop
	ApplyRun                        string     // the run of the unfinished apply row: the merge authorization
	ApplyEnded, ApplyStopPending    bool       // the execution of ApplyRun ended; its owner did not confirm the stop
	RecoveryEnded                   bool       // one receipt recovery of ApplyRun ended
	Repair                          string     // the repair that the operator checkout needs, or empty
	WaitUntil                       *time.Time // the end of the destination quiet interval
}

// WikiExecution is one active execution of a wiki update.
type WikiExecution struct {
	RunID     string
	Operation Operation
	Target    string
}

// WikiRetry is the retry time of the latest ended execution of one target of a wiki update.
type WikiRetry struct {
	Target    string
	Operation Operation
	Until     time.Time
}

// WikiCleanup is one temporary repository of a wiki update that meets the cleanup conditions.
type WikiCleanup struct{ WikiUpdate, RunID string }

// CandidateState tells whether a candidate can start now.
type CandidateState string

const (
	CandidateActive CandidateState = "active" // an execution of the target is active
	CandidateRetry  CandidateState = "retry"  // the retry time of the target did not end
	// CandidateWaiting: the quiet interval or the stop confirmation did not end,
	// or receipt recovery checks the merge of a wiki commit.
	CandidateWaiting CandidateState = "waiting"
	CandidateDue     CandidateState = "due" // the operation can start now
)

// Candidate is one repository operation that the selection rules admit.
type Candidate struct {
	RepositoryOperation
	State CandidateState
	At    *time.Time // the end of the retry time or of the quiet interval; nil when the wait has no end time
}

// Candidates returns each repository operation that the selection rules admit in one Project.
// The execution limits of SelectStarts do not apply here.
func Candidates(rec Records, now time.Time) []Candidate {
	candidates, _ := selectCandidates(rec, now)
	return candidates
}

// selectCandidates also returns each approved task whose merge step waits for
// an approved task with a lower id, mapped to that lower id.
func selectCandidates(rec Records, now time.Time) ([]Candidate, map[int64]int64) {
	tasks := tasksByID(rec)
	var out []Candidate
	add := func(op RepositoryOperation, waiting bool, at *time.Time) {
		out = append(out, candidateState(rec, op, waiting, at, now))
	}
	operation := func(t Task, op Operation, run string) RepositoryOperation {
		return RepositoryOperation{Project: rec.Project, TaskID: t.ID, Operation: op, RunID: run}
	}

	for _, t := range tasks {
		if t.Delivery.CaptureRun != "" && !t.Stalled {
			add(operation(t, OperationCapture, t.Delivery.CaptureRun), false, nil)
		}
	}
	for _, c := range rec.Cleanups {
		add(RepositoryOperation{Project: rec.Project, TaskID: c.TaskID, Operation: OperationCleanup, RunID: c.RunID}, false, nil)
	}
	recovery := false
	for _, t := range tasks {
		d := t.Delivery
		if d.RecoveryOperation == string(OperationApply) && d.RecoveryRun != "" && !t.Blocked && !t.Stalled {
			add(operation(t, OperationReconcile, d.RecoveryRun), d.RecoveryStopPending, nil)
			recovery = true
			break
		}
	}
	authorizationHold := false
	for _, t := range tasks {
		if t.Delivery.ApplyPending && (t.Blocked || t.Stalled) {
			authorizationHold = true
		}
	}
	if !recordsProjectActive(rec) || !rec.VisionReady || rec.EnvironmentCondition != "" || len(rec.AttentionTaskIDs) >= 3 {
		return out, nil
	}
	if recovery || authorizationHold {
		return out, nil
	}
	focus := mergeFocus(rec)

	type mergeStep struct {
		task     Task
		op       RepositoryOperation
		waiting  bool
		at       *time.Time
		recovery bool
	}
	var steps []mergeStep
	for _, t := range tasks {
		if t.Archived || t.Blocked || t.Stalled || t.ExecutionHeld {
			continue
		}
		d := t.Delivery
		var step mergeStep
		switch {
		case d.RecoveryOperation == string(OperationPrepare) && d.RecoveryRun != "":
			step = mergeStep{task: t, op: operation(t, OperationPrepare, d.RecoveryRun), waiting: d.RecoveryStopPending, recovery: true}
		case d.WaitUntil != nil:
			step = mergeStep{task: t, op: operation(t, OperationObserve, "")}
			if now.Before(*d.WaitUntil) {
				until := *d.WaitUntil
				step.waiting, step.at = true, &until
			}
		case t.Status == StatusPackaged && d.CandidateID == 0:
			step = mergeStep{task: t, op: operation(t, OperationPrepare, "")}
		case t.Status == StatusPackaged && t.Decision == DecisionApproved && d.CandidateMerged:
			step = mergeStep{task: t, op: operation(t, OperationApply, "")}
		default:
			continue
		}
		// While a task holds the Project priority, each other task receives only
		// its merge candidate step. The new merge candidate of an approved task
		// is part of its merge.
		if focus != nil && !focus[t.ID] && !step.recovery &&
			(step.op.Operation != OperationPrepare || t.Decision == DecisionApproved) {
			continue
		}
		steps = append(steps, step)
	}
	// Approved tasks merge in task id order. A prepare recovery only completes
	// an interrupted step, so the order does not hold it.
	var first int64
	for _, s := range steps {
		if s.task.Decision == DecisionApproved && (first == 0 || s.task.ID < first) {
			first = s.task.ID
		}
	}
	removed := map[int64]int64{}
	for _, s := range steps {
		if s.task.Decision == DecisionApproved && s.task.ID != first && !s.recovery {
			removed[s.task.ID] = first
			continue
		}
		// While receipt recovery checks the merge of a wiki commit, the step
		// waits and remains a candidate.
		add(s.op, s.waiting || rec.WikiReceiptRecovery, s.at)
	}
	return out, removed
}

func candidateState(rec Records, op RepositoryOperation, waiting bool, at *time.Time, now time.Time) Candidate {
	c := Candidate{RepositoryOperation: op, State: CandidateDue}
	target := op.Target()
	for _, x := range rec.Executions {
		if x.Target == target {
			c.State = CandidateActive
			return c
		}
	}
	if until := retryUntil(rec, op, now); until != nil {
		c.State, c.At = CandidateRetry, until
		return c
	}
	if waiting {
		c.State, c.At = CandidateWaiting, at
	}
	return c
}

// retryUntil returns the end of the retry time of the operation target.
// Receipt recovery also waits for the retry time of the interrupted merge.
func retryUntil(rec Records, op RepositoryOperation, now time.Time) *time.Time {
	target := op.Target()
	var until *time.Time
	for _, r := range rec.Retries {
		matches := r.Target == target ||
			(op.Operation == OperationReconcile && r.Operation == OperationApply && r.Target == taskTarget(op.TaskID))
		if matches && r.Until.After(now) && (until == nil || r.Until.After(*until)) {
			at := r.Until
			until = &at
		}
	}
	return until
}

// taskAuthorizationUnfinished reports a task of the Project with an unfinished merge authorization.
func taskAuthorizationUnfinished(rec Records) bool {
	for _, t := range rec.Tasks {
		d := t.Delivery
		if d.ApplyPending || (d.RecoveryOperation == string(OperationApply) && d.RecoveryRun != "") {
			return true
		}
	}
	return false
}

// WikiCandidates returns each repository operation of the wiki update that the selection rules admit.
// The capture, each cleanup, and receipt recovery ignore the state of the Project and of its tasks.
// The merge candidate step and the merge need an active Project with no unfinished merge authorization.
func WikiCandidates(rec Records, wiki WikiMergeRecords, now time.Time) []Candidate {
	var out []Candidate
	add := func(op RepositoryOperation, waiting bool, at *time.Time) {
		c := Candidate{RepositoryOperation: op, State: CandidateDue}
		target := op.Target()
		for _, x := range wiki.Executions {
			if x.Target == target {
				c.State = CandidateActive
				out = append(out, c)
				return
			}
		}
		// Receipt recovery also waits for the retry time of the interrupted merge.
		var until *time.Time
		for _, r := range wiki.Retries {
			matches := r.Target == target ||
				(op.Operation == OperationReconcile && r.Operation == OperationApply && r.Target == op.Subject().Target())
			if matches && r.Until.After(now) && (until == nil || r.Until.After(*until)) {
				at := r.Until
				until = &at
			}
		}
		switch {
		case until != nil:
			c.State, c.At = CandidateRetry, until
		case waiting:
			c.State, c.At = CandidateWaiting, at
		}
		out = append(out, c)
	}
	u := wiki.Update
	if u != nil && u.CaptureDue {
		add(RepositoryOperation{Project: rec.Project, WikiUpdate: u.RunID, Operation: OperationCapture, RunID: u.RunID}, false, nil)
	}
	for _, c := range wiki.Cleanups {
		add(RepositoryOperation{Project: rec.Project, WikiUpdate: c.WikiUpdate, Operation: OperationCleanup, RunID: c.RunID}, false, nil)
	}
	if u == nil {
		return out
	}
	if u.ApplyRun != "" && u.ApplyEnded {
		add(RepositoryOperation{Project: rec.Project, WikiUpdate: u.RunID, Operation: OperationReconcile, RunID: u.ApplyRun}, u.ApplyStopPending, nil)
	}
	if !recordsProjectActive(rec) || taskAuthorizationUnfinished(rec) || u.ApplyRun != "" {
		return out
	}
	switch {
	case u.PrepareRun != "":
		add(RepositoryOperation{Project: rec.Project, WikiUpdate: u.RunID, Operation: OperationPrepare, RunID: u.PrepareRun}, u.PrepareStopPending, nil)
	case u.Captured && !u.Candidate:
		op := RepositoryOperation{Project: rec.Project, WikiUpdate: u.RunID, Operation: OperationPrepare}
		if u.WaitUntil != nil && now.Before(*u.WaitUntil) {
			until := *u.WaitUntil
			add(op, true, &until)
		} else {
			add(op, false, nil)
		}
	case u.Candidate:
		add(RepositoryOperation{Project: rec.Project, WikiUpdate: u.RunID, Operation: OperationApply}, false, nil)
	}
	return out
}

// ReviewPublications returns each task whose review packet mc publishes now, in task id order.
func ReviewPublications(rec Records, cfg Config) []int64 {
	capacity := cfg.ReviewWIPCap - reviewOccupancy(rec)
	var ids []int64
	for _, t := range tasksByID(rec) {
		if len(ids) >= capacity {
			break
		}
		if t.Status == StatusPackaged && !t.Archived && !t.Blocked && !t.Stalled &&
			t.Delivery.CandidateMerged && t.Delivery.ReviewPending {
			ids = append(ids, t.ID)
		}
	}
	return ids
}

// AgentRunHold reports whether a repository operation holds the agent runs of the Project.
func AgentRunHold(rec Records, now time.Time) bool {
	return agentRunHold(rec, Candidates(rec, now))
}

func agentRunHold(rec Records, candidates []Candidate) bool {
	for _, c := range candidates {
		if c.State == CandidateWaiting {
			continue
		}
		switch c.Operation {
		case OperationCapture:
			return true
		case OperationPrepare:
			// A revision can return the task of a prepare recovery to seeded.
			// That recovery holds no agent run.
			if t := findTask(rec, c.TaskID); t != nil && t.Status == StatusPackaged {
				return true
			}
		}
	}
	for _, x := range rec.Executions {
		var task Task
		if t := findTask(rec, x.TaskID); t != nil {
			task = *t
		}
		switch x.Operation {
		case OperationCapture:
			if !task.Stalled {
				return true
			}
		case OperationPrepare:
			if task.Status == StatusPackaged && !task.Blocked && !task.Stalled {
				return true
			}
		}
	}
	return false
}

// taskExecutionActive reports an active execution of the task, except cleanup.
func taskExecutionActive(rec Records, taskID int64) bool {
	for _, x := range rec.Executions {
		if x.TaskID == taskID && x.Operation != OperationCleanup {
			return true
		}
	}
	return false
}

// ProjectInput is the state of one Project in a start request.
type ProjectInput struct {
	Records Records
	Lease   Lease
	Wiki    WikiMergeRecords
}

// SelectStarts returns each repository operation that one start request starts, in start order.
// With drain, it returns receipt recovery only.
func SelectStarts(projects []ProjectInput, now time.Time, drain bool) []RepositoryOperation {
	ordered := append([]ProjectInput(nil), projects...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Records.Project < ordered[j].Records.Project })

	var starts []RepositoryOperation
	var cleanups [][]RepositoryOperation
	activeCleanups := 0
	for _, p := range ordered {
		rec := p.Records
		mergeActive := false
		for _, x := range rec.Executions {
			if x.Operation == OperationCleanup {
				activeCleanups++
			}
			if x.Operation.MergeStep() {
				mergeActive = true
			}
		}
		for _, x := range p.Wiki.Executions {
			if x.Operation == OperationCleanup {
				activeCleanups++
			}
			if x.Operation.MergeStep() {
				mergeActive = true
			}
		}
		if rec.ExpiryActive {
			continue
		}
		var subject *int64
		if p.Lease.Held && p.Lease.FinalOutcome == "" {
			subject = p.Lease.SubjectID
		}
		// An unfinished merge authorization of the wiki commit holds each merge step of a task
		// until receipt recovery proves the result of the merge.
		hold := p.Wiki.Update != nil && p.Wiki.Update.ApplyRun != ""
		var captures, merges, wikiMerges, projectCleanups []RepositoryOperation
		for _, c := range Candidates(rec, now) {
			if c.State != CandidateDue || (drain && c.Operation != OperationReconcile) {
				continue
			}
			if subject != nil && c.Operation != OperationCleanup {
				if c.TaskID == *subject {
					continue
				}
			}
			switch c.Operation {
			case OperationCapture:
				captures = append(captures, c.RepositoryOperation)
			case OperationCleanup:
				projectCleanups = append(projectCleanups, c.RepositoryOperation)
			default:
				if !hold {
					merges = append(merges, c.RepositoryOperation)
				}
			}
		}
		for _, c := range WikiCandidates(rec, p.Wiki, now) {
			if c.State != CandidateDue || (drain && c.Operation != OperationReconcile) {
				continue
			}
			switch c.Operation {
			case OperationCapture:
				captures = append(captures, c.RepositoryOperation)
			case OperationCleanup:
				projectCleanups = append(projectCleanups, c.RepositoryOperation)
			default:
				wikiMerges = append(wikiMerges, c.RepositoryOperation)
			}
		}
		starts = append(starts, captures...)
		// The merge step of the wiki commit has the last rank of the merge lane.
		if !mergeActive {
			if step, ok := firstMergeStep(rec, merges); ok {
				starts = append(starts, step)
			} else if len(wikiMerges) > 0 {
				starts = append(starts, wikiMerges[0])
			}
		}
		cleanups = append(cleanups, projectCleanups)
	}

	limit := MaxCleanups - activeCleanups
	for round := 0; limit > 0; round++ {
		more := false
		for _, queue := range cleanups {
			if round < len(queue) && limit > 0 {
				starts = append(starts, queue[round])
				limit--
				more = true
			}
		}
		if !more {
			break
		}
	}
	return starts
}

// firstMergeStep selects the merge step of a Project: receipt recovery, then a
// merge step of the priority task, then a prepare recovery, then the merge
// step of an approved task, then the lowest task id.
func firstMergeStep(rec Records, merges []RepositoryOperation) (RepositoryOperation, bool) {
	if len(merges) == 0 {
		return RepositoryOperation{}, false
	}
	focus := mergeFocus(rec)
	rank := func(op RepositoryOperation) int {
		switch {
		case op.Operation == OperationReconcile:
			return 0
		case focus[op.TaskID]:
			return 1
		case op.Operation == OperationPrepare && op.RunID != "":
			return 2
		}
		if t := findTask(rec, op.TaskID); t != nil && t.Decision == DecisionApproved {
			return 3
		}
		return 4
	}
	best := merges[0]
	for _, op := range merges[1:] {
		if r, b := rank(op), rank(best); r < b || (r == b && op.TaskID < best.TaskID) {
			best = op
		}
	}
	return best, true
}

// DueTime returns the earliest future time at which a candidate in the retry or waiting state becomes due.
func DueTime(projects []ProjectInput, now time.Time) *time.Time {
	var due *time.Time
	for _, p := range projects {
		for _, c := range append(Candidates(p.Records, now), WikiCandidates(p.Records, p.Wiki, now)...) {
			if (c.State != CandidateRetry && c.State != CandidateWaiting) || c.At == nil || !c.At.After(now) {
				continue
			}
			if due == nil || c.At.Before(*due) {
				at := *c.At
				due = &at
			}
		}
	}
	return due
}

// MergeStateKind names why the merge of an approved task is not complete.
type MergeStateKind string

const (
	MergeStateMerge          MergeStateKind = "merge"
	MergeStateNewCandidate   MergeStateKind = "new-candidate"
	MergeStateAfterMergeStep MergeStateKind = "after-merge-step"
	MergeStateAfterTask      MergeStateKind = "after-task"
	MergeStatePaused         MergeStateKind = "paused"
	MergeStateAttention      MergeStateKind = "attention"
	MergeStateVision         MergeStateKind = "vision"
	MergeStateEnvironment    MergeStateKind = "environment"
	MergeStateRecovery       MergeStateKind = "recovery"
	MergeStateRetry          MergeStateKind = "retry"
	MergeStateBlocked        MergeStateKind = "blocked"
)

// MergeState is the merge state of one approved task.
type MergeState struct {
	Kind           MergeStateKind
	RelatedTask    int64 // 0 when no other task holds the merge
	RelatedBlocked bool  // after-task: the related task is blocked or stalled
}

// MergeStateOf states why the merge of an approved task is not complete.
func MergeStateOf(rec Records, wiki WikiMergeRecords, taskID int64, now time.Time) MergeState {
	task := findTask(rec, taskID)
	if task == nil {
		return MergeState{Kind: MergeStateMerge}
	}
	if task.Blocked || task.Stalled {
		return MergeState{Kind: MergeStateBlocked}
	}
	for _, x := range rec.Executions {
		if x.TaskID != taskID {
			continue
		}
		switch x.Operation {
		case OperationApply:
			return MergeState{Kind: MergeStateMerge}
		case OperationObserve, OperationPrepare:
			return MergeState{Kind: MergeStateNewCandidate}
		case OperationReconcile:
			return MergeState{Kind: MergeStateRecovery}
		}
	}
	candidates, removed := selectCandidates(rec, now)
	for _, c := range candidates {
		if c.Operation == OperationReconcile {
			state := MergeState{Kind: MergeStateRecovery}
			if c.TaskID != taskID {
				state.RelatedTask = c.TaskID
			}
			return state
		}
	}
	// The merge of the wiki commit ended with no proven result: each merge step of the Project waits.
	if wiki.Update != nil && wiki.Update.ApplyRun != "" && wiki.Update.ApplyEnded {
		return MergeState{Kind: MergeStateRecovery}
	}
	for _, t := range tasksByID(rec) {
		if t.ID != taskID && t.Delivery.ApplyPending && (t.Blocked || t.Stalled) {
			return MergeState{Kind: MergeStateAfterTask, RelatedTask: t.ID, RelatedBlocked: true}
		}
	}
	switch {
	case !recordsProjectActive(rec):
		return MergeState{Kind: MergeStatePaused}
	case len(rec.AttentionTaskIDs) >= 3:
		return MergeState{Kind: MergeStateAttention}
	case !rec.VisionReady:
		return MergeState{Kind: MergeStateVision}
	case rec.EnvironmentCondition != "":
		return MergeState{Kind: MergeStateEnvironment}
	}
	if p, ok := priorityTask(rec); ok && p.ID != taskID && !mergeFocus(rec)[taskID] {
		return MergeState{Kind: MergeStateAfterTask, RelatedTask: p.ID, RelatedBlocked: p.Blocked || p.Stalled}
	}
	if first, ok := removed[taskID]; ok {
		return MergeState{Kind: MergeStateAfterTask, RelatedTask: first}
	}
	var other *Execution
	for i, x := range rec.Executions {
		if x.TaskID != taskID && x.Operation.MergeStep() && (other == nil || x.TaskID < other.TaskID) {
			other = &rec.Executions[i]
		}
	}
	if other != nil {
		return MergeState{Kind: MergeStateAfterMergeStep, RelatedTask: other.TaskID}
	}
	for _, x := range wiki.Executions {
		if x.Operation.MergeStep() {
			return MergeState{Kind: MergeStateAfterMergeStep}
		}
	}
	target := taskTarget(taskID)
	for _, r := range rec.Retries {
		if r.Target == target && r.Until.After(now) {
			return MergeState{Kind: MergeStateRetry}
		}
	}
	for _, c := range candidates {
		if c.TaskID != taskID {
			continue
		}
		switch c.Operation {
		case OperationApply:
			return MergeState{Kind: MergeStateMerge}
		case OperationObserve, OperationPrepare:
			return MergeState{Kind: MergeStateNewCandidate}
		}
	}
	return MergeState{Kind: MergeStateMerge}
}

// tasksByID returns a copy of the tasks in id order, so that a selection is independent of the record order.
func tasksByID(rec Records) []Task {
	tasks := append([]Task(nil), rec.Tasks...)
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	return tasks
}
