package deployment

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	operatorconfig "mc/config"
)

func TestOpenCodeModelListUsesOnlyReleaseCatalog(t *testing.T) {
	home := agentProfileTestHome(t)
	_, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), func(cfg *operatorconfig.Config) error {
		cfg.Runtimes.Credentials["catalog"] = operatorconfig.CredentialSource{Name: "Catalog", Kind: operatorconfig.CredentialKindOpenCodeAPIKey, CatalogEntry: "moonshotai"}
		return cfg.Validate()
	})
	if err != nil {
		t.Fatal(err)
	}
	previous := agentModelAccess
	defer func() { agentModelAccess = previous }()
	agentModelAccess = func(string, string) (modelCatalogAccess, error) {
		t.Fatal("The catalog list requested provider access.")
		return modelCatalogAccess{}, nil
	}
	models, err := AgentConnectionModels(home, AgentModelListRequest{ConnectionID: "catalog"})
	if err != nil || !models.WebSearch || len(models.Models) == 0 {
		t.Fatalf("The release model list failed: %v", err)
	}
	for _, m := range models.Models {
		if m.ID == "kimi-k3" {
			if strings.Join(m.EffortLevels, ",") != "low,high,max" || m.Context == 0 {
				t.Fatal("The model lost its catalog details.")
			}
			return
		}
	}
	t.Fatal("The release model is absent.")
}

