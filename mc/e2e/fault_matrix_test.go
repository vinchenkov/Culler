//go:build docker_e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// stopResident gracefully stops the running resident process (the reboot
// drill's "power off"). The startResident cleanup captures its own cmd, so a
// later startResident restarts cleanly.
func (f *fixture) stopResident() {
	f.t.Helper()
	if f.resProc == nil {
		return
	}
	_ = f.resProc.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { f.resProc.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		_ = f.resProc.Process.Kill()
		<-done
	}
	f.resProc = nil
}

// A malformed harness records an infrastructure failure before its session starts.
// The task completes after the fixture restores a valid harness.
func TestFaultInfrastructureRetryDockerBoundary(t *testing.T) {
	f := setup(t)

	// First: a broken Worker harness that dies before session-start.
	brokenWorker := filepath.Join(f.base, "behaviors", "worker.json")
	if err := os.WriteFile(brokenWorker, []byte("{ not valid behavior json"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := f.mcOK("", "task", "add", "fault-retry task", "--project", project,
		"--description", "The Worker reports an infrastructure failure and completes after the harness recovers.")
	taskID := int64(res["task_id"].(float64))

	f.startResident()

	f.waitFor(90*time.Second, "the infrastructure failure consumes one retry", func() (bool, string) {
		task := f.mcOK("", "task", "get", fmt.Sprint(taskID))
		if r := task["dispatch_retries"].(float64); r < 3 {
			return true, ""
		}
		return false, fmt.Sprintf("dispatch_retries still %v", task["dispatch_retries"])
	})

	var failedRun string
	for _, r := range f.agentRuns("worker") {
		if r["outcome"] == "infra-failed" {
			failedRun = r["id"].(string)
		}
	}
	if failedRun == "" {
		t.Fatalf("the Worker did not record an infrastructure failure for task %d: %v", taskID, f.runs())
	}
	sessionDir := filepath.Join(f.home, "sessions", failedRun)
	if _, err := os.Stat(sessionDir); err != nil {
		t.Fatalf("the failed run lost its session folder %s: %v", sessionDir, err)
	}

	// The task never advanced (status unchanged, not blocked yet), so it is
	// still re-selectable.
	if st := f.mcOK("", "task", "get", fmt.Sprint(taskID))["status"]; st != "seeded" {
		t.Fatalf("the failed run changed the task status to %q", st)
	}

	// Now heal the Worker: the retry re-selects the same task and completes.
	writeBehaviorFile(t, f, "worker.json", behaviorSteps(
		execStep("bun /mc/behaviors/worker.ts"),
		succeedStep("worked"),
	))

	// The healed Worker re-selects the same task and drives it to packaged.
	f.waitForTaskStatus(taskID, "packaged", 180*time.Second)
	final := f.mcOK("", "task", "get", fmt.Sprint(taskID))
	if final["completion_sha"] == nil || final["completion_sha"] == f.git("rev-parse", "culler") {
		t.Fatalf("the recovered task submitted no new commit: %v", final["completion_sha"])
	}
	if final["blocked"].(float64) != 0 || final["stalled"].(float64) != 0 {
		t.Fatalf("the recovered task remains blocked or stalled: %v", final)
	}
	if r := final["dispatch_retries"].(float64); r != 3 {
		t.Fatalf("the accepted terminal restored %v retries; want 3", r)
	}
}

// Three infrastructure failures stall the task and preserve their recorded conditions.
func TestFaultBudgetExhaustionDockerBoundary(t *testing.T) {
	f := setup(t)

	// A permanently broken Worker harness.
	if err := os.WriteFile(filepath.Join(f.base, "behaviors", "worker.json"),
		[]byte("{ permanently broken"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := f.mcOK("", "task", "add", "fault-exhaustion task", "--project", project,
		"--description", "The Worker fails before its session starts. Three failures stall the task.")
	taskID := int64(res["task_id"].(float64))

	f.startResident()

	f.waitFor(180*time.Second, "the task stalls after three infrastructure failures", func() (bool, string) {
		task := f.mcOK("", "task", "get", fmt.Sprint(taskID))
		if task["stalled"].(float64) == 1 && f.lease(project)["run_id"] == nil {
			return true, ""
		}
		return false, fmt.Sprintf("dispatch_retries=%v stalled=%v", task["dispatch_retries"], task["stalled"])
	})

	task := f.mcOK("", "task", "get", fmt.Sprint(taskID))
	if r := task["dispatch_retries"].(float64); r != 0 {
		t.Fatalf("the stalled task retains %v retries; want 0", r)
	}
	if task["infrastructure_failure_count"] != float64(3) || task["blocked"] != float64(0) {
		t.Fatalf("the infrastructure failures changed the task's operator block or lost their count: %v", task)
	}
	failure, ok := task["latest_infrastructure_failure"].(map[string]any)
	if !ok || failure["condition"] == "" || failure["source"] != "runner" {
		t.Fatalf("the stalled task lost its runner failure evidence: %v", failure)
	}
}

// TestRebootDrillDockerBoundary is the reboot drill: the resident process is
// killed mid-pipeline and a fresh one started. The resident holds no in-memory
// pipeline state (all state is the spine), so the next tick reaps whatever the
// thresholds now say and re-selects. The task resumes to its packet with no
// lost work and no double-dispatch. The trace-only session folders survive
// the reboot.
func TestRebootDrillDockerBoundary(t *testing.T) {
	f := setup(t)

	res := f.mcOK("", "task", "add", "reboot-drill task", "--project", project,
		"--description", "The resident restarts mid-pipeline and resumes.")
	taskID := int64(res["task_id"].(float64))

	f.startResident()

	// Drive to a quiescent milestone: the Worker has committed and released the
	// lease. Rebooting here has no run in flight to orphan.
	f.waitForTaskStatus(taskID, "worked", 90*time.Second)
	f.waitFor(15*time.Second, "lease free before reboot", func() (bool, string) {
		if lease := f.lease(project); lease["run_id"] != nil {
			return false, fmt.Sprintf("held by %v", lease["owner"])
		}
		return true, ""
	})
	runsBefore := len(f.runs())

	// Power-cycle the resident.
	f.stopResident()
	f.startResident()

	// A fresh resident resumes from spine state alone: Verifier → Packager →
	// prepare → packet.
	f.waitForTaskStatus(taskID, "packaged", 180*time.Second)
	f.waitForReadyPacket(taskID, 90*time.Second)

	// No double-dispatch: exactly one packet, and the reboot did not re-run the
	// already-completed Worker stage.
	if p := f.packets(); len(p) != 1 || int64(p[0]["task_id"].(float64)) != taskID {
		t.Fatalf("packets after reboot = %v, want exactly one for task %d", p, taskID)
	}
	if workers := completedRuns(f, "worker", taskID); workers != 1 {
		t.Fatalf("completed worker runs after reboot = %d, want 1", workers)
	}
	// Session folders (including pre-reboot runs) survive.
	if runsBefore == 0 {
		t.Fatal("no runs recorded before reboot")
	}
	for _, r := range f.runs() {
		if r["binding"] == "mechanical" {
			continue
		}
		sd := filepath.Join(f.home, "sessions", r["id"].(string))
		if _, err := os.Stat(sd); err != nil {
			t.Fatalf("session folder %s vanished across the reboot: %v", sd, err)
		}
	}
}

// TestInterruptDockerBoundary is the interrupt path: the operator "scratch
// that". While a Worker holds the lease (a hang behavior keeps its container
// live), `mc task interrupt` cancels and archives the task, and the next
// dispatch tick reaps the run and stops its container. The container's death
// is the assertion that matters here, not the row.
func TestInterruptDockerBoundary(t *testing.T) {
	f := setup(t)

	// A Worker that establishes a session then hangs, holding the lease.
	writeBehaviorFile(t, f, "worker.json", map[string]any{
		"steps": []map[string]any{{"do": "hang"}},
	})

	res := f.mcOK("", "task", "add", "interrupt task", "--project", project,
		"--description", "The operator scratches a task mid-Worker.")
	taskID := int64(res["task_id"].(float64))

	f.startResident()

	// Wait until the hanging Worker holds the lease for this task.
	var workerRun string
	f.waitFor(90*time.Second, "the Worker holds the lease", func() (bool, string) {
		lease := f.lease(project)
		if lease["owner"] == "worker" && lease["subject"] != nil && int64(lease["subject"].(float64)) == taskID {
			workerRun, _ = lease["run_id"].(string)
			return true, ""
		}
		return false, fmt.Sprintf("lease=%v", lease["owner"])
	})

	// The hang container must be live before the interrupt, or "it is gone
	// afterwards" proves nothing about the interrupt. The lease is claimed
	// inside `mc dispatch` and the container is started afterwards by the
	// resident, so "the Worker holds the lease" is true for a window in which
	// no container exists yet.
	container := "mc-" + project + "-" + workerRun
	f.waitFor(90*time.Second, "the hanging Worker's container to be running", func() (bool, string) {
		if strings.TrimSpace(f.dockerOutput("ps", "-q", "--filter", "name=^"+container+"$")) != "" {
			return true, ""
		}
		return false, container + " not running"
	})

	// Scratch it. The verb writes one fact, the cancellation, and emits no
	// effect of its own; it deliberately leaves the lease held, because the
	// lease is what the reap selects on.
	if got := f.mcRun("", "task", "interrupt", fmt.Sprint(taskID)); got.code != 0 {
		t.Fatalf("mc task interrupt exited %d: %s", got.code, got.stderr)
	}

	task := f.mcOK("", "task", "get", fmt.Sprint(taskID))
	if task["archived"].(float64) != 1 || task["decision"] != "cancelled" {
		t.Fatalf("interrupted task = archived %v decision %v, want archived/cancelled", task["archived"], task["decision"])
	}

	// One tick later the run is reaped, the lease frees, and the container is
	// stopped by the resident. Two ticks of slack over the 60s interval, since
	// the interrupt lands at an arbitrary point inside one.
	f.waitFor(150*time.Second, "the interrupt reap frees the lease", func() (bool, string) {
		if lease := f.lease(project); lease["run_id"] != nil && lease["run_id"] == workerRun {
			return false, "worker still holds the lease"
		}
		return true, ""
	})
	if got := f.runRow(workerRun)["outcome"]; got != "interrupted" {
		t.Fatalf("interrupted run outcome = %v, want interrupted", got)
	}
	// An operator cancellation is neither an infra death nor a quality miss, so
	// the reap charges the subject nothing.
	if got := f.mcOK("", "task", "get", fmt.Sprint(taskID))["dispatch_retries"]; got.(float64) != 3 {
		t.Fatalf("the interrupt reap charged the subject: dispatch_retries = %v", got)
	}
	f.waitFor(60*time.Second, "the interrupted container is stopped", func() (bool, string) {
		if out := f.dockerOutput("ps", "-q", "--filter", "name=^"+container+"$"); strings.TrimSpace(out) != "" {
			return false, container + " still running"
		}
		return true, ""
	})
}
