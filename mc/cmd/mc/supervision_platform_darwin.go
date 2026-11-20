//go:build !test_fake_routing

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mc/deployment"
	"mc/verbs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Darwin service control: the launchd controllers, the LaunchAgents layout,
// and the uninstall enumeration. Every exec edge names its binary and verb
// literally in the call, and dynamic values are admitted only after an
// explicit character-class check. cullerLaunchd's original 5-second context
// deadline is preserved as a 5-second hard kill timer (equivalence note
// registered in the ticket evidence).

func supervisionInstallDir(userHome string) string {
	return filepath.Join(userHome, "Library", "LaunchAgents")
}

func installedUnitFileName(label string) string { return label + ".plist" }

var (
	launchdTargetPattern = regexp.MustCompile(`^gui/[0-9]{1,10}(/[A-Za-z0-9._-]{1,160})?$`)
	// launchdPathPattern is an exclusion class, not an allowlist: the callers
	// hand fixed binaries and fixed verbs to exec, each argv element is a
	// separate argument no shell ever parses, so the only genuinely dangerous
	// path bytes are control characters and the NUL terminator. Widening the
	// class preserves behavior for legitimate homes (spaces, apostrophes,
	// parentheses, dollar signs, and non-ASCII names all occur in practice).
	launchdPathPattern = regexp.MustCompile(`^/[\x20-\x7e\p{L}\p{N}]*$`)
	errServiceArgument = errors.New("service argument is not a safe unit label or path")
)

func checkedTarget(value string) (string, error) {
	if !launchdTargetPattern.MatchString(value) {
		return "", errServiceArgument
	}
	return value, nil
}

func checkedPath(value string) (string, error) {
	if value == "" || len(value) > 4094 || !launchdPathPattern.MatchString(value) {
		return "", errServiceArgument
	}
	return value, nil
}

