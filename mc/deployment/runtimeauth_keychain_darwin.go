//go:build darwin

package deployment

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Claude Code adds a configuration directory suffix to this service name.
const claudeKeychainServicePrefix = "Claude Code-credentials"

// A private name avoids macOS login-keychain behavior.
const claudeIsolatedKeychainName = "culler.keychain-db"

type claudeCredentialStore struct {
	keychainPath string
	environment  []string
}

// securityTool is pinned rather than resolved through PATH: this tool decides
// where a subscription credential lands, so a mutable search path must not be
// able to substitute it.
const securityTool = "/usr/bin/security"

func requireSecurityTool() error {
	info, err := os.Lstat(securityTool)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", securityTool, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s is not a regular executable", securityTool)
	}
	return nil
}

func (s *claudeCredentialStore) run(args ...string) ([]byte, error) {
	if err := requireSecurityTool(); err != nil {
		return nil, err
	}
	cmd := exec.Command(securityTool, args...)
	cmd.Env = s.environment
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("security %s: %w", args[0], err)
	}
	return out, nil
}

// prepareClaudeCredentialStore builds the disposable login keychain BEFORE any
// browser flow runs. Every failure here costs no login; that ordering is the
// point, not an accident.
func prepareClaudeCredentialStore(flowHome string, environment []string) (*claudeCredentialStore, error) {
	for _, dir := range []string{"Library", "Library/Keychains", "Library/Preferences"} {
		if err := os.Mkdir(filepath.Join(flowHome, dir), 0o700); err != nil && !os.IsExist(err) {
			return nil, fmt.Errorf("create isolated Claude keychain root: %w", err)
		}
	}
	secret := make([]byte, 32)
	defer clear(secret)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate isolated keychain password: %w", err)
	}
	// The password guards a keychain that exists only for this flow and is
	// destroyed with it; the OAuth credential itself never reaches argv.
	password := hex.EncodeToString(secret)
	store := &claudeCredentialStore{
		keychainPath: filepath.Join(flowHome, "Library", "Keychains", claudeIsolatedKeychainName),
		environment:  environment,
	}
	if _, err := store.run("create-keychain", "-p", password, store.keychainPath); err != nil {
		return nil, fmt.Errorf("create isolated Claude keychain: %w", err)
	}
	// No -t and no -l: the keychain must not auto-lock while the operator is
	// still in the browser.
	if _, err := store.run("set-keychain-settings", store.keychainPath); err != nil {
		return nil, fmt.Errorf("configure isolated Claude keychain: %w", err)
	}
	// Both are HOME-scoped: they rewrite the search list inside the disposable
	// home only, and leave the operator's own default keychain untouched.
	if _, err := store.run("default-keychain", "-s", store.keychainPath); err != nil {
		return nil, fmt.Errorf("select isolated Claude keychain: %w", err)
	}
	if _, err := store.run("list-keychains", "-s", store.keychainPath); err != nil {
		return nil, fmt.Errorf("scope isolated Claude keychain search list: %w", err)
	}
	if _, err := store.run("unlock-keychain", "-p", password, store.keychainPath); err != nil {
		return nil, fmt.Errorf("unlock isolated Claude keychain: %w", err)
	}
	return store, nil
}

// acquiredServices lists the candidate credential items the login left in the
// disposable keychain. Enumerating is deliberate: the suffix is derived from
// the config directory, and recomputing that derivation would couple this to a
// CLI internal that can change without notice.
func (s *claudeCredentialStore) acquiredServices() ([]string, error) {
	out, err := s.run("dump-keychain", s.keychainPath)
	if err != nil {
		return nil, fmt.Errorf("inspect isolated Claude keychain: %w", err)
	}
	seen := map[string]bool{}
	services := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		_, rest, ok := strings.Cut(line, `"svce"<blob>="`)
		if !ok {
			continue
		}
		name, _, ok := strings.Cut(rest, `"`)
		if !ok || !strings.HasPrefix(name, claudeKeychainServicePrefix) || seen[name] {
			continue
		}
		seen[name] = true
		services = append(services, name)
	}
	return services, nil
}

// export moves the acquired credential out of the disposable keychain and into
// the file shape the rest of the import pipeline already validates.
func (s *claudeCredentialStore) export(configDir string) error {
	path := filepath.Join(configDir, ".credentials.json")
	if _, err := os.Lstat(path); err == nil {
		return nil // a future CLI that writes the file directly still works
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect acquired Claude credentials source: %w", err)
	}
	services, err := s.acquiredServices()
	if err != nil {
		return err
	}
	switch len(services) {
	case 1:
	case 0:
		return fmt.Errorf(
			"the provider login stored no %q credential in its isolated keychain "+
				"and wrote no credentials file", claudeKeychainServicePrefix)
	default:
		// Service names are config-directory hashes, not secrets, so naming
		// them is safe and picking one arbitrarily is not.
		return fmt.Errorf(
			"the isolated keychain holds %d candidate Claude credential items (%s); refusing to guess",
			len(services), strings.Join(services, ", "))
	}
	// The keychain path is explicit, so a miss stays a miss: this can never
	// widen into the operator's own login keychain.
	body, err := s.run("find-generic-password", "-s", services[0], "-w", s.keychainPath)
	if err != nil {
		return fmt.Errorf("read acquired Claude credential from its isolated keychain: %w", err)
	}
	body = []byte(strings.TrimRight(string(body), "\r\n"))
	if len(body) == 0 {
		return fmt.Errorf("the isolated keychain returned an empty Claude credential")
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("stage acquired Claude credentials: %w", err)
	}
	return nil
}
