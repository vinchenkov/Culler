package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDashboardProjectSetupLifecycle(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	os.Chmod(home, 0700)
	spine := filepath.Join(home, "spine.db")
	env := []string{"MC_HOME=" + home, "MC_SPINE=" + spine}
	if result := runMC(t, env, "", "onboard", "home"); result.code != 0 {
		t.Fatal(result.stderr)
	}
	routes := filepath.Join(home, "routes.md")
	if err := os.WriteFile(routes, []byte(fakeRoutingMarkdown), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"onboard", "routing", "--runtime-bindings", "fake", "--routing-file", routes}, {"onboard", "surfaces"}} {
		if result := runMC(t, env, "", args...); result.code != 0 {
			t.Fatalf("%v: %s", args, result.stderr)
		}
	}
	call := func(action string, request map[string]any) mcResult {
		t.Helper()
		body, _ := json.Marshal(request)
		return runMC(t, env, string(body), "project", action)
	}
	must := func(action string, request map[string]any) map[string]any {
		t.Helper()
		result := call(action, request)
		if result.code != 0 {
			t.Fatalf("%s: %s %s", action, result.stdout, result.stderr)
		}
		return result.json
	}
	for _, id := range []string{"empty", "new", "imported"} {
		path := filepath.Join(home, id)
		initial := ""
		if id != "new" {
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
		}
		if id == "imported" {
			initial = "# Vision\nHelp people develop useful software.\n"
			if err := os.WriteFile(filepath.Join(path, "VISION.md"), []byte(initial), 0600); err != nil {
				t.Fatal(err)
			}
		}
		ws := must("setup-preview", map[string]any{"path": path + "/"})["workspace"].(map[string]any)
		request := map[string]any{"id": id, "title": id, "path": path, "identity": ws["identity"], "create": true, "confirm_access": true}
		registered := must("setup-save", request)
		draft := registered["draft"].(map[string]any)
		if draft["content"] != initial || draft["setup_status"] != "vision" {
			t.Fatalf("initial draft: %v", draft)
		}
		if draft["workspace_has_files"] != (id == "imported") {
			t.Fatalf("workspace context: %v", draft)
		}
		if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
			t.Fatal("registration did not create the repository")
		}
		retried := must("setup-save", request)["draft"].(map[string]any)
		if retried["revision"] != draft["revision"] {
			t.Fatal("registration retry changed the draft")
		}
		row := must("details", map[string]any{"id": id})["project"].(map[string]any)
		if row["status"] != "paused" || row["setup_status"] != "vision" {
			t.Fatalf("registered state: %v", row)
		}
		start := map[string]any{"id": id, "identity": draft["workspace_identity"], "revision": draft["revision"]}
		if result := call("start", start); result.code != 1 || !strings.Contains(result.stderr, "Save Vision") {
			t.Fatalf("Start accepted an unsubmitted Vision: %s %s", result.stdout, result.stderr)
		}
		if id != "imported" {
			draft = must("vision-edit", map[string]any{"id": id, "revision": draft["revision"], "content": "Help people understand their household expenses."})["draft"].(map[string]any)
			if _, err := os.Stat(filepath.Join(path, "VISION.md")); !os.IsNotExist(err) {
				t.Fatal("a draft edit changed the workspace")
			}
		}
		submitted := must("vision-submit", map[string]any{"id": id, "revision": draft["revision"], "workspace_identity": draft["workspace_identity"], "workspace_revision": draft["workspace_revision"]})["draft"].(map[string]any)
		submission := submitted["submission"].(map[string]any)
		apply := map[string]any{"id": id, "submission_id": submission["submission_id"]}
		saved := must("vision-apply", apply)["draft"].(map[string]any)
		if saved["setup_status"] != "configured" {
			t.Fatalf("completed state: %v", saved)
		}
		body, err := os.ReadFile(filepath.Join(path, "VISION.md"))
		if err != nil || string(body) != draft["content"] {
			t.Fatalf("workspace Vision: %q %v", body, err)
		}
		must("vision-apply", apply)
		row = must("details", map[string]any{"id": id})["project"].(map[string]any)
		if row["status"] != "paused" {
			t.Fatal("Vision completion resumed the Project")
		}
		start["revision"], start["submission_id"] = saved["revision"], submission["submission_id"]
		if err := os.WriteFile(filepath.Join(path, "VISION.md"), []byte("An external Project goal."), 0600); err != nil {
			t.Fatal(err)
		}
		if result := call("start", start); result.code != 1 || !strings.Contains(result.stderr, "workspace Vision changed") {
			t.Fatalf("Start accepted an external Vision change: %s %s", result.stdout, result.stderr)
		}
		if err := os.WriteFile(filepath.Join(path, "VISION.md"), body, 0600); err != nil {
			t.Fatal(err)
		}
		started := must("start", start)["draft"].(map[string]any)
		if started["setup_status"] != "configured" || started["project_status"] != "paused" || started["setup_id"] == "" {
			t.Fatalf("Project start state: %v", started)
		}
		must("start", start)
	}
	catalog := runMC(t, env, "", "project", "workspaces")
	if catalog.code != 0 || len(catalog.json["workspaces"].([]any)) != 3 {
		t.Fatalf("workspace catalog: %v %s", catalog.json, catalog.stderr)
	}
}

