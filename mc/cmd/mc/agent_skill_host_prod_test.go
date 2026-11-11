//go:build !test_fake_routing

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mc/deployment"
	"mc/distribution"
)

func TestExternalAgentSelectsCustomInstalledHelperWithoutPrivateVariables(t *testing.T) {
	userHome := t.TempDir()
	home, err := deployment.CanonicalHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", userHome)
	t.Setenv("MC_HELPER", "")
	t.Setenv("MC_SPINE", "")
	t.Setenv("CULLER_RELEASE_RECORD", "")
	previousRelease := releaseBuildID
	releaseBuildID = strings.Repeat("a", 40)
	t.Cleanup(func() { releaseBuildID = previousRelease })

	installed := publishedTestRecord(releaseBuildID, 20)
	installed.Image = "ghcr.io/culler/custom@sha256:" + strings.Repeat("b", 64)
	if err := distribution.Save(filepath.Join(home, distribution.InstalledRecord), installed); err != nil {
		t.Fatal(err)
	}
	other := installed
	other.Image = "ghcr.io/culler/default@sha256:" + strings.Repeat("c", 64)
	if err := distribution.Save(filepath.Join(userHome, ".culler", distribution.InstalledRecord), other); err != nil {
		t.Fatal(err)
	}
	want, err := deployment.RuntimeNames(home)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "custom-home")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	for _, selectedHome := range []string{home, alias} {
		t.Setenv("MC_HOME", selectedHome)
		docker := &scriptedDocker{t: t}
		manager, err := productionHelperManager(docker)
		if err != nil {
			t.Fatal(err)
		}
		if manager.names != want || manager.imageRef != installed.Image {
			t.Fatalf("custom install resolved another helper: names=%+v image=%q", manager.names, manager.imageRef)
		}
		if docker.calls != 0 {
			t.Fatalf("helper selection called Docker %d times", docker.calls)
		}
		if helperContainerName() != want.Helper || helperSpinePath() != "/mc/spine/spine.db" {
			t.Fatal("the ordinary command selected another helper or database path")
		}
	}
}
