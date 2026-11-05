//go:build !test_fake_routing

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"mc/deployment"
)

type dockerCommandRunner interface {
	CombinedOutput(args ...string) ([]byte, error)
}

type execDockerRunner struct{}

func (execDockerRunner) CombinedOutput(args ...string) ([]byte, error) {
	return exec.Command("docker", args...).CombinedOutput()
}

type helperImageInspect struct {
	ID           string `json:"Id"`
	OS           string `json:"Os"`
	Architecture string `json:"Architecture"`
}

type helperVolumeInspect struct {
	Name    string            `json:"Name"`
	Driver  string            `json:"Driver"`
	Options map[string]string `json:"Options"`
}

type helperContainerInspect struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Image  string `json:"Image"`
	Config struct {
		User   string            `json:"User"`
		Labels map[string]string `json:"Labels"`
		Cmd    []string          `json:"Cmd"`
		Env    []string          `json:"Env"`
	} `json:"Config"`
	HostConfig struct {
		Binds          []string `json:"Binds"`
		NetworkMode    string   `json:"NetworkMode"`
		IpcMode        string   `json:"IpcMode"`
		PidMode        string   `json:"PidMode"`
		UTSMode        string   `json:"UTSMode"`
		UsernsMode     string   `json:"UsernsMode"`
		CgroupnsMode   string   `json:"CgroupnsMode"`
		Privileged     bool     `json:"Privileged"`
		CapAdd         []string `json:"CapAdd"`
		CapDrop        []string `json:"CapDrop"`
		SecurityOpt    []string `json:"SecurityOpt"`
		NanoCPUs       int64    `json:"NanoCpus"`
		Memory         int64    `json:"Memory"`
		MemorySwap     int64    `json:"MemorySwap"`
		PidsLimit      int64    `json:"PidsLimit"`
		OomKillDisable *bool    `json:"OomKillDisable"`
		Devices        []any    `json:"Devices"`
		DeviceRequests []any    `json:"DeviceRequests"`
	} `json:"HostConfig"`
	Mounts []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
	} `json:"Mounts"`
	State struct {
		Running bool `json:"Running"`
	} `json:"State"`
}

