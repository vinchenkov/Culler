package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"mc/distribution"
)

func openCodeSourceGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

func openCodeSourceCommit(t *testing.T, root string) string {
	t.Helper()
	openCodeSourceGit(t, root, "add", ".")
	openCodeSourceGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "Test package inputs")
	return openCodeSourceGit(t, root, "rev-parse", "HEAD")
}

func openCodeSourceFixture(t *testing.T) runtimeSourceRecord {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
		"runner/opencode/package.json": "{}", "runner/opencode/package-lock.json": "{}",
		"runner/opencode/control/server.mjs": "export default {}", "runner/opencode/control/shell": "#!/bin/sh\n",
		"release/opencode/build.sh": "#!/bin/sh\n", "release/opencode/check_archive.py": "# archive check\n",
	} {
		file := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	openCodeSourceGit(t, root, "init", "--quiet")
	return runtimeSourceRecord{Source: root, Release: openCodeSourceCommit(t, root)}
}

type openCodeSourceDocker struct {
	t                  *testing.T
	home               string
	archive, checksum  string
	installedArchive   string
	content, release   string
	tools, image       string
	exists, foreign    bool
	damaged, active    bool
	buildError         string
	builds, installs   int
	verifies, metadata int
}

func newOpenCodeSourceDocker(t *testing.T, home string) *openCodeSourceDocker {
	t.Helper()
	archive, checksum := openCodeArchiveFixture(t, nil)
	return &openCodeSourceDocker{t: t, home: home, archive: archive, checksum: checksum,
		content: strings.Repeat("c", 64), image: "sha256:" + strings.Repeat("d", 64),
		tools: `{"platform":"linux-arm64","node":"v24.18.0","npm":"11.13.0"}`}
}

func (d *openCodeSourceDocker) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	d.t.Helper()
	switch strings.Join(args[:2], " ") {
	case "image inspect":
		return []byte(d.image), nil
	case "volume inspect":
		if !d.exists {
			return []byte("no such volume"), errors.New("absent")
		}
		names, _ := RuntimeNames(d.home)
		volume, _ := OpenCodeVolume(d.home)
		owner := names.Suffix
		if d.foreign {
			owner = "another-deployment"
		}
		return json.Marshal(map[string]any{"Name": volume, "Driver": "local", "Labels": map[string]string{"mc-component": "opencode", "mc-deployment": owner}})
	case "volume create":
		d.exists = true
		return nil, nil
	case "volume rm":
		d.exists = false
		return nil, nil
	case "container ls":
		if d.active {
			return []byte("agent-container"), nil
		}
		return nil, nil
	case "run --rm":
		if script := slices.Index(args, openCodeSourceProgram); script >= 0 {
			action := args[script+1]
			if !slices.Contains(args, "no-new-privileges") || !slices.Contains(args, "ALL") {
				d.t.Fatal("The source container lost its restrictions.")
			}
			switch action {
			case "tools":
				return []byte(d.tools), nil
			case "build":
				d.builds++
				if d.buildError != "" {
					return []byte(d.buildError), errors.New("package preparation failed")
				}
				var output string
				for _, arg := range args {
					if strings.HasPrefix(arg, "type=bind,src=") && strings.HasSuffix(arg, ",dst=/output") {
						output = strings.TrimSuffix(strings.TrimPrefix(arg, "type=bind,src="), ",dst=/output")
					}
				}
				if output == "" {
					d.t.Fatal("The source container lacks disposable output storage.")
				}
				body, err := os.ReadFile(d.archive)
				if err == nil {
					err = os.WriteFile(filepath.Join(output, "culler-opencode-linux-arm64.tar.gz"), body, 0600)
				}
				if err != nil {
					d.t.Fatal(err)
				}
				return json.Marshal(map[string]string{"archive_sha256": d.checksum, "content_sha256": d.content})
			case "verify":
				d.verifies++
				if !slices.Contains(args, "none") || !strings.Contains(strings.Join(args, " "), "readonly,volume-nocopy") {
					d.t.Fatal("The verification permits network access or volume writes.")
				}
				if d.damaged || !d.exists || args[script+3] != d.content || args[script+4] != d.installedArchive || args[script+5] != d.release {
					return []byte(`{"stage":"validation","cause":"The installed package differs."}`), errors.New("invalid package")
				}
				return []byte("opencode v" + args[script+6]), nil
			}
		}
		if script := slices.Index(args, openCodeInstallScript); script >= 0 {
			d.installs++
			d.release, d.damaged = args[script+2], false
			d.installedArchive = args[script+3]
			return nil, nil
		}
		if slices.Contains(args, "cat") {
			if args[len(args)-1] == OpenCodeRoot+"/archive-sha256" {
				return []byte(d.installedArchive), nil
			}
			return []byte(d.release), nil
		}
		if args[len(args)-2] == "release" {
			d.metadata++
			d.release = args[len(args)-1]
			return nil, nil
		}
	}
	d.t.Fatalf("Unexpected Docker command: %v", args)
	return nil, nil
}

