package verbs

// The wiki round. Each tick runs one wiki round for each Project, after the
// pipeline rounds. The round stops a wiki update run, or it starts the wiki
// update that is due. The run of a wiki update is a Scribe run with no task
// and no lease. No step of the round reads or writes a lease, a task, a
// failure allowance, or the connection wait of the Project.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"mc/dispatch"
	"mc/domain"
	"mc/refusal"
	"mc/repository"
	"mc/substrate"
)

// AcceptedCommitObserver reads the accepted commit of a Project on the host:
// the commit of the branch culler in the repository of the Project. An empty
// value states that the host has no observation.
type AcceptedCommitObserver interface {
	// All reads each Project at the same time under one shared time limit.
	All(ctx context.Context, projects []string) map[string]string
	One(ctx context.Context, project string) string
}

// PrivateWikiRound is the round name of a wiki round in a private frame.
const PrivateWikiRound = "wiki"

// The idle reasons that only the commit step of a wiki round gives.
const (
	idleWikiObservationChanged    = "wiki-observation-changed"
	idleWikiConnectionUnavailable = "wiki-connection-unavailable"
)

// canonicalWikiUpdate is one ended wiki update in the canonical projection
// of a wiki round.
type canonicalWikiUpdate struct {
	RunID             string `json:"run_id"`
	StartCommit       string `json:"start_commit"`
	WikiCommit        string `json:"wiki_commit"`
	StartedAt         string `json:"started_at"`
	EndedAt           string `json:"ended_at"`
	Result            string `json:"result"`
	Counted           bool   `json:"counted"`
	DestinationCommit string `json:"destination_commit"`
	MergedCommit      string `json:"merged_commit"`
}

type canonicalWikiTunables struct {
	TimeoutMinutes      int `json:"timeout_minutes"`
	GraceMinutes        int `json:"grace_minutes"`
	HeartbeatIntervalS  int `json:"heartbeat_interval_s"`
	SpawnGraceS         int `json:"spawn_grace_s"`
	HardDeadlineMinutes int `json:"hard_deadline_minutes"`
	WikiUpdateIntervalS int `json:"wiki_update_interval_s"`
}

// canonicalWikiPrepare freezes the facts that select the start of a wiki
// update. It follows the rules of the canonical structs of the dispatch seam:
// a closed struct in declared field order. The key round separates its token
// from the token of a pipeline round.
type canonicalWikiPrepare struct {
	UpdateRecovery       bool                      `json:"update_recovery,omitempty"`
	Version              int                       `json:"version"`
	Round                string                    `json:"round"`
	ReleaseBuildID       string                    `json:"release_build_id"`
	ControlVersion       int                       `json:"gateway_control_version"`
	SpineSchemaID        string                    `json:"spine_schema_id"`
	ConfigSchemaVersion  int                       `json:"config_schema_version"`
	DeploymentUUID       string                    `json:"deployment_uuid"`
	RequestID            string                    `json:"request_id"`
	Project              string                    `json:"project"`
	ProjectStatus        string                    `json:"project_status"`
	EnvironmentRevision  string                    `json:"environment_revision,omitempty"`
	EnvironmentCondition string                    `json:"environment_condition,omitempty"`
	AcceptedCommit       string                    `json:"accepted_commit"`
	MergeMarker          int64                     `json:"merge_marker"`
	LastSucceeded        *canonicalWikiUpdate      `json:"last_succeeded"`
	LastCounted          *canonicalWikiUpdate      `json:"last_counted"`
	Tunables             canonicalWikiTunables     `json:"tunables"`
	MountState           PrivateDispatchMountState `json:"mount_state"`
	Candidate            canonicalCandidate        `json:"candidate"`
}

func (p canonicalWikiPrepare) bytes() ([]byte, error) {
	return json.Marshal(p)
}

func canonicalWikiUpdateOf(row *domain.WikiUpdateRow) *canonicalWikiUpdate {
	if row == nil {
		return nil
	}
	return &canonicalWikiUpdate{
		RunID: row.RunID, StartCommit: row.StartCommit, WikiCommit: row.WikiCommit,
		StartedAt: row.StartedAt, EndedAt: row.EndedAt, Result: row.Result, Counted: row.Counted,
		DestinationCommit: row.DestinationCommit, MergedCommit: row.MergedCommit,
	}
}

