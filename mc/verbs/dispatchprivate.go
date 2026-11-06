package verbs

// The dispatch seam's private same-binary carrier. These structs are the
// closed, map-free frames between the Darwin broker and the Linux helper.
// RawMessage
// appears only for an already-final ordinary effect object; the helper never
// interprets caller-supplied RawMessage during commit.

import (
	"context"
	"database/sql"
	"encoding/json"
	"path"
	"reflect"
	"strings"
	"unicode/utf8"

	"mc/boundary"
	operatorconfig "mc/config"
	"mc/dispatch"
	"mc/refusal"
	"mc/routing"
	"mc/substrate"
)

// The frame bounds every ordinary scalar at 4 KiB. Domain cardinality is
// separately governed at admission and by the 1 MiB outer carrier.
const maxPrivateScalarBytes = 4 * 1024

// GatewayControlVersion identifies the resident control and private frame protocol.
// The resident uses the same value.
const GatewayControlVersion = 4

func DispatchPreparePrivateDB(db *sql.DB, req PrivateDispatchPrepareRequest) (PrivateDispatchPrepareResponse, error) {
	var response PrivateDispatchPrepareResponse
	err := underDispatchLock(db, func(ctx context.Context, q Q) error {
		var err error
		response, err = DispatchPreparePrivate(ctx, q, req)
		return err
	})
	return response, err
}

func DispatchCommitPrivateDB(db *sql.DB, req PrivateDispatchCommitRequest) (PrivateDispatchResult, error) {
	var response PrivateDispatchResult
	err := underDispatchLock(db, func(ctx context.Context, q Q) error {
		var err error
		response, err = DispatchCommitPrivate(ctx, q, req)
		return err
	})
	return response, err
}

func DispatchPlanPrivateDB(db *sql.DB, req PrivateDispatchPlanRequest) (PrivateDispatchPlanResponse, error) {
	var response PrivateDispatchPlanResponse
	err := underDispatchLock(db, func(ctx context.Context, q Q) error {
		var err error
		response, err = DispatchPlanPrivate(ctx, q, req)
		return err
	})
	return response, err
}

// PrivateDispatchPlanRequest is the tick's first private frame: it carries
// the same identity fields as a prepare and asks the helper which Projects
// this tick walks, in order.
type PrivateDispatchPlanRequest struct {
	Version             int    `json:"version"`
	ReleaseBuildID      string `json:"release_build_id"`
	ControlVersion      int    `json:"gateway_control_version"`
	SpineSchemaID       string `json:"spine_schema_id"`
	ConfigSchemaVersion int    `json:"config_schema_version"`
	DeploymentUUID      string `json:"deployment_uuid"`
	DispatchRequestID   string `json:"dispatch_request_id"`
}

// PrivateDispatchPlanResponse answers with dispatch.TickOrder: every active
// Project plus every non-active Project whose lease is held, in id order.
type PrivateDispatchPlanResponse struct {
	Version             int      `json:"version"`
	ReleaseBuildID      string   `json:"release_build_id"`
	ControlVersion      int      `json:"gateway_control_version"`
	SpineSchemaID       string   `json:"spine_schema_id"`
	ConfigSchemaVersion int      `json:"config_schema_version"`
	DeploymentUUID      string   `json:"deployment_uuid"`
	DispatchRequestID   string   `json:"dispatch_request_id"`
	Projects            []string `json:"projects"`
}

func NewPrivateDispatchPlanRequest(releaseBuildID, deploymentUUID string, controlVersion, configVersion int) (PrivateDispatchPlanRequest, error) {
	requestID, err := newDispatchRequestID()
	if err != nil {
		return PrivateDispatchPlanRequest{}, err
	}
	return PrivateDispatchPlanRequest{
		Version: 1, ReleaseBuildID: releaseBuildID,
		ControlVersion: controlVersion, SpineSchemaID: substrate.CurrentSchemaID,
		ConfigSchemaVersion: configVersion, DeploymentUUID: deploymentUUID,
		DispatchRequestID: requestID,
	}, nil
}

// DispatchPlanPrivate reads the tick order under the same deployment
// precondition every private dispatch frame owes. It mutates nothing.
func DispatchPlanPrivate(ctx context.Context, q Q, req PrivateDispatchPlanRequest) (PrivateDispatchPlanResponse, error) {
	if err := validatePrivateIdentity(req.Version, req.ReleaseBuildID, req.ControlVersion,
		req.SpineSchemaID, req.ConfigSchemaVersion, req.DeploymentUUID, req.DispatchRequestID); err != nil {
		return PrivateDispatchPlanResponse{}, err
	}
	if err := requireDeploymentUUID(ctx, q, req.DeploymentUUID); err != nil {
		return PrivateDispatchPlanResponse{}, err
	}
	order, err := loadTickOrder(ctx, q)
	if err != nil {
		return PrivateDispatchPlanResponse{}, err
	}
	if order == nil {
		order = []string{}
	}
	return PrivateDispatchPlanResponse{
		Version: 1, ReleaseBuildID: req.ReleaseBuildID,
		ControlVersion: req.ControlVersion, SpineSchemaID: req.SpineSchemaID,
		ConfigSchemaVersion: req.ConfigSchemaVersion, DeploymentUUID: req.DeploymentUUID,
		DispatchRequestID: req.DispatchRequestID, Projects: order,
	}, nil
}

type PrivateDispatchPrepareRequest struct {
	VisionCheck         *ProjectVisionCheck `json:"vision_check,omitempty"`
	Version             int                 `json:"version"`
	ReleaseBuildID      string              `json:"release_build_id"`
	ControlVersion      int                 `json:"gateway_control_version"`
	SpineSchemaID       string              `json:"spine_schema_id"`
	ConfigSchemaVersion int                 `json:"config_schema_version"`
	DeploymentUUID      string              `json:"deployment_uuid"`
	DispatchRequestID   string              `json:"dispatch_request_id"`
	// Project names the round this prepare evaluates: "" is the Collie
	// stage, a Project id is that Project's pipeline round or wiki round.
	Project string `json:"project"`
	// Round is "" for the Collie stage and a pipeline round, and "wiki" for a
	// wiki round.
	Round string `json:"round,omitempty"`
	// AcceptedCommit is the host observation of the accepted commit of the
	// Project. Only a wiki round carries it.
	AcceptedCommit  string                   `json:"accepted_commit,omitempty"`
	CollieInventory []PrivateCollieContainer `json:"collie_inventory"`
	// PipelineLiveness is the host's observation of whether recently-launched
	// pipeline runs still have containers — the fact the lock domain cannot
	// measure for itself, since only the host holds the container control
	// socket. It closes the lease wedge: a run that dies without a terminal
	// action otherwise holds its Project's lease for its full timeout.
	PipelineLiveness []PrivatePipelineLiveness `json:"pipeline_liveness"`
	// RuntimeCapability is the host-resolved enabled runtime catalog, carried
	// INTO prepare rather than resolved at attest. A pipeline spawn resolves its
	// route between prepare and commit, but the lease-free Collie wake finalizes
	// inside the prepare transaction, so the only place a
	// wake can learn what this deployment can currently run is here. The host
	// re-reads config.toml immediately after prepare and abandons the effect on
	// drift.
	RuntimeCapability PrivateRuntimeCapability `json:"runtime_capability"`
}

