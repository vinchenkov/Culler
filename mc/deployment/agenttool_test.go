package deployment

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentToolDiscoveryReadsPathsWithoutExecutingTools(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "command tools")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ harness, command string }{{"codex", "codex"}, {"claude-sdk", "claude"}} {
		path := filepath.Join(bin, fixture.command)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 99\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		found, err := findAgentToolOn(fixture.harness, home, bin, "linux")
		if err != nil || found != path {
			t.Fatalf("The command path check failed: %q, %v", found, err)
		}
	}
	packages := filepath.Join(home, "package tools")
	if err := os.Mkdir(packages, 0o700); err != nil {
		t.Fatal(err)
	}
	original := agentToolPackageDirectories
	agentToolPackageDirectories = []string{packages}
	t.Cleanup(func() { agentToolPackageDirectories = original })
	for _, application := range []string{"Codex.app", "ChatGPT.app"} {
		path := filepath.Join(home, "Applications", application, "Contents", "Resources", "codex")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 99\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		found, err := findAgentToolOn("codex", home, "", "darwin")
		if err != nil || found != path {
			t.Fatalf("The application tool check failed: %q, %v", found, err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(packages, "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	found, err := findAgentToolOn("claude-sdk", home, "", "darwin")
	if err != nil || found != path {
		t.Fatalf("The package directory check failed: %q, %v", found, err)
	}
	if found, err = findAgentToolOn("claude-sdk", home, "", "linux"); err != nil || found != "" {
		t.Fatalf("The package directory check applied to another platform: %q, %v", found, err)
	}
}
