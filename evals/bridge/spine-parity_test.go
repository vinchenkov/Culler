package verbs

import (
	"context"
	"encoding/json"
	"mc/dispatch"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestEvalPackagerSpineParity(t *testing.T) {
	t.Run("current_verdict_input", testEvalPackagerVerdictInput)
	f := newDeliveryFixture(t)
	task := f.add("Explain the reviewed contribution")
	f.advance(task, "feature.txt", "The reviewed contribution.\n", "verified")
	run, root, identity := f.run(task, "packager")
	packet := f.artifact(run, "The packet explains the contribution.")
	args := CompleteArgs{Task: task, Run: run, Status: "packaged", Outputs: packet, Thesis: "The contribution changes the behavior."}
	refuse := func(name string, candidate CompleteArgs) {
		t.Helper()
		if _, err := Complete(f.db, identity, candidate); err == nil {
			t.Fatalf("%s received an unexpected successful terminal", name)
		}
	}
	wrong := args
	wrong.Run = "another-run"
	refuse("a different run", wrong)
	wrong = args
	wrong.Task++
	refuse("a different task", wrong)
	wrong = args
	wrong.Thesis = ""
	refuse("an empty thesis", wrong)
	writeFile(t, packet, "")
	refuse("an empty packet", args)
	writeFile(t, packet, "<h1>The accepted contribution.</h1>")
	wrong = args
	wrong.Outputs = filepath.Join(t.TempDir(), "packet.html")
	writeFile(t, wrong.Outputs, "<h1>Outside the artifact plane.</h1>")
	refuse("an unmounted packet", wrong)
	link := filepath.Join(f.artifacts, "test", "linked.html")
	if err := os.Symlink(packet, link); err != nil {
		t.Fatal(err)
	}
	wrong = args
	wrong.Outputs = link
	refuse("a symbolic link packet", wrong)
	dirty := filepath.Join(root, "repo", "unexpected.txt")
	writeFile(t, dirty, "An uncommitted change.")
	refuse("a changed repository", args)
	if err := os.Remove(dirty); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repo")
	start := srcGit(t, repo, "rev-parse", "HEAD")
	srcGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "An unauthorized commit")
	refuse("a different HEAD", args)
	srcGit(t, repo, "reset", "--hard", start)
	if _, err := Complete(f.db, identity, args); err != nil {
		t.Fatalf("the repaired packet received a refusal: %v", err)
	}
	var recorded string
	if err := f.db.QueryRow("SELECT report_path FROM tasks WHERE id=?", task).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	writeFile(t, packet, "The role changed its submitted file afterward.")
	body, err := os.ReadFile(recorded)
	if err != nil || string(body) != "<h1>The accepted contribution.</h1>" {
		t.Fatalf("the spine failed to retain the accepted packet: %q, %v", body, err)
	}
	refuse("a second successful terminal", args)
}

func testEvalPackagerVerdictInput(t *testing.T) {
	db := dvSpine(t)
	task := dvTask(1, dispatch.ScopeTask, dispatch.StatusVerified, 2)
	dvInsertTask(t, db, task)
	for _, verdict := range []struct{ id, at, outcome, evidence, correction string }{
		{"earlier-correct", "2026-01-01 00:00:00", "correct", "evidence/earlier.md", "corrections/earlier.md"},
		{"current-pass", "2026-01-01 00:01:00", "pass", "evidence/current.md", ""},
	} {
		dvExec(t, db, `INSERT INTO runs
			(id, tier, role, project, subject, created_at, verdict_outcome, evidence_path, correction_path)
			VALUES (?, 'pipeline', 'verifier', 'ws-test', 1, ?, ?, ?, NULLIF(?, ''))`,
			verdict.id, verdict.at, verdict.outcome, verdict.evidence, verdict.correction)
	}
	brief, err := buildRunBrief(context.Background(), db, &dispatch.Spawn{
		Role: dispatch.RolePackager, Project: "ws-test", SubjectID: &task.ID,
	}, "eval-current-verdict", briefInputs{})
	if err != nil {
		t.Fatal(err)
	}
	storedParts := []string{"Verdict history", "Scribe report", "Earlier requirements"}
	storedFacts := []string{"Verified source commit", "Scribe input commit", "Scribe result commit", "Scribe state", "Wiki source baseline"}
	for _, part := range brief.Parts {
		if slices.Contains(storedParts, part.Heading) || part.Heading == partCorrection {
			t.Fatalf("The product Packager received an obsolete part: %s", part.Heading)
		}
		for _, fact := range part.Facts {
			if slices.Contains(storedFacts, fact.Label) {
				t.Fatalf("The product Packager received an obsolete fact: %s", fact.Label)
			}
		}
	}
	// A stored case brief holds the current parts, the obsolete parts, and the obsolete Git facts.
	git := briefPart{Heading: partGitState, Explanation: "These are the Git facts of this work.",
		Facts: []briefFact{{"Submitted commit", "stored-submitted-commit"}}}
	for _, label := range storedFacts {
		git.Facts = append(git.Facts, briefFact{label, "obsolete-fact-value"})
	}
	parts := []briefPart{git}
	for _, part := range brief.Parts {
		if part.Heading != partGitState {
			parts = append(parts, part)
		}
	}
	for _, heading := range storedParts {
		parts = append(parts, briefPart{Heading: heading, Text: "Obsolete evaluation metadata.", TextTag: "history"})
	}
	var wireParts []map[string]any
	for _, part := range parts {
		wireParts = append(wireParts, map[string]any{
			"heading": part.Heading, "explanation": part.Explanation, "facts": part.Facts,
			"text_tag": part.TextTag, "text": part.Text, "rows": part.Rows,
		})
	}
	request, err := json.Marshal(map[string]any{
		"operation": "input", "run_id": brief.RunID, "directive": "The editable directive.", "parts": wireParts,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := EvalContract(request)
	if err != nil {
		t.Fatal(err)
	}
	output := result.(map[string]any)
	input := output["input"].(string)
	for _, part := range brief.Parts {
		if !strings.Contains(input, "## "+part.Heading) {
			t.Fatalf("The evaluation omitted a current product part: %s", part.Heading)
		}
	}
	if !strings.Contains(input, "evidence/current.md") || strings.Contains(input, "evidence/earlier.md") || strings.Contains(input, "Obsolete evaluation metadata.") {
		t.Fatalf("The evaluation changed the latest-verdict policy: %s", input)
	}
	if !strings.Contains(input, "- Submitted commit: stored-submitted-commit\n") || strings.Contains(input, "obsolete-fact-value") {
		t.Fatalf("The evaluation changed the Git facts of the Packager: %s", input)
	}
	if !slices.Equal(output["omitted_parts"].([]string), storedParts) {
		t.Fatalf("The evaluation reported these omitted parts: %v", output["omitted_parts"])
	}
	if !slices.Equal(output["omitted_facts"].([]string), storedFacts) {
		t.Fatalf("The evaluation reported these omitted facts: %v", output["omitted_facts"])
	}
}
