// Package dispatch selects the next agent run of a Project and the repository
// operations that a start request starts. The caller supplies the clock for
// deadlines, retry times, and the destination quiet interval.
// Selection performs no external reads or writes.
package dispatch

import (
	"fmt"
	"sort"
	"time"
)

// ---------------------------------------------------------------------------
// Record types: the tasks and current reviews of one Project, restricted to
// the fields dispatch reads, plus that Project's lease row.
// ---------------------------------------------------------------------------

// Status is the last stage a task completed.
type Status string

const (
	StatusProposed Status = "proposed"
	StatusSeeded   Status = "seeded"
	StatusWorked   Status = "worked"
	StatusVerified Status = "verified"
	StatusPackaged Status = "packaged"
)

// Rank mirrors the generated stage_rank column: how far into
// the pipeline the row is, for furthest-first dispatch. packaged ranks 0 —
// in the review queue, out of pipeline dispatch. Unknown statuses rank -1 and
// are invisible to every query (defensive; the substrate forbids them).
func (s Status) Rank() int {
	switch s {
	case StatusProposed:
		return 1
	case StatusSeeded:
		return 2
	case StatusWorked:
		return 3
	case StatusVerified:
		return 4
	case StatusPackaged:
		return 0
	}
	return -1
}

// Scope identifies a task.
type Scope string

const (
	ScopeTask Scope = "task"
)

// TaskDecision is the terminal bookkeeping mark, orthogonal to status.
type TaskDecision string

const (
	DecisionNone      TaskDecision = ""
	DecisionApproved  TaskDecision = "approved"
	DecisionRejected  TaskDecision = "rejected"
	DecisionCancelled TaskDecision = "cancelled"
)

// Task contains the task fields that dispatch reads.
type Task struct {
	ExecutionHeld       bool
	RequirementsVersion int64
	Delivery            Delivery
	ID                  int64
	NextRetryAt         string
	Title               string
	Scope               Scope
	Priority            int // -1 = expedited, 0–3 = P0–P3; lower wins
	CreatedAt           time.Time
	Status              Status
	Blocked             bool // The task requires an operator decision.
	Stalled             bool // The task exhausted its task run failure allowance.
	DispatchRetries     int  // The remaining task run failure allowance.
	Decision            TaskDecision
	DecidedAt           *time.Time
	Archived            bool
	Project             string
	ProjectStatus       string // empty in a hand-built fixture means active
}

// Packet is one current review reservation, restricted to the fields
// dispatch reads. Exactly one per task; task_id is both PK and FK.
type Packet struct {
	Superseded bool
	TaskID     int64
	CreatedAt  time.Time
	Archived   bool
}

// Records is the durable state of ONE Project the evaluation recomputes
// from: the rows are the queue. Project names it; ProjectStatus is its
// status ("" in hand-built fixtures means active). Tasks and Packets are
// that Project's rows only.
type Records struct {
	BlockedRoles               map[Role]bool
	ConnectionStates           string
	InfrastructureFailureCount int
	InfrastructureNextRetryAt  string
	ProposalDecisionPending    bool
	Project                    string
	ProjectStatus              string
	EnvironmentCondition       string
	EnvironmentRevision        string
	VisionReady                bool
	VisionPath                 string
	VisionContent              string
	AttentionTaskIDs           []int64 // Distinct tasks that require attention within this Project.
	Tasks                      []Task
	Packets                    []Packet
	Executions                 []Execution // the active executions of the Project
	Retries                    []Retry     // the latest ended execution of each target, when it has a retry time
	Cleanups                   []Cleanup   // in run creation order
	ExpiryActive               bool        // an artifact expiry token exists in the Project
	// WikiReceiptRecovery: the merge of a wiki commit of the Project ended, and
	// its merge authorization is unfinished. Each merge step of a task then waits.
	WikiReceiptRecovery bool
}

// Lease is the Project lease row. Only an agent run holds it. Held=false is the free lease.
// The uptime fields hold the VM uptime of the claim, of the last heartbeat,
// and of the hard deadline. HeartbeatUptime is nil for a stamped-but-never-
// heartbeated slot (the first-heartbeat watchdog's case). SubjectID is nil for
// Strategist runs.
type Lease struct {
	Held            bool
	FinalOutcome    string
	RunID           string
	Owner           string
	SubjectID       *int64
	BootID          string
	AcquiredUptime  time.Duration
	HeartbeatUptime *time.Duration
	DeadlineUptime  time.Duration
}

