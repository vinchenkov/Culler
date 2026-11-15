//go:build !test_fake_routing

package main

// The production onboarding smoke. It owns only setup, observation, the
// explicit packet decision, and teardown of its smoke Project. Every
// pipeline transition is still produced by the normal resident tick and real
// adapters.

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mc/deployment"
	"mc/verbs"
)

const (
	productionSmokePollInterval = 2 * time.Second
	productionSmokePollAttempts = 300 // ten minutes
)

// smokeStateVersion is the version of the smoke state that this smoke check
// writes. The checks of another version do not prove the receipt of this
// smoke check, so a state file with another version counts as no smoke state.
const smokeStateVersion = 2

type productionSmokeState struct {
	Version       int    `json:"version"`
	Phase         string `json:"phase"`
	ProjectID     string `json:"project_id"`
	WorkspaceRoot string `json:"workspace_root"`
	TaskID        int64  `json:"task_id"`
}

type productionSmokeCommandResult struct {
	Code   int
	Body   map[string]any
	Stderr string
}

var (
	productionSmokeCommand       func([]string) productionSmokeCommandResult
	productionSmokePrepare       func(string) (productionSmokeState, error)
	productionSmokeRegister      func(productionSmokeState) error
	productionSmokeSleep         func(time.Duration)
	productionSmokeWorkspaceBase func(string) (string, error)
	productionSmokeCheckpoint    func(string, productionSmokeState) error
	productionSmokeCleanup       func(string, productionSmokeState) error
	productionSmokeVerifyMerge   func(productionSmokeState, map[string]any) error
	productionSmokeVerifyWiki    func(productionSmokeState) error
)

func init() {
	productionSmokeCommand = runProductionSmokeCommand
	productionSmokePrepare = prepareProductionSmoke
	productionSmokeRegister = registerSmokeProject
	productionSmokeSleep = time.Sleep
	productionSmokeWorkspaceBase = smokeWorkspaceBase
	productionSmokeCheckpoint = writeSmokeState
	productionSmokeCleanup = removeSmokeWorkspace
	productionSmokeVerifyMerge = verifySmokeMerge
	productionSmokeVerifyWiki = verifySmokeWiki
}

func runProductionSmokeCommand(args []string) productionSmokeCommandResult {
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(""), &stdout, &stderr)
	var body map[string]any
	if stdout.Len() != 0 {
		_ = json.Unmarshal(stdout.Bytes(), &body)
	}
	return productionSmokeCommandResult{Code: code, Body: body, Stderr: stderr.String()}
}

func smokeCommandOK(args ...string) (map[string]any, error) {
	result := productionSmokeCommand(args)
	if result.Code != 0 {
		return nil, fmt.Errorf("%s exited %d: %s",
			strings.Join(args, " "), result.Code, strings.TrimSpace(result.Stderr))
	}
	if result.Body == nil {
		return nil, fmt.Errorf("%s returned no JSON receipt", strings.Join(args, " "))
	}
	return result.Body, nil
}

func requireSmokePrivateDir(path, label string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create %s: %w", label, err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm() != 0o700 {
		return fmt.Errorf("%s must be an exact owner-only directory", label)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("%s must be owned by the operator", label)
	}
	return nil
}

func smokeStatePath(home string) string {
	return filepath.Join(home, "smoke", "current.json")
}

func smokeWorkspaceBase(home string) (string, error) {
	return deployment.SmokeWorkspaceBase(home)
}

func validateSmokeState(home string, state productionSmokeState) error {
	if state.Version != smokeStateVersion || !strings.HasPrefix(state.ProjectID, "smoke-") ||
		len(state.ProjectID) != len("smoke-")+16 || state.TaskID < 0 ||
		(state.Phase != "running" && state.Phase != "archived" && state.Phase != "complete") ||
		(state.Phase != "running" && state.TaskID == 0) {
		return fmt.Errorf("onboarding smoke state identity is malformed")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(state.ProjectID, "smoke-")); err != nil {
		return fmt.Errorf("onboarding smoke state identity is malformed")
	}
	base, err := productionSmokeWorkspaceBase(home)
	if err != nil {
		return err
	}
	expected := filepath.Join(base, state.ProjectID, "workspace")
	if state.WorkspaceRoot != expected {
		return fmt.Errorf("onboarding smoke state escaped its deployment root")
	}
	return nil
}

