package verbs

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed directives/setup.md
var environmentSetupDirective string

// ResolveEnvironmentRuntime selects the operator's Worker route for setup.
func ResolveEnvironmentRuntime() (PrivateRuntimeBinding, error) {
	home, err := resolveMCHome()
	if err != nil {
		return PrivateRuntimeBinding{}, err
	}
	route, authority, err := resolveRoleRouteWithAuthority(home, "worker")
	if err != nil {
		return PrivateRuntimeBinding{}, err
	}
	binding, ok := resolveFrozenCollieBinding(authority.capability(), route.HistoricalBinding())
	if !ok {
		return binding, Domainf("The setup agent profile is unavailable.")
	}
	return binding, nil
}

func environmentWorkTx(ctx context.Context, q Q, req EnvironmentRequest) (any, error) {
	if !imageDigest.MatchString(req.DefaultImageDigest) || req.RuntimeIdentity == "" {
		return nil, Usagef("Environment execution requires the default image digest and Culler runtime identity.")
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO environment_base_sources(base_digest,registry_reference,release_identity) VALUES(?,'',?) ON CONFLICT(base_digest,registry_reference) DO NOTHING`, req.DefaultImageDigest, req.RuntimeIdentity); err != nil {
		return nil, err
	}
	prior, err := environmentOperationsTx(ctx, q, `WHERE status IN ('queued','running') AND runtime_identity<>? AND project_id IN (SELECT id FROM projects WHERE status<>'archived') ORDER BY rowid`, req.RuntimeIdentity)
	if err != nil {
		return nil, err
	}
	for _, op := range prior {
		if err = finishEnvironmentOperationTx(ctx, q, op, "failed", "The Culler runtime changed before this operation completed.", environmentHash(map[string]string{"replacement_runtime": req.RuntimeIdentity})); err != nil {
			return nil, err
		}
		s, loadErr := environmentStateTx(ctx, q, op.ProjectID)
		if loadErr != nil {
			return nil, loadErr
		}
		r, loadErr := environmentRevisionTx(ctx, q, op.RevisionID)
		if loadErr != nil {
			return nil, loadErr
		}
		id, queueErr := queueEnvironmentOperationTx(ctx, q, s, r, op.Kind, op.ImageDigest, req.RuntimeIdentity, req.RuntimeBinding, 1)
		if queueErr != nil {
			return nil, queueErr
		}
		op.Metadata["previous_operation_id"] = op.ID
		markEnvironmentReplacement(op.Metadata, op, false)
		metadata, _ := json.Marshal(op.Metadata)
		if _, err = q.ExecContext(ctx, `UPDATE environment_operations SET metadata=? WHERE id=?`, string(metadata), id); err != nil {
			return nil, err
		}
	}
	rows, err := q.QueryContext(ctx, `SELECT e.project_id FROM project_environments e JOIN projects p ON p.id=e.project_id WHERE p.status<>'archived' AND e.phase='queued' AND e.active_operation_id IS NULL ORDER BY e.project_id`)
	if err != nil {
		return nil, err
	}
	projects := []string{}
	for rows.Next() {
		var p string
		if err = rows.Scan(&p); err != nil {
			break
		}
		projects = append(projects, p)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, project := range projects {
		s, err := environmentStateTx(ctx, q, project)
		if err != nil {
			return nil, err
		}
		if s.SelectedRevisionID != "" {
			var busy bool
			if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE project=? AND ended_at IS NULL) OR EXISTS(SELECT 1 FROM repository_executions WHERE project=? AND ended_at IS NULL)`, project, project).Scan(&busy); err != nil {
				return nil, err
			}
			if busy {
				continue
			}
		}
		if s.DraftRevisionID == "" {
			c := EnvironmentConfiguration{BaseDigest: req.DefaultImageDigest}
			r, err := saveEnvironmentRevisionTx(ctx, q, s, c, "The default Project image supplies the initial tools.", "culler")
			if err != nil {
				return nil, err
			}
			s.DraftRevisionID = r.ID
			if _, err = q.ExecContext(ctx, `UPDATE project_environments SET selected_base_digest=? WHERE project_id=?`, c.BaseDigest, project); err != nil {
				return nil, err
			}
		}
		r, err := environmentRevisionTx(ctx, q, s.DraftRevisionID)
		if err != nil {
			return nil, err
		}
		kind, image := "setup", r.Configuration.BaseDigest
		var approved bool
		if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM environment_approvals WHERE revision_id=?)`, r.ID).Scan(&approved); err != nil {
			return nil, err
		}
		if approved && s.Instruction == "" {
			kind = "validate"
			if r.Configuration.Dockerfile != nil {
				kind = "build"
			}
			if s.SelectedRevisionID != "" {
				selected, err := environmentRevisionTx(ctx, q, s.SelectedRevisionID)
				if err != nil {
					return nil, err
				}
				if selected.InputsDigest == r.InputsDigest {
					kind = "validate"
					image = s.SelectedImageDigest
				}
			}
		} else if s.SelectedImageDigest != "" {
			image = s.SelectedImageDigest
		}
		decision, err := environmentDecisionTx(ctx, q, s.ProjectID)
		if err != nil {
			return nil, err
		}
		denied := s.SelectedRevisionID == "" && decision != nil && decision.Decision == "denied"
		if denied {
			kind, image = "validate", decision.CurrentImage
		}
		if r.Configuration.Dockerfile != nil && !approved && !denied && s.Instruction == "" {
			if _, err = q.ExecContext(ctx, `UPDATE project_environments SET phase='approval' WHERE project_id=?`, project); err != nil {
				return nil, err
			}
			continue
		}
		if kind != "build" && req.RuntimeBinding.ID == "" {
			continue
		}
		if kind != "build" {
			now, err := spineClock(ctx, q)
			if err != nil {
				return nil, err
			}
			if _, _, err = spawnConnection(ctx, q, now, req.RuntimeBinding); err != nil {
				if _, saveErr := q.ExecContext(ctx, `UPDATE project_environments SET cause=? WHERE project_id=?`, err.Error(), project); saveErr != nil {
					return nil, saveErr
				}
				continue
			}
		}
		if _, err = queueEnvironmentOperationTx(ctx, q, s, r, kind, image, req.RuntimeIdentity, req.RuntimeBinding, 1); err != nil {
			return nil, err
		}
	}
	ops, err := environmentOperationsTx(ctx, q, `WHERE status IN ('queued','running') AND project_id IN (SELECT id FROM projects WHERE status<>'archived') AND (retry_at IS NULL OR retry_at<=datetime('now')) ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	for i := range ops {
		op := &ops[i]
		var archived bool
		if err = q.QueryRowContext(ctx, `SELECT p.status='archived',COALESCE(s.workspace_root,'') FROM projects p JOIN sandbox_profiles s ON s.id=p.sandbox_profile WHERE p.id=?`, op.ProjectID).Scan(&archived, &op.WorkspaceRoot); err != nil {
			return nil, err
		}
		if archived {
			continue
		}
		r, err := environmentRevisionTx(ctx, q, op.RevisionID)
		if err != nil {
			return nil, err
		}
		op.Configuration = r.Configuration
		if op.Kind == "setup" || op.Kind == "validate" {
			s, err := environmentStateTx(ctx, q, op.ProjectID)
			if err != nil {
				return nil, err
			}
			var vision string
			_ = q.QueryRowContext(ctx, `SELECT content FROM vision_drafts WHERE project_id=? AND revision=?`, op.ProjectID, s.VisionRevision).Scan(&vision)
			configuration, _ := json.MarshalIndent(op.Configuration, "", "  ")
			op.Brief = environmentSetupDirective + fmt.Sprintf("\n# Project environment setup\n\nUse the project-environment-setup skill. This run belongs to separate Project setup.\n\n## Assignment\n\nApply the setup checklist to prepare dependencies and assess each missing prerequisite. Base an image proposal on the image proposal condition and evidence of feasibility.\n\nProject: %s\nOperation: %s\nImage: %s\nRevision: %s\nbase_reference: %s\n\n## Execution limits\n\nSetup and pipeline agents share container permissions and resource limits; role authority and credentials remain separate.\nCuller keeps provider credentials on the host. This container uses a temporary Culler token through the host provider proxy.\nEach attempt has a 45-minute limit, and Culler permits two automatic attempts for each operation.\nThe log folder for command logs is %s.\n\n## Accepted Vision\n\n%s\n\n## Requested change\n\n%s\n\n## Retained configuration\n\nThe configuration defines dependency installation, services, variables, secrets, and image inputs. Its setup script prepares each fresh workspace and permits repeated execution against the persistent Project home.\n\n```json\n%s\n```\n\n## Report\n\nSubmit a concise summary and optional Markdown details through mc environment report. Report actual command results, image decisions, feasibility evidence, and unresolved prerequisites.\nRetained Project files are available at /workspace/source. Preserve them and keep secret values outside reports.\n", op.ProjectID, op.ID, op.ImageDigest, op.RevisionID, environmentBaseReference(op.Configuration.BaseDigest), environmentLogFolder, vision, s.Instruction, string(configuration))
			continuation, err := environmentContinuationBriefTx(ctx, q, s)
			if err != nil {
				return nil, err
			}
			op.Brief += continuation
			replacement, err := environmentReplacementBriefTx(ctx, q, *op)
			if err != nil {
				return nil, err
			}
			op.Brief += replacement
			if op.Kind == "validate" && s.SelectedRevisionID == "" && continuation == "" {
				op.Brief += "\n## Initial setup continuation\n\nThe operator approved the assigned configuration. The required outcome is `complete`.\nPrepare what the assigned image permits and report unresolved limits. Further image improvements use the Environment page after import.\n"
			}
			if op.Kind == "validate" && s.SelectedRevisionID != "" {
				op.Brief += "\n## Source preservation\n\nThe assigned source is committed development work. Preserve it while you check the approved environment.\nUse excluded disposable files for probes. Report application changes as ordinary Worker corrections.\n"
			}
			skills, err := skillSetByBasename("setup")
			if err != nil {
				return nil, err
			}
			op.Skills = SkillsEffectValue(skills)
		}
	}
	return map[string]any{"operations": ops}, nil
}

