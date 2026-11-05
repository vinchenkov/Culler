package onboarding

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testDockerProbe(t *testing.T) dockerProbe {
	t.Helper()
	home := t.TempDir()
	return dockerProbe{
		platform: "darwin", home: home, apps: []string{filepath.Join(home, "Applications", "Docker.app")},
		lookPath: func(string) (string, error) { return "", os.ErrNotExist },
		stat: func(path string) (os.FileInfo, error) {
			if !strings.HasPrefix(path, home+string(os.PathSeparator)) {
				return nil, os.ErrNotExist
			}
			return os.Stat(path)
		},
		run: func(context.Context, string, []string, string, ...string) ([]byte, error) {
			return dockerContextFixture(home, desktopContext, ""), nil
		},
		getenv:  func(string) string { return "" },
		resolve: filepath.EvalSymlinks,
		timeout: time.Second,
	}
}

func TestDockerReadinessStates(t *testing.T) {
	for _, state := range []DockerState{DockerAbsent, DockerToolsUnavailable, DockerEngineUnavailable, DockerReady} {
		t.Run(string(state), func(t *testing.T) {
			probe := testDockerProbe(t)
			if state != DockerAbsent {
				if err := os.MkdirAll(probe.apps[0], 0700); err != nil {
					t.Fatal(err)
				}
			}
			if state == DockerEngineUnavailable || state == DockerReady {
				probe.lookPath = func(string) (string, error) { return "/test/bin/docker", nil }
			}
			var calls []string
			probe.run = func(_ context.Context, _ string, _ []string, command string, args ...string) ([]byte, error) {
				calls = append(calls, command+" "+strings.Join(args, " "))
				if state == DockerEngineUnavailable && args[0] == "info" {
					return []byte("sensitive daemon output"), errors.New("unavailable")
				}
				return dockerContextFixture(probe.home, desktopContext, ""), nil
			}
			got := detectDocker(context.Background(), probe)
			if got.State != state || got.Detail == "" {
				t.Fatalf("status = %+v", got)
			}
			if strings.Contains(got.Detail, "sensitive") {
				t.Fatal("the status exposed command output")
			}
			if (state == DockerAbsent || state == DockerToolsUnavailable) && len(calls) != 0 {
				t.Fatalf("unexpected command calls: %v", calls)
			}
		})
	}
}

func dockerContextFixture(home, name, host string) []byte {
	if host == "" {
		host = "unix://" + filepath.Join(home, ".docker", "run", "docker.sock")
	}
	return []byte(fmt.Sprintf(`{"Name":%q,"Endpoints":{"docker":{"Host":%q}}}`, name, host))
}

func TestDockerRefusesOtherConnectionsBeforeEngineAccess(t *testing.T) {
	for _, tc := range []struct {
		name    string
		context string
		host    string
		env     map[string]string
	}{
		{name: "remote context", context: "remote", host: "ssh://user:SECRET_VALUE@remote.example"},
		{name: "renamed remote context", context: desktopContext, host: "tcp://remote.example:2375"},
		{name: "another local engine", context: desktopContext, host: "unix:///another/docker.sock"},
		{name: "remote host override", context: desktopContext, env: map[string]string{"DOCKER_HOST": "ssh://user:SECRET_VALUE@remote.example"}},
		{name: "remote context override", context: desktopContext, env: map[string]string{"DOCKER_CONTEXT": "SECRET_VALUE"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := testDockerProbe(t)
			if err := os.MkdirAll(probe.apps[0], 0700); err != nil {
				t.Fatal(err)
			}
			probe.lookPath = func(string) (string, error) { return "/test/bin/docker", nil }
			probe.getenv = func(name string) string { return tc.env[name] }
			probe.run = func(_ context.Context, _ string, _ []string, _ string, args ...string) ([]byte, error) {
				if args[0] == "info" {
					t.Fatal("the readiness check contacted another engine")
				}
				return dockerContextFixture(probe.home, tc.context, tc.host), nil
			}
			got := detectDocker(context.Background(), probe)
			if got.State != DockerEngineUnavailable || got.Failure == nil || got.Failure.Operation != "Docker connection check" {
				t.Fatalf("status = %+v", got)
			}
			if strings.Contains(strings.Join(got.Failure.DiagnosticDetails(), " "), "SECRET_VALUE") {
				t.Fatal("the diagnostic exposed a connection value")
			}
		})
	}
}

func TestDockerContextOverrideTakesPrecedenceOverHost(t *testing.T) {
	probe := testDockerProbe(t)
	probe.getenv = func(name string) string {
		if name == "DOCKER_CONTEXT" {
			return desktopContext
		}
		return "tcp://unused.example:2375"
	}
	if failure := probeDockerContext(context.Background(), "/test/bin/docker", probe); failure != nil {
		t.Fatal(failure)
	}
}

