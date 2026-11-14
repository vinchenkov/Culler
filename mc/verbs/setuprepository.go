package verbs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"mc/repository"
)

// SetupRepositoryEnvelope is the input of one host operation on the temporary
// repository of initial Project setup. The setup agent and source acceptance
// work in that repository, so the operator checkout stays unchanged until the
// setup commit reaches the culler branch.
type SetupRepositoryEnvelope struct {
	Operation     string `json:"operation"`
	SetupID       string `json:"setup_id"`
	WorkspaceRoot string `json:"workspace_root"`
	SetupRoot     string `json:"setup_root"`
	Commit        string `json:"commit,omitempty"`
	DeadlineMS    int64  `json:"deadline_ms,omitempty"`
}

// SetupRepositoryResult reports the start commit of the temporary repository
// and, for a delivery, the state of the culler branch.
type SetupRepositoryResult struct {
	Status         string `json:"status"`
	StartCommit    string `json:"start_commit"`
	Commit         string `json:"commit,omitempty"`
	CullerCommit   string `json:"culler_commit,omitempty"`
	OperatorAction string `json:"operator_action,omitempty"`
}

var setupIdentity = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// SetupRetainedRef names the reference that keeps the setup commit in the
// Project repository.
func SetupRetainedRef(setup string) string { return "refs/mc/setup/" + setup }

// ExecuteSetupRepository runs one operation from an envelope file.
func ExecuteSetupRepository(ctx context.Context, path string) (SetupRepositoryResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SetupRepositoryResult{}, err
	}
	var envelope SetupRepositoryEnvelope
	if err := decodeStrictJSON(strings.NewReader(string(data)), &envelope); err != nil {
		return SetupRepositoryResult{}, err
	}
	if envelope.DeadlineMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(envelope.DeadlineMS)*time.Millisecond)
		defer cancel()
	}
	return executeSetupRepository(ctx, envelope)
}

func executeSetupRepository(ctx context.Context, e SetupRepositoryEnvelope) (SetupRepositoryResult, error) {
	if !setupIdentity.MatchString(e.SetupID) {
		return SetupRepositoryResult{}, Domainf("the Project setup identity is invalid")
	}
	if !filepath.IsAbs(e.WorkspaceRoot) || e.WorkspaceRoot == "/" || !filepath.IsAbs(e.SetupRoot) ||
		filepath.Clean(e.SetupRoot) != e.SetupRoot || filepath.Base(e.SetupRoot) != "setup-"+e.SetupID {
		return SetupRepositoryResult{}, Domainf("the temporary repository of Project setup requires its setup folder")
	}
	if e.SetupRoot == e.WorkspaceRoot || strings.HasPrefix(e.SetupRoot, e.WorkspaceRoot+string(filepath.Separator)) ||
		strings.HasPrefix(e.WorkspaceRoot, e.SetupRoot+string(filepath.Separator)) {
		return SetupRepositoryResult{}, Domainf("the temporary repository of Project setup overlaps the Project repository")
	}
	switch e.Operation {
	case "prepare":
		return prepareSetupRepository(ctx, e)
	case "deliver":
		return deliverSetupCommit(ctx, e)
	default:
		return SetupRepositoryResult{}, Usagef("the setup repository operation is invalid")
	}
}

// prepareSetupRepository creates an independent repository at the tip of the
// culler branch. The checked-out branch and the uncommitted changes of the
// operator checkout do not reach it. A repeated call keeps the existing
// repository, so retries and the continuation keep the work of the setup agent.
func prepareSetupRepository(ctx context.Context, e SetupRepositoryEnvelope) (SetupRepositoryResult, error) {
	source := filepath.Join(e.SetupRoot, "source")
	run := repository.Git{Root: source}
	if _, err := os.Lstat(filepath.Join(source, ".git")); err == nil {
		if start, err := run.Commit(ctx, repository.DestinationRef); err == nil {
			return SetupRepositoryResult{Status: "prepared", StartCommit: start}, nil
		}
		// An interrupted preparation left no start reference.
		if err := os.RemoveAll(source); err != nil {
			return SetupRepositoryResult{}, err
		}
	} else if _, err := os.Lstat(source); err == nil {
		if err := os.RemoveAll(source); err != nil {
			return SetupRepositoryResult{}, err
		}
	}
	start, err := (repository.Git{Root: e.WorkspaceRoot}).Branch(ctx, "culler")
	if err != nil {
		return SetupRepositoryResult{}, Domainf("Project setup requires the culler branch: %v", err)
	}
	if err := os.MkdirAll(e.SetupRoot, 0700); err != nil {
		return SetupRepositoryResult{}, err
	}
	run, err = repository.Isolated(ctx, e.WorkspaceRoot, source, start)
	if err != nil {
		return SetupRepositoryResult{}, err
	}
	if err := run.LocalBranch(ctx, "culler", start); err != nil {
		return SetupRepositoryResult{}, err
	}
	// The setup agent writes this repository as the container user.
	if err := makeRunWritable(source); err != nil {
		return SetupRepositoryResult{}, err
	}
	// The start reference comes last: its presence marks a complete repository.
	if err := run.Reference(ctx, repository.DestinationRef, start); err != nil {
		return SetupRepositoryResult{}, err
	}
	return SetupRepositoryResult{Status: "prepared", StartCommit: start}, nil
}

// deliverSetupCommit moves the culler branch from the start commit to the
// accepted setup commit. The apply operation of Git delivery updates an
// operator checkout of the culler branch and keeps its local changes.
func deliverSetupCommit(ctx context.Context, e SetupRepositoryEnvelope) (SetupRepositoryResult, error) {
	if !repository.ValidCommit(e.Commit) {
		return SetupRepositoryResult{}, Domainf("the delivery requires the accepted setup commit")
	}
	source := filepath.Join(e.SetupRoot, "source")
	run := repository.Git{Root: source}
	start, err := run.Commit(ctx, repository.DestinationRef)
	if err != nil {
		return SetupRepositoryResult{}, Domainf("the temporary repository of Project setup is unavailable: %v", err)
	}
	result := SetupRepositoryResult{StartCommit: start, Commit: e.Commit}
	if !run.Ancestor(ctx, start, e.Commit) {
		return result, Domainf("the setup commit does not follow the start commit of Project setup")
	}
	project := repository.Git{Root: e.WorkspaceRoot}
	if e.Commit == start {
		live, err := project.Branch(ctx, "culler")
		if err != nil {
			return result, err
		}
		result.CullerCommit = live
		if live != start {
			result.Status = "destination-changed"
			return result, nil
		}
		result.Status = "delivered"
		return result, nil
	}
	if err := project.Retain(ctx, source, e.Commit, SetupRetainedRef(e.SetupID)); err != nil {
		return result, err
	}
	applied, err := repository.Apply(ctx, e.WorkspaceRoot, repository.Application{
		Authorization: "setup-" + e.SetupID,
		Candidate: repository.Candidate{Status: "merged", Commit: e.Commit, SubmittedCommit: e.Commit,
			DestinationCommit: start, DestinationBranch: "culler"},
	})
	switch {
	case errors.Is(err, repository.ErrDestinationChanged):
		result.Status = "destination-changed"
		result.CullerCommit, err = project.Branch(ctx, "culler")
		return result, err
	case errors.Is(err, repository.ErrOperatorCheckout):
		result.Status, result.OperatorAction = "operator-attention", err.Error()
		return result, nil
	case err != nil:
		return result, err
	}
	result.Status, result.CullerCommit = "delivered", applied.Commit
	if applied.CleanupError != "" {
		result.OperatorAction = applied.CleanupError
	}
	return result, nil
}
