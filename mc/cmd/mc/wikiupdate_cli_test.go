package main_test

// A wiki update through the real CLI, against a temp spine and a real Git
// repository. The tick starts the Scribe run with no task. The stand-in for
// the Scribe commits one wiki page and calls the terminal action. The start
// requests of the resident stand-in then merge the wiki commit into culler.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"mc/wiki"
	"mc/wikitest"
)

// fixtureWorkspace returns the workspace of ws-test.
func fixtureWorkspace(t *testing.T, db *sql.DB) string {
	t.Helper()
	return queryStr(t, db, `SELECT p.workspace_root FROM projects w JOIN sandbox_profiles p ON p.id = w.sandbox_profile WHERE w.id = 'ws-test'`)
}

// fixtureMain returns the commit of culler in a workspace.
func fixtureMain(t *testing.T, workspace string) string {
	t.Helper()
	return fixtureGit(t, workspace, "rev-parse", "refs/heads/culler")
}

// recordEarlierWikiUpdate records a successful wiki update of ws-test at the
// commit of culler. The wiki update started before the wiki update interval, so
// the wiki stays current and a new commit on culler makes a wiki update due.
func recordEarlierWikiUpdate(t *testing.T, db *sql.DB, workspace string) string {
	t.Helper()
	commit := fixtureMain(t, workspace)
	if _, recorded, err := wikitest.RecordCurrentWiki(context.Background(), db, "ws-test", commit, time.Now().Add(-2*time.Hour)); err != nil || !recorded {
		t.Fatalf("record the earlier wiki update: recorded=%v err=%v", recorded, err)
	}
	return commit
}

// tickScribeSpawns returns each Scribe spawn of a tick. An action with an
// error fails the test.
func tickScribeSpawns(t *testing.T, tick map[string]any) []map[string]any {
	t.Helper()
	var spawns []map[string]any
	for _, action := range tickActions(t, tick) {
		if _, failed := action["error"]; failed {
			t.Fatalf("the tick holds an error: %v", action)
		}
		if action["action"] == "spawn" && action["role"] == "scribe" {
			spawns = append(spawns, action)
		}
	}
	return spawns
}

// wikiRows counts the Scribe runs and the wiki updates of ws-test.
func wikiRows(t *testing.T, db *sql.DB) (runs, updates int) {
	t.Helper()
	return queryInt(t, db, `SELECT COUNT(*) FROM runs WHERE project = 'ws-test' AND role = 'scribe'`),
		queryInt(t, db, `SELECT COUNT(*) FROM wiki_updates WHERE project = 'ws-test'`)
}

// wikiStateRow returns the row of ws-test from `mc wiki-state list`.
func wikiStateRow(t *testing.T, spine string) map[string]any {
	t.Helper()
	res := runMC(t, spineEnv(spine), "", "wiki-state", "list")
	if res.code != 0 {
		t.Fatalf("wiki-state list failed (%d): %s", res.code, res.stderr)
	}
	rows, _ := res.json["wiki_states"].([]any)
	for _, raw := range rows {
		if row, ok := raw.(map[string]any); ok && row["project"] == "ws-test" {
			return row
		}
	}
	t.Fatalf("wiki-state list holds no row of ws-test: %s", res.stdout)
	return nil
}

// requireCurrentWiki requires the wiki state current at the commit, with the
// run as the last wiki update. merged is the commit that the wiki update
// merged into culler.
func requireCurrentWiki(t *testing.T, spine string, w wikiUpdateRun, wikiCommit, merged string) {
	t.Helper()
	row := wikiStateRow(t, spine)
	last, _ := row["last"].(map[string]any)
	if row["state"] != "current" || row["state_text"] != "The Project wiki is current." || row["accepted_commit"] != merged || row["active"] != nil ||
		last["run_id"] != w.id || last["result"] != "succeeded" || last["cause_text"] != nil ||
		last["start_commit"] != w.start || last["wiki_commit"] != wikiCommit || last["merged_commit"] != merged {
		t.Fatalf("wiki state = %v, want a current wiki after the merge of %s as %s by the run %s", row, wikiCommit, merged, w.id)
	}
}

