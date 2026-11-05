package verbs

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"mc/dispatch"
	"mc/domain"
)

const environmentTestBase = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const environmentTestImage = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const environmentTestFiles = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
const environmentTestCommit = "dddddddddddddddddddddddddddddddddddddddd"

func environmentFixture(t *testing.T) *sql.DB {
	t.Helper()
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM project_environments WHERE project_id='ws-test'`)
	dvExec(t, db, `UPDATE projects SET status='paused',setup_status='configured' WHERE id='ws-test'`)
	err := inTx(db, func(ctx context.Context, q Q) error { return initializeEnvironmentTx(ctx, q, "ws-test", "vision-one") })
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func environmentCall(t *testing.T, db *sql.DB, id *RunIdentity, action string, req EnvironmentRequest) map[string]any {
	t.Helper()
	value, err := ProjectEnvironment(db, id, action, req)
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return value.(map[string]any)
}

func environmentWork(t *testing.T, db *sql.DB) []EnvironmentOperation {
	t.Helper()
	return environmentCall(t, db, nil, "work", EnvironmentRequest{DefaultImageDigest: environmentTestBase, RuntimeIdentity: "runtime-one", RuntimeBinding: PrivateRuntimeBinding{ID: "fake", Harness: "fake"}})["operations"].([]EnvironmentOperation)
}

func environmentSetupID(op EnvironmentOperation) *RunIdentity {
	return &RunIdentity{Tier: "setup", Role: "setup", RunID: op.ID}
}
func environmentTestConfiguration() EnvironmentConfiguration {
	return EnvironmentConfiguration{BaseDigest: environmentTestBase}
}

func TestEnvironmentStorageSeparatesHomeAndDisposableCache(t *testing.T) {
	db := environmentFixture(t)
	environmentWork(t, db)
	view := environmentCall(t, db, nil, "storage-report", EnvironmentRequest{
		ProjectID: "ws-test", ImageDigest: environmentTestBase,
		Metadata: map[string]any{"home_bytes": 31, "cache_bytes": 17, "image_bytes": 101},
	})
	disk := view["disk_use"].(map[string]any)
	for key, want := range map[string]float64{"home_bytes": 31, "cache_bytes": 17, "image_bytes": 101} {
		if disk[key] != want {
			t.Fatalf("%s = %v, want %v", key, disk[key], want)
		}
	}
}

func TestEnvironmentInitialSetupUsesOneAgentAndRetainedAcceptance(t *testing.T) {
	db := environmentFixture(t)
	first := environmentWork(t, db)
	again := environmentWork(t, db)
	if len(first) != 1 || first[0].Kind != "setup" || again[0].ID != first[0].ID {
		t.Fatalf("Work identity: %+v / %+v", first, again)
	}
	op := first[0]
	if op.WorkspaceRoot == "" || !strings.Contains(op.Brief, "separate Project setup") || len(op.Skills) < 2 {
		t.Fatalf("Setup input: %+v", op)
	}
	configuration := environmentTestConfiguration()
	report := EnvironmentRequest{OperationID: op.ID, Outcome: "complete", Summary: "The available Project preparation is complete.", Configuration: &configuration, FilesDigest: environmentTestFiles}
	view := environmentCall(t, db, environmentSetupID(op), "report", report)
	state := view["environment"].(EnvironmentState)
	if state.Readiness == "ready" {
		t.Fatal("The environment became ready before source acceptance.")
	}
	accept := environmentWork(t, db)
	if len(accept) != 1 || accept[0].Kind != "accept" {
		t.Fatalf("Acceptance: %+v", accept)
	}
	if got := dfStr(t, db, `SELECT setup_status FROM projects WHERE id='ws-test'`); got != "configured" {
		t.Fatal(got)
	}
	result := EnvironmentRequest{OperationID: accept[0].ID, InputsDigest: accept[0].InputsDigest, Status: "succeeded", SourceCommit: environmentTestCommit, FilesDigest: environmentTestFiles}
	view = environmentCall(t, db, nil, "operation-result", result)
	state = view["environment"].(EnvironmentState)
	if state.Readiness != "ready" || state.SelectedImageDigest != environmentTestBase {
		t.Fatalf("Accepted state: %+v", state)
	}
	if dfStr(t, db, `SELECT status FROM projects WHERE id='ws-test'`) != "active" {
		t.Fatal("Setup did not release the Project.")
	}
	environmentCall(t, db, nil, "operation-result", result)
	environmentCall(t, db, environmentSetupID(op), "report", report)
	if len(environmentWork(t, db)) != 0 {
		t.Fatal("Setup started a duplicate agent.")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM environment_operations WHERE kind IN ('setup','validate')`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("Agent count %d: %v", count, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM tasks`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("Setup created a task: %d, %v", count, err)
	}
}

func TestEnvironmentProposalApprovalBuildAndContinuation(t *testing.T) {
	db := environmentFixture(t)
	op := environmentWork(t, db)[0]
	c := environmentTestConfiguration()
	c.Dockerfile = &EnvironmentFile{Path: ".culler/Dockerfile", Content: "FROM " + environmentBaseReference(environmentTestBase) + "\nRUN apt-get update && apt-get install -y libpq-dev=16.10-0ubuntu0.24.04.1\n"}
	view := environmentCall(t, db, environmentSetupID(op), "report", EnvironmentRequest{OperationID: op.ID, Outcome: "proposal", Configuration: &c, Summary: "The database client requires PostgreSQL development files."})
	s := view["environment"].(EnvironmentState)
	if s.Phase != "approval" || s.ActiveOperationID != "" {
		t.Fatalf("Proposal state: %+v", s)
	}
	if len(environmentWork(t, db)) != 0 {
		t.Fatal("An unapproved image build started.")
	}
	approval := EnvironmentRequest{ProjectID: "ws-test", RevisionID: s.DraftRevisionID}
	environmentCall(t, db, nil, "approve", approval)
	environmentCall(t, db, nil, "approve", approval)
	build := environmentWork(t, db)[0]
	if build.Kind != "build" || build.Configuration.Dockerfile.Content != c.Dockerfile.Content {
		t.Fatalf("Build: %+v", build)
	}
	if _, err := ProjectEnvironment(db, environmentSetupID(op), "approve", approval); err == nil {
		t.Fatal("The setup agent approved its own image.")
	}
	result := EnvironmentRequest{OperationID: build.ID, InputsDigest: build.InputsDigest, Status: "succeeded", ImageDigest: environmentTestImage, RuntimeCompatible: true}
	environmentCall(t, db, nil, "operation-result", result)
	continued := environmentWork(t, db)[0]
	if continued.Kind != "validate" || continued.ImageDigest != environmentTestImage || continued.ProjectID != op.ProjectID {
		t.Fatalf("Continuation: %+v", continued)
	}
	changed := c
	changed.Dockerfile = &EnvironmentFile{Path: c.Dockerfile.Path, Content: c.Dockerfile.Content + "RUN echo changed\n"}
	if _, err := ProjectEnvironment(db, environmentSetupID(continued), "report", EnvironmentRequest{OperationID: continued.ID, Outcome: "complete", Summary: "The available Project preparation is complete.", Configuration: &changed, FilesDigest: environmentTestFiles}); err == nil {
		t.Fatal("Changed image instructions reused approval.")
	}
	environmentCall(t, db, environmentSetupID(continued), "report", EnvironmentRequest{OperationID: continued.ID, Outcome: "complete", Summary: "The available Project preparation is complete.", Configuration: &c, FilesDigest: environmentTestFiles})
	accept := environmentWork(t, db)[0]
	environmentCall(t, db, nil, "operation-result", EnvironmentRequest{OperationID: accept.ID, InputsDigest: accept.InputsDigest, Status: "succeeded", SourceCommit: environmentTestCommit, FilesDigest: environmentTestFiles})
	if dfStr(t, db, `SELECT selected_image_digest FROM project_environments WHERE project_id='ws-test'`) != ""+environmentTestImage {
		t.Fatal("The derived image was not selected.")
	}
}

func TestEnvironmentExecutionRetryFencesStaleResults(t *testing.T) {
	db := environmentFixture(t)
	op := environmentWork(t, db)[0]
	fail := EnvironmentRequest{OperationID: op.ID, InputsDigest: op.InputsDigest, Status: "failed", Error: "The network is unavailable."}
	environmentCall(t, db, nil, "operation-result", fail)
	environmentCall(t, db, nil, "operation-result", fail)
	dvExec(t, db, `UPDATE environment_operations SET retry_at=NULL`)
	replacement := environmentWork(t, db)[0]
	if replacement.ID == op.ID || replacement.Attempt != 2 {
		t.Fatalf("Replacement: %+v", replacement)
	}
	c := environmentTestConfiguration()
	if _, err := ProjectEnvironment(db, environmentSetupID(op), "report", EnvironmentRequest{OperationID: op.ID, Outcome: "complete", Summary: "The available Project preparation is complete.", Configuration: &c, FilesDigest: environmentTestFiles}); err == nil {
		t.Fatal("A replaced execution completed setup.")
	}
	environmentCall(t, db, nil, "operation-result", EnvironmentRequest{OperationID: replacement.ID, InputsDigest: replacement.InputsDigest, Status: "failed", Error: "The network is unavailable."})
	if len(environmentWork(t, db)) != 0 {
		t.Fatal("Culler queued a third attempt.")
	}
	if dfInt(t, db, `SELECT COUNT(*) FROM environment_operations`) != 2 {
		t.Fatal("Culler did not retain exactly two attempts.")
	}
	if dfStr(t, db, `SELECT phase FROM project_environments`) != "failed" {
		t.Fatal("The exhausted operation has no repair state.")
	}
}

func TestEnvironmentConfigurationProtectsPathsAndRuntime(t *testing.T) {
	for _, bad := range []string{"/etc/passwd", "../file", "a/../../file", ".git/config", "a\\b", "./file"} {
		t.Run(bad, func(t *testing.T) {
			c := environmentTestConfiguration()
			c.Declarations = []string{bad}
			if ValidateEnvironmentConfiguration(&c) == nil {
				t.Fatal("An invalid path passed.")
			}
		})
	}
	for _, name := range []string{"PATH", "MC_HOME", "LD_PRELOAD", "NODE_OPTIONS", "GCONV_PATH", "LOCPATH", "GLIBC_TUNABLES"} {
		c := environmentTestConfiguration()
		c.Variables = []EnvironmentVariable{{Name: name, Value: "x", Scope: "both"}}
		if ValidateEnvironmentConfiguration(&c) == nil {
			t.Fatalf("Reserved variable %s passed.", name)
		}
	}
	c := environmentTestConfiguration()
	c.Dockerfile = &EnvironmentFile{Path: "Dockerfile", Content: "FROM ubuntu:latest\nRUN true\n"}
	if ValidateEnvironmentConfiguration(&c) == nil {
		t.Fatal("A floating base passed.")
	}
	c = environmentTestConfiguration()
	c.Secrets = []EnvironmentVariable{{Name: "TOKEN", Reference: "token", Value: "secret", Scope: "setup"}}
	if ValidateEnvironmentConfiguration(&c) == nil {
		t.Fatal("A retained secret value passed.")
	}
}

func TestEnvironmentPipelineCandidatePreservesSourceReview(t *testing.T) {
	db := dvSpine(t)
	task := dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 1)
	dvInsertTask(t, db, task)
	_, effect := dvDispatch(t, db, dispatch.Records{Tasks: []dispatch.Task{task}}, dispatch.Lease{}, dvConfig())
	run := effect["run_id"].(string)
	c := environmentTestConfiguration()
	c.SourceCommit = environmentTestCommit
	c.Dockerfile = &EnvironmentFile{Path: "Dockerfile", Content: "FROM " + environmentBaseReference(environmentTestBase) + "\nRUN true\n"}
	if _, err := ProjectEnvironment(db, &RunIdentity{RunID: run, Tier: "pipeline", Role: "verifier"}, "propose", EnvironmentRequest{Configuration: &c, Explanation: "The compiler requires another system library."}); err == nil {
		t.Fatal("A Verifier created an image proposal without committed build files.")
	}
	view := environmentCall(t, db, &RunIdentity{RunID: run, Tier: "pipeline", Role: "worker"}, "propose", EnvironmentRequest{Configuration: &c, Explanation: "The native extension requires system development files."})
	s := view["environment"].(EnvironmentState)
	if s.SelectedImageDigest != environmentTestBase || s.Phase != "approval" {
		t.Fatalf("Proposal changed selection: %+v", s)
	}
	dvExec(t, db, `UPDATE runs SET ended_at=datetime('now'),outcome='completed' WHERE id=?`, run)
	environmentCall(t, db, nil, "approve", EnvironmentRequest{ProjectID: "ws-test", RevisionID: s.DraftRevisionID})
	build := environmentWork(t, db)[0]
	if build.Metadata["source_run_id"] != run || build.Metadata["candidate"] != true {
		t.Fatalf("Candidate source was lost: %+v", build.Metadata)
	}
	environmentCall(t, db, nil, "operation-result", EnvironmentRequest{OperationID: build.ID, InputsDigest: build.InputsDigest, Status: "succeeded", ImageDigest: environmentTestImage, RuntimeCompatible: true})
	validation := environmentWork(t, db)[0]
	if validation.Metadata["previous_operation_id"] != build.ID {
		t.Fatal("The build predecessor was lost.")
	}
	environmentCall(t, db, environmentSetupID(validation), "report", EnvironmentRequest{OperationID: validation.ID, Outcome: "complete", Summary: "The available Project preparation is complete.", Configuration: &c, FilesDigest: environmentTestFiles, SourceCommit: environmentTestCommit})
	accept := environmentWork(t, db)[0]
	if accept.Metadata["validation_operation_id"] != validation.ID || accept.Metadata["candidate"] != true {
		t.Fatalf("Acceptance lost its check container: %+v", accept.Metadata)
	}
	environmentCall(t, db, nil, "operation-result", EnvironmentRequest{OperationID: accept.ID, InputsDigest: accept.InputsDigest, Status: "succeeded", SourceCommit: environmentTestCommit, FilesDigest: environmentTestFiles})
	if dfStr(t, db, `SELECT selected_image_digest FROM project_environments`) != environmentTestBase {
		t.Fatal("Candidate validation bypassed source review.")
	}
	resolved := environmentCall(t, db, nil, "resolve", EnvironmentRequest{ProjectID: "ws-test", RunID: "candidate-check", SourceCommit: environmentTestCommit, RuntimeIdentity: "runtime-one"})
	if resolved["image_digest"] != environmentTestImage {
		t.Fatalf("Candidate checks selected the wrong image: %+v", resolved)
	}
	descendant := strings.Repeat("1", 40)
	dvExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,ended_at) VALUES('environment-descendant','pipeline','worker','ws-test',1,datetime('now'))`)
	dvExec(t, db, `INSERT INTO repository_runs(run_id,task_id,input_commit,destination_commit,temporary_bytes,captured,cleaned) VALUES('environment-descendant',1,?,?,0,1,1)`, environmentTestCommit, environmentTestCommit)
	dvExec(t, db, `INSERT INTO version_submissions(run_id,task_id,input_commit,result_commit) VALUES('environment-descendant',1,?,?)`, environmentTestCommit, descendant)
	dvExec(t, db, `INSERT INTO task_versions(task_id,run_id,input_commit,result_commit,destination_branch,retained_ref) VALUES(1,'environment-descendant',?,?,'main','refs/mc/environment-descendant')`, environmentTestCommit, descendant)
	resolved = environmentCall(t, db, nil, "resolve", EnvironmentRequest{ProjectID: "ws-test", SourceCommit: descendant})
	if resolved["image_digest"] != environmentTestImage {
		t.Fatal("A retained descendant lost its candidate environment.")
	}
	merged := strings.Repeat("e", 40)
	hook := EnvironmentRequest{ProjectID: "ws-test", SourceCommit: merged, CandidateSource: descendant}
	environmentCall(t, db, nil, "source-accepting", hook)
	environmentCall(t, db, nil, "source-accepting", hook)
	rec, err := loadRecords(context.Background(), db, "ws-test")
	if err != nil || rec.EnvironmentCondition != "environment-source-association" {
		t.Fatalf("Pending source condition: %s, %v", rec.EnvironmentCondition, err)
	}
	if _, err = ProjectEnvironment(db, nil, "resolve", EnvironmentRequest{ProjectID: "ws-test", SourceCommit: environmentTestCommit}); err == nil {
		t.Fatal("A pending association allowed another ordinary run.")
	}
	environmentCall(t, db, nil, "source-aborted", hook)
	environmentCall(t, db, nil, "resolve", EnvironmentRequest{ProjectID: "ws-test", SourceCommit: descendant})
	environmentCall(t, db, nil, "source-accepting", hook)
	environmentCall(t, db, nil, "source-accepted", hook)
	environmentCall(t, db, nil, "source-accepted", hook)
	if dfStr(t, db, `SELECT selected_image_digest FROM project_environments`) != environmentTestImage {
		t.Fatal("Source delivery did not select its checked environment.")
	}
	hook.SourceCommit = strings.Repeat("f", 40)
	if _, err := ProjectEnvironment(db, nil, "source-accepted", hook); err == nil {
		t.Fatal("A second source replaced the accepted association.")
	}
	ordinary := EnvironmentRequest{ProjectID: "ws-test", CandidateSource: strings.Repeat("2", 40), SourceCommit: strings.Repeat("3", 40)}
	for _, action := range []string{"source-accepting", "source-accepted", "source-aborted"} {
		environmentCall(t, db, nil, action, ordinary)
	}
}

