package verbs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	operatorLaneStalledAfter   = time.Minute
	maxOperatorLaneHealthBytes = 4096
)

// OperatorLaneCondition reports that the available resident stopped its checks
// for operator requests. The resident records each working check in a health
// file. A resident that is not available gives no condition here.
func OperatorLaneCondition(home string, now time.Time) (SystemCondition, bool) {
	availability := ResidentAvailability(home, now)
	if !availability.Available {
		return SystemCondition{}, false
	}
	path := filepath.Join(home, "health", "operator-lane.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxOperatorLaneHealthBytes {
		return SystemCondition{}, false
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return SystemCondition{}, false
	}
	var health struct {
		ResidentInstanceID string `json:"resident_instance_id"`
		ObservedAt         string `json:"observed_at"`
	}
	// A record of an earlier resident says nothing about this one.
	if json.Unmarshal(body, &health) != nil || health.ResidentInstanceID != availability.ResidentInstanceID {
		return SystemCondition{}, false
	}
	observed, err := time.Parse(time.RFC3339Nano, health.ObservedAt)
	if err != nil || !observed.Before(now.Add(-operatorLaneStalledAfter)) {
		return SystemCondition{}, false
	}
	minutes := int(now.Sub(observed) / time.Minute)
	age := fmt.Sprintf("%d minutes", minutes)
	if minutes == 1 {
		age = "1 minute"
	}
	return SystemCondition{
		ID: "operator-lane:stalled", Group: "execution", State: "failed", Name: "Collie requests",
		Condition: "Culler last checked for Collie requests " + age + " ago. " +
			"Changes that Collie makes wait, and a Collie reply shows no text until it is complete. " +
			"To resume the checks, run culler restart resident.",
		Action: "View details",
	}, true
}
