package deployment

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type LaunchdController interface {
	Loaded(label string) (bool, error)
	Bootstrap(plist string) error
	Bootout(label string) error
}

type SupervisionActivationDeps struct {
	Launchd LaunchdController
	Now     func() time.Time
	Sleep   func(time.Duration)
}

type residentTickReceipt struct {
	Version             int    `json:"version"`
	ReleaseBuildID      string `json:"release_build_id"`
	ConfigSchemaVersion int    `json:"config_schema_version"`
	ResidentInstanceID  string `json:"resident_instance_id"`
	Sequence            int64  `json:"sequence"`
	PreviousCompletedAt string `json:"previous_completed_at,omitempty"`
	CompletedAt         string `json:"completed_at"`
}

func matchingTickReceipt(path, releaseBuildID string, notBefore, notAfter time.Time) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	uid, links, ok := statOwner(info)
	if !ok || int(uid) != os.Getuid() || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() ||
		info.Mode().Perm() != 0o600 || links != 1 || info.Size() <= 0 || info.Size() > 4096 {
		return false, fmt.Errorf("resident tick receipt is not an owner-only regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var receipt residentTickReceipt
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&receipt); err != nil {
		return false, fmt.Errorf("decode resident tick receipt: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return false, fmt.Errorf("resident tick receipt carries trailing JSON")
	}
	completed, err := time.Parse(time.RFC3339Nano, receipt.CompletedAt)
	if err != nil || receipt.Version != 1 || receipt.ConfigSchemaVersion != 1 ||
		receipt.ResidentInstanceID == "" || receipt.Sequence <= 0 {
		return false, fmt.Errorf("resident tick receipt has mismatched release/schema identity")
	}
	// Freshness before identity. Restarting a resident to take a new release
	// leaves the OUTGOING instance's receipt on disk, correct for the release
	// it was written under and stale by definition. Judging its build id would
	// roll the activation back on the first poll, before the incoming resident
	// could tick even once — which is precisely what an upgrade looks like.
	if !notBefore.IsZero() && completed.Before(notBefore) {
		return false, nil
	}
	if !notAfter.IsZero() && completed.After(notAfter) {
		return false, fmt.Errorf("resident tick receipt timestamp is in the future")
	}
	// A receipt that IS current and still names another release is a resident
	// running the wrong code right now; that stays a refusal.
	if receipt.ReleaseBuildID != releaseBuildID {
		return false, fmt.Errorf("resident tick receipt has mismatched release/schema identity")
	}
	return true, nil
}

// installedLaunchAgent is the rollback record of one installed plist.
// previous holds the replaced plist, or nil when no plist was present.
type installedLaunchAgent struct {
	identity     os.FileInfo
	written      bool
	previous     []byte
	previousMode os.FileMode
}

// installLaunchAgent installs the prepared plist of a unit that is not
// loaded. It replaces an installed plist that differs only when that plist is
// an operator-owned regular file with one link.
func installLaunchAgent(source, destination string) (installedLaunchAgent, error) {
	var installed installedLaunchAgent
	body, err := os.ReadFile(source)
	if err != nil {
		return installed, err
	}
	if existing, statErr := os.Lstat(destination); statErr == nil {
		uid, links, ok := statOwner(existing)
		current, readErr := os.ReadFile(destination)
		if readErr != nil || !ok || int(uid) != os.Getuid() || existing.Mode()&os.ModeSymlink != 0 ||
			!existing.Mode().IsRegular() || links != 1 {
			return installed, fmt.Errorf("installed LaunchAgent %q is not an owner-controlled regular file", destination)
		}
		if existing.Mode().Perm() == 0o644 && bytes.Equal(current, body) {
			installed.identity = existing
			return installed, nil
		}
		installed.previous, installed.previousMode = current, existing.Mode().Perm()
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return installed, statErr
	}
	installed.identity, installed.written, err = writeLaunchAgent(destination, body, 0o644)
	return installed, err
}

// writeLaunchAgent replaces destination with body through a temporary file.
// written reports that the rename took place.
func writeLaunchAgent(destination string, body []byte, mode os.FileMode) (identity os.FileInfo, written bool, err error) {
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".mc-launch-agent-")
	if err != nil {
		return nil, false, err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		_ = tmp.Close()
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return nil, false, err
	}
	if _, err := tmp.Write(body); err != nil {
		return nil, false, err
	}
	if err := tmp.Sync(); err != nil {
		return nil, false, err
	}
	if err := tmp.Close(); err != nil {
		return nil, false, err
	}
	if err := os.Rename(tmpPath, destination); err != nil {
		return nil, false, err
	}
	cleanup = false
	if err := syncDirectory(filepath.Dir(destination)); err != nil {
		identity, statErr := os.Lstat(destination)
		if statErr != nil {
			return nil, false, fmt.Errorf("sync installed LaunchAgent: %v; inspect rollback identity: %w", err, statErr)
		}
		return identity, true, err
	}
	info, err := os.Lstat(destination)
	return info, true, err
}

