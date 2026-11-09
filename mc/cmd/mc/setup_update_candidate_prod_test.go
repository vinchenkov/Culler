//go:build !test_fake_routing

package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	operatorconfig "mc/config"
	"mc/deployment"
	"mc/distribution"
	"mc/opencodecatalog"
	"mc/verbs"
)

func candidateProfileConfig(t *testing.T, home string, enabled bool) []byte {
	t.Helper()
	cfg := operatorconfig.Default()
	provider := opencodecatalog.Release().Providers[0]
	cfg.Runtimes.Credentials["catalog"] = operatorconfig.CredentialSource{Kind: operatorconfig.CredentialKindOpenCodeAPIKey, CatalogEntry: provider.ID}
	cfg.Runtimes.Bindings["catalog"] = operatorconfig.RuntimeBinding{Harness: "opencode", Model: "removed-model", Credential: "catalog", Effort: "high"}
	if enabled {
		cfg.Runtimes.EnabledBindings = append(cfg.Runtimes.EnabledBindings, "catalog")
	}
	body, err := operatorconfig.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), body, 0600); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestCandidateAssessmentPreservesInvalidSavedProfiles(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			home := t.TempDir()
			before := candidateProfileConfig(t, home, enabled)
			p := &publishedSetup{home: home}
			if err := p.assessCandidate(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(p.candidateAssessment.Lines()) != 1 || len(p.candidateAssessment.configSHA256()) != 64 {
				t.Fatalf("assessment: %#v", p.candidateAssessment)
			}
			line := p.candidateAssessment.Lines()[0]
			if enabled && !strings.Contains(line, "stays stopped") || !enabled && !strings.Contains(line, "disabled profile") {
				t.Fatalf("wrong disclosure: %s", line)
			}
			if err := p.verifyCandidateConfiguration(); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(filepath.Join(home, "config.toml"))
			if err != nil || string(before) != string(after) {
				t.Fatal("assessment changed the saved choices")
			}
			if err := os.WriteFile(filepath.Join(home, "config.toml"), append(before, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			if err := p.verifyCandidateConfiguration(); err == nil {
				t.Fatal("a changed configuration passed confirmation")
			}
		})
	}
}

