package verbs

// The dispatch seam's pure primitives. The canonical prepare
// projection, the preparation token, and the commit-side dispatch_key are a
// frozen cross-harness wire contract exactly like collieCandidateState: the
// golden vectors below pin the bytes, and the derivation tests pin the
// domain-separated hashing with the separators written as literals (never the
// production constants), so a drifted separator or field order fails here
// before it orphans a real deployment's replay fences.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"mc/dispatch"
	"mc/substrate"
)

func i64p(v int64) *int64   { return &v }
func strp(s string) *string { return &s }

// dsGoldenPrepare is a fully-populated canonical prepare projection: one task
// with every nullable field set, one with every nullable field null, a held
// lease, one Collie candidate state, and a spawn candidate. Values are synthetic
// — the vector freezes the wire shape, not a reachable spine state.
func dsGoldenPrepare() canonicalPrepare {
	return canonicalPrepare{
		Version:             16,
		ReleaseBuildID:      "development",
		ControlVersion:      4,
		SpineSchemaID:       "current",
		ConfigSchemaVersion: 1,
		DeploymentUUID:      "de41cafe-0000-4000-8000-000000000001",
		RequestID:           "00112233445566ff",
		Project:             "ws-test",
		ProjectStatus:       "active",
		VisionReady:         true, VisionPath: "/workspace/VISION.md", VisionContent: "Build reliable software.",
		AttentionTaskIDs: []int64{9},
		Tasks: []canonicalTask{
			{
				ID: 7, Title: "fix the gate", Scope: "task",
				Priority: 1, CreatedAt: "2026-01-02 03:04:05", Status: "seeded",
				Blocked: false, DispatchRetries: 2,
				Decision: "approved", DecidedAt: strp("2026-01-02 04:05:06"),
				Archived: false, Project: "ws-test", ProjectStatus: "active",
				Delivery: canonicalDelivery{VersionID: 3},
			},
			{
				ID: 9, Title: "t", Scope: "task",
				Priority: 0, CreatedAt: "2026-01-03 00:00:00", Status: "backlog",
				Blocked: true, Stalled: true, DispatchRetries: 0,
				Decision: "", DecidedAt: nil,
				Archived: false, Project: "ws-test", ProjectStatus: "active",
			},
		},
		Packets: []canonicalPacket{
			{TaskID: 7, CreatedAt: "2026-01-02 05:00:00", Archived: false},
		},
		Lease: canonicalLease{
			Held: true, RunID: "aaaabbbbccccdddd", Owner: "worker", SubjectID: i64p(7),
			BootID: "boot-a", AcquiredUptimeMS: i64p(21600000), HeartbeatUptimeMS: nil,
			DeadlineUptimeMS: i64p(28800000),
		},
		Tunables: canonicalTunables{
			TimeoutMinutes: 120, GraceMinutes: 30, HeartbeatIntervalS: 30,
			SpawnGraceS: 300, HardDeadlineMinutes: 480,
		},
		Collies: []collieCandidateState{
			{
				SessionID: "sess-1", LaunchID: strp("aaaaaaaaaaaaaaaa"),
				LaunchMode: strp("rows"), PrimeSeq: i64p(7), PrimeCount: i64p(3),
				ResumeOwed: 0, ResumeMode: nil, ResumeSeq: nil, ResumeCount: nil,
				Binding: "claude", InputSeq: i64p(4),
			},
		},
		MountState: PrivateDispatchMountState{
			SubjectRepository: &substrate.DispatchRepository{
				TaskID: 7, InputCommit: "abc123", DestinationBranch: "culler", VersionID: i64p(3),
				MaxTemporaryBytes: 1073741824,
			},
			SelectedProject: "ws-test",
			Projects: []PrivateDispatchProject{{
				ProjectID: "ws-test", Status: "active",
				ProfilePresent: true, ProfileID: "default", WorkspaceRoot: "/srv/ws-test",
				ArtifactRoots: []string{"/srv/artifacts"},
				DeniedPaths:   []string{"/srv/ws-test/private"}, ToolHomeDir: "/srv/tool-home",
				RuntimeControlDir: "/srv/runtime-control",
			}},
		},
		Candidate: canonicalCandidate{
			Kind: "spawn", RunID: "0123456789abcdef", Project: "ws-test", Role: "worker", SubjectID: i64p(7),
			ProposedPool: []int64{}, DedupeTitles: []string{},
		},
	}
}

