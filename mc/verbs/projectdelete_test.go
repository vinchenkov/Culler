package verbs

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"mc/dispatch"
)

func TestProjectDeleteIncludesTasksReviewsAndEverySetupTable(t *testing.T) {
	f := newDeliveryFixture(t)
	db := f.db
	task := f.add("Complete the Project contribution")
	f.pass(task, "feature.txt", "The accepted contribution.\n")
	f.merge(task, "prepare")
	f.approve(task)
	f.merge(task, "apply")
	dvExec(t, db, `INSERT INTO outbox(kind,surface,payload) VALUES('blocked_alert','dashboard',json_object('task_id',?))`, task)
	dvAddProject(t, db, "other", "active")
	if _, err := TaskAdd(db, nil, "Keep the other task", "other", "The other requirement.", nil); err != nil {
		t.Fatal(err)
	}
	revision := "fixture-environment-ws-test"
	dvExec(t, db, `INSERT INTO environment_operations(id,project_id,revision_id,kind,status,inputs_digest,image_digest,ended_at)
		VALUES('setup-record','ws-test',?,'setup','succeeded','inputs',?,datetime('now'))`, revision, environmentTestBase)
	dvExec(t, db, `INSERT INTO environment_results(id,project_id,operation_id,revision_id,image_digest,runtime_identity,outcome,summary,tool_versions,package_records)
		VALUES('setup-result','ws-test','setup-record',?,?,'runtime','complete','The setup is complete.','{}','[]')`, revision, environmentTestBase)
	dvExec(t, db, `INSERT INTO environment_approvals(revision_id,project_id,inputs_digest) VALUES(?,'ws-test','inputs')`, revision)
	dvExec(t, db, `INSERT INTO environment_decisions(revision_id,project_id,operation_id,decision,current_revision_id,current_image_digest)
		VALUES(?,'ws-test','setup-record','approved',?,?)`, revision, revision, environmentTestBase)
	dvExec(t, db, `INSERT INTO run_environments(run_id,project_id,revision_id,image_digest,base_digest,source_commit,runtime_identity)
		VALUES('setup-record','ws-test',?,?,?,'commit','runtime')`, revision, environmentTestBase, environmentTestBase)
	dvExec(t, db, `INSERT INTO environment_run_evidence(run_id,tool_versions,package_records) VALUES('setup-record','{}','[]')`)
	dvExec(t, db, `INSERT INTO environment_storage(project_id,image_digest,metadata) VALUES('ws-test',?,'{}')`, environmentTestBase)
	dvExec(t, db, `INSERT INTO environment_runtime_checks(project_id,image_digest,runtime_identity,status)
		VALUES('ws-test',?,'runtime','succeeded')`, environmentTestBase)
	dvExec(t, db, `INSERT INTO environment_candidates(revision_id,project_id,source_commit,image_digest,result_id)
		VALUES(?,'ws-test','commit',?,'setup-result')`, revision, environmentTestBase)
	dvExec(t, db, `INSERT INTO environment_associations(project_id,revision_id,candidate_source,source_commit,status)
		VALUES('ws-test',?,'commit','commit','complete')`, revision)
	run := dfStr(t, db, `SELECT id FROM runs WHERE project='ws-test' LIMIT 1`)
	dvExec(t, db, `INSERT INTO environment_proposals(revision_id,run_id,task_id,source_commit) VALUES(?,?,?,'commit')`, revision, run, task)
	dvExec(t, db, `INSERT INTO diagnostic_events(event_id,source,operation,outcome,project_id,run_id,observed_at,submission)
		VALUES(?,'mc','setup','succeeded','ws-test',?,datetime('now'),'{}')`, strings.Repeat("a", 32), run)
	dvExec(t, db, `INSERT INTO connection_health(connection_id,operation,authorization_id,state,latest_diagnostic_id)
		VALUES('shared','connection_access','authorization','ready',(SELECT MAX(id) FROM diagnostic_events))`)
	if _, err := ProjectSetStatus(db, nil, "ws-test", "paused"); err != nil {
		t.Fatal(err)
	}
	before := srcGit(t, f.workspace, "rev-parse", "HEAD")
	preview, err := ProjectDeletePreview(db, nil, "ws-test")
	if err != nil || preview.Tasks != 1 || preview.Reviews != 1 || preview.SetupOperations != 1 || len(preview.Blockers) != 0 {
		t.Fatalf("The deletion preview is incorrect: %+v, %v", preview, err)
	}
	if _, err := ProjectDelete(db, nil, "ws-test"); err != nil {
		t.Fatal(err)
	}
	for _, record := range projectDeletionTables {
		if n := dfInt(t, db, `SELECT count(*) FROM `+record.table); n != projectDeleteOtherCount(t, db, record.table) {
			t.Errorf("The table %s retains Project records: %d", record.table, n)
		}
	}
	if n := dfInt(t, db, `SELECT count(*) FROM environment_base_sources`); n != 1 {
		t.Fatal("Deletion changed the shared image source.")
	}
	if n := dfInt(t, db, `SELECT count(*) FROM sandbox_profiles`); n != 2 {
		t.Fatal("Deletion removed a sandbox profile.")
	}
	if got := dfStr(t, db, `SELECT state FROM connection_health WHERE connection_id='shared'`); got != "ready" {
		t.Fatal("Deletion changed shared connection access.")
	}
	if n := dfInt(t, db, `SELECT count(*) FROM connection_health WHERE latest_diagnostic_id IS NOT NULL`); n != 0 {
		t.Fatal("The shared connection retains a reference to deleted diagnostics.")
	}
	if got := srcGit(t, f.workspace, "rev-parse", "HEAD"); got != before {
		t.Fatal("Deletion changed the source repository.")
	}
	if _, err := os.Stat(filepath.Join(f.workspace, "feature.txt")); err != nil {
		t.Fatal("Deletion removed an accepted source file.")
	}
	if n := dfInt(t, db, `SELECT count(*) FROM project_deletion_records`); n != 0 {
		t.Fatal("Deletion retained its transaction scope.")
	}
	if n := dfInt(t, db, `SELECT count(*) FROM pragma_foreign_key_check`); n != 0 {
		t.Fatal("Deletion left invalid foreign keys.")
	}
	// Deleted task identifiers must never identify a new task.
	value, err := TaskAdd(db, nil, "Another task", "other", "Another requirement.", nil)
	if err != nil || value.(map[string]any)["task_id"].(int64) <= task {
		t.Fatalf("Deletion allowed task identifier reuse: %v, %v", value, err)
	}
}

