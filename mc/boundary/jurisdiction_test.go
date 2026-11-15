package boundary_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mc/boundary"
)

// The constructor, the zero-value law, non-subtractability, and the bound
// that must precede every stat.

// protectedID builds a pre-resolved member the way the host caller must: through
// ResolveSource, so /var -> /private/var is absorbed exactly once and every
// member is an identity rather than a spelling.
func protectedID(t *testing.T, path string) boundary.ProtectedID {
	t.Helper()
	id, err := boundary.ResolveSource(path)
	if err != nil {
		t.Fatalf("ResolveSource(%q) = %v", path, err)
	}
	return boundary.ProtectedID{Canonical: id.Canonical, Info: id.Info, IsDir: id.IsDir}
}

// absentID encodes a member that does not exist on disk: the
// declared cleaned path, and a nil Info. Absence is the happy path — MC_HOME/seals
// and most runtime-control dirs do not exist at boundary-check time.
func absentID(path string) boundary.ProtectedID {
	return boundary.ProtectedID{Canonical: filepath.Clean(path)}
}

// homeFixture builds a HOME the validation accepts. It is a SUBDIRECTORY of dir, never
// dir itself: dir would be a strict ancestor of HOME and broad_root would then
// reject the fixture root out from under every other assertion.
//
// Mode 0o750 deliberately — that is the real mode of a stock macOS HOME
// (drwxr-x---). A 0o700 fixture would pass while production rejects every real
// HOME.
func homeFixture(t *testing.T, dir string) string {
	t.Helper()
	return mkdir(t, filepath.Join(dir, "home"), 0o750)
}

// resolvedEmpty is the "empty but RESOLVED" jurisdiction the Authorize migration
// needs: it has a valid HOME and no other members, so it permits ordinary
// sources while still being a real constructed value rather than a zero one.
func resolvedEmpty(t *testing.T, dir string) boundary.Jurisdiction {
	t.Helper()
	j, err := boundary.ResolveJurisdiction(boundary.JurisdictionInput{
		Home: homeFixture(t, dir),
	}, os.Getuid())
	if err != nil {
		t.Fatalf("ResolveJurisdiction() = %v, want a resolved empty jurisdiction", err)
	}
	return j
}

// A zero Jurisdiction must fail closed: Rejects returns an error for every
// source, rather than be an empty set that permits everything.
//
// BlockPolicy gets this free by compiling its floor into a package-private array
// (shippedBlockedPatterns). Jurisdiction cannot: its members are injected. So the
// mechanism is an unexported `resolved` flag, and this test is what proves the
// mechanism exists rather than the property being asserted in prose.
func TestZeroJurisdictionRejectsEverySource(t *testing.T) {
	dir := t.TempDir()
	// A source that intersects nothing at all: the zero value must still reject
	// it. "Rejects only things it knows about" is the failure mode.
	harmless := mkdir(t, filepath.Join(dir, "harmless"), 0o755)
	id, err := boundary.ResolveSource(harmless)
	if err != nil {
		t.Fatal(err)
	}

	var zero boundary.Jurisdiction
	if err := zero.Rejects(id); err == nil {
		t.Fatal("zero Jurisdiction permitted a source; it must fail closed")
	} else if got := codeOf(t, err); got != boundary.CodeDeniedRoot {
		t.Errorf("zero Jurisdiction error code = %q, want %q", got, boundary.CodeDeniedRoot)
	}
}

// A resolved jurisdiction is distinguishable from a zero one: the same source
// that the zero value rejects is permitted once the value is constructed.
// Without this, the test above would pass against a Rejects that denies always.
func TestResolvedEmptyJurisdictionPermitsAnUnrelatedSource(t *testing.T) {
	dir := t.TempDir()
	j := resolvedEmpty(t, dir)

	harmless := mkdir(t, filepath.Join(dir, "harmless"), 0o755)
	id, err := boundary.ResolveSource(harmless)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Rejects(id); err != nil {
		t.Fatalf("resolved empty jurisdiction rejected an unrelated source: %v", err)
	}
}

