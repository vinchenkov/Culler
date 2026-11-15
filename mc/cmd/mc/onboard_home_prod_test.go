//go:build !test_fake_routing

package main

import (
	"errors"
	"mc/verbs"
	"strings"
	"testing"

	"mc/deployment"
)

func TestDatabaseResetRequiresStoppedUsersAndExactTarget(t *testing.T) {
	names, err := deployment.RuntimeNames("/Users/alice/.culler")
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"idle", "active runner", "active helper", "wrong volume", "wrong helper", "wrong image"} {
		t.Run(scenario, func(t *testing.T) {
			volume := helperVolumeInspect{Name: names.Volume, Driver: "local"}
			helper := exactHelperFixture(names, "sha256:"+strings.Repeat("a", 64))
			if scenario == "wrong volume" {
				volume.Name = "another-volume"
			}
			if scenario == "wrong image" {
				helper.Image = "sha256:" + strings.Repeat("b", 64)
			}
			if scenario == "wrong helper" {
				helper.Config.User = "root"
			}
			image := helperImageInspect{ID: "sha256:" + strings.Repeat("a", 64), OS: "linux", Architecture: "arm64"}
			steps := []dockerStep{{want: []string{"image", "inspect", "--format", "{{json .}}", productionImageRef}, out: inspectBytes(t, image)}, {want: []string{"volume", "inspect", "--format", "{{json .}}", names.Volume}, out: inspectBytes(t, volume)}}
			if scenario != "wrong volume" {
				steps = append(steps, dockerStep{want: []string{"container", "inspect", "--format", "{{json .}}", names.Helper}, out: inspectBytes(t, helper)})
			}
			if scenario != "wrong volume" && scenario != "wrong helper" && scenario != "wrong image" {
				users := helper.ID
				if scenario == "active runner" {
					users += "\n" + strings.Repeat("d", 64)
				}
				steps = append(steps, dockerStep{want: []string{"container", "ls", "--no-trunc", "--quiet", "--filter", "volume=" + names.Volume}, out: users})
				if scenario != "active runner" {
					processes := "PID COMMAND\n100 tini\n101 sleep\n"
					if scenario == "active helper" {
						processes += "102 mc\n"
					}
					steps = append(steps, dockerStep{want: []string{"container", "top", helper.ID, "-eo", "pid,comm"}, out: processes})
				}
			}
			runner := &scriptedDocker{t: t, steps: steps}
			manager := helperManager{docker: runner, names: names, imageRef: productionImageRef}
			err := manager.requireDatabaseUsersStopped()
			if (err == nil) != (scenario == "idle") {
				t.Fatalf("database reset check: %v", err)
			}
			if runner.calls != len(steps) {
				t.Fatalf("docker calls = %d, want %d", runner.calls, len(steps))
			}
		})
	}
}

func TestOnboardResetRequestFollowsTheStoppedUserCheck(t *testing.T) {
	for _, scenario := range []string{"keep", "reset", "stop failure"} {
		t.Run(scenario, func(t *testing.T) {
			calls, stops := 0, 0
			result, err := provisionOnboardSpine(verbs.OnboardSpineRequest{AllowReset: true, Reset: true}, scenario != "keep",
				func(req verbs.OnboardSpineRequest) (verbs.OnboardSpineResult, error) {
					calls++
					if req.AllowReset || req.Reset != (scenario == "reset") {
						t.Fatalf("reset request = %+v", req)
					}
					if req.Reset && stops != 1 {
						t.Fatal("the reset request preceded the stopped-user check")
					}
					return verbs.OnboardSpineResult{Status: "ok"}, nil
				}, func() error {
					stops++
					if scenario == "stop failure" {
						return errors.New("active database user")
					}
					return nil
				})
			wantCalls, wantStops := 1, 0
			if scenario == "reset" {
				wantStops = 1
			}
			if scenario == "stop failure" {
				wantCalls, wantStops = 0, 1
			}
			if calls != wantCalls || stops != wantStops || (err != nil) != (scenario == "stop failure") {
				t.Fatalf("calls=%d stops=%d result=%v error=%v", calls, stops, result, err)
			}
		})
	}
}

