package verbs

import (
	"io/fs"
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
	sets["homie"] = HomieSkillSet
	for tier, get := range sets {
		set, err := get()
		if err != nil {
			t.Fatalf("%s: %v", tier, err)
		}
		if len(set) == 0 || !strings.HasPrefix(set[0].Name, "mc-") {
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
				if strings.HasPrefix(invocation, "mc") && !strings.Contains(command.Body, invocation) {
					t.Errorf("%s additional skill %q names %q, which the command surface omits", tier, skill.Name, invocation)
				}
			}
		}
	}
}

// TestVerifierShipsVerifyBehavior keeps browser verification available when a
// user interface change requires it.
func TestVerifierShipsVerifyBehavior(t *testing.T) {
	set, err := RoleSkillSet(dispatch.RoleVerifier)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, skill := range set {
		names = append(names, skill.Name)
	}
	if strings.Join(names, ",") != "mc-verifier,verify-behavior,project-environment-setup,project-wiki,writing" {
		t.Fatalf("verifier set = %v", names)
	}
	for _, want := range []string{
		"playwright-cli", "--browser=chromium", "snapshot", "screenshot",
		"video-start", "console warning", "delete-data", "Do not use this skill",
	} {
		if !strings.Contains(set[1].Body, want) {
			t.Errorf("verify-behavior never states %q", want)
		}
	}
}

// TestPackagerShipsNoRoleSkill keeps the review packet instructions in the
// Packager directive: the set is the command surface and the shared skills.
func TestPackagerShipsNoRoleSkill(t *testing.T) {
	set, err := RoleSkillSet(dispatch.RolePackager)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, skill := range set {
		names = append(names, skill.Name)
	}
	if strings.Join(names, ",") != "mc-packager,project-environment-setup,project-wiki,writing" {
		t.Fatalf("packager set = %v", names)
	}
}

