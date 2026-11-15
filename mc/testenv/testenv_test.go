package testenv_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"mc/testenv"
)

func TestIsolateRemovesRunVariablesAndHidesTheRunIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MC_SPINE", "/mc/spine/spine.db")
	t.Setenv("CODEX_HOME", "/mc/codex")
	t.Setenv("MC_TEST_CONTROL", "kept")
	t.Setenv("MC_RUN_JSON", "/mc/run.json")
	if err := testenv.Isolate(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"MC_SPINE", "CODEX_HOME"} {
		if _, ok := os.LookupEnv(name); ok {
			t.Fatalf("%s remains in the test environment", name)
		}
	}
	if got := os.Getenv("MC_TEST_CONTROL"); got != "kept" {
		t.Fatalf("MC_TEST_CONTROL = %q, want the test control value", got)
	}
	if got := os.Getenv("MC_RUN_JSON"); got != filepath.Join(dir, "absent-run.json") {
		t.Fatalf("MC_RUN_JSON = %q", got)
	}
	if _, err := os.Stat(os.Getenv("MC_RUN_JSON")); !os.IsNotExist(err) {
		t.Fatalf("the test identity exists: %v", err)
	}
}

// The Bun test preload clears the same run variables.
func TestTheBunPreloadNamesTheSameRunVariables(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "runner", "shared", "test-environment.ts"))
	if err != nil {
		t.Fatal(err)
	}
	list := regexp.MustCompile(`(?s)RUN_VARIABLES = \[(.*?)\]`).FindSubmatch(body)
	if list == nil {
		t.Fatal("the Bun preload has no RUN_VARIABLES list")
	}
	var names []string
	for _, match := range regexp.MustCompile(`"([A-Z_]+)"`).FindAllSubmatch(list[1], -1) {
		names = append(names, string(match[1]))
	}
	if !slices.Equal(names, testenv.RunVariables()) {
		t.Fatalf("the Bun preload names %v, want %v", names, testenv.RunVariables())
	}
	for _, name := range []string{"CODEX_HOME", "MC_SPINE", "MC_TOKEN"} {
		if !slices.Contains(names, name) {
			t.Fatalf("the Bun preload omits %s", name)
		}
	}
}
