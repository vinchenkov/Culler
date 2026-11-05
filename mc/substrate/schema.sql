-- The spine stores Culler state. Constraints and triggers enforce
-- the current state transitions. Each connection enables foreign keys and
-- write-ahead logging. Each write transaction uses BEGIN IMMEDIATE.
-- Each trigger writes its dependent state without recursive triggers.

------------------------------------------------------------------------------
-- meta — spine identity. The singleton row is written by
-- first-ever provisioning (mc onboard), never seeded here.
------------------------------------------------------------------------------

CREATE TABLE meta (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    deployment_uuid TEXT    NOT NULL,
    schema_id       TEXT    NOT NULL,
    created_at      TEXT    NOT NULL DEFAULT (datetime('now'))
);

------------------------------------------------------------------------------
-- projects / sandbox_profiles — infrastructure records.
------------------------------------------------------------------------------

CREATE TABLE sandbox_profiles (
    id                    TEXT PRIMARY KEY,
    workspace_root        TEXT NOT NULL,
    artifact_roots        TEXT,                -- JSON array of paths
    denied_paths          TEXT,                -- JSON array of paths
    tool_home_dir         TEXT,
    runtime_control_dir   TEXT,
    harness_env_policy    TEXT,
    tool_env_policy       TEXT,
    tool_allowlist        TEXT,
    git_author_name       TEXT
                          CHECK (git_author_name IS NULL OR
                                 (typeof(git_author_name) = 'text' AND
                                  length(trim(git_author_name)) BETWEEN 1 AND 256 AND
                                  instr(git_author_name, char(0)) = 0 AND
                                  instr(git_author_name, char(10)) = 0 AND
                                  instr(git_author_name, char(13)) = 0)),
    git_author_email      TEXT
                          CHECK ((git_author_name IS NULL) = (git_author_email IS NULL) AND
                                 (git_author_email IS NULL OR
                                  (typeof(git_author_email) = 'text' AND
                                   length(trim(git_author_email)) BETWEEN 1 AND 256 AND
                                   instr(git_author_email, '@') > 0 AND
                                   instr(git_author_email, char(0)) = 0 AND
                                   instr(git_author_email, char(10)) = 0 AND
                                   instr(git_author_email, char(13)) = 0))),
    -- Agent containers receive projected access tokens.
    -- The host retains the real refresh tokens.
    runtime_auth_delivery TEXT NOT NULL DEFAULT 'projection'
                          CHECK (runtime_auth_delivery = 'projection')
);

CREATE TABLE projects (
    id              TEXT PRIMARY KEY,
    title           TEXT NOT NULL,
    jurisdiction    TEXT,
    -- A Project is a Git repository at the workspace root of its profile.
    sandbox_profile TEXT NOT NULL REFERENCES sandbox_profiles(id),
    status          TEXT NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'paused', 'archived')),
    setup_status    TEXT NOT NULL DEFAULT 'ready'
                    CHECK (setup_status IN ('ready', 'vision', 'configured')),
    -- VISION.md is the Project's North Star file: the Strategist and
    -- Arbiter seek, propose, and approve new work from it. The onboarding
    -- project section records what the host found at the workspace root:
    -- 'present' (the file exists), 'declined' (the operator chose to register
    -- without one), or 'unknown' (registered before the section ever looked,
    -- or through mc project add). mc doctor and the dashboard report every
    -- live repository Project whose vision is not 'present'.
    infrastructure_failure_count INTEGER NOT NULL DEFAULT 0 CHECK (infrastructure_failure_count BETWEEN 0 AND 3),
    infrastructure_next_retry_at TEXT,
    infrastructure_last_failed_run_id TEXT REFERENCES runs(id),
    infrastructure_condition TEXT NOT NULL DEFAULT '',
    vision          TEXT NOT NULL DEFAULT 'unknown'
                    CHECK (vision IN ('present', 'declined', 'unknown')),
    -- A connection check stopped the latest spawn of the Project.
    -- connection_wait names the connection. connection_wait_task names the
    -- task of that spawn. Both are NULL when no connection check stops it.
    connection_wait      TEXT,
    connection_wait_task INTEGER REFERENCES tasks(id),
    CHECK (connection_wait IS NOT NULL OR connection_wait_task IS NULL)
);

-- The Project deletion transaction records its exact rows before it deletes them.
-- Foreign keys require the transaction to clear this scope before commit.
CREATE TABLE project_deletion_records (
    project TEXT NOT NULL REFERENCES projects(id),
    table_name TEXT NOT NULL,
    row_id INTEGER NOT NULL,
    PRIMARY KEY (table_name, row_id)
);

CREATE TRIGGER projects_setup_pause_insert
BEFORE INSERT ON projects
WHEN NEW.setup_status <> 'ready' AND NEW.status = 'active'
BEGIN
    SELECT RAISE(ABORT, 'Project setup requires a pause');
END;

CREATE TRIGGER projects_setup_pause_update
BEFORE UPDATE OF status, setup_status ON projects
WHEN NEW.setup_status <> 'ready' AND NEW.status = 'active'
BEGIN
    SELECT RAISE(ABORT, 'Project setup requires a pause');
END;

CREATE TABLE vision_drafts (
    project_id TEXT PRIMARY KEY REFERENCES projects(id),
    content TEXT NOT NULL,
    revision TEXT NOT NULL,
    workspace_identity TEXT NOT NULL,
    workspace_revision TEXT NOT NULL,
    workspace_has_files INTEGER NOT NULL DEFAULT 0 CHECK (workspace_has_files IN (0, 1)),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE vision_submissions (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES vision_drafts(project_id),
    content TEXT NOT NULL,
    revision TEXT NOT NULL,
    workspace_identity TEXT NOT NULL,
    workspace_revision TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'saved', 'failed')),
    error TEXT NOT NULL DEFAULT '',
    saved_revision TEXT,
    attempted INTEGER NOT NULL DEFAULT 0 CHECK (attempted IN (0, 1)),
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (project_id, revision, workspace_revision),
    CHECK ((status = 'saved') = (saved_revision IS NOT NULL))
);

CREATE UNIQUE INDEX vision_one_pending ON vision_submissions(project_id) WHERE status = 'pending';

CREATE TRIGGER vision_submission_identity
BEFORE UPDATE OF id,project_id,content,revision,workspace_identity,workspace_revision,created_at ON vision_submissions
WHEN NEW.id IS NOT OLD.id OR NEW.project_id IS NOT OLD.project_id OR NEW.content IS NOT OLD.content
  OR NEW.revision IS NOT OLD.revision OR NEW.workspace_identity IS NOT OLD.workspace_identity
  OR NEW.workspace_revision IS NOT OLD.workspace_revision OR NEW.created_at IS NOT OLD.created_at
BEGIN
    SELECT RAISE(ABORT, 'The Vision submission is immutable');
END;

CREATE TRIGGER projects_archived_terminal
BEFORE UPDATE OF status ON projects
WHEN OLD.status = 'archived' AND NEW.status <> 'archived'
BEGIN
    SELECT RAISE(ABORT, 'archived Project status is terminal');
END;

CREATE TRIGGER projects_no_delete
BEFORE DELETE ON projects
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='projects' AND row_id=OLD.rowid)
BEGIN
    SELECT RAISE(ABORT, 'Project deletion requires its explicit operation');
END;

------------------------------------------------------------------------------
-- tasks stores proposals and accepted work.
------------------------------------------------------------------------------

CREATE TABLE tasks (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    title            TEXT    NOT NULL,
    description      TEXT,
    record_version   INTEGER NOT NULL DEFAULT 1 CHECK (record_version >= 1),
    requirements_version INTEGER NOT NULL DEFAULT 1 CHECK (requirements_version >= 1),
    scope            TEXT    NOT NULL DEFAULT 'task'
                     CHECK (scope = 'task'),
    priority         INTEGER NOT NULL DEFAULT 2
                     CHECK (priority BETWEEN -1 AND 3),  -- -1 = expedited, 0-3 = P0-P3
    created_at       TEXT    NOT NULL DEFAULT (datetime('now')),
    status           TEXT    NOT NULL DEFAULT 'proposed'
                     CHECK (status IN ('proposed', 'seeded', 'worked', 'verified', 'packaged')),
    stage_entered_at TEXT    NOT NULL DEFAULT (datetime('now')),
    correction_count INTEGER NOT NULL DEFAULT 0
                     CHECK (correction_count BETWEEN 0 AND 3),
    verifier_correction_open INTEGER NOT NULL DEFAULT 0 CHECK (verifier_correction_open IN (0,1)),
    allowance_condition TEXT CHECK (allowance_condition = 'verifier'),
    allowance_reason TEXT,
    allowance_version INTEGER REFERENCES task_versions(id),
    correction_operator_reason TEXT,
    returned_kind TEXT CHECK (returned_kind IN ('verifier','merge','operator')),
    blocked          INTEGER NOT NULL DEFAULT 0 CHECK (blocked IN (0, 1)),
    blocked_reason   TEXT,
    -- blocked_kind is the condition type of Decision Pending. It selects the
    -- resolution controls.
    blocked_kind     TEXT CHECK (blocked_kind IN
                     ('report','customization','allowance','confinement','checkout','operator','proposal')),
    dispatch_retries INTEGER NOT NULL DEFAULT 3 CHECK (dispatch_retries >= 0),
    run_retry_at TEXT,

    decision         TEXT    CHECK (decision IN ('approved', 'rejected', 'cancelled')),
    decided_at       TEXT,
    archived         INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1)),
    origin           TEXT    NOT NULL DEFAULT 'autonomous'
                     CHECK (origin IN ('user', 'autonomous')),
    project       TEXT    NOT NULL REFERENCES projects(id),

    completion_sha   TEXT,

    -- The next run receives the operator revision or conflict correction.
    -- A new packet clears these notes.
    revision_notes   TEXT,

    destination_branch TEXT GENERATED ALWAYS AS ('main') STORED,
    -- base_commit is the start commit of the first Worker run. input_commit
    -- is the start commit of the next Worker run without a checkpoint or a
    -- submitted version.
    base_commit TEXT,
    input_commit TEXT,
    submitted_version INTEGER REFERENCES task_versions(id),
    source_version INTEGER REFERENCES task_versions(id),
    checkpoint_version INTEGER REFERENCES task_versions(id),
    candidate_id INTEGER REFERENCES merge_candidates(id),
    report_path TEXT,
    report_sha256 TEXT,
    thesis TEXT,
    correction_open INTEGER NOT NULL DEFAULT 0 CHECK (correction_open IN (0, 1)),
    correction_reason TEXT,
    merge_after TEXT,
    observed_destination TEXT,
    observed_at TEXT,
    current_review_id INTEGER REFERENCES review_versions(id),
    review_slot_held INTEGER NOT NULL DEFAULT 0 CHECK (review_slot_held IN (0,1)),

    -- stage_rank: how far into the pipeline the row is, for furthest-first
    -- dispatch. packaged ranks 0 — in the review queue, out of
    -- pipeline dispatch. Generated: unwritable by construction.
    stage_rank       INTEGER GENERATED ALWAYS AS (
                         CASE status
                             WHEN 'packaged' THEN 0
                             WHEN 'proposed' THEN 1
                             WHEN 'seeded'   THEN 2
                             WHEN 'worked'   THEN 3
                             WHEN 'verified' THEN 4
                         END
                     ) STORED,

    stalled INTEGER GENERATED ALWAYS AS (dispatch_retries = 0) VIRTUAL
            CHECK (dispatch_retries BETWEEN 0 AND 3),
    infrastructure_failure_count INTEGER GENERATED ALWAYS AS (3 - dispatch_retries) VIRTUAL,

    CHECK (review_slot_held=0 OR current_review_id IS NOT NULL),
    CHECK (blocked_kind IS NOT 'allowance' OR (allowance_condition IS NOT NULL AND blocked=1)),
    CHECK ((allowance_condition IS NULL) = (allowance_reason IS NULL)),
    CHECK ((allowance_condition IS NULL) = (allowance_version IS NULL)),

    -- Blocked requires a reason and a condition type.
    CHECK (blocked = 0 OR blocked_reason IS NOT NULL),
    CHECK (blocked = 0 OR blocked_kind IS NOT NULL),
    -- No archive without a decision.
    CHECK (archived = 0 OR decision IS NOT NULL),
    -- Decision and timestamp travel together.
    CHECK ((decision IS NULL) = (decided_at IS NULL)),
    -- Approval only of packaged work.
    CHECK (decision <> 'approved' OR status = 'packaged')
);

CREATE TRIGGER tasks_forward_progress_restores_infrastructure_allowance
AFTER UPDATE OF status ON tasks
WHEN (CASE NEW.status
          WHEN 'proposed' THEN 0 WHEN 'seeded' THEN 1 WHEN 'worked' THEN 2
          WHEN 'verified' THEN 3 WHEN 'packaged' THEN 4 END)
   > (CASE OLD.status
          WHEN 'proposed' THEN 0 WHEN 'seeded' THEN 1 WHEN 'worked' THEN 2
          WHEN 'verified' THEN 3 WHEN 'packaged' THEN 4 END)
BEGIN
    UPDATE tasks SET dispatch_retries = 3,run_retry_at=NULL WHERE id = NEW.id;
END;

CREATE INDEX tasks_dispatch      ON tasks (archived, blocked, stage_rank, priority, created_at, id);
CREATE INDEX tasks_reject_ledger ON tasks (decided_at DESC) WHERE decision = 'rejected';

-- Operator work can start seeded. Agent proposals start proposed.
-- New records remain undecided and unarchived.
CREATE TRIGGER tasks_birth_rules
BEFORE INSERT ON tasks
BEGIN
    SELECT CASE
        WHEN NEW.status <> 'proposed'
             AND NOT (NEW.origin = 'user' AND NEW.status = 'seeded')
            THEN RAISE(ABORT, 'operator work starts seeded; agent proposals start proposed')
        WHEN NEW.decision IS NOT NULL OR NEW.archived <> 0
            THEN RAISE(ABORT, 'tasks are born undecided and unarchived')
    END;
END;

-- The state machine permits only the legal task transitions.
-- Self-assignment (status unchanged) is a no-op, not a transition.
CREATE TRIGGER tasks_legal_transitions
BEFORE UPDATE OF status ON tasks
WHEN OLD.status <> NEW.status
  AND NOT (   (OLD.status = 'proposed' AND NEW.status = 'seeded')    -- promote
           OR (OLD.status = 'seeded'   AND NEW.status = 'worked')    -- work
           OR (OLD.status = 'worked'   AND NEW.status = 'verified')  -- verify pass
           OR (OLD.status = 'worked'   AND NEW.status = 'seeded')    -- correction rally
           OR (OLD.status = 'verified' AND NEW.status = 'seeded'
               AND NEW.requirements_version = OLD.requirements_version + 1
               AND EXISTS (SELECT 1 FROM requirements_revisions r WHERE r.task_id=OLD.id AND r.state='pending'))
           OR (OLD.status = 'verified' AND NEW.status = 'packaged')  -- package
           OR (OLD.status = 'packaged' AND NEW.status = 'seeded'))   -- revision / operator revise
BEGIN
    SELECT RAISE(ABORT, 'illegal status transition');
END;

-- Blocking removes a proposal from Arbiter jurisdiction until the operator
-- unblocks it. A stale pool snapshot cannot promote or reject it.
CREATE TRIGGER blocked_proposals_cannot_promote
BEFORE UPDATE OF status ON tasks
WHEN OLD.status = 'proposed' AND OLD.blocked = 1 AND NEW.status = 'seeded'
BEGIN
    SELECT RAISE(ABORT, 'a blocked proposal cannot be promoted');
END;

CREATE TRIGGER blocked_proposals_cannot_reject
BEFORE UPDATE OF decision ON tasks
WHEN OLD.status = 'proposed' AND OLD.blocked = 1 AND NEW.decision = 'rejected'
BEGIN
    SELECT RAISE(ABORT, 'a blocked proposal cannot be rejected by Arbiter');
END;

-- Archived rows are terminal: no status movement ever again.
CREATE TRIGGER tasks_archived_are_terminal
BEFORE UPDATE OF status ON tasks
WHEN OLD.status <> NEW.status AND OLD.archived = 1
BEGIN
    SELECT RAISE(ABORT, 'archived rows are terminal: no status transitions');
END;

-- An archived task preserves its final state.
CREATE TRIGGER tasks_no_unarchive
BEFORE UPDATE OF archived ON tasks
WHEN OLD.archived = 1 AND NEW.archived = 0
BEGIN
    SELECT RAISE(ABORT, 'archived tasks are never un-archived');
END;

-- Terminal bookkeeping is itself terminal: the decision on an archived row is
-- the leverage-ledger record and never rewrites (same provenance class as
-- tasks_identity_immutable).
CREATE TRIGGER tasks_archived_decision_frozen
BEFORE UPDATE OF decision, decided_at ON tasks
WHEN OLD.archived = 1
  AND (NEW.decision IS NOT OLD.decision OR NEW.decided_at IS NOT OLD.decided_at)
BEGIN
    SELECT RAISE(ABORT, 'an archived row''s decision record is frozen');
END;

-- The task identity stays fixed after creation.
CREATE TRIGGER tasks_identity_immutable
BEFORE UPDATE ON tasks
WHEN NEW.id <> OLD.id
  OR NEW.scope <> OLD.scope
  OR NEW.origin <> OLD.origin
  OR NEW.project <> OLD.project
  OR NEW.created_at <> OLD.created_at
BEGIN
    SELECT RAISE(ABORT, 'task identity columns are immutable');
END;

-- stage_entered_at follows the transition, stamped with the lock
-- domain's own clock.
CREATE TRIGGER tasks_stamp_stage_entered
AFTER UPDATE OF status ON tasks
WHEN OLD.status <> NEW.status
BEGIN
    UPDATE tasks SET stage_entered_at = datetime('now') WHERE id = NEW.id;
END;

-- Unblock clears the reason and the condition type (unblocking resumes
-- exactly where it stopped; a stale question must not linger on the row).
CREATE TRIGGER tasks_unblock_clears_reason
AFTER UPDATE OF blocked ON tasks
WHEN OLD.blocked = 1 AND NEW.blocked = 0
  AND (NEW.blocked_reason IS NOT NULL OR NEW.blocked_kind IS NOT NULL)
BEGIN
    UPDATE tasks SET blocked_reason = NULL, blocked_kind = NULL WHERE id = NEW.id;
END;

-- Each requirements revision preserves the base commit of the task.
CREATE TRIGGER tasks_base_commit_immutable
BEFORE UPDATE OF base_commit ON tasks
WHEN OLD.base_commit IS NOT NULL AND NEW.base_commit IS NOT OLD.base_commit
BEGIN
    SELECT RAISE(ABORT, 'the base commit of a task is immutable');
END;

-- Archiving any task archives its packet: the packet's life ends with the
-- operator decision that archives the row.

-- Task history remains until explicit Project deletion.
CREATE TRIGGER tasks_no_delete
BEFORE DELETE ON tasks
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='tasks' AND row_id=OLD.rowid)
BEGIN
    SELECT RAISE(ABORT, 'These records require explicit Project deletion');
END;

------------------------------------------------------------------------------
-- settings — the one deployment-wide settings row. The lease tunables, the review WIP cap, the Homie idle timeout,
-- and the wiki update interval live here and nowhere else; `mc init` writes
-- the row's values, and the schema seeds the defaults so every fresh spine
-- carries exactly one row. It holds no lease state.
------------------------------------------------------------------------------

CREATE TABLE settings (
    id                   INTEGER PRIMARY KEY CHECK (id = 1),
    discord_available    INTEGER NOT NULL DEFAULT 0 CHECK (discord_available IN (0, 1)),
    discord_connection   TEXT NOT NULL DEFAULT '',
    timeout_minutes      INTEGER NOT NULL DEFAULT 60,
    grace_minutes        INTEGER NOT NULL DEFAULT 15,
    heartbeat_interval_s INTEGER NOT NULL DEFAULT 60,
    spawn_grace_s        INTEGER NOT NULL DEFAULT 60,
    -- hard_deadline_minutes: the awake time that a run can hold its lease.
    -- The claim stamps the deadline. A later change to the
    -- setting does not move the deadline of a held lease.
    hard_deadline_minutes INTEGER NOT NULL DEFAULT 240,
    -- review_wip_cap is the operator's single daily review-load knob. One
    -- number; it applies to each Project on its own — the packet-birth
    -- trigger counts per Project against this value.
    review_wip_cap        INTEGER NOT NULL DEFAULT 3
                          CHECK (review_wip_cap > 0),
    -- homie_idle_timeout_s: how long after a Homie session's last_activity_at
    -- the wake selector ends it and the launch-bind/runner-started non-idle
    -- CAS refuses (the session lives as long as the conversation).
    homie_idle_timeout_s  INTEGER NOT NULL DEFAULT 1800
                          CHECK (homie_idle_timeout_s > 0),
    -- wiki_update_interval_s: the minimum time between the starts of two
    -- wiki updates of one Project.
    wiki_update_interval_s INTEGER NOT NULL DEFAULT 3600
                          CHECK (wiki_update_interval_s > 0)
);

