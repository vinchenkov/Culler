package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func operatorTransportBody(t *testing.T, id string, args []string, input string) string {
	t.Helper()
	help := runMC(t, nil, "", "--help")
	if help.code != 0 {
		t.Fatal(help.stderr)
	}
	body, err := json.Marshal(map[string]any{"id": id, "command": args, "input": input, "release": help.json["release"], "origin": "external"})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestOperatorBridgeCLIProtectsCredentialOperations(t *testing.T) {
	spine := initSpine(t)
	permissions := []string{"agent-profile.operation-start", "agent-profile.operation-cancel"}
	started := runMC(t, spineEnv(spine), "", "collie", "start", "--from", "cli:credential-boundary", "--allow", strings.Join(permissions, ","))
	if started.code != 0 {
		t.Fatal(started.stderr)
	}
	env := collieJSONEnv(t, spine, started.json["session_id"].(string), permissions)
	root := filepath.Join(filepath.Dir(spine), "agent-profile-operations")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "authorization-fixture.json")
	body, _ := json.Marshal(map[string]any{"operation_id": "authorization-fixture", "kind": "authorization", "status": "running", "authorization_url": "https://example.invalid/private-grant", "user_code": "PRIVATE-CODE", "started_at": time.Now().UTC().Format(time.RFC3339Nano)})
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"operation-status", "operation-cancel"} {
		result := runMC(t, env, `{"operation_id":"authorization-fixture"}`, "agent-profile", action)
		if result.code != 1 || !strings.Contains(result.stderr, "credential authorization") || strings.Contains(result.stdout, "PRIVATE-CODE") || strings.Contains(result.stdout, "private-grant") {
			t.Fatalf("Collie reached the credential operation: %s %d %s %s", action, result.code, result.stdout, result.stderr)
		}
		current, err := os.ReadFile(path)
		if err != nil || string(current) != string(body) {
			t.Fatal("A refused operation changed credential authorization.")
		}
	}
	host := runMC(t, spineEnv(spine), `{"operation_id":"authorization-fixture"}`, "agent-profile", "operation-status")
	if host.code != 0 || host.json["user_code"] != "PRIVATE-CODE" {
		t.Fatalf("The host lost credential authorization: %v %s", host.json, host.stderr)
	}
	input := `{"operation_id":"authorization-new","kind":"authorization","connection_id":"absent"}`
	denied := runMC(t, env, operatorTransportBody(t, "collie-authorization", []string{"agent-profile", "operation-start"}, input), "__operator-submit")
	if denied.code != 2 {
		t.Fatalf("Collie admitted credential authorization: %s", denied.stdout)
	}
	admitted := runMC(t, spineEnv(spine), operatorTransportBody(t, "host-authorization", []string{"agent-profile", "operation-start"}, input), "__operator-submit")
	if admitted.code != 0 || admitted.json["state"] != "pending" {
		t.Fatalf("The host lost credential admission: %s %s", admitted.stdout, admitted.stderr)
	}
}

