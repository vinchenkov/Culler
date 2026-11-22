package deployment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReleaseBindProducerHasNoFailOpen is the G-B drift guard. The producer
// (resident effects) must mint every run envelope with a REQUIRED release
// identity through one choke; a `?? "development"` fallback or an omitted field
// would make the container-side mixed-build bind vacuous (that fail-open was
// the secondary defect the design named). This reads the TypeScript producer
// the same way the log-event registry guard reads the log module.
func TestReleaseBindProducerHasNoFailOpen(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(repoRoot, "resident", "src", "effects.ts"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, `release_build_id: config.releaseBuildId ??`) {
		t.Error("the run envelope reintroduced a fallback release identity; it must be required")
	}
	if got := strings.Count(text, "release_build_id: requiredReleaseBuildId(config.releaseBuildId)"); got != 2 {
		t.Errorf("expected both the pipeline and collie envelopes to bind the required release identity, found %d", got)
	}
	if !strings.Contains(text, "function requiredReleaseBuildId") {
		t.Error("the required-release-identity choke is missing from the producer")
	}
}
