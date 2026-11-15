package deployment

import (
	"context"
	"encoding/json"
	"fmt"
	"mc/distribution"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func updateTestRecord() distribution.Record {
	r := distribution.Record{Version: "0.1.0", Format: 1, Commit: strings.Repeat("a", 40), ReleaseID: strings.Repeat("a", 40), Tag: "v0.1.0", Sequence: 1, PublishedAt: time.Now().UTC(), Platform: "darwin-arm64", Schema: strings.Repeat("b", 64), Image: "ghcr.io/test/culler@sha256:" + strings.Repeat("c", 64), Runtimes: distribution.RuntimeVersions{Bun: "1.3.12", Go: "1.25.12"}, Installer: distribution.Asset{Name: "installer.sh", SHA256: strings.Repeat("d", 64), Size: 1}, Package: distribution.Asset{Name: "culler-darwin-arm64.tar.gz", SHA256: strings.Repeat("e", 64), Size: 1}}
	for _, n := range distribution.RequiredChecks {
		r.Checks = append(r.Checks, distribution.Check{Name: n, Passed: true})
	}
	return r
}
func updateTestJournal(t *testing.T) (string, *UpdateJournal) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	names, _ := RuntimeNames(home)
	id := strings.Repeat("a", 16)
	j := &UpdateJournal{Version: 1, ID: id, Deployment: names.Suffix, Phase: "stop", Candidate: updateTestRecord(), CandidateRoot: filepath.Join(UpdateDirectory(home, id), "candidate"), PriorServices: map[string]bool{"resident": true}, PriorStops: ServiceStops{}}
	if err := SaveUpdateJournal(home, j); err != nil {
		t.Fatal(err)
	}
	return home, j
}
func updateTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestUpdateJournalBlocksIncompleteAndUnreadableRecords(t *testing.T) {
	home, j := updateTestJournal(t)
	if UpdateBlocksWork(home) == nil {
		t.Fatal("incomplete update permitted work")
	}
	j.Phase = "committed"
	if err := SaveUpdateJournal(home, j); err != nil {
		t.Fatal(err)
	}
	if err := UpdateBlocksWork(home); err != nil {
		t.Fatal(err)
	}
	updateTestFile(t, UpdateJournalPath(home), "{")
	if UpdateBlocksWork(home) == nil {
		t.Fatal("unreadable update permitted work")
	}
}

