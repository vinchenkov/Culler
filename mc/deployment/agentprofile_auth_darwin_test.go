package deployment

import (
	"os"
	"path/filepath"
	"testing"

	operatorconfig "mc/config"
)

func TestAuthorizationExecutableOutsideSupervisedPath(t *testing.T) {
	home := t.TempDir()
	packages := filepath.Join(home, "package tools")
	if err := os.Mkdir(packages, 0o700); err != nil {
		t.Fatal(err)
	}
	original := agentToolPackageDirectories
	agentToolPackageDirectories = []string{packages}
	t.Cleanup(func() { agentToolPackageDirectories = original })
	t.Setenv("HOME", home)
	t.Setenv("PATH", filepath.Join(home, "supervised"))

	path := filepath.Join(packages, "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	found, err := authorizationExecutable("claude")
	if err != nil || found != path {
		t.Fatalf("The executable lookup failed: %q, %v", found, err)
	}
	if available, message := AgentProfileAuthorizationCapability(operatorconfig.CredentialKindClaudeOAuth); !available || message != "" {
		t.Fatalf("A present executable reported an unavailable authorization: %v, %q", available, message)
	}
}
