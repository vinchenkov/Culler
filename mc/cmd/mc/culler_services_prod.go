//go:build !test_fake_routing

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"mc/deployment"
	"mc/distribution"
	"mc/substrate"
)

type cullerServiceControl interface {
	deployment.LaunchdController
	SetStopped(string, bool) error
}

type cullerLaunchd struct{ ctx context.Context }

func cullerLabel(home, unit string) string {
	names, _ := deployment.RuntimeNames(home)
	return deployment.UnitLabel(names, unit)
}

func (c cullerLaunchd) command(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput()
}

func (c cullerLaunchd) Loaded(label string) (bool, error) {
	body, err := c.command("print", fmt.Sprintf("gui/%d/%s", os.Getuid(), label))
	if err == nil {
		return true, nil
	}
	text := strings.ToLower(string(body))
	if strings.Contains(text, "could not find service") || strings.Contains(text, "service not found") {
		return false, nil
	}
	return false, fmt.Errorf("The service state is unavailable for %s.", label)
}

func (c cullerLaunchd) Bootstrap(plist string) error {
	_, err := c.command("bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), plist)
	if err != nil {
		return errors.New("macOS could not start the service.")
	}
	return nil
}

func (c cullerLaunchd) Bootout(label string) error {
	_, err := c.command("bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), label))
	if err != nil {
		return errors.New("macOS could not stop the service.")
	}
	return nil
}

func (c cullerLaunchd) SetStopped(label string, stopped bool) error {
	verb := "enable"
	if stopped {
		verb = "disable"
	}
	_, err := c.command(verb, fmt.Sprintf("gui/%d/%s", os.Getuid(), label))
	if err != nil {
		return errors.New("macOS could not save the service start choice.")
	}
	return nil
}

type cullerOperation struct {
	ID       string                  `json:"id"`
	Command  string                  `json:"command"`
	Phase    string                  `json:"phase"`
	Started  time.Time               `json:"started_at"`
	Services []string                `json:"services"`
	Prior    map[string]bool         `json:"prior"`
	Stops    deployment.ServiceStops `json:"stops"`
}

const cullerOperationRecord = "state/lifecycle-operation.json"

type cullerManager struct {
	home, launchAgents string
	runtime            cullerRuntime
	control            func(context.Context) cullerServiceControl
	ready              func(context.Context, string, time.Time) error
	prepare            func(string) error
	validate           func(string) error
	quiet              func(context.Context, string) error
	maintenance        *publishedSetup
	now                func() time.Time
	sleep              func(context.Context, time.Duration) error
}

func newCullerManager(home string) (*cullerManager, error) {
	if err := deployment.UpdateBlocksWork(home); err != nil {
		return nil, err
	}
	r, err := readCullerRuntime(home)
	if err != nil {
		return nil, fmt.Errorf("The service configuration is absent or invalid. Use the installer to resume setup.\n%s", cullerInstallCommand(home))
	}
	installed, err := cullerInstalled(home)
	if err != nil {
		return nil, err
	}
	if cullerReleaseIdentity(r.Release) != installed || installed != cullerBuild() {
		return nil, fmt.Errorf("The command and installed services use different releases. Use the installer to repair the release.\n%s", deployment.CLICommand(home, "update"))
	}
	if err := inspectCullerCommandFiles(home); err != nil {
		return nil, fmt.Errorf("%w\n%s", err, cullerInstallCommand(home))
	}
	if _, err := os.ReadFile(filepath.Join(home, "deployment.uuid")); err != nil {
		return nil, fmt.Errorf("The deployment identity is unavailable. Use the installer to repair setup.\n%s", cullerInstallCommand(home))
	}
	if _, err := os.Stat(filepath.Join(home, "state", "pending-release.json")); err == nil {
		return nil, fmt.Errorf("An update requires verification. Use the installer to complete it.\n%s", deployment.CLICommand(home, "update"))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	record, err := distribution.Load(filepath.Join(home, distribution.InstalledRecord))
	if errors.Is(err, os.ErrNotExist) {
		record = distribution.Record{Commit: r.Release, Image: r.Image, Schema: substrate.CurrentSchemaID}
	} else if err != nil {
		return nil, err
	}
	if record.Schema != substrate.CurrentSchemaID {
		return nil, fmt.Errorf("The installed database schema requires a compatible release.\n%s", deployment.CLICommand(home, "update"))
	}
	return cullerManagerForRuntime(home, r, record)
}

func cullerManagerForRuntime(home string, r cullerRuntime, record distribution.Record) (*cullerManager, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	m := &cullerManager{home: home, launchAgents: filepath.Join(userHome, "Library", "LaunchAgents"), runtime: r,
		control: func(ctx context.Context) cullerServiceControl { return cullerLaunchd{ctx} }, now: time.Now,
		sleep: func(ctx context.Context, d time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
				return nil
			}
		},
		prepare: func(unit string) error {
			if unit == "agentsview" {
				return deployment.InstallAgentsViewConfig(home)
			}
			return nil
		},
		ready: func(ctx context.Context, unit string, since time.Time) error {
			return cullerReady(ctx, home, unit, r, since)
		},
	}
	m.maintenance = &publishedSetup{home: home, record: record, now: time.Now, sleep: time.Sleep, forceMaintenance: true,
		docker: func(ctx context.Context, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, r.Docker, args...).CombinedOutput()
		},
		states: func(string) (map[string]bool, error) { return m.states(context.Background()) },
	}
	m.validate = func(unit string) error {
		return deployment.InspectServiceDefinition(home, m.launchAgents, unit, r.Release)
	}
	m.quiet = func(ctx context.Context, unit string) error { return cullerQuiet(ctx, home, unit) }
	return m, nil
}

func (m *cullerManager) states(ctx context.Context) (map[string]bool, error) {
	states := map[string]bool{}
	for _, unit := range deployment.SupervisedUnits {
		loaded, err := m.control(ctx).Loaded(cullerLabel(m.home, unit))
		if err != nil {
			return nil, err
		}
		states[unit] = loaded
	}
	return states, nil
}

func readCullerOperation(home string) (*cullerOperation, error) {
	path := filepath.Join(home, cullerOperationRecord)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Getuid()) || owner.Nlink != 1 || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 65536 {
		return nil, errors.New("The service recovery record requires an owner-only regular file.")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var op cullerOperation
	if json.Unmarshal(body, &op) != nil || len(op.ID) != 32 || op.Prior == nil || op.Stops == nil || len(op.Services) == 0 || (op.Command != "start" && op.Command != "stop" && op.Command != "restart") {
		return nil, fmt.Errorf("The service recovery record is invalid. Ask your coding harness to inspect it.\n%s", path)
	}
	for _, unit := range op.Services {
		if !slices.Contains(deployment.SupervisedUnits, unit) {
			return nil, errors.New("The previous service operation names an invalid service.")
		}
		if _, ok := op.Prior[unit]; !ok {
			return nil, errors.New("The service recovery record lacks a previous state.")
		}
	}
	return &op, nil
}

func cullerOperationDetail(home string) string {
	if j, err := deployment.LoadUpdateJournal(home); err != nil {
		return "The update recovery record is unavailable. Keep Culler stopped."
	} else if j != nil && j.BlocksWork() {
		return "An interrupted update keeps work stopped. Run culler update to recover."
	} else if j != nil && j.Phase == "committed" {
		return "The update succeeded. Run culler update to complete cleanup."
	}
	if op, err := readCullerOperation(home); err != nil {
		return "The service recovery record is unavailable."
	} else if op != nil {
		return "Service operation: " + op.Command + ". Phase: " + op.Phase + ". A later service command checks recovery first."
	}
	var op struct {
		Phase string `json:"phase"`
	}
	if body, err := os.ReadFile(filepath.Join(home, "state", "install-operation.json")); err == nil && json.Unmarshal(body, &op) == nil && op.Phase != "complete" {
		return "An update awaits verification. Use the installer to resume it."
	}
	return ""
}

func (m *cullerManager) save(op *cullerOperation, phase string) error {
	op.Phase = phase
	return writePublishedState(filepath.Join(m.home, cullerOperationRecord), op)
}

func (m *cullerManager) change(ctx context.Context, command, service string, report func(string)) (result error) {
	lock, err := lockPublishedSetup(m.home)
	if err != nil {
		return err
	}
	defer lock.Close()
	priorOperation, err := readCullerOperation(m.home)
	if err != nil {
		return err
	}
	if priorOperation != nil {
		report("Restore the interrupted service operation")
		m.maintenance.maintenanceID = priorOperation.ID
		if priorOperation.Phase != "complete" {
			if err := m.restore(priorOperation, report); err != nil {
				return err
			}
		}
		m.maintenance.releaseMaintenance()
		if err := os.Remove(filepath.Join(m.home, cullerOperationRecord)); err != nil {
			return err
		}
	}
	prior, err := m.states(ctx)
	if err != nil {
		return err
	}
	stops, err := deployment.ReadServiceStops(m.home)
	if err != nil {
		return err
	}
	selected := slices.Clone(deployment.SupervisedUnits)
	if service != "" {
		selected = []string{service}
	}
	for _, unit := range selected {
		if err := m.validate(unit); err != nil {
			return fmt.Errorf("The %s service requires setup repair: %w", unit, err)
		}
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return err
	}
	op := &cullerOperation{ID: hex.EncodeToString(id), Command: command, Started: m.now(), Services: selected, Prior: prior, Stops: stops}
	if err := m.save(op, "prepare"); err != nil {
		return err
	}
	m.maintenance.maintenanceID = op.ID
	defer func() {
		if result != nil {
			if restoreErr := m.restore(op, report); restoreErr != nil {
				_ = m.save(op, "recovery required")
				result = errors.Join(result, restoreErr)
				return
			}
			result = fmt.Errorf("%w\nThe saved stop choices are restored. The service checks below show the current state.", result)
		}
		m.maintenance.releaseMaintenance()
		if err := os.Remove(filepath.Join(m.home, cullerOperationRecord)); err != nil {
			result = errors.Join(result, err)
		}
	}()
	if command != "start" {
		if slices.Contains(selected, "resident") && prior["resident"] {
			if err := m.waitForWork(ctx, report); err != nil {
				return err
			}
		}
		if err := m.save(op, "stop"); err != nil {
			return err
		}
		stop := func(string) error {
			for _, unit := range selected {
				if err := m.transition(ctx, unit, false, report); err != nil {
					return err
				}
			}
			return nil
		}
		if slices.Contains(selected, "resident") && prior["resident"] {
			if err := m.maintenance.stopAndRecheck(ctx, stop); err != nil {
				return err
			}
		} else if err := stop(m.home); err != nil {
			return err
		}
	}
	nextStops := deployment.ServiceStops{}
	for unit, stop := range stops {
		nextStops[unit] = stop
	}
	for _, unit := range selected {
		nextStops[unit] = command == "stop"
	}
	if err := deployment.WriteServiceStops(m.home, nextStops); err != nil {
		return err
	}
	if command != "stop" {
		if err := m.save(op, "start"); err != nil {
			return err
		}
		for i := len(selected) - 1; i >= 0; i-- {
			if err := m.transition(ctx, selected[i], true, report); err != nil {
				return err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.save(op, "complete")
}

func (m *cullerManager) waitForWork(ctx context.Context, report func(string)) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	started := m.now()
	return cullerReportWait(ctx, func() error { return m.maintenance.drain(ctx) }, func() {
		report(fmt.Sprintf("Wait for active agents and repository work · %s elapsed", m.now().Sub(started).Round(time.Second)))
	})
}

func cullerReportWait(ctx context.Context, action func() error, report func()) error {
	report()
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				report()
			}
		}
	}()
	err := action()
	close(done)
	<-finished
	return err
}

