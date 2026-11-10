package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mc/dispatch"
	"mc/domain"
)

// PacketDecide is the operator decision verb: exactly one of approve, revise,
// and cancel, on the reviewed packet and submitted version. A revise or a
// cancel requires a reason; an approve refuses one. Operator verbs only write
// state; they never dispatch.
func PacketDecide(db *sql.DB, id *RunIdentity, task int64, decision, reason string, reviewed ...int64) (any, error) {
	if err := RequireOperatorVerb(id, "packet.decide"); err != nil {
		return nil, err
	}
	switch decision {
	case "approve":
		if reason != "" {
			return nil, Domainf("--reason is forbidden for approve (asymmetric by design)")
		}
	case "revise", "cancel":
		if reason == "" {
			return nil, Domainf("--reason is required for %s (asymmetric by design)", decision)
		}
	default:
		return nil, Usagef("mc packet decide requires exactly one of --approve, --revise, --cancel")
	}

	result := map[string]any{"task_id": task}
	err := inTx(db, func(ctx context.Context, q Q) error {
		if err := requireOperatorVerbTx(ctx, q, id, "packet.decide"); err != nil {
			return err
		}
		if decision == "revise" {
			if err := domain.RequireEditableRecord(ctx, q, task, 0); err != nil {
				return err
			}
		}
		if decision != "cancel" {
			if err := domain.RequireNoRequirementsHold(ctx, q, task); err != nil {
				return err
			}
		}
		if len(reviewed) != 2 || reviewed[0] < 1 || reviewed[1] < 1 {
			return Domainf("the decision requires the reviewed packet and submitted version")
		}
		if decision == "approve" {
			if err := domain.ApproveReview(ctx, q, task, reviewed[0], reviewed[1]); err != nil {
				return err
			}
			result["decision"] = "approved"
			result["archived"] = false
			return nil
		}
		var active bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM review_versions p JOIN tasks d ON d.submitted_version=p.version_id AND d.current_review_id=p.id WHERE d.id=? AND p.id=? AND p.version_id=?)`, task, reviewed[0], reviewed[1]).Scan(&active); err != nil {
			return err
		}
		if !active {
			return Domainf("the reviewed packet is obsolete")
		}
		mark := "revise"
		if decision == "cancel" {
			mark = "cancelled"
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO review_decisions(packet_id,decision,reason) VALUES(?,?,?)`, reviewed[0], mark, reason); err != nil {
			return err
		}
		if decision == "revise" {
			result["decision"] = "revise"
			return domain.RequestPacketRevision(ctx, q, task, reason)
		}
		result["decision"] = "cancelled"
		result["archived"] = true
		return domain.Cancel(ctx, q, task, reason, "operator")
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// The heading of the opening text of a packet. The origin of the task selects it.
const (
	openingStrategicRationale = "Strategic rationale"
	openingThesis             = "Thesis"
)

// packetOpeningHeading names the opening text of a packet for its task origin.
func packetOpeningHeading(origin string) string {
	if origin == "autonomous" {
		return openingStrategicRationale
	}
	return openingThesis
}

// packetOpeningText is the opening text that Culler stores for a packet. The
// packet of a task that the Strategist proposed opens with the Packager's statement of the
// delivered change and then the Strategic rationale of the task. Each other
// packet opens with the Packager's text alone.
func packetOpeningText(ctx context.Context, q Q, task int64, statement string) (string, error) {
	var origin string
	var rationale sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT origin,strategic_rationale FROM tasks WHERE id=?`, task).Scan(&origin, &rationale); err != nil {
		return "", err
	}
	if origin != "autonomous" || !rationale.Valid {
		return statement, nil
	}
	return strings.TrimSpace(statement) + "\n\n" + strings.TrimSpace(rationale.String), nil
}

// PacketSelection limits `mc packet list` to the live queue, to the latest
// packet of one task, or to both. The zero value selects the latest packet of
// each task.
type PacketSelection struct {
	Live bool
	Task int64
}

// PacketList returns the latest packet of each task. The dashboard filters the rows in the browser.
// Each row carries one review state; the live flag selects the three states of the live queue.
// Each row carries the change data of the current merge candidate of the packet version.
// Without that change data, the row carries the change data of the merge candidate that the packet records.
// Each row names the packet of the same task that came directly before the latest packet.
// The thesis of a row is the stored opening text of the packet. For a task that the Strategist proposed, that text
// holds the Packager's statement and then the Strategic rationale.
func PacketList(db *sql.DB, selection PacketSelection) (any, error) {
	query := `
		WITH packet AS (
		SELECT t.id AS task_id, t.project, t.title, t.origin, rv.render_path, rv.thesis,
		       t.archived, 1-t.review_slot_held AS superseded, rv.requirements_version, rv.created_at,
		       rv.id AS packet_id, rv.version_id, v.input_commit, v.result_commit AS submitted_commit,
		       (SELECT MAX(e.id) FROM review_versions e WHERE e.task_id=t.id AND e.id<rv.id) AS previous_packet_id,
		       c.destination_branch, c.destination_commit, c.candidate_commit,
		       COALESCE(CASE WHEN c.version_id=rv.version_id THEN json_extract(c.result,'$.changes.candidate') END,
		                json_extract(rc.result,'$.changes.candidate')) AS candidate_changes,
		       rv.report_sha256,rv.criteria,
		       (SELECT evidence_path FROM version_verdicts WHERE version_id=rv.version_id) AS verifier_evidence,
		       CASE WHEN t.review_slot_held=0 THEN 0 ELSE
		         (rv.version_id=t.submitted_version AND t.status='packaged' AND t.archived=0 AND t.blocked=0 AND t.stalled=0 AND c.status='merged') END AS ready,
		       t.blocked, t.blocked_reason, t.blocked_kind,
		       CASE WHEN t.archived=1 AND t.decision='approved' THEN 'merged'
		            WHEN t.archived=1 THEN 'cancelled'
		            WHEN t.review_slot_held=0 THEN 'replaced'
		            WHEN t.decision='approved' THEN 'approved'
		            WHEN t.status='packaged' AND rv.version_id=t.submitted_version AND d.packet_id IS NULL THEN 'awaits_decision'
		            ELSE 'returned' END AS review_state,
		       t.decided_at AS task_decided_at, d.decision AS packet_decision, d.reason AS packet_reason,
		       (SELECT a.detail FROM activity a WHERE a.kind='task.cancelled' AND a.subject=t.id ORDER BY a.id DESC LIMIT 1) AS cancel_reason,
		       (SELECT f.status FROM merge_candidates f WHERE f.task_id=t.id AND f.version_id=rv.version_id
		          AND f.id>COALESCE(rv.candidate_id,0) AND f.status='conflict' ORDER BY f.id DESC LIMIT 1) AS failed_preparation
		FROM tasks t JOIN review_versions rv ON rv.id=t.current_review_id
		JOIN task_versions v ON v.id=rv.version_id
		LEFT JOIN merge_candidates c ON c.id=t.candidate_id
		LEFT JOIN merge_candidates rc ON rc.id=rv.candidate_id
		LEFT JOIN review_decisions d ON d.packet_id=rv.id)
		SELECT task_id, project, title, origin, render_path, thesis, archived, superseded, requirements_version, created_at,
		       packet_id, version_id, previous_packet_id, input_commit, submitted_commit,
		       destination_branch, destination_commit, candidate_commit, candidate_changes,
		       report_sha256, criteria, verifier_evidence, ready,
		       blocked, blocked_reason, blocked_kind, review_state,
		       CASE WHEN review_state IN ('approved','merged','cancelled') THEN task_decided_at END AS decided_at,
		       CASE WHEN review_state='cancelled' THEN COALESCE(cancel_reason,packet_reason)
		            WHEN review_state='returned' AND packet_decision='revise' THEN packet_reason END AS reason,
		       CASE WHEN review_state<>'returned' THEN NULL
		            WHEN packet_decision='revise' THEN 'revise' ELSE failed_preparation END AS return_cause,
		       CASE WHEN review_state='merged' THEN candidate_commit END AS merge_commit
		FROM packet`
	var where []string
	var args []any
	if selection.Live {
		where = append(where, `review_state IN ('awaits_decision','approved','returned')`)
	}
	if selection.Task > 0 {
		where = append(where, `task_id = ?`)
		args = append(args, selection.Task)
	}
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	query += ` ORDER BY task_id`
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, classify(err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()
	out, err := rowsToMaps(rows)
	if err != nil {
		return nil, classify(err)
	}
	rows.Close()
	for _, packet := range out {
		if body, ok := packet["candidate_changes"].(string); ok {
			var record map[string]any
			if err := json.Unmarshal([]byte(body), &record); err != nil {
				return nil, classify(err)
			}
			packet["candidate_changes"] = record
		}
		state, err := artifactState(ctx, tx, packet["task_id"].(int64), fmt.Sprint(packet["render_path"]))
		if err != nil {
			return nil, classify(err)
		}
		packet["artifact_retention"] = state
	}
	if err := addMergeStates(ctx, tx, out); err != nil {
		return nil, classify(err)
	}
	return map[string]any{"packets": out}, nil
}

// The operator texts of the merge states.
const (
	mergeTextMerge          = "Culler merges this change now."
	mergeTextNewCandidate   = "The %s branch changed after the review. Culler prepares a new merge candidate."
	mergeTextAfterMergeStep = "Culler merges this change after the current merge step."
	mergeTextAfterTask      = "Culler merges task #%d first."
	mergeTextTaskAttention  = " Task #%d needs your attention."
	mergeTextPaused         = "You paused the Project. Culler merges this change after you resume it."
	mergeTextAttention      = "At least three tasks need your attention. Culler merges this change when less than three tasks need your attention."
	mergeTextVision         = "Culler merges this change after the Project has a valid VISION.md."
	mergeTextRecovery       = "Culler checks the result of an interrupted merge."
	mergeTextRetry          = "The last merge attempt failed. Culler tries again in a minute."
	mergeTextStall          = "Three infrastructure failures used the retry allowance of this task. Automatic retries stopped."
)

// addMergeStates states why the merge of each approved packet is not complete.
// The state comes from the predicates that select the next merge step.
func addMergeStates(ctx context.Context, q Q, packets []map[string]any) error {
	type projectRecords struct {
		rec  dispatch.Records
		wiki dispatch.WikiMergeRecords
	}
	records := map[string]projectRecords{}
	var now time.Time
	for _, packet := range packets {
		packet["merge_state"], packet["merge_state_text"], packet["merge_task_id"] = nil, nil, nil
		if packet["review_state"] != "approved" {
			continue
		}
		project := fmt.Sprint(packet["project"])
		p, ok := records[project]
		if !ok {
			if len(records) == 0 {
				var err error
				if now, err = spineClock(ctx, q); err != nil {
					return err
				}
			}
			var err error
			if p.rec, err = loadRecords(ctx, q, project); err != nil {
				return err
			}
			var ready sql.NullBool
			if err := q.QueryRowContext(ctx, `SELECT (SELECT ready FROM vision_checks WHERE project=?)`, project).Scan(&ready); err != nil {
				return err
			}
			p.rec.VisionReady = !ready.Valid || ready.Bool
			if p.wiki, err = loadWikiMergeRecords(ctx, q, project); err != nil {
				return err
			}
			records[project] = p
		}
		task := packet["task_id"].(int64)
		state := dispatch.MergeStateOf(p.rec, p.wiki, task, now)
		text, err := mergeStateText(ctx, q, task, state)
		if err != nil {
			return err
		}
		// While the result of a merge of a wiki commit has no proof, the text
		// also states the repair of the operator checkout.
		if u := p.wiki.Update; state.Kind == dispatch.MergeStateRecovery && state.RelatedTask == 0 && u != nil && u.ApplyRun != "" && u.Repair != "" {
			text += " " + u.Repair
		}
		packet["merge_state"], packet["merge_state_text"] = string(state.Kind), text
		if state.RelatedTask != 0 {
			packet["merge_task_id"] = state.RelatedTask
		}
	}
	return nil
}

func mergeStateText(ctx context.Context, q Q, task int64, state dispatch.MergeState) (string, error) {
	switch state.Kind {
	case dispatch.MergeStateNewCandidate:
		var branch string
		if err := q.QueryRowContext(ctx, `SELECT destination_branch FROM tasks WHERE id=?`, task).Scan(&branch); err != nil {
			return "", err
		}
		return fmt.Sprintf(mergeTextNewCandidate, branch), nil
	case dispatch.MergeStateAfterMergeStep:
		return mergeTextAfterMergeStep, nil
	case dispatch.MergeStateAfterTask:
		text := fmt.Sprintf(mergeTextAfterTask, state.RelatedTask)
		if state.RelatedBlocked {
			text += fmt.Sprintf(mergeTextTaskAttention, state.RelatedTask)
		}
		return text, nil
	case dispatch.MergeStatePaused:
		return mergeTextPaused, nil
	case dispatch.MergeStateAttention:
		return mergeTextAttention, nil
	case dispatch.MergeStateVision:
		return mergeTextVision, nil
	case dispatch.MergeStateEnvironment:
		return "The Project environment requires setup or validation. Open the Project Environment page to continue.", nil
	case dispatch.MergeStateRecovery:
		return mergeTextRecovery, nil
	case dispatch.MergeStateRetry:
		return mergeTextRetry, nil
	case dispatch.MergeStateBlocked:
		var blocked, stalled bool
		var reason string
		if err := q.QueryRowContext(ctx, `SELECT tasks.blocked,tasks.dispatch_retries=0,COALESCE(tasks.blocked_reason,'')
			FROM tasks WHERE tasks.id=?`, task).Scan(&blocked, &stalled, &reason); err != nil {
			return "", err
		}
		switch {
		case blocked && reason != "":
			return reason, nil
		case stalled:
			return mergeTextStall, nil
		}
		return reason, nil
	}
	return mergeTextMerge, nil
}
