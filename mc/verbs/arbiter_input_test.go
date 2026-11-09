package verbs

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"mc/dispatch"
)

func TestArbiterInputPreservesAssignmentAndDeliversRulesOnce(t *testing.T) {
	db := dvSpine(t)
	proposal := dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2)
	const proposalDescription = "The export includes the selected date."
	dvInsertTask(t, db, proposal)
	active := dvTask(2, dispatch.ScopeTask, dispatch.StatusSeeded, 2)
	const activeDescription = "The export includes the selected format."
	dvInsertTask(t, db, active)
	dvExec(t, db, `UPDATE tasks SET description=? WHERE id=1`, proposalDescription)
	dvExec(t, db, `UPDATE tasks SET description=? WHERE id=2`, activeDescription)
	insertHistoricalTask(t, db, 3, "ws-test", "rejected", "2001-01-01 00:00:00")
	insertHistoricalTask(t, db, 4, "ws-test", "cancelled", "2001-01-01 00:00:00")
	custom := "Prefer improvements to the report export."
	saveCustom(t, db, dispatch.RoleArbiter, 0, custom)
	dvExec(t, db, `INSERT INTO proposal_decisions(project,question,resolution,resolved_at)
		VALUES('ws-test','Which format?','Use the open format.','2001-01-01 00:00:00')`)
	vision := &ProjectVisionCheck{Ready: true, Content: "The Project exports accessible reports."}
	brief, err := buildRunBrief(context.Background(), db, &dispatch.Spawn{
		Project: "ws-test", Role: dispatch.RoleArbiter, ProposedPool: []int64{1},
	}, "arbiter-run", briefInputs{Vision: vision})
	if err != nil {
		t.Fatal(err)
	}
	for _, heading := range []string{"Agent credentials", "Failure controls"} {
		if brief.part(heading) != nil {
			t.Errorf("Arbiter receives system mechanics: %s", heading)
		}
	}
	text, err := renderRunBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	rendered := parseRenderedBrief(t, text)
	var pool, board []briefBoardRow
	rendered.part(partPool).rows(t, &pool)
	rendered.part(partActiveTasks).rows(t, &board)
	if len(pool) != 1 || pool[0].ID != 1 || pool[0].Description != proposalDescription ||
		len(board) != 1 || board[0].ID != 2 || board[0].Description != activeDescription {
		t.Fatalf("assignment changed: pool=%+v board=%+v", pool, board)
	}
	var customization map[string]any
	rendered.part(partCustomization).rows(t, &customization)
	var decisions []map[string]any
	rendered.part(partProposalDecisions).rows(t, &decisions)
	if customization["instructions"] != custom || len(decisions) != 1 || decisions[0]["resolution"] != "Use the open format." {
		t.Fatal("Arbiter lost the operator's instructions or proposal resolution")
	}
	if rendered.part(partVision).Text != vision.Content ||
		rendered.part(partProject).Facts["Repository copy"] != "/workspace/seeding/ws-test/source" ||
		rendered.part(partRun).Facts["Run identifier"] != "arbiter-run" {
		t.Fatal("Arbiter lost its Project evidence or run identity")
	}
	for _, heading := range []string{partRejected, partCancelled} {
		if len(historicalRows(t, brief, heading)) != 1 {
			t.Errorf("Arbiter lost %s", heading)
		}
	}
	set, err := RoleSkillSet(dispatch.RoleArbiter)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range set {
		path := installedSkillRoot + skill.Name + "/SKILL.md"
		if !strings.Contains(brief.Directive, path) || skill.Body == "" {
			t.Errorf("skill pointer has no delivered file: %s", path)
		}
	}
	skill := set[0].Body
	for _, section := range []string{
		"The all-reject batch", "Historical work", "Research dependencies",
		"A run that cannot give a result", "Project role customization",
	} {
		if !strings.Contains(brief.Directive, section) || !strings.Contains(skill, "## "+section+"\n") {
			t.Errorf("conditional procedure has no pointer or destination: %s", section)
		}
	}
	// Shared skills retain their contract and stay outside this duplicate audit.
	input := text + "\n" + skill
	for _, rule := range []string{
		"Submit one verdict for each proposal in **Proposed pool**, and no verdicts for other tasks.",
		"Promote exactly one proposal and reject the rest",
		"End the run with exactly one permitted terminal action, including after an execution failure.",
		"New or changed tests must pass on the submitted commit.",
		"Reject whole-suite success requirements",
		"Reject required checks that need unavailable capabilities.",
		"An unchanged Project test failure alone does not invalidate a proposal.",
		"Reject a proposal whose result is not one Git commit to the Project repository.",
		"Compare proposals with each other, **Active tasks**, and merged work in the repository copy.",
		"An extension of merged work is not a duplicate.",
		"When two valid pool proposals overlap, promote the superset and reject the subset.",
		"Work in another Project cannot justify a duplicate rejection.",
		"A ready task has status `proposed`, `seeded`, `worked`, or `verified`.",
		"It is neither blocked nor stalled.",
		"Without a ready task, each rejection requires one of these exceptions:",
		"Each `duplicate_of` reference must name an active or merged task of this Project outside your pool.",
		"The fields `duplicate_of` and `rejection_category` are mutually exclusive.",
		"Treat equivalent work as a reason for rejection under the batch rules.",
		"Use each cancellation reason as context for the operator's preference.",
		"Apply each relevant resolution to this assignment.",
		"Read this Project's committed files at the read-only repository copy.",
		"The task list omits descriptions.",
		"Use the task read for scope absent from **Proposed pool** and **Active tasks**.",
		"Custom instructions replace defaults in this directive, the run brief, and role skills.",
		"Report direct observations without an inferred connection diagnosis.",
		"before your first `mc` call and before the terminal",
	} {
		if count := strings.Count(input, rule); count != 1 {
			t.Errorf("rule occurs %d times, want once: %s", count, rule)
		}
	}
	for _, removed := range []string{
		"host provider proxy", "credential update", "The Worker builds each task", "three counted failures",
		"./mc/check.sh", "10 cancelled tasks first", "160 Unicode characters", "agent profile",
	} {
		if strings.Contains(input, removed) {
			t.Errorf("Arbiter still receives %q", removed)
		}
	}
	requireAgentText(t, "Arbiter contract", roleContract(dispatch.RoleArbiter), []string{
		"Preserve proposal requirements and accepted work.",
		"Use only permitted `mc` commands with your own run identity",
		"Do not defer, merge, or rewrite proposals, change active tasks, or start another role.",
	}, nil)
}

func TestArbiterBriefPreservesPartPayloads(t *testing.T) {
	for _, heading := range []string{
		partCustomization, partRun, "Project environment", partProject, partPool, partActiveTasks,
		partRejected, partCancelled, partProposalDecisions, partVision,
	} {
		original := briefPart{Heading: heading, Explanation: "Original explanation.",
			Facts: []briefFact{{Label: "Identifier", Value: "ws-test"}}, TextTag: "input",
			Text: "Preserve the operator's instruction.", Rows: []string{"retained row"}}
		brief := runBrief{Parts: []briefPart{original}}
		trimArbiterBrief(&brief)
		if len(brief.Parts) != 1 {
			t.Fatalf("lost %s", heading)
		}
		got := brief.Parts[0]
		got.Explanation = original.Explanation
		if !reflect.DeepEqual(got, original) {
			t.Errorf("changed the payload of %s: %+v", heading, got)
		}
	}
}
