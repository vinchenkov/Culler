package substrate_test

import (
	"reflect"
	"strings"
	"testing"
)

func TestTaskScopeAndDestination(t *testing.T) {
	db := openSpine(t)
	for _, scope := range []string{"other", "", "TASK"} {
		wantAbort(t, db, `INSERT INTO tasks(title,project,scope) VALUES('Invalid scope','ws',?)`, scope)
	}
	task := mkTask(t, db, "task", "seeded")
	if got := taskStr(t, db, task, "destination_branch"); got != "main" {
		t.Fatalf("The task destination is %q, want main.", got)
	}
	if _, err := db.Exec(`UPDATE tasks SET destination_branch='other' WHERE id=?`, task); err == nil {
		t.Fatal("The task accepted another destination branch.")
	}
	if got := oneInt(t, db, `SELECT count(*) FROM pragma_foreign_key_list('tasks') WHERE "table"='tasks'`); got != 0 {
		t.Fatalf("The task model contains %d relations between tasks.", got)
	}
}

// A Strategist proposal holds its Strategic rationale from creation. Operator
// work holds none.
func TestStrategicRationaleBelongsToAStrategistProposal(t *testing.T) {
	db := openSpine(t)
	for _, rationale := range []string{"", "   ", " \n\t\r "} {
		wantAbort(t, db, `INSERT INTO tasks(title,project,strategic_rationale) VALUES('Blank rationale','ws',?)`, rationale)
	}
	wantAbort(t, db, `INSERT INTO tasks(title,project,origin,strategic_rationale) VALUES('Operator work','ws','user','The operator needs no rationale.')`)
	result := mustExec(t, db, `INSERT INTO tasks(title,project,description,strategic_rationale) VALUES('Proposal','ws','The criteria.','Observed: the export omits the date.')`)
	task, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if got := taskStr(t, db, task, "strategic_rationale"); got != "Observed: the export omits the date." {
		t.Fatalf("The stored rationale is %q.", got)
	}
	if got := taskStr(t, db, task, "description"); got != "The criteria." {
		t.Fatalf("The rationale changed the description: %q.", got)
	}
	for _, change := range []string{
		`UPDATE tasks SET strategic_rationale='Another rationale.' WHERE id=?`,
		`UPDATE tasks SET strategic_rationale=NULL WHERE id=?`,
	} {
		wantAbort(t, db, change, task)
	}
	mustExec(t, db, `UPDATE tasks SET description='The revised criteria.' WHERE id=?`, task)
	operator := mustExec(t, db, `INSERT INTO tasks(title,project,origin) VALUES('Operator work','ws','user')`)
	id, err := operator.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	wantAbort(t, db, `UPDATE tasks SET strategic_rationale='A later rationale.' WHERE id=?`, id)
}

func TestTaskModelColumns(t *testing.T) {
	db := openSpine(t)
	want := strings.Fields(`id title description strategic_rationale record_version requirements_version scope priority created_at status
		stage_entered_at correction_count verifier_correction_open allowance_condition allowance_reason
		allowance_version correction_operator_reason returned_kind blocked blocked_reason blocked_kind dispatch_retries run_retry_at
		decision decided_at archived origin project completion_sha revision_notes destination_branch base_commit input_commit
		submitted_version source_version checkpoint_version candidate_id report_path report_sha256 thesis correction_open
		correction_reason merge_after observed_destination observed_at current_review_id review_slot_held stage_rank stalled
		infrastructure_failure_count`)
	rows, err := db.Query(`SELECT name FROM pragma_table_xinfo('tasks') ORDER BY cid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("The task columns are %v, want %v.", got, want)
	}
}