func TestUpdateCommitWaitsForItsMaintenanceRelease(t *testing.T) {
	home, j := updateTestJournal(t)
	j.Phase = "committed"
	if err := SaveUpdateJournal(home, j); err != nil {
		t.Fatal(err)
	}
	request := filepath.Join(home, "state", "install-maintenance.json")
	updateTestFile(t, request, `{"id":"`+j.ID+`"}`)
	if UpdateBlocksWork(home) == nil {
		t.Fatal("visible commit permitted work before the activation gate was released")
	}
	if err := os.Remove(request); err != nil {
		t.Fatal(err)
	}
	if err := UpdateBlocksWork(home); err != nil {
		t.Fatal(err)
	}
	updateTestFile(t, request, `{"id":"another-service-operation"}`)
	if err := UpdateBlocksWork(home); err != nil {
		t.Fatal("an old update blocked a different service operation", err)
	}
}
func TestUpdateCheckpointRestoresHostDataAndPreservesProjectFiles(t *testing.T) {
	home, j := updateTestJournal(t)
	ctx := context.Background()
	updateTestFile(t, filepath.Join(home, "config.toml"), "original")
	updateTestFile(t, filepath.Join(home, "sessions", "native"), "native history")
	updateTestFile(t, filepath.Join(home, "refresh-grants", "key.json"), "old grant reference")
	if err := CreateUpdateCheckpoint(ctx, home, j, nil); err != nil {
		t.Fatal(err)
	}
	if err := SaveUpdateJournal(home, j); err != nil {
		t.Fatal(err)
	}
	updateTestFile(t, filepath.Join(home, "config.toml"), "candidate")
	updateTestFile(t, filepath.Join(home, "refresh-grants", "key.json"), "new grant reference")
	updateTestFile(t, filepath.Join(home, "release", "new"), "candidate file")
	if err := RestoreUpdateCheckpoint(ctx, home, j, nil); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	if string(body) != "original" {
		t.Fatal("configuration did not restore")
	}
	body, _ = os.ReadFile(filepath.Join(home, "sessions", "native"))
	if string(body) != "native history" {
		t.Fatal("native history changed")
	}
	body, _ = os.ReadFile(filepath.Join(home, "refresh-grants", "key.json"))
	if string(body) != "new grant reference" {
		t.Fatal("new credential reference was lost")
	}
	if _, err := os.Stat(filepath.Join(home, "release")); !os.IsNotExist(err) {
		t.Fatal("new software survived restoration")
	}
	if err := RestoreUpdateCheckpoint(ctx, home, j, nil); err != nil {
		t.Fatal("restore retry:", err)
	}
}
func TestUpdateCheckpointDamageRefusesBeforeLiveReplacement(t *testing.T) {
	home, j := updateTestJournal(t)
	ctx := context.Background()
	live := filepath.Join(home, "config.toml")
	updateTestFile(t, live, "original")
	if err := CreateUpdateCheckpoint(ctx, home, j, nil); err != nil {
		t.Fatal(err)
	}
	updateTestFile(t, j.Checkpoint.Paths[0].Backup, "damaged")
	updateTestFile(t, live, "current")
	if err := RestoreUpdateCheckpoint(ctx, home, j, nil); err == nil {
		t.Fatal("damaged checkpoint restored")
	}
	body, _ := os.ReadFile(live)
	if string(body) != "current" {
		t.Fatal("live data changed before checkpoint validation")
	}
}
func TestUpdateCheckpointAdoptsCompleteCopyAfterJournalInterruption(t *testing.T) {
	home, j := updateTestJournal(t)
	ctx := context.Background()
	updateTestFile(t, filepath.Join(home, "config.toml"), "original")
	if err := CreateUpdateCheckpoint(ctx, home, j, nil); err != nil {
		t.Fatal(err)
	}
	j.Checkpoint = nil
	if err := CreateUpdateCheckpoint(ctx, home, j, nil); err != nil {
		t.Fatal(err)
	}
	if j.Checkpoint == nil {
		t.Fatal("complete checkpoint was not adopted")
	}
}
func TestUpdateCleanupPreservesCandidateUntilDurableCompletion(t *testing.T) {
	home, j := updateTestJournal(t)
	ctx := context.Background()
	if err := CreateUpdateCheckpoint(ctx, home, j, nil); err != nil {
		t.Fatal(err)
	}
	updateTestFile(t, filepath.Join(j.CandidateRoot, "bin", "mc"), "candidate")
	updateTestFile(t, filepath.Join(home, "backups", "user.db"), "user backup")
	if err := CleanupUpdateCheckpoint(ctx, home, j, nil); err == nil {
		t.Fatal("precommit cleanup succeeded")
	}
	j.Phase = "committed"
	if err := SaveUpdateJournal(home, j); err != nil {
		t.Fatal(err)
	}
	if err := CleanupUpdateCheckpoint(ctx, home, j, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(j.CandidateRoot, "bin", "mc")); err != nil {
		t.Fatal("candidate disappeared before complete")
	}
	j.Phase = "complete"
	if err := SaveUpdateJournal(home, j); err != nil {
		t.Fatal(err)
	}
	if err := RemoveUpdateArtifacts(home, j); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "backups", "user.db")); err != nil {
		t.Fatal("user backup removed")
	}
	if err := UpdateBlocksWork(home); err != nil {
		t.Fatal("cleanup complete blocks service", err)
	}
}
func TestUpdateCandidateRetainsVerifiedExecutableAndRefusesTampering(t *testing.T) {
	home, j := updateTestJournal(t)
	source := t.TempDir()
	path := filepath.Join(source, "bin", "mc")
	updateTestFile(t, path, "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := RetainUpdateCandidate(home, j, source); err != nil {
		t.Fatal(err)
	}
	if err := SaveUpdateJournal(home, j); err != nil {
		t.Fatal(err)
	}
	if err := RetainUpdateCandidate(home, j, j.CandidateRoot); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(j.CandidateRoot, "release.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record distribution.Record
	if json.Unmarshal(body, &record) != nil || record.Commit != j.Candidate.Commit {
		t.Fatal("offline release record absent")
	}
	updateTestFile(t, filepath.Join(j.CandidateRoot, "bin", "mc"), "changed")
	if err := RetainUpdateCandidate(home, j, j.CandidateRoot); err == nil {
		t.Fatal("corrupted executable accepted")
	}
}
func TestUpdateCheckpointRefusesLinkedParent(t *testing.T) {
	home, j := updateTestJournal(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home, "agentsview")); err != nil {
		t.Fatal(err)
	}
	if err := CreateUpdateCheckpoint(context.Background(), home, j, nil); err == nil {
		t.Fatal("checkpoint followed a linked parent")
	}
}