// The frozen canonical bytes of dsGoldenPrepare. Assembled from per-struct
// segments for reviewability; the assertion is on the joined whole.
func dsGoldenPrepareJSON() string {
	return strings.Join([]string{
		`{"proposal_decision_pending":false,"connection_states":"","infrastructure_failure_count":0,"infrastructure_next_retry_at":"","version":16`,
		`"release_build_id":"development"`,
		`"gateway_control_version":4`,
		`"spine_schema_id":"current"`,
		`"config_schema_version":1`,
		`"deployment_uuid":"de41cafe-0000-4000-8000-000000000001"`,
		`"request_id":"00112233445566ff"`,
		`"project":"ws-test"`,
		`"project_status":"active"`,
		`"vision_ready":true`, `"vision_path":"/workspace/VISION.md"`, `"vision_content":"Build reliable software."`,
		`"attention_task_ids":[9]`,
		`"tasks":[` +
			`{"delivery":{"version_id":3},` +
			`"id":7,"next_retry_at":"","title":"fix the gate","scope":"task",` +
			`"priority":1,"created_at":"2026-01-02 03:04:05","status":"seeded",` +
			`"blocked":false,"stalled":false,"dispatch_retries":2,` +
			`"decision":"approved","decided_at":"2026-01-02 04:05:06",` +
			`"archived":false,"project":"ws-test","project_status":"active"},` +
			`{"delivery":{"version_id":0},` +
			`"id":9,"next_retry_at":"","title":"t","scope":"task",` +
			`"priority":0,"created_at":"2026-01-03 00:00:00","status":"backlog",` +
			`"blocked":true,"stalled":true,"dispatch_retries":0,` +
			`"decision":"","decided_at":null,` +
			`"archived":false,"project":"ws-test","project_status":"active"}]`,
		`"packets":[{"task_id":7,"created_at":"2026-01-02 05:00:00","archived":false}]`,
		`"lease":{"held":true,"final_outcome":"","run_id":"aaaabbbbccccdddd","owner":"worker","subject_id":7,` +
			`"boot_id":"boot-a","acquired_uptime_ms":21600000,"heartbeat_uptime_ms":null,` +
			`"deadline_uptime_ms":28800000}`,
		`"tunables":{"timeout_minutes":120,"grace_minutes":30,"heartbeat_interval_s":30,` +
			`"spawn_grace_s":300,"hard_deadline_minutes":480}`,
		`"collies":[{"session_id":"sess-1","launch_id":"aaaaaaaaaaaaaaaa","launch_mode":"rows",` +
			`"prime_through_seq":7,"prime_row_count":3,"resume_owed":0,"resume_mode":null,` +
			`"resume_prime_through_seq":null,"resume_prime_row_count":null,` +
			`"binding":"claude","input_seq":4}]`,
		`"mount_state":{"subject_repository":{"destination_branch":"culler","input_commit":"abc123",` +
			`"max_temporary_bytes":1073741824,"task_id":7,"version_id":3},` +
			`"selected_project":"ws-test","projects":[{` +
			`"project_id":"ws-test","status":"active",` +
			`"profile_present":true,"profile_id":"default","workspace_root":"/srv/ws-test",` +
			`"artifact_roots":["/srv/artifacts"],` +
			`"denied_paths":["/srv/ws-test/private"],"tool_home_dir":"/srv/tool-home",` +
			`"runtime_control_dir":"/srv/runtime-control","git_identity_override":null,` +
			`"harness_env_policy":"","tool_env_policy":"","runtime_auth_delivery":""}]}`,
		`"candidate":{"kind":"spawn","run_id":"0123456789abcdef","project":"ws-test","role":"worker",` +
			`"subject_id":7,"proposed_pool":[],"dedupe_titles":[]}}`,
	}, ",")
}

