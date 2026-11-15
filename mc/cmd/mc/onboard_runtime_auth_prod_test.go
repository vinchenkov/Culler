//go:build !test_fake_routing

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	operatorconfig "mc/config"
	"mc/deployment"
)

// writeEndpointFixtureConfig adds a second Claude Code connection to the template.
func writeEndpointFixtureConfig(t *testing.T, home string, enabled ...string) {
	t.Helper()
	cfg := operatorconfig.Default()
	profile := cfg.Runtimes.Bindings["chatgpt"]
	profile.Model = "fixture-model"
	cfg.Runtimes.Bindings["chatgpt"] = profile
	cfg.Runtimes.Credentials["endpoint"] = cfg.Runtimes.Credentials["claude"]
	second := cfg.Runtimes.Bindings["claude"]
	second.Credential, second.Model = "endpoint", "endpoint-model"
	cfg.Runtimes.Bindings["endpoint"] = second
	cfg.Runtimes.EnabledBindings = append([]string{}, enabled...)
	if _, err := operatorconfig.WriteAtomic(filepath.Join(home, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}
}

func runtimeAuthSourceFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// useFakeSignIn replaces the provider sign-in with one that writes a fixture
// login into the isolated credential directory of the sign-in.
func useFakeSignIn(t *testing.T) {
	t.Helper()
	original := productionRuntimeAuthLogin
	originalImport, originalSelectedImport := productionRuntimeAuthImport, productionSelectedRuntimeAuthImport
	t.Cleanup(func() {
		productionRuntimeAuthLogin = original
		productionRuntimeAuthImport = originalImport
		productionSelectedRuntimeAuthImport = originalSelectedImport
	})
	productionRuntimeAuthImport = fakeRuntimeAuthPublication(t)
	productionSelectedRuntimeAuthImport = fakeRuntimeAuthPublication(t)
	productionRuntimeAuthLogin = func(binary string, _ []string, environment []string, _ io.Reader, _ io.Writer) error {
		value := func(name string) string {
			for _, entry := range environment {
				if got, value, ok := strings.Cut(entry, "="); ok && got == name {
					return value
				}
			}
			return ""
		}
		switch binary {
		case "codex":
			return os.WriteFile(filepath.Join(value("CODEX_HOME"), "auth.json"),
				[]byte(`{"auth_mode":"chatgpt","OPENAI_API_KEY":null,"tokens":{"access_token":"ca","id_token":"ci","refresh_token":"sign-in-refresh","account_id":"acct"}}`), 0o600)
		case "claude":
			return os.WriteFile(filepath.Join(value("CLAUDE_CONFIG_DIR"), ".credentials.json"),
				[]byte(`{"claudeAiOauth":{"accessToken":"aa","refreshToken":"sign-in-refresh","expiresAt":9999999999999,"scopes":["user:inference"]}}`), 0o600)
		}
		return errors.New("unexpected provider login")
	}
}

func runtimeAuthBrokerArgs(t *testing.T, home string) []string {
	t.Helper()
	writeEndpointFixtureConfig(t, home)
	useFakeSignIn(t)
	return []string{"onboard", "runtime-auth", "--acquire", "--runtime-bindings", "chatgpt,endpoint"}
}

func TestBrokerOnboardRuntimeAuthPublishesOnlyAfterAllLiveGates(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_HOME", home)
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CODEX_ACCESS_TOKEN", "CODEX_API_KEY", "OPENAI_API_KEY"} {
		t.Setenv(key, "")
	}
	original := productionRuntimeAuthVerifier
	t.Cleanup(func() { productionRuntimeAuthVerifier = original })
	seen := []string{}
	productionRuntimeAuthVerifier = deployment.RuntimeAuthVerifyFunc(func(binding, stage string) error {
		seen = append(seen, binding)
		if _, err := os.Stat(filepath.Join(stage, binding+".json")); err != nil {
			t.Fatalf("missing staged grant for %s: %v", binding, err)
		}
		return nil
	})
	var stdout, stderr bytes.Buffer
	if code := brokerOnboardRuntimeAuth(runtimeAuthBrokerArgs(t, home), strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("broker exit %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Join(seen, ",") != "chatgpt,endpoint" {
		t.Fatalf("live gate order = %v", seen)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result["ok"] != true {
		t.Fatalf("result = %s, err %v", stdout.String(), err)
	}
	if _, err := os.Stat(filepath.Join(home, "refresh-grants", "endpoint.json")); err != nil {
		t.Fatalf("canonical store not published: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(home, "runtime-auth-sources"))
	if !os.IsNotExist(err) && (err != nil || len(entries) != 0) {
		t.Fatalf("staged credential copies survived import: %v, %v", entries, err)
	}
}

func TestBrokerOnboardRuntimeAuthVerifierFailurePublishesNothing(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_HOME", home)
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CODEX_ACCESS_TOKEN", "CODEX_API_KEY", "OPENAI_API_KEY"} {
		t.Setenv(key, "")
	}
	original := productionRuntimeAuthVerifier
	t.Cleanup(func() { productionRuntimeAuthVerifier = original })
	productionRuntimeAuthVerifier = deployment.RuntimeAuthVerifyFunc(func(binding, _ string) error {
		if binding == "endpoint" {
			return errors.New("provider rejected no-op")
		}
		return nil
	})
	var stdout, stderr bytes.Buffer
	if code := brokerOnboardRuntimeAuth(runtimeAuthBrokerArgs(t, home), strings.NewReader(""), &stdout, &stderr); code != 1 {
		t.Fatalf("broker exit %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "provider rejected no-op") {
		t.Fatalf("missing gate refusal: %q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, "refresh-grants")); !os.IsNotExist(err) {
		t.Fatalf("failed gate published grants: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(home, "runtime-auth-sources"))
	if !os.IsNotExist(err) && (err != nil || len(entries) != 0) {
		t.Fatalf("failed gate left staged credential copies: %v, %v", entries, err)
	}
}

func TestBrokerOnboardRuntimeAuthAcquiresInDisposableProviderHomes(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	writeEndpointFixtureConfig(t, home)
	t.Setenv("MC_HOME", home)
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CODEX_ACCESS_TOKEN", "CODEX_API_KEY", "OPENAI_API_KEY"} {
		t.Setenv(key, "")
	}
	originalVerifier := productionRuntimeAuthVerifier
	t.Cleanup(func() { productionRuntimeAuthVerifier = originalVerifier })
	productionRuntimeAuthVerifier = deployment.RuntimeAuthVerifyFunc(func(string, string) error { return nil })
	useFakeSignIn(t)
	var stdout, stderr bytes.Buffer
	args := []string{"onboard", "runtime-auth", "--acquire",
		"--runtime-bindings", "chatgpt,claude,endpoint"}
	if code := brokerOnboardRuntimeAuth(args, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("acquire broker exit %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, binding := range []string{"chatgpt", "claude", "endpoint"} {
		if _, err := os.Stat(filepath.Join(home, "refresh-grants", binding+".json")); err != nil {
			t.Fatalf("missing published %s grant: %v", binding, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(home, "runtime-auth-sources"))
	if !os.IsNotExist(err) && (err != nil || len(entries) != 0) {
		t.Fatalf("provider sources survived verified import: %v, %v", entries, err)
	}
}

func TestBrokerOnboardRuntimeAuthRequiresASignInForSubscriptions(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	writeEndpointFixtureConfig(t, home)
	t.Setenv("MC_HOME", home)
	original := productionRuntimeAuthVerifier
	t.Cleanup(func() { productionRuntimeAuthVerifier = original })
	productionRuntimeAuthVerifier = deployment.RuntimeAuthVerifyFunc(func(binding, _ string) error {
		t.Fatalf("the live gate ran for %s", binding)
		return nil
	})
	login := filepath.Join(filepath.Dir(home), "auth.json")
	for _, args := range [][]string{
		{"onboard", "runtime-auth", "--runtime-bindings", "chatgpt"},
		{"onboard", "runtime-auth", "--runtime-bindings", "chatgpt", "--codex-auth-file", login},
		{"onboard", "runtime-auth", "--runtime-bindings", "claude", "--claude-credentials-file", login},
	} {
		var stdout, stderr bytes.Buffer
		if code := brokerOnboardRuntimeAuth(args, strings.NewReader(""), &stdout, &stderr); code != 2 {
			t.Fatalf("%v: exit %d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
		if len(args) == 4 && !strings.Contains(stdout.String(), "requires a Culler sign-in: add --acquire (--acquire-runtime-auth for install.sh)") {
			t.Fatalf("the refusal does not name the sign-in option: %q", stdout.String())
		}
	}
	if _, err := os.Stat(filepath.Join(home, "refresh-grants")); !os.IsNotExist(err) {
		t.Fatalf("a refused request published grants: %v", err)
	}
}

func TestRuntimeAuthSectionRefusesAnUndefinedBindingBeforeAnyChange(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_HOME", home)
	original := productionRuntimeAuthVerifier
	t.Cleanup(func() { productionRuntimeAuthVerifier = original })
	productionRuntimeAuthVerifier = deployment.RuntimeAuthVerifyFunc(func(binding, _ string) error {
		t.Fatalf("the live gate ran for %s", binding)
		return nil
	})
	var stdout, stderr bytes.Buffer
	args := []string{"onboard", "runtime-auth", "--runtime-bindings", "chatgpt,endpoint"}
	if code := brokerOnboardRuntimeAuth(args, nil, &stdout, &stderr); code != 2 {
		t.Fatalf("undefined binding exit %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("result = %q: %v", stdout.String(), err)
	}
	errorBody, _ := result["error"].(map[string]any)
	const want = `--runtime-bindings names "endpoint", an undefined binding. The defined bindings are: chatgpt, claude. Use one of these ids.`
	if errorBody["message"] != want {
		t.Fatalf("error = %v, want %q", result["error"], want)
	}
	if _, err := os.Lstat(filepath.Join(home, "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("the refused section wrote config.toml: %v", err)
	}
}

func TestOnboardRejectsStaticCredentialFlagsBeforeAnyChange(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	t.Setenv("MC_HOME", home)
	for _, flag := range []string{"--static-binding", "--static-credential", "--static-token-file"} {
		var stdout, stderr bytes.Buffer
		if code := brokerOnboardRuntimeAuth([]string{"onboard", "runtime-auth", flag, "fixture-secret"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
			t.Fatalf("Flag %s returned %d", flag, code)
		}
		if strings.Contains(stdout.String()+stderr.String(), "fixture-secret") {
			t.Fatal("The refusal disclosed the credential")
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatal("The refusal created the home")
		}
	}
}

// The deployment tests verify protected publication. These tests verify the setup orchestration.
func fakeRuntimeAuthPublication(t *testing.T) func(string, *deployment.RuntimeAuthSourceStage, deployment.RuntimeAuthVerifier) (string, error) {
	t.Helper()
	return func(home string, source *deployment.RuntimeAuthSourceStage, verifier deployment.RuntimeAuthVerifier) (string, error) {
		stage := t.TempDir()
		cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
		if err != nil {
			return "", err
		}
		ids := map[string]bool{}
		for _, binding := range source.Sources.Bindings {
			id := cfg.Runtimes.Bindings[binding].Credential
			ids[id] = true
			if err := os.WriteFile(filepath.Join(stage, id+".json"), []byte(`{"fixture":true}`), 0600); err != nil {
				return "", err
			}
		}
		for _, binding := range source.Sources.Bindings {
			if err := verifier.Verify(binding, stage); err != nil {
				return "", err
			}
		}
		root := filepath.Join(home, "refresh-grants")
		if err := os.MkdirAll(root, 0700); err != nil {
			return "", err
		}
		for id := range ids {
			body, _ := json.Marshal(map[string]any{"version": 1, "credential": id, "channel": cfg.Runtimes.Credentials[id].Channel(), "type": "account", "credential_ref": strings.Repeat("b", 64), "authorization_id": strings.Repeat("a", 64)})
			if err := os.WriteFile(filepath.Join(root, id+".json"), body, 0600); err != nil {
				return "", err
			}
		}
		return "done", nil
	}
}
