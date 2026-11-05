package verbs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"mc/boundary"
)

// The authoritative Git control registry resolves a registered repo
// Project's administrative identities live at attest. It caches no
// jurisdiction input, reads no stored table, and never calls an
// operator-installed host Git.

func grWorkspace(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(dir, "ws")
	if err := os.Mkdir(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	return ws
}

func grCanonicals(ids []boundary.ProtectedID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.Canonical)
	}
	return out
}

func grMountCode(t *testing.T, err error) string {
	t.Helper()
	var me *boundary.MountError
	if !errors.As(err, &me) {
		t.Fatalf("want *boundary.MountError, got %v", err)
	}
	return me.Code
}

func TestGitRegistryResolvesDirectoryControl(t *testing.T) {
	ws := grWorkspace(t)
	gitDir := filepath.Join(ws, ".git")
	if err := os.Mkdir(gitDir, 0o700); err != nil {
		t.Fatal(err)
	}

	controls, err := resolveProjectGitControls(ws)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(controls) != 1 {
		t.Fatalf("want one control, got %v", grCanonicals(controls))
	}
	if controls[0].Canonical != gitDir {
		t.Fatalf("control canonical = %q, want %q", controls[0].Canonical, gitDir)
	}
	if !controls[0].Present() || !controls[0].IsDir {
		t.Fatalf("a real .git directory must resolve present as a directory")
	}
}

func TestGitRegistryEncodesAbsentControlAsMember(t *testing.T) {
	ws := grWorkspace(t)

	controls, err := resolveProjectGitControls(ws)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(controls) != 1 {
		t.Fatalf("want the absent .git member, got %v", grCanonicals(controls))
	}
	want := filepath.Join(ws, ".git")
	if controls[0].Canonical != want || controls[0].Present() {
		t.Fatalf("absent .git must stay a protected member: got %+v", controls[0])
	}
}

func TestGitRegistryProtectsBareRepositoryRoot(t *testing.T) {
	ws := grWorkspace(t)
	for _, dir := range []string{"objects", "refs"} {
		if err := os.Mkdir(filepath.Join(ws, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ws, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	controls, err := resolveProjectGitControls(ws)
	if err != nil {
		t.Fatalf("resolve bare repository: %v", err)
	}
	if len(controls) != 1 || controls[0].Canonical != ws || !controls[0].Present() || !controls[0].IsDir {
		t.Fatalf("bare repository root must be the protected control identity, got %+v", controls)
	}
}

func TestGitRegistryChasesWorktreePointerInsideWorkspace(t *testing.T) {
	ws := grWorkspace(t)
	// A linked-worktree checkout: .git is a regular file whose gitdir points at
	// the administrative directory; commondir points from there to the shared
	// control root.
	gitdir := filepath.Join(ws, "repo-admin", "worktrees", "wt1")
	common := filepath.Join(ws, "repo-admin")
	if err := os.MkdirAll(gitdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".git"), []byte("gitdir: repo-admin/worktrees/wt1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	controls, err := resolveProjectGitControls(ws)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got := map[string]bool{}
	for _, id := range controls {
		got[id.Canonical] = true
	}
	for _, want := range []string{filepath.Join(ws, ".git"), gitdir, common} {
		if !got[want] {
			t.Fatalf("controls %v miss %q", grCanonicals(controls), want)
		}
	}
}

func TestGitRegistryRegistersExternalPointerTarget(t *testing.T) {
	ws := grWorkspace(t)
	outside := filepath.Join(filepath.Dir(ws), "elsewhere-admin")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".git"), []byte("gitdir: "+outside+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	controls, err := resolveProjectGitControls(ws)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got := map[string]bool{}
	for _, id := range controls {
		got[id.Canonical] = true
	}
	if !got[outside] {
		t.Fatalf("an external administrative identity is still a registered control: %v", grCanonicals(controls))
	}
}

func TestGitRegistryRefusesSymlinkControl(t *testing.T) {
	ws := grWorkspace(t)
	real := filepath.Join(filepath.Dir(ws), "real-git")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(ws, ".git")); err != nil {
		t.Fatal(err)
	}

	_, err := resolveProjectGitControls(ws)
	if code := grMountCode(t, err); code != boundary.CodeSourceWrongKind {
		t.Fatalf("symlinked .git must refuse wrong-kind, got %q", code)
	}
}

func TestGitRegistryRefusesUnparsablePointer(t *testing.T) {
	for name, body := range map[string]string{
		"no-prefix":  "worktree: /somewhere\n",
		"multi-line": "gitdir: a\ngitdir: b\n",
		"empty":      "",
	} {
		t.Run(name, func(t *testing.T) {
			ws := grWorkspace(t)
			if err := os.WriteFile(filepath.Join(ws, ".git"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := resolveProjectGitControls(ws)
			if code := grMountCode(t, err); code != boundary.CodeSourceWrongKind {
				t.Fatalf("unparsable pointer must refuse wrong-kind, got %q", code)
			}
		})
	}
}

func TestGitRegistryRefusesOversizedPointer(t *testing.T) {
	ws := grWorkspace(t)
	big := make([]byte, maxGitPointerBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(ws, ".git"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := resolveProjectGitControls(ws)
	if code := grMountCode(t, err); code != boundary.CodeSourceWrongKind {
		t.Fatalf("oversized pointer must refuse wrong-kind, got %q", code)
	}
}

func TestGitRegistryRefusesDanglingPointerTarget(t *testing.T) {
	ws := grWorkspace(t)
	if err := os.WriteFile(filepath.Join(ws, ".git"), []byte("gitdir: gone/away\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := resolveProjectGitControls(ws)
	if code := grMountCode(t, err); code != boundary.CodeSourceMissing {
		t.Fatalf("a dangling administrative pointer is ambiguity and denies, got %q", code)
	}
}

func TestGitRegistryEmptyWorkspaceRootRegistersNothing(t *testing.T) {
	controls, err := resolveProjectGitControls("")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(controls) != 0 {
		t.Fatalf("no declared root means no controls to register, got %v", grCanonicals(controls))
	}
}
