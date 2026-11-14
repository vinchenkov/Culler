//go:build docker_e2e

// Package e2e runs the pipeline end to end. One operator task traverses
//
//	tick → dispatch → lease → fake-harness Worker → mc complete → capture → …
//	→ packet → approve → apply
//
// and one wiki update traverses
//
//	tick → dispatch → fake-harness Scribe → mc complete → capture → prepare → apply
//
// through the real mc binary, the real resident process on a real interval
// timer, and real containers. The mc binary delegates into the warm helper,
// so the spine never leaves its named volume. The tests invoke only operator
// verbs (init, task add, packet decide, the settings, and the reads). They
// never call `mc dispatch`, so every stage advance proves timer-driven
// advancement. The docker_e2e build tag gates the package: the fast suite
// never compiles it.
package e2e

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	operatorconfig "mc/config"
	"mc/mclog"
	"mc/verbs"
	"mc/wikitest"
)

const (
	spineDBPath = "/mc/spine/spine.db" // in the lock domain (named volume at /mc/spine)
	project     = "ws-e2e"
)

var image = testValue("MC_E2E_IMAGE", "mc-fake-e2e")
var realImage = testValue("MC_E2E_REAL_IMAGE", "mc-real-e2e")
var projectImage = testValue("MC_E2E_PROJECT_IMAGE", image+"-project")
var testReleaseID = testValue("MC_E2E_RELEASE_ID", "development")

func testValue(variable, fallback string) string {
	if value := os.Getenv(variable); value != "" {
		return value
	}
	return fallback
}

func imageCommand(repoRoot, variable, name string, real bool) *exec.Cmd {
	if os.Getenv(variable) != "" {
		return exec.Command("docker", "image", "inspect", "--", name)
	}
	args := []string{filepath.Join(repoRoot, "runner", "image", "build.sh")}
	if real {
		args = append(args, "--real")
	}
	return exec.Command("bash", args...)
}

var testCanaryImage struct {
	once sync.Once
	out  []byte
	err  error
}

var testRuntime struct {
	once   sync.Once
	volume string
	err    error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if testRuntime.volume != "" {
		_ = exec.Command("docker", "volume", "rm", testRuntime.volume).Run()
	}
	os.Exit(code)
}

// fixture owns every external resource; all cleanup is registered on t.
type fixture struct {
	t             *testing.T
	base          string // temp base under /private/tmp (Docker Desktop file sharing)
	hostMC        string // darwin mc binary
	env           []string
	helper        string
	volume        string
	runtimeVolume string
	projectDigest string
	home          string // MC_HOME
	ws            string // workspace git repo (the Project)
	seedRun       string // run that the seed step recorded for a current Project wiki; empty when a wiki update is due
	resLog        *os.File
	resProc       *exec.Cmd
}

