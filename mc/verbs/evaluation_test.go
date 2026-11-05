package verbs

import (
	"context"
	"encoding/json"
	"fmt"
	"mc/dispatch"
	"mc/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvaluationCapturePreservesPipelineStateAndOriginalOwnership(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Capture the original packet")
	f.advance(task, "capture.txt", "Original source\n", "verified")
	run, root, id := f.run(task, "packager")
	if _, err := EvaluationRegister(f.db, run, "case-original", true); err != nil {
		t.Fatal(err)
	}
	if _, err := EvaluationRegister(f.db, run, "case-other", false); err != nil {
		t.Fatal(err)
	}
	if dfInt(t, f.db, `SELECT enabled FROM evaluation_captures WHERE run_id=?`, run) != 1 {
		t.Fatal("The flag change replaced the capture authority.")
	}
	before := dfStr(t, f.db, `SELECT status||'/'||blocked||'/'||archived||'/'||correction_open FROM tasks WHERE id=?`, task)
	packet := f.artifact(run, "The packet describes the source.")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "packaged", Outputs: packet, Thesis: "The packet describes the source."}); err != nil {
		t.Fatal(err)
	}
	f.cleanup(task, run, root)
	f.merge(task, "prepare")
	records, err := EvaluationRecords(f.db, run)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(records)
	for _, secret := range []string{"authorization_id", "connection_revision", "native_session_ref", "trace_filename"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("The records exposed %s.", secret)
		}
	}
	var packetID, version int64
	var hash string
	if err = f.db.QueryRow(`SELECT id,version_id,report_sha256 FROM review_versions WHERE task_id=?`, task).Scan(&packetID, &version, &hash); err != nil {
		t.Fatal(err)
	}
	if _, err = EvaluationStatus(f.db, run, "complete", "", packetID, hash); err != nil {
		t.Fatal(err)
	}
	if _, err = EvaluationStatus(f.db, run, "complete", "", packetID, "wrong"); err == nil {
		t.Fatal("The capture accepted the wrong packet hash.")
	}
	missing, err := EvaluationLookup(f.db, "another-project", task, packetID, version)
	if err != nil {
		t.Fatal(err)
	}
	if missing.(map[string]any)["available"] != false {
		t.Fatal("The lookup accepted another Project.")
	}
	lookup, err := EvaluationLookup(f.db, "ws-test", task, packetID, version)
	if err != nil {
		t.Fatal(err)
	}
	if lookup.(map[string]any)["available"] != true {
		t.Fatalf("The capture is unavailable: %v", lookup)
	}
	after := dfStr(t, f.db, `SELECT status||'/'||blocked||'/'||archived||'/'||correction_open FROM tasks WHERE id=?`, task)
	if before != "verified/0/0/0" || after != "packaged/0/0/0" {
		t.Fatalf("Unexpected pipeline state: %s %s", before, after)
	}
	f.transaction(func(ctx context.Context, q Q) error {
		_, err := q.ExecContext(ctx, `UPDATE projects SET status='paused' WHERE id='ws-test'`)
		return err
	})
	if _, err = EvaluationLookup(f.db, "ws-test", task, packetID, version); err != nil {
		t.Fatal(fmt.Errorf("The pause prevents a capture read: %w", err))
	}
}

func TestEvaluationCaptureDefersCleanupWithoutChargingTheTask(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Retain the original source")
	f.advance(task, "source.txt", "The source remains available.\n", "verified")
	run, root, id := f.run(task, "packager")
	if _, err := EvaluationRegister(f.db, run, "case-incomplete", true); err != nil {
		t.Fatal(err)
	}
	report := f.artifact(run, "The packet requires a source capture.")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "packaged", Outputs: report, Thesis: "Preserve the source"}); err != nil {
		t.Fatal(err)
	}
	clocks := fixClocks(t, f.db)
	before := dfInt(t, f.db, `SELECT dispatch_retries FROM tasks WHERE id=?`, task)
	for range 4 {
		effect := startOperation(t, f.db, dispatch.RepositoryOperation{Project: "ws-test", TaskID: task, Operation: dispatch.OperationCleanup, RunID: run})
		cleanup := effect["run_id"].(string)
		if _, err := RepositoryRecord(f.db, nil, "cleanup", cleanup, RepositoryResult{TaskID: task, EvaluationPending: true, CleanupError: "The source capture requires a retry."}); err != nil {
			t.Fatal(err)
		}
		if dfInt(t, f.db, `SELECT charged FROM repository_executions WHERE run_id=?`, cleanup) != 0 {
			t.Fatal("The capture charged the task.")
		}
		clocks.advance(domain.RetryDelay)
	}
	if dfInt(t, f.db, `SELECT dispatch_retries FROM tasks WHERE id=?`, task) != before {
		t.Fatal("The capture changed the dispatch allowance.")
	}
	if dfInt(t, f.db, `SELECT cleaned FROM repository_runs WHERE run_id=?`, run) != 0 {
		t.Fatal("The source became eligible for removal.")
	}
	if _, err := os.Stat(filepath.Join(root, "repo")); err != nil {
		t.Fatal(err)
	}
}