// wikiSelection holds the facts that one step of a wiki round read for one
// Project, and the decision that they give.
type wikiSelection struct {
	updateRecovery bool
	project        string
	status         string
	now            time.Time
	tun            tunables
	state          domain.WikiState
	marker         int64
	action         dispatch.Action
	environment    EnvironmentState
}

func buildCanonicalWikiPrepare(identity dispatchProtocolIdentity, uuid, requestID string, sel wikiSelection, mounts PrivateDispatchMountState, cand canonicalCandidate) canonicalWikiPrepare {
	if mounts.Projects == nil {
		mounts.Projects = []PrivateDispatchProject{}
	}
	return canonicalWikiPrepare{
		Version:              dispatchPrepareVersion,
		Round:                PrivateWikiRound,
		ReleaseBuildID:       identity.releaseBuildID,
		ControlVersion:       identity.controlVersion,
		SpineSchemaID:        identity.spineSchemaID,
		ConfigSchemaVersion:  identity.configSchemaVersion,
		DeploymentUUID:       uuid,
		RequestID:            requestID,
		Project:              sel.project,
		ProjectStatus:        sel.status,
		EnvironmentRevision:  sel.environment.SelectedRevisionID,
		EnvironmentCondition: environmentCondition(sel.environment),
		UpdateRecovery:       sel.updateRecovery,
		AcceptedCommit:       sel.state.AcceptedCommit,
		MergeMarker:          sel.marker,
		LastSucceeded:        canonicalWikiUpdateOf(sel.state.LastSucceeded),
		LastCounted:          canonicalWikiUpdateOf(sel.state.LastCounted),
		Tunables: canonicalWikiTunables{
			TimeoutMinutes:      sel.tun.timeoutMinutes,
			GraceMinutes:        sel.tun.graceMinutes,
			HeartbeatIntervalS:  sel.tun.heartbeatIntervalS,
			SpawnGraceS:         sel.tun.spawnGraceS,
			HardDeadlineMinutes: sel.tun.hardDeadlineMinutes,
			WikiUpdateIntervalS: sel.tun.wikiUpdateIntervalS,
		},
		MountState: mounts,
		Candidate:  cand,
	}
}

// enterWikiRound starts a step of a wiki round. It stores the observation
// before each read of the wiki facts, because the token of the round holds
// the stored accepted commit. It returns the status of the Project and the
// accepted commit that the spine held before this step.
func enterWikiRound(ctx context.Context, q Q, project, observation string) (status, stored string, err error) {
	if observation != "" && !repository.ValidCommit(observation) {
		return "", "", Domainf("dispatch: the accepted commit of a wiki round is invalid")
	}
	if err := q.QueryRowContext(ctx, `SELECT status FROM projects WHERE id = ?`, project).Scan(&status); err != nil {
		if err == sql.ErrNoRows {
			return "", "", Domainf("no Project %q", project)
		}
		return "", "", err
	}
	if stored, err = domain.AcceptedCommit(ctx, q, project); err != nil {
		return "", "", err
	}
	if err := domain.RecordAcceptedCommit(ctx, q, project, observation); err != nil {
		return "", "", err
	}
	return status, stored, nil
}

// selectWiki loads the wiki facts of one Project and decides its wiki round.
// observed is the host observation of the run containers. Only the entry of
// the active wiki update run enters the decision.
func selectWiki(ctx context.Context, q Q, project, status, observation string, observed []PrivatePipelineLiveness) (wikiSelection, error) {
	sel := wikiSelection{project: project, status: status}
	var err error
	if sel.now, err = spineClock(ctx, q); err != nil {
		return sel, err
	}
	if sel.tun, err = loadSettings(ctx, q); err != nil {
		return sel, err
	}
	vm, err := domain.ReadVMClock()
	if err != nil {
		return sel, err
	}
	if sel.state, err = domain.WikiStateOf(ctx, q, project); err != nil {
		return sel, err
	}
	if sel.marker, err = domain.WikiMergeMarker(ctx, q, project); err != nil {
		return sel, err
	}
	if sel.environment, err = environmentStateTx(ctx, q, project); err != nil {
		return sel, err
	}
	if sel.updateRecovery, err = updateReceiptRecovery(ctx, q, project); err != nil {
		return sel, err
	}
	rec := dispatch.WikiRecords{
		Project: project, Status: status, AcceptedCommit: observation,
		EnvironmentCondition: environmentCondition(sel.environment),
		UpdateRecovery:       sel.updateRecovery,
		Active:               sel.state.Active.Record(),
		LastSucceeded:        sel.state.LastSucceeded.Record(),
		LastCounted:          sel.state.LastCounted.Record(),
		Unstopped:            sel.state.Unstopped,
	}
	active := ""
	if rec.Active != nil {
		active = rec.Active.RunID
	}
	cfg := dispatch.Config{
		ReviewWIPCap: sel.tun.reviewWIPCap, TimeoutMinutes: sel.tun.timeoutMinutes,
		GraceMinutes: sel.tun.graceMinutes, SpawnGraceS: sel.tun.spawnGraceS,
	}
	sel.action = dispatch.DecideWiki(rec, cfg, time.Duration(sel.tun.wikiUpdateIntervalS)*time.Second, dispatch.Clock{
		Now: sel.now, BootID: vm.BootID, Uptime: vm.Uptime,
		HostLiveness: hostLivenessForRun(observed, active),
	})
	return sel, nil
}

