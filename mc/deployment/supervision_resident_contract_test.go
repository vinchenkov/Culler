package deployment

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGeneratedResidentConfigPassesTheResidentsOwnValidators feeds the written
// bundle to the actual TypeScript the resident runs at startup. It imports the
// real modules rather than restating their rules, so a rule that changes on the
// resident side fails here instead of ninety seconds into an operator-present
// activation.
func TestGeneratedResidentConfigPassesTheResidentsOwnValidators(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun is not on PATH; the resident-side contract cannot be executed here")
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	home, spec := supervisionFixture(t)
	if _, _, err := PrepareSupervision(home, spec, func(string) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, "supervision", "resident.json")

	// Importing main.ts would start a resident; these are the validators it runs
	// against the config before it does anything else.
	probe := filepath.Join(t.TempDir(), "probe.ts")
	source := `
import { configuredGitEnvironment } from "` + filepath.Join(repoRoot, "resident/src/git-identity") + `";
import { parseAgentsViewBind } from "` + filepath.Join(repoRoot, "resident/src/agentsview") + `";
const config = await Bun.file(process.argv[2]).json();
configuredGitEnvironment(config.gitAuthorName, config.gitAuthorEmail);
parseAgentsViewBind(config.agentsViewBind);
console.log("ok");
`
	if err := os.WriteFile(probe, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bun, "run", probe, configPath)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ok") {
		t.Fatalf("the resident rejects the config mc generates for it: %v\n%s", err, out)
	}
}
