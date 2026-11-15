package deployment

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const UpdateStopGrace = 10 * time.Second
const UpdateStopLimit = 30 * time.Second

type UpdateProcess struct {
	PID      int
	Group    int
	Parent   int
	Identity string
	Command  string
}

type UpdateStopControl struct {
	Processes func(context.Context) ([]UpdateProcess, error)
	Signal    func(context.Context, UpdateProcess, syscall.Signal) error
	Services  func(context.Context, bool) error
	Docker    func(context.Context, ...string) ([]byte, error)
	Now       func() time.Time
	Wait      func(context.Context, time.Duration) error
}

func updateCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func readUpdateProcesses(ctx context.Context) ([]UpdateProcess, error) {
	body, err := updateCommand(ctx, "/bin/ps", "-ww", "-axo", "pid=,ppid=,pgid=,lstart=,command=")
	if err != nil {
		return nil, errors.New("The service process check is unavailable.")
	}
	var processes []UpdateProcess
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 9 {
			return nil, errors.New("The service process check returned an invalid record.")
		}
		pid, e1 := strconv.Atoi(fields[0])
		parent, e2 := strconv.Atoi(fields[1])
		group, e3 := strconv.Atoi(fields[2])
		if e1 != nil || e2 != nil || e3 != nil || pid < 1 || parent < 0 || group < 0 {
			return nil, errors.New("The service process identity is invalid.")
		}
		processes = append(processes, UpdateProcess{PID: pid, Parent: parent, Group: group, Identity: strings.Join(fields[3:], " "), Command: strings.Join(fields[8:], " ")})
	}
	return processes, nil
}

func NewUpdateStopControl(home string, docker func(context.Context, ...string) ([]byte, error)) UpdateStopControl {
	return UpdateStopControl{
		Processes: readUpdateProcesses,
		Signal: func(ctx context.Context, process UpdateProcess, signal syscall.Signal) error {
			current, err := readUpdateProcesses(ctx)
			if err != nil {
				return err
			}
			for _, row := range current {
				if row.PID == process.PID {
					if row.Identity != process.Identity {
						return fmt.Errorf("The identity of process %d changed. Retry the update.", process.PID)
					}
					if err := syscall.Kill(process.PID, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
						return err
					}
					return nil
				}
			}
			return nil
		},
		Services: func(ctx context.Context, stop bool) error {
			names, err := RuntimeNames(home)
			if err != nil {
				return err
			}
			var wg sync.WaitGroup
			var mu sync.Mutex
			var problems []error
			for _, unit := range SupervisedUnits {
				unit := unit
				wg.Add(1)
				go func() {
					defer wg.Done()
					label := fmt.Sprintf("gui/%d/%s", os.Getuid(), UnitLabel(names, unit))
					verb := "print"
					if stop {
						verb = "bootout"
					}
					body, err := updateCommand(ctx, "/bin/launchctl", verb, label)
					if err != nil {
						message := strings.ToLower(string(body))
						if strings.Contains(message, "could not find service") || strings.Contains(message, "service not found") || strings.Contains(message, "no such process") {
							return
						}
					}
					if stop {
						return
					}
					if err == nil {
						err = fmt.Errorf("The %s service remains loaded.", unit)
					} else {
						err = fmt.Errorf("The %s service state is unknown.", unit)
					}
					mu.Lock()
					problems = append(problems, err)
					mu.Unlock()
				}()
			}
			wg.Wait()
			return errors.Join(problems...)
		}, Docker: docker, Now: time.Now,
		Wait: func(ctx context.Context, d time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
				return nil
			}
		},
	}
}

func updateOwnedProcesses(home string, all []UpdateProcess, retained map[int]UpdateProcess) []UpdateProcess {
	markers := []string{filepath.Join(home, "supervision", "resident.json"), filepath.Join(home, "supervision", "dashboard.json"), AgentsViewPathsFor(home).Executable}
	owned := map[int]bool{}
	groups := map[int]bool{}
	for _, p := range retained {
		if p.Group > 1 && p.Group == p.PID {
			groups[p.Group] = true
		}
	}
	for _, p := range all {
		if old, ok := retained[p.PID]; ok && p.PID == p.Group && old.Identity != p.Identity {
			delete(groups, p.Group)
		}
		if old, ok := retained[p.PID]; ok && old.Identity == p.Identity {
			owned[p.PID] = true
		}
		for _, marker := range markers {
			if strings.Contains(p.Command, marker) {
				owned[p.PID] = true
				if p.Group > 1 && p.Group == p.PID {
					groups[p.Group] = true
				}
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, p := range all {
			if (owned[p.Parent] || groups[p.Group]) && !owned[p.PID] {
				owned[p.PID] = true
				changed = true
			}
		}
	}
	var result []UpdateProcess
	for _, p := range all {
		if owned[p.PID] {
			retained[p.PID] = p
			result = append(result, p)
		}
	}
	return result
}

type updateContainer struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Running bool   `json:"Running"`
		Status  string `json:"Status"`
	} `json:"State"`
}

