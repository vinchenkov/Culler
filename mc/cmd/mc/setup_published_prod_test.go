//go:build !test_fake_routing

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	operatorconfig "mc/config"
	"mc/deployment"
	"mc/distribution"
	"mc/onboarding"
	"mc/verbs"
)

func TestPublishedSetupChecksBeforeServiceStop(t *testing.T) {
	for _, refuse := range []bool{false, true} {
		t.Run(map[bool]string{false: "compatible", true: "refused"}[refuse], func(t *testing.T) {
			answers := []string{"update"}
			if refuse {
				answers = append(answers, ":exit")
			}
			w, _ := setupTestWizard(t, answers...)
			state := completeSetupDeployment()
			state.Update = true
			w.deps.inspect = func(string, verbs.OnboardArgs) (setupDeployment, error) { return state, nil }
			prepared := false
			stopped := false
			w.deps.beforeChange = func(string, verbs.OnboardArgs, func(string)) error {
				prepared = true
				if refuse {
					return errors.New("incompatible database")
				}
				return nil
			}
			w.deps.stop = func(string) error {
				if !prepared {
					t.Fatal("stop preceded image/schema preflight")
				}
				stopped = true
				return nil
			}
			_, err := w.inspectExistingDeployment()
			if refuse {
				if !errors.Is(err, onboarding.ErrExit) || stopped {
					t.Fatalf("refusal: stopped=%v err=%v", stopped, err)
				}
			} else if err != nil || !stopped {
				t.Fatalf("update: stopped=%v err=%v", stopped, err)
			}
		})
	}
}

func TestPublishedSetupLockRejectsConcurrentAndUnsafeFiles(t *testing.T) {
	home := t.TempDir()
	lock, err := lockPublishedSetup(home)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := lockPublishedSetup(home); err == nil {
		second.Close()
		t.Fatal("second installer acquired lock")
	}
	lock.Close()
	lock, err = lockPublishedSetup(home)
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	path := filepath.Join(home, "install.lock")
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if second, err := lockPublishedSetup(home); err == nil {
		second.Close()
		t.Fatal("unsafe lock accepted")
	}
}

func TestPublishedImageRequiresDigestCommitAndPlatform(t *testing.T) {
	r := distribution.Record{Commit: strings.Repeat("a", 40), Image: "ghcr.io/test/culler@sha256:" + strings.Repeat("b", 64)}
	image := map[string]any{"Id": "sha256:" + strings.Repeat("c", 64), "Os": "linux", "Architecture": "arm64", "RepoDigests": []string{r.Image}, "Config": map[string]any{"Labels": map[string]string{"org.opencontainers.image.revision": r.Commit}}}
	encode := func() []byte { body, _ := json.Marshal(image); return body }
	if err := validatePublishedImage(encode(), r); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]any{"Os": "darwin", "Architecture": "amd64", "RepoDigests": []string{"other"}, "Config": map[string]any{"Labels": map[string]string{"org.opencontainers.image.revision": "wrong"}}} {
		original := image[key]
		image[key] = value
		if err := validatePublishedImage(encode(), r); err == nil {
			t.Fatalf("accepted wrong %s", key)
		}
		image[key] = original
	}
}

