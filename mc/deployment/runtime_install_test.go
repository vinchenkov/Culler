package deployment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// runtimeInstallRunner starts the install program the way one JavaScript
// runtime does. The product image runs the program with its Node.js and
// standard input; the Mac test runs it with the pinned Bun from a file.
type runtimeInstallRunner struct {
	name string
	// setuid states whether the runtime applies the setuid and setgid bits.
	// Bun 1.3.12 masks them in chmodSync; the product image runs Node.js.
	setuid bool
	run    func(t *testing.T, image, volume, release string) ([]byte, error)
}

func runtimeInstallRunners(t *testing.T) []runtimeInstallRunner {
	t.Helper()
	script := filepath.Join(t.TempDir(), "runtime_install.js")
	if err := os.WriteFile(script, []byte(runtimeInstallScript), 0o644); err != nil {
		t.Fatal(err)
	}
	bun := []string{"mise", "exec", "--", "bun"}
	if path, err := exec.LookPath("bun"); err == nil {
		bun = []string{path}
	}
	node, nodeErr := exec.LookPath("node")
	return []runtimeInstallRunner{
		{
			name: "bun",
			run: func(_ *testing.T, image, volume, release string) ([]byte, error) {
				args := append(append([]string{}, bun[1:]...), script, image, volume, release)
				return exec.Command(bun[0], args...).CombinedOutput()
			},
		},
		{
			name:   "node",
			setuid: true,
			run: func(t *testing.T, image, volume, release string) ([]byte, error) {
				if nodeErr != nil {
					t.Skip("node is not on PATH; the Node.js run of the runtime install program is unverified")
				}
				command := exec.Command(node, "-", image, volume, release)
				command.Stdin = strings.NewReader(runtimeInstallScript)
				return command.CombinedOutput()
			},
		},
	}
}

type runtimeInstallSummary struct {
	Release   string `json:"release"`
	Written   int    `json:"written"`
	Unchanged int    `json:"unchanged"`
	Hashed    int    `json:"hashed"`
	Removed   int    `json:"removed"`
}

func parseRuntimeInstallSummary(t *testing.T, output []byte) runtimeInstallSummary {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var summary runtimeInstallSummary
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &summary); err != nil {
		t.Fatalf("the install program printed no summary: %v\n%s", err, output)
	}
	return summary
}

