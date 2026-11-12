package verbs

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"mc/dispatch"
)

func TestPackagerBriefPreservesItsRecords(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	dvExec(t, db, `UPDATE tasks SET description='The export includes the selected date.' WHERE id=1`)
	dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject, verdict_outcome, evidence_path)
		VALUES ('verifier', 'pipeline', 'verifier', 'ws-test', 1, 'pass', '/mc/artifacts/test/1/evidence.md')`)
	dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject, output_path, outcome)
		VALUES ('worker', 'pipeline', 'worker', 'ws-test', 1, '/mc/artifacts/test/1/report.md', 'completed')`)
	saveCustom(t, db, dispatch.RolePackager, "Use concise explanations.")
	brief := briefFor(t, db, dispatch.RolePackager, ptr64(1), briefInputs{})
	for _, heading := range []string{"Agent credentials", "Failure controls"} {
		if brief.part(heading) != nil {
			t.Errorf("Packager receives irrelevant mechanics: %s", heading)
		}
	}
	if brief.part(partTask).Text != "The export includes the selected date." ||
		brief.part(partRun).fact("Run identifier") != "run-packager" ||
		brief.part(partLatestVerdict).fact("Evidence file") != "/mc/artifacts/test/1/evidence.md" ||
		brief.part(partLastReport).fact("File") != "/mc/artifacts/test/1/report.md" {
		t.Fatal("The Packager lost its assignment or source records.")
	}
	text, err := renderRunBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	var custom map[string]any
	parseRenderedBrief(t, text).part(partCustomization).rows(t, &custom)
	if custom["instructions"] != "Use concise explanations." {
		t.Fatal("The Packager lost the operator's custom instructions.")
	}
	requireAgentText(t, "Packager brief", text, []string{
		"Use the Run identifier as `<run_id>`", "Use the Identifier as `<task>`",
		"Treat its statements as claims.", "Apply the directive's Verification summary requirements",
	}, []string{"host provider proxy", "credential update", "three counted failures", "claim that you must check"})
}

func TestPackagerBriefChangesOnlyExplanationsOfRetainedParts(t *testing.T) {
	for _, heading := range []string{
		partCustomization, partRun, "Project environment", partTask, partStrategicRationale,
		partLatestVerdict, partLastReport, partGitState, partPreviousPacket,
	} {
		original := briefPart{Heading: heading, Explanation: "Original explanation.",
			Facts:   []briefFact{{Label: "File", Value: "/mc/artifacts/test/1/source.md"}},
			TextTag: "source", Text: "Keep this source.", Rows: []string{"Keep this row."}}
		brief := runBrief{Parts: []briefPart{original}}
		trimPackagerBrief(&brief)
		if len(brief.Parts) != 1 {
			t.Fatalf("The Packager lost %s.", heading)
		}
		got := brief.Parts[0]
		got.Explanation = original.Explanation
		if !reflect.DeepEqual(got, original) {
			t.Errorf("The Packager changed the source data of %s.", heading)
		}
	}
}

func TestPackagerSkillPointersResolveToDeliveredFiles(t *testing.T) {
	set, err := RoleSkillSet(dispatch.RolePackager)
	if err != nil {
		t.Fatal(err)
	}
	directive, err := directiveForRole(dispatch.RolePackager)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	texts := map[string]string{"directive": directive}
	delivered := map[string]bool{}
	for _, skill := range set {
		delivered[skill.Name] = true
		dir := filepath.Join(root, ".agents", "skills", skill.Name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "SKILL.md"), skill.Body)
		if strings.HasPrefix(skill.Name, "packager-") || skill.Name == "mc-packager" {
			texts[skill.Name] = skill.Body
		}
	}
	for _, name := range []string{
		"mc-packager", "packager-browser", "packager-previous-packet",
		"project-environment-setup", "project-wiki", "task-description", "writing",
	} {
		if !delivered[name] || !strings.Contains(directive, installedSkillRoot+name+"/SKILL.md") {
			t.Errorf("The Packager cannot discover the delivered skill %s.", name)
		}
	}
	brief := runBrief{Parts: []briefPart{{Heading: partStrategicRationale}, {Heading: partPreviousPacket}}}
	trimPackagerBrief(&brief)
	for _, part := range brief.Parts {
		texts[part.Heading] = part.Explanation
	}
	pointer := regexp.MustCompile(`~/.agents/skills/([a-z-]+)/SKILL.md`)
	for source, body := range texts {
		for _, match := range pointer.FindAllStringSubmatch(body, -1) {
			path := filepath.Join(root, strings.TrimPrefix(match[0], "~/"))
			if data, err := os.ReadFile(path); err != nil || len(data) == 0 {
				t.Errorf("The %s pointer has no installed file: %s", source, match[0])
			}
		}
	}
}

