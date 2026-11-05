package deployment

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Nothing typechecks the TypeScript in this repository — there is no root
// tsconfig.json and no build step runs tsc — so a misspelled event name is not
// a compile error anywhere. It is a record nobody can select on, discovered
// months later by an operator who greps for an event that was never written.
// These tests are the enforcement the type system does not provide.
//
// They live in mc/deployment because mc/check.sh already runs
// `go test -count=1 ./deployment/` for exactly this class of test: one that
// reads files outside its own module, which Go's test cache does not track.

var (
	logEventRegistryEntry = regexp.MustCompile(`"([a-z][a-z0-9]*\.[a-z][a-z0-9_.]*)"`)
	logEventCallSite      = regexp.MustCompile(`\blog\.(?:error|warn|info|debug)\(\s*"([^"]+)"`)
	logEventGrammar       = regexp.MustCompile(`^[a-z][a-z0-9]*\.[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)?$`)
)

// logEventRegistry parses the EVENTS array out of runner/shared/log.ts. The
// array is the single source of truth in both languages; parsing it beats
// restating it here, which would put the drift one file further away.
func logEventRegistry(t *testing.T, repoRoot string) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot, "runner", "shared", "log.ts"))
	if err != nil {
		t.Fatalf("read the shared log module: %v", err)
	}
	text := string(body)
	start := strings.Index(text, "export const EVENTS = [")
	if start < 0 {
		t.Fatal("runner/shared/log.ts no longer declares `export const EVENTS = [`")
	}
	end := strings.Index(text[start:], "] as const;")
	if end < 0 {
		t.Fatal("the EVENTS array is not closed with `] as const;`")
	}
	events := map[string]bool{}
	for _, match := range logEventRegistryEntry.FindAllStringSubmatch(text[start:start+end], -1) {
		events[match[1]] = true
	}
	if len(events) == 0 {
		t.Fatal("parsed no events out of the EVENTS array")
	}
	return events
}

// logSourceFiles walks the TypeScript that emits, skipping node_modules and
// tests. A test may name an event that production does not, and that is not
// drift worth failing over.
func logSourceFiles(t *testing.T, repoRoot string) []string {
	t.Helper()
	roots := []string{"resident/src", "dashboard/src", "runner/agent-runner", "runner/homie-runner", "runner/shared"}
	files := []string{}
	for _, root := range roots {
		err := filepath.WalkDir(filepath.Join(repoRoot, root), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".test.ts") {
				return nil
			}
			files = append(files, path)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	if len(files) == 0 {
		t.Fatal("found no TypeScript sources to check")
	}
	return files
}

func TestEveryEmittedEventIsInTheRegistry(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	registry := logEventRegistry(t, repoRoot)
	for _, path := range logSourceFiles(t, repoRoot) {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		rel, _ := filepath.Rel(repoRoot, path)
		for _, match := range logEventCallSite.FindAllStringSubmatch(string(body), -1) {
			event := match[1]
			if !registry[event] {
				t.Errorf("%s emits %q, which is not in the EVENTS registry", rel, event)
			}
		}
	}
}

// The repository lane reports each failure of its host work through the
// repository events. An event without a call site is a record that no
// failure ever writes, so an operator who selects on it sees nothing.
func TestEveryRepositoryEventHasACallSite(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	emitted := map[string]bool{}
	for _, path := range logSourceFiles(t, repoRoot) {
		if !strings.HasPrefix(path, filepath.Join(repoRoot, "resident", "src")+string(filepath.Separator)) {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, match := range logEventCallSite.FindAllStringSubmatch(string(body), -1) {
			emitted[match[1]] = true
		}
	}
	names := []string{}
	for event := range logEventRegistry(t, repoRoot) {
		if strings.HasPrefix(event, "repository.") {
			names = append(names, event)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatal("the EVENTS registry holds no repository event")
	}
	for _, event := range names {
		if !emitted[event] {
			t.Errorf("registered event %q has no call site in resident/src", event)
		}
	}
}

func TestEveryRegisteredEventObeysTheGrammar(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for event := range logEventRegistry(t, repoRoot) {
		names = append(names, event)
	}
	sort.Strings(names)
	for _, event := range names {
		if !logEventGrammar.MatchString(event) {
			t.Errorf("registered event %q does not match the event grammar", event)
		}
	}
}

// The free-text escape hatch the migration used. It is gone, and a call site
// that reintroduces it — or a registry entry that readmits it — puts the log
// back to being prose nobody can select on.
func TestTheLegacyFreeTextEventIsGone(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if logEventRegistry(t, repoRoot)["legacy.line"] {
		t.Error("EVENTS readmitted legacy.line")
	}
	for _, path := range logSourceFiles(t, repoRoot) {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(body), `"legacy.line"`) {
			rel, _ := filepath.Rel(repoRoot, path)
			t.Errorf("%s names legacy.line", rel)
		}
	}
}

// The rotation ring is implemented twice — once in TypeScript for the two
// long-lived units, once in Go for every short-lived mc — against ONE file per
// component. Two writers that disagree about the cap produce a ring whose
// generation count depends on which process happened to cross the threshold.
func TestTheRotationConstantsAgreeAcrossLanguages(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	read := func(rel string) string {
		body, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(body)
	}
	number := func(source, pattern, label string) int {
		re := regexp.MustCompile(pattern)
		match := re.FindStringSubmatch(source)
		if match == nil {
			t.Fatalf("%s is no longer declared where this test looks for it", label)
		}
		value, err := strconv.Atoi(strings.ReplaceAll(match[1], "_", ""))
		if err != nil {
			t.Fatalf("%s is not a plain integer: %v", label, err)
		}
		return value
	}
	ts := read("runner/shared/log-sink.ts")
	goSource := read("mc/mclog/rotate.go")

	tsMaxMiB := number(ts, `ROTATE_MAX_BYTES\s*=\s*(\d+)\s*\*\s*1024\s*\*\s*1024`, "the TypeScript rotation cap")
	goMaxMiB := number(goSource, `ROTATE_MAX_BYTES\s*=\s*(\d+)\s*<<\s*20`, "the Go rotation cap")
	if tsMaxMiB != goMaxMiB {
		t.Errorf("rotation cap disagrees: TypeScript %d MiB, Go %d MiB", tsMaxMiB, goMaxMiB)
	}
	tsKeep := number(ts, `ROTATE_KEEP\s*=\s*(\d+)`, "the TypeScript ring size")
	goKeep := number(goSource, `ROTATE_KEEP\s*=\s*(\d+)`, "the Go ring size")
	if tsKeep != goKeep {
		t.Errorf("ring size disagrees: TypeScript keeps %d, Go keeps %d", tsKeep, goKeep)
	}
}
