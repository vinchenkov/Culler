
CREATE TABLE repository_limits (
    project TEXT PRIMARY KEY REFERENCES projects(id),
    destination_quiet_seconds INTEGER NOT NULL DEFAULT 30 CHECK (destination_quiet_seconds BETWEEN 1 AND 3600),
    max_temporary_bytes INTEGER NOT NULL DEFAULT 21474836480 CHECK (max_temporary_bytes > 0)
);

CREATE TABLE repository_runs (
    run_id TEXT PRIMARY KEY REFERENCES runs(id),
    task_id INTEGER REFERENCES tasks(id),
    wiki_update TEXT REFERENCES wiki_updates(run_id),
    input_commit TEXT NOT NULL,
    destination_commit TEXT NOT NULL,
    version_id INTEGER REFERENCES task_versions(id),
    temporary_bytes INTEGER NOT NULL CHECK (temporary_bytes >= 0),
    captured INTEGER NOT NULL DEFAULT 0 CHECK (captured IN (0, 1)),
    cleaned INTEGER NOT NULL DEFAULT 0 CHECK (cleaned IN (0, 1)),
    cleanup_error TEXT,
    CHECK (cleaned = 0 OR captured = 1),
    CHECK ((task_id IS NULL) <> (wiki_update IS NULL)),
    CHECK (wiki_update IS NULL OR version_id IS NULL)
);

CREATE TABLE repository_transfers (
    run_id TEXT PRIMARY KEY REFERENCES runs(id),
    producer_run TEXT NOT NULL REFERENCES repository_runs(run_id),
    task_id INTEGER NOT NULL REFERENCES tasks(id),
    finished INTEGER NOT NULL DEFAULT 0 CHECK(finished IN (0,1))
);

CREATE TABLE repository_observations (
    run_id TEXT PRIMARY KEY REFERENCES runs(id),
    task_id INTEGER NOT NULL REFERENCES tasks(id),
    operation TEXT NOT NULL CHECK (operation = 'observe'),
    result TEXT CHECK (result IS NULL OR json_valid(result))
);

CREATE TRIGGER repository_observations_immutable
BEFORE UPDATE ON repository_observations
WHEN NEW.run_id IS NOT OLD.run_id OR NEW.task_id IS NOT OLD.task_id
  OR NEW.operation IS NOT OLD.operation OR OLD.result IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'the repository observation preserves its assignment and result');
END;

CREATE TRIGGER repository_observations_assigned_run
BEFORE INSERT ON repository_observations
WHEN NOT EXISTS (SELECT 1 FROM repository_executions x
    WHERE x.run_id=NEW.run_id AND x.task_id=NEW.task_id AND x.operation=NEW.operation AND x.ended_at IS NULL)
BEGIN
    SELECT RAISE(ABORT, 'the repository observation requires its assigned active execution');
END;

CREATE TRIGGER repository_observations_no_delete
BEFORE DELETE ON repository_observations
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='repository_observations' AND row_id=OLD.rowid)
BEGIN
    SELECT RAISE(ABORT, 'the repository observation preserves its history');
END;

CREATE TABLE version_submissions (
    run_id TEXT PRIMARY KEY REFERENCES repository_runs(run_id),
    task_id INTEGER NOT NULL REFERENCES tasks(id),
    input_commit TEXT NOT NULL,
    result_commit TEXT NOT NULL,
    evidence_path TEXT,
    kind TEXT NOT NULL DEFAULT 'submission' CHECK(kind IN ('submission','checkpoint')),
    prior_version INTEGER REFERENCES task_versions(id),
    captured_version INTEGER REFERENCES task_versions(id)
);

CREATE TABLE task_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER NOT NULL REFERENCES tasks(id),
    run_id TEXT NOT NULL UNIQUE REFERENCES repository_runs(run_id),
    input_commit TEXT NOT NULL,
    result_commit TEXT NOT NULL,
    destination_branch TEXT NOT NULL,
    retained_ref TEXT NOT NULL UNIQUE,
    evidence_path TEXT,
    kind TEXT NOT NULL DEFAULT 'submission' CHECK(kind IN ('submission','checkpoint')),
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE version_verdicts (
    version_id INTEGER PRIMARY KEY REFERENCES task_versions(id),
    run_id TEXT NOT NULL UNIQUE REFERENCES repository_runs(run_id),
    outcome TEXT NOT NULL CHECK (outcome IN ('pass', 'correct')),
    evidence_path TEXT NOT NULL,
    problem TEXT,
    explanation_evidence TEXT,
    resolution TEXT,
    CHECK (outcome<>'correct' OR (length(trim(problem))>0 AND length(trim(explanation_evidence))>0 AND length(trim(resolution))>0
      AND problem IS NOT NULL AND explanation_evidence IS NOT NULL AND resolution IS NOT NULL))
);

