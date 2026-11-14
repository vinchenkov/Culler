//go:build docker_e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLandingFailureAndRecoveryDockerBoundary proves the refusal and the
// recovery of an approved update. An untracked operator file at a path the
// candidate adds makes the apply operation refuse. The refusal blocks the task
// with the operator action and moves nothing. The task keeps its approval.
// After the operator removes the file and unblocks the task, the next tick
// applies the same candidate and archives the task.
func TestLandingFailureAndRecoveryDockerBoundary(t *testing.T) {
	f := setup(t)

	res := f.mcOK("", "task", "add", "landing failure task", "--project", project,
		"--description", "The apply refuses an obstruction, then the operator recovers the task.")
	taskID := int64(res["task_id"].(float64))
	mainBefore := f.git("rev-parse", "culler")

	f.startResident()

	// The pipeline runs itself under the timer. The prepare operation tests
	// the merge before the obstruction exists, so the packet is ready.
	f.waitForTaskStatus(taskID, "packaged", 180*time.Second)
	packet := f.waitForReadyPacket(taskID, 90*time.Second)
	candidate, _ := packet["candidate_commit"].(string)
	if candidate == "" {
		t.Fatalf("the ready packet carries no candidate: %v", packet)
	}

	// The obstruction: an untracked file in the operator checkout at the
	// exact path the candidate adds.
	collision := filepath.Join(f.ws, fmt.Sprintf("task-%d.txt", taskID))
	if err := os.WriteFile(collision, []byte("operator bytes the update must not erase\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f.approve(taskID, packet)

	// The apply operation must refuse and block the task. It must not archive
	// the task and must not move culler.
	f.waitFor(90*time.Second, "the refused apply blocks the task", func() (bool, string) {
		task := f.mcOK("", "task", "get", fmt.Sprint(taskID))
		if task["archived"].(float64) == 1 {
			return false, "task archived: the obstructed apply must refuse, not update"
		}
		if task["blocked"].(float64) != 1 {
			return false, fmt.Sprintf("not blocked yet: %v", task["blocked_reason"])
		}
		return true, ""
	})

	task := f.mcOK("", "task", "get", fmt.Sprint(taskID))
	reason, _ := task["blocked_reason"].(string)
	if !strings.Contains(reason, "prevent the merge into culler") || !strings.Contains(reason, filepath.Base(collision)) {
		t.Fatalf("blocked_reason = %q, want the obstruction and the file name", reason)
	}
	if task["status"] != "packaged" || task["decision"] != "approved" {
		t.Fatalf("a refused apply must retain status=packaged decision=approved, got status=%v decision=%v", task["status"], task["decision"])
	}
	if now := f.git("rev-parse", "culler"); now != mainBefore {
		t.Fatalf("culler advanced to %s despite the refused apply (want unmoved %s)", now, mainBefore)
	}
	// The candidate survives the refusal, so the retry has a merged commit to apply.
	if got := f.livePacket(taskID); got == nil || got["candidate_commit"] != candidate {
		t.Fatalf("the packet lost its candidate %s after the refusal: %v", candidate, got)
	}
	f.waitFor(10*time.Second, "lease free after the refused apply", func() (bool, string) {
		if lease := f.lease(project); lease["run_id"] != nil {
			return false, fmt.Sprintf("held by %v", lease["owner"])
		}
		return true, ""
	})

	// A blocked task receives no repository operation. The operator unblock is
	// the only trigger, after the obstruction is gone.
	time.Sleep(2 * time.Second)
	if task := f.mcOK("", "task", "get", fmt.Sprint(taskID)); task["archived"].(float64) == 1 {
		t.Fatal("a blocked apply retried and archived; it must wait for an operator unblock")
	}

	if err := os.Remove(collision); err != nil {
		t.Fatal(err)
	}
	if got := f.mcRun("", "task", "unblock", fmt.Sprint(taskID), "--reason", "The operator removed the obstruction."); got.code != 0 {
		t.Fatalf("mc task unblock exited %d: %s", got.code, got.stderr)
	}
	f.waitForArchived(taskID, 90*time.Second)
	if mainAfter := f.git("rev-parse", "culler"); mainAfter != candidate {
		t.Fatalf("culler = %s after the recovery, want the candidate %s", mainAfter, candidate)
	}
	if got := f.git("show", "culler:"+filepath.Base(collision)); !strings.Contains(got, "work for task") {
		t.Fatalf("culler does not carry the Worker's file after the recovery: %q", got)
	}
}
