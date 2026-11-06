package verbs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"mc/domain"
)

type BoardWriteRequest struct {
	OperationID     string  `json:"operation_id"`
	Action          string  `json:"action"`
	TaskID          int64   `json:"task_id,omitempty"`
	ExpectedVersion int64   `json:"expected_version,omitempty"`
	Scope           string  `json:"scope,omitempty"`
	Project         string  `json:"project,omitempty"`
	Title           *string `json:"title,omitempty"`
	Priority        *int    `json:"priority,omitempty"`
	Description     *string `json:"description,omitempty"`
	Reason          *string `json:"reason,omitempty"`
}

func DecodeBoardWriteRequest(body []byte) (BoardWriteRequest, error) {
	var request BoardWriteRequest
	if len(body) > 1024*1024 {
		return request, domain.Errf(domain.CodeInvalidRequest, "The request exceeds one megabyte.")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, domain.Errf(domain.CodeInvalidRequest, "The request contains an invalid field: %v", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return request, domain.Errf(domain.CodeInvalidRequest, "The request must contain one object.")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return request, domain.Errf(domain.CodeInvalidRequest, "The request must contain one object.")
	}
	allowed := map[string]bool{"operation_id": true, "action": true}
	switch request.Action {
	case "create":
		for _, key := range []string{"scope", "project", "title", "priority", "description"} {
			allowed[key] = true
		}
	case "edit":
		for _, key := range []string{"task_id", "expected_version", "title", "priority"} {
			allowed[key] = true
		}
	case "revise":
		for _, key := range []string{"task_id", "expected_version", "description", "reason"} {
			allowed[key] = true
		}
	case "block", "unblock", "resume", "cancel", "allow-corrections":
		for _, key := range []string{"task_id", "expected_version", "reason"} {
			allowed[key] = true
		}
	}
	for key, value := range fields {
		if !allowed[key] || string(value) == "null" {
			return request, domain.Errf(domain.CodeInvalidRequest, "The %s action does not accept the %s field.", request.Action, key)
		}
	}
	return request, validateBoardRequest(request)
}

func validateBoardRequest(r BoardWriteRequest) error {
	if len(r.OperationID) < 1 || len(r.OperationID) > 128 || !utf8.ValidString(r.OperationID) {
		return domain.Errf(domain.CodeInvalidRequest, "The request requires an operation identifier with at most 128 bytes.")
	}
	for _, ch := range r.OperationID {
		if ch <= 32 || ch == 127 {
			return domain.Errf(domain.CodeInvalidRequest, "The operation identifier must contain no spaces or control characters.")
		}
	}
	if r.Title != nil {
		if strings.TrimSpace(*r.Title) == "" || len(*r.Title) > 4096 || !utf8.ValidString(*r.Title) {
			return domain.Errf(domain.CodeInvalidRequest, "The title requires valid text with at most 4096 bytes.")
		}
		for _, ch := range *r.Title {
			if ch < 32 || ch == 127 {
				return domain.Errf(domain.CodeInvalidRequest, "The title must contain no control characters.")
			}
		}
	}
	if r.Priority != nil && (*r.Priority < -1 || *r.Priority > 3) {
		return domain.Errf(domain.CodeInvalidRequest, "The priority must be between -1 and 3.")
	}
	switch r.Action {
	case "create":
		if r.TaskID != 0 || r.ExpectedVersion != 0 || r.Reason != nil || r.Title == nil || r.Project == "" || r.Scope != "task" {
			return domain.Errf(domain.CodeInvalidRequest, "Creation requires a type, title, and Project.")
		}
	case "edit", "revise", "block", "unblock", "resume", "cancel", "allow-corrections":
		if r.TaskID < 1 || r.ExpectedVersion < 1 || r.Scope != "" || r.Project != "" {
			return domain.Errf(domain.CodeInvalidRequest, "The action requires the record identifier and its displayed version.")
		}
		if r.Action == "edit" {
			if (r.Title == nil && r.Priority == nil) || r.Description != nil || r.Reason != nil {
				return domain.Errf(domain.CodeInvalidRequest, "An edit accepts a title or priority.")
			}
		} else {
			if r.Title != nil || r.Priority != nil {
				return domain.Errf(domain.CodeInvalidRequest, "The action does not accept a title or priority.")
			}
			if r.Reason == nil || strings.TrimSpace(*r.Reason) == "" {
				return domain.Errf(domain.CodeReasonRequired, "The action requires a reason.")
			}
			if r.Action != "revise" && r.Description != nil {
				return domain.Errf(domain.CodeInvalidRequest, "The action does not accept requirements.")
			}
			if r.Action == "revise" && r.Description == nil {
				return domain.Errf(domain.CodeInvalidRequest, "The revision requires revised requirements.")
			}
		}
	default:
		return domain.Errf(domain.CodeInvalidRequest, "The board action is invalid.")
	}
	return nil
}

// BoardWrite stores each accepted request and its receipt in one transaction.
func BoardWrite(db *sql.DB, id *RunIdentity, request BoardWriteRequest) (any, error) {
	if err := RequireOperatorVerb(id, "board.write"); err != nil {
		return nil, err
	}
	if err := validateBoardRequest(request); err != nil {
		return nil, err
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	err = inTx(db, func(ctx context.Context, q Q) error {
		if err := requireOperatorVerbTx(ctx, q, id, "board.write"); err != nil {
			return err
		}
		var priorBody, receipt string
		err := q.QueryRowContext(ctx, `SELECT request,result FROM board_operations WHERE operation_id=?`, request.OperationID).Scan(&priorBody, &receipt)
		if err == nil {
			if priorBody != string(body) {
				return domain.Errf(domain.CodeOperationConflict, "The operation identifier already belongs to a different request.")
			}
			decoder := json.NewDecoder(strings.NewReader(receipt))
			decoder.UseNumber()
			return decoder.Decode(&result)
		}
		if err != sql.ErrNoRows {
			return err
		}
		actor := "operator"
		if id != nil {
			actor = "collie:" + id.RunID
		}
		task := request.TaskID
		previous := map[string]any{}
		if request.Action != "create" {
			if err := domain.RequireRecordVersion(ctx, q, task, request.ExpectedVersion); err != nil {
				return err
			}
			if err := domain.RequireOperatorTaskControl(ctx, q, task); err != nil {
				return err
			}
			previous, err = boardCurrentValues(ctx, q, task)
			if err != nil {
				return err
			}
		}
		result = map[string]any{"operation_id": request.OperationID, "action": request.Action}
		switch request.Action {
		case "create":
			requirements := ""
			if request.Description != nil {
				requirements = *request.Description
			}
			task, err = domain.BirthOperatorWork(ctx, q, domain.ProposalArgs{Title: *request.Title, Description: requirements, Scope: request.Scope, Priority: request.Priority, Project: request.Project})
			if err != nil {
				return err
			}
		case "edit":
			if err := domain.RequireEditableRecord(ctx, q, task, request.ExpectedVersion); err != nil {
				return err
			}
			if _, err := q.ExecContext(ctx, `UPDATE tasks SET title=COALESCE(?,title),priority=COALESCE(?,priority) WHERE id=?`, request.Title, request.Priority, task); err != nil {
				return err
			}
		case "revise":
			revision, err := domain.RequestRequirementsRevision(ctx, q, task, request.ExpectedVersion, request.OperationID, *request.Description, *request.Reason, actor)
			if err != nil {
				return err
			}
			var state string
			if err := q.QueryRowContext(ctx, `SELECT state FROM requirements_revisions WHERE id=?`, revision).Scan(&state); err != nil {
				return err
			}
			result["revision_id"] = revision
			result["revision_state"] = state
		case "block":
			if err := blockTaskTx(ctx, q, task, domain.BlockOperator, *request.Reason, actor); err != nil {
				return err
			}
			result["blocked"] = true
		case "unblock":
			revision, err := unblockTaskTx(ctx, q, task, *request.Reason, actor)
			if err != nil {
				return err
			}
			result["blocked"] = false
			if revision != 0 {
				result["revision_id"] = revision
				result["revision_state"] = "pending"
			}
		case "allow-corrections":
			if err := domain.GrantCorrectionAllowance(ctx, q, task, *request.Reason); err != nil {
				return err
			}
		case "resume":
			if err := resumeTaskTx(ctx, q, task, *request.Reason, actor); err != nil {
				return err
			}
		case "cancel":
			if err := domain.Cancel(ctx, q, task, *request.Reason, actor); err != nil {
				return err
			}
			result["cancelled"] = true
		}
		var version int64
		var scope string
		if err := q.QueryRowContext(ctx, `SELECT record_version,scope FROM tasks WHERE id=?`, task).Scan(&version, &scope); err != nil {
			return err
		}
		result["task_id"] = task
		result["scope"] = scope
		result["record_version"] = version
		previousJSON, err := json.Marshal(previous)
		if err != nil {
			return err
		}
		resultJSON, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `INSERT INTO board_operations(operation_id,task_id,action,actor,request,previous_values,result,reason) VALUES(?,?,?,?,?,?,?,?)`, request.OperationID, task, request.Action, actor, string(body), string(previousJSON), string(resultJSON), request.Reason)
		return err
	})
	return result, err
}

func boardCurrentValues(ctx context.Context, q Q, task int64) (map[string]any, error) {
	rows, err := q.QueryContext(ctx, `SELECT title,COALESCE(description,'') AS requirements,priority,status,record_version,blocked,blocked_reason,blocked_kind,decision,archived FROM tasks WHERE id=?`, task)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values, err := taskRowsToMaps(rows)
	if err != nil {
		return nil, err
	}
	if len(values) != 1 {
		return nil, domain.Errf(domain.CodeNotFound, "The record does not exist.")
	}
	return values[0], nil
}

func blockTaskTx(ctx context.Context, q Q, task int64, kind, reason, actor string) error {
	if strings.TrimSpace(reason) == "" {
		return domain.Errf(domain.CodeReasonRequired, "Block requires a reason.")
	}
	var decision, current sql.NullString
	var archived bool
	if err := q.QueryRowContext(ctx, `SELECT decision,archived,CASE WHEN blocked=1 THEN blocked_kind END FROM tasks WHERE id=?`, task).Scan(&decision, &archived, &current); err != nil {
		return err
	}
	if decision.Valid || archived {
		return domain.Errf(domain.CodeAlreadyDecided, "The finalized record is read-only.")
	}
	// An operator block would replace the report, and a reason would then
	// resolve it without a requirements revision.
	if kind == domain.BlockOperator && current.String == domain.BlockReport {
		return domain.Errf(domain.CodeIllegalTransition, "Resolve this operator decision report with a requirements revision, or cancel the task.")
	}
	if err := domain.Block(ctx, q, task, kind, reason); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO activity(actor,kind,subject,detail) VALUES(?,'task.block_reason',?,?)`, actor, task, reason)
	return err
}

func unblockTaskTx(ctx context.Context, q Q, task int64, reason, actor string) (int64, error) {
	if strings.TrimSpace(reason) == "" {
		return 0, domain.Errf(domain.CodeReasonRequired, "Unblock requires a reason.")
	}
	var blocked, stalled, archived bool
	var decision, kind sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT blocked,stalled,archived,decision,blocked_kind FROM tasks WHERE id=?`, task).Scan(&blocked, &stalled, &archived, &decision, &kind); err != nil {
		return 0, err
	}
	if archived || decision.Valid && decision.String != "approved" {
		return 0, domain.Errf(domain.CodeAlreadyDecided, "The finalized record is read-only.")
	}
	if blocked && kind.String == domain.BlockReport {
		return 0, domain.Errf(domain.CodeIllegalTransition, "Resolve this operator decision report with a requirements revision, or cancel the task.")
	}
	if blocked {
		if err := domain.Unblock(ctx, q, task); err != nil {
			return 0, err
		}
	} else {
		return 0, domain.Errf(domain.CodeNotBlocked, "The record has no Decision Pending condition.")
	}
	if _, err := q.ExecContext(ctx, `UPDATE customization_conflicts SET cleared_at=datetime('now'),cleared_by=?,resolution=?
 WHERE cleared_at IS NULL AND run_id IN (SELECT id FROM runs WHERE subject=?)`, actor, reason, task); err != nil {
		return 0, err
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO activity(actor,kind,subject,detail) VALUES(?,'task.unblock_reason',?,?)`, actor, task, reason); err != nil {
		return 0, err
	}
	return 0, nil
}

func recordBoardDetails(ctx context.Context, q Q, task int64, record map[string]any) error {
	rows, err := q.QueryContext(ctx, `SELECT id,operation_id,requirements,previous_requirements,reason,state,actor,requested_at,applied_at,failure_reason FROM requirements_revisions WHERE task_id=? ORDER BY id DESC`, task)
	if err != nil {
		return err
	}
	revisions, err := rowsToMaps(rows)
	rows.Close()
	if err != nil {
		return err
	}
	record["requirements_history"] = revisions
	record["pending_revision"] = nil
	for _, revision := range revisions {
		if revision["state"] == "pending" {
			var run sql.NullString
			if err := q.QueryRowContext(ctx, `SELECT (SELECT l.run_id FROM leases l JOIN run_requirements rr ON rr.run_id=l.run_id WHERE rr.task_id=? LIMIT 1)`, task).Scan(&run); err != nil {
				return err
			}
			var execution bool
			if !run.Valid {
				if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM repository_executions x JOIN run_requirements rr ON rr.run_id=x.run_id
					WHERE x.ended_at IS NULL AND x.operation<>'cleanup' AND (rr.task_id=?))`, task).Scan(&execution); err != nil {
					return err
				}
			}
			if run.Valid {
				revision["active_run_id"] = run.String
				revision["wait_reason"] = "mc saved your revision request. The current run will finish before mc applies your changes."
			} else if execution {
				revision["wait_reason"] = "mc saved your revision request. A repository operation will finish before mc applies your changes."
			} else {
				var capture bool
				if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM version_submissions WHERE task_id=? AND captured_version IS NULL)`, task).Scan(&capture); err != nil {
					return err
				}
				if capture {
					revision["wait_reason"] = "mc must preserve the current run result before it applies your changes."
				}
			}
			record["pending_revision"] = revision
			break
		}
	}
	rows, err = q.QueryContext(ctx, `SELECT operation_id,action,actor,created_at,previous_values AS previous,request AS requested,reason,result AS outcome FROM board_operations WHERE task_id=? ORDER BY created_at DESC,rowid DESC`, task)
	if err != nil {
		return err
	}
	history, err := rowsToMaps(rows)
	rows.Close()
	if err != nil {
		return err
	}
	for _, event := range history {
		for _, key := range []string{"previous", "requested", "outcome"} {
			var value any
			decoder := json.NewDecoder(strings.NewReader(fmt.Sprint(event[key])))
			decoder.UseNumber()
			if err := decoder.Decode(&value); err != nil {
				return err
			}
			event[key] = value
		}
	}
	record["board_history"] = history
	for key, query := range map[string]string{
		"packet_history":   `SELECT p.*,r.id AS revision_id,1 AS superseded,r.requested_at AS recorded_at FROM review_versions p JOIN requirements_revisions r ON r.id=(SELECT min(id) FROM requirements_revisions WHERE task_id=p.task_id AND previous_requirements_version=p.requirements_version) WHERE p.task_id=? ORDER BY p.id DESC`,
		"evidence_history": `SELECT * FROM requirements_evidence WHERE task_id=? ORDER BY id DESC`,
		"run_requirements": `SELECT * FROM run_requirements WHERE task_id=? ORDER BY run_id`,
		"artifacts":        `SELECT f.path,f.expired_at,a.expires_at FROM task_artifacts f LEFT JOIN artifact_retention a ON a.task_id=f.task_id WHERE f.task_id=? ORDER BY f.path`,
	} {
		rows, err := q.QueryContext(ctx, query, task)
		if err != nil {
			return err
		}
		values, err := rowsToMaps(rows)
		rows.Close()
		if err != nil {
			return err
		}
		record[key] = values
	}
	rows, err = q.QueryContext(ctx, `SELECT r.* FROM runs r JOIN tasks t ON t.project=r.project WHERE t.id=? AND
 (r.subject=t.id OR r.role='arbiter' AND r.subject IS NULL AND EXISTS(SELECT 1 FROM json_each(COALESCE(r.pool_snapshot,'[]')) p WHERE p.value=t.id)) ORDER BY r.created_seq`, task)
	if err != nil {
		return err
	}
	runHistory, err := rowsToMaps(rows)
	rows.Close()
	if err != nil {
		return err
	}
	for _, run := range runHistory {
		run["agentsview"] = agentsViewSession(run["binding"], run["native_session_ref"])
	}
	record["run_history"] = runHistory
	record["edit_restriction"] = nil
	if record["status"] == "proposed" {
		record["edit_restriction"] = "Proposed tasks remain read-only until Arbiter proposal review ends."
	}
	return nil
}
