package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"mc/substrate"
)

const MaxVisionDraftBytes = 64 * 1024

type VisionDraftRequest struct {
	ID                string `json:"id"`
	Revision          string `json:"revision"`
	Content           string `json:"content"`
	WorkspaceIdentity string `json:"workspace_identity"`
	WorkspaceRevision string `json:"workspace_revision"`
	WorkspaceHasFiles bool   `json:"workspace_has_files"`
	SubmissionID      string `json:"submission_id"`
	Error             string `json:"error"`
}

type VisionDraft struct {
	ID                string            `json:"id"`
	Title             string            `json:"title"`
	WorkspacePath     string            `json:"workspace_path"`
	Content           string            `json:"content"`
	Revision          string            `json:"revision"`
	WorkspaceIdentity string            `json:"workspace_identity"`
	WorkspaceRevision string            `json:"workspace_revision"`
	WorkspaceHasFiles bool              `json:"workspace_has_files"`
	SetupStatus       string            `json:"setup_status"`
	ProjectStatus     string            `json:"project_status"`
	SetupID           string            `json:"setup_id"`
	SetupPhase        string            `json:"setup_phase"`
	Submission        *VisionSubmission `json:"submission,omitempty"`
}

type VisionSubmission struct {
	ID                string `json:"id"`
	SubmissionID      string `json:"submission_id"`
	Content           string `json:"content"`
	Revision          string `json:"revision"`
	WorkspaceIdentity string `json:"workspace_identity"`
	WorkspaceRevision string `json:"workspace_revision"`
	Status            string `json:"status"`
	Error             string `json:"error"`
	SavedRevision     string `json:"saved_revision,omitempty"`
	Attempted         bool   `json:"attempted"`
}

func visionDraftTx(ctx context.Context, q Q, project string) (VisionDraft, error) {
	var draft VisionDraft
	err := q.QueryRowContext(ctx, `SELECT d.project_id,p.title,COALESCE(s.workspace_root,''),d.content,d.revision,d.workspace_identity,d.workspace_revision,
        d.workspace_has_files,p.setup_status,p.status FROM vision_drafts d JOIN projects p ON p.id=d.project_id
        JOIN sandbox_profiles s ON s.id=p.sandbox_profile WHERE d.project_id=?`, project).
		Scan(&draft.ID, &draft.Title, &draft.WorkspacePath, &draft.Content, &draft.Revision, &draft.WorkspaceIdentity, &draft.WorkspaceRevision, &draft.WorkspaceHasFiles, &draft.SetupStatus, &draft.ProjectStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return draft, Domainf("This Project has no Vision draft. Open Project setup to create one.")
	}
	if err != nil {
		return draft, err
	}
	state, err := environmentStateTx(ctx, q, project)
	if err != nil {
		return draft, err
	}
	draft.SetupID, draft.SetupPhase = state.SetupID, state.Phase
	var submissionID string
	err = q.QueryRowContext(ctx, `SELECT id FROM vision_submissions WHERE project_id=? ORDER BY rowid DESC LIMIT 1`, project).Scan(&submissionID)
	if err == nil {
		submission, err := visionSubmissionTx(ctx, q, project, submissionID)
		if err != nil {
			return draft, err
		}
		draft.Submission = &submission
	} else if !errors.Is(err, sql.ErrNoRows) {
		return draft, err
	}
	return draft, nil
}

func visionSubmissionTx(ctx context.Context, q Q, project, submission string) (VisionSubmission, error) {
	var out VisionSubmission
	err := q.QueryRowContext(ctx, `SELECT project_id,id,content,revision,workspace_identity,workspace_revision,status,error,
		COALESCE(saved_revision,''),attempted FROM vision_submissions WHERE project_id=? AND id=?`, project, submission).
		Scan(&out.ID, &out.SubmissionID, &out.Content, &out.Revision, &out.WorkspaceIdentity, &out.WorkspaceRevision,
			&out.Status, &out.Error, &out.SavedRevision, &out.Attempted)
	if errors.Is(err, sql.ErrNoRows) {
		return out, Domainf("The Vision submission does not exist for this Project.")
	}
	return out, err
}

