package verbs

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"mc/dispatch"
)

// allSpawnRoles is the closed set every role-facing artifact must cover.
var allSpawnRoles = []dispatch.Role{
	dispatch.RoleStrategist,
	dispatch.RoleArbiter,
	dispatch.RoleWorker,
	dispatch.RoleVerifier,
	dispatch.RoleScribe,
	dispatch.RolePackager,
}

// TestEveryRoleShipsItsSkill is the artifact half of the anti-drift contract:
// a role handed prose that names a command, with no enumeration of what it may
// call, has no second source when that one sentence is wrong. `mc` has no help
// surface — bare `mc` prints "usage: mc <verb> …" and names zero verbs, and
// `mc --help` is an unknown verb — so this skill is the only verb surface an
// agent in the container can read.
func TestEveryRoleShipsItsSkill(t *testing.T) {
	for _, role := range allSpawnRoles {
		skill, err := RoleSkillFor(role)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		commands := ParseSkillCommands(skill.Body)
		if len(commands) == 0 {
			t.Errorf("%s skill enumerates no commands", role)
		}
		for _, command := range commands {
			if !strings.HasPrefix(command, "mc ") {
				t.Errorf("%s skill lists a non-mc command %q", role, command)
			}
		}
	}
}

// TestEverySkillCarriesItsTrigger pins the one part that is always resident.
// A skill's body loads on demand, but its description does not — it is what
// the harness lists and therefore the whole of what makes an agent reach for
// the skill at all. An empty or multi-line description is a skill that exists
// and is never opened.
func TestEverySkillCarriesItsTrigger(t *testing.T) {
	for _, role := range allSpawnRoles {
		skill, err := RoleSkillFor(role)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		description := SkillFrontmatterField(skill.Body, "description")
		if description == "" {
			t.Errorf("%s skill declares no description", role)
			continue
		}
		if strings.Contains(description, "\n") {
			t.Errorf("%s skill description is not a single line", role)
		}
	}
}

// TestRoleSkillSetIsTheCommandSurfaceThenTheAdditionalSkills pins the shape every
// consumer relies on: the set a spawn carries opens with the command surface,
// each further skill is a valid, described skill of its own, and no additional
// skill names an `mc` command the command surface omits — `mc` has exactly one
// place it can be corrected, for the same reason the prose does.
func TestRoleSkillSetIsTheCommandSurfaceThenTheAdditionalSkills(t *testing.T) {
	sets := map[string]func() ([]RoleSkill, error){}
	for _, role := range allSpawnRoles {
		role := role
		sets[string(role)] = func() ([]RoleSkill, error) { return RoleSkillSet(role) }
	}
	sets["collie"] = CollieSkillSet
	for tier, get := range sets {
		set, err := get()
		if err != nil {
			t.Fatalf("%s: %v", tier, err)
		}
		if len(set) == 0 || (tier == "collie" && set[0].Name != "culler") || (tier != "collie" && !strings.HasPrefix(set[0].Name, "mc-")) {
			t.Fatalf("%s set does not open with its command surface: %v", tier, set)
		}
		command := set[0]
		seen := map[string]bool{command.Name: true}
		for _, skill := range set[1:] {
			if seen[skill.Name] {
				t.Errorf("%s ships %q twice", tier, skill.Name)
			}
			seen[skill.Name] = true
			if strings.HasPrefix(skill.Name, "mc-") {
				t.Errorf("%s additional skill %q takes the command surface prefix", tier, skill.Name)
			}
			if description := skill.Description(); description == "" || strings.Contains(description, "\n") {
				t.Errorf("%s additional skill %q has no single-line description", tier, skill.Name)
			}
			for _, invocation := range proseVerbInvocations(skill.Body) {
				if tier != "collie" && strings.HasPrefix(invocation, "mc") && !strings.Contains(command.Body, invocation) {
					t.Errorf("%s additional skill %q names %q, which the command surface omits", tier, skill.Name, invocation)
				}
			}
		}
	}
}