func TestDispatchSeamCanonicalPrepareGoldenVector(t *testing.T) {
	got, err := dsGoldenPrepare().bytes()
	if err != nil {
		t.Fatalf("canonical bytes: %v", err)
	}
	if string(got) != dsGoldenPrepareJSON() {
		t.Fatalf("canonical prepare bytes drifted from the frozen vector\n got: %s\nwant: %s", got, dsGoldenPrepareJSON())
	}
}

// The preparation token is SHA-256 over "MC-DISPATCH-PREPARE-V1\x00" plus the
// canonical bytes, hex-encoded. The separator is written as
// a literal here on purpose.
func TestDispatchSeamPreparationTokenDerivation(t *testing.T) {
	canonical := []byte(dsGoldenPrepareJSON())
	sum := sha256.Sum256(append([]byte("MC-DISPATCH-PREPARE-V1\x00"), canonical...))
	want := hex.EncodeToString(sum[:])
	if got := preparationToken(canonical); got != want {
		t.Fatalf("preparationToken = %q, want %q", got, want)
	}
	if !validDispatchKey(want) {
		t.Fatalf("token %q is not 64 lowercase hex", want)
	}
}

// dispatch_key = SHA256("MC-DISPATCH-ACTION-V10\x00" || preparation_token ||
// canonical_action). The token participates in its hex ASCII form — the
// frozen convention, pinned here.
func TestDispatchSeamDispatchKeyDerivation(t *testing.T) {
	spawnAction := canonicalAction{
		Version: 10, RequestID: "00112233445566ff", Consequence: "spawn",
		RunID: "0123456789abcdef", Role: "worker", SubjectID: i64p(7),
		RuntimeDigest: strings.Repeat("cd", 32),
		RoutingDigest: strings.Repeat("ab", 32), Harness: "claude-sdk", Binding: "endpoint",
		RuntimeBinding: privateRuntimeBindingFromSpec(testEndpointBinding()),
	}
	wantSpawnJSON := `{"version":10,"request_id":"00112233445566ff","consequence":"spawn",` +
		`"run_id":"0123456789abcdef","role":"worker","subject_id":7,` +
		`"runtime_digest":"` + strings.Repeat("cd", 32) + `",` +
		`"routing_digest":"` + strings.Repeat("ab", 32) + `",` +
		`"harness":"claude-sdk","binding":"endpoint",` +
		`"runtime_binding":{"id":"endpoint","harness":"claude-sdk","model":"endpoint-model",` +
		`"channel":"claude","credential":"endpoint","auth_kind":"claude-oauth","delivery":"proxy",` +
		`"provider_credential_keys":["ANTHROPIC_API_KEY","ANTHROPIC_AUTH_TOKEN"],` +
		`"declared_static_key":"","base_url":"",` +
		`"token_url":"https://platform.claude.com/v1/oauth/token","client_id":"9d1c250a-e61b-44d9-88ed-5944d1962f5e"},"plan_digest":"","refusal":null}`
	gotJSON, err := spawnAction.bytes()
	if err != nil {
		t.Fatalf("canonical action bytes: %v", err)
	}
	if string(gotJSON) != wantSpawnJSON {
		t.Fatalf("canonical action bytes drifted\n got: %s\nwant: %s", gotJSON, wantSpawnJSON)
	}

	token := preparationToken([]byte(dsGoldenPrepareJSON()))
	sum := sha256.Sum256(append(append([]byte("MC-DISPATCH-ACTION-V10\x00"), []byte(token)...), gotJSON...))
	want := hex.EncodeToString(sum[:])
	got, err := deriveDispatchKey(token, spawnAction)
	if err != nil {
		t.Fatalf("deriveDispatchKey: %v", err)
	}
	if got != want {
		t.Fatalf("deriveDispatchKey = %q, want %q", got, want)
	}
	if !validDispatchKey(got) {
		t.Fatalf("dispatch key %q is not 64 lowercase hex", got)
	}
}

