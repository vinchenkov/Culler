package main_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"mc/substrate"
	"mc/verbs"
)

func onboardSpineFrame(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"protocol_version": 1,
		"schema_id":        substrate.CurrentSchemaID,
		"mirror_state":     "absent",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPrivateOnboardSpineCommandUsesOnlyFixedHelperScope(t *testing.T) {
	spine := filepath.Join(t.TempDir(), "spine.db")
	frame := onboardSpineFrame(t)

	withoutScope := runMC(t, nil, frame, "__onboard-spine")
	if withoutScope.code != 1 || !strings.Contains(withoutScope.stderr, "helper's fixed spine scope") {
		t.Fatalf("without scope: code=%d stderr=%q json=%v", withoutScope.code, withoutScope.stderr, withoutScope.json)
	}

	got := runMC(t, spineEnv(spine), frame, "__onboard-spine")
	if got.code != 0 || got.json["status"] != "initialized" || got.json["deployment_uuid"] == "" {
		t.Fatalf("private onboard: code=%d stderr=%q json=%v", got.code, got.stderr, got.json)
	}

	unknown := strings.TrimSuffix(frame, "}") + `,"home":"/host"}`
	refused := runMC(t, spineEnv(spine), unknown, "__onboard-spine")
	if refused.code != 2 || !strings.Contains(refused.stderr, "unknown field") {
		t.Fatalf("unknown frame field: code=%d stderr=%q json=%v", refused.code, refused.stderr, refused.json)
	}
}

func TestPrivateDoctorRuntimeUsesOnlyFixedHelperScope(t *testing.T) {
	spine := filepath.Join(t.TempDir(), "spine.db")
	initialized := runMC(t, spineEnv(spine), onboardSpineFrame(t), "__onboard-spine")
	if initialized.code != 0 {
		t.Fatalf("initialize: code=%d stderr=%q", initialized.code, initialized.stderr)
	}
	frame, err := json.Marshal(map[string]any{
		"protocol_version": 1, "release_build_id": "development",
		"control_version": verbs.GatewayControlVersion, "spine_schema_id": substrate.CurrentSchemaID,
		"config_schema_version": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	withoutScope := runMC(t, nil, string(frame), "__doctor-runtime")
	if withoutScope.code != 1 || !strings.Contains(withoutScope.stderr, "fixed spine scope") {
		t.Fatalf("without scope: code=%d stderr=%q", withoutScope.code, withoutScope.stderr)
	}
	got := runMC(t, spineEnv(spine), string(frame), "__doctor-runtime")
	if got.code != 0 || got.json["spine_uuid"] == "" {
		t.Fatalf("runtime doctor: code=%d stderr=%q json=%v", got.code, got.stderr, got.json)
	}
	findings, ok := got.json["findings"].([]any)
	if !ok || len(findings) != 4 {
		t.Fatalf("runtime findings = %#v", got.json["findings"])
	}
}

func TestPrivateAcceptanceSnapshotUsesOnlyFixedHelperScope(t *testing.T) {
	spine := filepath.Join(t.TempDir(), "spine.db")
	initialized := runMC(t, spineEnv(spine), onboardSpineFrame(t), "__onboard-spine")
	if initialized.code != 0 {
		t.Fatalf("initialize: code=%d stderr=%q", initialized.code, initialized.stderr)
	}
	withoutScope := runMC(t, nil, "", "__acceptance-snapshot")
	if withoutScope.code != 1 || !strings.Contains(withoutScope.stderr, "fixed spine scope") {
		t.Fatalf("without scope: code=%d stderr=%q", withoutScope.code, withoutScope.stderr)
	}
	got := runMC(t, spineEnv(spine), "", "__acceptance-snapshot")
	leases, leasesOK := got.json["leases"].([]any)
	settings, settingsOK := got.json["settings"].(map[string]any)
	if got.code != 0 || got.json["version"] != float64(1) ||
		got.json["vm_now"] == "" || !leasesOK || !settingsOK ||
		settings["timeout_minutes"] == nil {
		t.Fatalf("acceptance snapshot: code=%d stderr=%q json=%v", got.code, got.stderr, got.json)
	}
	for _, item := range leases {
		if lease := item.(map[string]any); lease["run_id"] != nil || lease["status"] != "free" {
			t.Fatalf("acceptance snapshot lease %v, want free", lease)
		}
	}
}

func TestPrivateOnboardStateIsIdentityBoundAndClosed(t *testing.T) {
	spine := filepath.Join(t.TempDir(), "spine.db")
	initialized := runMC(t, spineEnv(spine), onboardSpineFrame(t), "__onboard-spine")
	if initialized.code != 0 {
		t.Fatalf("initialize: code=%d stderr=%q", initialized.code, initialized.stderr)
	}
	uuid, _ := initialized.json["deployment_uuid"].(string)
	request := map[string]any{
		"protocol_version": 1, "release_build_id": "development",
		"control_version": verbs.GatewayControlVersion, "spine_schema_id": substrate.CurrentSchemaID,
		"config_schema_version": 1, "deployment_uuid": uuid, "section": "routing",
	}
	frame, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	withoutScope := runMC(t, nil, string(frame), "__onboard-state")
	if withoutScope.code != 1 || !strings.Contains(withoutScope.stderr, "fixed spine scope") {
		t.Fatalf("without scope: code=%d stderr=%q", withoutScope.code, withoutScope.stderr)
	}
	got := runMC(t, spineEnv(spine), string(frame), "__onboard-state")
	if got.code != 0 || got.json["status"] != "ok" || got.json["deployment_uuid"] != uuid {
		t.Fatalf("state identity: code=%d stderr=%q json=%v", got.code, got.stderr, got.json)
	}

	request["home"] = "/host"
	unknown, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	refused := runMC(t, spineEnv(spine), string(unknown), "__onboard-state")
	if refused.code != 2 || !strings.Contains(refused.stderr, "unknown field") {
		t.Fatalf("unknown field: code=%d stderr=%q json=%v", refused.code, refused.stderr, refused.json)
	}

	delete(request, "home")
	request["deployment_uuid"] = "wrong"
	mismatch, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	refused = runMC(t, spineEnv(spine), string(mismatch), "__onboard-state")
	if refused.code != 1 || !strings.Contains(refused.stderr, "deployment identity mismatch") {
		t.Fatalf("identity mismatch: code=%d stderr=%q json=%v", refused.code, refused.stderr, refused.json)
	}
}

func TestPrivateDatabaseSchemaUsesOnlyFixedHelperScope(t *testing.T) {
	spine := filepath.Join(t.TempDir(), "spine.db")
	withoutScope := runMC(t, nil, "", "__database-schema")
	if withoutScope.code != 1 || !strings.Contains(withoutScope.stderr, "fixed database scope") {
		t.Fatalf("without scope: code=%d stderr=%q", withoutScope.code, withoutScope.stderr)
	}
	absent := runMC(t, spineEnv(spine), "", "__database-schema")
	if absent.code != 0 || absent.json["state"] != "absent" || absent.json["schema_id"] != substrate.CurrentSchemaID {
		t.Fatalf("absent database: code=%d stderr=%q json=%v", absent.code, absent.stderr, absent.json)
	}
	initialized := runMC(t, spineEnv(spine), onboardSpineFrame(t), "__onboard-spine")
	if initialized.code != 0 {
		t.Fatalf("initialize: code=%d stderr=%q", initialized.code, initialized.stderr)
	}
	current := runMC(t, spineEnv(spine), "", "__database-schema")
	if current.code != 0 || current.json["state"] != "current" || current.json["deployment_uuid"] != initialized.json["deployment_uuid"] {
		t.Fatalf("current database: code=%d stderr=%q json=%v", current.code, current.stderr, current.json)
	}
}
