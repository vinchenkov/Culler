//go:build docker_e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	operatorconfig "mc/config"
)

// The production resident and Homie process use a controlled provider transport.
// The helper retains the spine in its actual named volume.
func TestHomieNativeProxyDockerBoundary(t *testing.T) {
	for _, harness := range []string{"codex", "claude-sdk"} {
		t.Run(harness, func(t *testing.T) {
			f := setup(t)
			f.mcOK("", "pause")
			binding, connection := "chatgpt", "codex-api-key"
			if harness == "claude-sdk" {
				binding, connection = "claude", "claude-api-key"
			}
			key := "synthetic-homie-key-" + harness
			catalog := f.mcOK("", "agent-profile", "list")
			request, _ := json.Marshal(map[string]any{"id": connection, "name": connection, "template_id": connection,
				"api_key": key, "revision": catalog["revision"], "submission_id": fmt.Sprintf("homie-key-%d", time.Now().UnixNano())})
			f.mcOK(string(request), "agent-profile", "connection-save")
			if _, err := operatorconfig.UpdateAtomic(filepath.Join(f.home, "config.toml"), func(cfg *operatorconfig.Config) error {
				profile := cfg.Runtimes.Bindings[binding]
				profile.Credential, profile.Model = connection, "fixture-model"
				cfg.Runtimes.Bindings[binding] = profile
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			route := "# Native Homie fixture\n| role | harness | binding |\n| --- | --- | --- |\n"
			for _, role := range []string{"strategist", "arbiter", "worker", "verifier", "scribe", "packager", "homie"} {
				selected := "fake | fake"
				if role == "homie" {
					selected = harness + " | " + binding
				}
				route += "| " + role + " | " + selected + " |\n"
			}
			if err := os.WriteFile(filepath.Join(f.home, "routing.md"), []byte(route), 0600); err != nil {
				t.Fatal(err)
			}
			checkID := fmt.Sprintf("%032x", time.Now().UnixNano())
			check, _ := json.Marshal(map[string]any{"connection_id": connection, "check_id": checkID})
			reserved := f.mcOK(string(check), "connection", "check-start")
			if reserved["allowed"] != true {
				t.Fatalf("The synthetic credential check was refused: %v", reserved)
			}
			f.mcOK(fmt.Sprintf(`{"connection_id":%q}`, connection), "agent-profile", "credential-read")
			ready, _ := json.Marshal(map[string]any{"connection_id": connection, "check_id": checkID,
				"authorization_id": reserved["authorization_id"], "operation": reserved["operation"],
				"event_id": fmt.Sprintf("%032x", time.Now().UnixNano()), "outcome": "ready", "observed_at": time.Now().UTC().Format(time.RFC3339Nano)})
			f.mcOK(string(ready), "connection", "observe")
			control := filepath.Join(f.base, "native-provider")
			if err := os.Mkdir(control, 0700); err != nil {
				t.Fatal(err)
			}
			text := "The native Homie reply arrived before the provider completed."
			body, _ := json.Marshal(map[string]string{"harness": harness, "key": key, "model": "fixture-model", "text": text})
			if err := os.WriteFile(filepath.Join(control, "config.json"), body, 0600); err != nil {
				t.Fatal(err)
			}
			start := f.mcOK("", "homie", "start", "--from", "dashboard:native-proxy")
			session, _ := start["session_id"].(string)
			if session == "" {
				t.Fatal("The actual Homie start returned no session.")
			}
			f.mcOK("", "homie", "send", session, "--from", "dashboard:native-proxy", "--body", "Return the controlled reply without tools.")
			startNativeProxyResident(t, f, control)
			t.Cleanup(func() {
				if !t.Failed() {
					return
				}
				output, _ := exec.Command("docker", "logs", "--tail", "40", "mc-homie-"+session).CombinedOutput()
				t.Logf("Homie process evidence:\n%s", strings.ReplaceAll(string(output), key, "[redacted]"))
			})
			f.waitFor(90*time.Second, "the native Homie draft before provider completion", func() (bool, string) {
				history := f.mcOK("", "homie", "history", session)
				messages, _ := history["messages"].([]any)
				for _, raw := range messages {
					message := raw.(map[string]any)
					if message["direction"] == "reply" {
						t.Fatal("The Homie replied before the provider released its terminal event.")
					}
					if message["draft"] == text {
						return true, ""
					}
				}
				return false, "The actual Homie history has no native draft yet."
			})
			if _, err := os.Stat(filepath.Join(control, "stream-complete")); !os.IsNotExist(err) {
				t.Fatal("The controlled provider completed before the draft assertion.")
			}
			requests, err := os.ReadFile(filepath.Join(control, "requests.jsonl"))
			if err != nil || len(strings.Fields(string(requests))) != 1 {
				t.Fatalf("The native Homie repeated its provider request: %v", err)
			}
			inspection := f.dockerOutput("inspect", "mc-homie-"+session)
			if strings.Contains(inspection, key) || !strings.Contains(inspection, "provider-access.json") {
				t.Fatal("The actual Homie container lost the provider credential boundary.")
			}
			if err := os.WriteFile(filepath.Join(control, "release"), []byte("release"), 0600); err != nil {
				t.Fatal(err)
			}
			f.waitFor(30*time.Second, "the durable native Homie reply", func() (bool, string) {
				history := f.mcOK("", "homie", "history", session)
				messages, _ := history["messages"].([]any)
				for _, raw := range messages {
					message := raw.(map[string]any)
					if message["direction"] == "reply" && message["body"] == text {
						return true, ""
					}
				}
				return false, fmt.Sprintf("The Homie has not saved its terminal reply: %v", history)
			})
			requests, err = os.ReadFile(filepath.Join(control, "requests.jsonl"))
			if err != nil || len(strings.Fields(string(requests))) != 1 || strings.Contains(f.residentLog(), key) {
				t.Fatal("The completed Homie repeated a provider request or exposed a provider credential.")
			}
			listed := f.mcOK("", "homie", "list")
			rows := listed["sessions"].([]any)
			found := false
			var trace string
			for _, raw := range rows {
				row := raw.(map[string]any)
				if row["id"] == session {
					found = row["native_session_ref"] != nil && row["native_session_ref"] != "" && row["trace_filename"] != nil && row["trace_filename"] != ""
					trace, _ = row["trace_filename"].(string)
				}
			}
			if !found {
				t.Fatal("The actual Homie process did not retain its native session identity.")
			}
			for _, path := range []string{filepath.Join(f.home, "sessions", session, trace),
				filepath.Join(f.home, "logs", "runs", session, "runner.ndjson")} {
				data, err := os.ReadFile(path)
				if err != nil || strings.Contains(string(data), key) {
					t.Fatalf("The native Homie evidence is absent or contains its provider credential: %v", err)
				}
			}
			f.mcOK("", "homie", "end", session, "--reason", "The controlled native proxy check completed.")
		})
	}
}

func startNativeProxyResident(t *testing.T, f *fixture, control string) {
	t.Helper()
	file, err := os.Create(filepath.Join(f.base, "resident.log"))
	if err != nil {
		t.Fatal(err)
	}
	f.resLog = file
	repo := mustAbs(t, "../..")
	cmd := exec.Command("mise", "exec", "--", "bun", "--preload", filepath.Join(repo, "tests/provider-proxy/homie-resident-preload.ts"),
		"src/main.ts", "--config", filepath.Join(f.base, "resident.json"))
	cmd.Dir = filepath.Join(repo, "resident")
	cmd.Env = append(append([]string{}, f.env...), "CULLER_HOMIE_PROXY_FIXTURE="+control)
	cmd.Stdout, cmd.Stderr = file, file
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	f.resProc = cmd
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(control, "release"), []byte("release"), 0600)
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(35 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		_ = file.Close()
		if t.Failed() {
			t.Logf("Resident evidence:\n%s", tail(residentDecisions(f.residentLog()), 12000))
		}
	})
	f.waitFor(90*time.Second, "the production resident with its controlled provider transport", func() (bool, string) {
		return strings.Contains(f.residentLog(), "started: tick every"), "The resident did not start its timer."
	})
}
