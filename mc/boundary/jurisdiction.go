package boundary

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// errNoMountPoint reports that this platform exposes no per-path mount point.
// It is part of mountPoint's contract, so it lives with the caller rather than
// in one platform arm: ancestorRoutes must distinguish "this platform has no
// firmlink plane, so the Dir chain is the whole answer" from "statfs failed, so
// HOME's alias routes are unknown and ambiguity denies".
var errNoMountPoint = errors.New("mount point lookup is not supported on this platform")

// maxDeniedPaths bounds the denied_paths input. An excess rejects before any
// identity walk or allocation. The collection is never truncated.
const maxDeniedPaths = 512

// ProtectedID is one member of the protected set, resolved by the host caller.
//
// It carries the canonical path and the fs.FileInfo. Identity comparison uses
// os.SameFile, which consumes stat objects. The canonical path serves the
// absent-member suffix comparison and every rejection message.
//
// An absent member is encoded {Canonical: <declared cleaned path>, Info: nil}.
// Absence is the normal case: most MC_HOME classes and another Project's
// artifact root often do not exist at check time.
type ProtectedID struct {
	Canonical string
	Info      fs.FileInfo
	IsDir     bool
}

// Present reports whether this value carries a resolved filesystem identity.
// An absent input is still a member: ResolveJurisdiction promotes it when the
// full path appeared, or enriches it with a canonical existing anchor and the
// original unresolved suffix. That pair can reject a source in either direction
// while the member's Info remains nil.
func (p ProtectedID) Present() bool { return p.Info != nil }

// ProtectedEvidence is the effective identity for one declared protected
// member. Anchor is the full present member or its nearest existing canonical
// ancestor; Suffix is the unresolved component sequence below that anchor.
// Callers that bind a jurisdiction snapshot use this shape so an absent
// member's protection cannot silently follow a replaced ancestor inode.
type ProtectedEvidence struct {
	Declared string
	Anchor   ProtectedID
	Suffix   []string
}

// ResolveProtectedEvidence exposes the same absent-member resolution used by
// ResolveJurisdiction without exposing its comparison-only absentRoot type.
// It performs no authorization and grants nothing; it is a canonical evidence
// projection for callers that must bind or recheck the jurisdiction input.
func ResolveProtectedEvidence(id ProtectedID) (ProtectedEvidence, error) {
	resolved, absent, err := resolveProtectedIDWith(id, liveProtectedPathOps)
	if err != nil {
		return ProtectedEvidence{}, err
	}
	evidence := ProtectedEvidence{Declared: id.Canonical, Anchor: resolved}
	if absent != nil {
		evidence.Anchor = absent.anchor
		evidence.Suffix = append([]string(nil), absent.suffix...)
	}
	return evidence, nil
}

// ProjectRoots is one Project's six root classes.
// Any of them can be absent; see ProtectedID.
type ProjectRoots struct {
	Workspace ProtectedID
	Worktree  ProtectedID
	Artifacts []ProtectedID
	State     ProtectedID
	Cache     ProtectedID
	ToolHome  ProtectedID
}

// JurisdictionInput is what the host caller assembles for ResolveJurisdiction.
//
// Two members arrive raw, for two different reasons:
//
//   - DeniedPaths is raw because a deny path can be absent, and no caller can
//     pre-resolve an absent path.
//   - Home is raw because its raw spelling is the evidence. resolveHome must
//     refuse a symlinked HOME, and EvalSymlinks removes the only proof that it
//     was one.
//
// Every other member arrives pre-resolved. That keeps this package pure: it
// never queries the spine, the Git registry, or the environment.
type JurisdictionInput struct {
	// Raw: resolved by ResolveJurisdiction, never by the caller.
	DeniedPaths []string
	Home        string

	// Pre-resolved by the host caller. Any of these except HomeClassRoots can
	// be absent; encode absence per ProtectedID.
	MCHome ProtectedID // the whole tree; subsumes the classes and sessions

	// HomeClassRoots is the one "when present" collection: an absent ~/.aws is
	// not a member and is omitted, never encoded with a nil Info.
	HomeClassRoots []ProtectedID

	GatewaySecrets  []ProtectedID
	RuntimeControls []ProtectedID // every runtime control dir, not only non-selected
	OwnProject      ProjectRoots
	OtherProjects   []ProjectRoots
	// Control roots are ownership-associated explicitly. A flat list cannot tell
	// an own external Git dir from another Project's control nested beneath
	// the own workspace, and guessing would turn the own-control ancestor
	// exemption into a cross-Project leak.
	OwnGitControls   []ProtectedID
	OtherGitControls []ProtectedID
}

