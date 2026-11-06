package verbs

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc/dispatch"
)

func TestArtifactExpiryPreservesActiveReviewAndRepositoryDeliverable(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Create a durable project report")
	document := "# Report\n\nThe complete project report.\n"
	f.pass(task, "docs/report.md", document)
	f.merge(task, "prepare")
	now := time.Now().UTC()
	if plan, err := ArtifactExpiryPlan(f.db, nil, now); err != nil || plan.TaskID != 0 {
		t.Fatalf("active review lost artifacts: %+v %v", plan, err)
	}
	f.approve(task)
	f.merge(task, "apply")
	commit := srcGit(t, f.workspace, "rev-parse", "main")
	if plan, err := ArtifactExpiryPlan(f.db, nil, now); err != nil || plan.TaskID != 0 {
		t.Fatal(plan, err)
	}
	var expiry string
	if err := f.db.QueryRow(`SELECT expires_at FROM artifact_retention WHERE task_id=?`, task).Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	if expiry != now.AddDate(0, 0, 90).Format("2006-01-02 15:04:05") {
		t.Fatal(expiry)
	}
	if plan, err := ArtifactExpiryPlan(f.db, nil, now.AddDate(0, 0, 89)); err != nil || plan.TaskID != 0 {
		t.Fatalf("artifacts expired before 90 days: %+v %v", plan, err)
	}
	plan, err := ArtifactExpiryPlan(f.db, nil, now.AddDate(0, 0, 91))
	if err != nil || plan.TaskID != task || len(plan.Paths) == 0 {
		t.Fatalf("expiry did not select the closed task: %+v %v", plan, err)
	}
	if _, err := f.db.Exec(`UPDATE tasks SET archived=0 WHERE id=?`, task); err == nil {
		t.Fatal("the task reopened during file removal")
	}
	changes := 0
	for _, name := range plan.Paths {
		if strings.Contains(name, "/changes/") {
			changes++
		}
	}
	if changes != 2 {
		t.Fatalf("the expiry plan holds %d change data files, want 2: %v", changes, plan.Paths)
	}
	roots, err := f.artifactRoots()
	if err != nil {
		t.Fatal(err)
	}
	roots["test"] = filepath.Join(f.artifacts, "test")
	if err := RemoveExpiredArtifacts(roots, plan); err != nil {
		t.Fatal(err)
	}
	// A repeated plan resumes the same reservation after a host interruption.
	resumed, err := ArtifactExpiryPlan(f.db, nil, now.AddDate(0, 0, 92))
	if err != nil || resumed.Token != plan.Token {
		t.Fatal(resumed, err)
	}
	if _, err := ArtifactExpiryComplete(f.db, nil, task, plan.Token, now.AddDate(0, 0, 92)); err != nil {
		t.Fatal(err)
	}
	for _, name := range plan.Paths {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Fatalf("artifact remains: %s %v", name, err)
		}
	}
	if srcGit(t, f.workspace, "rev-parse", "main") != commit || srcGit(t, f.workspace, "show", "main:docs/report.md") == "" {
		t.Fatal("expiry changed the repository deliverable")
	}
	state, err := artifactState(context.Background(), f.db, task, plan.Paths[0])
	if err != nil || state["status"] != "expired" || state["retention_days"] != 90 {
		t.Fatal(state, err)
	}
	// The spine keeps the packet facts after the report file expires.
	packet := packetRow(t, f.db, false, task)
	retention, _ := packet["artifact_retention"].(map[string]any)
	if retention["status"] != "expired" || packet["review_state"] != "merged" || packet["merge_commit"] != commit ||
		packet["candidate_changes"] == nil || packet["submitted_commit"] == nil || packet["criteria"] == nil {
		t.Fatalf("the expired packet lost its facts: %v", packet)
	}
	var decision string
	if err := f.db.QueryRow(`SELECT decision FROM tasks WHERE id=?`, task).Scan(&decision); err != nil || decision != "approved" {
		t.Fatal(decision, err)
	}

}