CREATE TABLE merge_candidates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER REFERENCES tasks(id),
    wiki_update TEXT REFERENCES wiki_updates(run_id),
    run_id TEXT NOT NULL UNIQUE REFERENCES runs(id),
    version_id INTEGER REFERENCES task_versions(id),
    destination_branch TEXT NOT NULL,
    destination_commit TEXT NOT NULL,
    candidate_commit TEXT,
    result TEXT NOT NULL CHECK (json_valid(result)),
    status TEXT NOT NULL CHECK (status IN ('merged', 'conflict')),
    retained_ref TEXT UNIQUE,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    CHECK ((status = 'conflict') = (candidate_commit IS NULL)),
    CHECK (status <> 'merged' OR retained_ref IS NOT NULL),
    CHECK ((task_id IS NULL) <> (wiki_update IS NULL)),
    CHECK ((task_id IS NULL) = (version_id IS NULL))
);

CREATE TRIGGER merge_candidates_wiki_update BEFORE INSERT ON merge_candidates
WHEN NEW.wiki_update IS NOT NULL AND (NEW.destination_branch <> 'main' OR NOT EXISTS (
    SELECT 1 FROM wiki_updates w WHERE w.run_id=NEW.wiki_update AND w.ended_at IS NULL
      AND w.wiki_commit IS NOT NULL AND w.wiki_commit IS NOT w.start_commit))
BEGIN SELECT RAISE(ABORT, 'the merge candidate of a wiki commit requires its active wiki update'); END;

CREATE TABLE review_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER NOT NULL REFERENCES tasks(id),
    version_id INTEGER NOT NULL REFERENCES task_versions(id),
    candidate_id INTEGER REFERENCES merge_candidates(id),
    render_path TEXT NOT NULL,
    report_sha256 TEXT NOT NULL,
    thesis TEXT NOT NULL,
    criteria TEXT NOT NULL,
    requirements_version INTEGER NOT NULL DEFAULT 1 CHECK (requirements_version >= 1),
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (task_id, version_id)
);

