package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSystemStatusReportsAStalledOperatorLane(t *testing.T) {
	spine := initSpine(t)
	home := filepath.Dir(spine)
	health := filepath.Join(home, "health")
	if err := os.MkdirAll(health, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, value any) {
		t.Helper()
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(health, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stalled := func() string {
		t.Helper()
		status := runMC(t, spineEnv(spine), "", "system", "status")
		if status.code != 0 {
			t.Fatalf("system status failed: %s", status.stderr)
		}
		for _, raw := range status.json["conditions"].([]any) {
			if condition := raw.(map[string]any); condition["id"] == "operator-lane:stalled" {
				return condition["condition"].(string)
			}
		}
		return ""
	}
	now := time.Now().UTC()
	write("resident-availability.json", map[string]any{
		"resident_instance_id": "resident-a", "observed_at": now.Format(time.RFC3339Nano), "can_commit": true,
	})
	write("operator-lane.json", map[string]any{"resident_instance_id": "resident-a", "observed_at": now.Format(time.RFC3339Nano)})
	if text := stalled(); text != "" {
		t.Fatalf("a working lane gave the condition %q", text)
	}
	write("operator-lane.json", map[string]any{
		"resident_instance_id": "resident-a", "observed_at": now.Add(-27 * time.Minute).Format(time.RFC3339Nano),
	})
	if text := stalled(); !strings.Contains(text, "27 minutes ago") {
		t.Fatalf("a stalled lane gave the condition %q", text)
	}
}
