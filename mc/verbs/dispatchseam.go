package verbs

// The dispatch seam's pure primitives: the canonical prepare projection, the
// preparation token, and the commit-side dispatch_key.
//
// The canonical structs below are a frozen cross-harness wire contract, the
// same discipline as homieCandidateState: json.Marshal over closed structs in
// declared field order, UTF-8 strings, decimal integers, explicit zero
// values, pointers for null, no maps or floats. Semantically unordered
// collections are sorted by their declared key before encoding; nil slices
// normalize to empty ones so "absent" and "empty" never encode differently.
// Times are spine strings ("2006-01-02 15:04:05"). A change to any shape here
// is a design decision. It bumps the action version and the dispatch key
// domain, so a receipt written under the old shape can never collide with a
// new key. The golden vectors in dispatchseam_test.go pin every shape.
//
// Wall-clock time is deliberately NOT part of the projection: commit
// re-decides with its own fresh clock, so second-granularity drift between
// the two transactions can change the selected action (caught by the
// re-decide equality check) but never falsely stales byte-identical state.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"

	"mc/boundary"
	"mc/dispatch"
	"mc/refusal"
	"mc/routing"
	"mc/substrate"
)

// Domain separators. The preparation token participates in dispatch_key
// derivation in its hex ASCII form; the plan digest participates in the
// canonical action the same way. The action domain version equals the
// canonicalAction version.
const (
	dispatchActionVersion  = 10
	dispatchPrepareVersion = 16
	prepareTokenDomain     = "MC-DISPATCH-PREPARE-V1\x00"
	dispatchKeyDomain      = "MC-DISPATCH-ACTION-V10\x00"
	dispatchPlanDomain     = "MC-DISPATCH-PLAN-V1\x00"
)

type canonicalTask struct {
	Delivery            canonicalDelivery `json:"delivery"`
	ExecutionHeld       bool              `json:"execution_held,omitempty"`
	RequirementsVersion int64             `json:"requirements_version,omitempty"`
	ID                  int64             `json:"id"`
	NextRetryAt         string            `json:"next_retry_at"`
	Title               string            `json:"title"`
	Scope               string            `json:"scope"`
	Priority            int               `json:"priority"`
	CreatedAt           string            `json:"created_at"`
	Status              string            `json:"status"`
	Blocked             bool              `json:"blocked"`
	Stalled             bool              `json:"stalled"`
	DispatchRetries     int               `json:"dispatch_retries"`
	Decision            string            `json:"decision"`
	DecidedAt           *string           `json:"decided_at"`
	Archived            bool              `json:"archived"`
	Project             string            `json:"project"`
	ProjectStatus       string            `json:"project_status"`
}

type canonicalDelivery struct {
	VersionID      int64 `json:"version_id"`
	CorrectionOpen bool  `json:"correction_open,omitempty"`
}

type canonicalPacket struct {
	Superseded bool   `json:"superseded,omitempty"`
	TaskID     int64  `json:"task_id"`
	CreatedAt  string `json:"created_at"`
	Archived   bool   `json:"archived"`
}

// canonicalLease is the evaluated Project's lease row.
type canonicalLease struct {
	Held              bool   `json:"held"`
	FinalOutcome      string `json:"final_outcome"`
	RunID             string `json:"run_id"`
	Owner             string `json:"owner"`
	SubjectID         *int64 `json:"subject_id"`
	BootID            string `json:"boot_id"`
	AcquiredUptimeMS  *int64 `json:"acquired_uptime_ms"`
	HeartbeatUptimeMS *int64 `json:"heartbeat_uptime_ms"`
	DeadlineUptimeMS  *int64 `json:"deadline_uptime_ms"`
}

type canonicalTunables struct {
	TimeoutMinutes      int `json:"timeout_minutes"`
	GraceMinutes        int `json:"grace_minutes"`
	HeartbeatIntervalS  int `json:"heartbeat_interval_s"`
	SpawnGraceS         int `json:"spawn_grace_s"`
	HardDeadlineMinutes int `json:"hard_deadline_minutes"`
}

// canonicalCandidate is the bounded logical candidate a prepare returns for
// the steps that need native authority. The run id is allocated at prepare
// and becomes canonical only at commit. The Project is the Project of the
// round.
type canonicalCandidate struct {
	Kind         string   `json:"kind"`
	RunID        string   `json:"run_id"`
	Project      string   `json:"project"`
	Role         string   `json:"role"`
	SubjectID    *int64   `json:"subject_id"`
	ProposedPool []int64  `json:"proposed_pool"`
	DedupeTitles []string `json:"dedupe_titles"`
}

// canonicalPrepare freezes the selected Project and its attention tasks.
type canonicalPrepare struct {
	ProposalDecisionPending    bool                      `json:"proposal_decision_pending"`
	ConnectionStates           string                    `json:"connection_states"`
	InfrastructureFailureCount int                       `json:"infrastructure_failure_count"`
	InfrastructureNextRetryAt  string                    `json:"infrastructure_next_retry_at"`
	Version                    int                       `json:"version"`
	ReleaseBuildID             string                    `json:"release_build_id"`
	ControlVersion             int                       `json:"gateway_control_version"`
	SpineSchemaID              string                    `json:"spine_schema_id"`
	ConfigSchemaVersion        int                       `json:"config_schema_version"`
	DeploymentUUID             string                    `json:"deployment_uuid"`
	RequestID                  string                    `json:"request_id"`
	Project                    string                    `json:"project"`
	ProjectStatus              string                    `json:"project_status"`
	EnvironmentCondition       string                    `json:"environment_condition,omitempty"`
	EnvironmentRevision        string                    `json:"environment_revision,omitempty"`
	VisionReady                bool                      `json:"vision_ready,omitempty"`
	VisionPath                 string                    `json:"vision_path,omitempty"`
	VisionContent              string                    `json:"vision_content,omitempty"`
	AttentionTaskIDs           []int64                   `json:"attention_task_ids"`
	Tasks                      []canonicalTask           `json:"tasks"`
	Packets                    []canonicalPacket         `json:"packets"`
	Lease                      canonicalLease            `json:"lease"`
	Tunables                   canonicalTunables         `json:"tunables"`
	Homies                     []homieCandidateState     `json:"homies"`
	MountState                 PrivateDispatchMountState `json:"mount_state"`
	Candidate                  canonicalCandidate        `json:"candidate"`
}

type dispatchProtocolIdentity struct {
	releaseBuildID      string
	controlVersion      int
	spineSchemaID       string
	configSchemaVersion int
}

var defaultDispatchProtocolIdentity = dispatchProtocolIdentity{
	releaseBuildID: "development", controlVersion: GatewayControlVersion,
	spineSchemaID: substrate.CurrentSchemaID, configSchemaVersion: 1,
}

