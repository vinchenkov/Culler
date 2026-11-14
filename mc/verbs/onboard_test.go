package verbs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"mc/boundary"
	operatorconfig "mc/config"
	"mc/routing"
	"mc/substrate"
)

// A `version = 1` allowlist with zero `[[allow]]` entries is a valid deny-all
// policy, and onboarding `home` creates it before the first Project exists.
// The judge here is the real consumer, the first two calls of planMounts,
// not a restatement of the bytes onboarding writes.
func TestOnboardHomeCreatesTheDenyAllMountAllowlist(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	spine := filepath.Join(home, "spine.db")
	t.Setenv("MC_HOME", home)

	if _, _, err := onboardHome(spine, true); err != nil {
		t.Fatalf("onboard home: %v", err)
	}

	path := MountAllowlistPath(home)
	if err := boundary.TrustPolicyFile(path, os.Getuid()); err != nil {
		t.Fatalf("dispatch would refuse the deployment onboarding just built: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	allowlist, err := boundary.ParseMountAllowlist(data)
	if err != nil {
		t.Fatalf("onboarding wrote an allowlist the parser rejects: %v", err)
	}
	// Deny-all grants nothing: the Project's own roots reach a container as
	// TYPED roots (task-root, task-source, task-git), which bypass allowlist
	// entries. An entry here would be an unasked-for grant.
	if entries := allowlist.Entries(); len(entries) != 0 {
		t.Fatalf("onboarding authored %d allow entries; deny-all must grant nothing: %+v",
			len(entries), entries)
	}
}

// The file is operator policy the moment it exists. Re-running onboarding is
// routine, since every doctor failure names the section that repairs it, and
// it must never silently revert an operator's grants to deny-all.
func TestOnboardHomeNeverOverwritesAnExistingMountAllowlist(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	spine := filepath.Join(home, "spine.db")
	t.Setenv("MC_HOME", home)

	if _, _, err := onboardHome(spine, true); err != nil {
		t.Fatalf("onboard home: %v", err)
	}
	path := MountAllowlistPath(home)
	operator := "version = 1\n\n[[allow]]\npath = \"" + t.TempDir() +
		"\"\ntarget = \"reference/library\"\naccess = \"ro\"\n"
	if err := os.WriteFile(path, []byte(operator), 0o600); err != nil {
		t.Fatal(err)
	}

	status, _, err := onboardHome(spine, true)
	if err != nil {
		t.Fatalf("re-run onboard home: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != operator {
		t.Fatalf("re-running onboarding rewrote the operator's mount policy:\n%s", after)
	}
	// An unchanged deployment reports `ok`; `done` means this run changed
	// something, and nothing changed.
	if status != "ok" {
		t.Fatalf("re-run status = %q, want ok — nothing needed changing", status)
	}
}

// Smoke task 2 reached `packaged` and the packet it produced does not exist:
// the Packager rendered into container-local `/tmp/claude/` because no
// Project had an artifact root, `onboard.go` wrote every profile with an
// empty `artifact_roots`, and nothing ever populated it. The `--rm` container
// took the review artifact the whole pipeline exists to produce with it.
//
// Judged by the REAL consumer, which is the only thing that has ever caught
// this class: the profile row alone proves nothing, because an artifact source
// reaches a container as an ALLOWLISTED mount, so a root the
// allowlist does not carry refuses at dispatch. Both halves, or neither.
func TestOnboardProjectAuthorizesItsArtifactPlaneForDispatch(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	spine := filepath.Join(home, "spine.db")
	root := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("MC_HOME", home)
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	writeVisionFixture(t, root)
	if _, err := gitAtSource(root, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := onboardHome(spine, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := onboardProject(OnboardArgs{
		Spine: spine, Project: "primary", WorkspaceRoot: root,
		AcceptGeneratedSandbox: true,
	}); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	artifactRoot := ProjectArtifactRoot(canonicalRoot)

	// Half one: the directory exists, is operator-owned and mode 0700, and is
	// kept out of the operator's `git status`.
	info, err := os.Lstat(artifactRoot)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("artifact root %q = %v (err %v), want a mode-0700 directory", artifactRoot, info, err)
	}
	exclude, err := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if !hasExcludeLine(exclude, ".mc-artifacts/") {
		t.Fatalf("`.mc-artifacts/` is not excluded; the operator's git status now shows every packet:\n%s", exclude)
	}

	// Half two: the allowlist authorizes it, and the DISPATCH PLANNER — not the
	// allowlist's own parser — is what says so.
	allowlistPath := MountAllowlistPath(home)
	if err := boundary.TrustPolicyFile(allowlistPath, os.Getuid()); err != nil {
		t.Fatalf("dispatch would refuse the allowlist onboarding just extended: %v", err)
	}
	body, err := os.ReadFile(allowlistPath)
	if err != nil {
		t.Fatal(err)
	}
	allowlist, err := boundary.ParseMountAllowlist(body)
	if err != nil {
		t.Fatalf("onboarding broke the mount allowlist: %v", err)
	}
	resolved, err := boundary.ResolveAllowlist(allowlist)
	if err != nil {
		t.Fatalf("ResolveAllowlist: %v", err)
	}
	auth, err := resolved.Authorize(artifactRoot, boundary.AccessRW,
		boundary.BlockPolicy{}, boundary.Jurisdiction{})
	// A zero Jurisdiction fails closed by construction, so the jurisdiction arm
	// is exercised end-to-end in the mount-attest suite instead; here the claim
	// is narrower and exact — the allowlist grants this root RW at this target.
	if err == nil && (auth.Target != "primary" || auth.Access != boundary.AccessRW) {
		t.Fatalf("artifact grant = %+v, want target primary rw", auth)
	}
	found := false
	for _, entry := range allowlist.Entries() {
		if entry.Path == artifactRoot {
			found = true
			if entry.Target != "primary" || entry.Maximum != boundary.AccessRW {
				t.Fatalf("artifact allow entry = %+v, want target primary max rw", entry)
			}
		}
	}
	if !found {
		t.Fatalf("no allow entry for %q; the profile names an artifact root dispatch cannot bind: %+v",
			artifactRoot, allowlist.Entries())
	}

	// Re-running the section is routine and must stay silent rather than
	// appending a duplicate grant every time.
	if _, _, err := onboardProject(OnboardArgs{
		Spine: spine, Project: "primary", WorkspaceRoot: root,
		AcceptGeneratedSandbox: true,
	}); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(allowlistPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(body) {
		t.Fatalf("re-running the section changed the allowlist:\n%s", again)
	}
}

func TestOnboardHomeProvisioningIsAtomicAndRetryable(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	spine := filepath.Join(home, "spine.db")
	t.Setenv("MC_HOME", home)

	originalSchema := substrate.Schema
	t.Cleanup(func() { substrate.Schema = originalSchema })
	substrate.Schema = `
		CREATE TABLE partial_provision (id INTEGER PRIMARY KEY);
		INSERT INTO missing_table VALUES (1);
	`
	if _, _, err := onboardHome(spine, true); err == nil {
		t.Fatal("injected schema failure unexpectedly succeeded")
	}

	if inspection, err := inspectSpineReadOnly(spine); err != nil || inspection.tables != 0 {
		t.Fatalf("failed provision left schema objects: %+v %v", inspection, err)
	}
	if _, err := os.Stat(filepath.Join(home, "deployment.uuid")); !os.IsNotExist(err) {
		t.Fatalf("failed provisioning left a UUID mirror: %v", err)
	}

	substrate.Schema = originalSchema
	status, _, err := onboardHome(spine, true)
	if err != nil {
		t.Fatalf("retry after rolled-back provisioning: %v", err)
	}
	if status != "done" {
		t.Fatalf("retry status = %q, want done", status)
	}
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var uuid string
	if err := db.QueryRow(`SELECT deployment_uuid FROM meta WHERE id = 1`).Scan(&uuid); err != nil {
		t.Fatal(err)
	}
	if uuid == "" {
		t.Fatal("retry did not create the deployment identity")
	}
}

func TestProvisioningStampsTheSchemaItApplied(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	spine := filepath.Join(home, "spine.db")
	t.Setenv("MC_HOME", home)
	if _, _, err := onboardHome(spine, true); err != nil {
		t.Fatal(err)
	}
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := substrate.CheckSchema(db); err != nil {
		t.Fatal(err)
	}
}

func TestOnboardConcurrentReplaysSerialize(t *testing.T) {
	home, _ := onboardConfigFixture(t)
	if _, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), enableTestBindings); err != nil {
		t.Fatal(err)
	}
	spine := filepath.Join(home, "spine.db")
	if _, _, err := onboardHome(spine, true); err != nil {
		t.Fatal(err)
	}
	const bindings = "chatgpt,claude,endpoint"
	if _, err := EnsureOnboardRuntimeCatalog(); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordOnboardRuntimeBindings(bindings); err != nil {
		t.Fatal(err)
	}

	runPair := func(t *testing.T, fn func() (string, error)) []string {
		t.Helper()
		statuses := make([]string, 2)
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range 2 {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				statuses[i], errs[i] = fn()
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("concurrent replay %d: %v", i, err)
			}
		}
		return statuses
	}
	wantDoneOK := func(t *testing.T, got []string) {
		t.Helper()
		counts := map[string]int{}
		for _, status := range got {
			counts[status]++
		}
		if counts["done"] != 1 || counts["ok"] != 1 {
			t.Fatalf("concurrent statuses = %v, want one done and one ok", got)
		}
	}

	routingArgs := OnboardArgs{AcceptDefaultRouting: true, RuntimeBindings: bindings}
	wantDoneOK(t, runPair(t, func() (string, error) {
		status, _, err := onboardRouting(routingArgs)
		return status, err
	}))
	configPath := filepath.Join(home, "config.toml")
	cfg, err := operatorconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.Runtimes.EnabledBindings, ","); got != bindings {
		t.Fatalf("The enabled bindings are %q, but the test requires %q.", got, bindings)
	}
	routingPath := filepath.Join(home, "routing.md")
	written, err := os.ReadFile(routingPath)
	if err != nil {
		t.Fatal(err)
	}
	table, err := routing.Parse(written, routing.RegistryFromRuntimes(cfg.Runtimes))
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range table.Rows() {
		if route.Harness != "codex" || route.Binding != "chatgpt" {
			t.Fatalf("The default route is %+v. The test requires codex/chatgpt.", route)
		}
	}
	configBefore, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if status, _, err := onboardRouting(routingArgs); err != nil || status != "ok" {
		t.Fatalf("Routing replay returned status %q and error %v. The test requires ok.", status, err)
	}
	for path, before := range map[string][]byte{routingPath: written, configPath: configBefore} {
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatalf("Routing replay changed %s.", filepath.Base(path))
		}
	}
	wantDoneOK(t, runPair(t, func() (string, error) {
		status, _, err := onboardTunables(OnboardArgs{Spine: spine, TimeoutMinutes: 7})
		return status, err
	}))
	surfacesDB, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := surfacesDB.Exec(`DELETE FROM alert_routes WHERE class = 'health'`); err != nil {
		t.Fatal(err)
	}
	if err := surfacesDB.Close(); err != nil {
		t.Fatal(err)
	}
	wantDoneOK(t, runPair(t, func() (string, error) {
		status, _, err := onboardSurfaces(OnboardArgs{Spine: spine})
		return status, err
	}))

	workspaces := []string{filepath.Join(t.TempDir(), "one"), filepath.Join(t.TempDir(), "two")}
	for _, root := range workspaces {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		writeVisionFixture(t, root)
		if _, err := gitAtSource(root, "init", "--quiet"); err != nil {
			t.Fatal(err)
		}
	}
	statuses := make([]string, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i], _, errs[i] = onboardProject(OnboardArgs{
				Spine: spine, Project: []string{"ws-one", "ws-two"}[i], WorkspaceRoot: workspaces[i],
				AcceptGeneratedSandbox: true,
			})
		}(i)
	}
	wg.Wait()
	// Two Projects register concurrently; each gets its own row and profile.
	for i := range 2 {
		if errs[i] != nil || statuses[i] != "done" {
			t.Fatalf("concurrent Projects = statuses %v errors %v", statuses, errs)
		}
	}
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count, profiles int
	if err := db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT sandbox_profile) FROM projects`).Scan(&count, &profiles); err != nil {
		t.Fatal(err)
	}
	if count != 2 || profiles != 2 {
		t.Fatalf("concurrent Projects left %d rows with %d profiles", count, profiles)
	}
}

func TestOnboardRoutingRequiresAnExplicitValidChoice(t *testing.T) {
	home, _ := onboardConfigFixture(t)

	if _, _, err := onboardRouting(OnboardArgs{}); err == nil ||
		!strings.Contains(err.Error(), "accept-default-routing") {
		t.Fatalf("missing routing choice error = %v", err)
	}
	if _, _, err := onboardRouting(OnboardArgs{
		AcceptDefaultRouting: true,
		RuntimeBindings:      "missing",
	}); err == nil || !strings.Contains(err.Error(), `--runtime-bindings names "missing"`) {
		t.Fatalf("default routing accepted an unknown binding: %v", err)
	}
	status, _, err := onboardRouting(OnboardArgs{
		AcceptDefaultRouting: true,
		RuntimeBindings:      "chatgpt",
	})
	if err != nil || status != "done" {
		t.Fatalf("default routing on one binding = status %q err %v", status, err)
	}
	written, err := os.ReadFile(filepath.Join(home, "routing.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(written), "\n") {
		if !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "| role") || strings.HasPrefix(line, "| ---") {
			continue
		}
		if !strings.HasSuffix(line, "| codex | chatgpt |") {
			t.Fatalf("default routing row does not map to the first enabled binding: %q", line)
		}
	}
}

func TestOnboardTunablesStoresReviewWIPCap(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	spine := filepath.Join(home, "spine.db")
	t.Setenv("MC_HOME", home)
	if _, _, err := onboardHome(spine, true); err != nil {
		t.Fatal(err)
	}

	status, _, err := onboardTunables(OnboardArgs{
		Spine: spine, ReviewWIPCap: 7,
	})
	if err != nil || status != "done" {
		t.Fatalf("review cap onboard = status %q err %v", status, err)
	}
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var got int
	if err := db.QueryRow(`SELECT review_wip_cap FROM settings WHERE id = 1`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != 7 {
		t.Fatalf("review_wip_cap = %d, want 7", got)
	}
}

// The first Project section stores the vision state of the workspace and
// repairs a stale stored state on replay.
func TestOnboardProjectPersistsVisionStateAndAdvisory(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	spine := filepath.Join(home, "spine.db")
	root := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("MC_HOME", home)
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	writeVisionFixture(t, root)
	if _, err := gitAtSource(root, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := onboardHome(spine, true); err != nil {
		t.Fatal(err)
	}
	args := OnboardArgs{
		Spine: spine, Project: "primary", WorkspaceRoot: root,
		AcceptGeneratedSandbox: true,
	}
	prepared, err := prepareOnboardProject(args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prepared.ProjectSandboxProfile, `"git_identity_override":null`) {
		t.Fatalf("canonical inherited profile omitted its required nullable decision: %s",
			prepared.ProjectSandboxProfile)
	}
	status, detail, err := onboardProject(prepared)
	if err != nil || status != "done" || !strings.Contains(detail, projectSecretsAdvisory) {
		t.Fatalf("first Project = status %q detail %q err %v", status, detail, err)
	}
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var vision string
	if err := db.QueryRow(`SELECT vision FROM projects WHERE id = 'primary'`).Scan(&vision); err != nil {
		t.Fatal(err)
	}
	if vision != ProjectVisionPresent {
		t.Fatalf("stored vision = %q", vision)
	}
	if status, _, err := onboardProject(args); err != nil || status != "ok" {
		t.Fatalf("exact replay = status %q err %v", status, err)
	}
	if _, err := db.Exec(`UPDATE projects SET vision = ? WHERE id = 'primary'`, ProjectVisionUnknown); err != nil {
		t.Fatal(err)
	}
	if status, _, err := onboardProject(args); err != nil || status != "done" {
		t.Fatalf("vision repair = status %q err %v", status, err)
	}
	if err := db.QueryRow(`SELECT vision FROM projects WHERE id = 'primary'`).Scan(&vision); err != nil {
		t.Fatal(err)
	}
	if vision != ProjectVisionPresent {
		t.Fatalf("replayed vision = %q", vision)
	}
	if status, _, err := onboardProject(args); err != nil || status != "ok" {
		t.Fatalf("repaired replay = status %q err %v", status, err)
	}
}

// A later Project registers through the same section as the first one. The
// row is born with its own profile, its title, and its pause state.
func TestOnboardProjectRegistersALaterProjectWithItsOwnProfile(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	spine := filepath.Join(home, "spine.db")
	first := filepath.Join(t.TempDir(), "primary")
	later := filepath.Join(t.TempDir(), "later")
	t.Setenv("MC_HOME", home)
	for _, dir := range []string{first, later} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		writeVisionFixture(t, dir)
		if _, err := gitAtSource(dir, "init", "--quiet"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := onboardHome(spine, true); err != nil {
		t.Fatal(err)
	}
	primary := OnboardArgs{
		Spine: spine, Project: "primary", WorkspaceRoot: first,
		AcceptGeneratedSandbox: true,
	}
	prepared, err := prepareOnboardProject(primary)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := onboardProject(prepared); err != nil {
		t.Fatal(err)
	}

	second := OnboardArgs{
		Spine: spine, Project: "added-later", WorkspaceRoot: later,
		AcceptGeneratedSandbox: true, ProjectTitle: " Added later ", ProjectPaused: true,
	}
	preparedLater, err := prepareOnboardProject(second)
	if err != nil {
		t.Fatal(err)
	}
	status, detail, err := onboardProject(preparedLater)
	if err != nil {
		t.Fatalf("registering a later Project refused: %v", err)
	}
	if status != "done" {
		t.Fatalf("status = %q, want done", status)
	}
	if !strings.Contains(detail, projectSecretsAdvisory) {
		t.Fatalf("registration dropped the secrets advisory: %s", detail)
	}
	if !strings.Contains(detail, preparedLater.WorkspaceRoot) {
		t.Fatalf("registration does not name the workspace it bound: %s", detail)
	}

	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var boundProfile, boundRoot, title, projectStatus, vision, primaryProfile string
	if err := db.QueryRow(`
		SELECT w.sandbox_profile, p.workspace_root, w.title, w.status, w.vision FROM projects w
		JOIN sandbox_profiles p ON p.id = w.sandbox_profile WHERE w.id = 'added-later'`).
		Scan(&boundProfile, &boundRoot, &title, &projectStatus, &vision); err != nil {
		t.Fatalf("the registered row does not join to a profile: %v", err)
	}
	if boundRoot != preparedLater.WorkspaceRoot {
		t.Fatalf("bound workspace_root = %q, want %q", boundRoot, preparedLater.WorkspaceRoot)
	}
	if title != "Added later" || projectStatus != "paused" || vision != ProjectVisionPresent {
		t.Fatalf("registered row = title %q status %q vision %q", title, projectStatus, vision)
	}
	if err := db.QueryRow(`SELECT sandbox_profile FROM projects WHERE id = 'primary'`).
		Scan(&primaryProfile); err != nil {
		t.Fatal(err)
	}
	if boundProfile == primaryProfile {
		t.Fatal("the later Project shares the first one's sandbox profile")
	}

	// A second run compares the stored posture and changes nothing.
	if status, _, err := onboardProject(preparedLater); err != nil || status != "ok" {
		t.Fatalf("re-running the section over a registered row = status %q err %v", status, err)
	}
	if err := db.QueryRow(`SELECT status FROM projects WHERE id = 'added-later'`).Scan(&projectStatus); err != nil {
		t.Fatal(err)
	}
	if projectStatus != "paused" {
		t.Fatalf("the second run changed the status to %q", projectStatus)
	}
}

// The title is one line of bounded length; a title without a Project is a
// usage error.
func TestOnboardProjectTitleIsBounded(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := prepareOnboardProject(OnboardArgs{ProjectTitle: "Orphan title"})
	if err == nil || !strings.Contains(err.Error(), "--project and --workspace-root") {
		t.Fatalf("a title without a Project must be a usage error, got %v", err)
	}
	long := OnboardArgs{
		Project: "p", WorkspaceRoot: root, AcceptGeneratedSandbox: true,
		ProjectTitle: strings.Repeat("x", MaxProjectTitleBytes+1),
	}
	if _, err := prepareOnboardProject(long); err == nil || !strings.Contains(err.Error(), "--title") {
		t.Fatalf("an oversized title must be refused, got %v", err)
	}
	control := long
	control.ProjectTitle = "two\nlines"
	if _, err := prepareOnboardProject(control); err == nil || !strings.Contains(err.Error(), "--title") {
		t.Fatalf("a control character in the title must be refused, got %v", err)
	}
}

func TestOnboardProjectRequiresReviewedSandboxBeforeAnyWrite(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	spine := filepath.Join(home, "spine.db")
	root := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("MC_HOME", home)
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAtSource(root, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := onboardHome(spine, true); err != nil {
		t.Fatal(err)
	}

	_, _, err := onboardProject(OnboardArgs{
		Spine: spine, Project: "primary", WorkspaceRoot: root,
	})
	if err == nil || !strings.Contains(err.Error(), "accept-generated-sandbox") {
		t.Fatalf("missing sandbox review error = %v", err)
	}
	db, openErr := substrate.Open(spine)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer db.Close()
	var projects int
	if scanErr := db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&projects); scanErr != nil {
		t.Fatal(scanErr)
	}
	if projects != 0 {
		t.Fatalf("unreviewed sandbox wrote %d Project rows", projects)
	}
	if _, statErr := os.Stat(ProjectArtifactRoot(root)); !os.IsNotExist(statErr) {
		t.Fatalf("unreviewed sandbox changed the workspace: %v", statErr)
	}
}

func TestOnboardProjectExistingRepoSetsRelativeWorktreesAndMinimalProfile(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	spine := filepath.Join(home, "spine.db")
	root := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("MC_HOME", home)
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	writeVisionFixture(t, root)
	if _, err := gitAtSource(root, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := onboardHome(spine, true); err != nil {
		t.Fatal(err)
	}

	status, _, err := onboardProject(OnboardArgs{
		Spine: spine, Project: "primary", WorkspaceRoot: root,
		AcceptGeneratedSandbox: true,
		ProjectGitAuthorName:   "Primary Bot", ProjectGitAuthorEmail: "primary@example.test",
	})
	if err != nil || status != "done" {
		t.Fatalf("existing repo = status %q err %v", status, err)
	}
	relative, err := gitAtSource(root, "config", "--local", "--get", "worktree.useRelativePaths")
	if err != nil || strings.TrimSpace(string(relative)) != "true" {
		t.Fatalf("worktree.useRelativePaths = %q, %v", relative, err)
	}
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var profile, artifacts, denied, toolHome, runtimeControl string
	var harnessEnv, toolEnv, toolAllowlist, authDelivery, gitName, gitEmail string
	if err := db.QueryRow(`
		SELECT w.sandbox_profile, p.artifact_roots, p.denied_paths,
		       p.tool_home_dir, p.runtime_control_dir, p.harness_env_policy, p.tool_env_policy,
		       p.tool_allowlist, p.runtime_auth_delivery, p.git_author_name, p.git_author_email
		FROM projects w JOIN sandbox_profiles p ON p.id = w.sandbox_profile
		WHERE w.id = 'primary'`,
	).Scan(&profile, &artifacts, &denied,
		&toolHome, &runtimeControl, &harnessEnv, &toolEnv,
		&toolAllowlist, &authDelivery, &gitName, &gitEmail); err != nil {
		t.Fatal(err)
	}
	var deniedPaths []string
	if err := json.Unmarshal([]byte(denied), &deniedPaths); err != nil {
		t.Fatal(err)
	}
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	// The artifact plane is the Project's own `.mc-artifacts`, a SIBLING of
	// `.culler` (which is a protected jurisdiction root, so nothing
	// beneath it can be mounted). Judged against the derivation every other half
	// reads, not against a hand-spelled literal.
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	wantArtifacts, err := json.Marshal([]string{ProjectArtifactRoot(canonicalRoot)})
	if err != nil {
		t.Fatal(err)
	}
	if profile != "project-primary" ||
		artifacts != string(wantArtifacts) || len(deniedPaths) == 0 ||
		toolHome != filepath.Join(canonicalHome, "state", "projects", "primary", "home") ||
		runtimeControl != "" || harnessEnv != "{}" || toolEnv != "{}" ||
		toolAllowlist != "[]" || authDelivery != "projection" ||
		gitName != "Primary Bot" || gitEmail != "primary@example.test" {
		t.Fatalf("generated profile = profile %q arrays %q/%q fields %q/%q/%q/%q/%q/%q",
			profile, artifacts, denied, toolHome, runtimeControl,
			harnessEnv, toolEnv, toolAllowlist, authDelivery)
	}
}

func TestOnboardProjectNonRepoInitCreatesMutableGitContract(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	spine := filepath.Join(home, "spine.db")
	root := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("MC_HOME", home)
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	writeVisionFixture(t, root)
	if _, _, err := onboardHome(spine, true); err != nil {
		t.Fatal(err)
	}

	if _, _, err := onboardProject(OnboardArgs{
		Spine: spine, Project: "primary", WorkspaceRoot: root,
		AcceptGeneratedSandbox: true,
	}); err != nil {
		t.Fatalf("init Project: %v", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	top, err := gitAtSource(root, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil || strings.TrimSpace(string(top)) != canonicalRoot {
		t.Fatalf("initialized top = %q, %v", top, err)
	}
	relative, err := gitAtSource(root, "config", "--local", "--get", "worktree.useRelativePaths")
	if err != nil || strings.TrimSpace(string(relative)) != "true" {
		t.Fatalf("initialized relative-worktree config = %q, %v", relative, err)
	}
	if status, _, err := onboardProject(OnboardArgs{
		Spine: spine, Project: "primary", WorkspaceRoot: root,
		AcceptGeneratedSandbox: true,
	}); err != nil || status != "ok" {
		t.Fatalf("durable init replay = status %q err %v", status, err)
	}
}

// An existing repository is ADOPTED: registration keeps the history that was
// already there rather than laying a fresh repository over it.
func TestPrepareOnboardProjectGitAdoptsAnExistingRepository(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAtSource(root, "init", "--quiet", "--initial-branch=main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("operator bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"config", "--local", "user.name", "Fixture"},
		{"config", "--local", "user.email", "fixture@example.test"},
		{"add", "notes.txt"},
		{"commit", "--quiet", "-m", "operator history"},
	} {
		if err := hostOnboardGitMutation(root, args...); err != nil {
			t.Fatalf("seed repository (%v): %v", args, err)
		}
	}
	head, err := gitAtSource(root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	original := strings.TrimSpace(string(head))
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}

	_, initialized, err := PrepareOnboardProjectGit(canonicalRoot, OnboardGitPostureIntent{
		Home: t.TempDir(), DeploymentUUID: "test-deployment", Project: "primary",
	}, hostOnboardGitMutation)
	if err != nil || initialized {
		t.Fatalf("existing repository = initialized %v err %v", initialized, err)
	}
	// The judge: the commit that was there is still reachable, so nothing
	// reinitialized the repository out from under the operator.
	adopted, err := gitAtSource(canonicalRoot, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(string(adopted)) != original {
		t.Fatalf("adopted HEAD = %q (want %q), err %v", adopted, original, err)
	}
	culler, err := gitAtSource(canonicalRoot, "rev-parse", "refs/heads/culler")
	if err != nil || strings.TrimSpace(string(culler)) != original {
		t.Fatalf("culler branch = %q (want the main commit %q), err %v", culler, original, err)
	}
}

// The trap this closes: a registration that ran `git init` left a receipt the
// next run had to contradict. Both halves of the rule record the same state, so
// re-running over the repository the first run created is accepted.
func TestPrepareOnboardProjectGitAcceptsARerunAfterItInitialized(t *testing.T) {
	root := grWorkspace(t)
	intent := OnboardGitPostureIntent{
		Home: t.TempDir(), DeploymentUUID: "test-deployment", Project: "primary",
	}
	if _, initialized, err := PrepareOnboardProjectGit(root, intent, hostOnboardGitMutation); err != nil || !initialized {
		t.Fatalf("first registration = initialized %v err %v", initialized, err)
	}
	changed, initialized, err := PrepareOnboardProjectGit(root, intent, hostOnboardGitMutation)
	if err != nil || changed || initialized {
		t.Fatalf("re-run after init = changed %v initialized %v err %v", changed, initialized, err)
	}
}

// A genuine posture change — the same Project now naming a different
// workspace — is still the implicit rewrite the receipt exists to refuse.
func TestPrepareOnboardProjectGitRefusesAChangedWorkspace(t *testing.T) {
	root := grWorkspace(t)
	intent := OnboardGitPostureIntent{
		Home: t.TempDir(), DeploymentUUID: "test-deployment", Project: "primary",
	}
	if _, _, err := PrepareOnboardProjectGit(root, intent, hostOnboardGitMutation); err != nil {
		t.Fatal(err)
	}
	moved := grWorkspace(t)
	_, _, err := PrepareOnboardProjectGit(moved, intent, hostOnboardGitMutation)
	if err == nil || !strings.Contains(err.Error(), "different durable Git posture") {
		t.Fatalf("changed workspace error = %v", err)
	}
	path, err := onboardGitPosturePath(intent)
	if err != nil {
		t.Fatal(err)
	}
	receipt, exists, err := readOnboardGitPosture(path)
	if err != nil || !exists || receipt.Root != root ||
		receipt.Posture != onboardGitPostureRepository {
		t.Fatalf("refused rewrite left receipt %+v (exists %v, err %v)", receipt, exists, err)
	}
}

// The repository rule, at the one place a first Project is born: a workspace that is
// not a repository gets one, the operator's bytes are left where they were, and
// the section reports which half of the rule it took.
func TestOnboardProjectInitializesANonRepositoryWorkspaceAndReportsIt(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	spine := filepath.Join(home, "spine.db")
	root := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("MC_HOME", home)
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	writeVisionFixture(t, root)
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("operator bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := onboardHome(spine, true); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}

	status, detail, err := onboardProject(OnboardArgs{
		Spine: spine, Project: "primary", WorkspaceRoot: root,
		AcceptGeneratedSandbox: true,
	})
	if err != nil || status != "done" {
		t.Fatalf("non-repository workspace = status %q err %v", status, err)
	}
	for _, want := range []string{
		ProjectGitStatement(canonicalRoot),
		ProjectGitOutcome(canonicalRoot, true),
	} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail omits %q: %s", want, detail)
		}
	}
	if _, statErr := os.Lstat(filepath.Join(root, ".git")); statErr != nil {
		t.Fatalf("registration left no repository: %v", statErr)
	}
	if body, readErr := os.ReadFile(filepath.Join(root, "notes.txt")); readErr != nil ||
		string(body) != "operator bytes\n" {
		t.Fatalf("registration disturbed the operator's bytes: %q %v", body, readErr)
	}
	db, openErr := substrate.Open(spine)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer db.Close()
	var count int
	if scanErr := db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&count); scanErr != nil {
		t.Fatal(scanErr)
	}
	if count != 1 {
		t.Fatalf("registration wrote %d Project rows", count)
	}
}

func TestOnboardProjectAcceptedGitInitRegistersTheRepository(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	spine := filepath.Join(home, "spine.db")
	root := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("MC_HOME", home)
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	writeVisionFixture(t, root)
	if _, _, err := onboardHome(spine, true); err != nil {
		t.Fatal(err)
	}

	status, _, err := onboardProject(OnboardArgs{
		Spine: spine, Project: "primary", WorkspaceRoot: root,
		AcceptGeneratedSandbox: true,
	})
	if err != nil || status != "done" {
		t.Fatalf("accepted git init = status %q err %v", status, err)
	}
	if _, statErr := os.Lstat(filepath.Join(root, ".git")); statErr != nil {
		t.Fatalf("accepted git init left no repository: %v", statErr)
	}
	db, openErr := substrate.Open(spine)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer db.Close()
	var registered int
	if scanErr := db.QueryRow(`SELECT COUNT(*) FROM projects WHERE id='primary' AND sandbox_profile IS NOT NULL`).Scan(&registered); scanErr != nil {
		t.Fatal(scanErr)
	}
	if registered != 1 {
		t.Fatal("accepted git init registered no Project with a sandbox profile")
	}
}

// The re-registration, at the durable receipt: a deployment carrying a
// "read-only" posture from before the retirement is not wedged. Re-running the
// section IS the operator re-registering that Project as a repository, so
// exactly that rewrite supersedes the receipt — and the superseding receipt is
// then as immutable as any other.
func TestOnboardProjectGitSupersedesAReadOnlyPostureReceiptOnce(t *testing.T) {
	root := grWorkspace(t)
	intent := OnboardGitPostureIntent{
		Home: t.TempDir(), DeploymentUUID: "test-deployment", Project: "primary",
	}
	if _, err := secureOnboardIntentDir(intent.Home); err != nil {
		t.Fatal(err)
	}
	path, err := onboardGitPosturePath(intent)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := json.Marshal(onboardGitPostureReceipt{
		Version: 1, DeploymentUUID: intent.DeploymentUUID,
		Project: intent.Project, Root: root, Posture: retiredReadOnlyGitPosture,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	changed, _, err := PrepareOnboardProjectGit(root, intent, hostOnboardGitMutation)
	if err != nil || !changed {
		t.Fatalf("re-registration over a read-only receipt = changed %v err %v", changed, err)
	}
	receipt, exists, err := readOnboardGitPosture(path)
	if err != nil || !exists || receipt.Posture != onboardGitPostureRepository {
		t.Fatalf("superseded receipt = %+v (exists %v, err %v)", receipt, exists, err)
	}

	// A read-only receipt for a DIFFERENT root is not this Project's, so it
	// refuses like any other mismatch — the supersession is not a skeleton key.
	other := grWorkspace(t)
	otherIntent := OnboardGitPostureIntent{
		Home: t.TempDir(), DeploymentUUID: intent.DeploymentUUID, Project: "primary",
	}
	if _, err := secureOnboardIntentDir(otherIntent.Home); err != nil {
		t.Fatal(err)
	}
	otherPath, err := onboardGitPosturePath(otherIntent)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := json.Marshal(onboardGitPostureReceipt{
		Version: 1, DeploymentUUID: otherIntent.DeploymentUUID,
		Project: otherIntent.Project, Root: filepath.Join(other, "elsewhere"),
		Posture: retiredReadOnlyGitPosture,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherPath, stale, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PrepareOnboardProjectGit(other, otherIntent, hostOnboardGitMutation); err == nil ||
		!strings.Contains(err.Error(), "different durable Git posture") {
		t.Fatalf("read-only receipt for another root = %v, want a refusal", err)
	}
}

// A receipt written before the state re-key spells the repository posture with
// the operator answer that produced it. It records the same state, so the
// Project it belongs to is not wedged by the vocabulary change.
func TestPrepareOnboardProjectGitReadsALegacyModeReceiptAsTheRepositoryState(t *testing.T) {
	for _, legacyMode := range []string{"existing", "init"} {
		t.Run(legacyMode, func(t *testing.T) {
			root := grWorkspace(t)
			intent := OnboardGitPostureIntent{
				Home: t.TempDir(), DeploymentUUID: "test-deployment", Project: "primary",
			}
			if _, err := secureOnboardIntentDir(intent.Home); err != nil {
				t.Fatal(err)
			}
			path, err := onboardGitPosturePath(intent)
			if err != nil {
				t.Fatal(err)
			}
			legacy, err := json.Marshal(onboardGitPostureReceipt{
				Version: 1, DeploymentUUID: intent.DeploymentUUID,
				Project: intent.Project, Root: root, Posture: legacyMode,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, legacy, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := PrepareOnboardProjectGit(root, intent, hostOnboardGitMutation); err != nil {
				t.Fatalf("registration over a %q receipt refused: %v", legacyMode, err)
			}
		})
	}
}

func TestOnboardProjectRefusesBlockedWorkspaceAddressBeforeWrites(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	spine := filepath.Join(home, "spine.db")
	root := filepath.Join(t.TempDir(), ".ssh", "workspace")
	t.Setenv("MC_HOME", home)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := onboardHome(spine, true); err != nil {
		t.Fatal(err)
	}

	_, _, err := onboardProject(OnboardArgs{
		Spine: spine, Project: "primary", WorkspaceRoot: root,
		AcceptGeneratedSandbox: true,
	})
	if err == nil || !strings.Contains(err.Error(), "blocked address") {
		t.Fatalf("blocked workspace error = %v", err)
	}
	db, openErr := substrate.Open(spine)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer db.Close()
	var count int
	if scanErr := db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&count); scanErr != nil {
		t.Fatal(scanErr)
	}
	if count != 0 {
		t.Fatalf("blocked workspace wrote %d Project rows", count)
	}
}

func TestOnboardProjectRefusesABareRepositoryWorkspace(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bare.git")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAtSource(root, "init", "--bare", "--quiet"); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	_, _, err := PrepareOnboardProjectGit(root, OnboardGitPostureIntent{
		Home: home, DeploymentUUID: "test-deployment", Project: "primary",
	}, hostOnboardGitMutation)
	if err == nil || !strings.Contains(err.Error(), "non-bare") {
		t.Fatalf("bare repo error = %v", err)
	}
	// A refused workspace leaves no durable posture behind to argue with later.
	if entries, readErr := os.ReadDir(filepath.Join(home, "onboard-intents")); readErr == nil && len(entries) != 0 {
		t.Fatalf("refused bare workspace wrote %d posture receipts", len(entries))
	}
}

func TestPrepareOnboardProjectGitSkipsAnAlreadyHealthyRepo(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAtSource(root, "init", "--quiet", "--initial-branch=culler"); err != nil {
		t.Fatal(err)
	}
	if err := hostOnboardGitMutation(root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgSign=false", "commit", "--allow-empty", "--quiet", "-m", "Create the initial commit"); err != nil {
		t.Fatal(err)
	}
	if err := hostOnboardGitMutation(
		root, "config", "--local", "worktree.useRelativePaths", "true"); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	changed, initialized, err := PrepareOnboardProjectGit(canonicalRoot, OnboardGitPostureIntent{
		Home: t.TempDir(), DeploymentUUID: "test-deployment", Project: "primary",
	}, func(string, ...string) error {
		calls++
		return nil
	})
	if err != nil || changed || initialized || calls != 0 {
		t.Fatalf("healthy Git replay = changed %v initialized %v calls %d err %v",
			changed, initialized, calls, err)
	}
}

func TestOnboardConcurrentFreshHomeNeverDeletesTheWinner(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	spine := filepath.Join(home, "spine.db")
	t.Setenv("MC_HOME", home)

	const callers = 8
	statuses := make([]string, callers)
	errs := make([]error, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			statuses[i], _, errs[i] = onboardHome(spine, true)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("fresh-home caller %d failed (statuses %v): %v", i, statuses, err)
		}
	}
	if err := requireOnboardIdentity(spine); err != nil {
		t.Fatalf("concurrent fresh home lost its winner: %v", err)
	}
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var metaRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM meta WHERE id = 1`).Scan(&metaRows); err != nil {
		t.Fatal(err)
	}
	if metaRows != 1 {
		t.Fatalf("concurrent fresh home left %d meta rows", metaRows)
	}
}