// TestWalkingSkeleton proves the first wiki update of a Project and the
// complete pipeline of one task. The Scribe creates the Project wiki, and the
// apply operation merges the wiki commit into culler with no review. Then the
// Worker commits in its isolated repository, the Verifier passes the commit,
// the Packager renders the packet, the operator approves it, and the apply
// operation moves culler to the candidate commit. No Scribe run has the task as
// its subject.
func TestWalkingSkeleton(t *testing.T) {
	f := setup(t, withDueWikiUpdate())

	initial := f.git("rev-parse", "culler")
	lease := f.lease(project)
	if lease["run_id"] != nil {
		t.Fatalf("lease must start free, got %v", lease)
	}

	f.startResident()

	// The Project has no wiki update, so the first tick starts one. The
	// Scribe run has no task.
	wikiRun := f.waitForAgentRun("scribe", 60*time.Second)
	update := f.waitForCurrentWiki(wikiRun, 180*time.Second)
	mainBefore := f.git("rev-parse", "culler")
	wikiCommit, _ := update["wiki_commit"].(string)
	if update["start_commit"] != initial || wikiCommit == "" || wikiCommit == initial || update["merged_commit"] != mainBefore {
		t.Fatalf("the wiki update = %v, want the start commit %s and a wiki commit that culler %s holds", update, initial, mainBefore)
	}
	if scribe := f.runRow(wikiRun); scribe["subject"] != nil || scribe["outcome"] != "completed" {
		t.Fatalf("the Scribe run of the wiki update = %v, want a completed run with no task", scribe)
	}
	// The merge gave culler one wiki commit, and the commit changes the wiki only.
	if n := f.git("rev-list", "--count", initial+".."+mainBefore); n != "1" {
		t.Fatalf("the wiki update gave culler %s commits, want one wiki commit", n)
	}
	for _, name := range strings.Fields(f.git("diff", "--name-only", initial, mainBefore)) {
		if !strings.HasPrefix(name, "openwiki/") {
			t.Fatalf("the wiki commit changes %s, a path outside openwiki/", name)
		}
	}
	if got := f.git("show", "culler:openwiki/index.md"); got == "" {
		t.Fatal("culler does not hold openwiki/index.md after the wiki update")
	}

	res := f.mcOK("", "task", "add", "skeleton task", "--project", project,
		"--description", "The walking skeleton has one task.")
	taskID := int64(res["task_id"].(float64))
	task := f.mcOK("", "task", "get", fmt.Sprint(taskID))
	if task["status"] != "seeded" || task["origin"] != "user" {
		t.Fatalf("fresh task = status %v origin %v, want seeded/user", task["status"], task["origin"])
	}

	// The Worker receives the operator requirements and a durable session.
	workerRun := f.waitForAgentRun("worker", 60*time.Second)
	f.waitFor(15*time.Second, "the Worker session trace appears", func() (bool, string) {
		path := filepath.Join(f.home, "sessions", workerRun, "native.jsonl")
		if _, err := os.Stat(path); err != nil {
			return false, path + " is absent"
		}
		return true, ""
	})

	var hb1 string
	f.waitFor(15*time.Second, "first worker heartbeat", func() (bool, string) {
		lease := f.lease(project)
		if lease["run_id"] != workerRun {
			return false, fmt.Sprintf("lease moved: %v", lease["run_id"])
		}
		if lease["last_heartbeat_at"] == nil {
			return false, "last_heartbeat_at still NULL"
		}
		hb1 = lease["last_heartbeat_at"].(string)
		return true, ""
	})
	f.waitFor(15*time.Second, "the heartbeat advances during the worker run", func() (bool, string) {
		lease := f.lease(project)
		if lease["run_id"] != workerRun {
			return false, "lease moved before a second heartbeat was observed"
		}
		hb, _ := lease["last_heartbeat_at"].(string)
		if hb == hb1 || hb == "" {
			return false, "still " + hb1
		}
		return true, ""
	})

	// The capture operation records the submitted commit and retains it in
	// the Project repository under the run's version reference.
	f.waitForTaskStatus(taskID, "worked", 60*time.Second)
	workedSHA := f.retainedVersion(taskID, workerRun)
	if workedSHA == mainBefore {
		t.Fatalf("the Worker submitted the input commit %s", mainBefore)
	}
	if got := f.mcOK("", "task", "get", fmt.Sprint(taskID))["completion_sha"]; got != workedSHA {
		t.Fatalf("completion_sha = %v, want the retained version %s", got, workedSHA)
	}
	workerRow := f.runRow(workerRun)
	if workerRow["native_session_ref"] != "fake-session" || workerRow["trace_filename"] != "native.jsonl" {
		t.Fatalf("register-session locators wrong on runs row: %v", workerRow)
	}

	// The Verifier records the exact reviewed commit.
	f.waitForTaskStatus(taskID, "verified", 60*time.Second)
	task = f.mcOK("", "task", "get", fmt.Sprint(taskID))
	if task["verified_sha"] != workedSHA {
		t.Fatalf("verified_sha = %v, want %s", task["verified_sha"], workedSHA)
	}

	// A verified task gets the Packager. The submitted commit is the commit
	// that the Verifier passed.
	f.waitForTaskStatus(taskID, "packaged", 120*time.Second)
	task = f.mcOK("", "task", "get", fmt.Sprint(taskID))
	delivery := task["delivery"].(map[string]any)
	submitted, _ := delivery["submitted_commit"].(string)
	if submitted != workedSHA || delivery["source_verified_commit"] != workedSHA {
		t.Fatalf("the packaged task does not hold the verified commit %s: %v", workedSHA, delivery)
	}

	// The prepare operation builds the candidate and publishes the packet.
	packet := f.waitForReadyPacket(taskID, 90*time.Second)
	if packet["submitted_commit"] != submitted {
		t.Fatalf("packet submitted_commit = %v, want %s", packet["submitted_commit"], submitted)
	}
	candidate, _ := packet["candidate_commit"].(string)
	if candidate != submitted {
		t.Fatalf("candidate_commit = %q, want the fast-forward candidate %s", candidate, submitted)
	}
	renderPrefix := fmt.Sprintf("%s/%d/packet/snapshot-", e2eArtifactPlane, taskID)
	if got, _ := packet["render_path"].(string); !strings.HasPrefix(got, renderPrefix) {
		t.Fatalf("packet render_path = %q, want an immutable report snapshot under %s", got, renderPrefix)
	}
	if len(f.packets()) != 1 {
		t.Fatalf("packet count = %d, want 1", len(f.packets()))
	}

	// The board is at rest, so a new Strategist run ends with an
	// empty batch. Earlier Strategist runs served the empty board of the wiki
	// update part.
	completedProposals := func() int {
		n := 0
		for _, r := range f.runs() {
			if r["role"] == "strategist" && r["ended_at"] != nil && r["outcome"] == "completed" {
				n++
			}
		}
		return n
	}
	proposalsBefore := completedProposals()
	f.waitFor(60*time.Second, "a Strategist run spawns and terminates via empty batch", func() (bool, string) {
		if n := completedProposals(); n > proposalsBefore {
			return true, ""
		}
		return false, "no new completed strategist run yet"
	})
	if got := f.mcRun("", "task", "get", fmt.Sprint(taskID+1)); got.code != 1 {
		t.Fatalf("a task %d appeared (exit %d): empty strategist batches must add nothing", taskID+1, got.code)
	}

	// The approval is a pure state write. The apply operation moves culler later.
	f.approve(taskID, packet)
	if now := f.git("rev-parse", "culler"); now != mainBefore {
		t.Fatalf("host culler moved at approve time (%s → %s): approve must be a pure state write", mainBefore, now)
	}
	task = f.mcOK("", "task", "get", fmt.Sprint(taskID))
	if task["decision"] != "approved" {
		t.Fatalf("after approve: decision = %v, want approved", task["decision"])
	}
	if task["archived"].(float64) == 0 && task["status"] != "packaged" {
		t.Fatalf("after approve: %v, want status=packaged while unarchived", task)
	}

	// The apply operation updates culler and archives the task.
	f.waitForArchived(taskID, 90*time.Second)
	if p := f.packets(); p[0]["archived"].(float64) != 1 {
		t.Fatalf("packet not archived with the task")
	}
	mainAfter := f.git("rev-parse", "culler")
	if mainAfter != candidate {
		t.Fatalf("culler = %s after the apply, want the candidate commit %s", mainAfter, candidate)
	}
	if got := f.git("show", "culler:task-"+fmt.Sprint(taskID)+".txt"); !strings.Contains(got, "work for task") {
		t.Fatalf("culler does not carry the Worker's file: %q", got)
	}
	f.waitFor(10*time.Second, "lease free at rest", func() (bool, string) {
		if lease := f.lease(project); lease["run_id"] != nil {
			return false, fmt.Sprintf("held by %v", lease["owner"])
		}
		return true, ""
	})

	// The merge of the task moved culler, so the Project wiki is behind. The
	// interval holds the next wiki update: the one Scribe run of the Project
	// is the run of the first wiki update, and it has no task.
	f.waitFor(15*time.Second, "the wiki state behind after the merge of the task", func() (bool, string) {
		state := f.wikiState()
		if state["state"] != "behind" || state["accepted_commit"] != mainAfter || state["next_start_at"] == nil {
			return false, fmt.Sprintf("state=%v accepted_commit=%v next_start_at=%v", state["state"], state["accepted_commit"], state["next_start_at"])
		}
		return true, ""
	})
	if scribes := f.agentRuns("scribe"); len(scribes) != 1 || scribes[0]["id"] != wikiRun || scribes[0]["subject"] != nil {
		t.Fatalf("Scribe runs = %v, want the one run %s of the wiki update with no task", scribes, wikiRun)
	}

	// Every session folder holds only the trace.
	sessions, err := os.ReadDir(filepath.Join(f.home, "sessions"))
	if err != nil {
		t.Fatalf("read sessions dir: %v", err)
	}
	for _, s := range sessions {
		entries, err := os.ReadDir(filepath.Join(f.home, "sessions", s.Name()))
		if err != nil {
			t.Fatalf("read session %s: %v", s.Name(), err)
		}
		for _, e := range entries {
			if e.Name() != "native.jsonl" {
				t.Fatalf("session %s holds %q; the folder holds nothing but the trace", s.Name(), e.Name())
			}
		}
	}
}

// TestSpineLockDomainGuardDockerBoundary pins both directions of the
// lock-domain guard against the real kernel that enforces it.
//
// The guard's only production implementation is Linux-only, so the fast lane
// proves its decision function against captured mountinfo text. Here the
// actual /proc/self/mountinfo of the actual container gets a vote. Every other
// Docker test proves acceptance implicitly, so acceptance alone can pass while
// the refusal has quietly rotted into a no-op.
//
// The refusal arm also pins a production finding: Docker Desktop surfaces a
// host bind as `fakeowner`, not `virtiofs`, so a denylist keyed on the obvious
// name would accept the very mount that corrupted the spine.
func TestSpineLockDomainGuardDockerBoundary(t *testing.T) {
	f := setup(t)

	// Refused: a host directory bound at /mc/spine.
	bindDir := filepath.Join(f.base, "bound-spine")
	if err := os.MkdirAll(bindDir, 0o777); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("docker", "run", "--rm", "--network", "none",
		"-v", bindDir+":/mc/spine", "-e", "MC_SPINE=/mc/spine/spine.db",
		image, "mc", "lease", "list").CombinedOutput()
	if err == nil {
		t.Fatalf("bind-mounted spine was ACCEPTED: %q; the lock-domain guard is not enforcing", out)
	}
	for _, want := range []string{"refusing to open the spine under", "fakeowner", "not a block-device-backed local filesystem"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("bind refusal %q does not name %q", out, want)
		}
	}

	// Refused: a single file bound over spine.db inside an otherwise legitimate
	// named volume. The directory still reports ext4; only the database itself
	// is on VirtioFS. This is the arm a directory-only guard would pass.
	fileBind := filepath.Join(f.base, "bound-spine.db")
	if err := os.WriteFile(fileBind, nil, 0o666); err != nil {
		t.Fatal(err)
	}
	out, err = exec.Command("docker", "run", "--rm", "--network", "none",
		"-v", f.volume+":/mc/spine", "-v", fileBind+":"+spineDBPath,
		"-e", "MC_SPINE="+spineDBPath, image, "mc", "lease", "list").CombinedOutput()
	if err == nil {
		t.Fatalf("single-file-bound spine was ACCEPTED: %q; the guard is checking only the directory", out)
	}
	if !strings.Contains(string(out), "refusing to open the spine under") || !strings.Contains(string(out), spineDBPath) {
		t.Fatalf("single-file bind refusal %q does not name the refusal and the spine file", out)
	}

	// Accepted: the named volume the production topology actually uses.
	if out := f.dockerOutput("run", "--rm", "--network", "none",
		"-v", f.volume+":/mc/spine", "-e", "MC_SPINE="+spineDBPath,
		image, "mc", "lease", "list"); !strings.Contains(out, "leases") {
		t.Fatalf("named-volume spine read = %q, want the lease rows; the guard must not refuse the lease read", out)
	}
}

