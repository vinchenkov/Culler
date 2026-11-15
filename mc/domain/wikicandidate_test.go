package domain_test

import (
	"context"
	"database/sql"
	"testing"

	"mc/domain"
	"mc/repository"
)

// prepareWikiMerge captures the wiki commit of a waiting wiki update and
// starts its merge candidate step with the unfinished prepare row.
func prepareWikiMerge(t *testing.T, db *sql.DB, wiki, prepare string, owner int64) {
	t.Helper()
	project := wikiStr(t, db, wiki, "project")
	capture := wikiExecution("capture-"+wiki, wiki, "capture", wiki, owner, bootA)
	capture.Project = project
	startExecution(t, db, capture)
	mustExec(t, db, `UPDATE repository_runs SET captured=1 WHERE run_id=?`, wiki)
	endExecution(t, db, domain.ExecutionEnd{RunID: capture.RunID, Reason: "result", Stopped: true, Now: spineNow})
	step := wikiExecution(prepare, wiki, "prepare", "wiki/"+wiki, owner, bootA)
	step.Project = project
	startExecution(t, db, step)
	mustExec(t, db, `INSERT INTO merge_operations(run_id,wiki_update,project,operation,destination_branch)
		VALUES(?,?,?,'prepare','culler')`, prepare, wiki, project)
}

// authorizeWikiMerge records a merged candidate of the wiki commit and starts
// the merge with its unfinished merge authorization.
func authorizeWikiMerge(t *testing.T, db *sql.DB, wiki, prepare, apply string, owner int64) int64 {
	t.Helper()
	prepareWikiMerge(t, db, wiki, prepare, owner)
	var candidate int64
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		var err error
		candidate, err = domain.RecordWikiCandidate(ctx, q, prepare, wiki, repository.Candidate{SubmittedCommit: wikiB,
			DestinationBranch: "culler", DestinationCommit: wikiA, Commit: wikiC, Status: "merged"}, "refs/mc/wiki/candidates/"+prepare)
		return err
	})
	mustExec(t, db, `UPDATE merge_operations SET result='{"status":""}',finished=1 WHERE run_id=?`, prepare)
	endExecution(t, db, domain.ExecutionEnd{RunID: prepare, Reason: "result", Stopped: true, Now: spineNow})
	project := wikiStr(t, db, wiki, "project")
	merge := wikiExecution(apply, wiki, "apply", "wiki/"+wiki, owner, bootA)
	merge.Project = project
	startExecution(t, db, merge)
	mustExec(t, db, `INSERT INTO merge_operations(run_id,wiki_update,project,operation,candidate_id,destination_branch)
		VALUES(?,?,?,'apply',?,'culler')`, apply, wiki, project, candidate)
	return candidate
}

// The record of a candidate of a wiki commit is the merge_candidates row only.
func TestRecordWikiCandidate(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	waitingWiki(t, db, "ws", "wiki-1")
	owner := registerOwner(t, db)
	prepareWikiMerge(t, db, "wiki-1", "prepare-1", owner)
	merged := repository.Candidate{SubmittedCommit: wikiB, DestinationBranch: "culler", DestinationCommit: wikiA, Commit: wikiC, Status: "merged"}
	record := func(run, wiki string, c repository.Candidate, ref string) func(ctx context.Context, q domain.Q) error {
		return func(ctx context.Context, q domain.Q) error {
			_, err := domain.RecordWikiCandidate(ctx, q, run, wiki, c, ref)
			return err
		}
	}

	otherCommit, otherBranch, noCommit, unknown := merged, merged, merged, merged
	otherCommit.SubmittedCommit = wikiD
	otherBranch.DestinationBranch = "develop"
	noCommit.Commit = ""
	unknown.Status = "pending"
	wantCode(t, db, domain.CodeStaleRun, record("prepare-1", "wiki-1", otherCommit, "refs/mc/wiki/candidates/prepare-1"))
	wantCode(t, db, domain.CodeStaleRun, record("prepare-1", "wiki-1", otherBranch, "refs/mc/wiki/candidates/prepare-1"))
	wantCode(t, db, domain.CodeStaleRun, record("prepare-1", "absent", merged, "refs/mc/wiki/candidates/prepare-1"))
	wantCode(t, db, domain.CodeSHARequired, record("prepare-1", "wiki-1", merged, ""))
	wantCode(t, db, domain.CodeIllegalTransition, record("prepare-1", "wiki-1", unknown, ""))
	wantReject(t, db, record("prepare-1", "wiki-1", noCommit, "refs/mc/wiki/candidates/prepare-1"))
	if got := oneInt(t, db, `SELECT COUNT(*) FROM merge_candidates`); got != 0 {
		t.Fatalf("refused records wrote %d candidates", got)
	}

	tasks := rowsText(t, db, `SELECT * FROM tasks ORDER BY id`)
	var id int64
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		var err error
		id, err = domain.RecordWikiCandidate(ctx, q, "prepare-1", "wiki-1", merged, "refs/mc/wiki/candidates/prepare-1")
		return err
	})
	if got := oneStr(t, db, `SELECT wiki_update||'|'||COALESCE(task_id,'none')||'|'||COALESCE(version_id,'none')||'|'||destination_branch||'|'||
		destination_commit||'|'||candidate_commit||'|'||status||'|'||retained_ref||'|'||json_extract(result,'$.submitted_commit')
		FROM merge_candidates WHERE id=?`, id); got != "wiki-1|none|none|culler|"+wikiA+"|"+wikiC+"|merged|refs/mc/wiki/candidates/prepare-1|"+wikiB {
		t.Fatalf("candidate = %s", got)
	}
	if got := rowsText(t, db, `SELECT * FROM tasks ORDER BY id`); got != tasks {
		t.Fatalf("the record changed a task: task %d must keep each column", task)
	}
	if got := oneInt(t, db, `SELECT COUNT(*) FROM activity WHERE kind='task.merge_repair'`); got != 0 {
		t.Fatalf("merge repairs = %d, want 0", got)
	}
	mustExec(t, db, `UPDATE merge_operations SET result='{"status":""}',finished=1 WHERE run_id='prepare-1'`)
	endExecution(t, db, domain.ExecutionEnd{RunID: "prepare-1", Reason: "result", Stopped: true, Now: spineNow})

	// A conflict has no candidate commit and no retained reference.
	step := wikiExecution("prepare-2", "wiki-1", "prepare", "wiki/wiki-1", owner, bootA)
	startExecution(t, db, step)
	conflict := repository.Candidate{SubmittedCommit: wikiB, DestinationBranch: "culler", DestinationCommit: wikiA, Status: "conflict",
		ConflictPaths: []string{"openwiki/index.md"}}
	mustTx(t, db, record("prepare-2", "wiki-1", conflict, ""))
	if got := oneStr(t, db, `SELECT status||'|'||COALESCE(candidate_commit,'none')||'|'||COALESCE(retained_ref,'none')||'|'||json_extract(result,'$.conflict_paths[0]')
		FROM merge_candidates WHERE run_id='prepare-2'`); got != "conflict|none|none|openwiki/index.md" {
		t.Fatalf("conflict candidate = %s", got)
	}
}
