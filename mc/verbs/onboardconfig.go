package verbs

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"mc/boundary"
	operatorconfig "mc/config"
	"mc/deployment"
	"mc/routing"
)

// postDiscordOnboardingTest is a seam so the owner-confirmed enablement gate
// is testable without a bot account. Its production implementation posts only
// a fixed confirmation string; the credential never appears in an error,
// config preview, or child process.
var postDiscordOnboardingTest = func(token, channelID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	body, err := json.Marshal(map[string]any{
		"content":          "Culler Discord onboarding test. Confirm this message with --confirm-discord-test.",
		"allowed_mentions": map[string]any{"parse": []string{}},
	})
	if err != nil {
		return Domainf("encode Discord onboarding test: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://discord.com/api/v10/channels/"+channelID+"/messages", bytes.NewReader(body))
	if err != nil {
		return Domainf("prepare Discord onboarding test: %v", err)
	}
	req.Header.Set("Authorization", "Bot "+token)
	req.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return Domainf("post Discord onboarding test: %v", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return Domainf("post Discord onboarding test: Discord returned HTTP %d", response.StatusCode)
	}
	return nil
}

func loadOperatorConfig() (operatorconfig.Config, string, error) {
	home, err := mcHomeDir()
	if err != nil {
		return operatorconfig.Config{}, "", err
	}
	path := filepath.Join(home, "config.toml")
	cfg, err := operatorconfig.Load(path)
	if err != nil {
		return operatorconfig.Config{}, "", Domainf("load operator config %q: %v", path, err)
	}
	return cfg, path, nil
}

func updateOperatorConfig(mutate func(*operatorconfig.Config) error) (bool, error) {
	_, path, err := loadOperatorConfig()
	if err != nil {
		return false, err
	}
	changed, err := operatorconfig.UpdateAtomic(path, mutate)
	if err != nil {
		return false, Domainf("publish operator config %q: %v", path, err)
	}
	return changed, nil
}

// onboardBindingCatalog lists the bindings that a runtime selection can name:
// each binding of cfg, plus the fake binding when the build admits it.
func onboardBindingCatalog(cfg operatorconfig.Config) map[string]routing.BindingSpec {
	catalog := routing.ConfiguredBindings(cfg.Runtimes)
	if active, allowFake := routing.ActiveRegistry(); allowFake {
		if harness, ok := active["fake"]; ok {
			catalog["fake"] = routing.BindingSpec{ID: "fake", Harness: harness}
		}
	}
	return catalog
}

// selectedOnboardBindingIDs splits the --runtime-bindings list. Each id must
// name one binding of the catalog, and only once.
func selectedOnboardBindingIDs(raw string, catalog map[string]routing.BindingSpec) ([]string, error) {
	if raw == "" {
		return nil, Usagef("onboarding requires an explicit runtime binding selection")
	}
	seen := map[string]bool{}
	out := []string{}
	for _, part := range strings.Split(raw, ",") {
		id := strings.TrimSpace(part)
		if id == "" {
			return nil, Usagef("--runtime-bindings contains an empty binding id. Remove the extra comma.")
		}
		if seen[id] {
			return nil, Usagef("--runtime-bindings names %q twice. Remove the repeated id.", id)
		}
		if _, ok := catalog[id]; !ok {
			return nil, Usagef("--runtime-bindings names %q, an undefined binding. The defined bindings are: %s. Use one of these ids.",
				id, strings.Join(slices.Sorted(maps.Keys(catalog)), ", "))
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// CheckOnboardRuntimeBindings verifies the --runtime-bindings list before
// onboarding changes a file. An id can name a binding of config.toml, a
// reference binding when config.toml is absent.
func CheckOnboardRuntimeBindings(raw string) error {
	cfg, err := loadOnboardSelectionConfig()
	if err != nil {
		return err
	}
	catalog := onboardBindingCatalog(cfg)
	_, err = selectedOnboardBindingIDs(raw, catalog)
	return err
}

// loadOnboardSelectionConfig reads config.toml. The template stands in for
// an absent config.toml.
func loadOnboardSelectionConfig() (operatorconfig.Config, error) {
	home, err := mcHomeDir()
	if err != nil {
		return operatorconfig.Config{}, err
	}
	path := filepath.Join(home, "config.toml")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return operatorconfig.Default(), nil
	} else if err != nil {
		return operatorconfig.Config{}, Domainf("inspect operator config %q: %v", path, err)
	}
	cfg, err := operatorconfig.Load(path)
	if err != nil {
		return operatorconfig.Config{}, Domainf("load operator config %q: %v", path, err)
	}
	return cfg, nil
}

// EnsureOnboardRuntimeCatalog creates the supported sign-in catalog when absent.
func EnsureOnboardRuntimeCatalog() (bool, error) {
	home, err := mcHomeDir()
	if err != nil {
		return false, err
	}
	changed, err := operatorconfig.Ensure(filepath.Join(home, "config.toml"))
	if err != nil {
		return false, Domainf("ensure operator config before runtime-auth: %v", err)
	}
	return changed, nil
}

// RecordOnboardRuntimeBindings publishes the successful Runtime Auth choice
// into config.toml. Extra imported grants remain inert; this list is the
// machine-global enablement authority used by later onboarding sections.
func RecordOnboardRuntimeBindings(raw string) (bool, error) {
	home, err := mcHomeDir()
	if err != nil {
		return false, err
	}
	ensured, err := operatorconfig.Ensure(filepath.Join(home, "config.toml"))
	if err != nil {
		return false, Domainf("ensure operator config before runtime publication: %v", err)
	}
	cfg, _, err := loadOperatorConfig()
	if err != nil {
		return false, err
	}
	bindings, err := selectedOnboardBindingIDs(raw, onboardBindingCatalog(cfg))
	if err != nil {
		return false, err
	}
	configBindings := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		if binding != "fake" {
			configBindings = append(configBindings, binding)
		}
	}
	if len(configBindings) == 0 {
		return ensured, nil
	}
	changed, err := updateOperatorConfig(func(cfg *operatorconfig.Config) error {
		cfg.Runtimes.EnabledBindings = append([]string(nil), configBindings...)
		return nil
	})
	return ensured || changed, err
}

// DisableOnboardRuntimeBinding removes one binding from the enabled list
// after a failed acceptance. A role that routing.md maps to the binding moves
// to the first enabled binding that remains. It returns the moved roles. The
// list is empty when the table does not name the binding or when no enabled
// binding remains; then the table stays as it is and every reader refuses it.
func DisableOnboardRuntimeBinding(binding string) ([]string, error) {
	home, err := mcHomeDir()
	if err != nil {
		return nil, err
	}
	if _, err := updateOperatorConfig(func(cfg *operatorconfig.Config) error {
		remaining := make([]string, 0, len(cfg.Runtimes.EnabledBindings))
		for _, id := range cfg.Runtimes.EnabledBindings {
			if id != binding {
				remaining = append(remaining, id)
			}
		}
		cfg.Runtimes.EnabledBindings = remaining
		return nil
	}); err != nil {
		return nil, err
	}
	cfg, _, err := loadOperatorConfig()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, "routing.md")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, Domainf("inspect routing.md at %q: %v", path, err)
	}
	data, err := readRoutingPath(path)
	if err != nil {
		return nil, err
	}
	// The disabled binding still resolves in the configured registry, so the
	// table parses; the moved rows must resolve in the enabled registry.
	_, allowFake := routing.ActiveRegistry()
	configured := routing.Registry{}
	for id, spec := range routing.ConfiguredBindings(cfg.Runtimes) {
		configured[id] = spec.Harness
	}
	enabled := routing.RegistryFromRuntimes(cfg.Runtimes)
	if allowFake {
		configured["fake"], enabled["fake"] = "fake", "fake"
	}
	table, err := routing.Parse(data, configured)
	if err != nil {
		return nil, Domainf("invalid routing.md at %q: %v", path, err)
	}
	first := ""
	if len(cfg.Runtimes.EnabledBindings) != 0 {
		first = cfg.Runtimes.EnabledBindings[0]
	} else if allowFake {
		first = "fake"
	}
	moved := []string{}
	for _, route := range table.Rows() {
		if route.Binding != binding {
			continue
		}
		if first == "" {
			return nil, nil
		}
		table, err = table.WithRoute(route.Role, first, enabled)
		if err != nil {
			return nil, Domainf("move role %s off binding %s: %v", route.Role, binding, err)
		}
		moved = append(moved, route.Role)
	}
	if len(moved) == 0 {
		return nil, nil
	}
	candidate := []byte(table.Markdown())
	if _, err := routing.Parse(candidate, enabled); err != nil {
		return nil, Domainf("routing repair produced an invalid table: %v", err)
	}
	if err := replaceRoutingTable(home, path, candidate); err != nil {
		return nil, err
	}
	return moved, nil
}

