package deployment

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	operatorconfig "mc/config"
)

var apiKeyValidationClient = &http.Client{Timeout: 10 * time.Second}

// APIKeyReplacementError contains only a safe provider check outcome.
type APIKeyReplacementError struct {
	Status          int
	InvalidResponse bool
}

func (err *APIKeyReplacementError) Error() string {
	switch err.Status {
	case http.StatusUnauthorized:
		return "The provider rejected the replacement key. Check the key and retry. The previous key remains saved."
	case http.StatusForbidden:
		return "The provider denied access to its model list for this key. Check its permissions and retry. The previous key remains saved."
	case http.StatusTooManyRequests:
		return "The provider limited the key check. Retry later. The previous key remains saved."
	}
	if err.InvalidResponse {
		return "The provider returned an unreadable response. Retry later. The previous key remains saved."
	}
	return "Culler could not check the replacement key. Check the connection and retry. The previous key remains saved."
}

func validateAPIKeyReplacement(home string, req AgentConnectionRequest, source operatorconfig.CredentialSource) (string, error) {
	if !agentAPIKeyKind(source.Kind) {
		return "", nil
	}
	id, err := agentProfileID(req.ID, req.SubmissionID)
	if err != nil {
		return "", err
	}
	if req.ID == "" {
		id = "connection-" + strings.TrimPrefix(id, "profile-")
	}
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		return "", err
	}
	identity, err := ReadProviderCredentialIdentity(home, id)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if identity.Type != "api_key" || cfg.Runtimes.Credentials[id].Kind != source.Kind {
		return "", fmt.Errorf("Create a new connection for a different authentication method.")
	}
	revision := agentProfileRevision(cfg)
	if req.Revision != revision || req.AuthorizationID != identity.AuthorizationID {
		return "", fmt.Errorf("The connection changed. Refresh the connection, then retry.")
	}
	client, err := apiKeyReplacementClient(req.APIKey)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := authenticateReplacementKey(ctx, client, source, req.APIKey); err != nil {
		return "", err
	}
	return revision, nil
}

func authenticateReplacementKey(ctx context.Context, client *http.Client, source operatorconfig.CredentialSource, key string) error {
	base := strings.TrimRight(source.InferenceURL, "/")
	target := base + "/models"
	headers := http.Header{"Accept": {"application/json"}}
	switch source.Kind {
	case operatorconfig.CredentialKindCodexAPIKey:
		headers.Set("Authorization", "Bearer "+key)
	case operatorconfig.CredentialKindClaudeAPIKey:
		target = base + "/v1/models?limit=1"
		headers.Set("x-api-key", key)
		headers.Set("anthropic-version", "2023-06-01")
	default:
		return fmt.Errorf("Select an API key connection.")
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	err := getAgentModels(ctx, client, target, headers, &body)
	if err != nil {
		var refusal *AgentModelListError
		if errors.As(err, &refusal) {
			return &APIKeyReplacementError{Status: refusal.Status}
		}
		return &APIKeyReplacementError{}
	}
	if body.Data == nil {
		return &APIKeyReplacementError{InvalidResponse: true}
	}
	for _, model := range body.Data {
		if strings.TrimSpace(model.ID) == "" {
			return &APIKeyReplacementError{InvalidResponse: true}
		}
	}
	return nil
}
