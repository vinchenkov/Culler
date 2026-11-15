package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	operatorconfig "mc/config"
)

func TestTraceSessionsVerbListsMainTracesForTheHostOnly(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	const claudeRef = "11111111-1111-4111-8111-111111111111"
	const codexRef = "22222222-2222-4222-8222-222222222222"
	codexFile := "2026/09/29/rollout-2026-09-29T10-00-00-" + codexRef + ".jsonl"
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`UPDATE projects SET title = 'Culler' WHERE id = 'ws-test'`, nil},
		{`INSERT INTO runs (id, tier, role, project, session_path, binding, native_session_ref, trace_filename, ended_at)
			VALUES ('r-claude', 'pipeline', 'strategist', 'ws-test', 'sessions/r-claude', 'claude-sdk/claude', ?,
			'native.jsonl', '2026-09-29 10:05:00')`, []any{claudeRef}},
		{`INSERT INTO runs (id, tier, role, project, session_path, binding)
			VALUES ('r-merge', 'pipeline', 'worker', 'ws-test', 'sessions/r-merge', 'mechanical')`, nil},
		{`INSERT INTO collie_sessions (id, container_name, verb_allowlist, session_path, binding,
			native_session_ref, trace_filename)
			VALUES ('h-codex', 'mc-collie-h-codex', '[]', 'sessions/h-codex', 'codex/chatgpt', ?, ?)`,
			[]any{codexRef, codexFile}},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	res := runMC(t, spineEnv(spine), "", "__trace-sessions")
	if res.code != 0 {
		t.Fatalf("__trace-sessions failed (%d): %s", res.code, res.stderr)
	}
	body, err := json.Marshal(res.json)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"sessions":[` +
		`{"agentsview_session":"` + claudeRef + `","display_name":"Culler · strategist · r-claude","ended":true,` +
		`"format":"claude","id":"r-claude","kind":"run","native_session_ref":"` + claudeRef + `",` +
		`"session_path":"sessions/r-claude","trace_filename":"native.jsonl"},` +
		`{"agentsview_session":"codex:` + codexRef + `","display_name":"Collie · h-codex","ended":false,` +
		`"format":"codex","id":"h-codex","kind":"collie","native_session_ref":"` + codexRef + `",` +
		`"session_path":"sessions/h-codex","trace_filename":"` + codexFile + `"}]}`
	if string(body) != want {
		t.Fatalf("__trace-sessions =\n%s\nwant\n%s", body, want)
	}

	if usage := runMC(t, spineEnv(spine), "", "__trace-sessions", "--all"); usage.code != 2 {
		t.Fatalf("an extra argument exited %d: %s", usage.code, usage.stderr)
	}
	agent := runMC(t, append(spineEnv(spine), runJSONEnv(t, spine, "r-claude", "pipeline", "worker")...), "", "__trace-sessions")
	if agent.code != 1 || !strings.Contains(agent.stderr, "host scope only") || strings.Contains(agent.stdout, claudeRef) {
		t.Fatalf("an agent read the trace sessions (%d): %s %s", agent.code, agent.stdout, agent.stderr)
	}
}

// fakeExportDocker is a docker executable that writes the five export files
// into the output mount after it checks the input mount.
const fakeExportDocker = `#!/bin/sh
printf '%s\n' "$*" >> "$MC_FAKE_DOCKER_LOG"
[ "$1" = run ] || exit 0
previous=""
for argument in "$@"; do
	if [ "$previous" = "-v" ]; then
		case "$argument" in
		*:/output) output="${argument%:/output}" ;;
		*:/input/openwiki:ro) input="${argument%:/input/openwiki:ro}" ;;
		esac
	fi
	previous="$argument"
done
[ -f "$input/index.md" ] || { echo "the input lacks index.md" >&2; exit 1; }
for name in index.html client.js client-lib.js styles.css graph.json; do
	printf 'export of %s\n' "$name" > "$output/$name"
