package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"mc/domain"
	"mc/trace"
)

// ProjectEnvironment owns environment state and the separate setup phase.
func ProjectEnvironment(db *sql.DB, id *RunIdentity, action string, req EnvironmentRequest) (any, error) {
	var result any
	err := inTx(db, func(ctx context.Context, q Q) error {
		if action == "run-evidence" {
			if id == nil || id.Tier != "pipeline" || id.RunID == "" || id.RunID != req.RunID {
				return Domainf("Run environment evidence requires its own pipeline identity.")
			}
			if _, err := domain.FenceAgentRun(ctx, q, id.RunID); err != nil {
				return err
			}
			err := environmentRunEvidenceTx(ctx, q, req)
			result = map[string]any{"run_id": req.RunID, "recorded": err == nil}
			return err
		}
		if action == "propose" {
			if id == nil || id.Tier != "pipeline" || id.Role != "worker" {
				return Domainf("Only an assigned Worker can propose a pipeline environment.")
			}
			subject, err := fenceRun(ctx, q, id.RunID)
			if err != nil {
				return err
			}
			if subject == nil {
				return Domainf("An environment proposal requires an assigned task.")
			}
			project, err := runProject(ctx, q, id.RunID)
			if err != nil {
				return err
			}
			req.ProjectID = project
		} else if action == "report" || (action == "heartbeat" && id != nil) {
			if id == nil || id.Tier != "setup" || id.Role != "setup" || id.RunID == "" || id.RunID != req.OperationID {
				return Domainf("This operation requires its setup agent identity.")
			}
		} else if action == "get" && id != nil {
			if id.Tier == "collie" {
				if err := RequireOperatorVerb(id, "environment.get"); err != nil {
					return err
				}
				if err := requireActiveCollieVerb(ctx, q, id, "environment.get"); err != nil {
					return err
				}
				var project sql.NullString
				if err := q.QueryRowContext(ctx, `SELECT vision_project_id FROM collie_sessions WHERE id=?`, id.RunID).Scan(&project); err != nil {
					return err
				}
				if project.Valid && project.String != req.ProjectID {
					return Domainf("This Vision conversation belongs to another Project.")
				}
			} else if id.Tier == "pipeline" && (id.Role == "worker" || id.Role == "verifier") {
				project, err := runProject(ctx, q, id.RunID)
				if err != nil {
					return err
				}
				if project != req.ProjectID {
					return Domainf("This run belongs to another Project.")
				}
			} else {
				if id.Tier != "setup" || id.Role != "setup" {
					return Domainf("Environment inspection requires the assigned setup identity.")
				}
				op, err := environmentOperationTx(ctx, q, id.RunID)
				if err != nil {
					return err
				}
				if req.ProjectID != op.ProjectID {
					return Domainf("This setup agent belongs to another Project.")
				}
			}
		} else if isOperatorEnvironmentAction(action) {
			if err := AuthorizeOperator(ctx, q, id, "environment."+action, req.ProjectID); err != nil {
				return err
			}
		} else if err := RequireHostScope(id, "Project environment "+action); err != nil {
			return err
		}
		switch action {
		case "base-record":
			reference, _ := req.Metadata["registry_reference"].(string)
			release, _ := req.Metadata["release_identity"].(string)
			if !imageDigest.MatchString(req.BaseDigest) || (reference != "" && !environmentRegistryDigest.MatchString(reference)) || release == "" || len(release) > 256 {
				return Usagef("A Culler base requires its exact image digest and release identity. A registry source requires an exact digest.")
			}
			_, err := q.ExecContext(ctx, `INSERT INTO environment_base_sources(base_digest,registry_reference,release_identity) VALUES(?,?,?) ON CONFLICT(base_digest,registry_reference) DO NOTHING`, req.BaseDigest, reference, release)
			result = map[string]any{"recorded": err == nil}
			return err
		case "selections":
			rows, err := q.QueryContext(ctx, `SELECT e.project_id,e.selected_revision_id AS revision_id,e.selected_image_digest AS image_digest,e.selected_base_digest AS base_digest,COALESCE(c.image_digest,'') AS checked_image_digest,COALESCE(c.runtime_identity,'') AS checked_runtime_identity,COALESCE(c.status,'') AS checked_status FROM project_environments e JOIN projects p ON p.id=e.project_id LEFT JOIN environment_runtime_checks c ON c.project_id=e.project_id WHERE e.selected_revision_id IS NOT NULL AND p.status<>'archived' ORDER BY e.project_id`)
			if err != nil {
				return err
			}
			defer rows.Close()
			selections, err := rowsToMaps(rows)
			result = map[string]any{"selections": selections}
			return err
		case "work":
			var err error
			result, err = environmentWorkTx(ctx, q, req)
			return err
		case "operation-result", "report", "heartbeat":
			op, err := environmentOperationTx(ctx, q, req.OperationID)
			if err != nil {
				return err
			}
			req.ProjectID = op.ProjectID
			if action == "heartbeat" {
				if op.Status != "queued" && op.Status != "running" {
					return Domainf("This environment execution is complete.")
				}
				_, err = q.ExecContext(ctx, `UPDATE environment_operations SET status='running',heartbeat_at=datetime('now') WHERE id=?`, op.ID)
				result = map[string]any{"ok": err == nil}
				return err
			}
			if action == "report" {
				err = environmentReportTx(ctx, q, op, req)
			} else {
				err = environmentOperationResultTx(ctx, q, op, req)
			}
			if err != nil {
				return err
			}
			result, err = environmentViewTx(ctx, q, req.ProjectID)
			return err
		}
		if !ValidProjectID(req.ProjectID) {
			return Usagef("A valid Project identifier is required.")
		}
		if action == "get" {
			var err error
			result, err = environmentViewTx(ctx, q, req.ProjectID)
			return err
		}
		if action == "resolve" {
			var err error
			result, err = environmentResolveTx(ctx, q, req)
			return err
		}
		state, err := environmentStateTx(ctx, q, req.ProjectID)
		if err != nil {
			return err
		}
		if state.ProjectStatus == "archived" {
			return Domainf("An archived Project permits inspection only.")
		}
		switch action {
		case "cache-clear":
			if state.SelectedRevisionID == "" || state.ActiveOperationID != "" {
				return Domainf("Cache removal requires an accepted environment without an active environment operation.")
			}
			var active bool
			if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE project=? AND ended_at IS NULL) OR EXISTS(SELECT 1 FROM repository_executions WHERE project=? AND ended_at IS NULL)`, req.ProjectID, req.ProjectID).Scan(&active); err != nil {
				return err
			}
			if active {
				return Domainf("Wait for the Project's active agents and repository operations before cache removal.")
			}
			r, e := environmentRevisionTx(ctx, q, state.SelectedRevisionID)
			if e != nil {
				return e
			}
			_, err = queueEnvironmentOperationTx(ctx, q, state, r, "cache-clear", state.SelectedImageDigest, "", PrivateRuntimeBinding{}, 1)
		case "source-accepting", "source-aborted":
			err = environmentSourceAssociationTx(ctx, q, state, action, req)
		case "source-accepted":
			err = environmentSourceAcceptedTx(ctx, q, state, req)
		case "propose":
			if state.ActiveOperationID != "" {
				return Domainf("The Project already has an environment operation.")
			}
			if req.Configuration == nil || !commitDigest.MatchString(req.Configuration.SourceCommit) || strings.TrimSpace(req.Explanation) == "" {
				return Usagef("A pipeline proposal requires its source commit, configuration, and explanation.")
			}
			r, e := saveEnvironmentRevisionTx(ctx, q, state, *req.Configuration, req.Explanation, "pipeline:"+id.RunID)
			if e != nil {
				return e
			}
			subject, e := fenceRun(ctx, q, id.RunID)
			if e != nil {
				return e
			}
			_, err = q.ExecContext(ctx, `INSERT INTO environment_proposals(revision_id,run_id,task_id,source_commit) VALUES(?,?,?,?) ON CONFLICT(revision_id) DO NOTHING`, r.ID, id.RunID, *subject, r.Configuration.SourceCommit)
			if err != nil {
				return err
			}
			_, err = q.ExecContext(ctx, `UPDATE project_environments SET phase='approval',cause='The task requires approval of its proposed Project environment.' WHERE project_id=?`, state.ProjectID)
			if err != nil {
				return err
			}
			_, err = q.ExecContext(ctx, `INSERT INTO activity(actor,kind,subject,detail) VALUES(?,'environment.proposed',?,?)`, id.Role, *subject, r.Explanation)
		case "storage-report":
			if !imageDigest.MatchString(req.ImageDigest) {
				return Usagef("The storage report requires an image digest.")
			}
			body, e := json.Marshal(req.Metadata)
			if e != nil {
				return e
			}
			if len(body) > 1024*1024 {
				return Usagef("The storage report exceeds its size limit.")
			}
			_, err = q.ExecContext(ctx, `INSERT INTO environment_storage(project_id,image_digest,metadata) VALUES(?,?,?) ON CONFLICT(project_id) DO UPDATE SET image_digest=excluded.image_digest,metadata=excluded.metadata,updated_at=datetime('now')`, req.ProjectID, req.ImageDigest, string(body))
		case "runtime-check":
			if req.ImageDigest != state.SelectedImageDigest || req.RuntimeIdentity == "" {
				return Domainf("The runtime check must identify the selected Project image and Culler runtime.")
			}
			if req.Status != "succeeded" && req.Status != "failed" {
				return Usagef("A runtime check requires a succeeded or failed status.")
			}
			if req.Status == "failed" && (req.Error == "" || len(req.Error) > 4096) {
				return Usagef("A failed runtime check requires a concise cause.")
			}
			missing, _ := req.Metadata["image_missing"].(bool)
			metadata, _ := json.Marshal(map[string]bool{"image_missing": req.Status == "failed" && missing})
			_, err = q.ExecContext(ctx, `INSERT INTO environment_runtime_checks(project_id,image_digest,runtime_identity,status,error,metadata) VALUES(?,?,?,?,?,?) ON CONFLICT(project_id) DO UPDATE SET image_digest=excluded.image_digest,runtime_identity=excluded.runtime_identity,status=excluded.status,error=excluded.error,metadata=excluded.metadata,checked_at=datetime('now')`, req.ProjectID, req.ImageDigest, req.RuntimeIdentity, req.Status, req.Error, string(metadata))
		case "save":
			if state.SelectedRevisionID == "" {
				decision, e := environmentDecisionTx(ctx, q, state.ProjectID)
				if e != nil {
					return e
				}
				if decision != nil {
					return Domainf("Initial setup must finish with the recorded image decision. Further changes use the Environment page after import.")
				}
			}
			if req.ExpectedRevisionID != state.DraftRevisionID {
				return Domainf("The environment draft changed. Reload it before another edit.")
			}
			if state.ActiveOperationID != "" {
				return Domainf("Wait for the current environment operation before another edit.")
			}
			if req.Configuration == nil {
				return Usagef("The environment configuration is required.")
			}
			_, err = saveEnvironmentRevisionTx(ctx, q, state, *req.Configuration, req.Explanation, "operator")
		case "approve":
			err = approveEnvironmentTx(ctx, q, state, req)
		case "assist":
			if state.SelectedRevisionID == "" {
				decision, e := environmentDecisionTx(ctx, q, state.ProjectID)
				if e != nil {
					return e
				}
				if decision != nil {
					return Domainf("Initial setup must finish with the recorded image decision. Further changes use the Environment page after import.")
				}
			}
			if state.ActiveOperationID != "" {
				return Domainf("The Project already has an environment operation.")
			}
			if strings.TrimSpace(req.Instruction) == "" || len(req.Instruction) > 8192 {
				return Usagef("Describe the environment change in at most 8192 characters.")
			}
			if state.SetupID == "" {
				if err = initializeEnvironmentTx(ctx, q, state.ProjectID, ""); err != nil {
					return err
				}
				if _, err = q.ExecContext(ctx, `UPDATE projects SET setup_status='configured',status='paused' WHERE id=?`, state.ProjectID); err != nil {
					return err
				}
			}
			_, err = q.ExecContext(ctx, `UPDATE project_environments SET phase='queued',instruction=?,cause='' WHERE project_id=?`, req.Instruction, req.ProjectID)
		case "retry":
			err = retryEnvironmentTx(ctx, q, state, req)
		case "deny":
			err = denyEnvironmentTx(ctx, q, state, req)
		default:
			return Usagef("Unknown Project environment action.")
		}
		if err != nil {
			return err
		}
		result, err = environmentViewTx(ctx, q, req.ProjectID)
		return err
	})
	return result, err
}

func environmentStateTx(ctx context.Context, q Q, project string) (EnvironmentState, error) {
	var s EnvironmentState
	err := q.QueryRowContext(ctx, `SELECT p.id,p.title,p.status,COALESCE(e.setup_id,''),COALESCE(e.vision_revision,''),COALESCE(e.selected_base_digest,''),
 COALESCE(e.selected_revision_id,''),COALESCE(e.selected_image_digest,''),COALESCE(e.draft_revision_id,''),COALESCE(e.readiness,'setup_required'),
 COALESCE(e.phase,'idle'),COALESCE(e.cause,''),COALESCE(e.active_operation_id,''),COALESCE(e.authorization,'initial'),COALESCE(e.instruction,''),
 COALESCE((SELECT error FROM environment_runtime_checks c WHERE c.project_id=p.id AND c.image_digest=e.selected_image_digest AND c.status='failed'),''),
 COALESCE((SELECT json_extract(metadata,'$.image_missing') FROM environment_runtime_checks c WHERE c.project_id=p.id AND c.image_digest=e.selected_image_digest AND c.status='failed'),0),
 EXISTS(SELECT 1 FROM environment_associations a WHERE a.project_id=p.id AND a.status='pending'),p.setup_status
 FROM projects p LEFT JOIN project_environments e ON e.project_id=p.id WHERE p.id=?`, project).Scan(&s.ProjectID, &s.ProjectTitle, &s.ProjectStatus, &s.SetupID, &s.VisionRevision, &s.SelectedBaseDigest, &s.SelectedRevisionID, &s.SelectedImageDigest, &s.DraftRevisionID, &s.Readiness, &s.Phase, &s.Cause, &s.ActiveOperationID, &s.Authorization, &s.Instruction, &s.RuntimeCondition, &s.ImageMissing, &s.AssociationPending, &s.SetupStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return s, Domainf("The Project does not exist.")
	}
	return s, err
}

func environmentCondition(s EnvironmentState) string {
	if s.AssociationPending {
		return "environment-source-association"
	}
	if s.RuntimeCondition != "" {
		return "environment-runtime-incompatible"
	}
	if s.ActiveOperationID != "" || s.Phase == "queued" || s.Phase == "approval" {
		return "environment-change"
	}
	if s.SelectedRevisionID == "" || s.Readiness != "ready" {
		return "environment-setup-required"
	}
	return ""
}

func initializeEnvironmentTx(ctx context.Context, q Q, project, vision string) error {
	s, err := environmentStateTx(ctx, q, project)
	if err != nil {
		return err
	}
	if s.SetupID != "" {
		if s.VisionRevision == "" && s.SelectedRevisionID == "" && s.ActiveOperationID == "" {
			_, err = q.ExecContext(ctx, `UPDATE project_environments SET vision_revision=?,phase='queued' WHERE project_id=?`, vision, project)
			return err
		}
		if s.VisionRevision != vision {
			return Domainf("The Project already has setup for another Vision revision.")
		}
		return nil
	}
	id, err := newRunID()
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO project_environments(project_id,setup_id,vision_revision) VALUES(?,?,?)`, project, id, vision)
	return err
}

func environmentRevisionTx(ctx context.Context, q Q, id string) (EnvironmentRevision, error) {
	var r EnvironmentRevision
	var body string
	err := q.QueryRowContext(ctx, `SELECT id,project_id,editor,configuration,inputs_digest,explanation,created_at FROM environment_revisions WHERE id=?`, id).Scan(&r.ID, &r.ProjectID, &r.Editor, &body, &r.InputsDigest, &r.Explanation, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, Domainf("The environment revision does not exist.")
	}
	if err != nil {
		return r, err
	}
	err = json.Unmarshal([]byte(body), &r.Configuration)
	return r, err
}

func saveEnvironmentRevisionTx(ctx context.Context, q Q, s EnvironmentState, c EnvironmentConfiguration, explanation, editor string) (EnvironmentRevision, error) {
	if err := ValidateEnvironmentConfiguration(&c); err != nil {
		return EnvironmentRevision{}, err
	}
	var knownBase bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM environment_base_sources WHERE base_digest=?)`, c.BaseDigest).Scan(&knownBase); err != nil {
		return EnvironmentRevision{}, err
	}
	if !knownBase {
		return EnvironmentRevision{}, Domainf("The base image is not a recorded Culler default image. Select a supported Culler base image.")
	}
	if len(explanation) > 8192 {
		return EnvironmentRevision{}, Usagef("The environment explanation exceeds 8192 characters.")
	}
	if s.SetupID == "" {
		id, err := newRunID()
		if err != nil {
			return EnvironmentRevision{}, err
		}
		if _, err = q.ExecContext(ctx, `INSERT INTO project_environments(project_id,setup_id,phase) VALUES(?,?,'idle')`, s.ProjectID, id); err != nil {
			return EnvironmentRevision{}, err
		}
	}
	body, _ := json.Marshal(c)
	if s.DraftRevisionID != "" {
		old, err := environmentRevisionTx(ctx, q, s.DraftRevisionID)
		if err != nil {
			return old, err
		}
		oldBody, _ := json.Marshal(old.Configuration)
		if string(oldBody) == string(body) && old.Explanation == explanation {
			return old, nil
		}
	}
	id, err := newRunID()
	if err != nil {
		return EnvironmentRevision{}, err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO environment_revisions(id,project_id,editor,configuration,inputs_digest,explanation) VALUES(?,?,?,?,?,?)`, id, s.ProjectID, editor, string(body), environmentBuildDigest(c), explanation)
	if err != nil {
		return EnvironmentRevision{}, err
	}
	_, err = q.ExecContext(ctx, `UPDATE project_environments SET draft_revision_id=?,cause='',updated_at=datetime('now') WHERE project_id=?`, id, s.ProjectID)
	if err != nil {
		return EnvironmentRevision{}, err
	}
	return environmentRevisionTx(ctx, q, id)
}