// loadWikiMountState builds the mount state of a wiki update run: the
// Project and the isolated repository of the run at the start commit.
func loadWikiMountState(ctx context.Context, q Q, project, runID, startCommit string) (PrivateDispatchMountState, error) {
	state := PrivateDispatchMountState{SelectedProject: project, Projects: []PrivateDispatchProject{}}
	var limit int64
	if err := q.QueryRowContext(ctx, `SELECT max_temporary_bytes FROM repository_limits WHERE project = ?`, project).Scan(&limit); err != nil {
		return state, err
	}
	state.SubjectRepository = &substrate.DispatchRepository{
		DestinationBranch: "culler", InputCommit: startCommit, MaxTemporaryBytes: limit, WikiUpdate: runID,
	}
	rows, err := substrate.LoadDispatchProjectProjection(ctx, q)
	if err != nil {
		return state, err
	}
	state.Projects = rows
	return state, nil
}

// wikiCandidate reports whether the candidate is the Scribe run of a wiki
// update: the repository plan of its mount state names the run.
func wikiCandidate(cand *preparedCandidate) bool {
	if cand == nil || cand.spawn == nil || cand.spawn.Role != dispatch.RoleScribe || cand.spawn.SubjectID != nil {
		return false
	}
	return wikiMountState(cand.mountState) && cand.mountState.SubjectRepository.WikiUpdate == cand.runID
}

// wikiMountState reports whether the mount state is the state of a wiki update run.
func wikiMountState(state PrivateDispatchMountState) bool {
	return state.SubjectRepository != nil && state.SubjectRepository.WikiUpdate != ""
}

// wikiStopEnd gives the end of a wiki update run for the reason of its stop.
// It returns false for a run that reported its outcome: that stop only
// removes the container.
func wikiStopEnd(reason dispatch.ReapReason) (domain.WikiRunEnd, bool, error) {
	end := domain.WikiRunEnd{Outcome: "reaped", EndReason: string(reason), Result: "failed"}
	switch reason {
	case dispatch.ReapReported:
		return domain.WikiRunEnd{}, false, nil
	case dispatch.ReapSpawnWatchdog:
		end.Cause = "The Scribe run did not start in time."
	case dispatch.ReapLeaseTimeout:
		end.Cause = "The heartbeat of the Scribe run stopped."
	case dispatch.ReapHardDeadline:
		end.Cause = "The Scribe run reached its time limit."
	case dispatch.ReapContainerGone:
		end.Cause = "The container of the Scribe run stopped."
	case dispatch.ReapVMRestart:
		end.Cause = "Docker restarted during the Scribe run."
	case dispatch.ReapInterrupted:
		end.Outcome, end.Cause = "interrupted", "The Project is archived."
	default:
		return domain.WikiRunEnd{}, false, Domainf("dispatch: a wiki round has no end for the stop reason %q", reason)
	}
	return end, true, nil
}

func wikiIdle(project, reason string) map[string]any {
	return map[string]any{"action": "idle", "project": project, "reason": reason}
}

