package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mc/dispatch"
	"mc/domain"
	"mc/routing"
)

// Dispatch is the one caller of dispatch.Decide.
// One call is one TICK: the Collie stage (lease-free, deployment-wide) and
// then one round per Project the tick walks (dispatch.TickOrder), in id
// order. Each round is the composition of the seam's three steps: prepare
// under the flock and one BEGIN IMMEDIATE transaction (lock-doculler branches
// finish there, with their request receipt); attest with both released (host
// file reads never happen under the spine locks); commit under a fresh flock
// and transaction (token verification, re-decide, exactly one consequence —
// a claim-and-spawn or a health refusal). A losing racer stales at commit
// before any effect data: claim stays CAS on the free lease row of that
// Project, and only one commit can match current state.
//
// The result is the tick object {"action":"tick","actions":[...],"idle":[...]}:
// the ordered list of committed effects (the Collie effect first, if any, then
// one effect per Project in id order, then the effects of the wiki rounds) and
// the Projects whose pipeline round committed nothing, each with its idle
// reason.
//
// Dispatch passes no observer of the accepted commit. Each wiki round then
// starts no wiki update, and it still stops a wiki update run.
func Dispatch(db *sql.DB) (any, error) {
	return dispatchWithIdentity(db, defaultDispatchProtocolIdentity, nil)
}

// DispatchWithObserver is the native in-process form used by the release CLI.
// The observer reads the accepted commit of each Project for the wiki rounds.
func DispatchWithObserver(db *sql.DB, releaseBuildID string, controlVersion int, spineSchemaID string, configSchemaVersion int, observe AcceptedCommitObserver) (any, error) {
	return dispatchWithIdentity(db, dispatchProtocolIdentity{
		releaseBuildID: releaseBuildID, controlVersion: controlVersion,
		spineSchemaID: spineSchemaID, configSchemaVersion: configSchemaVersion,
	}, observe)
}

func dispatchWithIdentity(db *sql.DB, identity dispatchProtocolIdentity, observe AcceptedCommitObserver) (any, error) {
	home, err := resolveMCHome()
	if err != nil {
		return nil, err
	}
	uuid, err := readDeploymentMirrorStrict(home)
	if err != nil {
		return nil, err
	}
	// The capability is resolved BEFORE prepare and rechecked immediately after:
	// the lease-free Collie branch finalizes inside the prepare transaction, so
	// there is no later attest step in which to notice that config.toml moved.
	capability := ResolveRuntimeCapability(home)

	// Plan: the Projects this tick walks, read once up front.
	var order []string
	if err := underDispatchLock(db, func(ctx context.Context, q Q) error {
		var e error
		order, e = loadTickOrder(ctx, q)
		return e
	}); err != nil {
		return nil, err
	}

	tick := newDispatchTick()
	// The Collie stage is the first round. A failure before any round completed
	// is the command's failure (today's refusal behaviour); a failure after one
	// is recorded in the tick so the resident still effects what was committed.
	effect, err := dispatchRoundWithIdentity(db, home, uuid, identity, capability, "")
	if err != nil {
		return nil, err
	}
	tick.record("", effect)
	for _, project := range order {
		effect, err := dispatchRoundWithIdentity(db, home, uuid, identity, capability, project)
		if err != nil {
			tick.fail(err)
			break
		}
		tick.record(project, effect)
	}
	dispatchWikiRounds(db, home, uuid, identity, capability, order, observe, tick)
	return tick.object(), nil
}

// dispatchTick assembles the tick object from its rounds' effects.
type dispatchTick struct {
	actions []map[string]any
	idle    []map[string]any
}

func newDispatchTick() *dispatchTick {
	return &dispatchTick{actions: []map[string]any{}, idle: []map[string]any{}}
}

// record files one round's committed effect: an idle result of a pipeline
// round becomes an idle entry (the Collie stage's idle is nothing at all);
// every other effect is an action, in round order.
func (t *dispatchTick) record(project string, effect map[string]any) {
	if effect["action"] == "idle" {
		if project != "" {
			t.idle = append(t.idle, map[string]any{
				"project": project, "reason": effect["reason"],
			})
		}
		return
	}
	t.actions = append(t.actions, effect)
}

// fail appends the failure of a round as an action, in the error envelope
// shape the CLI writes for a refused verb. A failed pipeline round ends the
// loop of the pipeline rounds. A failed wiki round ends only that round. The
// effects of the later rounds follow the envelope.
func (t *dispatchTick) fail(err error) {
	t.actions = append(t.actions, dispatchErrorEnvelope(err))
}

func (t *dispatchTick) object() map[string]any {
	return map[string]any{"action": "tick", "actions": t.actions, "idle": t.idle}
}