func environmentViewTx(ctx context.Context, q Q, project string) (any, error) {
	s, err := environmentStateTx(ctx, q, project)
	if err != nil {
		return nil, err
	}
	var draft, selected *EnvironmentRevision
	if s.DraftRevisionID != "" {
		r, e := environmentRevisionTx(ctx, q, s.DraftRevisionID)
		if e != nil {
			return nil, e
		}
		draft = &r
	}
	if s.SelectedRevisionID != "" {
		r, e := environmentRevisionTx(ctx, q, s.SelectedRevisionID)
		if e != nil {
			return nil, e
		}
		selected = &r
	}
	rows, err := q.QueryContext(ctx, `SELECT id FROM environment_revisions WHERE project_id=? ORDER BY rowid DESC`, project)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	revisions := []EnvironmentRevision{}
	for _, id := range ids {
		r, e := environmentRevisionTx(ctx, q, id)
		if e != nil {
			return nil, e
		}
		revisions = append(revisions, r)
	}
	ops, err := environmentOperationsTx(ctx, q, `WHERE project_id=? ORDER BY rowid DESC`, project)
	if err != nil {
		return nil, err
	}
	rows, err = q.QueryContext(ctx, `SELECT * FROM environment_results WHERE project_id=? ORDER BY rowid DESC`, project)
	if err != nil {
		return nil, err
	}
	results, err := rowsToMaps(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, r := range results {
		for _, name := range []string{"tool_versions", "package_records"} {
			var value any
			if body, ok := r[name].(string); ok && json.Unmarshal([]byte(body), &value) == nil {
				r[name] = value
			}
		}
	}
	storage := map[string]any{}
	var storageJSON string
	err = q.QueryRowContext(ctx, `SELECT metadata FROM environment_storage WHERE project_id=? AND image_digest IN (?,?)`, project, s.SelectedImageDigest, s.SelectedBaseDigest).Scan(&storageJSON)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(storageJSON), &storage); err != nil {
			return nil, err
		}
	}
	evidenceRows, err := q.QueryContext(ctx, `SELECT e.run_id,r.source_commit,r.revision_id,r.image_digest,r.base_digest,r.runtime_identity,e.tool_versions,e.package_records,e.created_at FROM environment_run_evidence e JOIN run_environments r ON r.run_id=e.run_id WHERE r.project_id=? ORDER BY e.rowid DESC LIMIT 100`, project)
	if err != nil {
		return nil, err
	}
	evidence, err := rowsToMaps(evidenceRows)
	evidenceRows.Close()
	if err != nil {
		return nil, err
	}
	for _, entry := range evidence {
		for _, key := range []string{"tool_versions", "package_records"} {
			var value any
			if err = json.Unmarshal([]byte(entry[key].(string)), &value); err != nil {
				return nil, err
			}
			entry[key] = value
		}
	}
	approvalRows, err := q.QueryContext(ctx, `SELECT revision_id,inputs_digest,created_at FROM environment_approvals WHERE project_id=? ORDER BY rowid`, project)
	if err != nil {
		return nil, err
	}
	approvals, err := rowsToMaps(approvalRows)
	approvalRows.Close()
	if err != nil {
		return nil, err
	}
	decisionRows, err := q.QueryContext(ctx, `SELECT * FROM environment_decisions WHERE project_id=? ORDER BY rowid`, project)
	if err != nil {
		return nil, err
	}
	decisions, err := rowsToMaps(decisionRows)
	decisionRows.Close()
	if err != nil {
		return nil, err
	}
	visionRows, err := q.QueryContext(ctx, `SELECT revision,content,created_at FROM vision_submissions WHERE project_id=? AND revision=? AND status='saved' ORDER BY rowid LIMIT 1`, project, s.VisionRevision)
	if err != nil {
		return nil, err
	}
	visions, err := rowsToMaps(visionRows)
	visionRows.Close()
	if err != nil {
		return nil, err
	}
	var vision map[string]any
	if len(visions) > 0 {
		vision = visions[0]
	}
	baseSource, err := environmentBaseSourceTx(ctx, q, s.SelectedBaseDigest)
	if err != nil {
		return nil, err
	}
	return map[string]any{"environment": s, "draft": draft, "selected": selected, "revisions": revisions, "operations": ops, "results": results, "approvals": approvals, "decisions": decisions, "vision": vision, "run_evidence": evidence, "base_source": baseSource, "inventory": storage["inventory"], "disk_use": map[string]any{"home_bytes": storage["home_bytes"], "cache_bytes": storage["cache_bytes"], "image_bytes": storage["image_bytes"]}}, nil
}