func killAfter(cmd *exec.Cmd) func() {
	timer := time.AfterFunc(5*time.Second, func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	return func() { timer.Stop() }
}

func launchctlAbsPrint(target string) ([]byte, error) {
	target, err := checkedTarget(target)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("/bin/launchctl", "print", target)
	defer killAfter(cmd)()
	return cmd.CombinedOutput()
}

func launchctlAbsBootstrap(target, plist string) ([]byte, error) {
	target, err := checkedTarget(target)
	if err != nil {
		return nil, err
	}
	plist, err = checkedPath(plist)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("/bin/launchctl", "bootstrap", target, plist)
	defer killAfter(cmd)()
	return cmd.CombinedOutput()
}

func launchctlAbsBootout(target string) ([]byte, error) {
	target, err := checkedTarget(target)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("/bin/launchctl", "bootout", target)
	defer killAfter(cmd)()
	return cmd.CombinedOutput()
}

func launchctlAbsEnable(target string) ([]byte, error) {
	target, err := checkedTarget(target)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("/bin/launchctl", "enable", target)
	defer killAfter(cmd)()
	return cmd.CombinedOutput()
}

func launchctlAbsDisable(target string) ([]byte, error) {
	target, err := checkedTarget(target)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("/bin/launchctl", "disable", target)
	defer killAfter(cmd)()
	return cmd.CombinedOutput()
}

func launchctlPrint(target string) ([]byte, error) {
	target, err := checkedTarget(target)
	if err != nil {
		return nil, err
	}
	return exec.Command("launchctl", "print", target).CombinedOutput()
}

func launchctlBootout(target string) ([]byte, error) {
	target, err := checkedTarget(target)
	if err != nil {
		return nil, err
	}
	return exec.Command("launchctl", "bootout", target).CombinedOutput()
}

func launchctlBootstrapPair(target, plist string) ([]byte, error) {
	target, err := checkedTarget(target)
	if err != nil {
		return nil, err
	}
	plist, err = checkedPath(plist)
	if err != nil {
		return nil, err
	}
	return exec.Command("launchctl", "bootstrap", target, plist).CombinedOutput()
}

func launchctlList() ([]byte, error) {
	return exec.Command("launchctl", "list").Output()
}

func plutilJSON(plist string) ([]byte, error) {
	plist, err := checkedPath(plist)
	if err != nil {
		return nil, err
	}
	return exec.Command("plutil", "-convert", "json", "-o", "-", plist).Output()
}

type cullerLaunchd struct{ ctx context.Context }

func (c cullerLaunchd) target(label string) string {
	return fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
}

func (c cullerLaunchd) Loaded(label string) (bool, error) {
	body, err := launchctlAbsPrint(c.target(label))
	if err == nil {
		return true, nil
	}
	text := strings.ToLower(string(body))
	if strings.Contains(text, "could not find service") || strings.Contains(text, "service not found") {
		return false, nil
	}
	return false, fmt.Errorf("The service state is unavailable for %s.", label)
}

func (c cullerLaunchd) Bootstrap(plist string) error {
	if _, err := launchctlAbsBootstrap(fmt.Sprintf("gui/%d", os.Getuid()), plist); err != nil {
		return errors.New("macOS could not start the service.")
	}
	return nil
}

func (c cullerLaunchd) Bootout(label string) error {
	if _, err := launchctlAbsBootout(c.target(label)); err != nil {
		return errors.New("macOS could not stop the service.")
	}
	return nil
}

func (c cullerLaunchd) SetStopped(label string, stopped bool) error {
	var err error
	if stopped {
		_, err = launchctlAbsDisable(c.target(label))
	} else {
		_, err = launchctlAbsEnable(c.target(label))
	}
	if err != nil {
		return errors.New("macOS could not save the service start choice.")
	}
	return nil
}

func newServiceController(_ context.Context) cullerServiceControl { return cullerLaunchd{} }

type launchdController struct{}

func (launchdController) Loaded(label string) (bool, error) { return launchdLabelLoaded(label) }

func (launchdController) Bootstrap(plist string) error {
	target := fmt.Sprintf("gui/%d", os.Getuid())
	label := strings.TrimSuffix(filepath.Base(plist), ".plist")
	if out, err := launchctlAbsEnable(target + "/" + label); err != nil {
		return fmt.Errorf("enable %s: %v: %s", label, err, strings.TrimSpace(string(out)))
	}
	out, err := launchctlBootstrapPair(target, plist)
	if err != nil {
		return fmt.Errorf("launchctl bootstrap %s %s: %v: %s", target, plist, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (launchdController) Bootout(label string) error {
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
	out, err := launchctlBootout(target)
	if err != nil {
		return fmt.Errorf("launchctl bootout %s: %v: %s", target, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func launchdLabelLoaded(label string) (bool, error) {
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
	out, err := launchctlPrint(target)
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		text := strings.ToLower(string(out))
		if strings.Contains(text, "could not find service") || strings.Contains(text, "service not found") {
			return false, nil
		}
	}
	return false, fmt.Errorf("launchctl print %s: %v: %s", target, err, strings.TrimSpace(string(out)))
}

func platformUnitLabelLoaded(label string) (bool, error) { return launchdLabelLoaded(label) }

func newActivationController() deployment.UnitController { return launchdController{} }

// Inspect both on-disk units and loaded units whose plist was removed manually.
func uninstallServiceUnits() (labels, plists, homes []string, err error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, nil, err
	}
	dir := supervisionInstallDir(userHome)
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, nil, err
	}
	var files []string
	for _, entry := range entries {
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	labels, plists = verbs.CullerLaunchAgents(files)
	for _, plist := range plists {
		body, err := plutilJSON(plist)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("read %s: %w", plist, err)
		}
		var config struct{ EnvironmentVariables map[string]string }
		if err := json.Unmarshal(body, &config); err != nil {
			return nil, nil, nil, err
		}
		if home := config.EnvironmentVariables["MC_HOME"]; home != "" {
			homes = unionNames(homes, []string{home})
		} else if home, ok := deployment.AgentsViewHomeForData(config.EnvironmentVariables["AGENTSVIEW_DATA_DIR"]); ok {
			homes = unionNames(homes, []string{home})
		}
	}
	out, err := launchctlList()
	if err != nil {
		return nil, nil, nil, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && verbs.CullerLaunchLabel(fields[2]) {
			labels = unionNames(labels, []string{fields[2]})
		}
	}
	return labels, plists, homes, nil
}

func stopRunningServiceUnit(label string) error {
	loaded, err := launchdLabelLoaded(label)
	if err != nil || !loaded {
		return err
	}
	if err := (launchdController{}).Bootout(label); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := launchdLabelLoaded(label)
		if err != nil || !loaded {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("service is still running after bootout")
}
