package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"mc/boundary"
	"mc/dispatch"
	"mc/refusal"
	"mc/substrate"
)

func maID(t *testing.T, path string) boundary.ProtectedID {
	t.Helper()
	id, err := boundary.ResolveSource(path)
	if err != nil {
		t.Fatalf("ResolveSource(%q): %v", path, err)
	}
	return boundary.ProtectedID{Canonical: id.Canonical, Info: id.Info, IsDir: id.IsDir}
}

func maMkdir(t *testing.T, root, name string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAssembleDispatchMountInputsUsesSelectedProfileAndCompleteJurisdiction(t *testing.T) {
	root := t.TempDir()
	operatorHome := maMkdir(t, root, "operator-home")
	mcHome := maMkdir(t, root, "mc-home")
	homeClass := maMkdir(t, operatorHome, ".ssh")
	gateway := maMkdir(t, root, "gateway-secret")
	ownWorkspace := maMkdir(t, root, "own-workspace")
	ownWorktree := maMkdir(t, root, "own-worktree")
	ownArtifact := maMkdir(t, root, "own-artifact")
	ownState := maMkdir(t, root, "own-state")
	ownCache := maMkdir(t, root, "own-cache")
	ownTool := maMkdir(t, root, "own-tool")
	ownGit := maMkdir(t, root, "own-git")
	otherWorkspace := maMkdir(t, root, "other-workspace")
	otherWorktree := maMkdir(t, root, "other-worktree")
	otherArtifact := maMkdir(t, root, "other-artifact")
	otherState := maMkdir(t, root, "other-state")
	otherCache := maMkdir(t, root, "other-cache")
	otherTool := maMkdir(t, root, "other-tool")
	otherGit := maMkdir(t, root, "other-git")
	ownRuntime := maMkdir(t, root, "own-runtime")
	otherRuntime := maMkdir(t, root, "other-runtime")

	state := PrivateDispatchMountState{SelectedProject: "own", Projects: []PrivateDispatchProject{
		{ProjectID: "other", Status: "active", ProfilePresent: true,
			ProfileID: "other-profile", WorkspaceRoot: otherWorkspace,
			ArtifactRoots: []string{otherArtifact}, DeniedPaths: []string{},
			ToolHomeDir: otherTool, RuntimeControlDir: otherRuntime},
		{ProjectID: "own", Status: "active", ProfilePresent: true,
			ProfileID: "own-profile", WorkspaceRoot: ownWorkspace,
			ArtifactRoots: []string{ownArtifact},
			DeniedPaths:   []string{filepath.Join(ownWorkspace, "private")},
			ToolHomeDir:   ownTool, RuntimeControlDir: ownRuntime},
	}}
	snapshot := dispatchMountHostSnapshot{
		OperatorHome:   operatorHome,
		MCHome:         maID(t, mcHome),
		HomeClassRoots: []boundary.ProtectedID{maID(t, homeClass)},
		GatewaySecrets: []boundary.ProtectedID{maID(t, gateway)},
		ProjectRoots: map[string]boundary.ProjectRoots{
			"own": {
				Workspace: maID(t, ownWorkspace), Worktree: maID(t, ownWorktree),
				Artifacts: []boundary.ProtectedID{maID(t, ownArtifact)}, State: maID(t, ownState),
				Cache: maID(t, ownCache), ToolHome: maID(t, ownTool),
			},
			"other": {
				Workspace: maID(t, otherWorkspace), Worktree: maID(t, otherWorktree),
				Artifacts: []boundary.ProtectedID{maID(t, otherArtifact)}, State: maID(t, otherState),
				Cache: maID(t, otherCache), ToolHome: maID(t, otherTool),
			},
		},
		GitControls: map[string][]boundary.ProtectedID{
			"own": {maID(t, ownGit)}, "other": {maID(t, otherGit)},
		},
		ResolveDeclared: func(path string) (boundary.ProtectedID, error) {
			id, err := boundary.ResolveSource(path)
			if err != nil {
				return boundary.ProtectedID{}, err
			}
			return boundary.ProtectedID{Canonical: id.Canonical, Info: id.Info, IsDir: id.IsDir}, nil
		},
	}

	requests, selected, r, err := deriveDispatchMountRequests(state)
	if err != nil || r != nil {
		t.Fatalf("derive = refusal %+v err %v", r, err)
	}
	assembled, err := assembleDispatchMountInputs(snapshot, state, requests, selected)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	wantRequests := []mountRequest{
		{Source: ownArtifact, Access: boundary.AccessRW, Authority: refusal.AuthorityCandidate, Class: classArtifact},
	}
	if !reflect.DeepEqual(assembled.Requests, wantRequests) {
		t.Fatalf("requests = %+v, want %+v", assembled.Requests, wantRequests)
	}
	in := assembled.Jurisdiction
	if in.Home != operatorHome || !reflect.DeepEqual(in.DeniedPaths, state.Projects[1].DeniedPaths) ||
		len(in.HomeClassRoots) != 1 || len(in.GatewaySecrets) != 1 || len(in.RuntimeControls) != 2 ||
		len(in.OtherProjects) != 1 || len(in.OwnGitControls) != 1 || len(in.OtherGitControls) != 1 {
		t.Fatalf("jurisdiction input omitted a required class: %+v", in)
	}
	j, err := boundary.ResolveJurisdiction(in, os.Getuid())
	if err != nil {
		t.Fatalf("ResolveJurisdiction: %v", err)
	}
	ownID, _ := boundary.ResolveSource(ownArtifact)
	if err := j.Rejects(ownID); err != nil {
		t.Fatalf("own ordinary artifact rejected: %v", err)
	}
	otherID, _ := boundary.ResolveSource(otherArtifact)
	if err := j.Rejects(otherID); err == nil {
		t.Fatalf("other Project artifact was accepted")
	} else if varME := new(boundary.MountError); !errors.As(err, &varME) || varME.Code != boundary.CodeCrossProject {
		t.Fatalf("other Project artifact = %v, want %s", err, boundary.CodeCrossProject)
	}
}

// Strategist is bound to the Project of the round. The plan of the
// proposer carries exactly one committed projection, the projection of its
// own Project, even when another Project is active.
func TestProposerMountsOnlyItsProjectCommittedProjection(t *testing.T) {
	root := t.TempDir()
	mcHome := maMkdir(t, root, "mc-home")
	if _, err := EnsureMountAllowlist(mcHome); err != nil {
		t.Fatal(err)
	}
	repo, _, _ := buildSourceRepo(t)
	other, _, _ := buildSourceRepo(t)
	state := PrivateDispatchMountState{SelectedProject: "repo", Projects: []PrivateDispatchProject{{
		ProjectID: "other", Status: "active", ProfilePresent: true,
		ProfileID: "other-profile", WorkspaceRoot: other,
		ArtifactRoots: []string{}, DeniedPaths: []string{},
	}, {
		ProjectID: "repo", Status: "active", ProfilePresent: true,
		ProfileID: "repo-profile", WorkspaceRoot: repo,
		ArtifactRoots: []string{}, DeniedPaths: []string{},
	}}}
	plan, r, err := attestCandidateMounts(mcHome, &preparedCandidate{
		spawn: &dispatch.Spawn{Project: "repo", Role: dispatch.RoleStrategist}, mountState: state,
	}, boundary.BlockPolicy{})
	if err != nil || r != nil || plan == nil {
		t.Fatalf("Git seeding committed projection = plan %+v refusal %+v err %v", plan, r, err)
	}
	if len(plan.Entries) != 0 || len(plan.SeedingProjections) != 1 {
		t.Fatalf("Git seeding plan = %+v, want exactly one projection and no live-root entry", plan)
	}
	step := plan.SeedingProjections[0]
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if step.ProjectID != "repo" || step.TargetRef != "HEAD" || step.WorkspaceRoot.Canonical != canonicalRepo {
		t.Fatalf("Git seeding projection = %+v", step)
	}
	if plan.JurisdictionDigest == "" {
		t.Fatalf("proposer plan carries no jurisdiction digest: %+v", plan)
	}
}

// The Arbiter reads the committed tree of its Project in each mode. A role with
// a repository plan derives its ordinary mounts as the Arbiter, and it carries
// no projection.
func TestArbiterMountsTheCommittedProjectionOfItsProject(t *testing.T) {
	root := t.TempDir()
	mcHome := maMkdir(t, root, "mc-home")
	if _, err := EnsureMountAllowlist(mcHome); err != nil {
		t.Fatal(err)
	}
	repo, _, _ := buildSourceRepo(t)
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	subject := int64(7)
	project := PrivateDispatchProject{
		ProjectID: "repo", Status: "active", ProfilePresent: true,
		ProfileID: "repo-profile", WorkspaceRoot: repo,
		ArtifactRoots: []string{}, DeniedPaths: []string{},
	}
	pool := PrivateDispatchMountState{SelectedProject: "repo", Projects: []PrivateDispatchProject{project}}
	delivery := pool
	delivery.SubjectRepository = &substrate.DispatchRepository{TaskID: subject, DestinationBranch: "main"}

	for _, tc := range []struct {
		name       string
		spawn      dispatch.Spawn
		state      PrivateDispatchMountState
		projection bool
	}{
		{"arbiter", dispatch.Spawn{Project: "repo", Role: dispatch.RoleArbiter, ProposedPool: []int64{7, 8}}, pool, true},
		{"worker", dispatch.Spawn{Project: "repo", Role: dispatch.RoleWorker, SubjectID: &subject}, delivery, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spawn := tc.spawn
			plan, r, err := attestCandidateMounts(mcHome, &preparedCandidate{spawn: &spawn, mountState: tc.state}, boundary.BlockPolicy{})
			if err != nil || r != nil || plan == nil {
				t.Fatalf("plan %+v refusal %+v err %v", plan, r, err)
			}
			if !tc.projection {
				if len(plan.SeedingProjections) != 0 || plan.Repository == nil {
					t.Fatalf("plan = %+v, want the repository plan and no projection", plan)
				}
				return
			}
			if len(plan.SeedingProjections) != 1 || plan.Repository != nil {
				t.Fatalf("plan = %+v, want one projection and no repository plan", plan)
			}
			step := plan.SeedingProjections[0]
			if step.ProjectID != "repo" || step.TargetRef != "HEAD" || step.WorkspaceRoot.Canonical != canonicalRepo {
				t.Fatalf("projection = %+v", step)
			}
			if err := validatePrivateMountPlan(plan); err != nil {
				t.Fatalf("the plan fails its own validation: %v", err)
			}
		})
	}
}

