package verbs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// OnboardGitMutation is the narrow mutating Git seam. Production supplies a
// throwaway mc-prod container runner; the direct development/test lane uses
// the same closed argv through host Git.
type OnboardGitMutation func(root string, args ...string) error

// OnboardGitPostureIntent identifies the durable receipt that binds a
// Project to the state of its workspace, so a later registration cannot
// silently change what Culler may do to that workspace.
type OnboardGitPostureIntent struct {
	Home           string `json:"-"`
	DeploymentUUID string `json:"deployment_uuid"`
	Project        string `json:"project"`
}

const (
	// onboardGitPostureRepository is the state the receipt records: this
	// workspace is a repository Culler develops.
	onboardGitPostureRepository = "repository"
	retiredReadOnlyGitPosture   = "read-only"
)

type onboardGitPostureReceipt struct {
	Version        int    `json:"version"`
	DeploymentUUID string `json:"deployment_uuid"`
	Project        string `json:"project"`
	Root           string `json:"root"`
	// Posture is what the workspace IS. The durable key stays "mode", and
	// receipts carrying "existing" or "init" spell this same state.
	Posture string `json:"mode"`
}

// recordedOnboardGitPosture reads a stored posture as the state it describes.
func recordedOnboardGitPosture(stored string) string {
	switch stored {
	case "existing", "init", onboardGitPostureRepository:
		return onboardGitPostureRepository
	}
	return stored
}

// ProjectGitStatement is the rule every surface that registers a Project
// states before it acts, naming the workspace it applies to.
func ProjectGitStatement(root string) string {
	return fmt.Sprintf("Culler develops %s with git. An existing repository there is adopted. "+
		"If there is no repository, Culler runs `git init` in %s.", root, root)
}

// AppendProjectGitDetail carries the rule and the taken half into a section
// detail line. Both onboarding lanes call it so neither can tell the operator a
// different story about the same workspace.
func AppendProjectGitDetail(detail, root string, initialized bool) string {
	if detail != "" {
		detail += "; "
	}
	return detail + ProjectGitStatement(root) + " " + ProjectGitOutcome(root, initialized)
}

// ProjectGitOutcome names which half of that rule the registration took.
func ProjectGitOutcome(root string, initialized bool) string {
	if initialized {
		return fmt.Sprintf("There was no repository at %s, so Culler ran `git init` there.", root)
	}
	return fmt.Sprintf("Culler adopted the existing repository at %s.", root)
}

func onboardGitPosturePath(intent OnboardGitPostureIntent) (string, error) {
	if !filepath.IsAbs(intent.Home) || intent.DeploymentUUID == "" ||
		!validStructuralText(intent.Project, maxPrivateScalarBytes) {
		return "", Domainf("workspace Git posture receipt identity is invalid")
	}
	sum := sha256.Sum256([]byte(intent.Project))
	return filepath.Join(intent.Home, "onboard-intents",
		"project-"+hex.EncodeToString(sum[:])+".json"), nil
}

func secureOnboardIntentDir(home string) (string, error) {
	dir := filepath.Join(home, "onboard-intents")
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return "", Domainf("create workspace Git posture receipt directory: %v", err)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o077 != 0 {
		return "", Domainf("workspace Git posture receipt directory is not owner-only")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return "", Domainf("workspace Git posture receipt directory is not operator-owned")
	}
	return dir, nil
}

