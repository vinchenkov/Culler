package distribution

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestLatestTestReleaseUsesTheNormalDownloadPath(t *testing.T) {
	record := fixtureRecord()
	record.TestRelease = true
	record.Tag = "test-" + record.Commit
	client := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/culler/releases/latest":
			_ = json.NewEncoder(w).Encode(githubRelease{Tag: record.Tag, Assets: []releaseAsset{
				{ID: 1, Name: RecordName},
				{ID: 2, Name: record.Installer.Name, Size: record.Installer.Size},
				{ID: 3, Name: record.Package.Name, Size: record.Package.Size},
			}})
		case "/repos/owner/culler/releases/assets/1":
			_ = json.NewEncoder(w).Encode(record)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	got, err := client.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.TestRelease || got.Tag != record.Tag || got.Commit != record.Commit {
		t.Fatalf("the downloaded release lost its test identity: %+v", got)
	}
}

func TestTestReleaseRequiresItsExactCommitTag(t *testing.T) {
	r := fixtureRecord()
	r.TestRelease = true
	r.Tag = "test-" + r.Commit
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"v" + r.Version, "test-" + strings.Repeat("b", 40), "test-" + r.Commit[:12]} {
		r.Tag = tag
		if err := r.Validate(); err == nil {
			t.Fatalf("accepted a different tag: %s", tag)
		}
	}
	r.Tag = "test-" + r.Commit
	r.TestRelease = false
	if err := r.Validate(); err == nil {
		t.Fatal("accepted a test tag without the test release field")
	}
}

func TestTestReleasePolicyRestoresVersionAndMigrationChecks(t *testing.T) {
	original := updatePolicyDefaults
	t.Cleanup(func() { updatePolicyDefaults = original })
	previous := policyFixture(t)
	previous.Version = "9.0.0"
	candidate := policyFixture(t)
	candidate.TestRelease = true
	candidate.Commit = strings.Repeat("e", 40)
	candidate.Schema = strings.Repeat("d", 64)
	candidate.Update.Formats["spine"] = candidate.Schema
	candidate.Update.Accepted["spine"] = []string{candidate.Schema}
	var policy map[string]any
	if err := json.Unmarshal(original, &policy); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, false} {
		policy["test_releases"] = enabled
		updatePolicyDefaults, _ = json.Marshal(policy)
		for _, version := range []string{"0.2.1", previous.Version, "10.0.0"} {
			candidate.Version = version
			err := ValidateUpdateSource(candidate, previous)
			if enabled && err != nil || !enabled && err == nil {
				t.Fatalf("test_releases=%v, version=%s: %v", enabled, version, err)
			}
		}
	}
	policy["test_releases"] = true
	updatePolicyDefaults, _ = json.Marshal(policy)
	candidate.TestRelease = false
	if err := ValidateUpdateSource(candidate, previous); err == nil {
		t.Fatal("a public release bypassed its update policy")
	}
	candidate.TestRelease = true
	candidate.Update.Formats["spine"] = "invalid"
	if err := ValidateUpdateSource(candidate, previous); err == nil {
		t.Fatal("a test release accepted an invalid format inventory")
	}
}
