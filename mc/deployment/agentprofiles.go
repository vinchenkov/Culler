package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	operatorconfig "mc/config"
	"mc/opencodecatalog"
	"mc/routing"
)

type AgentProfileRequest struct {
	SubmissionID string `json:"submission_id"`
	Revision     string `json:"revision"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	ConnectionID string `json:"connection_id"`
	Model        string `json:"model"`
	Effort       string `json:"effort,omitempty"`
	BaseURL      string `json:"-"`
	Enabled      bool   `json:"enabled"`
	TestID       string `json:"test_id"`
}

type AgentConnectionRequest struct {
	SubmissionID    string `json:"submission_id"`
	Revision        string `json:"revision"`
	ID              string `json:"id"`
	Name            string `json:"name"`
	Kind            string `json:"-"`
	TemplateID      string `json:"template_id"`
	BaseURL         string `json:"-"`
	Token           string `json:"-"`
	APIKey          string `json:"api_key,omitempty"`
	AuthorizationID string `json:"authorization_id,omitempty"`
}

type AgentProfileView struct {
	Problem        string   `json:"problem,omitempty"`
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Model          string   `json:"model"`
	Effort         string   `json:"effort,omitempty"`
	ConnectionID   string   `json:"connection_id"`
	ConnectionName string   `json:"connection_name"`
	Harness        string   `json:"harness"`
	Enabled        bool     `json:"enabled"`
	AccessStatus   string   `json:"access_status"`
	Roles          []string `json:"roles"`
}

type AgentConnectionView struct {
	CatalogEntry           string   `json:"catalog_entry,omitempty"`
	Address                string   `json:"address,omitempty"`
	Website                string   `json:"website,omitempty"`
	WebSearch              bool     `json:"web_search"`
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	Kind                   string   `json:"kind"`
	Harness                string   `json:"harness"`
	AccessStatus           string   `json:"access_status"`
	Models                 []string `json:"models"`
	AuthorizationAvailable bool     `json:"authorization_available"`
	AuthorizationMessage   string   `json:"authorization_message,omitempty"`
	AuthorizationID        string   `json:"authorization_id,omitempty"`
}

type AgentProfileCatalog struct {
	OpenCode     *AgentProfileOperation `json:"opencode,omitempty"`
	Revision     string                 `json:"revision"`
	Profiles     []AgentProfileView     `json:"profiles"`
	Connections  []AgentConnectionView  `json:"connections"`
	Providers    []AgentConnectionView  `json:"providers"`
	Integrations []AgentConnectionView  `json:"integrations"`
	ProfileID    string                 `json:"profile_id,omitempty"`
	ConnectionID string                 `json:"connection_id,omitempty"`
}

func agentProfileRevision(cfg operatorconfig.Config) string {
	body, _ := json.Marshal(cfg.Runtimes)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func agentDisplayName(id, name string) string {
	if name != "" {
		return name
	}
	return id
}

func agentProfileID(id, submission string) (string, error) {
	if id == "" {
		if submission == "" || len(submission) > 128 {
			return "", fmt.Errorf("A submission identifier is required. Retry the form.")
		}
		sum := sha256.Sum256([]byte(submission))
		id = "profile-" + hex.EncodeToString(sum[:12])
	}
	if len(id) > 64 || id == "" {
		return "", fmt.Errorf("The identifier is invalid.")
	}
	for i, r := range id {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || i > 0 && (r == '-' || r == '_' || r == '.') {
			continue
		}
		return "", fmt.Errorf("The identifier is invalid.")
	}
	return id, nil
}

func validateAgentName(name string) error {
	if name == "" || strings.TrimSpace(name) != name || len(name) > 128 || !utf8.ValidString(name) {
		return fmt.Errorf("Enter a name with 1 to 128 characters.")
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return fmt.Errorf("The name contains an invalid character.")
		}
	}
	return nil
}

func validateAgentProfileName(name string) error {
	if strings.IndexFunc(name, unicode.IsSpace) >= 0 {
		return fmt.Errorf("Enter an agent profile name without spaces.")
	}
	return validateAgentName(name)
}

func agentConnectionAccess(home, id string, source operatorconfig.CredentialSource) string {
	if !agentConnectionKind(source.Kind) {
		return "disconnected"
	}
	body, err := readConnectionCredentialBody(home, id)
	if err != nil {
		if errors.Is(err, ErrHostCredentialUnavailable) {
			return "unavailable"
		}
		return "disconnected"
	}
	defer clear(body)
	record, err := normalizeProviderCredential(id, json.RawMessage(body))
	if err == nil && record.Channel == source.Channel() && ((agentAPIKeyKind(source.Kind) && record.Type == "api_key") ||
		(agentSubscriptionKind(source.Kind) && record.Type == "account" && record.TokenURL == source.TokenURL && record.ClientID == source.ClientID)) {
		return "connected"
	}
	return "disconnected"
}

func AgentProfileList(home string) (AgentProfileCatalog, error) { return agentProfileList(home, true) }

// AgentProfileSystemCatalog reads configuration without credential or operation effects.
func AgentProfileSystemCatalog(home string) (AgentProfileCatalog, error) {
	return agentProfileList(home, false)
}

// agentProfileRoles maps each agent profile to the roles that routing.md assigns to it.
func agentProfileRoles(home string, cfg operatorconfig.Config) (map[string][]string, error) {
	roles := map[string][]string{}
	body, err := os.ReadFile(filepath.Join(home, "routing.md"))
	if os.IsNotExist(err) {
		return roles, nil
	}
	if err != nil {
		return nil, err
	}
	registry := routing.Registry{}
	for id, b := range cfg.Runtimes.Bindings {
		registry[id] = b.Harness
	}
	if _, fake := routing.ActiveRegistry(); fake {
		registry["fake"] = "fake"
	}
	table, err := routing.Parse(body, registry)
	if err != nil {
		return nil, err
	}
	for _, row := range table.Rows() {
		roles[row.Binding] = append(roles[row.Binding], row.Role)
	}
	return roles, nil
}

func agentProfileList(home string, includeEvidence bool) (AgentProfileCatalog, error) {
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		return AgentProfileCatalog{}, err
	}
	out := AgentProfileCatalog{Revision: agentProfileRevision(cfg), Profiles: []AgentProfileView{}, Connections: []AgentConnectionView{}}
	// The list shows no roles when routing.md is unreadable.
	roles, _ := agentProfileRoles(home, cfg)
	connectionIDs := []string{}
	for id := range cfg.Runtimes.Credentials {
		connectionIDs = append(connectionIDs, id)
	}
	sort.Strings(connectionIDs)
	views := map[string]AgentConnectionView{}
	for _, id := range connectionIDs {
		source := cfg.Runtimes.Credentials[id]
		view := AgentConnectionView{CatalogEntry: source.CatalogEntry, ID: id, Name: agentDisplayName(id, source.Name), Kind: source.Kind, Harness: source.Harness(), AccessStatus: "unknown", Models: []string{}}
		if source.Kind == operatorconfig.CredentialKindOpenCodeAPIKey {
			if provider, ok := opencodecatalog.Find(source.CatalogEntry); ok {
				view.WebSearch, view.Website = provider.WebSearch, provider.Website
			}
		}
		if includeEvidence {
			view.AccessStatus = agentConnectionAccess(home, id, source)
			view.AuthorizationID, _ = ReadConnectionAuthorizationIdentity(home, id)
		}
		seen := map[string]bool{}
		for _, b := range cfg.Runtimes.Bindings {
			if b.Credential == id {
				if !seen[b.Model] {
					view.Models = append(view.Models, b.Model)
					seen[b.Model] = true
				}
			}
		}
		sort.Strings(view.Models)
		views[id] = view
		out.Connections = append(out.Connections, view)
	}
	out.Integrations = agentSubscriptionIntegrations()
	out.Providers = AgentProviderCatalog()
	out.OpenCode = OpenCodeDownloadStatus(home)
	ids := []string{}
	for id := range cfg.Runtimes.Bindings {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		b := cfg.Runtimes.Bindings[id]
		connection := views[b.Credential]
		assigned := roles[id]
		if assigned == nil {
			assigned = []string{}
		}
		problem := ""
		if b.Harness == "opencode" {
			if err := ValidateOpenCodeProfile(home, connection.CatalogEntry, b.Model, b.Effort); err != nil {
				problem = err.Error()
			}
		}
		out.Profiles = append(out.Profiles, AgentProfileView{Problem: problem, ID: id, Name: agentDisplayName(id, b.Name), Model: b.Model, Effort: b.Effort, ConnectionID: b.Credential, ConnectionName: connection.Name, Harness: b.Harness, Enabled: agentEnabled(cfg, id), AccessStatus: connection.AccessStatus, Roles: assigned})
	}
	return out, nil
}

func agentSubscriptionIntegration(id string) (operatorconfig.CredentialSource, bool) {
	if strings.HasPrefix(id, "opencode:") {
		provider, ok := opencodecatalog.Find(strings.TrimPrefix(id, "opencode:"))
		return operatorconfig.CredentialSource{Kind: operatorconfig.CredentialKindOpenCodeAPIKey, CatalogEntry: provider.ID, Name: provider.Name}, ok
	}
	source, ok := operatorconfig.Default().Runtimes.Credentials[id]
	return source, ok && agentConnectionKind(source.Kind)
}

func agentSubscriptionKind(kind string) bool {
	return kind == operatorconfig.CredentialKindCodexOAuth || kind == operatorconfig.CredentialKindClaudeOAuth
}

func agentAPIKeyKind(kind string) bool {
	return kind == operatorconfig.CredentialKindCodexAPIKey || kind == operatorconfig.CredentialKindClaudeAPIKey || kind == operatorconfig.CredentialKindOpenCodeAPIKey
}

func agentConnectionKind(kind string) bool {
	return agentSubscriptionKind(kind) || agentAPIKeyKind(kind)
}

// The configuration template supplies the subscription integrations. Each
// deployment offers them without regard to its own connections.
func agentSubscriptionIntegrations() []AgentConnectionView {
	reference := operatorconfig.Default().Runtimes.Credentials
	ids := []string{}
	for id, source := range reference {
		if agentConnectionKind(source.Kind) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := []AgentConnectionView{}
	for _, id := range ids {
		source := reference[id]
		view := AgentConnectionView{ID: id, Name: agentDisplayName(id, source.Name), Kind: source.Kind, Harness: source.Harness(), Models: []string{}}
		view.AuthorizationAvailable, view.AuthorizationMessage = AgentProfileAuthorizationCapability(source.Kind)
		out = append(out, view)
	}
	return out
}

func agentEnabled(cfg operatorconfig.Config, id string) bool {
	for _, enabled := range cfg.Runtimes.EnabledBindings {
		if enabled == id {
			return true
		}
	}
	return false
}

func resolveAgentProfile(home string, cfg operatorconfig.Config, req AgentProfileRequest) (operatorconfig.RuntimeBinding, string, error) {
	id, err := agentProfileID(req.ID, req.SubmissionID)
	if err != nil {
		return operatorconfig.RuntimeBinding{}, "", err
	}
	if err := validateAgentProfileName(req.Name); err != nil {
		return operatorconfig.RuntimeBinding{}, "", err
	}
	source, ok := cfg.Runtimes.Credentials[req.ConnectionID]
	if !ok {
		return operatorconfig.RuntimeBinding{}, "", fmt.Errorf("Select an available connection.")
	}
	if req.BaseURL != "" {
		return operatorconfig.RuntimeBinding{}, "", fmt.Errorf("The agent profile does not accept a provider address.")
	}
	b := operatorconfig.RuntimeBinding{Name: req.Name, Harness: source.Harness(), Model: req.Model, Effort: req.Effort, Credential: req.ConnectionID}
	if err := operatorconfig.ValidateBindingShape(id, b, source.Kind); err != nil {
		return b, "", fmt.Errorf("Check the model: %w", err)
	}
	if source.Kind == operatorconfig.CredentialKindOpenCodeAPIKey {
		if err := ValidateOpenCodeProfile(home, source.CatalogEntry, b.Model, b.Effort); err != nil {
			return b, "", err
		}
	}
	return b, id, nil
}

func ResolveAgentProfile(home string, req AgentProfileRequest) (RuntimeCatalog, string, error) {
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		return RuntimeCatalog{}, "", err
	}
	b, id, err := resolveAgentProfile(home, cfg, req)
	if err != nil {
		return RuntimeCatalog{}, "", err
	}
	cfg.Runtimes.Bindings[id] = b
	return CatalogFromRuntimes(cfg.Runtimes), id, nil
}

func saveAgentProfile(home string, req AgentProfileRequest) (AgentProfileCatalog, error) {
	var profileID string
	_, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), func(cfg *operatorconfig.Config) error {
		b, id, err := resolveAgentProfile(home, *cfg, req)
		if err != nil {
			return err
		}
		profileID = id
		current, exists := cfg.Runtimes.Bindings[id]
		if exists && reflect.DeepEqual(current, b) && agentEnabled(*cfg, id) == req.Enabled {
			return nil
		}
		if req.Revision == "" || req.Revision != agentProfileRevision(*cfg) {
			return fmt.Errorf("The configuration changed. Refresh the agent profiles and retry.")
		}
		for other, profile := range cfg.Runtimes.Bindings {
			if other != id && strings.EqualFold(agentDisplayName(other, profile.Name), req.Name) {
				return fmt.Errorf("An agent profile already uses this name.")
			}
		}
		if req.Enabled {
			if err := VerifyAgentProfileTest(home, req.TestID, req); err != nil {
				return err
			}
		}
		if exists && agentEnabled(*cfg, id) && !req.Enabled {
			return fmt.Errorf("This agent profile is enabled. Save a new disabled agent profile.")
		}
		cfg.Runtimes.Bindings[id] = b
		if req.Enabled && !agentEnabled(*cfg, id) {
			cfg.Runtimes.EnabledBindings = append(cfg.Runtimes.EnabledBindings, id)
		}
		return cfg.Validate()
	})
	if err != nil {
		return AgentProfileCatalog{}, err
	}
	out, err := AgentProfileList(home)
	out.ProfileID = profileID
	return out, err
}

func saveAgentConnection(home string, req AgentConnectionRequest, validatedRevision string) (AgentProfileCatalog, error) {
	id, err := agentProfileID(req.ID, req.SubmissionID)
	if err != nil {
		return AgentProfileCatalog{}, err
	}
	if req.ID == "" {
		id = "connection-" + strings.TrimPrefix(id, "profile-")
	}
	if err := validateAgentName(req.Name); err != nil {
		return AgentProfileCatalog{}, err
	}
	err = operatorconfig.UpdateAtomicWithRecovery(filepath.Join(home, "config.toml"), func(cfg *operatorconfig.Config) error {
		if validatedRevision != "" && agentProfileRevision(*cfg) != validatedRevision {
			return fmt.Errorf("The configuration changed during the key check. Refresh the connection, then retry.")
		}
		source, ok := agentSubscriptionIntegration(req.TemplateID)
		if !ok {
			return fmt.Errorf("Select a supported connection.")
		}
		source.Name = req.Name
		current, exists := cfg.Runtimes.Credentials[id]
		same := exists && reflect.DeepEqual(current, source)
		if !same && (req.Revision == "" || req.Revision != agentProfileRevision(*cfg)) {
			return fmt.Errorf("The configuration changed. Refresh the connections and retry.")
		}
		for other, c := range cfg.Runtimes.Credentials {
			if other != id && strings.EqualFold(agentDisplayName(other, c.Name), req.Name) {
				return fmt.Errorf("A connection already uses this name.")
			}
		}
		if exists && !same {
			for _, b := range cfg.Runtimes.Bindings {
				if b.Credential == id {
					return fmt.Errorf("This connection has agent profiles. Create a new connection for different access settings.")
				}
			}
		}
		if exists && current.Kind != source.Kind {
			return fmt.Errorf("Create a new connection for a different authentication method.")
		}
		cfg.Runtimes.Credentials[id] = source
		if err := cfg.Validate(); err != nil {
			return err
		}
		return nil
	}, func(cfg operatorconfig.Config) error {
		source := cfg.Runtimes.Credentials[id]
		if req.APIKey != "" {
			return withAgentConnectionLock(home, id, func() error {
				identity, err := agentConnectionIdentityLocked(home, id)
				if err != nil {
					return err
				}
				if identity != req.AuthorizationID {
					return fmt.Errorf("The connection authorization changed. Refresh the connection, then retry.")
				}
				return publishAgentConnectionAuthorization(home, id, ProviderCredentialRecord{
					Credential: id, Channel: source.Channel(), Type: "api_key", APIKey: req.APIKey,
				})
			})
		}
		return nil
	}, func(operatorconfig.Config) error { return nil })
	if err != nil {
		return AgentProfileCatalog{}, err
	}
	out, err := AgentProfileList(home)
	out.ConnectionID = id
	return out, err
}

func PublishAgentConnectionGrant(home, id string, grant any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	release, err := AcquireConnectionOwner(ctx, home, id)
	if err != nil {
		return err
	}
	defer release()
	return withAgentConnectionLock(home, id, func() error { return publishAgentConnectionAuthorization(home, id, grant) })
}

func publishAgentConnectionGrant(home, id string, grant any) error {
	identity, err := agentConnectionIdentityLocked(home, id)
	if err != nil {
		return err
	}
	if identity == "" {
		identity, err = newLiveProviderAttemptID()
		if err != nil {
			return err
		}
	}
	return storeProviderCredential(home, id, identity, grant)
}

// AgentProviderCatalog matches release integrations by their model addresses.
func AgentProviderCatalog() []AgentConnectionView {
	out := []AgentConnectionView{}
	for _, provider := range opencodecatalog.Release().Providers {
		view := AgentConnectionView{ID: "opencode:" + provider.ID, Name: provider.Name, Kind: operatorconfig.CredentialKindOpenCodeAPIKey, Harness: "opencode", CatalogEntry: provider.ID, Website: provider.Website, WebSearch: provider.WebSearch, Models: []string{}}
		addresses := []string{}
		for _, model := range provider.Models {
			if !slices.Contains(addresses, model.Address) {
				addresses = append(addresses, model.Address)
			}
		}
		view.Address = strings.Join(addresses, ", ")
		for id, source := range operatorconfig.Default().Runtimes.Credentials {
			if !agentAPIKeyKind(source.Kind) {
				continue
			}
			integrationAddress := strings.TrimRight(source.InferenceURL, "/")
			// The Claude SDK takes the service origin; its API paths start with /v1.
			if source.Harness() == "claude-sdk" {
				integrationAddress += "/v1"
			}
			for _, address := range addresses {
				if strings.TrimRight(address, "/") == integrationAddress {
					view.ID = id
					view.Kind = source.Kind
					view.Harness = source.Harness()
					break
				}
			}
		}
		out = append(out, view)
	}
	return out
}