func projectDeleteOtherCount(t *testing.T, db *sql.DB, table string) int64 {
	t.Helper()
	switch table {
	case "projects", "tasks", "project_environments", "environment_revisions", "leases", "repository_limits":
		return 1
	case "activity":
		return dfInt(t, db, `SELECT count(*) FROM activity WHERE project='other'`)
	default:
		return 0
	}
}

func TestProjectDeleteIncludesVisionAndSetupConversations(t *testing.T) {
	db := dvSpine(t)
	draft := newVisionDraft(t, db)
	draftCall(t, db, "vision-submit", draftSubmission(draft))
	value, err := CollieStart(db, nil, CollieStartArgs{From: "dashboard:vision-ws-test", VisionProject: "ws-test"})
	if err != nil {
		t.Fatal(err)
	}
	session := value.(CollieStartResult).SessionID
	hwPendingInbound(t, db, session, 1)
	hwSession(t, db, "ordinary")
	hwPendingInbound(t, db, "ordinary", 1)
	if _, err := ProjectDelete(db, nil, "ws-test"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"projects", "vision_drafts", "vision_submissions"} {
		if n := dfInt(t, db, `SELECT count(*) FROM `+table); n != 0 {
			t.Errorf("Deletion retained %s", table)
		}
	}
	if n := dfInt(t, db, `SELECT count(*) FROM collie_sessions WHERE id=?`, session); n != 0 {
		t.Fatal("Deletion retained the setup conversation.")
	}
	if n := dfInt(t, db, `SELECT count(*) FROM conversation_messages WHERE session_id='ordinary'`); n != 1 {
		t.Fatal("Deletion changed an ordinary conversation.")
	}
}

