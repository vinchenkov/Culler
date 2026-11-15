// Package evalcase preserves private packet evidence and immutable assessments.
package evalcase

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

//go:embed rubric.json
var Rubric []byte

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,159}$`)
var Dimensions = []string{"comprehension", "causal_explanation", "decision_support", "visual_explanation", "clarity", "packet_length"}

func Hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func Now() string          { return time.Now().UTC().Format(time.RFC3339Nano) }

type Identity struct {
	Deployment   string `json:"deployment"`
	Project      string `json:"project"`
	Task         int64  `json:"task_id"`
	Requirements int64  `json:"requirements_version"`
	Packet       int64  `json:"packet_id"`
	Version      int64  `json:"version_id"`
	Run          string `json:"run_id"`
	PacketHash   string `json:"packet_sha256"`
	Commit       string `json:"submitted_commit"`
}
type Artifact struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	Origin     string `json:"origin"`
	CapturedAt string `json:"captured_at"`
	MediaType  string `json:"media_type"`
}
type Manifest struct {
	FormatVersion   int               `json:"format_version"`
	CaseID          string            `json:"case_id"`
	Group           string            `json:"case_group"`
	Identity        Identity          `json:"identity"`
	Authorized      bool              `json:"capture_evaluation_cases"`
	Artifacts       []Artifact        `json:"artifacts"`
	Absences        map[string]string `json:"absences"`
	Complete        bool              `json:"replay_complete"`
	ResourceRoots   []string          `json:"resource_roots"`
	ReferenceStatus string            `json:"reference_status"`
}
type Failure struct {
	Category    string `json:"category"`
	Explanation string `json:"explanation"`
	Evidence    string `json:"evidence"`
}
type Assessment struct {
	Observation       string            `json:"observation,omitempty"`
	RequestID         string            `json:"request_id"`
	Identity          Identity          `json:"identity"`
	Scores            map[string]*int   `json:"scores"`
	Notes             map[string]string `json:"notes"`
	Reason            string            `json:"reason"`
	CriticalFailure   *bool             `json:"critical_failure"`
	Failures          []Failure         `json:"failures"`
	FamiliarProject   bool              `json:"familiar_project"`
	SourceInspected   *bool             `json:"source_inspected"`
	EvidenceInspected *bool             `json:"evidence_inspected"`
	Uncertainty       string            `json:"uncertainty"`
	Supersedes        int               `json:"supersedes"`
	Display           json.RawMessage   `json:"display"`
	Wrapper           string            `json:"wrapper"`
	Reviewer          string            `json:"reviewer"`
	SubmittedAt       string            `json:"submitted_at"`
	RubricHash        string            `json:"rubric_sha256"`
}
type Receipt struct {
	CaseID       string `json:"case_id"`
	Revision     int    `json:"assessment_revision"`
	PacketHash   string `json:"packet_sha256"`
	RubricHash   string `json:"rubric_sha256"`
	ManifestHash string `json:"manifest_sha256"`
	RequestID    string `json:"request_id"`
	RequestHash  string `json:"request_sha256"`
}
type Store struct{ Root string }

func (s Store) Dir(id string) (string, error) {
	if !identifier.MatchString(id) {
		return "", fmt.Errorf("The case identifier is invalid.")
	}
	if err := privateDir(filepath.Dir(s.Root)); err != nil {
		return "", err
	}
	if err := privateDir(s.Root); err != nil {
		return "", err
	}
	dir := filepath.Join(s.Root, id)
	if info, err := os.Lstat(dir); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return "", fmt.Errorf("The case directory is unsafe.")
	}
	return dir, nil
}
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("The storage directory is unsafe: %s", dir)
	}
	return os.Chmod(dir, 0700)
}
func safePath(root, name string) (string, error) {
	if !fs.ValidPath(name) || strings.Contains(name, "\\") {
		return "", fmt.Errorf("The artifact path is invalid: %s", name)
	}
	cur := root
	for _, part := range strings.Split(name, "/") {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("The artifact path contains a symbolic link: %s", name)
		}
	}
	return cur, nil
}
func syncDir(dir string) error {
	f, e := os.Open(dir)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func atomicFile(path string, b []byte) error {
	if err := privateDir(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}
func jsonBytes(v any) []byte { b, _ := json.MarshalIndent(v, "", "  "); return append(b, '\n') }
func (s Store) Put(id, name string, b []byte, origin, media string) (Artifact, error) {
	if err := CheckPublic(name, b); err != nil {
		return Artifact{}, err
	}
	dir, err := s.Dir(id)
	if err != nil {
		return Artifact{}, err
	}
	path, err := safePath(dir, name)
	if err != nil {
		return Artifact{}, err
	}
	if err = atomicFile(path, b); err != nil {
		return Artifact{}, fmt.Errorf("Preserve %s: %w", name, err)
	}
	return Artifact{name, int64(len(b)), Hash(b), origin, Now(), media}, nil
}
func (s Store) SaveManifest(m Manifest) error {
	if m.FormatVersion != 1 || !m.Authorized {
		return fmt.Errorf("The capture manifest is invalid.")
	}
	dir, err := s.Dir(m.CaseID)
	if err != nil {
		return err
	}
	return atomicFile(filepath.Join(dir, "manifest.json"), jsonBytes(m))
}
func (s Store) Load(id string) (Manifest, error) {
	var m Manifest
	dir, err := s.Dir(id)
	if err != nil {
		return m, err
	}
	path, err := safePath(dir, "manifest.json")
	if err != nil {
		return m, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	if m.FormatVersion != 1 || m.CaseID != id || !m.Authorized {
		return m, fmt.Errorf("The case manifest is invalid.")
	}
	return m, nil
}
func (s Store) Validate(m Manifest) error {
	dir, err := s.Dir(m.CaseID)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, a := range m.Artifacts {
		if seen[a.Path] {
			return fmt.Errorf("The manifest repeats %s.", a.Path)
		}
		seen[a.Path] = true
		path, err := safePath(dir, a.Path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("Read %s: %w", a.Path, err)
		}
		if !info.Mode().IsRegular() || info.Size() != a.Size {
			return fmt.Errorf("The artifact size changed: %s", a.Path)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if Hash(b) != a.SHA256 {
			return fmt.Errorf("The artifact hash changed: %s", a.Path)
		}
	}
	if m.Complete {
		for _, path := range []string{"source/source.bundle", "input/effective.json", "input/records.json", "input/access.json", "input/runtime.json", "input/inventory.json", "source/identities.json", "packet/report", "packet/records.json"} {
			if !seen[path] {
				return fmt.Errorf("The case lacks %s.", path)
			}
		}
		for _, a := range m.Artifacts {
			if a.Path == "packet/report" && a.SHA256 != m.Identity.PacketHash {
				return fmt.Errorf("The packet hash differs from its identity.")
			}
		}
	}
	return nil
}
func ValidateAssessment(a Assessment) error {
	if !identifier.MatchString(a.RequestID) {
		return fmt.Errorf("The submission requires a request identifier.")
	}
	if len(a.Scores) != 6 {
		return fmt.Errorf("All six scores are required.")
	}
	for _, name := range Dimensions {
		v, ok := a.Scores[name]
		if !ok || v == nil || *v < 0 || *v > 4 {
			return fmt.Errorf("The %s score must be an integer from zero through four.", name)
		}
	}
	for name := range a.Notes {
		if _, ok := a.Scores[name]; !ok {
			return fmt.Errorf("The dimension note is invalid.")
		}
	}
	if strings.TrimSpace(a.Reason) == "" || len(a.Reason) > 32000 {
		return fmt.Errorf("An overall reason is required within 32000 characters.")
	}
	if a.CriticalFailure == nil || a.SourceInspected == nil || a.EvidenceInspected == nil || !a.FamiliarProject {
		return fmt.Errorf("Complete the critical failure and Project context fields.")
	}
	if *a.CriticalFailure != (len(a.Failures) > 0) {
		return fmt.Errorf("The critical failure determination and records must agree.")
	}
	var rubric struct {
		Categories []string `json:"critical_failure_categories"`
	}
	_ = json.Unmarshal(Rubric, &rubric)
	for _, f := range a.Failures {
		found := false
		for _, c := range rubric.Categories {
			found = found || c == f.Category
		}
		if !found || strings.TrimSpace(f.Explanation) == "" || strings.TrimSpace(f.Evidence) == "" {
			return fmt.Errorf("Each critical failure requires its category, explanation, and evidence reference.")
		}
	}
	if err := CheckPublic("assessment", jsonBytes(a)); err != nil {
		return err
	}
	if a.Wrapper == "" || !json.Valid(a.Display) {
		return fmt.Errorf("The displayed packet and display settings are required.")
	}
	if a.RubricHash != Hash(Rubric) {
		return fmt.Errorf("The rubric changed. Open the assessment again.")
	}
	return nil
}
func (s Store) lock(id string) (func(), error) {
	dir, err := s.Dir(id)
	if err != nil {
		return nil, err
	}
	if err = privateDir(dir); err != nil {
		return nil, err
	}
	path, err := safePath(dir, ".lock")
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
func (s Store) Receipts(id string) ([]Receipt, error) {
	dir, err := s.Dir(id)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(dir, "assessments"))
	if os.IsNotExist(err) {
		return []Receipt{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Receipt{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path, err := safePath(dir, "assessments/"+entry.Name()+"/receipt.json")
		if err != nil {
			return nil, err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var r Receipt
		if err = json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Revision < out[j].Revision })
	return out, nil
}
func (s Store) Submit(id string, a Assessment) (Receipt, error) {
	var empty Receipt
	unlock, err := s.lock(id)
	if err != nil {
		return empty, err
	}
	defer unlock()
	m, err := s.Load(id)
	if err != nil {
		return empty, err
	}
	if !m.Complete || m.Identity != a.Identity {
		return empty, fmt.Errorf("The assessment does not identify a complete packet capture.")
	}
	if err = s.Validate(m); err != nil {
		return empty, err
	}
	a.Reviewer = "operator"
	a.SubmittedAt = ""
	requestHash := Hash(jsonBytes(a))
	receipts, err := s.Receipts(id)
	if err != nil {
		return empty, err
	}
	for _, r := range receipts {
		if r.RequestID == a.RequestID {
			if r.RequestHash != requestHash {
				return empty, fmt.Errorf("The request identifier already identifies another assessment.")
			}
			return r, nil
		}
	}
	if err := ValidateAssessment(a); err != nil {
		return empty, err
	}
	if a.Supersedes != len(receipts) {
		return empty, fmt.Errorf("The assessment revision changed. Open the latest revision.")
	}
	revision := len(receipts) + 1
	dir, _ := s.Dir(id)
	parent := filepath.Join(dir, "assessments")
	if err = privateDir(parent); err != nil {
		return empty, err
	}
	stage, err := os.MkdirTemp(parent, ".revision-")
	if err != nil {
		return empty, err
	}
	defer os.RemoveAll(stage)
	a.SubmittedAt = Now()
	revisionManifest := m
	if a.Observation != "" {
		observation, err := s.ObservationArtifacts(id, a.Observation, m.Identity)
		if err != nil {
			return empty, err
		}
		revisionManifest.Artifacts = append(append([]Artifact{}, m.Artifacts...), observation...)
	}
	for name, b := range map[string][]byte{"assessment.json": jsonBytes(a), "rubric.json": Rubric, "wrapper.html": []byte(a.Wrapper)} {
		if err = atomicFile(filepath.Join(stage, name), b); err != nil {
			return empty, err
		}
		revisionManifest.Artifacts = append(revisionManifest.Artifacts, Artifact{fmt.Sprintf("assessments/%06d/%s", revision, name), int64(len(b)), Hash(b), "operator assessment", a.SubmittedAt, "application/json"})
	}
	manifest := jsonBytes(revisionManifest)
	r := Receipt{id, revision, m.Identity.PacketHash, Hash(Rubric), Hash(manifest), a.RequestID, requestHash}
	if err = atomicFile(filepath.Join(stage, "manifest.json"), manifest); err != nil {
		return empty, err
	}
	if err = atomicFile(filepath.Join(stage, "receipt.json"), jsonBytes(r)); err != nil {
		return empty, err
	}
	if err = os.Rename(stage, filepath.Join(parent, fmt.Sprintf("%06d", revision))); err != nil {
		return empty, err
	}
	if err = syncDir(parent); err != nil {
		return empty, err
	}
	return r, nil
}

// Archive includes only manifest artifacts and committed assessment revisions.
func (s Store) Archive(id string, w io.Writer) error {
	unlock, err := s.lock(id)
	if err != nil {
		return err
	}
	defer unlock()
	m, err := s.Load(id)
	if err != nil {
		return err
	}
	if err = s.Validate(m); err != nil {
		return err
	}
	receipts, err := s.Receipts(id)
	if err != nil {
		return err
	}
	if len(receipts) == 0 {
		return fmt.Errorf("The case has no saved assessment.")
	}
	names := map[string]bool{"manifest.json": true}
	for _, a := range m.Artifacts {
		names[a.Path] = true
	}
	dir, _ := s.Dir(id)
	for _, r := range receipts {
		prefix := fmt.Sprintf("assessments/%06d/", r.Revision)
		body, err := os.ReadFile(filepath.Join(dir, prefix, "manifest.json"))
		if err != nil {
			return err
		}
		if Hash(body) != r.ManifestHash {
			return fmt.Errorf("The assessment manifest hash changed.")
		}
		var revision Manifest
		if err = json.Unmarshal(body, &revision); err != nil {
			return err
		}
		if err = s.Validate(revision); err != nil {
			return err
		}
		if revision.Identity != m.Identity || r.CaseID != id || r.PacketHash != m.Identity.PacketHash {
			return fmt.Errorf("The assessment receipt identifies another case.")
		}
		rubric, err := os.ReadFile(filepath.Join(dir, prefix, "rubric.json"))
		if err != nil {
			return err
		}
		assessmentBody, err := os.ReadFile(filepath.Join(dir, prefix, "assessment.json"))
		if err != nil {
			return err
		}
		var assessment Assessment
		if err = json.Unmarshal(assessmentBody, &assessment); err != nil {
			return err
		}
		if Hash(rubric) != r.RubricHash || assessment.RubricHash != r.RubricHash || assessment.Identity != m.Identity || assessment.RequestID != r.RequestID {
			return fmt.Errorf("The assessment identity differs from its receipt.")
		}
		for _, artifact := range revision.Artifacts {
			names[artifact.Path] = true
		}
		for _, name := range []string{"manifest.json", "receipt.json", "assessment.json", "rubric.json", "wrapper.html"} {
			names[prefix+name] = true
		}
	}
	paths := []string{}
	for name := range names {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, name := range paths {
		path, err := safePath(dir, name)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err = tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err = tw.Write(b); err != nil {
			return err
		}
	}
	if err = tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// Import checks every archive path before it publishes a portable case.
func (s Store) Import(r io.Reader) (Manifest, error) {
	var empty Manifest
	if err := privateDir(s.Root); err != nil {
		return empty, err
	}
	stage, err := os.MkdirTemp(s.Root, ".import-")
	if err != nil {
		return empty, err
	}
	defer os.RemoveAll(stage)
	gz, err := gzip.NewReader(r)
	if err != nil {
		return empty, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return empty, err
		}
		if h.Typeflag != tar.TypeReg || h.Size < 0 || h.Size > 2<<30 || seen[h.Name] {
			return empty, fmt.Errorf("The archive entry is invalid.")
		}
		seen[h.Name] = true
		total += h.Size
		if total > 8<<30 {
			return empty, fmt.Errorf("The archive exceeds the size limit.")
		}
		path, err := safePath(stage, h.Name)
		if err != nil {
			return empty, err
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return empty, err
		}
		if err = atomicFile(path, b); err != nil {
			return empty, err
		}
	}
	b, err := os.ReadFile(filepath.Join(stage, "manifest.json"))
	if err != nil {
		return empty, err
	}
	var m Manifest
	if err = json.Unmarshal(b, &m); err != nil {
		return empty, err
	}
	dest, err := s.Dir(m.CaseID)
	if err != nil {
		return empty, err
	}
	if _, err = os.Lstat(dest); !os.IsNotExist(err) {
		return empty, fmt.Errorf("The imported case already exists.")
	}
	// Validation uses the same directory layout as a captured case.
	temporary, err := os.MkdirTemp(s.Root, ".validate-")
	if err != nil {
		return empty, err
	}
	defer os.RemoveAll(temporary)
	if err = os.Rename(stage, filepath.Join(temporary, m.CaseID)); err != nil {
		return empty, err
	}
	check := Store{temporary}
	loaded, err := check.Load(m.CaseID)
	if err != nil {
		return empty, err
	}
	var archive bytes.Buffer
	if err = check.Archive(loaded.CaseID, &archive); err != nil {
		return empty, err
	}
	validated, err := gzip.NewReader(bytes.NewReader(archive.Bytes()))
	if err != nil {
		return empty, err
	}
	allowed := map[string]bool{}
	contents := tar.NewReader(validated)
	for {
		header, e := contents.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return empty, e
		}
		allowed[header.Name] = true
	}
	_ = validated.Close()
	for name := range seen {
		if !allowed[name] {
			return empty, fmt.Errorf("The archive contains an unlisted artifact: %s", name)
		}
	}
	if err = VerifySource(filepath.Join(temporary, m.CaseID), m); err != nil {
		return empty, err
	}
	if err = os.Rename(filepath.Join(temporary, m.CaseID), dest); err != nil {
		return empty, err
	}
	return m, syncDir(s.Root)
}

// PruneUnscored preserves every case with a committed assessment receipt.
func (s Store) PruneUnscored(id string) error {
	dir, err := s.Dir(id)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(dir); os.IsNotExist(err) {
		return nil
	}
	unlock, err := s.lock(id)
	if err != nil {
		return err
	}
	defer unlock()
	receipts, err := s.Receipts(id)
	if err != nil {
		return err
	}
	if len(receipts) > 0 {
		return nil
	}
	if err = os.RemoveAll(dir); err != nil {
		return err
	}
	return syncDir(s.Root)
}

// ReadArtifact verifies the private copy before a capture retry reuses it.
func (s Store) ReadArtifact(m Manifest, name string) ([]byte, bool, error) {
	for _, a := range m.Artifacts {
		if a.Path != name {
			continue
		}
		dir, err := s.Dir(m.CaseID)
		if err != nil {
			return nil, true, err
		}
		path, err := safePath(dir, name)
		if err != nil {
			return nil, true, err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, true, err
		}
		if int64(len(body)) != a.Size || Hash(body) != a.SHA256 {
			return nil, true, fmt.Errorf("The preserved artifact changed: %s", name)
		}
		return body, true, nil
	}
	return nil, false, nil
}

func (s Store) Lock(id string) (func(), error) { return s.lock(id) }
