package deployment

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	operatorconfig "mc/config"
	"mc/routing"
)

func deleteFixture(t *testing.T) (string, ProviderCredentialAccess, AgentConnectionDeleteRequest) {
	t.Helper()
	fixture := agentProfileTestHome(t)
	home, err := os.MkdirTemp("/tmp", "culler-delete-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	config, err := os.ReadFile(filepath.Join(fixture, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), config, 0600); err != nil {
		t.Fatal(err)
	}
	if err := PublishAgentConnectionGrant(home, "claude", fixtureOAuthGrant("claude", "synthetic-delete-refresh")); err != nil {
		t.Fatal(err)
	}
	current, err := ReadProviderCredential(home, "claude")
	if err != nil {
		t.Fatal(err)
	}
	return home, current, AgentConnectionDeleteRequest{ConnectionID: "claude", AuthorizationID: current.AuthorizationID, Confirm: true}
}

func readDeleteConfig(t *testing.T, home string) operatorconfig.Config {
	t.Helper()
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func noRevocation(context.Context, string, string) error { return nil }

func TestConnectionDeleteRevokesActiveRequestsAtEveryHostProxy(t *testing.T) {
	home, _, request := deleteFixture(t)
	foreground, err := os.MkdirTemp("/tmp", "culler-proxy-")
	if err != nil {
		t.Fatal(err)
	}
	foreground, err = filepath.EvalSymlinks(foreground)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(foreground) })
	root := filepath.Join(home, "health", "provider-proxies")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	registry, _ := json.Marshal(map[string]string{"socket_home": foreground})
	if err := os.WriteFile(filepath.Join(root, "01234567-89ab-cdef-0123-456789abcdef.json"), registry, 0600); err != nil {
		t.Fatal(err)
	}
	observed := make(chan map[string]any, 2)
	for _, socketHome := range []string{home, foreground} {
		if err := os.MkdirAll(filepath.Join(socketHome, "health"), 0700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(socketHome, "health", "provider-acceptance.sock")
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = listener.Close() })
		if err := os.Chmod(path, 0600); err != nil {
			t.Fatal(err)
		}
		go func() {
			conn, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			line, _ := bufio.NewReader(conn).ReadBytes('\n')
			var request map[string]any
			_ = json.Unmarshal(line, &request)
			observed <- request
			_ = json.NewEncoder(conn).Encode(map[string]any{"version": 1, "status": "ok", "nonce": request["nonce"], "revoked": true})
		}()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := AgentConnectionDelete(ctx, home, request); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case request := <-observed:
			if len(request) != 4 || request["credential"] != "claude" || request["op"] != "connection_revoke" {
				t.Fatalf("A host proxy did not receive the exact connection revocation: %v", request)
			}
		case <-ctx.Done():
			t.Fatal("A host proxy did not receive revocation")
		}
	}
}

func TestConnectionDeleteRemovesTheConnectionAndItsAgentProfiles(t *testing.T) {
	home, _, _ := deleteFixture(t)
	agentProfileAccountFixture(t, home, "team-account", "first-model")
	if _, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), func(cfg *operatorconfig.Config) error {
		cfg.Runtimes.Bindings["team-second"] = operatorconfig.RuntimeBinding{Harness: "claude-sdk", Model: "second-model", Credential: "team-account"}
		cfg.Runtimes.EnabledBindings = []string{"team-account", "team-second"}
		return cfg.Validate()
	}); err != nil {
		t.Fatal(err)
	}
	if err := PublishAgentConnectionGrant(home, "team-account", fixtureOAuthGrant("team-account", "synthetic-team-refresh")); err != nil {
		t.Fatal(err)
	}
	current, err := ReadProviderCredential(home, "team-account")
	if err != nil {
		t.Fatal(err)
	}
	kept, err := ReadProviderCredential(home, "claude")
	if err != nil {
		t.Fatal(err)
	}
	revocations := 0
	request := AgentConnectionDeleteRequest{ConnectionID: "team-account", AuthorizationID: current.AuthorizationID, Confirm: true}
	result, err := deleteAgentConnection(context.Background(), home, request, func(_ context.Context, _, connection string) error {
		revocations++
		if connection != "team-account" {
			t.Fatal("The deletion revoked another connection")
		}
		if _, err := ReadHostSecret(home, "provider", current.CredentialRef); err != nil {
			t.Fatal("The credential disappeared before active request revocation")
		}
		return nil
	})
	if err != nil || !result.Deleted || result.ProviderRevoked || revocations != 1 || !strings.Contains(result.Message, "unconfirmed") {
		t.Fatalf("Incorrect deletion result: %+v %v", result, err)
	}
	if _, err := ReadHostSecret(home, "provider", current.CredentialRef); err == nil {
		t.Fatal("The deleted credential remains in the protected store")
	}
	if _, err := ReadConnectionAuthorizationIdentity(home, "team-account"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("The deleted connection retains an authorization identity")
	}
	cfg := readDeleteConfig(t, home)
	if _, ok := cfg.Runtimes.Credentials["team-account"]; ok {
		t.Fatal("The deleted connection remains in the configuration")
	}
	for _, profile := range []string{"team-account", "team-second"} {
		if _, ok := cfg.Runtimes.Bindings[profile]; ok || slices.Contains(cfg.Runtimes.EnabledBindings, profile) {
			t.Fatalf("The agent profile %s of the deleted connection remains", profile)
		}
	}
	if cfg.Git.AuthorName != "Preserve this author" {
		t.Fatal("The deletion changed unrelated configuration")
	}
	for _, row := range result.Connections {
		if row.ID == "team-account" {
			t.Fatal("The result still lists the deleted connection")
		}
	}
	if after, err := ReadProviderCredential(home, "claude"); err != nil || after != kept {
		t.Fatal("The deletion changed another connection")
	}
	if err := RenewProviderCredential(home, ProviderCredentialRenewal{ConnectionID: "team-account", AuthorizationID: current.AuthorizationID, CredentialRef: current.CredentialRef, Record: current.Record}); err == nil {
		t.Fatal("A late renewal restored a deleted credential")
	}
	if _, err := deleteAgentConnection(context.Background(), home, request, noRevocation); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("A repeated deletion did not report the missing connection: %v", err)
	}
}

