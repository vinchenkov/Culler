package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Homie runner reads the packet context through the real CLI in its own
// container. The command returns exactly four keys, refuses a conversation
// without a packet, and refuses each caller other than the runner of the
// conversation.
func TestHomiePacketContextCommandIsTheRunnerRead(t *testing.T) {
	spine := initSpine(t)
	taskID := proposalTask(t, spine, "packet context through the CLI")
	packageTask(t, spine, taskID)
	packet := reviewedPacket(t, spine, taskID)
	started := runMC(t, spineEnv(spine), "", "homie", "start", "--from", "dashboard:dash-1a2b3c4d",
		"--packet", fmt.Sprint(packet["packet_id"]), "--version", fmt.Sprint(packet["version_id"]))
	if started.code != 0 {
		t.Fatalf("the preparation failed (%d): %s", started.code, started.stderr)
	}
	session := started.json["session_id"].(string)

	read := runMC(t, homieJSONEnv(t, spine, session, []string{}), "", "homie", "packet-context", session)
	context, _ := read.json["context"].(string)
	if read.code != 0 || len(read.json) != 4 || read.json["session_id"] != session ||
		read.json["packet_id"] != packet["packet_id"] || read.json["version_id"] != packet["version_id"] ||
		!strings.HasPrefix(context, "# Packet context\n") ||
		!strings.Contains(context, fmt.Sprintf("- Task: %d (packet context through the CLI)\n", taskID)) {
		t.Fatalf("the packet context read gave %d %v (%s)", read.code, read.json, read.stderr)
	}

	ordinary := runMC(t, spineEnv(spine), "", "homie", "start", "--from", "dashboard:dash-5e6f7a8b")
	if ordinary.code != 0 {
		t.Fatalf("the ordinary start failed (%d): %s", ordinary.code, ordinary.stderr)
	}
	other := ordinary.json["session_id"].(string)
	refused := runMC(t, homieJSONEnv(t, spine, other, []string{}), "", "homie", "packet-context", other)
	message, _ := refused.json["error"].(map[string]any)["message"].(string)
	if refused.code != 1 || message != fmt.Sprintf("Homie session %q is not a packet conversation", other) {
		t.Fatalf("an ordinary conversation gave %d %v", refused.code, refused.json)
	}
	for name, env := range map[string][]string{
		"the host":             spineEnv(spine),
		"another conversation": homieJSONEnv(t, spine, other, []string{}),
	} {
		if denied := runMC(t, env, "", "homie", "packet-context", session); denied.code != 1 {
			t.Fatalf("%s read the packet context: %d %v", name, denied.code, denied.json)
		}
	}
}

// In production `mc homie start` reaches the spine through the helper. A
// preparation for a changed packet keeps its record-conflict code across that
// crossing, and repeated preparations give one conversation.
func TestHomieStartThroughTheHelperRelaysAChangedPacket(t *testing.T) {
	spine := initSpine(t)
	taskID := proposalTask(t, spine, "packet conversation through the helper")
	packageTask(t, spine, taskID)
	packet := reviewedPacket(t, spine, taskID)
	packetID := int64(packet["packet_id"].(float64))
	version := fmt.Sprint(int64(packet["version_id"].(float64)))

	stubDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stubDir, "docker"), []byte(brokerHelperStub), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"MC_HELPER=mc-helper-1",
		"MC_HOME=" + filepath.Dir(spine),
		"MC_TEST_BIN=" + mcBin,
		"MC_TEST_SPINE=" + spine,
		"PATH=" + stubDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	refused := runMC(t, env, "", "homie", "start", "--from", "dashboard:dash-0a1b2c3d",
		"--packet", fmt.Sprint(packetID+1), "--version", version)
	refusal, _ := refused.json["error"].(map[string]any)
	if refused.code != 1 || refusal["code"] != "record-conflict" || refusal["message"] != "The review packet changed. Open the current packet." {
		t.Fatalf("the brokered preparation of another packet gave %d %v (%s)", refused.code, refused.json, refused.stderr)
	}

	sessions := map[any]bool{}
	for _, ref := range []string{"dashboard:dash-0a1b2c3d", "dashboard:dash-4e5f6a7b"} {
		started := runMC(t, env, "", "homie", "start", "--from", ref, "--packet", fmt.Sprint(packetID), "--version", version)
		if started.code != 0 || started.json["status"] != "active" || started.json["from"] != "dashboard:dash-0a1b2c3d" {
			t.Fatalf("the brokered preparation gave %d %v (%s)", started.code, started.json, started.stderr)
		}
		sessions[started.json["session_id"]] = true
	}
	if len(sessions) != 1 {
		t.Fatalf("the repeated preparations gave the conversations %v", sessions)
	}
	usage := runMC(t, env, "", "homie", "start", "--from", "dashboard:dash-8c9d0e1f", "--packet", fmt.Sprint(packetID))
	message, _ := usage.json["error"].(map[string]any)["message"].(string)
	if usage.code != 2 || message != "A packet conversation requires --packet and --version." {
		t.Fatalf("a preparation without a version gave %d %v", usage.code, usage.json)
	}
}
