//go:build !test_fake_routing

package main

import (
	"os"
	"runtime"
)

const productionSpinePath = "/mc/spine/spine.db"

func shouldDelegateToHelper() bool { return runtime.GOOS == "darwin" }
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

func canInspectProjectVision() bool { return runtime.GOOS == "darwin" }
