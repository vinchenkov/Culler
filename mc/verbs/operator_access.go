package verbs

import (
	"context"
	"database/sql"
)

// AuthorizeOperator checks the current session and its frozen permissions.
// An empty permission permits reads after the session identity check.
func AuthorizeOperator(ctx context.Context, q Q, id *RunIdentity, permission, project string) error {
	if id == nil {
		return nil
	}
	if id.Tier != "collie" {
		return Domainf("This operation requires the operator or an active Collie session.")
	}
	if err := requireActiveCollieVerb(ctx, q, id, permission); err != nil {
		return err
	}
	if project != "" || permission == "pause" || permission == "resume" {
		var bound sql.NullString
		if err := q.QueryRowContext(ctx, `SELECT vision_project_id FROM collie_sessions WHERE id=?`, id.RunID).Scan(&bound); err != nil {
			return err
		}
		if bound.Valid {
			if project == "" {
				return Domainf("This Vision conversation requires controls for its associated Project.")
			}
			if bound.String != project {
				return Domainf("This Vision conversation belongs to another Project.")
			}
		}
	}
	return nil
}
