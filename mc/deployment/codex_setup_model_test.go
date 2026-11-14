package deployment

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	operatorconfig "mc/config"
)

const codexSetupSuccess = "{\"event\":\"session-start\",\"session_id\":\"fixture\",\"native_file\":\"native.jsonl\"}\n" +
	"{\"event\":\"turn-complete\",\"status\":\"success\",\"output\":\"MC_RUNTIME_AUTH_OK\"}\n"

func codexSetupFixture(t *testing.T) (string, *RuntimeAuthSourceStage, *AdapterNoopVerifier) {
	t.Helper()
	home, stage := selectedRuntimeAuthFixture(t)
	_, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), func(cfg *operatorconfig.Config) error {
		profile := cfg.Runtimes.Bindings["chatgpt"]
		profile.Name, profile.Model = "openai_profile", ""
		cfg.Runtimes.Bindings["chatgpt"] = profile
		cfg.Runtimes.EnabledBindings = slices.DeleteFunc(cfg.Runtimes.EnabledBindings, func(id string) bool { return id == "chatgpt" })
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	verifier := NewAdapterNoopVerifier(home, "fixture-image")
	verifier.RunnerSourceDir = verifierRunnerSource(t, home)
	verifier.SelectDefaultModel = true
	verifier.DefaultModel = func(context.Context, string, string) (string, error) { return "fixture-selected", nil }
	verifier.Access = newProviderAccessFixture(t, "codex", "account", 1).access
	return home, stage, verifier
}

func TestCodexSetupSavesTheVerifiedDefaultAndReusesIt(t *testing.T) {
	home, stage, verifier := codexSetupFixture(t)
	calls := 0
	verifier.Run = func(argv []string, _ string) (runtimeAuthOutput, error) {
		calls++
		model := argumentValue(argv, "--model")
		if model != "fixture-selected" {
			t.Fatalf("The connection test used the wrong model: %v", argv)
		}
		trace := `{"type":"turn_context","payload":{"model":"fixture-selected"}}` + "\n"
		if err := os.WriteFile(filepath.Join(mountSource(argv, "/mc/session"), "native.jsonl"), []byte(trace), 0600); err != nil {
			t.Fatal(err)
		}
		return runtimeAuthOutput{Stdout: []byte(codexSetupSuccess)}, nil
	}
	if _, err := ImportSelectedStagedRuntimeAuth(home, stage, verifier); err != nil {
		t.Fatal(err)
	}
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil || cfg.Runtimes.Bindings["chatgpt"].Model != "fixture-selected" || agentEnabled(cfg, "chatgpt") {
		t.Fatalf("Setup did not save the verified model before enablement: %v, %v", cfg.Runtimes, err)
	}
	if err := verifier.Verify("chatgpt", filepath.Join(home, "refresh-grants")); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("The checks made %d adapter calls.", calls)
	}
}