func TestArtifactRemovalPreservesSharedFilesAndRejectsEscapes(t *testing.T) {
	mount := t.TempDir()
	t.Setenv("MC_ARTIFACTS_ROOT", mount)
	plane := filepath.Join(mount, "reports")
	task := int64(42)
	directory := filepath.Join(plane, strconv.FormatInt(task, 10))
	owned := filepath.Join(directory, "packet.html")
	shared := filepath.Join(directory, "shared.md")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, owned, "The task packet.\n")
	writeFile(t, shared, "Another task requires this evidence.\n")
	plan := ArtifactExpiry{TaskID: task, Token: "reservation", Paths: []string{owned, shared}, Protected: []string{shared}}
	if err := RemoveExpiredArtifacts(map[string]string{"reports": plane}, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(owned); !os.IsNotExist(err) {
		t.Fatal("the eligible packet remains")
	}
	if _, err := os.Stat(shared); err != nil {
		t.Fatal("expiry removed shared evidence")
	}
	alias := filepath.Join(plane, "alias")
	if err := os.Symlink(directory, alias); err != nil {
		t.Fatal(err)
	}
	plan.Paths = []string{filepath.Join(alias, "shared.md")}
	if err := RemoveExpiredArtifacts(map[string]string{"reports": plane}, plan); err == nil {
		t.Fatal("expiry accepted a second path to shared evidence")
	}
	if _, err := os.Stat(shared); err != nil {
		t.Fatal("expiry removed shared evidence through an alias")
	}
	outside := filepath.Join(t.TempDir(), "source.md")
	writeFile(t, outside, "The repository deliverable.\n")
	for _, name := range []string{outside, filepath.Join(mount, "reports") + "/../source.md", filepath.Join(mount, "absent", "42", "report.md")} {
		plan.Paths = []string{name}
		if err := RemoveExpiredArtifacts(map[string]string{"reports": plane}, plan); err == nil {
			t.Fatalf("expiry accepted %s", name)
		}
	}
	link := filepath.Join(plane, "43")
	if err := os.Symlink(filepath.Dir(outside), link); err != nil {
		t.Fatal(err)
	}
	plan.TaskID = 43
	plan.Paths = []string{filepath.Join(link, "source.md")}
	plan.Protected = nil
	if err := RemoveExpiredArtifacts(map[string]string{"reports": plane}, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("expiry followed a symbolic link")
	}
}

func TestBlockedTaskRetainsArtifacts(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	dvExec(t, db, `UPDATE tasks SET blocked=1,blocked_kind='operator',blocked_reason='The task requires a decision.' WHERE id=1`)
	now := time.Now().UTC()
	if plan, err := ArtifactExpiryPlan(db, nil, now); err != nil || plan.TaskID != 0 {
		t.Fatal(plan, err)
	}
	dvExec(t, db, `UPDATE tasks SET decision='cancelled',decided_at=datetime('now'),archived=1 WHERE id=1`)
	if _, err := ArtifactExpiryPlan(db, nil, now); err != nil {
		t.Fatal(err)
	}
	if dfInt(t, db, `SELECT count(*) FROM artifact_retention WHERE expires_at IS NOT NULL`) != 1 {
		t.Fatal("the closed task lacks its retention period")
	}
}

func TestArtifactExpiryRemovesUnsubmittedTaskScratch(t *testing.T) {
	t.Setenv("MC_ARTIFACTS_ROOT", t.TempDir())
	workspace := t.TempDir()
	plane := ProjectArtifactRoot(workspace)
	directory := filepath.Join(plane, "42")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, "draft.md"), "An incomplete task capture.\n")
	writeFile(t, filepath.Join(plane, "keep.md"), "Another task owns this file.\n")
	// The mount allowlist target differs from the Project id.
	plan := ArtifactExpiry{TaskID: 42, Token: "reservation", WorkspaceRoot: workspace}
	if err := RemoveExpiredArtifacts(map[string]string{"earlier-project": plane}, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("the task directory remains", err)
	}
	if _, err := os.Stat(filepath.Join(plane, "keep.md")); err != nil {
		t.Fatal(err)
	}
}
