// CLI tests for the verb surface, driven through the real built binary
// against a temp spine — exit codes, stdout JSON, and spine state are all
// asserted at the os/exec boundary. Docker-free by construction: the e2e
// alone is behind the docker_e2e tag.
package main_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	operatorconfig "mc/config"
	"mc/domain"
	"mc/operatorcmd"
	"mc/routing"
	"mc/substrate"
	"mc/testenv"
	"mc/verbs"
	"mc/wikitest"
)

var mcBin string

// testHome stands in for the operator's home directory in every child mc
// process this package spawns. Stripping MC_HOME does not make a child
// hermetic: MC_HOME falls back to $HOME/.culler, so a test that
// supplies no MC_HOME resolves the operator's REAL deployment. One uncached run
// of this package wrote 382 records into the live deployment's mc.ndjson that
// way. $HOME is what has to move.
var testHome string

func TestMain(m *testing.M) {
	// These subprocesses use the parent environment without another mc build.
	if os.Getenv("MC_TEST_PRIVATE_SELF_DEADLINE") == "1" || os.Getenv("MC_TEST_PRIVATE_ABSOLUTE_DEADLINE") == "1" ||
		os.Getenv("MC_SETUP_INTERRUPT_FIXTURE") == "1" {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "mc-cli-test")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := testenv.Isolate(dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	mcBin = filepath.Join(dir, "mc")
	testHome = filepath.Join(dir, "home")
	if err := os.MkdirAll(testHome, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// The test binary uses a fake harness and a disposable local database.
	build := exec.Command("mise", "exec", "--", "go", "build", "-tags", cliTestBuildTags, "-o", mcBin, ".")
	out, err := build.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build mc binary: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	// Keep the Go caches stable when child test processes use a scratch home.
	if os.Getenv("GOMODCACHE") == "" || os.Getenv("GOCACHE") == "" {
		command := exec.Command("mise", "exec", "--", "go", "env", "GOMODCACHE", "GOCACHE")
		body, err := command.Output()
		paths := strings.Split(strings.TrimSpace(string(body)), "\n")
		if err != nil || len(paths) != 2 || paths[0] == "" || paths[1] == "" {
			fmt.Fprintln(os.Stderr, "The test process cannot resolve the Go caches.")
			os.RemoveAll(dir)
			os.Exit(1)
		}
		os.Setenv("GOMODCACHE", paths[0])
		os.Setenv("GOCACHE", paths[1])
	}
	// Only now — the build above resolves GOCACHE through os.UserCacheDir(),
	// so moving $HOME any earlier makes every run rebuild the world. From here
	// on the tests in package main that call run() in-process resolve the same
	// temp home their spawned counterparts get, instead of appending to the
	// operator's live $MC_HOME/logs/mc.ndjson.
	os.Setenv("HOME", testHome)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// mcResult is one CLI invocation's observable surface.
type mcResult struct {
	code   int
	stdout string
	stderr string
	json   map[string]any
}

// hermeticEnv is the ambient environment with every variable that could point a
// child at the operator's real deployment removed and $HOME moved to the
// package temp home. Caller overrides are appended last and therefore win:
// os/exec keeps the last duplicate of a key, so a test that needs its own HOME
// or MC_HOME still gets it.
func hermeticEnv(env ...string) []string {
	base := []string{}
	for _, e := range os.Environ() {
		name, _, _ := strings.Cut(e, "=")
		if testenv.RunVariable(name) || name == "HOME" {
			continue
		}
		base = append(base, e)
	}
	// An absent test identity prevents fallback to the identity of an agent container.
	base = append(base, "HOME="+testHome, "MC_RUN_JSON="+filepath.Join(testHome, "absent-run.json"))
	return append(base, env...)
}

// runMC invokes the built binary. env entries are KEY=VALUE overrides; the
// ambient MC_* variables and $HOME are replaced so tests are hermetic.
func runMC(t *testing.T, env []string, stdin string, args ...string) mcResult {
	t.Helper()
	cmd := exec.Command(mcBin, args...)
	cmd.Env = hermeticEnv(env...)
	stopHost := serveOperatorRequests(cmd.Env, args)
	defer stopHost()
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	res := mcResult{stdout: outBuf.String(), stderr: errBuf.String()}
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run mc %v: %v", args, err)
		}
		res.code = exit.ExitCode()
	}
	if strings.TrimSpace(res.stdout) != "" {
		if err := json.Unmarshal([]byte(res.stdout), &res.json); err != nil {
			t.Fatalf("mc %v: stdout is not a single JSON object: %q (%v)", args, res.stdout, err)
		}
	}
	return res
}

// The host fixture executes queued requests through the real command transport.
func serveOperatorRequests(env, args []string) func() {
	d, ok := operatorcmd.Lookup(args)
	if !ok || (!d.Host && !d.Mutates) {
		return func() {}
	}
	var identityPath string
	for _, value := range env {
		if strings.HasPrefix(value, "MC_RUN_JSON=") {
			identityPath = strings.TrimPrefix(value, "MC_RUN_JSON=")
		}
	}
	body, err := os.ReadFile(identityPath)
	var identity verbs.RunIdentity
	if err != nil || json.Unmarshal(body, &identity) != nil || identity.Tier != "collie" {
		return func() {}
	}
	hostEnv := append(append([]string{}, env...), "MC_RUN_JSON="+filepath.Join(testHome, "operator-host-identity-absent"))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(25 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				list := exec.CommandContext(ctx, mcBin, "__operator-due")
				list.Env = hostEnv
				list.Stdin = strings.NewReader("{}")
				output, err := list.Output()
				var requests struct {
					Operations []verbs.OperatorRequest `json:"operations"`
				}
				if err != nil || json.Unmarshal(output, &requests) != nil {
					continue
				}
				for _, request := range requests.Operations {
					execute := exec.CommandContext(ctx, mcBin, "__operator-execute", request.ID)
					execute.Env = hostEnv
					_ = execute.Run()
				}
			}
		}
	}()
	return func() { cancel(); <-done }
}

func TestStructuredErrorJSON(t *testing.T) {
	spine := initSpine(t)

	domainRes := runMC(t, spineEnv(spine), "", "task", "cancel", "999", "--reason", "absent")
	if domainRes.code != 1 {
		t.Fatalf("domain exit = %d stderr=%q", domainRes.code, domainRes.stderr)
	}
	domainErr, ok := domainRes.json["error"].(map[string]any)
	if !ok || domainErr["code"] != "not-found" || !strings.Contains(domainErr["message"].(string), "no task") {
		t.Fatalf("domain error JSON = %v", domainRes.json)
	}

	usageRes := runMC(t, spineEnv(spine), "", "task", "add")
	if usageRes.code != 2 {
		t.Fatalf("usage exit = %d stderr=%q", usageRes.code, usageRes.stderr)
	}
	usageErr, ok := usageRes.json["error"].(map[string]any)
	if !ok || usageErr["code"] != "usage" {
		t.Fatalf("usage error JSON = %v", usageRes.json)
	}

	// CLI-plane domain refusals without a narrower aggregate code still have
	// one stable public slug rather than an empty/missing field.
	eff := dispatchExpect(t, spine, "spawn")
	run := eff["run_id"].(string)
	scopeRes := runMC(t, runJSONEnv(t, spine, run, "pipeline", "arbiter"), "", "dispatch")
	if scopeRes.code != 1 {
		t.Fatalf("scope exit = %d stderr=%q", scopeRes.code, scopeRes.stderr)
	}
	scopeErr, ok := scopeRes.json["error"].(map[string]any)
	if !ok || scopeErr["code"] != "domain-rejection" {
		t.Fatalf("scope error JSON = %v", scopeRes.json)
	}
	if !strings.Contains(scopeRes.stderr, scopeErr["message"].(string)) {
		t.Fatalf("stderr %q does not preserve JSON diagnostic %v", scopeRes.stderr, scopeErr)
	}

	// Zero-args and wrapper-only delegation failures are local rejections
	// too: one stdout JSON envelope each, not bare stderr.
	bare := runMC(t, spineEnv(spine), "")
	if bare.code != 2 {
		t.Fatalf("zero-args exit = %d stderr=%q", bare.code, bare.stderr)
	}
	bareErr, ok := bare.json["error"].(map[string]any)
	if !ok || bareErr["code"] != "usage" {
		t.Fatalf("zero-args error JSON = %v", bare.json)
	}

	broken := runMC(t, []string{"MC_HELPER=phantom", "PATH=/nonexistent"}, "", "task", "list")
	if broken.code != 2 {
		t.Fatalf("delegation-failure exit = %d stderr=%q", broken.code, broken.stderr)
	}
	brokenErr, ok := broken.json["error"].(map[string]any)
	if !ok || brokenErr["code"] != "usage" {
		t.Fatalf("delegation-failure error JSON = %v stdout=%q", broken.json, broken.stdout)
	}
}

const fakeRoutingMarkdown = `# Culler routing

| role | harness | binding |
| --- | --- | --- |
| strategist | fake | fake |
| arbiter | fake | fake |
| worker | fake | fake |
| verifier | fake | fake |
| packager | fake | fake |
| collie | fake | fake |
`

const defaultRoutingMarkdown = `# Culler routing

| role | harness | binding |
| --- | --- | --- |
| strategist | claude-sdk | claude |
| arbiter | codex | chatgpt |
| worker | claude-sdk | endpoint |
| verifier | codex | chatgpt |
| packager | claude-sdk | endpoint |
| collie | claude-sdk | claude |
`

// Derived from the real registry, never hand-copied: a literal here drifts the
// moment a verb joins the allowlist, and it fails as an envelope/registry
// mismatch that names neither the new verb nor this stale copy. `mc collie start`
// freezes the same source into the spine, so deriving it is what makes the
// fixture agree with production by construction.
var defaultCollieAllowlist = verbs.MaximumCollieAgentVerbs()

func spineEnv(spine string) []string {
	return []string{
		"MC_SPINE=" + spine,
		"MC_HOME=" + filepath.Dir(spine),
	}
}

// gitFixtureWorkspace creates a repository at its canonical path with one
// commit on main and an untracked vision file. The commit carries a complete
// project wiki, which the wiki export reads.
func gitFixtureWorkspace(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve canonical fixture workspace: %v", err)
	}
	if out, err := exec.Command("git", "init", "-q", "-b", "main", root).CombinedOutput(); err != nil {
		t.Fatalf("git init fixture workspace: %v (%s)", err, out)
	}
	for name, body := range map[string]string{
		"README.md":                  "# Fixture\n",
		"openwiki/index.md":          "---\ntitle: Fixture\ntype: Index\n---\n# Fixture\n",
		"openwiki/.last-update.json": `{"status":"complete"}`,
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fixtureGit(t, root, "add", "-A")
	fixtureGit(t, root, "commit", "-q", "-m", "Start the fixture")
	writeVisionFile(t, root)
	return root
}

// fixtureGit runs one git command in a fixture repository with a fixed author.
func fixtureGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root,
		"-c", "user.name=Fixture", "-c", "user.email=fixture@example.test",
		"-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v (%s)", args, root, err, out)
	}
	return strings.TrimSpace(string(out))
}

// enableReferenceBindings adds the endpoint agent profile and enables it with
// the chatgpt and claude reference bindings, the set the test routing tables
// name. The shipped catalog enables no binding until onboarding records the
// operator's selection.
func enableReferenceBindings(cfg *operatorconfig.Config) error {
	if cfg.Runtimes.Credentials == nil {
		cfg.Runtimes.Credentials = map[string]operatorconfig.CredentialSource{}
	}
	if cfg.Runtimes.Bindings == nil {
		cfg.Runtimes.Bindings = map[string]operatorconfig.RuntimeBinding{}
	}
	profile := cfg.Runtimes.Bindings["chatgpt"]
	profile.Model = "fixture-model"
	cfg.Runtimes.Bindings["chatgpt"] = profile
	cfg.Runtimes.Credentials["endpoint"] = cfg.Runtimes.Credentials["claude"]
	cfg.Runtimes.Bindings["endpoint"] = operatorconfig.RuntimeBinding{
		Harness: "claude-sdk", Model: "endpoint-model", Credential: "endpoint",
	}
	cfg.Runtimes.EnabledBindings = []string{"chatgpt", "claude", "endpoint"}
	return nil
}

// initSpine provisions a fresh temp spine and returns its path.
func initSpine(t *testing.T, extra ...string) string {
	t.Helper()
	spine := filepath.Join(t.TempDir(), "spine.db")
	// `mc __init` creates the Project synthetic home under MC_HOME, and the
	// chain must be owner-only from the home down.
	if err := os.Chmod(filepath.Dir(spine), 0o700); err != nil {
		t.Fatalf("secure fixture home: %v", err)
	}
	workspace := gitFixtureWorkspace(t)
	args := append([]string{
		"__init", "--spine", spine,
		"--project", "ws-test",
		"--workspace-root", workspace,
	}, extra...)
	res := runMC(t, spineEnv(spine), "", args...)
	if res.code != 0 {
		t.Fatalf("mc __init failed (%d): %s", res.code, res.stderr)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(spine), "routing.md"), []byte(fakeRoutingMarkdown), 0o600); err != nil {
		t.Fatalf("write test routing.md: %v", err)
	}
	if _, err := operatorconfig.Ensure(filepath.Join(filepath.Dir(spine), "config.toml")); err != nil {
		t.Fatalf("write test config.toml: %v", err)
	}
	if _, err := operatorconfig.UpdateAtomic(filepath.Join(filepath.Dir(spine), "config.toml"), enableReferenceBindings); err != nil {
		t.Fatalf("enable reference bindings: %v", err)
	}
	// Through the real producer, never a hand-copied `version = 1`: the fixture
	// must stand up the MC_HOME that onboarding produces, or it certifies a
	// deployment shape no install can reach.
	if _, err := verbs.EnsureMountAllowlist(filepath.Dir(spine)); err != nil {
		t.Fatalf("scaffold test mount-allowlist: %v", err)
	}
	// The fake lane binds the whole Project workspace for every role, and
	// that request is ordinary — it needs the PERMISSION half or every fake
	// dispatch refuses as mount.not_allowlisted. The layout below is the other
	// half onboarding builds: the task-store parent must already be the exact
	// operator-owned mode-0700 directory before any Worker precreate resolves.
	if _, err := verbs.EnsureProjectArtifactGrant(filepath.Dir(spine), "ws-test", workspace); err != nil {
		t.Fatalf("grant test Project workspace: %v", err)
	}
	if _, _, err := verbs.EnsureOnboardWorkspaceLayout(
		[]verbs.OnboardWorkspaceRoot{{ID: "ws-test", Root: workspace}}, os.Getuid()); err != nil {
		t.Fatalf("build test Project workspace layout: %v", err)
	}
	// The deployment identity mirror: dispatch refuses to prepare without it
	// matching meta.deployment_uuid.
	uuid, _ := res.json["deployment_uuid"].(string)
	if uuid == "" {
		t.Fatalf("mc __init effect carries no deployment_uuid: %v", res.json)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(spine), "deployment.uuid"), []byte(uuid+"\n"), 0o600); err != nil {
		t.Fatalf("write deployment mirror: %v", err)
	}
	canonicalHome, err := filepath.EvalSymlinks(filepath.Dir(spine))
	if err != nil {
		t.Fatalf("resolve canonical fixture home: %v", err)
	}
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatalf("open fixture spine: %v", err)
	}
	defer db.Close()
	toolHome := filepath.Join(canonicalHome, "state", "projects", "ws-test", "home")
	if got := queryStr(t, db, `SELECT tool_home_dir FROM sandbox_profiles WHERE id='default'`); got != toolHome {
		t.Fatalf("mc __init stored synthetic home %q, want %q", got, toolHome)
	}
	if info, err := os.Stat(toolHome); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("mc __init left synthetic home %s unusable: %v", toolHome, err)
	}
	recordCurrentWiki(t, db, "ws-test", workspace)
	recordFixtureEnvironment(t, db, "ws-test")
	return spine
}

func recordFixtureEnvironment(t *testing.T, db *sql.DB, project string) {
	t.Helper()
	base := "sha256:" + strings.Repeat("a", 64)
	if _, err := db.Exec(`INSERT INTO environment_base_sources(base_digest,registry_reference,release_identity) VALUES(?,'','fixture') ON CONFLICT(base_digest,registry_reference) DO NOTHING`, base); err != nil {
		t.Fatal(err)
	}
	configuration := verbs.EnvironmentConfiguration{BaseDigest: base}
	if err := verbs.ValidateEnvironmentConfiguration(&configuration); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	revision := "fixture-environment-" + project
	if _, err = db.Exec(`INSERT INTO environment_revisions(id,project_id,editor,configuration,inputs_digest) VALUES(?,?,'fixture',?,?)`, revision, project, string(body), "sha256:"+strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO project_environments(project_id,setup_id,selected_base_digest,selected_revision_id,selected_image_digest,draft_revision_id,readiness,phase,authorization) VALUES(?,?,?,?,?,?,'ready','complete','change')`, project, "fixture-setup-"+project, base, revision, base, revision); err != nil {
		t.Fatal(err)
	}
}

// withoutScribeRuns removes each Scribe run from the rows of `mc run list`.
// The current wiki of the fixture holds one Scribe run.
func withoutScribeRuns(rows []any) []any {
	kept := make([]any, 0, len(rows))
	for _, raw := range rows {
		if row, ok := raw.(map[string]any); !ok || row["role"] != "scribe" {
			kept = append(kept, raw)
		}
	}
	return kept
}

// recordCurrentWiki gives the Project a wiki with the state current at the
// commit of main in its workspace. A tick then starts no wiki update for it.
func recordCurrentWiki(t *testing.T, db *sql.DB, project, workspace string) {
	t.Helper()
	commit := fixtureGit(t, workspace, "rev-parse", "refs/heads/main")
	if _, recorded, err := wikitest.RecordCurrentWiki(context.Background(), db, project, commit, time.Now()); err != nil || !recorded {
		t.Fatalf("record the current wiki of %s: recorded=%v err=%v", project, recorded, err)
	}
}

// openDB opens the spine directly for state assertions and test fixture
// surgery. The test is the operator here; the Docker e2e alone forces the
// spine onto a volume.
func openDB(t *testing.T, spine string) *sql.DB {
	t.Helper()
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatalf("open spine: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func queryStr(t *testing.T, db *sql.DB, q string, args ...any) string {
	t.Helper()
	var s sql.NullString
	if err := db.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	if !s.Valid {
		return "<NULL>"
	}
	return s.String
}

func queryInt(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return n
}

func queryString(t *testing.T, db *sql.DB, q string, args ...any) string {
	t.Helper()
	var s string
	if err := db.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return s
}

// runJSONEnv materializes a run.json identity file and returns the env pair
// selecting it (MC_RUN_JSON is the CLI test tier's stand-in for the fixed
// /mc/run.json mount).
func runJSONEnv(t *testing.T, spine, runID, tier, role string) []string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "run.json")
	b, _ := json.Marshal(map[string]any{"run_id": runID, "tier": tier, "role": role})
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return append(spineEnv(spine), "MC_RUN_JSON="+p)
}

func collieJSONEnv(t *testing.T, spine, sessionID string, allowlist []string) []string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "run.json")
	b, _ := json.Marshal(map[string]any{
		"run_id": sessionID, "tier": "collie", "role": "collie",
		"verb_allowlist": allowlist,
	})
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return append(spineEnv(spine), "MC_RUN_JSON="+p)
}

// proposalTask creates a proposal for tests of Arbiter decisions and dispatch.
func proposalTask(t *testing.T, spine, title string) int64 {
	t.Helper()
	tx, err := openDB(t, spine).BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	id, err := domain.BirthProposal(context.Background(), tx, domain.ProposalArgs{
		Title: title, Project: "ws-test", Origin: "user",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return id
}

// dispatch invokes mc dispatch and returns the tick JSON: one
// `{"action":"tick","actions":[...],"idle":[...]}` object per invocation.
func dispatch(t *testing.T, spine string) map[string]any {
	t.Helper()
	res := runMC(t, spineEnv(spine), "", "dispatch")
	if res.code != 0 {
		t.Fatalf("dispatch failed (%d): %s", res.code, res.stderr)
	}
	if res.json["action"] != "tick" {
		t.Fatalf("dispatch output = %v, want a tick", res.json)
	}
	return res.json
}

// tickActions returns the tick's committed actions in order.
func tickActions(t *testing.T, tick map[string]any) []map[string]any {
	t.Helper()
	if tick["action"] != "tick" {
		t.Fatalf("not a tick: %v", tick)
	}
	raw, ok := tick["actions"].([]any)
	if !ok {
		t.Fatalf("tick actions = %v, want a list", tick["actions"])
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		eff, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("tick action %v is not an object", item)
		}
		out = append(out, eff)
	}
	return out
}

// tickOnlyAction asserts the tick committed exactly one action and returns it.
func tickOnlyAction(t *testing.T, tick map[string]any) map[string]any {
	t.Helper()
	actions := tickActions(t, tick)
	if len(actions) != 1 {
		t.Fatalf("tick actions = %v, want exactly one (tick %v)", actions, tick)
	}
	return actions[0]
}

// tickFindAction returns the tick's first action with the given action name.
func tickFindAction(t *testing.T, tick map[string]any, action string) (map[string]any, bool) {
	t.Helper()
	for _, eff := range tickActions(t, tick) {
		if eff["action"] == action {
			return eff, true
		}
	}
	return nil, false
}

// tickIdle returns the tick's idle Projects as project → reason.
func tickIdle(t *testing.T, tick map[string]any) map[string]string {
	t.Helper()
	if tick["action"] != "tick" {
		t.Fatalf("not a tick: %v", tick)
	}
	raw, ok := tick["idle"].([]any)
	if !ok {
		t.Fatalf("tick idle = %v, want a list", tick["idle"])
	}
	out := map[string]string{}
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("tick idle row %v is not an object", item)
		}
		ws, _ := row["project"].(string)
		reason, _ := row["reason"].(string)
		out[ws] = reason
	}
	return out
}

// assertTickIdleOnly asserts the tick committed nothing and idled the one
// Project with the given reason.
func assertTickIdleOnly(t *testing.T, tick map[string]any, project, reason string) {
	t.Helper()
	if actions := tickActions(t, tick); len(actions) != 0 {
		t.Fatalf("tick committed %v, want nothing (tick %v)", actions, tick)
	}
	idle := tickIdle(t, tick)
	if len(idle) != 1 || idle[project] != reason {
		t.Fatalf("tick idle = %v, want {%s: %s}", idle, project, reason)
	}
}

// dispatchExpect asserts the tick's only action has the given action name and
// returns that action after the simulated resident confirms any container stop.
func dispatchExpect(t *testing.T, spine, action string) map[string]any {
	t.Helper()
	eff := tickOnlyAction(t, dispatch(t, spine))
	if eff["action"] != action {
		t.Fatalf("dispatch action = %v, want %q (effect %v)", eff["action"], action, eff)
	}
	if action == "reap" {
		stopped := runMC(t, spineEnv(spine), "", "run", "stopped", eff["run_id"].(string))
		if stopped.code != 0 {
			t.Fatalf("resident stop confirmation failed: %s", stopped.stderr)
		}
	}
	return eff
}

// ---------------------------------------------------------------------------
// mc __init
// ---------------------------------------------------------------------------

func TestInit(t *testing.T) {
	t.Run("provisions_spine_and_tunables", func(t *testing.T) {
		spine := initSpine(t,
			"--timeout-minutes", "5", "--grace-minutes", "1",
			"--heartbeat-interval-s", "1", "--spawn-grace-s", "5",
			"--hard-deadline-minutes", "120",
			"--review-wip-cap", "4", "--collie-idle-timeout-s", "900")
		db := openDB(t, spine)
		if n := queryInt(t, db, `SELECT COUNT(*) FROM meta`); n != 1 {
			t.Fatalf("meta rows = %d, want 1", n)
		}
		if ws := queryStr(t, db, `SELECT id FROM projects`); ws != "ws-test" {
			t.Fatalf("project = %q", ws)
		}
		if p := queryStr(t, db, `SELECT runtime_auth_delivery FROM sandbox_profiles`); p != "projection" {
			t.Fatalf("runtime_auth_delivery = %q, want projection (the default)", p)
		}
		for col, want := range map[string]int{
			"timeout_minutes": 5, "grace_minutes": 1,
			"heartbeat_interval_s": 1, "spawn_grace_s": 5,
			"hard_deadline_minutes": 120,
			"review_wip_cap":        4, "collie_idle_timeout_s": 900,
		} {
			if got := queryInt(t, db, `SELECT `+col+` FROM settings WHERE id = 1`); got != want {
				t.Fatalf("settings.%s = %d, want %d", col, got, want)
			}
		}
		// The Project's lease row is born with the Project, free.
		if n := queryInt(t, db, `SELECT COUNT(*) FROM leases WHERE project = 'ws-test' AND run_id IS NULL`); n != 1 {
			t.Fatalf("free lease rows for ws-test = %d, want 1", n)
		}

		// `mc settings get` is the read of the same values.
		settings := runMC(t, spineEnv(spine), "", "settings", "get")
		if settings.code != 0 {
			t.Fatalf("settings get failed (%d): %s", settings.code, settings.stderr)
		}
		wantSettings := map[string]any{
			"timeout_minutes": float64(5), "grace_minutes": float64(1),
			"heartbeat_interval_s": float64(1), "spawn_grace_s": float64(5),
			"hard_deadline_minutes": float64(120), "review_wip_cap": float64(4),
			"collie_idle_timeout_s": float64(900), "wiki_update_interval_s": float64(3600),
		}
		if !reflect.DeepEqual(settings.json, wantSettings) {
			t.Fatalf("settings get = %v, want %v", settings.json, wantSettings)
		}
	})

	t.Run("reinit_fails_loudly", func(t *testing.T) {
		spine := initSpine(t)
		res := runMC(t, spineEnv(spine), "", "__init", "--spine", spine,
			"--project", "ws-test", "--workspace-root", "/tmp/x")
		if res.code != 1 {
			t.Fatalf("re-init exit = %d, want 1 (provisioning happens exactly once)", res.code)
		}
	})

	t.Run("missing_flags_are_usage_errors", func(t *testing.T) {
		res := runMC(t, nil, "", "__init", "--spine", filepath.Join(t.TempDir(), "s.db"))
		if res.code != 2 {
			t.Fatalf("exit = %d, want 2", res.code)
		}
	})
}

// ---------------------------------------------------------------------------
// mc task add / get, mc packet list
// ---------------------------------------------------------------------------

func TestTaskAddAndGet(t *testing.T) {
	spine := initSpine(t)

	t.Run("creates_seeded_operator_work", func(t *testing.T) {
		created := runMC(t, spineEnv(spine), "", "task", "add", "first user task", "--project", "ws-test")
		if created.code != 0 {
			t.Fatalf("task add: %s", created.stderr)
		}
		id := int64(created.json["task_id"].(float64))
		res := runMC(t, spineEnv(spine), "", "task", "get", fmt.Sprint(id))
		if res.code != 0 {
			t.Fatalf("task get failed: %s", res.stderr)
		}
		for k, want := range map[string]any{
			"status": "seeded", "origin": "user",
			"priority": float64(2), "project": "ws-test",
		} {
			if got := res.json[k]; got != want {
				t.Fatalf("task get %s = %v, want %v", k, got, want)
			}
		}
		// The schema trigger gives every task its delivery record at birth.
		delivery, ok := res.json["delivery"].(map[string]any)
		if !ok || delivery["destination_branch"] != "main" || delivery["submitted_commit"] != nil {
			t.Fatalf("task get delivery = %v, want a fresh record on main", res.json["delivery"])
		}
	})

	t.Run("priority_flag_recorded", func(t *testing.T) {
		res := runMC(t, spineEnv(spine), "", "task", "add", "urgent",
			"--project", "ws-test", "--priority", "-1")
		if res.code != 0 {
			t.Fatalf("task add failed: %s", res.stderr)
		}
		id := int64(res.json["task_id"].(float64))
		got := runMC(t, spineEnv(spine), "", "task", "get", fmt.Sprint(id))
		if got.json["priority"] != float64(-1) {
			t.Fatalf("priority = %v, want -1", got.json["priority"])
		}
	})

	tests := []struct {
		name string
		args []string
		want int
	}{
		{"unknown_project_is_domain", []string{"task", "add", "x", "--project", "nope"}, 1},
		{"missing_project_is_usage", []string{"task", "add", "x"}, 2},
		{"missing_title_is_usage", []string{"task", "add", "--project", "ws-test"}, 2},
		{"get_missing_task_is_domain", []string{"task", "get", "999"}, 1},
		{"get_bad_id_is_usage", []string{"task", "get", "abc"}, 2},
		{"unknown_verb_is_usage", []string{"frobnicate"}, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := runMC(t, spineEnv(spine), "", tc.args...)
			if res.code != tc.want {
				t.Fatalf("mc %v exit = %d, want %d (stderr %q)", tc.args, res.code, tc.want, res.stderr)
			}
		})
	}
}

// addProjectProfile registers a sandbox profile over a fresh Git workspace
// and returns the profile id. A second Project takes a workspace of its own:
// two Projects over one workspace root make every mount of it cross-Project.
func addProjectProfile(t *testing.T, spine, project string) string {
	t.Helper()
	profile := project + "-profile"
	if _, err := openDB(t, spine).Exec(
		`INSERT INTO sandbox_profiles (id, workspace_root, artifact_roots)
		 VALUES (?, ?, '[]')`, profile, gitFixtureWorkspace(t)); err != nil {
		t.Fatalf("register sandbox profile %s: %v", profile, err)
	}
	return profile
}

// addProject registers a Project over its own Git workspace through the CLI.
func addProject(t *testing.T, spine, project, title string) mcResult {
	t.Helper()
	add := runMC(t, spineEnv(spine), "", "project", "add", project,
		"--title", title, "--sandbox-profile", addProjectProfile(t, spine, project))
	if add.code != 0 {
		t.Fatalf("project add %s failed: %s", project, add.stderr)
	}
	recordFixtureEnvironment(t, openDB(t, spine), project)
	return add
}

func TestProjectLifecycle(t *testing.T) {
	spine := initSpine(t)
	add := addProject(t, spine, "ws-two", "Second Project")
	advisory, ok := add.json["secrets_advisory"].(string)
	if !ok {
		t.Fatalf("project add omitted the connect-time secrets advisory: %v", add.json)
	}
	for _, required := range []string{
		"read every secret in this workspace and send them anywhere",
		"scoped, expiring, revocable credentials",
		"keep secret files gitignored",
	} {
		if !strings.Contains(advisory, required) {
			t.Errorf("connect-time advisory %q omitted %q", advisory, required)
		}
	}
	if strings.Contains(advisory, "egress_policy") || strings.Contains(advisory, "allowlist") {
		t.Errorf("connect-time advisory retained the deleted egress dial: %q", advisory)
	}
	bad := runMC(t, spineEnv(spine), "", "project", "add", "bad-profile",
		"--title", "Bad", "--sandbox-profile", "missing")
	if bad.code != 1 {
		t.Fatalf("missing profile exit=%d stderr=%q", bad.code, bad.stderr)
	}

	pausedTask := runMC(t, spineEnv(spine), "", "task", "add", "paused task",
		"--project", "ws-two", "--priority", "-1")
	if pausedTask.code != 0 {
		t.Fatalf("paused fixture add: %s", pausedTask.stderr)
	}
	activeID := proposalTask(t, spine, "active task")
	pause := runMC(t, spineEnv(spine), "", "project", "pause", "ws-two")
	if pause.code != 0 {
		t.Fatalf("pause failed: %s", pause.stderr)
	}
	eff := dispatchExpect(t, spine, "spawn")
	pool := eff["pool_ids"].([]any)
	if len(pool) != 1 || int64(pool[0].(float64)) != activeID {
		t.Fatalf("paused Project leaked into Arbiter pool: %v", pool)
	}

	run := eff["run_id"].(string)
	pipelineEnv := runJSONEnv(t, spine, run, "pipeline", "arbiter")
	listed := runMC(t, pipelineEnv, "", "project", "list")
	if listed.code != 0 || len(listed.json["projects"].([]any)) != 2 {
		t.Fatalf("pipeline list code=%d json=%v stderr=%q", listed.code, listed.json, listed.stderr)
	}
	for _, args := range [][]string{
		{"project", "pause", "ws-test"},
		{"project", "archive", "ws-test"},
		{"project", "add", "forged", "--title", "Forged", "--sandbox-profile", "ws-two-profile"},
	} {
		denied := runMC(t, pipelineEnv, "", args...)
		if denied.code != 1 || !strings.Contains(denied.stderr, "operator verb") {
			t.Fatalf("pipeline %v exit=%d stderr=%q", args, denied.code, denied.stderr)
		}
	}

	archive := runMC(t, spineEnv(spine), "", "project", "archive", "ws-two")
	if archive.code != 0 {
		t.Fatalf("archive failed: %s", archive.stderr)
	}
	db := openDB(t, spine)
	if got := queryStr(t, db, `SELECT status FROM projects WHERE id='ws-two'`); got != "archived" {
		t.Fatalf("project status = %q", got)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM projects WHERE id='bad-profile'`); got != 0 {
		t.Fatalf("invalid profile left %d project rows", got)
	}
}

func TestScopeRefusalPrecedesSpineOpen(t *testing.T) {
	// Every refusal precedes spine mutation. A pipeline
	// caller pointed at a fresh MC_SPINE path must be refused before the
	// database file (or -wal/-shm) is created.
	for _, args := range [][]string{
		{"delivery", "record", "--operation", "cleanup", "--run", "r-probe", "--result", "{}"},
		{"delivery", "register"},
		{"delivery", "start", "--request", "-"},
		{"delivery", "heartbeat", "--request", "-"},
		{"delivery", "fail", "--run", "r-probe", "--condition", "The copy failed."},
		{"outbox", "poll", "--surface", "dashboard"},
		{"outbox", "ack", "1", "--surface", "dashboard"},
	} {
		probe := filepath.Join(t.TempDir(), "probe.db")
		res := runMC(t, runJSONEnv(t, probe, "r-probe", "pipeline", "arbiter"), "", args...)
		if res.code != 1 {
			t.Fatalf("%v pipeline exit = %d stderr=%q", args, res.code, res.stderr)
		}
		if _, err := os.Stat(probe); !os.IsNotExist(err) {
			t.Fatalf("%v created spine bytes before the scope refusal (stat err=%v)", args, err)
		}
	}
}

func TestInactiveProjectRefusesNewWork(t *testing.T) {
	spine := initSpine(t)
	addProject(t, spine, "ws-cold", "Cold")
	db := openDB(t, spine)

	for _, status := range []string{"pause", "archive"} {
		if res := runMC(t, spineEnv(spine), "", "project", status, "ws-cold"); res.code != 0 {
			t.Fatalf("project %s failed: %s", status, res.stderr)
		}
		before := queryInt(t, db, `SELECT COUNT(*) FROM tasks`)
		for _, args := range [][]string{
			{"task", "add", "swallowed", "--project", "ws-cold"},
		} {
			res := runMC(t, spineEnv(spine), "", args...)
			if res.code != 1 {
				t.Fatalf("after %s, %v exit = %d stderr=%q", status, args, res.code, res.stderr)
			}
			errObj, ok := res.json["error"].(map[string]any)
			if !ok || errObj["code"] != "project-inactive" {
				t.Fatalf("after %s, %v error JSON = %v", status, args, res.json)
			}
		}
		if got := queryInt(t, db, `SELECT COUNT(*) FROM tasks`); got != before {
			t.Fatalf("after %s, inactive Project swallowed %d rows", status, got-before)
		}
	}
}

func TestCollieProjectPauseArchive(t *testing.T) {
	spine := initSpine(t)
	addProject(t, spine, "ws-collie", "Collie-managed")
	started := runMC(t, spineEnv(spine), "", "collie", "start", "--from", "dashboard:ops")
	if started.code != 0 {
		t.Fatalf("collie start failed: %s", started.stderr)
	}
	session := started.json["session_id"].(string)
	env := collieJSONEnv(t, spine, session, defaultCollieAllowlist)

	pause := runMC(t, env, "", "project", "pause", "ws-collie")
	if pause.code != 0 {
		t.Fatalf("allowlisted Collie project pause failed (%d): %s", pause.code, pause.stderr)
	}
	db := openDB(t, spine)
	if got := queryStr(t, db, `SELECT status FROM projects WHERE id='ws-collie'`); got != "paused" {
		t.Fatalf("after Collie pause status = %q", got)
	}
	archive := runMC(t, env, "", "project", "archive", "ws-collie")
	if archive.code != 0 {
		t.Fatalf("allowlisted Collie project archive failed (%d): %s", archive.code, archive.stderr)
	}
	if got := queryStr(t, db, `SELECT status FROM projects WHERE id='ws-collie'`); got != "archived" {
		t.Fatalf("after Collie archive status = %q", got)
	}

	narrow := runMC(t, spineEnv(spine), "", "collie", "start", "--from", "cli:narrow", "--allow", "task.block")
	if narrow.code != 0 {
		t.Fatalf("narrow collie start failed: %s", narrow.stderr)
	}
	narrowEnv := collieJSONEnv(t, spine, narrow.json["session_id"].(string), []string{"task.block"})
	denied := runMC(t, narrowEnv, "", "project", "pause", "ws-test")
	if denied.code != 1 || !strings.Contains(denied.stderr, "not allowed") {
		t.Fatalf("narrow Collie pause = code %d stderr %q", denied.code, denied.stderr)
	}
	if got := queryStr(t, db, `SELECT status FROM projects WHERE id='ws-test'`); got != "active" {
		t.Fatalf("denied pause mutated ws-test to %q", got)
	}
}

func TestPacketListEmpty(t *testing.T) {
	spine := initSpine(t)
	res := runMC(t, spineEnv(spine), "", "packet", "list")
	if res.code != 0 {
		t.Fatalf("packet list failed: %s", res.stderr)
	}
	if got := res.json["packets"].([]any); len(got) != 0 {
		t.Fatalf("packets = %v, want empty", got)
	}
}

func TestTaskBlockUnblock(t *testing.T) {
	t.Run("host_blocks_and_unblocks_without_moving_status", func(t *testing.T) {
		spine := initSpine(t)
		added := runMC(t, spineEnv(spine), "", "task", "add", "needs a decision", "--project", "ws-test")
		if added.code != 0 {
			t.Fatalf("task creation failed: %s", added.stderr)
		}
		id := int64(added.json["task_id"].(float64))
		res := runMC(t, spineEnv(spine), "", "task", "block", fmt.Sprint(id),
			"--reason", "choose a target")
		if res.code != 0 {
			t.Fatalf("block failed: %s", res.stderr)
		}
		db := openDB(t, spine)
		if got := queryStr(t, db, `SELECT status || '/' || blocked || '/' || blocked_reason FROM tasks WHERE id = ?`, id); got != "seeded/1/choose a target" {
			t.Fatalf("blocked row = %q", got)
		}
		res = runMC(t, spineEnv(spine), "", "task", "unblock", fmt.Sprint(id), "--reason", "The operator selected the target.")
		if res.code != 0 {
			t.Fatalf("unblock failed: %s", res.stderr)
		}
		if got := queryStr(t, db, `SELECT status || '/' || blocked || '/' || COALESCE(blocked_reason, 'none') FROM tasks WHERE id = ?`, id); got != "seeded/0/none" {
			t.Fatalf("unblocked row = %q", got)
		}
	})

	t.Run("pipeline_may_block_only_its_live_subject_and_never_unblock", func(t *testing.T) {
		spine, id, run, env := workerFixture(t, "pipeline block")
		other := proposalTask(t, spine, "not this run's subject")
		res := runMC(t, env, "", "task", "block", fmt.Sprint(other), "--reason", "no")
		if res.code != 1 || !strings.Contains(res.stderr, "own subject") {
			t.Fatalf("cross-subject block exit=%d stderr=%q", res.code, res.stderr)
		}
		res = runMC(t, env, "", "task", "block", fmt.Sprint(id), "--reason", "operator choice")
		if res.code != 0 {
			t.Fatalf("own-subject block failed: %s", res.stderr)
		}
		res = runMC(t, env, "", "task", "unblock", fmt.Sprint(id), "--reason", "The operator selected the target.")
		if res.code != 1 || !strings.Contains(res.stderr, "operator verb") {
			t.Fatalf("pipeline unblock exit=%d stderr=%q", res.code, res.stderr)
		}
		db := openDB(t, spine)
		if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != run {
			t.Fatalf("block released the lease before dispatch could request a stop: %q", got)
		}
		if got := queryInt(t, db, `SELECT blocked FROM tasks WHERE id = ?`, other); got != 0 {
			t.Fatalf("cross-subject refusal blocked other task")
		}
	})

	t.Run("validation", func(t *testing.T) {
		spine := initSpine(t)
		id := proposalTask(t, spine, "validation")
		for _, tc := range []struct {
			args []string
			want int
		}{
			{[]string{"task", "block", fmt.Sprint(id)}, 2},
			{[]string{"task", "unblock", fmt.Sprint(id)}, 2},
			{[]string{"task", "unblock", fmt.Sprint(id), "extra"}, 2},
			{[]string{"task", "block", "bad", "--reason", "x"}, 2},
		} {
			if res := runMC(t, spineEnv(spine), "", tc.args...); res.code != tc.want {
				t.Fatalf("mc %v exit=%d stderr=%q, want %d", tc.args, res.code, res.stderr, tc.want)
			}
		}
	})
}

// TestTaskCancel covers task cancellation after Arbiter promotion and during an assigned run.
func TestTaskCancel(t *testing.T) {
	t.Run("reaches_a_row_with_no_packet_and_no_live_run", func(t *testing.T) {
		spine := initSpine(t)
		db := openDB(t, spine)
		id := proposalTask(t, spine, "a queued row nobody could abandon")
		refused := runMC(t, spineEnv(spine), "", "task", "cancel", fmt.Sprint(id), "--reason", "Withdraw the proposal.")
		if refused.code != 1 || refused.json["error"].(map[string]any)["code"] != "edit-restricted" {
			t.Fatalf("Proposed cancellation: %+v", refused)
		}
		arbiterPromoteAll(t, spine, dispatchThrough(t, spine, "spawn"))

		if bare := runMC(t, spineEnv(spine), "", "task", "cancel", fmt.Sprint(id)); bare.code == 0 {
			t.Fatalf("cancel without --reason succeeded: %v", bare.json)
		}
		res := runMC(t, spineEnv(spine), "", "task", "cancel", fmt.Sprint(id),
			"--reason", "superseded by landed work")
		if res.code != 0 || res.json["cancelled"] != true {
			t.Fatalf("cancel code=%d json=%v stderr=%q", res.code, res.json, res.stderr)
		}
		// Nothing is running, so nothing is owed a stop.
		if _, ok := res.json["stop_pending"]; ok {
			t.Fatalf("cancel of an idle row claimed a pending stop: %v", res.json)
		}
		if got := queryStr(t, db, `SELECT decision || '/' || archived FROM tasks WHERE id=?`, id); got != "cancelled/1" {
			t.Fatalf("cancelled task = %q", got)
		}
		if replay := runMC(t, spineEnv(spine), "", "task", "cancel", fmt.Sprint(id),
			"--reason", "again"); replay.code != 1 {
			t.Fatalf("cancel replay exit=%d stderr=%q", replay.code, replay.stderr)
		}
	})

	t.Run("live_subject_converges_with_interrupt", func(t *testing.T) {
		spine, taskID, run, env := workerFixture(t, "cancel the live subject")
		denied := runMC(t, env, "", "task", "cancel", fmt.Sprint(taskID), "--reason", "no")
		if denied.code != 1 || !strings.Contains(denied.stderr, "operator verb") {
			t.Fatalf("pipeline cancel exit=%d stderr=%q", denied.code, denied.stderr)
		}
		res := runMC(t, spineEnv(spine), "", "task", "cancel", fmt.Sprint(taskID),
			"--reason", "operator scratched it")
		if res.code != 0 || res.json["run_id"] != run || res.json["stop_pending"] != true {
			t.Fatalf("cancel code=%d json=%v stderr=%q", res.code, res.json, res.stderr)
		}
		db := openDB(t, spine)
		// The lease stays HELD; the reap selects on it, exactly as for interrupt.
		if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != run {
			t.Fatalf("cancel released the lease the reap selects on: %q", got)
		}
		reap := dispatchExpect(t, spine, "reap")
		if reap["run_id"] != run || reap["reason"] != "interrupted" || reap["stop_container"] != true {
			t.Fatalf("cancel tick effect = %v", reap)
		}
		// Charges nothing: an operator cancellation is neither infra death nor
		// a quality miss.
		if got := queryStr(t, db, `SELECT dispatch_retries || '/' || blocked FROM tasks WHERE id=?`, taskID); got != "3/0" {
			t.Fatalf("cancel charged the subject: %q", got)
		}
	})

	t.Run("approval_can_cancel_before_application", func(t *testing.T) {
		spine := initSpine(t)
		taskID := proposalTask(t, spine, "approved and waiting for the update")
		packageTask(t, spine, taskID)
		packet := reviewedPacket(t, spine, taskID)
		if res := runMC(t, spineEnv(spine), "", packetDecideArgs(taskID, packet, "approve")...); res.code != 0 {
			t.Fatalf("approve fixture failed: %s", res.stderr)
		}
		res := runMC(t, spineEnv(spine), "", "task", "cancel", fmt.Sprint(taskID), "--reason", "give up")
		if res.code != 0 {
			t.Fatalf("cancel of an approved row exit=%d stderr=%q", res.code, res.stderr)
		}
		db := openDB(t, spine)
		if got := queryStr(t, db, `SELECT decision || '/' || archived FROM tasks WHERE id=?`, taskID); got != "cancelled/1" {
			t.Fatalf("the cancellation left the row at %q", got)
		}
		res = runMC(t, spineEnv(spine), "", "task", "cancel", fmt.Sprint(taskID), "--reason", "another request")
		if res.code != 1 || !strings.Contains(res.stderr, "archived") {
			t.Fatalf("the archived task accepted another cancellation: %v", res)
		}
	})
}

func TestTaskInterrupt(t *testing.T) {
	// The whole hop, because the verb's own result was never the thing that
	// stopped anything: the resident applies effects only from `mc dispatch`'s
	// stdout, so an interrupt is proven only by driving the NEXT TICK and
	// reading the stop out of it. The old test asserted the verb's return shape
	// and passed for as long as the container kept running.
	t.Run("cancellation_is_the_marker_and_the_next_tick_emits_the_stop", func(t *testing.T) {
		spine, taskID, run, _ := workerFixture(t, "interrupt live run")
		res := runMC(t, spineEnv(spine), "", "task", "interrupt", fmt.Sprint(taskID))
		if res.code != 0 || res.json["run_id"] != run ||
			res.json["cancelled"] != true || res.json["stop_pending"] != true {
			t.Fatalf("interrupt code=%d json=%v stderr=%q", res.code, res.json, res.stderr)
		}
		// Not effect-shaped: a result that looks like effect data is what hid
		// the missing stop.
		if _, ok := res.json["action"]; ok {
			t.Fatalf("interrupt result still effect-shaped: %v", res.json)
		}
		if _, ok := res.json["stop_container"]; ok {
			t.Fatalf("interrupt result still claims a stop it cannot perform: %v", res.json)
		}

		db := openDB(t, spine)
		if got := queryStr(t, db, `SELECT decision || '/' || archived FROM tasks WHERE id=?`, taskID); got != "cancelled/1" {
			t.Fatalf("interrupted task = %q", got)
		}
		// The lease stays HELD — it is what the reap selects on. Releasing it
		// here is precisely what left no path to a stop.
		if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != run {
			t.Fatalf("interrupt released the lease the reap selects on: %q", got)
		}
		if got := queryStr(t, db, `SELECT COALESCE(outcome,'<live>') FROM runs WHERE id=?`, run); got != "<live>" {
			t.Fatalf("interrupt ended the run itself: %q", got)
		}
		replay := runMC(t, spineEnv(spine), "", "task", "interrupt", fmt.Sprint(taskID))
		if replay.code != 1 {
			t.Fatalf("interrupt replay exit=%d stderr=%q", replay.code, replay.stderr)
		}

		reap := dispatchExpect(t, spine, "reap")
		if reap["run_id"] != run || reap["reason"] != "interrupted" || reap["stop_container"] != true {
			t.Fatalf("interrupt tick effect = %v", reap)
		}
		if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != "<NULL>" {
			t.Fatalf("interrupt reap left lease held: %q", got)
		}
		if got := queryStr(t, db, `SELECT outcome FROM runs WHERE id=?`, run); got != "interrupted" {
			t.Fatalf("interrupted run outcome = %q", got)
		}
		// Charges nothing: the operator cancelled the work, which is neither an
		// infra death nor a quality miss, and the row is archived anyway.
		if got := queryStr(t, db, `SELECT dispatch_retries || '/' || blocked FROM tasks WHERE id=?`, taskID); got != "3/0" {
			t.Fatalf("interrupt charged the subject: %q", got)
		}
	})

	t.Run("wrong_subject_and_pipeline_provenance_are_inert", func(t *testing.T) {
		spine, taskID, run, env := workerFixture(t, "protected live run")
		other := proposalTask(t, spine, "not in flight")
		wrong := runMC(t, spineEnv(spine), "", "task", "interrupt", fmt.Sprint(other))
		if wrong.code != 1 || !strings.Contains(wrong.stderr, "live lease") {
			t.Fatalf("wrong-subject exit=%d stderr=%q", wrong.code, wrong.stderr)
		}
		denied := runMC(t, env, "", "task", "interrupt", fmt.Sprint(taskID))
		if denied.code != 1 || !strings.Contains(denied.stderr, "operator verb") {
			t.Fatalf("pipeline interrupt exit=%d stderr=%q", denied.code, denied.stderr)
		}
		db := openDB(t, spine)
		if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != run {
			t.Fatalf("refusals disturbed lease: %q", got)
		}
		if got := queryStr(t, db, `SELECT decision FROM tasks WHERE id=?`, other); got != "<NULL>" {
			t.Fatalf("wrong-subject interrupt decided other task: %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// The pipeline walk: the full pipeline minus Docker — every stage advanced
// by a fresh `mc dispatch` against a temp spine, every terminal written by
// the role verb through the real CLI.
// ---------------------------------------------------------------------------

func TestPipelineWalk(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	taskID := proposalTask(t, spine, "walk the skeleton")

	// Stage: Arbiter spawn — pool snapshot, lease held, runs row committed
	// before any process exists.
	eff := dispatchExpect(t, spine, "spawn")
	if eff["role"] != "arbiter" {
		t.Fatalf("first spawn role = %v, want arbiter", eff["role"])
	}
	arbiterRun := eff["run_id"].(string)
	if pool := eff["pool_ids"].([]any); len(pool) != 1 || pool[0] != float64(taskID) {
		t.Fatalf("pool_ids = %v, want [%d]", pool, taskID)
	}
	if eff["session_path"] != "sessions/"+arbiterRun {
		t.Fatalf("session_path = %v", eff["session_path"])
	}
	if hb := eff["heartbeat_interval_s"]; hb != float64(60) {
		t.Fatalf("heartbeat_interval_s = %v, want the lock default 60", hb)
	}
	if got := queryStr(t, db, `SELECT owner FROM leases WHERE project = 'ws-test'`); got != "arbiter" {
		t.Fatalf("leases.owner = %q", got)
	}
	if got := queryStr(t, db, `SELECT pool_snapshot FROM runs WHERE id = ?`, arbiterRun); got != fmt.Sprintf("[%d]", taskID) {
		t.Fatalf("pool_snapshot = %q", got)
	}

	// A held, fresh lease idles the Project on the next tick.
	assertTickIdleOnly(t, dispatch(t, spine), "ws-test", "lease-held")

	// Arbiter terminal: batch promote over exactly the snapshotted pool.
	arbiterEnv := runJSONEnv(t, spine, arbiterRun, "pipeline", "arbiter")
	res := runMC(t, arbiterEnv, fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"promote","reason":"solid"}]}`, taskID),
		"arbiter", "decide", "--run", arbiterRun, "--batch", "-")
	if res.code != 0 {
		t.Fatalf("arbiter decide failed (%d): %s", res.code, res.stderr)
	}
	if got := queryStr(t, db, `SELECT status FROM tasks WHERE id = ?`, taskID); got != "seeded" {
		t.Fatalf("task status = %q, want seeded", got)
	}
	if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != "<NULL>" {
		t.Fatalf("lease not released after arbiter terminal: %q", got)
	}
	if got := queryStr(t, db, `SELECT ended_at FROM runs WHERE id = ?`, arbiterRun); got == "<NULL>" {
		t.Fatalf("runs.ended_at not stamped")
	}

	// Stage: Worker spawn. The spawn carries the repository plan, and the
	// resident readies the isolated repository before the container starts.
	eff = dispatchExpect(t, spine, "spawn")
	if eff["role"] != "worker" || eff["subject_id"] != float64(taskID) {
		t.Fatalf("spawn = %v, want worker on task %d", eff, taskID)
	}
	workerRun := eff["run_id"].(string)
	plan := eff["mount_plan"].(map[string]any)["repository"].(map[string]any)
	if plan["task_id"] != float64(taskID) || plan["destination_branch"] != "main" {
		t.Fatalf("repository plan = %v, want task %d on main", plan, taskID)
	}
	workerRoot := prepareRepositoryRun(t, spine, eff)
	workerEnv, _ := roleEnv(t, spine, workerRun, "worker", workerRoot)
	workspace := queryStr(t, db, `SELECT workspace_root FROM sandbox_profiles WHERE id = 'default'`)
	mainBefore := fixtureGit(t, workspace, "rev-parse", "main")
	if got := queryStr(t, db, `SELECT input_commit || '/' || destination_commit FROM repository_runs WHERE run_id = ?`, workerRun); got != mainBefore+"/"+mainBefore {
		t.Fatalf("repository_runs input/destination = %q, want main %s twice", got, mainBefore)
	}
	if got := fixtureGit(t, filepath.Join(workerRoot, "repo"), "rev-parse", "HEAD"); got != mainBefore {
		t.Fatalf("isolated repository HEAD = %s, want main %s", got, mainBefore)
	}

	// Runner lifecycle: heartbeat advances last_heartbeat_at, never the hard
	// deadline; register-session records the locators.
	deadlineBefore := queryStr(t, db, `SELECT deadline_uptime_ms FROM leases WHERE project = 'ws-test'`)
	hb := runMC(t, workerEnv, "", "heartbeat", workerRun)
	if hb.code != 0 {
		t.Fatalf("heartbeat failed: %s", hb.stderr)
	}
	if got := queryStr(t, db, `SELECT heartbeat_uptime_ms FROM leases WHERE project = 'ws-test'`); got == "<NULL>" {
		t.Fatalf("heartbeat did not stamp heartbeat_uptime_ms")
	}
	if got := queryStr(t, db, `SELECT deadline_uptime_ms FROM leases WHERE project = 'ws-test'`); got != deadlineBefore {
		t.Fatalf("heartbeat moved deadline_uptime_ms %q → %q", deadlineBefore, got)
	}
	if res := runMC(t, workerEnv, "", "heartbeat", "not-the-run"); res.code != 1 {
		t.Fatalf("stale heartbeat exit = %d, want 1", res.code)
	}
	if res := runMC(t, workerEnv, "", "run", "model-response", workerRun); res.code != 0 || !strings.Contains(res.stdout, `"recovered":false`) {
		t.Fatalf("The model response command failed: %+v", res)
	}
	if res := runMC(t, workerEnv, "", "run", "model-response", "another-run"); res.code != 1 {
		t.Fatalf("The model response command accepted another run: %+v", res)
	}
	rs := runMC(t, workerEnv, "", "run", "register-session", workerRun,
		"--native-ref", "fake-session", "--file", "native.jsonl")
	if rs.code != 0 {
		t.Fatalf("register-session failed: %s", rs.stderr)
	}
	if got := queryStr(t, db, `SELECT native_session_ref FROM runs WHERE id = ?`, workerRun); got != "fake-session" {
		t.Fatalf("native_session_ref = %q", got)
	}
	if replay := runMC(t, workerEnv, "", "run", "register-session", workerRun,
		"--native-ref", "fake-session", "--file", "native.jsonl"); replay.code != 0 {
		t.Fatalf("same-value locator replay exit=%d stderr=%q", replay.code, replay.stderr)
	}
	if conflict := runMC(t, workerEnv, "", "run", "register-session", workerRun,
		"--native-ref", "different-session", "--file", "other.jsonl"); conflict.code != 1 {
		t.Fatalf("conflicting locator rewrite exit=%d stderr=%q", conflict.code, conflict.stderr)
	}
	if got := queryStr(t, db, `SELECT native_session_ref || '/' || trace_filename FROM runs WHERE id = ?`, workerRun); got != "fake-session/native.jsonl" {
		t.Fatalf("conflicting registration rewrote locators to %q", got)
	}
	if res := runMC(t, workerEnv, "", "run", "register-session", "not-the-run",
		"--native-ref", "x", "--file", "y"); res.code != 1 {
		t.Fatalf("unknown-run register-session exit = %d, want 1", res.code)
	}

	// Worker terminal: the Worker submits the exact HEAD commit of its
	// isolated repository. The submission records the version; the task moves
	// to worked when the resident captures it. Complete never dispatches: no
	// effect data, and the lease is free.
	commit := commitContribution(t, workerRoot, "walk.txt", "walked\n")
	runsBefore := queryInt(t, db, `SELECT COUNT(*) FROM runs`)
	res = runMC(t, workerEnv, "", "complete", fmt.Sprint(taskID),
		"--run", workerRun, "--status", "worked", "--commit", commit)
	if res.code != 0 {
		t.Fatalf("complete worked failed (%d): %s", res.code, res.stderr)
	}
	if _, has := res.json["action"]; has {
		t.Fatalf("complete returned effect data %v; it must never dispatch", res.json)
	}
	if res.json["submitted"] != true {
		t.Fatalf("complete worked = %v, want submitted", res.json)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM runs`); got != runsBefore {
		t.Fatalf("complete changed runs count %d to %d; it dispatched", runsBefore, got)
	}
	if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != "<NULL>" {
		t.Fatalf("lease not released by complete: %q", got)
	}
	if got := queryStr(t, db, `SELECT status FROM tasks WHERE id = ?`, taskID); got != "seeded" {
		t.Fatalf("task status after the submission = %q, want seeded until the capture", got)
	}
	if got := queryStr(t, db, `SELECT input_commit || '/' || result_commit FROM version_submissions WHERE run_id = ?`, workerRun); got != mainBefore+"/"+commit {
		t.Fatalf("version submission = %q, want %s/%s", got, mainBefore, commit)
	}

	// Replay of the same terminal is fenced off: the lease is gone.
	res = runMC(t, workerEnv, "", "complete", fmt.Sprint(taskID),
		"--run", workerRun, "--status", "worked", "--commit", commit)
	if res.code != 1 {
		t.Fatalf("replayed complete exit = %d, want 1 (stale-run fencing)", res.code)
	}

	// A start request captures the submitted version under a mechanical run,
	// and the next start request cleans the isolated repository. The capture
	// moves the task to worked and retains the commit in the Project repository.
	capture := startExpect(t, spine, "capture")[0]
	if capture["producer_run"] != workerRun || capture["result_commit"] != commit {
		t.Fatalf("capture execution = %v", capture)
	}
	runExecution(t, spine, capture)
	if got := queryStr(t, db, `SELECT status || '/' || completion_sha FROM tasks WHERE id = ?`, taskID); got != "worked/"+commit {
		t.Fatalf("task after capture = %q, want worked/%s", got, commit)
	}
	if got := queryStr(t, db, `SELECT v.result_commit || '/' || v.retained_ref FROM tasks d JOIN task_versions v ON v.id = d.submitted_version WHERE d.id = ?`, taskID); got != fmt.Sprintf("%s/refs/mc/tasks/%d/versions/%s", commit, taskID, workerRun) {
		t.Fatalf("submitted version = %q", got)
	}
	if got := fixtureGit(t, workspace, "rev-parse", fmt.Sprintf("refs/mc/tasks/%d/versions/%s", taskID, workerRun)); got != commit {
		t.Fatalf("retained ref = %s, want %s", got, commit)
	}
	if got := queryStr(t, db, `SELECT outcome FROM runs WHERE id = ?`, capture["run_id"]); got != "completed" {
		t.Fatalf("capture run outcome = %q", got)
	}
	cleanup := startExpect(t, spine, "cleanup")[0]
	if cleanup["producer_run"] != workerRun {
		t.Fatalf("cleanup execution = %v", cleanup)
	}
	runExecution(t, spine, cleanup)
	if _, err := os.Stat(workerRoot); !os.IsNotExist(err) {
		t.Fatalf("cleanup left the isolated repository at %s (err %v)", workerRoot, err)
	}
	if got := queryInt(t, db, `SELECT cleaned FROM repository_runs WHERE run_id = ?`, workerRun); got != 1 {
		t.Fatalf("repository run not cleaned")
	}

	// register-session is NOT lease-fenced; it writes the run's own row. The
	// runner fires it at session-start, which can lose the race against the
	// behavior's terminal verb releasing the lease — the own-row write must
	// still land after release, or the locators are silently lost forever.
	rs = runMC(t, workerEnv, "", "run", "register-session", workerRun,
		"--native-ref", "fake-session", "--file", "native.jsonl")
	if rs.code != 0 {
		t.Fatalf("register-session after lease release exit = %d, want 0 (own-row identity, not fencing): %s", rs.code, rs.stderr)
	}

	// Stage: Verifier. Its isolated repository starts at the submitted
	// commit, and the verdict names that exact commit.
	eff, verifierRoot := spawnRole(t, spine, "verifier")
	verifierRun := eff["run_id"].(string)
	if got := fixtureGit(t, filepath.Join(verifierRoot, "repo"), "rev-parse", "HEAD"); got != commit {
		t.Fatalf("Verifier repository HEAD = %s, want the submitted commit %s", got, commit)
	}
	verifierEnv, verifierPlane := roleEnv(t, spine, verifierRun, "verifier", verifierRoot)
	evidence := writePlaneFile(t, verifierPlane, fmt.Sprintf("%d/verification/gate-ladder.md", taskID),
		"Criterion 1: PASS. The file walk.txt exists with the requested content.\n")
	res = runMC(t, verifierEnv, "", "verifier", "verdict", fmt.Sprint(taskID),
		"--run", verifierRun, "--outcome", "pass",
		"--evidence", evidence, "--sha", commit)
	if res.code != 0 {
		t.Fatalf("verifier verdict failed (%d): %s", res.code, res.stderr)
	}
	if got := queryStr(t, db, `SELECT status || '/' || (SELECT result_commit FROM task_versions WHERE id=tasks.source_version) FROM tasks WHERE id = ?`, taskID); got != "verified/"+commit {
		t.Fatalf("task = %q, want verified/%s", got, commit)
	}
	if got := queryStr(t, db, `SELECT outcome FROM version_verdicts WHERE run_id = ?`, verifierRun); got != "pass" {
		t.Fatalf("version verdict = %q", got)
	}

	// Stage: Packager. A verified task gets the Packager at the next dispatch,
	// and its isolated repository starts at the commit that the Verifier
	// passed. The packaged terminal records the report; the packet is
	// published only after the merge preparation checks the version.
	eff, packagerRoot := spawnRole(t, spine, "packager")
	packagerRun := eff["run_id"].(string)
	if got := fixtureGit(t, filepath.Join(packagerRoot, "repo"), "rev-parse", "HEAD"); got != commit {
		t.Fatalf("Packager repository HEAD = %s, want the verified commit %s", got, commit)
	}
	packagerRunEnv, packagerPlane := roleEnv(t, spine, packagerRun, "packager", packagerRoot)
	render := writePlaneFile(t, packagerPlane, fmt.Sprintf("%d/packet/render.html", taskID), "<h1>review packet</h1>\n")
	res = runMC(t, packagerRunEnv, "", "complete", fmt.Sprint(taskID),
		"--run", packagerRun, "--status", "packaged", "--outputs", render, "--thesis", "ships the thing")
	if res.code != 0 {
		t.Fatalf("complete packaged failed (%d): %s", res.code, res.stderr)
	}
	if got := queryStr(t, db, `SELECT status FROM tasks WHERE id = ?`, taskID); got != "packaged" {
		t.Fatalf("task after packaging = %q", got)
	}
	pl := runMC(t, spineEnv(spine), "", "packet", "list")
	if packets := pl.json["packets"].([]any); len(packets) != 0 {
		t.Fatalf("packets before the merge preparation = %v, want none", packets)
	}

	// One start request prepares the merge candidate under a mechanical run
	// and cleans the Packager repository. The merged candidate publishes the
	// review packet at once.
	started := startExpect(t, spine, "prepare", "cleanup")
	prepare, packagerCleanup := started[0], started[1]
	if prepare["submitted_commit"] != commit || prepare["task_id"] != float64(taskID) {
		t.Fatalf("prepare execution = %v", prepare)
	}
	if packagerCleanup["producer_run"] != packagerRun {
		t.Fatalf("Packager cleanup execution = %v", packagerCleanup)
	}
	runExecution(t, spine, prepare)
	runExecution(t, spine, packagerCleanup)
	prepareCleanup := startExpect(t, spine, "cleanup")[0]
	if prepareCleanup["producer_run"] != prepare["run_id"] {
		t.Fatalf("prepare cleanup execution = %v", prepareCleanup)
	}
	runExecution(t, spine, prepareCleanup)
	if got := queryStr(t, db, `SELECT c.status || '/' || c.destination_commit FROM tasks d JOIN merge_candidates c ON c.id = d.candidate_id WHERE d.id = ?`, taskID); got != "merged/"+mainBefore {
		t.Fatalf("merge candidate = %q, want merged on main %s", got, mainBefore)
	}
	pl = runMC(t, spineEnv(spine), "", "packet", "list")
	packets := pl.json["packets"].([]any)
	if len(packets) != 1 {
		t.Fatalf("packets = %v, want exactly one after publication", packets)
	}
	packet := packets[0].(map[string]any)
	if packet["task_id"] != float64(taskID) || packet["archived"] != float64(0) || packet["ready"] != float64(1) {
		t.Fatalf("packet = %v", packet)
	}
	if packet["submitted_commit"] != commit || packet["destination_branch"] != "main" {
		t.Fatalf("packet version = %v/%v, want %s on main", packet["submitted_commit"], packet["destination_branch"], commit)
	}
	requireArtifactSnapshot(t, render, fmt.Sprint(packet["render_path"]))
	// The thesis reaches the queue card through this column and no other.
	if packet["thesis"] != "ships the thing" {
		t.Fatalf("thesis = %v, want the Packager's own claim", packet["thesis"])
	}
	if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != "<NULL>" {
		t.Fatalf("the review publication left the lease held: %q", got)
	}

	// Board drained: the tick spawns Strategist; the fake terminal
	// is the empty batch.
	eff = dispatchExpect(t, spine, "spawn")
	if eff["role"] != "strategist(propose)" {
		t.Fatalf("drained-board spawn role = %v, want strategist(propose)", eff["role"])
	}
	if eff["subject_id"] != nil {
		t.Fatalf("strategist(propose) subject_id = %v, want null (subjectless lease)", eff["subject_id"])
	}
	strategistRun := eff["run_id"].(string)
	strategistEnv := runJSONEnv(t, spine, strategistRun, "pipeline", "strategist(propose)")
	tasksBefore := queryInt(t, db, `SELECT COUNT(*) FROM tasks`)
	res = runMC(t, strategistEnv, `{"proposals":[]}`,
		"strategist", "propose", "--run", strategistRun, "--batch", "-")
	if res.code != 0 {
		t.Fatalf("strategist propose failed (%d): %s", res.code, res.stderr)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM tasks`); got != tasksBefore {
		t.Fatalf("empty batch grew the board %d to %d", tasksBefore, got)
	}
	if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != "<NULL>" {
		t.Fatalf("subjectless lease not released: %q", got)
	}

	// The operator approves the reviewed packet and version. Approve is a
	// pure state write: decision set, status still packaged, nothing archived.
	res = runMC(t, spineEnv(spine), "", packetDecideArgs(taskID, packet, "approve")...)
	if res.code != 0 {
		t.Fatalf("packet decide --approve failed (%d): %s", res.code, res.stderr)
	}
	if res.json["archived"] != false {
		t.Fatalf("approve archived synchronously: %v", res.json)
	}
	tg := runMC(t, spineEnv(spine), "", "task", "get", fmt.Sprint(taskID))
	if tg.json["decision"] != "approved" || tg.json["status"] != "packaged" || tg.json["archived"] != float64(0) {
		t.Fatalf("post-approve task = %v", tg.json)
	}

	// The next start request authorizes the update of main under a mechanical run.
	apply := startExpect(t, spine, "apply")[0]
	if apply["task_id"] != float64(taskID) || apply["submitted_commit"] != commit {
		t.Fatalf("apply execution = %v", apply)
	}
	runExecution(t, spine, apply)
	tg = runMC(t, spineEnv(spine), "", "task", "get", fmt.Sprint(taskID))
	if tg.json["archived"] != float64(1) || tg.json["decision"] != "approved" {
		t.Fatalf("post-apply task = %v", tg.json)
	}
	delivery := tg.json["delivery"].(map[string]any)
	if delivery["submitted_commit"] != commit || delivery["destination_branch"] != "main" {
		t.Fatalf("post-apply delivery = %v", delivery)
	}
	pl = runMC(t, spineEnv(spine), "", "packet", "list")
	if got := pl.json["packets"].([]any)[0].(map[string]any)["archived"]; got != float64(1) {
		t.Fatalf("packet not cascaded to archived: %v", got)
	}
	if got := fixtureGit(t, workspace, "merge-base", "--is-ancestor", commit, "main"); got != "" {
		t.Fatalf("the submitted commit did not reach main: %s", got)
	}
	if got := fixtureGit(t, workspace, "show", "main:walk.txt"); got != "walked" {
		t.Fatalf("main:walk.txt = %q", got)
	}
	if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != "<NULL>" {
		t.Fatalf("the apply left the lease held: %q", got)
	}
}

// ---------------------------------------------------------------------------
// CAS claim: two concurrent claimants, exactly one winner.
// ---------------------------------------------------------------------------

func TestDispatchWaitsForProcessFlock(t *testing.T) {
	spine := initSpine(t)
	proposalTask(t, spine, "flock-serialized task")
	lockPath := filepath.Join(filepath.Dir(spine), "mc.dispatch.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lockFile.Close()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		}
	}()

	cmd := exec.Command(mcBin, "dispatch")
	cmd.Env = hermeticEnv(spineEnv(spine)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		t.Fatalf("dispatch exited while mc.dispatch.lock was held: err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	case <-time.After(150 * time.Millisecond):
		// Still blocked before evaluation: no Run row can have opened.
	}
	db := openDB(t, spine)
	if n := queryInt(t, db, `SELECT COUNT(*) FROM runs WHERE role IS NOT 'scribe'`); n != 0 {
		t.Fatalf("dispatch evaluated while process flock was held: runs=%d", n)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	locked = false

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("dispatch after flock release: %v stderr=%q", err, stderr.String())
		}
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("dispatch did not resume after process flock release")
	}
	var tick map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &tick); err != nil {
		t.Fatalf("dispatch output %q: %v", stdout.String(), err)
	}
	if effect := tickOnlyAction(t, tick); effect["action"] != "spawn" {
		t.Fatalf("post-flock effect = %v, want spawn", effect)
	}
}

func TestDispatchRoutingResolution(t *testing.T) {
	writeRouting := func(t *testing.T, spine, body string) {
		t.Helper()
		db := openDB(t, spine)
		cfg, err := operatorconfig.Load(filepath.Join(filepath.Dir(spine), "config.toml"))
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range routing.ConfiguredBindings(cfg.Runtimes) {
			operation := "connection_access"
			if strings.HasSuffix(spec.AuthKind, "oauth") {
				operation = "token_refresh"
			}
			if _, err := db.Exec(`INSERT OR IGNORE INTO connection_health(connection_id,operation,authorization_id,state,verified_at) VALUES(?,?,'unavailable','ready',datetime('now'))`, spec.Credential, operation); err != nil {
				t.Fatal(err)
			}
		}

		if err := os.WriteFile(filepath.Join(filepath.Dir(spine), "routing.md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("canonical_route_stamps_resolved_harness_and_binding", func(t *testing.T) {
		spine := initSpine(t)
		writeRouting(t, spine, defaultRoutingMarkdown)
		id := proposalTask(t, spine, "already edited")
		db := openDB(t, spine)
		if _, err := db.Exec(`UPDATE tasks SET status='seeded' WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
		eff := dispatchExpect(t, spine, "spawn")
		if eff["role"] != "worker" || eff["harness"] != "claude-sdk" || eff["model_binding"] != "endpoint" {
			t.Fatalf("resolved spawn = %v", eff)
		}
		run := eff["run_id"].(string)
		if got := queryStr(t, db, `SELECT binding FROM runs WHERE id=?`, run); got != "claude-sdk/endpoint" {
			t.Fatalf("runs.binding = %q", got)
		}
	})

	// A routing failure at attest is a deployment-health refusal
	// (health.routing_invalid): one dispatch.health action under a derived
	// dispatch_key, no Run, no claim, no task blame, exit 0 with the terminal
	// refused effect as the Project's element of the tick's actions. It is
	// not a command error.
	assertRoutingHealthRefusal := func(t *testing.T, spine string, res mcResult) {
		t.Helper()
		if res.code != 0 {
			t.Fatalf("a routing health refusal is a terminal effect, not an error: exit=%d stderr=%q", res.code, res.stderr)
		}
		eff := tickOnlyAction(t, res.json)
		if eff["action"] != "refused" || eff["class"] != "health" ||
			eff["code"] != "health.routing_invalid" || eff["consequence"] != "health" ||
			eff["project"] != "ws-test" {
			t.Fatalf("effect = %v, want refused/health/health.routing_invalid for ws-test", eff)
		}
		db := openDB(t, spine)
		if n := queryInt(t, db, `SELECT COUNT(*) FROM runs WHERE role IS NOT 'scribe'`); n != 0 {
			t.Fatalf("a routing refusal opened %d runs", n)
		}
		if n := queryInt(t, db, `SELECT COUNT(*) FROM tasks WHERE blocked = 1`); n != 0 {
			t.Fatalf("a deployment health refusal blocked %d tasks", n)
		}
		if n := queryInt(t, db, `SELECT COUNT(*) FROM activity WHERE kind='dispatch.health' AND dispatch_key IS NOT NULL`); n != 1 {
			t.Fatalf("want exactly one keyed dispatch.health action, got %d", n)
		}
	}

	t.Run("missing_file_is_health_refusal_before_claim", func(t *testing.T) {
		spine := initSpine(t)
		if err := os.Remove(filepath.Join(filepath.Dir(spine), "routing.md")); err != nil {
			t.Fatal(err)
		}
		proposalTask(t, spine, "must not dispatch")
		assertRoutingHealthRefusal(t, spine, runMC(t, spineEnv(spine), "", "dispatch"))
	})

	t.Run("unresolved_binding_is_health_refusal_before_claim", func(t *testing.T) {
		spine := initSpine(t)
		writeRouting(t, spine, strings.Replace(defaultRoutingMarkdown,
			"| worker | claude-sdk | endpoint |", "| worker | claude-sdk | missing |", 1))
		proposalTask(t, spine, "must not dispatch")
		assertRoutingHealthRefusal(t, spine, runMC(t, spineEnv(spine), "", "dispatch"))
	})

	// One binding can serve every role: the producer and the judge on the
	// same family and the same binding is a valid table.
	t.Run("one_binding_for_every_role_dispatches", func(t *testing.T) {
		spine := initSpine(t)
		table := defaultRoutingMarkdown
		for _, row := range []string{
			"| strategist | claude-sdk | claude |", "| worker | claude-sdk | endpoint |",
			"| packager | claude-sdk | endpoint |", "| collie | claude-sdk | claude |",
		} {
			role := strings.Fields(row)[1]
			table = strings.Replace(table, row, "| "+role+" | codex | chatgpt |", 1)
		}
		writeRouting(t, spine, table)
		proposalTask(t, spine, "judged on the one binding")
		eff := dispatchExpect(t, spine, "spawn")
		if eff["role"] != "arbiter" || eff["harness"] != "codex" || eff["model_binding"] != "chatgpt" {
			t.Fatalf("resolved spawn = %v", eff)
		}
	})

	t.Run("explicit_test_fake_route_propagates_without_fallback", func(t *testing.T) {
		spine := initSpine(t)
		proposalTask(t, spine, "fake route")
		eff := dispatchExpect(t, spine, "spawn")
		if eff["harness"] != "fake" || eff["model_binding"] != "fake" {
			t.Fatalf("fake dispatch effect=%v", eff)
		}
	})

	t.Run("invalid_routing_does_not_block_the_pending_update", func(t *testing.T) {
		spine := initSpine(t)
		id := proposalTask(t, spine, "already approved")
		packageTask(t, spine, id)
		packet := reviewedPacket(t, spine, id)
		res := runMC(t, spineEnv(spine), "", packetDecideArgs(id, packet, "approve")...)
		if res.code != 0 {
			t.Fatalf("approve fixture: %s", res.stderr)
		}
		writeRouting(t, spine, "not a routing table\n")
		eff := startExpect(t, spine, "apply")[0]
		if eff["project"] != "ws-test" {
			t.Fatalf("pending update under broken routing = %v, want ws-test's apply", eff)
		}
	})

	t.Run("relative_MC_HOME_is_refused", func(t *testing.T) {
		spine := initSpine(t)
		proposalTask(t, spine, "absolute home only")
		res := runMC(t, []string{"MC_SPINE=" + spine, "MC_HOME=relative/home"}, "", "dispatch")
		if res.code != 2 || !strings.Contains(res.stderr, "must be absolute") {
			t.Fatalf("exit=%d stderr=%q", res.code, res.stderr)
		}
	})

	t.Run("unset_MC_HOME_uses_only_default_home", func(t *testing.T) {
		spine := initSpine(t)
		writeRouting(t, spine, defaultRoutingMarkdown)
		proposalTask(t, spine, "default home")
		home := t.TempDir()
		root := filepath.Join(home, ".culler")
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "routing.md"), []byte(defaultRoutingMarkdown), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := operatorconfig.Ensure(filepath.Join(root, "config.toml")); err != nil {
			t.Fatal(err)
		}
		if _, err := operatorconfig.UpdateAtomic(filepath.Join(root, "config.toml"), enableReferenceBindings); err != nil {
			t.Fatal(err)
		}
		// The deployment mirror lives in whichever MC_HOME dispatch resolves.
		db := openDB(t, spine)
		uuid := queryStr(t, db, `SELECT deployment_uuid FROM meta WHERE id = 1`)
		if err := os.WriteFile(filepath.Join(root, "deployment.uuid"), []byte(uuid+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		canonicalRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatal(err)
		}
		toolHome := filepath.Join(canonicalRoot, "state", "projects", "ws-test", "home")
		if err := os.MkdirAll(toolHome, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(toolHome, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE sandbox_profiles SET tool_home_dir=? WHERE id='default'`, toolHome); err != nil {
			t.Fatal(err)
		}
		res := runMC(t, []string{"MC_SPINE=" + spine, "HOME=" + home}, "", "dispatch")
		if res.code != 0 {
			t.Fatalf("default-home dispatch code=%d json=%v stderr=%q", res.code, res.json, res.stderr)
		}
		if eff := tickOnlyAction(t, res.json); eff["harness"] != "codex" || eff["model_binding"] != "chatgpt" {
			t.Fatalf("default-home route effect=%v", eff)
		}
	})
}

func TestDispatchCASSingleWinner(t *testing.T) {
	spine := initSpine(t)
	proposalTask(t, spine, "contended task")

	const claimants = 4
	results := make([]mcResult, claimants)
	var wg sync.WaitGroup
	for i := 0; i < claimants; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command(mcBin, "dispatch")
			cmd.Env = hermeticEnv(spineEnv(spine)...)
			var outBuf, errBuf bytes.Buffer
			cmd.Stdout = &outBuf
			cmd.Stderr = &errBuf
			err := cmd.Run()
			res := mcResult{stdout: outBuf.String(), stderr: errBuf.String()}
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					res.code = exit.ExitCode()
				} else {
					res.code = -1
				}
			}
			results[i] = res
		}(i)
	}
	wg.Wait()

	spawns, losers := 0, 0
	for i, res := range results {
		if res.code != 0 {
			t.Fatalf("claimant %d exit = %d: %s", i, res.code, res.stderr)
		}
		var tick map[string]any
		if err := json.Unmarshal([]byte(res.stdout), &tick); err != nil {
			t.Fatalf("claimant %d output: %v", i, err)
		}
		actions := tickActions(t, tick)
		if len(actions) == 0 {
			// Prepared after the winner committed: the held-lease idle.
			assertTickIdleOnly(t, tick, "ws-test", "lease-held")
			losers++
			continue
		}
		eff := tickOnlyAction(t, tick)
		switch eff["action"] {
		case "spawn":
			spawns++
		case "refused":
			// Prepared before the winner committed: the commit fence — only
			// one commit can match current state; the loser stales inertly
			// and the next tick re-decides.
			if eff["code"] != "preflight.stale" || eff["consequence"] != "none" || eff["project"] != "ws-test" {
				t.Fatalf("claimant %d refused with %v, want preflight.stale/none for ws-test", i, eff)
			}
			losers++
		default:
			t.Fatalf("claimant %d action = %v", i, eff["action"])
		}
	}
	if spawns != 1 || losers != claimants-1 {
		t.Fatalf("spawns = %d, losers = %d; want exactly one winner among %d", spawns, losers, claimants)
	}

	db := openDB(t, spine)
	if n := queryInt(t, db, `SELECT COUNT(*) FROM runs WHERE role IS NOT 'scribe'`); n != 1 {
		t.Fatalf("runs rows = %d, want 1 (losers must not open runs)", n)
	}
}

// ---------------------------------------------------------------------------
// Reaping through the CLI: the writes of `mc dispatch` reap.
// ---------------------------------------------------------------------------

func TestDispatchReap(t *testing.T) {
	t.Run("spawn_watchdog_charges_retries", func(t *testing.T) {
		spine := initSpine(t, "--spawn-grace-s", "5")
		taskID := proposalTask(t, spine, "doomed spawn")
		eff := dispatchExpect(t, spine, "spawn")
		runID := eff["run_id"].(string)

		// Fixture surgery: age the stamped-but-never-heartbeated claim past
		// spawn_grace_s (the lock domain's clock cannot be injected).
		db := openDB(t, spine)
		if _, err := db.Exec(`UPDATE leases SET acquired_uptime_ms = acquired_uptime_ms - 60000 WHERE project = 'ws-test'`); err != nil {
			t.Fatal(err)
		}

		reap := dispatchExpect(t, spine, "reap")
		if reap["run_id"] != runID || reap["reason"] != "spawn-watchdog" || reap["stop_container"] != true {
			t.Fatalf("reap effect = %v", reap)
		}
		if got := queryStr(t, db, `SELECT outcome FROM runs WHERE id = ?`, runID); got != "reaped" {
			t.Fatalf("runs.outcome = %q, want reaped", got)
		}
		if got := queryInt(t, db, `SELECT dispatch_retries FROM tasks WHERE id = ?`, taskID); got != 3 {
			t.Fatalf("The launch failure changed the task allowance: %d", got)
		}
		if got := queryInt(t, db, `SELECT infrastructure_failure_count FROM projects WHERE id='ws-test'`); got != 1 {
			t.Fatalf("The launch failure count is %d", got)
		}
		if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != "<NULL>" {
			t.Fatalf("lease not freed by reap: %q", got)
		}
		assertTickIdleOnly(t, dispatch(t, spine), "ws-test", "project-infrastructure-retry-wait")
		if _, err := db.Exec(`UPDATE projects SET infrastructure_next_retry_at='2000-01-01T00:00:00Z' WHERE id='ws-test'`); err != nil {
			t.Fatal(err)
		}
		if eff := dispatchExpect(t, spine, "spawn"); eff["role"] != "arbiter" {
			t.Fatalf("post-reap re-select role = %v", eff["role"])
		}
	})

	t.Run("exhausted_launch_allowance_stops_Project", func(t *testing.T) {
		spine := initSpine(t, "--spawn-grace-s", "5")
		taskID := proposalTask(t, spine, "always dying")
		db := openDB(t, spine)
		if _, err := db.Exec(`UPDATE projects SET infrastructure_failure_count=2 WHERE id='ws-test'`); err != nil {
			t.Fatal(err)
		}
		dispatchExpect(t, spine, "spawn")
		if _, err := db.Exec(`UPDATE leases SET acquired_uptime_ms = acquired_uptime_ms - 60000 WHERE project = 'ws-test'`); err != nil {
			t.Fatal(err)
		}
		dispatchExpect(t, spine, "reap")
		if got := queryInt(t, db, `SELECT stalled FROM tasks WHERE id = ?`, taskID); got != 0 {
			t.Fatal("The Project launch failure stalled the proposal")
		}
		if got := queryInt(t, db, `SELECT infrastructure_failure_count FROM projects WHERE id='ws-test'`); got != 3 {
			t.Fatal("The Project allowance did not reach its limit")
		}
		if got := queryInt(t, db, `SELECT blocked FROM tasks WHERE id = ?`, taskID); got != 0 {
			t.Fatal("an infrastructure failure created an operator block")
		}
		if got := queryStr(t, db, `SELECT f.condition FROM infrastructure_failures f JOIN runs r ON r.id=f.run_id WHERE r.project='ws-test'`); got != "spawn-watchdog" {
			t.Fatalf("the failure condition = %q", got)
		}
		assertTickIdleOnly(t, dispatch(t, spine), "ws-test", "project-infrastructure-exhausted")
	})
}

func TestZombieCLILeavesNewHolderUntouched(t *testing.T) {
	spine, taskID, zombieRun, zombieEnv := workerFixture(t, "zombie CLI fence")
	db := openDB(t, spine)
	if _, err := db.Exec(`UPDATE leases SET acquired_uptime_ms=acquired_uptime_ms - 600000,
		last_heartbeat_at=NULL, heartbeat_uptime_ms=NULL WHERE project='ws-test'`); err != nil {
		t.Fatal(err)
	}
	if got := dispatchExpect(t, spine, "reap"); got["run_id"] != zombieRun {
		t.Fatalf("reaped run = %v, want %s", got["run_id"], zombieRun)
	}
	if _, err := db.Exec(`UPDATE tasks SET run_retry_at='2000-01-01T00:00:00Z' WHERE id=?`, taskID); err != nil {
		t.Fatal(err)
	}
	fresh := dispatchThrough(t, spine, "spawn")
	freshRun := fresh["run_id"].(string)
	if freshRun == zombieRun {
		t.Fatal("re-claim reused fencing token")
	}
	snapshot := func() string {
		return queryStr(t, db, `SELECT run_id || '|' || owner || '|' || subject || '|' ||
			acquired_at || '|' || COALESCE(last_heartbeat_at, 'null') || '|' || boot_id || '|' ||
			acquired_uptime_ms || '|' || COALESCE(heartbeat_uptime_ms, 'null') || '|' || deadline_uptime_ms
			FROM leases WHERE project = 'ws-test'`)
	}
	before := snapshot()

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "old_complete", args: []string{"complete", fmt.Sprint(taskID), "--run", zombieRun, "--status", "worked"}},
		{name: "old_heartbeat", args: []string{"heartbeat", zombieRun}},
		{name: "zombie_supplies_new_token", args: []string{"complete", fmt.Sprint(taskID), "--run", freshRun, "--status", "worked"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := runMC(t, zombieEnv, "", tc.args...)
			if res.code != 1 {
				t.Fatalf("exit = %d stderr=%q, want fenced refusal", res.code, res.stderr)
			}
		})
	}
	if after := snapshot(); after != before {
		t.Fatalf("zombie CLI disturbed new lease:\n before %s\n after  %s", before, after)
	}
}

// ---------------------------------------------------------------------------
// mc packet decide — validation + the revise/cancel arms.
// ---------------------------------------------------------------------------

func TestPacketDecideValidation(t *testing.T) {
	spine := initSpine(t)
	taskID := proposalTask(t, spine, "not yet packaged")

	tests := []struct {
		name string
		args []string
		want int
		msg  string
	}{
		{"approve_with_reason_forbidden",
			[]string{"packet", "decide", fmt.Sprint(taskID), "--approve", "--reason", "no"}, 1, "forbidden"},
		{"revise_without_reason",
			[]string{"packet", "decide", fmt.Sprint(taskID), "--revise"}, 1, "required"},
		{"cancel_without_reason",
			[]string{"packet", "decide", fmt.Sprint(taskID), "--cancel"}, 1, "required"},
		{"no_flag_is_usage",
			[]string{"packet", "decide", fmt.Sprint(taskID)}, 2, ""},
		{"two_flags_is_usage",
			[]string{"packet", "decide", fmt.Sprint(taskID), "--approve", "--cancel"}, 2, ""},
		{"approve_without_reviewed_version_is_domain",
			[]string{"packet", "decide", fmt.Sprint(taskID), "--approve"}, 1, "reviewed packet and submitted version"},
		{"approve_unpackaged_is_domain",
			[]string{"packet", "decide", fmt.Sprint(taskID), "--approve", "--packet", "1", "--version", "1"}, 1, ""},
		{"approve_missing_task",
			[]string{"packet", "decide", "999", "--approve", "--packet", "1", "--version", "1"}, 1, ""},
		{"cancel_without_packet_is_domain",
			[]string{"packet", "decide", fmt.Sprint(taskID), "--cancel", "--reason", "invisible", "--packet", "1", "--version", "1"}, 1, "obsolete"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := runMC(t, spineEnv(spine), "", tc.args...)
			if res.code != tc.want {
				t.Fatalf("exit = %d, want %d (stderr %q)", res.code, tc.want, res.stderr)
			}
			if tc.msg != "" && !strings.Contains(res.stderr, tc.msg) {
				t.Fatalf("stderr %q missing %q", res.stderr, tc.msg)
			}
		})
	}

	reviewed := func(t *testing.T, title string) (string, int64, map[string]any, *sql.DB) {
		t.Helper()
		s := initSpine(t)
		id := proposalTask(t, s, title)
		packageTask(t, s, id)
		packet := reviewedPacket(t, s, id)
		return s, id, packet, openDB(t, s)
	}

	t.Run("revise_reenters_same_task_and_keeps_packet_live", func(t *testing.T) {
		s, id, packet, db := reviewed(t, "revise me")
		res := runMC(t, spineEnv(s), "", packetDecideArgs(id, packet, "revise", "tighten evidence")...)
		if res.code != 0 {
			t.Fatalf("revise failed: %s", res.stderr)
		}
		if got := queryStr(t, db, `SELECT status || '/' || revision_notes FROM tasks WHERE id = ?`, id); got != "seeded/tighten evidence" {
			t.Fatalf("re-entered task = %q", got)
		}
		if got := queryInt(t, db, `SELECT t.archived FROM tasks t JOIN review_versions p ON p.id=t.current_review_id WHERE task_id = ?`, id); got != 0 {
			t.Fatalf("revise freed packet slot: archived=%d", got)
		}
		if got := queryStr(t, db, `SELECT decision FROM review_decisions WHERE packet_id = ?`, int64(packet["packet_id"].(float64))); got != "revise" {
			t.Fatalf("review decision = %q", got)
		}
	})

	t.Run("cancel_archives_task_and_packet", func(t *testing.T) {
		s, id, packet, db := reviewed(t, "cancel me")
		res := runMC(t, spineEnv(s), "", packetDecideArgs(id, packet, "cancel", "no longer useful")...)
		if res.code != 0 {
			t.Fatalf("cancel failed: %s", res.stderr)
		}
		if got := queryStr(t, db, `SELECT decision || '/' || archived FROM tasks WHERE id = ?`, id); got != "cancelled/1" {
			t.Fatalf("cancelled task = %q", got)
		}
		if got := queryInt(t, db, `SELECT t.archived FROM tasks t JOIN review_versions p ON p.id=t.current_review_id WHERE task_id = ?`, id); got != 1 {
			t.Fatalf("cancel did not archive packet: %d", got)
		}
	})

	t.Run("a_stale_packet_is_refused", func(t *testing.T) {
		s, id, packet, db := reviewed(t, "stale packet")
		stale := map[string]any{"packet_id": packet["packet_id"], "version_id": packet["version_id"].(float64) + 1}
		res := runMC(t, spineEnv(s), "", packetDecideArgs(id, stale, "approve")...)
		if res.code != 1 {
			t.Fatalf("stale approve exit = %d stderr %q", res.code, res.stderr)
		}
		if got := queryStr(t, db, `SELECT COALESCE(decision, 'none') FROM tasks WHERE id = ?`, id); got != "none" {
			t.Fatalf("stale approve decided the task: %q", got)
		}
	})
}

// packagerPlaneTarget names the one artifact plane a fixture run gets. The
// class root is image storage; only `<root>/<target>` is a host bind.
const packagerPlaneTarget = "ws-fixture"

// artifactRootFromEnv reads back the class root a fixture stood up. A role
// that writes at the class root passes every filesystem check and loses the
// bytes with its container. Tests that need to name the root itself must not
// hardcode `/mc/artifacts`: under test the root is an override, and
// hardcoding it turns a plane-membership assertion into a root-prefix one
// that passes for the wrong reason.
func artifactRootFromEnv(t *testing.T, env []string) string {
	t.Helper()
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "MC_ARTIFACTS_ROOT="); ok {
			return v
		}
	}
	t.Fatal("fixture env carries no MC_ARTIFACTS_ROOT")
	return ""
}

func TestDoubleApproveRejected(t *testing.T) {
	spine := initSpine(t)
	taskID := proposalTask(t, spine, "decided once")
	packageTask(t, spine, taskID)
	packet := reviewedPacket(t, spine, taskID)
	if res := runMC(t, spineEnv(spine), "", packetDecideArgs(taskID, packet, "approve")...); res.code != 0 {
		t.Fatalf("first approve failed: %s", res.stderr)
	}
	res := runMC(t, spineEnv(spine), "", packetDecideArgs(taskID, packet, "approve")...)
	if res.code != 1 {
		t.Fatalf("double approve exit = %d (%q), want 1", res.code, res.stderr)
	}
	db := openDB(t, spine)
	if got := queryInt(t, db, `SELECT COUNT(*) FROM review_decisions WHERE packet_id = ?`, int64(packet["packet_id"].(float64))); got != 1 {
		t.Fatalf("review decisions = %d, want the one approval", got)
	}
}

// ---------------------------------------------------------------------------
// Pipeline-role scope: identity, role match, fencing.
// ---------------------------------------------------------------------------

func TestRoleScopeEnforcement(t *testing.T) {
	spine := initSpine(t)
	taskID := proposalTask(t, spine, "scoped work")
	eff := dispatchExpect(t, spine, "spawn") // arbiter
	run := eff["run_id"].(string)
	arbiterEnv := runJSONEnv(t, spine, run, "pipeline", "arbiter")
	forbiddenInit := filepath.Join(t.TempDir(), "pipeline-must-not-init.db")

	batch := fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"promote","reason":"ok"}]}`, taskID)
	tests := []struct {
		name string
		env  []string
		args []string
		in   string
		msg  string
	}{
		{"no_run_json_refused", spineEnv(spine),
			[]string{"arbiter", "decide", "--run", run, "--batch", "-"}, batch, "pipeline run identity"},
		{"wrong_role_refused", runJSONEnv(t, spine, run, "pipeline", "worker"),
			[]string{"arbiter", "decide", "--run", run, "--batch", "-"}, batch, "role mismatch"},
		{"wrong_tier_refused", runJSONEnv(t, spine, run, "collie", "arbiter"),
			[]string{"arbiter", "decide", "--run", run, "--batch", "-"}, batch, "pipeline-tier"},
		{"stale_run_refused", runJSONEnv(t, spine, run, "pipeline", "arbiter"),
			[]string{"arbiter", "decide", "--run", "old-run", "--batch", "-"}, batch, "stale run"},
		{"caller_run_id_mismatch_refused", runJSONEnv(t, spine, "old-run", "pipeline", "arbiter"),
			[]string{"arbiter", "decide", "--run", run, "--batch", "-"}, batch, "caller run mismatch"},
		{"complete_without_identity", spineEnv(spine),
			[]string{"complete", fmt.Sprint(taskID), "--run", run, "--status", "worked"}, "", "pipeline run identity"},
		{"heartbeat_without_identity", spineEnv(spine),
			[]string{"heartbeat", run}, "", "pipeline run identity"},
		{"register_session_without_identity", spineEnv(spine),
			[]string{"run", "register-session", run, "--native-ref", "x", "--file", "native.jsonl"}, "", "pipeline run identity"},
		{"verifier_wrong_role", runJSONEnv(t, spine, run, "pipeline", "arbiter"),
			[]string{"verifier", "verdict", fmt.Sprint(taskID), "--run", run,
				"--outcome", "pass", "--evidence", "e", "--sha", "s"}, "", "role mismatch"},
		{"strategist_wrong_role", runJSONEnv(t, spine, run, "pipeline", "worker"),
			[]string{"strategist", "propose", "--run", run, "--batch", "-"}, `{"proposals":[]}`, "role mismatch"},
		{"pipeline_init_denied", arbiterEnv,
			[]string{"__init", "--spine", forbiddenInit, "--project", "forged"}, "", "host"},
		{"pipeline_dispatch_denied", arbiterEnv,
			[]string{"dispatch"}, "", "host"},
		{"pipeline_task_add_denied", arbiterEnv,
			[]string{"task", "add", "forged operator task", "--project", "ws-test"}, "", "operator verb"},
		{"pipeline_packet_decide_denied", arbiterEnv,
			[]string{"packet", "decide", fmt.Sprint(taskID), "--cancel", "--reason", "forged"}, "", "operator verb"},
		{"pipeline_delivery_record_denied", arbiterEnv,
			[]string{"delivery", "record", "--operation", "cleanup", "--run", run, "--result", "{}"}, "", "host"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := runMC(t, tc.env, tc.in, tc.args...)
			if res.code != 1 {
				t.Fatalf("exit = %d, want 1 (stderr %q)", res.code, res.stderr)
			}
			if !strings.Contains(res.stderr, tc.msg) {
				t.Fatalf("stderr %q missing %q", res.stderr, tc.msg)
			}
		})
	}

	// None of the refusals touched the state: the pool is intact and the
	// lease still held by the arbiter run.
	db := openDB(t, spine)
	if got := queryStr(t, db, `SELECT status FROM tasks WHERE id = ?`, taskID); got != "proposed" {
		t.Fatalf("refused calls moved the task to %q", got)
	}
	if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != run {
		t.Fatalf("refused calls disturbed the lease: %q", got)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM tasks`); got != 1 {
		t.Fatalf("pipeline operator-verb attempts changed task count to %d", got)
	}
	if _, err := os.Stat(forbiddenInit); !os.IsNotExist(err) {
		t.Fatalf("pipeline init created %s", forbiddenInit)
	}
}

func TestArbiterDecideCoverage(t *testing.T) {
	spine := initSpine(t)
	t1 := proposalTask(t, spine, "proposal one")
	t2 := proposalTask(t, spine, "proposal two")
	eff := dispatchExpect(t, spine, "spawn")
	run := eff["run_id"].(string)
	env := runJSONEnv(t, spine, run, "pipeline", "arbiter")

	tests := []struct {
		name string
		in   string
		want int
		msg  string
	}{
		{"subset_rejected",
			fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"promote","reason":"r"}]}`, t1), 1, "exactly"},
		{"superset_rejected",
			fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"promote","reason":"r"},{"task":%d,"decision":"promote","reason":"r"},{"task":99,"decision":"promote","reason":"r"}]}`, t1, t2), 1, "exactly"},
		{"unknown_decision",
			fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"defer","reason":"r"},{"task":%d,"decision":"promote","reason":"r"}]}`, t1, t2), 1, "unknown decision"},
		{"bad_json", `{"verdicts": nope}`, 1, "bad batch payload"},
		{"unknown_field_rejected",
			fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"promote","reason":"r"},{"task":%d,"decision":"reject","reason":"r"}],"surprise":true}`, t1, t2), 1, "bad batch payload"},
		{"trailing_json_rejected",
			fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"promote","reason":"r"},{"task":%d,"decision":"reject","reason":"r"}]} {}`, t1, t2), 1, "bad batch payload"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := runMC(t, env, tc.in, "arbiter", "decide", "--run", run, "--batch", "-")
			if res.code != tc.want || !strings.Contains(res.stderr, tc.msg) {
				t.Fatalf("exit = %d stderr %q, want %d containing %q", res.code, res.stderr, tc.want, tc.msg)
			}
		})
	}

	// Atomicity: the failed batches promoted nothing.
	db := openDB(t, spine)
	if n := queryInt(t, db, `SELECT COUNT(*) FROM tasks WHERE status = 'seeded'`); n != 0 {
		t.Fatalf("failed batches half-applied: %d seeded", n)
	}

	// The exact mixed batch promotes and rejects atomically.
	res := runMC(t, env,
		fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"promote","reason":"r"},{"task":%d,"decision":"reject","reason":"weak"}]}`, t1, t2),
		"arbiter", "decide", "--run", run, "--batch", "-")
	if res.code != 0 {
		t.Fatalf("exact batch failed: %s", res.stderr)
	}
	if n := queryInt(t, db, `SELECT COUNT(*) FROM tasks WHERE status = 'seeded'`); n != 1 {
		t.Fatalf("seeded = %d, want 1", n)
	}
	if got := queryStr(t, db, `SELECT decision || '/' || archived FROM tasks WHERE id = ?`, t2); got != "rejected/1" {
		t.Fatalf("rejected task = %q", got)
	}
}

// Duplicate rejections require valid references when no other task is ready.
func TestArbiterDuplicateRejectionsRequireValidCitations(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)

	// A real promoted row, then blocked: unarchived and non-proposed (so it is
	// existing_work) but invisible to the readiness count.
	landed := proposalTask(t, spine, "the work that already exists")
	run1 := dispatchExpect(t, spine, "spawn")["run_id"].(string)
	env1 := runJSONEnv(t, spine, run1, "pipeline", "arbiter")
	if res := runMC(t, env1,
		fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"promote","reason":"r"}]}`, landed),
		"arbiter", "decide", "--run", run1, "--batch", "-"); res.code != 0 {
		t.Fatalf("setup promote failed: %s", res.stderr)
	}
	if res := runMC(t, spineEnv(spine), "", "task", "block", fmt.Sprint(landed),
		"--reason", "operator holds it"); res.code != 0 {
		t.Fatalf("setup block failed: %s", res.stderr)
	}
	if n := queryInt(t, db,
		`SELECT COUNT(*) FROM tasks WHERE archived = 0 AND blocked = 0 AND stage_rank > 0`); n != 0 {
		t.Fatalf("setup left %d ready rows; the guard would not fire", n)
	}

	t1 := proposalTask(t, spine, "duplicate one")
	t2 := proposalTask(t, spine, "duplicate two")
	run2 := dispatchExpect(t, spine, "spawn")["run_id"].(string)
	env2 := runJSONEnv(t, spine, run2, "pipeline", "arbiter")

	refused := []struct {
		name string
		in   string
		msg  string
	}{
		{"no_citation_at_all",
			fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"reject","reason":"dup"},{"task":%d,"decision":"reject","reason":"dup"}]}`, t1, t2),
			"supplies neither exception"},
		{"one_citation_missing",
			fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"reject","reason":"dup","duplicate_of":%d},{"task":%d,"decision":"reject","reason":"dup"}]}`, t1, landed, t2),
			"supplies neither exception"},
		{"cites_a_pool_member",
			fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"reject","reason":"dup","duplicate_of":%d},{"task":%d,"decision":"reject","reason":"dup","duplicate_of":%d}]}`, t1, t2, t2, landed),
			"member of its own pool"},
		{"cites_a_row_that_does_not_exist",
			fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"reject","reason":"dup","duplicate_of":9999},{"task":%d,"decision":"reject","reason":"dup","duplicate_of":%d}]}`, t1, t2, landed),
			"not existing work"},
		{"duplicate_of_on_a_promote",
			fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"promote","reason":"r","duplicate_of":%d},{"task":%d,"decision":"reject","reason":"dup","duplicate_of":%d}]}`, t1, landed, t2, landed),
			"belongs to a reject"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			res := runMC(t, env2, tc.in, "arbiter", "decide", "--run", run2, "--batch", "-")
			if res.code != 1 || !strings.Contains(res.stderr, tc.msg) {
				t.Fatalf("exit = %d stderr %q, want 1 containing %q", res.code, res.stderr, tc.msg)
			}
		})
	}
	if n := queryInt(t, db, `SELECT COUNT(*) FROM tasks WHERE decision = 'rejected'`); n != 0 {
		t.Fatalf("a refused batch half-applied: %d rejected", n)
	}

	// The carve-out itself: every rejection cites a real existing_work row, so
	// the batch commits even though it promotes nothing and the board drains.
	res := runMC(t, env2,
		fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"reject","reason":"duplicates the blocked row","duplicate_of":%d},{"task":%d,"decision":"reject","reason":"duplicates the blocked row","duplicate_of":%d}]}`,
			t1, landed, t2, landed),
		"arbiter", "decide", "--run", run2, "--batch", "-")
	if res.code != 0 {
		t.Fatalf("all-duplicate batch refused: %s", res.stderr)
	}
	for _, id := range []int64{t1, t2} {
		if got := queryStr(t, db, `SELECT decision || '/' || archived FROM tasks WHERE id = ?`, id); got != "rejected/1" {
			t.Fatalf("task %d = %q, want rejected/1", id, got)
		}
	}
	if got := queryStr(t, db, `SELECT COALESCE(run_id, '') FROM leases WHERE project = 'ws-test'`); got != "" {
		t.Fatalf("carve-out batch left the lease held: %q", got)
	}
}

// The zero-promotion guard and the duplicate carve-out are scoped to the
// run's Project: ready work in another Project does not satisfy the guard,
// and a `duplicate_of` citing another Project's row is not existing work of
// this one.
func TestArbiterGuardAndDuplicateCitationsAreScopedToTheRunsProject(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	// A paused Project is not walked by the tick, so its rows can exist
	// without the tick acting on them; the guard and carve-out still see them
	// as rows of another Project.
	addProject(t, spine, "ws-other", "Other")
	if pause := runMC(t, spineEnv(spine), "", "project", "pause", "ws-other"); pause.code != 0 {
		t.Fatalf("project pause failed: %s", pause.stderr)
	}
	insertOther := func(title, status string) int64 {
		t.Helper()
		res, err := db.Exec(`INSERT INTO tasks (title, project) VALUES (?, 'ws-other')`, title)
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		for _, step := range []string{"seeded", "worked"} {
			if _, err := db.Exec(`UPDATE tasks SET status = ? WHERE id = ?`, step, id); err != nil {
				t.Fatal(err)
			}
			if step == status {
				break
			}
		}
		return id
	}
	insertOther("ready elsewhere", "seeded")
	otherWorked := insertOther("existing elsewhere", "worked")
	if n := queryInt(t, db,
		`SELECT COUNT(*) FROM tasks WHERE archived = 0 AND blocked = 0 AND stage_rank > 0 AND project = 'ws-other'`); n != 2 {
		t.Fatalf("setup left %d ready rows in ws-other, want 2", n)
	}

	t1 := proposalTask(t, spine, "duplicate one")
	t2 := proposalTask(t, spine, "duplicate two")
	run := dispatchExpect(t, spine, "spawn")["run_id"].(string)
	env := runJSONEnv(t, spine, run, "pipeline", "arbiter")
	for _, tc := range []struct {
		name string
		in   string
		msg  string
	}{
		{"ready_work_elsewhere_does_not_lift_the_guard",
			fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"reject","reason":"dup"},{"task":%d,"decision":"reject","reason":"dup"}]}`, t1, t2),
			"supplies neither exception"},
		{"citing_another_projects_row_is_not_existing_work",
			fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"reject","reason":"dup","duplicate_of":%d},{"task":%d,"decision":"reject","reason":"dup","duplicate_of":%d}]}`, t1, otherWorked, t2, otherWorked),
			"not existing work of Project ws-test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := runMC(t, env, tc.in, "arbiter", "decide", "--run", run, "--batch", "-")
			if res.code != 1 || !strings.Contains(res.stderr, tc.msg) {
				t.Fatalf("exit = %d stderr %q, want 1 containing %q", res.code, res.stderr, tc.msg)
			}
		})
	}
	if n := queryInt(t, db, `SELECT COUNT(*) FROM tasks WHERE decision = 'rejected'`); n != 0 {
		t.Fatalf("a refused batch half-applied: %d rejected", n)
	}
	res := runMC(t, env,
		fmt.Sprintf(`{"verdicts":[{"task":%d,"decision":"promote","reason":"r"},{"task":%d,"decision":"reject","reason":"weak"}]}`, t1, t2),
		"arbiter", "decide", "--run", run, "--batch", "-")
	if res.code != 0 {
		t.Fatalf("mixed batch failed: %s", res.stderr)
	}
	if got := queryStr(t, db, `SELECT status || '/' || project FROM tasks WHERE id = ?`, t1); got != "seeded/ws-test" {
		t.Fatalf("promoted task = %q", got)
	}
}

func TestStrategistProposeInserts(t *testing.T) {
	spine := initSpine(t)
	eff := dispatchExpect(t, spine, "spawn") // empty board → strategist(propose)
	if eff["role"] != "strategist(propose)" {
		t.Fatalf("role = %v", eff["role"])
	}
	run := eff["run_id"].(string)
	env := runJSONEnv(t, spine, run, "pipeline", "strategist(propose)")

	// An omitted `project` defaults to the run's Project; a present one
	// must equal it.
	// Only the operator sets a task priority: a proposal with a priority is refused.
	res := runMC(t, env, `{"proposals":[{"title":"agent idea one","priority":-1,"strategic_rationale":"Observed: the fixture lacks this result."}]}`,
		"strategist", "propose", "--run", run, "--batch", "-")
	if res.code != 1 {
		t.Fatalf("exit = %d stderr=%q, want a refusal of the proposal priority", res.code, res.stderr)
	}
	res = runMC(t, env, `{"proposals":[
		{"title":"agent idea one","description":"criteria","strategic_rationale":"Observed: the fixture lacks this result."},
		{"project":"ws-test","title":"agent idea two","strategic_rationale":"Observed: the fixture lacks this result."}]}`,
		"strategist", "propose", "--run", run, "--batch", "-")
	if res.code != 0 {
		t.Fatalf("strategist propose failed: %s", res.stderr)
	}
	ids := res.json["task_ids"].([]any)
	if len(ids) != 2 {
		t.Fatalf("task_ids = %v", ids)
	}
	db := openDB(t, spine)
	if n := queryInt(t, db, `SELECT COUNT(*) FROM tasks WHERE origin = 'autonomous' AND status = 'proposed' AND project = 'ws-test' AND priority = 2`); n != 2 {
		t.Fatalf("autonomous proposals in ws-test = %d, want 2", n)
	}
	if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != "<NULL>" {
		t.Fatalf("lease not released: %q", got)
	}

	t.Run("invalid_proposal_aborts_whole_batch", func(t *testing.T) {
		eff := dispatchExpect(t, spine, "spawn") // arbiter now owns the pool
		if eff["role"] != "arbiter" {
			t.Fatalf("spawn role = %v, want the Arbiter over the pool", eff["role"])
		}
		run := eff["run_id"].(string)
		env := runJSONEnv(t, spine, run, "pipeline", "strategist(propose)")
		// Wrong role for this lease's owner is not what we test here — use
		// the fencing-neutral validation failure instead: title missing.
		res := runMC(t, env, `{"proposals":[{"project":"ws-test"}]}`,
			"strategist", "propose", "--run", run, "--batch", "-")
		if res.code != 1 {
			t.Fatalf("exit = %d, want 1", res.code)
		}
	})
}

// A Strategist run inserts into the pool of its own Project only:
// a proposal naming another Project — active or not — refuses the whole
// batch and leaves the lease held.
func TestStrategistProposalLimitsAndArbiterPool(t *testing.T) {
	spine := initSpine(t)
	effect := dispatchExpect(t, spine, "spawn")
	run := effect["run_id"].(string)
	env := runJSONEnv(t, spine, run, "pipeline", "strategist(propose)")
	db := openDB(t, spine)
	for _, test := range []struct {
		name, batch, message string
	}{
		{"third_task", `{"proposals":[{"title":"One"},{"title":"Two"},{"title":"Three"}]}`, "at most two tasks"},
		{"unsupported_scope", `{"proposals":[{"title":"One","strategic_rationale":"Observed: the fixture lacks this result."},{"title":"Two","scope":"group","strategic_rationale":"Observed: the fixture lacks this result."}]}`, "scope must be task"},
		{"absent_rationale", `{"proposals":[{"title":"One","strategic_rationale":"Observed: the fixture lacks this result."},{"title":"Two"}]}`, "proposal 1: strategic_rationale is required"},
		{"blank_rationale", `{"proposals":[{"title":"One","strategic_rationale":" \n "},{"title":"Two","strategic_rationale":"Observed: the fixture lacks this result."}]}`, "proposal 0: strategic_rationale is required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := runMC(t, env, test.batch, "strategist", "propose", "--run", run, "--batch", "-")
			if result.code != 1 || !strings.Contains(result.stderr, test.message) {
				t.Fatalf("result = %v; %s", result.json, result.stderr)
			}
			if got := queryInt(t, db, `SELECT count(*) FROM tasks`); got != 0 {
				t.Fatalf("rejected batch created %d tasks", got)
			}
			if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project='ws-test'`); got != run {
				t.Fatalf("rejected batch changed the lease: %q", got)
			}
		})
	}
	created := runMC(t, env, `{"proposals":[{"title":"One","scope":"task","strategic_rationale":"Observed: the fixture lacks this result."},{"title":"Two","strategic_rationale":"Observed: the fixture lacks this result."}]}`,
		"strategist", "propose", "--run", run, "--batch", "-")
	if created.code != 0 {
		t.Fatalf("two proposals: %s", created.stderr)
	}
	ids := created.json["task_ids"].([]any)
	if len(ids) != 2 {
		t.Fatalf("task identifiers = %v", ids)
	}
	effect = dispatchExpect(t, spine, "spawn")
	if effect["role"] != "arbiter" {
		t.Fatalf("proposal selection role = %v", effect["role"])
	}
	run = effect["run_id"].(string)
	env = runJSONEnv(t, spine, run, "pipeline", "arbiter")
	partial := fmt.Sprintf(`{"verdicts":[{"task":%v,"decision":"promote"}]}`, ids[0])
	if result := runMC(t, env, partial, "arbiter", "decide", "--run", run, "--batch", "-"); result.code != 1 {
		t.Fatalf("partial pool accepted: %v; %s", result.json, result.stderr)
	}
	complete := fmt.Sprintf(`{"verdicts":[{"task":%v,"decision":"promote"},{"task":%v,"decision":"reject","reason":"The first task provides more value."}]}`, ids[0], ids[1])
	result := runMC(t, env, complete, "arbiter", "decide", "--run", run, "--batch", "-")
	if result.code != 0 {
		t.Fatalf("complete pool: %s", result.stderr)
	}
	if got := queryInt(t, db, `SELECT count(*) FROM tasks WHERE status='seeded' AND scope='task' AND archived=0`); got != 1 {
		t.Fatalf("selected tasks = %d", got)
	}
}

func TestStrategistProposeRefusesAnotherProject(t *testing.T) {
	spine := initSpine(t)
	eff := dispatchExpect(t, spine, "spawn")
	if eff["role"] != "strategist(propose)" || eff["project"] != "ws-test" {
		t.Fatalf("dispatch = %v, want strategist(propose) for ws-test", eff)
	}
	run := eff["run_id"].(string)
	env := runJSONEnv(t, spine, run, "pipeline", "strategist(propose)")
	db := openDB(t, spine)
	addProject(t, spine, "ws-other", "Other")
	res := runMC(t, env, `{"proposals":[
		{"title":"kept in this pool","strategic_rationale":"Observed: the fixture lacks this result."},
		{"project":"ws-other","title":"filed elsewhere","strategic_rationale":"Observed: the fixture lacks this result."}]}`,
		"strategist", "propose", "--run", run, "--batch", "-")
	if res.code != 1 || !strings.Contains(res.stderr, "inserts into its pool only") {
		t.Fatalf("exit = %d stderr %q, want 1 naming the run's Project", res.code, res.stderr)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM tasks`); got != 0 {
		t.Fatalf("refused batch inserted %d tasks", got)
	}
	if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != run {
		t.Fatalf("refused batch disturbed lease: %q", got)
	}
}

func TestStrategistProposeStrictBatchJSON(t *testing.T) {
	for _, tc := range []struct {
		name  string
		batch string
	}{
		{name: "unknown_nested_field", batch: `{"proposals":[{"project":"ws-test","title":"idea","surprise":true}]}`},
		{name: "trailing_value", batch: `{"proposals":[]} {}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spine := initSpine(t)
			eff := dispatchExpect(t, spine, "spawn")
			run := eff["run_id"].(string)
			env := runJSONEnv(t, spine, run, "pipeline", "strategist(propose)")
			res := runMC(t, env, tc.batch,
				"strategist", "propose", "--run", run, "--batch", "-")
			if res.code != 1 || !strings.Contains(res.stderr, "bad batch payload") {
				t.Fatalf("exit = %d stderr %q", res.code, res.stderr)
			}
			db := openDB(t, spine)
			if got := queryInt(t, db, `SELECT COUNT(*) FROM tasks`); got != 0 {
				t.Fatalf("invalid JSON inserted %d tasks", got)
			}
			if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != run {
				t.Fatalf("invalid JSON disturbed lease: %q", got)
			}
		})
	}
}

func TestStrategistProposeRoleAndSubjectFence(t *testing.T) {
	for _, role := range []string{"worker", "arbiter"} {
		t.Run(role, func(t *testing.T) {
			spine := initSpine(t)
			eff := dispatchExpect(t, spine, "spawn")
			run := eff["run_id"].(string)
			res := runMC(t, runJSONEnv(t, spine, run, "pipeline", role),
				`{"proposals":[]}`, "strategist", "propose", "--run", run, "--batch", "-")
			if res.code != 1 || !strings.Contains(res.stderr, "role mismatch") {
				t.Fatalf("exit = %d stderr %q", res.code, res.stderr)
			}
			db := openDB(t, spine)
			if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != run {
				t.Fatalf("wrong role disturbed lease: %q", got)
			}
		})
	}

	t.Run("subject_carrying_lease_refused", func(t *testing.T) {
		spine := initSpine(t)
		eff := dispatchExpect(t, spine, "spawn")
		run := eff["run_id"].(string)
		db := openDB(t, spine)
		res, err := db.Exec(`INSERT INTO tasks (title, project) VALUES ('fixture', 'ws-test')`)
		if err != nil {
			t.Fatal(err)
		}
		taskID, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE leases SET subject = ? WHERE project = 'ws-test'`, taskID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE runs SET subject = ? WHERE id = ?`, taskID, run); err != nil {
			t.Fatal(err)
		}
		got := runMC(t, runJSONEnv(t, spine, run, "pipeline", "strategist(propose)"),
			`{"proposals":[]}`, "strategist", "propose", "--run", run, "--batch", "-")
		if got.code != 1 || !strings.Contains(got.stderr, "subjectless") {
			t.Fatalf("exit = %d stderr %q", got.code, got.stderr)
		}
		if lockRun := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); lockRun != run {
			t.Fatalf("subject-shape refusal disturbed lease: %q", lockRun)
		}
	})
}

// `mc collie start` is host-scope only, and the brokered lane is the ONLY one
// production reaches, so the fence must sit on the brokered lane and not only
// on the local path. It must refuse before the broker resolves routing or
// touches Docker, which is also what lets this test stay Docker-free.
func TestCollieStartBrokerRefusesRunJSONCallers(t *testing.T) {
	agentRun := filepath.Join(t.TempDir(), "run.json")
	if err := os.WriteFile(agentRun,
		[]byte(`{"run_id":"r","tier":"pipeline","role":"worker"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	denied := runMC(t, []string{
		"MC_HELPER=mc-helper-that-must-never-be-reached",
		"MC_RUN_JSON=" + agentRun,
	}, "", "collie", "start", "--from", "dashboard:ops")
	if denied.code != 1 || !strings.Contains(denied.stderr, "host scope only") {
		t.Fatalf("brokered run.json caller = code %d stderr %q", denied.code, denied.stderr)
	}
}

func TestAttachmentTreePrepareStaysHostLocal(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	res := runMC(t, []string{
		"MC_HOME=" + home,
		"MC_HELPER=mc-helper-that-must-never-be-reached",
	}, "", "__attachment-tree-prepare", "h-tree")
	if res.code != 0 {
		t.Fatalf("prepare = code %d stderr %q", res.code, res.stderr)
	}
	root := filepath.Join(home, "attachments", "h-tree")
	if res.json["session"] != "h-tree" ||
		res.json["inbound_published"] != filepath.Join(root, "in", "published") ||
		res.json["outbound"] != filepath.Join(root, "out") {
		t.Fatalf("prepare receipt = %#v", res.json)
	}
	for _, path := range []string{
		filepath.Join(root, "in", "published", "sha256"),
		filepath.Join(root, "out", "published", "sha256"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("prepared path %q: %v", path, err)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("prepared path %q = mode %v", path, info.Mode())
		}
	}
}

func TestCollieStartBindList(t *testing.T) {
	start := func(t *testing.T, spine, from string, extra ...string) mcResult {
		t.Helper()
		args := []string{"collie", "start", "--from", from}
		args = append(args, extra...)
		res := runMC(t, spineEnv(spine), "", args...)
		if res.code != 0 {
			t.Fatalf("collie start failed (%d): %s", res.code, res.stderr)
		}
		return res
	}
	configureCustomCollie := func(t *testing.T, spine string, enabled bool) {
		t.Helper()
		home := filepath.Dir(spine)
		path := filepath.Join(home, "config.toml")
		cfg, err := operatorconfig.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Runtimes.Bindings["operator-collie"] = operatorconfig.RuntimeBinding{
			Harness: "codex", Model: "operator-collie-model", Credential: "chatgpt",
		}
		if enabled {
			cfg.Runtimes.EnabledBindings = append(cfg.Runtimes.EnabledBindings, "operator-collie")
		}
		if _, err := operatorconfig.WriteAtomic(path, cfg); err != nil {
			t.Fatal(err)
		}
		routes := `| role | harness | binding |
| --- | --- | --- |
| strategist | claude-sdk | claude |
| arbiter | codex | chatgpt |
| worker | claude-sdk | endpoint |
| verifier | codex | chatgpt |
| packager | claude-sdk | endpoint |
| collie | codex | operator-collie |
`
		if err := os.WriteFile(filepath.Join(home, "routing.md"), []byte(routes), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("configured_custom_binding_is_frozen_and_disabled_binding_is_inert", func(t *testing.T) {
		spine := initSpine(t)
		configureCustomCollie(t, spine, true)
		started := start(t, spine, "dashboard:custom")
		if started.json["binding"] != "codex/operator-collie" {
			t.Fatalf("custom Collie route = %v", started.json)
		}
		db := openDB(t, spine)
		if got := queryStr(t, db, `SELECT binding FROM collie_sessions`); got != "codex/operator-collie" {
			t.Fatalf("historical custom Collie binding = %q", got)
		}

		disabledSpine := initSpine(t)
		configureCustomCollie(t, disabledSpine, false)
		refused := runMC(t, spineEnv(disabledSpine), "", "collie", "start", "--from", "dashboard:disabled")
		if refused.code != 1 || !strings.Contains(refused.stderr, "routing") {
			t.Fatalf("disabled Collie binding = code %d stderr %q", refused.code, refused.stderr)
		}
		disabledDB := openDB(t, disabledSpine)
		if n := queryInt(t, disabledDB, `SELECT COUNT(*) FROM collie_sessions`); n != 0 {
			t.Fatalf("disabled Collie route created %d partial sessions", n)
		}
	})

	t.Run("start_is_atomic_registry_state_and_never_touches_pipeline_lease", func(t *testing.T) {
		spine := initSpine(t)
		proposalTask(t, spine, "keep the pipeline lease live")
		pipeline := dispatchExpect(t, spine, "spawn")
		db := openDB(t, spine)
		lockSnapshot := func() string {
			return queryStr(t, db, `SELECT
				COALESCE(run_id, '') || '|' || COALESCE(owner, '') || '|' ||
				COALESCE(subject, '') || '|' || project || '|' ||
				COALESCE(acquired_at, '') || '|' || COALESCE(last_heartbeat_at, '') || '|' ||
				COALESCE(boot_id, '') || '|' || COALESCE(acquired_uptime_ms, '') || '|' ||
				COALESCE(heartbeat_uptime_ms, '') || '|' ||
				COALESCE(deadline_uptime_ms, '') FROM leases WHERE project = 'ws-test'`)
		}
		before := lockSnapshot()

		res := start(t, spine, "dashboard:channel-1")
		if _, exists := res.json["action"]; exists {
			t.Fatalf("collie start returned a host effect: %v", res.json)
		}
		session := res.json["session_id"].(string)
		if !strings.HasPrefix(session, "h-") || res.json["status"] != "active" ||
			res.json["session_path"] != "sessions/"+session || res.json["binding"] != "fake/fake" {
			t.Fatalf("collie start result = %v", res.json)
		}
		if got := lockSnapshot(); got != before {
			t.Fatalf("collie start disturbed the pipeline lease:\n before %s\n after  %s", before, got)
		}
		if n := queryInt(t, db, `SELECT COUNT(*) FROM runs WHERE role IS NOT 'scribe'`); n != 1 {
			t.Fatalf("collie start opened a Runs row: %d", n)
		}
		if got := queryStr(t, db, `SELECT id FROM runs WHERE role IS NOT 'scribe'`); got != pipeline["run_id"] {
			t.Fatalf("pipeline Run changed: %q", got)
		}
		if n := queryInt(t, db, `SELECT COUNT(*) FROM outbox`); n != 0 {
			t.Fatalf("collie start emitted %d outbox rows", n)
		}
		if got := queryStr(t, db, `SELECT status || '|' || container_name || '|' ||
			session_path || '|' || binding FROM collie_sessions WHERE id = ?`, session); got != "active|mc-collie-"+session+"|sessions/"+session+"|fake/fake" {
			t.Fatalf("registry row = %q", got)
		}
		allowJSON, _ := json.Marshal(defaultCollieAllowlist)
		if got := queryStr(t, db, `SELECT verb_allowlist FROM collie_sessions WHERE id = ?`, session); got != string(allowJSON) {
			t.Fatalf("frozen default allowlist = %q, want %s", got, allowJSON)
		}
		if got := queryStr(t, db, `SELECT surface || ':' || channel_ref FROM collie_bindings WHERE active = 1`); got != "dashboard:channel-1" {
			t.Fatalf("initial binding = %q", got)
		}
		if got := queryStr(t, db, `SELECT native_session_ref || '/' || trace_filename FROM collie_sessions WHERE id = ?`, session); got != "<NULL>" {
			t.Fatalf("start guessed runner locators: %q", got)
		}

		listed := runMC(t, spineEnv(spine), "", "collie", "list")
		if listed.code != 0 {
			t.Fatalf("collie list failed: %s", listed.stderr)
		}
		sessions := listed.json["sessions"].([]any)
		if len(sessions) != 1 {
			t.Fatalf("collie list = %v", listed.json)
		}
		row := sessions[0].(map[string]any)
		if row["id"] != session || row["binding"] != "fake/fake" || row["status"] != "active" {
			t.Fatalf("listed session = %v", row)
		}
		bindings := row["active_bindings"].([]any)
		bindingID := queryStr(t, db, `SELECT CAST(id AS TEXT) FROM collie_bindings
			WHERE session_id=? AND active=1`, session)
		if len(bindings) != 1 ||
			bindings[0].(map[string]any)["channel_ref"] != "channel-1" ||
			bindings[0].(map[string]any)["binding_id"] != bindingID {
			t.Fatalf("listed active bindings = %v", bindings)
		}
		known := row["known_bindings"].([]any)
		if len(known) != 1 ||
			known[0].(map[string]any)["channel_ref"] != "channel-1" ||
			known[0].(map[string]any)["binding_id"] != bindingID {
			t.Fatalf("listed known bindings = %v", known)
		}
	})

	t.Run("canonical_active_session_fences_Collie_operator_mutations", func(t *testing.T) {
		spine := initSpine(t)
		session := start(t, spine, "dashboard:operator").json["session_id"].(string)
		env := collieJSONEnv(t, spine, session, defaultCollieAllowlist)

		create := func(operation, title string) string {
			return fmt.Sprintf(`{"operation_id":%q,"action":"create","scope":"task","project":"ws-test","title":%q}`, operation, title)
		}
		added := runMC(t, env, create("collie-relay", "Collie relays operator intent"), "board", "write")
		if added.code != 0 {
			t.Fatalf("allowlisted Collie task creation failed: %s", added.stderr)
		}
		taskID := int64(added.json["task_id"].(float64))
		blocked := runMC(t, env, "", "task", "block", fmt.Sprint(taskID), "--reason", "operator needs to decide")
		if blocked.code != 0 {
			t.Fatalf("allowlisted Collie task block failed: %s", blocked.stderr)
		}

		db := openDB(t, spine)
		beforeTasks := queryInt(t, db, `SELECT COUNT(*) FROM tasks`)
		forged := runMC(t, collieJSONEnv(t, spine, session, []string{"board.write"}),
			create("collie-forged", "forged envelope"), "board", "write")
		if forged.code != 1 || !strings.Contains(forged.stderr, "does not match") {
			t.Fatalf("forged allowlist = code %d stderr %q", forged.code, forged.stderr)
		}
		if queryInt(t, db, `SELECT COUNT(*) FROM tasks`) != beforeTasks {
			t.Fatal("forged Collie allowlist inserted a task")
		}

		if _, err := db.Exec(`UPDATE collie_sessions SET status = 'ended' WHERE id = ?`, session); err != nil {
			t.Fatal(err)
		}
		stale := runMC(t, env, create("collie-stale", "zombie write"), "board", "write")
		if stale.code != 1 || !strings.Contains(stale.stderr, "not active") {
			t.Fatalf("ended Collie write = code %d stderr %q", stale.code, stale.stderr)
		}
		if queryInt(t, db, `SELECT COUNT(*) FROM tasks`) != beforeTasks {
			t.Fatal("ended Collie inserted a task")
		}

		narrow := start(t, spine, "cli:narrow", "--allow", "board.write").json["session_id"].(string)
		narrowEnv := collieJSONEnv(t, spine, narrow, []string{"board.write"})
		if got := runMC(t, narrowEnv, create("collie-narrow", "narrow allowed"), "board", "write"); got.code != 0 {
			t.Fatalf("narrow canonical Collie task creation failed: %s", got.stderr)
		}
		if got := runMC(t, narrowEnv, "", "task", "unblock", fmt.Sprint(taskID), "--reason", "The operator resolved the block."); got.code != 1 || !strings.Contains(got.stderr, "not allowed") {
			t.Fatalf("narrow forbidden verb = code %d stderr %q", got.code, got.stderr)
		}
	})

	t.Run("bind_is_idempotent_but_never_transfers_a_live_place", func(t *testing.T) {
		spine := initSpine(t)
		a := start(t, spine, "dashboard:a").json["session_id"].(string)
		b := start(t, spine, "cli:b").json["session_id"].(string)
		db := openDB(t, spine)

		bind := runMC(t, spineEnv(spine), "", "collie", "bind", a, "--from", "discord:shared")
		if bind.code != 0 || bind.json["bound"] != true {
			t.Fatalf("first bind = code %d json %v stderr %q", bind.code, bind.json, bind.stderr)
		}
		replay := runMC(t, spineEnv(spine), "", "collie", "bind", a, "--from", "discord:shared")
		if replay.code != 0 || replay.json["bound"] != false {
			t.Fatalf("bind replay = code %d json %v stderr %q", replay.code, replay.json, replay.stderr)
		}
		if n := queryInt(t, db, `SELECT COUNT(*) FROM collie_bindings
			WHERE session_id = ? AND surface = 'discord' AND channel_ref = 'shared'`, a); n != 1 {
			t.Fatalf("bind replay appended history: %d", n)
		}

		conflict := runMC(t, spineEnv(spine), "", "collie", "bind", b, "--from", "discord:shared")
		if conflict.code != 1 || !strings.Contains(conflict.stderr, "already bound") {
			t.Fatalf("cross-session bind = code %d stderr %q", conflict.code, conflict.stderr)
		}
		if n := queryInt(t, db, `SELECT COUNT(*) FROM collie_bindings WHERE surface = 'discord' AND channel_ref = 'shared'`); n != 1 {
			t.Fatalf("conflicting bind changed history: %d", n)
		}

		unknown := runMC(t, spineEnv(spine), "", "collie", "bind", "h-missing", "--from", "dashboard:x")
		if unknown.code != 1 || !strings.Contains(unknown.stderr, "unknown Collie session") {
			t.Fatalf("unknown bind = code %d stderr %q", unknown.code, unknown.stderr)
		}
		if _, err := db.Exec(`UPDATE collie_sessions SET status = 'ended' WHERE id = ?`, a); err != nil {
			t.Fatal(err)
		}
		ended := runMC(t, spineEnv(spine), "", "collie", "bind", a, "--from", "dashboard:new")
		if ended.code != 1 || !strings.Contains(ended.stderr, "requires an active") {
			t.Fatalf("ended bind = code %d stderr %q", ended.code, ended.stderr)
		}
	})

	t.Run("scope_is_host_for_start_bind_and_own_session_for_collie_list", func(t *testing.T) {
		spine := initSpine(t)
		a := start(t, spine, "dashboard:a").json["session_id"].(string)
		b := start(t, spine, "cli:b", "--allow", "task.block").json["session_id"].(string)
		db := openDB(t, spine)
		beforeSessions := queryInt(t, db, `SELECT COUNT(*) FROM collie_sessions`)
		beforeBindings := queryInt(t, db, `SELECT COUNT(*) FROM collie_bindings`)

		pipelineEnv := runJSONEnv(t, spine, "pipeline-forgery", "pipeline", "worker")
		for _, args := range [][]string{
			{"collie", "start", "--from", "dashboard:pipeline"},
			{"collie", "bind", a, "--from", "dashboard:pipeline"},
			{"collie", "list"},
		} {
			got := runMC(t, pipelineEnv, "", args...)
			if got.code != 1 {
				t.Fatalf("pipeline %v exit = %d stderr %q", args, got.code, got.stderr)
			}
		}

		// Structural host-only rules win even over a forged/overbroad envelope.
		overbroad := collieJSONEnv(t, spine, a, []string{"collie.start", "collie.bind", "collie.list"})
		for _, args := range [][]string{
			{"collie", "start", "--from", "dashboard:forged"},
			{"collie", "bind", a, "--from", "dashboard:forged"},
		} {
			got := runMC(t, overbroad, "", args...)
			if got.code != 1 {
				t.Fatalf("Collie %v exit = %d stderr %q", args, got.code, got.stderr)
			}
		}
		if queryInt(t, db, `SELECT COUNT(*) FROM collie_sessions`) != beforeSessions ||
			queryInt(t, db, `SELECT COUNT(*) FROM collie_bindings`) != beforeBindings {
			t.Fatal("scope refusal mutated Collie registry")
		}

		own := runMC(t, collieJSONEnv(t, spine, a, defaultCollieAllowlist), "", "collie", "list")
		if own.code != 0 {
			t.Fatalf("own Collie list failed: %s", own.stderr)
		}
		rows := own.json["sessions"].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["id"] != a {
			t.Fatalf("Collie list escaped own session: %v", own.json)
		}
		denied := runMC(t, collieJSONEnv(t, spine, b, []string{"task.block"}), "", "collie", "list")
		if denied.code != 1 || !strings.Contains(denied.stderr, "not allowed") {
			t.Fatalf("narrow Collie list = code %d stderr %q", denied.code, denied.stderr)
		}
		if _, err := db.Exec(`UPDATE collie_sessions SET status = 'ended' WHERE id = ?`, a); err != nil {
			t.Fatal(err)
		}
		stale := runMC(t, collieJSONEnv(t, spine, a, defaultCollieAllowlist), "", "collie", "list")
		if stale.code != 1 || !strings.Contains(stale.stderr, "not active") {
			t.Fatalf("ended Collie list = code %d stderr %q", stale.code, stale.stderr)
		}

		host := runMC(t, spineEnv(spine), "", "collie", "list")
		if host.code != 0 || len(host.json["sessions"].([]any)) != 2 {
			t.Fatalf("host list omitted resumable rows: code %d json %v stderr %q", host.code, host.json, host.stderr)
		}
	})

	t.Run("invalid_inputs_routing_and_binding_abort_leave_no_partial_session", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			args []string
			msg  string
		}{
			{name: "missing_separator", args: []string{"--from", "dashboard"}, msg: "surface:channel_ref"},
			{name: "unknown_surface", args: []string{"--from", "email:x"}, msg: "discord|dashboard|cli"},
			{name: "empty_channel", args: []string{"--from", "dashboard:"}, msg: "channel_ref"},
			{name: "unknown_allow", args: []string{"--from", "dashboard:x", "--allow", "dispatch"}, msg: "not a Collie-agent verb"},
			{name: "task_add_allow", args: []string{"--from", "dashboard:x", "--allow", "task.add"}, msg: "not a Collie-agent verb"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				spine := initSpine(t)
				got := runMC(t, spineEnv(spine), "", append([]string{"collie", "start"}, tc.args...)...)
				if got.code == 0 || !strings.Contains(got.stderr, tc.msg) {
					t.Fatalf("exit = %d stderr %q, want failure containing %q", got.code, got.stderr, tc.msg)
				}
				db := openDB(t, spine)
				if queryInt(t, db, `SELECT COUNT(*) FROM collie_sessions`) != 0 ||
					queryInt(t, db, `SELECT COUNT(*) FROM collie_bindings`) != 0 {
					t.Fatal("invalid start left partial registry state")
				}
			})
		}

		t.Run("bad_route", func(t *testing.T) {
			spine := initSpine(t)
			if err := os.WriteFile(filepath.Join(filepath.Dir(spine), "routing.md"), []byte("broken\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			got := runMC(t, spineEnv(spine), "", "collie", "start", "--from", "dashboard:x")
			if got.code == 0 || !strings.Contains(got.stderr, "routing.md") {
				t.Fatalf("bad-route start = code %d stderr %q", got.code, got.stderr)
			}
			db := openDB(t, spine)
			if queryInt(t, db, `SELECT COUNT(*) FROM collie_sessions`) != 0 {
				t.Fatal("bad route opened a Collie session")
			}
		})

		t.Run("binding_insert_failure_rolls_back_registry", func(t *testing.T) {
			spine := initSpine(t)
			db := openDB(t, spine)
			if _, err := db.Exec(`CREATE TRIGGER test_fail_collie_binding
				BEFORE INSERT ON collie_bindings
				BEGIN SELECT RAISE(ABORT, 'injected binding failure'); END`); err != nil {
				t.Fatal(err)
			}
			got := runMC(t, spineEnv(spine), "", "collie", "start", "--from", "dashboard:x")
			if got.code != 1 || !strings.Contains(got.stderr, "injected binding failure") {
				t.Fatalf("atomic start = code %d stderr %q", got.code, got.stderr)
			}
			if queryInt(t, db, `SELECT COUNT(*) FROM collie_sessions`) != 0 ||
				queryInt(t, db, `SELECT COUNT(*) FROM collie_bindings`) != 0 {
				t.Fatal("failed initial binding left an orphan Collie session")
			}
		})

		t.Run("already_bound_origin_does_not_start_another_session", func(t *testing.T) {
			spine := initSpine(t)
			first := start(t, spine, "dashboard:x").json["session_id"]
			got := runMC(t, spineEnv(spine), "", "collie", "start", "--from", "dashboard:x")
			if got.code != 1 || !strings.Contains(got.stderr, "already bound") {
				t.Fatalf("duplicate-origin start = code %d stderr %q", got.code, got.stderr)
			}
			db := openDB(t, spine)
			if queryInt(t, db, `SELECT COUNT(*) FROM collie_sessions`) != 1 ||
				queryStr(t, db, `SELECT id FROM collie_sessions`) != first {
				t.Fatal("duplicate-origin start created a second session")
			}
		})
	})
}

func TestCollieSendHistoryEnd(t *testing.T) {
	start := func(t *testing.T, spine, from string, allow ...string) string {
		t.Helper()
		args := []string{"collie", "start", "--from", from}
		if len(allow) != 0 {
			args = append(args, "--allow", strings.Join(allow, ","))
		}
		res := runMC(t, spineEnv(spine), "", args...)
		if res.code != 0 {
			t.Fatalf("collie start failed: %s", res.stderr)
		}
		return res.json["session_id"].(string)
	}
	bind := func(t *testing.T, spine, session, from string) {
		t.Helper()
		res := runMC(t, spineEnv(spine), "", "collie", "bind", session, "--from", from)
		if res.code != 0 {
			t.Fatalf("collie bind failed: %s", res.stderr)
		}
	}

	t.Run("send_binds_origin_appends_stable_history_and_echoes_other_surfaces", func(t *testing.T) {
		spine := initSpine(t)
		// Conversation traffic is lease-free and must coexist with a live
		// leased pipeline run. The run is spawned before the session exists:
		// a started session is woken on the next tick, and that wake would
		// join this tick's action list.
		proposalTask(t, spine, "pipeline stays live during chat")
		dispatchExpect(t, spine, "spawn")
		session := start(t, spine, "dashboard:dash")
		bind(t, spine, session, "discord:disc")
		db := openDB(t, spine)
		lockBefore := queryStr(t, db, `SELECT run_id || '|' || owner || '|' || subject FROM leases WHERE project = 'ws-test'`)
		if _, err := db.Exec(`UPDATE collie_sessions SET last_activity_at = datetime('now', '-1 day') WHERE id = ?`, session); err != nil {
			t.Fatal(err)
		}
		oldActivity := queryStr(t, db, `SELECT last_activity_at FROM collie_sessions WHERE id = ?`, session)

		first := runMC(t, spineEnv(spine), "", "collie", "send", session,
			"--from", "cli:terminal", "--body", "hello from CLI")
		if first.code != 0 {
			t.Fatalf("collie send failed: %s", first.stderr)
		}
		if first.json["seq"] != float64(1) || first.json["echoes"] != float64(2) {
			t.Fatalf("first send result = %v", first.json)
		}
		if got := queryStr(t, db, `SELECT direction || '|' || surface || '|' || channel_ref || '|' || body || '|' || COALESCE(attachments, '<null>')
			FROM conversation_messages WHERE session_id = ? AND seq = 1`, session); got != `inbound|cli|terminal|hello from CLI|<null>` {
			t.Fatalf("first conversation row = %q", got)
		}
		if got := queryStr(t, db, `SELECT last_activity_at FROM collie_sessions WHERE id = ?`, session); got == oldActivity {
			t.Fatalf("send did not advance last_activity_at from %q", oldActivity)
		}
		if n := queryInt(t, db, `SELECT COUNT(*) FROM collie_bindings WHERE session_id = ? AND active = 1`, session); n != 3 {
			t.Fatalf("origin traffic did not bind CLI place: %d bindings", n)
		}
		if got := queryStr(t, db, `SELECT group_concat(surface || ':' || channel_ref, ',') FROM
			(SELECT surface, channel_ref FROM outbox WHERE kind = 'collie_echo' ORDER BY surface, channel_ref)`); got != "dashboard:dash,discord:disc" {
			t.Fatalf("echo destinations = %q", got)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(queryStr(t, db, `SELECT payload FROM outbox ORDER BY id LIMIT 1`)), &payload); err != nil {
			t.Fatal(err)
		}
		origin := payload["origin"].(map[string]any)
		if payload["body"] != "hello from CLI" || payload["seq"] != float64(1) ||
			origin["surface"] != "cli" || origin["channel_ref"] != "terminal" {
			t.Fatalf("echo payload = %v", payload)
		}
		if got := queryStr(t, db, `SELECT run_id || '|' || owner || '|' || subject FROM leases WHERE project = 'ws-test'`); got != lockBefore {
			t.Fatalf("send disturbed pipeline lease: before %q after %q", lockBefore, got)
		}

		second := runMC(t, spineEnv(spine), "", "collie", "send", session,
			"--from", "dashboard:dash", "--body", "second turn")
		if second.code != 0 || second.json["seq"] != float64(2) || second.json["echoes"] != float64(2) {
			t.Fatalf("second send = code %d json %v stderr %q", second.code, second.json, second.stderr)
		}

		history := runMC(t, spineEnv(spine), "", "collie", "history", session)
		if history.code != 0 {
			t.Fatalf("collie history failed: %s", history.stderr)
		}
		messages := history.json["messages"].([]any)
		if len(messages) != 2 {
			t.Fatalf("history = %v", history.json)
		}
		m1, m2 := messages[0].(map[string]any), messages[1].(map[string]any)
		if m1["seq"] != float64(1) || m2["seq"] != float64(2) || m2["body"] != "second turn" {
			t.Fatalf("history order/content = %v", messages)
		}
		if got := m1["attachments"].([]any); len(got) != 0 {
			t.Fatalf("attachment-free history row = %v", got)
		}
		if got := m2["attachments"].([]any); len(got) != 0 {
			t.Fatalf("attachment-free history row = %v", got)
		}
	})

	t.Run("send_outbox_failure_rolls_back_binding_message_and_activity", func(t *testing.T) {
		spine := initSpine(t)
		session := start(t, spine, "dashboard:dash")
		bind(t, spine, session, "discord:disc")
		db := openDB(t, spine)
		beforeActivity := queryStr(t, db, `SELECT last_activity_at FROM collie_sessions WHERE id = ?`, session)
		if _, err := db.Exec(`CREATE TRIGGER test_fail_collie_echo
			BEFORE INSERT ON outbox WHEN NEW.kind = 'collie_echo'
			BEGIN SELECT RAISE(ABORT, 'injected echo failure'); END`); err != nil {
			t.Fatal(err)
		}
		res := runMC(t, spineEnv(spine), "", "collie", "send", session,
			"--from", "cli:new-origin", "--body", "must roll back")
		if res.code != 1 || !strings.Contains(res.stderr, "injected echo failure") {
			t.Fatalf("send atomic failure = code %d stderr %q", res.code, res.stderr)
		}
		if queryInt(t, db, `SELECT COUNT(*) FROM conversation_messages`) != 0 ||
			queryInt(t, db, `SELECT COUNT(*) FROM outbox`) != 0 {
			t.Fatal("failed send left a message or outbox row")
		}
		if queryInt(t, db, `SELECT COUNT(*) FROM collie_bindings WHERE channel_ref = 'new-origin'`) != 0 {
			t.Fatal("failed send left the implicit origin binding")
		}
		if got := queryStr(t, db, `SELECT last_activity_at FROM collie_sessions WHERE id = ?`, session); got != beforeActivity {
			t.Fatalf("failed send changed last_activity_at: before %q after %q", beforeActivity, got)
		}
	})

	t.Run("end_activity_failure_rolls_back_status_and_binding_deactivation", func(t *testing.T) {
		spine := initSpine(t)
		session := start(t, spine, "dashboard:dash")
		db := openDB(t, spine)
		if _, err := db.Exec(`CREATE TRIGGER test_fail_collie_end_activity
			BEFORE INSERT ON activity WHEN NEW.kind = 'collie.ended'
			BEGIN SELECT RAISE(ABORT, 'injected end activity failure'); END`); err != nil {
			t.Fatal(err)
		}
		res := runMC(t, spineEnv(spine), "", "collie", "end", session, "--reason", "must roll back")
		if res.code != 1 || !strings.Contains(res.stderr, "injected end activity failure") {
			t.Fatalf("end atomic failure = code %d stderr %q", res.code, res.stderr)
		}
		if got := queryStr(t, db, `SELECT status FROM collie_sessions WHERE id = ?`, session); got != "active" {
			t.Fatalf("failed end changed status: %q", got)
		}
		if n := queryInt(t, db, `SELECT COUNT(*) FROM collie_bindings WHERE session_id = ? AND active = 1`, session); n != 1 {
			t.Fatalf("failed end deactivated bindings: %d active", n)
		}
		if n := queryInt(t, db, `SELECT COUNT(*) FROM activity WHERE kind = 'collie.ended'`); n != 0 {
			t.Fatalf("failed end left %d activity rows", n)
		}
	})

	t.Run("scope_history_and_end_are_own_session_fenced", func(t *testing.T) {
		spine := initSpine(t)
		a := start(t, spine, "dashboard:a")
		b := start(t, spine, "cli:b")
		if got := runMC(t, spineEnv(spine), "", "collie", "send", a,
			"--from", "dashboard:a", "--body", "visible only in A"); got.code != 0 {
			t.Fatalf("send fixture failed: %s", got.stderr)
		}
		db := openDB(t, spine)
		beforeMessages := queryInt(t, db, `SELECT COUNT(*) FROM conversation_messages`)

		pipeline := runJSONEnv(t, spine, "pipeline", "pipeline", "worker")
		for _, args := range [][]string{
			{"collie", "send", a, "--from", "dashboard:a", "--body", "forged"},
			{"collie", "history", a},
			{"collie", "end", a, "--reason", "forged"},
		} {
			got := runMC(t, pipeline, "", args...)
			if got.code != 1 {
				t.Fatalf("pipeline %v exit = %d stderr %q", args, got.code, got.stderr)
			}
		}
		if queryInt(t, db, `SELECT COUNT(*) FROM conversation_messages`) != beforeMessages ||
			queryStr(t, db, `SELECT status FROM collie_sessions WHERE id = ?`, a) != "active" {
			t.Fatal("pipeline Collie verb mutated state")
		}

		collieA := collieJSONEnv(t, spine, a, defaultCollieAllowlist)
		forgedSend := runMC(t, collieA, "", "collie", "send", a,
			"--from", "dashboard:a", "--body", "agent cannot inject transport")
		if forgedSend.code != 1 {
			t.Fatalf("Collie-agent send exit = %d stderr %q", forgedSend.code, forgedSend.stderr)
		}
		ownHistory := runMC(t, collieA, "", "collie", "history", a)
		if ownHistory.code != 0 || len(ownHistory.json["messages"].([]any)) != 1 {
			t.Fatalf("own history = code %d json %v stderr %q", ownHistory.code, ownHistory.json, ownHistory.stderr)
		}
		otherHistory := runMC(t, collieA, "", "collie", "history", b)
		if otherHistory.code != 1 || !strings.Contains(otherHistory.stderr, "own session") {
			t.Fatalf("cross-session history = code %d stderr %q", otherHistory.code, otherHistory.stderr)
		}
		otherEnd := runMC(t, collieA, "", "collie", "end", b, "--reason", "hijack")
		if otherEnd.code != 1 || !strings.Contains(otherEnd.stderr, "own session") {
			t.Fatalf("cross-session end = code %d stderr %q", otherEnd.code, otherEnd.stderr)
		}

		lockBefore := queryStr(t, db, `SELECT COALESCE(run_id, '<free>') FROM leases WHERE project = 'ws-test'`)
		ended := runMC(t, collieA, "", "collie", "end", a, "--reason", "operator done")
		if ended.code != 0 || ended.json["status"] != "ended" || ended.json["ended"] != true {
			t.Fatalf("own end = code %d json %v stderr %q", ended.code, ended.json, ended.stderr)
		}
		if got := queryStr(t, db, `SELECT status FROM collie_sessions WHERE id = ?`, a); got != "ended" {
			t.Fatalf("ended session status = %q", got)
		}
		if n := queryInt(t, db, `SELECT COUNT(*) FROM collie_bindings WHERE session_id = ? AND active = 1`, a); n != 0 {
			t.Fatalf("end left %d active bindings", n)
		}
		if got := queryStr(t, db, `SELECT kind || '|' || subject || '|' || detail FROM activity WHERE kind = 'collie.ended'`); got != "collie.ended|"+a+"|operator done" {
			t.Fatalf("end activity = %q", got)
		}
		if got := queryStr(t, db, `SELECT COALESCE(run_id, '<free>') FROM leases WHERE project = 'ws-test'`); got != lockBefore {
			t.Fatalf("end disturbed pipeline lease: before %q after %q", lockBefore, got)
		}
		stale := runMC(t, collieA, "", "collie", "history", a)
		if stale.code != 1 || !strings.Contains(stale.stderr, "not active") {
			t.Fatalf("ended Collie history = code %d stderr %q", stale.code, stale.stderr)
		}
		hostHistory := runMC(t, spineEnv(spine), "", "collie", "history", a)
		if hostHistory.code != 0 || len(hostHistory.json["messages"].([]any)) != 1 {
			t.Fatalf("host lost ended-session history: code %d json %v", hostHistory.code, hostHistory.json)
		}
		replay := runMC(t, spineEnv(spine), "", "collie", "end", a, "--reason", "operator done")
		if replay.code != 0 || replay.json["ended"] != false || queryInt(t, db, `SELECT COUNT(*) FROM activity WHERE kind = 'collie.ended'`) != 1 {
			t.Fatalf("host end replay = code %d json %v stderr %q", replay.code, replay.json, replay.stderr)
		}
	})

	t.Run("validation_and_cross_session_origin_conflict_are_inert", func(t *testing.T) {
		spine := initSpine(t)
		a := start(t, spine, "dashboard:a")
		b := start(t, spine, "cli:b")
		db := openDB(t, spine)
		for _, tc := range []struct {
			name string
			args []string
			msg  string
		}{
			{name: "missing_body", args: []string{"collie", "send", a, "--from", "dashboard:a"}, msg: "body or attachment"},
			{name: "bad_attachments_json", args: []string{"collie", "send", a, "--from", "dashboard:a", "--attachments", `{}`}, msg: "JSON array"},
			{name: "legacy_attachment_path", args: []string{"collie", "send", a, "--from", "dashboard:a", "--attachments", `["../secret"]`}, msg: "typed attachment publication"},
			{name: "unknown_session", args: []string{"collie", "send", "h-missing", "--from", "dashboard:x", "--body", "x"}, msg: "unknown Collie session"},
			{name: "origin_owned_by_other", args: []string{"collie", "send", a, "--from", "cli:b", "--body", "x"}, msg: "already bound"},
			{name: "history_unknown", args: []string{"collie", "history", "h-missing"}, msg: "unknown Collie session"},
			{name: "end_missing_reason", args: []string{"collie", "end", a}, msg: "requires --reason"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				beforeMessages := queryInt(t, db, `SELECT COUNT(*) FROM conversation_messages`)
				beforeOutbox := queryInt(t, db, `SELECT COUNT(*) FROM outbox`)
				got := runMC(t, spineEnv(spine), "", tc.args...)
				if got.code == 0 || !strings.Contains(got.stderr, tc.msg) {
					t.Fatalf("exit = %d stderr %q, want failure containing %q", got.code, got.stderr, tc.msg)
				}
				if queryInt(t, db, `SELECT COUNT(*) FROM conversation_messages`) != beforeMessages ||
					queryInt(t, db, `SELECT COUNT(*) FROM outbox`) != beforeOutbox {
					t.Fatal("invalid Collie message verb mutated state")
				}
			})
		}
		if _, err := db.Exec(`UPDATE collie_sessions SET status = 'ended' WHERE id = ?`, b); err != nil {
			t.Fatal(err)
		}
		// `mc collie send` binds as `cli` and resumes the bound session if it
		// ended. The container exits when idle by design, so without this
		// the operator's second turn refuses.
		endedSend := runMC(t, spineEnv(spine), "", "collie", "send", b,
			"--from", "cli:b", "--body", "the turn after the container exited")
		if endedSend.code != 0 {
			t.Fatalf("ended send = code %d stderr %q", endedSend.code, endedSend.stderr)
		}
		if got := queryString(t, db,
			`SELECT status FROM collie_sessions WHERE id = ?`, b); got != "active" {
			t.Fatalf("status after implicit resume = %q, want active", got)
		}
		if n := queryInt(t, db,
			`SELECT COUNT(*) FROM activity WHERE kind = 'collie.resumed' AND subject = '`+b+`'`); n != 1 {
			t.Fatalf("collie.resumed rows = %d, want 1", n)
		}

		// Still scoped to the bound place: an ended session named from a
		// place that never bound to it keeps the explicit-resume requirement.
		if _, err := db.Exec(`UPDATE collie_sessions SET status = 'ended' WHERE id = ?`, b); err != nil {
			t.Fatal(err)
		}
		strangerSend := runMC(t, spineEnv(spine), "", "collie", "send", b,
			"--from", "cli:never-bound-here", "--body", "let me in")
		if strangerSend.code != 1 || !strings.Contains(strangerSend.stderr, "use resume") {
			t.Fatalf("unbound send = code %d stderr %q", strangerSend.code, strangerSend.stderr)
		}
		if got := queryString(t, db,
			`SELECT status FROM collie_sessions WHERE id = ?`, b); got != "ended" {
			t.Fatalf("refused send flipped status to %q", got)
		}
	})
}

func TestCollieRunnerRegistersSessionLocators(t *testing.T) {
	spine := initSpine(t)
	started := runMC(t, spineEnv(spine), "", "collie", "start", "--from", "dashboard:reg")
	if started.code != 0 {
		t.Fatalf("collie start failed: %s", started.stderr)
	}
	session := started.json["session_id"].(string)
	runner := runJSONEnv(t, spine, session, "collie", "collie")
	db := openDB(t, spine)

	reg := runMC(t, runner, "", "run", "register-session", session,
		"--native-ref", "native-abc", "--file", "trace.jsonl")
	if reg.code != 0 {
		t.Fatalf("collie runner register-session failed (%d): %s", reg.code, reg.stderr)
	}
	locators := func() string {
		return queryStr(t, db, `SELECT COALESCE(native_session_ref, '<null>') || '|' ||
			COALESCE(trace_filename, '<null>') FROM collie_sessions WHERE id = ?`, session)
	}
	if got := locators(); got != "native-abc|trace.jsonl" {
		t.Fatalf("locators = %q", got)
	}

	replay := runMC(t, runner, "", "run", "register-session", session,
		"--native-ref", "native-abc", "--file", "trace.jsonl")
	if replay.code != 0 {
		t.Fatalf("same-value locator retry rejected: %s", replay.stderr)
	}
	conflict := runMC(t, runner, "", "run", "register-session", session,
		"--native-ref", "native-other", "--file", "other.jsonl")
	if conflict.code != 1 || !strings.Contains(conflict.stderr, "immutable") {
		t.Fatalf("conflicting locators = code %d stderr %q", conflict.code, conflict.stderr)
	}
	if got := locators(); got != "native-abc|trace.jsonl" {
		t.Fatalf("conflict mutated locators: %q", got)
	}

	// Cross-session, pipeline-identity, and host attempts are all inert.
	other := runMC(t, spineEnv(spine), "", "collie", "start", "--from", "cli:reg2")
	if other.code != 0 {
		t.Fatalf("second collie start failed: %s", other.stderr)
	}
	otherSession := other.json["session_id"].(string)
	for name, attempt := range map[string]mcResult{
		"cross-session": runMC(t, runner, "", "run", "register-session", otherSession,
			"--native-ref", "n", "--file", "f"),
		"pipeline-identity": runMC(t, runJSONEnv(t, spine, session, "pipeline", "worker"), "",
			"run", "register-session", session, "--native-ref", "n", "--file", "f"),
		"host": runMC(t, spineEnv(spine), "",
			"run", "register-session", otherSession, "--native-ref", "n", "--file", "f"),
	} {
		if attempt.code != 1 {
			t.Fatalf("%s register = %d stderr %q", name, attempt.code, attempt.stderr)
		}
	}
	if got := queryStr(t, db, `SELECT COALESCE(native_session_ref, '<null>')
		FROM collie_sessions WHERE id = ?`, otherSession); got != "<null>" {
		t.Fatalf("forged registration reached %q: %q", otherSession, got)
	}

	// Registration survives session end: identity, not liveness.
	ended := runMC(t, spineEnv(spine), "", "collie", "end", otherSession, "--reason", "test")
	if ended.code != 0 {
		t.Fatalf("collie end failed: %s", ended.stderr)
	}
	late := runMC(t, runJSONEnv(t, spine, otherSession, "collie", "collie"), "",
		"run", "register-session", otherSession, "--native-ref", "late", "--file", "late.jsonl")
	if late.code != 0 {
		t.Fatalf("post-end locator registration refused: %s", late.stderr)
	}
}

func TestCollieResume(t *testing.T) {
	begin := func(t *testing.T, spine, from string) string {
		t.Helper()
		res := runMC(t, spineEnv(spine), "", "collie", "start", "--from", from)
		if res.code != 0 {
			t.Fatalf("collie start failed: %s", res.stderr)
		}
		return res.json["session_id"].(string)
	}
	register := func(t *testing.T, spine, session string) {
		t.Helper()
		res := runMC(t, runJSONEnv(t, spine, session, "collie", "collie"), "",
			"run", "register-session", session, "--native-ref", "n-"+session, "--file", session+".jsonl")
		if res.code != 0 {
			t.Fatalf("register locators failed: %s", res.stderr)
		}
	}
	end := func(t *testing.T, spine, session string) {
		t.Helper()
		res := runMC(t, spineEnv(spine), "", "collie", "end", session, "--reason", "test drill")
		if res.code != 0 {
			t.Fatalf("collie end failed: %s", res.stderr)
		}
	}

	t.Run("record_only_transition_rebinds_the_requesting_surface", func(t *testing.T) {
		spine := initSpine(t)
		session := begin(t, spine, "dashboard:main")
		register(t, spine, session)
		end(t, spine, session)
		db := openDB(t, spine)

		res := runMC(t, spineEnv(spine), "", "collie", "resume", session, "--from", "discord:ops")
		if res.code != 0 || res.json["resumed"] != true {
			t.Fatalf("resume = code %d json %v stderr %q", res.code, res.json, res.stderr)
		}
		if got := queryStr(t, db, `SELECT status FROM collie_sessions WHERE id = ?`, session); got != "active" {
			t.Fatalf("resumed status = %q", got)
		}
		if n := queryInt(t, db, `SELECT COUNT(*) FROM collie_bindings
			WHERE session_id = ? AND surface = 'discord' AND channel_ref = 'ops' AND active = 1`, session); n != 1 {
			t.Fatalf("resume bound %d active discord:ops rows", n)
		}
		// Pre-end binding history is not resurrected.
		if n := queryInt(t, db, `SELECT COUNT(*) FROM collie_bindings
			WHERE session_id = ? AND surface = 'dashboard' AND active = 1`, session); n != 0 {
			t.Fatalf("resume reactivated %d historical bindings", n)
		}
		if n := queryInt(t, db, `SELECT COUNT(*) FROM activity
			WHERE kind = 'collie.resumed' AND subject = ?`, session); n != 1 {
			t.Fatalf("collie.resumed activity rows = %d", n)
		}

		// Crash-after-commit retry is idempotent; any other active-session
		// resume is bind's job.
		retry := runMC(t, spineEnv(spine), "", "collie", "resume", session, "--from", "discord:ops")
		if retry.code != 0 || retry.json["resumed"] != false {
			t.Fatalf("resume retry = code %d json %v stderr %q", retry.code, retry.json, retry.stderr)
		}
		elsewhere := runMC(t, spineEnv(spine), "", "collie", "resume", session, "--from", "cli:new")
		if elsewhere.code != 1 || !strings.Contains(elsewhere.stderr, "use bind") {
			t.Fatalf("active resume elsewhere = code %d stderr %q", elsewhere.code, elsewhere.stderr)
		}
	})

	t.Run("reaped_sessions_are_resumable", func(t *testing.T) {
		spine := initSpine(t)
		session := begin(t, spine, "dashboard:reap")
		register(t, spine, session)
		db := openDB(t, spine)
		if _, err := db.Exec(`UPDATE collie_sessions SET status = 'reaped' WHERE id = ?`, session); err != nil {
			t.Fatal(err)
		}
		res := runMC(t, spineEnv(spine), "", "collie", "resume", session, "--from", "dashboard:reap")
		if res.code != 0 || res.json["resumed"] != true {
			t.Fatalf("reaped resume = code %d json %v stderr %q", res.code, res.json, res.stderr)
		}
	})

	t.Run("native_continue_requires_the_registered_locator_pair", func(t *testing.T) {
		spine := initSpine(t)
		session := begin(t, spine, "cli:bare")
		end(t, spine, session)
		db := openDB(t, spine)
		res := runMC(t, spineEnv(spine), "", "collie", "resume", session, "--from", "cli:bare")
		if res.code != 1 || !strings.Contains(res.stderr, "locator") {
			t.Fatalf("locator-less resume = code %d stderr %q", res.code, res.stderr)
		}
		if got := queryStr(t, db, `SELECT status FROM collie_sessions WHERE id = ?`, session); got != "ended" {
			t.Fatalf("refused resume moved status to %q", got)
		}
	})

	t.Run("occupied_place_aborts_the_whole_transition", func(t *testing.T) {
		spine := initSpine(t)
		first := begin(t, spine, "dashboard:one")
		register(t, spine, first)
		end(t, spine, first)
		second := begin(t, spine, "cli:two")
		_ = second
		db := openDB(t, spine)

		res := runMC(t, spineEnv(spine), "", "collie", "resume", first, "--from", "cli:two")
		if res.code != 1 || !strings.Contains(res.stderr, "already bound") {
			t.Fatalf("occupied resume = code %d stderr %q", res.code, res.stderr)
		}
		if got := queryStr(t, db, `SELECT status FROM collie_sessions WHERE id = ?`, first); got != "ended" {
			t.Fatalf("aborted resume left status %q", got)
		}
		if n := queryInt(t, db, `SELECT COUNT(*) FROM activity
			WHERE kind = 'collie.resumed' AND subject = ?`, first); n != 0 {
			t.Fatalf("aborted resume left %d activity rows", n)
		}
	})

	t.Run("resume_is_host_scope_only", func(t *testing.T) {
		spine := initSpine(t)
		session := begin(t, spine, "dashboard:scope")
		register(t, spine, session)
		end(t, spine, session)
		db := openDB(t, spine)
		for name, env := range map[string][]string{
			"pipeline":     runJSONEnv(t, spine, "r-x", "pipeline", "worker"),
			"collie-agent": collieJSONEnv(t, spine, session, defaultCollieAllowlist),
		} {
			res := runMC(t, env, "", "collie", "resume", session, "--from", "dashboard:scope")
			if res.code != 1 {
				t.Fatalf("%s resume exit = %d stderr %q", name, res.code, res.stderr)
			}
		}
		if got := queryStr(t, db, `SELECT status FROM collie_sessions WHERE id = ?`, session); got != "ended" {
			t.Fatalf("denied resume moved status to %q", got)
		}
		unknown := runMC(t, spineEnv(spine), "", "collie", "resume", "h-missing", "--from", "cli:x")
		if unknown.code != 1 || !strings.Contains(unknown.stderr, "unknown Collie session") {
			t.Fatalf("unknown resume = code %d stderr %q", unknown.code, unknown.stderr)
		}
	})
}

// The runner transport: fenced idempotent claims of pending inbound turns
// and the atomic reply append + completion + collie_reply fan-out. Both are
// the runner's private own-session scope — never the model's frozen
// operator allowlist, never host, never pipeline.
func TestCollieClaimReply(t *testing.T) {
	start := func(t *testing.T, spine, from string) string {
		t.Helper()
		res := runMC(t, spineEnv(spine), "", "collie", "start", "--from", from)
		if res.code != 0 {
			t.Fatalf("collie start failed: %s", res.stderr)
		}
		return res.json["session_id"].(string)
	}
	send := func(t *testing.T, spine, session, from, body string) int64 {
		t.Helper()
		res := runMC(t, spineEnv(spine), "", "collie", "send", session, "--from", from, "--body", body)
		if res.code != 0 {
			t.Fatalf("collie send failed: %s", res.stderr)
		}
		return int64(res.json["message_id"].(float64))
	}
	// The runner identity is run.json presence alone — deliberately no
	// verb_allowlist: transport must never depend on the model's frozen
	// operator surface.
	runner := func(t *testing.T, spine, session string) []string {
		t.Helper()
		return runJSONEnv(t, spine, session, "collie", "collie")
	}

	t.Run("claim_stamps_and_returns_the_next_pending_turn_idempotently", func(t *testing.T) {
		spine := initSpine(t)
		session := start(t, spine, "dashboard:dash")
		m1 := send(t, spine, session, "cli:term", "first question")
		m2 := send(t, spine, session, "cli:term", "second question")
		db := openDB(t, spine)
		if _, err := db.Exec(`UPDATE collie_sessions SET last_activity_at = datetime('now', '-1 day') WHERE id = ?`, session); err != nil {
			t.Fatal(err)
		}
		beforeActivity := queryStr(t, db, `SELECT last_activity_at FROM collie_sessions WHERE id = ?`, session)

		claim := runMC(t, runner(t, spine, session), "", "collie", "claim", session)
		if claim.code != 0 {
			t.Fatalf("claim failed (%d): %s", claim.code, claim.stderr)
		}
		msg, ok := claim.json["message"].(map[string]any)
		if !ok {
			t.Fatalf("claim result = %v", claim.json)
		}
		if msg["id"] != strconv.FormatInt(m1, 10) || msg["seq"] != "1" || msg["body"] != "first question" ||
			msg["surface"] != "cli" || msg["channel_ref"] != "term" || msg["reclaimed"] != false {
			t.Fatalf("claimed message = %v", msg)
		}
		claimState := func(id int64) string {
			return queryStr(t, db, `SELECT COALESCE(claimed_by, '<null>') || '|' ||
				COALESCE(claimed_at, '<null>') FROM conversation_messages WHERE id = ?`, id)
		}
		firstClaim := claimState(m1)
		if !strings.HasPrefix(firstClaim, session+"|") || strings.HasSuffix(firstClaim, "|<null>") {
			t.Fatalf("claim state = %q", firstClaim)
		}
		if got := claimState(m2); got != "<null>|<null>" {
			t.Fatalf("claim touched the queued turn: %q", got)
		}

		// A fresh runner resumes the durable claim: same turn, same stamp.
		reclaim := runMC(t, runner(t, spine, session), "", "collie", "claim", session)
		if reclaim.code != 0 {
			t.Fatalf("reclaim failed: %s", reclaim.stderr)
		}
		remsg := reclaim.json["message"].(map[string]any)
		if remsg["id"] != strconv.FormatInt(m1, 10) || remsg["reclaimed"] != true {
			t.Fatalf("reclaimed message = %v", remsg)
		}
		if got := claimState(m1); got != firstClaim {
			t.Fatalf("reclaim rewrote claim state: before %q after %q", firstClaim, got)
		}
		// Claiming is bookkeeping, not conversation traffic.
		if got := queryStr(t, db, `SELECT last_activity_at FROM collie_sessions WHERE id = ?`, session); got != beforeActivity {
			t.Fatalf("claim advanced last_activity_at: before %q after %q", beforeActivity, got)
		}

		empty := start(t, spine, "cli:empty")
		nothing := runMC(t, runner(t, spine, empty), "", "collie", "claim", empty)
		if nothing.code != 0 || nothing.json["message"] != nil {
			t.Fatalf("empty claim = code %d json %v stderr %q", nothing.code, nothing.json, nothing.stderr)
		}
	})

	t.Run("claim_returns_only_complete_live_typed_attachment_identities", func(t *testing.T) {
		spine := initSpine(t)
		session := start(t, spine, "dashboard:typed")
		messageID := send(t, spine, session, "dashboard:typed", "inspect this")
		db := openDB(t, spine)
		digest := strings.Repeat("a", 64)
		if _, err := db.Exec(`INSERT INTO attachment_objects
			(session_id,direction,sha256,size,state,device,inode,file_type)
			VALUES (?,'in',?,7,'live','11','22','regular')`,
			session, digest); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO conversation_attachment_refs
			(message_id,ordinal,session_id,direction,sha256,size,media_type,display_name)
			VALUES (?,1,?,'in',?,7,'image/png','map.png')`,
			messageID, session, digest); err != nil {
			t.Fatal(err)
		}
		claim := runMC(t, runner(t, spine, session), "", "collie", "claim", session)
		if claim.code != 0 {
			t.Fatalf("typed claim failed (%d): %s", claim.code, claim.stderr)
		}
		message := claim.json["message"].(map[string]any)
		attachments := message["attachments"].([]any)
		if len(attachments) != 1 {
			t.Fatalf("typed attachments = %#v", attachments)
		}
		got := attachments[0].(map[string]any)
		if got["ordinal"] != float64(1) || got["session_id"] != session ||
			got["direction"] != "in" || got["sha256"] != digest ||
			got["size"] != float64(7) || got["media_type"] != "image/png" ||
			got["display_name"] != "map.png" {
			t.Fatalf("typed attachment = %#v", got)
		}
		if _, exists := got["path"]; exists {
			t.Fatalf("spine claim supplied a caller path: %#v", got)
		}
	})

	t.Run("reply_appends_completes_and_fans_out_to_every_binding", func(t *testing.T) {
		spine := initSpine(t)
		session := start(t, spine, "dashboard:dash")
		if res := runMC(t, spineEnv(spine), "", "collie", "bind", session, "--from", "discord:disc"); res.code != 0 {
			t.Fatalf("bind failed: %s", res.stderr)
		}
		m1 := send(t, spine, session, "cli:term", "please do X")
		// The unserved inbound turn wakes Collie: the
		// Collie effect leads the tick, and the same tick still runs ws-test's
		// pipeline round — the empty board spawns Strategist — because
		// Collie and the pipelines are independent stages. Committing the wake
		// binds the launch, after which the runner claims from inside it.
		tick := dispatch(t, spine)
		actions := tickActions(t, tick)
		if len(actions) != 2 || actions[0]["action"] != "collie-wake" ||
			actions[1]["action"] != "spawn" || actions[1]["role"] != "strategist(propose)" ||
			actions[1]["project"] != "ws-test" {
			t.Fatalf("tick = %v, want collie-wake then ws-test's strategist(propose) spawn", tick)
		}
		if res := runMC(t, runner(t, spine, session), "", "collie", "claim", session); res.code != 0 {
			t.Fatalf("claim failed: %s", res.stderr)
		}

		// Conversation transport is lease-free and coexists with the live
		// leased pipeline run the same tick spawned (a subjectless
		// Strategist lease).
		db := openDB(t, spine)
		lockBefore := queryStr(t, db, `SELECT run_id || '|' || owner || '|' || COALESCE(subject, '-') FROM leases WHERE project = 'ws-test'`)
		if lockBefore == "<NULL>" {
			t.Fatalf("pipeline lease is not held before the reply")
		}
		if _, err := db.Exec(`UPDATE collie_sessions SET last_activity_at = datetime('now', '-1 day') WHERE id = ?`, session); err != nil {
			t.Fatal(err)
		}
		beforeActivity := queryStr(t, db, `SELECT last_activity_at FROM collie_sessions WHERE id = ?`, session)

		reply := runMC(t, runner(t, spine, session), "", "collie", "reply", session,
			"--to", strconv.FormatInt(m1, 10), "--body", "done: X")
		if reply.code != 0 {
			t.Fatalf("reply failed (%d): %s", reply.code, reply.stderr)
		}
		if reply.json["replied"] != true || reply.json["seq"] != float64(2) ||
			reply.json["reply_to"] != float64(m1) || reply.json["deliveries"] != float64(3) {
			t.Fatalf("reply result = %v", reply.json)
		}
		replyID := int64(reply.json["message_id"].(float64))

		if got := queryStr(t, db, `SELECT direction || '|' || surface || '|' ||
			COALESCE(channel_ref, '<null>') || '|' || body || '|' || COALESCE(attachments, '<null>') || '|' || reply_to
			FROM conversation_messages WHERE id = ?`, replyID); got != `reply|collie|<null>|done: X|<null>|`+strconv.FormatInt(m1, 10) {
			t.Fatalf("reply row = %q", got)
		}
		if got := queryStr(t, db, `SELECT COALESCE(completed_at, '<null>') FROM conversation_messages WHERE id = ?`, m1); got == "<null>" {
			t.Fatalf("reply did not complete the claimed turn")
		}
		if got := queryStr(t, db, `SELECT group_concat(surface || ':' || channel_ref, ',') FROM
			(SELECT surface, channel_ref FROM outbox WHERE kind = 'collie_reply' ORDER BY surface, channel_ref)`); got != "cli:term,dashboard:dash,discord:disc" {
			t.Fatalf("reply destinations = %q", got)
		}
		// The `cli` row is born DELIVERED and the other two are not.
		//
		// The cli binding has no delivery loop: it reads the conversation
		// back with `mc collie history --follow`. Poll/ack belongs to each
		// NATIVE surface's loop — the resident and the dashboard. Nothing
		// drains cli, so a pending cli row is pending forever, and the outbox
		// delivery lag climbs monotonically on a healthy board. For the CLI
		// the conversation row IS the delivery; there is no second hop to
		// describe.
		if got := queryStr(t, db, `SELECT group_concat(surface || ':' ||
			CASE WHEN delivered_at IS NULL THEN 'pending' ELSE 'delivered' END, ',') FROM
			(SELECT surface, delivered_at FROM outbox WHERE kind = 'collie_reply' ORDER BY surface)`); got !=
			"cli:delivered,dashboard:pending,discord:pending" {
			t.Fatalf("reply delivery stamps = %q", got)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(queryStr(t, db,
			`SELECT payload FROM outbox WHERE kind = 'collie_reply' ORDER BY id LIMIT 1`)), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["message_id"] != float64(replyID) || payload["seq"] != float64(2) ||
			payload["body"] != "done: X" || payload["reply_to"] != float64(m1) ||
			len(payload["attachments"].([]any)) != 0 {
			t.Fatalf("reply payload = %v", payload)
		}
		if got := queryStr(t, db, `SELECT last_activity_at FROM collie_sessions WHERE id = ?`, session); got == beforeActivity {
			t.Fatalf("reply did not advance last_activity_at from %q", beforeActivity)
		}
		if got := queryStr(t, db, `SELECT run_id || '|' || owner || '|' || COALESCE(subject, '-') FROM leases WHERE project = 'ws-test'`); got != lockBefore {
			t.Fatalf("reply disturbed pipeline lease: before %q after %q", lockBefore, got)
		}

		// The completed turn leaves the pending queue; new traffic re-enters it.
		drained := runMC(t, runner(t, spine, session), "", "collie", "claim", session)
		if drained.code != 0 || drained.json["message"] != nil {
			t.Fatalf("post-reply claim = code %d json %v", drained.code, drained.json)
		}
		m3 := send(t, spine, session, "cli:term", "follow-up")
		next := runMC(t, runner(t, spine, session), "", "collie", "claim", session)
		if next.code != 0 {
			t.Fatalf("follow-up claim failed: %s", next.stderr)
		}
		if msg := next.json["message"].(map[string]any); msg["id"] != strconv.FormatInt(m3, 10) || msg["seq"] != "3" {
			t.Fatalf("follow-up claim = %v", next.json)
		}
	})

	t.Run("reply_replay_is_idempotent_and_a_second_logical_reply_is_refused", func(t *testing.T) {
		spine := initSpine(t)
		session := start(t, spine, "dashboard:dash")
		m1 := send(t, spine, session, "dashboard:dash", "one")
		if res := runMC(t, runner(t, spine, session), "", "collie", "claim", session); res.code != 0 {
			t.Fatalf("claim failed: %s", res.stderr)
		}
		first := runMC(t, runner(t, spine, session), "", "collie", "reply", session,
			"--to", strconv.FormatInt(m1, 10), "--body", "answer")
		if first.code != 0 {
			t.Fatalf("reply failed: %s", first.stderr)
		}
		db := openDB(t, spine)
		beforeMessages := queryInt(t, db, `SELECT COUNT(*) FROM conversation_messages`)
		beforeOutbox := queryInt(t, db, `SELECT COUNT(*) FROM outbox`)

		// Crash-after-commit retry: same logical reply, nothing re-appended.
		replay := runMC(t, runner(t, spine, session), "", "collie", "reply", session,
			"--to", strconv.FormatInt(m1, 10), "--body", "answer")
		if replay.code != 0 || replay.json["replied"] != false ||
			replay.json["message_id"] != first.json["message_id"] {
			t.Fatalf("reply replay = code %d json %v stderr %q", replay.code, replay.json, replay.stderr)
		}
		if queryInt(t, db, `SELECT COUNT(*) FROM conversation_messages`) != beforeMessages ||
			queryInt(t, db, `SELECT COUNT(*) FROM outbox`) != beforeOutbox {
			t.Fatal("idempotent replay appended rows")
		}
		// A different body against a completed turn is a second logical reply.
		second := runMC(t, runner(t, spine, session), "", "collie", "reply", session,
			"--to", strconv.FormatInt(m1, 10), "--body", "different answer")
		if second.code != 1 || !strings.Contains(second.stderr, "one logical reply") {
			t.Fatalf("second reply = code %d stderr %q", second.code, second.stderr)
		}

		// Reply protocol violations are inert.
		m2 := send(t, spine, session, "dashboard:dash", "two")
		other := start(t, spine, "cli:other")
		m3 := send(t, spine, other, "cli:other", "other conversation")
		replyID := int64(first.json["message_id"].(float64))
		for name, tc := range map[string]struct {
			to  int64
			msg string
		}{
			"unclaimed_turn":       {to: m2, msg: "unclaimed"},
			"reply_to_a_reply":     {to: replyID, msg: "inbound turn"},
			"unknown_message":      {to: 9999, msg: "unknown conversation message"},
			"another_conversation": {to: m3, msg: "another conversation"},
		} {
			t.Run(name, func(t *testing.T) {
				res := runMC(t, runner(t, spine, session), "", "collie", "reply", session,
					"--to", strconv.FormatInt(tc.to, 10), "--body", "x")
				if res.code != 1 || !strings.Contains(res.stderr, tc.msg) {
					t.Fatalf("exit = %d stderr %q, want failure containing %q", res.code, res.stderr, tc.msg)
				}
			})
		}
		if queryInt(t, db, `SELECT COUNT(*) FROM conversation_messages WHERE direction = 'reply'`) != 1 {
			t.Fatal("refused replies appended rows")
		}
	})

	t.Run("reply_outbox_failure_rolls_back_the_whole_turn", func(t *testing.T) {
		spine := initSpine(t)
		session := start(t, spine, "dashboard:dash")
		m1 := send(t, spine, session, "dashboard:dash", "roll me back")
		if res := runMC(t, runner(t, spine, session), "", "collie", "claim", session); res.code != 0 {
			t.Fatalf("claim failed: %s", res.stderr)
		}
		db := openDB(t, spine)
		beforeActivity := queryStr(t, db, `SELECT last_activity_at FROM collie_sessions WHERE id = ?`, session)
		if _, err := db.Exec(`CREATE TRIGGER test_fail_collie_reply
			BEFORE INSERT ON outbox WHEN NEW.kind = 'collie_reply'
			BEGIN SELECT RAISE(ABORT, 'injected reply failure'); END`); err != nil {
			t.Fatal(err)
		}
		res := runMC(t, runner(t, spine, session), "", "collie", "reply", session,
			"--to", strconv.FormatInt(m1, 10), "--body", "must roll back")
		if res.code != 1 || !strings.Contains(res.stderr, "injected reply failure") {
			t.Fatalf("reply atomic failure = code %d stderr %q", res.code, res.stderr)
		}
		if queryInt(t, db, `SELECT COUNT(*) FROM conversation_messages WHERE direction = 'reply'`) != 0 ||
			queryInt(t, db, `SELECT COUNT(*) FROM outbox`) != 0 {
			t.Fatal("failed reply left a reply or outbox row")
		}
		if got := queryStr(t, db, `SELECT COALESCE(completed_at, '<null>') FROM conversation_messages WHERE id = ?`, m1); got != "<null>" {
			t.Fatalf("failed reply completed the turn: %q", got)
		}
		if got := queryStr(t, db, `SELECT last_activity_at FROM collie_sessions WHERE id = ?`, session); got != beforeActivity {
			t.Fatalf("failed reply changed last_activity_at: before %q after %q", beforeActivity, got)
		}
	})

	t.Run("claim_and_reply_are_own_session_runner_scope", func(t *testing.T) {
		spine := initSpine(t)
		session := start(t, spine, "dashboard:dash")
		m1 := send(t, spine, session, "dashboard:dash", "fence me")
		db := openDB(t, spine)
		to := strconv.FormatInt(m1, 10)

		for name, env := range map[string][]string{
			"host":          spineEnv(spine),
			"pipeline":      runJSONEnv(t, spine, "r-x", "pipeline", "worker"),
			"cross-session": runner(t, spine, "h-other"),
		} {
			for _, args := range [][]string{
				{"collie", "claim", session},
				{"collie", "reply", session, "--to", to, "--body", "forged"},
			} {
				res := runMC(t, env, "", args...)
				if res.code != 1 {
					t.Fatalf("%s %v exit = %d stderr %q", name, args, res.code, res.stderr)
				}
			}
		}
		if got := queryStr(t, db, `SELECT COALESCE(claimed_by, '<null>') || '|' ||
			COALESCE(completed_at, '<null>') FROM conversation_messages WHERE id = ?`, m1); got != "<null>|<null>" {
			t.Fatalf("forged transport touched the turn: %q", got)
		}
		if queryInt(t, db, `SELECT COUNT(*) FROM conversation_messages`) != 1 ||
			queryInt(t, db, `SELECT COUNT(*) FROM outbox`) != 0 {
			t.Fatal("forged transport appended rows")
		}

		// An ended session's runner has no transport left.
		if res := runMC(t, runner(t, spine, session), "", "collie", "claim", session); res.code != 0 {
			t.Fatalf("claim failed: %s", res.stderr)
		}
		if res := runMC(t, spineEnv(spine), "", "collie", "end", session, "--reason", "test"); res.code != 0 {
			t.Fatalf("end failed: %s", res.stderr)
		}
		endedClaim := runMC(t, runner(t, spine, session), "", "collie", "claim", session)
		if endedClaim.code != 1 || !strings.Contains(endedClaim.stderr, "active") {
			t.Fatalf("ended claim = code %d stderr %q", endedClaim.code, endedClaim.stderr)
		}
		endedReply := runMC(t, runner(t, spine, session), "", "collie", "reply", session,
			"--to", to, "--body", "ghost turn")
		if endedReply.code != 1 || !strings.Contains(endedReply.stderr, "active") {
			t.Fatalf("ended reply = code %d stderr %q", endedReply.code, endedReply.stderr)
		}
		if got := queryStr(t, db, `SELECT COALESCE(completed_at, '<null>') FROM conversation_messages WHERE id = ?`, m1); got != "<null>" {
			t.Fatalf("ended-session transport completed the turn: %q", got)
		}
	})
}

// outboxSnapshot captures every outbox column in deterministic id order, so
// "read-only" means the whole table byte-identical, not a two-column proxy.
func outboxSnapshot(t *testing.T, db *sql.DB) string {
	t.Helper()
	return queryStr(t, db, `SELECT COALESCE(group_concat(row, ';'), '<empty>') FROM (
		SELECT id || '|' || kind || '|' || COALESCE(session_id, '<null>') || '|' ||
		       surface || '|' || COALESCE(channel_ref, '<null>') || '|' ||
		       payload || '|' || created_at || '|' ||
		       COALESCE(delivered_at, '<null>') AS row
		FROM outbox ORDER BY id)`)
}

func TestOutboxPollAck(t *testing.T) {
	fixture := func(t *testing.T) (string, *sql.DB) {
		t.Helper()
		spine := initSpine(t)
		started := runMC(t, spineEnv(spine), "", "collie", "start", "--from", "dashboard:dash")
		if started.code != 0 {
			t.Fatalf("collie start failed: %s", started.stderr)
		}
		session := started.json["session_id"].(string)
		if got := runMC(t, spineEnv(spine), "", "collie", "bind", session, "--from", "discord:disc"); got.code != 0 {
			t.Fatalf("collie bind failed: %s", got.stderr)
		}
		if got := runMC(t, spineEnv(spine), "", "collie", "send", session,
			"--from", "cli:origin", "--body", "deliver me"); got.code != 0 {
			t.Fatalf("collie send failed: %s", got.stderr)
		}
		return spine, openDB(t, spine)
	}

	t.Run("poll_is_surface_scoped_stable_and_read_only", func(t *testing.T) {
		spine, db := fixture(t)
		before := outboxSnapshot(t, db)

		dashboard := runMC(t, spineEnv(spine), "", "outbox", "poll", "--surface", "dashboard")
		if dashboard.code != 0 {
			t.Fatalf("dashboard poll failed: %s", dashboard.stderr)
		}
		rows := dashboard.json["rows"].([]any)
		if len(rows) != 1 {
			t.Fatalf("dashboard poll = %v", dashboard.json)
		}
		row := rows[0].(map[string]any)
		if row["surface"] != "dashboard" || row["channel_ref"] != "dash" || row["kind"] != "collie_echo" {
			t.Fatalf("dashboard outbox row = %v", row)
		}
		payload := row["payload"].(map[string]any)
		if payload["body"] != "deliver me" || payload["seq"] != float64(1) {
			t.Fatalf("structured outbox payload = %v", payload)
		}
		discord := runMC(t, spineEnv(spine), "", "outbox", "poll", "--surface", "discord")
		if discord.code != 0 || len(discord.json["rows"].([]any)) != 1 {
			t.Fatalf("discord poll = code %d json %v stderr %q", discord.code, discord.json, discord.stderr)
		}
		if got := outboxSnapshot(t, db); got != before {
			t.Fatalf("poll mutated the outbox: before %q after %q", before, got)
		}

		// Stable id order and limit are part of the delivery cursor contract.
		if _, err := db.Exec(`INSERT INTO outbox (kind, surface, channel_ref, payload)
			VALUES ('health', 'dashboard', 'dash', '{"n":2}'),
			       ('health', 'dashboard', 'dash', '{"n":3}')`); err != nil {
			t.Fatal(err)
		}
		limited := runMC(t, spineEnv(spine), "", "outbox", "poll", "--surface", "dashboard", "--limit", "2")
		if limited.code != 0 {
			t.Fatalf("limited poll failed: %s", limited.stderr)
		}
		limitedRows := limited.json["rows"].([]any)
		if len(limitedRows) != 2 || limitedRows[0].(map[string]any)["id"].(float64) >= limitedRows[1].(map[string]any)["id"].(float64) {
			t.Fatalf("limited poll is not ascending: %v", limitedRows)
		}
	})

	t.Run("ack_only_owns_its_surface_and_replay_is_idempotent", func(t *testing.T) {
		spine, db := fixture(t)
		dashboardID := queryInt(t, db, `SELECT id FROM outbox WHERE surface = 'dashboard'`)
		discordID := queryInt(t, db, `SELECT id FROM outbox WHERE surface = 'discord'`)

		wrong := runMC(t, spineEnv(spine), "", "outbox", "ack", fmt.Sprint(dashboardID), "--surface", "discord")
		if wrong.code != 1 || !strings.Contains(wrong.stderr, "belongs to surface") {
			t.Fatalf("wrong-surface ack = code %d stderr %q", wrong.code, wrong.stderr)
		}
		if got := queryStr(t, db, `SELECT delivered_at FROM outbox WHERE id = ?`, dashboardID); got != "<NULL>" {
			t.Fatalf("wrong-surface ack delivered row: %q", got)
		}

		acked := runMC(t, spineEnv(spine), "", "outbox", "ack", fmt.Sprint(dashboardID), "--surface", "dashboard")
		if acked.code != 0 || acked.json["acked"] != true {
			t.Fatalf("ack = code %d json %v stderr %q", acked.code, acked.json, acked.stderr)
		}
		deliveredAt := acked.json["delivered_at"].(string)
		if deliveredAt == "" || queryStr(t, db, `SELECT delivered_at FROM outbox WHERE id = ?`, dashboardID) != deliveredAt {
			t.Fatalf("ack timestamp mismatch: %v", acked.json)
		}
		if got := runMC(t, spineEnv(spine), "", "outbox", "poll", "--surface", "dashboard"); got.code != 0 || len(got.json["rows"].([]any)) != 0 {
			t.Fatalf("delivered row still polled: code %d json %v", got.code, got.json)
		}
		if got := queryStr(t, db, `SELECT delivered_at FROM outbox WHERE id = ?`, discordID); got != "<NULL>" {
			t.Fatalf("dashboard ack touched discord row: %q", got)
		}

		replay := runMC(t, spineEnv(spine), "", "outbox", "ack", fmt.Sprint(dashboardID), "--surface", "dashboard")
		if replay.code != 0 || replay.json["acked"] != false || replay.json["delivered_at"] != deliveredAt {
			t.Fatalf("ack replay = code %d json %v stderr %q", replay.code, replay.json, replay.stderr)
		}
		missing := runMC(t, spineEnv(spine), "", "outbox", "ack", "99999", "--surface", "dashboard")
		if missing.code != 1 || !strings.Contains(missing.stderr, "unknown outbox row") {
			t.Fatalf("missing ack = code %d stderr %q", missing.code, missing.stderr)
		}
	})

	t.Run("scope_and_usage_fail_before_delivery_mutation", func(t *testing.T) {
		spine, db := fixture(t)
		before := outboxSnapshot(t, db)
		pipeline := runJSONEnv(t, spine, "pipeline", "pipeline", "worker")
		collie := collieJSONEnv(t, spine, "h-forged", defaultCollieAllowlist)
		for name, env := range map[string][]string{"pipeline": pipeline, "collie": collie} {
			t.Run(name, func(t *testing.T) {
				for _, args := range [][]string{
					{"outbox", "poll", "--surface", "dashboard"},
					{"outbox", "ack", "1", "--surface", "dashboard"},
				} {
					got := runMC(t, env, "", args...)
					if got.code != 1 {
						t.Fatalf("%v exit = %d stderr %q", args, got.code, got.stderr)
					}
				}
			})
		}
		for _, tc := range []struct {
			args []string
			msg  string
		}{
			{args: []string{"outbox", "poll"}, msg: "requires --surface"},
			{args: []string{"outbox", "poll", "--surface", "email"}, msg: "discord|dashboard|cli"},
			{args: []string{"outbox", "poll", "--surface", "dashboard", "--limit", "0"}, msg: "limit"},
			{args: []string{"outbox", "ack", "1"}, msg: "requires --surface"},
		} {
			got := runMC(t, spineEnv(spine), "", tc.args...)
			if got.code != 2 || !strings.Contains(got.stderr, tc.msg) {
				t.Fatalf("%v exit = %d stderr %q", tc.args, got.code, got.stderr)
			}
			errObj, ok := got.json["error"].(map[string]any)
			if !ok || errObj["code"] != "usage" {
				t.Fatalf("%v error JSON = %v", tc.args, got.json)
			}
		}
		if got := outboxSnapshot(t, db); got != before {
			t.Fatalf("scope/usage failure mutated the outbox: before %q after %q", before, got)
		}
	})
}

// workerFixture returns a seeded task with its Worker run claimed, and the
// environment of the Worker container.
func workerFixture(t *testing.T, title string) (string, int64, string, []string) {
	t.Helper()
	spine := initSpine(t)
	taskID := proposalTask(t, spine, title)
	arbiterPromoteAll(t, spine, dispatchThrough(t, spine, "spawn"))
	eff, root := spawnRole(t, spine, "worker")
	run := eff["run_id"].(string)
	env, _ := roleEnv(t, spine, run, "worker", root)
	if started := runMC(t, env, "", "run", "process-started", run); started.code != 0 {
		t.Fatalf("Worker process start failed: %+v", started)
	}
	return spine, taskID, run, env
}

// verifierFixture returns a task at `worked` with its Verifier run claimed.
// The env carries a real artifact plane. `evidence` and `correction` name real
// files in that plane, because every verdict records paths a later run opens.
// `commit` is the submitted commit.
func verifierFixture(t *testing.T, title string) (spine string, taskID int64, run string, env []string, evidence, correction, commit string) {
	t.Helper()
	spine = initSpine(t)
	taskID = proposalTask(t, spine, title)
	arbiterPromoteAll(t, spine, dispatchThrough(t, spine, "spawn"))
	commit = workedTask(t, spine, taskID)
	eff, root := spawnRole(t, spine, "verifier")
	run = eff["run_id"].(string)
	var plane string
	env, plane = roleEnv(t, spine, run, "verifier", root)
	evidence = writePlaneFile(t, plane, fmt.Sprintf("%d/verification/gate-ladder.md", taskID),
		"Criterion 1, build: PASS. The build exits 0.\n"+
			"Criterion 2, trailing newline: FAIL. The file has 24 bytes, 25 required.\n")
	correction = writePlaneFile(t, plane, fmt.Sprintf("%d/corrections/round-1.md", taskID),
		"Criterion 2 fails: the deliverable has no trailing newline.\n")
	return spine, taskID, run, env, evidence, correction, commit
}

func TestVerifierVerdictValidation(t *testing.T) {
	t.Run("correct_requires_correction_file", func(t *testing.T) {
		_, taskID, run, env, evidence, _, _ := verifierFixture(t, "correct needs feedback")
		res := runMC(t, env, "", "verifier", "verdict", fmt.Sprint(taskID), "--run", run,
			"--outcome", "correct", "--problem", "The result misses a criterion.", "--explanation-evidence", "The check failed.", "--resolution", "Correct the result.", "--evidence", evidence)
		if res.code != 1 || !strings.Contains(res.stderr, "--correction") {
			t.Fatalf("exit = %d stderr %q", res.code, res.stderr)
		}
	})

	t.Run("correct_reenters_and_records_verdict", func(t *testing.T) {
		spine, taskID, run, env, evidence, correction, _ := verifierFixture(t, "correct once")
		res := runMC(t, env, "", "verifier", "verdict", fmt.Sprint(taskID), "--run", run,
			"--outcome", "correct", "--problem", "The result misses a criterion.", "--explanation-evidence", "The check failed.", "--resolution", "Correct the result.", "--evidence", evidence,
			"--correction", correction)
		if res.code != 0 {
			t.Fatalf("correct failed: %s", res.stderr)
		}
		db := openDB(t, spine)
		if got := queryStr(t, db, `SELECT t.status || '/' || t.correction_open FROM tasks t WHERE t.id = ?`, taskID); got != "seeded/1" {
			t.Fatalf("correct result = %q", got)
		}
		if got := queryStr(t, db, `SELECT verdict_outcome FROM runs WHERE id = ?`, run); got != "correct" {
			t.Fatalf("verdict record = %q", got)
		}
		requireArtifactSnapshot(t, correction, queryStr(t, db, `SELECT correction_path FROM runs WHERE id=?`, run))
	})

	// Every verdict arm records an evidence path. The Packager brief hands that
	// path on, so the file must exist in a durable plane. A pass leads the
	// table: a pass with unreadable evidence ships to the operator as a packet
	// that cites nothing.
	t.Run("evidence_must_outlive_the_verifier_on_every_arm", func(t *testing.T) {
		for _, arm := range []struct {
			name string
			args []string
		}{
			{"pass", []string{"--outcome", "pass"}},
			{"correct", []string{"--outcome", "correct", "--problem", "The result misses a criterion.", "--explanation-evidence", "The check failed.", "--resolution", "Correct the result."}},
		} {
			t.Run(arm.name, func(t *testing.T) {
				spine, taskID, run, env, evidence, correction, commit := verifierFixture(t, "evidence "+arm.name)
				if arm.name == "pass" {
					arm.args = append(arm.args, "--sha", commit)
				}
				for _, tc := range []struct {
					name string
					path string
					want string
				}{
					{"relative_string", "e.md", "durable artifact root"},
					{"container_local_scratch", "/tmp/verifier-evidence.txt", "durable artifact root"},
					{"the_class_root_itself", filepath.Join(artifactRootFromEnv(t, env), "evidence.md"), "not inside one of this run's mounted artifact planes"},
					{"a_fabricated_plane_target", filepath.Join(artifactRootFromEnv(t, env), "invented", "evidence.md"), "not inside one of this run's mounted artifact planes"},
					{"absent_inside_the_plane", filepath.Join(filepath.Dir(evidence), "never-written.md"), "does not exist"},
				} {
					t.Run(tc.name, func(t *testing.T) {
						args := append([]string{"verifier", "verdict", fmt.Sprint(taskID), "--run", run,
							"--evidence", tc.path}, arm.args...)
						if arm.name == "correct" {
							args = append(args, "--correction", correction)
						}
						res := runMC(t, env, "", args...)
						if res.code == 0 || !strings.Contains(res.stderr, tc.want) {
							t.Fatalf("exit = %d stderr %q, want a refusal naming %q", res.code, res.stderr, tc.want)
						}
						// Fail-closed: the verdict is not recorded and the
						// subject has not moved.
						db := openDB(t, spine)
						if got := queryStr(t, db, `SELECT status FROM tasks WHERE id = ?`, taskID); got != "worked" {
							t.Fatalf("a refused verdict moved the task to %q", got)
						}
						if got := queryStr(t, db, `SELECT COALESCE(verdict_outcome, '<none>') FROM runs WHERE id = ?`, run); got != "<none>" {
							t.Fatalf("a refused verdict recorded outcome %q", got)
						}
					})
				}
				// The same call with the real file is accepted — without this
				// the table above would pass against a verb that refuses
				// everything.
				args := append([]string{"verifier", "verdict", fmt.Sprint(taskID), "--run", run,
					"--evidence", evidence}, arm.args...)
				if arm.name == "correct" {
					args = append(args, "--correction", correction)
				}
				if res := runMC(t, env, "", args...); res.code != 0 {
					t.Fatalf("durable evidence rejected: %s", res.stderr)
				}
			})
		}
	})

	// A correct verdict whose correction path names nothing is not recorded.
	// The next Worker brief carries that path as binding input.
	t.Run("correct_refuses_a_correction_that_dies_with_the_container", func(t *testing.T) {
		spine, taskID, run, env, evidence, correction, _ := verifierFixture(t, "correction must be durable")
		for _, tc := range []struct {
			name string
			path string
			want string
		}{
			{"relative_string", "corrections/c1.md", "durable artifact root"},
			{"container_local_scratch", "/mc/scratch/c1.md", "durable artifact root"},
			{"absent_inside_the_plane", filepath.Join(filepath.Dir(correction), "never-written.md"), "does not exist"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				res := runMC(t, env, "", "verifier", "verdict", fmt.Sprint(taskID), "--run", run,
					"--outcome", "correct", "--problem", "The result misses a criterion.", "--explanation-evidence", "The check failed.", "--resolution", "Correct the result.", "--evidence", evidence, "--correction", tc.path)
				if res.code == 0 || !strings.Contains(res.stderr, tc.want) {
					t.Fatalf("exit = %d stderr %q, want a refusal naming %q", res.code, res.stderr, tc.want)
				}
				// Fail-closed: nothing about the task moved.
				db := openDB(t, spine)
				if got := queryStr(t, db, `SELECT status || '/' || correction_count FROM tasks WHERE id = ?`, taskID); got != "worked/0" {
					t.Fatalf("a refused verdict moved the task to %q", got)
				}
			})
		}
	})

	for _, tc := range []struct {
		name string
		args []string
		msg  string
	}{
		{name: "pass_forbids_correction", args: []string{"--outcome", "pass", "--evidence", "e", "--sha", "s", "--correction", "c"}, msg: "--correction"},
		{name: "correct_forbids_sha", args: []string{"--outcome", "correct", "--problem", "The result misses a criterion.", "--explanation-evidence", "The check failed.", "--resolution", "Correct the result.", "--evidence", "e", "--sha", "s", "--correction", "c"}, msg: "--sha"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spine, taskID, run, env, _, _, _ := verifierFixture(t, tc.name)
			args := []string{"verifier", "verdict", fmt.Sprint(taskID), "--run", run}
			args = append(args, tc.args...)
			res := runMC(t, env, "", args...)
			if res.code != 2 || !strings.Contains(res.stderr, tc.msg) {
				t.Fatalf("exit = %d stderr %q, want usage containing %q", res.code, res.stderr, tc.msg)
			}
			db := openDB(t, spine)
			if got := queryStr(t, db, `SELECT status FROM tasks WHERE id = ?`, taskID); got != "worked" {
				t.Fatalf("invalid carrier moved task to %q", got)
			}
			if got := queryStr(t, db, `SELECT ended_at FROM runs WHERE id = ?`, run); got != "<NULL>" {
				t.Fatalf("invalid carrier ended run at %q", got)
			}
			if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != run {
				t.Fatalf("invalid carrier released lease to %q", got)
			}
		})
	}

	for _, tc := range []struct {
		name string
		args func(taskID int64, run string) []string
		msg  string
	}{
		{"missing_sha_usage", func(id int64, run string) []string {
			return []string{"verifier", "verdict", fmt.Sprint(id), "--run", run, "--outcome", "pass", "--evidence", "e"}
		}, "--sha"},
		{"missing_evidence_usage", func(id int64, run string) []string {
			return []string{"verifier", "verdict", fmt.Sprint(id), "--run", run, "--outcome", "pass", "--sha", "s"}
		}, "--evidence"},
		{"bad_outcome_usage", func(id int64, run string) []string {
			return []string{"verifier", "verdict", fmt.Sprint(id), "--run", run, "--outcome", "maybe", "--evidence", "e", "--sha", "s"}
		}, "--outcome"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, taskID, run, env, _, _, _ := verifierFixture(t, tc.name)
			res := runMC(t, env, "", tc.args(taskID, run)...)
			if res.code != 2 || !strings.Contains(res.stderr, tc.msg) {
				t.Fatalf("exit = %d stderr %q, want usage containing %q", res.code, res.stderr, tc.msg)
			}
		})
	}
}

func TestCompletePhase2Terminals(t *testing.T) {
	t.Run("needs_operator_blocks_without_moving_status", func(t *testing.T) {
		spine, taskID, run, env := workerFixture(t, "operator decision")
		res := runMC(t, env, "", "complete", fmt.Sprint(taskID), "--run", run,
			"--needs-operator", "--reason", "choose a deployment target")
		if res.code != 0 {
			t.Fatalf("needs-operator failed: %s", res.stderr)
		}
		if _, has := res.json["action"]; has {
			t.Fatalf("terminal returned dispatch effect: %v", res.json)
		}
		db := openDB(t, spine)
		if got := queryStr(t, db, `SELECT status || '/' || blocked || '/' || blocked_reason FROM tasks WHERE id = ?`, taskID); got != "seeded/1/choose a deployment target" {
			t.Fatalf("blocked task = %q", got)
		}
		if got := queryStr(t, db, `SELECT outcome FROM runs WHERE id = ?`, run); got != "blocked" {
			t.Fatalf("run outcome = %q", got)
		}
		if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != run {
			t.Fatalf("the terminal released its lease before the stop: %q", got)
		}
		stopped := runMC(t, spineEnv(spine), "", "run", "stopped", run)
		if stopped.code != 0 {
			t.Fatalf("the stop acknowledgment failed: %s", stopped.stderr)
		}
		if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != "<NULL>" {
			t.Fatalf("the stop acknowledgment retained the lease: %q", got)
		}
	})

	t.Run("infra_charges_only_dispatch_budget", func(t *testing.T) {
		spine, taskID, run, env := workerFixture(t, "infra failure")
		res := runMC(t, env, "", "complete", fmt.Sprint(taskID), "--run", run,
			"--infra", "--reason", "adapter exited")
		if res.code != 0 {
			t.Fatalf("infra terminal failed: %s", res.stderr)
		}
		db := openDB(t, spine)
		if got := queryStr(t, db, `SELECT status || '/' || dispatch_retries || '/' || correction_count FROM tasks WHERE id = ?`, taskID); got != "seeded/2/0" {
			t.Fatalf("infra-charged task = %q", got)
		}
		if got := queryStr(t, db, `SELECT outcome FROM runs WHERE id = ?`, run); got != "infra-failed" {
			t.Fatalf("run outcome = %q", got)
		}
	})

	t.Run("operator_revision_at_capacity_keeps_the_packet_slot", func(t *testing.T) {
		spine := initSpine(t)
		db := openDB(t, spine)
		ids := make([]int64, 0, 3)
		for i := 0; i < 3; i++ {
			ids = append(ids, proposalTask(t, spine, fmt.Sprintf("queued %d", i)))
		}
		arbiterPromoteAll(t, spine, dispatchThrough(t, spine, "spawn"))
		packets := make([]map[string]any, 0, 3)
		for _, id := range ids {
			commit := workedTask(t, spine, id)
			verifiedTask(t, spine, id, commit)
			packagedTask(t, spine, id)
			packets = append(packets, reviewedPacket(t, spine, id))
		}
		idle := runMC(t, spineEnv(spine), "", "dispatch")
		if idle.code != 0 {
			t.Fatalf("Dispatch failed: %s", idle.stderr)
		}
		assertTickIdleOnly(t, idle.json, "ws-test", "queue-full")
		res := runMC(t, spineEnv(spine), "", packetDecideArgs(ids[0], packets[0], "revise", "Add sources and repeat the checks.")...)
		if res.code != 0 {
			t.Fatalf("The operator revision failed: %s", res.stderr)
		}
		if got := queryStr(t, db, `SELECT status || '/' || revision_notes FROM tasks WHERE id = ?`, ids[0]); got != "seeded/Add sources and repeat the checks." {
			t.Fatalf("re-entered task = %q", got)
		}
		if got := queryInt(t, db, `SELECT t.archived FROM tasks t JOIN review_versions p ON p.id=t.current_review_id WHERE task_id = ?`, ids[0]); got != 0 {
			t.Fatalf("The revision freed its packet slot: %d", got)
		}
	})

	t.Run("validation_and_correction_count_ownership", func(t *testing.T) {
		_, taskID, run, env := workerFixture(t, "complete validation")
		for _, tc := range []struct {
			name string
			args []string
			want int
			msg  string
		}{
			{"missing_reason", []string{"complete", fmt.Sprint(taskID), "--run", run, "--needs-operator"}, 2, "--reason"},
			{"two_arms", []string{"complete", fmt.Sprint(taskID), "--run", run, "--status", "worked", "--infra", "--reason", "x"}, 2, "exactly one"},
			{"correction_count_owned_by_verdict", []string{"complete", fmt.Sprint(taskID), "--run", run, "--status", "worked", "--correction-count", "1"}, 1, "verifier verdict"},
			{"seeded_status", []string{"complete", fmt.Sprint(taskID), "--run", run, "--status", "seeded"}, 2, "worked or packaged"},
			{"bad_status", []string{"complete", fmt.Sprint(taskID), "--run", run, "--status", "done"}, 2, "worked or packaged"},
			{"missing_run", []string{"complete", fmt.Sprint(taskID), "--status", "worked"}, 2, "--run"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				res := runMC(t, env, "", tc.args...)
				if res.code != tc.want || !strings.Contains(res.stderr, tc.msg) {
					t.Fatalf("exit=%d stderr=%q, want %d containing %q", res.code, res.stderr, tc.want, tc.msg)
				}
			})
		}
	})
}

// ---------------------------------------------------------------------------
// Self-delegation: MC_HELPER set + MC_SPINE unset →
// mc re-invokes itself through `docker exec -i <helper> mc <argv…>`,
// passing argv, stdout, and the exit code through untouched. A stub docker
// on PATH keeps the fast lane Docker-free.
// ---------------------------------------------------------------------------

func TestCompleteRejectsCrossArmFields(t *testing.T) {
	tests := []struct {
		name string
		args []string
		msg  string
	}{
		{name: "packaged_missing_report", args: []string{"--status", "packaged", "--thesis", "t"}, msg: "durable report"},
		{name: "packaged_missing_thesis", args: []string{"--status", "packaged"}, msg: "--thesis"},
		{name: "infra_thesis", args: []string{"--infra", "--reason", "boom", "--thesis", "t"}, msg: "take only --reason"},
		{name: "worked_reason", args: []string{"--status", "worked", "--reason", "ignored"}, msg: "one status and an exact commit"},
		{name: "needs_operator_outputs", args: []string{"--needs-operator", "--reason", "need", "--outputs", "ignored"}, msg: "take only --reason"},
		{name: "needs_operator_commit", args: []string{"--needs-operator", "--reason", "need", "--commit", "cafe"}, msg: "take only --reason"},
		{name: "infra_outputs", args: []string{"--infra", "--reason", "boom", "--outputs", "ignored"}, msg: "take only --reason"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spine, taskID, run, env := workerFixture(t, tc.name)
			args := []string{"complete", fmt.Sprint(taskID), "--run", run}
			args = append(args, tc.args...)
			res := runMC(t, env, "", args...)
			if res.code != 2 || !strings.Contains(res.stderr, tc.msg) {
				t.Fatalf("exit = %d stderr %q, want usage containing %q", res.code, res.stderr, tc.msg)
			}
			db := openDB(t, spine)
			if got := queryStr(t, db, `SELECT status FROM tasks WHERE id = ?`, taskID); got != "seeded" {
				t.Fatalf("invalid field matrix moved task to %q", got)
			}
			if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != run {
				t.Fatalf("invalid field matrix disturbed lease: %q", got)
			}
		})
	}
}

// The packaged terminal opens the render before the spine records its path.
// `mc` runs inside the container of the Packager, so a container-local file
// exists for the check and still dies with the container. The render must be
// inside a declared artifact plane of the run.
func TestPackagedTerminalRefusesARenderThatWillNotSurvive(t *testing.T) {
	for _, tc := range []struct {
		name    string
		render  string // written into the artifact root before the call
		outputs func(root string) string
		msg     string
	}{
		{
			name: "container_local_scratch_stats_fine_and_is_still_refused",
			// The exact shape smoke task 2 shipped. It exists on disk.
			outputs: func(root string) string { return "" }, // filled in below
			msg:     "not under the durable artifact root",
		},
		{
			name:    "the_deliverable_worktree_is_not_the_artifact_plane",
			outputs: func(root string) string { return "/workspace/source/REVIEW_PACKET.md" },
			msg:     "not under the durable artifact root",
		},
		{
			name:    "a_render_that_never_landed",
			outputs: func(root string) string { return filepath.Join(root, packagerPlaneTarget, "missing.html") },
			msg:     "does not exist",
		},
		{
			name:    "a_render_created_but_never_written",
			render:  packagerPlaneTarget + "/empty.html",
			outputs: func(root string) string { return filepath.Join(root, packagerPlaneTarget, "empty.html") },
			msg:     "is empty",
		},
		{
			// The class root is ordinary image storage, so this file is real,
			// readable, non-empty and under the artifact root — it passes
			// every other check here — and it dies with the `--rm` container
			// all the same. Only membership of a declared plane separates it
			// from a durable render.
			name:    "the_class_root_itself_is_container_local_storage",
			render:  "loose.html",
			outputs: func(root string) string { return filepath.Join(root, "loose.html") },
			msg:     "not inside one of this run's mounted artifact planes",
		},
		{
			// A fabricated plane is indistinguishable from a bound one by
			// shape alone, which is exactly why shape is not what is checked.
			name:    "a_directory_the_role_made_up_is_not_a_plane",
			render:  "invented/packet.html",
			outputs: func(root string) string { return filepath.Join(root, "invented", "packet.html") },
			msg:     "not inside one of this run's mounted artifact planes",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spine := initSpine(t)
			taskID := proposalTask(t, spine, "packet render")
			arbiterPromoteAll(t, spine, dispatchThrough(t, spine, "spawn"))
			commit := workedTask(t, spine, taskID)
			verifiedTask(t, spine, taskID, commit)
			eff, repo := spawnRole(t, spine, "packager")
			run := eff["run_id"].(string)
			env, plane := roleEnv(t, spine, run, "packager", repo)
			root := filepath.Dir(plane)
			if tc.render != "" {
				target := filepath.Join(root, tc.render)
				if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			outputs := tc.outputs(root)
			if outputs == "" {
				// Container-local scratch: a real, readable, non-empty file that
				// simply is not on a durable root.
				scratch := filepath.Join(t.TempDir(), "REVIEW_PACKET_task_2.md")
				if err := os.WriteFile(scratch, []byte("<h1>packet</h1>\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				outputs = scratch
			}
			got := runMC(t, env, "", "complete", fmt.Sprint(taskID),
				"--run", run, "--status", "packaged", "--outputs", outputs, "--thesis", "ships the thing")
			if got.code != 1 || !strings.Contains(got.stderr, tc.msg) {
				t.Fatalf("exit = %d stderr %q, want 1 containing %q", got.code, got.stderr, tc.msg)
			}
			db := openDB(t, spine)
			// No packet born against an unreadable render, no slot consumed, and
			// the lease still held so the run can render properly and retry.
			if n := queryInt(t, db, `SELECT COUNT(*) FROM tasks t JOIN review_versions p ON p.id=t.current_review_id`); n != 0 {
				t.Fatalf("refused terminal birthed %d packets", n)
			}
			if s := queryStr(t, db, `SELECT status FROM tasks WHERE id = ?`, taskID); s != "verified" {
				t.Fatalf("refused terminal advanced the task to %q", s)
			}
			if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != run {
				t.Fatalf("refused terminal disturbed lease: %q", got)
			}
		})
	}
}

func TestPhase2TerminalTransactionsRollback(t *testing.T) {
	t.Run("strategist_valid_first_invalid_second", func(t *testing.T) {
		spine := initSpine(t)
		eff := dispatchExpect(t, spine, "spawn")
		run := eff["run_id"].(string)
		env := runJSONEnv(t, spine, run, "pipeline", "strategist(propose)")
		batch := `{"proposals":[
			{"project":"ws-test","title":"must roll back","strategic_rationale":"Observed: the fixture lacks this result."},
			{"project":"ws-other","title":"another Project","strategic_rationale":"Observed: the fixture lacks this result."}]}`
		res := runMC(t, env, batch, "strategist", "propose", "--run", run, "--batch", "-")
		if res.code != 1 {
			t.Fatalf("exit = %d stderr=%q, want domain rollback", res.code, res.stderr)
		}
		db := openDB(t, spine)
		if got := queryInt(t, db, `SELECT COUNT(*) FROM tasks`); got != 0 {
			t.Fatalf("half Strategist batch committed %d tasks", got)
		}
		if got := queryStr(t, db, `SELECT ended_at FROM runs WHERE id=?`, run); got != "<NULL>" {
			t.Fatalf("failed Strategist batch ended run at %q", got)
		}
		if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != run {
			t.Fatalf("failed Strategist batch released lease: %q", got)
		}
	})

	t.Run("arbiter_reject_first_blocked_promote_second", func(t *testing.T) {
		spine := initSpine(t)
		t1 := proposalTask(t, spine, "reject must roll back")
		t2 := proposalTask(t, spine, "blocked after snapshot")
		eff := dispatchExpect(t, spine, "spawn")
		run := eff["run_id"].(string)
		db := openDB(t, spine)
		if _, err := db.Exec(`UPDATE tasks SET blocked=1, blocked_kind='operator', blocked_reason='concurrent hold' WHERE id=?`, t2); err != nil {
			t.Fatal(err)
		}
		batch := fmt.Sprintf(`{"verdicts":[
			{"task":%d,"decision":"reject","reason":"weak"},
			{"task":%d,"decision":"promote","reason":"strong"}]}`, t1, t2)
		res := runMC(t, runJSONEnv(t, spine, run, "pipeline", "arbiter"), batch,
			"arbiter", "decide", "--run", run, "--batch", "-")
		if res.code != 1 {
			t.Fatalf("exit = %d stderr=%q, want atomic refusal", res.code, res.stderr)
		}
		if got := queryStr(t, db, `SELECT status || '/' || COALESCE(decision, 'null') || '/' || archived FROM tasks WHERE id=?`, t1); got != "proposed/null/0" {
			t.Fatalf("first Arbiter verdict half-committed: %q", got)
		}
		if got := queryInt(t, db, `SELECT COUNT(*) FROM activity WHERE kind='task.rejected' AND subject=?`, t1); got != 0 {
			t.Fatalf("rolled-back rejection left %d activity rows", got)
		}
		if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != run {
			t.Fatalf("failed Arbiter batch released lease: %q", got)
		}
	})
}

func TestSelfDelegation(t *testing.T) {
	stubDir := t.TempDir()
	stub := filepath.Join(stubDir, "docker")
	script := "#!/bin/sh\necho \"{\\\"stub\\\":\\\"$*\\\"}\"\nexit 7\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"MC_HELPER=mc-helper-1",
		"PATH=" + stubDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	res := runMC(t, env, "", "task", "get", "1")
	if res.code != 7 {
		t.Fatalf("exit = %d, want the delegate's 7 passed through", res.code)
	}
	want := "exec -i mc-helper-1 mc task get 1"
	if !strings.Contains(res.stdout, want) {
		t.Fatalf("stdout %q missing delegated argv %q", res.stdout, want)
	}

	t.Run("direct_spine_wins_over_helper", func(t *testing.T) {
		spine := initSpine(t)
		env := append([]string{
			"MC_HELPER=mc-helper-1",
			"PATH=" + stubDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		}, spineEnv(spine)...)
		res := runMC(t, env, "", "packet", "list")
		if res.code != 0 || strings.Contains(res.stdout, "stub") {
			t.Fatalf("MC_SPINE set must not delegate: code %d, out %q", res.code, res.stdout)
		}
	})
}

func TestHostDispatchRequiresOneUseResidentControl(t *testing.T) {
	stubDir := t.TempDir()
	stub := filepath.Join(stubDir, "docker")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho '{\"unexpected\":\"docker ran\"}'\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"MC_HELPER=mc-helper-1",
		"PATH=" + stubDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	res := runMC(t, env, "", "dispatch")
	if res.code != 1 {
		t.Fatalf("exit = %d, want protocol refusal 1; stdout=%q stderr=%q", res.code, res.stdout, res.stderr)
	}
	if !strings.Contains(res.stderr, "resident control descriptor") {
		t.Fatalf("stderr %q does not name the missing resident control descriptor", res.stderr)
	}
	if strings.Contains(res.stdout, "docker ran") {
		t.Fatalf("dispatch reached the helper without resident control: %q", res.stdout)
	}
}

func TestPrivateDispatchVerbsRequireHelperSpineScope(t *testing.T) {
	for _, verb := range []string{"__dispatch-prepare", "__dispatch-commit"} {
		res := runMC(t, nil, "", verb)
		if res.code != 1 || !strings.Contains(res.stderr, "helper's fixed spine scope") {
			t.Fatalf("%s exit=%d stderr=%q, want fixed helper-scope refusal", verb, res.code, res.stderr)
		}
		if res.stdout != "" {
			t.Fatalf("%s leaked a public/private frame on stdout: %q", verb, res.stdout)
		}
	}
}

func TestHostDispatchHelloThenUsesPrivatePrepareCommit(t *testing.T) {
	spine := initSpine(t)
	stdout, stderr, err := brokerDispatch(t, spine, brokerHelperStub)
	if err != nil {
		t.Fatalf("dispatch exit err=%v, want private prepare/commit success; stdout=%q stderr=%q", err, stdout, stderr)
	}
	var tick map[string]any
	if err := json.Unmarshal([]byte(stdout), &tick); err != nil {
		t.Fatalf("dispatch stdout %q is not the final tick: %v", stdout, err)
	}
	if effect := tickOnlyAction(t, tick); effect["action"] != "spawn" {
		t.Fatalf("dispatch effect = %v, want spawn through private prepare/commit", effect)
	}
	if stderr != "" {
		t.Fatalf("private helper frames leaked to stderr: %q", stderr)
	}
}

// brokerHelperStub is a docker command that runs the test binary as the helper
// of the fixture spine.
const brokerHelperStub = "#!/bin/sh\n" +
	"test \"$1/$2/$3/$4\" = \"exec/-i/mc-helper-1/mc\" || exit 91\n" +
	"shift 4\n" +
	"exec env MC_SPINE=\"$MC_TEST_SPINE\" MC_HOME= MC_HELPER= \"$MC_TEST_BIN\" \"$@\"\n"

// brokerDispatch runs `mc dispatch` as the host broker of the fixture spine.
// stub is the script of the docker command on its PATH. A stand-in answers the
// resident control requests. It returns the output of the command.
func brokerDispatch(t *testing.T, spine, stub string) (string, string, error) {
	t.Helper()
	stubDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stubDir, "docker"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	parent := os.NewFile(uintptr(pair[0]), "resident-control-parent")
	child := os.NewFile(uintptr(pair[1]), "resident-control-child")
	defer parent.Close()

	cmd := exec.Command(mcBin, "dispatch")
	cmd.Env = hermeticEnv(
		"MC_HELPER=mc-helper-1",
		"MC_HOME="+filepath.Dir(spine),
		"MC_TEST_BIN="+mcBin,
		"MC_TEST_SPINE="+spine,
		"MC_TEST_TMP="+stubDir,
		"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	cmd.ExtraFiles = []*os.File{child} // child fd 3
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	serverErr := make(chan error, 1)
	go func() { serverErr <- answerResidentControl(parent) }()
	runErr := cmd.Run()
	// No process holds the control descriptor now, so an exchange that the
	// command did not complete ends with an error.
	child.Close()
	if err := <-serverErr; err != nil && runErr == nil {
		t.Fatalf("resident control: %v (stdout=%q stderr=%q)", err, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String(), runErr
}

// answerResidentControl answers the three control requests of one broker
// dispatch: the hello, the Collie inventory, and the pipeline liveness. Each
// observation is empty.
func answerResidentControl(parent *os.File) error {
	var prefix [4]byte
	if _, err := io.ReadFull(parent, prefix[:]); err != nil {
		return fmt.Errorf("read hello length: %w", err)
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n == 0 || n > 64*1024 {
		return fmt.Errorf("hello length %d outside frame bound", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(parent, body); err != nil {
		return fmt.Errorf("read hello: %w", err)
	}
	var hello struct {
		Op                  string `json:"op"`
		Seq                 int    `json:"seq"`
		ReleaseBuildID      string `json:"release_build_id"`
		ControlVersion      int    `json:"gateway_control_version"`
		SpineSchemaID       string `json:"spine_schema_id"`
		ConfigSchemaVersion int    `json:"config_schema_version"`
		DeploymentUUID      string `json:"deployment_uuid"`
	}
	if err := json.Unmarshal(body, &hello); err != nil {
		return fmt.Errorf("decode hello: %w", err)
	}
	if hello.Op != "hello" || hello.Seq != 1 || hello.ReleaseBuildID == "" ||
		hello.ControlVersion != verbs.GatewayControlVersion || hello.SpineSchemaID != substrate.CurrentSchemaID ||
		hello.ConfigSchemaVersion != 1 || hello.DeploymentUUID == "" {
		return fmt.Errorf("unexpected hello: %+v", hello)
	}
	hello.Op = "hello_ack"
	ack, err := json.Marshal(hello)
	if err != nil {
		return err
	}
	binary.BigEndian.PutUint32(prefix[:], uint32(len(ack)))
	if _, err := parent.Write(append(prefix[:], ack...)); err != nil {
		return fmt.Errorf("write hello_ack: %w", err)
	}
	for _, exchange := range []struct{ name, request, response string }{
		{"inventory", `{"op":"collie_inventory","seq":2}`, `{"op":"collie_inventory_result","seq":2,"containers":[]}`},
		{"pipeline liveness", `{"op":"pipeline_liveness","seq":3}`, `{"op":"pipeline_liveness_result","seq":3,"observed":[]}`},
	} {
		if _, err := io.ReadFull(parent, prefix[:]); err != nil {
			return fmt.Errorf("read %s request length: %w", exchange.name, err)
		}
		body = make([]byte, binary.BigEndian.Uint32(prefix[:]))
		if _, err := io.ReadFull(parent, body); err != nil {
			return fmt.Errorf("read %s request: %w", exchange.name, err)
		}
		if string(body) != exchange.request {
			return fmt.Errorf("unexpected %s request: %s", exchange.name, body)
		}
		binary.BigEndian.PutUint32(prefix[:], uint32(len(exchange.response)))
		if _, err := parent.Write(append(prefix[:], exchange.response...)); err != nil {
			return fmt.Errorf("write %s response: %w", exchange.name, err)
		}
	}
	return nil
}

// Missing MC_SPINE with no helper is an environment error, not a crash.
func TestMissingSpineIsUsageError(t *testing.T) {
	res := runMC(t, nil, "", "dispatch")
	if res.code != 2 || !strings.Contains(res.stderr, "MC_SPINE") {
		t.Fatalf("exit = %d stderr %q, want 2 naming MC_SPINE", res.code, res.stderr)
	}
}

// The read verbs of the Docker e2e's assertion channel: the e2e asserts
// lease/runs state and cannot open the spine volume, so mc lease list /
// mc settings get / mc run list are its only window.
func TestLeaseListSettingsGetAndRunList(t *testing.T) {
	spine := initSpine(t, "--heartbeat-interval-s", "7")

	leaseRows := func(t *testing.T) []map[string]any {
		t.Helper()
		res := runMC(t, spineEnv(spine), "", "lease", "list")
		if res.code != 0 {
			t.Fatalf("lease list failed (%d): %s", res.code, res.stderr)
		}
		raw, ok := res.json["leases"].([]any)
		if !ok {
			t.Fatalf("lease list = %v, want a leases list", res.json)
		}
		rows := make([]map[string]any, 0, len(raw))
		for _, item := range raw {
			rows = append(rows, item.(map[string]any))
		}
		return rows
	}
	leaseKeys := []string{"project", "status", "run_id", "owner", "subject",
		"acquired_at", "last_heartbeat_at", "binding", "run_outcome", "run_ended_at", "pool_snapshot", "run_role", "agent_started_at"}
	assertLeaseShape := func(t *testing.T, row map[string]any) {
		t.Helper()
		if len(row) != len(leaseKeys) {
			t.Fatalf("lease row = %v, want exactly the keys %v", row, leaseKeys)
		}
		for _, key := range leaseKeys {
			if _, ok := row[key]; !ok {
				t.Fatalf("lease row %v lacks %q", row, key)
			}
		}
	}

	// One Project, one free lease row: every claim field null.
	rows := leaseRows(t)
	if len(rows) != 1 {
		t.Fatalf("fresh lease list = %v, want one row per Project", rows)
	}
	free := rows[0]
	assertLeaseShape(t, free)
	if free["project"] != "ws-test" || free["status"] != "free" {
		t.Fatalf("fresh lease = %v, want ws-test free", free)
	}
	for _, key := range leaseKeys[2:] {
		if free[key] != nil {
			t.Fatalf("free lease %s = %v, want null", key, free[key])
		}
	}

	settings := runMC(t, spineEnv(spine), "", "settings", "get")
	if settings.code != 0 {
		t.Fatalf("settings get failed (%d): %s", settings.code, settings.stderr)
	}
	if got := settings.json["heartbeat_interval_s"].(float64); got != 7 {
		t.Fatalf("settings get heartbeat_interval_s = %v, want the init tunable 7", got)
	}
	for _, key := range []string{"timeout_minutes", "grace_minutes", "heartbeat_interval_s",
		"spawn_grace_s", "hard_deadline_minutes", "review_wip_cap",
		"collie_idle_timeout_s", "wiki_update_interval_s"} {
		if _, ok := settings.json[key]; !ok {
			t.Fatalf("settings get %v lacks %q", settings.json, key)
		}
	}
	if len(settings.json) != 8 {
		t.Fatalf("settings get = %v, want exactly the eight settings", settings.json)
	}
	if settings.json["review_wip_cap"] != float64(3) || settings.json["collie_idle_timeout_s"] != float64(1800) ||
		settings.json["wiki_update_interval_s"] != float64(3600) {
		t.Fatalf("settings get defaults = %v, want review_wip_cap 3, collie_idle_timeout_s 1800, and wiki_update_interval_s 3600", settings.json)
	}

	runs := runMC(t, spineEnv(spine), "", "run", "list")
	if runs.code != 0 {
		t.Fatalf("run list failed (%d): %s", runs.code, runs.stderr)
	}
	if n := len(withoutScribeRuns(runs.json["runs"].([]any))); n != 0 {
		t.Fatalf("fresh spine has %d runs rows, want 0", n)
	}

	// One claim: the Project's lease row reflects it, run list gains the
	// row.
	id := proposalTask(t, spine, "read-verbs task")
	eff := dispatchExpect(t, spine, "spawn")
	runID := eff["run_id"].(string)

	rows = leaseRows(t)
	if len(rows) != 1 {
		t.Fatalf("lease list after claim = %v, want one row", rows)
	}
	held := rows[0]
	assertLeaseShape(t, held)
	if held["project"] != "ws-test" || held["status"] != "held" ||
		held["run_id"] != runID || held["owner"] != "arbiter" {
		t.Fatalf("lease after claim = %v, want ws-test held by run %s owner arbiter", held, runID)
	}
	if held["subject"] != nil || held["acquired_at"] == nil || held["binding"] == nil || held["pool_snapshot"] != fmt.Sprintf("[%d]", id) {
		t.Fatalf("The shared review lease lost its assignment: %v", held)
	}
	if held["last_heartbeat_at"] != nil {
		t.Fatalf("fresh claim must have NULL last_heartbeat_at: %v", held)
	}

	runs = runMC(t, spineEnv(spine), "", "run", "list")
	runRows := withoutScribeRuns(runs.json["runs"].([]any))
	if len(runRows) != 1 {
		t.Fatalf("run list has %d rows, want 1", len(runRows))
	}
	row := runRows[0].(map[string]any)
	if row["id"] != runID || row["role"] != "arbiter" || row["ended_at"] != nil {
		t.Fatalf("run row = %v, want live arbiter run %s", row, runID)
	}
	_ = id

	// A second Project brings its own free lease row; rows come in
	// Project id order.
	twoWorkspace := gitFixtureWorkspace(t)
	if _, err := openDB(t, spine).Exec(
		`INSERT INTO sandbox_profiles (id, workspace_root, artifact_roots)
		 VALUES ('ws-alpha-profile', ?, '[]')`, twoWorkspace); err != nil {
		t.Fatal(err)
	}
	if add := runMC(t, spineEnv(spine), "", "project", "add", "ws-alpha",
		"--title", "Alpha", "--sandbox-profile", "ws-alpha-profile"); add.code != 0 {
		t.Fatalf("project add failed: %s", add.stderr)
	}
	rows = leaseRows(t)
	if len(rows) != 2 || rows[0]["project"] != "ws-alpha" || rows[1]["project"] != "ws-test" {
		t.Fatalf("lease list with two Projects = %v, want ws-alpha then ws-test", rows)
	}
	if rows[0]["status"] != "free" || rows[1]["status"] != "held" {
		t.Fatalf("lease statuses = %v, want ws-alpha free, ws-test held", rows)
	}

	// `mc lock get` left with the lock table: it is no verb any more.
	lock := runMC(t, spineEnv(spine), "", "lock", "get")
	if lock.code != 2 {
		t.Fatalf("mc lock get exit = %d stderr=%q, want usage error 2 (the verb is gone)", lock.code, lock.stderr)
	}
}

// mc wiki-state list returns one row for each Project, in identifier order.
// The resident and the dashboard read the wiki state through it, and mc run
// list states the wiki update of each Scribe run.
func TestWikiStateListCLI(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	states := func(t *testing.T) []map[string]any {
		t.Helper()
		res := runMC(t, spineEnv(spine), "", "wiki-state", "list")
		if res.code != 0 {
			t.Fatalf("wiki-state list failed (%d): %s", res.code, res.stderr)
		}
		raw, ok := res.json["wiki_states"].([]any)
		if !ok || len(res.json) != 1 {
			t.Fatalf("wiki-state list = %v, want only a wiki_states list", res.json)
		}
		rows := make([]map[string]any, 0, len(raw))
		for _, item := range raw {
			rows = append(rows, item.(map[string]any))
		}
		return rows
	}
	assertKeys := func(t *testing.T, what string, object map[string]any, keys ...string) {
		t.Helper()
		if len(object) != len(keys) {
			t.Fatalf("%s = %v, want exactly the keys %v", what, object, keys)
		}
		for _, key := range keys {
			if _, ok := object[key]; !ok {
				t.Fatalf("%s %v lacks %q", what, object, key)
			}
		}
	}
	assertRow := func(t *testing.T, row map[string]any, want map[string]any) {
		t.Helper()
		assertKeys(t, "wiki state row", row, "project", "state", "state_text", "accepted_commit",
			"next_start_at", "next_start_text", "active", "last")
		for key, value := range want {
			if row[key] != value {
				t.Fatalf("wiki state row %v has %s = %v, want %v", row, key, row[key], value)
			}
		}
	}
	wikiUpdate := func(t *testing.T, run string) any {
		t.Helper()
		res := runMC(t, spineEnv(spine), "", "run", "list")
		if res.code != 0 {
			t.Fatalf("run list failed (%d): %s", res.code, res.stderr)
		}
		for _, raw := range res.json["runs"].([]any) {
			if row := raw.(map[string]any); row["id"] == run {
				update, ok := row["wiki_update"]
				if !ok {
					t.Fatalf("run row %v lacks the key wiki_update", row)
				}
				return update
			}
		}
		t.Fatalf("run list holds no run %s", run)
		return nil
	}

	// The fixture holds one successful wiki update at the commit of main.
	workspace := queryStr(t, db, `SELECT p.workspace_root FROM projects w JOIN sandbox_profiles p ON p.id = w.sandbox_profile WHERE w.id = 'ws-test'`)
	commit := fixtureGit(t, workspace, "rev-parse", "refs/heads/main")
	rows := states(t)
	if len(rows) != 1 {
		t.Fatalf("wiki state rows = %v, want one row of ws-test", rows)
	}
	assertRow(t, rows[0], map[string]any{"project": "ws-test", "state": "current", "state_text": "The Project wiki is current.",
		"accepted_commit": commit, "next_start_at": nil, "next_start_text": nil, "active": nil})
	last, _ := rows[0]["last"].(map[string]any)
	assertKeys(t, "last wiki update", last, "run_id", "started_at", "ended_at", "result", "result_text", "cause_text",
		"start_commit", "wiki_commit", "merged_commit")
	fixtureRun, _ := last["run_id"].(string)
	if fixtureRun == "" || last["result"] != "succeeded" || last["result_text"] != "The Project wiki needed no change." || last["cause_text"] != nil ||
		last["start_commit"] != commit || last["wiki_commit"] != commit || last["merged_commit"] != nil {
		t.Fatalf("last wiki update = %v, want the successful wiki update of the fixture at %s with no merge", last, commit)
	}
	if got, want := wikiUpdate(t, fixtureRun), (map[string]any{"result": "succeeded", "result_text": "The Project wiki needed no change.", "cause_text": nil}); !reflect.DeepEqual(got, want) {
		t.Fatalf("wiki update of the fixture run in mc run list = %v, want %v", got, want)
	}

	// A new Project has no wiki update, and the spine holds no accepted commit for it.
	alpha := gitFixtureWorkspace(t)
	alphaHome, err := verbs.PrepareProjectHome(filepath.Dir(spine), "ws-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sandbox_profiles (id, workspace_root, artifact_roots, tool_home_dir) VALUES ('ws-alpha-profile', ?, '[]', ?)`, alpha, alphaHome.Canonical); err != nil {
		t.Fatal(err)
	}
	if add := runMC(t, spineEnv(spine), "", "project", "add", "ws-alpha", "--title", "Alpha", "--sandbox-profile", "ws-alpha-profile"); add.code != 0 {
		t.Fatalf("project add failed: %s", add.stderr)
	}
	recordFixtureEnvironment(t, db, "ws-alpha")
	rows = states(t)
	if len(rows) != 2 || rows[1]["project"] != "ws-test" {
		t.Fatalf("wiki state rows = %v, want ws-alpha then ws-test", rows)
	}
	assertRow(t, rows[0], map[string]any{"project": "ws-alpha", "state": "behind", "state_text": "The Project wiki is behind the accepted commit.",
		"accepted_commit": nil, "next_start_at": nil, "next_start_text": "Culler cannot read the main branch of the Project repository.",
		"active": nil, "last": nil})

	// The tick stores the commit of main and starts the first wiki update of the new Project.
	var run string
	for _, action := range tickActions(t, dispatch(t, spine)) {
		if action["action"] == "spawn" && action["role"] == "scribe" && action["project"] == "ws-alpha" {
			run, _ = action["run_id"].(string)
		}
	}
	if run == "" {
		t.Fatal("the tick started no wiki update of ws-alpha")
	}
	rows = states(t)
	if len(rows) != 2 {
		t.Fatalf("wiki state rows = %v, want two rows", rows)
	}
	assertRow(t, rows[0], map[string]any{"project": "ws-alpha", "state": "update-active", "state_text": "A wiki update is active.",
		"accepted_commit": fixtureGit(t, alpha, "rev-parse", "refs/heads/main"), "next_start_at": nil, "next_start_text": nil, "last": nil})
	active, _ := rows[0]["active"].(map[string]any)
	assertKeys(t, "active wiki update", active, "run_id", "started_at", "agent_started_at", "last_heartbeat_at", "run_ended_at",
		"step", "step_text", "repair_text")
	if active["run_id"] != run || active["started_at"] == nil || active["agent_started_at"] != nil || active["last_heartbeat_at"] != nil ||
		active["run_ended_at"] != nil || active["step"] != "run" || active["step_text"] != "The Scribe updates the Project wiki." || active["repair_text"] != nil {
		t.Fatalf("active wiki update = %v, want the run %s in the step run", active, run)
	}
	assertRow(t, rows[1], map[string]any{"project": "ws-test", "state": "current"})
	if got, want := wikiUpdate(t, run), (map[string]any{"result": nil, "result_text": nil, "cause_text": nil}); !reflect.DeepEqual(got, want) {
		t.Fatalf("wiki update of the active Scribe run in mc run list = %v, want %v", got, want)
	}

	// The read holds no row of an archived Project.
	if archive := runMC(t, spineEnv(spine), "", "project", "archive", "ws-test"); archive.code != 0 {
		t.Fatalf("project archive failed: %s", archive.stderr)
	}
	if rows = states(t); len(rows) != 1 || rows[0]["project"] != "ws-alpha" {
		t.Fatalf("wiki state rows after the archive of ws-test = %v, want only ws-alpha", rows)
	}

	// The verb takes no flag and no argument.
	for _, args := range [][]string{{"wiki-state"}, {"wiki-state", "show"}, {"wiki-state", "list", "ws-alpha"},
		{"wiki-state", "list", "--project", "ws-alpha"}} {
		res := runMC(t, spineEnv(spine), "", args...)
		refusal, _ := res.json["error"].(map[string]any)
		message, _ := refusal["message"].(string)
		if res.code != 2 || refusal["code"] != "usage" || !strings.Contains(message, "usage: mc wiki-state list") {
			t.Fatalf("mc %v: exit %d with %v, want the usage error of mc wiki-state list", args, res.code, res.json)
		}
	}
}

// mc packet list rows name the packet's Project: the review queue is per
// Project, so a reader can tell the queues apart.
func TestPacketListRowsCarryProject(t *testing.T) {
	spine := initSpine(t)
	taskID := proposalTask(t, spine, "packet with a project")
	packageTask(t, spine, taskID)
	reviewedPacket(t, spine, taskID)
	pl := runMC(t, spineEnv(spine), "", "packet", "list")
	if pl.code != 0 {
		t.Fatalf("packet list failed (%d): %s", pl.code, pl.stderr)
	}
	packets := pl.json["packets"].([]any)
	if len(packets) != 1 {
		t.Fatalf("packets = %v, want exactly one", packets)
	}
	packet := packets[0].(map[string]any)
	if packet["task_id"] != float64(taskID) || packet["project"] != "ws-test" {
		t.Fatalf("packet = %v, want task %d of ws-test", packet, taskID)
	}
}

// mc project add refuses an id outside ^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$
// (the id names Docker containers and state directories) as a usage error
// before it touches the spine.
func TestProjectAddValidatesID(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	workspace := gitFixtureWorkspace(t)
	if _, err := db.Exec(
		`INSERT INTO sandbox_profiles (id, workspace_root, artifact_roots)
		 VALUES ('ws-id-profile', ?, '[]')`, workspace); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"-leading-dash", ".leading-dot", "_leading-underscore", "has space",
		"slash/inside", "colon:inside", "ünïcode", strings.Repeat("x", 65),
	} {
		res := runMC(t, spineEnv(spine), "", "project", "add", bad,
			"--title", "Bad id", "--sandbox-profile", "ws-id-profile")
		if res.code != 2 {
			t.Fatalf("project add %q exit = %d stderr=%q, want usage error 2", bad, res.code, res.stderr)
		}
		if n := queryInt(t, db, `SELECT COUNT(*) FROM projects WHERE id = ?`, bad); n != 0 {
			t.Fatalf("project add %q was refused yet inserted", bad)
		}
	}
	good := "Ws_1.two-" + strings.Repeat("y", 55)
	if len(good) != 64 {
		t.Fatalf("fixture id length = %d, want the 64-byte maximum", len(good))
	}
	res := runMC(t, spineEnv(spine), "", "project", "add", good,
		"--title", "Good id", "--sandbox-profile", "ws-id-profile")
	if res.code != 0 {
		t.Fatalf("project add %q exit = %d stderr=%q, want success", good, res.code, res.stderr)
	}
	if n := queryInt(t, db, `SELECT COUNT(*) FROM leases WHERE project = ? AND run_id IS NULL`, good); n != 1 {
		t.Fatalf("free lease rows for %q = %d, want 1", good, n)
	}
}

// ---------------------------------------------------------------------------
// Operational verbs: doctor / backup / reset
// ---------------------------------------------------------------------------

// backupFiles lists MC_HOME/backups in name order.
func backupFiles(t *testing.T, spine string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(spine), "backups"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range entries {
		// Ignore WAL/SHM siblings materialized by the test's own snapshot
		// inspection; the snapshot set is the .db files.
		if strings.HasSuffix(e.Name(), "-wal") || strings.HasSuffix(e.Name(), "-shm") {
			continue
		}
		names = append(names, e.Name())
	}
	return names
}

func TestBackup(t *testing.T) {
	t.Run("snapshot_is_consistent_and_source_stays_usable", func(t *testing.T) {
		spine := initSpine(t)
		proposalTask(t, spine, "survives the snapshot")

		res := runMC(t, spineEnv(spine), "", "backup")
		if res.code != 0 {
			t.Fatalf("backup failed (%d): %s", res.code, res.stderr)
		}
		snapshot, _ := res.json["snapshot"].(string)
		if !strings.HasPrefix(snapshot, filepath.Join(filepath.Dir(spine), "backups")+string(filepath.Separator)) {
			t.Fatalf("snapshot path = %q, want under MC_HOME/backups/", snapshot)
		}
		if res.json["bytes"].(float64) <= 0 {
			t.Fatalf("snapshot bytes = %v", res.json["bytes"])
		}
		// The snapshot is a complete, consistent spine: identity and records.
		snapDB, err := substrate.Open(snapshot)
		if err != nil {
			t.Fatalf("open snapshot: %v", err)
		}
		defer snapDB.Close()
		if n := queryInt(t, snapDB, `SELECT COUNT(*) FROM tasks`); n != 1 {
			t.Fatalf("snapshot task rows = %d, want 1", n)
		}
		if got := queryStr(t, snapDB, `SELECT deployment_uuid FROM meta WHERE id = 1`); got == "" {
			t.Fatal("snapshot lost the deployment identity")
		}
		// No .tmp residue and the source keeps accepting writes.
		for _, name := range backupFiles(t, spine) {
			if strings.Contains(name, ".tmp-") {
				t.Fatalf("temp snapshot residue: %q", name)
			}
		}
		proposalTask(t, spine, "post-backup write")

		// A same-second second snapshot lands as its own file.
		again := runMC(t, spineEnv(spine), "", "backup")
		if again.code != 0 || again.json["snapshot"] == snapshot {
			t.Fatalf("second backup = code %d snapshot %v", again.code, again.json["snapshot"])
		}
		if n := len(backupFiles(t, spine)); n != 2 {
			t.Fatalf("backups dir has %d files, want 2", n)
		}
	})

	t.Run("backup_is_host_scope_and_refuses_before_touching_anything", func(t *testing.T) {
		spine := initSpine(t)
		for name, env := range map[string][]string{
			"pipeline": runJSONEnv(t, spine, "r-x", "pipeline", "worker"),
			"collie":   collieJSONEnv(t, spine, "h-x", defaultCollieAllowlist),
		} {
			res := runMC(t, env, "", "backup")
			if res.code != 1 {
				t.Fatalf("%s backup exit = %d stderr %q", name, res.code, res.stderr)
			}
		}
		if n := len(backupFiles(t, spine)); n != 0 {
			t.Fatalf("denied backup wrote %d snapshot files", n)
		}
		missing := runMC(t, []string{"MC_SPINE=" + filepath.Join(t.TempDir(), "absent.db"), "MC_HOME=" + t.TempDir()}, "", "backup")
		if missing.code != 1 || !strings.Contains(missing.stderr, "no spine") {
			t.Fatalf("missing-spine backup = code %d stderr %q", missing.code, missing.stderr)
		}
	})
}

func TestDoctor(t *testing.T) {
	findingByCheck := func(t *testing.T, res mcResult) map[string]map[string]any {
		t.Helper()
		out := map[string]map[string]any{}
		findings, ok := res.json["findings"].([]any)
		if !ok {
			t.Fatalf("doctor result = %v", res.json)
		}
		for _, raw := range findings {
			f := raw.(map[string]any)
			check, _ := f["check"].(string)
			status, _ := f["status"].(string)
			section, _ := f["onboard_section"].(string)
			if check == "" || status == "" {
				t.Fatalf("finding missing check/status: %v", f)
			}
			// Release-mechanic findings carry no onboarding section:
			// their repair is the front doors', and the pointer lives in the
			// detail. Every setup finding still names its repairing section.
			if section == "" && check != "container-runtime" && check != "supervision" && check != "agentsview" && check != "wiki" {
				t.Fatalf("finding missing onboard_section: %v", f)
			}
			out[check] = f
		}
		return out
	}
	prepareIdentityAndSurfaces := func(t *testing.T, spine string) string {
		t.Helper()
		db := openDB(t, spine)
		uuid := queryStr(t, db, `SELECT deployment_uuid FROM meta WHERE id = 1`)
		db.Close()
		if err := os.WriteFile(filepath.Join(filepath.Dir(spine), "deployment.uuid"), []byte(uuid+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return uuid
	}

	t.Run("unverified_deployment_reports_incomplete_checks", func(t *testing.T) {
		spine := initSpine(t)
		prepareIdentityAndSurfaces(t, spine)
		res := runMC(t, spineEnv(spine), "", "doctor")
		if res.code != 0 || res.json["ok"] != false || res.json["deployment_complete"] != false {
			t.Fatalf("doctor = code %d json %v stderr %q", res.code, res.json, res.stderr)
		}
		findings := findingByCheck(t, res)
		for check, wantStatus := range map[string]string{
			"mc-home": "ok", "spine": "ok", "deployment-identity": "ok",
			"config": "ok", "routing": "ok", "projects": "ok", "surfaces": "ok",
			"container-runtime": "deferred",
			"runtime-auth":      "deferred", "supervision": "deferred",
			"agentsview": "deferred", "wiki": "deferred",
		} {
			f, present := findings[check]
			if !present || f["status"] != wantStatus {
				t.Fatalf("finding %q = %v, want status %q", check, f, wantStatus)
			}
		}
		// The report excludes the retired gateway check.
		if f, present := findings["gateway"]; present {
			t.Fatalf("unexpected gateway check = %v", f)
		}
		if findings["routing"]["onboard_section"] != "routing" ||
			findings["spine"]["onboard_section"] != "home" ||
			findings["runtime-auth"]["onboard_section"] != "runtime-auth" {
			t.Fatalf("repairing sections = %v", findings)
		}
	})

	t.Run("invalid_config_fails_config_and_suppresses_routing_validation", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			mutate func(t *testing.T, path string)
		}{
			{name: "missing", mutate: func(t *testing.T, path string) {
				t.Helper()
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}},
			{name: "malformed", mutate: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("not = [valid\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				spine := initSpine(t)
				prepareIdentityAndSurfaces(t, spine)
				tc.mutate(t, filepath.Join(filepath.Dir(spine), "config.toml"))
				res := runMC(t, spineEnv(spine), "", "doctor")
				if res.code != 0 || res.json["ok"] != false {
					t.Fatalf("doctor = code %d json %v stderr %q", res.code, res.json, res.stderr)
				}
				findings := findingByCheck(t, res)
				if findings["config"]["status"] != "fail" ||
					findings["routing"]["status"] != "fail" ||
					!strings.Contains(findings["routing"]["detail"].(string), "not checked") {
					t.Fatalf("config/routing findings = %v / %v", findings["config"], findings["routing"])
				}
			})
		}
	})

	t.Run("failures_name_their_repairing_section_without_mutation", func(t *testing.T) {
		spine := initSpine(t)
		prepareIdentityAndSurfaces(t, spine)
		proposalTask(t, spine, "doctor must not touch me")
		db := openDB(t, spine)
		if err := os.WriteFile(filepath.Join(filepath.Dir(spine), "routing.md"),
			[]byte("| role | harness |\n|---|---|\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		res := runMC(t, spineEnv(spine), "", "doctor")
		if res.code != 0 || res.json["ok"] != false {
			t.Fatalf("doctor = code %d json %v", res.code, res.json)
		}
		findings := findingByCheck(t, res)
		if findings["routing"]["status"] != "fail" || findings["routing"]["onboard_section"] != "routing" {
			t.Fatalf("routing finding = %v", findings["routing"])
		}
		if findings["spine"]["status"] != "ok" {
			t.Fatalf("spine finding = %v", findings["spine"])
		}
		if n := queryInt(t, db, `SELECT COUNT(*) FROM tasks`); n != 1 {
			t.Fatalf("doctor mutated tasks: %d rows", n)
		}
	})

	t.Run("identity_mismatch_and_unrouted_alerts_are_failures", func(t *testing.T) {
		spine := initSpine(t)
		uuid := prepareIdentityAndSurfaces(t, spine)
		mirror := filepath.Join(filepath.Dir(spine), "deployment.uuid")
		if err := os.WriteFile(mirror, []byte("wrong\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		mismatch := runMC(t, spineEnv(spine), "", "doctor")
		findings := findingByCheck(t, mismatch)
		if mismatch.code != 0 || mismatch.json["ok"] != false || findings["deployment-identity"]["status"] != "fail" {
			t.Fatalf("identity mismatch doctor = code %d json %v", mismatch.code, mismatch.json)
		}
		if err := os.WriteFile(mirror, []byte(uuid+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		db := openDB(t, spine)
		if _, err := db.Exec(`DELETE FROM alert_routes WHERE class = 'health'`); err != nil {
			db.Close()
			t.Fatal(err)
		}
		db.Close()
		unrouted := runMC(t, spineEnv(spine), "", "doctor")
		findings = findingByCheck(t, unrouted)
		if unrouted.code != 0 || unrouted.json["ok"] != false || findings["surfaces"]["status"] != "fail" || findings["surfaces"]["onboard_section"] != "surfaces" {
			t.Fatalf("unrouted alerts doctor = code %d json %v", unrouted.code, unrouted.json)
		}
	})

	t.Run("missing_spine_reports_loss_without_creating_bytes", func(t *testing.T) {
		home := t.TempDir()
		absent := filepath.Join(home, "spine.db")
		if err := os.WriteFile(filepath.Join(home, "routing.md"), []byte(fakeRoutingMarkdown), 0o600); err != nil {
			t.Fatal(err)
		}
		res := runMC(t, []string{"MC_SPINE=" + absent, "MC_HOME=" + home}, "", "doctor")
		if res.code != 0 || res.json["ok"] != false {
			t.Fatalf("doctor = code %d json %v stderr %q", res.code, res.json, res.stderr)
		}
		findings := findingByCheck(t, res)
		if findings["spine"]["status"] != "fail" ||
			!strings.Contains(findings["spine"]["detail"].(string), "redeploy.sh") {
			t.Fatalf("spine loss finding = %v", findings["spine"])
		}
		if _, err := os.Stat(absent); !os.IsNotExist(err) {
			t.Fatalf("doctor created spine bytes at %q", absent)
		}
	})

	t.Run("doctor_is_host_scope", func(t *testing.T) {
		spine := initSpine(t)
		for name, env := range map[string][]string{
			"pipeline": runJSONEnv(t, spine, "r-x", "pipeline", "worker"),
			"collie":   collieJSONEnv(t, spine, "h-x", defaultCollieAllowlist),
		} {
			res := runMC(t, env, "", "doctor")
			if res.code != 1 {
				t.Fatalf("%s doctor exit = %d stderr %q", name, res.code, res.stderr)
			}
		}
	})
}

func TestReset(t *testing.T) {
	t.Run("refuses_without_confirmation_and_takes_no_snapshot", func(t *testing.T) {
		spine := initSpine(t)
		proposalTask(t, spine, "still here")
		res := runMC(t, spineEnv(spine), "", "__reset")
		if res.code != 1 || !strings.Contains(res.stderr, "--confirm") {
			t.Fatalf("unconfirmed reset = code %d stderr %q", res.code, res.stderr)
		}
		if _, err := os.Stat(spine); err != nil {
			t.Fatalf("unconfirmed reset touched the spine: %v", err)
		}
		if n := len(backupFiles(t, spine)); n != 0 {
			t.Fatalf("unconfirmed reset wrote %d snapshots", n)
		}
	})

	t.Run("confirmed_reset_snapshots_first_then_deletes_the_spine", func(t *testing.T) {
		spine := initSpine(t)
		proposalTask(t, spine, "must survive in the snapshot")
		res := runMC(t, spineEnv(spine), "", "__reset", "--confirm")
		if res.code != 0 || res.json["reset"] != true {
			t.Fatalf("reset = code %d json %v stderr %q", res.code, res.json, res.stderr)
		}
		// Output carries only paths and the flag — never config/secret values.
		for key := range res.json {
			switch key {
			case "spine", "snapshot", "reset":
			default:
				t.Fatalf("unexpected reset output key %q in %v", key, res.json)
			}
		}
		snapshot := res.json["snapshot"].(string)
		snapDB, err := substrate.Open(snapshot)
		if err != nil {
			t.Fatalf("open pre-reset snapshot: %v", err)
		}
		defer snapDB.Close()
		if n := queryInt(t, snapDB, `SELECT COUNT(*) FROM tasks`); n != 1 {
			t.Fatalf("snapshot task rows = %d, want 1", n)
		}
		for _, residue := range []string{spine, spine + "-wal", spine + "-shm"} {
			if _, err := os.Stat(residue); !os.IsNotExist(err) {
				t.Fatalf("reset left %q behind (err %v)", residue, err)
			}
		}
	})

	t.Run("failed_snapshot_aborts_the_reset", func(t *testing.T) {
		spine := initSpine(t)
		// A file where the backups directory must go makes the snapshot fail.
		if err := os.WriteFile(filepath.Join(filepath.Dir(spine), "backups"), []byte("in the way"), 0o600); err != nil {
			t.Fatal(err)
		}
		res := runMC(t, spineEnv(spine), "", "__reset", "--confirm")
		if res.code != 1 {
			t.Fatalf("snapshot-blocked reset = code %d stderr %q", res.code, res.stderr)
		}
		if _, err := os.Stat(spine); err != nil {
			t.Fatalf("aborted reset deleted the spine: %v", err)
		}
	})

	t.Run("reset_is_host_scope", func(t *testing.T) {
		spine := initSpine(t)
		for name, env := range map[string][]string{
			"pipeline": runJSONEnv(t, spine, "r-x", "pipeline", "worker"),
			"collie":   collieJSONEnv(t, spine, "h-x", defaultCollieAllowlist),
		} {
			res := runMC(t, env, "", "__reset", "--confirm")
			if res.code != 1 {
				t.Fatalf("%s reset exit = %d stderr %q", name, res.code, res.stderr)
			}
		}
		if _, err := os.Stat(spine); err != nil {
			t.Fatalf("denied reset touched the spine: %v", err)
		}
	})
}

// mc onboard: the section dispatcher — named sections only,
// resumable/idempotent, the spine identity rules, deferred doubles for the
// Docker/host-effect sections, no launchd ever.
func TestOnboard(t *testing.T) {
	freshHome := func(t *testing.T) (string, string, []string) {
		t.Helper()
		home := filepath.Join(t.TempDir(), "mc-home")
		spine := filepath.Join(home, "spine.db")
		return home, spine, []string{"MC_HOME=" + home, "MC_SPINE=" + spine}
	}
	sectionStatus := func(t *testing.T, res mcResult) map[string]map[string]any {
		t.Helper()
		out := map[string]map[string]any{}
		sections, ok := res.json["sections"].([]any)
		if !ok {
			t.Fatalf("onboard result = %v", res.json)
		}
		for _, raw := range sections {
			s := raw.(map[string]any)
			out[s["section"].(string)] = s
		}
		return out
	}

	t.Run("named_sections_only_and_host_scope", func(t *testing.T) {
		home, _, env := freshHome(t)
		res := runMC(t, env, "", "onboard", "bogus-section")
		if res.code != 2 || !strings.Contains(res.stderr, "home|runtime-auth|routing|project|tunables|surfaces") {
			t.Fatalf("unknown section = code %d stderr %q", res.code, res.stderr)
		}
		if strings.Contains(res.stderr, "preflight") || strings.Contains(res.stderr, "supervision|verify") {
			t.Fatalf("public onboard usage lists release sections: %q", res.stderr)
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatalf("refused onboard created MC_HOME (err %v)", err)
		}
		for name, identityEnv := range map[string]func(string) []string{
			"pipeline": func(spine string) []string {
				return runJSONEnv(t, spine, "r-x", "pipeline", "worker")
			},
			"collie": func(spine string) []string {
				return collieJSONEnv(t, spine, "h-x", defaultCollieAllowlist)
			},
		} {
			deniedHome, deniedSpine, _ := freshHome(t)
			envDenied := identityEnv(deniedSpine)
			envDenied = append(envDenied, "MC_HOME="+deniedHome)
			res := runMC(t, envDenied, "", "onboard", "home")
			if res.code != 1 {
				t.Fatalf("%s onboard exit = %d stderr %q", name, res.code, res.stderr)
			}
			if _, err := os.Stat(deniedHome); !os.IsNotExist(err) {
				t.Fatalf("%s denial created MC_HOME: %v", name, err)
			}
			if _, err := os.Stat(deniedSpine); !os.IsNotExist(err) {
				t.Fatalf("%s denial created the spine: %v", name, err)
			}
		}
		publicSmoke := runMC(t, env, "", "onboard", "--smoke")
		if publicSmoke.code != 2 || !strings.Contains(publicSmoke.stderr, "install.sh") {
			t.Fatalf("public smoke = code %d stderr %q, want an install.sh pointer", publicSmoke.code, publicSmoke.stderr)
		}
		smoke := runMC(t, env, "", "__onboard", "--smoke")
		if smoke.code != 1 || !strings.Contains(smoke.stderr, "--smoke") {
			t.Fatalf("smoke = code %d stderr %q", smoke.code, smoke.stderr)
		}
		bogusSmoke := runMC(t, env, "", "__onboard", "bogus-section", "--smoke")
		if bogusSmoke.code != 2 || !strings.Contains(bogusSmoke.stderr, "unknown onboarding section") {
			t.Fatalf("bogus smoke section = code %d stderr %q", bogusSmoke.code, bogusSmoke.stderr)
		}
	})

	t.Run("home_provisions_the_deployment_idempotently", func(t *testing.T) {
		home, spine, env := freshHome(t)
		res := runMC(t, env, "", "onboard", "home")
		if res.code != 0 {
			t.Fatalf("onboard home failed (%d): %s", res.code, res.stderr)
		}
		if sectionStatus(t, res)["home"]["status"] != "done" {
			t.Fatalf("fresh home = %v", res.json)
		}
		for _, dir := range []string{"backups", "sessions", "outputs", "attachments"} {
			if st, err := os.Stat(filepath.Join(home, dir)); err != nil || !st.IsDir() {
				t.Fatalf("home scaffold missing %s/: %v", dir, err)
			}
		}
		// `workflows/` is deliberately NOT scaffolded: nothing writes it, and
		// an empty directory advertises a plane nothing uses. Asserted absent
		// rather than dropped from the list, so re-adding it has to be a
		// decision.
		if _, err := os.Stat(filepath.Join(home, "workflows")); !os.IsNotExist(err) {
			t.Fatalf("home scaffolded workflows/, a plane with no writer: %v", err)
		}
		db, err := substrate.Open(spine)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		uuid := queryStr(t, db, `SELECT deployment_uuid FROM meta WHERE id = 1`)
		if uuid == "" {
			t.Fatal("home did not seed the meta identity")
		}
		mirror := filepath.Join(home, "deployment.uuid")
		mirrored, err := os.ReadFile(mirror)
		if err != nil || strings.TrimSpace(string(mirrored)) != uuid {
			t.Fatalf("deployment UUID mirror = %q, err %v; want %q", mirrored, err, uuid)
		}
		// Re-running skips; the deployment identity never changes.
		again := runMC(t, env, "", "onboard", "home")
		if again.code != 0 || sectionStatus(t, again)["home"]["status"] != "ok" {
			t.Fatalf("re-run home = code %d json %v", again.code, again.json)
		}
		if got := queryStr(t, db, `SELECT deployment_uuid FROM meta WHERE id = 1`); got != uuid {
			t.Fatalf("re-run changed deployment identity %q -> %q", uuid, got)
		}
		after, err := os.ReadFile(mirror)
		if err != nil || string(after) != string(mirrored) {
			t.Fatalf("re-run changed UUID mirror from %q to %q (err %v)", mirrored, after, err)
		}
		if err := os.WriteFile(mirror, []byte(strings.Repeat("f", 32)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		mismatch := runMC(t, env, "", "onboard", "home")
		if mismatch.code != 1 || !strings.Contains(mismatch.stderr, "identity mismatch") {
			t.Fatalf("UUID mismatch = code %d stderr %q", mismatch.code, mismatch.stderr)
		}
	})

	t.Run("home_adoption_and_scaffold_repairs_report_done", func(t *testing.T) {
		spine := initSpine(t)
		home := filepath.Dir(spine)
		env := spineEnv(spine)
		adopt := runMC(t, env, "", "onboard", "home")
		if adopt.code != 0 || sectionStatus(t, adopt)["home"]["status"] != "done" {
			t.Fatalf("pre-mirror adoption = code %d json %v stderr %q", adopt.code, adopt.json, adopt.stderr)
		}
		if err := os.RemoveAll(filepath.Join(home, "sessions")); err != nil {
			t.Fatal(err)
		}
		repair := runMC(t, env, "", "onboard", "home")
		if repair.code != 0 || sectionStatus(t, repair)["home"]["status"] != "done" {
			t.Fatalf("scaffold repair = code %d json %v stderr %q", repair.code, repair.json, repair.stderr)
		}
	})

	t.Run("home_preserves_an_unrecognized_database", func(t *testing.T) {
		root := t.TempDir()
		home := filepath.Join(root, "mc-home")
		spine := filepath.Join(root, "foreign.db")
		env := []string{"MC_HOME=" + home, "MC_SPINE=" + spine}
		raw, err := sql.Open("sqlite", "file:"+spine)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(`CREATE TABLE stranger (x); INSERT INTO stranger VALUES (42)`); err != nil {
			t.Fatal(err)
		}
		raw.Close()
		before, err := os.ReadFile(spine)
		if err != nil {
			t.Fatal(err)
		}
		res := runMC(t, env, "", "onboard", "home")
		if res.code != 1 || !strings.Contains(res.stderr, "no meta identity") {
			t.Fatalf("nonempty-spine home = code %d stderr %q", res.code, res.stderr)
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatalf("meta refusal scaffolded MC_HOME: %v", err)
		}
		after, err := os.ReadFile(spine)
		if err != nil || !bytes.Equal(after, before) {
			t.Fatalf("meta inspection mutated foreign spine (err %v)", err)
		}
		check, err := sql.Open("sqlite", "file:"+spine+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		defer check.Close()
		if n := queryInt(t, check, `SELECT COUNT(*) FROM stranger`); n != 1 {
			t.Fatalf("onboard touched the foreign spine: %d rows", n)
		}
		if mode := queryStr(t, check, `PRAGMA journal_mode`); mode != "delete" {
			t.Fatalf("read-only inspection changed journal mode to %q", mode)
		}
	})

	t.Run("home_detects_a_lost_spine_from_its_uuid_mirror", func(t *testing.T) {
		_, spine, env := freshHome(t)
		if res := runMC(t, env, "", "onboard", "home"); res.code != 0 {
			t.Fatalf("home failed: %s", res.stderr)
		}
		for _, path := range []string{spine, spine + "-wal", spine + "-shm"} {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
		lost := runMC(t, env, "", "onboard", "home")
		if lost.code != 1 || !strings.Contains(lost.stderr, "spine lost") || !strings.Contains(lost.stderr, "redeploy.sh") {
			t.Fatalf("lost spine = code %d stderr %q", lost.code, lost.stderr)
		}
		if _, err := os.Stat(spine); !os.IsNotExist(err) {
			t.Fatalf("loss path recreated the spine: %v", err)
		}
	})

	t.Run("home_initializes_a_zero_table_sqlite_file", func(t *testing.T) {
		root := t.TempDir()
		home := filepath.Join(root, "mc-home")
		spine := filepath.Join(root, "zero-table.db")
		db, err := sql.Open("sqlite", "file:"+spine)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`PRAGMA user_version = 7`); err != nil {
			db.Close()
			t.Fatal(err)
		}
		db.Close()
		before, err := os.ReadFile(spine)
		if err != nil || len(before) == 0 {
			t.Fatalf("zero-table fixture = %d bytes, err %v", len(before), err)
		}
		res := runMC(t, []string{"MC_HOME=" + home, "MC_SPINE=" + spine}, "", "onboard", "home")
		if res.code != 0 {
			t.Fatalf("zero-table database = code %d stderr %q", res.code, res.stderr)
		}
		check := openDB(t, spine)
		defer check.Close()
		if got := queryStr(t, check, `SELECT schema_id FROM meta WHERE id = 1`); got != substrate.CurrentSchemaID {
			t.Fatalf("schema identifier = %q", got)
		}
		if _, err := os.Stat(filepath.Join(home, "deployment.uuid")); err != nil {
			t.Fatalf("empty database lacks a deployment identity: %v", err)
		}
	})

	t.Run("preflight_fences_git_working_trees", func(t *testing.T) {
		repo := filepath.Join(t.TempDir(), "repo")
		if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
		home := filepath.Join(repo, "deep", "mc-home")
		res := runMC(t, []string{"MC_HOME=" + home, "MC_SPINE=" + filepath.Join(home, "s.db")}, "", "__onboard", "preflight")
		if res.code != 1 || !strings.Contains(res.stderr, "git working tree") {
			t.Fatalf("git-tree preflight = code %d stderr %q", res.code, res.stderr)
		}
		directHome := runMC(t, []string{"MC_HOME=" + home, "MC_SPINE=" + filepath.Join(home, "s.db")}, "", "onboard", "home")
		if directHome.code != 1 || !strings.Contains(directHome.stderr, "git working tree") {
			t.Fatalf("named home bypassed git fence = code %d stderr %q", directHome.code, directHome.stderr)
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatalf("denied named home wrote MC_HOME: %v", err)
		}
		ignoredRepo := filepath.Join(t.TempDir(), "ignored-repo")
		if out, err := exec.Command("git", "init", "-q", ignoredRepo).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, out)
		}
		if err := os.WriteFile(filepath.Join(ignoredRepo, ".gitignore"), []byte(".mc/\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		ignoredHome := filepath.Join(ignoredRepo, ".mc")
		ignored := runMC(t, []string{"MC_HOME=" + ignoredHome, "MC_SPINE=" + filepath.Join(ignoredHome, "s.db")}, "", "__onboard", "preflight")
		if ignored.code != 0 || sectionStatus(t, ignored)["preflight"]["status"] != "ok" {
			t.Fatalf("ignored in-tree home = code %d stderr %q json %v", ignored.code, ignored.stderr, ignored.json)
		}
		if _, err := os.Stat(ignoredHome); !os.IsNotExist(err) {
			t.Fatalf("preflight wrote ignored MC_HOME: %v", err)
		}

		targetRepo := filepath.Join(t.TempDir(), "target-repo")
		if out, err := exec.Command("git", "init", "-q", targetRepo).CombinedOutput(); err != nil {
			t.Fatalf("git init target: %v: %s", err, out)
		}
		aliasRoot := filepath.Join(t.TempDir(), "repo-alias")
		if err := os.Symlink(targetRepo, aliasRoot); err != nil {
			t.Fatal(err)
		}
		aliasedHome := filepath.Join(aliasRoot, "operator-state")
		aliased := runMC(t, []string{"MC_HOME=" + aliasedHome, "MC_SPINE=" + filepath.Join(aliasedHome, "s.db")}, "", "__onboard", "preflight")
		if aliased.code != 1 || !strings.Contains(aliased.stderr, "git working tree") {
			t.Fatalf("symlinked git-tree home = code %d stderr %q", aliased.code, aliased.stderr)
		}
		outside, _, env := freshHome(t)
		ok := runMC(t, env, "", "__onboard", "preflight")
		if ok.code != 0 || sectionStatus(t, ok)["preflight"]["status"] != "ok" {
			t.Fatalf("clean preflight = code %d json %v stderr %q", ok.code, ok.json, ok.stderr)
		}
		_ = outside
	})

	t.Run("routing_writes_a_valid_default_once_and_validates_existing", func(t *testing.T) {
		home, _, env := freshHome(t)
		tooEarly := runMC(t, env, "", "onboard", "routing",
			"--runtime-bindings", "chatgpt,claude", "--accept-default-routing")
		if tooEarly.code != 1 || !strings.Contains(tooEarly.stderr, "onboard home") {
			t.Fatalf("routing before home = code %d stderr %q", tooEarly.code, tooEarly.stderr)
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatalf("routing before identity created MC_HOME: %v", err)
		}
		if homeRes := runMC(t, env, "", "onboard", "home"); homeRes.code != 0 {
			t.Fatalf("home failed: %s", homeRes.stderr)
		}
		if _, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), func(cfg *operatorconfig.Config) error {
			profile := cfg.Runtimes.Bindings["chatgpt"]
			profile.Model = "fixture-model"
			cfg.Runtimes.Bindings["chatgpt"] = profile
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(home, "routing.md")
		outside := filepath.Join(t.TempDir(), "outside-routing.md")
		if err := os.Symlink(outside, path); err != nil {
			t.Fatal(err)
		}
		redirected := runMC(t, env, "", "onboard", "routing",
			"--runtime-bindings", "chatgpt,claude", "--accept-default-routing")
		if redirected.code != 1 || !strings.Contains(redirected.stderr, "symlink") {
			t.Fatalf("routing symlink = code %d stderr %q", redirected.code, redirected.stderr)
		}
		if _, err := os.Stat(outside); !os.IsNotExist(err) {
			t.Fatalf("routing symlink wrote outside MC_HOME: %v", err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		res := runMC(t, env, "", "onboard", "routing",
			"--runtime-bindings", "chatgpt,claude", "--accept-default-routing")
		if res.code != 0 || sectionStatus(t, res)["routing"]["status"] != "done" {
			t.Fatalf("fresh routing = code %d json %v stderr %q", res.code, res.json, res.stderr)
		}
		written, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		again := runMC(t, env, "", "onboard", "routing",
			"--runtime-bindings", "chatgpt,claude", "--accept-default-routing")
		if again.code != 0 || sectionStatus(t, again)["routing"]["status"] != "ok" {
			t.Fatalf("re-run routing = code %d json %v", again.code, again.json)
		}
		unchanged, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(unchanged) != string(written) {
			t.Fatal("re-run rewrote routing.md")
		}
		// An existing invalid table fails the section and is never repaired
		// silently: an invalid routing.md can never come out of onboarding.
		if err := os.WriteFile(path, []byte("| role | harness |\n|---|---|\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		invalid := runMC(t, env, "", "onboard", "routing",
			"--runtime-bindings", "chatgpt,claude", "--accept-default-routing")
		if invalid.code != 1 || !strings.Contains(invalid.stderr, "routing") {
			t.Fatalf("invalid routing = code %d stderr %q", invalid.code, invalid.stderr)
		}
		kept, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(kept) != "| role | harness |\n|---|---|\n" {
			t.Fatal("failed section rewrote the operator's routing.md")
		}
		candidate := filepath.Join(t.TempDir(), "routing.md")
		if err := os.WriteFile(candidate, written, 0o600); err != nil {
			t.Fatal(err)
		}
		repaired := runMC(t, env, "", "onboard", "routing",
			"--runtime-bindings", "chatgpt,claude", "--routing-file", candidate)
		if repaired.code != 0 || sectionStatus(t, repaired)["routing"]["status"] != "done" {
			t.Fatalf("explicit routing repair = code %d json %v stderr %q", repaired.code, repaired.json, repaired.stderr)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(written) {
			t.Fatal("explicit routing repair did not publish the requested table")
		}
	})

	t.Run("project_is_dual_input_and_skips_once_seeded", func(t *testing.T) {
		_, spine, env := freshHome(t)
		if res := runMC(t, env, "", "onboard", "home"); res.code != 0 {
			t.Fatalf("home failed: %s", res.stderr)
		}
		missing := runMC(t, env, "", "onboard", "project")
		if missing.code != 0 || !strings.Contains(missing.stdout, "Add a Project in the dashboard") {
			t.Fatalf("zero Project setup = code %d stdout %q stderr %q", missing.code, missing.stdout, missing.stderr)
		}
		relative := runMC(t, env, "", "onboard", "project",
			"--project", "ws-main", "--workspace-root", "relative/path")
		if relative.code != 2 {
			t.Fatalf("relative workspace = code %d stderr %q", relative.code, relative.stderr)
		}
		workspace := filepath.Join(t.TempDir(), "ws-main")
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			t.Fatal(err)
		}
		writeVisionFile(t, workspace)
		canonicalWorkspace, err := filepath.EvalSymlinks(workspace)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name string
			args []string
		}{
			{name: "one-sided", args: []string{
				"--project-git-author-name", "Project Bot",
			}},
			{name: "blank-pair", args: []string{
				"--project-git-author-name", "",
				"--project-git-author-email", "",
			}},
			{name: "whitespace", args: []string{
				"--project-git-author-name", " \t ",
				"--project-git-author-email", "bot@example.test",
			}},
			{name: "control", args: []string{
				"--project-git-author-name", "Project Bot\nInjected",
				"--project-git-author-email", "bot@example.test",
			}},
			{name: "malformed-email", args: []string{
				"--project-git-author-name", "Project Bot",
				"--project-git-author-email", "not-an-email",
			}},
			{name: "over-limit", args: []string{
				"--project-git-author-name", strings.Repeat("x", 257),
				"--project-git-author-email", "bot@example.test",
			}},
		} {
			unsafe := runMC(t, env, "", append([]string{
				"onboard", "project",
				"--project", "ws-unsafe", "--workspace-root", workspace,
				"--accept-generated-sandbox",
			}, tc.args...)...)
			if unsafe.code != 2 {
				t.Fatalf("%s Project Git identity = code %d stderr %q", tc.name, unsafe.code, unsafe.stderr)
			}
		}
		seeded := runMC(t, env, "", "onboard", "project",
			"--project", "ws-main", "--workspace-root", workspace,
			"--project-git-author-name", "Project Bot",
			"--project-git-author-email", "bot@example.test",
			"--accept-generated-sandbox")
		if seeded.code != 0 || sectionStatus(t, seeded)["project"]["status"] != "done" {
			t.Fatalf("seeding project = code %d json %v stderr %q", seeded.code, seeded.json, seeded.stderr)
		}
		db, err := substrate.Open(spine)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if got := queryStr(t, db, `SELECT sandbox_profile FROM projects WHERE id = 'ws-main'`); got != "project-ws-main" {
			t.Fatalf("project row = %q", got)
		}
		if got := queryStr(t, db, `SELECT workspace_root FROM sandbox_profiles WHERE id = 'project-ws-main'`); got != canonicalWorkspace {
			t.Fatalf("canonical workspace root = %q, want %q", got, canonicalWorkspace)
		}
		if got := queryStr(t, db, `SELECT git_author_name || '<' || git_author_email || '>'
			FROM sandbox_profiles WHERE id = 'project-ws-main'`); got != "Project Bot<bot@example.test>" {
			t.Fatalf("Project Git identity override = %q", got)
		}
		if got := queryInt(t, db, `SELECT COUNT(*) FROM sandbox_profiles
			WHERE id = 'project-ws-unsafe'`); got != 0 {
			t.Fatalf("unsafe Git identity persisted %d sandbox profiles", got)
		}
		exact := runMC(t, env, "", "onboard", "project",
			"--project", "ws-main", "--workspace-root", workspace,
			"--project-git-author-name", "Project Bot",
			"--project-git-author-email", "bot@example.test",
			"--accept-generated-sandbox")
		if exact.code != 0 || sectionStatus(t, exact)["project"]["status"] != "ok" {
			t.Fatalf("exact Project replay = code %d json %v stderr %q", exact.code, exact.json, exact.stderr)
		}
		changedIdentity := runMC(t, env, "", "onboard", "project",
			"--project", "ws-main", "--workspace-root", workspace,
			"--project-git-author-name", "Replacement Bot",
			"--project-git-author-email", "replacement@example.test",
			"--accept-generated-sandbox")
		if changedIdentity.code != 1 || !strings.Contains(changedIdentity.stderr, "different Git/sandbox posture") {
			t.Fatalf("changed Project identity replay = code %d stderr %q",
				changedIdentity.code, changedIdentity.stderr)
		}
		if got := queryStr(t, db, `SELECT git_author_name || '<' || git_author_email || '>'
			FROM sandbox_profiles WHERE id = 'project-ws-main'`); got != "Project Bot<bot@example.test>" {
			t.Fatalf("changed replay rewrote Project Git identity to %q", got)
		}
		otherWorkspace := filepath.Join(t.TempDir(), "other")
		if err := os.MkdirAll(otherWorkspace, 0o700); err != nil {
			t.Fatal(err)
		}
		writeVisionFile(t, otherWorkspace)
		moved := runMC(t, env, "", "onboard", "project",
			"--project", "ws-main", "--workspace-root", otherWorkspace,
			"--accept-generated-sandbox")
		if moved.code != 1 || !strings.Contains(moved.stderr, "already") {
			t.Fatalf("moved Project replay = code %d stderr %q", moved.code, moved.stderr)
		}
		// The section registers a second Project the same way.
		second := runMC(t, env, "", "onboard", "project",
			"--project", "ws-other", "--workspace-root", otherWorkspace,
			"--accept-generated-sandbox")
		if second.code != 0 || sectionStatus(t, second)["project"]["status"] != "done" {
			t.Fatalf("second Project registration = code %d json %v stderr %q", second.code, second.json, second.stderr)
		}
		if got := queryInt(t, db, `SELECT COUNT(*) FROM projects w JOIN sandbox_profiles p ON p.id = w.sandbox_profile WHERE w.id = 'ws-other'`); got != 1 {
			t.Fatalf("second Project rows with a profile = %d, want 1", got)
		}
		skip := runMC(t, env, "", "onboard", "project")
		if skip.code != 0 || sectionStatus(t, skip)["project"]["status"] != "ok" {
			t.Fatalf("seeded re-run = code %d json %v", skip.code, skip.json)
		}
		if err := os.RemoveAll(workspace); err != nil {
			t.Fatal(err)
		}
		missingRoot := runMC(t, env, "", "onboard", "project")
		if missingRoot.code != 1 || !strings.Contains(missingRoot.stderr, "workspace root") {
			t.Fatalf("missing stored workspace = code %d stderr %q", missingRoot.code, missingRoot.stderr)
		}
	})

	t.Run("project_refuses_a_conflicting_generated_profile", func(t *testing.T) {
		_, spine, env := freshHome(t)
		if res := runMC(t, env, "", "onboard", "home"); res.code != 0 {
			t.Fatalf("home failed: %s", res.stderr)
		}
		db, err := substrate.Open(spine)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO sandbox_profiles (id, workspace_root)
			VALUES ('project-ws-main', '/different')`); err != nil {
			db.Close()
			t.Fatal(err)
		}
		db.Close()
		workspace := filepath.Join(t.TempDir(), "requested")
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			t.Fatal(err)
		}
		writeVisionFile(t, workspace)
		res := runMC(t, env, "", "onboard", "project",
			"--project", "ws-main", "--workspace-root", workspace,
			"--accept-generated-sandbox")
		if res.code != 1 || !strings.Contains(res.stderr, "project-ws-main") {
			t.Fatalf("conflicting profile = code %d stderr %q", res.code, res.stderr)
		}
		check, err := substrate.Open(spine)
		if err != nil {
			t.Fatal(err)
		}
		defer check.Close()
		if got := queryInt(t, check, `SELECT COUNT(*) FROM projects`); got != 0 {
			t.Fatalf("conflicting profile left %d Projects", got)
		}
	})

	t.Run("project_refuses_a_rebound_generated_profile", func(t *testing.T) {
		_, spine, env := freshHome(t)
		if res := runMC(t, env, "", "onboard", "home"); res.code != 0 {
			t.Fatalf("home failed: %s", res.stderr)
		}
		workspace := filepath.Join(t.TempDir(), "requested")
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			t.Fatal(err)
		}
		writeVisionFile(t, workspace)
		db, err := substrate.Open(spine)
		if err != nil {
			t.Fatal(err)
		}
		// A pre-registered generated profile bound elsewhere must refuse the
		// requested workspace rather than silently rebinding it.
		if _, err := db.Exec(`INSERT INTO sandbox_profiles (id, workspace_root)
			VALUES ('project-ws-main', '/srv/elsewhere')`); err != nil {
			db.Close()
			t.Fatal(err)
		}
		db.Close()
		res := runMC(t, env, "", "onboard", "project",
			"--project", "ws-main", "--workspace-root", workspace,
			"--accept-generated-sandbox")
		if res.code != 1 || !strings.Contains(res.stderr, "conflicts") {
			t.Fatalf("rebound profile = code %d stderr %q", res.code, res.stderr)
		}
	})

	t.Run("project_vision_answer_is_a_closed_vocabulary", func(t *testing.T) {
		_, _, env := freshHome(t)
		workspace := filepath.Join(t.TempDir(), "ws-main")
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			t.Fatal(err)
		}
		res := runMC(t, env, "", "onboard", "project",
			"--project", "ws-main", "--workspace-root", workspace,
			"--accept-generated-sandbox", "--project-vision", "invalid")
		if res.code != 2 || !strings.Contains(res.stderr, "--project-vision must be create|later") {
			t.Fatalf("unknown vision answer = code %d stderr %q", res.code, res.stderr)
		}
	})
	t.Run("full_run_is_ordered_resumable_and_verified", func(t *testing.T) {
		home, spine, env := freshHome(t)
		if err := os.MkdirAll(home, 0700); err != nil {
			t.Fatal(err)
		}
		cfg := operatorconfig.Default()
		profile := cfg.Runtimes.Bindings["chatgpt"]
		profile.Model = "fixture-model"
		cfg.Runtimes.Bindings["chatgpt"] = profile
		if _, err := operatorconfig.WriteAtomic(filepath.Join(home, "config.toml"), cfg); err != nil {
			t.Fatal(err)
		}
		workspace := filepath.Join(t.TempDir(), "ws-full")
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			t.Fatal(err)
		}
		writeVisionFile(t, workspace)
		res := runMC(t, env, "", "__onboard",
			"--runtime-bindings", "chatgpt,claude",
			"--accept-default-routing", "--accept-default-tunables",
			"--project", "ws-full", "--workspace-root", workspace,
			"--accept-generated-sandbox")
		if res.code != 0 || res.json["ok"] != true {
			t.Fatalf("full onboard = code %d json %v stderr %q", res.code, res.json, res.stderr)
		}
		sections := res.json["sections"].([]any)
		order := []string{}
		for _, raw := range sections {
			order = append(order, raw.(map[string]any)["section"].(string))
		}
		if strings.Join(order, "|") != "preflight|home|runtime-auth|routing|container|project|tunables|surfaces|supervision|verify" {
			t.Fatalf("section order = %v", order)
		}
		byName := sectionStatus(t, res)
		for section, want := range map[string]string{
			"preflight": "ok", "home": "done", "runtime-auth": "deferred",
			"routing": "done", "container": "deferred", "project": "done",
			"tunables": "ok", "surfaces": "ok", "supervision": "deferred",
			"verify": "ok",
		} {
			if byName[section]["status"] != want {
				t.Fatalf("section %q = %v, want %q", section, byName[section], want)
			}
		}
		db, err := substrate.Open(spine)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		uuid := queryStr(t, db, `SELECT deployment_uuid FROM meta WHERE id = 1`)

		// Resumable: the identical re-run performs nothing and changes nothing.
		again := runMC(t, env, "", "__onboard",
			"--runtime-bindings", "chatgpt,claude",
			"--accept-default-routing", "--accept-default-tunables",
			"--project", "ws-full", "--workspace-root", workspace,
			"--accept-generated-sandbox")
		if again.code != 0 || again.json["ok"] != true {
			t.Fatalf("onboard re-run = code %d stderr %q", again.code, again.stderr)
		}
		reByName := sectionStatus(t, again)
		for _, section := range []string{"home", "routing", "project", "surfaces"} {
			if reByName[section]["status"] != "ok" {
				t.Fatalf("re-run section %q = %v, want ok (skip)", section, reByName[section])
			}
		}
		if got := queryStr(t, db, `SELECT deployment_uuid FROM meta WHERE id = 1`); got != uuid {
			t.Fatalf("re-run changed deployment identity %q -> %q", uuid, got)
		}
		// Deferred checks and unverified connections keep the result incomplete.
		doctor := runMC(t, env, "", "doctor")
		if doctor.code != 0 || doctor.json["ok"] != false || doctor.json["deployment_complete"] != false {
			t.Fatalf("post-onboard doctor = code %d json %v", doctor.code, doctor.json)
		}
	})

	t.Run("supervision_double_never_invokes_launchctl", func(t *testing.T) {
		_, _, env := freshHome(t)
		if res := runMC(t, env, "", "onboard", "home"); res.code != 0 {
			t.Fatalf("home failed: %s", res.stderr)
		}
		binDir := t.TempDir()
		marker := filepath.Join(t.TempDir(), "launchctl-ran")
		fake := filepath.Join(binDir, "launchctl")
		if err := os.WriteFile(fake, []byte("#!/bin/sh\n: > \""+marker+"\"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		env = append(env, "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		res := runMC(t, env, "", "__onboard", "supervision")
		if res.code != 0 || sectionStatus(t, res)["supervision"]["status"] != "deferred" {
			t.Fatalf("supervision double = code %d json %v stderr %q", res.code, res.json, res.stderr)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("supervision double invoked launchctl: %v", err)
		}
	})

	t.Run("tunables_and_surfaces_apply_operator_answers", func(t *testing.T) {
		home, spine, env := freshHome(t)
		if res := runMC(t, env, "", "onboard", "home"); res.code != 0 {
			t.Fatalf("home failed: %s", res.stderr)
		}
		negative := runMC(t, env, "", "onboard", "tunables", "--timeout-minutes", "-1")
		if negative.code != 2 {
			t.Fatalf("negative tunable = code %d stderr %q", negative.code, negative.stderr)
		}
		tun := runMC(t, env, "", "onboard", "tunables", "--timeout-minutes", "7")
		if tun.code != 0 || sectionStatus(t, tun)["tunables"]["status"] != "done" {
			t.Fatalf("tunables = code %d json %v stderr %q", tun.code, tun.json, tun.stderr)
		}
		tunAgain := runMC(t, env, "", "onboard", "tunables", "--timeout-minutes", "7")
		if tunAgain.code != 0 || sectionStatus(t, tunAgain)["tunables"]["status"] != "ok" {
			t.Fatalf("tunable replay = code %d json %v stderr %q", tunAgain.code, tunAgain.json, tunAgain.stderr)
		}
		inspectTunables := runMC(t, env, "", "onboard", "tunables")
		tunableDetail := sectionStatus(t, inspectTunables)["tunables"]["detail"].(string)
		if inspectTunables.code != 0 || !strings.Contains(tunableDetail, "already agree") {
			t.Fatalf("tunable inspection = code %d detail %q", inspectTunables.code, tunableDetail)
		}
		verified := runMC(t, env, "", "onboard", "surfaces")
		if verified.code != 0 || sectionStatus(t, verified)["surfaces"]["status"] != "ok" {
			t.Fatalf("surfaces with no answers = code %d json %v stderr %q", verified.code, verified.json, verified.stderr)
		}
		surf := runMC(t, env, "", "onboard", "surfaces",
			"--dashboard-bind", "127.0.0.1:7444", "--discord-mode", "disabled")
		if surf.code != 0 || sectionStatus(t, surf)["surfaces"]["status"] != "done" {
			t.Fatalf("surfaces = code %d json %v stderr %q", surf.code, surf.json, surf.stderr)
		}
		surfAgain := runMC(t, env, "", "onboard", "surfaces",
			"--dashboard-bind", "127.0.0.1:7444", "--discord-mode", "disabled")
		if surfAgain.code != 0 || sectionStatus(t, surfAgain)["surfaces"]["status"] != "ok" {
			t.Fatalf("surface replay = code %d json %v stderr %q", surfAgain.code, surfAgain.json, surfAgain.stderr)
		}
		db, err := substrate.Open(spine)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if got := queryStr(t, db, `SELECT timeout_minutes FROM settings WHERE id = 1`); got != "7" {
			t.Fatalf("applied answers = %q", got)
		}
		cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Timing.TimeoutMinutes != 7 || cfg.Surfaces.Dashboard.Bind != "127.0.0.1:7444" {
			t.Fatalf("config.toml projection = %+v %+v", cfg.Timing, cfg.Surfaces.Dashboard)
		}
		for name, flags := range map[string][]string{
			"conflicting-bind": {"--accept-default-dashboard-bind", "--dashboard-bind", "127.0.0.1:7444"},
			"bad-discord-mode": {"--dashboard-bind", "127.0.0.1:7444", "--discord-mode", "sometimes"},
		} {
			args := append([]string{"onboard", "surfaces"}, flags...)
			bad := runMC(t, env, "", args...)
			if bad.code != 2 {
				t.Fatalf("%s = code %d stderr %q", name, bad.code, bad.stderr)
			}
		}
	})

	// collie_idle_timeout is an operator knob, but the value the wake selector
	// reads is settings.collie_idle_timeout_s — so a config.toml the spine
	// never receives is decoration. The tunables section owns that
	// reconciliation exactly as it does for the five lease tunables.
	t.Run("the collie idle timeout reaches the spine settings", func(t *testing.T) {
		home, spine, env := freshHome(t)
		if res := runMC(t, env, "", "onboard", "home"); res.code != 0 {
			t.Fatalf("home failed: %s", res.stderr)
		}
		db, err := substrate.Open(spine)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if negative := runMC(t, env, "", "onboard", "tunables",
			"--collie-idle-timeout-s", "-1"); negative.code != 2 {
			t.Fatalf("negative idle timeout = code %d stderr %q", negative.code, negative.stderr)
		}
		set := runMC(t, env, "", "onboard", "tunables", "--collie-idle-timeout-s", "900")
		if set.code != 0 || sectionStatus(t, set)["tunables"]["status"] != "done" {
			t.Fatalf("idle timeout = code %d json %v stderr %q", set.code, set.json, set.stderr)
		}
		if got := queryStr(t, db,
			`SELECT collie_idle_timeout_s FROM settings WHERE id = 1`); got != "900" {
			t.Fatalf("settings.collie_idle_timeout_s = %q, want 900", got)
		}
		cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Timing.CollieIdleTimeoutS != 900 {
			t.Fatalf("config.toml collie_idle_timeout_s = %d, want 900", cfg.Timing.CollieIdleTimeoutS)
		}
		replay := runMC(t, env, "", "onboard", "tunables", "--collie-idle-timeout-s", "900")
		if replay.code != 0 || sectionStatus(t, replay)["tunables"]["status"] != "ok" {
			t.Fatalf("idle replay = code %d json %v", replay.code, replay.json)
		}
		// Accepting the recommended defaults must carry the spine back too,
		// not leave it holding the operator's abandoned override.
		reset := runMC(t, env, "", "onboard", "tunables", "--accept-default-tunables")
		if reset.code != 0 || sectionStatus(t, reset)["tunables"]["status"] != "done" {
			t.Fatalf("default reset = code %d json %v", reset.code, reset.json)
		}
		if got := queryStr(t, db,
			`SELECT collie_idle_timeout_s FROM settings WHERE id = 1`); got != "1800" {
			t.Fatalf("reset settings.collie_idle_timeout_s = %q, want 1800", got)
		}
	})
}

// A Project is a Git repository. `mc project add` binds a row to a workspace
// without the onboarding git section, so it applies the rule itself: it
// initializes a workspace without a repository, it adopts a repository with
// its history intact, and it reports which one happened.
func TestProjectAddAppliesTheWorkspaceGitRuleAndReportsIt(t *testing.T) {
	spine := initSpine(t)
	plain, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve plain workspace: %v", err)
	}
	db := openDB(t, spine)
	if _, err := db.Exec(`INSERT INTO sandbox_profiles (id, workspace_root, artifact_roots)
		VALUES ('plain-profile', ?, '[]')`, plain); err != nil {
		t.Fatalf("register plain sandbox profile: %v", err)
	}
	initialized := runMC(t, spineEnv(spine), "", "project", "add", "ws-plain",
		"--title", "Plain", "--sandbox-profile", "plain-profile")
	if initialized.code != 0 {
		t.Fatalf("non-repository workspace add = code %d stderr %q", initialized.code, initialized.stderr)
	}
	if got, _ := initialized.json["workspace_git"].(string); got !=
		verbs.ProjectGitStatement(plain)+" "+verbs.ProjectGitOutcome(plain, true) {
		t.Fatalf("initializing add reported %q", got)
	}
	if _, statErr := os.Lstat(filepath.Join(plain, ".git")); statErr != nil {
		t.Fatalf("initializing add left no repository: %v", statErr)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM projects WHERE id = 'ws-plain'`); got != 1 {
		t.Fatalf("initializing add wrote %d rows", got)
	}

	// The other half, over a repository that already carries history: it is
	// adopted, and the commit that was there is still reachable afterwards.
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve repo workspace: %v", err)
	}
	for _, args := range [][]string{
		{"init", "-q", repo},
		{"-C", repo, "config", "user.name", "Fixture"},
		{"-C", repo, "config", "user.email", "fixture@example.test"},
		{"-C", repo, "commit", "-q", "--allow-empty", "-m", "operator history"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("seed repository (%v): %v (%s)", args, err, out)
		}
	}
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("read seeded HEAD: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sandbox_profiles (id, workspace_root, artifact_roots)
		VALUES ('repo-profile', ?, '[]')`, repo); err != nil {
		t.Fatalf("register repo sandbox profile: %v", err)
	}
	adopted := runMC(t, spineEnv(spine), "", "project", "add", "ws-repo",
		"--title", "Repo", "--sandbox-profile", "repo-profile")
	if adopted.code != 0 {
		t.Fatalf("existing repository add = code %d stderr %q", adopted.code, adopted.stderr)
	}
	if got, _ := adopted.json["workspace_git"].(string); got !=
		verbs.ProjectGitStatement(repo)+" "+verbs.ProjectGitOutcome(repo, false) {
		t.Fatalf("adopting add reported %q", got)
	}
	after, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil || string(after) != string(head) {
		t.Fatalf("adopting add moved HEAD to %q (want %q), err %v", after, head, err)
	}

	// A Project without a sandbox profile has no workspace, so it is refused.
	unbound := runMC(t, spineEnv(spine), "", "project", "add", "ws-unbound",
		"--title", "Unbound")
	if unbound.code != 2 || !strings.Contains(unbound.stderr, "--sandbox-profile") {
		t.Fatalf("profile-less add = code %d stderr %q", unbound.code, unbound.stderr)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM projects WHERE id = 'ws-unbound'`); got != 0 {
		t.Fatalf("profile-less add wrote %d rows", got)
	}
}

// A fence that says only what is WRONG strands the role it was meant to
// correct. The common branch — a role that wrote to /tmp, /mc/scratch, or its
// own checkout — named the artifact CLASS ROOT, which is the one destination
// the next attempt is also refused for (the root is image storage; only a
// declared plane is a host bind). So the refusal pointed at a second refusal.
// A role holding a mandatory flag with no reachable destination takes no
// terminal at all, its container exits, and the task is charged an infra
// retry for a message it could have acted on.
//
// Every destination refusal now carries a place that WOULD be accepted,
// derived from the run's own declared planes, with the per-class leaf.
func TestADurableArtifactRefusalNamesAWritablePlace(t *testing.T) {
	badPath := func(root string) map[string]string {
		return map[string]string{
			"relative_string":           "e.md",
			"container_local_scratch":   "/tmp/artifact.md",
			"the_class_root_itself":     filepath.Join(root, "artifact.md"),
			"a_fabricated_plane_target": filepath.Join(root, "invented", "artifact.md"),
		}
	}

	t.Run("verifier_evidence_points_at_the_verification_leaf", func(t *testing.T) {
		spine, taskID, run, env, _, correction, _ := verifierFixture(t, "evidence hint")
		root := artifactRootFromEnv(t, env)
		plane := filepath.Join(root, packagerPlaneTarget)
		wantDest := filepath.Join(plane, fmt.Sprint(taskID), "verification")
		for name, path := range badPath(root) {
			res := runMC(t, env, "", "verifier", "verdict", fmt.Sprint(taskID),
				"--run", run, "--outcome", "correct", "--problem", "The result misses a criterion.", "--explanation-evidence", "The check failed.", "--resolution", "Correct the result.", "--evidence", path,
				"--correction", correction)
			if res.code == 0 {
				t.Fatalf("%s: a non-durable evidence path must be refused", name)
			}
			if !strings.Contains(res.stderr, plane) {
				t.Errorf("%s: refusal does not name the declared plane %s: %s", name, plane, res.stderr)
			}
			if !strings.Contains(res.stderr, wantDest) {
				t.Errorf("%s: refusal suggests no reachable destination (want %s): %s", name, wantDest, res.stderr)
			}
			// The leaf is per-class, not a constant: evidence must not be sent
			// to the corrections folder.
			if strings.Contains(res.stderr, filepath.Join(plane, fmt.Sprint(taskID), "corrections")) {
				t.Errorf("%s: evidence refusal points at the corrections leaf: %s", name, res.stderr)
			}
		}
		// Fail-closed throughout: a message-only assertion must not accept a
		// state change alongside it.
		db := openDB(t, spine)
		defer db.Close()
		var status string
		if err := db.QueryRow(`SELECT status FROM tasks WHERE id=?`, taskID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != "worked" {
			t.Fatalf("refused verdicts advanced the task to %q", status)
		}
	})

	t.Run("verifier_correction_points_at_the_corrections_leaf", func(t *testing.T) {
		_, taskID, run, env, evidence, _, _ := verifierFixture(t, "correction hint")
		root := artifactRootFromEnv(t, env)
		plane := filepath.Join(root, packagerPlaneTarget)
		wantDest := filepath.Join(plane, fmt.Sprint(taskID), "corrections")
		for name, path := range badPath(root) {
			res := runMC(t, env, "", "verifier", "verdict", fmt.Sprint(taskID),
				"--run", run, "--outcome", "correct", "--problem", "The result misses a criterion.", "--explanation-evidence", "The check failed.", "--resolution", "Correct the result.", "--evidence", evidence,
				"--correction", path)
			if res.code == 0 {
				t.Fatalf("%s: a non-durable correction path must be refused", name)
			}
			if !strings.Contains(res.stderr, wantDest) {
				t.Errorf("%s: refusal suggests no reachable destination (want %s): %s", name, wantDest, res.stderr)
			}
			if strings.Contains(res.stderr, filepath.Join(plane, fmt.Sprint(taskID), "verification")) {
				t.Errorf("%s: correction refusal points at the verification leaf: %s", name, res.stderr)
			}
		}
	})

	// A path that is correctly inside the plane but was never written is the
	// other case whose next move is "write it, then record it" — so it carries
	// the hint too.
	t.Run("an_absent_file_inside_the_plane_still_names_where_to_write", func(t *testing.T) {
		_, taskID, run, env, evidence, correction, _ := verifierFixture(t, "absent hint")
		root := artifactRootFromEnv(t, env)
		absent := filepath.Join(filepath.Dir(correction), "never-written.md")
		res := runMC(t, env, "", "verifier", "verdict", fmt.Sprint(taskID),
			"--run", run, "--outcome", "correct", "--problem", "The result misses a criterion.", "--explanation-evidence", "The check failed.", "--resolution", "Correct the result.", "--evidence", evidence,
			"--correction", absent)
		if res.code == 0 {
			t.Fatal("an absent correction file must be refused")
		}
		if !strings.Contains(res.stderr, "does not exist") ||
			!strings.Contains(res.stderr, filepath.Join(root, packagerPlaneTarget)) {
			t.Fatalf("absent-file refusal carries no destination: %s", res.stderr)
		}
	})
}

// The no-plane arm had no test at all, and it is the one branch that must NOT
// print an example: there is nowhere durable to write, so the only honest next
// move is --infra. Suggesting a path here would send the role in a circle.
func TestADurableArtifactRefusalWithNoPlaneRoutesToInfra(t *testing.T) {
	spine, taskID, run, fixtureEnv, _, _, commit := verifierFixture(t, "no plane at all")
	root := t.TempDir()
	// Same fixture shape, but the launch envelope declares zero writable
	// planes: the shape a Packager hit live when its profile carried no
	// artifact root. The isolated repository of the run stays where it is.
	runJSON := filepath.Join(t.TempDir(), "run.json")
	body, _ := json.Marshal(map[string]any{
		"run_id": run, "tier": "pipeline", "role": "verifier",
		"mounts": map[string]any{"allow_write": []string{}},
	})
	if err := os.WriteFile(runJSON, body, 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(spineEnv(spine), "MC_RUN_JSON="+runJSON, "MC_ARTIFACTS_ROOT="+root)
	for _, kv := range fixtureEnv {
		if strings.HasPrefix(kv, "MC_TASK_STORE_ROOT=") {
			env = append(env, kv)
		}
	}
	res := runMC(t, env, "", "verifier", "verdict", fmt.Sprint(taskID),
		"--run", run, "--outcome", "pass", "--evidence", filepath.Join(root, "e.md"),
		"--sha", commit)
	if res.code == 0 {
		t.Fatal("a run with no writable plane must refuse")
	}
	if !strings.Contains(res.stderr, "--infra") {
		t.Fatalf("no-plane refusal must route to --infra: %s", res.stderr)
	}
	if strings.Contains(res.stderr, "for example") {
		t.Fatalf("no-plane refusal must not suggest a destination: %s", res.stderr)
	}
}

// ---------------------------------------------------------------------------
// mc config show / mc routing show / mc logs — the dashboard read verbs
// ---------------------------------------------------------------------------

// dashReadHome stands up an MC_HOME with a minimal valid config.toml, the
// default routing table, and one resident stderr log line.
func dashReadHome(t *testing.T) (string, []string) {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"),
		[]byte("config_version = 2\n\n[budgets]\nreview_wip_cap = 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), enableReferenceBindings); err != nil {
		t.Fatal(err)
	}
	routing := "| role | harness | binding |\n| --- | --- | --- |\n" +
		"| strategist | claude-sdk | claude |\n| arbiter | codex | chatgpt |\n" +
		"| worker | claude-sdk | endpoint |\n| verifier | codex | chatgpt |\n" +
		"| packager | claude-sdk | endpoint |\n" +
		"| collie | claude-sdk | claude |\n"
	if err := os.WriteFile(filepath.Join(home, "routing.md"), []byte(routing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "logs", "resident.stderr.log"),
		[]byte("tick ok\ntick failed: canary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, []string{"MC_HOME=" + home}
}

func TestConfigShowCLI(t *testing.T) {
	_, env := dashReadHome(t)
	res := runMC(t, env, "", "config", "show")
	if res.code != 0 {
		t.Fatalf("config show exit=%d stderr=%q", res.code, res.stderr)
	}
	families, ok := res.json["families"].([]any)
	if !ok || len(families) == 0 {
		t.Fatalf("config show families = %#v", res.json["families"])
	}
	body, _ := json.Marshal(res.json)
	if !strings.Contains(string(body), `"budgets.review_wip_cap"`) ||
		!strings.Contains(string(body), `"operator"`) {
		t.Fatalf("config show output missing knob/layer: %s", body)
	}
	if usage := runMC(t, env, "", "config"); usage.code != 2 {
		t.Fatalf("bare mc config exit=%d, want usage", usage.code)
	}
}

func TestRoutingShowCLI(t *testing.T) {
	_, env := dashReadHome(t)
	res := runMC(t, env, "", "routing", "show")
	if res.code != 0 {
		t.Fatalf("routing show exit=%d stderr=%q", res.code, res.stderr)
	}
	routes, ok := res.json["routes"].([]any)
	if !ok || len(routes) != 7 {
		t.Fatalf("routing show routes = %#v", res.json["routes"])
	}
	body, _ := json.Marshal(res.json)
	if !strings.Contains(string(body), `"collie"`) {
		t.Fatalf("routing show omits the collie row: %s", body)
	}

	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"),
		[]byte("config_version = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := runMC(t, []string{"MC_HOME=" + home}, "", "routing", "show")
	if missing.code != 1 {
		t.Fatalf("missing routing.md exit=%d stderr=%q, want domain refusal", missing.code, missing.stderr)
	}
}

func TestRoutingSetCLI(t *testing.T) {
	home, env := dashReadHome(t)
	env = append(env, "MC_SPINE="+initSpine(t))
	routeOf := func(res mcResult, role string) map[string]any {
		t.Helper()
		routes, ok := res.json["routes"].([]any)
		if !ok || len(routes) != 7 {
			t.Fatalf("routes = %#v", res.json["routes"])
		}
		for _, raw := range routes {
			route := raw.(map[string]any)
			if route["role"] == role {
				return route
			}
		}
		t.Fatalf("role %q is absent from %#v", role, routes)
		return nil
	}
	for _, args := range [][]string{
		{"routing", "set"},
		{"routing", "set", "--role", "worker"},
		{"routing", "set", "--binding", "claude"},
		{"routing", "set", "--role", "worker", "--binding", "claude", "extra"},
		{"routing", "repoint"},
	} {
		if usage := runMC(t, env, "", args...); usage.code != 2 {
			t.Fatalf("%v exit=%d stderr=%q, want usage", args, usage.code, usage.stderr)
		}
	}
	// A disabled binding is a domain rejection. An unknown role is a usage error.
	if _, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), func(cfg *operatorconfig.Config) error {
		cfg.Runtimes.Credentials["second-endpoint"] = cfg.Runtimes.Credentials["claude"]
		cfg.Runtimes.Bindings["second-endpoint"] = operatorconfig.RuntimeBinding{
			Harness: "claude-sdk", Model: "second-model", Credential: "second-endpoint",
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		role, binding, want string
		code                int
	}{
		{"worker", "second-endpoint", "not enabled", 1},
		{"worker", "missing", "not enabled", 1},
		{"critic", "claude", "role accepts", 2},
	} {
		refused := runMC(t, env, "", "routing", "set", "--role", tc.role, "--binding", tc.binding, "--expected", "endpoint")
		if refused.code != tc.code || !strings.Contains(refused.stderr, tc.want) {
			t.Fatalf("routing set %s %s exit=%d stderr=%q, want %q", tc.role, tc.binding, refused.code, refused.stderr, tc.want)
		}
	}
	if worker := routeOf(runMC(t, env, "", "routing", "show"), "worker"); worker["binding"] != "endpoint" {
		t.Fatalf("a refused routing set changed the table: %#v", worker)
	}
	set := runMC(t, env, "", "routing", "set", "--role", "worker", "--binding", "claude", "--expected", "endpoint")
	if set.code != 0 {
		t.Fatalf("routing set exit=%d stderr=%q", set.code, set.stderr)
	}
	if worker := routeOf(set, "worker"); worker["binding"] != "claude" || worker["harness"] != "claude-sdk" || worker["model"] != "claude-opus-4-8" {
		t.Fatalf("routing set response worker = %#v", worker)
	}
	shown := runMC(t, env, "", "routing", "show")
	if shown.code != 0 {
		t.Fatalf("routing show after set exit=%d stderr=%q", shown.code, shown.stderr)
	}
	if !reflect.DeepEqual(shown.json, set.json) {
		t.Fatalf("routing set response differs from routing show:\n set: %#v\nshow: %#v", set.json, shown.json)
	}
	if arbiter := routeOf(shown, "arbiter"); arbiter["binding"] != "chatgpt" {
		t.Fatalf("routing set changed another role: %#v", arbiter)
	}
}

func TestLogsCLI(t *testing.T) {
	_, env := dashReadHome(t)
	res := runMC(t, env, "", "logs", "resident", "--tail", "1")
	if res.code != 0 {
		t.Fatalf("logs exit=%d stderr=%q", res.code, res.stderr)
	}
	lines, ok := res.json["lines"].([]any)
	if !ok || len(lines) != 1 || lines[0] != "tick failed: canary" {
		t.Fatalf("logs lines = %#v", res.json["lines"])
	}
	if res.json["unit"] != "resident" || res.json["stream"] != "stderr" || res.json["exists"] != true {
		t.Fatalf("logs envelope = %#v", res.json)
	}
	absent := runMC(t, env, "", "logs", "dashboard", "--stream", "stdout")
	if absent.code != 0 || absent.json["exists"] != false {
		t.Fatalf("absent log exit=%d json=%#v", absent.code, absent.json)
	}
	if usage := runMC(t, env, "", "logs"); usage.code != 2 {
		t.Fatalf("bare mc logs exit=%d, want usage", usage.code)
	}
	if unknown := runMC(t, env, "", "logs", "runner"); unknown.code != 2 {
		t.Fatalf("unknown unit exit=%d, want usage", unknown.code)
	}
	// AgentsView writes its operational log to the debug stream.
	agentsView := runMC(t, env, "", "logs", "agentsview")
	if agentsView.code != 0 || agentsView.json["unit"] != "agentsview" || agentsView.json["stream"] != "debug" {
		t.Fatalf("agentsview default stream exit=%d json=%#v", agentsView.code, agentsView.json)
	}
}

// TestDashboardReadVerbsRefusePipelineScope proves the host-scope gate fires
// before any file read: the pipeline caller's MC_HOME holds a config.toml that
// would fail parsing, and the refusal must still be the scope message.
func TestDashboardReadVerbsRefusePipelineScope(t *testing.T) {
	spine := filepath.Join(t.TempDir(), "probe.db")
	env := runJSONEnv(t, spine, "r-probe", "pipeline", "arbiter")
	home := filepath.Dir(spine)
	if err := os.WriteFile(filepath.Join(home, "config.toml"),
		[]byte("this is not toml {{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"config", "show"},
		{"routing", "show"},
		{"routing", "set", "--role", "worker", "--binding", "claude"},
		{"logs", "resident"},
	} {
		res := runMC(t, env, "", args...)
		if res.code != 1 || !strings.Contains(res.stderr, "host scope only") {
			t.Fatalf("%v pipeline exit=%d stderr=%q, want host-scope refusal", args, res.code, res.stderr)
		}
	}
}

// writeVisionFile gives a test workspace a high-level goal.
func writeVisionFile(t *testing.T, workspace string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(workspace, "VISION.md"), []byte("# Vision\nMake this project useful and dependable for its users.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProjectVisionDoctorAndListFollowTheWorkspaceFile(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	root := queryStr(t, db, `SELECT p.workspace_root FROM projects w JOIN sandbox_profiles p ON p.id = w.sandbox_profile WHERE w.id = 'ws-test'`)
	path := filepath.Join(root, "VISION.md")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	check := func(state string, ready bool) {
		t.Helper()
		list := runMC(t, spineEnv(spine), "", "project", "list")
		if list.code != 0 {
			t.Fatalf("project list: %s", list.stderr)
		}
		rows := list.json["projects"].([]any)
		status := rows[0].(map[string]any)["vision_status"].(map[string]any)
		if status["state"] != state || status["ready"] != ready || status["path"] != path {
			t.Fatalf("project list vision = %v", status)
		}
		if _, exists := status["content"]; exists {
			t.Fatal("project list exposed vision content")
		}
		doctor := runMC(t, spineEnv(spine), "", "doctor")
		if doctor.code != 0 {
			t.Fatalf("doctor: %s", doctor.stderr)
		}
		for _, raw := range doctor.json["findings"].([]any) {
			finding := raw.(map[string]any)
			if finding["check"] != "projects" {
				continue
			}
			got := finding["project_vision"].([]any)[0].(map[string]any)
			if !reflect.DeepEqual(got, status) {
				t.Fatalf("doctor vision = %v; list vision = %v", got, status)
			}
			if ready && finding["status"] != "ok" {
				t.Fatalf("doctor retained the work block: %v", finding)
			}
			if !ready && (finding["status"] != "fail" || !strings.Contains(finding["detail"].(string), path)) {
				t.Fatalf("doctor omitted the work block: %v", finding)
			}
			return
		}
		t.Fatal("doctor omitted the projects check")
	}
	check("absent", false)
	writeVisionFile(t, root)
	if out, err := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "VISION.md").CombinedOutput(); err == nil {
		t.Fatalf("vision fixture unexpectedly entered Git: %s", out)
	}
	check("present", true)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	check("absent", false)
}

// moveFixtureMain adds one commit to main of the fixture workspace and returns it.
func moveFixtureMain(t *testing.T, workspace, name string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(workspace, name), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, workspace, "add", name)
	fixtureGit(t, workspace, "commit", "-q", "-m", "Add "+name)
	return fixtureGit(t, workspace, "rev-parse", "refs/heads/main")
}

// `mc dispatch` and `mc delivery start` each read the commit of main on the
// host and store it as the accepted commit. A new commit on main makes the
// current wiki of the fixture behind, and the next tick starts one wiki update.
func TestDispatchRecordsTheAcceptedCommit(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	workspace := queryStr(t, db, `SELECT p.workspace_root FROM projects w JOIN sandbox_profiles p ON p.id = w.sandbox_profile WHERE w.id = 'ws-test'`)
	accepted := func() string {
		t.Helper()
		return queryStr(t, db, `SELECT accepted_commit FROM accepted_commits WHERE project = 'ws-test'`)
	}
	wikiUpdates := func() int {
		t.Helper()
		return queryInt(t, db, `SELECT COUNT(*) FROM wiki_updates WHERE project = 'ws-test'`)
	}
	scribeSpawns := func(tick map[string]any) []map[string]any {
		t.Helper()
		var spawns []map[string]any
		for _, action := range tickActions(t, tick) {
			if _, failed := action["error"]; failed {
				t.Fatalf("the tick holds an error: %v", action)
			}
			if action["action"] == "spawn" && action["role"] == "scribe" {
				spawns = append(spawns, action)
			}
		}
		return spawns
	}

	// The wiki update of the fixture started now, so the interval rule
	// permits no start. The tick still stores the new commit of main.
	second := moveFixtureMain(t, workspace, "second.md")
	if spawns := scribeSpawns(dispatch(t, spine)); len(spawns) != 0 {
		t.Fatalf("the tick started a wiki update before the interval: %v", spawns)
	}
	if got := accepted(); got != second {
		t.Fatalf("accepted commit after mc dispatch = %q, want the commit %q of main", got, second)
	}

	third := moveFixtureMain(t, workspace, "third.md")
	startRequest(t, spine)
	if got := accepted(); got != third {
		t.Fatalf("accepted commit after mc delivery start = %q, want the commit %q of main", got, third)
	}

	// A current wiki whose last wiki update started before the interval.
	if _, recorded, err := wikitest.RecordCurrentWiki(context.Background(), db, "ws-test", third, time.Now().Add(-2*time.Hour)); err != nil || !recorded {
		t.Fatalf("record the current wiki: recorded=%v err=%v", recorded, err)
	}
	before := wikiUpdates()
	if spawns := scribeSpawns(dispatch(t, spine)); len(spawns) != 0 || wikiUpdates() != before {
		t.Fatalf("the tick started a wiki update for a current wiki: %v", spawns)
	}

	fourth := moveFixtureMain(t, workspace, "fourth.md")
	spawns := scribeSpawns(dispatch(t, spine))
	if len(spawns) != 1 || spawns[0]["project"] != "ws-test" || spawns[0]["subject_id"] != nil {
		t.Fatalf("Scribe spawns = %v, want one wiki update of ws-test", spawns)
	}
	run, _ := spawns[0]["run_id"].(string)
	plan, _ := spawns[0]["mount_plan"].(map[string]any)
	repository, _ := plan["repository"].(map[string]any)
	if repository["input_commit"] != fourth || repository["wiki_update"] != run || repository["workspace_root"] != workspace {
		t.Fatalf("repository plan = %v, want the wiki update %s at the commit %s", repository, run, fourth)
	}
	if got := queryStr(t, db, `SELECT run_id || '/' || start_commit FROM wiki_updates WHERE project = 'ws-test' AND ended_at IS NULL`); got != run+"/"+fourth {
		t.Fatalf("active wiki update = %q, want %s at %s", got, run, fourth)
	}
	if got := accepted(); got != fourth {
		t.Fatalf("accepted commit = %q, want %q", got, fourth)
	}
	if got := queryStr(t, db, `SELECT kind || '/' || project FROM activity WHERE subject = ? AND actor = 'dispatch'`, run); got != "wiki.update_started/ws-test" {
		t.Fatalf("start record = %q, want wiki.update_started of ws-test", got)
	}

	// One wiki update is active, so the next tick starts no second one.
	if spawns := scribeSpawns(dispatch(t, spine)); len(spawns) != 0 || wikiUpdates() != before+1 {
		t.Fatalf("the tick started a second wiki update: %v", spawns)
	}
}
