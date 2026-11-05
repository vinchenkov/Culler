package verbs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	operatorconfig "mc/config"
	"mc/routing"
)

func onboardConfigFixture(t *testing.T) (string, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "mc-home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_HOME", home)
	path := filepath.Join(home, "config.toml")
	if _, err := operatorconfig.Ensure(path); err != nil {
		t.Fatal(err)
	}
	if _, err := operatorconfig.UpdateAtomic(path, func(cfg *operatorconfig.Config) error {
		profile := cfg.Runtimes.Bindings["chatgpt"]
		profile.Model = "fixture-model"
		cfg.Runtimes.Bindings["chatgpt"] = profile
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return home, path
}

func TestOnboardSurfacesPublishesAuthenticatedRemoteDashboard(t *testing.T) {
	home, path := onboardConfigFixture(t)
	tokenPath := filepath.Join(home, "dashboard-token")
	if err := os.WriteFile(tokenPath, []byte("dashboard-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := PrepareOnboardSurfacesHost(OnboardArgs{
		DashboardBind:          "0.0.0.0:7443",
		DashboardAuthTokenFile: tokenPath,
		DiscordMode:            "disabled",
	})
	if err != nil {
		t.Fatal(err)
	}
	status, detail, err := ApplyOnboardSurfacesHost(plan)
	if err != nil || status != "done" || !strings.Contains(detail, "Discord disabled") {
		t.Fatalf("apply = status %q detail %q err %v", status, detail, err)
	}
	cfg, err := operatorconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Surfaces.Dashboard.Bind != "0.0.0.0:7443" ||
		cfg.Surfaces.Dashboard.AuthToken != "dashboard-secret" ||
		cfg.Surfaces.Discord.Enabled {
		t.Fatalf("surface config = %+v", cfg.Surfaces)
	}
	plan, err = PrepareOnboardSurfacesHost(OnboardArgs{
		DashboardBind:          "0.0.0.0:7443",
		DashboardAuthTokenFile: tokenPath,
		DiscordMode:            "disabled",
	})
	if err != nil {
		t.Fatal(err)
	}
	status, _, err = ApplyOnboardSurfacesHost(plan)
	if err != nil || status != "ok" {
		t.Fatalf("replay = status %q err %v", status, err)
	}
}

func TestOnboardSurfacesMintsATokenWhenNoneSupplied(t *testing.T) {
	_, path := onboardConfigFixture(t)
	prepare := func() OnboardSurfacesPlan {
		t.Helper()
		plan, err := PrepareOnboardSurfacesHost(OnboardArgs{
			AcceptDefaultDashboardBind: true, DiscordMode: "disabled",
		})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	if _, _, err := ApplyOnboardSurfacesHost(prepare()); err != nil {
		t.Fatal(err)
	}
	cfg, err := operatorconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	minted := cfg.Surfaces.Dashboard.AuthToken
	if len(minted) != 64 {
		t.Fatalf("minted token = %q", minted)
	}
	if _, _, err := ApplyOnboardSurfacesHost(prepare()); err != nil {
		t.Fatal(err)
	}
	cfg, err = operatorconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Surfaces.Dashboard.AuthToken != minted {
		t.Fatal("a re-run replaced the minted token")
	}
}

func TestOnboardDashboardChangePreservesDiscord(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			_, path := onboardConfigFixture(t)
			cfg, err := operatorconfig.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Surfaces.Dashboard.AuthToken = "fixture-dashboard-token"
			if enabled {
				cfg.Surfaces.Discord = operatorconfig.Discord{
					Enabled: true, BotToken: "fixture-discord-token", GuildID: "123",
					ChannelID: "456", OperatorIDs: []string{"789"},
				}
				cfg.Surfaces.Alerts.Blocked = []string{"discord", "dashboard"}
				cfg.Surfaces.Alerts.Health = []string{"dashboard"}
			}
			if _, err := operatorconfig.WriteAtomic(path, cfg); err != nil {
				t.Fatal(err)
			}
			oldPost := postDiscordOnboardingTest
			t.Cleanup(func() { postDiscordOnboardingTest = oldPost })
			postDiscordOnboardingTest = func(string, string) error {
				t.Fatal("A dashboard change sent a Discord message.")
				return nil
			}
			plan, err := PrepareOnboardSurfacesHost(OnboardArgs{DashboardBind: "127.0.0.1:7444"})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := ApplyOnboardSurfacesHost(plan); err != nil {
				t.Fatal(err)
			}
			after, err := operatorconfig.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if after.Surfaces.Dashboard.Bind != "127.0.0.1:7444" ||
				after.Surfaces.Dashboard.AuthToken != cfg.Surfaces.Dashboard.AuthToken {
				t.Fatal("The dashboard change lost its address or authentication.")
			}
			if !reflect.DeepEqual(after.Surfaces.Discord, cfg.Surfaces.Discord) ||
				!reflect.DeepEqual(after.Surfaces.Alerts, cfg.Surfaces.Alerts) {
				t.Fatal("The dashboard change altered Discord or the alert routes.")
			}
		})
	}
}

