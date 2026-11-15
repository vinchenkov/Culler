package deployment

import (
	"encoding/json"
	"errors"
	operatorconfig "mc/config"
	"mc/routing"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func verifierRunnerSource(t *testing.T, root string) string {
	t.Helper()
	source := filepath.Join(root, "release", "runner")
	for _, rel := range runnerReleaseDirs[1:] {
		if err := os.MkdirAll(filepath.Join(source, filepath.FromSlash(rel)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range runnerReleaseFiles {
		if err := os.WriteFile(filepath.Join(source, filepath.FromSlash(rel)), []byte("// fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return source
}

func stagedVerifierGrant(t *testing.T, stage, binding string) {
	t.Helper()
	var grant any
	switch binding {
	case "chatgpt":
		grant = oauthRuntimeGrant{Credential: binding, Channel: "codex", TokenURL: "https://auth.openai.com/oauth/token",
			ClientID: "app_EMoamEEZ73f0CkXaXp7hrann", RefreshToken: "refresh", AccountID: "account"}
	case "claude":
		grant = oauthRuntimeGrant{Credential: binding, Channel: "claude", TokenURL: "https://platform.claude.com/v1/oauth/token",
			ClientID: "9d1c250a-e61b-44d9-88ed-5944d1962f5e", RefreshToken: "refresh", Scope: "user:inference"}
	case "endpoint", "second-endpoint", "third-endpoint":
		grant = fixtureOAuthGrant(binding, binding+"-refresh")
	}
	if err := writeRuntimeGrant(stage, binding, grant); err != nil {
		t.Fatal(err)
	}
}

func mountSource(argv []string, target string) string {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] != "-v" {
			continue
		}
		if source, destination, ok := strings.Cut(argv[i+1], ":"); ok && strings.TrimSuffix(destination, ":ro") == target {
			return source
		}
	}
	return ""
}

func argumentValue(argv []string, name string) string {
	for index := 0; index+1 < len(argv); index++ {
		if argv[index] == name {
			return argv[index+1]
		}
	}
	return ""
}

func verifierFixture(t *testing.T, harness, mode string, requests int) (*AdapterNoopVerifier, string, *providerAccessFixture) {
	t.Helper()
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	access := newProviderAccessFixture(t, harness, mode, requests)
	verifier := &AdapterNoopVerifier{Home: root, Image: "fixture-image", Catalog: signInCatalog(t), RunnerSourceDir: verifierRunnerSource(t, root), Access: access.access}
	return verifier, stage, access
}

func writeVerifierNativeTrace(t *testing.T, argv []string, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(mountSource(argv, "/mc/session"), "native.jsonl"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func verifierToolOutput() []byte {
	return []byte(strings.Join([]string{
		`{"type":"thread.started","thread_id":"session"}`,
		`{"event":"session-start","session_id":"session","native_file":"native.jsonl"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.started","item":{"id":"tool","type":"command_execution"}}`,
		`{"type":"item.completed","item":{"id":"tool","type":"command_execution","aggregated_output":"MC_LIVE_PROVIDER_TOOL_OK","exit_code":0,"status":"completed"}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"MC_LIVE_PROVIDER_TURN_OK"}}`,
		`{"type":"turn.completed"}`,
		`{"event":"turn-complete","status":"success","output":"MC_LIVE_PROVIDER_TURN_OK"}`,
	}, "\n"))
}

func TestAdapterNoopVerifierUsesOnlyCullerAccessForEveryConnectionMode(t *testing.T) {
	for _, id := range []string{"chatgpt", "claude", "codex-api-key", "claude-api-key"} {
		t.Run(id, func(t *testing.T) {
			cfg := signInCatalogConfig(t)
			source := cfg.Runtimes.Credentials[id]
			cfg.Runtimes.Bindings[id] = operatorconfig.RuntimeBinding{Harness: source.Harness(), Credential: id, Model: "fixture-model", Effort: "high"}
			mode := "account"
			if strings.HasSuffix(id, "api-key") {
				mode = "api-key"
			}
			verifier, stage, access := verifierFixture(t, source.Harness(), mode, 1)
			verifier.Catalog = CatalogFromRuntimes(cfg.Runtimes)
			called := 0
			verifier.Run = func(argv []string, prompt string) (runtimeAuthOutput, error) {
				called++
				if argumentValue(argv, "--effort") != "high" {
					t.Fatal("The model test lost its selected effort.")
				}
				joined := strings.Join(argv, " ")
				if prompt != runtimeAuthNoopPrompt || !strings.Contains(joined, "--user 10002:10002 --cap-drop ALL") || !strings.Contains(joined, "MC_PROVIDER_ACCESS_FILE=/mc/provider-access.json") {
					t.Fatalf("The verifier lost its agent boundary: %v", argv)
				}
				path := mountSource(argv, "/mc/provider-access.json")
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0644 {
					t.Fatal("The container user cannot read its access file.")
				}
				parent, err := os.Stat(filepath.Dir(path))
				if err != nil || parent.Mode().Perm() != 0700 {
					t.Fatal("The host parent does not protect the container access file.")
				}
				for _, mount := range []string{"/mc/session", "/workspace/source"} {
					info, err := os.Stat(mountSource(argv, mount))
					if err != nil || info.Mode().Perm() != 0777 {
						t.Fatal("The container user cannot write its session or workspace.")
					}
				}
				for _, value := range []string{"MC_PRIVATE_BUN=" + RuntimeRoot + "/bin/bun", "MC_PRIVATE_NODE=" + RuntimeRoot + "/bin/node"} {
					if !strings.Contains(joined, value) {
						t.Fatal("The verifier did not select its private runtime.")
					}
				}
				runner := mountSource(argv, RuntimeRoot+"/app/src")
				if runner == verifier.RunnerSourceDir || filepath.Dir(runner) != filepath.Dir(path) {
					t.Fatal("The verifier did not isolate its readable runner projection.")
				}
				for _, relative := range RunnerReleaseFiles() {
					body, err := os.ReadFile(filepath.Join(runner, relative))
					original, sourceErr := os.ReadFile(filepath.Join(verifier.RunnerSourceDir, relative))
					info, statErr := os.Stat(filepath.Join(runner, relative))
					if err != nil || sourceErr != nil || statErr != nil || string(body) != string(original) || info.Mode().Perm() != 0644 {
						t.Fatalf("The container runner file differs from the validated release: %s", relative)
					}
				}
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var config ProviderContainerAccess
				if json.Unmarshal(body, &config) != nil || config != access.Config {
					t.Fatal("The container did not receive the Culler access configuration.")
				}
				for _, forbidden := range []string{"refresh-grants", "auth.json", "--env-file", "CLAUDE_CODE_OAUTH_TOKEN", "OPENAI_API_KEY", "REFRESH_TOKEN_URL", access.Config.Token} {
					if strings.Contains(joined, forbidden) {
						t.Fatalf("The Docker arguments expose %s.", forbidden)
					}
				}
				if strings.Contains(string(body), "access_token") || strings.Contains(string(body), "refresh_token") || strings.Contains(string(body), "PRIVATE KEY") {
					t.Fatal("The container received provider credentials or a private certificate key.")
				}
				writeVerifierNativeTrace(t, argv, "{}\n")
				return runtimeAuthOutput{Stdout: []byte(codexSetupSuccess)}, nil
			}
			if err := verifier.Verify(id, stage); err != nil {
				t.Fatal(err)
			}
			calls := access.recorded()
			if called != 1 || len(calls) != 3 || calls[0]["credential"] != id || calls[0]["max_requests"] != float64(1) || calls[2]["op"] != "profile_revoke" {
				t.Fatalf("The verifier lost its request limit or cancellation: %v", calls)
			}
			assertRuntimeAuthVerifierScratchEmpty(t, filepath.Dir(stage))
		})
	}
}

func TestAdapterNoopVerifierRequiresNativeTraceAndSuccessfulTerminalResponse(t *testing.T) {
	for _, scenario := range []string{"missing trace", "symlink trace", "nested symlink", "no terminal", "failed terminal", "duplicate terminal", "no host request", "too many host requests", "trace token", "output token"} {
		t.Run(scenario, func(t *testing.T) {
			count := 1
			if scenario == "no host request" {
				count = 0
			}
			if scenario == "too many host requests" {
				count = 2
			}
			verifier, stage, access := verifierFixture(t, "codex", "account", count)
			verifier.Run = func(argv []string, _ string) (runtimeAuthOutput, error) {
				output := codexSetupSuccess
				trace := "{}\n"
				if scenario == "trace token" {
					trace = access.Config.Token
				}
				if scenario != "missing trace" {
					writeVerifierNativeTrace(t, argv, trace)
				}
				if scenario == "symlink trace" {
					path := filepath.Join(mountSource(argv, "/mc/session"), "native.jsonl")
					_ = os.Remove(path)
					if err := os.Symlink(filepath.Join(stage, "other"), path); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "nested symlink" {
					outside := t.TempDir()
					if err := os.WriteFile(filepath.Join(outside, "native.jsonl"), []byte("{}"), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outside, filepath.Join(mountSource(argv, "/mc/session"), "link")); err != nil {
						t.Fatal(err)
					}
					output = strings.ReplaceAll(output, "native.jsonl", "link/native.jsonl")
				}
				if scenario == "no terminal" {
					output = strings.Split(output, "\n")[0]
				}
				if scenario == "failed terminal" {
					output = strings.ReplaceAll(output, `"status":"success"`, `"status":"error"`)
				}
				if scenario == "duplicate terminal" {
					output += `{"event":"turn-complete","status":"success","output":"MC_RUNTIME_AUTH_OK"}`
				}
				if scenario == "output token" {
					output += access.Config.Token
				}
				return runtimeAuthOutput{Stdout: []byte(output)}, nil
			}
			if err := verifier.Verify("chatgpt", stage); err == nil {
				t.Fatal("The verifier accepted incomplete or unsafe evidence.")
			}
			calls := access.recorded()
			if calls[len(calls)-1]["op"] != "profile_revoke" {
				t.Fatal("The failed verifier retained its authorization.")
			}
		})
	}
}

func TestAdapterModelVerifierRequiresHostRequestEvidenceAndToolArtifact(t *testing.T) {
	for _, scenario := range []string{"complete", "no host report", "over limit", "no artifact", "wrong artifact", "artifact symlink", "stderr leak", "missing native completion"} {
		t.Run(scenario, func(t *testing.T) {
			count := 2
			if scenario == "no host report" {
				count = 0
			}
			if scenario == "over limit" {
				count = 3
			}
			verifier, stage, access := verifierFixture(t, "codex", "account", count)
			verifier.Run = func(argv []string, prompt string) (runtimeAuthOutput, error) {
				if prompt != liveProviderPrompt {
					t.Fatal("The verifier omitted the tool prompt.")
				}
				writeVerifierNativeTrace(t, argv, "{}\n")
				artifact := filepath.Join(mountSource(argv, "/workspace/source"), liveProviderArtifact)
				if scenario != "no artifact" {
					body := liveProviderToolMarker
					if scenario == "wrong artifact" {
						body = "wrong"
					}
					if err := os.WriteFile(artifact, []byte(body), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "artifact symlink" {
					_ = os.Remove(artifact)
					if err := os.Symlink(filepath.Join(stage, "other"), artifact); err != nil {
						t.Fatal(err)
					}
				}
				out := runtimeAuthOutput{Stdout: verifierToolOutput()}
				if scenario == "stderr leak" {
					out.Stderr = []byte(access.Config.Token)
				}
				if scenario == "missing native completion" {
					out.Stdout = []byte(strings.ReplaceAll(string(out.Stdout), `{"type":"turn.completed"}`, ""))
				}
				// The runner cannot supply trusted request counts.
				out.Stdout = append(out.Stdout, []byte("\n"+`{"event":"model-test-requests","provider_requests":1}`)...)
				return out, nil
			}
			observation, err := verifier.VerifyModel("chatgpt", stage)
			if (err == nil) != (scenario == "complete") {
				t.Fatalf("Unexpected evidence result: %+v %v", observation, err)
			}
			if err == nil && observation.ProviderRequests != 2 {
				t.Fatal("The verifier trusted the runner request count.")
			}
			calls := access.recorded()
			if calls[0]["max_requests"] != float64(2) || calls[len(calls)-1]["op"] != "profile_revoke" {
				t.Fatal("The model test lost its host limit or cancellation.")
			}
		})
	}
}

func TestAdapterVerifierRejectsHostRefusalBeforeModelCall(t *testing.T) {
	verifier, stage, _ := verifierFixture(t, "codex", "account", 0)
	verifier.Access = func(string, string, string, int) (*providerAccessLease, error) {
		return nil, errors.New("host authorization refused")
	}
	verifier.Run = func([]string, string) (runtimeAuthOutput, error) {
		t.Fatal("The refused authorization reached the adapter.")
		return runtimeAuthOutput{}, nil
	}
	if _, err := verifier.VerifyLive("chatgpt", stage, strings.Repeat("e", 64), strings.Repeat("f", 64)); err == nil {
		t.Fatal("The verifier accepted a refused authorization.")
	}
}

func TestAdapterFailureReportsRedactedAdapterOutput(t *testing.T) {
	verifier, stage, access := verifierFixture(t, "codex", "account", 0)
	verifier.Run = func([]string, string) (runtimeAuthOutput, error) {
		return runtimeAuthOutput{Stderr: []byte("401 unauthorized " + access.Config.Token)}, errors.New("exit status 1")
	}
	err := verifier.Verify("chatgpt", stage)
	if err == nil || !strings.Contains(err.Error(), "401 unauthorized") || !strings.Contains(err.Error(), "[redacted]") || strings.Contains(err.Error(), access.Config.Token) {
		t.Fatalf("The error lost its safe diagnostic: %v", err)
	}
}

func TestAdapterFailureWithoutOutputStillNamesTheSilence(t *testing.T) {
	verifier, stage, _ := verifierFixture(t, "codex", "account", 0)
	verifier.Run = func([]string, string) (runtimeAuthOutput, error) {
		return runtimeAuthOutput{}, errors.New("exit status 125")
	}
	if err := verifier.Verify("chatgpt", stage); err == nil || !strings.Contains(err.Error(), "adapter produced no output") {
		t.Fatalf("The error lost its diagnostic: %v", err)
	}
}

func TestImportRuntimeAuthVerifierReceivesOnlyProtectedReferences(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	sources := validRuntimeAuthSources(t, home)
	failure := errors.New("The model check failed.")
	_, err := ImportRuntimeAuth(home, sources, RuntimeAuthVerifyFunc(func(binding, root string) error {
		canonical, err := CanonicalHome(home)
		if err != nil {
			t.Fatal(err)
		}
		if root != filepath.Join(canonical, "refresh-grants") {
			t.Fatal("The verifier received a raw credential stage.")
		}
		body, readErr := os.ReadFile(filepath.Join(root, binding+".json"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, value := range []string{"codex-refresh", "claude-refresh", "access_token", "refresh_token"} {
			if strings.Contains(string(body), value) {
				t.Fatal("The verifier reference contains provider credentials.")
			}
		}
		return failure
	}))
	if !errors.Is(err, failure) {
		t.Fatalf("The model failure disappeared: %v", err)
	}
	for _, connection := range []string{"chatgpt", "claude", "endpoint"} {
		if _, err := ReadProviderCredential(home, connection); err != nil {
			t.Fatalf("The model failure lost the saved native connection: %v", err)
		}
	}
}

func TestRuntimeCatalogRejectsInvalidConnectionsBeforeCredentialUse(t *testing.T) {
	cases := []struct {
		name   string
		change func(*operatorconfig.CredentialSource, *routing.BindingSpec)
	}{
		{"source kind", func(source *operatorconfig.CredentialSource, _ *routing.BindingSpec) { source.Kind = "claude-static" }},
		{"source token address", func(source *operatorconfig.CredentialSource, _ *routing.BindingSpec) {
			source.TokenURL = "https://example.invalid/token"
		}},
		{"source client", func(source *operatorconfig.CredentialSource, _ *routing.BindingSpec) {
			source.ClientID = "another-client"
		}},
		{"source inference address", func(source *operatorconfig.CredentialSource, _ *routing.BindingSpec) {
			source.InferenceURL = "https://example.invalid/inference"
		}},
		{"source base address", func(source *operatorconfig.CredentialSource, _ *routing.BindingSpec) {
			source.BaseURL = "https://example.invalid"
		}},
		{"model", func(_ *operatorconfig.CredentialSource, spec *routing.BindingSpec) { spec.Model = "invalid\nmodel" }},
		{"empty model", func(_ *operatorconfig.CredentialSource, spec *routing.BindingSpec) { spec.Model = "" }},
		{"harness", func(_ *operatorconfig.CredentialSource, spec *routing.BindingSpec) { spec.Harness = "codex" }},
		{"channel", func(_ *operatorconfig.CredentialSource, spec *routing.BindingSpec) { spec.Channel = "none" }},
		{"kind", func(_ *operatorconfig.CredentialSource, spec *routing.BindingSpec) { spec.AuthKind = "claude-static" }},
		{"delivery", func(_ *operatorconfig.CredentialSource, spec *routing.BindingSpec) { spec.Delivery = "env" }},
		{"static key", func(_ *operatorconfig.CredentialSource, spec *routing.BindingSpec) {
			spec.DeclaredStaticKey = "ANTHROPIC_API_KEY"
		}},
		{"base address", func(_ *operatorconfig.CredentialSource, spec *routing.BindingSpec) {
			spec.BaseURL = "https://example.invalid"
		}},
		{"token address", func(_ *operatorconfig.CredentialSource, spec *routing.BindingSpec) {
			spec.TokenURL = "https://example.invalid/token"
		}},
		{"client", func(_ *operatorconfig.CredentialSource, spec *routing.BindingSpec) { spec.ClientID = "another-client" }},
		{"identity", func(_ *operatorconfig.CredentialSource, spec *routing.BindingSpec) { spec.ID = "another-profile" }},
		{"credential path", func(_ *operatorconfig.CredentialSource, spec *routing.BindingSpec) {
			spec.Credential = "../another-connection"
		}},
		{"credential environment", func(_ *operatorconfig.CredentialSource, spec *routing.BindingSpec) { spec.ProviderCredentialKeys = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			catalog := signInCatalog(t)
			source, spec := catalog.Credentials["claude"], catalog.Bindings["claude"]
			tc.change(&source, &spec)
			catalog.Credentials[spec.Credential] = source
			catalog.Bindings["claude"] = spec
			if _, err := catalog.credentialsOf([]string{"claude"}); err == nil {
				t.Fatal("The credential selection accepted an invalid catalog.")
			}
			root := t.TempDir()
			verifier := &AdapterNoopVerifier{
				Catalog: catalog,
				Access: func(string, string, string, int) (*providerAccessLease, error) {
					t.Fatal("The invalid catalog reached a credential request.")
					return nil, nil
				},
				Run: func([]string, string) (runtimeAuthOutput, error) {
					t.Fatal("The invalid catalog reached the adapter.")
					return runtimeAuthOutput{}, nil
				},
			}
			stage := filepath.Join(root, "absent", "grants")
			checks := []func() error{
				func() error { return verifier.Verify("claude", stage) },
				func() error { _, err := verifier.VerifyModel("claude", stage); return err },
				func() error {
					_, err := verifier.VerifyLive("claude", stage, strings.Repeat("a", 64), strings.Repeat("b", 64))
					return err
				},
			}
			for _, check := range checks {
				if err := check(); err == nil || strings.Contains(err.Error(), "runner source") || strings.Contains(err.Error(), "no such file") {
					t.Fatalf("The catalog did not fail before file access: %v", err)
				}
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("The invalid catalog changed the credential folder: %v", err)
			}
		})
	}
}

func TestAdapterVerifierRequiresAModelOutsideDefaultSetup(t *testing.T) {
	catalog := signInCatalog(t)
	spec := catalog.Bindings["chatgpt"]
	spec.Model = ""
	catalog.Bindings["chatgpt"] = spec
	if _, err := catalog.credentialsOf([]string{"chatgpt"}); err != nil {
		t.Fatalf("Setup could not select the sign-in connection: %v", err)
	}
	verifier := &AdapterNoopVerifier{Catalog: catalog, SelectDefaultModel: true}
	if _, err := verifier.VerifyModel("chatgpt", "absent"); err == nil || !strings.Contains(err.Error(), "model is invalid") {
		t.Fatalf("A model test accepted an absent model: %v", err)
	}
	verifier.SelectDefaultModel = false
	if err := verifier.Verify("chatgpt", "absent"); err == nil || !strings.Contains(err.Error(), "model is invalid") {
		t.Fatalf("A connection test accepted an absent model: %v", err)
	}
}
