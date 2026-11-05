package verbs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"

	"mc/dispatch"
	"mc/domain"
)

type RoleCustomization struct {
	Project           string        `json:"project"`
	Role              dispatch.Role `json:"role"`
	Revision          int64         `json:"revision"`
	Instructions      string        `json:"instructions"`
	Author            string        `json:"author"`
	CreatedAt         string        `json:"created_at"`
	StandardDirective string        `json:"standard_directive,omitempty"`
	StandardDigest    string        `json:"standard_digest,omitempty"`
}

type RoleSaveRequest struct {
	Project          string        `json:"project"`
	Role             dispatch.Role `json:"role"`
	ExpectedRevision *int64        `json:"expected_revision"`
	Instructions     *string       `json:"instructions"`
}

func customizationReader(id *RunIdentity) error {
	if id != nil && id.Tier != "homie" {
		return Domainf("Only the operator and Homie can read customization records.")
	}
	return nil
}

func customizationCurrent(ctx context.Context, q Q, project string, role dispatch.Role) (RoleCustomization, error) {
	c := RoleCustomization{Project: project, Role: role}
	directive, err := directiveForRole(role)
	if err != nil {
		return c, Usagef("Unknown pipeline role %q.", role)
	}
	var title string
	if err := q.QueryRowContext(ctx, `SELECT title FROM projects WHERE id=?`, project).Scan(&title); err != nil {
		if err == sql.ErrNoRows {
			return c, Domainf("Unknown Project %q.", project)
		}
		return c, err
	}
	c.StandardDirective = directive
	c.StandardDigest = fmt.Sprintf("%x", sha256.Sum256([]byte(directive)))
	err = q.QueryRowContext(ctx, `SELECT revision,instructions,author,created_at FROM role_customizations WHERE project=? AND role=? ORDER BY revision DESC LIMIT 1`, project, role).Scan(&c.Revision, &c.Instructions, &c.Author, &c.CreatedAt)
	if err == sql.ErrNoRows {
		err = nil
	}
	return c, err
}

