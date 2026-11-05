package main_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	// Aliased: this package's test suite already binds `dispatch` to a helper.
	roles "mc/dispatch"
	"mc/domain"
	"mc/verbs"
	"mc/wiki"
)

// TestEveryRoleSkillCommandIsRecognizedByTheRealCLI is the consumer half of the
// anti-drift contract. The skill shipped to each role is judged HERE, by
// the binary that must accept it, not by its own declaration — the failure
// mode this whole slice exists to close is a role artifact that reads
// correctly and names something the CLI does not implement.
//
// It asserts recognition, not success: every invocation below is expected to
// be refused on identity, scope, or state, because the test holds no lease.
// What it must never be refused for is not existing. An unknown verb, an
// unknown subverb, or an undefined flag means the skill has drifted from
// the CLI, and an agent following it would probe dead ends until its lease
// burned — observed live, and the reason this test exists.
//
// The run of a wiki update needs no lease, so each command line of the Scribe
// runs as the Scribe of a live wiki update run. The CLI must not refuse it for
// its usage or for its identity.
func TestEveryRoleSkillCommandIsRecognizedByTheRealCLI(t *testing.T) {
	spine := initSpine(t)
	env := []string{"MC_SPINE=" + spine}
	skills := map[string]string{}
	for _, role := range allSpawnRolesForConformance {
		skill, err := verbs.RoleSkillFor(role)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		skills[string(role)] = skill.Body
	}
	// The Homie tier is not a dispatch role and takes no lease, but its
	// surface is shipped the same way and drifts the same way, so it is judged
	// by the same binary. Its verbs are operator-scope: refused to a pipeline
	// identity, reachable here, and either way never for "unknown verb".
	homie, err := verbs.HomieSkill()
	if err != nil {
		t.Fatalf("homie: %v", err)
	}
	skills["homie"] = homie.Body

	for tier, body := range skills {
		commands := verbs.ParseSkillCommands(body)
		if len(commands) == 0 {
			t.Fatalf("%s: skill enumerates no commands", tier)
		}
		for _, command := range commands {
			args, run := resolvePlaceholders(command, nil), env
			if tier == string(roles.RoleScribe) {
				values, scribe := scribeConformanceRun(t)
				args, run = resolvePlaceholders(command, values), scribe
			}
			res := runMC(t, run, "", args[1:]...)
			surface := res.stdout + res.stderr
			if tier == string(roles.RoleScribe) {
				refusal, _ := res.json["error"].(map[string]any)
				if res.code == 2 || refusal["code"] == domain.CodeStaleRun || refusal["code"] == domain.CodeRoleMismatch {
					t.Errorf("%s skill command %q: the CLI refuses the command for its usage or its identity (exit code %d)\n%s",
						tier, command, res.code, strings.TrimSpace(surface))
				}
			}
			for _, drift := range []string{
				"unknown verb",
				"unknown subverb",
				"flag provided but not defined",
				// Reachable only because resolvePlaceholders now strips the
				// optional-flag brackets: before that every `[--flag …]` in
				// every skill arrived as the positional token `[--flag`, and
				// Go's flag package stops at the first non-flag argument. So
				// the undefined-flag check above silently covered required
				// flags only, and three shipped skills advertised optional
				// flags the CLI does not define.
				"unexpected argument",
			} {
				if strings.Contains(surface, drift) {
					t.Errorf("%s skill command %q: CLI reports %q\n%s",
						tier, command, drift, strings.TrimSpace(surface))
				}
			}
			// Each required CLI flag must appear in the role command template.
			if flag := singleRequiredFlag(surface); flag != "" && !strings.Contains(command, flag) {
				t.Errorf("%s skill command %q omits the flag the CLI requires (%s)\n%s",
					tier, command, flag, strings.TrimSpace(surface))
			}
		}
	}
}

var allSpawnRolesForConformance = []roles.Role{
	roles.RoleStrategist,
	roles.RoleArbiter,
	roles.RoleWorker,
	roles.RoleVerifier,
	roles.RoleScribe,
	roles.RolePackager,
}

var placeholder = regexp.MustCompile(`<[^>]+>`)

