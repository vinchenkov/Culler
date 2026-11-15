package verbs

import (
	"encoding/base64"
	"regexp"
	"regexp/syntax"
	"strconv"
	"strings"
	"testing"

	"mc/dispatch"
)

func TestFrozenRoleDirectivesCoverEverySpawnRole(t *testing.T) {
	roles := []dispatch.Role{
		dispatch.RoleStrategist,
		dispatch.RoleArbiter,
		dispatch.RoleWorker,
		dispatch.RoleVerifier,
		dispatch.RolePackager,
	}
	seen := map[string]dispatch.Role{}
	for _, role := range roles {
		directive, err := directiveForRole(role)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if prior, duplicate := seen[directive]; duplicate {
			t.Errorf("%s and %s share an undifferentiated directive", prior, role)
		}
		seen[directive] = role
	}
}

func TestUnknownSpawnRoleHasNoFallbackDirective(t *testing.T) {
	if _, err := directiveForRole(dispatch.Role("unknown")); err == nil {
		t.Fatal("unknown role received a fallback directive")
	}
}

// The role directive and the skills name a part of the run brief by its
// heading. A name that the run brief cannot render sends the agent to a part
// that does not exist.
func TestAgentTextNamesOnlyThePartsThatTheRunBriefRenders(t *testing.T) {
	headings := map[string]bool{}
	for _, heading := range []string{
		partCustomization, partRun, partTask, partStrategicRationale, partRevision, partRequirementsRevision, partMergeCandidate, partCorrection, partLastReport,
		partLatestVerdict, partGitState, partPreviousPacket, partMergedTasks,
		partPool, partActiveTasks, partRejected, partCancelled,
		partProposalDecisions, partProject, partVision,
	} {
		headings[heading] = true
	}
	named := regexp.MustCompile(`\*\*([^*]+)\*\* part`)
	for _, role := range allBriefRoles() {
		directive, err := directiveForRole(role)
		if err != nil {
			t.Fatalf("%s directive: %v", role, err)
		}
		skills, err := RoleSkillSet(role)
		if err != nil {
			t.Fatalf("%s skills: %v", role, err)
		}
		texts := map[string]string{"directive": directive}
		for _, skill := range skills {
			texts["skill "+skill.Name] = skill.Body
		}
		for surface, text := range texts {
			for _, match := range named.FindAllStringSubmatch(strings.Join(strings.Fields(text), " "), -1) {
				if !headings[match[1]] {
					t.Errorf("%s %s names the part %q, and the run brief has no such part", role, surface, match[1])
				}
			}
		}
	}
}

// A wiki update run has no task. The Scribe directive and the Scribe skills
// can name only the parts and the facts that the run brief of a wiki update
// holds.
func TestScribeTextNamesOnlyThePartsAndFactsOfItsRunBrief(t *testing.T) {
	db := dvSpine(t)
	// The run brief holds each of its parts and facts when the rows of the
	// merged tasks exceed the byte budget of their JSON block.
	briefWikiSucceeded(t, db, wrCommitA)
	briefPackagedTask(t, db, 1, "older task", strings.Repeat("a", mergedTasksBudget))
	briefMergeTask(t, db, 1)
	briefPackagedTask(t, db, 2, "newer task", "criterion")
	briefMergeTask(t, db, 2)
	parts, facts := map[string]bool{}, map[string]bool{}
	for _, part := range scribeBrief(t, db, wrCommitB).Parts {
		parts[part.Heading] = true
		for label := range part.Facts {
			facts[label] = true
		}
	}
	for _, heading := range []string{partCustomization, partRun, partProject, partGitState, partMergedTasks} {
		if !parts[heading] {
			t.Fatalf("the Scribe run brief holds no %q part: %v", heading, parts)
		}
	}
	if !facts["Omitted tasks"] {
		t.Fatalf("the Scribe run brief holds no fact of the omitted tasks: %v", facts)
	}

	directive, err := directiveForRole(dispatch.RoleScribe)
	if err != nil {
		t.Fatal(err)
	}
	skills, err := RoleSkillSet(dispatch.RoleScribe)
	if err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{"directive": directive}
	for _, skill := range skills {
		texts["skill "+skill.Name] = skill.Body
	}
	namedPart := regexp.MustCompile(`\*\*([^*]+)\*\* part`)
	namedFact := regexp.MustCompile("`([^`]+)` fact")
	for surface, text := range texts {
		flat := strings.Join(strings.Fields(text), " ")
		for _, match := range namedPart.FindAllStringSubmatch(flat, -1) {
			if !parts[match[1]] {
				t.Errorf("the Scribe %s names the part %q, and the run brief of a wiki update has no such part", surface, match[1])
			}
		}
		for _, match := range namedFact.FindAllStringSubmatch(flat, -1) {
			if !facts[match[1]] {
				t.Errorf("the Scribe %s names the fact %q, and the run brief of a wiki update has no such fact", surface, match[1])
			}
		}
	}
}

