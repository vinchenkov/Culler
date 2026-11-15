package verbs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// owFixture builds a workspace root that looks like a real repository: a plain
// directory with a `.git` DIRECTORY, which is what resolveGitAdminDir needs to
// find an info/exclude at all.
func owFixture(t *testing.T) OnboardWorkspaceRoot {
	t.Helper()
	// grWorkspace resolves symlinks: on macOS t.TempDir() hands back a
	// /var/folders path that is a symlink into /private/var, and production
	// roots are already EvalSymlinks-clean by the time onboarding stores them
	// (prepareOnboardProject), so an unresolved fixture would test a shape
	// production never produces.
	ws := grWorkspace(t)
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return OnboardWorkspaceRoot{ID: "primary", Root: ws}
}

func owExclude(t *testing.T, root OnboardWorkspaceRoot) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root.Root, ".git", "info", "exclude"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// The whole obligation in one pass: every root exists at exactly mode 0700
// owned by the operator, and every literal is excluded.
func TestEnsureOnboardWorkspaceLayoutCreatesEveryRootAndExclude(t *testing.T) {
	root := owFixture(t)

	changed, detail, err := EnsureOnboardWorkspaceLayout([]OnboardWorkspaceRoot{root}, os.Getuid())
	if err != nil {
		t.Fatalf("ensure layout: %v", err)
	}
	if !changed {
		t.Fatalf("first run reported no change; detail=%q", detail)
	}
	for _, rel := range onboardWorkspaceRoots {
		info, err := os.Lstat(filepath.Join(root.Root, rel))
		if err != nil {
			t.Fatalf("%s was not created: %v", rel, err)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s is mode %v dir=%v, want a mode-0700 directory — every consumer proves that exact shape",
				rel, info.Mode().Perm(), info.IsDir())
		}
	}
	body := owExclude(t, root)
	for _, entry := range onboardExcludeEntries {
		if !hasExcludeLine([]byte(body), entry) {
			t.Fatalf("info/exclude is missing %q; got %q", entry, body)
		}
	}
}

func TestPrepareOnboardProjectToolHomeCreatesAndProvesTheExactProfilePath(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_HOME", home)
	workspace := grWorkspace(t)
	writeVisionFixture(t, workspace)
	prepared, err := prepareOnboardProject(OnboardArgs{
		Project: "primary", WorkspaceRoot: workspace,
		AcceptGeneratedSandbox: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := PrepareOnboardProjectToolHome(prepared, os.Getuid())
	if err != nil || !changed {
		t.Fatalf("first synthetic-home prepare = changed %v err %v", changed, err)
	}
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(canonicalHome, "state", "projects", "primary", "home")
	for path := expected; path != canonicalHome; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("synthetic-home component %q = info %v err %v", path, info, err)
		}
	}
	changed, err = PrepareOnboardProjectToolHome(prepared, os.Getuid())
	if err != nil || changed {
		t.Fatalf("synthetic-home replay = changed %v err %v", changed, err)
	}
}