// wikiPrepare is the first step of a wiki round. It stores the observation
// of the accepted commit and decides the round. An idle result and a stop are
// final. A stop writes no receipt: the round repeats the stop until the
// resident confirms it. A due wiki update returns the candidate that owes
// the attest step and the commit step.
func wikiPrepare(ctx context.Context, q Q, identity dispatchProtocolIdentity, uuid, requestID string, pipelineLiveness []PrivatePipelineLiveness, capability PrivateRuntimeCapability, project, observation string) (preparedDispatch, error) {
	if err := requireDeploymentUUID(ctx, q, uuid); err != nil {
		return preparedDispatch{}, err
	}
	if !validLowercaseHex(requestID, 16) {
		return preparedDispatch{}, Domainf("dispatch request id must be exactly 16 lowercase hex characters")
	}
	if replay, found, err := lookupDispatchReceipt(ctx, q, requestID); err != nil {
		return preparedDispatch{}, err
	} else if found {
		return preparedDispatch{
			requestID: requestID, deploymentUUID: uuid, project: project, identity: identity,
			final: replay, capabilityDigest: consumedCapabilityDigest(replay, capability),
		}, nil
	}
	status, _, err := enterWikiRound(ctx, q, project, observation)
	if err != nil {
		return preparedDispatch{}, err
	}
	if _, err := domain.RecoverWikiUpdate(ctx, q, project); err != nil {
		return preparedDispatch{}, err
	}
	sel, err := selectWiki(ctx, q, project, status, observation, pipelineLiveness)
	if err != nil {
		return preparedDispatch{}, err
	}
	final := func(effect map[string]any) preparedDispatch {
		return preparedDispatch{requestID: requestID, deploymentUUID: uuid, project: project, identity: identity, final: effect}
	}
	switch sel.action.Kind {
	case dispatch.KindIdle:
		return final(wikiIdle(project, string(sel.action.Idle))), nil

	case dispatch.KindReap:
		reap := sel.action.Reap
		end, ends, err := wikiStopEnd(reap.Reason)
		if err != nil {
			return preparedDispatch{}, err
		}
		if ends {
			if _, err := domain.EndWikiRun(ctx, q, reap.RunID, end); err != nil {
				return preparedDispatch{}, err
			}
		}
		return final(map[string]any{
			"action":         "reap",
			"project":        project,
			"reason":         string(reap.Reason),
			"run_id":         reap.RunID,
			"stop_container": reap.StopContainer,
		}), nil

	case dispatch.KindSpawn:
		runID, err := newRunID()
		if err != nil {
			return preparedDispatch{}, err
		}
		mountState, err := loadWikiMountState(ctx, q, project, runID, observation)
		if err != nil {
			return preparedDispatch{}, err
		}
		canonical, err := buildCanonicalWikiPrepare(identity, uuid, requestID, sel, mountState,
			spawnCandidateProjection(runID, sel.action.Spawn)).bytes()
		if err != nil {
			return preparedDispatch{}, err
		}
		return preparedDispatch{requestID: requestID, deploymentUUID: uuid, project: project, identity: identity, candidate: &preparedCandidate{
			spawn:      sel.action.Spawn,
			runID:      runID,
			tun:        sel.tun,
			token:      preparationToken(canonical),
			mountState: mountState,
		}}, nil
	}
	return preparedDispatch{}, Domainf("dispatch: unknown action kind %q", sel.action.Kind)
}