// OnboardTunablesPlan keeps host-only config bytes out of the mountless
// helper. The helper receives only the resulting scalar spine projection.
type OnboardTunablesPlan struct {
	Original operatorconfig.Config
	Desired  operatorconfig.Config
	Path     string
}

func PrepareOnboardTunablesHost(a OnboardArgs) (OnboardTunablesPlan, error) {
	original, path, err := loadOperatorConfig()
	if err != nil {
		return OnboardTunablesPlan{}, err
	}
	desired := original
	applyOnboardTunableAnswers(&desired, a)
	if err := desired.Validate(); err != nil {
		return OnboardTunablesPlan{}, Usagef("requested tunable configuration is invalid: %v", err)
	}
	return OnboardTunablesPlan{Original: original, Desired: desired, Path: path}, nil
}

func applyOnboardTunableAnswers(desired *operatorconfig.Config, a OnboardArgs) {
	if a.TunablesConfirmed {
		defaults := operatorconfig.Default()
		desired.Budgets = defaults.Budgets
		desired.Timing.TimeoutMinutes = defaults.Timing.TimeoutMinutes
		desired.Timing.GraceMinutes = defaults.Timing.GraceMinutes
		desired.Timing.HeartbeatIntervalS = defaults.Timing.HeartbeatIntervalS
		desired.Timing.SpawnGraceS = defaults.Timing.SpawnGraceS
		desired.Timing.HardDeadlineMinutes = defaults.Timing.HardDeadlineMinutes
		desired.Timing.TickIntervalS = defaults.Timing.TickIntervalS
		desired.Timing.CollieIdleTimeoutS = defaults.Timing.CollieIdleTimeoutS
		desired.Timing.WikiUpdateIntervalS = defaults.Timing.WikiUpdateIntervalS
		desired.Timing.BackupIntervalS = defaults.Timing.BackupIntervalS
		desired.Timing.BackupRetain = defaults.Timing.BackupRetain
	}
	if a.AcceptDefaultGitIdentity {
		desired.Git = operatorconfig.Default().Git
	} else if a.GitAuthorName != "" && a.GitAuthorEmail != "" {
		desired.Git.AuthorName = a.GitAuthorName
		desired.Git.AuthorEmail = a.GitAuthorEmail
	}
	if a.ReviewWIPCap > 0 {
		desired.Budgets.ReviewWIPCap = a.ReviewWIPCap
	}
	for _, item := range []struct {
		value  int
		target *int
	}{
		{a.TimeoutMinutes, &desired.Timing.TimeoutMinutes},
		{a.GraceMinutes, &desired.Timing.GraceMinutes},
		{a.HeartbeatIntervalS, &desired.Timing.HeartbeatIntervalS},
		{a.SpawnGraceS, &desired.Timing.SpawnGraceS},
		{a.HardDeadlineMinutes, &desired.Timing.HardDeadlineMinutes},
		{a.CollieIdleTimeoutS, &desired.Timing.CollieIdleTimeoutS},
	} {
		if item.value > 0 {
			*item.target = item.value
		}
	}
}