func TestEnvironmentRuntimeFailurePreservesSelection(t *testing.T) {
	db := dvSpine(t)
	environmentCall(t, db, nil, "runtime-check", EnvironmentRequest{ProjectID: "ws-test", ImageDigest: environmentTestBase, RuntimeIdentity: "runtime-two", Status: "failed", Error: "The required Linux library is unavailable."})
	rec, err := loadRecords(context.Background(), db, "ws-test")
	if err != nil {
		t.Fatal(err)
	}
	if rec.EnvironmentCondition != "environment-runtime-incompatible" {
		t.Fatalf("Condition: %s", rec.EnvironmentCondition)
	}
	if _, err := ProjectEnvironment(db, nil, "resolve", EnvironmentRequest{ProjectID: "ws-test"}); err == nil {
		t.Fatal("The incompatible image allowed a new run.")
	}
	if dfStr(t, db, `SELECT selected_image_digest FROM project_environments`) != environmentTestBase {
		t.Fatal("A runtime failure changed the selected image.")
	}
	reference := "registry.example/culler-project@sha256:" + strings.Repeat("5", 64)
	record := EnvironmentRequest{BaseDigest: environmentTestBase, Metadata: map[string]any{"registry_reference": reference, "release_identity": "release-one"}}
	environmentCall(t, db, nil, "base-record", record)
	environmentCall(t, db, nil, "base-record", record)
	view := environmentCall(t, db, nil, "get", EnvironmentRequest{ProjectID: "ws-test"})
	if view["base_source"].(map[string]any)["registry_reference"] != reference {
		t.Fatal("The immutable base source is absent.")
	}
	record.Metadata["registry_reference"] = "registry.example/culler-project:latest"
	if _, err := ProjectEnvironment(db, nil, "base-record", record); err == nil {
		t.Fatal("A floating registry reference passed.")
	}
	environmentCall(t, db, nil, "retry", EnvironmentRequest{ProjectID: "ws-test"})
	operations := environmentCall(t, db, nil, "work", EnvironmentRequest{DefaultImageDigest: environmentTestBase, RuntimeIdentity: "runtime-two", RuntimeBinding: PrivateRuntimeBinding{ID: "fake", Harness: "fake"}})["operations"].([]EnvironmentOperation)
	if len(operations) != 1 || operations[0].Kind != "runtime-check" {
		t.Fatalf("Runtime repair started a setup agent: %+v", operations)
	}
	op := operations[0]
	if op.Metadata["base_source"].(map[string]any)["registry_reference"] != reference {
		t.Fatal("Runtime repair lost the retained base source.")
	}
	environmentCall(t, db, nil, "operation-result", EnvironmentRequest{OperationID: op.ID, InputsDigest: op.InputsDigest, Status: "succeeded", RuntimeCompatible: true})
	environmentCall(t, db, nil, "resolve", EnvironmentRequest{ProjectID: "ws-test"})
}

