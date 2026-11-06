package deployment

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"mc/opencodecatalog"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"
)

// ProviderContainerAccess contains Culler authority and public certificate trust.
type OpenCodeContainerAccess struct {
	Provider    string                `json:"provider"`
	KeyVariable string                `json:"keyVariable"`
	WebSearch   bool                  `json:"webSearch"`
	Model       opencodecatalog.Model `json:"model"`
}

type ProviderContainerAccess struct {
	OpenCode *OpenCodeContainerAccess `json:"opencode,omitempty"`
	Version  int                      `json:"version"`
	URL      string                   `json:"url"`
	Token    string                   `json:"token"`
	CA       string                   `json:"ca"`
	Harness  string                   `json:"harness"`
	AuthMode string                   `json:"authMode"`
}

var providerAccessTokenPattern = regexp.MustCompile(`^culler_proxy_[A-Za-z0-9_-]{43}$`)

type providerAccessLease struct {
	Config ProviderContainerAccess
	Home   string
	Run    string
	Launch string
	stop   func() error
}

func checkProviderAccessViaResident(home, connection string) error {
	socketHome, stop, err := providerAccessSocketHome(context.Background(), home)
	if err != nil {
		return err
	}
	defer stop()
	nonce, err := residentAcceptanceNonce()
	if err != nil {
		return err
	}
	body, err := residentAcceptanceExchange(socketHome, map[string]any{"version": 1, "op": "connection_check", "credential": connection, "nonce": nonce})
	var response struct {
		Status string `json:"status"`
		Nonce  string `json:"nonce"`
	}
	if err != nil || json.Unmarshal(body, &response) != nil || response.Status != "ok" || response.Nonce != nonce {
		return fmt.Errorf("The host could not verify connection access. Restore the resident, then retry.")
	}
	return nil
}

func authorizeProviderAccess(home, connection, profile, model, purpose string, maximum int, revision ...string) (*providerAccessLease, error) {
	return authorizeProviderAccessContext(context.Background(), home, connection, profile, model, purpose, maximum, revision...)
}

func authorizeProviderAccessContext(ctx context.Context, home, connection, profile, model, purpose string, maximum int, revision ...string) (*providerAccessLease, error) {
	socketHome, stop, err := providerAccessSocketHome(ctx, home)
	if err != nil {
		return nil, err
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = stop()
		}
	}()
	nonce, err := residentAcceptanceNonce()
	if err != nil {
		return nil, err
	}
	run := "acceptance-" + nonce
	defer func() {
		if !accepted {
			_ = (&providerAccessLease{Home: socketHome, Run: run, Launch: nonce}).close()
		}
	}()
	request := map[string]any{"version": 1, "op": "profile_authorize", "credential": connection,
		"profile": profile, "run_id": run, "launch_id": nonce, "model": model, "nonce": nonce,
		"expires_at_ms": time.Now().Add(20 * time.Minute).UnixMilli(), "max_requests": maximum}
	if len(revision) == 1 && revision[0] != "" {
		request["profile_revision"] = revision[0]
	}
	if purpose != "" {
		request["purpose"] = purpose
	}
	body, err := residentAcceptanceExchangeContext(ctx, socketHome, request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("The host provider proxy is unavailable. Restore the resident, then retry.")
	}
	var response struct {
		Version    int                     `json:"version"`
		Status     string                  `json:"status"`
		Credential string                  `json:"credential"`
		Nonce      string                  `json:"nonce"`
		Config     ProviderContainerAccess `json:"proxy_config"`
	}
	if decodeRuntimeAuthJSON(body, &response) != nil || response.Version != 1 || response.Status != "ok" ||
		response.Credential != connection || response.Nonce != nonce || validateProviderContainerAccess(response.Config) != nil {
		return nil, fmt.Errorf("The host provider access response is invalid.")
	}
	accepted = true
	return &providerAccessLease{Config: response.Config, Home: socketHome, Run: run, Launch: nonce, stop: stop}, nil
}