func (m *cullerManager) transition(ctx context.Context, unit string, start bool, report func(string)) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	control, label := m.control(ctx), cullerLabel(m.home, unit)
	loaded, err := control.Loaded(label)
	if err != nil {
		return err
	}
	if start && loaded && m.ready(ctx, unit, time.Time{}) == nil {
		return control.SetStopped(label, false)
	}
	if start && loaded {
		return fmt.Errorf("The %s service is active but unhealthy. Inspect its diagnostics before a restart.", unit)
	}
	if err := control.SetStopped(label, !start); err != nil {
		return err
	}
	since := m.now()
	if start {
		if err := m.prepare(unit); err != nil {
			return err
		}
		if err := control.Bootstrap(filepath.Join(m.launchAgents, label+".plist")); err != nil {
			return err
		}
	} else if loaded {
		if err := control.Bootout(label); err != nil {
			return err
		}
	}
	var last error
	for {
		loaded, err := control.Loaded(label)
		if err == nil && loaded != start {
			if loaded {
				err = errors.New("The service remains active.")
			} else {
				err = errors.New("The service remains stopped.")
			}
		}
		if err == nil && loaded == start {
			if !start {
				err = m.quiet(ctx, unit)
				if err == nil {
					return nil
				}
			} else {
				err = m.ready(ctx, unit, since)
				if err == nil {
					return nil
				}
			}
		}
		last = err
		verb := "stop"
		if start {
			verb = "start"
		}
		report(fmt.Sprintf("Wait for %s to %s · %s elapsed", unit, verb, m.now().Sub(since).Round(time.Second)))
		if err := m.sleep(ctx, time.Second); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			return fmt.Errorf("The %s service did not %s within 90 seconds. %v", unit, verb, last)
		}
	}
}