func TestEnvironmentMissingImageRebuildUsesApprovedInputsAndPreservesDraft(t *testing.T) {
	for _, digest := range []string{environmentTestImage, "sha256:" + strings.Repeat("f", 64)} {
		t.Run(digest, func(t *testing.T) { environmentTestMissingImageRepair(t, digest) })
	}
}

func environmentTestMissingImageRepair(t *testing.T, rebuilt string) {
	t.Helper()
	db := dvSpine(t)
	var selected EnvironmentRevision
	err := inTx(db, func(ctx context.Context, q Q) error {
		state, err := environmentStateTx(ctx, q, "ws-test")
		if err != nil {
			return err
		}
		configuration := environmentTestConfiguration()
		configuration.SourceCommit = environmentTestCommit
		configuration.Dockerfile = &EnvironmentFile{Path: ".culler/Dockerfile", Content: "FROM " + environmentBaseReference(environmentTestBase) + "\nRUN true\n"}
		selected, err = saveEnvironmentRevisionTx(ctx, q, state, configuration, "Accepted tools.", "operator")
		if err != nil {
			return err
		}
		return acceptEnvironmentTx(ctx, q, state, selected, environmentTestImage)
	})
	if err != nil {
		t.Fatal(err)
	}
	draft := selected.Configuration
	draft.Setup.Command = "npm ci"
	view := environmentCall(t, db, nil, "save", EnvironmentRequest{ProjectID: "ws-test", ExpectedRevisionID: selected.ID, Configuration: &draft})
	draftID := view["draft"].(*EnvironmentRevision).ID
	dvExec(t, db, `UPDATE project_environments SET phase='failed',cause='The draft check failed.',instruction='Repair this draft.' WHERE project_id='ws-test'`)
	failure := EnvironmentRequest{ProjectID: "ws-test", ImageDigest: environmentTestImage, RuntimeIdentity: "runtime-one", Status: "failed", Error: "The Project image is unavailable.", Metadata: map[string]any{"image_missing": true}}
	environmentCall(t, db, nil, "runtime-check", failure)
	currentSource := strings.Repeat("e", 40)
	retry := EnvironmentRequest{ProjectID: "ws-test", SourceCommit: currentSource, RuntimeBinding: PrivateRuntimeBinding{ID: "fake", Harness: "fake"}}
	environmentCall(t, db, nil, "retry", retry)
	environmentCall(t, db, nil, "retry", retry)
	build := environmentWork(t, db)[0]
	if build.Kind != "build" || build.Configuration.SourceCommit != currentSource || build.InputsDigest != selected.InputsDigest || build.Configuration.BaseDigest != selected.Configuration.BaseDigest {
		t.Fatalf("Repair inputs: %+v", build)
	}
	if build.Configuration.Setup.Command == draft.Setup.Command {
		t.Fatal("Repair adopted the unapproved draft.")
	}
	if dfStr(t, db, `SELECT selected_image_digest FROM project_environments`) != environmentTestImage {
		t.Fatal("Repair changed selection before checks.")
	}
	environmentCall(t, db, nil, "operation-result", EnvironmentRequest{OperationID: build.ID, InputsDigest: build.InputsDigest, Status: "succeeded", RuntimeCompatible: true, ImageDigest: rebuilt})
	validation := environmentWork(t, db)[0]
	if validation.Kind != "validate" || validation.ImageDigest != rebuilt {
		t.Fatalf("Repair validation: %+v", validation)
	}
	environmentCall(t, db, environmentSetupID(validation), "report", EnvironmentRequest{OperationID: validation.ID, Outcome: "complete", Summary: "The available Project preparation is complete.", Configuration: &validation.Configuration, SourceCommit: currentSource, FilesDigest: environmentTestFiles})
	accept := environmentWork(t, db)[0]
	environmentCall(t, db, nil, "operation-result", EnvironmentRequest{OperationID: accept.ID, InputsDigest: accept.InputsDigest, Status: "succeeded", SourceCommit: currentSource, FilesDigest: environmentTestFiles})
	view = environmentCall(t, db, nil, "get", EnvironmentRequest{ProjectID: "ws-test"})
	state := view["environment"].(EnvironmentState)
	if state.SelectedImageDigest != rebuilt || state.SelectedBaseDigest != environmentTestBase || state.DraftRevisionID != draftID || state.Phase != "failed" || state.Cause != "The draft check failed." || state.Instruction != "Repair this draft." {
		t.Fatalf("Repair acceptance: %+v", state)
	}
	if state.RuntimeCondition != "" || state.ImageMissing {
		t.Fatal("The rebuilt image retained the missing-image failure.")
	}
	for _, revision := range view["revisions"].([]EnvironmentRevision) {
		if revision.ID == selected.ID && revision.Configuration.SourceCommit != environmentTestCommit {
			t.Fatal("Repair changed the prior immutable revision.")
		}
	}
}

