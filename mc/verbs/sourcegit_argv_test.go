package verbs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The ownership grant that keeps source reads alive in containers (see the
// safeDirectoryArg comment) is only correct while it stays per-invocation and
// absolute. These guards pin the argv shape at the seam this file documents
// for exactly that purpose: a fake git that records its own arguments.

// injectArgvGit points sourceGitBin at a script that records one argument per
// line into a capture file, so any read through any caller can be inspected
// without a repository, including callers that discard stdout.
func injectArgvGit(t *testing.T) func() []string {
	t.Helper()
	dir := t.TempDir()
	capture := filepath.Join(dir, "argv.txt")
	script := filepath.Join(dir, "argv-git.sh")
	body := "#!/bin/sh\n: > " + shellQuote(capture) + "\nfor arg in \"$@\"; do printf '%s\\n' \"$arg\" >> " + shellQuote(capture) + "\ndone\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	previous := sourceGitBin
	sourceGitBin = script
	t.Cleanup(func() { sourceGitBin = previous })
	return func() []string {
		recorded, err := os.ReadFile(capture)
		if err != nil {
			t.Fatalf("the fake git recorded nothing: %v", err)
		}
		return strings.Split(strings.TrimSuffix(string(recorded), "\n"), "\n")
	}
}

func shellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

func assertOwnershipGrant(t *testing.T, lines []string, wantRoot string) {
	t.Helper()
	if len(lines) < 3 {
		t.Fatalf("argv too short to carry a grant: %q", lines)
	}
	if lines[0] != "-c" || !strings.HasPrefix(lines[1], "safe.directory=") {
		t.Fatalf("the grant must be the first argv pair, ahead of the subcommand: %q", lines)
	}
	value := strings.TrimPrefix(lines[1], "safe.directory=")
	if value == "*" || value == "" {
		t.Fatalf("the grant must name one repository, got %q", value)
	}
	if !filepath.IsAbs(value) {
		t.Fatalf("the grant must be absolute — Git warns and ignores relative forms: %q", value)
	}
	if filepath.Clean(value) != filepath.Clean(wantRoot) {
		t.Fatalf("the grant names %q, want the repository this call reads %q", value, wantRoot)
	}
}

func TestGitAtSourceGrantsItsOneRepositoryAbsolutely(t *testing.T) {
	recorded := injectArgvGit(t)
	root := t.TempDir()

	if _, err := gitAtSource(root, "rev-parse", "--verify", "HEAD^{commit}"); err != nil {
		t.Fatal(err)
	}
	lines := recorded()
	assertOwnershipGrant(t, lines, root)
	if lines[2] != "rev-parse" {
		t.Fatalf("the real subcommand must follow the grant pair: %q", lines)
	}

	// A relative caller is granted as the absolute path Git would accept;
	// the grant never travels in relative form.
	if _, err := gitAtSource(".", "status", "--porcelain"); err != nil {
		t.Fatal(err)
	}
	lines = recorded()
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	assertOwnershipGrant(t, lines, here)
}

// The host-side mutation caller bypasses gitAtSource for its own reasons; it
// must carry the same grant or the product keeps two git read paths with two
// behaviors — the exact inconsistency that hid DEFECT-004's other half.
func TestHostMutationCarriesTheSameGrant(t *testing.T) {
	recorded := injectArgvGit(t)
	root := t.TempDir()
	if err := hostOnboardGitMutation(root, "config", "core.quotePath", "false"); err != nil {
		t.Fatal(err)
	}
	assertOwnershipGrant(t, recorded(), root)
}

// The read-only status probe builds its own environment (it adds
// GIT_OPTIONAL_LOCKS=0), so it bypasses the choke by design. A source guard
// keeps it honest: both non-choke gitOutput calls in this file must run
// through safeDirectoryArg, and the count may not grow quietly.
func TestProjectGitBypassesAllCarryTheGrant(t *testing.T) {
	raw, err := os.ReadFile("onboardprojectgit.go")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	direct := 0
	granted := 0
	for _, line := range lines {
		if strings.Contains(line, "gitOutput(") {
			direct++
			if strings.Contains(line, "safeDirectoryArg(") {
				granted++
			}
		}
	}
	if direct == 0 {
		t.Fatal("gitOutput disappeared from onboardprojectgit.go; rewrite this guard against whatever replaced it")
	}
	if direct != granted {
		t.Fatalf("%d direct gitOutput calls but only %d carry the ownership grant", direct, granted)
	}
}
