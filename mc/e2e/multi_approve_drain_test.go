//go:build docker_e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMultiApproveDrainDockerBoundary proves the drain of two approved tasks
// and the wiki update that covers both. The first wiki update creates the
// Project wiki. Both tasks then start from one commit of culler, and each Worker
// commit holds one project document. Both candidates were prepared against the
// same culler. The first apply moves culler, so the second apply finds a changed
// destination. The task waits for the quiet interval, observes the
// destination, and prepares a new candidate against the moved destination.
// That candidate merges with no merge repair and no new decision. After the
// drain, one wiki update covers both tasks: culler gets one wiki commit, and
// each project document stays unchanged outside the wiki.
func TestMultiApproveDrainDockerBoundary(t *testing.T) {
	f := setup(t, withDueWikiUpdate())

	// A short quiet interval keeps the drain within the lane budget.
	f.mcOK("", "delivery", "limits", "--project", project, "--quiet-seconds", "2")

	// Each Worker commit holds one project document below docs/.
	writeBehaviorFile(t, f, "worker.json", behaviorSteps(
		execStep(`set -e
reports=/workspace/source/docs/reports
mkdir -p "$reports"
printf '%s\n' '---' "title: Report of task $MC_SUBJECT_ID" 'type: Research' 'document: true' '---' '' "# Report of task $MC_SUBJECT_ID" '' 'The task adds one file.' > "$reports/task-$MC_SUBJECT_ID.md"
bun /mc/behaviors/worker.ts`),
		succeedStep("worked"),
	))

	f.startResident()

	// The Project has no wiki update, so the first tick starts one. It creates
	// the wiki before a task exists.
	created := f.waitForCurrentWiki(f.waitForAgentRun("scribe", 60*time.Second), 180*time.Second)
	mainBefore := f.git("rev-parse", "culler")
	if created["merged_commit"] != mainBefore {
		t.Fatalf("the first wiki update = %v, want its merged commit on culler %s", created, mainBefore)
	}

	const nTasks = 2
	ids := make([]int64, 0, nTasks)
	for i := 0; i < nTasks; i++ {
		res := f.mcOK("", "task", "add", fmt.Sprintf("drain task %d", i+1), "--project", project,
			"--description", "The task is one of several tasks approved together.")
		ids = append(ids, int64(res["task_id"].(float64)))
	}

	// The lease serializes the pipeline, so the tasks reach their packets one
	// at a time; wait for all of them before approving any. Each Worker made
	// one commit above the same commit of culler.
	firstCandidate := map[int64]string{}
	for _, id := range ids {
		packet := f.waitForReadyPacket(id, 420*time.Second)
		firstCandidate[id] = packet["candidate_commit"].(string)
		if packet["destination_commit"] != mainBefore {
			t.Fatalf("task %d prepared against %v, want culler %s", id, packet["destination_commit"], mainBefore)
		}
		submitted, _ := packet["submitted_commit"].(string)
		if start := f.git("rev-parse", submitted+"^"); start != mainBefore {
			t.Fatalf("the Worker of task %d started at %s, want culler %s", id, start, mainBefore)
		}
	}

	// Approve both, spaced so decided_at is strictly increasing.
	for _, id := range ids {
		f.approve(id, f.readyPacket(id))
		time.Sleep(1100 * time.Millisecond)
	}

	// The first task applies. The second task finds a changed destination and
	// merges its new candidate with the same approval.
	f.waitFor(600*time.Second, "both approved tasks merge and archive", func() (bool, string) {
		done := 0
		var note []string
		for _, id := range ids {
			task := f.mcOK("", "task", "get", fmt.Sprint(id))
			if task["archived"].(float64) == 1 {
				done++
				continue
			}
			if task["decision"] != "approved" || task["blocked"].(float64) != 0 {
				t.Fatalf("task %d lost its approval before its merge: status=%v decision=%v blocked=%v (%v)", id, task["status"], task["decision"], task["blocked"], task["blocked_reason"])
			}
			note = append(note, fmt.Sprintf("task %d status=%v", id, task["status"]))
		}
		return done == len(ids), fmt.Sprint(note)
	})

	// The first task applied its first candidate. The second task applied a
	// candidate prepared against the moved destination, above its own
	// submitted version.
	first, second := ids[0], ids[1]
	mainAfter := f.git("rev-parse", "culler")
	if !f.isAncestor(firstCandidate[first], mainAfter) {
		t.Fatalf("culler does not contain the first candidate %s", firstCandidate[first])
	}
	if mainAfter == firstCandidate[second] || !f.isAncestor(firstCandidate[second], mainAfter) {
		t.Fatalf("culler %s must descend from the obsolete candidate %s of task %d", mainAfter, firstCandidate[second], second)
	}
	finalPacket := f.livePacketAny(second)
	if finalPacket == nil || finalPacket["candidate_commit"] != mainAfter {
		t.Fatalf("culler is not the final candidate of task %d: %v", second, finalPacket)
	}
	if finalPacket["destination_commit"] != firstCandidate[first] {
		t.Fatalf("task %d prepared its final candidate against %v, want the moved destination %s", second, finalPacket["destination_commit"], firstCandidate[first])
	}
	// Each task merged with its one approval: no correction, no merge repair,
	// and one run of each role.
	for _, id := range ids {
		task := f.mcOK("", "task", "get", fmt.Sprint(id))
		conflict := task["delivery"].(map[string]any)["latest_conflict"]
		if task["decision"] != "approved" || task["correction_count"].(float64) != 0 || conflict != nil {
			t.Fatalf("task %d = decision %v, correction_count %v, latest conflict %v; want approved, 0, none", id, task["decision"], task["correction_count"], conflict)
		}
		for _, role := range []string{"worker", "verifier", "packager"} {
			if n := completedRuns(f, role, id); n != 1 {
				t.Fatalf("task %d has %d completed %s runs, want 1", id, n, role)
			}
		}
		if _, err := f.gitErr("cat-file", "-e", fmt.Sprintf("culler:task-%d.txt", id)); err != nil {
			t.Fatalf("task %d's file is not on culler after the drain: %v", id, err)
		}
	}
	f.waitFor(10*time.Second, "lease free after the drain", func() (bool, string) {
		if lease := f.lease(project); lease["run_id"] != nil {
			return false, fmt.Sprintf("held by %v", lease["owner"])
		}
		return true, ""
	})

	// Both merges left the Project wiki behind, and the interval held the next
	// wiki update.
	f.waitFor(15*time.Second, "the wiki state behind after the drain", func() (bool, string) {
		state := f.wikiState()
		if state["state"] != "behind" || state["accepted_commit"] != mainAfter || state["next_start_at"] == nil {
			return false, fmt.Sprintf("state=%v accepted_commit=%v next_start_at=%v", state["state"], state["accepted_commit"], state["next_start_at"])
		}
		return true, ""
	})
	if n := len(f.agentRuns("scribe")); n != 1 {
		t.Fatalf("Scribe runs before the interval ends = %d, want the one run of the first wiki update", n)
	}

	// A short interval makes the wiki update due. The save also applies each
	// timing value of the configuration file; no later step reads one of them.
	f.mcOK(`{"changes":[{"key":"timing.wiki_update_interval_s","expected":3600,"value":1}]}`, "config", "save")
	var covering string
	f.waitFor(60*time.Second, "the wiki update that covers both tasks", func() (bool, string) {
		scribes := f.agentRuns("scribe")
		if len(scribes) < 2 {
			return false, fmt.Sprintf("%d Scribe runs", len(scribes))
		}
		covering = scribes[1]["id"].(string)
		return true, ""
	})
	update := f.waitForCurrentWiki(covering, 180*time.Second)
	wikiMain := f.git("rev-parse", "culler")
	if update["start_commit"] != mainAfter || update["merged_commit"] != wikiMain {
		t.Fatalf("the wiki update = %v, want the start commit %s and its merged commit on culler %s", update, mainAfter, wikiMain)
	}

	// One wiki commit covers both tasks, and it changes the wiki only.
	if n := f.git("rev-list", "--count", mainAfter+".."+wikiMain); n != "1" {
		t.Fatalf("the wiki update gave culler %s commits, want one wiki commit", n)
	}
	for _, name := range strings.Fields(f.git("diff", "--name-only", mainAfter, wikiMain)) {
		if !strings.HasPrefix(name, "openwiki/") {
			t.Fatalf("the wiki commit changes %s, a path outside openwiki/", name)
		}
	}
	// Each project document stays unchanged on culler.
	for _, id := range ids {
		title := fmt.Sprintf("Report of task %d", id)
		want := "---\ntitle: " + title + "\ntype: Research\ndocument: true\n---\n\n# " + title + "\n\nThe task adds one file."
		if got := f.git("show", fmt.Sprintf("culler:docs/reports/task-%d.md", id)); got != want {
			t.Fatalf("the project document of task %d changed:\n%s", id, got)
		}
	}

	// The Project wiki is current, so the short interval starts no later wiki
	// update. Each Scribe run has no task.
	time.Sleep(5 * time.Second)
	scribes := f.agentRuns("scribe")
	if len(scribes) != 2 {
		t.Fatalf("Scribe runs = %d, want the first wiki update and the one wiki update for both tasks", len(scribes))
	}
	for _, scribe := range scribes {
		if scribe["subject"] != nil {
			t.Fatalf("a Scribe run has a task as its subject: %v", scribe)
		}
	}
	if now := f.git("rev-parse", "culler"); now != wikiMain {
		t.Fatalf("culler moved to %s after the wiki update, want %s", now, wikiMain)
	}
}

// livePacketAny returns the packet row of the task with its current version,
// archived or not.
func (f *fixture) livePacketAny(taskID int64) map[string]any {
	f.t.Helper()
	for _, p := range f.packets() {
		if int64(p["task_id"].(float64)) == taskID && p["superseded"].(float64) == 0 {
			return p
		}
	}
	return nil
}

// --- behavior-file helpers (build via json.Marshal to avoid escaping) ---

type behaviorStep struct {
	Do      string `json:"do"`
	Command string `json:"command,omitempty"`
	Output  string `json:"output,omitempty"`
}

func execStep(command string) behaviorStep    { return behaviorStep{Do: "exec", Command: command} }
func succeedStep(output string) behaviorStep  { return behaviorStep{Do: "succeed", Output: output} }
func behaviorSteps(steps ...behaviorStep) any { return map[string]any{"steps": steps} }

func writeBehaviorFile(t *testing.T, f *fixture, name string, body any) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.base, "behaviors", name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
