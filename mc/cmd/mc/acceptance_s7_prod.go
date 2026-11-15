//go:build !test_fake_routing

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"mc/deployment"
	"mc/verbs"
)

const s7SnapshotOutputLimit = 64 * 1024
const s7PowerLogOutputLimit = 16 * 1024 * 1024

type s7RuntimeSnapshot struct {
	Version  int                           `json:"version"`
	VMNow    string                        `json:"vm_now"`
	Leases   []deployment.S7LeaseSnapshot  `json:"leases"`
	Settings deployment.S7SettingsSnapshot `json:"settings"`
}

func decodeS7RuntimeSnapshot(body []byte) (s7RuntimeSnapshot, error) {
	var runtime s7RuntimeSnapshot
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&runtime); err != nil {
		return runtime, fmt.Errorf("decode sleep drill helper snapshot: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return runtime, fmt.Errorf("sleep drill helper snapshot carries trailing data")
	}
	if runtime.Version != 1 {
		return runtime, fmt.Errorf("sleep drill helper snapshot version is unsupported")
	}
	return runtime, nil
}

func collectS7Sample(home string) (deployment.S7Sample, error) {
	var sample deployment.S7Sample
	helper, err := ensureRuntimeHelper()
	if err != nil {
		return sample, err
	}
	sample.HostBefore = time.Now().UTC()
	var stdout, stderr boundedDoctorBuffer
	stdout.limit = s7SnapshotOutputLimit
	stderr.limit = s7SnapshotOutputLimit
	cmd := exec.Command("docker", "exec", "-i", helper, "mc", "__acceptance-snapshot")
	cmd.Stdin = bytes.NewReader(nil)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return sample, fmt.Errorf("sleep drill helper snapshot failed: %v: %s", err, stderr.String())
	}
	if stdout.overflow || stderr.overflow {
		return sample, fmt.Errorf("sleep drill helper snapshot exceeded its output bound")
	}
	runtime, err := decodeS7RuntimeSnapshot(stdout.Bytes())
	if err != nil {
		return sample, err
	}
	sample.VMNow, err = time.Parse(time.RFC3339Nano, runtime.VMNow)
	if err != nil {
		return sample, fmt.Errorf("parse sleep drill helper clock: %w", err)
	}
	sample.Leases = runtime.Leases
	sample.Settings = runtime.Settings
	sample.Tick, err = deployment.ReadS7TickSnapshot(home)
	sample.HostAfter = time.Now().UTC()
	if err != nil {
		return sample, err
	}
	return sample, nil
}

func waitForS7Tick(home string, after int64, completedAfter, deadline time.Time) (deployment.S7Sample, error) {
	for time.Now().Before(deadline) {
		tick, err := deployment.ReadS7TickSnapshot(home)
		if err != nil {
			return deployment.S7Sample{}, err
		}
		completed, completedErr := deployment.S7TickCompletedAt(tick)
		if completedErr != nil {
			return deployment.S7Sample{}, completedErr
		}
		if tick.Sequence > after && !completed.Before(completedAfter) {
			sample, err := collectS7Sample(home)
			if err != nil {
				return deployment.S7Sample{}, err
			}
			sampleCompleted, err := deployment.S7TickCompletedAt(sample.Tick)
			if err != nil {
				return deployment.S7Sample{}, err
			}
			if sample.Tick.Sequence > after && !sampleCompleted.Before(completedAfter) {
				return sample, nil
			}
		}
		time.Sleep(time.Second)
	}
	return deployment.S7Sample{}, fmt.Errorf("resident produced no next tick within 90 seconds")
}

func requireS7ActiveSupervision(home string) error {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	_, err = deployment.InspectActiveSupervision(
		home, filepath.Join(userHome, "Library", "LaunchAgents"), releaseBuildID,
		launchdController{}, time.Now(),
	)
	return err
}

func readS7SleepWitness() (deployment.S7SleepWitness, error) {
	var stdout, stderr boundedDoctorBuffer
	stdout.limit = s7PowerLogOutputLimit
	stderr.limit = s7SnapshotOutputLimit
	cmd := exec.Command("/usr/bin/pmset", "-g", "log")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return deployment.S7SleepWitness{}, fmt.Errorf("read macOS power log: %v: %s", err, stderr.String())
	}
	if stdout.overflow || stderr.overflow {
		return deployment.S7SleepWitness{}, fmt.Errorf("macOS power log exceeded its output bound")
	}
	return deployment.ParseS7SleepWitness(stdout.String())
}

