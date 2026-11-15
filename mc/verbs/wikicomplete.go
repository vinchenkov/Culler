package verbs

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"

	"mc/domain"
	"mc/repository"
	"mc/wiki"
)

const wikiCompleteUsage = "usage: mc complete --run <run_id> --commit <full-commit> --wiki-evidence <path> | mc complete --run <run_id> --infra --reason <cause>"

// completeWikiUpdate records the terminal action of the Scribe: the wiki
// commit with its completion receipt, or a failure report. The Git reads and
// the checks of the wiki commit run outside a write transaction. A refusal
// changes no state, and a failed check ends the run and the wiki update.
func completeWikiUpdate(db *sql.DB, id *RunIdentity, a CompleteArgs) (any, error) {
	if err := requirePipeline(id); err != nil {
		return nil, err
	}
	if err := requireOwnRun(id, a.Run); err != nil {
		return nil, err
	}
	if a.Task != 0 || a.Status != "" || a.Checkpoint || a.Outputs != "" || a.Thesis != "" || a.NeedsOperator {
		return nil, Usagef(wikiCompleteUsage)
	}
	if a.Infra {
		if a.Commit != "" || a.WikiEvidence != "" || a.Reason == "" {
			return nil, Usagef(wikiCompleteUsage)
		}
		return RunInfrastructureFailure(db, id, a.Run, domain.InfrastructureFailureReport{Condition: a.Reason, Source: "agent"})
	}
	if a.Commit == "" || a.WikiEvidence == "" {
		return nil, Usagef(wikiCompleteUsage)
	}

	ctx := context.Background()
	start, err := wikiStartCommit(ctx, db, a.Run)
	if err != nil {
		return nil, classify(err)
	}
	g := repository.Git{Root: filepath.Join(completionStoreRoot(), "source")}
	head, err := g.Commit(ctx, "HEAD")
	if err != nil {
		return nil, classify(err)
	}
	if a.Commit != head {
		return nil, Domainf("The --commit value differs from HEAD.")
	}
	if err := g.Clean(ctx); err != nil {
		return nil, Domainf("The repository holds an uncommitted file.")
	}

	// A wiki commit that differs from the start commit waits for its merge. The wiki update stays active.
	end := domain.WikiRunEnd{Outcome: "completed", WikiCommit: a.Commit}
	var receipt *wiki.ReceiptError
	var failed *wiki.CheckError
	_, err = wiki.ValidateCompletion(ctx, g, start, a.Commit, a.WikiEvidence)
	switch {
	case errors.As(err, &receipt):
		return nil, Domainf("%s", receipt.Text)
	case errors.As(err, &failed):
		end.Result, end.Cause = "failed", failed.Text
	case err != nil:
		return nil, classify(err)
	case a.Commit == start:
		end.Result = "succeeded"
	}
	err = inTx(db, func(ctx context.Context, q Q) error {
		if _, err := wikiStartCommit(ctx, q, a.Run); err != nil {
			return err
		}
		_, err := domain.EndWikiRun(ctx, q, a.Run, end)
		return err
	})
	if err != nil {
		return nil, err
	}
	if failed != nil {
		return nil, domain.Errf(domain.CodeWikiCheckFailed, "%s", failed.Text)
	}
	return map[string]any{"run_id": a.Run, "wiki_commit": a.Commit, "wiki_change": a.Commit != start}, nil
}

// wikiStartCommit returns the start commit of the active wiki update of the run.
func wikiStartCommit(ctx context.Context, q Q, run string) (string, error) {
	fence, err := domain.FenceAgentRun(ctx, q, run)
	if err != nil {
		return "", err
	}
	if !fence.WikiUpdate {
		return "", domain.Errf(domain.CodeStaleRun, "run %q has no active wiki update", run)
	}
	return fence.StartCommit, nil
}