func TestProjectDeletePreservesTaskAndReviewIdentifiersAcrossReimport(t *testing.T) {
	f := newDeliveryFixture(t)
	first := f.add("The first task")
	f.pass(first, "first.txt", "The first change.\n")
	f.merge(first, "prepare")
	packet := dfInt(t, f.db, `SELECT id FROM review_versions WHERE task_id=?`, first)
	if _, err := ProjectSetStatus(f.db, nil, "ws-test", "paused"); err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectDelete(f.db, nil, "ws-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectAdd(f.db, nil, ProjectAddArgs{ID: "ws-test", Title: "The Project", SandboxProfile: "default"}); err != nil {
		t.Fatal(err)
	}
	dvReadyEnvironment(t, f.db, "ws-test")
	second := f.add("The second task")
	f.pass(second, "second.txt", "The second change.\n")
	f.merge(second, "prepare")
	newPacket := dfInt(t, f.db, `SELECT id FROM review_versions WHERE task_id=?`, second)
	if second <= first || newPacket <= packet {
		t.Fatalf("Deletion reused a task or review identifier: tasks %d/%d, reviews %d/%d", first, second, packet, newPacket)
	}
}

func TestProjectDeleteRefusesEveryAgentIdentity(t *testing.T) {
	for _, tier := range []string{"pipeline", "setup", "collie", ""} {
		id := &RunIdentity{Tier: tier, VerbAllowlist: []string{"project.delete", "project.delete-preview"}}
		if _, err := ProjectDelete(nil, id, "project"); err == nil {
			t.Fatalf("Deletion admitted the agent tier %q", tier)
		}
		if _, err := ProjectDeletePreview(nil, id, "project"); err == nil {
			t.Fatalf("The preview admitted the agent tier %q", tier)
		}
	}
}