// restoreLaunchAgent undoes installLaunchAgent. It removes a new plist and
// writes back a replaced plist.
func restoreLaunchAgent(path string, installed installedLaunchAgent) error {
	if !installed.written {
		return nil
	}
	current, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) && installed.previous == nil {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(installed.identity, current) {
		return fmt.Errorf("installed LaunchAgent %q changed identity during rollback", path)
	}
	if installed.previous == nil {
		return os.Remove(path)
	}
	_, _, err = writeLaunchAgent(path, installed.previous, installed.previousMode)
	return err
}

func verifyInstalledLaunchAgent(source, destination string) error {
	sourceBody, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	info, err := os.Lstat(destination)
	if err != nil {
		return err
	}
	uid, links, ok := statOwner(info)
	destinationBody, readErr := os.ReadFile(destination)
	if readErr != nil || !ok || int(uid) != os.Getuid() || info.Mode()&os.ModeSymlink != 0 ||
		!info.Mode().IsRegular() || info.Mode().Perm() != 0o644 || links != 1 || !bytes.Equal(sourceBody, destinationBody) {
		return fmt.Errorf("installed LaunchAgent %q differs from its prepared definition", destination)
	}
	return nil
}

// ActivateSupervision is the operator-present half of supervision. The
// prepared bundle remains the source of truth; this function installs those
// three plists, bootstraps the three jobs, and requires a new release-bound
// resident tick receipt. Every partial first activation is rolled back.
func ActivateSupervision(home, launchAgentsDir, releaseBuildID string, deps SupervisionActivationDeps) (string, SupervisionResult, error) {
	var result SupervisionResult
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return "", result, err
	}
	if err := requireLaunchdTCCSafeHome(canonicalHome); err != nil {
		return "", result, err
	}
	if !validProductionBuildID(releaseBuildID) {
		return "", result, fmt.Errorf("activation requires the immutable production release identity")
	}
	if deps.Launchd == nil || deps.Now == nil || deps.Sleep == nil {
		return "", result, fmt.Errorf("activation requires launchd, clock, and wait dependencies")
	}
	names, err := RuntimeNames(canonicalHome)
	if err != nil {
		return "", result, err
	}
	result = SupervisionResultFor(canonicalHome, names)
	if err := validateSupervisionTree(result.Root); err != nil {
		return "", result, fmt.Errorf("validate prepared supervision: %w", err)
	}
	residentBody, err := os.ReadFile(filepath.Join(result.Root, "resident.json"))
	if err != nil || !bytes.Contains(residentBody, []byte(`"releaseBuildId": "`+releaseBuildID+`"`)) {
		return "", result, fmt.Errorf("prepared resident config does not match activation release")
	}
	if launchAgentsDir == "" || !filepath.IsAbs(launchAgentsDir) || filepath.Clean(launchAgentsDir) != launchAgentsDir {
		return "", result, fmt.Errorf("LaunchAgents directory must be a clean absolute path")
	}
	units, labels, sources, destinations := supervisionStartOrder(names, result.Root, launchAgentsDir)
	stopped, err := ReadServiceStops(canonicalHome)
	if err != nil {
		return "", result, err
	}
	for i, source := range sources {
		body, err := os.ReadFile(source)
		if err != nil || !bytes.Contains(body, []byte("<string>"+labels[i]+"</string>")) {
			return "", result, fmt.Errorf("prepared LaunchAgent does not carry expected label %s", labels[i])
		}
	}
	loaded := make([]bool, len(labels))
	for i, label := range labels {
		loaded[i], err = deps.Launchd.Loaded(label)
		if err != nil {
			return "", result, err
		}
	}
	activeCount, expectedCount := 0, 0
	for i := range loaded {
		if stopped[units[i]] {
			if loaded[i] {
				return "", result, fmt.Errorf("the explicitly stopped %s service is loaded", units[i])
			}
			continue
		}
		expectedCount++
		if loaded[i] {
			activeCount++
		}
	}
	if activeCount != 0 && activeCount != expectedCount {
		return "", result, fmt.Errorf("supervision is partially loaded; repair requires explicit operator cleanup")
	}
	if activeCount > 0 {
		for i := range labels {
			if err := verifyInstalledLaunchAgent(sources[i], destinations[i]); err != nil {
				return "", result, err
			}
		}
		if stopped["resident"] {
			return "ok", result, nil
		}
		ok, err := matchingTickReceipt(filepath.Join(canonicalHome, "health", "resident-tick.json"), releaseBuildID, time.Time{}, deps.Now().UTC().Add(5*time.Second))
		if err != nil || !ok {
			return "", result, fmt.Errorf("loaded supervision has no matching resident tick receipt: %v", err)
		}
		return "ok", result, nil
	}
	if err := requireAgentsViewExecutable(AgentsViewPathsFor(canonicalHome).Executable); err != nil {
		return "", result, err
	}
	parent := filepath.Dir(launchAgentsDir)
	if err := requireOperatorSourceDirectory(parent, "LaunchAgents parent"); err != nil {
		return "", result, err
	}
	if err := os.Mkdir(launchAgentsDir, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", result, err
	}
	if err := requireOperatorSourceDirectory(launchAgentsDir, "LaunchAgents directory"); err != nil {
		return "", result, err
	}
	installed := make([]installedLaunchAgent, len(labels))
	bootstrapped := make([]bool, len(labels))
	rollback := func(primary error) error {
		problems := []string{primary.Error()}
		for i := len(labels) - 1; i >= 0; i-- {
			if bootstrapped[i] {
				if err := deps.Launchd.Bootout(labels[i]); err != nil {
					problems = append(problems, fmt.Sprintf("bootout %s: %v", labels[i], err))
				}
			}
		}
		for i := len(labels) - 1; i >= 0; i-- {
			if err := restoreLaunchAgent(destinations[i], installed[i]); err != nil {
				problems = append(problems, err.Error())
			}
		}
		return fmt.Errorf("supervision activation rolled back: %s", strings.Join(problems, "; "))
	}
	for i := range labels {
		installed[i], err = installLaunchAgent(sources[i], destinations[i])
		if err != nil {
			return "", result, rollback(err)
		}
	}
	started := deps.Now().UTC()
	for i := range labels {
		if stopped[units[i]] {
			continue
		}
		if units[i] == "agentsview" {
			if err := InstallAgentsViewConfig(canonicalHome); err != nil {
				return "", result, rollback(err)
			}
		}
		if err := deps.Launchd.Bootstrap(destinations[i]); err != nil {
			return "", result, rollback(fmt.Errorf("bootstrap %s: %w", labels[i], err))
		}
		bootstrapped[i] = true
	}
	for i, label := range labels {
		isLoaded, err := deps.Launchd.Loaded(label)
		if err != nil || isLoaded == stopped[units[i]] {
			if err == nil {
				err = fmt.Errorf("job is not loaded")
			}
			return "", result, rollback(fmt.Errorf("verify %s: %w", label, err))
		}
		destinationBody, readErr := os.ReadFile(destinations[i])
		sourceBody, sourceErr := os.ReadFile(sources[i])
		if readErr != nil || sourceErr != nil || !bytes.Equal(destinationBody, sourceBody) {
			return "", result, rollback(fmt.Errorf("installed LaunchAgent %s changed after bootstrap", label))
		}
	}
	if stopped["resident"] {
		return "done", result, nil
	}
	receiptPath := filepath.Join(canonicalHome, "health", "resident-tick.json")
	for attempt := 0; attempt < 90; attempt++ {
		ok, err := matchingTickReceipt(receiptPath, releaseBuildID, started, deps.Now().UTC().Add(2*time.Minute))
		if err != nil {
			return "", result, rollback(err)
		}
		if ok {
			return "done", result, nil
		}
		deps.Sleep(time.Second)
	}
	return "", result, rollback(fmt.Errorf("resident produced no successful release-bound tick within 90 seconds"))
}

