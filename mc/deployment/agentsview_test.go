package deployment

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheEmbeddedAgentsViewPinIsValidAndMatchesTheTrackedFile(t *testing.T) {
	pin, err := LoadAgentsViewPin()
	if err != nil {
		t.Fatal(err)
	}
	tracked, err := os.ReadFile("agentsview.json")
	if err != nil || !bytes.Equal(tracked, agentsViewPinBody) {
		t.Fatalf("the embedded pin differs from agentsview.json: %v", err)
	}
	if pin.Executable.Name != "agentsview" || len(pin.DisabledAgents) == 0 {
		t.Fatalf("pin = %+v", pin)
	}
}

func TestTheRepositoryHoldsTheAgentsViewLicense(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "third_party", "agentsview", "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "MIT License\n") {
		t.Fatalf("the AgentsView license text is not the MIT License:\n%s", body)
	}
}

func agentsViewPinFixture() map[string]any {
	return map[string]any{
		"version":  "1.2.3",
		"platform": "darwin-arm64",
		"archive": map[string]any{
			"name":   "agentsview_1.2.3_darwin_arm64.tar.gz",
			"url":    "https://example.com/releases/download/v1.2.3/agentsview_1.2.3_darwin_arm64.tar.gz",
			"size":   10,
			"sha256": strings.Repeat("ab", 32),
		},
		"executable": map[string]any{
			"name":   "agentsview",
			"size":   20,
			"sha256": strings.Repeat("cd", 32),
		},
		"disabled_agents": []any{"cursor", "gemini"},
	}
}

func TestAgentsViewPinParsesAValidFixture(t *testing.T) {
	body, err := json.Marshal(agentsViewPinFixture())
	if err != nil {
		t.Fatal(err)
	}
	pin, err := ParseAgentsViewPin(body)
	if err != nil {
		t.Fatal(err)
	}
	if pin.Version != "1.2.3" || pin.Archive.Size != 10 || pin.Executable.SHA256 != strings.Repeat("cd", 32) ||
		strings.Join(pin.DisabledAgents, ",") != "cursor,gemini" {
		t.Fatalf("pin = %+v", pin)
	}
}