func TestEnvironmentExecutionEvidenceRequiresAssignedLivePreparation(t *testing.T) {
	db := dvSpine(t)
	task := dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 1)
	dvInsertTask(t, db, task)
	dvExec(t, db, `INSERT INTO execution_owners(owner) VALUES(1)`)
	dvExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,session_path,binding) VALUES('environment-prepare','pipeline','worker','ws-test',1,'sessions/environment-prepare','mechanical')`)
	dvExec(t, db, `INSERT INTO repository_executions(run_id,project,task_id,operation,target,owner,effect,boot_id,heartbeat_uptime_ms,deadline_uptime_ms) VALUES('environment-prepare','ws-test',1,'prepare','environment-prepare',1,'{}','fixture',0,300000)`)
	request := EnvironmentRequest{RunID: "environment-prepare", ToolVersions: map[string]string{"node": "v24.1.0"}, PackageRecords: []string{"package-lock.json sha256:example"}}
	if _, err := ProjectEnvironment(db, nil, "execution-evidence", request); err == nil {
		t.Fatal("An unassigned execution supplied evidence.")
	}
	environmentCall(t, db, nil, "resolve", EnvironmentRequest{ProjectID: "ws-test", RunID: request.RunID, SourceCommit: environmentTestCommit, RuntimeIdentity: "runtime-one"})
	if _, err := ProjectEnvironment(db, &RunIdentity{Tier: "pipeline", Role: "worker", RunID: request.RunID}, "execution-evidence", request); err == nil {
		t.Fatal("An agent supplied host evidence.")
	}
	environmentCall(t, db, nil, "execution-evidence", request)
	environmentCall(t, db, nil, "execution-evidence", request)
	request.ToolVersions["node"] = "v25.0.0"
	if _, err := ProjectEnvironment(db, nil, "execution-evidence", request); err == nil {
		t.Fatal("An execution replaced its evidence.")
	}
	dvExec(t, db, `UPDATE repository_executions SET ended_at=datetime('now'),end_reason='result',stopped_at=datetime('now') WHERE run_id='environment-prepare'`)
	if _, err := ProjectEnvironment(db, nil, "execution-evidence", request); err == nil {
		t.Fatal("An ended execution supplied evidence.")
	}
}

func TestEnvironmentBaseUsesLocalConfigurationDigest(t *testing.T) {
	configuration := environmentTestConfiguration()
	configuration.BaseDigest = "registry.example/culler-project@" + environmentTestBase
	if err := ValidateEnvironmentConfiguration(&configuration); err == nil {
		t.Fatal("A registry digest passed as a local image configuration digest.")
	}
}

func TestEnvironmentAbsentStateHoldsDispatch(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM project_environments`)
	rec, err := loadRecords(context.Background(), db, "ws-test")
	if err != nil {
		t.Fatal(err)
	}
	rec.VisionReady = true
	action := dispatch.Decide(rec, dispatch.Lease{}, dvConfig(), dvClock(t, db))
	if action.Kind != dispatch.KindIdle || action.Idle != "environment-setup-required" {
		t.Fatalf("Dispatch: %+v", action)
	}
}

