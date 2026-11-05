package deployment

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func s7Fixture(t *testing.T) (string, string, time.Time, S7Sample) {
	t.Helper()
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	release := strings.Repeat("a", 40)
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	sample := S7Sample{
		HostBefore: now.Add(-time.Second),
		HostAfter:  now.Add(time.Second),
		VMNow:      now,
		Tick: S7TickSnapshot{
			Version: 1, ReleaseBuildID: release, ConfigSchemaVersion: 1,
			ResidentInstanceID: "resident-instance", Sequence: 7,
			PreviousCompletedAt: now.Add(-70 * time.Second).Format(time.RFC3339Nano),
			CompletedAt:         now.Add(-10 * time.Second).Format(time.RFC3339Nano),
		},
		Leases: []S7LeaseSnapshot{{
			Project: "primary", Status: "held", RunID: "run-live", Owner: "worker", Subject: 37,
			AcquiredAt:      now.Add(-5 * time.Minute).Format("2006-01-02 15:04:05"),
			LastHeartbeatAt: now.Add(-30 * time.Second).Format("2006-01-02 15:04:05"),
		}},
		Settings: S7SettingsSnapshot{
			TimeoutMinutes: 60, GraceMinutes: 15, HeartbeatIntervalS: 60,
			SpawnGraceS: 60, HardDeadlineMinutes: 240, ReviewWIPCap: 3,
			HomieIdleTimeoutS: 1800, WikiUpdateIntervalS: 3600,
		},
	}
	return home, release, now, sample
}

// s7SecondLease is a second Project's held lease, so a fixture can prove
// the drill tracks every held lease and not only the first.
func s7SecondLease(now time.Time) S7LeaseSnapshot {
	return S7LeaseSnapshot{
		Project: "secondary", Status: "held", RunID: "run-second", Owner: "worker", Subject: 41,
		AcquiredAt:      now.Add(-3 * time.Minute).Format("2006-01-02 15:04:05"),
		LastHeartbeatAt: now.Add(-20 * time.Second).Format("2006-01-02 15:04:05"),
	}
}

func TestPrepareS7SleepDrillRequiresFreshCadenceClockAndLiveLease(t *testing.T) {
	home, release, now, sample := s7Fixture(t)
	result, err := PrepareS7SleepDrill(home, release, true, sample)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if result.Status != "prepared" || !reflect.DeepEqual(result.RunIDs, []string{"run-live"}) ||
		result.ResidentInstanceID != "resident-instance" {
		t.Fatalf("result = %+v", result)
	}
	replayed, err := PrepareS7SleepDrill(home, release, true, sample)
	if err != nil || !reflect.DeepEqual(replayed, result) {
		t.Fatalf("prepare replay = %+v, %v", replayed, err)
	}
	info, err := os.Stat(filepath.Join(home, "acceptance", "s7-sleep.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode: info=%v err=%v", info, err)
	}

	for name, mutate := range map[string]func(*S7Sample){
		"no live lease": func(s *S7Sample) {
			s.Leases = []S7LeaseSnapshot{{Project: "primary", Status: "free"}}
		},
		"heartbeat in the future": func(s *S7Sample) {
			s.Leases[0].LastHeartbeatAt = now.Add(time.Minute).Format("2006-01-02 15:04:05")
		},
		"ended run": func(s *S7Sample) {
			s.Leases[0].RunOutcome = "infra-failed"
		},
		"second lease without a heartbeat": func(s *S7Sample) {
			second := s7SecondLease(now)
			second.LastHeartbeatAt = ""
			s.Leases = []S7LeaseSnapshot{s.Leases[0], second}
		},
		"no cadence evidence": func(s *S7Sample) { s.Tick.PreviousCompletedAt = "" },
		"clock skew":          func(s *S7Sample) { s.VMNow = now.Add(4 * time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			other := t.TempDir()
			if err := os.Chmod(other, 0o700); err != nil {
				t.Fatal(err)
			}
			bad := sample
			bad.Leases = append([]S7LeaseSnapshot{}, sample.Leases...)
			mutate(&bad)
			if _, err := PrepareS7SleepDrill(other, release, true, bad); err == nil {
				t.Fatal("unsafe preparation accepted")
			}
		})
	}
	other := t.TempDir()
	if _, err := PrepareS7SleepDrill(other, release, false, sample); err == nil ||
		!strings.Contains(err.Error(), "Resource Saver") {
		t.Fatalf("Resource Saver refusal = %v", err)
	}
}