func finishEnvironmentOperationTx(ctx context.Context, q Q, op EnvironmentOperation, status, message, digest string) error {
	var current string
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(active_operation_id,'') FROM project_environments WHERE project_id=?`, op.ProjectID).Scan(&current); err != nil {
		return err
	}
	if current != op.ID || (op.Status != "queued" && op.Status != "running") {
		return Domainf("This environment execution no longer controls the Project.")
	}
	if _, err := q.ExecContext(ctx, `UPDATE environment_operations SET status=?,error=?,report_digest=?,ended_at=datetime('now') WHERE id=?`, status, message, digest, op.ID); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `UPDATE project_environments SET active_operation_id=NULL,updated_at=datetime('now') WHERE project_id=?`, op.ProjectID)
	return err
}

func environmentOperationResultTx(ctx context.Context, q Q, op EnvironmentOperation, req EnvironmentRequest) error {
	if req.InputsDigest != op.InputsDigest {
		return Domainf("The execution result does not match its retained build inputs.")
	}
	if (op.Kind == "setup" || op.Kind == "validate") && op.Status == "succeeded" {
		return nil
	}
	digest := environmentHash(req)
	if op.ReportDigest != "" {
		if op.ReportDigest == digest {
			return nil
		}
		return Domainf("This environment execution already has another result.")
	}
	if req.Status != "succeeded" && req.Status != "failed" {
		return Usagef("The execution result requires a succeeded or failed status.")
	}
	if len(req.Error) > 4096 {
		return Usagef("The execution error exceeds 4096 characters.")
	}
	if req.Status == "succeeded" {
		if op.Kind == "runtime-check" {
			if !req.RuntimeCompatible {
				return Domainf("The runtime check requires successful compatibility evidence.")
			}
			if err := finishEnvironmentOperationTx(ctx, q, op, "succeeded", "", digest); err != nil {
				return err
			}
			if _, err := q.ExecContext(ctx, `INSERT INTO environment_runtime_checks(project_id,image_digest,runtime_identity,status,error) VALUES(?,?,?,'succeeded','') ON CONFLICT(project_id) DO UPDATE SET image_digest=excluded.image_digest,runtime_identity=excluded.runtime_identity,status='succeeded',error='',metadata='{}',checked_at=datetime('now')`, op.ProjectID, op.ImageDigest, op.RuntimeIdentity); err != nil {
				return err
			}
			_, err := q.ExecContext(ctx, `UPDATE project_environments SET phase='complete',cause='' WHERE project_id=?`, op.ProjectID)
			return err
		}
		if op.Kind == "cache-clear" {
			if err := finishEnvironmentOperationTx(ctx, q, op, "succeeded", "", digest); err != nil {
				return err
			}
			return restoreEnvironmentCacheStateTx(ctx, q, op)
		}
		if op.Kind == "accept" {
			if !commitDigest.MatchString(req.SourceCommit) {
				return Usagef("Source acceptance requires the checked development commit.")
			}
			resultID, _ := op.Metadata["result_id"].(string)
			var checked, source string
			if err := q.QueryRowContext(ctx, `SELECT files_digest,source_commit FROM environment_results WHERE id=? AND project_id=? AND revision_id=?`, resultID, op.ProjectID, op.RevisionID).Scan(&checked, &source); err != nil {
				return err
			}
			candidate, _ := op.Metadata["candidate"].(bool)
			checkedSource, _ := req.Metadata["checked_source_commit"].(string)
			if checked != req.FilesDigest || (source != "" && source != req.SourceCommit && (candidate || checkedSource != source)) {
				return Domainf("Source acceptance differs from the checked Project files.")
			}
			if err := finishEnvironmentOperationTx(ctx, q, op, "succeeded", "", digest); err != nil {
				return err
			}
			s, err := environmentStateTx(ctx, q, op.ProjectID)
			if err != nil {
				return err
			}
			r, err := environmentRevisionTx(ctx, q, op.RevisionID)
			if err != nil {
				return err
			}
			op.Metadata["source_commit"] = req.SourceCommit
			metadata, _ := json.Marshal(op.Metadata)
			if _, err = q.ExecContext(ctx, `UPDATE environment_operations SET metadata=? WHERE id=?`, string(metadata), op.ID); err != nil {
				return err
			}
			if candidate {
				if _, err = q.ExecContext(ctx, `INSERT INTO environment_candidates(revision_id,project_id,source_commit,image_digest,result_id) VALUES(?,?,?,?,?) ON CONFLICT(revision_id) DO NOTHING`, r.ID, s.ProjectID, source, op.ImageDigest, resultID); err != nil {
					return err
				}
				_, err = q.ExecContext(ctx, `UPDATE project_environments SET phase='complete',cause='The candidate environment is ready for source review.' WHERE project_id=?`, s.ProjectID)
				return err
			}
			if err = acceptEnvironmentTx(ctx, q, s, r, op.ImageDigest); err != nil {
				return err
			}
			previous, repair := op.Metadata["repair_previous"].(map[string]any)
			if repair {
				if _, err = q.ExecContext(ctx, `UPDATE environment_runtime_checks SET status='succeeded',error='',metadata='{}',checked_at=datetime('now') WHERE project_id=? AND image_digest=? AND runtime_identity=?`, s.ProjectID, op.ImageDigest, op.RuntimeIdentity); err != nil {
					return err
				}
			}
			if repair && s.DraftRevisionID == r.ID {
				if draft, ok := previous["draft_revision_id"].(string); ok && draft != "" {
					_, err = q.ExecContext(ctx, `UPDATE project_environments SET draft_revision_id=?,phase=?,cause=?,instruction=? WHERE project_id=?`, draft, previous["phase"], previous["cause"], previous["instruction"], s.ProjectID)
				}
			} else if repair && s.DraftRevisionID != r.ID {
				_, err = q.ExecContext(ctx, `UPDATE project_environments SET draft_revision_id=?,phase=?,cause=?,instruction=? WHERE project_id=?`, s.DraftRevisionID, s.Phase, s.Cause, s.Instruction, s.ProjectID)
			}
			return err
		}
		if op.Kind != "build" {
			return Domainf("The setup agent must submit its report.")
		}
		if !imageDigest.MatchString(req.ImageDigest) || !req.RuntimeCompatible {
			return Domainf("A built image requires its digest and successful runtime compatibility checks.")
		}
		if err := finishEnvironmentOperationTx(ctx, q, op, "succeeded", "", digest); err != nil {
			return err
		}
		for key, value := range req.Metadata {
			op.Metadata[key] = value
		}
		metadata, _ := json.Marshal(op.Metadata)
		if _, err := q.ExecContext(ctx, `UPDATE environment_operations SET image_digest=?,metadata=? WHERE id=?`, req.ImageDigest, string(metadata), op.ID); err != nil {
			return err
		}
		s, err := environmentStateTx(ctx, q, op.ProjectID)
		if err != nil {
			return err
		}
		r, err := environmentRevisionTx(ctx, q, op.RevisionID)
		if err != nil {
			return err
		}
		id, err := queueEnvironmentOperationTx(ctx, q, s, r, "validate", req.ImageDigest, op.RuntimeIdentity, op.RuntimeBinding, 1)
		if err != nil {
			return err
		}
		return preserveEnvironmentRepairTx(ctx, q, id, op)
	}
	if strings.TrimSpace(req.Error) == "" {
		return Usagef("A failed execution requires its cause.")
	}
	if err := finishEnvironmentOperationTx(ctx, q, op, "failed", req.Error, digest); err != nil {
		return err
	}
	details, hasDetails := req.Metadata["failure_details"]
	limit, _ := req.Metadata["attempt_limit"].(bool)
	if hasDetails || limit {
		metadata := map[string]any{}
		for key, value := range op.Metadata {
			metadata[key] = value
		}
		if hasDetails {
			text, valid := details.(string)
			if !valid || len(text) > 24000 {
				return Usagef("The execution details must contain at most 24000 bytes of text.")
			}
			metadata["failure_details"] = text
		}
		if limit {
			metadata["attempt_limit"] = true
		}
		body, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE environment_operations SET metadata=? WHERE id=?`, string(body), op.ID); err != nil {
			return err
		}
	}
	s, err := environmentStateTx(ctx, q, op.ProjectID)
	if err != nil {
		return err
	}
	if op.Attempt < 2 {
		r, err := environmentRevisionTx(ctx, q, op.RevisionID)
		if err != nil {
			return err
		}
		id, err := queueEnvironmentOperationTx(ctx, q, s, r, op.Kind, op.ImageDigest, op.RuntimeIdentity, op.RuntimeBinding, op.Attempt+1)
		if err != nil {
			return err
		}
		op.Metadata["previous_operation_id"] = op.ID
		markEnvironmentReplacement(op.Metadata, op, limit)
		metadata, _ := json.Marshal(op.Metadata)
		_, err = q.ExecContext(ctx, `UPDATE environment_operations SET retry_at=datetime('now','+30 seconds'),metadata=? WHERE id=?`, string(metadata), id)
		return err
	}
	if op.Kind == "cache-clear" {
		return restoreEnvironmentCacheStateTx(ctx, q, op)
	}
	_, err = q.ExecContext(ctx, `UPDATE project_environments SET phase='failed',cause=? WHERE project_id=?`, req.Error, op.ProjectID)
	return err
}

