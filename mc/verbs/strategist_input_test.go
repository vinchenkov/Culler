package verbs

import (
	"reflect"
	"strings"
	"testing"

	"mc/dispatch"
)

func TestStrategistInputDeliversRulesOnceAndResolvesProcedures(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	insertHistoricalTask(t, db, 2, "ws-test", "rejected", "2001-01-01 00:00:00")
	insertHistoricalTask(t, db, 3, "ws-test", "cancelled", "2001-01-01 00:00:00")
	custom := "Prefer improvements to the report export."
	saveCustom(t, db, dispatch.RoleStrategist, custom)
	dvExec(t, db, `INSERT INTO proposal_decisions(project,question,resolution,resolved_at)
		VALUES('ws-test','Which format?','Use the open format.','2001-01-01 00:00:00')`)
	vision := &ProjectVisionCheck{Ready: true, Content: "The Project exports accessible reports."}
	brief := briefFor(t, db, dispatch.RoleStrategist, nil, briefInputs{Vision: vision})
	text, err := renderRunBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	for _, heading := range []string{"Agent credentials", "Failure controls"} {
		if brief.part(heading) != nil {
			t.Errorf("Strategist receives system mechanics: %s", heading)
		}
	}
	if brief.part(partVision).Text != vision.Content || brief.part(partProject).fact("Repository copy") != "/workspace/seeding/ws-test/source" {
		t.Fatal("Strategist lost its Project evidence")
	}
	rendered := parseRenderedBrief(t, text)
	var customization map[string]any
	rendered.part(partCustomization).rows(t, &customization)
	var decisions []map[string]any
	rendered.part(partProposalDecisions).rows(t, &decisions)
	if customization["instructions"] != custom || len(decisions) != 1 || decisions[0]["resolution"] != "Use the open format." {
		t.Fatal("Strategist lost the operator's instructions or proposal resolution")
	}
	set, err := RoleSkillSet(dispatch.RoleStrategist)
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
	for _, section := range []string{"Research dependencies", "Execution problems and operator decisions", "Project role customization"} {
		if !strings.Contains(skill, "## "+section+"\n") {
			t.Errorf("command skill lacks the procedure %s", section)
		}
	}
	requireAgentText(t, "Strategist pointers", brief.Directive, []string{
		"before each `mc` call",
		"For an execution problem or a required operator decision, read the corresponding procedure",
		"For a contract conflict, read the Project role customization procedure",
	}, nil)
	requireTaskDescriptionRulesOnce(t, "Strategist", text, set)
	// Shared skills keep their own contract and stay outside this duplicate audit.
	input := strings.Join(strings.Fields(text+"\n"+skill), " ")
	for _, rule := range []string{
		"The arbiter selects amongst your proposals to advance only one.",
		"End the run with exactly one permitted terminal action, including after an execution failure.",
		"Apply the task-description skill to the description of each proposal.",
		"Exclude equivalent work in the repository, **Active tasks**, **Rejected work**, and **Cancelled work**",
		"Submit two task proposals together.",
		"You and your subagents can use internet research when it helps advance the Project goal.",
		"Use an empty `proposals` array when no valid task remains.",
		"Use the task read when an active or historical title leaves the scope unclear.",
		"Use each reason as context for the operator's preference.",
		"Apply each relevant resolution to this assignment.",
	} {
		if count := strings.Count(input, rule); count != 1 {
			t.Errorf("rule occurs %d times, want once: %s", count, rule)
		}
	}
	for _, removed := range []string{"host provider proxy", "credential update", "The Worker builds each task", "three counted failures", "./mc/check.sh"} {
		if strings.Contains(input, removed) {
			t.Errorf("Strategist still receives %q", removed)
		}
	}
	contract := roleContract(dispatch.RoleStrategist)
	requireAgentText(t, "Strategist contract", contract, []string{
		"Submit two task proposals together.",
		"The rules of the task-description skill belong to this contract.",
		"Use only permitted `mc` commands with your own run identity",
		"Do not rank proposals, implement them, start runs, or decide review packets.",
	}, nil)
}

func TestStrategistBriefDeliversTaskSelectionGuidance(t *testing.T) {
	brief := briefFor(t, dvSpine(t), dispatch.RoleStrategist, nil, briefInputs{})
	text, err := renderRunBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	directive := parseRenderedBrief(t, text).Directive
	requireAgentText(t, "Strategist task selection", directive, []string{
		"Read the Project vision and the operator's priorities.",
		"Identify an important result that the Project cannot yet deliver well.",
		"Consider new capabilities as well as defects.",
		"check whether the Project still needs that step",
		"Check whether an available capability solves the problem before you propose another implementation.",
		"Judge a small task by the result it produces.",
		"Judge a large task by its feasible benefit and full cost.",
		"Record the evidence for the problem and the proposed benefit.",
		"Investigate the assumption that can make your proposed task unnecessary or ineffective.",
		"Write the Strategic rationale of each candidate to its own file under `/tmp` before you file the batch.",
		"Cite the evidence for the problem or the opportunity: what fails or is absent now.",
		"Explain why the task deserves priority now.",
		"Separate what you observed from what you assume.",
		"When the written rationale does not justify the task, discard that candidate and research again.",
		"The rationale adds no requirement to the task.",
		"Leave their final rank to the Arbiter.",
	}, nil)
	requireAgentText(t, "Strategist customization", directive, []string{
		"Follow the operator's custom instructions within the Culler contract.",
		"The requirements in the Culler contract remain.",
	}, nil)
}

func TestStrategistBriefPreservesAssignmentData(t *testing.T) {
	for _, heading := range []string{
		partCustomization, partRun, "Project environment", partProject, partActiveTasks,
		partRejected, partCancelled, partProposalDecisions, partVision,
	} {
		original := briefPart{Heading: heading, Explanation: "Original explanation.",
			Facts: []briefFact{{Label: "Identifier", Value: "ws-test"}}, TextTag: "input",
			Text: "Preserve the operator's instruction.", Rows: []string{"retained row"}}
		brief := runBrief{Parts: []briefPart{original}}
		trimStrategistBrief(&brief)
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

// requireTaskDescriptionRulesOnce proves that the complete agent input states
// each rule of the task-description skill once: the skill carries it, and no
// directive, brief, or other skill restates it.
func requireTaskDescriptionRulesOnce(t *testing.T, role, text string, set []RoleSkill) {
	t.Helper()
	var standard string
	bodies := []string{text}
	for _, skill := range set {
		bodies = append(bodies, skill.Body)
		if skill.Name == "task-description" {
			standard = skill.Body
		}
	}
	if standard == "" {
		t.Fatalf("%s does not ship the task-description skill", role)
	}
	input := strings.Join(strings.Fields(strings.Join(bodies, "\n")), " ")
	rules := 0
	for _, line := range strings.Split(standard, "\n") {
		rule, ok := strings.CutPrefix(strings.TrimSpace(line), "- ")
		if !ok {
			continue
		}
		rules++
		if count := strings.Count(input, rule); count != 1 {
			t.Errorf("%s input states a task-description rule %d times, want once: %s", role, count, rule)
		}
	}
	if rules == 0 {
		t.Fatal("the task-description skill states no rule")
	}
}