func (p canonicalPrepare) bytes() ([]byte, error) {
	return json.Marshal(p)
}

// canonicalRefusal carries only mc/refusal's closed enums — never Message,
// which is hostile input.
type canonicalRefusal struct {
	Code      string `json:"code"`
	Authority string `json:"authority"`
	Field     string `json:"field"`
	Summary   string `json:"summary"`
	ItemIndex *int   `json:"item_index"`
}

// canonicalAction is the commit-side action encoding under dispatch_key. It
// binds the attested host projection (today: the routing.md digest and the
// resolved route) to the prepared candidate and its consequence.
type canonicalAction struct {
	Version        int                   `json:"version"`
	RequestID      string                `json:"request_id"`
	Consequence    string                `json:"consequence"` // "spawn" | "refusal"
	RunID          string                `json:"run_id"`
	Role           string                `json:"role"`
	SubjectID      *int64                `json:"subject_id"`
	RuntimeDigest  string                `json:"runtime_digest"`
	RoutingDigest  string                `json:"routing_digest"`
	Harness        string                `json:"harness"`
	Binding        string                `json:"binding"`
	RuntimeBinding PrivateRuntimeBinding `json:"runtime_binding"`
	PlanDigest     string                `json:"plan_digest"`
	Refusal        *canonicalRefusal     `json:"refusal"`
}

func (a canonicalAction) bytes() ([]byte, error) {
	return json.Marshal(a)
}

// preparationToken derives the prepare token: SHA-256 over the
// domain-separated canonical prepare bytes, hex-encoded.
func preparationToken(canonical []byte) string {
	h := sha256.New()
	h.Write([]byte(prepareTokenDomain))
	h.Write(canonical)
	return hex.EncodeToString(h.Sum(nil))
}

// mountPlanDigest derives the plan digest over the canonical plan
// bytes: SHA-256 of the domain-separated closed encoding, hex. A spawn action
// binds it into dispatch_key; a refusal consequence carries no plan and
// encodes the explicit empty string.
func mountPlanDigest(plan *PrivateDispatchMountPlan) (string, error) {
	if plan == nil {
		return "", nil
	}
	body, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(dispatchPlanDomain))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// deriveDispatchKey computes the commit-side idempotency fence. The helper
// computes it at commit from bytes it reconstructs itself; it is never a
// caller-supplied hash.
func deriveDispatchKey(token string, action canonicalAction) (string, error) {
	body, err := action.bytes()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(dispatchKeyDomain))
	h.Write([]byte(token))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// newDispatchRequestID allocates the command-scoped request id: exactly 16
// lowercase hex, minted once per external command and reused across transport
// retries of that command.
func newDispatchRequestID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", Usagef("allocate dispatch request id: %v", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func spineTimeString(t time.Time) string {
	return t.Format(spineTime)
}

func spineTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := spineTimeString(*t)
	return &s
}

// spawnCandidateProjection projects the decided spawn into the bounded
// candidate frame, normalizing nil slices.
func spawnCandidateProjection(runID string, sp *dispatch.Spawn) canonicalCandidate {
	c := canonicalCandidate{
		Kind:         "spawn",
		RunID:        runID,
		Project:      sp.Project,
		Role:         string(sp.Role),
		SubjectID:    sp.SubjectID,
		ProposedPool: sp.ProposedPool,
		DedupeTitles: sp.DedupeTitles,
	}
	if c.ProposedPool == nil {
		c.ProposedPool = []int64{}
	}
	if c.DedupeTitles == nil {
		c.DedupeTitles = []string{}
	}
	return c
}

// buildCanonicalPrepare assembles the closed projection from what the prepare
// transaction read for one Project (rec.Project names it). Records
// arrive in SQL order, which is not part of the wire contract, so tasks sort
// by id and packets by (task_id, created_at).
func buildCanonicalPrepare(uuid, requestID string, rec dispatch.Records, lease dispatch.Lease, tun tunables, homies []homieCandidateState, mounts PrivateDispatchMountState, cand canonicalCandidate) canonicalPrepare {
	return buildCanonicalPrepareWithIdentity(defaultDispatchProtocolIdentity, uuid, requestID, rec, lease, tun, homies, mounts, cand)
}

func buildCanonicalPrepareWithIdentity(identity dispatchProtocolIdentity, uuid, requestID string, rec dispatch.Records, lease dispatch.Lease, tun tunables, homies []homieCandidateState, mounts PrivateDispatchMountState, cand canonicalCandidate) canonicalPrepare {
	tasks := make([]canonicalTask, 0, len(rec.Tasks))
	for _, t := range rec.Tasks {
		delivery := canonicalDelivery{VersionID: t.Delivery.VersionID, CorrectionOpen: t.Delivery.CorrectionOpen}
		tasks = append(tasks, canonicalTask{
			Delivery:      delivery,
			ExecutionHeld: t.ExecutionHeld, RequirementsVersion: t.RequirementsVersion,
			NextRetryAt:     t.NextRetryAt,
			ID:              t.ID,
			Title:           t.Title,
			Scope:           string(t.Scope),
			Priority:        t.Priority,
			CreatedAt:       spineTimeString(t.CreatedAt),
			Status:          string(t.Status),
			Blocked:         t.Blocked,
			Stalled:         t.Stalled,
			DispatchRetries: t.DispatchRetries,
			Decision:        string(t.Decision),
			DecidedAt:       spineTimePtr(t.DecidedAt),
			Archived:        t.Archived,
			Project:         t.Project,
			ProjectStatus:   t.ProjectStatus,
		})
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })

	packets := make([]canonicalPacket, 0, len(rec.Packets))
	for _, p := range rec.Packets {
		packets = append(packets, canonicalPacket{
			Superseded: p.Superseded,
			TaskID:     p.TaskID,
			CreatedAt:  spineTimeString(p.CreatedAt),
			Archived:   p.Archived,
		})
	}
	sort.Slice(packets, func(i, j int) bool {
		if packets[i].TaskID != packets[j].TaskID {
			return packets[i].TaskID < packets[j].TaskID
		}
		return packets[i].CreatedAt < packets[j].CreatedAt
	})

	canonical := canonicalLease{Held: lease.Held, FinalOutcome: lease.FinalOutcome}
	if lease.Held {
		canonical.RunID = lease.RunID
		canonical.Owner = lease.Owner
		canonical.SubjectID = lease.SubjectID
		canonical.BootID = lease.BootID
		acquired, deadline := lease.AcquiredUptime.Milliseconds(), lease.DeadlineUptime.Milliseconds()
		canonical.AcquiredUptimeMS, canonical.DeadlineUptimeMS = &acquired, &deadline
		if lease.HeartbeatUptime != nil {
			heartbeat := lease.HeartbeatUptime.Milliseconds()
			canonical.HeartbeatUptimeMS = &heartbeat
		}
	}

	if homies == nil {
		homies = []homieCandidateState{}
	}
	if mounts.Projects == nil {
		mounts.Projects = []PrivateDispatchProject{}
	}

	return canonicalPrepare{
		ConnectionStates:           rec.ConnectionStates,
		ProposalDecisionPending:    rec.ProposalDecisionPending,
		InfrastructureFailureCount: rec.InfrastructureFailureCount, InfrastructureNextRetryAt: rec.InfrastructureNextRetryAt,
		Version:              dispatchPrepareVersion,
		ReleaseBuildID:       identity.releaseBuildID,
		ControlVersion:       identity.controlVersion,
		SpineSchemaID:        identity.spineSchemaID,
		ConfigSchemaVersion:  identity.configSchemaVersion,
		DeploymentUUID:       uuid,
		RequestID:            requestID,
		Project:              rec.Project,
		ProjectStatus:        rec.ProjectStatus,
		EnvironmentCondition: rec.EnvironmentCondition,
		EnvironmentRevision:  rec.EnvironmentRevision,
		VisionReady:          rec.VisionReady, VisionPath: rec.VisionPath, VisionContent: rec.VisionContent,
		AttentionTaskIDs: append([]int64{}, rec.AttentionTaskIDs...),
		Tasks:            tasks,
		Packets:          packets,
		Lease:            canonical,
		Tunables: canonicalTunables{
			TimeoutMinutes:      tun.timeoutMinutes,
			GraceMinutes:        tun.graceMinutes,
			HeartbeatIntervalS:  tun.heartbeatIntervalS,
			SpawnGraceS:         tun.spawnGraceS,
			HardDeadlineMinutes: tun.hardDeadlineMinutes,
		},
		Homies:     homies,
		MountState: mounts,
		Candidate:  cand,
	}
}

