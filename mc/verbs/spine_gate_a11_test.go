package verbs

import (
	"path/filepath"
	"strings"
	"testing"

	"mc/substrate"
)

// G-D/G-E coverage (A11): the onboarding write paths open the spine directly
// (bypassing verbs.OpenSpine), so requireSpineFile — their shared
// precondition — must refuse a drifted schema exactly like the write gate.
func TestRequireSpineFileGatesSchemaDrift(t *testing.T) {
	t.Run("drifted-existing-spine-refused", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "spine.db")
		provisionSpine(t, path)
		drift, err := substrate.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := drift.Exec(`ALTER TABLE tasks ADD COLUMN a11_probe TEXT`); err != nil {
			t.Fatal(err)
		}
		drift.Close()
		err = requireSpineFile(path)
		if err == nil {
			t.Fatal("requireSpineFile accepted a drifted existing spine")
		}
		if !strings.Contains(err.Error(), "No onboarding write ran") {
			t.Fatalf("error = %v, want the onboarding schema-gate refusal", err)
		}
	})
	t.Run("matching-spine-passes", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "spine.db")
		provisionSpine(t, path)
		if err := requireSpineFile(path); err != nil {
			t.Fatalf("requireSpineFile refused a matching spine: %v", err)
		}
	})
	t.Run("absent-spine-unchanged", func(t *testing.T) {
		// The pre-existing semantic stays exact: missing file is reported,
		// never created, and the message is unchanged.
		path := filepath.Join(t.TempDir(), "spine.db")
		err := requireSpineFile(path)
		if err == nil || !strings.Contains(err.Error(), "no spine at") {
			t.Fatalf("missing-spine error = %v, want the unchanged no-spine refusal", err)
		}
	})
}
