//go:build test_fake_routing

package verbs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	operatorconfig "mc/config"
)

func TestFakeRoutingDoesNotRestoreRemovedProductionBindings(t *testing.T) {
	home, path := onboardConfigFixture(t)
	cfg, err := operatorconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	delete(cfg.Runtimes.Bindings, "claude")
	cfg.Runtimes.EnabledBindings = []string{"chatgpt"}
	if _, err := operatorconfig.WriteAtomic(path, cfg); err != nil {
		t.Fatal(err)
	}
	if _, _, err := onboardRoutingRegistry(OnboardArgs{RuntimeBindings: "claude"}); err == nil ||
		!strings.Contains(err.Error(), `--runtime-bindings names "claude"`) {
		t.Fatalf("removed production binding error = %v", err)
	}
	registry, first, err := onboardRoutingRegistry(OnboardArgs{RuntimeBindings: "fake"})
	if err != nil || first != "fake" || registry["fake"] != "fake" || len(registry) != 1 {
		t.Fatalf("explicit fake registry = %+v first %q err %v", registry, first, err)
	}
	routingInput := filepath.Join(home, "fake-routing.md")
	body := `| role | harness | binding |
| --- | --- | --- |
| strategist | fake | fake |
| arbiter | fake | fake |
| worker | fake | fake |
| verifier | fake | fake |
| packager | fake | fake |
| homie | fake | fake |
`
	if err := os.WriteFile(routingInput, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	status, _, err := onboardRouting(OnboardArgs{
		RuntimeBindings: "fake", RoutingFile: routingInput,
	})
	if err != nil || status != "done" {
		t.Fatalf("full fake onboarding = status %q err %v", status, err)
	}
	status, _, err = onboardRouting(OnboardArgs{
		RuntimeBindings: "fake", RoutingFile: routingInput,
	})
	if err != nil || status != "ok" {
		t.Fatalf("full fake replay = status %q err %v", status, err)
	}
	cfg, err = operatorconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Runtimes.Bindings["fake"]; ok ||
		strings.Contains(strings.Join(cfg.Runtimes.EnabledBindings, ","), "fake") {
		t.Fatalf("test fake leaked into production config: %+v", cfg.Runtimes)
	}
}
