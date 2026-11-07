package verbs

import (
	"strings"
	"testing"

	"mc/dispatch"
)

func TestPlatformProceduresRemainAccessibleInRenderedInput(t *testing.T) {
	for _, tc := range []struct {
		role      dispatch.Role
		procedure string
	}{
		{dispatch.RoleWorker, "For an unavailable action or check, read Unavailable actions and checks in that skill."},
		{dispatch.RoleVerifier, "A required check cannot run: Unavailable checks."},
	} {
		t.Run(string(tc.role), func(t *testing.T) {
			db := dvSpine(t)
			dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
			brief := briefFor(t, db, tc.role, ptr64(1), briefInputs{})
			text, err := renderRunBrief(brief)
			if err != nil {
				t.Fatal(err)
			}
			requireAgentText(t, string(tc.role)+" rendered input", text, []string{
				tc.procedure,
				installedSkillRoot + "mc-" + string(tc.role) + "/SKILL.md",
				installedSkillRoot + "project-environment-setup/SKILL.md",
				"If this skill is absent or unreadable, report the failed read with this terminal:",
				"`mc complete <task> --run <run_id> --infra --reason <observed condition>`",
				"Use this identifier as `<run_id>` in command forms that require it.",
				"Use the Identifier as `<task>` in command forms.",
			}, nil)
		})
	}
}

func TestPlatformOutcomesPreserveRequiredTestsAndIndependentWork(t *testing.T) {
	for _, tc := range []struct {
		role dispatch.Role
		want []string
	}{
		{dispatch.RoleWorker, []string{
			"Use the project-environment-setup skill to assess each unavailable prerequisite.",
			"Fix source, tests, Workspace dependencies, or setup script defects within the task's authority.",
			"For a prerequisite that Image dependencies can supply, follow Project dependencies and submit a checkpoint.",
			"For a criterion or new or changed test, use the operator decision terminal when every agent container lacks its capability.",
			"State the required capability, the affected requirement, and the evidence in an operator decision report.",
			"An unavailable host check outside the criteria requires no operator decision or task run failure by itself.",
			"Continue independent work and checks that the container supports.",
			"Record actual command results and every skipped or unavailable check, including each `UNVERIFIED` entry, in the completion report.",
			"For an unavailable requirement, first apply Unavailable actions and checks.",
		}},
		{dispatch.RoleVerifier, []string{
			"For a criterion check or a new or changed test, select the outcome by its cause:",
			"Use CORRECT for source, tests, Workspace dependencies, or setup script defects that a Worker can change.",
			"Use an operator decision report for a capability that no agent container has.",
			"Use CORRECT when the cause remains unclear.",
			"For an Image dependency that can supply the prerequisite, use CORRECT so the Worker can propose the image change.",
			"Unavailable host tests outside the criteria permit PASS when the other requirements hold.",
			"Run later independent checks separately when an earlier command stops a script or command chain.",
			"An unchanged failure explains only the reproduced failure, not checks that it prevented from executing.",
			"Each skipped, unavailable, or incomplete check, marked unverified with its reason, including every `UNVERIFIED` entry.",
		}},
	} {
		t.Run(string(tc.role), func(t *testing.T) {
			name := "mc-" + string(tc.role)
			body := roleSkillBody(t, tc.role, name)
			want := append(tc.want,
				"This exception excludes new or changed tests, which must pass.",
				"Separate release and host acceptance requirements retain their authority.",
				"mc complete <task> --run <run_id> --needs-operator --reason <prose>",
			)
			requireAgentText(t, name, body, want, nil)
		})
	}
}

func TestPlatformLimitsAndEvidenceReachEveryRequiredRole(t *testing.T) {
	for _, role := range []dispatch.Role{dispatch.RoleWorker, dispatch.RoleVerifier} {
		t.Run(string(role), func(t *testing.T) {
			body := roleSkillBody(t, role, "project-environment-setup")
			requireAgentText(t, string(role)+" environment skill", body, []string{
				"Culler runs Project code in a Linux container inside a Linux virtual machine on the Mac host.",
				"The container has no host command executor or access to the host Docker daemon.",
				"Propose Image dependencies only when image contents can resolve a required prerequisite.",
				"Image additions cannot supply macOS system interfaces, host access, or additional container privileges.",
				"A Docker client alone provides no Docker daemon.",
				"Continue independent checks after a failure.",
				"An unexecuted command remains unverified.",
				"Later task verification retains the repository's test requirements.",
			}, nil)
		})
	}
	packager, err := directiveForRole(dispatch.RolePackager)
	if err != nil {
		t.Fatal(err)
	}
	requireAgentText(t, "Packager directive", strings.Join(strings.Fields(packager), " "), []string{
		"Preserve each `UNVERIFIED` entry from the Verifier's evidence.",
	}, nil)
	requireAgentText(t, "mc-packager", roleSkillBody(t, dispatch.RolePackager, "mc-packager"), []string{
		"Each `UNVERIFIED` entry and the coverage that remains unverified.",
		"Keep actual command results separate from the task verdict.",
		"These observations alone create no mandatory manual check or additional approval requirement.",
	}, nil)
}