func (p OnboardTunablesPlan) Project(a OnboardArgs) OnboardArgs {
	a.ReviewWIPCap = p.Desired.Budgets.ReviewWIPCap
	a.TimeoutMinutes = p.Desired.Timing.TimeoutMinutes
	a.GraceMinutes = p.Desired.Timing.GraceMinutes
	a.HeartbeatIntervalS = p.Desired.Timing.HeartbeatIntervalS
	a.SpawnGraceS = p.Desired.Timing.SpawnGraceS
	a.HardDeadlineMinutes = p.Desired.Timing.HardDeadlineMinutes
	a.CollieIdleTimeoutS = p.Desired.Timing.CollieIdleTimeoutS
	a.WikiUpdateIntervalS = p.Desired.Timing.WikiUpdateIntervalS
	return a
}

func ApplyOnboardTunablesHost(plan OnboardTunablesPlan) (bool, error) {
	changed, err := operatorconfig.WriteAtomicCAS(plan.Path, plan.Original, plan.Desired)
	if err != nil {
		return false, Domainf("publish tunable configuration: %v", err)
	}
	return changed, nil
}

// OnboardHostConfig is the already-validated host-process projection used by
// native supervision. Secret values never enter argv or the spine.
// It embeds the deployment's own policy struct rather than restating its
// fields: prepare and the loaded-policy recheck then read one value, so a
// new knob cannot reach the written bundle while the recheck ignores it.
type OnboardHostConfig struct {
	deployment.SupervisionPolicy
}

