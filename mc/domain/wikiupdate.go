package domain

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"mc/dispatch"
)

// The wiki update aggregate: one wiki_updates row for each wiki update, and
// the accepted commit of each Project. The run of a wiki update is a Scribe
// run with no task and no lease, so no function of this file reads or writes
// a lease, a task, or a failure allowance.

// WikiUpdateRow is one wiki_updates row with three facts of its run. A null
// text value is "".
type WikiUpdateRow struct {
	ID                int64
	Project           string
	RunID             string
	StartCommit       string
	WikiCommit        string
	StartedAt         string
	EndedAt           string
	Result            string
	Cause             string
	Counted           bool
	MergeMarker       int64
	BootID            string
	AcquiredUptimeMS  int64
	DeadlineUptimeMS  int64
	LastHeartbeatAt   string
	HeartbeatUptimeMS *int64
	StoppedAt         string
	MergeAfter        string
	DestinationCommit string
	MergedCommit      string
	// RunEndedAt, RunOutcome, and AgentStartedAt come from the runs row.
	RunEndedAt     string
	RunOutcome     string
	AgentStartedAt string
}

// WikiState is the wiki state of one Project with the rows that give it.
type WikiState struct {
	// Kind is the wiki state: current, behind, or update-active.
	Kind           string
	AcceptedCommit string
	// Unstopped is the run of the oldest wiki update with an ended run and no
	// stop confirmation. It is "" for none.
	Unstopped string
	Active    *WikiUpdateRow
	// Last is the last wiki update with an end.
	Last          *WikiUpdateRow
	LastSucceeded *WikiUpdateRow
	LastCounted   *WikiUpdateRow
}

// WikiStart is the start of one wiki update.
type WikiStart struct {
	Project     string
	RunID       string
	StartCommit string
	SessionPath string
	Binding     string
	// HardDeadlineMinutes is the awake time that the run can use.
	HardDeadlineMinutes int
}

// WikiRunEnd is the end of the run of a wiki update. An empty Result leaves
// the wiki update active with WikiCommit, which then waits for its merge.
type WikiRunEnd struct {
	Outcome    string
	EndReason  string
	WikiCommit string
	Result     string
	Cause      string
	NotCounted bool
	// Stopped records that no container of the run exists.
	Stopped bool
}

// WikiEnd is the end of a wiki update.
type WikiEnd struct {
	Result            string
	Cause             string
	WikiCommit        string
	DestinationCommit string
	MergedCommit      string
	// NotCounted excludes a failed wiki update from the interval rule.
	NotCounted bool
}

// AgentRunFence identifies the active agent run that a fence accepted.
type AgentRunFence struct {
	Project string
	// TaskID is the lease subject. It is nil for a run with no task.
	TaskID      *int64
	WikiUpdate  bool
	StartCommit string
}

const wikiCauseLimit = 2048

var errNoActiveWikiUpdate = errors.New("the run has no active wiki update")

// RecordAcceptedCommit stores a host observation of the accepted commit. An
// empty commit and an equal commit write nothing.
func RecordAcceptedCommit(ctx context.Context, q Q, project, commit string) error {
	if commit == "" {
		return nil
	}
	_, err := q.ExecContext(ctx, `INSERT INTO accepted_commits(project,accepted_commit,observed_at) VALUES(?,?,datetime('now'))
		ON CONFLICT(project) DO UPDATE SET accepted_commit=excluded.accepted_commit, observed_at=excluded.observed_at
		WHERE accepted_commits.accepted_commit IS NOT excluded.accepted_commit`, project, commit)
	return err
}

// AcceptedCommit returns the stored accepted commit of the Project, or "".
func AcceptedCommit(ctx context.Context, q Q, project string) (string, error) {
	var commit string
	err := q.QueryRowContext(ctx, `SELECT accepted_commit FROM accepted_commits WHERE project=?`, project).Scan(&commit)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return commit, err
}

