package verbs

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	"mc/dispatch"
	"mc/domain"
)

func saveCustom(t *testing.T, db *sql.DB, role dispatch.Role, revision int64, text string) RoleCustomization {
	t.Helper()
	result, err := ProjectRoleSave(db, nil, RoleSaveRequest{Project: "ws-test", Role: role, ExpectedRevision: &revision, Instructions: &text})
	if err != nil {
		t.Fatal(err)
	}
	return result.(RoleCustomization)
}
func claimCustom(t *testing.T, db *sql.DB, role dispatch.Role, subject *int64, run string) *RunIdentity {
	t.Helper()
	err := inTx(db, func(ctx context.Context, q Q) error {
		_, err := domain.Claim(ctx, q, time.Now(), domain.ClaimArgs{RunID: run, Project: "ws-test", Owner: baseRole(string(role)), SubjectID: subject, HardDeadlineMinutes: 60})
		if err != nil {
			return err
		}
		return recordRunCustomization(ctx, q, &dispatch.Spawn{Project: "ws-test", Role: role}, run)
	})
	if err != nil {
		t.Fatal(err)
	}
	return &RunIdentity{Tier: "pipeline", Role: string(role), RunID: run}
}
func TestCustomizationRevisionsAndRunIsolation(t *testing.T) {
	db := dvSpine(t)
	raw := "  Use the tests in api/.\nKeep this exact text.\n"
	c := saveCustom(t, db, dispatch.RoleStrategist, 0, raw)
	result, err := dvDispatchOne(db)
	if err != nil {
		t.Fatal(err)
	}
	effect := result.(map[string]any)
	run := effect["run_id"].(string)
	if !strings.Contains(effect["brief"].(string), "Project role customization") || !strings.Contains(effect["brief"].(string), "Standard Culler directive") {
		t.Fatal("The run input omits the input labels.")
	}
	if effect["role"] != "strategist(propose)" {
		t.Fatalf("The Strategist wire identifier changed: %v", effect["role"])
	}
	saveCustom(t, db, dispatch.RoleStrategist, c.Revision, "")
	got, err := RunCustomization(db, nil, run)
	if err != nil {
		t.Fatal(err)
	}
	original := got.(RoleCustomization)
	if original.Instructions != raw || original.Revision != 1 || original.StandardDigest == "" {
		t.Fatalf("The run lost its input: %+v", original)
	}
	fresh, err := customizationCurrent(context.Background(), db, "ws-test", dispatch.RoleStrategist)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Revision != 2 || fresh.Instructions != "" {
		t.Fatalf("The empty revision differs: %+v", fresh)
	}
	other, err := customizationCurrent(context.Background(), db, "ws-test", dispatch.RoleWorker)
	if err != nil || other.Revision != 0 {
		t.Fatalf("The revision crossed roles: %+v %v", other, err)
	}
	dvAddProject(t, db, "other", "paused")
	other, err = customizationCurrent(context.Background(), db, "other", dispatch.RoleStrategist)
	if err != nil || other.Revision != 0 {
		t.Fatalf("The revision crossed Projects: %+v %v", other, err)
	}
	if err := inTx(db, func(ctx context.Context, q Q) error {
		if err := endRun(ctx, q, run, "completed"); err != nil {
			return err
		}
		return releaseLease(ctx, q, run)
	}); err != nil {
		t.Fatal(err)
	}
	next, err := dvDispatchOne(db)
	if err != nil {
		t.Fatal(err)
	}
	nextRun := next.(map[string]any)["run_id"].(string)
	nextRecord, err := RunCustomization(db, nil, nextRun)
	if err != nil {
		t.Fatal(err)
	}
	if nextRecord.(RoleCustomization).Revision != 2 || nextRecord.(RoleCustomization).Instructions != "" {
		t.Fatal("The new run did not receive the empty revision.")
	}
	if _, err := db.Exec(`UPDATE role_customizations SET instructions='replace'`); err == nil {
		t.Fatal("The spine changed revision history.")
	}
	if _, err := db.Exec(`UPDATE run_customizations SET revision=2`); err == nil {
		t.Fatal("The spine changed run input.")
	}
}
func TestCustomizationPermissionsAndStaleSave(t *testing.T) {
	db := dvSpine(t)
	saveCustom(t, db, dispatch.RoleWorker, 0, "First")
	text := "Second"
	zero := int64(0)
	one := int64(1)
	a := RoleSaveRequest{Project: "ws-test", Role: dispatch.RoleWorker, ExpectedRevision: &zero, Instructions: &text}
	if _, err := ProjectRoleSave(db, nil, a); err == nil {
		t.Fatal("A stale save succeeded.")
	}
	a.ExpectedRevision = &one
	for _, id := range []*RunIdentity{{Tier: "pipeline", Role: "worker"}, {Tier: "collie"}} {
		if _, err := ProjectRoleSave(db, id, a); err == nil {
			t.Fatal("An unauthorized save succeeded.")
		}
	}
	collie := &RunIdentity{Tier: "collie", RunID: "collie-test", VerbAllowlist: MaximumCollieAgentVerbs()}
	r, err := ProjectRoleSave(db, collie, a)
	if err != nil {
		t.Fatal(err)
	}
	if r.(RoleCustomization).Author != "collie:collie-test" {
		t.Fatal("The revision lost its author.")
	}
	shown, err := ProjectRoleShow(db, collie, "ws-test", dispatch.RoleWorker)
	if err != nil {
		t.Fatal(err)
	}
	if len(shown.(map[string]any)["history"].([]RoleCustomization)) != 2 {
		t.Fatal("The history differs from the saves.")
	}
	roles := shown.(map[string]any)["roles"].([]map[string]string)
	if roles[0]["role"] != "strategist(propose)" || roles[0]["title"] != "Strategist" {
		t.Fatalf("The Strategist customization label differs: %v", roles[0])
	}
}
func TestCustomizationConflictBlocksSubjectOrRole(t *testing.T) {
	for _, task := range []bool{false, true} {
		t.Run(map[bool]string{false: "role", true: "task"}[task], func(t *testing.T) {
			db := dvSpine(t)
			role := dispatch.RoleStrategist
			var subject *int64
			if task {
				role = dispatch.RoleWorker
				n := int64(1)
				subject = &n
				dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
				dvExec(t, db, `UPDATE tasks SET correction_count=1,dispatch_retries=2 WHERE id=1`)
			}
			saveCustom(t, db, role, 0, "Report false evidence.")
			id := claimCustom(t, db, role, subject, "conflict-run")
			dvExec(t, db, `UPDATE projects SET status='paused' WHERE id='ws-test'`)
			a := CustomizationConflictRequest{Run: id.RunID, Revision: 1, Passage: "Report false evidence.", Requirement: "Report evidence truthfully.", Reason: "False evidence cannot be truthful."}
			wrong := *id
			wrong.RunID = "other"
			if _, err := RunCustomizationConflict(db, &wrong, a); err == nil {
				t.Fatal("Another run reported this conflict.")
			}
			bad := a
			bad.Passage = "Invented passage"
			if _, err := RunCustomizationConflict(db, id, bad); err == nil {
				t.Fatal("The terminal accepted an absent passage.")
			}
			if dfInt(t, db, `SELECT COUNT(*) FROM customization_conflicts`) != 0 {
				t.Fatal("The refused terminal wrote a report.")
			}
			if _, err := RunCustomizationConflict(db, id, a); err != nil {
				t.Fatal(err)
			}
			if dfStr(t, db, `SELECT outcome FROM runs WHERE id='conflict-run'`) != "blocked" {
				t.Fatal("The terminal did not end the run.")
			}
			if dfStr(t, db, `SELECT status FROM projects WHERE id='ws-test'`) != "paused" {
				t.Fatal("The block changed the Project pause.")
			}
			if dfInt(t, db, `SELECT COUNT(*) FROM infrastructure_failures`) != 0 {
				t.Fatal("The block recorded an infrastructure failure.")
			}
			saveCustom(t, db, role, 1, "")
			reports, err := customizationConflicts(context.Background(), db, "ws-test", role)
			if err != nil {
				t.Fatal(err)
			}
			if len(reports) != 1 || !reports[0].Blocked || reports[0].Revision != 1 || reports[0].Passage != a.Passage {
				t.Fatalf("The save changed the report: %+v", reports)
			}
			if task {
				if dfInt(t, db, `SELECT blocked=1 AND correction_count=1 AND dispatch_retries=2 AND status='seeded' FROM tasks WHERE id=1`) != 1 {
					t.Fatal("The block changed the task contract state.")
				}
				if kind := dfStr(t, db, `SELECT blocked_kind FROM tasks WHERE id=1`); kind != domain.BlockCustomization {
					t.Fatalf("The conflict set the condition type %q.", kind)
				}
				if _, err := ProjectRoleUnblock(db, nil, RoleUnblockRequest{Run: id.RunID, Reason: "Resolved"}); err == nil {
					t.Fatal("The role control cleared a subject block.")
				}
				if _, err := TaskUnblock(db, nil, *subject, "The instruction is removed."); err != nil {
					t.Fatal(err)
				}
				dvExec(t, db, `UPDATE tasks SET blocked=1,blocked_kind='operator',blocked_reason='A separate decision.' WHERE id=1`)
				reports, err = customizationConflicts(context.Background(), db, "ws-test", role)
				if err != nil || reports[0].Blocked || reports[0].Resolution != "The instruction is removed." {
					t.Fatalf("A later block changed the resolved report: %+v %v", reports, err)
				}

			} else {
				rec, err := loadRecords(context.Background(), db, "ws-test")
				if err != nil {
					t.Fatal(err)
				}
				if !rec.BlockedRoles[role] {
					t.Fatal("Dispatch lost the role block.")
				}
				rec.ProjectStatus = "active"
				rec.VisionReady = true
				clk := dispatch.Clock{Now: time.Now()}
				action := dispatch.Decide(rec, dispatch.Lease{}, dispatch.DefaultConfig(), clk)
				if action.Idle != "role-blocked" {
					t.Fatalf("The role block permits another proposal: %+v", action)
				}
				rec.Tasks = []dispatch.Task{{ID: 9, Status: dispatch.StatusSeeded, Scope: dispatch.ScopeTask, Project: "ws-test", DispatchRetries: 3}}
				action = dispatch.Decide(rec, dispatch.Lease{}, dispatch.DefaultConfig(), clk)
				if action.Spawn == nil || action.Spawn.Role != dispatch.RoleWorker {
					t.Fatalf("The role block withheld other work: %+v", action)
				}
				if _, err := ProjectRoleUnblock(db, nil, RoleUnblockRequest{Run: id.RunID, Reason: "The operator removed the instruction."}); err != nil {
					t.Fatal(err)
				}
				rec, err = loadRecords(context.Background(), db, "ws-test")
				if err != nil || rec.BlockedRoles[role] {
					t.Fatal("The role block remains after resolution.")
				}
			}
		})
	}
}

