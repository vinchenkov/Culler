package verbs

import (
	"context"
	"testing"

	"mc/dispatch"
)

func TestRequirementsRevisionKeepsPriorVerdictsOutsideCurrentBrief(t *testing.T) {
	db := dvSpine(t)
	task := dvTask(1, dispatch.ScopeTask, dispatch.StatusPackaged, 2)
	dvInsertTask(t, db, task)
	dvExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,verdict_outcome,evidence_path,correction_path,output_path)
 VALUES('prior-verifier','pipeline','verifier','ws-test',1,'correct','prior-evidence.md','prior-correction.md','prior-report.md')`)
	dvExec(t, db, `UPDATE tasks SET thesis='The prior packet describes its original requirements.' WHERE id=1`)
	dvPublishPacket(t, db, 1, "prior-packet.html", dvOld)
	boardRevision(t, db, 1, "The revised task requires new evidence.")
	verdict, err := loadLatestVerdict(context.Background(), db, 1)
	if err != nil || verdict != nil {
		t.Fatalf("the old verdict became current: %+v, %v", verdict, err)
	}
	if held := dfInt(t, db, `SELECT review_slot_held FROM tasks WHERE id=1`); held != 0 {
		t.Fatalf("the old packet retained its review slot: %d", held)
	}
	brief, err := buildRunBrief(context.Background(), db, &dispatch.Spawn{Project: "ws-test", Role: dispatch.RoleWorker, SubjectID: &task.ID}, "next-worker", briefInputs{})
	if err != nil {
		t.Fatal(err)
	}
	if brief.part(partCorrection) != nil {
		t.Fatal("the next Worker received a superseded correction as current")
	}
	if got := dfInt(t, db, `SELECT requirements_version FROM run_requirements WHERE run_id='prior-verifier' AND task_id=1`); got != 1 {
		t.Fatalf("the old verdict lost its original requirements version: %d", got)
	}
	dvExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,verdict_outcome,evidence_path)
 VALUES('current-verifier','pipeline','verifier','ws-test',1,'pass','current-evidence.md')`)
	verdict, err = loadLatestVerdict(context.Background(), db, 1)
	if err != nil || verdict == nil || verdict.RequirementsVersion != 2 || verdict.EvidencePath != "current-evidence.md" {
		t.Fatalf("the current verdict identity is incorrect: %+v, %v", verdict, err)
	}
}
