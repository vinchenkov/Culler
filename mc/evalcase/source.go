package evalcase

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var commitID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

type Source struct {
	Commits map[string]string `json:"commits"`
	Trees   map[string]string `json:"trees"`
}

func git(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-C", dir}, args...)...)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("The source operation %s failed: %w", args[0], err)
	}
	return b, nil
}

// CaptureSource creates bundle references in a private repository and excludes untracked files.
func (s Store) CaptureSource(id, repository string, commits map[string]string, fallback ...string) ([]Artifact, error) {
	dir, err := s.Dir(id)
	if err != nil {
		return nil, err
	}
	if err = privateDir(dir); err != nil {
		return nil, err
	}
	scratch, err := os.MkdirTemp(dir, ".source-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	if _, err = git(scratch, "init", "--bare"); err != nil {
		return nil, err
	}
	source := Source{commits, map[string]string{}}
	for name, commit := range commits {
		if !identifier.MatchString(name) || !commitID.MatchString(commit) {
			return nil, fmt.Errorf("The %s source commit is invalid.", name)
		}
		for _, sourceRoot := range append([]string{repository}, fallback...) {
			_, err = git(scratch, "fetch", "--no-tags", sourceRoot, commit)
			if err == nil {
				break
			}
		}
		if err != nil {
			return nil, err
		}
		if _, err = git(scratch, "update-ref", "refs/heads/"+name, commit); err != nil {
			return nil, err
		}
		tree, err := git(scratch, "rev-parse", commit+"^{tree}")
		if err != nil {
			return nil, err
		}
		source.Trees[name] = strings.TrimSpace(string(tree))
	}
	bundle := filepath.Join(scratch, "source.bundle")
	if _, err = git(scratch, "bundle", "create", bundle, "--branches"); err != nil {
		return nil, err
	}
	body, err := os.ReadFile(bundle)
	if err != nil {
		return nil, err
	}
	a, err := s.Put(id, "source/source.bundle", body, "Packager repository", "application/x-git-bundle")
	if err != nil {
		return nil, err
	}
	out := []Artifact{a}
	a, err = s.Put(id, "source/identities.json", jsonBytes(source), "Packager repository", "application/json")
	if err != nil {
		return nil, err
	}
	out = append(out, a)
	if commits["base"] != "" && commits["submitted"] != "" {
		start := commits["base"]
		// The Packager starts its reviewed range at the newest commit that the destination holds.
		if commits["destination"] != "" {
			shared, err := git(scratch, "merge-base", commits["destination"], commits["submitted"])
			if err != nil {
				return nil, err
			}
			start = strings.TrimSpace(string(shared))
		}
		diff, err := git(scratch, "diff", "--binary", "--full-index", "--no-ext-diff", start, commits["submitted"], "--")
		if err != nil {
			return nil, err
		}
		a, err = s.Put(id, "source/reviewed.diff", diff, "original reviewed source", "text/x-diff")
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}
func VerifySource(dir string, m Manifest) error {
	b, err := os.ReadFile(filepath.Join(dir, "source/identities.json"))
	if err != nil {
		return err
	}
	var source Source
	if err = json.Unmarshal(b, &source); err != nil {
		return err
	}
	scratch, err := os.MkdirTemp("", "culler-eval-source-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	if _, err = git(scratch, "init", "--bare"); err != nil {
		return err
	}
	bundle := filepath.Join(dir, "source/source.bundle")
	if _, err = git(scratch, "bundle", "verify", bundle); err != nil {
		return err
	}
	if _, err = git(scratch, "fetch", bundle, "refs/heads/*:refs/heads/*"); err != nil {
		return err
	}
	if source.Commits["submitted"] != m.Identity.Commit {
		return fmt.Errorf("The submitted source identity differs from the packet.")
	}
	for name, commit := range source.Commits {
		if !identifier.MatchString(name) || !commitID.MatchString(commit) {
			return fmt.Errorf("The source identity is invalid.")
		}
		actual, err := git(scratch, "rev-parse", "refs/heads/"+name)
		if err != nil {
			return err
		}
		tree, err := git(scratch, "rev-parse", commit+"^{tree}")
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(actual)) != commit || strings.TrimSpace(string(tree)) != source.Trees[name] {
			return fmt.Errorf("The preserved source identity changed.")
		}
	}
	return nil
}