// absentRoot is the comparison pair for a declared member with no inode of its
// own. anchor is the nearest existing original prefix after one canonicalization;
// suffix preserves the unresolved components from the cleaned original spelling.
type absentRoot struct {
	anchor ProtectedID
	suffix []string
	mode   suffixCaseMode
}

// protectedPathOps is the narrow filesystem seam behind absent-member
// resolution. It makes the distinction between ENOENT and every ambiguity
// deterministic in tests without making filesystem operations injectable
// across the package.
type protectedPathOps struct {
	lstat        func(string) (fs.FileInfo, error)
	evalSymlinks func(string) (string, error)
	stat         func(string) (fs.FileInfo, error)
	caseMode     func(string) (suffixCaseMode, error)
}

var liveProtectedPathOps = protectedPathOps{
	lstat:        os.Lstat,
	evalSymlinks: filepath.EvalSymlinks,
	stat:         os.Stat,
	caseMode:     suffixCaseModeForPath,
}

// protectedRoot is one resolved union member plus the code it reports.
type protectedRoot struct {
	id                        ProtectedID
	absent                    *absentRoot
	cross                     bool   // another Project's root -> mount.cross_project
	allowOwnWorkspaceAncestor bool   // own control root's one directional exemption
	label                     string // for the operator-facing message
}

// Jurisdiction is the non-subtractable protected set.
//
// NOTE: unrelated to the free-text `jurisdiction` Project column in
// mc/verbs/project.go. Different package, no compile conflict, same word.
//
// Non-subtractability is structural: ResolveJurisdiction is the only
// constructor, every field is unexported, and there is no setter, no negation
// form, and no config key. denied_paths is purely additive on top; the operator
// can add jurisdiction, never subtract it.
type Jurisdiction struct {
	// resolved makes the zero value fail closed. BlockPolicy gets the same
	// guarantee by compiling its floor into a package-private array;
	// Jurisdiction cannot, because its members are injected. So the mechanism
	// is an explicit flag.
	resolved bool

	home       ProtectedID
	broadRoots []ProtectedID // HOME's chain union its alias routes
	// Only this exact identity can earn the own-control ancestor exemption.
	// Artifacts/state/cache/tool-home are own roots too, but have no entitlement
	// to contain Git controls.
	ownWorkspace ProtectedID
	roots        []protectedRoot
}

// ResolveJurisdiction is the only constructor.
//
// ownerUID is an explicit parameter rather than a field: it mirrors the
// TrustPolicyFile and TrustHomeDir signatures, the compiler finds every call
// site, and it cannot be silently zero. This package refuses ambient identity:
// it never calls os.Getuid(), and it never reads $HOME.
func ResolveJurisdiction(in JurisdictionInput, ownerUID int) (Jurisdiction, error) {
	// The bound comes first, before any stat, any identity walk, and any
	// allocation, including before HOME is resolved. An excess rejects; the
	// collection is never truncated.
	if len(in.DeniedPaths) > maxDeniedPaths {
		return Jurisdiction{}, &MountError{Code: CodeDeniedRoot,
			Msg:               fmt.Sprintf("profile denied_paths: %d entries exceeds the %d limit", len(in.DeniedPaths), maxDeniedPaths),
			CandidateAuthored: true}
	}

	j := Jurisdiction{ownWorkspace: in.OwnProject.Workspace}

	home, err := resolveHome(in.Home, ownerUID)
	if err != nil {
		return Jurisdiction{}, err
	}
	j.home = home

	broad, err := resolveBroadRoots(home)
	if err != nil {
		return Jurisdiction{}, err
	}
	j.broadRoots = broad

	// The whole MC_HOME tree is one protected root. It subsumes every class
	// under MC_HOME and MC_HOME/sessions, so none of them is registered
	// separately.
	if err := j.addRoot(in.MCHome, false, "MC_HOME"); err != nil {
		return Jurisdiction{}, err
	}

	for _, id := range in.OwnGitControls {
		if err := j.addOwnControlRoot(id, "own registered Git control dir"); err != nil {
			return Jurisdiction{}, err
		}
	}
	for _, id := range in.OtherGitControls {
		if err := j.addRoot(id, false, "registered Git control dir"); err != nil {
			return Jurisdiction{}, err
		}
	}
	for _, id := range in.GatewaySecrets {
		if err := j.addRoot(id, false, "gateway secret / CA private-key root"); err != nil {
			return Jurisdiction{}, err
		}
	}
	for _, id := range in.RuntimeControls {
		if err := j.addRoot(id, false, "runtime control dir"); err != nil {
			return Jurisdiction{}, err
		}
	}
	// HomeClassRoots is the "when present" collection: the caller omits absent
	// ones, so anything here is a real member. A nil-Info entry is omission,
	// not an absent member; this is the one collection the general rule
	// excludes.
	for _, id := range in.HomeClassRoots {
		if !id.Present() {
			continue
		}
		if err := j.addRoot(id, false, "home credential root"); err != nil {
			return Jurisdiction{}, err
		}
	}

	// Another Project's roots report cross_project. The own Project is never a
	// member: its roots pass Authorize as ordinary sources. Own/other is
	// decided by identity, never by name.
	for _, ws := range in.OtherProjects {
		for _, id := range projectMembers(ws) {
			if err := j.addOtherProjectRoot(in.OwnProject, id); err != nil {
				return Jurisdiction{}, err
			}
		}
	}

	// denied_paths is purely additive, and last. It can only add.
	for _, p := range in.DeniedPaths {
		id, err := resolveDeclared(p)
		if err != nil {
			return Jurisdiction{}, markCandidateAuthored(err)
		}
		if err := j.addRoot(id, false, "profile denied_paths entry"); err != nil {
			return Jurisdiction{}, markCandidateAuthored(err)
		}
	}

	j.resolved = true
	return j, nil
}