// wikiCommit is the last step of a wiki round. It starts the wiki update of
// the prepared candidate, or it applies exactly one refusal. The start needs
// three equal commits: the observation of this step, the stored accepted
// commit, and the start commit of the candidate. The step then loads the
// facts again and requires the token of the prepare step.
//
// A failed connection check starts nothing and writes nothing: the wiki
// update stays due, and the next tick tests the connection again.
func wikiCommit(ctx context.Context, q Q, prepared preparedDispatch, attested attestedDispatch, observation string) (map[string]any, error) {
	cand := prepared.candidate
	if !wikiCandidate(cand) {
		return nil, Domainf("dispatch: the commit step of a wiki round requires a wiki candidate")
	}
	if attested.deploymentUUID != prepared.deploymentUUID {
		return nil, Domainf("dispatch: attested deployment identity does not match prepare")
	}
	if err := requireDeploymentUUID(ctx, q, attested.deploymentUUID); err != nil {
		return nil, err
	}
	if !validLowercaseHex(prepared.requestID, 16) {
		return nil, Domainf("dispatch request id must be exactly 16 lowercase hex characters")
	}
	if replay, found, err := lookupDispatchReceipt(ctx, q, prepared.requestID); err != nil {
		return nil, err
	} else if found {
		return replay, nil
	}
	project, runID := prepared.project, cand.runID
	startCommit := cand.mountState.SubjectRepository.InputCommit

	// This transaction stores the observation also when it starts nothing.
	status, stored, err := enterWikiRound(ctx, q, project, observation)
	if err != nil {
		return nil, err
	}
	if observation == "" || observation != stored || observation != startCommit {
		return wikiIdle(project, idleWikiObservationChanged), nil
	}

	if _, err := domain.RecoverWikiUpdate(ctx, q, project); err != nil {
		return nil, err
	}
	sel, err := selectWiki(ctx, q, project, status, observation, nil)
	if err != nil {
		return nil, err
	}
	start := domain.WikiStart{
		Project: project, RunID: runID, StartCommit: startCommit,
		SessionPath: "sessions/" + runID, HardDeadlineMinutes: sel.tun.hardDeadlineMinutes,
	}
	// A refusal without a route gives the run no binding.
	if attested.route.Harness != "" || attested.route.Binding != "" {
		start.Binding = attested.route.HistoricalBinding()
	}
	rcand := RefusalCandidate{Kind: RefusalWikiUpdate, Project: project, WikiStart: start}
	refuse := func(r refusal.Refusal, authority ...attestedDispatch) (map[string]any, error) {
		key, err := refusalDispatchKey(prepared, r, authority...)
		if err != nil {
			return nil, err
		}
		return applyWikiRefusal(ctx, q, sel.now, rcand, r, key, prepared.requestID)
	}
	inert := func(code string) (map[string]any, error) {
		return refuse(refusal.Refusal{Code: code, Field: refusal.FieldNone, Summary: refusal.SummaryMismatch})
	}

	currentMountState, err := loadWikiMountState(ctx, q, project, runID, startCommit)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(currentMountState, cand.mountState) {
		return inert(refusal.CodeStale)
	}
	proj := spawnCandidateProjection(runID, cand.spawn)
	canonical, err := buildCanonicalWikiPrepare(prepared.identity, attested.deploymentUUID, prepared.requestID, sel, cand.mountState, proj).bytes()
	if err != nil {
		return nil, err
	}
	if preparationToken(canonical) != cand.token {
		return inert(refusal.CodeStale)
	}
	if sel.action.Kind != dispatch.KindSpawn {
		return inert(refusal.CodeCandidateMismatch)
	}
	reselected, err := json.Marshal(spawnCandidateProjection(runID, sel.action.Spawn))
	if err != nil {
		return nil, err
	}
	preparedProj, err := json.Marshal(proj)
	if err != nil {
		return nil, err
	}
	if string(reselected) != string(preparedProj) {
		return inert(refusal.CodeCandidateMismatch)
	}

	if attested.refusal != nil {
		return refuse(*attested.refusal, attested)
	}

	plan := attested.mountPlan
	if plan == nil {
		return nil, Domainf("dispatch: a spawn attestation carries no mount plan")
	}
	expectedToolHome, err := expectedPrivateToolHome(currentMountState)
	if err != nil {
		return nil, err
	}
	// A wiki update uses its isolated repository and the Project home.
	if !reflect.DeepEqual(plan.Repository, expectedRepositoryPlan(currentMountState, string(cand.spawn.Role))) ||
		(expectedToolHome == "") != (plan.ToolHome == nil) ||
		(plan.ToolHome != nil && plan.ToolHome.Canonical != expectedToolHome) ||
		len(plan.Entries) != 0 || len(plan.SeedingProjections) != 0 || plan.JurisdictionDigest == "" {
		return inert(refusal.CodeStale)
	}
	planDigest, err := mountPlanDigest(plan)
	if err != nil {
		return nil, err
	}
	gitIdentityOverride, err := selectedDispatchGitIdentityOverride(currentMountState)
	if err != nil {
		return nil, err
	}
	runtime := privateRuntimeBindingFromSpec(attested.runtimeBinding)
	key, err := deriveDispatchKey(cand.token, canonicalAction{
		Version:        dispatchActionVersion,
		RequestID:      prepared.requestID,
		Consequence:    "spawn",
		RunID:          runID,
		Role:           string(cand.spawn.Role),
		RuntimeDigest:  attested.runtimeDigest,
		RoutingDigest:  attested.routingDigest,
		Harness:        attested.route.Harness,
		Binding:        attested.route.Binding,
		RuntimeBinding: runtime,
		PlanDigest:     planDigest,
	})
	if err != nil {
		return nil, err
	}

	authorization, revision, err := spawnConnection(ctx, q, sel.now, runtime)
	if err != nil {
		return wikiIdle(project, idleWikiConnectionUnavailable), nil
	}
	launch, err := buildSpawnEffect(ctx, q, sel.action.Spawn, sel.tun, attested.route, runtime, runID, plan, gitIdentityOverride)
	if errors.Is(err, errEffectOversize) {
		return refuse(refusal.Refusal{
			Code: refusal.CodeEffectOversize, Field: refusal.FieldNone, Summary: refusal.SummaryOversize,
		}, attested)
	}
	if err != nil {
		return nil, err
	}

	if err := domain.StartWikiUpdate(ctx, q, sel.now, start); err != nil {
		return nil, err
	}
	if err := recordRunConnection(ctx, q, runID, runtime, authorization, revision); err != nil {
		return nil, err
	}
	if err := recordRunCustomization(ctx, q, sel.action.Spawn, runID); err != nil {
		return nil, err
	}
	if err := writeWikiStartRecord(ctx, q, runID, project, key, prepared.requestID, launch.effect); err != nil {
		return nil, err
	}
	return launch.effect, nil
}

