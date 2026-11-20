package deployment

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Linux (user systemd) half of the update surface. The stop/confirm control
// mirrors the launchd half observable for observable: a stop swallows every
// service-manager error, including a unit that is already gone, and the
// confirm pass reports a unit that is still running as "remains loaded" and a
// manager it could not question as "state is unknown". The swallowed stop
// errors are an open question kept for the real-machine record, not an
// accident of this file.

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

func platformUpdateServices(home string) func(ctx context.Context, stop bool) error {
	return func(ctx context.Context, stop bool) error {
		names, err := RuntimeNames(home)
		if err != nil {
			return err
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		var problems []error
		for _, unit := range SupervisedUnits {
			unit := unit
			wg.Add(1)
			go func() {
				defer wg.Done()
				name := UnitLabel(names, unit) + ".service"
				if stop {
					updateCommand(ctx, "systemctl", "--user", "disable", "--now", name)
					return
				}
				body, err := updateCommand(ctx, "systemctl", "--user", "is-active", name)
				if err == nil {
					err = fmt.Errorf("The %s service remains loaded.", unit)
				} else {
					switch strings.ToLower(strings.TrimSpace(string(body))) {
					case "inactive", "failed", "unknown":
						return
					}
					err = fmt.Errorf("The %s service state is unknown.", unit)
				}
				mu.Lock()
				problems = append(problems, err)
				mu.Unlock()
			}()
		}
		wg.Wait()
		return errors.Join(problems...)
	}
}