INSERT INTO settings (id) VALUES (1);

CREATE TRIGGER settings_singleton_no_delete
BEFORE DELETE ON settings
BEGIN
    SELECT RAISE(ABORT, 'the settings row is never deleted');
END;

------------------------------------------------------------------------------
-- leases — one Project lease per Project. Only an agent run holds it.
-- The primary key is the Project id; the row is inserted with the
-- Project record (the projects_insert_lease trigger below) and never
-- deleted. CAS-only; free when run_id IS NULL. A lease row holds no setting.
------------------------------------------------------------------------------

CREATE TABLE leases (
    project           TEXT PRIMARY KEY REFERENCES projects(id),
    run_id               TEXT,
    subject              INTEGER REFERENCES tasks(id),  -- nullable: propose runs
    owner                TEXT CHECK (owner IN
                         ('strategist', 'arbiter', 'worker', 'verifier', 'packager')),
    acquired_at          TEXT,
    last_heartbeat_at    TEXT,
    -- The reap limits count VM uptime, which stops while the host sleeps.
    -- The boot identifier shows a VM restart after the claim.
    boot_id              TEXT,
    acquired_uptime_ms   INTEGER,
    heartbeat_uptime_ms  INTEGER,
    deadline_uptime_ms   INTEGER,   -- non-renewable; no heartbeat can extend it

    -- Claim fields travel together; a free lease carries no run residue.
    CHECK ((run_id IS NULL) = (owner IS NULL)),
    CHECK ((run_id IS NULL) = (acquired_at IS NULL)),
    CHECK ((run_id IS NULL) = (boot_id IS NULL)),
    CHECK ((run_id IS NULL) = (acquired_uptime_ms IS NULL)),
    CHECK ((run_id IS NULL) = (deadline_uptime_ms IS NULL)),
    CHECK ((last_heartbeat_at IS NULL) = (heartbeat_uptime_ms IS NULL)),
    CHECK (run_id IS NOT NULL OR subject IS NULL),
    CHECK (run_id IS NOT NULL OR last_heartbeat_at IS NULL)
);

-- A run holds at most one lease: the lease of its own Project.
CREATE UNIQUE INDEX leases_run_id ON leases (run_id) WHERE run_id IS NOT NULL;

CREATE TRIGGER projects_insert_lease
AFTER INSERT ON projects
BEGIN
    INSERT INTO leases (project) VALUES (NEW.id);
END;

CREATE TRIGGER leases_no_delete
BEFORE DELETE ON leases
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='leases' AND row_id=OLD.rowid)
BEGIN
    SELECT RAISE(ABORT, 'These records require explicit Project deletion');
END;

------------------------------------------------------------------------------
-- runs — one row per dispatched run, committed before the process starts
--; carries the four session-folder locators.
------------------------------------------------------------------------------

CREATE TABLE runs (
    id                 TEXT PRIMARY KEY,
    tier               TEXT NOT NULL CHECK (tier IN ('pipeline', 'homie')),
    role               TEXT CHECK (role IN
                       ('strategist', 'arbiter', 'worker', 'verifier', 'scribe', 'packager', 'homie')),
    project         TEXT REFERENCES projects(id),
    subject            INTEGER REFERENCES tasks(id),
    created_at         TEXT NOT NULL DEFAULT (datetime('now')),
    -- created_at has one-second precision and id is random. This sequence is
    -- the durable causal tie-break for every run-recency read.
    created_seq        INTEGER NOT NULL DEFAULT 0
                       CHECK (typeof(created_seq) = 'integer' AND created_seq >= 0),
    ended_at           TEXT,
    outcome            TEXT,
    -- Why a failed run ended. A completed or blocked run has no end reason.
    end_reason         TEXT CHECK (end_reason IN ('agent', 'harness', 'launch', 'repository',
                       'network', 'provider', 'connection', 'cancelled', 'interrupted',
                       'spawn-watchdog', 'lease-timeout', 'hard-deadline', 'container-gone', 'vm-restart')),
    connection_id      TEXT NOT NULL DEFAULT '',
    authorization_id   TEXT NOT NULL DEFAULT '',
    connection_revision INTEGER NOT NULL DEFAULT 0,
    profile_id         TEXT NOT NULL DEFAULT '',
    profile_revision   TEXT NOT NULL DEFAULT '',
    session_path       TEXT,   -- where the session folder is
    binding            TEXT,   -- immutable "harness/binding" locator for the runtime that wrote it
    native_session_ref TEXT,   -- the harness's own session handle
    trace_filename     TEXT,   -- the trace's filename
    agent_started_at TEXT,
    pool_snapshot      TEXT,   -- Arbiter runs: JSON array of the proposed pool
                               -- snapshotted at claim; `mc arbiter decide` must
                               -- cover it exactly

    -- The verdict record lives on the Verifier's own runs row; the next
    -- Worker brief's correction file is queried from the subject's latest
    -- correct-verdict run.
    verdict_outcome    TEXT CHECK (verdict_outcome IN ('pass', 'correct')),
    evidence_path      TEXT,
    correction_path    TEXT,
	output_path        TEXT,   -- terminal report/artifact reference from mc complete
	CHECK ((native_session_ref IS NULL) = (trace_filename IS NULL))
);

CREATE TABLE infrastructure_failures (
    run_id TEXT PRIMARY KEY REFERENCES runs(id),
    task_id INTEGER REFERENCES tasks(id),
    allowance TEXT NOT NULL CHECK(allowance IN ('task','project')),
    condition TEXT NOT NULL CHECK (typeof(condition) = 'text' AND length(trim(condition)) BETWEEN 1 AND 2048),
    source TEXT NOT NULL CHECK (source IN ('agent', 'resident', 'runner')),
    cause TEXT NOT NULL DEFAULT 'unknown' CHECK (cause IN ('unknown', 'credentials', 'quota', 'connection')),
    evidence TEXT NOT NULL DEFAULT '' CHECK (typeof(evidence) = 'text' AND length(evidence) <= 8192),
    operator_action TEXT NOT NULL DEFAULT '' CHECK (typeof(operator_action) = 'text' AND length(operator_action) <= 2048),
    quota_reset_at TEXT NOT NULL DEFAULT '',
    failed_at TEXT NOT NULL DEFAULT (datetime('now')),
    CHECK (cause = 'unknown' OR length(trim(evidence)) > 0),
    CHECK (operator_action = '' OR (cause <> 'unknown' AND length(trim(evidence)) > 0)),
    CHECK (quota_reset_at = '' OR (cause = 'quota' AND length(trim(evidence)) > 0
           AND julianday(quota_reset_at) IS NOT NULL))
);

CREATE INDEX infrastructure_failures_task ON infrastructure_failures(task_id, failed_at);

-- A failure of a wiki update uses no failure allowance and has no failure row.
CREATE TRIGGER infrastructure_failures_assigned_run
BEFORE INSERT ON infrastructure_failures
WHEN NOT EXISTS (
    SELECT 1 FROM runs r JOIN leases l ON l.run_id = r.id
    WHERE r.id = NEW.run_id AND r.subject IS NEW.task_id AND l.subject IS NEW.task_id
      AND r.ended_at IS NULL AND r.outcome IS NULL
) AND NOT EXISTS (
    SELECT 1 FROM runs r JOIN repository_executions x ON x.run_id = r.id
    WHERE r.id = NEW.run_id AND x.task_id IS NEW.task_id AND x.wiki_update IS NULL AND x.ended_at IS NULL
      AND r.ended_at IS NULL AND r.outcome IS NULL
)
BEGIN
    SELECT RAISE(ABORT, 'an infrastructure failure requires its assigned active run');
END;

CREATE TRIGGER infrastructure_failures_immutable
BEFORE UPDATE ON infrastructure_failures
BEGIN
    SELECT RAISE(ABORT, 'infrastructure failure records are immutable');
END;

CREATE TRIGGER infrastructure_failures_no_delete
BEFORE DELETE ON infrastructure_failures
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='infrastructure_failures' AND row_id=OLD.rowid)
BEGIN
    SELECT RAISE(ABORT, 'infrastructure failure records are retained');
END;

CREATE UNIQUE INDEX runs_created_seq_unique ON runs(created_seq);

CREATE TRIGGER runs_created_seq_input
BEFORE INSERT ON runs
WHEN NEW.created_seq <> 0
BEGIN
    SELECT RAISE(ABORT, 'runs.created_seq is generated by the spine');
END;

CREATE TRIGGER runs_created_seq_assign
AFTER INSERT ON runs
BEGIN
    UPDATE runs
    SET created_seq = (
        SELECT COALESCE(MAX(r.created_seq), 0) + 1
        FROM runs r WHERE r.id <> NEW.id
    )
    WHERE id = NEW.id;
END;

CREATE TRIGGER runs_created_seq_immutable
BEFORE UPDATE OF created_seq ON runs
WHEN NOT (OLD.created_seq = 0 AND NEW.created_seq > 0)
BEGIN
    SELECT RAISE(ABORT, 'runs.created_seq is immutable');
END;

CREATE TRIGGER runs_scribe_has_no_task
BEFORE INSERT ON runs
WHEN NEW.role = 'scribe' AND NEW.subject IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'a Scribe run has no task');
END;

CREATE TRIGGER runs_scribe_keeps_no_task
BEFORE UPDATE OF role, subject ON runs
WHEN NEW.role = 'scribe' AND NEW.subject IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'a Scribe run has no task');
END;

-- Run records remain until explicit Project deletion.
CREATE TRIGGER runs_no_delete
BEFORE DELETE ON runs
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='runs' AND row_id=OLD.rowid)
BEGIN
    SELECT RAISE(ABORT, 'These records require explicit Project deletion');
END;

CREATE TRIGGER runs_session_locators_immutable
BEFORE UPDATE OF native_session_ref, trace_filename ON runs
WHEN OLD.native_session_ref IS NOT NULL
  AND (NEW.native_session_ref IS NOT OLD.native_session_ref
       OR NEW.trace_filename IS NOT OLD.trace_filename)
BEGIN
    SELECT RAISE(ABORT, 'native session locators are immutable once registered');
END;

------------------------------------------------------------------------------
-- activity — the append-only log. actor is the logical originator;
-- the physical writer is always mc.
------------------------------------------------------------------------------

CREATE TABLE activity (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    actor               TEXT NOT NULL,
    kind                TEXT NOT NULL,   -- e.g. 'daily.briefing'
    subject             TEXT,
    detail              TEXT,
    -- Replay fences. Both length() and GLOB stop at the first NUL
    -- byte, so each hex shape is pinned by character length AND byte length:
    -- 64 hex + NUL + junk passes a character test (65+ bytes stored), and
    -- 63 hex + NUL passes a byte test (GLOB never sees the NUL). Requiring the
    -- two lengths to agree admits only NUL-free ASCII, which GLOB then reads
    -- whole. Without this a forged key stores as a distinct UNIQUE value that
    -- its own receipt lookup cannot find, and the replay fence fails open.
    --
    -- These fences hold only for TEXT: a BLOB bypasses affinity conversion,
    -- length(blob) counts bytes, and GLOB reads it only to the first NUL.
    -- The activity_receipt_keys_are_text trigger requires TEXT values.
    dispatch_key        TEXT
                        CHECK (dispatch_key IS NULL OR
                               (length(dispatch_key) = 64 AND
                                length(CAST(dispatch_key AS BLOB)) = 64 AND
                                dispatch_key NOT GLOB '*[^0-9a-f]*')),
    dispatch_request_id TEXT
                        CHECK (dispatch_request_id IS NULL OR
                               (length(dispatch_request_id) = 16 AND
                                length(CAST(dispatch_request_id AS BLOB)) = 16 AND
                                dispatch_request_id NOT GLOB '*[^0-9a-f]*')),
    dispatch_result     TEXT
                        CHECK (dispatch_result IS NULL OR
                               (length(CAST(dispatch_result AS BLOB)) <= 65536 AND
                                json_valid(dispatch_result) AND
                                json_type(dispatch_result) = 'object')),
    -- project identifies the Project for the event.
    -- Deployment events use NULL.
    project          TEXT REFERENCES projects(id),
    CHECK ((dispatch_request_id IS NULL) = (dispatch_result IS NULL))
);

CREATE INDEX activity_by_kind ON activity (kind, created_at);

-- Each dispatch key identifies one activity record.
CREATE UNIQUE INDEX activity_dispatch_key ON activity (dispatch_key);
CREATE UNIQUE INDEX activity_dispatch_request_id ON activity (dispatch_request_id);

CREATE TRIGGER activity_append_only_update
BEFORE UPDATE ON activity
BEGIN
    SELECT RAISE(ABORT, 'activity is append-only');
END;

CREATE TRIGGER activity_append_only_delete
BEFORE DELETE ON activity
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='activity' AND row_id=OLD.rowid)
BEGIN
    SELECT RAISE(ABORT, 'activity is append-only');
END;

-- The hex CHECKs hold
-- only for TEXT, so a BLOB forgery would store as a distinct UNIQUE value
-- whose own receipt lookup can never find it. Activity is append-only, so
-- INSERT is the only write this needs to cover.
CREATE TRIGGER activity_receipt_keys_are_text
BEFORE INSERT ON activity
WHEN (NEW.dispatch_key IS NOT NULL AND typeof(NEW.dispatch_key) != 'text')
  OR (NEW.dispatch_request_id IS NOT NULL AND typeof(NEW.dispatch_request_id) != 'text')
  OR (NEW.dispatch_result IS NOT NULL AND typeof(NEW.dispatch_result) != 'text')
BEGIN
    SELECT RAISE(ABORT, 'dispatch receipt columns must be TEXT; a BLOB bypasses the hex fences');
END;

------------------------------------------------------------------------------
-- Homie tier: session registry, bindings, conversation rows.
------------------------------------------------------------------------------

CREATE TABLE homie_sessions (
    id                 TEXT PRIMARY KEY,   -- also the session's run_id
    vision_project_id  TEXT REFERENCES projects(id),
    packet_id          INTEGER REFERENCES review_versions(id)
                       CHECK (packet_id IS NULL OR typeof(packet_id) = 'integer'),
    deleted_at         TEXT CHECK (deleted_at IS NULL OR (status <> 'active' AND vision_project_id IS NULL)),
    status             TEXT NOT NULL DEFAULT 'active'
                       CHECK (status IN ('active', 'ended', 'reaped')),
    created_at         TEXT NOT NULL DEFAULT (datetime('now')),
    last_activity_at   TEXT NOT NULL DEFAULT (datetime('now')),
    container_name     TEXT NOT NULL,
    verb_allowlist     TEXT NOT NULL    -- frozen at mc homie start
                       CHECK (json_valid(verb_allowlist)
                              AND json_type(verb_allowlist) = 'array'),
    session_path       TEXT NOT NULL,   -- the four locators
    binding            TEXT NOT NULL,
    effort             TEXT NOT NULL DEFAULT ''
                       CHECK (effort IN ('', 'none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra')),
    native_session_ref TEXT,
    trace_filename     TEXT,

    -- Launch fencing: canonical liveness only — no argv, mount,
    -- credential, nonce, or launch plan is persisted. The pairing lattice
    -- lives here as CHECKs, not as Go politeness: a half-bound launch, a
    -- debt-plus-launch row, or a prime cutoff outside `rows` mode is
    -- unstorable, so a superseded generation can never be half-cleared.
    -- The id fences extend the dual-length shape (see activity above)
    -- with a typeof pin: a BLOB bypasses TEXT affinity entirely, length()
    -- counts its bytes (NULs included), and GLOB reads it converted, only to
    -- the first NUL — so without typeof a NUL-embedded 16-byte BLOB forgery
    -- passes every other conjunct. With typeof(text), the dual length test
    -- admits only NUL-free ASCII, which GLOB then reads whole.
    -- The integer fences carry the same pin: INTEGER affinity stores
    -- non-numeric TEXT and any BLOB as-is, and SQLite's cross-type ordering
    -- ranks both above every number, so 'abc' >= 0 is TRUE.
    -- The prime pair is also coherence-checked: the fence closes the empty prefix
    -- to the single encoding (0,0) — zero is a below-everything sentinel,
    -- never a row identity — so a zero cutoff claiming rows, or a positive
    -- cutoff claiming none, is outside the closed grammar.
    current_launch_id         TEXT
                              CHECK (current_launch_id IS NULL OR
                                     (typeof(current_launch_id) = 'text' AND
                                      length(current_launch_id) = 16 AND
                                      length(CAST(current_launch_id AS BLOB)) = 16 AND
                                      current_launch_id NOT GLOB '*[^0-9a-f]*')),
    current_launch_mode       TEXT
                              CHECK (current_launch_mode IS NULL OR
                                     current_launch_mode IN ('fresh', 'native', 'rows'))
                              CHECK ((current_launch_id IS NULL) = (current_launch_mode IS NULL)),
    current_prime_through_seq INTEGER
                              CHECK (current_prime_through_seq IS NULL OR
                                     (typeof(current_prime_through_seq) = 'integer' AND
                                      current_prime_through_seq >= 0))
                              CHECK ((current_launch_mode IS 'rows') =
                                     (current_prime_through_seq IS NOT NULL)),
    current_prime_row_count   INTEGER
                              CHECK (current_prime_row_count IS NULL OR
                                     (typeof(current_prime_row_count) = 'integer' AND
                                      current_prime_row_count >= 0))
                              CHECK ((current_prime_through_seq IS NULL) =
                                     (current_prime_row_count IS NULL))
                              CHECK (current_prime_row_count IS NULL OR
                                     ((current_prime_through_seq = 0) =
                                      (current_prime_row_count = 0))),
    current_container_id      TEXT
                              CHECK (current_container_id IS NULL OR
                                     (typeof(current_container_id) = 'text' AND
                                      length(current_container_id) = 64 AND
                                      length(CAST(current_container_id AS BLOB)) = 64 AND
                                      current_container_id NOT GLOB '*[^0-9a-f]*')),
    launch_bound_at           TEXT
                              CHECK ((current_container_id IS NULL) = (launch_bound_at IS NULL))
                              CHECK (launch_bound_at IS NULL OR current_launch_id IS NOT NULL),
    launch_started_at         TEXT
                              CHECK (launch_started_at IS NULL OR launch_bound_at IS NOT NULL),
    resume_owed               INTEGER NOT NULL DEFAULT 0
                              CHECK (resume_owed IN (0, 1))
                              CHECK (resume_owed = 0 OR current_launch_id IS NULL),
    resume_mode               TEXT
                              CHECK (resume_mode IS NULL OR resume_mode IN ('native', 'rows'))
                              CHECK ((resume_owed = 1) = (resume_mode IS NOT NULL)),
    resume_prime_through_seq  INTEGER
                              CHECK (resume_prime_through_seq IS NULL OR
                                     (typeof(resume_prime_through_seq) = 'integer' AND
                                      resume_prime_through_seq >= 0))
                              CHECK ((resume_mode IS 'rows') =
                                     (resume_prime_through_seq IS NOT NULL)),
    resume_prime_row_count    INTEGER
                              CHECK (resume_prime_row_count IS NULL OR
                                     (typeof(resume_prime_row_count) = 'integer' AND
                                      resume_prime_row_count >= 0))
                              CHECK ((resume_prime_through_seq IS NULL) =
                                     (resume_prime_row_count IS NULL))
                              CHECK (resume_prime_row_count IS NULL OR
                                     ((resume_prime_through_seq = 0) =
                                      (resume_prime_row_count = 0))),
    title                     TEXT
                              CHECK (title IS NULL OR
                                     (typeof(title) = 'text' AND length(title) BETWEEN 1 AND 100)),

    CHECK ((native_session_ref IS NULL) = (trace_filename IS NULL)),
    CHECK (vision_project_id IS NULL OR packet_id IS NULL)
);

