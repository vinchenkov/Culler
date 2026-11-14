//go:build docker_e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestBackpressureDockerBoundary proves that a full review queue holds new
// operator work at seeded. An operator revision keeps its packet slot, cycles
// through the Worker at the cap, and returns as a new packet version.
func TestBackpressureDockerBoundary(t *testing.T) {
	f := setup(t)

	// Three tasks fill the queue to its cap of 3.
	capIDs := make([]int64, 0, 3)
	for i := 0; i < 3; i++ {
		res := f.mcOK("", "task", "add", fmt.Sprintf("queue filler %d", i+1), "--project", project,
			"--description", "The task is one of three packets that fill the review cap.")
		capIDs = append(capIDs, int64(res["task_id"].(float64)))
	}

	f.env = append(f.env, "MC_LOG_LEVEL=debug")
	f.startResident()

	// Wait until all three reach the queue: three unarchived packets = at cap.
	f.waitFor(420*time.Second, "review queue fills to the cap of 3 packets", func() (bool, string) {
		return countUnarchivedPackets(f) == 3, fmt.Sprintf("%d unarchived packets", countUnarchivedPackets(f))
	})
	first := f.waitForReadyPacket(capIDs[0], 60*time.Second)

	// A fourth task waits at seeded until the review queue has space.
	res := f.mcOK("", "task", "add", "blocked-at-cap task", "--project", project,
		"--description", "The task stays seeded until the review queue has space.")
	blockedID := int64(res["task_id"].(float64))

	idleBefore := strings.Count(f.residentLog(), "idle (queue-full)")
	f.waitFor(30*time.Second, "three idle ticks at the full queue", func() (bool, string) {
		idleTicks := strings.Count(f.residentLog(), "idle (queue-full)") - idleBefore
		return idleTicks >= 3, fmt.Sprintf("%d idle ticks", idleTicks)
	})
	for _, id := range capIDs {
		if status := f.mcOK("", "task", "get", fmt.Sprint(id))["status"]; status != "packaged" {
			t.Fatalf("The queued task %d changed to %v.", id, status)
		}
	}
	workersBefore := len(f.agentRuns("worker"))
	f.mcOK("", "packet", "decide", fmt.Sprint(capIDs[0]), "--revise", "--reason", "Add the requested evidence.",
		"--packet", fmt.Sprint(int64(first["packet_id"].(float64))), "--version", fmt.Sprint(int64(first["version_id"].(float64))))

	// The revision advances at the cap: the task cycles back through the Worker.
	f.waitFor(90*time.Second, "the revised task advances through the Worker at cap", func() (bool, string) {
		workers := len(f.agentRuns("worker"))
		return workers > workersBefore, fmt.Sprintf("%d worker runs", workers)
	})

	// The revised task returns with a new packet version in the same slot.
	f.waitForTaskStatus(capIDs[0], "packaged", 180*time.Second)
	f.waitFor(90*time.Second, "the revised task publishes a new packet version", func() (bool, string) {
		p := f.readyPacket(capIDs[0])
		if p == nil {
			return false, "no ready packet yet"
		}
		if p["version_id"] == first["version_id"] {
			return false, "the packet still shows the revised version"
		}
		return true, ""
	})

	// The fourth task keeps its initial stage during the operator revision.
	if st := f.mcOK("", "task", "get", fmt.Sprint(blockedID))["status"]; st != "seeded" {
		t.Fatalf("blocked task %d left seeded (%v) while the queue was at cap", blockedID, st)
	}
	if n := countUnarchivedPackets(f); n != 3 {
		t.Fatalf("unarchived packets = %d at cap, want exactly 3 (a fourth packet was born past the WIP cap)", n)
	}

	// Drain the queue: cancel the three packets.
	for _, id := range capIDs {
		p := f.livePacket(id)
		if p == nil {
			t.Fatalf("task %d has no live packet to cancel", id)
		}
		if got := f.mcRun("", "packet", "decide", fmt.Sprint(id), "--cancel", "--reason", "The operator drains the queue.",
			"--packet", fmt.Sprint(int64(p["packet_id"].(float64))), "--version", fmt.Sprint(int64(p["version_id"].(float64)))); got.code != 0 {
			t.Fatalf("cancel packet for task %d exited %d: %s", id, got.code, got.stderr)
		}
	}

	// With the queue drained, the held task advances to its own packet.
	f.waitForTaskStatus(blockedID, "packaged", 240*time.Second)
	f.waitForReadyPacket(blockedID, 90*time.Second)
}

func countUnarchivedPackets(f *fixture) int {
	n := 0
	for _, p := range f.packets() {
		if p["archived"].(float64) == 0 && p["superseded"].(float64) == 0 {
			n++
		}
	}
	return n
}

// addResidentRoleBehavior patches resident.json to map a role to a behavior
// file before the resident starts.
func addResidentRoleBehavior(t *testing.T, f *fixture, role, behaviorPath string) {
	t.Helper()
	patchResidentConfig(t, f, func(cfg map[string]any) {
		cfg["roleBehaviors"].(map[string]any)[role] = behaviorPath
	})
}

// patchResidentConfig reads resident.json, applies mutate, and writes it back.
// Must run after setup() (which writes the config) and before startResident().
func patchResidentConfig(t *testing.T, f *fixture, mutate func(map[string]any)) {
	t.Helper()
	path := f.residentConfig()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	mutate(cfg)
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}