// dispatchErrorEnvelope is the structured twin of the CLI's refused-verb
// envelope ({"error":{"code","message"}}), classified the same way: usage,
// the domain error's own code, or the plain domain rejection.
func dispatchErrorEnvelope(err error) map[string]any {
	code := "domain-rejection"
	var usage *UsageError
	var domainErr *DomainError
	if errors.As(err, &usage) {
		code = "usage"
	} else if errors.As(err, &domainErr) && domainErr.Code != "" {
		code = domainErr.Code
	}
	return map[string]any{"error": map[string]any{"code": code, "message": err.Error()}}
}

// dispatchRoundWithIdentity runs ONE round of the tick for project ("" is
// the Collie stage): prepare under the flock and transaction, then — for a
// spawn candidate, then attest and commit. It returns the
// round's committed effect; an idle effect reports the round's idle reason.
func dispatchRoundWithIdentity(db *sql.DB, home, uuid string, identity dispatchProtocolIdentity, capability PrivateRuntimeCapability, project string) (map[string]any, error) {
	requestID, err := newDispatchRequestID()
	if err != nil {
		return nil, err
	}
	var prepared preparedDispatch
	if err := underDispatchLock(db, func(ctx context.Context, q Q) error {
		var e error
		prepared, e = dispatchPrepareWithIdentity(ctx, q, identity, uuid, requestID, capability, project)
		return e
	}); err != nil {
		return nil, err
	}
	if prepared.final != nil {
		if RuntimeCapabilityDrifted(home, prepared.capabilityDigest) {
			return nil, Domainf("dispatch: runtime configuration changed during prepare; abandoning the resolved effect")
		}
		return prepared.final, nil
	}
	attested, err := dispatchAttest(home, prepared)
	if err != nil {
		return nil, err
	}
	attested = dispatchRecheckAttestation(home, prepared, attested)

	if prepared.visionCheck != nil {
		check := CheckProjectVision(filepath.Dir(prepared.visionCheck.Path))
		prepared.visionCheck = &check
	}

	var effect map[string]any
	if err := underDispatchLock(db, func(ctx context.Context, q Q) error {
		var e error
		effect, e = dispatchCommit(ctx, q, prepared, attested)
		return e
	}); err != nil {
		return nil, err
	}
	return effect, nil
}

// underDispatchLock runs fn inside the dispatch process flock and one BEGIN
// IMMEDIATE transaction. Prepare and commit each take their own acquisition:
// prepare releases both before any host or runtime I/O, and commit
// reacquires and redecides — no claim relies on one lock surviving attest.
func underDispatchLock(db *sql.DB, fn func(context.Context, Q) error) error {
	processLock, err := acquireDispatchProcessLock(db)
	if err != nil {
		return err
	}
	defer func() {
		_ = syscall.Flock(int(processLock.Fd()), syscall.LOCK_UN)
		_ = processLock.Close()
	}()
	return inTx(db, fn)
}

// spineSelection is everything one selection pass read and decided for ONE
// Project. Prepare and commit each run their own — the helper computes
// its own truth twice rather than trusting a frame's claim about either.
type spineSelection struct {
	project string
	now     time.Time
	lease   dispatch.Lease
	tun     tunables
	rec     dispatch.Records
	collies []collieCandidateState
	action  dispatch.Action
}

