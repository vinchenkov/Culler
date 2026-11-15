package main_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var agentsViewCLIExecutable = []byte("#!/bin/sh\necho synthetic agentsview\n")

// agentsViewCLIFixture writes a synthetic pin and returns the environment
// that selects it, the owner-only MC_HOME, and its canonical path.
func agentsViewCLIFixture(t *testing.T, archiveURL string) ([]string, string, string) {
	t.Helper()
	sum := sha256.Sum256(agentsViewCLIExecutable)
	pin := map[string]any{
		"version":  "1.2.3",
		"platform": "darwin-arm64",
		"archive": map[string]any{
			"name":   "agentsview_1.2.3_darwin_arm64.tar.gz",
			"url":    archiveURL,
			"size":   100,
			"sha256": strings.Repeat("ab", 32),
		},
		"executable": map[string]any{
			"name":   "agentsview",
			"size":   len(agentsViewCLIExecutable),
			"sha256": hex.EncodeToString(sum[:]),
		},
		"disabled_agents": []string{"cursor"},
	}
	body, err := json.Marshal(pin)
	if err != nil {
		t.Fatal(err)
	}
	pinPath := filepath.Join(t.TempDir(), "agentsview.json")
	if err := os.WriteFile(pinPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	return []string{"MC_HOME=" + home, "MC_TEST_AGENTSVIEW_PIN=" + pinPath}, home, canonical
}

func TestOnboardAgentsViewInstallsAVerifiedExecutable(t *testing.T) {
	env, _, canonical := agentsViewCLIFixture(t, "https://example.com/download/v1.2.3/agentsview_1.2.3_darwin_arm64.tar.gz")
	source := filepath.Join(t.TempDir(), "agentsview")
	if err := os.WriteFile(source, agentsViewCLIExecutable, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(canonical, "bin", "agentsview")
	for _, want := range []string{"done", "ok"} {
		res := runMC(t, env, "", "__onboard", "agentsview", "--executable", source)
		if res.code != 0 {
			t.Fatalf("install failed (%d): %s", res.code, res.stderr)
		}
		if res.json["status"] != want || res.json["version"] != "1.2.3" || res.json["path"] != target {
			t.Fatalf("install result = %v, want status %s at %s", res.json, want, target)
		}
	}
	body, err := os.ReadFile(target)
	info, statErr := os.Stat(target)
	if err != nil || statErr != nil || string(body) != string(agentsViewCLIExecutable) || info.Mode().Perm() != 0o755 {
		t.Fatalf("installed executable = %q, %v, %v, %v", body, info, err, statErr)
	}
}

func TestOnboardAgentsViewRefusesAMismatchedExecutable(t *testing.T) {
	env, _, canonical := agentsViewCLIFixture(t, "https://example.com/download/v1.2.3/agentsview_1.2.3_darwin_arm64.tar.gz")
	source := filepath.Join(t.TempDir(), "agentsview")
	if err := os.WriteFile(source, []byte("#!/bin/sh\necho changed\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(agentsViewCLIExecutable)
	res := runMC(t, env, "", "__onboard", "agentsview", "--executable", source)
	if res.code != 1 || !strings.Contains(res.stderr, "the AgentsView executable differs from its pin") ||
		!strings.Contains(res.stderr, hex.EncodeToString(sum[:])) {
		t.Fatalf("mismatch = %d %s", res.code, res.stderr)
	}
	if _, err := os.Stat(filepath.Join(canonical, "bin", "agentsview")); !os.IsNotExist(err) {
		t.Fatalf("a mismatched executable was installed: %v", err)
	}
}

// The download address is a closed loopback port, so the test proves the
// download path without a network connection.
func TestOnboardAgentsViewDownloadsOnlyForADifferentExecutable(t *testing.T) {
	env, _, canonical := agentsViewCLIFixture(t, "https://127.0.0.1:1/download/v1.2.3/agentsview_1.2.3_darwin_arm64.tar.gz")
	res := runMC(t, env, "", "__onboard", "agentsview")
	if res.code != 1 || !strings.Contains(res.stderr, "download the AgentsView release archive") {
		t.Fatalf("download failure = %d %s", res.code, res.stderr)
	}
	target := filepath.Join(canonical, "bin", "agentsview")
	if err := os.WriteFile(target, agentsViewCLIExecutable, 0o755); err != nil {
		t.Fatal(err)
	}
	res = runMC(t, env, "", "__onboard", "agentsview")
	if res.code != 0 || res.json["status"] != "ok" || res.json["path"] != target {
		t.Fatalf("a current executable = %d %v %s", res.code, res.json, res.stderr)
	}
	entries, err := os.ReadDir(filepath.Join(canonical, "bin"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("the download left files in MC_HOME/bin: %v, %v", entries, err)
	}
}

func TestOnboardAgentsViewRequiresHostScopeAndValidArguments(t *testing.T) {
	env, home, _ := agentsViewCLIFixture(t, "https://example.com/download/v1.2.3/agentsview_1.2.3_darwin_arm64.tar.gz")
	spine := initSpine(t)
	agent := append(runJSONEnv(t, spine, "agentsview-agent", "pipeline", "worker"), env...)
	res := runMC(t, agent, "", "__onboard", "agentsview", "--executable", "/bin/sh")
	if res.code != 1 || !strings.Contains(res.stderr, "host scope only") {
		t.Fatalf("agent scope = %d %s", res.code, res.stderr)
	}
	if _, err := os.Stat(filepath.Join(home, "bin")); !os.IsNotExist(err) {
		t.Fatalf("an agent request created MC_HOME/bin: %v", err)
	}
	for _, args := range [][]string{
		{"__onboard", "agentsview", "--executable", "agentsview"},
		{"__onboard", "agentsview", "extra"},
		{"__onboard", "agentsview", "--unknown"},
	} {
		res := runMC(t, env, "", args...)
		if res.code != 2 {
			t.Fatalf("%v = %d %s, want a usage error", args, res.code, res.stderr)
		}
	}
}
