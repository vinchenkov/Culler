//go:build !test_fake_routing

package main

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestProductionOnboardGitMutationUsesTheClosedContainerEnvelope(t *testing.T) {
	original := productionOnboardGitDockerRun
	t.Cleanup(func() { productionOnboardGitDockerRun = original })
	var got []string
	productionOnboardGitDockerRun = func(args []string) (string, string, error) {
		got = append([]string(nil), args...)
		return "", "", nil
	}
	root := "/operator/workspace"
	if err := runProductionOnboardGitMutation(
		root, "config", "--local", "worktree.useRelativePaths", "true"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, "\x00")
	for _, required := range []string{
		"run", "--rm", "--network", "none", "--read-only",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--user", strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()),
		"--mount", "type=bind,src=" + root + ",dst=/workspace",
		"--workdir", "/workspace", "--entrypoint", "git", productionImageRef,
		"config", "--local", "worktree.useRelativePaths", "true",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("production Git argv omitted %q: %v", required, got)
		}
	}
	if strings.Contains(joined, "--privileged") {
		t.Fatalf("production Git argv widened privileges: %v", got)
	}
}

func TestProductionOnboardGitMutationRefusesUnrepresentableRootsAndSurfacesFailure(t *testing.T) {
	if err := runProductionOnboardGitMutation("/operator/with,comma", "init"); err == nil {
		t.Fatal("comma-bearing Docker mount root was accepted")
	}
	original := productionOnboardGitDockerRun
	t.Cleanup(func() { productionOnboardGitDockerRun = original })
	productionOnboardGitDockerRun = func([]string) (string, string, error) {
		return "", "container refusal", errors.New("exit 1")
	}
	err := runProductionOnboardGitMutation("/operator/workspace", "init")
	if err == nil || !strings.Contains(err.Error(), "container refusal") {
		t.Fatalf("container failure = %v", err)
	}
}

func TestProductionOnboardGitVersionRequires248InTheClosedImage(t *testing.T) {
	original := productionOnboardGitDockerRun
	t.Cleanup(func() { productionOnboardGitDockerRun = original })
	var got []string
	productionOnboardGitDockerRun = func(args []string) (string, string, error) {
		got = append([]string(nil), args...)
		return "git version 2.48.1", "", nil
	}
	if err := verifyProductionOnboardGitVersion(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, "\x00")
	if strings.Contains(joined, "--mount") || !strings.Contains(joined, "--version") ||
		!strings.Contains(joined, productionImageRef) {
		t.Fatalf("version probe escaped its closed image-only envelope: %v", got)
	}

	productionOnboardGitDockerRun = func([]string) (string, string, error) {
		return "git version 2.47.9", "", nil
	}
	if err := verifyProductionOnboardGitVersion(); err == nil ||
		!strings.Contains(err.Error(), "2.48") {
		t.Fatalf("old Git version error = %v", err)
	}
}
