package deployment

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	operatorconfig "mc/config"
)

// fixtureAccounts supplies additional sign-in accounts for the tests.
var fixtureAccounts = []struct{ id, model string }{
	{"endpoint", "endpoint-model"},
	{"second-endpoint", "second-model"},
	{"third-endpoint", "third-model"},
}

// signInCatalogConfig enables the reference profiles and additional sign-in profiles.
func signInCatalogConfig(t *testing.T) operatorconfig.Config {
	t.Helper()
	cfg := operatorconfig.Default()
	profile := cfg.Runtimes.Bindings["chatgpt"]
	profile.Model = "fixture-model"
	cfg.Runtimes.Bindings["chatgpt"] = profile
	cfg.Runtimes.EnabledBindings = []string{"chatgpt", "claude"}
	for _, account := range fixtureAccounts {
		cfg.Runtimes.Credentials[account.id] = cfg.Runtimes.Credentials["claude"]
		cfg.Runtimes.Bindings[account.id] = operatorconfig.RuntimeBinding{
			Harness: "claude-sdk", Model: account.model, Credential: account.id,
		}
		cfg.Runtimes.EnabledBindings = append(cfg.Runtimes.EnabledBindings, account.id)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func signInCatalog(t *testing.T) RuntimeCatalog {
	t.Helper()
	return CatalogFromRuntimes(signInCatalogConfig(t).Runtimes)
}

// writeSignInCatalogConfig writes signInCatalogConfig as MC_HOME/config.toml.
func writeSignInCatalogConfig(t *testing.T, home string) {
	t.Helper()
	if _, err := operatorconfig.WriteAtomic(filepath.Join(home, "config.toml"), signInCatalogConfig(t)); err != nil {
		t.Fatal(err)
	}
}

func writeSecretFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func validRuntimeAuthSources(t *testing.T, home string) RuntimeAuthSources {
	t.Helper()
	writeSignInCatalogConfig(t, home)
	sourceRoot := runtimeAuthSourceRoot(home)
	if err := os.MkdirAll(sourceRoot, 0o700); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	sources, err := os.MkdirTemp(sourceRoot, "provider-homes-")
	if err != nil {
		t.Fatal(err)
	}
	return RuntimeAuthSources{
		Bindings: []string{"chatgpt", "claude", "endpoint"},
		CodexAuthFile: writeSecretFile(t, sources, "auth.json", `{
  "auth_mode":"chatgpt",
  "OPENAI_API_KEY":null,
  "tokens":{"access_token":"codex-access","id_token":"codex-id","refresh_token":"codex-refresh","account_id":"acct-1"},
  "last_refresh":"2026-07-22T00:00:00Z"
}`),
		ClaudeCredentialsFile: writeSecretFile(t, sources, ".credentials.json", `{
  "claudeAiOauth":{"accessToken":"claude-access","refreshToken":"claude-refresh","expiresAt":9999999999999,"refreshTokenExpiresAt":9999999999999,"scopes":["user:inference"],"subscriptionType":"max"}
}`),
	}
}

func readGrantMap(t *testing.T, dir string) map[string]map[string]any {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			t.Fatalf("unexpected runtime grant entry %q", entry.Name())
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if filepath.Base(dir) == "refresh-grants" {
			body, err = readConnectionCredentialBody(filepath.Dir(dir), strings.TrimSuffix(entry.Name(), ".json"))
		}
		if err != nil {
			t.Fatal(err)
		}
		var grant map[string]any
		if err := json.Unmarshal(body, &grant); err != nil {
			t.Fatal(err)
		}
		out[strings.TrimSuffix(entry.Name(), ".json")] = grant
		if info, err := entry.Info(); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("grant %s mode = %v, err %v", entry.Name(), info.Mode().Perm(), err)
		}
	}
	return out
}

