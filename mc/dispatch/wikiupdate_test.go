package dispatch

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var (
	wikiC1 = strings.Repeat("a", 40)
	wikiC2 = strings.Repeat("b", 40)
	wikiC3 = strings.Repeat("c", 40)
	wikiC4 = strings.Repeat("d", 40)
)

const wikiInterval = time.Hour

// wikiClock is the clock at base plus d, in the boot "boot".
func wikiClock(d time.Duration) Clock {
	return Clock{Now: base.Add(d), BootID: "boot", Uptime: up(d)}
}

// liveWikiRun is an active wiki update whose run started one minute before
// base, sent a heartbeat 30 seconds before base, and has its deadline in two hours.
func liveWikiRun() *WikiUpdate {
	return &WikiUpdate{
		RunID: "run-wiki", StartCommit: wikiC1, StartedAt: base.Add(-time.Minute), Counted: true,
		BootID: "boot", AcquiredUptime: up(-time.Minute), HeartbeatUptime: pd(up(-30 * time.Second)),
		DeadlineUptime: up(2 * time.Hour),
	}
}

// unchangedWiki is a successful wiki update with no wiki change at the commit.
func unchangedWiki(commit string, startedAt time.Time) *WikiUpdate {
	return &WikiUpdate{
		RunID: "run-done", StartCommit: commit, WikiCommit: commit, StartedAt: startedAt,
		Result: "succeeded", Counted: true, RunEnded: true,
	}
}

// decideWiki runs the decision twice and requires one well-formed action that
// names the Project and leaves the records unchanged.
func decideWiki(t *testing.T, rec WikiRecords, cfg Config, clk Clock) Action {
	t.Helper()
	before := cloneWikiRecords(rec)
	a := DecideWiki(rec, cfg, wikiInterval, clk)
	if again := DecideWiki(rec, cfg, wikiInterval, clk); !reflect.DeepEqual(a, again) {
		t.Fatalf("equal inputs gave two actions: %+v and %+v", a, again)
	}
	if !reflect.DeepEqual(rec, before) {
		t.Fatalf("the decision changed its records: %+v", rec)
	}
	switch a.Kind {
	case KindIdle:
		if a.Idle == "" || a.Reap != nil || a.Spawn != nil {
			t.Fatalf("malformed idle action: %+v", a)
		}
	case KindReap:
		if a.Reap == nil || a.Spawn != nil || a.Idle != "" {
			t.Fatalf("malformed reap action: %+v", a)
		}
		if a.Reap.Project != rec.Project || !a.Reap.StopContainer || a.Reap.ChargeRetries {
			t.Fatalf("the stop of a wiki update run names the Project, stops the container, and charges nothing: %+v", a.Reap)
		}
	case KindSpawn:
		if a.Spawn == nil || a.Reap != nil || a.Idle != "" {
			t.Fatalf("malformed spawn action: %+v", a)
		}
		if want := (Spawn{Project: rec.Project, Role: RoleScribe}); !reflect.DeepEqual(*a.Spawn, want) {
			t.Fatalf("the spawn of a wiki update is a Scribe run with no task: %+v", a.Spawn)
		}
	default:
		t.Fatalf("unknown action kind %q", a.Kind)
	}
	return a
}

func cloneWikiRecords(rec WikiRecords) WikiRecords {
	clone := func(u *WikiUpdate) *WikiUpdate {
		if u == nil {
			return nil
		}
		c := *u
		if u.HeartbeatUptime != nil {
			c.HeartbeatUptime = pd(*u.HeartbeatUptime)
		}
		return &c
	}
	rec.Active, rec.LastSucceeded, rec.LastCounted = clone(rec.Active), clone(rec.LastSucceeded), clone(rec.LastCounted)
	return rec
}

func wantWikiIdle(t *testing.T, a Action, reason IdleReason) {
	t.Helper()
	if a.Kind != KindIdle || a.Idle != reason {
		t.Fatalf("want idle %q, got %+v", reason, a)
	}
}

func wantWikiReap(t *testing.T, a Action, run string, reason ReapReason) {
	t.Helper()
	if a.Kind != KindReap || a.Reap.RunID != run || a.Reap.Reason != reason {
		t.Fatalf("want reap %q of %q, got %+v (%+v)", reason, run, a, a.Reap)
	}
}

func wantWikiSpawn(t *testing.T, a Action) {
	t.Helper()
	if a.Kind != KindSpawn {
		t.Fatalf("want the spawn of a wiki update, got %+v", a)
	}
}

