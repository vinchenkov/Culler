package verbs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"mc/dispatch"
	"mc/domain"
)

const (
	srRationale   = "Proposed change: the export holds the selected date.\n\nObserved: `export.go` writes no date field.\nAssumed: each client reads the date."
	srDescription = "Scope: the export command.\n\n- The export holds the selected date."
)

// srStrategistRun gives a Strategist run the Project lease.
func srStrategistRun(t *testing.T, db Q, run string) *RunIdentity {
	t.Helper()
	if _, err := domain.Claim(context.Background(), db, time.Now(), domain.ClaimArgs{Project: "ws-test", RunID: run, Owner: "strategist", HardDeadlineMinutes: 60}); err != nil {
		t.Fatal(err)
	}
	return &RunIdentity{Tier: "pipeline", Role: "strategist(propose)", RunID: run}
}

func srBatch(t *testing.T, proposals ...map[string]any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"proposals": proposals})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestStrategistProposeRefusesAProposalWithoutAStrategicRationale(t *testing.T) {
	db := dvSpine(t)
	id := srStrategistRun(t, db, "propose")
	valid := map[string]any{"title": "Add the date to the export", "description": srDescription, "strategic_rationale": srRationale}
	for name, second := range map[string]map[string]any{
		"absent":     {"title": "Add the format to the export", "description": srDescription},
		"empty":      {"title": "Add the format to the export", "description": srDescription, "strategic_rationale": ""},
		"blank":      {"title": "Add the format to the export", "description": srDescription, "strategic_rationale": " \n\t "},
		"over limit": {"title": "Add the format to the export", "description": srDescription, "strategic_rationale": strings.Repeat("x", domain.MaxStrategicRationaleBytes+1)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := StrategistPropose(db, id, "propose", strings.NewReader(srBatch(t, valid, second)))
			if err == nil {
				t.Fatal("The command accepted a proposal without a valid Strategic rationale.")
			}
			if name != "over limit" && !strings.Contains(err.Error(), "proposal 1: strategic_rationale is required") {
				t.Fatalf("The refusal does not name the absent field: %v", err)
			}
			if got := dfInt(t, db, `SELECT COUNT(*) FROM tasks`); got != 0 {
				t.Fatalf("The refused batch created %d tasks.", got)
			}
			if got := dfInt(t, db, `SELECT COUNT(*) FROM leases WHERE project='ws-test' AND run_id='propose'`); got != 1 {
				t.Fatal("The refused batch released the lease.")
			}
			if got := dfInt(t, db, `SELECT ended_at IS NULL FROM runs WHERE id='propose'`); got != 1 {
				t.Fatal("The refused batch ended the run.")
			}
		})
	}

	result, err := StrategistPropose(db, id, "propose", strings.NewReader(srBatch(t, valid,
		map[string]any{"title": "Add the format to the export", "description": "Scope: the format.", "strategic_rationale": "Observed: the export has one format."})))
	if err != nil {
		t.Fatal(err)
	}
	ids := result.(map[string]any)["task_ids"].([]int64)
	if len(ids) != 2 {
		t.Fatalf("The batch created %d tasks, want 2.", len(ids))
	}
	var description, rationale, origin, status string
	if err := db.QueryRow(`SELECT description,strategic_rationale,origin,status FROM tasks WHERE id=?`, ids[0]).Scan(&description, &rationale, &origin, &status); err != nil {
		t.Fatal(err)
	}
	if description != srDescription || rationale != srRationale || origin != "autonomous" || status != "proposed" {
		t.Fatalf("The proposal stores description %q, rationale %q, origin %q, and status %q.", description, rationale, origin, status)
	}

	// The Arbiter receives each stored proposal with its rationale.
	brief, err := buildRunBrief(context.Background(), db, &dispatch.Spawn{Project: "ws-test", Role: dispatch.RoleArbiter, ProposedPool: ids}, "arbiter-run", briefInputs{})
	if err != nil {
		t.Fatal(err)
	}
	pool := brief.part(partPool).Rows.([]briefBoardRow)
	if len(pool) != 2 || pool[0].StrategicRationale != srRationale || pool[1].StrategicRationale != "Observed: the export has one format." {
		t.Fatalf("The Arbiter pool lost a rationale: %+v", pool)
	}
}

