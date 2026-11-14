package verbs

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"mc/dispatch"
	"mc/domain"
)

func TestTaskRevisionKeepsRequirementsAndAllowanceForCorrections(t *testing.T) {
	for _, stage := range []dispatch.Status{dispatch.StatusSeeded, dispatch.StatusWorked, dispatch.StatusVerified, dispatch.StatusPackaged} {
		t.Run(string(stage), func(t *testing.T) {
			db := dvSpine(t)
			dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, stage, 2))
			dvExec(t, db, `UPDATE tasks SET description='The export works.',correction_count=2 WHERE id=1`)
			request := BoardWriteRequest{OperationID: "string-correction", Action: "revise", TaskID: 1,
				ExpectedVersion: boardVersion(t, db, 1), Title: boardText("Export the report"), Instructions: boardText("Replace Export now with Export report.")}
			first := boardRequest(t, db, request)
			if first["revision_state"] != "applied" {
				t.Fatal(first)
			}
			if got := dfStr(t, db, `SELECT status||':'||title||':'||description||':'||requirements_version||':'||correction_count FROM tasks WHERE id=1`); got != "seeded:Export the report:The export works.:1:2" {
				t.Fatal(got)
			}
			boardRequest(t, db, request)
			if dfInt(t, db, `SELECT count(*) FROM requirements_revisions`) != 1 {
				t.Fatal("The retry created another revision.")
			}
			_, err := BoardWrite(db, nil, BoardWriteRequest{OperationID: "empty", Action: "revise", TaskID: 1, ExpectedVersion: boardVersion(t, db, 1), Title: request.Title})
			requireBoardCode(t, err, domain.CodeInvalidRequest)
		})
	}
}

func TestTaskRevisionKeepsAnUnresolvedVerifierCorrection(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Correct the export")
	for attempt := 0; attempt <= 3; attempt++ {
		f.advance(task, "answer.txt", "A rejected answer.\n", "worked")
		submitVerdict(t, f, task, "correct")
	}
	before := dfStr(t, f.db, `SELECT correction_reason FROM tasks WHERE id=?`, task)
	boardRequest(t, f.db, BoardWriteRequest{OperationID: "keep-correction", Action: "revise", TaskID: task,
		ExpectedVersion: boardVersion(t, f.db, task), Instructions: boardText("Also correct the export label.")})
	if got := dfStr(t, f.db, `SELECT correction_reason FROM tasks WHERE id=?`, task); got != before {
		t.Fatal("The revision lost the unresolved correction.")
	}
	boardRequest(t, f.db, BoardWriteRequest{OperationID: "allow-correction", Action: "allow-corrections", TaskID: task,
		ExpectedVersion: boardVersion(t, f.db, task), Reason: boardText("Continue the correction.")})
	brief := briefFor(t, f.db, dispatch.RoleWorker, &task, briefInputs{})
	part := brief.part(partCorrection)
	if part == nil || part.fact("Correction file") != before || brief.part(partRevision) != nil {
		t.Fatalf("The Worker lost the correction pointer: %+v", part)
	}
	if dfInt(t, f.db, `SELECT blocked FROM tasks WHERE id=?`, task) != 0 {
		t.Fatal("The correction grant did not end its condition.")
	}
}

func TestTaskRevisionDefersTitleAndInstructionsUntilTheRunEnds(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	oldTitle := dfStr(t, db, `SELECT title FROM tasks WHERE id=1`)
	id := fhClaim(t, db, 1, "old-worker", "worker")
	result := boardRequest(t, db, BoardWriteRequest{OperationID: "wait-title", Action: "revise", TaskID: 1,
		ExpectedVersion: boardVersion(t, db, 1), Title: boardText("The new title"), Instructions: boardText("Correct one line.")})
	if result["revision_state"] != "pending" || dfStr(t, db, `SELECT title FROM tasks WHERE id=1`) != oldTitle {
		t.Fatal(result)
	}
	if _, err := db.Exec(`UPDATE requirements_revisions SET state='applied',applied_at=datetime('now') WHERE task_id=1`); err == nil {
		t.Fatal("The revision applied without saved evidence while the run held its lease.")
	}
	if stale, err := domain.RunRequirementsSuperseded(context.Background(), db, id.RunID, 1); err != nil || !stale {
		t.Fatalf("The old run remains current: %v %v", stale, err)
	}
	if _, err := Complete(db, id, CompleteArgs{Task: 1, Run: id.RunID, NeedsOperator: true, Reason: "Old question"}); err != nil {
		t.Fatal(err)
	}
	if _, err := RunStopped(db, nil, id.RunID); err != nil {
		t.Fatal(err)
	}
	if err := inTx(db, domain.ApplyPendingTaskRevisions); err != nil {
		t.Fatal(err)
	}
	if dfStr(t, db, `SELECT title FROM tasks WHERE id=1`) != "The new title" {
		t.Fatal("The title did not apply.")
	}
	if stale, err := domain.RunRequirementsSuperseded(context.Background(), db, id.RunID, 1); err != nil || !stale {
		t.Fatalf("The applied correction lost its fence: %v %v", stale, err)
	}
}