func readSmokeState(home string) (*productionSmokeState, error) {
	path := smokeStatePath(home)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 ||
		info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("onboarding smoke state must be an owner-only regular file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return nil, fmt.Errorf("onboarding smoke state must be owned by the operator")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var state productionSmokeState
	dec := json.NewDecoder(io.LimitReader(file, 4097))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return nil, fmt.Errorf("decode onboarding smoke state: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("onboarding smoke state carries trailing data")
	}
	if state.Version != smokeStateVersion {
		return nil, nil
	}
	if err := validateSmokeState(home, state); err != nil {
		return nil, err
	}
	return &state, nil
}

func writeSmokeState(home string, state productionSmokeState) error {
	if err := validateSmokeState(home, state); err != nil {
		return err
	}
	root := filepath.Join(home, "smoke")
	if err := requireSmokePrivateDir(root, "onboarding smoke root"); err != nil {
		return err
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	stage, err := os.CreateTemp(root, ".current-")
	if err != nil {
		return err
	}
	stagePath := stage.Name()
	defer os.Remove(stagePath)
	if err := stage.Chmod(0o600); err != nil {
		stage.Close()
		return err
	}
	if _, err := stage.Write(body); err != nil {
		stage.Close()
		return err
	}
	if err := stage.Sync(); err != nil {
		stage.Close()
		return err
	}
	if err := stage.Close(); err != nil {
		return err
	}
	if err := os.Rename(stagePath, smokeStatePath(home)); err != nil {
		return err
	}
	dir, err := os.Open(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync onboarding smoke state directory: %w", err)
	}
	return nil
}

func smokeGit(root string, args ...string) error {
	command := append([]string{"-C", root}, args...)
	out, err := exec.Command("git", command...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err,
			strings.TrimSpace(string(out)))
	}
	return nil
}

func prepareSmokeRepository(state productionSmokeState) error {
	parent := filepath.Dir(state.WorkspaceRoot)
	if err := requireSmokePrivateDir(parent, "onboarding smoke generation"); err != nil {
		return err
	}
	if err := requireSmokePrivateDir(state.WorkspaceRoot, "onboarding smoke workspace"); err != nil {
		return err
	}
	gitDir := filepath.Join(state.WorkspaceRoot, ".git")
	if _, err := os.Lstat(gitDir); err == nil {
		return smokeGit(state.WorkspaceRoot, "rev-parse", "--verify", "HEAD")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	entries, err := os.ReadDir(state.WorkspaceRoot)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("fresh onboarding smoke workspace is not empty")
	}
	if err := exec.Command("git", "init", "--initial-branch=culler", state.WorkspaceRoot).Run(); err != nil {
		return fmt.Errorf("initialize onboarding smoke repository: %w", err)
	}
	if err := smokeGit(state.WorkspaceRoot, "config", "user.name", "Culler Smoke"); err != nil {
		return err
	}
	if err := smokeGit(state.WorkspaceRoot, "config", "user.email", "smoke@culler.invalid"); err != nil {
		return err
	}
	readme := filepath.Join(state.WorkspaceRoot, "README.md")
	if err := os.WriteFile(readme, []byte("# Culler smoke\n"), 0o600); err != nil {
		return err
	}
	vision := filepath.Join(state.WorkspaceRoot, verbs.ProjectVisionFile)
	if err := os.WriteFile(vision, []byte(verbs.OnboardSmokeVision), 0o600); err != nil {
		return err
	}
	if err := smokeGit(state.WorkspaceRoot, "add", "README.md", verbs.ProjectVisionFile); err != nil {
		return err
	}
	return smokeGit(state.WorkspaceRoot, "commit", "-m", "Initialize smoke Project")
}

func newSmokeState(home string) (productionSmokeState, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return productionSmokeState{}, err
	}
	id := "smoke-" + hex.EncodeToString(raw[:])
	base, err := productionSmokeWorkspaceBase(home)
	if err != nil {
		return productionSmokeState{}, err
	}
	state := productionSmokeState{
		Version: smokeStateVersion, Phase: "running", ProjectID: id,
		WorkspaceRoot: filepath.Join(base, id, "workspace"),
	}
	if err := requireSmokePrivateDir(filepath.Join(home, "smoke"), "onboarding smoke root"); err != nil {
		return productionSmokeState{}, err
	}
	if err := requireSmokePrivateDir(base, "onboarding smoke deployment root"); err != nil {
		return productionSmokeState{}, err
	}
	if err := requireSmokePrivateDir(filepath.Join(base, id), "onboarding smoke generation"); err != nil {
		return productionSmokeState{}, err
	}
	if err := requireSmokePrivateDir(state.WorkspaceRoot, "onboarding smoke workspace"); err != nil {
		return productionSmokeState{}, err
	}
	if err := writeSmokeState(home, state); err != nil {
		return productionSmokeState{}, err
	}
	return state, nil
}

