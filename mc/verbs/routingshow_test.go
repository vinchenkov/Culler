package verbs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	operatorconfig "mc/config"
)

const rsTable = `# Culler routing
| role | harness | binding |
| --- | --- | --- |
| strategist | claude-sdk | claude |
| arbiter | codex | chatgpt |
| worker | claude-sdk | endpoint |
| verifier | codex | chatgpt |
| packager | claude-sdk | endpoint |
| collie | claude-sdk | claude |
`

// rsHome writes a home whose config enables the test bindings. The
// second-endpoint binding stays configured but disabled.
func rsHome(t *testing.T, routingBody string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("MC_HOME", home)
	cfg := testBindingConfig()
	cfg.Runtimes.Credentials["second-endpoint"] = cfg.Runtimes.Credentials["claude"]
	cfg.Runtimes.Bindings["second-endpoint"] = operatorconfig.RuntimeBinding{
		Harness: "claude-sdk", Model: "second-model", Credential: "second-endpoint",
	}
	if _, err := operatorconfig.WriteAtomic(filepath.Join(home, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	if routingBody != "" {
		if err := os.WriteFile(filepath.Join(home, "routing.md"), []byte(routingBody), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func rsRoutesByRole(t *testing.T, res any) map[string]map[string]any {
	t.Helper()
	routes := res.(map[string]any)["routes"].([]map[string]any)
	if len(routes) != 7 {
		t.Fatalf("routes = %d, want 7", len(routes))
	}
	byRole := map[string]map[string]any{}
	for _, route := range routes {
		byRole[route["role"].(string)] = route
	}
	return byRole
}

func TestRoutingShowReturnsEveryRoleWithItsModel(t *testing.T) {
	rsHome(t, rsTable)
	res, err := RoutingShow()
	if err != nil {
		t.Fatal(err)
	}
	byRole := rsRoutesByRole(t, res)
	worker := byRole["worker"]
	if worker["harness"] != "claude-sdk" || worker["binding"] != "endpoint" || worker["model"] != "endpoint-model" {
		t.Fatalf("worker route = %#v", worker)
	}
	if collie, ok := byRole["collie"]; !ok || collie["binding"] != "claude" {
		t.Fatalf("collie route = %#v", byRole["collie"])
	}
	bindings := res.(map[string]any)["bindings"].([]map[string]any)
	ids := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		ids = append(ids, binding["id"].(string))
	}
	if strings.Join(ids, ",") != "chatgpt,claude,endpoint" {
		t.Fatalf("enabled bindings = %v, want the enabled set only", ids)
	}
	if bindings[1]["harness"] != "claude-sdk" || bindings[1]["model"] != "claude-opus-4-8" {
		t.Fatalf("claude binding = %#v", bindings[1])
	}
}

func TestRoutingSetWritesATableThatRoutingShowReadsBack(t *testing.T) {
	home := rsHome(t, rsTable)
	res, err := RoutingSet("worker", "claude")
	if err != nil {
		t.Fatal(err)
	}
	byRole := rsRoutesByRole(t, res)
	if worker := byRole["worker"]; worker["binding"] != "claude" || worker["harness"] != "claude-sdk" || worker["model"] != "claude-opus-4-8" {
		t.Fatalf("worker route after set = %#v", worker)
	}
	if arbiter := byRole["arbiter"]; arbiter["binding"] != "chatgpt" {
		t.Fatalf("arbiter route changed: %#v", arbiter)
	}
	shown, err := RoutingShow()
	if err != nil {
		t.Fatal(err)
	}
	if worker := rsRoutesByRole(t, shown)["worker"]; worker["binding"] != "claude" {
		t.Fatalf("routing show after set = %#v", worker)
	}
	info, err := os.Lstat(filepath.Join(home, "routing.md"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("routing.md after set = %v, %v", info, err)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".routing.md.tmp-") {
			t.Fatalf("routing set left a temp file: %s", entry.Name())
		}
	}
}

func TestRoutingSetRejectsADisabledBindingAndAnUnknownRole(t *testing.T) {
	home := rsHome(t, rsTable)
	before, err := os.ReadFile(filepath.Join(home, "routing.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ role, binding, want string }{
		{"worker", "second-endpoint", "not enabled"},
		{"worker", "missing", "not enabled"},
		{"critic", "claude", "unknown role"},
	} {
		_, err := RoutingSet(tc.role, tc.binding)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("routing set %s %s error = %v, want %q", tc.role, tc.binding, err, tc.want)
		}
		if _, ok := err.(*DomainError); !ok {
			t.Fatalf("routing set error = %T, want DomainError", err)
		}
	}
	after, err := os.ReadFile(filepath.Join(home, "routing.md"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("a refused routing set changed routing.md: %v", err)
	}
}

func TestRoutingShowRefusesAMissingTable(t *testing.T) {
	rsHome(t, "")
	_, err := RoutingShow()
	if err == nil {
		t.Fatal("missing routing.md did not refuse")
	}
	if _, ok := err.(*DomainError); !ok {
		t.Fatalf("missing routing.md error = %T %v, want DomainError", err, err)
	}
}

func TestRoutingShowRefusesAnInvalidTable(t *testing.T) {
	rsHome(t, "| role | harness | binding |\n| --- | --- | --- |\n| worker | claude-sdk | endpoint |\n")
	_, err := RoutingShow()
	if err == nil {
		t.Fatal("incomplete routing.md did not refuse")
	}
	if _, ok := err.(*DomainError); !ok {
		t.Fatalf("invalid routing.md error = %T %v, want DomainError", err, err)
	}
}