func s7VerifyFixture(t *testing.T) (string, string, S7SleepWitness, []S7Sample) {
	t.Helper()
	home, release, preparedAt, baseline := s7Fixture(t)
	if _, err := PrepareS7SleepDrill(home, release, true, baseline); err != nil {
		t.Fatal(err)
	}
	verifyAt := preparedAt.Add(31 * time.Minute)
	samples := make([]S7Sample, 3)
	for i := range samples {
		completed := verifyAt.Add(time.Duration(i)*time.Minute - 10*time.Second)
		samples[i] = baseline
		samples[i].HostBefore = completed.Add(-time.Second)
		samples[i].HostAfter = completed.Add(time.Second)
		samples[i].VMNow = completed
		samples[i].Tick.Sequence = int64(40 + i)
		samples[i].Tick.PreviousCompletedAt = completed.Add(-time.Minute).Format(time.RFC3339Nano)
		samples[i].Tick.CompletedAt = completed.Format(time.RFC3339Nano)
		samples[i].Leases = append([]S7LeaseSnapshot{}, baseline.Leases...)
		samples[i].Leases[0].LastHeartbeatAt = completed.Add(-20 * time.Second).Format("2006-01-02 15:04:05")
	}
	witness := S7SleepWitness{
		SleptAt: preparedAt.Add(30 * time.Second),
		WokeAt:  verifyAt.Add(-20 * time.Second),
	}
	return home, release, witness, samples
}

func TestVerifyS7SleepDrillProvesSleepWakeCadenceClockAndSameLease(t *testing.T) {
	home, release, witness, samples := s7VerifyFixture(t)
	result, err := VerifyS7SleepDrill(home, release, witness, samples)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.Status != "verified" || result.SleepSeconds < 30*60 ||
		result.ObservedTicks != 3 {
		t.Fatalf("result = %+v", result)
	}
	if !reflect.DeepEqual(result.RunIDs, []string{"run-live"}) {
		t.Fatalf("result run ids = %v", result.RunIDs)
	}
	replayed, ok, err := ReplayVerifiedS7SleepDrill(home, release)
	if err != nil || !ok || !reflect.DeepEqual(replayed, result) {
		t.Fatalf("verify replay = %+v ok=%v err=%v", replayed, ok, err)
	}
	if _, err := CleanupS7SleepDrill(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "acceptance", "s7-sleep-receipt.json")); err != nil {
		t.Fatalf("verified sleep drill receipt was not retained: %v", err)
	}

	t.Run("late first sample", func(t *testing.T) {
		home, release, witness, late := s7VerifyFixture(t)
		late[0].HostBefore = witness.WokeAt.Add(91 * time.Second)
		late[0].HostAfter = late[0].HostBefore.Add(time.Second)
		late[0].VMNow = late[0].HostBefore
		late[0].Tick.CompletedAt = late[0].HostBefore.Format(time.RFC3339Nano)
		if _, err := VerifyS7SleepDrill(home, release, witness, late); err == nil {
			t.Fatal("late first post-wake sample accepted")
		}
	})
	t.Run("replacement lease", func(t *testing.T) {
		home, release, witness, replaced := s7VerifyFixture(t)
		replaced[1].Leases[0].RunID = "replacement"
		if _, err := VerifyS7SleepDrill(home, release, witness, replaced); err == nil {
			t.Fatal("replacement lease accepted")
		}
	})
	t.Run("settings changed across sleep", func(t *testing.T) {
		home, release, witness, changed := s7VerifyFixture(t)
		changed[1].Settings.ReviewWIPCap = 4
		if _, err := VerifyS7SleepDrill(home, release, witness, changed); err == nil ||
			!strings.Contains(err.Error(), "changed across sleep") {
			t.Fatalf("changed settings = %v", err)
		}
	})
	t.Run("missed cadence", func(t *testing.T) {
		home, release, witness, missed := s7VerifyFixture(t)
		missed[2].Tick.Sequence++
		if _, err := VerifyS7SleepDrill(home, release, witness, missed); err == nil {
			t.Fatal("missed tick accepted")
		}
	})
	t.Run("no real sleep witness", func(t *testing.T) {
		home, release, witness, samples := s7VerifyFixture(t)
		tooShort := witness
		tooShort.WokeAt = tooShort.SleptAt.Add(29 * time.Minute)
		if _, err := VerifyS7SleepDrill(home, release, tooShort, samples); err == nil {
			t.Fatal("short system sleep accepted")
		}
	})
	t.Run("first tick is not immediate", func(t *testing.T) {
		home, release, witness, late := s7VerifyFixture(t)
		completed := witness.WokeAt.Add(21 * time.Second)
		late[0].HostBefore = completed
		late[0].HostAfter = completed.Add(time.Second)
		late[0].VMNow = completed
		late[0].Tick.CompletedAt = completed.Format(time.RFC3339Nano)
		late[0].Tick.PreviousCompletedAt = witness.SleptAt.Format(time.RFC3339Nano)
		if _, err := VerifyS7SleepDrill(home, release, witness, late); err == nil {
			t.Fatal("late first resident tick accepted")
		}
	})
	t.Run("later observed tick carries immediate predecessor", func(t *testing.T) {
		home, release, witness, samples := s7VerifyFixture(t)
		for i := range samples {
			completed := witness.WokeAt.Add(65*time.Second + time.Duration(i)*time.Minute)
			samples[i].HostBefore = completed
			samples[i].HostAfter = completed.Add(time.Second)
			samples[i].VMNow = completed
			samples[i].Tick.PreviousCompletedAt = completed.Add(-time.Minute).Format(time.RFC3339Nano)
			samples[i].Tick.CompletedAt = completed.Format(time.RFC3339Nano)
			samples[i].Leases[0].LastHeartbeatAt = completed.Add(-10 * time.Second).Format("2006-01-02 15:04:05")
		}
		if _, err := VerifyS7SleepDrill(home, release, witness, samples); err != nil {
			t.Fatalf("immediate predecessor evidence refused: %v", err)
		}
	})
	t.Run("heartbeat may resume after the first wake tick", func(t *testing.T) {
		home, release, witness, samples := s7VerifyFixture(t)
		_, _, _, baseline := s7Fixture(t)
		samples[0].Leases[0].LastHeartbeatAt = baseline.Leases[0].LastHeartbeatAt
		if _, err := VerifyS7SleepDrill(home, release, witness, samples); err != nil {
			t.Fatalf("later heartbeat resumption refused: %v", err)
		}
	})
}

