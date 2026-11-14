package main_test

// The resident stand-in for the CLI test tier. A pipeline run receives an
// isolated repository from the resident, and the resident records every
// repository result through `mc delivery record`. These helpers play that part
// against a temp spine, with the real built binary and real Git repositories.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repositoryRunRoot names the host root of the isolated repository of a run.
func repositoryRunRoot(spine, run string) string {
	return filepath.Join(filepath.Dir(spine), "runs", "repositories", run)
}

// subjectRunRoot copies the subject of a repository plan or of an execution
// into a native envelope: one task or one wiki update. It returns the host
// root of the isolated repository of the run. The repositories of wiki
// updates have their own folder.
func subjectRunRoot(spine, run string, source, envelope map[string]any) string {
	if update, ok := source["wiki_update"].(string); ok {
		envelope["wiki_update"] = update
		return filepath.Join(filepath.Dir(spine), "runs", "wiki-repositories", run)
	}
	envelope["task_id"] = source["task_id"]
	return repositoryRunRoot(spine, run)
}

// repositoryNative writes one envelope and runs `mc __repository` on it.
func repositoryNative(t *testing.T, spine string, envelope map[string]any) map[string]any {
	t.Helper()
	runs := filepath.Join(filepath.Dir(spine), "runs")
	if err := os.MkdirAll(runs, 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(runs, fmt.Sprintf("%v.%v.repository.json", envelope["run_id"], envelope["operation"]))
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	res := runMC(t, spineEnv(spine), "", "__repository", path)
	if res.code != 0 {
		t.Fatalf("mc __repository %v failed (%d): %s", envelope["operation"], res.code, res.stderr)
	}
	return res.json
}

// repositoryRecord records one repository result through `mc delivery record`.
func repositoryRecord(t *testing.T, spine, operation, run string, result map[string]any) {
	t.Helper()
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	res := runMC(t, spineEnv(spine), string(body),
		"delivery", "record", "--operation", operation, "--run", run, "--result", "-")
	if res.code != 0 {
		t.Fatalf("mc delivery record %s failed (%d): %s", operation, res.code, res.stderr)
	}
}

// prepareRepositoryRun allocates and readies the isolated repository of a
// spawn, as the resident does before the container starts. It returns the
// run root. The container binds `<root>/repo` at `/workspace/source`; the
// stand-in links `<root>/source` to the same directory.
func prepareRepositoryRun(t *testing.T, spine string, eff map[string]any) string {
	t.Helper()
	mountPlan, _ := eff["mount_plan"].(map[string]any)
	plan, ok := mountPlan["repository"].(map[string]any)
	if !ok {
		t.Fatalf("spawn %v carries no repository plan", eff["role"])
	}
	run := eff["run_id"].(string)
	// The repository of a wiki update run gets the folders that the Scribe
	// container binds for writes.
	envelope := map[string]any{
		"operation": "allocate", "run_id": run, "wiki_only": plan["wiki_update"] != nil,
		"workspace_root": plan["workspace_root"], "input_commit": plan["input_commit"],
		"destination_branch": plan["destination_branch"], "max_temporary_bytes": plan["max_temporary_bytes"],
	}
	root := subjectRunRoot(spine, run, plan, envelope)
	envelope["run_root"] = root
	if version, ok := plan["version_id"]; ok {
		envelope["version_id"] = version
	}
	allocated := repositoryNative(t, spine, envelope)
	repositoryRecord(t, spine, "ready", run, allocated)
	envelope["operation"] = "ready"
	envelope["input_commit"] = allocated["input_commit"]
	envelope["destination_commit"] = allocated["destination_commit"]
	ready := repositoryNative(t, spine, envelope)
	repositoryRecord(t, spine, "ready", run, ready)
	if err := os.Symlink(filepath.Join(root, "repo"), filepath.Join(root, "source")); err != nil {
		t.Fatal(err)
	}
	return root
}

// deliveryOwner returns the owner number of the resident stand-in. It
// registers one through `mc delivery register` when the spine has none.
func deliveryOwner(t *testing.T, spine string) int64 {
	t.Helper()
	var owner sql.NullInt64
	db := openDB(t, spine)
	if err := db.QueryRow(`SELECT MAX(owner) FROM execution_owners`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner.Valid {
		return owner.Int64
	}
	res := runMC(t, spineEnv(spine), "", "delivery", "register")
	if res.code != 0 {
		t.Fatalf("mc delivery register failed (%d): %s", res.code, res.stderr)
	}
	return int64(res.json["owner"].(float64))
}

// startRequest runs one `mc delivery start` request of the resident stand-in.
func startRequest(t *testing.T, spine string) map[string]any {
	t.Helper()
	body := fmt.Sprintf(`{"owner":%d,"running":[],"stopped":[],"drain":false}`, deliveryOwner(t, spine))
	res := runMC(t, spineEnv(spine), body, "delivery", "start", "--request", "-")
	if res.code != 0 {
		t.Fatalf("mc delivery start failed (%d): %s", res.code, res.stderr)
	}
	if failed := res.json["failed"].([]any); len(failed) != 0 {
		t.Fatalf("the start request failed a start: %v", failed)
	}
	return res.json
}

// startedExecutions returns the listed execution of each run that the start
// response started, in start order.
func startedExecutions(t *testing.T, resp map[string]any) []map[string]any {
	t.Helper()
	listed := map[string]map[string]any{}
	for _, raw := range resp["executions"].([]any) {
		execution := raw.(map[string]any)
		listed[execution["run_id"].(string)] = execution
	}
	var out []map[string]any
	for _, run := range resp["started"].([]any) {
		execution, ok := listed[run.(string)]
		if !ok {
			t.Fatalf("the start response omits the started execution %v: %v", run, resp)
		}
		out = append(out, execution)
	}
	return out
}

// startExpect runs one start request and requires that it starts exactly the
// operations, in order. It returns the started executions.
func startExpect(t *testing.T, spine string, operations ...string) []map[string]any {
	t.Helper()
	started := startedExecutions(t, startRequest(t, spine))
	var got []string
	for _, execution := range started {
		got = append(got, execution["operation"].(string))
	}
	if strings.Join(got, ",") != strings.Join(operations, ",") {
		t.Fatalf("the start request started %v, want %v (%v)", got, operations, started)
	}
	return started
}

// startThrough sends start requests and executes each started execution until
// a start request starts nothing. It returns the executions in start order.
func startThrough(t *testing.T, spine string) []map[string]any {
	t.Helper()
	var executed []map[string]any
	for i := 0; i < 24; i++ {
		started := startedExecutions(t, startRequest(t, spine))
		if len(started) == 0 {
			return executed
		}
		for _, execution := range started {
			runExecution(t, spine, execution)
		}
		executed = append(executed, started...)
	}
	t.Fatal("the start requests never reached an idle Project")
	return nil
}

// runExecution executes the host work of one started execution as the
// resident does and records its result. The native envelope names the producer
// run for capture and cleanup, and the interrupted merge for receipt recovery.
func runExecution(t *testing.T, spine string, execution map[string]any) map[string]any {
	t.Helper()
	operation := execution["operation"].(string)
	run := execution["run_id"].(string)
	owner := run
	switch operation {
	case "capture", "cleanup":
		owner = execution["producer_run"].(string)
	case "reconcile":
		owner = execution["previous_run"].(string)
	}
	input, _ := execution["input_commit"].(string)
	if input == "" {
		input, _ = execution["submitted_commit"].(string)
	}
	envelope := map[string]any{
		"operation": operation, "run_id": owner, "workspace_root": execution["workspace_root"],
		"input_commit": input, "destination_branch": execution["destination_branch"],
		"author_name": "Fixture", "author_email": "fixture@example.test", "deadline_ms": execution["deadline_ms"],
	}
	envelope["run_root"] = subjectRunRoot(spine, owner, execution, envelope)
	for _, key := range []string{"start_commit", "result_commit", "submission_kind", "application", "version_id", "max_temporary_bytes"} {
		if value, ok := execution[key]; ok && value != nil {
			envelope[key] = value
		}
	}
	switch operation {
	case "capture", "cleanup", "observe", "apply", "reconcile":
		result := repositoryNative(t, spine, envelope)
		repositoryRecord(t, spine, operation, run, result)
		return result
	case "prepare":
		envelope["operation"] = "allocate"
		allocated := repositoryNative(t, spine, envelope)
		repositoryRecord(t, spine, "ready", run, allocated)
		envelope["operation"] = "prepare-repository"
		envelope["input_commit"] = allocated["input_commit"]
		envelope["destination_commit"] = allocated["destination_commit"]
		prepared := repositoryNative(t, spine, envelope)
		repositoryRecord(t, spine, "ready", run, prepared)
		envelope["operation"] = "prepare"
		envelope["destination_commit"] = prepared["destination_commit"]
		envelope["input_commit"] = prepared["input_commit"]
		facts := repositoryNative(t, spine, envelope)
		envelope["operation"] = "retain-candidate"
		envelope["candidate"] = facts["candidate"]
		result := repositoryNative(t, spine, envelope)
		repositoryRecord(t, spine, operation, run, result)
		return result
	}
	t.Fatalf("the execution %v has an unknown operation", execution)
	return nil
}

// dispatchThrough runs the start requests and their executions before each
// tick, then drives ticks until a tick commits the wanted action. It returns
// the wanted action. A tick with another action fails the test.
func dispatchThrough(t *testing.T, spine, action string) map[string]any {
	t.Helper()
	for i := 0; i < 12; i++ {
		startThrough(t, spine)
		actions := tickActions(t, dispatch(t, spine))
		if len(actions) == 0 {
			continue
		}
		if len(actions) != 1 || actions[0]["action"] != action {
			t.Fatalf("dispatch actions = %v, want one %q", actions, action)
		}
		eff := actions[0]
		if action == "reap" {
			stopped := runMC(t, spineEnv(spine), "", "run", "stopped", eff["run_id"].(string))
			if stopped.code != 0 {
				t.Fatalf("resident stop confirmation failed: %s", stopped.stderr)
			}
		}
		return eff
	}
	t.Fatalf("dispatch never reached action %q", action)
	return nil
}

// spawnRole drives ticks until the wanted role spawns. For a role that works
// in the isolated repository, it readies the repository and returns its root.
func spawnRole(t *testing.T, spine, role string) (map[string]any, string) {
	t.Helper()
	eff := dispatchThrough(t, spine, "spawn")
	return eff, spawnedRole(t, spine, eff, role)
}

// spawnedRole requires the role of a spawn effect. For a role that works in
// the isolated repository, it readies the repository and returns its root.
func spawnedRole(t *testing.T, spine string, eff map[string]any, role string) string {
	t.Helper()
	if eff["role"] != role {
		t.Fatalf("spawn role = %v, want %q (effect %v)", eff["role"], role, eff)
	}
	root := ""
	if mountPlan, ok := eff["mount_plan"].(map[string]any); ok && mountPlan["repository"] != nil {
		root = prepareRepositoryRun(t, spine, eff)
	}
	return root
}

// roleEnv builds the environment of one role container: the run identity, a
// durable artifact plane, and the isolated repository root when the role has
// one. It returns the env and the plane directory.
func roleEnv(t *testing.T, spine, run, role, root string) ([]string, string) {
	t.Helper()
	artifacts := t.TempDir()
	plane := filepath.Join(artifacts, packagerPlaneTarget)
	if err := os.MkdirAll(plane, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "run.json")
	b, _ := json.Marshal(map[string]any{
		"run_id": run, "tier": "pipeline", "role": role,
		"mounts": map[string]any{"allow_write": []string{plane}},
	})
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(spineEnv(spine), "MC_RUN_JSON="+p, "MC_ARTIFACTS_ROOT="+artifacts)
	if root != "" {
		env = append(env, "MC_TASK_STORE_ROOT="+root)
	}
	return env, plane
}

// writePlaneFile writes one artifact into a plane and returns its path.
func writePlaneFile(t *testing.T, plane, name, body string) string {
	t.Helper()
	path := filepath.Join(plane, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// commitContribution writes one file into the isolated repository of a run
// and commits it. It returns the new HEAD commit.
func commitContribution(t *testing.T, root, name, body string) string {
	t.Helper()
	repo := filepath.Join(root, "repo")
	if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, repo, "add", name)
	fixtureGit(t, repo, "commit", "-q", "-m", "Complete the contribution")
	return fixtureGit(t, repo, "rev-parse", "HEAD")
}

// arbiterPromoteAll promotes every proposal in the pool of an Arbiter spawn.
func arbiterPromoteAll(t *testing.T, spine string, eff map[string]any) {
	t.Helper()
	run := eff["run_id"].(string)
	pool := eff["pool_ids"].([]any)
	verdicts := make([]string, 0, len(pool))
	for _, p := range pool {
		verdicts = append(verdicts, fmt.Sprintf(`{"task":%v,"decision":"promote","reason":"ok"}`, p))
	}
	res := runMC(t, runJSONEnv(t, spine, run, "pipeline", "arbiter"),
		`{"verdicts":[`+strings.Join(verdicts, ",")+`]}`,
		"arbiter", "decide", "--run", run, "--batch", "-")
	if res.code != 0 {
		t.Fatalf("arbiter decide failed: %s", res.stderr)
	}
}

// workedTask drives a seeded task through the Worker: the Worker commits one
// file and submits the commit, and the capture and cleanup effects follow. It
// returns the submitted commit.
func workedTask(t *testing.T, spine string, taskID int64) string {
	t.Helper()
	eff, root := spawnRole(t, spine, "worker")
	return workerRun(t, spine, taskID, eff, root)
}

// workerRun completes a spawned Worker run with one committed file and returns the commit.
func workerRun(t *testing.T, spine string, taskID int64, eff map[string]any, root string) string {
	t.Helper()
	run := eff["run_id"].(string)
	env, _ := roleEnv(t, spine, run, "worker", root)
	commit := commitContribution(t, root, fmt.Sprintf("task-%d.txt", taskID), "complete\n")
	res := runMC(t, env, "", "complete", fmt.Sprint(taskID), "--run", run, "--status", "worked", "--commit", commit)
	if res.code != 0 {
		t.Fatalf("complete worked failed: %s", res.stderr)
	}
	return commit
}

// verifiedTask drives a worked task through a passing Verifier verdict.
func verifiedTask(t *testing.T, spine string, taskID int64, commit string) {
	t.Helper()
	eff, root := spawnRole(t, spine, "verifier")
	verifierRun(t, spine, taskID, commit, eff, root)
}

// verifierRun completes a spawned Verifier run with a passing verdict.
func verifierRun(t *testing.T, spine string, taskID int64, commit string, eff map[string]any, root string) {
	t.Helper()
	run := eff["run_id"].(string)
	env, plane := roleEnv(t, spine, run, "verifier", root)
	evidence := writePlaneFile(t, plane, fmt.Sprintf("%d/verification/gate-ladder.md", taskID),
		"Criterion 1: PASS. The file exists with the requested content.\n")
	res := runMC(t, env, "", "verifier", "verdict", fmt.Sprint(taskID), "--run", run,
		"--outcome", "pass", "--evidence", evidence, "--sha", commit)
	if res.code != 0 {
		t.Fatalf("verifier verdict failed: %s", res.stderr)
	}
}

// packagedTask drives a verified task through the Packager and returns the
// render path of the report.
func packagedTask(t *testing.T, spine string, taskID int64) string {
	t.Helper()
	eff, root := spawnRole(t, spine, "packager")
	return packagerRun(t, spine, taskID, eff, root)
}

// packagerRun completes a spawned Packager run and returns the render path of the report.
func packagerRun(t *testing.T, spine string, taskID int64, eff map[string]any, root string) string {
	t.Helper()
	run := eff["run_id"].(string)
	env, plane := roleEnv(t, spine, run, "packager", root)
	render := writePlaneFile(t, plane, fmt.Sprintf("%d/packet/render.html", taskID), "<h1>review packet</h1>\n")
	res := runMC(t, env, "", "complete", fmt.Sprint(taskID), "--run", run,
		"--status", "packaged", "--outputs", render, "--thesis", "ships the thing")
	if res.code != 0 {
		t.Fatalf("complete packaged failed: %s", res.stderr)
	}
	return render
}

// reviewedPacket drives a packaged task through the merge candidate step and
// the review publication, and returns its live packet row. It also runs every
// pending cleanup, so the next tick is free for the decision.
func reviewedPacket(t *testing.T, spine string, taskID int64) map[string]any {
	t.Helper()
	startThrough(t, spine)
	packet, ok := livePacket(t, spine, taskID)
	if !ok {
		t.Fatalf("task %d never reached a live packet", taskID)
	}
	return packet
}

// livePacket returns the unarchived packet of a task, when the task has one.
func livePacket(t *testing.T, spine string, taskID int64) (map[string]any, bool) {
	t.Helper()
	pl := runMC(t, spineEnv(spine), "", "packet", "list")
	if pl.code != 0 {
		t.Fatalf("packet list failed: %s", pl.stderr)
	}
	for _, raw := range pl.json["packets"].([]any) {
		packet := raw.(map[string]any)
		if packet["task_id"] == float64(taskID) && packet["archived"] == float64(0) {
			return packet, true
		}
	}
	return nil, false
}

// packetDecideArgs builds the `mc packet decide` arguments for a reviewed packet.
func packetDecideArgs(taskID int64, packet map[string]any, decision string, reason ...string) []string {
	args := []string{"packet", "decide", fmt.Sprint(taskID), "--" + decision,
		"--packet", fmt.Sprint(int64(packet["packet_id"].(float64))),
		"--version", fmt.Sprint(int64(packet["version_id"].(float64)))}
	if len(reason) == 1 {
		args = append(args, "--reason", reason[0])
	}
	return args
}

// packageTask drives a fresh proposal through Arbiter, Worker, Verifier, and
// Packager through the real CLI, and returns once it is packaged.
func packageTask(t *testing.T, spine string, taskID int64) string {
	t.Helper()
	arbiterPromoteAll(t, spine, dispatchThrough(t, spine, "spawn"))
	commit := workedTask(t, spine, taskID)
	verifiedTask(t, spine, taskID, commit)
	packagedTask(t, spine, taskID)
	return commit
}
