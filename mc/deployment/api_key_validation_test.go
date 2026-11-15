package deployment

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	operatorconfig "mc/config"
)

func useAPIKeyValidationResponse(t *testing.T, status int, body string) {
	t.Helper()
	previous := apiKeyValidationClient
	apiKeyValidationClient = &http.Client{Transport: fixtureRoundTripper(func(request *http.Request) (*http.Response, error) {
		if status == 0 {
			return nil, errors.New("synthetic transport failure")
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	t.Cleanup(func() { apiKeyValidationClient = previous })
}

func apiKeyReplacementFixture(t *testing.T, template string) (string, AgentConnectionRequest, ProviderCredentialAccess) {
	t.Helper()
	home := agentProfileTestHome(t)
	catalog, err := AgentProfileList(home)
	if err != nil {
		t.Fatal(err)
	}
	req := AgentConnectionRequest{SubmissionID: "first-key", Revision: catalog.Revision, Name: "Preserved connection", TemplateID: template, APIKey: "synthetic-original-key"}
	saved, err := AgentConnectionSave(home, req)
	if err != nil {
		t.Fatal(err)
	}
	access, err := ReadProviderCredential(home, saved.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	req.ID, req.SubmissionID, req.Revision, req.AuthorizationID = saved.ConnectionID, "replacement-key", saved.Revision, access.AuthorizationID
	req.APIKey = "synthetic-replacement-key"
	return home, req, access
}

func TestRejectedAPIKeyReplacementPreservesCredentialAndConfiguration(t *testing.T) {
	for _, template := range []string{"codex-api-key", "claude-api-key"} {
		for _, status := range []int{401, 403, 429, 503, 0} {
			t.Run(template+"/"+http.StatusText(status), func(t *testing.T) {
				useAPIKeyValidationResponse(t, status, `{"error":{"message":"synthetic-replacement-key"}}`)
				home, req, before := apiKeyReplacementFixture(t, template)
				configBefore, _ := os.ReadFile(filepath.Join(home, "config.toml"))
				req.Name = "Replacement name"
				_, err := AgentConnectionSave(home, req)
				var failure *APIKeyReplacementError
				if !errors.As(err, &failure) || failure.Status != status || !strings.Contains(err.Error(), "previous key remains saved") || strings.Contains(err.Error(), req.APIKey) {
					t.Fatal("The failed key check did not return its safe preservation message.")
				}
				after, err := ReadProviderCredential(home, req.ID)
				configAfter, _ := os.ReadFile(filepath.Join(home, "config.toml"))
				if err != nil || after != before || string(configBefore) != string(configAfter) {
					t.Fatal("The failed replacement changed the saved credential or configuration.")
				}
			})
		}
	}
}

func TestAPIKeyReplacementChecksOneCatalogRequestAndCompletedReplayDoesNotRepeatIt(t *testing.T) {
	for _, template := range []string{"codex-api-key", "claude-api-key"} {
		t.Run(template, func(t *testing.T) {
			useAPIKeyValidationResponse(t, 200, `{"data":[{"id":"fixture-model"}]}`)
			calls := 0
			apiKeyValidationClient.Transport = fixtureRoundTripper(func(request *http.Request) (*http.Response, error) {
				calls++
				if request.Method != http.MethodGet || request.URL.Scheme != "https" || request.Body != nil {
					t.Fatal("The replacement requested model inference.")
				}
				if template == "codex-api-key" {
					if request.Header.Get("Authorization") != "Bearer synthetic-replacement-key" || request.Header.Get("x-api-key") != "" || request.URL.Path != "/v1/models" {
						t.Fatal("The API catalog request differs.")
					}
				} else if request.Header.Get("x-api-key") != "synthetic-replacement-key" || request.Header.Get("Authorization") != "" || request.URL.Path != "/v1/models" || request.URL.Query().Get("limit") != "1" {
					t.Fatal("The API catalog request differs.")
				}
				if deadline, ok := request.Context().Deadline(); !ok || time.Until(deadline) > 10*time.Second {
					t.Fatal("The key check lacks its deadline.")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"fixture-model"}]}`)), Request: request}, nil
			})
			home, req, before := apiKeyReplacementFixture(t, template)
			if calls != 0 {
				t.Fatal("The first key used replacement validation.")
			}
			if _, err := AgentConnectionSave(home, req); err != nil {
				t.Fatal(err)
			}
			if _, err := AgentConnectionSave(home, req); err != nil {
				t.Fatal(err)
			}
			after, err := ReadProviderCredential(home, req.ID)
			if err != nil || calls != 1 || after.AuthorizationID == before.AuthorizationID || after.Record.APIKey != req.APIKey {
				t.Fatal("The validated replacement or its replay differs.")
			}
		})
	}
}

func TestAPIKeyReplacementRechecksConfigurationAndAuthorizationAfterProviderCheck(t *testing.T) {
	for _, change := range []string{"configuration", "authorization"} {
		t.Run(change, func(t *testing.T) {
			useAPIKeyValidationResponse(t, 200, `{"data":[]}`)
			home, req, before := apiKeyReplacementFixture(t, "codex-api-key")
			apiKeyValidationClient.Transport = fixtureRoundTripper(func(request *http.Request) (*http.Response, error) {
				if change == "configuration" {
					_, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), func(cfg *operatorconfig.Config) error {
						source := cfg.Runtimes.Credentials["claude"]
						source.Name = "New connection name"
						cfg.Runtimes.Credentials["claude"] = source
						return nil
					})
					if err != nil {
						t.Fatal(err)
					}
				} else {
					record := before.Record
					record.APIKey = "synthetic-newer-key"
					if err := PublishAgentConnectionGrant(home, req.ID, record); err != nil {
						t.Fatal(err)
					}
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":[]}`)), Request: request}, nil
			})
			if _, err := AgentConnectionSave(home, req); err == nil {
				t.Fatal("The delayed key check replaced newer state.")
			}
			after, err := ReadProviderCredential(home, req.ID)
			if err != nil || after.Record.APIKey == req.APIKey || change == "configuration" && after != before || change == "authorization" && after.Record.APIKey != "synthetic-newer-key" {
				t.Fatal("The delayed replacement changed the newer credential.")
			}
		})
	}
}