func TestPublishedDatabaseCheckUsesReadOnlyTargetImage(t *testing.T) {
	home := t.TempDir()
	r := distribution.Record{Schema: "schema", Image: "ghcr.io/test/culler@sha256:" + strings.Repeat("a", 64)}
	p := &publishedSetup{home: home, record: r}
	p.docker = func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "volume" {
			return json.Marshal(map[string]any{"Name": args[len(args)-1], "Driver": "local"})
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, ",dst=/mc/spine,readonly") || !strings.Contains(joined, r.Image+" mc __release-database") || strings.Contains(joined, "--allow-database-reset") {
			t.Fatalf("unsafe database check: %v", args)
		}
		return json.Marshal(verbs.ReleaseDatabase{Schema: r.Schema, Empty: true})
	}
	if _, err := p.database(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPublishedMaintenanceRequiresMatchingReceiptAndIdleDatabase(t *testing.T) {
	for _, busy := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "busy"}[busy], func(t *testing.T) {
			home := t.TempDir()
			if err := os.MkdirAll(filepath.Join(home, "state"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, distribution.InstalledRecord), []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			clock := time.Now()
			calls := 0
			for _, name := range []string{"install-maintenance.json", "install-maintenance-ack.json"} {
				if err := writePublishedState(filepath.Join(home, "state", name), map[string]any{"id": "stale-request", "ready": true, "observed_at": clock}); err != nil {
					t.Fatal(err)
				}
			}
			p := &publishedSetup{home: home, record: distribution.Record{Schema: "schema"}, maintenanceID: "stale-request", now: func() time.Time { return clock }, states: func(string) (map[string]bool, error) { return map[string]bool{"resident": true}, nil }}
			firstWait := true
			p.sleep = func(time.Duration) {
				if firstWait && calls != 0 {
					t.Fatal("stale maintenance receipt authorized an update")
				}
				firstWait = false
				clock = clock.Add(time.Minute)
				_ = writePublishedState(filepath.Join(home, "state", "install-maintenance-ack.json"), map[string]any{"id": p.maintenanceID, "ready": true, "observed_at": clock})
			}
			p.docker = func(_ context.Context, args ...string) ([]byte, error) {
				if args[0] == "volume" {
					return json.Marshal(map[string]any{"Name": args[len(args)-1], "Driver": "local"})
				}
				if args[0] == "container" {
					return nil, nil
				}
				calls++
				count := 0
				if busy {
					count = 1
				}
				return json.Marshal(verbs.ReleaseDatabase{Schema: "schema", Runs: count, Empty: true})
			}
			err := p.drain(context.Background())
			p.close()
			if p.maintenanceID == "stale-request" {
				t.Fatal("update reused the stale maintenance request")
			}
			if (err != nil) != busy || calls == 0 {
				t.Fatalf("drain busy=%v calls=%d err=%v", busy, calls, err)
			}
			if _, err := os.Stat(filepath.Join(home, "state", "install-maintenance.json")); !os.IsNotExist(err) {
				t.Fatal("maintenance request survived normal exit")
			}
		})
	}
}

func publishedTestRecord(commit string, sequence int64) distribution.Record {
	version := "0." + strconv.FormatInt(sequence, 10) + ".0"
	r := distribution.Record{Format: 1, Version: version, Commit: commit, ReleaseID: commit, Tag: "v" + version, Sequence: sequence, PublishedAt: time.Now().UTC(), Platform: "darwin-arm64", Schema: strings.Repeat("c", 64), Image: "ghcr.io/test/culler@sha256:" + strings.Repeat("d", 64), Runtimes: distribution.RuntimeVersions{Bun: "1.3.9", Go: "1.25.12"}, Installer: distribution.Asset{Name: "installer.sh", SHA256: strings.Repeat("e", 64), Size: 1}, Package: distribution.Asset{Name: "culler-darwin-arm64.tar.gz", SHA256: strings.Repeat("f", 64), Size: 1},
		HostBun: &distribution.HostBunRelease{Version: "1.3.9", Archives: map[string]distribution.HostBunArchive{distribution.HostBunPlatform: {Name: "bun-darwin-aarch64.zip", URL: "https://github.com/oven-sh/bun/releases/download/bun-v1.3.9/bun-darwin-aarch64.zip", Size: 1, SHA256: strings.Repeat("a", 64)}}}}
	for _, name := range distribution.RequiredChecks {
		r.Checks = append(r.Checks, distribution.Check{Name: name, Passed: true})
	}
	return r
}

func TestPublishedSetupRefusesChangedIdentityBeforeDownloads(t *testing.T) {
	for _, scenario := range []string{"older", "sequence collision", "changed image", "changed package"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			prior := publishedTestRecord(strings.Repeat("a", 40), 20)
			next := prior
			switch scenario {
			case "older":
				next.Sequence = 19
				next.Commit = strings.Repeat("b", 40)
			case "sequence collision":
				next.Commit = strings.Repeat("b", 40)
			case "changed image":
				next.Image = "ghcr.io/test/culler@sha256:" + strings.Repeat("c", 64)
			case "changed package":
				next.Package.SHA256 = strings.Repeat("c", 64)
			}
			if err := distribution.Save(filepath.Join(home, distribution.InstalledRecord), prior); err != nil {
				t.Fatal(err)
			}
			p := publishedSetup{home: home, record: next, pull: func(context.Context, distribution.Record, func(string)) error {
				t.Fatal("refused identity downloaded image")
				return nil
			}}
			if err := p.prepare(context.Background(), func(string) {}); err == nil {
				t.Fatal("changed identity passed")
			}
		})
	}
}

