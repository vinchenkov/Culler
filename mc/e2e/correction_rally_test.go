//go:build docker_e2e

package e2e

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestCorrectionRallyDockerBoundary proves the correction budget. The
// Verifier requests a correction four times. The first correction opens the
// budget, and each later correction concludes one attempt. The fourth request
// finds three finished attempts and blocks the task. The operator grants
// another allowance, the Worker reworks once more, the Verifier passes, and
// the task reaches its packet.
//
// The Worker reads the correction file from its brief on every correction
// round. The probe and the hand-in share one `set -e` command, because the
// fake harness continues after a failed command.
func TestCorrectionRallyDockerBoundary(t *testing.T) {
	f := setup(t)

	writeBehaviorFile(t, f, "worker.json", behaviorSteps(
		execStep(`set -e
if grep -q '## Correction from the Verifier' /mc/run.json; then
  # The brief rides run.json as an escaped JSON string, so an escaped line
  # break ends the fact line.
  corr=$(sed -n 's/.*- Correction file: \([^\\]*\)\\n.*/\1/p' /mc/run.json | head -1)
  : "${corr:?the brief carried no Correction file fact on a correction round}"
  test -f "$corr" || { echo "correction $corr does not exist in the Worker's container" >&2; exit 1; }
  grep -q "please revise" "$corr" || { echo "correction $corr does not carry the Verifier's prose" >&2; exit 1; }
fi
bun /mc/behaviors/worker.ts`),
		succeedStep("worked"),
	))

	// The Verifier counts its earlier correction files on the durable plane.
	// The verdict terminal writes a snapshot beside each correction file, so
	// the count selects the round files only. The Verifier requests a
	// correction four times, then passes the fifth submission.
	writeBehaviorFile(t, f, "verifier.json", behaviorSteps(
		execStep(`set -e
sha=$(git -C /workspace/source rev-parse HEAD)
`+e2eVerifierEvidence+`cd=`+e2eArtifactPlane+`/$MC_SUBJECT_ID/corrections
mkdir -p "$cd"
n=$(ls "$cd" | grep -c '^round-' || true)
printf 'rally verdict after %s corrections\n' "$n" > "$ev"
if [ "$n" -lt 4 ]; then
  corr=$cd/round-$n.txt
  printf 'please revise (round %s)\n' "$n" > "$corr"
  mc verifier verdict "$MC_SUBJECT_ID" --run "$MC_RUN_ID" --outcome correct --problem "The result misses a criterion." --explanation-evidence "The check failed." --resolution "Correct the result." --evidence "$ev" --correction "$corr"
else
  mc verifier verdict "$MC_SUBJECT_ID" --run "$MC_RUN_ID" --outcome pass --evidence "$ev" --sha "$sha"
fi`),
		succeedStep("verified"),
	))

	res := f.mcOK("", "task", "add", "correction rally task", "--project", project,
		"--description", "The Verifier requests four corrections, then the operator grants another allowance.")
	taskID := int64(res["task_id"].(float64))

	f.startResident()

	// The fourth correction request blocks the task with three finished attempts.
	f.waitFor(300*time.Second, "the fourth correction request blocks the task", func() (bool, string) {
		task := f.mcOK("", "task", "get", fmt.Sprint(taskID))
		if task["blocked"].(float64) != 1 {
			return false, fmt.Sprintf("status=%v correction_count=%v", task["status"], task["correction_count"])
		}
		return true, ""
	})
	task := f.mcOK("", "task", "get", fmt.Sprint(taskID))
	if cc := task["correction_count"].(float64); cc != 3 {
		t.Fatalf("correction_count = %v at the block, want 3", cc)
	}
	if reason, _ := task["blocked_reason"].(string); !strings.Contains(reason, "The Verifier rejected three corrections.") {
		t.Fatalf("blocked_reason = %q, want the exhausted correction budget", reason)
	}
	if verifiers := completedRuns(f, "verifier", taskID); verifiers != 4 {
		t.Fatalf("completed verifier runs at the block = %d, want 4", verifiers)
	}

	// The operator grants another allowance. The block clears and the
	// correction reopens.
	f.mcOK("", "delivery", "allow-corrections", "--task", fmt.Sprint(taskID),
		"--reason", "The operator authorizes three more attempts.")
	task = f.mcOK("", "task", "get", fmt.Sprint(taskID))
	if task["blocked"].(float64) != 0 || task["status"] != "seeded" || task["correction_count"].(float64) != 0 {
		t.Fatalf("after the allowance: %v, want unblocked, seeded, count 0", task)
	}

	// The fifth submission passes and the task reaches its packet.
	f.waitForTaskStatus(taskID, "packaged", 240*time.Second)
	f.waitForReadyPacket(taskID, 90*time.Second)
	if verifiers := completedRuns(f, "verifier", taskID); verifiers != 5 {
		t.Fatalf("completed verifier runs = %d, want 5 (four corrections and one pass)", verifiers)
	}
	if workers := completedRuns(f, "worker", taskID); workers != 5 {
		t.Fatalf("completed worker runs = %d, want 5 (the initial run and four reworks)", workers)
	}
	// Every Worker submission was a new commit above the previous submission.
	span := "main..refs/mc/tasks/" + fmt.Sprint(taskID) + "/versions/" + lastRun(f, "worker", taskID)
	if commits, err := strconv.Atoi(f.git("rev-list", "--count", span)); err != nil || commits != 5 {
		t.Fatalf("the last submission has %d commits above main (%v), want 5", commits, err)
	}
}

// completedRuns counts the completed harness runs of one role on the task.
func completedRuns(f *fixture, role string, taskID int64) int {
	n := 0
	for _, r := range f.agentRuns(role) {
		if r["outcome"] == "completed" && r["subject"] != nil && int64(r["subject"].(float64)) == taskID {
			n++
		}
	}
	return n
}

// lastRun returns the id of the newest completed harness run of one role on the task.
func lastRun(f *fixture, role string, taskID int64) string {
	id := ""
	for _, r := range f.agentRuns(role) {
		if r["outcome"] == "completed" && r["subject"] != nil && int64(r["subject"].(float64)) == taskID {
			id = r["id"].(string)
		}
	}
	return id
}
