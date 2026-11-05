package domain

import (
	"context"
	"database/sql"
	"strings"
	"unicode/utf8"
)

const maxDispatchScalarBytes = 4 * 1024

func validDispatchScalarAdmission(value string) bool {
	if value == "" || len(value) > maxDispatchScalarBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Task state machine — the Arbiter arms, the forward stage advances, the
// verdict table, re-entry, blocking, and the decision writes.
// ---------------------------------------------------------------------------

// Promote is the Arbiter's promote arm: proposed → seeded.
func Promote(ctx context.Context, q Q, taskID int64) error {
	if pending, err := RequirementsRevisionPending(ctx, q, taskID); err != nil {
		return err
	} else if pending {
		_, err := q.ExecContext(ctx, `INSERT INTO activity(actor,kind,subject,detail) VALUES('arbiter','proposal.superseded',?,'The Arbiter accepted the previous requirements.')`, taskID)
		return err
	}
	r, err := getTask(ctx, q, taskID)
	if err != nil {
		return err
	}
	if err := requireLive(r); err != nil {
		return err
	}
	if r.Blocked {
		return Errf(CodeBlocked,
			"blocked proposal %d is outside the Arbiter snapshot until unblocked", taskID)
	}
	if r.Status != "proposed" {
		return Errf(CodeIllegalTransition,
			"only proposed rows promote; task %d is %q", taskID, r.Status)
	}
	_, err = q.ExecContext(ctx, `UPDATE tasks SET status = 'seeded' WHERE id = ?`, taskID)
	return err
}

// RejectProposal is the Arbiter's reject arm: decision='rejected' + archive,
// reason mandatory. The reason prose is recorded as an activity row; the
// tasks row keeps only the mark.
func RejectProposal(ctx context.Context, q Q, taskID int64, reason string) error {
	if pending, err := RequirementsRevisionPending(ctx, q, taskID); err != nil {
		return err
	} else if pending {
		_, err := q.ExecContext(ctx, `INSERT INTO activity(actor,kind,subject,detail) VALUES('arbiter','proposal.superseded',?,?)`, taskID, reason)
		return err
	}
	if reason == "" {
		return Errf(CodeReasonRequired, "reject requires a reason")
	}
	r, err := getTask(ctx, q, taskID)
	if err != nil {
		return err
	}
	if err := requireLive(r); err != nil {
		return err
	}
	if r.Blocked {
		return Errf(CodeBlocked,
			"blocked proposal %d is outside the Arbiter snapshot until unblocked", taskID)
	}
	if r.Status != "proposed" {
		return Errf(CodeIllegalTransition,
			"only proposed rows are rejected; task %d is %q", taskID, r.Status)
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE tasks SET decision = 'rejected', decided_at = datetime('now'), archived = 1
		WHERE id = ?`, taskID); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `
		INSERT INTO activity (actor, kind, subject, detail)
		VALUES ('arbiter', 'task.rejected', ?, ?)`, taskID, reason)
	return err
}

// AdvanceStage moves a task to worked or packaged.
func AdvanceStage(ctx context.Context, q Q, taskID int64, to string) error {
	r, err := getTask(ctx, q, taskID)
	if err != nil {
		return err
	}
	if err := requireLive(r); err != nil {
		return err
	}
	switch to {
	case "worked":
		if r.Status != "seeded" {
			return Errf(CodeIllegalTransition,
				"seeded → worked only; task %d is %q", taskID, r.Status)
		}
	case "packaged":
		if r.Status != "verified" {
			return Errf(CodeIllegalTransition,
				"verified → packaged only; task %d is %q", taskID, r.Status)
		}
	default:
		return Errf(CodeIllegalTransition, "AdvanceStage moves to worked|packaged, not %q", to)
	}
	if _, err := q.ExecContext(ctx,
		`UPDATE tasks SET status = ? WHERE id = ?`, to, taskID); err != nil {
		return err
	}
	if to == "packaged" {
		// The carried notes were this round's brief payload; packaging ends
		// the round and clears them.
		if _, err := q.ExecContext(ctx,
			`UPDATE tasks SET revision_notes = NULL WHERE id = ?`, taskID); err != nil {
			return err
		}
	}
	return nil
}

// VerdictArgs carries one Verifier verdict.
type VerdictArgs struct {
	TaskID         int64
	RunID          string // the verdict record lands on this runs row
	Outcome        string // pass | correct
	EvidencePath   string
	VerifiedSHA    string // required for pass
	CorrectionPath string // required for correct
	Problem        string
	Evidence       string
	Resolution     string
}

// VerdictResult reports the applied outcome.
type VerdictResult struct {
	Status          string // resulting task status
	CorrectionCount int
}

// The condition types of Decision Pending.
const (
	BlockReport        = "report"
	BlockCustomization = "customization"
	BlockAllowance     = "allowance"
	BlockConfinement   = "confinement"
	BlockCheckout      = "checkout"
	BlockOperator      = "operator"
	BlockProposal      = "proposal"
)

// Block sets the dispatchability flag with its mandatory reason and its
// condition type. A later block replaces the reason and the type.
// Blocking never destroys pipeline position — status is untouched.
func Block(ctx context.Context, q Q, taskID int64, kind, reason string) error {
	if reason == "" {
		return Errf(CodeReasonRequired, "blocking requires a reason")
	}
	switch kind {
	case BlockReport, BlockCustomization, BlockConfinement, BlockCheckout, BlockOperator:
	default:
		return Errf(CodeInvalidRequest, "the block condition type %q is invalid", kind)
	}
	r, err := getTask(ctx, q, taskID)
	if err != nil {
		return err
	}
	if r.Archived {
		return Errf(CodeArchived, "task %d is archived", taskID)
	}
	_, err = q.ExecContext(ctx,
		`UPDATE tasks SET blocked = 1, blocked_kind = ?, blocked_reason = ? WHERE id = ?`, kind, reason, taskID)
	return err
}

// Unblock clears the flag; the substrate trigger clears the stale reason
// with it. Unblocking resumes exactly where it stopped.
func Unblock(ctx context.Context, q Q, taskID int64) error {
	var allowance bool
	if err := q.QueryRowContext(ctx, `SELECT allowance_condition IS NOT NULL FROM tasks WHERE id=?`, taskID).Scan(&allowance); err != nil {
		return err
	}
	if allowance {
		return Errf(CodeIllegalTransition, "Authorize the required correction or merge repair allowance first.")
	}
	r, err := getTask(ctx, q, taskID)
	if err != nil {
		return err
	}
	if !r.Blocked {
		return Errf(CodeNotBlocked, "task %d is not blocked", taskID)
	}
	_, err = q.ExecContext(ctx, `UPDATE tasks SET blocked = 0 WHERE id = ?`, taskID)
	return err
}

// Cancel records the operator decision and archives the task.
func Cancel(ctx context.Context, q Q, taskID int64, reason string, actor string) error {
	if actor == "operator" || strings.HasPrefix(actor, "homie:") {
		if err := RequireOperatorTaskControl(ctx, q, taskID); err != nil {
			return err
		}
	}
	if strings.TrimSpace(reason) == "" {
		return Errf(CodeReasonRequired, "--reason is required for cancel (asymmetric by design)")
	}
	r, err := getTask(ctx, q, taskID)
	if err != nil {
		return err
	}
	if r.Archived {
		return Errf(CodeArchived, "task %d is archived", taskID)
	}
	if r.Decision.Valid && r.Decision.String != "approved" {
		return Errf(CodeAlreadyDecided, "task %d is already decided", taskID)
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE tasks SET decision = 'cancelled', decided_at = datetime('now'), archived = 1
		WHERE id = ?`, taskID); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `
		INSERT INTO activity (actor, kind, subject, detail)
		VALUES (?, 'task.cancelled', ?, ?)`, actor, taskID, reason)
	return err
}

