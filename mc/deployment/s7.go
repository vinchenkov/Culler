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

const (
	s7MinimumSleep    = 30 * time.Minute
	s7FreshnessWindow = 2 * time.Minute
	s7ClockTolerance  = 2 * time.Second
)

type S7TickSnapshot struct {
	Version             int    `json:"version"`
	ReleaseBuildID      string `json:"release_build_id"`
	ConfigSchemaVersion int    `json:"config_schema_version"`
	ResidentInstanceID  string `json:"resident_instance_id"`
	Sequence            int64  `json:"sequence"`
	PreviousCompletedAt string `json:"previous_completed_at,omitempty"`
	CompletedAt         string `json:"completed_at"`
}

// S7LeaseSnapshot is one row of `mc lease list`: one Project's lease. A
// free lease carries "" / 0 in every claim field and Status "free".
type S7LeaseSnapshot struct {
	Project         string `json:"project"`
	Status          string `json:"status"`
	RunID           string `json:"run_id"`
	Owner           string `json:"owner"`
	Subject         int64  `json:"subject"`
	AcquiredAt      string `json:"acquired_at"`
	LastHeartbeatAt string `json:"last_heartbeat_at"`
	Binding         string `json:"binding"`
	RunOutcome      string `json:"run_outcome"`
	RunEndedAt      string `json:"run_ended_at"`
}

// S7SettingsSnapshot is `mc settings get`: the deployment-wide settings row.
type S7SettingsSnapshot struct {
	TimeoutMinutes      int `json:"timeout_minutes"`
	GraceMinutes        int `json:"grace_minutes"`
	HeartbeatIntervalS  int `json:"heartbeat_interval_s"`
	SpawnGraceS         int `json:"spawn_grace_s"`
	HardDeadlineMinutes int `json:"hard_deadline_minutes"`
	ReviewWIPCap        int `json:"review_wip_cap"`
	CollieIdleTimeoutS  int `json:"collie_idle_timeout_s"`
	WikiUpdateIntervalS int `json:"wiki_update_interval_s"`
}

type S7Sample struct {
	HostBefore time.Time
	HostAfter  time.Time
	VMNow      time.Time
	Tick       S7TickSnapshot
	Leases     []S7LeaseSnapshot
	Settings   S7SettingsSnapshot
}

type S7SleepWitness struct {
	SleptAt time.Time `json:"slept_at"`
	WokeAt  time.Time `json:"woke_at"`
}

type S7DrillResult struct {
	Status string `json:"status"`
	// RunIDs are the run ids of the live leases the drill tracks across
	// sleep, one per leased Project, in Project id order.
	RunIDs             []string `json:"run_ids,omitempty"`
	ResidentInstanceID string   `json:"resident_instance_id,omitempty"`
	SleepSeconds       int64    `json:"sleep_seconds,omitempty"`
	ObservedTicks      int      `json:"observed_ticks,omitempty"`
}

type s7DrillState struct {
	Version                 int                `json:"version"`
	ReleaseBuildID          string             `json:"release_build_id"`
	ResourceSaverDisabled   bool               `json:"resource_saver_disabled"`
	ResourceSaverObservedAt string             `json:"resource_saver_observed_at"`
	PreparedAt              string             `json:"prepared_at"`
	BaselineTick            S7TickSnapshot     `json:"baseline_tick"`
	BaselineLeases          []S7LeaseSnapshot  `json:"baseline_leases"`
	BaselineSettings        S7SettingsSnapshot `json:"baseline_settings"`
	Status                  string             `json:"status"`
	VerifiedAt              string             `json:"verified_at,omitempty"`
	SleepSeconds            int64              `json:"sleep_seconds,omitempty"`
	ObservedTicks           int                `json:"observed_ticks,omitempty"`
	SleepWitness            S7SleepWitness     `json:"sleep_witness,omitempty"`
	PostWakeSamples         []S7Sample         `json:"post_wake_samples,omitempty"`
}

func s7StatePath(home string) string {
	return filepath.Join(home, "acceptance", "s7-sleep.json")
}

func s7ReceiptPath(home string) string {
	return filepath.Join(home, "acceptance", "s7-sleep-receipt.json")
}

