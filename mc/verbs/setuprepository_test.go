package verbs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupProject returns a Project repository whose culler branch starts at main
// and the setup folder of one initial setup.
func setupProject(t *testing.T) (root, main, setupRoot string) {
	t.Helper()
	root, main = mainRepository(t)
	srcGit(t, root, "branch", "culler", main)
	return root, main, filepath.Join(t.TempDir(), "environment-runs", "setup-s1")
}

func setupOperation(t *testing.T, operation, root, setupRoot, commit string) SetupRepositoryResult {
	t.Helper()
	result, err := executeSetupRepository(context.Background(), SetupRepositoryEnvelope{
		Operation: operation, SetupID: "s1", WorkspaceRoot: root, SetupRoot: setupRoot, Commit: commit})
	if err != nil {
		t.Fatalf("%s: %v", operation, err)
	}
	return result
}

// commitSetupFiles commits files in the temporary repository as source
// acceptance does.
func commitSetupFiles(t *testing.T, setupRoot string, files map[string]string) string {
	t.Helper()
	source := filepath.Join(setupRoot, "source")
	for name, body := range files {
		writeFile(t, filepath.Join(source, name), body)
	}
	srcGit(t, source, "add", "-A", "--", ".")
	srcGit(t, source, "-c", "commit.gpgSign=false", "commit", "--quiet", "-m", "Initialize the Project development environment")
	return srcGit(t, source, "rev-parse", "HEAD")
}

