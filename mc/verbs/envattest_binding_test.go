package verbs

import (
	"testing"

	operatorconfig "mc/config"
	"mc/refusal"
	"mc/routing"
)

func TestCandidateEnvPolicyUsesSelectedBindingCatalog(t *testing.T) {
	cases := []struct {
		name, binding, policy string
		wantCode              string
	}{
		{name: "codex provider key", binding: "chatgpt", policy: `{"OPENAI_API_KEY":"metered"}`, wantCode: refusal.CodeEnvForbidden},
		{name: "codex agent identity", binding: "chatgpt", policy: `{"CODEX_ACCESS_TOKEN":"wrong-plane"}`, wantCode: refusal.CodeEnvForbidden},
		{name: "claude provider key", binding: "claude", policy: `{"ANTHROPIC_AUTH_TOKEN":"wrong-plane"}`, wantCode: refusal.CodeEnvForbidden},
		{name: "other provider credential", binding: "chatgpt", policy: `{"ANTHROPIC_AUTH_TOKEN":"foreign"}`, wantCode: refusal.CodeEnvForbidden},
		{name: "profile credential override", binding: "endpoint", policy: `{"ANTHROPIC_AUTH_TOKEN":"subscription"}`, wantCode: refusal.CodeEnvForbidden},
		{name: "all profiles exclude provider keys", binding: "endpoint", policy: `{"ANTHROPIC_API_KEY":"metered"}`, wantCode: refusal.CodeEnvForbidden},
		{name: "unknown binding", binding: "invented", policy: `{}`, wantCode: refusal.CodeAuthBindingInvalid},
	}
	cfg := testBindingConfig()
	catalog := routing.ConfiguredBindings(cfg.Runtimes)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			selected := PrivateDispatchProject{
				HarnessEnvPolicy: tc.policy,
				// The stored profile retains its legacy value across upgrades.
				RuntimeAuthDelivery: "projection",
			}
			got := attestCandidateEnvPolicy(
				selected, catalog[tc.binding], catalog, cfg.Runtimes.ForbiddenEnv,
			)
			if tc.wantCode == "" && got != nil {
				t.Fatalf("unexpected refusal: %+v", got)
			}
			if tc.wantCode != "" && (got == nil || got.Code != tc.wantCode) {
				t.Fatalf("refusal = %+v, want %s", got, tc.wantCode)
			}
		})
	}
}

func TestLegacyStoredProfileRequiresResolvedProxyDelivery(t *testing.T) {
	cfg := testBindingConfig()
	catalog := routing.ConfiguredBindings(cfg.Runtimes)
	profile := PrivateDispatchProject{RuntimeAuthDelivery: "projection", HarnessEnvPolicy: `{}`}
	selected := catalog["chatgpt"]
	if selected.Delivery != "proxy" {
		t.Fatalf("the resolved runtime delivery is %q", selected.Delivery)
	}
	if refusal := attestCandidateEnvPolicy(profile, selected, catalog, cfg.Runtimes.ForbiddenEnv); refusal != nil {
		t.Fatalf("the legacy profile refused proxy delivery: %+v", refusal)
	}
	selected.Delivery = "projection"
	if refusal := attestCandidateEnvPolicy(profile, selected, catalog, cfg.Runtimes.ForbiddenEnv); refusal == nil {
		t.Fatal("the legacy profile authorized provider credential projection")
	}
}

func TestCandidateEnvPolicyUsesConfiguredGuardAdditions(t *testing.T) {
	cfg := operatorconfig.Default()
	cfg.Runtimes.ForbiddenEnv = append(cfg.Runtimes.ForbiddenEnv, "WORKSPACE_FORBIDDEN")
	catalog := routing.ConfiguredBindings(cfg.Runtimes)
	selected := PrivateDispatchProject{
		HarnessEnvPolicy: `{"WORKSPACE_FORBIDDEN":"value"}`,
	}
	got := attestCandidateEnvPolicy(
		selected, catalog["chatgpt"], catalog, cfg.Runtimes.ForbiddenEnv,
	)
	if got == nil || got.Code != refusal.CodeEnvForbidden {
		t.Fatalf("configured guard refusal = %+v", got)
	}
}

func TestCandidateEnvPolicyExcludesCredentialsOfDisabledConnections(t *testing.T) {
	cfg := testBindingConfig()
	cfg.Runtimes.EnabledBindings = []string{"chatgpt", "claude"}
	configured := routing.ConfiguredBindings(cfg.Runtimes)
	got := attestCandidateEnvPolicy(
		PrivateDispatchProject{HarnessEnvPolicy: `{"ANTHROPIC_AUTH_TOKEN":"foreign"}`},
		configured["chatgpt"], configured, cfg.Runtimes.ForbiddenEnv,
	)
	if got == nil || got.Code != refusal.CodeEnvForbidden {
		t.Fatalf("a disabled connection permitted a provider credential: %+v", got)
	}
}