// resolvePlaceholders turns a skill template into a real argv. The values
// are shaped to pass argument PARSING — ids parse as integers, the seal
// receipt is 16 lowercase hex — so that anything the CLI still rejects is
// rejected for a reason other than the argument being unusable.
//
// A value of values replaces the default value of its placeholder.
func resolvePlaceholders(command string, values map[string]string) []string {
	// Substitute BEFORE splitting. A placeholder may contain spaces —
	// `<16 lowercase hex>` is the shipped example — and splitting first left
	// it as three literal tokens that no substitution could ever reach. That
	// went unnoticed because the CLI answers such an argv with "unexpected
	// argument", which this test did not check for until the same change that
	// stripped the optional-flag brackets.
	resolved := placeholder.ReplaceAllStringFunc(command, func(name string) string {
		if value, ok := values[name]; ok {
			return value
		}
		switch name {
		case "<task>", "<id>", "<n>":
			return "1"
		case "<16 lowercase hex>":
			return "4f2a9c1e70b35d68"
		case "<run_id>":
			return "0123456789abcdef"
		default:
			return "conformance-placeholder"
		}
	})
	var args []string
	for _, field := range strings.Fields(resolved) {
		// Optional-flag brackets are skill notation, not argv. Left in place
		// they turn `--steer` into the positional token `[--steer`, Go's flag
		// parser stops there, and every optional flag in every skill goes
		// unchecked — which is exactly how `--steer`, `--description` and an
		// optional `--reason` on approve all shipped against a CLI that
		// defines none of them.
		if field = strings.Trim(field, "[]"); field != "" {
			args = append(args, field)
		}
	}
	return args
}

// scribeRun is one active wiki update of a new fixture with the repository of
// its Scribe run. The repository is at the start commit. The completion
// receipt states a complete OpenWiki run with no wiki change.
type scribeRun struct {
	spine, run, start, source, receipt string
	// env is the environment of the Scribe of the run.
	env []string
}

