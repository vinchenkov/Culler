package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The resident resolves the artifact bind of a packet conversation on the
// host. The command stays host-local when other verbs delegate to the helper.
func TestTaskArtifactsCommandResolvesTheTaskFolderOnTheHost(t *testing.T) {
	spine := initSpine(t)
	workspace := queryStr(t, openDB(t, spine), `SELECT s.workspace_root FROM sandbox_profiles s
		JOIN projects p ON p.sandbox_profile = s.id WHERE p.id = 'ws-test'`)
	folder := filepath.Join(workspace, ".mc-artifacts", "21")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"MC_HOME=" + filepath.Dir(spine), "MC_HELPER=mc-helper-that-must-never-be-reached"}

	res := runMC(t, env, "", "__task-artifacts", workspace, "21")
	if res.code != 0 || res.json["destination"] != "/mc/artifacts/ws-test/.mc-artifacts/21" || res.json["source"] != folder {
		t.Fatalf("__task-artifacts gave %d %v (%s), want the bind of %s", res.code, res.json, res.stderr, folder)
	}
	absent := runMC(t, env, "", "__task-artifacts", workspace, "22")
	if source, present := absent.json["source"]; absent.code != 0 || !present || source != nil {
		t.Fatalf("an absent task folder gave %d %v, want a null source", absent.code, absent.json)
	}
	for _, args := range [][]string{{workspace}, {"relative/root", "21"}, {workspace, "0"}, {workspace, "x"}, {workspace, "21", "extra"}} {
		refused := runMC(t, env, "", append([]string{"__task-artifacts"}, args...)...)
		message, _ := refused.json["error"].(map[string]any)["message"].(string)
		if refused.code != 2 || message != "usage: mc __task-artifacts <workspace-root> <task>" {
			t.Fatalf("__task-artifacts %v gave %d %v", args, refused.code, refused.json)
		}
	}
	agent := runMC(t, homieJSONEnv(t, spine, "h-0123456789abcdef", []string{}), "", "__task-artifacts", workspace, "21")
	if agent.code != 1 || !strings.Contains(agent.stderr, "host scope only") {
		t.Fatalf("a Homie caller gave %d %q", agent.code, agent.stderr)
	}
}
