package verbs

import (
	"context"

	"mc/dispatch"
	"mc/substrate"
)

// PrivateDispatchMountState is the spine-owned, non-secret input the host
// needs to assemble the mount jurisdiction. It is frozen under the preparation
// token and repeated in the private candidate frame; the helper reloads and
// compares it before commit, so profile or Project drift is stale.
type PrivateDispatchMountState = substrate.DispatchMountState
type PrivateDispatchProject = substrate.DispatchProject

func loadDispatchMountState(ctx context.Context, q Q, sp *dispatch.Spawn, rec dispatch.Records) (PrivateDispatchMountState, error) {
	state := PrivateDispatchMountState{Projects: []PrivateDispatchProject{}}
	if sp.SubjectID == nil {
		// A subjectless run is bound to the round's Project like every other
		// run: its mount plan is the Sandbox Profile of that Project.
		state.SelectedProject = sp.Project
	}
	if sp.SubjectID != nil {
		var err error
		state.SubjectRepository, err = loadRepositoryProjection(ctx, q, *sp.SubjectID)
		if err != nil {
			return state, err
		}
		for _, task := range rec.Tasks {
			if task.ID == *sp.SubjectID {
				state.SelectedProject = task.Project
				break
			}
		}
		if state.SelectedProject == "" {
			return state, Domainf("dispatch: selected subject %d has no Project projection", *sp.SubjectID)
		}
		if sp.Project != "" && state.SelectedProject != sp.Project {
			return state, Domainf("dispatch: selected subject %d belongs to Project %q, not the round's %q", *sp.SubjectID, state.SelectedProject, sp.Project)
		}
	}

	rows, err := substrate.LoadDispatchProjectProjection(ctx, q)
	if err != nil {
		return state, err
	}
	state.Projects = rows
	return state, nil
}