// Config carries the settings dispatch reads.
type Config struct {
	ReviewWIPCap   int // review queue cap, default 3
	TimeoutMinutes int // lease timeout, default 60
	GraceMinutes   int // lease grace, default 15
	SpawnGraceS    int // first-heartbeat watchdog, default 60
}

// DefaultConfig returns the default settings.
func DefaultConfig() Config {
	return Config{
		ReviewWIPCap:   3,
		TimeoutMinutes: 60,
		GraceMinutes:   15,
		SpawnGraceS:    60,
	}
}

// Clock is the injected clock input — the only channel through which time
// reaches the decision.
type Clock struct {
	// Now is the wall clock of the VM. The VM wall clock stops while the host
	// sleeps and jumps to the host time at wake, so no reap limit reads it.
	Now time.Time
	// BootID and Uptime are the VM clock. The uptime stops while the host
	// sleeps and does not jump at wake, so the reap limits count awake time.
	BootID string
	Uptime time.Duration
	// HostLiveness is the resident's observation of the container of the run
	// that the decision evaluates: the lease holder, or the run of a wiki
	// update. It rides here rather than beside Records because its whole
	// correctness argument is about which clock measured it. The fields
	// below are measured entirely on the HOST clock and are only ever compared
	// with each other, so the reap they enable never crosses the two domains.
	//
	// nil means the host supplied no observation: the in-process development
	// seam, or a resident that could not reach Docker. Absent evidence is not
	// evidence of absence, so nil never reaps.
	HostLiveness *HostLiveness
}

// HostLiveness is the closed, host-measured fact about one pipeline run: the
// holder of the evaluated Project's lease, or the run of its wiki update. The
// resident gathers it before invoking dispatch and it crosses as prepare
// input; `mc` still decides inside the lock domain and still returns the stop
// as effect data, so decide-then-effect is untouched and the reap remains
// the single action of the round that decides it.
type HostLiveness struct {
	// RunID names the run this observation is about. A decision only ever
	// trusts an observation that names the run that it evaluates; anything
	// else is a stale or mismatched snapshot and is ignored.
	RunID string
	// Present is true when a container for RunID was observed. Only a
	// POSITIVE absence counts as absence — the host-side primitive treats
	// Docker's exact not-found as the sole success and throws on every
	// ambiguous inspect — so a container hiding by omitting a label can never
	// pass as gone.
	Present bool
	// LaunchAgeS is how long this run's launch record has existed, in whole
	// seconds of HOST time. It is the anti-race guard's only anchor: a run
	// claimed moments ago has legitimately not started its container yet, and
	// the guard must stay shut for that window measured in real time rather
	// than in the VM's stalled time — which is exactly the window, just after
	// a wake, when the VM clock is furthest behind.
	LaunchAgeS     int
	LaunchAgeKnown bool
}

// ---------------------------------------------------------------------------
// Action — the output of Decide and of DecideWiki. Exactly one action or idle
// per evaluation: each tick commits at most one action of the pipeline round
// and one action of the wiki round per Project, and idle commits zero. Every
// action names the Project it was evaluated for.
// ---------------------------------------------------------------------------

// Kind discriminates the action union.
type Kind string

const (
	// KindIdle leaves the current state unchanged.
	KindIdle Kind = "idle"
	// KindReap records the outcome and requests a container stop. The resident
	// confirms the stop. mc then releases the lease of a lease run, or records
	// the stop of a wiki update run.
	KindReap Kind = "reap"
	// KindSpawn opens one run and returns the spawn effect data. A lease run
	// claims the Project lease. The Scribe run of a wiki update claims none.
	KindSpawn Kind = "spawn"
)

// Role names the pipeline role a spawn opens. The Scribe run of a wiki update
// holds no lease. A run of each other role holds the Project lease.
type Role string

const (
	RoleArbiter  Role = "arbiter"
	RoleWorker   Role = "worker"
	RoleVerifier Role = "verifier"
	RoleScribe   Role = "scribe"
	RolePackager Role = "packager"
	// The wire identifier also keys stored custom instructions and failure records.
	RoleStrategist Role = "strategist(propose)"
)

// IdleReason says which zero-action outcome this is.
type IdleReason string