CREATE TABLE review_decisions (
    packet_id INTEGER PRIMARY KEY REFERENCES review_versions(id),
    decision TEXT NOT NULL CHECK (decision IN ('approved', 'revise', 'cancelled')),
    reason TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE correction_results (
    version_id INTEGER PRIMARY KEY REFERENCES task_versions(id),
    task_id INTEGER NOT NULL REFERENCES tasks(id),
    outcome TEXT NOT NULL CHECK (outcome IN ('pass', 'correct')),
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE merge_operations (
    run_id TEXT PRIMARY KEY REFERENCES runs(id),
    task_id INTEGER REFERENCES tasks(id),
    wiki_update TEXT REFERENCES wiki_updates(run_id),
    project TEXT NOT NULL REFERENCES projects(id),
    operation TEXT NOT NULL CHECK (operation IN ('prepare', 'apply')),
    version_id INTEGER REFERENCES task_versions(id),
    candidate_id INTEGER REFERENCES merge_candidates(id),
    destination_branch TEXT NOT NULL,
    packet_id INTEGER REFERENCES review_versions(id),
    result TEXT CHECK (result IS NULL OR json_valid(result)),
    finished INTEGER NOT NULL DEFAULT 0 CHECK (finished IN (0, 1)),
    CHECK (operation <> 'apply' OR candidate_id IS NOT NULL),
    CHECK ((task_id IS NULL) <> (wiki_update IS NULL)),
    CHECK ((task_id IS NULL) = (version_id IS NULL)),
    CHECK (wiki_update IS NULL OR packet_id IS NULL)
);

CREATE TRIGGER merge_operations_wiki_update BEFORE INSERT ON merge_operations
WHEN NEW.wiki_update IS NOT NULL AND (NEW.destination_branch <> 'main' OR NOT EXISTS (
    SELECT 1 FROM wiki_updates w WHERE w.run_id=NEW.wiki_update AND w.project=NEW.project
      AND w.ended_at IS NULL AND w.wiki_commit IS NOT NULL AND w.wiki_commit IS NOT w.start_commit)
  OR (NEW.candidate_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM merge_candidates c
      WHERE c.id=NEW.candidate_id AND c.wiki_update=NEW.wiki_update)))
BEGIN SELECT RAISE(ABORT, 'the merge of a wiki commit requires its active wiki update'); END;

CREATE UNIQUE INDEX merge_destination_authorization
ON merge_operations(project, destination_branch) WHERE operation = 'apply' AND finished = 0;

-- Each resident instance registers one owner number at start.
CREATE TABLE execution_owners (
    owner         INTEGER PRIMARY KEY AUTOINCREMENT,
    registered_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TRIGGER execution_owners_immutable BEFORE UPDATE ON execution_owners
BEGIN SELECT RAISE(ABORT, 'the owner record is immutable'); END;
CREATE TRIGGER execution_owners_no_delete BEFORE DELETE ON execution_owners
BEGIN SELECT RAISE(ABORT, 'the owner record is retained'); END;

-- One attempt of one repository operation. The run row carries the run history.
CREATE TABLE repository_executions (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id              TEXT NOT NULL UNIQUE REFERENCES runs(id),
    project             TEXT NOT NULL REFERENCES projects(id),
    task_id             INTEGER REFERENCES tasks(id),
    wiki_update         TEXT REFERENCES wiki_updates(run_id),
    operation           TEXT NOT NULL CHECK (operation IN
                        ('capture','cleanup','observe','prepare','apply','reconcile')),
    target              TEXT NOT NULL CHECK (length(target) BETWEEN 1 AND 64),
    owner               INTEGER NOT NULL REFERENCES execution_owners(owner),
    effect              TEXT NOT NULL CHECK (json_valid(effect) AND length(effect) <= 65536),
    started_at          TEXT NOT NULL DEFAULT (datetime('now')),
    boot_id             TEXT NOT NULL,
    heartbeat_uptime_ms INTEGER NOT NULL,
    deadline_uptime_ms  INTEGER NOT NULL,
    ended_at            TEXT,
    end_reason          TEXT CHECK (end_reason IN ('result','failure','evaluation-pending','container-present',
                        'owner-change','heartbeat','deadline','vm-restart')),
    retry_after         TEXT,
    charged             INTEGER NOT NULL DEFAULT 0 CHECK (charged IN (0,1)),
    stopped_at          TEXT,
    -- end_condition: the condition of an end, in plain words.
    end_condition       TEXT CHECK (end_condition IS NULL OR length(end_condition) BETWEEN 1 AND 2048),
    CHECK ((ended_at IS NULL) = (end_reason IS NULL)),
    CHECK (ended_at IS NOT NULL OR end_condition IS NULL),
    CHECK (wiki_update IS NULL OR charged = 0),
    CHECK (ended_at IS NOT NULL OR (retry_after IS NULL AND charged = 0 AND stopped_at IS NULL)),
    CHECK (end_reason IS NOT 'result' OR (retry_after IS NULL AND charged = 0)),
    CHECK ((task_id IS NULL) <> (wiki_update IS NULL)),
    CHECK (wiki_update IS NULL OR operation IN ('capture','cleanup','prepare','apply','reconcile'))
);

-- Invariant: each operation target has a maximum of one active execution.
CREATE UNIQUE INDEX repository_executions_active_target
ON repository_executions(target) WHERE ended_at IS NULL;

-- Invariant: each Project has a maximum of one active merge step.
-- The limit counts the merge steps of tasks and of wiki commits together.
CREATE UNIQUE INDEX repository_executions_active_merge_step
ON repository_executions(project)
WHERE ended_at IS NULL AND operation IN ('observe','prepare','apply','reconcile');

CREATE INDEX repository_executions_target_history ON repository_executions(target, id);
CREATE INDEX repository_executions_project ON repository_executions(project, ended_at);
CREATE INDEX repository_executions_unstopped ON repository_executions(stopped_at)
WHERE ended_at IS NOT NULL AND stopped_at IS NULL;

-- The Vision state that the latest start request used for each Project.
CREATE TABLE vision_checks (
    project    TEXT PRIMARY KEY REFERENCES projects(id),
    ready      INTEGER NOT NULL CHECK (ready IN (0,1)),
    checked_at TEXT NOT NULL
);

CREATE TRIGGER repository_executions_assigned_run BEFORE INSERT ON repository_executions
WHEN NEW.ended_at IS NOT NULL
  OR NEW.owner IS NOT (SELECT MAX(owner) FROM execution_owners)
  OR NOT EXISTS (SELECT 1 FROM runs r WHERE r.id=NEW.run_id AND r.binding='mechanical'
      AND r.tier='pipeline' AND r.subject IS NEW.task_id AND r.project=NEW.project
      AND r.ended_at IS NULL AND r.outcome IS NULL)
  OR (NEW.wiki_update IS NOT NULL AND NOT EXISTS (SELECT 1 FROM wiki_updates w
      WHERE w.run_id=NEW.wiki_update AND w.project=NEW.project))
BEGIN SELECT RAISE(ABORT, 'an execution requires its assigned active run and the current owner'); END;

-- A repository operation of a wiki update waits for the end of the run of that wiki update.
CREATE TRIGGER repository_executions_wiki_run BEFORE INSERT ON repository_executions
WHEN NEW.wiki_update IS NOT NULL AND EXISTS (SELECT 1 FROM runs r
    WHERE r.id=NEW.wiki_update AND r.ended_at IS NULL)
BEGIN SELECT RAISE(ABORT, 'the repository operation waits for the run of its wiki update'); END;

-- A repository operation of a task, except cleanup, waits for the agent run of that task.
CREATE TRIGGER repository_executions_task_agent_run BEFORE INSERT ON repository_executions
WHEN NEW.operation <> 'cleanup' AND EXISTS (SELECT 1 FROM leases l JOIN runs r ON r.id=l.run_id
    WHERE l.subject=NEW.task_id AND r.ended_at IS NULL AND r.outcome IS NULL)
BEGIN SELECT RAISE(ABORT, 'the repository operation waits for the agent run of its task'); END;

-- Artifact expiry and a repository operation do not run at the same time in one Project.
CREATE TRIGGER repository_executions_artifact_expiry BEFORE INSERT ON repository_executions
WHEN EXISTS (SELECT 1 FROM artifact_retention a JOIN tasks t ON t.id=a.task_id
    WHERE a.token IS NOT NULL AND t.project=NEW.project)
BEGIN SELECT RAISE(ABORT, 'artifact expiry is active; retry the repository operation'); END;

CREATE TRIGGER repository_executions_preserved BEFORE UPDATE ON repository_executions
WHEN NEW.id<>OLD.id OR NEW.run_id<>OLD.run_id OR NEW.project<>OLD.project
  OR NEW.task_id IS NOT OLD.task_id OR NEW.wiki_update IS NOT OLD.wiki_update
  OR NEW.operation<>OLD.operation OR NEW.target<>OLD.target OR NEW.owner<>OLD.owner
  OR NEW.effect<>OLD.effect OR NEW.started_at<>OLD.started_at OR NEW.boot_id<>OLD.boot_id
  OR NEW.deadline_uptime_ms<>OLD.deadline_uptime_ms
  OR (OLD.ended_at IS NOT NULL AND (NEW.ended_at IS NOT OLD.ended_at OR NEW.end_reason IS NOT OLD.end_reason
      OR NEW.retry_after IS NOT OLD.retry_after OR NEW.charged<>OLD.charged
      OR NEW.heartbeat_uptime_ms<>OLD.heartbeat_uptime_ms
      OR NEW.end_condition IS NOT OLD.end_condition))
  OR (OLD.stopped_at IS NOT NULL AND NEW.stopped_at IS NOT OLD.stopped_at)
BEGIN SELECT RAISE(ABORT, 'the execution preserves its assignment and its end'); END;

CREATE TRIGGER repository_executions_no_delete
BEFORE DELETE ON repository_executions
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='repository_executions' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'the execution preserves its history'); END;

-- Only agent runs hold the Project lease. A claim updates the lease before it
-- inserts the run, so one trigger covers each order.
CREATE TRIGGER leases_hold_agent_runs BEFORE UPDATE OF run_id ON leases
WHEN NEW.run_id IS NOT NULL AND EXISTS (SELECT 1 FROM runs r WHERE r.id=NEW.run_id AND r.binding='mechanical')
BEGIN SELECT RAISE(ABORT, 'only an agent run holds the Project lease'); END;

CREATE TRIGGER runs_mechanical_hold_no_lease AFTER INSERT ON runs
WHEN NEW.binding='mechanical' AND EXISTS (SELECT 1 FROM leases l WHERE l.run_id=NEW.id)
BEGIN SELECT RAISE(ABORT, 'only an agent run holds the Project lease'); END;

-- Backstop: an agent run of a task waits for the active repository operation of that task.
CREATE TRIGGER leases_wait_for_task_execution BEFORE UPDATE OF run_id ON leases
WHEN NEW.run_id IS NOT NULL AND NEW.subject IS NOT NULL AND EXISTS (SELECT 1 FROM repository_executions x
    WHERE x.task_id=NEW.subject AND x.ended_at IS NULL AND x.operation<>'cleanup')
BEGIN SELECT RAISE(ABORT, 'the agent run waits for the repository operation of its task'); END;

-- A repository result requires the active execution of its operation. A
-- repeated record of the same result writes no row.
CREATE TRIGGER repository_observations_result_execution BEFORE UPDATE OF result ON repository_observations
WHEN NEW.result IS NOT NULL AND NOT EXISTS (SELECT 1 FROM repository_executions x
    WHERE x.run_id=OLD.run_id AND x.operation=OLD.operation AND x.ended_at IS NULL)
BEGIN SELECT RAISE(ABORT, 'a repository result requires its active execution'); END;

-- The end of a capture execution finishes the transfer before the execution row ends.
CREATE TRIGGER repository_transfers_finish_execution BEFORE UPDATE OF finished ON repository_transfers
WHEN NEW.finished<>OLD.finished AND NOT EXISTS (SELECT 1 FROM repository_executions x
    WHERE x.run_id=OLD.run_id AND x.operation='capture' AND x.ended_at IS NULL)
BEGIN SELECT RAISE(ABORT, 'a repository result requires its active execution'); END;

-- The captured version is the result of a capture.
CREATE TRIGGER version_submissions_capture_execution BEFORE UPDATE OF captured_version ON version_submissions
WHEN NEW.captured_version IS NOT OLD.captured_version AND NOT EXISTS (SELECT 1 FROM repository_executions x
    WHERE x.target=OLD.run_id AND x.operation='capture' AND x.ended_at IS NULL)
BEGIN SELECT RAISE(ABORT, 'a repository result requires its active execution'); END;

CREATE TRIGGER merge_operations_result_execution BEFORE UPDATE OF result, finished ON merge_operations
WHEN (NEW.result IS NOT OLD.result OR NEW.finished<>OLD.finished) AND NOT EXISTS (
    SELECT 1 FROM repository_executions x WHERE x.ended_at IS NULL AND (
        x.run_id=OLD.run_id
        OR (x.operation='reconcile' AND x.target=OLD.run_id)
        OR (x.operation='prepare' AND OLD.operation='prepare'
            AND x.task_id IS OLD.task_id AND x.wiki_update IS OLD.wiki_update)))
BEGIN SELECT RAISE(ABORT, 'a repository result requires its active execution'); END;

CREATE TRIGGER repository_runs_cleanup_execution BEFORE UPDATE OF cleaned, cleanup_error ON repository_runs
WHEN (NEW.cleaned<>OLD.cleaned OR NEW.cleanup_error IS NOT OLD.cleanup_error)
  AND NOT EXISTS (SELECT 1 FROM repository_executions x
      WHERE x.target=OLD.run_id AND x.operation='cleanup' AND x.ended_at IS NULL)
BEGIN SELECT RAISE(ABORT, 'a repository result requires its active execution'); END;

-- Only the result of the merge or of receipt recovery ends a wiki update with an unfinished merge authorization.
CREATE TRIGGER wiki_updates_end_after_merge BEFORE UPDATE OF ended_at ON wiki_updates
WHEN NEW.ended_at IS NOT NULL AND OLD.ended_at IS NULL
  AND EXISTS (SELECT 1 FROM merge_operations m WHERE m.wiki_update=NEW.run_id AND m.operation='apply' AND m.finished=0)
BEGIN SELECT RAISE(ABORT, 'a wiki update ends after the result of its merge'); END;

-- A wiki update fails after the end of each execution of it, except a cleanup.
CREATE TRIGGER wiki_updates_fail_after_execution BEFORE UPDATE OF ended_at ON wiki_updates
WHEN NEW.ended_at IS NOT NULL AND OLD.ended_at IS NULL AND NEW.result IS 'failed'
  AND EXISTS (SELECT 1 FROM repository_executions x WHERE x.wiki_update=NEW.run_id AND x.ended_at IS NULL
      AND x.operation<>'cleanup')
BEGIN SELECT RAISE(ABORT, 'a wiki update fails after the end of its execution'); END;

-- A wiki update whose wiki commit is on the main branch cannot fail.
CREATE TRIGGER wiki_updates_fail_without_merge BEFORE UPDATE OF ended_at ON wiki_updates
WHEN NEW.ended_at IS NOT NULL AND OLD.ended_at IS NULL AND NEW.result IS 'failed'
  AND EXISTS (SELECT 1 FROM merge_operations m WHERE m.wiki_update=NEW.run_id AND m.operation='apply'
      AND json_extract(m.result,'$.applied.commit') IS NOT NULL)
BEGIN SELECT RAISE(ABORT, 'a wiki update with a merged wiki commit cannot fail'); END;

-- Each Project starts with the default repository limits.
CREATE TRIGGER projects_receive_repository_limits AFTER INSERT ON projects
BEGIN
    INSERT INTO repository_limits(project) VALUES (NEW.id);
END;

CREATE TRIGGER authorized_workspace_preserved BEFORE UPDATE ON sandbox_profiles
WHEN NEW.workspace_root<>OLD.workspace_root AND EXISTS(
 SELECT 1 FROM projects w JOIN merge_operations m ON m.project=w.id
 WHERE w.sandbox_profile=OLD.id AND m.operation='apply' AND m.finished=0)
BEGIN SELECT RAISE(ABORT, 'the authorized update requires its Project repository'); END;

CREATE TRIGGER authorized_profile_preserved BEFORE UPDATE ON projects
WHEN NEW.sandbox_profile IS NOT OLD.sandbox_profile AND EXISTS(
 SELECT 1 FROM merge_operations m WHERE m.project=OLD.id AND m.operation='apply' AND m.finished=0)
BEGIN SELECT RAISE(ABORT, 'the authorized update requires its Project profile'); END;

CREATE TRIGGER task_versions_immutable BEFORE UPDATE ON task_versions
BEGIN SELECT RAISE(ABORT, 'submitted versions are immutable'); END;

CREATE TRIGGER repository_run_assignment BEFORE INSERT ON repository_runs
WHEN NEW.task_id IS NOT NULL
 AND NOT EXISTS(SELECT 1 FROM runs r WHERE r.id=NEW.run_id AND r.subject=NEW.task_id)
BEGIN SELECT RAISE(ABORT, 'the repository requires its assigned task'); END;

-- The repository of the run starts at the start commit. The repository of a
-- merge candidate step starts at the wiki commit.
CREATE TRIGGER repository_run_wiki_assignment BEFORE INSERT ON repository_runs
WHEN NEW.wiki_update IS NOT NULL AND NOT EXISTS (SELECT 1 FROM wiki_updates w JOIN runs r ON r.id=w.run_id
    WHERE w.run_id=NEW.wiki_update AND w.ended_at IS NULL AND (
      (NEW.run_id=NEW.wiki_update AND r.ended_at IS NULL AND r.outcome IS NULL AND w.start_commit=NEW.input_commit)
   OR (NEW.run_id<>NEW.wiki_update AND w.wiki_commit=NEW.input_commit AND EXISTS (SELECT 1 FROM repository_executions x
        WHERE x.run_id=NEW.run_id AND x.wiki_update=NEW.wiki_update AND x.operation='prepare' AND x.ended_at IS NULL))))
BEGIN SELECT RAISE(ABORT, 'the repository requires its active wiki update and its assigned commit'); END;

CREATE TRIGGER submitted_version_matches_capture BEFORE INSERT ON task_versions
WHEN NOT EXISTS(SELECT 1 FROM version_submissions s WHERE s.run_id=NEW.run_id AND s.task_id=NEW.task_id
 AND s.input_commit=NEW.input_commit AND s.result_commit=NEW.result_commit AND s.kind=NEW.kind)
BEGIN SELECT RAISE(ABORT, 'the retained version must match the submitted commit'); END;

CREATE TRIGGER submitted_version_selection BEFORE UPDATE OF submitted_version,checkpoint_version ON tasks
WHEN (NEW.submitted_version IS NOT NULL AND NOT EXISTS(SELECT 1 FROM task_versions v
 WHERE v.id=NEW.submitted_version AND v.task_id=NEW.id AND v.kind='submission'))
 OR (NEW.checkpoint_version IS NOT NULL AND NOT EXISTS(SELECT 1 FROM task_versions v
 WHERE v.id=NEW.checkpoint_version AND v.task_id=NEW.id AND v.kind='checkpoint'))
BEGIN SELECT RAISE(ABORT, 'the task requires its own submitted version or checkpoint'); END;

CREATE TRIGGER version_verdict_matches_run BEFORE INSERT ON version_verdicts
WHEN NOT EXISTS(SELECT 1 FROM repository_runs r JOIN task_versions v ON v.id=NEW.version_id JOIN runs run ON run.id=r.run_id
 WHERE r.run_id=NEW.run_id AND r.version_id=v.id AND r.task_id=v.task_id AND r.input_commit=v.result_commit AND run.role='verifier')
BEGIN SELECT RAISE(ABORT, 'the Verifier must assess its assigned submitted version'); END;

CREATE TRIGGER version_verdicts_immutable BEFORE UPDATE ON version_verdicts
BEGIN SELECT RAISE(ABORT, 'version verdicts are immutable'); END;

CREATE TRIGGER version_verdicts_no_delete
BEFORE DELETE ON version_verdicts
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='version_verdicts' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'Version verdicts require explicit Project deletion'); END;