// PrivateRuntimeCapability is the closed, non-secret projection of config.toml's
// ENABLED runtime catalog plus the canonical digest that identifies it. An
// empty digest means the host could not resolve a catalog at all; the helper
// then treats every configured capability as absent and launches nothing.
type PrivateRuntimeCapability struct {
	Digest   string                  `json:"digest"`
	Bindings []PrivateRuntimeBinding `json:"bindings"`
}

type PrivateDispatchCandidate struct {
	RunID               string                    `json:"run_id"`
	Project             string                    `json:"project"`
	Role                string                    `json:"role"`
	SubjectID           *int64                    `json:"subject_id"`
	ProposedPool        []int64                   `json:"proposed_pool"`
	DedupeTitles        []string                  `json:"dedupe_titles"`
	Token               string                    `json:"preparation_token"`
	TimeoutMinutes      int                       `json:"timeout_minutes"`
	GraceMinutes        int                       `json:"grace_minutes"`
	HeartbeatIntervalS  int                       `json:"heartbeat_interval_s"`
	SpawnGraceS         int                       `json:"spawn_grace_s"`
	HardDeadlineMinutes int                       `json:"hard_deadline_minutes"`
	MountState          PrivateDispatchMountState `json:"mount_state"`
}

type PrivateDispatchPrepareResponse struct {
	VisionCheck         *ProjectVisionCheck       `json:"vision_check,omitempty"`
	Version             int                       `json:"version"`
	Kind                string                    `json:"kind"`
	ReleaseBuildID      string                    `json:"release_build_id"`
	ControlVersion      int                       `json:"gateway_control_version"`
	SpineSchemaID       string                    `json:"spine_schema_id"`
	ConfigSchemaVersion int                       `json:"config_schema_version"`
	DeploymentUUID      string                    `json:"deployment_uuid"`
	DispatchRequestID   string                    `json:"dispatch_request_id"`
	Final               *json.RawMessage          `json:"final"`
	Candidate           *PrivateDispatchCandidate `json:"candidate"`
	// RuntimeCapabilityDigest is set exactly when the returned final consumed
	// the carried capability (today: a Collie wake). It is the host's drift
	// fence input: the host recomputes the digest immediately and abandons a
	// final resolved against a catalog that has since changed.
	RuntimeCapabilityDigest string `json:"runtime_capability_digest"`
}

type PrivateDispatchRefusal struct {
	Code      string `json:"code"`
	Authority string `json:"authority"`
	Field     string `json:"field"`
	Summary   string `json:"summary"`
	ItemIndex *int   `json:"item_index"`
}

type PrivateDispatchAttestation struct {
	RuntimeDigest  string                    `json:"runtime_digest"`
	RoutingDigest  string                    `json:"routing_digest"`
	Harness        string                    `json:"harness"`
	Binding        string                    `json:"binding"`
	RuntimeBinding PrivateRuntimeBinding     `json:"runtime_binding"`
	MountPlan      *PrivateDispatchMountPlan `json:"mount_plan"`
	Refusal        *PrivateDispatchRefusal   `json:"refusal"`
}

// PrivateRuntimeBinding is the closed, non-secret runtime capability selected
// by the host from the digest-covered config catalog. The helper validates its
// shape and route coherence but never re-resolves catalog membership.
type PrivateRuntimeBinding struct {
	AuthorizationID        string   `json:"authorization_id,omitempty"`
	ProfileRevision        string   `json:"profile_revision,omitempty"`
	ID                     string   `json:"id"`
	Harness                string   `json:"harness"`
	Model                  string   `json:"model"`
	Effort                 string   `json:"effort,omitempty"`
	Channel                string   `json:"channel"`
	Credential             string   `json:"credential"`
	AuthKind               string   `json:"auth_kind"`
	CatalogEntry           string   `json:"catalog_entry,omitempty"`
	Delivery               string   `json:"delivery"`
	ProviderCredentialKeys []string `json:"provider_credential_keys"`
	DeclaredStaticKey      string   `json:"declared_static_key"`
	BaseURL                string   `json:"base_url"`
	TokenURL               string   `json:"token_url"`
	ClientID               string   `json:"client_id"`
}

type PrivateDispatchCommitRequest struct {
	VisionCheck         *ProjectVisionCheck `json:"vision_check,omitempty"`
	Version             int                 `json:"version"`
	ReleaseBuildID      string              `json:"release_build_id"`
	ControlVersion      int                 `json:"gateway_control_version"`
	SpineSchemaID       string              `json:"spine_schema_id"`
	ConfigSchemaVersion int                 `json:"config_schema_version"`
	DeploymentUUID      string              `json:"deployment_uuid"`
	DispatchRequestID   string              `json:"dispatch_request_id"`
	// Project is the round's Project — the prepared candidate's own,
	// re-validated against it by the helper.
	Project string `json:"project"`
	// Round and AcceptedCommit have the rules of the prepare request. The
	// host reads AcceptedCommit again for this frame.
	Round          string                     `json:"round,omitempty"`
	AcceptedCommit string                     `json:"accepted_commit,omitempty"`
	Candidate      PrivateDispatchCandidate   `json:"candidate"`
	Attestation    PrivateDispatchAttestation `json:"attestation"`
}

type PrivateDispatchResult struct {
	Version             int             `json:"version"`
	ReleaseBuildID      string          `json:"release_build_id"`
	ControlVersion      int             `json:"gateway_control_version"`
	SpineSchemaID       string          `json:"spine_schema_id"`
	ConfigSchemaVersion int             `json:"config_schema_version"`
	DeploymentUUID      string          `json:"deployment_uuid"`
	DispatchRequestID   string          `json:"dispatch_request_id"`
	Result              json.RawMessage `json:"result"`
}

