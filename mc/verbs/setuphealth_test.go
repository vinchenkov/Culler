package verbs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSetupHealthFreshnessAndResidentIdentity(t *testing.T) {
	home := healthHome(t)
	now := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	directory := filepath.Join(home, "health")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	availability := Availability{ResidentInstanceID: "current", Available: true}
	observation := setupObservation{ResidentInstanceID: "current", ObservedAt: now.Format(time.RFC3339Nano), Status: "failed", Message: "The work request failed.", Details: "Host evidence."}
	write := func(value any) {
		t.Helper()
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "resident-setup.json"), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if result := residentSetupHealth(home, availability, now); result.Request.Available {
		t.Fatal("An absent file supplied current health.")
	}
	write(setupHealth{Request: observation})
	if result := residentSetupHealth(home, availability, now); !result.Request.Available || result.Request.Message != observation.Message {
		t.Fatalf("Fresh failure: %+v", result)
	}
	if result := residentSetupHealth(home, availability, now.Add(31*time.Second)); result.Request.Available || result.Request.Message != observation.Message || result.Request.ObservedAt != observation.ObservedAt {
		t.Fatalf("Stale failure: %+v", result)
	}
	availability.ResidentInstanceID = "next"
	if result := residentSetupHealth(home, availability, now); result.Request.Available || result.Request.Message != observation.Message {
		t.Fatalf("Prior resident: %+v", result)
	}
	availability.ResidentInstanceID = "current"
	observation.Status = "succeeded"
	observation.Message = ""
	observation.Details = ""
	write(setupHealth{Request: observation})
	if result := residentSetupHealth(home, availability, now); !result.Request.Available || result.Request.Message != "" {
		t.Fatalf("Recovery: %+v", result)
	}
	write(map[string]any{"request": map[string]any{"status": "nonsense", "observed_at": observation.ObservedAt, "available": true}})
	if result := residentSetupHealth(home, availability, now); result.Request.Available {
		t.Fatal("An invalid record supplied current health.")
	}
	if err := os.WriteFile(filepath.Join(directory, "resident-setup.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if result := residentSetupHealth(home, availability, now); result.Request.Available {
		t.Fatal("Malformed JSON supplied current health.")
	}
}

func TestHealthReturnsSetupEvidenceWithoutSpineOrDocker(t *testing.T) {
	home := healthHome(t)
	t.Setenv("MC_SPINE", filepath.Join(home, "absent-spine"))
	t.Setenv("MC_HELPER", "absent-helper")
	directory := filepath.Join(home, "health")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for name, value := range map[string]any{
		"resident-availability.json": Availability{ResidentInstanceID: "resident", ObservedAt: now, CanCommit: false},
		"resident-setup.json":        setupHealth{Request: setupObservation{ResidentInstanceID: "resident", ObservedAt: now, Status: "failed", Message: "Docker is unavailable."}, Submissions: []setupObservation{{ResidentInstanceID: "resident", ObservedAt: now, Status: "failed", ProjectID: "garden", OperationID: "operation", Message: "The result could not be saved.", ResultError: "The harness stopped."}}},
	} {
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	response, err := Health(10)
	if err != nil {
		t.Fatal(err)
	}
	result := response.(map[string]any)["setup"].(setupHealth)
	if !result.Request.Available || result.Request.Message != "Docker is unavailable." {
		t.Fatalf("Setup health: %+v", result)
	}
	if len(result.Submissions) != 1 || !result.Submissions[0].Available || result.Submissions[0].ResultError != "The harness stopped." {
		t.Fatalf("Submission health: %+v", result)
	}
}
