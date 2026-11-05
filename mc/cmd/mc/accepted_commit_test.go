package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// acGit runs one git command in a fixture repository with a fixed author.
func acGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root,
		"-c", "user.name=Fixture", "-c", "user.email=fixture@example.test",
		"-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v (%s)", args, root, err, out)
	}
	return strings.TrimSpace(string(out))
}

// acRepository creates a repository with one commit on the branch. It returns
// the root and the commit.
func acRepository(t *testing.T, branch string) (string, string) {
	t.Helper()
	root := t.TempDir()
	acGit(t, root, "init", "-q", "-b", branch)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	acGit(t, root, "add", "-A")
	acGit(t, root, "commit", "-q", "-m", "Start the fixture")
	return root, acGit(t, root, "rev-parse", "HEAD")
}

func TestAcceptedCommitAt(t *testing.T) {
	root, commit := acRepository(t, "main")
	if got := acceptedCommitAt(root); got != commit {
		t.Fatalf("accepted commit of a repository with main = %q, want %q", got, commit)
	}
	other, _ := acRepository(t, "work")
	if got := acceptedCommitAt(other); got != "" {
		t.Fatalf("accepted commit of a repository with no main = %q, want none", got)
	}
	if got := acceptedCommitAt(t.TempDir()); got != "" {
		t.Fatalf("accepted commit of a folder with no repository = %q, want none", got)
	}
	if got := acceptedCommitAt(""); got != "" {
		t.Fatalf("accepted commit of an empty root = %q, want none", got)
	}

	// A new commit on main is the next observation.
	if err := os.WriteFile(filepath.Join(root, "next.md"), []byte("next\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	acGit(t, root, "add", "-A")
	acGit(t, root, "commit", "-q", "-m", "Move main")
	next := acGit(t, root, "rev-parse", "HEAD")
	if got := acceptedCommitAt(root); got != next || next == commit {
		t.Fatalf("accepted commit after a new commit = %q, want %q", got, next)
	}
}

// Two repositories give no answer. With one shared time limit the call returns
// after one limit and holds the commit of each repository that answered.
func TestWikiObservationsShareOneTimeLimit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	first, firstCommit := acRepository(t, "main")
	second, secondCommit := acRepository(t, "main")
	silent, late := t.TempDir(), t.TempDir()

	stubDir := t.TempDir()
	script := "#!/bin/sh\n" +
		"if [ \"$2\" = \"" + silent + "\" ] || [ \"$2\" = \"" + late + "\" ]; then exec sleep 30; fi\n" +
		"exec \"" + git + "\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(stubDir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The first Git start with a new PATH can be slow. This start has no time
	// limit and precedes the measured call.
	acGit(t, first, "rev-parse", "HEAD")
	limit := acceptedCommitLimit
	acceptedCommitLimit = 1500 * time.Millisecond
	t.Cleanup(func() { acceptedCommitLimit = limit })

	start := time.Now()
	got := acceptedCommitsAt(map[string]string{"first": first, "silent": silent, "second": second, "late": late, "none": ""})
	elapsed := time.Since(start)

	want := map[string]string{"first": firstCommit, "silent": "", "second": secondCommit, "late": "", "none": ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("observations = %v, want %v", got, want)
	}
	if elapsed >= 2*acceptedCommitLimit {
		t.Fatalf("the read of each root took %s; two silent roots must share the limit of %s", elapsed, acceptedCommitLimit)
	}
}