// selectFromSpine evaluates one Project's decision from lock-domain truth
// (the records and the lease of that Project and of no other). `observed`
// is the resident's bounded set of host
// observations (may be nil: the in-process development seam and every
// non-prepare re-decide supply none); only the entry naming this Project's
// lease holder is ever consulted, and an absent one simply leaves the
// decision exactly as it was before this input existed.
func selectFromSpine(ctx context.Context, q Q, observed []PrivatePipelineLiveness, project string, vision ...*ProjectVisionCheck) (spineSelection, error) {
	sel := spineSelection{project: project}
	var err error
	if sel.now, err = spineClock(ctx, q); err != nil {
		return sel, err
	}
	if sel.tun, err = loadSettings(ctx, q); err != nil {
		return sel, err
	}
	if sel.lease, err = loadLease(ctx, q, project); err != nil {
		return sel, err
	}
	if err = domain.ApplyPendingTaskRevisions(ctx, q); err != nil {
		return sel, err
	}
	if sel.rec, err = loadRecords(ctx, q, project); err != nil {
		return sel, err
	}
	var root string
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(s.workspace_root, '') FROM projects w LEFT JOIN sandbox_profiles s ON s.id = w.sandbox_profile WHERE w.id = ?`, project).Scan(&root); err != nil {
		return sel, err
	}
	var check ProjectVisionCheck
	if len(vision) > 0 && vision[0] != nil {
		check = *vision[0]
	} else {
		check = CheckProjectVision(root)
	}
	sel.rec.VisionReady = visionReady(check, root)
	sel.rec.VisionPath = check.Path
	sel.rec.VisionContent = check.Content
	// The launch-generation observation: active Collie state is
	// part of the canonical projection, so a launch bound or superseded
	// between prepare and commit stales the candidate.
	if sel.collies, err = loadCollieProjection(ctx, q); err != nil {
		return sel, err
	}
	cfg := dispatch.Config{
		ReviewWIPCap:   sel.tun.reviewWIPCap, // the substrate trigger is the backstop
		TimeoutMinutes: sel.tun.timeoutMinutes,
		GraceMinutes:   sel.tun.graceMinutes,
		SpawnGraceS:    sel.tun.spawnGraceS,
	}
	// The host observation enters here and nowhere else. Only THIS
	// Project's lease holder's entry crosses into the decision — the rest
	// of the set is the resident's guess at who the holders might be, and a
	// decision that acted on a non-holder's observation would be reaping on
	// hearsay.
	vm, err := domain.ReadVMClock()
	if err != nil {
		return sel, err
	}
	sel.action = dispatch.Decide(sel.rec, sel.lease, cfg, dispatch.Clock{
		Now:          sel.now,
		BootID:       vm.BootID,
		Uptime:       vm.Uptime,
		HostLiveness: hostLivenessForRun(observed, sel.lease.RunID),
	})
	return sel, nil
}

// visionReady reports whether the check found a valid VISION.md at the root of the Project.
func visionReady(check ProjectVisionCheck, root string) bool {
	return check.Ready && check.State == ProjectVisionPresent && check.Path == ProjectVisionPath(root) && strings.TrimSpace(check.Content) != ""
}

// loadTickOrder reads the per-Project facts dispatch.TickOrder ranks: every
// Project record with its status and whether its lease row is held.
func loadTickOrder(ctx context.Context, q Q) ([]string, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT w.id, w.status, l.run_id IS NOT NULL
		FROM projects w JOIN leases l ON l.project = w.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var states []dispatch.ProjectState
	for rows.Next() {
		var st dispatch.ProjectState
		if err := rows.Scan(&st.ID, &st.Status, &st.LeaseHeld); err != nil {
			return nil, err
		}
		states = append(states, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return dispatch.TickOrder(states), nil
}

// acquireDispatchProcessLock takes the `mc.dispatch` flock before any
// durable-state evaluation. The lock file lives beside the spine bytes, so it
// is arbitrated by the same container-runtime kernel. BEGIN IMMEDIATE + the
// lease CAS remain the correctness fence; this process lock prevents multiple
// host invocations from wasting concurrent evaluations.
func acquireDispatchProcessLock(db *sql.DB) (*os.File, error) {
	rows, err := db.Query(`PRAGMA database_list`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	spinePath := ""
	for rows.Next() {
		var seq int
		var name, path string
		if err := rows.Scan(&seq, &name, &path); err != nil {
			return nil, err
		}
		if name == "main" {
			spinePath = path
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if spinePath == "" {
		return nil, Domainf("mc dispatch requires a file-backed main spine for the process flock")
	}
	lockPath := filepath.Join(filepath.Dir(spinePath), "mc.dispatch.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, Domainf("open dispatch process flock %q: %v", lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, Domainf("acquire dispatch process flock %q: %v", lockPath, err)
	}
	return f, nil
}

// tunables are the settings row's stored knobs. Every one is
// deployment-wide.
type tunables struct {
	timeoutMinutes      int
	graceMinutes        int
	heartbeatIntervalS  int
	spawnGraceS         int
	hardDeadlineMinutes int
	reviewWIPCap        int
	collieIdleTimeoutS  int
	wikiUpdateIntervalS int
}

func spineNow(ctx context.Context, q Q) (time.Time, error) {
	var s string
	if err := q.QueryRowContext(ctx, `SELECT datetime('now')`).Scan(&s); err != nil {
		return time.Time{}, err
	}
	return parseSpineTime(s)
}

// loadSettings reads the one settings row.
func loadSettings(ctx context.Context, q Q) (tunables, error) {
	var tun tunables
	err := q.QueryRowContext(ctx, `
		SELECT timeout_minutes, grace_minutes, heartbeat_interval_s,
		       spawn_grace_s, hard_deadline_minutes, review_wip_cap,
		       collie_idle_timeout_s, wiki_update_interval_s
		FROM settings WHERE id = 1`).Scan(
		&tun.timeoutMinutes, &tun.graceMinutes, &tun.heartbeatIntervalS,
		&tun.spawnGraceS, &tun.hardDeadlineMinutes, &tun.reviewWIPCap,
		&tun.collieIdleTimeoutS, &tun.wikiUpdateIntervalS)
	if err == sql.ErrNoRows {
		return tun, Domainf("no settings row (uninitialized spine)")
	}
	return tun, err
}

// loadLease reads one Project's lease row into the dispatch projection. A
// Project with no lease row is a spine fault: the substrate inserts the row
// with the Project record.
func loadLease(ctx context.Context, q Q, project string) (dispatch.Lease, error) {
	var (
		runID, owner, bootID, finalOutcome           sql.NullString
		subject, acquiredUp, heartbeatUp, deadlineUp sql.NullInt64
	)
	err := q.QueryRowContext(ctx, `
		SELECT l.run_id, l.owner, l.subject, l.boot_id, l.acquired_uptime_ms, l.heartbeat_uptime_ms, l.deadline_uptime_ms,
               CASE WHEN r.ended_at IS NOT NULL THEN r.outcome ELSE NULL END
		FROM leases l LEFT JOIN runs r ON r.id = l.run_id WHERE l.project = ?`, project).Scan(
		&runID, &owner, &subject, &bootID, &acquiredUp, &heartbeatUp, &deadlineUp, &finalOutcome)
	if err == sql.ErrNoRows {
		return dispatch.Lease{}, Domainf("no lease row for Project %q", project)
	}
	if err != nil {
		return dispatch.Lease{}, err
	}
	lease := dispatch.Lease{}
	if runID.Valid {
		lease.Held = true
		lease.RunID = runID.String
		lease.FinalOutcome = finalOutcome.String
		lease.Owner = owner.String
		if subject.Valid {
			s := subject.Int64
			lease.SubjectID = &s
		}
		lease.BootID = bootID.String
		lease.AcquiredUptime = time.Duration(acquiredUp.Int64) * time.Millisecond
		lease.DeadlineUptime = time.Duration(deadlineUp.Int64) * time.Millisecond
		if heartbeatUp.Valid {
			up := time.Duration(heartbeatUp.Int64) * time.Millisecond
			lease.HeartbeatUptime = &up
		}
	}
	return lease, nil
}

// loadRecords reads the control state of one Project.
func loadRecords(ctx context.Context, q Q, project string) (dispatch.Records, error) {
	rec := dispatch.Records{Project: project, BlockedRoles: map[dispatch.Role]bool{}}
	blocked, err := q.QueryContext(ctx, `SELECT c.role FROM customization_conflicts f
 JOIN run_customizations c ON c.run_id=f.run_id JOIN runs r ON r.id=f.run_id
 WHERE c.project=? AND r.subject IS NULL AND f.cleared_at IS NULL`, project)
	if err != nil {
		return rec, err
	}
	for blocked.Next() {
		var role dispatch.Role
		if err := blocked.Scan(&role); err != nil {
			blocked.Close()
			return rec, err
		}
		rec.BlockedRoles[role] = true
	}
	err = blocked.Err()
	blocked.Close()
	if err != nil {
		return rec, err
	}
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(json_group_array(json_object('connection',connection_id,'operation',operation,'profile',profile_id,'revision',revision)), '[]') FROM (SELECT * FROM connection_health ORDER BY connection_id,operation,profile_id)`).Scan(&rec.ConnectionStates); err != nil {
		return rec, err
	}
	if err := q.QueryRowContext(ctx,
		`SELECT status, infrastructure_failure_count, COALESCE(infrastructure_next_retry_at, '') FROM projects WHERE id = ?`, project).Scan(&rec.ProjectStatus, &rec.InfrastructureFailureCount, &rec.InfrastructureNextRetryAt); err != nil {
		if err == sql.ErrNoRows {
			return rec, Domainf("no Project %q", project)
		}
		return rec, err
	}
	environment, err := environmentStateTx(ctx, q, project)
	if err != nil {
		return rec, err
	}
	if rec.UpdateRecovery, err = updateReceiptRecovery(ctx, q, project); err != nil {
		return rec, err
	}
	rec.EnvironmentRevision = environment.SelectedRevisionID
	rec.EnvironmentCondition = environmentCondition(environment)
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM proposal_decisions WHERE project=? AND resolved_at IS NULL)`, project).Scan(&rec.ProposalDecisionPending); err != nil {
		return rec, err
	}
	attention, err := q.QueryContext(ctx, `SELECT id FROM tasks
        WHERE project = ? AND archived = 0 AND (stalled = 1 OR blocked = 1)
        ORDER BY id`, project)
	if err != nil {
		return rec, err
	}
	rec.AttentionTaskIDs = []int64{}
	for attention.Next() {
		var id int64
		if err := attention.Scan(&id); err != nil {
			attention.Close()
			return rec, err
		}
		rec.AttentionTaskIDs = append(rec.AttentionTaskIDs, id)
	}
	err = attention.Err()
	attention.Close()
	if err != nil {
		return rec, err
	}
	rows, err := q.QueryContext(ctx, `
		SELECT tasks.id, tasks.title, tasks.scope, tasks.priority, tasks.created_at, tasks.status,
		       tasks.blocked, tasks.dispatch_retries, tasks.decision, tasks.decided_at,
		       tasks.archived, tasks.project, w.status,COALESCE(tasks.run_retry_at,'')
		FROM tasks JOIN projects w ON w.id = tasks.project
		WHERE tasks.project = ?`, project)
	if err != nil {
		return rec, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			t                   dispatch.Task
			scope, status       string
			createdAt           string
			blocked, archived   int
			decision, decidedAt sql.NullString
		)
		if err := rows.Scan(&t.ID, &t.Title, &scope, &t.Priority,
			&createdAt, &status, &blocked, &t.DispatchRetries,
			&decision, &decidedAt, &archived, &t.Project, &t.ProjectStatus, &t.NextRetryAt); err != nil {
			return rec, err
		}
		t.Scope = dispatch.Scope(scope)
		t.Status = dispatch.Status(status)
		t.Blocked = blocked == 1
		t.Stalled = t.DispatchRetries == 0
		t.Archived = archived == 1
		if t.CreatedAt, err = parseSpineTime(createdAt); err != nil {
			return rec, err
		}
		if decision.Valid {
			t.Decision = dispatch.TaskDecision(decision.String)
		}
		if decidedAt.Valid {
			d, err := parseSpineTime(decidedAt.String)
			if err != nil {
				return rec, err
			}
			t.DecidedAt = &d
		}
		rec.Tasks = append(rec.Tasks, t)
	}
	if err := rows.Err(); err != nil {
		return rec, err
	}

	rows.Close()
	if err := loadRequirementsRecords(ctx, q, &rec); err != nil {
		return rec, err
	}
	if err := loadDeliveryRecords(ctx, q, &rec); err != nil {
		return rec, err
	}

	prows, err := q.QueryContext(ctx, `
		SELECT t.id, p.created_at, t.archived, 1-t.review_slot_held
		FROM tasks t JOIN review_versions p ON p.id=t.current_review_id
		WHERE t.project = ?`, project)
	if err != nil {
		return rec, err
	}
	defer prows.Close()
	for prows.Next() {
		var p dispatch.Packet
		var createdAt string
		var archived int
		if err := prows.Scan(&p.TaskID, &createdAt, &archived, &p.Superseded); err != nil {
			return rec, err
		}
		if p.CreatedAt, err = parseSpineTime(createdAt); err != nil {
			return rec, err
		}
		p.Archived = archived == 1
		rec.Packets = append(rec.Packets, p)
	}
	if err := prows.Err(); err != nil {
		return rec, err
	}

	return rec, nil
}

// applyAction writes the decided action inside the same transaction and
// builds the pinned effect JSON. The write bodies live behind the domain
// aggregates.
func applyAction(ctx context.Context, q Q, now time.Time, a dispatch.Action, tun tunables) (map[string]any, error) {
	switch a.Kind {
	case dispatch.KindIdle:
		return map[string]any{"action": "idle", "reason": string(a.Idle)}, nil

	case dispatch.KindSpawn:
		// Spawn needs native authority (routing.md today, the mount plan
		// next), so it never applies from prepare's transaction: the seam
		// returns a candidate and dispatchCommit applies it.
		return nil, Domainf("dispatch: a spawn consequence applies only through the prepare/attest/commit seam")

	case dispatch.KindReap:
		// The domain preserves final outcomes and charges each run once.
		reap := domain.ReapArgs{
			RunID:    a.Reap.RunID,
			Reason:   string(a.Reap.Reason),
			NoCharge: !a.Reap.ChargeRetries,
		}
		if a.Reap.Reason == dispatch.ReapInterrupted {
			reap.Outcome = "interrupted"
		}
		if _, err := domain.ApplyReap(ctx, q, reap); err != nil {
			return nil, err
		}
		return map[string]any{
			"action":         "reap",
			"project":        a.Reap.Project,
			"run_id":         a.Reap.RunID,
			"reason":         string(a.Reap.Reason),
			"stop_container": a.Reap.StopContainer,
		}, nil

	}
	return nil, Domainf("dispatch: unknown action kind %q", a.Kind)
}

// applySpawn is the claim-and-spawn behind lease.Claim: CAS the free lease
// row of the spawn's Project + INSERT the runs row, one
// transaction. The run id arrives from the prepared candidate — allocated at
// prepare, canonical only now.
func applySpawn(ctx context.Context, q Q, now time.Time, sp *dispatch.Spawn, tun tunables, route routing.Route, runtime PrivateRuntimeBinding, runID string, mountPlan *PrivateDispatchMountPlan, gitIdentityOverride *dispatchGitIdentityOverride, vision ...*ProjectVisionCheck) (map[string]any, error) {
	authorization, connectionRevision, err := spawnConnection(ctx, q, now, runtime)
	if err != nil {
		return connectionWait(ctx, q, sp, runtime.Credential, err)
	}
	// The effect exists before the claim: a lease never waits for a launch
	// effect that the carriers cannot hold. domain.Claim remains the authority
	// that a subject task belongs to the Project of the spawn.
	launch, err := buildSpawnEffect(ctx, q, sp, tun, route, runtime, runID, mountPlan, gitIdentityOverride, vision...)
	if err != nil {
		return nil, err
	}

	if _, err := domain.Claim(ctx, q, now, domain.ClaimArgs{
		RunID:               runID,
		Project:             sp.Project,
		Owner:               launch.owner,
		SubjectID:           sp.SubjectID,
		SessionPath:         launch.sessionPath,
		Binding:             route.HistoricalBinding(),
		PoolSnapshot:        launch.pool,
		HardDeadlineMinutes: tun.hardDeadlineMinutes,
	}); err != nil {
		return nil, err
	}
	if err := recordRunConnection(ctx, q, runID, runtime, authorization, connectionRevision); err != nil {
		return nil, err
	}
	if err := recordRunCustomization(ctx, q, sp, runID); err != nil {
		return nil, err
	}
	if err := endConnectionWait(ctx, q, sp.Project); err != nil {
		return nil, err
	}
	return launch.effect, nil
}

// spawnConnection checks the connection of a run before each write of its
// spawn. It returns the authorization and the revision that the run records.
// A fake harness has no connection. An error states why the connection
// refuses the run.
func spawnConnection(ctx context.Context, q Q, now time.Time, runtime PrivateRuntimeBinding) (string, int64, error) {
	if runtime.Harness == "fake" {
		return "", 0, nil
	}
	authorization, revision, err := ConnectionAdmission(ctx, q, runtime.Credential, runtime.ID, now, runtime.ProfileRevision)
	if err != nil {
		return "", 0, err
	}
	if runtime.AuthorizationID != "" && authorization != runtime.AuthorizationID {
		return "", 0, Domainf("The connection authorization changed.")
	}
	return authorization, revision, nil
}

// recordRunConnection stores the connection identity that the spawn checked on the run.
func recordRunConnection(ctx context.Context, q Q, runID string, runtime PrivateRuntimeBinding, authorization string, revision int64) error {
	_, err := q.ExecContext(ctx, `UPDATE runs SET connection_id=?,authorization_id=?,connection_revision=?,profile_id=?,profile_revision=? WHERE id=?`, runtime.Credential, authorization, revision, runtime.ID, runtime.ProfileRevision, runID)
	return err
}

// spawnLaunch is the launch effect of a run with the values that its run
// record repeats.
type spawnLaunch struct {
	effect      map[string]any
	owner       string
	sessionPath string
	// pool is the identifier set that an Arbiter run records.
	pool []int64
}

// buildSpawnEffect builds the launch effect of a run. It writes nothing. Both
// spawn paths call it before their first write: the spawn of a lease run and
// the start of a wiki update. It returns errEffectOversize for an effect
// above a carrier limit.
func buildSpawnEffect(ctx context.Context, q Q, sp *dispatch.Spawn, tun tunables, route routing.Route, runtime PrivateRuntimeBinding, runID string, mountPlan *PrivateDispatchMountPlan, gitIdentityOverride *dispatchGitIdentityOverride, vision ...*ProjectVisionCheck) (spawnLaunch, error) {
	owner := baseRole(string(sp.Role))
	sessionPath := "sessions/" + runID // MC_HOME-relative
	inputs := briefInputs{}
	if len(vision) > 0 {
		inputs.Vision = vision[0]
	}
	// The role's shipped skills ride beside the brief, not inside it: the
	// runner installs them before the harness starts. Only the set of this
	// run's role is emitted, so no container carries the surface of another role.
	skills, err := RoleSkillSet(sp.Role)
	if err != nil {
		return spawnLaunch{}, err
	}
	if mountPlan != nil && mountPlan.Repository != nil {
		inputs.Repository = &mountPlan.Repository.DispatchRepository
	}
	document, err := buildRunBrief(ctx, q, sp, runID, inputs)
	if err != nil {
		return spawnLaunch{}, err
	}
	brief, err := renderRunBrief(document)
	if err != nil {
		return spawnLaunch{}, err
	}

	// The Arbiter receives the exact proposal identifiers recorded at claim.
	var pool []int64
	switch sp.Role {
	case dispatch.RoleArbiter:
		pool = sp.ProposedPool
		if pool == nil {
			pool = []int64{}
		}

	}

	poolIDs := sp.ProposedPool
	if poolIDs == nil {
		poolIDs = []int64{}
	}
	var subjectID any
	if sp.SubjectID != nil {
		subjectID = *sp.SubjectID
	}
	// Every spawn names its Project, propose included. It is the Project of
	// the decision.
	if sp.Project == "" {
		return spawnLaunch{}, Domainf("dispatch: a spawn names its Project")
	}
	// The operator skill store directory this run reads, MC_HOME-relative.
	// The resident copies the skills it finds there into the container beside
	// the shipped set below. Every run is bound to a Project, so every run
	// has a skill directory.
	skillStoreDir, err := RoleSkillStoreRelDir(sp.Project, sp.Role)
	if err != nil {
		return spawnLaunch{}, Domainf("dispatch: %v", err)
	}
	runtimeValue, err := runtimeBindingEffectValue(runtime)
	if err != nil {
		return spawnLaunch{}, err
	}
	effect := map[string]any{
		"action":               "spawn",
		"run_id":               runID,
		"role":                 string(sp.Role),
		"subject_id":           subjectID,
		"project":              sp.Project,
		"skill_store_dir":      skillStoreDir,
		"pool_ids":             poolIDs,
		"session_path":         sessionPath,
		"heartbeat_interval_s": tun.heartbeatIntervalS,
		"harness":              route.Harness,
		"model_binding":        route.Binding,
		// The attested capability travels with the spawn for the same reason it
		// travels with a Collie wake: the resident owns no route catalog, so the
		// launch is describable only from what config.toml resolved.
		"runtime_binding":       runtimeValue,
		"brief":                 brief,
		"skills":                SkillsEffectValue(skills),
		"mount_plan":            mountPlan,
		"git_identity_override": gitIdentityOverride,
	}
	// The carrier check precedes each write of the spawn. For a lease run it
	// prevents a lease without a launch effect: a watchdog failure before agent
	// start consumes the Project infrastructure allowance.
	if err := effectFitsItsCarriers(effect); err != nil {
		return spawnLaunch{}, err
	}
	return spawnLaunch{effect: effect, owner: owner, sessionPath: sessionPath, pool: pool}, nil
}

// connectionWait records that a connection check stopped the spawn of a
// Project, and returns the idle effect of the round. The first stop on a
// connection writes one activity record. Later stops on it write none.
func connectionWait(ctx context.Context, q Q, sp *dispatch.Spawn, connection string, cause error) (map[string]any, error) {
	var current sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT connection_wait FROM projects WHERE id = ?`, sp.Project).Scan(&current); err != nil {
		return nil, err
	}
	if current.String != connection {
		if err := endConnectionWait(ctx, q, sp.Project); err != nil {
			return nil, err
		}
		detail := "The " + spawnName(sp) + " waits for this connection. " + cause.Error()
		if _, err := q.ExecContext(ctx, `INSERT INTO activity (actor, kind, subject, detail, project)
			VALUES ('dispatch', 'dispatch.wait_started', ?, ?, ?)`, connection, detail, sp.Project); err != nil {
			return nil, err
		}
	}
	var task any
	if sp.SubjectID != nil {
		task = *sp.SubjectID
	}
	if _, err := q.ExecContext(ctx, `UPDATE projects SET connection_wait = ?, connection_wait_task = ? WHERE id = ?`,
		connection, task, sp.Project); err != nil {
		return nil, err
	}
	return map[string]any{"action": "idle", "reason": "connection-unavailable", "project": sp.Project}, nil
}

