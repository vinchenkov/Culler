package verbs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	operatorconfig "mc/config"
	"mc/routing"
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

func TestOpenCodePrivateCapabilityStartsHomieAndPreservesItsEffort(t *testing.T) {
	db := dvSpine(t)
	config := filepath.Join(os.Getenv("MC_HOME"), "config.toml")
	_, err := operatorconfig.UpdateAtomic(config, func(cfg *operatorconfig.Config) error {
		cfg.Runtimes.Credentials["catalog"] = operatorconfig.CredentialSource{Kind: operatorconfig.CredentialKindOpenCodeAPIKey, CatalogEntry: "moonshotai"}
		cfg.Runtimes.Bindings["catalog"] = operatorconfig.RuntimeBinding{Harness: "opencode", Model: "kimi-k3", Effort: "high", Credential: "catalog"}
		cfg.Runtimes.EnabledBindings = append(cfg.Runtimes.EnabledBindings, "catalog")
		return cfg.Validate()
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := ResolvePrivateHomieStartRequest(HomieStartArgs{From: "cli:catalog"})
	if err != nil {
		t.Fatal(err)
	}
	dvExec(t, db, `INSERT INTO connection_health(connection_id,operation,authorization_id,state,verified_at) VALUES('catalog','connection_access','unavailable','ready',datetime('now'))`)
	req.Binding = "opencode/catalog"
	frame, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(frame), `"catalog_entry":"moonshotai"`) {
		t.Fatal("The private capability lost the catalog entry.")
	}
	var carried PrivateHomieStartRequest
	if err := json.Unmarshal(frame, &carried); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}
	started, err := HomieStartPrivateDB(db, carried)
	if err != nil {
		t.Fatal(err)
	}
	if started.Binding != "opencode/catalog" {
		t.Fatal(started.Binding)
	}
	var effort string
	if err := db.QueryRow(`SELECT effort FROM homie_sessions WHERE id=?`, started.SessionID).Scan(&effort); err != nil || effort != "high" {
		t.Fatalf("The Homie effort differs: %s: %v", effort, err)
	}
	hwPendingInbound(t, db, started.SessionID, 1)
	wake, handled := hwWakeRoundWith(t, db, 1800, carried.RuntimeCapability)
	if !handled || wake["effort"] != "high" || wake["runtime_binding"].(map[string]any)["catalog_entry"] != "moonshotai" {
		t.Fatalf("The wake lost its catalog entry or effort: %+v", wake)
	}

	prepare := homieStageRequest(dfUUID(t, db))
	prepare.RuntimeCapability = carried.RuntimeCapability
	if err := inTx(db, func(ctx context.Context, q Q) error {
		_, err := DispatchPreparePrivate(ctx, q, prepare)
		return err
	}); err != nil {
		t.Fatalf("The dispatch rejected the enabled catalog: %v", err)
	}
	for _, binding := range carried.RuntimeCapability.Bindings {
		if binding.ID != "catalog" {
			continue
		}
		if err := validatePrivateRuntimeBinding(binding, "opencode", "catalog"); err != nil {
			t.Fatal(err)
		}
		spec := bindingSpecFromPrivate(binding)
		other := spec
		other.CatalogEntry = "moonshotai-cn"
		if routing.ProfileRevision(spec) == routing.ProfileRevision(other) {
			t.Fatal("The profile revision ignores the catalog entry.")
		}
		binding.CatalogEntry = ""
		if validatePrivateRuntimeBinding(binding, "opencode", "catalog") == nil {
			t.Fatal("The private capability accepted an absent catalog entry.")
		}
		binding.CatalogEntry = "moonshotai"
		binding.BaseURL = "https://unapproved.invalid"
		if validatePrivateRuntimeBinding(binding, "opencode", "catalog") == nil {
			t.Fatal("The private capability accepted a custom address.")
		}
	}
}
