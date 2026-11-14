package deployment

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"mc/routing"
)

type liveProviderState struct {
	Version         int                     `json:"version"`
	AttemptID       string                  `json:"attempt_id"`
	ReleaseBuildID  string                  `json:"release_build_id"`
	Binding         string                  `json:"binding"`
	Credential      string                  `json:"credential,omitempty"`
	Model           string                  `json:"model,omitempty"`
	Delivery        string                  `json:"delivery"`
	Status          string                  `json:"status"`
	StartedAt       string                  `json:"started_at"`
	VerifiedAt      string                  `json:"verified_at,omitempty"`
	AuthorizationID string                  `json:"authorization_id"`
	ProfileRevision string                  `json:"profile_revision"`
	Observation     LiveProviderObservation `json:"observation,omitempty"`
}

type LiveProviderClaim struct {
	home  string
	state liveProviderState
	file  *os.File
}

func (c *LiveProviderClaim) Result() LiveProviderResult {
	if c == nil {
		return LiveProviderResult{}
	}
	return liveProviderResult(c.state)
}

func (c *LiveProviderClaim) AttemptID() string {
	if c == nil {
		return ""
	}
	return c.state.AttemptID
}

func (c *LiveProviderClaim) AuthorizationID() string {
	if c == nil {
		return ""
	}
	return c.state.AuthorizationID
}

func (c *LiveProviderClaim) Close() error {
	if c == nil || c.file == nil {
		return nil
	}
	err := syscall.Flock(int(c.file.Fd()), syscall.LOCK_UN)
	closeErr := c.file.Close()
	c.file = nil
	if err != nil {
		return err
	}
	return closeErr
}

func liveProviderRoot(home string) string {
	return filepath.Join(home, "acceptance", "live-provider")
}

func liveProviderClaimPath(home, binding string) string {
	return filepath.Join(liveProviderRoot(home), binding+".claim.json")
}

func liveProviderReceiptPath(home, release, binding string) string {
	return filepath.Join(liveProviderRoot(home), "receipts", release, binding+".json")
}

// Kept as the receipt locator for focused package tests.
func liveProviderStatePath(home, release, binding string) string {
	return liveProviderReceiptPath(home, release, binding)
}

func liveProviderResult(state liveProviderState) LiveProviderResult {
	return LiveProviderResult{
		Status: state.Status, Binding: state.Binding, Credential: state.Credential,
		Model: state.Model, AttemptID: state.AttemptID,
		ReleaseBuildID: state.ReleaseBuildID, Delivery: state.Delivery,
		SessionID: state.Observation.SessionID, NativeFile: state.Observation.NativeFile,
		StreamEvents: state.Observation.StreamEvents, ToolUses: state.Observation.ToolUses,
		ProviderRequests: state.Observation.ProviderRequests,
	}
}

func ensureLiveProviderRoot(home string) (string, error) {
	acceptance := filepath.Join(home, "acceptance")
	if err := ensureOwnerDirectory(acceptance, "acceptance state root"); err != nil {
		return "", err
	}
	root := liveProviderRoot(home)
	if err := ensureOwnerDirectory(root, "live-provider acceptance root"); err != nil {
		return "", err
	}
	return root, nil
}

func ensureLiveProviderReceiptRoot(home, release string) (string, error) {
	root, err := ensureLiveProviderRoot(home)
	if err != nil {
		return "", err
	}
	receipts := filepath.Join(root, "receipts")
	if err := ensureOwnerDirectory(receipts, "live-provider receipt root"); err != nil {
		return "", err
	}
	releaseRoot := filepath.Join(receipts, release)
	if err := ensureOwnerDirectory(releaseRoot, "live-provider release receipt root"); err != nil {
		return "", err
	}
	return releaseRoot, nil
}

func encodeLiveProviderState(state liveProviderState) ([]byte, error) {
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func decodeLiveProviderState(body []byte, binding string) (liveProviderState, error) {
	var state liveProviderState
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return state, err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return state, fmt.Errorf("live-provider state carries trailing JSON")
	}
	if state.Version != 1 || !validSHA256(state.AttemptID) ||
		!validProductionBuildID(state.ReleaseBuildID) ||
		state.Binding != binding || state.Delivery != "proxy" ||
		!validSHA256(state.AuthorizationID) || !validSHA256(state.ProfileRevision) ||
		(state.Status != "running" && state.Status != "verified") ||
		state.StartedAt == "" {
		return state, fmt.Errorf("live-provider state identity is malformed")
	}
	return state, nil
}

