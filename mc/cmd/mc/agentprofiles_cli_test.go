package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	operatorconfig "mc/config"
)

func TestAgentProfileCommandsRequireHostScope(t *testing.T) {
	home, spine, _ := agentSurfaceHome(t)
	env := append(runJSONEnv(t, spine, "profile-agent", "pipeline", "worker"), "MC_HOME="+home)
	for _, action := range []string{"list", "models", "save", "connection-save", "runtime-catalog", "test-runtime-authorities", "grant-rotate", "operation-start", "operation-status", "operation-cancel", "operation-run"} {
		result := runMC(t, env, `{"token":"scope-secret"}`, "agent-profile", action)
		if result.code != 1 || !strings.Contains(result.stderr, "host scope only") {
			t.Fatalf("Action %s did not reject agent scope: %d %s", action, result.code, result.stderr)
		}
		if strings.Contains(result.stdout+result.stderr, "scope-secret") {
			t.Fatal("The scope refusal disclosed input")
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("A refused agent command created the operator home")
	}
}

func TestAgentProfileCommandProtectsSecretInputAndRequiresTestProof(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := operatorconfig.WriteAtomic(filepath.Join(home, "config.toml"), operatorconfig.Default()); err != nil {
		t.Fatal(err)
	}
	env := []string{"MC_HOME=" + home}
	list := runMC(t, env, "", "agent-profile", "list")
	if list.code != 0 {
		t.Fatalf("List: %s", list.stderr)
	}
	token := "command-private-token"
	forbidden, _ := json.Marshal(map[string]any{"submission_id": "command-forbidden", "revision": list.json["revision"], "name": "Unsupported connection", "kind": "static-env", "base_url": "https://example.invalid", "token": token})
	refused := runMC(t, env, string(forbidden), "agent-profile", "connection-save")
	if refused.code == 0 || strings.Contains(refused.stdout+refused.stderr, token) {
		t.Fatal("The command accepted or disclosed a static credential")
	}
	body, _ := json.Marshal(map[string]any{"submission_id": "command-connection", "revision": list.json["revision"], "name": "Second account", "template_id": "claude"})
	saved := runMC(t, env, string(body), "agent-profile", "connection-save")
	if saved.code != 0 {
		t.Fatalf("Save: %s", saved.stderr)
	}
	for _, input := range []string{`{"token":"` + token + `","unknown":true}`, `{"token":"` + token + `"} {}`, `{"kind":"claude-oauth","token":""}`, `{"kind":"codex-oauth","base_url":null}`, `{"kind":"claude-oauth","Token":null}`} {
		rejected := runMC(t, env, input, "agent-profile", "connection-save")
		if rejected.code != 2 {
			t.Fatalf("Expected invalid JSON request rejection: %d %s", rejected.code, rejected.stderr)
		}
		if strings.Contains(rejected.stdout+rejected.stderr, token) {
			t.Fatal("The invalid request response disclosed its token")
		}
	}
	for _, input := range []string{
		`{"kind":"model-test","profile":{"base_url":""}}`,
		`{"kind":"model-test","Profile":{"BASE_URL":null}}`,
		`{"kind":"model-test","profile":{"base_url":""},"profile":{"connection_id":"claude","model":"fixture-model"}}`,
		`{"kind":"model-test","profile":{"BASE_URL":null},"profile":{"connection_id":"claude","model":"fixture-model"}}`,
		`{"kind":"model-test","Profile":{"Base_URL":"https://example.invalid"},"Profile":{}}`,
	} {
		rejected := runMC(t, env, input, "agent-profile", "operation-start")
		if rejected.code != 2 {
			t.Fatalf("The model test accepted a forbidden field: %d %s", rejected.code, rejected.stderr)
		}
	}
	profileBody, _ := json.Marshal(map[string]any{"submission_id": "command-profile", "revision": saved.json["revision"], "name": "unverified_profile", "connection_id": saved.json["connection_id"], "model": "manual-model", "enabled": true, "test_id": "invented-proof"})
	rejected := runMC(t, env, string(profileBody), "agent-profile", "save")
	if rejected.code != 1 {
		t.Fatalf("An unverified profile passed the command: %d %s", rejected.code, rejected.stderr)
	}
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Runtimes.EnabledBindings) != 0 {
		t.Fatal("The command enabled an unverified profile")
	}
	logBody, err := os.ReadFile(filepath.Join(home, "logs", "mc.ndjson"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(logBody), token) {
		t.Fatal("The command log contains a token")
	}
}