-- The newest session of a Project is its Vision conversation. A clear starts
-- a newer session.
CREATE INDEX homie_sessions_vision_project ON homie_sessions (vision_project_id)
    WHERE vision_project_id IS NOT NULL;

-- The newest conversation of a packet is its current conversation when the
-- operator did not delete it. A New conversation action starts a newer one.
CREATE INDEX homie_sessions_packet ON homie_sessions (packet_id)
    WHERE packet_id IS NOT NULL;

-- Ended and reaped sessions remain available for resume until explicit deletion.
CREATE TRIGGER homie_sessions_no_delete
BEFORE DELETE ON homie_sessions
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='homie_sessions' AND row_id=OLD.rowid)
BEGIN
    SELECT RAISE(ABORT, 'These records require explicit Project deletion');
END;

CREATE TRIGGER homie_sessions_deletion_immutable
BEFORE UPDATE OF deleted_at ON homie_sessions
WHEN OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS NOT OLD.deleted_at
BEGIN
    SELECT RAISE(ABORT, 'a deleted conversation cannot be restored');
END;

CREATE TRIGGER homie_sessions_identity_immutable
BEFORE UPDATE OF id, created_at, container_name, verb_allowlist, session_path, binding, effort, vision_project_id, packet_id
ON homie_sessions
WHEN NEW.id <> OLD.id
  OR NEW.created_at <> OLD.created_at
  OR NEW.container_name <> OLD.container_name
  OR NEW.verb_allowlist <> OLD.verb_allowlist
  OR NEW.session_path <> OLD.session_path
  OR NEW.vision_project_id IS NOT OLD.vision_project_id
  OR NEW.packet_id IS NOT OLD.packet_id
  OR NEW.binding <> OLD.binding
  OR NEW.effort <> OLD.effort
BEGIN
    SELECT RAISE(ABORT, 'Homie session identity, route, effort, path, and allowlist are frozen at start');
END;

CREATE TRIGGER homie_sessions_native_locators_immutable
BEFORE UPDATE OF native_session_ref, trace_filename ON homie_sessions
WHEN OLD.native_session_ref IS NOT NULL
  AND (NEW.native_session_ref IS NOT OLD.native_session_ref
       OR NEW.trace_filename IS NOT OLD.trace_filename)
BEGIN
    SELECT RAISE(ABORT, 'Homie native session locators are immutable once registered');
END;

-- One row per bind *event*: active bindings clear when the session ends,
-- a resume creates a fresh binding for the requesting surface, and the row
-- and its binding history persists until explicit Project deletion.
-- Each surface and place has at most one active binding.
CREATE TABLE homie_bindings (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id  TEXT NOT NULL REFERENCES homie_sessions(id),
    surface     TEXT NOT NULL CHECK (surface IN ('discord', 'dashboard', 'cli')),
    channel_ref TEXT NOT NULL,
    bound_at    TEXT NOT NULL DEFAULT (datetime('now')),
    active      INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1))
);

CREATE UNIQUE INDEX bindings_one_active
    ON homie_bindings (surface, channel_ref) WHERE active = 1;

CREATE TRIGGER homie_bindings_require_active_session
BEFORE INSERT ON homie_bindings
WHEN NEW.active = 1 AND NOT EXISTS (
    SELECT 1 FROM homie_sessions s
    WHERE s.id = NEW.session_id AND s.status = 'active'
)
BEGIN
    SELECT RAISE(ABORT, 'an active binding requires an active Homie session');
END;

CREATE TRIGGER homie_bindings_history_immutable
BEFORE UPDATE ON homie_bindings
WHEN NEW.id <> OLD.id
  OR NEW.session_id <> OLD.session_id
  OR NEW.surface <> OLD.surface
  OR NEW.channel_ref <> OLD.channel_ref
  OR NEW.bound_at <> OLD.bound_at
  OR (OLD.active = 0 AND NEW.active = 1)
BEGIN
    SELECT RAISE(ABORT, 'binding history is immutable and inactive rows never reactivate');
END;

-- Ending/reaping a session clears its active bindings in the same statement;
-- resume creates fresh binding events rather than resurrecting history.
CREATE TRIGGER homie_sessions_deactivate_bindings
AFTER UPDATE OF status ON homie_sessions
WHEN OLD.status = 'active' AND NEW.status <> 'active'
BEGIN
    UPDATE homie_bindings SET active = 0
    WHERE session_id = NEW.id AND active = 1;
END;

CREATE TRIGGER homie_bindings_no_delete
BEFORE DELETE ON homie_bindings
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='homie_bindings' AND row_id=OLD.rowid)
BEGIN
    SELECT RAISE(ABORT, 'bindings history persists indefinitely');
END;

-- A Discord thread keeps its conversation across idle periods.
CREATE TABLE homie_discord_threads (
    session_id TEXT PRIMARY KEY REFERENCES homie_sessions(id),
    guild_id TEXT NOT NULL CHECK (length(guild_id) BETWEEN 1 AND 20 AND guild_id NOT GLOB '*[^0-9]*'),
    channel_id TEXT NOT NULL CHECK (length(channel_id) BETWEEN 1 AND 20 AND channel_id NOT GLOB '*[^0-9]*'),
    thread_id TEXT NOT NULL UNIQUE CHECK (length(thread_id) BETWEEN 1 AND 20 AND thread_id NOT GLOB '*[^0-9]*'),
    retired_at TEXT,
    CHECK (thread_id <> channel_id)
);
CREATE TRIGGER homie_discord_thread_identity
BEFORE UPDATE ON homie_discord_threads
WHEN NEW.session_id <> OLD.session_id OR NEW.guild_id <> OLD.guild_id
  OR NEW.channel_id <> OLD.channel_id OR NEW.thread_id <> OLD.thread_id
  OR (OLD.retired_at IS NOT NULL AND NEW.retired_at IS NOT OLD.retired_at)
BEGIN
    SELECT RAISE(ABORT, 'The Discord thread association is permanent.');
END;

CREATE TRIGGER homie_discord_thread_binding
BEFORE INSERT ON homie_bindings
WHEN NEW.surface = 'discord' AND EXISTS (
    SELECT 1 FROM homie_discord_threads t WHERE t.thread_id = NEW.channel_ref
      AND (t.session_id <> NEW.session_id OR t.retired_at IS NOT NULL)
)
BEGIN
    SELECT RAISE(ABORT, 'The Discord thread belongs to its original conversation.');
END;

-- Conversation rows: every inbound message and reply is an
-- append-only row with durable conversation identity and stable order,
-- alongside the pending-inbound claim state the Homie runner consumes.
-- Content columns are immutable; only the claim columns mutate.
CREATE TABLE conversation_messages (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id   TEXT    NOT NULL REFERENCES homie_sessions(id),
    seq          INTEGER NOT NULL,           -- stable order within the conversation
    direction    TEXT    NOT NULL CHECK (direction IN ('inbound', 'reply')),
    surface      TEXT    NOT NULL CHECK (surface IN ('discord', 'dashboard', 'cli', 'homie')),
    channel_ref  TEXT,
    body         TEXT    NOT NULL,
    source_message_id TEXT CHECK (source_message_id IS NULL OR
        (surface = 'discord' AND direction = 'inbound' AND
         length(source_message_id) BETWEEN 1 AND 20 AND source_message_id NOT GLOB '*[^0-9]*')),
    attachments  TEXT CHECK (attachments IS NULL OR
                       (json_valid(attachments) AND json_type(attachments) = 'array')),
                                             -- JSON array of file-plane references
    reply_to     INTEGER REFERENCES conversation_messages(id),
                                             -- the inbound turn a reply completes
    created_at   TEXT    NOT NULL DEFAULT (datetime('now')),
    claimed_by   TEXT,                       -- fenced, idempotent runner claims
    claimed_at   TEXT,
    completed_at TEXT,
    -- The runner's provisional reply text for an inbound turn that has no
    -- reply yet. Text only, at most 64 KiB, meaningful only until the reply.
    draft        TEXT    CHECK (draft IS NULL OR
                                (direction = 'inbound' AND typeof(draft) = 'text' AND
                                 length(CAST(draft AS BLOB)) <= 65536)),
    UNIQUE (session_id, seq),
    -- Every reply answers exactly one inbound turn; inbound rows never do.
    CHECK ((direction = 'inbound') = (reply_to IS NULL)),
    -- Claim state travels as a pair, only on inbound turns, and completion
    -- requires a claim.
    CHECK ((claimed_by IS NULL) = (claimed_at IS NULL)),
    CHECK (direction = 'inbound' OR claimed_by IS NULL),
    CHECK (completed_at IS NULL OR claimed_at IS NOT NULL)
);

-- "Runner death cannot create two logical replies to one inbound turn"
--: the one-reply-per-turn law lives in storage, not verb code.
CREATE UNIQUE INDEX conversation_one_reply_per_turn
    ON conversation_messages (reply_to) WHERE reply_to IS NOT NULL;

CREATE UNIQUE INDEX conversation_discord_source ON conversation_messages(source_message_id)
    WHERE source_message_id IS NOT NULL;

CREATE INDEX conversation_pending ON conversation_messages (session_id, id)
    WHERE direction = 'inbound' AND completed_at IS NULL;

CREATE TRIGGER conversation_content_immutable
BEFORE UPDATE ON conversation_messages
WHEN NEW.id <> OLD.id
  OR NEW.session_id <> OLD.session_id
  OR NEW.seq <> OLD.seq
  OR NEW.direction <> OLD.direction
  OR NEW.surface <> OLD.surface
  OR NEW.channel_ref IS NOT OLD.channel_ref
  OR NEW.body <> OLD.body
  OR NEW.source_message_id IS NOT OLD.source_message_id
  OR NEW.attachments IS NOT OLD.attachments
  OR NEW.reply_to IS NOT OLD.reply_to
  OR NEW.created_at <> OLD.created_at
BEGIN
    SELECT RAISE(ABORT, 'conversation rows are append-only; only claim state may change');
END;

-- A reply answers an inbound turn of its own conversation;
-- the FK alone would accept a reply row or another session's turn.
CREATE TRIGGER conversation_reply_answers_own_inbound
BEFORE INSERT ON conversation_messages
WHEN NEW.reply_to IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM conversation_messages m
    WHERE m.id = NEW.reply_to
      AND m.session_id = NEW.session_id
      AND m.direction = 'inbound'
)
BEGIN
    SELECT RAISE(ABORT, 'a reply must answer an inbound turn of its own conversation');
END;

-- Claim and completion state are one-way and set-once: a fresh runner
-- resumes the durable claim; nothing rewrites or clears it.
CREATE TRIGGER conversation_claim_set_once
BEFORE UPDATE OF claimed_by, claimed_at ON conversation_messages
WHEN OLD.claimed_by IS NOT NULL
  AND (NEW.claimed_by IS NOT OLD.claimed_by OR NEW.claimed_at IS NOT OLD.claimed_at)
BEGIN
    SELECT RAISE(ABORT, 'conversation claims are set-once');
END;

CREATE TRIGGER conversation_completion_set_once
BEFORE UPDATE OF completed_at ON conversation_messages
WHEN OLD.completed_at IS NOT NULL AND NEW.completed_at IS NOT OLD.completed_at
BEGIN
    SELECT RAISE(ABORT, 'turn completion is set-once');
END;

CREATE TRIGGER conversation_no_delete
BEFORE DELETE ON conversation_messages
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='conversation_messages' AND row_id=OLD.rowid)
BEGIN
    SELECT RAISE(ABORT, 'conversation rows are append-only');
END;

-- A transcript grows only while its session lives: a resurrected runner
-- replaying a claim/reply append onto an ended or reaped session must meet
-- a storage rejection, not just the domain layer's.
CREATE TRIGGER conversation_requires_active_session
BEFORE INSERT ON conversation_messages
WHEN NOT EXISTS (
    SELECT 1 FROM homie_sessions s
    WHERE s.id = NEW.session_id AND s.status = 'active'
)
BEGIN
    SELECT RAISE(ABORT, 'conversation rows require an active Homie session');
END;

------------------------------------------------------------------------------
-- Attachment file plane — canonical typed object identities and immutable
-- conversation references. The attachments column provides transcript context.
-- It grants no access to a host path.
------------------------------------------------------------------------------

CREATE TABLE attachment_objects (
    session_id TEXT NOT NULL REFERENCES homie_sessions(id),
    direction  TEXT NOT NULL CHECK (direction IN ('in', 'out')),
    sha256     TEXT NOT NULL CHECK (typeof(sha256) = 'text' AND
                                   length(sha256) = 64 AND
                                   length(CAST(sha256 AS BLOB)) = 64 AND
                                   sha256 GLOB '[0-9a-f]*' AND
                                   sha256 NOT GLOB '*[^0-9a-f]*'),
    size       INTEGER NOT NULL CHECK (typeof(size) = 'integer' AND
                                       size >= 0 AND size <= 268435456),
    state      TEXT NOT NULL CHECK (state IN ('publishing', 'live', 'delete_pending')),
    device     TEXT CHECK (device IS NULL OR
                           (typeof(device) = 'text' AND length(device) BETWEEN 1 AND 20 AND
                            device GLOB '[0-9]*' AND device NOT GLOB '*[^0-9]*')),
    inode      TEXT CHECK (inode IS NULL OR
                           (typeof(inode) = 'text' AND length(inode) BETWEEN 1 AND 20 AND
                            inode GLOB '[0-9]*' AND inode NOT GLOB '*[^0-9]*')),
    file_type  TEXT CHECK (file_type IS NULL OR file_type = 'regular'),
    creator_publication_id TEXT REFERENCES attachment_publications(id),
    creator_staging_name   TEXT,
    creator_journal_name   TEXT,
    creator_staging_device TEXT,
    creator_staging_inode  TEXT,
    creator_staging_type   TEXT CHECK (creator_staging_type IS NULL OR creator_staging_type='regular'),
    creator_journal_device TEXT,
    creator_journal_inode  TEXT,
    creator_journal_type   TEXT CHECK (creator_journal_type IS NULL OR creator_journal_type='regular'),
    gc_generation INTEGER NOT NULL DEFAULT 0
                  CHECK (typeof(gc_generation) = 'integer' AND gc_generation >= 0),
    PRIMARY KEY (session_id, direction, sha256, size),
    CHECK ((device IS NULL) = (inode IS NULL)),
    CHECK ((device IS NULL) = (file_type IS NULL)),
    CHECK (state = 'publishing' OR device IS NOT NULL)
);

CREATE TRIGGER attachment_objects_identity_immutable
BEFORE UPDATE ON attachment_objects
WHEN NEW.session_id <> OLD.session_id
  OR NEW.direction <> OLD.direction
  OR NEW.sha256 <> OLD.sha256
  OR NEW.size <> OLD.size
  OR (
      (NEW.creator_publication_id IS NOT OLD.creator_publication_id
       OR NEW.creator_staging_name IS NOT OLD.creator_staging_name
       OR NEW.creator_journal_name IS NOT OLD.creator_journal_name
       OR NEW.creator_staging_device IS NOT OLD.creator_staging_device
       OR NEW.creator_staging_inode IS NOT OLD.creator_staging_inode
       OR NEW.creator_staging_type IS NOT OLD.creator_staging_type
       OR NEW.creator_journal_device IS NOT OLD.creator_journal_device
       OR NEW.creator_journal_inode IS NOT OLD.creator_journal_inode
       OR NEW.creator_journal_type IS NOT OLD.creator_journal_type)
      AND NOT (
          OLD.state='live' AND NEW.state='live'
          AND OLD.creator_publication_id IS NOT NULL
          AND OLD.creator_staging_name IS NOT NULL
          AND OLD.creator_journal_name IS NOT NULL
          AND OLD.creator_staging_device IS NOT NULL
          AND OLD.creator_staging_inode IS NOT NULL
          AND OLD.creator_staging_type IS NOT NULL
          AND OLD.creator_journal_device IS NOT NULL
          AND OLD.creator_journal_inode IS NOT NULL
          AND OLD.creator_journal_type IS NOT NULL
          AND NEW.creator_publication_id IS NULL
          AND NEW.creator_staging_name IS NULL
          AND NEW.creator_journal_name IS NULL
          AND NEW.creator_staging_device IS NULL
          AND NEW.creator_staging_inode IS NULL
          AND NEW.creator_staging_type IS NULL
          AND NEW.creator_journal_device IS NULL
          AND NEW.creator_journal_inode IS NULL
          AND NEW.creator_journal_type IS NULL
          AND EXISTS (
              SELECT 1 FROM attachment_object_publications e
              JOIN attachment_publications p ON p.id=e.publication_id
              WHERE e.publication_id=OLD.creator_publication_id
                AND e.session_id=OLD.session_id AND e.direction=OLD.direction
                AND e.sha256=OLD.sha256 AND e.size=OLD.size
                AND p.state='object_bound'
          )
      )
  )
  OR (OLD.device IS NOT NULL AND
      (NEW.device IS NOT OLD.device OR NEW.inode IS NOT OLD.inode OR NEW.file_type IS NOT OLD.file_type))
BEGIN
    SELECT RAISE(ABORT, 'attachment canonical identity is immutable');
END;

CREATE TRIGGER attachment_objects_no_delete_when_referenced
BEFORE DELETE ON attachment_objects
WHEN (EXISTS (
    SELECT 1 FROM conversation_attachment_refs r
    WHERE r.session_id = OLD.session_id
      AND r.direction = OLD.direction
      AND r.sha256 = OLD.sha256
      AND r.size = OLD.size
) OR EXISTS (
    SELECT 1 FROM attachment_object_publications e
    WHERE e.session_id = OLD.session_id
      AND e.direction = OLD.direction
      AND e.sha256 = OLD.sha256
      AND e.size = OLD.size
))
  AND NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='attachment_objects' AND row_id=OLD.rowid)
BEGIN
    SELECT RAISE(ABORT, 'live attachment objects cannot be deleted while referenced');
END;

CREATE TRIGGER attachment_objects_require_exact_creator
BEFORE INSERT ON attachment_objects
WHEN NEW.state='publishing' AND (
    NEW.creator_publication_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM attachment_publications p
    WHERE p.id=NEW.creator_publication_id
      AND p.session_id=NEW.session_id AND p.direction=NEW.direction
      AND p.sha256=NEW.sha256 AND p.size=NEW.size
      AND p.state='publishing'
      AND p.staging_name=NEW.creator_staging_name
      AND p.journal_name=NEW.creator_journal_name
      AND p.staging_device=NEW.creator_staging_device
      AND p.staging_inode=NEW.creator_staging_inode
      AND p.staging_type=NEW.creator_staging_type
      AND p.journal_device=NEW.creator_journal_device
      AND p.journal_inode=NEW.creator_journal_inode
      AND p.journal_type=NEW.creator_journal_type
    )
)
BEGIN
    SELECT RAISE(ABORT, 'attachment object requires its exact durable creator publication');
END;

CREATE TRIGGER attachment_objects_creator_evidence_shape
BEFORE INSERT ON attachment_objects
WHEN NOT (
    (NEW.creator_publication_id IS NULL
     AND NEW.creator_staging_name IS NULL AND NEW.creator_journal_name IS NULL
     AND NEW.creator_staging_device IS NULL AND NEW.creator_staging_inode IS NULL
     AND NEW.creator_staging_type IS NULL AND NEW.creator_journal_device IS NULL
     AND NEW.creator_journal_inode IS NULL AND NEW.creator_journal_type IS NULL)
    OR
    (NEW.creator_publication_id IS NOT NULL
     AND NEW.creator_staging_name IS NOT NULL AND NEW.creator_journal_name IS NOT NULL
     AND NEW.creator_staging_device IS NOT NULL AND NEW.creator_staging_inode IS NOT NULL
     AND NEW.creator_staging_type IS NOT NULL AND NEW.creator_journal_device IS NOT NULL
     AND NEW.creator_journal_inode IS NOT NULL AND NEW.creator_journal_type IS NOT NULL)
)
BEGIN
    SELECT RAISE(ABORT, 'attachment object creator evidence must be complete or absent');
END;

CREATE UNIQUE INDEX attachment_objects_one_creator_publication
ON attachment_objects(creator_publication_id)
WHERE creator_publication_id IS NOT NULL;

