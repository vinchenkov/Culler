package verbs

import (
	"context"
	"database/sql"
	"time"

	"mc/dispatch"
	"mc/domain"
)

// The operator texts of the wiki reads. The dashboard shows each text unchanged.
const (
	wikiStateTextCurrent = "The Project wiki is current."
	wikiStateTextBehind  = "The Project wiki is behind the accepted commit."
	wikiStateTextActive  = "A wiki update is active."

	wikiStepTextRun     = "The Scribe updates the Project wiki."
	wikiStepTextMerge   = "Culler merges the wiki commit."
	wikiStepTextPaused  = "The Project is paused. Culler merges the wiki commit after the Project is active."
	wikiStepTextReceipt = "Culler checks the result of an interrupted merge of the wiki commit. Each other merge step of the Project waits for this result."

	wikiResultTextMerged     = "Culler merged the wiki commit."
	wikiResultTextNoChange   = "The Project wiki needed no change."
	wikiResultTextFailed     = "The wiki update failed."
	wikiResultTextNotCounted = "The connection check prevented the start of the wiki update."

	wikiNextTextPaused   = "The Project is paused. The next wiki update starts after the Project is active."
	wikiNextTextUnknown  = "Culler cannot read the culler branch of the Project repository."
	wikiNextTextStopping = "Culler stops the container of the last Scribe run. The next wiki update starts after that."
	wikiNextTextDue      = "The next wiki update is due."
)

// The steps of an active wiki update.
const (
	wikiStepRun     = "run"
	wikiStepReceipt = "receipt"
	wikiStepPaused  = "paused"
	wikiStepMerge   = "merge"
)

var wikiStateText = map[string]string{
	dispatch.WikiStateCurrent:      wikiStateTextCurrent,
	dispatch.WikiStateBehind:       wikiStateTextBehind,
	dispatch.WikiStateUpdateActive: wikiStateTextActive,
}

var wikiStepText = map[string]string{
	wikiStepRun:     wikiStepTextRun,
	wikiStepReceipt: wikiStepTextReceipt,
	wikiStepPaused:  wikiStepTextPaused,
	wikiStepMerge:   wikiStepTextMerge,
}

// wikiStep returns the step of an active wiki update. receipt states that the
// merge authorization is unfinished and its execution ended. executing states
// that an execution of a wiki update is active in the Project.
func wikiStep(runEnded, receipt, projectActive, executing bool) string {
	switch {
	case !runEnded:
		return wikiStepRun
	case receipt:
		return wikiStepReceipt
	case !projectActive && !executing:
		return wikiStepPaused
	}
	return wikiStepMerge
}

// wikiResultText returns the result text of a wiki update that has an end.
func wikiResultText(result string, merged, counted bool) string {
	switch {
	case result == "succeeded" && merged:
		return wikiResultTextMerged
	case result == "succeeded":
		return wikiResultTextNoChange
	case counted:
		return wikiResultTextFailed
	}
	return wikiResultTextNotCounted
}

// WikiStateList returns the wiki state of each Project that is not archived,
// in identifier order. It is a pure read.
func WikiStateList(db *sql.DB) (any, error) {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, classify(err)
	}
	defer tx.Rollback()
	states, err := wikiStates(ctx, tx)
	if err != nil {
		return nil, classify(err)
	}
	return map[string]any{"wiki_states": states}, nil
}

