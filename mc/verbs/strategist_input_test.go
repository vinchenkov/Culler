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
	requireAgentText(t, "Strategist pointers", brief.Directive, []string{"before each `mc` call"}, nil)
	requireTaskDescriptionRulesOnce(t, "Strategist", text, set)
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