// briefPartText returns one part of a run brief, from its heading to the next
// heading. It returns "" for an absent part.
func briefPartText(brief, heading string) string {
	start := strings.Index(brief, "\n## "+heading+"\n")
	if start < 0 {
		return ""
	}
	part := brief[start+1:]
	if end := strings.Index(part, "\n## "); end >= 0 {
		part = part[:end]
	}
	return part
}

// mergedTaskIDs reads the task identifiers of the Merged tasks part of a
// Scribe brief, in the order of the part. It returns nil for an absent part.
func mergedTaskIDs(t *testing.T, brief string) []int64 {
	t.Helper()
	part := briefPartText(brief, "Merged tasks")
	if part == "" {
		return nil
	}
	_, block, found := strings.Cut(part, "```json\n")
	body, _, closed := strings.Cut(block, "\n```")
	var rows []struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	}
	if !found || !closed || json.Unmarshal([]byte(body), &rows) != nil {
		t.Fatalf("the Merged tasks part holds no JSON block of task rows:\n%s", part)
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		if row.Title == "" {
			t.Fatalf("the Merged tasks part holds a row with no title:\n%s", part)
		}
		ids = append(ids, row.ID)
	}
	return ids
}

// wikiUpdateRun is the Scribe run of one wiki update of ws-test, with the
// isolated repository that the resident stand-in readied for it.
type wikiUpdateRun struct {
	spine, id, start, root, brief string
	// env is the environment of the Scribe of the run.
	env []string
}

// startedWikiUpdate requires exactly one Scribe spawn in the tick, with no
// task. It readies the isolated repository of the run as the resident does.
func startedWikiUpdate(t *testing.T, spine string, tick map[string]any) wikiUpdateRun {
	t.Helper()
	spawns := tickScribeSpawns(t, tick)
	if len(spawns) != 1 || spawns[0]["project"] != "ws-test" || spawns[0]["subject_id"] != nil {
		t.Fatalf("Scribe spawns = %v, want one wiki update of ws-test with no task (tick %v)", spawns, tick)
	}
	eff := spawns[0]
	id, _ := eff["run_id"].(string)
	brief, _ := eff["brief"].(string)
	root := prepareRepositoryRun(t, spine, eff)
	w := wikiUpdateRun{spine: spine, id: id, root: root, brief: brief,
		start: fixtureGit(t, filepath.Join(root, "repo"), "rev-parse", "HEAD"),
		env:   append(runJSONEnv(t, spine, id, "pipeline", "scribe"), "MC_TASK_STORE_ROOT="+root)}
	plan := eff["mount_plan"].(map[string]any)["repository"].(map[string]any)
	if plan["wiki_update"] != id || plan["input_commit"] != w.start || plan["destination_branch"] != "culler" {
		t.Fatalf("repository plan = %v, want the wiki update %s on culler at the commit %s of its repository", plan, id, w.start)
	}
	if got := "\n" + briefPartText(brief, "Git state"); !strings.Contains(got, "\n- Start commit: "+w.start+"\n") {
		t.Fatalf("the Git state part of the brief does not name the start commit %s:\n%s", w.start, got)
	}
	return w
}

