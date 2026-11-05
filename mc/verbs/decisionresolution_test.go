package verbs

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"mc/dispatch"
	"mc/domain"
	"mc/repository"
)

const decisionReportText = "The export check needs a Docker daemon. Select the required evidence."

// verifierDecisionReport builds a task whose Verifier submits an operator
// decision report on the submitted commit. The Verifier run stops and its
// repository is clean. It returns the base commit and the submitted commit.
func verifierDecisionReport(t *testing.T) (*deliveryFixture, int64, string, string) {
	t.Helper()
	f := newDeliveryFixture(t)
	base := srcGit(t, f.workspace, "rev-parse", "main")
	task := f.add("Export the report")
	commit := f.advance(task, "export.txt", "The export.\n", "worked")
	run, root, id := f.run(task, "verifier")
	value, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, NeedsOperator: true, Reason: decisionReportText})
	if err != nil {
		t.Fatal(err)
	}
	if value.(map[string]any)["blocked"] != true {
		t.Fatalf("the operator decision report did not block the task: %v", value)
	}
	f.stopped(task, run, root)
	return f, task, base, commit
}

// taskRecord returns the stored values of a task row as text.
func taskRecord(t *testing.T, f *deliveryFixture, task int64) string {
	t.Helper()
	rows, err := f.db.Query(`SELECT * FROM tasks WHERE id=?`, task)
	if err != nil {
		t.Fatal(err)
	}
	values, err := rowsToMaps(rows)
	rows.Close()
	if err != nil || len(values) != 1 {
		t.Fatalf("the task row is unavailable: %v", err)
	}
	return fmt.Sprint(values[0])
}

func reviseRequest(t *testing.T, f *deliveryFixture, task int64, operation, requirements string) BoardWriteRequest {
	t.Helper()
	return BoardWriteRequest{OperationID: operation, Action: "revise", TaskID: task, ExpectedVersion: boardVersion(t, f.db, task),
		Description: boardText(requirements), Reason: boardText("The operator moved the Docker check outside the task.")}
}

func TestVerifierDecisionReportSetsAReportCondition(t *testing.T) {
	f, task, base, _ := verifierDecisionReport(t)
	if got := dfStr(t, f.db, `SELECT status||'/'||blocked||'/'||blocked_kind||'/'||blocked_reason FROM tasks WHERE id=?`, task); got != "worked/1/report/"+decisionReportText {
		t.Fatalf("the report condition is %s", got)
	}
	record, err := TaskGet(f.db, task)
	if err != nil {
		t.Fatal(err)
	}
	row := record.(map[string]any)
	if row["blocked_kind"] != domain.BlockReport || row["base_commit"] != base {
		t.Fatalf("the task read omits the condition type or the base commit: %v / %v", row["blocked_kind"], row["base_commit"])
	}
	list, err := TaskList(f.db, false, "ws-test")
	if err != nil {
		t.Fatal(err)
	}
	rows := list.(map[string]any)["tasks"].([]map[string]any)
	if len(rows) != 1 || rows[0]["blocked_kind"] != domain.BlockReport || rows[0]["base_commit"] != base {
		t.Fatalf("the board read omits the condition type or the base commit: %v", rows)
	}
}

func TestTaskBlockSetsTheConditionTypeOfItsCaller(t *testing.T) {
	f := newDeliveryFixture(t)
	operator := f.add("Hold the operator decision")
	if _, err := TaskBlock(f.db, nil, operator, "The operator holds the task."); err != nil {
		t.Fatal(err)
	}
	reported := f.add("Report the contradiction")
	_, _, id := f.run(reported, "worker")
	if _, err := TaskBlock(f.db, id, reported, "The criteria contradict each other."); err != nil {
		t.Fatal(err)
	}
	board := f.add("Require the decision")
	boardRequest(t, f.db, BoardWriteRequest{OperationID: "require-decision", Action: "block", TaskID: board,
		ExpectedVersion: boardVersion(t, f.db, board), Reason: boardText("The operator requires a decision.")})
	for task, want := range map[int64]string{operator: domain.BlockOperator, reported: domain.BlockReport, board: domain.BlockOperator} {
		if got := dfStr(t, f.db, `SELECT blocked_kind FROM tasks WHERE id=?`, task); got != want {
			t.Fatalf("task %d has the condition type %q, want %q", task, got, want)
		}
	}
}