// loadHomieProjection reads every active session's frozen candidate state,
// ordered by session id — the launch-generation observation the prepare
// projection binds and the Homie-end fence later compares. The
// column list and InputSeq subquery must stay semantically identical to
// homiePrePrepareState's; TestDispatchSeamHomieProjection holds the two
// together by key equality.
func loadHomieProjection(ctx context.Context, q Q) ([]homieCandidateState, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT s.id, s.binding,
		       s.current_launch_id, s.current_launch_mode,
		       s.current_prime_through_seq, s.current_prime_row_count,
		       s.resume_owed, s.resume_mode,
		       s.resume_prime_through_seq, s.resume_prime_row_count,
		       (SELECT MAX(m.seq) FROM conversation_messages m
		        WHERE m.session_id = s.id
		          AND m.direction = 'inbound' AND m.completed_at IS NULL)
		FROM homie_sessions s WHERE s.status = 'active' ORDER BY s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []homieCandidateState{}
	for rows.Next() {
		var st homieCandidateState
		var launchID, launchMode, resumeMode sql.NullString
		var primeSeq, primeCount, resumeSeq, resumeCount, inputSeq sql.NullInt64
		if err := rows.Scan(
			&st.SessionID, &st.Binding,
			&launchID, &launchMode, &primeSeq, &primeCount,
			&st.ResumeOwed, &resumeMode, &resumeSeq, &resumeCount,
			&inputSeq,
		); err != nil {
			return nil, err
		}
		st.LaunchID = nullStringPtr(launchID)
		st.LaunchMode = nullStringPtr(launchMode)
		st.PrimeSeq = nullInt64Ptr(primeSeq)
		st.PrimeCount = nullInt64Ptr(primeCount)
		st.ResumeMode = nullStringPtr(resumeMode)
		st.ResumeSeq = nullInt64Ptr(resumeSeq)
		st.ResumeCount = nullInt64Ptr(resumeCount)
		st.InputSeq = nullInt64Ptr(inputSeq)
		out = append(out, st)
	}
	return out, rows.Err()
}

func nullStringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func nullInt64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

// ---------------------------------------------------------------------------
// The command frame: prepare → attest → commit.
//
// One external `mc dispatch` composes three steps. Prepare runs under the
// process flock and one BEGIN IMMEDIATE transaction against spine state only;
// a branch whose entire consequence is lock-domain-owned commits there, with
// its request receipt, and the command is done. A spawn instead returns a
// bounded candidate and preparation token. Attest runs with both released —
// it alone reads host files (routing.md and the mount plan). Commit
// reacquires, re-reads, requires the recomputed token to equal the prepared
// one byte-for-byte, re-decides, and applies exactly the reselected
// candidate's consequence — a spawn, or a classified refusal routed through
// the consequence router with a dispatch_key that is DERIVED (token +
// canonical action) rather than taken on faith. It never falls through to
// another candidate.
//
// This is the native single-process form for Linux: the resident calls the
// same prepare/attest/commit functions locally in one process, and releases
// the transaction and the flock across attest I/O on purpose. The Darwin
// broker/helper self-delegation split — private __dispatch-prepare/
// __dispatch-commit CLI frames over the one-shot control descriptor — is a
// layer over these same functions.
// ---------------------------------------------------------------------------

// preparedDispatch is what one prepare invocation produced: either a final
// result (lock-domain consequence or receipt replay) or a candidate that owes
// attest and commit. project names the round: "" is the Homie stage, a
// Project id is that Project's pipeline round.
type preparedDispatch struct {
	visionCheck    *ProjectVisionCheck
	visionObserved bool
	requestID      string
	deploymentUUID string
	project        string
	identity       dispatchProtocolIdentity
	final          map[string]any
	candidate      *preparedCandidate
	// capabilityDigest names the runtime catalog a FINAL effect resolved
	// against, and is empty for every final that consumed none. The host fences
	// drift on it immediately after prepare returns.
	capabilityDigest string
}

type preparedCandidate struct {
	spawn      *dispatch.Spawn
	runID      string
	tun        tunables
	token      string
	mountState PrivateDispatchMountState
	// binding is host-attested from routing.md immediately before mount/env
	// attestation. It is never prepared from helper-owned state.
	binding        string
	bindingSpec    routing.BindingSpec
	bindingCatalog map[string]routing.BindingSpec
	forbiddenEnv   []string
}

// attestedDispatch is the attest step's host projection: the resolved route,
// the digest binding it, and the validated mount plan — or a classified
// refusal, never both.
type attestedDispatch struct {
	deploymentUUID string
	route          routing.Route
	runtimeDigest  string
	routingDigest  string
	runtimeBinding routing.BindingSpec
	mountPlan      *PrivateDispatchMountPlan
	refusal        *refusal.Refusal
}

