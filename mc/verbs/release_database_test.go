package verbs

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mc/dispatch"
	"mc/domain"
	"mc/substrate"
)

func TestReleaseDatabaseSetupWorkPreventsIdle(t *testing.T) {
	if (ReleaseDatabase{EnvironmentOperations: 1}).Idle() {
		t.Fatal("active setup work passed the maintenance check")
	}
	if !(ReleaseDatabase{}).Idle() {
		t.Fatal("an empty deployment requires a work wait")
	}
}

func TestReleaseDatabaseIncludesArchivedProjectImages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spine.db")
	if _, err := OnboardSpine(path, absentMirrorRequest()); err != nil {
		t.Fatal(err)
	}
	db, err := substrate.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, status := range []string{"active", "archived"} {
		if _, err := db.Exec(`INSERT INTO sandbox_profiles(id,workspace_root) VALUES(?,?)`, status, "/fixture/"+status); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO projects(id,title,sandbox_profile,status) VALUES(?,?,?,?)`, status, status, status, status); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO project_environments(project_id,setup_id,selected_image_digest,selected_base_digest) VALUES(?,?,?,?)`, status, "setup-"+status, "image-"+status, "base-"+status); err != nil {
			t.Fatal(err)
		}
	}
	result, err := InspectReleaseDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ProjectImages) != 2 || result.ProjectImages[1].ProjectID != "archived" || !result.ProjectImages[1].Archived || result.ProjectImages[1].Image != "image-archived" || result.ProjectImages[1].Base != "base-archived" {
		t.Fatalf("Project images: %#v", result.ProjectImages)
	}
}

func TestReleaseDatabaseCheckPreservesAbsentAndIncompatibleData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spine.db")
	result, err := InspectReleaseDatabase(path)
	if err != nil || !result.Empty || !result.Idle() {
		t.Fatalf("absent: %#v %v", result, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("check created the database")
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	result, err = InspectReleaseDatabase(path)
	if err != nil || !result.Empty || !result.Idle() {
		t.Fatalf("empty: %#v %v", result, err)
	}
	if _, err := OnboardSpine(path, absentMirrorRequest()); err != nil {
		t.Fatal(err)
	}
	db, err := substrate.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE meta SET schema_id='incompatible'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InspectReleaseDatabase(path); err == nil {
		t.Fatal("incompatible data passed")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("compatibility check changed data")
	}
}

func TestReleaseDatabaseCheckPreservesOperatorPauses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spine.db")
	if _, err := OnboardSpine(path, absentMirrorRequest()); err != nil {
		t.Fatal(err)
	}
	db, err := substrate.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, status := range []string{"paused", "active"} {
		if _, err := db.Exec(`INSERT INTO sandbox_profiles(id,workspace_root) VALUES(?,?)`, status, "/fixture/"+status); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO projects(id,title,sandbox_profile,status) VALUES(?,?,?,?)`, status+"-project", status, status, status); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if result, err := InspectReleaseDatabase(path); err != nil || !result.Idle() {
			t.Fatalf("check: %#v %v", result, err)
		}
	}
	if got := dfStr(t, db, `SELECT status FROM projects WHERE id='paused-project'`); got != "paused" {
		t.Fatal("the Project pause changed")
	}
	if got := dfStr(t, db, `SELECT status FROM projects WHERE id='active-project'`); got != "active" {
		t.Fatal("the active Project changed")
	}
}

func TestReleaseDatabaseSeparatesHomieHistoryAndContainerProof(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spine.db")
	if _, err := OnboardSpine(path, absentMirrorRequest()); err != nil {
		t.Fatal(err)
	}
	db, err := substrate.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, status := range []string{"active", "ended"} {
		if _, err := db.Exec(`INSERT INTO homie_sessions(id,status,container_name,verb_allowlist,session_path,binding,current_launch_id,current_launch_mode,current_container_id,launch_bound_at) VALUES(?,?,?,'[]','/fixture/session','fixture',?,'fresh',?,datetime('now'))`, status, status, status, strings.Repeat("a", 16), strings.Repeat("b", 64)); err != nil {
			t.Fatal(err)
		}
	}
	result, err := InspectReleaseDatabase(path)
	if err != nil || !result.Idle() || len(result.HomieContainers) != 1 || result.HomieContainers[0] != strings.Repeat("b", 64) {
		t.Fatalf("Homie proof: %#v %v", result, err)
	}
	if _, err := db.Exec(`UPDATE homie_sessions SET current_container_id=NULL,launch_bound_at=NULL WHERE id='active'`); err != nil {
		t.Fatal(err)
	}
	result, err = InspectReleaseDatabase(path)
	if err != nil || result.Idle() || result.HomieLaunches != 1 || len(result.HomieContainers) != 0 {
		t.Fatalf("unbound launch: %#v %v", result, err)
	}
}

func TestReleaseDatabaseReportsActiveRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spine.db")
	initial, err := OnboardSpine(path, absentMirrorRequest())
	if err != nil {
		t.Fatal(err)
	}
	result, err := InspectReleaseDatabase(path)
	if err != nil || result.Empty || !result.Idle() || result.UUID != initial.DeploymentUUID {
		t.Fatalf("idle: %#v %v", result, err)
	}
	db, err := substrate.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO runs(id,tier,role) VALUES('active','pipeline','worker')`); err != nil {
		t.Fatal(err)
	}
	result, err = InspectReleaseDatabase(path)
	if err != nil || result.Idle() || result.Runs != 1 {
		t.Fatalf("active: %#v %v", result, err)
	}
	if _, err := db.Exec(`UPDATE runs SET ended_at=datetime('now') WHERE id='active'`); err != nil {
		t.Fatal(err)
	}
	result, err = InspectReleaseDatabase(path)
	if err != nil || !result.Idle() {
		t.Fatalf("completed: %#v %v", result, err)
	}
}

