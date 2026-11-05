package verbs

import (
	"context"
	"os"
	"testing"
	"time"

	"mc/dispatch"
)

func TestPauseAllAndResumeAllChangeEveryProject(t *testing.T) {
	db := dvSpine(t)
	dvAddProject(t, db, "ws-other", "paused")
	dvAddProject(t, db, "ws-archived", "archived")
	dvAddProject(t, db, "ws-setup", "paused")
	dvExec(t, db, `UPDATE projects SET setup_status='vision' WHERE id='ws-setup'`)
	for _, step := range []struct {
		paused bool
		want   map[string]string
	}{
		{true, map[string]string{"ws-test": "paused", "ws-other": "paused", "ws-archived": "archived", "ws-setup": "paused"}},
		{true, map[string]string{"ws-test": "paused", "ws-other": "paused", "ws-archived": "archived", "ws-setup": "paused"}},
		{false, map[string]string{"ws-test": "active", "ws-other": "active", "ws-archived": "archived", "ws-setup": "paused"}},
		{false, map[string]string{"ws-test": "active", "ws-other": "active", "ws-archived": "archived", "ws-setup": "paused"}},
	} {
		raw, err := SetEveryProjectPause(db, nil, step.paused)
		if err != nil {
			t.Fatal(err)
		}
		status := raw.(PipelinePauseStatus)
		if len(status.Projects) != len(step.want) {
			t.Fatalf("pause = %+v", status)
		}
		for _, ws := range status.Projects {
			if ws.Status != step.want[ws.ID] {
				t.Fatalf("Project = %+v, want %s", ws, step.want[ws.ID])
			}
		}
	}
	if _, err := ProjectSetStatus(db, nil, "ws-archived", "active"); err == nil {
		t.Fatal("An archived Project resumed.")
	}
}

func TestProjectResumeAfterPauseAllResumesOnlyThatProject(t *testing.T) {
	db := dvSpine(t)
	dvAddProject(t, db, "ws-other", "active")
	if _, err := SetEveryProjectPause(db, nil, true); err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectSetStatus(db, nil, "ws-test", "active"); err != nil {
		t.Fatal(err)
	}
	raw, err := PauseStatus(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, ws := range raw.(PipelinePauseStatus).Projects {
		want := map[string]string{"ws-test": "active", "ws-other": "paused"}[ws.ID]
		if ws.Status != want {
			t.Fatalf("Project = %+v, want %s", ws, want)
		}
	}
	if _, err := ProjectSetStatus(db, nil, "missing", "active"); err == nil {
		t.Fatal("An unknown Project resumed.")
	}
}

func TestPipelineAgentsCannotChangePauses(t *testing.T) {
	db := dvSpine(t)
	id := &RunIdentity{Tier: "pipeline"}
	for _, paused := range []bool{true, false} {
		if _, err := SetEveryProjectPause(db, id, paused); err == nil {
			t.Fatal("A pipeline agent changed every Project pause.")
		}
	}
	for _, target := range []string{"paused", "active"} {
		if _, err := ProjectSetStatus(db, id, "ws-test", target); err == nil {
			t.Fatal("A pipeline agent changed a Project pause.")
		}
	}
}

func TestPipelinePauseAllowsHomieDispatch(t *testing.T) {
	for _, scope := range []string{"all", "project"} {
		t.Run(scope, func(t *testing.T) {
			db := dvSpine(t)
			dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
			hwSession(t, db, "h-1")
			hwPendingInbound(t, db, "h-1", 1)
			var err error
			if scope == "all" {
				_, err = SetEveryProjectPause(db, nil, true)
			} else {
				_, err = ProjectSetStatus(db, nil, "ws-test", "paused")
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := Dispatch(db)
			if err != nil {
				t.Fatal(err)
			}
			actions := dvTickActions(t, raw)
			if len(actions) != 1 || actions[0]["action"] != "homie-wake" {
				t.Fatalf("actions = %v", actions)
			}
			if n := dfInt(t, db, `SELECT count(*) FROM runs WHERE tier='pipeline'`); n != 0 {
				t.Fatalf("The pause permitted %d pipeline runs.", n)
			}
		})
	}
}

func TestPauseBetweenPrepareAndCommitRefusesRun(t *testing.T) {
	for _, scope := range []string{"all", "project"} {
		t.Run(scope, func(t *testing.T) {
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
			if scope == "all" {
				_, err = SetEveryProjectPause(db, nil, true)
			} else {
				_, err = ProjectSetStatus(db, nil, "ws-test", "paused")
			}
			if err != nil {
				t.Fatal(err)
			}
			dfAssertInert(t, db, dfCommit(t, db, prepared, attested))
		})
	}
}

func TestPausedPipelineReleasesExpiredLease(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject) VALUES ('dead-run', 'pipeline', 'worker', 'ws-test', 1)`)
	dvHold(t, db, "ws-test", "dead-run", "worker", ptr64(1), 2*time.Hour, nil, time.Hour)
	if _, err := SetEveryProjectPause(db, nil, true); err != nil {
		t.Fatal(err)
	}
	prepared := dfPrepare(t, db, dfRequestID)
	if prepared.final["action"] != "reap" {
		t.Fatalf("effect = %v", prepared.final)
	}
	if _, err := RunStopped(db, nil, "dead-run"); err != nil {
		t.Fatal(err)
	}
	if n := dfInt(t, db, `SELECT count(*) FROM leases WHERE run_id IS NOT NULL`); n != 0 {
		t.Fatal("The pause prevented lease release.")
	}
}

func TestProjectPauseLeavesOtherPipelineAvailable(t *testing.T) {
	db := dvSpine(t)
	dvAddProject(t, db, "ws-other", "active")
	if _, err := ProjectSetStatus(db, nil, "ws-test", "paused"); err != nil {
		t.Fatal(err)
	}
	if err := inTx(db, func(ctx context.Context, q Q) error {
		for _, ws := range []string{"ws-test", "ws-other"} {
			sel, err := selectFromSpine(ctx, q, nil, ws)
			if err != nil {
				return err
			}
			if ws == "ws-test" && sel.action.Kind != dispatch.KindIdle {
				t.Fatalf("Paused Project selected %+v", sel.action)
			}
			if ws == "ws-other" && sel.action.Kind == dispatch.KindIdle {
				t.Fatalf("Active Project selected %+v", sel.action)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestResumeAllRollsBackEveryProject(t *testing.T) {
	db := dvSpine(t)
	dvAddProject(t, db, "ws-other", "active")
	if _, err := SetEveryProjectPause(db, nil, true); err != nil {
		t.Fatal(err)
	}
	// Reject one Project to prove that the other Project also rolls back.
	dvExec(t, db, `CREATE TRIGGER refuse_resume BEFORE UPDATE OF status ON projects WHEN NEW.id = 'ws-other' BEGIN SELECT RAISE(ABORT, 'test refusal'); END`)
	if _, err := SetEveryProjectPause(db, nil, false); err == nil {
		t.Fatal("The forced refusal succeeded.")
	}
	if got := dfStr(t, db, `SELECT group_concat(status) FROM projects`); got != "paused,paused" {
		t.Fatalf("The resume changed one Project: %s", got)
	}
}