// dispatchPrepare is the helper's first invocation. Order is load-bearing:
// the deployment precondition before anything on every branch, then the
// receipt fence BEFORE reading selection state — a lost response looked up
// after selection could reap-then-claim in one command, which the replay
// fence exists to prevent.
func dispatchPrepare(ctx context.Context, q Q, uuid, requestID string, capability PrivateRuntimeCapability, project string) (preparedDispatch, error) {
	return dispatchPrepareWithIdentity(ctx, q, defaultDispatchProtocolIdentity, uuid, requestID, capability, project)
}

func dispatchPrepareWithIdentity(ctx context.Context, q Q, identity dispatchProtocolIdentity, uuid, requestID string, capability PrivateRuntimeCapability, project string) (preparedDispatch, error) {
	return dispatchPrepareWithIdentityInventory(ctx, q, identity, uuid, requestID, nil, nil, capability, project)
}

// homieStageIdleReason is the Homie stage's "nothing owed" result. It is a
// final like every other prepare result so the private frame shape is one
// shape; the tick files it as no action at all (dispatchTick.record).
const homieStageIdleReason = "homie-idle"

// dispatchPrepareWithIdentityInventory is one round's prepare. project ""
// is the Homie stage (lease-free, over every Project); a Project id is that
// Project's pipeline round (the control flow of mc dispatch applied to the
// records and the lease of that Project).
func dispatchPrepareWithIdentityInventory(
	ctx context.Context,
	q Q,
	identity dispatchProtocolIdentity,
	uuid, requestID string,
	homieInventory []PrivateHomieContainer,
	pipelineLiveness []PrivatePipelineLiveness,
	capability PrivateRuntimeCapability,
	project string,
	vision ...*ProjectVisionCheck,
) (preparedDispatch, error) {
	if err := requireDeploymentUUID(ctx, q, uuid); err != nil {
		return preparedDispatch{}, err
	}
	if !validLowercaseHex(requestID, 16) {
		return preparedDispatch{}, Domainf("dispatch request id must be exactly 16 lowercase hex characters")
	}
	if replay, found, err := lookupDispatchReceipt(ctx, q, requestID); err != nil {
		return preparedDispatch{}, err
	} else if found {
		// A replayed effect owes the drift fence exactly as the fresh one did:
		// the spine has already committed the launch, but delivering a wake
		// resolved against a catalog the operator has since changed would start
		// the very binding they disabled. The host abandons it and the next tick
		// re-decides.
		return preparedDispatch{
			requestID: requestID, deploymentUUID: uuid, project: project, identity: identity,
			final: replay, capabilityDigest: consumedCapabilityDigest(replay, capability),
		}, nil
	}
	if project == "" {
		return dispatchPrepareHomieStage(ctx, q, identity, uuid, requestID, homieInventory, capability)
	}
	return dispatchPreparePipelineRound(ctx, q, identity, uuid, requestID, pipelineLiveness, project, vision...)
}

// dispatchPrepareHomieStage is the tick's first round: the lease-free Homie
// tier, deployment-wide, evaluated once per tick ahead of
// every pipeline round. The rounds run in their fixed order — exact orphan
// cleanup, created, removal, started-absent,
// starting-timeout, then wake/idle-end (branches 6-7) — and yield at most
// one Homie effect, which becomes the tick's first action. A nil inventory
// means the in-process development seam has no resident-owned Docker
// snapshot; the production private crossing always supplies a non-nil
// (possibly empty) inventory, and only then do the inventory rounds run. The
// wake round reads the spine alone and runs on both seams. A mutating round
// (launch persist, session end) carries a receipt like every other mutating
// branch. Nothing here reads a lease or a Project's records: the Homie
// stage pre-empts no pipeline round.
func dispatchPrepareHomieStage(ctx context.Context, q Q, identity dispatchProtocolIdentity, uuid, requestID string, homieInventory []PrivateHomieContainer, capability PrivateRuntimeCapability) (preparedDispatch, error) {
	now, err := spineNow(ctx, q)
	if err != nil {
		return preparedDispatch{}, err
	}
	tun, err := loadSettings(ctx, q)
	if err != nil {
		return preparedDispatch{}, err
	}
	final := func(effect map[string]any) preparedDispatch {
		return preparedDispatch{requestID: requestID, deploymentUUID: uuid, identity: identity, final: effect}
	}
	if homieInventory != nil {
		if orphanEffect, handled, err := homieOrphanRound(ctx, q, homieInventory); err != nil {
			return preparedDispatch{}, err
		} else if handled {
			return final(orphanEffect), nil
		}
		if adoptionEffect, handled, err := homieCreatedRound(
			ctx, q, homieInventory, now, tun.homieIdleTimeoutS,
		); err != nil {
			return preparedDispatch{}, err
		} else if handled {
			return final(adoptionEffect), nil
		}
		if removalEffect, handled, err := homieRemovalRound(
			ctx, q, homieInventory, now, tun.homieIdleTimeoutS,
		); err != nil {
			return preparedDispatch{}, err
		} else if handled {
			return final(removalEffect), nil
		}
		if absenceEffect, handled, err := homieStartedAbsentRound(
			ctx, q, homieInventory, now, tun.homieIdleTimeoutS,
		); err != nil {
			return preparedDispatch{}, err
		} else if handled {
			if err := writeDispatchReceipt(ctx, q, requestID, absenceEffect); err != nil {
				return preparedDispatch{}, err
			}
			return final(absenceEffect), nil
		}
		if timeoutEffect, handled, err := homieStartingTimeoutRound(
			ctx, q, homieInventory, now, tun.homieIdleTimeoutS, tun.spawnGraceS,
		); err != nil {
			return preparedDispatch{}, err
		} else if handled {
			return final(timeoutEffect), nil
		}
	}
	if homieEffect, handled, err := homieWakeRoundWithConfirmedAbsence(
		ctx, q, now, tun.homieIdleTimeoutS, homieInventory, capability,
	); err != nil {
		return preparedDispatch{}, err
	} else if handled {
		if err := writeDispatchReceipt(ctx, q, requestID, homieEffect); err != nil {
			return preparedDispatch{}, err
		}
		prepared := final(homieEffect)
		prepared.capabilityDigest = consumedCapabilityDigest(homieEffect, capability)
		return prepared, nil
	}
	return final(map[string]any{"action": "idle", "reason": homieStageIdleReason}), nil
}

