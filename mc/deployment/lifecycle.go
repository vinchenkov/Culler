package deployment

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
)

const ServiceStopsRecord = "state/stopped-services.json"

// ServiceStops records explicit operator choices independently of process health.
type ServiceStops map[string]bool

func ReadServiceStops(home string) (ServiceStops, error) {
	path := filepath.Join(home, ServiceStopsRecord)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ServiceStops{}, nil
	}
	if err != nil {
		return nil, err
	}
	uid, links, ok := statOwner(info)
	if !ok || int(uid) != os.Getuid() || links != 1 || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return nil, errors.New("The service stop record requires an owner-only regular file.")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var choices ServiceStops
	if json.Unmarshal(body, &choices) != nil || choices == nil {
		return nil, errors.New("The service stop record is invalid.")
	}
	for unit := range choices {
		if !slices.Contains(SupervisedUnits, unit) {
			return nil, errors.New("The service stop record names an unknown service.")
		}
	}
	return choices, nil
}

func WriteServiceStops(home string, choices ServiceStops) error {
	for unit := range choices {
		if !slices.Contains(SupervisedUnits, unit) {
			return errors.New("The service name is invalid.")
		}
	}
	if choices == nil {
		choices = ServiceStops{}
	}
	body, err := json.Marshal(choices)
	if err != nil {
		return err
	}
	dir := filepath.Join(home, "state")
	if err := ensureOwnerDirectory(dir, "service state"); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".service-stops-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(append(body, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), filepath.Join(home, ServiceStopsRecord)); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func InspectServiceDefinition(home, launchAgentsDir, unit, release string) error {
	if !slices.Contains(SupervisedUnits, unit) {
		return errors.New("The service name is invalid.")
	}
	if !validProductionBuildID(release) {
		return errors.New("The installed release identity is invalid.")
	}
	root := filepath.Join(home, "supervision")
	if err := validateSupervisionTree(root); err != nil {
		return err
	}
	match, err := SupervisionReleaseMatches(home, release)
	if err != nil {
		return err
	}
	if !match {
		return errors.New("The service files differ from the installed release.")
	}
	names, err := RuntimeNames(home)
	if err != nil {
		return err
	}
	return verifyInstalledLaunchAgent(filepath.Join(root, "LaunchAgents", unit+".plist"), filepath.Join(launchAgentsDir, UnitLabel(names, unit)+".plist"))
}

func InspectResidentReceipt(home, release string, since, now time.Time) error {
	if since.IsZero() {
		since = now.Add(-2 * time.Minute)
	}
	ok, err := matchingTickReceipt(filepath.Join(home, "health", "resident-tick.json"), release, since, now.Add(5*time.Second))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("The resident has no current successful check for this release.")
	}
	return nil
}
