package verbs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The host must resolve config.toml before crossing into the helper. Removing
// it after construction proves the helper write consumes only the closed
// carrier rather than accidentally reviving the helper-side host-uid read.
func TestPrivateHomieStartUsesCarriedRuntimeCapability(t *testing.T) {
	db := dvSpine(t)
	request, err := ResolvePrivateHomieStartRequest(HomieStartArgs{From: "dashboard:private-start"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(os.Getenv("MC_HOME"), "config.toml")); err != nil {
		t.Fatal(err)
	}
	started, err := HomieStartPrivateDB(db, request)
	if err != nil {
		t.Fatalf("private Homie start read helper-side config: %v", err)
	}
	if started.SessionID == "" || started.Binding != "claude-sdk/claude" || started.From != "dashboard:private-start" {
		t.Fatalf("private start = %+v", started)
	}

	request.Binding = "missing/not-configured"
	if _, err := HomieStartPrivateDB(db, request); err == nil {
		t.Fatal("want carried-capability refusal for a binding absent from the host catalog")
	}
}

// The packet and its version cross into the helper in the closed carrier, and
// an ordinary start carries neither key.
func TestPrivateHomieStartCarriesThePacket(t *testing.T) {
	db := dvSpine(t)
	packet, version := hpPacket(t, db, 1)
	request, err := ResolvePrivateHomieStartRequest(HomieStartArgs{From: "dashboard:dash-0a1b2c3d", Packet: packet, PacketVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]any
	if err := json.Unmarshal(frame, &keys); err != nil {
		t.Fatal(err)
	}
	if keys["packet_id"] != float64(packet) || keys["version_id"] != float64(version) {
		t.Fatalf("the private frame %s does not carry packet %d version %d", frame, packet, version)
	}
	var carried PrivateHomieStartRequest
	if err := json.Unmarshal(frame, &carried); err != nil {
		t.Fatal(err)
	}
	started, err := HomieStartPrivateDB(db, carried)
	if err != nil {
		t.Fatal(err)
	}
	var recorded int64
	hwGet(t, db, `SELECT packet_id FROM homie_sessions WHERE id='`+started.SessionID+`'`, &recorded)
	if recorded != packet {
		t.Fatalf("the helper recorded packet %d, want %d", recorded, packet)
	}

	ordinary, err := ResolvePrivateHomieStartRequest(HomieStartArgs{From: "dashboard:private-start"})
	if err != nil {
		t.Fatal(err)
	}
	if frame, err := json.Marshal(ordinary); err != nil || strings.Contains(string(frame), "packet_id") || strings.Contains(string(frame), "version_id") {
		t.Fatalf("the ordinary private frame is %s (%v)", frame, err)
	}
}