CREATE TRIGGER source_version_requires_pass
BEFORE UPDATE OF source_version ON tasks
WHEN NEW.source_version IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM task_versions v JOIN version_verdicts verdict ON verdict.version_id=v.id
 WHERE v.id=NEW.source_version AND v.task_id=NEW.id AND verdict.outcome='pass')
BEGIN SELECT RAISE(ABORT, 'the verified source requires a Verifier pass'); END;

CREATE TRIGGER package_requires_verified_version
BEFORE UPDATE OF status ON tasks
WHEN NEW.status='packaged' AND (NEW.source_version IS NULL OR NEW.source_version IS NOT NEW.submitted_version)
BEGIN SELECT RAISE(ABORT, 'the Packager requires the verified submitted version'); END;

CREATE TRIGGER merge_candidates_no_delete
BEFORE DELETE ON merge_candidates
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='merge_candidates' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'merge candidates require explicit history cleanup'); END;

CREATE TRIGGER review_versions_no_delete
BEFORE DELETE ON review_versions
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='review_versions' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'Review versions require explicit Project deletion'); END;
CREATE TRIGGER task_versions_no_delete
BEFORE DELETE ON task_versions
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='task_versions' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'submitted versions require explicit history cleanup'); END;
CREATE TRIGGER merge_candidates_immutable BEFORE UPDATE ON merge_candidates
BEGIN SELECT RAISE(ABORT, 'merge candidates are immutable'); END;
CREATE TRIGGER review_versions_immutable BEFORE UPDATE ON review_versions
BEGIN SELECT RAISE(ABORT, 'review versions are immutable'); END;
CREATE TRIGGER review_decisions_immutable BEFORE UPDATE ON review_decisions
BEGIN SELECT RAISE(ABORT, 'review decisions are immutable'); END;
CREATE TRIGGER review_decisions_no_delete
BEFORE DELETE ON review_decisions
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='review_decisions' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'Review decisions require explicit Project deletion'); END;
CREATE TRIGGER correction_results_immutable BEFORE UPDATE ON correction_results
BEGIN SELECT RAISE(ABORT, 'correction results are immutable'); END;
CREATE TRIGGER correction_results_no_delete
BEFORE DELETE ON correction_results
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='correction_results' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'Correction results require explicit Project deletion'); END;

