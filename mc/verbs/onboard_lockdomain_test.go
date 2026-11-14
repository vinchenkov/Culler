package verbs

import (
	"os"
	"strings"
	"testing"
)

// inspectSpineReadOnly used to be the codebase's ONE deliberate bypass of
// substrate.Open: it built its own read-only DSN and called sql.Open
// directly, so it was the one spine opener whose lock-domain guard was not
// inherited — and the one that could lose it silently. The bypass is gone:
// every read-only open, this inspection and spine_transfer's snapshot
// inspection included, now goes through substrate.OpenReadOnly, which carries
// the guard itself: exactly one function builds the read-only DSN.
//
// Nothing behavioral can catch a regrown bypass here. The fast lane runs on
// darwin, where substrate's platform mount source is inert, so a raw sql.Open
// smuggled back in turns no test red. A source-level assertion is the honest
// remaining option — the same shape as boundary's derivation guard, and for
// the same reason: a check that cannot be executed here is still worth
// pinning against drift.
func TestReadOnlyInspectionsOpenThroughTheSharedPath(t *testing.T) {
	for _, tc := range []struct{ file, marker string }{
		{"onboard.go", "func inspectSpineReadOnly("},
		{"spine_transfer.go", "func snapshotInspection("},
	} {
		raw, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatalf("read %s: %v", tc.file, err)
		}
		src := string(raw)

		start := strings.Index(src, tc.marker)
		if start < 0 {
			t.Fatalf("%s is gone from %s; this guard needs rewriting against whatever replaced it", tc.marker, tc.file)
		}
		body := src[start:]
		if end := strings.Index(body, "\nfunc "); end > 0 {
			body = body[:end]
		}

		if strings.Contains(body, "sql.Open(") {
			t.Fatalf("%s in %s calls sql.Open directly — the read-only open must go through substrate.OpenReadOnly, whose lock-domain guard and DSN it would otherwise bypass", tc.marker, tc.file)
		}
		if !strings.Contains(body, "substrate.OpenReadOnly(") {
			t.Fatalf("%s in %s no longer calls substrate.OpenReadOnly, the one shared read-only open path", tc.marker, tc.file)
		}
	}

	// The whole package: substrate.OpenReadOnly is the only builder of the
	// read-only DSN, so no verb file may spell mode=ro itself.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(raw), "mode=ro") {
			t.Fatalf("%s builds its own read-only DSN (mode=ro) — substrate.OpenReadOnly is the one place that string may be built", name)
		}
	}
}
