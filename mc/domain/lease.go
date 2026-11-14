package domain

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// The lease aggregate: one lease row per Project. The three reap conditions
// live in mc/dispatch.reapable; this file owns the write side. All lease
// arithmetic takes `now` by parameter, injectable for tests exactly like
// dispatch.Clock.

// ClaimArgs is one claim-and-spawn write: the runs row is committed before
// the process starts, in the same transaction as the CAS.
type ClaimArgs struct {
	RunID string
	// Project names the lease row the claim is a CAS on: every pipeline
	// run, propose included, belongs to exactly one Project. Required.
	Project     string
	Owner       string // leases.owner vocabulary (base role)
	SubjectID   *int64 // nil for subjectless runs (propose)
	SessionPath string
	Binding     string
	// PoolSnapshot: Arbiter runs snapshot the entire proposed pool at claim;
	// the coverage check reads it back. nil = not an Arbiter.
	PoolSnapshot []int64
	// HardDeadlineMinutes is the awake time that the run can hold the lease
	// (the settings row's tunable of this moment).
	HardDeadlineMinutes int
}

// ClaimResult reports the claim's derived fields.
type ClaimResult struct {
	Project string // the Project whose lease the run holds (always set)
}

// Claim is the CAS claim on one Project's lease row: UPDATE … WHERE
// project = ? AND run_id IS NULL first — against a held lease it aborts
// before any write lands (the in-transaction backstop branch) —
// then the runs-row insert. A claim in one Project neither waits for nor
// blocks a claim in another. A subject task must belong to the claimed
// Project. The claim stamps the VM clock and the hard deadline in VM
// uptime; last_heartbeat_at starts NULL (the first-heartbeat watchdog's case).
func Claim(ctx context.Context, q Q, now time.Time, a ClaimArgs) (ClaimResult, error) {
	var res ClaimResult
	if a.Project == "" {
		return res, Errf(CodeProjectMismatch, "a claim names the Project whose lease it takes")
	}

	var subject any
	if a.SubjectID != nil {
		subject = *a.SubjectID
		var ws string
		if err := q.QueryRowContext(ctx,
			`SELECT project FROM tasks WHERE id = ?`, *a.SubjectID).Scan(&ws); err != nil {
			if err == sql.ErrNoRows {
				return res, Errf(CodeNotFound, "no task %d", *a.SubjectID)
			}
			return res, err
		}
		if ws != a.Project {
			return res, Errf(CodeProjectMismatch,
				"task %d belongs to Project %q, not %q",
				*a.SubjectID, ws, a.Project)
		}
	}
	res.Project = a.Project

	vm, err := ReadVMClock()
	if err != nil {
		return res, err
	}
	acquired := now.UTC().Format(SpineTime)
	deadline := vm.Uptime + time.Duration(a.HardDeadlineMinutes)*time.Minute
	cas, err := q.ExecContext(ctx, `
		UPDATE leases SET run_id = ?, subject = ?, owner = ?,
			acquired_at = ?, last_heartbeat_at = NULL, boot_id = ?,
			acquired_uptime_ms = ?, heartbeat_uptime_ms = NULL, deadline_uptime_ms = ?
		WHERE project = ? AND run_id IS NULL`,
		a.RunID, subject, a.Owner, acquired, vm.BootID, vm.Uptime.Milliseconds(), deadline.Milliseconds(), a.Project)
	if err != nil {
		return res, err
	}
	n, err := cas.RowsAffected()
	if err != nil {
		return res, err
	}
	if n != 1 {
		return res, Errf(CodeLeaseHeld, "lost the claim race: the lease of Project %q is held", a.Project)
	}

	var pool any
	if a.PoolSnapshot != nil {
		b, err := json.Marshal(a.PoolSnapshot)
		if err != nil {
			return res, err
		}
		pool = string(b)
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO runs (id, tier, role, project, subject, session_path,
		                  binding, pool_snapshot)
		VALUES (?, 'pipeline', ?, ?, ?, ?, ?, ?)`,
		a.RunID, a.Owner, a.Project, subject, a.SessionPath, a.Binding, pool); err != nil {
		return res, err
	}
	return res, nil
}

// Fence verifies the run's fencing token against the live lease of its
// Project: a call whose run_id holds no lease is
// rejected, never double-applied. Returns the lease's subject (nil for
// subjectless runs).
func Fence(ctx context.Context, q Q, runID string) (*int64, error) {
	var subject sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT l.subject FROM leases l JOIN runs r ON r.id = l.run_id
        WHERE l.run_id = ? AND r.ended_at IS NULL AND r.outcome IS NULL`, runID).
		Scan(&subject)
	if err == sql.ErrNoRows {
		return nil, Errf(CodeStaleRun,
			"stale run: %q does not hold the live lease", runID)
	}
	if err != nil {
		return nil, err
	}
	if subject.Valid {
		s := subject.Int64
		return &s, nil
	}
	return nil, nil
}

