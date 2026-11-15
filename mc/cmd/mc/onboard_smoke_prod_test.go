//go:build !test_fake_routing

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc/deployment"
	"mc/verbs"
)

type smokeCommandFixture struct {
	taskID    int64
	projectID string
	phase     string
	approved  bool
	landed    bool
	calls     []string
	// roleRows holds the run history with no wiki update of the smoke Project.
	roleRows []any
	// wikiReads holds the row of the smoke Project in each wiki state read, in
	// order. The last entry repeats. A nil entry is a read with no such row.
	wikiReads []any
	wikiCalls int
	// earlierWikiRuns holds the run history rows of the wiki updates of the
	// smoke Project that the wiki state read does not report.
	earlierWikiRuns []any
	projectStatus   string
}

type smokeEffectFixture struct {
	complete bool
	cleanup  bool
	verified bool
	// wikiVerified states that the wiki proof ran. wikiProofAfter is the count
	// of commands before it.
	wikiVerified   bool
	wikiProofAfter int
	phases         []string
}

const (
	smokeOtherProject = "another-project"
	smokeTimeLimit    = "The Scribe run reached its time limit."
)

// smokeWikiRow returns one row of the wiki state read. active and last are
// the values of those keys, or nil. nextStartText is the condition that holds
// the next start, or the empty string.
func smokeWikiRow(project string, active, last map[string]any, nextStartText string) map[string]any {
	row := map[string]any{
		"project": project, "state": "behind",
		"state_text":      "The Project wiki is behind the accepted commit.",
		"accepted_commit": strings.Repeat("a", 40),
		"next_start_at":   nil, "next_start_text": nil, "active": nil, "last": nil,
	}
	if nextStartText != "" {
		row["next_start_text"] = nextStartText
	}
	if last != nil {
		row["last"] = last
		if last["result"] == "succeeded" {
			row["state"], row["state_text"] = "current", "The Project wiki is current."
		} else if nextStartText == "" {
			row["next_start_at"] = "2026-07-25 01:00:00"
		}
	}
	if active != nil {
		row["state"], row["state_text"], row["active"] = "update-active", "A wiki update is active.", active
	}
	return row
}

// smokeWikiActive returns the active value of a wiki state row. repair is the
// repair text, or nil. The Scribe run has an end in each step after the step
// run.
func smokeWikiActive(step, stepText string, repair any) map[string]any {
	active := map[string]any{
		"run_id": "wiki-run-1", "started_at": "2026-07-25 00:00:00",
		"agent_started_at": "2026-07-25 00:00:05", "last_heartbeat_at": "2026-07-25 00:01:00",
		"run_ended_at": nil, "step": step, "step_text": stepText, "repair_text": repair,
	}
	if step != "run" {
		active["run_ended_at"] = "2026-07-25 00:04:00"
	}
	return active
}

// smokeWikiLast returns the last value of a wiki state row. cause is the cause
// text and merged is the merged commit; each one can be nil. A wiki update
// that succeeded with no change holds its start commit as the wiki commit.
func smokeWikiLast(run, result string, cause, merged any) map[string]any {
	start := strings.Repeat("a", 40)
	text, wiki := "The wiki update failed.", any(nil)
	switch {
	case result == "succeeded" && merged != nil:
		text, wiki = "Culler merged the wiki commit.", strings.Repeat("b", 40)
	case result == "succeeded":
		text, wiki = "The Project wiki needed no change.", start
	}
	return map[string]any{
		"run_id": run, "started_at": "2026-07-25 00:00:00", "ended_at": "2026-07-25 00:05:00",
		"result": result, "result_text": text, "cause_text": cause,
		"start_commit": start, "wiki_commit": wiki, "merged_commit": merged,
	}
}

// smokeOtherWikiLast returns the last wiki update of the other Project. It
// failed.
func smokeOtherWikiLast() map[string]any {
	return smokeWikiLast("wiki-run-other", "failed", smokeTimeLimit, nil)
}

// smokeRunRow returns one row of the run history. subject is a task
// identifier, or nil.
func smokeRunRow(id, project, role, binding string, subject any) map[string]any {
	return map[string]any{
		"id": id, "tier": "pipeline", "project": project, "subject": subject,
		"role": role, "binding": binding, "wiki_update": nil,
		"ended_at": "2026-07-25 00:04:00", "outcome": "completed",
	}
}

// smokeWikiRunRow returns the run history row of a wiki update run: a Scribe
// run with no task. update is the last value or the active value of the wiki
// state row of that wiki update.
func smokeWikiRunRow(project string, update map[string]any) map[string]any {
	row := smokeRunRow(update["run_id"].(string), project, "scribe", "fake/fake", nil)
	wiki := map[string]any{
		"result": update["result"], "result_text": update["result_text"], "cause_text": update["cause_text"],
	}
	switch {
	case update["result"] == "failed":
		row["outcome"] = "reaped"
	case update["step"] == "run":
		row["ended_at"], row["outcome"] = nil, nil
	case update["step"] != nil:
		wiki["result_text"] = update["step_text"]
	}
	row["wiki_update"] = wiki
	return row
}

// wikiRow returns the row of the smoke Project in the wiki state read with
// the index, or nil.
func (f *smokeCommandFixture) wikiRow(index int) map[string]any {
	row, _ := f.wikiReads[min(index, len(f.wikiReads)-1)].(map[string]any)
	return row
}

// runRows returns the run history. After the role rows and the earlier wiki
// update runs, it holds the wiki update runs of the smoke Project that the
// last wiki state read reports. Before a wiki state read, the first one counts.
func (f *smokeCommandFixture) runRows() []any {
	rows := append(append([]any{}, f.roleRows...), f.earlierWikiRuns...)
	row := f.wikiRow(max(f.wikiCalls-1, 0))
	for _, key := range []string{"last", "active"} {
		if update, _ := row[key].(map[string]any); update != nil {
			rows = append(rows, smokeWikiRunRow(f.projectID, update))
		}
	}
	return rows
}

func (f *smokeCommandFixture) run(args []string) productionSmokeCommandResult {
	call := strings.Join(args, " ")
	f.calls = append(f.calls, call)
	ok := func(body map[string]any) productionSmokeCommandResult {
		return productionSmokeCommandResult{Body: body}
	}
	switch call {
	case "task get " + strconv.FormatInt(f.taskID, 10):
		if f.approved {
			archived := float64(0)
			if f.landed {
				archived = 1
			}
			return ok(map[string]any{
				"id": float64(f.taskID), "status": "packaged",
				"decision": "approved", "archived": archived,
				"blocked": float64(0),
			})
		}
		return ok(map[string]any{
			"id": float64(f.taskID), "title": "Culler full-pipeline smoke",
			"status": "packaged", "decision": nil, "archived": float64(0),
			"blocked": float64(0),
		})
	case "packet list":
		return ok(map[string]any{"packets": []any{map[string]any{
			"task_id": float64(f.taskID), "archived": float64(0), "packet_id": float64(11), "version_id": float64(12),
		}}})
	case "packet decide " + strconv.FormatInt(f.taskID, 10) + " --approve --packet 11 --version 12":
		f.approved = true
		f.landed = true
		return ok(map[string]any{"ok": true})
	case "run list":
		return ok(map[string]any{"runs": f.runRows()})
	case "wiki-state list":
		read := f.wikiRow(f.wikiCalls)
		f.wikiCalls++
		rows := []any{smokeWikiRow(smokeOtherProject, nil, smokeOtherWikiLast(), "")}
		if read != nil {
			rows = append(rows, read)
		}
		return ok(map[string]any{"wiki_states": rows})
	case "project list":
		return ok(map[string]any{"projects": []any{
			map[string]any{"id": smokeOtherProject, "status": "archived"},
			map[string]any{"id": f.projectID, "status": f.projectStatus},
		}})
	case "project archive " + f.projectID:
		return ok(map[string]any{"ok": true})
	default:
		return productionSmokeCommandResult{Code: 2, Stderr: "unexpected fixture call: " + call}
	}
}