// srAgentInput builds the run brief of a run. It returns the brief, its
// rendered text, and the complete agent input: the rendered brief with its
// role directive, then the skills of the role.
func srAgentInput(t *testing.T, db Q, sp *dispatch.Spawn) (runBrief, renderedBrief, string) {
	t.Helper()
	brief, err := buildRunBrief(context.Background(), db, sp, "run-"+string(sp.Role), briefInputs{})
	if err != nil {
		t.Fatal(err)
	}
	text, err := renderRunBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	rendered := parseRenderedBrief(t, text)
	skills, err := RoleSkillSet(sp.Role)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range skills {
		text += "\n" + skill.Body
	}
	return brief, rendered, text
}

func TestStrategicRationaleReachesTheArbiterAndThePackagerOnly(t *testing.T) {
	db := dvSpine(t)
	const activeRationale = "Observed: the report has no index."
	dvInsertStrategistTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2), srRationale)
	dvInsertStrategistTask(t, db, dvTask(2, dispatch.ScopeTask, dispatch.StatusSeeded, 2), activeRationale)
	dvInsertTask(t, db, dvTask(3, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	dvExec(t, db, `UPDATE tasks SET description=?`, srDescription)

	t.Run("Arbiter", func(t *testing.T) {
		brief, rendered, _ := srAgentInput(t, db, &dispatch.Spawn{Project: "ws-test", Role: dispatch.RoleArbiter, ProposedPool: []int64{1}})
		var pool, active []map[string]any
		rendered.part(partPool).rows(t, &pool)
		rendered.part(partActiveTasks).rows(t, &active)
		if len(pool) != 1 || pool[0]["strategic_rationale"] != srRationale || pool[0]["description"] != srDescription {
			t.Fatalf("The proposal lost its rationale or its description: %v", pool)
		}
		for _, row := range active {
			if _, ok := row["strategic_rationale"]; ok {
				t.Fatalf("An active task carries a rationale: %v", row)
			}
		}
		if !strings.Contains(brief.part(partPool).Explanation, "`strategic_rationale` holds the Strategist's claims and evidence for you to check") {
			t.Fatalf("The pool explanation does not name the rationale: %s", brief.part(partPool).Explanation)
		}
		for _, rule := range []string{
			"Use the rationale when you assess those three properties.",
			"Treat its statements as claims: check the cited evidence in the repository copy, and give an assumption less weight than an observation.",
			"The rationale adds no requirement; the description holds the scope and the criteria.",
		} {
			if !strings.Contains(brief.Directive, rule) {
				t.Errorf("The Arbiter directive omits: %s", rule)
			}
		}
	})

	subject := int64(2)
	for _, role := range []dispatch.Role{dispatch.RoleWorker, dispatch.RoleVerifier} {
		t.Run(string(role), func(t *testing.T) {
			brief, _, text := srAgentInput(t, db, &dispatch.Spawn{Project: "ws-test", Role: role, SubjectID: &subject})
			if brief.part(partStrategicRationale) != nil {
				t.Fatal("The brief holds a Strategic rationale part.")
			}
			if brief.part(partTask).Text != srDescription {
				t.Fatalf("The task part holds %q.", brief.part(partTask).Text)
			}
			if strings.Contains(strings.ToLower(text), "rationale") || strings.Contains(text, activeRationale) {
				t.Fatal("The agent input names or holds the Strategic rationale.")
			}
		})
	}

	t.Run("Packager of a task that the Strategist proposed", func(t *testing.T) {
		brief, rendered, _ := srAgentInput(t, db, &dispatch.Spawn{Project: "ws-test", Role: dispatch.RolePackager, SubjectID: &subject})
		part := rendered.part(partStrategicRationale)
		if part == nil || part.Text != activeRationale {
			t.Fatalf("The Packager lost the rationale: %+v", part)
		}
		if got := rendered.part(partTask).Text; got != srDescription {
			t.Fatalf("The task part holds %q.", got)
		}
		for i, p := range brief.Parts {
			if p.Heading == partTask && brief.Parts[i+1].Heading != partStrategicRationale {
				t.Fatalf("The rationale does not follow the task: %s", brief.Parts[i+1].Heading)
			}
		}
	})

	t.Run("Packager of operator work", func(t *testing.T) {
		operator := int64(3)
		brief, _, _ := srAgentInput(t, db, &dispatch.Spawn{Project: "ws-test", Role: dispatch.RolePackager, SubjectID: &operator})
		if brief.part(partStrategicRationale) != nil {
			t.Fatal("The brief of operator work holds a Strategic rationale part.")
		}
	})
}

func TestTaskReadGivesTheStrategicRationaleToItsReaders(t *testing.T) {
	db := dvSpine(t)
	dvInsertStrategistTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2), srRationale)
	dvInsertTask(t, db, dvTask(2, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	dvExec(t, db, `UPDATE tasks SET description=?`, srDescription)
	read := func(id *RunIdentity, task int64) map[string]any {
		t.Helper()
		row, err := TaskRead(db, id, task)
		if err != nil {
			t.Fatal(err)
		}
		return row.(map[string]any)
	}
	readers := map[string]*RunIdentity{
		"dashboard":  nil,
		"Collie":     {Tier: "collie", Role: "collie"},
		"Arbiter":    {Tier: "pipeline", Role: "arbiter"},
		"Packager":   {Tier: "pipeline", Role: "packager"},
		"Strategist": {Tier: "pipeline", Role: "strategist(propose)"},
	}
	for name, id := range readers {
		row := read(id, 1)
		if row["strategic_rationale"] != srRationale || row["description"] != srDescription || row["origin"] != "autonomous" {
			t.Errorf("%s reads rationale %v and description %v.", name, row["strategic_rationale"], row["description"])
		}
		if operator := read(id, 2); operator["strategic_rationale"] != nil || operator["origin"] != "user" {
			t.Errorf("%s reads a rationale on operator work: %v", name, operator["strategic_rationale"])
		}
	}
	for _, role := range []string{"worker", "verifier"} {
		row := read(&RunIdentity{Tier: "pipeline", Role: role}, 1)
		if _, ok := row["strategic_rationale"]; ok {
			t.Errorf("The %s task read holds the rationale.", role)
		}
		if row["description"] != srDescription {
			t.Errorf("The %s task read lost the description.", role)
		}
	}
}

// srPackage moves a task to a published packet through the real role
// terminals. It returns the packet identifier.
func srPackage(t *testing.T, f *deliveryFixture, task int64, thesis string) int64 {
	t.Helper()
	f.advance(task, "feature.txt", fmt.Sprintf("the change of task %d\n", task), "verified")
	run, _, id := f.run(task, "packager")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "packaged", Outputs: f.artifact(run, "The packet."), Thesis: thesis}); err != nil {
		t.Fatal(err)
	}
	f.cleanup(task, run, "")
	f.merge(task, "prepare")
	var packet int64
	if err := f.db.QueryRow(`SELECT current_review_id FROM tasks WHERE id=?`, task).Scan(&packet); err != nil {
		t.Fatal(err)
	}
	return packet
}