func newScribeRun(t *testing.T) scribeRun {
	t.Helper()
	spine := initSpine(t)
	db := openDB(t, spine)
	workspace := queryStr(t, db, `SELECT p.workspace_root FROM projects w JOIN sandbox_profiles p ON p.id=w.sandbox_profile WHERE w.id='ws-test'`)
	start := fixtureGit(t, workspace, "rev-parse", "refs/heads/main")
	const run = "5c21be0a9d3f4e67"
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	if err := domain.StartWikiUpdate(ctx, conn, time.Now(), domain.WikiStart{Project: "ws-test", RunID: run, StartCommit: start,
		SessionPath: "sessions/" + run, Binding: "fake/fake", HardDeadlineMinutes: 240}); err != nil {
		t.Fatalf("start the wiki update: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	source := filepath.Join(root, "source")
	fixtureGit(t, root, "clone", "-q", workspace, source)
	files := map[string][]byte{}
	if err := filepath.WalkDir(filepath.Join(source, "openwiki"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		name, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(name)], err = os.ReadFile(path)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	receipt, err := json.Marshal(wiki.Completion{Format: 1, Status: "noop", InputCommit: start, SourceCommit: start,
		WikiDigest: wiki.Digest(files), OpenWikiVersion: wiki.Version, OpenWikiRevision: wiki.Revision})
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(root, "openwiki-completion.json")
	if err := os.WriteFile(receiptPath, receipt, 0o600); err != nil {
		t.Fatal(err)
	}
	return scribeRun{spine: spine, run: run, start: start, source: source, receipt: receiptPath,
		env: append(runJSONEnv(t, spine, run, "pipeline", "scribe"), "MC_TASK_STORE_ROOT="+root)}
}

// scribeConformanceRun starts one wiki update in a new fixture. It returns the
// placeholder values of that run and the environment of its Scribe.
func scribeConformanceRun(t *testing.T) (map[string]string, []string) {
	t.Helper()
	f := newScribeRun(t)
	values := map[string]string{
		"<run_id>":      f.run,
		"<full-commit>": f.start,
		"<path>":        f.receipt,
		"<json>":        `{"run":"` + f.run + `","revision":1,"passage":"instruction","requirement":"contract","reason":"conflict"}`,
	}
	return values, f.env
}

// The Scribe reads the failed check from the output of its terminal action.
// The command ends the run, so a second submission is stale.
func TestScribeTerminalStatesTheFailedCheck(t *testing.T) {
	f := newScribeRun(t)
	if err := os.WriteFile(filepath.Join(f.source, "source-change.txt"), []byte("A source change of the Scribe.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, f.source, "add", "-A")
	fixtureGit(t, f.source, "commit", "-q", "-m", "Change a source file")
	commit := fixtureGit(t, f.source, "rev-parse", "HEAD")
	const check = "The wiki commit changes a path outside openwiki/: source-change.txt."

	res := runMC(t, f.env, "", "complete", "--run", f.run, "--commit", commit, "--wiki-evidence", f.receipt)
	refusal, _ := res.json["error"].(map[string]any)
	if res.code != 1 || refusal["code"] != domain.CodeWikiCheckFailed || refusal["message"] != check {
		t.Fatalf("terminal action with a source change: exit code %d, stdout %s; want exit code 1 and the failed check", res.code, res.stdout)
	}
	db := openDB(t, f.spine)
	if got := queryStr(t, db, `SELECT r.outcome || '|' || w.result || '|' || w.cause || '|' || w.wiki_commit
		FROM wiki_updates w JOIN runs r ON r.id=w.run_id WHERE w.run_id=?`, f.run); got != "completed|failed|"+check+"|"+commit {
		t.Fatalf("the run and its wiki update = %s, want a failed wiki update with the check and the submitted commit", got)
	}

	res = runMC(t, f.env, "", "complete", "--run", f.run, "--commit", commit, "--wiki-evidence", f.receipt)
	refusal, _ = res.json["error"].(map[string]any)
	if res.code != 1 || refusal["code"] != domain.CodeStaleRun {
		t.Fatalf("second terminal action: exit code %d, stdout %s; want a stale run refusal", res.code, res.stdout)
	}
}

// TestConformanceHarnessDetectsAnInventedVerb is the control arm. Without it
// a substring check that never fires is indistinguishable from one whose
// invocations all failed before the CLI ever parsed them.
func TestConformanceHarnessDetectsAnInventedVerb(t *testing.T) {
	spine := initSpine(t)
	env := []string{"MC_SPINE=" + spine}
	res := runMC(t, env, "", "worker", "finish-up", "--run", "0123456789abcdef")
	if !strings.Contains(res.stdout+res.stderr, "unknown verb") {
		t.Fatalf("an invented verb was not reported as unknown: %s%s", res.stdout, res.stderr)
	}
	res = runMC(t, env, "", "complete", "1", "--run", "0123456789abcdef", "--invented-flag", "x")
	if !strings.Contains(res.stdout+res.stderr, "flag provided but not defined") {
		t.Fatalf("an invented flag was not reported as undefined: %s%s", res.stdout, res.stderr)
	}
	// The terminal of a task is a usage error for the Scribe of a wiki update run.
	values, scribe := scribeConformanceRun(t)
	args := resolvePlaceholders("mc complete <task> --run <run_id> --status worked --commit <full-commit> --wiki-evidence <path>", values)
	if res = runMC(t, scribe, "", args[1:]...); res.code != 2 {
		t.Fatalf("a task terminal of the Scribe = exit code %d, want the usage error: %s%s", res.code, res.stdout, res.stderr)
	}
}

// requiresFlag matches a refusal that names exactly one required flag, e.g.
// `mc homie end requires --reason`. Deliberately narrow: "requires exactly one
// of --status, --needs-operator, --infra" names a CHOICE, and a template that
// carries any one of them is correct, so the anchored single-flag form is the
// only one this test can judge mechanically.
var requiresFlag = regexp.MustCompile(`requires (--[a-z][a-z-]*)`)

func singleRequiredFlag(surface string) string {
	m := requiresFlag.FindStringSubmatch(surface)
	if m == nil || strings.Contains(surface, "requires exactly one of") {
		return ""
	}
	return m[1]
}