// A candidate that names no Project is a protocol defect, never an empty
// plan. Every pipeline run belongs to one Project, the subjectless role
// included.
func TestAttestCandidateMountsRefusesACandidateWithoutAProject(t *testing.T) {
	root := t.TempDir()
	mcHome := maMkdir(t, root, "mc-home")
	for _, role := range []dispatch.Role{dispatch.RoleStrategist} {
		plan, r, err := attestCandidateMounts(mcHome, &preparedCandidate{
			spawn: &dispatch.Spawn{Role: role}, mountState: PrivateDispatchMountState{Projects: []PrivateDispatchProject{}},
		}, boundary.BlockPolicy{})
		if err == nil || r != nil || plan != nil {
			t.Fatalf("%s without a Project = plan %+v refusal %+v err %v, want a protocol error", role, plan, r, err)
		}
	}
}

func TestDispatchInvalidSelectedProfileMountNeverClaimsOrSpawns(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	root := t.TempDir()
	operatorHome := maMkdir(t, root, "operator-home")
	t.Setenv("HOME", operatorHome)
	if err := os.Chmod(os.Getenv("MC_HOME"), 0o700); err != nil {
		t.Fatal(err)
	}
	allowed := maMkdir(t, root, "allowed")
	missing := filepath.Join(allowed, "missing")
	dvExec(t, db, `UPDATE sandbox_profiles SET artifact_roots=? WHERE id='default'`, fmt.Sprintf(`[%q]`, missing))
	allowlist := fmt.Sprintf("version = 1\n\n[[allow]]\npath = %q\ntarget = \"artifacts\"\naccess = \"rw\"\n", allowed)
	if err := os.WriteFile(filepath.Join(os.Getenv("MC_HOME"), "mount-allowlist"), []byte(allowlist), 0o600); err != nil {
		t.Fatal(err)
	}
	oldCapture := captureDispatchMountSnapshot
	captureDispatchMountSnapshot = func(home string, state PrivateDispatchMountState) (dispatchMountHostSnapshot, error) {
		return dispatchMountHostSnapshot{
			OperatorHome: operatorHome, OwnerUID: os.Getuid(), MCHome: maID(t, home),
			HomeClassRoots: []boundary.ProtectedID{}, GatewaySecrets: []boundary.ProtectedID{},
			ProjectRoots: map[string]boundary.ProjectRoots{
				"ws-test": {Workspace: boundary.ProtectedID{Canonical: "/tmp/ws-test"}},
			},
			GitControls: map[string][]boundary.ProtectedID{"ws-test": {}},
			ResolveDeclared: func(path string) (boundary.ProtectedID, error) {
				return boundary.ProtectedID{Canonical: filepath.Clean(path)}, nil
			},
		}, nil
	}
	t.Cleanup(func() { captureDispatchMountSnapshot = oldCapture })

	prepared := dfPrepare(t, db, dfRequestID)
	attested, err := dispatchAttest(os.Getenv("MC_HOME"), prepared)
	if err != nil {
		t.Fatalf("dispatchAttest: %v", err)
	}
	if attested.refusal == nil || attested.refusal.Code != boundary.CodeSourceMissing {
		t.Fatalf("attestation refusal = %+v, want %s", attested.refusal, boundary.CodeSourceMissing)
	}
	if attested.refusal.Authority != refusal.AuthorityCandidate {
		t.Fatalf("invalid selected-profile source authority = %q", attested.refusal.Authority)
	}
	eff := dfCommit(t, db, prepared, attested)
	dfAssertInert(t, db, eff)
	if eff["action"] == "spawn" || eff["code"] != boundary.CodeSourceMissing {
		t.Fatalf("invalid mount effect = %v", eff)
	}
	if n := dfInt(t, db, `SELECT COUNT(*) FROM activity WHERE kind='dispatch.spawn'`); n != 0 {
		t.Fatalf("invalid mount wrote %d dispatch.spawn rows", n)
	}
	if n := dfInt(t, db, `SELECT COUNT(*) FROM tasks WHERE id=1 AND blocked=1`); n != 1 {
		t.Fatalf("invalid candidate mount blocked %d subject rows, want one", n)
	}
}

