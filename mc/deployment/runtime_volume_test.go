package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestRuntimeVolumeInstallsOnlyAfterUsersStop(t *testing.T) {
	for _, active := range []bool{true, false} {
		t.Run(map[bool]string{true: "active", false: "stopped"}[active], func(t *testing.T) {
			names, _ := RuntimeNames("/Users/test/.culler")
			copied := false
			var calls [][]string
			run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
				calls = append(calls, args)
				switch args[0] + " " + args[1] {
				case "volume inspect":
					return json.Marshal(map[string]any{"Name": names.RuntimeVolume, "Driver": "local", "Labels": map[string]string{"mc-component": "runtime", "mc-deployment": names.Suffix}})
				case "container ls":
					if active {
						return []byte("active-container"), nil
					}
					return nil, nil
				case "run --rm":
					joined := strings.Join(args, " ")
					if strings.Contains(joined, "dst=/runtime-output") {
						copied = true
						if !strings.Contains(joined, "volume-nocopy") || strings.Contains(joined, "mc/spine") {
							t.Fatal(joined)
						}
						return nil, nil
					}
					if !strings.Contains(joined, "dst="+RuntimeRoot+",readonly,volume-nocopy") {
						t.Fatal(joined)
					}
					if copied {
						return []byte("new-release\n"), nil
					}
					return []byte("old-release\n"), nil
				}
				return nil, errors.New("unexpected Docker operation")
			}
			err := InstallRuntimeVolume(context.Background(), "/Users/test/.culler", "service-image", "new-release", run)
			if active && (err == nil || copied) {
				t.Fatalf("active runtime changed: %v", err)
			}
			if !active && (err != nil || !copied) {
				t.Fatalf("stopped runtime did not update: %v", err)
			}
			for _, call := range calls {
				if strings.Contains(strings.Join(call, " "), "project-image") {
					t.Fatal("runtime update changed a Project image")
				}
			}
		})
	}
}

// The install program reaches the runtime Node.js through standard input.
// Every other docker command runs with closed standard input.
func TestRuntimeVolumeInstallRunsTheEmbeddedProgram(t *testing.T) {
	names, _ := RuntimeNames("/Users/test/.culler")
	installs := 0
	var installArgs []string
	installStdin := ""
	run := func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		switch args[0] + " " + args[1] {
		case "volume inspect":
			if stdin != "" {
				t.Fatalf("volume inspect received standard input: %q", stdin)
			}
			return json.Marshal(map[string]any{"Name": names.RuntimeVolume, "Driver": "local", "Labels": map[string]string{"mc-component": "runtime", "mc-deployment": names.Suffix}})
		case "container ls":
			if stdin != "" {
				t.Fatalf("container ls received standard input: %q", stdin)
			}
			return nil, nil
		case "run --rm":
			if strings.Contains(strings.Join(args, " "), "dst=/runtime-output") {
				installs++
				installArgs = args
				installStdin = stdin
				return []byte(`{"release":"new-release","written":1,"unchanged":2,"hashed":0,"removed":0}` + "\n"), nil
			}
			if stdin != "" {
				t.Fatalf("the probe received standard input: %q", stdin)
			}
			if installs > 0 {
				return []byte("new-release\n"), nil
			}
			return []byte("old-release\n"), nil
		}
		return nil, errors.New("unexpected Docker operation")
	}
	if err := InstallRuntimeVolume(context.Background(), "/Users/test/.culler", "product-image", "new-release", run); err != nil {
		t.Fatal(err)
	}
	want := []string{"run", "--rm", "-i", "--label", "mc-deployment=" + names.Suffix, "--network", "none", "--user", "0:0", "--mount",
		"type=volume,src=" + names.RuntimeVolume + ",dst=/runtime-output,volume-nocopy", "product-image",
		"/opt/culler-runtime/node/bin/node", "-", "/opt/culler-runtime", "/runtime-output", "new-release"}
	if installs != 1 || !slices.Equal(installArgs, want) {
		t.Fatalf("install calls %d with arguments %q", installs, installArgs)
	}
	if installStdin != runtimeInstallScript || !strings.Contains(installStdin, "require('node:fs')") {
		t.Fatalf("the install program did not reach standard input: %q", installStdin)
	}
}

