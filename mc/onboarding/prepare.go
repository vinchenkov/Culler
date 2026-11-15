package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type DockerState string

const (
	DockerAbsent            DockerState = "absent"
	DockerEngineUnavailable DockerState = "engine_unavailable"
	DockerToolsUnavailable  DockerState = "tools_unavailable"
	DockerReady             DockerState = "ready"
	prepareProbeTimeout                 = 4 * time.Second
	productionImage                     = "mc-prod"
	imageRevisionLabel                  = "org.opencontainers.image.revision"
	desktopContext                      = "desktop-linux"
	DesktopContextCommand               = "docker context use " + desktopContext
)

type DockerStatus struct {
	State       DockerState
	AppPath     string
	CommandPath string
	Detail      string
	Command     string // A shell command that the operator runs to repair the state.
	Failure     *PreparationError
}

type PreparationError struct {
	Operation  string
	ExitCode   int
	Cause      string
	Command    string
	Diagnostic string
}

func (failure *PreparationError) Error() string {
	return failure.Cause
}

func (failure *PreparationError) DiagnosticDetails() []string {
	exit := "unavailable"
	if failure.ExitCode >= 0 {
		exit = fmt.Sprint(failure.ExitCode)
	}
	lines := []string{"Operation: " + failure.Operation, "Exit code: " + exit, "Cause: " + failure.Cause}
	if failure.Command != "" {
		lines = append(lines, "Command: "+failure.Command)
	}
	if failure.Diagnostic != "" {
		lines = append(lines, "Diagnostic: "+failure.Diagnostic)
	}
	return lines
}

func preparationFailure(operation, cause string, err error) *PreparationError {
	failure := &PreparationError{Operation: operation, ExitCode: preparationExitCode(err), Cause: cause}
	if err != nil {
		failure.Diagnostic = Diagnostic(err.Error())
	}
	return failure
}

func preparationExitCode(err error) int {
	var result interface{ ExitCode() int }
	if errors.As(err, &result) {
		return result.ExitCode()
	}
	return -1
}

type prepareCommand func(context.Context, string, []string, string, ...string) ([]byte, error)

type dockerProbe struct {
	platform string
	home     string
	apps     []string
	lookPath func(string) (string, error)
	stat     func(string) (os.FileInfo, error)
	getenv   func(string) string
	resolve  func(string) (string, error)
	run      prepareCommand
	timeout  time.Duration
}

func DetectDocker(ctx context.Context) DockerStatus {
	return detectDocker(ctx, currentDockerProbe())
}

func currentDockerProbe() dockerProbe {
	home, _ := os.UserHomeDir()
	return dockerProbe{
		platform: runtime.GOOS, home: home,
		apps:     []string{"/Applications/Docker.app", filepath.Join(home, "Applications", "Docker.app")},
		lookPath: exec.LookPath, stat: os.Stat, getenv: os.Getenv, resolve: filepath.EvalSymlinks,
		run: runPrepareCommand, timeout: prepareProbeTimeout,
	}
}

func detectDocker(ctx context.Context, probe dockerProbe) DockerStatus {
	status := DockerStatus{State: DockerAbsent}
	for _, path := range probe.apps {
		if info, err := probe.stat(path); err == nil && info.IsDir() {
			status.AppPath = path
			break
		}
	}
	status.CommandPath, _ = probe.lookPath("docker")
	if status.CommandPath == "" {
		paths := []string{filepath.Join(probe.home, ".docker", "bin", "docker"), "/usr/local/bin/docker", "/opt/homebrew/bin/docker"}
		if status.AppPath != "" {
			paths = append([]string{filepath.Join(status.AppPath, "Contents", "Resources", "bin", "docker")}, paths...)
		}
		for _, path := range paths {
			if info, err := probe.stat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
				status.CommandPath = path
				break
			}
		}
	}
	if status.AppPath == "" && (probe.platform == "darwin" || status.CommandPath == "") {
		status.Detail = "Docker Desktop is not installed. Install and open Docker Desktop, then select Check Docker Desktop again."
		return status
	}
	if status.CommandPath == "" {
		status.State = DockerToolsUnavailable
		status.Detail = "Docker Desktop is present, but its command tools are unavailable. Complete Docker Desktop setup, then select Check Docker Desktop again."
		return status
	}
	probeCtx, cancel := context.WithTimeout(ctx, probe.timeout)
	_, err := probe.run(probeCtx, "", nil, status.CommandPath, "--version")
	cancel()
	if err != nil {
		status.State = DockerToolsUnavailable
		status.Detail = "Docker Desktop is present, but its command tools failed their check. Repair Docker Desktop, then select Check Docker Desktop again."
		status.Failure = preparationFailure("Docker command check", status.Detail, err)
		return status
	}
	if failure := probeDockerContext(ctx, status.CommandPath, probe); failure != nil {
		status.State = DockerEngineUnavailable
		status.Detail = failure.Cause
		status.Command = failure.Command
		status.Failure = failure
		return status
	}
	probeCtx, cancel = context.WithTimeout(ctx, probe.timeout)
	_, err = probe.run(probeCtx, "", nil, status.CommandPath, "info")
	cancel()
	if err != nil {
		status.State = DockerEngineUnavailable
		status.Detail = "The Docker Desktop engine is not running. Open Docker Desktop and wait until it shows Engine running, then select Check Docker Desktop again."
		status.Failure = preparationFailure("Docker engine check", status.Detail, err)
		return status
	}
	status.State = DockerReady
	status.Detail = "Docker Desktop and its command tools are ready."
	return status
}

