package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestEmptyConfigUsesTheShippedDefaults(t *testing.T) {
	cfg, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConfigVersion != CurrentVersion ||
		cfg.Surfaces.Dashboard.Bind != "127.0.0.1:7333" ||
		cfg.Surfaces.AgentsView.Bind != "127.0.0.1:7334" ||
		cfg.Surfaces.Wiki.Bind != "127.0.0.1:7335" ||
		cfg.Budgets.ReviewWIPCap != 3 || cfg.Timing.BackupRetain != 12 || cfg.Git.AuthorEmail != "mc@localhost" {
		t.Fatalf("defaults = %+v", cfg)
	}
}

// endpointConfig adds another sign-in connection and profile to the reference catalog.
func endpointConfig(t *testing.T) Config {
	t.Helper()
	cfg := Default()
	profile := cfg.Runtimes.Bindings["chatgpt"]
	profile.Model = "fixture-model"
	cfg.Runtimes.Bindings["chatgpt"] = profile
	cfg.Runtimes.Credentials["endpoint"] = cfg.Runtimes.Credentials["claude"]
	cfg.Runtimes.Bindings["endpoint"] = RuntimeBinding{
		Harness: "claude-sdk", Model: "endpoint-model", Credential: "endpoint",
	}
	cfg.Runtimes.EnabledBindings = []string{"chatgpt", "claude", "endpoint"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestNewConfigHoldsTheSupportedIntegrationsAndReferenceBindings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if changed, err := Ensure(path); err != nil || !changed {
		t.Fatalf("Ensure = changed %v err %v", changed, err)
	}
	created, err := Load(path)
	if err != nil {
		t.Fatalf("the new config.toml is invalid: %v", err)
	}
	for name, cfg := range map[string]Config{"new config.toml": created, "Default": Default()} {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			if len(cfg.Runtimes.EnabledBindings) != 0 {
				t.Fatalf("the template enables %v", cfg.Runtimes.EnabledBindings)
			}
			wantCredentials := map[string]string{
				"chatgpt": CredentialKindCodexOAuth, "claude": CredentialKindClaudeOAuth,
				"codex-api-key": CredentialKindCodexAPIKey, "claude-api-key": CredentialKindClaudeAPIKey,
			}
			if len(cfg.Runtimes.Credentials) != len(wantCredentials) {
				t.Fatalf("credentials = %+v", cfg.Runtimes.Credentials)
			}
			for id, kind := range wantCredentials {
				source := cfg.Runtimes.Credentials[id]
				if source.Kind != kind || source.InferenceURL == "" || (strings.HasSuffix(kind, "-oauth") && (source.TokenURL == "" || source.ClientID == "")) {
					t.Fatalf("subscription integration %s = %+v, want kind %q with all sign-in parameters", id, source, kind)
				}
			}
			wantBindings := map[string]RuntimeBinding{
				"chatgpt": {Harness: "codex", Model: "", Credential: "chatgpt"},
				"claude":  {Harness: "claude-sdk", Model: "claude-opus-4-8", Credential: "claude"},
			}
			if len(cfg.Runtimes.Bindings) != len(wantBindings) {
				t.Fatalf("bindings = %+v", cfg.Runtimes.Bindings)
			}
			for id, want := range wantBindings {
				if got := cfg.Runtimes.Bindings[id]; got != want {
					t.Fatalf("reference binding %s = %+v, want %+v", id, got, want)
				}
			}
			for id, source := range cfg.Runtimes.Credentials {
				if source.Kind == CredentialKindStaticEnv || source.Kind == CredentialKindNone || source.BaseURL != "" {
					t.Fatalf("the template holds endpoint connection %s: %+v", id, source)
				}
			}
		})
	}
}

func TestShippedExampleValidatesWithEveryReferenceBinding(t *testing.T) {
	// Setup resolves the Codex model before it enables the reference profiles.
	enabled := strings.Replace(string(exampleBody), "enabled_bindings = []",
		`enabled_bindings = ["chatgpt", "claude"]`, 1)
	enabled = strings.Replace(enabled, `model = ""`, `model = "fixture-model"`, 1)
	if enabled == string(exampleBody) {
		t.Fatal("the shipped example carries no empty enabled_bindings line")
	}
	cfg, err := Parse([]byte(enabled))
	if err != nil {
		t.Fatalf("shipped example with every reference binding enabled: %v", err)
	}
	if len(cfg.Runtimes.EnabledBindings) != 2 {
		t.Fatalf("enabled bindings = %v", cfg.Runtimes.EnabledBindings)
	}
	for _, id := range cfg.Runtimes.EnabledBindings {
		if source, ok := cfg.Runtimes.CredentialOf(id); !ok || source.Kind == "" {
			t.Fatalf("reference binding %q resolves no credential source", id)
		}
	}
}

