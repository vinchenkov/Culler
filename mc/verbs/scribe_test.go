package verbs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScribeRepositoryPreparesItsRestrictedMounts(t *testing.T) {
	f := newDeliveryFixture(t)
	// The start commit holds no wiki, so the ready step must create the wiki directory.
	srcGit(t, f.workspace, "rm", "-rq", "openwiki")
	srcGit(t, f.workspace, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "Use a Project with no wiki")
	commit := srcGit(t, f.workspace, "rev-parse", "HEAD")
	run := exScribe
	root := f.wikiRoot(run)
	_, err := executeRepository(context.Background(), RepositoryEnvelope{Operation: "ready", WikiUpdate: run, RunID: run, WorkspaceRoot: f.workspace, RunRoot: root, InputCommit: commit, DestinationBranch: "culler", WikiOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"openwiki", ".claude", ".git"} {
		info, err := os.Lstat(filepath.Join(root, "repo", name))
		if err != nil || !info.IsDir() {
			t.Fatal(name, info, err)
		}
	}
	if srcGit(t, filepath.Join(root, "repo"), "rev-parse", "HEAD") != commit {
		t.Fatal("the mount preparation changed the input commit")
	}
}
