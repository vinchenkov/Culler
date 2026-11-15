package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"mc/verbs"
)

var projectContainerID = regexp.MustCompile(`^[0-9a-f]{64}$`)

type projectTargets struct {
	Project    string   `json:"project_id"`
	Runs       []string `json:"runs"`
	Sessions   []string `json:"sessions"`
	Operations []string `json:"operations"`
}

// The admission lock excludes new commands while deletion stops existing commands.
// The work lock keeps deletion behind every admitted Docker command.
func projectLock(home, project, suffix string, mode int) (*os.File, error) {
	if !verbs.ValidProjectID(project) {
		return nil, verbs.Usagef("The Project identifier is invalid.")
	}
	dir := filepath.Join(home, "project-locks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, project+"."+suffix), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), mode|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func unlockProject(file *os.File) {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

func admitProjectWork(home, project string) (func(), error) {
	admission, err := projectLock(home, project, "admission", syscall.LOCK_SH)
	if err != nil {
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return nil, verbs.Domainf("Culler must finish Project deletion before new work can start.")
		}
		return nil, err
	}
	defer unlockProject(admission)
	work, err := projectLock(home, project, "work", syscall.LOCK_SH)
	if err != nil {
		return nil, err
	}
	return func() { unlockProject(work) }, nil
}

func projectSpineCommand(args []string, stdout, stderr io.Writer) int {
	if shouldDelegateToHelper() {
		if _, err := ensureRuntimeHelper(); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		return delegate(args, nil, stdout, stderr)
	}
	return runLocal(args, nil, stdout, stderr)
}

func readProjectTargets(project string) (projectTargets, error) {
	var out, diagnostic bytes.Buffer
	var targets projectTargets
	if projectSpineCommand([]string{"project", "container-targets", project}, &out, &diagnostic) != 0 {
		return targets, verbs.Domainf("The Project records are unavailable. Refresh the Projects page.")
	}
	if err := json.Unmarshal(out.Bytes(), &targets); err != nil {
		return targets, err
	}
	if targets.Project != project {
		return targets, fmt.Errorf("The Project identity differs from the request.")
	}
	return targets, nil
}

func dockerLabel(args []string, name string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--label" && strings.HasPrefix(args[i+1], name+"=") {
			return strings.TrimPrefix(args[i+1], name+"=")
		}
	}
	return ""
}

func containsProjectTarget(values []string, value string) bool {
	if value == "" {
		return false
	}
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func (t projectTargets) permits(labels map[string]string) bool {
	if run := labels["mc-run-id"]; run != "" {
		return containsProjectTarget(t.Runs, run)
	}
	if session := labels["mc-session-id"]; session != "" {
		return containsProjectTarget(t.Sessions, session)
	}
	if operation := labels["com.culler.environment.operation"]; operation != "" {
		return containsProjectTarget(t.Operations, operation)
	}
	return true
}

type projectContainer struct {
	ID     string                             `json:"Id"`
	Config struct{ Labels map[string]string } `json:"Config"`
	Mounts []struct{ Source string }          `json:"Mounts"`
}

func inspectProjectContainers(ctx context.Context, ids []string) ([]projectContainer, error) {
	out, err := exec.CommandContext(ctx, "docker", append([]string{"inspect", "--type", "container"}, ids...)...).Output()
	var containers []projectContainer
	if err != nil {
		return nil, fmt.Errorf("Culler cannot inspect the Project containers.")
	}
	if err := json.Unmarshal(out, &containers); err != nil {
		return nil, err
	}
	return containers, nil
}

// The command holds the work lock until Docker exits and returns its output as one record.
func runProjectDocker(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	id, err := verbs.LoadIdentity()
	if err == nil {
		err = verbs.RequireHostScope(id, "Project container control")
	}
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if !canInspectProjectVision() || len(args) < 3 || args[1] != "--" {
		return writeVerbError(stdout, stderr, verbs.Usagef("The Project container request is invalid."))
	}
	project, command := args[0], args[2:]
	if command[0] != "create" && command[0] != "run" && command[0] != "start" && command[0] != "build" {
		return writeVerbError(stdout, stderr, verbs.Usagef("The Project container command is invalid."))
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	release, err := admitProjectWork(home, project)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	defer release()
	targets, err := readProjectTargets(project)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	labels := map[string]string{}
	if command[0] == "start" {
		// The resident starts exactly one container per command.
		containers, err := inspectProjectContainers(context.Background(), []string{command[len(command)-1]})
		if err != nil || len(containers) != 1 {
			return writeVerbError(stdout, stderr, verbs.Domainf("The Project container is unavailable."))
		}
		labels = containers[0].Config.Labels
		if labels["mc-project"] != project && labels["com.culler.project"] != project {
			return writeVerbError(stdout, stderr, verbs.Domainf("The container belongs to another Project."))
		}
	} else {
		for _, key := range []string{"mc-run-id", "mc-session-id", "com.culler.environment.operation"} {
			labels[key] = dockerLabel(command, key)
		}
		command = append([]string{command[0], "--label", "com.culler.home=" + home, "--label", "com.culler.project=" + project}, command[1:]...)
	}
	if !targets.permits(labels) {
		return writeVerbError(stdout, stderr, verbs.Domainf("The Project operation no longer exists."))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	if command[0] == "build" {
		go func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					admission, err := projectLock(home, project, "admission", syscall.LOCK_SH)
					if err != nil {
						cancel()
						return
					}
					unlockProject(admission)
				}
			}
		}()
	}
	child := exec.CommandContext(ctx, "docker", command...)
	child.WaitDelay = 5 * time.Second
	var output, diagnostic bytes.Buffer
	child.Stdin, child.Stdout, child.Stderr = stdin, &output, &diagnostic
	exitCode := 0
	if err := child.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			exitCode = exit.ExitCode()
			if exitCode < 0 {
				exitCode = 1
			}
		} else {
			return writeVerbError(stdout, stderr, err)
		}
	}
	if err := writeJSON(stdout, map[string]any{"exitCode": exitCode, "stdout": output.String(), "stderr": diagnostic.String()}); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	return 0
}

