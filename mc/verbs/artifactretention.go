package verbs

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const ArtifactRetentionDays = 90

type ArtifactExpiry struct {
	WorkspaceRoot string   `json:"workspace_root"`
	TaskID        int64    `json:"task_id"`
	Token         string   `json:"token"`
	Paths         []string `json:"paths"`
	Protected     []string `json:"protected"`
}

func recordTaskArtifacts(ctx context.Context, q Q, task int64, paths ...string) error {
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO task_artifacts(task_id,path) VALUES(?,?)
            ON CONFLICT(task_id,path) DO UPDATE SET expired_at=NULL`, task, path); err != nil {
			return err
		}
	}
	return nil
}

// ArtifactExpiryPlan reserves one closed task before the host removes its files.
func ArtifactExpiryPlan(db *sql.DB, id *RunIdentity, now time.Time) (ArtifactExpiry, error) {
	result := ArtifactExpiry{Paths: []string{}, Protected: []string{}}
	if err := RequireHostScope(id, "artifact expiry"); err != nil {
		return result, err
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		if _, err := q.ExecContext(ctx, `INSERT INTO artifact_retention(task_id) SELECT id FROM tasks WHERE true ON CONFLICT DO NOTHING`); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE artifact_retention SET expires_at=NULL
            WHERE token IS NULL AND task_id IN (SELECT task_id FROM artifact_holds)`); err != nil {
			return err
		}
		expires := now.UTC().AddDate(0, 0, ArtifactRetentionDays).Format("2006-01-02 15:04:05")
		if _, err := q.ExecContext(ctx, `UPDATE artifact_retention SET expires_at=?
            WHERE token IS NULL AND expires_at IS NULL AND task_id NOT IN (SELECT task_id FROM artifact_holds)
            AND (expired_at IS NULL OR EXISTS(SELECT 1 FROM task_artifacts f WHERE f.task_id=artifact_retention.task_id AND f.expired_at IS NULL))`, expires); err != nil {
			return err
		}
		var token sql.NullString
		err := q.QueryRowContext(ctx, `SELECT a.task_id,a.token,s.workspace_root FROM artifact_retention a JOIN tasks t ON t.id=a.task_id
            JOIN projects p ON p.id=t.project JOIN sandbox_profiles s ON s.id=p.sandbox_profile
            WHERE a.token IS NOT NULL OR (a.expires_at<=? AND t.archived=1 AND a.task_id NOT IN(SELECT task_id FROM artifact_holds)
              AND (a.expired_at IS NULL OR EXISTS(SELECT 1 FROM task_artifacts f WHERE f.task_id=a.task_id AND f.expired_at IS NULL))
              AND NOT EXISTS(SELECT 1 FROM leases l WHERE l.project=t.project AND l.run_id IS NOT NULL)
              AND NOT EXISTS(SELECT 1 FROM repository_executions x WHERE x.project=t.project AND x.ended_at IS NULL)
              AND NOT EXISTS(SELECT 1 FROM runs r WHERE r.project=t.project AND r.ended_at IS NULL))
            ORDER BY a.token IS NULL,a.task_id LIMIT 1`, now.UTC().Format("2006-01-02 15:04:05")).Scan(&result.TaskID, &token, &result.WorkspaceRoot)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		result.Token = token.String
		if result.Token == "" {
			result.Token, err = newRunID()
			if err != nil {
				return err
			}
			if _, err = q.ExecContext(ctx, `UPDATE artifact_retention SET token=? WHERE task_id=?`, result.Token, result.TaskID); err != nil {
				return err
			}
		}
		rows, err := q.QueryContext(ctx, `SELECT path,task_id=? FROM task_artifacts WHERE expired_at IS NULL`, result.TaskID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			var owned bool
			if err := rows.Scan(&name, &owned); err != nil {
				return err
			}
			if owned {
				result.Paths = append(result.Paths, name)
			} else {
				result.Protected = append(result.Protected, name)
			}
		}
		return rows.Err()
	})
	return result, err
}