// The revision resolves the report in one board write. The next Worker run
// starts at the last submitted commit and can submit that commit again. The
// Verifier then assesses that commit against the original base commit.
func TestReportRevisionResolvesTheDecisionAndKeepsTheLastCommit(t *testing.T) {
	f, task, base, commit := verifierDecisionReport(t)
	const revised = "The export includes the date. The export check runs outside the agent container."
	result := boardRequest(t, f.db, reviseRequest(t, f, task, "resolve-report", revised))
	if result["revision_state"] != "applied" {
		t.Fatalf("the revision did not apply: %v", result)
	}
	if got := dfStr(t, f.db, `SELECT status||'/'||blocked||'/'||COALESCE(blocked_kind,'')||'/'||COALESCE(blocked_reason,'')||'/'||COALESCE(revision_notes,'') FROM tasks WHERE id=?`, task); got != "seeded/0///" {
		t.Fatalf("the revision did not end Decision Pending: %s", got)
	}
	if got := dfStr(t, f.db, `SELECT description||'/'||requirements_version||'/'||base_commit||'/'||input_commit FROM tasks WHERE id=?`, task); got != revised+"/2/"+base+"/"+commit {
		t.Fatalf("the revision changed the commits of the task: %s", got)
	}
	record, err := TaskGet(f.db, task)
	if err != nil {
		t.Fatal(err)
	}
	history := record.(map[string]any)["board_history"].([]map[string]any)
	if len(history) != 1 || history[0]["action"] != "revise" {
		t.Fatalf("the board history is %v", history)
	}
	previous := history[0]["previous"].(map[string]any)
	if fmt.Sprint(previous["blocked"]) != "1" || previous["blocked_kind"] != domain.BlockReport {
		t.Fatalf("the board write did not start from the report condition: %v", previous)
	}

	requireNextRole(t, f, dispatch.RoleWorker)
	projection, err := loadRepositoryProjection(context.Background(), f.db, task)
	if err != nil {
		t.Fatal(err)
	}
	if projection.InputCommit != commit {
		t.Fatalf("the next Worker run starts at %s, want the last submitted commit %s", projection.InputCommit, commit)
	}
	run, root, id := f.run(task, "worker")
	if got, err := (repository.Git{Root: filepath.Join(root, "repo")}).Commit(context.Background(), "HEAD"); err != nil || got != commit {
		t.Fatalf("the Worker repository starts at %s, %v", got, err)
	}
	if dfInt(t, f.db, `SELECT requirements_version FROM run_requirements WHERE run_id=?`, run) != 2 {
		t.Fatal("the next Worker run received the old requirements")
	}
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "worked", Commit: commit}); err != nil {
		t.Fatalf("the unchanged start commit must be accepted: %v", err)
	}
	f.capture(task, run, root)
	if got := dfStr(t, f.db, `SELECT t.status||'/'||v.input_commit||'/'||v.result_commit FROM tasks t JOIN task_versions v ON v.id=t.submitted_version WHERE t.id=?`, task); got != "worked/"+commit+"/"+commit {
		t.Fatalf("the submission of the start commit is %s", got)
	}

	run, root, id = f.run(task, "verifier")
	if got := srcGit(t, filepath.Join(root, "repo"), "rev-parse", "HEAD"); got != commit {
		t.Fatalf("the Verifier repository starts at %s", got)
	}
	if got := dfStr(t, f.db, `SELECT base_commit FROM tasks WHERE id=?`, task); got != base {
		t.Fatalf("the base commit is %s, want %s", got, base)
	}
	evidence := f.artifact(run, "The export satisfies each criterion of the revised description.")
	if _, err := VerifierVerdict(f.db, id, VerdictArgs{Task: task, Run: run, Outcome: "pass", SHA: commit, Evidence: evidence, StoreWorktree: filepath.Join(root, "repo")}); err != nil {
		t.Fatal(err)
	}
	if got := dfStr(t, f.db, `SELECT status FROM tasks WHERE id=?`, task); got != "verified" {
		t.Fatalf("the verdict left the task %s", got)
	}
}