func mustWriteFile(t *testing.T, path, content string, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func mustLink(t *testing.T, existing, path string) {
	t.Helper()
	if err := os.Link(existing, path); err != nil {
		t.Fatal(err)
	}
}

// writeRuntimeManifest writes the manifest of one runtime tree. The files
// map holds each regular file except the manifest itself. The skipped paths
// stay out of the map, like a file that a later build added.
func writeRuntimeManifest(t *testing.T, root, release string, skipped ...string) {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		if !entry.Type().IsRegular() || relative == "manifest.json" {
			return nil
		}
		for _, skip := range skipped {
			if relative == skip {
				return nil
			}
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		files[relative] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.MarshalIndent(map[string]any{"format": 1, "release": release, "tools": map[string]string{"node": "v1"}, "files": files}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(root, "manifest.json"), string(body)+"\n", 0o644)
}

// buildRuntimeFixture builds an image runtime and an old volume. The volume
// differs from the image in each way that an install must handle: changed,
// new, removed, and hard-linked files, a changed mode, a changed symlink
// target, an extra directory, a stale temporary file, a directory in the place
// of a file, and a file in the place of a directory. With setuid, mc-real has
// the mode of the product image.
func buildRuntimeFixture(t *testing.T, setuid bool) (image, volume string) {
	t.Helper()
	mcRealMode := fs.FileMode(0o755)
	if setuid {
		mcRealMode |= fs.ModeSetuid | fs.ModeSetgid
	}
	image = filepath.Join(t.TempDir(), "runtime")
	volume = filepath.Join(t.TempDir(), "volume")
	mustMkdir(t, image, 0o755)
	mustMkdir(t, volume, 0o755)

	mustWriteFile(t, filepath.Join(image, "release-id"), "new-release\n", 0o644)
	mustWriteFile(t, filepath.Join(image, "bin/check"), "check\n", 0o755)
	mustSymlink(t, "../node/bin/node", filepath.Join(image, "bin/node"))
	mustSymlink(t, "../bun/bin/bun", filepath.Join(image, "bin/bun"))
	mustWriteFile(t, filepath.Join(image, "app/same.txt"), "same\n", 0o644)
	mustWriteFile(t, filepath.Join(image, "app/changed.txt"), "changed new\n", 0o644)
	mustWriteFile(t, filepath.Join(image, "app/new.txt"), "new\n", 0o644)
	mustWriteFile(t, filepath.Join(image, "app/unlisted.txt"), "unlisted\n", 0o644)
	mustWriteFile(t, filepath.Join(image, "shared/x"), "shared old\n", 0o644)
	mustWriteFile(t, filepath.Join(image, "shared/y"), "shared new\n", 0o644)
	mustWriteFile(t, filepath.Join(image, "libexec/mc-real"), "mc\n", mcRealMode)
	mustWriteFile(t, filepath.Join(image, "mode.txt"), "mode\n", 0o644)
	mustWriteFile(t, filepath.Join(image, "new-dir/sub/file.txt"), "deep\n", 0o640)
	mustMkdir(t, filepath.Join(image, "new-dir"), 0o750)
	mustWriteFile(t, filepath.Join(image, "swap"), "swap file\n", 0o644)
	mustWriteFile(t, filepath.Join(image, "dirswap/inner.txt"), "inner\n", 0o644)
	writeRuntimeManifest(t, image, "new-release")

	mustWriteFile(t, filepath.Join(volume, "release-id"), "old-release\n", 0o644)
	mustWriteFile(t, filepath.Join(volume, "bin/check"), "check\n", 0o755)
	mustSymlink(t, "../old/node", filepath.Join(volume, "bin/node"))
	mustSymlink(t, "../bun/bin/bun", filepath.Join(volume, "bin/bun"))
	mustWriteFile(t, filepath.Join(volume, "app/same.txt"), "same\n", 0o644)
	mustWriteFile(t, filepath.Join(volume, "app/changed.txt"), "changed old\n", 0o644)
	mustWriteFile(t, filepath.Join(volume, "app/unlisted.txt"), "unlisted\n", 0o644)
	mustWriteFile(t, filepath.Join(volume, "app/removed.txt"), "removed\n", 0o644)
	mustWriteFile(t, filepath.Join(volume, "app/.culler-install.changed.txt"), "stale\n", 0o644)
	mustMkdir(t, filepath.Join(volume, "app"), 0o700)
	mustWriteFile(t, filepath.Join(volume, "shared/x"), "shared old\n", 0o644)
	mustLink(t, filepath.Join(volume, "shared/x"), filepath.Join(volume, "shared/y"))
	mustWriteFile(t, filepath.Join(volume, "libexec/mc-real"), "mc\n", 0o700)
	mustWriteFile(t, filepath.Join(volume, "mode.txt"), "mode\n", 0o600)
	mustWriteFile(t, filepath.Join(volume, "swap/inner"), "x\n", 0o644)
	mustWriteFile(t, filepath.Join(volume, "dirswap"), "flat\n", 0o644)
	mustWriteFile(t, filepath.Join(volume, "old-dir/with/file"), "old\n", 0o644)
	writeRuntimeManifest(t, volume, "old-release", "app/unlisted.txt")
	return image, volume
}

type treeEntry struct {
	Kind    string
	Mode    fs.FileMode
	UID     uint32
	GID     uint32
	Content string
	Target  string
}

// describeTree lists each entry below the root with the attributes that an
// install must reproduce.
func describeTree(t *testing.T, root string) map[string]treeEntry {
	t.Helper()
	tree := map[string]treeEntry{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		stat := info.Sys().(*syscall.Stat_t)
		described := treeEntry{UID: stat.Uid, GID: stat.Gid}
		relative := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			described.Kind = "symlink"
			described.Target, err = os.Readlink(path)
		case info.IsDir():
			described.Kind = "directory"
			described.Mode = info.Mode() & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)
		case info.Mode().IsRegular():
			described.Kind = "file"
			described.Mode = info.Mode() & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)
			var body []byte
			body, err = os.ReadFile(path)
			described.Content = string(body)
		default:
			described.Kind = "other"
		}
		if err != nil {
			return err
		}
		tree[relative] = described
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func requireSameTree(t *testing.T, image, volume string) {
	t.Helper()
	want := describeTree(t, image)
	got := describeTree(t, volume)
	for path, entry := range want {
		if got[path] != entry {
			t.Errorf("%s: image %+v, volume %+v", path, entry, got[path])
		}
	}
	for path := range got {
		if _, ok := want[path]; !ok {
			t.Errorf("%s: only the volume holds this entry", path)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
}

func inodeOf(t *testing.T, path string) (uint64, uint64) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	return uint64(stat.Ino), uint64(stat.Nlink)
}

func TestRuntimeInstallWritesOnlyTheChangedEntries(t *testing.T) {
	for _, runner := range runtimeInstallRunners(t) {
		t.Run(runner.name, func(t *testing.T) {
			image, volume := buildRuntimeFixture(t, runner.setuid)
			kept := []string{"bin/check", "app/same.txt", "app/unlisted.txt", "shared/x"}
			before := map[string]uint64{}
			for _, path := range kept {
				before[path], _ = inodeOf(t, filepath.Join(volume, path))
			}
			output, err := runner.run(t, image, volume, "new-release")
			if err != nil {
				t.Fatalf("%v\n%s", err, output)
			}
			requireSameTree(t, image, volume)
			summary := parseRuntimeInstallSummary(t, output)
			want := runtimeInstallSummary{Release: "new-release", Written: 8, Unchanged: 4, Hashed: 1, Removed: 2}
			if summary != want {
				t.Fatalf("summary %+v, want %+v", summary, want)
			}
			for _, path := range kept {
				if after, _ := inodeOf(t, filepath.Join(volume, path)); after != before[path] {
					t.Errorf("%s: the install replaced an unchanged file", path)
				}
			}
			xInode, xLinks := inodeOf(t, filepath.Join(volume, "shared/x"))
			yInode, _ := inodeOf(t, filepath.Join(volume, "shared/y"))
			if xLinks != 1 || xInode == yInode {
				t.Fatalf("the changed hard link still shares its inode: links %d", xLinks)
			}
			if body, _ := os.ReadFile(filepath.Join(volume, "release-id")); string(body) != "new-release\n" {
				t.Fatalf("release marker %q", body)
			}
		})
	}
}

func TestRuntimeInstallLeavesNoReleaseMarkerWhenItStops(t *testing.T) {
	for _, runner := range runtimeInstallRunners(t) {
		t.Run(runner.name, func(t *testing.T) {
			image, volume := buildRuntimeFixture(t, runner.setuid)
			// A locked directory in the place of the manifest stops the install
			// after the file synchronization and before the release marker.
			obstacle := filepath.Join(volume, "manifest.json")
			if err := os.Remove(obstacle); err != nil {
				t.Fatal(err)
			}
			mustWriteFile(t, filepath.Join(obstacle, "inner"), "x\n", 0o644)
			if err := os.Chmod(obstacle, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(obstacle, 0o755) })
			output, err := runner.run(t, image, volume, "new-release")
			if err == nil {
				t.Fatalf("the install completed over the obstacle:\n%s", output)
			}
			if _, err := os.Lstat(filepath.Join(volume, "release-id")); !os.IsNotExist(err) {
				t.Fatalf("the stopped install left a release marker: %v", err)
			}
			if body, _ := os.ReadFile(filepath.Join(volume, "app/changed.txt")); string(body) != "changed new\n" {
				t.Fatalf("the files were not synchronized before the stop: %q", body)
			}
			if err := os.Chmod(obstacle, 0o755); err != nil {
				t.Fatal(err)
			}
			output, err = runner.run(t, image, volume, "new-release")
			if err != nil {
				t.Fatalf("%v\n%s", err, output)
			}
			requireSameTree(t, image, volume)
			summary := parseRuntimeInstallSummary(t, output)
			want := runtimeInstallSummary{Release: "new-release", Written: 0, Unchanged: 12, Hashed: 12, Removed: 0}
			if summary != want {
				t.Fatalf("summary %+v, want %+v", summary, want)
			}
		})
	}
}

// A volume without a release marker holds the files of an interrupted
// install. Its manifest can name hashes that the files no longer have, so
// the install hashes each file instead.
func TestRuntimeInstallHashesWhenTheReleaseMarkerIsAbsent(t *testing.T) {
	for _, runner := range runtimeInstallRunners(t) {
		t.Run(runner.name, func(t *testing.T) {
			image, volume := buildRuntimeFixture(t, runner.setuid)
			manifest, err := os.ReadFile(filepath.Join(image, "manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			mustWriteFile(t, filepath.Join(volume, "manifest.json"), string(manifest), 0o644)
			if err := os.Remove(filepath.Join(volume, "release-id")); err != nil {
				t.Fatal(err)
			}
			output, err := runner.run(t, image, volume, "new-release")
			if err != nil {
				t.Fatalf("%v\n%s", err, output)
			}
			requireSameTree(t, image, volume)
			summary := parseRuntimeInstallSummary(t, output)
			want := runtimeInstallSummary{Release: "new-release", Written: 8, Unchanged: 4, Hashed: 8, Removed: 2}
			if summary != want {
				t.Fatalf("summary %+v, want %+v", summary, want)
			}
		})
	}
}

func TestRuntimeInstallRefusesAnotherRelease(t *testing.T) {
	for _, runner := range runtimeInstallRunners(t) {
		t.Run(runner.name, func(t *testing.T) {
			image, volume := buildRuntimeFixture(t, runner.setuid)
			before := describeTree(t, volume)
			output, err := runner.run(t, image, volume, "other-release")
			if err == nil || !strings.Contains(string(output), "is not release other-release") {
				t.Fatalf("the install accepted another release: %v\n%s", err, output)
			}
			after := describeTree(t, volume)
			if len(before) != len(after) {
				t.Fatal("the refused install changed the volume")
			}
			for path, entry := range before {
				if after[path] != entry {
					t.Fatalf("%s: the refused install changed the volume", path)
				}
			}
		})
	}
}

func TestRuntimeInstallHashesARepairOfTheSameRelease(t *testing.T) {
	for _, runner := range runtimeInstallRunners(t) {
		t.Run(runner.name, func(t *testing.T) {
			image, volume := buildRuntimeFixture(t, runner.setuid)
			output, err := runner.run(t, image, volume, "new-release")
			if err != nil {
				t.Fatalf("%v\n%s", err, output)
			}
			requireSameTree(t, image, volume)
			mustWriteFile(t, filepath.Join(volume, "app/same.txt"), "BAD!\n", 0o644)
			output, err = runner.run(t, image, volume, "new-release")
			if err != nil {
				t.Fatalf("%v\n%s", err, output)
			}
			requireSameTree(t, image, volume)
			summary := parseRuntimeInstallSummary(t, output)
			if summary.Written != 1 || summary.Removed != 0 || summary.Hashed == 0 {
				t.Fatalf("the repair of the same release did not hash the volume: %+v", summary)
			}
			body, err := os.ReadFile(filepath.Join(volume, "app/same.txt"))
			if err != nil || string(body) != "same\n" {
				t.Fatalf("the repair kept the changed content %q, %v", body, err)
			}
		})
	}
}
