//go:build !test_fake_routing

package main

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"mc/deployment"
)

type dockerStep struct {
	want []string
	out  string
	err  error
}

type scriptedDocker struct {
	t     *testing.T
	steps []dockerStep
	calls int
}

func (s *scriptedDocker) CombinedOutput(args ...string) ([]byte, error) {
	s.t.Helper()
	if s.calls >= len(s.steps) {
		s.t.Fatalf("unexpected docker call: %#v", args)
	}
	step := s.steps[s.calls]
	s.calls++
	if !reflect.DeepEqual(args, step.want) {
		s.t.Fatalf("docker call %d:\n got %#v\nwant %#v", s.calls, args, step.want)
	}
	return []byte(step.out), step.err
}

func inspectBytes(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestHelperCreateArgsPinLeastPrivilegeEnvelope(t *testing.T) {
	names, err := deployment.RuntimeNames("/Users/alice/.culler")
	if err != nil {
		t.Fatal(err)
	}
	m := helperManager{names: names}
	imageID := "sha256:" + strings.Repeat("a", 64)
	want := []string{
		"container", "create", "--rm", "--name", names.Helper,
		"--label", "mc-managed=true", "--label", "mc-component=helper", "--label", "mc-deployment=" + names.Suffix,
		"--network", "none", "--user", "10002:10002", "--cap-drop", "ALL",
		"--privileged=false", "--ipc", "private", "--cpus", "0.5",
		"--memory", "512m", "--memory-swap", "512m", "--pids-limit", "128",
		"--oom-kill-disable=false", "--mount", "type=volume,src=" + names.Volume + ",dst=/mc/spine",
		imageID, "/bin/sleep", "infinity",
	}
	if got := m.createArgs(imageID); !reflect.DeepEqual(got, want) {
		t.Fatalf("create argv:\n got %#v\nwant %#v", got, want)
	}
	joined := strings.Join(want, " ")
	for _, forbidden := range []string{"MC_HOME", "MC_SPINE", "MC_RUN_JSON", "mc-tier", "docker.sock", "--cap-add", "no-new-privileges", "type=bind"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("helper argv carries forbidden %q: %s", forbidden, joined)
		}
	}
}

func TestHelperCreateArgsCarryTheHostZone(t *testing.T) {
	names, err := deployment.RuntimeNames("/Users/alice/.culler")
	if err != nil {
		t.Fatal(err)
	}
	m := helperManager{names: names, zone: "Asia/Tokyo"}
	imageID := "sha256:" + strings.Repeat("a", 64)
	got := m.createArgs(imageID)
	want := []string{"-e", "TZ=Asia/Tokyo", imageID, "/bin/sleep", "infinity"}
	if tail := got[len(got)-len(want):]; !reflect.DeepEqual(tail, want) {
		t.Fatalf("create argv tail:\n got %#v\nwant %#v", tail, want)
	}
	if !reflect.DeepEqual(got[:len(got)-len(want)], (&helperManager{names: names}).createArgs(imageID)[:len(got)-len(want)]) {
		t.Fatalf("the zone changed more than the environment: %#v", got)
	}
}

