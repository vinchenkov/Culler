package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	operatorconfig "mc/config"
	"mc/dispatch"
	"mc/refusal"
	"mc/routing"
	"mc/substrate"
)

func privateRequest(uuid string) PrivateDispatchPrepareRequest {
	return PrivateDispatchPrepareRequest{
		Version:             1,
		ReleaseBuildID:      "development",
		ControlVersion:      GatewayControlVersion,
		SpineSchemaID:       substrate.CurrentSchemaID,
		ConfigSchemaVersion: 1,
		DeploymentUUID:      uuid,
		DispatchRequestID:   dfRequestID,
		Project:             "ws-test",
		CollieInventory:     []PrivateCollieContainer{},
		PipelineLiveness:    []PrivatePipelineLiveness{},
		RuntimeCapability:   shippedCapability(),
	}
}

func readyPrivateRequest(t *testing.T, db *sql.DB) PrivateDispatchPrepareRequest {
	t.Helper()
	req := privateRequest(dfUUID(t, db))
	root := dfStr(t, db, `SELECT workspace_root FROM sandbox_profiles WHERE id='default'`)
	check := CheckProjectVision(root)
	req.VisionCheck = &check
	return req
}

// collieStageRequest is the tick's Collie-stage prepare request: the same
// identity as a pipeline round, with no Project (the Collie rounds are
// deployment-wide and lease-free).
func collieStageRequest(uuid string) PrivateDispatchPrepareRequest {
	req := privateRequest(uuid)
	req.Project = ""
	return req
}

func collieRuntimeProof() *PrivateCollieRuntimeAttestation {
	return &PrivateCollieRuntimeAttestation{
		EnvelopeDigest:    strings.Repeat("e", 64),
		RuntimeGeneration: strings.Repeat("f", 64),
	}
}