func ReadS7TickSnapshot(home string) (S7TickSnapshot, error) {
	var tick S7TickSnapshot
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return tick, err
	}
	path := filepath.Join(canonicalHome, "health", "resident-tick.json")
	info, err := os.Lstat(path)
	if err != nil {
		return tick, err
	}
	uid, links, ok := statOwner(info)
	if !ok || int(uid) != os.Getuid() || info.Mode()&os.ModeSymlink != 0 ||
		!info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || links != 1 ||
		info.Size() <= 0 || info.Size() > 4096 {
		return tick, fmt.Errorf("resident tick receipt is not an owner-only regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return tick, err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&tick); err != nil {
		return tick, fmt.Errorf("decode resident tick receipt: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return tick, fmt.Errorf("resident tick receipt carries trailing JSON")
	}
	return tick, nil
}

func S7PreparedTickSequence(home string) (int64, error) {
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return 0, err
	}
	state, err := readS7State(canonicalHome)
	if err != nil {
		return 0, err
	}
	if state.Status != "prepared" && state.Status != "collecting" {
		return 0, fmt.Errorf("sleep drill is not awaiting verification")
	}
	if len(state.PostWakeSamples) > 0 {
		return state.PostWakeSamples[len(state.PostWakeSamples)-1].Tick.Sequence, nil
	}
	return state.BaselineTick.Sequence, nil
}

func S7CollectionCount(home string) (int, error) {
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return 0, err
	}
	state, err := readS7State(canonicalHome)
	if err != nil {
		return 0, err
	}
	if state.Status != "prepared" && state.Status != "collecting" {
		return 0, fmt.Errorf("sleep drill is not collecting post-wake evidence")
	}
	return len(state.PostWakeSamples), nil
}

func ReplayVerifiedS7SleepDrill(home, release string) (S7DrillResult, bool, error) {
	var result S7DrillResult
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return result, false, err
	}
	state, err := readS7StateAt(s7StatePath(canonicalHome))
	if errors.Is(err, fs.ErrNotExist) {
		state, err = readS7StateAt(s7ReceiptPath(canonicalHome))
	}
	if err != nil {
		return result, false, err
	}
	if state.ReleaseBuildID != release {
		return result, false, fmt.Errorf("sleep drill state is release-mismatched")
	}
	if state.Status != "verified" {
		return result, false, nil
	}
	if state.VerifiedAt == "" || state.SleepSeconds < int64(s7MinimumSleep.Seconds()) ||
		state.ObservedTicks != 3 {
		return result, false, fmt.Errorf("verified sleep drill state is incomplete")
	}
	return S7DrillResult{
		Status: "verified", RunIDs: heldS7RunIDs(state.BaselineLeases),
		ResidentInstanceID: state.BaselineTick.ResidentInstanceID,
		SleepSeconds:       state.SleepSeconds, ObservedTicks: state.ObservedTicks,
	}, true, nil
}

func ReplayPreparedS7SleepDrill(home, release string) (S7DrillResult, bool, error) {
	var result S7DrillResult
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return result, false, err
	}
	state, err := readS7State(canonicalHome)
	if errors.Is(err, fs.ErrNotExist) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	if state.ReleaseBuildID != release {
		return result, false, fmt.Errorf("sleep drill state is release-mismatched")
	}
	if state.Status != "prepared" && state.Status != "collecting" {
		return result, false, nil
	}
	return S7DrillResult{
		Status: "prepared", RunIDs: heldS7RunIDs(state.BaselineLeases),
		ResidentInstanceID: state.BaselineTick.ResidentInstanceID,
	}, true, nil
}

func parseS7Time(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid timestamp %q", value)
}

func S7TickCompletedAt(tick S7TickSnapshot) (time.Time, error) {
	return parseS7Time(tick.CompletedAt)
}

func ParseS7SleepWitness(log string) (S7SleepWitness, error) {
	var witness S7SleepWitness
	var pendingSleep time.Time
	for _, line := range strings.Split(log, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		when, err := time.Parse("2006-01-02 15:04:05 -0700",
			strings.Join(fields[:3], " "))
		if err != nil {
			continue
		}
		switch fields[3] {
		case "Sleep":
			pendingSleep = when
		case "Wake":
			if !pendingSleep.IsZero() && when.After(pendingSleep) {
				witness = S7SleepWitness{SleptAt: pendingSleep, WokeAt: when}
				pendingSleep = time.Time{}
			}
		}
	}
	if witness.SleptAt.IsZero() || witness.WokeAt.IsZero() {
		return S7SleepWitness{}, fmt.Errorf("macOS power log has no complete Sleep/Wake pair")
	}
	return witness, nil
}

