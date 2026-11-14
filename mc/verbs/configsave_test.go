package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	operatorconfig "mc/config"
	"mc/substrate"
)

func configChange(key string, expected, value any) ConfigSaveRequest {
	before, _ := json.Marshal(expected)
	after, _ := json.Marshal(value)
	return ConfigSaveRequest{Changes: []ConfigChange{{Key: key, Expected: before, Value: after}}}
}

func TestConfigSavePersistsEverySupportedSetting(t *testing.T) {
	cases := []struct {
		key   string
		value any
	}{
		{"budgets.review_wip_cap", 5},
		{"timing.timeout_minutes", 70},
		{"timing.grace_minutes", 20},
		{"timing.heartbeat_interval_s", 30},
		{"timing.spawn_grace_s", 90},
		{"timing.hard_deadline_minutes", 300},
		{"timing.collie_idle_timeout_s", 3600},
		{"timing.wiki_update_interval_s", 7200},
		{"timing.backup_retain", 8},
		{"container.cpu_limit", 4},
		{"container.memory_limit_mb", 8192},
		{"container.additional_blocked_patterns", []operatorconfig.BlockedPattern{{Kind: "component", Pattern: "private"}, {Kind: "basename-glob", Pattern: "*.secret"}}},
		{"runtimes.forbidden_env", append(operatorconfig.Default().Runtimes.ForbiddenEnv, "PRIVATE_TOKEN")},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			writeCSConfig(t, "config_version = 4\n[surfaces.dashboard]\nauth_token = \"preserved-secret\"\n")
			before, _ := ConfigShow()
			applied := false
			result, err := ConfigSave(configChange(tc.key, csKnob(t, before, tc.key)["value"], tc.value), func(values map[string]int) error {
				applied = true
				if len(values) != 8 {
					t.Fatal("The state projection is incomplete")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !sameKnobValue(csKnob(t, result, tc.key)["value"], tc.value) {
				t.Fatalf("The saved value differs: %v", result)
			}
			cfg, err := operatorconfig.Load(filepath.Join(os.Getenv("MC_HOME"), "config.toml"))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Surfaces.Dashboard.AuthToken != "preserved-secret" {
				t.Fatal("The save changed an unrelated secret")
			}
			show, err := ConfigShow()
			if err != nil || !sameKnobValue(csKnob(t, show, tc.key)["value"], tc.value) {
				t.Fatalf("The value did not persist: %v", err)
			}
			expectedApply := tc.key != "timing.backup_retain" && tc.key != "runtimes.forbidden_env" && !strings.HasPrefix(tc.key, "container.")
			if applied != expectedApply {
				t.Fatalf("State update = %v", applied)
			}
		})
	}
}

func TestConfigSaveRejectsInvalidAndStaleEditsWithoutWrites(t *testing.T) {
	cases := []ConfigSaveRequest{
		configChange("container.runtime", "docker", "other"),
		configChange("git.author_name", "Culler", "Other"),
		configChange("runtimes.bindings", []string{}, []string{}),
		configChange("timing.backup_retain", 12, 0),
		configChange("container.cpu_limit", 0, -1),
		configChange("container.cpu_limit", 0, 1025),
		configChange("container.cpu_limit", 0, 1.5),
		configChange("container.memory_limit_mb", 0, -1),
		configChange("container.memory_limit_mb", 0, 1048577),
		configChange("timing.backup_retain", 12, 1.5),
		configChange("timing.backup_retain", 12, "8"),
		configChange("timing.backup_retain", 12, nil),
		configChange("timing.timeout_minutes", 60, 525601),
		configChange("timing.heartbeat_interval_s", 60, 2147484),
		configChange("timing.backup_retain", 10, 8),
		configChange("container.additional_blocked_patterns", []string{}, []map[string]string{{"kind": "unsupported", "pattern": "private"}}),
		configChange("runtimes.forbidden_env", operatorconfig.Default().Runtimes.ForbiddenEnv, []string{"TOKEN", "TOKEN"}),
	}
	for i, request := range cases {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			writeCSConfig(t, "config_version = 4\n")
			path := filepath.Join(os.Getenv("MC_HOME"), "config.toml")
			before, _ := os.ReadFile(path)
			_, err := ConfigSave(request, func(map[string]int) error { t.Fatal("An invalid edit reached the spine"); return nil })
			if err == nil {
				t.Fatal("An invalid edit passed")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("An invalid edit changed the file")
			}
		})
	}
}