func removeSmokeWorkspace(home string, state productionSmokeState) error {
	if err := validateSmokeState(home, state); err != nil {
		return err
	}
	generation := filepath.Dir(state.WorkspaceRoot)
	info, err := os.Lstat(generation)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm() != 0o700 {
		return fmt.Errorf("onboarding smoke generation cannot be safely removed")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("onboarding smoke generation is not operator-owned")
	}
	// RemoveTree, not os.RemoveAll: the generation contains the smoke's own task
	// roots, and a task root is created mode 0555 as a tamper fence — a directory
	// without its write bit refuses unlinkat for everything inside it, so plain
	// removal dies with "permission denied" on a tree this process fully owns.
	// The fences above still decide WHETHER to remove; this only survives HOW.
	if err := verbs.RemoveTree(generation); err != nil {
		return fmt.Errorf("remove onboarding smoke generation: %w", err)
	}
	// Retract the artifact grant together with the directory it authorizes.
	// The mount allowlist resolves every root by filesystem identity, so one
	// dangling entry refuses every mounted dispatch in the deployment: every
	// Project, every role, permanently. The refusal then names a path inside
	// an archived Project, which reads as a defect in the Project that works.
	if _, err := verbs.RemoveProjectMountGrant(
		home, state.ProjectID, verbs.ProjectArtifactRoot(state.WorkspaceRoot),
	); err != nil {
		return fmt.Errorf("retract onboarding smoke artifact grant: %w", err)
	}
	parent, err := os.Open(filepath.Dir(generation))
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}

func registerSmokeProject(state productionSmokeState) error {
	var receiptOut, receiptErr bytes.Buffer
	code := brokerOnboardState([]string{
		"onboard", "smoke", "--smoke",
		"--project", state.ProjectID,
		"--workspace-root", state.WorkspaceRoot,
	}, &receiptOut, &receiptErr)
	if code != 0 {
		return fmt.Errorf("register smoke Project: %s", strings.TrimSpace(receiptErr.String()))
	}
	return nil
}

func prepareProductionSmoke(home string) (productionSmokeState, error) {
	state, err := readSmokeState(home)
	if err != nil {
		return productionSmokeState{}, err
	}
	if state == nil {
		fresh, err := newSmokeState(home)
		if err != nil {
			return productionSmokeState{}, err
		}
		state = &fresh
	}
	if state.TaskID > 0 {
		return *state, nil
	}
	if err := prepareSmokeRepository(*state); err != nil {
		return productionSmokeState{}, err
	}
	if err := productionSmokeRegister(*state); err != nil {
		return productionSmokeState{}, err
	}
	tasks, err := smokeCommandOK("task", "list", "--all", "--project", state.ProjectID)
	if err != nil {
		return productionSmokeState{}, err
	}
	rows, ok := tasks["tasks"].([]any)
	if !ok || len(rows) != 1 {
		return productionSmokeState{}, fmt.Errorf(
			"smoke Project has %d tasks, want exactly one", len(rows))
	}
	task, ok := rows[0].(map[string]any)
	if !ok {
		return productionSmokeState{}, fmt.Errorf("smoke task receipt is malformed")
	}
	taskID, ok := jsonInt64(task["id"])
	if !ok || taskID <= 0 {
		return productionSmokeState{}, fmt.Errorf("smoke task id is malformed")
	}
	state.TaskID = taskID
	if err := writeSmokeState(home, *state); err != nil {
		return productionSmokeState{}, err
	}
	return *state, nil
}

// smokeApprovalPrompt asks the operator for the approval of the smoke task.
// Each line has a maximum of 40 characters, so a narrow terminal shows it
// with no wrap.
const smokeApprovalPrompt = `Smoke task %d is ready for review:
%v

Your approval starts these steps:
1. Culler merges the task into culler.
2. The smoke check waits for the end
   of the first wiki update.
3. The smoke check archives the smoke
   Project.

Steps 1 and 2 each have a limit of ten
minutes. The terminal shows no text
before the result.

Type exactly: approve %d
`

