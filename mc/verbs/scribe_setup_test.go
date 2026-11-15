package verbs

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mc/substrate"
)

// requireDefaultRepositoryLimits requires that the new Project accepts a task
// and holds the default repository limits.
func requireDefaultRepositoryLimits(t *testing.T, db *sql.DB, project string) {
	t.Helper()
	if _, err := TaskAdd(db, nil, "Add the first task", project, "The new Project accepts a task.", nil); err != nil {
		t.Fatal(err)
	}
	var limits RepositoryLimits
	if err := db.QueryRow(`SELECT destination_quiet_seconds,max_temporary_bytes FROM repository_limits WHERE project=?`, project).
		Scan(&limits.QuietSeconds, &limits.TemporaryBytes); err != nil || limits != (RepositoryLimits{QuietSeconds: 30, TemporaryBytes: 20 << 30}) {
		t.Fatalf("the Project lost its default repository limits: %+v %v", limits, err)
	}
}

func TestProjectAddSetsTheDefaultRepositoryLimits(t *testing.T) {
	db := dvSpine(t)
	if _, err := ProjectAdd(db, nil, ProjectAddArgs{ID: "project-wiki", Title: "Project wiki", SandboxProfile: "default"}); err != nil {
		t.Fatal(err)
	}
	requireDefaultRepositoryLimits(t, db, "project-wiki")
}

func TestOnboardSetsTheDefaultRepositoryLimitsAndKeepsConfiguredLimits(t *testing.T) {
	for _, imported := range []bool{true, false} {
		name := "new"
		if imported {
			name = "imported"
		}
		t.Run(name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "control")
			spine := filepath.Join(home, "spine.db")
			t.Setenv("MC_HOME", home)
			var workspace string
			if imported {
				workspace = dvWorkspaceRepo(t)
			} else {
				workspace = filepath.Join(t.TempDir(), "project")
				if err := os.MkdirAll(workspace, 0700); err != nil {
					t.Fatal(err)
				}
			}
			writeVisionFixture(t, workspace)
			if _, _, err := onboardHome(spine, true); err != nil {
				t.Fatal(err)
			}
			a := OnboardArgs{Spine: spine, Project: "project", WorkspaceRoot: workspace, AcceptGeneratedSandbox: true}
			if _, _, err := onboardProject(a); err != nil {
				t.Fatal(err)
			}
			db, err := substrate.Open(spine)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			requireDefaultRepositoryLimits(t, db, "project")
			explicit := RepositoryLimits{QuietSeconds: 45, TemporaryBytes: 1 << 30}
			if _, err := ConfigureRepositoryLimits(db, nil, "project", explicit); err != nil {
				t.Fatal(err)
			}
			if _, _, err := onboardProject(a); err != nil {
				t.Fatal(err)
			}
			var limits RepositoryLimits
			if err := db.QueryRow(`SELECT destination_quiet_seconds,max_temporary_bytes FROM repository_limits WHERE project='project'`).
				Scan(&limits.QuietSeconds, &limits.TemporaryBytes); err != nil || limits != explicit {
				t.Fatalf("setup changed the configured repository limits: %+v %v", limits, err)
			}
		})
	}
}

func TestOnboardInitialCommitPreservesUserFilesAndIndex(t *testing.T) {
	root := t.TempDir()
	srcGit(t, root, "init", "--quiet", "--initial-branch=topic")
	writeFile(t, filepath.Join(root, "report.md"), "The complete user report remains intact.\n")
	writeFile(t, filepath.Join(root, "notes.md"), "The private notes remain untracked.\n")
	srcGit(t, root, "add", "report.md")
	changed, err := ensureWorkspaceInput(root, hostOnboardGitMutation)
	if err != nil || !changed {
		t.Fatalf("the initial repository input failed: %v %v", changed, err)
	}
	if got := srcGit(t, root, "ls-tree", "--name-only", "HEAD"); got != "" {
		t.Fatalf("the initial commit included user content: %s", got)
	}
	if got := srcGit(t, root, "diff", "--cached", "--name-only"); got != "report.md" {
		t.Fatalf("the initial commit changed the index: %s", got)
	}
	if got := srcGit(t, root, "symbolic-ref", "HEAD"); got != "refs/heads/topic" {
		t.Fatalf("the initial commit changed the user's branch: %s", got)
	}
	if srcGit(t, root, "rev-parse", "culler") != srcGit(t, root, "rev-parse", "HEAD") {
		t.Fatal("the accepted input has no culler reference")
	}
	for name, want := range map[string]string{"report.md": "The complete user report remains intact.\n", "notes.md": "The private notes remain untracked.\n"} {
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(body) != want {
			t.Fatalf("the initial commit changed %s: %q %v", name, body, err)
		}
	}
	changed, err = ensureWorkspaceInput(root, hostOnboardGitMutation)
	if err != nil || changed {
		t.Fatalf("the repeated input preparation changed the repository: %v %v", changed, err)
	}
}