// submitPage plays the Scribe. It adds one generated wiki page in the
// repository of the run, commits it, writes the completion receipt of the
// commit, and calls the terminal action. It returns the wiki commit.
func (w wikiUpdateRun) submitPage(t *testing.T, name, text string) string {
	t.Helper()
	repo := filepath.Join(w.root, "repo")
	page := "---\ntitle: " + strings.TrimSuffix(name, ".md") + "\ntype: Component\n---\n" + text + "\n"
	if err := os.WriteFile(filepath.Join(repo, "openwiki", name), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, repo, "add", "openwiki")
	fixtureGit(t, repo, "commit", "-q", "-m", "Update the Project wiki")
	commit := fixtureGit(t, repo, "rev-parse", "HEAD")

	files := map[string][]byte{}
	if err := filepath.WalkDir(filepath.Join(repo, "openwiki"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(repo, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)], err = os.ReadFile(path)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	receipt, err := json.Marshal(wiki.Completion{Format: 1, Status: "complete", InputCommit: w.start, SourceCommit: w.start,
		WikiDigest: wiki.Digest(files), OpenWikiVersion: wiki.Version, OpenWikiRevision: wiki.Revision})
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(t.TempDir(), "openwiki-completion.json")
	if err := os.WriteFile(receiptPath, receipt, 0o600); err != nil {
		t.Fatal(err)
	}
	res := runMC(t, w.env, "", "complete", "--run", w.id, "--commit", commit, "--wiki-evidence", receiptPath)
	if res.code != 0 || res.json["run_id"] != w.id || res.json["wiki_commit"] != commit || res.json["wiki_change"] != true {
		t.Fatalf("terminal action of the Scribe: exit code %d, stdout %s, stderr %s; want the wiki commit %s", res.code, res.stdout, res.stderr, commit)
	}
	return commit
}

// merge plays the resident after the terminal action of the Scribe. The tick
// orders the stop of the ended run, the stand-in confirms the stop, and the
// start requests capture and merge the wiki commit. It returns the operation
// of each execution in start order.
func (w wikiUpdateRun) merge(t *testing.T) []string {
	t.Helper()
	tick := dispatch(t, w.spine)
	reap, found := tickFindAction(t, tick, "reap")
	if !found || reap["run_id"] != w.id || reap["reason"] != "reported-outcome" {
		t.Fatalf("tick after the terminal action = %v, want the stop of the ended run %s", tick, w.id)
	}
	if stopped := runMC(t, spineEnv(w.spine), "", "run", "stopped", w.id); stopped.code != 0 {
		t.Fatalf("stop confirmation of the run %s failed: %s", w.id, stopped.stderr)
	}
	var operations []string
	for _, execution := range startThrough(t, w.spine) {
		if execution["wiki_update"] != w.id {
			t.Fatalf("the start requests started an operation of another subject: %v", execution)
		}
		operations = append(operations, execution["operation"].(string))
	}
	return operations
}

// The operations of a wiki update with a wiki commit, in start order.
var wikiMergeOperations = []string{"capture", "prepare", "cleanup", "apply", "cleanup"}

// Two tasks start from one commit of culler, and the operator approves both.
// Both merge with no correction. One wiki update then covers both tasks: its
// brief names both, and culler gets one wiki commit.
func TestWikiUpdateCoversBothMergedTasks(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	workspace := fixtureWorkspace(t, db)
	start := recordEarlierWikiUpdate(t, db, workspace)
	if limits := runMC(t, spineEnv(spine), "", "delivery", "limits", "--project", "ws-test", "--quiet-seconds", "1"); limits.code != 0 {
		t.Fatalf("set the quiet interval: %s", limits.stderr)
	}
	first := proposalTask(t, spine, "Add the first source file")
	second := proposalTask(t, spine, "Add the second source file")
	arbiterPromoteAll(t, spine, dispatchThrough(t, spine, "spawn"))

	// Each task gets its Worker, its Verifier, and its Packager. No merge runs
	// before both tasks are in review, so both Worker runs start at one commit.
	commits := map[int64]string{}
	for step := 0; step < 6; step++ {
		eff := dispatchThrough(t, spine, "spawn")
		role, _ := eff["role"].(string)
		subject, _ := eff["subject_id"].(float64)
		task := int64(subject)
		if task != first && task != second {
			t.Fatalf("step %d spawned %v, want a role of one of the two tasks", step, eff)
		}
		root := spawnedRole(t, spine, eff, role)
		switch role {
		case "worker":
			commits[task] = workerRun(t, spine, task, eff, root)
		case "verifier":
			verifierRun(t, spine, task, commits[task], eff, root)
		case "packager":
			packagerRun(t, spine, task, eff, root)
		default:
			t.Fatalf("step %d spawned %v, want a Worker, a Verifier, or a Packager", step, eff)
		}
	}
	packets := map[int64]map[string]any{first: reviewedPacket(t, spine, first), second: reviewedPacket(t, spine, second)}
	if n := queryInt(t, db, `SELECT COUNT(*) FROM repository_runs p JOIN runs r ON r.id = p.run_id WHERE r.role = 'worker' AND p.input_commit = ?`, start); n != 2 {
		t.Fatalf("%d Worker runs started at the commit %s of culler, want both", n, start)
	}
	if got := fixtureMain(t, workspace); got != start {
		t.Fatalf("culler moved to %s before an approval", got)
	}
	if runs, updates := wikiRows(t, db); runs != 2 || updates != 2 {
		t.Fatalf("%d Scribe runs and %d wiki updates before the merges, want the two records of the fixture", runs, updates)
	}

	// The merge of the first task changes culler, so the second task waits one
	// quiet interval and gets a new merge candidate. No tick runs here.
	for _, task := range []int64{first, second} {
		if res := runMC(t, spineEnv(spine), "", packetDecideArgs(task, packets[task], "approve")...); res.code != 0 {
			t.Fatalf("approval of task %d failed (%d): %s", task, res.code, res.stderr)
		}
	}
	var steps []string
	deadline := time.Now().Add(time.Minute)
	for queryInt(t, db, `SELECT COUNT(*) FROM tasks WHERE archived = 1`) != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the two approved tasks did not merge: %v", steps)
		}
		resp := startRequest(t, spine)
		started := startedExecutions(t, resp)
		for _, execution := range started {
			result := runExecution(t, spine, execution)
			if operation := execution["operation"]; operation != "cleanup" {
				steps = append(steps, fmt.Sprintf("%v/%v/%v", operation, execution["task_id"], result["status"]))
			}
		}
		if len(started) == 0 {
			due, waits := resp["due_in_ms"].(float64)
			if !waits {
				t.Fatalf("a start request started no operation and names no wait, with a task that did not merge: %v", resp)
			}
			time.Sleep(time.Duration(due)*time.Millisecond + 50*time.Millisecond)
		}
	}
	startThrough(t, spine)
	// The second merge finds the commit of the first merge on culler. The task
	// gets a new merge candidate and merges with no new decision.
	wantSteps := []string{
		fmt.Sprintf("apply/%d/", first),
		fmt.Sprintf("apply/%d/destination-changed", second),
		fmt.Sprintf("observe/%d/", second),
		fmt.Sprintf("prepare/%d/", second),
		fmt.Sprintf("apply/%d/", second),
	}
	if !reflect.DeepEqual(steps, wantSteps) {
		t.Fatalf("merge steps = %v, want %v", steps, wantSteps)
	}
	afterTasks := fixtureMain(t, workspace)
	for _, task := range []int64{first, second} {
		if got := queryStr(t, db, `SELECT decision || '/' || correction_count || '/' || correction_open FROM tasks WHERE id = ?`, task); got != "approved/0/0" {
			t.Fatalf("task %d after its merge = %s, want the approval with no correction", task, got)
		}
		if got := fixtureGit(t, workspace, "show", fmt.Sprintf("culler:task-%d.txt", task)); got != "complete" {
			t.Fatalf("culler:task-%d.txt = %q", task, got)
		}
	}
	if n := queryInt(t, db, `SELECT COUNT(*) FROM review_decisions`); n != 2 {
		t.Fatalf("%d review decisions, want one approval for each task", n)
	}
	// The run of each repository operation has the binding mechanical.
	if n := queryInt(t, db, `SELECT COUNT(*) FROM runs WHERE role IN ('worker', 'verifier', 'packager') AND binding IS NOT 'mechanical'`); n != 6 {
		t.Fatalf("%d agent runs of the Worker, the Verifier, and the Packager, want three for each task", n)
	}

	// One tick starts one wiki update at the commit that holds both merges.
	update := startedWikiUpdate(t, spine, dispatch(t, spine))
	if update.start != afterTasks {
		t.Fatalf("the wiki update starts at %s, want the commit %s of culler after both merges", update.start, afterTasks)
	}
	if got, want := mergedTaskIDs(t, update.brief), []int64{first, second}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the Merged tasks part names the tasks %v, want %v:\n%s", got, want, update.brief)
	}
	if got := "\n" + briefPartText(update.brief, "Git state"); !strings.Contains(got, "\n- Start commit of the last successful wiki update: "+start+"\n") {
		t.Fatalf("the Git state part does not name the start commit %s of the last successful wiki update:\n%s", start, got)
	}
	wikiCommit := update.submitPage(t, "tasks.md", "The Project holds two source files.")
	if got := update.merge(t); !reflect.DeepEqual(got, wikiMergeOperations) {
		t.Fatalf("operations of the wiki update = %v, want %v", got, wikiMergeOperations)
	}

	// Main gets the one wiki commit, and the commit changes only the wiki.
	merged := fixtureMain(t, workspace)
	if merged != wikiCommit || fixtureGit(t, workspace, "rev-list", "--count", afterTasks+".."+merged) != "1" {
		t.Fatalf("culler = %s, want the one wiki commit %s above %s", merged, wikiCommit, afterTasks)
	}
	if changed := fixtureGit(t, workspace, "diff", "--name-only", afterTasks, merged); changed != "openwiki/tasks.md" {
		t.Fatalf("the wiki commit changes %q, want only openwiki/tasks.md", changed)
	}
	requireCurrentWiki(t, spine, update, wikiCommit, merged)

	// The wiki commit on culler gives no second wiki update.
	for tick := 0; tick < 2; tick++ {
		if spawns := tickScribeSpawns(t, dispatch(t, spine)); len(spawns) != 0 {
			t.Fatalf("tick %d after the merge started a wiki update: %v", tick, spawns)
		}
	}
	if runs, updates := wikiRows(t, db); runs != 3 || updates != 3 {
		t.Fatalf("%d Scribe runs and %d wiki updates, want one wiki update above the two records of the fixture", runs, updates)
	}
}

