package deployment

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestTheEmbeddedHostBunPinIsValidAndMatchesTheTrackedFile(t *testing.T) {
	pin, err := LoadHostBunPin()
	if err != nil {
		t.Fatal(err)
	}
	tracked, err := os.ReadFile("hostbun.json")
	if err != nil || !bytes.Equal(tracked, hostBunPinBody) {
		t.Fatalf("the embedded pin differs from hostbun.json: %v", err)
	}
	archive := pin.Archives[HostBunPlatform]
	if archive.Name != "bun-darwin-aarch64.zip" || len(pin.Archives) != 1 {
		t.Fatalf("pin = %+v", pin)
	}
}

// The host Bun of a published install and the Bun of a source install must
// be the same release.
func TestTheHostBunPinMatchesTheMiseToolchain(t *testing.T) {
	pin, err := LoadHostBunPin()
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Tools map[string]string `toml:"tools"`
	}
	if _, err := toml.DecodeFile(filepath.Join("..", "..", ".mise.toml"), &config); err != nil {
		t.Fatal(err)
	}
	if config.Tools["bun"] != pin.Version {
		t.Fatalf("hostbun.json pins Bun %s, .mise.toml pins Bun %s", pin.Version, config.Tools["bun"])
	}
}

func hostBunPinFixture() map[string]any {
	return map[string]any{
		"version": "1.2.3",
		"archives": map[string]any{
			"darwin-arm64": map[string]any{
				"name":   "bun-darwin-aarch64.zip",
				"url":    "https://example.com/releases/download/bun-v1.2.3/bun-darwin-aarch64.zip",
				"size":   10,
				"sha256": strings.Repeat("ab", 32),
			},
		},
	}
}

func TestHostBunPinParsesAValidFixture(t *testing.T) {
	body, err := json.Marshal(hostBunPinFixture())
	if err != nil {
		t.Fatal(err)
	}
	pin, err := ParseHostBunPin(body)
	if err != nil {
		t.Fatal(err)
	}
	archive := pin.Archives["darwin-arm64"]
	if pin.Version != "1.2.3" || archive.Size != 10 || archive.SHA256 != strings.Repeat("ab", 32) || archive.Name != "bun-darwin-aarch64.zip" {
		t.Fatalf("pin = %+v", pin)
	}
}

func TestHostBunPinRejectsInvalidValues(t *testing.T) {
	archive := func(pin map[string]any) map[string]any {
		return pin["archives"].(map[string]any)["darwin-arm64"].(map[string]any)
	}
	for name, tc := range map[string]struct {
		mutate func(map[string]any)
		want   string
	}{
		"unknown key":      {func(p map[string]any) { p["mirror"] = "https://example.com" }, "unknown field"},
		"missing version":  {func(p map[string]any) { delete(p, "version") }, "version"},
		"prefixed version": {func(p map[string]any) { p["version"] = "v1.2.3" }, "version"},
		"no archives":      {func(p map[string]any) { p["archives"] = map[string]any{} }, "darwin-arm64"},
		"other platform only": {func(p map[string]any) {
			p["archives"] = map[string]any{"linux-arm64": archive(p)}
		}, "darwin-arm64"},
		"invalid platform": {func(p map[string]any) {
			p["archives"].(map[string]any)["Darwin ARM"] = archive(p)
		}, "platform"},
		"archive path":    {func(p map[string]any) { archive(p)["name"] = "../bun.zip" }, "name"},
		"archive tarball": {func(p map[string]any) { archive(p)["name"] = "bun.tar.gz" }, "name"},
		"zero size":       {func(p map[string]any) { archive(p)["size"] = 0 }, "size"},
		"short digest":    {func(p map[string]any) { archive(p)["sha256"] = "abcd" }, "sha256"},
		"uppercase digest": {func(p map[string]any) {
			archive(p)["sha256"] = strings.Repeat("AB", 32)
		}, "sha256"},
		"http url": {func(p map[string]any) {
			archive(p)["url"] = "http://example.com/releases/download/bun-v1.2.3/bun-darwin-aarch64.zip"
		}, "url"},
		"url with query": {func(p map[string]any) {
			archive(p)["url"] = "https://example.com/releases/download/bun-v1.2.3/bun-darwin-aarch64.zip?x=1"
		}, "url"},
		"url with credentials": {func(p map[string]any) {
			archive(p)["url"] = "https://user@example.com/releases/download/bun-v1.2.3/bun-darwin-aarch64.zip"
		}, "url"},
		"url of another version": {func(p map[string]any) {
			archive(p)["url"] = "https://example.com/releases/download/bun-v1.2.2/bun-darwin-aarch64.zip"
		}, "url"},
		"url of another name": {func(p map[string]any) {
			archive(p)["url"] = "https://example.com/releases/download/bun-v1.2.3/bun-darwin-x64.zip"
		}, "url"},
	} {
		t.Run(name, func(t *testing.T) {
			pin := hostBunPinFixture()
			tc.mutate(pin)
			body, err := json.Marshal(pin)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ParseHostBunPin(body)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "host Bun pin") {
				t.Fatalf("error = %v, want the host Bun pin and %q", err, tc.want)
			}
		})
	}
	body, err := json.Marshal(hostBunPinFixture())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseHostBunPin(append(body, []byte(` {}`)...)); err == nil {
		t.Fatal("the pin accepted data after its object")
	}
}
