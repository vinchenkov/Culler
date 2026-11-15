package verbs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mc/dispatch"
	"mc/domain"
	"mc/repository"
)

// testOwner returns the current resident owner. It registers one when the
// spine has none.
func testOwner(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var owner sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(owner) FROM execution_owners`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner.Valid {
		return owner.Int64
	}
	result, err := DeliveryRegister(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	return result.(map[string]any)["owner"].(int64)
}

// startOperation starts one repository operation as a start request starts
// it, without the selection rules, and returns the stored effect.
func startOperation(t *testing.T, db *sql.DB, op dispatch.RepositoryOperation) map[string]any {
	t.Helper()
	owner := testOwner(t, db)
	var run string
	if err := inTx(db, func(ctx context.Context, q Q) error {
		clock, err := vmClock()
		if err != nil {
			return err
		}
		run, err = startRepositoryOperation(ctx, q, clock, owner, op)
		return err
	}); err != nil {
		t.Fatalf("start %s of %s: %v", op.Operation, op.Subject().Target(), err)
	}
	return executionEffect(t, db, run)
}

// executionEffect returns the stored effect of an execution.
func executionEffect(t *testing.T, db *sql.DB, run string) map[string]any {
	t.Helper()
	var body string
	if err := db.QueryRow(`SELECT effect FROM repository_executions WHERE run_id=?`, run).Scan(&body); err != nil {
		t.Fatal(err)
	}
	return decodeEffect(t, []byte(body))
}

// decodeEffect decodes one effect with integer numbers and a typed application.
func decodeEffect(t *testing.T, body []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var effect map[string]any
	if err := decoder.Decode(&effect); err != nil {
		t.Fatal(err)
	}
	for key, value := range effect {
		if number, ok := value.(json.Number); ok {
			n, err := number.Int64()
			if err != nil {
				t.Fatal(err)
			}
			effect[key] = n
		}
	}
	if raw, ok := effect["application"]; ok && raw != nil {
		encoded, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		var application repository.Application
		if err := json.Unmarshal(encoded, &application); err != nil {
			t.Fatal(err)
		}
		effect["application"] = application
	}
	return effect
}

// runExecution executes the host work of one started execution as the
// resident does, and records its result.
func (f *deliveryFixture) runExecution(effect map[string]any) RepositoryResult {
	f.t.Helper()
	result := f.nativeExecution(effect)
	f.record(effect["operation"].(string), effect["run_id"].(string), result)
	return result
}

// nativeExecution executes the host work of one started execution as the
// resident does. It records each repository of a merge candidate step. It
// returns the result of the operation and does not record that result.
func (f *deliveryFixture) nativeExecution(effect map[string]any) RepositoryResult {
	f.t.Helper()
	operation, run := effect["operation"].(string), effect["run_id"].(string)
	envelope := f.envelope(effect)
	if operation == "prepare" {
		envelope.Operation = "allocate"
		allocated := f.native(envelope)
		f.record("ready", run, allocated)
		envelope.InputCommit, envelope.DestinationCommit = allocated.InputCommit, allocated.DestinationCommit
		envelope.Operation = "prepare-repository"
		f.record("ready", run, f.native(envelope))
		envelope.Operation = "prepare"
		prepared := f.native(envelope)
		if f.afterPrepare != nil {
			f.afterPrepare(*prepared.Candidate)
		}
		envelope.Operation, envelope.Candidate = "retain-candidate", prepared.Candidate
	}
	return f.native(envelope)
}

// wikiRoot returns the run root of one temporary repository of a wiki update.
func (f *deliveryFixture) wikiRoot(run string) string {
	return filepath.Join(f.wiki, run)
}

// envelope builds the native envelope of one started execution as the resident builds it.
func (f *deliveryFixture) envelope(effect map[string]any) RepositoryEnvelope {
	f.t.Helper()
	operation, run := effect["operation"].(string), effect["run_id"].(string)
	subject := run
	switch operation {
	case "capture", "cleanup":
		subject = effect["producer_run"].(string)
	case "reconcile":
		subject = effect["previous_run"].(string)
	}
	envelope := RepositoryEnvelope{
		Operation: operation, RunID: subject,
		WorkspaceRoot: f.workspace, RunRoot: filepath.Join(f.root, subject),
		DestinationBranch: stringField(effect, "destination_branch"), ResultCommit: stringField(effect, "result_commit"),
		SubmissionKind: stringField(effect, "submission_kind"), InputCommit: stringField(effect, "input_commit"),
		StartCommit: stringField(effect, "start_commit"),
		AuthorName:  "Test", AuthorEmail: "test@example.invalid",
		artifactRoots: f.artifactRoots,
	}
	if wiki := stringField(effect, "wiki_update"); wiki != "" {
		envelope.WikiUpdate, envelope.RunRoot = wiki, f.wikiRoot(subject)
	} else {
		envelope.TaskID = effect["task_id"].(int64)
	}
	if envelope.InputCommit == "" {
		envelope.InputCommit = stringField(effect, "submitted_commit")
	}
	if application, ok := effect["application"].(repository.Application); ok {
		envelope.Application = &application
	}
	if version, ok := effect["version_id"].(int64); ok {
		envelope.VersionID = &version
	}
	if maximum, ok := effect["max_temporary_bytes"].(int64); ok {
		envelope.MaxTemporaryBytes = maximum
	}
	return envelope
}

// fixturePlane is the mount allowlist target of the Project artifact root. An
// earlier onboarding of the workspace named it, so it differs from the Project id.
const fixturePlane = "earlier-project"

// artifactRoots maps the mount allowlist target of the Project artifact root to
// its host directory. The fixture artifact directory stands in for the container
// artifact root, so a link there binds the plane to the Project artifact root.
func (f *deliveryFixture) artifactRoots() (map[string]string, error) {
	if _, _, err := EnsureOnboardWorkspaceLayout([]OnboardWorkspaceRoot{{ID: "ws-test", Root: f.workspace}}, os.Getuid()); err != nil {
		return nil, err
	}
	plane := filepath.Join(f.artifacts, fixturePlane)
	if err := os.Symlink(ProjectArtifactRoot(f.workspace), plane); err != nil && !os.IsExist(err) {
		return nil, err
	}
	return map[string]string{fixturePlane: plane}, nil
}

func stringField(effect map[string]any, key string) string {
	value, _ := effect[key].(string)
	return value
}

// native runs one native repository operation.
func (f *deliveryFixture) native(envelope RepositoryEnvelope) RepositoryResult {
	f.t.Helper()
	result, err := executeRepository(context.Background(), envelope)
	if err != nil {
		f.t.Fatalf("the %s operation failed: %v", envelope.Operation, err)
	}
	return result
}

// record records one repository result.
func (f *deliveryFixture) record(operation, run string, result RepositoryResult) {
	f.t.Helper()
	if _, err := RepositoryRecord(f.db, nil, operation, run, result); err != nil {
		f.t.Fatalf("the %s result failed: %v", operation, err)
	}
}

// startRequest runs one start request of the current owner.
func startRequest(t *testing.T, db *sql.DB, drain bool) StartResponse {
	t.Helper()
	resp, err := DeliveryStart(db, nil, StartRequest{Owner: testOwner(t, db), Drain: drain})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// testClocks holds the spine clock and the VM clock of one test.
type testClocks struct {
	now time.Time
	vm  domain.VMClock
}

// fixClocks replaces the spine clock and the VM clock with their current
// values. The clocks move only when the test advances them.
func fixClocks(t *testing.T, db *sql.DB) *testClocks {
	t.Helper()
	c := &testClocks{}
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		c.now, err = spineNow(ctx, q)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var err error
	if c.vm, err = domain.ReadVMClock(); err != nil {
		t.Fatal(err)
	}
	previousSpine, previousVM := spineClock, vmClock
	t.Cleanup(func() { spineClock, vmClock = previousSpine, previousVM })
	spineClock = func(context.Context, Q) (time.Time, error) { return c.now, nil }
	vmClock = func() (domain.VMClock, error) { return c.vm, nil }
	return c
}

// advance moves the spine clock and the VM clock forward.
func (c *testClocks) advance(d time.Duration) {
	c.now = c.now.Add(d)
	c.vm.Uptime += d
}

// startedOperations names each execution that a start response started, as
// operation/task. An execution of a wiki update is operation/wiki.
func startedOperations(t *testing.T, db *sql.DB, resp StartResponse) []string {
	t.Helper()
	var out []string
	for _, run := range resp.Started {
		var operation string
		var task sql.NullInt64
		if err := db.QueryRow(`SELECT operation,task_id FROM repository_executions WHERE run_id=?`, run).Scan(&operation, &task); err != nil {
			t.Fatal(err)
		}
		if !task.Valid {
			out = append(out, operation+"/wiki")
			continue
		}
		out = append(out, fmt.Sprintf("%s/%d", operation, task.Int64))
	}
	return out
}

// wantStart fails unless one start request of the records at now starts the operation of the task.
func wantStart(t *testing.T, records dispatch.Records, now time.Time, operation dispatch.Operation, task int64) {
	t.Helper()
	for _, op := range dispatch.SelectStarts([]dispatch.ProjectInput{{Records: records}}, now, false) {
		if op.Operation == operation && op.TaskID == task {
			return
		}
	}
	t.Fatalf("no start request starts the %s of task %d", operation, task)
}