// The 512 bound rejects before identity walking or allocation. No
// collection is truncated.
func TestDeniedPathsBound(t *testing.T) {
	dir := t.TempDir()
	home := homeFixture(t, dir)

	t.Run("exactly 512 is accepted", func(t *testing.T) {
		paths := make([]string, 512)
		for i := range paths {
			paths[i] = filepath.Join(dir, "deny", itoaTest(i))
		}
		if _, err := boundary.ResolveJurisdiction(boundary.JurisdictionInput{
			Home:        home,
			DeniedPaths: paths,
		}, os.Getuid()); err != nil {
			t.Fatalf("512 denied paths rejected: %v", err)
		}
	})

	t.Run("513 rejects rather than truncates", func(t *testing.T) {
		paths := make([]string, 513)
		for i := range paths {
			paths[i] = filepath.Join(dir, "deny", itoaTest(i))
		}
		_, err := boundary.ResolveJurisdiction(boundary.JurisdictionInput{
			Home:        home,
			DeniedPaths: paths,
		}, os.Getuid())
		if err == nil {
			t.Fatal("513 denied paths accepted; the bound rejects rather than truncating")
		}
		if got := codeOf(t, err); got != boundary.CodeDeniedRoot {
			t.Errorf("bound error code = %q, want %q", got, boundary.CodeDeniedRoot)
		}
	})

	// "Before any stat" is an ORDERING claim, and the way to test an ordering is
	// to make every later step fail differently and prove the earlier one wins.
	// Here HOME is invalid (absent), which the validation refuses, so if the bound were
	// checked after HOME resolution we would see the HOME error instead.
	t.Run("the bound is checked before HOME resolution and before any stat", func(t *testing.T) {
		paths := make([]string, 513)
		for i := range paths {
			// Paths that would be expensive/erroring to stat if we ever got there.
			paths[i] = filepath.Join(dir, "does", "not", "exist", itoaTest(i))
		}
		_, err := boundary.ResolveJurisdiction(boundary.JurisdictionInput{
			Home:        filepath.Join(dir, "no-such-home"), // the validation would refuse this
			DeniedPaths: paths,
		}, os.Getuid())
		if err == nil {
			t.Fatal("accepted 513 denied paths with an absent HOME")
		}
		if !strings.Contains(err.Error(), "512") {
			t.Errorf("error %q does not name the 512 bound; the bound must be checked "+
				"before HOME resolution and before any stat", err)
		}
	})
}

// ResolveJurisdiction is the ONLY constructor, and its members cannot
// afterwards be removed: no setter, no exported field, no negation form, no
// config.toml key.
//
// The blocked floor models the shape (shippedBlockedPatterns) by compiling itself into
// a private array. Jurisdiction cannot do that — its members are injected — so
// the guarantee is structural: nothing about the value is reachable from outside
// the package.
func TestJurisdictionHasNoExportedMutableSurface(t *testing.T) {
	rt := reflect.TypeOf(boundary.Jurisdiction{})
	for i := 0; i < rt.NumField(); i++ {
		if f := rt.Field(i); f.IsExported() {
			t.Errorf("Jurisdiction.%s is exported: an operator-reachable field is a way to "+
				"subtract a member", f.Name)
		}
	}
	// A method that hands back a mutable view is the same hole wearing a hat.
	for i := 0; i < reflect.TypeOf(boundary.Jurisdiction{}).NumMethod(); i++ {
		m := reflect.TypeOf(boundary.Jurisdiction{}).Method(i)
		if m.Name != "Rejects" {
			t.Errorf("Jurisdiction has unexpected exported method %q; the value permits no accessor "+
				"that could remove or replace a member", m.Name)
		}
	}
}

// An absent member is a MEMBER, not an error and not a skip. Absence is the
// happy path: no MC_HOME class exists before the real scaffolder runs.
func TestAbsentMembersDoNotFailConstruction(t *testing.T) {
	dir := t.TempDir()
	j, err := boundary.ResolveJurisdiction(boundary.JurisdictionInput{
		Home:   homeFixture(t, dir),
		MCHome: absentID(filepath.Join(dir, "mc-home-not-created-yet")),
		OtherGitControls: []boundary.ProtectedID{
			absentID(filepath.Join(dir, "ws", ".git")),
		},
		DeniedPaths: []string{filepath.Join(dir, "deny-does-not-exist")},
	}, os.Getuid())
	if err != nil {
		t.Fatalf("ResolveJurisdiction with absent members = %v; absence is the happy path "+
			"not a construction error", err)
	}
	// It must still be a real, resolved value.
	harmless := mkdir(t, filepath.Join(dir, "harmless"), 0o755)
	id, err := boundary.ResolveSource(harmless)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Rejects(id); err != nil {
		t.Fatalf("jurisdiction built from absent members rejected an unrelated source: %v", err)
	}
}

