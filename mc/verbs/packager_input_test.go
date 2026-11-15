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
	}, []string{"three counted failures", "claim that you must check"})
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
