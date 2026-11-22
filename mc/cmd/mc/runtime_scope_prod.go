//go:build !test_fake_routing

package main

import (
	"os"
	"runtime"
)

const productionSpinePath = "/mc/spine/spine.db"

// The operator host is any place Culler's own containers are not. The project
// vision surface and the helper delegation both key on this one fact: a process
// inside a Culler container (agent or helper) carries /.dockerenv, and must
// neither inspect project vision nor delegate onward to another helper. Docker
// ships /.dockerenv in every image, so its absence is the scope fact; the
// darwin/Linux name presentation differs only by host, and both behave alike.
// The seams let the three-domain matrix be proven in unit form (host / helper /
// agent); the run.json identity layer (RequireHostScope) and the lock-domain
// guard are the separate, unchanged defenses they are.
var (
	operatorHostOS  = runtime.GOOS
	dockerEnvMarker = func() error { _, err := os.Stat("/.dockerenv"); return err }
)

func operatorHostScope() bool {
	switch operatorHostOS {
	case "darwin", "linux":
		return os.IsNotExist(dockerEnvMarker())
	default:
		return false
	}
}

func shouldDelegateToHelper() bool { return operatorHostScope() }
func directPrivateAttachmentOK() bool {
	return false
}
func helperContainerName() string {
	m, err := productionHelperManager(execDockerRunner{})
	if err != nil {
		return ""
	}
	return m.names.Helper
}
func helperSpinePath() string { return productionSpinePath }

func privateHelperScopeOK() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		return false
	}
	// Every agent container has the immutable run envelope mounted here. It
	// cannot unset or rename the mount, so clearing MC_RUN_JSON cannot forge
	// the helper's fixed scope.
	if _, err := os.Stat("/mc/run.json"); !os.IsNotExist(err) {
		return false
	}
	return true
}

func canInspectProjectVision() bool { return operatorHostScope() }
