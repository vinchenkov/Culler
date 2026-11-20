package deployment

import (
	"path/filepath"
	"strings"
)

// Linux layout for the activation seam. The install/restore/verify file
// operations are shared (they are owner-controlled file work in both
// formats); only the controller operations (bootstrap/bootout/loaded/enabled)
// arrive with stage ②c through the injected platform controller.

func activationOrder(names Names, root, unitsDir string) (units, labels, sources, destinations []string) {
	for i := len(SupervisedUnits) - 1; i >= 0; i-- {
		unit := SupervisedUnits[i]
		label := UnitLabel(names, unit)
		units = append(units, unit)
		labels = append(labels, label)
		sources = append(sources, filepath.Join(root, "units", unit+".service"))
		destinations = append(destinations, filepath.Join(unitsDir, label+".service"))
	}
	return units, labels, sources, destinations
}

func unitFileCarriesLabel(body []byte, label string) bool {
	return strings.Contains(string(body), label)
}

func installedUnitPaths(names Names, root, unitsDir, unit string) (string, string) {
	return filepath.Join(root, "units", unit+".service"),
		filepath.Join(unitsDir, UnitLabel(names, unit)+".service")
}