func TestUpdateCheckpointRequiresCompleteVolumeInventory(t *testing.T) {
	home, j := updateTestJournal(t)
	t.Setenv("HOME", home)
	previous := j.Candidate
	j.Previous = &previous
	c := &UpdateCheckpoint{Root: filepath.Join(UpdateDirectory(home, j.ID), "checkpoint")}
	for i, path := range updateHostPaths(home, j) {
		c.Paths = append(c.Paths, UpdatePath{Path: path, Backup: filepath.Join(c.Root, "host", fmt.Sprint(i))})
	}
	if err := c.validate(home, j); err == nil {
		t.Fatal("missing spine snapshot was accepted")
	}
	names, _ := RuntimeNames(home)
	optional, _ := OpenCodeVolume(home)
	c.Volumes = []UpdateVolume{{Name: names.Volume, Backup: names.Volume + "-update-" + j.ID, SHA256: strings.Repeat("a", 64)}}
	if err := c.validate(home, j); err == nil {
		t.Fatal("unknown optional volume state was accepted")
	}
	c.AbsentVolumes = []string{optional}
	if err := c.validate(home, j); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateRestoreRetriesAfterPartialHostExchange(t *testing.T) {
	home, j := updateTestJournal(t)
	ctx := context.Background()
	updateTestFile(t, filepath.Join(home, "config.toml"), "original")
	updateTestFile(t, filepath.Join(home, "agentsview", "token"), "saved token")
	if err := CreateUpdateCheckpoint(ctx, home, j, nil); err != nil {
		t.Fatal(err)
	}
	updateTestFile(t, filepath.Join(home, "config.toml"), "candidate")
	if err := os.RemoveAll(filepath.Join(home, "agentsview")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home, "agentsview")); err != nil {
		t.Fatal(err)
	}
	if err := RestoreUpdateCheckpoint(ctx, home, j, nil); err == nil {
		t.Fatal("unsafe later destination was accepted")
	}
	body, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	if string(body) != "original" {
		t.Fatal("fixture did not reach a partial exchange")
	}
	if err := os.Remove(filepath.Join(home, "agentsview")); err != nil {
		t.Fatal(err)
	}
	if err := RestoreUpdateCheckpoint(ctx, home, j, nil); err != nil {
		t.Fatal("restore retry:", err)
	}
	body, _ = os.ReadFile(filepath.Join(home, "agentsview", "token"))
	if string(body) != "saved token" {
		t.Fatal("later store did not restore")
	}
	files, err := os.ReadDir(outside)
	if err != nil || len(files) != 0 {
		t.Fatal("restore changed the unrelated directory")
	}
}
