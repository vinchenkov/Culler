//go:build !test_fake_routing

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"mc/deployment"
	"mc/substrate"
	"mc/verbs"
)

func TestS7BrokerDecodesTheExactPrivateLockSnapshot(t *testing.T) {
	db, err := substrate.Open(filepath.Join(t.TempDir(), "spine.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := substrate.Init(db); err != nil {
		t.Fatal(err)
	}
	snapshot, err := verbs.AcceptanceRuntimeSnapshot(
		db, time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeS7RuntimeSnapshot(body)
	if err != nil {
		t.Fatalf("decode exact private output: %v\n%s", err, body)
	}
	if got.Version != 1 || !strings.HasPrefix(got.VMNow, "2026-07-25T12:00:00") {
		t.Fatalf("decoded snapshot = %+v", got)
	}
	wantSettings := deployment.S7SettingsSnapshot{
		TimeoutMinutes: 60, GraceMinutes: 15, HeartbeatIntervalS: 60,
		SpawnGraceS: 60, HardDeadlineMinutes: 240, ReviewWIPCap: 3,
		HomieIdleTimeoutS: 1800, WikiUpdateIntervalS: 3600,
	}
	if got.Settings != wantSettings {
		t.Fatalf("decoded settings = %+v, want %+v", got.Settings, wantSettings)
	}
	// A fresh spine has no Project yet, so it has no lease row either:
	// lease rows exist one per Project and never stand alone.
	if len(got.Leases) != 0 {
		t.Fatalf("fresh spine leases = %+v, want none", got.Leases)
	}
}

func TestS7ProductionOrchestrationRunsWithReadOnlyFakesAndNoLaunchdLoad(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	release := strings.Repeat("a", 40)
	preparedAt := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	baseline := deployment.S7Sample{
		HostBefore: preparedAt.Add(-time.Second), HostAfter: preparedAt,
		VMNow: preparedAt,
		Tick: deployment.S7TickSnapshot{
			Version: 1, ReleaseBuildID: release, ConfigSchemaVersion: 1,
			ResidentInstanceID: "resident", Sequence: 7,
			PreviousCompletedAt: preparedAt.Add(-70 * time.Second).Format(time.RFC3339Nano),
			CompletedAt:         preparedAt.Add(-10 * time.Second).Format(time.RFC3339Nano),
		},
		Leases: []deployment.S7LeaseSnapshot{{
			Project: "primary", Status: "held", RunID: "run", Owner: "worker", Subject: 9,
			AcquiredAt:      preparedAt.Add(-time.Minute).Format("2006-01-02 15:04:05"),
			LastHeartbeatAt: preparedAt.Add(-30 * time.Second).Format("2006-01-02 15:04:05"),
		}},
		Settings: deployment.S7SettingsSnapshot{
			TimeoutMinutes: 60, GraceMinutes: 15, HeartbeatIntervalS: 60,
			SpawnGraceS: 60, HardDeadlineMinutes: 240, ReviewWIPCap: 3,
			HomieIdleTimeoutS: 1800, WikiUpdateIntervalS: 3600,
		},
	}
	witness := deployment.S7SleepWitness{
		SleptAt: preparedAt.Add(30 * time.Second),
		WokeAt:  preparedAt.Add(30*time.Minute + 30*time.Second),
	}
	samples := make([]deployment.S7Sample, 3)
	for i := range samples {
		completed := witness.WokeAt.Add(10*time.Second + time.Duration(i)*time.Minute)
		samples[i] = baseline
		samples[i].HostBefore = completed.Add(-time.Second)
		samples[i].HostAfter = completed.Add(time.Second)
		samples[i].VMNow = completed
		samples[i].Tick.Sequence = int64(20 + i)
		samples[i].Tick.PreviousCompletedAt = completed.Add(-time.Minute).Format(time.RFC3339Nano)
		samples[i].Tick.CompletedAt = completed.Format(time.RFC3339Nano)
		samples[i].Leases = append([]deployment.S7LeaseSnapshot{}, baseline.Leases...)
		samples[i].Leases[0].LastHeartbeatAt = completed.Add(-10 * time.Second).Format("2006-01-02 15:04:05")
	}
	activeCalls, waitCalls := 0, 0
	deps := s7AcceptanceDeps{
		requireActive:    func(string) error { activeCalls++; return nil },
		resourceDisabled: func() (bool, error) { return true, nil },
		collectSample:    func(string) (deployment.S7Sample, error) { return baseline, nil },
		readSleepWitness: func() (deployment.S7SleepWitness, error) { return witness, nil },
		waitForTick: func(_ string, _ int64, _ time.Time, _ time.Time) (deployment.S7Sample, error) {
			sample := samples[waitCalls]
			waitCalls++
			return sample, nil
		},
		now: func() time.Time { return witness.WokeAt },
	}
	if got, err := executeS7Acceptance(home, release, "prepare", deps); err != nil || got.Status != "prepared" {
		t.Fatalf("prepare = %+v, %v", got, err)
	}
	if got, err := executeS7Acceptance(home, release, "verify", deps); err != nil ||
		got.Status != "verified" || got.ObservedTicks != 3 ||
		!reflect.DeepEqual(got.RunIDs, []string{"run"}) {
		t.Fatalf("verify = %+v, %v", got, err)
	}
	if activeCalls != 2 || waitCalls != 3 {
		t.Fatalf("orchestration calls active=%d waits=%d", activeCalls, waitCalls)
	}
	if got, err := executeS7Acceptance(home, release, "cleanup", deps); err != nil || got.Status != "cleaned" {
		t.Fatalf("cleanup = %+v, %v", got, err)
	}
}
