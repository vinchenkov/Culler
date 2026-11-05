package deployment

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderArtifactRetirementScrubsNativeCredentialEchoesAndKeepsContext(t *testing.T) {
	home := agentProfileTestHome(t)
	legacyCredentialGrant(t, home)
	old := "historical-access-token-123456789"
	unknown := "historical-refresh-token-987654321"
	auth := `{"tokens":{"access_token":"` + old + `","refresh_token":"` + unknown + `"}}`
	artifact := filepath.Join(home, "runs", "0123456789abcdef.codex-auth.json")
	if err := os.MkdirAll(filepath.Dir(artifact), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte(auth), 0600); err != nil {
		t.Fatal(err)
	}
	var unicode, percent strings.Builder
	for _, b := range []byte(old) {
		fmt.Fprintf(&unicode, "\\u%04x", b)
		fmt.Fprintf(&percent, "%%%02X", b)
	}
	values := []string{auth, old, unknown, "synthetic-legacy-refresh", base64.StdEncoding.EncodeToString([]byte(old)),
		base64.RawURLEncoding.EncodeToString([]byte(auth)), unicode.String(), percent.String(),
		"sk-ant-oat01-historical-token-12345678901234567890",
		`{"access_token":"unknown-opaque-access-123456789"}`,
		"CLAUDE_CODE_OAUTH_TOKEN=unknown-environment-access-123456789"}
	var original []byte
	for index, value := range values {
		line, _ := json.Marshal(map[string]any{"type": "tool_result", "session_id": "native-context-id",
			"sequence": index, "output": value, "context": "Keep this conversation context."})
		original = append(original, append(line, '\n')...)
	}
	paths := []string{"sessions/h-example/native.jsonl", "environment-runs/setup-operation/session/native.jsonl",
		"agentsview/traces/codex/sessions/native.jsonl", "logs/resident.ndjson"}
	for _, path := range paths {
		path = filepath.Join(home, path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Fatal(err)
		}
	}
	envelope := []byte(`{"run_id":"0123456789abcdef","tier":"pipeline","harness":"codex","model_binding":"profile","runtime":{"base_url":"","channel":"codex","harness":"codex","id":"profile","model":"fixture"},"brief":"Keep this task brief.","skills":[],"pool_ids":[],"heartbeat_interval_s":30,"mode":"fresh","mounts":{"session":"/mc/session","workspace":"/workspace/source","allow_write":[]}}`)
	envelopePath := filepath.Join(home, "runs", "0123456789abcdef.json")
	if err := os.WriteFile(envelopePath, envelope, 0600); err != nil {
		t.Fatal(err)
	}
	if err := RetireProviderCredentialArtifacts(home, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		body, err := os.ReadFile(filepath.Join(home, path))
		if err != nil || len(body) != len(original) {
			t.Fatalf("The native byte count changed: %s: %v", path, err)
		}
		for _, value := range values {
			if bytes.Contains(body, []byte(value)) {
				t.Fatalf("The native record retains a credential echo: %s", path)
			}
		}
		for _, line := range bytes.Split(bytes.TrimSpace(body), []byte{'\n'}) {
			var record map[string]any
			if json.Unmarshal(line, &record) != nil || record["session_id"] != "native-context-id" || record["context"] != "Keep this conversation context." {
				t.Fatalf("The scrub changed native context or JSON validity: %s", path)
			}
		}
	}
	if body, err := os.ReadFile(envelopePath); err != nil || !bytes.Equal(body, envelope) {
		t.Fatal("The legacy launch envelope changed its public fields.")
	}
	if err := RetireProviderCredentialArtifacts(home, func() error { return nil }); err != nil {
		t.Fatal("The repeated scrub failed:", err)
	}
}

func TestProviderArtifactRetirementRefusesAContaminatedNativeLink(t *testing.T) {
	home := agentProfileTestHome(t)
	outside := filepath.Join(t.TempDir(), "native.jsonl")
	const secret = `{"access_token":"synthetic-outside-token"}`
	if err := os.WriteFile(outside, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "sessions", "h-example")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "native.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := RetireProviderCredentialArtifacts(home, func() error { return nil }); err == nil {
		t.Fatal("The linked native file permitted activation.")
	}
	if body, err := os.ReadFile(outside); err != nil || string(body) != secret {
		t.Fatal("The native scrub changed a file outside its retained directories.")
	}
}