func NewPrivateDispatchPrepareRequest(
	releaseBuildID, deploymentUUID string,
	controlVersion, configVersion int,
	project string,
	collieInventory []PrivateCollieContainer,
	pipelineLiveness []PrivatePipelineLiveness,
	capability PrivateRuntimeCapability,
) (PrivateDispatchPrepareRequest, error) {
	requestID, err := newDispatchRequestID()
	if err != nil {
		return PrivateDispatchPrepareRequest{}, err
	}
	if err := validatePrivateRoundProject(project); err != nil {
		return PrivateDispatchPrepareRequest{}, err
	}
	if err := ValidatePrivateCollieInventory(collieInventory); err != nil {
		return PrivateDispatchPrepareRequest{}, err
	}
	if err := ValidatePrivatePipelineLiveness(pipelineLiveness); err != nil {
		return PrivateDispatchPrepareRequest{}, err
	}
	if err := validatePrivateRuntimeCapability(capability); err != nil {
		return PrivateDispatchPrepareRequest{}, err
	}
	return PrivateDispatchPrepareRequest{
		Version: 1, ReleaseBuildID: releaseBuildID,
		ControlVersion: controlVersion, SpineSchemaID: substrate.CurrentSchemaID,
		ConfigSchemaVersion: configVersion, DeploymentUUID: deploymentUUID,
		DispatchRequestID: requestID,
		Project:           project,
		CollieInventory:   collieInventory,
		PipelineLiveness:  pipelineLiveness,
		RuntimeCapability: capability,
	}, nil
}

// validatePrivateRoundProject admits the Collie stage ("") or a bounded
// structural Project id. Whether the id names a Project of this spine
// is the helper's reload to decide, inside its own transaction.
func validatePrivateRoundProject(project string) error {
	if project == "" {
		return nil
	}
	if !validStructuralText(project, maxPrivateScalarBytes) {
		return Domainf("dispatch: private round project is invalid")
	}
	return nil
}

// validatePrivateRound admits the round name of a private frame. Only a wiki
// round can carry an accepted commit, and it always names a Project.
func validatePrivateRound(round, project, acceptedCommit string) error {
	switch round {
	case "":
		if acceptedCommit != "" {
			return Domainf("dispatch: private accepted commit has no wiki round")
		}
	case PrivateWikiRound:
		if project == "" {
			return Domainf("dispatch: private wiki round names no Project")
		}
		if acceptedCommit != "" && !validLowercaseHex(acceptedCommit, 40) && !validLowercaseHex(acceptedCommit, 64) {
			return Domainf("dispatch: private accepted commit is invalid")
		}
	default:
		return Domainf("dispatch: private round is unknown")
	}
	return nil
}

// validatePrivateRuntimeCapability re-validates the carrier at the helper
// boundary. The catalog is either wholly absent (empty digest, no bindings) or
// digest-covered, strictly ordered by unique id, and structurally coherent
// binding by binding — the same per-binding check the attested spawn route
// owes. Membership stays the host's answer; the helper only refuses shapes it
// could not have produced.
func validatePrivateRuntimeCapability(capability PrivateRuntimeCapability) error {
	if capability.Bindings == nil {
		return Domainf("dispatch: private runtime capability carries no explicit binding list")
	}
	if capability.Digest == "" {
		if len(capability.Bindings) != 0 {
			return Domainf("dispatch: private runtime capability has bindings without a catalog digest")
		}
		return nil
	}
	if !validLowercaseHex(capability.Digest, 64) {
		return Domainf("dispatch: private runtime capability digest is malformed")
	}
	for i, binding := range capability.Bindings {
		if i > 0 && capability.Bindings[i-1].ID >= binding.ID {
			return Domainf("dispatch: private runtime capability is not strictly ordered by binding id")
		}
		if !validStructuralText(binding.ID, 4096) || !validStructuralText(binding.Harness, 4096) {
			return Domainf("dispatch: private runtime capability names a malformed binding")
		}
		if err := validatePrivateRuntimeBindingShape(binding, binding.Harness, binding.ID); err != nil {
			return err
		}
	}
	return nil
}

func DispatchPreparePrivate(ctx context.Context, q Q, req PrivateDispatchPrepareRequest) (PrivateDispatchPrepareResponse, error) {
	if err := validatePrivateIdentity(req.Version, req.ReleaseBuildID, req.ControlVersion,
		req.SpineSchemaID, req.ConfigSchemaVersion, req.DeploymentUUID, req.DispatchRequestID); err != nil {
		return PrivateDispatchPrepareResponse{}, err
	}
	if err := ValidatePrivateCollieInventory(req.CollieInventory); err != nil {
		return PrivateDispatchPrepareResponse{}, err
	}
	if err := ValidatePrivatePipelineLiveness(req.PipelineLiveness); err != nil {
		return PrivateDispatchPrepareResponse{}, err
	}
	if err := validatePrivateRuntimeCapability(req.RuntimeCapability); err != nil {
		return PrivateDispatchPrepareResponse{}, err
	}
	if err := validatePrivateRoundProject(req.Project); err != nil {
		return PrivateDispatchPrepareResponse{}, err
	}
	if err := validatePrivateRound(req.Round, req.Project, req.AcceptedCommit); err != nil {
		return PrivateDispatchPrepareResponse{}, err
	}
	identity := privateIdentity(req.ReleaseBuildID, req.ControlVersion, req.SpineSchemaID, req.ConfigSchemaVersion)
	var prepared preparedDispatch
	var err error
	if req.Round == PrivateWikiRound {
		prepared, err = wikiPrepare(ctx, q, identity, req.DeploymentUUID, req.DispatchRequestID,
			req.PipelineLiveness, req.RuntimeCapability, req.Project, req.AcceptedCommit)
	} else {
		prepared, err = dispatchPrepareWithIdentityInventory(ctx, q, identity, req.DeploymentUUID, req.DispatchRequestID, req.CollieInventory,
			req.PipelineLiveness, req.RuntimeCapability, req.Project, privateVisionCheck(req.VisionCheck))
	}
	if err != nil {
		return PrivateDispatchPrepareResponse{}, err
	}
	response := PrivateDispatchPrepareResponse{
		Version: 1, ReleaseBuildID: req.ReleaseBuildID,
		ControlVersion: req.ControlVersion, SpineSchemaID: req.SpineSchemaID,
		ConfigSchemaVersion: req.ConfigSchemaVersion, DeploymentUUID: req.DeploymentUUID,
		DispatchRequestID: req.DispatchRequestID,
		VisionCheck:       prepared.visionCheck,
	}
	if prepared.final != nil {
		final, err := json.Marshal(prepared.final)
		if err != nil {
			return PrivateDispatchPrepareResponse{}, err
		}
		rawFinal := json.RawMessage(final)
		response.Kind = "final"
		response.Final = &rawFinal
		response.RuntimeCapabilityDigest = prepared.capabilityDigest
		return response, nil
	}
	response.Kind = "candidate"
	response.Candidate = privateCandidateFromPrepared(prepared.candidate)
	return response, nil
}

