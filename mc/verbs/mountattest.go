package verbs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"

	"mc/boundary"
	"mc/deployment"
	"mc/dispatch"
	"mc/refusal"
)

// dispatchMountHostSnapshot is the host-only half of the jurisdiction input.
// Project/profile text crosses in the token-bound candidate; actual
// filesystem identities and account/deployment roots never cross into the
// helper and are assembled during attest.
type dispatchMountHostSnapshot struct {
	OperatorHome    string
	OwnerUID        int
	MCHome          boundary.ProtectedID
	HomeClassRoots  []boundary.ProtectedID
	GatewaySecrets  []boundary.ProtectedID
	ProjectRoots    map[string]boundary.ProjectRoots
	GitControls     map[string][]boundary.ProtectedID
	ResolveDeclared func(string) (boundary.ProtectedID, error)
}

type dispatchMountAssembly struct {
	Requests     []mountRequest
	Jurisdiction boundary.JurisdictionInput
}

type jurisdictionDigestMember struct {
	Anchor   string   `json:"anchor"`
	Class    string   `json:"class"`
	Declared string   `json:"declared"`
	Device   string   `json:"device"`
	Inode    string   `json:"inode"`
	Kind     string   `json:"kind"`
	Present  bool     `json:"present"`
	Suffix   []string `json:"suffix"`
}

// jurisdictionInputDigest keeps the distinction between rerunning the
// predicate and rerunning the input. A protected-root identity change must
// stale even when every requested source and final verdict remain unchanged,
// so the authorized plan binds this canonical non-secret snapshot in addition
// to its mount entries.
func jurisdictionInputDigest(in boundary.JurisdictionInput, ownerUID int) (string, error) {
	projection := struct {
		DeniedPaths []string                   `json:"denied_paths"`
		Home        string                     `json:"home"`
		Members     []jurisdictionDigestMember `json:"members"`
		OwnerUID    int                        `json:"owner_uid"`
	}{DeniedPaths: append([]string(nil), in.DeniedPaths...), Home: in.Home, OwnerUID: ownerUID}
	sort.Strings(projection.DeniedPaths)
	add := func(class string, id boundary.ProtectedID) error {
		effective, err := boundary.ResolveProtectedEvidence(id)
		if err != nil {
			return err
		}
		anchor := effective.Anchor
		member := jurisdictionDigestMember{
			Anchor: anchor.Canonical, Class: class, Declared: effective.Declared,
			Present: anchor.Present(), Suffix: append([]string(nil), effective.Suffix...),
		}
		if anchor.Present() {
			st, ok := anchor.Info.Sys().(*syscall.Stat_t)
			if !ok {
				return Domainf("protected root %q has no native identity evidence", id.Canonical)
			}
			member.Device = strconv.FormatUint(uint64(st.Dev), 10)
			member.Inode = strconv.FormatUint(st.Ino, 10)
			if anchor.IsDir {
				member.Kind = "dir"
			} else {
				member.Kind = "file"
			}
		}
		projection.Members = append(projection.Members, member)
		return nil
	}
	for i, path := range projection.DeniedPaths {
		if err := add("denied."+strconv.Itoa(i), boundary.ProtectedID{Canonical: path}); err != nil {
			var mountErr *boundary.MountError
			if errors.As(err, &mountErr) {
				candidateErr := *mountErr
				candidateErr.CandidateAuthored = true
				return "", &candidateErr
			}
			return "", err
		}
	}
	addRoots := func(prefix string, roots boundary.ProjectRoots) error {
		for class, id := range map[string]boundary.ProtectedID{
			"workspace": roots.Workspace, "worktree": roots.Worktree,
			"state": roots.State, "cache": roots.Cache, "tool_home": roots.ToolHome,
		} {
			if err := add(prefix+"."+class, id); err != nil {
				return err
			}
		}
		for i, id := range roots.Artifacts {
			if err := add(prefix+".artifact."+strconv.Itoa(i), id); err != nil {
				return err
			}
		}
		return nil
	}
	if err := add("mc_home", in.MCHome); err != nil {
		return "", err
	}
	if err := addRoots("own", in.OwnProject); err != nil {
		return "", err
	}
	for i, roots := range in.OtherProjects {
		if err := addRoots("other."+strconv.Itoa(i), roots); err != nil {
			return "", err
		}
	}
	groups := []struct {
		name string
		ids  []boundary.ProtectedID
	}{
		{"home_class", in.HomeClassRoots}, {"gateway", in.GatewaySecrets},
		{"runtime_control", in.RuntimeControls}, {"own_git", in.OwnGitControls},
		{"other_git", in.OtherGitControls},
	}
	for _, group := range groups {
		for i, id := range group.ids {
			if err := add(group.name+"."+strconv.Itoa(i), id); err != nil {
				return "", err
			}
		}
	}
	sort.Slice(projection.Members, func(i, j int) bool {
		a, b := projection.Members[i], projection.Members[j]
		if a.Class != b.Class {
			return a.Class < b.Class
		}
		if a.Declared != b.Declared {
			return a.Declared < b.Declared
		}
		return a.Anchor < b.Anchor
	})
	body, err := json.Marshal(projection)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("MC-JURISDICTION-SNAPSHOT-V1\x00"), body...))
	return hex.EncodeToString(sum[:]), nil
}

