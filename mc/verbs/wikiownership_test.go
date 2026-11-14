package verbs

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"mc/repository"
)

func TestWorkerRejectsWikiChangesBeforeSubmission(t *testing.T) {
	for _, checkpoint := range []bool{false, true} {
		t.Run(map[bool]string{false: "submission", true: "checkpoint"}[checkpoint], func(t *testing.T) {
			f := newDeliveryFixture(t)
			task := f.add("Write the research report")
			run, root, id := f.run(task, "worker")
			repo := filepath.Join(root, "repo")
			input := srcGit(t, repo, "rev-parse", "HEAD")
			commit := f.commit(root, "openwiki/report.md", "The research report.\n")
			a := CompleteArgs{Task: task, Run: run, Status: "worked", Commit: commit}
			if checkpoint {
				a.Status, a.Checkpoint = "", true
			}
			if _, err := Complete(f.db, id, a); err == nil || !strings.Contains(err.Error(), "Only the Scribe") {
				t.Fatalf("the wiki change passed: %v", err)
			}
			if dfInt(t, f.db, `SELECT count(*) FROM version_submissions WHERE task_id=?`, task) != 0 ||
				dfInt(t, f.db, `SELECT count(*) FROM runs WHERE id=? AND ended_at IS NOT NULL`, run) != 0 {
				t.Fatal("the rejected submission changed the task or ended the run")
			}
			srcGit(t, repo, "reset", "--hard", input)
			a.Commit = f.commit(root, "docs/report.html", "<html><body>The research report.</body></html>\n")
			if _, err := Complete(f.db, id, a); err != nil {
				t.Fatalf("the report outside the wiki failed: %v", err)
			}
			f.capture(task, run, root)
		})
	}
}

func TestWorkerMergeAcceptsTheDestinationWiki(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Complete the research report")
	run, root, id := f.run(task, "worker")
	commit := f.commit(root, "docs/report.md", "Research in progress.\n")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Checkpoint: true, Commit: commit}); err != nil {
		t.Fatal(err)
	}
	f.capture(task, run, root)
	f.commitMain("openwiki/index.md", "The latest accepted wiki.\n")
	run, root, id = f.run(task, "worker")
	repo := filepath.Join(root, "repo")
	srcGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "merge", "--no-edit", repository.DestinationRef)
	commit = f.commit(root, "docs/report.md", "The complete research report.\n")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "worked", Commit: commit}); err != nil {
		t.Fatal(err)
	}
	f.capture(task, run, root)
}

func TestTaskCandidateCannotChangeTheDestinationWiki(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Preserve the wiki")
	run, root, _ := f.run(task, "worker")
	destination := srcGit(t, f.workspace, "rev-parse", "culler")
	commit := f.commit(root, "openwiki/index.md", "An unauthorized wiki change.\n")
	before := f.mcRefs()
	candidate := repository.Candidate{Status: "merged", SubmittedCommit: commit, DestinationCommit: destination,
		DestinationBranch: "culler", Commit: commit}
	_, err := executeRepository(context.Background(), RepositoryEnvelope{Operation: "retain-candidate", TaskID: task,
		RunID: run, RunRoot: root, WorkspaceRoot: f.workspace, Candidate: &candidate})
	if err == nil || !strings.Contains(err.Error(), "Only the Scribe") {
		t.Fatalf("the executor retained the task wiki change: %v", err)
	}
	if f.mcRefs() != before || srcGit(t, f.workspace, "rev-parse", "culler") != destination {
		t.Fatal("the rejected candidate changed the Project repository")
	}
}

func TestTaskCandidateKeepsConcurrentWikiUpdates(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Write the research report")
	f.pass(task, "docs/report.md", "The complete research report.\n")
	f.commitMain("openwiki/index.md", "A concurrent accepted wiki update.\n")
	candidate := f.merge(task, "prepare")
	if got := srcGit(t, f.workspace, "show", candidate.Commit+":openwiki/index.md"); got != "A concurrent accepted wiki update." {
		t.Fatalf("the task candidate lost the accepted wiki: %s", got)
	}
}
