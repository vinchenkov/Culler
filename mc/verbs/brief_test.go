package verbs

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc/dispatch"
	"mc/domain"
	"mc/repository"
	"mc/substrate"
	"mc/wikitest"
)

// Assigned task roles have a task subject. Project proposal roles and the Scribe have no task subject.
var briefSubjectRoles = []dispatch.Role{
	dispatch.RoleWorker, dispatch.RoleVerifier, dispatch.RolePackager,
}

var briefVisionRoles = map[dispatch.Role]bool{
	dispatch.RoleStrategist: true,
	dispatch.RoleArbiter:    true,
}

func briefFor(t *testing.T, q Q, role dispatch.Role, subject *int64, in briefInputs) runBrief {
	t.Helper()
	brief, err := buildRunBrief(context.Background(), q, &dispatch.Spawn{Project: "ws-test", Role: role, SubjectID: subject}, "run-"+string(role), in)
	if err != nil {
		t.Fatalf("%s brief: %v", role, err)
	}
	return brief
}

func allBriefRoles() []dispatch.Role {
	return append([]dispatch.Role{dispatch.RoleStrategist, dispatch.RoleArbiter, dispatch.RoleScribe}, briefSubjectRoles...)
}

func briefSubject(role dispatch.Role, id int64) *int64 {
	for _, r := range briefSubjectRoles {
		if r == role {
			return &id
		}
	}
	return nil
}

func TestRunBriefHoldsTheRoleDirectiveOfEachRole(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	dvInsertTask(t, db, dvTask(2, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	for _, role := range allBriefRoles() {
		want, err := directiveForRole(role)
		if err != nil {
			t.Fatal(err)
		}
		first := briefFor(t, db, role, briefSubject(role, 1), briefInputs{})
		second := briefFor(t, db, role, briefSubject(role, 2), briefInputs{})
		if first.Directive != want || second.Directive != want {
			t.Errorf("%s: the run brief does not hold the role directive of its role", role)
		}
		text, err := renderRunBrief(first)
		if err != nil {
			t.Fatal(err)
		}
		if got := parseRenderedBrief(t, text).Directive; got != strings.TrimSpace(want) {
			t.Errorf("%s: the rendered directive differs from the role directive:\n%s", role, got)
		}
		if role == dispatch.RoleStrategist {
			if !strings.Contains(text, "# Strategist run ") || !strings.HasPrefix(want, "# Strategist\n") {
				t.Fatal("The Strategist brief and directive must use the role name.")
			}
			if !strings.Contains(want, "~/.agents/skills/mc-strategist/SKILL.md") {
				t.Fatal("The Strategist directive must name its command skill.")
			}
		}
	}
}

func TestRunBriefGivesTheVisionOnlyToTheRolesThatSelectAndPlanWork(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	vision := &ProjectVisionCheck{State: ProjectVisionPresent, Ready: true, Path: "/host/project/VISION.md", Content: "Build reliable software."}
	for _, role := range allBriefRoles() {
		brief := briefFor(t, db, role, briefSubject(role, 1), briefInputs{Vision: vision})
		part := brief.part(partVision)
		if briefVisionRoles[role] != (part != nil) {
			t.Errorf("%s: vision part present = %v, want %v", role, part != nil, briefVisionRoles[role])
		}
		if part != nil && part.Text != vision.Content {
			t.Errorf("%s: the vision part lost the content: %q", role, part.Text)
		}
		text, err := renderRunBrief(brief)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, vision.Path) {
			t.Errorf("%s: the run brief holds the host location of the vision file", role)
		}
	}
}

// Each part tells a first-time reader what it is and what to do with it, in a
// maximum of two sentences, before any data.
func TestRunBriefExplainsEachPart(t *testing.T) {
	db := dvSpine(t)
	task := dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2)
	dvInsertTask(t, db, task)
	boardRevision(t, db, 1, "The proof covers each criterion.")
	dvExec(t, db, `UPDATE tasks SET revision_notes='tighten the proof', correction_count=1 WHERE id=1`)
	dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject, verdict_outcome, evidence_path, correction_path)
		VALUES ('v1', 'pipeline', 'verifier', 'ws-test', 1, 'correct', 'evidence/e1.md', 'corrections/c1.md')`)
	dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject, output_path, outcome)
		VALUES ('w1', 'pipeline', 'worker', 'ws-test', 1, 'outputs/w1.md', 'completed')`)
	// The Scribe brief holds each of its parts for a wiki update that starts
	// after a successful wiki update and the merge of a task.
	briefWikiSucceeded(t, db, wrCommitA)
	briefPackagedTask(t, db, 2, "merged task", "criterion")
	briefMergeTask(t, db, 2)
	vision := &ProjectVisionCheck{State: ProjectVisionPresent, Ready: true, Content: "Build reliable software."}
	for _, role := range allBriefRoles() {
		in := briefInputs{Vision: vision}
		if role == dispatch.RoleScribe {
			in.Repository = scribeBriefInputs(wrCommitB).Repository
		}
		brief := briefFor(t, db, role, briefSubject(role, 1), in)
		if len(brief.Parts) < 2 {
			t.Errorf("%s: the run brief holds %d parts", role, len(brief.Parts))
		}
		if role == dispatch.RoleScribe && (len(brief.Parts) != 5 || brief.part(partMergedTasks) == nil) {
			t.Errorf("the Scribe run brief holds %d parts, want its five parts with the %s part", len(brief.Parts), partMergedTasks)
		}
		if role == dispatch.RoleWorker {
			for _, heading := range []string{partRevision, partRequirementsRevision, partCorrection, partLastReport} {
				if brief.part(heading) == nil {
					t.Errorf("the Worker run brief holds no %s part", heading)
				}
			}
		}
		seen := map[string]bool{}
		for _, part := range brief.Parts {
			if seen[part.Heading] {
				t.Errorf("%s: two parts share the heading %q", role, part.Heading)
			}
			seen[part.Heading] = true
			sentences := strings.Count(part.Explanation, ". ") + 1
			if part.Explanation == "" || !strings.HasSuffix(part.Explanation, ".") || sentences > 2 {
				t.Errorf("%s: part %q needs an explanation of one or two sentences, got %q", role, part.Heading, part.Explanation)
			}
		}
	}
}

