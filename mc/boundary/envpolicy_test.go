package boundary_test

import (
	"errors"
	"strings"
	"testing"

	"mc/boundary"
)

// The environment policy excludes provider credentials and preserves declared tool secrets.

func proxyBinding() boundary.EnvBinding {
	return boundary.EnvBinding{
		Delivery:               boundary.DeliveryProxy,
		ProviderCredentialKeys: []string{"OPENAI_API_KEY"},
	}
}

func mustPlan(t *testing.T, policy string, guard boundary.EnvGuard, binding boundary.EnvBinding, foreign []string) boundary.EnvPlan {
	t.Helper()
	plan, err := boundary.BuildEnvPlan(policy, guard, binding, foreign)
	if err != nil {
		t.Fatalf("BuildEnvPlan(%q): %v", policy, err)
	}
	return plan
}

func mustReject(t *testing.T, policy string, guard boundary.EnvGuard, binding boundary.EnvBinding, foreign []string, kind boundary.EnvPolicyErrorKind, name string) {
	t.Helper()
	_, err := boundary.BuildEnvPlan(policy, guard, binding, foreign)
	if err == nil {
		t.Fatalf("BuildEnvPlan(%q) accepted; want %s on %s", policy, kind, name)
	}
	var policyErr *boundary.EnvPolicyError
	if !errors.As(err, &policyErr) {
		t.Fatalf("BuildEnvPlan(%q) error %v is not an EnvPolicyError", policy, err)
	}
	if policyErr.Kind != kind || policyErr.Name != name {
		t.Fatalf("BuildEnvPlan(%q) = %s on %q; want %s on %q", policy, policyErr.Kind, policyErr.Name, kind, name)
	}
}

func TestEnvPlanIsBuiltFromTheDeclaredBaseAlone(t *testing.T) {
	t.Run("an absent policy yields an empty plan, never ambient env", func(t *testing.T) {
		for _, policy := range []string{"", "{}"} {
			plan := mustPlan(t, policy, boundary.EnvGuard{}, proxyBinding(), nil)
			if len(plan.Entries()) != 0 {
				t.Fatalf("policy %q produced entries %v; the safe base is empty", policy, plan.Entries())
			}
		}
	})

	t.Run("the plan carries exactly the declared entries in name order", func(t *testing.T) {
		plan := mustPlan(t, `{"ZED_FLAG":"1","ALPHA_FLAG":"two"}`, boundary.EnvGuard{}, proxyBinding(), nil)
		entries := plan.Entries()
		if len(entries) != 2 ||
			entries[0] != (boundary.EnvEntry{Name: "ALPHA_FLAG", Value: "two"}) ||
			entries[1] != (boundary.EnvEntry{Name: "ZED_FLAG", Value: "1"}) {
			t.Fatalf("entries = %v; want the two declared entries sorted by name", entries)
		}
	})
}

func TestEnvPlanRejectsTheShippedFloorInEveryPlane(t *testing.T) {
	// Both the harness policy and the tool policy reject each shipped
	// forbidden key" — the builder is the single mechanism behind both
	// planes, and even a zero-value guard carries the floor.
	for _, name := range []string{"CODEX_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "OPENAI_BASE_URL", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_USE_BEDROCK", "MC_PROVIDER_ACCESS_FILE", "CULLER_PROXY_TOKEN", "CODEX_CA_CERTIFICATE"} {
		mustReject(t, `{"`+name+`":"sk-plant"}`, boundary.EnvGuard{}, proxyBinding(), nil,
			boundary.EnvPolicyForbidden, name)
	}
}

func TestEnvGuardIsExtendOnly(t *testing.T) {
	t.Run("a sentinel added to the operator guard is found and rejected", func(t *testing.T) {
		guard, err := boundary.NewEnvGuard([]string{"SENTINEL_API_KEY"})
		if err != nil {
			t.Fatal(err)
		}
		mustReject(t, `{"SENTINEL_API_KEY":"planted"}`, guard, proxyBinding(), nil,
			boundary.EnvPolicyForbidden, "SENTINEL_API_KEY")
	})

	t.Run("without the extension the sentinel is enumerated but not floor", func(t *testing.T) {
		plan := mustPlan(t, `{"SENTINEL_API_KEY":"planted"}`, boundary.EnvGuard{}, proxyBinding(), nil)
		shaped := plan.APIKeyShaped()
		if len(shaped) != 1 || shaped[0] != "SENTINEL_API_KEY" {
			t.Fatalf("APIKeyShaped = %v; the wildcard scan must still surface the sentinel", shaped)
		}
	})

	t.Run("the guard has no API that can shrink the floor", func(t *testing.T) {
		// Extending with a floor name changes nothing; the floor is compiled
		// into rejection, not stored on the value.
		guard, err := boundary.NewEnvGuard([]string{"CODEX_API_KEY"})
		if err != nil {
			t.Fatal(err)
		}
		mustReject(t, `{"CODEX_API_KEY":"sk-plant"}`, guard, proxyBinding(), nil,
			boundary.EnvPolicyForbidden, "CODEX_API_KEY")
		mustReject(t, `{"ANTHROPIC_API_KEY":"sk-plant"}`, guard, proxyBinding(), nil,
			boundary.EnvPolicyForbidden, "ANTHROPIC_API_KEY")
	})

	t.Run("guard additions must be plausible env names", func(t *testing.T) {
		for _, bad := range []string{"", "1LEADING_DIGIT", "HAS SPACE", "HAS=EQUALS", strings.Repeat("A", 256)} {
			if _, err := boundary.NewEnvGuard([]string{bad}); err == nil {
				t.Errorf("NewEnvGuard accepted %q", bad)
			}
		}
	})
}

