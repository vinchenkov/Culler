package main_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	// Aliased: the test suite of this package binds `dispatch` to a helper.
	roles "mc/dispatch"
	"mc/verbs"
)

// brokerTickProject registers one more complete Project with no wiki record,
// so its pipeline round and its wiki round each start a run. The operator
// instructions of the Strategist and of the Scribe have the largest size.
func brokerTickProject(t *testing.T, spine, project string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(filepath.Dir(spine))
	if err != nil {
		t.Fatal(err)
	}
	db := openDB(t, spine)
	workspace := gitFixtureWorkspace(t)
	toolHome := filepath.Join(home, "state", "projects", project, "home")
	if err := os.MkdirAll(toolHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sandbox_profiles (id, workspace_root, artifact_roots, tool_home_dir)
		VALUES (?, ?, '[]', ?)`, project+"-profile", workspace, toolHome); err != nil {
		t.Fatal(err)
	}
	if add := runMC(t, spineEnv(spine), "", "project", "add", project, "--title", project,
		"--sandbox-profile", project+"-profile"); add.code != 0 {
		t.Fatalf("project add %s failed: %s", project, add.stderr)
	}
	recordFixtureEnvironment(t, db, project)
	if _, err := verbs.EnsureProjectArtifactGrant(filepath.Dir(spine), project, workspace); err != nil {
		t.Fatal(err)
	}
	if _, _, err := verbs.EnsureOnboardWorkspaceLayout(
		[]verbs.OnboardWorkspaceRoot{{ID: project, Root: workspace}}, os.Getuid()); err != nil {
		t.Fatal(err)
	}
	revision, instructions := int64(0), strings.Repeat("Keep each page short. ", 65536/22)
	for _, role := range []roles.Role{roles.RoleStrategist, roles.RoleScribe} {
		if _, err := verbs.ProjectRoleSave(db, nil, verbs.RoleSaveRequest{Project: project, Role: role,
			ExpectedRevision: &revision, Instructions: &instructions}); err != nil {
			t.Fatalf("save the instructions of the role %s of %s: %v", role, project, err)
		}
	}
}

// Each effect of a broker tick arrives in one private frame, and the tick
// object has no size limit of its own. Six Projects start a Strategist run and
// a Scribe run in one tick. Their effects together pass the frame limit, and
// the tick object still reaches the output with each spawn.
func TestBrokerTickAboveTheFrameLimitStartsEachRun(t *testing.T) {
	const frameLimit = 1 << 20
	spine := initSpine(t)
	db := openDB(t, spine)
	projects := []string{"ws-a", "ws-b", "ws-c", "ws-d", "ws-e", "ws-f"}
	for _, project := range projects {
		brokerTickProject(t, spine, project)
	}

	stdout, stderr, err := brokerDispatch(t, spine, brokerHelperStub)
	if err != nil {
		t.Fatalf("dispatch exit err=%v, want a written tick; stdout has %d bytes, stderr=%q", err, len(stdout), stderr)
	}
	if len(stdout) <= frameLimit {
		t.Fatalf("the tick object has %d bytes, want more than the frame limit of %d bytes", len(stdout), frameLimit)
	}
	var tick map[string]any
	if err := json.Unmarshal([]byte(stdout), &tick); err != nil {
		t.Fatalf("dispatch stdout of %d bytes is not the final tick: %v", len(stdout), err)
	}
	spawns := map[string]int{}
	for _, action := range tickActions(t, tick) {
		body, err := json.Marshal(action)
		if err != nil {
			t.Fatal(err)
		}
		if action["action"] != "spawn" || len(body) > frameLimit {
			t.Fatalf("action of %d bytes = %.200s, want a spawn inside the frame limit", len(body), body)
		}
		spawns[fmt.Sprint(action["role"])]++
	}
	// ws-test has a current wiki, so only its pipeline round starts a run.
	if spawns["strategist(propose)"] != len(projects)+1 || spawns["scribe"] != len(projects) || len(spawns) != 2 {
		t.Fatalf("spawns = %v, want one Strategist run for each of %d Projects and one Scribe run for each of %d", spawns, len(projects)+1, len(projects))
	}
	if n := queryInt(t, db, `SELECT COUNT(*) FROM runs WHERE ended_at IS NULL`); n != 2*len(projects)+1 {
		t.Fatalf("%d active runs, want the %d runs of the tick", n, 2*len(projects)+1)
	}
}