func markCandidateAuthored(err error) error {
	var me *MountError
	if !errors.As(err, &me) {
		return err
	}
	marked := *me
	marked.CandidateAuthored = true
	return &marked
}

// addRoot registers one member. A zero ProtectedID (no canonical path at all) is
// not a member: the caller supplied nothing. An ABSENT member — a declared path
// with a nil Info — IS a member, and is registered.
func (j *Jurisdiction) addRoot(id ProtectedID, cross bool, label string) error {
	root, ok, err := prepareProtectedRoot(id, cross, false, label)
	if err != nil {
		return err
	}
	if ok {
		j.roots = append(j.roots, root)
	}
	return nil
}

func (j *Jurisdiction) addOwnControlRoot(id ProtectedID, label string) error {
	root, ok, err := prepareProtectedRoot(id, false, true, label)
	if err != nil {
		return err
	}
	if ok {
		j.roots = append(j.roots, root)
	}
	return nil
}

// addOtherProjectRoot prepares before own/other discrimination. A path that
// was nil-Info in the caller's snapshot may have appeared before this fresh
// construction; once promoted to a present identity it must not be misfiled as
// another Project merely because the stale nil could not compare.
func (j *Jurisdiction) addOtherProjectRoot(own ProjectRoots, id ProtectedID) error {
	root, ok, err := prepareProtectedRoot(id, true, false, "another Project's root")
	if err != nil {
		return err
	}
	if !ok || j.isOwn(own, root.id) {
		return nil
	}
	j.roots = append(j.roots, root)
	return nil
}

func prepareProtectedRoot(id ProtectedID, cross, allowOwnWorkspaceAncestor bool, label string) (protectedRoot, bool, error) {
	if id.Canonical == "" {
		return protectedRoot{}, false, nil
	}
	resolved, absent, err := resolveProtectedIDWith(id, liveProtectedPathOps)
	if err != nil {
		code := CodeDeniedRoot
		if cross {
			code = CodeCrossProject
		}
		return protectedRoot{}, false, mountErrf(code,
			"cannot resolve protected %s %q: %v", label, id.Canonical, err)
	}
	return protectedRoot{
		id:                        resolved,
		absent:                    absent,
		cross:                     cross,
		allowOwnWorkspaceAncestor: allowOwnWorkspaceAncestor,
		label:                     label,
	}, true, nil
}

