//go:build test_cli_faults

package verbs

import (
	"fmt"
	"os"
	"strings"
)

// faultLabels keep the abort wording of the triggers this hook replaced.
var faultLabels = map[string]string{
	"collie_binding":      "binding",
	"collie_echo":         "echo",
	"collie_end_activity": "end activity",
	"collie_reply":        "reply",
}

// faultPoint aborts a named write when MC_TEST_FAULT lists it. This file only
// compiles under the test_cli_faults build tag used by the CLI test harness;
// release builds carry the no-op twin, so no runtime configuration can arm a
// failure switch in production.
func faultPoint(name string) error {
	if name == "" {
		return nil
	}
	for _, armed := range strings.Split(os.Getenv("MC_TEST_FAULT"), ",") {
		if strings.TrimSpace(armed) == name {
			return fmt.Errorf("injected %s failure", faultLabels[name])
		}
	}
	return nil
}