func TestCandidateAssessmentRefusesUnsafeConfiguration(t *testing.T) {
	home := t.TempDir()
	if _, err := assessUpdateConfiguration(home); err != nil {
		t.Fatal(err)
	}
	candidateProfileConfig(t, home, false)
	if err := os.Chmod(filepath.Join(home, "config.toml"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := assessUpdateConfiguration(home); err == nil {
		t.Fatal("unsafe configuration passed")
	}
}

func TestCandidateAssessmentChecksProviderModelAndExplicitEffort(t *testing.T) {
	for _, problem := range []string{"provider", "model", "effort", "default"} {
		t.Run(problem, func(t *testing.T) {
			home := t.TempDir()
			candidateProfileConfig(t, home, false)
			cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
			if err != nil {
				t.Fatal(err)
			}
			provider := opencodecatalog.Release().Providers[0]
			profile := cfg.Runtimes.Bindings["catalog"]
			profile.Model, profile.Effort = provider.Models[0].ID, ""
			switch problem {
			case "provider":
				connection := cfg.Runtimes.Credentials["catalog"]
				connection.CatalogEntry = "removed-provider"
				cfg.Runtimes.Credentials["catalog"] = connection
			case "model":
				profile.Model = "removed-model"
			case "effort":
				for _, effort := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "thinking"} {
					if opencodecatalog.ValidateProfile(provider.ID, profile.Model, effort) != nil {
						profile.Effort = effort
						break
					}
				}
				if profile.Effort == "" {
					t.Fatal("the fixture model accepts every effort")
				}
			}
			cfg.Runtimes.Bindings["catalog"] = profile
			if _, err := operatorconfig.WriteAtomic(filepath.Join(home, "config.toml"), cfg); err != nil {
				t.Fatal(err)
			}
			assessment, err := assessUpdateConfiguration(home)
			if err != nil {
				t.Fatal(err)
			}
			if (len(assessment.Lines()) == 0) != (problem == "default") {
				t.Fatalf("assessment: %#v", assessment.Lines())
			}
		})
	}
}

func TestCandidateOpenCodeUsesIsolatedVerifiedArchive(t *testing.T) {
	home, scratch := t.TempDir(), t.TempDir()
	p := &publishedSetup{home: home, record: distribution.Record{Image: "candidate", Commit: strings.Repeat("a", 40), OpenCode: &distribution.Asset{Name: "opencode.tar.gz"}}, update: &deployment.UpdateJournal{ID: strings.Repeat("b", 32)}}
	archive := filepath.Join(deployment.UpdateDirectory(home, p.update.ID), p.record.OpenCode.Name)
	if err := os.MkdirAll(filepath.Dir(archive), 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(file)
	writer := tar.NewWriter(compressed)
	elf := make([]byte, 20)
	copy(elf, "\x7fELF")
	elf[4], elf[5], elf[18] = 2, 1, 0xb7
	for name, body := range map[string][]byte{"node_modules/@opencode/cli-linux-arm64/bin/opencode": elf, "control/shell": []byte("shell"), "control/server.mjs": []byte("plugin"), "node_modules/opencode-websearch/dist/index.js": []byte("websearch")} {
		if err := writer.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	p.record.OpenCode.SHA256 = hex.EncodeToString(digest[:])
	live, _ := deployment.OpenCodeVolume(home)
	names, _ := deployment.RuntimeNames(home)
	isolated, _ := deployment.OpenCodeVolume(scratch)
	installed := false
	p.docker = func(_ context.Context, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if args[0] == "volume" && args[1] == "inspect" {
			if args[len(args)-1] == live {
				return json.Marshal(map[string]any{"Name": live, "Driver": "local", "Labels": map[string]string{"mc-component": "opencode", "mc-deployment": names.Suffix}})
			}
			return []byte("no such volume"), errors.New("absent")
		}
		if args[0] == "run" {
			installed = true
			if !strings.Contains(joined, "src="+isolated+",dst=/output") || strings.Contains(joined, "src="+live+",") || !strings.Contains(joined, "src="+archive+",dst=/archive.tar.gz,readonly") {
				t.Fatalf("unsafe archive check: %s", joined)
			}
		}
		return nil, nil
	}
	got, err := p.candidateCheckOpenCode(context.Background(), scratch)
	if err != nil || got != isolated || !installed {
		t.Fatalf("optional check: %q %v", got, err)
	}
	installed = false
	p.record.OpenCode.SHA256 = strings.Repeat("f", 64)
	if _, err := p.candidateCheckOpenCode(context.Background(), scratch); err == nil || installed {
		t.Fatal("an invalid archive changed a volume")
	}
}

func TestCandidateChecksArchivedImagesWithIsolatedRuntime(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "compatible", true: "incompatible"}[fail], func(t *testing.T) {
			home := t.TempDir()
			names, _ := deployment.RuntimeNames(home)
			optional, _ := deployment.OpenCodeVolume(home)
			record := distribution.Record{Schema: "schema", Commit: strings.Repeat("a", 40), Image: "candidate"}
			p := &publishedSetup{home: home, record: record, update: &deployment.UpdateJournal{ID: strings.Repeat("f", 32)}}
			var calls []string
			p.docker = func(_ context.Context, args ...string) ([]byte, error) {
				joined := strings.Join(args, " ")
				calls = append(calls, joined)
				if args[0] == "container" && args[1] == "inspect" {
					return []byte("no such container"), errors.New("absent")
				}
				if args[0] == "volume" && args[1] == "inspect" {
					if args[len(args)-1] == optional || args[len(args)-1] != names.Volume {
						return []byte("no such volume"), errors.New("absent")
					}
					return json.Marshal(map[string]any{"Name": names.Volume, "Driver": "local"})
				}
				if strings.Contains(joined, "mc __release-database") {
					return json.Marshal(verbs.ReleaseDatabase{Schema: record.Schema, Empty: true, ProjectImages: []verbs.ReleaseProjectImage{
						{ProjectID: "archived", Image: "selected", Base: "base", Archived: true},
						{ProjectID: "active", Image: "selected", Base: "base"},
					}})
				}
				if strings.Contains(joined, "/opt/culler-runtime/bin/check") {
					if strings.Contains(joined, "src="+names.RuntimeVolume+",") || strings.Contains(joined, "dst=/mc/spine") || !strings.Contains(joined, "readonly,volume-nocopy") || !strings.Contains(joined, "--network none") || !strings.Contains(joined, "--label mc-deployment="+names.Suffix) {
						t.Fatalf("unsafe runtime check: %s", joined)
					}
					if fail {
						return nil, errors.New("runtime failed")
					}
				}
				return nil, nil
			}
			err := p.checkCandidateProjectImages(context.Background())
			if (err != nil) != fail {
				t.Fatalf("result: %v", err)
			}
			joined := strings.Join(calls, "\n")
			if strings.Count(joined, "/opt/culler-runtime/bin/check") != 1 || !strings.Contains(joined, "image inspect base") || !strings.Contains(joined, "image inspect selected") {
				t.Fatalf("checks: %s", joined)
			}
			if !strings.HasPrefix(calls[len(calls)-1], "volume rm ") || !strings.Contains(joined, "rm --force culler-update-check-") {
				t.Fatalf("temporary runtime remained: %s", joined)
			}
		})
	}
}

func TestCandidateRuntimeScriptIsValidShell(t *testing.T) {
	for _, openCode := range []bool{false, true} {
		script := candidateRuntimeScript(openCode)
		if output, err := exec.Command("/bin/sh", "-n", "-c", script).CombinedOutput(); err != nil {
			t.Fatalf("OpenCode %v: %v: %s\n%s", openCode, err, output, script)
		}
		if strings.Contains(script, "/opt/culler-opencode/") != openCode {
			t.Fatalf("OpenCode %v: %s", openCode, script)
		}
	}
}

func TestCandidateCheckPreservesForeignVolume(t *testing.T) {
	p := &publishedSetup{}
	var calls []string
	p.docker = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return []byte(`{"Name":"check-volume","Driver":"local","Labels":{"mc-component":"update-check","mc-deployment":"other"}}`), nil
	}
	if err := p.clearCandidateCheckVolume(context.Background(), "check-volume", "update-check", "ours"); err == nil {
		t.Fatal("a foreign volume passed")
	}
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "volume inspect ") {
		t.Fatalf("a foreign volume changed: %v", calls)
	}
}