// residentRuntimeBindings projects the enabled catalog into the resident's
// credential shape: the channel and the authority that channel is defined by,
// and nothing else. No secret and no model crosses — the model reaches a
// container through the dispatch effect, not through the resident's config.
func residentRuntimeBindings(runtimes operatorconfig.Runtimes) map[string]deployment.ResidentRuntimeBinding {
	out := map[string]deployment.ResidentRuntimeBinding{}
	for id, spec := range routing.EnabledBindings(runtimes) {
		binding := deployment.ResidentRuntimeBinding{
			Harness: spec.Harness, Credential: spec.Credential, Channel: spec.Channel,
		}
		binding.TokenURL = spec.TokenURL
		binding.ClientID = spec.ClientID
		out[id] = binding
	}
	return out
}

func LoadOnboardHostConfig() (OnboardHostConfig, error) {
	cfg, _, err := loadOperatorConfig()
	if err != nil {
		return OnboardHostConfig{}, err
	}
	return OnboardHostConfig{deployment.SupervisionPolicy{
		DashboardBind:      cfg.Surfaces.Dashboard.Bind,
		DashboardAuthToken: cfg.Surfaces.Dashboard.AuthToken,
		BackupIntervalS:    cfg.Timing.BackupIntervalS,
		TickIntervalS:      cfg.Timing.TickIntervalS,
		GitAuthorName:      cfg.Git.AuthorName,
		GitAuthorEmail:     cfg.Git.AuthorEmail,
		RuntimeBindings:    residentRuntimeBindings(cfg.Runtimes),
		AgentsViewBind:     cfg.Surfaces.AgentsView.Bind,
		WikiBind:           cfg.Surfaces.Wiki.Bind,
	}}, nil
}

type OnboardSurfacesPlan struct {
	Original          operatorconfig.Config
	Config            operatorconfig.Config
	Path              string
	Supplied          bool
	DashboardSupplied bool
	DiscordSupplied   bool
}

func readOnboardSecretFile(path, label string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", Usagef("%s must be an absolute owner-only file path", label)
	}
	if err := boundary.TrustPolicyFile(path, os.Getuid()); err != nil {
		return "", Domainf("%s: %v", label, err)
	}
	before, err := os.Lstat(path)
	if err != nil {
		return "", Domainf("%s: %v", label, err)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", Domainf("%s: %v", label, err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return "", Domainf("%s changed during open", label)
	}
	body, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(body) > 4096 {
		return "", Domainf("%s exceeds its 4096-byte bound", label)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(opened, after) {
		return "", Domainf("%s changed during read", label)
	}
	value := strings.TrimSpace(string(body))
	if value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return "", Domainf("%s is empty or contains forbidden controls", label)
	}
	return value, nil
}

func splitDiscordOperatorIDs(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, strings.TrimSpace(part))
	}
	return out
}

func PrepareOnboardSurfacesHost(a OnboardArgs) (OnboardSurfacesPlan, error) {
	cfg, path, err := loadOperatorConfig()
	if err != nil {
		return OnboardSurfacesPlan{}, err
	}
	original := cfg
	surfaceChoice := a.AcceptDefaultDashboardBind || a.DashboardBind != "" ||
		a.DashboardAuthTokenFile != "" || a.DiscordMode != ""
	if !surfaceChoice {
		return OnboardSurfacesPlan{
			Original: original, Config: cfg, Path: path,
		}, nil
	}
	if a.AcceptDefaultDashboardBind == (a.DashboardBind != "") {
		return OnboardSurfacesPlan{}, Usagef("surfaces requires exactly one of --accept-default-dashboard-bind or --dashboard-bind")
	}
	if a.AcceptDefaultDashboardBind {
		cfg.Surfaces.Dashboard.Bind = operatorconfig.Default().Surfaces.Dashboard.Bind
	} else {
		cfg.Surfaces.Dashboard.Bind = a.DashboardBind
	}
	if a.DashboardAuthTokenFile != "" {
		token, err := readOnboardSecretFile(a.DashboardAuthTokenFile, "dashboard auth-token source")
		if err != nil {
			return OnboardSurfacesPlan{}, err
		}
		cfg.Surfaces.Dashboard.AuthToken = token
	} else if cfg.Surfaces.Dashboard.AuthToken == "" {
		// Every bind is token-gated, loopback included. Without an
		// operator-supplied token file, onboarding mints one; a re-run keeps
		// the minted token stable.
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return OnboardSurfacesPlan{}, Domainf("mint dashboard auth token: %v", err)
		}
		cfg.Surfaces.Dashboard.AuthToken = hex.EncodeToString(raw)
	}
	discordEnabled := false
	switch a.DiscordMode {
	case "":
		// A dashboard change preserves the current Discord configuration.
	case "disabled":
		cfg.Surfaces.Discord = operatorconfig.Discord{OperatorIDs: []string{}}
		cfg.Surfaces.Alerts.Blocked = []string{"dashboard"}
		cfg.Surfaces.Alerts.Health = []string{"dashboard"}
	case "enabled":
		if !a.DiscordTestConfirmed {
			return OnboardSurfacesPlan{}, Usagef("enabled Discord requires --confirm-discord-test after verifying the onboarding test message")
		}
		token, err := readOnboardSecretFile(a.DiscordBotTokenFile, "Discord bot-token source")
		if err != nil {
			return OnboardSurfacesPlan{}, err
		}
		cfg.Surfaces.Discord = operatorconfig.Discord{
			Enabled: true, BotToken: token, GuildID: a.DiscordGuildID,
			ChannelID: a.DiscordChannelID, OperatorIDs: splitDiscordOperatorIDs(a.DiscordOperatorIDs),
		}
		cfg.Surfaces.Alerts.Blocked = []string{"dashboard", "discord"}
		cfg.Surfaces.Alerts.Health = []string{"dashboard", "discord"}
		discordEnabled = true
	default:
		return OnboardSurfacesPlan{}, Usagef("surfaces requires --discord-mode disabled|enabled")
	}
	if err := cfg.Validate(); err != nil {
		return OnboardSurfacesPlan{}, Usagef("requested surface configuration is invalid: %v", err)
	}
	if discordEnabled {
		if err := postDiscordOnboardingTest(cfg.Surfaces.Discord.BotToken, cfg.Surfaces.Discord.ChannelID); err != nil {
			return OnboardSurfacesPlan{}, err
		}
	}
	return OnboardSurfacesPlan{
		Original: original, Config: cfg, Path: path, Supplied: true,
		DashboardSupplied: true, DiscordSupplied: a.DiscordMode != "",
	}, nil
}