CREATE TRIGGER delivery_approval_requires_merged_version BEFORE UPDATE OF decision ON tasks
WHEN NEW.decision='approved'
 AND NOT EXISTS(SELECT 1 FROM task_versions v
 JOIN version_verdicts verdict ON verdict.version_id=NEW.source_version AND verdict.outcome='pass'
 JOIN merge_candidates c ON c.id=NEW.candidate_id AND c.version_id=v.id AND c.task_id=NEW.id
 WHERE v.id=NEW.submitted_version AND NEW.status='packaged' AND c.status='merged'
 AND (EXISTS(SELECT 1 FROM review_versions p JOIN review_decisions decision
 ON decision.packet_id=p.id WHERE p.task_id=NEW.id AND p.version_id=v.id AND decision.decision='approved')))
BEGIN SELECT RAISE(ABORT, 'approval requires the merged submitted version and its acceptance authority'); END;

CREATE TRIGGER version_submission_identity_preserved BEFORE UPDATE ON version_submissions
WHEN NEW.run_id<>OLD.run_id OR NEW.task_id<>OLD.task_id OR NEW.input_commit<>OLD.input_commit
 OR NEW.result_commit<>OLD.result_commit OR NEW.kind<>OLD.kind OR NEW.evidence_path IS NOT OLD.evidence_path
 OR NEW.prior_version IS NOT OLD.prior_version
 OR (OLD.captured_version IS NOT NULL AND NEW.captured_version IS NOT OLD.captured_version)