// smokeResetReason is the stored `--reason` on a retired packet. A cancel
// requires a reason, and this one is read later by whoever finds a cancelled
// packet in the history and asks what happened to it.
const smokeResetReason = "onboarding-smoke-reset"

// discardSmokeState removes the generation pointer so the next `--smoke` mints
// a fresh one. The workspace directory it named is deliberately left on disk:
// it may hold real commits, and no retry is worth destroying uncommitted work
// for. A fresh generation gets a fresh random id and its own directory.
func discardSmokeState(home string) error {
	if err := os.Remove(smokeStatePath(home)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("discard onboarding smoke state: %w", err)
	}
	dir, err := os.Open(filepath.Join(home, "smoke"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// smokeProjectStatus reports the registered status of the generation's
// Project, and whether the spine has it at all. A rebuilt or restored spine
// legitimately does not, and a reset must not fail on that.
func smokeProjectStatus(projectID string) (string, bool, error) {
	body, err := smokeCommandOK("project", "list")
	if err != nil {
		return "", false, err
	}
	rows, ok := body["projects"].([]any)
	if !ok {
		return "", false, fmt.Errorf("smoke Project receipt is malformed")
	}
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok || row["id"] != projectID {
			continue
		}
		status, _ := row["status"].(string)
		return status, true, nil
	}
	return "", false, nil
}

// smokeHasLivePacket reports whether this task still holds one of the global
// review slots. Occupancy is counted across ALL Projects, so archiving the
// Project does not return the slot — only a decision does.
func smokeHasLivePacket(taskID int64) (bool, error) {
	_, found, err := smokeReviewPacket(taskID)
	return found, err
}

func smokeReviewPacket(taskID int64) (map[string]any, bool, error) {
	body, err := smokeCommandOK("packet", "list")
	if err != nil {
		return nil, false, err
	}
	packets, ok := body["packets"].([]any)
	if !ok {
		return nil, false, fmt.Errorf("smoke packet receipt is malformed")
	}
	for _, raw := range packets {
		packet, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := jsonInt64(packet["task_id"])
		archived, _ := jsonInt64(packet["archived"])
		if id == taskID && archived == 0 {
			return packet, true, nil
		}
	}
	return nil, false, nil
}

func decideSmokePacket(taskID int64, decision, reason string, reviewed ...map[string]any) error {
	var packet map[string]any
	if len(reviewed) == 1 {
		packet = reviewed[0]
	} else {
		var err error
		packet, _, err = smokeReviewPacket(taskID)
		if err != nil {
			return err
		}
	}
	packetID, packetOK := jsonInt64(packet["packet_id"])
	versionID, versionOK := jsonInt64(packet["version_id"])
	if !packetOK || !versionOK || packetID < 1 || versionID < 1 {
		return fmt.Errorf("the smoke decision requires its reviewed packet and submitted version")
	}
	args := []string{"packet", "decide", strconv.FormatInt(taskID, 10), "--" + decision, "--packet", strconv.FormatInt(packetID, 10), "--version", strconv.FormatInt(versionID, 10)}
	if reason != "" {
		args = append(args, "--reason", reason)
	}
	_, err := smokeCommandOK(args...)
	return err
}

// abandonProductionSmoke retires the registered generation so a fresh
// `mc __onboard --smoke` can mint a new one.
//
// The smoke is resumable by design: `prepareProductionSmoke` returns the
// checkpointed state whenever one exists, and every rerun therefore lands back
// on the same Project and the same task. That is correct while the
// generation can still finish, and a trap once it cannot — a Project
// registered before its sandbox profile carried a synthetic home is refused by
// every dispatch tick forever, and rerunning the smoke cannot escape it
// because the rerun resumes exactly the row that is being refused.
//
// Retirement is the smallest set of writes that makes the old generation
// inert, and nothing more. Archiving the Project drops all of its tasks out
// of selection (`projectActive`), which is what stops the resident.
// Cancelling a live packet returns the review slot, which occupancy counts
// deployment-wide and archiving alone would strand. An approved task whose
// merge is not applied refuses instead: the operator authorized that merge,
// the merge may be in flight, and a decision is never rewritten.
func abandonProductionSmoke(home string) (map[string]any, error) {
	state, err := readSmokeState(home)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return map[string]any{
			"abandoned": false,
			"detail":    "no onboarding smoke generation is registered; nothing to retire",
		}, nil
	}
	if state.TaskID > 0 {
		task, err := smokeCommandOK("task", "get", strconv.FormatInt(state.TaskID, 10))
		if err != nil {
			return nil, err
		}
		decision, _ := task["decision"].(string)
		archived, _ := jsonInt64(task["archived"])
		switch {
		case decision == "approved" && archived != 1:
			return nil, fmt.Errorf(
				"smoke task %d is approved and its merge is not applied yet: retiring it now would abandon a merge the operator authorized; wait for it to finish, then reset",
				state.TaskID)
		case decision == "":
			live, err := smokeHasLivePacket(state.TaskID)
			if err != nil {
				return nil, err
			}
			if live {
				if err := decideSmokePacket(state.TaskID, "cancel", smokeResetReason); err != nil {
					return nil, fmt.Errorf("cancel stuck smoke packet: %w", err)
				}
			}
		}
	}
	status, present, err := smokeProjectStatus(state.ProjectID)
	if err != nil {
		return nil, err
	}
	if present && status != "archived" {
		if _, err := smokeCommandOK("project", "archive", state.ProjectID); err != nil {
			return nil, fmt.Errorf("archive stuck smoke Project: %w", err)
		}
	}
	if err := discardSmokeState(home); err != nil {
		return nil, err
	}
	return map[string]any{
		"abandoned": true, "project_id": state.ProjectID,
		"task_id": state.TaskID, "project_present": present,
		"detail": "smoke generation retired; the next mc __onboard --smoke starts a fresh one",
	}, nil
}

func jsonInt64(value any) (int64, bool) {
	number, ok := value.(float64)
	if !ok || number < 0 || number != float64(int64(number)) {
		return 0, false
	}
	return int64(number), true
}

func smokeTaskReady(task map[string]any) (bool, error) {
	blocked, _ := jsonInt64(task["blocked"])
	if blocked != 0 {
		return false, fmt.Errorf("smoke task blocked: %v", task["blocked_reason"])
	}
	archived, _ := jsonInt64(task["archived"])
	if archived != 0 {
		return false, fmt.Errorf("smoke task archived before its packet approval")
	}
	return task["status"] == "packaged" &&
		(task["decision"] == nil || task["decision"] == ""), nil
}

func waitForSmokeTask(state productionSmokeState, wantArchived bool) (map[string]any, error) {
	for attempt := 0; attempt <= productionSmokePollAttempts; attempt++ {
		body, err := smokeCommandOK("task", "get", strconv.FormatInt(state.TaskID, 10))
		if err != nil {
			return nil, err
		}
		if wantArchived {
			archived, _ := jsonInt64(body["archived"])
			if archived == 1 && body["decision"] == "approved" {
				return body, nil
			}
			blocked, _ := jsonInt64(body["blocked"])
			if blocked != 0 {
				return nil, fmt.Errorf("smoke merge blocked: %v", body["blocked_reason"])
			}
		} else if ready, err := smokeTaskReady(body); err != nil {
			return nil, err
		} else if ready {
			live, err := smokeHasLivePacket(state.TaskID)
			if err != nil {
				return nil, err
			}
			if live {
				return body, nil
			}
		}
		if attempt == productionSmokePollAttempts {
			break
		}
		productionSmokeSleep(productionSmokePollInterval)
	}
	return nil, fmt.Errorf("full-pipeline smoke exceeded its ten-minute bound")
}

// smokeRoleHistoryComplete proves the agent runs of the smoke task. The
// Arbiter selects the task from the proposals of the Project, so its run holds
// no task. A wiki update has no task, so a Scribe run with the smoke task
// fails the proof. The run of a repository operation holds the binding
// mechanical and is no agent run.
func smokeRoleHistoryComplete(state productionSmokeState) error {
	body, err := smokeCommandOK("run", "list")
	if err != nil {
		return err
	}
	rows, ok := body["runs"].([]any)
	if !ok {
		return fmt.Errorf("smoke run history receipt is malformed")
	}
	seen := map[string]bool{}
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok || row["tier"] != "pipeline" || row["project"] != state.ProjectID ||
			row["binding"] == "mechanical" {
			continue
		}
		subject, subjectOK := jsonInt64(row["subject"])
		ofTask := subjectOK && subject == state.TaskID
		role, _ := row["role"].(string)
		role = strings.SplitN(role, ":", 2)[0]
		if role == "scribe" && ofTask {
			return fmt.Errorf("smoke run history has a Scribe run for smoke task %d. A wiki update has no task.", state.TaskID)
		}
		if row["ended_at"] == nil || row["outcome"] != "completed" {
			continue
		}
		switch role {
		case "arbiter":
			seen[role] = true
		case "worker", "verifier", "packager":
			if ofTask {
				seen[role] = true
			}
		}
	}
	for _, role := range []string{"arbiter", "worker", "verifier", "packager"} {
		if !seen[role] {
			return fmt.Errorf("smoke run history has no completed %s for %s",
				role, state.ProjectID)
		}
	}
	return nil
}

