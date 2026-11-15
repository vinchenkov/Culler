package deployment

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type providerAccessFixture struct {
	Home     string
	Config   ProviderContainerAccess
	Requests int
	mu       sync.Mutex
	calls    []map[string]any
	answer   func(map[string]any, map[string]any) map[string]any
}

func residentAcceptanceHome(t *testing.T) (string, *net.UnixListener) {
	t.Helper()
	home, err := os.MkdirTemp("", "proxy-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	if err := os.Mkdir(filepath.Join(home, "health"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "health", "provider-acceptance.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	return home, listener
}

func newProviderAccessFixture(t *testing.T, harness, mode string, requests int) *providerAccessFixture {
	t.Helper()
	bundle, err := newProviderTLS(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	home, listener := residentAcceptanceHome(t)
	fixture := &providerAccessFixture{Home: home, Requests: requests, Config: ProviderContainerAccess{
		Version: 1, URL: "https://host.docker.internal:4443", Token: "culler_proxy_" + strings.Repeat("c", 43), CA: bundle.CA, Harness: harness, AuthMode: mode,
	}}
	go func() {
		for {
			conn, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var request map[string]any
				if json.Unmarshal(line, &request) != nil {
					return
				}
				fixture.mu.Lock()
				fixture.calls = append(fixture.calls, request)
				response := map[string]any{"version": 1, "status": "ok", "nonce": request["nonce"]}
				switch request["op"] {
				case "profile_authorize":
					response["credential"] = request["credential"]
					response["proxy_config"] = fixture.Config
				case "profile_status":
					response["provider_requests"] = fixture.Requests
				case "profile_revoke":
					response["revoked"] = true
				default:
					response["status"] = "error"
				}
				if fixture.answer != nil {
					response = fixture.answer(request, response)
				}
				fixture.mu.Unlock()
				_ = json.NewEncoder(conn).Encode(response)
			}()
		}
	}()
	return fixture
}

func (fixture *providerAccessFixture) access(connection, profile, model string, maximum int) (*providerAccessLease, error) {
	return authorizeProviderAccess(fixture.Home, connection, profile, model, "", maximum)
}

func (fixture *providerAccessFixture) recorded() []map[string]any {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return append([]map[string]any(nil), fixture.calls...)
}

func TestProviderAccessClientScopesAuthorizationAndRevokesExactLaunch(t *testing.T) {
	fixture := newProviderAccessFixture(t, "codex", "account", 2)
	lease, err := authorizeProviderAccess(fixture.Home, "account-1", "profile-1", "cheap-model", "", 2, "exact-profile-revision")
	if err != nil {
		t.Fatal(err)
	}
	if lease.Config != fixture.Config {
		t.Fatal("The client changed the Culler access configuration.")
	}
	if count, err := lease.requests(); err != nil || count != 2 {
		t.Fatalf("Host request evidence: %d, %v", count, err)
	}
	if err := lease.close(); err != nil {
		t.Fatal(err)
	}
	calls := fixture.recorded()
	if len(calls) != 3 {
		t.Fatalf("Unexpected host calls: %v", calls)
	}
	authorize := calls[0]
	if authorize["profile_revision"] != "exact-profile-revision" {
		t.Fatal("The authorization lost the selected profile revision.")
	}
	if authorize["credential"] != "account-1" || authorize["profile"] != "profile-1" || authorize["model"] != "cheap-model" || authorize["max_requests"] != float64(2) || authorize["launch_id"] == "" || authorize["run_id"] == "" {
		t.Fatalf("The authorization lost its scope: %v", authorize)
	}
	expiry := time.UnixMilli(int64(authorize["expires_at_ms"].(float64)))
	if expiry.Before(time.Now()) || expiry.After(time.Now().Add(21*time.Minute)) {
		t.Fatal("The authorization lifetime is unbounded.")
	}
	for _, call := range calls[1:] {
		if call["launch_id"] != authorize["launch_id"] || call["run_id"] != authorize["run_id"] || call["nonce"] == authorize["nonce"] {
			t.Fatal("A host request lost its launch identity or reused its nonce.")
		}
	}
}

func TestProviderAccessClientRejectsForeignAndSecretResponses(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"nonce":      func(response map[string]any) { response["nonce"] = "foreign" },
		"connection": func(response map[string]any) { response["credential"] = "foreign" },
		"version":    func(response map[string]any) { response["version"] = 2 },
		"secret":     func(response map[string]any) { response["access_token"] = "synthetic-provider-secret" },
		"refusal":    func(response map[string]any) { response["status"] = "error" },
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newProviderAccessFixture(t, "codex", "account", 1)
			fixture.answer = func(_ map[string]any, response map[string]any) map[string]any { change(response); return response }
			lease, err := fixture.access("connection", "profile", "model", 1)
			if err == nil || lease != nil || strings.Contains(err.Error(), "synthetic-provider-secret") {
				t.Fatalf("The client accepted or exposed a foreign response: %v", err)
			}
		})
	}
}