func TestReleaseDatabaseIdleAfterFailedCapture(t *testing.T) {
	db := dvSpine(t)
	stTask(t, db, 1, "ws-test", dispatch.StatusSeeded)
	stSubmission(t, db, 1)
	path := filepath.Join(os.Getenv("MC_HOME"), "spine.db")
	resp := startRequest(t, db, false)
	stWantStarted(t, db, resp, "capture/1")
	result, err := InspectReleaseDatabase(path)
	if err != nil || result.Idle() || result.RepositoryOperations != 1 {
		t.Fatalf("active capture: %#v %v", result, err)
	}
	stFail(t, db, resp.Started[0], false)
	result, err = InspectReleaseDatabase(path)
	if err != nil || !result.Idle() {
		t.Fatalf("failed capture: %#v %v", result, err)
	}
	if finished := dfInt(t, db, `SELECT finished FROM repository_transfers WHERE run_id=?`, resp.Started[0]); finished != 1 {
		t.Fatal("the end of the capture left its transfer unfinished")
	}
}

func TestReleaseDatabaseActiveForOperatorAttentionApply(t *testing.T) {
	db := dvSpine(t)
	stMergeable(t, db, 1)
	stApprove(t, db, 1)
	path := filepath.Join(os.Getenv("MC_HOME"), "spine.db")
	resp := startRequest(t, db, false)
	stWantStarted(t, db, resp, "apply/1")
	if _, err := RepositoryRecord(db, nil, "apply", resp.Started[0], RepositoryResult{TaskID: 1, Status: "operator-attention",
		OperatorAction: "The operator checkout has uncommitted changes."}); err != nil {
		t.Fatal(err)
	}
	result, err := InspectReleaseDatabase(path)
	if err != nil || result.Idle() || result.RepositoryOperations != 1 || result.Runs != 0 {
		t.Fatalf("operator attention: %#v %v", result, err)
	}
}

// An active wiki update run is a pipeline run with no end, so the release
// check waits for it. A wiki update whose run has an end adds no run.
func TestReleaseDatabaseCountsAWikiUpdateRun(t *testing.T) {
	db := dvSpine(t)
	path := filepath.Join(os.Getenv("MC_HOME"), "spine.db")
	inspect := func(step string) ReleaseDatabase {
		t.Helper()
		result, err := InspectReleaseDatabase(path)
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		return result
	}
	if result := inspect("no wiki update"); !result.Idle() {
		t.Fatalf("no wiki update: %#v, want an idle database", result)
	}

	run := wrWantSpawn(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test")
	if result := inspect("an active wiki update run"); result.Idle() || result.Runs != 1 || result.Leases != 0 || result.RepositoryOperations != 0 {
		t.Fatalf("an active wiki update run: %#v, want one run and no lease", result)
	}

	// The wiki update stays active: its wiki commit waits for the merge.
	wrEndRun(t, db, run, domain.WikiRunEnd{Outcome: "completed", WikiCommit: wrCommitB})
	if ended := dfInt(t, db, `SELECT COUNT(*) FROM wiki_updates WHERE run_id=? AND ended_at IS NULL AND stopped_at IS NULL`, run); ended != 1 {
		t.Fatal("the fixture wiki update has an end or a stop confirmation")
	}
	if result := inspect("a wiki update with an ended run"); !result.Idle() || result.Runs != 0 {
		t.Fatalf("a wiki update with an ended run: %#v, want an idle database", result)
	}
}
