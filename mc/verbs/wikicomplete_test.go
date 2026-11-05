package verbs

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mc/dispatch"
	"mc/domain"
	"mc/wiki"
)

// wcRun is one active wiki update with the isolated repository of its Scribe
// run. The start commit holds a complete wiki.
type wcRun struct {
	t      *testing.T
	db     *sql.DB
	id     *RunIdentity
	run    string
	source string
	start  string
}

// wcScribe is the identity of the Scribe of the run.
func wcScribe(run string) *RunIdentity {
	return &RunIdentity{Tier: "pipeline", Role: "scribe", RunID: run}
}

// newWCRun starts a wiki update of the Project. The repository of the run is
// at the start commit, and the terminal action of this test process reads it.
func newWCRun(t *testing.T, db *sql.DB, project string) *wcRun {
	t.Helper()
	return newWCRunAt(t, db, project, func(*wcRun) {})
}

// newWCRunAt is newWCRun with one more step: arrange changes the index of the start commit.
func newWCRunAt(t *testing.T, db *sql.DB, project string, arrange func(f *wcRun)) *wcRun {
	t.Helper()
	root := t.TempDir()
	f := &wcRun{t: t, db: db, run: wrNewRun(t), source: filepath.Join(root, "source")}
	if err := os.Mkdir(f.source, 0o700); err != nil {
		t.Fatal(err)
	}
	srcGit(t, f.source, "init", "-q")
	srcGit(t, f.source, "config", "commit.gpgsign", "false")
	f.write("source.txt", "The source of the Project.\n")
	f.write("openwiki/index.md", "# Project wiki\n")
	f.write("openwiki/.last-update.json", "{\"status\":\"complete\"}\n")
	srcGit(t, f.source, "add", "-A")
	arrange(f)
	srcGit(t, f.source, "commit", "-q", "-m", "Accept the source and the wiki")
	f.start = srcGit(t, f.source, "rev-parse", "HEAD")
	t.Setenv("MC_TASK_STORE_ROOT", root)
	wrFresh(t, db, project, f.run, f.start)
	f.id = wcScribe(f.run)
	return f
}

func (f *wcRun) write(name, body string) {
	f.t.Helper()
	path := filepath.Join(f.source, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		f.t.Fatal(err)
	}
	writeFile(f.t, path, body)
}

// commit commits each file of the repository and returns the new HEAD commit.
func (f *wcRun) commit(message string) string {
	f.t.Helper()
	srcGit(f.t, f.source, "add", "-A")
	srcGit(f.t, f.source, "commit", "-q", "-m", message)
	return srcGit(f.t, f.source, "rev-parse", "HEAD")
}