func environmentBaseSourceTx(ctx context.Context, q Q, digest string) (map[string]any, error) {
	var reference, release string
	err := q.QueryRowContext(ctx, `SELECT registry_reference,release_identity FROM environment_base_sources WHERE base_digest=? ORDER BY registry_reference<>'' DESC,rowid DESC LIMIT 1`, digest).Scan(&reference, &release)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"base_digest": digest, "registry_reference": reference, "release_identity": release}, nil
}

func environmentRunEvidenceTx(ctx context.Context, q Q, req EnvironmentRequest) error {
	if req.ToolVersions == nil {
		req.ToolVersions = map[string]string{}
	}
	if req.PackageRecords == nil {
		req.PackageRecords = []string{}
	}
	tools, _ := json.Marshal(req.ToolVersions)
	packages, _ := json.Marshal(req.PackageRecords)
	if len(tools) > 65536 || len(packages) > 256*1024 || len(req.ToolVersions) > 128 || len(req.PackageRecords) > 1024 {
		return Usagef("The run evidence exceeds its size limit.")
	}
	var assigned bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM run_environments WHERE run_id=?)`, req.RunID).Scan(&assigned); err != nil {
		return err
	}
	if !assigned {
		return Domainf("The run requires its recorded environment assignment before evidence.")
	}
	var priorTools, priorPackages string
	err := q.QueryRowContext(ctx, `SELECT tool_versions,package_records FROM environment_run_evidence WHERE run_id=?`, req.RunID).Scan(&priorTools, &priorPackages)
	if err == nil {
		if priorTools != string(tools) || priorPackages != string(packages) {
			return Domainf("The run already contains different environment evidence.")
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO environment_run_evidence(run_id,tool_versions,package_records) VALUES(?,?,?)`, req.RunID, string(tools), string(packages))
	return err
}