func deployOpenCodeSourceFixture(t *testing.T, home string, source runtimeSourceRecord, d *openCodeSourceDocker) error {
	t.Helper()
	if err := ConfigureRuntimeSource(home, filepath.Join(source.Source, "runner"), source.Release); err != nil {
		t.Fatal(err)
	}
	return updateOpenCodeVolume(context.Background(), home, "mc-prod", source.Release, d.run)
}

func TestOpenCodeSourcePreparesBeforeUseAndReusesAcrossCommits(t *testing.T) {
	home, source := agentProfileTestHome(t), openCodeSourceFixture(t)
	d := newOpenCodeSourceDocker(t, home)
	if err := deployOpenCodeSourceFixture(t, home, source, d); err != nil {
		t.Fatal(err)
	}
	if status := OpenCodeDownloadStatus(home); status == nil || status.Status != "succeeded" || d.builds != 1 || d.installs != 1 {
		t.Fatalf("The source package is not ready: %+v, %+v", status, d)
	}
	d.buildError = `{"stage":"download","cause":"ENETUNREACH"}`
	source.Release = openCodeSourceCommit(t, source.Source)
	d.image = "sha256:" + strings.Repeat("e", 64)
	if err := deployOpenCodeSourceFixture(t, home, source, d); err != nil {
		t.Fatal(err)
	}
	if d.builds != 1 || d.installs != 1 || d.metadata != 1 || d.release != source.Release {
		t.Fatalf("The new commit failed package reuse: %+v", d)
	}
	moved := filepath.Join(t.TempDir(), "source")
	if err := os.Rename(source.Source, moved); err != nil {
		t.Fatal(err)
	}
	source.Source = moved
	if err := os.Chtimes(filepath.Join(moved, "runner/opencode/package.json"), time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	root, _ := agentProfileOperationsRoot(home)
	if err := recordOpenCodeSourceStatus(root, "failed", "An earlier download failed.", "opencode_download"); err != nil {
		t.Fatal(err)
	}
	if err := deployOpenCodeSourceFixture(t, home, source, d); err != nil {
		t.Fatal(err)
	}
	status := OpenCodeDownloadStatus(home)
	if d.builds != 1 || status.Status != "succeeded" || status.ErrorCode != "" {
		t.Fatalf("Verification retained a stale failure: %+v", status)
	}
	if err := os.Rename(moved, moved+"-absent"); err != nil {
		t.Fatal(err)
	}
	operation, err := startOpenCodeDownload(home, false, func(string, string) error {
		t.Fatal("The first connection started another download.")
		return nil
	})
	if err != nil || operation.Status != "succeeded" {
		t.Fatal(operation, err)
	}
}

func TestOpenCodeSourceInputChangesRequirePreparation(t *testing.T) {
	for _, file := range []string{"runner/opencode/package.json", "runner/opencode/package-lock.json", "runner/opencode/control/server.mjs", "runner/opencode/control/shell", "release/opencode/build.sh", "release/opencode/check_archive.py", "tools"} {
		t.Run(file, func(t *testing.T) {
			home, source := agentProfileTestHome(t), openCodeSourceFixture(t)
			d := newOpenCodeSourceDocker(t, home)
			if err := deployOpenCodeSourceFixture(t, home, source, d); err != nil {
				t.Fatal(err)
			}
			if file == "tools" {
				d.tools = `{"platform":"linux-arm64","node":"v25.0.0","npm":"12.0.0"}`
			} else {
				if err := os.WriteFile(filepath.Join(source.Source, file), []byte("changed input"), 0644); err != nil {
					t.Fatal(err)
				}
				source.Release = openCodeSourceCommit(t, source.Source)
			}
			if err := deployOpenCodeSourceFixture(t, home, source, d); err != nil {
				t.Fatal(err)
			}
			if d.builds != 2 || d.installs != 2 {
				t.Fatalf("The package reused different inputs: %+v", d)
			}
		})
	}
}

func TestOpenCodeSourceRepairsAbsentDamagedAndLegacyVolumes(t *testing.T) {
	for _, damage := range []string{"absent", "damaged", "legacy", "partial"} {
		t.Run(damage, func(t *testing.T) {
			home, source := agentProfileTestHome(t), openCodeSourceFixture(t)
			d := newOpenCodeSourceDocker(t, home)
			if err := deployOpenCodeSourceFixture(t, home, source, d); err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "absent":
				d.exists = false
			case "damaged":
				d.damaged = true
			case "legacy":
				if err := os.Remove(filepath.Join(home, "state/opencode-source-package.json")); err != nil {
					t.Fatal(err)
				}
			case "partial":
				d.release = ""
			}
			if err := deployOpenCodeSourceFixture(t, home, source, d); err != nil {
				t.Fatal(err)
			}
			if d.installs != 2 || OpenCodeDownloadStatus(home).Status != "succeeded" {
				t.Fatal("The damaged package did not receive a verified replacement.")
			}
		})
	}
}