func validateS7Clock(sample S7Sample) error {
	if sample.HostBefore.IsZero() || sample.HostAfter.Before(sample.HostBefore) ||
		sample.VMNow.IsZero() {
		return fmt.Errorf("sleep drill clock sample is malformed")
	}
	lower := sample.VMNow.Sub(sample.HostAfter)
	upper := sample.VMNow.Sub(sample.HostBefore)
	if lower < -s7ClockTolerance || upper > s7ClockTolerance {
		return fmt.Errorf("Docker VM clock skew bound [%s,%s] exceeds +/- %s",
			lower, upper, s7ClockTolerance)
	}
	return nil
}

func validateS7Tick(sample S7Sample, release string, requirePrevious bool) (time.Time, error) {
	tick := sample.Tick
	if tick.Version != 1 || tick.ReleaseBuildID != release ||
		tick.ConfigSchemaVersion != 1 || tick.ResidentInstanceID == "" ||
		tick.Sequence <= 0 {
		return time.Time{}, fmt.Errorf("resident tick identity is malformed or release-mismatched")
	}
	completed, err := parseS7Time(tick.CompletedAt)
	if err != nil {
		return time.Time{}, err
	}
	if completed.After(sample.HostAfter.Add(5*time.Second)) ||
		sample.HostAfter.Sub(completed) > s7FreshnessWindow {
		return time.Time{}, fmt.Errorf("resident tick is not fresh at the host sample")
	}
	if requirePrevious {
		previous, err := parseS7Time(tick.PreviousCompletedAt)
		if err != nil {
			return time.Time{}, fmt.Errorf("resident tick lacks prior cadence evidence: %w", err)
		}
		delta := completed.Sub(previous)
		if delta < 30*time.Second || delta > 2*time.Minute {
			return time.Time{}, fmt.Errorf("resident tick cadence %s is outside the production window", delta)
		}
	}
	return completed, nil
}

// heldS7Leases is the sample's live leases — one per leased Project — in
// the order `mc lease list` reports them (Project id).
func heldS7Leases(leases []S7LeaseSnapshot) []S7LeaseSnapshot {
	held := []S7LeaseSnapshot{}
	for _, lease := range leases {
		if lease.RunID != "" {
			held = append(held, lease)
		}
	}
	return held
}

func heldS7RunIDs(leases []S7LeaseSnapshot) []string {
	ids := []string{}
	for _, lease := range heldS7Leases(leases) {
		ids = append(ids, lease.RunID)
	}
	return ids
}

// validateS7Leases checks every live lease of the sample against the settings
// of the same sample and returns each one's heartbeat, keyed by Project.
// The drill requires at least one genuine subject-carrying live lease. The
// lease limits count VM uptime, so a sleep of any length uses none of them.
func validateS7Leases(sample S7Sample) (map[string]time.Time, error) {
	settings := sample.Settings
	held := heldS7Leases(sample.Leases)
	if len(held) == 0 || settings.TimeoutMinutes <= 0 ||
		settings.GraceMinutes < 0 || settings.HeartbeatIntervalS <= 0 ||
		settings.SpawnGraceS <= 0 || settings.HardDeadlineMinutes <= 0 {
		return nil, fmt.Errorf("sleep drill requires a genuine subject-carrying live lease")
	}
	heartbeats := map[string]time.Time{}
	for _, lease := range held {
		if lease.Project == "" || lease.Subject <= 0 || lease.Owner == "" ||
			lease.AcquiredAt == "" || lease.LastHeartbeatAt == "" || lease.RunOutcome != "" {
			return nil, fmt.Errorf("sleep drill requires a genuine subject-carrying live lease")
		}
		heartbeat, err := parseS7Time(lease.LastHeartbeatAt)
		if err != nil {
			return nil, err
		}
		if heartbeat.After(sample.VMNow.Add(5 * time.Second)) {
			return nil, fmt.Errorf("live lease heartbeat is in the future")
		}
		heartbeats[lease.Project] = heartbeat
	}
	return heartbeats, nil
}

// s7Heartbeats parses the heartbeat of every live lease, keyed by Project.
func s7Heartbeats(leases []S7LeaseSnapshot) (map[string]time.Time, error) {
	heartbeats := map[string]time.Time{}
	for _, lease := range heldS7Leases(leases) {
		heartbeat, err := parseS7Time(lease.LastHeartbeatAt)
		if err != nil {
			return nil, err
		}
		heartbeats[lease.Project] = heartbeat
	}
	return heartbeats, nil
}

