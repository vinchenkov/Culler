//go:build darwin

package deployment

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestHostSecretKeychainRoundTripUsesIsolatedStoreAndHandlesLongValues(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	environment := appendLoginHome(isolatedLoginBaseEnvironment(os.Environ()), home, "CLAUDE_CONFIG_DIR", home+"/claude")
	isolated, err := prepareClaudeCredentialStore(home, environment)
	if err != nil {
		t.Fatal(err)
	}
	store := &keychainHostSecretStore{keychain: isolated.keychainPath, environment: environment}
	value := []byte(strings.Repeat("synthetic-session-value-", 600))
	if err := store.Write("culler-test", "provider-fixture", value); err != nil {
		t.Fatal(err)
	}
	body, err := store.Read("culler-test", "provider-fixture")
	if err != nil || !bytes.Equal(body, value) {
		t.Fatalf("The isolated Keychain round trip failed: %v", err)
	}
	if err := store.Delete("culler-test", "provider-fixture"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("culler-test", "provider-fixture"); err != nil {
		t.Fatal("The protected deletion cannot resume after completion")
	}
	if _, err := store.Read("culler-test", "provider-fixture"); err == nil {
		t.Fatal("The deleted secret remains available.")
	}
}
