package verbs

import (
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"mc/dispatch"
)

// Every skill Culler ships to an agent, embedded beside the frozen
// directives and installed into the run's container as a real agent skill.
//
// The layout is one directory per role, holding one directory per skill:
//
//	skills/<role-basename>/<skill-name>/SKILL.md
//
// A role's set always holds its command surface, named `mc-<role-basename>`:
// `mc` ships no help surface — bare `mc` names zero verbs and `mc --help` is
// an unknown verb — so that skill is the only verb surface a container agent
// can read. The other skills in the directory are the role's additional skills:
// how to do a part of its job that its command surface does not cover. The
// `shared` directory holds the skills every tier ships; they close each set.
// The operator store adds per-Project skills.
//
// A skill rather than brief text, for two reasons. Cost: only the frontmatter
// description stays resident, and the body loads when the agent reaches for
// it. Isolation: the body is a file the run's own container carries, so a
// role's skills are delivered per role rather than concatenated into text
// every role's brief already pays for.
//
//go:embed skills/*/*/SKILL.md
var roleSkillFS embed.FS

// RoleSkill is one shipped skill: the directory name the harness discovers it
// by, and the SKILL.md body.
type RoleSkill struct {
	Name string
	Body string
}

// Description is the skill's single-line frontmatter description, the one
// part of a skill that is always resident in the agent's context.
func (s RoleSkill) Description() string { return SkillFrontmatterField(s.Body, "description") }

// roleDirectiveBasename maps a role to the shared basename of its prose
// directive and its skill directory. One map, so the two halves cannot drift
// onto different files for the same role.
var roleDirectiveBasename = map[dispatch.Role]string{
	dispatch.RoleStrategist: "strategist",
	dispatch.RoleArbiter:    "arbiter",
	dispatch.RoleWorker:     "worker",
	dispatch.RoleVerifier:   "verifier",
	dispatch.RoleScribe:     "scribe",
	dispatch.RolePackager:   "packager",
}

// HomieSkillBasename is the Homie tier's skill directory. The tier has no
// dispatch.Role: a Homie takes no execution lease and is not a pipeline role.
// Its command surface is nonetheless just as fixed as the pipeline roles' —
// maximumHomieAgentVerbs is compiled in, and `mc homie start` defaults to the
// whole of it — so it is one static contract. An operator may start a
// narrower session with `--allow`, which can only ever be a SUBSET of that
// same set, and the CLI refuses the difference by scope.
const HomieSkillBasename = "homie"

// SharedSkillBasename is the directory of the skills every pipeline role and
// the Homie tier ship. It has no command surface and no operator store
// directory; its skills close each tier's set.
const SharedSkillBasename = "shared"

// commandSkillPrefix marks a role's command surface skill. The name is derived
// from the role, never authored, so a file cannot rename itself out of the
// directive that names it.
const commandSkillPrefix = "mc-"

// RoleSkillName is the command surface skill's discovery name — the directory
// the harness finds it under and the identifier the directive points the
// agent at.
func RoleSkillName(role dispatch.Role) (string, error) {
	basename, ok := roleDirectiveBasename[role]
	if !ok {
		return "", fmt.Errorf("no skill for role %q", role)
	}
	return commandSkillPrefix + basename, nil
}

// RoleSkillFor returns the role's shipped command surface. Exported so the CLI
// test tier can drive every enumerated command through the real `mc` binary:
// the contract is judged by the CLI that must accept it, never by its own
// declaration.
func RoleSkillFor(role dispatch.Role) (RoleSkill, error) {
	basename, ok := roleDirectiveBasename[role]
	if !ok {
		return RoleSkill{}, fmt.Errorf("no skill for role %q", role)
	}
	return commandSkillByBasename(basename)
}

// RoleSkillSet returns everything the role ships: the command surface first,
// then the role's additional skills in name order, then the shared skills in
// name order. This is the set a spawn effect carries and the runner installs.
func RoleSkillSet(role dispatch.Role) ([]RoleSkill, error) {
	basename, ok := roleDirectiveBasename[role]
	if !ok {
		return nil, fmt.Errorf("no skill for role %q", role)
	}
	return skillSetByBasename(basename)
}

// HomieSkill returns the Homie tier's command surface, on the same embed and
// the same name derivation as the pipeline roles'.
func HomieSkill() (RoleSkill, error) { return commandSkillByBasename(HomieSkillBasename) }

// HomieSkillSet returns everything the Homie tier ships, command surface first.
func HomieSkillSet() ([]RoleSkill, error) { return skillSetByBasename(HomieSkillBasename) }

func commandSkillByBasename(basename string) (RoleSkill, error) {
	return skillByPath(basename, commandSkillPrefix+basename)
}

func skillSetByBasename(basename string) ([]RoleSkill, error) {
	command, err := commandSkillByBasename(basename)
	if err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(roleSkillFS, "skills/"+basename)
	if err != nil {
		return nil, fmt.Errorf("list skills for %q: %v", basename, err)
	}
	set := []RoleSkill{command}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == command.Name {
			continue
		}
		skill, err := skillByPath(basename, entry.Name())
		if err != nil {
			return nil, err
		}
		set = append(set, skill)
	}
	shared, err := sharedSkills()
	if err != nil {
		return nil, err
	}
	return append(set, shared...), nil
}

