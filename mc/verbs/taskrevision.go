package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mc/domain"
)

const revisionPageSize = 6000

// TaskRevisionRead returns one immutable revision through bounded, separate views.
func TaskRevisionRead(db *sql.DB, id *RunIdentity, task, revision int64, view string, offset int) (any, error) {
	if task < 1 || revision < 1 || offset < 0 {
		return nil, Usagef("The task, revision, and offset must identify a valid revision page.")
	}
	if id != nil && id.Tier == "pipeline" {
		var assigned bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM run_requirements WHERE run_id=? AND task_id=? AND revision_id>=?)`, id.RunID, task, revision).Scan(&assigned); err != nil {
			return nil, err
		}
		if !assigned {
			return nil, Domainf("Read this run's revision or an earlier revision of its task.")
		}
	}
	var before, after, instructions, state string
	var title, oldTitle sql.NullString
	err := db.QueryRow(`SELECT previous_requirements,requirements,reason,state,title,previous_title
	 FROM requirements_revisions WHERE id=? AND task_id=?`, revision, task).Scan(&before, &after, &instructions, &state, &title, &oldTitle)
	if err == sql.ErrNoRows {
		return nil, domain.Errf(domain.CodeNotFound, "The task revision does not exist.")
	}
	if err != nil {
		return nil, err
	}
	result := map[string]any{"task_id": task, "revision_id": revision, "state": state, "view": view}
	var text string
	switch view {
	case "summary":
		result["title"] = title.String
		result["previous_title"] = oldTitle.String
		result["description_changed"] = before != after
		result["instructions_characters"] = len([]rune(instructions))
		result["views"] = []string{"instructions", "earlier-instructions", "changes", "evidence"}
		return result, nil
	case "instructions":
		text = instructions
	case "earlier-instructions":
		text, err = earlierRevisionPointers(context.Background(), db, task, revision)
		if err != nil {
			return nil, err
		}
	case "changes":
		text, err = revisionDescriptionDiff(before, after)
		if err != nil {
			return nil, err
		}
	case "evidence":
		var retained sql.NullString
		err = db.QueryRow(`SELECT delivery FROM requirements_evidence WHERE revision_id=?`, revision).Scan(&retained)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		var evidence map[string]any
		if retained.Valid {
			if err := json.Unmarshal([]byte(retained.String), &evidence); err != nil {
				return nil, err
			}
		}
		pointers := map[string]any{}
		for _, key := range []string{"checkpoint_version", "submitted_version", "report_path", "worker_report", "verifier_evidence", "verifier_correction", "base_commit", "input_commit"} {
			if value := evidence[key]; value != nil {
				pointers[key] = value
			}
		}
		result["previous_evidence"] = pointers
		result["start_commit"], err = revisionStartCommit(context.Background(), db, revision)
		return result, err
	default:
		return nil, Usagef("The revision view must be summary, instructions, earlier-instructions, changes, or evidence.")
	}
	runes := []rune(text)
	if offset > len(runes) {
		return nil, Usagef("The offset exceeds the revision text.")
	}
	end := min(offset+revisionPageSize, len(runes))
	result["text"], result["offset"], result["total_characters"] = string(runes[offset:end]), offset, len(runes)
	result["next_offset"] = nil
	if end < len(runes) {
		result["next_offset"] = end
	}
	return result, nil
}

func earlierRevisionPointers(ctx context.Context, q Q, task, revision int64) (string, error) {
	rows, err := q.QueryContext(ctx, `SELECT id FROM requirements_revisions
	 WHERE task_id=? AND state='applied' AND id<? AND length(trim(reason))>0
	 AND id>COALESCE((SELECT json_extract(delivery,'$.verified_revision') FROM requirements_evidence WHERE revision_id=?),0)
	 ORDER BY id`, task, revision, revision)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var text strings.Builder
	for rows.Next() {
		var earlier int64
		if err := rows.Scan(&earlier); err != nil {
			return "", err
		}
		fmt.Fprintf(&text, "Revision %d: mc task revision %d --revision %d --view instructions\n", earlier, task, earlier)
	}
	return text.String(), rows.Err()
}

func revisionStartCommit(ctx context.Context, q Q, revision int64) (string, error) {
	var commit string
	err := q.QueryRowContext(ctx, `SELECT COALESCE(
	 (SELECT result_commit FROM task_versions WHERE id=json_extract(e.delivery,'$.checkpoint_version')),
	 (SELECT result_commit FROM task_versions WHERE id=json_extract(e.delivery,'$.submitted_version')),
	 json_extract(e.delivery,'$.input_commit'),json_extract(e.delivery,'$.base_commit'),'')
	 FROM requirements_evidence e WHERE e.revision_id=?`, revision).Scan(&commit)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return commit, err
}

func revisionDescriptionDiff(before, after string) (string, error) {
	if before == after {
		return "", nil
	}
	dir, err := os.MkdirTemp("", "culler-revision-diff-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	for name, text := range map[string]string{"previous-description": before, "current-description": after} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			return "", err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "--no-pager", "diff", "--no-index", "--no-ext-diff", "--no-textconv", "--no-color", "--unified=3", "--", "previous-description", "current-description")
	cmd.Dir = dir
	output, err := cmd.Output()
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		return "", fmt.Errorf("read the description diff: %w", err)
	}
	return string(output), nil
}
