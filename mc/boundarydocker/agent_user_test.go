//go:build docker_boundary

package boundarydocker

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	operatorconfig "mc/config"
	"mc/substrate"
	"mc/verbs"
	"mc/wikitest"
)

// These checks run the pinned Claude SDK through the shipped runner and
// adapter in the production image, with the container profile of every agent
// run: uid 10002, no capabilities, and the spine named volume. A scripted
// local endpoint supplies each model turn, so no provider request occurs. The
// spine is a disposable named volume, and the credential is synthetic.

const (
	agentProject   = "ws-agent"
	agentToken     = "culler_proxy_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	agentEndpoint  = "https://127.0.0.1:18080"
	agentBinding   = "fixture-static"
	agentTitle     = "Quote \" and 'single' — é 漢字 🚀 $(touch /tmp/owned) `id` $HOME ; | && > <"
	agentDetail    = "Line one.\nLine two with $(id) and `uname`.\n\tTabbed line.\n"
	agentDelimiter = "JSON"
)

const agentRouting = `# Culler routing

| role | harness | binding |
| --- | --- | --- |
| strategist | fake | fake |
| arbiter | fake | fake |
| worker | fake | fake |
| verifier | fake | fake |
| packager | fake | fake |
| collie | fake | fake |
`

// agentEndpointScript serves the scripted turns, runs the shipped runner, and
// writes the observed tool results to /mc/logs/fixture.json.
const agentEndpointScript = `
const steps = JSON.parse(await Bun.file("/fixture/steps.json").text());
const access = JSON.parse(await Bun.file("/mc/provider-access.json").text());
const results = {};
let requests = 0;
let unauthorized = 0;
function event(type, value) { return "event: " + type + "\ndata: " + JSON.stringify({ type, ...value }) + "\n\n"; }
function reply(id, block, delta, stop) {
  return event("message_start", { message: { id, type: "message", role: "assistant", content: [], model: "fixture-model", stop_reason: null, stop_sequence: null, usage: { input_tokens: 20, output_tokens: 0 } } })
    + event("content_block_start", { index: 0, content_block: block })
    + event("content_block_delta", { index: 0, delta })
    + event("content_block_stop", { index: 0 })
    + event("message_delta", { delta: { stop_reason: stop, stop_sequence: null }, usage: { output_tokens: 20 } })
    + event("message_stop", {});
}
const server = Bun.serve({ hostname: "127.0.0.1", port: 18080,
  tls: { cert: await Bun.file("/fixture/server.pem").text(), key: await Bun.file("/fixture/server.key").text() }, async fetch(request) {
  if (!new URL(request.url).pathname.endsWith("/v1/messages")) return new Response("not found", { status: 404 });
  requests++;
  if (request.headers.get("authorization") !== "Bearer " + access.token || request.headers.has("x-api-key")) unauthorized++;
  const body = await request.json();
  const hasBash = Array.isArray(body.tools) && body.tools.some((tool) => tool.name === "Bash");
  for (const message of body.messages) {
    if (!Array.isArray(message.content)) continue;
    for (const block of message.content) {
      if (block.type !== "tool_result") continue;
      const content = Array.isArray(block.content) ? block.content.map((part) => part.text ?? "").join("") : String(block.content ?? "");
      results[block.tool_use_id] = { is_error: block.is_error ?? false, content };
    }
  }
  const done = body.messages.filter((m) => m.role === "assistant" && Array.isArray(m.content) && m.content.some((b) => b.type === "tool_use")).length;
  const headers = { "content-type": "text/event-stream" };
  if (!hasBash || done >= steps.length) {
    return new Response(reply("msg_" + requests, { type: "text", text: "" }, { type: "text_delta", text: "FIXTURE_DONE" }, "end_turn"), { headers });
  }
  const input = { command: steps[done], description: "step " + done };
  return new Response(reply("msg_" + requests, { type: "tool_use", id: "step_" + done, name: "Bash", input: {} },
    { type: "input_json_delta", partial_json: JSON.stringify(input) }, "tool_use"), { headers });
} });
const runner = Bun.spawn(["bun", "/app/src/agent-runner/main.ts"], { stdin: "ignore", stdout: "pipe", stderr: "pipe" });
const [, stderr, code] = await Promise.all([new Response(runner.stdout).text(), new Response(runner.stderr).text(), runner.exited]);
server.stop(true);
const sdk = JSON.parse(await Bun.file("/app/node_modules/@anthropic-ai/claude-agent-sdk/package.json").text()).version;
await Bun.write("/mc/logs/fixture.json", JSON.stringify({ code, requests, unauthorized, results, sdk, stderr: stderr.slice(-4000) }));
`

