package verbs

import (
	"encoding/json"
	"strings"

	operatorconfig "mc/config"
)

// ConfigShow exposes only settings with a supported dashboard edit path.
func ConfigShow() (any, error) {
	cfg, _, err := loadOperatorConfig()
	if err != nil {
		return nil, err
	}
	return configView(cfg), nil
}

func configView(cfg operatorconfig.Config) map[string]any {
	defaults := operatorconfig.Default()
	families := []map[string]any{}
	for _, setting := range dashboardSettings {
		family, _, _ := strings.Cut(setting.key, ".")
		if len(families) == 0 || families[len(families)-1]["family"] != family {
			families = append(families, map[string]any{"family": family, "knobs": []map[string]any{}})
		}
		layer := "default"
		if !sameKnobValue(setting.value(&cfg), setting.value(&defaults)) {
			layer = "operator"
		}
		row := map[string]any{
			"key": setting.key, "value": settingValue(setting, &cfg), "layer": layer,
			"label": setting.label, "description": setting.description, "type": setting.kind,
		}
		if setting.kind == "integer" {
			row["min"] = setting.minimum
			row["max"] = setting.maximum
		}
		if setting.kind == "lines" {
			row["required_values"] = defaults.Runtimes.ForbiddenEnv
		}
		if setting.kind == "patterns" {
			row["options"] = []string{"component", "basename-glob"}
		}
		last := families[len(families)-1]
		last["knobs"] = append(last["knobs"].([]map[string]any), row)
	}
	return map[string]any{"config_version": cfg.ConfigVersion, "families": families}
}

func sameKnobValue(a, b any) bool {
	aj, aerr := json.Marshal(a)
	bj, berr := json.Marshal(b)
	return aerr == nil && berr == nil && string(aj) == string(bj)
}

func settingValue(setting dashboardSetting, cfg *operatorconfig.Config) any {
	switch value := setting.value(cfg).(type) {
	case *bool:
		return *value
	case *int:
		return *value
	case *[]string:
		return *value
	case *[]operatorconfig.BlockedPattern:
		return *value
	}
	panic("Unsupported dashboard setting type")
}

func EvaluationCaptureEnabled() (bool, error) {
	cfg, _, err := loadOperatorConfig()
	return cfg.Development.CaptureEvaluationCases, err
}