BEGIN SELECT RAISE(ABORT, 'the submitted result and its capture identity are immutable'); END;

CREATE TRIGGER repository_limits_no_delete
BEFORE DELETE ON repository_limits
WHEN (EXISTS(SELECT 1 FROM tasks t WHERE t.project=OLD.project))
  AND NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='repository_limits' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'the tasks of a Project require its repository limits'); END;

CREATE TRIGGER authorized_task_preserves_version BEFORE UPDATE ON tasks
WHEN EXISTS (SELECT 1 FROM merge_operations m WHERE m.task_id = OLD.id AND m.operation = 'apply' AND m.finished = 0)
 AND (NEW.submitted_version IS NOT OLD.submitted_version OR NEW.destination_branch <> OLD.destination_branch)
BEGIN SELECT RAISE(ABORT, 'the authorized update requires the recorded task version'); END;

CREATE TRIGGER tasks_decided_never_move BEFORE UPDATE OF status ON tasks
WHEN OLD.status <> NEW.status AND OLD.decision IS NOT NULL
 AND NOT (OLD.decision='approved' AND NEW.decision IS NULL AND OLD.archived=0)
BEGIN SELECT RAISE(ABORT, 'a decided task requires a new version before another stage'); END;

CREATE TRIGGER tasks_approve_requires_live_packet BEFORE UPDATE OF decision ON tasks
WHEN NEW.decision='approved'
 AND (NEW.current_review_id IS NULL OR NEW.review_slot_held=0)