// The conflict of a Scribe run fails only its wiki update. The role keeps its
// next start, and no operator action clears a block.
func TestScribeCustomizationConflictSetsNoRoleBlock(t *testing.T) {
	db := dvSpine(t)
	saveCustom(t, db, dispatch.RoleScribe, 0, "Report false evidence.")
	run := wrWantSpawn(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test")
	id := wcScribe(run)
	a := CustomizationConflictRequest{Run: run, Revision: 1, Passage: "Report false evidence.",
		Requirement: "Report evidence truthfully.", Reason: "False evidence cannot be truthful."}
	bad := a
	bad.Passage = "Invented passage"
	if _, err := RunCustomizationConflict(db, id, bad); err == nil {
		t.Fatal("The terminal accepted an absent passage.")
	}
	if got := wrDump(t, db, `SELECT r.outcome, w.result FROM wiki_updates w JOIN runs r ON r.id=w.run_id WHERE w.run_id=?`, run); got != "outcome=<nil>|result=<nil>|\n" {
		t.Fatalf("the refused terminal changed the run or its wiki update: %s", got)
	}

	result, err := RunCustomizationConflict(db, id, a)
	if err != nil {
		t.Fatalf("customization conflict of the Scribe: %v", err)
	}
	if want := map[string]any{"run_id": run, "blocked": false}; !reflect.DeepEqual(result, want) {
		t.Fatalf("result = %v, want %v", result, want)
	}
	const cause = "The custom instructions of the Scribe conflict with the Culler contract."
	if got, want := wrRunEnd(t, db, run), wrEndLine("completed", "", cause, true, false); got != want {
		t.Fatalf("the run and its wiki update = %swant %s", got, want)
	}
	if got := wrDump(t, db, `SELECT run_id, passage, cleared_at IS NOT NULL AS cleared, cleared_by, resolution FROM customization_conflicts`); got !=
		"run_id="+run+"|passage=Report false evidence.|cleared=1|cleared_by=mc|resolution=The wiki update ended as failed.|\n" {
		t.Fatalf("conflict records = %s, want one cleared record of the run", got)
	}
	if got := wrDump(t, db, `SELECT actor, kind, subject, project FROM activity WHERE kind IN ('customization.conflict','wiki.update_ended') ORDER BY id`); got !=
		"actor=scribe|kind=customization.conflict|subject="+run+"|project=ws-test|\nactor=mc|kind=wiki.update_ended|subject="+run+"|project=ws-test|\n" {
		t.Fatalf("records of the conflict = %s, want the conflict record and the end record", got)
	}
	if got := wrText(t, db, `SELECT detail FROM activity WHERE kind='wiki.update_ended' AND subject=?`, run); got != "The wiki update failed. "+cause {
		t.Fatalf("end record = %q", got)
	}

	// No read shows a block of the role, and no task has a block.
	reports, err := customizationConflicts(context.Background(), db, "ws-test", dispatch.RoleScribe)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].Blocked || reports[0].Run != run || reports[0].Revision != 1 || reports[0].Resolution != "The wiki update ended as failed." {
		t.Fatalf("conflict reports of the role = %+v, want one report with no block", reports)
	}
	rec, err := loadRecords(context.Background(), db, "ws-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.BlockedRoles) != 0 {
		t.Fatalf("blocked roles = %v, want none", rec.BlockedRoles)
	}
	if n := dfInt(t, db, `SELECT (SELECT COUNT(*) FROM tasks WHERE blocked=1) + (SELECT COUNT(*) FROM infrastructure_failures)`); n != 0 {
		t.Fatalf("the conflict wrote %d task blocks and infrastructure failure records, want none", n)
	}
	if _, err := ProjectRoleUnblock(db, nil, RoleUnblockRequest{Run: run, Reason: "The operator removed the instruction."}); err == nil {
		t.Fatal("The role control cleared a block that does not exist.")
	}
	if _, err := RunCustomizationConflict(db, id, a); !rnStale(err) {
		t.Fatalf("second conflict terminal = %v, want a stale run refusal", err)
	}

	// The same instructions are current. The next wiki update still starts after the interval.
	wrWantReap(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test", run, "reported-outcome")
	wrStopped(t, db, run)
	wrWantIdle(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test", "wiki-interval")
	if next := wrStart(t, db, "ws-test"); next == run {
		t.Fatal("the round returned the run of the failed wiki update")
	}
}

func TestCustomizationConflictBlocksSubjectlessArbiter(t *testing.T) {
	db, id := proposalReview(t)
	saveCustom(t, db, dispatch.RoleArbiter, 0, "Report false evidence.")
	if err := inTx(db, func(ctx context.Context, q Q) error {
		return recordRunCustomization(ctx, q, &dispatch.Spawn{Project: "ws-test", Role: dispatch.RoleArbiter}, id.RunID)
	}); err != nil {
		t.Fatal(err)
	}
	dvExec(t, db, `UPDATE projects SET infrastructure_failure_count=1 WHERE id='ws-test'`)
	_, err := RunCustomizationConflict(db, id, CustomizationConflictRequest{
		Run: id.RunID, Revision: 1, Passage: "Report false evidence.",
		Requirement: "Report evidence truthfully.", Reason: "False evidence cannot be truthful.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if dfInt(t, db, `SELECT infrastructure_failure_count FROM projects WHERE id='ws-test'`) != 1 {
		t.Fatal("The conflict changed the Project failure allowance.")
	}
	if dfInt(t, db, `SELECT COUNT(*) FROM tasks WHERE blocked=1 OR status<>'proposed'`) != 0 {
		t.Fatal("The role block changed the proposal pool.")
	}
	saveCustom(t, db, dispatch.RoleArbiter, 1, "")
	rec, err := loadRecords(context.Background(), db, "ws-test")
	if err != nil {
		t.Fatal(err)
	}
	rec.VisionReady = true
	clk := dispatch.Clock{Now: time.Now()}
	action := dispatch.Decide(rec, dispatch.Lease{}, dispatch.DefaultConfig(), clk)
	if !rec.BlockedRoles[dispatch.RoleArbiter] || action.Spawn == nil || action.Spawn.Role != dispatch.RoleStrategist {
		t.Fatalf("The Arbiter block did not preserve the other role's eligibility: %+v", action)
	}
	rec.Tasks = append(rec.Tasks, dvTask(9, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	action = dispatch.Decide(rec, dispatch.Lease{}, dispatch.DefaultConfig(), clk)
	if action.Spawn == nil || action.Spawn.Role != dispatch.RoleWorker {
		t.Fatalf("The Arbiter block withheld assigned task work: %+v", action)
	}
	if _, err := ProjectRoleUnblock(db, nil, RoleUnblockRequest{Run: id.RunID, Reason: "The operator removed the instruction."}); err != nil {
		t.Fatal(err)
	}
	rec, err = loadRecords(context.Background(), db, "ws-test")
	if err != nil {
		t.Fatal(err)
	}
	rec.VisionReady = true
	action = dispatch.Decide(rec, dispatch.Lease{}, dispatch.DefaultConfig(), clk)
	if action.Spawn == nil || action.Spawn.Role != dispatch.RoleArbiter || action.Spawn.SubjectID != nil || len(action.Spawn.ProposedPool) != 4 {
		t.Fatalf("The operator resolution did not restore the shared review: %+v", action)
	}
}