// smokeWikiState returns the row of the smoke Project in the wiki state read.
// The read holds no row of an archived Project, and the result is then nil.
// The smoke check archives its Project only after the wiki proof, so a resumed
// smoke check reads an archived Project only after that proof.
func smokeWikiState(state productionSmokeState) (map[string]any, error) {
	body, err := smokeCommandOK("wiki-state", "list")
	if err != nil {
		return nil, fmt.Errorf("production smoke: %w", err)
	}
	rows, _ := body["wiki_states"].([]any)
	for _, raw := range rows {
		if row, ok := raw.(map[string]any); ok && row["project"] == state.ProjectID {
			return row, nil
		}
	}
	status, present, err := smokeProjectStatus(state.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("production smoke: %w", err)
	}
	if !present || status != "archived" {
		return nil, errors.New("Culler holds no wiki state of the smoke Project. " +
			"Ask your coding harness to find the cause.")
	}
	return nil, nil
}

// smokeWikiText returns the operator text of the read for a Project whose
// wiki update has no end: the step of the active wiki update with its repair,
// or the condition of the next start.
func smokeWikiText(row map[string]any) string {
	var texts []string
	if active, ok := row["active"].(map[string]any); ok {
		for _, key := range []string{"step_text", "repair_text"} {
			if text, _ := active[key].(string); text != "" {
				texts = append(texts, text)
			}
		}
	} else if text, _ := row["next_start_text"].(string); text != "" {
		texts = append(texts, text)
	}
	return strings.Join(texts, " ")
}