// VisionDraftContext supplies the current shared document for a Collie turn.
func VisionDraftContext(ctx context.Context, q Q, project string) (map[string]any, error) {
	draft, err := visionDraftTx(ctx, q, project)
	if err != nil {
		return nil, err
	}
	var title string
	if err := q.QueryRowContext(ctx, `SELECT title FROM projects WHERE id=?`, project).Scan(&title); err != nil {
		return nil, err
	}
	status := "present"
	if draft.WorkspaceRevision == "absent" {
		status = "absent"
	}
	return map[string]any{"project_id": project, "title": title, "workspace_has_files": draft.WorkspaceHasFiles,
		"workspace_vision_status": status, "draft": draft}, nil
}

func requireVisionAccess(ctx context.Context, q Q, id *RunIdentity, action, project string) error {
	switch action {
	case "vision-draft", "vision-edit", "vision-rebase", "vision-submit", "vision-submission":
		return AuthorizeOperator(ctx, q, id, "project."+action, project)
	default:
		return RequireHostScope(id, "Vision setup")
	}
}

// ProjectVision executes the closed draft and submission command surface.
func ProjectVision(db *sql.DB, id *RunIdentity, action string, req VisionDraftRequest) (any, error) {
	if action != "vision-pending" && !ValidProjectID(req.ID) {
		return nil, Usagef("A valid Project identifier is required.")
	}
	if !utf8.ValidString(req.Content) || strings.ContainsRune(req.Content, 0) || len(req.Content) > MaxVisionDraftBytes {
		return nil, Usagef("The Vision must contain valid text of at most 64 KiB.")
	}
	var result any
	err := inTx(db, func(ctx context.Context, q Q) error {
		if err := requireVisionAccess(ctx, q, id, action, req.ID); err != nil {
			return err
		}
		if action == "vision-pending" {
			rows, err := q.QueryContext(ctx, `SELECT project_id AS id,id AS submission_id FROM vision_submissions WHERE status='pending' ORDER BY rowid`)
			if err != nil {
				return err
			}
			defer rows.Close()
			items, err := rowsToMaps(rows)
			result = map[string]any{"submissions": items}
			return err
		}
		var status string
		if err := q.QueryRowContext(ctx, `SELECT status FROM projects WHERE id=?`, req.ID).Scan(&status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Domainf("This Project no longer exists.")
			}
			return err
		}
		if status == "archived" && action != "vision-draft" && action != "vision-submission" && action != "vision-claim-apply" && action != "vision-fail" {
			return Domainf("An archived Project permits inspection only.")
		}
		if action == "vision-init" {
			if req.WorkspaceIdentity == "" || req.WorkspaceRevision == "" {
				return Usagef("The workspace identity and Vision revision are required.")
			}
			revision, err := newRunID()
			if err != nil {
				return err
			}
			_, err = q.ExecContext(ctx, `INSERT INTO vision_drafts(project_id,content,revision,workspace_identity,workspace_revision,workspace_has_files)
                VALUES(?,?,?,?,?,?) ON CONFLICT(project_id) DO NOTHING`, req.ID, req.Content, revision, req.WorkspaceIdentity,
				req.WorkspaceRevision, req.WorkspaceHasFiles)
			if err != nil {
				return err
			}
		} else if action == "vision-submission" {
			submission, err := visionSubmissionTx(ctx, q, req.ID, req.SubmissionID)
			result = map[string]any{"submission": submission}
			return err
		} else if action == "vision-claim-apply" {
			submission, err := visionSubmissionTx(ctx, q, req.ID, req.SubmissionID)
			if err != nil {
				return err
			}
			if submission.Status == "pending" {
				if _, err := q.ExecContext(ctx, `UPDATE vision_submissions SET attempted=1 WHERE id=?`, req.SubmissionID); err != nil {
					return err
				}
			}
			result = map[string]any{"submission": submission, "already_attempted": submission.Attempted}
			return nil
		}
		draft, err := visionDraftTx(ctx, q, req.ID)
		if err != nil {
			return err
		}
		switch action {
		case "vision-init":
			if req.WorkspaceIdentity != draft.WorkspaceIdentity {
				return Domainf("The Project folder changed. The Vision draft remains available for comparison.")
			}
		case "vision-draft":
		case "vision-edit":
			if req.Revision != draft.Revision {
				return Domainf("The Vision draft changed. Compare the current draft with your text before another edit.")
			}
			if req.Content != draft.Content {
				revision, err := newRunID()
				if err != nil {
					return err
				}
				if _, err := q.ExecContext(ctx, `UPDATE vision_drafts SET content=?,revision=?,updated_at=datetime('now') WHERE project_id=?`, req.Content, revision, req.ID); err != nil {
					return err
				}
			}
		case "vision-rebase":
			if req.Revision != draft.Revision || req.WorkspaceIdentity != draft.WorkspaceIdentity || req.WorkspaceRevision == "" {
				return Domainf("The Vision draft or its folder changed. Compare the current versions before another save.")
			}
			if draft.Submission != nil && draft.Submission.Status == "pending" {
				return Domainf("Wait for the current Vision save before comparison.")
			}
			if _, err := q.ExecContext(ctx, `UPDATE vision_drafts SET workspace_revision=? WHERE project_id=?`, req.WorkspaceRevision, req.ID); err != nil {
				return err
			}
		case "vision-submit":
			if err := submitVisionTx(ctx, q, req, draft); err != nil {
				return err
			}
		case "vision-start":
			if err := ValidateVisionStart(draft, req); err != nil {
				return err
			}
			if err := initializeEnvironmentTx(ctx, q, req.ID, req.Revision); err != nil {
				return err
			}
			if err := substrate.ValidateDispatchMountProjection(ctx, q); err != nil {
				return err
			}
		case "vision-complete", "vision-fail":
			if err := finishVisionTx(ctx, q, action, req); err != nil {
				return err
			}
		default:
			return Usagef("Unknown Vision action.")
		}
		draft, err = visionDraftTx(ctx, q, req.ID)
		if err != nil {
			return err
		}
		// The transaction serializes file replacement with both editor types.
		// The spine copy repairs a file after an interrupted transaction.
		if err := persistVisionDraft(ctx, q, draft); err != nil {
			return err
		}
		response := map[string]any{"draft": draft}
		if action == "vision-submit" {
			var submissionID string
			if err := q.QueryRowContext(ctx, `SELECT id FROM vision_submissions WHERE project_id=? AND revision=? AND workspace_revision=?`,
				req.ID, req.Revision, req.WorkspaceRevision).Scan(&submissionID); err != nil {
				return err
			}
			submission, err := visionSubmissionTx(ctx, q, req.ID, submissionID)
			if err != nil {
				return err
			}
			response["submission"] = submission
		}
		result = response
		return nil
	})
	return result, err
}