// ProjectOnboardAlertRoutes carries only the validated, secret-free
// destination projection across the host→helper boundary. Bot credentials and
// author ids remain resident-only host policy.
func ProjectOnboardAlertRoutes(req *OnboardStateRequest, cfg operatorconfig.Config) error {
	if req == nil || req.Section != "surfaces" {
		return Domainf("alert routes may be projected only for onboard surfaces")
	}
	out := make([]OnboardAlertRoute, 0, 6)
	for _, class := range []struct {
		name   string
		routes []string
	}{
		{name: "blocked", routes: cfg.Surfaces.Alerts.Blocked},
		{name: "health", routes: cfg.Surfaces.Alerts.Health},
	} {
		for index, surface := range class.routes {
			channel := ""
			if surface == "discord" {
				channel = cfg.Surfaces.Discord.ChannelID
			}
			out = append(out, OnboardAlertRoute{
				Class: class.name, Ordinal: index + 1,
				Surface: surface, ChannelRef: channel,
			})
		}
	}
	if err := validateOnboardAlertRoutes(out); err != nil {
		return Domainf("project configured alert routes: %v", err)
	}
	req.AlertRoutes = out
	return nil
}

func ProjectCurrentOnboardAlertRoutes(req *OnboardStateRequest) error {
	cfg, _, err := loadOperatorConfig()
	if err != nil {
		return err
	}
	return ProjectOnboardAlertRoutes(req, cfg)
}

func ApplyOnboardSurfacesHost(plan OnboardSurfacesPlan) (string, string, error) {
	if !plan.Supplied {
		return "ok", "config.toml surface policy is valid", nil
	}
	changed, err := operatorconfig.UpdateAtomic(plan.Path, func(cfg *operatorconfig.Config) error {
		if plan.DashboardSupplied {
			cfg.Surfaces.Dashboard = plan.Config.Surfaces.Dashboard
		}
		if plan.DiscordSupplied {
			cfg.Surfaces.Discord = plan.Config.Surfaces.Discord
			cfg.Surfaces.Alerts = plan.Config.Surfaces.Alerts
		}
		return cfg.Validate()
	})
	if err != nil {
		return "", "", Domainf("publish surface configuration: %v", err)
	}
	status := "ok"
	if changed {
		status = "done"
	}
	discord := "disabled"
	if plan.Config.Surfaces.Discord.Enabled {
		discord = "enabled"
	}
	return status, "dashboard " + plan.Config.Surfaces.Dashboard.Bind +
		" (token-gated on every bind; open http://" +
		plan.Config.Surfaces.Dashboard.Bind + "/#token=<surfaces.dashboard.auth_token from config.toml> once per browser;" +
		" the browser keeps the token, so http://" + plan.Config.Surfaces.Dashboard.Bind + "/ works after that)" +
		"; Discord " + discord + "; config.toml surface policy verified", nil
}
