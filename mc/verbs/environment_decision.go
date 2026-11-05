package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
)

type environmentDecision struct {
	RevisionID      string `json:"revision_id"`
	ProjectID       string `json:"project_id"`
	OperationID     string `json:"operation_id"`
	Decision        string `json:"decision"`
	CurrentRevision string `json:"current_revision_id"`
	CurrentImage    string `json:"current_image_digest"`
}

func environmentDecisionTx(ctx context.Context, q Q, project string) (*environmentDecision, error) {
	var d environmentDecision
	err := q.QueryRowContext(ctx, `SELECT revision_id,project_id,operation_id,decision,current_revision_id,current_image_digest FROM environment_decisions WHERE project_id=?`, project).Scan(&d.RevisionID, &d.ProjectID, &d.OperationID, &d.Decision, &d.CurrentRevision, &d.CurrentImage)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &d, err
}

func recordEnvironmentDecisionTx(ctx context.Context, q Q, s EnvironmentState, revision, decision string) error {
	prior, err := environmentDecisionTx(ctx, q, s.ProjectID)
	if err != nil {
		return err
	}
	if prior != nil {
		if prior.RevisionID == revision && prior.Decision == decision {
			return nil
		}
		return Domainf("Initial setup already has an image decision. Further image changes use the Environment page after import.")
	}
	var operation, current, image string
	err = q.QueryRowContext(ctx, `SELECT o.id,o.revision_id,o.image_digest FROM environment_operations o JOIN environment_results r ON r.operation_id=o.id WHERE r.revision_id=? AND r.project_id=? AND r.outcome='proposal' AND o.status='succeeded'`, revision, s.ProjectID).Scan(&operation, &current, &image)
	if err == sql.ErrNoRows {
		// An operator can edit the proposal before its image decision.
		err = q.QueryRowContext(ctx, `SELECT o.id,o.revision_id,o.image_digest FROM environment_operations o JOIN environment_results r ON r.operation_id=o.id WHERE r.project_id=? AND r.outcome='proposal' AND o.status='succeeded' ORDER BY r.rowid DESC LIMIT 1`, s.ProjectID).Scan(&operation, &current, &image)
		if err == sql.ErrNoRows && decision == "approved" {
			return nil
		}
	}
	if err == sql.ErrNoRows {
		return Domainf("The image decision requires the setup agent's current proposal.")
	}
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO environment_decisions(revision_id,project_id,operation_id,decision,current_revision_id,current_image_digest) VALUES(?,?,?,?,?,?)`, revision, s.ProjectID, operation, decision, current, image)
	return err
}

func denyEnvironmentTx(ctx context.Context, q Q, s EnvironmentState, req EnvironmentRequest) error {
	prior, err := environmentDecisionTx(ctx, q, s.ProjectID)
	if err != nil {
		return err
	}
	if prior != nil {
		if prior.RevisionID == req.RevisionID && prior.Decision == "denied" {
			return nil
		}
		return Domainf("The Project already has another image decision.")
	}
	if s.SelectedRevisionID != "" || s.Phase != "approval" || s.ActiveOperationID != "" || req.RevisionID != s.DraftRevisionID {
		return Domainf("Continue with current image requires the current initial setup proposal.")
	}
	if err = recordEnvironmentDecisionTx(ctx, q, s, req.RevisionID, "denied"); err != nil {
		return err
	}
	d, err := environmentDecisionTx(ctx, q, s.ProjectID)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `UPDATE project_environments SET draft_revision_id=?,phase='queued',instruction='',cause='' WHERE project_id=?`, d.CurrentRevision, s.ProjectID)
	return err
}

func environmentContinuationBriefTx(ctx context.Context, q Q, s EnvironmentState) (string, error) {
	if s.SelectedRevisionID != "" {
		return "", nil
	}
	d, err := environmentDecisionTx(ctx, q, s.ProjectID)
	if err != nil || d == nil {
		return "", err
	}
	r, err := environmentRevisionTx(ctx, q, d.RevisionID)
	if err != nil {
		return "", err
	}
	var summary, details string
	if err = q.QueryRowContext(ctx, `SELECT summary,details FROM environment_results WHERE operation_id=?`, d.OperationID).Scan(&summary, &details); err != nil {
		return "", err
	}
	body, _ := json.MarshalIndent(map[string]any{"decision": d, "proposal": r.Configuration, "summary": summary, "details": details}, "", "  ")
	return "\n## Initial setup continuation\n\nThe operator decided this image proposal. The required outcome is `complete`.\nPrepare what the assigned image permits and report unresolved limits. After denial, state that setup used the current image in the summary. Further image improvements use the Environment page after import.\n\n```json\n" + string(body) + "\n```\n", nil
}