func readOnboardGitPosture(path string) (onboardGitPostureReceipt, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return onboardGitPostureReceipt{}, false, nil
	}
	if err != nil {
		return onboardGitPostureReceipt{}, false, Domainf("inspect workspace Git posture receipt: %v", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 ||
		stat.Uid != uint32(os.Getuid()) || stat.Nlink != 1 {
		return onboardGitPostureReceipt{}, false, Domainf("workspace Git posture receipt is not an owner-only single-link regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return onboardGitPostureReceipt{}, false, Domainf("open workspace Git posture receipt: %v", err)
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, maxPrivateScalarBytes+1))
	dec.DisallowUnknownFields()
	var receipt onboardGitPostureReceipt
	if err := dec.Decode(&receipt); err != nil {
		return onboardGitPostureReceipt{}, false, Domainf("decode workspace Git posture receipt: %v", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return onboardGitPostureReceipt{}, false, Domainf("workspace Git posture receipt carries trailing data")
	}
	return receipt, true, nil
}

func ensureOnboardGitPosture(intent OnboardGitPostureIntent, root string) error {
	path, err := onboardGitPosturePath(intent)
	if err != nil {
		return err
	}
	dir, err := secureOnboardIntentDir(intent.Home)
	if err != nil {
		return err
	}
	want := onboardGitPostureReceipt{
		Version: 1, DeploymentUUID: intent.DeploymentUUID,
		Project: intent.Project, Root: root,
		Posture: onboardGitPostureRepository,
	}
	got, exists, err := readOnboardGitPosture(path)
	if err != nil {
		return err
	}
	if exists {
		got.Posture = recordedOnboardGitPosture(got.Posture)
		if got == want {
			return nil
		}
		// A recorded "read-only" posture is the one rewrite the operator may
		// perform: re-running the project section IS the explicit
		// re-registration of that Project as a repository. The identity
		// fields must still match — a receipt for a different root or
		// deployment refuses like any other rewrite.
		supersededReadOnly := got.Posture == retiredReadOnlyGitPosture &&
			got.Version == want.Version &&
			got.DeploymentUUID == want.DeploymentUUID &&
			got.Project == want.Project && got.Root == want.Root
		if !supersededReadOnly {
			return Domainf("Project %s already has a different durable Git posture, recorded for workspace %s; refuse implicit rewrite — re-run `mc onboard project --project %s --workspace-root %s --accept-generated-sandbox`",
				intent.Project, got.Root, intent.Project, got.Root)
		}
		if err := os.Remove(path); err != nil {
			return Domainf("supersede read-only workspace Git posture receipt: %v", err)
		}
	}
	body, err := json.Marshal(want)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Domainf("create workspace Git posture receipt: %v", err)
	}
	if _, err := f.Write(append(body, '\n')); err != nil {
		f.Close()
		_ = os.Remove(path)
		return Domainf("write workspace Git posture receipt: %v", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(path)
		return Domainf("sync workspace Git posture receipt: %v", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return Domainf("close workspace Git posture receipt: %v", err)
	}
	if err := syncDir(dir); err != nil {
		return Domainf("sync workspace Git posture receipt directory: %v", err)
	}
	return nil
}

func inspectOnboardGitRoot(root string) (bool, error) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return false, Domainf("workspace root %q is unavailable: %v", root, err)
	}
	bare, bareErr := gitAtSource(root, "rev-parse", "--is-bare-repository")
	if bareErr == nil && strings.TrimSpace(string(bare)) == "true" {
		return false, Domainf("workspace root %q must be a non-bare Git worktree", root)
	}
	out, err := gitAtSource(root, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil {
		if _, statErr := os.Lstat(filepath.Join(root, ".git")); statErr == nil {
			return false, Domainf("workspace %q has a malformed Git administrative entry: %v", root, err)
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return false, nil
		}
		return false, Domainf("probe workspace Git contract at %q: %v", root, err)
	}
	top := filepath.Clean(strings.TrimSpace(string(out)))
	if top != root {
		return false, Domainf("workspace root %q is inside repository %q; register the repository root instead", root, top)
	}
	bare, err = gitAtSource(root, "rev-parse", "--is-bare-repository")
	if err != nil {
		return false, Domainf("probe workspace Git kind at %q: %v", root, err)
	}
	if strings.TrimSpace(string(bare)) != "false" {
		return false, Domainf("workspace root %q must be a non-bare Git worktree", root)
	}
	return true, nil
}

func hostOnboardGitMutation(root string, args ...string) error {
	if _, err := gitOutput(root, sourceGitEnv(), nil, args...); err != nil {
		return Domainf("prepare workspace Git contract at %q: %v", root, err)
	}
	return nil
}

// PrepareOnboardProjectGit enforces the workspace Git contract before any
// durable Project row is written. Culler develops the workspace with git: a
// repository at the root is adopted, and its absence is served by `git init`
// there. The durable receipt binds the resulting state to the Project
// before the first mutation, so a later registration cannot silently change
// what Culler may do to that workspace. It reports whether the
// workspace changed and whether the repository was initialized.
func PrepareOnboardProjectGit(root string, intent OnboardGitPostureIntent, mutate OnboardGitMutation) (bool, bool, error) {
	isRepo, err := inspectOnboardGitRoot(root)
	if err != nil {
		return false, false, err
	}
	if isRepo {
		if err := validateInitialCullerBranch(root); err != nil {
			return false, false, err
		}
	}
	if err := ensureOnboardGitPosture(intent, root); err != nil {
		return false, false, err
	}
	changed, initialized, err := applyWorkspaceGitRule(root, isRepo, mutate)
	if err != nil {
		return false, false, err
	}
	inputChanged, err := ensureWorkspaceInput(root, mutate)
	return changed || inputChanged, initialized, err
}

// validateInitialCullerBranch refuses an existing culler branch with commits
// that main does not have, because initialization moves the branch to main.
func validateInitialCullerBranch(root string) error {
	culler, err := gitAtSource(root, "rev-parse", "--verify", "refs/heads/culler^{commit}")
	if err != nil {
		return nil
	}
	main, err := gitAtSource(root, "rev-parse", "--verify", "refs/heads/main^{commit}")
	if err != nil {
		return nil
	}
	if _, err := gitAtSource(root, "merge-base", "--is-ancestor", strings.TrimSpace(string(culler)), strings.TrimSpace(string(main))); err != nil {
		return Domainf("The culler branch has commits that main does not have. Merge the culler branch into main or delete it, then initialize the Project.")
	}
	return nil
}

// ensureWorkspaceRepository applies the same rule where no durable receipt is
// kept, because the workspace binding is the sandbox profile's rather than
// this registration's.
func ensureWorkspaceRepository(root string, mutate OnboardGitMutation) (bool, bool, error) {
	isRepo, err := inspectOnboardGitRoot(root)
	if err != nil {
		return false, false, err
	}
	if isRepo {
		if err := validateInitialCullerBranch(root); err != nil {
			return false, false, err
		}
	}
	changed, initialized, err := applyWorkspaceGitRule(root, isRepo, mutate)
	if err != nil {
		return false, false, err
	}
	inputChanged, err := ensureWorkspaceInput(root, mutate)
	return changed || inputChanged, initialized, err
}

// ensureWorkspaceInput points the culler branch at the tip of main, whatever
// the checkout holds. A repository without commits first receives an initial
// commit on its current branch, and the culler branch starts there. When that
// branch is culler, Culler created the repository, and the initial commit
// holds the files of the folder. Otherwise the initial commit is empty and
// keeps the operator's files and index.
func ensureWorkspaceInput(root string, mutate OnboardGitMutation) (bool, error) {
	initial := false
	_, headErr := gitAtSource(root, "rev-parse", "--verify", "HEAD^{commit}")
	branches, err := gitAtSource(root, "for-each-ref", "--count=1", "--format=%(refname)", "refs/heads/")
	if err != nil {
		return false, Domainf("the repository branches are unreadable: %v", err)
	}
	if headErr != nil && strings.TrimSpace(string(branches)) == "" {
		if mutate == nil {
			return false, Domainf("the repository requires an initial commit")
		}
		only := "--only"
		if head, err := gitAtSource(root, "symbolic-ref", "--quiet", "HEAD"); err == nil && strings.TrimSpace(string(head)) == "refs/heads/culler" {
			if err := mutate(root, "add", "--all", "--", "."); err != nil {
				return false, err
			}
			only = "--no-only"
		}
		if err := mutate(root, "-c", "user.name=Culler", "-c", "user.email=culler@localhost", "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgSign=false",
			"commit", "--allow-empty", only, "--quiet", "-m", "Initialize the project history"); err != nil {
			return false, err
		}
		initial = true
	}
	if err := validateInitialCullerBranch(root); err != nil {
		return false, err
	}
	culler, cullerErr := gitAtSource(root, "rev-parse", "--verify", "refs/heads/culler^{commit}")
	start, mainErr := gitAtSource(root, "rev-parse", "--verify", "refs/heads/main^{commit}")
	switch {
	case mainErr == nil:
	case cullerErr == nil:
		return initial, nil
	case initial:
		start, err = gitAtSource(root, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return false, Domainf("the repository did not retain its initial commit")
		}
	default:
		return false, Domainf("Culler starts the culler branch from main. This repository has no main branch.")
	}
	commit, previous := strings.TrimSpace(string(start)), strings.Repeat("0", len(strings.TrimSpace(string(start))))
	if cullerErr == nil {
		if previous = strings.TrimSpace(string(culler)); previous == commit {
			return initial, nil
		}
	}
	if mutate == nil {
		return false, Domainf("the repository requires its culler branch")
	}
	if err := mutate(root, "update-ref", "refs/heads/culler", commit, previous); err != nil {
		return false, err
	}
	return true, nil
}

func applyWorkspaceGitRule(root string, isRepo bool, mutate OnboardGitMutation) (bool, bool, error) {
	changed, initialized := false, false
	if !isRepo {
		if mutate == nil {
			return false, false, Domainf("workspace Git initializer is unavailable")
		}
		if err := mutate(root, "init", "--quiet", "--template=/dev/null", "--initial-branch=culler"); err != nil {
			return false, false, err
		}
		changed, initialized = true, true
	}
	relative, relativeErr := gitAtSource(root, "config", "--local", "--get", "worktree.useRelativePaths")
	if relativeErr != nil || strings.TrimSpace(string(relative)) != "true" {
		if mutate == nil {
			return false, false, Domainf("workspace Git configurator is unavailable")
		}
		if err := mutate(root, "config", "--local", "worktree.useRelativePaths", "true"); err != nil {
			return false, false, err
		}
		changed = true
	}
	isRepo, err := inspectOnboardGitRoot(root)
	if err != nil || !isRepo {
		return false, false, Domainf("workspace Git contract did not produce a usable repository: %v", err)
	}
	relative, err = gitAtSource(root, "config", "--local", "--get", "worktree.useRelativePaths")
	if err != nil || strings.TrimSpace(string(relative)) != "true" {
		return false, false, Domainf("workspace Git contract did not retain worktree.useRelativePaths=true")
	}
	env := append(sourceGitEnv(), "GIT_OPTIONAL_LOCKS=0")
	if _, err := gitOutput(root, env, nil, "status", "--porcelain=v1", "--untracked-files=no"); err != nil {
		return false, false, Domainf("host read-only Git probe failed for %q: %v", root, err)
	}
	return changed, initialized, nil
}

// RepairOnboardProjectGit verifies an already-durable Project. It never
// initializes a repository — a registered Project whose workspace stopped
// being one is a fact to report, not to paper over; legacy rows may only
// receive the required relative-path config repair.
func RepairOnboardProjectGit(root string, mutate OnboardGitMutation) (bool, error) {
	isRepo, err := inspectOnboardGitRoot(root)
	if err != nil {
		return false, err
	}
	if !isRepo {
		return false, Domainf("Project %q is no longer a Git repository", root)
	}
	relative, relativeErr := gitAtSource(root, "config", "--local", "--get", "worktree.useRelativePaths")
	if relativeErr == nil && strings.TrimSpace(string(relative)) == "true" {
		return false, nil
	}
	if mutate == nil {
		return false, Domainf("workspace Git configurator is unavailable")
	}
	if err := mutate(root, "config", "--local", "worktree.useRelativePaths", "true"); err != nil {
		return false, err
	}
	relative, err = gitAtSource(root, "config", "--local", "--get", "worktree.useRelativePaths")
	if err != nil || strings.TrimSpace(string(relative)) != "true" {
		return false, Domainf("workspace Git contract did not retain worktree.useRelativePaths=true")
	}
	return true, nil
}
