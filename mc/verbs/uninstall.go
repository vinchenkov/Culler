package verbs

import (
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func UninstallWorkspaceRoots(db *sql.DB) (any, error) {
	rows, err := db.Query(`SELECT DISTINCT workspace_root FROM sandbox_profiles WHERE workspace_root <> '' ORDER BY workspace_root`)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()
	roots := []string{}
	for rows.Next() {
		var root string
		if err := rows.Scan(&root); err != nil {
			return nil, classify(err)
		}
		roots = append(roots, root)
	}
	if err := rows.Err(); err != nil {
		return nil, classify(err)
	}
	return map[string]any{"workspace_roots": roots}, nil
}

// UninstallWorkspaceDirs are the Culler-owned directories inside a workspace.
var UninstallWorkspaceDirs = []string{projectArtifactDir}

const mcBranchPrefix = "mc/"

// WorkspaceCleanup reports exactly what was removed from one workspace, so a
// dry run and a real run can produce the same shape and the operator can read
// the plan before consenting to it.
type WorkspaceCleanup struct {
	Workspace string   `json:"workspace"`
	Removed   []string `json:"removed"`
	Branches  []string `json:"branches"`
	Retained  []string `json:"retained,omitempty"`
	NotARepo  bool     `json:"not_a_repo,omitempty"`
	// Absent marks a registered workspace whose directory is already gone. It is
	// reported rather than warned about — see uninstallWorkspaceRoot.
	Absent bool `json:"absent,omitempty"`
}

func CleanWorkspace(workspaceRoot string, dryRun bool) (WorkspaceCleanup, error) {
	root, absent, err := uninstallWorkspaceRoot(workspaceRoot)
	if err != nil {
		return WorkspaceCleanup{}, err
	}
	report := WorkspaceCleanup{Workspace: root, Removed: []string{}, Branches: []string{}}
	if absent {
		report.Absent = true
		return report, nil
	}

	report.NotARepo = !workspaceIsRepository(root)

	if !report.NotARepo {
		branches, err := mcBranches(root)
		if err != nil {
			return WorkspaceCleanup{}, err
		}
		for _, branch := range branches {
			report.Branches = append(report.Branches, branch)
			if dryRun {
				continue
			}
			// -D, not -d: an unmerged MC branch is the normal case (a
			// cancelled or never-landed task), and a merge check here would
			// refuse exactly the branches most in need of removal.
			if _, err := gitAtSource(root, "branch", "-D", branch); err != nil {
				return WorkspaceCleanup{}, Domainf("delete branch %q in %q: %v", branch, root, err)
			}
		}
	}

	for _, name := range UninstallWorkspaceDirs {
		target := filepath.Join(root, name)
		info, err := os.Lstat(target)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return WorkspaceCleanup{}, Domainf("inspect %q: %v", target, err)
		}
		// A symlink is refused rather than followed: removing it is harmless,
		// but its TARGET is by definition outside the set this verb reasoned
		// about, and RemoveAll on a symlinked directory is the one call here
		// that could reach arbitrary operator data.
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			report.Retained = append(report.Retained, target+" (not a plain directory)")
			continue
		}
		report.Removed = append(report.Removed, target)
		if dryRun {
			continue
		}
		if err := RemoveTree(target); err != nil {
			return WorkspaceCleanup{}, Domainf("remove %q: %v", target, err)
		}
	}
	return report, nil
}

func RemoveTree(root string) error {
	err := os.RemoveAll(root)
	if err == nil {
		return nil
	}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // unreadable subtree: let the retry report it
		}
		if entry.IsDir() {
			_ = os.Chmod(path, 0o700)
		}
		return nil
	})
	return os.RemoveAll(root)
}

func uninstallWorkspaceRoot(workspaceRoot string) (root string, absent bool, err error) {
	if workspaceRoot == "" || !filepath.IsAbs(workspaceRoot) {
		return "", false, Usagef("uninstall needs an absolute workspace path, got %q", workspaceRoot)
	}
	root = filepath.Clean(workspaceRoot)
	if root == "/" || root == filepath.Dir(root) {
		return "", false, Domainf("refusing to clean the filesystem root %q", root)
	}
	info, statErr := os.Lstat(root)
	if statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return root, true, nil
		}
		return "", false, Domainf("workspace %q is unreadable: %v", root, statErr)
	}
	if !info.IsDir() {
		return "", false, Domainf("workspace %q is not a directory", root)
	}
	return root, false, nil
}

// workspaceIsRepository asks Git whether the workspace is a repository. The
// answer is not always `<root>/.git`: a Project can be a worktree or carry a
// `.git` pointer file, so the path is never guessed.
func workspaceIsRepository(root string) bool {
	_, err := gitAtSource(root, "rev-parse", "--git-dir")
	return err == nil
}

func mcBranches(root string) ([]string, error) {
	out, err := gitAtSource(root, "for-each-ref", "--format=%(refname:short)", "refs/heads/"+mcBranchPrefix)
	if err != nil {
		return nil, Domainf("list Culler branches in %q: %v", root, err)
	}
	var branches []string
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		// Belt and braces: for-each-ref already scoped the namespace, but a
		// branch this verb deletes must never depend on a single check.
		if !cullerBranch(name) {
			continue
		}
		branches = append(branches, name)
	}
	return branches, nil
}

// Culler creates task branches with numeric IDs.
func cullerBranch(name string) bool {
	for _, prefix := range []string{"mc/task-"} {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		id := strings.TrimPrefix(name, prefix)
		if id == "" {
			return false
		}
		for _, r := range id {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	return false
}
