package property

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"time"

	"mc/dispatch"
)

// Shared, test-only property corpus. The untagged honesty and mutant gates
// and the nightly runtime properties consume these same cases, so the fast
// lane cannot certify a different generator.

var propertyNow = time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)

// propertyUptime is the VM uptime at propertyNow.
const propertyUptime = 24 * time.Hour

type dispatchSample struct {
	Seed    int64
	Index   int
	Records dispatch.Records
	Lock    dispatch.Lease
	Config  dispatch.Config
	Clock   dispatch.Clock
}

type taskProfile struct {
	Status   dispatch.Status
	Scope    dispatch.Scope
	Decision dispatch.TaskDecision
}

type packetProfile struct {
	Scope dispatch.Scope
	Shape string
}

type decisionPacketProfile struct {
	Scope    dispatch.Scope
	Decision dispatch.TaskDecision
	Shape    string
}

var statuses = []dispatch.Status{
	dispatch.StatusProposed,
	dispatch.StatusSeeded,
	dispatch.StatusWorked,
	dispatch.StatusVerified,
	dispatch.StatusPackaged,
}

var scopes = []dispatch.Scope{dispatch.ScopeTask}

func legalTaskProfiles() []taskProfile {
	var profiles []taskProfile
	for _, scope := range scopes {
		for _, status := range statuses {
			profiles = append(profiles,
				taskProfile{Status: status, Scope: scope},
				taskProfile{Status: status, Scope: scope, Decision: dispatch.DecisionCancelled},
			)
		}
		profiles = append(profiles,
			taskProfile{Status: dispatch.StatusProposed, Scope: scope, Decision: dispatch.DecisionRejected},
			taskProfile{Status: dispatch.StatusPackaged, Scope: scope, Decision: dispatch.DecisionApproved},
		)
	}
	return profiles
}

func legalPacketProfiles() []packetProfile {
	var profiles []packetProfile
	for _, scope := range scopes {
		for _, shape := range []string{"absent", "live", "archived"} {
			profiles = append(profiles, packetProfile{Scope: scope, Shape: shape})
		}
	}
	return profiles
}

func legalDecisionPacketProfiles() []decisionPacketProfile {
	var profiles []decisionPacketProfile
	for _, scope := range scopes {
		for _, p := range []struct {
			decision dispatch.TaskDecision
			shape    string
		}{
			{dispatch.DecisionNone, "absent"},
			{dispatch.DecisionNone, "live"},
			{dispatch.DecisionCancelled, "absent"},
			{dispatch.DecisionCancelled, "archived"},
			{dispatch.DecisionRejected, "absent"},
			{dispatch.DecisionApproved, "live"},
			{dispatch.DecisionApproved, "archived"},
		} {
			profiles = append(profiles, decisionPacketProfile{
				Scope: scope, Decision: p.decision, Shape: p.shape,
			})
		}
	}
	return profiles
}

// deliveryFor builds the delivery record of a generated task in a state the
// its branch, and a packaged task sometimes waits for its merge candidate.
func deliveryFor(r *rand.Rand, id int64, p taskProfile) dispatch.Delivery {
	d := dispatch.Delivery{}
	switch {
	case p.Status == dispatch.StatusPackaged && p.Decision == dispatch.DecisionNone && r.Intn(3) == 0:
		return d
	case p.Status == dispatch.StatusPackaged:
		d.CandidateID = id
		d.CandidateMerged = true
	}
	return d
}