func TestRuntimeVolumeReportsTheInstallFailure(t *testing.T) {
	names, _ := RuntimeNames("/Users/test/.culler")
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch args[0] + " " + args[1] {
		case "volume inspect":
			return json.Marshal(map[string]any{"Name": names.RuntimeVolume, "Driver": "local", "Labels": map[string]string{"mc-component": "runtime", "mc-deployment": names.Suffix}})
		case "container ls":
			return nil, nil
		case "run --rm":
			if strings.Contains(strings.Join(args, " "), "dst=/runtime-output") {
				return []byte("node:internal\nruntime install: the image runtime at /opt/culler-runtime is not release new-release\n"), errors.New("exit status 2")
			}
			return []byte("old-release\n"), nil
		}
		return nil, errors.New("unexpected Docker operation")
	}
	err := InstallRuntimeVolume(context.Background(), "/Users/test/.culler", "product-image", "new-release", run)
	if err == nil || !strings.Contains(err.Error(), "exit status 2: runtime install: the image runtime at /opt/culler-runtime is not release new-release") {
		t.Fatalf("the install failure lost its cause: %v", err)
	}
}

func TestRuntimeVolumeRejectsForeignVolume(t *testing.T) {
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] != "volume" || args[1] != "inspect" {
			t.Fatal(args)
		}
		return []byte(`{"Name":"other","Driver":"local","Labels":{}}`), nil
	}
	if err := InstallRuntimeVolume(context.Background(), "/Users/test/.culler", "image", "release", run); err == nil {
		t.Fatal("a foreign volume received a runtime install")
	}
}

func TestRuntimeVolumeReusesExactRelease(t *testing.T) {
	names, _ := RuntimeNames("/Users/test/.culler")
	run := func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		if stdin != "" {
			t.Fatalf("a release check received standard input: %q", stdin)
		}
		if args[0] == "volume" {
			return json.Marshal(map[string]any{"Name": names.RuntimeVolume, "Driver": "local", "Labels": map[string]string{"mc-component": "runtime", "mc-deployment": names.Suffix}})
		}
		if args[0] != "run" || !strings.Contains(args[len(args)-1], RuntimeRoot+"/bin/check >/dev/null; cat "+RuntimeRoot+"/release-id") ||
			!strings.Contains(strings.Join(args, " "), "--user 10002:10002") {
			t.Fatal(args)
		}
		return []byte("release\n"), nil
	}
	if err := InstallRuntimeVolume(context.Background(), "/Users/test/.culler", "image", "release", run); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeVolumeRepairsUnusableRelease(t *testing.T) {
	for _, tc := range []struct {
		name       string
		active     bool
		copyUsable bool
		wantError  bool
	}{
		{name: "repair", copyUsable: true},
		{name: "active", active: true, copyUsable: true, wantError: true},
		{name: "invalid-copy", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			names, _ := RuntimeNames("/Users/test/.culler")
			copied := false
			run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
				switch args[0] + " " + args[1] {
				case "volume inspect":
					return json.Marshal(map[string]any{"Name": names.RuntimeVolume, "Driver": "local", "Labels": map[string]string{"mc-component": "runtime", "mc-deployment": names.Suffix}})
				case "container ls":
					if tc.active {
						return []byte("active-container"), nil
					}
					return nil, nil
				case "run --rm":
					if strings.Contains(strings.Join(args, " "), "dst=/runtime-output") {
						copied = true
						return nil, nil
					}
					if copied && tc.copyUsable {
						return []byte("release\n"), nil
					}
					return []byte("release\n"), errors.New("the runtime source is unreadable")
				}
				return nil, errors.New("unexpected Docker operation")
			}
			err := InstallRuntimeVolume(context.Background(), "/Users/test/.culler", "image", "release", run)
			if (err != nil) != tc.wantError || copied == tc.active {
				t.Fatalf("runtime repair: copied=%v, error=%v", copied, err)
			}
		})
	}
}
