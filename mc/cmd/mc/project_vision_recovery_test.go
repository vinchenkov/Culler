package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestVisionRecoveryPreservesExternalRestore(t *testing.T) {
	for _, initial := range []string{"", "The original Project goal.\n"} {
		name := "absent"
		if initial != "" {
			name = "present"
		}
		t.Run(name, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(home, 0700); err != nil {
				t.Fatal(err)
			}
			env := []string{"MC_HOME=" + home, "MC_SPINE=" + filepath.Join(home, "spine.db")}
			if result := runMC(t, env, "", "onboard", "home"); result.code != 0 {
				t.Fatal(result.stderr)
			}
			call := func(action string, request map[string]any) map[string]any {
				t.Helper()
				body, err := json.Marshal(request)
				if err != nil {
					t.Fatal(err)
				}
				result := runMC(t, env, string(body), "project", action)
				if result.code != 0 {
					t.Fatalf("%s: %s %s", action, result.stdout, result.stderr)
				}
				return result.json
			}
			workspace := filepath.Join(home, "project")
			if err := os.Mkdir(workspace, 0700); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(workspace, "VISION.md")
			if initial != "" {
				if err := os.WriteFile(destination, []byte(initial), 0600); err != nil {
					t.Fatal(err)
				}
			}
			preview := call("setup-preview", map[string]any{"path": workspace})["workspace"].(map[string]any)
			draft := call("setup-save", map[string]any{
				"id": "project", "title": "Project", "path": workspace, "identity": preview["identity"], "confirm_access": true,
			})["draft"].(map[string]any)
			content := "Help people develop a recipe collection.\n"
			draft = call("vision-edit", map[string]any{"id": "project", "revision": draft["revision"], "content": content})["draft"].(map[string]any)
			authorization := map[string]any{"id": "project", "revision": draft["revision"], "workspace_identity": draft["workspace_identity"], "workspace_revision": draft["workspace_revision"]}
			queued := call("vision-submit", authorization)["draft"].(map[string]any)
			submission := queued["submission"].(map[string]any)
			apply := map[string]any{"id": "project", "submission_id": submission["submission_id"]}
			claim := call("vision-claim-apply", apply)
			if claim["already_attempted"] != false {
				t.Fatalf("first claim = %+v", claim)
			}
			// The file save completes, then the host stops before its completion receipt.
			if err := os.WriteFile(destination, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			// An external editor restores the previous file state before recovery.
			if initial == "" {
				if err := os.Remove(destination); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(destination, []byte(initial), 0600); err != nil {
				t.Fatal(err)
			}
			recovered := call("vision-apply", apply)["draft"].(map[string]any)
			if recovered["setup_status"] != "vision" || recovered["submission"].(map[string]any)["status"] != "failed" {
				t.Fatalf("recovery = %+v", recovered)
			}
			if initial == "" {
				if _, err := os.Stat(destination); !os.IsNotExist(err) {
					t.Fatalf("recovery replaced the external deletion: %v", err)
				}
			} else {
				body, err := os.ReadFile(destination)
				if err != nil || string(body) != initial {
					t.Fatalf("recovery replaced the external text: %q, %v", body, err)
				}
			}
			retry := call("vision-submit", authorization)["draft"].(map[string]any)
			if retry["submission"].(map[string]any)["submission_id"] != submission["submission_id"] {
				t.Fatal("retry created another submission")
			}
			completed := call("vision-apply", apply)["draft"].(map[string]any)
			if completed["setup_status"] != "configured" {
				t.Fatalf("explicit retry = %+v", completed)
			}
			again := call("vision-apply", apply)["draft"].(map[string]any)
			if again["submission"].(map[string]any)["submission_id"] != submission["submission_id"] {
				t.Fatal("The completed retry changed its submission.")
			}
			body, err := os.ReadFile(destination)
			if err != nil || string(body) != content {
				t.Fatalf("retry saved %q, %v", body, err)
			}
		})
	}
}
