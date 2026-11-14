package repository

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTreeIDNamesOnlyATreeAtTheExactPath(t *testing.T) {
	g, first := fixture(t)
	ctx := context.Background()
	if tree, err := g.TreeID(ctx, first, "openwiki"); err != nil || tree != "" {
		t.Fatalf("absent folder: tree %q, error %v", tree, err)
	}
	if err := os.MkdirAll(filepath.Join(g.Root, "openwiki", "guide"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTest(t, g.Root, "openwiki/index.md", "# Index\n")
	writeTest(t, g.Root, "openwiki/guide/start.md", "# Start\n")
	writeTest(t, g.Root, "openwiki-notes", "A file with a similar name.\n")
	gitTest(t, g, "add", ".")
	gitTest(t, g, "commit", "--quiet", "-m", "Add the wiki")
	second := gitTest(t, g, "rev-parse", "HEAD")
	want := gitTest(t, g, "rev-parse", second+":openwiki")
	if tree, err := g.TreeID(ctx, second, "openwiki"); err != nil || tree != want {
		t.Fatalf("wiki folder: tree %q, error %v, want %q", tree, err, want)
	}
	if tree, err := g.TreeID(ctx, second, "settings.txt"); err != nil || tree != "" {
		t.Fatalf("a file is not a tree: tree %q, error %v", tree, err)
	}
	for _, name := range []string{"", "..", "/openwiki", "../openwiki", "openwiki/../settings.txt"} {
		if _, err := g.TreeID(ctx, second, name); err == nil {
			t.Fatalf("the reader accepted the path %q", name)
		}
	}
	if _, err := g.TreeID(ctx, "culler", "openwiki"); err == nil {
		t.Fatal("the reader accepted a branch name as the commit")
	}
}
