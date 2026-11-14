package verbs

import (
	"regexp"
	"strings"
	"testing"

	"mc/dispatch"
)

// scribeRoleSkills are the role-specific skills of the Scribe.
var scribeRoleSkills = []string{"mc-scribe", "openwiki", "openwiki-documents", "openwiki-recovery"}

// scribeSkills returns the body of each role-specific skill that the Scribe
// of a wiki update receives.
func scribeSkills(t *testing.T) map[string]string {
	t.Helper()
	set, err := RoleSkillSet(dispatch.RoleScribe)
	if err != nil {
		t.Fatal(err)
	}
	skills := map[string]string{}
	for _, skill := range set {
		for _, name := range scribeRoleSkills {
			if skill.Name == name {
				skills[name] = skill.Body
			}
		}
	}
	if len(skills) != len(scribeRoleSkills) {
		t.Fatalf("the Scribe receives the role-specific skills %v, want %v", skills, scribeRoleSkills)
	}
	return skills
}

func flat(text string) string { return strings.Join(strings.Fields(text), " ") }

// Each section reference of the Scribe names an existing heading of its target.
func TestScribeSectionReferencesResolve(t *testing.T) {
	prose, err := roleProseDirective(dispatch.RoleScribe)
	if err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{"directive": prose}
	for name, body := range scribeSkills(t) {
		texts[name] = body
	}
	reference := regexp.MustCompile("\"([^\"]+)\" section(?: of (the directive|`~/.agents/skills/([a-z-]+)/SKILL.md`))?")
	for source, text := range texts {
		for _, match := range reference.FindAllStringSubmatch(flat(text), -1) {
			target := source
			if match[3] != "" {
				target = match[3]
			} else if match[2] != "" {
				target = "directive"
			}
			if !strings.Contains(texts[target], "## "+match[1]+"\n") {
				t.Errorf("the %s names the %q section, and the %s has no such heading", source, match[1], target)
			}
		}
	}
}

// The Scribe directive points to each role-specific skill with the skill's
// own description, and each command surface pointer names its terminal.
func TestScribeSkillPointersCarryTheSkillDescriptions(t *testing.T) {
	directive, err := directiveForRole(dispatch.RoleScribe)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range scribeSkills(t) {
		if name == "mc-scribe" {
			continue
		}
		pointer := "- " + name + " (`" + installedSkillRoot + name + "/SKILL.md`): " + SkillFrontmatterField(body, "description")
		if !strings.Contains(directive, pointer) {
			t.Errorf("the Scribe directive omits the skill pointer %q", pointer)
		}
	}
	// The Scribe pointer uses the term of the Scribe directive. The common
	// generator keeps the pointer of each other role.
	for role, pointer := range map[dispatch.Role]string{
		dispatch.RoleScribe:   "/SKILL.md` before your first `mc` call and before the terminal action.\n",
		dispatch.RoleWorker:   "/SKILL.md` before your first `mc` call and before the terminal.\n",
		dispatch.RoleVerifier: "/SKILL.md` before your first `mc` call and before the terminal.\n",
		dispatch.RoleArbiter:  "/SKILL.md` before your first `mc` call and before the terminal.\n",
	} {
		text, err := directiveForRole(role)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, pointer) {
			t.Errorf("the %s command surface pointer omits %q", role, pointer)
		}
	}
}
