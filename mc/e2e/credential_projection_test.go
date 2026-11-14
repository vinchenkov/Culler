//go:build docker_e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	operatorconfig "mc/config"
)

// The real resident creates the Worker and its scoped host proxy authority.
// The synthetic Worker checks the container boundary before it submits its commit.
func TestCredentialProjectionDockerBoundary(t *testing.T) {
	for _, channel := range []string{"codex", "claude"} {
		t.Run(channel, func(t *testing.T) { providerAccessDockerBoundary(t, channel, false) })
	}
}

func TestCredentialProjectionFailClosedDockerBoundary(t *testing.T) {
	providerAccessDockerBoundary(t, "codex", true)
}

func providerAccessDockerBoundary(t *testing.T, channel string, invalidToken bool) {
	f := setup(t)
	binding, harness := "chatgpt", "codex"
	if channel == "claude" {
		binding, harness = "claude", "claude-sdk"
	}
	routing := "# Proxy fixture routing\n| role | harness | binding |\n| --- | --- | --- |\n"
	for _, role := range []string{"strategist", "arbiter", "worker", "verifier", "scribe", "packager", "collie"} {
		route := "fake | fake"
		if role == "worker" {
			route = harness + " | " + binding
		}
		routing += "| " + role + " | " + route + " |\n"
	}
	if err := os.WriteFile(filepath.Join(f.home, "routing.md"), []byte(routing), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := "synthetic-refresh-SENTINEL-" + channel
	config, err := operatorconfig.Load(filepath.Join(f.home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	source := config.Runtimes.Credentials[binding]
	grant := map[string]any{"credential": binding, "channel": channel, "refresh_token": marker,
		"token_url": source.TokenURL, "client_id": source.ClientID,
		"access_token": "synthetic-access-SENTINEL-" + channel, "expires_at_ms": time.Now().Add(time.Hour).UnixMilli()}
	if channel == "codex" {
		grant["account_id"] = "synthetic-account"
	} else {
		grant["scope"] = "user:inference"
	}
	path := filepath.Join(f.home, "refresh-grants", binding+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(grant)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	// These requests fail before host credential access. They contact no provider.
	probe := `import { existsSync, readFileSync } from "node:fs";
const fail = () => { throw new Error("The container provider boundary failed."); };
const path = process.env.MC_PROVIDER_ACCESS_FILE;
if (path !== "/mc/provider-access.json") fail();
const access = JSON.parse(readFileSync(path, "utf8"));
if (Object.keys(access).sort().join(",") !== "authMode,ca,harness,token,url,version") fail();
if (!/^culler_proxy_[A-Za-z0-9_-]{43}$/.test(access.token) || !access.url.startsWith("https://")) fail();
if (access.ca.includes("PRIVATE KEY") || !access.ca.includes("BEGIN CERTIFICATE")) fail();
for (const key of ["OPENAI_API_KEY", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "MC_CREDENTIAL_REFRESH_URL", "CODEX_REFRESH_TOKEN_URL_OVERRIDE", "CULLER_TEST_HOST_SECRET_DIR"]) {
  if (process.env[key]) fail();
}
for (const file of ["/mc/codex/auth.json", "/home/agent/.codex/auth.json", "/home/agent/.claude/.credentials.json"]) {
  if (existsSync(file)) fail();
}
const deniedToken = ` + fmt.Sprint(invalidToken) + `;
const token = deniedToken ? "culler_proxy_" + "x".repeat(43) : access.token;
const response = await fetch(access.url + (access.harness === "codex" ? "/responses" : "/v1/messages"), {
  method: "POST", headers: { authorization: "Bearer " + token, "content-type": "application/json" },
  body: JSON.stringify({ model: "outside-authorized-model", input: "synthetic", messages: [] }), tls: { ca: access.ca },
});
if (response.status !== (deniedToken ? 401 : 403)) fail();
const answer = await response.text();
if (answer.includes(access.token) || !response.headers.get("x-culler-request-id")) fail();
console.log("CULLER_CONTAINER_PROVIDER_BOUNDARY_OK");
`
	if err := os.WriteFile(filepath.Join(f.base, "behaviors", "provider-probe.ts"), []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	writeBehaviorFile(t, f, "worker-credential.json", behaviorSteps(
		execStep("bun /mc/behaviors/provider-probe.ts && bun /mc/behaviors/worker.ts"), succeedStep("completed")))
	setResidentWorkerBehavior(t, f, "/mc/behaviors/worker-credential.json", []string{harness + "/" + binding})
	result := f.mcOK("", "task", "add", "provider boundary task", "--project", project,
		"--description", "The Worker checks scoped provider access before it submits.")
	taskID := int64(result["task_id"].(float64))
	f.startResident()
	f.waitForTaskStatus(taskID, "worked", 120*time.Second)
	reference, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(reference), marker) || !strings.Contains(string(reference), "credential_ref") {
		t.Fatal("The host did not replace the legacy credential with a private store reference.")
	}
	if strings.Contains(f.residentLog(), marker) {
		t.Fatal("The resident log contains a provider credential.")
	}
	if err := filepath.WalkDir(f.home, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		body, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if strings.Contains(string(body), marker) {
			return fmt.Errorf("a host artifact contains a provider credential: %s", filepath.Base(path))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func mutateResidentConfig(t *testing.T, f *fixture, mutate func(map[string]any)) {
	t.Helper()
	cfgBytes, err := os.ReadFile(f.residentConfig())
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		t.Fatal(err)
	}
	mutate(cfg)
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.residentConfig(), out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func setResidentWorkerBehavior(t *testing.T, f *fixture, behaviorPath string, routes []string) {
	t.Helper()
	mutateResidentConfig(t, f, func(cfg map[string]any) {
		cfg["roleBehaviors"].(map[string]any)["worker"] = behaviorPath
		cfg["agentRunnerRoutes"] = routes
	})
}