func TestRunBriefSubjectHoldsNoBoardState(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	wantFacts := map[dispatch.Role][]string{
		dispatch.RoleWorker:   {"Identifier", "Title"},
		dispatch.RoleVerifier: {"Identifier", "Title", "Unsuccessful Verifier corrections"},
		dispatch.RolePackager: {"Identifier", "Title", "Requirements version"},
	}
	for role, want := range wantFacts {
		part := briefFor(t, db, role, briefSubject(role, 1), briefInputs{}).part(partTask)
		if part == nil {
			t.Errorf("%s: the run brief holds no task part", role)
			continue
		}
		var got []string
		for _, f := range part.Facts {
			got = append(got, f.Label)
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%s: task facts = %v, want %v", role, got, want)
		}
	}
	if part := briefFor(t, db, dispatch.RoleScribe, nil, briefInputs{}).part(partTask); part != nil {
		t.Errorf("the Scribe run brief holds a task part: %+v", part)
	}
}

// A merged task is in the repository, so only unmerged work reaches the Arbiter.
func TestArbiterBriefHoldsOnlyActiveTasks(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusPackaged, 2))
	merged := dvTask(2, dispatch.ScopeTask, dispatch.StatusPackaged, 2)
	merged.Title = "merged last week"
	decided := time.Now()
	merged.Decision, merged.DecidedAt, merged.Archived = dispatch.DecisionApproved, &decided, true
	dvInsertTask(t, db, merged)
	dvInsertTask(t, db, dvTask(3, dispatch.ScopeTask, dispatch.StatusProposed, 2))

	brief, err := buildRunBrief(context.Background(), db, &dispatch.Spawn{
		Project: "ws-test", Role: dispatch.RoleArbiter, ProposedPool: []int64{3},
	}, "arbiter-run", briefInputs{})
	if err != nil {
		t.Fatal(err)
	}
	active, ok := brief.part(partActiveTasks).Rows.([]briefBoardRow)
	if !ok || len(active) != 1 || active[0].ID != 1 {
		t.Fatalf("active tasks = %+v, want only task 1", active)
	}
}