func TestPublishedSetupRetainsVerifiedMetadataUntilReadiness(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	prior := publishedTestRecord(strings.Repeat("a", 40), 20)
	next := publishedTestRecord(strings.Repeat("b", 40), 21)
	if err := distribution.Save(filepath.Join(home, distribution.InstalledRecord), prior); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "install.sh"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	clock := time.Now()
	p := publishedSetup{home: home, root: root, record: next, started: clock.Add(-time.Minute), now: func() time.Time { return clock }}
	if err := p.stageInstalled(); err != nil {
		t.Fatal(err)
	}
	installed, err := distribution.Load(filepath.Join(home, distribution.InstalledRecord))
	if err != nil || installed.Commit != prior.Commit {
		t.Fatalf("premature publication: %#v %v", installed, err)
	}
	pending, err := distribution.Load(filepath.Join(home, "state", "pending-release.json"))
	if err != nil || pending.Commit != next.Commit {
		t.Fatalf("recovery record: %#v %v", pending, err)
	}
	p.close()
	recovered := publishedSetup{home: home, record: next, started: p.started, now: p.now, docker: publishedDockerUnavailable}
	ready := false
	deps := setupDependencies{ready: func(string) ([]string, error) {
		if !ready {
			return nil, errors.New("services are not ready")
		}
		return nil, nil
	}}
	recovered.configure(&deps)
	if _, err := deps.ready(home); err == nil {
		t.Fatal("incomplete recovery passed readiness")
	}
	installed, err = distribution.Load(filepath.Join(home, distribution.InstalledRecord))
	if err != nil || installed.Commit != prior.Commit {
		t.Fatalf("failed recovery changed verified release: %#v %v", installed, err)
	}
	ready = true
	if _, err := deps.ready(home); err != nil {
		t.Fatal(err)
	}
	installed, err = distribution.Load(filepath.Join(home, distribution.InstalledRecord))
	if err != nil || installed.Commit != next.Commit {
		t.Fatalf("completion: %#v %v", installed, err)
	}
	body, err := os.ReadFile(filepath.Join(home, "state", "install-operation.json"))
	if err != nil {
		t.Fatal(err)
	}
	var operation struct {
		Phase    string  `json:"phase"`
		Duration float64 `json:"duration_seconds"`
	}
	if json.Unmarshal(body, &operation) != nil || operation.Phase != "complete" || operation.Duration != 60 {
		t.Fatalf("duration receipt: %s", body)
	}
}

func TestPublishedAutomaticUpdatePreservesOperatorConfiguration(t *testing.T) {
	w, _ := setupTestWizard(t)
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Runtimes.EnabledBindings = []string{"chatgpt", "claude"}
	cfg.Git.AuthorName, cfg.Git.AuthorEmail = "Fixture Operator", "fixture@example.test"
	cfg.Surfaces.Dashboard.Bind = "127.0.0.1:7444"
	path := filepath.Join(w.home, "config.toml")
	if _, err := operatorconfig.WriteAtomic(path, cfg); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	p := publishedSetup{home: w.home}
	args, err := p.automaticArgs([]string{"--yes", "--activate"}, verbs.OnboardArgs{ActivateSupervision: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := parseOnboardArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.RuntimeBindings != "chatgpt,claude" || resolved.GitAuthorName != cfg.Git.AuthorName || resolved.GitAuthorEmail != cfg.Git.AuthorEmail || resolved.DashboardBind != cfg.Surfaces.Dashboard.Bind || resolved.RoutingFile != filepath.Join(w.home, "routing.md") || resolved.DiscordMode != "" {
		t.Fatalf("update choices changed: %#v", resolved)
	}
	for _, field := range setupTunableFields(cfg) {
		if setupTunableAnswers(resolved)[field.name] != field.value {
			t.Fatalf("changed %s", field.name)
		}
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || strings.Contains(strings.Join(args, " "), cfg.Surfaces.Dashboard.AuthToken) {
		t.Fatal("update changed configuration or exposed the dashboard credential")
	}
}

func TestPublishedMaintenanceCleanupRetainsOtherRequests(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "state", "install-maintenance.json")
	if err := writePublishedState(path, map[string]string{"id": "new-request"}); err != nil {
		t.Fatal(err)
	}
	p := publishedSetup{home: home, maintenanceID: "old-request"}
	p.releaseMaintenance()
	if _, err := os.Stat(path); err != nil {
		t.Fatal("cleanup removed another request")
	}
	p.maintenanceID = "new-request"
	p.close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("cleanup retained its own request")
	}
}

func TestPublishedSetupRepairsSameRelease(t *testing.T) {
	home := t.TempDir()
	record := publishedTestRecord(strings.Repeat("a", 40), 20)
	if err := distribution.Save(filepath.Join(home, distribution.InstalledRecord), record); err != nil {
		t.Fatal(err)
	}
	pulls := 0
	p := publishedSetup{home: home, record: record, pull: func(_ context.Context, selected distribution.Record, _ func(string)) error {
		pulls++
		if !samePublishedRecord(selected, record) {
			t.Fatal("repair changed the release")
		}
		return nil
	}, docker: func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] != "volume" {
			t.Fatal("absent database acquired a container")
		}
		return []byte("No such volume"), errors.New("absent")
	}}
	if err := p.prepare(context.Background(), func(string) {}); err != nil {
		t.Fatal(err)
	}
	if !p.prepared || pulls != 1 {
		t.Fatalf("repair: prepared=%v pulls=%d", p.prepared, pulls)
	}
}