func validContainerID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// destroySpineVolume runs only after a durable host backup. It rechecks the
// exact ephemeral helper and local volume, removes the helper by immutable ID,
// then removes the derived volume. A failed volume removal leaves all spine
// bytes intact and a later ordinary command can recreate the helper.
func (m *helperManager) destroySpineVolume() error {
	image, exists, err := inspectJSON[helperImageInspect](m.docker, "image", m.imageRef)
	if err != nil || !exists || image.OS != "linux" || image.Architecture != "arm64" || !strings.HasPrefix(image.ID, "sha256:") || len(image.ID) != 71 {
		return fmt.Errorf("reinspect production image before reset: %v", err)
	}
	volume, exists, err := inspectJSON[helperVolumeInspect](m.docker, "volume", m.names.Volume)
	if err != nil || !exists || volume.Name != m.names.Volume || volume.Driver != "local" || len(volume.Options) != 0 {
		return fmt.Errorf("reinspect exact spine volume before reset: exists=%v err=%v", exists, err)
	}
	container, exists, err := inspectJSON[helperContainerInspect](m.docker, "container", m.names.Helper)
	if err != nil || !exists || !m.helperExact(container, image.ID) || !validContainerID(container.ID) {
		return fmt.Errorf("reinspect exact helper before reset: exists=%v err=%v", exists, err)
	}
	if out, err := m.docker.CombinedOutput("container", "rm", "-f", container.ID); err != nil {
		return fmt.Errorf("remove exact helper before volume reset: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if replacement, exists, err := inspectJSON[helperContainerInspect](m.docker, "container", m.names.Helper); err != nil || exists {
		return fmt.Errorf("helper name changed during reset: exists=%v id=%q err=%v", exists, replacement.ID, err)
	}
	volume, exists, err = inspectJSON[helperVolumeInspect](m.docker, "volume", m.names.Volume)
	if err != nil || !exists || volume.Name != m.names.Volume || volume.Driver != "local" || len(volume.Options) != 0 {
		return fmt.Errorf("spine volume changed before reset commit: exists=%v err=%v", exists, err)
	}
	if out, err := m.docker.CombinedOutput("volume", "rm", m.names.Volume); err != nil {
		return fmt.Errorf("remove spine volume after backup: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if _, exists, err := inspectJSON[helperVolumeInspect](m.docker, "volume", m.names.Volume); err != nil || exists {
		return fmt.Errorf("spine volume reset commit is ambiguous: exists=%v err=%v", exists, err)
	}
	return nil
}

// The helper stays active so it can replace the database after this check.
func (m *helperManager) requireDatabaseUsersStopped() error {
	image, exists, err := inspectJSON[helperImageInspect](m.docker, "image", m.imageRef)
	if err != nil || !exists || image.OS != "linux" || image.Architecture != "arm64" || !strings.HasPrefix(image.ID, "sha256:") || len(image.ID) != 71 {
		return fmt.Errorf("inspect database reset image: exists=%v err=%v", exists, err)
	}
	volume, exists, err := inspectJSON[helperVolumeInspect](m.docker, "volume", m.names.Volume)
	if err != nil || !exists || volume.Name != m.names.Volume || volume.Driver != "local" || len(volume.Options) != 0 {
		return fmt.Errorf("inspect exact database reset volume: exists=%v err=%v", exists, err)
	}
	container, exists, err := inspectJSON[helperContainerInspect](m.docker, "container", m.names.Helper)
	if err != nil || !exists || !validContainerID(container.ID) || !m.helperExact(container, image.ID) {
		return fmt.Errorf("inspect exact database reset helper: exists=%v err=%v", exists, err)
	}
	out, err := m.docker.CombinedOutput("container", "ls", "--no-trunc", "--quiet", "--filter", "volume="+m.names.Volume)
	if err != nil {
		return fmt.Errorf("inspect database users: %w", err)
	}
	for _, id := range strings.Fields(string(out)) {
		if id != container.ID {
			return fmt.Errorf("database reset requires stopped containers that use the database volume")
		}
	}
	out, err = m.docker.CombinedOutput("container", "top", container.ID, "-eo", "pid,comm")
	if err != nil {
		return fmt.Errorf("inspect helper processes: %w: %s", err, strings.TrimSpace(string(out)))
	}
	processes := strings.Fields(string(out))
	// Docker requires the PID column. The image starts sleep through tini.
	if len(processes) != 6 || processes[0] != "PID" || processes[1] != "COMMAND" {
		return fmt.Errorf("database reset requires an idle helper")
	}
	counts := map[string]int{}
	for i := 2; i < len(processes); i += 2 {
		pid, err := strconv.Atoi(processes[i])
		if err != nil || pid <= 0 {
			return fmt.Errorf("database reset requires an idle helper")
		}
		counts[processes[i+1]]++
	}
	if counts["tini"] != 1 || counts["sleep"] != 1 {
		return fmt.Errorf("database reset requires an idle helper")
	}
	return nil
}

type helperManager struct {
	docker   dockerCommandRunner
	imageRef string
	names    deployment.Names
	// zone is the zone of the Mac, carried into the helper as TZ. Empty when
	// the host names no zone; the helper then keeps the zone of the image.
	zone string
}

// hostZone names the zone of the Mac: TZ when the environment sets one, else
// the zone that /etc/localtime links to.
func hostZone() string {
	if tz := strings.TrimPrefix(os.Getenv("TZ"), ":"); tz != "" {
		return tz
	}
	target, err := os.Readlink("/etc/localtime")
	if err != nil {
		return ""
	}
	at := strings.LastIndex(target, "zoneinfo/")
	if at < 0 {
		return ""
	}
	return target[at+len("zoneinfo/"):]
}

func productionHelperManager(r dockerCommandRunner) (*helperManager, error) {
	home, err := configuredCanonicalHome()
	if err != nil {
		return nil, err
	}
	names, err := deployment.RuntimeNames(home)
	if err != nil {
		return nil, err
	}
	image, err := configuredProductionImage()
	if err != nil {
		return nil, err
	}
	return &helperManager{docker: r, imageRef: image, names: names, zone: hostZone()}, nil
}

func (m *helperManager) createArgs(imageID string) []string {
	args := []string{
		"container", "create", "--rm",
		"--name", m.names.Helper,
		"--label", "mc-managed=true",
		"--label", "mc-component=helper",
		"--network", "none",
		"--user", "10002:10002",
		"--cap-drop", "ALL",
		"--privileged=false",
		"--ipc", "private",
		"--cpus", "0.5",
		"--memory", "512m",
		"--memory-swap", "512m",
		"--pids-limit", "128",
		"--oom-kill-disable=false",
		"--mount", "type=volume,src=" + m.names.Volume + ",dst=/mc/spine",
	}
	if m.zone != "" {
		args = append(args, "-e", "TZ="+m.zone)
	}
	return append(args, imageID, "/bin/sleep", "infinity")
}

func dockerObjectMissing(output []byte) bool {
	s := strings.ToLower(string(output))
	return strings.Contains(s, "no such image") || strings.Contains(s, "no such volume") || strings.Contains(s, "no such container")
}

func inspectJSON[T any](r dockerCommandRunner, kind, name string) (T, bool, error) {
	var value T
	out, err := r.CombinedOutput(kind, "inspect", "--format", "{{json .}}", name)
	if err != nil {
		if dockerObjectMissing(out) {
			return value, false, nil
		}
		return value, false, fmt.Errorf("docker %s inspect %s: %v: %s", kind, name, err, strings.TrimSpace(string(out)))
	}
	if err := json.Unmarshal(out, &value); err != nil {
		return value, false, fmt.Errorf("decode docker %s inspect %s: %w", kind, name, err)
	}
	return value, true, nil
}

func (m *helperManager) ensure() (string, error) {
	image, exists, err := inspectJSON[helperImageInspect](m.docker, "image", m.imageRef)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("production image %q is missing; build the exact image before provisioning the helper", m.imageRef)
	}
	if image.OS != "linux" || image.Architecture != "arm64" || !strings.HasPrefix(image.ID, "sha256:") || len(image.ID) != 71 {
		return "", fmt.Errorf("production image %q has invalid identity/platform id=%q os=%q arch=%q", m.imageRef, image.ID, image.OS, image.Architecture)
	}

	volume, exists, err := inspectJSON[helperVolumeInspect](m.docker, "volume", m.names.Volume)
	if err != nil {
		return "", err
	}
	if !exists {
		if out, createErr := m.docker.CombinedOutput("volume", "create", m.names.Volume); createErr != nil {
			return "", fmt.Errorf("create spine volume %s: %v: %s", m.names.Volume, createErr, strings.TrimSpace(string(out)))
		}
		volume, exists, err = inspectJSON[helperVolumeInspect](m.docker, "volume", m.names.Volume)
		if err != nil || !exists {
			return "", fmt.Errorf("reinspect created spine volume %s: %v", m.names.Volume, err)
		}
	}
	if volume.Name != m.names.Volume || volume.Driver != "local" || len(volume.Options) != 0 {
		return "", fmt.Errorf("spine volume %s is not the exact local volume (name=%q driver=%q options=%v)", m.names.Volume, volume.Name, volume.Driver, volume.Options)
	}

	container, exists, err := inspectJSON[helperContainerInspect](m.docker, "container", m.names.Helper)
	if err != nil {
		return "", err
	}
	if exists && !helperOwned(container) {
		return "", fmt.Errorf("container name %s is occupied by an unmanaged container; refusing to remove it", m.names.Helper)
	}
	if exists && !m.helperExact(container, image.ID) {
		if out, removeErr := m.docker.CombinedOutput("container", "rm", "-f", m.names.Helper); removeErr != nil {
			return "", fmt.Errorf("replace stale helper %s: %v: %s", m.names.Helper, removeErr, strings.TrimSpace(string(out)))
		}
		exists = false
	}
	if !exists {
		if out, createErr := m.docker.CombinedOutput(m.createArgs(image.ID)...); createErr != nil {
			return "", fmt.Errorf("create helper %s: %v: %s", m.names.Helper, createErr, strings.TrimSpace(string(out)))
		}
		container, exists, err = inspectJSON[helperContainerInspect](m.docker, "container", m.names.Helper)
		if err != nil || !exists || !m.helperExact(container, image.ID) {
			return "", fmt.Errorf("created helper %s failed exact inspection: %v", m.names.Helper, err)
		}
	}
	if !container.State.Running {
		if out, startErr := m.docker.CombinedOutput("container", "start", m.names.Helper); startErr != nil {
			return "", fmt.Errorf("start helper %s: %v: %s", m.names.Helper, startErr, strings.TrimSpace(string(out)))
		}
		container, exists, err = inspectJSON[helperContainerInspect](m.docker, "container", m.names.Helper)
		if err != nil || !exists || !container.State.Running || !m.helperExact(container, image.ID) {
			return "", fmt.Errorf("started helper %s failed exact inspection: %v", m.names.Helper, err)
		}
	}
	return m.names.Helper, nil
}

func helperOwned(c helperContainerInspect) bool {
	return c.Config.Labels["mc-managed"] == "true" && c.Config.Labels["mc-component"] == "helper"
}

func helperLabelsExact(labels map[string]string) bool {
	if labels["mc-managed"] != "true" || labels["mc-component"] != "helper" {
		return false
	}
	for key := range labels {
		if strings.HasPrefix(key, "mc-") && key != "mc-managed" && key != "mc-component" {
			return false
		}
	}
	return true
}

// helperZoneExact holds when the container's TZ is the zone of the Mac, so a
// helper created under another zone is replaced at the next ensure.
func helperZoneExact(env []string, zone string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, "TZ=") {
			return zone != "" && kv == "TZ="+zone
		}
	}
	return zone == ""
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (m *helperManager) helperExact(c helperContainerInspect, imageID string) bool {
	oomDisabled := c.HostConfig.OomKillDisable == nil || !*c.HostConfig.OomKillDisable
	return strings.TrimPrefix(c.Name, "/") == m.names.Helper && c.Image == imageID &&
		c.Config.User == "10002:10002" && stringSliceEqual(c.Config.Cmd, []string{"/bin/sleep", "infinity"}) &&
		helperLabelsExact(c.Config.Labels) &&
		c.HostConfig.NetworkMode == "none" && c.HostConfig.IpcMode == "private" && c.HostConfig.PidMode == "" &&
		c.HostConfig.UTSMode == "" && c.HostConfig.UsernsMode == "" && c.HostConfig.CgroupnsMode == "private" &&
		!c.HostConfig.Privileged && len(c.HostConfig.Binds) == 0 && len(c.HostConfig.CapAdd) == 0 &&
		stringSliceEqual(c.HostConfig.CapDrop, []string{"ALL"}) && len(c.HostConfig.SecurityOpt) == 0 &&
		c.HostConfig.NanoCPUs == 500000000 && c.HostConfig.Memory == 536870912 &&
		c.HostConfig.MemorySwap == 536870912 && c.HostConfig.PidsLimit == 128 && oomDisabled &&
		len(c.HostConfig.Devices) == 0 && len(c.HostConfig.DeviceRequests) == 0 &&
		len(c.Mounts) == 1 && c.Mounts[0].Type == "volume" && c.Mounts[0].Name == m.names.Volume &&
		c.Mounts[0].Destination == "/mc/spine" && c.Mounts[0].RW &&
		helperZoneExact(c.Config.Env, m.zone)
}

func ensureRuntimeHelper() (string, error) {
	m, err := productionHelperManager(execDockerRunner{})
	if err != nil {
		return "", err
	}
	return m.ensure()
}
