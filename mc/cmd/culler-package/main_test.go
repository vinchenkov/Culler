package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"mc/deployment"
)

func TestRuntimePinsRequireThePackageToolchain(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".mise.toml")
	config := "[tools]\ngo = '" + strings.TrimPrefix(runtime.Version(), "go") + "'\nbun = '1.3.9'\n"
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	pins, err := runtimePins(root)
	if err != nil || pins["bun"] != "1.3.9" || "go"+pins["go"] != runtime.Version() {
		t.Fatalf("runtime pins: %v, %v", pins, err)
	}
	if err := os.WriteFile(path, []byte("[tools]\ngo = '1.0.0'\nbun = '1.3.9'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runtimePins(root); err == nil {
		t.Fatal("the package accepted a different Go toolchain")
	}
}

func TestPackageContainsOnlyDeclaredRuntimeFiles(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "application")
	agentsView := filepath.Join(root, "viewer", "agentsview")
	files := packageFiles(root, binary, agentsView)
	if files["bin/agentsview"] != agentsView {
		t.Fatalf("the package omits the AgentsView executable: %v", files)
	}
	for _, path := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("runtime\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "private"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "private", "state"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "package.tar.gz")
	if err := writePackage(root, binary, agentsView, output); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	r := tar.NewReader(z)
	for {
		header, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := files[header.Name]; !ok {
			t.Fatalf("unexpected package file %s", header.Name)
		}
		if header.Typeflag != tar.TypeReg || header.Uid != 0 || header.Gid != 0 {
			t.Fatalf("unsafe archive header: %+v", header)
		}
		wantMode := int64(0o600)
		if strings.HasPrefix(header.Name, "bin/") {
			wantMode = 0o700
		}
		if header.Mode != wantMode {
			t.Fatalf("%s has mode %o, want %o", header.Name, header.Mode, wantMode)
		}
		delete(files, header.Name)
	}
	if len(files) != 0 {
		t.Fatalf("missing package files: %v", files)
	}
	if err := os.Remove(binary); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "private", "state"), binary); err != nil {
		t.Fatal(err)
	}
	if err := writePackage(root, binary, agentsView, filepath.Join(root, "linked.tar.gz")); err == nil {
		t.Fatal("the package accepted a symbolic link")
	}
}