func TestPublishedSetupRestoresServicesIfWorkRacesStop(t *testing.T) {
	home := t.TempDir()
	stopped, restored := false, false
	p := publishedSetup{home: home, record: distribution.Record{Schema: "schema"}, states: func(string) (map[string]bool, error) {
		return map[string]bool{"resident": true, "dashboard": false}, nil
	}, restore: func(_ string, states map[string]bool) error {
		restored = states["resident"] && !states["dashboard"]
		return nil
	}}
	p.docker = func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "volume" {
			return json.Marshal(map[string]any{"Name": args[len(args)-1], "Driver": "local"})
		}
		if !stopped {
			t.Fatal("post-stop check preceded stop")
		}
		return json.Marshal(verbs.ReleaseDatabase{Schema: "schema", Empty: true, Runs: 1})
	}
	err := p.stopAndRecheck(context.Background(), func(string) error { stopped = true; return nil })
	if err == nil || !restored {
		t.Fatalf("race: restored=%v err=%v", restored, err)
	}
}

func TestPublishedSetupRequiresExactHomieAbsenceAndAllVolumeUsers(t *testing.T) {
	for _, exists := range []bool{false, true} {
		home := t.TempDir()
		id := strings.Repeat("a", 64)
		listed := false
		p := publishedSetup{home: home, docker: func(_ context.Context, args ...string) ([]byte, error) {
			if args[1] == "inspect" {
				if args[len(args)-1] != id {
					t.Fatal("wrong Homie container")
				}
				if exists {
					return []byte(`{}`), nil
				}
				return []byte("No such container"), errors.New("absent")
			}
			listed = true
			if !strings.Contains(strings.Join(args, " "), "ls --all") {
				t.Fatal("created containers escaped the check")
			}
			return nil, nil
		}}
		err := p.requireIdleContainers(context.Background(), id)
		if (err != nil) != exists || listed == exists {
			t.Fatalf("container exists=%v listed=%v err=%v", exists, listed, err)
		}
	}
}

func TestPublishedDockerEndpointRetainsCurrentContext(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("DOCKER_HOST", "unix:///wrong.sock")
	t.Setenv("DOCKER_CONTEXT", "desktop-linux")
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\n[ \"$1 $2\" = 'context inspect' ] || exit 2\nprintf '%s\\n' '\"unix:///fixture/docker.sock\"'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	endpoint, err := publishedDockerEndpoint(context.Background())
	if err != nil || endpoint != "unix:///fixture/docker.sock" {
		t.Fatalf("Docker endpoint: %q %v", endpoint, err)
	}
	t.Setenv("DOCKER_CONTEXT", "")
	endpoint, err = publishedDockerEndpoint(context.Background())
	if err != nil || endpoint != "unix:///wrong.sock" {
		t.Fatalf("explicit Docker endpoint: %q %v", endpoint, err)
	}
}

func TestPublishedInspectionUsesImageRevision(t *testing.T) {
	home := t.TempDir()
	prior := publishedTestRecord(strings.Repeat("a", 40), 20)
	proposed := publishedTestRecord(strings.Repeat("b", 40), 21)
	prior.Version, prior.Tag, proposed.Version, proposed.Tag = "0.1.0", "v0.1.0", "0.2.0", "v0.2.0"
	if err := distribution.Save(filepath.Join(home, distribution.InstalledRecord), prior); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "release.json")
	if err := distribution.Save(path, proposed); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	for _, revision := range []string{proposed.Commit, prior.Commit} {
		body, _ := json.Marshal(map[string]any{"Id": "sha256:" + strings.Repeat("c", 64), "Os": "linux", "Architecture": "arm64", "RepoDigests": []string{proposed.Image}, "Config": map[string]any{"Labels": map[string]string{"org.opencontainers.image.revision": revision}}})
		if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nprintf '%s\\n' '"+string(body)+"'\n"), 0755); err != nil {
			t.Fatal(err)
		}
		state := inspectPublishedSetupRelease(home, verbs.OnboardArgs{}, onboarding.DockerStatus{State: onboarding.DockerReady}, path)
		if state.InstalledDisplay != "0.1.0 (aaaaaaaaaaaa)" || state.ProposedDisplay != "0.2.0 (bbbbbbbbbbbb)" {
			t.Fatalf("release labels: %q %q", state.InstalledDisplay, state.ProposedDisplay)
		}
		found := false
		for _, component := range state.Components {
			if component.Name != "Agent image" {
				continue
			}
			found = true
			want := "unknown"
			if revision == proposed.Commit {
				want = proposed.Commit
			}
			if component.Installed != want {
				t.Fatalf("image revision=%q want=%q", component.Installed, want)
			}
		}
		if !found {
			t.Fatal("image component absent")
		}
	}
}

