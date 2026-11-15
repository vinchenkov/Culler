package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	operatorconfig "mc/config"
)

const fixtureModelAccess = "fixture-culler-model-access"

func codexModelServer(t *testing.T, requests *int) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requests++
		if r.Method != http.MethodGet || r.URL.Path != "/backend-api/codex/models" || r.URL.Query().Get("client_version") != CodexClientVersion {
			t.Errorf("Unexpected model list request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer fixture-culler-model-access" || r.Header.Get("ChatGPT-Account-Id") != "" {
			t.Errorf("The model list request omitted the connection access")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]any{
			{"slug": "gpt-listed", "display_name": "GPT Listed", "visibility": "list"},
			{"slug": "gpt-hidden", "display_name": "GPT Hidden", "visibility": "hide"},
			{"slug": "gpt-listed", "display_name": "Duplicate", "visibility": "list"},
			{"slug": "gpt\nunsafe", "display_name": "Unsafe", "visibility": "list"},
			{"slug": "gpt-unnamed", "display_name": "", "visibility": "list"},
		}})
	}))
	t.Cleanup(server.Close)
	return server
}

func codexModelSource(server *httptest.Server) operatorconfig.CredentialSource {
	return operatorconfig.CredentialSource{Kind: operatorconfig.CredentialKindCodexOAuth, InferenceURL: server.URL + "/backend-api/codex/"}
}

func TestProviderModelsListsTheCodexModelsOfThePinnedVersion(t *testing.T) {
	requests := 0
	server := codexModelServer(t, &requests)
	listed, err := providerModels(context.Background(), server.Client(), codexModelSource(server), fixtureModelAccess, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []AgentModel{{ID: "gpt-listed", Name: "GPT Listed", EffortLevels: operatorconfig.EffortLevels("codex")}, {ID: "gpt-unnamed", Name: "gpt-unnamed", EffortLevels: operatorconfig.EffortLevels("codex")}}
	if !reflect.DeepEqual(listed, want) {
		t.Fatalf("Unexpected listed models: %+v", listed)
	}
	all, err := providerModels(context.Background(), server.Client(), codexModelSource(server), fixtureModelAccess, true)
	if err != nil || len(all) != 3 || all[1].ID != "gpt-hidden" {
		t.Fatalf("The version check omitted a hidden model: %+v %v", all, err)
	}
	if requests != 2 {
		t.Fatalf("Unexpected request count: %d", requests)
	}
}

func TestProviderModelsFollowsTheClaudeModelPages(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer fixture-culler-model-access" ||
			r.Header.Get("anthropic-beta") != "oauth-2025-04-20" || r.Header.Get("anthropic-version") == "" {
			t.Errorf("Unexpected model list request: %s %v", r.URL, r.Header)
		}
		if r.URL.Query().Get("after_id") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "claude-first", "display_name": "Claude First"}}, "has_more": true, "last_id": "claude-first"})
			return
		}
		if r.URL.Query().Get("after_id") != "claude-first" {
			t.Errorf("Unexpected page cursor: %s", r.URL)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "claude-second", "display_name": "Claude Second"}}, "has_more": false})
	}))
	defer server.Close()
	source := operatorconfig.CredentialSource{Kind: operatorconfig.CredentialKindClaudeOAuth, InferenceURL: server.URL}
	models, err := providerModels(context.Background(), server.Client(), source, fixtureModelAccess, false)
	want := []AgentModel{{ID: "claude-first", Name: "Claude First", EffortLevels: operatorconfig.EffortLevels("claude-sdk")}, {ID: "claude-second", Name: "Claude Second", EffortLevels: operatorconfig.EffortLevels("claude-sdk")}}
	if err != nil || !reflect.DeepEqual(models, want) {
		t.Fatalf("Unexpected Claude models: %+v %v", models, err)
	}
}

func TestProviderModelsReportsProviderFailuresWithoutTheToken(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"rejected access", http.StatusUnauthorized, `{}`, http.StatusUnauthorized},
		{"server failure", http.StatusBadGateway, `{}`, http.StatusBadGateway},
		{"redirect", http.StatusFound, `{}`, http.StatusFound},
		{"invalid body", http.StatusOK, `not json`, 0},
		{"empty list", http.StatusOK, `{"models":[]}`, 0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if testCase.status == http.StatusFound {
					w.Header().Set("Location", "/elsewhere")
				}
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(testCase.body))
			}))
			defer server.Close()
			_, err := providerModels(context.Background(), server.Client(), codexModelSource(server), fixtureModelAccess, false)
			var failure *AgentModelListError
			if !errors.As(err, &failure) || failure.Status != testCase.want {
				t.Fatalf("Unexpected failure: %#v", err)
			}
			if strings.Contains(err.Error(), fixtureModelAccess) {
				t.Fatal("The failure disclosed the access token")
			}
		})
	}
}