// completedSmokeRoleRows returns the run history of a smoke task that Culler
// merged, with no wiki update of the smoke Project: the four agent runs, the
// wiki update of the other Project, and the runs of the repository operations.
func completedSmokeRoleRows(project string) []any {
	task := float64(37)
	return []any{
		smokeRunRow("run-arbiter", project, "arbiter", "fake/fake", nil),
		smokeRunRow("run-worker", project, "worker", "fake/fake", task),
		smokeRunRow("run-verifier", project, "verifier", "fake/fake", task),
		smokeRunRow("run-packager", project, "packager", "fake/fake", task),
		smokeWikiRunRow(smokeOtherProject, smokeOtherWikiLast()),
		smokeRunRow("run-capture", project, "worker", "mechanical", task),
		smokeRunRow("run-wiki-merge", project, "worker", "mechanical", nil),
	}
}

func withSmokeFakes(t *testing.T) (*smokeCommandFixture, *smokeEffectFixture) {
	t.Helper()
	originalCommand := productionSmokeCommand
	originalPrepare := productionSmokePrepare
	originalRegister := productionSmokeRegister
	originalSleep := productionSmokeSleep
	originalBase := productionSmokeWorkspaceBase
	originalCheckpoint := productionSmokeCheckpoint
	originalCleanup := productionSmokeCleanup
	originalVerify := productionSmokeVerifyMerge
	originalVerifyWiki := productionSmokeVerifyWiki
	t.Cleanup(func() {
		productionSmokeCommand = originalCommand
		productionSmokePrepare = originalPrepare
		productionSmokeRegister = originalRegister
		productionSmokeSleep = originalSleep
		productionSmokeWorkspaceBase = originalBase
		productionSmokeCheckpoint = originalCheckpoint
		productionSmokeCleanup = originalCleanup
		productionSmokeVerifyMerge = originalVerify
		productionSmokeVerifyWiki = originalVerifyWiki
	})

	home := filepath.Join(t.TempDir(), "mc-home")
	t.Setenv("MC_HOME", home)
	var err error
	home, err = configuredCanonicalHome()
	if err != nil {
		t.Fatalf("canonical test home: %v", err)
	}
	fixture := &smokeCommandFixture{
		taskID: 37, projectID: "smoke-0011223344556677", phase: "running", projectStatus: "active",
	}
	fixture.wikiReads = []any{smokeWikiRow(fixture.projectID, nil,
		smokeWikiLast("wiki-run-1", "succeeded", nil, strings.Repeat("c", 40)), "")}
	base, err := deployment.SmokeWorkspaceBase(home)
	if err != nil {
		t.Fatal(err)
	}
	fixture.roleRows = completedSmokeRoleRows(fixture.projectID)
	productionSmokePrepare = func(gotHome string) (productionSmokeState, error) {
		if gotHome != home {
			t.Fatalf("prepare home = %q, want %q", gotHome, home)
		}
		return productionSmokeState{
			Version: smokeStateVersion, Phase: fixture.phase, ProjectID: fixture.projectID,
			WorkspaceRoot: filepath.Join(base, fixture.projectID, "workspace"),
			TaskID:        fixture.taskID,
		}, nil
	}
	// The registration of a smoke Project starts the helper container. A test
	// that uses the real prepare function supplies its own registration.
	productionSmokeRegister = func(productionSmokeState) error {
		t.Fatal("the fixture registers no smoke Project")
		return nil
	}
	productionSmokeCommand = fixture.run
	productionSmokeSleep = func(time.Duration) {
		t.Fatal("ready fixture unexpectedly slept")
	}
	effects := &smokeEffectFixture{}
	productionSmokeCheckpoint = func(gotHome string, state productionSmokeState) error {
		if gotHome != home {
			t.Fatalf("checkpoint home = %q, want %q", gotHome, home)
		}
		effects.phases = append(effects.phases, state.Phase)
		effects.complete = state.Phase == "complete"
		return nil
	}
	productionSmokeCleanup = func(gotHome string, _ productionSmokeState) error {
		if gotHome != home {
			t.Fatalf("cleanup home = %q, want %q", gotHome, home)
		}
		effects.cleanup = true
		return nil
	}
	productionSmokeVerifyMerge = func(_ productionSmokeState, _ map[string]any) error {
		effects.verified = true
		return nil
	}
	productionSmokeVerifyWiki = func(productionSmokeState) error {
		effects.wikiVerified = true
		effects.wikiProofAfter = len(fixture.calls)
		return nil
	}
	return fixture, effects
}

func runSmoke(t *testing.T, a verbs.OnboardArgs, input string) (int, map[string]any, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := brokerOnboardSmoke(a, strings.NewReader(input), &stdout, &stderr)
	var body map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode smoke receipt %q: %v", stdout.String(), err)
	}
	return code, body, stderr.String()
}

func TestSmokeStateAndRepositoryAreOwnerOnlyAndResumable(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	canonical, err := deployment.CanonicalHome(home)
	if err != nil {
		t.Fatal(err)
	}
	state, err := newSmokeState(canonical)
	if err != nil {
		t.Fatalf("new smoke state: %v", err)
	}
	info, err := os.Lstat(smokeStatePath(canonical))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %v err=%v", info, err)
	}
	if err := prepareSmokeRepository(state); err != nil {
		t.Fatalf("prepare repository: %v", err)
	}
	if err := prepareSmokeRepository(state); err != nil {
		t.Fatalf("resume prepared repository: %v", err)
	}
	if err := smokeGit(state.WorkspaceRoot, "rev-parse", "--verify", "HEAD"); err != nil {
		t.Fatalf("repository has no committed HEAD: %v", err)
	}
	replayed, err := readSmokeState(canonical)
	if err != nil || replayed == nil || *replayed != state {
		t.Fatalf("replayed state=%+v err=%v, want %+v", replayed, err, state)
	}
	if strings.HasPrefix(state.WorkspaceRoot, canonical+string(filepath.Separator)) {
		t.Fatalf("smoke workspace remained under protected MC_HOME: %q", state.WorkspaceRoot)
	}
}

