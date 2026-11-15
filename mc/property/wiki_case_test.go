package property

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"mc/dispatch"
)

// wikiSample is one input of DecideWiki. The generator builds the commit
// facts and the liveness facts from known cases, so each check reads the case
// and makes no second derivation.
type wikiSample struct {
	Seed     int64
	Index    int
	Records  dispatch.WikiRecords
	Config   dispatch.Config
	Interval time.Duration
	Clock    dispatch.Clock
	// Behind is true when the commit facts state a Project wiki that is behind.
	Behind bool
	// Stop is the reason that stops the active wiki update run. It is "" for no stop.
	Stop dispatch.ReapReason
}

func generateWikiSample(seed int64, index int) wikiSample {
	r := rand.New(rand.NewSource(seed + int64(index)*7919))
	s := wikiSample{
		Seed: seed, Index: index, Config: dispatch.DefaultConfig(),
		Interval: []time.Duration{time.Second, time.Minute, time.Hour}[r.Intn(3)],
		Clock:    dispatch.Clock{Now: propertyNow, BootID: "boot", Uptime: propertyUptime},
	}
	// A short configured grace leaves the minimum grace of a wiki update run in effect.
	if r.Intn(3) == 0 {
		s.Config.SpawnGraceS = 5
	}
	rec := dispatch.WikiRecords{Project: "ws", Status: "active", AcceptedCommit: "source-2"}
	switch r.Intn(6) {
	case 0:
		rec.Status = "paused"
	case 1:
		rec.Status = "archived"
	}

	// Each index selects one case of the last successful wiki update.
	last := &dispatch.WikiUpdate{
		RunID: "wiki-run-last", StartCommit: "source-1", WikiCommit: "source-1",
		Result: "succeeded", Counted: true, RunEnded: true,
	}
	merged := func(destination string) {
		last.WikiCommit, last.DestinationCommit, last.MergedCommit = "wiki-1", destination, "merged-1"
	}
	switch index % 6 {
	case 0: // no successful wiki update
		last, s.Behind = nil, true
	case 1: // an unchanged wiki, and no later commit
		rec.AcceptedCommit = "source-1"
	case 2: // an unchanged wiki, and a later commit
		s.Behind = true
	case 3: // a merged wiki commit, and no later commit
		merged("source-1")
		rec.AcceptedCommit = "merged-1"
	case 4: // a merged wiki commit, and a later commit
		merged("source-1")
		s.Behind = true
	case 5: // a merge after a source change that the wiki update did not read
		merged("source-2")
		rec.AcceptedCommit = "merged-1"
		s.Behind = true
	}
	if r.Intn(8) == 0 {
		rec.AcceptedCommit = ""
	}

	// The last counted wiki update started inside the interval, at its end, or after it.
	age := []time.Duration{s.Interval / 2, s.Interval, 2 * s.Interval}[r.Intn(3)]
	failed := &dispatch.WikiUpdate{
		RunID: "wiki-run-failed", StartCommit: "source-1", StartedAt: propertyNow.Add(-age),
		Result: "failed", Counted: true, RunEnded: true,
	}
	switch {
	case last == nil && r.Intn(2) == 0:
		rec.LastCounted = failed
	case last != nil && r.Intn(2) == 0:
		last.StartedAt = propertyNow.Add(-age - s.Interval)
		rec.LastSucceeded, rec.LastCounted = last, failed
	case last != nil:
		last.StartedAt = propertyNow.Add(-age)
		counted := *last
		rec.LastSucceeded, rec.LastCounted = last, &counted
	}

	if r.Intn(3) == 0 {
		rec.Active, s.Stop = generatedWikiRun(r, &s.Clock)
		if rec.Status == "archived" && !rec.Active.RunEnded {
			s.Stop = dispatch.ReapInterrupted
		}
	}
	if r.Intn(6) == 0 {
		rec.Unstopped = "wiki-run-ended"
	}
	s.Records = rec
	return s
}