// RecordMergedCommit moves the accepted commit to the commit of a merge into
// main, only while the stored value is the destination commit of that merge.
func RecordMergedCommit(ctx context.Context, q Q, project, destination, merged string) error {
	_, err := q.ExecContext(ctx, `UPDATE accepted_commits SET accepted_commit=?, observed_at=datetime('now')
		WHERE project=? AND accepted_commit=?`, merged, project, destination)
	return err
}

// WikiMergeMarker returns the highest run sequence of a completed merge of a
// task into main in the Project, or 0.
func WikiMergeMarker(ctx context.Context, q Q, project string) (int64, error) {
	var marker int64
	err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(r.created_seq),0) FROM merge_operations m JOIN runs r ON r.id=m.run_id
		WHERE m.project=? AND m.operation='apply' AND m.finished=1 AND m.destination_branch='main'
		  AND m.task_id IS NOT NULL AND json_extract(m.result,'$.applied.commit') IS NOT NULL`, project).Scan(&marker)
	return marker, err
}

// StartWikiUpdate inserts the Scribe run and the wiki update of the Project.
// It stamps the VM clock and the hard deadline as a lease claim does, and it
// claims no lease. A Project with an active wiki update gets no row.
func StartWikiUpdate(ctx context.Context, q Q, now time.Time, a WikiStart) error {
	vm, err := ReadVMClock()
	if err != nil {
		return err
	}
	marker, err := WikiMergeMarker(ctx, q, a.Project)
	if err != nil {
		return err
	}
	res, err := q.ExecContext(ctx, `INSERT INTO runs(id,tier,role,project,subject,session_path,binding)
		SELECT ?,'pipeline','scribe',?,NULL,?,NULLIF(?,'')
		WHERE NOT EXISTS (SELECT 1 FROM wiki_updates WHERE project=? AND ended_at IS NULL)`,
		a.RunID, a.Project, a.SessionPath, a.Binding, a.Project)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return Errf(CodeStaleRun, "the Project has an active wiki update")
	}
	deadline := vm.Uptime + time.Duration(a.HardDeadlineMinutes)*time.Minute
	_, err = q.ExecContext(ctx, `INSERT INTO wiki_updates
		(project,run_id,start_commit,started_at,merge_marker,boot_id,acquired_uptime_ms,deadline_uptime_ms)
		VALUES(?,?,?,?,?,?,?,?)`,
		a.Project, a.RunID, a.StartCommit, now.UTC().Format(SpineTime), marker, vm.BootID,
		vm.Uptime.Milliseconds(), deadline.Milliseconds())
	return err
}

// wikiRunFence reads the fence of an active wiki update run. The result is
// false for each other run.
func wikiRunFence(ctx context.Context, q Q, run string) (AgentRunFence, bool, error) {
	fence := AgentRunFence{WikiUpdate: true}
	err := q.QueryRowContext(ctx, `SELECT w.project, w.start_commit FROM wiki_updates w JOIN runs r ON r.id=w.run_id
		WHERE w.run_id=? AND w.ended_at IS NULL AND r.ended_at IS NULL AND r.outcome IS NULL`, run).
		Scan(&fence.Project, &fence.StartCommit)
	if err == sql.ErrNoRows {
		return AgentRunFence{}, false, nil
	}
	if err != nil {
		return AgentRunFence{}, false, err
	}
	return fence, true, nil
}

// FenceAgentRun accepts an active run that holds a live lease or that is the
// run of an active wiki update. Each other run is stale.
func FenceAgentRun(ctx context.Context, q Q, run string) (AgentRunFence, error) {
	fence, wiki, err := wikiRunFence(ctx, q, run)
	if err != nil || wiki {
		return fence, err
	}
	subject, err := Fence(ctx, q, run)
	if err != nil {
		return AgentRunFence{}, err
	}
	fence.TaskID = subject
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(project,'') FROM runs WHERE id=?`, run).Scan(&fence.Project); err != nil {
		return AgentRunFence{}, err
	}
	return fence, nil
}