func TestProjectDeleteRemovesActiveAndStaleRecords(t *testing.T) {
	for _, kind := range []string{"active", "agent", "lease", "setup", "conversation", "vision", "wiki", "archived", "queued", "missing"} {
		t.Run(kind, func(t *testing.T) {
			db := dvSpine(t)
			draft := newVisionDraft(t, db)
			switch kind {
			case "active":
				dvExec(t, db, `UPDATE projects SET setup_status='ready',status='active' WHERE id='ws-test'`)
			case "agent", "lease":
				dvExec(t, db, `INSERT INTO runs(id,tier,role,project) VALUES('agent','pipeline','worker','ws-test')`)
				if kind == "lease" {
					dvExec(t, db, `UPDATE runs SET ended_at=datetime('now') WHERE id='agent'`)
					dvHold(t, db, "ws-test", "agent", "worker", nil, 0, nil, 0)
				}
			case "setup", "queued":
				dvExec(t, db, `INSERT INTO environment_revisions(id,project_id,editor,configuration,inputs_digest)
				 VALUES('revision','ws-test','fixture','{}','digest')`)
				status := "running"
				if kind == "queued" {
					status = "queued"
				}
				dvExec(t, db, `INSERT INTO environment_operations(id,project_id,revision_id,kind,status,inputs_digest,image_digest)
				 VALUES('operation','ws-test','revision','setup',?,'digest','image')`, status)
			case "conversation":
				value, err := CollieStart(db, nil, CollieStartArgs{From: "dashboard:vision-ws-test", VisionProject: "ws-test"})
				if err != nil {
					t.Fatal(err)
				}
				dvExec(t, db, `UPDATE collie_sessions SET current_launch_id=?,current_launch_mode='fresh' WHERE id=?`, strings.Repeat("a", 16), value.(CollieStartResult).SessionID)
			case "vision":
				draftCall(t, db, "vision-submit", draftSubmission(draft))
				dvExec(t, db, `UPDATE vision_submissions SET attempted=1 WHERE project_id='ws-test'`)
			case "wiki":
				wrActive(t, db, "ws-test", "wiki-run", strings.Repeat("a", 40), "boot", 0, nil, 1)
				dvExec(t, db, `UPDATE runs SET ended_at=datetime('now') WHERE id='wiki-run'`)
				dvExec(t, db, `UPDATE wiki_updates SET ended_at=datetime('now'),result='failed',cause='The agent ended.' WHERE run_id='wiki-run'`)
			case "archived":
				dvExec(t, db, `UPDATE projects SET status='archived' WHERE id='ws-test'`)
			}
			project := "ws-test"
			if kind == "missing" {
				project = "absent"
			}
			_, err := ProjectDelete(db, nil, project)
			if kind != "missing" {
				if err != nil {
					t.Fatal(err)
				}
				if dfInt(t, db, `SELECT count(*) FROM projects`) != 0 || dfInt(t, db, `SELECT count(*) FROM pragma_foreign_key_check`) != 0 {
					t.Fatal("Deletion retained the Project or invalid references.")
				}
				return
			}
			if err == nil || dfInt(t, db, `SELECT count(*) FROM projects`) != 1 || dfInt(t, db, `SELECT count(*) FROM vision_drafts`) != 1 {
				t.Fatalf("Deletion changed a Project with a rejection: %v", err)
			}
			if n := dfInt(t, db, `SELECT count(*) FROM project_deletion_records`); n != 0 {
				t.Fatal("A rejected deletion retained its scope.")
			}
		})
	}
}

func TestProjectDeleteRollsBackLateFailures(t *testing.T) {
	for _, failure := range []string{"trigger", "foreign-key"} {
		t.Run(failure, func(t *testing.T) {
			db := dvSpine(t)
			newVisionDraft(t, db)
			dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
			if failure == "trigger" {
				dvExec(t, db, `CREATE TRIGGER reject_project_delete BEFORE DELETE ON projects
				 BEGIN SELECT RAISE(ABORT,'The fixture rejects deletion.'); END`)
			} else {
				dvExec(t, db, `CREATE TABLE retained_reference(project TEXT REFERENCES projects(id))`)
				dvExec(t, db, `INSERT INTO retained_reference VALUES('ws-test')`)
			}
			before := projectDeleteDump(t, db)
			if _, err := ProjectDelete(db, nil, "ws-test"); err == nil {
				t.Fatal("Deletion ignored the injected failure.")
			}
			if after := projectDeleteDump(t, db); !reflect.DeepEqual(after, before) {
				t.Fatal("A failed deletion changed database records.")
			}
			if _, err := db.Exec(`DELETE FROM tasks`); err == nil {
				t.Fatal("A failed deletion left record protections disabled.")
			}
		})
	}
}