// generatedWikiRun returns an active wiki update and the liveness rule that
// stops its run in a Project with no archive.
func generatedWikiRun(r *rand.Rand, clk *dispatch.Clock) (*dispatch.WikiUpdate, dispatch.ReapReason) {
	run := &dispatch.WikiUpdate{
		RunID: "wiki-run-active", StartCommit: "source-2", StartedAt: propertyNow.Add(-10 * time.Minute),
		BootID: clk.BootID, AcquiredUptime: propertyUptime - 10*time.Minute,
		DeadlineUptime: propertyUptime + 2*time.Hour,
	}
	fresh := durationPointer(propertyUptime - 30*time.Second)
	switch r.Intn(8) {
	case 0: // the run has an end, and the merge of its wiki commit continues
		run.RunEnded = true
		run.WikiCommit = "wiki-2"
	case 1: // no heartbeat inside the minimum grace of 60 seconds
		run.AcquiredUptime = propertyUptime - 30*time.Second
	case 2: // no heartbeat after the grace
		run.AcquiredUptime = propertyUptime - 2*time.Minute
		return run, dispatch.ReapSpawnWatchdog
	case 3:
		run.HeartbeatUptime = fresh
	case 4: // no heartbeat for the timeout and its grace
		run.HeartbeatUptime = durationPointer(propertyUptime - 2*time.Hour)
		return run, dispatch.ReapLeaseTimeout
	case 5:
		run.HeartbeatUptime = fresh
		run.DeadlineUptime = propertyUptime - time.Minute
		return run, dispatch.ReapHardDeadline
	case 6:
		run.HeartbeatUptime = fresh
		run.BootID = "boot-before-restart"
		return run, dispatch.ReapVMRestart
	case 7: // the host observed no container for the run
		run.HeartbeatUptime = fresh
		clk.HostLiveness = &dispatch.HostLiveness{RunID: run.RunID, LaunchAgeS: 600, LaunchAgeKnown: true}
		return run, dispatch.ReapContainerGone
	}
	return run, ""
}

func cloneWikiUpdate(in *dispatch.WikiUpdate) *dispatch.WikiUpdate {
	if in == nil {
		return nil
	}
	out := *in
	if in.HeartbeatUptime != nil {
		out.HeartbeatUptime = durationPointer(*in.HeartbeatUptime)
	}
	return &out
}

func cloneWikiSample(in wikiSample) wikiSample {
	out := in
	out.Records.Active = cloneWikiUpdate(in.Records.Active)
	out.Records.LastSucceeded = cloneWikiUpdate(in.Records.LastSucceeded)
	out.Records.LastCounted = cloneWikiUpdate(in.Records.LastCounted)
	if in.Clock.HostLiveness != nil {
		live := *in.Clock.HostLiveness
		out.Clock.HostLiveness = &live
	}
	return out
}

// wikiDue applies the due conditions that DecideWiki reads.
func wikiDue(s wikiSample) bool {
	rec := s.Records
	return rec.Status == "active" && s.Behind && rec.Active == nil &&
		rec.AcceptedCommit != "" && rec.Unstopped == "" &&
		(rec.LastCounted == nil || !s.Clock.Now.Before(rec.LastCounted.StartedAt.Add(s.Interval)))
}