func (m *cullerManager) restore(op *cullerOperation, report func(string)) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	var problems []error
	for _, unit := range op.Services {
		loaded, err := m.control(ctx).Loaded(cullerLabel(m.home, unit))
		if err != nil {
			problems = append(problems, fmt.Errorf("%s state is unknown: %w", unit, err))
			continue
		}
		if loaded && !op.Prior[unit] {
			if unit == "resident" {
				if err := m.waitForWork(ctx, report); err != nil {
					problems = append(problems, err)
					continue
				}
			}
			if err := m.transition(ctx, unit, false, report); err != nil {
				problems = append(problems, fmt.Errorf("%s remains active: %w", unit, err))
			}
		}
	}
	if err := deployment.WriteServiceStops(m.home, op.Stops); err != nil {
		problems = append(problems, err)
	}
	for i := len(op.Services) - 1; i >= 0; i-- {
		unit := op.Services[i]
		if err := m.control(ctx).SetStopped(cullerLabel(m.home, unit), op.Stops[unit]); err != nil {
			problems = append(problems, err)
		}
		if op.Prior[unit] {
			loaded, err := m.control(ctx).Loaded(cullerLabel(m.home, unit))
			if err == nil && loaded {
				continue
			}
			if err := m.transition(ctx, unit, true, report); err != nil {
				problems = append(problems, fmt.Errorf("%s restoration failed: %w", unit, err))
			}
		}
	}
	return errors.Join(problems...)
}