func readLiveProviderStateAt(path, binding string) (liveProviderState, error) {
	var state liveProviderState
	info, err := os.Lstat(path)
	if err != nil {
		return state, err
	}
	uid, links, ok := statOwner(info)
	if !ok || int(uid) != os.Getuid() || info.Mode()&os.ModeSymlink != 0 ||
		!info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || links != 1 ||
		info.Size() <= 0 || info.Size() > 64*1024 {
		return state, fmt.Errorf("live-provider state is not an owner-only regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return state, err
	}
	return decodeLiveProviderState(body, binding)
}

func writeLinkedState(root, final string, state liveProviderState, keepLocked bool) (*os.File, error) {
	body, err := encodeLiveProviderState(state)
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(root, ".provider-")
	if err != nil {
		return nil, err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	fail := func(err error) (*os.File, error) {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		return fail(err)
	}
	if keepLocked {
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			return fail(err)
		}
	}
	if _, err := file.Write(body); err != nil {
		return fail(err)
	}
	if err := file.Sync(); err != nil {
		return fail(err)
	}
	if err := os.Link(tmp, final); err != nil {
		return fail(err)
	}
	if err := os.Remove(tmp); err != nil {
		_ = os.Remove(final)
		return fail(err)
	}
	if err := syncDirectory(root); err != nil {
		_ = os.Remove(final)
		return fail(err)
	}
	if keepLocked {
		return file, nil
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return nil, nil
}

func newLiveProviderAttemptID() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func ReplayLiveProviderAcceptance(home, release, binding string) (LiveProviderResult, bool, error) {
	var result LiveProviderResult
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return result, false, err
	}
	if err := validateLiveProviderIdentity(canonicalHome, release, binding); err != nil {
		return result, false, err
	}
	state, err := readLiveProviderStateAt(
		liveProviderReceiptPath(canonicalHome, release, binding), binding)
	if errors.Is(err, fs.ErrNotExist) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	if state.Status != "verified" || state.ReleaseBuildID != release ||
		state.VerifiedAt == "" || state.Observation.SessionID == "" ||
		state.Observation.NativeFile == "" || state.Observation.StreamEvents <= 0 ||
		state.Observation.ToolUses != 1 || state.Observation.ToolResults != 1 {
		return result, false, fmt.Errorf("verified live-provider state is incomplete")
	}
	if state.Observation.ProviderRequests < 1 || state.Observation.ProviderRequests > maxLiveProviderRequests {
		return result, false, fmt.Errorf("The provider receipt lacks bounded host request evidence.")
	}
	spec, err := validateEnabledRuntimeBinding(canonicalHome, binding, nil)
	if err != nil {
		return result, false, err
	}
	identity, err := AgentConnectionAuthorizationIdentity(canonicalHome, spec.Credential)
	if err != nil || identity != state.AuthorizationID || routing.ProfileRevision(spec) != state.ProfileRevision {
		return result, false, fmt.Errorf("The connection or agent profile changed after the provider test.")
	}

	return liveProviderResult(state), true, nil
}

func StatusLiveProviderAcceptance(home, release, binding string) (LiveProviderResult, error) {
	var result LiveProviderResult
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return result, err
	}
	if err := validateLiveProviderIdentity(canonicalHome, release, binding); err != nil {
		return result, err
	}
	claim, err := readLiveProviderStateAt(liveProviderClaimPath(canonicalHome, binding), binding)
	if err == nil {
		return liveProviderResult(claim), nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return result, err
	}
	if replayed, ok, err := ReplayLiveProviderAcceptance(canonicalHome, release, binding); err != nil {
		return result, err
	} else if ok {
		return replayed, nil
	}
	return LiveProviderResult{Status: "absent", Binding: binding, ReleaseBuildID: release}, nil
}

func BeginLiveProviderAcceptance(home, release, binding string, environment []string, now time.Time) (*LiveProviderClaim, error) {
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return nil, err
	}
	if err := requireOwnerOnlyDirectory(canonicalHome, "MC_HOME"); err != nil {
		return nil, err
	}
	if err := validateLiveProviderIdentity(canonicalHome, release, binding); err != nil {
		return nil, err
	}
	if replayed, ok, err := ReplayLiveProviderAcceptance(canonicalHome, release, binding); err != nil {
		return nil, err
	} else if ok {
		return nil, fmt.Errorf("live-provider acceptance is already verified as attempt %s", replayed.AttemptID)
	}
	spec, err := validateEnabledRuntimeBinding(canonicalHome, binding, environment)
	if err != nil {
		return nil, err
	}
	if now.IsZero() {
		return nil, fmt.Errorf("live-provider acceptance requires a host timestamp")
	}
	attemptID, err := newLiveProviderAttemptID()
	if err != nil {
		return nil, err
	}
	state := liveProviderState{
		Version: 1, AttemptID: attemptID, ReleaseBuildID: release, Binding: binding,
		Credential: spec.Credential, Model: spec.Model,
		Delivery: string(spec.Delivery), Status: "running",
		StartedAt: now.UTC().Format(time.RFC3339Nano),
	}
	state.AuthorizationID, err = AgentConnectionAuthorizationIdentity(canonicalHome, spec.Credential)
	if err != nil {
		return nil, err
	}
	state.ProfileRevision = routing.ProfileRevision(spec)

	root, err := ensureLiveProviderRoot(canonicalHome)
	if err != nil {
		return nil, err
	}
	file, err := writeLinkedState(root, liveProviderClaimPath(canonicalHome, binding), state, true)
	if errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf(
			"live-provider acceptance is ambiguous because another binding-global claim exists; explicit status and cleanup are required before any retry")
	}
	if err != nil {
		return nil, err
	}
	return &LiveProviderClaim{home: canonicalHome, state: state, file: file}, nil
}