// resolveProtectedIDWith turns a nil-Info declaration into a stable
// canonical-anchor/original-suffix pair. Only ENOENT peels: every other lookup
// result leaves the boundary unknowable and therefore denies construction.
//
// If the full path appeared since the caller made its nil-Info snapshot, it is
// promoted to an ordinary present identity. That is fresh reconstruction, not
// mutation of an already-constructed Jurisdiction.
func resolveProtectedIDWith(id ProtectedID, ops protectedPathOps) (ProtectedID, *absentRoot, error) {
	if id.Present() || id.Canonical == "" {
		return id, nil, nil
	}
	if strings.ContainsAny(id.Canonical, "\x00\r\n") {
		return ProtectedID{}, nil, mountErrf(CodeDeniedRoot,
			"protected path %q contains NUL or newline", id.Canonical)
	}
	if !filepath.IsAbs(id.Canonical) {
		return ProtectedID{}, nil, mountErrf(CodeDeniedRoot,
			"protected path %q is not absolute", id.Canonical)
	}

	declared := filepath.Clean(id.Canonical)
	current := declared
	var suffix []string

	for {
		_, err := ops.lstat(current)
		if err == nil {
			canonical, evalErr := ops.evalSymlinks(current)
			if evalErr != nil {
				return ProtectedID{}, nil, mountErrf(CodeDeniedRoot,
					"protected path %q reached existing prefix %q but it cannot be canonicalized: %v; ambiguity denies",
					declared, current, evalErr)
			}
			canonical = filepath.Clean(canonical)
			if !filepath.IsAbs(canonical) {
				return ProtectedID{}, nil, mountErrf(CodeDeniedRoot,
					"protected path %q resolved prefix %q to non-absolute %q; ambiguity denies",
					declared, current, canonical)
			}
			info, statErr := ops.stat(canonical)
			if statErr != nil {
				return ProtectedID{}, nil, mountErrf(CodeDeniedRoot,
					"protected path %q resolved prefix %q to %q but it cannot be stat'd: %v; ambiguity denies",
					declared, current, canonical, statErr)
			}

			anchor := ProtectedID{Canonical: canonical, Info: info, IsDir: info.IsDir()}
			if len(suffix) == 0 {
				return anchor, nil, nil
			}
			if !info.IsDir() {
				return ProtectedID{}, nil, mountErrf(CodeDeniedRoot,
					"protected path %q has unresolved suffix below non-directory prefix %q; ambiguity denies",
					declared, canonical)
			}

			mode, modeErr := ops.caseMode(canonical)
			if modeErr != nil || (mode != suffixCaseSensitive && mode != suffixCaseInsensitive) {
				// Unknown case semantics do not make every comparison ambiguous.
				// Exact NFC components and different full folds remain decidable;
				// suffixComponentEqual denies only a fold-equivalent variant.
				mode = suffixCaseUnknown
			}
			return ProtectedID{Canonical: declared}, &absentRoot{
				anchor: anchor,
				suffix: append([]string(nil), suffix...),
				mode:   mode,
			}, nil
		}

		if !errors.Is(err, syscall.ENOENT) {
			return ProtectedID{}, nil, mountErrf(CodeDeniedRoot,
				"protected path %q cannot be examined at %q: %v; only ENOENT is absence and ambiguity denies",
				declared, current, err)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return ProtectedID{}, nil, mountErrf(CodeDeniedRoot,
				"protected path %q has no existing ancestor; ambiguity denies", declared)
		}
		suffix = append([]string{filepath.Base(current)}, suffix...)
		current = parent
	}
}

func projectMembers(ws ProjectRoots) []ProtectedID {
	out := make([]ProtectedID, 0, 5+len(ws.Artifacts))
	out = append(out, ws.Workspace, ws.Worktree, ws.State, ws.Cache, ws.ToolHome)
	return append(out, ws.Artifacts...)
}

// isOwn decides own-vs-other by filesystem identity, never by name. An
// absent member cannot be compared by identity and is therefore never "own":
// treating an unidentifiable root as the caller's own would be the fail-open
// direction.
func (j *Jurisdiction) isOwn(own ProjectRoots, id ProtectedID) bool {
	if !id.Present() {
		return false
	}
	for _, o := range projectMembers(own) {
		if o.Present() && sameFile(o.Info, id.Info) {
			return true
		}
	}
	return false
}

// resolveDeclared turns an operator-declared path into a member. The path can
// be absent, which is not an error: it becomes an absent member, and the
// nearest-existing-ancestor rule compares it.
func resolveDeclared(path string) (ProtectedID, error) {
	if path == "" {
		return ProtectedID{}, mountErrf(CodeDeniedRoot, "denied_paths entry is empty")
	}
	if strings.ContainsAny(path, "\x00\r\n") {
		return ProtectedID{}, mountErrf(CodeDeniedRoot,
			"denied_paths entry %q contains NUL or newline", path)
	}
	if !filepath.IsAbs(path) {
		return ProtectedID{}, mountErrf(CodeDeniedRoot, "denied_paths entry %q is not absolute", path)
	}
	// Registration performs the one authoritative absent-member resolution.
	// Routing this through ResolveSource first would collapse ENOTDIR, dangling
	// links, and resolution races into mount.source_missing and falsely
	// legalize ambiguity.
	return ProtectedID{Canonical: filepath.Clean(path)}, nil
}

