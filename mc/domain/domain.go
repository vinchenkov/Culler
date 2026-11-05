// Package domain enforces task state before the substrate checks each write.
// Each function uses the transaction that its caller supplies through Q.
package domain

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Q is the query surface domain functions run against inside the caller's
// transaction. mc/verbs re-exports it.
type Q interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Stable error-code slugs. Every domain rejection carries one; the CLI
// reports it in the stdout error envelope beside the exit 1 and the stderr
// message.
const (
	CodeStaleRun           = "stale-run"
	CodeRoleMismatch       = "role-mismatch"
	CodeIllegalTransition  = "illegal-transition"
	CodeWIPCap             = "wip-cap"
	CodeCorrectionRequired = "correction-required"
	CodeSHARequired        = "sha-required"
	CodeCarrierForbidden   = "carrier-forbidden"
	CodeNotPackaged        = "not-packaged"
	CodeMergePending       = "merge-pending"
	CodeAlreadyDecided     = "already-decided"
	CodeZeroPromotion      = "zero-promotion"
	CodePoolMismatch       = "pool-mismatch"
	CodeLeaseHeld          = "lease-held"
	CodeNotFound           = "not-found"
	CodeNotBlocked         = "not-blocked"
	CodeBlocked            = "blocked"
	CodeReasonRequired     = "reason-required"
	CodeArchived           = "archived"
	CodeProjectInactive    = "project-inactive"
	// CodeWikiCheckFailed: the wiki commit of a Scribe terminal action failed
	// a check. The command ended the run and the wiki update as failed.
	CodeWikiCheckFailed = "wiki-check-failed"
	// CodeSessionInactive: a Homie transport verb named a session that is not
	// active. The Homie runner ends on it.
	CodeSessionInactive = "session-inactive"
	// CodeRuntimeChanged: the launch uses an earlier profile or authorization.
	CodeRuntimeChanged = "runtime-changed"
	// CodeProjectMismatch: a claim named a Project that is not the
	// subject task's, or no Project at all.
	CodeProjectMismatch = "project-mismatch"
	// CodeStaleOwner: a resident instance with an earlier owner number sent a
	// request. The resident exits on it.
	CodeStaleOwner = "stale-owner"
	// CodeSpineFault is NOT a rule refusal: the storage layer itself failed
	// (a SQLite driver error — locking, I/O, corruption). It is separated so
	// an agent or operator can tell "the spine refused you" from "the spine
	// broke", which reading them alike once cost a whole diagnosis cycle.
	CodeSpineFault = "spine-fault"
)

// DomainError is a domain rejection: exit 1 at the CLI, carrying a stable
// Code slug for the error envelope.
type DomainError struct {
	Code string
	Msg  string
}

func (e *DomainError) Error() string { return e.Msg }

// Errf builds a coded DomainError.
func Errf(code, format string, a ...any) error {
	return &DomainError{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// SpineTime is the datetime('now') storage format.
const SpineTime = "2006-01-02 15:04:05"

// Now reads the lock domain's own clock inside the caller's transaction —
// the one production source of the `now` parameters.
func Now(ctx context.Context, q Q) (time.Time, error) {
	var s string
	if err := q.QueryRowContext(ctx, `SELECT datetime('now')`).Scan(&s); err != nil {
		return time.Time{}, err
	}
	return ParseSpineTime(s)
}

// ParseSpineTime parses a stored spine timestamp.
func ParseSpineTime(s string) (time.Time, error) {
	t, err := time.Parse(SpineTime, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse spine timestamp %q: %w", s, err)
	}
	return t, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// taskRow is the domain's read of one tasks row.
type taskRow struct {
	ID              int64
	Status          string
	CorrectionCount int
	Blocked         bool
	Decision        sql.NullString
	Archived        bool
	Project         string
}

func getTask(ctx context.Context, q Q, id int64) (taskRow, error) {
	var r taskRow
	var blocked, archived int
	err := q.QueryRowContext(ctx, `
		SELECT id, status, correction_count, blocked,
		       decision, archived, project
		FROM tasks WHERE id = ?`, id).Scan(
		&r.ID, &r.Status, &r.CorrectionCount,
		&blocked, &r.Decision, &archived, &r.Project)
	if err == sql.ErrNoRows {
		return r, Errf(CodeNotFound, "no task %d", id)
	}
	if err != nil {
		return r, err
	}
	r.Blocked = blocked == 1
	r.Archived = archived == 1
	return r, nil
}

// requireLive rejects archived and decided rows: archived rows are terminal
// and decided rows never transition.
func requireLive(r taskRow) error {
	if r.Archived {
		return Errf(CodeArchived, "task %d is archived: terminal, no transitions", r.ID)
	}
	if r.Decision.Valid {
		return Errf(CodeAlreadyDecided,
			"task %d is already decided (%s); decisions never rewrite", r.ID, r.Decision.String)
	}
	return nil
}