func (t projectTargets) owns(container projectContainer, home string) bool {
	labels := container.Config.Labels
	if labels["mc-managed"] != "true" {
		return false
	}
	project := labels["mc-project"]
	if project == "" {
		project = labels["com.culler.project"]
	}
	if labels["com.culler.home"] != "" {
		return labels["com.culler.home"] == home && project == t.Project
	}
	// Existing containers identify their deployment through their private mounts.
	local := false
	for _, mount := range container.Mounts {
		source := strings.TrimPrefix(mount.Source, "/host_mnt")
		if source == home || strings.HasPrefix(source, home+string(filepath.Separator)) {
			local = true
		}
	}
	if !local {
		return false
	}
	return project == t.Project || containsProjectTarget(t.Runs, labels["mc-run-id"]) ||
		containsProjectTarget(t.Sessions, labels["mc-session-id"]) || containsProjectTarget(t.Operations, labels["com.culler.environment.operation"])
}

func stopProjectContainers(ctx context.Context, home string, targets projectTargets) error {
	out, err := exec.CommandContext(ctx, "docker", "ps", "--all", "--no-trunc", "--quiet", "--filter", "label=mc-managed=true").Output()
	if err != nil {
		return verbs.Domainf("Culler cannot inspect Docker. The Project records remain intact. Restore Docker, then retry deletion.")
	}
	for _, id := range strings.Fields(string(out)) {
		if !projectContainerID.MatchString(id) {
			return fmt.Errorf("Docker returned an invalid container identifier.")
		}
		containers, err := inspectProjectContainers(ctx, []string{id})
		if err != nil {
			// A container can disappear between the list and inspection.
			check, checkErr := exec.CommandContext(ctx, "docker", "ps", "--all", "--no-trunc", "--quiet", "--filter", "id="+id).Output()
			if checkErr == nil && len(bytes.TrimSpace(check)) == 0 {
				continue
			}
			return err
		}
		if len(containers) != 1 || containers[0].ID != id {
			return fmt.Errorf("Docker returned another container identity.")
		}
		if !targets.owns(containers[0], home) {
			continue
		}
		if output, err := exec.CommandContext(ctx, "docker", "rm", "--force", id).CombinedOutput(); err != nil && !strings.Contains(string(output), "No such container") {
			return verbs.Domainf("Culler cannot stop a Project container. The Project records remain intact. Retry deletion.")
		}
	}
	return nil
}

func brokerProjectDelete(args []string, stdout, stderr io.Writer) int {
	id, err := verbs.LoadIdentity()
	if err == nil {
		err = verbs.RequireHostScope(id, "project.delete")
	}
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if len(args) != 4 || args[3] != "--confirm" {
		return writeVerbError(stdout, stderr, verbs.Usagef("Project deletion requires explicit confirmation in the Projects page."))
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	project := args[2]
	admission, err := projectLock(home, project, "admission", syscall.LOCK_EX)
	if err != nil {
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			err = verbs.Domainf("Another Project operation is active. Retry deletion.")
		}
		return writeVerbError(stdout, stderr, err)
	}
	defer unlockProject(admission)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		targets, err := readProjectTargets(project)
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		if err := stopProjectContainers(ctx, home, targets); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		work, err := projectLock(home, project, "work", syscall.LOCK_EX)
		if err == nil {
			defer unlockProject(work)
			// The final inspection covers a create that completed during the previous inspection.
			if err := stopProjectContainers(ctx, home, targets); err != nil {
				return writeVerbError(stdout, stderr, err)
			}
			return projectSpineCommand(args, stdout, stderr)
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return writeVerbError(stdout, stderr, err)
		}
		select {
		case <-ctx.Done():
			return writeVerbError(stdout, stderr, verbs.Domainf("Culler could not stop all Project operations. The Project records remain intact. Retry deletion."))
		case <-time.After(100 * time.Millisecond):
		}
	}
}