func TestPublishedCompletionDurationIncludesLauncherWork(t *testing.T) {
	finished := time.Date(2026, time.September, 26, 12, 5, 0, 0, time.UTC)
	invoked := finished.Add(-3 * time.Minute)
	setupStarted := finished.Add(-30 * time.Second)
	p := publishedSetup{
		home: t.TempDir(), record: publishedTestRecord(strings.Repeat("a", 40), 20),
		started: publishedInstallStartedAt(strconv.FormatInt(invoked.Unix(), 10), setupStarted),
		now:     func() time.Time { return finished }, docker: publishedDockerUnavailable,
	}
	if err := p.complete(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(p.home, "state", "install-operation.json"))
	if err != nil {
		t.Fatal(err)
	}
	var operation struct {
		Started  time.Time `json:"started_at"`
		Duration float64   `json:"duration_seconds"`
	}
	if err := json.Unmarshal(body, &operation); err != nil || !operation.Started.Equal(invoked) || operation.Duration != 180 {
		t.Fatalf("completion omitted launcher work: %s (%v)", body, err)
	}
	for _, value := range []string{"", "invalid", "-1", "0", "9223372036854775808", strconv.FormatInt(setupStarted.Add(time.Second).Unix(), 10)} {
		if started := publishedInstallStartedAt(value, setupStarted); !started.Equal(setupStarted) {
			t.Fatalf("invalid timestamp %q selected %v", value, started)
		}
	}
}

func TestConnectionTestUsesTheAgentImageOfTheInstall(t *testing.T) {
	pending := publishedTestRecord(strings.Repeat("a", 40), 1)
	for name, want := range map[string]string{"published": pending.Image, "source": productionImageRef} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("MC_HOME", home)
			t.Setenv("CULLER_RELEASE_RECORD", "")
			previous := releaseBuildID
			releaseBuildID = pending.Commit
			t.Cleanup(func() { releaseBuildID = previous })
			if name == "published" {
				if err := distribution.Save(filepath.Join(home, "state", "pending-release.json"), pending); err != nil {
					t.Fatal(err)
				}
			}
			verifier, err := runtimeAuthVerifier(context.Background(), home)
			if err != nil {
				t.Fatal(err)
			}
			adapter, ok := verifier.(*deployment.AdapterNoopVerifier)
			if !ok {
				t.Fatalf("The connection test uses verifier %T", verifier)
			}
			if adapter.Image != want {
				t.Fatalf("The connection test uses image %q, want %q", adapter.Image, want)
			}
		})
	}
}

func TestPublishedAutomaticSetupRefusesAnUndefinedBindingBeforeTheDownload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MC_HOME", home)
	p := &publishedSetup{home: home, record: publishedTestRecord(strings.Repeat("a", 40), 20),
		pull: func(context.Context, distribution.Record, func(string)) error {
			t.Fatal("an undefined binding reached the download")
			return nil
		},
		docker: func(context.Context, ...string) ([]byte, error) {
			t.Fatal("an undefined binding reached Docker")
			return nil, nil
		}}
	raw := []string{"--yes", "--activate", "--runtime-bindings", "chatgpt,endpoint",
		"--accept-default-routing", "--accept-default-tunables", "--accept-default-git-identity",
		"--accept-default-dashboard-bind", "--discord-mode", "disabled"}
	a, err := parseTerminalSetupArgs(raw)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := p.runAutomatic(raw, a, strings.NewReader(""), &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("result = %q: %v", stdout.String(), err)
	}
	errorBody, _ := result["error"].(map[string]any)
	const want = `--runtime-bindings names "endpoint", an undefined binding. The defined bindings are: chatgpt, claude. Use one of these ids.`
	if errorBody["message"] != want {
		t.Fatalf("error = %v, want %q", result["error"], want)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatalf("the refused setup changed the deployment home: %v, %v", entries, err)
	}
}

type restoreController struct {
	fail       string
	bootstraps []string
}

func (c *restoreController) Loaded(string) (bool, error) { return false, nil }
func (c *restoreController) Bootout(string) error        { return errors.New("unexpected service stop") }
func (c *restoreController) Bootstrap(plist string) error {
	unit := strings.TrimSuffix(filepath.Base(plist), ".plist")
	c.bootstraps = append(c.bootstraps, unit)
	if unit == c.fail {
		return errors.New("injected bootstrap failure of " + unit)
	}
	return nil
}

