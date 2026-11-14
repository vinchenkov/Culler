//go:build !test_fake_routing

package main

// Uninstall's honesty tests. The verb removes every deployment's STORAGE but
// can only remove the home DIRECTORIES it was handed, because a runtime object
// name comes from a one-way hash of MC_HOME. Whether it says so is the whole
// difference between an uninstall that is complete and one that only reports
// completion — so it is pinned here rather than left to a comment.

import (
	"reflect"
	"testing"

	"mc/deployment"
)

func TestUnlocatedDeploymentsNamesTheOnesItCannotFind(t *testing.T) {
	// One live machine: one deployment whose home is known, four from dead
	// scratch homes, and one leaked e2e volume that is not a deployment at
	// all.
	report := uninstallReport{
		Volumes: []string{
			"mc-e2e-spine-1312717434",
			"mc-spine-0287f755f1b7",
			"mc-spine-09720f462528",
			"mc-spine-6c748362e524",
		},
		Containers:   []string{"mc-helper-bf91fd73e102", "mc-primary-9f2c1a4b7e0d3c88"},
		LaunchAgents: []string{"com.culler.e8292e37f8e1.resident"},
	}
	// A home whose derived suffix is one of the above must drop out; using a
	// real derivation rather than a hardcoded pair is what keeps this test
	// honest if the hash domain ever changes.
	known := t.TempDir()
	names, err := runtimeNamesForTest(known)
	if err != nil {
		t.Fatal(err)
	}
	report.Volumes = append(report.Volumes, "mc-spine-"+names.Suffix)

	got := unlocatedDeployments(report, []string{known})
	want := []string{"0287f755f1b7", "09720f462528", "6c748362e524", "bf91fd73e102", "e8292e37f8e1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unlocatedDeployments:\n got %v\nwant %v", got, want)
	}
	for _, suffix := range got {
		if suffix == names.Suffix {
			t.Fatalf("reported a deployment whose home WAS supplied: %s", suffix)
		}
		if suffix == "1312717434" {
			t.Fatalf("reported the e2e volume as a deployment")
		}
	}
}

func TestUnionNamesDedupesAndKeepsOrder(t *testing.T) {
	got := unionNames(
		[]string{"mc-helper-a", "mc-primary-b"},
		[]string{"mc-primary-b", "mc-setup-c"},
	)
	want := []string{"mc-helper-a", "mc-primary-b", "mc-setup-c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unionNames:\n got %v\nwant %v", got, want)
	}
}

// runtimeNamesForTest resolves a home the same way the verb does, so the test
// cannot drift from the derivation it is asserting against.
func runtimeNamesForTest(home string) (deployment.Names, error) {
	return deployment.RuntimeNames(home)
}
