package deployment

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var secretShape = regexp.MustCompile(`^[0-9a-f]{64}$`)

func requireSecretFile(t *testing.T, path, secret string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("secret file %s = %v, %v", path, info, err)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != secret+"\n" || !secretShape.MatchString(secret) {
		t.Fatalf("secret file body = %q, secret %q, err %v", body, secret, err)
	}
}

func TestViewerSecretsAreCreatedOnceAndNeverRewritten(t *testing.T) {
	for name, secret := range map[string]struct {
		path   func(string) string
		ensure func(string) (string, error)
		read   func(string) (string, error)
	}{
		"AgentsView token": {func(home string) string { return AgentsViewPathsFor(home).Token }, EnsureAgentsViewToken, ReadAgentsViewToken},
		"wiki key":         {WikiKeyPath, EnsureWikiKey, ReadWikiKey},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if _, err := secret.read(home); err == nil || !strings.Contains(err.Error(), "is missing") ||
				errors.Is(err, ErrUnusableViewerSecret) {
				t.Fatalf("an absent %s was read: %v", name, err)
			}
			first, err := secret.ensure(home)
			if err != nil {
				t.Fatal(err)
			}
			requireSecretFile(t, secret.path(home), first)
			if info, err := os.Lstat(filepath.Dir(secret.path(home))); err != nil || info.Mode().Perm() != 0o700 {
				t.Fatalf("the %s folder = %v, %v", name, info, err)
			}
			again, err := secret.ensure(home)
			if err != nil || again != first {
				t.Fatalf("a second creation = %q, %v; want %q", again, err, first)
			}
			if read, err := secret.read(home); err != nil || read != first {
				t.Fatalf("read = %q, %v", read, err)
			}
			entries, _ := os.ReadDir(filepath.Dir(secret.path(home)))
			if len(entries) != 1 {
				t.Fatalf("the creation left a temporary file: %v", entries)
			}
			other, err := secret.ensure(t.TempDir())
			if err != nil || other == first {
				t.Fatalf("two deployments share the %s: %v", name, err)
			}
		})
	}
}

func TestViewerSecretsRefuseAnUnsafeFileAndKeepIt(t *testing.T) {
	home := t.TempDir()
	token, err := EnsureAgentsViewToken(home)
	if err != nil {
		t.Fatal(err)
	}
	path := AgentsViewPathsFor(home).Token
	for name, change := range map[string]func(){
		"readable mode": func() { mustChmod(t, path, 0o644) },
		"short value":   func() { mustWrite(t, path, "abc\n") },
		"upper case":    func() { mustWrite(t, path, strings.ToUpper(token)) },
		"symbolic link": func() {
			target := filepath.Join(t.TempDir(), "token")
			mustWrite(t, target, token)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			_ = os.Remove(path)
			mustWrite(t, path, token+"\n")
			change()
			before, _ := os.Lstat(path)
			if _, err := ReadAgentsViewToken(home); err == nil || !strings.Contains(err.Error(), path) ||
				!errors.Is(err, ErrUnusableViewerSecret) {
				t.Fatalf("an unsafe token file was read: %v", err)
			}
			if _, err := EnsureAgentsViewToken(home); err == nil {
				t.Fatal("an unsafe token file was accepted")
			}
			after, _ := os.Lstat(path)
			if !os.SameFile(before, after) {
				t.Fatal("the creation rewrote an unsafe token file")
			}
		})
	}
}

func mustChmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	mustChmod(t, path, 0o600)
}

// The token and the key persist over a new preparation, an activation, an
// upgrade, and a loss of the AgentsView archive.
func TestPreparedSupervisionCarriesThePersistentViewerSecrets(t *testing.T) {
	home, spec := supervisionFixture(t)
	if _, _, err := PrepareSupervision(home, spec, func(string) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	token, err := ReadAgentsViewToken(home)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ReadWikiKey(home)
	if err != nil {
		t.Fatal(err)
	}
	requireSecretFile(t, AgentsViewPathsFor(home).Token, token)
	requireSecretFile(t, WikiKeyPath(home), key)
	config, err := os.ReadFile(filepath.Join(home, "supervision", "agentsview.toml"))
	if err != nil || !strings.Contains(string(config), "require_auth = true\nauth_token = \""+token+"\"\n") {
		t.Fatalf("prepared AgentsView configuration = %s, %v", config, err)
	}
	var dashboard map[string]string
	body, err := os.ReadFile(filepath.Join(home, "supervision", "dashboard.json"))
	if err != nil || json.Unmarshal(body, &dashboard) != nil || dashboard["wiki_key"] != key {
		t.Fatalf("dashboard config = %s, %v", body, err)
	}
	if err := InstallAgentsViewConfig(home); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(AgentsViewPathsFor(home).Data); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(home, "supervision")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PrepareSupervision(home, spec, func(string) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	if again, err := ReadAgentsViewToken(home); err != nil || again != token {
		t.Fatalf("the token after a new preparation = %q, %v", again, err)
	}
	if again, err := ReadWikiKey(home); err != nil || again != key {
		t.Fatalf("the wiki key after a new preparation = %q, %v", again, err)
	}
	if err := InstallAgentsViewConfig(home); err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(AgentsViewPathsFor(home).Config)
	if err != nil || !strings.Contains(string(installed), "auth_token = \""+token+"\"") {
		t.Fatalf("the restored AgentsView configuration = %s, %v", installed, err)
	}
}

func TestSupervisionPolicyComparesTheViewerSecrets(t *testing.T) {
	for name, change := range map[string]func(t *testing.T, home string){
		"another wiki key": func(t *testing.T, home string) {
			mustWrite(t, WikiKeyPath(home), strings.Repeat("a", 64)+"\n")
		},
		"a missing wiki key": func(t *testing.T, home string) {
			if err := os.Remove(WikiKeyPath(home)); err != nil {
				t.Fatal(err)
			}
		},
		"another AgentsView token": func(t *testing.T, home string) {
			mustWrite(t, AgentsViewPathsFor(home).Token, strings.Repeat("b", 64)+"\n")
		},
		"a missing AgentsView token": func(t *testing.T, home string) {
			if err := os.Remove(AgentsViewPathsFor(home).Token); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			home, spec := supervisionFixture(t)
			if _, _, err := PrepareSupervision(home, spec, func(string) (bool, error) { return false, nil }); err != nil {
				t.Fatal(err)
			}
			if matches, err := SupervisionPolicyMatches(home, spec.SupervisionPolicy); err != nil || !matches {
				t.Fatalf("the prepared policy match = %v, %v", matches, err)
			}
			change(t, home)
			if matches, err := SupervisionPolicyMatches(home, spec.SupervisionPolicy); err != nil || matches {
				t.Fatalf("the changed policy match = %v, %v", matches, err)
			}
		})
	}
}
