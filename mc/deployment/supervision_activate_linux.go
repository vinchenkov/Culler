package deployment

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Linux half of the activation seam. The unit file layout is declared so the
// shared tree walk and orchestration compile and reason about the systemd
// shape; the install/restore/verify operations arrive with A03 stage ② and
// refuse loudly until then.

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

func installUnitFile(_, _ string) (installedUnitFile, error) {
	return installedUnitFile{}, fmt.Errorf("systemd unit installation is not implemented yet")
}

func restoreUnitFile(_ string, _ installedUnitFile) error {
	return fmt.Errorf("systemd unit restoration is not implemented yet")
}

func verifyInstalledUnit(_, _ string) error {
	return fmt.Errorf("systemd unit verification is not implemented yet")
}