// ProposalArgs births one proposed row: tasks are born proposed.
type ProposalArgs struct {
	Title       string
	Description string
	Scope       string // "" defaults to task
	Priority    *int   // nil defaults to the schema's P2
	Origin      string // user | autonomous
	Project     string
}

// requireActiveProject refuses filing new work into a paused or archived
// Project. Pause stops intake and archive is terminal — a row filed there
// would be invisible to every selection site, silently swallowing operator
// intent.
func requireActiveProject(ctx context.Context, q Q, id string) error {
	var status string
	err := q.QueryRowContext(ctx,
		`SELECT status FROM projects WHERE id = ?`, id).Scan(&status)
	if err == sql.ErrNoRows {
		return Errf(CodeNotFound, "unknown project %q", id)
	}
	if err != nil {
		return err
	}
	if status != "active" {
		return Errf(CodeProjectInactive,
			"project %q is %s and accepts no new work", id, status)
	}
	return nil
}

// BirthProposal files one row into the proposed pool.
func BirthProposal(ctx context.Context, q Q, a ProposalArgs) (int64, error) {
	if a.Title == "" || a.Project == "" {
		return 0, Errf(CodeReasonRequired, "a proposal requires title and project")
	}
	if !validDispatchScalarAdmission(a.Title) {
		return 0, Errf(CodeCarrierForbidden, "proposal title must be valid UTF-8 without controls and at most 4096 bytes")
	}
	if err := requireActiveProject(ctx, q, a.Project); err != nil {
		return 0, err
	}
	scope := a.Scope
	if scope == "" {
		scope = "task"
	}
	if scope != "task" {
		return 0, Errf(CodeIllegalTransition, "scope must be task, not %q", scope)
	}
	pri := 2
	if a.Priority != nil {
		pri = *a.Priority
	}
	res, err := q.ExecContext(ctx, `
		INSERT INTO tasks (title, description, scope, priority, origin, project)
		VALUES (?, ?, ?, ?, ?, ?)`,
		a.Title, nullIfEmpty(a.Description), scope, pri, a.Origin, a.Project)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}