// runtimesExample holds two agent profiles on one subscription connection
// and one profile on another sign-in connection.
const runtimesExample = `config_version = 4

[runtimes]
enabled_bindings = ["claude-opus", "claude-sonnet", "endpoint"]

[runtimes.credentials.claude]
kind = "claude-oauth"
token_url = "https://platform.claude.com/v1/oauth/token"
client_id = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

[runtimes.credentials.endpoint]
kind = "claude-oauth"
token_url = "https://platform.claude.com/v1/oauth/token"
client_id = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

[runtimes.bindings.claude-opus]
harness = "claude-sdk"
model = "claude-opus-4-8"
credential = "claude"

[runtimes.bindings.claude-sonnet]
harness = "claude-sdk"
model = "claude-sonnet-5"
credential = "claude"

[runtimes.bindings.endpoint]
harness = "claude-sdk"
model = "endpoint-model"
credential = "endpoint"
`

func TestRuntimesExampleParsesWithoutAHarnessFamilyList(t *testing.T) {
	cfg, err := Parse([]byte(runtimesExample))
	if err != nil {
		t.Fatalf("runtimes example: %v", err)
	}
	if strings.Join(cfg.Runtimes.EnabledBindings, ",") != "claude-opus,claude-sonnet,endpoint" ||
		len(cfg.Runtimes.Bindings) != 3 || len(cfg.Runtimes.Credentials) != 2 {
		t.Fatalf("runtimes example = %+v", cfg.Runtimes)
	}
	withFamilies := strings.Replace(runtimesExample, "[runtimes]\n",
		"[runtimes]\nenabled_harness_families = [\"claude-sdk\"]\n", 1)
	if _, err := Parse([]byte(withFamilies)); err == nil || !strings.Contains(err.Error(), "enabled_harness_families") {
		t.Fatalf("a current-version file with the removed key parsed: %v", err)
	}
}

func TestCredentialSourceKindSelectsDeliveryChannelAndHarness(t *testing.T) {
	for kind, want := range map[string][3]string{
		CredentialKindCodexOAuth:   {"proxy", "codex", "codex"},
		CredentialKindClaudeOAuth:  {"proxy", "claude", "claude-sdk"},
		CredentialKindCodexAPIKey:  {"proxy", "codex", "codex"},
		CredentialKindClaudeAPIKey: {"proxy", "claude", "claude-sdk"},
		CredentialKindStaticEnv:    {"", "", ""},
		CredentialKindNone:         {"", "", ""},
		"api-key":                  {"", "", ""},
	} {
		source := CredentialSource{Kind: kind}
		if got := [3]string{source.Delivery(), source.Channel(), source.Harness()}; got != want {
			t.Fatalf("kind %q = %v, want %v", kind, got, want)
		}
	}
}

func TestConfigurationValidationAcceptsTwoBindingsOnOneCredentialSource(t *testing.T) {
	cfg := endpointConfig(t)
	cfg.Runtimes.Bindings["claude-sonnet"] = RuntimeBinding{
		Harness: "claude-sdk", Model: "claude-sonnet-5", Credential: "claude",
	}
	cfg.Runtimes.EnabledBindings = append(cfg.Runtimes.EnabledBindings, "claude-sonnet")
	if err := cfg.Validate(); err != nil {
		t.Fatalf("two bindings on one credential source: %v", err)
	}
	cfg.Runtimes.Bindings["second-endpoint"] = RuntimeBinding{
		Harness: "claude-sdk", Model: "second-model", Credential: "endpoint",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("two profiles on one sign-in connection: %v", err)
	}
	same := cfg.Runtimes.Bindings["claude"]
	cfg.Runtimes.Bindings["claude-twin"] = same
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Two named agent profiles can use the same execution configuration: %v", err)
	}
}