type agentFixture struct {
	t      *testing.T
	mc     string
	home   string
	ws     string
	volume string
}

type agentResult struct {
	Code         int                       `json:"code"`
	Requests     int                       `json:"requests"`
	Unauthorized int                       `json:"unauthorized"`
	Results      map[string]agentToolReply `json:"results"`
	SDK          string                    `json:"sdk"`
	Stderr       string                    `json:"stderr"`
}

type agentToolReply struct {
	IsError bool   `json:"is_error"`
	Content string `json:"content"`
}

func syntheticGatewayCertificate(t *testing.T) (string, []byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Synthetic gateway CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Synthetic gateway"},
		NotBefore: root.NotBefore, NotAfter: root.NotAfter, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

func (r agentResult) step(t *testing.T, index int) string {
	t.Helper()
	reply, ok := r.Results[fmt.Sprintf("step_%d", index)]
	if !ok {
		t.Fatalf("step %d has no tool result; the run ended early: %+v", index, r)
	}
	return reply.Content
}

func newAgentFixture(t *testing.T) *agentFixture {
	t.Helper()
	requireProdImage(t)
	f := &agentFixture{t: t}
	f.mc = filepath.Join(sharedTempDir(t), "mc")
	build := exec.Command("mise", "exec", "--", "go", "build", "-tags", "test_fake_routing", "-o", f.mc, "./cmd/mc")
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the fixture mc: %v\n%s", err, out)
	}
	f.home = sharedTempDir(t)
	f.ws = sharedTempDir(t)
	if err := os.Chmod(f.home, 0o700); err != nil {
		t.Fatal(err)
	}
	f.workspace()
	init := f.host("__init", "--spine", f.spine(), "--project", agentProject, "--workspace-root", f.ws)
	f.currentWiki()
	f.write("routing.md", agentRouting)
	if _, err := operatorconfig.Ensure(filepath.Join(f.home, "config.toml")); err != nil {
		t.Fatal(err)
	}
	if _, err := verbs.EnsureMountAllowlist(f.home); err != nil {
		t.Fatal(err)
	}
	if _, err := verbs.EnsureProjectArtifactGrant(f.home, agentProject, f.ws); err != nil {
		t.Fatal(err)
	}
	if _, _, err := verbs.EnsureOnboardWorkspaceLayout([]verbs.OnboardWorkspaceRoot{{ID: agentProject, Root: f.ws}}, os.Getuid()); err != nil {
		t.Fatal(err)
	}
	f.write("deployment.uuid", init["deployment_uuid"].(string)+"\n")
	f.volume = fmt.Sprintf("mc-agent-spine-%d", time.Now().UnixNano())
	f.docker("volume", "create", f.volume)
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", "-f", f.volume).Run() })
	return f
}

func (f *agentFixture) spine() string { return filepath.Join(f.home, "spine.db") }