func updateContainers(ctx context.Context, names Names, control UpdateStopControl) ([]string, error) {
	ids := map[string]bool{}
	for _, filter := range []string{"label=mc-deployment=" + names.Suffix, "volume=" + names.Volume, "volume=" + names.RuntimeVolume} {
		body, err := control.Docker(ctx, "container", "ls", "--all", "--no-trunc", "--quiet", "--filter", filter)
		if err != nil {
			return nil, fmt.Errorf("The update cannot list its containers: %w", err)
		}
		for _, id := range strings.Fields(string(body)) {
			if _, err := hex.DecodeString(id); len(id) != 64 || err != nil {
				return nil, errors.New("The container list contains an invalid identity.")
			}
			ids[id] = true
		}
	}
	var active []string
	for id := range ids {
		body, err := control.Docker(ctx, "container", "inspect", "--format", "{{json .}}", id)
		if err != nil {
			if strings.Contains(strings.ToLower(string(body)), "no such") {
				continue
			}
			return nil, fmt.Errorf("The state of container %s is unknown.", id[:12])
		}
		var container updateContainer
		if json.Unmarshal(body, &container) != nil || container.ID != id {
			return nil, errors.New("The container inspection returned an invalid identity.")
		}
		if container.Config.Labels["mc-deployment"] != names.Suffix {
			return nil, fmt.Errorf("Container %s uses this deployment without its identity. Replacement remains blocked.", id[:12])
		}
		if container.State.Running || (container.State.Status != "created" && container.State.Status != "exited" && container.State.Status != "dead") {
			active = append(active, id)
		}
	}
	return active, nil
}

// StopUpdateWork verifies every stop before the caller replaces software.
func StopUpdateWork(ctx context.Context, home string, control UpdateStopControl, report func(string)) error {
	names, err := RuntimeNames(home)
	if err != nil {
		return err
	}
	if control.Now == nil {
		control.Now = time.Now
	}
	if report == nil {
		report = func(string) {}
	}
	ctx, cancel := context.WithTimeout(ctx, UpdateStopLimit)
	deadline := control.Now().Add(UpdateStopLimit)
	forceAt := control.Now().Add(UpdateStopGrace)
	retained := map[int]UpdateProcess{}
	signalled := map[string]string{}
	containersSent := map[string]bool{}
	forced := false
	var serviceProblem error
	initial, err := control.Processes(ctx)
	if err != nil {
		cancel()
		return err
	}
	updateOwnedProcesses(home, initial, retained)
	serviceDone := make(chan error, 1)
	go func() { serviceDone <- control.Services(ctx, true) }()
	var commands sync.WaitGroup
	defer func() { cancel(); commands.Wait() }()
	report("Stop services and active containers")
	for {
		all, err := control.Processes(ctx)
		if err != nil {
			return err
		}
		processes := updateOwnedProcesses(home, all, retained)
		containers, err := updateContainers(ctx, names, control)
		if err != nil {
			return err
		}
		force := !control.Now().Before(forceAt)
		if force && !forced {
			report("Force processes that did not exit")
			forced = true
		}
		for _, process := range processes {
			stage := "term"
			signal := syscall.SIGTERM
			if force {
				stage = "kill"
				signal = syscall.SIGKILL
			}
			key := strconv.Itoa(process.PID) + "/" + process.Identity
			if signalled[key] == stage {
				continue
			}
			if err := control.Signal(ctx, process, signal); err != nil {
				return fmt.Errorf("Process %d could not stop: %w", process.PID, err)
			}
			signalled[key] = stage
		}
		for _, id := range containers {
			if containersSent[id] && !force {
				continue
			}
			key := id
			if force {
				key += "/kill"
			}
			if containersSent[key] {
				continue
			}
			containersSent[key] = true
			id := id
			commands.Add(1)
			go func() {
				defer commands.Done()
				if force {
					_, _ = control.Docker(ctx, "kill", id)
				} else {
					_, _ = control.Docker(ctx, "stop", "--time", "10", id)
				}
			}()
		}
		if len(processes) == 0 && len(containers) == 0 {
			serviceProblem = control.Services(ctx, false)
			if serviceProblem == nil {
				select {
				case <-serviceDone:
					return nil
				default:
				}
			}
		}
		if !control.Now().Before(deadline) || ctx.Err() != nil {
			return updateStopTimeout(processes, containers, serviceProblem)
		}
		if err := control.Wait(ctx, 100*time.Millisecond); err != nil {
			return updateStopTimeout(processes, containers, serviceProblem)
		}
	}
}

func updateStopTimeout(processes []UpdateProcess, containers []string, service error) error {
	detail := "The service state remains unknown."
	if len(processes) > 0 {
		detail = fmt.Sprintf("Process %d remains active.", processes[0].PID)
	}
	if len(containers) > 0 {
		detail = fmt.Sprintf("Container %s remains active.", containers[0][:12])
	}
	if service != nil {
		detail = service.Error()
	}
	return fmt.Errorf("%s The stop exceeded thirty seconds. Replacement remains blocked. Retry culler update.", detail)
}