// A commit from outside the pipeline makes the wiki behind. The next wiki
// update starts at that commit, and its merge makes the wiki current.
func TestOutsideCommitMakesTheWikiBehind(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	workspace := fixtureWorkspace(t, db)
	recordEarlierWikiUpdate(t, db, workspace)
	if row := wikiStateRow(t, spine); row["state"] != "current" {
		t.Fatalf("wiki state before the commit = %v, want current", row)
	}

	// A start request stores the new commit of culler and starts no agent run.
	outside := moveFixtureMain(t, workspace, "outside.md")
	startRequest(t, spine)
	if row := wikiStateRow(t, spine); row["state"] != "behind" || row["state_text"] != "The Project wiki is behind the accepted commit." ||
		row["accepted_commit"] != outside || row["active"] != nil {
		t.Fatalf("wiki state after the commit = %v, want behind at %s with no active wiki update", row, outside)
	}
	if runs, updates := wikiRows(t, db); runs != 2 || updates != 2 {
		t.Fatalf("%d Scribe runs and %d wiki updates before the tick, want the two records of the fixture", runs, updates)
	}

	// The tick starts one wiki update at the commit. No task merged, so the
	// brief holds no Merged tasks part.
	update := startedWikiUpdate(t, spine, dispatch(t, spine))
	if update.start != outside {
		t.Fatalf("the wiki update starts at %s, want the commit %s from outside the pipeline", update.start, outside)
	}
	if _, err := os.Stat(filepath.Join(update.root, "repo", "outside.md")); err != nil {
		t.Fatalf("the repository of the Scribe lacks the file of the commit: %v", err)
	}
	if ids := mergedTaskIDs(t, update.brief); ids != nil {
		t.Fatalf("the brief names the merged tasks %v, want no Merged tasks part", ids)
	}
	if row := wikiStateRow(t, spine); row["state"] != "update-active" || row["state_text"] != "A wiki update is active." {
		t.Fatalf("wiki state during the wiki update = %v, want update-active", row)
	}
	wikiCommit := update.submitPage(t, "outside.md", "The Project holds a file from outside the pipeline.")
	if got := update.merge(t); !reflect.DeepEqual(got, wikiMergeOperations) {
		t.Fatalf("operations of the wiki update = %v, want %v", got, wikiMergeOperations)
	}

	merged := fixtureMain(t, workspace)
	if merged != wikiCommit || fixtureGit(t, workspace, "diff", "--name-only", outside, merged) != "openwiki/outside.md" {
		t.Fatalf("culler = %s, want the wiki commit %s with one new wiki page above %s", merged, wikiCommit, outside)
	}
	requireCurrentWiki(t, spine, update, wikiCommit, merged)
	if spawns := tickScribeSpawns(t, dispatch(t, spine)); len(spawns) != 0 {
		t.Fatalf("the tick after the merge started a wiki update: %v", spawns)
	}
	if runs, updates := wikiRows(t, db); runs != 3 || updates != 3 {
		t.Fatalf("%d Scribe runs and %d wiki updates, want one wiki update for the commit", runs, updates)
	}
}

