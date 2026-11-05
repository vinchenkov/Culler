package verbs

import (
	"strings"
	"testing"
)

func TestSetupAgentInputSeparatesProposalSummaryFromEvidence(t *testing.T) {
	op := environmentWork(t, environmentFixture(t))[0]
	for _, required := range []string{
		"Workspace dependencies",
		"Culler preserves the Project home at `HOME`",
		"The Project also shares `/cache`",
		"Its setup script prepares each fresh workspace",
		"Image dependencies",
		"at most two sentences and 25 words per sentence",
		"This outcome remains valid when every repository command remains unavailable",
		"assess each missing prerequisite before the final decision",
		"Use permitted probes and compatibility evidence",
		"State uncertainty and separate expected capabilities from verified capabilities",
		"The runner records the trace separately from the report",
		"After an initial image decision, submit `complete` with the assigned image",
	} {
		if !strings.Contains(op.Brief, required) {
			t.Errorf("The setup brief omits %q.", required)
		}
	}
	skills, err := skillSetByBasename("setup")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mc-setup", "project-environment-setup"} {
		found := false
		for _, skill := range skills {
			if skill.Name != name {
				continue
			}
			found = true
			requiredText := map[string][]string{
				"mc-setup":                  {"`proposal` or `complete`", "25 words per sentence", "`summary`", "`details`", "source acceptance"},
				"project-environment-setup": {"Workspace dependencies", "Image dependencies", "complete", "summary", "details"},
			}
			for _, required := range requiredText[name] {
				if !strings.Contains(skill.Body, required) {
					t.Errorf("The %s skill omits %q.", name, required)
				}
			}
		}
		if !found {
			t.Errorf("The setup input omits the %s skill.", name)
		}
	}
}