func TestEnvironmentRunEvidenceBelongsToItsAssignedLiveRun(t *testing.T) {
	db := dvSpine(t)
	task := dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 1)
	dvInsertTask(t, db, task)
	_, effect := dvDispatch(t, db, dispatch.Records{Tasks: []dispatch.Task{task}}, dispatch.Lease{}, dvConfig())
	run := effect["run_id"].(string)
	id := &RunIdentity{RunID: run, Tier: "pipeline", Role: "worker"}
	req := EnvironmentRequest{RunID: run, ToolVersions: map[string]string{"node": "v24.1.0"}, PackageRecords: []string{"package-lock.json sha256:example"}}
	if _, err := ProjectEnvironment(db, id, "run-evidence", req); err == nil {
		t.Fatal("Evidence preceded the run assignment.")
	}
	environmentCall(t, db, nil, "resolve", EnvironmentRequest{ProjectID: "ws-test", RunID: run, SourceCommit: environmentTestCommit, RuntimeIdentity: "runtime-one"})
	if _, err := ProjectEnvironment(db, nil, "run-evidence", req); err == nil {
		t.Fatal("A host supplied agent observations.")
	}
	environmentCall(t, db, id, "run-evidence", req)
	environmentCall(t, db, id, "run-evidence", req)
	req.ToolVersions["node"] = "v25.0.0"
	if _, err := ProjectEnvironment(db, id, "run-evidence", req); err == nil {
		t.Fatal("Evidence changed after its first record.")
	}
	view := environmentCall(t, db, nil, "get", EnvironmentRequest{ProjectID: "ws-test"})
	if len(view["run_evidence"].([]map[string]any)) != 1 {
		t.Fatal("The Project view lost its run evidence.")
	}
}