func restoreEnvironmentCacheStateTx(ctx context.Context, q Q, op EnvironmentOperation) error {
	prior, ok := op.Metadata["cache_previous"].(map[string]any)
	if !ok {
		return Domainf("Cache removal has no retained Project state.")
	}
	_, err := q.ExecContext(ctx, `UPDATE project_environments SET phase=?,cause=?,instruction=? WHERE project_id=? AND COALESCE(draft_revision_id,'')=?`, prior["phase"], prior["cause"], prior["instruction"], op.ProjectID, prior["draft_revision_id"])
	return err
}

func environmentReportTx(ctx context.Context, q Q, op EnvironmentOperation, req EnvironmentRequest) error {
	r, err := environmentRevisionTx(ctx, q, op.RevisionID)
	if err != nil {
		return err
	}
	reportConfiguration := r.Configuration
	reportConfiguration.Secrets = append([]EnvironmentVariable{}, r.Configuration.Secrets...)
	if req.Configuration != nil {
		reportConfiguration.Secrets = append(reportConfiguration.Secrets, req.Configuration.Secrets...)
	}
	if err := validateEnvironmentReport(&req, &reportConfiguration); err != nil {
		return err
	}
	if req.Configuration != nil {
		if err := ValidateEnvironmentConfiguration(req.Configuration); err != nil {
			return err
		}
	}
	digest := environmentHash(req)
	if op.ReportDigest != "" {
		if op.ReportDigest == digest {
			return nil
		}
		return Domainf("This setup execution already has another result.")
	}
	if op.Kind != "setup" && op.Kind != "validate" {
		return Domainf("Only a setup agent can submit a report.")
	}
	s, err := environmentStateTx(ctx, q, op.ProjectID)
	if err != nil {
		return err
	}
	if s.ActiveOperationID != op.ID {
		return Domainf("This setup execution no longer controls the Project.")
	}
	if s.SelectedRevisionID == "" && req.Outcome == "proposal" {
		var decided bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM environment_decisions WHERE project_id=?)`, s.ProjectID).Scan(&decided); err != nil {
			return err
		}
		if decided || op.Kind == "validate" {
			return Domainf("The initial setup continuation requires a complete report. Further image changes use the Environment page after import.")
		}
	}
	if req.Configuration != nil {
		if err = ValidateEnvironmentConfiguration(req.Configuration); err != nil {
			return err
		}
		if req.Outcome != "proposal" && environmentBuildDigest(*req.Configuration) != r.InputsDigest {
			return Domainf("Image dependencies require a proposal and operator approval.")
		}
		if req.Outcome == "proposal" && s.SelectedRevisionID == "" && environmentBuildDigest(*req.Configuration) == r.InputsDigest {
			return Domainf("An initial proposal must change the image inputs. Submit other configuration changes in a complete report.")
		}
		if req.Outcome != "proposal" && s.SelectedRevisionID != "" && op.Kind == "validate" && environmentHash(*req.Configuration) != environmentHash(r.Configuration) {
			return Domainf("Validation must use the approved configuration. Submit changes as a proposal.")
		}
		if req.Outcome == "proposal" || environmentHash(*req.Configuration) != environmentHash(r.Configuration) {
			r, err = saveEnvironmentRevisionTx(ctx, q, s, *req.Configuration, req.Summary, "setup:"+op.ID)
			if err != nil {
				return err
			}
		}
	}
	if req.Outcome == "complete" && s.SelectedRevisionID != "" && op.Kind == "setup" {
		return Domainf("Agent assistance must save a proposal for operator approval.")
	}
	resultID, err := newRunID()
	if err != nil {
		return err
	}
	tools, _ := json.Marshal(req.ToolVersions)
	packages, _ := json.Marshal(req.PackageRecords)
	if _, err = q.ExecContext(ctx, `INSERT INTO environment_results(id,project_id,operation_id,revision_id,image_digest,files_digest,source_commit,runtime_identity,outcome,summary,details,tool_versions,package_records) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, resultID, op.ProjectID, op.ID, r.ID, op.ImageDigest, req.FilesDigest, req.SourceCommit, op.RuntimeIdentity, req.Outcome, req.Summary, req.Details, string(tools), string(packages)); err != nil {
		return err
	}
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata["report_outcome"] = req.Outcome
	if req.Outcome == "proposal" {
		op.Metadata["proposed_revision_id"] = r.ID
	}
	metadata, err := json.Marshal(op.Metadata)
	if err != nil {
		return err
	}
	if _, err = q.ExecContext(ctx, `UPDATE environment_operations SET metadata=? WHERE id=?`, string(metadata), op.ID); err != nil {
		return err
	}
	if req.Outcome == "proposal" {
		if candidate, _ := op.Metadata["candidate"].(bool); candidate {
			if _, err = q.ExecContext(ctx, `INSERT INTO environment_proposals(revision_id,run_id,task_id,source_commit) SELECT ?,run_id,task_id,? FROM environment_proposals WHERE revision_id=? ON CONFLICT(revision_id) DO NOTHING`, r.ID, r.Configuration.SourceCommit, op.RevisionID); err != nil {
				return err
			}
		}
		if err = finishEnvironmentOperationTx(ctx, q, op, "succeeded", "", digest); err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `UPDATE project_environments SET phase='approval',cause='',instruction='' WHERE project_id=?`, op.ProjectID)
		return err
	}
	if !imageDigest.MatchString(req.FilesDigest) {
		return Usagef("Setup results require the digest of checked Project files.")
	}
	if s.SelectedRevisionID != "" {
		if !commitDigest.MatchString(req.SourceCommit) || r.Configuration.SourceCommit != req.SourceCommit {
			return Domainf("Validation results require the intended source commit.")
		}
	}
	if err = finishEnvironmentOperationTx(ctx, q, op, "succeeded", "", digest); err != nil {
		return err
	}
	return queueEnvironmentAcceptanceTx(ctx, q, s, r, op, resultID, req.FilesDigest, req.SourceCommit)
}