// TestEveryTierShipsTheWritingSkill pins the one shared skill: every pipeline
// role and the Homie tier close their set with `writing`, the standard for
// each text a human or another agent reads.
func TestEveryTierShipsTheWritingSkill(t *testing.T) {
	sets := map[string]func() ([]RoleSkill, error){"homie": HomieSkillSet}
	for _, role := range allSpawnRoles {
		role := role
		sets[string(role)] = func() ([]RoleSkill, error) { return RoleSkillSet(role) }
	}
	for tier, get := range sets {
		set, err := get()
		if err != nil {
			t.Fatalf("%s: %v", tier, err)
		}
		last := set[len(set)-1]
		if last.Name != "writing" {
			t.Errorf("%s set does not close with the writing skill: %q", tier, last.Name)
			continue
		}
		for _, want := range []string{
			"Never invent language", "Simplified Technical English",
			"declarative style", "a commit message", "a reply to the operator",
		} {
			if !strings.Contains(last.Body, want) {
				t.Errorf("%s writing skill never states %q", tier, want)
			}
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

// TestHomieSkillEnumeratesExactlyItsAllowlist judges the Homie contract by the
// authority that actually gates it: maximumHomieAgentVerbs, the compiled-in set
// `mc homie start` freezes onto a session and the agent-scoped `mc` capability
// enforces. Not by the file's own declaration.
//
// Both directions matter. A verb the allowlist grants and the skill omits is a
// capability the operator's surface never learns it has; a verb the skill names
// and the allowlist withholds is a call refused by scope, which is the exact
// waste this artifact exists to prevent. The narrowed-session case does not
// weaken this: `--allow` can only ever select a SUBSET of the same set.
func TestHomieSkillEnumeratesExactlyItsAllowlist(t *testing.T) {
	skill, err := HomieSkill()
	if err != nil {
		t.Fatalf("homie: %v", err)
	}
	enumerated := map[string]bool{}
	for _, command := range ParseSkillCommands(skill.Body) {
		fields := strings.Fields(command)
		// "mc" plus verb plus optional subverb; the allowlist's own grammar is
		// dotted ("packet.decide"), so normalize to that.
		if len(fields) < 2 || fields[0] != "mc" {
			t.Errorf("homie skill lists a non-mc command %q", command)
			continue
		}
		head := fields[1]
		if len(fields) > 2 && !strings.HasPrefix(fields[2], "-") && !strings.HasPrefix(fields[2], "<") {
			head += "." + fields[2]
		}
		enumerated[head] = true
	}
	// The runner, not the model, calls these; the brief keeps them out of
	// the model's vocabulary.
	runnerOwned := map[string]bool{"homie.draft": true}
	granted := map[string]bool{}
	for _, verb := range maximumHomieAgentVerbs {
		granted[verb] = true
		if !enumerated[verb] && !runnerOwned[verb] {
			t.Errorf("homie allowlist grants %q, which the skill never enumerates", verb)
		}
	}
	// Reads are ungated and correctly absent from the allowlist, so only the
	// write-shaped heads are held to it.
	reads := map[string]bool{
		"task.get": true, "task.list": true, "packet.list": true,
		"activity.list": true, "project.list": true,
		"project.role-show": true, "run.customization": true, "run.list": true,
	}
	for head := range enumerated {
		if !granted[head] && !reads[head] {
			t.Errorf("homie skill names %q, which is neither on its allowlist nor an ungated read", head)
		}
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

// TestEveryDirectiveForbidsASilentExit pins the instruction added after task 8
// blocked live: a Verifier ended three turns with NO terminal action — twice on
// `task_complete`, once `turn_aborted` — after a Codex subagent hit a provider
// stream disconnect inside the fan-out. The lease rules recover exactly that,
// and they did; the defect is that nothing ever gets better, so
// `dispatch_retries` drained and a sound task blocked with no diagnosis on it.
//
// The lever is the frozen prose, because the runner deliberately never converts
// an ordinary harness exit into success and never infers a terminal from the
// model's final text. It is asserted here rather than trusted because
// every role carries its own copy. Each copy must require a terminal.
func TestEveryDirectiveForbidsASilentExit(t *testing.T) {
	for _, role := range allSpawnRoles {
		directive, err := directiveForRole(role)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		// Each directive states the rule in its own words; one of these
		// sentences must be present.
		forbids := false
		for _, sentence := range []string{
			"Never end your turn without a terminal action",
			"Always end your turn with a terminal action",
			"Every turn must end with the terminal action",
		} {
			if strings.Contains(directive, sentence) {
				forbids = true
				break
			}
		}
		if !forbids {
			t.Errorf("%s directive does not forbid ending the turn without a terminal", role)
			continue
		}
		// A subject-bearing role must also be told WHICH terminal covers the
		// failure, or the instruction is an exhortation with no arm behind it.
		// The Strategist has no task subject for --infra:
		// its reap charges nothing and simply re-fires, which is that
		// tier's own answer to the same concern.
		subjectless := role == dispatch.RoleStrategist
		if subjectless {
			continue
		}
		if !strings.Contains(directive, "--infra") {
			t.Errorf("%s directive forbids a silent exit but names no terminal for a failed run", role)
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

// The steps of a task are the Worker, the Verifier, the Packager, and the
// review. The Scribe has no task: it updates the Project wiki on a schedule.
// Each directive states this model.
func TestEachDirectiveStatesTheWikiSchedule(t *testing.T) {
	for _, role := range allSpawnRoles {
		prose, err := roleProseDirective(role)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		flat := strings.Join(strings.Fields(prose), " ")
		for _, want := range []string{
			"The Worker builds each task. The Verifier judges the result. The Packager writes the review packet that the operator approves or rejects.",
			"The Scribe has no task. It updates the Project wiki on a schedule.",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("%s directive omits %q", role, want)
			}
		}
		if strings.Contains(flat, "The Scribe updates the project wiki.") {
			t.Errorf("%s directive states the Scribe as a step of a task", role)
		}
	}
	homie := strings.Join(strings.Fields(homieDirective), " ")
	for _, want := range []string{
		"The Scribe updates the Project wiki on a schedule. It has no task.",
		"Culler merges its wiki commit with no review.",
	} {
		if !strings.Contains(homie, want) {
			t.Errorf("homie directive omits %q", want)
		}
	}
}

// A wiki update run has no task. The directive and the skills that the Scribe
// receives can name no task argument, no task report, and no task read.
func TestScribeInputNamesNoTask(t *testing.T) {
	directive, err := directiveForRole(dispatch.RoleScribe)
	if err != nil {
		t.Fatal(err)
	}
	set, err := RoleSkillSet(dispatch.RoleScribe)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	texts := map[string]string{"directive": directive}
	for _, skill := range set {
		names = append(names, skill.Name)
		texts["skill "+skill.Name] = skill.Body
	}
	if strings.Join(names, ",") != "mc-scribe,openwiki,project-environment-setup,project-wiki,writing" {
		t.Fatalf("scribe set = %v", names)
	}
	for surface, text := range texts {
		for _, banned := range []string{"<task>", "--outputs", "--needs-operator", "mc task get"} {
			if strings.Contains(text, banned) {
				t.Errorf("the Scribe %s names %q", surface, banned)
			}
		}
	}
}

// Each tier ships the shared wiki skill. The skill states who maintains the
// generated files of the Project wiki, and that those files can be behind the
// accepted commit.
func TestSharedWikiSkillStatesTheSchedule(t *testing.T) {
	sets := map[string]func() ([]RoleSkill, error){"homie": HomieSkillSet}
	for _, role := range allSpawnRoles {
		role := role
		sets[string(role)] = func() ([]RoleSkill, error) { return RoleSkillSet(role) }
	}
	for tier, get := range sets {
		set, err := get()
		if err != nil {
			t.Fatalf("%s: %v", tier, err)
		}
		body := ""
		for _, skill := range set {
			if skill.Name == "project-wiki" {
				body = strings.Join(strings.Fields(skill.Body), " ")
			}
		}
		if body == "" {
			t.Errorf("%s set holds no project-wiki skill", tier)
			continue
		}
		for _, want := range []string{
			"The Scribe maintains the control records, the Claim sidecars, the generated pages, and the navigation pages.",
			"The Scribe does this work on a schedule.",
			"A generated page or a navigation page can be behind the accepted commit until the next wiki update.",
			"Current repository evidence takes precedence over a generated page.",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s project-wiki skill omits %q", tier, want)
			}
		}
	}
}

// Each Homie container masks the artifact files of each task. The shared wiki
// skill limits the run envelope of artifact planes to pipeline runs and
// states the artifact access of a packet conversation, as the Homie directive
// does.
func TestSharedWikiSkillStatesTheHomieArtifactAccess(t *testing.T) {
	set, err := HomieSkillSet()
	if err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, skill := range set {
		if skill.Name == "project-wiki" {
			body = strings.Join(strings.Fields(skill.Body), " ")
		}
	}
	for _, want := range []string{
		"The run envelope of a pipeline run identifies each mounted artifact plane under `/mc/artifacts/`.",
		"A Homie container shows an empty `.mc-artifacts/` folder in each Project mount.",
		"A packet conversation reads the artifact files of its task only at the paths in its packet context.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the Homie project-wiki skill omits %q", want)
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(homieDirective), " "),
		"A packet conversation can read the artifact files of its task at the paths in its packet context.") {
		t.Error("the Homie directive omits the artifact access of a packet conversation")
	}
}

// The submitted commit of a task is the commit that the Verifier passed, and
// the run brief holds no Scribe commit of a task. No directive and no skill
// can send an agent to such a fact.
func TestAgentInputNamesNoScribeCommitOfATask(t *testing.T) {
	texts := map[string]string{"homie directive": homieDirective}
	for _, role := range allSpawnRoles {
		prose, err := roleProseDirective(role)
		if err != nil {
			t.Fatal(err)
		}
		texts[string(role)+" directive"] = prose
	}
	err := fs.WalkDir(roleSkillFS, "skills", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		body, err := roleSkillFS.ReadFile(path)
		texts[path] = string(body)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range texts {
		flat := strings.Join(strings.Fields(text), " ")
		for _, fact := range []string{"Verified source commit", "Scribe input commit", "Scribe result commit"} {
			if strings.Contains(flat, fact) {
				t.Errorf("%s names the fact %q", name, fact)
			}
		}
	}
}
