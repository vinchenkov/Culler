package deployment

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type undeletableHostSecretStore struct{ hostSecretStore }

func (undeletableHostSecretStore) Delete(string, string) error { return ErrHostCredentialUnavailable }

func TestHostSecretLifecycleDeletesReplacedCredentialsAndTLS(t *testing.T) {
	home := agentProfileTestHome(t)
	if err := PublishAgentConnectionGrant(home, "claude", fixtureOAuthGrant("claude", "synthetic-first")); err != nil {
		t.Fatal(err)
	}
	before, err := ReadProviderCredentialIdentity(home, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if err := PublishAgentConnectionGrant(home, "claude", fixtureOAuthGrant("claude", "synthetic-next")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHostSecret(home, "provider", before.CredentialRef); err == nil {
		t.Fatal("The replacement retained the obsolete credential")
	}
	now := time.Now()
	first, err := EnsureProviderProxyTLS(home, now)
	if err != nil {
		t.Fatal(err)
	}
	next, err := EnsureProviderProxyTLS(home, now.Add(61*24*time.Hour))
	if err != nil || next.Key == first.Key || next.CA != first.CA {
		t.Fatal("The certificate did not rotate under its retained authority")
	}
	entries, err := os.ReadDir(filepath.Join(home, "host-credential-references"))
	if err != nil || len(entries) != 2 {
		t.Fatal("The host retained obsolete credential references")
	}
	if err := DeleteInstallationHostSecrets(home); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadProviderCredential(home, "claude"); err == nil {
		t.Fatal("The full wipe retained a provider credential")
	}
	if err := DeleteInstallationHostSecrets(home); err != nil {
		t.Fatal("The credential cleanup cannot resume")
	}
}

func TestHostSecretDeletionFailureKeepsReferencesForRecovery(t *testing.T) {
	home := agentProfileTestHome(t)
	if err := PublishAgentConnectionGrant(home, "claude", fixtureOAuthGrant("claude", "synthetic-kept")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "refresh-grants", "claude.json")
	before, _ := os.ReadFile(path)
	backend := hostSecretBackend
	hostSecretBackend = undeletableHostSecretStore{backend}
	t.Cleanup(func() { hostSecretBackend = backend })
	if err := DeleteInstallationHostSecrets(home); !errors.Is(err, ErrHostCredentialUnavailable) {
		t.Fatal("The failed protected deletion reported success")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("The failed protected deletion removed its reference")
	}
	entries, err := os.ReadDir(filepath.Join(home, "host-credential-references"))
	if err != nil || len(entries) != 1 {
		t.Fatal("The failed protected deletion lost its recovery record")
	}
	hostSecretBackend = backend
	if err := DeleteInstallationHostSecrets(home); err != nil {
		t.Fatal(err)
	}
}