func TestPrivateDispatchFramesRoundTripCandidateWithoutHostReadsInPrepare(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	req := readyPrivateRequest(t, db)

	var prepared PrivateDispatchPrepareResponse
	err := inTx(db, func(ctx context.Context, q Q) error {
		var e error
		prepared, e = DispatchPreparePrivate(ctx, q, req)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Kind != "candidate" || prepared.Candidate == nil || prepared.Final != nil {
		t.Fatalf("prepared frame = %+v, want candidate only", prepared)
	}
	if prepared.Candidate.Token == "" || prepared.Candidate.RunID == "" || prepared.Candidate.Role != "arbiter" {
		t.Fatalf("prepared candidate omitted bounded identity: %+v", prepared.Candidate)
	}

	commit, err := DispatchAttestPrivate(os.Getenv("MC_HOME"), prepared)
	if err != nil {
		t.Fatal(err)
	}
	if commit.Candidate.Token != prepared.Candidate.Token ||
		commit.Attestation.RuntimeDigest == "" ||
		commit.Attestation.RoutingDigest == "" {
		t.Fatalf("commit frame did not bind prepare + host attestation: %+v", commit)
	}

	var result PrivateDispatchResult
	err = inTx(db, func(ctx context.Context, q Q) error {
		var e error
		result, e = DispatchCommitPrivate(ctx, q, commit)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	var effect map[string]any
	if err := json.Unmarshal(result.Result, &effect); err != nil {
		t.Fatal(err)
	}
	if effect["action"] != "spawn" || effect["run_id"] != prepared.Candidate.RunID {
		t.Fatalf("private commit result = %v, want prepared spawn", effect)
	}
	if inherited, present := effect["git_identity_override"]; !present || inherited != nil {
		t.Fatalf("default profile Git override = (%v, present=%v), want required explicit null", inherited, present)
	}
}

func TestPrivateDispatchProjectsSelectedProjectGitIdentityOverride(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	dvExec(t, db, `UPDATE sandbox_profiles
		SET git_author_name='Project Bot', git_author_email='bot@example.test'
		WHERE id='default'`)
	var prepared PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		prepared, err = DispatchPreparePrivate(ctx, q, readyPrivateRequest(t, db))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	selected, err := selectedDispatchProject(prepared.Candidate.MountState)
	if err != nil {
		t.Fatal(err)
	}
	if selected.GitIdentityOverride == nil ||
		selected.GitIdentityOverride.AuthorName != "Project Bot" ||
		selected.GitIdentityOverride.AuthorEmail != "bot@example.test" {
		t.Fatalf("frozen profile override = %+v", selected.GitIdentityOverride)
	}
	commit, err := DispatchAttestPrivate(os.Getenv("MC_HOME"), prepared)
	if err != nil {
		t.Fatal(err)
	}
	var result PrivateDispatchResult
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		result, err = DispatchCommitPrivate(ctx, q, commit)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var effect map[string]any
	if err := json.Unmarshal(result.Result, &effect); err != nil {
		t.Fatal(err)
	}
	override, ok := effect["git_identity_override"].(map[string]any)
	if !ok || override["author_name"] != "Project Bot" ||
		override["author_email"] != "bot@example.test" {
		t.Fatalf("committed profile Git override = %#v", effect["git_identity_override"])
	}
}

func TestPrivateDispatchRejectsProjectGitIdentitySubstitutionAfterPrepare(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	dvExec(t, db, `UPDATE sandbox_profiles
		SET git_author_name='Project Bot', git_author_email='bot@example.test'
		WHERE id='default'`)
	var prepared PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		prepared, err = DispatchPreparePrivate(ctx, q, readyPrivateRequest(t, db))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	dvExec(t, db, `UPDATE sandbox_profiles
		SET git_author_name='Substituted Bot', git_author_email='substituted@example.test'
		WHERE id='default'`)
	commit, err := DispatchAttestPrivate(os.Getenv("MC_HOME"), prepared)
	if err != nil {
		t.Fatal(err)
	}
	var result PrivateDispatchResult
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		result, err = DispatchCommitPrivate(ctx, q, commit)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var effect map[string]any
	if err := json.Unmarshal(result.Result, &effect); err != nil {
		t.Fatal(err)
	}
	if effect["action"] != "refused" || effect["code"] != refusal.CodeStale ||
		effect["consequence"] != "none" {
		t.Fatalf("substituted Project Git identity effect = %v, want inert preflight.stale", effect)
	}
	if got := dfInt(t, db, `SELECT COUNT(*) FROM runs`); got != 0 {
		t.Fatalf("substitution refusal still claimed %d runs", got)
	}
}

func TestPrivateDispatchCandidateFreezesSelectedProjectAndEveryProfile(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	dvExec(t, db, `UPDATE sandbox_profiles SET artifact_roots=?, denied_paths=?,
		tool_home_dir=?, runtime_control_dir=? WHERE id='default'`,
		`["/tmp/artifact-b","/tmp/artifact-a"]`, `["/tmp/denied"]`, `/tmp/tool-home`, `/tmp/runtime-control`)
	dvExec(t, db, `INSERT INTO sandbox_profiles (id, workspace_root, artifact_roots,
		denied_paths) VALUES ('other-profile','/tmp/other','[]','[]')`)
	dvExec(t, db, `INSERT INTO projects (id,title,sandbox_profile,status)
		VALUES ('other','Other','other-profile','paused')`)

	prepare := func(requestID string) PrivateDispatchPrepareResponse {
		req := readyPrivateRequest(t, db)
		req.DispatchRequestID = requestID
		var out PrivateDispatchPrepareResponse
		if err := inTx(db, func(ctx context.Context, q Q) error {
			var err error
			out, err = DispatchPreparePrivate(ctx, q, req)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}

	first := prepare("0123456789abcdea")
	if first.Candidate == nil {
		t.Fatal("prepare returned no candidate")
	}
	state := first.Candidate.MountState
	if state.SelectedProject != "ws-test" || len(state.Projects) != 2 {
		t.Fatalf("mount state = %+v, want selected ws-test and every Project", state)
	}
	if got := state.Projects[1]; got.ProjectID != "ws-test" || got.ProfileID != "default" ||
		len(got.ArtifactRoots) != 2 || got.ArtifactRoots[0] != "/tmp/artifact-a" ||
		got.DeniedPaths[0] != "/tmp/denied" ||
		got.ToolHomeDir != "/tmp/tool-home" || got.RuntimeControlDir != "/tmp/runtime-control" {
		t.Fatalf("normalized selected profile = %+v", got)
	}

	dvExec(t, db, `UPDATE sandbox_profiles SET denied_paths='["/tmp/denied-2"]' WHERE id='default'`)
	second := prepare("0123456789abcdeb")
	if second.Candidate.Token == first.Candidate.Token {
		t.Fatal("profile drift did not change the preparation token")
	}
}

func TestPrivateSyntheticHomeMustMatchTheFrozenSelectedProfile(t *testing.T) {
	candidate := &preparedCandidate{
		spawn: &dispatch.Spawn{Role: dispatch.RoleWorker},
		mountState: PrivateDispatchMountState{
			SelectedProject: "primary",
			Projects: []PrivateDispatchProject{{
				ProjectID: "primary", ProfilePresent: true,
				ToolHomeDir: "/srv/mc/state/projects/primary/home",
			}},
		},
	}
	exact := &PrivateDispatchMountPlan{
		Version: 1, Entries: []PrivateDispatchMountEntry{},
		ToolHome: &PrivateDispatchPathIdentity{
			Canonical: "/srv/mc/state/projects/primary/home",
			Device:    "8", Inode: "9", OwnerUID: 501,
		},
	}
	if err := validatePrivateToolHomeCandidate(candidate, exact); err != nil {
		t.Fatalf("exact selected synthetic home refused: %v", err)
	}
	stripped := *exact
	stripped.ToolHome = nil
	if err := validatePrivateToolHomeCandidate(candidate, &stripped); err == nil {
		t.Fatal("stripped selected synthetic home was accepted")
	}
	substituted := *exact
	other := *exact.ToolHome
	other.Canonical = "/Users/operator/.ssh"
	substituted.ToolHome = &other
	if err := validatePrivateToolHomeCandidate(candidate, &substituted); err == nil {
		t.Fatal("substituted synthetic home was accepted")
	}
	unselected := &preparedCandidate{
		spawn:      &dispatch.Spawn{Role: dispatch.RoleStrategist},
		mountState: PrivateDispatchMountState{Projects: []PrivateDispatchProject{}},
	}
	if err := validatePrivateToolHomeCandidate(unselected, exact); err == nil {
		t.Fatal("a candidate without a selected Project fabricated a synthetic home")
	}
	homeless := &preparedCandidate{
		spawn: &dispatch.Spawn{Role: dispatch.RoleWorker},
		mountState: PrivateDispatchMountState{
			SelectedProject: "homeless",
			Projects:        []PrivateDispatchProject{{ProjectID: "homeless"}},
		},
	}
	empty := &PrivateDispatchMountPlan{Version: 1, Entries: []PrivateDispatchMountEntry{}}
	if err := validatePrivateToolHomeCandidate(homeless, empty); err == nil {
		t.Fatal("a profile with no synthetic home was accepted")
	}
}

func TestPrivateDispatchCommitRejectsStrippedOrSubstitutedSyntheticHomeBeforeClaim(t *testing.T) {
	for name, mutate := range map[string]func(*PrivateDispatchMountPlan){
		"stripped": func(plan *PrivateDispatchMountPlan) {
			plan.ToolHome = nil
		},
		"substituted": func(plan *PrivateDispatchMountPlan) {
			substitute := *plan.ToolHome
			substitute.Canonical = "/Users/operator/.ssh"
			plan.ToolHome = &substitute
		},
	} {
		t.Run(name, func(t *testing.T) {
			db := dvSpine(t)
			dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
			var prepared PrivateDispatchPrepareResponse
			if err := inTx(db, func(ctx context.Context, q Q) error {
				var err error
				prepared, err = DispatchPreparePrivate(ctx, q, readyPrivateRequest(t, db))
				return err
			}); err != nil {
				t.Fatal(err)
			}
			commit, err := DispatchAttestPrivate(os.Getenv("MC_HOME"), prepared)
			if err != nil {
				t.Fatal(err)
			}
			if commit.Attestation.MountPlan == nil || commit.Attestation.MountPlan.ToolHome == nil {
				t.Fatalf("host attestation omitted selected synthetic home: %+v", commit.Attestation)
			}
			mutate(commit.Attestation.MountPlan)
			err = inTx(db, func(ctx context.Context, q Q) error {
				_, err := DispatchCommitPrivate(ctx, q, commit)
				return err
			})
			if err == nil {
				t.Fatal("doctored private synthetic-home carrier reached commit")
			}
			if got := dfInt(t, db, `SELECT COUNT(*) FROM runs`); got != 0 {
				t.Fatalf("doctored synthetic-home carrier opened %d runs", got)
			}
			if got := dfStr(t, db, `SELECT status FROM tasks WHERE id=1`); got != "proposed" {
				t.Fatalf("doctored synthetic-home carrier changed task status to %q", got)
			}
		})
	}
}

func TestProjectArchiveRejectsProjectionBeyondAggregateBudget(t *testing.T) {
	db := dvSpine(t)
	roots := make([]string, 0, 64)
	for i := 0; i < 63; i++ {
		prefix := fmt.Sprintf("/%02d", i)
		roots = append(roots, prefix+strings.Repeat("x", 4096-len(prefix)))
	}
	roots = append(roots, "/z")
	toolHome := dfStr(t, db, `SELECT tool_home_dir FROM sandbox_profiles WHERE id='default'`)
	workspaceRoot := dfStr(t, db, `SELECT workspace_root FROM sandbox_profiles WHERE id='default'`)
	row := substrate.DispatchProject{
		ProjectID: "ws-test", Status: "active", ProfilePresent: true,
		ProfileID: "default", WorkspaceRoot: workspaceRoot, ArtifactRoots: roots,
		DeniedPaths: []string{},
		ToolHomeDir: toolHome,
		// The stored profile carries the v12 delivery default; the exact-budget
		// calibration must marshal the same bytes the projection will.
		RuntimeAuthDelivery: "projection",
	}
	body, err := json.Marshal([]substrate.DispatchProject{row})
	if err != nil {
		t.Fatal(err)
	}
	delta := substrate.MaxDispatchMountProjectionBytes - len(body)
	if delta < 0 || delta > 4094 {
		t.Fatalf("exact-budget fixture needs delta %d outside 0..4094", delta)
	}
	roots[len(roots)-1] += strings.Repeat("x", delta)
	artifactJSON, err := json.Marshal(roots)
	if err != nil {
		t.Fatal(err)
	}
	dvExec(t, db, `UPDATE sandbox_profiles SET artifact_roots=? WHERE id='default'`, string(artifactJSON))
	if err := substrate.ValidateDispatchMountProjection(context.Background(), db); err != nil {
		t.Fatalf("exact aggregate budget rejected: %v", err)
	}

	if _, err := ProjectSetStatus(db, nil, "ws-test", "archived"); err == nil {
		t.Fatal("archive admitted a Project projection beyond the aggregate budget")
	}
	if got := dfStr(t, db, `SELECT status FROM projects WHERE id='ws-test'`); got != "active" {
		t.Fatalf("rejected archive left status %q, want transactional rollback to active", got)
	}
}

func TestPrivateDispatchPrepareReturnsFinalWithoutAttest(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject) VALUES ('fresh-run', 'pipeline', 'worker', 'ws-test', NULL)`)
	dvHold(t, db, "ws-test", "fresh-run", "worker", nil, 0, nil, 24*time.Hour)
	prepared, err := func() (PrivateDispatchPrepareResponse, error) {
		var out PrivateDispatchPrepareResponse
		err := inTx(db, func(ctx context.Context, q Q) error {
			var e error
			out, e = DispatchPreparePrivate(ctx, q, readyPrivateRequest(t, db))
			return e
		})
		return out, err
	}()
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Kind != "final" || prepared.Candidate != nil || prepared.Final == nil {
		t.Fatalf("prepared frame = %+v, want final only", prepared)
	}
	var effect map[string]any
	if err := json.Unmarshal(*prepared.Final, &effect); err != nil {
		t.Fatal(err)
	}
	if effect["action"] != "idle" {
		t.Fatalf("unexpected final effect %v, want idle", effect)
	}
}

func TestPrivateDispatchPrepareRemovesCollieOrphanBeforePipelineWork(t *testing.T) {
	db := dvSpine(t)
	req := collieStageRequest(dfUUID(t, db))
	req.CollieInventory = []PrivateCollieContainer{{
		ContainerID: strings.Repeat("a", 64),
		Launch:      strings.Repeat("b", 16),
		Name:        "mc-collie-h-orphan",
		Session:     "h-orphan",
		State:       "running",
	}}

	var prepared PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		prepared, err = DispatchPreparePrivate(ctx, q, req)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if prepared.Kind != "final" || prepared.Final == nil || prepared.Candidate != nil {
		t.Fatalf("prepared frame = %+v, want exact orphan cleanup final", prepared)
	}
	var effect map[string]any
	if err := json.Unmarshal(*prepared.Final, &effect); err != nil {
		t.Fatal(err)
	}
	if effect["action"] != "collie-remove" ||
		effect["session"] != "h-orphan" ||
		effect["launch"] != strings.Repeat("b", 16) ||
		effect["container_id"] != strings.Repeat("a", 64) ||
		effect["container_name"] != "mc-collie-h-orphan" ||
		effect["reason"] != "orphan" {
		t.Fatalf("orphan cleanup effect = %+v", effect)
	}
}

func TestPrivateDispatchPrepareKeepsExactActiveCollieContainer(t *testing.T) {
	db := dvSpine(t)
	hwSession(t, db, "h-live")
	containerID := strings.Repeat("c", 64)
	launchID := strings.Repeat("d", 16)
	dvExec(t, db, `UPDATE collie_sessions
		SET current_launch_id = ?, current_launch_mode = 'fresh',
		    current_container_id = ?, launch_bound_at = datetime('now'),
		    launch_started_at = datetime('now')
		WHERE id = 'h-live'`, launchID, containerID)
	req := readyPrivateRequest(t, db)
	req.CollieInventory = []PrivateCollieContainer{{
		ContainerID: containerID, Launch: launchID, Name: "mc-collie-h-live",
		RuntimeAttestation: collieRuntimeProof(), Session: "h-live", State: "running",
	}}

	var prepared PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		prepared, err = DispatchPreparePrivate(ctx, q, req)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if prepared.Final != nil {
		var effect map[string]any
		if err := json.Unmarshal(*prepared.Final, &effect); err != nil {
			t.Fatal(err)
		}
		if effect["action"] == "collie-remove" {
			t.Fatalf("exact active container was classified as an orphan: %+v", effect)
		}
	}
}

func TestPrivateDispatchPrepareAdoptsOnlyAttestedExactCreatedCollie(t *testing.T) {
	db := dvSpine(t)
	hwSession(t, db, "h-created")
	containerID := strings.Repeat("c", 64)
	launchID := strings.Repeat("d", 16)
	dvExec(t, db, `UPDATE collie_sessions
		SET current_launch_id = ?, current_launch_mode = 'fresh',
		    current_container_id = ?, launch_bound_at = datetime('now'),
		    launch_started_at = NULL
		WHERE id = 'h-created'`, launchID, containerID)
	req := collieStageRequest(dfUUID(t, db))
	req.CollieInventory = []PrivateCollieContainer{{
		ContainerID: containerID, Launch: launchID, Name: "mc-collie-h-created",
		RuntimeAttestation: collieRuntimeProof(), Session: "h-created", State: "created",
	}}

	var prepared PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		prepared, err = DispatchPreparePrivate(ctx, q, req)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var effect map[string]any
	if prepared.Final == nil {
		t.Fatalf("prepared frame = %+v, want exact-created adoption", prepared)
	}
	if err := json.Unmarshal(*prepared.Final, &effect); err != nil {
		t.Fatal(err)
	}
	if effect["action"] != "collie-adopt" ||
		effect["session"] != "h-created" ||
		effect["launch"] != launchID ||
		effect["container_id"] != containerID ||
		effect["container_name"] != "mc-collie-h-created" ||
		effect["envelope_digest"] != strings.Repeat("e", 64) ||
		effect["runtime_generation"] != strings.Repeat("f", 64) {
		t.Fatalf("adoption effect = %+v", effect)
	}
	var started any
	if err := db.QueryRow(`SELECT launch_started_at FROM collie_sessions
		WHERE id = 'h-created'`).Scan(&started); err != nil {
		t.Fatal(err)
	}
	if started != nil {
		t.Fatalf("adoption selection stamped runner-started: %v", started)
	}
}

func TestPrivateDispatchPrepareRemovesUnattestedExactCreatedCollie(t *testing.T) {
	db := dvSpine(t)
	hwSession(t, db, "h-created")
	containerID := strings.Repeat("c", 64)
	launchID := strings.Repeat("d", 16)
	dvExec(t, db, `UPDATE collie_sessions
		SET current_launch_id = ?, current_launch_mode = 'fresh',
		    current_container_id = ?, launch_bound_at = datetime('now')
		WHERE id = 'h-created'`, launchID, containerID)
	req := collieStageRequest(dfUUID(t, db))
	req.CollieInventory = []PrivateCollieContainer{{
		ContainerID: containerID, Launch: launchID, Name: "mc-collie-h-created",
		Session: "h-created", State: "created",
	}}

	var prepared PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		prepared, err = DispatchPreparePrivate(ctx, q, req)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var effect map[string]any
	if prepared.Final == nil {
		t.Fatalf("prepared frame = %+v, want branch-3 cleanup", prepared)
	}
	if err := json.Unmarshal(*prepared.Final, &effect); err != nil {
		t.Fatal(err)
	}
	if effect["action"] != "collie-remove" || effect["reason"] != "orphan" ||
		effect["container_id"] != containerID {
		t.Fatalf("unattested exact-created effect = %+v", effect)
	}
}

func TestPrivateDispatchPrepareRemovesExactTransitionalCollieContainer(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `INSERT INTO activity (actor, kind, created_at)
		VALUES ('strategist', 'daily.briefing', datetime('now'))`)
	hwSession(t, db, "h-paused")
	containerID := strings.Repeat("c", 64)
	launchID := strings.Repeat("d", 16)
	dvExec(t, db, `UPDATE collie_sessions
		SET current_launch_id = ?, current_launch_mode = 'fresh',
		    current_container_id = ?, launch_bound_at = datetime('now'),
		    launch_started_at = datetime('now')
		WHERE id = 'h-paused'`, launchID, containerID)
	req := collieStageRequest(dfUUID(t, db))
	req.CollieInventory = []PrivateCollieContainer{{
		ContainerID: containerID,
		Launch:      launchID,
		Name:        "mc-collie-h-paused",
		Session:     "h-paused",
		State:       "paused",
	}}

	var prepared PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		prepared, err = DispatchPreparePrivate(ctx, q, req)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if prepared.Kind != "final" || prepared.Final == nil || prepared.Candidate != nil {
		t.Fatalf("prepared frame = %+v, want exact transitional cleanup final", prepared)
	}
	var effect map[string]any
	if err := json.Unmarshal(*prepared.Final, &effect); err != nil {
		t.Fatal(err)
	}
	if effect["action"] != "collie-remove" ||
		effect["session"] != "h-paused" ||
		effect["launch"] != launchID ||
		effect["container_id"] != containerID ||
		effect["container_name"] != "mc-collie-h-paused" ||
		effect["reason"] != "paused" {
		t.Fatalf("transitional cleanup effect = %+v", effect)
	}
	var status, gotLaunch, gotContainer string
	hwGet(t, db, `SELECT status, current_launch_id, current_container_id
		FROM collie_sessions WHERE id = 'h-paused'`,
		&status, &gotLaunch, &gotContainer)
	if status != "active" || gotLaunch != launchID || gotContainer != containerID {
		t.Fatalf("cleanup selection mutated spine state: status=%q launch=%q container=%q",
			status, gotLaunch, gotContainer)
	}
}

func TestPrivateDispatchPrepareCollieCleanupOrdersByCaseThenActivity(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `INSERT INTO activity (actor, kind, created_at)
		VALUES ('strategist', 'daily.briefing', datetime('now'))`)
	hwSession(t, db, "h-terminal")
	hwSession(t, db, "h-transitional")
	terminalContainer := strings.Repeat("a", 64)
	transitionalContainer := strings.Repeat("b", 64)
	terminalLaunch := strings.Repeat("c", 16)
	transitionalLaunch := strings.Repeat("d", 16)
	dvExec(t, db, `UPDATE collie_sessions
		SET last_activity_at = datetime('now', '-10 minutes'),
		    current_launch_id = ?, current_launch_mode = 'fresh',
		    current_container_id = ?, launch_bound_at = datetime('now'),
		    launch_started_at = datetime('now')
		WHERE id = 'h-terminal'`, terminalLaunch, terminalContainer)
	dvExec(t, db, `UPDATE collie_sessions
		SET last_activity_at = datetime('now', '-1 minute'),
		    current_launch_id = ?, current_launch_mode = 'fresh',
		    current_container_id = ?, launch_bound_at = datetime('now'),
		    launch_started_at = datetime('now')
		WHERE id = 'h-transitional'`, transitionalLaunch, transitionalContainer)
	req := collieStageRequest(dfUUID(t, db))
	req.CollieInventory = []PrivateCollieContainer{
		{
			ContainerID: terminalContainer,
			Launch:      terminalLaunch,
			Name:        "mc-collie-h-terminal",
			Session:     "h-terminal",
			State:       "dead",
		},
		{
			ContainerID: transitionalContainer,
			Launch:      transitionalLaunch,
			Name:        "mc-collie-h-transitional",
			Session:     "h-transitional",
			State:       "restarting",
		},
	}

	var prepared PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		prepared, err = DispatchPreparePrivate(ctx, q, req)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var effect map[string]any
	if prepared.Final == nil {
		t.Fatalf("prepared frame = %+v, want exact transitional cleanup", prepared)
	}
	if err := json.Unmarshal(*prepared.Final, &effect); err != nil {
		t.Fatal(err)
	}
	if effect["session"] != "h-transitional" || effect["reason"] != "restarting" {
		t.Fatalf("cleanup effect = %+v, want case-rank 0 before older case-rank 1", effect)
	}
}

func TestPrivateDispatchPrepareIdleThresholdWinsForSameCollieContainer(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `INSERT INTO activity (actor, kind, created_at)
		VALUES ('strategist', 'daily.briefing', datetime('now'))`)
	hwSession(t, db, "h-idle-paused")
	containerID := strings.Repeat("e", 64)
	launchID := strings.Repeat("f", 16)
	dvExec(t, db, `UPDATE collie_sessions
		SET last_activity_at = '2000-01-01 00:00:00',
		    current_launch_id = ?, current_launch_mode = 'fresh',
		    current_container_id = ?, launch_bound_at = datetime('now'),
		    launch_started_at = datetime('now')
		WHERE id = 'h-idle-paused'`, launchID, containerID)
	req := collieStageRequest(dfUUID(t, db))
	req.CollieInventory = []PrivateCollieContainer{{
		ContainerID: containerID,
		Launch:      launchID,
		Name:        "mc-collie-h-idle-paused",
		Session:     "h-idle-paused",
		State:       "paused",
	}}

	var prepared PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		prepared, err = DispatchPreparePrivate(ctx, q, req)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var effect map[string]any
	if prepared.Final == nil {
		t.Fatalf("prepared frame = %+v, want idle-end final", prepared)
	}
	if err := json.Unmarshal(*prepared.Final, &effect); err != nil {
		t.Fatal(err)
	}
	if effect["action"] != "collie-stop" || effect["session"] != "h-idle-paused" ||
		effect["container_id"] != containerID || effect["reason"] != "idle" {
		t.Fatalf("idle-threshold effect = %+v, want branch 6 ownership", effect)
	}
	var status string
	hwGet(t, db, `SELECT status FROM collie_sessions WHERE id = 'h-idle-paused'`, &status)
	if status != "ended" {
		t.Fatalf("idle-threshold session status = %q, want ended", status)
	}
}

func TestPrivateDispatchPrepareEndsStartedCollieAfterCleanupConfirmsAbsence(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `INSERT INTO activity (actor, kind, created_at)
		VALUES ('strategist', 'daily.briefing', datetime('now'))`)
	hwSession(t, db, "h-exited")
	containerID := strings.Repeat("a", 64)
	launchID := strings.Repeat("b", 16)
	dvExec(t, db, `UPDATE collie_sessions
		SET current_launch_id = ?, current_launch_mode = 'fresh',
		    current_container_id = ?, launch_bound_at = datetime('now'),
		    launch_started_at = datetime('now')
		WHERE id = 'h-exited'`, launchID, containerID)
	req := collieStageRequest(dfUUID(t, db))
	req.CollieInventory = []PrivateCollieContainer{{
		ContainerID: containerID,
		Launch:      launchID,
		Name:        "mc-collie-h-exited",
		Session:     "h-exited",
		State:       "exited",
	}}

	var cleanup PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		cleanup, err = DispatchPreparePrivate(ctx, q, req)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var cleanupEffect map[string]any
	if cleanup.Final == nil {
		t.Fatalf("cleanup frame = %+v, want exact terminal removal", cleanup)
	}
	if err := json.Unmarshal(*cleanup.Final, &cleanupEffect); err != nil {
		t.Fatal(err)
	}
	if cleanupEffect["action"] != "collie-remove" || cleanupEffect["reason"] != "exited" {
		t.Fatalf("cleanup effect = %+v, want exact exited removal", cleanupEffect)
	}
	var status string
	hwGet(t, db, `SELECT status FROM collie_sessions WHERE id = 'h-exited'`, &status)
	if status != "active" {
		t.Fatalf("first tick status = %q, want active until absence is observed", status)
	}

	req.CollieInventory = []PrivateCollieContainer{}
	var ended PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		ended, err = DispatchPreparePrivate(ctx, q, req)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var endedEffect map[string]any
	if ended.Final == nil {
		t.Fatalf("absence frame = %+v, want started-generation end", ended)
	}
	if err := json.Unmarshal(*ended.Final, &endedEffect); err != nil {
		t.Fatal(err)
	}
	if endedEffect["action"] != "collie-stop" ||
		endedEffect["session"] != "h-exited" ||
		endedEffect["reason"] != "exited" ||
		endedEffect["container_id"] != nil {
		t.Fatalf("absence effect = %+v, want no-container exited stop", endedEffect)
	}
	hwGet(t, db, `SELECT status FROM collie_sessions WHERE id = 'h-exited'`, &status)
	if status != "ended" {
		t.Fatalf("second tick status = %q, want ended", status)
	}
	var actor, detail string
	hwGet(t, db, `SELECT actor, detail FROM activity
		WHERE kind = 'collie.ended' AND subject = 'h-exited'`, &actor, &detail)
	if actor != "dispatch" || detail != "exited" {
		t.Fatalf("end activity actor=%q detail=%q, want dispatch/exited", actor, detail)
	}
}

func TestPrivateDispatchPrepareConfirmsStartedAbsencePerGeneration(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `INSERT INTO activity (actor, kind, created_at)
		VALUES ('strategist', 'daily.briefing', datetime('now'))`)
	hwSession(t, db, "h-absent")
	hwSession(t, db, "h-live")
	absentContainer := strings.Repeat("c", 64)
	liveContainer := strings.Repeat("d", 64)
	absentLaunch := strings.Repeat("e", 16)
	liveLaunch := strings.Repeat("f", 16)
	dvExec(t, db, `UPDATE collie_sessions
		SET current_launch_id = ?, current_launch_mode = 'fresh',
		    current_container_id = ?, launch_bound_at = datetime('now'),
		    launch_started_at = datetime('now')
		WHERE id = 'h-absent'`, absentLaunch, absentContainer)
	dvExec(t, db, `UPDATE collie_sessions
		SET current_launch_id = ?, current_launch_mode = 'fresh',
		    current_container_id = ?, launch_bound_at = datetime('now'),
		    launch_started_at = datetime('now')
		WHERE id = 'h-live'`, liveLaunch, liveContainer)
	req := collieStageRequest(dfUUID(t, db))
	req.CollieInventory = []PrivateCollieContainer{{
		ContainerID: liveContainer, Launch: liveLaunch, Name: "mc-collie-h-live",
		RuntimeAttestation: collieRuntimeProof(), Session: "h-live", State: "running",
	}}

	var prepared PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		prepared, err = DispatchPreparePrivate(ctx, q, req)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var effect map[string]any
	if prepared.Final == nil {
		t.Fatalf("prepared frame = %+v, want per-generation absence end", prepared)
	}
	if err := json.Unmarshal(*prepared.Final, &effect); err != nil {
		t.Fatal(err)
	}
	if effect["action"] != "collie-stop" || effect["session"] != "h-absent" ||
		effect["reason"] != "exited" || effect["container_id"] != nil {
		t.Fatalf("per-generation absence effect = %+v", effect)
	}
	var absentStatus, liveStatus string
	hwGet(t, db, `SELECT status FROM collie_sessions WHERE id = 'h-absent'`, &absentStatus)
	hwGet(t, db, `SELECT status FROM collie_sessions WHERE id = 'h-live'`, &liveStatus)
	if absentStatus != "ended" || liveStatus != "active" {
		t.Fatalf("statuses absent=%q live=%q, want ended/active", absentStatus, liveStatus)
	}
}

func TestPrivateDispatchPrepareRemovesUnstartedRunningCollieAfterSpawnGrace(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `INSERT INTO activity (actor, kind, created_at)
		VALUES ('strategist', 'daily.briefing', datetime('now'))`)
	hwSession(t, db, "h-starting")
	containerID := strings.Repeat("a", 64)
	launchID := strings.Repeat("b", 16)
	dvExec(t, db, `UPDATE collie_sessions
		SET current_launch_id = ?, current_launch_mode = 'fresh',
		    current_container_id = ?, launch_bound_at = '2000-01-01 00:00:00',
		    launch_started_at = NULL
		WHERE id = 'h-starting'`, launchID, containerID)
	req := collieStageRequest(dfUUID(t, db))
	req.CollieInventory = []PrivateCollieContainer{{
		ContainerID: containerID, Launch: launchID, Name: "mc-collie-h-starting",
		RuntimeAttestation: collieRuntimeProof(), Session: "h-starting", State: "running",
	}}

	var prepared PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		prepared, err = DispatchPreparePrivate(ctx, q, req)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var effect map[string]any
	if prepared.Final == nil {
		t.Fatalf("prepared frame = %+v, want starting-timeout cleanup", prepared)
	}
	if err := json.Unmarshal(*prepared.Final, &effect); err != nil {
		t.Fatal(err)
	}
	if effect["action"] != "collie-remove" ||
		effect["session"] != "h-starting" ||
		effect["launch"] != launchID ||
		effect["container_id"] != containerID ||
		effect["reason"] != "starting-timeout" {
		t.Fatalf("starting-timeout effect = %+v", effect)
	}
	var status, gotLaunch, gotContainer string
	hwGet(t, db, `SELECT status, current_launch_id, current_container_id
		FROM collie_sessions WHERE id = 'h-starting'`,
		&status, &gotLaunch, &gotContainer)
	if status != "active" || gotLaunch != launchID || gotContainer != containerID {
		t.Fatalf("starting cleanup mutated spine: status=%q launch=%q container=%q",
			status, gotLaunch, gotContainer)
	}
}

func TestPrivateDispatchPrepareReemitsConfirmedAbsentNativeLaunchDebt(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `INSERT INTO collie_sessions
		(id, container_name, verb_allowlist, session_path, binding)
		VALUES ('h-native', 'mc-collie-h-native', '[]', 'sessions/h-native',
		        'claude-sdk/claude')`)
	launchID := strings.Repeat("e", 16)
	dvExec(t, db, `UPDATE collie_sessions
		SET current_launch_id = ?, current_launch_mode = 'native',
		    native_session_ref = 'native-frozen',
		    trace_filename = 'trace/frozen.jsonl'
		WHERE id = 'h-native'`, launchID)
	req := collieStageRequest(dfUUID(t, db))

	var prepared PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		prepared, err = DispatchPreparePrivate(ctx, q, req)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if prepared.Kind != "final" || prepared.Final == nil || prepared.Candidate != nil {
		t.Fatalf("prepared frame = %+v, want frozen native wake final", prepared)
	}
	var effect map[string]any
	if err := json.Unmarshal(*prepared.Final, &effect); err != nil {
		t.Fatal(err)
	}
	if effect["action"] != "collie-wake" ||
		effect["session"] != "h-native" ||
		effect["launch"] != launchID ||
		effect["mode"] != "native" ||
		effect["binding"] != "claude-sdk/claude" ||
		effect["native_session_ref"] != "native-frozen" ||
		effect["trace_filename"] != "trace/frozen.jsonl" {
		t.Fatalf("unbound native launch was not re-emitted exactly: %+v", effect)
	}
	var gotLaunch string
	var resumeOwed int
	hwGet(t, db, `SELECT current_launch_id, resume_owed FROM collie_sessions
		WHERE id = 'h-native'`, &gotLaunch, &resumeOwed)
	if gotLaunch != launchID || resumeOwed != 0 {
		t.Fatalf("re-emission mutated frozen launch debt: launch=%q resume_owed=%d", gotLaunch, resumeOwed)
	}
}

func TestPrivateDispatchPrepareDoesNotReadHostRouting(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	if err := os.Remove(filepath.Join(os.Getenv("MC_HOME"), "routing.md")); err != nil {
		t.Fatal(err)
	}
	var prepared PrivateDispatchPrepareResponse
	err := inTx(db, func(ctx context.Context, q Q) error {
		var e error
		prepared, e = DispatchPreparePrivate(ctx, q, readyPrivateRequest(t, db))
		return e
	})
	if err != nil {
		t.Fatalf("lock-domain prepare touched missing host routing: %v", err)
	}
	if prepared.Kind != "candidate" || prepared.Candidate == nil {
		t.Fatalf("prepare = %+v, want candidate independent of routing.md", prepared)
	}
	attested, err := DispatchAttestPrivate(os.Getenv("MC_HOME"), prepared)
	if err != nil {
		t.Fatal(err)
	}
	if attested.Attestation.Refusal == nil || attested.Attestation.Refusal.Code != "health.routing_invalid" {
		t.Fatalf("host attest = %+v, want routing health refusal", attested.Attestation)
	}
}

func TestPrivateDispatchPrepareDoesNotReadHostRuntimeConfig(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	if err := os.Remove(filepath.Join(os.Getenv("MC_HOME"), "config.toml")); err != nil {
		t.Fatal(err)
	}
	var prepared PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		prepared, err = DispatchPreparePrivate(ctx, q, readyPrivateRequest(t, db))
		return err
	}); err != nil {
		t.Fatalf("lock-domain prepare touched missing host config: %v", err)
	}
	if prepared.Kind != "candidate" || prepared.Candidate == nil {
		t.Fatalf("prepare = %+v, want candidate independent of config.toml", prepared)
	}
	attested, err := DispatchAttestPrivate(os.Getenv("MC_HOME"), prepared)
	if err != nil {
		t.Fatal(err)
	}
	if attested.Attestation.Refusal == nil ||
		attested.Attestation.Refusal.Code != refusal.CodeConfigInvalid {
		t.Fatalf("host attest = %+v, want config health refusal", attested.Attestation)
	}
}

func TestPrivateDispatchRechecksHostFilesImmediatelyBeforeCommit(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	prepared := func() PrivateDispatchPrepareResponse {
		var out PrivateDispatchPrepareResponse
		if err := inTx(db, func(ctx context.Context, q Q) error {
			var err error
			out, err = DispatchPreparePrivate(ctx, q, readyPrivateRequest(t, db))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}()
	home := os.Getenv("MC_HOME")
	commit, err := DispatchAttestPrivate(home, prepared)
	if err != nil {
		t.Fatal(err)
	}
	routingPath := filepath.Join(home, "routing.md")
	raw, err := os.ReadFile(routingPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(routingPath, append(raw, []byte("\n# concurrent operator edit\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	commit = DispatchRecheckPrivate(home, prepared, commit)
	if commit.Attestation.Refusal == nil || commit.Attestation.Refusal.Code != "preflight.stale" {
		t.Fatalf("recheck = %+v, want stale refusal", commit.Attestation)
	}
	var result PrivateDispatchResult
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		result, err = DispatchCommitPrivate(ctx, q, commit)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var effect map[string]any
	if err := json.Unmarshal(result.Result, &effect); err != nil {
		t.Fatal(err)
	}
	if effect["action"] != "refused" || effect["consequence"] != "none" {
		t.Fatalf("stale recheck effect = %v", effect)
	}
	if got := dfInt(t, db, `SELECT COUNT(*) FROM runs`); got != 0 {
		t.Fatalf("stale host-file recheck opened %d runs", got)
	}
}

func TestPrivateDispatchRechecksRuntimeCatalogImmediatelyBeforeCommit(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	var prepared PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		prepared, err = DispatchPreparePrivate(ctx, q, readyPrivateRequest(t, db))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	home := os.Getenv("MC_HOME")
	commit, err := DispatchAttestPrivate(home, prepared)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, "config.toml")
	cfg, err := operatorconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	chatgpt := cfg.Runtimes.Bindings["chatgpt"]
	chatgpt.Model = "operator-repointed-model"
	cfg.Runtimes.Bindings["chatgpt"] = chatgpt
	if _, err := operatorconfig.WriteAtomic(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	commit = DispatchRecheckPrivate(home, prepared, commit)
	if commit.Attestation.Refusal == nil ||
		commit.Attestation.Refusal.Code != refusal.CodeStale {
		t.Fatalf("runtime-catalog recheck = %+v, want stale refusal", commit.Attestation)
	}
	var result PrivateDispatchResult
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		result, err = DispatchCommitPrivate(ctx, q, commit)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var effect map[string]any
	if err := json.Unmarshal(result.Result, &effect); err != nil {
		t.Fatal(err)
	}
	if effect["action"] != "refused" || effect["consequence"] != "none" ||
		dfInt(t, db, `SELECT COUNT(*) FROM runs`) != 0 {
		t.Fatalf("runtime-catalog drift was not inert: %v", effect)
	}
}

func TestPrivateDispatchRechecksAuthorityBehindEquivalentRefusal(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	dvExec(t, db, `UPDATE sandbox_profiles SET harness_env_policy=? WHERE id='default'`,
		`{"ANTHROPIC_API_KEY":"planted"}`)
	var prepared PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		prepared, err = DispatchPreparePrivate(ctx, q, readyPrivateRequest(t, db))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	home := os.Getenv("MC_HOME")
	commit, err := DispatchAttestPrivate(home, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if commit.Attestation.Refusal == nil ||
		commit.Attestation.Refusal.Code != refusal.CodeEnvForbidden ||
		commit.Attestation.RuntimeDigest == "" ||
		commit.Attestation.RoutingDigest == "" {
		t.Fatalf("routed refusal omitted its authority fence: %+v", commit.Attestation)
	}
	configPath := filepath.Join(home, "config.toml")
	cfg, err := operatorconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	chatgpt := cfg.Runtimes.Bindings["chatgpt"]
	chatgpt.Model = "changed-behind-equivalent-refusal"
	cfg.Runtimes.Bindings["chatgpt"] = chatgpt
	if _, err := operatorconfig.WriteAtomic(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	commit = DispatchRecheckPrivate(home, prepared, commit)
	if commit.Attestation.Refusal == nil ||
		commit.Attestation.Refusal.Code != refusal.CodeStale ||
		commit.Attestation.RuntimeDigest != "" ||
		commit.Attestation.RoutingDigest != "" {
		t.Fatalf("equivalent refusal authority drift = %+v, want sanitized stale", commit.Attestation)
	}
}

func TestPrivateCandidateStructuralBounds(t *testing.T) {
	base := PrivateDispatchCandidate{
		RunID: "0123456789abcdef", Project: "ws-test", Role: "worker", ProposedPool: []int64{},
		DedupeTitles: []string{}, Token: strings.Repeat("a", 64),
		TimeoutMinutes: 120, GraceMinutes: 30, HeartbeatIntervalS: 30,
		SpawnGraceS: 300, HardDeadlineMinutes: 480,
		MountState: PrivateDispatchMountState{Projects: []PrivateDispatchProject{}},
	}
	// The unmutated base must be ACCEPTED, or every subtest below rejects for
	// the wrong reason and the named validations go unproven (a missing
	// MountState alone voids all five).
	accepted := base
	if _, err := preparedFromCandidate(defaultDispatchProtocolIdentity,
		"deployment-test", dfRequestID, &accepted); err != nil {
		t.Fatalf("the unmutated base candidate must be accepted, got: %v", err)
	}
	tests := map[string]func(*PrivateDispatchCandidate){
		"role_control":  func(c *PrivateDispatchCandidate) { c.Role = "worker\n" },
		"no_project":    func(c *PrivateDispatchCandidate) { c.Project = "" },
		"duplicate_ids": func(c *PrivateDispatchCandidate) { c.ProposedPool = []int64{1, 1} },
		"title_control": func(c *PrivateDispatchCandidate) { c.DedupeTitles = []string{"secret\nnext"} },
		"invalid_utf8":  func(c *PrivateDispatchCandidate) { c.DedupeTitles = []string{string([]byte{0xff})} },
		"scalar_oversize": func(c *PrivateDispatchCandidate) {
			c.DedupeTitles = []string{strings.Repeat("x", maxPrivateScalarBytes+1)}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			if _, err := preparedFromCandidate(defaultDispatchProtocolIdentity,
				"deployment-test", dfRequestID, &candidate); err == nil {
				t.Fatal("malformed candidate projection was accepted")
			}
		})
	}
}

// The helper never trusts the broker's plan shape: a route attestation must
// carry an explicit, bounded, evidence-complete plan with strictly ordered
// unique non-overlapping absolute destinations; a refusal carries none.
func TestValidatePrivateAttestationMountPlanRules(t *testing.T) {
	digest := strings.Repeat("ab", 32)
	entry := PrivateDispatchMountEntry{
		Access: "rw", Destination: "/mc/artifacts/art", Device: "42", Inode: "7",
		Kind: "dir", LogicalID: "artifact:art", Mode: 448, OwnerUID: 501, Source: "/srv/artifact",
	}
	route := func(plan *PrivateDispatchMountPlan) PrivateDispatchAttestation {
		return PrivateDispatchAttestation{
			RuntimeDigest: digest, RoutingDigest: digest,
			Harness: "claude-sdk", Binding: "endpoint",
			RuntimeBinding: privateRuntimeBindingFromSpec(testEndpointBinding()),
			MountPlan:      plan,
		}
	}
	valid := func() *PrivateDispatchMountPlan {
		return &PrivateDispatchMountPlan{Version: 1, Entries: []PrivateDispatchMountEntry{entry}}
	}
	if err := validatePrivateAttestation(route(valid())); err != nil {
		t.Fatalf("a valid route+plan attestation was rejected: %v", err)
	}
	custom := route(valid())
	custom.Harness = "codex"
	custom.Binding = "operator-chat"
	custom.RuntimeBinding = privateRuntimeBindingFromSpec(routing.BindingSpec{
		ID: "operator-chat", Harness: "codex", Model: "operator-model",
		Channel: "codex", Credential: "operator-chat", AuthKind: "codex-oauth",
		Delivery: routing.ProviderProxy,
		ProviderCredentialKeys: []string{
			"CODEX_ACCESS_TOKEN", "CODEX_API_KEY", "OPENAI_API_KEY",
		},
		TokenURL: operatorconfig.Default().Runtimes.Credentials["chatgpt"].TokenURL, ClientID: operatorconfig.Default().Runtimes.Credentials["chatgpt"].ClientID,
	})
	if err := validatePrivateAttestation(custom); err != nil {
		t.Fatalf("a structurally valid custom binding was rejected: %v", err)
	}
	substituted := custom
	substituted.RuntimeBinding.TokenURL = "https://untrusted.example/token"
	if err := validatePrivateAttestation(substituted); err == nil {
		t.Fatal("The launch accepted a custom provider destination.")
	}
	for _, kind := range []string{"static-env", "none"} {
		substituted = custom
		substituted.RuntimeBinding.AuthKind = kind
		if err := validatePrivateAttestation(substituted); err == nil {
			t.Fatal("The launch accepted an unsupported connection.")
		}
	}
	substituted = custom
	substituted.RuntimeBinding.ID = "other"
	if err := validatePrivateAttestation(substituted); err == nil {
		t.Fatal("a runtime binding whose id differs from the route was accepted")
	}
	substituted = custom
	substituted.RuntimeBinding.Harness = "claude-sdk"
	if err := validatePrivateAttestation(substituted); err == nil {
		t.Fatal("a runtime binding whose harness differs from the route was accepted")
	}
	substituted = custom
	substituted.RuntimeBinding.AuthKind = "invented"
	if err := validatePrivateAttestation(substituted); err == nil {
		t.Fatal("a runtime binding with an invented auth shape was accepted")
	}
	substituted = custom
	substituted.RuntimeBinding.Credential = ""
	if err := validatePrivateAttestation(substituted); err == nil {
		t.Fatal("a runtime binding without a credential source was accepted")
	}
	if err := validatePrivateAttestation(route(&PrivateDispatchMountPlan{
		Version: 1, Entries: []PrivateDispatchMountEntry{},
	})); err != nil {
		t.Fatalf("an explicit empty plan was rejected: %v", err)
	}
	if err := validatePrivateAttestation(route(nil)); err == nil {
		t.Fatal("a route attestation without an explicit plan was accepted")
	}
	if err := validatePrivateAttestation(PrivateDispatchAttestation{
		MountPlan: valid(),
		Refusal:   &PrivateDispatchRefusal{Code: refusal.CodeStale, Field: "none", Summary: "mismatch"},
	}); err == nil {
		t.Fatal("a refusal attestation carrying a plan was accepted")
	}
	if err := validatePrivateAttestation(PrivateDispatchAttestation{
		RuntimeBinding: custom.RuntimeBinding,
		Refusal:        &PrivateDispatchRefusal{Code: refusal.CodeStale, Field: "none", Summary: "mismatch"},
	}); err == nil {
		t.Fatal("a refusal attestation carrying runtime authority was accepted")
	}

	mutations := map[string]func(*PrivateDispatchMountPlan){
		"nil_entries":     func(p *PrivateDispatchMountPlan) { p.Entries = nil },
		"bad_version":     func(p *PrivateDispatchMountPlan) { p.Version = 2 },
		"bad_access":      func(p *PrivateDispatchMountPlan) { p.Entries[0].Access = "rwx" },
		"bad_kind":        func(p *PrivateDispatchMountPlan) { p.Entries[0].Kind = "socket" },
		"relative_source": func(p *PrivateDispatchMountPlan) { p.Entries[0].Source = "srv/artifact" },
		"colon_source":    func(p *PrivateDispatchMountPlan) { p.Entries[0].Source = "/srv/a:b" },
		"unclean_dest":    func(p *PrivateDispatchMountPlan) { p.Entries[0].Destination = "/workspace//art" },
		"hex_inode":       func(p *PrivateDispatchMountPlan) { p.Entries[0].Inode = "0x2a" },
		"empty_device":    func(p *PrivateDispatchMountPlan) { p.Entries[0].Device = "" },
		"mode_over_perm":  func(p *PrivateDispatchMountPlan) { p.Entries[0].Mode = 0o1000 },
		"negative_owner":  func(p *PrivateDispatchMountPlan) { p.Entries[0].OwnerUID = -1 },
		"unsorted_destinations": func(p *PrivateDispatchMountPlan) {
			second := entry
			second.Destination = "/mc/artifacts/aaa"
			second.LogicalID = "artifact:aaa"
			p.Entries = append(p.Entries, second)
		},
		"overlapping_destinations": func(p *PrivateDispatchMountPlan) {
			second := entry
			second.Destination = entry.Destination + "/nested"
			second.LogicalID = "artifact:art/nested"
			p.Entries = append(p.Entries, second)
		},
		"duplicate_destinations": func(p *PrivateDispatchMountPlan) {
			p.Entries = append(p.Entries, entry)
		},
		"colon_destination": func(p *PrivateDispatchMountPlan) { p.Entries[0].Destination = "/workspace/a:b" },
		"destination_outside_workspace": func(p *PrivateDispatchMountPlan) {
			p.Entries[0].Destination = "/mc/session"
			p.Entries[0].LogicalID = "artifact:session"
		},
		"duplicate_logical_ids": func(p *PrivateDispatchMountPlan) {
			second := entry
			second.Destination = "/mc/artifacts/other"
			p.Entries = append([]PrivateDispatchMountEntry{second}, p.Entries...)
		},
		"oversized_plan_bytes": func(p *PrivateDispatchMountPlan) {
			// Structurally valid and honestly sorted, but past the 32 KiB
			// byte budget: the helper's own bound must refuse it even though
			// the producer bound would never have emitted it.
			p.Entries = nil
			for i := 0; i < 9; i++ {
				huge := entry
				huge.Destination = fmt.Sprintf("/mc/artifacts/bulk-%d", i)
				huge.LogicalID = fmt.Sprintf("artifact:bulk-%d:%s", i, strings.Repeat("x", 4000))
				p.Entries = append(p.Entries, huge)
			}
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			plan := valid()
			mutate(plan)
			if err := validatePrivateAttestation(route(plan)); err == nil {
				t.Fatal("a malformed plan attestation was accepted")
			}
		})
	}
}

func TestValidatePrivateMountPlanSeedingProjectionRules(t *testing.T) {
	valid := func() *PrivateDispatchMountPlan {
		return &PrivateDispatchMountPlan{
			Entries: []PrivateDispatchMountEntry{},
			SeedingProjections: []PrivateDispatchSeedingProjection{{
				GitCoverKind: "seeding-source-git-cover", ObjectFormat: "sha1",
				ProjectionKind: "seeding-committed-projection", TargetRef: "HEAD",
				WorkspaceRoot: PrivateDispatchPathIdentity{
					Canonical: "/srv/repo", Device: "8", Inode: "9", OwnerUID: 501,
				},
				ProjectID: "repo",
			}},
			Version: 1,
		}
	}
	if err := validatePrivateMountPlan(valid()); err != nil {
		t.Fatalf("valid seeding projection rejected: %v", err)
	}

	bad := map[string]func(*PrivateDispatchMountPlan){
		"cover_kind":      func(p *PrivateDispatchMountPlan) { p.SeedingProjections[0].GitCoverKind = "git" },
		"projection_kind": func(p *PrivateDispatchMountPlan) { p.SeedingProjections[0].ProjectionKind = "worktree" },
		"object_format":   func(p *PrivateDispatchMountPlan) { p.SeedingProjections[0].ObjectFormat = "md5" },
		"empty_target":    func(p *PrivateDispatchMountPlan) { p.SeedingProjections[0].TargetRef = "" },
		"nested_id":       func(p *PrivateDispatchMountPlan) { p.SeedingProjections[0].ProjectID = "repo/sub" },
		"relative_root":   func(p *PrivateDispatchMountPlan) { p.SeedingProjections[0].WorkspaceRoot.Canonical = "srv/repo" },
		"non_decimal_device": func(p *PrivateDispatchMountPlan) {
			p.SeedingProjections[0].WorkspaceRoot.Device = "0x8"
		},
		// A pipeline mount plan names one Project: a second projection,
		// duplicate or not, is refused.
		"duplicate_projection": func(p *PrivateDispatchMountPlan) {
			p.SeedingProjections = append(p.SeedingProjections, p.SeedingProjections[0])
		},
		"second_project_projection": func(p *PrivateDispatchMountPlan) {
			second := p.SeedingProjections[0]
			second.ProjectID = "alpha"
			p.SeedingProjections = append(p.SeedingProjections, second)
		},
	}
	for name, mutate := range bad {
		t.Run(name, func(t *testing.T) {
			p := valid()
			mutate(p)
			if err := validatePrivateMountPlan(p); err == nil {
				t.Fatalf("%s: malformed seeding projection was accepted", name)
			}
		})
	}
}

// The isolated repository plan of a subject run is helper-boundary input like
// every other plan member: the helper re-validates its shape.
func TestValidatePrivateMountPlanRepositoryRules(t *testing.T) {
	valid := func() *PrivateDispatchMountPlan {
		version := int64(3)
		return &PrivateDispatchMountPlan{
			Repository: &PrivateRepositoryPlan{
				DispatchRepository: substrate.DispatchRepository{
					TaskID: 7, InputCommit: strings.Repeat("a", 40), DestinationBranch: "main",
					VersionID: &version, MaxTemporaryBytes: 1 << 30,
				},
				WorkspaceRoot: "/srv/repo",
			},
			Entries: []PrivateDispatchMountEntry{}, Version: 1,
		}
	}
	if err := validatePrivateMountPlan(valid()); err != nil {
		t.Fatalf("valid repository plan rejected: %v", err)
	}
	fresh := valid()
	fresh.Repository.InputCommit = ""
	if err := validatePrivateMountPlan(fresh); err != nil {
		t.Fatalf("a repository plan without an input commit rejected: %v", err)
	}
	bad := map[string]func(*PrivateDispatchMountPlan){
		"no_task":             func(p *PrivateDispatchMountPlan) { p.Repository.TaskID = 0 },
		"relative_root":       func(p *PrivateDispatchMountPlan) { p.Repository.WorkspaceRoot = "srv/repo" },
		"unclean_root":        func(p *PrivateDispatchMountPlan) { p.Repository.WorkspaceRoot = "/srv/repo/../repo" },
		"filesystem_root":     func(p *PrivateDispatchMountPlan) { p.Repository.WorkspaceRoot = "/" },
		"no_destination":      func(p *PrivateDispatchMountPlan) { p.Repository.DestinationBranch = "" },
		"short_commit":        func(p *PrivateDispatchMountPlan) { p.Repository.InputCommit = "abc" },
		"non_hex_commit":      func(p *PrivateDispatchMountPlan) { p.Repository.InputCommit = strings.Repeat("z", 40) },
		"no_temporary_budget": func(p *PrivateDispatchMountPlan) { p.Repository.MaxTemporaryBytes = 0 },
	}
	for name, mutate := range bad {
		t.Run(name, func(t *testing.T) {
			p := valid()
			mutate(p)
			if err := validatePrivateMountPlan(p); err == nil {
				t.Fatalf("%s: a malformed repository plan was accepted", name)
			}
		})
	}
}

// The candidate's frozen mount state is helper-boundary input like any other:
// the Project projection must arrive shaped exactly as the spine tables would
// have produced it.
func TestValidatePrivateMountStateProjectRules(t *testing.T) {
	valid := func() PrivateDispatchMountState {
		return PrivateDispatchMountState{
			SelectedProject: "repo",
			Projects: []PrivateDispatchProject{{
				ProjectID: "alpha", Status: "active",
				ArtifactRoots: []string{}, DeniedPaths: []string{},
			}, {
				ProjectID: "repo", Status: "active", ProfilePresent: true, ProfileID: "p",
				WorkspaceRoot: "/srv/repo", ToolHomeDir: "/srv/home",
				ArtifactRoots: []string{"/srv/a", "/srv/b"}, DeniedPaths: []string{},
				GitIdentityOverride: &substrate.DispatchGitIdentityOverride{AuthorName: "Bot", AuthorEmail: "bot@example.test"},
			}},
		}
	}
	if err := validatePrivateMountState(valid()); err != nil {
		t.Fatalf("valid Project projection rejected: %v", err)
	}
	cases := map[string]func(*PrivateDispatchMountState){
		"nil_projects":          func(s *PrivateDispatchMountState) { s.Projects = nil },
		"selected_absent":       func(s *PrivateDispatchMountState) { s.SelectedProject = "missing" },
		"unsorted_projects":     func(s *PrivateDispatchMountState) { s.Projects[0], s.Projects[1] = s.Projects[1], s.Projects[0] },
		"nil_artifact_roots":    func(s *PrivateDispatchMountState) { s.Projects[1].ArtifactRoots = nil },
		"unsorted_artifacts":    func(s *PrivateDispatchMountState) { s.Projects[1].ArtifactRoots = []string{"/srv/b", "/srv/a"} },
		"control_byte_scalar":   func(s *PrivateDispatchMountState) { s.Projects[1].WorkspaceRoot = "/srv/repo\x00" },
		"present_without_id":    func(s *PrivateDispatchMountState) { s.Projects[1].ProfileID = "" },
		"absent_profile_state":  func(s *PrivateDispatchMountState) { s.Projects[0].WorkspaceRoot = "/srv/alpha" },
		"identity_without_mail": func(s *PrivateDispatchMountState) { s.Projects[1].GitIdentityOverride.AuthorEmail = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			state := valid()
			mutate(&state)
			if err := validatePrivateMountState(state); err == nil {
				t.Fatal("a malformed Project projection was accepted")
			}
		})
	}
}

// The commit-side mount-state drift fence (the reflect.DeepEqual reload in
// dispatchCommit) — the token structurally cannot catch profile drift because
// it is rebuilt from the PREPARED state, so this fence alone stands between a
// stale mount authorization and a claim.
func TestPrivateDispatchCommitStalesOnMountStateDrift(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	var prepared PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		prepared, err = DispatchPreparePrivate(ctx, q, readyPrivateRequest(t, db))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if prepared.Kind != "candidate" {
		t.Fatalf("prepare = %q, want a candidate", prepared.Kind)
	}
	// A concurrent operator edit to the selected profile between prepare and
	// commit: the reloaded projection must stale the prepared candidate.
	dvExec(t, db, `UPDATE sandbox_profiles SET artifact_roots='["/srv/added-after-prepare"]' WHERE id='default'`)

	commit, err := DispatchAttestPrivate(os.Getenv("MC_HOME"), prepared)
	if err != nil {
		t.Fatal(err)
	}
	var result PrivateDispatchResult
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		result, err = DispatchCommitPrivate(ctx, q, commit)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var effect map[string]any
	if err := json.Unmarshal(result.Result, &effect); err != nil {
		t.Fatal(err)
	}
	if effect["action"] != "refused" || effect["code"] != refusal.CodeStale || effect["consequence"] != "none" {
		t.Fatalf("mount-state drift effect = %v, want inert preflight.stale", effect)
	}
	if got := dfInt(t, db, `SELECT COUNT(*) FROM runs`); got != 0 {
		t.Fatalf("mount-state drift opened %d runs", got)
	}
}

// The before-commit attest repeat, end to end through the private frames: a
// host chmod on an authorized source between attest and commit changes the
// plan's mode evidence, so the recheck's canonical attestation differs and
// the candidate stales — no claim from drifted host authority.
func TestPrivateDispatchRecheckStalesOnMountEvidenceDrift(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	root := t.TempDir()
	artifacts := maMkdir(t, root, "artifacts")
	dvExec(t, db, `UPDATE sandbox_profiles SET artifact_roots=? WHERE id='default'`, fmt.Sprintf(`[%q]`, artifacts))
	home := os.Getenv("MC_HOME")
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	allowlist := fmt.Sprintf("version = 1\n\n[[allow]]\npath = %q\ntarget = \"artifacts\"\naccess = \"rw\"\n", artifacts)
	if err := os.WriteFile(filepath.Join(home, "mount-allowlist"), []byte(allowlist), 0o600); err != nil {
		t.Fatal(err)
	}
	maStubSnapshot(t, root, "ws-test")

	var prepared PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		prepared, err = DispatchPreparePrivate(ctx, q, readyPrivateRequest(t, db))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	commit, err := DispatchAttestPrivate(home, prepared)
	if err != nil {
		t.Fatal(err)
	}
	plan := commit.Attestation.MountPlan
	if plan == nil || len(plan.Entries) != 1 || plan.Entries[0].Mode != 0o700 {
		t.Fatalf("attested plan = %+v, want the artifact entry with its mode evidence", plan)
	}

	// Host drift inside the released-lock window: same path, same inode, new
	// mode grant. Docker inspect could never see this; the evidence does.
	if err := os.Chmod(artifacts, 0o755); err != nil {
		t.Fatal(err)
	}
	commit = DispatchRecheckPrivate(home, prepared, commit)
	if commit.Attestation.Refusal == nil || commit.Attestation.Refusal.Code != refusal.CodeStale {
		t.Fatalf("evidence-drift recheck = %+v, want stale refusal", commit.Attestation)
	}
	var result PrivateDispatchResult
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		result, err = DispatchCommitPrivate(ctx, q, commit)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var effect map[string]any
	if err := json.Unmarshal(result.Result, &effect); err != nil {
		t.Fatal(err)
	}
	if effect["action"] != "refused" || effect["consequence"] != "none" {
		t.Fatalf("evidence-drift effect = %v", effect)
	}
	if got := dfInt(t, db, `SELECT COUNT(*) FROM runs`); got != 0 {
		t.Fatalf("evidence drift opened %d runs", got)
	}
}

// TestPrivateDispatchPrepareCarriesTheRuntimeCapabilityIntoACollieWake pins the
// crossing the lease-free branch owes: the capability is host input to PREPARE
// (the wake finalizes there and never reaches attest), the wake resolves its
// frozen binding against it, and the response names the exact catalog it used
// so the host can fence drift immediately.
func TestPrivateDispatchPrepareCarriesTheRuntimeCapabilityIntoACollieWake(t *testing.T) {
	db := dvSpine(t)
	hwSession(t, db, "h-1")
	hwPendingInbound(t, db, "h-1", 1)

	req := collieStageRequest(dfUUID(t, db))
	var prepared PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		prepared, err = DispatchPreparePrivate(ctx, q, req)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if prepared.Kind != "final" || prepared.Final == nil {
		t.Fatalf("prepared frame = %+v, want a final collie wake", prepared)
	}
	var effect map[string]any
	if err := json.Unmarshal(*prepared.Final, &effect); err != nil {
		t.Fatal(err)
	}
	if effect["action"] != "collie-wake" {
		t.Fatalf("final effect = %+v, want a collie wake", effect)
	}
	runtime, ok := effect["runtime_binding"].(map[string]any)
	if !ok || runtime["id"] != "claude" || runtime["harness"] != "claude-sdk" {
		t.Fatalf("wake final carries no resolved capability: %+v", effect)
	}
	if prepared.RuntimeCapabilityDigest != req.RuntimeCapability.Digest {
		t.Fatalf("prepare named catalog %q, carried %q",
			prepared.RuntimeCapabilityDigest, req.RuntimeCapability.Digest)
	}
}

// TestPrivateDispatchPrepareNamesNoCatalogForARuntimeFreeFinal keeps the drift
// fence honest in the other direction: an idle end resolves no runtime, so it
// must not drag an unrelated config edit into a stale refusal.
func TestPrivateDispatchPrepareNamesNoCatalogForARuntimeFreeFinal(t *testing.T) {
	db := dvSpine(t)
	hwSession(t, db, "h-1")
	dvExec(t, db, `UPDATE collie_sessions SET last_activity_at = '2000-01-01 00:00:00' WHERE id = 'h-1'`)

	var prepared PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		prepared, err = DispatchPreparePrivate(ctx, q, collieStageRequest(dfUUID(t, db)))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var effect map[string]any
	if err := json.Unmarshal(*prepared.Final, &effect); err != nil {
		t.Fatal(err)
	}
	if effect["action"] != "collie-stop" {
		t.Fatalf("final effect = %+v, want the idle end", effect)
	}
	if prepared.RuntimeCapabilityDigest != "" {
		t.Fatalf("a runtime-free final named catalog %q", prepared.RuntimeCapabilityDigest)
	}
}

// TestPrivateDispatchPrepareRefusesMalformedCapabilityCarriers proves the helper
// re-validates the carrier rather than trusting the broker's shape — the same
// posture the mount plan and the attested route binding already take.
func TestPrivateDispatchPrepareRefusesMalformedCapabilityCarriers(t *testing.T) {
	db := dvSpine(t)
	uuid := dfUUID(t, db)
	shipped := shippedCapability()
	for name, capability := range map[string]PrivateRuntimeCapability{
		"nil binding list":    {Digest: shipped.Digest},
		"undigested bindings": {Bindings: shipped.Bindings},
		"malformed digest":    {Digest: "nope", Bindings: shipped.Bindings},
		"unordered bindings":  {Digest: shipped.Digest, Bindings: []PrivateRuntimeBinding{shipped.Bindings[len(shipped.Bindings)-1], shipped.Bindings[0]}},
		"incoherent binding":  {Digest: shipped.Digest, Bindings: []PrivateRuntimeBinding{{ID: "x", Harness: "claude-sdk"}}},
	} {
		t.Run(name, func(t *testing.T) {
			req := privateRequest(uuid)
			req.RuntimeCapability = capability
			err := inTx(db, func(ctx context.Context, q Q) error {
				_, err := DispatchPreparePrivate(ctx, q, req)
				return err
			})
			if err == nil {
				t.Fatal("a malformed runtime capability carrier was admitted")
			}
		})
	}
}

// TestRuntimeCapabilityDriftedIsTheImmediatePostPrepareFence covers the host
// half: a wake resolved under one catalog is abandoned when config.toml moves
// before the effect is handed to the resident, while a runtime-free final and
// an unchanged catalog stay clean.
func TestRuntimeCapabilityDriftedIsTheImmediatePostPrepareFence(t *testing.T) {
	db := dvSpine(t)
	_ = db
	home := os.Getenv("MC_HOME")
	capability := ResolveRuntimeCapability(home)
	if !validLowercaseHex(capability.Digest, 64) {
		t.Fatalf("fixture home resolved no catalog: %+v", capability)
	}
	if RuntimeCapabilityDrifted(home, capability.Digest) {
		t.Fatal("an unchanged catalog reported drift")
	}
	if RuntimeCapabilityDrifted(home, "") {
		t.Fatal("a runtime-free final was fenced against config")
	}
	configPath := filepath.Join(home, "config.toml")
	cfg, err := operatorconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	claude := cfg.Runtimes.Bindings["claude"]
	claude.Model = "operator-repointed-model"
	cfg.Runtimes.Bindings["claude"] = claude
	if _, err := operatorconfig.WriteAtomic(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	if !RuntimeCapabilityDrifted(home, capability.Digest) {
		t.Fatal("a repointed catalog did not stale the resolved effect")
	}
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	if !RuntimeCapabilityDrifted(home, capability.Digest) {
		t.Fatal("a removed config did not stale the resolved effect")
	}
}

// A refusal frame attests no route, so its validator requires an EMPTY
// runtime binding. The projection that builds the frame normalizes its
// credential key list to an empty slice while the zero struct leaves it nil.
// reflect.DeepEqual tells those apart, so a validator that compares against
// the zero value refuses every frame the host itself produces.
//
// This test needs no Docker and no spine: it asks the validator to admit
// exactly what canonicalPrivateAttestation builds.
func TestRouteFreeFramesAdmitTheHostsOwnEmptyRuntimeBinding(t *testing.T) {
	inertRefusal := &refusal.Refusal{
		Code: refusal.CodeStale, Field: refusal.FieldNone, Summary: refusal.SummaryMismatch,
	}
	t.Run("refusal", func(t *testing.T) {
		frame := canonicalPrivateAttestation(attestedDispatch{refusal: inertRefusal})
		if err := validatePrivateAttestation(frame); err != nil {
			t.Fatalf("the host's own route-free refusal frame was refused: %v", err)
		}
		// The frame crosses as JSON, so the round trip must not reintroduce
		// the nil/empty distinction either.
		body, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		var round PrivateDispatchAttestation
		if err := json.Unmarshal(body, &round); err != nil {
			t.Fatal(err)
		}
		if err := validatePrivateAttestation(round); err != nil {
			t.Fatalf("the round-tripped route-free refusal frame was refused: %v", err)
		}
		// The converse still holds: a refusal may not smuggle a route.
		round.RuntimeBinding.ID = "smuggled"
		if err := validatePrivateAttestation(round); err == nil {
			t.Fatal("a refusal frame smuggled a route through the runtime binding")
		}
	})

	t.Run("stale attestation", func(t *testing.T) {
		if err := validatePrivateAttestation(stalePrivateAttestation()); err != nil {
			t.Fatalf("the stale refusal frame was refused: %v", err)
		}
	})
}
