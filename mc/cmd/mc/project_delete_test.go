package main_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectDeleteCLIRequiresHostScopeAndConfirmation(t *testing.T) {
	spine := initSpine(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\n[ \"$1\" = ps ] || exit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, env := range [][]string{
		runJSONEnv(t, spine, "worker", "pipeline", "worker"),
		runJSONEnv(t, spine, "setup", "setup", "setup"),
		homieJSONEnv(t, spine, "homie", []string{"project.delete", "project.delete-preview"}),
	} {
		for _, args := range [][]string{{"project", "delete-preview", "ws-test"}, {"project", "delete", "ws-test", "--confirm"}, {"project", "container-targets", "ws-test"}, {"__project-docker", "ws-test", "--", "create", "image"}} {
			if result := runMC(t, env, "", args...); result.code != 1 {
				t.Fatalf("The agent command %v returned %d: %s", args, result.code, result.stderr)
			}
		}
	}
	if result := runMC(t, spineEnv(spine), "", "project", "delete", "ws-test"); result.code != 2 {
		t.Fatalf("Deletion omitted confirmation: %+v", result)
	}
	if result := runMC(t, spineEnv(spine), "", "project", "pause", "ws-test"); result.code != 0 {
		t.Fatal(result.stderr)
	}
	preview := runMC(t, spineEnv(spine), "", "project", "delete-preview", "ws-test")
	if preview.code != 0 || preview.json["tasks"] != float64(0) || len(preview.json["blockers"].([]any)) != 0 {
		t.Fatalf("The deletion preview failed: %+v", preview)
	}
	if result := runMC(t, spineEnv(spine), "", "project", "delete", "ws-test", "--confirm"); result.code != 0 {
		t.Fatal(result.stderr)
	}
	result := runMC(t, spineEnv(spine), "", "project", "list")
	if result.code != 0 || len(result.json["projects"].([]any)) != 0 {
		t.Fatalf("The Project remains registered: %+v", result)
	}
}