CREATE TABLE conversation_attachment_refs (
    message_id   INTEGER NOT NULL REFERENCES conversation_messages(id),
    ordinal      INTEGER NOT NULL CHECK (typeof(ordinal) = 'integer' AND ordinal BETWEEN 1 AND 32),
    session_id   TEXT NOT NULL,
    direction    TEXT NOT NULL CHECK (direction IN ('in', 'out')),
    sha256       TEXT NOT NULL CHECK (typeof(sha256) = 'text' AND
                                     length(sha256) = 64 AND
                                     length(CAST(sha256 AS BLOB)) = 64 AND
                                     sha256 GLOB '[0-9a-f]*' AND
                                     sha256 NOT GLOB '*[^0-9a-f]*'),
    size         INTEGER NOT NULL CHECK (typeof(size) = 'integer' AND
                                         size >= 0 AND size <= 268435456),
    media_type   TEXT NOT NULL CHECK (typeof(media_type) = 'text' AND
                                      length(media_type) BETWEEN 1 AND 255 AND
                                      instr(media_type, char(0)) = 0 AND
                                      instr(media_type, char(10)) = 0 AND
                                      instr(media_type, char(13)) = 0),
    display_name TEXT NOT NULL CHECK (typeof(display_name) = 'text' AND
                                      length(display_name) BETWEEN 1 AND 255 AND
                                      instr(display_name, '/') = 0 AND
                                      instr(display_name, char(92)) = 0 AND
                                      instr(display_name, char(10)) = 0 AND
                                      instr(display_name, char(13)) = 0),
    PRIMARY KEY (message_id, ordinal),
    FOREIGN KEY (session_id, direction, sha256, size)
        REFERENCES attachment_objects(session_id, direction, sha256, size)
);

CREATE TRIGGER conversation_attachment_refs_match_message
BEFORE INSERT ON conversation_attachment_refs
WHEN NOT EXISTS (
    SELECT 1 FROM conversation_messages m
    WHERE m.id = NEW.message_id
      AND m.session_id = NEW.session_id
      AND ((m.direction = 'inbound' AND NEW.direction = 'in') OR
           (m.direction = 'reply' AND NEW.direction = 'out'))
)
BEGIN
    SELECT RAISE(ABORT, 'attachment reference must match its conversation session and direction');
END;

CREATE TRIGGER conversation_attachment_refs_require_live_object
BEFORE INSERT ON conversation_attachment_refs
WHEN NOT EXISTS (
    SELECT 1 FROM attachment_objects o
    WHERE o.session_id = NEW.session_id
      AND o.direction = NEW.direction
      AND o.sha256 = NEW.sha256
      AND o.size = NEW.size
      AND o.state = 'live'
)
BEGIN
    SELECT RAISE(ABORT, 'conversation attachment reference requires a materialized live object');
END;

CREATE TRIGGER conversation_attachment_refs_immutable
BEFORE UPDATE ON conversation_attachment_refs
BEGIN
    SELECT RAISE(ABORT, 'conversation attachment references are immutable');
END;

CREATE TRIGGER conversation_attachment_refs_no_delete
BEFORE DELETE ON conversation_attachment_refs
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='conversation_attachment_refs' AND row_id=OLD.rowid)
BEGIN
    SELECT RAISE(ABORT, 'conversation attachment references are durable history');
END;

-- Durable native inbox reservation. Manifest bytes are small metadata only;
-- staged attachment bytes and host paths never enter the spine.
CREATE TABLE attachment_ingresses (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id          TEXT NOT NULL REFERENCES homie_sessions(id),
    binding_id          INTEGER NOT NULL REFERENCES homie_bindings(id),
    surface             TEXT NOT NULL CHECK (surface IN ('discord', 'dashboard', 'cli')),
    external_message_id TEXT NOT NULL CHECK (typeof(external_message_id) = 'text' AND length(external_message_id) BETWEEN 1 AND 255),
    body                TEXT NOT NULL CHECK (typeof(body) = 'text' AND length(CAST(body AS BLOB)) <= 65536),
    body_sha256         TEXT CHECK (body_sha256 IS NULL OR
                                    (typeof(body_sha256)='text' AND length(body_sha256)=64 AND
                                     body_sha256 GLOB '[0-9a-f]*' AND
                                     body_sha256 NOT GLOB '*[^0-9a-f]*')),
    manifest            TEXT NOT NULL CHECK (json_valid(manifest) AND json_type(manifest) = 'array'),
    manifest_sha256     TEXT NOT NULL CHECK (typeof(manifest_sha256) = 'text' AND length(manifest_sha256) = 64 AND manifest_sha256 GLOB '[0-9a-f]*' AND manifest_sha256 NOT GLOB '*[^0-9a-f]*'),
    state               TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'publishing', 'consumed', 'failed')),
    attempt_count       INTEGER NOT NULL DEFAULT 0 CHECK (typeof(attempt_count) = 'integer' AND attempt_count >= 0),
    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    failure_count       INTEGER NOT NULL DEFAULT 0 CHECK (typeof(failure_count) = 'integer' AND failure_count BETWEEN 0 AND 3),
    failure_receipt     TEXT CHECK (failure_receipt IS NULL OR (json_valid(failure_receipt) AND json_type(failure_receipt)='object' AND length(CAST(failure_receipt AS BLOB)) <= 65536)),
    compacted_at        TEXT,
    UNIQUE (session_id, binding_id, surface, external_message_id)
);
CREATE TRIGGER attachment_ingresses_require_active_binding BEFORE INSERT ON attachment_ingresses
WHEN NOT EXISTS (SELECT 1 FROM homie_bindings b JOIN homie_sessions s ON s.id=b.session_id WHERE b.id=NEW.binding_id AND b.session_id=NEW.session_id AND b.surface=NEW.surface AND b.active=1 AND s.status='active')
BEGIN SELECT RAISE(ABORT, 'attachment ingress requires its exact active binding and session'); END;
CREATE TRIGGER attachment_ingresses_require_queued_insert BEFORE INSERT ON attachment_ingresses
WHEN NEW.state<>'queued' OR NEW.attempt_count<>0 OR NEW.failure_count<>0
  OR NEW.failure_receipt IS NOT NULL OR NEW.compacted_at IS NOT NULL
BEGIN SELECT RAISE(ABORT, 'attachment ingress must enter through the empty queued state'); END;
CREATE TRIGGER attachment_ingresses_content_immutable BEFORE UPDATE ON attachment_ingresses
WHEN NEW.id<>OLD.id OR NEW.session_id<>OLD.session_id OR NEW.binding_id<>OLD.binding_id
  OR NEW.surface<>OLD.surface OR NEW.external_message_id<>OLD.external_message_id
  OR NEW.body_sha256 IS NOT OLD.body_sha256
  OR NEW.manifest_sha256<>OLD.manifest_sha256 OR NEW.created_at<>OLD.created_at
  OR (
      (NEW.body<>OLD.body OR NEW.manifest<>OLD.manifest
       OR NEW.compacted_at IS NOT OLD.compacted_at)
      AND NOT (
          OLD.state='consumed' AND NEW.state='consumed'
          AND OLD.compacted_at IS NULL AND NEW.compacted_at IS NOT NULL
          AND NEW.body='' AND NEW.manifest='[]'
      )
  )
BEGIN SELECT RAISE(ABORT, 'attachment ingress identity and sealed content are immutable'); END;
CREATE TRIGGER attachment_ingresses_no_delete
BEFORE DELETE ON attachment_ingresses
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='attachment_ingresses' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'attachment ingress is durable native inbox history'); END;

-- One sealed batch and its exact publication credits are allocated before an
-- attachment publisher may stage a byte. Inbound batches belong to one stable
-- ingress; the nullable turn provenance reserves the same shape for the
-- outbound runner transaction without enabling that path yet.
CREATE TABLE attachment_batches (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id      TEXT NOT NULL REFERENCES homie_sessions(id),
    direction       TEXT NOT NULL CHECK (direction IN ('in', 'out')),
    ingress_id      INTEGER REFERENCES attachment_ingresses(id),
    turn_id         INTEGER REFERENCES conversation_messages(id),
    manifest        TEXT NOT NULL CHECK (json_valid(manifest) AND json_type(manifest) = 'array'),
    manifest_sha256 TEXT NOT NULL CHECK (typeof(manifest_sha256) = 'text' AND length(manifest_sha256) = 64 AND manifest_sha256 GLOB '[0-9a-f]*' AND manifest_sha256 NOT GLOB '*[^0-9a-f]*'),
    object_count    INTEGER NOT NULL CHECK (typeof(object_count) = 'integer' AND object_count BETWEEN 1 AND 32),
    byte_count      INTEGER NOT NULL CHECK (typeof(byte_count) = 'integer' AND byte_count BETWEEN 0 AND 536870912),
    state           TEXT NOT NULL DEFAULT 'reserved' CHECK (state IN ('reserved', 'publishing', 'materialized', 'consumed', 'aborted', 'cleanup_pending')),
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    CHECK ((direction = 'in' AND ingress_id IS NOT NULL AND turn_id IS NULL) OR
           (direction = 'out' AND ingress_id IS NULL AND turn_id IS NOT NULL))
);
CREATE TRIGGER attachment_batches_require_exact_ingress BEFORE INSERT ON attachment_batches
WHEN NEW.direction = 'in' AND NOT EXISTS (
    SELECT 1 FROM attachment_ingresses i
    WHERE i.id=NEW.ingress_id AND i.session_id=NEW.session_id
      AND i.state='queued' AND i.manifest=NEW.manifest
      AND i.manifest_sha256=NEW.manifest_sha256
)
BEGIN SELECT RAISE(ABORT, 'attachment batch must seal one queued ingress exactly'); END;
CREATE TRIGGER attachment_batches_require_reserved_insert BEFORE INSERT ON attachment_batches
WHEN NEW.state<>'reserved'
BEGIN SELECT RAISE(ABORT, 'attachment batch must enter through reserved state'); END;
CREATE TRIGGER attachment_batches_outbound_not_enabled BEFORE INSERT ON attachment_batches
WHEN NEW.direction = 'out'
BEGIN SELECT RAISE(ABORT, 'outbound attachment reservation is not enabled yet'); END;
CREATE TRIGGER attachment_batches_content_immutable BEFORE UPDATE ON attachment_batches
WHEN NEW.id<>OLD.id OR NEW.session_id<>OLD.session_id OR NEW.direction<>OLD.direction
  OR NEW.ingress_id IS NOT OLD.ingress_id OR NEW.turn_id IS NOT OLD.turn_id
  OR NEW.manifest<>OLD.manifest OR NEW.manifest_sha256<>OLD.manifest_sha256
  OR NEW.object_count<>OLD.object_count OR NEW.byte_count<>OLD.byte_count
  OR NEW.created_at<>OLD.created_at
BEGIN SELECT RAISE(ABORT, 'attachment batch declaration is immutable'); END;

CREATE TABLE attachment_publications (
    id                  TEXT PRIMARY KEY CHECK (typeof(id) = 'text' AND length(id) = 16 AND id GLOB '[0-9a-f]*' AND id NOT GLOB '*[^0-9a-f]*'),
    batch_id            INTEGER NOT NULL REFERENCES attachment_batches(id),
    session_id          TEXT NOT NULL REFERENCES homie_sessions(id),
    direction           TEXT NOT NULL CHECK (direction IN ('in', 'out')),
    ordinal             INTEGER NOT NULL CHECK (typeof(ordinal) = 'integer' AND ordinal BETWEEN 1 AND 32),
    size                INTEGER NOT NULL CHECK (typeof(size) = 'integer' AND size BETWEEN 0 AND 268435456),
    media_type          TEXT NOT NULL CHECK (typeof(media_type) = 'text' AND length(media_type) BETWEEN 1 AND 255 AND instr(media_type, char(0))=0 AND instr(media_type, char(10))=0 AND instr(media_type, char(13))=0),
    display_name        TEXT NOT NULL CHECK (typeof(display_name) = 'text' AND length(display_name) BETWEEN 1 AND 255 AND instr(display_name, '/')=0 AND instr(display_name, char(92))=0 AND instr(display_name, char(10))=0 AND instr(display_name, char(13))=0),
    sha256              TEXT CHECK (sha256 IS NULL OR (typeof(sha256) = 'text' AND length(sha256) = 64 AND sha256 GLOB '[0-9a-f]*' AND sha256 NOT GLOB '*[^0-9a-f]*')),
    owner_token         TEXT NOT NULL CHECK (typeof(owner_token) = 'text' AND length(owner_token) = 32 AND owner_token GLOB '[0-9a-f]*' AND owner_token NOT GLOB '*[^0-9a-f]*'),
    owner_generation    TEXT NOT NULL CHECK (typeof(owner_generation) = 'text' AND length(owner_generation) BETWEEN 1 AND 255 AND instr(owner_generation, char(0))=0 AND instr(owner_generation, char(10))=0 AND instr(owner_generation, char(13))=0),
    publisher_lease_until TEXT NOT NULL,
    hard_deadline       TEXT NOT NULL,
    staging_name        TEXT NOT NULL CHECK (staging_name = id || '.part'),
    journal_temp_name   TEXT NOT NULL,
    journal_name        TEXT NOT NULL CHECK (journal_name = id || '.json'),
    staging_device      TEXT,
    staging_inode       TEXT,
    staging_type        TEXT CHECK (staging_type IS NULL OR staging_type='regular'),
    journal_temp_device TEXT,
    journal_temp_inode  TEXT,
    journal_temp_type   TEXT CHECK (journal_temp_type IS NULL OR journal_temp_type='regular'),
    journal_device      TEXT,
    journal_inode       TEXT,
    journal_type        TEXT CHECK (journal_type IS NULL OR journal_type='regular'),
    object_role         TEXT CHECK (object_role IS NULL OR object_role IN ('creator','reuse')),
    state               TEXT NOT NULL DEFAULT 'reserved' CHECK (state IN ('reserved', 'publishing', 'object_bound', 'materialized', 'consumed', 'aborted', 'cleanup_pending')),
    gc_generation       INTEGER NOT NULL DEFAULT 0 CHECK (typeof(gc_generation) = 'integer' AND gc_generation >= 0),
    materialization_receipt TEXT CHECK (materialization_receipt IS NULL OR
                              (json_valid(materialization_receipt) AND
                               json_type(materialization_receipt)='object' AND
                               length(CAST(materialization_receipt AS BLOB))<=65536)),
    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    cleanup_from_state  TEXT CHECK (cleanup_from_state IS NULL OR cleanup_from_state IN ('reserved','publishing','object_bound','materialized','consumed')),
    cleanup_kind        TEXT CHECK (cleanup_kind IS NULL OR cleanup_kind IN ('attempt_terminal')),
    UNIQUE (batch_id, ordinal),
    CHECK (publisher_lease_until <= hard_deadline),
    CHECK ((staging_device IS NULL) = (staging_inode IS NULL)),
    CHECK ((journal_device IS NULL) = (journal_inode IS NULL))
);
CREATE TRIGGER attachment_publications_match_manifest BEFORE INSERT ON attachment_publications
WHEN NOT EXISTS (
    SELECT 1 FROM attachment_batches b, json_each(b.manifest) e
    WHERE b.id=NEW.batch_id AND b.session_id=NEW.session_id
      AND b.direction=NEW.direction AND b.state='reserved'
      AND CAST(json_extract(e.value,'$.ordinal') AS INTEGER)=NEW.ordinal
      AND CAST(json_extract(e.value,'$.size') AS INTEGER)=NEW.size
      AND json_extract(e.value,'$.media_type')=NEW.media_type
      AND json_extract(e.value,'$.display_name')=NEW.display_name
)
BEGIN SELECT RAISE(ABORT, 'attachment publication must match one sealed manifest ordinal'); END;
CREATE TRIGGER attachment_publications_require_reserved_insert BEFORE INSERT ON attachment_publications
WHEN NEW.state<>'reserved' OR NEW.gc_generation<>0
  OR NEW.staging_device IS NOT NULL OR NEW.staging_inode IS NOT NULL
  OR NEW.staging_type IS NOT NULL
  OR NEW.journal_temp_device IS NOT NULL OR NEW.journal_temp_inode IS NOT NULL
  OR NEW.journal_temp_type IS NOT NULL
  OR NEW.journal_device IS NOT NULL OR NEW.journal_inode IS NOT NULL
  OR NEW.journal_type IS NOT NULL OR NEW.object_role IS NOT NULL
  OR NEW.cleanup_from_state IS NOT NULL OR NEW.cleanup_kind IS NOT NULL
  OR NEW.materialization_receipt IS NOT NULL
  OR NEW.journal_temp_name<>NEW.id || '.json.tmp'
BEGIN SELECT RAISE(ABORT, 'attachment publication must enter through empty reserved state'); END;
CREATE TRIGGER attachment_publications_declaration_immutable BEFORE UPDATE ON attachment_publications
WHEN NEW.id<>OLD.id OR NEW.batch_id<>OLD.batch_id OR NEW.session_id<>OLD.session_id
  OR NEW.direction<>OLD.direction OR NEW.ordinal<>OLD.ordinal OR NEW.size<>OLD.size
  OR NEW.media_type<>OLD.media_type OR NEW.display_name<>OLD.display_name
  OR NEW.owner_token<>OLD.owner_token OR NEW.owner_generation<>OLD.owner_generation
  OR NEW.hard_deadline<>OLD.hard_deadline OR NEW.staging_name<>OLD.staging_name
  OR NEW.journal_temp_name<>OLD.journal_temp_name
  OR NEW.journal_name<>OLD.journal_name OR NEW.created_at<>OLD.created_at
  OR (OLD.sha256 IS NOT NULL AND NEW.sha256 IS NOT OLD.sha256)
  OR (OLD.staging_device IS NOT NULL AND (NEW.staging_device IS NOT OLD.staging_device OR NEW.staging_inode IS NOT OLD.staging_inode))
  OR (OLD.staging_type IS NOT NULL AND NEW.staging_type IS NOT OLD.staging_type)
  OR (OLD.journal_temp_device IS NOT NULL AND (NEW.journal_temp_device IS NOT OLD.journal_temp_device OR NEW.journal_temp_inode IS NOT OLD.journal_temp_inode))
  OR (OLD.journal_temp_type IS NOT NULL AND NEW.journal_temp_type IS NOT OLD.journal_temp_type)
  OR (OLD.journal_device IS NOT NULL AND (NEW.journal_device IS NOT OLD.journal_device OR NEW.journal_inode IS NOT OLD.journal_inode))
  OR (OLD.journal_type IS NOT NULL AND NEW.journal_type IS NOT OLD.journal_type)
  OR (OLD.object_role IS NOT NULL AND NEW.object_role IS NOT OLD.object_role)
  OR (OLD.cleanup_from_state IS NOT NULL AND NEW.cleanup_from_state IS NOT OLD.cleanup_from_state)
  OR (OLD.cleanup_kind IS NOT NULL AND NEW.cleanup_kind IS NOT OLD.cleanup_kind)
  OR (OLD.materialization_receipt IS NOT NULL AND NEW.materialization_receipt IS NOT OLD.materialization_receipt)
BEGIN SELECT RAISE(ABORT, 'attachment publication declaration and learned identity are immutable'); END;

CREATE TRIGGER attachment_publications_file_evidence_shape
BEFORE INSERT ON attachment_publications
WHEN NEW.journal_temp_name<>NEW.id || '.json.tmp'
  OR ((NEW.staging_device IS NULL)<>(NEW.staging_inode IS NULL))
  OR ((NEW.staging_device IS NULL)<>(NEW.staging_type IS NULL))
  OR ((NEW.journal_temp_device IS NULL)<>(NEW.journal_temp_inode IS NULL))
  OR ((NEW.journal_temp_device IS NULL)<>(NEW.journal_temp_type IS NULL))
  OR ((NEW.journal_device IS NULL)<>(NEW.journal_inode IS NULL))
  OR ((NEW.journal_device IS NULL)<>(NEW.journal_type IS NULL))
  OR ((NEW.cleanup_from_state IS NULL)<>(NEW.cleanup_kind IS NULL))
BEGIN
    SELECT RAISE(ABORT, 'attachment publication file evidence is incomplete or malformed');
END;

