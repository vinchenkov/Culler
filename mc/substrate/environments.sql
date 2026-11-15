CREATE TABLE project_environments (
    project_id TEXT PRIMARY KEY REFERENCES projects(id),
    setup_id TEXT NOT NULL UNIQUE,
    vision_revision TEXT NOT NULL DEFAULT '',
    selected_base_digest TEXT NOT NULL DEFAULT '',
    selected_revision_id TEXT REFERENCES environment_revisions(id),
    selected_image_digest TEXT NOT NULL DEFAULT '',
    draft_revision_id TEXT REFERENCES environment_revisions(id),
    readiness TEXT NOT NULL DEFAULT 'setup_required' CHECK(readiness IN ('setup_required','ready')),
    phase TEXT NOT NULL DEFAULT 'queued' CHECK(phase IN ('idle','queued','agent','approval','build','validate','complete','failed')),
    cause TEXT NOT NULL DEFAULT '',
    active_operation_id TEXT REFERENCES environment_operations(id),
    authorization TEXT NOT NULL DEFAULT 'initial' CHECK(authorization IN ('initial','change')),
    instruction TEXT NOT NULL DEFAULT '',
    pause_after_setup INTEGER NOT NULL DEFAULT 0 CHECK(pause_after_setup IN (0,1)),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE environment_revisions (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    editor TEXT NOT NULL,
    configuration TEXT NOT NULL CHECK(json_valid(configuration)),
    inputs_digest TEXT NOT NULL,
    explanation TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX environment_revisions_project ON environment_revisions(project_id,created_at);
CREATE TRIGGER environment_revision_immutable BEFORE UPDATE ON environment_revisions
BEGIN SELECT RAISE(ABORT,'Environment revisions are immutable'); END;

CREATE TABLE environment_approvals (
    revision_id TEXT PRIMARY KEY REFERENCES environment_revisions(id),
    project_id TEXT NOT NULL REFERENCES projects(id),
    inputs_digest TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TRIGGER environment_approval_immutable BEFORE UPDATE ON environment_approvals
BEGIN SELECT RAISE(ABORT,'Environment approvals are immutable'); END;

CREATE TABLE environment_operations (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    revision_id TEXT NOT NULL REFERENCES environment_revisions(id),
    kind TEXT NOT NULL CHECK(kind IN ('setup','build','validate','accept','cache-clear','runtime-check')),
    status TEXT NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','running','succeeded','failed','canceled')),
    inputs_digest TEXT NOT NULL,
    image_digest TEXT NOT NULL,
    runtime_identity TEXT NOT NULL DEFAULT '',
    runtime_binding TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(runtime_binding)),
    attempt INTEGER NOT NULL DEFAULT 1 CHECK(attempt BETWEEN 1 AND 3),
    error TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(metadata)),
    report_digest TEXT NOT NULL DEFAULT '',
    native_session_ref TEXT,
    trace_filename TEXT,
    heartbeat_at TEXT,
    retry_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    ended_at TEXT,
    CHECK((status IN ('queued','running')) = (ended_at IS NULL))
);
CREATE UNIQUE INDEX environment_operation_active ON environment_operations(project_id) WHERE status IN ('queued','running');
CREATE TRIGGER environment_operation_identity BEFORE UPDATE ON environment_operations
WHEN NEW.id<>OLD.id OR NEW.project_id<>OLD.project_id OR NEW.revision_id<>OLD.revision_id
 OR NEW.kind<>OLD.kind OR NEW.inputs_digest<>OLD.inputs_digest OR NEW.attempt<>OLD.attempt
BEGIN SELECT RAISE(ABORT,'Environment execution identity is immutable'); END;

CREATE TABLE environment_results (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    operation_id TEXT NOT NULL UNIQUE REFERENCES environment_operations(id),
    revision_id TEXT NOT NULL REFERENCES environment_revisions(id),
    image_digest TEXT NOT NULL,
    files_digest TEXT NOT NULL DEFAULT '',
    source_commit TEXT NOT NULL DEFAULT '',
    runtime_identity TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK(outcome IN ('proposal','complete')),
    summary TEXT NOT NULL CHECK(length(trim(summary))>0),
    details TEXT NOT NULL DEFAULT '',
    tool_versions TEXT NOT NULL CHECK(json_valid(tool_versions)),
    package_records TEXT NOT NULL CHECK(json_valid(package_records)),
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TRIGGER environment_result_immutable BEFORE UPDATE ON environment_results
BEGIN SELECT RAISE(ABORT,'Environment results are immutable'); END;
CREATE TABLE environment_decisions (
    revision_id TEXT PRIMARY KEY REFERENCES environment_revisions(id),
    project_id TEXT NOT NULL UNIQUE REFERENCES projects(id),
    operation_id TEXT NOT NULL REFERENCES environment_operations(id),
    decision TEXT NOT NULL CHECK(decision IN ('approved','denied')),
    current_revision_id TEXT NOT NULL REFERENCES environment_revisions(id),
    current_image_digest TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TRIGGER environment_decision_immutable BEFORE UPDATE ON environment_decisions
BEGIN SELECT RAISE(ABORT,'Environment decisions are immutable'); END;

CREATE TABLE run_environments (
    run_id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    revision_id TEXT NOT NULL REFERENCES environment_revisions(id),
    image_digest TEXT NOT NULL,
    base_digest TEXT NOT NULL,
    source_commit TEXT NOT NULL,
    runtime_identity TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TRIGGER run_environment_immutable BEFORE UPDATE ON run_environments
BEGIN SELECT RAISE(ABORT,'Run environments are immutable'); END;

CREATE TABLE environment_run_evidence (
    run_id TEXT PRIMARY KEY REFERENCES run_environments(run_id),
    tool_versions TEXT NOT NULL CHECK(json_valid(tool_versions)),
    package_records TEXT NOT NULL CHECK(json_valid(package_records)),
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TRIGGER environment_run_evidence_immutable BEFORE UPDATE ON environment_run_evidence
BEGIN SELECT RAISE(ABORT,'Run environment evidence is immutable'); END;

CREATE TABLE environment_storage (
    project_id TEXT PRIMARY KEY REFERENCES projects(id),
    image_digest TEXT NOT NULL,
    metadata TEXT NOT NULL CHECK(json_valid(metadata)),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE environment_base_sources (
    base_digest TEXT NOT NULL,
    registry_reference TEXT NOT NULL,
    release_identity TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY(base_digest,registry_reference)
);
CREATE TRIGGER environment_base_source_immutable BEFORE UPDATE ON environment_base_sources
BEGIN SELECT RAISE(ABORT,'Base image sources are immutable'); END;
CREATE TABLE environment_runtime_checks (
    project_id TEXT PRIMARY KEY REFERENCES projects(id),
    image_digest TEXT NOT NULL,
    runtime_identity TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('succeeded','failed')),
    error TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(metadata)),
    checked_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE environment_proposals (
    revision_id TEXT PRIMARY KEY REFERENCES environment_revisions(id),
    run_id TEXT NOT NULL REFERENCES runs(id),
    task_id INTEGER NOT NULL REFERENCES tasks(id),
    source_commit TEXT NOT NULL
);
CREATE TABLE environment_candidates (
    revision_id TEXT PRIMARY KEY REFERENCES environment_revisions(id),
    project_id TEXT NOT NULL REFERENCES projects(id),
    source_commit TEXT NOT NULL,
    image_digest TEXT NOT NULL,
    result_id TEXT NOT NULL REFERENCES environment_results(id),
    accepted_commit TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX environment_candidate_source ON environment_candidates(project_id,source_commit);

CREATE TABLE environment_associations (
    project_id TEXT PRIMARY KEY REFERENCES projects(id),
    revision_id TEXT NOT NULL REFERENCES environment_revisions(id),
    candidate_source TEXT NOT NULL,
    source_commit TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('pending','complete','canceled')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
