package deployment

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The runtime manifest closes each runner and helper import graph.
// Package imports resolve from the runtime dependencies.
func runnerReleaseEntryClosure(t *testing.T, runnerRoot string) map[string]bool {
	t.Helper()
	seen := map[string]bool{}
	stack := append([]string{"agent-runner/main.ts", "homie-runner/main.ts", "setup-runner/main.ts", "shared/check-project.ts", "shared/project-files.ts"},
		runnerSpawnTargets(t, runnerRoot)...)
	for len(stack) > 0 {
		rel := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[rel] {
			continue
		}
		seen[rel] = true
		body, err := os.ReadFile(filepath.Join(runnerRoot, rel))
		if err != nil {
			t.Fatalf("runner entry graph reached an unreadable file %s: %v", rel, err)
		}
		for _, match := range hostReleaseImport.FindAllStringSubmatch(string(body), -1) {
			base := filepath.Join(filepath.Dir(rel), match[1])
			// An import that climbs out of runner/ is unshippable by
			// construction: readReleaseSource walks each path component down
			// from the runner root and cannot express "..". Catching it here
			// names the cause; the alternative is a staging failure that reads
			// as a missing file.
			if strings.HasPrefix(base, "..") {
				t.Fatalf("runner entry graph imports %q from %s, which escapes runner/ and cannot be shipped: "+
					"runnerReleaseFiles addresses entries from the runner root", match[1], rel)
			}
			resolved := ""
			for _, candidate := range []string{base, base + ".ts", base + ".js", filepath.Join(base, "index.ts")} {
				if info, err := os.Stat(filepath.Join(runnerRoot, candidate)); err == nil && info.Mode().IsRegular() {
					resolved = candidate
					break
				}
			}
			if resolved == "" {
				t.Fatalf("runner entry graph has an unresolvable import %q in %s", match[1], rel)
			}
			stack = append(stack, filepath.ToSlash(resolved))
		}
	}
	return seen
}

// The runner starts adapters through absolute paths instead of imports.
// These paths require files in the runtime manifest.
var runnerSpawnLiteral = regexp.MustCompile(`"(?:/opt/culler-runtime)?/app/src/([^"]+)"`)

// fake-harness is reachable only when config.runnerSrcDir points at the
// repository tree, which is the e2e lane; a production deployment points it at
// $MC_HOME/release/runner (PrepareSupervision) and dispatches the fake family
// nowhere. Shipping it would put test scaffolding on the operator's disk.
var runnerSpawnNotShipped = map[string]bool{"fake-harness/cli.ts": true}

func runnerSpawnTargets(t *testing.T, runnerRoot string) []string {
	t.Helper()
	found := map[string]bool{}
	for _, rel := range runnerReleaseFiles {
		body, err := os.ReadFile(filepath.Join(runnerRoot, rel))
		if err != nil {
			t.Fatalf("runner release manifest names an unreadable file %s: %v", rel, err)
		}
		for _, match := range runnerSpawnLiteral.FindAllStringSubmatch(string(body), -1) {
			found[match[1]] = true
		}
	}
	if len(found) == 0 {
		t.Fatal("no /app/src spawn target found in the runner sources — the adapter wiring changed")
	}
	targets := []string{}
	for rel := range found {
		if runnerSpawnNotShipped[rel] {
			continue
		}
		targets = append(targets, rel)
	}
	sort.Strings(targets)
	return targets
}

func TestRunnerReleaseSpawnTargetsAreShipped(t *testing.T) {
	runnerRoot, err := filepath.Abs("../../runner")
	if err != nil {
		t.Fatal(err)
	}
	shipped := map[string]bool{}
	for _, rel := range runnerReleaseFiles {
		shipped[rel] = true
	}
	for _, rel := range runnerSpawnTargets(t, runnerRoot) {
		if !shipped[rel] {
			t.Fatalf("a runner spawns /app/src/%s but the runner release does not ship it: "+
				"it fails inside a --rm container whose output nothing reads", rel)
		}
	}
}

func TestRunnerReleaseManifestCoversTheEntryImportGraph(t *testing.T) {
	runnerRoot, err := filepath.Abs("../../runner")
	if err != nil {
		t.Fatal(err)
	}
	shipped := map[string]bool{}
	for _, rel := range runnerReleaseFiles {
		shipped[rel] = true
	}
	missing := []string{}
	for rel := range runnerReleaseEntryClosure(t, runnerRoot) {
		if !shipped[rel] {
			missing = append(missing, rel)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("the runner release ships runners that import modules it omits; add to runnerReleaseFiles: %s",
			strings.Join(missing, ", "))
	}
}

// The converse, and the directory half the host test gets for free from
// ValidateHostRelease: a manifest entry whose parent directory is not in
// runnerReleaseDirs is staged into a directory InstallRunnerRelease never
// creates, so the copy fails at onboarding rather than here.
func TestRunnerReleaseManifestShipsNothingUnreachable(t *testing.T) {
	runnerRoot, err := filepath.Abs("../../runner")
	if err != nil {
		t.Fatal(err)
	}
	reachable := runnerReleaseEntryClosure(t, runnerRoot)
	dirs := map[string]bool{}
	for _, rel := range runnerReleaseDirs {
		dirs[rel] = true
	}
	for _, rel := range runnerReleaseFiles {
		if info, err := os.Stat(filepath.Join(runnerRoot, rel)); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("runner release manifest names a file the repository does not have: %s", rel)
		}
		if !reachable[rel] {
			t.Fatalf("runner release ships %s, which neither runner entry point imports", rel)
		}
		if parent := filepath.ToSlash(filepath.Dir(rel)); !dirs[parent] {
			t.Fatalf("runner release ships %s but runnerReleaseDirs omits %q, so staging has nowhere to put it", rel, parent)
		}
	}
}