// A wiki update starts while a Worker run of the same Project holds the
// lease. The wiki commit merges while the Worker run is active, and each run
// ends with its own result.
func TestWikiUpdateRunsBesideAWorkerRun(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	workspace := fixtureWorkspace(t, db)
	start := recordEarlierWikiUpdate(t, db, workspace)
	task := proposalTask(t, spine, "Add a source file")
	arbiterPromoteAll(t, spine, dispatchThrough(t, spine, "spawn"))
	worker, workerRoot := spawnRole(t, spine, "worker")
	workerID := worker["run_id"].(string)
	workerEnv, _ := roleEnv(t, spine, workerID, "worker", workerRoot)
	lease := func() string {
		t.Helper()
		return queryStr(t, db, `SELECT owner || '/' || run_id FROM leases WHERE project = 'ws-test'`)
	}
	if got := lease(); got != "worker/"+workerID {
		t.Fatalf("lease = %s, want the Worker run %s", got, workerID)
	}

	// Main moves while the Worker run is active. The lease stops the next
	// step of the task, and the same tick starts the wiki update.
	outside := moveFixtureMain(t, workspace, "outside.md")
	tick := dispatch(t, spine)
	if idle := tickIdle(t, tick); idle["ws-test"] != "lease-held" {
		t.Fatalf("idle reasons = %v, want the held lease of ws-test", idle)
	}
	if actions := tickActions(t, tick); len(actions) != 1 {
		t.Fatalf("tick actions = %v, want only the Scribe spawn", actions)
	}
	update := startedWikiUpdate(t, spine, tick)
	if update.start != outside {
		t.Fatalf("the wiki update starts at %s, want the new commit %s of culler", update.start, outside)
	}
	if got := lease(); got != "worker/"+workerID {
		t.Fatalf("lease after the Scribe spawn = %s, want the Worker run %s", got, workerID)
	}
	if got := queryStr(t, db, `SELECT COUNT(*) FROM runs WHERE project = 'ws-test' AND ended_at IS NULL`); got != "2" {
		t.Fatalf("%s active runs, want the Worker run and the Scribe run", got)
	}
	for id, env := range map[string][]string{workerID: workerEnv, update.id: update.env} {
		if hb := runMC(t, env, "", "heartbeat", id); hb.code != 0 {
			t.Fatalf("heartbeat of the run %s failed (%d): %s", id, hb.code, hb.stderr)
		}
	}

	// The Scribe ends first. Its wiki commit merges while the Worker run is active.
	wikiCommit := update.submitPage(t, "outside.md", "The Project holds a file from outside the pipeline.")
	if got := update.merge(t); !reflect.DeepEqual(got, wikiMergeOperations) {
		t.Fatalf("operations of the wiki update = %v, want %v", got, wikiMergeOperations)
	}
	merged := fixtureMain(t, workspace)
	if merged != wikiCommit {
		t.Fatalf("culler = %s, want the wiki commit %s", merged, wikiCommit)
	}
	requireCurrentWiki(t, spine, update, wikiCommit, merged)
	if got := queryStr(t, db, `SELECT outcome FROM runs WHERE id = ?`, update.id); got != "completed" {
		t.Fatalf("outcome of the Scribe run = %s, want completed", got)
	}
	if got := queryStr(t, db, `SELECT ended_at FROM runs WHERE id = ?`, workerID); got != "<NULL>" || lease() != "worker/"+workerID {
		t.Fatalf("the wiki update ended the Worker run (ended_at %s, lease %s)", got, lease())
	}
	if hb := runMC(t, workerEnv, "", "heartbeat", workerID); hb.code != 0 {
		t.Fatalf("heartbeat of the Worker run after the wiki merge failed (%d): %s", hb.code, hb.stderr)
	}

	// The Worker run then ends with its own result, from the commit of its start.
	commit := workerRun(t, spine, task, worker, workerRoot)
	startThrough(t, spine)
	if got := queryStr(t, db, `SELECT status || '/' || completion_sha || '/' || infrastructure_failure_count FROM tasks WHERE id = ?`, task); got != "worked/"+commit+"/0" {
		t.Fatalf("task after the Worker run = %s, want worked at %s with no failure", got, commit)
	}
	if got := queryStr(t, db, `SELECT r.outcome || '/' || v.input_commit FROM runs r JOIN task_versions v ON v.run_id = r.id WHERE r.id = ?`, workerID); got != "completed/"+start {
		t.Fatalf("Worker run = %s, want completed from the commit %s", got, start)
	}
	if runs, updates := wikiRows(t, db); runs != 3 || updates != 3 {
		t.Fatalf("%d Scribe runs and %d wiki updates, want one wiki update above the two records of the fixture", runs, updates)
	}
}