func readText(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestSetupRepositoryStartsAtTheCullerBranchWhateverTheCheckout(t *testing.T) {
	root, main, setupRoot := setupProject(t)
	srcGit(t, root, "checkout", "--quiet", "-b", "topic")
	topic := commitOn(t, root, "Work on the topic")
	writeFile(t, filepath.Join(root, "README.md"), "An uncommitted edit.\n")
	writeFile(t, filepath.Join(root, "notes.md"), "An untracked file.\n")

	result := setupOperation(t, "prepare", root, setupRoot, "")
	source := filepath.Join(setupRoot, "source")
	if result.Status != "prepared" || result.StartCommit != main {
		t.Fatalf("the temporary repository starts at %+v, want the culler commit %s", result, main)
	}
	if got := srcGit(t, source, "rev-parse", "HEAD"); got != main {
		t.Fatalf("the temporary repository holds %s, want %s", got, main)
	}
	if got := srcGit(t, source, "symbolic-ref", "HEAD"); got != "refs/heads/culler" {
		t.Fatalf("the temporary repository is on %s, want the culler branch", got)
	}
	if got := readText(t, filepath.Join(source, "README.md")); got != "The main branch.\n" {
		t.Fatalf("an uncommitted edit reached the temporary repository: %q", got)
	}
	if _, err := os.Lstat(filepath.Join(source, "notes.md")); !os.IsNotExist(err) {
		t.Fatalf("an untracked file reached the temporary repository: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(source, ".git", "objects", "info", "alternates")); !os.IsNotExist(err) {
		t.Fatalf("the temporary repository depends on the Project objects: %v", err)
	}
	if srcGit(t, root, "symbolic-ref", "HEAD") != "refs/heads/topic" || srcGit(t, root, "rev-parse", "HEAD") != topic ||
		readText(t, filepath.Join(root, "README.md")) != "An uncommitted edit.\n" {
		t.Fatal("preparation changed the operator checkout")
	}
}

func TestSetupRepositoryPreparationKeepsTheSetupWork(t *testing.T) {
	root, main, setupRoot := setupProject(t)
	setupOperation(t, "prepare", root, setupRoot, "")
	writeFile(t, filepath.Join(setupRoot, "source", "setup.sh"), "echo ready\n")
	commitOn(t, root, "A later main commit")
	srcGit(t, root, "branch", "--force", "culler", "main")

	result := setupOperation(t, "prepare", root, setupRoot, "")
	if result.StartCommit != main {
		t.Fatalf("a repeated preparation starts at %s, want the first start commit %s", result.StartCommit, main)
	}
	if got := readText(t, filepath.Join(setupRoot, "source", "setup.sh")); got != "echo ready\n" {
		t.Fatalf("a repeated preparation lost the setup work: %q", got)
	}
}

func TestSetupRepositoryReplacesAnInterruptedPreparation(t *testing.T) {
	root, main, setupRoot := setupProject(t)
	source := filepath.Join(setupRoot, "source")
	if err := os.MkdirAll(filepath.Join(source, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if result := setupOperation(t, "prepare", root, setupRoot, ""); result.StartCommit != main {
		t.Fatalf("the replaced repository starts at %s, want %s", result.StartCommit, main)
	}
	if got := srcGit(t, source, "rev-parse", "HEAD"); got != main {
		t.Fatalf("the replaced repository holds %s, want %s", got, main)
	}
}

func TestSetupCommitMovesTheCullerBranchAndLeavesTheCheckout(t *testing.T) {
	root, main, setupRoot := setupProject(t)
	srcGit(t, root, "checkout", "--quiet", "-b", "topic")
	topic := commitOn(t, root, "Work on the topic")
	writeFile(t, filepath.Join(root, "README.md"), "An uncommitted edit.\n")
	setupOperation(t, "prepare", root, setupRoot, "")
	commit := commitSetupFiles(t, setupRoot, map[string]string{"setup.sh": "echo ready\n"})

	result := setupOperation(t, "deliver", root, setupRoot, commit)
	if result.Status != "delivered" || result.StartCommit != main || result.CullerCommit != commit {
		t.Fatalf("the delivery reports %+v", result)
	}
	if got := srcGit(t, root, "rev-parse", "culler"); got != commit {
		t.Fatalf("the culler branch is at %s, want the setup commit %s", got, commit)
	}
	if got := srcGit(t, root, "rev-parse", SetupRetainedRef("s1")); got != commit {
		t.Fatalf("the Project repository keeps %s, want the setup commit %s", got, commit)
	}
	if srcGit(t, root, "symbolic-ref", "HEAD") != "refs/heads/topic" || srcGit(t, root, "rev-parse", "HEAD") != topic ||
		readText(t, filepath.Join(root, "README.md")) != "An uncommitted edit.\n" {
		t.Fatal("the delivery changed the operator checkout")
	}
	if _, err := os.Lstat(filepath.Join(root, "setup.sh")); !os.IsNotExist(err) {
		t.Fatalf("the delivery wrote a setup file into a checkout of another branch: %v", err)
	}
	if again := setupOperation(t, "deliver", root, setupRoot, commit); again.Status != "delivered" {
		t.Fatalf("a repeated delivery reports %+v", again)
	}
}

func TestSetupCommitUpdatesACheckedOutCullerBranch(t *testing.T) {
	root, _, setupRoot := setupProject(t)
	srcGit(t, root, "checkout", "--quiet", "culler")
	writeFile(t, filepath.Join(root, "README.md"), "An uncommitted edit.\n")
	setupOperation(t, "prepare", root, setupRoot, "")
	commit := commitSetupFiles(t, setupRoot, map[string]string{"setup.sh": "echo ready\n"})

	if result := setupOperation(t, "deliver", root, setupRoot, commit); result.Status != "delivered" {
		t.Fatalf("the delivery reports %+v", result)
	}
	if got := srcGit(t, root, "rev-parse", "HEAD"); got != commit {
		t.Fatalf("the checkout holds %s, want the setup commit %s", got, commit)
	}
	if got := readText(t, filepath.Join(root, "setup.sh")); got != "echo ready\n" {
		t.Fatalf("the checkout of the culler branch lacks the setup file: %q", got)
	}
	if got := readText(t, filepath.Join(root, "README.md")); got != "An uncommitted edit.\n" {
		t.Fatalf("the delivery lost a local change: %q", got)
	}
	if got := srcGit(t, root, "status", "--porcelain"); got != "M README.md" {
		t.Fatalf("the checkout status is %q, want only the local change", got)
	}
}

func TestSetupCommitWaitsForAConflictingLocalFile(t *testing.T) {
	root, main, setupRoot := setupProject(t)
	srcGit(t, root, "checkout", "--quiet", "culler")
	writeFile(t, filepath.Join(root, "setup.sh"), "echo mine\n")
	setupOperation(t, "prepare", root, setupRoot, "")
	commit := commitSetupFiles(t, setupRoot, map[string]string{"setup.sh": "echo ready\n"})

	result := setupOperation(t, "deliver", root, setupRoot, commit)
	if result.Status != "operator-attention" || !strings.Contains(result.OperatorAction, "setup.sh") {
		t.Fatalf("the delivery reports %+v, want a refusal that names setup.sh", result)
	}
	if got := srcGit(t, root, "rev-parse", "culler"); got != main {
		t.Fatalf("a refused delivery moved the culler branch to %s", got)
	}
	if got := readText(t, filepath.Join(root, "setup.sh")); got != "echo mine\n" {
		t.Fatalf("a refused delivery changed a local file: %q", got)
	}
}

func TestSetupCommitRequiresTheStartCommit(t *testing.T) {
	root, _, setupRoot := setupProject(t)
	setupOperation(t, "prepare", root, setupRoot, "")
	commit := commitSetupFiles(t, setupRoot, map[string]string{"setup.sh": "echo ready\n"})
	srcGit(t, root, "checkout", "--quiet", "culler")
	moved := commitOn(t, root, "An operator commit on the culler branch")
	srcGit(t, root, "checkout", "--quiet", "main")

	result := setupOperation(t, "deliver", root, setupRoot, commit)
	if result.Status != "destination-changed" || result.CullerCommit != moved {
		t.Fatalf("the delivery reports %+v, want the moved culler commit %s", result, moved)
	}
	if got := srcGit(t, root, "rev-parse", "culler"); got != moved {
		t.Fatalf("the delivery replaced the operator commit with %s", got)
	}
}

func TestSetupWithoutChangesLeavesTheCullerBranch(t *testing.T) {
	root, main, setupRoot := setupProject(t)
	setupOperation(t, "prepare", root, setupRoot, "")
	if result := setupOperation(t, "deliver", root, setupRoot, main); result.Status != "delivered" || result.CullerCommit != main {
		t.Fatalf("the delivery reports %+v", result)
	}
	if _, err := os.Lstat(filepath.Join(root, ".git", "refs", "mc", "setup", "s1")); !os.IsNotExist(err) {
		t.Fatalf("a setup without changes kept a reference: %v", err)
	}
}

func TestSetupRepositoryRequiresItsSetupFolder(t *testing.T) {
	root, _, setupRoot := setupProject(t)
	for name, envelope := range map[string]SetupRepositoryEnvelope{
		"identity":       {Operation: "prepare", SetupID: "../s1", WorkspaceRoot: root, SetupRoot: setupRoot},
		"folder":         {Operation: "prepare", SetupID: "s2", WorkspaceRoot: root, SetupRoot: setupRoot},
		"inside Project": {Operation: "prepare", SetupID: "s1", WorkspaceRoot: root, SetupRoot: filepath.Join(root, "setup-s1")},
		"operation":      {Operation: "remove", SetupID: "s1", WorkspaceRoot: root, SetupRoot: setupRoot},
		"commit":         {Operation: "deliver", SetupID: "s1", WorkspaceRoot: root, SetupRoot: setupRoot, Commit: "main"},
	} {
		if _, err := executeSetupRepository(context.Background(), envelope); err == nil {
			t.Fatalf("%s: the operation accepted an invalid envelope", name)
		}
	}
	if _, err := os.Lstat(setupRoot); !os.IsNotExist(err) {
		t.Fatalf("an invalid envelope created the setup folder: %v", err)
	}
}