func TestAbsentDeniedPathRejectsCanonicalParentAndKeepsSiblingDecidable(t *testing.T) {
	dir := t.TempDir()
	home := homeFixture(t, dir)
	anchor := mkdir(t, filepath.Join(dir, "real", "deep", "anchor"), 0o755)
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(anchor, alias); err != nil {
		t.Fatal(err)
	}

	j, err := boundary.ResolveJurisdiction(boundary.JurisdictionInput{
		Home:        home,
		DeniedPaths: []string{filepath.Join(alias, "blocked", "child")},
	}, os.Getuid())
	if err != nil {
		t.Fatalf("ResolveJurisdiction() = %v", err)
	}
	aboveAnchor := filepath.Dir(anchor)
	aboveID, err := boundary.ResolveSource(aboveAnchor)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Rejects(aboveID); codeOf(t, err) != boundary.CodeDeniedRoot {
		t.Fatalf("source above canonical anchor code = %q, want %q (error: %v)",
			codeOf(t, err), boundary.CodeDeniedRoot, err)
	}

	// These appear only after the jurisdiction snapshot. The protected member
	// remains absent in that snapshot, so the verdict must come from its stored
	// canonical-anchor/original-suffix pair.
	blockedParent := mkdir(t, filepath.Join(anchor, "blocked"), 0o755)
	nearPrefixSibling := mkdir(t, filepath.Join(anchor, "blocked-near"), 0o755)

	blockedID, err := boundary.ResolveSource(blockedParent)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Rejects(blockedID); codeOf(t, err) != boundary.CodeDeniedRoot {
		t.Fatalf("absent denied-path parent code = %q, want %q (error: %v)",
			codeOf(t, err), boundary.CodeDeniedRoot, err)
	}

	protectedPath := mkdir(t, filepath.Join(blockedParent, "child"), 0o755)
	protectedDescendant := mkdir(t, filepath.Join(protectedPath, "deeper"), 0o755)
	for _, path := range []string{protectedPath, protectedDescendant} {
		id, err := boundary.ResolveSource(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := j.Rejects(id); codeOf(t, err) != boundary.CodeDeniedRoot {
			t.Fatalf("absent denied-path intersection %q code = %q, want %q (error: %v)",
				path, codeOf(t, err), boundary.CodeDeniedRoot, err)
		}
	}

	siblingID, err := boundary.ResolveSource(nearPrefixSibling)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Rejects(siblingID); err != nil {
		t.Fatalf("component near-prefix sibling rejected: %v", err)
	}
}

func TestAbsentOtherProjectArtifactRejectsItsCanonicalParent(t *testing.T) {
	dir := t.TempDir()
	home := homeFixture(t, dir)
	anchor := mkdir(t, filepath.Join(dir, "other", "artifacts"), 0o755)
	j, err := boundary.ResolveJurisdiction(boundary.JurisdictionInput{
		Home: home,
		OtherProjects: []boundary.ProjectRoots{{
			Artifacts: []boundary.ProtectedID{
				absentID(filepath.Join(anchor, "future", "artifact")),
			},
		}},
	}, os.Getuid())
	if err != nil {
		t.Fatalf("ResolveJurisdiction() = %v", err)
	}

	id, err := boundary.ResolveSource(anchor)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Rejects(id); codeOf(t, err) != boundary.CodeCrossProject {
		t.Fatalf("absent other-artifact parent code = %q, want %q (error: %v)",
			codeOf(t, err), boundary.CodeCrossProject, err)
	}
}

func TestAbsentProtectedPathAmbiguityDeniesConstruction(t *testing.T) {
	dir := t.TempDir()
	home := homeFixture(t, dir)

	regular := filepath.Join(dir, "regular")
	if err := os.WriteFile(regular, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "missing-target"), dangling); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		filepath.Join(regular, "child"),
		filepath.Join(dangling, "child"),
	} {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			_, err := boundary.ResolveJurisdiction(boundary.JurisdictionInput{
				Home:        home,
				DeniedPaths: []string{path},
			}, os.Getuid())
			if err == nil {
				t.Fatalf("ambiguous absent path %q was accepted", path)
			}
			if got := codeOf(t, err); got != boundary.CodeDeniedRoot {
				t.Fatalf("code = %q, want %q (error: %v)", got, boundary.CodeDeniedRoot, err)
			}
		})
	}

	t.Run("other-Project ambiguity keeps cross code", func(t *testing.T) {
		path := filepath.Join(regular, "other-artifact")
		_, err := boundary.ResolveJurisdiction(boundary.JurisdictionInput{
			Home: home,
			OtherProjects: []boundary.ProjectRoots{{
				Artifacts: []boundary.ProtectedID{absentID(path)},
			}},
		}, os.Getuid())
		if got := codeOf(t, err); got != boundary.CodeCrossProject {
			t.Fatalf("code = %q, want %q (error: %v)", got, boundary.CodeCrossProject, err)
		}
	})
}