func TestRepositoryThatCullerCreatesStartsWithTheFolderFiles(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "app.js"), "console.log('ready');\n")
	writeFile(t, filepath.Join(root, ".gitignore"), "secret.env\n")
	writeFile(t, filepath.Join(root, "secret.env"), "TOKEN=private\n")
	changed, _, err := ensureWorkspaceRepository(root, hostOnboardGitMutation)
	if err != nil || !changed {
		t.Fatalf("the repository was not created: %v %v", changed, err)
	}
	if got := srcGit(t, root, "symbolic-ref", "HEAD"); got != "refs/heads/culler" {
		t.Fatalf("the new repository is on %s, want the culler branch", got)
	}
	if got := srcGit(t, root, "ls-tree", "--name-only", "culler"); got != ".gitignore\napp.js" {
		t.Fatalf("the initial commit holds %q, want the files of the folder", got)
	}
	if got := srcGit(t, root, "status", "--porcelain"); got != "" {
		t.Fatalf("the initial commit left changes in the folder: %q", got)
	}
}

// mainRepository returns a repository whose main branch holds one commit.
func mainRepository(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	srcGit(t, root, "init", "--quiet", "--initial-branch=main")
	writeFile(t, filepath.Join(root, "README.md"), "The main branch.\n")
	srcGit(t, root, "add", "README.md")
	srcGit(t, root, "-c", "commit.gpgSign=false", "commit", "--quiet", "-m", "Start main")
	return root, srcGit(t, root, "rev-parse", "main")
}

func commitOn(t *testing.T, root, message string) string {
	t.Helper()
	srcGit(t, root, "-c", "commit.gpgSign=false", "commit", "--allow-empty", "--quiet", "-m", message)
	return srcGit(t, root, "rev-parse", "HEAD")
}

func TestCullerBranchStartsFromMainWhateverTheCheckout(t *testing.T) {
	root, main := mainRepository(t)
	srcGit(t, root, "checkout", "--quiet", "-b", "topic")
	topic := commitOn(t, root, "Work on the topic")
	writeFile(t, filepath.Join(root, "README.md"), "An uncommitted edit.\n")
	writeFile(t, filepath.Join(root, "notes.md"), "An untracked file.\n")
	changed, err := ensureWorkspaceInput(root, hostOnboardGitMutation)
	if err != nil || !changed {
		t.Fatalf("the culler branch was not created: %v %v", changed, err)
	}
	if got := srcGit(t, root, "rev-parse", "culler"); got != main {
		t.Fatalf("the culler branch starts at %s, want the main commit %s", got, main)
	}
	if got := srcGit(t, root, "symbolic-ref", "HEAD"); got != "refs/heads/topic" || srcGit(t, root, "rev-parse", "HEAD") != topic {
		t.Fatalf("initialization changed the checkout: %s", got)
	}
	if got := srcGit(t, root, "status", "--porcelain"); got != "M README.md\n?? notes.md" {
		t.Fatalf("initialization changed the working tree: %q", got)
	}
}

func TestCullerBranchMovesToMainWhenMainContainsIt(t *testing.T) {
	root, main := mainRepository(t)
	srcGit(t, root, "branch", "culler", main)
	advanced := commitOn(t, root, "Advance main")
	changed, err := ensureWorkspaceInput(root, hostOnboardGitMutation)
	if err != nil || !changed {
		t.Fatalf("the culler branch did not move: %v %v", changed, err)
	}
	if got := srcGit(t, root, "rev-parse", "culler"); got != advanced {
		t.Fatalf("the culler branch is at %s, want the main commit %s", got, advanced)
	}
	changed, err = ensureWorkspaceInput(root, hostOnboardGitMutation)
	if err != nil || changed {
		t.Fatalf("the repeated input preparation changed the repository: %v %v", changed, err)
	}
}

func TestCullerBranchWithOwnCommitsStopsInitialization(t *testing.T) {
	root, _ := mainRepository(t)
	srcGit(t, root, "checkout", "--quiet", "-b", "culler")
	own := commitOn(t, root, "Culler work outside main")
	srcGit(t, root, "checkout", "--quiet", "main")
	for name, run := range map[string]func() error{
		"validation": func() error { return validateInitialCullerBranch(root) },
		"input":      func() error { _, err := ensureWorkspaceInput(root, hostOnboardGitMutation); return err },
	} {
		if err := run(); err == nil || !strings.Contains(err.Error(), "The culler branch has commits that main does not have.") {
			t.Fatalf("%s accepted a culler branch with its own commits: %v", name, err)
		}
	}
	if got := srcGit(t, root, "rev-parse", "culler"); got != own {
		t.Fatal("initialization moved a culler branch with its own commits")
	}
}

func TestRepositoryWithoutMainCannotStartTheCullerBranch(t *testing.T) {
	root := t.TempDir()
	srcGit(t, root, "init", "--quiet", "--initial-branch=trunk")
	commitOn(t, root, "Start trunk")
	_, err := ensureWorkspaceInput(root, hostOnboardGitMutation)
	if err == nil || err.Error() != "Culler starts the culler branch from main. This repository has no main branch." {
		t.Fatalf("a repository without main = %v", err)
	}
	if out, err := gitAtSource(root, "rev-parse", "--verify", "--quiet", "refs/heads/culler"); err == nil {
		t.Fatalf("a refused initialization created the culler branch at %s", out)
	}
}