func environmentOperationsTx(ctx context.Context, q Q, where string, args ...any) ([]EnvironmentOperation, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,project_id,revision_id,kind,status,inputs_digest,image_digest,runtime_identity,runtime_binding,attempt,error,created_at,COALESCE(ended_at,''),report_digest,metadata,COALESCE(native_session_ref,''),COALESCE(trace_filename,'') FROM environment_operations `+where, args...)
	if err != nil {
		return nil, err
	}
	ops := []EnvironmentOperation{}
	for rows.Next() {
		var o EnvironmentOperation
		var runtime, metadata string
		err = rows.Scan(&o.ID, &o.ProjectID, &o.RevisionID, &o.Kind, &o.Status, &o.InputsDigest, &o.ImageDigest, &o.RuntimeIdentity, &runtime, &o.Attempt, &o.Error, &o.CreatedAt, &o.EndedAt, &o.ReportDigest, &metadata, &o.NativeSessionRef, &o.TraceFilename)
		if err != nil {
			break
		}
		if err = json.Unmarshal([]byte(runtime), &o.RuntimeBinding); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(metadata), &o.Metadata); err != nil {
			break
		}
		binding := o.RuntimeBinding.Harness + "/" + o.RuntimeBinding.ID
		if trace.ValidFilename(binding, o.TraceFilename) {
			o.Trace = agentsViewSession(binding, o.NativeSessionRef)
			o.TraceFilename = trace.Filename(binding, o.TraceFilename)
		}
		ops = append(ops, o)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	return ops, nil
}

func environmentOperationTx(ctx context.Context, q Q, id string) (EnvironmentOperation, error) {
	ops, err := environmentOperationsTx(ctx, q, `WHERE id=?`, id)
	if err != nil {
		return EnvironmentOperation{}, err
	}
	if len(ops) != 1 {
		return EnvironmentOperation{}, Domainf("The environment execution does not exist.")
	}
	return ops[0], nil
}

func queueEnvironmentOperationTx(ctx context.Context, q Q, s EnvironmentState, r EnvironmentRevision, kind, image, runtime string, binding PrivateRuntimeBinding, attempt int) (string, error) {
	metadata := map[string]any{}
	if kind == "cache-clear" {
		metadata["cache_previous"] = map[string]any{"draft_revision_id": s.DraftRevisionID, "phase": s.Phase, "cause": s.Cause, "instruction": s.Instruction}
	}
	if s.SelectedRevisionID == "" && (kind == "setup" || kind == "validate") {
		metadata["initial_setup"] = true
		metadata["setup_id"] = s.SetupID
		decision, err := environmentDecisionTx(ctx, q, s.ProjectID)
		if err != nil {
			return "", err
		}
		if decision != nil && decision.Decision == "denied" {
			proposal, err := environmentRevisionTx(ctx, q, decision.RevisionID)
			if err != nil {
				return "", err
			}
			files := append([]EnvironmentFile{}, proposal.Configuration.BuildFiles...)
			if proposal.Configuration.Dockerfile != nil {
				files = append(files, *proposal.Configuration.Dockerfile)
			}
			var original string
			if err := q.QueryRowContext(ctx, `SELECT revision_id FROM environment_results WHERE operation_id=?`, decision.OperationID).Scan(&original); err != nil {
				return "", err
			}
			if original != decision.RevisionID {
				prior, err := environmentRevisionTx(ctx, q, original)
				if err != nil {
					return "", err
				}
				originalFiles := append([]EnvironmentFile{}, prior.Configuration.BuildFiles...)
				if prior.Configuration.Dockerfile != nil {
					originalFiles = append(originalFiles, *prior.Configuration.Dockerfile)
				}
				paths := map[string]bool{}
				for _, file := range originalFiles {
					paths[file.Path] = true
				}
				for _, file := range files {
					if !paths[file.Path] {
						originalFiles = append(originalFiles, file)
					}
				}
				files = originalFiles
			}
			metadata["denied_build_files"] = files
			metadata["proposal_operation_id"] = decision.OperationID
		}
	}
	baseSource, err := environmentBaseSourceTx(ctx, q, r.Configuration.BaseDigest)
	if err != nil {
		return "", err
	}
	if baseSource != nil {
		metadata["base_source"] = baseSource
	}
	if s.SelectedRevisionID != "" {
		selected, err := environmentRevisionTx(ctx, q, s.SelectedRevisionID)
		if err != nil {
			return "", err
		}
		paths := map[string]bool{}
		for _, file := range r.Configuration.BuildFiles {
			paths[file.Path] = true
		}
		if r.Configuration.Dockerfile != nil {
			paths[r.Configuration.Dockerfile.Path] = true
		}
		prior := append([]EnvironmentFile{}, selected.Configuration.BuildFiles...)
		if selected.Configuration.Dockerfile != nil {
			prior = append(prior, *selected.Configuration.Dockerfile)
		}
		removed := []EnvironmentFile{}
		for _, file := range prior {
			if !paths[file.Path] {
				removed = append(removed, file)
			}
		}
		if len(removed) > 0 {
			metadata["removed_files"] = removed
		}
	}
	if kind == "setup" && s.SelectedRevisionID != "" {
		metadata["draft_only"] = true
	}
	var previous string
	if err := q.QueryRowContext(ctx, `SELECT id FROM environment_operations WHERE project_id=? ORDER BY rowid DESC LIMIT 1`, s.ProjectID).Scan(&previous); err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if previous != "" {
		metadata["previous_operation_id"] = previous
	}
	var origin, source string
	if err := q.QueryRowContext(ctx, `SELECT run_id,source_commit FROM environment_proposals WHERE revision_id=?`, r.ID).Scan(&origin, &source); err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if origin != "" {
		metadata["source_run_id"] = origin
		metadata["candidate_source"] = source
		metadata["candidate"] = true
	}
	id, err := newRunID()
	if err != nil {
		return "", err
	}
	meta, _ := json.Marshal(metadata)
	body, _ := json.Marshal(binding)
	_, err = q.ExecContext(ctx, `INSERT INTO environment_operations(id,project_id,revision_id,kind,inputs_digest,image_digest,runtime_identity,runtime_binding,attempt) VALUES(?,?,?,?,?,?,?,?,?)`, id, s.ProjectID, r.ID, kind, r.InputsDigest, image, runtime, string(body), attempt)
	if err != nil {
		return "", err
	}
	if _, err = q.ExecContext(ctx, `UPDATE environment_operations SET metadata=? WHERE id=?`, string(meta), id); err != nil {
		return "", err
	}
	phase := kind
	if kind == "accept" || kind == "cache-clear" || kind == "runtime-check" {
		phase = "validate"
	}
	if kind == "setup" {
		phase = "agent"
	}
	_, err = q.ExecContext(ctx, `UPDATE project_environments SET active_operation_id=?,phase=?,cause='',updated_at=datetime('now') WHERE project_id=?`, id, phase, s.ProjectID)
	return id, err
}

func approveEnvironmentTx(ctx context.Context, q Q, s EnvironmentState, req EnvironmentRequest) error {
	r, err := environmentRevisionTx(ctx, q, req.RevisionID)
	if err != nil {
		return err
	}
	if r.ProjectID != s.ProjectID {
		return Domainf("Approval requires this Project's current draft.")
	}
	var prior bool
	if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM environment_approvals WHERE revision_id=?)`, r.ID).Scan(&prior); err != nil {
		return err
	}
	if prior {
		return nil
	}
	if r.ID != s.DraftRevisionID {
		return Domainf("Approval requires this Project's current draft.")
	}
	if s.ActiveOperationID != "" {
		return Domainf("Wait for the current environment operation before approval.")
	}
	if s.SelectedRevisionID != "" && r.Configuration.SourceCommit == "" {
		return Usagef("Environment validation requires the intended source commit.")
	}
	if s.SelectedRevisionID == "" {
		if err = recordEnvironmentDecisionTx(ctx, q, s, r.ID, "approved"); err != nil {
			return err
		}
	}
	if _, err = q.ExecContext(ctx, `INSERT INTO environment_approvals(revision_id,project_id,inputs_digest) VALUES(?,?,?)`, r.ID, s.ProjectID, r.InputsDigest); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `UPDATE project_environments SET phase='queued',instruction='',cause='' WHERE project_id=?`, s.ProjectID)
	return err
}