// plan_digest = SHA256("MC-DISPATCH-PLAN-V1\x00" || canonical_plan), hex —
// the canonical plan bytes are pinned here because the effect's replay
// path round-trips maps alphabetically, so the declared field order must be
// alphabetical and must never drift.
func TestDispatchSeamMountPlanDigestDerivation(t *testing.T) {
	plan := &PrivateDispatchMountPlan{Version: 1, Entries: []PrivateDispatchMountEntry{{
		Access: "rw", Destination: "/mc/artifacts/art", Device: "42", Inode: "7",
		Kind: "dir", LogicalID: "artifact:art", Mode: 448, OwnerUID: 501, Source: "/srv/artifact",
	}}}
	wantJSON := `{"entries":[{"access":"rw","destination":"/mc/artifacts/art",` +
		`"device":"42","inode":"7","kind":"dir","logical_id":"artifact:art",` +
		`"mode":448,"owner_uid":501,"source":"/srv/artifact"}],"version":1}`
	gotJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("canonical plan bytes: %v", err)
	}
	if string(gotJSON) != wantJSON {
		t.Fatalf("canonical plan bytes drifted\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
	sum := sha256.Sum256(append([]byte("MC-DISPATCH-PLAN-V1\x00"), gotJSON...))
	want := hex.EncodeToString(sum[:])
	got, err := mountPlanDigest(plan)
	if err != nil {
		t.Fatalf("mountPlanDigest: %v", err)
	}
	if got != want {
		t.Fatalf("mountPlanDigest = %q, want %q", got, want)
	}
	if empty, err := mountPlanDigest(nil); err != nil || empty != "" {
		t.Fatalf("nil-plan digest = (%q, %v), want the explicit empty string", empty, err)
	}
	withRepository := &PrivateDispatchMountPlan{
		Repository: &PrivateRepositoryPlan{
			DispatchRepository: substrate.DispatchRepository{
				TaskID: 7, InputCommit: "abc123", DestinationBranch: "culler", VersionID: i64p(3),
				MaxTemporaryBytes: 1073741824,
			},
			WorkspaceRoot: "/srv/repo",
		},
		Entries: []PrivateDispatchMountEntry{},
		Version: 1,
	}
	// The isolated repository plan of a subject run is digest-covered too.
	wantRepositoryJSON := `{"entries":[],"repository":{"destination_branch":"culler","input_commit":"abc123",` +
		`"max_temporary_bytes":1073741824,"task_id":7,"version_id":3,` +
		`"workspace_root":"/srv/repo"},"version":1}`
	gotRepositoryJSON, err := json.Marshal(withRepository)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotRepositoryJSON) != wantRepositoryJSON {
		t.Fatalf("canonical repository plan bytes drifted\n got: %s\nwant: %s", gotRepositoryJSON, wantRepositoryJSON)
	}
	first, err := mountPlanDigest(withRepository)
	if err != nil {
		t.Fatal(err)
	}
	withRepository.Repository.InputCommit = "def456"
	second, err := mountPlanDigest(withRepository)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("an input commit change did not change the digest-covered plan")
	}
	withRepository.Repository.InputCommit = "abc123"
	withRepository.Repository.WorkspaceRoot = "/srv/other"
	third, err := mountPlanDigest(withRepository)
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Fatal("a workspace root change did not change the digest-covered plan")
	}
}