func TestImportRuntimeAuthPublishesProtectedSessionsBeforeModelTests(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSignInCatalogConfig(t, home)
	canonical := filepath.Join(home, "refresh-grants")
	if err := os.Mkdir(canonical, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonical, "old.json"), []byte("old-owner\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	seen := []string{}
	verifier := RuntimeAuthVerifyFunc(func(binding, stagedDir string) error {
		seen = append(seen, binding)
		actual, _ := filepath.EvalSymlinks(canonical)
		if stagedDir != actual {
			t.Fatal("The model test did not use the saved protected sessions")
		}
		if _, err := os.Stat(filepath.Join(canonical, "old.json")); !os.IsNotExist(err) {
			t.Fatal("The obsolete session remained current during the model test")
		}
		if _, err := os.Stat(filepath.Join(stagedDir, binding+".json")); err != nil {
			t.Fatalf("verifier cannot inspect the staged grant of %s: %v", binding, err)
		}
		return nil
	})
	status, err := ImportRuntimeAuth(home, validRuntimeAuthSources(t, home), verifier)
	if err != nil || status != "done" {
		t.Fatalf("import = status %q err %v", status, err)
	}
	if !reflect.DeepEqual(seen, []string{"chatgpt", "claude", "endpoint"}) {
		t.Fatalf("verification order = %v", seen)
	}
	grants := readGrantMap(t, canonical)
	if got := grants["chatgpt"]; got["channel"] != "codex" || got["refresh_token"] != "codex-refresh" || got["account_id"] != "acct-1" {
		t.Fatalf("codex grant = %#v", got)
	}
	if got := grants["claude"]; got["channel"] != "claude" || got["refresh_token"] != "claude-refresh" || got["scope"] != "user:inference" {
		t.Fatalf("claude grant = %#v", got)
	}
	if got := grants["endpoint"]; got["channel"] != "claude" || got["refresh_token"] != "claude-refresh" {
		t.Fatalf("endpoint grant = %#v", got)
	}
	if _, exists := grants["old"]; exists {
		t.Fatal("old grant survived atomic replacement")
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{".config.lock", "config.toml", "host-credential-references", "refresh-grants"}) {
		t.Fatalf("secret staging residue under MC_HOME: %v", names)
	}
}

func TestImportRuntimeAuthModelFailureRetainsSavedNativeSessions(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	canonical := filepath.Join(home, "refresh-grants")
	if err := os.MkdirAll(canonical, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSignInCatalogConfig(t, home)
	before := []byte("existing-canonical-owner\n")
	if err := os.WriteFile(filepath.Join(canonical, "owner.json"), before, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ImportRuntimeAuth(home, validRuntimeAuthSources(t, home), RuntimeAuthVerifyFunc(func(binding, _ string) error {
		if binding == "claude" {
			return errors.New("live no-op refused")
		}
		return nil
	}))
	if err == nil || !strings.Contains(err.Error(), "claude") || !strings.Contains(err.Error(), "live no-op refused") {
		t.Fatalf("gate failure = %v", err)
	}
	grants := readGrantMap(t, canonical)
	if grants["chatgpt"]["refresh_token"] != "codex-refresh" || grants["claude"]["refresh_token"] != "claude-refresh" {
		t.Fatal("The failed model test lost a new native session")
	}
	if _, exists := grants["owner"]; exists {
		t.Fatal("The failed model test restored an obsolete session")
	}
	entries, readErr := os.ReadDir(home)
	if readErr != nil || len(entries) != 4 {
		t.Fatalf("failed import left staging residue: entries=%v err=%v", entries, readErr)
	}
}

func TestImportRuntimeAuthHealthyReplayRunsGatesWithoutReplacingTheStore(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSignInCatalogConfig(t, home)
	sources := validRuntimeAuthSources(t, home)
	verifications := 0
	verifier := RuntimeAuthVerifyFunc(func(string, string) error { verifications++; return nil })
	if status, err := ImportRuntimeAuth(home, sources, verifier); err != nil || status != "done" {
		t.Fatalf("first import = %q, %v", status, err)
	}
	canonical := filepath.Join(home, "refresh-grants")
	before, err := os.Stat(canonical)
	if err != nil {
		t.Fatal(err)
	}
	sources = validRuntimeAuthSources(t, home)
	if status, err := ImportRuntimeAuth(home, sources, verifier); err != nil || status != "ok" {
		t.Fatalf("replay = %q, %v", status, err)
	}
	after, err := os.Stat(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("healthy replay replaced the canonical grant directory")
	}
	if verifications != 6 {
		t.Fatalf("healthy replay skipped live gates: %d calls", verifications)
	}
}

func TestValidateRuntimeGrantStoreIsTokenFreeAndFailClosed(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSignInCatalogConfig(t, home)
	sources := validRuntimeAuthSources(t, home)
	bindings := []string{"chatgpt", "claude", "endpoint"}
	if _, err := ImportRuntimeAuth(home, sources, RuntimeAuthVerifyFunc(func(string, string) error { return nil })); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRuntimeGrantStore(home, bindings, []string{"PATH=/usr/bin"}); err != nil {
		t.Fatalf("validate canonical store: %v", err)
	}
	if err := ValidateRuntimeGrantStore(home, bindings, []string{"OPENAI_API_KEY=metered"}); err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("forbidden environment accepted: %v", err)
	}
	grant := filepath.Join(home, "refresh-grants", "claude.json")
	if err := os.Chmod(grant, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRuntimeGrantStore(home, bindings, nil); err == nil || !strings.Contains(err.Error(), "mode-0600") {
		t.Fatalf("widened canonical grant accepted: %v", err)
	}
}

func TestImportRuntimeAuthRejectsUnsafeSourcesAndBindingSets(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(t *testing.T, root string, sources *RuntimeAuthSources)
		want   string
	}{
		"duplicate binding": {func(_ *testing.T, _ string, s *RuntimeAuthSources) { s.Bindings = []string{"chatgpt", "chatgpt"} }, "duplicate runtime binding"},
		"unknown binding":   {func(_ *testing.T, _ string, s *RuntimeAuthSources) { s.Bindings = []string{"unknown"} }, "unknown runtime binding"},
		"metered codex fallback": {func(t *testing.T, _ string, s *RuntimeAuthSources) {
			s.CodexAuthFile = writeSecretFile(t, filepath.Dir(s.CodexAuthFile), "metered.json", `{"auth_mode":"chatgpt","OPENAI_API_KEY":"metered","tokens":{"access_token":"a","id_token":"i","refresh_token":"r","account_id":"x"}}`)
		}, "API key"},
		"missing claude scope": {func(t *testing.T, _ string, s *RuntimeAuthSources) {
			s.ClaudeCredentialsFile = writeSecretFile(t, filepath.Dir(s.ClaudeCredentialsFile), "scopeless.json", `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":9999999999999,"scopes":["profile"]}}`)
		}, "user:inference"},
		"group-readable token": {func(t *testing.T, _ string, s *RuntimeAuthSources) {
			if err := os.Chmod(s.ClaudeCredentialsFile, 0o640); err != nil {
				t.Fatal(err)
			}
		}, "mode 0600"},
		"symlink source": {func(t *testing.T, root string, s *RuntimeAuthSources) {
			link := filepath.Join(filepath.Dir(s.CodexAuthFile), "codex-link")
			if err := os.Symlink(s.CodexAuthFile, link); err != nil {
				t.Fatal(err)
			}
			s.CodexAuthFile = link
		}, "symlink"},
		"ambient provider key": {func(_ *testing.T, _ string, s *RuntimeAuthSources) {
			s.Environment = []string{"PATH=/bin", "OPENAI_API_KEY=metered"}
		}, "OPENAI_API_KEY"},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			if err := os.Mkdir(home, 0o700); err != nil {
				t.Fatal(err)
			}
			writeSignInCatalogConfig(t, home)
			sources := validRuntimeAuthSources(t, home)
			tc.mutate(t, root, &sources)
			_, err := ImportRuntimeAuth(home, sources, RuntimeAuthVerifyFunc(func(string, string) error { return nil }))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if _, statErr := os.Stat(filepath.Join(home, "refresh-grants")); !os.IsNotExist(statErr) {
				t.Fatalf("refusal published a canonical store: %v", statErr)
			}
		})
	}
}

func fixtureOAuthGrant(id, refresh string) oauthRuntimeGrant {
	source := operatorconfig.Default().Runtimes.Credentials["claude"]
	return oauthRuntimeGrant{Credential: id, Channel: source.Channel(), TokenURL: source.TokenURL, ClientID: source.ClientID, RefreshToken: refresh, Scope: "user:inference"}
}
