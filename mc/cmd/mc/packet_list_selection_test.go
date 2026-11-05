package main_test

import (
	"fmt"
	"testing"
)

// A Homie session reads the current record of the packet of its conversation
// with a task selection. The read needs no allowlist entry.
func TestHomieWithAnEmptyAllowlistSelectsOnePacket(t *testing.T) {
	spine := initSpine(t)
	taskID := proposalTask(t, spine, "packet with a task selection")
	packageTask(t, spine, taskID)
	packet := reviewedPacket(t, spine, taskID)

	started := runMC(t, spineEnv(spine), "", "homie", "start", "--from", "cli:empty-allowlist", "--allow", "none")
	if started.code != 0 {
		t.Fatalf("homie start failed (%d): %s", started.code, started.stderr)
	}
	env := homieJSONEnv(t, spine, started.json["session_id"].(string), []string{})

	all := runMC(t, env, "", "packet", "list")
	selected := runMC(t, env, "", "packet", "list", "--task", fmt.Sprint(taskID))
	if all.code != 0 || selected.code != 0 {
		t.Fatalf("packet list failed: %s %s", all.stderr, selected.stderr)
	}
	rows := selected.json["packets"].([]any)
	if len(rows) != 1 || fmt.Sprint(rows[0]) != fmt.Sprint(all.json["packets"].([]any)[0]) {
		t.Fatalf("the selection of task %d returned %v, want the packet %v", taskID, rows, packet)
	}
	other := runMC(t, env, "", "packet", "list", "--live", "--task", fmt.Sprint(taskID+1))
	if other.code != 0 || len(other.json["packets"].([]any)) != 0 {
		t.Fatalf("the selection of a task without a packet returned %d %v", other.code, other.json)
	}
	for _, bad := range []string{"0", "-1"} {
		refused := runMC(t, env, "", "packet", "list", "--task", bad)
		message, _ := refused.json["error"].(map[string]any)["message"].(string)
		if refused.code != 2 || message != "mc packet list --task requires a positive task identifier" {
			t.Fatalf("--task %s gave %d %v", bad, refused.code, refused.json)
		}
	}
}
