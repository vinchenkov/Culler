package verbs

import (
	_ "embed"
	"fmt"
	"strings"

	"mc/dispatch"
)

// These are product-owned, frozen directives, embedded into the mc
// binary so installation cannot accidentally omit or locally reinterpret
// them. Changing one is a reviewed product change, not operator config.

//go:embed directives/strategist.md
var strategistDirective string

//go:embed directives/arbiter.md
var arbiterDirective string

//go:embed directives/worker.md
var workerDirective string

//go:embed directives/verifier.md
var verifierDirective string

//go:embed directives/scribe.md
var scribeDirective string

//go:embed directives/packager.md
var packagerDirective string

//go:embed directives/collie.md
var collieDirective string

// directiveForRole assembles what the model is actually handed: the frozen
// prose plus one line naming the role's command contract. The contract itself
// is a skill installed in the run's container, not text inlined here — only
// its one-line description stays resident, and the body loads when the agent
// reaches for it.
//
// The pointer is not redundant with the harness's own skill listing. It is the
// trigger (an agent that never learns the skill exists never loads it) and the
// fallback: the skill is a readable file at a fixed path, so a harness that
// fails to surface it costs a Read rather than the run.
func directiveForRole(role dispatch.Role) (string, error) {
	prose, err := roleProseDirective(role)
	if err != nil {
		return "", err
	}
	skills, err := RoleSkillSet(role)
	if err != nil {
		return "", err
	}
	return prose + "\n\n" + skillPointer(skills), nil
}

// installedSkillRoot is the container-side root the agent runner installs
// skills into (runner/agent-runner/skills.ts). Pipeline skill pointers use it.
const installedSkillRoot = "~/.agents/skills/"

// skillPointer names every shipped skill the run carries: the command surface
// first, then the role's additional skills with their descriptions. The body of a
// skill is never inlined; the pointer is what makes an agent reach for it.
func skillPointer(skills []RoleSkill) string {
	command := skills[0]
	text := "## Your command surface\n\n" +
		"Every `mc` command available to you, with its exact flags and constraints, is the " +
		command.Name + " skill, installed in this container at `" + installedSkillRoot + command.Name + "/SKILL.md`. " +
		"Load it before your first `mc` call and again before you submit your terminal.\n\n" +
		"`mc` ships no help surface: bare `mc` names zero verbs and `mc --help` is an unknown " +
		"verb. That skill is the complete surface your role may call. A verb absent from it " +
		"does not exist for you, and a search for one wastes the run."
	if command.Name == "mc-worker" || command.Name == "mc-scribe" || command.Name == "mc-verifier" {
		help := "The skill lists the permitted commands; `mc` has no help command."
		if command.Name == "mc-scribe" {
			help = "The skill lists the permitted commands.\n`mc` has no help command."
		}
		text = "## Your command surface\n\n" +
			"Read `" + installedSkillRoot + command.Name + "/SKILL.md` before your first `mc` call and before the terminal.\n" +
			help
		if command.Name == "mc-worker" || command.Name == "mc-verifier" {
			text += "\nIf this skill is absent or unreadable, report the failed read with this terminal:\n\n" +
				"`mc complete <task> --run <run_id> --infra --reason <observed condition>`."
		}
	}
	if command.Name == "mc-strategist" {
		text = "## Your command surface\n\n" +
			"Read `" + installedSkillRoot + command.Name + "/SKILL.md` before each `mc` call.\n" +
			"The skill lists the permitted commands; `mc` has no help command."
	}

	if command.Name == "mc-arbiter" {
		text = "## Your command surface\n\n" +
			"Read `" + installedSkillRoot + command.Name + "/SKILL.md` before your first `mc` call and before the terminal.\n" +
			"The skill lists the permitted commands; `mc` has no help command."
	}

	if len(skills) == 1 {
		return text
	}
	otherSkills := "Each skill below is installed in this container under `" + installedSkillRoot + "`. " +
		"Load a skill when its description applies to the work in front of you.\n"
	if command.Name == "mc-scribe" {
		otherSkills = "Read each skill when its description applies.\n"
	}
	text += "\n\n## Your other skills\n\n" + otherSkills
	for _, skill := range skills[1:] {
		text += "\n- " + skill.Name + " (`" + installedSkillRoot + skill.Name + "/SKILL.md`): " + skill.Description()
	}
	return text
}

func roleProseDirective(role dispatch.Role) (string, error) {
	directives := map[dispatch.Role]string{
		dispatch.RoleStrategist: strategistDirective,
		dispatch.RoleArbiter:    arbiterDirective,
		dispatch.RoleWorker:     workerDirective,
		dispatch.RoleVerifier:   verifierDirective,
		dispatch.RoleScribe:     scribeDirective,
		dispatch.RolePackager:   packagerDirective,
	}
	directive, ok := directives[role]
	if !ok {
		return "", fmt.Errorf("no frozen directive for role %q", role)
	}
	return strings.TrimSpace(directive), nil
}

func roleContract(role dispatch.Role) string {
	prose, err := roleProseDirective(role)
	if err != nil {
		return ""
	}
	_, tail, _ := strings.Cut(prose, "## Culler contract\n")
	contract, _, _ := strings.Cut(tail, "## Project role customization")
	return strings.TrimSpace(contract)
}