// A checkpoint remains the start commit of the next Worker run after a
// revision, and the base commit stays the start commit of the first run.
func TestRevisionKeepsTheCheckpointAsTheNextStart(t *testing.T) {
	f := newDeliveryFixture(t)
	base := srcGit(t, f.workspace, "rev-parse", "main")
	task := f.add("Export the report")
	run, root, id := f.run(task, "worker")
	checkpoint := f.commit(root, "export.txt", "The partial export.\n")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Checkpoint: true, Commit: checkpoint}); err != nil {
		t.Fatal(err)
	}
	f.capture(task, run, root)
	if state := boardRevision(t, f.db, task, "The export includes the date.")["revision_state"]; state != "applied" {
		t.Fatalf("the revision is %v", state)
	}
	if got := dfStr(t, f.db, `SELECT v.result_commit||'/'||t.base_commit FROM tasks t JOIN task_versions v ON v.id=t.checkpoint_version WHERE t.id=?`, task); got != checkpoint+"/"+base {
		t.Fatalf("the revision changed the checkpoint or the base commit: %s", got)
	}
	projection, err := loadRepositoryProjection(context.Background(), f.db, task)
	if err != nil {
		t.Fatal(err)
	}
	if projection.InputCommit != checkpoint {
		t.Fatalf("the next Worker run starts at %s, want the checkpoint %s", projection.InputCommit, checkpoint)
	}
}

func TestRevisionWithUnchangedRequirementsChangesNoRecord(t *testing.T) {
	f, task, _, _ := verifierDecisionReport(t)
	current := dfStr(t, f.db, `SELECT description FROM tasks WHERE id=?`, task)
	before := taskRecord(t, f, task)
	counts := `SELECT (SELECT count(*) FROM requirements_revisions)||'/'||(SELECT count(*) FROM board_operations)||'/'||(SELECT count(*) FROM activity)`
	count := dfStr(t, f.db, counts)
	for n, requirements := range []string{current, "\n  " + current + "  \n"} {
		_, err := BoardWrite(f.db, nil, reviseRequest(t, f, task, fmt.Sprintf("unchanged-%d", n), requirements))
		requireBoardCode(t, err, domain.CodeInvalidRequest)
		if !strings.Contains(err.Error(), "The revised requirements must differ from the current requirements.") {
			t.Fatalf("the refusal is %v", err)
		}
	}
	if after := taskRecord(t, f, task); after != before {
		t.Fatalf("the refused revision changed the task:\n%s\n%s", before, after)
	}
	if got := dfStr(t, f.db, counts); got != count {
		t.Fatalf("the refused revision wrote a record: %s, want %s", got, count)
	}
	body := fmt.Sprintf(`{"operation_id":"no-requirements","action":"revise","task_id":%d,"expected_version":%d,"reason":"A reason"}`, task, boardVersion(t, f.db, task))
	if _, err := DecodeBoardWriteRequest([]byte(body)); err == nil {
		t.Fatal("the board accepted a revision without requirements")
	}
}