// dispatchPreparePipelineRound is one Project's round: select from that
// Project's records and lease, then either finalize (idle, or a reap with its
// receipt) or return the candidate a spawn owes to attest and commit. Every
// result names the Project.
func dispatchPreparePipelineRound(ctx context.Context, q Q, identity dispatchProtocolIdentity, uuid, requestID string, pipelineLiveness []PrivatePipelineLiveness, project string, vision ...*ProjectVisionCheck) (preparedDispatch, error) {
	sel, err := selectFromSpine(ctx, q, pipelineLiveness, project, vision...)
	if err != nil {
		return preparedDispatch{}, err
	}

	if sel.action.Kind == dispatch.KindSpawn {
		// Spawn needs native authority; allocate the candidate identity now
		// (it becomes canonical only at commit) and freeze the projection
		// under the token.
		runID, err := newRunID()
		if err != nil {
			return preparedDispatch{}, err
		}
		mountState, err := loadDispatchMountState(ctx, q, sel.action.Spawn, sel.rec)
		if err != nil {
			return preparedDispatch{}, err
		}
		canonical, err := buildCanonicalPrepareWithIdentity(identity, uuid, requestID, sel.rec, sel.lease, sel.tun, sel.homies, mountState,
			spawnCandidateProjection(runID, sel.action.Spawn)).bytes()
		if err != nil {
			return preparedDispatch{}, err
		}
		return preparedDispatch{requestID: requestID, deploymentUUID: uuid, project: project, identity: identity, visionCheck: visionCheckFromRecords(sel.rec), candidate: &preparedCandidate{
			spawn:      sel.action.Spawn,
			runID:      runID,
			tun:        sel.tun,
			token:      preparationToken(canonical),
			mountState: mountState,
		}}, nil
	}

	effect, err := applyAction(ctx, q, sel.now, sel.action, sel.tun)
	if err != nil {
		return preparedDispatch{}, err
	}
	// A round without a spawn candidate waits for no connection.
	if err := endConnectionWait(ctx, q, project); err != nil {
		return preparedDispatch{}, err
	}
	switch sel.action.Kind {
	case dispatch.KindIdle:
		// An idle round names its Project too, so the tick can file it.
		effect["project"] = project
	case dispatch.KindReap:
		// A reap inserts its receipt atomically with the consequence. An idle
		// round needs no receipt: a lost-response retry evaluates it again, and
		// the end of a connection wait happens once.
		if err := writeDispatchReceipt(ctx, q, requestID, effect); err != nil {
			return preparedDispatch{}, err
		}
	}
	return preparedDispatch{requestID: requestID, deploymentUUID: uuid, project: project, identity: identity, final: effect}, nil
}

// consumedCapabilityDigest names the catalog a final effect resolved against,
// and the empty string for one that resolved no runtime at all (an idle end, a
// reap, the pipeline-idle result). It is the ONE place the spine side decides
// which effects owe the host's drift fence.
//
// The broker asks the same question of the same effect independently, from its
// raw bytes (capabilityConsumingFinal). That duplication is deliberate: a
// producer-only fence is fail-open, because an effect that simply omitted its
// claim would look like one owing no check. The two must agree on the
// discriminator — the effect's own action, plus the presence of a carried
// runtime — so that agreement is stated here and there in the same terms.
func consumedCapabilityDigest(effect map[string]any, capability PrivateRuntimeCapability) string {
	if effect == nil {
		return ""
	}
	if effect["action"] != "homie-wake" {
		if _, carried := effect["runtime_binding"]; !carried {
			return ""
		}
	}
	return capability.Digest
}

// dispatchAttest is the host-authority step, run with the flock and
// transaction released. It attests routing.md — reading, digesting, parsing,
// and resolving the candidate's role — and classifies any failure as the
// deployment-health refusal instead of erroring the command: routing
// brokenness is the deployment's fault, never the candidate task's, and the
// consequence (one dispatch.health action, no charge, no block, no claim)
// belongs to the commit transaction. The mount-plan attestation follows for
// a candidate that carries mount requests.
func dispatchAttest(home string, prepared preparedDispatch) (attestedDispatch, error) {
	cand := prepared.candidate
	if cand == nil {
		return attestedDispatch{}, Domainf("dispatch: attest requires a prepared candidate")
	}
	reattestedUUID, err := attestDeploymentPreamble(home, prepared)
	if err != nil {
		return attestedDispatch{}, err
	}
	authority, err := loadRuntimeAuthority(home)
	if err != nil {
		summary := refusal.SummaryUnparsable
		if os.IsNotExist(err) {
			summary = refusal.SummaryMissing
		}
		return configRefusal(reattestedUUID, summary, err), nil
	}
	path := filepath.Join(home, "routing.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return routingRefusal(reattestedUUID, refusal.SummaryMissing, err), nil
	}
	sum := sha256.Sum256(data)
	table, err := routing.Parse(data, authority.registry)
	if err != nil {
		return routingRefusal(reattestedUUID, refusal.SummaryUnparsable, err), nil
	}
	route, err := table.Resolve(baseRole(string(cand.spawn.Role)))
	if err != nil {
		return routingRefusal(reattestedUUID, refusal.SummaryUnresolved, err), nil
	}
	cand.binding = route.Binding
	cand.bindingCatalog = authority.configuredBindings
	cand.bindingSpec = authority.bindings[route.Binding]
	cand.forbiddenEnv = append([]string(nil), authority.config.Runtimes.ForbiddenEnv...)
	// The operator's blocked-pattern additions come from the SAME config
	// snapshot this attestation already resolved its routing authority from, so
	// one dispatch cannot mix two reads of config.toml.
	blocked, err := boundary.NewBlockPolicy(authority.config.Container.BoundaryPatterns())
	if err != nil {
		return configRefusal(reattestedUUID, refusal.SummaryUnparsable, err), nil
	}
	plan, mountRefusal, err := attestCandidateMounts(home, cand, blocked)
	if err != nil {
		return attestedDispatch{}, err
	}
	if mountRefusal != nil {
		return attestedDispatch{
			deploymentUUID: reattestedUUID,
			runtimeDigest:  authority.digest,
			routingDigest:  hex.EncodeToString(sum[:]),
			refusal:        mountRefusal,
		}, nil
	}
	return attestedDispatch{
		deploymentUUID: reattestedUUID, route: route,
		runtimeDigest: authority.digest, routingDigest: hex.EncodeToString(sum[:]),
		runtimeBinding: cand.bindingSpec,
		mountPlan:      plan,
	}, nil
}

// attestDeploymentPreamble is the fence every attest leg owes before it
// reads any other host file: re-read the deployment identity mirror strictly,
// and abandon the command if it moved since prepare.
func attestDeploymentPreamble(home string, prepared preparedDispatch) (string, error) {
	reattestedUUID, err := readDeploymentMirrorStrict(home)
	if err != nil {
		return "", err
	}
	if reattestedUUID != prepared.deploymentUUID {
		return "", Domainf("deployment identity mirror changed between dispatch prepare and attest")
	}
	return reattestedUUID, nil
}

