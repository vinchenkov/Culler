//go:build update_docker

package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc/substrate"
)

// This test uses only scratch homes and volumes. The binary comes from the
// checkout; the existing image supplies the private Node.js and runtime files.
func TestUpdateCheckpointDockerRestoresAfterInterruptedVolumeCopy(t *testing.T) {
	image := os.Getenv("MC_UPDATE_TEST_IMAGE")
	program := os.Getenv("MC_UPDATE_TEST_BINARY")
	if image == "" || !filepath.IsAbs(program) {
		t.Fatal("Set MC_UPDATE_TEST_IMAGE and an absolute MC_UPDATE_TEST_BINARY for this opt-in test.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	home, j := updateTestJournal(t)
	userHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", userHome)
	names, _ := RuntimeNames(home)
	optional, _ := OpenCodeVolume(home)
	run := func(ctx context.Context, input string, args ...string) ([]byte, error) {
		for i, arg := range args {
			if arg == j.Candidate.Image {
				args[i] = image
			}
		}
		if len(args) > 0 && args[0] == "run" {
			args = append([]string{"run", "--mount", "type=bind,src=" + program + ",dst=/usr/local/bin/mc,readonly"}, args[1:]...)
		}
		return runRuntimeDocker(ctx, input, args...)
	}
	docker := func(input string, args ...string) []byte {
		t.Helper()
		body, err := run(ctx, input, args...)
		if err != nil {
			t.Fatalf("docker %v: %v: %s", args, err, body)
		}
		return body
	}
	volumes := []string{names.Volume, optional, names.RuntimeVolume, names.Volume + "-update-" + j.ID, optional + "-update-" + j.ID}
	t.Cleanup(func() {
		clean, done := context.WithTimeout(context.Background(), time.Minute)
		defer done()
		for _, volume := range volumes {
			_, _ = runRuntimeDocker(clean, "", "volume", "rm", volume)
		}
	})
	for _, entry := range []struct{ name, component string }{{names.Volume, "spine"}, {optional, "opencode"}} {
		docker("", "volume", "create", "--label", "mc-deployment="+j.Deployment, "--label", "mc-component="+entry.component, entry.name)
	}
	release := strings.TrimSpace(string(docker("", "run", "--rm", "--network", "none", image, "/bin/cat", RuntimeRoot+"/release-id")))
	j.Candidate.Commit, j.Candidate.ReleaseID, j.Candidate.Tag = release, release, "v"+j.Candidate.Version
	j.Candidate.Schema = substrate.CurrentSchemaID
	previous := j.Candidate
	j.Previous = &previous
	if err := SaveUpdateJournal(home, j); err != nil {
		t.Fatal(err)
	}
	spine := filepath.Join(t.TempDir(), "spine.db")
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	if err := substrate.Init(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO meta (id,deployment_uuid,schema_id) VALUES (1,?,?)", strings.Repeat("f", 32), substrate.CurrentSchemaID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	docker("", "run", "--rm", "--network", "none", "--user", "0:0", "--mount", "type=bind,src="+spine+",dst=/input.db,readonly", "--mount", "type=volume,src="+names.Volume+",dst=/destination", image, "/bin/sh", "-ec", "cp /input.db /destination/spine.db; chown -R 10002:10002 /destination")
	docker("", "run", "--rm", "--network", "none", "--mount", "type=volume,src="+optional+",dst=/destination", image, "/bin/sh", "-ec", "printf original > /destination/native-store")
	updateTestFile(t, filepath.Join(home, "config.toml"), "original configuration")
	if err := CreateUpdateCheckpoint(ctx, home, j, run); err != nil {
		t.Fatal(err)
	}
	if len(j.Checkpoint.Volumes) != 2 {
		t.Fatal("the native runtime snapshot is absent")
	}
	if err := SaveUpdateJournal(home, j); err != nil {
		t.Fatal(err)
	}
	docker("", "run", "--rm", "--network", "none", "--mount", "type=volume,src="+optional+",dst=/destination", image, "/bin/sh", "-ec", "printf candidate > /destination/native-store")
	updateTestFile(t, filepath.Join(home, "config.toml"), "candidate configuration")
	failed := false
	interrupt := func(ctx context.Context, input string, args ...string) ([]byte, error) {
		if !failed && len(args) > 0 && args[0] == "run" && strings.Contains(strings.Join(args, " "), "src="+optional+",dst=/destination") {
			failed = true
			return nil, errors.New("injected loss of the restore process")
		}
		return run(ctx, input, args...)
	}
	if err := RestoreUpdateCheckpoint(ctx, home, j, interrupt); err == nil || !failed {
		t.Fatalf("restore interruption: %v", err)
	}
	if err := RestoreUpdateCheckpoint(ctx, home, j, run); err != nil {
		t.Fatal("restore retry:", err)
	}
	body := docker("", "run", "--rm", "--network", "none", "--mount", "type=volume,src="+optional+",dst=/source,readonly", image, "/bin/cat", "/source/native-store")
	if string(body) != "original" {
		t.Fatal("optional runtime data did not restore")
	}
	body, err = os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil || string(body) != "original configuration" {
		t.Fatal("configuration did not restore", err)
	}
	checked := docker("", "run", "--rm", "--network", "none", "--user", "10002:10002", "--mount", "type=volume,src="+names.Volume+",dst=/mc/spine,readonly", image, "mc", "__release-database")
	var facts struct{ Schema, UUID string }
	if json.Unmarshal(checked, &facts) != nil || facts.Schema != substrate.CurrentSchemaID || facts.UUID != strings.Repeat("f", 32) {
		t.Fatalf("spine identity: %s", checked)
	}
	j.Phase = "restored"
	if err := SaveUpdateJournal(home, j); err != nil {
		t.Fatal(err)
	}
	if err := CleanupUpdateCheckpoint(ctx, home, j, run); err != nil {
		t.Fatal(err)
	}
}
