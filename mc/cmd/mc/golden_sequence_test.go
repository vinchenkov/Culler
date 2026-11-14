package main_test

import (
	"strings"
	"testing"
)

// TestGoldenSequenceSpawnTicks proves that repository operations add no tick:
// each of the three ticks from the Worker to the published packet is a spawn.
func TestGoldenSequenceSpawnTicks(t *testing.T) {
	spine := initSpine(t)
	taskID := proposalTask(t, spine, "count the ticks of the golden sequence")
	arbiterPromoteAll(t, spine, dispatchThrough(t, spine, "spawn"))
	var commit string
	for tick, role := range []string{"worker", "verifier", "packager"} {
		startThrough(t, spine)
		actions := tickActions(t, dispatch(t, spine))
		if len(actions) != 1 || actions[0]["action"] != "spawn" {
			t.Fatalf("tick %d returned %v, want one %s spawn", tick+1, actions, role)
		}
		eff := actions[0]
		root := spawnedRole(t, spine, eff, role)
		switch role {
		case "worker":
			commit = workerRun(t, spine, taskID, eff, root)
		case "verifier":
			verifierRun(t, spine, taskID, commit, eff, root)
		case "packager":
			packagerRun(t, spine, taskID, eff, root)
		}
	}
	// The merge candidate step precedes the cleanups of the Packager run and of the step.
	var operations []string
	for _, execution := range startThrough(t, spine) {
		operations = append(operations, execution["operation"].(string))
	}
	if strings.Join(operations, ",") != "prepare,cleanup,cleanup" {
		t.Fatalf("the Packager run was followed by %v", operations)
	}
	if _, ok := livePacket(t, spine, taskID); !ok {
		t.Fatal("the start requests after the Packager run published no packet")
	}
}