func TestDockerContextAcceptsVerifiedSocketAlias(t *testing.T) {
	probe := testDockerProbe(t)
	expected := filepath.Join(probe.home, ".docker", "run", "docker.sock")
	if err := os.MkdirAll(filepath.Dir(expected), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(expected, nil, 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(probe.home, "socket-alias")
	if err := os.Symlink(expected, alias); err != nil {
		t.Fatal(err)
	}
	probe.run = func(context.Context, string, []string, string, ...string) ([]byte, error) {
		return dockerContextFixture(probe.home, desktopContext, "unix://"+alias), nil
	}
	if failure := probeDockerContext(context.Background(), "/test/bin/docker", probe); failure != nil {
		t.Fatal(failure)
	}
}

func TestDockerContextFailureRetainsSafeMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
		err    error
		code   int
	}{
		{name: "unreadable result", output: "SECRET_VALUE", code: 0},
		{name: "command failure", output: "SECRET_VALUE", err: prepareExitError(7), code: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := testDockerProbe(t)
			probe.run = func(context.Context, string, []string, string, ...string) ([]byte, error) {
				return []byte(tc.output), tc.err
			}
			failure := probeDockerContext(context.Background(), "/test/bin/docker", probe)
			if failure == nil || failure.ExitCode != tc.code || failure.Operation != "Docker connection check" {
				t.Fatalf("failure = %+v", failure)
			}
			if strings.Contains(strings.Join(failure.DiagnosticDetails(), " "), "SECRET_VALUE") {
				t.Fatal("the diagnostic exposed command output")
			}
		})
	}
}

func TestPrepareImageRechecksDockerConnection(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "docker")
	body := `#!/bin/sh
if [ "$*" != 'context inspect --format {{json .}}' ]; then
  touch "$HOME/unexpected-image-action"
  exit 2
fi
printf '%s\n' '{"Name":"desktop-linux","Endpoints":{"docker":{"Host":"tcp://SECRET_VALUE@remote.example:2375"}}}'
`
	if err := os.WriteFile(tool, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", root)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_HOST", "")
	err := PrepareImage(context.Background(), root, strings.Repeat("b", 40), nil)
	var failure *PreparationError
	if !errors.As(err, &failure) || failure.Operation != "Docker connection check" {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "unexpected-image-action")); !os.IsNotExist(err) {
		t.Fatal("image preparation contacted the other engine")
	}
	if strings.Contains(strings.Join(failure.DiagnosticDetails(), " "), "SECRET_VALUE") {
		t.Fatal("the diagnostic exposed a connection value")
	}
}