func ArtifactExpiryComplete(db *sql.DB, id *RunIdentity, task int64, token string, now time.Time) (any, error) {
	if err := RequireHostScope(id, "artifact expiry"); err != nil {
		return nil, err
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		var recorded sql.NullString
		if err := q.QueryRowContext(ctx, `SELECT token FROM artifact_retention WHERE task_id=?`, task).Scan(&recorded); err != nil {
			return err
		}
		if token == "" || recorded.String != token {
			return Domainf("the artifact expiry no longer identifies this task")
		}
		at := now.UTC().Format("2006-01-02 15:04:05")
		if _, err := q.ExecContext(ctx, `UPDATE task_artifacts SET expired_at=? WHERE task_id=? AND expired_at IS NULL`, at, task); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE artifact_retention SET token=NULL,expired_at=? WHERE task_id=?`, at, task); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, `INSERT INTO activity(actor,kind,subject,detail) VALUES('system','artifacts.expired',?,'Task artifacts expired after the retention period.')`, task)
		return err
	})
	return map[string]any{"task_id": task, "expired": err == nil}, err
}

// RemoveExpiredArtifacts uses descriptor-relative paths inside trusted artifact roots.
func RemoveExpiredArtifacts(roots map[string]string, plan ArtifactExpiry) error {
	if plan.TaskID <= 0 || plan.Token == "" {
		return fmt.Errorf("artifact expiry requires its task and reservation")
	}
	paths := map[string]bool{}
	owned := append([]string{}, plan.Paths...)
	// The task directory is owned only when the artifact plane of the Project
	// is a complete mount allowlist entry.
	if plan.WorkspaceRoot != "" {
		if target, inside, err := projectArtifactPlane(roots, plan.WorkspaceRoot); err == nil && inside == "" {
			owned = append(owned, filepath.Join(ArtifactsMountRoot(), target, strconv.FormatInt(plan.TaskID, 10), ".expiry"))
		}
	}
	for _, name := range owned {
		if filepath.Clean(name) != name || !strings.HasPrefix(name, ArtifactsMountRoot()+"/") {
			return fmt.Errorf("artifact expiry requires a canonical artifact path")
		}
		rest := strings.TrimPrefix(name, ArtifactsMountRoot()+"/")
		plane, relative, ok := strings.Cut(rest, "/")
		if !ok || relative == "" || roots[plane] == "" {
			return fmt.Errorf("artifact expiry requires an authorized artifact plane")
		}
		candidate := name
		if strings.HasPrefix(relative, strconv.FormatInt(plan.TaskID, 10)+"/") {
			candidate = ArtifactsMountRoot() + "/" + plane + "/" + strconv.FormatInt(plan.TaskID, 10)
		}
		shared := false
		for _, protected := range plan.Protected {
			if protected == candidate || strings.HasPrefix(protected, candidate+"/") {
				shared = true
				break
			}
		}
		if shared {
			candidate = name
			for _, protected := range plan.Protected {
				if candidate == protected {
					candidate = ""
					break
				}
			}
		}
		if candidate != "" {
			paths[candidate] = true
		}
	}
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rest := strings.TrimPrefix(name, ArtifactsMountRoot()+"/")
		plane, relative, _ := strings.Cut(rest, "/")
		root, err := os.OpenRoot(roots[plane])
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		parent := ""
		for _, part := range strings.Split(filepath.Dir(relative), string(filepath.Separator)) {
			if part == "." {
				continue
			}
			parent = filepath.Join(parent, part)
			info, statErr := root.Lstat(parent)
			if os.IsNotExist(statErr) {
				break
			}
			if statErr != nil {
				root.Close()
				return statErr
			}
			if info.Mode()&os.ModeSymlink != 0 {
				root.Close()
				return fmt.Errorf("artifact expiry refuses a path through a symbolic link")
			}
		}
		err = root.RemoveAll(relative)
		root.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func artifactState(ctx context.Context, q Q, task int64, path string) (map[string]any, error) {
	var expires, expired, token sql.NullString
	err := q.QueryRowContext(ctx, `SELECT a.expires_at,f.expired_at,a.token FROM artifact_retention a
        LEFT JOIN task_artifacts f ON f.task_id=a.task_id AND f.path=? WHERE a.task_id=?`, path, task).Scan(&expires, &expired, &token)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	state := "retained"
	if token.Valid {
		state = "expiry_pending"
	}
	if expired.Valid {
		state = "expired"
	}
	return map[string]any{"status": state, "expires_at": nullIfEmpty(expires.String), "expired_at": nullIfEmpty(expired.String), "retention_days": ArtifactRetentionDays}, nil
}