// writeWikiStartRecord writes the start record of a wiki update. The record
// is the receipt of the round: a second commit step with the same request
// identifier returns the stored result.
func writeWikiStartRecord(ctx context.Context, q Q, runID, project, key, requestID string, effect map[string]any) error {
	body, err := storableReceipt(effect)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `
		INSERT INTO activity
			(actor, kind, subject, detail, project, dispatch_key, dispatch_request_id, dispatch_result)
		VALUES ('dispatch', 'wiki.update_started', ?, 'The wiki update started.', ?, ?, ?, ?)`,
		runID, project, key, requestID, body)
	return err
}

// dispatchWikiRounds is the wiki loop of the in-process tick: one wiki round
// for each Project of the tick order. It reads each accepted commit one time
// before the first round. A failed round adds one error envelope to the tick,
// and the loop continues. An idle round adds nothing.
func dispatchWikiRounds(db *sql.DB, home, uuid string, identity dispatchProtocolIdentity, capability PrivateRuntimeCapability, order []string, observe AcceptedCommitObserver, tick *dispatchTick) {
	observed := map[string]string{}
	if observe != nil && len(order) > 0 {
		observed = observe.All(context.Background(), order)
	}
	for _, project := range order {
		effect, err := dispatchWikiRound(db, home, uuid, identity, capability, project, observed[project], observe)
		if err != nil {
			tick.fail(err)
			continue
		}
		if effect["action"] != "idle" {
			tick.actions = append(tick.actions, effect)
		}
	}
}

// dispatchWikiRound runs one wiki round of the in-process tick. A panic of a
// step becomes the error of the round. A panic inside a transaction first
// becomes the error of that transaction, so the transaction rolls back.
func dispatchWikiRound(db *sql.DB, home, uuid string, identity dispatchProtocolIdentity, capability PrivateRuntimeCapability, project, observation string, observe AcceptedCommitObserver) (effect map[string]any, err error) {
	defer func() {
		if r := recover(); r != nil {
			effect, err = nil, Domainf("dispatch: the wiki round of Project %q stopped: %v", project, r)
		}
	}()
	step := func(fn func(context.Context, Q) error) error {
		return underDispatchLock(db, func(ctx context.Context, q Q) (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = Domainf("dispatch: the wiki round of Project %q stopped: %v", project, r)
				}
			}()
			return fn(ctx, q)
		})
	}
	requestID, err := newDispatchRequestID()
	if err != nil {
		return nil, err
	}
	var prepared preparedDispatch
	if err := step(func(ctx context.Context, q Q) error {
		var e error
		prepared, e = wikiPrepare(ctx, q, identity, uuid, requestID, nil, capability, project, observation)
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

	// The commit step needs a new observation. The read is outside each transaction.
	second := ""
	if observe != nil {
		second = observe.One(context.Background(), project)
	}
	if err := step(func(ctx context.Context, q Q) error {
		var e error
		effect, e = wikiCommit(ctx, q, prepared, attested, second)
		return e
	}); err != nil {
		return nil, err
	}
	return effect, nil
}