func selectedDispatchProject(state PrivateDispatchMountState) (PrivateDispatchProject, error) {
	if state.SelectedProject == "" {
		return PrivateDispatchProject{}, Domainf("dispatch: a filesystem-bearing candidate has no selected Project")
	}
	for _, ws := range state.Projects {
		if ws.ProjectID == state.SelectedProject {
			return ws, nil
		}
	}
	return PrivateDispatchProject{}, Domainf("dispatch: selected Project is absent from the mount projection")
}

func selectedProfileMountRequests(state PrivateDispatchMountState) ([]mountRequest, PrivateDispatchProject, *refusal.Refusal, error) {
	selected, err := selectedDispatchProject(state)
	if err != nil {
		return nil, selected, nil, err
	}
	if !selected.ProfilePresent {
		// An absent profile is deployment configuration the candidate never
		// authored: health, not a per-task confinement block.
		r, err := refusalForMountError(&boundary.MountError{
			Code: boundary.CodeRuntimeUnappliable, Msg: "selected Project has no sandbox profile",
		}, refusal.AuthorityDeployment, nil)
		return nil, selected, &r, err
	}
	requests := make([]mountRequest, 0, len(selected.ArtifactRoots))
	for _, source := range selected.ArtifactRoots {
		requests = append(requests, mountRequest{Source: source, Access: boundary.AccessRW, Authority: refusal.AuthorityCandidate, Class: classArtifact})
	}
	return requests, selected, nil, nil
}

// seedingProjectionFence is the health check ahead of Strategist's
// committed-projection step: the run's Project must carry a complete
// profile and an id usable as the one seeding destination component.
// The projection step
// (captureSeedingProjectionStep) resolves after this fence and relies on it
// for the id shape of its /workspace/seeding/<id>/source destination. Only
// the run's Project is examined: another Project's profile is not this
// run's concern.
func seedingProjectionFence(selected PrivateDispatchProject) (*refusal.Refusal, error) {
	if !selected.ProfilePresent || selected.WorkspaceRoot == "" {
		r, err := refusalForMountError(&boundary.MountError{
			Code: boundary.CodeRuntimeUnappliable,
			Msg:  "the run's Project has no complete sandbox profile for the seeding projection",
		}, refusal.AuthorityDeployment, nil)
		return &r, err
	}
	if err := boundary.ValidateTarget(selected.ProjectID); err != nil || filepath.Base(selected.ProjectID) != selected.ProjectID {
		r, adaptErr := refusalForMountError(&boundary.MountError{
			Code: boundary.CodeTargetInvalid,
			Msg:  "the run's Project id cannot form one seeding destination component",
		}, refusal.AuthorityDeployment, nil)
		return &r, adaptErr
	}
	return nil, nil
}

