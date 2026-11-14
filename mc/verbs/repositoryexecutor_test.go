package verbs

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"mc/repository"
)

func TestRepositoryCommandHonorsDeadline(t *testing.T) {
	shim := t.TempDir()
	writeFile(t, filepath.Join(shim, "git"), "#!/bin/sh\nexec sleep 30\n")
	if err := os.Chmod(filepath.Join(shim, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	workspace := t.TempDir()
	envelope := func(deadline int64) string {
		body, err := json.Marshal(RepositoryEnvelope{Operation: "observe", TaskID: 1, WorkspaceRoot: workspace, DestinationBranch: "culler", DeadlineMS: deadline})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "envelope.json")
		writeFile(t, path, string(body))
		return path
	}

	started := time.Now()
	if _, err := ExecuteRepository(context.Background(), envelope(200), nil); err == nil {
		t.Fatal("the native operation succeeded after its deadline")
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("the deadline stopped the Git process after %v", elapsed)
	}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	started = time.Now()
	if _, err := ExecuteRepository(ctx, envelope(0), nil); err == nil {
		t.Fatal("the native operation succeeded after its abort")
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("the abort stopped the Git process after %v", elapsed)
	}
}

func TestRepositoryBytesSkipsRemovedEntries(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "0123456789")
	writeFile(t, filepath.Join(root, "b.txt"), "removed during the walk")
	if err := os.MkdirAll(filepath.Join(root, "c", "d"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "c", "d", "e.txt"), "removed with its directory")
	previous := walkDir
	t.Cleanup(func() { walkDir = previous })
	walkDir = func(start string, fn fs.WalkDirFunc) error {
		return filepath.WalkDir(start, func(path string, entry fs.DirEntry, err error) error {
			if path == filepath.Join(root, "a.txt") {
				// Another operation removes a file and a directory during the walk.
				if err := os.Remove(filepath.Join(root, "b.txt")); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(filepath.Join(root, "c")); err != nil {
					t.Fatal(err)
				}
			}
			return fn(path, entry, err)
		})
	}
	size, err := repositoryBytes(root)
	if err != nil || size != 10 {
		t.Fatalf("the size walk returned %d, %v", size, err)
	}
	if _, err := repositoryBytes(filepath.Join(root, "absent")); err == nil {
		t.Fatal("the size walk accepted an absent root")
	}
}

// The run identifiers of the executor tests of a wiki update.
const (
	exScribe = "0123456789abcdef"
	exStep   = "fedcba9876543210"
)

// scribeCommit builds the isolated repository of a Scribe run at the commit of
// culler, commits one wiki change in it, and captures that wiki commit. It
// returns the start commit and the wiki commit.
func (f *deliveryFixture) scribeCommit(run string) (start, commit string) {
	f.t.Helper()
	start = srcGit(f.t, f.workspace, "rev-parse", "culler")
	envelope := RepositoryEnvelope{Operation: "allocate", WikiUpdate: run, RunID: run, WikiOnly: true, WorkspaceRoot: f.workspace,
		RunRoot: f.wikiRoot(run), InputCommit: start, DestinationBranch: "culler"}
	if allocated := f.native(envelope); allocated.WikiUpdate != run || allocated.TaskID != 0 || allocated.InputCommit != start || allocated.DestinationCommit != start {
		f.t.Fatalf("allocation of the Scribe repository = %+v, want the wiki update at %s", allocated, start)
	}
	envelope.Operation = "ready"
	f.native(envelope)
	repo := filepath.Join(f.wikiRoot(run), "repo")
	if branch := srcGit(f.t, repo, "rev-parse", "--abbrev-ref", "HEAD"); branch != "mc/wiki-update" {
		f.t.Fatalf("local branch of the Scribe repository = %s, want mc/wiki-update", branch)
	}
	writeFile(f.t, filepath.Join(repo, "openwiki/flow.md"), "---\ntitle: Request flow\ntype: Component\n---\nA generated explanation.\n")
	srcGit(f.t, repo, "add", "openwiki")
	srcGit(f.t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "Update the Project wiki")
	commit = srcGit(f.t, repo, "rev-parse", "HEAD")
	capture := RepositoryEnvelope{Operation: "capture", WikiUpdate: run, RunID: run, WorkspaceRoot: f.workspace, RunRoot: f.wikiRoot(run),
		InputCommit: start, DestinationBranch: "culler", ResultCommit: commit}
	withKind := capture
	withKind.SubmissionKind = "checkpoint"
	if _, err := executeRepository(context.Background(), withKind); !wcRefused(err, "", "the capture of a wiki commit has no submission kind") {
		f.t.Fatalf("capture of a wiki commit with a submission kind = %v, want a refusal", err)
	}
	if captured := f.native(capture); captured.RetainedRef != "refs/mc/wiki/commits/"+run || captured.ResultCommit != commit || captured.WikiUpdate != run {
		f.t.Fatalf("capture of the wiki commit = %+v, want the reference refs/mc/wiki/commits/%s at %s", captured, run, commit)
	}
	return start, commit
}