// endConnectionWait ends the connection wait of a Project and writes one
// activity record. A Project without a connection wait stays unchanged.
func endConnectionWait(ctx context.Context, q Q, project string) error {
	var connection sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT connection_wait FROM projects WHERE id = ?`, project).Scan(&connection); err != nil {
		return err
	}
	if !connection.Valid {
		return nil
	}
	if _, err := q.ExecContext(ctx, `UPDATE projects SET connection_wait = NULL, connection_wait_task = NULL WHERE id = ?`, project); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO activity (actor, kind, subject, detail, project)
		VALUES ('dispatch', 'dispatch.wait_ended', ?, 'The wait for this connection ended.', ?)`, connection.String, project)
	return err
}

// spawnName names the run of a spawn with its role and its task.
func spawnName(sp *dispatch.Spawn) string {
	role := baseRole(string(sp.Role))
	name := strings.ToUpper(role[:1]) + role[1:] + " run"
	if sp.SubjectID != nil {
		name += " for task #" + strconv.FormatInt(*sp.SubjectID, 10)
	}
	return name
}

// errEffectOversize marks the one dispatch failure that is neither the world
// moving nor a candidate's own bad input: the deployment built a decision it
// cannot store or deliver. dispatchCommit turns it into a health refusal.
var errEffectOversize = errors.New("dispatch: the effect exceeds the carriers that must record and deliver it")

