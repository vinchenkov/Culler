//go:build !test_fake_routing

package main

import (
	"bufio"
	"context"
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

// Linux service control: systemctl --user operations with the same
// literal-command discipline as the darwin file (fixed binary, fixed verb,
// regex-admitted unit names), plus the unit-directory enumeration the
// uninstall plan needs.

func supervisionInstallDir(userHome string) string {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(userHome, ".config")
	}
	return filepath.Join(configHome, "systemd", "user")
}

func installedUnitFileName(label string) string { return label + ".service" }

var systemdUnitPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,152}\.service$`)

func checkedUnit(value string) (string, error) {
	if !systemdUnitPattern.MatchString(value) {
		return "", errors.New("service argument is not a safe unit name")
	}
	return value, nil
}

func systemctlUser(command string, unit string) ([]byte, error) {
	if command == "" {
		return nil, errors.New("systemctl verb is required")
	}
	if unit != "" {
		name, err := checkedUnit(unit)
		if err != nil {
			return nil, err
		}
		unit = name
		return exec.Command("systemctl", "--user", command, unit).CombinedOutput()
	}
	return exec.Command("systemctl", "--user", command).CombinedOutput()
}

func systemctlUserPair(command, flag, unit string) ([]byte, error) {
	name, err := checkedUnit(unit)
	if err != nil {
		return nil, err
	}
	return exec.Command("systemctl", "--user", command, flag, name).CombinedOutput()
}

func systemctlListUserUnits() ([]byte, error) {
	return exec.Command("systemctl", "--user", "list-unit-files", "--type", "service", "--no-legend").Output()
}

type systemdControl struct{ ctx context.Context }

func (c systemdControl) unitName(label string) string {
	if strings.HasSuffix(label, ".service") {
		return label
	}
	return label + ".service"
}

func (c systemdControl) Loaded(label string) (bool, error) {
	body, err := systemctlUser("is-active", c.unitName(label))
	if err == nil && strings.TrimSpace(string(body)) == "active" {
		return true, nil
	}
	text := strings.ToLower(string(body))
	if strings.Contains(text, "inactive") || strings.Contains(text, "failed") || strings.Contains(text, "unknown") {
		return false, nil
	}
	return false, fmt.Errorf("The service state is unavailable for %s.", label)
}

func (c systemdControl) Bootstrap(unit string) error {
	if _, err := systemctlUser("daemon-reload", ""); err != nil {
		return errors.New("systemd could not reload the user manager.")
	}
	if _, err := systemctlUserPair("enable", "--now", filepath.Base(unit)); err != nil {
		return errors.New("systemd could not start the service.")
	}
	return nil
}

func (c systemdControl) Bootout(label string) error {
	if _, err := systemctlUserPair("disable", "--now", c.unitName(label)); err != nil {
		return errors.New("systemd could not stop the service.")
	}
	return nil
}

func (c systemdControl) SetStopped(label string, stopped bool) error {
	command := "enable"
	if stopped {
		command = "disable"
	}
	if _, err := systemctlUser(command, c.unitName(label)); err != nil {
		return errors.New("systemd could not save the service start choice.")
	}
	return nil
}

func newServiceController(ctx context.Context) cullerServiceControl { return systemdControl{ctx} }

type activationController struct{}

func (activationController) Loaded(label string) (bool, error) {
	return systemdControl{}.Loaded(label)
}

func (activationController) Bootstrap(unit string) error { return systemdControl{}.Bootstrap(unit) }

func (activationController) Bootout(label string) error { return systemdControl{}.Bootout(label) }

func platformUnitLabelLoaded(label string) (bool, error) {
	return systemdControl{}.Loaded(label)
}

func newActivationController() deployment.UnitController { return activationController{} }

// uninstallServiceUnits inspects the user unit directory and the enabled
// unit-file list; MC_HOME is recovered from each unit's Environment lines.
func uninstallServiceUnits() (labels, files, homes []string, err error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, nil, err
	}
	dir := supervisionInstallDir(userHome)
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, nil, err
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if !strings.HasSuffix(entry.Name(), ".service") {
			continue
		}
		_, nameErr := checkedUnit(entry.Name())
		if nameErr != nil || !verbs.CullerLaunchLabel(strings.TrimSuffix(entry.Name(), ".service")) {
			continue
		}
		labels = unionNames(labels, []string{strings.TrimSuffix(entry.Name(), ".service")})
		files = unionNames(files, []string{path})
		file, err := os.Open(path)
		if err != nil {
			return nil, nil, nil, err
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			value, ok := strings.CutPrefix(line, "Environment=\"MC_HOME=")
			if !ok {
				if value, ok = strings.CutPrefix(line, "Environment=MC_HOME="); !ok {
					continue
				}
			}
			if home := strings.TrimSuffix(value, "\""); strings.HasPrefix(home, "/") {
				homes = unionNames(homes, []string{home})
			}
		}
		if err := scanner.Err(); err != nil {
			file.Close()
			return nil, nil, nil, err
		}
		file.Close()
	}
	out, err := systemctlListUserUnits()
	if err != nil {
		return nil, nil, nil, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 1 && verbs.CullerLaunchLabel(strings.TrimSuffix(fields[0], ".service")) {
			labels = unionNames(labels, []string{strings.TrimSuffix(fields[0], ".service")})
		}
	}
	return labels, files, homes, nil
}

func stopRunningServiceUnit(label string) error {
	loaded, err := platformUnitLabelLoaded(label)
	if err != nil || !loaded {
		return err
	}
	if err := (activationController{}).Bootout(label); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := platformUnitLabelLoaded(label)
		if err != nil || !loaded {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("service is still running after bootout")
}
