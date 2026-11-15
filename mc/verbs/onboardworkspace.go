package verbs

// The host side of the Project workspace layout. Onboarding creates the
// Culler-owned root inside each registered workspace and adds its
// `info/exclude` entry to the local Git configuration of the repository.
//
// This code runs on the host as the operator and executes no Git. It measures
// the operator uid here, never inside a helper. It finds the Git
// administrative directory by reading the pointer files of Git.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"mc/boundary"
)

// projectArtifactDir is the one Culler-owned directory inside a workspace.
// The artifact root stores operational files across runs and review. Git
// stores the durable Project deliverables.
const projectArtifactDir = ".mc-artifacts"

// onboardWorkspaceRoots are the operator-owned mode-0700 directories that
// onboarding creates inside each workspace. Every consumer requires an
// unsymlinked ancestor chain for each of them.
var onboardWorkspaceRoots = []string{projectArtifactDir}

// onboardExcludeEntries are the literal `info/exclude` patterns, trailing
// slash included. A bare `.mc-artifacts` is a different and broader Git
// pattern, so the exact bytes here are what get written.
var onboardExcludeEntries = []string{projectArtifactDir + "/"}

// ProjectArtifactRoot is the single derivation of that path. The generated
// profile, the host-side creator, and the allowlist grant all read it here, so
// no two of them can spell it slightly differently and still agree with
// themselves — the failure mode that shipped an empty `artifact_roots` past
// every green suite.
func ProjectArtifactRoot(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, projectArtifactDir)
}

// maxExcludeBytes bounds the read of a file that lives in the operator's own
// `.git`. It is generous for a hand-maintained exclude list and small enough
// that a pathological file cannot be pulled into memory.
const maxExcludeBytes = 1 << 20

// ProjectToolHome is the per-Project synthetic HOME: containers run with no
// ambient home, so this path IS the operator-owned one their harnesses get.
// The generated profile, the host-side creator that proves it, and the mount
// attestation that binds it all read it here, and a Project whose profile
// carries no tool home is refused at dispatch. One derivation, so a stand-in
// for onboarding cannot restate it slightly differently and pass its own
// comparison.
func ProjectToolHome(home, project string) string {
	return filepath.Join(home, "state", "projects", project, "home")
}

// PrepareProjectHome preserves the Project home and creates absent directories.
func PrepareProjectHome(home, project string) (PrivateDispatchPathIdentity, error) {
	if !ValidProjectID(project) {
		return PrivateDispatchPathIdentity{}, Usagef("The Project identity is invalid.")
	}
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		return PrivateDispatchPathIdentity{}, err
	}
	for _, path := range []string{
		filepath.Join(canonical, "state"),
		filepath.Join(canonical, "state", "projects"),
		filepath.Join(canonical, "state", "projects", project),
		ProjectToolHome(canonical, project),
	} {
		if _, err := ensureOperatorRoot(path, os.Getuid()); err != nil {
			return PrivateDispatchPathIdentity{}, Domainf("The Project home is unavailable: %v", err)
		}
	}
	root, err := resolveDispatchProtected(ProjectToolHome(canonical, project), true)
	if err != nil {
		return PrivateDispatchPathIdentity{}, err
	}
	return hostPathIdentity(root)
}

