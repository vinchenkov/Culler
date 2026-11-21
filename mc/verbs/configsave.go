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
	minimum, maximum                      int
	value                                 func(*operatorconfig.Config) any
}

var dashboardSettings = []dashboardSetting{
	{"budgets.review_wip_cap", "Review capacity", "Maximum review slots per Project. Approved tasks retain their slots until archive. The next dispatch uses this value.", "integer", "review_wip_cap", 1, 1000000, func(c *operatorconfig.Config) any { return &c.Budgets.ReviewWIPCap }},
	{"timing.timeout_minutes", "Lease timeout (minutes)", "Time without a heartbeat before a lease expires. The next lease check uses this value.", "integer", "timeout_minutes", 1, 525600, func(c *operatorconfig.Config) any { return &c.Timing.TimeoutMinutes }},
	{"timing.grace_minutes", "Lease grace (minutes)", "Additional time before Culler reclaims an expired lease. The next lease check uses this value.", "integer", "grace_minutes", 1, 525600, func(c *operatorconfig.Config) any { return &c.Timing.GraceMinutes }},
	{"timing.heartbeat_interval_s", "Heartbeat interval (seconds)", "New runs use this interval between heartbeats.", "integer", "heartbeat_interval_s", 1, 2147483, func(c *operatorconfig.Config) any { return &c.Timing.HeartbeatIntervalS }},
	{"timing.spawn_grace_s", "First heartbeat grace (seconds)", "Time for a new run to send its first heartbeat. Scribe receives at least 60 seconds.", "integer", "spawn_grace_s", 1, 31536000, func(c *operatorconfig.Config) any { return &c.Timing.SpawnGraceS }},
	{"timing.hard_deadline_minutes", "Run deadline (minutes)", "Maximum duration for new runs. Current runs retain their deadlines.", "integer", "hard_deadline_minutes", 1, 525600, func(c *operatorconfig.Config) any { return &c.Timing.HardDeadlineMinutes }},
	{"timing.collie_idle_timeout_s", "Collie idle timeout (seconds)", "Time before an idle Collie session expires. The next session check uses this value.", "integer", "collie_idle_timeout_s", 1, 31536000, func(c *operatorconfig.Config) any { return &c.Timing.CollieIdleTimeoutS }},
	{"timing.wiki_update_interval_s", "Wiki update interval (seconds)", "The minimum time between the starts of two wiki updates of one Project. The next wiki update check uses this value.", "integer", "wiki_update_interval_s", 1, 31536000, func(c *operatorconfig.Config) any { return &c.Timing.WikiUpdateIntervalS }},
	{"timing.backup_retain", "Backup retention", "Number of backups to retain. The next backup uses this value.", "integer", "", 1, 1000000, func(c *operatorconfig.Config) any { return &c.Timing.BackupRetain }},
	{"container.additional_blocked_patterns", "Additional blocked paths", "New runs reject paths that match these rules. Built-in rules always apply.", "patterns", "", 0, 0, func(c *operatorconfig.Config) any { return &c.Container.AdditionalBlockedPatterns }},
	{"container.cpu_limit", "Agent container CPU limit", "Maximum CPUs for each agent container. 0 means no limit. New agent containers use this value.", "integer", "", 0, operatorconfig.MaxContainerCPULimit, func(c *operatorconfig.Config) any { return &c.Container.CPULimit }},
	{"container.memory_limit_mb", "Agent container memory limit (MB)", "Maximum memory in megabytes for each agent container, without additional swap. 0 means no limit. New agent containers use this value.", "integer", "", 0, operatorconfig.MaxContainerMemoryLimitMB, func(c *operatorconfig.Config) any { return &c.Container.MemoryLimitMB }},
	{"container.trust_tier", "Agent container trust tier", "Which resource table new agent containers run under: t0 keeps your own limits, t1 applies the audited-external table, t2 applies the untrusted table and requires every t2 value to be set.", "tier", "", 0, 0, func(c *operatorconfig.Config) any { return &c.Container.TrustTier }},
	{"container.fd_soft", "Container file-descriptor soft limit", "Maximum open files for each new agent container before it must raise the limit itself.", "integer", "", 1, operatorconfig.MaxContainerFDLimit, func(c *operatorconfig.Config) any { return &c.Container.FDSoft }},
	{"container.fd_hard", "Container file-descriptor hard limit", "Ceiling for the soft limit. Must not be below container.fd_soft.", "integer", "", 1, operatorconfig.MaxContainerFDLimit, func(c *operatorconfig.Config) any { return &c.Container.FDHard }},
	{"container.log_max_mb", "Container log file cap (MB)", "Each container log file is capped at this size and rotated; the cap bounds the host-side copy of stdout.", "integer", "", 1, operatorconfig.MaxContainerMB, func(c *operatorconfig.Config) any { return &c.Container.LogMaxMB }},
	{"container.log_keep_files", "Container log rotation count", "How many capped log files the container runtime keeps per container.", "integer", "", 1, 100, func(c *operatorconfig.Config) any { return &c.Container.LogKeepFiles }},
	{"container.run_tree_max_mb", "Run history byte budget (MB)", "Total budget for retained per-run log trees; the oldest entries are pruned with a record when the budget is exceeded.", "integer", "", 1, operatorconfig.MaxContainerMB, func(c *operatorconfig.Config) any { return &c.Container.RunTreeMaxMB }},
	{"container.t1_cpu_limit", "T1 CPU limit", "Maximum CPUs for each new t1 agent container. The t1 table has no unlimited form.", "integer", "", 1, operatorconfig.MaxContainerCPULimit, func(c *operatorconfig.Config) any { return &c.Container.T1CPULimit }},
	{"container.t1_memory_limit_mb", "T1 memory limit (MB)", "Maximum memory without additional swap for each new t1 agent container.", "integer", "", 1, operatorconfig.MaxContainerMB, func(c *operatorconfig.Config) any { return &c.Container.T1MemoryLimitMB }},
	{"container.t1_tmpfs_mb", "T1 temporary mount cap (MB)", "Size cap for the container temporary filesystems (scratch and home) under t1.", "integer", "", 1, operatorconfig.MaxContainerMB, func(c *operatorconfig.Config) any { return &c.Container.T1TmpfsMB }},
	{"container.t1_workspace_soft_mb", "T1 workspace soft limit (MB)", "A t1 run whose workspace grows past this is ended with a resource outcome before it can crowd the host.", "integer", "", 1, operatorconfig.MaxContainerMB, func(c *operatorconfig.Config) any { return &c.Container.T1WorkspaceSoftMB }},
	{"container.t2_cpu_limit", "T2 CPU limit", "Maximum CPUs for each new t2 (untrusted) agent container. Required while the tier is t2.", "integer", "", 0, operatorconfig.MaxContainerCPULimit, func(c *operatorconfig.Config) any { return &c.Container.T2CPULimit }},
	{"container.t2_memory_limit_mb", "T2 memory limit (MB)", "Maximum memory without additional swap for each new t2 agent container. Required while the tier is t2.", "integer", "", 0, operatorconfig.MaxContainerMB, func(c *operatorconfig.Config) any { return &c.Container.T2MemoryLimitMB }},
	{"container.t2_tmpfs_mb", "T2 temporary mount cap (MB)", "Size cap for the container temporary filesystems under t2. Required while the tier is t2.", "integer", "", 0, operatorconfig.MaxContainerMB, func(c *operatorconfig.Config) any { return &c.Container.T2TmpfsMB }},
	{"container.t2_workspace_soft_mb", "T2 workspace soft limit (MB)", "A t2 run whose workspace grows past this is ended with a resource outcome. Required while the tier is t2.", "integer", "", 0, operatorconfig.MaxContainerMB, func(c *operatorconfig.Config) any { return &c.Container.T2WorkspaceSoftMB }},
	{"runtimes.forbidden_env", "Additional forbidden environment variables", "Enter one variable name per line. New runs reject these variables. Built-in restrictions always apply.", "lines", "", 0, 0, func(c *operatorconfig.Config) any { return &c.Runtimes.ForbiddenEnv }},
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
		if value, ok := target.(*int); ok && (*value < setting.minimum || *value > setting.maximum) {
			return false, Usagef("The value for %s must be an integer from %d to %d.", setting.key, setting.minimum, setting.maximum)
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