func TestHelperExactRequiresTheHostZone(t *testing.T) {
	names, err := deployment.RuntimeNames("/Users/alice/.culler")
	if err != nil {
		t.Fatal(err)
	}
	imageID := "sha256:" + strings.Repeat("a", 64)
	plain := exactHelperFixture(names, imageID)
	zoned := exactHelperFixture(names, imageID)
	zoned.Config.Env = []string{"PATH=/usr/bin", "TZ=Asia/Tokyo"}
	cases := []struct {
		name string
		zone string
		c    helperContainerInspect
		want bool
	}{
		{"no zone, no TZ", "", plain, true},
		{"no zone, stale TZ", "", zoned, false},
		{"zone, no TZ", "Asia/Tokyo", plain, false},
		{"zone, same TZ", "Asia/Tokyo", zoned, true},
		{"zone, other TZ", "Europe/Paris", zoned, false},
	}
	for _, tc := range cases {
		m := helperManager{names: names, zone: tc.zone}
		if got := m.helperExact(tc.c, imageID); got != tc.want {
			t.Fatalf("%s: helperExact = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestHostZoneReadsTZThenTheLocaltimeLink(t *testing.T) {
	t.Setenv("TZ", ":Europe/Paris")
	if got := hostZone(); got != "Europe/Paris" {
		t.Fatalf("hostZone with TZ set = %q, want Europe/Paris", got)
	}
	t.Setenv("TZ", "")
	target, err := os.Readlink("/etc/localtime")
	if err != nil {
		if got := hostZone(); got != "" {
			t.Fatalf("hostZone with no link = %q, want empty", got)
		}
		return
	}
	at := strings.LastIndex(target, "zoneinfo/")
	want := ""
	if at >= 0 {
		want = target[at+len("zoneinfo/"):]
	}
	if got := hostZone(); got != want {
		t.Fatalf("hostZone from %s = %q, want %q", target, got, want)
	}
}

func exactHelperFixture(names deployment.Names, imageID string) helperContainerInspect {
	var c helperContainerInspect
	c.ID = strings.Repeat("c", 64)
	c.Name = "/" + names.Helper
	c.Image = imageID
	c.Config.User = "10002:10002"
	c.Config.Labels = map[string]string{"mc-managed": "true", "mc-component": "helper", "mc-deployment": names.Suffix}
	c.Config.Cmd = []string{"/bin/sleep", "infinity"}
	c.HostConfig.NetworkMode = "none"
	c.HostConfig.IpcMode = "private"
	c.HostConfig.CgroupnsMode = "private"
	c.HostConfig.CapDrop = []string{"ALL"}
	c.HostConfig.NanoCPUs = 500000000
	c.HostConfig.Memory = 536870912
	c.HostConfig.MemorySwap = 536870912
	c.HostConfig.PidsLimit = 128
	c.Mounts = append(c.Mounts, struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
	}{Type: "volume", Name: names.Volume, Destination: "/mc/spine", RW: true})
	return c
}

func TestHelperDatabaseResetRequiresIdleProcesses(t *testing.T) {
	names, _ := deployment.RuntimeNames("/Users/alice/.culler")
	imageID := "sha256:" + strings.Repeat("a", 64)
	image := helperImageInspect{ID: imageID, OS: "linux", Architecture: "arm64"}
	volume := helperVolumeInspect{Name: names.Volume, Driver: "local"}
	helper := exactHelperFixture(names, imageID)
	helper.State.Running = true
	for _, tc := range []struct {
		name string
		out  string
		err  error
		ok   bool
	}{
		{name: "idle image", out: "PID COMMAND\n4979 tini\n4992 sleep\n", ok: true},
		{name: "active command", out: "PID COMMAND\n4979 tini\n4992 sleep\n5100 mc\n"},
		{name: "unexpected command", out: "PID COMMAND\n4979 tini\n4992 mc\n"},
		{name: "missing process", out: "PID COMMAND\n4979 tini\n"},
		{name: "duplicate process", out: "PID COMMAND\n4979 sleep\n4992 sleep\n"},
		{name: "invalid identifier", out: "PID COMMAND\ninvalid tini\n4992 sleep\n"},
		{name: "missing identifier column", out: "COMMAND\ntini\nsleep\n"},
		{name: "docker failure", out: "process inspection failed", err: errors.New("exit 1")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &scriptedDocker{t: t, steps: []dockerStep{
				{want: []string{"image", "inspect", "--format", "{{json .}}", productionImageRef}, out: inspectBytes(t, image)},
				{want: []string{"volume", "inspect", "--format", "{{json .}}", names.Volume}, out: inspectBytes(t, volume)},
				{want: []string{"container", "inspect", "--format", "{{json .}}", names.Helper}, out: inspectBytes(t, helper)},
				{want: []string{"container", "ls", "--no-trunc", "--quiet", "--filter", "volume=" + names.Volume}, out: helper.ID},
				{want: []string{"container", "top", helper.ID, "-eo", "pid,comm"}, out: tc.out, err: tc.err},
			}}
			m := helperManager{docker: runner, imageRef: productionImageRef, names: names}
			if err := m.requireDatabaseUsersStopped(); (err == nil) != tc.ok {
				t.Fatalf("database reset check = %v, want success=%v", err, tc.ok)
			}
			if runner.calls != len(runner.steps) {
				t.Fatalf("docker calls=%d want=%d", runner.calls, len(runner.steps))
			}
		})
	}
}

func TestHelperDestroySpineVolumeRechecksAndCommitsInOrder(t *testing.T) {
	names, _ := deployment.RuntimeNames("/Users/alice/.culler")
	imageID := "sha256:" + strings.Repeat("a", 64)
	image := helperImageInspect{ID: imageID, OS: "linux", Architecture: "arm64"}
	volume := helperVolumeInspect{Name: names.Volume, Driver: "local"}
	helper := exactHelperFixture(names, imageID)
	helper.State.Running = true
	runner := &scriptedDocker{t: t, steps: []dockerStep{
		{want: []string{"image", "inspect", "--format", "{{json .}}", productionImageRef}, out: inspectBytes(t, image)},
		{want: []string{"volume", "inspect", "--format", "{{json .}}", names.Volume}, out: inspectBytes(t, volume)},
		{want: []string{"container", "inspect", "--format", "{{json .}}", names.Helper}, out: inspectBytes(t, helper)},
		{want: []string{"container", "rm", "-f", helper.ID}, out: helper.ID},
		{want: []string{"container", "inspect", "--format", "{{json .}}", names.Helper}, out: "No such container", err: errors.New("exit 1")},
		{want: []string{"volume", "inspect", "--format", "{{json .}}", names.Volume}, out: inspectBytes(t, volume)},
		{want: []string{"volume", "rm", names.Volume}, out: names.Volume},
		{want: []string{"volume", "inspect", "--format", "{{json .}}", names.Volume}, out: "No such volume", err: errors.New("exit 1")},
	}}
	m := helperManager{docker: runner, imageRef: productionImageRef, names: names}
	if err := m.destroySpineVolume(); err != nil {
		t.Fatal(err)
	}
	if runner.calls != len(runner.steps) {
		t.Fatalf("docker calls=%d want=%d", runner.calls, len(runner.steps))
	}
}

func TestHelperDestroySpineVolumeRefusesMutableContainerTarget(t *testing.T) {
	names, _ := deployment.RuntimeNames("/Users/alice/.culler")
	imageID := "sha256:" + strings.Repeat("a", 64)
	image := helperImageInspect{ID: imageID, OS: "linux", Architecture: "arm64"}
	volume := helperVolumeInspect{Name: names.Volume, Driver: "local"}
	helper := exactHelperFixture(names, imageID)
	helper.ID = ""
	runner := &scriptedDocker{t: t, steps: []dockerStep{
		{want: []string{"image", "inspect", "--format", "{{json .}}", productionImageRef}, out: inspectBytes(t, image)},
		{want: []string{"volume", "inspect", "--format", "{{json .}}", names.Volume}, out: inspectBytes(t, volume)},
		{want: []string{"container", "inspect", "--format", "{{json .}}", names.Helper}, out: inspectBytes(t, helper)},
	}}
	m := helperManager{docker: runner, imageRef: productionImageRef, names: names}
	if err := m.destroySpineVolume(); err == nil || !strings.Contains(err.Error(), "exact helper") {
		t.Fatalf("mutable target accepted: %v", err)
	}
	if runner.calls != len(runner.steps) {
		t.Fatalf("refusal mutated Docker state: calls=%d", runner.calls)
	}
}

func TestHelperExactRejectsAuthorityWidening(t *testing.T) {
	names, _ := deployment.RuntimeNames("/Users/alice/.culler")
	m := helperManager{names: names}
	imageID := "sha256:" + strings.Repeat("a", 64)
	base := exactHelperFixture(names, imageID)
	base.Config.Labels["org.opencontainers.image.version"] = "24.04"
	if !m.helperExact(base, imageID) {
		t.Fatal("exact helper fixture with inherited image metadata was rejected")
	}

	cases := map[string]func(*helperContainerInspect){
		"root user":        func(c *helperContainerInspect) { c.Config.User = "0:0" },
		"tier label":       func(c *helperContainerInspect) { c.Config.Labels["mc-tier"] = "pipeline" },
		"host network":     func(c *helperContainerInspect) { c.HostConfig.NetworkMode = "host" },
		"host pid":         func(c *helperContainerInspect) { c.HostConfig.PidMode = "host" },
		"host userns":      func(c *helperContainerInspect) { c.HostConfig.UsernsMode = "host" },
		"privileged":       func(c *helperContainerInspect) { c.HostConfig.Privileged = true },
		"cap add":          func(c *helperContainerInspect) { c.HostConfig.CapAdd = []string{"CHOWN"} },
		"NNP":              func(c *helperContainerInspect) { c.HostConfig.SecurityOpt = []string{"no-new-privileges:true"} },
		"host bind":        func(c *helperContainerInspect) { c.HostConfig.Binds = []string{"/tmp:/tmp"} },
		"extra mount":      func(c *helperContainerInspect) { c.Mounts = append(c.Mounts, c.Mounts[0]) },
		"wrong image":      func(c *helperContainerInspect) { c.Image = "sha256:" + strings.Repeat("b", 64) },
		"unbounded memory": func(c *helperContainerInspect) { c.HostConfig.Memory = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := exactHelperFixture(names, imageID)
			mutate(&c)
			if m.helperExact(c, imageID) {
				t.Fatalf("widened helper was accepted: %#v", c)
			}
		})
	}
}

