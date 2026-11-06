package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc/deployment"
)

func TestCollieLaunchCheckUsesTransactionAndPreservesRestrictions(t *testing.T) {
	spine := initSpine(t)
	env := spineEnv(spine)
	started := runMC(t, env, "", "collie", "start", "--from", "dashboard:launch-check")
	if started.code != 0 {
		t.Fatal(started.stderr)
	}
	session := started.json["session_id"].(string)
	const launch = "0123456789abcdef"
	db := openDB(t, spine)
	defer db.Close()
	if _, err := db.Exec(`UPDATE collie_sessions SET current_launch_id=?,current_launch_mode='fresh' WHERE id=?`, launch, session); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO connection_health(connection_id,operation,authorization_id,state) VALUES('test-connection','connection_access','test-authorization','ready')`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, launch, authorization, state, refusal string
	}{
		{"current launch", launch, "test-authorization", "ready", ""},
		{"stale launch", "fedcba9876543210", "test-authorization", "ready", "The Collie launch changed."},
		{"stale authorization", launch, "old-authorization", "ready", "The connection authorization changed."},
		{"stopped connection", launch, "test-authorization", "stopped", "The connection requires attention."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec(`UPDATE connection_health SET state=? WHERE connection_id='test-connection'`, tc.state); err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(map[string]string{
				"session": session, "launch": tc.launch, "connection": "test-connection",
				"authorization": tc.authorization, "profile": "test-profile", "profile_revision": "test-revision",
			})
			if err != nil {
				t.Fatal(err)
			}
			result := runMC(t, env, string(body), "__system-state", "collie-launch-check")
			if tc.refusal == "" {
				if result.code != 0 || result.json["allowed"] != true {
					t.Fatalf("Launch check failed: %s %s", result.stdout, result.stderr)
				}
			} else if result.code == 0 || !strings.Contains(result.stdout+result.stderr, tc.refusal) {
				t.Fatalf("Expected %q: %s %s", tc.refusal, result.stdout, result.stderr)
			}
			if got := queryStr(t, db, `SELECT status FROM collie_sessions WHERE id=?`, session); got != "active" {
				t.Fatalf("The check changed the session status to %q.", got)
			}
		})
	}
}

func TestFailedModelOperationPreservesTrustedHealthAndItsFailureMessage(t *testing.T) {
	spine := initSpine(t)
	env := spineEnv(spine)
	home := filepath.Dir(spine)
	root := filepath.Join(home, "refresh-grants")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	authorization := strings.Repeat("a", 64)
	metadata, _ := json.Marshal(map[string]any{"version": 1, "credential": "claude", "channel": "claude", "type": "account", "authorization_id": authorization, "credential_ref": strings.Repeat("b", 64)})
	if err := os.WriteFile(filepath.Join(root, "claude.json"), metadata, 0600); err != nil {
		t.Fatal(err)
	}
	profile := deployment.AgentProfileRequest{SubmissionID: "failed-profile-proof", Name: "Failed test profile", ConnectionID: "claude", Model: "fixture-model"}
	_, binding, err := deployment.ResolveAgentProfile(home, profile)
	if err != nil {
		t.Fatal(err)
	}
	const operation = "failed-model-health-proof"
	body, _ := json.Marshal(deployment.AgentProfileOperationRequest{OperationID: operation, Kind: "model_test", ConnectionID: "claude", Profile: profile, ConfirmSpend: true})
	started := runMC(t, env, string(body), "agent-profile", "operation-start")
	if started.code != 0 {
		t.Fatalf("Start model operation: %s %s", started.stdout, started.stderr)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		status, err := deployment.GetAgentProfileOperation(home, operation)
		if err != nil {
			t.Fatal(err)
		}
		if status.Status == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("The model test did not reject the absent runner release")
		}
		time.Sleep(20 * time.Millisecond)
	}
	db := openDB(t, spine)
	defer db.Close()
	for _, state := range []string{"ready", "retry_wait"} {
		if _, err := db.Exec(`INSERT INTO connection_health(connection_id,operation,profile_id,authorization_id,profile_revision,state,verified_at) VALUES('claude','model_access',?,?,'trusted-profile',?,datetime('now')) ON CONFLICT DO UPDATE SET state=excluded.state`, binding, authorization, state); err != nil {
			t.Fatal(err)
		}
		before := queryInt(t, db, `SELECT COUNT(*) FROM diagnostic_events`)
		result := runMC(t, env, `{"operation_id":"`+operation+`"}`, "agent-profile", "operation-status")
		if result.code != 0 || result.json["status"] != "failed" || result.json["message"] == "" {
			t.Fatalf("The failed test lost its operation result: %s %s", result.stdout, result.stderr)
		}
		if got := queryStr(t, db, `SELECT state FROM connection_health WHERE connection_id='claude' AND profile_id=?`, binding); got != state {
			t.Fatalf("The failed operation changed trusted health from %s to %s", state, got)
		}
		if after := queryInt(t, db, `SELECT COUNT(*) FROM diagnostic_events`); after != before {
			t.Fatal("The failed operation added generic failure evidence")
		}
	}
}

func TestDoctorPreservesConnectionRestrictionsAndProjectInfrastructureAllowance(t *testing.T) {
	spine := initSpine(t)
	env := spineEnv(spine)
	check := runMC(t, env, `{"connection_id":"claude","check_id":"11111111111111111111111111111111"}`, "connection", "check-start")
	if check.code != 0 {
		t.Fatal(check.stderr)
	}
	body, _ := json.Marshal(map[string]any{
		"connection_id": "claude", "authorization_id": check.json["authorization_id"], "operation": "token_refresh",
		"check_id": "11111111111111111111111111111111", "event_id": "22222222222222222222222222222222",
		"outcome": "failed", "observed_at": time.Now().UTC().Format(time.RFC3339Nano), "http_status": 400, "provider_error_code": "invalid_grant",
		"condition": "secret-body", "evidence": "secret-body",
	})
	failed := runMC(t, env, string(body), "connection", "observe")
	if failed.code != 0 {
		t.Fatal(failed.stderr)
	}
	db := openDB(t, spine)
	defer db.Close()
	if _, err := db.Exec(`UPDATE projects SET infrastructure_failure_count=3 WHERE id='ws-test'`); err != nil {
		t.Fatal(err)
	}
	before := queryInt(t, db, `SELECT COUNT(*) FROM diagnostic_events`)
	health := filepath.Join(filepath.Dir(spine), "health")
	if err := os.MkdirAll(health, 0700); err != nil {
		t.Fatal(err)
	}
	availability, _ := json.Marshal(map[string]any{"observed_at": time.Now().UTC().Format(time.RFC3339Nano), "can_commit": true})
	if err := os.WriteFile(filepath.Join(health, "resident-availability.json"), availability, 0600); err != nil {
		t.Fatal(err)
	}
	status := runMC(t, env, "", "system", "status")
	doctor := runMC(t, env, "", "doctor")
	if status.code != 0 || status.json["complete"] != true || doctor.code != 0 || doctor.json["ok"] != false {
		t.Fatalf("Status=%v doctor=%v", status.json, doctor.json)
	}
	for _, result := range []mcResult{status, doctor} {
		if !strings.Contains(result.stdout, "invalid_grant") || !strings.Contains(result.stdout, "The Project infrastructure allowance is exhausted.") || strings.Contains(result.stdout, "secret-body") {
			t.Fatalf("Incorrect diagnostic output: %s", result.stdout)
		}
	}
	if doctor.json["completed_at"] == "" {
		t.Fatal("The inspection omitted its time.")
	}
	if queryInt(t, db, `SELECT COUNT(*) FROM diagnostic_events`) != before || queryInt(t, db, `SELECT infrastructure_failure_count FROM projects WHERE id='ws-test'`) != 3 {
		t.Fatal("Inspection changed operational state.")
	}
	for _, value := range doctor.json["findings"].([]any) {
		finding := value.(map[string]any)
		if finding["check"] == "runtime-auth" && finding["status"] != "fail" {
			t.Fatalf("Known rejection became an unperformed test: %v", finding)
		}
	}
}

func TestConnectionProxyObservationUsesHostCredentialReferences(t *testing.T) {
	spine := initSpine(t)
	env := spineEnv(spine)
	home := filepath.Dir(spine)
	root := filepath.Join(home, "refresh-grants")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	authorization, reference := strings.Repeat("a", 64), strings.Repeat("b", 64)
	metadata, _ := json.Marshal(map[string]any{"version": 1, "credential": "proxy-test", "channel": "claude", "type": "api_key", "authorization_id": authorization, "credential_ref": reference})
	if err := os.WriteFile(filepath.Join(root, "proxy-test.json"), metadata, 0600); err != nil {
		t.Fatal(err)
	}
	db := openDB(t, spine)
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO connection_health(connection_id,operation,authorization_id,state) VALUES('proxy-test','connection_access',?,'ready')`, authorization); err != nil {
		t.Fatal(err)
	}
	for _, stale := range []bool{true, false} {
		request, used := "11111111111111111111111111111111", strings.Repeat("c", 64)
		if !stale {
			request, used = "22222222222222222222222222222222", reference
		}
		body, _ := json.Marshal(map[string]any{
			"request_id": request, "connection": "proxy-test", "authorization_revision": authorization, "credential_ref": used,
			"current_authorization_revision": authorization, "current_credential_ref": used,
			"operation": "model", "origin": "provider", "outcome": "response_complete", "provider_sent": true,
			"http_status": 401, "observed_at": time.Now().UTC().Format(time.RFC3339Nano), "duration_ms": 1,
		})
		result := runMC(t, env, string(body), "connection", "proxy-observe")
		if result.code != 0 {
			t.Fatalf("Proxy observation failed: %s %s", result.stdout, result.stderr)
		}
		want := "ready"
		if !stale {
			want = "stopped"
		}
		if got := queryStr(t, db, `SELECT state FROM connection_health WHERE connection_id='proxy-test'`); got != want {
			t.Fatalf("The wrapper trusted the caller's current reference: state=%s want=%s", got, want)
		}
	}
}