func TestOpenCodeSourceFailurePreservesVolumeAndRetryRepairsStatus(t *testing.T) {
	for _, stage := range []string{"download", "preparation", "validation"} {
		t.Run(stage, func(t *testing.T) {
			home, source := agentProfileTestHome(t), openCodeSourceFixture(t)
			d := newOpenCodeSourceDocker(t, home)
			d.exists, d.release = true, "previous-release"
			d.buildError = `{"stage":"` + stage + `","cause":"specific failure cause"}`
			err := deployOpenCodeSourceFixture(t, home, source, d)
			status := OpenCodeDownloadStatus(home)
			if err == nil || !strings.Contains(err.Error(), "specific failure cause") || status.Status != "failed" || status.ErrorCode != "opencode_"+stage {
				t.Fatalf("The failure lost its cause: %+v, %v", status, err)
			}
			if !d.exists || d.release != "previous-release" || d.installs != 0 {
				t.Fatal("The failed preparation changed the previous volume.")
			}
			d.buildError = ""
			if err := deployOpenCodeSourceFixture(t, home, source, d); err != nil {
				t.Fatal(err)
			}
			if status := OpenCodeDownloadStatus(home); status.Status != "succeeded" || status.ErrorCode != "" {
				t.Fatalf("The retry retained the failure: %+v", status)
			}
		})
	}
}

func TestOpenCodeSourceRefusesForeignVolumeAndActiveAgents(t *testing.T) {
	for _, foreign := range []bool{true, false} {
		home, source := agentProfileTestHome(t), openCodeSourceFixture(t)
		d := newOpenCodeSourceDocker(t, home)
		d.exists, d.foreign, d.active, d.release = true, foreign, !foreign, "previous"
		if err := deployOpenCodeSourceFixture(t, home, source, d); err == nil {
			t.Fatal("The source deployment changed an unavailable volume.")
		}
		if !d.exists || d.installs != 0 || d.release != "previous" || (foreign && d.builds != 0) {
			t.Fatalf("The refused volume changed: %+v", d)
		}
	}
}

