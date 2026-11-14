package verbs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mc/repository"
)

// wikiScribe starts a wiki update at the start commit with the repository of
// its Scribe run. It returns the wiki update and the stand-in of its Scribe,
// whose terminal action reads that repository.
func (f *deliveryFixture) wikiScribe(start string) (*wmUpdate, *wcRun) {
	f.t.Helper()
	u := &wmUpdate{t: f.t, db: f.db, f: f, run: wrNewRun(f.t), startCommit: start}
	wrFresh(f.t, f.db, "ws-test", u.run, start)
	u.allocate()
	root := f.wikiRoot(u.run)
	if err := os.Symlink(filepath.Join(root, "repo"), filepath.Join(root, "source")); err != nil {
		f.t.Fatal(err)
	}
	f.t.Setenv("MC_TASK_STORE_ROOT", root)
	return u, &wcRun{t: f.t, db: f.db, id: wcScribe(u.run), run: u.run, source: filepath.Join(root, "repo"), start: start}
}

// joinMain makes the commit of a hostile Scribe. The commit has the tree of
// the wiki commit and two parents: the wiki commit and the commit of culler.
// The repository of the run holds no commit after the start commit, so the
// test fetches culler into it.
func (f *deliveryFixture) joinMain(scribe *wcRun, page string) string {
	f.t.Helper()
	srcGit(f.t, scribe.source, "fetch", "-q", f.workspace, "culler")
	commit := srcGit(f.t, scribe.source, "commit-tree", page+"^{tree}", "-p", page, "-p", "FETCH_HEAD", "-m", "Update the Project wiki")
	srcGit(f.t, scribe.source, "reset", "-q", "--hard", commit)
	return commit
}

// submitWiki submits the wiki commit through the terminal action of the
// Scribe and captures it. Each check of the terminal action must pass.
func (f *deliveryFixture) submitWiki(u *wmUpdate, scribe *wcRun, commit string) {
	f.t.Helper()
	u.commit = commit
	if _, err := scribe.complete(commit, scribe.receipt("complete")); err != nil {
		f.t.Fatalf("terminal action of the Scribe: %v", err)
	}
	f.request("capture/wiki")
}

// wantCandidateRefusal runs the merge candidate step. The step must refuse the
// merge candidate with the check text. The wiki update must end as failed
// with that cause, with no candidate, no merge authorization, and no change of culler.
func (f *deliveryFixture) wantCandidateRefusal(u *wmUpdate, destination, check string) {
	t := f.t
	t.Helper()
	if result := f.request("prepare/wiki", "cleanup/wiki")[0]; result.Status != "operator-attention" || result.OperatorAction != check || result.RetainedRef != "" {
		t.Fatalf("merge candidate step = %+v, want the refusal %q and no retained candidate", result, check)
	}
	if got, want := u.end(), wmFailed(check); got != want {
		t.Fatalf("wiki update = %swant %s", got, want)
	}
	if got := wrText(t, f.db, `SELECT detail FROM activity WHERE kind='wiki.update_ended' AND subject=?`, u.run); got != "The wiki update failed. "+check {
		t.Fatalf("end record = %q", got)
	}
	if n := dfInt(t, f.db, `SELECT (SELECT COUNT(*) FROM merge_candidates)+(SELECT COUNT(*) FROM merge_operations WHERE finished=0 OR operation<>'prepare')`); n != 0 {
		t.Fatalf("the refused wiki commit has %d candidates, unfinished steps, and merge authorizations; want none", n)
	}
	if refs := srcGit(t, f.workspace, "for-each-ref", "refs/mc/wiki/candidates"); refs != "" {
		t.Fatalf("the Project repository retains a merge candidate of the refused wiki commit: %s", refs)
	}
	if got := f.mainCommit(); got != destination {
		t.Fatalf("culler holds %s, want the unchanged commit %s", got, destination)
	}
	f.request("cleanup/wiki")
	if resp := wmStart(t, f.db, nil); len(resp.Started) != 0 || resp.DueInMS != nil {
		t.Fatalf("start response after the failed wiki update = %+v, want no operation and no wait", resp)
	}
}

// A wiki commit can hold a later commit of culler as a parent and the tree of
// the start commit. The checks of the terminal action compare the wiki commit
// with the start commit, and they pass. The merge candidate removes the source
// change of the later commit, and the check of the merge candidate refuses it.
func TestWikiCommitThatRemovesALaterSourceChangeFailsTheWikiUpdate(t *testing.T) {
	f := newDeliveryFixture(t)
	u, scribe := f.wikiScribe(f.mainCommit())
	destination := f.commitMain("feature.txt", "a source change that culler keeps\n")
	f.submitWiki(u, scribe, f.joinMain(scribe, u.commitPage()))

	f.wantCandidateRefusal(u, destination, "The merge candidate changes a path outside openwiki/: feature.txt.")
	if body, err := os.ReadFile(filepath.Join(f.workspace, "feature.txt")); err != nil || string(body) != "a source change that culler keeps\n" {
		t.Fatalf("the operator checkout lost the source change: %q, %v", body, err)
	}
}

// A later commit of culler changes the body of a project document. The hostile
// wiki commit returns the body of the start commit.
func TestWikiCommitThatReturnsAnEarlierProjectDocumentFailsTheWikiUpdate(t *testing.T) {
	const corrected = "---\ntitle: Test project\ntype: project\ndocument: true\n---\nA Worker corrected this project document.\n"
	f := newDeliveryFixture(t)
	u, scribe := f.wikiScribe(f.mainCommit())
	destination := f.commitMain("openwiki/project.md", corrected)
	f.submitWiki(u, scribe, f.joinMain(scribe, u.commitPage()))

	f.wantCandidateRefusal(u, destination, "The merge candidate changes a protected part of a project document: openwiki/project.md.")
	if body, err := os.ReadFile(filepath.Join(f.workspace, "openwiki/project.md")); err != nil || string(body) != corrected {
		t.Fatalf("the operator checkout lost the corrected project document: %q, %v", body, err)
	}
}