// dsGoldenWikiPrepare is a canonical projection of a wiki round with each
// field set: a last successful wiki update with a merge, a last counted wiki
// update that failed, and the Scribe candidate. The values are synthetic.
func dsGoldenWikiPrepare() canonicalWikiPrepare {
	a, b, c, d := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)
	return canonicalWikiPrepare{
		Version: 16, Round: "wiki", ReleaseBuildID: "development", ControlVersion: 4, SpineSchemaID: "current",
		ConfigSchemaVersion: 1, DeploymentUUID: "de41cafe-0000-4000-8000-000000000001", RequestID: "00112233445566ff",
		Project: "ws-test", ProjectStatus: "active", AcceptedCommit: a, MergeMarker: 12,
		LastSucceeded: &canonicalWikiUpdate{
			RunID: "1111222233334444", StartCommit: b, WikiCommit: c,
			StartedAt: "2026-01-02 03:04:05", EndedAt: "2026-01-02 03:30:00", Result: "succeeded", Counted: true,
			DestinationCommit: b, MergedCommit: d,
		},
		LastCounted: &canonicalWikiUpdate{
			RunID: "5555666677778888", StartCommit: d,
			StartedAt: "2026-01-03 00:00:00", EndedAt: "2026-01-03 00:10:00", Result: "failed", Counted: true,
		},
		Tunables: canonicalWikiTunables{
			TimeoutMinutes: 120, GraceMinutes: 30, HeartbeatIntervalS: 30,
			SpawnGraceS: 300, HardDeadlineMinutes: 480, WikiUpdateIntervalS: 3600,
		},
		MountState: PrivateDispatchMountState{
			SubjectRepository: &substrate.DispatchRepository{
				DestinationBranch: "culler", InputCommit: a, MaxTemporaryBytes: 1073741824, WikiUpdate: "0123456789abcdef",
			},
			SelectedProject: "ws-test",
			Projects:        dsGoldenPrepare().MountState.Projects,
		},
		Candidate: canonicalCandidate{
			Kind: "spawn", RunID: "0123456789abcdef", Project: "ws-test", Role: "scribe",
			ProposedPool: []int64{}, DedupeTitles: []string{},
		},
	}
}