func TestWikiStateOf(t *testing.T) {
	merged := func(destination string) *WikiUpdate {
		return &WikiUpdate{StartCommit: wikiC1, WikiCommit: wikiC2, Result: "succeeded",
			DestinationCommit: destination, MergedCommit: wikiC3}
	}
	cases := []struct {
		name     string
		accepted string
		active   bool
		last     *WikiUpdate
		want     string
	}{
		{"an active wiki update", wikiC1, true, unchangedWiki(wikiC1, base), WikiStateUpdateActive},
		{"no successful wiki update", wikiC1, false, nil, WikiStateBehind},
		{"an unknown accepted commit", "", false, &WikiUpdate{}, WikiStateBehind},
		{"the merge onto the start commit is the accepted commit", wikiC3, false, merged(wikiC1), WikiStateCurrent},
		{"the destination of the merge differs from the start commit", wikiC3, false, merged(wikiC4), WikiStateBehind},
		{"the accepted commit is the start commit of a merged wiki update", wikiC1, false, merged(wikiC1), WikiStateBehind},
		{"the accepted commit is the wiki commit of a merged wiki update", wikiC2, false, merged(wikiC1), WikiStateBehind},
		{"no wiki change at the accepted commit", wikiC1, false, unchangedWiki(wikiC1, base), WikiStateCurrent},
		{"no wiki change at an earlier commit", wikiC2, false, unchangedWiki(wikiC1, base), WikiStateBehind},
		{"a wiki commit with no merge", wikiC1, false,
			&WikiUpdate{StartCommit: wikiC1, WikiCommit: wikiC2, Result: "succeeded"}, WikiStateBehind},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := WikiStateOf(tc.accepted, tc.active, tc.last); got != tc.want {
				t.Fatalf("WikiStateOf = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCurrentWikiStartsNoWikiUpdate(t *testing.T) {
	old := base.Add(-2 * wikiInterval)
	cases := []struct {
		name     string
		accepted string
		last     *WikiUpdate
	}{
		{"the merged wiki commit is the accepted commit", wikiC3, &WikiUpdate{
			RunID: "run-done", StartCommit: wikiC1, WikiCommit: wikiC2, StartedAt: old, Result: "succeeded",
			Counted: true, DestinationCommit: wikiC1, MergedCommit: wikiC3, RunEnded: true}},
		{"the wiki needed no change at the accepted commit", wikiC1, unchangedWiki(wikiC1, old)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := WikiRecords{Project: "ws", Status: "active", AcceptedCommit: tc.accepted,
				LastSucceeded: tc.last, LastCounted: tc.last}
			wantWikiIdle(t, decideWiki(t, rec, DefaultConfig(), wikiClock(0)), IdleWikiCurrent)
			rec.AcceptedCommit = wikiC4
			wantWikiSpawn(t, decideWiki(t, rec, DefaultConfig(), wikiClock(0)))
		})
	}
}

// The cases are in the check order of DecideWiki. Each case makes the condition
// of its check true together with the condition of each later check that can be
// true at the same time.
func TestDecideWikiCheckOrder(t *testing.T) {
	cfg := DefaultConfig()
	absent := &HostLiveness{RunID: "run-wiki", LaunchAgeKnown: true, LaunchAgeS: 3600}
	silent := func(mod func(*WikiUpdate)) *WikiUpdate {
		run := liveWikiRun()
		run.HeartbeatUptime = nil
		run.AcquiredUptime = up(-time.Hour)
		run.DeadlineUptime = up(-time.Minute)
		if mod != nil {
			mod(run)
		}
		return run
	}
	inInterval := unchangedWiki(wikiC1, base.Add(-wikiInterval+time.Second))
	failedInInterval := &WikiUpdate{RunID: "run-failed", StartCommit: wikiC2,
		StartedAt: base.Add(-wikiInterval + time.Second), Result: "failed", Counted: true, RunEnded: true}
	cases := []struct {
		name string
		rec  WikiRecords
		live *HostLiveness
		want func(*testing.T, Action)
	}{
		{"an archived Project stops its active run",
			WikiRecords{Status: "archived", Unstopped: "run-old",
				Active: silent(func(run *WikiUpdate) { run.BootID = "earlier-boot" })}, absent,
			func(t *testing.T, a Action) { wantWikiReap(t, a, "run-wiki", ReapInterrupted) }},
		{"a new boot identifier",
			WikiRecords{Status: "active", Unstopped: "run-old",
				Active: silent(func(run *WikiUpdate) { run.BootID = "earlier-boot" })}, absent,
			func(t *testing.T, a Action) { wantWikiReap(t, a, "run-wiki", ReapVMRestart) }},
		{"no first heartbeat",
			WikiRecords{Status: "active", Unstopped: "run-old", Active: silent(nil)}, absent,
			func(t *testing.T, a Action) { wantWikiReap(t, a, "run-wiki", ReapSpawnWatchdog) }},
		{"heartbeat silence",
			WikiRecords{Status: "active", Unstopped: "run-old", Active: silent(func(run *WikiUpdate) {
				run.HeartbeatUptime = pd(up(-75 * time.Minute))
			})}, absent,
			func(t *testing.T, a Action) { wantWikiReap(t, a, "run-wiki", ReapLeaseTimeout) }},
		{"the hard deadline",
			WikiRecords{Status: "active", Unstopped: "run-old", Active: silent(func(run *WikiUpdate) {
				run.HeartbeatUptime = pd(up(-30 * time.Second))
			})}, absent,
			func(t *testing.T, a Action) { wantWikiReap(t, a, "run-wiki", ReapHardDeadline) }},
		{"an absent container",
			WikiRecords{Status: "active", Unstopped: "run-old", Active: liveWikiRun()}, absent,
			func(t *testing.T, a Action) { wantWikiReap(t, a, "run-wiki", ReapContainerGone) }},
		{"an ended run with no stop confirmation",
			WikiRecords{Status: "paused", Unstopped: "run-old", Active: liveWikiRun()}, nil,
			func(t *testing.T, a Action) { wantWikiReap(t, a, "run-old", ReapReported) }},
		{"an inactive Project",
			WikiRecords{Status: "paused", Active: liveWikiRun()}, nil,
			func(t *testing.T, a Action) { wantWikiIdle(t, a, IdleWikiProjectInactive) }},
		{"an active wiki update",
			WikiRecords{Status: "active", Active: liveWikiRun(), LastCounted: inInterval}, nil,
			func(t *testing.T, a Action) { wantWikiIdle(t, a, IdleWikiUpdateActive) }},
		{"an unknown accepted commit",
			WikiRecords{Status: "active", LastSucceeded: inInterval, LastCounted: inInterval}, nil,
			func(t *testing.T, a Action) { wantWikiIdle(t, a, IdleWikiCommitUnknown) }},
		{"a current wiki",
			WikiRecords{Status: "active", AcceptedCommit: wikiC1, LastSucceeded: inInterval, LastCounted: inInterval}, nil,
			func(t *testing.T, a Action) { wantWikiIdle(t, a, IdleWikiCurrent) }},
		{"the interval",
			WikiRecords{Status: "active", AcceptedCommit: wikiC2, LastSucceeded: inInterval, LastCounted: inInterval}, nil,
			func(t *testing.T, a Action) { wantWikiIdle(t, a, IdleWikiInterval) }},
		{"the interval counts from a counted failure after the last success",
			WikiRecords{Status: "active", AcceptedCommit: wikiC2,
				LastSucceeded: unchangedWiki(wikiC1, base.Add(-2*wikiInterval)), LastCounted: failedInInterval}, nil,
			func(t *testing.T, a Action) { wantWikiIdle(t, a, IdleWikiInterval) }},
		{"the interval counts from a counted failure with no success",
			WikiRecords{Status: "active", AcceptedCommit: wikiC2, LastCounted: failedInInterval}, nil,
			func(t *testing.T, a Action) { wantWikiIdle(t, a, IdleWikiInterval) }},
		{"a due wiki update at the end of the interval",
			WikiRecords{Status: "active", AcceptedCommit: wikiC2,
				LastSucceeded: unchangedWiki(wikiC1, base.Add(-wikiInterval)),
				LastCounted:   unchangedWiki(wikiC1, base.Add(-wikiInterval))}, nil,
			wantWikiSpawn},
		{"a Project with no counted wiki update",
			WikiRecords{Status: "active", AcceptedCommit: wikiC2}, nil, wantWikiSpawn},
		{"an ended run gets no liveness rule",
			WikiRecords{Status: "archived", Active: silent(func(run *WikiUpdate) {
				run.BootID = "earlier-boot"
				run.RunEnded = true
			})}, absent,
			func(t *testing.T, a Action) { wantWikiIdle(t, a, IdleWikiProjectInactive) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.rec.Project = "ws"
			clk := wikiClock(0)
			clk.HostLiveness = tc.live
			tc.want(t, decideWiki(t, tc.rec, cfg, clk))
		})
	}
}

func TestPausedProjectStartsNoWikiUpdate(t *testing.T) {
	old := unchangedWiki(wikiC1, base.Add(-2*wikiInterval))
	behind := WikiRecords{Project: "ws", Status: "active", AcceptedCommit: wikiC2, LastSucceeded: old, LastCounted: old}
	wantWikiSpawn(t, decideWiki(t, behind, DefaultConfig(), wikiClock(0)))
	behind.Status = "paused"
	wantWikiIdle(t, decideWiki(t, behind, DefaultConfig(), wikiClock(0)), IdleWikiProjectInactive)

	overdue := liveWikiRun()
	overdue.HeartbeatUptime = pd(up(-75 * time.Minute))
	paused := WikiRecords{Project: "ws", Status: "paused", AcceptedCommit: wikiC2, Active: overdue}
	wantWikiReap(t, decideWiki(t, paused, DefaultConfig(), wikiClock(0)), "run-wiki", ReapLeaseTimeout)
}

func TestWikiRunGraceIsAtLeastSixtySeconds(t *testing.T) {
	cases := []struct {
		configuredS int
		graceS      int
	}{{5, 60}, {90, 90}}
	for _, tc := range cases {
		cfg := DefaultConfig()
		cfg.SpawnGraceS = tc.configuredS
		grace := time.Duration(tc.graceS) * time.Second

		started := &WikiUpdate{RunID: "run-wiki", StartCommit: wikiC1, StartedAt: base, Counted: true,
			BootID: "boot", AcquiredUptime: up(0), DeadlineUptime: up(2 * time.Hour)}
		rec := WikiRecords{Project: "ws", Status: "active", AcceptedCommit: wikiC1, Active: started}
		wantWikiIdle(t, decideWiki(t, rec, cfg, wikiClock(grace-time.Second)), IdleWikiUpdateActive)
		wantWikiReap(t, decideWiki(t, rec, cfg, wikiClock(grace)), "run-wiki", ReapSpawnWatchdog)

		rec.Active = liveWikiRun()
		clk := wikiClock(0)
		clk.HostLiveness = &HostLiveness{RunID: "run-wiki", LaunchAgeKnown: true, LaunchAgeS: tc.graceS - 1}
		wantWikiIdle(t, decideWiki(t, rec, cfg, clk), IdleWikiUpdateActive)
		clk.HostLiveness = &HostLiveness{RunID: "run-wiki", LaunchAgeKnown: true, LaunchAgeS: tc.graceS}
		wantWikiReap(t, decideWiki(t, rec, cfg, clk), "run-wiki", ReapContainerGone)
	}
}

// An observation that is inconclusive stops no wiki update run.
func TestInconclusiveHostObservationStopsNoWikiUpdateRun(t *testing.T) {
	rec := WikiRecords{Project: "ws", Status: "active", Active: liveWikiRun()}
	for name, live := range map[string]*HostLiveness{
		"no observation":                nil,
		"a present container":           {RunID: "run-wiki", Present: true, LaunchAgeKnown: true, LaunchAgeS: 3600},
		"no launch age":                 {RunID: "run-wiki"},
		"an observation of another run": {RunID: "run-other", LaunchAgeKnown: true, LaunchAgeS: 3600},
	} {
		clk := wikiClock(0)
		clk.HostLiveness = live
		if a := decideWiki(t, rec, DefaultConfig(), clk); a.Kind != KindIdle || a.Idle != IdleWikiUpdateActive {
			t.Fatalf("%s: want idle %q, got %+v", name, IdleWikiUpdateActive, a)
		}
	}
}

// The inputs of the decision are the wiki records, the run limits, the
// interval, and the clock. The field lists fail when a pipeline fact enters them.
func TestDecideWikiIgnoresPipelineFacts(t *testing.T) {
	var _ func(WikiRecords, Config, time.Duration, Clock) Action = DecideWiki
	fields := func(v any) []string {
		typ := reflect.TypeOf(v)
		names := make([]string, typ.NumField())
		for i := range names {
			names[i] = typ.Field(i).Name
		}
		return names
	}
	if got, want := fields(WikiRecords{}), []string{"UpdateRecovery", "Project", "EnvironmentCondition", "Status", "AcceptedCommit", "Active",
		"LastSucceeded", "LastCounted", "Unstopped"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("WikiRecords fields = %v, want %v", got, want)
	}
	if got, want := fields(WikiUpdate{}), []string{"RunID", "StartCommit", "WikiCommit", "StartedAt", "Result",
		"Counted", "DestinationCommit", "MergedCommit", "RunEnded", "BootID", "AcquiredUptime", "HeartbeatUptime",
		"DeadlineUptime"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("WikiUpdate fields = %v, want %v", got, want)
	}

	old := unchangedWiki(wikiC1, base.Add(-2*wikiInterval))
	rec := WikiRecords{Project: "ws", Status: "active", AcceptedCommit: wikiC2, LastSucceeded: old, LastCounted: old}
	for _, reviewCap := range []int{0, 1, 100} {
		cfg := DefaultConfig()
		cfg.ReviewWIPCap = reviewCap
		wantWikiSpawn(t, decideWiki(t, rec, cfg, wikiClock(0)))
	}
}
