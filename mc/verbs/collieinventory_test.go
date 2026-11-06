package verbs

import (
	"strings"
	"testing"
)

func TestValidatePrivateCollieInventory(t *testing.T) {
	valid := []PrivateCollieContainer{{
		ContainerID: strings.Repeat("a", 64),
		Launch:      strings.Repeat("b", 16),
		Name:        "mc-collie-h-abc",
		Session:     "h-abc",
		State:       "running",
	}}
	if err := ValidatePrivateCollieInventory(valid); err != nil {
		t.Fatalf("valid inventory refused: %v", err)
	}
	attested := valid[0]
	attested.RuntimeAttestation = &PrivateCollieRuntimeAttestation{
		EnvelopeDigest: strings.Repeat("c", 64), RuntimeGeneration: strings.Repeat("d", 64),
	}
	if err := ValidatePrivateCollieInventory([]PrivateCollieContainer{attested}); err != nil {
		t.Fatalf("attested inventory refused: %v", err)
	}
	for name, inventory := range map[string][]PrivateCollieContainer{
		"absent": nil,
		"wrong name": {{
			ContainerID: strings.Repeat("a", 64), Launch: strings.Repeat("b", 16),
			Name: "mc-collie-other", Session: "h-abc", State: "running",
		}},
		"unknown state": {{
			ContainerID: strings.Repeat("a", 64), Launch: strings.Repeat("b", 16),
			Name: "mc-collie-h-abc", Session: "h-abc", State: "ghost",
		}},
		"duplicate": {valid[0], valid[0]},
		"bad runtime proof": {{
			ContainerID: strings.Repeat("a", 64), Launch: strings.Repeat("b", 16),
			Name: "mc-collie-h-abc", Session: "h-abc", State: "created",
			RuntimeAttestation: &PrivateCollieRuntimeAttestation{
				EnvelopeDigest: "short", RuntimeGeneration: strings.Repeat("d", 64),
			},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidatePrivateCollieInventory(inventory); err == nil {
				t.Fatal("malformed inventory accepted")
			}
		})
	}
}