func TestPublishedRestoreStartsEachUnitThatItCan(t *testing.T) {
	home := t.TempDir()
	all := map[string]bool{"resident": true, "dashboard": true, "agentsview": true}

	controller := &restoreController{}
	err := restorePublishedServicesWith(home, all, controller)
	if err == nil || !strings.Contains(err.Error(), "validate prepared supervision") {
		t.Fatalf("an AgentsView configuration failure = %v", err)
	}
	if strings.Join(controller.bootstraps, ",") != "dashboard,resident" {
		t.Fatalf("an AgentsView failure stopped the restore: %v", controller.bootstraps)
	}

	controller = &restoreController{fail: "dashboard"}
	err = restorePublishedServicesWith(home, map[string]bool{"resident": true, "dashboard": true}, controller)
	if err == nil || !strings.Contains(err.Error(), "injected bootstrap failure of dashboard") {
		t.Fatalf("a dashboard failure = %v", err)
	}
	if strings.Join(controller.bootstraps, ",") != "dashboard,resident" {
		t.Fatalf("a dashboard failure stopped the restore: %v", controller.bootstraps)
	}
}

func publishedDockerUnavailable(context.Context, ...string) ([]byte, error) {
	return []byte("Cannot connect to the Docker daemon\n"), errors.New("exit status 1")
}

func publishedTestProjectImage(c byte) string {
	return "ghcr.io/test/project@sha256:" + strings.Repeat(string(c), 64)
}

func publishedTestImageID(c byte) string { return "sha256:" + strings.Repeat(string(c), 64) }

type publishedImageOperation struct {
	Phase  string `json:"phase"`
	Images struct {
		Removed []string               `json:"removed"`
		Kept    []deployment.KeptImage `json:"kept"`
		Log     []string               `json:"log"`
	} `json:"images"`
}

func readPublishedImageOperation(t *testing.T, home string) publishedImageOperation {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(home, "state", "install-operation.json"))
	if err != nil {
		t.Fatal(err)
	}
	var operation publishedImageOperation
	if err := json.Unmarshal(body, &operation); err != nil {
		t.Fatalf("operation record: %s (%v)", body, err)
	}
	return operation
}

