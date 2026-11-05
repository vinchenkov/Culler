package verbs

import (
	"database/sql"
	"os"
	"path/filepath"
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
	if srcGit(t, root, "rev-parse", "main") != srcGit(t, root, "rev-parse", "HEAD") {
		t.Fatal("the accepted input has no main reference")
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
