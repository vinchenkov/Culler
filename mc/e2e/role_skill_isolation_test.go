//go:build docker_e2e

package e2e

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The Worker checks its installed skills after an Arbiter uses the same home.
func TestRoleSkillIsolationDockerBoundary(t *testing.T) {
	// The explicit proposal makes Arbiter precede the Worker in the same skill directory.
	f := setup(t, withSeededSpine(seedRoleSkillProposal))

	// One shell command prevents completion after a failed probe.
	// The fake harness continues after a failed command.
	writeBehaviorFile(t, f, "worker-skill.json", behaviorSteps(
		execStep(`set -e
root="$HOME/.agents/skills"
if [ -z "$HOME" ]; then echo "NO CONTAINER HOME: the skill root cannot be derived" >&2; exit 1; fi
if [ ! -d "$root" ]; then echo "NO SKILL ROOT: $root is not a directory" >&2; exit 1; fi
if [ ! -f "$root/mc-worker/SKILL.md" ]; then echo "OWN ROLE SKILL MISSING: $root/mc-worker/SKILL.md" >&2; exit 1; fi
if ! grep -q '^name: mc-worker$' "$root/mc-worker/SKILL.md"; then echo "FRONTMATTER NAME IS NOT mc-worker" >&2; exit 1; fi
for skill in writing project-wiki project-environment-setup task-description; do
  if [ ! -f "$root/$skill/SKILL.md" ]; then echo "SHARED SKILL MISSING: $skill" >&2; exit 1; fi
  if ! grep -q "^name: $skill$" "$root/$skill/SKILL.md"; then echo "SHARED SKILL NAME WRONG: $skill" >&2; exit 1; fi
done
foreign=$(ls -A "$root" | grep -Ev '^(mc-worker|writing|project-wiki|project-environment-setup|task-description)$' || true)
if [ -n "$foreign" ]; then echo "FOREIGN ROLE SKILL PRESENT: $foreign" >&2; exit 1; fi
bun /mc/behaviors/worker.ts`),
		succeedStep("worked"),
	))
	setResidentWorkerBehavior(t, f, "/mc/behaviors/worker-skill.json", []string{})

	const taskID int64 = 1

	f.startResident()

	// The Worker completes only after the role and shared skill checks pass.
	f.waitForTaskStatus(taskID, "worked", 180*time.Second)
	for _, run := range f.runs() {
		if run["role"] == "arbiter" && run["ended_at"] != nil && run["outcome"] == "completed" {
			return
		}
	}
	t.Fatal("The Worker ran without the prior Arbiter proposal decision.")
}

// The negative control requires a foreign skill and proves that the probe prevents work.
func TestRoleSkillIsolationProbeDetectsAForeignRoleSkillDockerBoundary(t *testing.T) {
	f := setup(t, withSeededSpine(seedRoleSkillProposal))

	// The probe and completion share one shell command, as in the successful test.
	writeBehaviorFile(t, f, "worker-skill-control.json", behaviorSteps(
		execStep(`set -e
root="$HOME/.agents/skills"
if [ ! -f "$root/mc-verifier/SKILL.md" ]; then echo "CONTROL ARM: no Verifier skill in a Worker container, as required" >&2; exit 1; fi
bun /mc/behaviors/worker.ts`),
		succeedStep("worked"),
	))
	setResidentWorkerBehavior(t, f, "/mc/behaviors/worker-skill-control.json", []string{})

	const taskID int64 = 1

	f.startResident()

	workerRun := f.waitForAgentRun("worker", 180*time.Second)
	f.waitFor(45*time.Second, "the Worker records the negative control failure", func() (bool, string) {
		body, err := os.ReadFile(filepath.Join(f.home, "sessions", workerRun, "native.jsonl"))
		if err != nil {
			return false, err.Error()
		}
		for _, line := range strings.Split(string(body), "\n") {
			var event struct {
				Event    string `json:"event"`
				ExitCode int    `json:"exit_code"`
				Stderr   string `json:"stderr"`
			}
			if json.Unmarshal([]byte(line), &event) != nil || event.Event != "tool-use" {
				continue
			}
			if event.ExitCode == 1 && strings.Contains(event.Stderr, "CONTROL ARM: no Verifier skill in a Worker container") {
				if row := f.runRow(workerRun); row != nil && row["ended_at"] != nil {
					return true, ""
				}
			}
		}
		return false, "The Worker lacks a completed negative control failure."
	})
	if status := f.mcOK("", "task", "get", fmt.Sprint(taskID))["status"]; status != "seeded" {
		t.Fatalf("the failed skill probe advanced task %d to %v", taskID, status)
	}
	if got := f.mcOK("", "task", "get", fmt.Sprint(taskID))["completion_sha"]; got != nil {
		t.Fatalf("task %d recorded a submission %v after the failed skill probe", taskID, got)
	}
}