// smokeFirstWikiRun returns the run of the first wiki update of the smoke
// Project.
func smokeFirstWikiRun(state productionSmokeState) (any, error) {
	body, err := smokeCommandOK("run", "list")
	if err != nil {
		return nil, fmt.Errorf("production smoke: %w", err)
	}
	rows, _ := body["runs"].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if _, ok := row["wiki_update"].(map[string]any); ok && row["project"] == state.ProjectID {
			return row["id"], nil
		}
	}
	return nil, nil
}

// waitForSmokeWikiUpdate waits until a wiki update of the smoke Project has an
// end, and proves the last one that has an end. The first wiki update of a
// Project writes each wiki file, so it must merge a wiki commit. A later wiki
// update can succeed with no change.
func waitForSmokeWikiUpdate(state productionSmokeState) error {
	waiting := ""
	for attempt := 0; attempt <= productionSmokePollAttempts; attempt++ {
		row, err := smokeWikiState(state)
		if err != nil || row == nil {
			return err
		}
		if last, ok := row["last"].(map[string]any); ok {
			if last["result"] != "succeeded" {
				cause, _ := last["cause_text"].(string)
				return fmt.Errorf("The last wiki update of the smoke Project failed. %s\n"+
					"Ask your coding harness to resume the smoke check after the next wiki update merges its wiki commit.", cause)
			}
			if last["merged_commit"] != nil {
				return nil
			}
			first, err := smokeFirstWikiRun(state)
			if err != nil {
				return err
			}
			if first == last["run_id"] {
				return errors.New("The first wiki update of the smoke Project merged no wiki commit. " +
					"The Scribe run changed no wiki file. " +
					"Ask your coding harness to read the trace of that Scribe run.")
			}
			return nil
		}
		waiting = smokeWikiText(row)
		if attempt == productionSmokePollAttempts {
			break
		}
		productionSmokeSleep(productionSmokePollInterval)
	}
	if waiting != "" {
		waiting = " " + waiting
	}
	return fmt.Errorf("The smoke check waited ten minutes. No wiki update of the smoke Project ended.%s\n"+
		"Ask your coding harness to resume the smoke check.", waiting)
}

