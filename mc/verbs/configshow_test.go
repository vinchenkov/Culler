package verbs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCSConfig(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("MC_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func csKnob(t *testing.T, res any, key string) map[string]any {
	t.Helper()
	for _, family := range res.(map[string]any)["families"].([]map[string]any) {
		for _, knob := range family["knobs"].([]map[string]any) {
			if knob["key"] == key {
				return knob
			}
		}
	}
	t.Fatalf("knob %q not in output", key)
	return nil
}

func TestConfigShowReportsDefaultLayerForUnsetKnobs(t *testing.T) {
	writeCSConfig(t, "config_version = 2\n")
	res, err := ConfigShow()
	if err != nil {
		t.Fatal(err)
	}
	knob := csKnob(t, res, "budgets.review_wip_cap")
	if knob["value"] != 3 || knob["layer"] != "default" {
		t.Fatalf("review_wip_cap = %#v, want value 3 layer default", knob)
	}
	for _, family := range res.(map[string]any)["families"].([]map[string]any) {
		for _, knob := range family["knobs"].([]map[string]any) {
			if knob["layer"] != "default" {
				t.Fatalf("knob %v reports layer %v on an all-default config", knob["key"], knob["layer"])
			}
		}
	}
}

func TestConfigShowReportsOperatorLayerForChangedKnobs(t *testing.T) {
	writeCSConfig(t, "config_version = 2\n\n[budgets]\nreview_wip_cap = 5\n")
	res, err := ConfigShow()
	if err != nil {
		t.Fatal(err)
	}
	set := csKnob(t, res, "budgets.review_wip_cap")
	if set["value"] != 5 || set["layer"] != "operator" {
		t.Fatalf("review_wip_cap = %#v, want value 5 layer operator", set)
	}

}

// Onboarding's Ensure materializes EVERY knob into config.toml, so the layer
// must be judged by value, not key presence — else every onboarded deployment
// reports every knob as operator-set and the tag distinguishes nothing.
func TestConfigShowJudgesLayersByValueOnAMaterializedConfig(t *testing.T) {
	writeCSConfig(t, "config_version = 2\n\n[timing]\ntimeout_minutes = 60\ngrace_minutes = 20\n")
	res, err := ConfigShow()
	if err != nil {
		t.Fatal(err)
	}
	if knob := csKnob(t, res, "timing.timeout_minutes"); knob["layer"] != "default" {
		t.Fatalf("explicitly-written default value reports %v, want default", knob["layer"])
	}
	if knob := csKnob(t, res, "timing.grace_minutes"); knob["layer"] != "operator" || knob["value"] != 20 {
		t.Fatalf("changed value = %#v, want 20/operator", knob)
	}
}

// TestConfigShowNeverEmitsSecretValues is the PAGES.md-mandated proof that a
// secret-shaped value cannot appear in this verb's output: the whole result is
// serialized and searched for canaries.
func TestConfigShowNeverEmitsSecretValues(t *testing.T) {
	const (
		dashCanary = "canary-dash-7f3q-token"
		botCanary  = "canary-bot-9zk2-token"
	)
	writeCSConfig(t, `config_version = 2

[surfaces.dashboard]
auth_token = "`+dashCanary+`"

[surfaces.discord]
enabled = true
bot_token = "`+botCanary+`"
guild_id = "123456789012345678"
channel_id = "234567890123456789"
operator_ids = ["345678901234567890"]
`)
	res, err := ConfigShow()
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{dashCanary, botCanary} {
		if strings.Contains(string(body), canary) {
			t.Fatalf("secret value %q leaked into config show output", canary)
		}
	}
	for _, family := range res.(map[string]any)["families"].([]map[string]any) {
		for _, knob := range family["knobs"].([]map[string]any) {
			if strings.HasPrefix(knob["key"].(string), "surfaces.") {
				t.Fatal("A surface setting reached the dashboard editor")
			}
		}
	}
}

func TestConfigShowRefusesAMissingConfig(t *testing.T) {
	t.Setenv("MC_HOME", t.TempDir())
	_, err := ConfigShow()
	if err == nil {
		t.Fatal("missing config.toml did not refuse")
	}
	if _, ok := err.(*DomainError); !ok {
		t.Fatalf("missing config error = %T %v, want DomainError", err, err)
	}
}

func TestConfigShowExposesOnlySupportedEditors(t *testing.T) {
	writeCSConfig(t, "config_version = 4\n")
	res, err := ConfigShow()
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, family := range res.(map[string]any)["families"].([]map[string]any) {
		for _, knob := range family["knobs"].([]map[string]any) {
			key := knob["key"].(string)
			keys[key] = true
			if knob["type"] == nil || knob["label"] == "" || knob["description"] == "" {
				t.Fatalf("Incomplete editor: %v", knob)
			}
		}
	}
	want := []string{"development.capture_evaluation_cases", "budgets.review_wip_cap", "timing.timeout_minutes", "timing.grace_minutes", "timing.heartbeat_interval_s", "timing.spawn_grace_s", "timing.hard_deadline_minutes", "timing.collie_idle_timeout_s", "timing.wiki_update_interval_s", "timing.backup_retain", "container.additional_blocked_patterns", "container.cpu_limit", "container.memory_limit_mb", "runtimes.forbidden_env"}
	if len(keys) != len(want) {
		t.Fatalf("Unexpected settings: %v", keys)
	}
	for _, key := range want {
		if !keys[key] {
			t.Errorf("Missing editor: %s", key)
		}
	}
}