// ValidateVisionStart binds initialization to the confirmed workspace submission.
func ValidateVisionStart(draft VisionDraft, req VisionDraftRequest) error {
	if draft.SetupStatus == "ready" && draft.ProjectStatus != "active" {
		return Domainf("Initialization is complete. Use Resume Project to continue work.")
	}
	submission := draft.Submission
	if submission == nil || submission.Status != "saved" {
		return Domainf("Initialization is incomplete. Use Save Vision and wait for the file save before you start the pipeline.")
	}
	if req.ID != draft.ID || req.Revision != draft.Revision || submission.Revision != draft.Revision || req.SubmissionID != submission.SubmissionID {
		return Domainf("The Vision draft changed. Use Save Vision before you start the pipeline.")
	}
	if req.WorkspaceIdentity != draft.WorkspaceIdentity || req.WorkspaceIdentity != submission.WorkspaceIdentity ||
		req.WorkspaceRevision != submission.SavedRevision || req.WorkspaceRevision != draft.WorkspaceRevision {
		return Domainf("The workspace Vision changed. Compare it with the draft and use Save Vision before you start the pipeline.")
	}
	return nil
}

func submitVisionTx(ctx context.Context, q Q, req VisionDraftRequest, draft VisionDraft) error {
	var prior string
	err := q.QueryRowContext(ctx, `SELECT id FROM vision_submissions WHERE project_id=? AND revision=? AND workspace_revision=?`,
		req.ID, req.Revision, req.WorkspaceRevision).Scan(&prior)
	if err == nil {
		old, err := visionSubmissionTx(ctx, q, req.ID, prior)
		if err != nil {
			return err
		}
		if old.WorkspaceIdentity != req.WorkspaceIdentity {
			return Domainf("The Project folder changed. Open Project setup again.")
		}
		if old.Status != "failed" {
			return nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if req.Revision != draft.Revision || req.WorkspaceRevision != draft.WorkspaceRevision || req.WorkspaceIdentity != draft.WorkspaceIdentity {
		return Domainf("The Vision draft or workspace changed. Compare the current versions before another save.")
	}
	if !checkProjectVisionContent("", []byte(draft.Content)).Ready {
		return Domainf("Add the Project goal before you save the Vision.")
	}
	if draft.Submission != nil && draft.Submission.Status == "pending" {
		return Domainf("The current Vision save must finish before another submission.")
	}
	if prior != "" {
		_, err := q.ExecContext(ctx, `UPDATE vision_submissions SET status='pending',error='',attempted=0 WHERE id=?`, prior)
		return err
	}
	submission, err := newRunID()
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO vision_submissions(id,project_id,content,revision,workspace_identity,workspace_revision)
        VALUES(?,?,?,?,?,?)`, submission, req.ID, draft.Content, draft.Revision, draft.WorkspaceIdentity, draft.WorkspaceRevision)
	return err
}

func finishVisionTx(ctx context.Context, q Q, action string, req VisionDraftRequest) error {
	submission, err := visionSubmissionTx(ctx, q, req.ID, req.SubmissionID)
	if err != nil {
		return err
	}
	if submission.Status == "saved" {
		if action == "vision-complete" && req.WorkspaceRevision != submission.SavedRevision {
			return Domainf("The saved Vision revision differs from this completion.")
		}
		return nil
	}
	if action == "vision-fail" {
		if req.Error == "" || len(req.Error) > 4096 {
			return Usagef("A concise save error is required.")
		}
		_, err = q.ExecContext(ctx, `UPDATE vision_submissions SET status='failed',error=? WHERE id=?`, req.Error, req.SubmissionID)
		return err
	}
	if submission.Status != "pending" {
		return Domainf("This Vision submission requires a retry before completion.")
	}
	if req.WorkspaceRevision != visionRevision([]byte(submission.Content)) {
		return Domainf("The workspace file does not match the submitted Vision.")
	}
	if _, err := q.ExecContext(ctx, `UPDATE vision_submissions SET status='saved',error='',saved_revision=? WHERE id=?`, req.WorkspaceRevision, req.SubmissionID); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE vision_drafts SET workspace_revision=? WHERE project_id=?`, req.WorkspaceRevision, req.ID); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `UPDATE projects SET
        status=CASE WHEN setup_status='ready' THEN status ELSE 'paused' END,
        setup_status=CASE WHEN setup_status='ready' THEN 'ready' ELSE 'configured' END,
        vision='present' WHERE id=?`, req.ID)
	return err
}

func persistVisionDraft(ctx context.Context, q Q, draft VisionDraft) error {
	rows, err := q.QueryContext(ctx, `PRAGMA database_list`)
	if err != nil {
		return err
	}
	var databasePath string
	for rows.Next() {
		var seq int
		var name, path string
		if err := rows.Scan(&seq, &name, &path); err != nil {
			rows.Close()
			return err
		}
		if name == "main" {
			databasePath = path
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if databasePath == "" {
		return Domainf("The Vision draft requires persistent Culler storage.")
	}
	root := filepath.Join(filepath.Dir(databasePath), "vision-drafts")
	for _, dir := range []string{root, filepath.Join(root, draft.ID)} {
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return Domainf("The Vision draft storage is unavailable.")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm() != 0o700 {
			return Domainf("The Vision draft storage permissions are invalid.")
		}
	}
	dir := filepath.Join(root, draft.ID)
	body, err := json.Marshal(draft)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".draft-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(body); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), filepath.Join(dir, "draft.json")); err != nil {
		return err
	}
	folder, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer folder.Close()
	return folder.Sync()
}
