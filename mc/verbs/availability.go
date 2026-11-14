package verbs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type Availability struct {
	ResidentInstanceID string `json:"resident_instance_id,omitempty"`
	ObservedAt         string `json:"observed_at"`
	CanCommit          bool   `json:"can_commit"`
	Available          bool   `json:"available"`
}

func ResidentAvailability(home string, now time.Time) Availability {
	var a Availability
	body, err := os.ReadFile(filepath.Join(home, "health", "resident-availability.json"))
	if err != nil || json.Unmarshal(body, &a) != nil {
		return Availability{}
	}
	at, err := time.Parse(time.RFC3339Nano, a.ObservedAt)
	a.Available = err == nil && !at.After(now.Add(5*time.Second)) && now.Sub(at) <= 30*time.Second
	return a
}
