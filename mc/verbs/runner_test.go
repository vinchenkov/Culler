package verbs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"mc/domain"
)

// rnStale reports whether the error refuses a run that is not active.
func rnStale(err error) bool {
	var refused *DomainError
	return errors.As(err, &refused) && refused.Code == domain.CodeStaleRun
}

// The run of a wiki update holds no lease. Each runner command reads the wiki
// update in place of the lease.
func TestRunnerCommandsAcceptAWikiUpdateRun(t *testing.T) {
	db := dvSpine(t)
	run := wrWantSpawn(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test")
	id := &RunIdentity{Tier: "pipeline", Role: "scribe", RunID: run}
	leases := wrDump(t, db, `SELECT * FROM leases ORDER BY project`)
	liveness := func() string {
		return wrDump(t, db, `SELECT w.last_heartbeat_at, w.heartbeat_uptime_ms IS NOT NULL AS uptime, r.agent_started_at IS NOT NULL AS started,
			w.stopped_at IS NOT NULL AS stopped FROM wiki_updates w JOIN runs r ON r.id=w.run_id WHERE w.run_id=?`, run)
	}
	if got := liveness(); got != "last_heartbeat_at=<nil>|uptime=0|started=0|stopped=0|\n" {
		t.Fatalf("liveness facts of a new wiki update run = %s", got)
	}

	result, err := Heartbeat(db, id, run)
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	stamp, _ := result.(map[string]any)["last_heartbeat_at"].(string)
	if stamp == "" || result.(map[string]any)["run_id"] != run {
		t.Fatalf("heartbeat result = %v, want the run and the stamp", result)
	}
	if got := liveness(); got != fmt.Sprintf("last_heartbeat_at=%s|uptime=1|started=0|stopped=0|\n", stamp) {
		t.Fatalf("liveness facts after the heartbeat = %s, want the stamp on the wiki update", got)
	}

	if result, err := RunProcessStarted(db, id, run); err != nil || result.(map[string]any)["agent_started"] != true {
		t.Fatalf("process start = %v, %v", result, err)
	}
	if result, err := RunModelResponse(db, id, run); err != nil || result.(map[string]any)["recovered"] != false {
		t.Fatalf("model response = %v, %v", result, err)
	}
	if _, err := RegisterSession(db, id, run, "native-session", "trace.jsonl"); err != nil {
		t.Fatalf("register session: %v", err)
	}
	if result, err := RunLaunchCheck(db, nil, run); err != nil || result.(map[string]any)["allowed"] != true {
		t.Fatalf("launch check = %v, %v", result, err)
	}
	if _, err := RunStopped(db, nil, run); !rnStale(err) {
		t.Fatalf("stop confirmation of an active run = %v, want the refusal of a run with no outcome", err)
	}
	// The terminal action of a task run ends no wiki update run.
	if err := inTx(db, func(ctx context.Context, q Q) error { return endRun(ctx, q, run, "completed") }); err == nil ||
		!strings.Contains(err.Error(), "a wiki update run ends through domain.EndWikiRun") {
		t.Fatalf("the end of a task run for a wiki update run = %v, want a refusal", err)
	}
	active := fmt.Sprintf("last_heartbeat_at=%s|uptime=1|started=1|stopped=0|\n", stamp)
	if got := liveness(); got != active {
		t.Fatalf("liveness facts of the active run = %swant %s", got, active)
	}
	if got := wrDump(t, db, `SELECT native_session_ref, trace_filename, ended_at FROM runs WHERE id=?`, run); got !=
		"native_session_ref=native-session|trace_filename=trace.jsonl|ended_at=<nil>|\n" {
		t.Fatalf("run row = %s, want the session locators and no end", got)
	}

	// The failure report of the runner ends the run. The end releases no
	// lease. Each command that needs an active run then refuses.
	report := domain.InfrastructureFailureReport{Source: "runner", Condition: "The agent program ended with the exit code 1."}
	wrReport(t, db, id, run, "ws-test", report)
	ended := wrEndLine("infra-failed", "harness", "The agent program of the Scribe run stopped.", true, false)
	if got := wrRunEnd(t, db, run); got != ended {
		t.Fatalf("the run and its wiki update after the failure report = %swant %s", got, ended)
	}
	if _, err := Heartbeat(db, id, run); !rnStale(err) {
		t.Fatalf("heartbeat after the end = %v, want a stale run refusal", err)
	}
	if _, err := RunProcessStarted(db, id, run); !rnStale(err) {
		t.Fatalf("process start after the end = %v, want a stale run refusal", err)
	}
	if _, err := RunModelResponse(db, id, run); !rnStale(err) {
		t.Fatalf("model response after the end = %v, want a stale run refusal", err)
	}
	if _, err := RunLaunchCheck(db, nil, run); err == nil || err.Error() != "The run already ended." {
		t.Fatalf("launch check after the end = %v, want the refusal of an ended run", err)
	}
	// The session locators need no active run.
	if _, err := RegisterSession(db, id, run, "native-session", "trace.jsonl"); err != nil {
		t.Fatalf("register session after the end: %v", err)
	}
	// A second failure report finds the end of the run. It writes nothing.
	diagnostics := wrDump(t, db, `SELECT * FROM diagnostic_events ORDER BY id`)
	wrReport(t, db, id, run, "ws-test", report)
	if got := wrRunEnd(t, db, run); got != ended {
		t.Fatalf("the second failure report changed the wiki update: %swant %s", got, ended)
	}
	if got := wrDump(t, db, `SELECT * FROM diagnostic_events ORDER BY id`); got != diagnostics || strings.Count(got, "\n") != 1 {
		t.Fatalf("diagnostics after the second failure report:\n%s\nwant the one record of the first report:\n%s", got, diagnostics)
	}
	if got := liveness(); got != active {
		t.Fatalf("a refused command changed the liveness facts: %swant %s", got, active)
	}

	for range 2 {
		if result, err := RunStopped(db, nil, run); err != nil || result.(map[string]any)["stopped"] != true {
			t.Fatalf("stop confirmation = %v, %v", result, err)
		}
	}
	if got := liveness(); got != fmt.Sprintf("last_heartbeat_at=%s|uptime=1|started=1|stopped=1|\n", stamp) {
		t.Fatalf("liveness facts after the stop confirmation = %s", got)
	}
	if got := wrDump(t, db, `SELECT * FROM leases ORDER BY project`); got != leases {
		t.Fatalf("a runner command of a wiki update run changed a lease:\n%s\nbefore:\n%s", got, leases)
	}
}