func TestTaskRevisionSmallCorrectionPreservesCodeAndPacketHistory(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Deliver the report")
	large := strings.Repeat("The existing feature stays intact.\n", 1000)
	start := f.advance(task, "feature.txt", large+"Export now\n", "packaged")
	f.merge(task, "prepare")
	packet := dfInt(t, f.db, `SELECT current_review_id FROM tasks WHERE id=?`, task)
	version := dfInt(t, f.db, `SELECT submitted_version FROM tasks WHERE id=?`, task)
	result := boardRequest(t, f.db, BoardWriteRequest{OperationID: "one-line", Action: "revise", TaskID: task,
		ExpectedVersion: boardVersion(t, f.db, task), Instructions: boardText("In feature.txt, replace Export now with Export report."), PacketID: packet, VersionID: version})
	revision := result["revision_id"].(int64)
	if verdict, err := loadLatestVerdict(context.Background(), f.db, task); err != nil || verdict != nil {
		t.Fatalf("The old pass remains current: %v %v", verdict, err)
	}
	if _, err := PacketDecide(f.db, nil, task, "approve", "", packet, version); err == nil {
		t.Fatal("The old packet accepted approval.")
	}
	run, root, id := f.run(task, "worker")
	if got := srcGit(t, filepath.Join(root, "repo"), "rev-parse", "HEAD"); got != start {
		t.Fatalf("The revision lost saved code: %s", got)
	}
	evidence, err := TaskRevisionRead(f.db, id, task, revision, "evidence", 0)
	if err != nil {
		t.Fatal(err)
	}
	pointers := evidence.(map[string]any)["previous_evidence"].(map[string]any)
	if evidence.(map[string]any)["start_commit"] != start || pointers["verifier_evidence"] == nil || pointers["report_path"] == nil {
		t.Fatalf("The revision lost prior evidence pointers: %v", evidence)
	}
	commit := f.commit(root, "feature.txt", large+"Export report\n")
	diff := srcGit(t, filepath.Join(root, "repo"), "diff", "--numstat", start, commit)
	if diff != "1\t1\tfeature.txt" {
		t.Fatalf("The correction changed unrelated code: %q", diff)
	}
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "worked", Commit: commit}); err != nil {
		t.Fatal(err)
	}
	f.capture(task, run, root)
	run, root, id = f.run(task, "verifier")
	if _, err := VerifierVerdict(f.db, id, VerdictArgs{Task: task, Run: run, Outcome: "pass", SHA: commit, Evidence: f.artifact(run, "The correction passes."), StoreWorktree: filepath.Join(root, "repo")}); err != nil {
		t.Fatal(err)
	}
	f.cleanup(task, run, root)
	run, root, id = f.run(task, "packager")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "packaged", Outputs: f.artifact(run, "The export text changed."), Thesis: "The export text changed."}); err != nil {
		t.Fatal(err)
	}
	f.cleanup(task, run, root)
	f.merge(task, "prepare")
	value, err := TaskGet(f.db, task)
	if err != nil {
		t.Fatal(err)
	}
	history := value.(map[string]any)["packet_history"].([]map[string]any)
	if len(history) != 1 || fmt.Sprint(history[0]["id"]) != fmt.Sprint(packet) {
		t.Fatalf("The new packet became historical: %+v", history)
	}
	boardRequest(t, f.db, BoardWriteRequest{OperationID: "after-pass", Action: "revise", TaskID: task,
		ExpectedVersion: boardVersion(t, f.db, task), Instructions: boardText("Add a full stop to the report text.")})
	part := briefFor(t, f.db, dispatch.RoleWorker, &task, briefInputs{}).part(partRequirementsRevision)
	if part.fact("Earlier instructions") != "" {
		t.Fatal("A verified correction entered the next revision as unresolved work.")
	}
}

func TestTaskRevisionRefusesAStaleReviewedPacket(t *testing.T) {
	db := dvSpine(t)
	oldPacket, oldVersion := hpPacket(t, db, 1)
	hpRevisedPacket(t, db, 1, oldPacket, oldVersion)
	request := BoardWriteRequest{OperationID: "stale-reviewed-packet", Action: "revise", TaskID: 1,
		ExpectedVersion: boardVersion(t, db, 1), PacketID: oldPacket, VersionID: oldVersion, Instructions: boardText("Change the old result.")}
	before := dfInt(t, db, `SELECT count(*) FROM requirements_revisions`)
	_, err := BoardWrite(db, nil, request)
	requireBoardCode(t, err, domain.CodeRecordConflict)
	if dfInt(t, db, `SELECT count(*) FROM requirements_revisions`) != before {
		t.Fatal("The stale packet changed the current task.")
	}
}