func probeDockerContext(ctx context.Context, command string, probe dockerProbe) *PreparationError {
	const operation = "Docker context check"
	if selected := probe.getenv("DOCKER_CONTEXT"); selected != "" && selected != desktopContext {
		return preparationFailure(operation, dockerVariableCause("DOCKER_CONTEXT"), nil)
	} else if selected == "" && probe.getenv("DOCKER_HOST") != "" {
		return preparationFailure(operation, dockerVariableCause("DOCKER_HOST"), nil)
	}
	probeCtx, cancel := context.WithTimeout(ctx, probe.timeout)
	defer cancel()
	output, err := probe.run(probeCtx, "", nil, command, "context", "inspect", "--format", "{{json .}}")
	if err != nil {
		return preparationFailure(operation, "The Docker context check failed. Open Docker Desktop, then select Check Docker Desktop again.", err)
	}
	var current struct {
		Name      string `json:"Name"`
		Endpoints map[string]struct {
			Host string `json:"Host"`
		} `json:"Endpoints"`
	}
	if json.Unmarshal(output, &current) != nil {
		failure := preparationFailure(operation, "The Docker context check returned an unreadable result. Open Docker Desktop, then select Check Docker Desktop again.", nil)
		failure.ExitCode = 0
		return failure
	}
	if current.Name != desktopContext {
		// Docker Desktop creates and selects its context at its first start for each user.
		probeCtx, cancel := context.WithTimeout(ctx, probe.timeout)
		_, err := probe.run(probeCtx, "", nil, command, "context", "inspect", desktopContext)
		cancel()
		failure := preparationFailure(operation, "Docker Desktop is installed, but this macOS user has not set it up. Open Docker Desktop from Applications, finish its setup, then select Check Docker Desktop again.", nil)
		if err == nil {
			failure.Cause = "Docker uses a context other than Docker Desktop. Run the command below in another terminal window, then select Check Docker Desktop again."
			failure.Command = DesktopContextCommand
		}
		failure.ExitCode = 0
		return failure
	}
	const wrongEngine = "The " + desktopContext + " Docker context points to another engine. Ask your coding harness to repair this context, then select Check Docker Desktop again."
	endpoint, err := url.Parse(current.Endpoints["docker"].Host)
	if err != nil || endpoint.Scheme != "unix" || endpoint.Host != "" ||
		endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || !filepath.IsAbs(endpoint.Path) {
		failure := preparationFailure(operation, wrongEngine, nil)
		failure.ExitCode = 0
		return failure
	}
	socket := filepath.Join(probe.home, ".docker", "run", "docker.sock")
	if probe.platform == "linux" {
		socket = filepath.Join(probe.home, ".docker", "desktop", "docker.sock")
	}
	if filepath.Clean(endpoint.Path) != socket {
		resolved, resolveErr := probe.resolve(endpoint.Path)
		expected, expectedErr := probe.resolve(socket)
		if resolveErr != nil || expectedErr != nil || resolved != expected {
			failure := preparationFailure(operation, wrongEngine, nil)
			failure.ExitCode = 0
			return failure
		}
	}
	return nil
}

func dockerVariableCause(variable string) string {
	return "Setup cannot use Docker Desktop while this terminal sets " + variable + ". Press q to exit setup, remove " + variable + " from your shell profile, such as ~/.zshrc, then resume setup in a new terminal."
}

func PrepareImage(ctx context.Context, sourceRoot, releaseBuildID string, report func(string)) error {
	return prepareImageConnection(ctx, sourceRoot, releaseBuildID, report, false)
}

func RebuildImage(ctx context.Context, sourceRoot, releaseBuildID string, report func(string)) error {
	return prepareImageConnection(ctx, sourceRoot, releaseBuildID, report, true)
}

func prepareImageConnection(ctx context.Context, sourceRoot, releaseBuildID string, report func(string), rebuild bool) error {
	reportImageProgress(report, "Check Docker Desktop")
	if failure := probeDockerContext(ctx, "docker", currentDockerProbe()); failure != nil {
		return failure
	}
	run := func(ctx context.Context, dir string, env []string, command string, args ...string) ([]byte, error) {
		if command == "bash" {
			return runImageBuildCommand(ctx, dir, env, report, command, args...)
		}
		return runPrepareCommand(ctx, dir, env, command, args...)
	}
	return prepareImageWithRebuild(ctx, sourceRoot, releaseBuildID, report, run, rebuild)
}

