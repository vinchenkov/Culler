package verbs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"mc/repository"
)

// commit writes one file in the isolated repository of a run and commits it.
// It returns the new HEAD commit.
func (f *deliveryFixture) commit(root, file, content string) string {
	f.t.Helper()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, file)), 0700); err != nil {
		f.t.Fatal(err)
	}
	writeFile(f.t, filepath.Join(repo, file), content)
	srcGit(f.t, repo, "add", ".")
	srcGit(f.t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "Complete the contribution")
	commit, err := (repository.Git{Root: repo}).Commit(context.Background(), "HEAD")
	if err != nil {
		f.t.Fatal(err)
	}
	return commit
}

// capture retains the submitted version of a completed run and cleans its repository.
func (f *deliveryFixture) capture(task int64, run, root string) {
	f.t.Helper()
	f.captureSubmission(task, run)
	f.cleanup(task, run, root)
}

// stopped confirms the container stop of a run that ended without a submission and cleans its repository.
func (f *deliveryFixture) stopped(task int64, run, root string) {
	f.t.Helper()
	if _, err := RunStopped(f.db, nil, run); err != nil {
		f.t.Fatal(err)
	}
	f.cleanup(task, run, root)
}

// artifact writes one durable report on the artifact plane of the run and returns its path.
func (f *deliveryFixture) artifact(run, body string) string {
	f.t.Helper()
	path := filepath.Join(f.artifacts, "test", run+".html")
	writeFile(f.t, path, "<html><body>"+body+"</body></html>")
	return path
}

// advance moves the task through the pipeline roles up to the stage and
// returns the Worker commit. The stages are worked, verified, and packaged.
func (f *deliveryFixture) advance(task int64, file, content, stage string) string {
	f.t.Helper()
	run, root, id := f.run(task, "worker")
	commit := f.commit(root, file, content)
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "worked", Commit: commit}); err != nil {
		f.t.Fatal(err)
	}
	f.capture(task, run, root)
	if stage == "worked" {
		return commit
	}
	run, root, id = f.run(task, "verifier")
	evidence := f.artifact(run, "The required checks pass.")
	if _, err := VerifierVerdict(f.db, id, VerdictArgs{Task: task, Run: run, Outcome: "pass", SHA: commit, Evidence: evidence, StoreWorktree: filepath.Join(root, "repo")}); err != nil {
		f.t.Fatal(err)
	}
	f.cleanup(task, run, root)
	if stage == "verified" {
		return commit
	}
	run, root, id = f.run(task, "packager")
	packet := f.artifact(run, "The contribution satisfies the task criteria.")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "packaged", Outputs: packet, Thesis: "The contribution satisfies the task criteria."}); err != nil {
		f.t.Fatal(err)
	}
	f.cleanup(task, run, root)
	if stage != "packaged" {
		f.t.Fatalf("the fixture has no stage %q", stage)
	}
	return commit
}

// spentBudget builds a task whose Worker run holds the lease with one allowance unit left.
func spentBudget(t *testing.T) (*deliveryFixture, int64, string, string, *RunIdentity) {
	t.Helper()
	f := newDeliveryFixture(t)
	task := f.add("Finish the work with one allowance unit")
	dvExec(t, f.db, `UPDATE tasks SET dispatch_retries=1 WHERE id=?`, task)
	run, root, id := f.run(task, "worker")
	return f, task, run, root, id
}