// maxReceiptBytes is the activity receipt column's own limit
// (substrate/schema.sql's dispatch_result CHECK). An effect larger than this
// can still be delivered — storableReceipt drops the regenerable bodies and
// marks the receipt — so this bounds what is STORED, not what may be decided.
const maxReceiptBytes = 65536

// maxEffectBytes is the real ceiling on a committed effect: the private frame
// that carries it to the resident (cmd/mc/private_dispatch.go's
// maxPrivateFrameBytes). Beyond this the decision cannot be delivered at all,
// and no amount of trimming the receipt helps.
//
// The two limits are separate because they answer different questions: an
// effect too large to record can still be delivered, so the board degrades
// rather than stops. The wire limit is set by the frame rather than invented.
const maxEffectBytes = 1 << 20

// effectFitsItsCarriers refuses an effect that cannot survive the trip to the
// resident. The Arbiter's brief is the input that reaches this first: it is
// unbounded on purpose (brief.go — "silently truncating the overlap set is the
// same defect one level down"), and it grows with every row that lands and
// every proposal that waits. So the ceiling is real, it is reachable by an
// ordinary healthy board, and the only honest answer is to refuse loudly.
func effectFitsItsCarriers(effect map[string]any) error {
	body, err := json.Marshal(effect)
	if err != nil {
		return err
	}
	if len(body) > maxEffectBytes {
		return errEffectOversize
	}
	// The receipt is allowed to be lean, but only by dropping members that are
	// regenerable. If what remains still cannot be stored then the decision
	// cannot be recorded at all, and the rule is refuse rather than degrade.
	if _, err := storableReceipt(effect); err != nil {
		return errEffectOversize
	}
	return nil
}

