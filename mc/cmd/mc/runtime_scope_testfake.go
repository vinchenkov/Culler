//go:build test_fake_routing

package main

import "os"

func shouldDelegateToHelper() bool {
	return os.Getenv("MC_HELPER") != "" && os.Getenv("MC_SPINE") == ""
}

func directPrivateAttachmentOK() bool      { return os.Getenv("MC_SPINE") != "" }
func helperContainerName() string          { return os.Getenv("MC_HELPER") }
func helperSpinePath() string              { return os.Getenv("MC_SPINE") }
func privateHelperScopeOK() bool           { return os.Getenv("MC_SPINE") != "" }
func ensureRuntimeHelper() (string, error) { return helperContainerName(), nil }

func canInspectProjectVision() bool { return true }
