// Package repository preserves task commits and prepares isolated repositories.
package repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var objectID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func ValidCommit(commit string) bool { return objectID.MatchString(commit) }

type Git struct {
	Root      string
	directory string
	index     string
}

func (g Git) command(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	cmd := g.process(ctx, args...)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func (g Git) process(ctx context.Context, args ...string) *exec.Cmd {
	fixed := []string{"-C", g.Root,
		"-c", "safe.directory=" + g.Root,
		"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false",
		"-c", "core.attributesFile=/dev/null", "-c", "core.excludesFile=/dev/null",
		"-c", "gc.auto=0", "-c", "maintenance.auto=false",
		"-c", "core.fsync=objects,reference", "-c", "core.fsyncMethod=fsync",
		"-c", "protocol.allow=never", "-c", "protocol.file.allow=always",
		"-c", "fetch.writeCommitGraph=false", "-c", "submodule.recurse=false"}
	fixed = append(fixed, "-c", "fetch.fsckObjects=true", "-c", "transfer.fsckObjects=true")
	if g.directory != "" {
		fixed = append(fixed, "--git-dir="+g.directory, "--work-tree="+g.Root)
	}
	cmd := exec.CommandContext(ctx, "git", append(fixed, args...)...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.TempDir(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_NO_REPLACE_OBJECTS=1",
		"GIT_ATTR_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0",
		"LC_ALL=C"}
	if g.index != "" {
		cmd.Env = append(cmd.Env, "GIT_INDEX_FILE="+g.index)
	}
	return cmd
}

func (g Git) output(ctx context.Context, args ...string) (string, error) {
	out, err := g.command(ctx, nil, args...)
	return strings.TrimSpace(string(out)), err
}

func (g Git) Commit(ctx context.Context, ref string) (string, error) {
	if strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, "\n\r\x00") {
		return "", fmt.Errorf("the commit reference is invalid")
	}
	commit, err := g.output(ctx, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	if !ValidCommit(commit) {
		return "", fmt.Errorf("the reference does not identify a complete commit")
	}
	return commit, nil
}

func (g Git) Ancestor(ctx context.Context, base, commit string) bool {
	if !ValidCommit(base) || !ValidCommit(commit) {
		return false
	}
	_, err := g.output(ctx, "merge-base", "--is-ancestor", base, commit)
	return err == nil
}

// answer runs a Git command whose exit code 1 is the answer "no".
// An error means that Git gave no answer.
func (g Git) answer(ctx context.Context, args ...string) (bool, error) {
	_, err := g.output(ctx, args...)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return err == nil, err
}

// IsAncestor reports whether base is an ancestor of commit. A commit that the
// repository does not hold has no ancestor relation. An error means that Git
// gave no answer.
func (g Git) IsAncestor(ctx context.Context, base, commit string) (bool, error) {
	if !ValidCommit(base) || !ValidCommit(commit) {
		return false, nil
	}
	ancestor, err := g.answer(ctx, "merge-base", "--is-ancestor", base, commit)
	if err == nil {
		return ancestor, nil
	}
	// Git gives one exit code for an absent commit and for a failure.
	for _, id := range []string{base, commit} {
		held, probe := g.answer(ctx, "rev-parse", "--verify", "--quiet", "--end-of-options", id+"^{commit}")
		if probe == nil && !held {
			return false, nil
		}
	}
	return false, err
}

func (g Git) Branch(ctx context.Context, branch string) (string, error) {
	ref := "refs/heads/" + branch
	if _, err := g.output(ctx, "check-ref-format", ref); err != nil {
		return "", fmt.Errorf("the destination branch is invalid: %w", err)
	}
	if target, err := g.output(ctx, "symbolic-ref", "--quiet", ref); err == nil {
		return "", fmt.Errorf("the destination branch must be a direct reference: %s", target)
	}
	return g.Commit(ctx, ref)
}

func (g Git) LocalBranch(ctx context.Context, branch, commit string) error {
	if !ValidCommit(commit) {
		return fmt.Errorf("the local branch requires an exact commit")
	}
	_, err := g.output(ctx, "checkout", "--quiet", "-b", branch, commit)
	return err
}

// DestinationRef names the destination commit in the repository of a run.
const DestinationRef = "refs/mc/destination"

// Reference points a reference at a commit that this repository holds.
func (g Git) Reference(ctx context.Context, ref, commit string) error {
	if !ValidCommit(commit) || !strings.HasPrefix(ref, "refs/mc/") {
		return fmt.Errorf("the reference or its commit is invalid")
	}
	_, err := g.output(ctx, "update-ref", ref, commit)
	return err
}

// Isolated imports complete history through Git's transfer protocol.
// A local clone can create hard links, so this operation uses init and fetch.
func Isolated(ctx context.Context, source, destination string, commits ...string) (Git, error) {
	g := Git{Root: destination}
	if len(commits) == 0 {
		return g, fmt.Errorf("the run requires an input commit")
	}
	for _, commit := range commits {
		if !ValidCommit(commit) {
			return g, fmt.Errorf("the run input is not a complete commit")
		}
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return g, fmt.Errorf("create the run repository: %w", err)
	}
	format, err := (Git{Root: source}).output(ctx, "rev-parse", "--show-object-format")
	if err != nil {
		return g, err
	}
	if format != "sha1" && format != "sha256" {
		return g, fmt.Errorf("the repository object format is unsupported")
	}
	if _, err := g.output(ctx, "init", "--quiet", "--template=", "--object-format="+format); err != nil {
		return g, err
	}
	view, _, closeView, err := applicationView(ctx, source, "culler")
	if err != nil {
		return g, err
	}
	defer closeView()
	args := []string{"fetch", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", "--", view.directory}
	if _, err := g.output(ctx, append(args, commits...)...); err != nil {
		return g, err
	}
	if _, err := os.Lstat(filepath.Join(destination, ".git", "objects", "info", "alternates")); !os.IsNotExist(err) {
		return g, fmt.Errorf("the run repository contains an object dependency")
	}
	if _, err := g.output(ctx, "fsck", "--connectivity-only", "--no-reflogs", commits[0]); err != nil {
		return g, err
	}
	_, err = g.output(ctx, "checkout", "--quiet", "--detach", commits[0])
	return g, err
}

// Retain leaves accepted branches and the operator's files unchanged.
// The reference survives a database failure and permits an identical retry.
func (g Git) Retain(ctx context.Context, source, commit, ref string) error {
	if !ValidCommit(commit) || !strings.HasPrefix(ref, "refs/mc/") {
		return fmt.Errorf("the retained commit or reference is invalid")
	}
	if _, err := g.output(ctx, "check-ref-format", ref); err != nil {
		return err
	}
	if current, err := g.Commit(ctx, ref); err == nil {
		if current != commit {
			return fmt.Errorf("the retained reference already identifies another commit")
		}
		return nil
	}
	view, _, closeView, err := applicationView(ctx, source, "culler")
	if err != nil {
		return err
	}
	defer closeView()
	if _, err := g.output(ctx, "fetch", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", "--", view.directory, commit); err != nil {
		return err
	}
	if got, err := g.Commit(ctx, commit); err != nil || got != commit {
		return fmt.Errorf("the imported commit is unavailable")
	}
	if _, err := g.output(ctx, "update-ref", ref, commit, strings.Repeat("0", len(commit))); err != nil {
		if got, readErr := g.Commit(ctx, ref); readErr != nil || got != commit {
			return err
		}
	}
	return nil
}

func (g Git) Clean(ctx context.Context) error {
	out, err := g.output(ctx, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return err
	}
	if out != "" {
		return fmt.Errorf("the run repository must contain only committed files")
	}
	return nil
}