func TestConfigSaveRemovesAgentContainerLimitsWithZero(t *testing.T) {
	writeCSConfig(t, "config_version = 4\n[container]\ncpu_limit = 4\nmemory_limit_mb = 8192\n")
	for _, change := range []ConfigSaveRequest{configChange("container.cpu_limit", 4, 0), configChange("container.memory_limit_mb", 8192, 0)} {
		if _, err := ConfigSave(change, func(map[string]int) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := operatorconfig.Load(filepath.Join(os.Getenv("MC_HOME"), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Container.CPULimit != 0 || cfg.Container.MemoryLimitMB != 0 {
		t.Fatalf("The limits remain: %+v", cfg.Container)
	}
	show, _ := ConfigShow()
	if knob := csKnob(t, show, "container.cpu_limit"); knob["min"] != 0 || knob["max"] != operatorconfig.MaxContainerCPULimit {
		t.Fatalf("The CPU limit bounds differ: %v", knob)
	}
}

func TestConfigSaveRetriesFailedActivation(t *testing.T) {
	writeCSConfig(t, "config_version = 4\n")
	request := configChange("timing.timeout_minutes", 60, 75)
	_, err := ConfigSave(request, func(map[string]int) error { return errors.New("unavailable") })
	if err == nil {
		t.Fatal("A failed activation reported success")
	}
	_, err = ConfigSave(request, func(values map[string]int) error {
		if values["timeout_minutes"] != 75 {
			t.Fatal("The retry lost the value")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestConfigSaveBatchRejectsAllChangesWhenOneIsInvalid(t *testing.T) {
	writeCSConfig(t, "config_version = 4\n")
	request := configChange("timing.backup_retain", 12, 8)
	request.Changes = append(request.Changes, configChange("container.runtime", "docker", "other").Changes...)
	if _, err := ConfigSave(request, nil); err == nil {
		t.Fatal("The invalid batch passed")
	}
	cfg, _, _ := loadOperatorConfig()
	if cfg.Timing.BackupRetain != 12 {
		t.Fatal("The invalid batch partially changed the file")
	}
}

func TestConfigSaveAppliesSettingsToDispatch(t *testing.T) {
	db := dvSpine(t)
	request := configChange("budgets.review_wip_cap", 3, 8)
	request.Changes = append(request.Changes, configChange("timing.timeout_minutes", 60, 90).Changes...)
	_, err := ConfigSave(request, func(values map[string]int) error {
		_, err := ConfigApplySettings(db, values)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	tun, err := loadSettings(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if tun.reviewWIPCap != 8 || tun.timeoutMinutes != 90 {
		t.Fatalf("Dispatch read stale settings: %+v", tun)
	}
	if _, err := ConfigApplySettings(db, map[string]int{"review_wip_cap": 2}); err == nil {
		t.Fatal("An incomplete projection passed")
	}
	tun, _ = loadSettings(context.Background(), db)
	if tun.reviewWIPCap != 8 {
		t.Fatal("The rejected projection changed dispatch")
	}
}

func TestEvaluationFlagUsesCanonicalConfigWithoutSpineProjection(t *testing.T) {
	writeCSConfig(t, "config_version = 4\n")
	apply := func(map[string]int) error {
		t.Fatal("The development flag requires no dispatch projection.")
		return nil
	}
	for _, enabled := range []bool{true, false} {
		if _, err := ConfigSave(configChange("development.capture_evaluation_cases", !enabled, enabled), apply); err != nil {
			t.Fatal(err)
		}
		actual, err := EvaluationCaptureEnabled()
		if err != nil || actual != enabled {
			t.Fatalf("The current flag differs: %v %v", actual, err)
		}
	}
}

func TestWikiUpdateIntervalSetting(t *testing.T) {
	const key = "timing.wiki_update_interval_s"
	_, spine := provisionOnboardStateFixture(t)
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	stored := func() int {
		t.Helper()
		var interval int
		if err := db.QueryRowContext(ctx, `SELECT wiki_update_interval_s FROM settings WHERE id = 1`).Scan(&interval); err != nil {
			t.Fatal(err)
		}
		return interval
	}
	apply := func(values map[string]int) error {
		_, err := ConfigApplySettings(db, values)
		return err
	}

	show, err := ConfigShow()
	if err != nil {
		t.Fatal(err)
	}
	knob := csKnob(t, show, key)
	if knob["value"] != 3600 || knob["layer"] != "default" || knob["type"] != "integer" ||
		knob["min"] != 1 || knob["max"] != 31536000 {
		t.Fatalf("editor = %#v, want the default 3600 and the bounds 1 and 31536000", knob)
	}
	if got := stored(); got != 3600 {
		t.Fatalf("settings row of a new database = %d, want 3600", got)
	}

	for _, value := range []int{0, 31536001} {
		if _, err := ConfigSave(configChange(key, 3600, value), apply); err == nil {
			t.Fatalf("a save of %d passed", value)
		}
	}
	if cfg, _, err := loadOperatorConfig(); err != nil || cfg.Timing.WikiUpdateIntervalS != 3600 || stored() != 3600 {
		t.Fatalf("a refused save changed the value: configuration %d, settings row %d, error %v",
			cfg.Timing.WikiUpdateIntervalS, stored(), err)
	}

	saved, err := ConfigSave(configChange(key, 3600, 31536000), apply)
	if err != nil {
		t.Fatal(err)
	}
	if knob := csKnob(t, saved, key); knob["value"] != 31536000 || knob["layer"] != "operator" {
		t.Fatalf("saved editor = %#v, want 31536000 from the operator", knob)
	}
	tun, err := loadSettings(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if tun.wikiUpdateIntervalS != 31536000 || stored() != 31536000 {
		t.Fatalf("the save copied %d into the settings row and dispatch reads %d, want 31536000",
			stored(), tun.wikiUpdateIntervalS)
	}
	read, err := SettingsGet(db)
	if err != nil {
		t.Fatal(err)
	}
	if got := read.(map[string]any)["wiki_update_interval_s"]; got != int64(31536000) {
		t.Fatalf("settings read = %v, want 31536000", got)
	}

	// The onboarding projection copies the value of the desired configuration.
	if _, err := updateOperatorConfig(func(cfg *operatorconfig.Config) error {
		cfg.Timing.WikiUpdateIntervalS = 900
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	plan, err := PrepareOnboardTunablesHost(OnboardArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if status, _, err := onboardTunablesDB(plan.Project(OnboardArgs{Spine: spine})); err != nil || status != "done" {
		t.Fatalf("projection = status %q, error %v", status, err)
	}
	if got := stored(); got != 900 {
		t.Fatalf("settings row after the projection = %d, want 900", got)
	}

	// The accepted defaults reset the value in the configuration and in the row.
	plan, err = PrepareOnboardTunablesHost(OnboardArgs{TunablesConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Desired.Timing.WikiUpdateIntervalS != 3600 {
		t.Fatalf("accepted default = %d, want 3600", plan.Desired.Timing.WikiUpdateIntervalS)
	}
	if status, _, err := onboardTunablesDB(plan.Project(OnboardArgs{Spine: spine})); err != nil || status != "done" {
		t.Fatalf("default projection = status %q, error %v", status, err)
	}
	if got := stored(); got != 3600 {
		t.Fatalf("settings row after the accepted defaults = %d, want 3600", got)
	}

	// The projection refuses a request with no value.
	if _, _, err := onboardTunablesDB(OnboardArgs{
		Spine: spine, ReviewWIPCap: 3, TimeoutMinutes: 60, GraceMinutes: 15, HeartbeatIntervalS: 60,
		SpawnGraceS: 60, HardDeadlineMinutes: 240, CollieIdleTimeoutS: 1800,
	}); err == nil {
		t.Fatal("a projection with no wiki update interval passed")
	}
}