// resolveHome validates the injected HOME. It does not trust it.
//
// It takes the raw spelling, because the raw spelling is the evidence: leg 2
// cannot exist against a pre-resolved path, since a canonical path's final
// component is never a symlink.
//
// It is not TrustHomeDir, and it must never route through trustedOwnerMode.
// That seam belongs to MC_HOME: MC creates MC_HOME itself at 0700, so it
// enforces perm&0o077 == 0. The operator's real HOME is 0750 on stock macOS
// and 0755 elsewhere, so that seam rejects every real HOME and no plan can be
// made. HOME's mode is not MC's business, so this check has five legs and no
// mode leg. For the same reason it has no ACL leg: a real HOME can carry an
// ACL, and a managed or network HOME can carry allow ACEs. Reuse the lstat
// seam only.
func resolveHome(home string, ownerUID int) (ProtectedID, error) {
	if home == "" {
		return ProtectedID{}, mountErrf(CodeDeniedRoot,
			"jurisdiction requires the operator's real HOME; it is injected, never read from $HOME")
	}
	if !filepath.IsAbs(home) {
		return ProtectedID{}, mountErrf(CodeDeniedRoot, "HOME %q is not absolute", home)
	}
	clean := filepath.Clean(home)

	// Leg 1: Lstat, NEVER Stat. Stat follows the link and destroys leg 2's only
	// evidence.
	info, err := os.Lstat(clean)
	if err != nil {
		return ProtectedID{}, mountErrf(CodeDeniedRoot, "HOME %q cannot be stat'd: %v", home, err)
	}

	// Leg 2: refuse a symlink; do not resolve through it. This matches
	// trustedLstat: a symlink to an otherwise-trusted object is not itself
	// trusted. A $HOME pointed at an attacker-controlled directory would
	// otherwise relocate broad_root's anchor and the whole ~/.ssh-class member
	// set: the real ~/.ssh left unprotected, the attacker's protected.
	if info.Mode()&os.ModeSymlink != 0 {
		return ProtectedID{}, mountErrf(CodeDeniedRoot,
			"HOME %q is a symlink; the operator's real HOME is refused rather than resolved through", home)
	}

	// Leg 3: a directory.
	if !info.IsDir() {
		return ProtectedID{}, mountErrf(CodeDeniedRoot, "HOME %q is not a directory", home)
	}

	// Leg 4: not a filesystem root. Before ownership, so "/" refuses for the right
	// reason. A HOME of "/" would make broad_root reject every source on the
	// machine: fail-closed, but useless.
	if root, why := isFilesystemRoot(clean, info); root {
		return ProtectedID{}, mountErrf(CodeDeniedRoot,
			"HOME %q is a filesystem root (%s); broad_root would then reject every source", home, why)
	}

	// Leg 5: owned by the operator. Injection relocates trust; it does not remove
	// it.
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ProtectedID{}, mountErrf(CodeDeniedRoot, "cannot read ownership of HOME %q", home)
	}
	if int(stat.Uid) != ownerUID {
		return ProtectedID{}, mountErrf(CodeDeniedRoot,
			"HOME %q is owned by uid %d, not the operator uid %d", home, stat.Uid, ownerUID)
	}

	// Validated. Now canonicalize for the identity comparisons broad_root walks.
	canonical, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return ProtectedID{}, mountErrf(CodeDeniedRoot, "HOME %q cannot be resolved: %v", home, err)
	}
	cinfo, err := os.Stat(canonical)
	if err != nil {
		return ProtectedID{}, mountErrf(CodeDeniedRoot, "HOME %q cannot be stat'd: %v", canonical, err)
	}
	return ProtectedID{Canonical: canonical, Info: cinfo, IsDir: true}, nil
}

// isFilesystemRoot implements leg 4 of resolveHome. The three tests are
// complementary: Dir-self misses a device-boundary root such as
// /System/Volumes/VM, while the Darwin mount-point identity still catches APFS
// roots such as /System/Volumes/Data whose parent reports the same device.
//
// Ambiguity denies: if the parent cannot be examined, treat the path as a
// root rather than assume it is not.
func isFilesystemRoot(clean string, info fs.FileInfo) (bool, string) {
	return isFilesystemRootWith(clean, info, os.Lstat, mountPoint)
}