// dispatchRecheckAttestation is the immediate pre-commit host-file fence.
// Any deployment/routing byte or interpretation drift abandons the prepared
// candidate as stale; it never claims from old host authority.
func dispatchRecheckAttestation(home string, prepared preparedDispatch, first attestedDispatch) attestedDispatch {
	second, err := dispatchAttest(home, prepared)
	if err == nil && reflect.DeepEqual(canonicalPrivateAttestation(first), canonicalPrivateAttestation(second)) {
		return first
	}
	return attestedDispatch{
		deploymentUUID: prepared.deploymentUUID,
		refusal: &refusal.Refusal{
			Code: refusal.CodeStale, Field: refusal.FieldNone, Summary: refusal.SummaryMismatch,
		},
	}
}

// routingRefusal classifies one routing failure. The raw error text rides
// only in Message, which DetailFor drops — routing.md bytes are operator
// material and never reach a stored detail.
func routingRefusal(deploymentUUID string, summary refusal.Summary, err error) attestedDispatch {
	return attestedDispatch{deploymentUUID: deploymentUUID, refusal: &refusal.Refusal{
		Code:    refusal.CodeRoutingInvalid,
		Field:   refusal.FieldRouting,
		Summary: summary,
		Message: err.Error(),
	}}
}

func configRefusal(deploymentUUID string, summary refusal.Summary, err error) attestedDispatch {
	return attestedDispatch{deploymentUUID: deploymentUUID, refusal: &refusal.Refusal{
		Code:    refusal.CodeConfigInvalid,
		Field:   refusal.FieldConfig,
		Summary: summary,
		Message: err.Error(),
	}}
}

// dispatchCommit is the helper's second invocation: under
// a fresh flock and transaction it reloads and re-decides lock-domain truth,
// requires the recomputed canonical projection to reproduce the preparation
// token byte-for-byte, and applies exactly the reselected candidate's
// consequence. Ordinary drift is preflight.stale; a decision that no longer
// reselects the prepared candidate is preflight.candidate_mismatch — both
// stale-class, both inert.
func dispatchCommit(ctx context.Context, q Q, prepared preparedDispatch, attested attestedDispatch) (map[string]any, error) {
	cand := prepared.candidate
	if cand == nil {
		return nil, Domainf("dispatch: commit requires a prepared candidate")
	}
	if attested.deploymentUUID != prepared.deploymentUUID {
		return nil, Domainf("dispatch: attested deployment identity does not match prepare")
	}
	if err := requireDeploymentUUID(ctx, q, attested.deploymentUUID); err != nil {
		return nil, err
	}
	// No host observation on the commit re-decide, deliberately: a reap is
	// applied as a final inside prepare, so the only decisions that reach here
	// are spawn/setup candidates — which exist because the lease was
	// FREE. Supplying an observation here could only ever re-decide a lease
	// nobody holds, and withholding it is the fail-closed direction anyway.
	// The re-decide is the prepared round's Project: the candidate's own
	// Project is bound into its token and re-compared below.
	sel, err := selectFromSpine(ctx, q, nil, prepared.project, prepared.visionObservation())
	if err != nil {
		return nil, err
	}
	rcand := refusalCandidateFor(cand.spawn)

	proj := spawnCandidateProjection(cand.runID, cand.spawn)
	currentMountState, err := loadDispatchMountState(ctx, q, cand.spawn, sel.rec)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(currentMountState, cand.mountState) {
		return commitInertRefusal(ctx, q, prepared, rcand, refusal.CodeStale)
	}
	canonical, err := buildCanonicalPrepareWithIdentity(prepared.identity, attested.deploymentUUID, prepared.requestID, sel.rec, sel.lease, sel.tun, sel.homies, cand.mountState, proj).bytes()
	if err != nil {
		return nil, err
	}
	if preparationToken(canonical) != cand.token {
		return commitInertRefusal(ctx, q, prepared, rcand, refusal.CodeStale)
	}

	// Re-decide with commit's own clock. The helper computes its own truth:
	// byte-identical state must still reselect this candidate, so a doctored
	// frame or a time-flipped decision refuses here rather than committing a
	// consequence nothing selected.
	if sel.action.Kind != dispatch.KindSpawn {
		return commitInertRefusal(ctx, q, prepared, rcand, refusal.CodeCandidateMismatch)
	}
	reselected, err := json.Marshal(spawnCandidateProjection(cand.runID, sel.action.Spawn))
	if err != nil {
		return nil, err
	}
	preparedProj, err := json.Marshal(proj)
	if err != nil {
		return nil, err
	}
	if string(reselected) != string(preparedProj) {
		return commitInertRefusal(ctx, q, prepared, rcand, refusal.CodeCandidateMismatch)
	}

	if attested.refusal != nil {
		key, err := refusalDispatchKey(prepared, *attested.refusal, attested)
		if err != nil {
			return nil, err
		}
		return applyAttestedRefusal(ctx, q, prepared.requestID, rcand, *attested.refusal, key)
	}

	if attested.mountPlan == nil {
		return nil, Domainf("dispatch: a spawn attestation carries no mount plan")
	}
	if !reflect.DeepEqual(attested.mountPlan.Repository, expectedRepositoryPlan(currentMountState, string(cand.spawn.Role))) {
		return commitInertRefusal(ctx, q, prepared, rcand, refusal.CodeStale)
	}
	expectedToolHome, err := expectedPrivateToolHome(currentMountState)
	if err != nil {
		return nil, err
	}
	if (expectedToolHome == "" && attested.mountPlan.ToolHome != nil) ||
		(expectedToolHome != "" && (attested.mountPlan.ToolHome == nil ||
			attested.mountPlan.ToolHome.Canonical != expectedToolHome)) {
		return commitInertRefusal(ctx, q, prepared, rcand, refusal.CodeStale)
	}
	planDigest, err := mountPlanDigest(attested.mountPlan)
	if err != nil {
		return nil, err
	}
	gitIdentityOverride, err := selectedDispatchGitIdentityOverride(currentMountState)
	if err != nil {
		return nil, err
	}
	action := canonicalAction{
		Version:        dispatchActionVersion,
		RequestID:      prepared.requestID,
		Consequence:    "spawn",
		RunID:          cand.runID,
		Role:           string(cand.spawn.Role),
		SubjectID:      cand.spawn.SubjectID,
		RuntimeDigest:  attested.runtimeDigest,
		RoutingDigest:  attested.routingDigest,
		Harness:        attested.route.Harness,
		Binding:        attested.route.Binding,
		RuntimeBinding: privateRuntimeBindingFromSpec(attested.runtimeBinding),
		PlanDigest:     planDigest,
	}
	key, err := deriveDispatchKey(cand.token, action)
	if err != nil {
		return nil, err
	}
	effect, err := applySpawn(ctx, q, sel.now, sel.action.Spawn, sel.tun, attested.route,
		privateRuntimeBindingFromSpec(attested.runtimeBinding),
		cand.runID, attested.mountPlan, gitIdentityOverride, visionCheckFromRecords(sel.rec))
	if errors.Is(err, errEffectOversize) {
		// Refuse to claim or spawn, and write a health event instead of
		// degrading. applySpawn checked before claiming, so there is nothing to
		// undo here — the refusal is the tick's whole terminal action.
		//
		// This is the arm that ends the loop. Health charges the subject's own
		// budget and blocks it at exhaustion, and a blocked proposal leaves
		// proposedPool, so an Arbiter whose brief outgrew the crossing shrinks its
		// own pool over three ticks instead of failing identically forever.
		return commitOversizeRefusal(ctx, q, prepared, rcand, attested)
	}
	if err != nil {
		return nil, err
	}
	if effect["action"] != "spawn" {
		// A connection check stopped the spawn. Nothing was claimed, so the
		// round owes no receipt.
		return effect, nil
	}
	if err := writeAttestedReceipt(ctx, q, prepared.requestID, key, effect,
		"dispatch.spawn", cand.runID, spawnName(sel.action.Spawn)+"."); err != nil {
		return nil, err
	}
	return effect, nil
}