type Substitution struct {
	Original    string `json:"original"`
	Replacement string `json:"replacement"`
}

// ReplayInput exposes only original input artifacts. Assessments and packet feedback remain separate.
func (s Store) ReplayInput(id, destination string, substitutions []Substitution) error {
	m, err := s.Load(id)
	if err != nil {
		return err
	}
	runReplacement, sourceReplacement := false, false
	for _, sub := range substitutions {
		if sub.Original == m.Identity.Run && sub.Replacement != sub.Original && identifier.MatchString(sub.Replacement) {
			runReplacement = true
		}
		if sub.Original == "/workspace/source" && sub.Replacement != "/workspace/source" && filepath.IsAbs(sub.Replacement) {
			sourceReplacement = true
		}
	}
	if !runReplacement || !sourceReplacement {
		return fmt.Errorf("The replay requires a synthetic run identity and an isolated source path.")
	}
	if err = s.Validate(m); err != nil {
		return err
	}
	if !m.Complete {
		return fmt.Errorf("The capture is incomplete.")
	}
	dir, _ := s.Dir(id)
	if err = VerifySource(dir, m); err != nil {
		return err
	}
	if _, err = os.Lstat(destination); !os.IsNotExist(err) {
		return fmt.Errorf("The replay destination must be new.")
	}
	if err = privateDir(destination); err != nil {
		return err
	}
	for _, a := range m.Artifacts {
		if !strings.HasPrefix(a.Path, "input/") && !strings.HasPrefix(a.Path, "source/") && !strings.HasPrefix(a.Path, "evidence/") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, a.Path))
		if err != nil {
			return err
		}
		if err = atomicFile(filepath.Join(destination, "original", a.Path), b); err != nil {
			return err
		}
		replay := b
		if strings.HasPrefix(a.MediaType, "text/") || a.MediaType == "application/json" {
			text := string(b)
			for _, sub := range substitutions {
				if sub.Original == "" || sub.Replacement == "" {
					return fmt.Errorf("The replay substitution is invalid.")
				}
				text = strings.ReplaceAll(text, sub.Original, sub.Replacement)
			}
			replay = []byte(text)
		}
		if err = atomicFile(filepath.Join(destination, "input", a.Path), replay); err != nil {
			return err
		}
	}
	return atomicFile(filepath.Join(destination, "substitutions.json"), jsonBytes(substitutions))
}

// Trial records its assessment revision outside the trial inputs.
func (s Store) Trial(id string, revision int, role, destination string, substitutions []Substitution) error {
	if role != "packager" && role != "judge" {
		return fmt.Errorf("The trial role is invalid.")
	}
	receipts, err := s.Receipts(id)
	if err != nil {
		return err
	}
	var receipt *Receipt
	for i := range receipts {
		if receipts[i].Revision == revision {
			receipt = &receipts[i]
		}
	}
	if receipt == nil {
		return fmt.Errorf("The trial requires an exact saved assessment revision.")
	}
	dir, err := s.Dir(id)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("assessments/%06d/manifest.json", revision)))
	if err != nil {
		return err
	}
	if Hash(body) != receipt.ManifestHash {
		return fmt.Errorf("The trial assessment manifest changed.")
	}
	var manifest Manifest
	if err = json.Unmarshal(body, &manifest); err != nil {
		return err
	}
	if err = s.Validate(manifest); err != nil {
		return err
	}
	if err = s.ReplayInput(id, destination, substitutions); err != nil {
		return err
	}
	if role == "judge" {
		dir, _ := s.Dir(id)
		for target, source := range map[string]string{"packet/report": "packet/report", "packet/wrapper.html": fmt.Sprintf("assessments/%06d/wrapper.html", revision), "rubric.json": fmt.Sprintf("assessments/%06d/rubric.json", revision)} {
			body, err := os.ReadFile(filepath.Join(dir, source))
			if err != nil {
				return err
			}
			if target == "rubric.json" && Hash(body) != receipt.RubricHash {
				return fmt.Errorf("The trial rubric hash changed.")
			}
			if err = atomicFile(filepath.Join(destination, "input", target), body); err != nil {
				return err
			}
		}
	}
	return atomicFile(filepath.Join(destination, "experiment.json"), jsonBytes(map[string]any{"role": role, "case_id": id, "assessment_revision": revision, "manifest_sha256": receipt.ManifestHash}))
}
