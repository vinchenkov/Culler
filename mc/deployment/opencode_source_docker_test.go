//go:build docker_boundary

package deployment

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestOpenCodeSourceDockerPreparationAndOfflineReuse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	image := os.Getenv("CULLER_OPENCODE_TEST_IMAGE")
	if image == "" {
		image = "mc-prod"
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(docker, filepath.Join(bin, "docker")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	if _, err := exec.LookPath("npm"); err == nil {
		t.Fatal("The isolated host path includes npm.")
	}
	checkout, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	commit := openCodeSourceGit(t, checkout, "rev-parse", "HEAD")
	source := runtimeSourceRecord{Source: checkout, Release: commit}
	// A small committed repository permits a second commit without a change to the real checkout.
	root := t.TempDir()
	if _, err := snapshotOpenCodeSource(ctx, source, root); err != nil {
		t.Fatal(err)
	}
	openCodeSourceGit(t, root, "init", "--quiet")
	source = runtimeSourceRecord{Source: root, Release: openCodeSourceCommit(t, root)}
	home := agentProfileTestHome(t)
	volume, err := OpenCodeVolume(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if output, err := runRuntimeDocker(context.Background(), "", "volume", "rm", volume); err != nil && !strings.Contains(string(output), "no such volume") {
			t.Errorf("Remove the scratch OpenCode volume: %v: %s", err, output)
		}
	})
	builds, installs, metadata := 0, 0, 0
	offline := false
	run := func(ctx context.Context, stdin string, args ...string) ([]byte, error) {
		if index := slices.Index(args, openCodeSourceProgram); index >= 0 && args[index+1] == "build" {
			builds++
			if offline {
				t.Fatal("The verified package requested another archive build.")
			}
		}
		if slices.Contains(args, openCodeInstallScript) {
			installs++
		}
		if len(args) >= 2 && args[len(args)-2] == "release" {
			metadata++
		}
		if offline && args[0] == "run" && !slices.Contains(args, "none") {
			t.Fatal("Package reuse requested a network connection.")
		}
		return runRuntimeDocker(ctx, stdin, args...)
	}
	if err := ConfigureRuntimeSource(home, filepath.Join(source.Source, "runner"), source.Release); err != nil {
		t.Fatal(err)
	}
	if err := updateOpenCodeVolume(ctx, home, image, source.Release, run); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(home, "state", "opencode-source-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var first openCodeSourcePackage
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatal(err)
	}
	t.Logf("The declared OpenCode version %s passed the native check and the archive content check.", first.Inputs.Version)
	offline = true
	source.Release = openCodeSourceCommit(t, root)
	if err := ConfigureRuntimeSource(home, filepath.Join(root, "runner"), source.Release); err != nil {
		t.Fatal(err)
	}
	if err := updateOpenCodeVolume(ctx, home, image, source.Release, run); err != nil {
		t.Fatal(err)
	}
	if builds != 1 || installs != 1 || metadata != 1 || OpenCodeDownloadStatus(home).Status != "succeeded" {
		t.Fatalf("The second commit failed reuse: builds=%d installs=%d metadata=%d", builds, installs, metadata)
	}
	if err := os.Rename(root, root+"-moved"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root + "-moved") })
	operation, err := startOpenCodeDownload(home, false, func(string, string) error {
		t.Fatal("The first connection requested package preparation after the checkout moved.")
		return nil
	})
	if err != nil || operation.Status != "succeeded" {
		t.Fatal(operation, err)
	}
	// A changed plugin file must prevent readiness even when the operation record says success.
	output, err := runRuntimeDocker(ctx, "", "run", "--rm", "--network", "none", "--user", "0:0", "--mount",
		"type=volume,src="+volume+",dst=/output,volume-nocopy", image, "/bin/sh", "-ec", "printf changed > /output/control/server.mjs")
	if err != nil {
		t.Fatalf("Change the scratch package: %v: %s", err, output)
	}
	first.Release = source.Release
	if err := verifyOpenCodeSource(ctx, image, volume, first, run); err == nil || !strings.Contains(err.Error(), "files differ") {
		t.Fatalf("The corrupt scratch package passed: %v", err)
	}
	t.Log("The second commit reused all package files without network access. A corrupt plugin failed verification.")
}
