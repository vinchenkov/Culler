//go:build test_fake_routing

package deployment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixtureHostSecretStoreRefusesRealCredentialsAndDeploymentPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MC_HOME", home)
	root := filepath.Join(t.TempDir(), "secrets")
	store := &fixtureHostSecretStore{root: root}
	account := "provider:" + strings.Repeat("a", 64)
	secret := []byte(`{"version":1,"type":"api_key","credential":"fixture","channel":"codex","api_key":"synthetic-api-key"}`)
	if err := store.Write("fixture", account, secret); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Read("fixture", account)
	if err != nil || string(saved) != string(secret) {
		t.Fatal("The synthetic credential did not survive a new read")
	}
	if err := store.Write("fixture", account, secret); err == nil {
		t.Fatal("The store replaced an immutable credential")
	}
	if err := store.Write("fixture", "provider:other", []byte(strings.ReplaceAll(string(secret), "synthetic-api-key", "sk-real-value"))); err == nil {
		t.Fatal("The test store accepted a real credential shape")
	}
	if err := store.Delete("fixture", account); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("fixture", account); err != nil {
		t.Fatal("The synthetic store refused a repeated deletion")
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatal("The test store retained a deleted credential")
	}
	store.root = filepath.Join(home, "secrets")
	if err := store.Write("fixture", account, secret); err == nil {
		t.Fatal("The test store wrote a credential below a deployment mount")
	}
}