// InspectActiveSupervision is the read-only doctor half: the three exact jobs
// must be loaded from the prepared definitions and the resident must have
// produced a recent receipt for this release/schema.
func InspectActiveSupervision(home, launchAgentsDir, releaseBuildID string, launchd LaunchdController, now time.Time) (SupervisionResult, error) {
	var result SupervisionResult
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return result, err
	}
	if err := requireLaunchdTCCSafeHome(canonicalHome); err != nil {
		return result, err
	}
	if !validProductionBuildID(releaseBuildID) || launchd == nil {
		return result, fmt.Errorf("supervision inspection lacks release or launchd identity")
	}
	names, err := RuntimeNames(canonicalHome)
	if err != nil {
		return result, err
	}
	result = SupervisionResultFor(canonicalHome, names)
	if err := validateSupervisionTree(result.Root); err != nil {
		return result, fmt.Errorf("prepared supervision is invalid: %w", err)
	}
	units, labels, sources, destinations := supervisionStartOrder(names, result.Root, launchAgentsDir)
	stopped, err := ReadServiceStops(canonicalHome)
	if err != nil {
		return result, err
	}
	for i, label := range labels {
		loaded, err := launchd.Loaded(label)
		if err != nil {
			return result, err
		}
		if loaded == stopped[units[i]] {
			return result, fmt.Errorf("LaunchAgent %s is not loaded", label)
		}
		if err := verifyInstalledLaunchAgent(sources[i], destinations[i]); err != nil {
			return result, err
		}
	}
	if stopped["resident"] {
		return result, nil
	}
	ok, err := matchingTickReceipt(filepath.Join(canonicalHome, "health", "resident-tick.json"), releaseBuildID, now.UTC().Add(-2*time.Minute), now.UTC().Add(5*time.Second))
	if err != nil {
		return result, err
	}
	if !ok {
		return result, fmt.Errorf("resident has no release-bound tick receipt in the last two minutes")
	}
	return result, nil
}