func TestRenderedBriefKeepsProseReadable(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	description := "Required end state:\n\n- The file \"a.txt\" exists.\n- x < y && y > z."
	dvExec(t, db, `UPDATE tasks SET description=? WHERE id=1`, description)
	text, err := buildSpawnBrief(context.Background(), db, &dispatch.Spawn{Project: "ws-test", Role: dispatch.RoleWorker, SubjectID: ptr64(1)}, "worker-run")
	if err != nil {
		t.Fatal(err)
	}
	if got := parseRenderedBrief(t, text).part(partTask).Text; got != description {
		t.Fatalf("the task description changed on the way to the agent:\n%q", got)
	}
	for _, escape := range []string{`\n`, `\"`, `\u003c`} {
		if strings.Contains(text, escape) {
			t.Fatalf("the run brief holds the escape sequence %s:\n%s", escape, text)
		}
	}
}

// The run brief of a wiki update run names the run, the Project, and the
// start commit. The run has no task, no Vision, and no failure allowance.
func TestScribeBriefHoldsTheStartCommit(t *testing.T) {
	db := dvSpine(t)
	commit := strings.Repeat("c", 40)
	vision := &ProjectVisionCheck{State: ProjectVisionPresent, Ready: true, Content: "Build reliable software."}
	brief := briefFor(t, db, dispatch.RoleScribe, nil, briefInputs{Vision: vision, Repository: &substrate.DispatchRepository{
		DestinationBranch: "main", InputCommit: commit, MaxTemporaryBytes: 1 << 30, WikiUpdate: "run-scribe",
	}})
	text, err := renderRunBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	rendered := parseRenderedBrief(t, text)

	want := []struct {
		heading, explanation string
		facts                map[string]string
	}{
		{partCustomization, "This part identifies the operator's custom instructions and their revision.",
			map[string]string{"Project": "ws-test", "Role": "scribe", "Customization revision": "0",
				"Standard directive digest": brief.part(partCustomization).fact("Standard directive digest")}},
		{partRun, "This value identifies this run. It is the `<run_id>` value of each `mc` command.",
			map[string]string{"Run identifier": "run-scribe"}},
		{partProject, "The assigned repository is at `/workspace/source`.",
			map[string]string{"Identifier": "ws-test"}},
		{partGitState, "The repository starts at the accepted `Start commit`. The previous update identifies the earlier source version when available.",
			map[string]string{"Start commit": commit}},
	}
	// The role directive is the first section. The parts follow it.
	if len(rendered.Parts) != len(want)+1 || rendered.Parts[0].Heading != "Standard Culler directive" {
		t.Fatalf("sections of the Scribe run brief = %v, want the directive and %d parts", rendered.headings(), len(want))
	}
	for i, part := range rendered.Parts[1:] {
		if part.Heading != want[i].heading || part.Explanation != want[i].explanation {
			t.Errorf("part %d = %q with the explanation %q\nwant %q with the explanation %q",
				i+1, part.Heading, part.Explanation, want[i].heading, want[i].explanation)
		}
		if len(part.Facts) != len(want[i].facts) {
			t.Errorf("part %q holds the facts %v, want %v", part.Heading, part.Facts, want[i].facts)
		}
		for label, value := range want[i].facts {
			if value == "" || part.Facts[label] != value {
				t.Errorf("part %q: fact %q = %q, want %q", part.Heading, label, part.Facts[label], value)
			}
		}
	}
	for _, heading := range []string{"Agent credentials", "Failure controls", partVision, partProposalDecisions, partTask} {
		if brief.part(heading) != nil {
			t.Errorf("the Scribe run brief holds the part %q", heading)
		}
	}

	// A run with no repository plan gets no Git facts.
	bare := briefFor(t, db, dispatch.RoleScribe, nil, briefInputs{})
	if bare.part(partGitState) != nil || len(bare.Parts) != 3 {
		t.Errorf("the Scribe run brief with no repository plan holds %d parts, want three and no Git state part", len(bare.Parts))
	}
}

