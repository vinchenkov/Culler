//go:build darwin

package deployment

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture writes through the selected scratch home, as the command does.
func claudeKeychainLogin(t *testing.T, services []string, body string) RuntimeAuthLoginFunc {
	t.Helper()
	return func(binary string, args, environment []string, _ io.Reader, _ io.Writer) error {
		switch binary {
		case "codex":
			return os.WriteFile(
				filepath.Join(envValue(environment, "CODEX_HOME"), "auth.json"),
				[]byte(`{"auth_mode":"chatgpt"}`+"\n"), 0o600)
		case "claude":
			for _, service := range services {
				// No explicit keychain path: rely on the default keychain the
				// acquisition wired into this HOME, exactly as the CLI does.
				cmd := exec.Command(securityTool, "add-generic-password",
					"-s", service, "-a", "probe", "-w", body)
				cmd.Env = environment
				out, err := cmd.CombinedOutput()
				if err != nil {
					return errors.New("fixture keychain write failed: " + string(out))
				}
			}
			return nil
		}
		return errors.New("unexpected binary")
	}
}

func acquireWithKeychainServices(t *testing.T, services []string, body string) (*RuntimeAuthAcquisition, error) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSignInCatalogConfig(t, home)
	return AcquireRuntimeAuth(home, []string{"claude"}, nil, io.Discard,
		[]string{"PATH=" + os.Getenv("PATH"), "HOME=/personal", "LANG=en_US.UTF-8"},
		claudeKeychainLogin(t, services, body))
}

const validClaudeCredential = `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1,"scopes":["user:inference"]}}`

func TestAcquireRuntimeAuthExportsClaudeCredentialsFromItsIsolatedKeychain(t *testing.T) {
	for _, service := range []string{
		claudeKeychainServicePrefix + "-6980771c",
		claudeKeychainServicePrefix,
	} {
		t.Run(service, func(t *testing.T) {
			acquisition, err := acquireWithKeychainServices(t, []string{service}, validClaudeCredential)
			if err != nil {
				t.Fatalf("acquisition refused a keychain-stored credential: %v", err)
			}
			defer acquisition.Cleanup()
			if got := strings.TrimSpace(string(acquisition.claudeCredentialsBody)); got != validClaudeCredential {
				t.Fatalf("exported credential = %q, want %q", got, validClaudeCredential)
			}
			if _, err := buildRuntimeGrantBody("claude", signInCatalog(t).Credentials["claude"], acquisition.claudeCredentialsBody); err != nil {
				t.Fatalf("exported credential failed its own grant validation: %v", err)
			}
		})
	}
}

func TestAcquireRuntimeAuthRefusesAmbiguousKeychainCandidates(t *testing.T) {
	acquisition, err := acquireWithKeychainServices(t, []string{
		claudeKeychainServicePrefix + "-aaaaaaaa",
		claudeKeychainServicePrefix + "-bbbbbbbb",
	}, validClaudeCredential)
	if err == nil {
		acquisition.Cleanup()
		t.Fatal("acquisition guessed between two candidate credentials")
	}
	if !strings.Contains(err.Error(), "refusing to guess") {
		t.Fatalf("error did not refuse the ambiguity: %v", err)
	}
}

func TestAcquireRuntimeAuthRefusesWhenTheIsolatedKeychainStayedEmpty(t *testing.T) {
	acquisition, err := acquireWithKeychainServices(t, nil, "")
	if err == nil {
		acquisition.Cleanup()
		t.Fatal("acquisition accepted an empty isolated keychain")
	}
	if !strings.Contains(err.Error(), "isolated keychain") {
		t.Fatalf("error did not name the isolated keychain: %v", err)
	}
}

func TestClaudeCredentialStoreNeverFallsBackToAnotherKeychain(t *testing.T) {
	prepare := func() *claudeCredentialStore {
		home := t.TempDir()
		store, err := prepareClaudeCredentialStore(home, []string{"HOME=" + home, "PATH=/usr/bin:/bin"})
		if err != nil {
			t.Fatal(err)
		}
		return store
	}
	outer, inner := prepare(), prepare()
	if _, err := outer.run("add-generic-password", "-s", claudeKeychainServicePrefix, "-a", "fixture", "-w", validClaudeCredential, outer.keychainPath); err != nil {
		t.Fatal(err)
	}
	// The ambient default holds a credential. Only the explicit inner store is eligible.
	inner.environment = append([]string(nil), outer.environment...)
	config := t.TempDir()
	if err := inner.export(config); err == nil || !strings.Contains(err.Error(), "stored no") {
		t.Fatalf("empty isolated store = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(config, ".credentials.json")); !os.IsNotExist(err) {
		t.Fatal("the empty isolated store exported another store's credential")
	}
	body, err := outer.run("find-generic-password", "-s", claudeKeychainServicePrefix, "-a", "fixture", "-w", outer.keychainPath)
	if err != nil || strings.TrimSpace(string(body)) != validClaudeCredential {
		t.Fatal("the isolated export changed the other scratch credential")
	}
}

// The store must exist before the browser flow starts, so a store that cannot
// be built costs no login. Prevents the live failure mode where a real OAuth
// round trip was spent and only then refused.
func TestAcquireRuntimeAuthPreparesTheKeychainBeforeSpendingALogin(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSignInCatalogConfig(t, home)
	called := false
	login := func(binary string, _, environment []string, _ io.Reader, _ io.Writer) error {
		called = true
		keychain := filepath.Join(envValue(environment, "HOME"),
			"Library", "Keychains", claudeIsolatedKeychainName)
		info, err := os.Stat(keychain)
		if err != nil {
			return fmt.Errorf("login started before its keychain existed: %w", err)
		}
		if info.Size() == 0 {
			return errors.New("login started before its keychain was initialized")
		}
		return nil
	}
	acquisition, err := AcquireRuntimeAuth(home, []string{"claude"}, nil, io.Discard,
		[]string{"PATH=" + os.Getenv("PATH"), "LANG=en_US.UTF-8"}, login)
	if err == nil {
		acquisition.Cleanup()
		t.Fatal("acquisition accepted a login that stored nothing")
	}
	if !called {
		t.Fatalf("the isolated keychain prevented the fixture login: %v", err)
	}
	if strings.Contains(err.Error(), "before its keychain") {
		t.Fatalf("keychain was not ready when the login ran: %v", err)
	}
}
