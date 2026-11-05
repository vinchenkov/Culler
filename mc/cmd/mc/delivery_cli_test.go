package main_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// wantKeys fails unless the JSON object has exactly the keys.
func wantKeys(t *testing.T, name string, object map[string]any, keys ...string) {
	t.Helper()
	var got []string
	for key := range object {
		got = append(got, key)
	}
	sort.Strings(got)
	sort.Strings(keys)
	if !reflect.DeepEqual(got, keys) {
		t.Fatalf("%s has the keys %v, want %v", name, got, keys)
	}
}

// wantRefusal fails unless mc exits 1 with the refusal code.
func wantRefusal(t *testing.T, res mcResult, code string) {
	t.Helper()
	refusal, _ := res.json["error"].(map[string]any)
	if res.code != 1 || refusal["code"] != code {
		t.Fatalf("mc returned %d %v, want the refusal %s (stderr %q)", res.code, res.json, code, res.stderr)
	}
}

func TestDeliveryVerbsCLI(t *testing.T) {
	spine := initSpine(t)
	env := spineEnv(spine)
	taskID := proposalTask(t, spine, "exercise the delivery verbs")
	arbiterPromoteAll(t, spine, dispatchThrough(t, spine, "spawn"))
	workedTask(t, spine, taskID)
	earlier := deliveryOwner(t, spine)

	register := runMC(t, env, "", "delivery", "register")
	if register.code != 0 {
		t.Fatalf("mc delivery register failed (%d): %s", register.code, register.stderr)
	}
	wantKeys(t, "the registration", register.json, "owner")
	owner := int64(register.json["owner"].(float64))
	if owner != earlier+1 {
		t.Fatalf("the new owner is %d after owner %d", owner, earlier)
	}

	start := runMC(t, env, fmt.Sprintf(`{"owner":%d,"running":[],"stopped":[],"drain":false}`, owner), "delivery", "start", "--request", "-")
	if start.code != 0 {
		t.Fatalf("mc delivery start failed (%d): %s", start.code, start.stderr)
	}
	wantKeys(t, "the start response", start.json, "owner", "executions", "stops", "confirmed", "started", "failed", "published", "due_in_ms", "drain_pending")
	started := start.json["started"].([]any)
	executions := start.json["executions"].([]any)
	if len(started) != 1 || len(executions) != 1 || start.json["drain_pending"] != true || start.json["due_in_ms"] != nil {
		t.Fatalf("the start response is %v", start.json)
	}
	execution := executions[0].(map[string]any)
	wantKeys(t, "the capture", execution, "run_id", "operation", "task_id", "project", "workspace_root", "destination_branch",
		"input_commit", "result_commit", "submission_kind", "producer_run", "deadline_ms", "container_grace_ms")
	run := started[0].(string)
	if execution["run_id"] != run || execution["operation"] != "capture" || execution["task_id"] != float64(taskID) {
		t.Fatalf("the started execution is %v", execution)
	}

	heartbeat := runMC(t, env, fmt.Sprintf(`{"owner":%d,"running":[%q],"stopped":[],"containers":[]}`, owner, run), "delivery", "heartbeat", "--request", "-")
	if heartbeat.code != 0 {
		t.Fatalf("mc delivery heartbeat failed (%d): %s", heartbeat.code, heartbeat.stderr)
	}
	wantKeys(t, "the heartbeat response", heartbeat.json, "renewed", "unstarted", "stops", "confirmed", "ended", "container_stops")
	if renewed := heartbeat.json["renewed"].([]any); len(renewed) != 1 || renewed[0] != run || len(heartbeat.json["unstarted"].([]any)) != 0 {
		t.Fatalf("the heartbeat response is %v", heartbeat.json)
	}

	wantRefusal(t, runMC(t, env, fmt.Sprintf(`{"owner":%d}`, earlier), "delivery", "start", "--request", "-"), "stale-owner")
	wantRefusal(t, runMC(t, env, fmt.Sprintf(`{"owner":%d}`, earlier), "delivery", "heartbeat", "--request", "-"), "stale-owner")

	fail := runMC(t, env, "", "delivery", "fail", "--run", run, "--condition", "The capture failed.")
	if fail.code != 0 {
		t.Fatalf("mc delivery fail failed (%d): %s", fail.code, fail.stderr)
	}
	wantKeys(t, "the failure report", fail.json, "run_id", "task_id", "charged", "retry_after", "remaining", "stalled")
	if fail.json["run_id"] != run || fail.json["charged"] != true || fail.json["remaining"] != float64(2) || fail.json["stalled"] != false {
		t.Fatalf("the failure report is %v", fail.json)
	}
	if _, ok := fail.json["retry_after"].(string); !ok {
		t.Fatalf("the failure report has no retry time: %v", fail.json)
	}
	wantRefusal(t, runMC(t, env, "", "delivery", "fail", "--run", run, "--condition", "The capture failed."), "stale-run")

	for _, args := range [][]string{
		{"delivery", "start"},
		{"delivery", "heartbeat"},
		{"delivery", "fail", "--run", run},
		{"delivery", "register", "extra"},
	} {
		if res := runMC(t, env, "", args...); res.code != 2 {
			t.Fatalf("mc %v exited %d, want a usage error", args, res.code)
		}
	}
}

// The helper of a test build runs the start request that the host broker
// sends. The request carries the host Vision check, and the helper cannot
// read the workspace file.
func TestDeliveryStartKeepsTheReceivedVisionCheck(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	root := queryStr(t, db, `SELECT p.workspace_root FROM projects w JOIN sandbox_profiles p ON p.id=w.sandbox_profile WHERE w.id='ws-test'`)
	path := filepath.Join(root, "VISION.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	owner := deliveryOwner(t, spine)
	ready := func(request string) int {
		t.Helper()
		res := runMC(t, spineEnv(spine), request, "delivery", "start", "--request", "-")
		if res.code != 0 {
			t.Fatalf("mc delivery start failed (%d): %s", res.code, res.stderr)
		}
		return queryInt(t, db, `SELECT ready FROM vision_checks WHERE project='ws-test'`)
	}
	if got := ready(fmt.Sprintf(`{"owner":%d}`, owner)); got != 0 {
		t.Fatalf("a request without a check records ready=%d for an absent VISION.md", got)
	}
	check, err := json.Marshal(map[string]any{"state": "present", "path": path, "ready": true, "content": string(body)})
	if err != nil {
		t.Fatal(err)
	}
	if got := ready(fmt.Sprintf(`{"owner":%d,"vision":{"ws-test":%s}}`, owner, check)); got != 1 {
		t.Fatalf("a request with the ready host check records ready=%d", got)
	}
}