// wikiCandidate runs the native steps of one merge candidate step of a wiki
// commit up to its candidate. It returns the envelope that retains the candidate.
func (f *deliveryFixture) wikiCandidate(wiki, step, start, commit string) RepositoryEnvelope {
	f.t.Helper()
	envelope := RepositoryEnvelope{Operation: "allocate", WikiUpdate: wiki, RunID: step, WorkspaceRoot: f.workspace, RunRoot: f.wikiRoot(step),
		InputCommit: commit, StartCommit: start, DestinationBranch: "culler", AuthorName: "Test", AuthorEmail: "test@example.invalid",
		artifactRoots: func() (map[string]string, error) {
			f.t.Error("the merge candidate step of a wiki commit read the artifact roots")
			return nil, nil
		}}
	envelope.DestinationCommit = f.native(envelope).DestinationCommit
	envelope.Operation = "prepare-repository"
	f.native(envelope)
	envelope.Operation = "prepare"
	prepared := f.native(envelope)
	if prepared.Candidate == nil || prepared.Candidate.Status != "merged" {
		f.t.Fatalf("merge candidate step = %+v, want a merged candidate", prepared)
	}
	envelope.Operation, envelope.Candidate = "retain-candidate", prepared.Candidate
	return envelope
}

// mcRefs lists each reference below refs/mc/ in the Project repository, as name and commit.
func (f *deliveryFixture) mcRefs() string {
	f.t.Helper()
	return srcGit(f.t, f.workspace, "for-each-ref", "--format=%(refname) %(objectname)", "refs/mc/")
}

// Each reference of a wiki update is below refs/mc/wiki/ and holds a run
// identifier. A candidate of a wiki commit has no change data.
func TestExecutorWikiReferences(t *testing.T) {
	f := newDeliveryFixture(t)
	start, commit := f.scribeCommit(exScribe)
	// The culler branch moves after the start. The candidate is a merge commit.
	destination := f.commitMain("source.txt", "A source change of a task.\n")
	retained := f.native(f.wikiCandidate(exScribe, exStep, start, commit))
	c := retained.Candidate
	if retained.Status != "" || retained.RetainedRef != "refs/mc/wiki/candidates/"+exStep || retained.WikiUpdate != exScribe || retained.TaskID != 0 {
		t.Fatalf("retained candidate = %+v, want the reference refs/mc/wiki/candidates/%s", retained, exStep)
	}
	if c.Changes != nil || c.SubmittedCommit != commit || c.DestinationCommit != destination || c.Commit == commit || c.Commit == destination {
		t.Fatalf("candidate = %+v, want a merge of %s into %s with no change data", *c, commit, destination)
	}
	want := "refs/mc/wiki/candidates/" + exStep + " " + c.Commit + "\n" +
		"refs/mc/wiki/commits/" + exScribe + " " + commit + "\n" +
		"refs/mc/wiki/inputs/" + exScribe + " " + start + "\n" +
		"refs/mc/wiki/inputs/" + exStep + " " + commit
	if got := f.mcRefs(); got != want {
		t.Fatalf("references of the wiki update =\n%s\nwant\n%s", got, want)
	}

	// A wiki update has no destination observation.
	for _, operation := range []string{"observe"} {
		_, err := executeRepository(context.Background(), RepositoryEnvelope{Operation: operation, WikiUpdate: exScribe, WorkspaceRoot: f.workspace, DestinationBranch: "culler"})
		if !wcRefused(err, "", "the repository operation is not valid for a wiki update") {
			t.Fatalf("%s of a wiki update = %v, want a refusal", operation, err)
		}
	}
	// An envelope names one task or one wiki update.
	for name, envelope := range map[string]RepositoryEnvelope{
		"no subject":   {Operation: "observe", WorkspaceRoot: f.workspace, DestinationBranch: "culler"},
		"two subjects": {Operation: "observe", TaskID: 1, WikiUpdate: exScribe, WorkspaceRoot: f.workspace, DestinationBranch: "culler"},
	} {
		if _, err := executeRepository(context.Background(), envelope); !wcRefused(err, "", "the repository operation requires its task or its wiki update") {
			t.Fatalf("envelope with %s = %v, want a refusal", name, err)
		}
	}
	if got := f.mcRefs(); got != want {
		t.Fatalf("a refused operation changed the references:\n%s", got)
	}
}

