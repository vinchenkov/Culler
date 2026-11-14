// Package testenv gives a test process the environment of an operator host.
// An agent container sets run variables and mounts its run identity at
// /mc/run.json. A test that inherits them tests the container instead of
// the code.
package testenv

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// runVariables are the variables that Culler sets in an agent container or
// that name a deployment. runner/shared/test-environment.ts holds the same list.
var runVariables = []string{
	"CODEX_HOME", "CULLER_CACHE_LIMIT_BYTES", "MC_AGENT_RUNNER_ROUTES", "MC_ARTIFACTS_ROOT", "MC_BIN", "MC_BIN_DIR",
	"MC_CLAUDE_ADAPTER", "MC_CLAUDE_BIN", "MC_CODEX_ADAPTER", "MC_CODEX_BIN", "MC_HARNESS_CLI", "MC_HELPER",
	"MC_HOME", "MC_COLLIE_POLL_MS", "MC_LOG_LEVEL", "MC_OPERATOR_ORIGIN", "MC_POOL_IDS", "MC_PRIVATE_BUN",
	"MC_PRIVATE_NODE", "MC_ROLE", "MC_RUN_ID", "MC_RUN_JSON", "MC_RUN_LOG_DIR",
	"MC_SCRIBE_IMAGE_OK", "MC_SPINE", "MC_SUBJECT_ID", "MC_TASK_STORE_ROOT",
	"MC_TICK_INTERVAL_MS", "MC_TOKEN",
}

// RunVariables returns the run variables in order.
func RunVariables() []string {
	return slices.Clone(runVariables)
}

// RunVariable reports whether the named variable is a run variable.
func RunVariable(name string) bool {
	return slices.Contains(runVariables, name)
}

// Isolate removes each run variable from the process environment. It points
// MC_RUN_JSON at an absent file in dir, so the code under test finds no run
// identity. A test can still set its own identity.
func Isolate(dir string) error {
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if RunVariable(name) {
			if err := os.Unsetenv(name); err != nil {
				return err
			}
		}
	}
	return os.Setenv("MC_RUN_JSON", filepath.Join(dir, "absent-run.json"))
}
