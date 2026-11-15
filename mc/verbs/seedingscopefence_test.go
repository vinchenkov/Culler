package verbs

import (
	"testing"

	"mc/boundary"
	"mc/dispatch"
	"mc/refusal"
)

// Strategist reads its own Project's committed tree through one
// seeding projection. Its two health
// refusals live on the projection fence: the run's Project with an
// incomplete profile, and the run's Project whose id cannot be one
// destination component. captureSeedingProjectionStep hands the resident
// `/workspace/seeding/<id>/source` and validates neither.
func TestSeedingProjectionFenceRefusesAnIncompleteProfileOfTheRunsProject(t *testing.T) {
	root := t.TempDir()
	mcHome := maMkdir(t, root, "mc-home")
	state := PrivateDispatchMountState{SelectedProject: "repo", Projects: []PrivateDispatchProject{{
		ProjectID: "repo", Status: "active",
		ProfilePresent: false, ArtifactRoots: []string{}, DeniedPaths: []string{},
	}}}
	plan, r, err := attestCandidateMounts(mcHome, &preparedCandidate{
		spawn: &dispatch.Spawn{Project: "repo", Role: dispatch.RoleStrategist}, mountState: state,
	}, boundary.BlockPolicy{})
	if err != nil || r == nil || plan != nil {
		t.Fatalf("incomplete-profile seeding attest = plan %+v refusal %+v err %v", plan, r, err)
	}
	if r.Code != boundary.CodeRuntimeUnappliable || r.Authority != refusal.AuthorityDeployment {
		t.Fatalf("incomplete-profile seeding refusal = %+v", r)
	}
	if class, cerr := refusal.Classify(*r); cerr != nil || class != refusal.ClassHealth {
		t.Fatalf("incomplete-profile seeding classify = %v/%v, want health", class, cerr)
	}
}

func TestSeedingProjectionFenceRefusesAnUnusableProjectID(t *testing.T) {
	root := t.TempDir()
	mcHome := maMkdir(t, root, "mc-home")
	workspace := maMkdir(t, root, "workspace")
	state := PrivateDispatchMountState{SelectedProject: "alpha/beta", Projects: []PrivateDispatchProject{{
		ProjectID: "alpha/beta", Status: "active",
		ProfilePresent: true, ProfileID: "p", WorkspaceRoot: workspace,
		ArtifactRoots: []string{}, DeniedPaths: []string{},
	}}}
	plan, r, err := attestCandidateMounts(mcHome, &preparedCandidate{
		spawn: &dispatch.Spawn{Project: "alpha/beta", Role: dispatch.RoleStrategist}, mountState: state,
	}, boundary.BlockPolicy{})
	if err != nil || r == nil || plan != nil {
		t.Fatalf("unusable-id seeding attest = plan %+v refusal %+v err %v", plan, r, err)
	}
	if r.Code != boundary.CodeTargetInvalid || r.Authority != refusal.AuthorityDeployment {
		t.Fatalf("unusable-id seeding refusal = %+v", r)
	}
}

// Only the run's Project is fenced: another active Project with an
// incomplete profile is another pipeline's health and must not refuse this
// round's proposer (one Project's pipeline does not read the state of
// another).
func TestSeedingProjectionFenceIgnoresAnotherProjectsIncompleteProfile(t *testing.T) {
	root := t.TempDir()
	mcHome := maMkdir(t, root, "mc-home")
	if _, err := EnsureMountAllowlist(mcHome); err != nil {
		t.Fatal(err)
	}
	repo, _, _ := buildSourceRepo(t)
	state := PrivateDispatchMountState{SelectedProject: "repo", Projects: []PrivateDispatchProject{{
		ProjectID: "broken", Status: "active",
		ProfilePresent: false, ArtifactRoots: []string{}, DeniedPaths: []string{},
	}, {
		ProjectID: "repo", Status: "active",
		ProfilePresent: true, ProfileID: "repo-profile", WorkspaceRoot: repo,
		ArtifactRoots: []string{}, DeniedPaths: []string{},
	}}}
	plan, r, err := attestCandidateMounts(mcHome, &preparedCandidate{
		spawn: &dispatch.Spawn{Project: "repo", Role: dispatch.RoleStrategist}, mountState: state,
	}, boundary.BlockPolicy{})
	if err != nil || r != nil || plan == nil {
		t.Fatalf("seeding attest beside a broken Project = plan %+v refusal %+v err %v, want a plan", plan, r, err)
	}
	if len(plan.SeedingProjections) != 1 || plan.SeedingProjections[0].ProjectID != "repo" {
		t.Fatalf("seeding plan = %+v, want exactly the run's Project projection", plan)
	}
}
