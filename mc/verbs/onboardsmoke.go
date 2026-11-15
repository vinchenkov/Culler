package verbs

// Production onboarding smoke registration.
//
// This is deliberately only the durable starting transaction. It creates one
// isolated smoke Project and one expedited origin:user task; every stage
// after that is driven by the ordinary resident tick, real routes,
// completion verbs, packet decision, and merge. No smoke-only state
// transition exists.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"mc/deployment"
	"mc/domain"
	"mc/substrate"
)

var smokeProjectID = regexp.MustCompile(`^smoke-[0-9a-f]{16}$`)

const (
	onboardSmokeTitle       = "Culler full-pipeline smoke"
	onboardSmokeDescription = "Create SMOKE_RESULT.md with the sole line " +
		"`culler smoke ok` (including its trailing newline) and commit it. " +
		"Preserve all other application source."
)

// OnboardSmokeVision is the VISION.md content of the smoke Project. The host
// writes it into the smoke repository before the Project is registered.
const OnboardSmokeVision = "# Vision\n\nThis disposable Project exists only for the " +
	"Culler full-pipeline acceptance smoke. The smoke task creates SMOKE_RESULT.md.\n"

// prepareOnboardSmoke is the host-authority half. The helper receives the
// canonical root only as schema data; it never stats or opens a host path.
func prepareOnboardSmoke(a OnboardArgs) (OnboardArgs, error) {
	if a.Section != "smoke" || !a.Smoke {
		return OnboardArgs{}, Usagef("private onboarding smoke requires the smoke section")
	}
	if !smokeProjectID.MatchString(a.Project) {
		return OnboardArgs{}, Usagef("onboarding smoke Project id is invalid")
	}
	if !validStructuralText(a.WorkspaceRoot, maxPrivateScalarBytes) ||
		!filepath.IsAbs(a.WorkspaceRoot) {
		return OnboardArgs{}, Usagef("onboarding smoke workspace root must be an absolute canonical path")
	}
	resolved, err := filepath.EvalSymlinks(a.WorkspaceRoot)
	if err != nil {
		return OnboardArgs{}, Domainf("resolve onboarding smoke workspace root: %v", err)
	}
	a.WorkspaceRoot = filepath.Clean(resolved)
	info, err := os.Stat(a.WorkspaceRoot)
	if err != nil || !info.IsDir() {
		return OnboardArgs{}, Domainf("onboarding smoke workspace root is unavailable: %v", err)
	}
	home, err := mcHomeDir()
	if err != nil {
		return OnboardArgs{}, err
	}
	// The caller generates the smoke root outside MC_HOME because the
	// ordinary mount jurisdiction correctly rejects every MC_HOME descendant.
	// The private frame still proves a closed smoke identity and exact canonical
	// directory; the host orchestrator separately derives that root from the
	// deployment identity.
	base, err := deployment.SmokeWorkspaceBase(home)
	if err != nil {
		return OnboardArgs{}, Domainf("derive isolated onboarding smoke root: %v", err)
	}
	expected, err := filepath.EvalSymlinks(
		filepath.Join(base, a.Project, "workspace"))
	if err != nil {
		return OnboardArgs{}, Domainf("resolve isolated onboarding smoke root: %v", err)
	}
	if a.WorkspaceRoot != filepath.Clean(expected) {
		return OnboardArgs{}, Domainf(
			"onboarding smoke workspace must be the isolated deployment path %q", expected)
	}
	// Containers run with no ambient HOME, so this per-Project path IS
	// the `/home/agent` the harness gets, and the private commit leg refuses any
	// non-fake harness whose selected profile carries an empty one. The
	// project section gets this through its generated profile JSON; the smoke
	// section carries the scalar directly, so derive and create it here — the
	// one function in this section that already owns host filesystem work.
	// Without it the smoke registers a Project that can never be dispatched.
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		return OnboardArgs{}, Domainf(
			"resolve canonical MC_HOME for the onboarding smoke synthetic home: %v", err)
	}
	a.ProjectToolHomeDir = ProjectToolHome(filepath.Clean(canonical), a.Project)
	if _, err := PrepareOnboardProjectToolHome(a, os.Getuid()); err != nil {
		return OnboardArgs{}, err
	}
	return a, nil
}

