package verbs

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mc/domain"
)

func TestPacketHistoryPreservesSnapshotAfterSourceOverwrite(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Prove the first requirement")
	f.advance(task, "feature.txt", "The original result.\n", "verified")
	run, root, id := f.run(task, "packager")
	source := filepath.Join(f.artifacts, "test", run+".html")
	original := "<html><body>The original packet proves the first requirement.</body></html>"
	writeFile(t, source, original)
	if _, err := Complete(f.db, id, CompleteArgs{Run: run, Task: task, Status: "packaged", Outputs: source, Thesis: "The original packet proves the first requirement."}); err != nil {
		t.Fatal(err)
	}
	f.cleanup(task, run, root)
	// The delivery record and the run hold the snapshot, never the source path.
	snapshot := dfStr(t, f.db, `SELECT report_path FROM tasks WHERE id=?`, task)
	if dfStr(t, f.db, `SELECT output_path FROM runs WHERE id=?`, run) != snapshot {
		t.Fatal("the run output differs from the recorded report")
	}
	if snapshot == source || filepath.Ext(snapshot) != ".html" || filepath.Dir(snapshot) != filepath.Dir(source) {
		t.Fatalf("the snapshot changed its report context: %s", snapshot)
	}
	// The tick prepares the merge candidate and publishes the packet; the
	// revision then supersedes the published packet into the history.
	f.merge(task, "prepare")
	f.transaction(func(ctx context.Context, q Q) error { return domain.PublishReview(ctx, q, task) })
	boardRevision(t, f.db, task, "The new requirement needs another packet.")
	if err := os.WriteFile(source, []byte("<html><body>The next packet replaces the source file.</body></html>"), 0600); err != nil {
		t.Fatal(err)
	}
	historical := dfStr(t, f.db, `SELECT render_path FROM review_versions p WHERE EXISTS(SELECT 1 FROM requirements_revisions r WHERE r.task_id=p.task_id AND r.previous_requirements_version=p.requirements_version) AND task_id=? ORDER BY id LIMIT 1`, task)
	body, err := os.ReadFile(historical)
	if err != nil || string(body) != original || historical != snapshot {
		t.Fatalf("historical packet bytes changed: %q, %v", body, err)
	}
	info, err := os.Stat(snapshot)
	if err != nil || info.Mode().Perm()&0222 != 0 {
		t.Fatal("the recorded snapshot remains writable")
	}
}

func TestArtifactSnapshotRejectsSymlinksAndEscapes(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MC_ARTIFACTS_ROOT", root)
	plane := filepath.Join(root, "test")
	if err := os.Mkdir(plane, 0700); err != nil {
		t.Fatal(err)
	}
	id := &RunIdentity{Mounts: RunMounts{AllowWrite: []string{plane}}}
	source := filepath.Join(plane, "report.md")
	body := []byte("The report has immutable evidence.")
	if err := os.WriteFile(source, body, 0600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(plane, fmt.Sprintf("snapshot-%x.md", sha256.Sum256(body)))
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("The outside file remains intact."), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotRunArtifact(id, source, 1024); err == nil {
		t.Fatal("a symlink replaced the snapshot destination")
	}
	if _, err := snapshotRunArtifact(id, outside, 1024); err == nil {
		t.Fatal("the snapshot accepted an unauthorized source")
	}
	nested := filepath.Join(plane, "nested")
	if err := os.Symlink(filepath.Dir(outside), nested); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotRunArtifact(id, filepath.Join(nested, "outside.md"), 1024); err == nil {
		t.Fatal("the snapshot followed a directory outside its plane")
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "The outside file remains intact." {
		t.Fatal("the snapshot changed the outside file")
	}
}

func TestArtifactSnapshotRetriesIgnoreUnpublishedTemporaryFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MC_ARTIFACTS_ROOT", root)
	plane := filepath.Join(root, "test")
	if err := os.Mkdir(plane, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(plane, "evidence.md")
	if err := os.WriteFile(source, []byte("The complete report survives a process restart."), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plane, ".snapshot-interrupted"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	id := &RunIdentity{Mounts: RunMounts{AllowWrite: []string{plane}}}
	first, err := snapshotRunArtifact(id, source, 1024)
	if err != nil {
		t.Fatal(err)
	}
	second, err := snapshotRunArtifact(id, source, 1024)
	if err != nil || first != second {
		t.Fatalf("the retry changed its snapshot identity: %s, %s, %v", first, second, err)
	}
	body, err := os.ReadFile(second)
	if err != nil || !strings.Contains(string(body), "The complete report survives") {
		t.Fatalf("the published report contains partial bytes: %q, %v", body, err)
	}
}
