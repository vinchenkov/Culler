package repository

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// submoduleChange builds two commits. The first commit holds the file
// .gitmodules and the submodule entry vendor/dep. The second commit moves
// vendor/dep to another commit, adds the submodule entry added/mod, and adds
// one wiki file.
func submoduleChange(t *testing.T, modules string) (g Git, before, after string) {
	t.Helper()
	g = Git{Root: t.TempDir()}
	gitTest(t, g, "init", "--quiet", "-b", "main")
	gitTest(t, g, "config", "user.name", "Test Author")
	gitTest(t, g, "config", "user.email", "test@example.invalid")
	entry := func(commit, path string) {
		gitTest(t, g, "update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat(commit, 40)+","+path)
	}
	writeTest(t, g.Root, ".gitmodules", modules)
	gitTest(t, g, "add", ".gitmodules")
	entry("a", "vendor/dep")
	gitTest(t, g, "commit", "--quiet", "-m", "Add a submodule")
	before = gitTest(t, g, "rev-parse", "HEAD")
	if err := os.Mkdir(filepath.Join(g.Root, "openwiki"), 0700); err != nil {
		t.Fatal(err)
	}
	writeTest(t, g.Root, "openwiki/index.md", "# Project wiki\n")
	gitTest(t, g, "add", "openwiki/index.md")
	entry("b", "vendor/dep")
	entry("c", "added/mod")
	gitTest(t, g, "commit", "--quiet", "-m", "Change each submodule entry")
	return g, before, gitTest(t, g, "rev-parse", "HEAD")
}

// A scope check reads the changed paths in a repository that an agent can
// configure. No configuration can remove a submodule entry from the list.
func TestChangedPathsListsEachSubmoduleChange(t *testing.T) {
	const modules = "[submodule \"dep\"]\n\tpath = vendor/dep\n\turl = https://example.invalid/dep.git\n"
	want := []string{"added/mod", "openwiki/index.md", "vendor/dep"}
	for _, c := range []struct {
		name, modules string
		config        []string
	}{
		{"no configuration", modules, nil},
		{"diff.ignoreSubmodules in the repository configuration", modules, []string{"diff.ignoreSubmodules", "all"}},
		{"submodule.<name>.ignore in the repository configuration", modules, []string{"submodule.dep.ignore", "all"}},
		{"ignore in the committed .gitmodules file", modules + "\tignore = all\n", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			g, before, after := submoduleChange(t, c.modules)
			if c.config != nil {
				gitTest(t, g, append([]string{"config"}, c.config...)...)
			}
			got, err := g.ChangedPaths(context.Background(), before, after)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("changed paths = %v, %v; want %v", got, err, want)
			}
		})
	}
}

// The Files changed view reads each version of a changed file at its exact
// commit. Only a regular file within the byte limit is readable.
func TestReadFileReadsTheRegularFileAtAnExactCommit(t *testing.T) {
	g, before := fixture(t)
	if err := os.MkdirAll(filepath.Join(g.Root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	writeTest(t, g.Root, "settings.txt", "settings = { limit: 20, label: new }\n")
	writeTest(t, g.Root, "docs/guide.md", "# Guide\n")
	if err := os.Symlink("settings.txt", filepath.Join(g.Root, "link")); err != nil {
		t.Fatal(err)
	}
	after := commitAll(t, g, "Change the settings")
	ctx := context.Background()
	for _, c := range []struct{ commit, name, want string }{
		{before, "settings.txt", "settings = { limit: 10, label: old }\n"},
		{after, "settings.txt", "settings = { limit: 20, label: new }\n"},
		{after, "docs/guide.md", "# Guide\n"},
	} {
		body, err := g.ReadFile(ctx, c.commit, c.name, 1024)
		if err != nil || string(body) != c.want {
			t.Fatalf("ReadFile(%s, %s) = %q, %v; want %q", c.commit[:7], c.name, body, err, c.want)
		}
	}
	for _, c := range []struct {
		commit, name string
		limit        int64
		want         string
	}{
		{before, "docs/guide.md", 1024, "the commit holds no file at docs/guide.md"},
		{after, "docs", 1024, "the commit holds no file at docs"},
		{after, "link", 1024, "the repository file must contain regular file data: link"},
		{after, "settings.txt", 8, "the repository file exceeds the byte limit: settings.txt"},
		{after, "../settings.txt", 1024, "the repository read requires an exact commit and a relative path"},
		{after, "/settings.txt", 1024, "the repository read requires an exact commit and a relative path"},
		{"main", "settings.txt", 1024, "the repository read requires an exact commit and a relative path"},
	} {
		if _, err := g.ReadFile(ctx, c.commit, c.name, c.limit); err == nil || err.Error() != c.want {
			t.Fatalf("ReadFile(%s, %s) gave %v; want %q", c.commit, c.name, err, c.want)
		}
	}
}
