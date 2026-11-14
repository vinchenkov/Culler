package verbs

import (
	"context"
	"database/sql"
	"regexp"

	"mc/substrate"
)

const projectSecretsAdvisory = "Agents can read every secret in this workspace and send them anywhere: containers browse the internet freely, and nothing in the request path restricts or records that traffic. Use scoped, expiring, revocable credentials (such as limited database roles or fine-grained PATs), and keep secret files gitignored. There is no per-Project egress dial; credential scope is the blast-radius control."

type ProjectAddArgs struct {
	ID             string
	Title          string
	Jurisdiction   string
	SandboxProfile string
	Paused         bool
}

// projectIDPattern bounds a Project id to Docker's container-name
// alphabet (the container name is `mc-<project>-<run_id>`) and to 64
// characters.
const projectIDPattern = `^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`

// MaxProjectTitleBytes bounds the display title of a Project.
const MaxProjectTitleBytes = 200

var projectIDRe = regexp.MustCompile(projectIDPattern)

// ValidProjectID reports whether id is a legal Project id.
func ValidProjectID(id string) bool { return projectIDRe.MatchString(id) }

func ProjectAdd(db *sql.DB, id *RunIdentity, a ProjectAddArgs) (any, error) {
	if err := RequireOperatorVerb(id, "project.add"); err != nil {
		return nil, err
	}
	if a.ID == "" || a.Title == "" {
		return nil, Usagef("mc project add requires id and --title")
	}
	if !validStructuralText(a.ID, maxPrivateScalarBytes) ||
		(a.SandboxProfile != "" && !validStructuralText(a.SandboxProfile, maxPrivateScalarBytes)) {
		return nil, Usagef("mc project add id/profile must be valid UTF-8 without controls and at most 4096 bytes")
	}
	// The id names the pipeline container (`mc-<project>-<run_id>`), so it
	// is bounded to Docker's container-name alphabet and length.
	if !ValidProjectID(a.ID) {
		return nil, Usagef("mc project add id must match %s (Docker container-name safe)", projectIDPattern)
	}
	if a.SandboxProfile == "" {
		return nil, Usagef("mc project add requires --sandbox-profile: a Project is a Git repository at the workspace root of its profile")
	}
	status := "active"
	if a.Paused {
		status = "paused"
	}
	workspaceGit := ""
	err := inTx(db, func(ctx context.Context, q Q) error {
		if err := requireOperatorVerbTx(ctx, q, id, "project.add"); err != nil {
			return err
		}
		if a.SandboxProfile != "" {
			var workspaceRoot sql.NullString
			err := q.QueryRowContext(ctx,
				`SELECT workspace_root FROM sandbox_profiles WHERE id = ?`, a.SandboxProfile,
			).Scan(&workspaceRoot)
			if err == sql.ErrNoRows {
				return Domainf("unknown sandbox profile %q", a.SandboxProfile)
			}
			if err != nil {
				return err
			}
			// A Project is a Git repository. This is the one path where a row
			// is born already bound to a workspace without passing the
			// onboarding git section, so the rule is applied here: a repository
			// at the profile's workspace root is adopted, and its absence is
			// served by `git init` there.
			if !workspaceRoot.Valid || workspaceRoot.String == "" {
				return Domainf("sandbox profile %q names no workspace root; a Project must be a Git repository", a.SandboxProfile)
			}
			// A stored root is never filesystem authority, and `git init`
			// writes: the address must clear the same attestation the
			// onboarding section applies before anything runs in it.
			if err := ValidateOnboardWorkspaceRoots([]OnboardWorkspaceRoot{
				{ID: a.ID, Root: workspaceRoot.String},
			}); err != nil {
				return err
			}
			_, initialized, err := ensureWorkspaceRepository(workspaceRoot.String, hostOnboardGitMutation)
			if err != nil {
				return err
			}
			workspaceGit = ProjectGitStatement(workspaceRoot.String) + " " +
				ProjectGitOutcome(workspaceRoot.String, initialized)
		}
		_, err := q.ExecContext(ctx, `
			INSERT INTO projects
				(id, title, jurisdiction, sandbox_profile, status)
			VALUES (?, ?, ?, ?, ?)`,
			a.ID, a.Title, nullIfEmpty(a.Jurisdiction), a.SandboxProfile, status)
		if err != nil {
			return err
		}
		return substrate.ValidateDispatchMountProjection(ctx, q)
	})
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"project_id":       a.ID,
		"status":           status,
		"secrets_advisory": projectSecretsAdvisory,
	}
	if workspaceGit != "" {
		result["workspace_git"] = workspaceGit
	}
	return result, nil
}

func ProjectSetStatus(db *sql.DB, id *RunIdentity, project, target string) (any, error) {
	// Each status change requires its own operator permission.
	verbs := map[string]string{"paused": "project.pause", "active": "project.resume", "archived": "project.archive"}
	verb, ok := verbs[target]
	if !ok {
		return nil, Usagef("project status target must be active|paused|archived")
	}
	if err := RequireOperatorVerb(id, verb); err != nil {
		return nil, err
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		if err := AuthorizeOperator(ctx, q, id, verb, project); err != nil {
			return err
		}
		var current, setup string
		err := q.QueryRowContext(ctx,
			`SELECT status, setup_status FROM projects WHERE id = ?`, project).Scan(&current, &setup)
		if err == sql.ErrNoRows {
			return Domainf("unknown project %q", project)
		}
		if err != nil {
			return err
		}
		if target == "active" && setup != "ready" {
			return Domainf("Project initialization is incomplete. The Project remains paused.")
		}
		if target == "paused" && setup != "ready" {
			if _, err = q.ExecContext(ctx, `UPDATE project_environments SET pause_after_setup=1 WHERE project_id=?`, project); err != nil {
				return err
			}
		}
		if current == target {
			return nil // operator/surface retry is idempotent
		}
		if current == "archived" {
			return Domainf("archived project %q is terminal", project)
		}
		_, err = q.ExecContext(ctx,
			`UPDATE projects SET status = ? WHERE id = ?`, target, project)
		if err != nil {
			return err
		}
		return substrate.ValidateDispatchMountProjection(ctx, q)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"project_id": project, "status": target}, nil
}

func ProjectList(db *sql.DB) (any, error) {
	rows, err := db.Query(`
		SELECT w.id, w.title, w.jurisdiction, w.sandbox_profile,
		       w.status, w.vision, w.setup_status, p.workspace_root, p.git_author_name, p.git_author_email
		FROM projects w LEFT JOIN sandbox_profiles p ON p.id = w.sandbox_profile ORDER BY w.id`)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()
	out, err := rowsToMaps(rows)
	if err != nil {
		return nil, classify(err)
	}
	return map[string]any{"projects": out}, nil
}