func retryEnvironmentTx(ctx context.Context, q Q, s EnvironmentState, req EnvironmentRequest) error {
	if s.ActiveOperationID != "" {
		return nil
	}
	if s.RuntimeCondition != "" && s.SelectedRevisionID != "" {
		r, err := environmentRevisionTx(ctx, q, s.SelectedRevisionID)
		if err != nil {
			return err
		}
		var runtime string
		if err = q.QueryRowContext(ctx, `SELECT runtime_identity FROM environment_runtime_checks WHERE project_id=?`, s.ProjectID).Scan(&runtime); err != nil {
			return err
		}
		if s.ImageMissing && r.Configuration.Dockerfile != nil {
			return repairEnvironmentImageTx(ctx, q, s, r, runtime, req)
		}
		_, err = queueEnvironmentOperationTx(ctx, q, s, r, "runtime-check", s.SelectedImageDigest, runtime, PrivateRuntimeBinding{}, 1)
		return err
	}
	if s.Phase != "failed" {
		return Domainf("The Project has no failed setup to retry.")
	}
	prior, err := environmentOperationsTx(ctx, q, `WHERE project_id=? AND status='failed' ORDER BY rowid DESC LIMIT 1`, s.ProjectID)
	if err != nil {
		return err
	}
	if len(prior) != 1 {
		return Domainf("The failed setup operation is unavailable.")
	}
	op := prior[0]
	r, err := environmentRevisionTx(ctx, q, op.RevisionID)
	if err != nil {
		return err
	}
	id, err := queueEnvironmentOperationTx(ctx, q, s, r, op.Kind, op.ImageDigest, op.RuntimeIdentity, op.RuntimeBinding, 1)
	if err != nil {
		return err
	}
	op.Metadata["previous_operation_id"] = op.ID
	markEnvironmentReplacement(op.Metadata, op, false)
	metadata, _ := json.Marshal(op.Metadata)
	_, err = q.ExecContext(ctx, `UPDATE environment_operations SET metadata=? WHERE id=?`, string(metadata), id)
	return err
}

