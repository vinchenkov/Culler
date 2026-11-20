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

// Darwin (launchd) half of the update surface: the stop/confirm control used
// inside the update window, and the installed-unit paths a checkpoint binds.

func platformInstalledUnitPaths(names Names) []string {
	userHome, _ := os.UserHomeDir()
	paths := make([]string, 0, len(SupervisedUnits))
	for _, unit := range SupervisedUnits {
		paths = append(paths, filepath.Join(userHome, "Library", "LaunchAgents", UnitLabel(names, unit)+".plist"))
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
				label := fmt.Sprintf("gui/%d/%s", os.Getuid(), UnitLabel(names, unit))
				verb := "print"
				if stop {
					verb = "bootout"
				}
				body, err := updateCommand(ctx, "/bin/launchctl", verb, label)
				if err != nil {
					message := strings.ToLower(string(body))
					if strings.Contains(message, "could not find service") || strings.Contains(message, "service not found") || strings.Contains(message, "no such process") {
						return
					}
				}
				if stop {
					return
				}
				if err == nil {
					err = fmt.Errorf("The %s service remains loaded.", unit)
				} else {
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
