package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"mc/deployment"
	"mc/evalcase"
	"mc/repository"
	"mc/verbs"
)

type evaluationRequest struct {
	Candidate   string              `json:"candidate_commit"`
	Destination string              `json:"destination_commit"`
	Run         string              `json:"run_id"`
	CaseID      string              `json:"case_id"`
	Enabled     bool                `json:"enabled"`
	Project     string              `json:"project"`
	Task        int64               `json:"task_id"`
	Packet      int64               `json:"packet_id"`
	Version     int64               `json:"version_id"`
	Status      string              `json:"status"`
	Error       string              `json:"error"`
	Hash        string              `json:"packet_sha256"`
	Image       string              `json:"image"`
	Release     string              `json:"release"`
	Assessment  evalcase.Assessment `json:"assessment"`
}

func evalDecode(r io.Reader, v any) error {
	b, err := io.ReadAll(io.LimitReader(r, 16*1024*1024+1))
	if err != nil {
		return err
	}
	if len(b) > 16*1024*1024 {
		return verbs.Usagef("The case request exceeds the size limit.")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(v); err != nil {
		return verbs.Usagef("The case request is invalid: %v", err)
	}
	if d.Decode(new(any)) != io.EOF {
		return verbs.Usagef("The case request contains extra data.")
	}
	return nil
}
func evaluationFrame(action string, request evaluationRequest) (map[string]any, error) {
	body, _ := json.Marshal(request)
	var output, diagnostic bytes.Buffer
	if run([]string{"__evaluation-record", action}, bytes.NewReader(body), &output, &diagnostic) != 0 {
		return nil, fmt.Errorf("The capture record operation failed: %s", diagnostic.String())
	}
	var result map[string]any
	err := json.Unmarshal(output.Bytes(), &result)
	return result, err
}
func commandEvaluationRecord(args []string, stdin io.Reader) (any, error) {
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err = verbs.RequireHostScope(id, "evaluation records"); err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, verbs.Usagef("An evaluation record action is required.")
	}
	var r evaluationRequest
	if err = evalDecode(stdin, &r); err != nil {
		return nil, err
	}
	return withSpine(func(db *sql.DB) (any, error) {
		switch args[0] {
		case "capture":
			return verbs.EvaluationCaptureRecord(db, r.Run)
		case "records":
			return verbs.EvaluationRecords(db, r.Run)
		case "register":
			return verbs.EvaluationRegister(db, r.Run, r.CaseID, r.Enabled)
		case "status":
			return verbs.EvaluationStatus(db, r.Run, r.Status, r.Error, r.Packet, r.Hash)
		case "lookup":
			return verbs.EvaluationLookup(db, r.Project, r.Task, r.Packet, r.Version)
		case "trace":
			return verbs.EvaluationTrace(db, r.Run)
		case "expired":
			return verbs.EvaluationExpiredCases(db, r.Task)
		case "pending":
			return verbs.EvaluationPending(db)
		}
		return nil, verbs.Usagef("The evaluation record action is unsupported.")
	})
}
func commandEvaluation(args []string, stdin io.Reader) (any, error) {
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err = verbs.RequireHostScope(id, "evaluation cases"); err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, verbs.Usagef("An evaluation case action is required.")
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return nil, err
	}
	s := evalcase.Store{Root: filepath.Join(home, "evals", "cases")}
	if args[0] == "import" {
		return s.Import(stdin)
	}
	if args[0] == "recover" {
		return recoverEvaluation(home, s)
	}
	var r evaluationRequest
	if err = evalDecode(stdin, &r); err != nil {
		return nil, err
	}
	switch args[0] {
	case "start":
		return startEvaluation(home, s, r)
	case "finish":
		return finishEvaluation(home, s, r.Run)
	case "status":
		enabled, err := verbs.EvaluationCaptureEnabled()
		if err != nil {
			return nil, err
		}
		result, err := evaluationFrame("lookup", r)
		if err != nil {
			return nil, err
		}
		result["enabled"] = enabled
		result["rubric"] = json.RawMessage(evalcase.Rubric)
		result["rubric_sha256"] = evalcase.Hash(evalcase.Rubric)
		if caseID, ok := result["case_id"].(string); ok {
			if result["available"] != true {
				_, _ = finishEvaluation(home, s, fmt.Sprint(result["run_id"]))
				result, err = evaluationFrame("lookup", r)
				if err != nil {
					return nil, err
				}
				result["enabled"] = enabled
				result["rubric"] = json.RawMessage(evalcase.Rubric)
				result["rubric_sha256"] = evalcase.Hash(evalcase.Rubric)
			}
			m, err := s.Load(caseID)
			if err == nil {
				result["identity"] = m.Identity
				result["case_id"] = caseID
				result["available"] = m.Complete && result["available"] == true
				result["receipts"], _ = s.Receipts(caseID)
			} else {
				result["available"] = false
				result["error"] = "The source capture is unavailable: " + err.Error()
			}
		}
		return result, nil
	case "submit":
		enabled, err := verbs.EvaluationCaptureEnabled()
		if err != nil {
			return nil, err
		}
		if !enabled {
			return nil, verbs.Domainf("Capture evaluation cases is disabled. Your draft remains available.")
		}
		// The server checks the exact ownership independently of browser fields.
		lookup, err := evaluationFrame("lookup", evaluationRequest{Project: r.Assessment.Identity.Project, Task: r.Assessment.Identity.Task, Packet: r.Assessment.Identity.Packet, Version: r.Assessment.Identity.Version})
		if err != nil {
			return nil, err
		}
		if lookup["case_id"] != r.CaseID || lookup["packet_sha256"] != r.Assessment.Identity.PacketHash {
			return nil, verbs.Domainf("The assessment packet identity is invalid.")
		}
		return s.Submit(r.CaseID, r.Assessment)
	case "inspect":
		m, err := s.Load(r.CaseID)
		if err != nil {
			return nil, err
		}
		if err = s.Validate(m); err != nil {
			return nil, err
		}
		result := map[string]any{"identity": m.Identity, "absences": m.Absences}
		evidence := []map[string]any{}
		for _, a := range m.Artifacts {
			if a.Path == "source/reviewed.diff" || a.Path == "packet/report" || a.Path == "packet/records.json" {
				body, err := os.ReadFile(filepath.Join(s.Root, m.CaseID, a.Path))
				if err != nil {
					return nil, err
				}
				result[a.Path] = string(body)
			}
			if strings.HasPrefix(a.Path, "evidence/") {
				row := map[string]any{"origin": a.Origin, "sha256": a.SHA256, "media_type": a.MediaType}
				if strings.HasPrefix(a.MediaType, "text/") || a.MediaType == "application/json" {
					body, err := os.ReadFile(filepath.Join(s.Root, m.CaseID, a.Path))
					if err != nil {
						return nil, err
					}
					row["text"] = string(body)
				}
				evidence = append(evidence, row)
			}
		}
		result["evidence"] = evidence
		if r.Candidate != "" {
			observation, diff, err := captureEvaluationObservation(s, m, r.Candidate, r.Destination)
			if err != nil {
				return nil, err
			}
			result["observation"] = observation
			result["candidate_diff"] = diff
		}
		return result, nil
	case "download":
		var archive bytes.Buffer
		if err = s.Archive(r.CaseID, &archive); err != nil {
			return nil, err
		}
		return map[string]any{"case_id": r.CaseID, "archive_base64": base64.StdEncoding.EncodeToString(archive.Bytes())}, nil
	case "list":
		entries, err := os.ReadDir(s.Root)
		if os.IsNotExist(err) {
			return map[string]any{"cases": []any{}}, nil
		}
		if err != nil {
			return nil, err
		}
		cases := []any{}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			m, err := s.Load(e.Name())
			if err != nil {
				continue
			}
			receipts, err := s.Receipts(e.Name())
			if err == nil && len(receipts) > 0 {
				cases = append(cases, map[string]any{"manifest": m, "receipts": receipts})
			}
		}
		return map[string]any{"cases": cases}, nil
	}
	return nil, verbs.Usagef("The evaluation case action is unsupported.")
}
func evalRows(records map[string]any, key string) []map[string]any {
	out := []map[string]any{}
	rows, _ := records[key].([]any)
	for _, v := range rows {
		if row, ok := v.(map[string]any); ok {
			out = append(out, row)
		}
	}
	return out
}
func evalNumber(v any) int64 { n, _ := strconv.ParseInt(fmt.Sprint(v), 10, 64); return n }
func evalText(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func evalJSON(v any) []byte { b, _ := json.MarshalIndent(v, "", "  "); return append(b, '\n') }
func evalPut(s evalcase.Store, m *evalcase.Manifest, path string, b []byte, origin, media string) error {
	for _, a := range m.Artifacts {
		if a.Path == path {
			if a.SHA256 != evalcase.Hash(b) {
				return fmt.Errorf("The original artifact changed: %s", path)
			}
			return nil
		}
	}
	a, err := s.Put(m.CaseID, path, b, origin, media)
	if err != nil {
		return err
	}
	m.Artifacts = append(m.Artifacts, a)
	return s.SaveManifest(*m)
}
func startEvaluation(home string, s evalcase.Store, r evaluationRequest) (any, error) {
	enabled, err := verbs.EvaluationCaptureEnabled()
	if err != nil {
		return nil, err
	}
	prior, err := evaluationFrame("capture", evaluationRequest{Run: r.Run})
	if err != nil {
		return nil, err
	}
	existing := evalRows(prior, "capture")
	if len(existing) == 0 && !enabled {
		return map[string]any{"enabled": false}, nil
	}
	names, err := deployment.RuntimeNames(home)
	if err != nil {
		return nil, err
	}
	caseID := "case-" + names.Suffix + "-" + r.Run
	records, err := evaluationFrame("register", evaluationRequest{Run: r.Run, CaseID: caseID, Enabled: enabled})
	if err != nil {
		return nil, err
	}
	captures := evalRows(records, "capture")
	if len(captures) != 1 {
		return nil, fmt.Errorf("The Packager source assignment is unavailable.")
	}
	if evalNumber(captures[0]["enabled"]) != 1 {
		return map[string]any{"enabled": false}, nil
	}
	caseID = evalText(captures[0]["case_id"])
	unlock, err := s.Lock(caseID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if _, err = s.Load(caseID); err == nil {
		return map[string]any{"case_id": caseID}, nil
	}
	tasks := evalRows(records, "task")
	if len(tasks) != 1 {
		return nil, fmt.Errorf("The original task is unavailable.")
	}
	task := tasks[0]
	m := evalcase.Manifest{FormatVersion: 1, CaseID: caseID, Group: fmt.Sprintf("%s/%s/%d", records["deployment"], records["project_id"], evalNumber(records["task_id"])), Authorized: true,
		Identity: evalcase.Identity{Deployment: evalText(records["deployment"]), Project: evalText(records["project_id"]), Task: evalNumber(records["task_id"]), Requirements: evalNumber(task["requirements_version"]), Version: evalNumber(captures[0]["version_id"]), Run: r.Run},
		Absences: map[string]string{"effective_model": "The harness did not report an effective model.", "commands": "The original run has no separate command transcript. The preserved brief and evidence retain their recorded claims.", "production_observations": "No later production observation belongs to the original input."}, ReferenceStatus: "Source facts require a separate reference review."}
	for _, v := range evalRows(records, "versions") {
		if evalNumber(v["id"]) == m.Identity.Version {
			m.Identity.Commit = evalText(v["result_commit"])
		}
	}
	if _, err = s.Dir(caseID); err != nil {
		return nil, err
	}
	if err = s.SaveManifest(m); err != nil {
		return nil, err
	}
	image := ""
	fail := func(err error) (any, error) {
		_, _ = evaluationFrame("status", evaluationRequest{Run: r.Run, Status: "failed", Error: err.Error()})
		if image != "" {
			return map[string]any{"case_id": caseID, "enabled": true, "runner_image": image, "status": "failed", "error": err.Error()}, nil
		}
		return nil, err
	}
	// Read only the public effective fields. Authentication material and mount identities remain outside the capture.
	path := filepath.Join(home, "runs", r.Run+".json")
	body, err := os.ReadFile(path)
	if err != nil {
		return fail(err)
	}
	var envelope map[string]json.RawMessage
	if err = json.Unmarshal(body, &envelope); err != nil {
		return fail(err)
	}
	effective := map[string]json.RawMessage{}
	for _, key := range []string{"brief", "skills", "skill_problems", "role", "project", "subject_id", "harness", "model_binding", "repository", "harness_config"} {
		if value, ok := envelope[key]; ok {
			effective[key] = value
		}
	}
	var publicRuntime map[string]json.RawMessage
	_ = json.Unmarshal(envelope["runtime"], &publicRuntime)
	runtime := map[string]json.RawMessage{}
	for _, key := range []string{"harness", "model", "id", "channel"} {
		if value, ok := publicRuntime[key]; ok {
			runtime[key] = value
		}
	}
	var baseURL string
	if value, ok := publicRuntime["base_url"]; ok {
		if err = json.Unmarshal(value, &baseURL); err != nil {
			return fail(err)
		}
		parsed, parseErr := url.Parse(baseURL)
		if parseErr != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fail(fmt.Errorf("The runtime address contains private or ambiguous configuration."))
		}
		runtime["base_url"] = value
	}
	effective["runtime"] = evalJSON(runtime)
	if len(effective["brief"]) == 0 || len(effective["skills"]) == 0 {
		return fail(fmt.Errorf("The original effective brief or skills are absent."))
	}
	var plan struct {
		Entries []struct {
			Destination string `json:"destination"`
			Access      string `json:"access"`
		} `json:"entries"`
	}
	policyBody, err := os.ReadFile(filepath.Join(home, "runs", r.Run+".mounts.json"))
	if err != nil {
		return fail(err)
	}
	if err = json.Unmarshal(policyBody, &plan); err != nil {
		return fail(err)
	}
	for _, entry := range plan.Entries {
		if strings.HasPrefix(entry.Destination, verbs.ArtifactsMountRoot()+"/") {
			m.ResourceRoots = append(m.ResourceRoots, entry.Destination)
		}
	}
	if len(m.ResourceRoots) == 0 {
		return fail(fmt.Errorf("The original artifact access policy is absent."))
	}
	if err = evalPut(s, &m, "input/access.json", evalJSON(plan), "resident artifact access policy", "application/json"); err != nil {
		return fail(err)
	}
	if err = evalPut(s, &m, "input/effective.json", evalJSON(effective), "resident effective input", "application/json"); err != nil {
		return fail(err)
	}
	var brief string
	if err = json.Unmarshal(effective["brief"], &brief); err != nil {
		return fail(err)
	}
	if err = evalPut(s, &m, "input/brief.txt", []byte(brief), "resident effective run brief", "text/plain"); err != nil {
		return fail(err)
	}
	if err = evalPut(s, &m, "input/skills.json", effective["skills"], "resident effective skill delivery order", "application/json"); err != nil {
		return fail(err)
	}
	if _, directive, found := strings.Cut(brief, "<role_directive>\n"); found {
		if text, _, found := strings.Cut(directive, "\n</role_directive>"); found {
			if err = evalPut(s, &m, "input/directive.md", []byte(text), "resident effective role directive", "text/markdown"); err != nil {
				return fail(err)
			}
		}
	}
	if err = evalPut(s, &m, "input/records.json", evalJSON(records), "spine at Packager launch", "application/json"); err != nil {
		return fail(err)
	}
	image = r.Image
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(image) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		resolved, inspectErr := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", image).Output()
		cancel()
		image = strings.TrimSpace(string(resolved))
		if inspectErr != nil || !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(image) {
			return fail(fmt.Errorf("The immutable runner image identity is unavailable."))
		}
	}
	if r.Release == "" {
		return fail(fmt.Errorf("The deployed Culler release identity is unavailable."))
	}
	if err = evalPut(s, &m, "input/runtime.json", evalJSON(map[string]any{"image": image, "release": r.Release, "source_access": "The Packager reads the assigned repository and artifact mounts."}), "resident launch", "application/json"); err != nil {
		return fail(err)
	}
	if r.Image == "" {
		m.Absences["runner_image"] = "The resident did not supply an immutable runner image identity."
	}
	if r.Release == "" {
		m.Absences["release"] = "The resident did not supply a release identity."
	}
	if err = captureEvaluationInventory(home, s, &m); err != nil {
		return fail(err)
	}
	if err = captureOriginalSource(home, s, &m, records); err != nil {
		return fail(err)
	}
	if err = captureEvaluationEvidence(home, s, &m, records, "evidence"); err != nil {
		return fail(err)
	}
	return map[string]any{"case_id": caseID, "enabled": true, "runner_image": image}, s.SaveManifest(m)
}
func captureOriginalSource(home string, s evalcase.Store, m *evalcase.Manifest, records map[string]any) error {
	commits := map[string]string{"submitted": m.Identity.Commit}
	tasks := evalRows(records, "task")
	if len(tasks) == 1 {
		task := tasks[0]
		if v := evalText(task["input_commit"]); v != "" {
			commits["base"] = v
		}
		for _, v := range evalRows(records, "versions") {
			n := evalNumber(v["id"])
			commits[fmt.Sprintf("version-%d", n)] = evalText(v["result_commit"])
			commits[fmt.Sprintf("input-%d", n)] = evalText(v["input_commit"])
			if n == evalNumber(task["source_version"]) {
				commits["verified"] = evalText(v["result_commit"])
			}
		}
	}
	for _, c := range evalRows(records, "candidates") {
		for _, key := range []string{"candidate_commit", "destination_commit"} {
			if v := evalText(c[key]); v != "" {
				commits[fmt.Sprintf("%s-%d", key, evalNumber(c["id"]))] = v
			}
		}
	}
	root := filepath.Join(home, "runs", "repositories", m.Identity.Run, "repo")
	if destination, err := (repository.Git{Root: root}).Commit(context.Background(), repository.DestinationRef); err == nil {
		commits["destination"] = destination
	}
	artifacts, err := s.CaptureSource(m.CaseID, root, commits, evalText(records["workspace_root"]))
	if err != nil {
		return err
	}
	m.Artifacts = append(m.Artifacts, artifacts...)
	return s.SaveManifest(*m)
}