BEGIN SELECT RAISE(ABORT, 'operator approval requires a current review'); END;

CREATE TRIGGER authorized_task_preserves_state BEFORE UPDATE ON tasks
WHEN EXISTS(SELECT 1 FROM merge_operations m WHERE m.task_id=OLD.id AND m.operation='apply' AND m.finished=0)
 AND (NEW.status<>OLD.status OR NEW.decision IS NOT OLD.decision OR NEW.archived<>OLD.archived
      OR NEW.description IS NOT OLD.description OR NEW.source_version IS NOT OLD.source_version)
BEGIN SELECT RAISE(ABORT, 'the task must preserve its authorization until the update result arrives'); END;

CREATE TABLE task_artifacts (
    task_id INTEGER NOT NULL REFERENCES tasks(id),
    path TEXT NOT NULL,
    expired_at TEXT,
    PRIMARY KEY (task_id,path)
);

CREATE TABLE artifact_retention (
    task_id INTEGER PRIMARY KEY REFERENCES tasks(id),
    expires_at TEXT,
    expired_at TEXT,
    token TEXT UNIQUE,
    CHECK (token IS NULL OR expires_at IS NOT NULL)
);

CREATE VIEW artifact_holds AS
WITH held(id) AS (
    SELECT id FROM tasks WHERE archived=0
    UNION SELECT subject FROM leases WHERE subject IS NOT NULL
    UNION SELECT subject FROM runs WHERE subject IS NOT NULL AND ended_at IS NULL
    UNION SELECT task_id FROM repository_runs WHERE cleaned=0 AND task_id IS NOT NULL
)
SELECT id AS task_id FROM held;