// scribeBriefInputs is the input of the run brief of a wiki update run that
// starts at the commit.
func scribeBriefInputs(commit string) briefInputs {
	return briefInputs{Repository: &substrate.DispatchRepository{
		DestinationBranch: "main", InputCommit: commit, MaxTemporaryBytes: 1 << 30, WikiUpdate: "run-scribe",
	}}
}

// scribeBrief returns the run brief of a wiki update run of ws-test that
// starts at the commit, as the Scribe reads it.
func scribeBrief(t *testing.T, db *sql.DB, commit string) renderedBrief {
	t.Helper()
	text, err := renderRunBrief(briefFor(t, db, dispatch.RoleScribe, nil, scribeBriefInputs(commit)))
	if err != nil {
		t.Fatal(err)
	}
	return parseRenderedBrief(t, text)
}

// briefWikiSucceeded records a successful wiki update of ws-test that started
// at the commit. It returns the run of the wiki update.
func briefWikiSucceeded(t *testing.T, db *sql.DB, commit string) string {
	t.Helper()
	run, recorded, err := wikitest.RecordCurrentWiki(context.Background(), db, "ws-test", commit, time.Now().Add(-2*time.Hour))
	if err != nil || !recorded {
		t.Fatalf("record a successful wiki update: recorded=%v err=%v", recorded, err)
	}
	return run
}

// briefPackagedTask inserts a packaged task of ws-test with a merged candidate.
func briefPackagedTask(t *testing.T, db *sql.DB, id int64, title, description string) {
	t.Helper()
	task := dvTask(id, dispatch.ScopeTask, dispatch.StatusPackaged, 2)
	task.Title = title
	dvInsertTask(t, db, task)
	dvExec(t, db, `UPDATE tasks SET description=NULLIF(?,'') WHERE id=?`, description, id)
	stMergedCandidate(t, db, id)
}

// briefMergeTask records the operator approval and the merge of the packaged
// task into its destination branch. It returns the run of the merge.
func briefMergeTask(t *testing.T, db *sql.DB, task int64) string {
	t.Helper()
	stApprove(t, db, task)
	project := dfStr(t, db, `SELECT project FROM tasks WHERE id=?`, task)
	run, _ := startOperation(t, db, dispatch.RepositoryOperation{Project: project, TaskID: task, Operation: dispatch.OperationApply})["run_id"].(string)
	if _, err := RepositoryRecord(db, nil, "apply", run, RepositoryResult{TaskID: task,
		Applied: &repository.Applied{Authorization: run, Commit: stCandidateCommit}}); err != nil {
		t.Fatalf("record the merge of task %d: %v", task, err)
	}
	return run
}

// mergedTaskRows returns the rows of the Merged tasks part, or nil when the
// run brief holds no such part.
func mergedTaskRows(t *testing.T, brief renderedBrief) []map[string]any {
	t.Helper()
	part := brief.part(partMergedTasks)
	if part == nil {
		return nil
	}
	rows := []map[string]any{}
	part.rows(t, &rows)
	return rows
}