// EnsureOnboardWorkspaceLayout creates the roots and exclude entries for every
// registered workspace root, and reports whether it changed anything.
//
// Idempotent by contract: `mc onboard` re-runs every section on every
// invocation, so the steady state is "everything already exists" and must stay
// silent. Nothing here ever removes, chmods, or rewrites what it finds — a root
// of the wrong shape is REFUSED, never repaired, because a wrong-mode or
// foreign root is a condition the operator must see, not one to paper over.
func EnsureOnboardWorkspaceLayout(roots []OnboardWorkspaceRoot, ownerUID int) (bool, string, error) {
	var notes []string
	changed := false
	for _, root := range roots {
		created, err := ensureWorkspaceRoots(root, ownerUID)
		if err != nil {
			return false, "", err
		}
		appended, err := ensureWorkspaceExclude(root)
		if err != nil {
			return false, "", err
		}
		switch {
		case len(created) > 0 && len(appended) > 0:
			notes = append(notes, fmt.Sprintf("%s: created %s, excluded %s",
				root.ID, strings.Join(created, " "), strings.Join(appended, " ")))
		case len(created) > 0:
			notes = append(notes, fmt.Sprintf("%s: created %s", root.ID, strings.Join(created, " ")))
		case len(appended) > 0:
			notes = append(notes, fmt.Sprintf("%s: excluded %s", root.ID, strings.Join(appended, " ")))
		}
		changed = changed || len(created) > 0 || len(appended) > 0
	}
	return changed, strings.Join(notes, "; "), nil
}

// PrepareOnboardProjectToolHome creates and proves the generated profile's
// per-Project synthetic HOME on the host before the profile becomes
// launchable. Each ancestor is created and proved one at a time so an existing
// symlink or foreign/widened directory refuses before a child can be written
// through it.
func PrepareOnboardProjectToolHome(a OnboardArgs, ownerUID int) (bool, error) {
	profile, err := onboardProjectProfile(a)
	if err != nil {
		return false, err
	}
	home, err := mcHomeDir()
	if err != nil {
		return false, err
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return false, Domainf("resolve canonical MC_HOME for Project synthetic home: %v", err)
	}
	home = filepath.Clean(home)
	expected := ProjectToolHome(home, a.Project)
	if profile.ToolHomeDir != expected {
		return false, Domainf("Project %s synthetic home %q is not the generated per-Project path %q",
			a.Project, profile.ToolHomeDir, expected)
	}
	changed := false
	for _, path := range []string{
		filepath.Join(home, "state"),
		filepath.Join(home, "state", "projects"),
		filepath.Join(home, "state", "projects", a.Project),
		expected,
	} {
		created, err := ensureOperatorRoot(path, ownerUID)
		if err != nil {
			return false, Domainf("Project %s cannot use synthetic home component %q: %v",
				a.Project, path, err)
		}
		changed = changed || created
	}
	return changed, nil
}

// ensureWorkspaceRoots creates the missing roots under one already-validated
// workspace root and proves every one of them, new or pre-existing.
func ensureWorkspaceRoots(root OnboardWorkspaceRoot, ownerUID int) ([]string, error) {
	// The workspace root itself is never created here. `mc onboard project`
	// must keep failing when the operator has deleted it, and an MkdirAll from
	// the workspace down would silently resurrect it instead.
	workspace, err := boundary.ResolveSource(root.Root)
	if err != nil {
		return nil, err
	}
	if workspace.Canonical != filepath.Clean(root.Root) {
		return nil, Domainf("Project %s workspace root %q is not its own canonical path; refuse to build a layout under a symlinked root",
			root.ID, root.Root)
	}
	var created []string
	for _, rel := range onboardWorkspaceRoots {
		path := filepath.Join(workspace.Canonical, rel)
		madeIt, err := ensureOperatorRoot(path, ownerUID)
		if err != nil {
			return nil, Domainf("Project %s cannot use %q: %v (a pre-existing foreign, symlinked, or wrong-mode root is refused, never repaired)",
				root.ID, path, err)
		}
		if madeIt {
			created = append(created, rel)
		}
	}
	return created, nil
}

