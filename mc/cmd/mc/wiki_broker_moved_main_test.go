package main_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mc/wikitest"
)

// The branch culler of ws-test moves after the first read of the broker tick and
// before the commit step of the wiki round. The broker reads culler again for
// the commit frame, so the round starts no wiki update at the old commit and
// stores the new commit. The next tick starts the wiki update at the new commit.
func TestBrokerWikiRoundReadsMainAgainForTheCommitStep(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	workspace := queryStr(t, db, `SELECT p.workspace_root FROM projects w JOIN sandbox_profiles p ON p.id = w.sandbox_profile WHERE w.id = 'ws-test'`)

	// The last wiki update started before the wiki update interval, so the new
	// commit on culler makes a wiki update due.
	start := fixtureGit(t, workspace, "rev-parse", "refs/heads/culler")
	if _, recorded, err := wikitest.RecordCurrentWiki(context.Background(), db, "ws-test", start, time.Now().Add(-2*time.Hour)); err != nil || !recorded {
		t.Fatalf("record the earlier wiki update: recorded=%v err=%v", recorded, err)
	}
	fixtureGit(t, workspace, "commit", "-q", "--allow-empty", "-m", "Change the source")
	first := fixtureGit(t, workspace, "rev-parse", "refs/heads/culler")
	updates := queryInt(t, db, `SELECT COUNT(*) FROM wiki_updates WHERE project='ws-test'`)

	// The stub makes one commit on culler when it receives the prepare call of
	// the wiki round. The broker read culler for that frame before the call. The
	// stub records the accepted commit of the frame and passes each call to
	// the helper.
	frames := filepath.Join(t.TempDir(), "frames")
	stub := "#!/bin/sh\n" +
		"test \"$1\" = exec || exit 91\n" +
		"shift\n" +
		"test \"$1\" = -i && shift\n" +
		"test \"$1/$2\" = \"mc-helper-1/mc\" || exit 91\n" +
		"shift 2\n" +
		"if [ \"$1\" = \"__dispatch-prepare\" ]; then\n" +
		"  frame=\"$MC_TEST_TMP/frame.$$\"\n" +
		"  cat > \"$frame\"\n" +
		"  if grep -a -q '\"project\":\"ws-test\"' \"$frame\" && grep -a -q '\"round\":\"wiki\"' \"$frame\"; then\n" +
		"    git -C '" + workspace + "' -c user.name=Fixture -c user.email=fixture@example.test -c commit.gpgsign=false" +
		" commit -q --allow-empty -m 'Move culler' || exit 92\n" +
		"    grep -a -o '\"accepted_commit\":\"[0-9a-f]*\"' \"$frame\" >> '" + frames + "'\n" +
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

	// The prepare frame held the first commit, and culler moved one time.
	recorded, err := os.ReadFile(frames)
	if err != nil {
		t.Fatalf("the stub got no prepare call of the wiki round: %v (stderr %q)", err, stderr)
	}
	if got, want := string(recorded), "\"accepted_commit\":\""+first+"\"\n"; got != want {
		t.Fatalf("prepare frames of the wiki round = %q, want one frame with the first commit %s", got, first)
	}
	second := fixtureGit(t, workspace, "rev-parse", "refs/heads/culler")
	if second == first {
		t.Fatalf("culler stayed at %s; the stub made no commit", first)
	}

	for _, action := range tickActions(t, tick) {
		if action["error"] != nil {
			t.Fatalf("tick action = %v (stderr %q), want no refused round", action, stderr)
		}
		if action["role"] == "scribe" {
			plan, _ := action["mount_plan"].(map[string]any)
			repository, _ := plan["repository"].(map[string]any)
			t.Fatalf("the tick holds the Scribe %v of the run %v at the commit %v, want no Scribe action: culler moved from %s to %s before the commit step",
				action["action"], action["run_id"], repository["input_commit"], first, second)
		}
	}
	if n := queryInt(t, db, `SELECT COUNT(*) FROM wiki_updates WHERE project='ws-test'`); n != updates {
		t.Fatalf("ws-test has %d wiki updates, want the %d of the fixture: the commit of the commit frame differs from the start commit", n, updates)
	}
	// Only the commit step can store the second commit: the prepare frame held the first.
	if got := queryStr(t, db, `SELECT accepted_commit FROM accepted_commits WHERE project='ws-test'`); got != second {
		t.Fatalf("stored accepted commit = %q, want the second commit %q from the read before the commit step", got, second)
	}

	// The next tick has a stable culler. It starts the wiki update at the second commit.
	stdout, stderr, err = brokerDispatch(t, spine, brokerHelperStub)
	if err != nil {
		t.Fatalf("second dispatch exit err=%v, want a written tick; stdout=%q stderr=%q", err, stdout, stderr)
	}
	tick = nil
	if err := json.Unmarshal([]byte(stdout), &tick); err != nil {
		t.Fatalf("second dispatch stdout %q is not the final tick: %v", stdout, err)
	}
	var spawns []map[string]any
	for _, action := range tickActions(t, tick) {
		if action["role"] == "scribe" {
			spawns = append(spawns, action)
		}
	}
	if len(spawns) != 1 || spawns[0]["action"] != "spawn" || spawns[0]["project"] != "ws-test" {
		t.Fatalf("Scribe actions of the second tick = %v (stderr %q), want the one Scribe spawn of ws-test", spawns, stderr)
	}
	run, _ := spawns[0]["run_id"].(string)
	plan, _ := spawns[0]["mount_plan"].(map[string]any)
	repository, _ := plan["repository"].(map[string]any)
	if run == "" || repository["input_commit"] != second || repository["wiki_update"] != run {
		t.Fatalf("repository plan = %v, want the wiki update %s at the second commit %s", repository, run, second)
	}
	if got := queryStr(t, db, `SELECT run_id || '/' || start_commit FROM wiki_updates WHERE project='ws-test' AND ended_at IS NULL`); got != run+"/"+second {
		t.Fatalf("active wiki update = %q, want %s at %s", got, run, second)
	}
}