// s7TwoLeaseVerifyFixture prepares the drill with two Projects' held
// leases and returns three post-wake samples in which both heartbeats moved.
func s7TwoLeaseVerifyFixture(t *testing.T) (string, string, S7SleepWitness, []S7Sample) {
	t.Helper()
	home, release, preparedAt, baseline := s7Fixture(t)
	baseline.Leases = append(baseline.Leases, s7SecondLease(preparedAt))
	if _, err := PrepareS7SleepDrill(home, release, true, baseline); err != nil {
		t.Fatal(err)
	}
	verifyAt := preparedAt.Add(31 * time.Minute)
	samples := make([]S7Sample, 3)
	for i := range samples {
		completed := verifyAt.Add(time.Duration(i)*time.Minute - 10*time.Second)
		samples[i] = baseline
		samples[i].HostBefore = completed.Add(-time.Second)
		samples[i].HostAfter = completed.Add(time.Second)
		samples[i].VMNow = completed
		samples[i].Tick.Sequence = int64(40 + i)
		samples[i].Tick.PreviousCompletedAt = completed.Add(-time.Minute).Format(time.RFC3339Nano)
		samples[i].Tick.CompletedAt = completed.Format(time.RFC3339Nano)
		samples[i].Leases = append([]S7LeaseSnapshot{}, baseline.Leases...)
		samples[i].Leases[0].LastHeartbeatAt = completed.Add(-20 * time.Second).Format("2006-01-02 15:04:05")
		samples[i].Leases[1].LastHeartbeatAt = completed.Add(-15 * time.Second).Format("2006-01-02 15:04:05")
	}
	witness := S7SleepWitness{
		SleptAt: preparedAt.Add(30 * time.Second),
		WokeAt:  verifyAt.Add(-20 * time.Second),
	}
	return home, release, witness, samples
}

