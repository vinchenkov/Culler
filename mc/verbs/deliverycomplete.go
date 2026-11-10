package verbs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"mc/domain"
	"mc/repository"
	"mc/wiki"
	"os"
	"path/filepath"
)

func completeDelivery(db *sql.DB, id *RunIdentity, a CompleteArgs) (any, error) {
	if err := requirePipeline(id); err != nil {
		return nil, err
	}
	if err := requireOwnRun(id, a.Run); err != nil {
		return nil, err
	}
	if a.Infra || a.NeedsOperator || a.Reason != "" {
		return nil, Usagef("the submission requires one status and an exact commit")
	}
	if err := requireDurableOutputs(id, a.Task, a.Status, a.Outputs); err != nil {
		return nil, err
	}
	originalOutput := a.Outputs
	if a.Outputs != "" && a.Status != "seeded" {
		snapshot, err := snapshotRunArtifact(id, a.Outputs, maxExecutionReportBytes)
		if err != nil {
			return nil, err
		}
		a.Outputs = snapshot
	}
	if a.Status == "packaged" {
		if err := requireThesisProse(a.Thesis); err != nil {
			return nil, err
		}
		if a.Outputs == "" {
			return nil, Usagef("the Packager requires its durable report")
		}
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		if err := recordTaskArtifacts(ctx, q, a.Task, originalOutput, a.Outputs); err != nil {
			return err
		}
		subject, err := fenceRun(ctx, q, a.Run)
		if err != nil {
			return err
		}
		if subject == nil || *subject != a.Task {
			return Domainf("the submission requires its assigned task")
		}
		var input, base, destination string
		var version sql.NullInt64
		if err := q.QueryRowContext(ctx, `SELECT r.input_commit,COALESCE(t.base_commit,r.input_commit),r.version_id,r.destination_commit FROM repository_runs r
			JOIN tasks t ON t.id=r.task_id WHERE r.run_id=? AND r.task_id=?`, a.Run, a.Task).Scan(&input, &base, &version, &destination); err != nil {
			return err
		}
		g := repository.Git{Root: filepath.Join(completionStoreRoot(), "source")}
		head, err := g.Commit(ctx, "HEAD")
		if err != nil {
			return err
		}
		if err := g.Clean(ctx); err != nil {
			return err
		}
		switch a.Status {
		case "worked":
			if a.Checkpoint && baseRole(id.Role) != "worker" {
				return roleMismatch(id, "worker")
			}
			if baseRole(id.Role) != "worker" {
				return roleMismatch(id, "worker")
			}
			commit := a.Commit
			if commit == "" || commit != head || !g.Ancestor(ctx, input, commit) {
				return Domainf("the submission must identify HEAD and preserve its assigned input")
			}
			// A task delivers one commit. A Worker run can submit its start
			// commit unchanged when that commit differs from the base commit.
			if commit == base {
				return Domainf("the submission must be a commit that differs from the base commit %s", base)
			}
			if err := wiki.ValidateTaskCommit(ctx, g, input, destination, commit); err != nil {
				return Domainf("%s", err)
			}
			kind := "submission"
			if a.Checkpoint {
				kind = "checkpoint"
			}
			if _, err := q.ExecContext(ctx, `INSERT INTO version_submissions(run_id,task_id,input_commit,result_commit,evidence_path,kind,prior_version)
			SELECT ?,id,?,?,?,?,submitted_version FROM tasks WHERE id=?`, a.Run, input, commit, nullIfEmpty(a.Outputs), kind, a.Task); err != nil {
				return err
			}
		case "packaged":
			if baseRole(id.Role) != "packager" {
				return roleMismatch(id, "packager")
			}
			if head != input {
				return Domainf("the Packager must preserve its assigned submitted version")
			}
			var passed bool
			if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM version_verdicts v JOIN tasks d ON d.source_version=v.version_id WHERE d.id=? AND d.submitted_version=? AND d.source_version=d.submitted_version AND v.outcome='pass')`, a.Task, version).Scan(&passed); err != nil {
				return err
			}
			if !passed {
				return Domainf("the Packager requires the verified submitted version")
			}
			if err := domain.AdvanceStage(ctx, q, a.Task, "packaged"); err != nil {
				return err
			}
			body, err := os.ReadFile(a.Outputs)
			if err != nil {
				return err
			}
			digest := fmt.Sprintf("%x", sha256.Sum256(body))
			opening, err := packetOpeningText(ctx, q, a.Task, a.Thesis)
			if err != nil {
				return err
			}
			if _, err := q.ExecContext(ctx, `UPDATE tasks SET report_path=?,report_sha256=?,thesis=? WHERE id=?`, a.Outputs, digest, opening, a.Task); err != nil {
				return err
			}
		default:
			return Usagef("the repository terminal requires worked or packaged")
		}
		if a.Outputs != "" {
			if _, err := q.ExecContext(ctx, `UPDATE runs SET output_path=? WHERE id=?`, a.Outputs, a.Run); err != nil {
				return err
			}
		}
		if err := endRun(ctx, q, a.Run, "completed"); err != nil {
			return err
		}
		return releaseLease(ctx, q, a.Run)
	})
	return map[string]any{"task_id": a.Task, "submitted": err == nil && !a.Checkpoint, "checkpoint_pending": err == nil && a.Checkpoint}, err
}
