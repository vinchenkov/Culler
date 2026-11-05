//go:build docker_boundary

package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectDeleteDockerBoundary(t *testing.T) {
	spine := initSpine(t)
	home, err := filepath.EvalSymlinks(filepath.Dir(spine))
	if err != nil {
		t.Fatal(err)
	}
	image := "mc-prod"
	if out, err := exec.Command("docker", "image", "inspect", image).CombinedOutput(); err != nil {
		t.Fatalf("The test requires the local image %s: %v, %s", image, err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("Docker %s failed: %v, %s", args[0], err, out)
		}
		return strings.TrimSpace(string(out))
	}
	base := []string{"--network", "none", "--read-only", "--memory", "64m", "--pids-limit", "32", "--entrypoint", "/bin/sh"}
	loop := []string{image, "-c", "while :; do sleep 1; done"}
	owned := []string{}
	for _, tier := range []string{"setup", "homie", "wiki-export"} {
		args := []string{"create", "--label", "mc-managed=true", "--label", "com.culler.home=" + home, "--label", "com.culler.project=ws-test", "--label", "mc-tier=" + tier}
		args = append(args, base...)
		args = append(args, loop...)
		id := docker(args...)
		t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
		start := exec.CommandContext(ctx, mcBin, "__project-docker", "ws-test", "--", "start", id)
		start.Env = hermeticEnv(spineEnv(spine)...)
		if out, err := start.CombinedOutput(); err != nil {
			t.Fatalf("The guarded start failed: %v, %s", err, out)
		}
		if state := docker("inspect", "--format", "{{.State.Running}}", id); state != "true" {
			t.Fatal("The Project container did not start.")
		}
		owned = append(owned, id)
	}
	unrelated := []string{}
	for _, labels := range [][]string{
		{"--label", "mc-managed=true", "--label", "com.culler.home=" + home, "--label", "mc-project=other"},
		{"--label", "mc-managed=true", "--label", "com.culler.home=" + home + "-other", "--label", "mc-project=ws-test"},
	} {
		args := append([]string{"run", "-d"}, labels...)
		args = append(args, base...)
		args = append(args, loop...)
		id := docker(args...)
		t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
		unrelated = append(unrelated, id)
	}
	name := fmt.Sprintf("culler-delete-boundary-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	args := []string{"__project-docker", "ws-test", "--", "run", "--rm", "--name", name, "--label", "mc-managed=true", "--label", "mc-project=ws-test"}
	args = append(args, base...)
	args = append(args, loop...)
	command := exec.CommandContext(ctx, mcBin, args...)
	command.Env = hermeticEnv(spineEnv(spine)...)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	awaitProjectCondition(t, func() bool {
		out, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.Running}}", name).Output()
		return err == nil && strings.TrimSpace(string(out)) == "true"
	})
	before := time.Now()
	result := runMC(t, spineEnv(spine), "", "project", "delete", "ws-test", "--confirm")
	if result.code != 0 {
		t.Fatalf("Deletion failed: %s", result.stderr)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("The guarded command failed: %v, %s", err, output.String())
	}
	var stopped struct {
		ExitCode int `json:"exitCode"`
	}
	if err := json.Unmarshal(output.Bytes(), &stopped); err != nil || stopped.ExitCode == 0 {
		t.Fatalf("The Project command did not report its forced stop: %s, %v", output.String(), err)
	}
	for _, id := range append(owned, name) {
		if out, err := exec.Command("docker", "inspect", id).CombinedOutput(); err == nil {
			t.Fatalf("Deletion retained %s: %s", id, out)
		}
	}
	for _, id := range unrelated {
		if state := docker("inspect", "--format", "{{.State.Running}}", id); state != "true" {
			t.Fatal("Deletion stopped another Project container.")
		}
	}
	if result := runMC(t, spineEnv(spine), "", "project", "list"); len(result.json["projects"].([]any)) != 0 {
		t.Fatal("Deletion retained the Project records.")
	}
	t.Logf("Deletion stopped four Project containers in %s and preserved two unrelated containers.", time.Since(before).Round(time.Millisecond))
}