// TestVerifierShipsVerifyBehavior keeps the verify-behavior skill in the
// Verifier set.
func TestVerifierShipsVerifyBehavior(t *testing.T) {
	set, err := RoleSkillSet(dispatch.RoleVerifier)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, skill := range set {
		names = append(names, skill.Name)
	}
	if strings.Join(names, ",") != "mc-verifier,verify-behavior,project-environment-setup,project-wiki,task-description,writing" {
		t.Fatalf("verifier set = %v", names)
	}
}

// TestEveryTierShipsTheWritingSkill pins the one shared skill: every pipeline
// role and the Collie tier close their set with `writing`, the standard for
// each text a human or another agent reads.
func TestEveryTierShipsTheWritingSkill(t *testing.T) {
	sets := map[string]func() ([]RoleSkill, error){"collie": CollieSkillSet}
	for _, role := range allSpawnRoles {
		role := role
		sets[string(role)] = func() ([]RoleSkill, error) { return RoleSkillSet(role) }
	}
	for tier, get := range sets {
		set, err := get()
		if err != nil {
			t.Fatalf("%s: %v", tier, err)
		}
		if last := set[len(set)-1]; last.Name != "writing" {
			t.Errorf("%s set does not close with the writing skill: %q", tier, last.Name)
		}
	}
}

// TestNoTwoRolesShareASkill checks that each role has a distinct skill name.
// The container tests also verify that each run receives only its role's skills.
func TestNoTwoRolesShareASkill(t *testing.T) {
	names := map[string]dispatch.Role{}
	bodies := map[string]dispatch.Role{}
	for _, role := range allSpawnRoles {
		skill, err := RoleSkillFor(role)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if other, seen := names[skill.Name]; seen {
			t.Errorf("%s and %s share the skill name %q", other, role, skill.Name)
		}
		if other, seen := bodies[skill.Body]; seen {
			t.Errorf("%s and %s ship an identical skill body", other, role)
		}
		names[skill.Name] = role
		bodies[skill.Body] = role
	}
}