// Main moves after the start commit: a task adds a source file and a project
// document. The check of the merge candidate starts at the destination commit,
// so the wiki commit on the start commit merges.
func TestWikiCommitMergesIntoALaterCommitOfMain(t *testing.T) {
	const report = "---\ntitle: Task report\ntype: Research\ndocument: true\n---\nThe complete report of a task.\n"
	f := newDeliveryFixture(t)
	u, scribe := f.wikiScribe(f.mainCommit())
	f.commitMain("feature.txt", "a source change of a task\n")
	destination := f.commitMain("openwiki/report.md", report)
	f.submitWiki(u, scribe, u.commitPage())

	result := f.request("prepare/wiki", "cleanup/wiki")[0]
	candidate := result.Candidate
	if result.Status != "" || candidate == nil || candidate.Status != "merged" || candidate.SubmittedCommit != u.commit || candidate.DestinationCommit != destination {
		t.Fatalf("merge candidate step = %+v, want the wiki commit %s merged at %s", result, u.commit, destination)
	}
	if applied := f.request("apply/wiki", "cleanup/wiki")[0].Applied; applied == nil || applied.Commit != candidate.Commit {
		t.Fatalf("merge = %+v, want the candidate commit %s", applied, candidate.Commit)
	}
	if got, want := u.end(), wmSucceeded(destination, candidate.Commit); got != want {
		t.Fatalf("wiki update after the merge = %swant %s", got, want)
	}
	if got := f.mainCommit(); got != candidate.Commit {
		t.Fatalf("culler holds %s, want the candidate commit %s", got, candidate.Commit)
	}
	for name, want := range map[string]string{"feature.txt": "a source change of a task\n", "openwiki/report.md": report,
		"openwiki/flow.md": "---\ntitle: Request flow\ntype: Component\n---\nA generated explanation.\n"} {
		if body := srcGit(t, f.workspace, "show", "culler:"+name); body != strings.TrimSpace(want) {
			t.Fatalf("culler holds %q as %s, want %q", body, name, want)
		}
	}
}

// The repository of a Scribe run starts at the start commit, also while culler
// holds a later commit. The Scribe cannot join a later commit of culler to the
// wiki commit.
func TestScribeRepositoryHoldsNoCommitAfterTheStartCommit(t *testing.T) {
	f := newDeliveryFixture(t)
	start := f.mainCommit()
	later := f.commitMain("feature.txt", "a source change after the start commit\n")
	run := wrNewRun(t)
	wrFresh(t, f.db, "ws-test", run, start)

	// The resident passes the destination commit of the allocation to the next step.
	envelope := RepositoryEnvelope{Operation: "allocate", WikiUpdate: run, RunID: run, WikiOnly: true, WorkspaceRoot: f.workspace,
		RunRoot: f.wikiRoot(run), InputCommit: start, DestinationBranch: "culler"}
	allocated := f.native(envelope)
	f.record("ready", run, allocated)
	envelope.Operation, envelope.DestinationCommit = "ready", allocated.DestinationCommit
	ready := f.native(envelope)
	f.record("ready", run, ready)
	for step, result := range map[string]RepositoryResult{"allocation": allocated, "repository": ready} {
		if result.InputCommit != start || result.DestinationCommit != start {
			t.Fatalf("%s of the Scribe run = %+v, want the start commit %s as the input commit and as the destination commit", step, result, start)
		}
	}

	repo := filepath.Join(f.wikiRoot(run), "repo")
	if got := srcGit(t, repo, "rev-parse", repository.DestinationRef); got != start {
		t.Fatalf("%s = %s, want the start commit %s", repository.DestinationRef, got, start)
	}
	for _, object := range []string{later, later + "^{tree}", later + ":feature.txt"} {
		id := srcGit(t, f.workspace, "rev-parse", object)
		if exec.Command("git", "-C", repo, "cat-file", "-e", id).Run() == nil {
			t.Fatalf("the repository of the Scribe run holds %s of the later commit of culler", object)
		}
	}
}

// A Git process that fails in the check of the merge candidate gives no check
// result. The step returns the error, with no refusal and no retained candidate.
func TestFailedGitProcessInTheCandidateCheckIsNoRefusal(t *testing.T) {
	f := newDeliveryFixture(t)
	start, commit := f.scribeCommit(exScribe)
	envelope := f.wikiCandidate(exScribe, exStep, start, commit)
	refs := f.mcRefs()

	wcFailingGit(t, "diff")
	result, err := executeRepository(context.Background(), envelope)
	if err == nil || !strings.Contains(err.Error(), "git diff") || result.Status != "" || result.RetainedRef != "" {
		t.Fatalf("merge candidate step with a failed Git process = %+v, %v; want the Git error with no refusal", result, err)
	}
	if got := f.mcRefs(); got != refs {
		t.Fatalf("the failed step changed the references:\n%s\nwant\n%s", got, refs)
	}
	if retained := f.native(envelope); retained.Status != "" || retained.RetainedRef != "refs/mc/wiki/candidates/"+exStep {
		t.Fatalf("second merge candidate step = %+v, want the retained candidate", retained)
	}
}