func TestModelTestStopsBeforeTheProviderRequestForAnAbsentCodexModel(t *testing.T) {
	requests := 0
	server := codexModelServer(t, &requests)
	previous := agentModelClient
	agentModelClient = server.Client()
	t.Cleanup(func() { agentModelClient = previous })
	if err := requireOfferedCodexModel(context.Background(), codexModelSource(server), fixtureModelAccess, "gpt-hidden"); err != nil {
		t.Fatalf("The check rejected an offered model: %v", err)
	}
	err := requireOfferedCodexModel(context.Background(), codexModelSource(server), fixtureModelAccess, "gpt-absent")
	var notOffered *AgentModelNotOfferedError
	if !errors.As(err, &notOffered) {
		t.Fatalf("The check accepted an absent model: %v", err)
	}
	code, message := agentProfileOperationError("model_test", err)
	if code != "model" || !strings.Contains(message, "gpt-absent") || !strings.Contains(message, CodexClientVersion) {
		t.Fatalf("Unexpected operation error: %s %s", code, message)
	}
	code, _ = agentProfileOperationError("model_test", &AgentModelListError{Status: http.StatusForbidden, Message: "rejected"})
	if code != "authorization" {
		t.Fatalf("A rejected model list did not require a reconnect: %s", code)
	}
}

func TestAgentConnectionModelsRequiresAConnectedSubscription(t *testing.T) {
	requests := 0
	server := codexModelServer(t, &requests)
	home := agentProfileTestHome(t)
	agentProfileAccountFixture(t, home, "endpoint", "endpoint-model")
	previousAccess := agentModelAccess
	agentModelAccess = func(_, credential string) (modelCatalogAccess, error) {
		if credential != "chatgpt" {
			t.Errorf("Unexpected provider access for %s", credential)
		}
		return modelCatalogAccess{Token: fixtureModelAccess, URL: server.URL + "/backend-api/codex/", Client: server.Client(), Close: func() {}}, nil
	}
	t.Cleanup(func() { agentModelAccess = previousAccess })
	for _, testCase := range []struct{ connection, want string }{
		{"absent", "Select an available connection"},
		{"endpoint", "Reconnect"},
		{"chatgpt", "Reconnect"},
	} {
		if _, err := AgentConnectionModels(home, AgentModelListRequest{ConnectionID: testCase.connection}); err == nil || !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("Connection %s: unexpected result %v", testCase.connection, err)
		}
	}
	if requests != 0 {
		t.Fatal("An unavailable connection sent a model list request")
	}
	catalog, err := LoadRuntimeCatalog(home)
	if err != nil {
		t.Fatal(err)
	}
	source := catalog.Credentials["chatgpt"]
	if err := PublishAgentConnectionGrant(home, "chatgpt", oauthRuntimeGrant{Credential: "chatgpt", Channel: source.Channel(), TokenURL: source.TokenURL, ClientID: source.ClientID, RefreshToken: "fixture-refresh"}); err != nil {
		t.Fatal(err)
	}
	list, err := AgentConnectionModels(home, AgentModelListRequest{ConnectionID: "chatgpt"})
	if err != nil || list.ConnectionID != "chatgpt" || len(list.Models) != 2 || list.Models[0].ID != "gpt-listed" {
		t.Fatalf("Unexpected model list: %+v %v", list, err)
	}
}

func TestCodexClientVersionMatchesTheRunnerPin(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "runner", "agent-runner", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Dependencies["@openai/codex"] != CodexClientVersion {
		t.Fatalf("The model list uses Codex %s, but the runner pins %s", CodexClientVersion, manifest.Dependencies["@openai/codex"])
	}
}

type fixtureRoundTripper func(*http.Request) (*http.Response, error)

func (f fixtureRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestProviderModelEffortCapabilities(t *testing.T) {
	for _, tc := range []struct {
		kind, body string
		levels     []string
		known      bool
	}{
		{operatorconfig.CredentialKindCodexOAuth, `{"models":[{"slug":"fixture-model","visibility":"list","supported_reasoning_levels":[{"effort":"high"},{"effort":"low"},{"effort":"future"}]}]}`, []string{"low", "high"}, true},
		{operatorconfig.CredentialKindCodexOAuth, `{"models":[{"slug":"fixture-model","visibility":"list","supported_reasoning_levels":[]}]}`, []string{}, true},
		{operatorconfig.CredentialKindClaudeAPIKey, `{"data":[{"id":"fixture-model","capabilities":{"effort":{"supported":true,"low":{"supported":true},"high":{"supported":true},"max":{"supported":false}}}}]}`, []string{"low", "high"}, true},
		{operatorconfig.CredentialKindClaudeOAuth, `{"data":[{"id":"fixture-model","capabilities":{"effort":{"supported":false}}}]}`, []string{}, true},
		{operatorconfig.CredentialKindClaudeOAuth, `{"data":[{"id":"fixture-model"}]}`, []string{"low", "medium", "high", "xhigh", "max"}, false},
	} {
		t.Run(tc.kind+tc.body, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer server.Close()
			models, err := providerModels(context.Background(), server.Client(), operatorconfig.CredentialSource{Kind: tc.kind, InferenceURL: server.URL}, fixtureModelAccess, false)
			if err != nil || len(models) != 1 {
				t.Fatalf("The model list failed: %v", err)
			}
			if !reflect.DeepEqual(models[0].EffortLevels, tc.levels) || models[0].EffortSupportKnown != tc.known {
				t.Fatalf("Unexpected efforts: %+v", models[0])
			}
			if err := validateAgentModelEffort(models, "fixture-model", "ultra"); err == nil {
				t.Fatal("The model accepted an unavailable effort.")
			}
			for _, level := range tc.levels {
				if err := validateAgentModelEffort(models, "fixture-model", level); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
