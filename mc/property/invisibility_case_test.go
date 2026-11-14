package property

import (
	"fmt"
	"math/rand"
	"time"

	"mc/dispatch"
)

type invisibilityCase struct {
	Name     string
	Seed     int64
	Index    int
	Base     dispatch.Records
	Extended dispatch.Records
	Mutated  dispatch.Records
	Config   dispatch.Config
	Clock    dispatch.Clock
	Inserted int64
}

func dominantIneligibleCase(seed int64, index int, class string) invisibilityCase {
	r := rand.New(rand.NewSource(seed + int64(index)*3571))
	winnerID := int64(index*100 + 70 + r.Intn(10))
	insertedID := int64(index*100 + 1 + r.Intn(10))
	winner := dispatch.Task{
		ID: winnerID, Title: "visible winner", Scope: dispatch.ScopeTask,
		Priority: 2, CreatedAt: propertyNow, Status: dispatch.StatusSeeded,
		DispatchRetries: 3, Project: "ws-visible", ProjectStatus: "active",
	}
	inserted := dispatch.Task{
		ID: insertedID, Title: "dominant but ineligible", Scope: dispatch.ScopeTask,
		Priority: -1, CreatedAt: propertyNow.Add(-24 * time.Hour),
		Status: dispatch.StatusVerified, DispatchRetries: 3,
		Project: "ws-hidden", ProjectStatus: "active",
	}
	mutated := inserted
	switch class {
	case "blocked":
		inserted.Blocked = true
		mutated = inserted
		mutated.Blocked = false
	case "archived":
		inserted.Decision = dispatch.DecisionCancelled
		inserted.DecidedAt = timePointer(propertyNow.Add(-time.Hour))
		inserted.Archived = true
		mutated = inserted
		mutated.Decision = dispatch.DecisionNone
		mutated.DecidedAt = nil
		mutated.Archived = false
	case "paused-project":
		inserted.ProjectStatus = "paused"
		mutated = inserted
		mutated.ProjectStatus = "active"
	default:
		panic("unknown ineligible class " + class)
	}
	base := dispatch.Records{VisionReady: true, Tasks: []dispatch.Task{winner}}
	extended := cloneRecords(base)
	extended.Tasks = append(extended.Tasks, inserted)
	mutantRecords := cloneRecords(base)
	mutantRecords.Tasks = append(mutantRecords.Tasks, mutated)
	cfg := dispatch.DefaultConfig()
	return invisibilityCase{
		Name: class, Seed: seed, Index: index, Base: base, Extended: extended,
		Mutated: mutantRecords, Config: cfg, Clock: dispatch.Clock{Now: propertyNow, Uptime: propertyUptime},
		Inserted: insertedID,
	}
}

func archivedPacketCase(seed int64, index int) invisibilityCase {
	r := rand.New(rand.NewSource(seed + int64(index)*4441))
	offset := int64(index*1000 + r.Intn(100))
	packetA, packetB := offset+10, offset+20
	winnerID, historyID := offset+30, offset+1
	// A packaged task under review carries its merged candidate.
	packaged := func(id int64, priority int) dispatch.Task {
		return dispatch.Task{
			ID: id, Title: fmt.Sprintf("packet-%d", id), Scope: dispatch.ScopeTask,
			Priority: priority, CreatedAt: propertyNow.Add(time.Duration(id%5) * time.Minute),
			Status: dispatch.StatusPackaged, DispatchRetries: 3,
			Project: "ws", ProjectStatus: "active",
			Delivery: dispatch.Delivery{CandidateID: id, CandidateMerged: true},
		}
	}
	winner := dispatch.Task{
		ID: winnerID, Title: "with-room winner", Scope: dispatch.ScopeTask,
		Priority: 2, CreatedAt: propertyNow, Status: dispatch.StatusSeeded,
		DispatchRetries: 3, Project: "ws", ProjectStatus: "active",
	}
	base := dispatch.Records{
		VisionReady: true,
		Tasks:       []dispatch.Task{packaged(packetA, 0), packaged(packetB, 1), winner},
		Packets: []dispatch.Packet{
			{TaskID: packetA, CreatedAt: propertyNow.Add(-2 * time.Hour)},
			{TaskID: packetB, CreatedAt: propertyNow.Add(-time.Hour)},
		},
	}
	history := packaged(historyID, -1)
	history.Decision = dispatch.DecisionCancelled
	history.DecidedAt = timePointer(propertyNow.Add(-30 * time.Minute))
	history.Archived = true
	historyPacket := dispatch.Packet{
		TaskID: historyID, CreatedAt: propertyNow.Add(-3 * time.Hour), Archived: true,
	}
	extended := cloneRecords(base)
	extended.Tasks = append(extended.Tasks, history)
	extended.Packets = append(extended.Packets, historyPacket)
	mutated := cloneRecords(extended)
	for i := range mutated.Packets {
		if mutated.Packets[i].TaskID == historyID {
			mutated.Packets[i].Archived = false
		}
	}
	cfg := dispatch.DefaultConfig()
	return invisibilityCase{
		Name: "archived-packet", Seed: seed, Index: index,
		Base: base, Extended: extended, Mutated: mutated,
		Config: cfg, Clock: dispatch.Clock{Now: propertyNow, Uptime: propertyUptime}, Inserted: historyID,
	}
}

func evaluateInvisibilityCase(c invisibilityCase) (dispatch.Action, dispatch.Action, dispatch.Action) {
	return dispatch.Decide(c.Base, dispatch.Lease{}, c.Config, c.Clock),
		dispatch.Decide(c.Extended, dispatch.Lease{}, c.Config, c.Clock),
		dispatch.Decide(c.Mutated, dispatch.Lease{}, c.Config, c.Clock)
}
