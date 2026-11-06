// Package config owns the operator configuration file. The file is the
// machine-global policy authority. A consumer can project values into a
// narrower runtime record, but no second operator-config source can override
// it.
package config

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/BurntSushi/toml"

	"mc/boundary"
)

const (
	CurrentVersion = 4
	maxConfigBytes = 64 * 1024
)

const (
	CredentialKindCodexOAuth     = "codex-oauth"
	CredentialKindClaudeOAuth    = "claude-oauth"
	CredentialKindCodexAPIKey    = "codex-api-key"
	CredentialKindClaudeAPIKey   = "claude-api-key"
	CredentialKindStaticEnv      = "static-env"
	CredentialKindNone           = "none"
	CredentialKindOpenCodeAPIKey = "opencode-api-key"
)

//go:embed config.example.toml
var exampleBody []byte

type Development struct {
	CaptureEvaluationCases bool `toml:"capture_evaluation_cases"`
}

type Config struct {
	Development   Development `toml:"development"`
	ConfigVersion int         `toml:"config_version"`
	Runtimes      Runtimes    `toml:"runtimes"`
	Budgets       Budgets     `toml:"budgets"`
	Timing        Timing      `toml:"timing"`
	Container     Container   `toml:"container"`
	Surfaces      Surfaces    `toml:"surfaces"`
	Git           Git         `toml:"git"`
	migrated      bool
}

type Runtimes struct {
	EnabledBindings []string                    `toml:"enabled_bindings"`
	ForbiddenEnv    []string                    `toml:"forbidden_env"`
	Credentials     map[string]CredentialSource `toml:"credentials"`
	Bindings        map[string]RuntimeBinding   `toml:"bindings"`
}

// CredentialSource selects one supported provider connection.
// The host credential store holds its grant under the credential source identifier.
type CredentialSource struct {
	CatalogEntry string `toml:"catalog_entry,omitempty"`
	InferenceURL string `toml:"inference_url,omitempty"`
	Name         string `toml:"name,omitempty"`
	BaseURL      string `toml:"base_url,omitempty"`
	Kind         string `toml:"kind"`
	TokenURL     string `toml:"token_url,omitempty"`
	ClientID     string `toml:"client_id,omitempty"`
}

// Delivery selects the host proxy for a supported connection.
func (s CredentialSource) Delivery() string {
	switch s.Kind {
	case CredentialKindCodexOAuth, CredentialKindClaudeOAuth, CredentialKindCodexAPIKey, CredentialKindClaudeAPIKey, CredentialKindOpenCodeAPIKey:
		return "proxy"
	}
	return ""
}

// Channel is the credential channel the resident and the adapters read.
func (s CredentialSource) Channel() string {
	switch s.Kind {
	case CredentialKindOpenCodeAPIKey:
		return "opencode"
	case CredentialKindCodexOAuth, CredentialKindCodexAPIKey:
		return "codex"
	case CredentialKindClaudeOAuth, CredentialKindClaudeAPIKey:
		return "claude"
	}
	return ""
}

// Harness is the one harness family a binding on this kind can use.
func (s CredentialSource) Harness() string {
	switch s.Kind {
	case CredentialKindOpenCodeAPIKey:
		return "opencode"
	case CredentialKindCodexOAuth, CredentialKindCodexAPIKey:
		return "codex"
	case CredentialKindClaudeOAuth, CredentialKindClaudeAPIKey:
		return "claude-sdk"
	}
	return ""
}

// RuntimeBinding holds the agent profile configuration.
type RuntimeBinding struct {
	Name       string `toml:"name,omitempty"`
	Harness    string `toml:"harness"`
	Model      string `toml:"model"`
	Effort     string `toml:"effort,omitempty"`
	Credential string `toml:"credential"`
	BaseURL    string `toml:"base_url,omitempty"`
}

// CredentialOf resolves the credential source of one binding.
func (r Runtimes) CredentialOf(bindingID string) (CredentialSource, bool) {
	binding, ok := r.Bindings[bindingID]
	if !ok {
		return CredentialSource{}, false
	}
	source, ok := r.Credentials[binding.Credential]
	return source, ok
}

// legacyRuntimeBinding is the config_version 2 binding shape. Parse translates
// it into one credential source and one binding.
type legacyRuntimeBinding struct {
	Harness   string `toml:"harness"`
	Model     string `toml:"model"`
	BaseURL   string `toml:"base_url"`
	AuthKind  string `toml:"auth_kind"`
	Delivery  string `toml:"delivery"`
	TokenURL  string `toml:"token_url"`
	ClientID  string `toml:"client_id"`
	StaticEnv string `toml:"static_env"`
}

type canonicalRuntimeBinding struct {
	ID         string `json:"id"`
	Harness    string `json:"harness"`
	Model      string `json:"model"`
	BaseURL    string `json:"base_url"`
	Credential string `json:"credential"`
}