func TestS7SleepDrillTracksEveryHeldLease(t *testing.T) {
	home, release, witness, samples := s7TwoLeaseVerifyFixture(t)
	result, err := VerifyS7SleepDrill(home, release, witness, samples)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.Status != "verified" || result.ObservedTicks != 3 ||
		!reflect.DeepEqual(result.RunIDs, []string{"run-live", "run-second"}) {
		t.Fatalf("result = %+v", result)
	}
	replayed, ok, err := ReplayVerifiedS7SleepDrill(home, release)
	if err != nil || !ok || !reflect.DeepEqual(replayed, result) {
		t.Fatalf("verify replay = %+v ok=%v err=%v", replayed, ok, err)
	}

	t.Run("second lease replaced", func(t *testing.T) {
		home, release, witness, replaced := s7TwoLeaseVerifyFixture(t)
		replaced[1].Leases[1].RunID = "replacement"
		if _, err := VerifyS7SleepDrill(home, release, witness, replaced); err == nil ||
			!strings.Contains(err.Error(), "live lease identity or immutable tunables changed across sleep") {
			t.Fatalf("replacement of the second lease = %v", err)
		}
	})
	t.Run("second lease released", func(t *testing.T) {
		home, release, witness, released := s7TwoLeaseVerifyFixture(t)
		released[2].Leases[1] = S7LeaseSnapshot{Project: "secondary", Status: "free"}
		if _, err := VerifyS7SleepDrill(home, release, witness, released); err == nil ||
			!strings.Contains(err.Error(), "changed across sleep") {
			t.Fatalf("release of the second lease = %v", err)
		}
	})
	t.Run("second lease heartbeat moved backwards", func(t *testing.T) {
		home, release, witness, moved := s7TwoLeaseVerifyFixture(t)
		moved[2].Leases[1].LastHeartbeatAt = moved[0].Leases[1].LastHeartbeatAt
		if _, err := VerifyS7SleepDrill(home, release, witness, moved); err == nil ||
			!strings.Contains(err.Error(), "heartbeat moved backwards") {
			t.Fatalf("second lease heartbeat regression = %v", err)
		}
	})
	t.Run("second lease heartbeat never resumes", func(t *testing.T) {
		home, release, witness, stuck := s7TwoLeaseVerifyFixture(t)
		_, _, preparedAt, _ := s7Fixture(t)
		for i := range stuck {
			stuck[i].Leases[1].LastHeartbeatAt = s7SecondLease(preparedAt).LastHeartbeatAt
		}
		if _, err := VerifyS7SleepDrill(home, release, witness, stuck); err == nil ||
			!strings.Contains(err.Error(), "did not resume") {
			t.Fatalf("stuck second heartbeat = %v", err)
		}
	})
}

func TestParseS7SleepWitnessUsesLatestCompleteSystemPair(t *testing.T) {
	log := `
2026-07-25 11:00:00 -0700 Sleep Entering Sleep state due to 'Clamshell Sleep'
2026-07-25 11:05:00 -0700 DarkWake DarkWake from Deep Idle
2026-07-25 11:31:00 -0700 Wake Wake from Deep Idle
2026-07-25 12:00:00 -0700 Sleep Entering Sleep state due to 'Software Sleep'
2026-07-25 12:35:00 -0700 Wake Wake from Deep Idle
`
	got, err := ParseS7SleepWitness(log)
	if err != nil {
		t.Fatal(err)
	}
	if got.SleptAt.Format(time.RFC3339) != "2026-07-25T12:00:00-07:00" ||
		got.WokeAt.Format(time.RFC3339) != "2026-07-25T12:35:00-07:00" {
		t.Fatalf("witness = %+v", got)
	}
	if _, err := ParseS7SleepWitness("no power events\n"); err == nil {
		t.Fatal("missing system witness accepted")
	}
}

func TestCleanupS7SleepDrillRemovesOnlyItsExactState(t *testing.T) {
	home, release, _, sample := s7Fixture(t)
	if _, err := PrepareS7SleepDrill(home, release, true, sample); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(home, "acceptance", "keep")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := CleanupS7SleepDrill(home)
	if err != nil || result.Status != "cleaned" {
		t.Fatalf("cleanup = %+v, %v", result, err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("cleanup removed unrelated state: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "acceptance", "s7-sleep.json")); !os.IsNotExist(err) {
		t.Fatalf("sleep drill state remains: %v", err)
	}
}

func TestDockerDesktopResourceSaverDisabledReadsTheRealSetting(t *testing.T) {
	if ok, err := DockerDesktopResourceSaverDisabled([]byte(`{"UseResourceSaver":false,"other":1}`)); err != nil || !ok {
		t.Fatalf("disabled = %v, %v", ok, err)
	}
	if ok, err := DockerDesktopResourceSaverDisabled([]byte(`{"UseResourceSaver":true}`)); err != nil || ok {
		t.Fatalf("enabled = %v, %v", ok, err)
	}
	if _, err := DockerDesktopResourceSaverDisabled([]byte(`{}`)); err == nil {
		t.Fatal("missing setting accepted")
	}
}