func (f *agentFixture) write(name, body string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.home, name), []byte(body), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *agentFixture) workspace() {
	f.t.Helper()
	files := map[string]string{
		"README.md":                  "# Fixture\n",
		"openwiki/index.md":          "---\ntitle: Fixture\ntype: Index\n---\n# Fixture\n",
		"openwiki/.last-update.json": `{"status":"complete"}`,
	}
	for name, body := range files {
		path := filepath.Join(f.ws, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q", "-b", "culler"}, {"add", "-A"}, {"commit", "-q", "-m", "Start the fixture"}} {
		cmd := exec.Command("git", append([]string{"-C", f.ws, "-c", "user.name=Fixture",
			"-c", "user.email=fixture@example.test", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			f.t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(f.ws, "VISION.md"), []byte("# Vision\nMake this project useful.\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// currentWiki records a current Project wiki at the commit of culler. Each tick
// then holds only the action of its agent run.
func (f *agentFixture) currentWiki() {
	f.t.Helper()
	commit, err := exec.Command("git", "-C", f.ws, "rev-parse", "culler").Output()
	if err != nil {
		f.t.Fatalf("read the commit of culler: %v", err)
	}
	db, err := substrate.Open(f.spine())
	if err != nil {
		f.t.Fatal(err)
	}
	defer db.Close()
	projectImage := os.Getenv("MC_BOUNDARY_PROJECT_IMAGE")
	if projectImage == "" {
		projectImage = "mc-project"
	}
	base := strings.TrimSpace(f.docker("image", "inspect", "--format", "{{.Id}}", projectImage))
	configuration := verbs.EnvironmentConfiguration{BaseDigest: base}
	if err := verbs.ValidateEnvironmentConfiguration(&configuration); err != nil {
		f.t.Fatal(err)
	}
	body, err := json.Marshal(configuration)
	if err != nil {
		f.t.Fatal(err)
	}
	revision := "fixture-environment-" + agentProject
	if _, err = db.Exec(`INSERT INTO environment_revisions(id,project_id,editor,configuration,inputs_digest) VALUES(?,?,'fixture',?,?)`, revision, agentProject, string(body), "sha256:"+strings.Repeat("b", 64)); err != nil {
		f.t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO project_environments(project_id,setup_id,selected_base_digest,selected_revision_id,selected_image_digest,draft_revision_id,readiness,phase,authorization) VALUES(?,?,?,?,?,?,'ready','complete','change')`, agentProject, "fixture-setup-"+agentProject, base, revision, base, revision); err != nil {
		f.t.Fatal(err)
	}
	_, recorded, err := wikitest.RecordCurrentWiki(context.Background(), db, agentProject, strings.TrimSpace(string(commit)), time.Now())
	if err != nil || !recorded {
		f.t.Fatalf("record the current Project wiki: recorded %v, %v", recorded, err)
	}
}

// host runs the fixture mc against the host copy of the spine.
func (f *agentFixture) host(args ...string) map[string]any {
	f.t.Helper()
	cmd := exec.Command(f.mc, args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + f.home, "MC_HOME=" + f.home,
		"MC_SPINE=" + f.spine(), "MC_RUN_JSON=" + filepath.Join(f.home, "absent-run.json")}
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if exit, ok := err.(*exec.ExitError); ok {
			stderr = string(exit.Stderr)
		}
		f.t.Fatalf("mc %v: %v\n%s\n%s", args, err, out, stderr)
	}
	var value map[string]any
	if err := json.Unmarshal(out, &value); err != nil {
		f.t.Fatalf("mc %v: %v\n%s", args, err, out)
	}
	return value
}

func (f *agentFixture) docker(args ...string) string {
	f.t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		f.t.Fatalf("docker %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// dispatch returns the one spawn action of the next tick.
func (f *agentFixture) dispatch(role string) map[string]any {
	f.t.Helper()
	tick := f.host("dispatch")
	actions, _ := tick["actions"].([]any)
	if len(actions) != 1 {
		f.t.Fatalf("tick = %v, want one action", tick)
	}
	action := actions[0].(map[string]any)
	if action["action"] != "spawn" || action["role"] != role {
		f.t.Fatalf("action = %v, want a %s spawn", action, role)
	}
	return action
}

// load copies the host spine into the named volume with the ownership and
// mode that the image gives /mc/spine.
func (f *agentFixture) load() string {
	f.t.Helper()
	return f.docker("run", "--rm", "--network", "none", "-v", f.volume+":/mc/spine", "-v", f.home+":/in:ro", prodImage,
		"sh", "-ec", `rm -f /mc/spine/spine.db*
for file in /in/spine.db*; do cp "$file" /mc/spine/; done
chown 10001:10001 /mc/spine/spine.db*
chmod 0600 /mc/spine/spine.db*
stat -c '%u:%g %a %n' /mc/spine /mc/spine/spine.db`)
}

// unload copies the spine from the named volume back to the host.
func (f *agentFixture) unload() {
	f.t.Helper()
	out := f.docker("run", "--rm", "--network", "none", "-v", f.volume+":/mc/spine", "-v", f.home+":/out", prodImage,
		"sh", "-ec", `rm -f /out/spine.db*
for file in /mc/spine/spine.db*; do cp "$file" /out/; done
ls /out`)
	if !strings.Contains(out, "spine.db") {
		f.t.Fatalf("the spine copy failed:\n%s", out)
	}
}

func (f *agentFixture) query(q string, args ...any) string {
	f.t.Helper()
	db, err := substrate.Open(f.spine())
	if err != nil {
		f.t.Fatal(err)
	}
	defer db.Close()
	var value sql.NullString
	if err := db.QueryRow(q, args...).Scan(&value); err != nil {
		f.t.Fatalf("query %q: %v", q, err)
	}
	return value.String
}

// runRole starts one pipeline run in the production container profile of a
// run without a repository, with the claude-sdk route and a static synthetic
// credential. It returns the fixture record of the endpoint.
func (f *agentFixture) runRole(effect map[string]any, steps []string) agentResult {
	f.t.Helper()
	runID := effect["run_id"].(string)
	root := sharedTempDir(f.t)
	dirs := map[string]string{}
	// The resident creates the session and scratch folders owner-only, and the
	// log folder open to every uid.
	for name, mode := range map[string]os.FileMode{"session": 0o700, "scratch": 0o700, "logs": 0o777, "fixture": 0o755} {
		dirs[name] = filepath.Join(root, name)
		if err := os.MkdirAll(dirs[name], mode); err != nil {
			f.t.Fatal(err)
		}
		if err := os.Chmod(dirs[name], mode); err != nil {
			f.t.Fatal(err)
		}
	}
	allowWrite := []string{"/mc/scratch", "/mc/logs"}
	var binds []string
	plan, _ := effect["mount_plan"].(map[string]any)
	entries, _ := plan["entries"].([]any)
	for _, raw := range entries {
		entry := raw.(map[string]any)
		source, destination, access := entry["source"].(string), entry["destination"].(string), entry["access"].(string)
		if access == "rw" {
			allowWrite = append([]string{destination}, allowWrite...)
			binds = append(binds, "-v", source+":"+destination)
		} else {
			binds = append(binds, "-v", source+":"+destination+":ro")
		}
	}
	if home, ok := plan["tool_home"].(map[string]any); ok {
		binds = append(binds, "-v", home["canonical"].(string)+":/home/agent", "-e", "HOME=/home/agent")
	}
	envelope := map[string]any{
		"run_id": runID, "tier": "pipeline", "role": effect["role"], "subject_id": effect["subject_id"],
		"project": effect["project"], "harness": "claude-sdk", "model_binding": agentBinding,
		"runtime": map[string]any{"base_url": "", "channel": "claude", "harness": "claude-sdk",
			"id": agentBinding, "model": "fixture-model"},
		"mode": "fresh", "brief": effect["brief"], "skills": effect["skills"], "pool_ids": effect["pool_ids"],
		"heartbeat_interval_s": effect["heartbeat_interval_s"],
		"mounts":               map[string]any{"session": "/mc/session", "workspace": "/mc/scratch", "allow_write": allowWrite},
	}
	body, _ := json.MarshalIndent(envelope, "", "  ")
	runJSON := filepath.Join(root, "run.json")
	if err := os.WriteFile(runJSON, body, 0o644); err != nil {
		f.t.Fatal(err)
	}
	stepBody, _ := json.Marshal(steps)
	ca, cert, key := syntheticGatewayCertificate(f.t)
	access, _ := json.Marshal(map[string]any{"version": 1, "url": agentEndpoint, "token": agentToken,
		"ca": ca, "harness": "claude-sdk", "authMode": "account"})
	for name, content := range map[string][]byte{"steps.json": stepBody, "endpoint.ts": []byte(agentEndpointScript),
		"access.json": access, "server.pem": cert, "server.key": key} {
		if err := os.WriteFile(filepath.Join(dirs["fixture"], name), content, 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
	runner := stageRunnerSource(f.t)
	f.load()
	name := "mc-agent-" + runID
	f.t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	args := append([]string{"run", "--rm", "--name", name, "--network", "none",
		"--user", "10002:10002", "--cap-drop", "ALL",
		"--label", "mc-run-id=" + runID,
		"-v", dirs["session"] + ":/mc/session",
		"-v", runJSON + ":/mc/run.json:ro",
		"-v", dirs["scratch"] + ":/mc/scratch",
		"-v", dirs["logs"] + ":/mc/logs",
		"-v", runner + ":/app/src:ro",
		"-v", dirs["fixture"] + ":/fixture:ro",
		"-v", filepath.Join(dirs["fixture"], "access.json") + ":/mc/provider-access.json:ro",
	}, binds...)
	args = append(args,
		"-v", f.volume+":/mc/spine",
		"-e", "MC_SPINE=/mc/spine/spine.db",
		"-e", "MC_PROVIDER_ACCESS_FILE=/mc/provider-access.json",
		prodImage, "bun", "/fixture/endpoint.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		f.t.Fatalf("the run container failed: %v\n%s", err, out)
	}
	f.unload()
	record, err := os.ReadFile(filepath.Join(dirs["logs"], "fixture.json"))
	if err != nil {
		f.t.Fatalf("the endpoint wrote no record: %v\n%s", err, out)
	}
	var result agentResult
	if err := json.Unmarshal(record, &result); err != nil {
		f.t.Fatalf("decode the endpoint record: %v\n%s", err, record)
	}
	if result.Unauthorized != 0 || result.Requests == 0 {
		f.t.Fatalf("the endpoint saw %d requests and %d without the synthetic credential\n%s", result.Requests, result.Unauthorized, result.Stderr)
	}
	if result.Code != 0 {
		f.t.Fatalf("runner exit = %d\n%s", result.Code, result.Stderr)
	}
	f.t.Logf("SDK %s, %d model requests", result.SDK, result.Requests)
	return result
}

// agentPayload keeps the shell metacharacters literal in the JSON text.
func agentPayload(t *testing.T) string {
	t.Helper()
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(map[string]any{"proposals": []map[string]any{{"title": agentTitle, "description": agentDetail, "strategic_rationale": "Observed: the fixture lacks this result."}}}); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(body.String(), "\n")
}

func agentHeredoc(command, payload string) string {
	return command + " <<'" + agentDelimiter + "'\n" + payload + "\n" + agentDelimiter
}

func requireOutput(t *testing.T, label, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("%s: output %q lacks %q", label, got, want)
		}
	}
}

// A Claude agent runs as uid 10002 without the SDK sandbox. It cannot read the
// spine directly, and its shell commands reach the spine only through the
// setuid mc. A Strategist batch from a here-document and an Arbiter batch
// from a pipe both arrive in the spine.
func TestClaudeAgentUsesMCAsTheModelUserDockerBoundary(t *testing.T) {
	f := newAgentFixture(t)
	effect := f.dispatch("strategist(propose)")
	runID := effect["run_id"].(string)
	if volume := f.load(); !strings.Contains(volume, "10001:10001 700 /mc/spine") || !strings.Contains(volume, "10001:10001 600 /mc/spine/spine.db") {
		t.Fatalf("the spine volume has unexpected ownership:\n%s", volume)
	}
	steps := []string{
		// 0: the agent identity and the setuid precondition.
		`id -u; grep NoNewPrivs /proc/self/status; touch "$HOME/.probe"; echo "home_write=$?"`,
		// 1: direct spine access.
		`cat /mc/spine/spine.db > /dev/null; echo "spine_read=$?"; ls /mc/spine > /dev/null; echo "spine_list=$?"`,
		// 2: a plain authorized read through mc.
		`mc task list`,
		// 3: a false executable earlier on PATH.
		`mkdir -p /tmp/bin && printf '#!/bin/sh\ncat /mc/spine/spine.db > /dev/null && echo FALSE_MC_READ_SPINE\necho false-mc-ran\n' > /tmp/bin/mc && chmod +x /tmp/bin/mc && PATH="/tmp/bin:$PATH" mc task list; echo "false=$?"`,
		// 4: an altered identity.
		`printf '{"run_id":"forged-run","tier":"pipeline","role":"arbiter"}' > /tmp/run.json; ` +
			agentHeredoc(`MC_RUN_JSON=/tmp/run.json mc arbiter decide --run forged-run --batch -`, `{"verdicts":[]}`) + `
echo "identity=$?"`,
		// 5: the terminal action from a here-document.
		agentHeredoc(`mc strategist propose --run `+runID+` --batch -`, agentPayload(t)) + `
echo "submit=$?"`,
	}
	result := f.runRole(effect, steps)
	requireOutput(t, "identity", result.step(t, 0), "10002\n", "NoNewPrivs:\t0", "home_write=0")
	requireOutput(t, "spine", result.step(t, 1), "Permission denied", "spine_read=1", "spine_list=2")
	var list map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.step(t, 2))), &list); err != nil {
		t.Fatalf("the plain read returned %q: %v", result.step(t, 2), err)
	}
	requireOutput(t, "false executable", result.step(t, 3), "false-mc-ran", "false=0")
	if strings.Contains(result.step(t, 3), "FALSE_MC_READ_SPINE") {
		t.Errorf("a false executable read the spine: %q", result.step(t, 3))
	}
	requireOutput(t, "altered identity", result.step(t, 4), `"code":"role-mismatch"`, "identity=1")
	requireOutput(t, "submit", result.step(t, 5), "submit=0")
	if got := f.query(`SELECT outcome FROM runs WHERE id = ?`, runID); got != "completed" {
		t.Fatalf("Strategist outcome = %q, want completed", got)
	}
	if got := f.query(`SELECT title FROM tasks WHERE project = ?`, agentProject); got != agentTitle {
		t.Fatalf("stored title = %q, want %q", got, agentTitle)
	}
	if got := f.query(`SELECT description FROM tasks WHERE project = ?`, agentProject); got != agentDetail {
		t.Fatalf("stored description = %q, want %q", got, agentDetail)
	}

	arbiter := f.dispatch("arbiter")
	arbiterRun := arbiter["run_id"].(string)
	task := f.query(`SELECT id FROM tasks WHERE project = ?`, agentProject)
	verdict := `{"verdicts":[{"task":` + task + `,"decision":"promote"}]}`
	arbiterResult := f.runRole(arbiter, []string{
		agentHeredoc(`cat > /tmp/verdicts.json`, verdict),
		`cat /tmp/verdicts.json | mc arbiter decide --run ` + arbiterRun + ` --batch -; echo "decide=$?"`,
	})
	requireOutput(t, "arbiter", arbiterResult.step(t, 1), "decide=0")
	if got := f.query(`SELECT outcome FROM runs WHERE id = ?`, arbiterRun); got != "completed" {
		t.Fatalf("Arbiter outcome = %q, want completed", got)
	}
	if got := f.query(`SELECT status FROM tasks WHERE id = ?`, task); got != "seeded" {
		t.Fatalf("promoted task status = %q, want seeded", got)
	}
}

// The Collie container has no synthetic home. The image home of uid 10002 must
// accept the skill install.
func TestCollieHomeIsWritableForTheModelUserDockerBoundary(t *testing.T) {
	requireProdImage(t)
	out, err := dockerRun(t, "--user", "10002:10002", "--cap-drop", "ALL", "-e", "HOME=/home/mc-model", prodImage,
		"sh", "-c", `mkdir -p "$HOME/.agents/skills/probe" && echo home-ok; ls /mc/spine > /dev/null; echo "spine_list=$?"`)
	if err != nil {
		t.Fatalf("probe the Collie home: %v\n%s", err, out)
	}
	requireOutput(t, "home", out, "home-ok", "spine_list=2")
}
