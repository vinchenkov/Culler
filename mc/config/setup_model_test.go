package config

import "testing"

func TestCodexSetupRequiresASelectedModelBeforeEnablement(t *testing.T) {
	cfg := Default()
	profile := cfg.Runtimes.Bindings["chatgpt"]
	if profile.Model != "" {
		t.Fatal("The initial Codex profile must leave model selection to setup.")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBindingShape("chatgpt", profile, CredentialKindCodexOAuth); err == nil {
		t.Fatal("The dashboard must require a specific model.")
	}
	cfg.Runtimes.EnabledBindings = []string{"chatgpt"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("An enabled profile must name its selected model.")
	}
	profile.Model = "fixture-selected"
	cfg.Runtimes.Bindings["chatgpt"] = profile
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
