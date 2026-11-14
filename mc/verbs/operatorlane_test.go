package verbs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeOperatorLaneFixture(t *testing.T, home, name string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "health"), 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "health", name), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOperatorLaneConditionReportsOnlyAStalledLaneOfTheAvailableResident(t *testing.T) {
	now := time.Date(2026, 11, 13, 19, 38, 7, 0, time.UTC)
	stamp := func(age time.Duration) string { return now.Add(-age).Format(time.RFC3339Nano) }
	available := map[string]any{"resident_instance_id": "resident-a", "observed_at": stamp(5 * time.Second), "can_commit": true}
	lane := func(instance string, age time.Duration) map[string]any {
		return map[string]any{"resident_instance_id": instance, "observed_at": stamp(age)}
	}
	cases := []struct {
		name         string
		availability any
		lane         any
		want         string
	}{
		{"a current record", available, lane("resident-a", 20*time.Second), ""},
		{"a stalled lane", available, lane("resident-a", 27*time.Minute), "27 minutes"},
		{"a lane stalled for one minute", available, lane("resident-a", 90*time.Second), "1 minute ago"},
		{"a record of an earlier resident", available, lane("resident-b", 27*time.Minute), ""},
		{"no record", available, nil, ""},
		{"a resident that is not available", map[string]any{"resident_instance_id": "resident-a", "observed_at": stamp(10 * time.Minute), "can_commit": true}, lane("resident-a", 27*time.Minute), ""},
		{"an unreadable time", available, map[string]any{"resident_instance_id": "resident-a", "observed_at": "soon"}, ""},
	}
	for _, c := range cases {
		home := t.TempDir()
		writeOperatorLaneFixture(t, home, "resident-availability.json", c.availability)
		if c.lane != nil {
			writeOperatorLaneFixture(t, home, "operator-lane.json", c.lane)
		}
		condition, ok := OperatorLaneCondition(home, now)
		if ok != (c.want != "") {
			t.Errorf("%s: condition present = %v, want %v", c.name, ok, c.want != "")
			continue
		}
		if !ok {
			continue
		}
		if condition.ID != "operator-lane:stalled" || condition.Group != "execution" || condition.State != "failed" {
			t.Errorf("%s: condition identity = %+v", c.name, condition)
		}
		if !strings.Contains(condition.Condition, c.want) || !strings.Contains(condition.Condition, "culler restart resident") {
			t.Errorf("%s: condition text = %q", c.name, condition.Condition)
		}
	}
}
