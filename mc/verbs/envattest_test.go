package verbs

import (
	"os"
	"strings"
	"testing"

	"mc/dispatch"
	"mc/refusal"
)

// The verb validates both declared env planes against the shipped forbidden
// env floor BEFORE any pipeline lease claim. The refusal is candidate-owned,
// blocks the subject with the stable confinement reason, and never leaks the
// planted value.
func TestDispatchForbiddenEnvPolicyNeverClaimsOrSpawns(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	dvExec(t, db, `UPDATE sandbox_profiles SET harness_env_policy=? WHERE id='default'`,
		`{"ANTHROPIC_API_KEY":"sk-planted-secret"}`)

	prepared := dfPrepare(t, db, dfRequestID)
	attested, err := dispatchAttest(os.Getenv("MC_HOME"), prepared)
	if err != nil {
		t.Fatalf("dispatchAttest: %v", err)
	}
	if attested.refusal == nil || attested.refusal.Code != refusal.CodeEnvForbidden {
		t.Fatalf("attestation refusal = %+v, want %s", attested.refusal, refusal.CodeEnvForbidden)
	}
	if attested.refusal.Authority != "" {
		t.Fatalf("env codes fix their own class; authority = %q, want empty", attested.refusal.Authority)
	}
	eff := dfCommit(t, db, prepared, attested)
	dfAssertInert(t, db, eff)
	if eff["action"] == "spawn" {
		t.Fatalf("forbidden env still spawned: %v", eff)
	}
	if n := dfInt(t, db, `SELECT COUNT(*) FROM tasks WHERE id=1 AND blocked=1`); n != 1 {
		t.Fatalf("forbidden env blocked %d subject rows, want one", n)
	}
	var reason string
	if err := db.QueryRow(`SELECT blocked_reason FROM tasks WHERE id=1`).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != "confinement:env.forbidden" {
		t.Fatalf("blocked_reason = %q, want confinement:env.forbidden", reason)
	}
	if strings.Contains(reason, "sk-planted-secret") {
		t.Fatalf("blocked_reason leaks the planted value: %q", reason)
	}
}

func TestDispatchEnvPolicyPlaneRules(t *testing.T) {
	attempt := func(t *testing.T, plane, policy string) *attestedDispatch {
		t.Helper()
		db := dvSpine(t)
		dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
		dvExec(t, db, `UPDATE sandbox_profiles SET `+plane+`=? WHERE id='default'`, policy)
		prepared := dfPrepare(t, db, dfRequestID)
		attested, err := dispatchAttest(os.Getenv("MC_HOME"), prepared)
		if err != nil {
			t.Fatalf("dispatchAttest: %v", err)
		}
		return &attested
	}

	t.Run("the tool plane rejects the shipped floor too", func(t *testing.T) {
		attested := attempt(t, "tool_env_policy", `{"CODEX_API_KEY":"sk-planted"}`)
		if attested.refusal == nil || attested.refusal.Code != refusal.CodeEnvForbidden {
			t.Fatalf("refusal = %+v, want %s", attested.refusal, refusal.CodeEnvForbidden)
		}
	})

	t.Run("a malformed policy refuses env.invalid before claim", func(t *testing.T) {
		attested := attempt(t, "harness_env_policy", `{"A":`)
		if attested.refusal == nil || attested.refusal.Code != refusal.CodeEnvInvalid {
			t.Fatalf("refusal = %+v, want %s", attested.refusal, refusal.CodeEnvInvalid)
		}
	})

	t.Run("refresh material never enters a projection profile", func(t *testing.T) {
		attested := attempt(t, "harness_env_policy", `{"CODEX_REFRESH_TOKEN":"rt-planted"}`)
		if attested.refusal == nil || attested.refusal.Code != refusal.CodeEnvForbidden {
			t.Fatalf("refusal = %+v, want %s", attested.refusal, refusal.CodeEnvForbidden)
		}
	})

	t.Run("selected Codex binding forbids its provider key", func(t *testing.T) {
		attested := attempt(t, "harness_env_policy", `{"OPENAI_API_KEY":"metered"}`)
		if attested.refusal == nil || attested.refusal.Code != refusal.CodeEnvForbidden {
			t.Fatalf("refusal = %+v, want %s", attested.refusal, refusal.CodeEnvForbidden)
		}
	})

	t.Run("selected Codex profile excludes other provider credentials", func(t *testing.T) {
		attested := attempt(t, "harness_env_policy", `{"ANTHROPIC_AUTH_TOKEN":"foreign"}`)
		if attested.refusal == nil || attested.refusal.Code != refusal.CodeEnvForbidden {
			t.Fatalf("refusal = %+v, want %s", attested.refusal, refusal.CodeEnvForbidden)
		}
	})

	t.Run("the sentinel is enumerated but the shipped floor does not grow", func(t *testing.T) {
		attested := attempt(t, "harness_env_policy", `{"SENTINEL_API_KEY":"planted"}`)
		if attested.refusal != nil {
			t.Fatalf("sentinel without an operator guard extension refused: %+v", attested.refusal)
		}
	})

	t.Run("a declared tool secret survives in its plane", func(t *testing.T) {
		attested := attempt(t, "tool_env_policy", `{"MYTOOL_API_KEY":"tool-secret"}`)
		if attested.refusal != nil {
			t.Fatalf("declared tool secret refused: %+v", attested.refusal)
		}
	})
}

func TestDispatchSignInProfileRejectsDeclaredProviderToken(t *testing.T) {
	db := dvSpine(t)
	// seeded selects Worker, which the test routing table binds to the endpoint.
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	dvExec(t, db, `UPDATE sandbox_profiles SET harness_env_policy=? WHERE id='default'`,
		`{"ANTHROPIC_AUTH_TOKEN":"subscription"}`)
	prepared := dfPrepare(t, db, dfRequestID)
	attested, err := dispatchAttest(os.Getenv("MC_HOME"), prepared)
	if err != nil {
		t.Fatal(err)
	}
	if attested.refusal == nil || attested.refusal.Code != refusal.CodeEnvForbidden {
		t.Fatalf("The sign-in profile accepted a declared provider token: %+v", attested.refusal)
	}
}