// The run brief of a wiki update that follows a successful wiki update holds
// five parts. The Git state part names both ends of the range, and the Merged
// tasks part lists each task whose merge is in the range, oldest merge first.
func TestScribeBriefParts(t *testing.T) {
	db := dvSpine(t)
	lastStart := strings.Repeat("d", 40)
	// The merge of task 1 is before the last successful wiki update.
	briefPackagedTask(t, db, 1, "covered task", "The last wiki update covered this task.")
	briefMergeTask(t, db, 1)
	briefWikiSucceeded(t, db, wrCommitC)
	last := briefWikiSucceeded(t, db, lastStart)
	// The merge of a task of another Project is in no range of ws-test.
	dvAddProject(t, db, "ws-other", "active")
	stTask(t, db, 5, "ws-other", dispatch.StatusPackaged)
	stMergedCandidate(t, db, 5)
	briefMergeTask(t, db, 5)
	// Task 3 merges before task 2, so the merge order differs from the identifier order.
	const criteria = "Required end state:\n\n- The file \"a.txt\" exists.\n- x < y && y > z."
	briefPackagedTask(t, db, 2, "second merge", "")
	briefPackagedTask(t, db, 3, "first merge", criteria)
	briefMergeTask(t, db, 3)
	// A failed wiki update moves no end of the range. It started after the merge of task 3.
	wsEnded(t, db, "ws-test", time.Now().Add(-time.Hour), wsTimeLimit, true)
	upper := briefMergeTask(t, db, 2)
	// An active task is no row.
	stTask(t, db, 4, "ws-test", dispatch.StatusSeeded)

	brief := scribeBrief(t, db, wrCommitB)
	want := []struct {
		heading, explanation string
		facts                []string
	}{
		{partCustomization, "This part identifies the operator's custom instructions and their revision.",
			[]string{"Project", "Role", "Customization revision", "Standard directive digest"}},
		{partRun, "This value identifies this run. It is the `<run_id>` value of each `mc` command.", []string{"Run identifier"}},
		{partProject, "The assigned repository is at `/workspace/source`.", []string{"Identifier"}},
		{partGitState, "The repository starts at the accepted `Start commit`. The previous update identifies the earlier source version when available.",
			[]string{"Start commit", "Start commit of the last successful wiki update"}},
		{"Merged tasks", "These tasks merged after the last successful wiki update, oldest first. " +
			"`Omitted tasks` counts older rows excluded from this part.", nil},
	}
	if len(brief.Parts) != len(want)+1 || brief.Parts[0].Heading != "Standard Culler directive" {
		t.Fatalf("sections of the Scribe run brief = %v, want the directive and %d parts", brief.headings(), len(want))
	}
	for i, part := range brief.Parts[1:] {
		if part.Heading != want[i].heading || part.Explanation != want[i].explanation {
			t.Errorf("part %d = %q with the explanation %q\nwant %q with the explanation %q",
				i+1, part.Heading, part.Explanation, want[i].heading, want[i].explanation)
		}
		if len(part.Facts) != len(want[i].facts) {
			t.Errorf("part %q holds the facts %v, want the labels %v", part.Heading, part.Facts, want[i].facts)
		}
		for _, label := range want[i].facts {
			if part.Facts[label] == "" {
				t.Errorf("part %q holds no fact %q: %v", part.Heading, label, part.Facts)
			}
		}
	}
	git := brief.part(partGitState)
	if git.Facts["Start commit"] != wrCommitB || git.Facts["Start commit of the last successful wiki update"] != lastStart {
		t.Errorf("Git facts = %v, want the start commit %s and the start commit %s of the last successful wiki update", git.Facts, wrCommitB, lastStart)
	}
	// The OpenWiki procedure uses both commits for its coverage.
	skill := roleSkillBody(t, dispatch.RoleScribe, "openwiki")
	for label := range git.Facts {
		if !strings.Contains(skill, "`"+label+"`") {
			t.Errorf("the %s skill does not name the fact %q of the %s part", "openwiki", label, partGitState)
		}
	}
	wantRows := []map[string]any{
		{"id": float64(3), "title": "first merge", "description": criteria},
		{"id": float64(2), "title": "second merge", "description": ""},
	}
	if got := mergedTaskRows(t, brief); !reflect.DeepEqual(got, wantRows) {
		t.Errorf("rows of the Merged tasks part = %v\nwant %v", got, wantRows)
	}

	// The range starts above the merge marker of the last successful wiki
	// update and ends at the merge marker of the Project.
	lower := dfInt(t, db, `SELECT merge_marker FROM wiki_updates WHERE run_id=?`, last)
	if covered := dfInt(t, db, `SELECT created_seq FROM runs WHERE id=(SELECT run_id FROM merge_operations WHERE task_id=1 AND operation='apply')`); lower != covered {
		t.Errorf("merge marker of the last successful wiki update = %d, want the merge run %d of task 1", lower, covered)
	}
	marker, err := domain.WikiMergeMarker(context.Background(), db, "ws-test")
	if err != nil {
		t.Fatal(err)
	}
	if end := dfInt(t, db, `SELECT created_seq FROM runs WHERE id=?`, upper); marker != end {
		t.Errorf("merge marker of the Project = %d, want the merge run %d of task 2", marker, end)
	}
}