func TestOnboardSurfacesRefusesUnsafeSecretsAndUnconfirmedDiscord(t *testing.T) {
	home, _ := onboardConfigFixture(t)
	widened := filepath.Join(home, "widened-token")
	if err := os.WriteFile(widened, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string]OnboardArgs{
		"widened token": {
			DashboardBind: "0.0.0.0:7443", DashboardAuthTokenFile: widened,
			DiscordMode: "disabled",
		},
		"Discord confirmation missing": {
			AcceptDefaultDashboardBind: true, DiscordMode: "enabled",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := PrepareOnboardSurfacesHost(args); err == nil {
				t.Fatal("unsafe or unavailable surface policy accepted")
			}
		})
	}
}

func TestOnboardSurfacesEnablesConfirmedDiscord(t *testing.T) {
	home, path := onboardConfigFixture(t)
	tokenPath := filepath.Join(home, "discord-token")
	if err := os.WriteFile(tokenPath, []byte("discord-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldPost := postDiscordOnboardingTest
	t.Cleanup(func() { postDiscordOnboardingTest = oldPost })
	posts := [][2]string{}
	postDiscordOnboardingTest = func(token, channel string) error {
		posts = append(posts, [2]string{token, channel})
		return nil
	}
	plan, err := PrepareOnboardSurfacesHost(OnboardArgs{
		AcceptDefaultDashboardBind: true,
		DiscordMode:                "enabled", DiscordBotTokenFile: tokenPath,
		DiscordGuildID: "123", DiscordChannelID: "456", DiscordOperatorIDs: "789, 987",
		DiscordTestConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 || posts[0] != [2]string{"discord-secret", "456"} {
		t.Fatalf("Discord onboarding post = %#v", posts)
	}
	if _, _, err := ApplyOnboardSurfacesHost(plan); err != nil {
		t.Fatal(err)
	}
	cfg, err := operatorconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Surfaces.Discord.Enabled || cfg.Surfaces.Discord.BotToken != "discord-secret" ||
		strings.Join(cfg.Surfaces.Discord.OperatorIDs, ",") != "789,987" ||
		strings.Join(cfg.Surfaces.Alerts.Blocked, ",") != "dashboard,discord" ||
		strings.Join(cfg.Surfaces.Alerts.Health, ",") != "dashboard,discord" {
		t.Fatalf("Discord config = %+v", cfg.Surfaces)
	}
}

func TestOnboardSurfacesDoesNotPublishDiscordWhenTestPostFails(t *testing.T) {
	home, path := onboardConfigFixture(t)
	tokenPath := filepath.Join(home, "discord-token")
	if err := os.WriteFile(tokenPath, []byte("discord-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldPost := postDiscordOnboardingTest
	t.Cleanup(func() { postDiscordOnboardingTest = oldPost })
	postDiscordOnboardingTest = func(string, string) error { return Domainf("test post refused") }
	_, err := PrepareOnboardSurfacesHost(OnboardArgs{
		AcceptDefaultDashboardBind: true, DiscordMode: "enabled", DiscordBotTokenFile: tokenPath,
		DiscordGuildID: "123", DiscordChannelID: "456", DiscordOperatorIDs: "789", DiscordTestConfirmed: true,
	})
	if err == nil || !strings.Contains(err.Error(), "test post refused") {
		t.Fatalf("test post failure = %v", err)
	}
	cfg, err := operatorconfig.Load(path)
	if err != nil || cfg.Surfaces.Discord.Enabled {
		t.Fatalf("failed Discord plan published config=%+v err=%v", cfg.Surfaces.Discord, err)
	}
}

func TestOnboardTunablesPlanResetsEveryTunableDefaultAndCarriesGitIdentity(t *testing.T) {
	_, path := onboardConfigFixture(t)
	cfg, err := operatorconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Budgets.MaxCorrectionCount = 9
	cfg.Budgets.DispatchRetries = 8
	cfg.Timing.TickIntervalS = 6
	cfg.Timing.HomieIdleTimeoutS = 5
	cfg.Timing.BackupIntervalS = 4
	cfg.Timing.BackupRetain = 3
	cfg.Git.AuthorName = "Old Identity"
	cfg.Git.AuthorEmail = "old@example.invalid"
	if _, err := operatorconfig.WriteAtomic(path, cfg); err != nil {
		t.Fatal(err)
	}
	plan, err := PrepareOnboardTunablesHost(OnboardArgs{
		TunablesConfirmed:        true,
		AcceptDefaultGitIdentity: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defaults := operatorconfig.Default()
	if plan.Desired.Budgets != defaults.Budgets ||
		plan.Desired.Timing.TickIntervalS != defaults.Timing.TickIntervalS ||
		plan.Desired.Timing.HomieIdleTimeoutS != defaults.Timing.HomieIdleTimeoutS ||
		plan.Desired.Timing.BackupIntervalS != defaults.Timing.BackupIntervalS ||
		plan.Desired.Timing.BackupRetain != defaults.Timing.BackupRetain ||
		plan.Desired.Git != defaults.Git {
		t.Fatalf("accepted defaults = %+v", plan.Desired)
	}
	if changed, err := ApplyOnboardTunablesHost(plan); err != nil || !changed {
		t.Fatalf("apply defaults = changed %v err %v", changed, err)
	}
}

func TestOnboardTunablesDBProjectionDoesNotReadHostConfig(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	spine := filepath.Join(home, "spine.db")
	t.Setenv("MC_HOME", home)
	if _, _, err := onboardHome(spine, true); err != nil {
		t.Fatal(err)
	}
	plan, err := PrepareOnboardTunablesHost(OnboardArgs{TimeoutMinutes: 7})
	if err != nil {
		t.Fatal(err)
	}
	projected := plan.Project(OnboardArgs{Spine: spine})
	t.Setenv("MC_HOME", filepath.Join(t.TempDir(), "not-mounted-in-helper"))
	status, _, err := onboardTunablesDB(projected)
	if err != nil || status != "done" {
		t.Fatalf("mountless helper projection = status %q err %v", status, err)
	}
}

func TestOnboardRuntimeSelectionUsesConfiguredBindingCatalog(t *testing.T) {
	_, path := onboardConfigFixture(t)
	cfg, err := operatorconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Runtimes.Credentials["studio"] = cfg.Runtimes.Credentials["claude"]
	cfg.Runtimes.Bindings["studio"] = operatorconfig.RuntimeBinding{
		Harness: "claude-sdk", Model: "studio-model",
		Credential: "studio",
	}
	if _, err := operatorconfig.WriteAtomic(path, cfg); err != nil {
		t.Fatal(err)
	}
	registry, first, err := onboardRoutingRegistry(OnboardArgs{RuntimeBindings: "chatgpt,studio"})
	if err != nil || first != "chatgpt" || registry["chatgpt"] != "codex" ||
		registry["studio"] != "claude-sdk" || len(registry) != 2 {
		t.Fatalf("configured registry = %+v first %q err %v", registry, first, err)
	}
	changed, err := RecordOnboardRuntimeBindings("chatgpt,studio")
	if err != nil || !changed {
		t.Fatalf("record configured selection = changed %v err %v", changed, err)
	}
	cfg, err = operatorconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.Runtimes.EnabledBindings, ",") != "chatgpt,studio" {
		t.Fatalf("recorded runtimes = %+v", cfg.Runtimes)
	}
	if _, _, err := onboardRoutingRegistry(OnboardArgs{RuntimeBindings: "missing"}); err == nil {
		t.Fatal("unknown configured binding entered onboarding routing")
	}
}

func TestOnboardRuntimeSelectionRejectsAnUndefinedBindingID(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_HOME", home)
	path := filepath.Join(home, "config.toml")
	wantUsage := func(t *testing.T, err error, want string) {
		t.Helper()
		var usage *UsageError
		if !errors.As(err, &usage) || usage.Msg != want {
			t.Fatalf("error = %#v, want usage error %q", err, want)
		}
	}
	refusal := func(id string, defined ...string) string {
		if _, allowFake := routing.ActiveRegistry(); allowFake {
			defined = append(defined, "fake")
		}
		slices.Sort(defined)
		return fmt.Sprintf("--runtime-bindings names %q, an undefined binding. The defined bindings are: %s. Use one of these ids.",
			id, strings.Join(defined, ", "))
	}
	unknownEndpoint := refusal("endpoint", "chatgpt", "claude")

	if err := CheckOnboardRuntimeBindings("chatgpt,claude"); err != nil {
		t.Fatalf("reference bindings before config.toml exists: %v", err)
	}
	wantUsage(t, CheckOnboardRuntimeBindings("chatgpt,endpoint"), unknownEndpoint)
	wantUsage(t, CheckOnboardRuntimeBindings("second-endpoint"),
		refusal("second-endpoint", "chatgpt", "claude"))
	wantUsage(t, CheckOnboardRuntimeBindings("chatgpt,chatgpt"), `--runtime-bindings names "chatgpt" twice. Remove the repeated id.`)
	wantUsage(t, CheckOnboardRuntimeBindings("chatgpt,"), "--runtime-bindings contains an empty binding id. Remove the extra comma.")
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("the check wrote config.toml: %v", err)
	}

	if _, err := operatorconfig.Ensure(path); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wantUsage(t, CheckOnboardRuntimeBindings("endpoint"), unknownEndpoint)
	if _, err := RecordOnboardRuntimeBindings("chatgpt,endpoint"); err == nil {
		t.Fatal("an undefined binding was recorded")
	} else {
		wantUsage(t, err, unknownEndpoint)
	}
	if _, _, err := onboardRoutingRegistry(OnboardArgs{RuntimeBindings: "chatgpt,endpoint"}); err == nil {
		t.Fatal("an undefined binding entered onboarding routing")
	} else {
		wantUsage(t, err, unknownEndpoint)
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != string(before) {
		t.Fatalf("a refused selection changed config.toml: %v", err)
	}

	if _, err := operatorconfig.UpdateAtomic(path, enableTestBindings); err != nil {
		t.Fatal(err)
	}
	if err := CheckOnboardRuntimeBindings("chatgpt,endpoint"); err != nil {
		t.Fatalf("a binding of config.toml was refused: %v", err)
	}

	cfg, err := operatorconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	delete(cfg.Runtimes.Bindings, "claude")
	delete(cfg.Runtimes.Credentials, "claude")
	cfg.Runtimes.EnabledBindings = slices.DeleteFunc(cfg.Runtimes.EnabledBindings,
		func(id string) bool { return id == "claude" })
	if _, err := operatorconfig.WriteAtomic(path, cfg); err != nil {
		t.Fatal(err)
	}
	wantUsage(t, CheckOnboardRuntimeBindings("claude"), refusal("claude", "chatgpt", "endpoint"))
}

func TestOnboardRoutingRejectsUnsupportedConnectionBeforePublication(t *testing.T) {
	home, path := onboardConfigFixture(t)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body), `kind = "claude-oauth"`, `kind = "static-env"`, 1))
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := onboardRouting(OnboardArgs{RuntimeBindings: "chatgpt,claude", AcceptDefaultRouting: true}); err == nil {
		t.Fatal("The unsupported connection entered the route catalog.")
	}
	if _, err := os.Lstat(filepath.Join(home, "routing.md")); !os.IsNotExist(err) {
		t.Fatal("The rejected connection published routing.md.")
	}
}

func TestDisableOnboardRuntimeBindingMovesItsRolesToTheFirstEnabledBinding(t *testing.T) {
	home, path := onboardConfigFixture(t)
	if _, err := operatorconfig.UpdateAtomic(path, func(cfg *operatorconfig.Config) error {
		cfg.Runtimes.Credentials["second-endpoint"] = cfg.Runtimes.Credentials["claude"]
		cfg.Runtimes.Bindings["second-endpoint"] = operatorconfig.RuntimeBinding{
			Harness: "claude-sdk", Model: "second-model", Credential: "second-endpoint",
		}
		return enableTestBindings(cfg)
	}); err != nil {
		t.Fatal(err)
	}
	table := `| role | harness | binding |
| --- | --- | --- |
| strategist | claude-sdk | claude |
| arbiter | codex | chatgpt |
| worker | claude-sdk | claude |
| verifier | claude-sdk | endpoint |
| packager | codex | chatgpt |
| homie | claude-sdk | claude |
`
	routingPath := filepath.Join(home, "routing.md")
	if err := os.WriteFile(routingPath, []byte(table), 0o600); err != nil {
		t.Fatal(err)
	}
	moved, err := DisableOnboardRuntimeBinding("claude")
	if err != nil || strings.Join(moved, ",") != "strategist,worker,scribe,homie" {
		t.Fatalf("disable claude = moved %v err %v", moved, err)
	}
	cfg, err := operatorconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.Runtimes.EnabledBindings, ",") != "chatgpt,endpoint" {
		t.Fatalf("enabled after disable = %v", cfg.Runtimes.EnabledBindings)
	}
	written, err := os.ReadFile(routingPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), "| claude |") ||
		!strings.Contains(string(written), "| strategist | codex | chatgpt |") ||
		!strings.Contains(string(written), "| verifier | claude-sdk | endpoint |") {
		t.Fatalf("repaired routing.md:\n%s", written)
	}
	parsed, err := routing.Parse(written, routing.RegistryFromRuntimes(cfg.Runtimes))
	if err != nil || len(parsed.Rows()) != 7 {
		t.Fatalf("repaired table parse = %v rows %d", err, len(parsed.Rows()))
	}

	// A binding the table does not name moves no role.
	moved, err = DisableOnboardRuntimeBinding("second-endpoint")
	if err != nil || len(moved) != 0 {
		t.Fatalf("disable unnamed binding = moved %v err %v", moved, err)
	}

	// When no enabled binding remains the table stays as it is.
	if _, err := DisableOnboardRuntimeBinding("endpoint"); err != nil {
		t.Fatal(err)
	}
	moved, err = DisableOnboardRuntimeBinding("chatgpt")
	if err != nil || len(moved) != 0 {
		t.Fatalf("disable last binding = moved %v err %v", moved, err)
	}
	cfg, err = operatorconfig.Load(path)
	if err != nil || len(cfg.Runtimes.EnabledBindings) != 0 {
		t.Fatalf("enabled after last disable = %v err %v", cfg.Runtimes.EnabledBindings, err)
	}
	unchanged, err := os.ReadFile(routingPath)
	if err != nil || !strings.Contains(string(unchanged), "| arbiter | codex | chatgpt |") {
		t.Fatalf("table changed without an enabled binding:\n%s err %v", unchanged, err)
	}
}
