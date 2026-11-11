package verbs

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"mc/trace"
)

func TestSetupTraceRegistrationRetainsOperationIdentityAndResult(t *testing.T) {
	for _, test := range []struct{ harness, kind string }{{"claude-sdk", "setup"}, {"codex", "setup"}, {"opencode", "setup"}, {"opencode", "validate"}} {
		t.Run(test.harness+"/"+test.kind, func(t *testing.T) {
			harness := test.harness
			db := environmentFixture(t)
			op := environmentWork(t, db)[0]
			if test.kind == "validate" {
				config := environmentTestConfiguration()
				config.Dockerfile = &EnvironmentFile{Path: ".culler/Dockerfile", Content: "FROM " + environmentBaseReference(environmentTestBase) + "\nRUN true\n"}
				view := environmentCall(t, db, environmentSetupID(op), "report", EnvironmentRequest{OperationID: op.ID, Outcome: "proposal", Configuration: &config, Summary: "The Project requires an image."})
				state := view["environment"].(EnvironmentState)
				environmentCall(t, db, nil, "approve", EnvironmentRequest{ProjectID: op.ProjectID, RevisionID: state.DraftRevisionID})
				build := environmentWork(t, db)[0]
				environmentCall(t, db, nil, "operation-result", EnvironmentRequest{OperationID: build.ID, InputsDigest: build.InputsDigest, Status: "succeeded", ImageDigest: environmentTestImage, RuntimeCompatible: true})
				op = environmentWork(t, db)[0]
				if op.Kind != "validate" {
					t.Fatalf("validation operation: %+v", op)
				}
			}
			runtime, _ := json.Marshal(PrivateRuntimeBinding{Harness: harness, ID: "fixture"})
			dvExec(t, db, `UPDATE environment_operations SET runtime_binding=?,status='succeeded',ended_at=datetime('now'),report_digest='retained' WHERE id=?`, string(runtime), op.ID)
			file := "native.jsonl"
			if harness == "codex" {
				file = "2026/10/12/rollout-fixture.jsonl"
			}
			if harness == "opencode" {
				file = "opencode/data/opencode/opencode.db"
			}
			before := environmentCall(t, db, nil, "get", EnvironmentRequest{ProjectID: op.ProjectID})
			for i := 0; i < 2; i++ {
				if _, err := RegisterSession(db, environmentSetupID(op), op.ID, "setup-native", file); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := RegisterSession(db, &RunIdentity{Tier: "setup", Role: "setup", RunID: "other"}, op.ID, "setup-native", file); err == nil {
				t.Fatal("another operation registered the trace")
			}
			if _, err := RegisterSession(db, environmentSetupID(op), op.ID, "different-native", file); err == nil {
				t.Fatal("conflicting native identity replaced the trace")
			}
			if _, err := RegisterSetupSession(db, nil, op.ID, "setup-native", file); err != nil {
				t.Fatal(err)
			}
			after := environmentCall(t, db, nil, "get", EnvironmentRequest{ProjectID: op.ProjectID})
			if !reflect.DeepEqual(before["environment"], after["environment"]) {
				t.Fatal("registration changed the environment")
			}
			var registered EnvironmentOperation
			err := inTx(db, func(ctx context.Context, q Q) error {
				var err error
				registered, err = environmentOperationTx(ctx, q, op.ID)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if registered.Status != "succeeded" || registered.ReportDigest != "retained" || registered.Attempt != op.Attempt || registered.Trace == nil {
				t.Fatalf("registration changed its result: %+v", registered)
			}
			listed, err := TraceSessions(db)
			if err != nil {
				t.Fatal(err)
			}
			rows := listed.(map[string]any)["sessions"].([]TraceSession)
			if len(rows) != 1 || rows[0].Kind != "setup" || rows[0].SessionPath != "environment-runs/"+op.ID+"/session" || !rows[0].Ended {
				t.Fatalf("wrong setup trace: %+v", rows)
			}
		})
	}
}

func TestRetainedOpenCodeSetupTraceUsesNativeLocation(t *testing.T) {
	db := environmentFixture(t)
	op := environmentWork(t, db)[0]
	legacy := "rollout-" + trace.OpenCodeID("ses_setup") + ".jsonl"
	dvExec(t, db, `UPDATE environment_operations SET runtime_binding='{"harness":"opencode","id":"fixture"}',
	 native_session_ref='ses_setup',trace_filename=? WHERE id=?`, legacy, op.ID)
	for _, filename := range []string{legacy, trace.OpenCodeDatabase} {
		if _, err := RegisterSetupSession(db, nil, op.ID, "ses_setup", filename); err != nil {
			t.Fatal(err)
		}
	}
	var stored string
	if err := db.QueryRow(`SELECT trace_filename FROM environment_operations WHERE id=?`, op.ID).Scan(&stored); err != nil || stored != legacy {
		t.Fatalf("retained setup locator changed: %q, %v", stored, err)
	}
	listed, err := TraceSessions(db)
	if err != nil {
		t.Fatal(err)
	}
	rows := listed.(map[string]any)["sessions"].([]TraceSession)
	if len(rows) != 1 || rows[0].TraceFilename != trace.OpenCodeDatabase || rows[0].LegacyFilename != legacy {
		t.Fatalf("retained setup trace: %+v", rows)
	}
}

func TestSetupTraceRecoveryCandidatesIncludeActiveAndFinishedOperations(t *testing.T) {
	db := environmentFixture(t)
	op := environmentWork(t, db)[0]
	dvExec(t, db, `UPDATE environment_operations SET runtime_binding='{"harness":"claude-sdk","id":"fixture"}' WHERE id=?`, op.ID)
	for _, ended := range []bool{false, true} {
		if ended {
			dvExec(t, db, `UPDATE environment_operations SET status='failed',ended_at=datetime('now') WHERE id=?`, op.ID)
		}
		listed, err := TraceSessions(db)
		if err != nil {
			t.Fatal(err)
		}
		pending := listed.(map[string]any)["setup_pending"].([]TraceSession)
		if len(pending) != 1 || pending[0].ID != op.ID || pending[0].Ended != ended || pending[0].Format != "claude" {
			t.Fatalf("recovery candidates: %+v", pending)
		}
	}
	for _, file := range []string{"../native.jsonl", "/native.jsonl", "folder/../native.jsonl", "folder\\native.jsonl", ".hidden/native.jsonl"} {
		if _, err := RegisterSession(db, environmentSetupID(op), op.ID, "native", file); err == nil {
			t.Errorf("unsafe file accepted: %q", file)
		}
	}
	if _, err := RegisterSession(db, environmentSetupID(op), op.ID, "../native", "native.jsonl"); err == nil {
		t.Fatal("unsafe session ID accepted")
	}
	if _, err := RegisterSetupSession(db, &RunIdentity{Tier: "pipeline", Role: "worker", RunID: op.ID}, op.ID, "native", "native.jsonl"); err == nil {
		t.Fatal("pipeline identity accepted")
	}
}