func TestOperatorBridgeCLIRejectsForgedTransportAndPreservesReceipt(t *testing.T) {
	spine := initSpine(t)
	started := runMC(t, spineEnv(spine), "", "collie", "start", "--from", "cli:operator-bridge", "--allow", "project.pause")
	if started.code != 0 {
		t.Fatal(started.stderr)
	}
	session := started.json["session_id"].(string)
	env := collieJSONEnv(t, spine, session, []string{"project.pause"})
	stalePath := filepath.Join(t.TempDir(), "stale.json")
	staleBody, _ := json.Marshal(map[string]any{"run_id": session, "tier": "collie", "verb_allowlist": []string{"project.pause"}, "release_build_id": "another-release"})
	if err := os.WriteFile(stalePath, staleBody, 0644); err != nil {
		t.Fatal(err)
	}
	staleEnv := append(spineEnv(spine), "MC_RUN_JSON="+stalePath)
	stale := runMC(t, staleEnv, operatorTransportBody(t, "stale", []string{"project", "pause", "ws-test"}, ""), "__operator-submit")
	if stale.code != 1 || !strings.Contains(stale.stderr, "different releases") {
		t.Fatalf("A raw request bypassed its stale launch: %v %s", stale.json, stale.stderr)
	}
	forged := runMC(t, env, `{"id":"forged","command":["project","pause","ws-test"],"release":"dev","identity":null}`, "__operator-submit")
	if forged.code != 2 {
		t.Fatalf("The transport accepted a caller identity: %v %s", forged.json, forged.stderr)
	}
	internal := runMC(t, env, operatorTransportBody(t, "internal", []string{"agent-profile", "credential-read"}, `{"id":"secret"}`), "__operator-submit")
	if internal.code != 1 {
		t.Fatalf("The transport accepted an internal command: %v %s", internal.json, internal.stderr)
	}
	body := operatorTransportBody(t, "pause-request", []string{"project", "pause", "ws-test"}, "")
	submitted := runMC(t, env, body, "__operator-submit")
	if submitted.code != 0 || submitted.json["origin"] != "collie" || submitted.json["state"] != "pending" {
		t.Fatalf("The request lost its caller: %v %s", submitted.json, submitted.stderr)
	}
	for _, args := range [][]string{{"__operator-execute", "pause-request"}, {"__operator-forward", "pause-request", "--", "project", "pause", "ws-test"}} {
		denied := runMC(t, env, "", args...)
		if denied.code != 1 {
			t.Fatalf("An agent acquired host execution: %v %v", args, denied.json)
		}
	}
	db := openDB(t, spine)
	if got := queryStr(t, db, `SELECT status FROM projects WHERE id='ws-test'`); got != "active" {
		t.Fatalf("A denied request changed the Project: %s", got)
	}
	executed := runMC(t, spineEnv(spine), "", "__operator-execute", "pause-request")
	if executed.code != 0 {
		t.Fatal(executed.stderr)
	}
	receipt := runMC(t, env, "", "operation", "get", "pause-request")
	op := receipt.json["operation"].(map[string]any)
	if receipt.code != 0 || op["state"] != "complete" || op["session_id"] != session || op["exit_code"] != float64(0) {
		t.Fatalf("The receipt is invalid: %v %s", receipt.json, receipt.stderr)
	}
	if _, exists := op["identity"]; exists {
		t.Fatal("The public receipt exposes the launch identity.")
	}
	if replay := runMC(t, env, body, "__operator-submit"); replay.code != 0 || replay.json["state"] != "complete" {
		t.Fatalf("The exact retry lost its result: %v", replay.json)
	}
	if conflict := runMC(t, env, operatorTransportBody(t, "pause-request", []string{"project", "pause", "another"}, ""), "__operator-submit"); conflict.code != 1 {
		t.Fatalf("A changed retry reused the request: %v", conflict.json)
	}
	if again := runMC(t, spineEnv(spine), "", "__operator-execute", "pause-request"); again.code != 0 {
		t.Fatal(again.stderr)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM operator_requests WHERE id='pause-request'`); got != 1 {
		t.Fatalf("The retry created %d operations.", got)
	}
	if _, err := db.Exec(`UPDATE collie_sessions SET status='ended' WHERE id=?`, session); err != nil {
		t.Fatal(err)
	}
	if denied := runMC(t, env, operatorTransportBody(t, "ended-request", []string{"project", "pause", "ws-test"}, ""), "__operator-submit"); denied.code != 1 {
		t.Fatalf("An ended session submitted work: %v", denied.json)
	}
	if after := runMC(t, env, "", "operation", "get", "pause-request"); after.code != 0 {
		t.Fatalf("An ended session lost its receipt: %s", after.stderr)
	}
}

func TestOperatorBridgeCLIKeepsUpdateRefusalBeforeEffect(t *testing.T) {
	spine := initSpine(t)
	started := runMC(t, spineEnv(spine), "", "collie", "start", "--from", "cli:operator-guard", "--allow", "project.pause")
	if started.code != 0 {
		t.Fatal(started.stderr)
	}
	env := collieJSONEnv(t, spine, started.json["session_id"].(string), []string{"project.pause"})
	submitted := runMC(t, env, operatorTransportBody(t, "guarded", []string{"project", "pause", "ws-test"}, ""), "__operator-submit")
	if submitted.code != 0 {
		t.Fatal(submitted.stderr)
	}
	state := filepath.Join(filepath.Dir(spine), "state")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "install-maintenance.json"), []byte(`{"id":"incomplete-install"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if executed := runMC(t, spineEnv(spine), "", "__operator-execute", "guarded"); executed.code != 0 {
		t.Fatal(executed.stderr)
	}
	receipt := runMC(t, env, "", "operation", "get", "guarded")
	op := receipt.json["operation"].(map[string]any)
	result := op["result"].(map[string]any)
	if op["exit_code"] != float64(1) || result["error"].(map[string]any)["code"] != "update-incomplete" {
		t.Fatalf("The update guard lost its refusal: %v", op)
	}
	db := openDB(t, spine)
	if got := queryStr(t, db, `SELECT status FROM projects WHERE id='ws-test'`); got != "active" {
		t.Fatalf("The incomplete update admitted a change: %s", got)
	}
	host := runMC(t, spineEnv(spine), "", "project", "pause", "ws-test", "--request-id", "host-guarded")
	if host.code != 1 || !strings.Contains(host.stderr, "maintenance") {
		t.Fatalf("The external command bypassed the update: %v %s", host.json, host.stderr)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM operator_requests WHERE id='host-guarded'`); got != 0 {
		t.Fatal("A blocked external change created work.")
	}
}

func TestOperatorBridgeCLIRefusesOnlyPendingRequests(t *testing.T) {
	spine := initSpine(t)
	started := runMC(t, spineEnv(spine), "", "collie", "start", "--from", "cli:refusal-owner", "--allow", "project.pause")
	if started.code != 0 {
		t.Fatal(started.stderr)
	}
	env := collieJSONEnv(t, spine, started.json["session_id"].(string), []string{"project.pause"})
	for _, request := range []string{"pending-refusal", "active-refusal"} {
		result := runMC(t, env, operatorTransportBody(t, request, []string{"project", "pause", "ws-test"}, ""), "__operator-submit")
		if result.code != 0 {
			t.Fatal(result.stderr)
		}
	}
	claim := runMC(t, spineEnv(spine), `{"id":"active-refusal"}`, "__operator-claim")
	if claim.code != 0 || claim.json["claimed"] != true {
		t.Fatalf("The host did not claim its request: %v %s", claim.json, claim.stderr)
	}
	refusal := func(request string) string {
		body, _ := json.Marshal(map[string]any{"id": request, "exit_code": 2, "result": map[string]any{"error": map[string]any{"code": "environment", "message": "The helper was unavailable."}}})
		return string(body)
	}
	if denied := runMC(t, env, refusal("active-refusal"), "__operator-refuse"); denied.code != 1 {
		t.Fatalf("Collie acquired refusal authority: %s", denied.stdout)
	}
	for _, item := range []struct{ request, state string }{{"pending-refusal", "complete"}, {"active-refusal", "active"}} {
		result := runMC(t, spineEnv(spine), refusal(item.request), "__operator-refuse")
		if result.code != 0 || result.json["state"] != item.state {
			t.Fatalf("The refusal changed the wrong state: %v %s", result.json, result.stderr)
		}
	}
	if again := runMC(t, spineEnv(spine), "", "__operator-execute", "active-refusal"); again.code != 0 || again.json["operation"].(map[string]any)["state"] != "active" {
		t.Fatalf("A second executor took the active request: %v %s", again.json, again.stderr)
	}
	db := openDB(t, spine)
	if state := queryStr(t, db, `SELECT status FROM projects WHERE id='ws-test'`); state != "active" {
		t.Fatal("A refused claimant executed an effect.")
	}
}

func TestOperatorBridgeCLIRejectsAbsentResourceTargets(t *testing.T) {
	spine := initSpine(t)
	missing := runMC(t, spineEnv(spine), "", "task", "block", "50000", "--reason", "The operator requested a pause.", "--request-id", "absent-task")
	if missing.code != 1 || missing.json["error"].(map[string]any)["code"] != "not-found" {
		t.Fatalf("The absent task lost its domain refusal: %v %s", missing.json, missing.stderr)
	}
	missing = runMC(t, spineEnv(spine), `{"run":"absent-run","reason":"The instructions were corrected."}`, "project", "role-unblock", "--request-id", "absent-run")
	if missing.code != 1 || !strings.Contains(missing.stderr, "no active role block") {
		t.Fatalf("The absent run lost its domain refusal: %v %s", missing.json, missing.stderr)
	}
	db := openDB(t, spine)
	if count := queryInt(t, db, `SELECT COUNT(*) FROM operator_requests WHERE id IN ('absent-task','absent-run')`); count != 0 {
		t.Fatal("An absent resource acquired an executable request.")
	}
}