// HeartbeatAgentRun stamps the heartbeat time and the VM uptime of an active
// agent run: on the wiki update for its run, and on the lease for each other
// run. It returns the stamp.
func HeartbeatAgentRun(ctx context.Context, q Q, runID string) (string, error) {
	_, wiki, err := wikiRunFence(ctx, q, runID)
	if err != nil {
		return "", err
	}
	if !wiki {
		return Heartbeat(ctx, q, runID)
	}
	vm, err := ReadVMClock()
	if err != nil {
		return "", err
	}
	var stamp string
	if err := q.QueryRowContext(ctx, `SELECT datetime('now')`).Scan(&stamp); err != nil {
		return "", err
	}
	_, err = q.ExecContext(ctx, `UPDATE wiki_updates SET last_heartbeat_at=?, heartbeat_uptime_ms=?
		WHERE run_id=? AND ended_at IS NULL`, stamp, vm.Uptime.Milliseconds(), runID)
	return stamp, err
}

// EndWikiRun ends the active run of a wiki update. The same call ends the
// wiki update, or stores the wiki commit that waits for its merge. It returns
// false and writes nothing for a run that has an end. A run with no active
// wiki update is an error, and the caller rolls back. It releases no lease
// and restores no allowance.
func EndWikiRun(ctx context.Context, q Q, run string, end WikiRunEnd) (bool, error) {
	res, err := q.ExecContext(ctx, `UPDATE runs SET ended_at=datetime('now'), outcome=?, end_reason=NULLIF(?,'')
		WHERE id=? AND ended_at IS NULL AND outcome IS NULL`, end.Outcome, end.EndReason, run)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	if end.Result == "" {
		if end.WikiCommit == "" {
			return false, errors.New("the end of a wiki update run with no result requires a wiki commit")
		}
		res, err := q.ExecContext(ctx, `UPDATE wiki_updates SET wiki_commit=? WHERE run_id=? AND ended_at IS NULL`,
			end.WikiCommit, run)
		if err != nil {
			return false, err
		}
		if n, err := res.RowsAffected(); err != nil {
			return false, err
		} else if n == 0 {
			return false, errNoActiveWikiUpdate
		}
	} else {
		ended, err := endWikiUpdate(ctx, q, run, WikiEnd{Result: end.Result, Cause: end.Cause,
			WikiCommit: end.WikiCommit, NotCounted: end.NotCounted})
		if err != nil {
			return false, err
		}
		if !ended {
			return false, errNoActiveWikiUpdate
		}
	}
	if end.Stopped {
		if _, err := q.ExecContext(ctx, `UPDATE wiki_updates SET stopped_at=datetime('now')
			WHERE run_id=? AND stopped_at IS NULL`, run); err != nil {
			return false, err
		}
	}
	if end.Outcome == "completed" {
		if err := ClearNetworkRetry(ctx, q, run); err != nil {
			return false, err
		}
	}
	return true, nil
}

// EndWikiUpdate ends the active wiki update of the run and writes its end
// record. It writes nothing for a wiki update that has an end. The stored
// cause of a failure has 1 to 2048 characters.
func EndWikiUpdate(ctx context.Context, q Q, run string, end WikiEnd) error {
	_, err := endWikiUpdate(ctx, q, run, end)
	return err
}

// endWikiUpdate reports whether it ended a wiki update.
func endWikiUpdate(ctx context.Context, q Q, run string, end WikiEnd) (bool, error) {
	failed := end.Result == "failed"
	cause, counted := "", 1
	if failed {
		cause = end.Cause
		if utf8.RuneCountInString(cause) > wikiCauseLimit {
			cause = string([]rune(cause)[:wikiCauseLimit])
		}
		if cause = strings.TrimSpace(cause); cause == "" {
			cause = "The wiki update failed."
		}
		if end.NotCounted {
			counted = 0
		}
	}
	res, err := q.ExecContext(ctx, `UPDATE wiki_updates SET ended_at=datetime('now'), result=?, cause=NULLIF(?,''), counted=?,
		wiki_commit=COALESCE(wiki_commit, NULLIF(?,'')), destination_commit=NULLIF(?,''), merged_commit=NULLIF(?,'')
		WHERE run_id=? AND ended_at IS NULL`,
		end.Result, cause, counted, end.WikiCommit, end.DestinationCommit, end.MergedCommit, run)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	var detail string
	switch {
	case failed:
		detail = "The wiki update failed. " + cause
	case end.MergedCommit != "":
		detail = "The wiki update succeeded. Culler merged the wiki commit into main."
	default:
		detail = "The wiki update succeeded. The Project wiki needed no change."
	}
	_, err = q.ExecContext(ctx, `INSERT INTO activity(actor,kind,subject,detail,project)
		SELECT 'mc','wiki.update_ended',run_id,?,project FROM wiki_updates WHERE run_id=?`, detail, run)
	return err == nil, err
}