func TestEnvironmentHomieReadUsesItsFrozenAuthority(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `INSERT INTO homie_sessions(id,container_name,verb_allowlist,session_path,binding,vision_project_id) VALUES('environment-homie','mc-environment-homie','["environment.get"]','sessions/environment-homie','fake/fake','ws-test')`)
	id := &RunIdentity{Tier: "homie", RunID: "environment-homie", VerbAllowlist: []string{"environment.get"}}
	req := EnvironmentRequest{ProjectID: "ws-test"}
	environmentCall(t, db, id, "get", req)
	for _, action := range []string{"save", "approve", "work", "operation-result", "deny"} {
		if _, err := ProjectEnvironment(db, id, action, req); err == nil {
			t.Fatalf("Homie gained environment mutation %s.", action)
		}
	}
	narrow := *id
	narrow.VerbAllowlist = nil
	if _, err := ProjectEnvironment(db, &narrow, "get", req); err == nil {
		t.Fatal("A narrower session gained an environment read.")
	}
	req.ProjectID = "ws-other"
	if _, err := ProjectEnvironment(db, id, "get", req); err == nil {
		t.Fatal("A Vision conversation crossed its Project.")
	}
}

func TestEnvironmentRemovalRetainsExactPriorFilesThroughAcceptance(t *testing.T) {
	db := dvSpine(t)
	var prior EnvironmentRevision
	err := inTx(db, func(ctx context.Context, q Q) error {
		s, err := environmentStateTx(ctx, q, "ws-test")
		if err != nil {
			return err
		}
		c := environmentTestConfiguration()
		c.SourceCommit = environmentTestCommit
		c.Dockerfile = &EnvironmentFile{Path: ".culler/Dockerfile", Content: "FROM " + environmentBaseReference(environmentTestBase) + "\nCOPY prerequisite.txt /opt/prerequisite.txt\n"}
		c.BuildFiles = []EnvironmentFile{{Path: "prerequisite.txt", Content: "prior content\n"}}
		prior, err = saveEnvironmentRevisionTx(ctx, q, s, c, "Prior accepted tools.", "operator")
		if err != nil {
			return err
		}
		return acceptEnvironmentTx(ctx, q, s, prior, environmentTestImage)
	})
	if err != nil {
		t.Fatal(err)
	}
	c := environmentTestConfiguration()
	c.SourceCommit = environmentTestCommit
	view := environmentCall(t, db, nil, "save", EnvironmentRequest{ProjectID: "ws-test", ExpectedRevisionID: prior.ID, Configuration: &c})
	r := view["draft"].(*EnvironmentRevision)
	environmentCall(t, db, nil, "approve", EnvironmentRequest{ProjectID: "ws-test", RevisionID: r.ID})
	validation := environmentWork(t, db)[0]
	if validation.Kind != "validate" || validation.ImageDigest != environmentTestBase {
		t.Fatalf("Removal selected the wrong operation: %+v", validation)
	}
	removed, ok := validation.Metadata["removed_files"].([]any)
	if !ok || len(removed) != 2 {
		t.Fatalf("The removed files are absent: %+v", validation.Metadata)
	}
	if removed[0].(map[string]any)["content"] != "prior content\n" {
		t.Fatal("Removal lost the prior exact contents.")
	}
	environmentCall(t, db, environmentSetupID(validation), "report", EnvironmentRequest{OperationID: validation.ID, Outcome: "complete", Summary: "The available Project preparation is complete.", Configuration: &c, FilesDigest: environmentTestFiles, SourceCommit: environmentTestCommit})
	accept := environmentWork(t, db)[0]
	if len(accept.Metadata["removed_files"].([]any)) != 2 {
		t.Fatal("Acceptance lost the explicit removals.")
	}
}

func TestEnvironmentRuntimeChangeCreatesRetainedContinuation(t *testing.T) {
	db := environmentFixture(t)
	prior := environmentWork(t, db)[0]
	environmentCall(t, db, environmentSetupID(prior), "heartbeat", EnvironmentRequest{OperationID: prior.ID})
	req := EnvironmentRequest{DefaultImageDigest: environmentTestBase, RuntimeIdentity: "runtime-two", RuntimeBinding: PrivateRuntimeBinding{ID: "fake", Harness: "fake"}}
	operations := environmentCall(t, db, nil, "work", req)["operations"].([]EnvironmentOperation)
	if len(operations) != 1 {
		t.Fatalf("The runtime change queued %d operations.", len(operations))
	}
	current := operations[0]
	if current.ID == prior.ID || current.RuntimeIdentity != "runtime-two" || current.InputsDigest != prior.InputsDigest || current.Metadata["previous_operation_id"] != prior.ID {
		t.Fatalf("The continuation lost its identity: %+v", current)
	}
	if dfStr(t, db, `SELECT runtime_identity FROM environment_operations WHERE id=?`, prior.ID) != "runtime-one" || dfStr(t, db, `SELECT status FROM environment_operations WHERE id=?`, prior.ID) != "failed" {
		t.Fatal("The runtime change rewrote or retained the old active operation.")
	}
	again := environmentCall(t, db, nil, "work", req)["operations"].([]EnvironmentOperation)
	if len(again) != 1 || again[0].ID != current.ID {
		t.Fatal("The runtime change created duplicate continuations.")
	}
	if _, err := ProjectEnvironment(db, environmentSetupID(prior), "report", EnvironmentRequest{OperationID: prior.ID, Outcome: "blocked", Error: "Late result."}); err == nil {
		t.Fatal("The old runtime changed its replacement operation.")
	}
}

