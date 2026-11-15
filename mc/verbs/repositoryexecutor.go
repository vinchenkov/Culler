package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mc/repository"
	"mc/wiki"
)

// RepositoryEnvelope is the input of one native repository operation. Its
// subject is one task or one wiki update.
type RepositoryEnvelope struct {
	WikiOnly          bool                    `json:"wiki_only,omitempty"`
	Operation         string                  `json:"operation"`
	TaskID            int64                   `json:"task_id,omitempty"`
	WikiUpdate        string                  `json:"wiki_update,omitempty"`
	RunID             string                  `json:"run_id"`
	WorkspaceRoot     string                  `json:"workspace_root"`
	RunRoot           string                  `json:"run_root"`
	InputCommit       string                  `json:"input_commit"`
	StartCommit       string                  `json:"start_commit,omitempty"`
	DestinationBranch string                  `json:"destination_branch"`
	DestinationCommit string                  `json:"destination_commit,omitempty"`
	ResultCommit      string                  `json:"result_commit,omitempty"`
	SubmissionKind    string                  `json:"submission_kind,omitempty"`
	VersionID         *int64                  `json:"version_id,omitempty"`
	Candidate         *repository.Candidate   `json:"candidate,omitempty"`
	Application       *repository.Application `json:"application,omitempty"`
	MaxTemporaryBytes int64                   `json:"max_temporary_bytes,omitempty"`
	AuthorName        string                  `json:"author_name,omitempty"`
	AuthorEmail       string                  `json:"author_email,omitempty"`
	DeadlineMS        int64                   `json:"deadline_ms,omitempty"`
	// artifactRoots maps each mount allowlist target to its host directory.
	artifactRoots func() (map[string]string, error)
}

// ExecuteRepository runs one native repository operation. A cancelled context
// or the deadline of the envelope ends each Git child process.
func ExecuteRepository(ctx context.Context, path string, artifactRoots func() (map[string]string, error)) (RepositoryResult, error) {
	var envelope RepositoryEnvelope
	data, err := os.ReadFile(path)
	if err != nil {
		return RepositoryResult{}, err
	}
	if err := decodeStrictJSON(strings.NewReader(string(data)), &envelope); err != nil {
		return RepositoryResult{}, err
	}
	envelope.artifactRoots = artifactRoots
	if envelope.DeadlineMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(envelope.DeadlineMS)*time.Millisecond)
		defer cancel()
	}
	return executeRepository(ctx, envelope)
}

// wikiRepositories names the folder of the temporary repositories of wiki
// updates. The temporary repositories of tasks have another folder. The disk
// limit counts the sizes below the parent folder of a run root, so each
// folder has its own limit.
const wikiRepositories = "wiki-repositories"

// wikiStartCommitText is the operator text of a merge candidate of a wiki
// commit whose destination commit does not hold the start commit.
const wikiStartCommitText = "The culler branch does not hold the start commit of the wiki update. Culler discarded the wiki commit."

// wikiCandidateCheck returns the text of the scope check that the merge
// candidate of a wiki commit fails. The check reads the merge candidate
// repository, which no agent writes.
func wikiCandidateCheck(ctx context.Context, candidates repository.Git, c repository.Candidate) (string, error) {
	err := wiki.ValidateMergeCandidate(ctx, candidates, c.DestinationCommit, c.Commit)
	var failed *wiki.CheckError
	if errors.As(err, &failed) {
		return failed.Text, nil
	}
	return "", err
}

func taskWikiCandidateCheck(ctx context.Context, candidates repository.Git, c repository.Candidate) error {
	return wiki.ValidateTaskCommit(ctx, candidates, c.DestinationCommit, c.DestinationCommit, c.Commit)
}

// retainedRef names the reference that retains one commit of the run:
// refs/mc/tasks/<id>/<kind>/<run_id> for a task, and
// refs/mc/wiki/<kind>/<run_id> for a wiki update.
func (e RepositoryEnvelope) retainedRef(kind string) string {
	if e.WikiUpdate != "" {
		return fmt.Sprintf("refs/mc/wiki/%s/%s", kind, e.RunID)
	}
	return fmt.Sprintf("refs/mc/tasks/%d/%s/%s", e.TaskID, kind, e.RunID)
}