// hasSeedingProjection reports whether the candidate carries the one
// committed projection: Strategist over its Project.
func hasSeedingProjection(selected PrivateDispatchProject, role string) bool {
	return seedingProjectionRole(role) && selected.ProjectID != ""
}

// seedingProjectionRole identifies a role that reads the committed tree of its
// Project: the role that proposes work and the role that selects work.
func seedingProjectionRole(role string) bool {
	return role == string(dispatch.RoleStrategist) || baseRole(role) == "arbiter"
}

// captureSeedingProjectionStep authors Strategist's one committed
// projection: the culler branch of the run's Project at
// /workspace/seeding/<id>/source, whatever the operator has checked out. Its
// mount plan is the sandbox profile of that Project and of no other. The
// plan carries at most one entry.
func captureSeedingProjectionStep(selected PrivateDispatchProject, snapshot dispatchMountHostSnapshot, role string) ([]PrivateDispatchSeedingProjection, error) {
	if !hasSeedingProjection(selected, role) {
		return nil, nil
	}
	roots, ok := snapshot.ProjectRoots[selected.ProjectID]
	if !ok {
		return nil, Domainf("dispatch: host snapshot omitted Git Project %q roots", selected.ProjectID)
	}
	identity, err := hostPathIdentity(roots.Workspace)
	if err != nil {
		return nil, err
	}
	if identity.OwnerUID != snapshot.OwnerUID {
		return nil, &boundary.MountError{Code: boundary.CodeRuntimeUnappliable, Msg: "Git seeding Project root is not operator-owned"}
	}
	format, err := probeRepoObjectFormat(selected.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	return []PrivateDispatchSeedingProjection{{
		GitCoverKind: seedingSourceGitCoverKind, ObjectFormat: format,
		ProjectionKind: seedingCommittedProjectionKind, TargetRef: "refs/heads/culler",
		WorkspaceRoot: identity, ProjectID: selected.ProjectID,
	}}, nil
}

// deriveDispatchMountRequests is the single derivation point for the ordinary
// mount requests of a candidate: the artifact RW sources of the selected
// profile. The isolated repository of a subject run is a separate plan.
func deriveDispatchMountRequests(state PrivateDispatchMountState) ([]mountRequest, PrivateDispatchProject, *refusal.Refusal, error) {
	if state.SelectedProject == "" {
		// Every pipeline run names its Project, the subjectless roles
		// included; a candidate without one is a protocol defect, never an
		// empty plan.
		return nil, PrivateDispatchProject{}, nil, Domainf("dispatch: candidate names no Project")
	}
	requests, selected, r, err := selectedProfileMountRequests(state)
	if wikiMountState(state) {
		requests = nil
	}
	return requests, selected, r, err
}

// assembleDispatchMountInputs associates the frozen all-Project projection
// with independently resolved host identities. Request derivation lives in
// deriveDispatchMountRequests; this function owns only the jurisdiction
// assembly for an already-derived request set.
func assembleDispatchMountInputs(snapshot dispatchMountHostSnapshot, state PrivateDispatchMountState, requests []mountRequest, selected PrivateDispatchProject) (dispatchMountAssembly, error) {
	if selected.ProjectID == "" {
		in := boundary.JurisdictionInput{
			Home: snapshot.OperatorHome, MCHome: snapshot.MCHome,
			HomeClassRoots:  append([]boundary.ProtectedID(nil), snapshot.HomeClassRoots...),
			GatewaySecrets:  append([]boundary.ProtectedID(nil), snapshot.GatewaySecrets...),
			RuntimeControls: []boundary.ProtectedID{}, OtherProjects: []boundary.ProjectRoots{},
			OtherGitControls: []boundary.ProtectedID{},
		}
		for _, ws := range state.Projects {
			roots, ok := snapshot.ProjectRoots[ws.ProjectID]
			if !ok {
				return dispatchMountAssembly{}, Domainf("dispatch: host snapshot omitted Project %q roots", ws.ProjectID)
			}
			in.OtherProjects = append(in.OtherProjects, roots)
			in.OtherGitControls = append(in.OtherGitControls, snapshot.GitControls[ws.ProjectID]...)
			if ws.RuntimeControlDir != "" {
				id, err := snapshot.ResolveDeclared(ws.RuntimeControlDir)
				if err != nil {
					return dispatchMountAssembly{}, err
				}
				in.RuntimeControls = append(in.RuntimeControls, id)
			}
		}
		return dispatchMountAssembly{Requests: requests, Jurisdiction: in}, nil
	}
	if snapshot.ResolveDeclared == nil {
		return dispatchMountAssembly{}, Domainf("dispatch: host mount snapshot has no protected-path resolver")
	}
	own, ok := snapshot.ProjectRoots[selected.ProjectID]
	if !ok {
		return dispatchMountAssembly{}, Domainf("dispatch: host snapshot omitted selected Project roots")
	}

	in := boundary.JurisdictionInput{
		DeniedPaths:      append([]string(nil), selected.DeniedPaths...),
		Home:             snapshot.OperatorHome,
		MCHome:           snapshot.MCHome,
		HomeClassRoots:   append([]boundary.ProtectedID(nil), snapshot.HomeClassRoots...),
		GatewaySecrets:   append([]boundary.ProtectedID(nil), snapshot.GatewaySecrets...),
		RuntimeControls:  []boundary.ProtectedID{},
		OwnProject:       own,
		OtherProjects:    []boundary.ProjectRoots{},
		OwnGitControls:   append([]boundary.ProtectedID(nil), snapshot.GitControls[selected.ProjectID]...),
		OtherGitControls: []boundary.ProtectedID{},
	}
	for _, ws := range state.Projects {
		roots, ok := snapshot.ProjectRoots[ws.ProjectID]
		if !ok {
			return dispatchMountAssembly{}, Domainf("dispatch: host snapshot omitted Project %q roots", ws.ProjectID)
		}
		if ws.RuntimeControlDir != "" {
			id, err := snapshot.ResolveDeclared(ws.RuntimeControlDir)
			if err != nil {
				return dispatchMountAssembly{}, err
			}
			in.RuntimeControls = append(in.RuntimeControls, id)
		}
		if ws.ProjectID == selected.ProjectID {
			continue
		}
		in.OtherProjects = append(in.OtherProjects, roots)
		in.OtherGitControls = append(in.OtherGitControls, snapshot.GitControls[ws.ProjectID]...)
	}
	return dispatchMountAssembly{Requests: requests, Jurisdiction: in}, nil
}

func resolveDispatchProtected(path string, absentAllowed bool) (boundary.ProtectedID, error) {
	if path == "" {
		return boundary.ProtectedID{}, nil
	}
	id, err := boundary.ResolveSource(path)
	if err == nil {
		return boundary.ProtectedID{Canonical: id.Canonical, Info: id.Info, IsDir: id.IsDir}, nil
	}
	var me *boundary.MountError
	if absentAllowed && errors.As(err, &me) && me.Code == boundary.CodeSourceMissing && filepath.IsAbs(path) {
		return boundary.ProtectedID{Canonical: filepath.Clean(path)}, nil
	}
	return boundary.ProtectedID{}, err
}

func realOperatorHome(uid int) (string, error) {
	account, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		return "", err
	}
	if account.HomeDir == "" || !filepath.IsAbs(account.HomeDir) {
		return "", Domainf("dispatch: operator account database returned no absolute home")
	}
	return filepath.Clean(account.HomeDir), nil
}