CREATE TRIGGER attachment_publications_file_evidence_shape_update
BEFORE UPDATE ON attachment_publications
WHEN NEW.journal_temp_name<>NEW.id || '.json.tmp'
  OR ((NEW.staging_device IS NULL)<>(NEW.staging_inode IS NULL))
  OR ((NEW.staging_device IS NULL)<>(NEW.staging_type IS NULL))
  OR ((NEW.journal_temp_device IS NULL)<>(NEW.journal_temp_inode IS NULL))
  OR ((NEW.journal_temp_device IS NULL)<>(NEW.journal_temp_type IS NULL))
  OR ((NEW.journal_device IS NULL)<>(NEW.journal_inode IS NULL))
  OR ((NEW.journal_device IS NULL)<>(NEW.journal_type IS NULL))
  OR ((NEW.cleanup_from_state IS NULL)<>(NEW.cleanup_kind IS NULL))
BEGIN
    SELECT RAISE(ABORT, 'attachment publication file evidence is incomplete or malformed');
END;

-- A digest learned for an inbound ordinal survives attempt cleanup and is
-- immutable across retries of the stable ingress.
CREATE TABLE attachment_ingress_digests (
    ingress_id INTEGER NOT NULL REFERENCES attachment_ingresses(id),
    ordinal    INTEGER NOT NULL CHECK (typeof(ordinal) = 'integer' AND ordinal BETWEEN 1 AND 32),
    sha256     TEXT NOT NULL CHECK (typeof(sha256) = 'text' AND length(sha256) = 64 AND sha256 GLOB '[0-9a-f]*' AND sha256 NOT GLOB '*[^0-9a-f]*'),
    PRIMARY KEY (ingress_id, ordinal)
);
CREATE TRIGGER attachment_ingress_digests_match_manifest BEFORE INSERT ON attachment_ingress_digests
WHEN NOT EXISTS (
    SELECT 1 FROM attachment_ingresses i, json_each(i.manifest) e
    WHERE i.id=NEW.ingress_id
      AND CAST(json_extract(e.value,'$.ordinal') AS INTEGER)=NEW.ordinal
)
BEGIN SELECT RAISE(ABORT, 'attachment digest must name a declared ingress ordinal'); END;
CREATE TRIGGER attachment_ingress_digests_immutable BEFORE UPDATE ON attachment_ingress_digests
BEGIN SELECT RAISE(ABORT, 'attachment ingress ordinal digest is immutable'); END;
CREATE TRIGGER attachment_publications_require_retry_digest BEFORE INSERT ON attachment_publications
WHEN NEW.direction='in' AND EXISTS (
    SELECT 1 FROM attachment_batches b
    JOIN attachment_ingress_digests d ON d.ingress_id=b.ingress_id AND d.ordinal=NEW.ordinal
    WHERE b.id=NEW.batch_id AND (NEW.sha256 IS NULL OR NEW.sha256<>d.sha256)
)
BEGIN SELECT RAISE(ABORT, 'attachment publication digest changed across ingress retry'); END;
CREATE TRIGGER attachment_publications_record_ingress_digest AFTER INSERT ON attachment_publications
WHEN NEW.direction='in' AND NEW.sha256 IS NOT NULL
BEGIN
    INSERT OR IGNORE INTO attachment_ingress_digests (ingress_id,ordinal,sha256)
    SELECT ingress_id,NEW.ordinal,NEW.sha256 FROM attachment_batches WHERE id=NEW.batch_id;
END;
CREATE TRIGGER attachment_publications_recheck_learned_digest BEFORE UPDATE OF sha256 ON attachment_publications
WHEN OLD.sha256 IS NULL AND NEW.sha256 IS NOT NULL AND EXISTS (
    SELECT 1 FROM attachment_batches b
    JOIN attachment_ingress_digests d ON d.ingress_id=b.ingress_id AND d.ordinal=NEW.ordinal
    WHERE b.id=NEW.batch_id AND NEW.sha256<>d.sha256
)
BEGIN SELECT RAISE(ABORT, 'attachment publication learned a conflicting ingress digest'); END;
CREATE TRIGGER attachment_publications_record_learned_digest AFTER UPDATE OF sha256 ON attachment_publications
WHEN OLD.sha256 IS NULL AND NEW.sha256 IS NOT NULL AND NEW.direction='in'
BEGIN
    INSERT OR IGNORE INTO attachment_ingress_digests (ingress_id,ordinal,sha256)
    SELECT ingress_id,NEW.ordinal,NEW.sha256 FROM attachment_batches WHERE id=NEW.batch_id;
END;

-- A publication edge is the removable liveness claim over one canonical
-- object. Conversation references remain the separate permanent ownership
-- class; neither a retry nor publication cleanup may delete those.
CREATE TABLE attachment_object_publications (
    publication_id TEXT PRIMARY KEY REFERENCES attachment_publications(id),
    session_id     TEXT NOT NULL,
    direction      TEXT NOT NULL CHECK (direction IN ('in', 'out')),
    sha256         TEXT NOT NULL,
    size           INTEGER NOT NULL,
    created_at     TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (session_id, direction, sha256, size)
        REFERENCES attachment_objects(session_id, direction, sha256, size)
);

CREATE TRIGGER attachment_object_publications_require_exact_live_claim
BEFORE INSERT ON attachment_object_publications
WHEN NOT EXISTS (
    SELECT 1
    FROM attachment_publications p
    JOIN attachment_objects o
      ON o.session_id=NEW.session_id AND o.direction=NEW.direction
     AND o.sha256=NEW.sha256 AND o.size=NEW.size
    WHERE p.id=NEW.publication_id
      AND p.session_id=NEW.session_id AND p.direction=NEW.direction
      AND p.sha256=NEW.sha256 AND p.size=NEW.size
      AND p.state='publishing'
      AND o.state IN ('publishing','live')
)
BEGIN
    SELECT RAISE(ABORT, 'attachment publication edge requires its exact publishing or live object');
END;

CREATE TRIGGER attachment_object_publications_immutable
BEFORE UPDATE ON attachment_object_publications
BEGIN
    SELECT RAISE(ABORT, 'attachment publication object edges are immutable');
END;

CREATE TRIGGER attachment_object_publications_delete_requires_terminal
BEFORE DELETE ON attachment_object_publications
WHEN (NOT EXISTS (
    SELECT 1 FROM attachment_publications p
    JOIN attachment_attempts a ON a.batch_id=p.batch_id
    JOIN attachment_batch_receipts r ON r.attempt_id=a.id AND r.batch_id=a.batch_id
    WHERE p.id=OLD.publication_id AND a.state='active'
))
  AND NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='attachment_object_publications' AND row_id=OLD.rowid)
BEGIN
    SELECT RAISE(ABORT, 'attachment publication edge deletion requires its exact terminal receipt');
END;

CREATE INDEX attachment_publications_cleanup_due
ON attachment_publications(state, publisher_lease_until, id);

CREATE TABLE attachment_attempts (
    id                    TEXT PRIMARY KEY CHECK (typeof(id) = 'text' AND length(id) = 16 AND id GLOB '[0-9a-f]*' AND id NOT GLOB '*[^0-9a-f]*'),
    ingress_id            INTEGER NOT NULL REFERENCES attachment_ingresses(id),
    session_id            TEXT NOT NULL REFERENCES homie_sessions(id),
    batch_id              INTEGER NOT NULL UNIQUE REFERENCES attachment_batches(id),
    generation            INTEGER NOT NULL CHECK (typeof(generation) = 'integer' AND generation >= 1),
    owner_token           TEXT NOT NULL CHECK (typeof(owner_token) = 'text' AND length(owner_token) = 32 AND owner_token GLOB '[0-9a-f]*' AND owner_token NOT GLOB '*[^0-9a-f]*'),
    owner_generation      TEXT NOT NULL CHECK (typeof(owner_generation) = 'text' AND length(owner_generation) BETWEEN 1 AND 255 AND instr(owner_generation, char(0))=0 AND instr(owner_generation, char(10))=0 AND instr(owner_generation, char(13))=0),
    publisher_lease_until TEXT NOT NULL,
    hard_deadline         TEXT NOT NULL,
    state                 TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'aborted', 'consumed', 'failed', 'cleanup_pending')),
    failure_receipt       TEXT CHECK (failure_receipt IS NULL OR (json_valid(failure_receipt) AND json_type(failure_receipt)='object' AND length(CAST(failure_receipt AS BLOB)) <= 65536)),
    created_at            TEXT NOT NULL DEFAULT (datetime('now')),
    owner_token_sha256    TEXT NOT NULL DEFAULT ''
                          CHECK (typeof(owner_token_sha256)='text' AND length(owner_token_sha256)=64 AND owner_token_sha256 GLOB '[0-9a-f]*' AND owner_token_sha256 NOT GLOB '*[^0-9a-f]*'),
    UNIQUE (ingress_id, generation),
    CHECK (publisher_lease_until <= hard_deadline),
    CHECK ((state='failed') = (failure_receipt IS NOT NULL))
);
CREATE UNIQUE INDEX attachment_attempts_one_active_session
ON attachment_attempts(session_id) WHERE state='active';
CREATE TRIGGER attachment_attempts_require_complete_batch BEFORE INSERT ON attachment_attempts
WHEN NOT EXISTS (
    SELECT 1 FROM attachment_batches b
    WHERE b.id=NEW.batch_id AND b.ingress_id=NEW.ingress_id
      AND b.session_id=NEW.session_id AND b.direction='in' AND b.state='reserved'
      AND b.object_count=(SELECT COUNT(*) FROM attachment_publications p WHERE p.batch_id=b.id)
      AND b.byte_count=(SELECT COALESCE(SUM(p.size),0) FROM attachment_publications p WHERE p.batch_id=b.id)
      AND b.object_count=json_array_length(b.manifest)
      AND b.byte_count=(
          SELECT COALESCE(SUM(CAST(json_extract(e.value,'$.size') AS INTEGER)),0)
          FROM json_each(b.manifest) e
      )
      AND 1=(SELECT MIN(p.ordinal) FROM attachment_publications p WHERE p.batch_id=b.id)
      AND b.object_count=(SELECT MAX(p.ordinal) FROM attachment_publications p WHERE p.batch_id=b.id)
      AND b.object_count=(
          SELECT COUNT(*) FROM attachment_publications p
          WHERE p.batch_id=b.id AND p.owner_generation=NEW.owner_generation
            AND p.publisher_lease_until=NEW.publisher_lease_until
            AND p.hard_deadline=NEW.hard_deadline
      )
)
BEGIN SELECT RAISE(ABORT, 'attachment attempt requires one complete exact sealed batch'); END;
CREATE TRIGGER attachment_attempts_require_active_insert BEFORE INSERT ON attachment_attempts
WHEN NEW.state<>'active' OR NEW.failure_receipt IS NOT NULL OR NEW.owner_token_sha256=''
BEGIN SELECT RAISE(ABORT, 'attachment attempt must enter through an authenticated active state'); END;
CREATE TRIGGER attachment_attempts_generation_matches_ingress BEFORE INSERT ON attachment_attempts
WHEN NOT EXISTS (
    SELECT 1 FROM attachment_ingresses i
    WHERE i.id=NEW.ingress_id AND i.session_id=NEW.session_id
      AND i.state='queued' AND NEW.generation=i.attempt_count+1
      AND EXISTS (SELECT 1 FROM homie_sessions s WHERE s.id=i.session_id AND s.status='active')
      AND NOT EXISTS (
          SELECT 1 FROM attachment_ingresses earlier
          WHERE earlier.session_id=i.session_id AND earlier.state='queued' AND earlier.id<i.id
      )
)
BEGIN SELECT RAISE(ABORT, 'attachment attempt generation must advance its queued ingress exactly'); END;
CREATE TRIGGER attachment_attempts_identity_immutable BEFORE UPDATE ON attachment_attempts
WHEN NEW.id<>OLD.id OR NEW.ingress_id<>OLD.ingress_id OR NEW.session_id<>OLD.session_id
  OR NEW.batch_id<>OLD.batch_id OR NEW.generation<>OLD.generation
  OR NEW.owner_token<>OLD.owner_token OR NEW.owner_generation<>OLD.owner_generation
  OR NEW.hard_deadline<>OLD.hard_deadline OR NEW.created_at<>OLD.created_at
  OR NEW.owner_token_sha256<>OLD.owner_token_sha256
BEGIN SELECT RAISE(ABORT, 'attachment attempt identity and hard deadline are immutable'); END;
CREATE TRIGGER attachment_attempts_activate_complete_ingress AFTER INSERT ON attachment_attempts
BEGIN
    UPDATE attachment_batches SET state='publishing'
    WHERE id=NEW.batch_id AND state='reserved';
    UPDATE attachment_ingresses
    SET state='publishing',attempt_count=NEW.generation
    WHERE id=NEW.ingress_id AND state='queued';
END;

CREATE TRIGGER attachment_ingresses_oldest_only BEFORE UPDATE OF state ON attachment_ingresses
WHEN OLD.state='queued' AND NEW.state='publishing' AND EXISTS (
    SELECT 1 FROM attachment_ingresses earlier
    WHERE earlier.session_id=OLD.session_id AND earlier.state='queued' AND earlier.id<OLD.id
)
BEGIN SELECT RAISE(ABORT, 'only the oldest queued attachment ingress may publish'); END;
CREATE TRIGGER attachment_ingresses_publishing_requires_attempt BEFORE UPDATE OF state ON attachment_ingresses
WHEN OLD.state='queued' AND NEW.state='publishing' AND NOT EXISTS (
    SELECT 1 FROM attachment_attempts a
    WHERE a.ingress_id=OLD.id AND a.session_id=OLD.session_id
      AND a.state='active' AND a.generation=NEW.attempt_count
)
BEGIN SELECT RAISE(ABORT, 'publishing attachment ingress requires its complete active attempt'); END;

-- A heartbeat may advance the exact active capability, but can neither revive
-- an expired lease nor move it more than 30 seconds beyond lock-domain DB time.
CREATE TRIGGER attachment_attempts_renew_live_lease BEFORE UPDATE OF publisher_lease_until ON attachment_attempts
WHEN NEW.publisher_lease_until IS NOT OLD.publisher_lease_until AND (
    OLD.state<>'active' OR NEW.state<>'active'
    OR datetime('now')>=OLD.publisher_lease_until
    OR datetime('now')>=OLD.hard_deadline
    OR NEW.publisher_lease_until<OLD.publisher_lease_until
    OR NEW.publisher_lease_until>datetime('now','+30 seconds')
    OR NEW.publisher_lease_until>OLD.hard_deadline
)
BEGIN SELECT RAISE(ABORT, 'attachment attempt lease renewal lost its live ownership fence'); END;

CREATE TRIGGER attachment_publications_renew_live_lease BEFORE UPDATE OF publisher_lease_until ON attachment_publications
WHEN NEW.publisher_lease_until IS NOT OLD.publisher_lease_until AND (
    OLD.state NOT IN ('reserved','publishing') OR NEW.state<>OLD.state
    OR datetime('now')>=OLD.publisher_lease_until
    OR datetime('now')>=OLD.hard_deadline
    OR NEW.publisher_lease_until<OLD.publisher_lease_until
    OR NEW.publisher_lease_until>datetime('now','+30 seconds')
    OR NEW.publisher_lease_until>OLD.hard_deadline
)
BEGIN SELECT RAISE(ABORT, 'attachment publication lease renewal lost its live ownership fence'); END;

CREATE TRIGGER attachment_publications_forward_state_fence BEFORE UPDATE OF state ON attachment_publications
WHEN NEW.state<>OLD.state AND NEW.state<>'cleanup_pending' AND NOT (
    OLD.state='reserved' AND NEW.state='publishing'
    AND NEW.sha256 IS NOT NULL
    AND NEW.staging_device IS NOT NULL AND NEW.staging_inode IS NOT NULL
    AND NEW.staging_type='regular'
    AND NEW.journal_device IS NULL AND NEW.journal_inode IS NULL
) AND NOT (
    OLD.state='publishing' AND NEW.state='object_bound'
    AND NEW.journal_device IS NOT NULL AND NEW.journal_inode IS NOT NULL
    AND NEW.journal_type='regular'
    AND NEW.object_role IN ('creator','reuse')
    AND EXISTS (
        SELECT 1 FROM attachment_object_publications e
        WHERE e.publication_id=OLD.id AND e.session_id=OLD.session_id
          AND e.direction=OLD.direction AND e.sha256=NEW.sha256 AND e.size=OLD.size
    )
) AND NOT (
    OLD.state='object_bound' AND NEW.state='materialized'
    AND NEW.materialization_receipt IS NOT NULL
    AND EXISTS (
        SELECT 1 FROM attachment_object_publications e
        JOIN attachment_objects o
          ON o.session_id=e.session_id AND o.direction=e.direction
         AND o.sha256=e.sha256 AND o.size=e.size
        WHERE e.publication_id=OLD.id AND o.state='live'
    )
) AND NOT (
    OLD.state='materialized' AND NEW.state='consumed'
    AND EXISTS (
        SELECT 1 FROM attachment_attempts a
        JOIN attachment_batch_receipts r
          ON r.attempt_id=a.id AND r.batch_id=a.batch_id
        JOIN conversation_attachment_refs c
          ON c.message_id=r.conversation_message_id
         AND c.ordinal=OLD.ordinal
         AND c.session_id=OLD.session_id
         AND c.direction=OLD.direction
         AND c.sha256=OLD.sha256 AND c.size=OLD.size
        WHERE a.batch_id=OLD.batch_id AND a.state='active'
          AND r.outcome='consumed'
    )
) AND NOT (
    OLD.state='cleanup_pending' AND NEW.state='aborted'
    AND NEW.cleanup_from_state=OLD.cleanup_from_state
    AND NEW.cleanup_kind=OLD.cleanup_kind
    AND NOT EXISTS (
        SELECT 1 FROM attachment_object_publications e
        WHERE e.publication_id=OLD.id
    )
)
BEGIN SELECT RAISE(ABORT, 'attachment publication state lacks its exact file/object evidence'); END;

CREATE TRIGGER attachment_objects_forward_state_fence BEFORE UPDATE OF state ON attachment_objects
WHEN NEW.state<>OLD.state AND NOT (
    OLD.state='publishing' AND NEW.state='live'
    AND NEW.device IS NOT NULL AND NEW.inode IS NOT NULL AND NEW.file_type='regular'
    AND (OLD.creator_publication_id IS NULL OR EXISTS (
        SELECT 1 FROM attachment_object_publications e
        JOIN attachment_publications p ON p.id=e.publication_id
        WHERE e.session_id=OLD.session_id AND e.direction=OLD.direction
          AND e.sha256=OLD.sha256 AND e.size=OLD.size
          AND p.id=OLD.creator_publication_id AND p.state='object_bound'
    ))
) AND NOT (
    OLD.state='publishing' AND NEW.state='delete_pending'
    AND NEW.gc_generation=OLD.gc_generation+1
    AND NOT EXISTS (
        SELECT 1 FROM attachment_object_publications e
        WHERE e.session_id=OLD.session_id AND e.direction=OLD.direction
          AND e.sha256=OLD.sha256 AND e.size=OLD.size
    )
    AND NOT EXISTS (
        SELECT 1 FROM conversation_attachment_refs r
        WHERE r.session_id=OLD.session_id AND r.direction=OLD.direction
          AND r.sha256=OLD.sha256 AND r.size=OLD.size
    )
) AND NOT (
    OLD.state='live' AND NEW.state='delete_pending'
    AND NEW.gc_generation=OLD.gc_generation+1
    AND NOT EXISTS (
        SELECT 1 FROM attachment_object_publications e
        WHERE e.session_id=OLD.session_id AND e.direction=OLD.direction
          AND e.sha256=OLD.sha256 AND e.size=OLD.size
    )
    AND NOT EXISTS (
        SELECT 1 FROM conversation_attachment_refs r
        WHERE r.session_id=OLD.session_id AND r.direction=OLD.direction
          AND r.sha256=OLD.sha256 AND r.size=OLD.size
    )
)
BEGIN SELECT RAISE(ABORT, 'attachment object state lacks its exact liveness evidence'); END;

CREATE TRIGGER attachment_batches_forward_state_fence BEFORE UPDATE OF state ON attachment_batches
WHEN NEW.state<>OLD.state AND NEW.state<>'cleanup_pending' AND NOT (
    OLD.state='reserved' AND NEW.state='publishing' AND EXISTS (
        SELECT 1 FROM attachment_attempts a
        WHERE a.batch_id=OLD.id AND a.state='active'
    )
) AND NOT (
    OLD.state='publishing' AND NEW.state='consumed'
    AND EXISTS (
        SELECT 1 FROM attachment_attempts a
        JOIN attachment_batch_receipts r
          ON r.attempt_id=a.id AND r.batch_id=a.batch_id
        WHERE a.batch_id=OLD.id AND a.state='active'
          AND r.outcome='consumed'
    )
    AND OLD.object_count=(
        SELECT COUNT(*) FROM attachment_publications p
        WHERE p.batch_id=OLD.id AND p.state='consumed'
    )
)
BEGIN SELECT RAISE(ABORT, 'attachment batch forward state lacks its exact active attempt'); END;