func wikiStates(ctx context.Context, q Q) ([]map[string]any, error) {
	now, err := spineClock(ctx, q)
	if err != nil {
		return nil, err
	}
	tun, err := loadSettings(ctx, q)
	if err != nil {
		return nil, err
	}
	interval := time.Duration(tun.wikiUpdateIntervalS) * time.Second
	rows, err := q.QueryContext(ctx, `SELECT id, status FROM projects WHERE status <> 'archived' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	type project struct{ id, status string }
	var projects []project
	for rows.Next() {
		var p project
		if err := rows.Scan(&p.id, &p.status); err != nil {
			rows.Close()
			return nil, err
		}
		projects = append(projects, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	states := make([]map[string]any, 0, len(projects))
	for _, p := range projects {
		state, err := domain.WikiStateOf(ctx, q, p.id)
		if err != nil {
			return nil, err
		}
		projectActive := p.status == "active"
		row := map[string]any{
			"project":         p.id,
			"state":           state.Kind,
			"state_text":      wikiStateText[state.Kind],
			"accepted_commit": nullIfEmpty(state.AcceptedCommit),
			"next_start_at":   nil,
			"next_start_text": nil,
			"active":          nil,
			"last":            nil,
		}
		if state.Kind == dispatch.WikiStateBehind {
			row["next_start_at"], row["next_start_text"] = wikiNextStart(state, projectActive, interval, now)
		}
		if active := state.Active; active != nil {
			rec, err := loadWikiMergeRecords(ctx, q, p.id)
			if err != nil {
				return nil, err
			}
			var repair string
			if u := rec.Update; u != nil {
				repair = u.Repair
			}
			step := wikiStep(active.RunEndedAt != "", wikiReceiptRecovery(rec.Update), projectActive, len(rec.Executions) > 0)
			row["active"] = map[string]any{
				"run_id":            active.RunID,
				"started_at":        active.StartedAt,
				"agent_started_at":  nullIfEmpty(active.AgentStartedAt),
				"last_heartbeat_at": nullIfEmpty(active.LastHeartbeatAt),
				"run_ended_at":      nullIfEmpty(active.RunEndedAt),
				"step":              step,
				"step_text":         wikiStepText[step],
				"repair_text":       nullIfEmpty(repair),
			}
		}
		if last := state.Last; last != nil {
			row["last"] = map[string]any{
				"run_id":        last.RunID,
				"started_at":    last.StartedAt,
				"ended_at":      last.EndedAt,
				"result":        last.Result,
				"result_text":   wikiResultText(last.Result, last.MergedCommit != "", last.Counted),
				"cause_text":    nullIfEmpty(last.Cause),
				"start_commit":  last.StartCommit,
				"wiki_commit":   nullIfEmpty(last.WikiCommit),
				"merged_commit": nullIfEmpty(last.MergedCommit),
			}
		}
		states = append(states, row)
	}
	return states, nil
}

// wikiNextStart returns the earliest start time of the next wiki update of a
// Project whose wiki is behind, or the text of the condition that holds the
// start. One of the two values is nil.
func wikiNextStart(state domain.WikiState, projectActive bool, interval time.Duration, now time.Time) (at, text any) {
	switch {
	case !projectActive:
		return nil, wikiNextTextPaused
	case state.AcceptedCommit == "":
		return nil, wikiNextTextUnknown
	}
	if counted := state.LastCounted.Record(); counted != nil {
		if next := counted.StartedAt.Add(interval); now.Before(next) {
			return next.Format(spineTime), nil
		}
	}
	if state.Unstopped != "" {
		return nil, wikiNextTextStopping
	}
	return nil, wikiNextTextDue
}

// loadRunWikiUpdates returns the wiki_update value of `mc run list` for each
// run of a wiki update. The two step facts apply to an active wiki update only.
func loadRunWikiUpdates(ctx context.Context, q Q) (map[string]any, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT w.run_id, COALESCE(w.result,''), COALESCE(w.cause,''), w.counted, w.merged_commit IS NOT NULL,
		       w.ended_at IS NOT NULL, r.ended_at IS NOT NULL, p.status='active',
		       CASE WHEN w.ended_at IS NULL THEN EXISTS(SELECT 1 FROM merge_operations m JOIN runs a ON a.id=m.run_id
		           WHERE m.wiki_update=w.run_id AND m.operation='apply' AND m.finished=0 AND a.ended_at IS NOT NULL) ELSE 0 END,
		       CASE WHEN w.ended_at IS NULL THEN EXISTS(SELECT 1 FROM repository_executions x
		           WHERE x.project=w.project AND x.wiki_update IS NOT NULL AND x.ended_at IS NULL) ELSE 0 END
		FROM wiki_updates w JOIN runs r ON r.id=w.run_id JOIN projects p ON p.id=w.project`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	updates := map[string]any{}
	for rows.Next() {
		var run, result, cause string
		var counted, merged, ended, runEnded, projectActive, receipt, executing bool
		if err := rows.Scan(&run, &result, &cause, &counted, &merged, &ended, &runEnded, &projectActive, &receipt, &executing); err != nil {
			return nil, err
		}
		update := map[string]any{"result": nullIfEmpty(result), "result_text": nil, "cause_text": nullIfEmpty(cause)}
		switch {
		case ended:
			update["result_text"] = wikiResultText(result, merged, counted)
		case runEnded:
			update["result_text"] = wikiStepText[wikiStep(true, receipt, projectActive, executing)]
		}
		updates[run] = update
	}
	return updates, rows.Err()
}