// The envelope and the result of a wiki update name the wiki update in one
// field. The result of a task holds its task and no wiki update.
func TestWikiEnvelopeAndResultFields(t *testing.T) {
	f := newDeliveryFixture(t)
	start, commit := f.scribeCommit(exScribe)
	path := filepath.Join(t.TempDir(), "envelope.json")
	writeFile(t, path, fmt.Sprintf(`{"operation":"allocate","wiki_update":%q,"run_id":%q,"workspace_root":%q,"run_root":%q,"input_commit":%q,"start_commit":%q,
		"destination_branch":"culler","max_temporary_bytes":21474836480,"author_name":"Test","author_email":"test@example.invalid","deadline_ms":600000}`,
		exScribe, exStep, f.workspace, f.wikiRoot(exStep), commit, start))
	result, err := ExecuteRepository(context.Background(), path, nil)
	if err != nil {
		t.Fatalf("allocation from the envelope file: %v", err)
	}
	body, err := json.Marshal(result)
	want := fmt.Sprintf(`{"wiki_update":%q,"input_commit":%q,"destination_commit":%q,"result_commit":"","retained_ref":"","temporary_bytes":0,"status":""}`, exScribe, commit, start)
	if err != nil || string(body) != want {
		t.Fatalf("result of a wiki update = %s, %v; want %s", body, err, want)
	}
	if parsed, err := RepositoryResultJSON(want); err != nil || !reflect.DeepEqual(parsed, result) || !parsed.Subject().Wiki() {
		t.Fatalf("parsed result = %+v, %v; want %+v", parsed, err, result)
	}
	if got := srcGit(t, f.workspace, "rev-parse", "refs/mc/wiki/inputs/"+exStep); got != commit {
		t.Fatalf("the input reference of the step holds %s, want the wiki commit %s", got, commit)
	}

	task, err := json.Marshal(RepositoryResult{TaskID: 7, InputCommit: start})
	if want := fmt.Sprintf(`{"task_id":7,"input_commit":%q,"destination_commit":"","result_commit":"","retained_ref":"","temporary_bytes":0,"status":""}`, start); err != nil || string(task) != want {
		t.Fatalf("result of a task = %s, %v; want %s", task, err, want)
	}
}

// The disk limit counts the temporary repositories of tasks and of wiki
// updates in separate folders.
func TestWikiRepositoryDoesNotRefuseATaskAllocation(t *testing.T) {
	f := newDeliveryFixture(t)
	start := srcGit(t, f.workspace, "rev-parse", "culler")
	const limit = 8 << 20
	// The temporary repositories of wiki updates hold as many bytes as the limit.
	full := f.wikiRoot(exScribe)
	if err := os.MkdirAll(full, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, "objects"), make([]byte, limit), 0o600); err != nil {
		t.Fatal(err)
	}
	wiki := RepositoryEnvelope{Operation: "allocate", WikiUpdate: exScribe, RunID: exStep, WorkspaceRoot: f.workspace, RunRoot: f.wikiRoot(exStep),
		InputCommit: start, DestinationBranch: "culler", MaxTemporaryBytes: limit}
	if _, err := executeRepository(context.Background(), wiki); !wcRefused(err, "", "the temporary disk limit prevents another repository") {
		t.Fatalf("allocation of a wiki repository at the limit = %v, want a refusal", err)
	}
	task := RepositoryEnvelope{Operation: "allocate", TaskID: 1, RunID: "task-run", WorkspaceRoot: f.workspace, RunRoot: filepath.Join(f.root, "task-run"),
		InputCommit: start, DestinationBranch: "culler", MaxTemporaryBytes: limit}
	if allocated := f.native(task); allocated.InputCommit != start || allocated.TaskID != 1 {
		t.Fatalf("allocation of a task repository = %+v, want the input commit %s", allocated, start)
	}
	if got, want := f.mcRefs(), "refs/mc/tasks/1/inputs/task-run "+start; got != want {
		t.Fatalf("references after the allocations = %q, want %q", got, want)
	}
}

