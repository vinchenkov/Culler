package verbs

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

type setupObservation struct {
	ResidentInstanceID string `json:"resident_instance_id,omitempty"`
	ObservedAt         string `json:"observed_at,omitempty"`
	Status             string `json:"status,omitempty"`
	Message            string `json:"message,omitempty"`
	Details            string `json:"details,omitempty"`
	ProjectID          string `json:"project_id,omitempty"`
	OperationID        string `json:"operation_id,omitempty"`
	ResultError        string `json:"result_error,omitempty"`
	ResultDetails      string `json:"result_details,omitempty"`
	Available          bool   `json:"available"`
}

type setupHealth struct {
	Request     setupObservation   `json:"request"`
	Submissions []setupObservation `json:"submissions"`
}

func residentSetupHealth(home string, availability Availability, now time.Time) setupHealth {
	result := setupHealth{Submissions: []setupObservation{}}
	file, err := os.Open(filepath.Join(home, "health", "resident-setup.json"))
	if err != nil {
		return result
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, 8*1024*1024+1))
	if err != nil || len(body) > 8*1024*1024 || json.Unmarshal(body, &result) != nil {
		return setupHealth{Submissions: []setupObservation{}}
	}
	validate := func(observation *setupObservation, submission bool) {
		at, err := time.Parse(time.RFC3339Nano, observation.ObservedAt)
		valid := err == nil && (observation.Status == "succeeded" || observation.Status == "failed")
		valid = valid && (observation.Status != "failed" || observation.Message != "")
		valid = valid && len(observation.Message) <= 4096 && len(observation.Details) <= 24000
		valid = valid && len(observation.ResultError) <= 4096 && len(observation.ResultDetails) <= 24000
		if submission {
			valid = valid && observation.ProjectID != "" && observation.OperationID != ""
		} else {
			valid = valid && observation.ProjectID == "" && observation.OperationID == ""
		}
		observation.Available = valid && availability.Available && availability.ResidentInstanceID != "" &&
			observation.ResidentInstanceID == availability.ResidentInstanceID &&
			!at.After(now.Add(5*time.Second)) && now.Sub(at) <= 30*time.Second
		if !valid {
			*observation = setupObservation{ObservedAt: observation.ObservedAt}
		}
	}
	validate(&result.Request, false)
	for i := range result.Submissions {
		validate(&result.Submissions[i], true)
	}
	if result.Submissions == nil {
		result.Submissions = []setupObservation{}
	}
	return result
}