// A report on a packaged task waits for the merge candidate step. The task
// keeps Decision Pending until the revision applies.
func TestReportRevisionWaitsForARepositoryOperation(t *testing.T) {
	db := dvSpine(t)
	stTask(t, db, 1, "ws-test", dispatch.StatusPackaged)
	dvExec(t, db, `UPDATE tasks SET candidate_id=NULL WHERE id=1`)
	resp := startRequest(t, db, false)
	stWantStarted(t, db, resp, "prepare/1")
	if err := inTx(db, func(ctx context.Context, q Q) error {
		return domain.Block(ctx, q, 1, domain.BlockReport, decisionReportText)
	}); err != nil {
		t.Fatal(err)
	}
	if state := boardRevision(t, db, 1, "The revised export includes the date.")["revision_state"]; state != "pending" {
		t.Fatalf("the revision is %v during the merge candidate step", state)
	}
	pending := func() {
		t.Helper()
		if got := dfStr(t, db, `SELECT blocked||'/'||blocked_kind||'/'||blocked_reason||'/'||status FROM tasks WHERE id=1`); got != "1/report/"+decisionReportText+"/packaged" {
			t.Fatalf("the pending revision ended Decision Pending: %s", got)
		}
	}
	pending()
	record, err := TaskGet(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	revision, _ := record.(map[string]any)["pending_revision"].(map[string]any)
	if revision == nil || revision["wait_reason"] != "mc saved your revision request. A repository operation will finish before mc applies your changes." {
		t.Fatalf("the pending revision is %v", revision)
	}
	stWantStarted(t, db, startRequest(t, db, false))
	pending()
	stFail(t, db, resp.Started[0], false)
	startRequest(t, db, false)
	if got := dfStr(t, db, `SELECT r.state||'/'||t.blocked||'/'||COALESCE(t.blocked_kind,'')||'/'||t.status FROM tasks t JOIN requirements_revisions r ON r.task_id=t.id WHERE t.id=1`); got != "applied/0//seeded" {
		t.Fatalf("the applied revision left %s", got)
	}
}

// A report from an active run waits for the stop of the run.
func TestReportRevisionWaitsForTheActiveRun(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Export the report")
	commit := f.advance(task, "export.txt", "The export.\n", "worked")
	run, root, id := f.run(task, "verifier")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, NeedsOperator: true, Reason: decisionReportText}); err != nil {
		t.Fatal(err)
	}
	if state := boardRequest(t, f.db, reviseRequest(t, f, task, "resolve-during-run", "The revised export includes the date."))["revision_state"]; state != "pending" {
		t.Fatalf("the revision is %v during the run", state)
	}
	f.transaction(domain.ApplyPendingTaskRevisions)
	if got := dfStr(t, f.db, `SELECT blocked||'/'||blocked_kind||'/'||status FROM tasks WHERE id=?`, task); got != "1/report/worked" {
		t.Fatalf("the pending revision ended Decision Pending: %s", got)
	}
	f.stopped(task, run, root)
	f.transaction(domain.ApplyPendingTaskRevisions)
	if got := dfStr(t, f.db, `SELECT blocked||'/'||COALESCE(blocked_kind,'')||'/'||status||'/'||input_commit FROM tasks WHERE id=?`, task); got != "0//seeded/"+commit {
		t.Fatalf("the applied revision left %s", got)
	}
}

func TestAppliedRevisionKeepsEachOtherCondition(t *testing.T) {
	for _, kind := range []string{domain.BlockCustomization, domain.BlockConfinement, domain.BlockCheckout, domain.BlockOperator} {
		t.Run(kind, func(t *testing.T) {
			db := dvSpine(t)
			dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
			reason := "The " + kind + " condition requires the operator."
			if err := inTx(db, func(ctx context.Context, q Q) error { return domain.Block(ctx, q, 1, kind, reason) }); err != nil {
				t.Fatal(err)
			}
			if state := boardRevision(t, db, 1, "The revised requirements.")["revision_state"]; state != "applied" {
				t.Fatalf("the revision is %v", state)
			}
			if got := dfStr(t, db, `SELECT description||'/'||blocked||'/'||blocked_kind||'/'||blocked_reason FROM tasks WHERE id=1`); got != "The revised requirements./1/"+kind+"/"+reason {
				t.Fatalf("the revision changed the %s condition: %s", kind, got)
			}
		})
	}
}