// The folder of a run root must be the folder of its subject.
func TestExecutorRefusesARunRootOfTheOtherSubject(t *testing.T) {
	f := newDeliveryFixture(t)
	start := srcGit(t, f.workspace, "rev-parse", "culler")
	for _, operation := range []string{"allocate", "ready", "prepare-repository"} {
		for name, envelope := range map[string]RepositoryEnvelope{
			"a wiki update in the folder of the tasks": {Operation: operation, WikiUpdate: exScribe, RunID: exScribe, WorkspaceRoot: f.workspace,
				RunRoot: filepath.Join(f.root, exScribe), InputCommit: start, DestinationBranch: "culler"},
			"a task in the folder of the wiki updates": {Operation: operation, TaskID: 1, RunID: "task-run", WorkspaceRoot: f.workspace,
				RunRoot: f.wikiRoot("task-run"), InputCommit: start, DestinationBranch: "culler"},
		} {
			if _, err := executeRepository(context.Background(), envelope); !wcRefused(err, "", "the run root does not match the subject of the repository operation") {
				t.Fatalf("%s of %s = %v, want a refusal", operation, name, err)
			}
			if _, err := os.Stat(envelope.RunRoot); !os.IsNotExist(err) {
				t.Fatalf("%s of %s created the run root: %v", operation, name, err)
			}
		}
	}
	if refs := f.mcRefs(); refs != "" {
		t.Fatalf("the refused operations wrote references:\n%s", refs)
	}
}

// A merge of the wiki commit into a culler branch that does not hold the start
// commit returns each commit that the operator removed. The executor keeps no
// candidate for that merge.
func TestExecutorRefusesAWikiCandidateWithoutTheStartCommit(t *testing.T) {
	f := newDeliveryFixture(t)
	before := srcGit(t, f.workspace, "rev-parse", "culler")
	f.commitMain("source.txt", "A source change that the operator removes.\n")
	start, commit := f.scribeCommit(exScribe)
	// The operator removes the start commit from culler and adds another commit.
	srcGit(t, f.workspace, "reset", "-q", "--hard", before)
	destination := f.commitMain("other.txt", "Another source change.\n")
	refs := f.mcRefs()

	envelope := f.wikiCandidate(exScribe, exStep, start, commit)
	if (repository.Git{Root: f.workspace}).Ancestor(context.Background(), start, destination) {
		t.Fatal("the fixture kept the start commit on culler")
	}
	inputs := refs + "\nrefs/mc/wiki/inputs/" + exStep + " " + commit
	discarded := f.native(envelope)
	if discarded.Status != "operator-attention" || discarded.OperatorAction != "The culler branch does not hold the start commit of the wiki update. Culler discarded the wiki commit." || discarded.RetainedRef != "" {
		t.Fatalf("candidate on a culler branch with no start commit = %+v, want operator-attention with the start commit text and no reference", discarded)
	}
	if got := f.mcRefs(); got != inputs {
		t.Fatalf("references after the discarded candidate =\n%s\nwant\n%s", got, inputs)
	}

	// An envelope with no start commit cannot prove the rule.
	envelope.StartCommit = ""
	if _, err := executeRepository(context.Background(), envelope); !wcRefused(err, "", "the merge candidate of a wiki commit requires its start commit") {
		t.Fatalf("candidate with no start commit = %v, want a refusal", err)
	}
	if got := f.mcRefs(); got != inputs {
		t.Fatalf("the refused candidate changed the references:\n%s", got)
	}
}