// isFilesystemRootWith carries the three complementary root witnesses behind
// injectable read-only lookups. The seam is deliberately small: it lets the
// portable device-boundary leg be tested on a host whose Darwin mount-point API
// would otherwise make that same witness pass for a different reason.
func isFilesystemRootWith(
	clean string,
	info fs.FileInfo,
	lstat func(string) (fs.FileInfo, error),
	lookupMountPoint func(string) (string, error),
) (bool, string) {
	parent := filepath.Dir(clean)
	if parent == clean {
		return true, "it is its own parent"
	}
	pinfo, err := lstat(parent)
	if err != nil {
		return true, "its parent cannot be examined, and ambiguity denies"
	}
	if sameFile(info, pinfo) {
		return true, "it is the same object as its parent"
	}

	// Portable mount-boundary evidence. A mounted filesystem root may have an
	// ordinary lexical parent, but stat(2) still exposes the device transition.
	// If either stat object is unavailable, assuming the devices match would be
	// the fail-open answer.
	stat, statOK := info.Sys().(*syscall.Stat_t)
	parentStat, parentStatOK := pinfo.Sys().(*syscall.Stat_t)
	if !statOK || stat == nil || !parentStatOK || parentStat == nil {
		return true, "its device identity cannot be examined, and ambiguity denies"
	}
	if parentStat.Dev != stat.Dev {
		return true, "its device differs from its parent"
	}

	// The kernel's own answer, where available: a path whose volume mount point is
	// itself IS a filesystem root, however ordinary its parent looks.
	mnt, err := lookupMountPoint(clean)
	if err != nil {
		if errors.Is(err, errNoMountPoint) {
			return false, ""
		}
		return true, "its volume mount point cannot be examined, and ambiguity denies"
	}
	minfo, err := lstat(mnt)
	if err != nil {
		return true, "its volume mount point cannot be stat'd, and ambiguity denies"
	}
	if sameFile(info, minfo) {
		return true, "it is a volume mount point"
	}
	return false, ""
}

// ancestorRoutes returns every path that is an ancestor of p by reachability:
// p's own filepath.Dir chain, union the chain of each member's volume mount
// point.
//
// The second half matters. /Users is an APFS firmlink: lstat reports a plain
// directory, ModeSymlink is clear, and filepath.EvalSymlinks neither sees
// through it nor normalizes it. So a HOME under /Users has a second live
// ancestor chain that no Dir walk produces. os.SameFile catches aliases of the
// chain's own members, but the volume mount point /System/Volumes/Data is an
// alias of nothing in the chain, and the entire HOME tree is readable through
// it.
//
// Both consumers share this helper: broad_root, and the union's "S is an
// ancestor of P" mirror walk.
//
// Ambiguity denies: a real statfs failure on any member returns an error
// rather than a short set. A platform that exposes no mount point at all is a
// different case: there the rule degrades to the Dir chain, because without
// firmlinks the mount point is already on it.
func ancestorRoutes(p string) ([]string, error) {
	seen := map[string]bool{}
	var out []string

	addChain := func(start string) {
		for cur := start; ; {
			if !seen[cur] {
				seen[cur] = true
				out = append(out, cur)
			}
			parent := filepath.Dir(cur)
			if parent == cur {
				return
			}
			cur = parent
		}
	}
	addChain(p)

	// Snapshot before iterating: addChain appends to out.
	chain := append([]string(nil), out...)
	for _, member := range chain {
		if _, err := os.Lstat(member); err != nil {
			// Absent declarations are enriched before this helper is called, so
			// every input here is a present canonical root or source route.
			// Missing and unreadable members are races/ambiguity, never expected
			// absence.
			return nil, mountErrf(CodeDeniedRoot,
				"cannot examine ancestor route %q: %v; ambiguity denies", member, err)
		}
		mnt, err := mountPoint(member)
		if err != nil {
			if errors.Is(err, errNoMountPoint) {
				continue // no firmlink plane here; the Dir chain is the whole answer
			}
			// The path is there and the kernel still would not say which volume
			// hosts it: the alias routes are unknown, and ambiguity denies.
			return nil, mountErrf(CodeDeniedRoot,
				"cannot determine the volume hosting %q, so its alias routes are unknown "+
					"and ambiguity denies: %v", member, err)
		}
		if !seen[mnt] {
			addChain(mnt)
		}
	}
	return out, nil
}

// resolveBroadRoots computes the broad_root set: HOME, its ancestors, and its
// alias routes. HOME was validated and every route is present; a failed lookup
// is a resolution race, not evidence that the member is irrelevant.
func resolveBroadRoots(home ProtectedID) ([]ProtectedID, error) {
	routes, err := ancestorRoutes(home.Canonical)
	if err != nil {
		return nil, err
	}
	out := make([]ProtectedID, 0, len(routes))
	for _, r := range routes {
		info, err := os.Lstat(r)
		if err != nil {
			return nil, mountErrf(CodeDeniedRoot,
				"cannot stat HOME ancestor route %q: %v; ambiguity denies", r, err)
		}
		out = append(out, ProtectedID{Canonical: r, Info: info, IsDir: info.IsDir()})
	}
	return out, nil
}

// sameFile compares two stat objects by filesystem identity, tolerating the nil
// Info of an absent member. os.SameFile itself returns false rather than
// panicking on nil, but going through one helper keeps that fact in one place.
func sameFile(a, b fs.FileInfo) bool {
	if a == nil || b == nil {
		return false
	}
	return os.SameFile(a, b)
}

