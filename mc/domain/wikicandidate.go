package domain

import (
	"context"
	"database/sql"
	"encoding/json"

	"mc/repository"
)

// RecordWikiCandidate records the candidate that a merge candidate step built
// for the wiki commit of an active wiki update. It returns the candidate id.
func RecordWikiCandidate(ctx context.Context, q Q, run, wiki string, c repository.Candidate, ref string) (int64, error) {
	var commit sql.NullString
	err := q.QueryRowContext(ctx, `SELECT wiki_commit FROM wiki_updates WHERE run_id=? AND ended_at IS NULL`, wiki).Scan(&commit)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	if err == sql.ErrNoRows || !commit.Valid || commit.String != c.SubmittedCommit || c.DestinationBranch != "main" {
		return 0, Errf(CodeStaleRun, "the merge result does not identify the wiki commit of the active wiki update")
	}
	if c.Status == "merged" {
		if err := c.Validate(); err != nil {
			return 0, err
		}
		if ref == "" {
			return 0, Errf(CodeSHARequired, "the merged candidate requires a retained Git reference")
		}
	} else if c.Status != "conflict" {
		return 0, Errf(CodeIllegalTransition, "the merge result is invalid")
	}
	body, err := json.Marshal(c)
	if err != nil {
		return 0, err
	}
	res, err := q.ExecContext(ctx, `INSERT INTO merge_candidates(wiki_update,run_id,destination_branch,destination_commit,candidate_commit,result,status,retained_ref)
		VALUES(?,?,'main',?,?,?,?,?)`, wiki, run, c.DestinationCommit, nullIfEmpty(c.Commit), string(body), c.Status, nullIfEmpty(ref))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
