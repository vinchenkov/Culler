package dispatch

import "time"

// The wiki states of a Project.
const (
	WikiStateCurrent      = "current"
	WikiStateBehind       = "behind"
	WikiStateUpdateActive = "update-active"
)

// The idle reasons of DecideWiki.
const (
	IdleWikiProjectInactive IdleReason = "wiki-project-inactive"
	IdleWikiUpdateActive    IdleReason = "wiki-update-active"
	IdleWikiCommitUnknown   IdleReason = "wiki-commit-unknown"
	IdleWikiCurrent         IdleReason = "wiki-current"
	IdleWikiInterval        IdleReason = "wiki-interval"
)

// minWikiSpawnGraceS is the minimum first-heartbeat grace of a wiki update run, in seconds.
const minWikiSpawnGraceS = 60

// WikiUpdate is one wiki update with the liveness facts of its run.
type WikiUpdate struct {
	RunID       string
	StartCommit string
	WikiCommit  string
	StartedAt   time.Time
	// Result is "" while the wiki update is active.
	Result            string
	Counted           bool
	DestinationCommit string
	MergedCommit      string
	RunEnded          bool
	BootID            string
	AcquiredUptime    time.Duration
	HeartbeatUptime   *time.Duration
	DeadlineUptime    time.Duration
}

// WikiRecords holds the facts that decide the wiki update of one Project.
type WikiRecords struct {
	Project              string
	EnvironmentCondition string
	// Status is projects.status.
	Status string
	// AcceptedCommit is the observation of this request. It is "" when the request holds none.
	AcceptedCommit string
	Active         *WikiUpdate
	LastSucceeded  *WikiUpdate
	LastCounted    *WikiUpdate
	// Unstopped is the run of the oldest wiki update with an ended run and no stop confirmation.
	// It is "" for none.
	Unstopped string
}

// WikiStateOf derives the wiki state from commit equality only. last is the
// last successful wiki update of the Project.
func WikiStateOf(accepted string, active bool, last *WikiUpdate) string {
	switch {
	case active:
		return WikiStateUpdateActive
	case last == nil || accepted == "":
		return WikiStateBehind
	case last.MergedCommit != "":
		if last.DestinationCommit == last.StartCommit && accepted == last.MergedCommit {
			return WikiStateCurrent
		}
	case last.WikiCommit == last.StartCommit && accepted == last.StartCommit:
		return WikiStateCurrent
	}
	return WikiStateBehind
}

// DecideWiki decides the wiki update of one Project for one tick. It returns
// a stop of a wiki update run, a spawn of a Scribe run with no task, or an
// idle reason. It is pure. No lease, no task, and no allowance is an input.
func DecideWiki(rec WikiRecords, cfg Config, interval time.Duration, clk Clock) Action {
	if run := rec.Active; run != nil && !run.RunEnded {
		if reason, stop := wikiRunStop(rec.Status, *run, cfg, clk); stop {
			return wikiReap(rec.Project, run.RunID, reason)
		}
	}
	// The run of this stop has an end. The stop only removes its container.
	if rec.Unstopped != "" {
		return wikiReap(rec.Project, rec.Unstopped, ReapReported)
	}
	if rec.Status != "active" {
		return idle(IdleWikiProjectInactive)
	}
	if rec.EnvironmentCondition != "" {
		return idle(IdleReason(rec.EnvironmentCondition))
	}
	if rec.Active != nil {
		return idle(IdleWikiUpdateActive)
	}
	if rec.AcceptedCommit == "" {
		return idle(IdleWikiCommitUnknown)
	}
	if WikiStateOf(rec.AcceptedCommit, false, rec.LastSucceeded) == WikiStateCurrent {
		return idle(IdleWikiCurrent)
	}
	if rec.LastCounted != nil && clk.Now.Before(rec.LastCounted.StartedAt.Add(interval)) {
		return idle(IdleWikiInterval)
	}
	return Action{Kind: KindSpawn, Spawn: &Spawn{Project: rec.Project, Role: RoleScribe}}
}

// wikiRunStop applies the stop rules to a wiki update run with no end. An
// archived Project stops its run. Each other rule is a rule of reapable, in
// its order: the liveness facts of the wiki update take the place of the
// lease, and the first-heartbeat grace has its minimum.
func wikiRunStop(status string, run WikiUpdate, cfg Config, clk Clock) (ReapReason, bool) {
	if status == "archived" {
		return ReapInterrupted, true
	}
	cfg.SpawnGraceS = max(cfg.SpawnGraceS, minWikiSpawnGraceS)
	return reapable(Records{}, Lease{
		Held: true, RunID: run.RunID, BootID: run.BootID, AcquiredUptime: run.AcquiredUptime,
		HeartbeatUptime: run.HeartbeatUptime, DeadlineUptime: run.DeadlineUptime,
	}, cfg, clk)
}

// wikiReap requests the stop of the container of a wiki update run. It charges no allowance.
func wikiReap(project, run string, reason ReapReason) Action {
	return Action{Kind: KindReap, Reap: &Reap{Project: project, RunID: run, Reason: reason, StopContainer: true}}
}