func DispatchAttestPrivate(home string, prepared PrivateDispatchPrepareResponse) (PrivateDispatchCommitRequest, error) {
	internal, err := preparedFromPrivate(prepared)
	if err != nil {
		return PrivateDispatchCommitRequest{}, err
	}
	if prepared.Kind != "candidate" {
		return PrivateDispatchCommitRequest{}, Domainf("dispatch: only a candidate private frame may attest")
	}
	attested, err := dispatchAttest(home, internal)
	if err != nil {
		return PrivateDispatchCommitRequest{}, err
	}
	req := PrivateDispatchCommitRequest{
		Version: 1, ReleaseBuildID: prepared.ReleaseBuildID,
		ControlVersion: prepared.ControlVersion, SpineSchemaID: prepared.SpineSchemaID,
		ConfigSchemaVersion: prepared.ConfigSchemaVersion, DeploymentUUID: prepared.DeploymentUUID,
		DispatchRequestID: prepared.DispatchRequestID,
		Attestation:       canonicalPrivateAttestation(attested),
		VisionCheck:       prepared.VisionCheck,
		Candidate:         *prepared.Candidate,
		Project:           prepared.Candidate.Project,
	}
	return req, nil
}

// DispatchRecheckPrivate performs the second host-file read immediately
// before __dispatch-commit. Drift becomes a closed stale attestation; the
// helper then redecides under a fresh lock and applies no consequence.
func DispatchRecheckPrivate(home string, prepared PrivateDispatchPrepareResponse, commit PrivateDispatchCommitRequest) PrivateDispatchCommitRequest {
	internal, err := preparedFromPrivate(prepared)
	if err != nil {
		commit.Attestation = stalePrivateAttestation()
		return commit
	}
	first := attestedFromPrivate(commit.Attestation, prepared.DeploymentUUID)
	commit.Attestation = canonicalPrivateAttestation(dispatchRecheckAttestation(home, internal, first))
	return commit
}

func canonicalPrivateAttestation(attested attestedDispatch) PrivateDispatchAttestation {
	frame := PrivateDispatchAttestation{
		RuntimeDigest: attested.runtimeDigest,
		RoutingDigest: attested.routingDigest,
		Harness:       attested.route.Harness, Binding: attested.route.Binding,
		RuntimeBinding: privateRuntimeBindingFromSpec(attested.runtimeBinding),
		MountPlan:      attested.mountPlan,
	}
	if attested.refusal != nil {
		frame.Refusal = &PrivateDispatchRefusal{
			Code: attested.refusal.Code, Authority: string(attested.refusal.Authority),
			Field: string(attested.refusal.Field), Summary: string(attested.refusal.Summary),
			ItemIndex: attested.refusal.ItemIndex,
		}
	}
	return frame
}

func attestedFromPrivate(frame PrivateDispatchAttestation, deploymentUUID string) attestedDispatch {
	attested := attestedDispatch{
		deploymentUUID: deploymentUUID,
		route:          routing.Route{Harness: frame.Harness, Binding: frame.Binding},
		runtimeDigest:  frame.RuntimeDigest,
		routingDigest:  frame.RoutingDigest,
		runtimeBinding: bindingSpecFromPrivate(frame.RuntimeBinding),
		mountPlan:      frame.MountPlan,
	}
	if frame.Refusal != nil {
		r := frame.Refusal
		attested.refusal = &refusal.Refusal{
			Code: r.Code, Authority: refusal.Authority(r.Authority), Field: refusal.Field(r.Field),
			Summary: refusal.Summary(r.Summary), ItemIndex: r.ItemIndex,
		}
	}
	return attested
}

func stalePrivateAttestation() PrivateDispatchAttestation {
	return PrivateDispatchAttestation{
		RuntimeBinding: emptyPrivateRuntimeBinding(),
		Refusal: &PrivateDispatchRefusal{
			Code:    refusal.CodeStale,
			Field:   string(refusal.FieldNone),
			Summary: string(refusal.SummaryMismatch),
		},
	}
}

func DispatchCommitPrivate(ctx context.Context, q Q, req PrivateDispatchCommitRequest) (PrivateDispatchResult, error) {
	if err := validatePrivateIdentity(req.Version, req.ReleaseBuildID, req.ControlVersion,
		req.SpineSchemaID, req.ConfigSchemaVersion, req.DeploymentUUID, req.DispatchRequestID); err != nil {
		return PrivateDispatchResult{}, err
	}
	if err := validatePrivateRound(req.Round, req.Project, req.AcceptedCommit); err != nil {
		return PrivateDispatchResult{}, err
	}
	identity := privateIdentity(req.ReleaseBuildID, req.ControlVersion, req.SpineSchemaID, req.ConfigSchemaVersion)
	prepared, err := preparedFromCandidate(identity, req.DeploymentUUID, req.DispatchRequestID, &req.Candidate)
	if err != nil {
		return PrivateDispatchResult{}, err
	}
	if err := validatePrivateAttestation(req.Attestation); err != nil {
		return PrivateDispatchResult{}, err
	}
	if req.Attestation.Refusal == nil {
		if err := validatePrivateToolHomeCandidate(prepared.candidate, req.Attestation.MountPlan); err != nil {
			return PrivateDispatchResult{}, err
		}
	}
	// The round's Project is the candidate's own: a frame that commits one
	// Project's candidate under another's round is malformed, never
	// re-decided.
	if req.Project == "" || prepared.project != req.Project {
		return PrivateDispatchResult{}, Domainf("dispatch: private commit project does not name the candidate's round")
	}
	// The round name of the frame and the candidate agree: only a wiki round
	// commits the Scribe run of a wiki update.
	wikiState := wikiMountState(prepared.candidate.mountState)
	if (req.Round == PrivateWikiRound) != wikiState || (wikiState && !wikiCandidate(prepared.candidate)) {
		return PrivateDispatchResult{}, Domainf("dispatch: private commit round does not match the candidate")
	}
	attested := attestedFromPrivate(req.Attestation, req.DeploymentUUID)
	var effect map[string]any
	if wikiState {
		effect, err = wikiCommit(ctx, q, prepared, attested, req.AcceptedCommit)
	} else {
		prepared.visionCheck = privateVisionCheck(req.VisionCheck)
		prepared.visionObserved = true
		effect, err = dispatchCommit(ctx, q, prepared, attested)
	}
	if err != nil {
		return PrivateDispatchResult{}, err
	}
	result, err := json.Marshal(effect)
	if err != nil {
		return PrivateDispatchResult{}, err
	}
	return PrivateDispatchResult{
		Version: 1, ReleaseBuildID: req.ReleaseBuildID,
		ControlVersion: req.ControlVersion, SpineSchemaID: req.SpineSchemaID,
		ConfigSchemaVersion: req.ConfigSchemaVersion, DeploymentUUID: req.DeploymentUUID,
		DispatchRequestID: req.DispatchRequestID, Result: result,
	}, nil
}