func smokeCommitID(value any) (string, bool) {
	sha, ok := value.(string)
	if !ok || (len(sha) != 40 && len(sha) != 64) {
		return "", false
	}
	if _, err := hex.DecodeString(sha); err != nil || strings.ToLower(sha) != sha {
		return "", false
	}
	return sha, true
}

func smokeGitOutput(root string, args ...string) ([]byte, error) {
	command := append([]string{"-C", root}, args...)
	out, err := exec.Command("git", command...).Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

func verifySmokeMerge(state productionSmokeState, task map[string]any) error {
	delivery, _ := task["delivery"].(map[string]any)
	sha, ok := smokeCommitID(delivery["submitted_commit"])
	if !ok {
		return fmt.Errorf("the accepted smoke task has no canonical submitted commit")
	}
	if delivery["destination_branch"] != "culler" {
		return fmt.Errorf("the accepted smoke task destination changed from culler")
	}
	if err := smokeGit(state.WorkspaceRoot, "merge-base", "--is-ancestor", sha, "culler"); err != nil {
		return fmt.Errorf("verified smoke revision did not reach culler: %w", err)
	}
	body, err := smokeGitOutput(state.WorkspaceRoot, "show", "culler:SMOKE_RESULT.md")
	if err != nil {
		return fmt.Errorf("read landed smoke artifact: %w", err)
	}
	if string(body) != "culler smoke ok\n" {
		return fmt.Errorf("landed SMOKE_RESULT.md does not contain the exact requested bytes")
	}
	return nil
}

// verifySmokeWiki proves that the culler branch of the smoke repository holds
// the Project wiki. Git lists no entry and reports no error for a path that
// the branch does not hold, so an error of Git is a failed read.
func verifySmokeWiki(state productionSmokeState) error {
	entry, err := smokeGitOutput(state.WorkspaceRoot, "ls-tree", "culler", "--", "openwiki/index.md")
	if err != nil {
		return fmt.Errorf("The smoke check cannot read the culler branch of the smoke repository: %v\n"+
			"Ask your coding harness to resume the smoke check.", err)
	}
	if len(bytes.TrimSpace(entry)) == 0 {
		return errors.New("The smoke check found no file openwiki/index.md on the culler branch of the smoke repository. " +
			"Ask your coding harness to read the trace of the last Scribe run of the smoke Project.")
	}
	return nil
}

func writeSmokeReceipt(stdout io.Writer, body map[string]any) int {
	if err := writeJSON(stdout, body); err != nil {
		return 2
	}
	return 0
}

func completeSmokeReceipt(stdout io.Writer, state productionSmokeState) int {
	return writeSmokeReceipt(stdout, map[string]any{
		"ok": true,
		"sections": []map[string]any{{
			"section": "smoke", "status": "done",
			"detail": "Culler merged the smoke task. Culler merged the wiki commit of a wiki update.",
		}},
		"task_id": state.TaskID, "project_id": state.ProjectID,
	})
}

func brokerOnboardSmoke(a verbs.OnboardArgs, stdin io.Reader, stdout, stderr io.Writer) int {
	home, err := configuredCanonicalHome()
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("resolve production MC_HOME: %v", err))
	}
	if a.SmokeReset {
		// Before prepare, never after: prepare is what resumes the generation
		// this flag exists to escape, and it registers a fresh one on the way.
		body, err := abandonProductionSmoke(home)
		if err != nil {
			return writeVerbError(stdout, stderr, verbs.Domainf("reset production smoke: %v", err))
		}
		body["ok"] = true
		return writeSmokeReceipt(stdout, body)
	}
	state, err := productionSmokePrepare(home)
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Domainf("prepare production smoke: %v", err))
	}
	if state.Phase == "complete" {
		return completeSmokeReceipt(stdout, state)
	}
	if state.Phase == "archived" {
		if err := productionSmokeCleanup(home, state); err != nil {
			return writeVerbError(stdout, stderr, verbs.Domainf("clean production smoke workspace: %v", err))
		}
		state.Phase = "complete"
		if err := productionSmokeCheckpoint(home, state); err != nil {
			return writeVerbError(stdout, stderr, verbs.Domainf("complete production smoke state: %v", err))
		}
		return completeSmokeReceipt(stdout, state)
	}
	task, err := smokeCommandOK("task", "get", strconv.FormatInt(state.TaskID, 10))
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Domainf("production smoke: %v", err))
	}
	decision, _ := task["decision"].(string)
	archived, _ := jsonInt64(task["archived"])
	switch decision {
	case "approved":
		// The task is the durable decision record. A crash can occur after the
		// real packet decision commits but before this broker observes the merge;
		// resume from that record without asking for or writing a second decision.
	case "":
		if archived != 0 {
			return writeVerbError(stdout, stderr, verbs.Domainf(
				"production smoke task archived without an approved decision"))
		}
		task, err = waitForSmokeTask(state, false)
		if err != nil {
			return writeVerbError(stdout, stderr, verbs.Domainf("production smoke: %v", err))
		}
		reviewed, found, err := smokeReviewPacket(state.TaskID)
		if err != nil {
			return writeVerbError(stdout, stderr, verbs.Domainf("the production smoke requires its current review packet: %v", err))
		}
		if !found {
			return writeVerbError(stdout, stderr, verbs.Domainf(
				"The smoke review packet is unavailable. Ask your coding harness to resume the smoke check."))
		}

		if a.SmokeApproveTask != 0 && a.SmokeApproveTask != state.TaskID {
			return writeVerbError(stdout, stderr, verbs.Domainf(
				"production smoke packet is task %d, not approved task %d",
				state.TaskID, a.SmokeApproveTask))
		}
		if a.SmokeApproveTask == 0 {
			fmt.Fprintf(stderr, smokeApprovalPrompt, state.TaskID, task["title"], state.TaskID)
			line, readErr := bufio.NewReader(stdin).ReadString('\n')
			if strings.TrimSpace(line) != "approve "+strconv.FormatInt(state.TaskID, 10) {
				detail := fmt.Sprintf(
					"smoke packet task %d awaits explicit operator approval; no decision was written",
					state.TaskID)
				if readErr != nil && !errors.Is(readErr, io.EOF) {
					detail += ": " + readErr.Error()
				}
				_ = writeJSON(stdout, map[string]any{
					"ok": false, "stopped_at": "smoke-approval", "detail": detail,
					"task_id": state.TaskID, "project_id": state.ProjectID,
				})
				fmt.Fprintln(stderr, "mc:", detail)
				return 1
			}
		}

		if err := decideSmokePacket(state.TaskID, "approve", "", reviewed); err != nil {
			return writeVerbError(stdout, stderr, verbs.Domainf("approve production smoke packet: %v", err))
		}
	default:
		return writeVerbError(stdout, stderr, verbs.Domainf(
			"production smoke packet already has terminal decision %q", decision))
	}
	if archived != 1 {
		task, err = waitForSmokeTask(state, true)
		if err != nil {
			return writeVerbError(stdout, stderr, verbs.Domainf("production smoke merge: %v", err))
		}
	}
	if err := productionSmokeVerifyMerge(state, task); err != nil {
		return writeVerbError(stdout, stderr, verbs.Domainf("production smoke merge evidence: %v", err))
	}
	if err := smokeRoleHistoryComplete(state); err != nil {
		return writeVerbError(stdout, stderr, verbs.Domainf("production smoke evidence: %v", err))
	}
	if err := waitForSmokeWikiUpdate(state); err != nil {
		return writeVerbError(stdout, stderr, verbs.Domainf("%v", err))
	}
	if err := productionSmokeVerifyWiki(state); err != nil {
		return writeVerbError(stdout, stderr, verbs.Domainf("%v", err))
	}
	if _, err := smokeCommandOK("project", "archive", state.ProjectID); err != nil {
		return writeVerbError(stdout, stderr, verbs.Domainf("archive production smoke Project: %v", err))
	}
	state.Phase = "archived"
	if err := productionSmokeCheckpoint(home, state); err != nil {
		return writeVerbError(stdout, stderr, verbs.Domainf("checkpoint archived production smoke: %v", err))
	}
	if err := productionSmokeCleanup(home, state); err != nil {
		return writeVerbError(stdout, stderr, verbs.Domainf("clean production smoke workspace: %v", err))
	}
	state.Phase = "complete"
	if err := productionSmokeCheckpoint(home, state); err != nil {
		return writeVerbError(stdout, stderr, verbs.Domainf("complete production smoke state: %v", err))
	}
	return completeSmokeReceipt(stdout, state)
}