func TestPackageMetadataRecordsTheAgentsViewPin(t *testing.T) {
	output := filepath.Join(t.TempDir(), "culler-darwin-arm64.tar.gz")
	if err := os.WriteFile(output, []byte("package\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pin, err := deployment.LoadAgentsViewPin()
	if err != nil {
		t.Fatal(err)
	}
	hostBun, err := deployment.LoadHostBunPin()
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := packageMetadata(output, strings.Repeat("a", 40), map[string]string{"go": "1.25.12", "bun": "1.3.9"}, pin, hostBun)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"version": pin.Version, "archive_sha256": pin.Archive.SHA256, "executable_sha256": pin.Executable.SHA256}
	if got, ok := metadata["agentsview"].(map[string]string); !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("agentsview = %#v, want %#v", metadata["agentsview"], want)
	}
	if metadata["size"] != int64(len("package\n")) || metadata["name"] != "culler-darwin-arm64.tar.gz" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

// The release record carries the host Bun pin with the shape of hostbun.json.
func TestPackageMetadataRecordsTheHostBunPin(t *testing.T) {
	output := filepath.Join(t.TempDir(), "culler-darwin-arm64.tar.gz")
	if err := os.WriteFile(output, []byte("package\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pin, err := deployment.LoadAgentsViewPin()
	if err != nil {
		t.Fatal(err)
	}
	hostBun, err := deployment.LoadHostBunPin()
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := packageMetadata(output, strings.Repeat("a", 40), map[string]string{"go": "1.25.12", "bun": "1.3.9"}, pin, hostBun)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(metadata["host_bun"])
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatal(err)
	}
	archive := record["archives"].(map[string]any)["darwin-arm64"].(map[string]any)
	want := hostBun.Archives["darwin-arm64"]
	if record["version"] != hostBun.Version || archive["name"] != want.Name || archive["url"] != want.URL ||
		archive["sha256"] != want.SHA256 || archive["size"] != float64(want.Size) || len(record) != 2 || len(archive) != 4 {
		t.Fatalf("host_bun = %s, want the pin %+v", body, hostBun)
	}
}

func TestProductionBuildRecordRejectsTagsAndWrongIdentity(t *testing.T) {
	commit := strings.Repeat("a", 40)
	info := &debug.BuildInfo{GoVersion: runtime.Version(), Path: "mc/cmd/mc", Settings: []debug.BuildSetting{
		{Key: "GOOS", Value: "darwin"}, {Key: "GOARCH", Value: "arm64"},
		{Key: "CGO_ENABLED", Value: "0"}, {Key: "-ldflags", Value: "-w -X main.releaseBuildID=" + commit},
	}}
	if err := validateBuildInfo(info, commit, "darwin-arm64"); err != nil {
		t.Fatal(err)
	}
	kept := *info
	kept.Settings = append(append([]debug.BuildSetting{}, info.Settings[:3]...), debug.BuildSetting{Key: "-ldflags", Value: "-X main.releaseBuildID=" + commit})
	if err := validateBuildInfo(&kept, commit, "darwin-arm64"); err == nil {
		t.Fatal("the production record accepted a link without -w")
	}
	// A -trimpath build records no -ldflags setting.
	trimmed := *info
	trimmed.Settings = append(append([]debug.BuildSetting{}, info.Settings[:3]...), debug.BuildSetting{Key: "-trimpath", Value: "true"})
	if err := validateBuildInfo(&trimmed, commit, "darwin-arm64"); err != nil {
		t.Fatal(err)
	}
	for _, tags := range []string{"test_scratch_spine", "test_fake_routing", "test_scratch_spine,test_fake_routing"} {
		copy := *info
		copy.Settings = append(append([]debug.BuildSetting{}, info.Settings...), debug.BuildSetting{Key: "-tags", Value: tags})
		if err := validateBuildInfo(&copy, commit, "darwin-arm64"); err == nil {
			t.Fatalf("the production record accepted %s", tags)
		}
	}
	if err := validateBuildInfo(info, strings.Repeat("b", 40), "darwin-arm64"); err == nil {
		t.Fatal("the binary accepted a different commit")
	}
	if err := validateBuildInfo(info, commit, "linux-arm64"); err == nil {
		t.Fatal("the binary accepted a different platform")
	}
}

func TestBinaryIdentityFromLinkedExecutables(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "cmd", "mc")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		filepath.Join(root, "go.mod"):    "module mc\n\ngo 1.25.0\n",
		filepath.Join(source, "main.go"): "package main\nimport \"fmt\"\nvar releaseBuildID = \"development\"\nfunc main() { fmt.Println(releaseBuildID) }\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	commit := strings.Repeat("a", 40)
	build := func(t *testing.T, platform, path, ldflags string) {
		command := exec.Command("mise", "exec", "--", "go", "-C", source, "build", "-trimpath", "-ldflags", ldflags, "-o", path, ".")
		command.Env = append(os.Environ(), "GOENV=off", "GOFLAGS=", "CGO_ENABLED=0", "GOOS="+platform, "GOARCH=arm64")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("compile the binary fixture: %v\n%s", err, output)
		}
	}
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			path := filepath.Join(root, "binary-"+platform)
			build(t, platform, path, "-w -X main.releaseBuildID="+commit)
			if err := validateBinary(path, commit, platform+"-arm64"); err != nil {
				t.Fatal(err)
			}
			if err := validateBinary(path, strings.Repeat("b", 40), platform+"-arm64"); err == nil {
				t.Fatal("the binary accepted a different embedded release identity")
			}
			// The same build without -w keeps DWARF data, which the release refuses.
			debugPath := filepath.Join(root, "binary-"+platform+"-dwarf")
			build(t, platform, debugPath, "-X main.releaseBuildID="+commit)
			err := validateBinary(debugPath, commit, platform+"-arm64")
			if err == nil || !strings.Contains(err.Error(), "DWARF") {
				t.Fatalf("the binary with DWARF data was accepted: %v", err)
			}
		})
	}
}
