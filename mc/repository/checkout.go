package repository

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// checkoutState reports whether the operator checkout has the destination
// branch checked out. It refuses a Git operation in progress and a second
// checkout of the destination branch.
func checkoutState(common, branch string) (bool, error) {
	if name := operationInProgress(common); name != "" {
		return false, refuse("A Git %s is in progress in the operator checkout. Finish or stop the %s.", name, name)
	}
	ref := "refs/heads/" + branch
	head, err := os.ReadFile(filepath.Join(common, "HEAD"))
	if err != nil {
		return false, err
	}
	worktrees, err := os.ReadDir(filepath.Join(common, "worktrees"))
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	for _, worktree := range worktrees {
		data, err := os.ReadFile(filepath.Join(common, "worktrees", worktree.Name(), "HEAD"))
		if err != nil {
			return false, err
		}
		if strings.TrimSpace(string(data)) == "ref: "+ref {
			return false, refuse("The branch %s is open in another checkout of the Project repository. Switch that checkout to another branch.", branch)
		}
	}
	return strings.TrimSpace(string(head)) == "ref: "+ref, nil
}

// operationInProgress names the Git operation that the operator started and
// did not finish in the checkout.
func operationInProgress(common string) string {
	exists := func(name string) bool {
		_, err := os.Lstat(filepath.Join(common, name))
		return !os.IsNotExist(err)
	}
	switch {
	case exists("rebase-apply/applying"):
		return "patch application"
	case exists("rebase-merge"), exists("rebase-apply"):
		return "rebase"
	case exists("MERGE_HEAD"):
		return "merge"
	case exists("CHERRY_PICK_HEAD"):
		return "cherry-pick"
	case exists("REVERT_HEAD"):
		return "revert"
	case exists("sequencer"):
		return "cherry-pick or revert"
	case exists("BISECT_LOG"):
		return "bisect"
	}
	return ""
}

// loadIndex copies the operator index to the index of g and refreshes the
// copy. The operator index stays unchanged. An index with unresolved
// conflicts stops the merge.
func loadIndex(ctx context.Context, g Git, common string, c Candidate) error {
	body, err := os.ReadFile(filepath.Join(common, "index"))
	switch {
	case err == nil:
		if err := os.WriteFile(g.index, body, 0600); err != nil {
			return err
		}
	case os.IsNotExist(err):
		if _, err := g.output(ctx, "read-tree", c.DestinationCommit); err != nil {
			return err
		}
	default:
		return err
	}
	unmerged, err := g.command(ctx, nil, "ls-files", "--unmerged", "-z")
	if err != nil {
		return err
	}
	var paths []string
	for _, entry := range strings.Split(string(unmerged), "\x00") {
		if _, path, ok := strings.Cut(entry, "\t"); ok && !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	if len(paths) > 0 {
		return refuse("The operator checkout has unresolved conflicts in %s. Resolve each conflict.", strings.Join(paths, ", "))
	}
	_, err = g.output(ctx, "update-index", "-q", "--refresh")
	return err
}

// mergeRefusal runs the Git two-way merge from the destination commit to the
// candidate as a dry run on the index of g. Git reports one refused path in
// each run, so each refused path takes its candidate entry in the index copy
// before the next run. The refusal names each refused path.
func mergeRefusal(ctx context.Context, g Git, c Candidate) error {
	changed, err := g.ChangedPaths(ctx, c.DestinationCommit, c.Commit)
	if err != nil {
		return err
	}
	candidate, err := treeFiles(ctx, g, c.Commit)
	if err != nil {
		return err
	}
	var refused []string
	for range len(changed) + 1 {
		stderr, err := g.errorText(ctx, "read-tree", "-n", "-u", "-m", c.DestinationCommit, c.Commit)
		if err == nil {
			break
		}
		path := refusedPath(stderr)
		if path == "" && len(refused) == 0 {
			return fmt.Errorf("git read-tree: %w: %s", err, strings.TrimSpace(stderr))
		}
		if path == "" || slices.Contains(refused, path) {
			break
		}
		refused = append(refused, path)
		if !slices.Contains(changed, path) || takeEntry(ctx, g, candidate, path) != nil {
			break
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return refuse("Local changes in the operator checkout prevent the merge into %s: %s. Commit, stash, or discard each of these changes.",
		c.DestinationBranch, strings.Join(refused, ", "))
}

// refusalForms are the Git messages that refuse a merge for one path.
var refusalForms = [][2]string{
	{"Entry '", "' not uptodate. Cannot merge."},
	{"Entry '", "' would be overwritten by merge. Cannot merge."},
	{"Untracked working tree file '", "' would be overwritten by merge."},
	{"Untracked working tree file '", "' would be removed by merge."},
	{"Updating '", "' would lose untracked files in it"},
}

// refusedPath reads the path from the Git refusal of a merge.
func refusedPath(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		message, ok := strings.CutPrefix(line, "error: ")
		if !ok {
			continue
		}
		for _, form := range refusalForms {
			if rest, ok := strings.CutPrefix(message, form[0]); ok {
				if path, ok := strings.CutSuffix(rest, form[1]); ok {
					return path
				}
			}
		}
	}
	return ""
}

// takeEntry gives path its entry of files in the index of g. A path that
// files do not hold leaves the index.
func takeEntry(ctx context.Context, g Git, files map[string]treeFile, path string) error {
	if file, ok := files[path]; ok {
		_, err := g.output(ctx, "update-index", "--add", "--cacheinfo", file.mode+","+file.object+","+path)
		return err
	}
	_, err := g.output(ctx, "update-index", "--force-remove", "--", path)
	return err
}

// errorText runs Git and returns its error output.
func (g Git) errorText(ctx context.Context, args ...string) (string, error) {
	cmd := g.process(ctx, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stderr.String(), err
}