// The canonical projection of a wiki round, its mount state, and the mount
// plan of a wiki update run are wire shapes. This test fixes their bytes.
func TestWikiProjectionGolden(t *testing.T) {
	a, b, c, d := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)
	wantMountState := `{"subject_repository":{"destination_branch":"culler","input_commit":"` + a + `",` +
		`"max_temporary_bytes":1073741824,"wiki_update":"0123456789abcdef"},` +
		`"selected_project":"ws-test","projects":[{` +
		`"project_id":"ws-test","status":"active",` +
		`"profile_present":true,"profile_id":"default","workspace_root":"/srv/ws-test",` +
		`"artifact_roots":["/srv/artifacts"],` +
		`"denied_paths":["/srv/ws-test/private"],"tool_home_dir":"/srv/tool-home",` +
		`"runtime_control_dir":"/srv/runtime-control","git_identity_override":null,` +
		`"harness_env_policy":"","tool_env_policy":"","runtime_auth_delivery":""}]}`
	wantPrepare := strings.Join([]string{
		`{"version":16`, `"round":"wiki"`,
		`"release_build_id":"development"`,
		`"gateway_control_version":4`,
		`"spine_schema_id":"current"`,
		`"config_schema_version":1`,
		`"deployment_uuid":"de41cafe-0000-4000-8000-000000000001"`,
		`"request_id":"00112233445566ff"`,
		`"project":"ws-test"`,
		`"project_status":"active"`,
		`"accepted_commit":"` + a + `"`,
		`"merge_marker":12`,
		`"last_succeeded":{"run_id":"1111222233334444","start_commit":"` + b + `","wiki_commit":"` + c + `",` +
			`"started_at":"2026-01-02 03:04:05","ended_at":"2026-01-02 03:30:00","result":"succeeded","counted":true,` +
			`"destination_commit":"` + b + `","merged_commit":"` + d + `"}`,
		`"last_counted":{"run_id":"5555666677778888","start_commit":"` + d + `","wiki_commit":"",` +
			`"started_at":"2026-01-03 00:00:00","ended_at":"2026-01-03 00:10:00","result":"failed","counted":true,` +
			`"destination_commit":"","merged_commit":""}`,
		`"tunables":{"timeout_minutes":120,"grace_minutes":30,"heartbeat_interval_s":30,` +
			`"spawn_grace_s":300,"hard_deadline_minutes":480,"wiki_update_interval_s":3600}`,
		`"mount_state":` + wantMountState,
		`"candidate":{"kind":"spawn","run_id":"0123456789abcdef","project":"ws-test","role":"scribe",` +
			`"subject_id":null,"proposed_pool":[],"dedupe_titles":[]}}`,
	}, ",")

	prepare := dsGoldenWikiPrepare()
	got, err := prepare.bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != wantPrepare {
		t.Fatalf("the bytes of the wiki projection changed\n got: %s\nwant: %s", got, wantPrepare)
	}
	sum := sha256.Sum256(append([]byte("MC-DISPATCH-PREPARE-V1\x00"), got...))
	if token := preparationToken(got); token != hex.EncodeToString(sum[:]) {
		t.Fatalf("token of the wiki projection = %q, want %q", token, hex.EncodeToString(sum[:]))
	}
	if preparationToken(got) == preparationToken([]byte(dsGoldenPrepareJSON())) {
		t.Fatal("a wiki projection and a pipeline projection have the same token")
	}

	gotMountState, err := json.Marshal(prepare.MountState)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotMountState) != wantMountState {
		t.Fatalf("the bytes of the wiki mount state changed\n got: %s\nwant: %s", gotMountState, wantMountState)
	}

	// A Project with no earlier wiki update has two null rows.
	prepare.LastSucceeded, prepare.LastCounted = nil, nil
	if got, err = prepare.bytes(); err != nil || !strings.Contains(string(got), `"merge_marker":12,"last_succeeded":null,"last_counted":null,"tunables":`) {
		t.Fatalf("the projection with no earlier wiki update = %s, %v", got, err)
	}

	plan := &PrivateDispatchMountPlan{
		JurisdictionDigest: strings.Repeat("d", 64),
		Repository: &PrivateRepositoryPlan{
			DispatchRepository: *dsGoldenWikiPrepare().MountState.SubjectRepository,
			WorkspaceRoot:      "/srv/repo",
		},
		ToolHome: &PrivateDispatchPathIdentity{Canonical: "/srv/tool-home", Device: "1", Inode: "2", OwnerUID: 501},
		Entries:  []PrivateDispatchMountEntry{},
		Version:  1,
	}
	wantPlan := `{"entries":[],"jurisdiction_digest":"` + strings.Repeat("d", 64) + `","repository":{"destination_branch":"culler","input_commit":"` + a + `",` +
		`"max_temporary_bytes":1073741824,"wiki_update":"0123456789abcdef",` +
		`"workspace_root":"/srv/repo"},"tool_home":{"canonical":"/srv/tool-home","device":"1","inode":"2","owner_uid":501},"version":1}`
	gotPlan, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotPlan) != wantPlan {
		t.Fatalf("the bytes of the wiki mount plan changed\n got: %s\nwant: %s", gotPlan, wantPlan)
	}
	// The stored effect of a start returns through a generic map, which
	// writes its keys in alphabetical order. The plan must keep its bytes.
	var generic any
	if err := json.Unmarshal(gotPlan, &generic); err != nil {
		t.Fatal(err)
	}
	if again, err := json.Marshal(generic); err != nil || string(again) != wantPlan {
		t.Fatalf("the wiki mount plan changed in a generic map\n got: %s\nwant: %s", again, wantPlan)
	}
	planSum := sha256.Sum256(append([]byte("MC-DISPATCH-PLAN-V1\x00"), gotPlan...))
	if digest, err := mountPlanDigest(plan); err != nil || digest != hex.EncodeToString(planSum[:]) {
		t.Fatalf("digest of the wiki mount plan = %q, %v, want %q", digest, err, hex.EncodeToString(planSum[:]))
	}
}

func TestDispatchSeamCanonicalActionRefusalVariant(t *testing.T) {
	idx := 2
	action := canonicalAction{
		Version: 10, RequestID: "00112233445566ff", Consequence: "refusal",
		RunID: "0123456789abcdef", Role: "worker", SubjectID: nil,
		Refusal: &canonicalRefusal{
			Code: "health.routing_invalid", Authority: "", Field: "routing",
			Summary: "unparsable", ItemIndex: &idx,
		},
	}
	want := `{"version":10,"request_id":"00112233445566ff","consequence":"refusal",` +
		`"run_id":"0123456789abcdef","role":"worker","subject_id":null,` +
		`"runtime_digest":"","routing_digest":"","harness":"","binding":"",` +
		`"runtime_binding":{"id":"","harness":"","model":"","channel":"","credential":"","auth_kind":"",` +
		`"delivery":"","provider_credential_keys":null,"declared_static_key":"",` +
		`"base_url":"","token_url":"","client_id":""},"plan_digest":"",` +
		`"refusal":{"code":"health.routing_invalid","authority":"","field":"routing",` +
		`"summary":"unparsable","item_index":2}}`
	got, err := action.bytes()
	if err != nil {
		t.Fatalf("canonical action bytes: %v", err)
	}
	if string(got) != want {
		t.Fatalf("refusal action bytes drifted\n got: %s\nwant: %s", got, want)
	}
}

