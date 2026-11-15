//go:build !test_fake_routing

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc/deployment"
	"mc/distribution"
)

func writeHostBunFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '1.3.9\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestHostBunPathSelectsTheHostBunOfAPublishedInstall(t *testing.T) {
	home := t.TempDir()
	record := publishedTestRecord(strings.Repeat("a", 40), 20)
	if _, err := hostBunPath(home, &record); err == nil {
		t.Fatal("an absent host Bun passed")
	}
	program := hostBunExecutable(home, record.HostBun.Version)
	if program != filepath.Join(home, "bun", "1.3.9", "bin", "bun") {
		t.Fatalf("host Bun path = %s", program)
	}
	writeHostBunFixture(t, program)
	got, err := hostBunPath(home, &record)
	if err != nil || got != program {
		t.Fatalf("host Bun = %q, %v", got, err)
	}
	if err := os.Chmod(program, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := hostBunPath(home, &record); err == nil {
		t.Fatal("a host Bun without execute permission passed")
	}
	record.HostBun = nil
	if _, err := hostBunPath(home, &record); err == nil {
		t.Fatal("a record without a host Bun passed")
	}
}

func TestHostBunPathOfASourceInstallComesFromPATH(t *testing.T) {
	directory := t.TempDir()
	program := filepath.Join(directory, "bun")
	writeHostBunFixture(t, program)
	t.Setenv("PATH", directory)
	got, err := hostBunPath(t.TempDir(), nil)
	if err != nil || got != program {
		t.Fatalf("source install Bun = %q, %v", got, err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := hostBunPath(t.TempDir(), nil); err == nil {
		t.Fatal("a source install without bun on PATH passed")
	}
}

func TestPublishedSupervisionRecordSelectsTheReleaseOfThisSetup(t *testing.T) {
	home := t.TempDir()
	previous := releaseBuildID
	releaseBuildID = strings.Repeat("b", 40)
	t.Cleanup(func() { releaseBuildID = previous })
	t.Setenv("CULLER_RELEASE_RECORD", "")
	record, err := publishedSupervisionRecord(home)
	if err != nil || record != nil {
		t.Fatalf("an empty home is a source install: %+v, %v", record, err)
	}
	installed := publishedTestRecord(strings.Repeat("a", 40), 20)
	if err := distribution.Save(filepath.Join(home, distribution.InstalledRecord), installed); err != nil {
		t.Fatal(err)
	}
	record, err = publishedSupervisionRecord(home)
	if err != nil || record != nil {
		t.Fatalf("an installed record of another release is a source install: %+v, %v", record, err)
	}
	pending := publishedTestRecord(releaseBuildID, 21)
	if err := distribution.Save(filepath.Join(home, "state", "pending-release.json"), pending); err != nil {
		t.Fatal(err)
	}
	record, err = publishedSupervisionRecord(home)
	if err != nil || record == nil || record.Commit != pending.Commit {
		t.Fatalf("the pending record of this release = %+v, %v", record, err)
	}
	path := filepath.Join(t.TempDir(), "release.json")
	if err := distribution.Save(path, publishedTestRecord(strings.Repeat("c", 40), 22)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CULLER_RELEASE_RECORD", path)
	record, err = publishedSupervisionRecord(home)
	if err != nil || record == nil || record.Commit != strings.Repeat("c", 40) {
		t.Fatalf("the installer record = %+v, %v", record, err)
	}
	t.Setenv("CULLER_RELEASE_RECORD", filepath.Join(t.TempDir(), "absent.json"))
	if _, err := publishedSupervisionRecord(home); err == nil {
		t.Fatal("an absent installer record passed")
	}
}

func TestCheckPublishedHostBunRequiresThePinnedVersionAndTheProgram(t *testing.T) {
	pin, err := deployment.LoadHostBunPin()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	record := publishedTestRecord(strings.Repeat("a", 40), 20)
	record.HostBun.Version = pin.Version
	err = checkPublishedHostBun(home, record, pin)
	if err == nil || !strings.Contains(err.Error(), "has no Bun program") {
		t.Fatalf("absent program: %v", err)
	}
	writeHostBunFixture(t, hostBunExecutable(home, pin.Version))
	if err := checkPublishedHostBun(home, record, pin); err != nil {
		t.Fatal(err)
	}
	other := record
	other.HostBun = &distribution.HostBunRelease{Version: "0.0.1", Archives: record.HostBun.Archives}
	err = checkPublishedHostBun(home, other, pin)
	if err == nil || !strings.Contains(err.Error(), "different Bun release") {
		t.Fatalf("other version: %v", err)
	}
	other.HostBun = nil
	err = checkPublishedHostBun(home, other, pin)
	if err == nil || !strings.Contains(err.Error(), "no Bun entry") {
		t.Fatalf("absent entry: %v", err)
	}
}

func TestPublishedCompletionRemovesThePreviousHostBun(t *testing.T) {
	home := t.TempDir()
	record := publishedTestRecord(strings.Repeat("a", 40), 20)
	previous := hostBunExecutable(home, "1.3.8")
	current := hostBunExecutable(home, record.HostBun.Version)
	for _, program := range []string{previous, current} {
		writeHostBunFixture(t, program)
	}
	leftover := filepath.Join(hostBunRoot(home), ".download.abc")
	if err := os.MkdirAll(leftover, 0o700); err != nil {
		t.Fatal(err)
	}
	clock := time.Now()
	p := publishedSetup{home: home, record: record, started: clock, now: func() time.Time { return clock }, docker: publishedDockerUnavailable}
	// Both versions exist until the activation completes.
	for _, program := range []string{previous, current} {
		if _, err := os.Stat(program); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.complete(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(current); err != nil {
		t.Fatalf("the completion removed the installed host Bun: %v", err)
	}
	for _, path := range []string{previous, leftover} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("the completion kept %s: %v", path, err)
		}
	}
	installed, err := distribution.Load(filepath.Join(home, distribution.InstalledRecord))
	if err != nil || installed.Commit != record.Commit {
		t.Fatalf("completion: %#v %v", installed, err)
	}
}

func TestPublishedCompletionWithoutAHostBunFolderPasses(t *testing.T) {
	clock := time.Now()
	p := publishedSetup{home: t.TempDir(), record: publishedTestRecord(strings.Repeat("a", 40), 20), started: clock, now: func() time.Time { return clock }, docker: publishedDockerUnavailable}
	if err := p.complete(); err != nil {
		t.Fatal(err)
	}
}

func TestProviderProxyBunOfAPublishedInstallIsTheHostBun(t *testing.T) {
	home := t.TempDir()
	record := publishedTestRecord(strings.Repeat("a", 40), 20)
	path := filepath.Join(t.TempDir(), "release.json")
	if err := distribution.Save(path, record); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CULLER_RELEASE_RECORD", path)
	other := t.TempDir()
	writeHostBunFixture(t, filepath.Join(other, "bun"))
	t.Setenv("PATH", other)
	if _, err := deployment.HostBunPath(home); err == nil {
		t.Fatal("a published install without its host Bun passed")
	}
	program := hostBunExecutable(home, record.HostBun.Version)
	writeHostBunFixture(t, program)
	got, err := deployment.HostBunPath(home)
	if err != nil || got != program {
		t.Fatalf("provider proxy Bun = %q, %v", got, err)
	}
}

func TestPublishedCompletionRecordsAHostBunRemovalProblem(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the root account removes a folder without read permission")
	}
	home := t.TempDir()
	record := publishedTestRecord(strings.Repeat("a", 40), 20)
	current := hostBunExecutable(home, record.HostBun.Version)
	writeHostBunFixture(t, current)
	stuck := filepath.Join(hostBunRoot(home), "1.3.8")
	writeHostBunFixture(t, filepath.Join(stuck, "bin", "bun"))
	if err := os.Chmod(stuck, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(stuck, 0o700) })
	if err := distribution.Save(filepath.Join(home, "state", "pending-release.json"), record); err != nil {
		t.Fatal(err)
	}
	clock := time.Now()
	p := publishedSetup{home: home, record: record, started: clock, now: func() time.Time { return clock }, docker: publishedDockerUnavailable}
	if err := p.complete(); err != nil {
		t.Fatalf("a host Bun removal problem stopped the completion: %v", err)
	}
	operation, err := os.ReadFile(filepath.Join(home, "state", "install-operation.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(operation), `"phase":"complete"`) || !strings.Contains(string(operation), `"host_bun":{"problem":"`) || !strings.Contains(string(operation), "permission denied") {
		t.Fatalf("the operation record lacks the host Bun problem: %s", operation)
	}
	if _, err := os.Stat(filepath.Join(home, "state", "pending-release.json")); !os.IsNotExist(err) {
		t.Fatalf("the pending record stayed: %v", err)
	}
	if _, err := os.Stat(current); err != nil {
		t.Fatalf("the completion removed the installed host Bun: %v", err)
	}
}
