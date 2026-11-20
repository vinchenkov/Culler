package deployment

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Darwin (launchd) half of the activation file operations: install, restore,
// verify, the bundle layout, and the plist label marker. The shared
// orchestration in supervision_activate.go reaches them only through the
// five hooks below.

func activationOrder(names Names, root, launchAgentsDir string) (units, labels, sources, destinations []string) {
	for i := len(SupervisedUnits) - 1; i >= 0; i-- {
		unit := SupervisedUnits[i]
		label := UnitLabel(names, unit)
		units = append(units, unit)
		labels = append(labels, label)
		sources = append(sources, filepath.Join(root, "LaunchAgents", unit+".plist"))
		destinations = append(destinations, filepath.Join(launchAgentsDir, label+".plist"))
	}
	return units, labels, sources, destinations
}

func unitFileCarriesLabel(body []byte, label string) bool {
	return bytes.Contains(body, []byte("<string>"+label+"</string>"))
}

// installedUnitPaths locates one unit's prepared and installed definitions for
// the read-only inspectors (they name a single unit, not the start order).
func installedUnitPaths(names Names, root, launchAgentsDir, unit string) (string, string) {
	return filepath.Join(root, "LaunchAgents", unit+".plist"),
		filepath.Join(launchAgentsDir, UnitLabel(names, unit)+".plist")
}

// installUnitFile installs the prepared plist of a unit that is not loaded.
// It replaces an installed plist that differs only when that plist is an
// operator-owned regular file with one link.
func installUnitFile(source, destination string) (installedUnitFile, error) {
	var installed installedUnitFile
	body, err := os.ReadFile(source)
	if err != nil {
		return installed, err
	}
	if existing, statErr := os.Lstat(destination); statErr == nil {
		uid, links, ok := statOwner(existing)
		current, readErr := os.ReadFile(destination)
		if readErr != nil || !ok || int(uid) != os.Getuid() || existing.Mode()&os.ModeSymlink != 0 ||
			!existing.Mode().IsRegular() || links != 1 {
			return installed, fmt.Errorf("installed LaunchAgent %q is not an owner-controlled regular file", destination)
		}
		if existing.Mode().Perm() == 0o644 && bytes.Equal(current, body) {
			installed.identity = existing
			return installed, nil
		}
		installed.previous, installed.previousMode = current, existing.Mode().Perm()
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return installed, statErr
	}
	installed.identity, installed.written, err = writeLaunchAgent(destination, body, 0o644)
	return installed, err
}

// writeLaunchAgent replaces destination with body through a temporary file.
// written reports that the rename took place.
func writeLaunchAgent(destination string, body []byte, mode os.FileMode) (identity os.FileInfo, written bool, err error) {
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".mc-launch-agent-")
	if err != nil {
		return nil, false, err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		_ = tmp.Close()
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return nil, false, err
	}
	if _, err := tmp.Write(body); err != nil {
		return nil, false, err
	}
	if err := tmp.Sync(); err != nil {
		return nil, false, err
	}
	if err := tmp.Close(); err != nil {
		return nil, false, err
	}
	if err := os.Rename(tmpPath, destination); err != nil {
		return nil, false, err
	}
	cleanup = false
	if err := syncDirectory(filepath.Dir(destination)); err != nil {
		identity, statErr := os.Lstat(destination)
		if statErr != nil {
			return nil, false, fmt.Errorf("sync installed LaunchAgent: %v; inspect rollback identity: %w", err, statErr)
		}
		return identity, true, err
	}
	info, err := os.Lstat(destination)
	return info, true, err
}

// restoreUnitFile undoes installUnitFile. It removes a new plist and writes
// back a replaced plist.
func restoreUnitFile(path string, installed installedUnitFile) error {
	if !installed.written {
		return nil
	}
	current, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) && installed.previous == nil {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(installed.identity, current) {
		return fmt.Errorf("installed LaunchAgent %q changed identity during rollback", path)
	}
	if installed.previous == nil {
		return os.Remove(path)
	}
	_, _, err = writeLaunchAgent(path, installed.previous, installed.previousMode)
	return err
}

func verifyInstalledUnit(source, destination string) error {
	sourceBody, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	info, err := os.Lstat(destination)
	if err != nil {
		return err
	}
	uid, links, ok := statOwner(info)
	destinationBody, readErr := os.ReadFile(destination)
	if readErr != nil || !ok || int(uid) != os.Getuid() || info.Mode()&os.ModeSymlink != 0 ||
		!info.Mode().IsRegular() || info.Mode().Perm() != 0o644 || links != 1 || !bytes.Equal(sourceBody, destinationBody) {
		return fmt.Errorf("installed LaunchAgent %q differs from its prepared definition", destination)
	}
	return nil
}