func ProjectRoleShow(db *sql.DB, id *RunIdentity, project string, role dispatch.Role) (any, error) {
	if err := customizationReader(id); err != nil {
		return nil, err
	}
	result := map[string]any{}
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	err = func(q Q) error {
		c, err := customizationCurrent(ctx, q, project, role)
		if err != nil {
			return err
		}
		result["current"] = c
		result["contract"] = roleContract(role)
		var title string
		if err := q.QueryRowContext(ctx, `SELECT title FROM projects WHERE id=?`, project).Scan(&title); err != nil {
			return err
		}
		result["project_title"] = title
		roles := []map[string]string{}
		for _, r := range []dispatch.Role{dispatch.RoleStrategist, dispatch.RoleArbiter, dispatch.RoleWorker, dispatch.RoleVerifier, dispatch.RoleScribe, dispatch.RolePackager} {
			roles = append(roles, map[string]string{"role": string(r), "title": roleTitle[r]})
		}
		result["roles"] = roles
		rows, err := q.QueryContext(ctx, `SELECT revision,instructions,author,created_at FROM role_customizations WHERE project=? AND role=? ORDER BY revision DESC`, project, role)
		if err != nil {
			return err
		}
		history := []RoleCustomization{}
		for rows.Next() {
			r := RoleCustomization{Project: project, Role: role}
			if err := rows.Scan(&r.Revision, &r.Instructions, &r.Author, &r.CreatedAt); err != nil {
				rows.Close()
				return err
			}
			history = append(history, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		result["history"] = history
		runRows, err := q.QueryContext(ctx, `SELECT c.run_id,COALESCE(c.revision,0) FROM run_customizations c JOIN runs r ON r.id=c.run_id WHERE c.project=? AND c.role=? ORDER BY r.created_seq DESC`, project, role)
		if err != nil {
			return err
		}
		runs := []map[string]any{}
		for runRows.Next() {
			var run string
			var revision int64
			if err := runRows.Scan(&run, &revision); err != nil {
				runRows.Close()
				return err
			}
			runs = append(runs, map[string]any{"run_id": run, "revision": revision})
		}
		err = runRows.Err()
		runRows.Close()
		if err != nil {
			return err
		}
		result["runs"] = runs

		conflicts, err := customizationConflicts(ctx, q, project, role)
		result["conflicts"] = conflicts
		return err
	}(tx)
	if err == nil {
		err = tx.Commit()
	}
	return result, err
}

func ProjectRoleSave(db *sql.DB, id *RunIdentity, a RoleSaveRequest) (any, error) {
	if err := RequireOperatorVerb(id, "project.role-save"); err != nil {
		return nil, err
	}
	if a.ExpectedRevision == nil || *a.ExpectedRevision < 0 || a.Instructions == nil {
		return nil, Usagef("The save requires instructions and the revision that you read.")
	}
	if len(*a.Instructions) > 65536 {
		return nil, Usagef("Custom instructions exceed 65536 bytes.")
	}
	var result RoleCustomization
	err := inTx(db, func(ctx context.Context, q Q) error {
		c, err := customizationCurrent(ctx, q, a.Project, a.Role)
		if err != nil {
			return err
		}
		if c.Revision != *a.ExpectedRevision {
			return domain.Errf(domain.CodeRecordConflict, "Another edit changed this customization. Read the current revision and compare your text.")
		}
		author := "operator"
		if id != nil {
			author = "homie:" + id.RunID
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO role_customizations(project,role,revision,instructions,author) VALUES(?,?,?,?,?)`, a.Project, a.Role, c.Revision+1, *a.Instructions, author); err != nil {
			return err
		}
		result, err = customizationCurrent(ctx, q, a.Project, a.Role)
		return err
	})
	return result, err
}

func customizationForRun(ctx context.Context, q Q, sp *dispatch.Spawn, run string) (RoleCustomization, error) {
	c, err := loadRunCustomization(ctx, q, run)
	if err == sql.ErrNoRows {
		return customizationCurrent(ctx, q, sp.Project, sp.Role)
	}
	return c, err
}
func loadRunCustomization(ctx context.Context, q Q, run string) (RoleCustomization, error) {
	var c RoleCustomization
	err := q.QueryRowContext(ctx, `SELECT c.project,c.role,COALESCE(c.revision,0),COALESCE(r.instructions,''),COALESCE(r.author,''),COALESCE(r.created_at,''),c.standard_directive,c.standard_digest
 FROM run_customizations c LEFT JOIN role_customizations r ON r.project=c.project AND r.role=c.role AND r.revision=c.revision WHERE c.run_id=?`, run).Scan(&c.Project, &c.Role, &c.Revision, &c.Instructions, &c.Author, &c.CreatedAt, &c.StandardDirective, &c.StandardDigest)
	return c, err
}
func recordRunCustomization(ctx context.Context, q Q, sp *dispatch.Spawn, run string) error {
	c, err := customizationCurrent(ctx, q, sp.Project, sp.Role)
	if err != nil {
		return err
	}
	var revision any
	if c.Revision > 0 {
		revision = c.Revision
	}
	_, err = q.ExecContext(ctx, `INSERT INTO run_customizations(run_id,project,role,revision,standard_directive,standard_digest) VALUES(?,?,?,?,?,?)`, run, c.Project, c.Role, revision, c.StandardDirective, c.StandardDigest)
	return err
}
func RunCustomization(db *sql.DB, id *RunIdentity, run string) (any, error) {
	if err := customizationReader(id); err != nil {
		return nil, err
	}
	c, err := loadRunCustomization(context.Background(), db, run)
	if err == sql.ErrNoRows {
		return nil, Domainf("This run has no customization record.")
	}
	return c, err
}

type CustomizationConflictRequest struct {
	Run         string `json:"run"`
	Revision    int64  `json:"revision"`
	Passage     string `json:"passage"`
	Requirement string `json:"requirement"`
	Reason      string `json:"reason"`
}

func RunCustomizationConflict(db *sql.DB, id *RunIdentity, a CustomizationConflictRequest) (any, error) {
	if err := requirePipeline(id); err != nil {
		return nil, err
	}
	if err := requireOwnRun(id, a.Run); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Passage) == "" || strings.TrimSpace(a.Requirement) == "" || strings.TrimSpace(a.Reason) == "" || len(a.Passage) > 65536 || len(a.Requirement) > 8192 || len(a.Reason) > 8192 {
		return nil, Usagef("The conflict requires a passage, a contract requirement, and a reason within the text limits.")
	}
	result := map[string]any{"run_id": a.Run, "blocked": true}
	err := inTx(db, func(ctx context.Context, q Q) error {
		fence, err := domain.FenceAgentRun(ctx, q, a.Run)
		if err != nil {
			return err
		}
		subject := fence.TaskID
		c, err := loadRunCustomization(ctx, q, a.Run)
		if err != nil {
			return err
		}
		if id != nil && string(c.Role) != id.Role {
			return Domainf("The role differs from the assigned run role.")
		}
		if a.Revision == 0 || a.Revision != c.Revision || !strings.Contains(c.Instructions, a.Passage) {
			return Domainf("The conflict must cite a passage from the revision assigned to this run.")
		}
		var title string
		if err := q.QueryRowContext(ctx, `SELECT title FROM projects WHERE id=?`, c.Project).Scan(&title); err != nil {
			return err
		}
		insert := `INSERT INTO customization_conflicts(run_id,project_title,passage,requirement,reason) VALUES(?,?,?,?,?)`
		if fence.WikiUpdate {
			// The conflict of a wiki update run blocks no role. Its row is cleared from the start.
			insert = `INSERT INTO customization_conflicts(run_id,project_title,passage,requirement,reason,cleared_at,cleared_by,resolution) VALUES(?,?,?,?,?,datetime('now'),'mc','The wiki update ended as failed.')`
		}
		if _, err := q.ExecContext(ctx, insert, a.Run, title, a.Passage, a.Requirement, a.Reason); err != nil {
			return err
		}
		report := fmt.Sprintf("Project: %s (%s)\nRole: %s\nRun: %s\nCustomization revision: %d\nPassage: %s\nContract requirement: %s\nReason: %s", title, c.Project, c.Role, a.Run, c.Revision, a.Passage, a.Requirement, a.Reason)
		if subject != nil {
			report = fmt.Sprintf("Subject: %d\n%s", *subject, report)
			if err := blockTaskTx(ctx, q, *subject, domain.BlockCustomization, report, string(c.Role)); err != nil {
				return err
			}
			result["task_id"] = *subject
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO activity(actor,kind,subject,detail,project) VALUES(?,'customization.conflict',?,?,?)`, string(c.Role), a.Run, report, c.Project); err != nil {
			return err
		}
		if fence.WikiUpdate {
			result["blocked"] = false
			_, err := domain.EndWikiRun(ctx, q, a.Run, domain.WikiRunEnd{Outcome: "completed", Result: "failed",
				Cause: "The custom instructions of the Scribe conflict with the Culler contract."})
			return err
		}
		return endRun(ctx, q, a.Run, "blocked")
	})
	return result, err
}

type CustomizationConflict struct {
	Run          string `json:"run"`
	Project      string `json:"project"`
	ProjectTitle string `json:"project_title"`
	Role         string `json:"role"`
	Revision     int64  `json:"revision"`
	Task         *int64 `json:"task"`
	Passage      string `json:"passage"`
	Requirement  string `json:"requirement"`
	Reason       string `json:"reason"`
	CreatedAt    string `json:"created_at"`
	Blocked      bool   `json:"blocked"`
	Resolution   string `json:"resolution"`
}

func customizationConflicts(ctx context.Context, q Q, project string, role dispatch.Role) ([]CustomizationConflict, error) {
	rows, err := q.QueryContext(ctx, `SELECT f.run_id,c.project,f.project_title,c.role,c.revision,r.subject,f.passage,f.requirement,f.reason,f.created_at,
 CASE WHEN r.subject IS NULL THEN f.cleared_at IS NULL ELSE t.blocked AND f.cleared_at IS NULL END,COALESCE(f.resolution,'')
 FROM customization_conflicts f JOIN run_customizations c ON c.run_id=f.run_id JOIN runs r ON r.id=f.run_id
 LEFT JOIN tasks t ON t.id=r.subject WHERE c.project=? AND c.role=? ORDER BY r.created_seq DESC`, project, role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CustomizationConflict{}
	for rows.Next() {
		var c CustomizationConflict
		if err := rows.Scan(&c.Run, &c.Project, &c.ProjectTitle, &c.Role, &c.Revision, &c.Task, &c.Passage, &c.Requirement, &c.Reason, &c.CreatedAt, &c.Blocked, &c.Resolution); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

type RoleUnblockRequest struct {
	Run    string `json:"run"`
	Reason string `json:"reason"`
}

func ProjectRoleUnblock(db *sql.DB, id *RunIdentity, a RoleUnblockRequest) (any, error) {
	if err := RequireOperatorVerb(id, "project.role-unblock"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Reason) == "" {
		return nil, Usagef("The block requires a resolution reason.")
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		actor := "operator"
		if id != nil {
			actor = "homie:" + id.RunID
		}
		res, err := q.ExecContext(ctx, `UPDATE customization_conflicts SET cleared_at=datetime('now'),cleared_by=?,resolution=? WHERE run_id=? AND cleared_at IS NULL
 AND EXISTS(SELECT 1 FROM runs WHERE id=? AND subject IS NULL)`, actor, a.Reason, a.Run, a.Run)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return Domainf("This run has no active role block. Use the subject block controls for a task.")
		}
		return nil
	})
	return map[string]any{"run": a.Run, "blocked": false}, err
}