// sharedSkills returns the skills every tier ships, in name order.
func sharedSkills() ([]RoleSkill, error) {
	entries, err := fs.ReadDir(roleSkillFS, "skills/"+SharedSkillBasename)
	if err != nil {
		return nil, fmt.Errorf("list shared skills: %v", err)
	}
	var set []RoleSkill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skill, err := skillByPath(SharedSkillBasename, entry.Name())
		if err != nil {
			return nil, err
		}
		set = append(set, skill)
	}
	return set, nil
}

// skillByPath reads one shipped skill and checks the two things the delivery
// chain relies on. The directory name is what the harness lists the skill by
// and what the directive tells the agent to load, so the frontmatter name must
// equal it; a mismatch is a build-time failure rather than a runtime surprise.
// The name must also be one safe path segment, because the runner joins it
// under a root it wipes.
func skillByPath(basename, name string) (RoleSkill, error) {
	if !skillName.MatchString(name) {
		return RoleSkill{}, fmt.Errorf("skill directory %q under %q is not a valid skill name", name, basename)
	}
	body, err := roleSkillFS.ReadFile("skills/" + basename + "/" + name + "/SKILL.md")
	if err != nil {
		return RoleSkill{}, fmt.Errorf("read skill %q for %q: %v", name, basename, err)
	}
	if declared := SkillFrontmatterField(string(body), "name"); declared != name {
		return RoleSkill{}, fmt.Errorf("skill %q for %q declares name %q", name, basename, declared)
	}
	return RoleSkill{Name: name, Body: strings.TrimSpace(string(body)) + "\n"}, nil
}

// SkillsEffectValue is the shape a spawn or wake effect carries a skill set
// in: one object per skill, in install order. The resident copies it into
// run.json as the head of the run's `skills` list.
func SkillsEffectValue(set []RoleSkill) []map[string]any {
	value := make([]map[string]any, 0, len(set))
	for _, skill := range set {
		value = append(value, map[string]any{"name": skill.Name, "body": skill.Body})
	}
	return value
}

// skillName is the permitted skill name: lowercase letters, digits and
// hyphens, starting with a letter or digit. The resident's store reader and
// the runner's installer hold the same rule.
var skillName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// SkillFrontmatterField reads one scalar out of a SKILL.md's leading YAML
// block. Deliberately not a YAML parser: the frontmatter this ships is two
// flat keys, and a real parser would accept shapes the harness's own loader
// does not. Returns "" when the block or the key is absent.
func SkillFrontmatterField(body, field string) string {
	rest, ok := strings.CutPrefix(strings.TrimLeft(body, "\n"), "---\n")
	if !ok {
		return ""
	}
	block, _, ok := strings.Cut(rest, "\n---")
	if !ok {
		return ""
	}
	for _, line := range strings.Split(block, "\n") {
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(name) == field {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// ParseSkillCommands pulls the command templates out of a skill body. A
// command is a backtick-delimited span on its own bullet, kept to exactly one
// line so this parse stays trivial and a malformed entry is visible on sight
// rather than silently skipped.
func ParseSkillCommands(body string) []string {
	var commands []string
	for _, line := range strings.Split(body, "\n") {
		// Bullets only. The surrounding prose backticks flag names and `mc`
		// itself; treating any backticked line as a command would enumerate
		// those as callable and hand the CLI conformance test nonsense.
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- `") {
			continue
		}
		rest := line[len("- `"):]
		end := strings.Index(rest, "`")
		if end < 0 {
			continue
		}
		if command := strings.TrimSpace(rest[:end]); command != "" {
			commands = append(commands, command)
		}
	}
	return commands
}

// proseVerbInvocations extracts each `mc` command and its flags from the prose
// half of a directive. Another tool can live in the directive itself.
func proseVerbInvocations(prose string) []string {
	var invocations []string
	seen := map[string]bool{}
	add := func(value string) {
		if value != "" && !seen[value] {
			seen[value] = true
			invocations = append(invocations, value)
		}
	}
	for _, span := range proseCommandSpans(prose) {
		// Directives wrap long commands across lines; the skill keeps each on
		// one. Normalize before comparing or every wrapped command would read
		// as absent.
		fields := strings.Fields(span)
		for i, field := range fields {
			field = strings.Trim(field, "[],.")
			if field != "mc" {
				continue
			}
			head := "mc"
			// A verb plus at most one subverb; anything further is a
			// positional argument whose name is the role's to choose.
			for _, next := range fields[i+1:][:min(2, len(fields)-i-1)] {
				if strings.HasPrefix(next, "-") || strings.HasPrefix(next, "<") {
					break
				}
				head += " " + next
			}
			add(head)
			for _, next := range fields[i+1:] {
				// Optional arguments use `[--branch <branch>]`.
				next = strings.Trim(next, "[],.")
				if strings.HasPrefix(next, "--") {
					add(next)
				}
			}
			break
		}
	}
	return invocations
}

// proseCommandSpans finds commands in backtick spans and indented code blocks.
func proseCommandSpans(text string) []string {
	var spans []string
	parts := strings.Split(text, "`")
	// Odd indices are the delimited spans; an unterminated final span is not
	// one and is dropped with the trailing part.
	for i := 1; i < len(parts); i += 2 {
		spans = append(spans, parts[i])
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "    ") && strings.Contains(line, "mc ") {
			spans = append(spans, strings.TrimSpace(line))
		}
	}
	return spans
}