// A tick on a Project with a current wiki inserts no Scribe run and no wiki
// update, also after the wiki update interval. Main gets no commit.
func TestTickOnACurrentWikiInsertsNoRun(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	workspace := fixtureWorkspace(t, db)
	commit := recordEarlierWikiUpdate(t, db, workspace)
	runs, updates := wikiRows(t, db)
	for tick := 0; tick < 3; tick++ {
		if spawns := tickScribeSpawns(t, dispatch(t, spine)); len(spawns) != 0 {
			t.Fatalf("tick %d started a wiki update for a current wiki: %v", tick, spawns)
		}
		startThrough(t, spine)
	}
	if r, u := wikiRows(t, db); r != runs || u != updates {
		t.Fatalf("%d Scribe runs and %d wiki updates after the ticks, want %d and %d", r, u, runs, updates)
	}
	if got := fixtureMain(t, workspace); got != commit {
		t.Fatalf("culler moved from %s to %s", commit, got)
	}
	if row := wikiStateRow(t, spine); row["state"] != "current" || row["accepted_commit"] != commit {
		t.Fatalf("wiki state = %v, want current at %s", row, commit)
	}

	// The same fixture starts a wiki update after a new commit on culler, so
	// only the current wiki stopped the start.
	moved := moveFixtureMain(t, workspace, "outside.md")
	if update := startedWikiUpdate(t, spine, dispatch(t, spine)); update.start != moved {
		t.Fatalf("the wiki update starts at %s, want the new commit %s", update.start, moved)
	}
}

