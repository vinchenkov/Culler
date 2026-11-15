package verbs

// The classification tests. Every case here is a name that really exists on
// the operator's machine or a near-miss chosen because a prefix check alone
// would misclassify it.

import (
	"reflect"
	"testing"
)

func TestCullerVolumesSelectsOnlyOurs(t *testing.T) {
	// The live daemon: five spine volumes (one live deployment, four from dead
	// scratch homes), one leaked e2e volume, and unrelated projects.
	got := CullerVolumes([]string{
		"autogpt_platform_clamav-data",
		"mc-e2e-spine-1312717434",
		"mc-spine-0287f755f1b7",
		"mc-spine-09720f462528",
		"mc-spine-6c748362e524",
		"mc-spine-bf91fd73e102",
		"mc-spine-e8292e37f8e1",
		"mc-runtime-e8292e37f8e1",
		"mcspike-01-spine",
		"onecli_pgdata",
		"supabase_db_dev",
	})
	want := []string{
		"mc-e2e-spine-1312717434",
		"mc-spine-0287f755f1b7",
		"mc-spine-09720f462528",
		"mc-spine-6c748362e524",
		"mc-spine-bf91fd73e102",
		"mc-spine-e8292e37f8e1",
		"mc-runtime-e8292e37f8e1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CullerVolumes:\n got %v\nwant %v", got, want)
	}
}

// Pipeline containers (`mc-<project>-<run_id>`) are NOT selected by name: a
// Project id is operator-chosen, so the only grammar that would match them
// would also match an operator's own `mc-…` container. They are swept by
// label (PipelineContainerLabelFilters).
func TestCullerContainersSelectsOnlyOurs(t *testing.T) {
	got := CullerContainers([]string{
		"mc-helper-0287f755f1b7",
		"mc-helper-bf91fd73e102",
		"mc-primary-9f2c1a4b7e0d3c88",
		"mc-run-9f2c1a4b7e0d3c88",
		"mc-setup-9f2c1a4b7e0d3c88",
		"mc-e2e-helper-xyz",
		"mcspike-01-agent",
		"onecli-postgres-1",
		"supabase_studio_dev",
	})
	want := []string{
		"mc-helper-0287f755f1b7",
		"mc-helper-bf91fd73e102",
		"mc-setup-9f2c1a4b7e0d3c88",
		"mc-e2e-helper-xyz",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CullerContainers:\n got %v\nwant %v", got, want)
	}
}

// The pipeline sweep requires BOTH labels: `mc-managed=true` alone is every
// Culler container, and `mc-tier=pipeline` alone could be an
// operator's own label.
func TestPipelineContainerLabelFiltersRequireBothLabels(t *testing.T) {
	want := []string{"--filter", "label=mc-managed=true", "--filter", "label=mc-tier=pipeline"}
	if got := PipelineContainerLabelFilters(); !reflect.DeepEqual(got, want) {
		t.Fatalf("PipelineContainerLabelFilters = %v, want %v", got, want)
	}
}

// The prefix families are the ones that could reach an operator's own object,
// because `mc-` is a plausible thing for a human to name something.
func TestDerivedSuffixKeepsPrefixesFromWidening(t *testing.T) {
	for _, name := range []string{
		"mc-spine-of-my-own",     // human-named, right prefix
		"mc-spine-",              // empty suffix
		"mc-spine-0287F755F1B7",  // uppercase: not our alphabet
		"mc-spine-0287f755f1b",   // 11 chars
		"mc-spine-0287f755f1b7a", // 13 chars
		"mc-spine-zzzzzzzzzzzz",  // right length, wrong alphabet
	} {
		if got := CullerVolumes([]string{name}); len(got) != 0 {
			t.Errorf("CullerVolumes(%q) = %v, want nothing", name, got)
		}
	}
	for _, name := range []string{"mc-helper-mine", "mc-helper-0287f755f1b7x"} {
		if got := CullerContainers([]string{name}); len(got) != 0 {
			t.Errorf("CullerContainers(%q) = %v, want nothing", name, got)
		}
	}
}

func TestCullerLaunchAgentsFindsEveryUnit(t *testing.T) {
	labels, plists := CullerLaunchAgents([]string{
		"/Users/o/Library/LaunchAgents/com.culler.0287f755f1b7.agentsview.plist",
		"/Users/o/Library/LaunchAgents/com.culler.0287f755f1b7.dashboard.plist",
		"/Users/o/Library/LaunchAgents/com.culler.0287f755f1b7.resident.plist",
		"/Users/o/Library/LaunchAgents/com.valvesoftware.steamclean.plist",
		"/Users/o/Library/LaunchAgents/com.culler.notes.txt",
	})
	wantLabels := []string{
		"com.culler.0287f755f1b7.agentsview",
		"com.culler.0287f755f1b7.dashboard",
		"com.culler.0287f755f1b7.resident",
	}
	if !reflect.DeepEqual(labels, wantLabels) {
		t.Fatalf("labels:\n got %v\nwant %v", labels, wantLabels)
	}
	if len(plists) != 3 {
		t.Fatalf("plists = %v, want the three matching files", plists)
	}
	for _, p := range plists {
		if p == "/Users/o/Library/LaunchAgents/com.valvesoftware.steamclean.plist" {
			t.Fatalf("selected an unrelated LaunchAgent: %v", plists)
		}
	}
}

func TestCullerLaunchAgentsIncludesLegacyAndRejectsNearMisses(t *testing.T) {
	labels, _ := CullerLaunchAgents([]string{
		"/agents/com.mission-control.012345abcdef.resident.plist",
		"/agents/com.mission-control.012345abcdef.dashboard.plist",
		"/agents/com.culler.personal.resident.plist",
		"/agents/com.mission-control.012345abcdef.notes.plist",
		"/agents/com.mission-control.012345abcdef.agentsview.plist",
		"/agents/com.culler.012345abcdef.agentsview.extra.plist",
	})
	if len(labels) != 2 {
		t.Fatalf("selected units: %v", labels)
	}
}