func TestAppliedRevisionEndsACorrectionAllowanceLimit(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Correct the required result")
	for attempt := 0; attempt <= 3; attempt++ {
		f.advance(task, "answer.txt", "a rejected answer\n", "worked")
		submitVerdict(t, f, task, "correct")
	}
	if got := dfStr(t, f.db, `SELECT blocked||'/'||blocked_kind||'/'||allowance_condition FROM tasks WHERE id=?`, task); got != "1/allowance/verifier" {
		t.Fatalf("the allowance limit is %s", got)
	}
	if state := boardRevision(t, f.db, task, "The answer states the required unit.")["revision_state"]; state != "applied" {
		t.Fatalf("the revision is %v", state)
	}
	if got := dfStr(t, f.db, `SELECT blocked||'/'||COALESCE(blocked_kind,'')||'/'||COALESCE(allowance_condition,'')||'/'||correction_count||'/'||status FROM tasks WHERE id=?`, task); got != "0///0/seeded" {
		t.Fatalf("the revision kept the allowance limit: %s", got)
	}
}

func TestResolveDecisionWithAReasonRefusesAReport(t *testing.T) {
	f, task, _, _ := verifierDecisionReport(t)
	before := taskRecord(t, f, task)
	const refusal = "Resolve this operator decision report with a requirements revision, or cancel the task."
	_, err := TaskUnblock(f.db, nil, task, "The operator decided.")
	requireBoardCode(t, err, domain.CodeIllegalTransition)
	if !strings.Contains(err.Error(), refusal) {
		t.Fatalf("the refusal is %v", err)
	}
	_, err = BoardWrite(f.db, nil, BoardWriteRequest{OperationID: "resolve-report-with-reason", Action: "unblock", TaskID: task,
		ExpectedVersion: boardVersion(t, f.db, task), Reason: boardText("The operator decided.")})
	requireBoardCode(t, err, domain.CodeIllegalTransition)
	if after := taskRecord(t, f, task); after != before {
		t.Fatalf("the refused resolution changed the task:\n%s\n%s", before, after)
	}
	if dfInt(t, f.db, `SELECT count(*) FROM board_operations`) != 0 || dfInt(t, f.db, `SELECT count(*) FROM activity WHERE kind='task.unblock_reason'`) != 0 {
		t.Fatal("the refused resolution recorded an accepted action")
	}
}

func TestResolveDecisionWithAReasonEndsAnOperatorBlock(t *testing.T) {
	for _, board := range []bool{false, true} {
		t.Run(fmt.Sprint(board), func(t *testing.T) {
			f := newDeliveryFixture(t)
			task := f.add("Hold the task")
			boardRequest(t, f.db, BoardWriteRequest{OperationID: "require-decision", Action: "block", TaskID: task,
				ExpectedVersion: boardVersion(t, f.db, task), Reason: boardText("The operator requires a decision.")})
			description := dfStr(t, f.db, `SELECT description FROM tasks WHERE id=?`, task)
			const reason = "The operator selected the current requirements."
			if board {
				boardRequest(t, f.db, BoardWriteRequest{OperationID: "resolve-decision", Action: "unblock", TaskID: task,
					ExpectedVersion: boardVersion(t, f.db, task), Reason: boardText(reason)})
			} else if _, err := TaskUnblock(f.db, nil, task, reason); err != nil {
				t.Fatal(err)
			}
			if got := dfStr(t, f.db, `SELECT blocked||'/'||COALESCE(blocked_kind,'')||'/'||COALESCE(blocked_reason,'')||'/'||description FROM tasks WHERE id=?`, task); got != "0///"+description {
				t.Fatalf("the resolution left %s", got)
			}
			if dfInt(t, f.db, `SELECT count(*) FROM activity WHERE kind='task.unblock_reason' AND detail=?`, reason) != 1 {
				t.Fatal("the resolution lost its reason")
			}
		})
	}
}