const (
	IdleAttentionRequired IdleReason = "attention-required"
	IdleLeaseHeld         IdleReason = "lease-held" // step (0): fresh lease → return
	IdleQueueFull         IdleReason = "queue-full"
	// IdleProjectInactive: the Project is paused or archived. Step (0)
	// still reconciled its lease (a held run ends on the ordinary lease
	// rules); nothing else is selected for it.
	IdleProjectInactive IdleReason = "project-inactive"
	IdleVisionRequired  IdleReason = "vision-required"
	// IdleRepositoryOperation: a repository operation must finish before the next agent run.
	IdleRepositoryOperation IdleReason = "repository-operation"
)

// ReapReason names which crash-detection threshold fired.
type ReapReason string

const (
	ReapReported      ReapReason = "reported-outcome"
	ReapSpawnWatchdog ReapReason = "spawn-watchdog" // stamped but never heartbeated past spawn_grace_s
	ReapLeaseTimeout  ReapReason = "lease-timeout"  // heartbeated then silent past timeout+grace
	ReapHardDeadline  ReapReason = "hard-deadline"  // still heartbeating past the deadline uptime
	// ReapVMRestart: the VM restarted after the claim. A VM restart stops every
	// run container, and it is a system condition, so it charges nothing.
	ReapVMRestart ReapReason = "vm-restart"
	// ReapContainerGone: the host positively observed the holder's container
	// absent, past a host-measured launch grace. The only reap reason that
	// reads no lock-domain clock.
	ReapContainerGone ReapReason = "container-gone"
	// ReapInterrupted: the live lease's subject was cancelled out from under
	// its run — `mc task interrupt`, or any other cancellation that lands while
	// the subject holds the lease. The operator verb writes only the
	// cancellation and leaves the lease held; THIS tick turns that durable fact
	// into the stop, because only a dispatch decision may emit an effect.
	// It is the one reason that reads no clock at all, and the one that
	// charges the subject nothing: the work is void by operator decision,
	// which is neither an infra death nor a quality miss.
	ReapInterrupted ReapReason = "interrupted"
)

// chargesAllowance excludes operator cancellation, an earlier terminal result, and a host restart.
func (r ReapReason) chargesAllowance() bool {
	return r != ReapInterrupted && r != ReapReported && r != ReapVMRestart
}

// Reap is the step-(0) action payload.
type Reap struct {
	Project string // the Project of the reaped run
	RunID   string
	Reason  ReapReason
	// The failure classifier selects the task allowance or the Project allowance.
	ChargeRetries bool
	// StopContainer: effect data for the resident — only the host side holds
	// the control socket.
	StopContainer bool
}

// Spawn is the payload of a spawn: a lease run, or the Scribe run of a wiki
// update.
type Spawn struct {
	// Project is the Project of the run — set for every role, propose
	// included. A lease run claims the lease of this Project.
	Project string
	Role    Role
	// SubjectID is nil for the subjectless runs: Strategist proposal creation,
	// Arbiter proposal review, and the Scribe run of a wiki update.
	SubjectID *int64
	// ProposedPool: Arbiter only — the brief snapshots the *entire* proposed
	// pool for contrastive rank-then-cut, never the subject row alone.
	// Ascending id order (deterministic snapshot).
	ProposedPool []int64
	// DedupeTitles carries the Strategist's rejected titles in the dispatch protocol.
	// The brief selects its historical entries from the spine snapshot.
	DedupeTitles []string
}

// Action is the single evaluation result. Exactly one payload is non-nil for
// the non-idle kinds; KindIdle carries only IdleReason.
type Action struct {
	Kind  Kind
	Idle  IdleReason
	Reap  *Reap
	Spawn *Spawn
}

func idle(r IdleReason) Action { return Action{Kind: KindIdle, Idle: r} }

// ---------------------------------------------------------------------------
// Decide — the tick, walked top-down.
// ---------------------------------------------------------------------------

