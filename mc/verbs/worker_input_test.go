package verbs

import (
	"strings"
	"testing"

	"mc/dispatch"
)

func TestWorkerBriefDeliversProceduresWithoutSystemMechanics(t *testing.T) {
	db := dvSpine(t)
	task := dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2)
	dvInsertTask(t, db, task)
	dvExec(t, db, `UPDATE tasks SET description='The export contains the requested date.' WHERE id=1`)
	brief := briefFor(t, db, dispatch.RoleWorker, ptr64(1), briefInputs{})
	for _, heading := range []string{"Agent credentials", "Failure controls", "Correction allowances"} {
		if brief.part(heading) != nil {
			t.Errorf("Worker receives system mechanics: %s", heading)
		}
	}
	if task := brief.part(partTask); task == nil || task.Text != "The export contains the requested date." || len(task.Facts) != 2 {
		t.Fatalf("Worker lost its assignment: %+v", task)
	}
	text, err := renderRunBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"host provider proxy", "credential update", "The Scribe", "three unsuccessful corrections"} {
		if strings.Contains(text, removed) {
			t.Errorf("Worker still receives %q", removed)
		}
	}
	for _, required := range []string{
		"~/.agents/skills/mc-worker/SKILL.md",
		"before your first `mc` call and before the terminal",
		"Use only permitted `mc` commands with your own run identity",
		"Correction from the Verifier", "Merge candidate", "Revision request", "Requirements revision", "Checkpoint version",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("Worker cannot discover required procedure: %s", required)
		}
	}
	skills, err := RoleSkillSet(dispatch.RoleWorker)
	if err != nil {
		t.Fatal(err)
	}
	profileText := text
	for _, skill := range skills {
		if skill.Name == "mc-worker" {
			profileText += skill.Body
		}
		path := installedSkillRoot + skill.Name + "/SKILL.md"
		if !strings.Contains(text, path) || skill.Body == "" {
			t.Errorf("delivered skill and pointer disagree: %s", path)
		}
	}
	if strings.Contains(profileText, "agent profile") {
		t.Fatal("the Worker input names the agent profile")
	}
}

func TestWorkerBriefKeepsCasePayloads(t *testing.T) {
	for _, heading := range []string{
		partCustomization, partRun, partTask, "Project environment", partGitState,
		partRequirementsRevision, partRevision, partMergeCandidate, partCorrection,
		"Operator allowance explanation", partLastReport,
	} {
		original := briefPart{Heading: heading, Explanation: "Original explanation.",
			Facts:   []briefFact{{Label: "File", Value: "/mc/artifacts/plane/1/input.md"}},
			TextTag: "input", Text: "Preserve the requested behavior.", Rows: []string{"retained row"}}
		brief := runBrief{Parts: []briefPart{original}}
		trimWorkerBrief(&brief)
		if len(brief.Parts) != 1 {
			t.Fatalf("lost %s", heading)
		}
		got := brief.Parts[0]
		if got.Facts[0] != original.Facts[0] || got.Text != original.Text || got.TextTag != original.TextTag || got.Rows.([]string)[0] != "retained row" {
			t.Errorf("changed the payload of %s", heading)
		}
	}
}