func TestPublishedCompletionRemovesSupersededImages(t *testing.T) {
	home := t.TempDir()
	prior := publishedTestRecord(strings.Repeat("a", 40), 20)
	prior.Image = "ghcr.io/test/culler@sha256:" + strings.Repeat("e", 64)
	prior.ProjectImage = publishedTestProjectImage('c')
	next := publishedTestRecord(strings.Repeat("b", 40), 21)
	next.ProjectImage = publishedTestProjectImage('a')
	for path, record := range map[string]distribution.Record{distribution.InstalledRecord: prior, "state/pending-release.json": next} {
		if err := distribution.Save(filepath.Join(home, path), record); err != nil {
			t.Fatal(err)
		}
	}
	names, err := deployment.RuntimeNames(home)
	if err != nil {
		t.Fatal(err)
	}
	unusedBase := "culler-project-base:" + strings.Repeat("3", 64)
	inUseBase := "culler-project-base:" + strings.Repeat("4", 64)
	list := publishedTestProjectImage('a') + "\t" + publishedTestImageID('1') + "\n" + publishedTestProjectImage('b') + "\t" + publishedTestImageID('2') + "\n" +
		publishedTestProjectImage('c') + "\t" + publishedTestImageID('3') + "\n" + publishedTestProjectImage('d') + "\t" + publishedTestImageID('4') + "\n"
	var calls []string
	docker := func(_ context.Context, args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		switch call {
		case "exec " + names.Helper + " mc __project-image-digests":
			return []byte(`{"digests":["` + publishedTestImageID('2') + `"]}` + "\n"), nil
		case "image ls --digests --no-trunc --format {{.Repository}}@{{.Digest}}\\t{{.ID}} ghcr.io/test/project":
			return []byte(list), nil
		case "image inspect --format {{.Id}} " + unusedBase:
			return []byte(publishedTestImageID('3') + "\n"), nil
		case "image inspect --format {{.Id}} " + inUseBase:
			return []byte("Error response from daemon: No such image: " + inUseBase + "\n"), errors.New("exit status 1")
		case "image rm " + publishedTestProjectImage('d'):
			return []byte(`Error response from daemon: conflict: unable to remove repository reference "` + publishedTestProjectImage('d') + `" (must force) - container ac337021da3a is using its referenced image 56868084a44f` + "\n"), errors.New("exit status 1")
		case "image rm " + prior.Image, "image rm " + unusedBase, "image rm " + publishedTestProjectImage('c'):
			return []byte("Untagged: " + args[2] + "\n"), nil
		}
		t.Fatalf("unexpected Docker command: %s", call)
		return nil, nil
	}
	clock := time.Now()
	p := publishedSetup{home: home, record: next, docker: docker, started: clock.Add(-time.Minute), now: func() time.Time { return clock }}
	if err := p.finishExisting(); err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{
		"exec " + names.Helper + " mc __project-image-digests",
		"image rm " + prior.Image,
		"image ls --digests --no-trunc --format {{.Repository}}@{{.Digest}}\\t{{.ID}} ghcr.io/test/project",
		"image inspect --format {{.Id}} " + unusedBase,
		"image rm " + unusedBase,
		"image rm " + publishedTestProjectImage('c'),
		"image inspect --format {{.Id}} " + inUseBase,
		"image rm " + publishedTestProjectImage('d'),
	}
	if strings.Join(calls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("Docker commands:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(wantCalls, "\n"))
	}
	operation := readPublishedImageOperation(t, home)
	wantRemoved := []string{prior.Image, unusedBase, publishedTestProjectImage('c')}
	wantKept := []deployment.KeptImage{
		{Reference: publishedTestProjectImage('a'), Reason: "a Project or the installed release needs the image"},
		{Reference: publishedTestProjectImage('b'), Reason: "a Project or the installed release needs the image"},
		{Reference: publishedTestProjectImage('d'), Reason: "a container uses the image"},
	}
	if operation.Phase != "complete" || strings.Join(operation.Images.Removed, "\n") != strings.Join(wantRemoved, "\n") || fmt.Sprint(operation.Images.Kept) != fmt.Sprint(wantKept) {
		t.Fatalf("operation record: %#v", operation)
	}
	if len(operation.Images.Log) != 6 || operation.Images.Log[0] != "Removed image "+prior.Image+"." || operation.Images.Log[5] != "Kept image "+publishedTestProjectImage('d')+": a container uses the image." {
		t.Fatalf("operation log: %q", operation.Images.Log)
	}
	installed, err := distribution.Load(filepath.Join(home, distribution.InstalledRecord))
	if err != nil || installed.Commit != next.Commit {
		t.Fatalf("installed record: %#v %v", installed, err)
	}
	if _, err := os.Stat(filepath.Join(home, "state", "pending-release.json")); !os.IsNotExist(err) {
		t.Fatalf("pending record: %v", err)
	}
}

func TestPublishedCompletionKeepsProjectImagesWithoutTheHelper(t *testing.T) {
	home := t.TempDir()
	prior := publishedTestRecord(strings.Repeat("a", 40), 20)
	prior.Image = "ghcr.io/test/culler@sha256:" + strings.Repeat("e", 64)
	next := publishedTestRecord(strings.Repeat("b", 40), 21)
	next.ProjectImage = publishedTestProjectImage('a')
	names, err := deployment.RuntimeNames(home)
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	docker := func(_ context.Context, args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		switch call {
		case "exec " + names.Helper + " mc __project-image-digests":
			return []byte("Error response from daemon: No such container: " + names.Helper + "\n"), errors.New("exit status 1")
		case "image rm " + prior.Image:
			return []byte("Untagged: " + prior.Image + "\n"), nil
		}
		t.Fatalf("unexpected Docker command: %s", call)
		return nil, nil
	}
	clock := time.Now()
	p := publishedSetup{home: home, record: next, installed: prior, docker: docker, started: clock, now: func() time.Time { return clock }}
	if err := p.complete(); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("Docker commands: %q", calls)
	}
	operation := readPublishedImageOperation(t, home)
	if operation.Phase != "complete" || strings.Join(operation.Images.Removed, "\n") != prior.Image || len(operation.Images.Kept) != 0 {
		t.Fatalf("operation record: %#v", operation)
	}
	if len(operation.Images.Log) != 2 || !strings.HasPrefix(operation.Images.Log[0], "The default Project images stay: the selected Project images are unknown: ") || !strings.Contains(operation.Images.Log[0], "No such container") {
		t.Fatalf("operation log: %q", operation.Images.Log)
	}
}

