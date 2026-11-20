package deployment

import (
	"bytes"
	"path/filepath"
)

// Darwin (launchd) half of the activation seam: the bundle layout and the
// plist label marker. The install/restore/verify file operations are shared.

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

// installUnitFile, restoreUnitFile, verifyInstalledUnit, and the atomic
// writer moved to the shared file: they are platform-neutral owner-controlled
// file operations, and both unit formats install the same way.
