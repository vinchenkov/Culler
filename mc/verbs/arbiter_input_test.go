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
		"The all-reject batch", "Historical work",
		"A run that cannot give a result", "Project role customization",
	} {
		if !strings.Contains(brief.Directive, section) || !strings.Contains(skill, "## "+section+"\n") {
			t.Errorf("conditional procedure has no pointer or destination: %s", section)
		}
	}
	requireTaskDescriptionRulesOnce(t, "Arbiter", text, set)
	// Shared skills retain their contract and stay outside this duplicate audit.
	input := text + "\n" + skill
	for _, rule := range []string{
		"Submit one verdict for each proposal in **Proposed pool**, and no verdicts for other tasks.",
		"Promote exactly one proposal and reject the rest",
		"End the run with exactly one permitted terminal action, including after an execution failure.",
		"Judge the description of each proposal against the task-description skill.",
		"Reject a proposal that violates a rule of the skill.",
		"Use `invalid_criteria` for a violation of a rule for acceptance criteria.",
		"Identify the invalid criterion and the rule that it violates in the rejection reason.",
		"Task overlap means proposed changes to the same files or behavior.",
		"Compare proposals with each other and **Active tasks**.",
		"Reject a proposal that overlaps active or merged work, even when the proposal is better.",
		"An extension of merged work is not a duplicate.",
		"When two valid pool proposals overlap, promote the superset and reject the subset.",
		"The Arbiter must choose one proposal to proceed.",
		"It can reject both only if each proposal has at least one of these defects:",
		"It duplicates active and recent (see Historical work) tasks in the same Project.",
		"It requires checks that Culler’s rules do not allow.",
		"Explain the defect in each proposal.",
		"For a duplicate, identify the existing task.",
		"Each rejection in an all-reject batch requires one of these exceptions:",
		"A valid `duplicate_of` reference.",
		"A `rejection_category` value of `invalid_criteria` and a reason.",
		"The batch can combine exceptions across proposals.",
		"Each `duplicate_of` reference must name an active or historical task of this Project.",
		"The fields `duplicate_of` and `rejection_category` are mutually exclusive.",
		"Treat equivalent work as a reason for rejection under the batch rules.",
		"Use each cancellation reason as context for the operator's preference.",
		"Apply each relevant resolution to this assignment.",
		"Read this Project's committed files at the read-only repository copy.",
		"The task list omits descriptions.",
		"Use the task read for scope absent from **Proposed pool** and **Active tasks**.",
		"Follow the operator's custom instructions within the Culler contract.",
		"The requirements in the Culler contract remain.",
		"Submit this terminal to end the run with a customization conflict:",
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
		"Orchestrate by default", "A ready task permits a batch that rejects every proposal.", "## Research dependencies\n",
	} {
		if strings.Contains(input, removed) {
			t.Errorf("Arbiter still receives %q", removed)
		}
	}
	requireAgentText(t, "Arbiter contract", roleContract(dispatch.RoleArbiter), []string{
		"Preserve proposal requirements and accepted work.",
		"The promotion limit and the rules of the task-description skill belong to this contract.",
		"Use only permitted `mc` commands with your own run identity",
		"Do not defer, merge, or rewrite proposals, change active tasks, or start another role.",
	}, nil)
	_, customizationProcedure, found := strings.Cut(skill, "## Project role customization\n")
	if !found {
		t.Fatal("Arbiter skill omits the customization conflict procedure")
	}
	customizationProcedure, _, _ = strings.Cut(customizationProcedure, "\n## ")
	requireAgentText(t, "Arbiter customization conflict terminal", customizationProcedure, []string{
		"Submit this terminal to end the run with a customization conflict:\n\n- `mc run customization-conflict --request <json>`",
	}, nil)
}

func TestArbiterBriefDeliversProposalReviewGuidance(t *testing.T) {
	brief := briefFor(t, dvSpine(t), dispatch.RoleArbiter, nil, briefInputs{})
	text, err := renderRunBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	directive := parseRenderedBrief(t, text).Directive
	requireAgentText(t, "Arbiter proposal review", directive, []string{
		"You promote the highest-leverage proposal from the Strategist to advance the user's Vision.",
		"Rank proposals by how far they advance the **Vision** ( VISION.md ) goal for their cost.",
		"Judge a small task by the result it produces.",
		"Judge a large task by its feasible benefit and full cost.",
		"Consider new capabilities as well as defects.",
		"A product can work correctly and still lack the capability that makes it useful.",
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