func readResourceSaverDisabled() (bool, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	path := filepath.Join(userHome, "Library", "Group Containers",
		"group.com.docker", "settings-store.json")
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() ||
		info.Size() <= 0 || info.Size() > 16*1024*1024 {
		return false, fmt.Errorf("Docker Desktop settings are not a bounded regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return deployment.DockerDesktopResourceSaverDisabled(body)
}

type s7AcceptanceDeps struct {
	requireActive    func(string) error
	resourceDisabled func() (bool, error)
	collectSample    func(string) (deployment.S7Sample, error)
	readSleepWitness func() (deployment.S7SleepWitness, error)
	waitForTick      func(string, int64, time.Time, time.Time) (deployment.S7Sample, error)
	now              func() time.Time
}

func productionS7AcceptanceDeps() s7AcceptanceDeps {
	return s7AcceptanceDeps{
		requireActive: requireS7ActiveSupervision, resourceDisabled: readResourceSaverDisabled,
		collectSample: collectS7Sample, readSleepWitness: readS7SleepWitness,
		waitForTick: waitForS7Tick, now: time.Now,
	}
}

func executeS7Acceptance(home, release, mode string, deps s7AcceptanceDeps) (deployment.S7DrillResult, error) {
	var result deployment.S7DrillResult
	switch mode {
	case "prepare":
		replayed, ok, err := deployment.ReplayPreparedS7SleepDrill(home, release)
		if err != nil {
			return result, fmt.Errorf("read prepared state: %w", err)
		}
		if ok {
			return replayed, nil
		}
		if err := deps.requireActive(home); err != nil {
			return result, fmt.Errorf("supervision check: %w", err)
		}
		disabled, err := deps.resourceDisabled()
		if err != nil {
			return result, fmt.Errorf("inspect Docker Desktop Resource Saver: %w", err)
		}
		if !disabled {
			return result, fmt.Errorf("Docker Desktop Resource Saver remains enabled; disable it before sleep drill prepare")
		}
		sample, err := deps.collectSample(home)
		if err != nil {
			return result, fmt.Errorf("collect baseline: %w", err)
		}
		return deployment.PrepareS7SleepDrill(home, release, true, sample)
	case "verify":
		replayed, ok, err := deployment.ReplayVerifiedS7SleepDrill(home, release)
		if err != nil {
			return result, fmt.Errorf("read verification state: %w", err)
		}
		if ok {
			return replayed, nil
		}
		witness, err := deps.readSleepWitness()
		if err != nil {
			return result, fmt.Errorf("collect system sleep witness: %w", err)
		}
		sequence, err := deployment.S7PreparedTickSequence(home)
		if err != nil {
			return result, fmt.Errorf("read prepared state: %w", err)
		}
		count, err := deployment.S7CollectionCount(home)
		if err != nil {
			return result, fmt.Errorf("read collection state: %w", err)
		}
		if count > 0 {
			if err := deps.requireActive(home); err != nil {
				return result, fmt.Errorf("post-wake supervision check: %w", err)
			}
		}
		for count < 3 {
			sample, err := deps.waitForTick(
				home, sequence, witness.WokeAt, deps.now().Add(90*time.Second))
			if err != nil {
				return result, fmt.Errorf("collect post-wake cadence: %w", err)
			}
			count, err = deployment.RecordS7PostWakeSample(home, release, witness, sample)
			if err != nil {
				return result, fmt.Errorf("persist post-wake cadence: %w", err)
			}
			if count == 1 {
				if err := deps.requireActive(home); err != nil {
					return result, fmt.Errorf("post-wake supervision check: %w", err)
				}
			}
			sequence = sample.Tick.Sequence
		}
		return deployment.FinalizeS7SleepDrill(home, release)
	case "cleanup":
		return deployment.CleanupS7SleepDrill(home)
	default:
		return result, fmt.Errorf("unsupported sleep drill mode %q", mode)
	}
}

func brokerAcceptance(args []string, stdout, stderr io.Writer) int {
	if len(args) >= 2 && args[0] == "__acceptance" && args[1] == "provider" {
		return brokerProviderAcceptance(args, stdout, stderr)
	}
	if len(args) < 3 || args[0] != "__acceptance" || args[1] != "s7" {
		return writeVerbError(stdout, stderr,
			verbs.Usagef("usage: mc __acceptance <s7|provider> ..."))
	}
	mode := args[2]
	rest := args[3:]
	switch mode {
	case "prepare":
		if len(rest) != 1 || rest[0] != "--resource-saver-disabled" {
			return writeVerbError(stdout, stderr,
				verbs.Usagef("usage: mc __acceptance s7 prepare --resource-saver-disabled"))
		}
	case "verify", "cleanup":
		if len(rest) != 0 {
			return writeVerbError(stdout, stderr,
				verbs.Usagef("usage: mc __acceptance s7 %s", mode))
		}
	default:
		return writeVerbError(stdout, stderr,
			verbs.Usagef("usage: mc __acceptance s7 <prepare|verify|cleanup> [--resource-saver-disabled]"))
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("resolve sleep drill MC_HOME: %v", err))
	}
	result, err := executeS7Acceptance(home, releaseBuildID, mode, productionS7AcceptanceDeps())
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Domainf("sleep drill %s: %v", mode, err))
	}
	if err := writeJSON(stdout, result); err != nil {
		fmt.Fprintln(stderr, "mc:", err)
		return 2
	}
	return 0
}