CREATE TRIGGER attachment_attempts_terminal_state_fence BEFORE UPDATE OF state ON attachment_attempts
WHEN NEW.state<>OLD.state AND NEW.state<>'cleanup_pending' AND NOT (
    OLD.state='active' AND NEW.state='consumed'
    AND EXISTS (
        SELECT 1 FROM attachment_batch_receipts r
        JOIN attachment_batches b ON b.id=OLD.batch_id
        JOIN attachment_ingresses i ON i.id=OLD.ingress_id
        WHERE r.attempt_id=OLD.id AND r.batch_id=OLD.batch_id
          AND r.ingress_id=OLD.ingress_id AND r.session_id=OLD.session_id
          AND r.generation=OLD.generation AND r.outcome='consumed'
          AND b.state='consumed' AND i.state='consumed'
    )
)
BEGIN SELECT RAISE(ABORT, 'attachment attempt terminal state requires the receipt-backed terminalizer'); END;

------------------------------------------------------------------------------
-- outbox — the one system→operator interconnect. Delivery
-- bookkeeping only; the durable conversation row is never removed by
-- delivery. One row per (event, destination).
------------------------------------------------------------------------------

-- Host config.toml remains the operator authority; onboarding projects only
-- its validated, secret-free alert destinations into the lock domain so the
-- event transaction can resolve fan-out without reading a host path.
CREATE TABLE alert_routes (
    class       TEXT NOT NULL CHECK (class IN ('blocked', 'health')),
    ordinal     INTEGER NOT NULL CHECK (ordinal >= 1 AND ordinal <= 2),
    surface     TEXT NOT NULL CHECK (surface IN ('dashboard', 'discord')),
    channel_ref TEXT,
    PRIMARY KEY (class, ordinal),
    UNIQUE (class, surface),
    CHECK (
        (surface = 'dashboard' AND channel_ref IS NULL) OR
        (surface = 'discord' AND
         typeof(channel_ref) = 'text' AND
         length(channel_ref) BETWEEN 1 AND 20 AND
         channel_ref NOT GLOB '*[^0-9]*')
    )
);

INSERT INTO alert_routes (class, ordinal, surface, channel_ref) VALUES
    ('blocked', 1, 'dashboard', NULL),
    ('health', 1, 'dashboard', NULL);

CREATE TABLE outbox (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    kind                  TEXT NOT NULL CHECK (kind IN
                          ('homie_reply', 'homie_echo', 'blocked_alert', 'health')),
    session_id            TEXT REFERENCES homie_sessions(id),
    surface               TEXT NOT NULL CHECK (surface IN ('discord', 'dashboard', 'cli')),
    channel_ref           TEXT,
    payload               TEXT NOT NULL    -- small inline JSON; anything large rides as a path reference
                          CHECK (json_valid(payload) AND json_type(payload) = 'object'),
    source_activity_id    INTEGER REFERENCES activity(id),
    -- Same dual-length NUL fence as activity's keys above.
    event_destination_key TEXT
                          CHECK (event_destination_key IS NULL OR
                                 (length(event_destination_key) = 64 AND
                                  length(CAST(event_destination_key AS BLOB)) = 64 AND
                                  event_destination_key NOT GLOB '*[^0-9a-f]*')),
    created_at            TEXT NOT NULL DEFAULT (datetime('now')),
    delivered_at          TEXT,
    retired_at            TEXT,
    CHECK (retired_at IS NULL OR (surface = 'discord' AND delivered_at IS NULL)),
    CHECK ((source_activity_id IS NULL) = (event_destination_key IS NULL))
);

CREATE UNIQUE INDEX outbox_event_destination_key ON outbox (event_destination_key);

CREATE INDEX outbox_undelivered ON outbox (surface, id) WHERE delivered_at IS NULL;

-- Nothing pushed is ever lost: outbox rows are delivery history —
-- The content stays immutable until explicit Project deletion. Delivery stamps once.
-- (ack replay preserves the original timestamp).
CREATE TRIGGER outbox_no_delete
BEFORE DELETE ON outbox
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='outbox' AND row_id=OLD.rowid)
BEGIN
    SELECT RAISE(ABORT, 'These records require explicit Project deletion');
END;

CREATE TRIGGER outbox_content_immutable
BEFORE UPDATE ON outbox
WHEN NEW.id <> OLD.id
  OR NEW.kind <> OLD.kind
  OR NEW.session_id IS NOT OLD.session_id
  OR NEW.surface <> OLD.surface
  OR NEW.channel_ref IS NOT OLD.channel_ref
  OR NEW.payload <> OLD.payload
  OR NEW.source_activity_id IS NOT OLD.source_activity_id
  OR NEW.event_destination_key IS NOT OLD.event_destination_key
  OR NEW.created_at <> OLD.created_at
BEGIN
    SELECT RAISE(ABORT, 'outbox content is immutable; only delivery bookkeeping advances');
END;

-- The outbox_content_immutable trigger
-- already refuses any UPDATE that changes the key (including NULL -> BLOB),
-- so INSERT is the only write this needs to cover.
CREATE TRIGGER outbox_event_destination_key_is_text
BEFORE INSERT ON outbox
WHEN NEW.event_destination_key IS NOT NULL AND typeof(NEW.event_destination_key) != 'text'
BEGIN
    SELECT RAISE(ABORT, 'outbox.event_destination_key must be TEXT; a BLOB bypasses the hex fence');
END;

CREATE TRIGGER outbox_delivered_once
BEFORE UPDATE OF delivered_at ON outbox
WHEN OLD.delivered_at IS NOT NULL AND NEW.delivered_at IS NOT OLD.delivered_at
BEGIN
    SELECT RAISE(ABORT, 'delivered_at is set exactly once; replays preserve the original');
END;

CREATE TRIGGER outbox_retired_once
BEFORE UPDATE OF retired_at ON outbox
WHEN OLD.retired_at IS NOT NULL AND NEW.retired_at IS NOT OLD.retired_at
BEGIN
    SELECT RAISE(ABORT, 'A retired destination remains retired.');
END;

-- A direct block and its operator alerts commit together.
CREATE TRIGGER tasks_blocked_alert_outbox
AFTER UPDATE OF blocked ON tasks
WHEN OLD.blocked = 0 AND NEW.blocked = 1
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM alert_routes WHERE class = 'blocked'
    ) THEN RAISE(ABORT, 'blocked alert routing is not configured') END;
    INSERT INTO outbox (kind, surface, channel_ref, payload)
    SELECT 'blocked_alert', surface, channel_ref,
           json_object(
               'task_id', NEW.id,
               'reason', substr(NEW.blocked_reason, 1, 900),
               'body', 'Task #' || NEW.id || ' blocked: ' || substr(NEW.blocked_reason, 1, 900)
           )
    FROM alert_routes
    WHERE class = 'blocked'
    ORDER BY ordinal;
END;

CREATE TRIGGER tasks_blocked_birth_alert_outbox
AFTER INSERT ON tasks
WHEN NEW.blocked = 1
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM alert_routes WHERE class = 'blocked'
    ) THEN RAISE(ABORT, 'blocked alert routing is not configured') END;
    INSERT INTO outbox (kind, surface, channel_ref, payload)
    SELECT 'blocked_alert', surface, channel_ref,
           json_object(
               'task_id', NEW.id,
               'reason', substr(NEW.blocked_reason, 1, 900),
               'body', 'Task #' || NEW.id || ' blocked: ' || substr(NEW.blocked_reason, 1, 900)
           )
    FROM alert_routes
    WHERE class = 'blocked'
    ORDER BY ordinal;
END;

-- Deployment-health recording and notification are the same transaction.
-- The producer detail was already reduced to the closed canonical refusal
-- object before this trigger sees it.
CREATE TRIGGER activity_health_alert_outbox
AFTER INSERT ON activity
WHEN NEW.kind = 'dispatch.health'
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM alert_routes WHERE class = 'health'
    ) THEN RAISE(ABORT, 'health alert routing is not configured') END;
    INSERT INTO outbox (kind, surface, channel_ref, payload)
    SELECT 'health', surface, channel_ref,
           json_object(
               'activity_id', NEW.id,
               'subject', NEW.subject,
               'detail', NEW.detail,
               'body', 'Culler health: ' || NEW.detail
           )
    FROM alert_routes
    WHERE class = 'health'
    ORDER BY ordinal;
END;

-- Permanent small terminal evidence survives later compaction of derived
-- attempts/publications. The capability is retained only as a SHA-256 digest,
-- so replay can authenticate without keeping the bearer token itself.
CREATE TABLE attachment_batch_receipts (
    batch_id               INTEGER PRIMARY KEY,
    attempt_id             TEXT NOT NULL UNIQUE CHECK (typeof(attempt_id)='text' AND length(attempt_id)=16 AND attempt_id GLOB '[0-9a-f]*' AND attempt_id NOT GLOB '*[^0-9a-f]*'),
    ingress_id             INTEGER NOT NULL REFERENCES attachment_ingresses(id),
    session_id             TEXT NOT NULL REFERENCES homie_sessions(id),
    generation             INTEGER NOT NULL CHECK (typeof(generation)='integer' AND generation>=1),
    owner_generation       TEXT NOT NULL CHECK (typeof(owner_generation)='text' AND length(owner_generation) BETWEEN 1 AND 255 AND instr(owner_generation,char(0))=0 AND instr(owner_generation,char(10))=0 AND instr(owner_generation,char(13))=0),
    outcome                TEXT NOT NULL CHECK (outcome IN ('requeued','failed','consumed')),
    reason                 TEXT NOT NULL CHECK (reason IN ('explicit_abort','hard_expired','consumed')),
    capability_sha256      TEXT NOT NULL CHECK (typeof(capability_sha256)='text' AND length(capability_sha256)=64 AND capability_sha256 GLOB '[0-9a-f]*' AND capability_sha256 NOT GLOB '*[^0-9a-f]*'),
    receipt                TEXT NOT NULL CHECK (json_valid(receipt) AND json_type(receipt)='object' AND length(CAST(receipt AS BLOB))<=65536),
    outbox_id              INTEGER UNIQUE REFERENCES outbox(id),
    conversation_message_id INTEGER REFERENCES conversation_messages(id),
    created_at             TEXT NOT NULL DEFAULT (datetime('now')),
    CHECK ((outcome='failed')=(outbox_id IS NOT NULL)),
    CHECK ((outcome='consumed')=(reason='consumed'))
);
CREATE UNIQUE INDEX attachment_batch_receipts_conversation_message
ON attachment_batch_receipts(conversation_message_id)
WHERE conversation_message_id IS NOT NULL;

CREATE TRIGGER attachment_batches_require_monotonic_receipt_id
AFTER INSERT ON attachment_batches
WHEN NEW.id <= COALESCE((SELECT MAX(batch_id) FROM attachment_batch_receipts),0)
BEGIN SELECT RAISE(ABORT, 'attachment batch id must advance permanent receipt history'); END;

CREATE TRIGGER attachment_batch_receipts_require_live_attempt BEFORE INSERT ON attachment_batch_receipts
WHEN NOT EXISTS (
    SELECT 1 FROM attachment_attempts a
    JOIN attachment_batches b ON b.id=a.batch_id
    JOIN attachment_ingresses i ON i.id=a.ingress_id
    WHERE a.id=NEW.attempt_id AND a.batch_id=NEW.batch_id
      AND a.ingress_id=NEW.ingress_id AND a.session_id=NEW.session_id
      AND a.generation=NEW.generation
      AND a.owner_generation=NEW.owner_generation AND a.state='active'
      AND b.state='publishing' AND i.state='publishing'
      AND NEW.generation=i.attempt_count
      AND ((NEW.outcome='requeued' AND i.failure_count<2)
        OR (NEW.outcome='failed' AND i.failure_count=2)
        OR (NEW.outcome='consumed'
            AND NEW.outbox_id IS NULL
            AND NEW.conversation_message_id IS NOT NULL
            AND EXISTS (
                SELECT 1 FROM conversation_messages m
                JOIN homie_bindings origin ON origin.id=i.binding_id
                WHERE m.id=NEW.conversation_message_id
                  AND m.session_id=NEW.session_id
                  AND origin.session_id=i.session_id
                  AND origin.surface=i.surface
                  AND origin.active=1
                  AND m.direction='inbound'
                  AND m.surface=i.surface
                  AND m.channel_ref=origin.channel_ref
                  AND m.body=i.body
                  AND m.attachments IS NULL
            )
            AND b.object_count=(
                SELECT COUNT(*) FROM attachment_publications p
                WHERE p.batch_id=b.id AND p.state='materialized'
            )
            AND b.object_count=(
                SELECT COUNT(*) FROM conversation_attachment_refs c
                JOIN attachment_publications p
                  ON p.batch_id=b.id AND p.ordinal=c.ordinal
                 AND p.session_id=c.session_id AND p.direction=c.direction
                 AND p.sha256=c.sha256 AND p.size=c.size
                JOIN attachment_objects o
                  ON o.session_id=c.session_id AND o.direction=c.direction
                 AND o.sha256=c.sha256 AND o.size=c.size
                JOIN attachment_object_publications e
                  ON e.publication_id=p.id
                 AND e.session_id=o.session_id AND e.direction=o.direction
                 AND e.sha256=o.sha256 AND e.size=o.size
                WHERE c.message_id=NEW.conversation_message_id
                  AND c.session_id=NEW.session_id
                  AND c.direction='in'
                  AND c.media_type=p.media_type
                  AND c.display_name=p.display_name
                  AND o.state='live'
            )))
      AND NEW.capability_sha256=a.owner_token_sha256
      AND ((NEW.outcome='consumed')=(NEW.conversation_message_id IS NOT NULL))
      AND NEW.receipt=CASE NEW.outcome
          WHEN 'requeued' THEN json_object(
              'code','attachment_publish_retry',
              'attachment_ingress_id',NEW.ingress_id,
              'attempt_id',NEW.attempt_id,
              'attempt',NEW.generation,
              'outcome',NEW.outcome,
              'reason',NEW.reason,
              'message','Attachment publication attempt was fenced and queued for retry.',
              'body','Attachment publication attempt was fenced and queued for retry.',
              'retryable',json('true'))
          WHEN 'failed' THEN json_object(
              'code','attachment_publish_failed',
              'attachment_ingress_id',NEW.ingress_id,
              'attempt_id',NEW.attempt_id,
              'attempt',NEW.generation,
              'outcome',NEW.outcome,
              'reason',NEW.reason,
              'message','Attachment publication failed after three attempts.',
              'body','Attachment publication failed after three attempts.',
              'retryable',json('false'))
          WHEN 'consumed' THEN json_object(
              'attachment_ingress_id',NEW.ingress_id,
              'attempt',NEW.generation,
              'attempt_id',NEW.attempt_id,
              'batch_id',NEW.batch_id,
              'body_sha256',i.body_sha256,
              'channel_ref',(SELECT channel_ref FROM homie_bindings WHERE id=i.binding_id),
              'echoes',(SELECT COUNT(*) FROM homie_bindings destination
                        WHERE destination.session_id=NEW.session_id
                          AND destination.active=1
                          AND NOT (destination.surface=i.surface
                                   AND destination.channel_ref=(
                                       SELECT channel_ref FROM homie_bindings
                                       WHERE id=i.binding_id))),
              'external_message_id',i.external_message_id,
              'manifest_sha256',i.manifest_sha256,
              'message_id',NEW.conversation_message_id,
              'seq',(SELECT seq FROM conversation_messages
                     WHERE id=NEW.conversation_message_id),
              'session_id',NEW.session_id,
              'state','consumed',
              'surface',i.surface)
          END
      AND (NEW.outcome<>'consumed' OR i.body_sha256 IS NOT NULL)
      AND (NEW.outbox_id IS NULL OR EXISTS (
          SELECT 1 FROM outbox o
          WHERE o.id=NEW.outbox_id AND o.kind='homie_reply'
            AND o.session_id=NEW.session_id AND o.surface=i.surface
            AND o.channel_ref=(SELECT channel_ref FROM homie_bindings WHERE id=i.binding_id)
            AND o.payload=NEW.receipt
      ))
)
BEGIN SELECT RAISE(ABORT, 'attachment terminal receipt requires its exact live attempt and surface event'); END;

CREATE TRIGGER attachment_batch_receipts_immutable BEFORE UPDATE ON attachment_batch_receipts
BEGIN SELECT RAISE(ABORT, 'attachment batch terminal receipts are immutable'); END;

CREATE TRIGGER attachment_batch_receipts_no_delete
BEFORE DELETE ON attachment_batch_receipts
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='attachment_batch_receipts' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'attachment batch terminal receipts survive cleanup'); END;

CREATE TRIGGER attachment_publications_delete_requires_terminal_receipt
BEFORE DELETE ON attachment_publications
WHEN (OLD.state<>'aborted'
  OR OLD.cleanup_kind<>'attempt_terminal'
  OR OLD.cleanup_from_state IS NULL
  OR EXISTS (
      SELECT 1 FROM attachment_object_publications e
      WHERE e.publication_id=OLD.id
  )
  OR NOT EXISTS (
      SELECT 1 FROM attachment_attempts a
      JOIN attachment_batch_receipts r
        ON r.attempt_id=a.id AND r.batch_id=a.batch_id
      WHERE a.batch_id=OLD.batch_id AND a.session_id=OLD.session_id
        AND a.state='cleanup_pending'
        AND ((OLD.cleanup_from_state='consumed' AND r.outcome='consumed')
          OR (OLD.cleanup_from_state<>'consumed' AND r.outcome IN ('requeued','failed')))
  ))
  AND NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='attachment_publications' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'attachment publication deletion requires exact terminal cleanup evidence'); END;

CREATE TRIGGER attachment_attempts_delete_requires_terminal_receipt
BEFORE DELETE ON attachment_attempts
WHEN (OLD.state<>'cleanup_pending'
  OR EXISTS (
      SELECT 1 FROM attachment_publications p WHERE p.batch_id=OLD.batch_id
  )
  OR NOT EXISTS (
      SELECT 1 FROM attachment_batch_receipts r
      JOIN attachment_batches b ON b.id=OLD.batch_id
      JOIN attachment_ingresses i ON i.id=OLD.ingress_id
      WHERE r.attempt_id=OLD.id AND r.batch_id=OLD.batch_id
        AND r.ingress_id=OLD.ingress_id AND r.session_id=OLD.session_id
        AND r.generation=OLD.generation
        AND r.owner_generation=OLD.owner_generation
        AND b.session_id=OLD.session_id AND b.ingress_id=OLD.ingress_id
        AND b.state='cleanup_pending'
        AND ((r.outcome='requeued' AND i.state='queued')
          OR (r.outcome='failed' AND i.state='failed' AND i.failure_receipt=r.receipt)
          OR (r.outcome='consumed' AND i.state='consumed'))
  ))
  AND NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='attachment_attempts' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'attachment attempt deletion requires exact terminal cleanup evidence'); END;

CREATE TRIGGER attachment_batches_delete_requires_terminal_receipt
BEFORE DELETE ON attachment_batches
WHEN (OLD.state<>'cleanup_pending'
  OR EXISTS (
      SELECT 1 FROM attachment_publications p WHERE p.batch_id=OLD.id
  )
  OR EXISTS (
      SELECT 1 FROM attachment_attempts a WHERE a.batch_id=OLD.id
  )
  OR NOT EXISTS (
      SELECT 1 FROM attachment_batch_receipts r
      JOIN attachment_ingresses i ON i.id=OLD.ingress_id
      WHERE r.batch_id=OLD.id AND r.ingress_id=OLD.ingress_id
        AND r.session_id=OLD.session_id
        AND ((r.outcome='requeued' AND i.state='queued')
          OR (r.outcome='failed' AND i.state='failed' AND i.failure_receipt=r.receipt)
          OR (r.outcome='consumed' AND i.state='consumed'))
  ))
  AND NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='attachment_batches' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'attachment batch deletion requires exact terminal cleanup evidence'); END;

