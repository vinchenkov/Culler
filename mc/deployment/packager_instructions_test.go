package deployment

import (
	"os"
	"strings"
	"testing"
)

func TestPackagerEvidenceRuleSeparatesRecordsFromPresentation(t *testing.T) {
	doc, err := os.ReadFile("../../AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), "evidence file and Review Packet") ||
		!strings.Contains(string(doc), "The Verifier preserves each `UNVERIFIED` entry in the evidence file") ||
		!strings.Contains(string(doc), "**Verification summary** in `mc/verbs/directives/packager.md`") {
		t.Fatal("The repository evidence rule conflicts with packet presentation.")
	}
	directive, err := os.ReadFile("../verbs/directives/packager.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(directive), "## Verification summary\n") ||
		!strings.Contains(string(directive), "The Verifier evidence retains detailed results and each exact `UNVERIFIED` entry.") {
		t.Fatal("The repository rule points to an absent or incomplete summary requirement.")
	}
}