func TestConnectionDeleteRestoresTemplateEntries(t *testing.T) {
	home, _, request := deleteFixture(t)
	if _, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), func(cfg *operatorconfig.Config) error {
		source := cfg.Runtimes.Credentials["claude"]
		source.Name = "renamed_connection"
		cfg.Runtimes.Credentials["claude"] = source
		profile := cfg.Runtimes.Bindings["claude"]
		profile.Name, profile.Model = "renamed_profile", "another-model"
		cfg.Runtimes.Bindings["claude"] = profile
		cfg.Runtimes.Bindings["claude-extra"] = operatorconfig.RuntimeBinding{Harness: "claude-sdk", Model: "extra-model", Credential: "claude"}
		cfg.Runtimes.EnabledBindings = []string{"claude", "claude-extra"}
		return cfg.Validate()
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := deleteAgentConnection(context.Background(), home, request, noRevocation); err != nil {
		t.Fatal(err)
	}
	cfg := readDeleteConfig(t, home)
	template := operatorconfig.Default().Runtimes
	if !reflect.DeepEqual(cfg.Runtimes.Credentials["claude"], template.Credentials["claude"]) || !reflect.DeepEqual(cfg.Runtimes.Bindings["claude"], template.Bindings["claude"]) {
		t.Fatal("A deleted template connection did not return to its template definition")
	}
	if _, ok := cfg.Runtimes.Bindings["claude-extra"]; ok || len(cfg.Runtimes.EnabledBindings) != 0 {
		t.Fatalf("An agent profile of the deleted connection remains: %v", cfg.Runtimes.EnabledBindings)
	}
}