func TestPrepareOnboardProjectToolHomeRefusesASymlinkedAncestor(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_HOME", home)
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(home, "state")); err != nil {
		t.Fatal(err)
	}
	workspace := grWorkspace(t)
	writeVisionFixture(t, workspace)
	prepared, err := prepareOnboardProject(OnboardArgs{
		Project: "primary", WorkspaceRoot: workspace,
		AcceptGeneratedSandbox: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareOnboardProjectToolHome(prepared, os.Getuid()); err == nil {
		t.Fatal("symlinked synthetic-home ancestor was accepted")
	}
	if _, err := os.Lstat(filepath.Join(elsewhere, "projects")); !os.IsNotExist(err) {
		t.Fatalf("refused symlink was traversed before rejection: %v", err)
	}
}

// `mc onboard` re-runs every section on every invocation, so the steady state
// is a no-op. A second run that reported a change would flip the section's
// status and duplicate both literals forever.
func TestEnsureOnboardWorkspaceLayoutIsIdempotent(t *testing.T) {
	root := owFixture(t)
	if _, _, err := EnsureOnboardWorkspaceLayout([]OnboardWorkspaceRoot{root}, os.Getuid()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first := owExclude(t, root)

	changed, detail, err := EnsureOnboardWorkspaceLayout([]OnboardWorkspaceRoot{root}, os.Getuid())
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if changed {
		t.Fatalf("re-run reported a change on an already-healthy layout: %q", detail)
	}
	if second := owExclude(t, root); second != first {
		t.Fatalf("re-run rewrote info/exclude:\nfirst:  %q\nsecond: %q", first, second)
	}
}

// The single most likely way a naive append is wrong: a body with no trailing
// newline fuses our first literal onto the operator's last pattern, destroying
// one rule and never creating the other, with no error anywhere.
func TestEnsureOnboardWorkspaceLayoutRepairsAMissingTrailingNewline(t *testing.T) {
	root := owFixture(t)
	infoDir := filepath.Join(root.Root, ".git", "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(infoDir, "exclude"), []byte("build"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := EnsureOnboardWorkspaceLayout([]OnboardWorkspaceRoot{root}, os.Getuid()); err != nil {
		t.Fatalf("ensure layout: %v", err)
	}
	body := owExclude(t, root)
	if strings.Contains(body, "build.mc-artifacts/") {
		t.Fatalf("the operator's last pattern was fused with ours: %q", body)
	}
	if !hasExcludeLine([]byte(body), "build") {
		t.Fatalf("the operator's own pattern did not survive: %q", body)
	}
	for _, entry := range onboardExcludeEntries {
		if !hasExcludeLine([]byte(body), entry) {
			t.Fatalf("missing %q after the repair: %q", entry, body)
		}
	}
}

// The operator's own rules are never rewritten, reordered, or deduped — this
// file is theirs and may be under their own version control discipline. A
// literal that is already present is never appended again.
func TestEnsureOnboardWorkspaceLayoutPreservesExistingExcludeContent(t *testing.T) {
	root := owFixture(t)
	infoDir := filepath.Join(root.Root, ".git", "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := "# operator rules\nnode_modules/\n.mc-artifacts/\n"
	if err := os.WriteFile(filepath.Join(infoDir, "exclude"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	_, detail, err := EnsureOnboardWorkspaceLayout([]OnboardWorkspaceRoot{root}, os.Getuid())
	if err != nil {
		t.Fatalf("ensure layout: %v", err)
	}
	if body := owExclude(t, root); body != original {
		t.Fatalf("existing content was not preserved verbatim: %q", body)
	}
	if strings.Contains(detail, "excluded") {
		t.Fatalf("an already-present literal was reported as excluded: %q", detail)
	}
}

// A pre-existing root of the wrong shape REJECTS. It is never chmod'ed into
// compliance: a 0755 root is world-readable state that something else put
// there, and silently tightening it hides whatever did.
func TestEnsureOnboardWorkspaceLayoutRefusesAWrongModeRoot(t *testing.T) {
	root := owFixture(t)
	victim := ProjectArtifactRoot(root.Root)
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(victim, 0o755); err != nil {
		t.Fatal(err)
	}

	_, _, err := EnsureOnboardWorkspaceLayout([]OnboardWorkspaceRoot{root}, os.Getuid())
	if err == nil {
		t.Fatal("a mode-0755 root was accepted")
	}
	if info, statErr := os.Lstat(victim); statErr != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("the refused root was repaired anyway: perm=%v err=%v", info.Mode().Perm(), statErr)
	}
}

// The same rejection for a symlink, which is the shape the ancestor-canonical
// checks exist to catch: it stats like a directory and refuses much later, at
// precreate, as an opaque health refusal.
func TestEnsureOnboardWorkspaceLayoutRefusesASymlinkedRoot(t *testing.T) {
	root := owFixture(t)
	elsewhere := t.TempDir()
	victim := ProjectArtifactRoot(root.Root)
	if err := os.Symlink(elsewhere, victim); err != nil {
		t.Fatal(err)
	}

	if _, _, err := EnsureOnboardWorkspaceLayout([]OnboardWorkspaceRoot{root}, os.Getuid()); err == nil {
		t.Fatal("a symlinked root was accepted")
	}
	if info, err := os.Lstat(victim); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the refused symlink was replaced rather than reported")
	}
}

// A deleted workspace root must stay a hard failure. Creating the layout with
// an MkdirAll from the workspace down would resurrect it and turn the
// operator's mistake into a silent pass.
func TestEnsureOnboardWorkspaceLayoutNeverResurrectsTheWorkspaceRoot(t *testing.T) {
	root := OnboardWorkspaceRoot{ID: "primary", Root: filepath.Join(grWorkspace(t), "gone")}

	if _, _, err := EnsureOnboardWorkspaceLayout([]OnboardWorkspaceRoot{root}, os.Getuid()); err == nil {
		t.Fatal("an absent workspace root was accepted")
	}
	if _, err := os.Lstat(root.Root); !os.IsNotExist(err) {
		t.Fatalf("the absent workspace root was created: %v", err)
	}
}
