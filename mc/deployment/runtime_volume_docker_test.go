//go:build docker_boundary

package deployment

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestRuntimeUpdatePreservesProjectImageDocker(t *testing.T) {
	project := os.Getenv("CULLER_PROJECT_TEST_IMAGE")
	first := os.Getenv("CULLER_RUNTIME_TEST_IMAGE")
	second := os.Getenv("CULLER_RUNTIME_NEXT_TEST_IMAGE")
	if project == "" || first == "" || second == "" {
		t.Fatal("Set the three CULLER image variables for the isolated runtime update check.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		body, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("Docker %v: %v\n%s", args, err, body)
		}
		return strings.TrimSpace(string(body))
	}
	selected := docker("image", "inspect", "--format", "{{.Id}}", project)
	home := t.TempDir()
	names, err := RuntimeNames(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", names.RuntimeVolume).Run() })
	prior := ""
	for _, donor := range []string{first, second} {
		release := docker("run", "--rm", "--network", "none", donor, "/bin/cat", RuntimeRoot+"/release-id")
		if release == "" || release == prior {
			t.Fatal("The fixture requires two different runtime releases.")
		}
		if err := InstallRuntimeVolume(ctx, home, donor, release, nil); err != nil {
			t.Fatal(err)
		}
		if got := docker("image", "inspect", "--format", "{{.Id}}", project); got != selected {
			t.Fatal("The runtime update changed the Project image.")
		}
		output := docker("run", "--rm", "--network", "none", "--user", "10002:10002", "--cap-drop", "ALL",
			"--mount", "type=volume,src="+names.RuntimeVolume+",dst="+RuntimeRoot+",readonly,volume-nocopy", selected,
			"/bin/sh", "-ec", "test -s /usr/local/share/image-dependency-proof; /opt/culler-runtime/bin/node --version; /opt/culler-runtime/bin/bun --version; cat /opt/culler-runtime/release-id")
		if !strings.HasSuffix(output, release) {
			t.Fatalf("The selected Project received another runtime: %s", output)
		}
		if err := InstallRuntimeVolume(ctx, home, donor, release, nil); err != nil {
			t.Fatal(err)
		}
		prior = release
	}
	t.Logf("The same Project image %s executed both Culler runtime releases.", selected)
}

func TestRuntimeVolumeRepairsUnreadableSourceDocker(t *testing.T) {
	image := os.Getenv("CULLER_RUNTIME_TEST_IMAGE")
	if image == "" {
		t.Fatal("Set CULLER_RUNTIME_TEST_IMAGE for the runtime repair check.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		body, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("Docker %v: %v\n%s", args, err, body)
		}
		return strings.TrimSpace(string(body))
	}
	home := t.TempDir()
	names, err := RuntimeNames(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", names.RuntimeVolume).Run() })
	release := docker("run", "--rm", "--network", "none", image, "/bin/cat", RuntimeRoot+"/release-id")
	if err := InstallRuntimeVolume(ctx, home, image, release, nil); err != nil {
		t.Fatal(err)
	}
	docker("run", "--rm", "--network", "none", "--user", "0:0", "--mount",
		"type=volume,src="+names.RuntimeVolume+",dst="+RuntimeRoot+",volume-nocopy", image,
		"/bin/chmod", "0700", RuntimeRoot+"/app/src/agent-runner")
	probe := []string{"run", "--rm", "--network", "none", "--user", "10002:10002", "--cap-drop", "ALL", "--mount",
		"type=volume,src=" + names.RuntimeVolume + ",dst=" + RuntimeRoot + ",readonly,volume-nocopy", image, RuntimeRoot + "/bin/check"}
	if body, err := exec.CommandContext(ctx, "docker", probe...).CombinedOutput(); err == nil {
		t.Fatalf("The agent read the root-only source: %s", body)
	}
	if err := InstallRuntimeVolume(ctx, home, image, release, nil); err != nil {
		t.Fatal(err)
	}
	docker(probe...)
	if got := docker("run", "--rm", "--network", "none", "--mount",
		"type=volume,src="+names.RuntimeVolume+",dst="+RuntimeRoot+",readonly,volume-nocopy", image,
		"/bin/cat", RuntimeRoot+"/release-id"); got != release {
		t.Fatalf("The repair changed the release: %q", got)
	}
}

// changedPaths names each path whose listing line differs between two
// listings, except the ignored paths. The path is the sixth field of each
// line. The install rewrites the release marker on each run, and the
// filesystem can give the new marker the inode number of the old one, so
// each caller ignores the marker.
func changedPaths(before, after string, ignored ...string) []string {
	lines := func(listing string) map[string]bool {
		set := map[string]bool{}
		for _, line := range strings.Split(strings.TrimSpace(listing), "\n") {
			set[line] = true
		}
		return set
	}
	first, second := lines(before), lines(after)
	changed := map[string]bool{}
	for line := range first {
		if !second[line] {
			changed[strings.Fields(line)[5]] = true
		}
	}
	for line := range second {
		if !first[line] {
			changed[strings.Fields(line)[5]] = true
		}
	}
	for _, path := range ignored {
		delete(changed, path)
	}
	paths := make([]string, 0, len(changed))
	for path := range changed {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// The install writes only the entries that differ from the image, and the
// volume equals the image runtime file for file after each install.
func TestRuntimeVolumeInstallWritesOnlyChangesDocker(t *testing.T) {
	image := os.Getenv("CULLER_RUNTIME_TEST_IMAGE")
	if image == "" {
		t.Fatal("Set CULLER_RUNTIME_TEST_IMAGE for the runtime install check.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		body, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("Docker %v: %v\n%s", args, err, body)
		}
		return strings.TrimSpace(string(body))
	}
	home := t.TempDir()
	names, err := RuntimeNames(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", names.RuntimeVolume).Run() })
	release := docker("run", "--rm", "--network", "none", image, "/bin/cat", RuntimeRoot+"/release-id")

	mount := "type=volume,src=" + names.RuntimeVolume + ",dst=/runtime-output,volume-nocopy"
	inVolume := func(script string) string {
		t.Helper()
		return docker("run", "--rm", "--network", "none", "--user", "0:0", "--mount", mount, image, "/bin/sh", "-ec", script)
	}
	listing := func() string {
		return inVolume(`cd /runtime-output && find . -mindepth 1 -printf '%i %m %U %G %y %p %l\n' | sort -k6`)
	}
	install := func() runtimeInstallSummary {
		t.Helper()
		started := time.Now()
		output, err := runRuntimeDocker(ctx, runtimeInstallScript, runtimeInstallArgs(names.RuntimeVolume, image, release)...)
		if err != nil {
			t.Fatalf("install: %v\n%s", err, output)
		}
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		var summary runtimeInstallSummary
		if err := json.Unmarshal([]byte(lines[len(lines)-1]), &summary); err != nil {
			t.Fatalf("summary: %v\n%s", err, output)
		}
		t.Logf("install in %s: %+v", time.Since(started).Round(time.Millisecond), summary)
		return summary
	}

	started := time.Now()
	if err := InstallRuntimeVolume(ctx, home, image, release, nil); err != nil {
		t.Fatal(err)
	}
	t.Logf("first install through InstallRuntimeVolume in %s", time.Since(started).Round(time.Millisecond))
	first := listing()

	// A second install of the same release writes only the release marker.
	summary := install()
	second := listing()
	if changed := changedPaths(first, second, "./release-id"); summary.Written != 0 || summary.Removed != 0 || len(changed) != 0 {
		t.Fatalf("the repeated install changed %v: %+v", changed, summary)
	}
	if marker := inVolume("cat /runtime-output/release-id"); marker != release {
		t.Fatalf("release marker %q", marker)
	}

	// The install repairs a changed mode through a new file. Every other entry keeps its inode.
	inVolume("chmod 0600 /runtime-output/bin/check")
	summary = install()
	third := listing()
	if changed := changedPaths(second, third, "./release-id"); summary.Written != 1 || strings.Join(changed, " ") != "./bin/check" {
		t.Fatalf("the mode repair changed %v: %+v", changed, summary)
	}
	if mode := inVolume("stat -c %a /runtime-output/bin/check"); mode != "755" {
		t.Fatalf("bin/check mode %s", mode)
	}

	// Without the volume manifest, the install hashes each file and repairs a changed content.
	// The install writes the manifest again, and that new file can reuse the inode number too.
	inVolume("rm /runtime-output/manifest.json; printf x >>/runtime-output/artifacts.txt")
	summary = install()
	fourth := listing()
	if changed := changedPaths(third, fourth, "./release-id", "./manifest.json"); summary.Written != 1 || summary.Hashed < 1000 || strings.Join(changed, " ") != "./artifacts.txt" {
		t.Fatalf("the content repair changed %v: %+v", changed, summary)
	}
	inVolume("cmp /opt/culler-runtime/manifest.json /runtime-output/manifest.json")

	// The volume equals the image runtime file for file.
	compare := `cd /opt/culler-runtime && find . -mindepth 1 -printf '%m %U %G %y %p %l\n' | sort >/tmp/image-entries
cd /runtime-output && find . -mindepth 1 -printf '%m %U %G %y %p %l\n' | sort >/tmp/volume-entries
diff /tmp/image-entries /tmp/volume-entries
cd /opt/culler-runtime && find . -type f -exec sha256sum {} + | sort -k2 >/tmp/image-hashes
cd /runtime-output && find . -type f -exec sha256sum {} + | sort -k2 >/tmp/volume-hashes
diff /tmp/image-hashes /tmp/volume-hashes
wc -l </tmp/volume-hashes`
	if count := inVolume(compare); count == "" || count == "0" {
		t.Fatalf("the comparison listed no file: %q", count)
	}
	if err := InstallRuntimeVolume(ctx, home, image, release, nil); err != nil {
		t.Fatal(err)
	}
}