func queueEnvironmentAcceptanceTx(ctx context.Context, q Q, s EnvironmentState, r EnvironmentRevision, prior EnvironmentOperation, result, files, source string) error {
	id, err := queueEnvironmentOperationTx(ctx, q, s, r, "accept", prior.ImageDigest, prior.RuntimeIdentity, prior.RuntimeBinding, 1)
	if err != nil {
		return err
	}
	metadata := map[string]any{"result_id": result, "files_digest": files, "source_commit": source, "initial_setup": s.SelectedRevisionID == "", "previous_operation_id": prior.ID, "validation_operation_id": prior.ID}
	for _, key := range []string{"proposal_operation_id", "denied_build_files", "setup_id"} {
		if value, ok := prior.Metadata[key]; ok {
			metadata[key] = value
		}
	}
	if previous, ok := prior.Metadata["repair_previous"]; ok {
		metadata["repair_previous"] = previous
	}
	if removed, ok := prior.Metadata["removed_files"]; ok {
		metadata["removed_files"] = removed
	}
	var origin string
	if err = q.QueryRowContext(ctx, `SELECT run_id FROM environment_proposals WHERE revision_id=?`, r.ID).Scan(&origin); err != nil && err != sql.ErrNoRows {
		return err
	}
	if origin != "" {
		metadata["candidate"] = true
		metadata["source_run_id"] = origin
		metadata["candidate_source"] = source
	}
	body, _ := json.Marshal(metadata)
	_, err = q.ExecContext(ctx, `UPDATE environment_operations SET metadata=? WHERE id=?`, string(body), id)
	return err
}