// Decide evaluates ONE Project for one tick against that Project's
// records, its lease, and the clock inputs. It returns a stop of an agent run,
// a spawn of an agent run, or an idle reason. It selects no repository
// operation: a repository operation only holds the agent runs of the Project.
// It is pure: no I/O, no globals, inputs are never mutated, and identical
// inputs always produce the identical Action. Every action names rec.Project.
func Decide(rec Records, lease Lease, cfg Config, clk Clock) Action {
	// (0) Reconcile the lease. Fresh → return; reapable → reap and return.
	// Dispatch runs only when the lease is free.
	if lease.Held {
		if reason, ok := reapable(rec, lease, cfg, clk); ok {
			return reapAction(rec, lease, reason)
		}
		return idle(IdleLeaseHeld)
	}
	if !recordsProjectActive(rec) {
		return idle(IdleProjectInactive)
	}
	if rec.EnvironmentCondition != "" {
		return idle(IdleReason(rec.EnvironmentCondition))
	}
	if rec.InfrastructureFailureCount >= 3 {
		return idle(IdleReason("project-infrastructure-exhausted"))
	}
	if retry, err := time.Parse(time.RFC3339, rec.InfrastructureNextRetryAt); err == nil && clk.Now.Before(retry) {
		return idle(IdleReason("project-infrastructure-retry-wait"))
	}
	rec.Tasks = append([]Task(nil), rec.Tasks...)
	for i := range rec.Tasks {
		if retry, err := time.Parse(time.RFC3339, rec.Tasks[i].NextRetryAt); err == nil && clk.Now.Before(retry) {
			rec.Tasks[i].ExecutionHeld = true
		}
	}

	if !rec.VisionReady {
		return idle(IdleVisionRequired)
	}
	if len(rec.AttentionTaskIDs) >= 3 {
		return idle(IdleAttentionRequired)
	}
	candidates, _ := selectCandidates(rec, clk.Now)
	if agentRunHold(rec, candidates) {
		return idle(IdleRepositoryOperation)
	}
	action := decideAgentRun(rec, cfg, clk)
	if action.Kind == KindSpawn && action.Spawn.SubjectID != nil && taskExecutionActive(rec, *action.Spawn.SubjectID) {
		return idle(IdleRepositoryOperation)
	}
	return action
}

// decideAgentRun selects the next agent run.
func decideAgentRun(rec Records, cfg Config, clk Clock) Action {
	if task, ok := priorityTask(rec); ok {
		return priorityAction(rec, task)
	}
	// The packet of an open correction keeps its review slot, so the
	// correction starts before the review capacity check.
	if action, ok := correctionAction(rec); ok {
		return action
	}

	// (1) Queue occupancy: the unarchived packets of THIS Project (the cap
	// applies per Project; rec holds one Project's packets). A packet whose
	// merge is pending still counts: the slot frees when the merge is
	// applied, never at approve time.
	if reviewOccupancy(rec) >= cfg.ReviewWIPCap {
		// Revisions use their existing packet slots at the queue cap.
		if t, ok := inFlightRevision(rec); ok {
			return spawnFor(rec, t)
		}
		return idle(IdleQueueFull)
	}

	// Queue has room:
	// (3) the single next dispatch.
	if t, ok := nextDispatch(rec); ok {
		return spawnFor(rec, t)
	}

	if rec.ProposalDecisionPending {
		return idle(IdleReason("proposal-decision-pending"))
	}
	if rec.BlockedRoles[RoleStrategist] {
		return idle(IdleReason("role-blocked"))
	}

	// (4) No task is dispatchable. The Strategist proposes work for this Project.
	return Action{Kind: KindSpawn, Spawn: &Spawn{
		Project:      rec.Project,
		Role:         RoleStrategist,
		DedupeTitles: rejectedTitles(rec, 20),
	}}
}

// ProjectState is the per-Project fact TickOrder reads: the record's
// id and status, and whether its lease row is held.
type ProjectState struct {
	ID        string
	Status    string // "active" | "paused" | "archived"
	LeaseHeld bool
}

// TickOrder includes inactive Projects, because a reap can apply there.
// Decide applies each Project's restrictions.
func TickOrder(ws []ProjectState) []string {
	ids := make([]string, 0, len(ws))
	for _, w := range ws {
		ids = append(ids, w.ID)
	}
	sort.Strings(ids)
	return ids
}

// ---------------------------------------------------------------------------
// Step (0): lease reconciliation.
// ---------------------------------------------------------------------------