done
`

func exportEnv(t *testing.T, spine string) ([]string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(fakeExportDocker), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "docker.log")
	return append(spineEnv(spine), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"MC_FAKE_DOCKER_LOG="+log), log
}

func dockerRuns(t *testing.T, log string) int {
	t.Helper()
	body, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(body), "run --rm ")
}

func TestWikiExportVerbExportsTheAcceptedWiki(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	workspace := queryString(t, db, `SELECT p.workspace_root FROM projects w
		JOIN sandbox_profiles p ON p.id = w.sandbox_profile WHERE w.id = 'ws-test'`)
	accepted := fixtureGit(t, workspace, "rev-parse", "refs/heads/culler")
	env, log := exportEnv(t, spine)

	first := runMC(t, env, "", "__wiki-export", "--image", "mc-test-image")
	if first.code != 0 {
		t.Fatalf("__wiki-export failed (%d): %s %s", first.code, first.stdout, first.stderr)
	}
	if strings.TrimSpace(first.stdout) != `{"projects":[{"project":"ws-test","status":"exported","commit":"`+accepted+`"}]}` {
		t.Fatalf("__wiki-export = %s", first.stdout)
	}
	record, err := os.ReadFile(filepath.Join(filepath.Dir(spine), "wiki", "ws-test", "export.json"))
	if err != nil || !strings.Contains(string(record), `"directory":"`+accepted+`"`) {
		t.Fatalf("export record = %s %v", record, err)
	}
	page, err := os.ReadFile(filepath.Join(filepath.Dir(spine), "wiki", "ws-test", accepted, "index.html"))
	if err != nil || string(page) != "export of index.html\n" {
		t.Fatalf("exported page = %q %v", page, err)
	}
	body, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(body), "--network none --user 10002:10002 --cap-drop ALL") ||
		!strings.Contains(string(body), " mc-test-image /usr/bin/env -i ") {
		t.Fatalf("docker calls = %s %v", body, err)
	}

	second := runMC(t, env, "", "__wiki-export", "--image", "mc-test-image")
	if second.code != 0 || !strings.Contains(second.stdout, `"status":"current"`) || dockerRuns(t, log) != 1 {
		t.Fatalf("a current export ran again (%d runs): %s %s", dockerRuns(t, log), second.stdout, second.stderr)
	}

	for _, args := range [][]string{
		{"__wiki-export"},
		{"__wiki-export", "--image", "-mc-prod"},
		{"__wiki-export", "--image", "mc prod"},
		{"__wiki-export", "--image", "mc-prod", "extra"},
	} {
		if usage := runMC(t, env, "", args...); usage.code != 2 {
			t.Fatalf("%v exited %d: %s", args, usage.code, usage.stderr)
		}
	}
	agent := runMC(t, append(env, runJSONEnv(t, spine, "r-agent", "pipeline", "worker")...), "",
		"__wiki-export", "--image", "mc-test-image")
	if agent.code != 1 || !strings.Contains(agent.stderr, "host scope only") || dockerRuns(t, log) != 1 {
		t.Fatalf("an agent started a wiki export (%d): %s", agent.code, agent.stderr)
	}
}

// waitingExportDocker is a docker executable whose export run waits until a
// signal stops it.
const waitingExportDocker = `#!/bin/sh
printf '%s\n' "$*" >> "$MC_FAKE_DOCKER_LOG"
[ "$1" = run ] || exit 0
exec sleep 60
`

func TestWikiExportStopsItsContainerAtAStopRequest(t *testing.T) {
	spine := initSpine(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(waitingExportDocker), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "docker.log")
	cmd := exec.Command(mcBin, "__wiki-export", "--image", "mc-test-image")
	cmd.Env = hermeticEnv(append(spineEnv(spine), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"MC_FAKE_DOCKER_LOG="+log)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.Now().Add(30 * time.Second)
	for dockerRuns(t, log) == 0 {
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("the export container did not start: %s", stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("__wiki-export did not stop cleanly: %v: %s", err, stderr.String())
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("__wiki-export did not stop after SIGTERM")
	}
	body, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(body), "rm -f mc-wiki-export-") {
		t.Fatalf("the stopped export left its container: %s %v", body, err)
	}
	if !strings.Contains(stdout.String(), `"status":"failed"`) {
		t.Fatalf("__wiki-export = %s", stdout.String())
	}
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(spine), "wiki", "ws-test"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the stopped export left %d entries, first %s", len(entries), entries[0].Name())
	}
}

func TestWikiCommandShowsOnlyTheAcceptedWiki(t *testing.T) {
	spine := initSpine(t)
	show := runMC(t, spineEnv(spine), "", "wiki", "show", "--project", "ws-test")
	if show.code != 0 || show.json["project"] != "ws-test" || show.json["status"] != "ready" {
		t.Fatalf("wiki show (%d): %s %s", show.code, show.stdout, show.stderr)
	}
	file := runMC(t, spineEnv(spine), "", "wiki", "file", "--project", "ws-test", "--commit",
		show.json["accepted_commit"].(string), "--path", "README.md")
	if file.code != 2 || !strings.Contains(file.stderr, "usage: mc wiki show --project <id>") {
		t.Fatalf("wiki file (%d): %s %s", file.code, file.stdout, file.stderr)
	}
}

func TestSystemStatusCarriesTheViewers(t *testing.T) {
	spine := initSpine(t)
	home := filepath.Dir(spine)
	env := spineEnv(spine)
	viewers := func() (map[string]any, []any) {
		t.Helper()
		status := runMC(t, env, "", "system", "status")
		if status.code != 0 {
			t.Fatalf("system status failed (%d): %s", status.code, status.stderr)
		}
		value, ok := status.json["viewers"].(map[string]any)
		if !ok {
			t.Fatalf("system status lacks the viewers: %s", status.stdout)
		}
		conditions, _ := status.json["conditions"].([]any)
		return value, conditions
	}

	value, conditions := viewers()
	body, _ := json.Marshal(value)
	if string(body) != `{"agentsview":{"reason":"AgentsView runs only in a production deployment.","state":"unavailable",`+
		`"url":"http://127.0.0.1:7334"},"wiki":{"projects":{},"url":"http://127.0.0.1:7335"}}` {
		t.Fatalf("viewers without a unit = %s", body)
	}
	for _, raw := range conditions {
		if strings.HasPrefix(raw.(map[string]any)["id"].(string), "agentsview:") {
			t.Fatalf("a deployment without the unit reported %v", raw)
		}
	}

	withDocker, _ := exportEnv(t, spine)
	exportRun := runMC(t, withDocker, "", "__wiki-export", "--image", "mc-test-image")
	if exportRun.code != 0 {
		t.Fatalf("__wiki-export failed: %s", exportRun.stderr)
	}
	commit := exportRun.json["projects"].([]any)[0].(map[string]any)["commit"].(string)
	key := writeViewerSecret(t, filepath.Join(home, "state"), "wiki-key", strings.Repeat("1", 64))
	value, _ = viewers()
	wiki := value["wiki"].(map[string]any)["projects"].(map[string]any)
	if exported, _ := json.Marshal(wiki); string(exported) !=
		`{"ws-test":{"commit":"`+commit+`","url":"http://127.0.0.1:7335/`+key+`/ws-test/"}}` {
		t.Fatalf("wiki exports = %s", exported)
	}

	token := writeViewerSecret(t, filepath.Join(home, "agentsview"), "token", strings.Repeat("2", 64))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"service":"agentsview","version":"v0.44.0"}`))
	}))
	defer server.Close()
	bind := strings.TrimPrefix(server.URL, "http://")
	if _, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), func(c *operatorconfig.Config) error {
		c.Surfaces.AgentsView.Bind = bind
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(home, "supervision", "LaunchAgents")
	if err := os.MkdirAll(unit, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unit, "agentsview.plist"), []byte("<plist/>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, _ = viewers()
	if agentsView := value["agentsview"].(map[string]any); agentsView["state"] != "available" ||
		agentsView["reason"] != "" || agentsView["url"] != server.URL {
		t.Fatalf("AgentsView with an answer = %v", agentsView)
	}
	writeViewerSecret(t, filepath.Join(home, "agentsview"), "token", strings.Repeat("3", 64))
	value, _ = viewers()
	if agentsView := value["agentsview"].(map[string]any); agentsView["state"] != "unavailable" ||
		agentsView["reason"] != "AgentsView at "+server.URL+" refuses the AgentsView token of this deployment." {
		t.Fatalf("AgentsView with a refused token = %v", agentsView)
	}
	if status := runMC(t, env, "", "system", "status"); strings.Contains(status.stdout, token) ||
		strings.Contains(status.stdout, strings.Repeat("3", 64)) {
		t.Fatalf("the system status holds the AgentsView token: %s", status.stdout)
	}
	writeViewerSecret(t, filepath.Join(home, "agentsview"), "token", token)

	server.Close()
	started := time.Now()
	value, conditions = viewers()
	if agentsView := value["agentsview"].(map[string]any); agentsView["state"] != "unavailable" ||
		agentsView["reason"] != "AgentsView does not answer at "+server.URL+"." {
		t.Fatalf("AgentsView without an answer = %v", agentsView)
	}
	found := false
	for _, raw := range conditions {
		condition := raw.(map[string]any)
		if condition["id"] == "agentsview:unavailable" {
			found = strings.Contains(condition["condition"].(string), "Trace links do not open.")
		}
	}
	if !found || time.Since(started) > 10*time.Second {
		t.Fatalf("conditions = %v after %s", conditions, time.Since(started))
	}
}

// writeViewerSecret writes one secret file of a deployment with mode 0600 in
// a folder with mode 0700.
func writeViewerSecret(t *testing.T, dir, name, secret string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	_ = os.Remove(path)
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return secret
}

func TestAgentsViewAccessVerbServesTheHostOnly(t *testing.T) {
	spine := initSpine(t)
	home := filepath.Dir(spine)
	missing := runMC(t, spineEnv(spine), "", "__agentsview-access")
	if missing.code != 1 || !strings.Contains(missing.stderr, "The AgentsView token file") ||
		!strings.Contains(missing.stderr, "To repair AgentsView, run install.sh again.") {
		t.Fatalf("a missing token (%d): %s %s", missing.code, missing.stdout, missing.stderr)
	}
	token := writeViewerSecret(t, filepath.Join(home, "agentsview"), "token", strings.Repeat("4", 64))
	res := runMC(t, spineEnv(spine), "", "__agentsview-access")
	if res.code != 0 || strings.TrimSpace(res.stdout) != `{"token":"`+token+`","url":"http://127.0.0.1:7334"}` {
		t.Fatalf("__agentsview-access (%d): %s %s", res.code, res.stdout, res.stderr)
	}
	if usage := runMC(t, spineEnv(spine), "", "__agentsview-access", "--all"); usage.code != 2 {
		t.Fatalf("__agentsview-access --all exit = %d", usage.code)
	}
	agent := runMC(t, append(spineEnv(spine), runJSONEnv(t, spine, "r-token", "pipeline", "worker")...), "", "__agentsview-access")
	if agent.code == 0 || strings.Contains(agent.stdout, token) || !strings.Contains(agent.stderr, "host scope only") {
		t.Fatalf("an agent read the token (%d): %s %s", agent.code, agent.stdout, agent.stderr)
	}
	body, _ := os.ReadFile(filepath.Join(home, "logs", "mc.ndjson"))
	if strings.Contains(string(body), token) {
		t.Fatal("the mc log holds the AgentsView token")
	}
}

func TestSetupTraceCommandsFenceRunnerIdentityAndHostRecovery(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	if _, err := db.Exec(`INSERT INTO environment_revisions(id,project_id,editor,configuration,inputs_digest)
		VALUES('trace-revision','ws-test','culler','{}','fixture')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO environment_operations(id,project_id,revision_id,kind,status,inputs_digest,image_digest,runtime_binding,ended_at)
		VALUES('setup-trace','ws-test','trace-revision','setup','succeeded','fixture','fixture',
		'{"harness":"claude-sdk","id":"fixture"}',datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	runner := append(spineEnv(spine), runJSONEnv(t, spine, "setup-trace", "setup", "setup")...)
	registered := runMC(t, runner, "", "run", "register-session", "setup-trace", "--native-ref", "fixture-native", "--file", "native.jsonl")
	if registered.code != 0 {
		t.Fatalf("late setup registration failed: %s", registered.stderr)
	}
	other := append(spineEnv(spine), runJSONEnv(t, spine, "other-operation", "setup", "setup")...)
	if got := runMC(t, other, "", "run", "register-session", "setup-trace", "--native-ref", "fixture-native", "--file", "native.jsonl"); got.code != 1 {
		t.Fatalf("another runner registered a setup trace: %d %s", got.code, got.stderr)
	}
	if got := runMC(t, runner, "", "__trace-register-setup", "setup-trace", "fixture-native", "native.jsonl"); got.code != 1 {
		t.Fatalf("a runner invoked host recovery: %d %s", got.code, got.stderr)
	}
	if got := runMC(t, spineEnv(spine), "", "__trace-register-setup", "setup-trace", "fixture-native", "native.jsonl"); got.code != 0 {
		t.Fatalf("host recovery refused identical locators: %d %s", got.code, got.stderr)
	}
	if got := runMC(t, spineEnv(spine), "", "__trace-register-setup", "setup-trace", "another-native", "native.jsonl"); got.code != 1 {
		t.Fatalf("host recovery replaced registered locators: %d %s", got.code, got.stderr)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM environment_operations WHERE id='setup-trace'`).Scan(&status); err != nil || status != "succeeded" {
		t.Fatalf("trace registration changed the result: %q %v", status, err)
	}
}