// TestDeploymentMirrorDockerBoundary covers the real resident/helper seam of
// the deployment identity mirror. The unit frame tests own the
// prepare→attest swap race; this tagged probe proves the host file is visible
// in the helper, a foreign value prevents a resident-driven claim, and
// recovery requires the exact mirror.
func TestDeploymentMirrorDockerBoundary(t *testing.T) {
	f := setup(t)
	mirror := filepath.Join(f.home, "deployment.uuid")
	good, err := os.ReadFile(mirror)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.dockerOutput("exec", f.helper, "cat", "/mc/home/deployment.uuid"); got != string(good) {
		t.Fatalf("helper deployment mirror = %q, want host bind bytes %q", got, good)
	}
	// The fixture changes the mirror after the resident completes its startup checks.
	f.startResident()

	if err := os.WriteFile(mirror, []byte("foreign-deployment\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := f.mcOK("", "task", "add", "mirror boundary task", "--project", project,
		"--description", "The task must not claim under a foreign deployment mirror.")
	taskID := int64(res["task_id"].(float64))
	f.waitFor(10*time.Second, "the helper rejects a foreign deployment mirror", func() (bool, string) {
		if !strings.Contains(f.residentLog(), "deployment identity mismatch") {
			return false, "mirror mismatch not yet logged"
		}
		if len(f.runs()) != 0 {
			return false, "foreign mirror created a Run"
		}
		lease := f.lease(project)
		if lease["run_id"] != nil {
			return false, fmt.Sprintf("foreign mirror held the lease %v", lease["run_id"])
		}
		return true, ""
	})
	if task := f.mcOK("", "task", "get", fmt.Sprint(taskID)); task["status"] != "seeded" || task["blocked"].(float64) != 0 {
		t.Fatalf("foreign mirror mutated task state: %v", task)
	}
	if err := os.WriteFile(mirror, good, 0o600); err != nil {
		t.Fatal(err)
	}
	f.waitFor(15*time.Second, "dispatch recovery after restoring the exact deployment mirror", func() (bool, string) {
		for _, run := range f.runs() {
			if run["role"] == "worker" {
				return true, ""
			}
		}
		return false, "The Worker did not start after mirror recovery."
	})
}

// ───────────────────────────── fixtures ─────────────────────────────────

type setupOptions struct {
	// seedSpine adds test records before the fixture copies the closed spine into its named volume.
	seedSpine func(f *fixture, db *sql.DB)
	// dueWikiUpdate leaves the Project with no wiki update, so the first tick starts one.
	dueWikiUpdate bool
}

func withSeededSpine(seed func(f *fixture, db *sql.DB)) func(*setupOptions) {
	return func(o *setupOptions) { o.seedSpine = seed }
}

// withDueWikiUpdate serves a test that proves a wiki update. Each other test
// starts with a current Project wiki, so its ticks start no Scribe run.
func withDueWikiUpdate() func(*setupOptions) {
	return func(o *setupOptions) { o.dueWikiUpdate = true }
}

// e2eArtifactPlane is where the Project artifact root lands inside a
// container. The mount plan composes `/mc/artifacts/` + the allowlist target,
// and the onboarding grant names the Project as the target.
const e2eArtifactPlane = "/mc/artifacts/" + project

// e2eVerifierEvidence is the shell prologue every fake Verifier runs before its
// verdict. The evidence lands in the task's own folder on the durable plane,
// never in /tmp and never in the repository. The verdict terminal opens the
// path, so a fixture that writes to /tmp fails here rather than in a live run.
const e2eVerifierEvidence = `d=` + e2eArtifactPlane + `/$MC_SUBJECT_ID/verification
mkdir -p "$d"
ev=$d/gate-ladder.txt
`

// initTunables is the provisioning tail of `mc __init`. The profile's
// workspace root is the host path: the mount attest derives every container
// destination from it.
func initTunables(ws string) []string {
	return []string{
		"--project", project, "--workspace-root", ws,
		// The request half of the artifact plane. Without it the profile
		// derives a plan with no writable artifact row and every packaged
		// terminal is refused as undurable.
		"--artifact-root", verbs.ProjectArtifactRoot(ws),
		"--timeout-minutes", "10", "--grace-minutes", "5",
		"--heartbeat-interval-s", "1", "--spawn-grace-s", "5",
		"--hard-deadline-minutes", "30",
	}
}

// loadSpineIntoVolume copies a closed host-built spine into the named volume
// before any container opens it, using a never-started stager container as the
// `docker cp` target (a volume has no path of its own to copy into).
//
// It then widens the volume root. Docker materializes a fresh volume root as
// root:root 0755, which is enough for the root helper but not for the
// hardened agent containers: those run with `--user 10002:10002`, and the
// setuid mc runs its verbs as uid 10001. Both uids write the spine, including
// the -wal/-shm siblings SQLite creates in the directory.
func (f *fixture) loadSpineIntoVolume(hostSpine string) {
	f.t.Helper()
	f.loadSpineIntoNamedVolume(f.volume, hostSpine)
}

func (f *fixture) loadSpineIntoNamedVolume(volume, hostSpine string) {
	f.t.Helper()
	stager := volume + "-load"
	f.docker("create", "--name", stager, "-v", volume+":/mc/spine", image, "true")
	defer func() { exec.Command("docker", "rm", "-f", stager).Run() }()
	f.docker("cp", hostSpine, stager+":"+spineDBPath)
	f.docker("run", "--rm", "-v", volume+":/mc/spine", image,
		"sh", "-ec", "chmod 0777 /mc/spine && chmod 0666 "+spineDBPath)
}

func setup(t *testing.T, opts ...func(*setupOptions)) *fixture {
	t.Helper()
	var o setupOptions
	for _, fn := range opts {
		fn(&o)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("docker CLI not found: %v", err)
	}

	// /private/tmp is Docker-Desktop-shared by default; t.TempDir() lands in
	// /var/folders which may not be. MC_HOME must be a scratch path, never
	// ~/.culler.
	base, err := os.MkdirTemp("/private/tmp", "mc-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	// MC_E2E_KEEP preserves the tree after the run. Every run's native session
	// file, and with it the exit code, stdout and stderr of each fake-harness
	// step, lives under here.
	t.Cleanup(func() {
		if os.Getenv("MC_E2E_KEEP") != "" {
			t.Logf("MC_E2E_KEEP set; leaving %s in place", base)
			return
		}
		os.RemoveAll(base)
	})

	f := &fixture{t: t, base: base}
	secretDir := filepath.Join(base, "host-secrets")
	if err := os.Mkdir(secretDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CULLER_TEST_HOST_SECRET_DIR", secretDir)
	sfx := strings.ToLower(filepath.Base(base)[len("mc-e2e-"):])
	f.volume = "mc-e2e-spine-" + sfx
	f.helper = "mc-e2e-helper-" + sfx
	f.home = filepath.Join(base, "home")
	f.ws = filepath.Join(base, "ws")
	for _, d := range []string{f.home, f.ws, filepath.Join(base, "bin")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Host mount attest trusts MC_HOME the way it trusts the allowlist:
	// operator-only, no group/other bits.
	if err := os.Chmod(f.home, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"runs", "sessions", "logs"} {
		if err := os.MkdirAll(filepath.Join(f.home, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// config.toml is what the runtime capability, the credential catalog, the
	// role Git identity, and backup retention are all resolved from. t.Setenv
	// also pins these tests non-parallel, which they already are by resource.
	if _, err := operatorconfig.Ensure(filepath.Join(f.home, "config.toml")); err != nil {
		t.Fatal(err)
	}
	// The template enables no binding. The lane enables both reference
	// bindings and one endpoint binding that it defines.
	if _, err := operatorconfig.UpdateAtomic(filepath.Join(f.home, "config.toml"), func(cfg *operatorconfig.Config) error {
		profile := cfg.Runtimes.Bindings["chatgpt"]
		profile.Model = "fixture-model"
		cfg.Runtimes.Bindings["chatgpt"] = profile
		cfg.Runtimes.Credentials["endpoint"] = cfg.Runtimes.Credentials["claude"]
		cfg.Runtimes.Bindings["endpoint"] = operatorconfig.RuntimeBinding{
			Harness: "claude-sdk", Model: "endpoint-model", Credential: "endpoint",
		}
		cfg.Runtimes.EnabledBindings = []string{"chatgpt", "claude", "endpoint"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_HOME", f.home)
	// The allowlist baseline and the Project artifact grant come from the real
	// onboarding producers, so the lane starts from the MC_HOME an install
	// produces.
	if _, err := verbs.EnsureMountAllowlist(f.home); err != nil {
		t.Fatal(err)
	}
	if _, err := verbs.EnsureProjectArtifactGrant(f.home, project, verbs.ProjectArtifactRoot(f.ws)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.home, "routing.md"), []byte(fakeRouting), 0o600); err != nil {
		t.Fatal(err)
	}

	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	// Host (darwin) mc binary for the test and the resident. The
	// test_fake_routing tag selects the whole host↔helper crossing:
	// shouldDelegateToHelper, helperContainerName, helperSpinePath, and
	// ensureRuntimeHelper. An untagged host reads neither MC_HELPER nor
	// MC_SPINE, so it cannot reach the helper this fixture manages.
	f.hostMC = filepath.Join(base, "bin", "mc")
	cmd := exec.Command("mise", "exec", "--", "go", "build", "-tags", "test_fake_routing", "-ldflags", "-X main.attachmentCanaryImage="+realImage+" -X main.releaseBuildID="+testReleaseID, "-o", f.hostMC, "./cmd/mc")
	cmd.Dir = filepath.Join(repoRoot, "mc")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build host mc: %v\n%s", err, out)
	}

	// The attachment canary uses the untagged test image.
	testCanaryImage.once.Do(func() {
		build := imageCommand(repoRoot, "MC_E2E_REAL_IMAGE", realImage, true)
		testCanaryImage.out, testCanaryImage.err = build.CombinedOutput()
	})
	if testCanaryImage.err != nil {
		t.Fatalf("prepare test attachment canary image: %v\n%s",
			testCanaryImage.err, testCanaryImage.out)
	}

	// The image: linux/arm64 mc + mc-land baked. Built once; Docker's cache
	// makes reruns cheap.
	build := imageCommand(repoRoot, "MC_E2E_IMAGE", image, false)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("prepare image: %v\n%s", err, out)
	}
	f.projectDigest = strings.TrimSpace(f.dockerOutput("image", "inspect", "--format", "{{.Id}}", projectImage))
	testRuntime.once.Do(func() {
		testRuntime.volume = fmt.Sprintf("culler-e2e-runtime-%d", os.Getpid())
		if output, err := exec.Command("docker", "volume", "create", testRuntime.volume).CombinedOutput(); err != nil {
			testRuntime.err = fmt.Errorf("create the test runtime: %w: %s", err, output)
			return
		}
		output, err := exec.Command("docker", "run", "--rm", "--network", "none", "--user", "0:0", "--mount", "type=volume,src="+testRuntime.volume+",dst=/runtime-output,volume-nocopy", image, "/bin/sh", "-ec", "cp -a /opt/culler-runtime/. /runtime-output/").CombinedOutput()
		if err != nil {
			testRuntime.err = fmt.Errorf("copy the test runtime: %w: %s", err, output)
		}
	})
	if testRuntime.err != nil {
		t.Fatal(testRuntime.err)
	}
	f.runtimeVolume = testRuntime.volume

	// The Project: a host git repository with one commit on culler. The durable
	// artifact plane is the host location production onboarding derives. It
	// exists before the first dispatch because the mount attest resolves every
	// plan source with mustExist. It is ignored so the apply operation sees a
	// clean checkout.
	f.git("init", "-q", "-b", "culler")
	f.git("config", "user.name", "e2e operator")
	f.git("config", "user.email", "operator@e2e.invalid")
	if err := os.MkdirAll(verbs.ProjectArtifactRoot(f.ws), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.ws, ".gitignore"), []byte(".mc-artifacts/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.ws, "README.md"), []byte("walking skeleton project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.ws, "VISION.md"), []byte("# Project vision\n\nThe project provides reliable results through complete, verified contributions.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.git("add", ".gitignore", "README.md", "VISION.md")
	f.git("commit", "-q", "-m", "initial commit")

	// Host-side mc env: helper delegation only. MC_SPINE stays unset because
	// the spine never leaves the lock domain. MC_HELPER is appended only after
	// the helper exists, so the seeding init below runs direct.
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "MC_SPINE=") || strings.HasPrefix(e, "MC_HELPER=") ||
			strings.HasPrefix(e, "MC_RUN_JSON=") || strings.HasPrefix(e, "MC_TICK_INTERVAL_MS=") {
			continue
		}
		f.env = append(f.env, e)
	}

	// Spine volume + warm helper: the lock domain.
	f.docker("volume", "create", f.volume)
	t.Cleanup(func() {
		// Volume removal must outlive (run after) container removal: LIFO.
		exec.Command("docker", "volume", "rm", "-f", f.volume).Run()
	})

	// The spine is built host-side in a directory nothing bind-mounts, then
	// loaded into the named volume. `mc __init` also creates the Project tool
	// home under MC_HOME.
	seedDir := filepath.Join(base, "seed")
	if err := os.MkdirAll(seedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	hostSpine := filepath.Join(seedDir, "spine.db")
	initEffect := f.mcOK("", append([]string{"__init", "--spine", hostSpine}, initTunables(f.ws)...)...)
	db, err := verbs.OpenSpine(hostSpine)
	if err != nil {
		t.Fatalf("open host-built spine: %v", err)
	}
	f.recordEnvironment(db)
	if !o.dueWikiUpdate {
		run, recorded, err := wikitest.RecordCurrentWiki(context.Background(), db, project, f.git("rev-parse", "culler"), time.Now())
		if err != nil || !recorded {
			t.Fatalf("record the current Project wiki: recorded %v, %v", recorded, err)
		}
		f.seedRun = run
	}
	if o.seedSpine != nil {
		o.seedSpine(f, db)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close host-built spine: %v", err)
	}
	f.loadSpineIntoVolume(hostSpine)

	f.docker("run", "-d", "--rm", "--name", f.helper,
		"--label", "mc-managed", "--label", "mc-tier=helper",
		"-v", f.volume+":/mc/spine",
		"-v", f.home+":/mc/home:ro",
		"-e", "MC_SPINE="+spineDBPath,
		"-e", "MC_HOME=/mc/home",
		image, "sleep", "infinity")
	t.Cleanup(func() {
		// Helper verbs run as `docker exec`, so their stderr goes to the exec,
		// never to the container's log. Reap any straggler pipeline and Collie
		// containers this run spawned by their labels, then the helper. A
		// running Collie container holds the spine volume, the runtime volume,
		// and the bind mounts under the fixture folder, so a later removal of
		// each one fails while it runs. The volume filter keeps the reap away
		// from each container of another spine.
		collie := []string{"--filter", "label=mc-managed=true", "--filter", "label=mc-tier=collie"}
		for _, labels := range [][]string{verbs.PipelineContainerLabelFilters(), collie} {
			filters := append(labels, "--filter", "volume="+f.volume)
			if out, err := exec.Command("docker", append([]string{"ps", "-aq"}, filters...)...).Output(); err == nil {
				for _, id := range strings.Fields(string(out)) {
					exec.Command("docker", "rm", "-f", id).Run()
				}
			}
		}
		exec.Command("docker", "rm", "-f", f.helper).Run()
	})

	f.env = append(f.env, "MC_HELPER="+f.helper)

	// The deployment identity mirror: dispatch refuses to prepare without it
	// matching meta.deployment_uuid. f.home is the host side of the
	// container's MC_HOME bind.
	uuid, _ := initEffect["deployment_uuid"].(string)
	if uuid == "" {
		t.Fatalf("mc __init effect carries no deployment_uuid: %v", initEffect)
	}
	if err := os.WriteFile(filepath.Join(f.home, "deployment.uuid"), []byte(uuid+"\n"), 0o600); err != nil {
		t.Fatalf("write deployment mirror: %v", err)
	}

	f.writeBehaviors()
	f.writeResidentConfig(repoRoot)
	return f
}

func (f *fixture) recordEnvironment(db *sql.DB) {
	f.t.Helper()
	configuration := verbs.EnvironmentConfiguration{BaseDigest: f.projectDigest}
	if err := verbs.ValidateEnvironmentConfiguration(&configuration); err != nil {
		f.t.Fatal(err)
	}
	body, err := json.Marshal(configuration)
	if err != nil {
		f.t.Fatal(err)
	}
	revision := "fixture-environment-" + project
	if _, err = db.Exec(`INSERT INTO environment_revisions(id,project_id,editor,configuration,inputs_digest) VALUES(?,?,'fixture',?,?)`, revision, project, string(body), "sha256:"+strings.Repeat("b", 64)); err != nil {
		f.t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO project_environments(project_id,setup_id,selected_base_digest,selected_revision_id,selected_image_digest,draft_revision_id,readiness,phase,authorization) VALUES(?,?,?,?,?,?,'ready','complete','change')`, project, "fixture-setup-"+project, f.projectDigest, revision, f.projectDigest, revision); err != nil {
		f.t.Fatal(err)
	}
	cachePrefix := fmt.Sprintf("mc-cache-%x", sha256.Sum256([]byte(f.home)))[:25]
	f.t.Cleanup(func() {
		output, _ := exec.Command("docker", "volume", "ls", "--quiet", "--filter", "name="+cachePrefix+"-").Output()
		for _, name := range strings.Fields(string(output)) {
			_ = exec.Command("docker", "volume", "rm", name).Run()
		}
	})
}

// fakeRouting routes every role to the fake harness.
const fakeRouting = `# fake E2E routing
| role | harness | binding |
| --- | --- | --- |
| strategist | fake | fake |
| arbiter | fake | fake |
| worker | fake | fake |
| verifier | fake | fake |
| scribe | fake | fake |
| packager | fake | fake |
| collie | fake | fake |
`

// workerScript is the default Worker. It reads its envelope, answers a
// correction by merging the destination, writes one file named after the
// task, commits, and submits the exact commit. A file named after the task
// keeps independent tasks free of conflicts.
const workerScript = `import { writeFileSync } from "node:fs";

const envelope = await Bun.file("/mc/run.json").json();
const workspace = envelope.mounts.workspace;
const task = String(envelope.subject_id);
const run = envelope.run_id;

// The run brief is a text document. A part starts at a "## " heading.
function part(heading: string): string {
  const start = envelope.brief.indexOf("\n## " + heading + "\n");
  if (start < 0) return "";
  const rest = envelope.brief.slice(start + 1);
  const end = rest.indexOf("\n## ", 1);
  return end < 0 ? rest : rest.slice(0, end);
}

function command(args: string[]): string {
  const result = Bun.spawnSync(args, { cwd: workspace, stdin: "ignore", stdout: "pipe", stderr: "pipe" });
  if (result.exitCode !== 0) throw new Error(args.join(" ") + " failed: " + result.stderr.toString());
  return result.stdout.toString().trim();
}

const mergePart = part("Merge candidate");
if (mergePart) {
  const fence = String.fromCharCode(96).repeat(3);
  const candidateJSON = mergePart.split(fence + "json\n")[1]?.split("\n" + fence)[0];
  if (!candidateJSON) throw new Error("The merge candidate requires its JSON facts.");
  const destination = JSON.parse(candidateJSON).destination_commit;
  if (typeof destination !== "string" || !/^(?:[0-9a-f]{40}|[0-9a-f]{64})$/.test(destination) ||
      command(["git", "rev-parse", "--verify", destination + "^{commit}"]) !== destination) {
    throw new Error("The merge candidate requires an exact destination commit.");
  }
  const merged = Bun.spawnSync(["git", "merge", "--no-commit", "--no-ff", destination],
    { cwd: workspace, stdin: "ignore", stdout: "pipe", stderr: "pipe" });
  const merging = Bun.spawnSync(["git", "rev-parse", "-q", "--verify", "MERGE_HEAD"], { cwd: workspace }).exitCode === 0;
  const unresolved = command(["git", "diff", "--name-only", "--diff-filter=U"]);
  if (merged.exitCode !== 0 && !(merged.exitCode === 1 && merging && unresolved !== "")) {
    throw new Error("The destination merge failed: " + merged.stderr.toString());
  }
  // Preserve the accepted wiki.
  const destinationWiki = Bun.spawnSync(["git", "cat-file", "-e", destination + ":openwiki"], { cwd: workspace }).exitCode === 0;
  if (merging && destinationWiki) {
    command(["git", "restore", "--source", destination, "--staged", "--worktree", "--", "openwiki"]);
  }
  if (command(["git", "diff", "--name-only", "--diff-filter=U"])) {
    throw new Error("The correction left unresolved paths.");
  }
}
writeFileSync(workspace + "/task-" + task + ".txt", "work for task " + task + " by run " + run + "\n");
command(["git", "add", "."]);
command(["git", "commit", "--allow-empty", "-qm", "Complete task " + task]);
command(["mc", "complete", task, "--run", run, "--status", "worked", "--commit", command(["git", "rev-parse", "HEAD"])]);
`

// scribeScript is the default Scribe. It runs the OpenWiki integration at the
// start commit of the wiki update: the first run creates the wiki, and a
// later run updates it. It commits each wiki change and submits the commit
// with the completion receipt. An unchanged wiki gets no commit.
const scribeScript = `const envelope = await Bun.file("/mc/run.json").json();
const workspace = envelope.mounts.workspace;
const run = envelope.run_id;
if (envelope.subject_id !== null) throw new Error("The Scribe run of a wiki update has no task.");

function command(args: string[]): string {
  const result = Bun.spawnSync(args, { cwd: workspace, stdin: "ignore", stdout: "pipe", stderr: "pipe" });
  if (result.exitCode !== 0) throw new Error(args[0] + " failed: " + result.stderr.toString());
  return result.stdout.toString().trim();
}

const result = command([
  "/usr/bin/env", "-i", "PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/tmp/mc-openwiki",
  "/usr/local/bin/node", "--input-type=module", "-e", ` + "`" + `
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { createIntegration, loadUpstream } from "/opt/culler-runtime/app/src/openwiki/integration.mjs";
const root = process.argv[1];
await mkdir(process.env.HOME, { recursive: true });
const provider = await createIntegration({ root, inputCommit: process.argv[2], receiptPath: "/tmp/openwiki-completion.json", upstream: await loadUpstream() });
const call = (name, input) => provider.tools().find((tool) => tool.name === name).handle(input);
const existing = await readFile(root + "/openwiki/.last-update.json").then(() => true).catch(() => false);
const begin = await call("openwiki_begin", { root, mode: existing ? "update" : "init" });
if (begin.status !== "noop") {
  // The first run plans the one page of the fixture. A later run plans no
  // page: OpenWiki then refreshes the navigation pages only.
  await call("openwiki_submit_plan", { runId: begin.runId, pages: existing ? [] : [{
    path: "/openwiki/quickstart.md", title: "Project overview",
    purpose: "Record the project purpose.", seedPaths: ["README.md"],
  }] });
  for (let step = 0; !existing && step < 10; step++) {
    const pending = await call("openwiki_next_page", { runId: begin.runId });
    if (pending.status === "complete") break;
    if (pending.job.path !== "/openwiki/quickstart.md") throw new Error("The fixture received an unexpected wiki page.");
    const line = (await readFile(root + "/README.md", "utf8")).split("\\n")[0].trim();
    const statement = "The project is the " + line + ".";
    await writeFile(root + "/openwiki/quickstart.md", "---\\ntitle: Project overview\\ntype: Guide\\n---\\n\\n# Project overview\\n\\n" + statement + "\\n");
    const current = await call("openwiki_inspect_page_claims", { runId: begin.runId, jobId: pending.job.id });
    await call("openwiki_submit_page", { runId: begin.runId, jobId: pending.job.id,
      retractedClaimIds: current.claims.map((claim) => claim.id),
      claims: [{ statement, evidence: [{ resource: "repo://README.md#L1" }] }],
    });
  }
  const finished = await call("openwiki_finish", { runId: begin.runId });
  if (finished.status !== "complete") throw new Error("The wiki fixture did not complete.");
}
process.stdout.write(JSON.stringify({ receipt: provider.receiptPath }));
` + "`" + `, workspace, envelope.repository.input_commit,
]);
const receipt = JSON.parse(result).receipt;
if (command(["git", "status", "--porcelain"]) !== "") {
  command(["git", "add", "."]);
  command(["git", "commit", "-qm", "Update the Project wiki"]);
}
command(["mc", "complete", "--run", run, "--commit", command(["git", "rev-parse", "HEAD"]), "--wiki-evidence", receipt]);
`

// writeBehaviors materializes the scripted role behaviors. The behaviors
// directory is bind-mounted read-only at /mc/behaviors. The exec steps are
// the agent side of the container invoking the real scoped mc.
func (f *fixture) writeBehaviors() {
	f.t.Helper()
	dir := filepath.Join(f.base, "behaviors")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	behaviors := map[string]string{
		"worker.ts": workerScript,
		"scribe.ts": scribeScript,
		// Arbiter: promote the exact snapshotted pool (single task in the
		// skeleton, so $MC_POOL_IDS is one id).
		"arbiter.json": `{"steps":[
			{"do":"exec","command":"printf '{\"verdicts\":[{\"task\":%s,\"decision\":\"promote\",\"reason\":\"skeleton arbiter pass\"}]}' \"$MC_POOL_IDS\" | mc arbiter decide --run \"$MC_RUN_ID\" --batch -"},
			{"do":"succeed","output":"promoted"}]}`,
		// Worker: sleep 2.5 s (spans heartbeat intervals), then commit in the
		// isolated repository and submit the exact commit.
		"worker.json": `{"steps":[
			{"do":"sleep","seconds":2.5},
			{"do":"exec","command":"bun /mc/behaviors/worker.ts"},
			{"do":"succeed","output":"worked"}]}`,
		// Verifier: verdict on the exact commit it inspected. Evidence goes on
		// the durable artifact plane, in the task's folder.
		"verifier.json": `{"steps":[
			{"do":"exec","command":"set -e; sha=$(git -C /workspace/source rev-parse HEAD); d=` + e2eArtifactPlane + `/$MC_SUBJECT_ID/verification; mkdir -p $d; ev=$d/gate-ladder.txt; printf 'gate ladder: fake pass\\n' > \"$ev\"; mc verifier verdict \"$MC_SUBJECT_ID\" --run \"$MC_RUN_ID\" --outcome pass --evidence \"$ev\" --sha \"$sha\""},
			{"do":"succeed","output":"verified"}]}`,
		// Scribe: the OpenWiki integration at the start commit of the wiki update.
		"scribe.json": `{"steps":[
			{"do":"exec","command":"bun /mc/behaviors/scribe.ts"},
			{"do":"succeed","output":"updated"}]}`,
		// Packager: the render lands on the durable artifact plane. The
		// packaged terminal opens the path it is handed and refuses anything
		// that will not survive the --rm container.
		"packager.json": `{"steps":[
			{"do":"exec","command":"set -e; pd=` + e2eArtifactPlane + `/$MC_SUBJECT_ID/packet; mkdir -p $pd; p=$pd/render.md; printf 'review packet for task %s\\n' \"$MC_SUBJECT_ID\" > \"$p\"; mc complete \"$MC_SUBJECT_ID\" --run \"$MC_RUN_ID\" --status packaged --outputs \"$p\" --thesis 'The contribution satisfies the task criteria.'"},
			{"do":"succeed","output":"packaged"}]}`,
		// Strategist: the liveness terminal, an empty batch.
		"strategist.json": `{"steps":[
			{"do":"exec","command":"printf '{\"proposals\":[]}' | mc strategist propose --run \"$MC_RUN_ID\" --batch -"},
			{"do":"succeed","output":"no proposals"}]}`,
		// Collie: a lease-free conversational turn. The fake harness reads the
		// operator's turn on stdin and answers with this fixed reply.
		"collie.json": `{"steps":[
			{"do":"succeed","output":"collie ack"}]}`,
	}
	for name, body := range behaviors {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *fixture) writeResidentConfig(repoRoot string) {
	f.t.Helper()
	docker, err := exec.LookPath("docker")
	if err != nil {
		f.t.Fatal(err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	dockerPath := filepath.Join(f.base, "bin", "docker-fixture")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = ps ]; then\n" +
		"  shift\n" +
		"  exec " + quote(docker) + " ps --filter " + quote("volume="+f.volume) + " \"$@\"\n" +
		"fi\n" +
		"exec " + quote(docker) + " \"$@\"\n"
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		f.t.Fatal(err)
	}
	// Derived through the same function `mc __onboard supervision` uses, not
	// restated here. Every operator value the resident refuses to start
	// without arrives that way.
	hostConfig, err := verbs.LoadOnboardHostConfig()
	if err != nil {
		f.t.Fatal(err)
	}
	cfg := map[string]any{
		"mcPath":     f.hostMC,
		"dockerPath": dockerPath,
		// Deliberately not the configured tick: this lane is driven by a real
		// timer and waits on it.
		"tickIntervalMs":      500,
		"gitAuthorName":       hostConfig.GitAuthorName,
		"gitAuthorEmail":      hostConfig.GitAuthorEmail,
		"runtimeBindings":     hostConfig.RuntimeBindings,
		"agentsViewBind":      hostConfig.AgentsViewBind,
		"mcHome":              f.home,
		"releaseBuildId":      testReleaseID,
		"configSchemaVersion": 1,
		"image":               image,
		"projectImage":        projectImage,
		"runtimeVolume":       f.runtimeVolume,
		"agentCmd":            []string{"/opt/culler-runtime/bin/bun", "/opt/culler-runtime/app/src/agent-runner/main.ts"},
		"collieCmd":           []string{"/opt/culler-runtime/bin/bun", "/opt/culler-runtime/app/src/collie-runner/main.ts"},
		"landCmd":             []string{"mc-land"},
		"behaviorsDir":        filepath.Join(f.base, "behaviors"),
		"runnerSrcDir":        filepath.Join(repoRoot, "runner"),
		"workspaceRoot":       f.ws,
		"spineVolume":         f.volume,
		"spineDbPath":         spineDBPath,
		"roleBehaviors": map[string]string{
			"arbiter":             "/mc/behaviors/arbiter.json",
			"worker":              "/mc/behaviors/worker.json",
			"verifier":            "/mc/behaviors/verifier.json",
			"scribe":              "/mc/behaviors/scribe.json",
			"packager":            "/mc/behaviors/packager.json",
			"strategist(propose)": "/mc/behaviors/strategist.json",
			"collie":              "/mc/behaviors/collie.json",
		},
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(f.residentConfig()), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(f.residentConfig(), b, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// residentConfig is the production path of the resident configuration. The
// host mc reads the release and MC_HOME of the running services from it
// before each operator change.
func (f *fixture) residentConfig() string {
	return filepath.Join(f.home, "supervision", "resident.json")
}

// startResident launches the real resident on the real timer; the test only
// observes from here.
func (f *fixture) startResident() {
	f.t.Helper()
	logPath := filepath.Join(f.base, "resident.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		f.t.Fatal(err)
	}
	f.resLog = logFile

	cmd := exec.Command("mise", "exec", "--", "bun", "src/main.ts", "--config", f.residentConfig())
	cmd.Dir = filepath.Join(mustAbs(f.t, "../.."), "resident")
	cmd.Env = f.env
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		f.t.Fatalf("start resident: %v", err)
	}
	f.resProc = cmd
	f.t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		logFile.Close()
		if f.t.Failed() {
			// A stalled resident emits an idle tick twice a second, so a raw
			// tail is 8000 characters of "tick: idle" and the line that
			// explains the stall has long scrolled off. Show the decisions.
			f.t.Logf("resident log (idle ticks elided):\n%s", tail(residentDecisions(f.residentLog()), 8000))
		}
	})
	f.waitFor(90*time.Second, "resident dispatch after its image checks", func() (bool, string) {
		if !strings.Contains(f.residentLog(), "started: tick every") {
			return false, "resident dispatch did not start yet"
		}
		return true, ""
	})
}

// ───────────────────────────── helpers ──────────────────────────────────

type mcResult struct {
	code   int
	stdout string
	stderr string
}

func (f *fixture) mcRun(stdin string, args ...string) mcResult {
	f.t.Helper()
	cmd := exec.Command(f.hostMC, args...)
	cmd.Env = f.env
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	res := mcResult{stdout: out.String(), stderr: errb.String()}
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			res.code = exit.ExitCode()
		} else {
			f.t.Fatalf("run mc %v: %v", args, err)
		}
	}
	return res
}

func (f *fixture) mcOK(stdin string, args ...string) map[string]any {
	f.t.Helper()
	res := f.mcRun(stdin, args...)
	if res.code != 0 {
		f.t.Fatalf("mc %v exited %d: %s", args, res.code, res.stderr)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &m); err != nil {
		f.t.Fatalf("mc %v: bad JSON %q: %v", args, res.stdout, err)
	}
	return m
}

// runs returns each run that the pipeline of the test started. The seed step
// records a current Project wiki as one ended Scribe run, and that row is no
// run of the pipeline.
func (f *fixture) runs() []map[string]any {
	f.t.Helper()
	raw := f.mcOK("", "run", "list")["runs"].([]any)
	rows := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if row := r.(map[string]any); f.seedRun == "" || row["id"] != f.seedRun {
			rows = append(rows, row)
		}
	}
	return rows
}

// agentRuns returns the runs of one role that a harness executed.
// Repository operations run as mechanical runs without a lease, so a count of
// agent runs excludes them.
func (f *fixture) agentRuns(role string) []map[string]any {
	f.t.Helper()
	var rows []map[string]any
	for _, r := range f.runs() {
		if r["role"] == role && r["binding"] != "mechanical" {
			rows = append(rows, r)
		}
	}
	return rows
}

// leases returns every `mc lease list` row: one per Project, in
// Project id order, each with "status" ("held" | "free") beside the lease
// columns (null on a free lease).
func (f *fixture) leases() []map[string]any {
	f.t.Helper()
	raw := f.mcOK("", "lease", "list")["leases"].([]any)
	rows := make([]map[string]any, len(raw))
	for i, r := range raw {
		rows[i] = r.(map[string]any)
	}
	return rows
}

// lease returns the lease row of one Project. The spine carries a row for
// every Project it knows, so a missing row is a fixture error.
func (f *fixture) lease(ws string) map[string]any {
	f.t.Helper()
	for _, row := range f.leases() {
		if row["project"] == ws {
			return row
		}
	}
	f.t.Fatalf("no lease row for Project %q in %v", ws, f.leases())
	return nil
}

func (f *fixture) runRow(runID string) map[string]any {
	f.t.Helper()
	for _, r := range f.runs() {
		if r["id"] == runID {
			return r
		}
	}
	return nil
}

func (f *fixture) packets() []map[string]any {
	f.t.Helper()
	raw := f.mcOK("", "packet", "list")["packets"].([]any)
	rows := make([]map[string]any, len(raw))
	for i, r := range raw {
		rows[i] = r.(map[string]any)
	}
	return rows
}

// wikiState returns the wiki state row of the Project.
func (f *fixture) wikiState() map[string]any {
	f.t.Helper()
	for _, raw := range f.mcOK("", "wiki-state", "list")["wiki_states"].([]any) {
		if row := raw.(map[string]any); row["project"] == project {
			return row
		}
	}
	f.t.Fatalf("no wiki state row for Project %q", project)
	return nil
}

// waitForCurrentWiki waits until the wiki update of the run has an end and
// the Project wiki is current. It returns the record of the wiki update. A
// failed wiki update fails the test with its cause.
func (f *fixture) waitForCurrentWiki(run string, d time.Duration) map[string]any {
	f.t.Helper()
	var last map[string]any
	f.waitFor(d, "the wiki update of run "+run+" leaves the Project wiki current", func() (bool, string) {
		state := f.wikiState()
		last, _ = state["last"].(map[string]any)
		if last == nil || last["run_id"] != run {
			active, _ := state["active"].(map[string]any)
			return false, fmt.Sprintf("state=%v step=%v", state["state"], active["step"])
		}
		if last["result"] != "succeeded" {
			f.t.Fatalf("the wiki update of run %s has the result %v: %v", run, last["result"], last["cause_text"])
		}
		return state["state"] == "current", fmt.Sprintf("state=%v accepted_commit=%v", state["state"], state["accepted_commit"])
	})
	return last
}

// outageNote marks an observation a condition could not make, as opposed to one
// it made and found false. A condition opts in by prefixing its note with it.
//
// `docker inspect` returning "No such object" is an answer: the container is not
// there yet, and that time is exactly what the budget is for. `docker inspect`
// returning a 500, or timing out against a degraded daemon, is not an answer
// about Culler at all. Charging it to the budget converts a recoverable host
// hiccup into a test failure that reads like a product defect.
const outageNote = "environment unavailable: "

func (f *fixture) waitFor(d time.Duration, desc string, cond func() (bool, string)) {
	f.t.Helper()
	deadline := time.Now().Add(d)
	last := "(no observation)"
	// Absorbed outage is capped at the original budget, so a daemon that is sick
	// for good still fails in at most 2d, never forever.
	var absorbed time.Duration
	for time.Now().Before(deadline) {
		started := time.Now()
		ok, note := cond()
		if ok {
			return
		}
		if note != "" {
			last = note
		}
		if strings.HasPrefix(note, outageNote) {
			if spent := time.Since(started); absorbed+spent <= d {
				absorbed += spent
				deadline = deadline.Add(spent)
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if absorbed > 0 {
		f.t.Fatalf("timed out (%s budget, %s of it absorbed as environment outage) waiting for %s; last: %s",
			d, absorbed.Round(time.Millisecond), desc, last)
	}
	f.t.Fatalf("timed out (%s) waiting for %s; last: %s", d, desc, last)
}

// waitForAgentRun polls the run list until a harness run of the role exists
// and returns its run id.
func (f *fixture) waitForAgentRun(role string, d time.Duration) string {
	f.t.Helper()
	var runID string
	f.waitFor(d, "a "+role+" run", func() (bool, string) {
		rows := f.agentRuns(role)
		if len(rows) == 0 {
			return false, "no " + role + " run yet"
		}
		runID = rows[0]["id"].(string)
		return true, ""
	})
	return runID
}

func (f *fixture) waitForTaskStatus(taskID int64, status string, d time.Duration) {
	f.t.Helper()
	f.waitFor(d, fmt.Sprintf("task %d status %s", taskID, status), func() (bool, string) {
		task := f.mcOK("", "task", "get", fmt.Sprint(taskID))
		if task["status"] != status {
			return false, fmt.Sprintf("status=%v blocked=%v (%v)", task["status"], task["blocked"], task["blocked_reason"])
		}
		return true, ""
	})
}

// waitForArchived polls the task until the apply operation archived it.
func (f *fixture) waitForArchived(taskID int64, d time.Duration) {
	f.t.Helper()
	f.waitFor(d, fmt.Sprintf("task %d archived", taskID), func() (bool, string) {
		task := f.mcOK("", "task", "get", fmt.Sprint(taskID))
		if task["archived"].(float64) != 1 {
			return false, fmt.Sprintf("status=%v archived=%v blocked=%v (%v)", task["status"], task["archived"], task["blocked"], task["blocked_reason"])
		}
		return true, ""
	})
}

// livePacket returns the unarchived packet of the task, or nil.
func (f *fixture) livePacket(taskID int64) map[string]any {
	f.t.Helper()
	for _, p := range f.packets() {
		if int64(p["task_id"].(float64)) == taskID && p["archived"].(float64) == 0 && p["superseded"].(float64) == 0 {
			return p
		}
	}
	return nil
}

// readyPacket returns the live packet of the task when its candidate is
// checked and the operator can decide it.
func (f *fixture) readyPacket(taskID int64) map[string]any {
	f.t.Helper()
	// `ready` is NULL while the task has no candidate.
	if p := f.livePacket(taskID); p != nil {
		if ready, _ := p["ready"].(float64); ready == 1 {
			return p
		}
	}
	return nil
}

// waitForReadyPacket polls the packet list until the task has a live packet
// with a merged candidate.
func (f *fixture) waitForReadyPacket(taskID int64, d time.Duration) map[string]any {
	f.t.Helper()
	var packet map[string]any
	f.waitFor(d, fmt.Sprintf("a ready packet for task %d", taskID), func() (bool, string) {
		packet = f.readyPacket(taskID)
		if packet == nil {
			task := f.mcOK("", "task", "get", fmt.Sprint(taskID))
			return false, fmt.Sprintf("status=%v blocked=%v (%v) packets=%d", task["status"], task["blocked"], task["blocked_reason"], len(f.packets()))
		}
		return true, ""
	})
	return packet
}

// approve records the operator approval of the reviewed packet version.
func (f *fixture) approve(taskID int64, packet map[string]any) {
	f.t.Helper()
	packetID := int64(packet["packet_id"].(float64))
	versionID := int64(packet["version_id"].(float64))
	if got := f.mcRun("", "packet", "decide", fmt.Sprint(taskID), "--approve",
		"--packet", fmt.Sprint(packetID), "--version", fmt.Sprint(versionID)); got.code != 0 {
		f.t.Fatalf("mc packet decide --approve for task %d exited %d: %s", taskID, got.code, got.stderr)
	}
}

// retainedVersion returns the commit the capture operation retained for the run.
func (f *fixture) retainedVersion(taskID int64, run string) string {
	f.t.Helper()
	return f.git("rev-parse", fmt.Sprintf("refs/mc/tasks/%d/versions/%s", taskID, run))
}

// isAncestor reports whether the first commit is an ancestor of the second in the Project repository.
func (f *fixture) isAncestor(ancestor, commit string) bool {
	f.t.Helper()
	_, err := f.gitErr("merge-base", "--is-ancestor", ancestor, commit)
	return err == nil
}

func (f *fixture) docker(args ...string) {
	f.t.Helper()
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		f.t.Fatalf("docker %v: %v\n%s", args, err, out)
	}
}

func (f *fixture) dockerOutput(args ...string) string {
	f.t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		f.t.Fatalf("docker %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (f *fixture) git(args ...string) string {
	f.t.Helper()
	out, err := f.gitErr(args...)
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return out
}

func (f *fixture) gitErr(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = f.ws
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// residentLog is everything the resident said, from both of its channels.
//
// The resident writes its records to a file it opens and owns,
// $MC_HOME/logs/resident.ndjson, because the launchd redirect it runs behind in
// production cannot be rotated. This fixture still captures the process's
// stdout/stderr, which carries only what the logger cannot: pre-logger boot
// validation, uncaught exceptions, and runtime aborts.
//
// The records are rendered to text through the same renderer `mc logs` uses, so
// a `Contains("spawn refused: …")` written against the prose still matches.
func (f *fixture) residentLog() string {
	captured, _ := os.ReadFile(filepath.Join(f.base, "resident.log"))
	records, _ := os.ReadFile(filepath.Join(f.home, "logs", "resident.ndjson"))
	rendered := []string{}
	for _, line := range strings.Split(string(records), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if text, ok := mclog.RenderText([]byte(line)); ok {
			rendered = append(rendered, text)
			continue
		}
		rendered = append(rendered, line)
	}
	return string(captured) + strings.Join(rendered, "\n") + "\n"
}

// residentDecisions drops the resident's idle-tick heartbeat, keeping only the
// lines where it decided something. A tick that fails the same way every 500ms
// repeats its line thousands of times, so consecutive repeats of the same
// message collapse too. The timestamp prefix is stripped for that comparison.
func residentDecisions(s string) string {
	var kept []string
	elided, prev, repeats := 0, "", 0
	flush := func() {
		if repeats > 1 {
			kept = append(kept, fmt.Sprintf("        … previous line repeated %d more times", repeats-1))
		}
	}
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "tick: idle") || strings.Contains(line, "tick.skipped") {
			elided++
			continue
		}
		msg := line
		if i := strings.Index(line, "] "); i >= 0 {
			msg = line[i+2:]
		}
		if msg == prev && msg != "" {
			repeats++
			continue
		}
		flush()
		kept = append(kept, line)
		prev, repeats = msg, 1
	}
	flush()
	return fmt.Sprintf("(%d idle ticks elided)\n%s", elided, strings.Join(kept, "\n"))
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