// reapable evaluates the three crash-detection thresholds. A threshold
// fires at exactly its boundary instant (now >= threshold). The check order
// (watchdog, timeout, hard deadline) fixes the reported reason when several
// hold at once; the reap outcome is identical either way.
func reapable(rec Records, lease Lease, cfg Config, clk Clock) (ReapReason, bool) {
	if lease.FinalOutcome != "" {
		return ReapReported, true
	}
	// Interrupt: checked FIRST and without any threshold. The subject is
	// already cancelled, so there is no run worth waiting out and no reading of
	// the clock that could change the answer — every tick from here would
	// otherwise sit at idle(lease-held) until the lease timed out.
	if subjectCancelled(rec, lease) {
		return ReapInterrupted, true
	}
	if lease.BootID != clk.BootID {
		return ReapVMRestart, true
	}
	// Every limit below counts VM uptime, so host sleep uses none of it.
	up := clk.Uptime
	if lease.HeartbeatUptime == nil {
		if up >= lease.AcquiredUptime+time.Duration(cfg.SpawnGraceS)*time.Second {
			return ReapSpawnWatchdog, true
		}
	} else {
		// Lease timeout: heartbeated then went silent → reaped on the full
		// lease (timeout + grace) measured from the last heartbeat.
		full := time.Duration(cfg.TimeoutMinutes+cfg.GraceMinutes) * time.Minute
		if up >= *lease.HeartbeatUptime+full {
			return ReapLeaseTimeout, true
		}
	}
	// Hard deadline: still heartbeating past the deadline is reaped
	// regardless — liveness is not progress.
	if up >= lease.DeadlineUptime {
		return ReapHardDeadline, true
	}
	// Container gone: the holder's container is positively absent and its
	// launch record is older than the spawn grace, both measured on the HOST
	// clock. Checked last so it can only ADD a reap the three thresholds above
	// would have missed, never rename one they already fire — the ordinary
	// reasons stay the ones an operator reads for an ordinary death.
	if containerGone(lease, cfg, clk) {
		return ReapContainerGone, true
	}
	return "", false
}

// subjectCancelled is the interrupt condition, and it is deliberately derived
// from the cancellation itself rather than from a marker column: the durable
// fact the next tick must act on is exactly "this lease's subject is
// cancelled", and a separate flag could only ever disagree with it. The records
// carry archived rows (loadRecords applies no `archived = 0` filter), so the row
// is still visible here after `mc task interrupt` archived it.
//
// It generalizes past `task interrupt` on purpose: a packet cancel or an
// cancellation that lands on the live subject means the same thing — the
// running container is producing work no one will accept — and wants the same
// stop.
func subjectCancelled(rec Records, lease Lease) bool {
	if lease.SubjectID == nil {
		return false
	}
	t := findTask(rec, *lease.SubjectID)
	return t != nil && t.Decision == DecisionCancelled
}

// containerGone is the whole of the fourth condition, and every way it can be
// unsure is a false: no observation, an observation about some other run, a
// container that was actually seen, or no launch record to age. A wedged lease
// costs one tick to recover once this fires; reaping a live run costs the work
// it was doing, so the asymmetry decides every ambiguous case.
func containerGone(lease Lease, cfg Config, clk Clock) bool {
	live := clk.HostLiveness
	if live == nil || live.RunID == "" || live.RunID != lease.RunID {
		return false
	}
	if live.Present || !live.LaunchAgeKnown {
		return false
	}
	return live.LaunchAgeS >= cfg.SpawnGraceS
}

// reapAction requests a stop and identifies whether the run consumes an allowance.
func reapAction(rec Records, lease Lease, reason ReapReason) Action {
	r := &Reap{Project: rec.Project, RunID: lease.RunID, Reason: reason, StopContainer: true,
		ChargeRetries: reason.chargesAllowance()}
	return Action{Kind: KindReap, Reap: r}
}

// ---------------------------------------------------------------------------
// Advance an active revision at the queue cap.
// ---------------------------------------------------------------------------

// inFlightRevision selects a task with a live packet.
// The stage rank takes precedence over priority and age.
func inFlightRevision(rec Records) (Task, bool) {
	live := make(map[int64]bool, len(rec.Packets)) // task ids with an unarchived packet
	for _, p := range rec.Packets {
		if !p.Archived && !p.Superseded {
			live[p.TaskID] = true
		}
	}
	var cand []Task
	for _, t := range rec.Tasks {
		if !projectActive(t) || t.Archived || t.Blocked || t.Stalled || t.ExecutionHeld || t.Status == StatusPackaged || t.Status.Rank() < 0 {
			continue
		}
		linked := live[t.ID]
		if !linked {
			continue
		}
		if t.Delivery.CaptureRun != "" {
			continue
		}
		cand = append(cand, t)
	}
	if len(cand) == 0 {
		return Task{}, false
	}
	sort.Slice(cand, func(i, j int) bool { return lessInFlight(cand[i], cand[j]) })
	return cand[0], true
}

