package deployment

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	operatorconfig "mc/config"
)

// CodexClientVersion is the Codex version in the runner image. The ChatGPT
// model list offers only the models that this version supports.
const CodexClientVersion = "0.156.1"

const (
	maxAgentModelListBytes = 8 << 20
	maxAgentModels         = 200
	maxAgentModelPages     = 5
)

type AgentModelListRequest struct {
	ConnectionID string `json:"connection_id"`
}

type AgentModel struct {
	Context            int      `json:"context,omitempty"`
	InputPrice         float64  `json:"input_price"`
	OutputPrice        float64  `json:"output_price"`
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	EffortLevels       []string `json:"effort_levels"`
	EffortSupportKnown bool     `json:"effort_support_known"`
}

type AgentModelList struct {
	WebSearch    bool         `json:"web_search"`
	ConnectionID string       `json:"connection_id"`
	Models       []AgentModel `json:"models"`
	// Source names the source of the model list of an OpenCode connection.
	Source string `json:"source,omitempty"`
	// Refreshing reports a refresh of the live models that waits or runs.
	Refreshing bool `json:"refreshing,omitempty"`
}

// AgentModelListError reports a model list request that the provider did not complete.
type AgentModelListError struct {
	Status  int
	Message string
}

func (e *AgentModelListError) Error() string { return e.Message }

// AgentModelNotOfferedError reports a model that is absent from the connection model list.
type AgentModelNotOfferedError struct {
	Model string
}

func (e *AgentModelNotOfferedError) Error() string {
	return fmt.Sprintf("The connection does not offer the model %s with Codex %s. Select a model from the list.", e.Model, CodexClientVersion)
}

type AgentModelEffortError struct{}

func (*AgentModelEffortError) Error() string {
	return "This model does not offer the selected effort. Select another effort level."
}

var agentModelClient = &http.Client{Timeout: 30 * time.Second}

type modelCatalogAccess struct {
	Token  string
	URL    string
	Client *http.Client
	Close  func()
}

var agentModelAccess = func(home, connection string) (modelCatalogAccess, error) {
	lease, err := authorizeProviderAccess(home, connection, "model-list", "", "model_list", maxAgentModelPages)
	if err != nil {
		return modelCatalogAccess{}, err
	}
	client, err := providerAccessHTTPClient(lease.Config)
	if err != nil {
		_ = lease.close()
		return modelCatalogAccess{}, err
	}
	return modelCatalogAccess{Token: lease.Config.Token, URL: lease.Config.URL, Client: client, Close: func() { _ = lease.close() }}, nil
}

func listsAgentModels(kind string) bool {
	return agentConnectionKind(kind)
}

// AgentConnectionModels returns the model list of a connection.
// A request for the model list of an OpenCode connection starts a refresh of the live models when one is due.
func AgentConnectionModels(home string, req AgentModelListRequest) (AgentModelList, error) {
	return agentConnectionModels(home, req, true)
}

func agentConnectionModels(home string, req AgentModelListRequest, refresh bool) (AgentModelList, error) {
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		return AgentModelList{}, err
	}
	source, ok := cfg.Runtimes.Credentials[req.ConnectionID]
	if !ok {
		return AgentModelList{}, fmt.Errorf("Select an available connection.")
	}
	if source.Kind == operatorconfig.CredentialKindOpenCodeAPIKey {
		provider, ok := OpenCodeProvider(home, source.CatalogEntry)
		list := OpenCodeModelSource(home)
		if refresh {
			provider, list, ok = OpenCodeModelList(home, source.CatalogEntry)
		}
		if !ok {
			return AgentModelList{}, fmt.Errorf("The release catalog lacks this provider. Select another provider.")
		}
		out := AgentModelList{ConnectionID: req.ConnectionID, WebSearch: provider.WebSearch, Models: []AgentModel{},
			Source: list, Refreshing: openCodeModelRefreshActive(home)}
		for _, model := range provider.Models {
			out.Models = append(out.Models, AgentModel{ID: model.ID, Name: model.Name, Context: model.Context, InputPrice: model.InputPrice, OutputPrice: model.OutputPrice, EffortLevels: model.Efforts(), EffortSupportKnown: true})
		}
		return out, nil
	}
	if !listsAgentModels(source.Kind) {
		return AgentModelList{}, fmt.Errorf("This connection does not list its models. Enter the model identifier.")
	}
	if agentConnectionAccess(home, req.ConnectionID, source) != "connected" {
		return AgentModelList{}, fmt.Errorf("The connection is disconnected. Reconnect, then retry.")
	}
	access, err := agentModelAccess(home, req.ConnectionID)
	if err != nil {
		return AgentModelList{}, err
	}
	defer access.Close()
	source.InferenceURL = access.URL
	// The dashboard ends a read after 15 seconds.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	models, err := providerModels(ctx, access.Client, source, access.Token, false)
	if err != nil {
		return AgentModelList{}, err
	}
	return AgentModelList{ConnectionID: req.ConnectionID, Models: models}, nil
}