// Heartbeat stamps the heartbeat time and VM uptime iff runID holds a live
// lease — fenced: a zombie run can neither renew its old lease nor touch the
// new holder's. It never moves the deadline. Returns the stamp.
func Heartbeat(ctx context.Context, q Q, runID string) (string, error) {
	vm, err := ReadVMClock()
	if err != nil {
		return "", err
	}
	res, err := q.ExecContext(ctx, `
		UPDATE leases SET last_heartbeat_at = datetime('now'), heartbeat_uptime_ms = ?
		WHERE run_id = ? AND EXISTS(SELECT 1 FROM runs WHERE id=leases.run_id AND ended_at IS NULL AND outcome IS NULL)`, vm.Uptime.Milliseconds(), runID)
	if err != nil {
		return "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", Errf(CodeStaleRun,
			"stale run: %q does not hold the live lease", runID)
	}
	var stamped string
	err = q.QueryRowContext(ctx,
		`SELECT last_heartbeat_at FROM leases WHERE run_id = ?`, runID).Scan(&stamped)
	return stamped, err
}

// Release frees the lease iff runID holds it (fenced): NULL every claim
// column — a free lease carries no run residue (substrate CHECKs).
func Release(ctx context.Context, q Q, runID string) error {
	res, err := q.ExecContext(ctx, `
		UPDATE leases SET run_id = NULL, subject = NULL,
			owner = NULL, acquired_at = NULL, last_heartbeat_at = NULL, boot_id = NULL,
			acquired_uptime_ms = NULL, heartbeat_uptime_ms = NULL, deadline_uptime_ms = NULL
		WHERE run_id = ?`, runID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return Errf(CodeStaleRun,
			"stale run: %q does not hold the live lease", runID)
	}
	return nil
}

// ReapArgs is the write side of one step-(0) reap decision (the conditions
// were evaluated by the frozen mc/dispatch.reapable).
type ReapArgs struct {
	RunID  string
	Reason string
	// NoCharge suppresses the subject's infra-death charge. It carries the
	// decision's own ChargeRetries rather than being re-derived here, because
	// only the decision knows WHY the run is being reaped: a crash charges, an
	// operator interrupt does not. Zero value = charge, so every existing
	// caller and every crash reason is unchanged.
	NoCharge bool
	// Outcome overrides the `runs.outcome` mark. Empty means "reaped", the
	// ordinary crash record; an interrupt records itself so a later reader can
	// tell an operator "scratch that" from a run that died on its own.
	Outcome string
}

// ReapResult reports the applied charge.
type ReapResult struct {
	Charged bool
	Stalled bool
	Blocked bool // Infrastructure failures never create an operator block.
}

// ApplyReap records an execution failure. The lease remains held until the resident confirms the container stop.
func ApplyReap(ctx context.Context, q Q, a ReapArgs) (ReapResult, error) {
	outcome := a.Outcome
	if outcome == "" {
		outcome = "reaped"
	}
	report := InfrastructureFailureReport{Condition: a.Reason, Source: "resident", Cause: "unknown"}
	if !a.NoCharge {
		if err := ValidateInfrastructureFailureReport(report); err != nil {
			return ReapResult{}, err
		}
	}
	result, err := finishInfrastructureFailure(ctx, q, a.RunID, report, outcome, a.Reason, !a.NoCharge)
	return ReapResult{Charged: result.Charged, Stalled: result.Allowance == "task" && result.Stalled}, err
}