// s7HeartbeatsNotBefore reports whether every tracked lease's heartbeat in
// `later` is at or after its heartbeat in `earlier`; a lease absent from
// either side is a lease-identity change, caught by sameS7Leases.
func s7HeartbeatsNotBefore(later, earlier map[string]time.Time) bool {
	for project, before := range earlier {
		after, ok := later[project]
		if !ok || after.Before(before) {
			return false
		}
	}
	return true
}

func writeS7State(home string, state s7DrillState) error {
	root := filepath.Join(home, "acceptance")
	if err := ensureOwnerDirectory(root, "acceptance state root"); err != nil {
		return err
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	tmp, err := os.CreateTemp(root, ".s7-sleep-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s7StatePath(home)); err != nil {
		return err
	}
	return syncDirectory(root)
}

func readS7StateAt(path string) (s7DrillState, error) {
	var state s7DrillState
	info, err := os.Lstat(path)
	if err != nil {
		return state, err
	}
	uid, links, ok := statOwner(info)
	if !ok || int(uid) != os.Getuid() || info.Mode()&os.ModeSymlink != 0 ||
		!info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || links != 1 ||
		info.Size() <= 0 || info.Size() > 64*1024 {
		return state, fmt.Errorf("sleep drill state is not an owner-only regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return state, err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return state, err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return state, fmt.Errorf("sleep drill state carries trailing JSON")
	}
	if state.Version != 1 || !validProductionBuildID(state.ReleaseBuildID) ||
		!state.ResourceSaverDisabled || state.ResourceSaverObservedAt == "" ||
		state.Status == "" {
		return state, fmt.Errorf("sleep drill state identity is malformed")
	}
	return state, nil
}

func DockerDesktopResourceSaverDisabled(body []byte) (bool, error) {
	var settings struct {
		UseResourceSaver *bool `json:"UseResourceSaver"`
	}
	if err := json.Unmarshal(body, &settings); err != nil {
		return false, fmt.Errorf("decode Docker Desktop settings: %w", err)
	}
	if settings.UseResourceSaver == nil {
		return false, fmt.Errorf("Docker Desktop settings omit UseResourceSaver")
	}
	return !*settings.UseResourceSaver, nil
}

func readS7State(home string) (s7DrillState, error) {
	return readS7StateAt(s7StatePath(home))
}

func PrepareS7SleepDrill(home, release string, resourceSaverDisabled bool, sample S7Sample) (S7DrillResult, error) {
	var result S7DrillResult
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return result, err
	}
	if !validProductionBuildID(release) {
		return result, fmt.Errorf("sleep drill prepare requires the immutable production release identity")
	}
	if !resourceSaverDisabled {
		return result, fmt.Errorf("sleep drill prepare requires Docker Desktop Resource Saver disabled; production ticks prevent a valid idle pause proof")
	}
	if _, err := os.Lstat(s7StatePath(canonicalHome)); err == nil {
		state, readErr := readS7State(canonicalHome)
		if readErr != nil {
			return result, readErr
		}
		if state.ReleaseBuildID == release &&
			(state.Status == "prepared" || state.Status == "collecting") {
			return S7DrillResult{
				Status: "prepared", RunIDs: heldS7RunIDs(state.BaselineLeases),
				ResidentInstanceID: state.BaselineTick.ResidentInstanceID,
			}, nil
		}
		return result, fmt.Errorf("a sleep drill is already recorded; verify or clean it before preparing another")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return result, err
	}
	if err := validateS7Clock(sample); err != nil {
		return result, err
	}
	if _, err := validateS7Tick(sample, release, true); err != nil {
		return result, err
	}
	if _, err := validateS7Leases(sample); err != nil {
		return result, err
	}
	state := s7DrillState{
		Version: 1, ReleaseBuildID: release, ResourceSaverDisabled: true,
		ResourceSaverObservedAt: sample.HostAfter.UTC().Format(time.RFC3339Nano),
		PreparedAt:              sample.HostAfter.UTC().Format(time.RFC3339Nano),
		BaselineTick:            sample.Tick, BaselineLeases: sample.Leases,
		BaselineSettings: sample.Settings, Status: "prepared",
	}
	if err := writeS7State(canonicalHome, state); err != nil {
		return result, err
	}
	return S7DrillResult{
		Status: "prepared", RunIDs: heldS7RunIDs(sample.Leases),
		ResidentInstanceID: sample.Tick.ResidentInstanceID,
	}, nil
}

// sameS7Leases reports whether two samples track the same live leases: the
// same leased Projects, each with the same run, owner, subject, and
// acquisition. Only the heartbeat may move.
func sameS7Leases(left, right []S7LeaseSnapshot) bool {
	l, r := heldS7Leases(left), heldS7Leases(right)
	if len(l) != len(r) {
		return false
	}
	for i := range l {
		a, b := l[i], r[i]
		a.LastHeartbeatAt, b.LastHeartbeatAt = "", ""
		if a != b {
			return false
		}
	}
	return true
}

func RecordS7PostWakeSample(home, release string, witness S7SleepWitness, sample S7Sample) (int, error) {
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return 0, err
	}
	state, err := readS7State(canonicalHome)
	if err != nil {
		return 0, err
	}
	if (state.Status != "prepared" && state.Status != "collecting") ||
		state.ReleaseBuildID != release {
		return 0, fmt.Errorf("prepared sleep drill state is absent or release-mismatched")
	}
	preparedAt, err := parseS7Time(state.PreparedAt)
	if err != nil {
		return 0, err
	}
	if witness.SleptAt.Before(preparedAt.Add(-5*time.Second)) ||
		witness.SleptAt.After(preparedAt.Add(90*time.Second)) ||
		witness.WokeAt.Sub(witness.SleptAt) < s7MinimumSleep {
		return 0, fmt.Errorf("macOS power log does not prove a prompt 30-minute Sleep/Wake pair")
	}
	if !state.SleepWitness.SleptAt.IsZero() &&
		(!state.SleepWitness.SleptAt.Equal(witness.SleptAt) ||
			!state.SleepWitness.WokeAt.Equal(witness.WokeAt)) {
		return 0, fmt.Errorf("macOS Sleep/Wake witness changed during collection")
	}
	if len(state.PostWakeSamples) == 0 &&
		(sample.HostBefore.Before(witness.WokeAt) ||
			sample.HostBefore.Sub(witness.WokeAt) > 90*time.Second) {
		return 0, fmt.Errorf("first post-wake sample was not collected promptly")
	}
	if err := validateS7Clock(sample); err != nil {
		return 0, err
	}
	completed, err := validateS7Tick(sample, release, len(state.PostWakeSamples) > 0)
	if err != nil {
		return 0, err
	}
	if completed.Before(witness.WokeAt.Add(-5 * time.Second)) {
		return 0, fmt.Errorf("resident tick predates the proved macOS wake")
	}
	if len(state.PostWakeSamples) == 0 && completed.After(witness.WokeAt.Add(20*time.Second)) {
		previous, previousErr := parseS7Time(sample.Tick.PreviousCompletedAt)
		if previousErr != nil || previous.Before(witness.WokeAt.Add(-5*time.Second)) ||
			previous.After(witness.WokeAt.Add(20*time.Second)) {
			return 0, fmt.Errorf("resident produced no tick within 20 seconds of wake")
		}
	}
	if sample.Tick.ResidentInstanceID != state.BaselineTick.ResidentInstanceID ||
		sample.Tick.Sequence <= state.BaselineTick.Sequence {
		return 0, fmt.Errorf("resident process identity or tick sequence did not survive sleep")
	}
	if !sameS7Leases(sample.Leases, state.BaselineLeases) || sample.Settings != state.BaselineSettings {
		return 0, fmt.Errorf("live lease identity or immutable tunables changed across sleep")
	}
	heartbeats, err := validateS7Leases(sample)
	if err != nil {
		return 0, err
	}
	baselineHeartbeats, err := s7Heartbeats(state.BaselineLeases)
	if err != nil {
		return 0, err
	}
	if !s7HeartbeatsNotBefore(heartbeats, baselineHeartbeats) {
		return 0, fmt.Errorf("live lease heartbeat moved backwards across sleep")
	}
	if n := len(state.PostWakeSamples); n > 0 {
		previous := state.PostWakeSamples[n-1]
		if sample.Tick.Sequence == previous.Tick.Sequence {
			return n, nil
		}
		if sample.Tick.Sequence != previous.Tick.Sequence+1 {
			return 0, fmt.Errorf("post-wake tick sequence is not consecutive")
		}
		previousCompleted, err := parseS7Time(previous.Tick.CompletedAt)
		if err != nil {
			return 0, err
		}
		delta := completed.Sub(previousCompleted)
		if delta < 30*time.Second || delta > 2*time.Minute {
			return 0, fmt.Errorf("post-wake cadence %s is outside the production window", delta)
		}
		previousHeartbeats, err := s7Heartbeats(previous.Leases)
		if err != nil {
			return 0, err
		}
		if !s7HeartbeatsNotBefore(heartbeats, previousHeartbeats) {
			return 0, fmt.Errorf("live lease heartbeat moved backwards")
		}
	}
	if len(state.PostWakeSamples) >= 3 {
		return 0, fmt.Errorf("sleep drill already has its three post-wake samples")
	}
	state.Status = "collecting"
	state.SleepWitness = witness
	state.PostWakeSamples = append(state.PostWakeSamples, sample)
	if err := writeS7State(canonicalHome, state); err != nil {
		return 0, err
	}
	return len(state.PostWakeSamples), nil
}