// selectDefaultAgentModel follows the visible order of the native model catalog.
var selectDefaultAgentModel = func(ctx context.Context, home, connection string) (string, error) {
	catalog, err := LoadRuntimeCatalog(home)
	if err != nil {
		return "", err
	}
	source, exists := catalog.Credentials[connection]
	if !exists || source.Kind != operatorconfig.CredentialKindCodexOAuth {
		return "", fmt.Errorf("Select a model for this connection before the model test.")
	}
	access, err := agentModelAccess(home, connection)
	if err != nil {
		return "", err
	}
	defer access.Close()
	source.InferenceURL = access.URL
	models, err := providerModels(ctx, access.Client, source, access.Token, false)
	if err != nil {
		return "", err
	}
	return models[0].ID, nil
}

// requireOfferedCodexModel stops a model test before the provider request when
// the ChatGPT backend does not offer the model to the pinned Codex version.
func requireOfferedCodexModel(ctx context.Context, source operatorconfig.CredentialSource, token string, model string) error {
	models, err := providerModels(ctx, agentModelClient, source, token, true)
	if err != nil {
		return err
	}
	for _, offered := range models {
		if offered.ID == model {
			return nil
		}
	}
	return &AgentModelNotOfferedError{Model: model}
}

func providerModels(ctx context.Context, client *http.Client, source operatorconfig.CredentialSource, token string, includeHidden bool) ([]AgentModel, error) {
	base := strings.TrimRight(source.InferenceURL, "/")
	if base == "" {
		return nil, fmt.Errorf("The connection lacks its model endpoint. Repair the connection configuration with your coding harness.")
	}
	header := http.Header{"Authorization": {"Bearer " + token}, "Accept": {"application/json"}}
	models := []AgentModel{}
	seen := map[string]bool{}
	add := func(id, name string, levels []string) {
		binding := operatorconfig.RuntimeBinding{Harness: source.Harness(), Model: id}
		if seen[id] || len(models) == maxAgentModels || operatorconfig.ValidateBindingShape("model-list", binding, source.Kind) != nil {
			return
		}
		seen[id] = true
		if strings.TrimSpace(name) == "" || len(name) > 120 || strings.ContainsAny(name, "\x00\r\n\t") {
			name = id
		}
		known := levels != nil
		accepted := []string{}
		for _, level := range operatorconfig.EffortLevels(source.Harness()) {
			if !known || slices.Contains(levels, level) {
				accepted = append(accepted, level)
			}
		}
		models = append(models, AgentModel{ID: id, Name: name, EffortLevels: accepted, EffortSupportKnown: known})
	}
	switch source.Kind {
	case operatorconfig.CredentialKindCodexOAuth:
		var body struct {
			Models []struct {
				Slug             string `json:"slug"`
				DisplayName      string `json:"display_name"`
				Visibility       string `json:"visibility"`
				Priority         int    `json:"priority"`
				SupportedEfforts []struct {
					Effort string `json:"effort"`
				} `json:"supported_reasoning_levels"`
			} `json:"models"`
		}
		if err := getAgentModels(ctx, client, base+"/models?client_version="+url.QueryEscape(CodexClientVersion), header, &body); err != nil {
			return nil, err
		}
		sort.SliceStable(body.Models, func(i, j int) bool { return body.Models[i].Priority < body.Models[j].Priority })
		for _, model := range body.Models {
			if includeHidden || model.Visibility == "list" {
				var levels []string
				if model.SupportedEfforts != nil {
					levels = []string{}
					for _, effort := range model.SupportedEfforts {
						levels = append(levels, effort.Effort)
					}
				}
				add(model.Slug, model.DisplayName, levels)
			}
		}
	case operatorconfig.CredentialKindCodexAPIKey:
		var body struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := getAgentModels(ctx, client, base+"/models", header, &body); err != nil {
			return nil, err
		}
		for _, model := range body.Data {
			add(model.ID, model.ID, nil)
		}
	case operatorconfig.CredentialKindClaudeOAuth, operatorconfig.CredentialKindClaudeAPIKey:
		header.Set("anthropic-version", "2023-06-01")
		if source.Kind == operatorconfig.CredentialKindClaudeOAuth {
			header.Set("anthropic-beta", "oauth-2025-04-20")
		}
		after := ""
		for page := 0; page < maxAgentModelPages; page++ {
			query := url.Values{"limit": {"1000"}}
			if after != "" {
				query.Set("after_id", after)
			}
			var body struct {
				Data []struct {
					ID           string `json:"id"`
					DisplayName  string `json:"display_name"`
					Capabilities struct {
						Effort json.RawMessage `json:"effort"`
					} `json:"capabilities"`
				} `json:"data"`
				HasMore bool   `json:"has_more"`
				LastID  string `json:"last_id"`
			}
			if err := getAgentModels(ctx, client, base+"/v1/models?"+query.Encode(), header, &body); err != nil {
				return nil, err
			}
			for _, model := range body.Data {
				var levels []string
				if len(model.Capabilities.Effort) > 0 && string(model.Capabilities.Effort) != "null" {
					var capability struct {
						Supported bool `json:"supported"`
					}
					if err := json.Unmarshal(model.Capabilities.Effort, &capability); err != nil {
						return nil, err
					}
					var raw map[string]json.RawMessage
					if err := json.Unmarshal(model.Capabilities.Effort, &raw); err != nil {
						return nil, err
					}
					levels = []string{}
					if capability.Supported {
						for _, level := range operatorconfig.EffortLevels(source.Harness()) {
							if len(raw[level]) == 0 {
								continue
							}
							var support struct {
								Supported bool `json:"supported"`
							}
							if err := json.Unmarshal(raw[level], &support); err != nil {
								return nil, err
							}
							if support.Supported {
								levels = append(levels, level)
							}
						}
					}
				}
				add(model.ID, model.DisplayName, levels)
			}
			if !body.HasMore || body.LastID == "" {
				break
			}
			after = body.LastID
		}
	default:
		return nil, fmt.Errorf("This connection does not list its models. Enter the model identifier.")
	}
	if len(models) == 0 {
		return nil, &AgentModelListError{Message: "The provider listed no models for this connection. Reconnect, then retry."}
	}
	return models, nil
}

func getAgentModels(ctx context.Context, client *http.Client, target string, header http.Header, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header = header.Clone()
	// A redirect could send the access token to another host.
	direct := *client
	direct.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := direct.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return &AgentModelListError{Message: "The provider model list exceeded its time limit. Retry later."}
		}
		return &AgentModelListError{Message: "The provider model list is unavailable. Check the network connection, then retry."}
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxAgentModelListBytes+1))
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return &AgentModelListError{Status: res.StatusCode, Message: "The provider rejected the connection access. Reconnect, then retry."}
	case res.StatusCode < 200 || res.StatusCode >= 300:
		return &AgentModelListError{Status: res.StatusCode, Message: fmt.Sprintf("The provider model list returned HTTP %d. Retry later.", res.StatusCode)}
	case err != nil || len(body) > maxAgentModelListBytes:
		return &AgentModelListError{Message: "The provider model list exceeded its size limit."}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &AgentModelListError{Message: "The provider model list is invalid. Retry later."}
	}
	return nil
}

func validateAgentModelEffort(models []AgentModel, model, effort string) error {
	for _, offered := range models {
		if offered.ID == model && !slices.Contains(offered.EffortLevels, effort) {
			return &AgentModelEffortError{}
		}
	}
	return nil
}
