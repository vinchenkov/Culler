package evalcase

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testCase(t *testing.T) (Store, Manifest, Assessment) {
	t.Helper()
	s := Store{filepath.Join(t.TempDir(), "cases")}
	repo := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.test"}} {
		if _, err := git(repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "source.txt"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "source.txt"}, {"commit", "-m", "Base"}} {
		if _, err := git(repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	base, _ := git(repo, "rev-parse", "HEAD")
	_ = os.WriteFile(filepath.Join(repo, "source.txt"), []byte("result\n"), 0600)
	_ = os.WriteFile(filepath.Join(repo, "image.bin"), []byte{0, 1, 2, 3}, 0600)
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "Result"}} {
		if _, err := git(repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	commit, _ := git(repo, "rev-parse", "HEAD")
	_ = os.WriteFile(filepath.Join(repo, "untracked-token"), []byte("credential-fixture-must-remain-private"), 0600)
	m := Manifest{FormatVersion: 1, CaseID: "case-fixture", Group: "deployment/project/1", Authorized: true, Complete: true,
		Identity: Identity{Deployment: "deployment", Project: "project", Task: 1, Requirements: 1, Packet: 2, Version: 3, Run: "original-run", PacketHash: Hash([]byte("packet")), Commit: strings.TrimSpace(string(commit))},
		Absences: map[string]string{"later_observations": "No later observations exist."}, ReferenceStatus: "Source facts require review."}
	artifacts, err := s.CaptureSource(m.CaseID, repo, map[string]string{"base": strings.TrimSpace(string(base)), "submitted": m.Identity.Commit})
	if err != nil {
		t.Fatal(err)
	}
	m.Artifacts = artifacts
	for name, body := range map[string]string{"input/effective.json": `{"brief":"Original /workspace/source input for original-run","skills":[{"name":"project","content":"original text"}]}`, "input/records.json": `{"original":true}`, "input/access.json": `{}`, "input/inventory.json": `[]`, "input/runtime.json": `{"image":"fixture"}`, "packet/report": "packet", "packet/records.json": `{"packet":2}`, "evidence/result.txt": "The producer reported a pass. The case performs no check."} {
		a, err := s.Put(m.CaseID, name, []byte(body), "fixture", "text/plain")
		if err != nil {
			t.Fatal(err)
		}
		m.Artifacts = append(m.Artifacts, a)
	}
	if err = s.SaveManifest(m); err != nil {
		t.Fatal(err)
	}
	yes, no := true, false
	score := 3
	scores := map[string]*int{}
	for _, key := range Dimensions {
		v := score
		scores[key] = &v
	}
	a := Assessment{RequestID: "request-one", Identity: m.Identity, Scores: scores, Reason: "The packet explains the source and its limits.", CriticalFailure: &no, FamiliarProject: true, SourceInspected: &yes, EvidenceInspected: &no, RubricHash: Hash(Rubric), Wrapper: "<html>packet</html>", Display: json.RawMessage(`{"viewport":{"width":1280,"height":800}}`)}
	// Source reconstruction must work without the original repository.
	if err = os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	return s, m, a
}

// The submitted commit holds a merge of the destination. The reviewed diff
// holds only the change of the work.
func TestCaptureSourceStartsTheReviewedDiffAtTheSharedDestinationCommit(t *testing.T) {
	s := Store{filepath.Join(t.TempDir(), "cases")}
	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := git(repo, args...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-b", "culler"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.test"}} {
		run(args...)
	}
	write("source.txt", "base\n")
	run("add", ".")
	run("commit", "-m", "Base")
	base := run("rev-parse", "HEAD")
	run("checkout", "-b", "task")
	write("source.txt", "result\n")
	run("commit", "-am", "Result")
	run("checkout", "culler")
	write("other.txt", "another contribution\n")
	run("add", ".")
	run("commit", "-m", "Other")
	destination := run("rev-parse", "HEAD")
	run("checkout", "task")
	run("merge", "--no-ff", "-m", "Merge the destination", "culler")
	submitted := run("rev-parse", "HEAD")
	if _, err := s.CaptureSource("case-merged", repo, map[string]string{"base": base, "submitted": submitted, "destination": destination}); err != nil {
		t.Fatal(err)
	}
	dir, err := s.Dir("case-merged")
	if err != nil {
		t.Fatal(err)
	}
	diff, err := os.ReadFile(filepath.Join(dir, "source/reviewed.diff"))
	if err != nil || !bytes.Contains(diff, []byte("source.txt")) || bytes.Contains(diff, []byte("other.txt")) {
		t.Fatalf("the reviewed diff is not the change of the work alone: %v\n%s", err, diff)
	}
}
func TestCaseReconstructsAndImportsAfterSourceRemoval(t *testing.T) {
	s, m, a := testCase(t)
	dir, _ := s.Dir(m.CaseID)
	if err := VerifySource(dir, m); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.Submit(m.CaseID, a)
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err = s.Archive(m.CaseID, &archive); err != nil {
		t.Fatal(err)
	}
	target := Store{filepath.Join(t.TempDir(), "cases")}
	imported, err := target.Import(bytes.NewReader(archive.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if imported.Identity != m.Identity {
		t.Fatal("The import changed the original identity.")
	}
	receipts, err := target.Receipts(m.CaseID)
	if err != nil || len(receipts) != 1 || receipts[0] != receipt {
		t.Fatalf("The receipt changed: %v %v", receipts, err)
	}
	replay := filepath.Join(t.TempDir(), "replay")
	if err = target.ReplayInput(m.CaseID, replay, []Substitution{{"original-run", "synthetic-run"}, {"/workspace/source", "/isolated/source"}}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(replay, "input/input/effective.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "original-run") || !strings.Contains(string(body), "synthetic-run") {
		t.Fatal("The replay identities are not isolated.")
	}
	_ = filepath.WalkDir(replay, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(path, "assessment") || strings.Contains(path, "packet/report") {
			t.Errorf("The replay exposes feedback: %s", path)
		}
		return nil
	})
	original, _ := os.ReadFile(filepath.Join(replay, "original/input/effective.json"))
	if !strings.Contains(string(original), "original-run") {
		t.Fatal("The original input changed.")
	}
}
func TestSubmissionRetryRevisionAndTamper(t *testing.T) {
	s, m, a := testCase(t)
	for _, alter := range []func(*Identity){func(id *Identity) { id.Packet++ }, func(id *Identity) { id.Version++ }, func(id *Identity) { id.Project = "another-project" }, func(id *Identity) { id.PacketHash = "another-report" }} {
		changed := a
		alter(&changed.Identity)
		if _, err := s.Submit(m.CaseID, changed); err == nil {
			t.Fatal("The score accepted another packet identity.")
		}
	}
	first, err := s.Submit(m.CaseID, a)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.Submit(m.CaseID, a)
	if err != nil || retry != first {
		t.Fatalf("The retry changed the receipt: %v", err)
	}
	changed := a
	changed.Reason = "Another reason."
	if _, err = s.Submit(m.CaseID, changed); err == nil {
		t.Fatal("The request identifier accepted a changed assessment.")
	}
	a.RequestID = "request-two"
	a.Supersedes = 1
	a.Reason = "The second assessment preserves the first."
	second, err := s.Submit(m.CaseID, a)
	if err != nil || second.Revision != 2 {
		t.Fatalf("The revision failed: %v", err)
	}
	receipts, _ := s.Receipts(m.CaseID)
	if len(receipts) != 2 || receipts[0] != first {
		t.Fatal("The first revision changed.")
	}
	dir, _ := s.Dir(m.CaseID)
	_ = os.WriteFile(filepath.Join(dir, "packet/report"), []byte("tamper"), 0600)
	if _, err = s.Submit(m.CaseID, a); err == nil {
		t.Fatal("The retry ignored an artifact hash change.")
	}
	var archive bytes.Buffer
	if err = s.Archive(m.CaseID, &archive); err == nil {
		t.Fatal("The archive accepted changed bytes.")
	}
}
func TestAssessmentRejectsUnknownAndInvalidValues(t *testing.T) {
	_, _, valid := testCase(t)
	for _, mutate := range []func(*Assessment){
		func(a *Assessment) { a.Scores = map[string]*int{} }, func(a *Assessment) { a.CriticalFailure = nil },
		func(a *Assessment) { a.SourceInspected = nil }, func(a *Assessment) { a.FamiliarProject = false }, func(a *Assessment) { a.Reason = " " },
		func(a *Assessment) { a.RubricHash = "wrong" }, func(a *Assessment) { yes := true; a.CriticalFailure = &yes },
	} {
		a := valid
		mutate(&a)
		if ValidateAssessment(a) == nil {
			t.Fatal("The incomplete assessment passed.")
		}
	}
	for _, score := range []string{"0.5", "-1", "5", "null", "\"3\""} {
		body, _ := json.Marshal(valid)
		var wire map[string]json.RawMessage
		_ = json.Unmarshal(body, &wire)
		var scores map[string]json.RawMessage
		_ = json.Unmarshal(wire["scores"], &scores)
		scores["clarity"] = json.RawMessage(score)
		wire["scores"], _ = json.Marshal(scores)
		body, _ = json.Marshal(wire)
		var a Assessment
		err := json.Unmarshal(body, &a)
		if err == nil && ValidateAssessment(a) == nil {
			t.Fatalf("The invalid score passed: %s", score)
		}
	}
}
func TestPrivateStoreRejectsTraversalSymlinksAndPartialSubmission(t *testing.T) {
	s, m, a := testCase(t)
	for _, name := range []string{"../escape", "/escape", "nested/../../escape"} {
		if _, err := s.Put(m.CaseID, name, []byte("x"), "fixture", "text/plain"); err == nil {
			t.Fatal("The traversal passed.")
		}
	}
	dir, _ := s.Dir(m.CaseID)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(m.CaseID, "escape/secret", []byte("x"), "fixture", "text/plain"); err == nil {
		t.Fatal("The symbolic link passed.")
	}
	// A storage obstruction prevents any successful assessment receipt.
	if err := os.WriteFile(filepath.Join(dir, "assessments"), []byte("obstruction"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit(m.CaseID, a); err == nil {
		t.Fatal("The partial write returned success.")
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("The case directory is not private.")
	}
}

func TestRetentionAndReplayKeepAssessmentBoundaries(t *testing.T) {
	s, m, a := testCase(t)
	if _, err := s.Submit(m.CaseID, a); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneUnscored(m.CaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(m.CaseID); err != nil {
		t.Fatal("Artifact expiry removed a saved case.")
	}
	if err := s.ReplayInput(m.CaseID, filepath.Join(t.TempDir(), "replay"), nil); err == nil {
		t.Fatal("The replay accepted the original execution identity.")
	}
	for _, role := range []string{"packager", "judge"} {
		dest := filepath.Join(t.TempDir(), "trial")
		if err := s.Trial(m.CaseID, 1, role, dest, []Substitution{{m.Identity.Run, "synthetic-trial"}, {"/workspace/source", "/isolated/source"}}); err != nil {
			t.Fatal(err)
		}
		_ = filepath.WalkDir(filepath.Join(dest, "input"), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.Contains(b, []byte(a.Reason)) || strings.Contains(path, "assessment.json") {
				t.Errorf("The %s trial contains human feedback.", role)
			}
			return nil
		})
	}
	other, unsaved, _ := testCase(t)
	if err := other.PruneUnscored(unsaved.CaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Load(unsaved.CaseID); !os.IsNotExist(err) {
		t.Fatal("Artifact expiry retained an unscored capture.")
	}
}
func TestCaptureRejectsAuthenticationMaterialWithoutDisclosure(t *testing.T) {
	s := Store{filepath.Join(t.TempDir(), "cases")}
	token := `{"access_token":"fixture-private-authentication-token-value"}`
	_, err := s.Put("case-token", "evidence/token.json", []byte(token), "fixture", "application/json")
	if err == nil || strings.Contains(err.Error(), "fixture-private-authentication-token-value") {
		t.Fatal("The token boundary failed.")
	}
	dir, _ := s.Dir("case-token")
	if _, err = os.Stat(filepath.Join(dir, "evidence/token.json")); !os.IsNotExist(err) {
		t.Fatal("The capture retained authentication material.")
	}
}

func TestLaterSourceObservationRemainsOutsideReplay(t *testing.T) {
	s, m, a := testCase(t)
	original, err := os.ReadFile(filepath.Join(s.Root, m.CaseID, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	observations := Store{Root: filepath.Join(s.Root, m.CaseID, "observations")}
	observation := Manifest{FormatVersion: 1, CaseID: "candidate-observation", Group: m.Group, Identity: m.Identity, Authorized: true}
	for _, artifact := range m.Artifacts {
		if !strings.HasPrefix(artifact.Path, "source/") {
			continue
		}
		body, _, err := s.ReadArtifact(m, artifact.Path)
		if err != nil {
			t.Fatal(err)
		}
		copied, err := observations.Put(observation.CaseID, artifact.Path, body, "later candidate", artifact.MediaType)
		if err != nil {
			t.Fatal(err)
		}
		observation.Artifacts = append(observation.Artifacts, copied)
	}
	if err = observations.SaveManifest(observation); err != nil {
		t.Fatal(err)
	}
	a.Observation = observation.CaseID
	if _, err = s.Submit(m.CaseID, a); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := os.ReadFile(filepath.Join(s.Root, m.CaseID, "manifest.json"))
	if !bytes.Equal(original, unchanged) {
		t.Fatal("The assessment changed the original manifest.")
	}
	var archive bytes.Buffer
	if err = s.Archive(m.CaseID, &archive); err != nil {
		t.Fatal(err)
	}
	imported := Store{Root: filepath.Join(t.TempDir(), "cases")}
	if _, err = imported.Import(&archive); err != nil {
		t.Fatal(err)
	}
	if _, err = imported.ObservationArtifacts(m.CaseID, a.Observation, m.Identity); err != nil {
		t.Fatal(err)
	}
	replay := filepath.Join(t.TempDir(), "trial")
	if err = imported.Trial(m.CaseID, 1, "judge", replay, []Substitution{{m.Identity.Run, "trial-run"}, {"/workspace/source", "/isolated/source"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(replay, "input", "observations")); !os.IsNotExist(err) {
		t.Fatal("The trial received a later observation.")
	}
	wrapper, err := os.ReadFile(filepath.Join(replay, "input", "packet", "wrapper.html"))
	if err != nil || string(wrapper) != a.Wrapper {
		t.Fatal("The trial lost the preserved display.")
	}
}
