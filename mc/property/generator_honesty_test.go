package property

import (
	"fmt"
	"testing"

	"mc/dispatch"
)

func boundedDispatchSamples() []dispatchSample {
	var samples []dispatchSample
	for _, seed := range []int64{20260713, 26071301} {
		for i := 0; i < 256; i++ {
			samples = append(samples, generateDispatchSample(seed, i))
		}
	}
	return samples
}

func TestGeneratorHonesty(t *testing.T) {
	counts := map[string]int{}
	for _, sample := range boundedDispatchSamples() {
		seenTasks := map[int64]bool{}
		livePackets := 0
		for _, task := range sample.Records.Tasks {
			if seenTasks[task.ID] {
				t.Fatalf("seed=%d case=%d duplicate task id %d", sample.Seed, sample.Index, task.ID)
			}
			seenTasks[task.ID] = true
			shape := packetShape(task.ID, sample.Records.Packets)
			counts[fmt.Sprintf("task:%s|%s|%s", task.Status, task.Scope, decisionName(task.Decision))]++
			counts[fmt.Sprintf("packet:%s|%s", task.Scope, shape)]++
			counts[fmt.Sprintf("decision-packet:%s|%s|%s", task.Scope, decisionName(task.Decision), shape)]++
			if task.Blocked {
				counts["blocked:true"]++
			} else {
				counts["blocked:false"]++
			}
		}
		seenPackets := map[int64]bool{}
		for _, packet := range sample.Records.Packets {
			if seenPackets[packet.TaskID] {
				t.Fatalf("seed=%d case=%d duplicate packet for task %d", sample.Seed, sample.Index, packet.TaskID)
			}
			seenPackets[packet.TaskID] = true
			if !seenTasks[packet.TaskID] {
				t.Fatalf("seed=%d case=%d packet has no task %d", sample.Seed, sample.Index, packet.TaskID)
			}
			if !packet.Archived {
				livePackets++
			}
		}
		if livePackets > sample.Config.ReviewWIPCap {
			t.Fatalf("seed=%d case=%d generated %d live packets above cap %d",
				sample.Seed, sample.Index, livePackets, sample.Config.ReviewWIPCap)
		}
		counts["lease:"+leaseShape(sample.Lock, sample.Config, sample.Clock)]++
		if err := validateDecide(dispatch.Decide(
			sample.Records, sample.Lock, sample.Config, sample.Clock,
		)); err != nil {
			t.Fatalf("seed=%d case=%d: %v", sample.Seed, sample.Index, err)
		}
	}

	assertFloor := func(key string, floor int) {
		t.Helper()
		if got := counts[key]; got < floor {
			t.Errorf("generator bucket %q = %d, want >= %d; present keys=%v",
				key, got, floor, sortedCountKeys(counts))
		}
	}
	for _, p := range legalTaskProfiles() {
		assertFloor(fmt.Sprintf("task:%s|%s|%s", p.Status, p.Scope, decisionName(p.Decision)), 16)
	}
	for _, p := range legalPacketProfiles() {
		assertFloor(fmt.Sprintf("packet:%s|%s", p.Scope, p.Shape), 32)
	}
	for _, p := range legalDecisionPacketProfiles() {
		assertFloor(fmt.Sprintf("decision-packet:%s|%s|%s",
			p.Scope, decisionName(p.Decision), p.Shape), 16)
	}
	for _, shape := range []string{
		"free", "fresh-no-heartbeat", "watchdog-expired",
		"fresh-heartbeat", "timeout-expired", "hard-deadline",
	} {
		assertFloor("lease:"+shape, 80)
	}
	assertFloor("blocked:true", 64)
	assertFloor("blocked:false", 512)
}