// The Scribe run of a wiki update carries the skills of the Scribe and the shared skills.
func TestScribeSkillIsolationDockerBoundary(t *testing.T) {
	f := setup(t, withDueWikiUpdate())

	// One shell command prevents the wiki commit after a failed probe.
	writeBehaviorFile(t, f, "scribe-skill.json", behaviorSteps(
		execStep(`set -e
root="$HOME/.agents/skills"
if [ -z "$HOME" ]; then echo "NO CONTAINER HOME: the skill root cannot be derived" >&2; exit 1; fi
if [ ! -d "$root" ]; then echo "NO SKILL ROOT: $root is not a directory" >&2; exit 1; fi
for skill in mc-scribe openwiki openwiki-documents openwiki-recovery; do
  if [ ! -f "$root/$skill/SKILL.md" ]; then echo "OWN ROLE SKILL MISSING: $skill" >&2; exit 1; fi
  if ! grep -q "^name: $skill$" "$root/$skill/SKILL.md"; then echo "OWN ROLE SKILL NAME WRONG: $skill" >&2; exit 1; fi
done
for skill in writing project-wiki project-environment-setup task-description; do
  if [ ! -f "$root/$skill/SKILL.md" ]; then echo "SHARED SKILL MISSING: $skill" >&2; exit 1; fi
  if ! grep -q "^name: $skill$" "$root/$skill/SKILL.md"; then echo "SHARED SKILL NAME WRONG: $skill" >&2; exit 1; fi
done
foreign=$(ls -A "$root" | grep -Ev '^(mc-scribe|openwiki|openwiki-documents|openwiki-recovery|writing|project-wiki|project-environment-setup|task-description)$' || true)
if [ -n "$foreign" ]; then echo "FOREIGN ROLE SKILL PRESENT: $foreign" >&2; exit 1; fi
bun /mc/behaviors/scribe.ts`),
		succeedStep("updated"),
	))
	addResidentRoleBehavior(t, f, "scribe", "/mc/behaviors/scribe-skill.json")

	f.startResident()

	// The wiki update succeeds only after the skill checks pass.
	update := f.waitForCurrentWiki(f.waitForAgentRun("scribe", 60*time.Second), 180*time.Second)
	if update["merged_commit"] != f.git("rev-parse", "culler") {
		t.Fatalf("the wiki update = %v, want its merged commit on culler", update)
	}
}

// The Collie container carries its own skill and the shared skills.
func TestCollieSkillIsolationDockerBoundary(t *testing.T) {
	f := setup(t)
	if err := os.WriteFile(filepath.Join(f.base, "behaviors", "collie.json"), []byte(`{"steps":[
		{"do":"sleep","seconds":10},
		{"do":"succeed","output":"collie ack"}]}`), 0o644); err != nil {
		t.Fatalf("write skill-probe Collie behavior: %v", err)
	}

	start := f.mcOK("", "collie", "start", "--from", "dashboard:chan-1")
	session, ok := start["session_id"].(string)
	if !ok || session == "" {
		t.Fatalf("mc collie start returned no session_id: %v", start)
	}
	f.startResident()
	if send := f.mcOK("", "collie", "send", session, "--from", "dashboard:chan-1", "--body", "ping"); send["message_id"] == nil {
		t.Fatalf("mc collie send returned no message_id: %v", send)
	}

	containerName := "mc-collie-" + session
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", containerName).Run() })

	f.waitFor(60*time.Second, "the Collie container carries its own skill and the shared skills", func() (bool, string) {
		out, err := exec.Command(
			"docker", "exec", containerName,
			"sh", "-c", `set -e
root="$HOME/.agents/skills"
test -n "$HOME" || { echo "NO CONTAINER HOME"; exit 1; }
test -d "$root" || { echo "NO SKILL ROOT: $root"; exit 1; }
test -f "$root/culler/SKILL.md" || { echo "OWN SKILL MISSING"; exit 1; }
grep -q '^name: culler$' "$root/culler/SKILL.md" || { echo "FRONTMATTER NAME WRONG"; exit 1; }
for skill in writing project-wiki project-environment-setup task-description; do
  test -f "$root/$skill/SKILL.md" || { echo "SHARED SKILL MISSING: $skill"; exit 1; }
  grep -q "^name: $skill$" "$root/$skill/SKILL.md" || { echo "SHARED SKILL NAME WRONG: $skill"; exit 1; }
done
foreign=$(ls -A "$root" | grep -Ev '^(culler|writing|project-wiki|project-environment-setup|task-description)$' || true)
test -z "$foreign" || { echo "FOREIGN SKILL PRESENT: $foreign"; exit 1; }
echo OK`,
		).CombinedOutput()
		if err != nil {
			return false, "probe not satisfied yet: " + strings.TrimSpace(string(out))
		}
		if !strings.Contains(string(out), "OK") {
			return false, "probe produced no verdict: " + strings.TrimSpace(string(out))
		}
		return true, ""
	})

	// Collie excludes pipeline role skills.
	if out, err := exec.Command(
		"docker", "exec", containerName, "sh", "-c", `test -e "$HOME/.agents/skills/mc-worker"`,
	).CombinedOutput(); err == nil {
		t.Fatalf("a pipeline role skill is present in a Collie container: %s", out)
	}
}

func seedRoleSkillProposal(f *fixture, db *sql.DB) {
	f.t.Helper()
	if _, err := db.Exec(`INSERT INTO tasks(id,title,description,scope,status,origin,project)
 VALUES(1,'Check the role skills','The Worker must receive only its assigned skills.','task','proposed','autonomous',?)`, project); err != nil {
		f.t.Fatal(err)
	}
}