func TestPackagerConditionalProceduresKeepTheirRequirements(t *testing.T) {
	for name, required := range map[string][]string{
		"packager-browser": {
			"Capture new media only for a required state that the Verifier evidence omits.",
			"--browser=chromium", "-s=packager-<run_id>", "screenshot --full-page",
			"video-start", "video-stop", "delete-data", "durable packet directory",
		},
		"packager-previous-packet": {
			"Its status 1 means a comparison conflict and is an expected result.",
			"current `Evidence file` in **Latest verdict**", "directive's **Verification summary**",
			"Retain prior evidence only when the difference leaves its subject unchanged.",
			"Place **Changes since the previous packet** directly after the first section.",
			"When the difference is empty, state that the reviewed change is the same.",
		},
		"mc-packager": {
			"`HEAD` must equal that commit", "status --porcelain=v1 --untracked-files=all",
			"nonempty regular file of at most 64 mebibytes", "at most 4096 bytes without control characters",
			"Use only an existing plane", "A refused submission records nothing.",
			"## A run without a packet", "## Project role customization",
			"### Novelty claims", "inspect the reviewed repository and report what you find, or omit the claim.",
		},
	} {
		requireAgentText(t, name, roleSkillBody(t, dispatch.RolePackager, name), required, nil)
	}
}

func TestPackagerVerificationSummaryHasOneInstructionSource(t *testing.T) {
	set, err := RoleSkillSet(dispatch.RolePackager)
	if err != nil {
		t.Fatal(err)
	}
	input := packagerDirective
	for _, skill := range set {
		if strings.HasPrefix(skill.Name, "packager-") || skill.Name == "mc-packager" {
			input += "\n" + skill.Body
		}
	}
	for _, rule := range []string{
		"Group checks by the behavior or criterion that they verify.",
		"Group unverified checks with a common cause into one concise explanation.",
		"State each shared coverage limit once, with its cause and the result that it qualifies.",
		"Use individual test names or raw output only when the operator needs that detail for the decision.",
		"The Verifier evidence retains detailed results and each exact `UNVERIFIED` entry.",
		"Identify that evidence as the source for individual check details.",
	} {
		if strings.Count(input, rule) != 1 {
			t.Errorf("The Packager must receive this rule once: %s", rule)
		}
	}
	for _, removed := range []string{
		"Preserve each `UNVERIFIED` entry from the Verifier's evidence.",
		"Each `UNVERIFIED` entry and the coverage that remains unverified.",
		"host provider proxy", "credential renewal", "three task run failure attempts",
		"In Culler for example", "Culler being a Project",
	} {
		if strings.Contains(input, removed) {
			t.Errorf("The Packager retains irrelevant or conflicting text: %s", removed)
		}
	}

}

func TestPackagerAdditionalSkillNamesMatchTheirDirectories(t *testing.T) {
	entries, err := fs.ReadDir(roleSkillFS, "skills/packager")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		body, err := roleSkillFS.ReadFile("skills/packager/" + entry.Name() + "/SKILL.md")
		if err != nil {
			t.Fatal(err)
		}
		if SkillFrontmatterField(string(body), "name") != entry.Name() {
			t.Errorf("The skill name differs from its directory: %s", entry.Name())
		}
	}
}