func TestDispatchSeamRequestIDShape(t *testing.T) {
	a, err := newDispatchRequestID()
	if err != nil {
		t.Fatalf("newDispatchRequestID: %v", err)
	}
	b, err := newDispatchRequestID()
	if err != nil {
		t.Fatalf("newDispatchRequestID: %v", err)
	}
	if !validLowercaseHex(a, 16) || !validLowercaseHex(b, 16) {
		t.Fatalf("request ids %q, %q are not 16 lowercase hex", a, b)
	}
	if a == b {
		t.Fatalf("two request ids collided: %q", a)
	}
}

// buildCanonicalPrepare must sort tasks by id and packets by (task_id,
// created_at) — records arrive in SQL order, which is not part of the wire
// contract — and normalize nil slices to empty ones so the encoding never
// distinguishes "absent" from "empty".
func TestDispatchSeamBuildCanonicalPrepareNormalizes(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	rec := dispatch.Records{
		Tasks: []dispatch.Task{
			{ID: 9, Title: "b", Scope: "task", CreatedAt: created, Status: "backlog", Project: "ws-test", ProjectStatus: "active"},
			{ID: 7, Title: "a", Scope: "task", CreatedAt: created, Status: "seeded", Project: "ws-test", ProjectStatus: "active"},
		},
		Packets: []dispatch.Packet{
			{TaskID: 9, CreatedAt: created.Add(time.Hour)},
			{TaskID: 9, CreatedAt: created},
			{TaskID: 7, CreatedAt: created},
		},
	}
	sp := &dispatch.Spawn{Role: dispatch.Role("worker")}
	got := buildCanonicalPrepare("uuid-x", "00112233445566ff", rec, dispatch.Lease{}, tunables{}, nil, PrivateDispatchMountState{}, spawnCandidateProjection("0123456789abcdef", sp))
	if got.Tasks[0].ID != 7 || got.Tasks[1].ID != 9 {
		t.Fatalf("tasks not sorted by id: %+v", got.Tasks)
	}
	if got.Packets[0].TaskID != 7 || got.Packets[1].TaskID != 9 || got.Packets[1].CreatedAt >= got.Packets[2].CreatedAt {
		t.Fatalf("packets not sorted by (task_id, created_at): %+v", got.Packets)
	}
	if got.Collies == nil {
		t.Fatalf("nil collies must normalize to an empty slice")
	}
	if got.Candidate.ProposedPool == nil || got.Candidate.DedupeTitles == nil {
		t.Fatalf("nil candidate slices must normalize to empty slices: %+v", got.Candidate)
	}
	if got.Lease.Held || got.Lease.SubjectID != nil || got.Lease.AcquiredUptimeMS != nil {
		t.Fatalf("free lease must project as unheld with null uptimes: %+v", got.Lease)
	}
	b1, err := got.bytes()
	if err != nil {
		t.Fatalf("bytes: %v", err)
	}
	b2, err := buildCanonicalPrepare("uuid-x", "00112233445566ff", rec, dispatch.Lease{}, tunables{}, nil, PrivateDispatchMountState{}, spawnCandidateProjection("0123456789abcdef", sp)).bytes()
	if err != nil {
		t.Fatalf("bytes: %v", err)
	}
	if string(b1) != string(b2) {
		t.Fatalf("canonical bytes are not deterministic:\n%s\n%s", b1, b2)
	}
}

