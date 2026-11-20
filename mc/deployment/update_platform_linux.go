package deployment

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// Linux half of the update surface. The checkpoint path binding uses the
// systemd user-unit directory; the stop/confirm control arrives with A03
// stage ② and refuses loudly until then.

func platformInstalledUnitPaths(names Names) []string {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		userHome, _ := os.UserHomeDir()
		configHome = filepath.Join(userHome, ".config")
	}
	paths := make([]string, 0, len(SupervisedUnits))
	for _, unit := range SupervisedUnits {
		paths = append(paths, filepath.Join(configHome, "systemd", "user", UnitLabel(names, unit)+".service"))
	}
	return paths
}

func platformUpdateServices(_ string) func(ctx context.Context, stop bool) error {
	return func(context.Context, bool) error {
		return errors.New("systemd update service control is not implemented yet")
	}
}
