package config

import (
	"strings"
	"testing"
)

func TestConfigurationRejectsUnsupportedConnections(t *testing.T) {
	for _, kind := range []string{"static-env", "none", "api-key", "custom"} {
		t.Run(kind, func(t *testing.T) {
			cfg := Default()
			cfg.Runtimes.Credentials["custom"] = CredentialSource{Kind: kind}
			if _, err := Marshal(cfg); err == nil {
				t.Fatal("The unsupported connection passed validation.")
			}
		})
	}
}

func TestConfigurationRestrictsProviderDestinations(t *testing.T) {
	for _, id := range []string{"chatgpt", "claude"} {
		for _, field := range []string{"inference_url", "token_url", "client_id", "base_url"} {
			t.Run(id+"/"+field, func(t *testing.T) {
				cfg := Default()
				source := cfg.Runtimes.Credentials[id]
				switch field {
				case "inference_url":
					source.InferenceURL = "https://untrusted.example/model"
				case "token_url":
					source.TokenURL = "https://untrusted.example/token"
				case "client_id":
					source.ClientID = "untrusted-client"
				case "base_url":
					source.BaseURL = "https://untrusted.example"
				}
				cfg.Runtimes.Credentials[id] = source
				if _, err := Marshal(cfg); err == nil {
					t.Fatal("The custom provider destination passed validation.")
				}
			})
		}
	}
}

func TestConfigurationRejectsCredentialValues(t *testing.T) {
	for _, field := range []string{"token", "api_key", "access_token", "refresh_token"} {
		body := strings.Replace(string(exampleBody), "[runtimes.credentials.chatgpt]", "[runtimes.credentials.chatgpt]\n"+field+` = "supplied-secret"`, 1)
		if _, err := Parse([]byte(body)); err == nil || strings.Contains(err.Error(), "supplied-secret") {
			t.Fatalf("The credential field did not fail safely: %v", err)
		}
	}
}

func TestConfigurationRejectsEmptyProviderAddressFields(t *testing.T) {
	for _, table := range []string{"credentials", "bindings"} {
		header := "[runtimes." + table + ".chatgpt]"
		body := strings.Replace(string(exampleBody), header, header+"\nbase_url = \"\"", 1)
		if _, err := Parse([]byte(body)); err == nil {
			t.Fatal("The forbidden address field passed validation.")
		}
	}
}

func TestPublicConnectionValidationRejectsUnsafeIdentifiers(t *testing.T) {
	cfg := Default()
	source := cfg.Runtimes.Credentials["claude"]
	profile := cfg.Runtimes.Bindings["claude"]
	for _, id := range []string{"", "../credential", "/tmp/credential", "bad id"} {
		if err := ValidateCredentialSource(id, source); err == nil {
			t.Fatal("The connection accepted an unsafe identifier.")
		}
		if err := ValidateBindingShape(id, profile, source.Kind); err == nil {
			t.Fatal("The profile accepted an unsafe identifier.")
		}
		changed := profile
		changed.Credential = id
		if err := ValidateBindingShape("profile", changed, source.Kind); id != "" && err == nil {
			t.Fatal("The profile accepted an unsafe connection identifier.")
		}
	}
}

func TestProfileEffortRoundTripAndHarnessValidation(t *testing.T) {
	for _, id := range []string{"chatgpt", "claude"} {
		cfg := Default()
		profile := cfg.Runtimes.Bindings[id]
		profile.Model = "fixture-model"
		for _, effort := range append([]string{""}, EffortLevels(profile.Harness)...) {
			profile.Effort = effort
			cfg.Runtimes.Bindings[id] = profile
			body, err := Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := Parse(body)
			if err != nil || restored.Runtimes.Bindings[id].Effort != effort {
				t.Fatalf("The effort changed: %s %v", effort, err)
			}
		}
		for _, effort := range []string{"HIGH", "unknown", "high\n", "high\""} {
			profile.Effort = effort
			cfg.Runtimes.Bindings[id] = profile
			if _, err := Marshal(cfg); err == nil {
				t.Fatal("The profile accepted an invalid effort.")
			}
		}
	}
	cfg := Default()
	profile := cfg.Runtimes.Bindings["claude"]
	profile.Effort = "ultra"
	cfg.Runtimes.Bindings["claude"] = profile
	if _, err := Marshal(cfg); err == nil {
		t.Fatal("The profile accepted another harness's effort.")
	}
}