func TestVerifySmokeLandingPinsExactRevisionAndArtifact(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	canonical, err := deployment.CanonicalHome(home)
	if err != nil {
		t.Fatal(err)
	}
	state, err := newSmokeState(canonical)
	if err != nil {
		t.Fatal(err)
	}
	state.TaskID = 37
	if err := prepareSmokeRepository(state); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(state.WorkspaceRoot, "SMOKE_RESULT.md")
	if err := os.WriteFile(artifact, []byte("culler smoke ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := smokeGit(state.WorkspaceRoot, "add", "SMOKE_RESULT.md"); err != nil {
		t.Fatal(err)
	}
	if err := smokeGit(state.WorkspaceRoot, "commit", "-m", "Complete smoke task"); err != nil {
		t.Fatal(err)
	}
	rawSHA, err := smokeGitOutput(state.WorkspaceRoot, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	delivery := map[string]any{
		"submitted_commit": strings.TrimSpace(string(rawSHA)), "destination_branch": "culler",
	}
	task := map[string]any{"delivery": delivery}
	if err := verifySmokeMerge(state, task); err != nil {
		t.Fatalf("verify exact landing: %v", err)
	}

	// The task commit holds no wiki file. The merge proof passes, and the wiki
	// proof fails until a wiki commit is on culler.
	err = verifySmokeWiki(state)
	if err == nil || err.Error() != "The smoke check found no file openwiki/index.md on the culler branch of the smoke repository. "+
		"Ask your coding harness to read the trace of the last Scribe run of the smoke Project." {
		t.Fatalf("wiki proof of a culler branch with no wiki = %v", err)
	}
	// A failed read is no proof that the culler branch holds no wiki file.
	absent := state
	absent.WorkspaceRoot = filepath.Join(t.TempDir(), "absent")
	err = verifySmokeWiki(absent)
	if err == nil || err.Error() != "The smoke check cannot read the culler branch of the smoke repository: "+
		"git ls-tree culler -- openwiki/index.md: exit status 128\n"+
		"Ask your coding harness to resume the smoke check." {
		t.Fatalf("wiki proof of a repository that Git cannot read = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(state.WorkspaceRoot, "openwiki"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state.WorkspaceRoot, "openwiki/index.md"), []byte("# Smoke project\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := smokeGit(state.WorkspaceRoot, "add", "openwiki/index.md"); err != nil {
		t.Fatal(err)
	}
	if err := smokeGit(state.WorkspaceRoot, "commit", "-m", "Update the Project wiki"); err != nil {
		t.Fatal(err)
	}
	if err := verifySmokeWiki(state); err != nil {
		t.Fatalf("wiki proof of a culler branch with the wiki: %v", err)
	}
	if err := verifySmokeMerge(state, task); err != nil {
		t.Fatalf("merge proof after the wiki commit: %v", err)
	}

	if err := os.WriteFile(artifact, []byte("wrong\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := smokeGit(state.WorkspaceRoot, "add", "SMOKE_RESULT.md"); err != nil {
		t.Fatal(err)
	}
	if err := smokeGit(state.WorkspaceRoot, "commit", "-m", "Corrupt smoke artifact"); err != nil {
		t.Fatal(err)
	}
	rawSHA, err = smokeGitOutput(state.WorkspaceRoot, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	delivery["submitted_commit"] = strings.TrimSpace(string(rawSHA))
	if err := verifySmokeMerge(state, task); err == nil ||
		!strings.Contains(err.Error(), "exact requested bytes") {
		t.Fatalf("wrong artifact verification error = %v", err)
	}
}

func TestProductionSmokeRequiresExplicitExactPacketApproval(t *testing.T) {
	fixture, effects := withSmokeFakes(t)
	code, body, stderr := runSmoke(t, verbs.OnboardArgs{Smoke: true}, "")
	if code != 1 || body["stopped_at"] != "smoke-approval" ||
		!strings.Contains(stderr, "no decision was written") {
		t.Fatalf("code=%d body=%v stderr=%q", code, body, stderr)
	}
	if strings.Contains(strings.Join(fixture.calls, "|"), "packet decide") || effects.complete {
		t.Fatalf("approval refusal mutated decision/archive: calls=%v effects=%+v", fixture.calls, effects)
	}
	prompt := strings.Join([]string{
		"Smoke task 37 is ready for review:",
		"Culler full-pipeline smoke",
		"",
		"Your approval starts these steps:",
		"1. Culler merges the task into culler.",
		"2. The smoke check waits for the end",
		"   of the first wiki update.",
		"3. The smoke check archives the smoke",
		"   Project.",
		"",
		"Steps 1 and 2 each have a limit of ten",
		"minutes. The terminal shows no text",
		"before the result.",
		"",
		"Type exactly: approve 37",
		"",
	}, "\n")
	if !strings.HasPrefix(stderr, prompt+"mc: ") {
		t.Fatalf("approval prompt = %q, want %q before the stop text", stderr, prompt)
	}
	for _, line := range strings.Split(prompt, "\n") {
		if len(line) > 40 {
			t.Fatalf("prompt line %q has %d characters; a terminal of 40 columns wraps it", line, len(line))
		}
	}

	fixture.calls = nil
	code, body, _ = runSmoke(t, verbs.OnboardArgs{
		Smoke: true, SmokeApproveTask: fixture.taskID + 1,
	}, "")
	if code != 1 || body["error"] == nil {
		t.Fatalf("wrong exact task code=%d body=%v", code, body)
	}
	if strings.Contains(strings.Join(fixture.calls, "|"), "packet decide") || effects.complete {
		t.Fatalf("wrong exact task mutated decision/archive: calls=%v effects=%+v", fixture.calls, effects)
	}
}

func TestProductionSmokeApprovesLandsProvesRolesAndArchives(t *testing.T) {
	fixture, effects := withSmokeFakes(t)
	code, body, stderr := runSmoke(t, verbs.OnboardArgs{
		Smoke: true, SmokeApproveTask: fixture.taskID,
	}, "")
	if code != 0 || body["ok"] != true || stderr != "" ||
		!effects.verified || !effects.cleanup || !effects.complete ||
		strings.Join(effects.phases, "|") != "archived|complete" {
		t.Fatalf("code=%d body=%v stderr=%q effects=%+v", code, body, stderr, effects)
	}
	want := strings.Join([]string{
		"task get 37", "task get 37", "packet list", "packet list", "packet decide 37 --approve --packet 11 --version 12",
		"task get 37", "run list", "wiki-state list",
		"project archive smoke-0011223344556677",
	}, "|")
	if got := strings.Join(fixture.calls, "|"); got != want {
		t.Fatalf("calls=%q, want %q", got, want)
	}
	// The wiki proof runs after the wiki state read and before the archive.
	if !effects.wikiVerified || effects.wikiProofAfter != len(fixture.calls)-1 {
		t.Fatalf("wiki proof after %d of %d commands: effects=%+v", effects.wikiProofAfter, len(fixture.calls), effects)
	}
	sections, _ := body["sections"].([]any)
	if len(sections) != 1 {
		t.Fatalf("receipt sections = %v", body["sections"])
	}
	section, _ := sections[0].(map[string]any)
	if section["section"] != "smoke" || section["status"] != "done" ||
		section["detail"] != "Culler merged the smoke task. Culler merged the wiki commit of a wiki update." {
		t.Fatalf("receipt section = %v", section)
	}
}

func TestProductionSmokeStopsWhenReviewedPacketDisappears(t *testing.T) {
	fixture, effects := withSmokeFakes(t)
	packetReads := 0
	productionSmokeCommand = func(args []string) productionSmokeCommandResult {
		result := fixture.run(args)
		if strings.Join(args, " ") == "packet list" {
			packetReads++
			if packetReads == 2 {
				result.Body = map[string]any{"packets": []any{}}
			}
		}
		return result
	}
	code, body, stderr := runSmoke(t, verbs.OnboardArgs{
		Smoke: true, SmokeApproveTask: fixture.taskID,
	}, "")
	rejection, ok := body["error"].(map[string]any)
	if code != 1 || !ok || rejection["code"] != "domain-rejection" || packetReads != 2 {
		t.Fatalf("the absent packet did not stop the smoke check: code=%d body=%v reads=%d", code, body, packetReads)
	}
	if !strings.Contains(stderr, "The smoke review packet is unavailable.") ||
		!strings.Contains(stderr, "Ask your coding harness to resume the smoke check.") || strings.Contains(stderr, "<nil>") {
		t.Fatalf("the packet diagnostic lacks its cause or recovery action: %q", stderr)
	}
	if fixture.approved || effects.verified || effects.cleanup || effects.complete || len(effects.phases) != 0 ||
		strings.Contains(strings.Join(fixture.calls, "|"), "packet decide") {
		t.Fatalf("the absent packet permitted approval or completion: calls=%v effects=%+v", fixture.calls, effects)
	}
}

// smokeTaskReads counts the reads of the smoke task.
func smokeTaskReads(fixture *smokeCommandFixture) int {
	reads := 0
	for _, call := range fixture.calls {
		if call == "task get 37" {
			reads++
		}
	}
	return reads
}

// The wait for the review packet ends at the poll limit. The smoke check then
// approves no packet and records no phase.
func TestProductionSmokePacketWaitEndsAtThePollLimit(t *testing.T) {
	fixture, effects := withSmokeFakes(t)
	productionSmokeCommand = func(args []string) productionSmokeCommandResult {
		if strings.Join(args, " ") == "task get 37" {
			fixture.calls = append(fixture.calls, "task get 37")
			return productionSmokeCommandResult{Body: map[string]any{
				"id": float64(37), "title": "Culler full-pipeline smoke", "status": "worked",
				"decision": nil, "archived": float64(0), "blocked": float64(0),
			}}
		}
		return fixture.run(args)
	}
	sleeps := 0
	productionSmokeSleep = func(d time.Duration) {
		if d != productionSmokePollInterval {
			t.Fatalf("sleep = %v, want the poll interval %v", d, productionSmokePollInterval)
		}
		sleeps++
	}
	code, _, stderr := runSmoke(t, verbs.OnboardArgs{Smoke: true}, "")
	if code != 1 || stderr != "mc: production smoke: full-pipeline smoke exceeded its ten-minute bound\n" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	// One read finds a task with no approval, and each other read belongs to the wait.
	if sleeps != productionSmokePollAttempts || smokeTaskReads(fixture) != productionSmokePollAttempts+2 {
		t.Fatalf("sleeps = %d, task reads = %d, want %d reads", sleeps, smokeTaskReads(fixture), productionSmokePollAttempts+2)
	}
	if fixture.approved || effects.verified || effects.wikiVerified || effects.complete || len(effects.phases) != 0 ||
		strings.Contains(strings.Join(fixture.calls, "|"), "packet decide") {
		t.Fatalf("the ended wait permitted approval or completion: effects=%+v", effects)
	}
}

// The wait for the merge of the approved smoke task ends at the poll limit.
// The smoke check then proves no merge and records no phase.
func TestProductionSmokeMergeWaitEndsAtThePollLimit(t *testing.T) {
	fixture, effects := withSmokeFakes(t)
	fixture.approved = true
	sleeps := 0
	productionSmokeSleep = func(d time.Duration) {
		if d != productionSmokePollInterval {
			t.Fatalf("sleep = %v, want the poll interval %v", d, productionSmokePollInterval)
		}
		sleeps++
	}
	code, _, stderr := runSmoke(t, verbs.OnboardArgs{Smoke: true}, "")
	if code != 1 || stderr != "mc: production smoke merge: full-pipeline smoke exceeded its ten-minute bound\n" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	// One read finds the approval, and each other read belongs to the wait.
	if sleeps != productionSmokePollAttempts || smokeTaskReads(fixture) != productionSmokePollAttempts+2 {
		t.Fatalf("sleeps = %d, task reads = %d, want %d reads", sleeps, smokeTaskReads(fixture), productionSmokePollAttempts+2)
	}
	if effects.verified || effects.wikiVerified || effects.complete || len(effects.phases) != 0 {
		t.Fatalf("the ended wait permitted a merge proof or completion: effects=%+v", effects)
	}
}

func TestProductionSmokeResumesAfterDurableApprovalWithoutSecondDecision(t *testing.T) {
	fixture, effects := withSmokeFakes(t)
	fixture.approved = true
	fixture.landed = true
	code, body, stderr := runSmoke(t, verbs.OnboardArgs{Smoke: true}, "")
	if code != 0 || body["ok"] != true || stderr != "" || !effects.complete {
		t.Fatalf("code=%d body=%v stderr=%q effects=%+v", code, body, stderr, effects)
	}
	got := strings.Join(fixture.calls, "|")
	if strings.Contains(got, "packet list") || strings.Contains(got, "packet decide") {
		t.Fatalf("approved resume re-opened packet decision: %v", fixture.calls)
	}
}

func TestProductionSmokeResumesArchivedCheckpointAndReplaysCompletion(t *testing.T) {
	fixture, effects := withSmokeFakes(t)
	fixture.phase = "archived"
	code, body, stderr := runSmoke(t, verbs.OnboardArgs{Smoke: true}, "")
	if code != 0 || body["ok"] != true || stderr != "" ||
		!effects.cleanup || !effects.complete ||
		strings.Join(effects.phases, "|") != "complete" || len(fixture.calls) != 0 {
		t.Fatalf("archived resume code=%d body=%v stderr=%q effects=%+v calls=%v",
			code, body, stderr, effects, fixture.calls)
	}

	fixture.calls = nil
	effects.complete = false
	effects.cleanup = false
	effects.phases = nil
	fixture.phase = "complete"
	code, body, stderr = runSmoke(t, verbs.OnboardArgs{Smoke: true}, "")
	if code != 0 || body["ok"] != true || stderr != "" ||
		effects.cleanup || len(effects.phases) != 0 || len(fixture.calls) != 0 {
		t.Fatalf("complete replay code=%d body=%v stderr=%q effects=%+v calls=%v",
			code, body, stderr, effects, fixture.calls)
	}
}

// A state file with another version counts as no smoke state. The state file
// holds a complete smoke check. With the current version, the smoke check
// replays the receipt. With the version 1, the smoke check registers a new
// smoke Project and stops at the approval of the new task.
func TestSmokeStateOfAnotherVersionStartsANewSmokeCheck(t *testing.T) {
	fixture, _ := withSmokeFakes(t)
	home, err := configuredCanonicalHome()
	if err != nil {
		t.Fatal(err)
	}
	base, err := deployment.SmokeWorkspaceBase(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "smoke"), 0o700); err != nil {
		t.Fatal(err)
	}
	const earlier = "smoke-8899aabbccddeeff"
	write := func(version int) {
		t.Helper()
		body, err := json.MarshalIndent(productionSmokeState{
			Version: version, Phase: "complete", ProjectID: earlier,
			WorkspaceRoot: filepath.Join(base, earlier, "workspace"), TaskID: 36,
		}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(smokeStatePath(home), append(body, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	productionSmokePrepare = prepareProductionSmoke
	var registered []productionSmokeState
	productionSmokeRegister = func(state productionSmokeState) error {
		registered = append(registered, state)
		fixture.projectID = state.ProjectID
		return nil
	}
	productionSmokeCommand = func(args []string) productionSmokeCommandResult {
		if strings.Join(args, " ") == "task list --all --project "+fixture.projectID {
			return productionSmokeCommandResult{Body: map[string]any{
				"tasks": []any{map[string]any{"id": float64(fixture.taskID)}},
			}}
		}
		return fixture.run(args)
	}

	write(smokeStateVersion)
	code, receipt, _ := runSmoke(t, verbs.OnboardArgs{Smoke: true}, "")
	if code != 0 || receipt["ok"] != true || receipt["project_id"] != earlier ||
		len(registered) != 0 || len(fixture.calls) != 0 {
		t.Fatalf("current version: code=%d receipt=%v registered=%+v calls=%v, want the receipt of the complete state",
			code, receipt, registered, fixture.calls)
	}

	write(1)
	code, receipt, _ = runSmoke(t, verbs.OnboardArgs{Smoke: true}, "")
	if len(registered) != 1 || registered[0].ProjectID == earlier || registered[0].Phase != "running" {
		t.Fatalf("version 1: registered smoke Projects = %+v (receipt %v), want one new smoke Project", registered, receipt)
	}
	if code != 1 || receipt["stopped_at"] != "smoke-approval" || receipt["project_id"] != registered[0].ProjectID {
		t.Fatalf("version 1: code=%d receipt=%v, want the stop at the approval of the task of %s",
			code, receipt, registered[0].ProjectID)
	}
	want := registered[0]
	want.TaskID = fixture.taskID
	if stored, err := readSmokeState(home); err != nil || stored == nil || *stored != want {
		t.Fatalf("stored smoke state = %+v err=%v, want %+v", stored, err, want)
	}
}

func TestProductionSmokeRequiresEveryRole(t *testing.T) {
	// The run history rows: 0 Arbiter, 1 Worker, 2 Verifier, 3 Packager.
	for _, tc := range []struct {
		name   string
		role   string
		change func(row map[string]any)
	}{
		{"a Packager run of another task", "packager", func(row map[string]any) { row["subject"] = float64(38) }},
		{"a Verifier run with no task", "verifier", func(row map[string]any) { row["subject"] = nil }},
		{"a Worker run that failed", "worker", func(row map[string]any) { row["outcome"] = "failed" }},
		{"a Worker run with no end", "worker", func(row map[string]any) { row["ended_at"] = nil }},
		// The run of a repository operation of the smoke task stays in the
		// history. It holds the role worker and is no Worker run.
		{"a repository operation in place of the Worker run", "worker", func(row map[string]any) { row["binding"] = "mechanical" }},
		{"an Arbiter run of another Project", "arbiter", func(row map[string]any) { row["project"] = smokeOtherProject }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, effects := withSmokeFakes(t)
			index := map[string]int{"arbiter": 0, "worker": 1, "verifier": 2, "packager": 3}[tc.role]
			tc.change(fixture.roleRows[index].(map[string]any))
			code, body, stderr := runSmoke(t, verbs.OnboardArgs{
				Smoke: true, SmokeApproveTask: fixture.taskID,
			}, "")
			want := "mc: production smoke evidence: smoke run history has no completed " + tc.role +
				" for " + fixture.projectID + "\n"
			if code != 1 || body["error"] == nil || stderr != want {
				t.Fatalf("code=%d body=%v stderr=%q, want %q", code, body, stderr, want)
			}
			calls := strings.Join(fixture.calls, "|")
			if strings.Contains(calls, "wiki-state list") || strings.Contains(calls, "project archive") ||
				effects.wikiVerified || effects.complete || len(effects.phases) != 0 {
				t.Fatalf("the smoke check continued with no %s run: calls=%v effects=%+v", tc.role, fixture.calls, effects)
			}
		})
	}
}

// The task of a smoke check reaches review and merges with no Scribe run. The
// only Scribe run of the smoke Project is the run of a wiki update, and that
// run holds no task.
func TestSmokeRoleProofRefusesAScribeRunWithTheTask(t *testing.T) {
	t.Run("the Scribe run of a wiki update holds no task", func(t *testing.T) {
		fixture, effects := withSmokeFakes(t)
		scribeRuns := 0
		for _, raw := range fixture.runRows() {
			row := raw.(map[string]any)
			if row["role"] != "scribe" || row["project"] != fixture.projectID {
				continue
			}
			scribeRuns++
			if row["subject"] != nil {
				t.Fatalf("the run of a wiki update holds the task %v", row["subject"])
			}
		}
		if scribeRuns != 1 {
			t.Fatalf("the run history holds %d Scribe runs of the smoke Project, want 1", scribeRuns)
		}
		code, body, stderr := runSmoke(t, verbs.OnboardArgs{
			Smoke: true, SmokeApproveTask: fixture.taskID,
		}, "")
		if code != 0 || body["ok"] != true || stderr != "" || !effects.complete {
			t.Fatalf("code=%d body=%v stderr=%q effects=%+v", code, body, stderr, effects)
		}
	})

	for _, tc := range []struct {
		name   string
		change func(row map[string]any)
	}{
		{"a completed Scribe run", func(map[string]any) {}},
		{"a Scribe run with no end", func(row map[string]any) { row["ended_at"], row["outcome"] = nil, nil }},
		{"a Scribe run that failed", func(row map[string]any) { row["outcome"] = "failed" }},
	} {
		t.Run(tc.name+" with the smoke task", func(t *testing.T) {
			fixture, effects := withSmokeFakes(t)
			row := smokeRunRow("run-scribe", fixture.projectID, "scribe", "fake/fake", float64(fixture.taskID))
			tc.change(row)
			fixture.roleRows = append(fixture.roleRows, row)
			code, body, stderr := runSmoke(t, verbs.OnboardArgs{
				Smoke: true, SmokeApproveTask: fixture.taskID,
			}, "")
			want := "mc: production smoke evidence: smoke run history has a Scribe run for smoke task 37. " +
				"A wiki update has no task.\n"
			if code != 1 || body["error"] == nil || stderr != want {
				t.Fatalf("code=%d body=%v stderr=%q, want %q", code, body, stderr, want)
			}
			calls := strings.Join(fixture.calls, "|")
			if strings.Contains(calls, "wiki-state list") || strings.Contains(calls, "project archive") ||
				effects.wikiVerified || effects.complete || len(effects.phases) != 0 {
				t.Fatalf("the smoke check continued: calls=%v effects=%+v", fixture.calls, effects)
			}
		})
	}
}

// The smoke check archives its Project only after the end of a wiki update of
// the Project and the proof of the Project wiki on culler.
func TestSmokeRequiresTheFirstWikiUpdate(t *testing.T) {
	approve := func(t *testing.T, fixture *smokeCommandFixture) (int, map[string]any, string) {
		t.Helper()
		return runSmoke(t, verbs.OnboardArgs{Smoke: true, SmokeApproveTask: fixture.taskID}, "")
	}
	count := func(fixture *smokeCommandFixture, call string) int {
		n := 0
		for _, got := range fixture.calls {
			if got == call {
				n++
			}
		}
		return n
	}
	completed := func(t *testing.T, fixture *smokeCommandFixture, effects *smokeEffectFixture,
		code int, body map[string]any, stderr string) {
		t.Helper()
		if code != 0 || body["ok"] != true || stderr != "" || !effects.complete || !effects.wikiVerified {
			t.Fatalf("code=%d body=%v stderr=%q effects=%+v calls=%v", code, body, stderr, effects, fixture.calls)
		}
		// The wiki proof runs directly before the archive.
		archive := "project archive " + fixture.projectID
		if effects.wikiProofAfter != len(fixture.calls)-1 || fixture.calls[len(fixture.calls)-1] != archive {
			t.Fatalf("wiki proof after %d commands, calls=%v", effects.wikiProofAfter, fixture.calls)
		}
	}
	refused := func(t *testing.T, fixture *smokeCommandFixture, effects *smokeEffectFixture,
		code int, body map[string]any, stderr, want string) {
		t.Helper()
		rejection, _ := body["error"].(map[string]any)
		if code != 1 || rejection["code"] != "domain-rejection" || rejection["message"] != want ||
			stderr != "mc: "+want+"\n" {
			t.Fatalf("code=%d body=%v stderr=%q, want the refusal %q", code, body, stderr, want)
		}
		if count(fixture, "project archive "+fixture.projectID) != 0 ||
			effects.complete || effects.cleanup || len(effects.phases) != 0 {
			t.Fatalf("the smoke check archived its Project: calls=%v effects=%+v", fixture.calls, effects)
		}
	}
	const (
		runText     = "The Scribe updates the Project wiki."
		mergeText   = "Culler merges the wiki commit."
		receiptText = "Culler checks the result of an interrupted merge of the wiki commit. " +
			"Each other merge step of the Project waits for this result."
		repairText  = "The last check ended with no result. The repository operation failed."
		unknownText = "Culler cannot read the culler branch of the Project repository."
		noEndText   = "The smoke check waited ten minutes. No wiki update of the smoke Project ended."
		resumeText  = "Ask your coding harness to resume the smoke check."
	)
	merged := strings.Repeat("c", 40)

	t.Run("the wait ends with the merged wiki commit", func(t *testing.T) {
		fixture, effects := withSmokeFakes(t)
		project := fixture.projectID
		fixture.wikiReads = []any{
			smokeWikiRow(project, nil, nil, "The next wiki update is due."),
			smokeWikiRow(project, smokeWikiActive("run", runText, nil), nil, ""),
			smokeWikiRow(project, smokeWikiActive("merge", mergeText, nil), nil, ""),
			smokeWikiRow(project, nil, smokeWikiLast("wiki-run-1", "succeeded", nil, merged), ""),
		}
		var sleeps []time.Duration
		productionSmokeSleep = func(d time.Duration) { sleeps = append(sleeps, d) }
		code, body, stderr := approve(t, fixture)
		completed(t, fixture, effects, code, body, stderr)
		if count(fixture, "wiki-state list") != 4 || len(sleeps) != 3 {
			t.Fatalf("wiki state reads = %d, sleeps = %v", count(fixture, "wiki-state list"), sleeps)
		}
		for _, sleep := range sleeps {
			if sleep != productionSmokePollInterval {
				t.Fatalf("sleeps = %v, want the poll interval %v", sleeps, productionSmokePollInterval)
			}
		}
	})

	t.Run("a wiki update that failed", func(t *testing.T) {
		fixture, effects := withSmokeFakes(t)
		fixture.wikiReads = []any{smokeWikiRow(fixture.projectID, nil,
			smokeWikiLast("wiki-run-1", "failed", smokeTimeLimit, nil), "")}
		code, body, stderr := approve(t, fixture)
		refused(t, fixture, effects, code, body, stderr,
			"The last wiki update of the smoke Project failed. "+smokeTimeLimit+
				"\nAsk your coding harness to resume the smoke check after the next wiki update merges its wiki commit.")
		if effects.wikiVerified {
			t.Fatal("the wiki proof ran after a failed wiki update")
		}
	})

	t.Run("the first wiki update merged no wiki commit", func(t *testing.T) {
		fixture, effects := withSmokeFakes(t)
		fixture.wikiReads = []any{smokeWikiRow(fixture.projectID, nil,
			smokeWikiLast("wiki-run-1", "succeeded", nil, nil), "")}
		code, body, stderr := approve(t, fixture)
		refused(t, fixture, effects, code, body, stderr,
			"The first wiki update of the smoke Project merged no wiki commit. The Scribe run changed no wiki file. "+
				"Ask your coding harness to read the trace of that Scribe run.")
		if effects.wikiVerified {
			t.Fatal("the wiki proof ran for a first wiki update with no wiki commit")
		}
	})

	t.Run("a later wiki update that needed no change", func(t *testing.T) {
		fixture, effects := withSmokeFakes(t)
		fixture.earlierWikiRuns = []any{smokeWikiRunRow(fixture.projectID,
			smokeWikiLast("wiki-run-1", "succeeded", nil, merged))}
		fixture.wikiReads = []any{smokeWikiRow(fixture.projectID, nil,
			smokeWikiLast("wiki-run-2", "succeeded", nil, nil), "")}
		code, body, stderr := approve(t, fixture)
		completed(t, fixture, effects, code, body, stderr)
	})

	if time.Duration(productionSmokePollAttempts)*productionSmokePollInterval != 10*time.Minute {
		t.Fatalf("the poll bound is %v; the text of the wait states ten minutes",
			time.Duration(productionSmokePollAttempts)*productionSmokePollInterval)
	}
	for _, tc := range []struct {
		name string
		row  func(project string) map[string]any
		text string
	}{
		{"an active Scribe run", func(project string) map[string]any {
			return smokeWikiRow(project, smokeWikiActive("run", runText, nil), nil, "")
		}, runText},
		{"an interrupted merge with a repair text", func(project string) map[string]any {
			return smokeWikiRow(project, smokeWikiActive("receipt", receiptText, repairText), nil, "")
		}, receiptText + " " + repairText},
		{"a wiki update with no start", func(project string) map[string]any {
			return smokeWikiRow(project, nil, nil, unknownText)
		}, unknownText},
	} {
		t.Run("no end inside the poll bound: "+tc.name, func(t *testing.T) {
			fixture, effects := withSmokeFakes(t)
			fixture.wikiReads = []any{tc.row(fixture.projectID)}
			sleeps := 0
			productionSmokeSleep = func(time.Duration) { sleeps++ }
			code, body, stderr := approve(t, fixture)
			refused(t, fixture, effects, code, body, stderr, noEndText+" "+tc.text+"\n"+resumeText)
			if count(fixture, "wiki-state list") != productionSmokePollAttempts+1 || sleeps != productionSmokePollAttempts {
				t.Fatalf("wiki state reads = %d, sleeps = %d, want %d reads",
					count(fixture, "wiki-state list"), sleeps, productionSmokePollAttempts+1)
			}
			if effects.wikiVerified {
				t.Fatal("the wiki proof ran for a wiki update with no end")
			}
		})
	}

	t.Run("a culler branch with no wiki", func(t *testing.T) {
		fixture, effects := withSmokeFakes(t)
		const noWiki = "The smoke check found no file openwiki/index.md on the culler branch of the smoke repository. " +
			"Ask your coding harness to read the trace of the last Scribe run of the smoke Project."
		productionSmokeVerifyWiki = func(productionSmokeState) error { return errors.New(noWiki) }
		code, body, stderr := approve(t, fixture)
		refused(t, fixture, effects, code, body, stderr, noWiki)
		if count(fixture, "wiki-state list") != 1 {
			t.Fatalf("the wiki proof ran before the wiki state read: calls=%v", fixture.calls)
		}
	})

	// A host failure after the archive and before its checkpoint leaves an
	// archived Project. The wiki state read holds no row of it.
	t.Run("a resume after the archive", func(t *testing.T) {
		fixture, effects := withSmokeFakes(t)
		fixture.approved, fixture.landed = true, true
		fixture.earlierWikiRuns = []any{smokeWikiRunRow(fixture.projectID,
			smokeWikiLast("wiki-run-1", "succeeded", nil, merged))}
		fixture.wikiReads, fixture.projectStatus = []any{nil}, "archived"
		code, body, stderr := runSmoke(t, verbs.OnboardArgs{Smoke: true}, "")
		completed(t, fixture, effects, code, body, stderr)
		if count(fixture, "wiki-state list") != 1 || count(fixture, "project list") != 1 {
			t.Fatalf("calls = %v, want one wiki state read and one Project read", fixture.calls)
		}
	})

	t.Run("no wiki state of the active Project", func(t *testing.T) {
		fixture, effects := withSmokeFakes(t)
		fixture.wikiReads = []any{nil}
		code, body, stderr := approve(t, fixture)
		refused(t, fixture, effects, code, body, stderr,
			"Culler holds no wiki state of the smoke Project. Ask your coding harness to find the cause.")
		if effects.wikiVerified {
			t.Fatal("the wiki proof ran with no wiki state")
		}
	})

	t.Run("a wiki state read that failed", func(t *testing.T) {
		fixture, effects := withSmokeFakes(t)
		productionSmokeCommand = func(args []string) productionSmokeCommandResult {
			if strings.Join(args, " ") == "wiki-state list" {
				fixture.calls = append(fixture.calls, "wiki-state list")
				return productionSmokeCommandResult{Code: 1, Stderr: "mc: the database is busy\n"}
			}
			return fixture.run(args)
		}
		code, body, stderr := approve(t, fixture)
		refused(t, fixture, effects, code, body, stderr,
			"production smoke: wiki-state list exited 1: mc: the database is busy")
		if effects.wikiVerified {
			t.Fatal("the wiki proof ran after a failed wiki state read")
		}
	})
}

func TestProductionSmokeCheckpointFailureIsNotReportedComplete(t *testing.T) {
	fixture, _ := withSmokeFakes(t)
	productionSmokeCheckpoint = func(string, productionSmokeState) error {
		return errors.New("disk unavailable")
	}
	code, body, stderr := runSmoke(t, verbs.OnboardArgs{
		Smoke: true, SmokeApproveTask: fixture.taskID,
	}, "")
	if code != 1 || body["error"] == nil || !strings.Contains(stderr, "disk unavailable") {
		t.Fatalf("code=%d body=%v stderr=%q", code, body, stderr)
	}
}

func TestWholeOnboardSmokeRoutesDirectlyToProductionRideThrough(t *testing.T) {
	fixture, effects := withSmokeFakes(t)
	var stdout, stderr bytes.Buffer
	code := brokerWholeOnboard(
		[]string{"__onboard", "--smoke", "--smoke-approve-task", "37"},
		strings.NewReader(""), &stdout, &stderr)
	if code != 0 || stderr.String() != "" || !effects.complete {
		t.Fatalf("code=%d stdout=%q stderr=%q effects=%+v",
			code, stdout.String(), stderr.String(), effects)
	}
	if !strings.Contains(strings.Join(fixture.calls, "|"), "packet decide 37 --approve") {
		t.Fatalf("smoke did not reach packet decision: %v", fixture.calls)
	}
}

func TestWholeOnboardSmokeRejectsUnrelatedWizardInputs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := brokerWholeOnboard(
		[]string{"__onboard", "--smoke", "--project", "primary"},
		strings.NewReader(""), &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "may be combined only") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestSmokeApprovalFlagRequiresPositiveExactTask(t *testing.T) {
	for _, args := range [][]string{
		{"--smoke-approve-task", "37"},
		{"--smoke", "--smoke-approve-task", "0"},
	} {
		if _, err := parseOnboardArgs(args); err == nil {
			t.Fatalf("args %v admitted", args)
		}
	}
}

// smokeResetFixture answers the exact spine reads a retirement makes. Unlike
// the ride-through fixture it starts from a REGISTERED-BUT-STUCK generation:
// a task that never reached a packet, which is what a Project refused at
// dispatch actually leaves behind.
type smokeResetFixture struct {
	taskID       int64
	projectID    string
	decision     any
	archivedTask float64
	packets      []any
	projects     []any
	calls        []string
}

func (f *smokeResetFixture) run(args []string) productionSmokeCommandResult {
	call := strings.Join(args, " ")
	f.calls = append(f.calls, call)
	ok := func(body map[string]any) productionSmokeCommandResult {
		return productionSmokeCommandResult{Body: body}
	}
	id := strconv.FormatInt(f.taskID, 10)
	switch call {
	case "task get " + id:
		return ok(map[string]any{
			"id": float64(f.taskID), "status": "seeded",
			"decision": f.decision, "archived": f.archivedTask,
		})
	case "packet list":
		return ok(map[string]any{"packets": f.packets})
	case "packet decide " + id + " --cancel --packet 11 --version 12 --reason " + smokeResetReason:
		f.decision = "cancelled"
		f.archivedTask = 1
		return ok(map[string]any{"decision": "cancelled"})
	case "project list":
		return ok(map[string]any{"projects": f.projects})
	case "project archive " + f.projectID:
		f.projects = []any{map[string]any{"id": f.projectID, "status": "archived"}}
		return ok(map[string]any{"status": "archived"})
	default:
		return productionSmokeCommandResult{Code: 2, Stderr: "unexpected reset call: " + call}
	}
}

// withSmokeResetState lays down a REAL on-disk state file, because discarding
// that file is the outcome under test; a stubbed prepare would let a broken
// discard pass.
func withSmokeResetState(t *testing.T) (string, *smokeResetFixture) {
	t.Helper()
	original := productionSmokeCommand
	t.Cleanup(func() { productionSmokeCommand = original })

	home := filepath.Join(t.TempDir(), "mc-home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_HOME", home)
	canonical, err := configuredCanonicalHome()
	if err != nil {
		t.Fatalf("canonical test home: %v", err)
	}
	state, err := newSmokeState(canonical)
	if err != nil {
		t.Fatalf("new smoke state: %v", err)
	}
	state.TaskID = 41
	if err := writeSmokeState(canonical, state); err != nil {
		t.Fatalf("checkpoint stuck state: %v", err)
	}
	fixture := &smokeResetFixture{
		taskID: state.TaskID, projectID: state.ProjectID,
		packets:  []any{},
		projects: []any{map[string]any{"id": state.ProjectID, "status": "active"}},
	}
	productionSmokeCommand = fixture.run
	return canonical, fixture
}

func TestSmokeResetArchivesTheProjectAndDiscardsItsState(t *testing.T) {
	home, fixture := withSmokeResetState(t)
	body, err := abandonProductionSmoke(home)
	if err != nil {
		t.Fatalf("abandon: %v", err)
	}
	if body["abandoned"] != true || body["project_id"] != fixture.projectID {
		t.Fatalf("receipt = %v", body)
	}
	joined := strings.Join(fixture.calls, "|")
	if !strings.Contains(joined, "project archive "+fixture.projectID) {
		t.Fatalf("stale Project was never archived, so dispatch still selects it: %v", fixture.calls)
	}
	// No packet exists, so no decision may be written: `packet decide --cancel`
	// requires a live packet and would refuse outright.
	if strings.Contains(joined, "packet decide") {
		t.Fatalf("packetless task was sent a decision: %v", fixture.calls)
	}
	replayed, err := readSmokeState(home)
	if err != nil || replayed != nil {
		t.Fatalf("state survived retirement: %+v err=%v", replayed, err)
	}
}

func TestSmokeResetCancelsALivePacketSoTheGlobalWIPSlotReturns(t *testing.T) {
	home, fixture := withSmokeResetState(t)
	fixture.packets = []any{map[string]any{
		"task_id": float64(fixture.taskID), "archived": float64(0), "packet_id": float64(11), "version_id": float64(12),
	}}
	if _, err := abandonProductionSmoke(home); err != nil {
		t.Fatalf("abandon: %v", err)
	}
	if !strings.Contains(strings.Join(fixture.calls, "|"),
		"packet decide "+strconv.FormatInt(fixture.taskID, 10)+" --cancel") {
		t.Fatalf("live packet kept its slot across the whole deployment: %v", fixture.calls)
	}
}

func TestSmokeResetRefusesAnApprovedGenerationThatHasNotLanded(t *testing.T) {
	home, fixture := withSmokeResetState(t)
	fixture.decision = "approved"
	fixture.archivedTask = 0
	body, err := abandonProductionSmoke(home)
	if err == nil {
		t.Fatalf("approved-but-unlanded generation was retired: %v", body)
	}
	if !strings.Contains(err.Error(), "approved") {
		t.Fatalf("refusal does not name the approved decision: %v", err)
	}
	if replayed, stateErr := readSmokeState(home); stateErr != nil || replayed == nil {
		t.Fatalf("refusal still discarded the state: %+v err=%v", replayed, stateErr)
	}
}

func TestSmokeResetToleratesAProjectTheSpineNoLongerHas(t *testing.T) {
	home, fixture := withSmokeResetState(t)
	fixture.projects = []any{}
	fixture.decision = nil
	if _, err := abandonProductionSmoke(home); err != nil {
		t.Fatalf("abandon over an absent Project: %v", err)
	}
	if strings.Contains(strings.Join(fixture.calls, "|"), "project archive") {
		t.Fatalf("archived a Project the spine does not have: %v", fixture.calls)
	}
	if replayed, err := readSmokeState(home); err != nil || replayed != nil {
		t.Fatalf("state survived: %+v err=%v", replayed, err)
	}
}

func TestSmokeResetWithoutARegisteredGenerationIsANoOp(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_HOME", home)
	canonical, err := configuredCanonicalHome()
	if err != nil {
		t.Fatal(err)
	}
	original := productionSmokeCommand
	t.Cleanup(func() { productionSmokeCommand = original })
	productionSmokeCommand = func(args []string) productionSmokeCommandResult {
		t.Fatalf("no-op reset touched the spine: %v", args)
		return productionSmokeCommandResult{}
	}
	body, err := abandonProductionSmoke(canonical)
	if err != nil || body["abandoned"] != false {
		t.Fatalf("body=%v err=%v", body, err)
	}
}

func TestSmokeResetFlagRequiresSmokeAndRefusesTheApprovalFlag(t *testing.T) {
	for _, args := range [][]string{
		{"--smoke-reset"},
		{"--smoke", "--smoke-reset", "--smoke-approve-task", "37"},
	} {
		if _, err := parseOnboardArgs(args); err == nil {
			t.Fatalf("args %v admitted", args)
		}
	}
	a, err := parseOnboardArgs([]string{"--smoke", "--smoke-reset"})
	if err != nil || !a.SmokeReset || !a.Smoke {
		t.Fatalf("args=%+v err=%v", a, err)
	}
}

func TestWholeOnboardSmokeAdmitsResetAlongsideSmoke(t *testing.T) {
	home, _ := withSmokeResetState(t)
	_ = home
	var stdout, stderr bytes.Buffer
	code := brokerWholeOnboard(
		[]string{"__onboard", "--smoke", "--smoke-reset"},
		strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var body map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode reset receipt %q: %v", stdout.String(), err)
	}
	if body["ok"] != true || body["abandoned"] != true {
		t.Fatalf("receipt = %v", body)
	}
}

// The smoke teardown is a second call site of the read-only directory defect
// that uninstall repairs: a directory without its write bit refuses unlinkat
// for everything inside it, so plain os.RemoveAll fails with "permission
// denied" on a tree the operator fully owns. An agent can leave such a
// directory inside the artifact root of the smoke workspace. A failure here
// leaves the generation on disk and no final receipt.
//
// The other tests replace productionSmokeCleanup with a stub. This one calls
// the real function.
func TestRemoveSmokeWorkspaceDeletesReadOnlyDirectories(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "mc-home")
	original := productionSmokeWorkspaceBase
	t.Cleanup(func() { productionSmokeWorkspaceBase = original })
	workspaceBase := filepath.Join(base, "smoke-workspaces")
	productionSmokeWorkspaceBase = func(string) (string, error) { return workspaceBase, nil }

	state := productionSmokeState{
		Version:   smokeStateVersion,
		Phase:     "archived",
		ProjectID: "smoke-b1d42d4418202086",
		TaskID:    1,
	}
	generation := filepath.Join(workspaceBase, state.ProjectID)
	state.WorkspaceRoot = filepath.Join(generation, "workspace")

	// The shape: a sealed 0555 directory inside the artifact root that holds
	// two child directories with content.
	sealed := filepath.Join(state.WorkspaceRoot, ".mc-artifacts", "1")
	build := func() {
		for _, dir := range []string{filepath.Join(sealed, "packet"), filepath.Join(sealed, "evidence")} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "content"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chmod(sealed, 0o555); err != nil {
			t.Fatal(err)
		}
		// The generation itself must be 0700 and operator-owned or the fences refuse.
		if err := os.Chmod(generation, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	build()
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o700) })

	// The defect, pinned: without the repair this is exactly what happens.
	if err := os.RemoveAll(generation); err == nil {
		t.Fatal("os.RemoveAll unexpectedly succeeded; this fixture no longer reproduces the failure")
	}
	if err := os.RemoveAll(workspaceBase); err != nil {
		_ = os.Chmod(sealed, 0o700)
		if err := os.RemoveAll(workspaceBase); err != nil {
			t.Fatal(err)
		}
	}
	build()

	if err := removeSmokeWorkspace(home, state); err != nil {
		t.Fatalf("removeSmokeWorkspace: %v", err)
	}
	if _, err := os.Stat(generation); !os.IsNotExist(err) {
		t.Fatalf("smoke generation survived teardown: %v", err)
	}
}