const refreshGrantStoreDir = "refresh-grants"

// Absent source directories stay protected before a native sign-in creates them.
func resolveGatewaySecretRoots(home string) ([]boundary.ProtectedID, error) {
	paths := []string{filepath.Join(home, refreshGrantStoreDir), deployment.NativeCredentialSourceRoot()}
	roots := make([]boundary.ProtectedID, 0, len(paths))
	for _, path := range paths {
		id, err := resolveDispatchProtected(path, true)
		if err != nil {
			return nil, err
		}
		roots = append(roots, id)
	}
	return roots, nil
}

func captureDispatchMountHostSnapshot(home string, state PrivateDispatchMountState) (dispatchMountHostSnapshot, error) {
	uid := os.Getuid()
	if err := boundary.TrustHomeDir(home, uid); err != nil {
		return dispatchMountHostSnapshot{}, err
	}
	operatorHome, err := realOperatorHome(uid)
	if err != nil {
		return dispatchMountHostSnapshot{}, err
	}
	mcHome, err := resolveDispatchProtected(home, false)
	if err != nil {
		return dispatchMountHostSnapshot{}, err
	}
	gatewaySecrets, err := resolveGatewaySecretRoots(home)
	if err != nil {
		return dispatchMountHostSnapshot{}, err
	}
	snapshot := dispatchMountHostSnapshot{
		OperatorHome: operatorHome, OwnerUID: uid, MCHome: mcHome,
		HomeClassRoots: []boundary.ProtectedID{}, GatewaySecrets: gatewaySecrets,
		ProjectRoots: map[string]boundary.ProjectRoots{},
		GitControls:  map[string][]boundary.ProtectedID{},
		ResolveDeclared: func(path string) (boundary.ProtectedID, error) {
			return resolveDispatchProtected(path, true)
		},
	}
	for _, rel := range []string{
		".ssh", ".aws", ".azure", ".config", ".docker", ".gnupg", ".kube", ".codex", ".claude",
		"Library/Keychains", ".netrc", ".npmrc", ".pypirc", ".git-credentials",
	} {
		path := filepath.Join(operatorHome, filepath.FromSlash(rel))
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return dispatchMountHostSnapshot{}, err
		}
		id, err := resolveDispatchProtected(path, false)
		if err != nil {
			return dispatchMountHostSnapshot{}, err
		}
		snapshot.HomeClassRoots = append(snapshot.HomeClassRoots, id)
	}
	for _, ws := range state.Projects {
		workspace, err := resolveDispatchProtected(ws.WorkspaceRoot, true)
		if err != nil {
			return dispatchMountHostSnapshot{}, err
		}
		artifacts := make([]boundary.ProtectedID, 0, len(ws.ArtifactRoots))
		for _, path := range ws.ArtifactRoots {
			id, err := resolveDispatchProtected(path, true)
			if err != nil {
				return dispatchMountHostSnapshot{}, err
			}
			artifacts = append(artifacts, id)
		}
		stateHome := ProjectToolHome(mcHome.Canonical, ws.ProjectID)
		if ws.ProjectID == state.SelectedProject && ws.ToolHomeDir == stateHome {
			if _, err := PrepareProjectHome(mcHome.Canonical, ws.ProjectID); err != nil {
				return dispatchMountHostSnapshot{}, err
			}
		}
		toolHome, err := resolveDispatchProtected(ws.ToolHomeDir, true)
		if err != nil {
			return dispatchMountHostSnapshot{}, err
		}
		snapshot.ProjectRoots[ws.ProjectID] = boundary.ProjectRoots{
			Workspace: workspace,
			Artifacts: artifacts,
			State:     mustDispatchProtected(stateHome),
			Cache:     mustDispatchProtected(filepath.Join(stateHome, ".cache")),
			ToolHome:  toolHome,
		}
		// The Git control registry resolves live per attest.
		controls, err := resolveProjectGitControls(ws.WorkspaceRoot)
		if err != nil {
			return dispatchMountHostSnapshot{}, err
		}
		snapshot.GitControls[ws.ProjectID] = controls
	}
	return snapshot, nil
}