func TestEnvPlanPermitsOperatorToolSecrets(t *testing.T) {
	// Operator-managed tool secrets are permitted; *_API_KEY shape
	// alone is classification, never rejection.
	plan := mustPlan(t, `{"MYTOOL_API_KEY":"tool-secret"}`, boundary.EnvGuard{}, proxyBinding(), nil)
	if len(plan.Entries()) != 1 {
		t.Fatalf("entries = %v; a declared tool secret must survive in its plane", plan.Entries())
	}
	if shaped := plan.APIKeyShaped(); len(shaped) != 1 || shaped[0] != "MYTOOL_API_KEY" {
		t.Fatalf("APIKeyShaped = %v; want the tool secret enumerated", plan.APIKeyShaped())
	}
}

func TestEnvPlanForbidsOAuthRefreshMaterial(t *testing.T) {
	t.Run("a refresh-token-shaped name never enters a container", func(t *testing.T) {
		mustReject(t, `{"CODEX_REFRESH_TOKEN":"rt-plant"}`, boundary.EnvGuard{}, proxyBinding(), nil,
			boundary.EnvPolicyForbidden, "CODEX_REFRESH_TOKEN")
	})

	t.Run("a binding's declared provider credential keys are forbidden", func(t *testing.T) {
		mustReject(t, `{"OPENAI_API_KEY":"sk-plant"}`, boundary.EnvGuard{}, proxyBinding(), nil,
			boundary.EnvPolicyForbidden, "OPENAI_API_KEY")
	})

	t.Run("unsupported credential delivery fails", func(t *testing.T) {
		for _, delivery := range []boundary.EnvDelivery{"materialized", "projection", "none"} {
			mustReject(t, `{}`, boundary.EnvGuard{}, boundary.EnvBinding{Delivery: delivery}, nil,
				boundary.EnvPolicyInvalid, "")
		}
	})

	t.Run("another binding's static key never crosses planes", func(t *testing.T) {
		mustReject(t, `{"ENDPOINT_API_KEY":"static-plant"}`, boundary.EnvGuard{}, proxyBinding(),
			[]string{"ENDPOINT_API_KEY"}, boundary.EnvPolicyForbidden, "ENDPOINT_API_KEY")
	})
}

func TestEnvPlanFailsClosedOnInvalidPolicy(t *testing.T) {
	cases := []struct {
		label  string
		policy string
		name   string
	}{
		{"malformed json", `{"A":`, ""},
		{"non-object", `["A"]`, ""},
		{"non-string value", `{"A_FLAG":1}`, ""},
		{"invalid name", `{"1BAD":"x"}`, "1BAD"},
		{"name with equals", `{"BAD=NAME":"x"}`, "BAD=NAME"},
		{"control bytes in value", "{\"A_FLAG\":\"a\\u0000b\"}", "A_FLAG"},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			_, err := boundary.BuildEnvPlan(c.policy, boundary.EnvGuard{}, proxyBinding(), nil)
			if err == nil {
				t.Fatalf("BuildEnvPlan(%q) accepted", c.policy)
			}
			var policyErr *boundary.EnvPolicyError
			if !errors.As(err, &policyErr) {
				t.Fatalf("error %v is not an EnvPolicyError", err)
			}
			if policyErr.Kind != boundary.EnvPolicyInvalid {
				t.Fatalf("kind = %s; want %s", policyErr.Kind, boundary.EnvPolicyInvalid)
			}
			if policyErr.Name != c.name {
				t.Fatalf("name = %q; want %q", policyErr.Name, c.name)
			}
		})
	}

	t.Run("rejection never leaks the planted value", func(t *testing.T) {
		_, err := boundary.BuildEnvPlan(`{"CODEX_API_KEY":"sk-secret-value"}`, boundary.EnvGuard{}, proxyBinding(), nil)
		if err == nil {
			t.Fatal("accepted a floor key")
		}
		if strings.Contains(err.Error(), "sk-secret-value") {
			t.Fatalf("error %q leaks the rejected value", err.Error())
		}
	})
}
