package verbs

import (
	"errors"

	"mc/boundary"
	"mc/refusal"
	"mc/routing"
)

// attestCandidateEnvPolicy validates both declared env planes before any
// pipeline lease claim. The zero-value guard is the floor that no operator
// can shrink; the operator's forbidden_env additions extend it. The provider
// credential keys of the selected binding come from the binding catalog. The refusal names
// only the enumerated field identifier, never a value.
func attestCandidateEnvPolicy(
	selected PrivateDispatchProject,
	spec routing.BindingSpec,
	catalog map[string]routing.BindingSpec,
	forbiddenEnv []string,
) *refusal.Refusal {
	if spec.ID == "" {
		return &refusal.Refusal{Code: refusal.CodeAuthBindingInvalid, Field: refusal.FieldNone, Summary: refusal.SummaryUnparsable}
	}
	additionalGuards := make([]string, 0, len(forbiddenEnv))
	for _, name := range forbiddenEnv {
		if name != "CODEX_API_KEY" && name != "ANTHROPIC_API_KEY" {
			additionalGuards = append(additionalGuards, name)
		}
	}
	guard, err := boundary.NewEnvGuard(additionalGuards)
	if err != nil {
		return &refusal.Refusal{Code: refusal.CodeEnvInvalid, Field: refusal.FieldEnvName, Summary: refusal.SummaryUnparsable}
	}
	binding := boundary.EnvBinding{
		Delivery:               boundary.EnvDelivery(spec.Delivery),
		ProviderCredentialKeys: spec.ProviderCredentialKeys,
		DeclaredStaticKey:      spec.DeclaredStaticKey,
	}
	for _, policy := range []string{selected.HarnessEnvPolicy, selected.ToolEnvPolicy} {
		if _, err := boundary.BuildEnvPlan(policy, guard, binding, nil); err != nil {
			code, summary := refusal.CodeEnvInvalid, refusal.SummaryUnparsable
			var policyErr *boundary.EnvPolicyError
			if errors.As(err, &policyErr) && policyErr.Kind == boundary.EnvPolicyForbidden {
				code, summary = refusal.CodeEnvForbidden, refusal.SummaryForbidden
			}
			// Authority stays empty: the env codes are ClassCandidate by
			// their own table entry, so authority is not one of their
			// dimensions.
			return &refusal.Refusal{Code: code, Field: refusal.FieldEnvName, Summary: summary}
		}
	}
	return nil
}