func lessInFlight(a, b Task) bool {
	if ra, rb := a.Status.Rank(), b.Status.Rank(); ra != rb {
		return ra > rb // furthest-first
	}
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}

// ---------------------------------------------------------------------------
// Step (3): the single next dispatch (queue has room).
// ---------------------------------------------------------------------------

// nextDispatch selects an eligible task by priority, stage, age, and identifier.
func nextDispatch(rec Records) (Task, bool) {
	var cand []Task
	for _, t := range rec.Tasks {
		if !projectActive(t) || t.Archived || t.Blocked || t.Stalled || t.ExecutionHeld || t.Status.Rank() <= 0 || (rec.ProposalDecisionPending || rec.BlockedRoles[RoleArbiter]) && t.Status == StatusProposed {
			continue
		}
		if t.Delivery.CaptureRun != "" {
			continue
		}
		cand = append(cand, t)
	}
	if len(cand) == 0 {
		return Task{}, false
	}
	sort.Slice(cand, func(i, j int) bool { return lessNextDispatch(cand[i], cand[j]) })
	return cand[0], true
}

func lessNextDispatch(a, b Task) bool {
	ea, eb := a.Priority <= -1, b.Priority <= -1
	if ea != eb {
		return ea // expedite lane partitions ahead of everything
	}
	if ra, rb := a.Status.Rank(), b.Status.Rank(); ra != rb {
		return ra > rb // furthest-first
	}
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}

// spawnFor maps the selected row to a role by the (status, scope) table
// (step 3 and step 2a map by the same table) and builds the spawn.
func spawnFor(rec Records, t Task) Action {
	id := t.ID
	sp := &Spawn{Project: rec.Project, SubjectID: &id}
	switch t.Status {
	case StatusProposed:
		sp.Role = RoleArbiter
		sp.SubjectID = nil
		sp.ProposedPool = proposedPool(rec) // batch-wise: the entire pool, never this row alone
	case StatusSeeded:
		sp.Role = RoleWorker
	case StatusWorked:
		sp.Role = RoleVerifier
	case StatusVerified:
		sp.Role = RolePackager
	default:
		// Unreachable: both selecting queries exclude packaged/unknown rows.
		panic(fmt.Sprintf("dispatch: no role for status %q", t.Status))
	}
	return Action{Kind: KindSpawn, Spawn: sp}
}

// ---------------------------------------------------------------------------
// Helpers (pure; operate only on the passed records).
// ---------------------------------------------------------------------------

func findTask(rec Records, id int64) *Task {
	for i := range rec.Tasks {
		if rec.Tasks[i].ID == id {
			t := rec.Tasks[i] // copy; never expose the caller's backing array
			return &t
		}
	}
	return nil
}

func projectActive(t Task) bool {
	// Empty preserves hand-built fixtures; the SQL loader always
	// supplies the authoritative status.
	return t.ProjectStatus == "" || t.ProjectStatus == "active"
}

// recordsProjectActive is the Project-level form of the same predicate:
// the evaluated Project itself is active (or a hand-built fixture with no
// status). The loader always supplies the authoritative status.
func recordsProjectActive(rec Records) bool {
	return rec.ProjectStatus == "" || rec.ProjectStatus == "active"
}

// proposedPool snapshots every open proposal of the Project (ascending id)
// for the Arbiter's contrastive rank-then-cut brief: the whole proposed
// pool of that Project.
func proposedPool(rec Records) []int64 {
	var ids []int64
	for _, t := range rec.Tasks {
		if projectActive(t) && t.Status == StatusProposed && !t.Archived && !t.Blocked && !t.Stalled && !t.ExecutionHeld {
			ids = append(ids, t.ID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// rejectedTitles: the n most recently rejected titles of the Project,
// newest first; equal decided_at breaks the tie by id DESC.
func rejectedTitles(rec Records, n int) []string {
	var rej []Task
	for _, t := range rec.Tasks {
		if t.Decision == DecisionRejected {
			rej = append(rej, t)
		}
	}
	sort.Slice(rej, func(i, j int) bool {
		di, dj := timeOrZero(rej[i].DecidedAt), timeOrZero(rej[j].DecidedAt)
		if !di.Equal(dj) {
			return di.After(dj)
		}
		return rej[i].ID > rej[j].ID
	})
	if len(rej) > n {
		rej = rej[:n]
	}
	titles := make([]string, 0, len(rej))
	for _, t := range rej {
		titles = append(titles, t.Title)
	}
	return titles
}

func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
