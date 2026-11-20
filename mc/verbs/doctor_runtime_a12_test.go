package verbs

import (
	"strings"
	"testing"

	"mc/substrate"
)

// The doctor-runtime frame is a plain exec entry, so its build identity
// lives entirely in the envelope: any component of the caller's identity
// that differs from the helper's own constants must be refused before the
// spine is touched.

func TestDoctorRuntimeRefusesForeignBuildIdentity(t *testing.T) {
	const spine = "/nonexistent/a12-doctor-spine.db"
	good := DoctorRuntimeRequest{
		ProtocolVersion: 1, ReleaseBuildID: "rel-a", ControlVersion: 7,
		SpineSchemaID: substrate.CurrentSchemaID, ConfigSchemaVersion: 1,
	}
	if _, err := DoctorRuntime(spine, good, "rel-a", 7, 1); err != nil && strings.Contains(err.Error(), "build/schema identity mismatch") {
		t.Fatalf("the matching identity was refused: %v", err)
	}
	for name, mutate := range map[string]func(*DoctorRuntimeRequest){
		"protocol": func(r *DoctorRuntimeRequest) { r.ProtocolVersion = 2 },
		"release":  func(r *DoctorRuntimeRequest) { r.ReleaseBuildID = "rel-b" },
		"control":  func(r *DoctorRuntimeRequest) { r.ControlVersion = 8 },
		"schema": func(r *DoctorRuntimeRequest) {
			r.SpineSchemaID = "0000000000000000000000000000000000000000000000000000000000000000"
		},
		"config": func(r *DoctorRuntimeRequest) { r.ConfigSchemaVersion = 2 },
	} {
		bad := good
		mutate(&bad)
		_, err := DoctorRuntime(spine, bad, "rel-a", 7, 1)
		if err == nil || !strings.Contains(err.Error(), "build/schema identity mismatch") {
			t.Fatalf("%s mismatch accepted: %v", name, err)
		}
	}
}