func TestCollieSkillReferencesResolveInDeliveredTree(t *testing.T) {
	skill, err := CollieSkill()
	if err != nil {
		t.Fatal(err)
	}
	if skill.Name != "culler" {
		t.Fatalf("skill name = %q", skill.Name)
	}
	links := regexp.MustCompile(`\]\((references/[^)]+)\)`)
	for _, match := range links.FindAllStringSubmatch(skill.Body, -1) {
		if skill.Files[match[1]] == "" {
			t.Errorf("reference %q is absent", match[1])
		}
	}
	for _, name := range []string{"writing", "task-description"} {
		source, err := skillByPath("shared", name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(skill.Files["references/"+name+".md"]) != strings.TrimSpace(source.Body) {
			t.Errorf("%s reference differs from its authoritative source", name)
		}
	}
	if len(ParseSkillCommands(skill.Body)) != 0 {
		t.Fatal("the common skill duplicates the command catalog")
	}
	effect := SkillsEffectValue([]RoleSkill{skill})
	if len(effect[0]["files"].(map[string]string)) != len(skill.Files) {
		t.Fatal("the launch lost skill files")
	}
}

// TestUnknownRoleHasNoFallbackSkill mirrors the directive rule: a role the
// product does not know must not silently inherit another role's surface.
func TestUnknownRoleHasNoFallbackSkill(t *testing.T) {
	if _, err := RoleSkillFor(dispatch.Role("unknown")); err == nil {
		t.Fatal("unknown role received a fallback skill")
	}
	if _, err := RoleSkillName(dispatch.Role("unknown")); err == nil {
		t.Fatal("unknown role received a fallback skill name")
	}
}

// TestDirectiveNamesItsSkillWithoutCarryingIt pins both halves of the delivery
// choice. The directive must NAME the skill — an agent that never learns it
// exists never loads it, and the frozen prose is the only text guaranteed to
// reach the model. It must not CONTAIN it: inlining the body would
// spend the context the skill exists to save, and would restore the drift risk
// of a second copy nobody updates.
func TestDirectiveNamesItsSkillWithoutCarryingIt(t *testing.T) {
	for _, role := range allSpawnRoles {
		directive, err := directiveForRole(role)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		set, err := RoleSkillSet(role)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		for _, skill := range set {
			if !strings.Contains(directive, skill.Name) {
				t.Errorf("%s directive never names its skill %q", role, skill.Name)
			}
			// The path the directive advertises is the fallback when the
			// harness's own discovery does not fire, so it must be the root the
			// agent runner installs into (runner/agent-runner/skills.ts).
			if !strings.Contains(directive, "~/.agents/skills/"+skill.Name+"/SKILL.md") {
				t.Errorf("%s directive names no readable path for skill %q", role, skill.Name)
			}
			// Not command-by-command: a frozen directive may name its own
			// terminal in prose, and TestProseNamesNoCommandTheSkillOmits
			// already holds that direction. What must not happen is the whole
			// surface riding along.
			if strings.Contains(directive, strings.TrimSpace(skill.Body)) {
				t.Errorf("%s directive inlines the whole body of %q instead of pointing at it", role, skill.Name)
			}
		}
	}
}

// TestProseNamesNoCommandTheSkillOmits is the drift catcher aimed at the exact
// failure observed live: the Worker's prose named `mc complete … --status
// worked`, which the CLI refuses for every assigned task, and no other command
// was on offer. Prose and skills must not be able to disagree — every `mc …`
// span in the frozen prose must appear in the command surface. Each `mc` flag
// must also appear there. Another tool can live in the directive itself.
func TestProseNamesNoCommandTheSkillOmits(t *testing.T) {
	for _, role := range allSpawnRoles {
		prose, err := roleProseDirective(role)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		set, err := RoleSkillSet(role)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		var bodies []string
		for _, skill := range set {
			bodies = append(bodies, skill.Body)
		}
		for _, invocation := range proseVerbInvocations(prose) {
			if strings.HasPrefix(invocation, "mc") {
				if !strings.Contains(set[0].Body, invocation) {
					t.Errorf("%s prose names %q, which its command surface omits", role, invocation)
				}
				continue
			}
			if !strings.Contains(strings.Join(bodies, "\n"), invocation) {
				t.Errorf("%s prose names %q, which none of its skills states", role, invocation)
			}
		}
	}
}

func TestProseVerbInvocationsKeepsOtherToolFlagsSeparate(t *testing.T) {
	prose := "Use `playwright-cli --browser=chromium open` before `mc complete <task> --run <run_id>`."
	got := strings.Join(proseVerbInvocations(prose), ",")
	if got != "mc complete,--run" {
		t.Fatalf("invocations = %q", got)
	}
}

// TestScribeShipsItsSkillSet keeps the skill set of the Scribe.
func TestScribeShipsItsSkillSet(t *testing.T) {
	set, err := RoleSkillSet(dispatch.RoleScribe)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, skill := range set {
		names = append(names, skill.Name)
	}
	if strings.Join(names, ",") != "mc-scribe,openwiki,openwiki-documents,openwiki-recovery,project-environment-setup,project-wiki,task-description,writing" {
		t.Fatalf("scribe set = %v", names)
	}
}

// TestEveryTierShipsTheProjectWikiSkill keeps the shared project-wiki skill in
// the set of each tier.
func TestEveryTierShipsTheProjectWikiSkill(t *testing.T) {
	sets := map[string]func() ([]RoleSkill, error){"collie": CollieSkillSet}
	for _, role := range allSpawnRoles {
		role := role
		sets[string(role)] = func() ([]RoleSkill, error) { return RoleSkillSet(role) }
	}
	for tier, get := range sets {
		set, err := get()
		if err != nil {
			t.Fatalf("%s: %v", tier, err)
		}
		found := false
		for _, skill := range set {
			found = found || skill.Name == "project-wiki"
		}
		if !found {
			t.Errorf("%s set holds no project-wiki skill", tier)
		}
	}
}

func TestScribeSkillPointersResolveInTheDeliveredSet(t *testing.T) {
	set, err := RoleSkillSet(dispatch.RoleScribe)
	if err != nil {
		t.Fatal(err)
	}
	directive, err := directiveForRole(dispatch.RoleScribe)
	if err != nil {
		t.Fatal(err)
	}
	bodies := map[string]string{"directive": directive}
	for _, skill := range set {
		bodies[skill.Name] = skill.Body
	}
	pointer := regexp.MustCompile(`~/.agents/skills/([a-z-]+)/SKILL.md`)
	for source, body := range bodies {
		for _, match := range pointer.FindAllStringSubmatch(body, -1) {
			if _, ok := bodies[match[1]]; !ok {
				t.Errorf("%s points to an absent delivered skill: %s", source, match[0])
			}
		}
	}
}

// TestTaskDescriptionSkillIsTheOneStandard keeps one source for the standard of
// a task description. The Strategist, the Arbiter, and Collie ship the skill.
// No other directive or skill restates its rules.
func TestTaskDescriptionSkillIsTheOneStandard(t *testing.T) {
	sets := map[string]func() ([]RoleSkill, error){
		"strategist": func() ([]RoleSkill, error) { return RoleSkillSet(dispatch.RoleStrategist) },
		"arbiter":    func() ([]RoleSkill, error) { return RoleSkillSet(dispatch.RoleArbiter) },
		"collie":     CollieSkillSet,
	}
	const name = "task-description"
	var standard string
	for tier, get := range sets {
		set, err := get()
		if err != nil {
			t.Fatalf("%s: %v", tier, err)
		}
		found := false
		for _, skill := range set {
			if skill.Name == name {
				found, standard = true, skill.Body
			}
		}
		if !found {
			t.Errorf("%s does not ship the %s skill", tier, name)
		}
	}
	if standard == "" {
		t.Fatal("no tier ships the task-description skill")
	}
	if invocations := proseVerbInvocations(standard); len(invocations) != 0 {
		t.Errorf("the task-description skill names commands: %v", invocations)
	}
	others := map[string]string{
		"strategist directive": strategistDirective, "arbiter directive": arbiterDirective,
		"worker directive": workerDirective, "verifier directive": verifierDirective,
		"scribe directive": scribeDirective, "packager directive": packagerDirective,
		"collie directive": collieDirective,
	}
	err := fs.WalkDir(roleSkillFS, "skills", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || path == "skills/shared/task-description/SKILL.md" {
			return err
		}
		body, err := roleSkillFS.ReadFile(path)
		others[path] = string(body)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(standard, "\n") {
		rule, ok := strings.CutPrefix(strings.TrimSpace(line), "- ")
		if !ok {
			continue
		}
		for name, text := range others {
			if strings.Contains(strings.Join(strings.Fields(text), " "), rule) {
				t.Errorf("%s restates the task-description rule %q", name, rule)
			}
		}
	}
}

func TestCollieDirectiveUsesTheCommonAuthorizationSource(t *testing.T) {
	skill, err := CollieSkill()
	if err != nil {
		t.Fatal(err)
	}
	_, rest, found := strings.Cut(skill.Body, "## Authorization\n")
	policy, _, closed := strings.Cut(rest, "\n## Procedures\n")
	if !found || !closed {
		t.Fatal("the common authorization is absent")
	}
	if strings.Count(collieDirective, strings.TrimSpace(policy)) != 1 {
		t.Fatal("the directive must receive the common authorization exactly once")
	}
	if strings.Contains(collieDirectiveTemplate, strings.TrimSpace(policy)) {
		t.Fatal("the directive template duplicates the common authorization")
	}
}
