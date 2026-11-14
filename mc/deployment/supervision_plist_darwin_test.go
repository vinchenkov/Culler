package deployment

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSupervisionPropertyListsPassPlatformValidation(t *testing.T) {
	home, spec := supervisionFixture(t)
	if _, _, err := PrepareSupervision(home, spec, func(string) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"resident.plist", "dashboard.plist", "agentsview.plist"} {
		path := filepath.Join(home, "supervision", "LaunchAgents", name)
		if out, err := exec.Command("/usr/bin/plutil", "-lint", path).CombinedOutput(); err != nil {
			t.Fatalf("property list validation %s: %v: %s", name, err, out)
		}
		if name == "resident.plist" || name == "dashboard.plist" {
			out, err := exec.Command("/usr/bin/plutil", "-extract", "ProcessType", "raw", "-o", "-", path).CombinedOutput()
			if err != nil || strings.TrimSpace(string(out)) != "Standard" {
				t.Fatalf("process policy %s: %v: %s", name, err, out)
			}
		}
	}
}