var markdownResource = regexp.MustCompile(`!?\[[^\]]*\]\(([^) ]+)(?: +[^)]*)?\)`)

var localResource = regexp.MustCompile(`(?:src|href|poster)\s*=\s*["']([^"'#]+)["']|url\(\s*["']?([^"')]+)`)

func captureEvaluationEvidence(home string, s evalcase.Store, m *evalcase.Manifest, records map[string]any, prefix string) error {
	paths := map[string]bool{}
	for _, key := range []string{"runs", "verdicts", "submissions"} {
		for _, row := range evalRows(records, key) {
			for _, field := range []string{"output_path", "evidence_path", "correction_path"} {
				if p := evalText(row[field]); p != "" {
					paths[p] = true
				}
			}
		}
	}
	for p := range paths {
		if _, err := copyEvaluationResource(home, s, m, p, prefix, paths); err != nil {
			return err
		}
	}
	return nil
}
func copyEvaluationResource(home string, s evalcase.Store, m *evalcase.Manifest, path, prefix string, seen map[string]bool) ([]byte, error) {
	allowed := false
	for _, root := range m.ResourceRoots {
		allowed = allowed || path == root || strings.HasPrefix(path, root+"/")
	}
	if !allowed {
		return nil, fmt.Errorf("The resource is outside the original Packager access policy: %s", path)
	}
	if filepath.Clean(path) != path {
		return nil, fmt.Errorf("The resource path is invalid: %s", path)
	}
	target := prefix + "/" + strings.TrimPrefix(path, "/")
	body, found, err := s.ReadArtifact(*m, target)
	if err != nil {
		return nil, err
	}
	media := mime.TypeByExtension(filepath.Ext(path))
	if media == "" {
		media = "application/octet-stream"
	}
	if !found {
		host, err := verbs.ResolveArtifactHostPath(home, path)
		if err != nil {
			return nil, err
		}
		body, err = os.ReadFile(host)
		if err != nil {
			return nil, err
		}
		if err = evalPut(s, m, target, body, path, media); err != nil {
			return nil, err
		}
	}
	if strings.HasPrefix(media, "text/") || strings.Contains(media, "svg") {
		matches := localResource.FindAllSubmatch(body, -1)
		for _, match := range markdownResource.FindAllSubmatch(body, -1) {
			matches = append(matches, [][]byte{match[0], match[1], nil, []byte("optional")})
		}
		for _, match := range matches {
			ref := string(match[1])
			if ref == "" {
				ref = string(match[2])
			}
			if strings.Contains(ref, ":") || strings.HasPrefix(ref, "//") || strings.HasPrefix(ref, "#") {
				continue
			}
			if parsed, err := url.Parse(ref); err == nil {
				ref = parsed.Path
			}
			if !filepath.IsAbs(ref) {
				ref = filepath.Join(filepath.Dir(path), ref)
			}
			// Only artifact resources can cross into a case archive.
			if !strings.HasPrefix(ref, verbs.ArtifactsMountRoot()+"/") {
				return nil, fmt.Errorf("The local resource is outside the artifact roots: %s", ref)
			}
			if seen[ref] {
				continue
			}
			seen[ref] = true
			if _, err = copyEvaluationResource(home, s, m, ref, prefix, seen); err != nil {
				if len(match) > 3 {
					m.Absences["reference:"+ref] = "The original report names this unavailable local reference: " + err.Error()
					continue
				}
				return nil, err
			}
		}
	}
	return body, nil
}
func finishEvaluation(home string, s evalcase.Store, runID string) (any, error) {
	prior, err := evaluationFrame("capture", evaluationRequest{Run: runID})
	if err != nil {
		return nil, err
	}
	if len(evalRows(prior, "capture")) == 0 {
		return map[string]any{"enabled": false}, nil
	}
	records, err := evaluationFrame("records", evaluationRequest{Run: runID})
	if err != nil {
		return nil, err
	}
	captures := evalRows(records, "capture")
	if len(captures) != 1 || evalNumber(captures[0]["enabled"]) != 1 {
		return map[string]any{"enabled": false}, nil
	}
	caseID := evalText(captures[0]["case_id"])
	unlock, err := s.Lock(caseID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	m, err := s.Load(caseID)
	if err != nil {
		return nil, err
	}
	if m.Complete {
		if err = s.Validate(m); err != nil {
			return nil, err
		}
		_, err = evaluationFrame("status", evaluationRequest{Run: runID, Status: "complete", Packet: m.Identity.Packet, Hash: m.Identity.PacketHash})
		return map[string]any{"case_id": caseID, "status": "complete"}, err
	}
	sourceReady := func() bool {
		found := false
		source := m
		source.Complete = false
		source.Artifacts = nil
		for _, a := range m.Artifacts {
			if strings.HasPrefix(a.Path, "source/") {
				source.Artifacts = append(source.Artifacts, a)
			}
			found = found || a.Path == "source/source.bundle"
		}
		return found && s.Validate(source) == nil
	}
	fail := func(err error) (any, error) {
		_, _ = evaluationFrame("status", evaluationRequest{Run: runID, Status: "failed", Error: err.Error()})
		if sourceReady() {
			return map[string]any{"case_id": caseID, "status": "failed", "cleanup_ready": true, "error": err.Error()}, nil
		}
		return nil, err
	}
	var packet map[string]any
	var output string
	for _, r := range evalRows(records, "runs") {
		if r["id"] == runID {
			output = evalText(r["output_path"])
		}
	}
	for _, p := range evalRows(records, "packets") {
		if evalNumber(p["version_id"]) == m.Identity.Version && p["render_path"] == output {
			packet = p
		}
	}
	if packet == nil {
		return map[string]any{"status": "pending", "case_id": caseID, "cleanup_ready": sourceReady()}, nil
	}
	originalPath := filepath.Join(s.Root, caseID, "input", "records.json")
	originalBody, err := os.ReadFile(originalPath)
	if err != nil {
		return fail(fmt.Errorf("The original launch records are absent: %w", err))
	}
	var original map[string]any
	if err = json.Unmarshal(originalBody, &original); err != nil {
		return fail(err)
	}
	sourcePresent := false
	for _, a := range m.Artifacts {
		sourcePresent = sourcePresent || a.Path == "source/source.bundle"
	}
	if !sourcePresent {
		if err = captureOriginalSource(home, s, &m, original); err != nil {
			return fail(err)
		}
	}
	if err = captureEvaluationEvidence(home, s, &m, original, "evidence"); err != nil {
		return fail(err)
	}
	body, err := copyEvaluationResource(home, s, &m, output, "packet/resources", map[string]bool{output: true})
	if err != nil {
		return fail(err)
	}
	if evalcase.Hash(body) != packet["report_sha256"] {
		return fail(fmt.Errorf("The submitted packet report hash changed: %s", output))
	}
	m.Identity.Packet = evalNumber(packet["id"])
	m.Identity.PacketHash = evalText(packet["report_sha256"])
	if err = evalPut(s, &m, "packet/report", body, output, mime.TypeByExtension(filepath.Ext(output))); err != nil {
		return fail(err)
	}
	if err = evalPut(s, &m, "packet/records.json", evalJSON(map[string]any{"packet": packet, "run_id": runID, "terminal_output": output, "thesis": packet["thesis"], "runs": evalRows(records, "runs")}), "submitted packet", "application/json"); err != nil {
		return fail(err)
	}
	if err = captureEvaluationTrace(home, s, &m); err != nil {
		return fail(err)
	}
	m.Complete = true
	if err = s.Validate(m); err != nil {
		return fail(err)
	}
	if err = s.SaveManifest(m); err != nil {
		return fail(err)
	}
	_, err = evaluationFrame("status", evaluationRequest{Run: runID, Status: "complete", Packet: m.Identity.Packet, Hash: m.Identity.PacketHash})
	return map[string]any{"case_id": caseID, "status": "complete"}, err
}
func recoverEvaluation(home string, s evalcase.Store) (any, error) {
	records, err := evaluationFrame("pending", evaluationRequest{})
	if err != nil {
		return nil, err
	}
	failures := []string{}
	for _, r := range evalRows(records, "runs") {
		if _, err = finishEvaluation(home, s, evalText(r["run_id"])); err != nil {
			failures = append(failures, err.Error())
		}
	}
	return map[string]any{"errors": failures}, nil
}

func captureEvaluationTrace(home string, s evalcase.Store, m *evalcase.Manifest) error {
	record, err := evaluationFrame("trace", evaluationRequest{Run: m.Identity.Run})
	if err != nil {
		return err
	}
	session, trace := evalText(record["session_path"]), evalText(record["trace_filename"])
	if session == "" || trace == "" {
		m.Absences["execution_trace"] = "The original Packager has no recorded execution trace."
		return nil
	}
	root := filepath.Join(home, "sessions", m.Identity.Run)
	if session != "sessions/"+m.Identity.Run || filepath.IsAbs(trace) || filepath.Clean(trace) != trace || trace == ".." || strings.HasPrefix(trace, "../") {
		return fmt.Errorf("The original trace path is outside its run.")
	}
	path := filepath.Join(root, trace)
	resolved, err := filepath.EvalSymlinks(path)
	if os.IsNotExist(err) {
		m.Absences["execution_trace"] = "The recorded execution trace is unavailable."
		return nil
	}
	if err != nil {
		return err
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(resolved, canonicalRoot+string(filepath.Separator)) {
		return fmt.Errorf("The original trace leaves its run directory.")
	}
	body, err := os.ReadFile(resolved)
	if err != nil {
		return err
	}
	if err = evalPut(s, m, "execution/trace.jsonl", body, "original Packager execution trace", "application/x-ndjson"); err != nil {
		return err
	}
	delete(m.Absences, "commands")
	m.Absences["effective_model"] = "The execution trace preserves model reports. No separate effective model record exists."
	return nil
}

func captureEvaluationObservation(s evalcase.Store, original evalcase.Manifest, candidate, destination string) (string, string, error) {
	records, err := evaluationFrame("records", evaluationRequest{Run: original.Identity.Run})
	if err != nil {
		return "", "", err
	}
	var matched map[string]any
	for _, row := range evalRows(records, "candidates") {
		if evalText(row["candidate_commit"]) == candidate && evalText(row["destination_commit"]) == destination && evalNumber(row["version_id"]) == original.Identity.Version {
			matched = row
			break
		}
	}
	if matched == nil {
		return "", "", fmt.Errorf("The displayed merge candidate does not belong to this packet version.")
	}
	id := evalcase.Hash([]byte(candidate + "/" + destination))
	store := evalcase.Store{Root: filepath.Join(s.Root, original.CaseID, "observations")}
	unlock, err := store.Lock(id)
	if err != nil {
		return "", "", err
	}
	defer unlock()
	m, err := store.Load(id)
	if os.IsNotExist(err) {
		identity := original.Identity
		identity.Commit = candidate
		m = evalcase.Manifest{FormatVersion: 1, CaseID: id, Group: original.Group, Identity: identity, Authorized: true, ReferenceStatus: "Later source comparison. This record is outside the original Packager input."}
		m.Artifacts, err = store.CaptureSource(id, evalText(records["workspace_root"]), map[string]string{"submitted": candidate, "base": destination})
		if err != nil {
			return "", "", err
		}
		if err = evalPut(store, &m, "candidate.json", evalJSON(matched), "displayed merge candidate", "application/json"); err != nil {
			return "", "", err
		}
	}
	if err != nil {
		return "", "", err
	}
	if err = store.Validate(m); err != nil {
		return "", "", err
	}
	diff, _, err := store.ReadArtifact(m, "source/reviewed.diff")
	return id, string(diff), err
}

func captureEvaluationInventory(home string, s evalcase.Store, m *evalcase.Manifest) error {
	roots, err := verbs.ArtifactHostRoots(home)
	if err != nil {
		return err
	}
	inventory := []map[string]any{}
	seen := map[string]bool{}
	for _, containerRoot := range m.ResourceRoots {
		relative := strings.TrimPrefix(containerRoot, verbs.ArtifactsMountRoot()+"/")
		target, suffix, _ := strings.Cut(relative, "/")
		hostRoot := roots[target]
		if hostRoot == "" {
			return fmt.Errorf("The original artifact root is unavailable: %s", containerRoot)
		}
		canonical, err := filepath.EvalSymlinks(hostRoot)
		if err != nil {
			return err
		}
		root := filepath.Join(canonical, suffix)
		if root != canonical && !strings.HasPrefix(root, canonical+string(filepath.Separator)) {
			return fmt.Errorf("The artifact inventory leaves its authorized root.")
		}
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("The original artifact inventory contains a symbolic link: %s", path)
			}
			if entry.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			original := filepath.Join(containerRoot, relative)
			if seen[original] {
				return nil
			}
			seen[original] = true
			body, err := copyEvaluationResource(home, s, m, original, "evidence", seen)
			if err != nil {
				return err
			}
			inventory = append(inventory, map[string]any{"path": original, "sha256": evalcase.Hash(body), "size": len(body)})
			return nil
		})
		if err != nil {
			return err
		}
	}
	return evalPut(s, m, "input/inventory.json", evalJSON(inventory), "artifact mounts before the Packager launch", "application/json")
}
