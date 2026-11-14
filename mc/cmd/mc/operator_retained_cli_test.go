package main_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRetainedHostWritesUseOperatorReceiptsAndUpdateGuard(t *testing.T) {
	spine := initSpine(t)
	created := runMC(t, spineEnv(spine), "", "task", "add", "Retained task", "--project", "ws-test", "--request-id", "retained-task")
	if created.code != 0 {
		t.Fatal(created.stderr)
	}
	replay := runMC(t, spineEnv(spine), "", "task", "add", "Retained task", "--project", "ws-test", "--request-id", "retained-task")
	if replay.code != 0 || replay.stdout != created.stdout {
		t.Fatalf("The repeated task request changed its result: %s", replay.stdout)
	}
	receipt := runMC(t, spineEnv(spine), "", "operation", "get", "retained-task")
	op := receipt.json["operation"].(map[string]any)
	if op["origin"] != "external" || op["target"] != "ws-test" || op["state"] != "complete" {
		t.Fatalf("The retained command lost its origin or target: %v", op)
	}
	state := filepath.Join(filepath.Dir(spine), "state")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "install-maintenance.json"), []byte(`{"id":"installation"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"task", "add", "Blocked task", "--project", "ws-test"},
		{"project", "add", "blocked", "--title", "Blocked Project", "--sandbox-profile", "default"},
	} {
		refused := runMC(t, spineEnv(spine), "", args...)
		if refused.code != 1 || refused.json["error"].(map[string]any)["code"] != "update-incomplete" {
			t.Fatalf("The retained command bypassed the update guard: %v %s", args, refused.stdout)
		}
	}
}
