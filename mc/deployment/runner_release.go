package deployment

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const maxRunnerReleaseFileBytes = 4 << 20

var runnerReleaseFiles = []string{
	"agent-runner/adapters/claude.ts",
	"agent-runner/adapters/codex.ts",
	"agent-runner/adapters/provider-access.ts",
	"agent-runner/adapters/openwiki.ts",
	"agent-runner/adapters/session-store.ts",
	"agent-runner/main.ts",
	"agent-runner/diagnostics.ts",
	"agent-runner/skills.ts",
	"homie-runner/main.ts",
	"setup-runner/main.ts",
	"shared/project-environment.ts",
	"shared/project-files.ts",
	"shared/project-cache.ts",
	"shared/project-evidence.ts",
	"shared/check-project.ts",
	"openwiki/server.ts",
	"openwiki/integration.mjs",
	// The shared structured logger, same physical file the host tier ships. See
	// the note in hostReleaseFiles: this list is runner/ relative and cannot
	// reach outside it, which is why the module lives here and not at the repo
	// root. Moving it makes the runners import a file that is not in their image.
	"shared/log-sink.ts",
	"shared/log.ts",
	"shared/network-error.ts",
	"shared/provider-token.ts",
	"shared/setup-error.ts",
	"shared/model-response.ts",
}

// RunnerReleaseFiles is the exact shipped set, exported for the same reason
// HostReleaseFiles is: fixtures build their fake runner trees from the manifest
// itself. A hand-copied list here stayed green through a manifest change and
// failed only when the real onboard ran.
func RunnerReleaseFiles() []string {
	return append([]string(nil), runnerReleaseFiles...)
}

var runnerReleaseDirs = []string{
	".",
	"agent-runner",
	"agent-runner/adapters",
	"homie-runner",
	"setup-runner",
	"openwiki",
	"shared",
}

func requireOperatorSourceDirectory(path, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s %q: %w", label, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s %q must be a real directory", label, path)
	}
	uid, _, ok := statOwner(info)
	if !ok || int(uid) != os.Getuid() || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s %q must be operator-owned and not group/other writable", label, path)
	}
	return nil
}

func readReleaseSource(root, rel string) ([]byte, error) {
	parts := strings.Split(filepath.FromSlash(rel), string(os.PathSeparator))
	dir := root
	for _, part := range parts[:len(parts)-1] {
		dir = filepath.Join(dir, part)
		if err := requireOperatorSourceDirectory(dir, "release source directory"); err != nil {
			return nil, err
		}
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	before, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect release source %q: %w", path, err)
	}
	uid, links, ok := statOwner(before)
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() ||
		!ok || int(uid) != os.Getuid() || links != 1 || before.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("release source %q must be a singly linked, operator-owned regular file that is not group/other writable", path)
	}
	if before.Size() <= 0 || before.Size() > maxRunnerReleaseFileBytes {
		return nil, fmt.Errorf("release source %q has invalid size", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open release source %q: %w", path, err)
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, fmt.Errorf("release source %q changed during inspection", path)
	}
	body, err := io.ReadAll(io.LimitReader(f, maxRunnerReleaseFileBytes+1))
	if err != nil || len(body) > maxRunnerReleaseFileBytes {
		return nil, fmt.Errorf("read release source %q: invalid bounded content", path)
	}
	return body, nil
}

func writeReleaseFile(root, rel string, body []byte) error {
	path := filepath.Join(root, filepath.FromSlash(rel))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

// ValidateRunnerRelease checks the exact source set for the private runtime.
// Dependency declarations belong to the runtime image recipe.
func ValidateRunnerRelease(root string) error {
	expectedDirs := make(map[string]bool, len(runnerReleaseDirs))
	for _, rel := range runnerReleaseDirs {
		expectedDirs[rel] = true
	}
	expectedFiles := make(map[string]bool, len(runnerReleaseFiles))
	for _, rel := range runnerReleaseFiles {
		expectedFiles[rel] = true
	}
	seenFiles := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		uid, links, ok := statOwner(info)
		if !ok || int(uid) != os.Getuid() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("installed runner entry %q lost operator ownership or became a symlink", rel)
		}
		if entry.IsDir() {
			if !expectedDirs[rel] || info.Mode().Perm() != 0o700 {
				return fmt.Errorf("installed runner contains unexpected or non-owner-only directory %q", rel)
			}
			return nil
		}
		if !expectedFiles[rel] || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || links != 1 ||
			info.Size() <= 0 || info.Size() > maxRunnerReleaseFileBytes {
			return fmt.Errorf("installed runner contains unexpected or unsafe file %q", rel)
		}
		seenFiles[rel] = true
		return nil
	})
	if err != nil {
		return err
	}
	for _, rel := range runnerReleaseFiles {
		if !seenFiles[rel] {
			return fmt.Errorf("installed runner is missing %q", rel)
		}
	}
	return nil
}