func applyAttestedRefusal(ctx context.Context, q Q, requestID string, cand RefusalCandidate, r refusal.Refusal, key string) (map[string]any, error) {
	effect, receiptWritten, err := applyRefusalWithReceipt(ctx, q, cand, r, key, requestID)
	if err != nil {
		return nil, err
	}
	// receiptWritten is the router's own report, NOT an inference from
	// effect["consequence"]: the health arm rewrites that field to "task_blocked"
	// when the budget exhausts, having already written the row. Keying on the
	// value inserted a second row under the same dispatch_request_id, and the
	// UNIQUE index turned that into an aborted dispatch transaction on every
	// subsequent tick — a permanently wedged board.
	if !receiptWritten && effect["consequence"] != "none" {
		if err := writeAttestedReceipt(ctx, q, requestID, key, effect,
			"dispatch.result", refusalReceiptSubject(cand), ""); err != nil {
			return nil, err
		}
	}
	return effect, nil
}

func refusalReceiptSubject(c RefusalCandidate) any {
	switch c.Kind {
	case RefusalSubjectTask:
		return *c.TaskID
	case RefusalHomie:
		return c.SessionID
	default:
		return nil
	}
}

// commitOversizeRefusal routes the health consequence for an effect the
// deployment cannot record or deliver. Unlike commitInertRefusal's stale arm it
// goes through applyAttestedRefusal, because a health consequence owes both the
// activity row and the request's receipt.
func commitOversizeRefusal(ctx context.Context, q Q, prepared preparedDispatch, rcand RefusalCandidate, attested attestedDispatch) (map[string]any, error) {
	r := refusal.Refusal{
		Code:    refusal.CodeEffectOversize,
		Field:   refusal.FieldNone,
		Summary: refusal.SummaryOversize,
	}
	key, err := refusalDispatchKey(prepared, r, attested)
	if err != nil {
		return nil, err
	}
	return applyAttestedRefusal(ctx, q, prepared.requestID, rcand, r, key)
}

// commitInertRefusal routes a stale-class preflight refusal through the
// consequence router: no durable mutation, terminal refused effect, next
// tick re-decides.
func commitInertRefusal(ctx context.Context, q Q, prepared preparedDispatch, rcand RefusalCandidate, code string) (map[string]any, error) {
	r := refusal.Refusal{Code: code, Field: refusal.FieldNone, Summary: refusal.SummaryMismatch}
	key, err := refusalDispatchKey(prepared, r)
	if err != nil {
		return nil, err
	}
	return applyRefusal(ctx, q, rcand, r, key)
}

// refusalDispatchKey derives the replay fence for a refusal consequence from
// the preparation token and the canonical action naming the refusal — the
// derivation applyRefusal used to take on faith as an input.
func refusalDispatchKey(prepared preparedDispatch, r refusal.Refusal, authority ...attestedDispatch) (string, error) {
	cand := prepared.candidate
	var runtimeDigest, routingDigest string
	if len(authority) != 0 {
		runtimeDigest = authority[0].runtimeDigest
		routingDigest = authority[0].routingDigest
	}
	return deriveDispatchKey(cand.token, canonicalAction{
		Version: dispatchActionVersion, RequestID: prepared.requestID, Consequence: "refusal",
		RunID: cand.runID, Role: string(cand.spawn.Role), SubjectID: cand.spawn.SubjectID,
		RuntimeDigest: runtimeDigest, RoutingDigest: routingDigest,
		Refusal: &canonicalRefusal{
			Code:      r.Code,
			Authority: string(r.Authority),
			Field:     string(r.Field),
			Summary:   string(r.Summary),
			ItemIndex: r.ItemIndex,
		},
	})
}

// refusalCandidateFor names who a pipeline candidate's refusal is about: the
// subject task when there is one to blame, the subjectless shape otherwise.
// Homie candidates arrive with the future wake selector.
func refusalCandidateFor(sp *dispatch.Spawn) RefusalCandidate {
	if sp.SubjectID != nil {
		return RefusalCandidate{Kind: RefusalSubjectTask, TaskID: sp.SubjectID, Project: sp.Project}
	}
	return RefusalCandidate{Kind: RefusalSubjectlessPipeline, Project: sp.Project, PoolIDs: sp.ProposedPool}
}

// requireDeploymentUUID is the first inert precondition, checked before
// selection or mutation on every branch of both helper invocations.
func requireDeploymentUUID(ctx context.Context, q Q, uuid string) error {
	var stored string
	if err := q.QueryRowContext(ctx, `SELECT deployment_uuid FROM meta WHERE id = 1`).Scan(&stored); err != nil {
		return Domainf("read spine deployment identity: %v — restore from backup", err)
	}
	if stored != uuid {
		return Domainf("deployment identity mismatch: MC_HOME mirror %q does not name this spine's deployment %q (run: mc onboard home)", uuid, stored)
	}
	return nil
}