func TestEverySpawnRoleShipsTheDependencySkill(t *testing.T) {
	for _, role := range allSpawnRoles {
		skills, err := RoleSkillSet(role)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, skill := range skills {
			if skill.Name == "project-environment-setup" {
				found = true
			}
		}
		if !found {
			t.Errorf("The %s input omits the dependency skill.", role)
		}
	}
}

// `mc packet decide` refuses a decision on an obsolete packet version. The
// Collie skill quotes that refusal.
func TestCollieSkillQuotesTheObsoletePacketRefusal(t *testing.T) {
	skill, err := CollieSkill()
	if err != nil {
		t.Fatal(err)
	}
	body := skill.Files["references/packet-decisions.md"]
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusPackaged, 0))
	packet, version := dvPublishPacket(t, db, 1, "", dvOld)
	for decision, reason := range map[string]string{"approve": "", "revise": "Add the missing evidence."} {
		_, err := PacketDecide(db, nil, 1, decision, reason, packet, version+1)
		if err == nil {
			t.Fatalf("%s of an obsolete version succeeded", decision)
		}
		if !strings.Contains(body, err.Error()) {
			t.Errorf("the procedure omits the refusal %q", err.Error())
		}
	}
}

// The packet procedures of the Collie skill read the lines of a real packet
// context and the fields of the current packet record. The media reference
// format of the skill must be the format of the builder. The decode command of
// the skill must find the same data addresses as the builder, in the same order.
func TestCollieSkillProceduresMatchThePacketContext(t *testing.T) {
	skill, err := CollieSkill()
	if err != nil {
		t.Fatal(err)
	}
	png := []byte("\x89PNG\r\n\x1a\nfixture-pixels")
	f := newPCFixture(t, "packet.html", []byte(`<img src="data:image/png;base64,`+base64.StdEncoding.EncodeToString(png)+`">`), true)
	text := f.build(t)
	for _, label := range []string{"Destination commit", "Merge candidate commit", "Report file",
		"Verifier evidence", "Change data inventory", "Change data patch"} {
		if !strings.Contains(text, "\n- "+label+": ") {
			t.Errorf("the packet context has no %q line:\n%s", label, text)
		}
	}
	template := "[media: <type>, <n> bytes]"
	reference := strings.NewReplacer("<type>", "image/png", "<n>", strconv.Itoa(len(png))).Replace(template)
	if !strings.Contains(skill.Files["references/packet-decisions.md"], "`"+template+"`") || !strings.Contains(text, reference) {
		t.Errorf("the packet context does not hold the reference %q of the skill:\n%s", reference, text)
	}

	_, rest, found := strings.Cut(skill.Files["references/packet-decisions.md"], "matchAll(/")
	source, _, closed := strings.Cut(rest, "/gi)")
	if !found || !closed {
		t.Fatal("the collie skill has no case-insensitive decode expression")
	}
	// In JavaScript, \s also matches Unicode space characters. RE2 matches
	// only the ASCII space characters.
	if strings.Contains(source, `\s`) || strings.Contains(source, `\S`) {
		t.Errorf("the decode expression %q uses a space class that JavaScript and RE2 read differently", source)
	}
	want, err := syntax.Parse(packetMediaAddress.String(), syntax.Perl)
	if err != nil {
		t.Fatal(err)
	}
	got, err := syntax.Parse("(?i)"+source, syntax.Perl)
	if err != nil {
		t.Fatalf("the decode expression %q: %v", source, err)
	}
	if !got.Equal(want) {
		t.Errorf("the decode expression %q differs from the data address expression %q", source, packetMediaAddress.String())
	}

	// A stall refuses the approval of the current packet. The current packet
	// record then shows the same identifiers, and only `ready` shows the cause.
	dvExec(t, f.db, `UPDATE tasks SET dispatch_retries=0 WHERE id=1`)
	if _, err := PacketDecide(f.db, nil, 1, "approve", "", f.packet, f.version); err == nil || err.Error() != "the reviewed packet is obsolete" {
		t.Fatalf("the approval of a stalled task gave %v", err)
	}
	list, err := PacketList(f.db, PacketSelection{Task: 1})
	if err != nil {
		t.Fatal(err)
	}
	row := list.(map[string]any)["packets"].([]map[string]any)[0]
	if row["packet_id"] != f.packet || row["version_id"] != f.version || row["review_state"] != "awaits_decision" || row["ready"] != int64(0) {
		t.Fatalf("the current packet record of a stalled task is %v", row)
	}
}

func requireAgentText(t *testing.T, source, text string, want, stale []string) {
	t.Helper()
	for _, required := range want {
		if !strings.Contains(text, required) {
			t.Errorf("the %s omits %q", source, required)
		}
	}
	for _, removed := range stale {
		if strings.Contains(text, removed) {
			t.Errorf("the %s still states %q", source, removed)
		}
	}
}