func TestHelperEnsureCreatesAndStartsOnlyExactEnvelope(t *testing.T) {
	names, _ := deployment.RuntimeNames("/Users/alice/.culler")
	imageID := "sha256:" + strings.Repeat("a", 64)
	image := helperImageInspect{ID: imageID, OS: "linux", Architecture: "arm64"}
	volume := helperVolumeInspect{Name: names.Volume, Driver: "local"}
	created := exactHelperFixture(names, imageID)
	running := exactHelperFixture(names, imageID)
	running.State.Running = true
	m := helperManager{imageRef: productionImageRef, names: names}
	runner := &scriptedDocker{t: t, steps: []dockerStep{
		{want: []string{"image", "inspect", "--format", "{{json .}}", productionImageRef}, out: inspectBytes(t, image)},
		{want: []string{"volume", "inspect", "--format", "{{json .}}", names.Volume}, out: "Error: No such volume", err: errors.New("exit 1")},
		{want: []string{"volume", "create", names.Volume}, out: names.Volume},
		{want: []string{"volume", "inspect", "--format", "{{json .}}", names.Volume}, out: inspectBytes(t, volume)},
		{want: []string{"container", "inspect", "--format", "{{json .}}", names.Helper}, out: "Error: No such container", err: errors.New("exit 1")},
		{want: m.createArgs(imageID), out: "container-id"},
		{want: []string{"container", "inspect", "--format", "{{json .}}", names.Helper}, out: inspectBytes(t, created)},
		{want: []string{"container", "start", names.Helper}, out: names.Helper},
		{want: []string{"container", "inspect", "--format", "{{json .}}", names.Helper}, out: inspectBytes(t, running)},
	}}
	m.docker = runner
	got, err := m.ensure()
	if err != nil || got != names.Helper {
		t.Fatalf("ensure helper = %q, %v", got, err)
	}
	if runner.calls != len(runner.steps) {
		t.Fatalf("docker calls = %d, want %d", runner.calls, len(runner.steps))
	}
}