func TestPublishedCompletionLeavesAnUnchangedProductImage(t *testing.T) {
	home := t.TempDir()
	record := publishedTestRecord(strings.Repeat("a", 40), 20)
	record.ProjectImage = publishedTestProjectImage('a')
	names, err := deployment.RuntimeNames(home)
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	docker := func(_ context.Context, args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		switch call {
		case "exec " + names.Helper + " mc __project-image-digests":
			return []byte(`{"digests":[]}` + "\n"), nil
		case "image ls --digests --no-trunc --format {{.Repository}}@{{.Digest}}\\t{{.ID}} ghcr.io/test/project":
			return []byte(publishedTestProjectImage('a') + "\t" + publishedTestImageID('1') + "\n"), nil
		}
		t.Fatalf("unexpected Docker command: %s", call)
		return nil, nil
	}
	clock := time.Now()
	p := publishedSetup{home: home, record: record, installed: record, docker: docker, started: clock, now: func() time.Time { return clock }}
	if err := p.complete(); err != nil {
		t.Fatal(err)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "image rm") {
			t.Fatalf("a repair removed an image: %s", call)
		}
	}
	operation := readPublishedImageOperation(t, home)
	if len(operation.Images.Removed) != 0 || len(operation.Images.Kept) != 1 || operation.Images.Kept[0].Reference != publishedTestProjectImage('a') {
		t.Fatalf("operation record: %#v", operation)
	}
}

func TestPublishedPrepareRetainsTheInstalledRecordWithoutRemovals(t *testing.T) {
	home := t.TempDir()
	prior := publishedTestRecord(strings.Repeat("a", 40), 20)
	prior.Image = "ghcr.io/test/culler@sha256:" + strings.Repeat("e", 64)
	next := publishedTestRecord(strings.Repeat("b", 40), 21)
	next.ProjectImage = publishedTestProjectImage('a')
	if err := distribution.Save(filepath.Join(home, distribution.InstalledRecord), prior); err != nil {
		t.Fatal(err)
	}
	var calls []string
	p := publishedSetup{home: home, record: next, pull: func(context.Context, distribution.Record, func(string)) error { return nil },
		docker: func(_ context.Context, args ...string) ([]byte, error) {
			calls = append(calls, strings.Join(args, " "))
			if args[0] == "volume" {
				return []byte("No such volume"), errors.New("absent")
			}
			t.Fatalf("prepare ran docker %v", args)
			return nil, nil
		}}
	if err := p.prepare(context.Background(), func(string) {}); err != nil {
		t.Fatal(err)
	}
	if p.installed.Commit != prior.Commit || p.installed.Image != prior.Image {
		t.Fatalf("installed record: %#v", p.installed)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "image") || strings.HasPrefix(call, "exec") {
			t.Fatalf("an incomplete update touched an image: %s", call)
		}
	}
	installed, err := distribution.Load(filepath.Join(home, distribution.InstalledRecord))
	if err != nil || installed.Commit != prior.Commit {
		t.Fatalf("an incomplete update changed the installed record: %#v %v", installed, err)
	}
}

func TestPublishedRecoveryLinesShowTheVersions(t *testing.T) {
	home := t.TempDir()
	installed := publishedTestRecord(strings.Repeat("a", 40), 20)
	pending := publishedTestRecord(strings.Repeat("b", 40), 21)
	installed.Version, installed.Tag, pending.Version, pending.Tag = "0.1.0", "v0.1.0", "0.2.0", "v0.2.0"
	for path, record := range map[string]distribution.Record{distribution.InstalledRecord: installed, filepath.Join("state", "pending-release.json"): pending} {
		if err := distribution.Save(filepath.Join(home, path), record); err != nil {
			t.Fatal(err)
		}
	}
	if err := writePublishedState(filepath.Join(home, "supervision", "resident.json"), map[string]string{"releaseBuildId": pending.Commit}); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(publishedRecoveryLines(home), "\n")
	want := "Service release: 0.2.0\u00a0(bbbbbbbbbbbb).\nLast verified release: 0.1.0\u00a0(aaaaaaaaaaaa).\nRelease awaiting verification: 0.2.0\u00a0(bbbbbbbbbbbb)."
	if got != want {
		t.Fatalf("recovery lines:\n%s", got)
	}
}

func TestSetupReleaseLinesLabelTheAgentImageRelease(t *testing.T) {
	r := setupRelease{Installed: strings.Repeat("a", 40), Proposed: strings.Repeat("b", 40), InstalledDisplay: "0.1.0 (aaaaaaaaaaaa)", ProposedDisplay: "0.2.0 (bbbbbbbbbbbb)", SourceState: "published",
		Components: []setupReleaseComponent{{Name: "Agent image", Installed: strings.Repeat("a", 40), Status: "different"}}}
	if lines := strings.Join(r.lines(), "\n"); !strings.Contains(lines, "Agent image: different. Release: 0.1.0\u00a0(aaaaaaaaaaaa).") ||
		!strings.Contains(lines, "Proposed release: 0.2.0\u00a0(bbbbbbbbbbbb)") {
		t.Fatalf("release lines:\n%s", lines)
	}
	r.Components[0].Installed = strings.Repeat("c", 40)
	if lines := strings.Join(r.lines(), "\n"); !strings.Contains(lines, "Agent image: different. Release: cccccccccccc.") {
		t.Fatalf("release lines:\n%s", lines)
	}
}
