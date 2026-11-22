package verbs

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadIdentityBindsRunEnvelopeToRelease proves the G-B consumer throat: an
// agent container's run.json must carry this binary's release build id, and a
// mismatch (or a missing field) is refused before any spine open. The check is
// skipped when no self identity was recorded, so host-side package tests that
// never run through main are unaffected.
func TestLoadIdentityBindsRunEnvelopeToRelease(t *testing.T) {
	cases := []struct {
		name    string
		self    string
		doc     string
		wantErr bool
	}{
		{"matched pair loads", "REL-A", `{"tier":"pipeline","role":"worker","release_build_id":"REL-A"}`, false},
		{"mixed build refused", "REL-A", `{"tier":"pipeline","role":"worker","release_build_id":"REL-OLD"}`, true},
		{"missing field refused", "REL-A", `{"tier":"pipeline","role":"worker"}`, true},
		{"no self identity skips", "", `{"tier":"pipeline","role":"worker"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "run.json")
			if err := os.WriteFile(path, []byte(tc.doc), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("MC_RUN_JSON", path)
			SetSelfReleaseBuildID(tc.self)
			t.Cleanup(func() { SetSelfReleaseBuildID("") })
			id, err := LoadIdentity()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("mixed/missing release was accepted: %+v", id)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			if id == nil {
				t.Fatal("expected an agent identity, got host scope")
			}
			if tc.self != "" && id.ReleaseBuildID != tc.self {
				t.Fatalf("identity release %q, want %q", id.ReleaseBuildID, tc.self)
			}
		})
	}
}

// TestLoadIdentityHostScopeUnaffected pins that the bind never fires for a
// process with no run.json: the host operator loads nil regardless of a
// recorded self identity.
func TestLoadIdentityHostScopeUnaffected(t *testing.T) {
	t.Setenv("MC_RUN_JSON", filepath.Join(t.TempDir(), "absent.json"))
	SetSelfReleaseBuildID("REL-A")
	t.Cleanup(func() { SetSelfReleaseBuildID("") })
	id, err := LoadIdentity()
	if err != nil || id != nil {
		t.Fatalf("host scope must be (nil, nil), got (%v, %v)", id, err)
	}
}