func TestDispatchInvalidDeniedPathWithoutRequestsNeverClaims(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	dvExec(t, db, `UPDATE sandbox_profiles SET denied_paths='["relative/path"]' WHERE id='default'`)
	root := t.TempDir()
	operatorHome := maMkdir(t, root, "operator-home")
	if err := os.Chmod(os.Getenv("MC_HOME"), 0o700); err != nil {
		t.Fatal(err)
	}
	oldCapture := captureDispatchMountSnapshot
	captureDispatchMountSnapshot = func(home string, state PrivateDispatchMountState) (dispatchMountHostSnapshot, error) {
		return dispatchMountHostSnapshot{
			OperatorHome: operatorHome, OwnerUID: os.Getuid(), MCHome: maID(t, home),
			HomeClassRoots: []boundary.ProtectedID{}, GatewaySecrets: []boundary.ProtectedID{},
			ProjectRoots: map[string]boundary.ProjectRoots{
				"ws-test": {Workspace: boundary.ProtectedID{Canonical: "/tmp/ws-test"}},
			},
			GitControls: map[string][]boundary.ProtectedID{"ws-test": {}},
			ResolveDeclared: func(path string) (boundary.ProtectedID, error) {
				return boundary.ProtectedID{Canonical: filepath.Clean(path)}, nil
			},
		}, nil
	}
	t.Cleanup(func() { captureDispatchMountSnapshot = oldCapture })

	prepared := dfPrepare(t, db, "0123456789abcdec")
	attested, err := dispatchAttest(os.Getenv("MC_HOME"), prepared)
	if err != nil {
		t.Fatalf("dispatchAttest: %v", err)
	}
	if attested.refusal == nil || attested.refusal.Code != boundary.CodeDeniedRoot ||
		attested.refusal.Authority != refusal.AuthorityCandidate {
		t.Fatalf("denied-path attestation = %+v", attested.refusal)
	}
	eff := dfCommit(t, db, prepared, attested)
	dfAssertInert(t, db, eff)
	if eff["action"] == "spawn" || eff["code"] != boundary.CodeDeniedRoot {
		t.Fatalf("invalid denied-path effect = %v", eff)
	}
}

func TestJurisdictionErrorProvenanceDoesNotChargeCandidateForDeploymentFailure(t *testing.T) {
	root := t.TempDir()
	home := maMkdir(t, root, "home")
	base := boundary.JurisdictionInput{
		Home: home, MCHome: boundary.ProtectedID{Canonical: filepath.Join(root, "mc-home")},
		DeniedPaths: []string{"relative/path"},
	}
	_, err := boundary.ResolveJurisdiction(base, os.Getuid())
	var me *boundary.MountError
	if !errors.As(err, &me) || !me.CandidateAuthored {
		t.Fatalf("denied-path failure provenance = %#v, want candidate-authored", err)
	}

	base.Home = filepath.Join(root, "missing-home")
	_, err = boundary.ResolveJurisdiction(base, os.Getuid())
	me = nil
	if !errors.As(err, &me) || me.CandidateAuthored {
		t.Fatalf("deployment failure provenance = %#v, must not charge candidate", err)
	}
}

// maStubSnapshot installs a hand-built host snapshot for the given
// Projects, returning the operator home it fabricates. The real capture is
// exercised separately; these tests own the plan construction after it.
func maStubSnapshot(t *testing.T, root string, wsIDs ...string) string {
	t.Helper()
	operatorHome := maMkdir(t, root, "operator-home")
	oldCapture := captureDispatchMountSnapshot
	captureDispatchMountSnapshot = func(home string, state PrivateDispatchMountState) (dispatchMountHostSnapshot, error) {
		snapshot := dispatchMountHostSnapshot{
			OperatorHome: operatorHome, OwnerUID: os.Getuid(), MCHome: maID(t, home),
			HomeClassRoots: []boundary.ProtectedID{}, GatewaySecrets: []boundary.ProtectedID{},
			ProjectRoots: map[string]boundary.ProjectRoots{},
			GitControls:  map[string][]boundary.ProtectedID{},
			ResolveDeclared: func(path string) (boundary.ProtectedID, error) {
				return resolveDispatchProtected(path, true)
			},
		}
		for _, id := range wsIDs {
			roots := boundary.ProjectRoots{
				Workspace: boundary.ProtectedID{Canonical: "/tmp/" + id},
			}
			for _, ws := range state.Projects {
				if ws.ProjectID != id {
					continue
				}
				// The seeding projection needs the stat evidence of a real root.
				if info, err := os.Stat(ws.WorkspaceRoot); err == nil && info.IsDir() {
					roots.Workspace = maID(t, ws.WorkspaceRoot)
				}
				if ws.ToolHomeDir != "" {
					roots.ToolHome = maID(t, ws.ToolHomeDir)
				}
				break
			}
			snapshot.ProjectRoots[id] = roots
			snapshot.GitControls[id] = []boundary.ProtectedID{}
		}
		return snapshot, nil
	}
	t.Cleanup(func() { captureDispatchMountSnapshot = oldCapture })
	return operatorHome
}