// checkWikiDecision calls DecideWiki two times for the sample and compares the
// action with the case that the generator built. It returns the action.
func checkWikiDecision(s wikiSample) (dispatch.Action, error) {
	before := cloneWikiSample(s)
	a := dispatch.DecideWiki(s.Records, s.Config, s.Interval, s.Clock)
	if b := dispatch.DecideWiki(s.Records, s.Config, s.Interval, s.Clock); !reflect.DeepEqual(a, b) {
		return a, fmt.Errorf("two calls gave two actions:\nfirst=%+v\nsecond=%+v", a, b)
	}
	if !reflect.DeepEqual(s, before) {
		return a, fmt.Errorf("DecideWiki changed an input")
	}
	if err := validateAction(a); err != nil {
		return a, err
	}
	rec := s.Records
	stop := func(run string, reason dispatch.ReapReason) error {
		want := dispatch.Reap{Project: rec.Project, RunID: run, Reason: reason, StopContainer: true}
		if a.Kind != dispatch.KindReap || *a.Reap != want {
			return fmt.Errorf("the action is %+v with the stop %+v, want the stop %+v", a, a.Reap, want)
		}
		return nil
	}
	switch {
	case s.Stop != "":
		return a, stop(rec.Active.RunID, s.Stop)
	case rec.Unstopped != "":
		return a, stop(rec.Unstopped, dispatch.ReapReported)
	case wikiDue(s):
		want := dispatch.Spawn{Project: rec.Project, Role: dispatch.RoleScribe}
		if a.Kind != dispatch.KindSpawn || !reflect.DeepEqual(*a.Spawn, want) {
			return a, fmt.Errorf("the action is %+v with the spawn %+v, want the spawn %+v", a, a.Spawn, want)
		}
		return a, nil
	case a.Kind != dispatch.KindIdle:
		return a, fmt.Errorf("the action is %+v, want an idle reason: a due condition is false", a)
	}
	// Each idle reason states a fact of the sample.
	var holds bool
	switch a.Idle {
	case dispatch.IdleWikiProjectInactive:
		holds = rec.Status != "active"
	case dispatch.IdleWikiUpdateActive:
		holds = rec.Active != nil
	case dispatch.IdleWikiCommitUnknown:
		holds = rec.AcceptedCommit == ""
	case dispatch.IdleWikiCurrent:
		holds = !s.Behind && rec.AcceptedCommit != ""
	case dispatch.IdleWikiInterval:
		holds = rec.LastCounted != nil && s.Clock.Now.Before(rec.LastCounted.StartedAt.Add(s.Interval))
	}
	if !holds {
		return a, fmt.Errorf("the idle reason %q states a false fact", a.Idle)
	}
	return a, nil
}

// wikiActionShape names the action for the counts of the generator.
func wikiActionShape(a dispatch.Action) string {
	switch a.Kind {
	case dispatch.KindSpawn:
		return "spawn"
	case dispatch.KindReap:
		return "stop:" + string(a.Reap.Reason)
	}
	return "idle:" + string(a.Idle)
}

func TestWikiDecisionStartsOnlyADueWikiUpdate(t *testing.T) {
	counts := map[string]int{}
	for _, seed := range []int64{20260713, 26071301} {
		for i := 0; i < 512; i++ {
			s := generateWikiSample(seed, i)
			a, err := checkWikiDecision(s)
			if err != nil {
				t.Fatalf("seed=%d case=%d: %v\nrecords=%+v", seed, i, err, s.Records)
			}
			counts[wikiActionShape(a)]++
			if s.Records.Status != "active" && wikiDueExceptStatus(s) {
				counts["held:"+s.Records.Status]++
			}
		}
	}
	for _, shape := range []string{
		"spawn",
		"stop:" + string(dispatch.ReapInterrupted), "stop:" + string(dispatch.ReapSpawnWatchdog),
		"stop:" + string(dispatch.ReapLeaseTimeout), "stop:" + string(dispatch.ReapHardDeadline),
		"stop:" + string(dispatch.ReapVMRestart), "stop:" + string(dispatch.ReapContainerGone),
		"stop:" + string(dispatch.ReapReported),
		"idle:" + string(dispatch.IdleWikiProjectInactive), "idle:" + string(dispatch.IdleWikiUpdateActive),
		"idle:" + string(dispatch.IdleWikiCommitUnknown), "idle:" + string(dispatch.IdleWikiCurrent),
		"idle:" + string(dispatch.IdleWikiInterval),
		"held:paused", "held:archived",
	} {
		if got := counts[shape]; got < 24 {
			t.Errorf("generator bucket %q = %d, want >= 24; present keys=%v", shape, got, sortedCountKeys(counts))
		}
	}
}

// wikiDueExceptStatus reports a sample whose Project status alone holds the wiki update.
func wikiDueExceptStatus(s wikiSample) bool {
	s.Records.Status = "active"
	return wikiDue(s)
}
