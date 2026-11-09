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
	"strings"
	"testing"
	"time"
)

func disconnectFixture(t *testing.T) (string, ProviderCredentialAccess, AgentConnectionDisconnectRequest) {
	t.Helper()
	fixture := agentProfileTestHome(t)
	home, err := os.MkdirTemp("/tmp", "culler-disconnect-")
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
	if err := PublishAgentConnectionGrant(home, "claude", fixtureOAuthGrant("claude", "synthetic-disconnect-refresh")); err != nil {
		t.Fatal(err)
	}
	current, err := ReadProviderCredential(home, "claude")
	if err != nil {
		t.Fatal(err)
	}
	return home, current, AgentConnectionDisconnectRequest{ConnectionID: "claude", AuthorizationID: current.AuthorizationID, CredentialRef: current.CredentialRef, Confirm: true}
}

func TestConnectionDisconnectRevokesTheCurrentRevisionAtEveryHostProxy(t *testing.T) {
	home, current, request := disconnectFixture(t)
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
	if _, err := AgentConnectionDisconnect(ctx, home, request); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case request := <-observed:
			if len(request) != 5 || request["credential"] != "claude" || request["authorization_id"] != current.AuthorizationID || request["op"] != "connection_revoke" {
				t.Fatal("A host proxy did not receive the exact current authorization")
			}
		case <-ctx.Done():
			t.Fatal("A host proxy did not receive revocation")
		}
	}
}

func TestConnectionDisconnectRevokesBeforeDeletionAndPreservesConfiguration(t *testing.T) {
	home, current, request := disconnectFixture(t)
	before, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	revocations := 0
	result, err := disconnectAgentConnection(context.Background(), home, request, func(context.Context, string, string, string) error {
		revocations++
		if _, err := ReadHostSecret(home, "provider", current.CredentialRef); err != nil {
			t.Fatal("The credential disappeared before active request revocation")
		}
		return nil
	})
	if err != nil || !result.Disconnected || result.ProviderRevoked || revocations != 1 || !strings.Contains(result.Message, "unconfirmed") {
		t.Fatalf("Incorrect disconnection result: %+v %v", result, err)
	}
	if _, err := ReadHostSecret(home, "provider", current.CredentialRef); err == nil {
		t.Fatal("The disconnected credential remains in the protected store")
	}
	if identity, err := AgentConnectionAuthorizationIdentity(home, "claude"); err != nil || identity != "" {
		t.Fatal("The disconnected authorization remains current")
	}
	after, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("Disconnection changed the saved configuration")
	}
	if err := RenewProviderCredential(home, ProviderCredentialRenewal{ConnectionID: "claude", AuthorizationID: current.AuthorizationID, CredentialRef: current.CredentialRef, Record: current.Record}); err == nil {
		t.Fatal("A late renewal restored a disconnected credential")
	}
	if _, err := AgentConnectionDisconnect(context.Background(), home, request); err != nil {
		t.Fatal("A repeated disconnection failed")
	}
}

func TestConnectionDisconnectRefusesStaleOrUnconfirmedRevocation(t *testing.T) {
	for _, mode := range []string{"stale", "unconfirmed", "cancelled", "missing-confirmation"} {
		t.Run(mode, func(t *testing.T) {
			home, before, request := disconnectFixture(t)
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
			_, err := disconnectAgentConnection(ctx, home, request, func(context.Context, string, string, string) error {
				calls++
				return errors.New("The resident did not confirm revocation.")
			})
			if err == nil {
				t.Fatal("The unsafe disconnection succeeded")
			}
			if mode != "unconfirmed" && calls != 0 {
				t.Fatal("The stale request revoked current access")
			}
			after, readErr := ReadProviderCredential(home, "claude")
			if readErr != nil || before != after {
				t.Fatal("The failed disconnection changed the current credential")
			}
		})
	}
}

func TestConnectionDisconnectRefusesAnExistingInvalidSocket(t *testing.T) {
	home, before, request := disconnectFixture(t)
	if err := os.Mkdir(filepath.Join(home, "health"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "health", "provider-acceptance.sock"), []byte("synthetic-invalid-socket"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := AgentConnectionDisconnect(context.Background(), home, request); err == nil {
		t.Fatal("The disconnection ignored an existing invalid socket")
	}
	after, err := ReadProviderCredential(home, "claude")
	if err != nil || before != after {
		t.Fatal("The invalid socket caused credential deletion")
	}
}

func TestConnectionDisconnectKeepsOtherConnections(t *testing.T) {
	home, _, request := disconnectFixture(t)
	if err := PublishAgentConnectionGrant(home, "chatgpt", ProviderCredentialRecord{Credential: "chatgpt", Channel: "codex", RefreshToken: "synthetic-kept-refresh"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "refresh-grants", "chatgpt.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AgentConnectionDisconnect(context.Background(), home, request); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("Disconnection changed another connection")
	}
}

func TestConnectionDisconnectValidatesForegroundRegistryBeforeDeletion(t *testing.T) {
	for _, mode := range []string{"unrelated-directory", "extra-field", "unsafe-mode", "invalid-name", "missing-directory", "missing-socket"} {
		t.Run(mode, func(t *testing.T) {
			home, before, request := disconnectFixture(t)
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
			_, err = AgentConnectionDisconnect(context.Background(), home, request)
			if mode == "missing-directory" || mode == "missing-socket" {
				if err != nil {
					t.Fatalf("The obsolete foreground entry blocked disconnection: %v", err)
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
	home, current, _ := disconnectFixture(t)
	revocations := 0
	revoke := func(_ context.Context, _, connection, authorization string) error {
		revocations++
		if connection != "claude" || authorization != current.AuthorizationID {
			t.Fatal("The removal revoked another authorization")
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