func projectDeleteDump(t *testing.T, db *sql.DB) map[string][]map[string]any {
	t.Helper()
	out := map[string][]map[string]any{}
	for _, record := range projectDeletionTables {
		rows, err := db.Query(`SELECT * FROM ` + record.table + ` ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		out[record.table], err = rowsToMaps(rows)
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	out["scope"] = []map[string]any{{"count": dfInt(t, db, `SELECT count(*) FROM project_deletion_records`)}}
	return out
}

func TestProjectDeleteCoversEveryDependentTable(t *testing.T) {
	db := dvSpine(t)
	covered := map[string]bool{"project_deletion_records": true, "connection_health": true}
	for _, record := range projectDeletionTables {
		covered[record.table] = true
		// Each selector must use the actual current schema.
		rows, err := db.Query(`SELECT rowid FROM `+record.table+` WHERE `+record.where, sql.Named("project", "ws-test"))
		if err != nil {
			t.Fatalf("The selector for %s fails: %v", record.table, err)
		}
		rows.Close()
	}
	rows, err := db.Query(`SELECT m.name, f."table" FROM sqlite_master m JOIN pragma_foreign_key_list(m.name) f
		WHERE m.type='table'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var references [][2]string
	for rows.Next() {
		var pair [2]string
		if err := rows.Scan(&pair[0], &pair[1]); err != nil {
			t.Fatal(err)
		}
		references = append(references, pair)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	dependent := map[string]bool{"projects": true}
	for changed := true; changed; {
		changed = false
		for _, pair := range references {
			if dependent[pair[1]] && !dependent[pair[0]] {
				dependent[pair[0]] = true
				changed = true
			}
		}
	}
	for table := range dependent {
		if !covered[table] {
			t.Errorf("Project deletion must account for the dependent table %s", table)
		}
	}
	// A scope for one Project cannot permit deletion from another Project.
	dvAddProject(t, db, "other", "paused")
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	err = inTx(db, func(ctx context.Context, q Q) error {
		_, err := q.ExecContext(ctx, `INSERT INTO project_deletion_records(project,table_name,row_id)
			SELECT 'ws-test','tasks',rowid FROM tasks WHERE project='ws-test'`)
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM projects WHERE id='other'`); err == nil {
			t.Error("The deletion scope admitted another Project.")
		}
		return fmt.Errorf("The fixture rolls back its deletion scope")
	})
	if err == nil || dfInt(t, db, `SELECT count(*) FROM projects`) != 2 {
		t.Fatal("The deletion scope changed another Project.")
	}
}

func TestProjectDeleteRemovesPacketConversations(t *testing.T) {
	db := dvSpine(t)
	packet, version := hpPacket(t, db, 1)
	first := hpMustPrepare(t, db, packet, version, false)
	hpSend(t, db, first, "A question about the packet.")
	current := hpMustPrepare(t, db, packet, version, true)
	hpSend(t, db, current, "Another question.")
	dvExec(t, db, `INSERT INTO collie_discord_threads(session_id,guild_id,channel_id,thread_id) VALUES(?,'1','2','100')`, first.SessionID)
	ordinary := threadSession(t, db, "101")
	hwPendingInbound(t, db, ordinary, 1)

	targets, err := ProjectContainerTargets(db, nil, "ws-test")
	if err != nil {
		t.Fatal(err)
	}
	sessions := targets["sessions"].([]string)
	if len(sessions) != 2 || !slices.Contains(sessions, first.SessionID) || !slices.Contains(sessions, current.SessionID) {
		t.Fatalf("the container targets name the sessions %v, want both packet conversations", sessions)
	}
	if _, err := ProjectDelete(db, nil, "ws-test"); err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{first.SessionID, current.SessionID} {
		for _, table := range []string{"collie_sessions", "collie_bindings", "collie_discord_threads", "conversation_messages"} {
			column := "session_id"
			if table == "collie_sessions" {
				column = "id"
			}
			if n := dfInt(t, db, `SELECT count(*) FROM `+table+` WHERE `+column+`=?`, session); n != 0 {
				t.Errorf("the deletion retained %d %s rows of the packet conversation %s", n, table, session)
			}
		}
	}
	if n := dfInt(t, db, `SELECT count(*) FROM conversation_messages WHERE session_id=?`, ordinary); n != 1 {
		t.Fatal("the deletion changed an ordinary conversation")
	}
	if n := dfInt(t, db, `SELECT count(*) FROM collie_discord_threads WHERE session_id=? AND thread_id='101'`, ordinary); n != 1 {
		t.Fatal("The deletion changed an ordinary Discord thread association.")
	}
	if n := dfInt(t, db, `SELECT count(*) FROM pragma_foreign_key_check`); n != 0 {
		t.Fatal("the deletion left invalid foreign keys")
	}
}