func TestDatabaseResetRemovesOnlyManagedUsersOtherThanTheHelper(t *testing.T) {
	names, err := deployment.RuntimeNames("/Users/alice/.culler")
	if err != nil {
		t.Fatal(err)
	}
	helper := exactHelperFixture(names, "sha256:"+strings.Repeat("a", 64))
	collie, runner := strings.Repeat("d", 64), strings.Repeat("e", 64)
	list := []string{"container", "ls", "--no-trunc", "--quiet", "--filter", "volume=" + names.Volume, "--filter", "label=mc-managed=true"}
	inspect := dockerStep{want: []string{"container", "inspect", "--format", "{{json .}}", names.Helper}, out: inspectBytes(t, helper)}
	for _, scenario := range []string{"users", "helper only", "invalid identity", "remove failure"} {
		t.Run(scenario, func(t *testing.T) {
			steps := []dockerStep{inspect}
			switch scenario {
			case "users":
				steps = append(steps,
					dockerStep{want: list, out: helper.ID + "\n" + collie + "\n" + runner + "\n"},
					dockerStep{want: []string{"container", "rm", "--force", collie}},
					dockerStep{want: []string{"container", "rm", "--force", runner}})
			case "helper only":
				steps = append(steps, dockerStep{want: list, out: helper.ID + "\n"})
			case "invalid identity":
				steps = append(steps, dockerStep{want: list, out: "not-a-container\n"})
			case "remove failure":
				steps = append(steps,
					dockerStep{want: list, out: collie + "\n"},
					dockerStep{want: []string{"container", "rm", "--force", collie}, err: errors.New("exit status 1")})
			}
			runner := &scriptedDocker{t: t, steps: steps}
			err := removeManagedDatabaseUsers(&helperManager{docker: runner, names: names, imageRef: productionImageRef})
			wantError := scenario == "invalid identity" || scenario == "remove failure"
			if (err != nil) != wantError {
				t.Fatalf("remove managed users: %v", err)
			}
			if runner.calls != len(steps) {
				t.Fatalf("docker calls = %d, want %d", runner.calls, len(steps))
			}
		})
	}
}

func TestReleaseUpdateRemovesAllManagedContainersAndPreservesTheHelper(t *testing.T) {
	names, err := deployment.RuntimeNames("/Users/alice/.culler")
	if err != nil {
		t.Fatal(err)
	}
	helper := exactHelperFixture(names, "sha256:"+strings.Repeat("a", 64))
	collie, setup := strings.Repeat("d", 64), strings.Repeat("e", 64)
	list := func(volume string) []string {
		return []string{"container", "ls", "--all", "--no-trunc", "--quiet", "--filter", "volume=" + volume,
			"--filter", "label=mc-managed=true"}
	}
	for _, scenario := range []string{"created and active", "invalid identity", "remove failure"} {
		t.Run(scenario, func(t *testing.T) {
			steps := []dockerStep{
				{want: []string{"container", "inspect", "--format", "{{json .}}", names.Helper}, out: inspectBytes(t, helper)},
				{want: list(names.Volume), out: helper.ID + "\n" + collie + "\n"},
			}
			if scenario == "invalid identity" {
				steps = append(steps, dockerStep{want: list(names.RuntimeVolume), out: "not-a-container\n"})
			} else {
				steps = append(steps, dockerStep{want: list(names.RuntimeVolume), out: collie + "\n" + setup + "\n"})
				step := dockerStep{want: []string{"container", "rm", "--force", collie}}
				if scenario == "remove failure" {
					step.err = errors.New("exit status 1")
				}
				steps = append(steps, step)
				if scenario != "remove failure" {
					steps = append(steps, dockerStep{want: []string{"container", "rm", "--force", setup}})
				}
			}
			runner := &scriptedDocker{t: t, steps: steps}
			err := removeManagedReleaseUsers(&helperManager{docker: runner, names: names, imageRef: productionImageRef})
			if (err != nil) != (scenario != "created and active") {
				t.Fatalf("remove previous release users: %v", err)
			}
			if runner.calls != len(steps) {
				t.Fatalf("docker calls = %d, want %d", runner.calls, len(steps))
			}
		})
	}
}