func TestConnectionDeleteRefusesAConnectionThatARoleUses(t *testing.T) {
	for _, mode := range []string{"assigned", "unreadable"} {
		t.Run(mode, func(t *testing.T) {
			home, before, request := deleteFixture(t)
			cfg := readDeleteConfig(t, home)
			cfg.Runtimes.EnabledBindings = []string{"claude"}
			if _, err := operatorconfig.WriteAtomic(filepath.Join(home, "config.toml"), cfg); err != nil {
				t.Fatal(err)
			}
			body := "synthetic unreadable routing"
			if mode == "assigned" {
				table, err := routing.UniformTable("claude", routing.RegistryFromRuntimes(cfg.Runtimes))
				if err != nil {
					t.Fatal(err)
				}
				body = table.Markdown()
			}
			if err := os.WriteFile(filepath.Join(home, "routing.md"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			configBefore, err := os.ReadFile(filepath.Join(home, "config.toml"))
			if err != nil {
				t.Fatal(err)
			}
			revocations := 0
			_, err = deleteAgentConnection(context.Background(), home, request, func(context.Context, string, string) error {
				revocations++
				return nil
			})
			want := map[string]string{"assigned": "Roles use this connection: Arbiter, ", "unreadable": "role assignments are unreadable"}[mode]
			if err == nil || !strings.Contains(err.Error(), want) || revocations != 0 {
				t.Fatalf("The deletion did not refuse a connection that a role may use: %v", err)
			}
			configAfter, err := os.ReadFile(filepath.Join(home, "config.toml"))
			if err != nil || !bytes.Equal(configBefore, configAfter) {
				t.Fatal("The refused deletion changed the configuration")
			}
			if after, err := ReadProviderCredential(home, "claude"); err != nil || after != before {
				t.Fatal("The refused deletion changed the credential")
			}
		})
	}
}

func TestConnectionDeleteRefusesStaleOrUnconfirmedRevocation(t *testing.T) {
	for _, mode := range []string{"stale", "unconfirmed", "cancelled", "missing-confirmation"} {
		t.Run(mode, func(t *testing.T) {
			home, before, request := deleteFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "stale":
				request.AuthorizationID = strings.Repeat("a", 64)
			case "cancelled":
				cancel()
			case "missing-confirmation":
				request.Confirm = false
			}
			calls := 0
			_, err := deleteAgentConnection(ctx, home, request, func(context.Context, string, string) error {
				calls++
				return errors.New("The resident did not confirm revocation.")
			})
			if err == nil {
				t.Fatal("The unsafe deletion succeeded")
			}
			if mode != "unconfirmed" && calls != 0 {
				t.Fatal("The stale request revoked current access")
			}
			after, readErr := ReadProviderCredential(home, "claude")
			if readErr != nil || before != after {
				t.Fatal("The failed deletion changed the current credential")
			}
			if _, ok := readDeleteConfig(t, home).Runtimes.Bindings["claude"]; !ok {
				t.Fatal("The failed deletion changed the configuration")
			}
		})
	}
}

func TestConnectionDeleteRefusesAnExistingInvalidSocket(t *testing.T) {
	home, before, request := deleteFixture(t)
	if err := os.Mkdir(filepath.Join(home, "health"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "health", "provider-acceptance.sock"), []byte("synthetic-invalid-socket"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := AgentConnectionDelete(context.Background(), home, request); err == nil {
		t.Fatal("The deletion ignored an existing invalid socket")
	}
	after, err := ReadProviderCredential(home, "claude")
	if err != nil || before != after {
		t.Fatal("The invalid socket caused credential deletion")
	}
}

func TestConnectionDeleteKeepsOtherConnections(t *testing.T) {
	home, _, request := deleteFixture(t)
	if err := PublishAgentConnectionGrant(home, "chatgpt", ProviderCredentialRecord{Credential: "chatgpt", Channel: "codex", RefreshToken: "synthetic-kept-refresh"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "refresh-grants", "chatgpt.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AgentConnectionDelete(context.Background(), home, request); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("Deletion changed another connection")
	}
}

func TestConnectionDeleteValidatesForegroundRegistryBeforeDeletion(t *testing.T) {
	for _, mode := range []string{"unrelated-directory", "extra-field", "unsafe-mode", "invalid-name", "missing-directory", "missing-socket"} {
		t.Run(mode, func(t *testing.T) {
			home, before, request := deleteFixture(t)
			root := filepath.Join(home, "health", "provider-proxies")
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			socketHome, err := os.MkdirTemp("/tmp", "culler-proxy-")
			if err != nil {
				t.Fatal(err)
			}
			socketHome, err = filepath.EvalSymlinks(socketHome)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(socketHome) })
			value := map[string]any{"socket_home": socketHome}
			name := "01234567-89ab-cdef-0123-456789abcdef.json"
			permissions := os.FileMode(0600)
			switch mode {
			case "unrelated-directory":
				value["socket_home"] = home
			case "extra-field":
				value["extra"] = "unsupported"
			case "unsafe-mode":
				permissions = 0644
			case "invalid-name":
				name = "unsupported.json"
			case "missing-directory":
				if err := os.Remove(socketHome); err != nil {
					t.Fatal(err)
				}
			}
			body, _ := json.Marshal(value)
			path := filepath.Join(root, name)
			if err := os.WriteFile(path, body, permissions); err != nil {
				t.Fatal(err)
			}
			_, err = AgentConnectionDelete(context.Background(), home, request)
			if mode == "missing-directory" || mode == "missing-socket" {
				if err != nil {
					t.Fatalf("The obsolete foreground entry blocked deletion: %v", err)
				}
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("The obsolete registry entry remained")
				}
				if _, err := os.Stat(home); err != nil {
					t.Fatal("Registry cleanup removed the deployment")
				}
				return
			}
			if err == nil {
				t.Fatal("An invalid registry entry permitted credential deletion")
			}
			after, err := ReadProviderCredential(home, "claude")
			if err != nil || before != after {
				t.Fatal("An invalid registry entry changed the credential")
			}
			if _, err := os.Stat(home); err != nil {
				t.Fatal("Registry cleanup removed an unrelated path")
			}
		})
	}
}

func TestConnectionCredentialRemovalDeletesTheSavedCredentialOnce(t *testing.T) {
	home, current, _ := deleteFixture(t)
	revocations := 0
	revoke := func(_ context.Context, _, connection string) error {
		revocations++
		if connection != "claude" {
			t.Fatal("The removal revoked another connection")
		}
		return nil
	}
	if err := removeAgentConnectionCredential(home, "claude", revoke); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHostSecret(home, "provider", current.CredentialRef); err == nil {
		t.Fatal("The removed credential remains in the protected store")
	}
	if _, err := ReadConnectionAuthorizationIdentity(home, "claude"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("The removed connection retains an authorization identity")
	}
	if err := removeAgentConnectionCredential(home, "claude", revoke); err != nil || revocations != 1 {
		t.Fatal("A repeated removal did not complete without another revocation")
	}
}
