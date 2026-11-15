//go:build !test_fake_routing

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc/deployment"
	"mc/substrate"
	"mc/verbs"
)

func TestRedeployCheckReadsTheDeployedDatabaseReadOnly(t *testing.T) {
	const uuid = "0123456789abcdef0123456789abcdef"
	image := helperImageInspect{ID: "sha256:" + strings.Repeat("a", 64), OS: "linux", Architecture: "arm64"}
	report := func(state, deployment string) string {
		return inspectBytes(t, verbs.DeployedDatabase{ProtocolVersion: 1, SchemaID: substrate.CurrentSchemaID, State: state, DeploymentUUID: deployment})
	}
	failure := errors.New("exit status 1")
	for _, scenario := range []struct {
		name      string
		mirror    bool
		volume    bool
		image     bool
		runs      []dockerStep
		want      string
		wantError string
		usage     bool
	}{
		{name: "current", mirror: true, volume: true, image: true, runs: []dockerStep{{out: report("current", uuid)}}, want: "current"},
		{name: "differs", mirror: true, volume: true, image: true, runs: []dockerStep{{out: report("differs", uuid)}}, want: "differs"},
		{name: "busy then current", mirror: true, volume: true, image: true, runs: []dockerStep{{out: "database is locked", err: failure}, {out: report("current", uuid)}}, want: "current"},
		{name: "empty volume", mirror: true, volume: true, image: true, runs: []dockerStep{{out: report("absent", "")}}, want: "absent"},
		{name: "absent volume", mirror: true, want: "absent"},
		{name: "new deployment", want: "new"},
		{name: "repair mirror", volume: true, image: true, runs: []dockerStep{{out: report("current", uuid)}}, want: "current"},
		{name: "identity differs", mirror: true, volume: true, image: true, runs: []dockerStep{{out: report("current", strings.Repeat("f", 32))}}, wantError: "identities differ"},
		{name: "image absent", mirror: true, volume: true, wantError: "unavailable for the database check", usage: true},
		{name: "unreadable database", mirror: true, volume: true, image: true, runs: []dockerStep{{out: "unable to open", err: failure}, {out: "unable to open", err: failure}, {out: "unable to open", err: failure}}, wantError: "could not read the database", usage: true},
		{name: "another release", mirror: true, volume: true, image: true, runs: []dockerStep{{out: `{"protocol_version":1,"schema_id":"other","state":"current"}`}}, wantError: "invalid release identity", usage: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			home := t.TempDir()
			if scenario.mirror {
				if err := os.WriteFile(filepath.Join(home, "deployment.uuid"), []byte(uuid+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			names, err := deployment.RuntimeNames(home)
			if err != nil {
				t.Fatal(err)
			}
			linuxMC := filepath.Join(home, "mc-linux")
			steps := []dockerStep{{want: []string{"volume", "inspect", "--format", "{{json .}}", names.Volume}, out: "Error: No such volume: " + names.Volume, err: failure}}
			if scenario.volume {
				steps[0] = dockerStep{want: steps[0].want, out: inspectBytes(t, helperVolumeInspect{Name: names.Volume, Driver: "local"})}
				inspect := dockerStep{want: []string{"image", "inspect", "--format", "{{json .}}", productionImageRef}, out: "Error: No such image: " + productionImageRef, err: failure}
				if scenario.image {
					inspect.out, inspect.err = inspectBytes(t, image), nil
				}
				steps = append(steps, inspect)
			}
			run := []string{"run", "--rm", "--pids-limit=2048", "--network", "none", "--cap-drop", "ALL", "--user", "10001:10001",
				"--security-opt", "no-new-privileges",
				"--mount", "type=volume,src=" + names.Volume + ",dst=/mc/spine,readonly",
				"--mount", "type=bind,src=" + linuxMC + ",dst=/mc/check/mc,readonly",
				"--entrypoint", "/mc/check/mc", image.ID, "__database-schema"}
			for _, step := range scenario.runs {
				step.want = run
				steps = append(steps, step)
			}
			runner := &scriptedDocker{t: t, steps: steps}
			sleeps := 0
			got, err := checkRedeployDatabase(runner, home, productionImageRef, linuxMC, func(time.Duration) { sleeps++ })
			if runner.calls != len(steps) {
				t.Fatalf("docker calls = %d, want %d", runner.calls, len(steps))
			}
			if want := max(len(scenario.runs)-1, 0); sleeps != want {
				t.Fatalf("retries = %d, want %d", sleeps, want)
			}
			if scenario.wantError != "" {
				var usage *verbs.UsageError
				if err == nil || !strings.Contains(err.Error(), scenario.wantError) || errors.As(err, &usage) != scenario.usage {
					t.Fatalf("check = %+v, %v; want error %q (usage %v)", got, err, scenario.wantError, scenario.usage)
				}
				return
			}
			if err != nil || got.State != scenario.want {
				t.Fatalf("check = %+v, %v; want %s", got, err, scenario.want)
			}
		})
	}
}
