package verbs

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"mc/boundary"
	"mc/deployment"
	"mc/substrate"
)

// Containers run with no ambient HOME, so a Project's synthetic home
// IS the `/home/agent` its harness gets, and the private commit leg refuses any
// non-fake harness whose selected profile carries an empty one. The project
// section derives that path, records it, and creates the operator-owned
// directory; the smoke section inserted `(id, workspace_root)` only, leaving
// `tool_home_dir` NULL. Every subject-bearing dispatch against the smoke
// Project was therefore refused at commit with "selected Project profile
// carries no synthetic home" — so the full-pipeline smoke could never have
// completed, whatever else was fixed. Asserted against ProjectToolHome, the
// single derivation the mount attestation and dispatch also read, so this test
// cannot pass by restating the path its own way.
func TestOnboardSmokeProfileCarriesTheSyntheticHomeDispatchRequires(t *testing.T) {
	home, spine, id, root := smokeFixture(t)
	a, err := prepareOnboardSmoke(OnboardArgs{
		Section: "smoke", Spine: spine, Smoke: true, Project: id, WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("prepare onboarding smoke: %v", err)
	}
	if _, _, _, err := onboardSmokeDB(a); err != nil {
		t.Fatalf("register onboarding smoke: %v", err)
	}

	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	want := ProjectToolHome(filepath.Clean(canonical), id)

	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var toolHome sql.NullString
	if err := db.QueryRow(
		`SELECT tool_home_dir FROM sandbox_profiles WHERE id=?`, id).Scan(&toolHome); err != nil {
		t.Fatal(err)
	}
	if !toolHome.Valid || toolHome.String == "" {
		t.Fatal("smoke profile carries no synthetic home: every subject-bearing dispatch against it is refused at commit")
	}
	if toolHome.String != want {
		t.Fatalf("smoke synthetic home = %q, want the derived per-Project path %q", toolHome.String, want)
	}

	// Recorded is not enough — the commit leg binds it, so it must exist on the
	// host, operator-owned and unwidened, exactly as the project section
	// leaves it.
	info, err := os.Stat(want)
	if err != nil || !info.IsDir() {
		t.Fatalf("smoke synthetic home %q was recorded but never created: %v", want, err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("smoke synthetic home mode = %04o, want 0700", perm)
	}
}

func smokeFixture(t *testing.T) (home, spine, id, root string) {
	t.Helper()
	home = filepath.Join(t.TempDir(), "mc-home")
	spine = filepath.Join(home, "spine.db")
	id = "smoke-0011223344556677"
	t.Setenv("MC_HOME", home)
	if _, _, err := onboardHome(spine, true); err != nil {
		t.Fatalf("onboard home: %v", err)
	}
	base, err := deployment.SmokeWorkspaceBase(home)
	if err != nil {
		t.Fatalf("smoke workspace base: %v", err)
	}
	root = filepath.Join(base, id, "workspace")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("smoke workspace: %v", err)
	}
	if err := os.Chmod(filepath.Dir(root), 0o700); err != nil {
		t.Fatalf("smoke parent mode: %v", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("smoke workspace mode: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve smoke workspace: %v", err)
	}
	root = filepath.Clean(resolved)
	return
}

// The Packager renders the packet onto the profile's artifact roots, so a
// smoke profile with an empty `artifact_roots` derives a plan with no artifact
// plane to write — and the packet the smoke exists to produce can never be
// rendered. Every Packager attempt is then stamped and reaped by the
// spawn-watchdog with an empty session folder and no heartbeat, which burns
// the dispatch budget and blocks the task at `verified`.
//
// Filesystem-side checks cannot see this. The smoke's `.mc-artifacts`
// directory IS created and its allowlist grant IS written, so every
// filesystem-side check passes; the allowlist is the PERMISSION and the
// profile row is the REQUEST, and only the request can be missing. `mc
// onboard project` builds its profile through `onboardProjectProfile`, but
// the smoke section carries its own INSERT.
//
// Judged through the real consumer — the spine projection dispatch loads and
// the derivation the Packager itself takes — never by re-reading the column
// this test would be asserting. A profile row that merely holds the string
// proves nothing; what must hold is that the derived plan has somewhere to
// write.
func TestOnboardSmokePackagerDerivesAWritableArtifactMount(t *testing.T) {
	_, spine, id, root := smokeFixture(t)
	a, err := prepareOnboardSmoke(OnboardArgs{
		Section: "smoke", Spine: spine, Smoke: true, Project: id, WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("prepare onboarding smoke: %v", err)
	}
	if _, _, _, err := onboardSmokeDB(a); err != nil {
		t.Fatalf("register onboarding smoke: %v", err)
	}

	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	projects, err := substrate.LoadDispatchProjectProjection(context.Background(), db)
	if err != nil {
		t.Fatalf("load Project projection: %v", err)
	}

	// The ordinary mount requests of a run are the artifact roots of the
	// selected profile. The Packager writes its packet there.
	state := PrivateDispatchMountState{
		Projects:        projects,
		SelectedProject: id,
	}
	requests, selected, refused, err := deriveDispatchMountRequests(state)
	if err != nil {
		t.Fatalf("derive Packager mounts: %v", err)
	}
	if refused != nil {
		t.Fatalf("Packager mount derivation refused: %+v", refused)
	}
	if selected.WorkspaceRoot != root {
		t.Fatalf("selected workspace root = %q, want %q", selected.WorkspaceRoot, root)
	}

	want := ProjectArtifactRoot(root)
	found := false
	for _, request := range requests {
		if request.Access == boundary.AccessRW && request.Source == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("the smoke Packager derives NO writable artifact mount, so the packet the smoke "+
			"exists to produce can never be rendered; want the artifact plane %q among %d requests",
			want, len(requests))
	}
}

func TestPrepareOnboardSmokeAdmitsOnlyItsIsolatedDeploymentRoot(t *testing.T) {
	_, _, id, root := smokeFixture(t)
	got, err := prepareOnboardSmoke(OnboardArgs{
		Section: "smoke", Smoke: true, Project: id, WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("prepare exact root: %v", err)
	}
	if got.WorkspaceRoot != root {
		t.Fatalf("prepared root = %q, want %q", got.WorkspaceRoot, root)
	}
	for name, mutate := range map[string]func(*OnboardArgs){
		"not smoke":     func(a *OnboardArgs) { a.Smoke = false },
		"bad id":        func(a *OnboardArgs) { a.Project = "operator-repo" },
		"foreign root":  func(a *OnboardArgs) { a.WorkspaceRoot = filepath.Dir(root) },
		"relative root": func(a *OnboardArgs) { a.WorkspaceRoot = "smoke/workspace" },
	} {
		t.Run(name, func(t *testing.T) {
			a := OnboardArgs{Section: "smoke", Smoke: true, Project: id, WorkspaceRoot: root}
			mutate(&a)
			if _, err := prepareOnboardSmoke(a); err == nil {
				t.Fatalf("%s was admitted", name)
			}
		})
	}
}

func TestOnboardSmokeRegistersOneIdempotentTransientOriginUserTask(t *testing.T) {
	_, spine, id, root := smokeFixture(t)
	// Through prepare, not hand-built: prepare is where the section derives and
	// creates the synthetic home, and assembling OnboardArgs directly here is
	// what let a profile with no tool home look registrable for a whole phase.
	a, err := prepareOnboardSmoke(OnboardArgs{
		Section: "smoke", Spine: spine, Smoke: true,
		Project: id, WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	status, _, roots, err := onboardSmokeDB(a)
	if err != nil {
		t.Fatalf("first registration: %v", err)
	}
	if status != "done" || len(roots) != 1 || roots[0].ID != id || roots[0].Root != root {
		t.Fatalf("first receipt = status %q roots %+v", status, roots)
	}

	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var profile, wsStatus, storedRoot string
	if err := db.QueryRow(`
		SELECT w.sandbox_profile, w.status, p.workspace_root
		FROM projects w JOIN sandbox_profiles p ON p.id=w.sandbox_profile
		WHERE w.id=?`, id).Scan(&profile, &wsStatus, &storedRoot); err != nil {
		t.Fatal(err)
	}
	if profile != id || wsStatus != "active" || storedRoot != root {
		t.Fatalf("Project = %q/%q/%q", profile, wsStatus, storedRoot)
	}
	var taskID int64
	var title, origin, taskStatus, project string
	var priority int
	if err := db.QueryRow(`
		SELECT id, title, origin, status, project, priority
		FROM tasks WHERE project=?`, id).
		Scan(&taskID, &title, &origin, &taskStatus, &project, &priority); err != nil {
		t.Fatal(err)
	}
	if taskID <= 0 || title != onboardSmokeTitle || origin != "user" ||
		taskStatus != "proposed" || project != id || priority != -1 {
		t.Fatalf("smoke task = %d %q %q %q %q %d",
			taskID, title, origin, taskStatus, project, priority)
	}

	status, _, _, err = onboardSmokeDB(a)
	if err != nil || status != "ok" {
		t.Fatalf("idempotent replay = status %q err %v", status, err)
	}
	var tasks int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE project=?`, id).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if tasks != 1 {
		t.Fatalf("replay left %d smoke tasks, want one", tasks)
	}
}