// RecoverWikiUpdate ends the active wiki update of the Project as failed
// when its run has an end and it can get no result: it holds no wiki commit,
// or the spine holds no record of the repository of its run, so the wiki
// commit can get no capture. It returns true when it ended one.
func RecoverWikiUpdate(ctx context.Context, q Q, project string) (bool, error) {
	var run string
	var commit sql.NullString
	err := q.QueryRowContext(ctx, `SELECT w.run_id,w.wiki_commit FROM wiki_updates w JOIN runs r ON r.id=w.run_id
		WHERE w.project=? AND w.ended_at IS NULL AND r.ended_at IS NOT NULL
		  AND (w.wiki_commit IS NULL OR NOT EXISTS (SELECT 1 FROM repository_runs s WHERE s.run_id=w.run_id))`, project).
		Scan(&run, &commit)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	cause := "The Scribe run ended with no result."
	if commit.Valid {
		cause = "Culler has no record of the repository of the Scribe run."
	}
	if err := EndWikiUpdate(ctx, q, run, WikiEnd{Result: "failed", Cause: cause}); err != nil {
		return false, err
	}
	return true, nil
}

// EndArchivedWikiUpdates ends as failed each active wiki update of an
// archived Project whose run has an end. A wiki update with an active
// execution or an unfinished merge authorization stays active.
func EndArchivedWikiUpdates(ctx context.Context, q Q) error {
	rows, err := q.QueryContext(ctx, `SELECT w.run_id FROM wiki_updates w JOIN runs r ON r.id=w.run_id
		JOIN projects p ON p.id=w.project
		WHERE w.ended_at IS NULL AND r.ended_at IS NOT NULL AND p.status='archived'
		  AND NOT EXISTS (SELECT 1 FROM repository_executions x WHERE x.wiki_update=w.run_id AND x.ended_at IS NULL)
		  AND NOT EXISTS (SELECT 1 FROM merge_operations m WHERE m.wiki_update=w.run_id
		      AND m.operation='apply' AND m.finished=0)
		ORDER BY w.id`)
	if err != nil {
		return err
	}
	var runs []string
	for rows.Next() {
		var run string
		if err := rows.Scan(&run); err != nil {
			rows.Close()
			return err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, run := range runs {
		if err := EndWikiUpdate(ctx, q, run, WikiEnd{Result: "failed", Cause: "The Project is archived."}); err != nil {
			return err
		}
	}
	return nil
}

// WikiStateOf loads the wiki state of the Project: the stored accepted
// commit, the active wiki update, and the last wiki update of each kind that
// the due decision and the reads use.
func WikiStateOf(ctx context.Context, q Q, project string) (WikiState, error) {
	var state WikiState
	var err error
	if state.AcceptedCommit, err = AcceptedCommit(ctx, q, project); err != nil {
		return WikiState{}, err
	}
	if state.Active, err = lastWikiUpdate(ctx, q, project, `w.ended_at IS NULL`); err != nil {
		return WikiState{}, err
	}
	if state.Last, err = lastWikiUpdate(ctx, q, project, `w.ended_at IS NOT NULL`); err != nil {
		return WikiState{}, err
	}
	if state.LastSucceeded, err = lastWikiUpdate(ctx, q, project, `w.result='succeeded'`); err != nil {
		return WikiState{}, err
	}
	if state.LastCounted, err = lastWikiUpdate(ctx, q, project, `w.counted=1`); err != nil {
		return WikiState{}, err
	}
	err = q.QueryRowContext(ctx, `SELECT w.run_id FROM wiki_updates w JOIN runs r ON r.id=w.run_id
		WHERE w.project=? AND w.stopped_at IS NULL AND r.ended_at IS NOT NULL ORDER BY w.id LIMIT 1`, project).
		Scan(&state.Unstopped)
	if err != nil && err != sql.ErrNoRows {
		return WikiState{}, err
	}
	state.Kind = dispatch.WikiStateOf(state.AcceptedCommit, state.Active != nil, state.LastSucceeded.Record())
	return state, nil
}

// lastWikiUpdate returns the last wiki update of the Project that matches
// the condition, or nil. The condition is a fixed text of this file.
func lastWikiUpdate(ctx context.Context, q Q, project, where string) (*WikiUpdateRow, error) {
	var row WikiUpdateRow
	var counted int
	var heartbeat sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT w.id, w.project, w.run_id, w.start_commit, COALESCE(w.wiki_commit,''), w.started_at,
		COALESCE(w.ended_at,''), COALESCE(w.result,''), COALESCE(w.cause,''), w.counted, w.merge_marker, w.boot_id,
		w.acquired_uptime_ms, w.deadline_uptime_ms, COALESCE(w.last_heartbeat_at,''), w.heartbeat_uptime_ms,
		COALESCE(w.stopped_at,''), COALESCE(w.merge_after,''), COALESCE(w.destination_commit,''),
		COALESCE(w.merged_commit,''), COALESCE(r.ended_at,''), COALESCE(r.outcome,''), COALESCE(r.agent_started_at,'')
		FROM wiki_updates w JOIN runs r ON r.id=w.run_id
		WHERE w.project=? AND `+where+` ORDER BY w.id DESC LIMIT 1`, project).Scan(
		&row.ID, &row.Project, &row.RunID, &row.StartCommit, &row.WikiCommit, &row.StartedAt,
		&row.EndedAt, &row.Result, &row.Cause, &counted, &row.MergeMarker, &row.BootID,
		&row.AcquiredUptimeMS, &row.DeadlineUptimeMS, &row.LastHeartbeatAt, &heartbeat,
		&row.StoppedAt, &row.MergeAfter, &row.DestinationCommit,
		&row.MergedCommit, &row.RunEndedAt, &row.RunOutcome, &row.AgentStartedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := ParseSpineTime(row.StartedAt); err != nil {
		return nil, err
	}
	row.Counted = counted == 1
	if heartbeat.Valid {
		row.HeartbeatUptimeMS = &heartbeat.Int64
	}
	return &row, nil
}

// Record converts the row to the record of the due decision. It returns nil
// for a nil row. A row that WikiStateOf loaded has a valid start time.
func (r *WikiUpdateRow) Record() *dispatch.WikiUpdate {
	if r == nil {
		return nil
	}
	started, _ := ParseSpineTime(r.StartedAt)
	record := &dispatch.WikiUpdate{
		RunID:             r.RunID,
		StartCommit:       r.StartCommit,
		WikiCommit:        r.WikiCommit,
		StartedAt:         started,
		Result:            r.Result,
		Counted:           r.Counted,
		DestinationCommit: r.DestinationCommit,
		MergedCommit:      r.MergedCommit,
		RunEnded:          r.RunEndedAt != "",
		BootID:            r.BootID,
		AcquiredUptime:    time.Duration(r.AcquiredUptimeMS) * time.Millisecond,
		DeadlineUptime:    time.Duration(r.DeadlineUptimeMS) * time.Millisecond,
	}
	if r.HeartbeatUptimeMS != nil {
		heartbeat := time.Duration(*r.HeartbeatUptimeMS) * time.Millisecond
		record.HeartbeatUptime = &heartbeat
	}
	return record
}