var captureDispatchMountSnapshot = captureDispatchMountHostSnapshot

func mustDispatchProtected(path string) boundary.ProtectedID {
	if path == "" || !filepath.IsAbs(path) {
		return boundary.ProtectedID{}
	}
	return boundary.ProtectedID{Canonical: filepath.Clean(path)}
}

// maxDispatchMountPlanBytes bounds the serialized carrier at attest, BEFORE
// any claim: the committed spawn effect embeds the plan and must survive the
// broker's 64 KiB canonical-result cap, so an oversized plan is a pre-commit
// deployment-health refusal, never a post-commit wedge.
const maxDispatchMountPlanBytes = 32 * 1024

// attestCandidateMounts is the host-authority mount leg of dispatchAttest: it
// derives the ordinary requests of the candidate, assembles jurisdiction from
// independently resolved host identities, authorizes the whole set, and
// returns the bounded evidence-backed plan carrier, or exactly one classified
// refusal. A code role with a subject also carries the plan of its isolated
// repository. An empty plan (empty profile) is explicit, never absent.
func attestCandidateMounts(home string, cand *preparedCandidate, blocked boundary.BlockPolicy) (*PrivateDispatchMountPlan, *refusal.Refusal, error) {
	role := string(cand.spawn.Role)
	plan, r, err := attestOrdinaryMounts(home, cand, blocked)
	if err != nil || r != nil {
		return nil, r, err
	}
	plan.Repository = expectedRepositoryPlan(cand.mountState, role)
	return plan, nil, nil
}

