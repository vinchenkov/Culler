package main_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func boardRequest(t *testing.T, spine string, request map[string]any) mcResult {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return runMC(t, spineEnv(spine), string(body), "board", "write")
}

func TestBoardCreateRetryAcrossProcesses(t *testing.T) {
	spine := initSpine(t)
	request := map[string]any{
		"operation_id": "create-task-from-board", "action": "create", "scope": "task",
		"project": "ws-test", "title": "The operator task", "description": "The output meets the requirement.", "priority": -1,
	}
	created := boardRequest(t, spine, request)
	if created.code != 0 {
		t.Fatalf("create: %s", created.stderr)
	}
	body, _ := json.Marshal(request)
	replayed := runMC(t, spineEnv(spine), "", "board", "write", "--request", string(body))
	if replayed.code != 0 || !reflect.DeepEqual(replayed.json, created.json) {
		t.Fatalf("replay = %v, want %v; %s", replayed.json, created.json, replayed.stderr)
	}
	id := created.json["task_id"]
	record := runMC(t, spineEnv(spine), "", "task", "get", fmt.Sprint(id))
	for key, want := range map[string]any{"status": "seeded", "scope": "task", "origin": "user", "project": "ws-test", "priority": float64(-1)} {
		if record.code != 0 || record.json[key] != want {
			t.Fatalf("task %s = %v, want %v; %s", key, record.json[key], want, record.stderr)
		}
	}
	if got := queryInt(t, openDB(t, spine), `SELECT count(*) FROM tasks`); got != 1 {
		t.Fatalf("task count = %d, want 1", got)
	}
	request["title"] = "A different request"
	conflict := boardRequest(t, spine, request)
	if conflict.code != 1 || conflict.json["error"].(map[string]any)["code"] != "operation-conflict" {
		t.Fatalf("reused operation = %v; %s", conflict.json, conflict.stderr)
	}
}

func TestTaskRevisionCommandsExposeSeparateViews(t *testing.T) {
	spine := initSpine(t)
	created := boardRequest(t, spine, map[string]any{
		"operation_id": "create-revised-task", "action": "create", "scope": "task", "project": "ws-test",
		"title": "Export", "description": "The export includes dates.",
	})
	if created.code != 0 {
		t.Fatal(created.stderr)
	}
	task := fmt.Sprint(created.json["task_id"])
	revised := boardRequest(t, spine, map[string]any{
		"operation_id": "edit-revised-task", "action": "revise", "task_id": created.json["task_id"],
		"expected_version": created.json["record_version"], "title": "Export the report", "description": "The export includes ISO dates.",
	})
	if revised.code != 0 {
		t.Fatal(revised.stderr)
	}
	revision := fmt.Sprint(revised.json["revision_id"])
	summary := runMC(t, spineEnv(spine), "", "task", "revision", task, "--revision", revision)
	if summary.code != 0 || summary.json["title"] != "Export the report" || summary.json["description_changed"] != true || summary.json["text"] != nil {
		t.Fatalf("The summary is %v: %s", summary.json, summary.stderr)
	}
	changes := runMC(t, spineEnv(spine), "", "task", "revision", task, "--revision", revision, "--view", "changes")
	if changes.code != 0 || !strings.Contains(fmt.Sprint(changes.json["text"]), "+The export includes ISO dates.") {
		t.Fatalf("The description diff is %v: %s", changes.json, changes.stderr)
	}
	correction := boardRequest(t, spine, map[string]any{
		"operation_id": "correct-revised-task", "action": "revise", "task_id": created.json["task_id"],
		"expected_version": revised.json["record_version"], "instructions": "Change Export now to Export report.",
	})
	if correction.code != 0 {
		t.Fatal(correction.stderr)
	}
	instructions := runMC(t, spineEnv(spine), "", "task", "revision", task, "--revision", fmt.Sprint(correction.json["revision_id"]), "--view", "instructions", "--offset", "0")
	if instructions.code != 0 || instructions.json["text"] != "Change Export now to Export report." || instructions.json["next_offset"] != nil {
		t.Fatalf("The instructions are %v: %s", instructions.json, instructions.stderr)
	}
	if invalid := runMC(t, spineEnv(spine), "", "task", "revision", task, "--revision", revision, "--offset", "-1"); invalid.code != 2 {
		t.Fatalf("The command accepted an invalid offset: %v", invalid.json)
	}
}

func TestBoardEditVersionAndProtectedFields(t *testing.T) {
	spine := initSpine(t)
	created := boardRequest(t, spine, map[string]any{
		"operation_id": "new-record", "action": "create", "scope": "task", "project": "ws-test", "title": "The initial title",
	})
	if created.code != 0 {
		t.Fatalf("create: %s", created.stderr)
	}
	id := created.json["task_id"]
	current := runMC(t, spineEnv(spine), "", "task", "get", fmt.Sprint(id))
	request := map[string]any{
		"operation_id": "first-edit", "action": "edit", "task_id": id,
		"expected_version": current.json["record_version"], "title": "The accepted title",
	}
	if result := boardRequest(t, spine, request); result.code != 0 {
		t.Fatalf("edit: %s", result.stderr)
	}
	request["operation_id"] = "concurrent-edit"
	request["title"] = "The stale title"
	conflict := boardRequest(t, spine, request)
	if conflict.code != 1 || conflict.json["error"].(map[string]any)["code"] != "record-conflict" {
		t.Fatalf("version conflict = %v; %s", conflict.json, conflict.stderr)
	}
	current = runMC(t, spineEnv(spine), "", "task", "get", fmt.Sprint(id))
	if current.json["title"] != "The accepted title" || current.json["status"] != "seeded" {
		t.Fatalf("current task = %v", current.json)
	}
	request["expected_version"] = current.json["record_version"]
	for _, field := range []string{"status", "branch", "origin", "archived", "project"} {
		t.Run(field, func(t *testing.T) {
			request["operation_id"] = "protected-" + field
			request[field] = "forged"
			defer delete(request, field)
			if result := boardRequest(t, spine, request); result.code == 0 {
				t.Fatalf("the edit accepted %s", field)
			}
		})
	}
}

