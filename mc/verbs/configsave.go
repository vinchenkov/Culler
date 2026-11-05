package verbs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	operatorconfig "mc/config"
)

type dashboardSetting struct {
	key, label, description, kind, column string
	maximum                               int
	value                                 func(*operatorconfig.Config) any
}

var dashboardSettings = []dashboardSetting{
	{"development.capture_evaluation_cases", "Capture evaluation cases", "Preserve subsequent Packager inputs for private evaluation cases. This development feature takes effect at once.", "boolean", "", 0, func(c *operatorconfig.Config) any { return &c.Development.CaptureEvaluationCases }},
	{"budgets.review_wip_cap", "Review capacity", "Maximum review slots per Project. Approved tasks retain their slots until archive. The next dispatch uses this value.", "integer", "review_wip_cap", 1000000, func(c *operatorconfig.Config) any { return &c.Budgets.ReviewWIPCap }},
	{"timing.timeout_minutes", "Lease timeout (minutes)", "Time without a heartbeat before a lease expires. The next lease check uses this value.", "integer", "timeout_minutes", 525600, func(c *operatorconfig.Config) any { return &c.Timing.TimeoutMinutes }},
	{"timing.grace_minutes", "Lease grace (minutes)", "Additional time before Culler reclaims an expired lease. The next lease check uses this value.", "integer", "grace_minutes", 525600, func(c *operatorconfig.Config) any { return &c.Timing.GraceMinutes }},
	{"timing.heartbeat_interval_s", "Heartbeat interval (seconds)", "New runs use this interval between heartbeats.", "integer", "heartbeat_interval_s", 2147483, func(c *operatorconfig.Config) any { return &c.Timing.HeartbeatIntervalS }},
	{"timing.spawn_grace_s", "First heartbeat grace (seconds)", "Time for a new run to send its first heartbeat. Scribe receives at least 60 seconds.", "integer", "spawn_grace_s", 31536000, func(c *operatorconfig.Config) any { return &c.Timing.SpawnGraceS }},
	{"timing.hard_deadline_minutes", "Run deadline (minutes)", "Maximum duration for new runs. Current runs retain their deadlines.", "integer", "hard_deadline_minutes", 525600, func(c *operatorconfig.Config) any { return &c.Timing.HardDeadlineMinutes }},
	{"timing.homie_idle_timeout_s", "Homie idle timeout (seconds)", "Time before an idle Homie session expires. The next session check uses this value.", "integer", "homie_idle_timeout_s", 31536000, func(c *operatorconfig.Config) any { return &c.Timing.HomieIdleTimeoutS }},
	{"timing.wiki_update_interval_s", "Wiki update interval (seconds)", "The minimum time between the starts of two wiki updates of one Project. The next wiki update check uses this value.", "integer", "wiki_update_interval_s", 31536000, func(c *operatorconfig.Config) any { return &c.Timing.WikiUpdateIntervalS }},
	{"timing.backup_retain", "Backup retention", "Number of backups to retain. The next backup uses this value.", "integer", "", 1000000, func(c *operatorconfig.Config) any { return &c.Timing.BackupRetain }},
	{"container.additional_blocked_patterns", "Additional blocked paths", "New runs reject paths that match these rules. Built-in rules always apply.", "patterns", "", 0, func(c *operatorconfig.Config) any { return &c.Container.AdditionalBlockedPatterns }},
	{"runtimes.forbidden_env", "Additional forbidden environment variables", "Enter one variable name per line. New runs reject these variables. Built-in restrictions always apply.", "lines", "", 0, func(c *operatorconfig.Config) any { return &c.Runtimes.ForbiddenEnv }},
}

type ConfigChange struct {
	Key      string          `json:"key"`
	Value    json.RawMessage `json:"value"`
	Expected json.RawMessage `json:"expected"`
}

type ConfigSaveRequest struct {
	Changes []ConfigChange `json:"changes"`
}

func applyConfigChanges(cfg *operatorconfig.Config, request ConfigSaveRequest) (bool, error) {
	if len(request.Changes) == 0 || len(request.Changes) > len(dashboardSettings) {
		return false, Usagef("Select at least one supported setting.")
	}
	seen := map[string]bool{}
	project := false
	for _, change := range request.Changes {
		if seen[change.Key] {
			return false, Usagef("Each setting must appear once.")
		}
		seen[change.Key] = true
		var setting *dashboardSetting
		for i := range dashboardSettings {
			if dashboardSettings[i].key == change.Key {
				setting = &dashboardSettings[i]
				break
			}
		}
		if setting == nil {
			return false, Usagef("This setting has no dashboard editor.")
		}
		var expected, desired any
		if json.Unmarshal(change.Expected, &expected) != nil || json.Unmarshal(change.Value, &desired) != nil || expected == nil || desired == nil {
			return false, Usagef("Each setting requires a value and its previous value.")
		}
		target := setting.value(cfg)
		if !sameKnobValue(target, expected) && !sameKnobValue(target, desired) {
			return false, Domainf("The setting %s changed elsewhere. Reload the page before you save.", setting.key)
		}
		decoder := json.NewDecoder(bytes.NewReader(change.Value))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(target); err != nil {
			return false, Usagef("The value for %s has the wrong type.", setting.key)
		}
		if value, ok := target.(*int); ok && (*value < 1 || *value > setting.maximum) {
			return false, Usagef("The value for %s must be an integer from 1 to %d.", setting.key, setting.maximum)
		}
		project = project || setting.column != ""
	}
	if err := cfg.Validate(); err != nil {
		return false, Usagef("The configuration is invalid: %v", err)
	}
	return project, nil
}

// ConfigSave preserves unrelated values and serializes concurrent dashboard saves.
func ConfigSave(request ConfigSaveRequest, apply func(map[string]int) error) (any, error) {
	_, path, err := loadOperatorConfig()
	if err != nil {
		return nil, err
	}
	var saved operatorconfig.Config
	var project bool
	_, err = operatorconfig.UpdateAtomicAndApply(path, func(cfg *operatorconfig.Config) error {
		var err error
		project, err = applyConfigChanges(cfg, request)
		return err
	}, func(cfg operatorconfig.Config) error {
		saved = cfg
		if !project {
			return nil
		}
		values := map[string]int{}
		for _, setting := range dashboardSettings {
			if setting.column != "" {
				values[setting.column] = *setting.value(&cfg).(*int)
			}
		}
		if err := apply(values); err != nil {
			return Domainf("Culler saved the values, but activation is unverified. Save again to retry. %v", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return configView(saved), nil
}

func ConfigApplySettings(db *sql.DB, values map[string]int) (any, error) {
	if len(values) != 8 {
		return nil, Usagef("The settings projection is incomplete.")
	}
	for _, setting := range dashboardSettings {
		if setting.column == "" {
			continue
		}
		value, ok := values[setting.column]
		if !ok || value < 1 || value > setting.maximum {
			return nil, Usagef("The settings projection is invalid.")
		}
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		for _, setting := range dashboardSettings {
			if setting.column == "" {
				continue
			}
			result, err := q.ExecContext(ctx, fmt.Sprintf("UPDATE settings SET %s = ? WHERE id = 1", setting.column), values[setting.column])
			if err != nil {
				return err
			}
			count, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if count != 1 {
				return Domainf("The deployment settings are unavailable.")
			}
		}
		return nil
	})
	return map[string]any{"applied": err == nil}, err
}