// attestOrdinaryMounts attests the artifact mounts, the seeding projection,
// and the synthetic home of the candidate.
func attestOrdinaryMounts(home string, cand *preparedCandidate, blocked boundary.BlockPolicy) (*PrivateDispatchMountPlan, *refusal.Refusal, error) {
	seedingRole := string(cand.spawn.Role)
	empty := &PrivateDispatchMountPlan{Version: 1, Entries: []PrivateDispatchMountEntry{}}
	requests, selected, r, err := deriveDispatchMountRequests(cand.mountState)
	if err != nil || r != nil {
		return nil, r, err
	}
	hasGitSeeding := hasSeedingProjection(selected, seedingRole)
	if seedingProjectionRole(seedingRole) {
		if r, err := seedingProjectionFence(selected); err != nil || r != nil {
			return nil, r, err
		}
	}
	// The env planes refuse before the empty-plan early return below: a
	// profile whose only content is a forbidden or malformed env policy must
	// still block before claim.
	if selected.ProjectID != "" && (selected.HarnessEnvPolicy != "" || selected.ToolEnvPolicy != "") && cand.binding != "" {
		if r := attestCandidateEnvPolicy(
			selected, cand.bindingSpec, cand.bindingCatalog, cand.forbiddenEnv,
		); r != nil {
			return nil, r, nil
		}
	} else if selected.ProjectID != "" && (selected.HarnessEnvPolicy != "" || selected.ToolEnvPolicy != "") && cand.binding == "" {
		return nil, &refusal.Refusal{Code: refusal.CodeAuthBindingInvalid, Field: refusal.FieldNone, Summary: refusal.SummaryUnparsable}, nil
	}
	// A truly empty ordinary policy has nothing to authorize. A nonempty
	// denied_paths set still constructs below even with zero requests: malformed
	// candidate policy must refuse before claim, not hide behind an empty plan.
	if len(requests) == 0 && !hasGitSeeding && len(selected.DeniedPaths) == 0 &&
		selected.ToolHomeDir == "" {
		return empty, nil, nil
	}
	snapshot, err := captureDispatchMountSnapshot(home, cand.mountState)
	if err != nil {
		r, aerr := adaptMountError(err, refusal.AuthorityDeployment, nil)
		return nil, r, aerr
	}
	assembled, err := assembleDispatchMountInputs(snapshot, cand.mountState, requests, selected)
	if err != nil {
		// A boundary rejection during assembly (a declared runtime-control
		// path failing resolution) is deployment configuration: health, not a
		// dispatch protocol error (takeover-review fix, 2026-07-16). Non-mount
		// errors stay protocol errors.
		r, aerr := adaptMountError(err, refusal.AuthorityDeployment, nil)
		return nil, r, aerr
	}
	j, err := boundary.ResolveJurisdiction(assembled.Jurisdiction, snapshot.OwnerUID)
	if err != nil {
		authority := refusal.AuthorityDeployment
		var me *boundary.MountError
		if errors.As(err, &me) && me.CandidateAuthored {
			authority = refusal.AuthorityCandidate
		}
		r, aerr := adaptMountError(err, authority, nil)
		return nil, r, aerr
	}
	jurisdictionDigest, err := jurisdictionInputDigest(assembled.Jurisdiction, snapshot.OwnerUID)
	if err != nil {
		authority := refusal.AuthorityDeployment
		var mountErr *boundary.MountError
		if errors.As(err, &mountErr) && mountErr.CandidateAuthored {
			authority = refusal.AuthorityCandidate
		}
		r, aerr := adaptMountError(err, authority, nil)
		return nil, r, aerr
	}
	entries, r, err := planMounts(assembled.Requests, mountPlanInputs{
		AllowlistPath: MountAllowlistPath(home), OwnerUID: snapshot.OwnerUID,
		Blocked: blocked, Jurisdiction: j,
	})
	if err != nil || r != nil {
		return nil, r, err
	}
	if entries == nil {
		entries = []PrivateDispatchMountEntry{}
	}
	plan := &PrivateDispatchMountPlan{
		Version: 1, Entries: entries, JurisdictionDigest: jurisdictionDigest,
	}
	plan.SeedingProjections, err = captureSeedingProjectionStep(selected, snapshot, seedingRole)
	if err != nil {
		r, aerr := adaptMountError(err, refusal.AuthorityDeployment, nil)
		return nil, r, aerr
	}
	if selected.ToolHomeDir != "" {
		roots, ok := snapshot.ProjectRoots[selected.ProjectID]
		if !ok {
			return nil, nil, Domainf("dispatch: host snapshot omitted selected Project roots")
		}
		if roots.ToolHome.Canonical != selected.ToolHomeDir ||
			roots.ToolHome.Info == nil || !roots.ToolHome.IsDir ||
			roots.ToolHome.Info.Mode().Perm() != 0o700 {
			r, err := refusalForMountError(&boundary.MountError{
				Code: boundary.CodeRuntimeUnappliable,
				Msg:  "the selected Project synthetic home is absent or not an owner-only directory",
			}, refusal.AuthorityDeployment, nil)
			return nil, &r, err
		}
		identity, err := hostPathIdentity(roots.ToolHome)
		if err != nil {
			return nil, nil, err
		}
		if identity.OwnerUID != snapshot.OwnerUID {
			r, err := refusalForMountError(&boundary.MountError{
				Code: boundary.CodeRuntimeUnappliable,
				Msg:  "the selected Project synthetic home is not operator-owned",
			}, refusal.AuthorityDeployment, nil)
			return nil, &r, err
		}
		plan.ToolHome = &identity
	}
	body, err := json.Marshal(plan)
	if err != nil {
		return nil, nil, err
	}
	if len(body) > maxDispatchMountPlanBytes {
		r, err := refusalForMountError(&boundary.MountError{
			Code: boundary.CodeRuntimeUnappliable,
			Msg:  "the authorized mount plan exceeds its effect byte budget",
		}, refusal.AuthorityDeployment, nil)
		return nil, &r, err
	}
	return plan, nil, nil
}

// hostPathIdentity projects a resolved protected root onto the private
// path-identity carrier.
//
// The type assertion is CHECKED, unlike the inline conversions elsewhere in
// this file: this runs on a path the resident later rechecks against, so a
// platform where Sys() is not a *syscall.Stat_t must refuse rather than panic
// mid-attestation or, worse, carry a zero device/inode that would compare
// equal to an unpopulated field.
func hostPathIdentity(id boundary.ProtectedID) (PrivateDispatchPathIdentity, error) {
	if id.Info == nil {
		return PrivateDispatchPathIdentity{}, Domainf("host anchor %q carries no stat evidence", id.Canonical)
	}
	st, ok := id.Info.Sys().(*syscall.Stat_t)
	if !ok {
		return PrivateDispatchPathIdentity{}, Domainf("host anchor %q exposes no host identity", id.Canonical)
	}
	return PrivateDispatchPathIdentity{
		Canonical: id.Canonical,
		Device:    strconv.FormatUint(uint64(st.Dev), 10),
		Inode:     strconv.FormatUint(st.Ino, 10),
		OwnerUID:  int(st.Uid),
	}, nil
}
