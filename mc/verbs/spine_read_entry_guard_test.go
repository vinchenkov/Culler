package verbs

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The snapshot read is safe only for a spine the caller has quiesced, so the
// boundary between the settled and the live door must be structural, not
// tribal knowledge. These guards enumerate every call site of the settled
// machinery; a new route that quietly reads a possibly-live spine through
// the snapshot shape fails here with a named file, and the planted-violation
// case proves the checker actually has teeth.

// settledEntryAllowlist maps each file allowed to reference the snapshot
// shape or the settled inspector to the exact number of occurrences.
var settledEntryAllowlist = map[string]int{
	"substrate/substrate.go":          1, // the guarded entry itself
	"verbs/onboard.go":                1, // inspectSpineSettled hands it over
	"verbs/release_database.go":       2, // open + the settled inspector dispatch
	"verbs/spine_transfer.go":         1, // the staged backup/restore reader
	"deployment/update_checkpoint.go": 1, // the checkpoint verify composite
	"cmd/mc/setup_published_prod.go":  1, // the settled install/repair door
}

var settledMarkers = []string{"OpenSnapshotReadOnly", "InspectSettledReleaseDatabase"}

func settledReferenceViolations(files map[string][]string) []string {
	counts := map[string]int{}
	for name, lines := range files {
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue // comments describe the boundary; they are not crossings
			}
			if strings.Contains(trimmed, "func OpenSnapshotReadOnly(") {
				continue // the definition is not a call site
			}
			for _, marker := range settledMarkers {
				if strings.Contains(trimmed, marker) {
					counts[name]++
				}
			}
		}
	}
	var out []string
	for name := range counts {
		if settledEntryAllowlist[name] == 0 {
			out = append(out, "settled read reached an unlisted file: "+name)
		}
	}
	for name, want := range settledEntryAllowlist {
		if counts[name] > want {
			out = append(out, "settled read count grew in "+name)
		}
	}
	// The redeploy gate is a live-spine reader by contract: it must never
	// gain the snapshot shape, and the checkpoint verify must never route
	// back through the live verb.
	if extra := counts["verbs/onboard_spine.go"]; extra > 0 {
		out = append(out, "the live deployment inspector gained a settled read")
	}
	sort.Strings(out)
	return out
}

func TestSettledReadEntriesStayWithinTheAllowlist(t *testing.T) {
	root := ".."
	files := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path) // path already carries the root prefix
		if err != nil {
			return err
		}
		files[strings.TrimPrefix(filepath.ToSlash(path), "../")] = strings.Split(string(body), "\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if violations := settledReferenceViolations(files); len(violations) > 0 {
		t.Fatalf("settled read boundary moved: %v", violations)
	}
	// The checkpoint verify left the verb surface entirely; a return would
	// re-import the version-skew defect this ticket fixed.
	body, err := os.ReadFile(filepath.Join(root, "deployment/update_checkpoint.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"__release-database"`) {
		t.Fatal("the checkpoint verify routes through the live verb again")
	}
}

// A guard that cannot fail is decoration: plant each forbidden shape and
// require a named violation, the same negative-proof discipline the acceptor
// markers answer to.
func TestSettledReadGuardRejectsPlantedMisroutes(t *testing.T) {
	plants := map[string]map[string][]string{
		"new file takes the snapshot door": {"mc/deployment/other.go": {
			`	db, err := substrate.OpenSnapshotReadOnly(spine)`}},
		"live inspector gains settled": {"mc/verbs/onboard_spine.go": {
			`	db, err := substrate.OpenSnapshotReadOnly(spine)`}},
		"settled inspector smuggled into cmd": {"mc/cmd/mc/sneaky.go": {
			`	r, err := verbs.InspectSettledReleaseDatabase(p)`}},
	}
	for label, extra := range plants {
		t.Run(label, func(t *testing.T) {
			files := map[string][]string{}
			for name, lines := range extra {
				files[strings.TrimPrefix(filepath.ToSlash(name), "mc/")] = lines
			}
			if len(settledReferenceViolations(files)) == 0 {
				t.Fatal("the planted violation passed silently")
			}
		})
	}
}