func acceptEnvironmentTx(ctx context.Context, q Q, s EnvironmentState, r EnvironmentRevision, image string) error {
	if _, err := q.ExecContext(ctx, `UPDATE project_environments SET selected_revision_id=?,draft_revision_id=?,selected_base_digest=?,selected_image_digest=?,readiness='ready',phase='complete',cause='',authorization='change',instruction='' WHERE project_id=?`, r.ID, r.ID, r.Configuration.BaseDigest, image, s.ProjectID); err != nil {
		return err
	}
	if s.SelectedRevisionID == "" {
		if _, err := q.ExecContext(ctx, `UPDATE settings SET discord_available=1 WHERE id=1`); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, `UPDATE projects SET setup_status='ready',status=CASE WHEN (SELECT pause_after_setup FROM project_environments WHERE project_id=projects.id)=1 THEN 'paused' ELSE 'active' END WHERE id=? AND status<>'archived'`, s.ProjectID)
		return err
	}
	return nil
}

func environmentResolveTx(ctx context.Context, q Q, req EnvironmentRequest) (any, error) {
	if req.RunID != "" {
		var revision, image, base, source, runtime string
		err := q.QueryRowContext(ctx, `SELECT revision_id,image_digest,base_digest,source_commit,runtime_identity FROM run_environments WHERE run_id=? AND project_id=?`, req.RunID, req.ProjectID).Scan(&revision, &image, &base, &source, &runtime)
		if err == nil {
			if req.SourceCommit != "" && req.SourceCommit != source {
				return nil, Domainf("The run already names another source commit.")
			}
			r, err := environmentRevisionTx(ctx, q, revision)
			if err != nil {
				return nil, err
			}
			return environmentResolutionTx(ctx, q, r, image, runtime, source)
		}
		if err != sql.ErrNoRows {
			return nil, err
		}
	}
	s, err := environmentStateTx(ctx, q, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if s.Readiness != "ready" || s.SelectedRevisionID == "" {
		return nil, Domainf("The Project environment requires setup before development.")
	}
	if s.RuntimeCondition != "" {
		return nil, Domainf("%s", s.RuntimeCondition)
	}
	if s.AssociationPending && req.CandidateSource == "" {
		return nil, Domainf("The source and environment association requires recovery before another run.")
	}
	if s.ActiveOperationID != "" || s.Phase == "queued" {
		return nil, Domainf("The Project environment change must finish before another run.")
	}
	r, err := environmentRevisionTx(ctx, q, s.SelectedRevisionID)
	if err != nil {
		return nil, err
	}
	candidateSource := req.CandidateSource
	if candidateSource == "" {
		candidateSource = req.SourceCommit
	}
	candidateRevision, candidateImage, err := environmentCandidateTx(ctx, q, req.ProjectID, candidateSource)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err == nil {
		r, err = environmentRevisionTx(ctx, q, candidateRevision)
		if err != nil {
			return nil, err
		}
		s.SelectedRevisionID, s.SelectedImageDigest, s.SelectedBaseDigest = r.ID, candidateImage, r.Configuration.BaseDigest
	}
	if req.RunID != "" {
		if !environmentID.MatchString(req.RunID) || !commitDigest.MatchString(req.SourceCommit) || req.RuntimeIdentity == "" {
			return nil, Usagef("A run environment requires its source commit and Culler runtime identity.")
		}
		_, err = q.ExecContext(ctx, `INSERT INTO run_environments(run_id,project_id,revision_id,image_digest,base_digest,source_commit,runtime_identity) VALUES(?,?,?,?,?,?,?)`, req.RunID, req.ProjectID, r.ID, s.SelectedImageDigest, s.SelectedBaseDigest, req.SourceCommit, req.RuntimeIdentity)
		if err != nil {
			return nil, err
		}
	}
	return environmentResolutionTx(ctx, q, r, s.SelectedImageDigest, req.RuntimeIdentity, req.SourceCommit)
}

func environmentResolutionTx(ctx context.Context, q Q, revision EnvironmentRevision, image, runtime, source string) (any, error) {
	result := map[string]any{"image_digest": image, "base_digest": revision.Configuration.BaseDigest, "configuration_revision": revision.ID, "configuration": revision.Configuration, "runtime_identity": runtime, "source_commit": source}
	return result, nil
}

// Captures prove ancestry before a later task version can use a candidate image.
func environmentCandidateTx(ctx context.Context, q Q, project, source string) (string, string, error) {
	var revision, image string
	err := q.QueryRowContext(ctx, `WITH RECURSIVE source_edges(parent,child) AS (
 SELECT v.input_commit,v.result_commit FROM task_versions v JOIN tasks t ON t.id=v.task_id WHERE t.project=?
 UNION SELECT v.result_commit,m.candidate_commit FROM merge_candidates m JOIN task_versions v ON v.id=m.version_id
 JOIN tasks t ON t.id=v.task_id WHERE t.project=? AND m.status='merged'
 ), ancestors(commit_sha) AS (
 SELECT ? UNION SELECT e.parent FROM source_edges e JOIN ancestors a ON a.commit_sha=e.child
 ) SELECT c.revision_id,c.image_digest FROM environment_candidates c JOIN ancestors a ON a.commit_sha=c.source_commit
 WHERE c.project_id=? AND c.accepted_commit IS NULL ORDER BY c.rowid DESC LIMIT 1`, project, project, source, project).Scan(&revision, &image)
	return revision, image, err
}

func environmentAssociationCandidateTx(ctx context.Context, q Q, project, source string) (string, string, string, error) {
	var revision, image, accepted string
	err := q.QueryRowContext(ctx, `SELECT c.revision_id,c.image_digest,COALESCE(c.accepted_commit,'') FROM environment_associations a JOIN environment_candidates c ON c.revision_id=a.revision_id WHERE a.project_id=? AND a.candidate_source=? AND a.status<>'canceled'`, project, source).Scan(&revision, &image, &accepted)
	if err != sql.ErrNoRows {
		return revision, image, accepted, err
	}
	revision, image, err = environmentCandidateTx(ctx, q, project, source)
	return revision, image, "", err
}

func environmentSourceAcceptedTx(ctx context.Context, q Q, s EnvironmentState, req EnvironmentRequest) error {
	if !commitDigest.MatchString(req.SourceCommit) || !commitDigest.MatchString(req.CandidateSource) {
		return Usagef("Source acceptance requires the applied commit and its candidate source.")
	}
	revision, image, accepted, err := environmentAssociationCandidateTx(ctx, q, s.ProjectID, req.CandidateSource)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if accepted != "" {
		if accepted == req.SourceCommit {
			return nil
		}
		return Domainf("The candidate environment already belongs to another accepted source.")
	}
	if s.ActiveOperationID != "" {
		return Domainf("Source acceptance waits for the active environment operation.")
	}
	var expected, candidate string
	err = q.QueryRowContext(ctx, `SELECT source_commit,candidate_source FROM environment_associations WHERE project_id=? AND status='pending'`, s.ProjectID).Scan(&expected, &candidate)
	if err == sql.ErrNoRows {
		return Domainf("Source acceptance requires its recorded pending association.")
	}
	if err != nil {
		return err
	}
	if candidate != req.CandidateSource || (expected != "" && expected != req.SourceCommit) {
		return Domainf("The source acceptance differs from its recorded association.")
	}
	r, err := environmentRevisionTx(ctx, q, revision)
	if err != nil {
		return err
	}
	if err = acceptEnvironmentTx(ctx, q, s, r, image); err != nil {
		return err
	}
	if s.DraftRevisionID != revision {
		if _, err = q.ExecContext(ctx, `UPDATE project_environments SET draft_revision_id=? WHERE project_id=?`, s.DraftRevisionID, s.ProjectID); err != nil {
			return err
		}
	}
	if _, err = q.ExecContext(ctx, `UPDATE environment_associations SET status='complete',source_commit=?,updated_at=datetime('now') WHERE project_id=? AND candidate_source=?`, req.SourceCommit, s.ProjectID, req.CandidateSource); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `UPDATE environment_candidates SET accepted_commit=? WHERE revision_id=? AND accepted_commit IS NULL`, req.SourceCommit, revision)
	return err
}

