package deployment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const signInCodexFixture = `{"auth_mode":"chatgpt","tokens":{"access_token":"fixture-access","id_token":"fixture-id","refresh_token":"fixture-refresh","account_id":"fixture-account"}}`
const signInClaudeFixture = `{"claudeAiOauth":{"accessToken":"fixture-access","refreshToken":"fixture-refresh","expiresAt":9999999999999,"scopes":["user:inference"]}}`

// signInFixture returns the result of a Culler sign-in without a provider login.
func signInFixture(t *testing.T, home, codex, claude string) *RuntimeAuthAcquisition {
	t.Helper()
	canonical, err := CanonicalHome(home)
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot := runtimeAuthSourceRoot(canonical)
	if err := os.MkdirAll(sourceRoot, 0o700); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	flowRoot, err := os.MkdirTemp(sourceRoot, ".provider-login-")
	if err != nil {
		t.Fatal(err)
	}
	disposable, err := openRuntimeAuthDisposableRoot(sourceRoot, flowRoot, ".provider-login-", "runtime-auth acquisition flow")
	if err != nil {
		t.Fatal(err)
	}
	acquisition := &RuntimeAuthAcquisition{
		sourceRoot: sourceRoot, flowRoot: flowRoot, disposable: disposable,
		codexAuthBody: []byte(codex), claudeCredentialsBody: []byte(claude),
	}
	t.Cleanup(func() { _ = acquisition.Cleanup() })
	return acquisition
}

func TestStageRuntimeAuthSourcesRequiresACullerSignInForSubscriptions(t *testing.T) {
	for _, fixture := range []struct{ binding, name, body, want string }{
		{"chatgpt", "auth.json", signInCodexFixture, "a Codex subscription login requires a Culler sign-in"},
		{"claude", ".credentials.json", signInClaudeFixture, "a Claude Code subscription login requires a Culler sign-in"},
	} {
		t.Run(fixture.binding, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			operator := filepath.Join(root, "operator")
			for _, directory := range []string{home, operator} {
				if err := os.Mkdir(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			writeSignInCatalogConfig(t, home)
			path := writeSecretFile(t, operator, fixture.name, fixture.body)
			sources := RuntimeAuthSources{Bindings: []string{fixture.binding}, CodexAuthFile: path, ClaudeCredentialsFile: path}
			stage, err := StageRuntimeAuthSources(home, sources)
			if err == nil {
				_ = stage.Cleanup()
				t.Fatal("The stage accepted a login file of the operator.")
			}
			if !strings.Contains(err.Error(), fixture.want) {
				t.Fatalf("The refusal does not name the sign-in: %v", err)
			}
			if body, err := os.ReadFile(path); err != nil || string(body) != fixture.body {
				t.Fatalf("The stage changed the login file of the operator: %v", err)
			}
			entries, err := os.ReadDir(runtimeAuthSourceRoot(home))
			if err != nil || len(entries) != 0 {
				t.Fatalf("The refused stage left files: %v, %v", entries, err)
			}
		})
	}
}