func TestProjectSetupRequiresHostScope(t *testing.T) {
	home, spine, _ := agentSurfaceHome(t)
	env := append(runJSONEnv(t, spine, "setup-agent", "pipeline", "worker"), "MC_HOME="+home)
	for _, action := range []string{"setup-preview", "setup-save", "details", "vision-save", "vision-workspace", "vision-apply", "start", "readiness", "workspaces"} {
		got := runMC(t, env, `{"vision":"private-vision"}`, "project", action)
		if got.code != 1 || !strings.Contains(got.stderr, "host scope only") {
			t.Fatalf("scope: %s %d %s", action, got.code, got.stderr)
		}
		if strings.Contains(got.stdout+got.stderr, "private-vision") {
			t.Fatal("the scope rejection exposed input")
		}
	}
}

func TestVisionSavePreservesExternalChangesAndRecoversAnInterruptedSave(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	os.Chmod(home, 0700)
	env := []string{"MC_HOME=" + home, "MC_SPINE=" + filepath.Join(home, "spine.db")}
	if result := runMC(t, env, "", "onboard", "home"); result.code != 0 {
		t.Fatal(result.stderr)
	}
	call := func(action string, request map[string]any) map[string]any {
		t.Helper()
		body, _ := json.Marshal(request)
		result := runMC(t, env, string(body), "project", action)
		if result.code != 0 {
			t.Fatalf("%s: %s %s", action, result.stdout, result.stderr)
		}
		return result.json
	}
	for _, id := range []string{"external", "recovery", "later-draft"} {
		path := filepath.Join(home, id)
		ws := call("setup-preview", map[string]any{"path": path})["workspace"].(map[string]any)
		draft := call("setup-save", map[string]any{"id": id, "title": id, "path": path, "identity": ws["identity"], "create": true, "confirm_access": true})["draft"].(map[string]any)
		content := "Help people record useful ideas."
		draft = call("vision-edit", map[string]any{"id": id, "revision": draft["revision"], "content": content})["draft"].(map[string]any)
		submitted := call("vision-submit", map[string]any{"id": id, "revision": draft["revision"], "workspace_identity": draft["workspace_identity"], "workspace_revision": draft["workspace_revision"]})["draft"].(map[string]any)
		submission := submitted["submission"].(map[string]any)
		if id == "recovery" {
			call("vision-claim-apply", map[string]any{"id": id, "submission_id": submission["submission_id"]})
		}
		if id == "external" {
			content = "An external editor changed the goal."
		}
		if id == "later-draft" {
			call("vision-edit", map[string]any{"id": id, "revision": draft["revision"], "content": "Later ideas remain a draft."})
		} else if err := os.WriteFile(filepath.Join(path, "VISION.md"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		result := call("vision-apply", map[string]any{"id": id, "submission_id": submission["submission_id"]})["draft"].(map[string]any)
		body, err := os.ReadFile(filepath.Join(path, "VISION.md"))
		if err != nil || string(body) != content {
			t.Fatalf("workspace: %q, %v", body, err)
		}
		if id == "external" {
			if result["setup_status"] != "vision" || result["submission"].(map[string]any)["status"] != "failed" {
				t.Fatalf("external change: %v", result)
			}
		} else if result["setup_status"] != "configured" {
			t.Fatalf("completion: %v", result)
		}
		if id == "later-draft" && result["content"] != "Later ideas remain a draft." {
			t.Fatal("the save discarded a later draft")
		}
	}
}

func TestVisionOpenPreservesExistingProjectState(t *testing.T) {
	spine := initSpine(t)
	call := func(action string, request map[string]any) map[string]any {
		t.Helper()
		body, _ := json.Marshal(request)
		result := runMC(t, spineEnv(spine), string(body), "project", action)
		if result.code != 0 {
			t.Fatalf("%s: %s %s", action, result.stdout, result.stderr)
		}
		return result.json
	}
	for _, status := range []string{"active", "paused"} {
		if status == "paused" {
			result := runMC(t, spineEnv(spine), "", "project", "pause", "ws-test")
			if result.code != 0 {
				t.Fatal(result.stderr)
			}
		}
		draft := call("vision-open", map[string]any{"id": "ws-test"})["draft"].(map[string]any)
		if draft["setup_status"] != "ready" {
			t.Fatalf("existing Project: %v", draft)
		}
		draft = call("vision-edit", map[string]any{"id": "ws-test", "revision": draft["revision"], "content": "Help people develop clear goals while the Project is " + status + "."})["draft"].(map[string]any)
		submitted := call("vision-submit", map[string]any{"id": "ws-test", "revision": draft["revision"], "workspace_identity": draft["workspace_identity"], "workspace_revision": draft["workspace_revision"]})["draft"].(map[string]any)
		submission := submitted["submission"].(map[string]any)
		call("vision-apply", map[string]any{"id": "ws-test", "submission_id": submission["submission_id"]})
		row := call("details", map[string]any{"id": "ws-test"})["project"].(map[string]any)
		if row["status"] != status || row["setup_status"] != "ready" {
			t.Fatalf("existing Project state: %v", row)
		}
	}
}

func TestProjectRegistrationRetryRejectsAReplacedFolderBeforeMutation(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	os.Chmod(home, 0700)
	env := []string{"MC_HOME=" + home, "MC_SPINE=" + filepath.Join(home, "spine.db")}
	if result := runMC(t, env, "", "onboard", "home"); result.code != 0 {
		t.Fatal(result.stderr)
	}
	path := filepath.Join(home, "project")
	request := map[string]any{"id": "project", "title": "Project", "path": path, "create": true, "confirm_access": true}
	body, _ := json.Marshal(map[string]any{"path": path})
	preview := runMC(t, env, string(body), "project", "setup-preview")
	if preview.code != 0 {
		t.Fatal(preview.stderr)
	}
	request["identity"] = preview.json["workspace"].(map[string]any)["identity"]
	body, _ = json.Marshal(request)
	registered := runMC(t, env, string(body), "project", "setup-save")
	if registered.code != 0 {
		t.Fatal(registered.stderr)
	}
	if err := os.Rename(path, path+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	retried := runMC(t, env, string(body), "project", "setup-save")
	if retried.code == 0 {
		t.Fatal("the retry accepted a replacement folder")
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		t.Fatalf("the retry modified the replacement folder: %v %v", entries, err)
	}
}

func TestVisionSaveStopsAfterProjectArchive(t *testing.T) {
	spine := initSpine(t)
	call := func(action string, request map[string]any) map[string]any {
		t.Helper()
		body, _ := json.Marshal(request)
		result := runMC(t, spineEnv(spine), string(body), "project", action)
		if result.code != 0 {
			t.Fatalf("%s: %s %s", action, result.stdout, result.stderr)
		}
		return result.json
	}
	workspace := call("details", map[string]any{"id": "ws-test"})["workspace"].(map[string]any)
	path := filepath.Join(workspace["path"].(string), "VISION.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	draft := call("vision-open", map[string]any{"id": "ws-test"})["draft"].(map[string]any)
	draft = call("vision-edit", map[string]any{"id": "ws-test", "revision": draft["revision"], "content": "Help people develop a new Project goal."})["draft"].(map[string]any)
	submitted := call("vision-submit", map[string]any{"id": "ws-test", "revision": draft["revision"], "workspace_identity": draft["workspace_identity"], "workspace_revision": draft["workspace_revision"]})["draft"].(map[string]any)
	submission := submitted["submission"].(map[string]any)
	archived := runMC(t, spineEnv(spine), "", "project", "archive", "ws-test")
	if archived.code != 0 {
		t.Fatal(archived.stderr)
	}
	result := call("vision-apply", map[string]any{"id": "ws-test", "submission_id": submission["submission_id"]})["draft"].(map[string]any)
	if result["submission"].(map[string]any)["status"] != "failed" {
		t.Fatalf("archived submission: %v", result)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("the save changed an archived workspace: %q %v", after, err)
	}
	if pending := call("vision-pending", map[string]any{})["submissions"].([]any); len(pending) != 0 {
		t.Fatalf("the archived submission remains pending: %v", pending)
	}
}