func asMountError(err error, target **MountError) bool { return errors.As(err, target) }

// Rejects reports whether one source is out of jurisdiction. It is callable
// without an allow-root match: a source owes its identity and cross-Project
// checks whether or not the allowlist admits it.
func (j Jurisdiction) Rejects(id SourceIdentity) error {
	// The zero value fails closed. A zero Jurisdiction is not an empty set that
	// permits everything; it is a value nobody constructed.
	if !j.resolved {
		return mountErrf(CodeDeniedRoot,
			"unresolved jurisdiction rejects every source: %q", id.Canonical)
	}

	// broad_root is HOME's directional weakening. S == HOME or S a strict
	// ancestor of HOME rejects; descendants stay eligible unless they hit
	// another protected root, because every workspace lives under HOME. Every
	// other member is bidirectional.
	//
	// It reports mount.denied_root: the closed code list has no
	// mount.broad_root, and the case files beside the other jurisdiction cases.
	// The message says broad_root so the operator sees which rule fired.
	for _, r := range j.broadRoots {
		if sameFile(id.Info, r.Info) {
			return mountErrf(CodeDeniedRoot,
				"source %q equals or is an ancestor of the operator's HOME %q (broad_root: %q)",
				id.Canonical, j.home.Canonical, r.Canonical)
		}
	}

	// Precedence: when a path is both another Project's root and a
	// denied_paths entry, cross_project wins. It is the more specific
	// statement of why the path is denied, and the one an operator can act on
	// by re-scoping a Project. The order is fixed here and pinned by a test.
	if err := j.rejectsUnion(id, true); err != nil {
		return err
	}
	return j.rejectsUnion(id, false)
}

// rejectsUnion applies the bidirectional intersection over the members of one
// class (cross-Project or not).
func (j Jurisdiction) rejectsUnion(id SourceIdentity, cross bool) error {
	for _, root := range j.roots {
		if root.cross != cross {
			continue
		}
		code := CodeDeniedRoot
		if root.cross {
			code = CodeCrossProject
		}

		// Present roots use identity in all three directions. The descendant walk
		// is strict: a failed parent lookup is ambiguity rather than "not under".
		if root.id.Present() {
			if sameFile(id.Info, root.id.Info) {
				return mountErrf(code, "source %q is the protected %s %q",
					id.Canonical, root.label, root.id.Canonical)
			}
			_, below, err := remainderBelowIdentityWith(root.id.Info, id, os.Lstat)
			if err != nil {
				return unionComparisonError(code, id, root, err)
			}
			if !below {
				// A Darwin firmlink route can make the protected identity an
				// ancestor even when it is absent from the lexical Dir chain.
				below, err = identityOnAncestorRoutes(id.Canonical, root.id.Info)
				if err != nil {
					return unionComparisonError(code, id, root, err)
				}
			}
			if below {
				return mountErrf(code, "source %q is under the protected %s %q",
					id.Canonical, root.label, root.id.Canonical)
			}

			// S is a strict ancestor of present P — the mirror walk, including
			// volume alias routes that no bare filepath.Dir chain reaches.
			ancestor, err := j.isAncestorOf(id, root.id.Canonical)
			if err != nil {
				return unionComparisonError(code, id, root, err)
			}
			if ancestor {
				if root.allowOwnWorkspaceAncestor && j.sourceIsOwnWorkspace(id) {
					continue
				}
				return mountErrf(code, "source %q is an ancestor of the protected %s %q, and mounting "+
					"it would expose that root", id.Canonical, root.label, root.id.Canonical)
			}
			continue
		}

		if root.absent == nil {
			return mountErrf(code,
				"protected %s %q is absent but has no canonical-anchor/suffix pair; ambiguity denies",
				root.label, root.id.Canonical)
		}
		intersects, sourceAncestor, err := j.intersectsAbsent(id, *root.absent)
		if err != nil {
			return unionComparisonError(code, id, root, err)
		}
		if !intersects {
			continue
		}
		// The control exception is directional. It applies only when the exact
		// own workspace is an ancestor of the would-be control; equality with or
		// a descendant of that control remains protected.
		if sourceAncestor && root.allowOwnWorkspaceAncestor && j.sourceIsOwnWorkspace(id) {
			continue
		}
		return mountErrf(code, "source %q intersects absent protected %s %q, and mounting it "+
			"would expose that root", id.Canonical, root.label, root.id.Canonical)
	}
	return nil
}