func TestDockerDetectsApplicationToolsOutsidePath(t *testing.T) {
	probe := testDockerProbe(t)
	command := filepath.Join(probe.apps[0], "Contents", "Resources", "bin", "docker")
	if err := os.MkdirAll(filepath.Dir(command), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(command, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	got := detectDocker(context.Background(), probe)
	if got.State != DockerReady || got.AppPath != probe.apps[0] || got.CommandPath != command {
		t.Fatalf("status = %+v", got)
	}
}

func TestDockerCommandFailureIsSeparateFromEngineFailure(t *testing.T) {
	probe := testDockerProbe(t)
	if err := os.MkdirAll(probe.apps[0], 0700); err != nil {
		t.Fatal(err)
	}
	probe.lookPath = func(string) (string, error) { return "/test/bin/docker", nil }
	probe.run = func(_ context.Context, _ string, _ []string, _ string, args ...string) ([]byte, error) {
		if len(args) != 1 || args[0] != "--version" {
			t.Fatalf("unexpected engine probe: %v", args)
		}
		return nil, errors.New("cannot execute")
	}
	if got := detectDocker(context.Background(), probe); got.State != DockerToolsUnavailable {
		t.Fatalf("status = %+v", got)
	}
}

func TestDockerProbeUsesBoundedDeadline(t *testing.T) {
	probe := testDockerProbe(t)
	if err := os.MkdirAll(probe.apps[0], 0700); err != nil {
		t.Fatal(err)
	}
	probe.lookPath = func(string) (string, error) { return "/test/bin/docker", nil }
	probe.timeout = 10 * time.Millisecond
	probe.run = func(ctx context.Context, _ string, _ []string, _ string, _ ...string) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > probe.timeout {
			t.Fatal("the probe requires a bounded deadline")
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if got := detectDocker(context.Background(), probe); got.State != DockerToolsUnavailable {
		t.Fatalf("status = %+v", got)
	}
}

func imageFixture(revision string) []byte {
	return []byte(fmt.Sprintf(`{"Id":"sha256:%s","Os":"linux","Architecture":"arm64","Config":{"Labels":{"org.opencontainers.image.revision":%q}}}`,
		strings.Repeat("a", 64), revision))
}

func TestPrepareImageReusesMatchingRelease(t *testing.T) {
	release := strings.Repeat("b", 40)
	calls := 0
	run := func(ctx context.Context, _ string, _ []string, command string, args ...string) ([]byte, error) {
		calls++
		if command != "docker" || strings.Join(args, " ") != "image inspect --format {{json .}} mc-prod" {
			t.Fatalf("unexpected command: %s %v", command, args)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("the image check requires a deadline")
		}
		return imageFixture(release), nil
	}
	var progress []string
	if err := prepareImage(context.Background(), t.TempDir(), release, func(message string) { progress = append(progress, message) }, run); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("command count = %d", calls)
	}
	if strings.Join(progress, ",") != "Check the existing agent container,Use the existing agent container" {
		t.Fatalf("Progress did not explain container reuse: %v", progress)
	}
}

func TestPrepareImageBuildsAndVerifiesChangedRelease(t *testing.T) {
	release := strings.Repeat("b", 40)
	root := t.TempDir()
	var calls []string
	run := func(_ context.Context, dir string, env []string, command string, args ...string) ([]byte, error) {
		calls = append(calls, command)
		if command == "bash" {
			if dir != root || strings.Join(env, " ") != "MC_RELEASE_BUILD_ID="+release || args[0] != filepath.Join(root, "runner", "image", "build-prod.sh") {
				t.Fatalf("unexpected image build: %s %v %v", dir, env, args)
			}
			return []byte("routine image build output"), nil
		}
		if len(calls) == 1 {
			return imageFixture(strings.Repeat("c", 40)), nil
		}
		return imageFixture(release), nil
	}
	if err := prepareImage(context.Background(), root, release, nil, run); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "docker,bash,docker" {
		t.Fatalf("commands = %v", calls)
	}
}

func TestPrepareImageFailurePreservesRedactedDiagnostic(t *testing.T) {
	release := strings.Repeat("b", 40)
	run := func(_ context.Context, _ string, _ []string, command string, _ ...string) ([]byte, error) {
		if command == "docker" {
			return imageFixture(""), nil
		}
		return []byte("compiler failed; api_key=SECRET_VALUE"), prepareExitError(9)
	}
	err := prepareImage(context.Background(), t.TempDir(), release, nil, run)
	if err == nil || strings.Contains(err.Error(), "SECRET_VALUE") || !strings.Contains(err.Error(), "Retry") {
		t.Fatalf("error = %v", err)
	}
	var failure *PreparationError
	if !errors.As(err, &failure) || failure.Operation != "Agent image build" || failure.ExitCode != 9 {
		t.Fatalf("error metadata = %+v", failure)
	}
	details := strings.Join(failure.DiagnosticDetails(), " ")
	if strings.Contains(details, "SECRET_VALUE") || !strings.Contains(details, "compiler failed") || !strings.Contains(details, "Exit code: 9") || strings.Contains(details, "services are active") {
		t.Fatalf("diagnostic = %s", details)
	}
}

type prepareExitError int

func (code prepareExitError) Error() string { return "api_key=SECRET_VALUE" }
func (code prepareExitError) ExitCode() int { return int(code) }

func TestPrepareImageRecognizesActiveServiceRefusal(t *testing.T) {
	for _, code := range []int{1, 3} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			run := func(_ context.Context, _ string, _ []string, command string, _ ...string) ([]byte, error) {
				if command == "docker" {
					return imageFixture(""), nil
				}
				return []byte("build-prod.sh: a supervised Culler deployment is loaded.\napi_key=SECRET_VALUE\n"), prepareExitError(code)
			}
			err := prepareImage(context.Background(), t.TempDir(), strings.Repeat("b", 40), nil, run)
			var failure *PreparationError
			if !errors.As(err, &failure) || failure.ExitCode != code || failure.Operation != "Agent image build" {
				t.Fatalf("error metadata = %+v", failure)
			}
			if got := strings.Contains(failure.Cause, "services are active"); got != (code == 3) {
				t.Fatalf("cause = %s", failure.Cause)
			}
			if strings.Contains(strings.Join(failure.DiagnosticDetails(), " "), "SECRET_VALUE") {
				t.Fatal("the diagnostic exposed command output")
			}
		})
	}
}

func TestPrepareCommandBoundsOutputAndProcessWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := runPrepareCommand(ctx, "", nil, "sh", "-c", "sleep 2")
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("the command exceeded its deadline: %v", err)
	}
	var output prepareOutput
	want := 128 * 1024
	n, err := output.Write(bytes.Repeat([]byte{'x'}, want))
	if err != nil || n != want || output.Len() != 64*1024 {
		t.Fatalf("output length = %d; count = %d; error = %v", output.Len(), n, err)
	}
}

func TestPrepareImageRebuildsSameCommitWhenFilesDiffer(t *testing.T) {
	release := strings.Repeat("b", 40)
	var calls []string
	run := func(_ context.Context, _ string, _ []string, command string, _ ...string) ([]byte, error) {
		calls = append(calls, command)
		return imageFixture(release), nil
	}
	if err := prepareImageWithRebuild(context.Background(), t.TempDir(), release, nil, run, true); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "docker,bash,docker" {
		t.Fatalf("rebuild omitted preparation or verification: %v", calls)
	}
}