// ensureOperatorRoot creates one mode-0700 root if it is absent, then proves it
// whether or not it just created it.
//
// The chmod after Mkdir is not redundant: the process umask strips bits from
// the mode Mkdir requests, and every consumer compares Perm() against exactly
// 0700. The prove runs unconditionally because Mkdir's ErrExist tells us
// nothing about the shape of what is already there.
func ensureOperatorRoot(path string, ownerUID int) (bool, error) {
	created := false
	switch err := os.Mkdir(path, 0o700); {
	case err == nil:
		created = true
		if err := os.Chmod(path, 0o700); err != nil {
			return false, err
		}
	case errors.Is(err, fs.ErrExist):
	default:
		return false, err
	}
	if err := proveOperatorRoot(path, ownerUID, filepath.Base(path)); err != nil {
		return false, err
	}
	return created, nil
}

// proveOperatorRoot proves one root: a non-symlink directory at its exact
// canonical path, with mode 0700, owned by the operator.
func proveOperatorRoot(path string, ownerUID int, label string) error {
	if err := boundary.TrustHomeDir(path, ownerUID); err != nil {
		return err
	}
	parent, err := boundary.ResolveSource(path)
	if err != nil {
		return err
	}
	if !parent.IsDir || parent.Canonical != path || parent.Info.Mode().Perm() != 0o700 {
		return &boundary.MountError{
			Code: boundary.CodeSourceWrongKind, Msg: label + " is not the exact canonical mode-0700 directory",
		}
	}
	st, ok := parent.Info.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != ownerUID {
		return &boundary.MountError{
			Code: boundary.CodeRuntimeUnappliable, Msg: label + " is not owned by the operator",
		}
	}
	return nil
}

// ensureWorkspaceExclude appends the missing literals to the repository's
// `info/exclude` and returns what it appended. A Project is a Git
// repository, so a root without an administrative directory is a refusal.
// The exclude entry is a courtesy to the operator's `git status`, not a
// fence.
func ensureWorkspaceExclude(root OnboardWorkspaceRoot) ([]string, error) {
	admin, err := resolveGitAdminDir(root.Root)
	if err != nil {
		return nil, err
	}
	// `resolveGitAdminDir` follows `commondir`, so a linked worktree resolves to
	// the SHARED control directory. That is deliberate and load-bearing:
	// info/exclude is read from $GIT_COMMON_DIR, and writing the per-worktree
	// gitdir instead would produce a file Git never consults.
	infoDir := filepath.Join(admin, "info")
	// 0755/0644 are Git's own conventions for its administrative files. The
	// 0700 discipline above governs MC's roots; tightening permissions inside
	// the operator's `.git` would be an unrequested change to their repository.
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		return nil, err
	}
	excludePath := filepath.Join(infoDir, "exclude")
	body, err := readBoundedFile(excludePath, maxExcludeBytes)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, entry := range onboardExcludeEntries {
		if !hasExcludeLine(body, entry) {
			missing = append(missing, entry)
		}
	}
	if len(missing) == 0 {
		return nil, nil
	}
	// Append only, never temp-and-rename. This file is the operator's, it may
	// carry their own rules, and a rename would replace the inode — dropping
	// their mode and ownership, breaking hardlinks, and racing any concurrent
	// edit. A leading newline repairs a body with no trailing one, which would
	// otherwise silently FUSE our first literal onto their last pattern and
	// destroy both.
	var out strings.Builder
	if len(body) > 0 && !strings.HasSuffix(string(body), "\n") {
		out.WriteString("\n")
	}
	for _, entry := range missing {
		out.WriteString(entry)
		out.WriteString("\n")
	}
	file, err := os.OpenFile(excludePath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := file.WriteString(out.String()); err != nil {
		file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return missing, nil
}

// hasExcludeLine reports whether the pattern is already present as its own
// entry. Matching whole lines rather than substrings keeps `.mc-artifacts/`
// from being considered present because some unrelated rule mentions it, and
// keeps a commented-out `#.mc-artifacts/` from counting as active.
func hasExcludeLine(body []byte, entry string) bool {
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == entry {
			return true
		}
	}
	return false
}

// readBoundedFile reads at most limit bytes, treating absence as empty.
func readBoundedFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return nil, err
	}
	return body, nil
}