func TestCodexSetupRequiresSuccessfulUnambiguousModelEvidence(t *testing.T) {
	for name, trace := range map[string]string{
		"missing model":    "{}\n",
		"empty model":      `{"type":"turn_context","payload":{"model":""}}`,
		"invalid model":    `{"type":"turn_context","payload":{"model":" bad-model "}}`,
		"different models": "{\"type\":\"turn_context\",\"payload\":{\"model\":\"first-model\"}}\n{\"type\":\"turn_context\",\"payload\":{\"model\":\"second-model\"}}\n",
		"oversized trace":  strings.Repeat(" ", (1<<20)+1),
	} {
		t.Run(name, func(t *testing.T) {
			home, stage, verifier := codexSetupFixture(t)
			verifier.Run = func(argv []string, _ string) (runtimeAuthOutput, error) {
				if err := os.WriteFile(filepath.Join(mountSource(argv, "/mc/session"), "native.jsonl"), []byte(trace), 0600); err != nil {
					t.Fatal(err)
				}
				return runtimeAuthOutput{Stdout: []byte(codexSetupSuccess)}, nil
			}
			_, err := ImportSelectedStagedRuntimeAuth(home, stage, verifier)
			var modelError *CodexSetupModelError
			if !errors.As(err, &modelError) {
				t.Fatalf("Setup accepted invalid model evidence: %v", err)
			}
			cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
			if err != nil || cfg.Runtimes.Bindings["chatgpt"].Model != "" || agentConnectionAccess(home, "chatgpt", cfg.Runtimes.Credentials["chatgpt"]) != "connected" {
				t.Fatalf("The failed check changed the model or lost the saved connection: %v", err)
			}
		})
	}
	t.Run("no successful reply", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "native.jsonl")
		if err := os.WriteFile(path, []byte(`{"type":"turn_context","payload":{"model":"fixture-model"}}`), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := verifiedCodexModel(path, []byte(`{"event":"session-start"}`)); err == nil {
			t.Fatal("A trace alone proved a connection without a successful reply.")
		}
		rerouted := codexSetupSuccess + `{"type":"item.completed","item":{"type":"error","message":"model rerouted: another model"}}`
		if _, err := verifiedCodexModel(path, []byte(rerouted)); err == nil {
			t.Fatal("A model change proved access to a different model.")
		}
	})
}

func TestCodexSetupPreservesExplicitProfiles(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "named profile", true: "enabled profile"}[enabled], func(t *testing.T) {
			home, stage, verifier := codexSetupFixture(t)
			if _, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), func(cfg *operatorconfig.Config) error {
				profile := cfg.Runtimes.Bindings["chatgpt"]
				profile.Model = "operator-selected"
				if enabled {
					cfg.Runtimes.EnabledBindings = append(cfg.Runtimes.EnabledBindings, "chatgpt")
				} else {
					profile.Name = "Operator profile"
				}
				cfg.Runtimes.Bindings["chatgpt"] = profile
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			verifier.Run = func(argv []string, _ string) (runtimeAuthOutput, error) {
				if argumentValue(argv, "--model") != "operator-selected" {
					t.Fatalf("Setup replaced an explicit model: %v", argv)
				}
				if err := os.WriteFile(filepath.Join(mountSource(argv, "/mc/session"), "native.jsonl"), []byte("{}\n"), 0600); err != nil {
					t.Fatal(err)
				}
				return runtimeAuthOutput{Stdout: []byte(codexSetupSuccess)}, nil
			}
			if _, err := ImportSelectedStagedRuntimeAuth(home, stage, verifier); err != nil {
				t.Fatal(err)
			}
			cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
			if err != nil || cfg.Runtimes.Bindings["chatgpt"].Model != "operator-selected" {
				t.Fatalf("Setup changed the saved model: %v", err)
			}
		})
	}
}

func TestCodexSetupPreservesAConcurrentProfileEdit(t *testing.T) {
	home, stage, verifier := codexSetupFixture(t)
	verifier.Run = func(argv []string, _ string) (runtimeAuthOutput, error) {
		if err := os.WriteFile(filepath.Join(mountSource(argv, "/mc/session"), "native.jsonl"), []byte(`{"type":"turn_context","payload":{"model":"fixture-selected"}}`), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), func(cfg *operatorconfig.Config) error {
			profile := cfg.Runtimes.Bindings["chatgpt"]
			profile.Model = "operator-selected"
			cfg.Runtimes.Bindings["chatgpt"] = profile
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return runtimeAuthOutput{Stdout: []byte(codexSetupSuccess)}, nil
	}
	if _, err := ImportSelectedStagedRuntimeAuth(home, stage, verifier); err == nil || !strings.Contains(err.Error(), "configuration changed") {
		t.Fatalf("Setup overwrote a concurrent profile edit: %v", err)
	}
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil || cfg.Runtimes.Bindings["chatgpt"].Model != "operator-selected" {
		t.Fatalf("Setup lost the operator's model selection: %v", err)
	}
}
