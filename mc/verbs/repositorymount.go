package verbs

import (
	"context"
	"database/sql"

	"mc/substrate"
)

func loadRepositoryProjection(ctx context.Context, q Q, task int64) (*substrate.DispatchRepository, error) {
	var p substrate.DispatchRepository
	var version sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT t.id,COALESCE(cp.result_commit,v.result_commit,t.input_commit,''),t.destination_branch,t.submitted_version,
		p.max_temporary_bytes
		FROM tasks t JOIN repository_limits p ON p.project=t.project
		LEFT JOIN task_versions cp ON cp.id=t.checkpoint_version LEFT JOIN task_versions v ON v.id=t.submitted_version WHERE t.id=?`, task).
		Scan(&p.TaskID, &p.InputCommit, &p.DestinationBranch, &version, &p.MaxTemporaryBytes)
	if err == sql.ErrNoRows {
		return nil, Domainf("task %d does not exist", task)
	}
	if err != nil {
		return nil, err
	}
	if version.Valid {
		p.VersionID = &version.Int64
	}
	return &p, nil
}

func repositoryCodeRole(role string) bool {
	return baseRole(role) == "worker" || baseRole(role) == "verifier" || baseRole(role) == "scribe" || baseRole(role) == "packager"
}

func expectedRepositoryPlan(state PrivateDispatchMountState, role string) *PrivateRepositoryPlan {
	if state.SubjectRepository == nil || !repositoryCodeRole(role) {
		return nil
	}
	for _, w := range state.Projects {
		if w.ProjectID == state.SelectedProject {
			return &PrivateRepositoryPlan{DispatchRepository: *state.SubjectRepository, WorkspaceRoot: w.WorkspaceRoot}
		}
	}
	return nil
}

// PrivateRepositoryPlan is the isolated repository plan of a subject run.
// Its json keys are alphabetical, like the plan that carries it.
type PrivateRepositoryPlan struct {
	substrate.DispatchRepository
	WorkspaceRoot string `json:"workspace_root"`
}