func TestAPIKeyReplacementRejectsRedirectsAndInvalidCatalogsWithoutSecretErrors(t *testing.T) {
	for _, value := range []struct {
		name, body string
		status     int
	}{
		{"redirect", "synthetic-replacement-key", 302}, {"malformed", "synthetic-replacement-key", 200},
		{"missing", `{}`, 200}, {"invalid-entry", `{"data":[{}]}`, 200},
		{"oversized", strings.Repeat("x", maxAgentModelListBytes+1), 200},
	} {
		t.Run(value.name, func(t *testing.T) {
			useAPIKeyValidationResponse(t, value.status, value.body)
			calls := 0
			original := apiKeyValidationClient.Transport
			apiKeyValidationClient.Transport = fixtureRoundTripper(func(request *http.Request) (*http.Response, error) {
				calls++
				response, err := original.RoundTrip(request)
				if response != nil {
					response.Header.Set("Location", "https://unrelated.example.invalid")
				}
				return response, err
			})
			source, _ := agentSubscriptionIntegration("codex-api-key")
			err := authenticateReplacementKey(context.Background(), apiKeyValidationClient, source, "synthetic-replacement-key")
			if err == nil || strings.Contains(err.Error(), "synthetic-replacement-key") || calls != 1 {
				t.Fatal("The key check accepted an invalid response, followed a redirect, or exposed its key.")
			}
		})
	}
}

type cancelledCatalogBody struct{ ctx context.Context }

func (body cancelledCatalogBody) Read([]byte) (int, error) {
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}
func (cancelledCatalogBody) Close() error { return nil }

func TestAPIKeyReplacementBoundsSlowResponseBodies(t *testing.T) {
	useAPIKeyValidationResponse(t, 200, `{"data":[]}`)
	apiKeyValidationClient.Transport = fixtureRoundTripper(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: cancelledCatalogBody{request.Context()}, Request: request}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	source, _ := agentSubscriptionIntegration("codex-api-key")
	if err := authenticateReplacementKey(ctx, apiKeyValidationClient, source, "synthetic-replacement-key"); err == nil || ctx.Err() == nil {
		t.Fatal("The slow catalog response escaped its deadline.")
	}
}