CREATE TRIGGER artifact_expiry_preserves_closed_tasks
BEFORE UPDATE OF archived ON tasks
WHEN NEW.archived=0 AND EXISTS (
    SELECT 1 FROM artifact_retention a JOIN tasks t ON t.id=a.task_id
    WHERE a.token IS NOT NULL AND t.project=NEW.project)
BEGIN
    SELECT RAISE(ABORT, 'artifact expiry is active; retry the task change');
END;

CREATE TRIGGER artifact_expiry_preserves_lease_insert
BEFORE INSERT ON leases
WHEN NEW.run_id IS NOT NULL AND EXISTS (
    SELECT 1 FROM artifact_retention a JOIN tasks t ON t.id=a.task_id
    WHERE a.token IS NOT NULL AND t.project=NEW.project)
BEGIN
    SELECT RAISE(ABORT, 'artifact expiry is active; retry the run');
END;

CREATE TRIGGER artifact_expiry_preserves_lease_update
BEFORE UPDATE ON leases
WHEN NEW.run_id IS NOT NULL AND EXISTS (
    SELECT 1 FROM artifact_retention a JOIN tasks t ON t.id=a.task_id
    WHERE a.token IS NOT NULL AND t.project=NEW.project)
BEGIN
    SELECT RAISE(ABORT, 'artifact expiry is active; retry the run');
END;

CREATE TRIGGER artifact_retention_reopen
AFTER UPDATE OF archived ON tasks
WHEN OLD.archived=1 AND NEW.archived=0
BEGIN
    UPDATE artifact_retention SET expires_at=NULL WHERE task_id=NEW.id;
END;

CREATE TRIGGER tasks_current_review_requires_version BEFORE UPDATE OF current_review_id ON tasks
WHEN NEW.current_review_id IS NOT OLD.current_review_id AND NEW.current_review_id IS NOT NULL
 AND (NEW.archived=1 OR NEW.decision IS NOT NULL OR NEW.status<>'packaged'
 OR NOT EXISTS(SELECT 1 FROM review_versions p WHERE p.id=NEW.current_review_id AND p.task_id=NEW.id
     AND p.version_id=NEW.submitted_version AND p.requirements_version=NEW.requirements_version))
BEGIN SELECT RAISE(ABORT, 'the current review requires the live packaged task version'); END;

CREATE TRIGGER tasks_review_capacity BEFORE UPDATE OF review_slot_held ON tasks
WHEN OLD.review_slot_held=0 AND NEW.review_slot_held=1
 AND (SELECT count(*) FROM tasks WHERE archived=0 AND review_slot_held=1 AND project=NEW.project AND id<>NEW.id)
    >=(SELECT review_wip_cap FROM settings WHERE id=1)
BEGIN SELECT RAISE(ABORT, 'the review queue reached its capacity'); END;

CREATE TRIGGER tasks_review_slot_requires_current BEFORE UPDATE OF review_slot_held ON tasks
WHEN OLD.review_slot_held=0 AND NEW.review_slot_held=1
 AND (NEW.current_review_id IS NULL OR NEW.archived=1
 OR NOT EXISTS(SELECT 1 FROM review_versions p WHERE p.id=NEW.current_review_id AND p.task_id=NEW.id
     AND p.requirements_version=NEW.requirements_version)
 OR EXISTS(SELECT 1 FROM requirements_revisions r WHERE r.task_id=NEW.id AND r.state='pending'))
BEGIN SELECT RAISE(ABORT, 'the review slot requires the current requirements'); END;

CREATE TRIGGER tasks_review_slot_release BEFORE UPDATE OF review_slot_held ON tasks
WHEN OLD.review_slot_held=1 AND NEW.review_slot_held=0 AND NEW.archived=0
 AND NOT EXISTS(SELECT 1 FROM requirements_revisions r WHERE r.task_id=NEW.id AND r.state='pending')
BEGIN SELECT RAISE(ABORT, 'the review slot remains held until archive or requirements revision'); END;

CREATE TRIGGER tasks_born_without_review BEFORE INSERT ON tasks
WHEN NEW.current_review_id IS NOT NULL OR NEW.review_slot_held<>0
BEGIN SELECT RAISE(ABORT, 'a new task starts without an operator review'); END;