// receipt writes the completion receipt of a complete OpenWiki run whose
// result is the wiki of the files in the repository. It returns the path.
func (f *wcRun) receipt(status string) string {
	f.t.Helper()
	files := map[string][]byte{}
	if err := filepath.WalkDir(filepath.Join(f.source, "openwiki"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		name, err := filepath.Rel(f.source, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(name)], err = os.ReadFile(path)
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
	body, err := json.Marshal(wiki.Completion{Format: 1, Status: status, InputCommit: f.start, SourceCommit: f.start,
		WikiDigest: wiki.Digest(files), OpenWikiVersion: wiki.Version, OpenWikiRevision: wiki.Revision})
	if err != nil {
		f.t.Fatal(err)
	}
	path := filepath.Join(f.t.TempDir(), "openwiki-completion.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		f.t.Fatal(err)
	}
	return path
}

// complete submits the success form of the terminal action.
func (f *wcRun) complete(commit, receipt string) (any, error) {
	return Complete(f.db, f.id, CompleteArgs{Run: f.run, Commit: commit, WikiEvidence: receipt})
}

// row reads the run and its wiki update as one line.
func (f *wcRun) row() string {
	f.t.Helper()
	return wrDump(f.t, f.db, `SELECT r.outcome, r.end_reason, r.ended_at IS NOT NULL AS run_ended, w.ended_at IS NOT NULL AS ended,
		w.result, w.cause, w.wiki_commit, w.counted, w.stopped_at IS NOT NULL AS stopped
		FROM wiki_updates w JOIN runs r ON r.id=w.run_id WHERE w.run_id=?`, f.run)
}

// wcReaps returns the reason of each reap effect of the tick for the run.
func wcReaps(t *testing.T, raw any, run string) []string {
	t.Helper()
	var reasons []string
	for _, action := range dvTickActions(t, raw) {
		if action["action"] == "reap" && action["run_id"] == run {
			reasons = append(reasons, action["reason"].(string))
		}
	}
	return reasons
}

// wcRefused reports whether the error is a refusal with the code and the text.
func wcRefused(err error, code, text string) bool {
	var refused *DomainError
	return errors.As(err, &refused) && refused.Code == code && refused.Msg == text
}

func TestWikiCompleteWithNoChangeSucceeds(t *testing.T) {
	db := dvSpine(t)
	f := newWCRun(t, db, "ws-test")
	result, err := f.complete(f.start, f.receipt("noop"))
	if err != nil {
		t.Fatalf("terminal action with no wiki change: %v", err)
	}
	if want := map[string]any{"run_id": f.run, "wiki_commit": f.start, "wiki_change": false}; !reflect.DeepEqual(result, want) {
		t.Fatalf("result = %v, want %v", result, want)
	}
	if got, want := f.row(), "outcome=completed|end_reason=<nil>|run_ended=1|ended=1|result=succeeded|cause=<nil>|wiki_commit="+f.start+"|counted=1|stopped=0|\n"; got != want {
		t.Fatalf("the run and its wiki update = %swant %s", got, want)
	}
	if got := wrText(t, db, `SELECT detail FROM activity WHERE kind='wiki.update_ended' AND subject=?`, f.run); got != "The wiki update succeeded. The Project wiki needed no change." {
		t.Fatalf("end record = %q", got)
	}
	if n := dfInt(t, db, `SELECT (SELECT COUNT(*) FROM merge_operations) + (SELECT COUNT(*) FROM repository_executions)`); n != 0 {
		t.Fatalf("a wiki update with no wiki change wrote %d merge records, want none", n)
	}

	// The wiki is current at the start commit. The run still owes its stop confirmation.
	observe := wrSame(map[string]string{"ws-test": f.start})
	if reaps := wcReaps(t, wrTick(t, db, observe), f.run); !reflect.DeepEqual(reaps, []string{"reported-outcome"}) {
		t.Fatalf("reap effects of the next tick = %v, want one reported-outcome", reaps)
	}
	if stopped := wrText(t, db, `SELECT stopped_at FROM wiki_updates WHERE run_id=?`, f.run); stopped != "" {
		t.Fatalf("the tick set the stop time %s, want the stop confirmation of the resident", stopped)
	}
	wrStopped(t, db, f.run)
	if stopped := wrText(t, db, `SELECT stopped_at FROM wiki_updates WHERE run_id=?`, f.run); stopped == "" {
		t.Fatal("the stop confirmation set no stop time")
	}
	wrWantIdle(t, wrRound(t, db, "ws-test", f.start, f.start), "ws-test", "wiki-current")
	if reaps := wcReaps(t, wrTick(t, db, observe), f.run); len(reaps) != 0 {
		t.Fatalf("reap effects after the stop confirmation = %v, want none", reaps)
	}
}

func TestWikiCompleteHandsTheWikiCommitToTheMerge(t *testing.T) {
	db := dvSpine(t)
	f := newWCRun(t, db, "ws-test")
	wmReady(t, db, f.run, f.start)
	f.write("openwiki/index.md", "# Project wiki\n\n[Request flow](flow.md)\n")
	f.write("openwiki/flow.md", "---\ntitle: Request flow\ntype: Component\n---\nA generated explanation.\n")
	commit := f.commit("Update the Project wiki")
	result, err := f.complete(commit, f.receipt("complete"))
	if err != nil {
		t.Fatalf("terminal action with a wiki commit: %v", err)
	}
	if want := map[string]any{"run_id": f.run, "wiki_commit": commit, "wiki_change": true}; !reflect.DeepEqual(result, want) {
		t.Fatalf("result = %v, want %v", result, want)
	}
	// The run has its end. The wiki update stays active with the wiki commit until its merge.
	waits := "outcome=completed|end_reason=<nil>|run_ended=1|ended=0|result=<nil>|cause=<nil>|wiki_commit=" + commit + "|counted=1|stopped=0|\n"
	if got := f.row(); got != waits {
		t.Fatalf("the run and its wiki update = %swant %s", got, waits)
	}
	if _, ended := wrRecords(t, db, f.run, "ws-test"); ended != 0 {
		t.Fatalf("%d end records of a wiki update that waits for its merge, want none", ended)
	}
	if _, err := f.complete(commit, f.receipt("complete")); !rnStale(err) {
		t.Fatalf("second terminal action = %v, want a stale run refusal", err)
	}
	// The round ends no wiki update that waits for its merge.
	wrWantReap(t, wrRound(t, db, "ws-test", f.start, f.start), "ws-test", f.run, "reported-outcome")
	if got := f.row(); got != waits {
		t.Fatalf("the round changed the wiki update that waits for its merge: %s", got)
	}
}

func TestWikiCommitOutsideOpenwikiFailsTheWikiUpdate(t *testing.T) {
	db := dvSpine(t)
	workspace := wrText(t, db, `SELECT workspace_root FROM sandbox_profiles WHERE id='default'`)
	main := srcGit(t, workspace, "rev-parse", "refs/heads/main")
	f := newWCRun(t, db, "ws-test")
	f.write("source.txt", "A source change of the Scribe.\n")
	commit := f.commit("Change a source file")
	const check = "The wiki commit changes a path outside openwiki/: source.txt."
	result, err := f.complete(commit, f.receipt("complete"))
	if result != nil || !wcRefused(err, domain.CodeWikiCheckFailed, check) {
		t.Fatalf("terminal action = %v, %v; want the code %s with the failed check", result, err, domain.CodeWikiCheckFailed)
	}
	if got, want := f.row(), "outcome=completed|end_reason=<nil>|run_ended=1|ended=1|result=failed|cause="+check+"|wiki_commit="+commit+"|counted=1|stopped=0|\n"; got != want {
		t.Fatalf("the run and its wiki update = %swant %s", got, want)
	}
	if got := wrText(t, db, `SELECT detail FROM activity WHERE kind='wiki.update_ended' AND subject=?`, f.run); got != "The wiki update failed. "+check {
		t.Fatalf("end record = %q", got)
	}
	// No merge step can start: the wiki update has its end, and a start request starts no operation.
	if n := dfInt(t, db, `SELECT COUNT(*) FROM wiki_updates WHERE ended_at IS NULL`); n != 0 {
		t.Fatalf("%d wiki updates wait for a merge after a failed check, want none", n)
	}
	if resp := stStart(t, db, testOwner(t, db)); len(resp.Executions) != 0 || len(resp.Started) != 0 || len(resp.Failed) != 0 {
		t.Fatalf("start request after the failed check = %+v, want no operation", resp)
	}
	if n := dfInt(t, db, `SELECT (SELECT COUNT(*) FROM merge_operations) + (SELECT COUNT(*) FROM repository_executions)`); n != 0 {
		t.Fatalf("a failed check wrote %d merge records, want none", n)
	}
	if got := srcGit(t, workspace, "rev-parse", "refs/heads/main"); got != main {
		t.Fatalf("the branch main of the Project moved from %s to %s", main, got)
	}
	// The failed check ended the run. A corrected commit gets no second check.
	srcGit(t, f.source, "reset", "-q", "--hard", f.start)
	if _, err := f.complete(f.start, f.receipt("noop")); !rnStale(err) {
		t.Fatalf("terminal action after the failed check = %v, want a stale run refusal", err)
	}
}

// The checks also apply to a submission with no wiki change.
func TestWikiCheckAppliesToTheStartCommit(t *testing.T) {
	db := dvSpine(t)
	f := newWCRun(t, db, "ws-test")
	const check = "The OpenWiki completion receipt names another start commit, another OpenWiki version, or an incomplete run."
	result, err := f.complete(f.start, f.receipt("incomplete"))
	if result != nil || !wcRefused(err, domain.CodeWikiCheckFailed, check) {
		t.Fatalf("terminal action = %v, %v; want the code %s with the failed check", result, err, domain.CodeWikiCheckFailed)
	}
	if got, want := f.row(), "outcome=completed|end_reason=<nil>|run_ended=1|ended=1|result=failed|cause="+check+"|wiki_commit="+f.start+"|counted=1|stopped=0|\n"; got != want {
		t.Fatalf("the run and its wiki update = %swant %s", got, want)
	}
	if _, ended := wrRecords(t, db, f.run, "ws-test"); ended != 1 {
		t.Fatalf("%d end records, want one", ended)
	}
}

// The start commit holds an entry that no wiki commit can correct. The first
// submission ends the wiki update, and the cause names the commit and the entry.
func TestStartCommitEntryThatIsNoRegularFileFailsTheWikiUpdate(t *testing.T) {
	db := dvSpine(t)
	f := newWCRunAt(t, db, "ws-test", func(f *wcRun) {
		// A submodule entry is an empty folder in the work tree of the run.
		if err := os.Mkdir(filepath.Join(f.source, "openwiki", "vendor"), 0o700); err != nil {
			t.Fatal(err)
		}
		srcGit(t, f.source, "update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat("1", 40)+",openwiki/vendor")
	})
	f.write("openwiki/index.md", "# Project wiki\n\n[Request flow](flow.md)\n")
	f.write("openwiki/flow.md", "---\ntitle: Request flow\ntype: Component\n---\nA generated explanation.\n")
	commit := f.commit("Update the Project wiki")
	const check = "The start commit holds a submodule under openwiki/: openwiki/vendor."
	result, err := f.complete(commit, f.receipt("complete"))
	if result != nil || !wcRefused(err, domain.CodeWikiCheckFailed, check) {
		t.Fatalf("terminal action = %v, %v; want the code %s with the failed check", result, err, domain.CodeWikiCheckFailed)
	}
	if got, want := f.row(), "outcome=completed|end_reason=<nil>|run_ended=1|ended=1|result=failed|cause="+check+"|wiki_commit="+commit+"|counted=1|stopped=0|\n"; got != want {
		t.Fatalf("the run and its wiki update = %swant %s", got, want)
	}
	if got := wrText(t, db, `SELECT detail FROM activity WHERE kind='wiki.update_ended' AND subject=?`, f.run); got != "The wiki update failed. "+check {
		t.Fatalf("end record = %q", got)
	}
	if _, err := f.complete(commit, f.receipt("complete")); !rnStale(err) {
		t.Fatalf("second terminal action = %v, want a stale run refusal", err)
	}
}

// wcFailingGit puts a git program first on PATH. The program fails for the
// first call of the Git command and runs the real program for each other call.
func wcFailingGit(t *testing.T, command string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()
	mark := filepath.Join(folder, "failed")
	body := "#!/bin/sh\nfor argument in \"$@\"; do\n  if [ \"$argument\" = " + command + " ] && [ ! -e '" + mark + "' ]; then\n" +
		"    : > '" + mark + "'\n    echo 'fatal: the process failed' >&2\n    exit 128\n  fi\ndone\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(folder, "git"), []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", folder+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A Git process that fails in the ancestor test gives no check result. The
// refusal changes no state, and the same submission then succeeds.
func TestFailedGitProcessRefusesTheWikiTerminal(t *testing.T) {
	db := dvSpine(t)
	f := newWCRun(t, db, "ws-test")
	f.write("openwiki/index.md", "# Project wiki\n\n[Request flow](flow.md)\n")
	f.write("openwiki/flow.md", "---\ntitle: Request flow\ntype: Component\n---\nA generated explanation.\n")
	commit := f.commit("Update the Project wiki")
	receipt := f.receipt("complete")
	before := f.row()

	wcFailingGit(t, "merge-base")
	result, err := f.complete(commit, receipt)
	var refused *DomainError
	if result != nil || !errors.As(err, &refused) || refused.Code != "" || !strings.Contains(refused.Msg, "git merge-base") {
		t.Fatalf("terminal action with a failed Git process = %v, %v; want a refusal with the Git error", result, err)
	}
	if got := f.row(); got != before {
		t.Fatalf("the refusal changed the run or its wiki update: %s", got)
	}
	if _, ended := wrRecords(t, db, f.run, "ws-test"); ended != 0 {
		t.Fatalf("%d end records after the refusal, want none", ended)
	}
	if _, err := f.complete(commit, receipt); err != nil {
		t.Fatalf("second terminal action: %v", err)
	}
	if got := wrDump(t, db, `SELECT r.outcome, w.wiki_commit FROM wiki_updates w JOIN runs r ON r.id=w.run_id WHERE w.run_id=?`, f.run); got != "outcome=completed|wiki_commit="+commit+"|\n" {
		t.Fatalf("the run and its wiki update after the accepted terminal action = %s", got)
	}
}

func TestWikiCompleteRefusalsChangeNoState(t *testing.T) {
	db := dvSpine(t)
	f := newWCRun(t, db, "ws-test")
	receipt := f.receipt("noop")
	state := func() string {
		t.Helper()
		return wrDump(t, db, `SELECT * FROM runs ORDER BY id`) + wrDump(t, db, `SELECT * FROM wiki_updates ORDER BY id`) +
			wrDump(t, db, `SELECT * FROM activity ORDER BY id`) + wrDump(t, db, `SELECT * FROM diagnostic_events ORDER BY id`)
	}
	before := state()
	same := func(step string) {
		t.Helper()
		if got := state(); got != before {
			t.Fatalf("%s changed a row:\n%s\nbefore:\n%s", step, got, before)
		}
	}

	if _, err := Complete(db, f.id, CompleteArgs{Run: "0123456789abcdef", Commit: f.start, WikiEvidence: receipt}); !rnStale(err) {
		t.Fatalf("terminal action with another run identifier = %v, want a stale run refusal", err)
	}
	same("another run identifier")

	usage := map[string]CompleteArgs{
		"a task argument":                          {Task: 1, Commit: f.start, WikiEvidence: receipt},
		"--status":                                 {Status: "worked", Commit: f.start, WikiEvidence: receipt},
		"--checkpoint":                             {Checkpoint: true, Commit: f.start, WikiEvidence: receipt},
		"--outputs":                                {Outputs: "/mc/artifacts/report.md", Commit: f.start, WikiEvidence: receipt},
		"--thesis":                                 {Thesis: "The wiki is complete.", Commit: f.start, WikiEvidence: receipt},
		"--needs-operator":                         {NeedsOperator: true, Reason: "The operator must decide."},
		"--infra with --commit":                    {Infra: true, Reason: "OpenWiki failed.", Commit: f.start},
		"--infra with --wiki-evidence":             {Infra: true, Reason: "OpenWiki failed.", WikiEvidence: receipt},
		"--infra with no --reason":                 {Infra: true},
		"the success form with no --commit":        {WikiEvidence: receipt},
		"the success form with no --wiki-evidence": {Commit: f.start},
		"no terminal":                              {},
	}
	for name, a := range usage {
		a.Run = f.run
		var refused *UsageError
		if _, err := Complete(db, f.id, a); !errors.As(err, &refused) || refused.Msg != wikiCompleteUsage {
			t.Fatalf("%s = %v, want the usage error of the Scribe terminal", name, err)
		}
		same(name)
	}

	// Each refusal below names a property that the Scribe can correct.
	if _, err := f.complete(wrCommitA, receipt); !wcRefused(err, "", "The --commit value differs from HEAD.") {
		t.Fatalf("a commit that differs from HEAD = %v, want the refusal", err)
	}
	same("a commit that differs from HEAD")
	f.write("openwiki/draft.md", "An uncommitted page.\n")
	if _, err := f.complete(f.start, receipt); !wcRefused(err, "", "The repository holds an uncommitted file.") {
		t.Fatalf("an uncommitted file = %v, want the refusal", err)
	}
	same("an uncommitted file")
	if err := os.Remove(filepath.Join(f.source, "openwiki", "draft.md")); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"an absent receipt": filepath.Join(t.TempDir(), "absent.json"), "a folder as the receipt": t.TempDir()} {
		if _, err := f.complete(f.start, path); !wcRefused(err, "", "The OpenWiki completion receipt is not a readable regular file.") {
			t.Fatalf("%s = %v, want the refusal", name, err)
		}
		same(name)
	}

	// The receipt belongs to the Scribe terminal. Each other role gets a usage error.
	for _, other := range []*RunIdentity{nil, {Tier: "pipeline", Role: string(dispatch.RoleWorker), RunID: f.run}} {
		var refused *UsageError
		if _, err := Complete(db, other, CompleteArgs{Task: 1, Run: f.run, Status: "worked", Commit: f.start, WikiEvidence: receipt}); !errors.As(err, &refused) ||
			refused.Msg != "mc complete accepts --wiki-evidence from the Scribe only" {
			t.Fatalf("the receipt flag of %+v = %v, want the usage error", other, err)
		}
		same("the receipt flag of another role")
	}

	// The same run can still submit its terminal action.
	if _, err := f.complete(f.start, receipt); err != nil {
		t.Fatalf("terminal action after the refusals: %v", err)
	}
	if got := wrDump(t, db, `SELECT r.outcome, w.result FROM wiki_updates w JOIN runs r ON r.id=w.run_id WHERE w.run_id=?`, f.run); got != "outcome=completed|result=succeeded|\n" {
		t.Fatalf("the run and its wiki update after the accepted terminal action = %s", got)
	}
}

func TestScribeInfraTerminalFailsTheWikiUpdate(t *testing.T) {
	db := dvSpine(t)
	run := wrWantSpawn(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test")
	const reason = "The OpenWiki command failed."
	result, err := Complete(db, wcScribe(run), CompleteArgs{Run: run, Infra: true, Reason: reason})
	if err != nil {
		t.Fatalf("failure terminal of the Scribe: %v", err)
	}
	if want := map[string]any{"run_id": run, "wiki_update": true, "charged": false, "project_id": "ws-test"}; !reflect.DeepEqual(result, want) {
		t.Fatalf("result = %v, want %v", result, want)
	}
	if got, want := wrRunEnd(t, db, run), wrEndLine("completed", "", "The Scribe reported a failure: "+reason, true, false); got != want {
		t.Fatalf("the run and its wiki update = %swant %s", got, want)
	}
	if got := wrDump(t, db, `SELECT source, condition FROM diagnostic_events WHERE run_id=?`, run); got != "source=agent|condition="+reason+"|\n" {
		t.Fatalf("diagnostics of the run = %s, want one record with the reason", got)
	}
	if n := dfInt(t, db, `SELECT COUNT(*) FROM infrastructure_failures`); n != 0 {
		t.Fatalf("the failure terminal wrote %d infrastructure failure records, want none", n)
	}
	observe := wrSame(map[string]string{"ws-test": wrCommitA})
	if reaps := wcReaps(t, wrTick(t, db, observe), run); !reflect.DeepEqual(reaps, []string{"reported-outcome"}) {
		t.Fatalf("reap effects of the next tick = %v, want one reported-outcome", reaps)
	}
	if _, err := Complete(db, wcScribe(run), CompleteArgs{Run: run, Infra: true, Reason: reason}); err != nil {
		t.Fatalf("second failure terminal: %v", err)
	}
	if got, want := wrRunEnd(t, db, run), wrEndLine("completed", "", "The Scribe reported a failure: "+reason, true, false); got != want {
		t.Fatalf("the second failure terminal changed the wiki update: %s", got)
	}
}