func TestResolveDecisionWithAReasonEndsTheOtherConditions(t *testing.T) {
	for _, kind := range []string{domain.BlockCustomization, domain.BlockConfinement, domain.BlockCheckout} {
		t.Run(kind, func(t *testing.T) {
			db := dvSpine(t)
			dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
			if err := inTx(db, func(ctx context.Context, q Q) error {
				return domain.Block(ctx, q, 1, kind, "The "+kind+" condition requires the operator.")
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := TaskUnblock(db, nil, 1, "The operator repaired the condition."); err != nil {
				t.Fatal(err)
			}
			if got := dfStr(t, db, `SELECT blocked||'/'||COALESCE(blocked_kind,'') FROM tasks WHERE id=1`); got != "0/" {
				t.Fatalf("the resolution left %s", got)
			}
		})
	}
}

func TestHomieRevisionResolvesAReport(t *testing.T) {
	f, task, base, commit := verifierDecisionReport(t)
	const session = "h-aaaaaaaaaaaaaaaa"
	dvExec(t, f.db, `INSERT INTO homie_sessions (id, container_name, verb_allowlist, session_path, binding)
		VALUES (?, 'mc-homie-'||?, '["board.write"]', 'sessions/h-a', 'claude')`, session, session)
	homie := &RunIdentity{Tier: "homie", RunID: session, VerbAllowlist: []string{"board.write"}}
	_, err := BoardWrite(f.db, homie, BoardWriteRequest{OperationID: "homie-reason", Action: "unblock", TaskID: task,
		ExpectedVersion: boardVersion(t, f.db, task), Reason: boardText("The operator decided.")})
	requireBoardCode(t, err, domain.CodeIllegalTransition)
	value, err := BoardWrite(f.db, homie, reviseRequest(t, f, task, "homie-revision", "The export includes the date. The export check runs outside the agent container."))
	if err != nil {
		t.Fatal(err)
	}
	if value.(map[string]any)["revision_state"] != "applied" {
		t.Fatalf("the Homie revision did not apply: %v", value)
	}
	if got := dfStr(t, f.db, `SELECT blocked||'/'||COALESCE(blocked_kind,'')||'/'||status||'/'||base_commit||'/'||input_commit FROM tasks WHERE id=?`, task); got != "0//seeded/"+base+"/"+commit {
		t.Fatalf("the Homie revision left %s", got)
	}
	if got := dfStr(t, f.db, `SELECT actor FROM requirements_revisions WHERE task_id=?`, task); got != "homie:"+session {
		t.Fatalf("the revision actor is %s", got)
	}
}

// An operator block would replace the report with a condition that a reason
// resolves. mc refuses it, so the report keeps its two resolutions.
func TestOperatorBlockKeepsAnOperatorDecisionReport(t *testing.T) {
	f, task, _, _ := verifierDecisionReport(t)
	before := taskRecord(t, f, task)
	const refusal = "Resolve this operator decision report with a requirements revision, or cancel the task."
	_, err := TaskBlock(f.db, nil, task, "The operator holds the task.")
	requireBoardCode(t, err, domain.CodeIllegalTransition)
	if !strings.Contains(err.Error(), refusal) {
		t.Fatalf("the refusal is %v", err)
	}
	_, err = BoardWrite(f.db, nil, BoardWriteRequest{OperationID: "require-decision", Action: "block", TaskID: task,
		ExpectedVersion: boardVersion(t, f.db, task), Reason: boardText("The operator holds the task.")})
	requireBoardCode(t, err, domain.CodeIllegalTransition)
	if after := taskRecord(t, f, task); after != before {
		t.Fatalf("the refused block changed the task:\n%s\n%s", before, after)
	}
	if _, err := TaskUnblock(f.db, nil, task, "The operator decided."); err == nil {
		t.Fatal("a reason resolved the operator decision report")
	}
}

// A merge repair Worker reports two contradictory changes. The revision that
// records the decision keeps the conflict, so the next Worker run repairs the
// merge with the revised description.
func TestRevisionKeepsAnOpenMergeRepair(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Preserve both contributions")
	submitted := f.pass(task, "answer.txt", "worker 1\n")
	f.commitMain("answer.txt", "main 1\n")
	if candidate := f.merge(task, "prepare"); candidate.Status != "conflict" {
		t.Fatal(candidate)
	}
	conflict := dfStr(t, f.db, `SELECT revision_notes FROM tasks WHERE id=?`, task)
	run, root, id := f.run(task, "worker")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, NeedsOperator: true, Reason: "The two changes of answer.txt contradict each other."}); err != nil {
		t.Fatal(err)
	}
	f.stopped(task, run, root)
	if state := boardRevision(t, f.db, task, "The answer holds the main text, then the worker text.")["revision_state"]; state != "applied" {
		t.Fatalf("the revision is %v", state)
	}
	if got := dfStr(t, f.db, `SELECT blocked||'/'||status||'/'||returned_kind||'/'||correction_open||'/'||input_commit FROM tasks WHERE id=?`, task); got != "0/seeded/merge/1/"+submitted {
		t.Fatalf("the revision changed the merge repair: %s", got)
	}
	if got := dfStr(t, f.db, `SELECT revision_notes FROM tasks WHERE id=?`, task); got != conflict {
		t.Fatalf("the revision lost the conflict: %s", got)
	}
	brief := briefFor(t, f.db, dispatch.RoleWorker, briefSubject(dispatch.RoleWorker, task), briefInputs{})
	if brief.part(partMergeCandidate) == nil || brief.part(partRequirementsRevision) == nil || brief.part(partRevision) != nil {
		var headings []string
		for _, part := range brief.Parts {
			headings = append(headings, part.Heading)
		}
		t.Fatalf("the Worker brief after the revision holds the parts %v", headings)
	}
	f.pass(task, "answer.txt", "main 1\nworker 1\n")
	if candidate := f.merge(task, "prepare"); candidate.Status != "merged" {
		t.Fatalf("the repaired version did not merge: %v", candidate)
	}
}

