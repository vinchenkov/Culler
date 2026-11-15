package deployment

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	operatorconfig "mc/config"
	"os/exec"
	"strings"
)

const RuntimeRoot = "/opt/culler-runtime"
const DefaultProjectImage = "mc-project"

// The runtime Node.js runs this program as root in a container of the product
// image. The program makes the runtime volume equal to the image runtime.
//
//go:embed runtime_install.js
var runtimeInstallScript string

// RuntimeDocker runs one docker command with the given standard input and
// returns its combined output. An empty stdin gives the command no input.
type RuntimeDocker func(ctx context.Context, stdin string, args ...string) ([]byte, error)

func runRuntimeDocker(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "docker", args...)
	if stdin != "" {
		command.Stdin = strings.NewReader(stdin)
	}
	return command.CombinedOutput()
}

// runtimeInstallArgs are the docker arguments that run the install program.
// The program reads the volume at /runtime-output and the runtime of the image.
func runtimeInstallArgs(volume, image, release string) []string {
	return []string{"run", "--rm", operatorconfig.PidsLimitFlag, "-i", "--label", "mc-deployment=" + strings.TrimPrefix(volume, "mc-runtime-"), "--network", "none", "--user", "0:0", "--mount",
		"type=volume,src=" + volume + ",dst=/runtime-output,volume-nocopy", image,
		RuntimeRoot + "/node/bin/node", "-", RuntimeRoot, "/runtime-output", release}
}

// InstallRuntimeVolume makes the runtime volume equal to the runtime of the
// image. It writes only the entries that differ, and only after the runtime
// containers stop.
func InstallRuntimeVolume(ctx context.Context, home, image, release string, run RuntimeDocker) error {
	if run == nil {
		run = runRuntimeDocker
	}
	names, err := RuntimeNames(home)
	if err != nil {
		return err
	}
	if image == "" || strings.HasPrefix(image, "-") || release == "" {
		return fmt.Errorf("the Culler runtime requires an image and release identity")
	}
	volume := names.RuntimeVolume
	inspect, err := run(ctx, "", "volume", "inspect", "--format", "{{json .}}", volume)
	if err != nil {
		if !strings.Contains(strings.ToLower(string(inspect)), "no such volume") {
			return fmt.Errorf("inspect the Culler runtime volume: %w", err)
		}
		if _, err := run(ctx, "", "volume", "create", "--label", "mc-component=runtime", "--label", "mc-deployment="+names.Suffix, volume); err != nil {
			return fmt.Errorf("create the Culler runtime volume: %w", err)
		}
		inspect, err = run(ctx, "", "volume", "inspect", "--format", "{{json .}}", volume)
		if err != nil {
			return fmt.Errorf("inspect the new Culler runtime volume: %w", err)
		}
	}
	var record struct {
		Name    string
		Driver  string
		Options map[string]string
		Labels  map[string]string
	}
	if json.Unmarshal(inspect, &record) != nil || record.Name != volume || record.Driver != "local" || len(record.Options) != 0 ||
		record.Labels["mc-component"] != "runtime" || record.Labels["mc-deployment"] != names.Suffix {
		return fmt.Errorf("the Culler runtime volume has a different owner or configuration")
	}
	probe := []string{"run", "--rm", operatorconfig.PidsLimitFlag, "--label", "mc-deployment=" + names.Suffix, "--network", "none", "--cap-drop", "ALL", "--user", "10002:10002", "--mount",
		"type=volume,src=" + volume + ",dst=" + RuntimeRoot + ",readonly,volume-nocopy", image, "/bin/sh", "-ec",
		RuntimeRoot + "/bin/check >/dev/null; cat " + RuntimeRoot + "/release-id"}
	current, probeErr := run(ctx, "", probe...)
	if probeErr == nil && strings.TrimSpace(string(current)) == release {
		return nil
	}
	users, err := run(ctx, "", "container", "ls", "--quiet", "--filter", "volume="+volume)
	if err != nil {
		return fmt.Errorf("inspect the Culler runtime users: %w", err)
	}
	if strings.TrimSpace(string(users)) != "" {
		return fmt.Errorf("stop Culler agent containers before the runtime update")
	}
	// An interrupted install leaves no release marker. The next install repairs the volume.
	output, err := run(ctx, runtimeInstallScript, runtimeInstallArgs(volume, image, release)...)
	if err != nil {
		return fmt.Errorf("install the Culler runtime files: %w: %s", err, lastLine(output))
	}
	current, err = run(ctx, "", probe...)
	if err != nil || strings.TrimSpace(string(current)) != release {
		return fmt.Errorf("the Culler runtime volume failed its release check")
	}
	return nil
}

func lastLine(output []byte) string {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
