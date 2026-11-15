package distribution

import (
	"path/filepath"
	"strings"
	"testing"
)

func policyFixture(t *testing.T) Record {
	t.Helper()
	r := fixtureRecord()
	version, policy, err := BuildUpdatePolicy(filepath.Join("..", ".."), r.Schema)
	if err != nil {
		t.Fatal(err)
	}
	r.Version, r.Update = version, policy
	r.Dependencies = []DependencyPin{{Path: "fixture.json", SHA256: strings.Repeat("a", 64)}}
	return r
}
func TestUpdatePolicyAcceptsSkippedReleasesWithSameFormats(t *testing.T) {
	previous := policyFixture(t)
	candidate := policyFixture(t)
	candidate.Commit = strings.Repeat("e", 40)
	candidate.Sequence++
	candidate.Version = "1.3.0"
	if err := ValidateUpdateSource(candidate, previous); err != nil {
		t.Fatal(err)
	}
}

func TestUpdatePolicyRequiresANewVersionForADifferentCommit(t *testing.T) {
	previous := policyFixture(t)
	candidate := policyFixture(t)
	if err := ValidateUpdateSource(candidate, previous); err != nil {
		t.Fatal("same release repair:", err)
	}
	candidate.Commit = strings.Repeat("e", 40)
	candidate.Sequence++
	if err := ValidateUpdateSource(candidate, previous); err == nil || !strings.Contains(err.Error(), "newer public version") {
		t.Fatalf("reused version: %v", err)
	}
	for _, version := range []string{"01.0.0", "1.0.0-beta"} {
		candidate.Version = version
		if err := candidate.Update.validate(version, candidate.Schema); err == nil {
			t.Fatalf("invalid public version accepted: %s", version)
		}
	}
}
func TestUpdatePolicyRejectsUndeclaredNativeChangeAndUnreleasedSource(t *testing.T) {
	previous := policyFixture(t)
	candidate := policyFixture(t)
	candidate.Commit = strings.Repeat("e", 40)
	candidate.Sequence++
	candidate.Version = "1.1.0"
	candidate.Update.Formats["opencode"] = "3.0.0"
	candidate.Update.Accepted["opencode"] = []string{"3.0.0"}
	if err := ValidateUpdateSource(candidate, previous); err == nil || !strings.Contains(err.Error(), "opencode") {
		t.Fatalf("native change: %v", err)
	}
	previous.Update = nil
	previous.Version = ""
	if err := ValidateUpdateSource(candidate, previous); err == nil {
		t.Fatal("unreleased source accepted")
	}
}
func TestUpdatePolicyRejectsDowngradeAndMissingIntermediate(t *testing.T) {
	previous := policyFixture(t)
	candidate := policyFixture(t)
	candidate.Commit = strings.Repeat("e", 40)
	candidate.Sequence++
	previous.Version = "2.0.0"
	if err := ValidateUpdateSource(candidate, previous); err == nil {
		t.Fatal("downgrade accepted")
	}
	previous.Version = "1.0.0"
	candidate.Version = "2.0.0"
	candidate.Update.MinimumVersion = "1.5.0"
	if err := ValidateUpdateSource(candidate, previous); err == nil || !strings.Contains(err.Error(), "intermediate") {
		t.Fatalf("minimum: %v", err)
	}
}
func TestUpdatePolicyRequiresMigrationForNewSpineOrConfiguration(t *testing.T) {
	for _, name := range []string{"spine", "configuration", "credentials"} {
		t.Run(name, func(t *testing.T) {
			previous := policyFixture(t)
			candidate := policyFixture(t)
			candidate.Commit = strings.Repeat("e", 40)
			candidate.Sequence++
			candidate.Version = "1.1.0"
			next := "next-format"
			if name == "spine" {
				next = strings.Repeat("1", 64)
				candidate.Schema = next
			}
			candidate.Update.Formats[name] = next
			candidate.Update.Accepted[name] = []string{previous.Update.Formats[name], next}
			if err := ValidateUpdateSource(candidate, previous); err == nil || !strings.Contains(err.Error(), "migration") {
				t.Fatalf("format conversion: %v", err)
			}
		})
	}
}
func TestDependencyInventoryIncludesOptionalAndBundledTools(t *testing.T) {
	rows, err := DependencyInventory(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, row := range rows {
		if !hexDigest.MatchString(row.SHA256) {
			t.Fatal("invalid digest")
		}
		paths[row.Path] = true
	}
	for _, path := range []string{"release/opencode/pins.json", "runner/openwiki/package-lock.json", "runner/image/project-tools/package-lock.json", "mc/deployment/agentsview.json", "mc/deployment/hostbun.json"} {
		if !paths[path] {
			t.Fatal("missing", path)
		}
	}
}