func environmentSourceAssociationTx(ctx context.Context, q Q, s EnvironmentState, action string, req EnvironmentRequest) error {
	if !commitDigest.MatchString(req.CandidateSource) || (req.SourceCommit != "" && !commitDigest.MatchString(req.SourceCommit)) {
		return Usagef("A source association requires its candidate commit and any known applied commit.")
	}
	revision, _, accepted, err := environmentAssociationCandidateTx(ctx, q, s.ProjectID, req.CandidateSource)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if accepted != "" {
		return nil
	}
	if action == "source-aborted" {
		_, err = q.ExecContext(ctx, `UPDATE environment_associations SET status='canceled',updated_at=datetime('now') WHERE project_id=? AND candidate_source=? AND status='pending'`, s.ProjectID, req.CandidateSource)
		return err
	}
	var prior, source string
	err = q.QueryRowContext(ctx, `SELECT candidate_source,source_commit FROM environment_associations WHERE project_id=? AND status='pending'`, s.ProjectID).Scan(&prior, &source)
	if err == nil {
		if prior != req.CandidateSource || (source != "" && req.SourceCommit != "" && source != req.SourceCommit) {
			return Domainf("Another source and environment association requires recovery.")
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO environment_associations(project_id,revision_id,candidate_source,source_commit,status) VALUES(?,?,?,?,'pending') ON CONFLICT(project_id) DO UPDATE SET revision_id=excluded.revision_id,candidate_source=excluded.candidate_source,source_commit=excluded.source_commit,status='pending',updated_at=datetime('now')`, s.ProjectID, revision, req.CandidateSource, req.SourceCommit)
	return err
}
