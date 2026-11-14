package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func commitAll(t *testing.T, g Git, message string) string {
	t.Helper()
	gitTest(t, g, "add", "-A")
	gitTest(t, g, "commit", "--quiet", "-m", message)
	return gitTest(t, g, "rev-parse", "HEAD")
}

func TestCompareDisclosesEachChangeTypeWithItsOwnPatch(t *testing.T) {
	g, _ := fixture(t)
	writeTest(t, g.Root, "moved.txt", strings.Repeat("a stable line of the moved file\n", 20))
	writeTest(t, g.Root, "removed.txt", "removed\n")
	writeTest(t, g.Root, "script.sh", "echo run\n")
	writeTest(t, g.Root, "link-target.txt", "target\n")
	writeTest(t, g.Root, "kind.txt", "a file that becomes a link\n")
	writeTest(t, g.Root, "image.bin", "\x00\x01\x02 old bytes")
	base := commitAll(t, g, "Create the base")

	writeTest(t, g.Root, "settings.txt", "settings = { limit: 20, label: old }\n")
	writeTest(t, g.Root, "new file ü.txt", "first\nsecond\n")
	writeTest(t, g.Root, "empty.txt", "")
	writeTest(t, g.Root, "image.bin", "\x00\x03\x04 new bytes")
	if err := os.Remove(filepath.Join(g.Root, "removed.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(g.Root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	gitTest(t, g, "mv", "moved.txt", "docs/moved.txt")
	gitTest(t, g, "update-index", "--chmod=+x", "script.sh")
	if err := os.Remove(filepath.Join(g.Root, "kind.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("link-target.txt", filepath.Join(g.Root, "kind.txt")); err != nil {
		t.Fatal(err)
	}
	writeTest(t, g.Root, "large.txt", strings.Repeat("a line that makes the patch larger than the old limit\n", 6000))
	gitTest(t, g, "add", "-A")
	gitTest(t, g, "update-index", "--chmod=+x", "script.sh")
	gitTest(t, g, "commit", "--quiet", "-m", "Change every kind of file")
	head := gitTest(t, g, "rev-parse", "HEAD")

	c, patch, err := g.Compare(context.Background(), base, head)
	if err != nil {
		t.Fatal(err)
	}
	if c.FromCommit != base || c.ToCommit != head || c.PatchBytes != int64(len(patch)) || c.PatchBytes <= 256*1024 {
		t.Fatalf("the comparison lost its identity or bytes: %+v", c)
	}
	sum := sha256.Sum256(patch)
	if c.PatchSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("the patch digest differs from the patch")
	}
	want := map[string]ChangeFile{
		"settings.txt":   {Status: "modified", OldMode: "100644", NewMode: "100644", Additions: 1, Deletions: 1, TextDiff: true},
		"new file ü.txt": {Status: "added", NewMode: "100644", Additions: 2, TextDiff: true},
		"empty.txt":      {Status: "added", NewMode: "100644"},
		"image.bin":      {Status: "modified", OldMode: "100644", NewMode: "100644", Binary: true},
		"removed.txt":    {Status: "deleted", OldMode: "100644", Deletions: 1, TextDiff: true},
		"docs/moved.txt": {Status: "renamed", PreviousPath: "moved.txt", OldMode: "100644", NewMode: "100644", Similarity: 100},
		"script.sh":      {Status: "modified", OldMode: "100644", NewMode: "100755"},
		"kind.txt":       {Status: "type-changed", OldMode: "100644", NewMode: "120000", Additions: 1, Deletions: 1, TextDiff: true},
		"large.txt":      {Status: "added", NewMode: "100644", Additions: 6000, TextDiff: true},
	}
	if len(c.Files) != len(want) {
		t.Fatalf("the inventory holds %d files, want %d: %+v", len(c.Files), len(want), c.Files)
	}
	var joined []byte
	for _, file := range c.Files {
		expected, ok := want[file.Path]
		if !ok {
			t.Fatalf("the inventory holds an unexpected path: %q", file.Path)
		}
		body := patch[file.PatchOffset : file.PatchOffset+file.PatchBytes]
		joined = append(joined, body...)
		expected.Path, expected.PatchOffset, expected.PatchBytes = file.Path, file.PatchOffset, file.PatchBytes
		if file != expected {
			t.Errorf("%s: got %+v, want %+v", file.Path, file, expected)
		}
		if !bytes.HasPrefix(body, []byte("diff --git ")) {
			t.Errorf("%s: the patch part does not start with its header", file.Path)
		}
	}
	if !bytes.Equal(joined, patch) {
		t.Fatal("the patch parts do not cover the complete patch in inventory order")
	}
	var large ChangeFile
	for _, file := range c.Files {
		if file.Path == "large.txt" {
			large = file
		}
	}
	if got := bytes.Count(patch[large.PatchOffset:large.PatchOffset+large.PatchBytes], []byte("\n+a line that")); got != 6000 {
		t.Fatalf("the large patch holds %d added lines, want 6000", got)
	}
}

func TestCompareOfIdenticalCommitsHasNoFiles(t *testing.T) {
	g, commit := fixture(t)
	c, patch, err := g.Compare(context.Background(), commit, commit)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Files) != 0 || len(patch) != 0 || c.PatchBytes != 0 {
		t.Fatalf("an empty comparison holds changes: %+v", c)
	}
}

func TestCompareIgnoresRepositoryDiffConfiguration(t *testing.T) {
	g, base := fixture(t)
	writeTest(t, g.Root, "a.txt", "a\n")
	writeTest(t, g.Root, "b.txt", "b\n")
	head := commitAll(t, g, "Add two files")
	want, wantPatch, err := g.Compare(context.Background(), base, head)
	if err != nil {
		t.Fatal(err)
	}
	writeTest(t, g.Root, "order", "b.txt\na.txt\n")
	for _, setting := range [][2]string{
		{"diff.noprefix", "true"}, {"diff.orderFile", filepath.Join(g.Root, "order")},
		{"diff.renames", "copies"}, {"diff.context", "9"}, {"diff.relative", "true"},
		{"color.diff", "always"}, {"diff.external", "false"},
	} {
		gitTest(t, g, "config", setting[0], setting[1])
	}
	got, gotPatch, err := g.Compare(context.Background(), base, head)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotPatch, wantPatch) || got.PatchSHA256 != want.PatchSHA256 || got.Files[0].Path != "a.txt" {
		t.Fatalf("the repository configuration changed the comparison:\n%s", gotPatch)
	}
}

func TestCompareRefusesAnInexactCommit(t *testing.T) {
	g, commit := fixture(t)
	if _, _, err := g.Compare(context.Background(), "culler", commit); err == nil {
		t.Fatal("the comparison accepted a branch name")
	}
}

func TestPatchRangesRefuseAPatchThatDoesNotMatchTheInventory(t *testing.T) {
	files := []ChangeFile{{Status: "modified", Path: "a"}, {Status: "modified", Path: "b"}}
	if err := addPatchRanges(files, []byte("diff --git a/a b/a\n@@ -1 +1 @@\n-x\n+y\n")); err == nil {
		t.Fatal("one patch part matched two inventory entries")
	}
	if err := addPatchRanges(files[:1], []byte("preamble\ndiff --git a/a b/a\n")); err == nil {
		t.Fatal("the patch accepted text before the first part")
	}
}