func CompleteLiveProviderAcceptance(claim *LiveProviderClaim, observation LiveProviderObservation, now time.Time) (LiveProviderResult, error) {
	var result LiveProviderResult
	if claim == nil || claim.file == nil {
		return result, fmt.Errorf("live-provider completion requires the held attempt claim")
	}
	state := claim.state
	pathInfo, err := os.Lstat(liveProviderClaimPath(claim.home, state.Binding))
	fileInfo, fileErr := claim.file.Stat()
	if err != nil || fileErr != nil || !os.SameFile(pathInfo, fileInfo) {
		return result, fmt.Errorf("live-provider claim identity changed during the attempt")
	}
	spec, err := validateEnabledRuntimeBinding(claim.home, state.Binding, nil)
	if err != nil {
		return result, fmt.Errorf("revalidate canonical runtime grant after live turn: %w", err)
	}
	if err := validLiveProviderObservation(observation); err != nil {
		return result, err
	}
	started, err := time.Parse(time.RFC3339Nano, state.StartedAt)
	if err != nil || now.IsZero() || now.Before(started) {
		return result, fmt.Errorf("live-provider completion timestamp is invalid")
	}
	identity, err := AgentConnectionAuthorizationIdentity(claim.home, spec.Credential)
	if err != nil || identity != state.AuthorizationID || routing.ProfileRevision(spec) != state.ProfileRevision {
		return result, fmt.Errorf("The connection or agent profile changed during the provider test.")
	}
	if observation.ProviderRequests < 1 || observation.ProviderRequests > maxLiveProviderRequests {
		return result, fmt.Errorf("The provider test lacks bounded host request evidence.")
	}

	state.Status = "verified"
	state.VerifiedAt = now.UTC().Format(time.RFC3339Nano)
	state.Observation = observation
	receiptRoot, err := ensureLiveProviderReceiptRoot(claim.home, state.ReleaseBuildID)
	if err != nil {
		return result, err
	}
	if _, err := writeLinkedState(
		receiptRoot,
		liveProviderReceiptPath(claim.home, state.ReleaseBuildID, state.Binding),
		state, false,
	); err != nil {
		return result, fmt.Errorf("persist live-provider receipt: %w", err)
	}
	if err := os.Remove(liveProviderClaimPath(claim.home, state.Binding)); err != nil {
		return result, fmt.Errorf("verified receipt is durable but active claim cleanup failed: %w", err)
	}
	if err := syncDirectory(liveProviderRoot(claim.home)); err != nil {
		return result, err
	}
	claim.state = state
	if err := claim.Close(); err != nil {
		return result, err
	}
	return liveProviderResult(state), nil
}

func CleanupLiveProviderAcceptance(
	home, release, binding, attemptID string,
	beforeRemove func() error,
) (LiveProviderResult, error) {
	var result LiveProviderResult
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return result, err
	}
	if err := validateLiveProviderIdentity(canonicalHome, release, binding); err != nil {
		return result, err
	}
	if !validSHA256(attemptID) {
		return result, fmt.Errorf("cleanup requires the exact 64-hex attempt id from status")
	}
	path := liveProviderClaimPath(canonicalHome, binding)
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, fs.ErrNotExist) {
		if replayed, ok, replayErr := ReplayLiveProviderAcceptance(canonicalHome, release, binding); replayErr != nil {
			return result, replayErr
		} else if ok {
			return replayed, nil
		}
		return LiveProviderResult{
			Status: "cleaned", Binding: binding, AttemptID: attemptID,
			ReleaseBuildID: release,
		}, nil
	}
	if err != nil {
		return result, err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return result, fmt.Errorf("live-provider attempt %s is still active; cleanup refused", attemptID)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	state, err := readLiveProviderStateAt(path, binding)
	if err != nil {
		return result, err
	}
	if state.AttemptID != attemptID {
		return result, fmt.Errorf("cleanup attempt id does not match the binding-global claim")
	}
	if beforeRemove != nil {
		if err := beforeRemove(); err != nil {
			return result, err
		}
	}
	if err := os.Remove(path); err != nil {
		return result, err
	}
	if err := syncDirectory(liveProviderRoot(canonicalHome)); err != nil {
		return result, err
	}
	return LiveProviderResult{
		Status: "cleaned", Binding: binding, AttemptID: attemptID,
		ReleaseBuildID: state.ReleaseBuildID,
	}, nil
}