// The operator allowance explanation belongs to the requirements version of
// its grant. A revision removes it from later briefs.
func TestRevisionEndsTheOperatorAllowanceExplanation(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Correct the required result")
	for attempt := 0; attempt <= 3; attempt++ {
		f.advance(task, "answer.txt", "a rejected answer\n", "worked")
		submitVerdict(t, f, task, "correct")
	}
	boardRequest(t, f.db, BoardWriteRequest{OperationID: "grant", Action: "allow-corrections", TaskID: task,
		ExpectedVersion: boardVersion(t, f.db, task), Reason: boardText("The evidence supports another correction.")})
	const explanation = "Operator allowance explanation"
	for _, role := range []dispatch.Role{dispatch.RoleWorker, dispatch.RoleVerifier} {
		if briefFor(t, f.db, role, briefSubject(role, task), briefInputs{}).part(explanation) == nil {
			t.Fatalf("the %s brief after the grant omits the explanation", role)
		}
	}
	if state := boardRevision(t, f.db, task, "The answer states the required unit.")["revision_state"]; state != "applied" {
		t.Fatalf("the revision is %v", state)
	}
	for _, role := range []dispatch.Role{dispatch.RoleWorker, dispatch.RoleVerifier} {
		if briefFor(t, f.db, role, briefSubject(role, task), briefInputs{}).part(explanation) != nil {
			t.Fatalf("the %s brief after the revision holds the explanation of the earlier requirements", role)
		}
	}
}