func expectedPrivateToolHome(state PrivateDispatchMountState) (string, error) {
	if state.SelectedProject == "" {
		return "", nil
	}
	selected, err := selectedDispatchProject(state)
	if err != nil {
		return "", err
	}
	if selected.ToolHomeDir == "" {
		return "", Domainf("dispatch: selected Project profile carries no synthetic home")
	}
	return selected.ToolHomeDir, nil
}

func validatePrivateToolHomeCandidate(cand *preparedCandidate, plan *PrivateDispatchMountPlan) error {
	if cand == nil || cand.spawn == nil || plan == nil {
		return Domainf("dispatch: private synthetic-home validation has no candidate or plan")
	}
	expected, err := expectedPrivateToolHome(cand.mountState)
	if err != nil {
		return err
	}
	if expected == "" {
		if plan.ToolHome != nil {
			return Domainf("dispatch: private mount plan fabricates a synthetic home")
		}
		return nil
	}
	if plan.ToolHome == nil || plan.ToolHome.Canonical != expected {
		return Domainf("dispatch: private mount plan synthetic home does not match the selected Project profile")
	}
	return nil
}

func validatePrivateAttestation(a PrivateDispatchAttestation) error {
	if a.Refusal != nil {
		hasAuthorityDigest := validLowercaseHex(a.RuntimeDigest, 64) &&
			validLowercaseHex(a.RoutingDigest, 64)
		if (a.RuntimeDigest != "" || a.RoutingDigest != "") && !hasAuthorityDigest {
			return Domainf("dispatch: private refusal authority digest is incomplete")
		}
		if a.Harness != "" || a.Binding != "" ||
			!reflect.DeepEqual(a.RuntimeBinding, emptyPrivateRuntimeBinding()) ||
			a.MountPlan != nil {
			return Domainf("dispatch: private attestation carries both a route and refusal")
		}
		r := a.Refusal
		if _, err := refusal.DetailFor(refusal.Refusal{
			Code: r.Code, Authority: refusal.Authority(r.Authority), Field: refusal.Field(r.Field),
			Summary: refusal.Summary(r.Summary), ItemIndex: r.ItemIndex,
		}); err != nil {
			return Domainf("dispatch: invalid private refusal attestation")
		}
		return nil
	}
	if !validLowercaseHex(a.RuntimeDigest, 64) ||
		!validLowercaseHex(a.RoutingDigest, 64) ||
		!validStructuralText(a.Harness, 4096) ||
		!validStructuralText(a.Binding, 4096) {
		return Domainf("dispatch: private route attestation is incomplete")
	}
	if err := validatePrivateRuntimeBinding(a.RuntimeBinding, a.Harness, a.Binding); err != nil {
		return err
	}
	return validatePrivateMountPlan(a.MountPlan)
}

// emptyPrivateRuntimeBinding is the ONE "this frame carries no route" value.
//
// It is the NORMALIZED projection of an empty spec, not the zero struct: the
// projection normalizes its credential key list to an empty slice, and
// reflect.DeepEqual([]string{}, []string(nil)) is false. Every producer of a
// route-free frame and every validator that requires one must use this same
// value, or a frame the host itself built fails the check that exists to
// describe it. Four call sites once compared against the zero struct; each was
// a whole lane refusing every commit.
func emptyPrivateRuntimeBinding() PrivateRuntimeBinding {
	return privateRuntimeBindingFromSpec(routing.BindingSpec{})
}

func privateRuntimeBindingFromSpec(spec routing.BindingSpec) PrivateRuntimeBinding {
	// The credential key list normalizes nil to empty, the same rule the seam
	// header states for every carrier: absent and empty must never encode
	// differently. A binding with no provider keys (the fake family) would
	// otherwise ship JSON `null` here, and a consumer that requires a list —
	// which the resident now does, because this carrier is its only launch
	// authority — refuses the whole launch over the difference.
	keys := append([]string{}, spec.ProviderCredentialKeys...)
	return PrivateRuntimeBinding{
		AuthorizationID: spec.AuthorizationID, ProfileRevision: spec.ProfileRevision,
		ID: spec.ID, Harness: spec.Harness, Model: spec.Model, Effort: spec.Effort, Channel: spec.Channel,
		Credential: spec.Credential, AuthKind: spec.AuthKind, CatalogEntry: spec.CatalogEntry, Delivery: string(spec.Delivery),
		ProviderCredentialKeys: keys,
		DeclaredStaticKey:      spec.DeclaredStaticKey, BaseURL: spec.BaseURL,
		TokenURL: spec.TokenURL, ClientID: spec.ClientID,
	}
}

