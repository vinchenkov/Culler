package verbs

import (
	"encoding/json"
	"testing"

	"mc/dispatch"
)

func TestTaskReadsExposeOpenVerifierCorrection(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	for _, open := range []int{0, 1, 0} {
		if _, err := db.Exec(`UPDATE tasks SET verifier_correction_open=? WHERE id=1`, open); err != nil {
			t.Fatal(err)
		}
		get, err := TaskGet(db, 1)
		if err != nil {
			t.Fatal(err)
		}
		list, err := TaskList(db, false, "ws-test")
		if err != nil {
			t.Fatal(err)
		}
		for name, value := range map[string]any{"get": get, "list": list} {
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var row struct {
				Delivery struct {
					Open *int `json:"verifier_correction_open"`
				} `json:"delivery"`
			}
			if name == "list" {
				var result struct {
					Tasks []json.RawMessage `json:"tasks"`
				}
				if err := json.Unmarshal(data, &result); err != nil || len(result.Tasks) != 1 {
					t.Fatalf("task list = %s, error = %v", data, err)
				}
				data = result.Tasks[0]
			}
			if err := json.Unmarshal(data, &row); err != nil {
				t.Fatal(err)
			}
			if row.Delivery.Open == nil || *row.Delivery.Open != open {
				t.Fatalf("%s correction state = %s, want %d", name, data, open)
			}
		}
	}
}