func TestConfigurationValidationRejectsInvalidBindingShapes(t *testing.T) {
	tests := map[string]struct {
		mutate func(*Config)
		want   string
	}{
		"missing enabled binding": {func(cfg *Config) {
			cfg.Runtimes.EnabledBindings = append(cfg.Runtimes.EnabledBindings, "missing")
		}, "undefined"},
		"unknown harness": {func(cfg *Config) {
			binding := cfg.Runtimes.Bindings["chatgpt"]
			binding.Harness = "shell"
			cfg.Runtimes.Bindings["chatgpt"] = binding
		}, "does not match credential kind"},
		"empty model": {func(cfg *Config) {
			binding := cfg.Runtimes.Bindings["claude"]
			binding.Model = ""
			cfg.Runtimes.Bindings["claude"] = binding
		}, "model is invalid"},
		"codex binding with base URL": {func(cfg *Config) {
			binding := cfg.Runtimes.Bindings["chatgpt"]
			binding.BaseURL = "https://codex.example/v1"
			cfg.Runtimes.Bindings["chatgpt"] = binding
		}, "forbidden provider address"},
		"claude-oauth binding with base URL": {func(cfg *Config) {
			binding := cfg.Runtimes.Bindings["claude"]
			binding.BaseURL = "https://claude.example/v1"
			cfg.Runtimes.Bindings["claude"] = binding
		}, "forbidden provider address"},
		"static connection": {func(cfg *Config) {
			cfg.Runtimes.Credentials["endpoint"] = CredentialSource{Kind: CredentialKindStaticEnv}
		}, "unsupported kind"},
		"credential in base URL": {func(cfg *Config) {
			binding := cfg.Runtimes.Bindings["endpoint"]
			binding.BaseURL = "https://secret@example.com/anthropic"
			cfg.Runtimes.Bindings["endpoint"] = binding
		}, "forbidden provider address"},
		"codex credential on claude-sdk": {func(cfg *Config) {
			binding := cfg.Runtimes.Bindings["claude"]
			binding.Credential = "chatgpt"
			cfg.Runtimes.Bindings["claude"] = binding
		}, "does not match credential kind"},
		"undefined credential": {func(cfg *Config) {
			binding := cfg.Runtimes.Bindings["claude"]
			binding.Credential = "nobody"
			cfg.Runtimes.Bindings["claude"] = binding
		}, "undefined credential"},
		"static credential with token URL": {func(cfg *Config) {
			cfg.Runtimes.Credentials["endpoint"] = CredentialSource{Kind: CredentialKindStaticEnv, TokenURL: "https://example.com/token"}
		}, "unsupported kind"},
		"OAuth credential without client id": {func(cfg *Config) {
			cfg.Runtimes.Credentials["claude"] = CredentialSource{Kind: CredentialKindClaudeOAuth, TokenURL: "https://example.com/token"}
		}, "release provider integration"},
		"unsupported credential kind": {func(cfg *Config) {
			cfg.Runtimes.Credentials["claude"] = CredentialSource{Kind: "api-key"}
		}, "unsupported kind"},
		"invalid credential id": {func(cfg *Config) {
			cfg.Runtimes.Credentials["bad id"] = CredentialSource{Kind: CredentialKindStaticEnv}
		}, "invalid credential id"},
		"URL query": {func(cfg *Config) {
			binding := cfg.Runtimes.Bindings["endpoint"]
			binding.BaseURL = "https://example.com/v1?token=secret"
			cfg.Runtimes.Bindings["endpoint"] = binding
		}, "forbidden provider address"},
		"shrunken guard floor": {func(cfg *Config) {
			cfg.Runtimes.ForbiddenEnv = []string{"CODEX_API_KEY"}
		}, "must retain"},
		"malformed guard": {func(cfg *Config) {
			cfg.Runtimes.ForbiddenEnv = append(cfg.Runtimes.ForbiddenEnv, "BAD-NAME")
		}, "invalid environment name"},
		"model control": {func(cfg *Config) {
			binding := cfg.Runtimes.Bindings["claude"]
			binding.Model = "claude\tmodel"
			cfg.Runtimes.Bindings["claude"] = binding
		}, "model is invalid"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := endpointConfig(t)
			tc.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("accepted invalid runtime config: %+v", cfg.Runtimes)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestRuntimeBindingCatalogSupportsAddRemoveAndRepoint(t *testing.T) {
	cfg := endpointConfig(t)
	delete(cfg.Runtimes.Bindings, "endpoint")
	delete(cfg.Runtimes.Credentials, "endpoint")
	cfg.Runtimes.EnabledBindings = []string{"chatgpt", "claude", "studio"}
	cfg.Runtimes.Credentials["studio"] = cfg.Runtimes.Credentials["claude"]
	cfg.Runtimes.Bindings["studio"] = RuntimeBinding{
		Harness: "claude-sdk", Model: "studio-model",
		Credential: "studio",
	}
	chatgpt := cfg.Runtimes.Bindings["chatgpt"]
	chatgpt.Model = "operator-selected-codex"
	cfg.Runtimes.Bindings["chatgpt"] = chatgpt
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	body, err := Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := roundTrip.Runtimes.Bindings["endpoint"]; ok {
		t.Fatal("removed binding reappeared after round trip")
	}
	if _, ok := roundTrip.Runtimes.Credentials["endpoint"]; ok {
		t.Fatal("removed credential reappeared after round trip")
	}
	if got := roundTrip.Runtimes.Bindings["studio"]; got.Model != "studio-model" || got.Credential != "studio" {
		t.Fatalf("custom binding = %+v", got)
	}
	if got := roundTrip.Runtimes.Bindings["chatgpt"]; got.Model != "operator-selected-codex" {
		t.Fatalf("repointed binding = %+v", got)
	}
}

func TestRuntimeCatalogDigestIsCanonicalAndScopesActivePolicy(t *testing.T) {
	left := endpointConfig(t)
	right := endpointConfig(t)
	right.Runtimes.EnabledBindings = []string{"endpoint", "claude", "chatgpt"}
	right.Runtimes.ForbiddenEnv = []string{"ANTHROPIC_API_KEY", "CODEX_API_KEY"}
	leftDigest, err := RuntimeCatalogDigest(left.Runtimes)
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := RuntimeCatalogDigest(right.Runtimes)
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest || len(leftDigest) != 64 {
		t.Fatalf("canonical digests = %q %q", leftDigest, rightDigest)
	}
	right.Surfaces.Dashboard.Bind = "localhost:7443"
	unrelatedDigest, err := RuntimeCatalogDigest(right.Runtimes)
	if err != nil || unrelatedDigest != leftDigest {
		t.Fatalf("unrelated config changed catalog digest: %q err %v", unrelatedDigest, err)
	}
	disabled := right.Runtimes.Bindings["claude"]
	disabled.Model = "unused-model"
	right.Runtimes.Bindings["claude"] = disabled
	right.Runtimes.EnabledBindings = []string{"chatgpt", "endpoint"}
	left.Runtimes.EnabledBindings = []string{"chatgpt", "endpoint"}
	leftDigest, _ = RuntimeCatalogDigest(left.Runtimes)
	disabledDigest, _ := RuntimeCatalogDigest(right.Runtimes)
	if disabledDigest != leftDigest {
		t.Fatal("disabled binding changed active catalog digest")
	}
	right.Runtimes.Bindings["claude"] = disabled
	active := right.Runtimes.Bindings["chatgpt"]
	active.Model = "operator-model"
	right.Runtimes.Bindings["chatgpt"] = active
	activeDigest, _ := RuntimeCatalogDigest(right.Runtimes)
	if activeDigest == leftDigest {
		t.Fatal("active binding repoint did not change catalog digest")
	}
	right.Runtimes.Bindings["chatgpt"] = left.Runtimes.Bindings["chatgpt"]
	right.Runtimes.Credentials["chatgpt"] = CredentialSource{
		Kind: CredentialKindCodexOAuth, TokenURL: "https://auth.example/oauth/token", ClientID: "other",
	}
	credentialDigest, _ := RuntimeCatalogDigest(right.Runtimes)
	if credentialDigest == leftDigest {
		t.Fatal("active credential source repoint did not change catalog digest")
	}
	noStatic := left
	noStatic.Runtimes.EnabledBindings = []string{"chatgpt"}
	noStatic.Runtimes.Bindings = map[string]RuntimeBinding{"chatgpt": left.Runtimes.Bindings["chatgpt"]}
	noStaticDigest, _ := RuntimeCatalogDigest(noStatic.Runtimes)
	withStatic := noStatic
	withStatic.Runtimes.Bindings = map[string]RuntimeBinding{
		"chatgpt": left.Runtimes.Bindings["chatgpt"], "endpoint": left.Runtimes.Bindings["endpoint"],
	}
	withStaticDigest, _ := RuntimeCatalogDigest(withStatic.Runtimes)
	if noStaticDigest != withStaticDigest {
		t.Fatal("a disabled profile changed the active catalog digest")
	}
}

func TestRuntimeForbiddenEnvBoundsOperatorAdditions(t *testing.T) {
	cfg := Default()
	for i := 0; i < 129; i++ {
		cfg.Runtimes.ForbiddenEnv = append(cfg.Runtimes.ForbiddenEnv, "OPERATOR_GUARD_"+strconv.Itoa(i))
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "128 additions") {
		t.Fatalf("oversized forbidden-env catalog error = %v", err)
	}
}

func TestConfigV1MigrationMaterializesCanonicalRuntimeBindings(t *testing.T) {
	legacy := []byte(`config_version = 1
[runtimes]
enabled_harness_families = ["codex", "claude-sdk"]
enabled_bindings = ["chatgpt", "claude"]
forbidden_env = ["CODEX_API_KEY", "ANTHROPIC_API_KEY"]
`)
	cfg, err := Parse(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConfigVersion != CurrentVersion || !cfg.migrated ||
		len(cfg.Runtimes.Bindings) != 2 || len(cfg.Runtimes.Credentials) != 4 || strings.Join(cfg.Runtimes.EnabledBindings, ",") != "claude" {
		t.Fatalf("migration = version %d migrated %v bindings %d credentials %d",
			cfg.ConfigVersion, cfg.migrated, len(cfg.Runtimes.Bindings), len(cfg.Runtimes.Credentials))
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := Ensure(path)
	if err != nil || !changed {
		t.Fatalf("Ensure migration = changed %v err %v", changed, err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "config_version = 4") ||
		!strings.Contains(string(body), "[runtimes.bindings.chatgpt]") ||
		!strings.Contains(string(body), "[runtimes.credentials.chatgpt]") {
		t.Fatalf("materialized config:\n%s", body)
	}
	changed, err = Ensure(path)
	if err != nil || changed {
		t.Fatalf("second Ensure migration = changed %v err %v", changed, err)
	}
}

func TestConfigV2MigrationRejectsUnsupportedConnections(t *testing.T) {
	for _, kind := range []string{"static-env", "none", "api-key"} {
		body := `config_version = 2
[runtimes]
enabled_bindings = ["custom"]
[runtimes.bindings.custom]
harness = "claude-sdk"
model = "model"
auth_kind = "` + kind + `"
delivery = "materialized"
base_url = "https://example.test"
static_env = "ANTHROPIC_AUTH_TOKEN"
`
		if _, err := Parse([]byte(body)); err == nil {
			t.Fatalf("accepted unsupported legacy connection %s", kind)
		}
	}
}

func TestVersion3CatalogTablesReplaceTheShippedCatalogTogether(t *testing.T) {
	cfg, err := Parse([]byte(`config_version = 3

[runtimes]
enabled_bindings = ["claude"]
forbidden_env = ["CODEX_API_KEY", "ANTHROPIC_API_KEY"]

[runtimes.credentials.claude]
kind = "claude-oauth"
token_url = "https://platform.claude.com/v1/oauth/token"
client_id = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

[runtimes.bindings.claude]
harness = "claude-sdk"
model = "claude-opus-4-8"
credential = "claude"
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Runtimes.Bindings) != 1 || len(cfg.Runtimes.Credentials) != 1 {
		t.Fatalf("operator catalog carries shipped entries: %+v / %+v", cfg.Runtimes.Bindings, cfg.Runtimes.Credentials)
	}
	_, err = Parse([]byte(`config_version = 3

[runtimes.credentials.claude]
kind = "claude-oauth"
token_url = "https://platform.claude.com/v1/oauth/token"
client_id = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
`))
	if err == nil || !strings.Contains(err.Error(), "runtimes.bindings must define") {
		t.Fatalf("credentials without bindings error = %v", err)
	}
	_, err = Parse([]byte(`config_version = 3

[runtimes.bindings.claude]
harness = "claude-sdk"
model = "claude-opus-4-8"
credential = "claude"
`))
	if err == nil || !strings.Contains(err.Error(), "runtimes.credentials must define") {
		t.Fatalf("bindings without credentials error = %v", err)
	}
	_, err = Parse([]byte("config_version = 2\n\n[runtimes.credentials.claude]\nkind = \"claude-oauth\"\n"))
	if err == nil || !strings.Contains(err.Error(), "must not contain runtimes.credentials") {
		t.Fatalf("version 2 with credentials error = %v", err)
	}
}

func TestVersionlessV1ConfigIsMaterializedByEnsure(t *testing.T) {
	legacy := []byte(`[git]
author_name = "Legacy Agent"
author_email = "legacy@example.invalid"
`)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := Ensure(path)
	if err != nil || !changed {
		t.Fatalf("versionless Ensure = changed %v err %v", changed, err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConfigVersion != CurrentVersion || cfg.migrated ||
		cfg.Git.AuthorName != "Legacy Agent" || len(cfg.Runtimes.Bindings) != 2 {
		t.Fatalf("versionless migration = %+v", cfg)
	}
}

func TestV1MigrationPreservesTunablesAndDefersUnselectedModels(t *testing.T) {
	cfg, err := Parse([]byte(`config_version = 1
[runtimes]
enabled_harness_families = ["codex"]
enabled_bindings = ["chatgpt"]
forbidden_env = ["CODEX_API_KEY", "ANTHROPIC_API_KEY"]
[budgets]
review_wip_cap = 7
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Budgets.ReviewWIPCap != 7 ||
		len(cfg.Runtimes.EnabledBindings) != 0 || cfg.Runtimes.Bindings["chatgpt"].Model != "" {
		t.Fatalf("v1 values drifted: %+v", cfg)
	}
}

func TestV1ConfigRejectsCatalogTables(t *testing.T) {
	_, err := Parse([]byte(`config_version = 1
[runtimes.bindings.custom]
harness = "claude-sdk"
model = "custom"
base_url = ""
auth_kind = "static-env"
delivery = "materialized"
token_url = ""
client_id = ""
static_env = "ANTHROPIC_AUTH_TOKEN"
`))
	if err == nil || !strings.Contains(err.Error(), "must not contain") {
		t.Fatalf("v1 mixed-shape error = %v", err)
	}
}

func TestTrackedAndEmbeddedExamplesAreIdenticalAndValid(t *testing.T) {
	rootBody, err := os.ReadFile(filepath.Join("..", "..", "config.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(rootBody) != string(exampleBody) {
		t.Fatal("root and embedded config.example.toml drifted")
	}
	if _, err := Parse(exampleBody); err != nil {
		t.Fatal(err)
	}
}

func TestConfigRejectsUnknownKeysAndUnsafeSurfacePolicy(t *testing.T) {
	for name, body := range map[string]string{
		"unknown":          "mystery = true\n",
		"remote_no_token":  "[surfaces.dashboard]\nbind = \"0.0.0.0:7333\"\n",
		"partial_discord":  "[surfaces.discord]\nenabled = true\n",
		"disabled_secret":  "[surfaces.discord]\nbot_token = \"secret\"\n",
		"unknown_alert":    " [surfaces.alerts]\nhealth = [\"email\"]\n",
		"invalid_git_mail": "[git]\nauthor_email = \"not-an-email\"\n",
		"forged_git_name":  "[git]\nauthor_name = \"Cull\\ner\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(body)); err == nil {
				t.Fatalf("accepted invalid config %q", body)
			}
		})
	}
}

func TestViewerBindsAcceptOnlyLoopbackHosts(t *testing.T) {
	for _, key := range []string{"agentsview", "wiki"} {
		for _, bind := range []string{"127.0.0.1:7340", "[::1]:7340", "localhost:7340"} {
			body := "[surfaces." + key + "]\nbind = \"" + bind + "\"\n"
			if _, err := Parse([]byte(body)); err != nil {
				t.Errorf("surfaces.%s.bind %q: %v", key, bind, err)
			}
		}
		const loopback = "must use a loopback host (127.0.0.1, ::1, or localhost)"
		for bind, want := range map[string]string{
			"0.0.0.0:7340":     loopback,
			"[::]:7340":        loopback,
			"127.0.0.2:7340":   loopback,
			"192.0.2.10:7340":  loopback,
			"example.com:7340": loopback,
			"7340":             "must be host:port",
			":7340":            "must be host:port",
			"":                 "must be host:port",
			"127.0.0.1:0":      "port must be 1..65535",
			"127.0.0.1:65536":  "port must be 1..65535",
		} {
			body := "[surfaces." + key + "]\nbind = \"" + bind + "\"\n"
			_, err := Parse([]byte(body))
			if want = "surfaces." + key + ".bind " + want; err == nil || err.Error() != want {
				t.Errorf("bind %q error = %v, want %q", bind, err, want)
			}
		}
	}
}

func TestDashboardBindErrorsNameTheDashboardKey(t *testing.T) {
	for bind, want := range map[string]string{
		"7333":            "surfaces.dashboard.bind must be host:port",
		"127.0.0.1:99999": "surfaces.dashboard.bind port must be 1..65535",
		"0.0.0.0:7333":    "surfaces.dashboard.auth_token is required for non-loopback bind 0.0.0.0:7333",
	} {
		_, err := Parse([]byte("[surfaces.dashboard]\nbind = \"" + bind + "\"\n"))
		if err == nil || err.Error() != want {
			t.Errorf("dashboard bind %q error = %v, want %q", bind, err, want)
		}
	}
}

func TestDashboardAndViewerBindsUseThreeDifferentPorts(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"dashboard on the AgentsView default": {
			"[surfaces.dashboard]\nbind = \"127.0.0.1:7334\"\n",
			"surfaces.dashboard.bind and surfaces.agentsview.bind must use different ports",
		},
		"remote dashboard on the wiki port": {
			"[surfaces.dashboard]\nbind = \"0.0.0.0:7335\"\nauth_token = \"dashboard-secret\"\n",
			"surfaces.dashboard.bind and surfaces.wiki.bind must use different ports",
		},
		"wiki on the AgentsView port with another host": {
			"[surfaces.wiki]\nbind = \"localhost:7334\"\n",
			"surfaces.agentsview.bind and surfaces.wiki.bind must use different ports",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.body))
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
	moved, err := Parse([]byte("[surfaces.dashboard]\nbind = \"127.0.0.1:7334\"\n" +
		"[surfaces.agentsview]\nbind = \"127.0.0.1:7433\"\n[surfaces.wiki]\nbind = \"[::1]:7333\"\n"))
	if err != nil {
		t.Fatalf("three different ports: %v", err)
	}
	if moved.Surfaces.AgentsView.Bind != "127.0.0.1:7433" || moved.Surfaces.Wiki.Bind != "[::1]:7333" {
		t.Fatalf("viewer binds = %+v", moved.Surfaces)
	}
}

func TestCurrentVersionFileWithoutViewerBindsLoadsTheDefaultsUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "config_version = 4\n[surfaces.dashboard]\nbind = \"127.0.0.1:7333\"\nauth_token = \"dashboard-secret\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.migrated || cfg.Surfaces.AgentsView.Bind != "127.0.0.1:7334" || cfg.Surfaces.Wiki.Bind != "127.0.0.1:7335" {
		t.Fatalf("loaded surfaces = %+v, migrated %v", cfg.Surfaces, cfg.migrated)
	}
	if changed, err := Ensure(path); err != nil || changed {
		t.Fatalf("Ensure = changed %v err %v", changed, err)
	}
	saved, err := os.ReadFile(path)
	if err != nil || string(saved) != body {
		t.Fatalf("Ensure rewrote the file: %s, %v", saved, err)
	}
	updated, err := UpdateAtomic(path, func(cfg *Config) error {
		cfg.Git.AuthorName = "Viewer Writer"
		return nil
	})
	if err != nil || !updated {
		t.Fatalf("UpdateAtomic = %v, %v", updated, err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	compact := strings.Join(strings.Fields(string(written)), " ")
	for _, want := range []string{
		`[surfaces.agentsview] bind = "127.0.0.1:7334"`, `[surfaces.wiki] bind = "127.0.0.1:7335"`,
	} {
		if !strings.Contains(compact, want) {
			t.Fatalf("written config lacks %q:\n%s", want, written)
		}
	}
}

func TestConfigAtomicUpdatesSerializeDisjointSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if _, err := WriteAtomic(path, Default()); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, mutate := range []func(*Config){
		func(cfg *Config) { cfg.Git.AuthorName = "Serialized Roles" },
		func(cfg *Config) { cfg.Surfaces.Dashboard.Bind = "localhost:7443" },
	} {
		wg.Add(1)
		go func(mutate func(*Config)) {
			defer wg.Done()
			<-start
			_, err := UpdateAtomic(path, func(cfg *Config) error {
				mutate(cfg)
				return nil
			})
			errs <- err
		}(mutate)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Git.AuthorName != "Serialized Roles" ||
		cfg.Surfaces.Dashboard.Bind != "localhost:7443" {
		t.Fatalf("concurrent updates lost a section: %+v", cfg)
	}
}

func TestConfigCASRefusesAStalePlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := Default()
	if _, err := WriteAtomic(path, original); err != nil {
		t.Fatal(err)
	}
	updated := original
	updated.Git.AuthorName = "First Writer"
	if _, err := WriteAtomicCAS(path, original, updated); err != nil {
		t.Fatal(err)
	}
	stale := original
	stale.Surfaces.Dashboard.Bind = "localhost:7443"
	if _, err := WriteAtomicCAS(path, original, stale); err == nil ||
		!strings.Contains(err.Error(), "changed after onboarding") {
		t.Fatalf("stale CAS error = %v", err)
	}
}

func TestConfigAcceptsRemoteDashboardAndCompleteDiscord(t *testing.T) {
	cfg := Default()
	cfg.Surfaces.Dashboard = Dashboard{Bind: "0.0.0.0:7333", AuthToken: "dashboard-secret"}
	cfg.Surfaces.Discord = Discord{
		Enabled: true, BotToken: "discord-secret", GuildID: "123",
		ChannelID: "456", OperatorIDs: []string{"789"},
	}
	cfg.Surfaces.Alerts.Blocked = []string{"dashboard", "discord"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestConfigAtomicRoundTripIsOwnerOnlyAndIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := Default()
	changed, err := WriteAtomic(path, cfg)
	if err != nil || !changed {
		t.Fatalf("first write = changed %v err %v", changed, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %v err %v", info.Mode(), err)
	}
	got, err := Load(path)
	if err != nil || got.Surfaces.Dashboard.Bind != cfg.Surfaces.Dashboard.Bind {
		t.Fatalf("round trip = %+v err %v", got, err)
	}
	changed, err = WriteAtomic(path, cfg)
	if err != nil || changed {
		t.Fatalf("replay = changed %v err %v", changed, err)
	}
}

func TestConfigLoadRefusesSymlinkAndWidenedMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link); err == nil || !strings.Contains(err.Error(), "owner-only") {
		t.Fatalf("symlink error = %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(target, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(link, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link); err == nil || !strings.Contains(err.Error(), "owner-only") {
		t.Fatalf("mode error = %v", err)
	}
}

// The operator extension has exactly this shape:
//
//	additional_blocked_patterns = [
//	  { kind = "component", pattern = ".terraform.d" },
//	  { kind = "basename-glob", pattern = "*.agekey" },
//	]
//
// It was declared here as a bare []string, which cannot express `kind` at all —
// so the one shape an operator could write was the one shape nothing could
// consume. Config and the boundary validator must agree, so the same rules that
// govern a BlockPattern govern this table: no separators or wildcards in a
// component, no `?`/class/escape syntax and lowercase only in a glob.
func TestAdditionalBlockedPatternsAreTaggedAndValidated(t *testing.T) {
	good := []byte(`config_version = 2

[container]
additional_blocked_patterns = [
  { kind = "component", pattern = ".terraform.d" },
  { kind = "basename-glob", pattern = "*.agekey" },
]
`)
	cfg, err := Parse(good)
	if err != nil {
		t.Fatalf("the documented example was refused: %v", err)
	}
	if len(cfg.Container.AdditionalBlockedPatterns) != 2 ||
		cfg.Container.AdditionalBlockedPatterns[0].Kind != "component" ||
		cfg.Container.AdditionalBlockedPatterns[0].Pattern != ".terraform.d" ||
		cfg.Container.AdditionalBlockedPatterns[1].Kind != "basename-glob" ||
		cfg.Container.AdditionalBlockedPatterns[1].Pattern != "*.agekey" {
		t.Fatalf("parsed additions = %+v", cfg.Container.AdditionalBlockedPatterns)
	}
	for name, table := range map[string]string{
		"unknown kind":         `{ kind = "regex", pattern = "x" }`,
		"component with glob":  `{ kind = "component", pattern = "*.pem" }`,
		"component with slash": `{ kind = "component", pattern = "a/b" }`,
		"glob with class":      `{ kind = "basename-glob", pattern = "[abc]*" }`,
		"uppercase glob":       `{ kind = "basename-glob", pattern = "*.PEM" }`,
		"empty pattern":        `{ kind = "component", pattern = "" }`,
		"missing kind":         `{ pattern = ".terraform.d" }`,
	} {
		body := []byte("config_version = 2\n\n[container]\nadditional_blocked_patterns = [" + table + "]\n")
		if _, err := Parse(body); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	// Duplicates are refused the way every other operator list here is.
	dup := []byte(`config_version = 2

[container]
additional_blocked_patterns = [
  { kind = "component", pattern = ".terraform.d" },
  { kind = "component", pattern = ".terraform.d" },
]
`)
	if _, err := Parse(dup); err == nil {
		t.Fatal("duplicate additions were accepted")
	}
}

func TestRemovedBudgetKeyConversion(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			body := "config_version = " + strconv.Itoa(version) + "\n[budgets]\nsaturation_streak = 3\nreview_wip_cap = 7\n"
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			changed, err := Ensure(path)
			if err != nil || !changed {
				t.Fatalf("Ensure = %v, %v", changed, err)
			}
			cfg, err := Load(path)
			if err != nil || cfg.ConfigVersion != CurrentVersion || cfg.migrated || cfg.Budgets.ReviewWIPCap != 7 {
				t.Fatalf("converted config = %+v, %v", cfg, err)
			}
			saved, err := os.ReadFile(path)
			if err != nil || strings.Contains(string(saved), "saturation_streak") {
				t.Fatalf("removed key remains: %s, %v", saved, err)
			}
			changed, err = Ensure(path)
			if err != nil || changed {
				t.Fatalf("second Ensure = %v, %v", changed, err)
			}
		})
	}
}

func TestRemovedBudgetKeyConversionRetainsStrictValidation(t *testing.T) {
	for _, body := range []string{
		"config_version = 3\n[budgets]\nsaturation_streak = 3\nreview_wip_caap = 7\n",
		"config_version = 4\n[budgets]\nsaturation_streak = 3\n",
		"config_version = 3\n[runtimes]\nenabled_harness_families = []\n",
	} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		changed, err := Ensure(path)
		if err == nil || !strings.Contains(err.Error(), "unknown key") || changed {
			t.Fatalf("Ensure = %v, %v", changed, err)
		}
		saved, err := os.ReadFile(path)
		if err != nil || string(saved) != body {
			t.Fatal("the rejected configuration changed")
		}
	}
}

func TestEvaluationCaptureDefaultsAndRoundTrip(t *testing.T) {
	for _, body := range []string{"", "config_version = 4\n"} {
		cfg, err := Parse([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Development.CaptureEvaluationCases {
			t.Fatal("The default enabled capture.")
		}
	}
	cfg := Default()
	cfg.Development.CaptureEvaluationCases = true
	path := filepath.Join(t.TempDir(), "config.toml")
	if _, err := WriteAtomic(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || !loaded.Development.CaptureEvaluationCases {
		t.Fatalf("The flag changed: %v", err)
	}
}
