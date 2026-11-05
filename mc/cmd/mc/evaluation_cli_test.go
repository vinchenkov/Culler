package main_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	operatorconfig "mc/config"
	"mc/evalcase"
)

func TestEvaluationCommandsRequireHostScope(t *testing.T) {
	home, spine, _ := agentSurfaceHome(t)
	env := append(runJSONEnv(t, spine, "case-agent", "pipeline", "packager"), "MC_HOME="+home)
	for _, args := range [][]string{{"evaluation", "status"}, {"evaluation", "start"}, {"evaluation", "submit"}, {"evaluation", "download"}, {"__evaluation-record", "pending"}} {
		result := runMC(t, env, "{}", args...)
		if result.code != 1 || !strings.Contains(result.stderr, "host scope only") {
			t.Fatalf("The scope check failed: %v %d %s", args, result.code, result.stderr)
		}
	}
}
func TestEvaluationCapturesEffectiveInputBeforeRepositoryCleanup(t *testing.T) {
	spine := initSpine(t)
	home := filepath.Dir(spine)
	task := proposalTask(t, spine, "Capture a real packet")
	arbiterPromoteAll(t, spine, dispatchThrough(t, spine, "spawn"))
	commit := workedTask(t, spine, task)
	artifacts := filepath.Join(t.TempDir(), "artifacts")
	plane := filepath.Join(artifacts, packagerPlaneTarget)
	if err := os.MkdirAll(plane, 0700); err != nil {
		t.Fatal(err)
	}
	allowlist := filepath.Join(home, "mount-allowlist")
	grant, err := os.OpenFile(allowlist, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(grant, "\n[[allow]]\npath = %q\ntarget = %q\naccess = \"rw\"\n", plane, packagerPlaneTarget)
	grant.Close()
	if err != nil {
		t.Fatal(err)
	}
	host := append(spineEnv(spine), "MC_ARTIFACTS_ROOT="+artifacts)
	envFor := func(run, role, root string) []string {
		path := filepath.Join(t.TempDir(), "identity.json")
		body, _ := json.Marshal(map[string]any{"run_id": run, "tier": "pipeline", "role": role, "mounts": map[string]any{"allow_write": []string{plane}}})
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		return append(append([]string{}, host...), "MC_RUN_JSON="+path, "MC_TASK_STORE_ROOT="+root)
	}
	verifier, verifierRoot := spawnRole(t, spine, "verifier")
	verifierID := verifier["run_id"].(string)
	evidence := writePlaneFile(t, plane, fmt.Sprintf("%d/verification/report.md", task), "The Verifier reported a pass. The fixture executes no additional source check.\n")
	result := runMC(t, envFor(verifierID, "verifier", verifierRoot), "", "verifier", "verdict", fmt.Sprint(task), "--run", verifierID, "--outcome", "pass", "--evidence", evidence, "--sha", commit)
	if result.code != 0 {
		t.Fatal(result.stderr)
	}
	packager, root := spawnRole(t, spine, "packager")
	runID := packager["run_id"].(string)
	cfgPath := filepath.Join(home, "config.toml")
	if _, err = operatorconfig.UpdateAtomic(cfgPath, func(c *operatorconfig.Config) error { c.Development.CaptureEvaluationCases = true; return nil }); err != nil {
		t.Fatal(err)
	}
	// This is the effective resident input, after the Project skill append.
	skills := append(packager["skills"].([]any), map[string]any{"name": "project-fixture", "content": "The effective Project skill text.", "path": "SKILL.md"})
	envelope := map[string]any{"brief": packager["brief"], "skills": skills, "role": "packager", "project": "ws-test", "subject_id": task,
		"runtime":        map[string]any{"harness": "fake", "model": "fixture-model", "access_token": "fixture-authentication-material-must-stay-private"},
		"authentication": "fixture-authentication-material-must-stay-private", "mounts": map[string]any{"session": "/mc/session"}}
	body, _ := json.Marshal(envelope)
	if err = os.WriteFile(filepath.Join(home, "runs", runID+".json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	policy, _ := json.Marshal(map[string]any{"entries": []any{map[string]any{"destination": plane, "access": "rw"}}})
	if err = os.WriteFile(filepath.Join(home, "runs", runID+".mounts.json"), policy, 0600); err != nil {
		t.Fatal(err)
	}
	request := fmt.Sprintf(`{"run_id":%q,"image":%q,"release":"fixture-release"}`, runID, "sha256:"+strings.Repeat("a", 64))
	result = runMC(t, host, request, "evaluation", "start")
	if result.code != 0 {
		t.Fatalf("The source capture failed: %s", result.stderr)
	}
	caseID := result.json["case_id"].(string)
	// A flag change does not revoke this run's capture authority.
	if _, err = operatorconfig.UpdateAtomic(cfgPath, func(c *operatorconfig.Config) error { c.Development.CaptureEvaluationCases = false; return nil }); err != nil {
		t.Fatal(err)
	}
	rendered := writePlaneFile(t, plane, fmt.Sprintf("%d/packet/report.html", task), "<!doctype html><html><body><h1>The original packet</h1></body></html>")
	result = runMC(t, envFor(runID, "packager", root), "", "complete", fmt.Sprint(task), "--run", runID, "--status", "packaged", "--outputs", rendered, "--thesis", "The original summary")
	if result.code != 0 {
		t.Fatal(result.stderr)
	}
	packet := reviewedPacket(t, spine, task)
	if _, err = os.Stat(filepath.Join(root, "repo")); !os.IsNotExist(err) {
		t.Fatal("The fixture did not remove the original run repository.")
	}
	result = runMC(t, host, fmt.Sprintf(`{"run_id":%q}`, runID), "evaluation", "finish")
	if result.code != 0 {
		t.Fatalf("The packet capture failed: %s", result.stderr)
	}
	store := evalcase.Store{Root: filepath.Join(home, "evals", "cases")}
	manifest, err := store.Load(caseID)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.Complete || manifest.Identity.Packet == 0 {
		t.Fatal("The capture lost its exact packet identity.")
	}
	if err = store.Validate(manifest); err != nil {
		t.Fatal(err)
	}
	dir, _ := store.Dir(caseID)
	if err = evalcase.VerifySource(dir, manifest); err != nil {
		t.Fatal(err)
	}
	effective, err := os.ReadFile(filepath.Join(dir, "input/effective.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(effective), "The effective Project skill text.") {
		t.Fatal("The capture lost the effective Project skill.")
	}
	if strings.Contains(string(effective), "fixture-authentication-material") {
		t.Fatal("The capture exposed an authentication token.")
	}
	result = runMC(t, host, fmt.Sprintf(`{"case_id":%q,"candidate_commit":%q,"destination_commit":%q}`, caseID, packet["candidate_commit"], packet["destination_commit"]), "evaluation", "inspect")
	if result.code != 0 {
		t.Fatalf("The candidate capture failed: %s", result.stderr)
	}
	observation, ok := result.json["observation"].(string)
	if !ok || observation == "" {
		t.Fatal("The displayed candidate has no preserved source.")
	}
	if _, err = store.ObservationArtifacts(caseID, observation, manifest.Identity); err != nil {
		t.Fatal(err)
	}
	unchanged, err := store.Load(caseID)
	if err != nil || len(unchanged.Artifacts) != len(manifest.Artifacts) {
		t.Fatal("The later candidate changed the original input.")
	}
	result = runMC(t, host, "", "evaluation", "recover")
	if result.code != 0 {
		t.Fatal(result.stderr)
	}
	yes, no, score := true, false, 3
	scores := map[string]*int{}
	for _, dimension := range evalcase.Dimensions {
		value := score
		scores[dimension] = &value
	}
	assessment := evalcase.Assessment{RequestID: "saved-before-reset", Identity: manifest.Identity, Scores: scores, Reason: "The original packet explains the source.", CriticalFailure: &no, FamiliarProject: true, SourceInspected: &yes, EvidenceInspected: &yes, RubricHash: evalcase.Hash(evalcase.Rubric), Wrapper: "<html>The original packet</html>", Display: json.RawMessage(`{"viewport":{"width":1280,"height":800}}`), Observation: observation}
	if _, err = store.Submit(caseID, assessment); err != nil {
		t.Fatal(err)
	}
	result = runMC(t, host, `{"case_id":"anything","assessment":{}}`, "evaluation", "submit")
	if result.code != 1 || !strings.Contains(result.stderr, "disabled") {
		t.Fatalf("The disabled flag accepted a submission: %s", result.stderr)
	}
	if err = os.Rename(spine, spine+".before-reset"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(spine+".before-reset", spine)
	result = runMC(t, host, `{}`, "evaluation", "list")
	if result.code != 0 || len(result.json["cases"].([]any)) != 1 {
		t.Fatalf("The database reset hid the saved case: %s", result.stderr)
	}
	result = runMC(t, host, fmt.Sprintf(`{"case_id":%q}`, caseID), "evaluation", "download")
	if result.code != 0 || result.json["archive_base64"] == "" {
		t.Fatalf("The database reset prevented the archive: %s", result.stderr)
	}
}
