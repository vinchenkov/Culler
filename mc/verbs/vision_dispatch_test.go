package verbs

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"mc/dispatch"
)

func TestVisionRemovalBetweenPrepareAndCommitRefusesRun(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	prepared := dfPrepare(t, db, dfRequestID)
	if prepared.candidate == nil {
		t.Fatal("The fixture selected no candidate.")
	}
	attested, err := dispatchAttest(os.Getenv("MC_HOME"), prepared)
	if err != nil {
		t.Fatal(err)
	}
	root := dfStr(t, db, `SELECT workspace_root FROM sandbox_profiles WHERE id='default'`)
	if err := os.Remove(ProjectVisionPath(root)); err != nil {
		t.Fatal(err)
	}
	dfAssertInert(t, db, dfCommit(t, db, prepared, attested))
}

func TestPrivateVisionRequiresHostObservation(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	req := privateRequest(dfUUID(t, db))
	prepared, err := DispatchPreparePrivateDB(db, req)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Final == nil || !strings.Contains(string(*prepared.Final), "vision-required") {
		t.Fatalf("Missing host check allowed work: %+v", prepared)
	}
	root := dfStr(t, db, `SELECT workspace_root FROM sandbox_profiles WHERE id='default'`)
	check := CheckProjectVision(root)
	req.VisionCheck = &check
	prepared, err = DispatchPreparePrivateDB(db, req)
	if err != nil || prepared.Candidate == nil {
		t.Fatalf("Host check refused: %+v %v", prepared, err)
	}
	commit, err := DispatchAttestPrivate(os.Getenv("MC_HOME"), prepared)
	if err != nil {
		t.Fatal(err)
	}
	// A changed file invalidates the content that the preparation token covers.
	if err := os.WriteFile(ProjectVisionPath(root), []byte("Help people build useful software."), 0o644); err != nil {
		t.Fatal(err)
	}
	changed := CheckProjectVision(root)
	commit.VisionCheck = &changed
	result, err := DispatchCommitPrivateDB(db, commit)
	if err != nil {
		t.Fatal(err)
	}
	var effect map[string]any
	if err := json.Unmarshal(result.Result, &effect); err != nil {
		t.Fatal(err)
	}
	dfAssertInert(t, db, effect)
}

func TestDispatchBriefContainsCurrentUntrackedVision(t *testing.T) {
	db := dvSpine(t)
	root := dfStr(t, db, `SELECT workspace_root FROM sandbox_profiles WHERE id='default'`)
	body := "Help small teams build reliable software."
	if err := os.WriteFile(ProjectVisionPath(root), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := gitAtSource(root, "ls-files", ProjectVisionFile); err != nil || strings.TrimSpace(string(output)) != "" {
		t.Fatalf("The fixture vision must be untracked: %q %v", output, err)
	}
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	prepared := dfPrepare(t, db, dfRequestID)
	attested, err := dispatchAttest(os.Getenv("MC_HOME"), prepared)
	if err != nil {
		t.Fatal(err)
	}
	effect := dfCommit(t, db, prepared, attested)
	if effect["action"] != "spawn" {
		t.Fatalf("Dispatch result: %+v", effect)
	}
	text := effect["brief"].(string)
	vision := parseRenderedBrief(t, text).part(partVision)
	if vision == nil || vision.Text != body {
		t.Fatalf("The brief lost the current vision: %+v", vision)
	}
	if strings.Contains(text, root) {
		t.Fatalf("The brief holds a host location: %s", text)
	}
}

func TestVisionRefreshKeepsProjectsIndependent(t *testing.T) {
	db := dvSpine(t)
	dvAddProject(t, db, "ws-other", "active")
	root := dfStr(t, db, `SELECT workspace_root FROM sandbox_profiles WHERE id='default'`)
	if err := os.Remove(ProjectVisionPath(root)); err != nil {
		t.Fatal(err)
	}
	check := func(project string, want dispatch.IdleReason) {
		t.Helper()
		if err := inTx(db, func(ctx context.Context, q Q) error {
			sel, err := selectFromSpine(ctx, q, nil, project)
			if err != nil {
				return err
			}
			if want == "" && sel.action.Kind == dispatch.KindIdle {
				t.Fatalf("Ready Project blocked: %+v", sel.action)
			}
			if want != "" && sel.action.Idle != want {
				t.Fatalf("Missing vision allowed: %+v", sel.action)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	check("ws-test", dispatch.IdleVisionRequired)
	check("ws-other", "")
	writeVisionFixture(t, root)
	check("ws-test", "")
}