func TestPacketOpeningTextFollowsTheTaskOrigin(t *testing.T) {
	const statement = "The export now holds the selected date."
	context1 := func(f *deliveryFixture, packet int64) string {
		t.Helper()
		text, err := BuildPacketContext(context.Background(), f.db, f.artifacts, packet)
		if err != nil {
			t.Fatal(err)
		}
		return text
	}

	t.Run("task that the Strategist proposed", func(t *testing.T) {
		f := newDeliveryFixture(t)
		var task int64
		f.transaction(func(ctx context.Context, q Q) error {
			var err error
			task, err = domain.BirthProposal(ctx, q, domain.ProposalArgs{Title: "Add the date to the export", Description: srDescription,
				StrategicRationale: srRationale, Origin: "autonomous", Project: "ws-test"})
			if err != nil {
				return err
			}
			return domain.Promote(ctx, q, task)
		})
		packet := srPackage(t, f, task, statement)
		opening := statement + "\n\n" + srRationale
		row := packetRow(t, f.db, true, task)
		if row["thesis"] != opening || row["origin"] != "autonomous" {
			t.Fatalf("The review queue receives origin %v and opening text %q.", row["origin"], row["thesis"])
		}
		text := context1(f, packet)
		if !strings.Contains(text, "## Packet content\n\n### Strategic rationale\n\n"+opening+"\n\n### Criteria\n\n"+srDescription+"\n\n") {
			t.Fatalf("The packet conversation does not open with the Strategic rationale:\n%s", text)
		}
		if strings.Contains(text, "### Thesis") {
			t.Fatal("The packet conversation of a task that the Strategist proposed holds a Thesis section.")
		}
		var description string
		if err := f.db.QueryRow(`SELECT description FROM tasks WHERE id=?`, task).Scan(&description); err != nil || description != srDescription {
			t.Fatalf("The description changed: %q, %v", description, err)
		}
	})

	t.Run("operator work", func(t *testing.T) {
		f := newDeliveryFixture(t)
		task := f.add("Add the format to the export")
		packet := srPackage(t, f, task, statement)
		row := packetRow(t, f.db, true, task)
		if row["thesis"] != statement || row["origin"] != "user" {
			t.Fatalf("The review queue receives origin %v and opening text %q.", row["origin"], row["thesis"])
		}
		text := context1(f, packet)
		if !strings.Contains(text, "## Packet content\n\n### Thesis\n\n"+statement+"\n\n### Criteria\n\n") {
			t.Fatalf("The packet conversation does not open with the Thesis:\n%s", text)
		}
		if strings.Contains(text, "Strategic rationale") {
			t.Fatal("The packet conversation of operator work holds a Strategic rationale section.")
		}
	})
}