CREATE TRIGGER attachment_ingress_digests_no_delete
BEFORE DELETE ON attachment_ingress_digests
WHEN (NOT EXISTS (
    SELECT 1 FROM attachment_ingresses i
    JOIN attachment_batch_receipts r ON r.ingress_id=i.id
    JOIN conversation_attachment_refs c
      ON c.message_id=r.conversation_message_id
     AND c.ordinal=OLD.ordinal
     AND c.session_id=r.session_id
     AND c.direction='in'
     AND c.sha256=OLD.sha256
    WHERE i.id=OLD.ingress_id AND i.state='consumed'
      AND r.outcome='consumed'
      AND NOT EXISTS (
          SELECT 1 FROM attachment_batches b WHERE b.ingress_id=i.id
      )
))
  AND NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='attachment_ingress_digests' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'attachment ingress digest deletion requires durable conversation ownership'); END;

CREATE TRIGGER attachment_publications_cleanup_requires_receipt BEFORE UPDATE OF state ON attachment_publications
WHEN NEW.state='cleanup_pending' AND OLD.state<>NEW.state AND (
    NEW.sha256 IS NOT OLD.sha256
    OR NEW.staging_device IS NOT OLD.staging_device OR NEW.staging_inode IS NOT OLD.staging_inode
    OR NEW.staging_type IS NOT OLD.staging_type
    OR NEW.journal_temp_device IS NOT OLD.journal_temp_device
    OR NEW.journal_temp_inode IS NOT OLD.journal_temp_inode
    OR NEW.journal_temp_type IS NOT OLD.journal_temp_type
    OR NEW.journal_device IS NOT OLD.journal_device OR NEW.journal_inode IS NOT OLD.journal_inode
    OR NEW.journal_type IS NOT OLD.journal_type
    OR NEW.object_role IS NOT OLD.object_role
    OR NEW.materialization_receipt IS NOT OLD.materialization_receipt
    OR NEW.gc_generation<>OLD.gc_generation+1
    OR NEW.cleanup_from_state<>OLD.state OR NEW.cleanup_kind<>'attempt_terminal'
    OR NOT EXISTS (
        SELECT 1 FROM attachment_attempts a
        JOIN attachment_batch_receipts r ON r.attempt_id=a.id AND r.batch_id=a.batch_id
        WHERE a.batch_id=OLD.batch_id
          AND (
              (a.state='active' AND r.outcome IN ('requeued','failed'))
              OR
              (OLD.state='consumed' AND a.state='consumed'
               AND r.outcome='consumed'
               AND EXISTS (
                   SELECT 1 FROM conversation_attachment_refs c
                   WHERE c.message_id=r.conversation_message_id
                     AND c.ordinal=OLD.ordinal
                     AND c.session_id=OLD.session_id
                     AND c.direction=OLD.direction
                     AND c.sha256=OLD.sha256 AND c.size=OLD.size
               ))
          )
    )
)
BEGIN SELECT RAISE(ABORT, 'attachment publication cleanup requires its fenced terminal receipt'); END;

CREATE TRIGGER attachment_batches_cleanup_requires_receipt BEFORE UPDATE OF state ON attachment_batches
WHEN NEW.state='cleanup_pending' AND OLD.state<>NEW.state AND NOT EXISTS (
    SELECT 1 FROM attachment_attempts a
    JOIN attachment_batch_receipts r ON r.attempt_id=a.id AND r.batch_id=OLD.id
    WHERE a.batch_id=OLD.id
      AND ((a.state='active' AND r.outcome IN ('requeued','failed'))
        OR (a.state='consumed' AND r.outcome='consumed'))
      AND NOT EXISTS (
          SELECT 1 FROM attachment_publications p
          WHERE p.batch_id=OLD.id AND p.state<>'cleanup_pending'
      )
)
BEGIN SELECT RAISE(ABORT, 'attachment batch cleanup requires every exact publication fenced'); END;

CREATE TRIGGER attachment_ingresses_terminal_transition BEFORE UPDATE ON attachment_ingresses
WHEN (
    NEW.state IS NOT OLD.state OR NEW.attempt_count<>OLD.attempt_count
    OR NEW.failure_count<>OLD.failure_count
    OR NEW.failure_receipt IS NOT OLD.failure_receipt
) AND NOT (
    OLD.state='queued' AND NEW.state='publishing'
    AND NEW.attempt_count=OLD.attempt_count+1
    AND NEW.failure_count=OLD.failure_count
    AND NEW.failure_receipt IS OLD.failure_receipt
) AND NOT (
    OLD.state='publishing' AND NEW.state='queued'
    AND NEW.attempt_count=OLD.attempt_count
    AND NEW.failure_count=OLD.failure_count+1 AND NEW.failure_count<3
    AND NEW.failure_receipt IS OLD.failure_receipt
    AND EXISTS (
        SELECT 1 FROM attachment_attempts a
        JOIN attachment_batches b ON b.id=a.batch_id
        JOIN attachment_batch_receipts r ON r.attempt_id=a.id AND r.outcome='requeued'
        WHERE a.ingress_id=OLD.id AND a.state='active'
          AND b.state='cleanup_pending'
    )
) AND NOT (
    OLD.state='publishing' AND NEW.state='failed'
    AND NEW.attempt_count=OLD.attempt_count
    AND NEW.failure_count=OLD.failure_count+1 AND NEW.failure_count=3
    AND NEW.failure_receipt IS NOT NULL
    AND EXISTS (
        SELECT 1 FROM attachment_attempts a
        JOIN attachment_batches b ON b.id=a.batch_id
        JOIN attachment_batch_receipts r ON r.attempt_id=a.id
          AND r.outcome='failed' AND r.receipt=NEW.failure_receipt
        WHERE a.ingress_id=OLD.id AND a.state='active'
          AND b.state='cleanup_pending'
    )
) AND NOT (
    OLD.state='publishing' AND NEW.state='consumed'
    AND NEW.attempt_count=OLD.attempt_count
    AND NEW.failure_count=OLD.failure_count
    AND NEW.failure_receipt IS OLD.failure_receipt
    AND EXISTS (
        SELECT 1 FROM attachment_attempts a
        JOIN attachment_batches b ON b.id=a.batch_id
        JOIN attachment_batch_receipts r
          ON r.attempt_id=a.id AND r.batch_id=a.batch_id
        WHERE a.ingress_id=OLD.id AND a.state='active'
          AND a.generation=OLD.attempt_count
          AND b.state='consumed' AND r.outcome='consumed'
          AND r.ingress_id=OLD.id
    )
)
BEGIN SELECT RAISE(ABORT, 'attachment ingress transition lacks its exact reservation or terminal receipt'); END;

CREATE TRIGGER attachment_attempts_cleanup_requires_receipt BEFORE UPDATE OF state ON attachment_attempts
WHEN NEW.state='cleanup_pending' AND OLD.state<>NEW.state AND NOT EXISTS (
    SELECT 1 FROM attachment_batch_receipts r
    JOIN attachment_batches b ON b.id=OLD.batch_id
    JOIN attachment_ingresses i ON i.id=OLD.ingress_id
    WHERE r.attempt_id=OLD.id AND r.batch_id=OLD.batch_id
      AND b.state='cleanup_pending'
      AND ((r.outcome='requeued' AND i.state='queued')
        OR (r.outcome='failed' AND i.state='failed' AND i.failure_receipt=r.receipt)
        OR (r.outcome='consumed' AND i.state='consumed'))
)
BEGIN SELECT RAISE(ABORT, 'attachment attempt cleanup requires its exact durable terminal'); END;

CREATE TABLE board_operations (
    operation_id TEXT PRIMARY KEY CHECK (length(operation_id) BETWEEN 1 AND 128),
    task_id INTEGER NOT NULL REFERENCES tasks(id),
    action TEXT NOT NULL CHECK (action IN ('create','edit','revise','block','unblock','resume','cancel','allow-corrections')),
    actor TEXT NOT NULL,
    request TEXT NOT NULL CHECK (json_valid(request)),
    previous_values TEXT NOT NULL CHECK (json_valid(previous_values)),
    result TEXT NOT NULL CHECK (json_valid(result)),
    reason TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE requirements_revisions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    operation_id TEXT NOT NULL UNIQUE,
    task_id INTEGER NOT NULL REFERENCES tasks(id),
    expected_version INTEGER NOT NULL CHECK (expected_version >= 1),
    requirements TEXT NOT NULL,
    previous_requirements TEXT NOT NULL,
    previous_requirements_version INTEGER NOT NULL DEFAULT 1 CHECK (previous_requirements_version >= 1),
    reason TEXT NOT NULL CHECK (length(trim(reason)) > 0),
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','applied','cancelled','failed')),
    actor TEXT NOT NULL,
    requested_at TEXT NOT NULL DEFAULT (datetime('now')),
    applied_at TEXT,
    failure_reason TEXT,
    CHECK ((state='applied') = (applied_at IS NOT NULL))
);
CREATE UNIQUE INDEX requirements_one_pending ON requirements_revisions(task_id) WHERE state='pending';

CREATE TABLE requirements_evidence (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    revision_id INTEGER NOT NULL REFERENCES requirements_revisions(id),
    task_id INTEGER NOT NULL REFERENCES tasks(id),
    requirements_version INTEGER NOT NULL,
    status TEXT NOT NULL,
    completion_sha TEXT,
    verified_sha TEXT,
    correction_count INTEGER NOT NULL,
    delivery TEXT CHECK (delivery IS NULL OR json_valid(delivery)),
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE run_requirements (
    run_id TEXT NOT NULL REFERENCES runs(id),
    task_id INTEGER NOT NULL REFERENCES tasks(id),
    requirements_version INTEGER NOT NULL,
    revision_id INTEGER REFERENCES requirements_revisions(id),
    requirements TEXT NOT NULL,
    PRIMARY KEY (run_id,task_id)
);

CREATE TRIGGER runs_capture_requirements AFTER INSERT ON runs
BEGIN
    INSERT INTO run_requirements(run_id,task_id,requirements_version,revision_id,requirements)
    SELECT NEW.id,id,requirements_version,
      (SELECT MAX(r.id) FROM requirements_revisions r WHERE r.task_id=t.id AND r.state='applied'),
      COALESCE(description,'') FROM tasks t WHERE id=NEW.subject
      OR id IN (SELECT value FROM json_each(COALESCE(NEW.pool_snapshot,'[]')));
END;

CREATE TRIGGER tasks_record_version AFTER UPDATE ON tasks
WHEN NEW.record_version=OLD.record_version AND (
    NEW.title IS NOT OLD.title OR NEW.description IS NOT OLD.description OR NEW.priority<>OLD.priority
    OR NEW.requirements_version<>OLD.requirements_version
    OR NEW.status<>OLD.status OR NEW.blocked<>OLD.blocked OR NEW.blocked_reason IS NOT OLD.blocked_reason
    OR NEW.blocked_kind IS NOT OLD.blocked_kind
    OR NEW.decision IS NOT OLD.decision OR NEW.archived<>OLD.archived OR NEW.dispatch_retries<>OLD.dispatch_retries
    OR NEW.correction_count<>OLD.correction_count OR NEW.completion_sha IS NOT OLD.completion_sha
    OR NEW.allowance_condition IS NOT OLD.allowance_condition
    OR NEW.source_version IS NOT OLD.source_version)
BEGIN
    UPDATE tasks SET record_version=record_version+1 WHERE id=NEW.id;
END;

CREATE TRIGGER tasks_version_never_decreases BEFORE UPDATE OF record_version ON tasks
WHEN NEW.record_version<OLD.record_version
BEGIN SELECT RAISE(ABORT, 'the record version must preserve its order'); END;

CREATE TRIGGER requirements_revision_identity BEFORE UPDATE ON requirements_revisions
WHEN NEW.operation_id<>OLD.operation_id OR NEW.task_id<>OLD.task_id OR NEW.expected_version<>OLD.expected_version
 OR NEW.previous_requirements_version<>OLD.previous_requirements_version
 OR NEW.requirements<>OLD.requirements OR NEW.previous_requirements<>OLD.previous_requirements
 OR NEW.reason<>OLD.reason OR NEW.actor<>OLD.actor OR NEW.requested_at<>OLD.requested_at
 OR OLD.state<>'pending'
BEGIN SELECT RAISE(ABORT, 'the requirements revision preserves its request and terminal result'); END;

CREATE TRIGGER tasks_requirements_revision BEFORE UPDATE OF requirements_version ON tasks
WHEN NEW.requirements_version<>OLD.requirements_version AND (
 NEW.requirements_version<>OLD.requirements_version+1 OR OLD.decision IS NOT NULL OR OLD.archived=1
 OR NOT EXISTS (SELECT 1 FROM requirements_revisions r WHERE r.task_id=OLD.id AND r.state='pending'
     AND r.requirements=COALESCE(NEW.description,'')))
BEGIN SELECT RAISE(ABORT, 'the effective requirements require an accepted revision'); END;

CREATE TRIGGER tasks_revision_cancellation AFTER UPDATE OF decision ON tasks
WHEN NEW.decision IN ('cancelled','rejected') AND OLD.decision IS NULL
BEGIN
 UPDATE requirements_revisions SET state='cancelled',failure_reason='The record reached a final decision.'
 WHERE state='pending' AND task_id=NEW.id;
END;

CREATE TRIGGER tasks_approval_revision_fence BEFORE UPDATE OF decision ON tasks
WHEN NEW.decision='approved' AND (
 EXISTS(SELECT 1 FROM requirements_revisions r WHERE r.state='pending' AND r.task_id=NEW.id)
 OR NEW.review_slot_held=0)
BEGIN SELECT RAISE(ABORT, 'approval requires the effective requirements and a current packet'); END;

CREATE TRIGGER board_operations_immutable BEFORE UPDATE ON board_operations
BEGIN SELECT RAISE(ABORT, 'the accepted operation preserves its receipt'); END;
CREATE TRIGGER board_operations_no_delete
BEFORE DELETE ON board_operations
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='board_operations' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'the accepted operation preserves its history'); END;
CREATE TRIGGER requirements_revisions_no_delete
BEFORE DELETE ON requirements_revisions
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='requirements_revisions' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'the requirements revision preserves its history'); END;
CREATE TRIGGER requirements_evidence_immutable BEFORE UPDATE ON requirements_evidence
BEGIN SELECT RAISE(ABORT, 'the previous evidence preserves its history'); END;
CREATE TRIGGER requirements_evidence_no_delete
BEFORE DELETE ON requirements_evidence
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='requirements_evidence' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'the previous evidence preserves its history'); END;
CREATE TRIGGER run_requirements_immutable BEFORE UPDATE ON run_requirements
BEGIN SELECT RAISE(ABORT, 'the run preserves its assigned requirements'); END;
CREATE TRIGGER run_requirements_no_delete
BEFORE DELETE ON run_requirements
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='run_requirements' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'the run preserves its assigned requirements'); END;

CREATE TRIGGER tasks_requirements_application_evidence BEFORE UPDATE OF requirements_version ON tasks
WHEN NEW.requirements_version<>OLD.requirements_version AND (
 NEW.completion_sha IS NOT NULL OR NEW.source_version IS NOT NULL OR NEW.correction_count<>0
 OR NOT EXISTS(SELECT 1 FROM requirements_evidence e JOIN requirements_revisions r ON r.id=e.revision_id
     WHERE r.task_id=OLD.id AND r.state='pending' AND e.requirements_version=OLD.requirements_version
       AND e.status=OLD.status AND e.completion_sha IS OLD.completion_sha AND e.verified_sha IS (SELECT result_commit FROM task_versions WHERE id=OLD.source_version))
 OR EXISTS(SELECT 1 FROM leases l JOIN run_requirements rr ON rr.run_id=l.run_id
     WHERE rr.task_id=OLD.id)
 OR EXISTS(SELECT 1 FROM repository_executions x JOIN run_requirements rr ON rr.run_id=x.run_id
     WHERE x.ended_at IS NULL AND x.operation<>'cleanup'
       AND (rr.task_id=OLD.id))
 OR EXISTS(SELECT 1 FROM version_submissions s WHERE s.captured_version IS NULL
     AND (s.task_id=OLD.id)))
BEGIN SELECT RAISE(ABORT, 'the requirements revision requires the previous result and a complete run lifecycle'); END;

CREATE TABLE diagnostic_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE CHECK(length(event_id) = 32 AND event_id NOT GLOB '*[^0-9a-f]*'),
    source TEXT NOT NULL CHECK(source IN ('resident','runner','agent','operator','mc')),
    request_sent INTEGER NOT NULL DEFAULT 0 CHECK (request_sent IN (0,1)),
    operation TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK(outcome IN ('started','ready','failed','resumed','cancelled','succeeded')),
    connection_id TEXT NOT NULL DEFAULT '',
    authorization_id TEXT NOT NULL DEFAULT '',
    check_id TEXT NOT NULL DEFAULT '',
    profile_id TEXT NOT NULL DEFAULT '',
    profile_revision TEXT NOT NULL DEFAULT '',
    project_id TEXT REFERENCES projects(id),
    run_id TEXT REFERENCES runs(id),
    reply_attempt_id TEXT REFERENCES homie_reply_attempts(attempt_id),
    observed_at TEXT NOT NULL,
    recorded_at TEXT NOT NULL DEFAULT (datetime('now')),
    http_status INTEGER CHECK(http_status BETWEEN 100 AND 599),
    provider_error_code TEXT NOT NULL DEFAULT '' CHECK(length(provider_error_code) <= 64),
    network_error TEXT NOT NULL DEFAULT '' CHECK(network_error IN ('','connect','reset','timeout','tls','other')),
    condition TEXT NOT NULL DEFAULT '' CHECK(length(condition) <= 2048),
    evidence TEXT NOT NULL DEFAULT '' CHECK(length(evidence) <= 8192),
    state_before TEXT NOT NULL DEFAULT '',
    state_after TEXT NOT NULL DEFAULT '',
    count_before INTEGER NOT NULL DEFAULT 0,
    count_after INTEGER NOT NULL DEFAULT 0,
    submission TEXT NOT NULL
);
CREATE INDEX diagnostic_events_connection ON diagnostic_events(connection_id, id);
CREATE INDEX diagnostic_events_run ON diagnostic_events(run_id, id);
CREATE INDEX diagnostic_events_reply ON diagnostic_events(reply_attempt_id, id);
CREATE TRIGGER diagnostic_events_immutable BEFORE UPDATE ON diagnostic_events BEGIN
    SELECT RAISE(ABORT, 'diagnostic history is immutable');
END;
CREATE TRIGGER diagnostic_events_retained
BEFORE DELETE ON diagnostic_events
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='diagnostic_events' AND row_id=OLD.rowid)
BEGIN
    SELECT RAISE(ABORT, 'diagnostic history is retained');
END;

CREATE TABLE connection_health (
    connection_id TEXT NOT NULL,
    operation TEXT NOT NULL CHECK(operation IN ('token_refresh','connection_access','model_access')),
    profile_id TEXT NOT NULL DEFAULT '',
    authorization_id TEXT NOT NULL,
    profile_revision TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'unknown' CHECK(state IN ('unknown','ready','retry_wait','stopped')),
    revision INTEGER NOT NULL DEFAULT 0 CHECK(revision >= 0),
    check_id TEXT NOT NULL DEFAULT '',
    check_sequence INTEGER NOT NULL DEFAULT 0,
    model_refresh_used INTEGER NOT NULL DEFAULT 0 CHECK(model_refresh_used IN (0,1)),
    failure_count INTEGER NOT NULL DEFAULT 0 CHECK(failure_count BETWEEN 0 AND 3),
    first_failure_at TEXT,
    latest_failure_at TEXT,
    next_retry_at TEXT,
    request_deadline TEXT,
    verified_at TEXT,
    expires_at TEXT,
    latest_diagnostic_id INTEGER REFERENCES diagnostic_events(id),
    PRIMARY KEY(connection_id, operation, profile_id),
    CHECK((operation = 'model_access') = (profile_id <> ''))
);

