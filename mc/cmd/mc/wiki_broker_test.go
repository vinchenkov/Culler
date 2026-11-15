package main_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"mc/verbs"
)

// The pipeline round of ws-test is refused at the crossing. The wiki loop of
// the same broker tick still runs: it starts the wiki update of ws-zeta, whose
// wiki is behind.
func TestWikiRoundRunsAfterARefusedPipelineRound(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	workspace := gitFixtureWorkspace(t)
	toolHome, err := verbs.PrepareProjectHome(filepath.Dir(spine), "ws-zeta")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sandbox_profiles (id, workspace_root, artifact_roots, tool_home_dir)
		VALUES ('ws-zeta-profile', ?, '[]', ?)`, workspace, toolHome.Canonical); err != nil {
		t.Fatal(err)
	}
	if add := runMC(t, spineEnv(spine), "", "project", "add", "ws-zeta",
		"--title", "Zeta", "--sandbox-profile", "ws-zeta-profile"); add.code != 0 {
		t.Fatalf("project add failed: %s", add.stderr)
	}
	recordFixtureEnvironment(t, db, "ws-zeta")
	commit := fixtureGit(t, workspace, "rev-parse", "refs/heads/culler")

	// The stub refuses the prepare call of the pipeline round of ws-test. It
	// passes each other call to the helper.
	stub := "#!/bin/sh\n" +
		"test \"$1\" = exec || exit 91\n" +
		"shift\n" +
		"test \"$1\" = -i && shift\n" +
		"test \"$1/$2\" = \"mc-helper-1/mc\" || exit 91\n" +
		"shift 2\n" +
		"if [ \"$1\" = \"__dispatch-prepare\" ]; then\n" +
		"  frame=\"$MC_TEST_TMP/frame.$$\"\n" +
		"  cat > \"$frame\"\n" +
		"  if grep -a -q '\"project\":\"ws-test\"' \"$frame\" && ! grep -a -q '\"round\":\"wiki\"' \"$frame\"; then\n" +
		"    echo 'mc: private prepare refused: the stub refuses this round' >&2\n" +
		"    exit 1\n" +
		"  fi\n" +
		"  exec env MC_SPINE=\"$MC_TEST_SPINE\" MC_HOME= MC_HELPER= \"$MC_TEST_BIN\" \"$@\" < \"$frame\"\n" +
		"fi\n" +
		"exec env MC_SPINE=\"$MC_TEST_SPINE\" MC_HOME= MC_HELPER= \"$MC_TEST_BIN\" \"$@\"\n"
	stdout, stderr, err := brokerDispatch(t, spine, stub)
	if err != nil {
		t.Fatalf("dispatch exit err=%v, want a written tick; stdout=%q stderr=%q", err, stdout, stderr)
	}
	var tick map[string]any
	if err := json.Unmarshal([]byte(stdout), &tick); err != nil {
		t.Fatalf("dispatch stdout %q is not the final tick: %v", stdout, err)
	}
	actions := tickActions(t, tick)
	if len(actions) != 2 {
		t.Fatalf("tick actions = %v, want the refusal of the pipeline round and the start of the wiki update", actions)
	}
	refused, _ := actions[0]["error"].(map[string]any)
	if refused["code"] != "health.helper_unavailable" || !strings.Contains(stderr, "the stub refuses this round") {
		t.Fatalf("first action = %v (stderr %q), want the refusal envelope of the pipeline round of ws-test", actions[0], stderr)
	}
	spawn := actions[1]
	run, _ := spawn["run_id"].(string)
	if spawn["action"] != "spawn" || spawn["role"] != "scribe" || spawn["project"] != "ws-zeta" || spawn["subject_id"] != nil || run == "" {
		t.Fatalf("second action = %v, want the Scribe spawn of ws-zeta", spawn)
	}
	plan, _ := spawn["mount_plan"].(map[string]any)
	repository, _ := plan["repository"].(map[string]any)
	if repository["input_commit"] != commit || repository["wiki_update"] != run || repository["destination_branch"] != "culler" ||
		repository["workspace_root"] != workspace {
		t.Fatalf("repository plan = %v, want the wiki update %s at the commit %s of culler", repository, run, commit)
	}

	if got := queryStr(t, db, `SELECT run_id || '/' || start_commit FROM wiki_updates WHERE project='ws-zeta' AND ended_at IS NULL`); got != run+"/"+commit {
		t.Fatalf("active wiki update of ws-zeta = %q, want %s at %s", got, run, commit)
	}
	if got := queryStr(t, db, `SELECT accepted_commit FROM accepted_commits WHERE project='ws-zeta'`); got != commit {
		t.Fatalf("stored accepted commit of ws-zeta = %q, want %q", got, commit)
	}
	// The refused pipeline round ended the loop of the pipeline rounds: no
	// lease has a run, and the wiki of ws-test stays current.
	if n := queryInt(t, db, `SELECT COUNT(*) FROM leases WHERE run_id IS NOT NULL`); n != 0 {
		t.Fatalf("%d leases hold a run, want none", n)
	}
	if n := queryInt(t, db, `SELECT COUNT(*) FROM wiki_updates WHERE project='ws-test'`); n != 1 {
		t.Fatalf("ws-test has %d wiki updates, want the one of the fixture", n)
	}
}
