package repository

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ancestorHistory builds the history start <- middle <- last and one commit with no parent.
func ancestorHistory(t *testing.T) (g Git, start, middle, last, unrelated string) {
	t.Helper()
	g, start = fixture(t)
	commit := func(file string) string {
		writeTest(t, g.Root, file, file+"\n")
		gitTest(t, g, "add", ".")
		gitTest(t, g, "commit", "--quiet", "-m", "Add "+file)
		return gitTest(t, g, "rev-parse", "HEAD")
	}
	middle, last = commit("middle.txt"), commit("last.txt")
	gitTest(t, g, "checkout", "--quiet", "--orphan", "unrelated")
	gitTest(t, g, "commit", "--quiet", "--allow-empty", "-m", "Unrelated history")
	unrelated = gitTest(t, g, "rev-parse", "HEAD")
	gitTest(t, g, "checkout", "--quiet", "main")
	return g, start, middle, last, unrelated
}

// IsAncestor gives an answer for each pair of commits and an error for a Git failure.
func TestIsAncestorSeparatesAnAnswerFromAFailure(t *testing.T) {
	ctx := context.Background()
	g, start, middle, last, unrelated := ancestorHistory(t)
	blob := gitTest(t, g, "rev-parse", last+":last.txt")
	absent := strings.Repeat("2", 40)
	for _, c := range []struct {
		name, base, commit string
		want               bool
	}{
		{"an ancestor", start, last, true},
		{"the same commit", last, last, true},
		{"a commit outside the history", start, unrelated, false},
		{"a later commit as the base", last, start, false},
		{"a base that the repository does not hold", absent, last, false},
		{"a commit that the repository does not hold", start, absent, false},
		{"a base that is no commit", blob, last, false},
		{"an identifier that is no object name", "HEAD", last, false},
	} {
		got, err := g.IsAncestor(ctx, c.base, c.commit)
		if err != nil || got != c.want {
			t.Errorf("%s = %v, %v; want %v with no error", c.name, got, err, c.want)
		}
		if old := g.Ancestor(ctx, c.base, c.commit); old != c.want {
			t.Errorf("%s: Ancestor = %v, want %v", c.name, old, c.want)
		}
	}

	// A damaged commit object between the two commits: Git gives no answer.
	name := filepath.Join(g.Root, ".git", "objects", middle[:2], middle[2:])
	if err := os.Chmod(name, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte("damaged object data"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := g.IsAncestor(ctx, start, last); err == nil || got {
		t.Fatalf("a damaged commit object = %v, %v; want an error", got, err)
	}
	if got, err := g.IsAncestor(ctx, middle, last); err == nil || got {
		t.Fatalf("a damaged base = %v, %v; want an error", got, err)
	}

	// A Git process that fails gives no answer.
	g, start, _, last, _ = ancestorHistory(t)
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()
	body := "#!/bin/sh\nfor argument in \"$@\"; do\n  if [ \"$argument\" = merge-base ]; then\n    echo 'fatal: the process failed' >&2\n    exit 128\n  fi\ndone\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(folder, "git"), []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", folder+string(os.PathListSeparator)+os.Getenv("PATH"))
	if got, err := g.IsAncestor(ctx, start, last); err == nil || got {
		t.Fatalf("a failed Git process = %v, %v; want an error", got, err)
	}
	t.Setenv("PATH", t.TempDir())
	if got, err := g.IsAncestor(ctx, start, last); err == nil || got {
		t.Fatalf("an absent Git program = %v, %v; want an error", got, err)
	}
}