func TestHelperEnsureMissingOrWrongImageMutatesNothing(t *testing.T) {
	names, _ := deployment.RuntimeNames("/Users/alice/.culler")
	for name, image := range map[string]helperImageInspect{
		"wrong architecture": {ID: "sha256:" + strings.Repeat("a", 64), OS: "linux", Architecture: "amd64"},
		"malformed id":       {ID: "latest", OS: "linux", Architecture: "arm64"},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &scriptedDocker{t: t, steps: []dockerStep{{
				want: []string{"image", "inspect", "--format", "{{json .}}", productionImageRef}, out: inspectBytes(t, image),
			}}}
			m := helperManager{docker: runner, imageRef: productionImageRef, names: names}
			if _, err := m.ensure(); err == nil {
				t.Fatal("invalid image was accepted")
			}
			if runner.calls != 1 {
				t.Fatalf("invalid image caused %d Docker calls", runner.calls)
			}
		})
	}
}

func TestHelperEnsurePreservesUnmanagedNameCollision(t *testing.T) {
	names, _ := deployment.RuntimeNames("/Users/alice/.culler")
	imageID := "sha256:" + strings.Repeat("a", 64)
	image := helperImageInspect{ID: imageID, OS: "linux", Architecture: "arm64"}
	volume := helperVolumeInspect{Name: names.Volume, Driver: "local"}
	foreign := exactHelperFixture(names, imageID)
	foreign.Config.Labels = map[string]string{"somebody": "else"}
	runner := &scriptedDocker{t: t, steps: []dockerStep{
		{want: []string{"image", "inspect", "--format", "{{json .}}", productionImageRef}, out: inspectBytes(t, image)},
		{want: []string{"volume", "inspect", "--format", "{{json .}}", names.Volume}, out: inspectBytes(t, volume)},
		{want: []string{"container", "inspect", "--format", "{{json .}}", names.Helper}, out: inspectBytes(t, foreign)},
	}}
	m := helperManager{docker: runner, imageRef: productionImageRef, names: names}
	if _, err := m.ensure(); err == nil || !strings.Contains(err.Error(), "unmanaged") {
		t.Fatalf("collision error = %v", err)
	}
	if runner.calls != len(runner.steps) {
		t.Fatalf("collision caused a mutation: calls=%d", runner.calls)
	}
}