// The Merged tasks part is absent when the range holds no merge of a task
// into main.
func TestScribeBriefOmitsMergedTasksForAnEmptyRange(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, db *sql.DB)
	}{
		{"no merge", func(t *testing.T, db *sql.DB) { briefWikiSucceeded(t, db, wrCommitA) }},
		{"a merge at the lower limit", func(t *testing.T, db *sql.DB) {
			briefPackagedTask(t, db, 1, "covered task", "criterion")
			briefMergeTask(t, db, 1)
			briefWikiSucceeded(t, db, wrCommitA)
		}},
		{"a merge with no result", func(t *testing.T, db *sql.DB) {
			briefWikiSucceeded(t, db, wrCommitA)
			briefPackagedTask(t, db, 1, "active merge", "criterion")
			stApprove(t, db, 1)
			startOperation(t, db, dispatch.RepositoryOperation{Project: "ws-test", TaskID: 1, Operation: dispatch.OperationApply})
		}},
		{"the merge of a wiki commit", func(t *testing.T, db *sql.DB) {
			u := wmFixtureUpdate(t, db)
			candidate := u.candidate()
			merge := u.startRun(dispatch.OperationApply, "")
			u.record("apply", merge, u.applied(merge, candidate))
			if got := wrDump(t, db, `SELECT result, merged_commit FROM wiki_updates WHERE run_id=?`, u.run); got != "result=succeeded|merged_commit="+wrCommitC+"|\n" {
				t.Fatalf("wiki update after the merge of its wiki commit = %s", got)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := dvSpine(t)
			test.setup(t, db)
			brief := scribeBrief(t, db, wrCommitB)
			if got := brief.headings(); !reflect.DeepEqual(got[1:], []string{partCustomization, partRun, partProject, partGitState}) {
				t.Fatalf("parts of the Scribe run brief = %v, want four parts and no %s part", got[1:], partMergedTasks)
			}
			if facts := brief.part(partGitState).Facts; len(facts) != 2 || facts["Start commit of the last successful wiki update"] == "" {
				t.Fatalf("Git facts = %v, want both commits of the range", facts)
			}
		})
	}
}

// With no successful wiki update the wiki update has no range. The Git state
// part then holds only the start commit, and the Merged tasks part is absent.
func TestScribeBriefWithNoSuccessfulWikiUpdate(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, db *sql.DB)
	}{
		{"no wiki update", func(*testing.T, *sql.DB) {}},
		{"a failed wiki update", func(t *testing.T, db *sql.DB) {
			wsEnded(t, db, "ws-test", time.Now().Add(-2*time.Hour), wsTimeLimit, true)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := dvSpine(t)
			test.setup(t, db)
			briefPackagedTask(t, db, 1, "merged task", "criterion")
			briefMergeTask(t, db, 1)
			brief := scribeBrief(t, db, wrCommitB)
			if got := brief.headings(); !reflect.DeepEqual(got[1:], []string{partCustomization, partRun, partProject, partGitState}) {
				t.Fatalf("parts of the Scribe run brief = %v, want four parts and no %s part", got[1:], partMergedTasks)
			}
			if facts := brief.part(partGitState).Facts; !reflect.DeepEqual(facts, map[string]string{"Start commit": wrCommitB}) {
				t.Fatalf("Git facts = %v, want only the start commit", facts)
			}
		})
	}
}