type canonicalCredentialSource struct {
	CatalogEntry string `json:"catalog_entry,omitempty"`
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	TokenURL     string `json:"token_url"`
	ClientID     string `json:"client_id"`
	InferenceURL string `json:"inference_url"`
}

type canonicalRuntimeCatalog struct {
	Version      int                         `json:"version"`
	Bindings     []canonicalRuntimeBinding   `json:"bindings"`
	Credentials  []canonicalCredentialSource `json:"credentials"`
	ForbiddenEnv []string                    `json:"forbidden_env"`
}

// RuntimeCatalogDigest binds the enabled non-secret runtime authority. An
// unrelated config section or a disabled binding draft does not change it.
func RuntimeCatalogDigest(runtimes Runtimes) (string, error) {
	forbidden := append([]string(nil), runtimes.ForbiddenEnv...)
	sort.Strings(forbidden)
	ids := append([]string(nil), runtimes.EnabledBindings...)
	sort.Strings(ids)
	bindings := make([]canonicalRuntimeBinding, 0, len(ids))
	referenced := map[string]bool{}
	for _, id := range ids {
		binding, ok := runtimes.Bindings[id]
		if !ok {
			return "", fmt.Errorf("enabled runtime binding %q is undefined", id)
		}
		if _, ok := runtimes.Credentials[binding.Credential]; !ok {
			return "", fmt.Errorf("enabled runtime binding %q references undefined credential %q", id, binding.Credential)
		}
		referenced[binding.Credential] = true
		bindings = append(bindings, canonicalRuntimeBinding{
			ID: id, Harness: binding.Harness, Model: binding.Model,
			BaseURL: binding.BaseURL, Credential: binding.Credential,
		})
	}
	credentialIDs := make([]string, 0, len(referenced))
	for id := range referenced {
		credentialIDs = append(credentialIDs, id)
	}
	sort.Strings(credentialIDs)
	credentials := make([]canonicalCredentialSource, 0, len(credentialIDs))
	for _, id := range credentialIDs {
		source := runtimes.Credentials[id]
		credentials = append(credentials, canonicalCredentialSource{
			ID: id, Kind: source.Kind, CatalogEntry: source.CatalogEntry, TokenURL: source.TokenURL, ClientID: source.ClientID, InferenceURL: source.InferenceURL,
		})
	}
	body, err := json.Marshal(canonicalRuntimeCatalog{
		Version: CurrentVersion, Bindings: bindings, Credentials: credentials,
		ForbiddenEnv: forbidden,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

type Budgets struct {
	ReviewWIPCap       int `toml:"review_wip_cap"`
	MaxCorrectionCount int `toml:"max_correction_count"`
	DispatchRetries    int `toml:"dispatch_retries"`
}

type Timing struct {
	TimeoutMinutes      int `toml:"timeout_minutes"`
	GraceMinutes        int `toml:"grace_minutes"`
	HeartbeatIntervalS  int `toml:"heartbeat_interval_s"`
	SpawnGraceS         int `toml:"spawn_grace_s"`
	HardDeadlineMinutes int `toml:"hard_deadline_minutes"`
	TickIntervalS       int `toml:"tick_interval_s"`
	CollieIdleTimeoutS  int `toml:"collie_idle_timeout_s"`
	WikiUpdateIntervalS int `toml:"wiki_update_interval_s"`
	BackupIntervalS     int `toml:"backup_interval_s"`
	BackupRetain        int `toml:"backup_retain"`
}

type Container struct {
	Runtime                   string           `toml:"runtime"`
	BaseImage                 string           `toml:"base_image"`
	PackageCacheRoot          string           `toml:"package_cache_root"`
	MountAllowlist            string           `toml:"mount_allowlist"`
	AdditionalBlockedPatterns []BlockedPattern `toml:"additional_blocked_patterns"`
}

// BlockedPattern is one operator-added deny. It is a table, not a bare
// string, because `kind` selects the pattern language: component or
// basename-glob.
type BlockedPattern struct {
	Kind    string `toml:"kind" json:"kind"`
	Pattern string `toml:"pattern" json:"pattern"`
}

// BoundaryPatterns projects the operator's additions into the boundary
// package's own type. Validation is DELEGATED to that package rather than
// restated here: config and the policy that enforces it must not be able to
// disagree about which additions are legal.
func (c Container) BoundaryPatterns() []boundary.BlockPattern {
	out := make([]boundary.BlockPattern, 0, len(c.AdditionalBlockedPatterns))
	for _, addition := range c.AdditionalBlockedPatterns {
		out = append(out, boundary.BlockPattern{
			Kind: boundary.PatternKind(addition.Kind), Pattern: addition.Pattern,
		})
	}
	return out
}

type Surfaces struct {
	Dashboard  Dashboard  `toml:"dashboard"`
	AgentsView AgentsView `toml:"agentsview"`
	Wiki       Wiki       `toml:"wiki"`
	Discord    Discord    `toml:"discord"`
	Alerts     Alerts     `toml:"alerts"`
}

type Dashboard struct {
	Bind      string `toml:"bind"`
	AuthToken string `toml:"auth_token"`
}

// AgentsView is the loopback address of the trace viewer.
type AgentsView struct {
	Bind string `toml:"bind"`
}

// Wiki is the loopback address where the dashboard process serves the wiki
// exports.
type Wiki struct {
	Bind string `toml:"bind"`
}

type Discord struct {
	Enabled     bool     `toml:"enabled"`
	BotToken    string   `toml:"bot_token"`
	GuildID     string   `toml:"guild_id"`
	ChannelID   string   `toml:"channel_id"`
	OperatorIDs []string `toml:"operator_ids"`
}

type Alerts struct {
	Blocked []string `toml:"blocked"`
	Health  []string `toml:"health"`
}

type Git struct {
	AuthorName  string `toml:"author_name"`
	AuthorEmail string `toml:"author_email"`
}

var referenceRuntimeCatalog = func() Runtimes {
	var reference struct {
		Runtimes Runtimes `toml:"runtimes"`
	}
	if _, err := toml.Decode(string(exampleBody), &reference); err != nil {
		panic("The embedded runtime configuration is invalid.")
	}
	return reference.Runtimes
}()

func defaultRuntimeCatalog() Runtimes {
	catalog := referenceRuntimeCatalog
	catalog.EnabledBindings = append([]string{}, catalog.EnabledBindings...)
	catalog.ForbiddenEnv = append([]string{}, catalog.ForbiddenEnv...)
	catalog.Credentials = make(map[string]CredentialSource, len(referenceRuntimeCatalog.Credentials))
	for id, source := range referenceRuntimeCatalog.Credentials {
		catalog.Credentials[id] = source
	}
	catalog.Bindings = make(map[string]RuntimeBinding, len(referenceRuntimeCatalog.Bindings))
	for id, profile := range referenceRuntimeCatalog.Bindings {
		catalog.Bindings[id] = profile
	}
	return catalog
}

func Default() Config {
	return Config{
		ConfigVersion: CurrentVersion,
		Runtimes:      defaultRuntimeCatalog(),
		Budgets: Budgets{
			ReviewWIPCap: 3, MaxCorrectionCount: 3,
			DispatchRetries: 3,
		},
		Timing: Timing{
			TimeoutMinutes: 60, GraceMinutes: 15, HeartbeatIntervalS: 60,
			SpawnGraceS: 60, HardDeadlineMinutes: 240, TickIntervalS: 60,
			CollieIdleTimeoutS: 1800, WikiUpdateIntervalS: 3600,
			BackupIntervalS: 3600, BackupRetain: 12,
		},
		Container: Container{
			Runtime: "docker", BaseImage: "mc-prod",
			PackageCacheRoot: "cache", MountAllowlist: "mount-allowlist",
			AdditionalBlockedPatterns: []BlockedPattern{},
		},
		Surfaces: Surfaces{
			Dashboard:  Dashboard{Bind: "127.0.0.1:7333"},
			AgentsView: AgentsView{Bind: "127.0.0.1:7334"},
			Wiki:       Wiki{Bind: "127.0.0.1:7335"},
			Discord:    Discord{OperatorIDs: []string{}},
			Alerts: Alerts{
				Blocked: []string{"dashboard"},
				Health:  []string{"dashboard"},
			},
		},
		Git: Git{AuthorName: "Culler", AuthorEmail: "mc@localhost"},
	}
}

func Parse(body []byte) (Config, error) {
	if len(body) > maxConfigBytes {
		return Config{}, fmt.Errorf("config.toml exceeds %d-byte limit", maxConfigBytes)
	}
	if !utf8.Valid(body) {
		return Config{}, fmt.Errorf("config.toml is not valid UTF-8")
	}
	var probe struct {
		ConfigVersion int `toml:"config_version"`
	}
	probeMeta, err := toml.Decode(string(body), &probe)
	if err != nil {
		return Config{}, fmt.Errorf("decode config.toml: %w", err)
	}
	sourceVersion := 1
	if probeMeta.IsDefined("config_version") {
		sourceVersion = probe.ConfigVersion
	}
	if sourceVersion < 1 || sourceVersion > CurrentVersion {
		return Config{}, fmt.Errorf("config_version %d is unsupported by this build (want 1..%d)", sourceVersion, CurrentVersion)
	}
	definesBindings := probeMeta.IsDefined("runtimes", "bindings")
	definesCredentials := probeMeta.IsDefined("runtimes", "credentials")
	if sourceVersion == 1 && (definesBindings || definesCredentials) {
		return Config{}, fmt.Errorf("config_version 1 must not contain runtimes.bindings or runtimes.credentials tables")
	}
	if sourceVersion == 2 && definesCredentials {
		return Config{}, fmt.Errorf("config_version 2 must not contain runtimes.credentials tables")
	}
	cfg := Default()
	// The credential table and the binding table form one catalog. A file
	// that defines either table replaces the whole shipped catalog, so an
	// error names only a table the operator wrote.
	if definesBindings || definesCredentials {
		cfg.Runtimes.Bindings = map[string]RuntimeBinding{}
		cfg.Runtimes.Credentials = map[string]CredentialSource{}
	}
	meta, err := toml.Decode(string(body), &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("decode config.toml: %w", err)
	}
	if sourceVersion >= 3 {
		for id := range cfg.Runtimes.Credentials {
			if meta.IsDefined("runtimes", "credentials", id, "base_url") {
				return Config{}, fmt.Errorf("runtimes.credentials.%s carries a forbidden base_url", id)
			}
		}
		for id := range cfg.Runtimes.Bindings {
			if meta.IsDefined("runtimes", "bindings", id, "base_url") {
				return Config{}, fmt.Errorf("runtimes.bindings.%s carries a forbidden base_url", id)
			}
		}
	}
	undecoded := meta.Undecoded()
	if sourceVersion < 3 {
		undecoded = dropLegacyRuntimeKeys(undecoded)
	}
	if sourceVersion < 4 {
		remaining := undecoded[:0]
		for _, key := range undecoded {
			if len(key) == 2 && key[0] == "budgets" && key[1] == "saturation_streak" {
				continue
			}
			remaining = append(remaining, key)
		}
		undecoded = remaining
	}
	if sourceVersion == 2 && definesBindings {
		undecoded, err = migrateLegacyBindings(body, &cfg, undecoded)
		if err != nil {
			return Config{}, err
		}
	}
	if sourceVersion == 1 {
		// A profile without a model stays disabled until setup verifies its selection.
		cfg.Runtimes.EnabledBindings = slices.DeleteFunc(cfg.Runtimes.EnabledBindings, func(id string) bool {
			binding := cfg.Runtimes.Bindings[id]
			return binding.Model == "" && cfg.Runtimes.Credentials[binding.Credential].Kind == CredentialKindCodexOAuth
		})
	}
	if len(undecoded) != 0 {
		return Config{}, fmt.Errorf("config.toml carries unknown key %q", undecoded[0].String())
	}
	if sourceVersion != CurrentVersion {
		cfg.ConfigVersion = CurrentVersion
		cfg.migrated = true
	}
	for id, source := range cfg.Runtimes.Credentials {
		if source.InferenceURL != "" {
			continue
		}
		for _, reference := range referenceRuntimeCatalog.Credentials {
			if reference.InferenceURL != "" && source.Kind == reference.Kind && source.TokenURL == reference.TokenURL && source.ClientID == reference.ClientID {
				source.InferenceURL = reference.InferenceURL
				cfg.Runtimes.Credentials[id] = source
				break
			}
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// dropLegacyRuntimeKeys removes the keys an older config version carried and
// the current version derives. The enabled harness set is the set of the
// enabled bindings' harness families.
func dropLegacyRuntimeKeys(undecoded []toml.Key) []toml.Key {
	remaining := make([]toml.Key, 0, len(undecoded))
	for _, key := range undecoded {
		if len(key) == 2 && key[0] == "runtimes" && key[1] == "enabled_harness_families" {
			continue
		}
		remaining = append(remaining, key)
	}
	return remaining
}

// migrateLegacyBindings translates the config_version 2 binding tables. Each
// legacy binding becomes one credential source with the binding id and one
// binding that references it. The legacy keys leave the undecoded list.
func migrateLegacyBindings(body []byte, cfg *Config, undecoded []toml.Key) ([]toml.Key, error) {
	var legacy struct {
		Runtimes struct {
			Bindings map[string]legacyRuntimeBinding `toml:"bindings"`
		} `toml:"runtimes"`
	}
	legacyMeta, err := toml.Decode(string(body), &legacy)
	if err != nil {
		return nil, fmt.Errorf("decode config.toml: %w", err)
	}
	for _, key := range legacyMeta.Undecoded() {
		if len(key) >= 4 && key[0] == "runtimes" && key[1] == "bindings" {
			return nil, fmt.Errorf("config.toml carries unknown key %q", key.String())
		}
	}
	remaining := make([]toml.Key, 0, len(undecoded))
	for _, key := range undecoded {
		if len(key) >= 4 && key[0] == "runtimes" && key[1] == "bindings" {
			continue
		}
		remaining = append(remaining, key)
	}
	cfg.Runtimes.Credentials = map[string]CredentialSource{}
	cfg.Runtimes.Bindings = map[string]RuntimeBinding{}
	for id, binding := range legacy.Runtimes.Bindings {
		source := CredentialSource{Kind: binding.AuthKind}
		if source.Delivery() == "" {
			return nil, fmt.Errorf("runtimes.bindings.%s has unsupported auth_kind %q", id, binding.AuthKind)
		}
		// The legacy keys that the new shape derives must agree with the
		// derivation. A file the old validator refused stays refused.
		if binding.Delivery != source.Delivery() {
			return nil, fmt.Errorf("runtimes.bindings.%s delivery %q does not match auth_kind %q", id, binding.Delivery, binding.AuthKind)
		}
		if binding.StaticEnv != "" {
			return nil, fmt.Errorf("runtimes.bindings.%s carries a forbidden static_env", id)
		}
		source.TokenURL = binding.TokenURL
		source.ClientID = binding.ClientID
		cfg.Runtimes.Credentials[id] = source
		cfg.Runtimes.Bindings[id] = RuntimeBinding{
			Harness: binding.Harness, Model: binding.Model,
			Credential: id, BaseURL: binding.BaseURL,
		}
	}
	return remaining, nil
}

func (c Config) Validate() error {
	if c.ConfigVersion != CurrentVersion {
		return fmt.Errorf("config_version %d is unsupported by this build (want %d)", c.ConfigVersion, CurrentVersion)
	}
	if err := uniqueNonempty("runtimes.enabled_bindings", c.Runtimes.EnabledBindings); err != nil {
		return err
	}
	if err := uniqueNonempty("runtimes.forbidden_env", c.Runtimes.ForbiddenEnv); err != nil {
		return err
	}
	if len(c.Runtimes.ForbiddenEnv) > 130 {
		return fmt.Errorf("runtimes.forbidden_env may define at most 128 additions to the 2-name shipped floor")
	}
	for _, required := range []string{"CODEX_API_KEY", "ANTHROPIC_API_KEY"} {
		if !contains(c.Runtimes.ForbiddenEnv, required) {
			return fmt.Errorf("runtimes.forbidden_env may add guards but must retain %q", required)
		}
	}
	for _, name := range c.Runtimes.ForbiddenEnv {
		if !safeEnvName(name) {
			return fmt.Errorf("runtimes.forbidden_env contains invalid environment name %q", name)
		}
	}
	if len(c.Runtimes.Credentials) == 0 {
		return fmt.Errorf("runtimes.credentials must define at least one credential source")
	}
	for id, source := range c.Runtimes.Credentials {
		if !safeRuntimeID(id) {
			return fmt.Errorf("runtimes.credentials contains invalid credential id %q", id)
		}
		if err := validateCredentialSource(id, source); err != nil {
			return err
		}
	}
	if len(c.Runtimes.Bindings) == 0 {
		return fmt.Errorf("runtimes.bindings must define at least one binding")
	}
	bindingIDs := make([]string, 0, len(c.Runtimes.Bindings))
	for id := range c.Runtimes.Bindings {
		bindingIDs = append(bindingIDs, id)
	}
	sort.Strings(bindingIDs)
	for _, id := range bindingIDs {
		binding := c.Runtimes.Bindings[id]
		if !safeRuntimeID(id) {
			return fmt.Errorf("runtimes.bindings contains invalid binding id %q", id)
		}
		source, ok := c.Runtimes.Credentials[binding.Credential]
		if !ok {
			return fmt.Errorf("runtimes.bindings.%s references undefined credential %q", id, binding.Credential)
		}
		// Disabled Codex profiles can wait for setup to select a model.
		unselected := source.Kind == CredentialKindCodexOAuth && !contains(c.Runtimes.EnabledBindings, id)
		if err := validateBindingShape(id, binding, source.Kind, unselected); err != nil {
			return err
		}
	}
	for _, id := range c.Runtimes.EnabledBindings {
		_, ok := c.Runtimes.Bindings[id]
		if !ok {
			return fmt.Errorf("runtimes.enabled_bindings references undefined binding %q", id)
		}
	}
	for name, value := range map[string]int{
		"budgets.review_wip_cap":        c.Budgets.ReviewWIPCap,
		"budgets.max_correction_count":  c.Budgets.MaxCorrectionCount,
		"budgets.dispatch_retries":      c.Budgets.DispatchRetries,
		"timing.timeout_minutes":        c.Timing.TimeoutMinutes,
		"timing.grace_minutes":          c.Timing.GraceMinutes,
		"timing.heartbeat_interval_s":   c.Timing.HeartbeatIntervalS,
		"timing.spawn_grace_s":          c.Timing.SpawnGraceS,
		"timing.hard_deadline_minutes":  c.Timing.HardDeadlineMinutes,
		"timing.tick_interval_s":        c.Timing.TickIntervalS,
		"timing.collie_idle_timeout_s":  c.Timing.CollieIdleTimeoutS,
		"timing.wiki_update_interval_s": c.Timing.WikiUpdateIntervalS,
		"timing.backup_interval_s":      c.Timing.BackupIntervalS,
		"timing.backup_retain":          c.Timing.BackupRetain,
	} {
		if value <= 0 {
			return fmt.Errorf("%s must be positive", name)
		}
	}
	if c.Container.Runtime != "docker" || c.Container.BaseImage == "" ||
		!safeRelative(c.Container.PackageCacheRoot) ||
		!safeRelative(c.Container.MountAllowlist) {
		return fmt.Errorf("container policy is invalid")
	}
	// The boundary package owns the pattern languages, their bounds, and the
	// count limit.
	if _, err := boundary.NewBlockPolicy(c.Container.BoundaryPatterns()); err != nil {
		return fmt.Errorf("container.additional_blocked_patterns: %w", err)
	}
	seenPattern := map[BlockedPattern]bool{}
	for _, addition := range c.Container.AdditionalBlockedPatterns {
		if seenPattern[addition] {
			return fmt.Errorf("container.additional_blocked_patterns contains a duplicate entry")
		}
		seenPattern[addition] = true
	}
	host, port, err := splitBind("surfaces.dashboard.bind", c.Surfaces.Dashboard.Bind)
	if err != nil {
		return err
	}
	if !loopbackHost(host) && strings.TrimSpace(c.Surfaces.Dashboard.AuthToken) == "" {
		return fmt.Errorf("surfaces.dashboard.auth_token is required for non-loopback bind %s:%d", host, port)
	}
	portKeys := map[int]string{port: "surfaces.dashboard.bind"}
	for _, viewer := range []struct{ key, value string }{
		{"surfaces.agentsview.bind", c.Surfaces.AgentsView.Bind},
		{"surfaces.wiki.bind", c.Surfaces.Wiki.Bind},
	} {
		viewerPort, err := loopbackBind(viewer.key, viewer.value)
		if err != nil {
			return err
		}
		if other, taken := portKeys[viewerPort]; taken {
			return fmt.Errorf("%s and %s must use different ports", other, viewer.key)
		}
		portKeys[viewerPort] = viewer.key
	}
	if c.Surfaces.Discord.Enabled {
		if !validSecret(c.Surfaces.Discord.BotToken) ||
			!decimalID(c.Surfaces.Discord.GuildID) ||
			!decimalID(c.Surfaces.Discord.ChannelID) ||
			len(c.Surfaces.Discord.OperatorIDs) == 0 {
			return fmt.Errorf("enabled Discord requires bot_token, guild_id, channel_id, and operator_ids")
		}
		if err := uniqueDecimalIDs(c.Surfaces.Discord.OperatorIDs); err != nil {
			return err
		}
	} else if c.Surfaces.Discord.BotToken != "" || c.Surfaces.Discord.GuildID != "" ||
		c.Surfaces.Discord.ChannelID != "" || len(c.Surfaces.Discord.OperatorIDs) != 0 {
		return fmt.Errorf("disabled Discord must not retain Discord credentials or ids")
	}
	enabledSurfaces := map[string]bool{"dashboard": true, "discord": c.Surfaces.Discord.Enabled}
	for name, routes := range map[string][]string{
		"surfaces.alerts.blocked": c.Surfaces.Alerts.Blocked,
		"surfaces.alerts.health":  c.Surfaces.Alerts.Health,
	} {
		if len(routes) == 0 {
			return fmt.Errorf("%s must have at least one destination", name)
		}
		if err := uniqueNonempty(name, routes); err != nil {
			return err
		}
		for _, route := range routes {
			if !enabledSurfaces[route] {
				return fmt.Errorf("%s references disabled or unknown surface %q", name, route)
			}
		}
	}
	if strings.TrimSpace(c.Git.AuthorName) == "" ||
		strings.TrimSpace(c.Git.AuthorEmail) == "" ||
		!strings.Contains(c.Git.AuthorEmail, "@") ||
		len(c.Git.AuthorName) > 256 || len(c.Git.AuthorEmail) > 256 ||
		strings.ContainsAny(c.Git.AuthorName, "\x00\r\n") ||
		strings.ContainsAny(c.Git.AuthorEmail, "\x00\r\n") {
		return fmt.Errorf("git author_name and author_email must be explicit")
	}
	return nil
}

func Marshal(c Config) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteString("# Culler operator configuration.\n")
	out.WriteString("# This file is local to MC_HOME and must never be committed.\n\n")
	if err := toml.NewEncoder(&out).Encode(c); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func Load(path string) (Config, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return Config{}, err
	}
	stat, ok := before.Sys().(*syscall.Stat_t)
	if !ok || !before.Mode().IsRegular() || before.Mode().Perm() != 0o600 ||
		stat.Uid != uint32(os.Getuid()) || stat.Nlink != 1 {
		return Config{}, fmt.Errorf("config.toml must be an owner-only single-link regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return Config{}, fmt.Errorf("config.toml changed during open")
	}
	body, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return Config{}, err
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(opened, after) {
		return Config{}, fmt.Errorf("config.toml changed during read")
	}
	return Parse(body)
}

// WriteAtomic validates and publishes a complete configuration. It returns
// false when the exact canonical bytes are already installed.
func WriteAtomic(path string, cfg Config) (bool, error) {
	var changed bool
	err := withConfigLock(path, func() error {
		var err error
		changed, err = writeAtomicUnlocked(path, cfg)
		return err
	})
	return changed, err
}

// UpdateAtomic serializes a read-modify-write over the machine-global
// operator config so independent onboarding sections cannot erase one
// another's tables.
func UpdateAtomic(path string, mutate func(*Config) error) (bool, error) {
	return UpdateAtomicAndApply(path, mutate, nil)
}

// UpdateAtomicAndApply holds the config lock through publication and application.
// An application error leaves the saved configuration available for a retry.
func UpdateAtomicAndApply(path string, mutate func(*Config) error, apply func(Config) error) (bool, error) {
	var changed bool
	err := withConfigLock(path, func() error {
		cfg, err := Load(path)
		if err != nil {
			return err
		}
		if err := mutate(&cfg); err != nil {
			return err
		}
		changed, err = writeAtomicUnlocked(path, cfg)
		if err != nil {
			return err
		}
		if apply != nil {
			return apply(cfg)
		}
		return nil
	})
	return changed, err
}

// UpdateAtomicWithRecovery restores the saved values when activation fails.
func UpdateAtomicWithRecovery(path string, mutate func(*Config) error, apply, recover func(Config) error) error {
	return withConfigLock(path, func() error {
		previous, err := Load(path)
		if err != nil {
			return err
		}
		body, err := json.Marshal(previous)
		if err != nil {
			return err
		}
		var desired Config
		if err := json.Unmarshal(body, &desired); err != nil {
			return err
		}
		if err := mutate(&desired); err != nil {
			return err
		}
		if _, err := writeAtomicUnlocked(path, desired); err != nil {
			return err
		}
		if err := apply(desired); err != nil {
			if _, restoreErr := writeAtomicUnlocked(path, previous); restoreErr != nil {
				return fmt.Errorf("The change failed. The saved configuration requires recovery.")
			}
			if restoreErr := recover(previous); restoreErr != nil {
				return fmt.Errorf("The previous configuration is saved. Its activation is unverified. Check the connection again.")
			}
			return err
		}
		return nil
	})
}

// WriteAtomicCAS publishes desired only while path still contains expected.
// Host-side onboarding plans use it to reject concurrent section updates.
func WriteAtomicCAS(path string, expected, desired Config) (bool, error) {
	var changed bool
	err := withConfigLock(path, func() error {
		current, err := Load(path)
		if err != nil {
			return err
		}
		currentBody, err := Marshal(current)
		if err != nil {
			return err
		}
		expectedBody, err := Marshal(expected)
		if err != nil {
			return err
		}
		if !bytes.Equal(currentBody, expectedBody) {
			return fmt.Errorf("config.toml changed after onboarding prepared its update; retry the section")
		}
		changed, err = writeAtomicUnlocked(path, desired)
		return err
	})
	return changed, err
}

func writeAtomicUnlocked(path string, cfg Config) (bool, error) {
	body, err := Marshal(cfg)
	if err != nil {
		return false, err
	}
	if current, err := Load(path); err == nil {
		if !current.migrated {
			currentBody, marshalErr := Marshal(current)
			if marshalErr != nil {
				return false, marshalErr
			}
			if bytes.Equal(currentBody, body) {
				return false, nil
			}
		}
	} else if !os.IsNotExist(err) {
		return false, err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config.toml.tmp-*")
	if err != nil {
		return false, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return false, err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return false, err
	}
	if err := syncConfigDir(dir); err != nil {
		return true, err
	}
	return true, nil
}

func Ensure(path string) (bool, error) {
	var changed bool
	err := withConfigLock(path, func() error {
		if cfg, err := Load(path); err == nil {
			if !cfg.migrated {
				return nil
			}
			var writeErr error
			changed, writeErr = writeAtomicUnlocked(path, cfg)
			return writeErr
		} else if !os.IsNotExist(err) {
			return err
		}
		if _, err := Parse(exampleBody); err != nil {
			return fmt.Errorf("embedded config.example.toml is invalid: %w", err)
		}
		var err error
		changed, err = writeBodyAtomic(path, exampleBody)
		return err
	})
	return changed, err
}

func writeBodyAtomic(path string, body []byte) (bool, error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config.toml.tmp-*")
	if err != nil {
		return false, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return false, err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return false, err
	}
	if err := syncConfigDir(dir); err != nil {
		return true, err
	}
	return true, nil
}

func withConfigLock(path string, fn func() error) error {
	lockPath := filepath.Join(filepath.Dir(path), ".config.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 ||
		stat.Uid != uint32(os.Getuid()) || stat.Nlink != 1 {
		return fmt.Errorf("config lock must be an owner-only single-link regular file")
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func syncConfigDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func uniqueNonempty(name string, values []string) error {
	if values == nil {
		return fmt.Errorf("%s must be explicit", name)
	}
	seen := map[string]bool{}
	for _, value := range values {
		if strings.TrimSpace(value) == "" || seen[value] {
			return fmt.Errorf("%s contains an empty or duplicate value", name)
		}
		seen[value] = true
	}
	return nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func safeRuntimeID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
			(i > 0 && (r == '-' || r == '_' || r == '.')) {
			continue
		}
		return false
	}
	return true
}

func safeRuntimeValue(value string) bool {
	if strings.TrimSpace(value) != value || value == "" || len(value) > 256 ||
		!utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func safeDisplayName(value string) bool {
	return strings.TrimSpace(value) == value && value != "" && len(value) <= 120 && !strings.ContainsAny(value, "\x00\r\n\t")
}

func safeEnvName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for i, r := range value {
		if (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

// ValidateCredentialSource restricts a connection to the release integration for its kind.
func ValidateCredentialSource(id string, source CredentialSource) error {
	if !safeRuntimeID(id) {
		return fmt.Errorf("the connection identifier is invalid")
	}
	return validateCredentialSource(id, source)
}

func validateCredentialSource(id string, source CredentialSource) error {
	if source.Name != "" && !safeDisplayName(source.Name) {
		return fmt.Errorf("connection name is invalid")
	}
	if source.BaseURL != "" {
		return fmt.Errorf("runtimes.credentials.%s carries a forbidden base_url", id)
	}
	if source.Kind == CredentialKindOpenCodeAPIKey {
		if !safeRuntimeID(source.CatalogEntry) || source.InferenceURL != "" || source.TokenURL != "" || source.ClientID != "" {
			return fmt.Errorf("The OpenCode connection requires one catalog entry and no address.")
		}
		return nil
	}
	if source.CatalogEntry != "" {
		return fmt.Errorf("This connection cannot select a catalog entry.")
	}
	for _, reference := range referenceRuntimeCatalog.Credentials {
		if source.Kind != reference.Kind {
			continue
		}
		if source.TokenURL != reference.TokenURL || source.ClientID != reference.ClientID ||
			(source.InferenceURL != "" && source.InferenceURL != reference.InferenceURL) {
			return fmt.Errorf("runtimes.credentials.%s must use the release provider integration", id)
		}
		return nil
	}
	return fmt.Errorf("runtimes.credentials.%s has an unsupported kind", id)
}

// ValidateBindingShape checks the profile against its supported sign-in connection.
func ValidateBindingShape(id string, binding RuntimeBinding, kind string) error {
	if !safeRuntimeID(id) || (binding.Credential != "" && !safeRuntimeID(binding.Credential)) {
		return fmt.Errorf("the profile or connection identifier is invalid")
	}
	return validateBindingShape(id, binding, kind, false)
}

func validateBindingShape(id string, binding RuntimeBinding, kind string, allowUnselected bool) error {
	if binding.Name != "" && !safeDisplayName(binding.Name) {
		return fmt.Errorf("agent profile name is invalid")
	}
	if !(allowUnselected && binding.Model == "") && !safeRuntimeValue(binding.Model) {
		return fmt.Errorf("runtimes.bindings.%s model is invalid", id)
	}
	source := CredentialSource{Kind: kind}
	if source.Harness() == "" {
		return fmt.Errorf("runtimes.bindings.%s references a credential with unsupported kind %q", id, kind)
	}
	if binding.Harness != source.Harness() {
		return fmt.Errorf("runtimes.bindings.%s harness %q does not match credential kind %q", id, binding.Harness, kind)
	}
	if binding.BaseURL != "" {
		return fmt.Errorf("runtimes.bindings.%s carries a forbidden provider address", id)
	}
	if binding.Effort != "" && !slices.Contains(EffortLevels(binding.Harness), binding.Effort) {
		return fmt.Errorf("The effort level is unsupported by this harness.")
	}
	return nil
}

func uniqueDecimalIDs(values []string) error {
	seen := map[string]bool{}
	for _, value := range values {
		if !decimalID(value) || seen[value] {
			return fmt.Errorf("discord.operator_ids contains an invalid or duplicate id")
		}
		seen[value] = true
	}
	return nil
}

func decimalID(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validSecret(value string) bool {
	return value != "" && len(value) <= 4096 && utf8.ValidString(value) &&
		!strings.ContainsAny(value, "\x00\r\n")
}

func safeRelative(value string) bool {
	return value != "" && !filepath.IsAbs(value) && filepath.Clean(value) == value &&
		value != "." && value != ".." && !strings.HasPrefix(value, ".."+string(os.PathSeparator))
}

func splitBind(key, value string) (string, int, error) {
	host, rawPort, err := net.SplitHostPort(value)
	if err != nil || host == "" {
		return "", 0, fmt.Errorf("%s must be host:port", key)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("%s port must be 1..65535", key)
	}
	return host, port, nil
}

// loopbackBind accepts only the host names that the viewers accept in a Host
// header, so a loopback alias such as 127.0.0.2 is refused.
func loopbackBind(key, value string) (int, error) {
	host, port, err := splitBind(key, value)
	if err != nil {
		return 0, err
	}
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return port, nil
	}
	return 0, fmt.Errorf("%s must use a loopback host (127.0.0.1, ::1, or localhost)", key)
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// EffortLevels lists the levels accepted by the pinned harnesses.
func EffortLevels(harness string) []string {
	switch harness {
	case "opencode":
		return []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "thinking"}
	case "codex":
		return []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}
	case "claude-sdk":
		return []string{"low", "medium", "high", "xhigh", "max"}
	default:
		return nil
	}
}