func TestConnectionFirstProxyRefusalUsesTheProtectedCredentialType(t *testing.T) {
	for _, kind := range []string{"api_key", "account"} {
		t.Run(kind, func(t *testing.T) {
			spine := initSpine(t)
			env := spineEnv(spine)
			root := filepath.Join(filepath.Dir(spine), "refresh-grants")
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			authorization, reference := strings.Repeat("a", 64), strings.Repeat("b", 64)
			metadata, _ := json.Marshal(map[string]any{"version": 1, "credential": "proxy-first", "channel": "claude", "type": kind, "authorization_id": authorization, "credential_ref": reference})
			if err := os.WriteFile(filepath.Join(root, "proxy-first.json"), metadata, 0600); err != nil {
				t.Fatal(err)
			}
			forged := "account"
			if kind == "account" {
				forged = "api_key"
			}
			body, _ := json.Marshal(map[string]any{
				"request_id": strings.Repeat("1", 32), "connection": "proxy-first", "authorization_revision": authorization, "credential_ref": reference,
				"current_credential_type": forged, "operation": "model", "origin": "provider", "outcome": "response_complete", "provider_sent": true,
				"http_status": 401, "observed_at": time.Now().UTC().Format(time.RFC3339Nano), "duration_ms": 1,
			})
			result := runMC(t, env, string(body), "connection", "proxy-observe")
			if result.code != 0 || result.json["applied"] != true {
				t.Fatalf("The first refusal lost its diagnostic: %s %s", result.stdout, result.stderr)
			}
			db := openDB(t, spine)
			defer db.Close()
			operation, state := "connection_access", "stopped"
			if kind == "account" {
				operation, state = "token_refresh", "unknown"
			}
			if got := queryStr(t, db, `SELECT operation||':'||state FROM connection_health WHERE connection_id='proxy-first'`); got != operation+":"+state {
				t.Fatalf("The wrapper used caller credential type: %s", got)
			}
		})
	}
}
