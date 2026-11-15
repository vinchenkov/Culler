package verbs

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"mc/repository"
)

// A task delivers one commit. A Worker submission or checkpoint on the base
// commit is refused, and a completion on a new commit is accepted.
func TestDeliveryWorkerCompletionRequiresANewCommit(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Add the README")
	run, root, id := f.run(task, "worker")
	repo := filepath.Join(root, "repo")

	var input, base string
	if err := f.db.QueryRow(`SELECT r.input_commit,t.base_commit FROM repository_runs r JOIN tasks t ON t.id=r.task_id WHERE r.run_id=?`, run).Scan(&input, &base); err != nil {
		t.Fatal(err)
	}
	if base != input {
		t.Fatalf("the first Worker run must set the base commit to its start commit %s, got %s", input, base)
	}
	for _, checkpoint := range []bool{false, true} {
		a := CompleteArgs{Task: task, Run: run, Status: "worked", Commit: input}
		if checkpoint {
			a.Status, a.Checkpoint = "", true
		}
		_, err := Complete(f.db, id, a)
		if err == nil || !strings.Contains(err.Error(), "the submission must be a commit that differs from the base commit "+base) {
			t.Fatalf("a completion on the base commit must be refused (checkpoint %v), got %v", checkpoint, err)
		}
	}
	var status string
	if err := f.db.QueryRow(`SELECT status FROM tasks WHERE id=?`, task).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "seeded" || dfInt(t, f.db, `SELECT count(*) FROM version_submissions WHERE task_id=?`, task) != 0 {
		t.Fatalf("the refusal must leave the task at its stage without a submission, got %s", status)
	}

	writeFile(t, filepath.Join(repo, "README.md"), "The README.\n")
	srcGit(t, repo, "add", ".")
	srcGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "Add the README")
	commit, err := (repository.Git{Root: repo}).Commit(context.Background(), "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "worked", Commit: commit}); err != nil {
		t.Fatalf("a completion on a new commit must be accepted: %v", err)
	}
}
