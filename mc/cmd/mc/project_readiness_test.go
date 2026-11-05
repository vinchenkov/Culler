package main

import (
	"reflect"
	"testing"

	"mc/deployment"
	"mc/routing"
)

func TestAgentReadinessReadsOnlyTheCurrentConfiguration(t *testing.T) {
	routes := []routing.Route{
		{Role: "worker", Harness: "claude-sdk", Binding: "daily"},
		{Role: "packager", Harness: "claude-sdk", Binding: "daily"},
		{Role: "verifier", Harness: "codex", Binding: "review"},
		{Role: "scribe", Harness: "codex", Binding: "absent"},
		{Role: "arbiter", Harness: "codex", Binding: "draft"},
		{Role: "homie", Harness: "fake", Binding: "fake"},
	}
	profiles := []deployment.AgentProfileView{
		{ID: "daily", Name: "Daily", Enabled: true, AccessStatus: "connected"},
		{ID: "review", Name: "Review", Enabled: true, AccessStatus: "disconnected"},
		{ID: "draft", Name: "Draft", Enabled: false, AccessStatus: "no_token"},
	}
	issues, assigned := agentReadinessIssues(routes, profiles, true)
	want := []string{
		"Complete access for Review in Model configuration.",
		"Select an agent profile for scribe in Model configuration.",
		"Complete access for Draft in Model configuration.",
	}
	if !reflect.DeepEqual(issues, want) {
		t.Fatalf("issues = %q", issues)
	}
	if len(assigned) != 3 || assigned[0].ID != "daily" || assigned[1].ID != "review" || assigned[2].ID != "draft" {
		t.Fatalf("assigned = %+v", assigned)
	}
	issues, _ = agentReadinessIssues(routes[:2], profiles, false)
	if len(issues) != 0 {
		t.Fatalf("An enabled agent profile with connection access blocked readiness: %q", issues)
	}
	issues, _ = agentReadinessIssues(routes[5:], profiles, false)
	if !reflect.DeepEqual(issues, []string{"Select an agent profile for homie in Model configuration."}) {
		t.Fatalf("issues without the fake harness = %q", issues)
	}
}