CREATE TABLE homie_reply_attempts (
    attempt_id TEXT PRIMARY KEY CHECK(length(attempt_id) = 32 AND attempt_id NOT GLOB '*[^0-9a-f]*'),
    session_id TEXT NOT NULL REFERENCES homie_sessions(id),
    message_id INTEGER NOT NULL REFERENCES conversation_messages(id),
    launch_id TEXT NOT NULL DEFAULT '',
    connection_id TEXT NOT NULL DEFAULT '',
    authorization_id TEXT NOT NULL DEFAULT '',
    connection_revision INTEGER NOT NULL DEFAULT 0,
    profile_id TEXT NOT NULL DEFAULT '',
    profile_revision TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL CHECK(state IN ('authorized','active','succeeded','failed','cancelled')),
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    ended_at TEXT,
    latest_diagnostic_id INTEGER REFERENCES diagnostic_events(id),
    retry_key TEXT UNIQUE,
    CHECK((state IN ('succeeded','failed','cancelled')) = (ended_at IS NOT NULL))
);
CREATE UNIQUE INDEX homie_reply_active ON homie_reply_attempts(message_id) WHERE ended_at IS NULL;
CREATE INDEX homie_reply_message ON homie_reply_attempts(message_id, created_at);
CREATE TRIGGER homie_reply_terminal BEFORE UPDATE ON homie_reply_attempts
WHEN OLD.ended_at IS NOT NULL BEGIN
    SELECT RAISE(ABORT, 'a reply attempt retains its terminal result');
END;

CREATE TRIGGER runs_connection_identity_immutable BEFORE UPDATE ON runs
WHEN OLD.profile_id<>'' AND (NEW.connection_id<>OLD.connection_id OR NEW.authorization_id<>OLD.authorization_id
 OR NEW.connection_revision<>OLD.connection_revision OR NEW.profile_id<>OLD.profile_id OR NEW.profile_revision<>OLD.profile_revision)
BEGIN SELECT RAISE(ABORT, 'the run connection identity is immutable'); END;

CREATE TRIGGER homie_reply_attempt_message BEFORE INSERT ON homie_reply_attempts
WHEN NOT EXISTS(SELECT 1 FROM conversation_messages WHERE id=NEW.message_id AND session_id=NEW.session_id AND direction='inbound' AND completed_at IS NULL)
BEGIN SELECT RAISE(ABORT, 'a reply attempt requires an unanswered message in its session'); END;

CREATE TRIGGER homie_reply_attempt_identity BEFORE UPDATE ON homie_reply_attempts
WHEN NEW.attempt_id<>OLD.attempt_id OR NEW.session_id<>OLD.session_id OR NEW.message_id<>OLD.message_id
 OR NEW.retry_key IS NOT OLD.retry_key OR NEW.created_at<>OLD.created_at
 OR (OLD.state<>'authorized' AND (NEW.launch_id<>OLD.launch_id OR NEW.connection_id<>OLD.connection_id
 OR NEW.authorization_id<>OLD.authorization_id OR NEW.connection_revision<>OLD.connection_revision
 OR NEW.profile_id<>OLD.profile_id OR NEW.profile_revision<>OLD.profile_revision))
BEGIN SELECT RAISE(ABORT, 'the reply attempt identity is immutable'); END;

CREATE TRIGGER homie_reply_attempt_transition BEFORE UPDATE OF state ON homie_reply_attempts
WHEN NEW.state<>OLD.state AND NOT (
 (OLD.state='authorized' AND NEW.state IN ('active','failed','cancelled')) OR
 (OLD.state='active' AND NEW.state IN ('succeeded','failed','cancelled')))
BEGIN SELECT RAISE(ABORT, 'the reply attempt transition is invalid'); END;

-- Capture records remain outside the pipeline control model.
CREATE TABLE evaluation_captures (
    run_id TEXT PRIMARY KEY REFERENCES runs(id),
    project TEXT NOT NULL REFERENCES projects(id),
    task_id INTEGER NOT NULL REFERENCES tasks(id),
    version_id INTEGER NOT NULL REFERENCES task_versions(id),
    enabled INTEGER NOT NULL CHECK (enabled IN (0,1)),
    case_id TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','complete','failed')),
    error TEXT NOT NULL DEFAULT '',
    packet_id INTEGER UNIQUE REFERENCES review_versions(id),
    report_sha256 TEXT,
    captured_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TRIGGER evaluation_capture_identity BEFORE UPDATE ON evaluation_captures
WHEN NEW.run_id IS NOT OLD.run_id OR NEW.project IS NOT OLD.project OR NEW.task_id IS NOT OLD.task_id
 OR NEW.version_id IS NOT OLD.version_id OR NEW.enabled IS NOT OLD.enabled OR NEW.case_id IS NOT OLD.case_id
 OR (OLD.packet_id IS NOT NULL AND NEW.packet_id IS NOT OLD.packet_id)
 OR (OLD.report_sha256 IS NOT NULL AND NEW.report_sha256 IS NOT OLD.report_sha256)
BEGIN SELECT RAISE(ABORT, 'The capture identity is immutable.'); END;

CREATE TABLE proposal_decisions (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 project TEXT NOT NULL REFERENCES projects(id),
 run_id TEXT UNIQUE REFERENCES runs(id),
 question TEXT NOT NULL CHECK(length(trim(question))>0),
 created_at TEXT NOT NULL DEFAULT(datetime('now')),
 resolved_at TEXT,
 resolution TEXT,
 CHECK((resolved_at IS NULL)=(resolution IS NULL))
);
CREATE UNIQUE INDEX proposal_decisions_open ON proposal_decisions(project) WHERE resolved_at IS NULL;
CREATE TABLE proposal_decision_tasks (
 decision_id INTEGER NOT NULL REFERENCES proposal_decisions(id),
 task_id INTEGER NOT NULL REFERENCES tasks(id),
 owns_block INTEGER NOT NULL CHECK(owns_block IN (0,1)),
 PRIMARY KEY(decision_id,task_id)
);
CREATE TRIGGER proposal_decision_preserve_independent_block
AFTER UPDATE OF blocked_reason ON tasks
WHEN NEW.blocked=1 AND NEW.blocked_reason IS NOT OLD.blocked_reason
BEGIN
 UPDATE proposal_decision_tasks SET owns_block=0 WHERE task_id=NEW.id AND decision_id IN
 (SELECT id FROM proposal_decisions WHERE resolved_at IS NULL AND question IS NOT NEW.blocked_reason);
END;
CREATE TRIGGER proposal_decision_requires_resolution
BEFORE UPDATE OF blocked ON tasks
WHEN NEW.blocked=0 AND EXISTS(SELECT 1 FROM proposal_decision_tasks a JOIN proposal_decisions d ON d.id=a.decision_id
 WHERE a.task_id=NEW.id AND d.resolved_at IS NULL)
BEGIN SELECT RAISE(ABORT,'Resolve the shared Project proposal decision first.'); END;
CREATE TRIGGER run_agent_start_preserved
BEFORE UPDATE OF agent_started_at ON runs
WHEN OLD.agent_started_at IS NOT NULL AND NEW.agent_started_at IS NOT OLD.agent_started_at
BEGIN SELECT RAISE(ABORT,'The run preserves its agent process start.'); END;

CREATE TRIGGER proposal_decision_identity
BEFORE UPDATE ON proposal_decisions
WHEN NEW.id<>OLD.id OR NEW.project<>OLD.project OR NEW.run_id IS NOT OLD.run_id
 OR NEW.question<>OLD.question OR NEW.created_at<>OLD.created_at
 OR (OLD.resolved_at IS NOT NULL AND (NEW.resolved_at IS NOT OLD.resolved_at OR NEW.resolution IS NOT OLD.resolution))
BEGIN SELECT RAISE(ABORT,'The proposal decision preserves its identity and resolution.'); END;
CREATE TRIGGER proposal_decision_task_project
BEFORE INSERT ON proposal_decision_tasks
WHEN NOT EXISTS(SELECT 1 FROM tasks t JOIN proposal_decisions d ON d.project=t.project
 WHERE t.id=NEW.task_id AND d.id=NEW.decision_id)
BEGIN SELECT RAISE(ABORT,'The proposal decision and task must belong to the same Project.'); END;
CREATE TRIGGER run_proposal_assignment_preserved
BEFORE UPDATE OF pool_snapshot ON runs
WHEN NEW.pool_snapshot IS NOT OLD.pool_snapshot
BEGIN SELECT RAISE(ABORT,'The run preserves its proposal assignment.'); END;

-- The accepted commit that the host observed for each Project.
CREATE TABLE accepted_commits (
    project         TEXT PRIMARY KEY REFERENCES projects(id),
    accepted_commit TEXT NOT NULL CHECK (length(accepted_commit) IN (40, 64)
                    AND accepted_commit NOT GLOB '*[^0-9a-f]*'),
    observed_at     TEXT NOT NULL
);

-- One wiki update of one Project. Its run is a Scribe run with no task and no lease.
CREATE TABLE wiki_updates (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    project             TEXT NOT NULL REFERENCES projects(id),
    run_id              TEXT NOT NULL UNIQUE REFERENCES runs(id),
    start_commit        TEXT NOT NULL CHECK (length(start_commit) IN (40, 64)
                        AND start_commit NOT GLOB '*[^0-9a-f]*'),
    wiki_commit         TEXT CHECK (wiki_commit IS NULL OR (length(wiki_commit) IN (40, 64)
                        AND wiki_commit NOT GLOB '*[^0-9a-f]*')),
    started_at          TEXT NOT NULL,
    ended_at            TEXT,
    result              TEXT CHECK (result IN ('succeeded', 'failed')),
    cause               TEXT,
    -- The interval rule counts each wiki update with counted = 1.
    counted             INTEGER NOT NULL DEFAULT 1 CHECK (counted IN (0, 1)),
    -- The highest created_seq of the run of a completed merge of a task into main at the start.
    merge_marker        INTEGER NOT NULL DEFAULT 0 CHECK (merge_marker >= 0),
    -- The liveness facts of the run. The names are the names of the leases table.
    boot_id             TEXT NOT NULL,
    acquired_uptime_ms  INTEGER NOT NULL,
    last_heartbeat_at   TEXT,
    heartbeat_uptime_ms INTEGER,
    deadline_uptime_ms  INTEGER NOT NULL,
    -- The time at which mc recorded that it owes no container stop for the run.
    stopped_at          TEXT,
    -- The merge facts. merge_after holds the end of the destination quiet interval.
    merge_after         TEXT,
    destination_commit  TEXT,
    merged_commit       TEXT,
    CHECK ((ended_at IS NULL) = (result IS NULL)),
    CHECK ((result IS 'failed') = (cause IS NOT NULL)),
    CHECK (cause IS NULL OR length(trim(cause)) BETWEEN 1 AND 2048),
    CHECK (counted = 1 OR result IS 'failed'),
    CHECK ((last_heartbeat_at IS NULL) = (heartbeat_uptime_ms IS NULL)),
    CHECK (result IS NOT 'succeeded' OR wiki_commit IS NOT NULL),
    CHECK ((destination_commit IS NULL) = (merged_commit IS NULL)),
    CHECK (merged_commit IS NULL OR (result IS 'succeeded' AND wiki_commit IS NOT start_commit)),
    CHECK (result IS NOT 'succeeded' OR merged_commit IS NOT NULL OR wiki_commit IS start_commit),
    CHECK (merge_after IS NULL OR wiki_commit IS NOT NULL),
    -- An active wiki update with a wiki commit has a wiki change to merge.
    CHECK (wiki_commit IS NOT start_commit OR ended_at IS NOT NULL)
);

-- Invariant: each Project has a maximum of one active wiki update.
CREATE UNIQUE INDEX wiki_updates_active ON wiki_updates(project) WHERE ended_at IS NULL;

CREATE INDEX wiki_updates_project ON wiki_updates(project, id);

CREATE INDEX wiki_updates_unstopped ON wiki_updates(project, id) WHERE stopped_at IS NULL;

CREATE TRIGGER wiki_updates_assigned_run BEFORE INSERT ON wiki_updates
WHEN NEW.ended_at IS NOT NULL OR NEW.wiki_commit IS NOT NULL OR NEW.last_heartbeat_at IS NOT NULL
  OR NEW.stopped_at IS NOT NULL OR NEW.merge_after IS NOT NULL OR NEW.merged_commit IS NOT NULL
  OR NEW.counted <> 1
  OR NOT EXISTS (SELECT 1 FROM runs r WHERE r.id=NEW.run_id AND r.tier='pipeline' AND r.role='scribe'
      AND r.project=NEW.project AND r.subject IS NULL AND r.binding IS NOT 'mechanical'
      AND r.ended_at IS NULL AND r.outcome IS NULL)
  OR EXISTS (SELECT 1 FROM leases l WHERE l.run_id=NEW.run_id)
BEGIN SELECT RAISE(ABORT, 'a wiki update requires its own active Scribe run with no task and no lease'); END;

-- The run of a wiki update ends before the wiki update ends and before the stop confirmation.
CREATE TRIGGER wiki_updates_end_after_run BEFORE UPDATE OF ended_at, stopped_at, wiki_commit ON wiki_updates
WHEN ((NEW.ended_at IS NOT NULL AND OLD.ended_at IS NULL) OR (NEW.stopped_at IS NOT NULL AND OLD.stopped_at IS NULL)
      OR (NEW.wiki_commit IS NOT NULL AND OLD.wiki_commit IS NULL))
  AND EXISTS (SELECT 1 FROM runs r WHERE r.id=NEW.run_id AND r.ended_at IS NULL)
BEGIN SELECT RAISE(ABORT, 'a wiki update ends after its run'); END;

CREATE TRIGGER wiki_updates_preserved BEFORE UPDATE ON wiki_updates
WHEN NEW.id IS NOT OLD.id OR NEW.project IS NOT OLD.project OR NEW.run_id IS NOT OLD.run_id
  OR NEW.start_commit IS NOT OLD.start_commit OR NEW.started_at IS NOT OLD.started_at
  OR NEW.merge_marker IS NOT OLD.merge_marker OR NEW.boot_id IS NOT OLD.boot_id
  OR NEW.acquired_uptime_ms IS NOT OLD.acquired_uptime_ms
  OR NEW.deadline_uptime_ms IS NOT OLD.deadline_uptime_ms
  OR (OLD.wiki_commit IS NOT NULL AND NEW.wiki_commit IS NOT OLD.wiki_commit)
  OR (OLD.stopped_at IS NOT NULL AND NEW.stopped_at IS NOT OLD.stopped_at)
  OR (OLD.ended_at IS NOT NULL AND (NEW.ended_at IS NOT OLD.ended_at OR NEW.result IS NOT OLD.result
      OR NEW.cause IS NOT OLD.cause OR NEW.counted IS NOT OLD.counted
      OR NEW.wiki_commit IS NOT OLD.wiki_commit
      OR NEW.last_heartbeat_at IS NOT OLD.last_heartbeat_at
      OR NEW.heartbeat_uptime_ms IS NOT OLD.heartbeat_uptime_ms
      OR NEW.merge_after IS NOT OLD.merge_after
      OR NEW.destination_commit IS NOT OLD.destination_commit
      OR NEW.merged_commit IS NOT OLD.merged_commit))
BEGIN SELECT RAISE(ABORT, 'the wiki update preserves its assignment and its end'); END;

CREATE TRIGGER wiki_updates_no_delete
BEFORE DELETE ON wiki_updates
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='wiki_updates' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'the wiki update preserves its history'); END;

CREATE TABLE role_customizations (
    project TEXT NOT NULL REFERENCES projects(id),
    role TEXT NOT NULL CHECK (role IN ('strategist(propose)',
        'arbiter', 'worker', 'verifier', 'scribe', 'packager')),
    revision INTEGER NOT NULL CHECK (revision > 0),
    instructions TEXT NOT NULL CHECK (length(CAST(instructions AS BLOB)) <= 65536),
    author TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (project, role, revision)
);
CREATE TRIGGER role_customizations_immutable BEFORE UPDATE ON role_customizations
BEGIN SELECT RAISE(ABORT, 'customization revisions are immutable'); END;
CREATE TRIGGER role_customizations_retained
BEFORE DELETE ON role_customizations
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='role_customizations' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'customization revisions are retained'); END;

CREATE TABLE run_customizations (
    run_id TEXT PRIMARY KEY REFERENCES runs(id),
    project TEXT NOT NULL REFERENCES projects(id),
    role TEXT NOT NULL CHECK (role IN ('strategist(propose)',
        'arbiter', 'worker', 'verifier', 'scribe', 'packager')),
    revision INTEGER,
    standard_directive TEXT NOT NULL,
    standard_digest TEXT NOT NULL,
    FOREIGN KEY (project, role, revision) REFERENCES role_customizations(project, role, revision)
);
CREATE INDEX run_customizations_project_role ON run_customizations(project,role);

CREATE TRIGGER run_customizations_immutable BEFORE UPDATE ON run_customizations
BEGIN SELECT RAISE(ABORT, 'run customizations are immutable'); END;
CREATE TRIGGER run_customizations_retained
BEFORE DELETE ON run_customizations
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='run_customizations' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'run customizations are retained'); END;

CREATE TABLE customization_conflicts (
    run_id TEXT PRIMARY KEY REFERENCES run_customizations(run_id),
    project_title TEXT NOT NULL,
    passage TEXT NOT NULL CHECK (length(trim(passage)) > 0),
    requirement TEXT NOT NULL CHECK (length(trim(requirement)) > 0),
    reason TEXT NOT NULL CHECK (length(trim(reason)) > 0),
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    cleared_at TEXT,
    cleared_by TEXT,
    resolution TEXT,
    CHECK ((cleared_at IS NULL AND cleared_by IS NULL AND resolution IS NULL)
        OR (cleared_at IS NOT NULL AND cleared_by IS NOT NULL AND length(trim(resolution)) > 0))
);
CREATE TRIGGER customization_conflicts_immutable
BEFORE UPDATE OF run_id, project_title, passage, requirement, reason, created_at ON customization_conflicts
BEGIN SELECT RAISE(ABORT, 'conflict reports are immutable'); END;
CREATE TRIGGER customization_conflicts_retained
BEFORE DELETE ON customization_conflicts
WHEN NOT EXISTS (SELECT 1 FROM project_deletion_records
    WHERE table_name='customization_conflicts' AND row_id=OLD.rowid)
BEGIN SELECT RAISE(ABORT, 'conflict reports are retained'); END;

CREATE TRIGGER role_customizations_sequence BEFORE INSERT ON role_customizations
WHEN NEW.revision <> (SELECT COALESCE(MAX(revision),0)+1 FROM role_customizations WHERE project=NEW.project AND role=NEW.role)
BEGIN SELECT RAISE(ABORT, 'customization revisions must be consecutive'); END;

CREATE TRIGGER run_customizations_assigned_run BEFORE INSERT ON run_customizations
WHEN NOT EXISTS (
    SELECT 1 FROM runs r
    WHERE r.id=NEW.run_id AND r.project=NEW.project AND r.tier='pipeline'
      AND r.ended_at IS NULL AND r.outcome IS NULL
      AND (EXISTS (SELECT 1 FROM leases l WHERE l.run_id=r.id)
        OR EXISTS (SELECT 1 FROM wiki_updates w WHERE w.run_id=r.id AND w.ended_at IS NULL))
      AND (r.role=NEW.role OR (r.role='strategist' AND NEW.role='strategist(propose)'))
)
BEGIN SELECT RAISE(ABORT, 'customization input requires its assigned active run'); END;

CREATE TRIGGER customization_conflicts_assigned_run BEFORE INSERT ON customization_conflicts
WHEN NOT EXISTS (
    SELECT 1 FROM runs r
    JOIN run_customizations c ON c.run_id=r.id
    JOIN role_customizations v ON v.project=c.project AND v.role=c.role AND v.revision=c.revision
    WHERE r.id=NEW.run_id AND r.ended_at IS NULL AND r.outcome IS NULL
      AND (EXISTS (SELECT 1 FROM leases l WHERE l.run_id=r.id)
        OR EXISTS (SELECT 1 FROM wiki_updates w WHERE w.run_id=r.id AND w.ended_at IS NULL))
      AND instr(v.instructions,NEW.passage)>0
)
BEGIN SELECT RAISE(ABORT, 'a conflict requires the custom instructions of its active run'); END;

-- The conflict of a wiki update run sets no role block: its row is cleared at the insert.
CREATE TRIGGER customization_conflicts_wiki_update_cleared BEFORE INSERT ON customization_conflicts
WHEN NEW.cleared_at IS NULL AND EXISTS (SELECT 1 FROM wiki_updates w WHERE w.run_id=NEW.run_id)
BEGIN SELECT RAISE(ABORT, 'the conflict of a wiki update run sets no role block'); END;
