package boundary

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// An absent member is a MEMBER. The external test can only see
// that construction does not error, which a silent skip also satisfies — a
// mutation that dropped absent members passed it. The direction that would
// expose the skip (the ancestor branch) does not exist until step 7, so this
// internal test pins the registration itself now rather than leaving a
// known-vacuous assertion in the suite.
func TestAbsentMemberIsRegisteredNotSkipped(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o750); err != nil {
		t.Fatal(err)
	}

	absent := filepath.Join(dir, "mc-home-not-created-yet")
	j, err := ResolveJurisdiction(JurisdictionInput{
		Home:   home,
		MCHome: ProtectedID{Canonical: absent},
	}, os.Getuid())
	if err != nil {
		t.Fatalf("ResolveJurisdiction() = %v", err)
	}

	var found bool
	for _, r := range j.roots {
		if r.id.Canonical == absent {
			found = true
			if r.id.Present() {
				t.Errorf("absent member %q reports Present()", absent)
			}
		}
	}
	if !found {
		t.Fatalf("absent member %q was SKIPPED, not registered; protection is "+
			"depend on directory creation order if absent members are dropped", absent)
	}
}

// The converse, so the test above cannot pass by registering everything blindly:
// a member the caller simply did not supply (a zero ProtectedID, no declared
// path at all) is not a member.
func TestZeroProtectedIDIsNotAMember(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o750); err != nil {
		t.Fatal(err)
	}

	j, err := ResolveJurisdiction(JurisdictionInput{Home: home}, os.Getuid())
	if err != nil {
		t.Fatalf("ResolveJurisdiction() = %v", err)
	}
	for _, r := range j.roots {
		if r.id.Canonical == "" {
			t.Errorf("a zero ProtectedID was registered as a member (label %q)", r.label)
		}
	}
	if len(j.roots) != 0 {
		t.Errorf("empty input produced %d members, want 0", len(j.roots))
	}
}

// The suffix boundary comes from the cleaned ORIGINAL spelling, not from the
// canonical target or from filepath.EvalSymlinks's error path. The alias and
// target deliberately have different depths and repeat component names: any
// attempt to recover the suffix by comparing canonical strings gets the wrong
// answer.
func TestResolveAbsentProtectedIDRetainsOriginalSuffixAcrossAliasDepth(t *testing.T) {
	dir := t.TempDir()
	anchor := filepath.Join(dir, "real", "deeper", "repeat")
	if err := os.MkdirAll(anchor, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "repeat")
	if err := os.Symlink(anchor, alias); err != nil {
		t.Fatal(err)
	}
	declared := filepath.Join(alias, "repeat", "leaf", "repeat")
	canonicalAnchor, err := filepath.EvalSymlinks(anchor)
	if err != nil {
		t.Fatal(err)
	}
	var evalCalls int
	var evalPath string
	var caseCalls int
	var casePath string
	ops := liveProtectedPathOps
	ops.evalSymlinks = func(path string) (string, error) {
		evalCalls++
		evalPath = path
		return filepath.EvalSymlinks(path)
	}
	ops.caseMode = func(path string) (suffixCaseMode, error) {
		caseCalls++
		casePath = path
		return suffixCaseSensitive, nil
	}

	id, absent, err := resolveProtectedIDWith(
		ProtectedID{Canonical: declared}, ops,
	)
	if err != nil {
		t.Fatalf("resolveProtectedIDWith(%q) = %v", declared, err)
	}
	if id.Present() {
		t.Fatalf("absent declared member became present: %#v", id)
	}
	if id.Canonical != filepath.Clean(declared) {
		t.Errorf("declared spelling = %q, want %q", id.Canonical, filepath.Clean(declared))
	}
	if absent == nil {
		t.Fatal("absent member has no canonical-anchor/suffix pair")
	}
	if absent.anchor.Canonical != canonicalAnchor || !sameFile(absent.anchor.Info, mustLstat(t, anchor)) {
		t.Errorf("canonical anchor = %#v, want identity %q", absent.anchor, canonicalAnchor)
	}
	if evalCalls != 1 {
		t.Errorf("EvalSymlinks calls = %d, want exactly 1", evalCalls)
	}
	if evalPath != alias {
		t.Errorf("EvalSymlinks path = %q, want nearest existing original prefix %q", evalPath, alias)
	}
	if caseCalls != 1 || casePath != canonicalAnchor || absent.mode != suffixCaseSensitive {
		t.Errorf("case lookup = (%q, %v), want (%q, sensitive)", casePath, absent.mode, canonicalAnchor)
	}
	wantSuffix := []string{"repeat", "leaf", "repeat"}
	if len(absent.suffix) != len(wantSuffix) {
		t.Fatalf("suffix = %#v, want %#v", absent.suffix, wantSuffix)
	}
	for i := range wantSuffix {
		if absent.suffix[i] != wantSuffix[i] {
			t.Errorf("suffix[%d] = %q, want %q", i, absent.suffix[i], wantSuffix[i])
		}
	}
}