func TestProviderAccessClientRequiresOwnerOnlySocket(t *testing.T) {
	home, _ := residentAcceptanceHome(t)
	path := filepath.Join(home, "health", "provider-acceptance.sock")
	for _, mode := range []os.FileMode{0644, 0666} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := residentAcceptanceExchange(home, map[string]any{}); err == nil || !strings.Contains(err.Error(), "owner-only") {
			t.Fatalf("The client accepted mode %o: %v", mode, err)
		}
	}
	if err := os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path+".original", path); err != nil {
		t.Fatal(err)
	}
	if _, err := residentAcceptanceExchange(home, map[string]any{}); err == nil {
		t.Fatal("The client accepted a socket symlink.")
	}
}

func TestProviderAccessClientCancelsBlockedHostResponse(t *testing.T) {
	home, listener := residentAcceptanceHome(t)
	requested := make(chan struct{})
	closed := make(chan struct{})
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = bufio.NewReader(conn).ReadBytes('\n')
		close(requested)
		var data [1]byte
		_, _ = conn.Read(data[:])
		close(closed)
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := residentAcceptanceExchangeContext(ctx, home, map[string]any{"op": "profile_authorize"})
		done <- err
	}()
	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("The host request did not arrive.")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Cancellation was lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Cancellation left the host request active.")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Cancellation left the socket open.")
	}
}

func TestProviderAccessConfigurationRejectsUnsafeDestinationsAndTrust(t *testing.T) {
	fixture := newProviderAccessFixture(t, "codex", "account", 1)
	for name, change := range map[string]func(*ProviderContainerAccess){
		"plaintext":  func(v *ProviderContainerAccess) { v.URL = "http://host.docker.internal:4443" },
		"other host": func(v *ProviderContainerAccess) { v.URL = "https://example.invalid:4443" },
		"path":       func(v *ProviderContainerAccess) { v.URL += "/forward" },
		"query":      func(v *ProviderContainerAccess) { v.URL += "?upstream=other" },
		"userinfo":   func(v *ProviderContainerAccess) { v.URL = "https://user@host.docker.internal:4443" },
		"trust":      func(v *ProviderContainerAccess) { v.CA = "invalid" },
		"token":      func(v *ProviderContainerAccess) { v.Token = "short" },
		"harness":    func(v *ProviderContainerAccess) { v.Harness = "other" },
		"mode":       func(v *ProviderContainerAccess) { v.AuthMode = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			access := fixture.Config
			change(&access)
			if _, err := providerAccessHTTPClient(access); err == nil {
				t.Fatal("The client accepted unsafe provider access.")
			}
		})
	}
}

func TestProviderAccessHTTPClientRequiresInstallationTrustAndFixedHost(t *testing.T) {
	bundle, err := newProviderTLS(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair([]byte(bundle.Cert), []byte(bundle.Key))
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Location", "https://example.invalid")
		w.WriteHeader(302)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	access := ProviderContainerAccess{Version: 1, URL: "https://host.docker.internal:" + port, Token: "culler_proxy_" + strings.Repeat("t", 43), CA: bundle.CA, Harness: "codex", AuthMode: "api-key"}
	client, err := providerAccessHTTPClient(access)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(access.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 302 || requests != 1 {
		t.Fatal("The client followed a provider redirect.")
	}
	if _, err := client.Get("https://example.invalid"); err == nil {
		t.Fatal("The client accepted another host.")
	}
	other, err := newProviderTLS(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	access.CA = other.CA
	client, err = providerAccessHTTPClient(access)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(access.URL); err == nil || requests != 1 {
		t.Fatal("The client accepted a foreign certificate authority.")
	}
}