// readDeploymentMirrorStrict reads MC_HOME's identity mirror the strict way
// dispatch requires: a fixed non-symlink regular file, opened no-follow,
// bounded.
// onboard's readDeploymentMirror tolerates absence because provisioning is
// its job; dispatch never provisions, so an unonboarded or foreign MC_HOME
// refuses before any spine read.
func readDeploymentMirrorStrict(home string) (string, error) {
	path := filepath.Join(home, deploymentUUIDFilename)
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", Domainf("read deployment identity mirror %q: %v (run: mc onboard home)", path, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", Domainf("read deployment identity mirror %q: %v (run: mc onboard home)", path, err)
	}
	if !fi.Mode().IsRegular() {
		return "", Domainf("deployment identity mirror %q must be a regular file", path)
	}
	b, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return "", Domainf("read deployment identity mirror %q: %v", path, err)
	}
	uuid := strings.TrimSpace(string(b))
	if uuid == "" {
		return "", Domainf("deployment identity mirror %q is empty — restore from backup", path)
	}
	return uuid, nil
}

// ResolveDispatchDeployment is the host broker's pre-handshake identity
// read. It deliberately reuses the same strict MC_HOME and no-follow mirror
// seam as the attest step; the broker must not invent a second interpretation
// of deployment identity.
func ResolveDispatchDeployment() (string, error) {
	home, err := resolveMCHome()
	if err != nil {
		return "", err
	}
	return readDeploymentMirrorStrict(home)
}

// ResolveDispatchHome exposes only the already-validated absolute host root
// to the Darwin broker's attest step. Private helpers never call it and never
// receive raw host paths or file bytes.
func ResolveDispatchHome() (string, error) {
	return resolveMCHome()
}

// lookupDispatchReceipt is the replay fence's read half. An unreadable
// stored result is a protocol error, never a green light to re-execute.
func lookupDispatchReceipt(ctx context.Context, q Q, requestID string) (map[string]any, bool, error) {
	var stored string
	err := q.QueryRowContext(ctx,
		`SELECT dispatch_result FROM activity WHERE dispatch_request_id = ?`, requestID).Scan(&stored)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stored), &result); err != nil {
		return nil, false, Domainf("stored dispatch result for request %s is unreadable: %v — restore from backup", requestID, err)
	}
	// A receipt that could not hold the whole effect cannot answer a replay:
	// handing the resident a spawn with no brief would launch an agent with no
	// instructions, and effects.ts refuses that fail-closed anyway. Refuse here
	// instead, so the reason is stated rather than inferred from a silent
	// no-spawn. Nothing is lost — the run holds the lease, the spawn-watchdog
	// reaps it on the ordinary grace, and the next tick re-decides.
	if _, elided := result[receiptElidedKey]; elided {
		return nil, false, Domainf(
			"stored dispatch result for request %s omitted %v because the effect exceeded the receipt column; not replaying a spawn without its brief",
			requestID, result[receiptElidedKey])
	}
	return result, true, nil
}

// receiptElidedKey marks a stored receipt that could not hold the whole effect.
// Its presence is the replay path's refusal signal, and it is deliberately a key
// the effect itself can never carry (applySpawn builds a closed map).
const receiptElidedKey = "receipt_elided"

// regenerableEffectBodies are the two effect members that are large, are not
// decisions, and exist elsewhere: the brief is materialize-at-spawn conveyance
// (the database is the source of truth and the file is only the conveyance)
// and reaches the harness verbatim in the run's native session file, kept
// forever; the shipped skill set is a release artifact, identical for every
// run of that role on this build.
var regenerableEffectBodies = []string{"brief", "skills"}

// storableReceipt is what the receipt column actually holds.
//
// It is the whole effect whenever the whole effect fits, so the replay fence
// keeps working byte-for-byte on every dispatch this deployment has ever made.
// Only when the effect cannot fit does it fall back to a lean form: the same
// object minus the two regenerable bodies, plus a marker saying so.
//
// The fallback is deliberately NOT the general case. A receipt that always
// elided would silently disable lost-response replay for spawns, trading a rare
// failure for a permanent capability loss; this way replay degrades exactly and
// only in the case that used to stop the board outright.
func storableReceipt(effect map[string]any) (string, error) {
	body, err := json.Marshal(effect)
	if err != nil {
		return "", err
	}
	if len(body) <= maxReceiptBytes {
		return string(body), nil
	}
	lean := make(map[string]any, len(effect)+1)
	for k, v := range effect {
		lean[k] = v
	}
	elided := make([]string, 0, len(regenerableEffectBodies))
	for _, k := range regenerableEffectBodies {
		if _, carried := lean[k]; carried {
			delete(lean, k)
			elided = append(elided, k)
		}
	}
	lean[receiptElidedKey] = elided
	leanBody, err := json.Marshal(lean)
	if err != nil {
		return "", err
	}
	if len(leanBody) > maxReceiptBytes {
		// Nothing left to drop that is not a decision. applySpawn's oversize
		// refusal is the arm for this, and it runs before any claim — reaching
		// here means an effect got past it, so refuse rather than truncate.
		return "", Domainf("dispatch receipt exceeds %d bytes even without its regenerable bodies", maxReceiptBytes)
	}
	return string(leanBody), nil
}

func writeDispatchReceipt(ctx context.Context, q Q, requestID string, effect map[string]any) error {
	body, err := storableReceipt(effect)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `
		INSERT INTO activity (actor, kind, subject, detail, dispatch_request_id, dispatch_result)
		VALUES ('dispatch', 'dispatch.result', NULL, NULL, ?, ?)`, requestID, body)
	return err
}

// writeAttestedReceipt closes both replay dimensions on one atomic consequence:
// dispatch_key is the candidate/action digest fence, while requestID/result is
// the command's exact lost-response replay. Health writes these fields directly
// on its action row because activity is append-only; spawn, task-block, and
// Homie-end consequences use this receipt row. An empty detail stores NULL.
func writeAttestedReceipt(ctx context.Context, q Q, requestID, key string, effect map[string]any, fallbackKind string, subject any, detail string) error {
	body, err := storableReceipt(effect)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `
		INSERT INTO activity
			(actor, kind, subject, detail, dispatch_key, dispatch_request_id, dispatch_result)
		VALUES ('dispatch', ?, ?, NULLIF(?, ''), ?, ?, ?)`, fallbackKind, subject, detail, key, requestID, body)
	return err
}

func visionCheckFromRecords(rec dispatch.Records) *ProjectVisionCheck {
	state := ProjectVisionUnknown
	if rec.VisionReady {
		state = ProjectVisionPresent
	}
	return &ProjectVisionCheck{State: state, Ready: rec.VisionReady, Path: rec.VisionPath, Content: rec.VisionContent}
}

func (p preparedDispatch) visionObservation() *ProjectVisionCheck {
	if p.visionObserved {
		return p.visionCheck
	}
	return nil
}
