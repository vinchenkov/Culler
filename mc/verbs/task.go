package verbs

import (
	"context"
	"database/sql"
	"strings"

	"mc/domain"
)

// TaskAdd creates explicit operator work with its repository assignments.
func TaskAdd(db *sql.DB, id *RunIdentity, title, project, description string, priority *int) (any, error) {
	if err := RequireOperatorVerb(id, "task.add"); err != nil {
		return nil, err
	}
	if title == "" {
		return nil, Usagef("mc task add requires a title")
	}
	if project == "" {
		return nil, Usagef("mc task add requires --project")
	}
	var taskID int64
	err := inTx(db, func(ctx context.Context, q Q) error {
		if err := requireOperatorVerbTx(ctx, q, id, "task.add"); err != nil {
			return err
		}
		var err error
		taskID, err = domain.BirthOperatorWork(ctx, q, domain.ProposalArgs{
			Title:       title,
			Description: description,
			Priority:    priority,
			Origin:      "user",
			Project:     project,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"task_id": taskID}, nil
}

// TaskGet returns one task row as JSON — the e2e's assertion channel into
// the spine.
func TaskGet(db *sql.DB, id int64) (any, error) {
	rows, err := db.Query(`
		SELECT id, title, description, scope, priority, record_version, requirements_version,
		       created_at, status, stage_rank, stage_entered_at,
		       correction_count, blocked, blocked_reason, blocked_kind, dispatch_retries,run_retry_at,
               stalled, infrastructure_failure_count, `+latestInfrastructureFailureSQL+`, `+connectionWaitSQL+`, `+projectInfrastructureSQL+`,
		       decision, decided_at, archived, origin, project,
		       completion_sha, (SELECT result_commit FROM task_versions WHERE id=tasks.source_version) AS verified_sha, base_commit, revision_notes, `+deliveryTaskSQL+`
		FROM tasks WHERE id = ?`, id)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()
	out, err := taskRowsToMaps(rows)
	if err != nil {
		return nil, classify(err)
	}
	if len(out) == 0 {
		return nil, Domainf("no task %d", id)
	}
	rows.Close()
	if err := recordBoardDetails(context.Background(), db, id, out[0]); err != nil {
		return nil, classify(err)
	}
	return out[0], nil
}

// TaskBlock records an operator decision. A pipeline report also ends its run.
func TaskBlock(db *sql.DB, id *RunIdentity, task int64, reason string) (any, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, domain.Errf(domain.CodeReasonRequired, "Block requires a reason.")
	}
	pipeline := id != nil && id.Tier == "pipeline"
	superseded := false
	if !pipeline {
		if err := RequireOperatorVerb(id, "task.block"); err != nil {
			return nil, err
		}
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		if pipeline {
			// Pipeline caller: own subject only, fenced to the live lease.
			subject, err := fenceRun(ctx, q, id.RunID)
			if err != nil {
				return err
			}
			if subject == nil || *subject != task {
				return Domainf("a pipeline run blocks only its own subject; task %d is not it", task)
			}
		} else {
			if err := requireOperatorVerbTx(ctx, q, id, "task.block"); err != nil {
				return err
			}
			if err := domain.RequireOperatorTaskControl(ctx, q, task); err != nil {
				return err
			}
		}
		if pipeline {
			pending, err := domain.RunRequirementsSuperseded(ctx, q, id.RunID, task)
			if err != nil {
				return err
			}
			if pending {
				superseded = true
				if _, err := q.ExecContext(ctx, `INSERT INTO activity(actor,kind,subject,detail) VALUES(?,'task.superseded_block',?,?)`, id.Role, task, reason); err != nil {
					return err
				}
				return endRun(ctx, q, id.RunID, "blocked")
			}
		}
		actor, kind := "operator", domain.BlockOperator
		if pipeline {
			actor, kind = id.Role, domain.BlockReport
		}
		if err := blockTaskTx(ctx, q, task, kind, reason, actor); err != nil {
			return err
		}
		if pipeline {
			if err := endRun(ctx, q, id.RunID, "blocked"); err != nil {
				return err
			}
			return nil // Dispatch stops the container before it releases the lease.
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"task_id": task, "blocked": !superseded, "superseded": superseded}, nil
}

// TaskUnblock resolves the operator decision and preserves the task run allowance.
func TaskUnblock(db *sql.DB, id *RunIdentity, task int64, reasons ...string) (any, error) {
	if err := RequireOperatorVerb(id, "task.unblock"); err != nil {
		return nil, err
	}
	if len(reasons) != 1 || strings.TrimSpace(reasons[0]) == "" {
		return nil, domain.Errf(domain.CodeReasonRequired, "Unblock requires a reason.")
	}
	var revision int64
	err := inTx(db, func(ctx context.Context, q Q) error {
		if err := requireOperatorVerbTx(ctx, q, id, "task.unblock"); err != nil {
			return err
		}
		if err := domain.RequireOperatorTaskControl(ctx, q, task); err != nil {
			return err
		}
		var err error
		revision, err = unblockTaskTx(ctx, q, task, reasons[0], "operator")
		return err
	})
	if err != nil {
		return nil, err
	}
	result := map[string]any{"task_id": task, "blocked": false}
	if revision != 0 {
		result["revision_id"] = revision
		result["revision_state"] = "pending"
	}
	return result, nil
}

func TaskResume(db *sql.DB, id *RunIdentity, task int64, reason string) (any, error) {
	if err := RequireOperatorVerb(id, "task.resume"); err != nil {
		return nil, err
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		if err := requireOperatorVerbTx(ctx, q, id, "task.resume"); err != nil {
			return err
		}
		return resumeTaskTx(ctx, q, task, reason, "operator")
	})
	return map[string]any{"task_id": task, "resumed": err == nil}, err
}

// TaskInterrupt is the operator's in-flight "scratch that": cancel the
// exact live subject, and stop there. It writes ONE durable fact and emits no
// effect of its own.
//
// It used to end the Run and release the lease as well, returning
// {"action":"interrupt", "stop_container":true} — and that stop never happened.
// The resident applies effects at exactly one call site, the parsed stdout of
// `mc dispatch`; this verb's result goes to its own caller's stdout, which for
// a Homie is inside a container. Releasing the lease then removed the only
// remaining path to a stop, because the reap selects on the lease. So the
// operator was told the run had stopped while the agent kept running and
// writing.
//
// The lease is therefore left HELD. The cancellation is the durable marker, the
// next tick reaps on it (dispatch.ReapInterrupted) and the resident stops the
// container through the reap machinery that already exists — the architecture's
// own decide-then-effect shape. The cost is at most one tick of held
// lease, which is also how long the operator waits for any other transition.
func TaskInterrupt(db *sql.DB, id *RunIdentity, task int64) (any, error) {
	if err := RequireOperatorVerb(id, "task.interrupt"); err != nil {
		return nil, err
	}
	var runID string
	err := inTx(db, func(ctx context.Context, q Q) error {
		if err := requireOperatorVerbTx(ctx, q, id, "task.interrupt"); err != nil {
			return err
		}
		// The live lease whose subject is this task: the lease of the task's
		// own Project.
		err := q.QueryRowContext(ctx,
			`SELECT run_id FROM leases WHERE run_id IS NOT NULL AND subject = ?`, task,
		).Scan(&runID)
		if err == sql.ErrNoRows {
			return Domainf("task %d is not the live lease subject; nothing to interrupt", task)
		}
		if err != nil {
			return err
		}
		return domain.Cancel(ctx, q, task, "operator_interrupt", "operator")
	})
	if err != nil {
		return nil, err
	}
	// Deliberately NOT effect-shaped: no "action", no "stop_container". This
	// result reaches only its caller, and a result that looks like effect data
	// is what made the missing stop invisible for as long as it was.
	return map[string]any{
		"task_id": task, "run_id": runID,
		"cancelled": true, "stop_pending": true,
	}, nil
}

// TaskCancel is `mc task cancel <task> --reason <prose>`, the cancellation of a
// task at any stage. When the task is the live lease subject, the next tick
// reaps the run on the cancellation and the resident stops the container.
func TaskCancel(db *sql.DB, id *RunIdentity, task int64, reason string) (any, error) {
	if err := RequireOperatorVerb(id, "task.cancel"); err != nil {
		return nil, err
	}
	var runID string
	err := inTx(db, func(ctx context.Context, q Q) error {
		if err := requireOperatorVerbTx(ctx, q, id, "task.cancel"); err != nil {
			return err
		}
		var lockRun sql.NullString
		err := q.QueryRowContext(ctx,
			`SELECT run_id FROM leases WHERE run_id IS NOT NULL AND subject = ?`, task,
		).Scan(&lockRun)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if lockRun.Valid {
			runID = lockRun.String
		}
		return domain.Cancel(ctx, q, task, reason, "operator")
	})
	if err != nil {
		return nil, err
	}
	// Not effect-shaped, for TaskInterrupt's reason: a result that looks like
	// effect data is what made that verb's missing stop invisible. stop_pending
	// says only that a reap is owed, and it is owed to the next tick.
	out := map[string]any{"task_id": task, "cancelled": true}
	if runID != "" {
		out["run_id"] = runID
		out["stop_pending"] = true
	}
	return out, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// rowsToMaps renders generic rows as JSON-ready maps (reads only).
func rowsToMaps(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				m[c] = string(b)
			} else {
				m[c] = vals[i]
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