func repairEnvironmentImageTx(ctx context.Context, q Q, s EnvironmentState, selected EnvironmentRevision, runtime string, req EnvironmentRequest) error {
	if !commitDigest.MatchString(req.SourceCommit) || req.RuntimeBinding.ID == "" {
		return Domainf("Image repair requires the current accepted source and an available setup agent.")
	}
	var active bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE project=? AND ended_at IS NULL) OR EXISTS(SELECT 1 FROM repository_executions WHERE project=? AND ended_at IS NULL)`, s.ProjectID, s.ProjectID).Scan(&active); err != nil {
		return err
	}
	if active {
		return Domainf("Wait for the Project's active work before image repair.")
	}
	preserved := map[string]any{}
	if s.DraftRevisionID != selected.ID && s.DraftRevisionID != "" {
		preserved = map[string]any{"draft_revision_id": s.DraftRevisionID, "phase": s.Phase, "cause": s.Cause, "instruction": s.Instruction}
		prior, err := environmentOperationsTx(ctx, q, `WHERE project_id=? AND revision_id=? ORDER BY rowid DESC LIMIT 1`, s.ProjectID, s.DraftRevisionID)
		if err != nil {
			return err
		}
		if len(prior) > 0 {
			if old, ok := prior[0].Metadata["repair_previous"].(map[string]any); ok {
				preserved = old
			}
		}
	}
	configuration := selected.Configuration
	configuration.SourceCommit = req.SourceCommit
	revision, err := saveEnvironmentRevisionTx(ctx, q, s, configuration, "Restore the approved Project image from its retained build files.", "culler")
	if err != nil {
		return err
	}
	if revision.InputsDigest != selected.InputsDigest {
		return Domainf("Image repair must preserve the approved build inputs.")
	}
	if _, err = q.ExecContext(ctx, `INSERT INTO environment_approvals(revision_id,project_id,inputs_digest) VALUES(?,?,?) ON CONFLICT(revision_id) DO NOTHING`, revision.ID, s.ProjectID, revision.InputsDigest); err != nil {
		return err
	}
	id, err := queueEnvironmentOperationTx(ctx, q, s, revision, "build", configuration.BaseDigest, runtime, req.RuntimeBinding, 1)
	if err != nil {
		return err
	}
	operation, err := environmentOperationTx(ctx, q, id)
	if err != nil {
		return err
	}
	operation.Metadata["repair_previous"] = preserved
	metadata, _ := json.Marshal(operation.Metadata)
	_, err = q.ExecContext(ctx, `UPDATE environment_operations SET metadata=? WHERE id=?`, string(metadata), id)
	return err
}

func preserveEnvironmentRepairTx(ctx context.Context, q Q, id string, prior EnvironmentOperation) error {
	previous, ok := prior.Metadata["repair_previous"]
	if !ok {
		return nil
	}
	operation, err := environmentOperationTx(ctx, q, id)
	if err != nil {
		return err
	}
	operation.Metadata["repair_previous"] = previous
	metadata, _ := json.Marshal(operation.Metadata)
	_, err = q.ExecContext(ctx, `UPDATE environment_operations SET metadata=? WHERE id=?`, string(metadata), id)
	return err
}

func isOperatorEnvironmentAction(action string) bool {
	switch action {
	case "get", "save", "approve", "deny", "assist", "retry", "cache-clear":
		return true
	default:
		return false
	}
}
