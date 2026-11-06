package main

import (
	"mc/deployment"
	"mc/verbs"
	"os"
	"path/filepath"
)

// Recovery commands also enter this dispatcher. Ordinary work stays stopped
// until the journal records a commit or a completed restore.
func gateUpdateCommand(args []string) error {
	if len(args) == 0 || updateRecoveryCommand(args[0]) || (len(args) == 2 && args[0] == "agent-profile" && (args[1] == "runtime-catalog" || args[1] == "test-runtime-authorities")) {
		return nil
	}
	if home := os.Getenv("MC_HOME"); home != "" && !filepath.IsAbs(home) {
		return verbs.Usagef("MC_HOME must be absolute")
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return verbs.Usagef("resolve deployment for update check: %v", err)
	}
	if err := deployment.UpdateBlocksWork(home); err != nil {
		return verbs.Domainf("%v", err)
	}
	return nil
}

func updateRecoveryCommand(command string) bool {
	switch command {
	case "__setup", "__onboard", "__onboard-spine", "__onboard-state",
		"__update-reconcile", "__release", "__release-database", "__database-schema",
		"__doctor-runtime", "doctor", "health", "logs":
		return true
	default:
		return false
	}
}