// onboardSmokeDB is the helper-authoritative half. Its one transaction is
// idempotent for the exact generated identity so a host crash after commit can
// resume without fabricating a second smoke task.
func onboardSmokeDB(a OnboardArgs) (string, string, []OnboardWorkspaceRoot, error) {
	if err := requireSpineFile(a.Spine); err != nil {
		return "", "", nil, err
	}
	if !a.Smoke || !smokeProjectID.MatchString(a.Project) ||
		!filepathIsCleanAbsolute(a.WorkspaceRoot) {
		return "", "", nil, Usagef("private onboarding smoke frame is invalid")
	}
	db, err := substrate.Open(a.Spine)
	if err != nil {
		return "", "", nil, Usagef("%v", err)
	}
	defer db.Close()

	var taskID int64
	created := false
	err = inTx(db, func(ctx context.Context, q Q) error {
		var profile, status, root string
		err := q.QueryRowContext(ctx, `
			SELECT w.sandbox_profile, w.status, p.workspace_root
			FROM projects w
			JOIN sandbox_profiles p ON p.id = w.sandbox_profile
			WHERE w.id = ?`, a.Project).Scan(&profile, &status, &root)
		switch {
		case err == nil:
			if profile != a.Project ||
				status != "active" || root != a.WorkspaceRoot {
				return Domainf("onboarding smoke Project %q conflicts with durable state", a.Project)
			}
			rows, err := q.QueryContext(ctx, `
				SELECT id FROM tasks
				WHERE project = ? AND origin = 'user' AND title = ?
				ORDER BY id`, a.Project, onboardSmokeTitle)
			if err != nil {
				return err
			}
			defer rows.Close()
			var ids []int64
			for rows.Next() {
				var id int64
				if err := rows.Scan(&id); err != nil {
					return err
				}
				ids = append(ids, id)
			}
			if err := rows.Err(); err != nil {
				return err
			}
			if len(ids) != 1 {
				return Domainf("onboarding smoke Project %q has %d canonical tasks, want exactly one",
					a.Project, len(ids))
			}
			taskID = ids[0]
			return nil
		case err != sql.ErrNoRows:
			return err
		}

		if a.ProjectToolHomeDir == "" {
			return Domainf("onboarding smoke profile requires its synthetic home: a profile without one is refused at dispatch")
		}
		// The artifact plane is not decoration on the smoke profile; it is the
		// Packager's ONLY writable surface. Its sealed-view arm forces every task
		// plan row to RO, the resident derives the claude-sdk writable set from
		// the RW plan entries alone, and a claude-sdk route with an empty writable
		// set refuses before the harness starts — so a smoke profile without this
		// column cannot reach a rendered packet, whatever else is correct.
		//
		// Derived through ProjectArtifactRoot, the same single derivation the
		// host half already uses to create the directory and to author its mount
		// allowlist grant. Those two were built for the smoke and this column was
		// not, which is why every filesystem-side check passed while the mount was
		// never requested.
		artifactRoots, err := json.Marshal([]string{ProjectArtifactRoot(a.WorkspaceRoot)})
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO sandbox_profiles (id, workspace_root, artifact_roots, tool_home_dir)
			VALUES (?, ?, ?, ?)`,
			a.Project, a.WorkspaceRoot, string(artifactRoots), a.ProjectToolHomeDir); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO projects (id, title, sandbox_profile, vision)
			VALUES (?, ?, ?, 'present')`,
			a.Project, onboardSmokeTitle, a.Project); err != nil {
			return err
		}
		priority := -1
		taskID, err = domain.BirthProposal(ctx, q, domain.ProposalArgs{
			Title: onboardSmokeTitle, Description: onboardSmokeDescription,
			Priority: &priority, Origin: "user", Project: a.Project,
		})
		if err != nil {
			return err
		}
		if err := substrate.ValidateDispatchMountProjection(ctx, q); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return "", "", nil, err
	}
	status := "ok"
	if created {
		status = "done"
	}
	return status,
		fmt.Sprintf("smoke Project %s and origin:user task %d registered",
			a.Project, taskID),
		[]OnboardWorkspaceRoot{{ID: a.Project, Root: a.WorkspaceRoot}}, nil
}