func runnerReleasesEqual(left, right string) (bool, error) {
	for _, rel := range runnerReleaseFiles {
		leftBody, err := os.ReadFile(filepath.Join(left, filepath.FromSlash(rel)))
		if err != nil {
			return false, err
		}
		rightBody, err := os.ReadFile(filepath.Join(right, filepath.FromSlash(rel)))
		if err != nil {
			return false, err
		}
		if !bytes.Equal(leftBody, rightBody) {
			return false, nil
		}
	}
	return true, nil
}

// InstallRunnerRelease durably publishes the fixed production runner tree
// beneath canonical MC_HOME. The repository path is input evidence only; the
// runtime image also receives this exact set.
func InstallRunnerRelease(home, source string) (string, error) {
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return "", fmt.Errorf("resolve runner-release MC_HOME: %w", err)
	}
	if err := requireOwnerOnlyDirectory(canonicalHome, "MC_HOME"); err != nil {
		return "", err
	}
	if source == "" || !filepath.IsAbs(source) {
		return "", fmt.Errorf("runner release source must be an absolute path")
	}
	source = filepath.Clean(source)
	if err := requireOperatorSourceDirectory(source, "runner source root"); err != nil {
		return "", err
	}
	bodies := make(map[string][]byte, len(runnerReleaseFiles))
	for _, rel := range runnerReleaseFiles {
		body, err := readReleaseSource(source, rel)
		if err != nil {
			return "", err
		}
		bodies[rel] = body
	}

	releaseRoot := filepath.Join(canonicalHome, "release")
	if err := os.Mkdir(releaseRoot, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("create release root: %w", err)
	}
	if err := requireOwnerOnlyDirectory(releaseRoot, "release root"); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(releaseRoot, ".runner-stage-")
	if err != nil {
		return "", fmt.Errorf("create runner release stage: %w", err)
	}
	defer os.RemoveAll(stage)
	for _, rel := range runnerReleaseDirs[1:] {
		if err := os.Mkdir(filepath.Join(stage, filepath.FromSlash(rel)), 0o700); err != nil {
			return "", fmt.Errorf("stage runner directory %q: %w", rel, err)
		}
	}
	for _, rel := range runnerReleaseFiles {
		if err := writeReleaseFile(stage, rel, bodies[rel]); err != nil {
			return "", fmt.Errorf("stage runner file %q: %w", rel, err)
		}
	}
	for i := len(runnerReleaseDirs) - 1; i >= 0; i-- {
		if err := syncDirectory(filepath.Join(stage, filepath.FromSlash(runnerReleaseDirs[i]))); err != nil {
			return "", fmt.Errorf("sync staged runner directory: %w", err)
		}
	}
	if err := ValidateRunnerRelease(stage); err != nil {
		return "", fmt.Errorf("validate staged runner release: %w", err)
	}

	canonical := filepath.Join(releaseRoot, "runner")
	info, statErr := os.Lstat(canonical)
	if statErr == nil {
		uid, _, ok := statOwner(info)
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !ok || int(uid) != os.Getuid() {
			return "", fmt.Errorf("installed runner %q must remain a real operator-owned directory", canonical)
		}
		if err := ValidateRunnerRelease(canonical); err == nil {
			equal, err := runnerReleasesEqual(stage, canonical)
			if err != nil {
				return "", fmt.Errorf("compare installed runner release: %w", err)
			}
			if equal {
				return "ok", nil
			}
		}
		if err := atomicExchangeDirectories(stage, canonical); err != nil {
			return "", fmt.Errorf("atomically exchange installed runner release: %w", err)
		}
		if err := syncDirectory(releaseRoot); err != nil {
			return "", fmt.Errorf("sync release root after runner exchange: %w", err)
		}
		if err := os.RemoveAll(stage); err != nil {
			return "", fmt.Errorf("remove replaced runner release: %w", err)
		}
		if err := syncDirectory(releaseRoot); err != nil {
			return "", fmt.Errorf("sync release root after replaced runner removal: %w", err)
		}
		return "done", nil
	}
	if !errors.Is(statErr, fs.ErrNotExist) {
		return "", fmt.Errorf("inspect installed runner release: %w", statErr)
	}
	if err := os.Rename(stage, canonical); err != nil {
		return "", fmt.Errorf("publish installed runner release: %w", err)
	}
	if err := syncDirectory(releaseRoot); err != nil {
		return "", fmt.Errorf("sync release root after runner publication: %w", err)
	}
	return "done", nil
}

// RunnerReleaseMatches compares the installed files without a release change.
func RunnerReleaseMatches(home, source string) (bool, error) {
	root := filepath.Join(home, "release", "runner")
	if source == "" {
		return false, nil
	}
	if err := ValidateRunnerRelease(root); err != nil {
		return false, nil
	}
	return runnerReleasesEqual(root, source)
}