func TestOpenCodeDownloadProgressAndRetry(t *testing.T) {
	home := agentProfileTestHome(t)
	launches := 0
	launch := func(string, string) error { launches++; return nil }
	if OpenCodeDownloadStatus(home) != nil {
		t.Fatal("The empty installation contains an OpenCode operation.")
	}
	for _, cause := range []string{"The network connection failed.", "The archive checksum differs.", "The archive is incomplete."} {
		operation, err := startOpenCodeDownload(home, true, launch)
		if err != nil || operation.Status != "pending" {
			t.Fatal(operation, err)
		}
		_, _ = startOpenCodeDownload(home, true, launch)
		err = runOpenCodeDownloadWith(home, func(_ context.Context, progress func(string)) error {
			progress("Download the archive.")
			status := OpenCodeDownloadStatus(home)
			if status == nil || status.Message != "Download the archive." || status.Status != "running" {
				t.Fatal("The progress did not persist.")
			}
			return errors.New(cause)
		})
		if err != nil {
			t.Fatal(err)
		}
		if status := OpenCodeDownloadStatus(home); status.Status != "failed" || status.Message != cause {
			t.Fatal(status)
		}
	}
	if launches != 3 {
		t.Fatal("A duplicate operation started.")
	}
	_, _ = startOpenCodeDownload(home, true, launch)
	if err := runOpenCodeDownloadWith(home, func(context.Context, func(string)) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if OpenCodeDownloadStatus(home).Status != "succeeded" {
		t.Fatal("Retry failed.")
	}
}

func openCodeArchiveFixture(t *testing.T, extra *tar.Header) (string, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "archive.tar.gz")
	out, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	gzipper := gzip.NewWriter(out)
	writer := tar.NewWriter(gzipper)
	elf := make([]byte, 20)
	copy(elf, "\x7fELF")
	elf[4] = 2
	elf[5] = 1
	elf[18] = 0xb7
	for name, body := range map[string][]byte{"node_modules/@opencode/cli-linux-arm64/bin/opencode": elf, "control/shell": []byte("shell"), "control/server.mjs": []byte("plugin"), "node_modules/opencode-websearch/dist/index.js": []byte("websearch")} {
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if extra != nil {
		if err := writer.WriteHeader(extra); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipper.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(file)
	sum := sha256.Sum256(body)
	return file, hex.EncodeToString(sum[:])
}

func TestOpenCodeArchiveRefusesChecksumAndUnsafePaths(t *testing.T) {
	file, checksum := openCodeArchiveFixture(t, nil)
	if err := checkOpenCodeArchive(file, checksum); err != nil {
		t.Fatal(err)
	}
	if checkOpenCodeArchive(file, strings.Repeat("0", 64)) == nil {
		t.Fatal("A changed archive passed.")
	}
	for _, header := range []*tar.Header{{Name: "../escape", Typeflag: tar.TypeReg, Mode: 0644}, {Name: "control", Typeflag: tar.TypeSymlink, Linkname: "."}, {Name: "node_modules/.bin/unsafe", Typeflag: tar.TypeSymlink, Linkname: "../../../../escape"}} {
		file, checksum := openCodeArchiveFixture(t, header)
		if checkOpenCodeArchive(file, checksum) == nil {
			t.Fatal("An unsafe archive passed.")
		}
	}
}

func TestOpenCodePartialUnpackRemovesVolume(t *testing.T) {
	file, checksum := openCodeArchiveFixture(t, nil)
	home := agentProfileTestHome(t)
	removed := false
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch args[0] + " " + args[1] {
		case "container ls":
			return nil, nil
		case "volume inspect":
			return []byte("no such volume"), errors.New("absent")
		case "volume create":
			return nil, nil
		case "volume rm":
			removed = true
			return nil, nil
		case "run --rm":
			return []byte("incomplete archive"), errors.New("unpack failed")
		}
		t.Fatalf("Unexpected Docker command: %v", args)
		return nil, nil
	}
	if InstallOpenCodeArchive(context.Background(), home, "image", file, checksum, strings.Repeat("a", 40), run) == nil || !removed {
		t.Fatal("The partial volume remained usable.")
	}
}

func TestOpenCodeReleaseIntegrationUsesItsExistingHarness(t *testing.T) {
	providers := AgentProviderCatalog()
	for id, source := range operatorconfig.Default().Runtimes.Credentials {
		if !agentAPIKeyKind(source.Kind) {
			continue
		}
		found := false
		for _, row := range providers {
			if row.ID == id {
				found = true
				if row.Harness != source.Harness() || row.CatalogEntry == "" || row.Address == "" {
					t.Fatal("The release integration changed harness or lost its address.")
				}
			}
		}
		if !found {
			t.Fatalf("The provider search omitted %s.", id)
		}
	}
}

func TestOpenCodeModelTestRequiresNativeToolAndFinalReply(t *testing.T) {
	output, err := os.ReadFile("testdata/opencode-model-test.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	got, err := InspectLiveProviderOutput("opencode", output)
	if err != nil || got.ToolUses != 1 || got.ToolResults != 1 || got.StreamEvents < 3 {
		t.Fatalf("The native model test failed: %+v, %v", got, err)
	}
	for _, invalid := range []string{
		strings.Replace(string(output), `"exit":0`, `"exit":1`, 1),
		strings.ReplaceAll(string(output), `"type":"tool_use"`, `"type":"text"`),
		strings.Replace(string(output), `"event":"turn-complete"`, `"event":"infrastructure-failure"`, 1),
		strings.ReplaceAll(string(output), `"reason":"stop"`, `"reason":"length"`),
	} {
		if _, err := InspectLiveProviderOutput("opencode", []byte(invalid)); err == nil {
			t.Fatal("An incomplete native model test passed.")
		}
	}
}

func TestOpenCodeInstallChecksTheNativeVersion(t *testing.T) {
	for _, version := range []string{"opencode v2.2.5", "opencode v2.2.4", "2.2.5"} {
		t.Run(version, func(t *testing.T) {
			root := t.TempDir()
			output := filepath.Join(root, "output")
			if err := os.Mkdir(output, 0700); err != nil {
				t.Fatal(err)
			}
			archive := filepath.Join(root, "native.tar.gz")
			file, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			gz := gzip.NewWriter(file)
			tw := tar.NewWriter(gz)
			body := "#!/bin/sh\nprintf '%s\\n' '" + version + "'\n"
			if err := tw.WriteHeader(&tar.Header{Name: "node_modules/@opencode/cli-linux-arm64/bin/opencode", Mode: 0755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
			for _, close := range []func() error{tw.Close, gz.Close, file.Close} {
				if err := close(); err != nil {
					t.Fatal(err)
				}
			}
			script := strings.NewReplacer("/output", output, "/archive.tar.gz", archive).Replace(openCodeInstallScript)
			result, err := exec.Command("/bin/sh", "-ec", script, "install", "release", "checksum", "2.2.5").CombinedOutput()
			if version != "opencode v2.2.5" {
				if err == nil || !strings.Contains(string(result), "This release requires OpenCode 2.2.5.") {
					t.Fatalf("The version check passed: %s", result)
				}
				if _, err := os.Stat(filepath.Join(output, "release-id")); !os.IsNotExist(err) {
					t.Fatal("The invalid program received a release identity.")
				}
				return
			}
			if err != nil {
				t.Fatalf("The pinned program failed: %s: %v", result, err)
			}
			for name, want := range map[string]string{"release-id": "release\n", "archive-sha256": "checksum\n"} {
				body, err := os.ReadFile(filepath.Join(output, name))
				if err != nil || string(body) != want {
					t.Fatalf("The program lacks its %s: %q: %v", name, body, err)
				}
			}
		})
	}
}