func TestResolveAbsentProtectedIDPromotesAPathThatAppeared(t *testing.T) {
	dir := t.TempDir()
	appeared := filepath.Join(dir, "appeared")
	if err := os.Mkdir(appeared, 0o755); err != nil {
		t.Fatal(err)
	}

	id, absent, err := resolveProtectedIDWith(ProtectedID{Canonical: appeared}, liveProtectedPathOps)
	if err != nil {
		t.Fatalf("resolveProtectedIDWith() = %v", err)
	}
	if !id.Present() || absent != nil {
		t.Fatalf("appeared path = (%#v, %#v), want present identity and no absent pair", id, absent)
	}
	if !sameFile(id.Info, mustLstat(t, appeared)) {
		t.Fatal("promoted identity is not the path that appeared")
	}
}

func TestResolveAbsentProtectedIDRejectsNonDirectoryAnchorWithSuffix(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fileInfo := mustLstat(t, file)
	declared := filepath.Join(file, "child")

	ops := liveProtectedPathOps
	ops.lstat = func(path string) (fs.FileInfo, error) {
		switch path {
		case declared:
			return nil, &os.PathError{Op: "lstat", Path: path, Err: syscall.ENOENT}
		case file:
			return fileInfo, nil
		default:
			t.Fatalf("unexpected Lstat(%q)", path)
			return nil, syscall.EIO
		}
	}
	ops.evalSymlinks = func(path string) (string, error) { return path, nil }
	ops.stat = func(string) (fs.FileInfo, error) { return fileInfo, nil }

	if _, _, err := resolveProtectedIDWith(ProtectedID{Canonical: declared}, ops); err == nil {
		t.Fatal("non-directory canonical anchor with an unresolved suffix was accepted")
	}
}

func TestResolveAbsentProtectedIDStoresUnknownCaseModeWithoutFailing(t *testing.T) {
	dir := t.TempDir()
	declared := filepath.Join(dir, "future")
	ops := liveProtectedPathOps
	ops.caseMode = func(string) (suffixCaseMode, error) {
		return suffixCaseUnknown, syscall.EIO
	}

	_, absent, err := resolveProtectedIDWith(ProtectedID{Canonical: declared}, ops)
	if err != nil {
		t.Fatalf("case-capability error failed construction: %v", err)
	}
	if absent == nil || absent.mode != suffixCaseUnknown {
		t.Fatalf("absent pair = %#v, want stored unknown case mode", absent)
	}
}

