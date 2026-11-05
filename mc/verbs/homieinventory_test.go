package verbs

import (
	"strings"
	"testing"
)

func TestValidatePrivateHomieInventory(t *testing.T) {
	valid := []PrivateHomieContainer{{
		ContainerID: strings.Repeat("a", 64),
		Launch:      strings.Repeat("b", 16),
		Name:        "mc-homie-h-abc",
		Session:     "h-abc",
		State:       "running",
	}}
	if err := ValidatePrivateHomieInventory(valid); err != nil {
		t.Fatalf("valid inventory refused: %v", err)
	}
	attested := valid[0]
	attested.RuntimeAttestation = &PrivateHomieRuntimeAttestation{
		EnvelopeDigest: strings.Repeat("c", 64), RuntimeGeneration: strings.Repeat("d", 64),
	}
	if err := ValidatePrivateHomieInventory([]PrivateHomieContainer{attested}); err != nil {
		t.Fatalf("attested inventory refused: %v", err)
	}
	for name, inventory := range map[string][]PrivateHomieContainer{
		"absent": nil,
		"wrong name": {{
			ContainerID: strings.Repeat("a", 64), Launch: strings.Repeat("b", 16),
			Name: "mc-homie-other", Session: "h-abc", State: "running",
		}},
		"unknown state": {{
			ContainerID: strings.Repeat("a", 64), Launch: strings.Repeat("b", 16),
			Name: "mc-homie-h-abc", Session: "h-abc", State: "ghost",
		}},
		"duplicate": {valid[0], valid[0]},
		"bad runtime proof": {{
			ContainerID: strings.Repeat("a", 64), Launch: strings.Repeat("b", 16),
			Name: "mc-homie-h-abc", Session: "h-abc", State: "created",
			RuntimeAttestation: &PrivateHomieRuntimeAttestation{
				EnvelopeDigest: "short", RuntimeGeneration: strings.Repeat("d", 64),
			},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidatePrivateHomieInventory(inventory); err == nil {
				t.Fatal("malformed inventory accepted")
			}
		})
	}
}