// runtimeBindingEffectValue renders the resolved capability the way a STORED
// receipt replays it: struct → JSON → generic map.
//
// This normalization is load-bearing, not cosmetic. A dispatch effect is stored
// as JSON and replayed as map[string]any, so a struct value here would encode in
// declared field order when freshly committed and in sorted key order when
// replayed — two byte-different renderings of identical state, which is exactly
// the trap this repo has now paid for three times. Normalizing once, here, makes
// the live and replayed effects identical by construction rather than by a
// hand-maintained field list that a new field would silently break.
func runtimeBindingEffectValue(binding PrivateRuntimeBinding) (map[string]any, error) {
	body, err := json.Marshal(binding)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func bindingSpecFromPrivate(binding PrivateRuntimeBinding) routing.BindingSpec {
	return routing.BindingSpec{
		AuthorizationID: binding.AuthorizationID, ProfileRevision: binding.ProfileRevision,
		ID: binding.ID, Harness: binding.Harness, Model: binding.Model, Effort: binding.Effort, Channel: binding.Channel,
		Credential: binding.Credential, AuthKind: binding.AuthKind, CatalogEntry: binding.CatalogEntry,
		Delivery:               routing.CredentialDelivery(binding.Delivery),
		ProviderCredentialKeys: append([]string(nil), binding.ProviderCredentialKeys...),
		DeclaredStaticKey:      binding.DeclaredStaticKey, BaseURL: binding.BaseURL,
		TokenURL: binding.TokenURL, ClientID: binding.ClientID,
	}
}

// validatePrivateRuntimeBinding is the SELECTION fence: a binding this build
// cannot serve must never launch. Serviceability lives here and not in the
// carrier loop, because the carrier is the operator's whole enabled catalog —
// refusing all of it over one entry no route selected left a deployment unable
// to dispatch on the bindings it CAN serve.
func validatePrivateRuntimeBinding(binding PrivateRuntimeBinding, harness, id string) error {
	if harness == "fake" {
		if _, allowFake := routing.ActiveRegistry(); !allowFake {
			return Domainf("dispatch: private fake runtime binding is unavailable")
		}
	}
	return validatePrivateRuntimeBindingShape(binding, harness, id)
}

// validatePrivateRuntimeBindingShape is the STRUCTURAL fence every carried
// entry owes, whether or not this build could serve it. A fake entry must still
// be the exact canonical fake spec; a real one must still describe a valid
// config binding. Only "can this build serve it?" is deferred to selection.
func validatePrivateRuntimeBindingShape(binding PrivateRuntimeBinding, harness, id string) error {
	if binding.ID != id || binding.Harness != harness {
		return Domainf("dispatch: private runtime binding does not match its resolved route")
	}
	spec := bindingSpecFromPrivate(binding)
	if harness == "fake" {
		want := routing.BindingSpec{
			ID: "fake", Harness: "fake", Channel: "fake",
			Delivery: routing.ProviderProxy,
		}
		if !reflect.DeepEqual(spec, want) {
			return Domainf("dispatch: private fake runtime binding is malformed")
		}
		return nil
	}
	// The carrier names its credential source. The rebuilt one-binding config
	// keys the source by that name, so the same validation the operator
	// config passes applies to the carried shape.
	source := operatorconfig.CredentialSource{Kind: binding.AuthKind, CatalogEntry: binding.CatalogEntry, TokenURL: binding.TokenURL, ClientID: binding.ClientID}
	cfg := operatorconfig.Default()
	cfg.Runtimes.EnabledBindings = []string{id}
	cfg.Runtimes.Credentials = map[string]operatorconfig.CredentialSource{binding.Credential: source}
	cfg.Runtimes.Bindings = map[string]operatorconfig.RuntimeBinding{id: {
		Harness: harness, Model: binding.Model, Effort: binding.Effort, BaseURL: binding.BaseURL, Credential: binding.Credential,
	}}
	if err := cfg.Validate(); err != nil {
		return Domainf("dispatch: private runtime binding is malformed")
	}
	want := routing.EnabledBindings(cfg.Runtimes)[id]
	want.AuthorizationID = binding.AuthorizationID
	want.ProfileRevision = binding.ProfileRevision
	if !reflect.DeepEqual(spec, want) {
		return Domainf("dispatch: private runtime binding projection is incoherent")
	}
	return nil
}

// validatePrivateMountPlan re-validates the carrier at the helper boundary: a
// route attestation always carries an explicit plan (possibly empty), whose
// entries are bounded, evidence-complete, and strictly ordered by unique
// non-overlapping absolute destinations. The helper never trusts the broker's
// shape.
func validatePrivateMountPlan(plan *PrivateDispatchMountPlan) error {
	if plan == nil || plan.Version != 1 || plan.Entries == nil {
		return Domainf("dispatch: private route attestation carries no explicit mount plan")
	}
	if r := plan.Repository; r != nil {
		// The plan has exactly one subject: a task or a wiki update.
		if (r.TaskID >= 1) == (r.WikiUpdate != "") || !path.IsAbs(r.WorkspaceRoot) || path.Clean(r.WorkspaceRoot) != r.WorkspaceRoot || r.WorkspaceRoot == "/" || r.DestinationBranch == "" || r.MaxTemporaryBytes < 1 || (r.InputCommit != "" && !validLowercaseHex(r.InputCommit, 40) && !validLowercaseHex(r.InputCommit, 64)) {
			return Domainf("the isolated repository plan is invalid")
		}
		if r.WikiUpdate != "" && (!validLowercaseHex(r.WikiUpdate, 16) || r.TaskID != 0 || r.DestinationBranch != "main" || r.InputCommit == "" ||
			r.VersionID != nil) {
			return Domainf("the isolated repository plan is invalid")
		}
	}
	if plan.JurisdictionDigest != "" && !validLowercaseHex(plan.JurisdictionDigest, 64) {
		return Domainf("dispatch: private mount plan jurisdiction digest is invalid")
	}
	if home := plan.ToolHome; home != nil {
		if !validStructuralText(home.Canonical, maxPrivateScalarBytes) ||
			!path.IsAbs(home.Canonical) || path.Clean(home.Canonical) != home.Canonical ||
			!validDecimalText(home.Device) || !validDecimalText(home.Inode) ||
			home.OwnerUID < 0 {
			return Domainf("dispatch: private mount plan synthetic home evidence is invalid")
		}
	}
	if len(plan.Entries) > maxPlanMounts {
		return Domainf("dispatch: private mount plan exceeds %d entries", maxPlanMounts)
	}
	body, err := json.Marshal(plan)
	if err != nil || len(body) > maxDispatchMountPlanBytes {
		return Domainf("dispatch: private mount plan exceeds its byte budget")
	}
	if len(plan.SeedingProjections) > 1 {
		// A pipeline mount plan names one Project: Strategist reads
		// the committed tree of its own Project and of no other.
		return Domainf("dispatch: private plan carries more than one seeding projection")
	}
	for _, step := range plan.SeedingProjections {
		if err := boundary.ValidateTarget(step.ProjectID); err != nil || path.Base(step.ProjectID) != step.ProjectID ||
			step.ProjectionKind != seedingCommittedProjectionKind ||
			step.GitCoverKind != seedingSourceGitCoverKind ||
			(step.ObjectFormat != "sha1" && step.ObjectFormat != "sha256") ||
			!validStructuralText(step.TargetRef, maxPrivateScalarBytes) || step.TargetRef == "" {
			return Domainf("dispatch: private seeding projection descriptor is invalid")
		}
		root := step.WorkspaceRoot
		if !validStructuralText(root.Canonical, maxPrivateScalarBytes) ||
			!path.IsAbs(root.Canonical) || path.Clean(root.Canonical) != root.Canonical ||
			!validDecimalText(root.Device) || !validDecimalText(root.Inode) || root.OwnerUID < 0 {
			return Domainf("dispatch: private seeding projection root evidence is invalid")
		}
	}
	prior := ""
	logicalIDs := map[string]bool{}
	for i, e := range plan.Entries {
		if !validStructuralText(e.LogicalID, maxPrivateScalarBytes) ||
			!validStructuralText(e.Source, maxPrivateScalarBytes) ||
			!validStructuralText(e.Destination, maxPrivateScalarBytes) {
			return Domainf("dispatch: private mount entry %d text is invalid", i)
		}
		if logicalIDs[e.LogicalID] {
			return Domainf("dispatch: private mount logical ids must be unique")
		}
		logicalIDs[e.LogicalID] = true
		if !strings.HasPrefix(e.Source, "/") || path.Clean(e.Destination) != e.Destination ||
			strings.Contains(e.Source, ":") || strings.Contains(e.Destination, ":") {
			return Domainf("dispatch: private mount entry %d path shape is invalid", i)
		}
		// Plan destinations are a closed set: the artifact class namespace on
		// `/mc/artifacts/`. Every other `/mc` bind that the resident composes
		// (run.json, session, spine, outputs, scratch) stays unaddressable, as
		// do /workspace, /app/src, /home/agent and /etc. The Clean check above
		// keeps `/mc/artifacts/../run.json` out of this prefix test.
		if !strings.HasPrefix(e.Destination, "/mc/artifacts/") {
			return Domainf("dispatch: private mount entry %d destination is outside the ordinary namespace", i)
		}
		if e.Kind != "dir" && e.Kind != "file" {
			return Domainf("dispatch: private mount entry %d kind is invalid", i)
		}
		if e.Access != string(boundary.AccessRO) && e.Access != string(boundary.AccessRW) {
			return Domainf("dispatch: private mount entry %d access is invalid", i)
		}
		if !validDecimalText(e.Device) || !validDecimalText(e.Inode) {
			return Domainf("dispatch: private mount entry %d identity evidence is invalid", i)
		}
		if e.OwnerUID < 0 || e.Mode < 0 || e.Mode > 0o777 {
			return Domainf("dispatch: private mount entry %d owner/mode evidence is invalid", i)
		}
		if i > 0 && e.Destination <= prior {
			return Domainf("dispatch: private mount destinations are unsorted or overlapping")
		}
		// Sorted order puts an ancestor before every descendant but NOT
		// adjacent to it ('-' sorts before '/', so a sibling can interleave):
		// the overlap scan must walk every prior entry, not just the last.
		for _, p := range plan.Entries[:i] {
			if strings.HasPrefix(e.Destination, p.Destination+"/") {
				return Domainf("dispatch: private mount destinations are unsorted or overlapping")
			}
		}
		prior = e.Destination
	}
	return nil
}

func validDecimalText(value string) bool {
	if value == "" || len(value) > 20 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func privateCandidateFromPrepared(c *preparedCandidate) *PrivateDispatchCandidate {
	return &PrivateDispatchCandidate{
		RunID: c.runID, Project: c.spawn.Project, Role: string(c.spawn.Role), SubjectID: c.spawn.SubjectID,
		ProposedPool: nonNilInt64s(c.spawn.ProposedPool),
		DedupeTitles: nonNilStrings(c.spawn.DedupeTitles), Token: c.token,
		TimeoutMinutes: c.tun.timeoutMinutes, GraceMinutes: c.tun.graceMinutes,
		HeartbeatIntervalS: c.tun.heartbeatIntervalS, SpawnGraceS: c.tun.spawnGraceS,
		HardDeadlineMinutes: c.tun.hardDeadlineMinutes,
		MountState:          c.mountState,
	}
}

func preparedFromPrivate(frame PrivateDispatchPrepareResponse) (preparedDispatch, error) {
	if err := validatePrivateIdentity(frame.Version, frame.ReleaseBuildID, frame.ControlVersion,
		frame.SpineSchemaID, frame.ConfigSchemaVersion, frame.DeploymentUUID, frame.DispatchRequestID); err != nil {
		return preparedDispatch{}, err
	}
	switch frame.Kind {
	case "candidate":
		if frame.Candidate == nil || frame.Final != nil {
			return preparedDispatch{}, Domainf("dispatch: malformed private candidate response")
		}
		return preparedFromCandidate(
			privateIdentity(frame.ReleaseBuildID, frame.ControlVersion, frame.SpineSchemaID, frame.ConfigSchemaVersion),
			frame.DeploymentUUID, frame.DispatchRequestID, frame.Candidate)
	case "final":
		if frame.Candidate != nil || frame.Final == nil {
			return preparedDispatch{}, Domainf("dispatch: malformed private final response")
		}
		return preparedDispatch{requestID: frame.DispatchRequestID, deploymentUUID: frame.DeploymentUUID,
			identity: privateIdentity(frame.ReleaseBuildID, frame.ControlVersion, frame.SpineSchemaID, frame.ConfigSchemaVersion)}, nil
	default:
		return preparedDispatch{}, Domainf("dispatch: unknown private prepare response kind %q", frame.Kind)
	}
}

func preparedFromCandidate(identity dispatchProtocolIdentity, deploymentUUID, requestID string, c *PrivateDispatchCandidate) (preparedDispatch, error) {
	if c == nil || !validLowercaseHex(c.RunID, 16) || !validLowercaseHex(c.Token, 64) {
		return preparedDispatch{}, Domainf("dispatch: malformed private candidate identity")
	}
	if c.Project == "" || !validStructuralText(c.Project, maxPrivateScalarBytes) {
		return preparedDispatch{}, Domainf("dispatch: private candidate names no Project")
	}
	if c.ProposedPool == nil || c.DedupeTitles == nil {
		return preparedDispatch{}, Domainf("dispatch: private candidate collections must be explicit")
	}
	if err := validatePrivateMountState(c.MountState); err != nil {
		return preparedDispatch{}, err
	}
	if !validPrivateRole(c.Role) {
		return preparedDispatch{}, Domainf("dispatch: private candidate role is invalid")
	}
	if !strictPositiveIDs(c.ProposedPool) {
		return preparedDispatch{}, Domainf("dispatch: private candidate ids are not sorted unique positive values")
	}
	if !validStructuralTexts(c.DedupeTitles, maxPrivateScalarBytes) {
		return preparedDispatch{}, Domainf("dispatch: private candidate structural text is invalid")
	}
	if (c.SubjectID != nil && *c.SubjectID <= 0) || c.TimeoutMinutes <= 0 || c.GraceMinutes < 0 ||
		c.HeartbeatIntervalS <= 0 || c.SpawnGraceS <= 0 || c.HardDeadlineMinutes <= 0 {
		return preparedDispatch{}, Domainf("dispatch: private candidate scalar is outside its bound")
	}
	sp := &dispatch.Spawn{
		Project: c.Project, Role: dispatch.Role(c.Role), SubjectID: c.SubjectID,
		ProposedPool: c.ProposedPool, DedupeTitles: c.DedupeTitles,
	}
	tun := tunables{
		timeoutMinutes: c.TimeoutMinutes, graceMinutes: c.GraceMinutes,
		heartbeatIntervalS: c.HeartbeatIntervalS, spawnGraceS: c.SpawnGraceS,
		hardDeadlineMinutes: c.HardDeadlineMinutes,
	}
	return preparedDispatch{
		requestID: requestID, deploymentUUID: deploymentUUID, project: c.Project,
		identity:  identity,
		candidate: &preparedCandidate{spawn: sp, runID: c.RunID, tun: tun, token: c.Token, mountState: c.MountState},
	}, nil
}

func validatePrivateMountState(state PrivateDispatchMountState) error {
	if state.Projects == nil {
		return Domainf("dispatch: private mount-state Projects must be explicit")
	}
	projection, err := json.Marshal(state.Projects)
	if err != nil || len(projection) > substrate.MaxDispatchMountProjectionBytes {
		return Domainf("dispatch: private Project projection exceeds its admitted byte budget")
	}
	foundSelected := state.SelectedProject == ""
	prior := ""
	for i, ws := range state.Projects {
		if !validStructuralText(ws.ProjectID, maxPrivateScalarBytes) ||
			!validStructuralText(ws.Status, maxPrivateScalarBytes) ||
			(i > 0 && ws.ProjectID <= prior) {
			return Domainf("dispatch: private Project projection is invalid or unsorted")
		}
		prior = ws.ProjectID
		if ws.ProjectID == state.SelectedProject {
			foundSelected = true
		}
		if ws.ArtifactRoots == nil || ws.DeniedPaths == nil ||
			!strictStructuralTexts(ws.ArtifactRoots) ||
			!strictStructuralTexts(ws.DeniedPaths) {
			return Domainf("dispatch: private Project path projection is invalid")
		}
		for _, value := range []string{ws.ProfileID, ws.WorkspaceRoot, ws.ToolHomeDir, ws.RuntimeControlDir} {
			if value != "" && !validStructuralText(value, maxPrivateScalarBytes) {
				return Domainf("dispatch: private Project scalar is invalid")
			}
		}
		if !validProfileGitIdentity(ws.GitIdentityOverride) {
			return Domainf("dispatch: private Project Git identity override is invalid")
		}
		if ws.ProfilePresent && ws.ProfileID == "" {
			return Domainf("dispatch: private Project profile presence is incoherent")
		}
		if !ws.ProfilePresent && (ws.WorkspaceRoot != "" || ws.ToolHomeDir != "" || ws.RuntimeControlDir != "" ||
			ws.GitIdentityOverride != nil ||
			len(ws.ArtifactRoots) != 0 || len(ws.DeniedPaths) != 0) {
			return Domainf("dispatch: absent private Project profile carries state")
		}
	}
	if !foundSelected {
		return Domainf("dispatch: selected Project is absent from the private projection")
	}
	return nil
}

func validProfileGitIdentity(identity *substrate.DispatchGitIdentityOverride) bool {
	if identity == nil {
		return true
	}
	name, email := identity.AuthorName, identity.AuthorEmail
	return name != "" && email != "" &&
		strings.TrimSpace(name) != "" && strings.TrimSpace(email) != "" &&
		strings.Contains(email, "@") &&
		len(name) <= 256 && len(email) <= 256 &&
		!strings.ContainsAny(name, "\x00\r\n") &&
		!strings.ContainsAny(email, "\x00\r\n")
}

type dispatchGitIdentityOverride = substrate.DispatchGitIdentityOverride

func selectedDispatchGitIdentityOverride(state PrivateDispatchMountState) (*dispatchGitIdentityOverride, error) {
	if state.SelectedProject == "" {
		return nil, nil
	}
	selected, err := selectedDispatchProject(state)
	if err != nil {
		return nil, err
	}
	if !validProfileGitIdentity(selected.GitIdentityOverride) {
		return nil, Domainf("dispatch: selected Project Git identity override is invalid")
	}
	if selected.GitIdentityOverride == nil {
		return nil, nil
	}
	return &dispatchGitIdentityOverride{
		AuthorName:  selected.GitIdentityOverride.AuthorName,
		AuthorEmail: selected.GitIdentityOverride.AuthorEmail,
	}, nil
}

func strictStructuralTexts(values []string) bool {
	for i, value := range values {
		if !validStructuralText(value, maxPrivateScalarBytes) || (i > 0 && values[i-1] >= value) {
			return false
		}
	}
	return true
}

func privateIdentity(build string, control int, schema string, config int) dispatchProtocolIdentity {
	return dispatchProtocolIdentity{
		releaseBuildID: build, controlVersion: control,
		spineSchemaID: schema, configSchemaVersion: config,
	}
}

func validatePrivateIdentity(version int, build string, control int, schema string, config int, deployment, request string) error {
	if version != 1 || !validStructuralText(build, maxPrivateScalarBytes) || control != GatewayControlVersion || schema != substrate.CurrentSchemaID || config != 1 {
		return Domainf("dispatch: private frame version identity mismatch")
	}
	if !validStructuralText(deployment, maxPrivateScalarBytes) || !validLowercaseHex(request, 16) {
		return Domainf("dispatch: private frame deployment/request identity is invalid")
	}
	return nil
}

func validPrivateRole(role string) bool {
	switch dispatch.Role(role) {
	case dispatch.RoleArbiter, dispatch.RoleWorker,
		dispatch.RoleVerifier, dispatch.RoleScribe, dispatch.RolePackager,
		dispatch.RoleStrategist:
		return true
	default:
		return false
	}
}

func strictPositiveIDs(ids []int64) bool {
	for i, id := range ids {
		if id <= 0 || (i > 0 && ids[i-1] >= id) {
			return false
		}
	}
	return true
}

func validStructuralTexts(values []string, maxBytes int) bool {
	for _, value := range values {
		if !validStructuralText(value, maxBytes) {
			return false
		}
	}
	return true
}

func validStructuralText(value string, maxBytes int) bool {
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func nonNilInt64s(in []int64) []int64 {
	if in == nil {
		return []int64{}
	}
	return in
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func privateVisionCheck(check *ProjectVisionCheck) *ProjectVisionCheck {
	if check == nil {
		return &ProjectVisionCheck{State: ProjectVisionUnknown}
	}
	return check
}