// resolveMCHome names the one authoritative MC_HOME for host file reads —
// the env override or the fixed default, always absolute. Routing.md, the
// deployment identity mirror, and the future mount allowlist all resolve
// against it; failure here is an environment usage error, not a refusal.
func resolveMCHome() (string, error) {
	home := os.Getenv("MC_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", Usagef("resolve default MC_HOME for routing.md: %v (run: mc onboard routing)", err)
		}
		home = filepath.Join(userHome, ".culler")
	}
	if !filepath.IsAbs(home) {
		return "", Usagef("MC_HOME must be absolute to resolve routing.md, got %q (run: mc onboard routing)", home)
	}
	return filepath.Clean(home), nil
}

// resolveRoleRoute reads the authoritative route for the lease-free Collie
// registry (pipeline spawns attest routing through the dispatch seam
// instead). Capturing Collie's exact historical binding at start makes later
// resume independent of routing.md drift.
func resolveRoleRoute(role string) (routing.Route, error) {
	home, err := resolveMCHome()
	if err != nil {
		return routing.Route{}, err
	}
	return resolveRoleRouteAtHome(home, role)
}

// resolveRoleRouteAtHome keeps the one config.toml authority read adjacent to
// the routing.md parse. Callers that must carry the resolved route across the
// helper boundary use this rather than letting the helper read a host-owned
// config file under its different uid.
func resolveRoleRouteAtHome(home, role string) (routing.Route, error) {
	route, _, err := resolveRoleRouteWithAuthority(home, role)
	return route, err
}

func resolveRoleRouteWithAuthority(home, role string) (routing.Route, runtimeAuthority, error) {
	path := filepath.Join(home, "routing.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return routing.Route{}, runtimeAuthority{}, Usagef("read routing.md at %q: %v (run: mc onboard routing)", path, err)
	}
	authority, err := loadRuntimeAuthority(home)
	if err != nil {
		return routing.Route{}, runtimeAuthority{}, Domainf("invalid config.toml for routing at %q: %v (run: mc onboard routing)", home, err)
	}
	table, err := routing.Parse(data, authority.registry)
	if err != nil {
		return routing.Route{}, runtimeAuthority{}, Domainf("invalid routing.md at %q: %v (run: mc onboard routing)", path, err)
	}
	resolved, err := table.Resolve(baseRole(role))
	if err != nil {
		return routing.Route{}, runtimeAuthority{}, Domainf("invalid routing.md at %q: %v (run: mc onboard routing)", path, err)
	}
	if _, available := authority.bindings[resolved.Binding]; !available {
		return routing.Route{}, runtimeAuthority{}, Domainf("The selected agent profile requires repair in the dashboard before work can start.")
	}
	return resolved, authority, nil
}