func TestEveryAbsentUnionCollectionGetsCanonicalPair(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	anchor := filepath.Join(dir, "anchor")
	if err := os.MkdirAll(home, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(anchor, 0o755); err != nil {
		t.Fatal(err)
	}
	absent := func(name string) ProtectedID {
		return ProtectedID{Canonical: filepath.Join(anchor, name, "future")}
	}

	expected := map[string]bool{}
	add := func(id ProtectedID, cross bool) ProtectedID {
		expected[id.Canonical] = cross
		return id
	}
	homeClassOmission := absent("home-class-omitted")
	in := JurisdictionInput{
		Home:             home,
		MCHome:           add(absent("mc-home"), false),
		HomeClassRoots:   []ProtectedID{homeClassOmission},
		GatewaySecrets:   []ProtectedID{add(absent("gateway"), false)},
		RuntimeControls:  []ProtectedID{add(absent("runtime"), false)},
		OwnGitControls:   []ProtectedID{add(absent("own-git"), false)},
		OtherGitControls: []ProtectedID{add(absent("other-git"), false)},
		DeniedPaths:      []string{add(absent("denied"), false).Canonical},
		OtherProjects: []ProjectRoots{{
			Workspace: add(absent("other-workspace"), true),
			Worktree:  add(absent("other-worktree"), true),
			Artifacts: []ProtectedID{
				add(absent("other-artifact-0"), true),
				add(absent("other-artifact-1"), true),
			},
			State:    add(absent("other-state"), true),
			Cache:    add(absent("other-cache"), true),
			ToolHome: add(absent("other-tool-home"), true),
		}},
	}

	j, err := ResolveJurisdiction(in, os.Getuid())
	if err != nil {
		t.Fatalf("ResolveJurisdiction() = %v", err)
	}
	seen := map[string]bool{}
	for _, root := range j.roots {
		wantCross, wanted := expected[root.id.Canonical]
		if !wanted {
			t.Errorf("unexpected root %q (HOME-class absence must be omitted)", root.id.Canonical)
			continue
		}
		seen[root.id.Canonical] = true
		if root.absent == nil || !root.absent.anchor.Present() || len(root.absent.suffix) == 0 {
			t.Errorf("root %q was not enriched: %#v", root.id.Canonical, root.absent)
		}
		if root.cross != wantCross {
			t.Errorf("root %q cross = %v, want %v", root.id.Canonical, root.cross, wantCross)
		}
	}
	for path := range expected {
		if !seen[path] {
			t.Errorf("absent union member %q was skipped", path)
		}
	}
	if seen[homeClassOmission.Canonical] {
		t.Fatal("absent HOME-class root was generalized into a member")
	}
}

func TestAppearedOtherProjectRootIsPromotedBeforeOwnCheck(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	workspace := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(home, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	ownSource, err := ResolveSource(workspace)
	if err != nil {
		t.Fatal(err)
	}
	own := ProtectedID{Canonical: ownSource.Canonical, Info: ownSource.Info, IsDir: true}

	j, err := ResolveJurisdiction(JurisdictionInput{
		Home:       home,
		OwnProject: ProjectRoots{Workspace: own},
		OtherProjects: []ProjectRoots{{
			// Stale nil-Info declaration: the path has appeared before this fresh
			// construction and is the own workspace by identity.
			Workspace: ProtectedID{Canonical: workspace},
		}},
	}, os.Getuid())
	if err != nil {
		t.Fatalf("ResolveJurisdiction() = %v", err)
	}
	for _, root := range j.roots {
		if root.cross {
			t.Fatalf("appeared own workspace was registered as cross: %#v", root)
		}
	}
	if err := j.Rejects(ownSource); err != nil {
		t.Fatalf("own workspace rejected after promotion-before-own-check: %v", err)
	}
}

func TestRemainderBelowIdentityLookupErrorIsAmbiguity(t *testing.T) {
	dir := t.TempDir()
	anchor := filepath.Join(dir, "anchor")
	source := filepath.Join(dir, "source", "child")
	if err := os.MkdirAll(anchor, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := ResolveSource(source)
	if err != nil {
		t.Fatal(err)
	}

	for _, errno := range []syscall.Errno{syscall.EACCES, syscall.EIO} {
		t.Run(errno.Error(), func(t *testing.T) {
			_, found, err := remainderBelowIdentityWith(
				mustLstat(t, anchor), id,
				func(path string) (fs.FileInfo, error) {
					return nil, &os.PathError{Op: "lstat", Path: path, Err: errno}
				},
			)
			if err == nil || found {
				t.Fatalf("remainder = (found %v, err %v), want ambiguity", found, err)
			}
		})
	}
}

func TestAbsentCrossComparisonAmbiguityKeepsCrossCode(t *testing.T) {
	dir := t.TempDir()
	anchor := filepath.Join(dir, "anchor")
	source := filepath.Join(anchor, "future")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	anchorInfo := mustLstat(t, anchor)
	id, err := ResolveSource(source)
	if err != nil {
		t.Fatal(err)
	}

	j := Jurisdiction{
		resolved: true,
		roots: []protectedRoot{{
			id:    ProtectedID{Canonical: filepath.Join(anchor, "Future")},
			cross: true,
			label: "another Project's root",
			absent: &absentRoot{
				anchor: ProtectedID{Canonical: anchor, Info: anchorInfo, IsDir: true},
				suffix: []string{"Future"},
				mode:   suffixCaseUnknown,
			},
		}},
	}
	err = j.Rejects(id)
	var me *MountError
	if !asMountError(err, &me) || me.Code != CodeCrossProject {
		t.Fatalf("comparison ambiguity error = %v, want %s", err, CodeCrossProject)
	}
}

// Only literal ENOENT means "peel one original component". ENOTDIR,
// permission failures, and I/O failures do not identify a trustworthy nearest
// existing ancestor, so treating them as absence would be fail-open.
func TestResolveAbsentProtectedIDPeelsOnlyENOENT(t *testing.T) {
	dir := t.TempDir()
	declared := filepath.Join(dir, "future", "root")

	for _, errno := range []syscall.Errno{syscall.ENOTDIR, syscall.EACCES, syscall.EIO} {
		t.Run(errno.Error(), func(t *testing.T) {
			ops := liveProtectedPathOps
			var calls []string
			ops.lstat = func(path string) (fs.FileInfo, error) {
				calls = append(calls, path)
				return nil, &os.PathError{Op: "lstat", Path: path, Err: errno}
			}

			_, _, err := resolveProtectedIDWith(ProtectedID{Canonical: declared}, ops)
			if err == nil {
				t.Fatalf("%v was treated as absence", errno)
			}
			var me *MountError
			if !asMountError(err, &me) || me.Code != CodeDeniedRoot {
				t.Fatalf("error = %v, want %s", err, CodeDeniedRoot)
			}
			if len(calls) != 1 || calls[0] != declared {
				t.Fatalf("lstat calls = %#v, want only the full declared path", calls)
			}
		})
	}
}

func TestResolveAbsentProtectedIDAmbiguousCanonicalizationDenies(t *testing.T) {
	dir := t.TempDir()
	info := mustLstat(t, dir)

	tests := []struct {
		name string
		edit func(*protectedPathOps)
	}{
		{
			name: "EvalSymlinks race",
			edit: func(ops *protectedPathOps) {
				ops.lstat = func(string) (fs.FileInfo, error) { return info, nil }
				ops.evalSymlinks = func(path string) (string, error) {
					return "", &os.PathError{Op: "evalsymlinks", Path: path, Err: syscall.EIO}
				}
			},
		},
		{
			name: "canonical stat race",
			edit: func(ops *protectedPathOps) {
				ops.lstat = func(string) (fs.FileInfo, error) { return info, nil }
				ops.evalSymlinks = func(path string) (string, error) { return path, nil }
				ops.stat = func(path string) (fs.FileInfo, error) {
					return nil, &os.PathError{Op: "stat", Path: path, Err: syscall.ENOENT}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := liveProtectedPathOps
			tt.edit(&ops)
			if _, _, err := resolveProtectedIDWith(ProtectedID{Canonical: dir}, ops); err == nil {
				t.Fatal("ambiguous canonicalization was accepted")
			}
		})
	}
}

func mustLstat(t *testing.T, path string) fs.FileInfo {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// Ownership follows the supplied filesystem identity, even when path names differ.
func TestOwnProjectIsNotACrossMember(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o750); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(dir, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}

	mk := func(p string) ProtectedID {
		id, err := ResolveSource(p)
		if err != nil {
			t.Fatal(err)
		}
		return ProtectedID{Canonical: id.Canonical, Info: id.Info, IsDir: id.IsDir}
	}

	own := mk(ws)
	other := mk(ws)
	alias := filepath.Join(filepath.Dir(own.Canonical), "WS")
	if info, err := os.Stat(alias); err == nil && sameFile(own.Info, info) {
		other = mk(alias)
		t.Logf("The fixture uses the actual case alias %q.", alias)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	} else {
		// A case-sensitive filesystem requires a distinct-name identity fixture.
		other.Canonical = alias
		t.Logf("The fixture uses a synthetic case alias %q.", alias)
	}
	if !sameFile(own.Info, other.Info) {
		t.Fatal("The fixture requires one filesystem identity.")
	}
	if own.Canonical == other.Canonical {
		t.Fatal("The fixture requires distinct path names.")
	}

	j, err := ResolveJurisdiction(JurisdictionInput{
		Home:          home,
		OwnProject:    ProjectRoots{Workspace: own},
		OtherProjects: []ProjectRoots{{Workspace: other}},
	}, os.Getuid())
	if err != nil {
		t.Fatalf("ResolveJurisdiction() = %v", err)
	}
	for _, r := range j.roots {
		if r.cross {
			t.Errorf("the own Project's root was registered as cross-Project via its "+
				"alias %q; own/other must be decided by identity, not by name", r.id.Canonical)
		}
	}
}

// The HOME validation requires a portable device-boundary check in addition to Darwin's
// mount-point evidence. The injected lookup deliberately reports that no
// mount-point API exists, so parent.Dev != child.Dev is the only possible
// reason this can classify the child as a filesystem root.
func TestFilesystemRootDetectsDeviceBoundaryWithoutMountPointAPI(t *testing.T) {
	child := deviceFileInfo{name: "volume", stat: &syscall.Stat_t{Dev: 11, Ino: 2}}
	parent := deviceFileInfo{name: "parent", stat: &syscall.Stat_t{Dev: 10, Ino: 3}}

	root, why := isFilesystemRootWith(
		"/parent/volume",
		child,
		func(path string) (fs.FileInfo, error) {
			if path != "/parent" {
				t.Fatalf("Lstat(%q), want parent only", path)
			}
			return parent, nil
		},
		func(string) (string, error) { return "", errNoMountPoint },
	)
	if !root {
		t.Fatal("device-boundary filesystem root was accepted when mount-point lookup was unavailable")
	}
	if !strings.Contains(why, "device") {
		t.Fatalf("reason %q does not identify the device boundary", why)
	}
}

func TestFilesystemRootSameDeviceWithoutMountPointAPIIsOrdinary(t *testing.T) {
	child := deviceFileInfo{name: "child", stat: &syscall.Stat_t{Dev: 10, Ino: 2}}
	parent := deviceFileInfo{name: "parent", stat: &syscall.Stat_t{Dev: 10, Ino: 3}}

	root, why := isFilesystemRootWith(
		"/parent/child",
		child,
		func(string) (fs.FileInfo, error) { return parent, nil },
		func(string) (string, error) { return "", errNoMountPoint },
	)
	if root || why != "" {
		t.Fatalf("ordinary same-device directory = (%v, %q), want (false, empty)", root, why)
	}
}

// The device comparison itself is boundary evidence. If either stat object is
// unavailable, treating the candidate as an ordinary directory would be the
// fail-open answer.
func TestFilesystemRootDeviceAmbiguityDenies(t *testing.T) {
	child := deviceFileInfo{name: "volume", stat: &syscall.Stat_t{Dev: 11, Ino: 2}}
	parent := deviceFileInfo{name: "parent"}

	root, why := isFilesystemRootWith(
		"/parent/volume",
		child,
		func(string) (fs.FileInfo, error) { return parent, nil },
		func(string) (string, error) { return "", errNoMountPoint },
	)
	if !root || !strings.Contains(why, "ambiguity") {
		t.Fatalf("unknown parent device = (%v, %q), want ambiguity denial", root, why)
	}
}

type deviceFileInfo struct {
	name string
	stat *syscall.Stat_t
}

func (i deviceFileInfo) Name() string     { return i.name }
func (deviceFileInfo) Size() int64        { return 0 }
func (deviceFileInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o755 }
func (deviceFileInfo) ModTime() time.Time { return time.Time{} }
func (deviceFileInfo) IsDir() bool        { return true }
func (i deviceFileInfo) Sys() any         { return i.stat }