// loadCollieProjection is the launch-generation observation the Collie-end
// fence compares: every active session's frozen collieCandidateState, ordered
// by session id. It must agree byte-for-byte with the single-session
// colliePrePrepareState — the marker-key comparison in the future selector
// depends on the two never drifting.
func TestDispatchSeamCollieProjection(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `
		INSERT INTO collie_sessions (id, container_name, verb_allowlist, session_path, binding)
		VALUES ('h-bbbbbbbbbbbbbbbb', 'mc-collie-h-bbbbbbbbbbbbbbbb', '[]', 'sessions/h-b', 'claude')`)
	dvExec(t, db, `
		INSERT INTO collie_sessions (id, container_name, verb_allowlist, session_path, binding)
		VALUES ('h-aaaaaaaaaaaaaaaa', 'mc-collie-h-aaaaaaaaaaaaaaaa', '[]', 'sessions/h-a', 'claude')`)
	dvExec(t, db, `
		UPDATE collie_sessions
		SET current_launch_id = 'aaaaaaaaaaaaaaaa', current_launch_mode = 'rows',
		    current_prime_through_seq = 7, current_prime_row_count = 3
		WHERE id = 'h-aaaaaaaaaaaaaaaa'`)
	dvExec(t, db, `
		INSERT INTO collie_sessions (id, container_name, verb_allowlist, session_path, binding, status)
		VALUES ('h-cccccccccccccccc', 'mc-collie-h-cccccccccccccccc', '[]', 'sessions/h-c', 'claude', 'ended')`)

	var got []collieCandidateState
	err := inTx(db, func(ctx context.Context, q Q) error {
		var e error
		got, e = loadCollieProjection(ctx, q)
		return e
	})
	if err != nil {
		t.Fatalf("loadCollieProjection: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("projection covers active sessions only, got %d entries: %+v", len(got), got)
	}
	if got[0].SessionID != "h-aaaaaaaaaaaaaaaa" || got[1].SessionID != "h-bbbbbbbbbbbbbbbb" {
		t.Fatalf("projection not ordered by session id: %q, %q", got[0].SessionID, got[1].SessionID)
	}
	if got[0].LaunchID == nil || *got[0].LaunchID != "aaaaaaaaaaaaaaaa" || got[0].LaunchMode == nil || *got[0].LaunchMode != "rows" {
		t.Fatalf("launch generation not observed: %+v", got[0])
	}
	if got[1].LaunchID != nil {
		t.Fatalf("no-launch session must project a null launch id: %+v", got[1])
	}

	// Drift guard: the projection row equals colliePrePrepareState's.
	for _, want := range got {
		var single collieCandidateState
		err := inTx(db, func(ctx context.Context, q Q) error {
			var e error
			single, _, e = colliePrePrepareState(ctx, q, want.SessionID)
			return e
		})
		if err != nil {
			t.Fatalf("colliePrePrepareState(%q): %v", want.SessionID, err)
		}
		pb, err := single.key()
		if err != nil {
			t.Fatalf("key: %v", err)
		}
		wb, err := want.key()
		if err != nil {
			t.Fatalf("key: %v", err)
		}
		if pb != wb {
			t.Fatalf("projection drifted from colliePrePrepareState for %q:\n%+v\n%+v", want.SessionID, want, single)
		}
	}
}

func TestDispatchSeamSubmittedVersionChangesPreparation(t *testing.T) {
	subject := int64(1)
	rec := dispatch.Records{Project: "ws-test", Tasks: []dispatch.Task{{ID: subject, Status: dispatch.StatusVerified, Delivery: dispatch.Delivery{VersionID: 3}}}}
	spawn := &dispatch.Spawn{Project: "ws-test", Role: dispatch.RolePackager, SubjectID: &subject}
	token := func() string {
		t.Helper()
		prepare := buildCanonicalPrepare("submitted-version", "00112233445566ff", rec, dispatch.Lease{}, tunables{}, nil, PrivateDispatchMountState{}, spawnCandidateProjection("0123456789abcdef", spawn))
		body, err := prepare.bytes()
		if err != nil {
			t.Fatal(err)
		}
		return preparationToken(body)
	}
	prior := token()
	rec.Tasks[0].Delivery.VersionID = 4
	if prior == token() {
		t.Fatal("the changed submitted version reused its preparation token")
	}
}
