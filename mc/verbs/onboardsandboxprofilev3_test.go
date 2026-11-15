package verbs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The generated sandbox profile is v3 because `readonly_mounts` left it with
// the read-only Project. canonicalOnboardSandboxProfile decodes with
// DisallowUnknownFields, so a v2 body that still carries the field
// refuses rather than being read past — and the section stays unwedgeable
// because the body is regenerated from the same operator inputs on every run.
func TestOnboardSandboxProfileV2BodyRefusesAndV3RegeneratesCleanly(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatalf("create fixture home: %v", err)
	}
	t.Setenv("MC_HOME", home)
	root := grWorkspace(t)
	a := OnboardArgs{
		Project: "ws-profile", WorkspaceRoot: root,
		AcceptGeneratedSandbox: true,
	}

	profile, body, err := canonicalOnboardSandboxProfile(a)
	if err != nil {
		t.Fatalf("generate canonical profile: %v", err)
	}
	if profile.Version != onboardSandboxProfileVersion || onboardSandboxProfileVersion != 3 {
		t.Fatalf("generated profile version = %d, want 3", profile.Version)
	}
	if strings.Contains(body, "readonly_mounts") {
		t.Fatalf("v3 profile still carries the reference plane: %s", body)
	}
	// Every workspace is mutable, so the artifact plane is unconditional.
	if len(profile.ArtifactRoots) != 1 || profile.ArtifactRoots[0] != ProjectArtifactRoot(root) {
		t.Fatalf("v3 artifact plane = %v", profile.ArtifactRoots)
	}

	// The regenerated body is accepted as its own canonical frame.
	replay := a
	replay.ProjectSandboxProfile = body
	if _, _, err := canonicalOnboardSandboxProfile(replay); err != nil {
		t.Fatalf("v3 body replay: %v", err)
	}

	// A v2 body — version 2, with readonly_mounts — refuses.
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("decode generated body: %v", err)
	}
	raw["version"] = 2
	raw["readonly_mounts"] = []string{root}
	legacy, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("encode v2 body: %v", err)
	}
	stale := a
	stale.ProjectSandboxProfile = string(legacy)
	if _, _, err := canonicalOnboardSandboxProfile(stale); err == nil {
		t.Fatal("a v2 sandbox profile body carrying readonly_mounts was accepted")
	}
}