// ReconfigureSupervision applies a changed operator policy to units that are
// ALREADY LOADED. PrepareSupervision deliberately refuses to replace a loaded
// bundle offline — a running resident holding a config file that no longer
// describes it is exactly the drift the policy recheck exists to catch — so the
// only correct order is: boot each loaded unit out, rewrite the bundle from the
// new policy, then activate through the existing rollback and release-bound
// tick proof.
//
// It is operator-present BY CONSTRUCTION: it stops a running resident, and the
// window between bootout and a proven tick is real. Callers must therefore
// reach it only on an explicit operator instruction, never as the repair arm of
// a routine health check.
// waitForUnload polls until launchd stops reporting the label, bounded so a
// unit that refuses to die is a refusal rather than a hang. The resident stops
// "after any in-flight tick", so the bound has to exceed one tick.
func waitForUnload(label string, deps SupervisionActivationDeps) error {
	for attempt := 0; attempt < 90; attempt++ {
		loaded, err := deps.Launchd.Loaded(label)
		if err != nil {
			return fmt.Errorf("inspect %s while it unloads: %w", label, err)
		}
		if !loaded {
			return nil
		}
		deps.Sleep(time.Second)
	}
	return fmt.Errorf("%s was still loaded 90 seconds after bootout", label)
}

func ReconfigureSupervision(
	home, launchAgentsDir, releaseBuildID string,
	spec SupervisionSpec,
	deps SupervisionActivationDeps,
) (string, SupervisionResult, error) {
	var result SupervisionResult
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return "", result, err
	}
	if err := requireLaunchdTCCSafeHome(canonicalHome); err != nil {
		return "", result, err
	}
	if deps.Launchd == nil || deps.Now == nil || deps.Sleep == nil {
		return "", result, fmt.Errorf("reconfiguration requires launchd, clock, and wait dependencies")
	}
	names, err := RuntimeNames(canonicalHome)
	if err != nil {
		return "", result, err
	}
	result = SupervisionResultFor(canonicalHome, names)
	// Resident first: the other units only serve, while the resident is the one
	// thing that mutates the spine, so it is the one to stop first and start
	// last. ActivateSupervision bootstraps in the reverse order for the same
	// reason.
	for _, label := range result.Labels() {
		loaded, err := deps.Launchd.Loaded(label)
		if err != nil {
			return "", result, fmt.Errorf("inspect %s before reconfiguration: %w", label, err)
		}
		if !loaded {
			continue
		}
		if err := deps.Launchd.Bootout(label); err != nil {
			return "", result, fmt.Errorf("boot out %s for reconfiguration: %w", label, err)
		}
		// launchctl bootout returns once the request is ACCEPTED; the label
		// outlives it while the resident finishes its in-flight tick and
		// exits. Rewriting on the next statement therefore refuses with
		// "already loaded" whenever the resident is healthy enough to be
		// mid-tick — the one case reconfiguration exists for. A dead resident
		// unloads fast enough to slip through, which is what made this look
		// like it worked.
		if err := waitForUnload(label, deps); err != nil {
			return "", result, err
		}
	}
	if _, _, err := PrepareSupervision(canonicalHome, spec, deps.Launchd.Loaded); err != nil {
		return "", result, fmt.Errorf("rewrite supervision for the new policy: %w", err)
	}
	return ActivateSupervision(canonicalHome, launchAgentsDir, releaseBuildID, deps)
}

// supervisionStartOrder returns the units, labels, prepared plists, and
// installed plists in start order: the reverse of SupervisedUnits.
func supervisionStartOrder(names Names, root, launchAgentsDir string) (units, labels, sources, destinations []string) {
	for i := len(SupervisedUnits) - 1; i >= 0; i-- {
		unit := SupervisedUnits[i]
		label := UnitLabel(names, unit)
		units = append(units, unit)
		labels = append(labels, label)
		sources = append(sources, filepath.Join(root, "LaunchAgents", unit+".plist"))
		destinations = append(destinations, filepath.Join(launchAgentsDir, label+".plist"))
	}
	return units, labels, sources, destinations
}