func prepareImage(ctx context.Context, sourceRoot, releaseBuildID string, report func(string), run prepareCommand) error {
	return prepareImageWithRebuild(ctx, sourceRoot, releaseBuildID, report, run, false)
}

func prepareImageWithRebuild(ctx context.Context, sourceRoot, releaseBuildID string, report func(string), run prepareCommand, rebuild bool) error {
	if len(releaseBuildID) < 40 || len(releaseBuildID) > 64 || strings.Trim(releaseBuildID, "0123456789abcdef") != "" {
		return preparationFailure("Agent image preparation", "The release identity is invalid. Resume setup from a complete repository checkout.", nil)
	}
	reportImageProgress(report, "Check the existing agent container")
	current, err := preparedImageMatches(ctx, releaseBuildID, run)
	if err != nil {
		return err
	}
	if current && !rebuild {
		reportImageProgress(report, "Use the existing agent container")
		return nil
	}
	if !filepath.IsAbs(sourceRoot) {
		return preparationFailure("Agent image preparation", "The release source requires an absolute path. Resume setup through install.sh.", nil)
	}
	buildCtx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	output, err := run(buildCtx, sourceRoot, []string{"MC_RELEASE_BUILD_ID=" + releaseBuildID}, "bash", filepath.Join(sourceRoot, "runner", "image", "build-prod.sh"))
	if err != nil {
		if preparationExitCode(err) == 3 && bytes.Contains(output, []byte("build-prod.sh: a supervised Culler deployment is loaded.")) {
			return preparationFailure("Agent image build", "Culler services are active. Ask your coding harness to stop the services before this image rebuild. Select Retry after the services stop.", err)
		}
		if errors.Is(buildCtx.Err(), context.Canceled) {
			return preparationFailure("Agent image build", "The agent image preparation stopped. Select Retry to continue.", err)
		}
		if errors.Is(buildCtx.Err(), context.DeadlineExceeded) {
			return preparationFailure("Agent image build", "The agent image build exceeded its time limit. Select Retry, or inspect the diagnostic details.", err)
		}
		failure := preparationFailure("Agent image build", "The agent image build failed. Open the error details, then select Retry.", err)
		failure.Diagnostic = Diagnostic(string(output))
		return failure
	}
	reportImageProgress(report, "Verify the agent container")
	current, err = preparedImageMatches(ctx, releaseBuildID, run)
	if err != nil {
		return err
	}
	if !current {
		failure := preparationFailure("Agent image check", "The agent image does not match this release. Select Retry to rebuild the image.", nil)
		failure.ExitCode = 0
		return failure
	}
	reportImageProgress(report, "The agent container is ready")
	return nil
}

func preparedImageMatches(ctx context.Context, releaseBuildID string, run prepareCommand) (bool, error) {
	probeCtx, cancel := context.WithTimeout(ctx, prepareProbeTimeout)
	defer cancel()
	output, err := run(probeCtx, "", nil, "docker", "image", "inspect", "--format", "{{json .}}", productionImage)
	if err != nil {
		if probeCtx.Err() != nil {
			return false, preparationFailure("Agent image check", "The agent image check did not finish. Check Docker Desktop, then select Retry.", err)
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
			return false, nil
		}
		return false, preparationFailure("Agent image check", "The agent image check failed. Check Docker Desktop, then select Retry.", err)
	}
	var image struct {
		ID           string `json:"Id"`
		OS           string `json:"Os"`
		Architecture string `json:"Architecture"`
		Config       struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if json.Unmarshal(output, &image) != nil {
		failure := preparationFailure("Agent image check", "The agent image check returned an unreadable result. Check Docker Desktop, then select Retry.", nil)
		failure.ExitCode = 0
		return false, failure
	}
	validID := strings.HasPrefix(image.ID, "sha256:") && len(image.ID) == 71 && strings.Trim(image.ID[7:], "0123456789abcdef") == ""
	return validID && image.OS == "linux" && image.Architecture == "arm64" && image.Config.Labels[imageRevisionLabel] == releaseBuildID, nil
}

type prepareOutput struct {
	bytes.Buffer
}

func (output *prepareOutput) Write(data []byte) (int, error) {
	n := len(data)
	if remaining := 64*1024 - output.Len(); remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = output.Buffer.Write(data)
	}
	return n, nil
}

func runPrepareCommand(ctx context.Context, dir string, extraEnv []string, command string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.WaitDelay = 250 * time.Millisecond
	var output prepareOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if err != nil {
		err = fmt.Errorf("%w: %s", err, Diagnostic(output.String()))
	}
	return output.Bytes(), err
}

// ImageMatches inspects the existing image without a build or container change.
func ImageMatches(ctx context.Context, release string) (bool, error) {
	return preparedImageMatches(ctx, release, runPrepareCommand)
}