func unionComparisonError(code string, id SourceIdentity, root protectedRoot, err error) error {
	return mountErrf(code,
		"cannot safely compare source %q with protected %s %q: %v",
		id.Canonical, root.label, root.id.Canonical, err)
}

// intersectsAbsent compares the two halves of an absent member with their
// respective laws: filesystem identity for the anchor, component semantics for
// the suffix.
// sourceAncestor is returned separately because only that direction can earn
// the exact-own-workspace control exemption.
func (j Jurisdiction) intersectsAbsent(id SourceIdentity, absent absentRoot) (intersects, sourceAncestor bool, err error) {
	remainder, below, err := remainderBelowIdentityWith(absent.anchor.Info, id, os.Lstat)
	if err != nil {
		return false, false, err
	}
	if below {
		overlaps, overlapErr := suffixesOverlap(absent.suffix, remainder, absent.mode)
		if overlapErr != nil {
			return false, false, overlapErr
		}
		if !overlaps {
			return false, false, nil
		}
		return true, len(remainder) < len(absent.suffix), nil
	}

	// If a firmlink says the anchor is reachable above the source but the
	// lexical identity walk could not derive a remainder, the relationship is
	// real and its suffix is unknowable. Treating it as unrelated would be the
	// fail-open answer.
	reachable, routeErr := identityOnAncestorRoutes(id.Canonical, absent.anchor.Info)
	if routeErr != nil {
		return false, false, routeErr
	}
	if reachable {
		return false, false, mountErrf(CodeDeniedRoot,
			"canonical anchor %q is reachable above source %q only through an alias route; "+
				"the corresponding suffix is unknown and ambiguity denies",
			absent.anchor.Canonical, id.Canonical)
	}

	above, aboveErr := j.isAncestorOf(id, absent.anchor.Canonical)
	if aboveErr != nil {
		return false, false, aboveErr
	}
	if above {
		return true, true, nil
	}
	return false, false, nil
}

// remainderBelowIdentityWith identity-walks an existing source upward and
// returns its canonical component remainder below ancestor. The source leaf's
// FileInfo is the already-resolved snapshot; every parent lookup is strict.
func remainderBelowIdentityWith(
	ancestor fs.FileInfo,
	id SourceIdentity,
	lstat func(string) (fs.FileInfo, error),
) ([]string, bool, error) {
	current := id.Canonical
	currentInfo := id.Info
	var remainder []string
	for {
		if sameFile(currentInfo, ancestor) {
			return remainder, true, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil, false, nil
		}
		remainder = append([]string{filepath.Base(current)}, remainder...)
		parentInfo, err := lstat(parent)
		if err != nil {
			return nil, false, mountErrf(CodeDeniedRoot,
				"cannot examine source ancestor %q: %v; ambiguity denies", parent, err)
		}
		if parentInfo.Mode()&os.ModeSymlink != 0 {
			return nil, false, mountErrf(CodeDeniedRoot,
				"source ancestor %q became a symlink after canonicalization; ambiguity denies", parent)
		}
		current = parent
		currentInfo = parentInfo
	}
}

// identityOnAncestorRoutes reports whether target is an ancestor of canonical
// by reachability, including Darwin volume aliases. Every route is expected to
// exist; lookup failure is ambiguity.
func identityOnAncestorRoutes(canonical string, target fs.FileInfo) (bool, error) {
	routes, err := ancestorRoutes(canonical)
	if err != nil {
		return false, err
	}
	for _, route := range routes {
		info, statErr := os.Lstat(route)
		if statErr != nil {
			return false, mountErrf(CodeDeniedRoot,
				"cannot stat ancestor route %q: %v; ambiguity denies", route, statErr)
		}
		if sameFile(info, target) {
			return true, nil
		}
	}
	return false, nil
}

func (j Jurisdiction) sourceIsOwnWorkspace(id SourceIdentity) bool {
	return j.ownWorkspace.Present() && sameFile(id.Info, j.ownWorkspace.Info)
}

// isAncestorOf reports whether the source is an ancestor of the protected path,
// by identity, including the alias routes a filepath.Dir walk cannot reach.
func (j Jurisdiction) isAncestorOf(id SourceIdentity, protected string) (bool, error) {
	routes, err := ancestorRoutes(protected)
	if err != nil {
		return false, err
	}
	for _, r := range routes {
		if r == protected {
			continue // that is the equality case, handled by the caller
		}
		info, statErr := os.Lstat(r)
		if statErr != nil {
			return false, mountErrf(CodeDeniedRoot,
				"cannot stat protected ancestor route %q: %v; ambiguity denies", r, statErr)
		}
		if sameFile(id.Info, info) {
			return true, nil
		}
	}
	return false, nil
}