func maEvidence(t *testing.T, source string) (device, inode string, uid, mode int) {
	t.Helper()
	res, err := boundary.ResolveSource(source)
	if err != nil {
		t.Fatalf("ResolveSource(%q): %v", source, err)
	}
	st, ok := res.Info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("no Stat_t for %q", source)
	}
	return strconv.FormatUint(uint64(st.Dev), 10), strconv.FormatUint(st.Ino, 10),
		int(st.Uid), int(res.Info.Mode().Perm())
}

// A valid nonempty ordinary profile yields the evidence-backed plan carrier:
// canonical sources, class-prefixed deterministic destinations, access, and
// the (device, inode, kind, owner, mode) identity, sorted by destination.
func TestAttestCandidateMountsBuildsEvidencePlan(t *testing.T) {
	root := t.TempDir()
	mcHome := maMkdir(t, root, "mc-home")
	artifact := maMkdir(t, root, "artifact")
	manifest := filepath.Join(root, "manifest.md")
	if err := os.WriteFile(manifest, []byte("doc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	allowlist := fmt.Sprintf("version = 1\n\n[[allow]]\npath = %q\ntarget = \"art\"\naccess = \"rw\"\n"+
		"\n[[allow]]\npath = %q\ntarget = \"doc\"\naccess = \"rw\"\n", artifact, manifest)
	if err := os.WriteFile(filepath.Join(mcHome, "mount-allowlist"), []byte(allowlist), 0o600); err != nil {
		t.Fatal(err)
	}
	maStubSnapshot(t, root, "ws-plan")
	state := PrivateDispatchMountState{SelectedProject: "ws-plan", Projects: []PrivateDispatchProject{{
		ProjectID: "ws-plan", Status: "active", ProfilePresent: true,
		ProfileID: "p", WorkspaceRoot: "/tmp/ws-plan",
		ArtifactRoots: []string{artifact, manifest}, DeniedPaths: []string{},
	}}}

	plan, r, err := attestOrdinaryMounts(mcHome, &preparedCandidate{spawn: &dispatch.Spawn{Role: dispatch.RoleWorker}, mountState: state}, boundary.BlockPolicy{})
	if err != nil || r != nil {
		t.Fatalf("attest = refusal %+v err %v, want a plan", r, err)
	}
	if plan == nil || plan.Version != 1 || len(plan.Entries) != 2 {
		t.Fatalf("plan = %+v, want version 1 with two entries", plan)
	}
	artDev, artIno, artUID, artMode := maEvidence(t, artifact)
	artCanonical, _ := boundary.ResolveSource(artifact)
	want0 := PrivateDispatchMountEntry{
		LogicalID: "artifact:art", Source: artCanonical.Canonical, Destination: "/mc/artifacts/art",
		Kind: "dir", Access: "rw", Device: artDev, Inode: artIno, OwnerUID: artUID, Mode: artMode,
	}
	if plan.Entries[0] != want0 {
		t.Fatalf("artifact entry = %+v, want %+v", plan.Entries[0], want0)
	}
	docDev, docIno, docUID, docMode := maEvidence(t, manifest)
	docCanonical, _ := boundary.ResolveSource(manifest)
	want1 := PrivateDispatchMountEntry{
		LogicalID: "artifact:doc", Source: docCanonical.Canonical, Destination: "/mc/artifacts/doc",
		Kind: "file", Access: "rw", Device: docDev, Inode: docIno, OwnerUID: docUID, Mode: docMode,
	}
	if plan.Entries[1] != want1 {
		t.Fatalf("file artifact entry = %+v, want %+v", plan.Entries[1], want1)
	}
}

func TestAttestCandidateMountsCarriesTheSelectedSyntheticHome(t *testing.T) {
	root := t.TempDir()
	mcHome := maMkdir(t, root, "mc-home")
	operatorHome := maMkdir(t, root, "operator-home")
	workspace := maMkdir(t, root, "workspace")
	stateRoot := maMkdir(t, mcHome, "state")
	toolHome := maID(t, maMkdir(t, stateRoot, "tool-home")).Canonical
	if err := os.WriteFile(filepath.Join(mcHome, "mount-allowlist"), []byte("version = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldCapture := captureDispatchMountSnapshot
	captureDispatchMountSnapshot = func(string, PrivateDispatchMountState) (dispatchMountHostSnapshot, error) {
		return dispatchMountHostSnapshot{
			OperatorHome: operatorHome, OwnerUID: os.Getuid(), MCHome: maID(t, mcHome),
			HomeClassRoots: []boundary.ProtectedID{}, GatewaySecrets: []boundary.ProtectedID{},
			ProjectRoots: map[string]boundary.ProjectRoots{
				"primary": {Workspace: maID(t, workspace), State: maID(t, stateRoot), ToolHome: maID(t, toolHome)},
			},
			GitControls: map[string][]boundary.ProtectedID{"primary": {}},
			ResolveDeclared: func(path string) (boundary.ProtectedID, error) {
				return resolveDispatchProtected(path, true)
			},
		}, nil
	}
	t.Cleanup(func() { captureDispatchMountSnapshot = oldCapture })
	state := PrivateDispatchMountState{
		SelectedProject: "primary",
		Projects: []PrivateDispatchProject{{
			ProjectID: "primary", Status: "active",
			ProfilePresent: true, ProfileID: "project-primary",
			WorkspaceRoot: workspace, ArtifactRoots: []string{},
			DeniedPaths: []string{},
			ToolHomeDir: toolHome,
		}},
	}
	plan, r, err := attestOrdinaryMounts(mcHome, &preparedCandidate{
		spawn: &dispatch.Spawn{Role: dispatch.RoleWorker}, mountState: state,
	}, boundary.BlockPolicy{})
	if err != nil || r != nil {
		t.Fatalf("synthetic-home attest = plan %+v refusal %+v err %v", plan, r, err)
	}
	if plan.ToolHome == nil || plan.ToolHome.Canonical != toolHome ||
		plan.ToolHome.OwnerUID != os.Getuid() || plan.ToolHome.Device == "" ||
		plan.ToolHome.Inode == "" {
		t.Fatalf("synthetic-home evidence = %+v", plan.ToolHome)
	}
}

// An absent sandbox profile is deployment configuration the candidate never
// authored: deployment health, not a per-task confinement block.
func TestAttestCandidateMountsAbsentProfileIsDeploymentHealth(t *testing.T) {
	root := t.TempDir()
	mcHome := maMkdir(t, root, "mc-home")
	state := PrivateDispatchMountState{SelectedProject: "ws-bare", Projects: []PrivateDispatchProject{{
		ProjectID: "ws-bare", Status: "active", ProfilePresent: false,
		ArtifactRoots: []string{}, DeniedPaths: []string{},
	}}}
	plan, r, err := attestCandidateMounts(mcHome, &preparedCandidate{spawn: &dispatch.Spawn{Role: dispatch.RoleWorker}, mountState: state}, boundary.BlockPolicy{})
	if err != nil || r == nil || plan != nil {
		t.Fatalf("absent-profile attest = plan %+v refusal %+v err %v", plan, r, err)
	}
	if r.Code != boundary.CodeRuntimeUnappliable || r.Authority != refusal.AuthorityDeployment {
		t.Fatalf("absent-profile refusal = %+v", r)
	}
	if class, cerr := refusal.Classify(*r); cerr != nil || class != refusal.ClassHealth {
		t.Fatalf("absent-profile classify = %v/%v, want health", class, cerr)
	}
}

// A boundary rejection while assembling jurisdiction (a declared
// runtime-control path failing resolution) is deployment health, never a
// dispatch protocol error.
func TestAttestCandidateMountsAssemblyFailureIsDeploymentHealth(t *testing.T) {
	root := t.TempDir()
	mcHome := maMkdir(t, root, "mc-home")
	artifact := maMkdir(t, root, "artifact")
	maStubSnapshot(t, root, "own", "other")
	state := PrivateDispatchMountState{SelectedProject: "own", Projects: []PrivateDispatchProject{
		{ProjectID: "other", Status: "active", ProfilePresent: true,
			ProfileID: "p2", RuntimeControlDir: "relative/path",
			ArtifactRoots: []string{}, DeniedPaths: []string{}},
		{ProjectID: "own", Status: "active", ProfilePresent: true,
			ProfileID: "p1", ArtifactRoots: []string{artifact}, DeniedPaths: []string{}},
	}}
	plan, r, err := attestOrdinaryMounts(mcHome, &preparedCandidate{spawn: &dispatch.Spawn{Role: dispatch.RoleWorker}, mountState: state}, boundary.BlockPolicy{})
	if err != nil || r == nil || plan != nil {
		t.Fatalf("assembly-failure attest = plan %+v refusal %+v err %v, want a health refusal", plan, r, err)
	}
	if r.Code != boundary.CodeSourceWrongKind || r.Authority != refusal.AuthorityDeployment {
		t.Fatalf("assembly-failure refusal = %+v", r)
	}
	if class, cerr := refusal.Classify(*r); cerr != nil || class != refusal.ClassHealth {
		t.Fatalf("assembly-failure classify = %v/%v, want health", class, cerr)
	}
}

// The carrier is byte-bounded at attest, before any claim: the committed
// spawn effect embeds the plan and must survive the broker's 64 KiB result
// cap, so an oversized plan refuses health here rather than wedging
// post-commit.
func TestAttestCandidateMountsOversizedPlanFailsHealth(t *testing.T) {
	root := t.TempDir()
	mcHome := maMkdir(t, root, "mc-home")
	bulk := maMkdir(t, root, "bulk")
	sources := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		name := fmt.Sprintf("%s%03d", strings.Repeat("a", 237), i)
		sources = append(sources, maMkdir(t, bulk, name))
	}
	allowlist := fmt.Sprintf("version = 1\n\n[[allow]]\npath = %q\ntarget = \"bulk\"\naccess = \"rw\"\n", bulk)
	if err := os.WriteFile(filepath.Join(mcHome, "mount-allowlist"), []byte(allowlist), 0o600); err != nil {
		t.Fatal(err)
	}
	maStubSnapshot(t, root, "ws-bulk")
	state := PrivateDispatchMountState{SelectedProject: "ws-bulk", Projects: []PrivateDispatchProject{{
		ProjectID: "ws-bulk", Status: "active", ProfilePresent: true,
		ProfileID: "p", ArtifactRoots: sources, DeniedPaths: []string{},
	}}}
	plan, r, err := attestOrdinaryMounts(mcHome, &preparedCandidate{spawn: &dispatch.Spawn{Role: dispatch.RoleWorker}, mountState: state}, boundary.BlockPolicy{})
	if err != nil || r == nil || plan != nil {
		t.Fatalf("oversized attest = plan %+v refusal %+v err %v, want the byte-bound refusal", plan, r, err)
	}
	if r.Code != boundary.CodeRuntimeUnappliable || r.Authority != refusal.AuthorityDeployment {
		t.Fatalf("oversized refusal = %+v", r)
	}
}

// maRepoCandidate builds a candidate over a real Git repository with one
// allowlisted artifact root inside it, and a trusted MC_HOME. Nothing stubs
// the snapshot: these tests drive the real captureDispatchMountHostSnapshot
// and the live Git registry. A subject carries a repository projection.
func maRepoCandidate(t *testing.T, role dispatch.Role, subject *int64) (string, *preparedCandidate, string) {
	t.Helper()
	repo, _, _ := buildSourceRepo(t)
	ws, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	artifact := maMkdir(t, ws, "artifacts")
	mcHome := maMkdir(t, filepath.Dir(ws), "mc-home-"+string(role))
	if err := os.Chmod(mcHome, 0o700); err != nil {
		t.Fatal(err)
	}
	allowlist := fmt.Sprintf("version = 1\n\n[[allow]]\npath = %q\ntarget = \"art\"\naccess = \"rw\"\n", artifact)
	if err := os.WriteFile(filepath.Join(mcHome, "mount-allowlist"), []byte(allowlist), 0o600); err != nil {
		t.Fatal(err)
	}
	state := PrivateDispatchMountState{SelectedProject: "repo-ws", Projects: []PrivateDispatchProject{{
		ProjectID: "repo-ws", Status: "active", ProfilePresent: true,
		ProfileID: "p", WorkspaceRoot: ws,
		ArtifactRoots: []string{artifact}, DeniedPaths: []string{},
	}}}
	if subject != nil {
		state.SubjectRepository = &substrate.DispatchRepository{
			TaskID: *subject, DestinationBranch: "main", MaxTemporaryBytes: 1 << 30,
		}
	}
	return mcHome, &preparedCandidate{spawn: &dispatch.Spawn{Role: role, SubjectID: subject}, mountState: state}, ws
}

func TestDispatchCreatesAnAbsentProjectHomeAndPreservesIt(t *testing.T) {
	home, candidate, _ := maRepoCandidate(t, dispatch.RoleWorker, nil)
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	projectHome := ProjectToolHome(canonical, "repo-ws")
	candidate.mountState.Projects[0].ToolHomeDir = projectHome
	first, err := captureDispatchMountHostSnapshot(home, candidate.mountState)
	if err != nil {
		t.Fatal(err)
	}
	if got := first.ProjectRoots["repo-ws"].ToolHome.Canonical; got != projectHome {
		t.Fatalf("Project home = %q, want %q", got, projectHome)
	}
	file := filepath.Join(projectHome, "retained")
	if err := os.WriteFile(file, []byte("dependency"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureDispatchMountHostSnapshot(home, candidate.mountState); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(file); err != nil || string(body) != "dependency" {
		t.Fatalf("The retained dependency changed: %q %v", body, err)
	}
	other := candidate.mountState.Projects[0]
	other.ProjectID = "other"
	other.ToolHomeDir = ProjectToolHome(canonical, other.ProjectID)
	if err := os.MkdirAll(other.ToolHomeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(other.ToolHomeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	candidate.mountState.Projects = append(candidate.mountState.Projects, other)
	if _, err := captureDispatchMountHostSnapshot(home, candidate.mountState); err != nil {
		t.Fatalf("Another Project's home prevented the selected Project snapshot: %v", err)
	}
}

// probeRepoObjectFormat reads the object format from the repository's
// administrative files only — the host never executes operator-installed git
// (the in-container executor re-verifies against the pinned image's git).
func TestProbeRepoObjectFormat(t *testing.T) {
	mkRepo := func(t *testing.T, config string) string {
		ws := t.TempDir()
		if err := os.Mkdir(filepath.Join(ws, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
		if config != "" {
			if err := os.WriteFile(filepath.Join(ws, ".git", "config"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return ws
	}
	t.Run("no_config_is_sha1", func(t *testing.T) {
		got, err := probeRepoObjectFormat(mkRepo(t, ""))
		if err != nil || got != "sha1" {
			t.Fatalf("= (%q, %v), want sha1", got, err)
		}
	})
	t.Run("config_without_extensions_is_sha1", func(t *testing.T) {
		got, err := probeRepoObjectFormat(mkRepo(t, "[core]\n\tbare = false\n"))
		if err != nil || got != "sha1" {
			t.Fatalf("= (%q, %v), want sha1", got, err)
		}
	})
	t.Run("extensions_sha256", func(t *testing.T) {
		got, err := probeRepoObjectFormat(mkRepo(t, "[core]\n\trepositoryformatversion = 1\n[extensions]\n\tobjectFormat = sha256\n"))
		if err != nil || got != "sha256" {
			t.Fatalf("= (%q, %v), want sha256", got, err)
		}
	})
	t.Run("section_and_key_are_case_insensitive", func(t *testing.T) {
		got, err := probeRepoObjectFormat(mkRepo(t, "[EXTENSIONS]\n\tObjectFormat = sha256\n"))
		if err != nil || got != "sha256" {
			t.Fatalf("= (%q, %v), want sha256", got, err)
		}
	})
	t.Run("unknown_format_refuses", func(t *testing.T) {
		if _, err := probeRepoObjectFormat(mkRepo(t, "[extensions]\n\tobjectFormat = sha512\n")); err == nil {
			t.Fatal("an unrecognized object format was accepted")
		}
	})
	t.Run("no_repo_refuses", func(t *testing.T) {
		if _, err := probeRepoObjectFormat(t.TempDir()); err == nil {
			t.Fatal("a workspace with no Git administrative directory was accepted")
		}
	})
	t.Run("oversized_config_refuses", func(t *testing.T) {
		if _, err := probeRepoObjectFormat(mkRepo(t, strings.Repeat("#\n", 64*1024))); err == nil {
			t.Fatal("an oversized administrative config was accepted")
		}
	})
	t.Run("symlinked_config_refuses", func(t *testing.T) {
		ws := mkRepo(t, "")
		target := filepath.Join(ws, "aliased-config")
		if err := os.WriteFile(target, []byte("[extensions]\n\tobjectFormat = sha256\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(ws, ".git", "config")); err != nil {
			t.Fatal(err)
		}
		if _, err := probeRepoObjectFormat(ws); err == nil {
			t.Fatal("a symlinked administrative config was accepted")
		}
	})
	t.Run("worktree_pointer_chases_to_common_config", func(t *testing.T) {
		base := t.TempDir()
		common := filepath.Join(base, "common")
		admin := filepath.Join(common, "worktrees", "wt")
		if err := os.MkdirAll(admin, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(common, "config"), []byte("[extensions]\n\tobjectFormat = sha256\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(admin, "commondir"), []byte("../..\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		ws := filepath.Join(base, "ws")
		if err := os.Mkdir(ws, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws, ".git"), []byte("gitdir: "+admin+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := probeRepoObjectFormat(ws)
		if err != nil || got != "sha256" {
			t.Fatalf("= (%q, %v), want sha256 via the commondir chase", got, err)
		}
	})
}

func TestAttestCandidateMountsRegistryProtectsRealGitControl(t *testing.T) {
	subject := int64(7)
	mcHome, cand, ws := maRepoCandidate(t, dispatch.RoleWorker, &subject)
	// An artifact root reaching inside the registered control refuses on
	// jurisdiction even though the operator allowlisted it: the registry now
	// feeds OwnGitControls, so the real object store can never ride an
	// ordinary mount.
	inside := filepath.Join(ws, ".git", "objects")
	if err := os.MkdirAll(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	allowlist := fmt.Sprintf("version = 1\n\n[[allow]]\npath = %q\ntarget = \"objects\"\naccess = \"rw\"\n", inside)
	if err := os.WriteFile(filepath.Join(mcHome, "mount-allowlist"), []byte(allowlist), 0o600); err != nil {
		t.Fatal(err)
	}
	cand.mountState.Projects[0].ArtifactRoots = []string{inside}

	plan, r, err := attestCandidateMounts(mcHome, cand, boundary.BlockPolicy{})
	if err != nil || r == nil || plan != nil {
		t.Fatalf("attest = plan %+v refusal %+v err %v, want the jurisdiction refusal", plan, r, err)
	}
	if r.Code != boundary.CodeDeniedRoot {
		t.Fatalf("control-intersecting artifact = %+v, want denied_root", r)
	}
}

// A subject run of a code role carries the repository plan of its task next
// to its ordinary mounts. The plan passes the helper boundary.
func TestAttestCandidateMountsCarriesTheRepositoryPlanOfTheSubject(t *testing.T) {
	subject := int64(7)
	mcHome, cand, ws := maRepoCandidate(t, dispatch.RoleWorker, &subject)
	plan, r, err := attestCandidateMounts(mcHome, cand, boundary.BlockPolicy{})
	if err != nil || r != nil || plan == nil {
		t.Fatalf("attest = plan %+v refusal %+v err %v", plan, r, err)
	}
	if plan.Repository == nil || plan.Repository.TaskID != subject || plan.Repository.WorkspaceRoot != ws ||
		plan.Repository.DestinationBranch != "main" {
		t.Fatalf("repository plan = %+v, want task %d at %q on main", plan.Repository, subject, ws)
	}
	if len(plan.Entries) != 1 || plan.Entries[0].Destination != "/mc/artifacts/art" {
		t.Fatalf("entries = %+v, want the one artifact root", plan.Entries)
	}
	if err := validatePrivateMountPlan(plan); err != nil {
		t.Fatalf("the helper boundary refused its own plan: %v", err)
	}
	// A role that reads no repository carries no repository plan.
	cand.spawn.Role = dispatch.RoleArbiter
	plan, r, err = attestCandidateMounts(mcHome, cand, boundary.BlockPolicy{})
	if err != nil || r != nil || plan == nil || plan.Repository != nil {
		t.Fatalf("arbiter attest = plan %+v refusal %+v err %v, want no repository plan", plan, r, err)
	}
}

func TestAttestCandidateMountsCarriesProtectedSetIdentityDrift(t *testing.T) {
	subject := int64(7)
	mcHome, cand, ws := maRepoCandidate(t, dispatch.RoleWorker, &subject)
	first, r, err := attestCandidateMounts(mcHome, cand, boundary.BlockPolicy{})
	if err != nil || r != nil {
		t.Fatalf("first attest = refusal %+v err %v", r, err)
	}

	gitPath := filepath.Join(ws, ".git")
	oldGit := filepath.Join(ws, ".git-old")
	if err := os.Rename(gitPath, oldGit); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(gitPath, 0o700); err != nil {
		t.Fatal(err)
	}
	second, r, err := attestCandidateMounts(mcHome, cand, boundary.BlockPolicy{})
	if err != nil || r != nil {
		t.Fatalf("second attest = refusal %+v err %v", r, err)
	}
	if reflect.DeepEqual(first, second) {
		t.Fatal("protected Git-control identity drift disappeared behind an unchanged mount verdict")
	}
}

func TestJurisdictionDigestCarriesAbsentMemberAnchorIdentity(t *testing.T) {
	root := t.TempDir()
	anchor := maMkdir(t, root, "anchor")
	declared := filepath.Join(anchor, "future", "git")
	in := boundary.JurisdictionInput{
		DeniedPaths:      []string{},
		OtherGitControls: []boundary.ProtectedID{{Canonical: declared}},
	}
	first, err := jurisdictionInputDigest(in, os.Getuid())
	if err != nil {
		t.Fatalf("first digest: %v", err)
	}
	if err := os.Rename(anchor, filepath.Join(root, "old-anchor")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(anchor, 0o700); err != nil {
		t.Fatal(err)
	}
	second, err := jurisdictionInputDigest(in, os.Getuid())
	if err != nil {
		t.Fatalf("second digest: %v", err)
	}
	if first == second {
		t.Fatal("the absent-member digest omitted the identity of its nearest existing ancestor")
	}
}

func TestJurisdictionDigestCarriesDeniedPathIdentity(t *testing.T) {
	root := t.TempDir()
	denied := maMkdir(t, root, "denied")
	in := boundary.JurisdictionInput{
		DeniedPaths: []string{denied},
	}
	first, err := jurisdictionInputDigest(in, os.Getuid())
	if err != nil {
		t.Fatalf("first digest: %v", err)
	}
	if err := os.Rename(denied, filepath.Join(root, "old-denied")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(denied, 0o700); err != nil {
		t.Fatal(err)
	}
	second, err := jurisdictionInputDigest(in, os.Getuid())
	if err != nil {
		t.Fatalf("second digest: %v", err)
	}
	if first == second {
		t.Fatal("denied-path identity drift disappeared behind its unchanged raw spelling")
	}
}

func TestJurisdictionDigestPreservesDeniedPathCandidateAuthority(t *testing.T) {
	_, err := jurisdictionInputDigest(boundary.JurisdictionInput{
		DeniedPaths: []string{"relative/path"},
	}, os.Getuid())
	var mountErr *boundary.MountError
	if !errors.As(err, &mountErr) || !mountErr.CandidateAuthored {
		t.Fatalf("denied-path evidence error = %v, want candidate-authored MountError", err)
	}
}

// The plan and the brief of the proposer survive prepare, attest, and commit
// bound to the Project of the round alone. With three active repository
// Projects, the ws-test round carries exactly one committed projection, and
// the brief names the projection path of ws-test and of no other Project.
func TestProposerSeedingPlanSurvivesPrepareAttestCommitForOneProject(t *testing.T) {
	alpha, _, _ := buildSourceRepo(t)
	db := dvSpine(t, func(a *InitArgs) { a.WorkspaceRoot = alpha })
	dvAddProject(t, db, "beta", "active")
	dvAddProject(t, db, "repo", "active")

	prepared := dfPrepare(t, db, dfRequestID)
	if prepared.candidate == nil || prepared.candidate.spawn.Role != dispatch.RoleStrategist ||
		prepared.candidate.spawn.Project != "ws-test" ||
		prepared.candidate.mountState.SelectedProject != "ws-test" {
		t.Fatalf("prepared candidate = %+v, want Strategist bound to ws-test", prepared.candidate)
	}
	attested, err := dispatchAttest(os.Getenv("MC_HOME"), prepared)
	if err != nil || attested.refusal != nil {
		t.Fatalf("proposer attest = refusal %+v err %v", attested.refusal, err)
	}
	effect := dfCommit(t, db, prepared, attested)
	if effect["action"] != "spawn" || effect["role"] != string(dispatch.RoleStrategist) || effect["project"] != "ws-test" {
		t.Fatalf("effect = %v, want proposer spawn for ws-test", effect)
	}
	body, err := json.Marshal(effect["mount_plan"])
	if err != nil {
		t.Fatal(err)
	}
	var plan PrivateDispatchMountPlan
	if err := json.Unmarshal(body, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Entries) != 0 || len(plan.SeedingProjections) != 1 {
		t.Fatalf("committed seeding plan = %+v, want no bound rows and exactly one Git projection", plan)
	}
	if step := plan.SeedingProjections[0]; step.ProjectID != "ws-test" || step.TargetRef != "HEAD" {
		t.Fatalf("committed seeding projection = %+v, want ws-test at HEAD", step)
	}
	brief := fmt.Sprint(effect["brief"])
	rendered := parseRenderedBrief(t, brief)
	project, vision := rendered.part(partProject), rendered.part(partVision)
	if project == nil || project.Facts["Identifier"] != "ws-test" || project.Facts["Repository copy"] != "/workspace/seeding/ws-test/source" {
		t.Fatalf("committed proposer brief lost its Project: %s", brief)
	}
	if vision == nil || vision.Text != "# Vision\n\nShip it." {
		t.Fatalf("committed proposer brief lost the vision content: %s", brief)
	}
	if strings.Contains(brief, ProjectVisionPath(alpha)) {
		t.Fatalf("committed proposer brief holds the host location of the vision file: %s", brief)
	}
	for _, leak := range []string{"/workspace/seeding/beta/source", "/workspace/seeding/repo/source", "seeding_scope"} {
		if strings.Contains(brief, leak) {
			t.Fatalf("committed proposer brief leaked another Project's state %q: %s", leak, brief)
		}
	}
}

func TestLoadDispatchMountStateIsRoleBlind(t *testing.T) {
	// The loader reads sp.SubjectID and never sp.Role: the mount-state
	// projection is subject-keyed, so a role must not change it. Assert it
	// across every role the spawn path can carry rather than trusting the
	// current implementation.
	db := dvSpine(t)
	taskID := int64(7)
	// The schema trigger writes the delivery record the loader projects.
	dvInsertTask(t, db, dvTask(taskID, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	rec := dispatch.Records{
		Tasks: []dispatch.Task{{ID: taskID, Project: "ws-test"}},
	}

	var first PrivateDispatchMountState
	for i, role := range []dispatch.Role{
		"", dispatch.RoleWorker, dispatch.RoleVerifier, dispatch.RolePackager, dispatch.RoleArbiter,
	} {
		state, err := loadDispatchMountState(context.Background(), db,
			&dispatch.Spawn{Role: role, SubjectID: &taskID}, rec)
		if err != nil {
			t.Fatalf("loadDispatchMountState(role=%q): %v", role, err)
		}
		if i == 0 {
			first = state
			continue
		}
		if !reflect.DeepEqual(first, state) {
			t.Fatalf("role %q changed the mount-state projection; the landing wrapper's role-less Spawn is no longer honest", role)
		}
	}
}

// An operator can add blocked patterns over the shipped floor, and the
// effective set is the floor plus the additions. This pins enforcement at the
// mount plan, which is where a source address becomes the bind of an agent.
func TestAttestCandidateMountsHonorsOperatorBlockedAdditions(t *testing.T) {
	root := t.TempDir()
	mcHome := maMkdir(t, root, "mc-home")
	// A name the SHIPPED floor permits: nothing in it matches "vault".
	artifact := maMkdir(t, root, "vault")
	allowlist := fmt.Sprintf(
		"version = 1\n\n[[allow]]\npath = %q\ntarget = \"art\"\naccess = \"rw\"\n", artifact)
	if err := os.WriteFile(filepath.Join(mcHome, "mount-allowlist"), []byte(allowlist), 0o600); err != nil {
		t.Fatal(err)
	}
	maStubSnapshot(t, root, "ws-blocked")
	state := PrivateDispatchMountState{SelectedProject: "ws-blocked", Projects: []PrivateDispatchProject{{
		ProjectID: "ws-blocked", Status: "active", ProfilePresent: true,
		ProfileID: "p", WorkspaceRoot: "/tmp/ws-blocked",
		ArtifactRoots: []string{artifact}, DeniedPaths: []string{},
	}}}
	candidate := func() *preparedCandidate {
		return &preparedCandidate{
			spawn: &dispatch.Spawn{Role: dispatch.RoleWorker}, mountState: state,
		}
	}

	// Baseline: the shipped floor alone admits it, so the refusal below can
	// only come from the operator's addition.
	plan, r, err := attestOrdinaryMounts(mcHome, candidate(), boundary.BlockPolicy{})
	if err != nil || r != nil || plan == nil || len(plan.Entries) != 1 {
		t.Fatalf("shipped floor alone = plan %+v refusal %+v err %v", plan, r, err)
	}

	blocked, err := boundary.NewBlockPolicy([]boundary.BlockPattern{
		{Kind: boundary.PatternComponent, Pattern: "vault"},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, r, err = attestOrdinaryMounts(mcHome, candidate(), blocked)
	if err != nil {
		t.Fatalf("operator addition errored instead of refusing: %v", err)
	}
	if r == nil || plan != nil {
		t.Fatalf("operator-blocked source still planned: plan %+v refusal %+v", plan, r)
	}
}