// A tick on a paused Project inserts no Scribe run and no wiki update,
// although its wiki is behind and the wiki update interval ended. The first
// tick after the resume starts the wiki update.
func TestTickOnAPausedProjectInsertsNoWikiUpdate(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	workspace := fixtureWorkspace(t, db)
	recordEarlierWikiUpdate(t, db, workspace)
	if pause := runMC(t, spineEnv(spine), "", "project", "pause", "ws-test"); pause.code != 0 {
		t.Fatalf("project pause failed: %s", pause.stderr)
	}
	moved := moveFixtureMain(t, workspace, "outside.md")
	runs, updates := wikiRows(t, db)
	for tick := 0; tick < 3; tick++ {
		if actions := tickActions(t, dispatch(t, spine)); len(actions) != 0 {
			t.Fatalf("tick %d on the paused Project committed %v, want no action", tick, actions)
		}
		startThrough(t, spine)
	}
	if r, u := wikiRows(t, db); r != runs || u != updates {
		t.Fatalf("%d Scribe runs and %d wiki updates on the paused Project, want %d and %d", r, u, runs, updates)
	}
	if got := fixtureMain(t, workspace); got != moved {
		t.Fatalf("culler moved from %s to %s", moved, got)
	}
	if row := wikiStateRow(t, spine); row["state"] != "behind" || row["accepted_commit"] != moved || row["active"] != nil {
		t.Fatalf("wiki state of the paused Project = %v, want behind at %s with no active wiki update", row, moved)
	}

	if resume := runMC(t, spineEnv(spine), "", "project", "resume", "ws-test"); resume.code != 0 {
		t.Fatalf("project resume failed: %s", resume.stderr)
	}
	if update := startedWikiUpdate(t, spine, dispatch(t, spine)); update.start != moved {
		t.Fatalf("the wiki update after the resume starts at %s, want %s", update.start, moved)
	}
}