// The JSON block of the Merged tasks part has a byte budget. Above the budget
// the part holds the newest rows that fit, and a fact counts the older rows
// that it omits.
func TestScribeBriefMergedTasksBudget(t *testing.T) {
	const budget = 262144
	// part returns the Merged tasks part of a wiki update after the merges of
	// tasks 1 to 3, in that order, with one description for each task.
	part := func(t *testing.T, descriptions ...string) (*renderedPart, []int64) {
		t.Helper()
		db := dvSpine(t)
		briefWikiSucceeded(t, db, wrCommitA)
		for i, description := range descriptions {
			id := int64(i + 1)
			briefPackagedTask(t, db, id, fmt.Sprintf("task %d", id), description)
			briefMergeTask(t, db, id)
		}
		brief := scribeBrief(t, db, wrCommitB)
		part := brief.part(partMergedTasks)
		if part == nil {
			t.Fatalf("the Scribe run brief holds no %s part: %v", partMergedTasks, brief.headings())
		}
		ids := []int64{}
		for _, row := range mergedTaskRows(t, brief) {
			ids = append(ids, int64(row["id"].(float64)))
			if want := descriptions[int(row["id"].(float64))-1]; row["description"] != want {
				t.Fatalf("task %v: the description has %d bytes, want %d", row["id"], len(row["description"].(string)), len(want))
			}
		}
		return part, ids
	}
	empty, _ := part(t, "", "", "")
	fill := budget - len(empty.Rows)
	if fill < 2 {
		t.Fatalf("the JSON block of three rows with no description has %d bytes, which leaves no space below the budget", len(empty.Rows))
	}
	first, second := strings.Repeat("a", fill/2), strings.Repeat("b", fill-fill/2)

	for _, test := range []struct {
		name         string
		descriptions []string
		rows         []int64
		omitted      string // the value of the Omitted tasks fact, or "" for no fact
		size         int    // the size of the JSON block, or 0 for no check
	}{
		{"a block of the size of the budget", []string{first, second, ""}, []int64{1, 2, 3}, "", budget},
		{"one byte above the budget", []string{first, second + "b", ""}, []int64{2, 3}, "1", 0},
		{"a newer row that does not fit", []string{"", strings.Repeat("b", budget), ""}, []int64{3}, "2", 0},
		{"no row fits", []string{"", "", strings.Repeat("c", budget)}, []int64{}, "3", len("[]")},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ids := part(t, test.descriptions...)
			if !reflect.DeepEqual(ids, test.rows) {
				t.Errorf("rows = %v, want %v", ids, test.rows)
			}
			if len(got.Rows) > budget || (test.size != 0 && len(got.Rows) != test.size) {
				t.Errorf("the JSON block has %d bytes, want %d (0 states a size of a maximum of %d)", len(got.Rows), test.size, budget)
			}
			wantFacts := map[string]string{}
			if test.omitted != "" {
				wantFacts["Omitted tasks"] = test.omitted
			}
			if !reflect.DeepEqual(got.Facts, wantFacts) {
				t.Errorf("facts = %v, want %v", got.Facts, wantFacts)
			}
			if omitted, _ := strconv.Atoi(test.omitted); omitted+len(ids) != len(test.descriptions) {
				t.Errorf("the part lists %d rows and omits %d, want %d tasks in sum", len(ids), omitted, len(test.descriptions))
			}
		})
	}
}