func validateProviderContainerAccess(access ProviderContainerAccess) error {
	target, err := url.Parse(access.URL)
	if err != nil || target.Scheme != "https" || target.Hostname() != providerProxyHostname || target.Port() == "" ||
		target.User != nil || target.RawQuery != "" || target.Fragment != "" || (access.Harness != "opencode" && target.Path != "" && target.Path != "/") ||
		access.Version != 1 || !providerAccessTokenPattern.MatchString(access.Token) ||
		(access.Harness != "codex" && access.Harness != "claude-sdk" && access.Harness != "opencode") || (access.AuthMode != "account" && access.AuthMode != "api-key") {
		return fmt.Errorf("The host provider access configuration is invalid.")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(access.CA)) {
		return fmt.Errorf("The host provider certificate authority is invalid.")
	}
	if access.Harness == "opencode" {
		if access.OpenCode == nil || access.AuthMode != "api-key" {
			return fmt.Errorf("The OpenCode access configuration is invalid.")
		}
		provider, ok := opencodecatalog.Find(access.OpenCode.Provider)
		model, found := provider.Model(access.OpenCode.Model.ID)
		address, addressErr := url.Parse(model.Address)
		if !ok || !found || addressErr != nil || access.OpenCode.KeyVariable != provider.KeyVariable || access.OpenCode.WebSearch != provider.WebSearch ||
			!reflect.DeepEqual(access.OpenCode.Model, model) || strings.TrimRight(address.Path, "/") != strings.TrimRight(target.Path, "/") {
			return fmt.Errorf("The OpenCode access differs from the release catalog.")
		}
	} else if access.OpenCode != nil {
		return fmt.Errorf("This harness cannot use OpenCode access.")
	}
	return nil
}

func (lease *providerAccessLease) close() (result error) {
	if lease.stop != nil {
		defer func() { result = errors.Join(result, lease.stop()) }()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	nonce, err := residentAcceptanceNonce()
	if err != nil {
		return err
	}
	body, err := residentAcceptanceExchangeContext(ctx, lease.Home, map[string]any{"version": 1, "op": "profile_revoke",
		"run_id": lease.Run, "launch_id": lease.Launch, "nonce": nonce})
	var response struct {
		Status  string `json:"status"`
		Nonce   string `json:"nonce"`
		Revoked bool   `json:"revoked"`
	}
	if err != nil || json.Unmarshal(body, &response) != nil || response.Status != "ok" || response.Nonce != nonce || !response.Revoked {
		return fmt.Errorf("The host could not confirm provider access cancellation.")
	}
	return nil
}

func (lease *providerAccessLease) requests() (int, error) {
	return lease.requestsContext(context.Background())
}
func (lease *providerAccessLease) requestsContext(ctx context.Context) (int, error) {
	nonce, err := residentAcceptanceNonce()
	if err != nil {
		return 0, err
	}
	body, err := residentAcceptanceExchangeContext(ctx, lease.Home, map[string]any{"version": 1, "op": "profile_status",
		"run_id": lease.Run, "launch_id": lease.Launch, "nonce": nonce})
	var response struct {
		Status   string `json:"status"`
		Nonce    string `json:"nonce"`
		Requests int    `json:"provider_requests"`
	}
	if err != nil || json.Unmarshal(body, &response) != nil || response.Status != "ok" || response.Nonce != nonce || response.Requests < 0 {
		return 0, fmt.Errorf("The host provider request evidence is unavailable.")
	}
	return response.Requests, nil
}

func providerAccessHTTPClient(access ProviderContainerAccess) (*http.Client, error) {
	if err := validateProviderContainerAccess(access); err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM([]byte(access.CA))
	target, _ := url.Parse(access.URL)
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: providerProxyHostname, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != target.Host {
				return nil, fmt.Errorf("The provider access destination changed.")
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", target.Port()))
		}}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
