package main_test

import "testing"

func TestOperatorRoleCommandsPreserveCanonicalRoleAndInput(t *testing.T) {
	spine := initSpine(t)
	read := func() map[string]any {
		t.Helper()
		result := runMC(t, spineEnv(spine), "", "project", "role-show", "--project", "ws-test", "--role", "strategist(propose)")
		if result.code != 0 {
			t.Fatalf("The canonical role read failed: %s", result.stderr)
		}
		return result.json["current"].(map[string]any)
	}
	if read()["revision"] != float64(0) {
		t.Fatal("The initial role revision is invalid.")
	}
	body := `{"project":"ws-test","role":"strategist(propose)","expected_revision":0,"instructions":"Use the accepted Project goals."}`
	saved := runMC(t, spineEnv(spine), body, "project", "role-save")
	if saved.code != 0 {
		t.Fatalf("The host lost its retained role input: %s", saved.stderr)
	}
	if current := read(); current["revision"] != float64(1) || current["instructions"] != "Use the accepted Project goals." {
		t.Fatalf("The role save changed its input: %v", current)
	}
	started := runMC(t, spineEnv(spine), "", "collie", "start", "--from", "cli:role-input", "--allow", "project.role-save")
	if started.code != 0 {
		t.Fatal(started.stderr)
	}
	env := collieJSONEnv(t, spine, started.json["session_id"].(string), []string{"project.role-save"})
	body = `{"project":"ws-test","role":"strategist(propose)","expected_revision":1,"instructions":"Inspect the current Project goals."}`
	saved = runMC(t, env, body, "project", "role-save", "--request", "-")
	if saved.code != 0 {
		t.Fatalf("Collie lost its retained role input: %s", saved.stderr)
	}
	if current := read(); current["revision"] != float64(2) || current["instructions"] != "Inspect the current Project goals." {
		t.Fatalf("Collie changed the role input: %v", current)
	}
}
