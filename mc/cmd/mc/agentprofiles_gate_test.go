package main

import (
	"path/filepath"
	"strings"
	"testing"

	"mc/routing"
)

func TestAgentProfileRuntimeAuthoritiesRequireTestBuild(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MC_HOME", home)
	t.Setenv("MC_RUN_JSON", filepath.Join(home, "absent-run.json"))
	result, err := commandAgentProfile([]string{"test-runtime-authorities"}, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	_, enabled := routing.ActiveRegistry()
	if result.(map[string]any)["enabled"] != enabled {
		t.Fatalf("The runtime authority gate differs from the compiled test control: %v", result)
	}
}