func executeRepository(ctx context.Context, e RepositoryEnvelope) (RepositoryResult, error) {
	r := RepositoryResult{WikiUpdate: e.WikiUpdate, TaskID: e.TaskID, InputCommit: e.InputCommit, VersionID: e.VersionID}
	if e.TaskID < 0 || !r.Subject().Valid() {
		return r, Domainf("the repository operation requires its task or its wiki update")
	}
	wiki := r.Subject().Wiki()
	if !filepath.IsAbs(e.WorkspaceRoot) || e.WorkspaceRoot == "/" {
		return r, Domainf("the Project repository path is invalid")
	}
	g := repository.Git{Root: e.WorkspaceRoot}
	if e.Operation == "reconcile" {
		if e.Application == nil {
			return r, Domainf("the reconciliation requires its recorded authorization")
		}
		applied, err := repository.ReconcileApplication(ctx, e.WorkspaceRoot, *e.Application)
		if errors.Is(err, repository.ErrOperatorCheckout) {
			r.Status, r.OperatorAction = "operator-attention", err.Error()
			return r, nil
		}
		if err != nil {
			return r, err
		}
		if applied != nil {
			r.Applied = applied
			return r, nil
		}
		r.Status = "not-applied"
		if wiki {
			// The record compares the live commit with the destination commit
			// of the authorized candidate.
			r.DestinationCommit, err = g.Branch(ctx, e.DestinationBranch)
		}
		return r, err
	}
	if e.Operation == "apply" {
		if e.Application == nil {
			return r, Domainf("the application requires its recorded authorization")
		}
		applied, err := repository.Apply(ctx, e.WorkspaceRoot, *e.Application)
		if errors.Is(err, repository.ErrDestinationChanged) {
			r.Status = "destination-changed"
			r.DestinationCommit, err = g.Branch(ctx, e.DestinationBranch)
			return r, err
		}
		if errors.Is(err, repository.ErrOperatorCheckout) {
			r.Status = "operator-attention"
			r.OperatorAction = err.Error()
			return r, nil
		}
		r.Applied = &applied
		return r, err
	}
	if wiki && e.Operation == "observe" {
		return r, Domainf("the repository operation is not valid for a wiki update")
	}
	if e.Operation == "observe" {
		var err error
		r.DestinationCommit, err = g.Branch(ctx, e.DestinationBranch)
		return r, err
	}
	if e.RunID == "" || strings.ContainsAny(e.RunID, "/\\\n\r\x00") || !filepath.IsAbs(e.RunRoot) || filepath.Base(e.RunRoot) != e.RunID {
		return r, Domainf("the temporary repository requires its run directory")
	}
	if e.RunRoot == e.WorkspaceRoot || strings.HasPrefix(e.WorkspaceRoot, e.RunRoot+string(filepath.Separator)) {
		return r, Domainf("the temporary repository overlaps the Project repository")
	}
	root := filepath.Join(e.RunRoot, "repo")
	switch e.Operation {
	case "allocate", "ready", "prepare-repository":
		if (filepath.Base(filepath.Dir(e.RunRoot)) == wikiRepositories) != wiki {
			return r, Domainf("the run root does not match the subject of the repository operation")
		}
		destination := e.DestinationCommit
		var err error
		if destination == "" {
			destination, err = g.Branch(ctx, e.DestinationBranch)
		}
		if err != nil {
			return r, err
		}
		input := e.InputCommit
		if input == "" {
			input = destination
		}
		// The repository of a Scribe run holds no commit of culler after its
		// start commit.
		if e.WikiOnly {
			destination = input
		}
		estimate, err := g.RequiredBytes(ctx, input, destination)
		if err != nil {
			return r, err
		}
		if err := repository.RequireFreeSpace(filepath.Dir(e.RunRoot), estimate); err != nil {
			return r, err
		}
		if e.MaxTemporaryBytes > 0 {
			total, err := repositoryBytes(filepath.Dir(e.RunRoot))
			if err != nil && !os.IsNotExist(err) {
				return r, err
			}
			if total >= e.MaxTemporaryBytes || estimate > e.MaxTemporaryBytes-total {
				return r, Domainf("the temporary disk limit prevents another repository")
			}
		}
		if e.Operation == "allocate" {
			r.InputCommit, r.DestinationCommit = input, destination
			err = g.Retain(ctx, e.WorkspaceRoot, input, e.retainedRef("inputs"))
			return r, err
		}
		if err := os.MkdirAll(e.RunRoot, 0700); err != nil {
			return r, err
		}
		run, err := repository.Isolated(ctx, e.WorkspaceRoot, root, input, destination)
		if err != nil {
			return r, err
		}
		branch := fmt.Sprintf("mc/task-%d", e.TaskID)
		if wiki {
			branch = "mc/wiki-update"
		}
		if err := run.LocalBranch(ctx, branch, input); err != nil {
			return r, err
		}
		// The agent finds the start of its change against this reference.
		if err := run.Reference(ctx, repository.DestinationRef, destination); err != nil {
			return r, err
		}
		if err := g.Retain(ctx, root, input, e.retainedRef("inputs")); err != nil {
			return r, err
		}
		r.InputCommit, r.DestinationCommit = input, destination
		r.TemporaryBytes, err = repositoryBytes(e.RunRoot)
		if err != nil {
			return r, err
		}
		if e.MaxTemporaryBytes > 0 {
			total, err := repositoryBytes(filepath.Dir(e.RunRoot))
			if err != nil {
				return r, err
			}
			if total > e.MaxTemporaryBytes {
				return r, Domainf("the temporary repositories exceed the configured disk limit")
			}
		}
		if e.WikiOnly {
			for _, name := range []string{"openwiki", ".claude"} {
				dir := filepath.Join(root, name)
				if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
					return r, err
				}
				info, err := os.Lstat(dir)
				if err != nil || !info.IsDir() {
					return r, Domainf("the Scribe requires a regular repository directory: %s", name)
				}
			}
		}
		if err := makeRunWritable(e.RunRoot); err != nil {
			return r, err
		}
		return r, nil
	case "capture":
		if !repository.ValidCommit(e.InputCommit) || !repository.ValidCommit(e.ResultCommit) {
			return r, Domainf("the submission requires exact input and result commits")
		}
		run := repository.Git{Root: root}
		if !run.Ancestor(ctx, e.InputCommit, e.ResultCommit) {
			return r, Domainf("the submitted version must preserve the assigned input commit")
		}
		switch {
		case wiki && e.SubmissionKind != "":
			return r, Domainf("the capture of a wiki commit has no submission kind")
		case wiki:
			r.RetainedRef = e.retainedRef("commits")
		case e.SubmissionKind == "checkpoint":
			r.RetainedRef = e.retainedRef("checkpoints")
		default:
			r.RetainedRef = e.retainedRef("versions")
		}
		r.ResultCommit = e.ResultCommit
		return r, g.Retain(ctx, root, e.ResultCommit, r.RetainedRef)
	case "prepare":
		if !repository.ValidCommit(e.DestinationCommit) {
			return r, Domainf("merge preparation requires its recorded destination commit")
		}
		c, err := repository.Prepare(ctx, repository.Git{Root: root}, e.InputCommit, e.DestinationBranch, e.DestinationCommit, e.AuthorName, e.AuthorEmail)
		r.Candidate = &c
		return r, err
	case "retain-candidate":
		if e.Candidate == nil {
			return r, Domainf("the candidate result is absent")
		}
		r.Candidate = e.Candidate
		if e.Candidate.Status == "merged" {
			if err := e.Candidate.Validate(); err != nil {
				return r, err
			}
			candidateRepository := repository.Git{Root: root}
			if !candidateRepository.Ancestor(ctx, e.Candidate.SubmittedCommit, e.Candidate.Commit) || !candidateRepository.Ancestor(ctx, e.Candidate.DestinationCommit, e.Candidate.Commit) {
				return r, Domainf("the candidate must preserve both recorded inputs")
			}
			if wiki {
				if !repository.ValidCommit(e.StartCommit) {
					return r, Domainf("the merge candidate of a wiki commit requires its start commit")
				}
				// A destination that does not hold the start commit lost commits
				// after the start. A merge of the wiki commit returns them.
				if !g.Ancestor(ctx, e.StartCommit, e.Candidate.DestinationCommit) {
					r.Status, r.OperatorAction = "operator-attention", wikiStartCommitText
					return r, nil
				}
				check, err := wikiCandidateCheck(ctx, candidateRepository, *e.Candidate)
				if err != nil {
					return r, err
				}
				if check != "" {
					r.Status, r.OperatorAction = "operator-attention", check
					return r, nil
				}
			} else if err := taskWikiCandidateCheck(ctx, candidateRepository, *e.Candidate); err != nil {
				return r, err
			}
			r.RetainedRef = e.retainedRef("candidates")
			if err := g.Retain(ctx, root, e.Candidate.Commit, r.RetainedRef); err != nil {
				return r, err
			}
			if err := repository.Preflight(ctx, e.WorkspaceRoot, *e.Candidate); err != nil {
				if errors.Is(err, repository.ErrDestinationChanged) {
					r.Status = "destination-changed"
					r.DestinationCommit, err = g.Branch(ctx, e.DestinationBranch)
					return r, err
				}
				if errors.Is(err, repository.ErrOperatorCheckout) {
					r.Status = "operator-attention"
					r.OperatorAction = err.Error()
					return r, nil
				}
				return r, err
			}
			// A candidate of a wiki commit has no change data.
			if wiki {
				return r, nil
			}
			if e.artifactRoots == nil {
				return r, Domainf("the change data requires the artifact roots")
			}
			roots, err := e.artifactRoots()
			if err != nil {
				return r, err
			}
			candidate := *e.Candidate
			candidate.Changes, err = storeChangeData(ctx, g, roots, e.WorkspaceRoot, e.TaskID, e.RunID, candidate)
			if err != nil {
				return r, err
			}
			r.Candidate = &candidate
		}
		return r, nil
	case "cleanup":
		if err := os.RemoveAll(e.RunRoot); err != nil {
			r.CleanupError = err.Error()
			r.TemporaryBytes, _ = repositoryBytes(e.RunRoot)
		}
		return r, nil
	default:
		return r, Usagef("the repository operation is invalid")
	}
}

// walkDir is a test seam.
var walkDir = filepath.WalkDir

// repositoryBytes adds the size of each regular file under root.
// An entry that another operation removes during the walk adds nothing.
func repositoryBytes(root string) (int64, error) {
	var size int64
	err := walkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if path != root && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		size += info.Size()
		return nil
	})
	return size, err
}

func RepositoryResultJSON(body string) (RepositoryResult, error) {
	var r RepositoryResult
	err := json.Unmarshal([]byte(body), &r)
	return r, err
}

func makeRunWritable(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := os.FileMode(0666)
		if entry.IsDir() || info.Mode()&0111 != 0 {
			mode = 0777
		}
		return os.Chmod(path, mode)
	})
}