func TestTaskRevisionRetainsUnverifiedInstructionsThroughPointers(t *testing.T) {
	db := dvSpine(t)
	task := dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2)
	dvInsertTask(t, db, task)
	instructions := strings.Repeat("Keep the first correction.\n", 100)
	first := boardRequest(t, db, BoardWriteRequest{OperationID: "first", Action: "revise", TaskID: 1,
		ExpectedVersion: boardVersion(t, db, 1), Instructions: &instructions})["revision_id"].(int64)
	second := boardRequest(t, db, BoardWriteRequest{OperationID: "second", Action: "revise", TaskID: 1,
		ExpectedVersion: boardVersion(t, db, 1), Description: boardText("The export uses ISO dates.")})["revision_id"].(int64)
	for _, role := range []dispatch.Role{dispatch.RoleWorker, dispatch.RoleVerifier, dispatch.RolePackager} {
		brief := briefFor(t, db, role, &task.ID, briefInputs{})
		if brief.part(partRequirementsRevision).fact("Earlier instructions") == "" {
			t.Fatalf("%s lost the unresolved instruction pointer.", role)
		}
		text, err := renderRunBrief(brief)
		if err != nil || strings.Contains(text, instructions) {
			t.Fatalf("The brief copied prior instructions: %v", err)
		}
	}
	id := fhClaim(t, db, 1, "revised-worker", "worker")
	value, err := TaskRevisionRead(db, id, 1, second, "earlier-instructions", 0)
	if err != nil || !strings.Contains(fmt.Sprint(value), fmt.Sprintf("--revision %d --view instructions", first)) || strings.Contains(fmt.Sprint(value), instructions) {
		t.Fatalf("The earlier instructions lack a pointer: %v %v", value, err)
	}
	if _, err := TaskRevisionRead(db, id, 1, first, "instructions", 0); err != nil {
		t.Fatal(err)
	}
	for _, request := range [][2]int64{{1, second + 1}, {2, first}} {
		if _, err := TaskRevisionRead(db, id, request[0], request[1], "instructions", 0); err == nil {
			t.Fatal("The run read another assignment or a future revision.")
		}
	}
}

func TestTaskRevisionBriefsUseBoundedPointersForLargeChanges(t *testing.T) {
	db := dvSpine(t)
	task := dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2)
	dvInsertTask(t, db, task)
	before := strings.Repeat("The old requirement needs replacement.\n", 500)
	dvExec(t, db, `UPDATE tasks SET description=? WHERE id=1`, before)
	instructions := strings.Repeat("Inspect the changed export criteria. 界\n", 800)
	result := boardRequest(t, db, BoardWriteRequest{OperationID: "large", Action: "revise", TaskID: 1,
		ExpectedVersion: boardVersion(t, db, 1), Description: boardText("The export includes the date."), Instructions: &instructions})
	revision := result["revision_id"].(int64)
	for _, role := range []dispatch.Role{dispatch.RoleWorker, dispatch.RoleVerifier, dispatch.RolePackager} {
		brief := briefFor(t, db, role, &task.ID, briefInputs{})
		part := brief.part(partRequirementsRevision)
		if part == nil || part.Text != "" || part.fact("Instructions") == "" {
			t.Fatalf("%s lost the instruction pointer: %+v", role, part)
		}
		text, err := renderRunBrief(brief)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, before) || strings.Contains(text, instructions) {
			t.Fatal("Historical text entered the brief.")
		}
	}
	var restored strings.Builder
	for offset := 0; ; {
		value, err := TaskRevisionRead(db, nil, 1, revision, "instructions", offset)
		if err != nil {
			t.Fatal(err)
		}
		page := value.(map[string]any)
		text := page["text"].(string)
		if len([]rune(text)) > revisionPageSize {
			t.Fatal("The revision read exceeded its limit.")
		}
		restored.WriteString(text)
		if page["next_offset"] == nil {
			break
		}
		offset = page["next_offset"].(int)
	}
	if restored.String() != instructions {
		t.Fatal("Pagination changed the instructions.")
	}
	value, err := TaskRevisionRead(db, nil, 1, revision, "changes", 0)
	if err != nil || !strings.Contains(fmt.Sprint(value), "-The old requirement") {
		t.Fatalf("The description diff is absent: %v %v", value, err)
	}
}