func taskFromProfile(r *rand.Rand, id int64, p taskProfile) (dispatch.Task, *dispatch.Packet) {
	retries := r.Intn(4)
	t := dispatch.Task{
		ID:              id,
		Title:           fmt.Sprintf("generated-%d", id),
		Scope:           p.Scope,
		Priority:        r.Intn(5) - 1,
		CreatedAt:       propertyNow.Add(time.Duration(r.Intn(1440)-720) * time.Minute),
		Status:          p.Status,
		DispatchRetries: retries,
		Stalled:         retries == 0,
		Project:         fmt.Sprintf("ws-%d", r.Intn(3)),
		ProjectStatus:   "active",
		Delivery:        deliveryFor(r, id, p),
	}
	if r.Intn(7) == 0 {
		t.Blocked = true
	}

	var packet *dispatch.Packet
	switch p.Decision {
	case dispatch.DecisionNone:
		if p.Status == dispatch.StatusPackaged {
			packet = &dispatch.Packet{TaskID: id, CreatedAt: t.CreatedAt.Add(time.Minute)}
		}
	case dispatch.DecisionRejected:
		t.Status = dispatch.StatusProposed
		t.Decision = p.Decision
		t.DecidedAt = timePointer(t.CreatedAt.Add(time.Hour))
		t.Archived = true
		t.Blocked = false
	case dispatch.DecisionCancelled:
		t.Decision = p.Decision
		t.DecidedAt = timePointer(t.CreatedAt.Add(time.Hour))
		t.Archived = true
		t.Blocked = false
		if p.Status == dispatch.StatusPackaged {
			packet = &dispatch.Packet{
				TaskID: id, CreatedAt: t.CreatedAt.Add(time.Minute), Archived: true,
			}
		}
	case dispatch.DecisionApproved:
		t.Status = dispatch.StatusPackaged
		t.Decision = p.Decision
		t.DecidedAt = timePointer(t.CreatedAt.Add(time.Hour))
		t.Blocked = false
		packet = &dispatch.Packet{TaskID: id, CreatedAt: t.CreatedAt.Add(time.Minute)}
		t.Delivery.CandidateID = id
		t.Delivery.CandidateMerged = true
		if r.Intn(2) == 1 {
			// The merge finished: the task is history.
			t.Archived = true
			packet.Archived = true
		}
	}
	return t, packet
}

func taskFromPacketProfile(r *rand.Rand, id int64, p packetProfile) (dispatch.Task, *dispatch.Packet) {
	status := dispatch.StatusSeeded
	if p.Shape != "absent" {
		status = dispatch.StatusPackaged
	}
	t, _ := taskFromProfile(r, id, taskProfile{Status: status, Scope: p.Scope})
	switch p.Shape {
	case "absent":
		return t, nil
	case "live":
		return t, &dispatch.Packet{TaskID: id, CreatedAt: t.CreatedAt.Add(time.Minute)}
	case "archived":
		t.Decision = dispatch.DecisionCancelled
		t.DecidedAt = timePointer(t.CreatedAt.Add(time.Hour))
		t.Archived = true
		t.Blocked = false
		return t, &dispatch.Packet{TaskID: id, CreatedAt: t.CreatedAt.Add(time.Minute), Archived: true}
	default:
		panic("unknown packet profile " + p.Shape)
	}
}

func taskFromDecisionPacketProfile(r *rand.Rand, id int64, p decisionPacketProfile) (dispatch.Task, *dispatch.Packet) {
	status := dispatch.StatusSeeded
	if p.Decision == dispatch.DecisionRejected {
		status = dispatch.StatusProposed
	}
	if p.Shape != "absent" || p.Decision == dispatch.DecisionApproved {
		status = dispatch.StatusPackaged
	}
	t, packet := taskFromProfile(r, id, taskProfile{
		Status: status, Scope: p.Scope, Decision: p.Decision,
	})
	switch p.Shape {
	case "absent":
		packet = nil
	case "live":
		t.Status = dispatch.StatusPackaged
		t.Archived = false
		packet = &dispatch.Packet{
			TaskID: id, CreatedAt: t.CreatedAt.Add(time.Minute),
		}
		if p.Decision == dispatch.DecisionApproved {
			t.Delivery.CandidateID = id
			t.Delivery.CandidateMerged = true
		}
	case "archived":
		t.Status = dispatch.StatusPackaged
		t.Archived = true
		packet = &dispatch.Packet{
			TaskID: id, CreatedAt: t.CreatedAt.Add(time.Minute), Archived: true,
		}
	default:
		panic("unknown decision/packet profile " + p.Shape)
	}
	return t, packet
}

