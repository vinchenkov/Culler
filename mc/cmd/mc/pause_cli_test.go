package main_test

import "testing"

func TestPauseCLI(t *testing.T) {
	spine := initSpine(t)
	command := func(args ...string) mcResult {
		t.Helper()
		res := runMC(t, spineEnv(spine), "", args...)
		if res.code != 0 {
			t.Fatalf("%v failed: %s", args, res.stderr)
		}
		return res
	}
	statuses := func(res mcResult) []string {
		t.Helper()
		var out []string
		for _, raw := range res.json["projects"].([]any) {
			out = append(out, raw.(map[string]any)["status"].(string))
		}
		return out
	}
	if got := statuses(command("pause", "status")); len(got) != 1 || got[0] != "active" {
		t.Fatalf("initial pause = %v", got)
	}
	if _, ok := command("pause", "status").json["global_paused"]; ok {
		t.Fatal("The pause status reported a global field.")
	}
	command("pause")
	// A separate process reads the durable pause.
	if got := statuses(command("pause", "status")); got[0] != "paused" {
		t.Fatalf("stored pause = %v", got)
	}
	command("project", "resume", "ws-test")
	if got := statuses(command("pause", "status")); got[0] != "active" {
		t.Fatalf("Project resume = %v", got)
	}
	command("project", "pause", "ws-test")
	if got := statuses(command("resume")); got[0] != "active" {
		t.Fatalf("Resume all retained a pause: %v", got)
	}
	for _, args := range [][]string{{"pause", "bad"}, {"pause", "status", "bad"}, {"resume", "bad"}, {"project", "resume"}} {
		if res := runMC(t, spineEnv(spine), "", args...); res.code != 2 {
			t.Fatalf("%v returned %d", args, res.code)
		}
	}
}
