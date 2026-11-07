package verbs

import (
	"strings"
	"testing"

	"mc/dispatch"
)

func TestVerifierAssessmentIsPartOfTheCustomizationContract(t *testing.T) {
	directive, err := directiveForRole(dispatch.RoleVerifier)
	if err != nil {
		t.Fatal(err)
	}
	_, tail, found := strings.Cut(directive, "## Culler contract\n")
	if !found {
		t.Fatal("the Verifier directive has no contract")
	}
	contractSection, _, _ := strings.Cut(tail, "\n## ")
	if !strings.Contains(contractSection, "The Assessment duties belong to this contract.") {
		t.Fatal("the Verifier treats Assessment as a default that custom instructions can replace")
	}

	db := dvSpine(t)
	collie := &RunIdentity{Tier: "collie", RunID: "collie-test", VerbAllowlist: MaximumCollieAgentVerbs()}
	shown, err := ProjectRoleShow(db, collie, "ws-test", dispatch.RoleVerifier)
	if err != nil {
		t.Fatal(err)
	}
	contract := shown.(map[string]any)["contract"].(string)
	if !strings.Contains(directive, "## Culler contract\n\n"+contract+"\n\n## Project role customization") {
		t.Fatal("Collie and the Verifier receive different customization contract boundaries")
	}
	for _, duty := range []string{
		"Obtain your own results on the submitted commit for every acceptance criterion and each new or changed test.",
		"Run the Project's normal broad checks for task regressions, including required build and analysis checks.",
	} {
		if strings.Count(contract, duty) != 1 {
			t.Errorf("the Verifier contract must contain this duty once: %s", duty)
		}
	}
}

func TestVerifierBriefDeliversProceduresWithoutSystemMechanics(t *testing.T) {
	db := dvSpine(t)
	task := dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2)
	dvInsertTask(t, db, task)
	dvExec(t, db, `UPDATE tasks SET description='The export contains the requested date.' WHERE id=1`)
	brief := briefFor(t, db, dispatch.RoleVerifier, ptr64(1), briefInputs{})
	for _, heading := range []string{"Agent credentials", "Failure controls", "Correction allowances"} {
		if brief.part(heading) != nil {
			t.Errorf("Verifier receives system mechanics: %s", heading)
		}
	}
	if task := brief.part(partTask); task == nil || task.Text != "The export contains the requested date." || len(task.Facts) != 2 {
		t.Fatalf("Verifier lost its assignment: %+v", task)
	}
	text, err := renderRunBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"host provider proxy", "credential update", "The Scribe", "three unsuccessful corrections"} {
		if strings.Contains(text, removed) {
			t.Errorf("Verifier still receives %q", removed)
		}
	}
	for _, required := range []string{
		"~/.agents/skills/mc-verifier/SKILL.md",
		"before your first `mc` call and before the terminal",
		"Use only permitted `mc` commands with your own run identity",
		"A submitted broad check fails: Broad check failures.",
		"Read when a criterion or broad check needs browser interaction or visual confirmation.",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("Verifier cannot discover required procedure: %s", required)
		}
	}
	skills, err := RoleSkillSet(dispatch.RoleVerifier)
	if err != nil {
		t.Fatal(err)
	}
	profileText := text
	for _, skill := range skills {
		if skill.Name == "mc-verifier" || skill.Name == "verify-behavior" {
			profileText += skill.Body
		}
		path := installedSkillRoot + skill.Name + "/SKILL.md"
		if !strings.Contains(text, path) || skill.Body == "" {
			t.Errorf("delivered skill and pointer disagree: %s", path)
		}
	}
	if strings.Count(profileText, "agent profile") != 1 {
		t.Fatal("Verifier must receive the model and effort rule once")
	}
}

func TestVerifierBriefKeepsCasePayloads(t *testing.T) {
	for _, heading := range []string{
		partCustomization, partRun, partTask, "Project environment", partGitState,
		partRequirementsRevision, partRevision, partMergeCandidate, partCorrection,
		"Operator allowance explanation", partLastReport,
	} {
		original := briefPart{Heading: heading, Explanation: "Original explanation.",
			Facts:   []briefFact{{Label: "File", Value: "/mc/artifacts/plane/1/input.md"}},
			TextTag: "input", Text: "Preserve the requested behavior.", Rows: []string{"retained row"}}
		brief := runBrief{Parts: []briefPart{original}}
		trimVerifierBrief(&brief)
		if len(brief.Parts) != 1 {
			t.Fatalf("lost %s", heading)
		}
		got := brief.Parts[0]
		if got.Facts[0] != original.Facts[0] || got.Text != original.Text || got.TextTag != original.TextTag || got.Rows.([]string)[0] != "retained row" {
			t.Errorf("changed the payload of %s", heading)
		}
	}
}