func generateDispatchSample(seed int64, index int) dispatchSample {
	r := rand.New(rand.NewSource(seed + int64(index)*7919))
	profiles := legalTaskProfiles()
	packetProfiles := legalPacketProfiles()
	decisionPacketProfiles := legalDecisionPacketProfiles()

	tasks := make([]dispatch.Task, 0, 12)
	packets := make([]dispatch.Packet, 0, 6)
	appendTask := func(t dispatch.Task, p *dispatch.Packet) {
		tasks = append(tasks, t)
		if p != nil {
			packets = append(packets, *p)
		}
	}

	// Three independently observed forced strata make the bounded honesty
	// floors deterministic without making the rest of the state non-random.
	t, p := taskFromProfile(r, int64(index*100+1), profiles[index%len(profiles)])
	appendTask(t, p)
	t, p = taskFromPacketProfile(r, int64(index*100+2), packetProfiles[index%len(packetProfiles)])
	appendTask(t, p)
	t, p = taskFromDecisionPacketProfile(
		r, int64(index*100+3), decisionPacketProfiles[index%len(decisionPacketProfiles)],
	)
	appendTask(t, p)

	noise := 2 + r.Intn(7)
	for i := 0; i < noise; i++ {
		id := int64(index*100 + 4 + i)
		profile := decisionPacketProfiles[r.Intn(len(decisionPacketProfiles))]
		t, p := taskFromDecisionPacketProfile(r, id, profile)
		appendTask(t, p)
	}

	// The real substrate admits at most three live packets. If randomized
	// noise would exceed it, turn only the noise row into terminal history;
	// the three forced honesty strata stay intact.
	live := 0
	for i := range packets {
		if packets[i].Archived {
			continue
		}
		if live < 3 {
			live++
			continue
		}
		packets[i].Archived = true
		for j := range tasks {
			if tasks[j].ID != packets[i].TaskID {
				continue
			}
			tasks[j].Decision = dispatch.DecisionCancelled
			tasks[j].DecidedAt = timePointer(tasks[j].CreatedAt.Add(time.Hour))
			tasks[j].Archived = true
			tasks[j].Blocked = false
			break
		}
	}

	records := dispatch.Records{VisionReady: true, Tasks: tasks, Packets: packets}

	cfg := dispatch.DefaultConfig()

	return dispatchSample{
		Seed: seed, Index: index, Records: records,
		Lock: generatedLock(index, tasks), Config: cfg,
		Clock: dispatch.Clock{Now: propertyNow, Uptime: propertyUptime},
	}
}

func generatedLock(index int, tasks []dispatch.Task) dispatch.Lease {
	mode := index % 6
	if mode == 0 {
		return dispatch.Lease{}
	}
	lock := dispatch.Lease{
		Held:           true,
		RunID:          fmt.Sprintf("run-%d", index),
		Owner:          "worker",
		AcquiredUptime: propertyUptime - 10*time.Minute,
		DeadlineUptime: propertyUptime + 2*time.Hour,
	}
	if len(tasks) > 0 && index%4 != 0 {
		id := tasks[index%len(tasks)].ID
		lock.SubjectID = &id
	}
	switch mode {
	case 1: // fresh, never heartbeated
		lock.AcquiredUptime = propertyUptime - 30*time.Second
	case 2: // first-heartbeat watchdog expired
		lock.AcquiredUptime = propertyUptime - 2*time.Minute
	case 3: // fresh heartbeat
		lock.HeartbeatUptime = durationPointer(propertyUptime - 30*time.Second)
	case 4: // heartbeat timeout expired
		lock.HeartbeatUptime = durationPointer(propertyUptime - 2*time.Hour)
	case 5: // hard deadline expired while heartbeat remains fresh
		lock.HeartbeatUptime = durationPointer(propertyUptime - 30*time.Second)
		lock.DeadlineUptime = propertyUptime - time.Minute
	}
	return lock
}