func FinalizeS7SleepDrill(home, release string) (S7DrillResult, error) {
	var result S7DrillResult
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return result, err
	}
	state, err := readS7State(canonicalHome)
	if err != nil {
		return result, err
	}
	if state.Status != "collecting" || state.ReleaseBuildID != release ||
		len(state.PostWakeSamples) != 3 {
		return result, fmt.Errorf("sleep drill requires three durable post-wake samples before finalization")
	}
	baselineHeartbeats, err := s7Heartbeats(state.BaselineLeases)
	if err != nil {
		return result, err
	}
	finalHeartbeats, err := s7Heartbeats(state.PostWakeSamples[2].Leases)
	if err != nil {
		return result, err
	}
	for project, baseline := range baselineHeartbeats {
		if final, ok := finalHeartbeats[project]; !ok || !final.After(baseline) {
			return result, fmt.Errorf("live lease heartbeat did not resume by the final post-wake sample")
		}
	}
	state.Status = "verified"
	state.VerifiedAt = state.PostWakeSamples[2].HostAfter.UTC().Format(time.RFC3339Nano)
	state.SleepSeconds = int64(state.SleepWitness.WokeAt.Sub(state.SleepWitness.SleptAt).Seconds())
	state.ObservedTicks = len(state.PostWakeSamples)
	if err := writeS7State(canonicalHome, state); err != nil {
		return result, err
	}
	return S7DrillResult{
		Status: "verified", RunIDs: heldS7RunIDs(state.BaselineLeases),
		ResidentInstanceID: state.BaselineTick.ResidentInstanceID,
		SleepSeconds:       state.SleepSeconds,
		ObservedTicks:      state.ObservedTicks,
	}, nil
}

