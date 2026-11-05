package deployment

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	operatorconfig "mc/config"
)

// migrationHome includes a sign-in connection whose credential identifier differs from its profile identifier.
func migrationHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := operatorconfig.Default()
	profile := cfg.Runtimes.Bindings["chatgpt"]
	profile.Model = "fixture-model"
	cfg.Runtimes.Bindings["chatgpt"] = profile
	cfg.Runtimes.EnabledBindings = []string{"chatgpt", "studio"}
	cfg.Runtimes.Credentials["token-b"] = cfg.Runtimes.Credentials["claude"]
	cfg.Runtimes.Bindings["studio"] = operatorconfig.RuntimeBinding{
		Harness: "claude-sdk", Model: "studio-model", Credential: "token-b",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := operatorconfig.WriteAtomic(filepath.Join(home, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	return home
}

func writeLegacyGrants(t *testing.T, home string) string {
	t.Helper()
	store := filepath.Join(home, "refresh-grants")
	if err := os.Mkdir(store, 0o700); err != nil {
		t.Fatal(err)
	}
	grants := map[string]string{
		"chatgpt.json": `{"binding":"chatgpt","channel":"codex","token_url":"https://old.example/token","client_id":"old-client","refresh_token":"codex-refresh","account_id":"acct-1"}` + "\n",
		"studio.json":  `{"binding":"studio","channel":"claude","refresh_token":"studio-refresh","scope":"user:inference"}` + "\n",
	}
	for name, body := range grants {
		if err := os.WriteFile(filepath.Join(store, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func TestMigrateRuntimeGrantStoreRekeysLegacyGrantsByCredential(t *testing.T) {
	home := migrationHome(t)
	store := writeLegacyGrants(t, home)

	migrated, err := MigrateRuntimeGrantStore(home)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(migrated, []string{"chatgpt", "token-b"}) {
		t.Fatalf("migrated = %v", migrated)
	}
	grants := readGrantMap(t, store)
	if len(grants) != 2 {
		t.Fatalf("store holds %d grants: %v", len(grants), grants)
	}
	chatgpt := grants["chatgpt"]
	if chatgpt["credential"] != "chatgpt" || chatgpt["channel"] != "codex" || chatgpt["refresh_token"] != "codex-refresh" ||
		chatgpt["account_id"] != "acct-1" || chatgpt["binding"] != nil {
		t.Fatalf("chatgpt grant = %v", chatgpt)
	}
	catalog := operatorconfig.Default().Runtimes.Credentials["chatgpt"]
	if chatgpt["token_url"] != catalog.TokenURL || chatgpt["client_id"] != catalog.ClientID {
		t.Fatalf("chatgpt grant authority = %v, want the config authority", chatgpt)
	}
	tokenB := grants["token-b"]
	if tokenB["credential"] != "token-b" || tokenB["channel"] != "claude" || tokenB["scope"] != "user:inference" ||
		tokenB["refresh_token"] != "studio-refresh" || tokenB["binding"] != nil {
		t.Fatalf("token-b grant = %v", tokenB)
	}
	if err := ValidateRuntimeGrantStore(home, []string{"chatgpt", "studio"}, nil); err != nil {
		t.Fatalf("migrated store does not validate: %v", err)
	}

	again, err := MigrateRuntimeGrantStore(home)
	if err != nil || again != nil {
		t.Fatalf("second migration = %v, %v", again, err)
	}
}

func TestMigrateRuntimeGrantStoreLeavesAbsentAndCurrentStoresAlone(t *testing.T) {
	home := migrationHome(t)
	if migrated, err := MigrateRuntimeGrantStore(home); err != nil || migrated != nil {
		t.Fatalf("absent store = %v, %v", migrated, err)
	}
	store := filepath.Join(home, "refresh-grants")
	if err := os.Mkdir(store, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"credential":"token-b","channel":"claude","refresh_token":"studio-refresh","scope":"user:inference"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "token-b.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if migrated, err := MigrateRuntimeGrantStore(home); err != nil || migrated != nil {
		t.Fatalf("current store = %v, %v", migrated, err)
	}
	after, err := os.ReadFile(filepath.Join(store, "token-b.json"))
	if err != nil || string(after) != body {
		t.Fatalf("current grant changed: %q, %v", after, err)
	}
}

func TestMigrateRuntimeGrantStoreRefusesAnUnknownBinding(t *testing.T) {
	home := migrationHome(t)
	store := filepath.Join(home, "refresh-grants")
	if err := os.Mkdir(store, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"binding":"retired","channel":"claude","refresh_token":"r","scope":"user:inference"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "retired.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := MigrateRuntimeGrantStore(home)
	if err == nil || !strings.Contains(err.Error(), `binding "retired"`) {
		t.Fatalf("unknown binding err = %v", err)
	}
	after, readErr := os.ReadFile(filepath.Join(store, "retired.json"))
	if readErr != nil || string(after) != body {
		t.Fatalf("refused grant changed: %q, %v", after, readErr)
	}
}