func TestEnvironmentRevisionRequiresRecordedCullerBase(t *testing.T) {
	db := environmentFixture(t)
	configuration := environmentTestConfiguration()
	configuration.BaseDigest = "sha256:" + strings.Repeat("9", 64)
	request := EnvironmentRequest{ProjectID: "ws-test", Configuration: &configuration, Explanation: "Select a different base."}
	if _, err := ProjectEnvironment(db, nil, "save", request); err == nil {
		t.Fatal("An unrecorded local image became a Culler base.")
	}
	op := environmentWork(t, db)[0]
	identity := environmentSetupID(op)
	record := EnvironmentRequest{BaseDigest: configuration.BaseDigest, Metadata: map[string]any{"release_identity": "release-two"}}
	if _, err := ProjectEnvironment(db, identity, "base-record", record); err == nil {
		t.Fatal("A setup agent recorded its own trusted base.")
	}
	proposal := EnvironmentRequest{OperationID: op.ID, Outcome: "proposal", Configuration: &configuration, Summary: "Select a different base."}
	if _, err := ProjectEnvironment(db, identity, "report", proposal); err == nil {
		t.Fatal("A setup agent proposed an unrecorded local image.")
	}
	environmentCall(t, db, nil, "base-record", record)
	environmentCall(t, db, identity, "report", proposal)
	view := environmentCall(t, db, nil, "get", EnvironmentRequest{ProjectID: "ws-test"})
	if view["draft"].(*EnvironmentRevision).Configuration.BaseDigest != configuration.BaseDigest {
		t.Fatal("The recorded Culler base was unavailable.")
	}
}

func TestEnvironmentWorkRecordsLocalDefaultBase(t *testing.T) {
	db := environmentFixture(t)
	dvExec(t, db, `DELETE FROM environment_base_sources`)
	environmentWork(t, db)
	if dfStr(t, db, `SELECT base_digest FROM environment_base_sources`) != environmentTestBase {
		t.Fatal("Work did not retain its trusted default base.")
	}
	if dfStr(t, db, `SELECT release_identity FROM environment_base_sources`) != "runtime-one" {
		t.Fatal("The default base lost its Culler release identity.")
	}
}

func TestEnvironmentBaseSourcePrefersPublishedDigest(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM environment_base_sources`)
	reference := "registry.example/culler-project@sha256:" + strings.Repeat("5", 64)
	environmentCall(t, db, nil, "base-record", EnvironmentRequest{BaseDigest: environmentTestBase, Metadata: map[string]any{"registry_reference": reference, "release_identity": "published-release"}})
	environmentCall(t, db, nil, "base-record", EnvironmentRequest{BaseDigest: environmentTestBase, Metadata: map[string]any{"release_identity": "local-release"}})
	view := environmentCall(t, db, nil, "get", EnvironmentRequest{ProjectID: "ws-test"})
	if view["base_source"].(map[string]any)["registry_reference"] != reference {
		t.Fatal("A local base record hid its exact registry source.")
	}
}

func TestEnvironmentCacheClearWaitsForRepositoryExecution(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 1))
	dvExec(t, db, `INSERT INTO execution_owners(owner) VALUES(1)`)
	dvExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,session_path,binding) VALUES('cache-prepare','pipeline','worker','ws-test',1,'sessions/cache-prepare','mechanical')`)
	dvExec(t, db, `INSERT INTO repository_executions(run_id,project,task_id,operation,target,owner,effect,boot_id,heartbeat_uptime_ms,deadline_uptime_ms) VALUES('cache-prepare','ws-test',1,'prepare','cache-prepare',1,'{}','fixture',0,300000)`)
	dvExec(t, db, `UPDATE runs SET ended_at=datetime('now'),end_reason='agent' WHERE id='cache-prepare'`)
	request := EnvironmentRequest{ProjectID: "ws-test"}
	if _, err := ProjectEnvironment(db, nil, "cache-clear", request); err == nil {
		t.Fatal("Cache removal overlapped an active repository execution.")
	}
	dvExec(t, db, `UPDATE repository_executions SET ended_at=datetime('now'),end_reason='result',stopped_at=datetime('now') WHERE run_id='cache-prepare'`)
	view := environmentCall(t, db, nil, "cache-clear", request)
	if view["environment"].(EnvironmentState).ActiveOperationID == "" {
		t.Fatal("Cache removal remained blocked after repository execution ended.")
	}
}

func TestEnvironmentCacheClearHoldsNewPreparation(t *testing.T) {
	db := dvSpine(t)
	stMergeable(t, db, 1)
	dvExec(t, db, `UPDATE tasks SET candidate_id=NULL WHERE id=1`)
	environmentCall(t, db, nil, "cache-clear", EnvironmentRequest{ProjectID: "ws-test"})
	stWantStarted(t, db, startRequest(t, db, false))
	op := environmentWork(t, db)[0]
	if op.Kind != "cache-clear" {
		t.Fatalf("The cache operation changed kind: %+v", op)
	}
	environmentCall(t, db, nil, "operation-result", EnvironmentRequest{OperationID: op.ID, InputsDigest: op.InputsDigest, Status: "succeeded"})
	stWantStarted(t, db, startRequest(t, db, false), "prepare/1")
}