// Rerunning Rejects against an old value must reproduce that immutable
// snapshot; observing protected-set drift requires rebuilding Jurisdiction from
// fresh host inputs. The source identities stay unchanged while only the
// selector symlink for the absent protected path moves.
func TestJurisdictionReconstructionTracksAbsentAliasRetarget(t *testing.T) {
	dir := t.TempDir()
	home := homeFixture(t, dir)
	a := mkdir(t, filepath.Join(dir, "target-a"), 0o755)
	b := mkdir(t, filepath.Join(dir, "target-b"), 0o755)
	alias := filepath.Join(dir, "protected-selector")
	if err := os.Symlink(a, alias); err != nil {
		t.Fatal(err)
	}
	declared := filepath.Join(alias, "future", "root")

	aSource, err := boundary.ResolveSource(a)
	if err != nil {
		t.Fatal(err)
	}
	bSource, err := boundary.ResolveSource(b)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(aSource.Info, bSource.Info) {
		t.Fatal("fixture is inert: target A and target B are the same identity")
	}
	selectorBefore, err := boundary.ResolveSource(alias)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(selectorBefore.Info, aSource.Info) || os.SameFile(selectorBefore.Info, bSource.Info) {
		t.Fatal("fixture selector does not initially resolve only to target A")
	}
	build := func(t *testing.T) boundary.Jurisdiction {
		t.Helper()
		j, err := boundary.ResolveJurisdiction(boundary.JurisdictionInput{
			Home:        home,
			DeniedPaths: []string{declared},
		}, os.Getuid())
		if err != nil {
			t.Fatalf("ResolveJurisdiction() = %v", err)
		}
		return j
	}
	assertDenied := func(t *testing.T, j boundary.Jurisdiction, source boundary.SourceIdentity) {
		t.Helper()
		err := j.Rejects(source)
		if got := codeOf(t, err); got != boundary.CodeDeniedRoot {
			t.Fatalf("source %q code = %q, want %q (error: %v)",
				source.Canonical, got, boundary.CodeDeniedRoot, err)
		}
	}
	assertPermitted := func(t *testing.T, j boundary.Jurisdiction, source boundary.SourceIdentity) {
		t.Helper()
		if err := j.Rejects(source); err != nil {
			t.Fatalf("source %q rejected: %v", source.Canonical, err)
		}
	}

	first := build(t)
	assertDenied(t, first, aSource)
	assertPermitted(t, first, bSource)

	nextAlias := alias + ".next"
	if err := os.Symlink(b, nextAlias); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(nextAlias, alias); err != nil {
		t.Fatal(err)
	}
	selectorAfter, err := boundary.ResolveSource(alias)
	if err != nil {
		t.Fatal(err)
	}
	if selectorAfter.RawClean != selectorBefore.RawClean ||
		!os.SameFile(selectorAfter.Info, bSource.Info) || os.SameFile(selectorAfter.Info, aSource.Info) {
		t.Fatal("selector address did not retain its spelling while changing from identity A to B")
	}
	aAfter, err := boundary.ResolveSource(a)
	if err != nil {
		t.Fatal(err)
	}
	bAfter, err := boundary.ResolveSource(b)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(aAfter.Info, aSource.Info) || !os.SameFile(bAfter.Info, bSource.Info) {
		t.Fatal("direct source identities changed; fixture does not isolate protected-set drift")
	}

	// Re-running the predicate is not allowed to mutate the old snapshot.
	assertDenied(t, first, aSource)
	assertPermitted(t, first, bSource)

	// Fresh reconstruction is what observes drift and flips both controls.
	second := build(t)
	assertPermitted(t, second, aSource)
	assertDenied(t, second, bSource)
}

// The accessor distinguishes a resolved identity from an absent member whose
// bidirectional verdict comes from the private anchor and suffix pair.
func TestProtectedIDPresent(t *testing.T) {
	dir := t.TempDir()
	if got := protectedID(t, dir).Present(); !got {
		t.Error("Present() = false for a real directory")
	}
	if got := absentID(filepath.Join(dir, "nope")).Present(); got {
		t.Error("Present() = true for an absent member")
	}
}

func itoaTest(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