func VerifyS7SleepDrill(home, release string, witness S7SleepWitness, samples []S7Sample) (S7DrillResult, error) {
	if len(samples) != 3 {
		return S7DrillResult{}, fmt.Errorf("sleep drill verify requires exactly three consecutive post-wake samples")
	}
	for _, sample := range samples {
		if _, err := RecordS7PostWakeSample(home, release, witness, sample); err != nil {
			return S7DrillResult{}, err
		}
	}
	return FinalizeS7SleepDrill(home, release)
}

func CleanupS7SleepDrill(home string) (S7DrillResult, error) {
	var result S7DrillResult
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return result, err
	}
	state, err := readS7State(canonicalHome)
	if errors.Is(err, fs.ErrNotExist) {
		return S7DrillResult{Status: "cleaned"}, nil
	} else if err != nil {
		return result, err
	}
	if state.Status == "verified" {
		receipt := s7ReceiptPath(canonicalHome)
		if _, err := os.Lstat(receipt); errors.Is(err, fs.ErrNotExist) {
			if err := os.Rename(s7StatePath(canonicalHome), receipt); err != nil {
				return result, err
			}
		} else if err != nil {
			return result, err
		} else {
			current, readErr := os.ReadFile(s7StatePath(canonicalHome))
			saved, receiptErr := os.ReadFile(receipt)
			if readErr != nil || receiptErr != nil || !bytes.Equal(current, saved) {
				return result, fmt.Errorf("retained sleep drill receipt differs from verified state")
			}
			if err := os.Remove(s7StatePath(canonicalHome)); err != nil {
				return result, err
			}
		}
	} else if err := os.Remove(s7StatePath(canonicalHome)); err != nil {
		return result, err
	}
	if err := syncDirectory(filepath.Join(canonicalHome, "acceptance")); err != nil {
		return result, err
	}
	return S7DrillResult{Status: "cleaned"}, nil
}