func TestEnvironmentCacheClearPermitsRequiredReceiptRecovery(t *testing.T) {
	db := dvSpine(t)
	stMergeable(t, db, 1)
	clocks := fixClocks(t, db)
	stApprove(t, db, 1)
	started := startRequest(t, db, false)
	stWantStarted(t, db, started, "apply/1")
	stFail(t, db, started.Started[0], false)
	environmentCall(t, db, nil, "cache-clear", EnvironmentRequest{ProjectID: "ws-test"})
	clocks.advance(domain.RetryDelay + time.Second)
	stWantStarted(t, db, startRequest(t, db, false), "reconcile/1")
}

func TestEnvironmentCacheClearPreservesDraftApproval(t *testing.T) {
	for _, status := range []string{"succeeded", "failed"} {
		t.Run(status, func(t *testing.T) {
			db := dvSpine(t)
			configuration := environmentTestConfiguration()
			configuration.SourceCommit = environmentTestCommit
			view := environmentCall(t, db, nil, "save", EnvironmentRequest{ProjectID: "ws-test", ExpectedRevisionID: "fixture-environment-ws-test", Configuration: &configuration})
			draft := view["environment"].(EnvironmentState).DraftRevisionID
			dvExec(t, db, `UPDATE project_environments SET phase='approval',instruction='Review this draft.' WHERE project_id='ws-test'`)
			environmentCall(t, db, nil, "cache-clear", EnvironmentRequest{ProjectID: "ws-test"})
			for attempt := 1; attempt <= 2; attempt++ {
				dvExec(t, db, `UPDATE environment_operations SET retry_at=NULL WHERE status='queued'`)
				op := environmentWork(t, db)[0]
				request := EnvironmentRequest{OperationID: op.ID, InputsDigest: op.InputsDigest, Status: status}
				if status == "failed" {
					request.Error = "The cache volume is unavailable."
				}
				environmentCall(t, db, nil, "operation-result", request)
				if status == "succeeded" {
					break
				}
			}
			state := environmentCall(t, db, nil, "get", EnvironmentRequest{ProjectID: "ws-test"})["environment"].(EnvironmentState)
			if state.DraftRevisionID != draft || state.Phase != "approval" || state.Instruction != "Review this draft." || state.ActiveOperationID != "" || state.Readiness != "ready" {
				t.Fatalf("Cache removal changed the draft approval: %+v", state)
			}
		})
	}
}

func TestEnvironmentFailureDetailsRemainWithTheFailedAttempt(t *testing.T) {
	db := environmentFixture(t)
	op := environmentWork(t, db)[0]
	result := EnvironmentRequest{OperationID: op.ID, InputsDigest: op.InputsDigest, Status: "failed", Error: "The harness request failed.", Metadata: map[string]any{"failure_details": "The harness diagnostic record."}}
	environmentCall(t, db, nil, "operation-result", result)
	environmentCall(t, db, nil, "operation-result", result)
	if got := dfStr(t, db, `SELECT json_extract(metadata,'$.failure_details') FROM environment_operations WHERE id=?`, op.ID); got != "The harness diagnostic record." {
		t.Fatalf("Details: %q", got)
	}
	dvExec(t, db, `UPDATE environment_operations SET retry_at=NULL`)
	retry := environmentWork(t, db)[0]
	if retry.Metadata["failure_details"] != nil {
		t.Fatal("The retry inherited the failed attempt's details.")
	}
}

func TestEnvironmentReportSurvivesLaterHarnessFailure(t *testing.T) {
	db := environmentFixture(t)
	op := environmentWork(t, db)[0]
	configuration := environmentTestConfiguration()
	environmentCall(t, db, environmentSetupID(op), "report", EnvironmentRequest{OperationID: op.ID, Outcome: "complete", Summary: "The Project requires a host capability.", Configuration: &configuration, FilesDigest: environmentTestFiles})
	result := EnvironmentRequest{OperationID: op.ID, InputsDigest: op.InputsDigest, Status: "failed", Error: "The harness stopped after the report.", Metadata: map[string]any{"failure_details": "Later process output."}}
	environmentCall(t, db, nil, "operation-result", result)
	environmentCall(t, db, nil, "operation-result", result)
	if got := dfStr(t, db, `SELECT summary FROM environment_results WHERE operation_id=?`, op.ID); got != "The Project requires a host capability." {
		t.Fatalf("Report cause: %q", got)
	}
	if got := dfStr(t, db, `SELECT status FROM environment_operations WHERE id=?`, op.ID); got != "succeeded" {
		t.Fatalf("Report status: %q", got)
	}
}

func TestEnvironmentSelectionsCarryTheLastRuntimeCheck(t *testing.T) {
	db := dvSpine(t)
	selection := func() map[string]any {
		for _, row := range environmentCall(t, db, nil, "selections", EnvironmentRequest{})["selections"].([]map[string]any) {
			if row["project_id"] == "ws-test" {
				return row
			}
		}
		t.Fatal("The selections omit ws-test.")
		return nil
	}
	before := selection()
	if before["image_digest"] != environmentTestBase || before["checked_status"] != "" || before["checked_image_digest"] != "" || before["checked_runtime_identity"] != "" {
		t.Fatalf("selection before a runtime check: %+v", before)
	}
	environmentCall(t, db, nil, "runtime-check", EnvironmentRequest{ProjectID: "ws-test", ImageDigest: environmentTestBase, RuntimeIdentity: "runtime-two", Status: "succeeded"})
	after := selection()
	if after["checked_status"] != "succeeded" || after["checked_image_digest"] != environmentTestBase || after["checked_runtime_identity"] != "runtime-two" {
		t.Fatalf("selection after a runtime check: %+v", after)
	}
}
