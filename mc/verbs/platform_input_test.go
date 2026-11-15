package verbs

import (
	"testing"

	"mc/dispatch"
)

func TestPlatformProceduresRemainAccessibleInRenderedInput(t *testing.T) {
	for _, role := range []dispatch.Role{dispatch.RoleWorker, dispatch.RoleVerifier} {
		t.Run(string(role), func(t *testing.T) {
			db := dvSpine(t)
			dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
			brief := briefFor(t, db, role, ptr64(1), briefInputs{})
			text, err := renderRunBrief(brief)
			if err != nil {
				t.Fatal(err)
			}
			requireAgentText(t, string(role)+" rendered input", text, []string{
				installedSkillRoot + "mc-" + string(role) + "/SKILL.md",
				installedSkillRoot + "project-environment-setup/SKILL.md",
				"If this skill is absent or unreadable, report the failed read with this terminal:",
				"`mc complete <task> --run <run_id> --infra --reason <observed condition>`",
				"Use this identifier as `<run_id>` in command forms that require it.",
				"Use the Identifier as `<task>` in command forms.",
			}, nil)
		})
	}
}