// The grant writer is parameterized by TARGET, and this is what that buys: a
// Project may need more than one grant, and the shadow check refuses a
// second source under a target already bound — two sources answering to one
// target is exactly the ambiguity it exists to stop.
//
// The claim is judged by the DISPATCH-SIDE resolver, not by the writer's own
// round-trip: an entry that parses but does not authorize would be the same
// permission-vs-request split that has twice cost the Packager.
func TestMountGrantWritesOneSourcePerTargetAndRefusesShadows(t *testing.T) {
	home := t.TempDir()
	// The real deny-all author, not a hand-rolled body: this test's whole claim
	// is about what dispatch will accept, so the file it extends must be the
	// file onboarding actually writes.
	if _, err := EnsureMountAllowlist(home); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	artifacts := ProjectArtifactRoot(workspace)
	if err := os.MkdirAll(artifacts, 0o700); err != nil {
		t.Fatal(err)
	}
	// A DIFFERENT Project's artifact plane, under its own target.
	second := ProjectArtifactRoot(t.TempDir())
	if err := os.MkdirAll(second, 0o700); err != nil {
		t.Fatal(err)
	}

	if granted, err := EnsureProjectArtifactGrant(home, "primary", artifacts); err != nil || !granted {
		t.Fatalf("artifact grant = %v, %v", granted, err)
	}
	if granted, err := ensureProjectMountGrant(home, "secondary", second); err != nil || !granted {
		t.Fatalf("second grant = %v, %v", granted, err)
	}

	body, err := os.ReadFile(MountAllowlistPath(home))
	if err != nil {
		t.Fatal(err)
	}
	allowlist, err := boundary.ParseMountAllowlist(body)
	if err != nil {
		t.Fatalf("two grants broke the mount allowlist: %v", err)
	}
	if _, err := boundary.ResolveAllowlist(allowlist); err != nil {
		t.Fatalf("ResolveAllowlist: %v", err)
	}
	for _, tc := range []struct {
		name   string
		path   string
		target string
	}{
		{"first_artifact_plane", artifacts, "primary"},
		{"second_artifact_plane", second, "secondary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var entry *boundary.AllowEntry
			for i, e := range allowlist.Entries() {
				if e.Path == tc.path {
					entry = &allowlist.Entries()[i]
				}
			}
			if entry == nil {
				t.Fatalf("no allow entry for %q: %+v", tc.path, allowlist.Entries())
			}
			// Every grant this package writes is rw: the artifact plane is the
			// sole remaining family.
			if entry.Target != tc.target || entry.Maximum != boundary.AccessRW {
				t.Fatalf("entry = %+v, want target %q max rw", *entry, tc.target)
			}
		})
	}

	// Idempotent: re-granting appends nothing.
	if granted, err := ensureProjectMountGrant(home, "secondary", second); err != nil || granted {
		t.Fatalf("re-grant = %v, %v; want false, nil", granted, err)
	}
	// And the shadow check still fires: a second, DIFFERENT source under a bound
	// target is the ambiguity the writer exists to refuse.
	third := ProjectArtifactRoot(t.TempDir())
	if err := os.MkdirAll(third, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureProjectMountGrant(home, "secondary", third); err == nil {
		t.Fatal("a second source under target secondary was accepted")
	}

	// The constraint that keeps a Project to ONE grant, pinned here because
	// it is invisible until two grants coexist: allow roots may not OVERLAP. A
	// Project's artifact plane lives inside its workspace, so that Project
	// can never also carry a workspace-wide grant — the resolver refuses the
	// pair outright and one unresolvable allowlist refuses EVERY mount in the
	// deployment.
	if granted, err := ensureProjectMountGrant(home, "primary-whole", workspace); err != nil || !granted {
		t.Fatalf("overlapping grant was refused at write time (%v, %v); the check under test is the RESOLVER's", granted, err)
	}
	body, err = os.ReadFile(MountAllowlistPath(home))
	if err != nil {
		t.Fatal(err)
	}
	overlapping, err := boundary.ParseMountAllowlist(body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := boundary.ResolveAllowlist(overlapping); err == nil {
		t.Fatal("an allowlist granting both a workspace and its own artifact plane resolved")
	}
}

func TestOnboardVerificationPermitsDeferredVision(t *testing.T) {
	finding := DoctorFinding{
		Check: "projects", Status: "fail", SetupComplete: true,
		Detail: "Project primary requires /workspace/VISION.md before work can start.",
	}
	detail, err := OnboardVerification([]DoctorFinding{finding})
	if err != nil || !strings.Contains(detail, "Setup is complete.") || !strings.Contains(detail, finding.Detail) {
		t.Fatalf("deferred vision verification = %q, %v", detail, err)
	}
	finding.SetupComplete = false
	if _, err := OnboardVerification([]DoctorFinding{finding}); err == nil {
		t.Fatal("incomplete workspace setup passed verification")
	}
	finding.SetupComplete = true
	if _, err := OnboardVerification([]DoctorFinding{finding, {Check: "routing", Status: "fail", Detail: "Routes are absent."}}); err == nil {
		t.Fatal("another failed setup check passed verification")
	}
}
