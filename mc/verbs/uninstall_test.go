package verbs

// These tests judge CleanWorkspace by its real external consumer: an actual Git
// repository on disk, carrying both Culler's objects and an operator's own.
//
// The load-bearing case is TestCleanWorkspaceKeepsAnOperatorWorktreeOutside:
// a `git worktree prune` destroys operator worktrees that live outside
// `workspace_root`, and that is exactly the shortcut a later change might
// reach for. The test builds that worktree for real and proves it still works
// afterwards.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func uninstallGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@localhost",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@localhost",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// uninstallFixture builds a workspace that carries every object uninstall must
// remove and every object it must not touch.
func uninstallFixture(t *testing.T) (workspace string, operatorWorktree string) {
	t.Helper()
	base := t.TempDir()
	workspace = filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	uninstallGit(t, workspace, "init", "-q", "-b", "main")
	uninstallGit(t, workspace, "config", "--local", "user.name", "t")
	uninstallGit(t, workspace, "config", "--local", "user.email", "t@localhost")
	uninstallGit(t, workspace, "config", "--local", "worktree.useRelativePaths", "true")
	if err := os.WriteFile(filepath.Join(workspace, "README"), []byte("operator work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	uninstallGit(t, workspace, "add", "README")
	uninstallGit(t, workspace, "commit", "-qm", "initial")

	// The operator's own branch and their own worktree, deliberately OUTSIDE
	// workspace_root: the prune hazard, built for real.
	uninstallGit(t, workspace, "branch", "feature/operator-work")
	uninstallGit(t, workspace, "branch", "mc/custom")
	operatorWorktree = filepath.Join(base, "operator-worktree")
	uninstallGit(t, workspace, "worktree", "add", "-q", operatorWorktree, "feature/operator-work")

	// Culler's branches.
	uninstallGit(t, workspace, "branch", "mc/task-1")

	// The artifact root and its contents.
	packet := filepath.Join(workspace, ".mc-artifacts", "3", "packet")
	if err := os.MkdirAll(packet, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packet, "packet.html"), []byte("<html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The exclude literal onboarding writes and uninstall must leave alone.
	excludes := filepath.Join(workspace, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(excludes), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(excludes, []byte(".mc-artifacts/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return workspace, operatorWorktree
}

func TestCleanWorkspaceRemovesEveryCullerDirectory(t *testing.T) {
	workspace, _ := uninstallFixture(t)
	if _, err := CleanWorkspace(workspace, false); err != nil {
		t.Fatalf("CleanWorkspace: %v", err)
	}
	for _, name := range UninstallWorkspaceDirs {
		if _, err := os.Lstat(filepath.Join(workspace, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived uninstall (err=%v)", name, err)
		}
	}
	// The operator's own tracked file is untouched.
	if _, err := os.Stat(filepath.Join(workspace, "README")); err != nil {
		t.Errorf("operator file did not survive: %v", err)
	}
}

func TestCleanWorkspaceRemovesOnlyCullerBranches(t *testing.T) {
	workspace, _ := uninstallFixture(t)
	if _, err := CleanWorkspace(workspace, false); err != nil {
		t.Fatalf("CleanWorkspace: %v", err)
	}
	branches := uninstallGit(t, workspace, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	for _, gone := range []string{"mc/task-1"} {
		if strings.Contains(branches, gone) {
			t.Errorf("branch %s survived uninstall; have %q", gone, branches)
		}
	}
	for _, kept := range []string{"main", "feature/operator-work", "mc/custom"} {
		if !strings.Contains(branches, kept) {
			t.Errorf("operator branch %s was destroyed; have %q", kept, branches)
		}
	}
}

// A `git worktree prune` would deregister the operator's worktree. Uninstall
// must never touch the worktree registrations of the operator.
func TestCleanWorkspaceKeepsAnOperatorWorktreeOutside(t *testing.T) {
	workspace, operatorWorktree := uninstallFixture(t)
	if _, err := CleanWorkspace(workspace, false); err != nil {
		t.Fatalf("CleanWorkspace: %v", err)
	}
	// Still registered...
	list := uninstallGit(t, workspace, "worktree", "list")
	if !strings.Contains(list, operatorWorktree) {
		t.Fatalf("operator worktree was deregistered; worktree list:\n%s", list)
	}
	// ...and still functional, which is the property the operator actually
	// cares about: a surviving registration pointing at a broken checkout
	// would pass a weaker assertion.
	if head := uninstallGit(t, operatorWorktree, "rev-parse", "--abbrev-ref", "HEAD"); head != "feature/operator-work" {
		t.Fatalf("operator worktree is broken: HEAD=%q", head)
	}
	if _, err := os.Stat(filepath.Join(operatorWorktree, "README")); err != nil {
		t.Fatalf("operator worktree checkout is gone: %v", err)
	}
}

// The Git exclude entry and the Git settings of the Project stay in place.
// Uninstall cannot prove that a setting is Culler's, so it leaves them alone.
func TestCleanWorkspaceLeavesGitExcludesAndSettings(t *testing.T) {
	workspace, _ := uninstallFixture(t)
	if _, err := CleanWorkspace(workspace, false); err != nil {
		t.Fatalf("CleanWorkspace: %v", err)
	}
	excludes, err := os.ReadFile(filepath.Join(workspace, ".git", "info", "exclude"))
	if err != nil {
		t.Fatalf("read exclude: %v", err)
	}
	if !strings.Contains(string(excludes), ".mc-artifacts/") {
		t.Errorf("exclude literal %q was removed; uninstall must leave it", ".mc-artifacts/")
	}
	if got := uninstallGit(t, workspace, "config", "--local", "--get", "worktree.useRelativePaths"); got != "true" {
		t.Errorf("worktree.useRelativePaths = %q, want it left at true", got)
	}
}

func TestCleanWorkspaceDryRunRemovesNothing(t *testing.T) {
	workspace, _ := uninstallFixture(t)
	report, err := CleanWorkspace(workspace, true)
	if err != nil {
		t.Fatalf("CleanWorkspace dry run: %v", err)
	}
	if len(report.Removed) != 1 || len(report.Branches) != 1 {
		t.Fatalf("dry run must still report the full plan, got %+v", report)
	}
	for _, name := range UninstallWorkspaceDirs {
		if _, err := os.Stat(filepath.Join(workspace, name)); err != nil {
			t.Errorf("dry run deleted %s: %v", name, err)
		}
	}
	branches := uninstallGit(t, workspace, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if !strings.Contains(branches, "mc/task-1") {
		t.Errorf("dry run deleted a branch; have %q", branches)
	}
}

// A symlink is the one path where RemoveAll could reach arbitrary operator data.
func TestCleanWorkspaceRefusesToFollowASymlinkedPlane(t *testing.T) {
	workspace, _ := uninstallFixture(t)
	if err := os.RemoveAll(filepath.Join(workspace, ".mc-artifacts")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "precious")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, ".mc-artifacts")); err != nil {
		t.Fatal(err)
	}
	if _, err := CleanWorkspace(workspace, false); err != nil {
		t.Fatalf("CleanWorkspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatalf("uninstall followed a symlink out of the workspace: %v", err)
	}
}

func TestCleanWorkspaceHandlesANonRepoWorkspace(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".mc-artifacts"), 0o700); err != nil {
		t.Fatal(err)
	}
	report, err := CleanWorkspace(workspace, false)
	if err != nil {
		t.Fatalf("CleanWorkspace on a non-repo: %v", err)
	}
	if !report.NotARepo {
		t.Errorf("expected the non-repo signal")
	}
	if _, err := os.Stat(filepath.Join(workspace, ".mc-artifacts")); !os.IsNotExist(err) {
		t.Errorf(".mc-artifacts survived on a non-repo workspace")
	}
}

func TestCleanWorkspaceRefusesNonsenseRoots(t *testing.T) {
	for _, bad := range []string{"", "relative/path", "/"} {
		if _, err := CleanWorkspace(bad, true); err == nil {
			t.Errorf("CleanWorkspace(%q) was accepted", bad)
		}
	}
}

func TestCleanWorkspaceReportsAnAlreadyDeletedWorkspaceAsAbsent(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "workspace-the-smoke-deleted")
	report, err := CleanWorkspace(missing, true)
	if err != nil {
		t.Fatalf("a workspace that is already gone was refused: %v", err)
	}
	if !report.Absent {
		t.Errorf("expected the absent signal, got %+v", report)
	}
	if report.Workspace != missing {
		t.Errorf("report names %q, want the registered root %q", report.Workspace, missing)
	}
	if len(report.Removed) != 0 || len(report.Branches) != 0 {
		t.Errorf("an absent workspace claimed removals: %+v", report)
	}
}

// The other half, and the reason absence is not simply "any unreadable root is
// fine": a workspace behind a permission wall may still hold the artifact root
// Culler planted, so reporting it clean would be a false all-clear.
// Only ErrNotExist is the designed end state.
func TestCleanWorkspaceStillRefusesAWorkspaceItCannotRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root traverses a 0000 directory, so the permission wall cannot be built")
	}
	parent := filepath.Join(t.TempDir(), "wall")
	workspace := filepath.Join(parent, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	report, err := CleanWorkspace(workspace, true)
	if err == nil {
		t.Fatalf("an unreadable workspace was accepted as %+v", report)
	}
	if report.Absent {
		t.Errorf("an unreadable workspace was reported absent")
	}
}

// A directory without its write bit refuses unlinkat for everything inside
// it, so plain os.RemoveAll fails with "permission denied" on a tree the
// operator fully owns. An agent can leave such a directory inside the artifact
// root. The shape is reproduced exactly: a 0555 directory owned by this
// process.
func TestRemoveTreeDeletesReadOnlyDirectories(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	sealed := filepath.Join(root, ".mc-artifacts", "3")
	for _, dir := range []string{filepath.Join(sealed, "packet"), filepath.Join(sealed, "evidence")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "content"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(sealed, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o700) })

	// The defect, pinned: without the repair this is exactly what happens.
	if err := os.RemoveAll(root); err == nil {
		t.Fatal("os.RemoveAll unexpectedly succeeded; this fixture no longer reproduces the failure")
	}
	if err := RemoveTree(root); err != nil {
		t.Fatalf("RemoveTree: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("tree survived RemoveTree: %v", err)
	}
}

// The same shape reached through the real consumer.
func TestCleanWorkspaceRemovesReadOnlyArtifactDirectories(t *testing.T) {
	workspace, _ := uninstallFixture(t)
	sealed := filepath.Join(workspace, ".mc-artifacts", "3")
	if err := os.Chmod(sealed, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o700) })

	if _, err := CleanWorkspace(workspace, false); err != nil {
		t.Fatalf("CleanWorkspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".mc-artifacts")); !os.IsNotExist(err) {
		t.Fatalf(".mc-artifacts survived: %v", err)
	}
}