func TestAgentsViewPinRejectsInvalidValues(t *testing.T) {
	archive := func(pin map[string]any) map[string]any { return pin["archive"].(map[string]any) }
	executable := func(pin map[string]any) map[string]any { return pin["executable"].(map[string]any) }
	for name, tc := range map[string]struct {
		mutate func(map[string]any)
		want   string
	}{
		"unknown key":          {func(p map[string]any) { p["mirror"] = "https://example.com" }, "unknown field"},
		"missing version":      {func(p map[string]any) { delete(p, "version") }, "version"},
		"prefixed version":     {func(p map[string]any) { p["version"] = "v1.2.3" }, "version"},
		"other platform":       {func(p map[string]any) { p["platform"] = "linux-arm64" }, "platform"},
		"archive path":         {func(p map[string]any) { archive(p)["name"] = "../agentsview.tar.gz" }, "archive name"},
		"executable path":      {func(p map[string]any) { executable(p)["name"] = "bin/agentsview" }, "executable name"},
		"zero archive size":    {func(p map[string]any) { archive(p)["size"] = 0 }, "archive size"},
		"negative binary size": {func(p map[string]any) { executable(p)["size"] = -1 }, "executable size"},
		"short digest":         {func(p map[string]any) { archive(p)["sha256"] = "abcd" }, "archive sha256"},
		"uppercase digest":     {func(p map[string]any) { executable(p)["sha256"] = strings.Repeat("AB", 32) }, "executable sha256"},
		"non-hex digest":       {func(p map[string]any) { executable(p)["sha256"] = strings.Repeat("zz", 32) }, "executable sha256"},
		"http url": {func(p map[string]any) {
			archive(p)["url"] = "http://example.com/releases/download/v1.2.3/agentsview_1.2.3_darwin_arm64.tar.gz"
		}, "archive url"},
		"url with query": {func(p map[string]any) {
			archive(p)["url"] = "https://example.com/releases/download/v1.2.3/agentsview_1.2.3_darwin_arm64.tar.gz?x=1"
		}, "archive url"},
		"url with credentials": {func(p map[string]any) {
			archive(p)["url"] = "https://user@example.com/releases/download/v1.2.3/agentsview_1.2.3_darwin_arm64.tar.gz"
		}, "archive url"},
		"url of another version": {func(p map[string]any) {
			archive(p)["url"] = "https://example.com/releases/download/v1.2.2/agentsview_1.2.3_darwin_arm64.tar.gz"
		}, "archive url"},
		"no disabled agents":      {func(p map[string]any) { p["disabled_agents"] = []any{} }, "at least one agent"},
		"empty agent ID":          {func(p map[string]any) { p["disabled_agents"] = []any{"cursor", ""} }, "invalid agent ID"},
		"agent ID with a space":   {func(p map[string]any) { p["disabled_agents"] = []any{"cursor ide"} }, "invalid agent ID"},
		"duplicate agent ID":      {func(p map[string]any) { p["disabled_agents"] = []any{"cursor", "gemini", "cursor"} }, "more than once"},
		"disabled Claude reader":  {func(p map[string]any) { p["disabled_agents"] = []any{"cursor", "claude"} }, `"claude"`},
		"disabled Codex reader":   {func(p map[string]any) { p["disabled_agents"] = []any{"codex"} }, `"codex"`},
		"disabled agents as text": {func(p map[string]any) { p["disabled_agents"] = "cursor" }, "disabled_agents"},
	} {
		t.Run(name, func(t *testing.T) {
			pin := agentsViewPinFixture()
			tc.mutate(pin)
			body, err := json.Marshal(pin)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ParseAgentsViewPin(body)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "AgentsView pin") {
				t.Fatalf("error = %v, want the AgentsView pin and %q", err, tc.want)
			}
		})
	}
	body, err := json.Marshal(agentsViewPinFixture())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseAgentsViewPin(append(body, []byte(` {}`)...)); err == nil {
		t.Fatal("the pin accepted data after its object")
	}
}

func TestAgentsViewPathsFollowTheMCHomeLayout(t *testing.T) {
	got := AgentsViewPathsFor("/Users/alice/.culler")
	want := AgentsViewPaths{
		Executable:        "/Users/alice/.culler/bin/agentsview",
		Root:              "/Users/alice/.culler/agentsview",
		Data:              "/Users/alice/.culler/agentsview/data",
		Config:            "/Users/alice/.culler/agentsview/data/config.toml",
		DebugLog:          "/Users/alice/.culler/agentsview/data/debug.log",
		Token:             "/Users/alice/.culler/agentsview/token",
		Home:              "/Users/alice/.culler/agentsview/home",
		ClaudeTraceRoot:   "/Users/alice/.culler/agentsview/traces/claude/projects",
		ClaudeTraceCopies: "/Users/alice/.culler/agentsview/traces/claude/projects/culler",
		CodexTraceRoot:    "/Users/alice/.culler/agentsview/traces/codex/sessions",
		StdoutLog:         "/Users/alice/.culler/logs/agentsview.stdout.log",
		StderrLog:         "/Users/alice/.culler/logs/agentsview.stderr.log",
		Health:            "/Users/alice/.culler/health/agentsview.json",
	}
	if got != want {
		t.Fatalf("paths = %+v\nwant %+v", got, want)
	}
	if key := WikiKeyPath("/Users/alice/.culler"); key != "/Users/alice/.culler/state/wiki-key" {
		t.Fatalf("wiki key = %q", key)
	}
	if root := WikiExportRoot("/Users/alice/.culler"); root != "/Users/alice/.culler/wiki" {
		t.Fatalf("wiki export root = %q", root)
	}
}