func TestOpenCodeSourceRejectsUncommittedInputs(t *testing.T) {
	home, source := agentProfileTestHome(t), openCodeSourceFixture(t)
	d := newOpenCodeSourceDocker(t, home)
	if err := os.WriteFile(filepath.Join(source.Source, "runner/opencode/package.json"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := deployOpenCodeSourceFixture(t, home, source, d); err == nil || !strings.Contains(err.Error(), "differ from the selected commit") {
		t.Fatal(err)
	}
	if d.builds != 0 || d.installs != 0 {
		t.Fatal("The changed checkout supplied package inputs.")
	}
}

func TestOpenCodeSourceConcurrentPreparationUsesOnePackage(t *testing.T) {
	home, source := agentProfileTestHome(t), openCodeSourceFixture(t)
	d := newOpenCodeSourceDocker(t, home)
	if err := ConfigureRuntimeSource(home, filepath.Join(source.Source, "runner"), source.Release); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := updateOpenCodeVolume(context.Background(), home, "mc-prod", source.Release, d.run); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	if d.builds != 1 || d.installs != 1 || OpenCodeDownloadStatus(home).Status != "succeeded" {
		t.Fatal("Concurrent requests did not reuse the verified package.")
	}
}

func openCodePublishedFixture(t *testing.T, home, file string) distribution.Record {
	t.Helper()
	record := distribution.Record{Format: 1, Version: "0.1.0", Commit: strings.Repeat("a", 40), ReleaseID: strings.Repeat("a", 40), Tag: "v0.1.0",
		Sequence: 1, PublishedAt: time.Now(), Platform: "darwin-arm64", Schema: strings.Repeat("a", 64),
		Runtimes:  distribution.RuntimeVersions{Bun: "1.3.12", Go: "1.25.12"},
		Image:     "ghcr.io/test/culler@sha256:" + strings.Repeat("a", 64),
		Installer: distribution.Asset{Name: "installer.sh", Size: 1, SHA256: strings.Repeat("a", 64)},
		Package:   distribution.Asset{Name: "culler-darwin-arm64.tar.gz", Size: 1, SHA256: strings.Repeat("a", 64)}}
	for _, name := range distribution.RequiredChecks {
		record.Checks = append(record.Checks, distribution.Check{Name: name, Passed: true})
	}
	if err := distribution.Save(filepath.Join(home, file), record); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestOpenCodePublishedContextTakesPrecedenceOverSource(t *testing.T) {
	for _, file := range []string{distribution.InstalledRecord, "state/pending-release.json"} {
		t.Run(file, func(t *testing.T) {
			home, source := agentProfileTestHome(t), openCodeSourceFixture(t)
			if err := ConfigureRuntimeSource(home, filepath.Join(source.Source, "runner"), source.Release); err != nil {
				t.Fatal(err)
			}
			record := openCodePublishedFixture(t, home, file)
			d := newOpenCodeSourceDocker(t, home)
			if err := updateOpenCodeVolume(context.Background(), home, record.Image, record.Commit, d.run); err != nil {
				t.Fatal(err)
			}
			if d.exists || d.builds != 0 || OpenCodeDownloadStatus(home) != nil {
				t.Fatal("The published installation prepared an optional package.")
			}
			// The absent release archive must fail through the published path, without a source build.
			err := downloadOpenCodeWithDocker(context.Background(), home, record.Image, func(string) {}, d.run)
			if err == nil || !strings.Contains(err.Error(), "installed release lacks its OpenCode archive") || d.builds != 0 {
				t.Fatalf("The published download selected the source: %v", err)
			}
			selected, err := openCodeSourceForImage(home, "mc-prod")
			if err != nil || selected == nil || selected.Release != source.Release {
				t.Fatal("The previous published record blocked a source deployment.")
			}
		})
	}
}

func TestOpenCodePublishedUpdateUsesRetainedArchiveAfterSource(t *testing.T) {
	home, source := agentProfileTestHome(t), openCodeSourceFixture(t)
	if err := ConfigureRuntimeSource(home, filepath.Join(source.Source, "runner"), source.Release); err != nil {
		t.Fatal(err)
	}
	d := newOpenCodeSourceDocker(t, home)
	record := openCodePublishedFixture(t, home, distribution.InstalledRecord)
	info, err := os.Stat(d.archive)
	if err != nil {
		t.Fatal(err)
	}
	record.OpenCode = &distribution.Asset{Name: "culler-opencode-linux-arm64.tar.gz", SHA256: d.checksum, Size: info.Size()}
	names, _ := RuntimeNames(home)
	journal := &UpdateJournal{Version: 1, ID: strings.Repeat("b", 32), Deployment: names.Suffix, Phase: "replace", Candidate: record, Previous: &record}
	journal.CandidateRoot = filepath.Join(UpdateDirectory(home, journal.ID), "candidate")
	if err := SaveUpdateJournal(home, journal); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(d.archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(UpdateDirectory(home, journal.ID), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(UpdateDirectory(home, journal.ID), record.OpenCode.Name), body, 0600); err != nil {
		t.Fatal(err)
	}
	d.exists, d.release, d.installedArchive = true, record.Commit, strings.Repeat("0", 64)
	for range 2 {
		if err := updateOpenCodeVolume(context.Background(), home, record.Image, record.Commit, d.run); err != nil {
			t.Fatal(err)
		}
	}
	if d.builds != 0 || d.installs != 1 || d.installedArchive != record.OpenCode.SHA256 {
		t.Fatal("The source archive replaced the required published archive.")
	}
	journal.Phase = "restore"
	if err := SaveUpdateJournal(home, journal); err != nil {
		t.Fatal(err)
	}
	selected, err := openCodeSourceForImage(home, "mc-prod")
	if selected != nil || err != nil {
		t.Fatal("Published recovery selected the retained source.")
	}
}

func TestOpenCodePublishedInstallDropsSourceReadinessWithoutVolume(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "ready", true: "failed"}[failed], func(t *testing.T) {
			home, source := agentProfileTestHome(t), openCodeSourceFixture(t)
			d := newOpenCodeSourceDocker(t, home)
			if failed {
				d.buildError = `{"stage":"download","cause":"ENETUNREACH"}`
			}
			if err := deployOpenCodeSourceFixture(t, home, source, d); (err != nil) != failed {
				t.Fatalf("The source fixture returned an unexpected result: %v", err)
			}
			d.exists = false
			record := openCodePublishedFixture(t, home, distribution.InstalledRecord)
			if err := ConfigureRuntimeSource(home, filepath.Join(t.TempDir(), "runner"), record.Commit); err != nil {
				t.Fatal(err)
			}
			if err := updateOpenCodeVolume(context.Background(), home, record.Image, record.Commit, d.run); err != nil {
				t.Fatal(err)
			}
			if OpenCodeDownloadStatus(home) != nil || d.exists || d.builds != 1 {
				t.Fatal("The published installation retained readiness without its source volume.")
			}
			launches := 0
			operation, err := startOpenCodeDownload(home, false, func(string, string) error { launches++; return nil })
			if err != nil || operation.Status != "pending" || launches != 1 {
				t.Fatal("The first published connection failed to request its archive.")
			}
		})
	}
}

func TestOpenCodePublishedInstallRechecksACompletedSourceWorker(t *testing.T) {
	home, source := agentProfileTestHome(t), openCodeSourceFixture(t)
	d := newOpenCodeSourceDocker(t, home)
	if err := deployOpenCodeSourceFixture(t, home, source, d); err != nil {
		t.Fatal(err)
	}
	record := openCodePublishedFixture(t, home, distribution.InstalledRecord)
	first := true
	run := func(ctx context.Context, stdin string, args ...string) ([]byte, error) {
		if first && args[0] == "volume" && args[1] == "inspect" {
			first = false
			return []byte("no such volume"), errors.New("absent")
		}
		return d.run(ctx, stdin, args...)
	}
	err := updateOpenCodeVolume(context.Background(), home, record.Image, record.Commit, run)
	if err == nil || !strings.Contains(err.Error(), "installed release lacks its OpenCode archive") {
		t.Fatalf("The source worker satisfied a different published release: %v", err)
	}
}