func TestBoardRejectsInvalidRequestDocuments(t *testing.T) {
	spine := initSpine(t)
	for _, body := range []string{"null", "[]", "{} {}", `{"action":"create","operation_id":"bad","scope":"task","project":"ws-test","title":"T","decision":"approved"}`} {
		result := runMC(t, spineEnv(spine), body, "board", "write")
		if result.code == 0 {
			t.Fatalf("the command accepted %s", body)
		}
	}
	if got := queryInt(t, openDB(t, spine), `SELECT count(*) FROM tasks`); got != 0 {
		t.Fatalf("invalid requests created %d tasks", got)
	}
}

func TestBoardHistoryReportRequiresItsTask(t *testing.T) {
	spine := initSpine(t)
	task := proposalTask(t, spine, "The report subject")
	other := proposalTask(t, spine, "Another task")
	db := openDB(t, spine)
	for _, query := range []string{
		`INSERT INTO runs(id,tier,role,project,subject,ended_at,outcome,binding) SELECT 'history-result','pipeline','worker',project,id,datetime('now'),'completed','fake' FROM tasks WHERE id=?`,
		`INSERT INTO repository_runs(run_id,task_id,input_commit,destination_commit,temporary_bytes,captured,cleaned) VALUES('history-result',?,'base','base',0,1,1)`,
		`INSERT INTO version_submissions(run_id,task_id,input_commit,result_commit) VALUES('history-result',?,'base','result')`,
		`INSERT INTO task_versions(task_id,run_id,input_commit,result_commit,destination_branch,retained_ref) VALUES(?,'history-result','base','result','culler','refs/test/history')`,
		`INSERT INTO requirements_revisions(operation_id,task_id,expected_version,requirements,previous_requirements,reason,actor) VALUES('history-revision',?,1,'new','old','The operator revised the requirements.','operator')`,
	} {
		if _, err := db.Exec(query, task); err != nil {
			t.Fatal(err)
		}
	}
	result, err := db.Exec(`INSERT INTO review_versions
		(task_id,version_id,requirements_version,render_path,report_sha256,thesis,criteria)
		SELECT ?,id,1,'/mc/artifacts/reports/original.md','hash','The original result','old' FROM task_versions WHERE run_id='history-result'`, task)
	if err != nil {
		t.Fatal(err)
	}
	record, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	for _, subject := range []int64{task, other} {
		response := runMC(t, spineEnv(spine), "", "board", "history-report",
			"--task", fmt.Sprint(subject), "--record", fmt.Sprint(record), "--kind", "packet")
		if response.code != 0 {
			t.Fatalf("history: %s", response.stderr)
		}
		if subject == task && response.json["render_path"] != "/mc/artifacts/reports/original.md" {
			t.Fatalf("recorded report = %v", response.json)
		}
		if subject == other && response.json["render_path"] != nil {
			t.Fatalf("another task exposed the report: %v", response.json)
		}
	}
}

func TestBoardCollieUsesTheOperatorRequestContract(t *testing.T) {
	spine := initSpine(t)
	started := runMC(t, spineEnv(spine), "", "collie", "start", "--from", "cli:board-controls")
	if started.code != 0 {
		t.Fatalf("start Collie: %s", started.stderr)
	}
	session := started.json["session_id"].(string)
	body := `{"operation_id":"collie-create","action":"create","scope":"task","project":"ws-test","title":"The requested task","description":"The operator can verify each result."}`
	env := collieJSONEnv(t, spine, session, defaultCollieAllowlist)
	created := runMC(t, env, body, "board", "write")
	if created.code != 0 {
		t.Fatalf("Collie create: %s", created.stderr)
	}
	db := openDB(t, spine)
	task := int64(created.json["task_id"].(float64))
	if got := queryString(t, db, `SELECT status || '/' || origin || '/' || scope FROM tasks WHERE id=?`, task); got != "seeded/user/task" {
		t.Fatalf("Collie creation = %q", got)
	}
	if got := queryString(t, db, `SELECT actor FROM board_operations WHERE operation_id='collie-create'`); got != "collie:"+session {
		t.Fatalf("actor = %q", got)
	}
	if _, err := db.Exec(`UPDATE collie_sessions SET status='ended' WHERE id=?`, session); err != nil {
		t.Fatal(err)
	}
	if rejected := runMC(t, env, body, "board", "write"); rejected.code != 1 {
		t.Fatalf("ended Collie = %v; %s", rejected.json, rejected.stderr)
	}
	if rejected := runMC(t, runJSONEnv(t, spine, "pipeline-fixture", "pipeline", "worker"), body, "board", "write"); rejected.code != 1 {
		t.Fatalf("pipeline write = %v; %s", rejected.json, rejected.stderr)
	}
	if got := queryInt(t, db, `SELECT count(*) FROM tasks`); got != 1 {
		t.Fatalf("task count = %d, want 1", got)
	}
}