// The Packager learns the first section of each task origin from its directive
// and its skill. The Strategist learns the rationale procedure from its own.
func TestRoleInputsDescribeTheStrategicRationale(t *testing.T) {
	skill := func(role dispatch.Role) string {
		t.Helper()
		set, err := RoleSkillSet(role)
		if err != nil {
			t.Fatal(err)
		}
		return set[0].Body
	}
	directive := func(role dispatch.Role) string {
		t.Helper()
		text, err := directiveForRole(role)
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	for _, test := range []struct {
		name, text string
		want       []string
	}{
		{"Strategist directive", directive(dispatch.RoleStrategist), []string{
			"Each proposal also holds a Strategic rationale, separate from its description.",
			"The description holds every requirement of the task.",
			"Write the Strategic rationale of each candidate to its own file under `/tmp` before you file the batch.",
			"- State the proposed change in one or two sentences.",
			"- Cite the evidence for the problem or the opportunity: what fails or is absent now.",
			"- Explain how the task advances the Vision.",
			"- Explain why the expected benefit justifies the effort.",
			"- Explain why the task deserves priority now.",
			"- Separate what you observed from what you assume.",
			"When the written rationale does not justify the task, discard that candidate and research again.",
			"In each `strategic_rationale` field, supply the text of the candidate's rationale file without changes.",
		}},
		{"Strategist skill", skill(dispatch.RoleStrategist), []string{
			"| `strategic_rationale` | Required; the text of the proposal's rationale file.",
			"| `description` | Optional in the command schema; contains the scope and the success criteria. |",
		}},
		{"Packager directive", directive(dispatch.RolePackager), []string{
			"The first section is the only section that gives the opening overview and the\njustification of the work.",
			"- The heading of the first section is **Strategic rationale**. The packet has\n  no **Summary** section.",
			"- The section starts with your statement of the delivered change, in one or\n  two sentences.",
			"- The complete rationale of the Strategist follows, with its evidence.",
			"- The heading of the first section is **Summary**. The section holds the same\n  text that you submit with `--thesis`.",
			"- The packet has no **Strategic rationale** section.",
			"1. **Strategic rationale** or **Summary** — the first section, as its rules\n   above state.",
			"The first section of a task that the Strategist proposed stays first and keeps its heading.",
			"Culler stores the result as the opening text of the packet.",
		}},
		{"Packager skill", skill(dispatch.RolePackager), []string{
			"For a task that the Strategist proposed, `mc` stores your text and then the Strategic rationale of the task as the opening text of the packet.",
		}},
	} {
		for _, want := range test.want {
			if !strings.Contains(test.text, want) {
				t.Errorf("The %s omits: %s", test.name, want)
			}
		}
	}
}