func timePointer(v time.Time) *time.Time { return &v }

func durationPointer(v time.Duration) *time.Duration { return &v }

func cloneRecords(in dispatch.Records) dispatch.Records {
	out := in
	if in.AttentionTaskIDs != nil {
		out.AttentionTaskIDs = append(make([]int64, 0, len(in.AttentionTaskIDs)), in.AttentionTaskIDs...)
	}
	if in.Tasks != nil {
		out.Tasks = append(make([]dispatch.Task, 0, len(in.Tasks)), in.Tasks...)
	}
	if in.Packets != nil {
		out.Packets = append(make([]dispatch.Packet, 0, len(in.Packets)), in.Packets...)
	}
	for i := range out.Tasks {
		if in.Tasks[i].DecidedAt != nil {
			v := *in.Tasks[i].DecidedAt
			out.Tasks[i].DecidedAt = &v
		}
		if in.Tasks[i].Delivery.WaitUntil != nil {
			v := *in.Tasks[i].Delivery.WaitUntil
			out.Tasks[i].Delivery.WaitUntil = &v
		}
	}
	return out
}

func cloneLock(in dispatch.Lease) dispatch.Lease {
	out := in
	if in.SubjectID != nil {
		v := *in.SubjectID
		out.SubjectID = &v
	}
	if in.HeartbeatUptime != nil {
		v := *in.HeartbeatUptime
		out.HeartbeatUptime = &v
	}
	return out
}

func validateAction(a dispatch.Action) error {
	payloads := 0
	for _, present := range []bool{a.Reap != nil, a.Spawn != nil} {
		if present {
			payloads++
		}
	}
	switch a.Kind {
	case dispatch.KindIdle:
		if payloads != 0 || a.Idle == "" {
			return fmt.Errorf("malformed idle action: %+v", a)
		}
	case dispatch.KindReap:
		if payloads != 1 || a.Reap == nil {
			return fmt.Errorf("malformed reap action: %+v", a)
		}
	case dispatch.KindSpawn:
		if payloads != 1 || a.Spawn == nil || a.Spawn.Role == "" {
			return fmt.Errorf("malformed spawn action: %+v", a)
		}
	default:
		return fmt.Errorf("unknown action kind %q", a.Kind)
	}
	return nil
}

// validateDecide checks an action of Decide. Only DecideWiki starts a Scribe run.
func validateDecide(a dispatch.Action) error {
	if err := validateAction(a); err != nil {
		return err
	}
	if a.Kind == dispatch.KindSpawn && a.Spawn.Role == dispatch.RoleScribe {
		return fmt.Errorf("Decide started a Scribe run: %+v", *a.Spawn)
	}
	return nil
}

func decisionName(d dispatch.TaskDecision) string {
	if d == dispatch.DecisionNone {
		return "none"
	}
	return string(d)
}

func packetShape(taskID int64, packets []dispatch.Packet) string {
	for _, p := range packets {
		if p.TaskID != taskID {
			continue
		}
		switch {
		case p.Archived:
			return "archived"
		default:
			return "live"
		}
	}
	return "absent"
}

func leaseShape(lock dispatch.Lease, cfg dispatch.Config, clk dispatch.Clock) string {
	if !lock.Held {
		return "free"
	}
	a := dispatch.Decide(dispatch.Records{}, lock, cfg, clk)
	if lock.HeartbeatUptime == nil {
		if a.Kind == dispatch.KindReap {
			return "watchdog-expired"
		}
		return "fresh-no-heartbeat"
	}
	if a.Kind != dispatch.KindReap {
		return "fresh-heartbeat"
	}
	if a.Reap.Reason == dispatch.ReapLeaseTimeout {
		return "timeout-expired"
	}
	return "hard-deadline"
}

func sortedCountKeys(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sameAction(a, b dispatch.Action) bool { return reflect.DeepEqual(a, b) }
