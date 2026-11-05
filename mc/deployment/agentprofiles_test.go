package deployment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	operatorconfig "mc/config"
	"mc/routing"
)

func agentProfileTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := operatorconfig.Default()
	cfg.Surfaces.Dashboard.AuthToken = "dashboard-secret-fixture"
	cfg.Git.AuthorName = "Preserve this author"
	if _, err := operatorconfig.WriteAtomic(filepath.Join(home, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	return home
}

// agentProfileAccountFixture defines one sign-in connection and one agent profile.
func agentProfileAccountFixture(t *testing.T, home, id, model string) {
	t.Helper()
	if _, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), func(cfg *operatorconfig.Config) error {
		cfg.Runtimes.Credentials[id] = cfg.Runtimes.Credentials["claude"]
		cfg.Runtimes.Bindings[id] = operatorconfig.RuntimeBinding{Harness: "claude-sdk", Model: model, Credential: id}
		return cfg.Validate()
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAgentProfilesPreserveConfigurationAndRecoverLostSave(t *testing.T) {
	home := agentProfileTestHome(t)
	before, err := AgentProfileList(home)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := AgentConnectionSave(home, AgentConnectionRequest{SubmissionID: "new-endpoint", Revision: before.Revision, Name: "Endpoint", TemplateID: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	req := AgentProfileRequest{SubmissionID: "new-profile", Revision: connection.Revision, Name: "Careful", ConnectionID: connection.ConnectionID, Model: "custom-model"}
	saved, err := AgentProfileSave(home, req)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := AgentProfileSave(home, req)
	if err != nil {
		t.Fatal(err)
	}
	if retry.ProfileID != saved.ProfileID || len(retry.Profiles) != len(saved.Profiles) {
		t.Fatal("A repeated save created another agent profile")
	}
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Git.AuthorName != "Preserve this author" || cfg.Surfaces.Dashboard.AuthToken != "dashboard-secret-fixture" {
		t.Fatal("The save changed unrelated configuration")
	}
	profile := cfg.Runtimes.Bindings[saved.ProfileID]
	if profile.Model != "custom-model" || profile.BaseURL != "" || profile.Credential != connection.ConnectionID || agentEnabled(cfg, saved.ProfileID) {
		t.Fatalf("Unexpected saved profile: %+v", profile)
	}
	body, err := json.Marshal(retry)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "secret-fixture") {
		t.Fatal("The catalog disclosed a secret")
	}
	configBody, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(configBody), "endpoint-secret-fixture") {
		t.Fatal("The configuration contains the endpoint token")
	}
	if _, err := os.Stat(filepath.Join(home, "refresh-grants", connection.ConnectionID+".json")); !os.IsNotExist(err) {
		t.Fatal("Connection creation stored a credential before sign-in.")
	}
}

func TestAgentProfileSaveRejectsStaleChangesAndDuplicateNames(t *testing.T) {
	home := agentProfileTestHome(t)
	catalog, err := AgentProfileList(home)
	if err != nil {
		t.Fatal(err)
	}
	req := AgentProfileRequest{SubmissionID: "one", Revision: catalog.Revision, Name: "Careful", ConnectionID: "claude", Model: "manual-model"}
	saved, err := AgentProfileSave(home, req)
	if err != nil {
		t.Fatal(err)
	}
	req.SubmissionID = "two"
	req.Name = "Fast"
	if _, err := AgentProfileSave(home, req); err == nil || !strings.Contains(err.Error(), "configuration changed") {
		t.Fatalf("Expected stale conflict: %v", err)
	}
	req.Revision = saved.Revision
	req.Name = "careful"
	if _, err := AgentProfileSave(home, req); err == nil || !strings.Contains(err.Error(), "already uses") {
		t.Fatalf("Expected duplicate name: %v", err)
	}
}

func TestAgentProfileRequiresHostTestBeforeEnable(t *testing.T) {
	home := agentProfileTestHome(t)
	catalog, err := AgentProfileList(home)
	if err != nil {
		t.Fatal(err)
	}
	req := AgentProfileRequest{SubmissionID: "one", Revision: catalog.Revision, Name: "Careful", ConnectionID: "claude", Model: "manual-model", Enabled: true, TestID: "forged"}
	if _, err := AgentProfileSave(home, req); err == nil {
		t.Fatal("An unverified agent profile became enabled")
	}
	after, err := AgentProfileList(home)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != catalog.Revision {
		t.Fatal("A refused save changed configuration")
	}
}

func TestAgentProfileSharesConnectionAndPreservesAssignments(t *testing.T) {
	home := agentProfileTestHome(t)
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Runtimes.EnabledBindings = []string{"claude"}
	if _, err := operatorconfig.WriteAtomic(filepath.Join(home, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	table, err := routing.UniformTable("claude", routing.RegistryFromRuntimes(cfg.Runtimes))
	if err != nil {
		t.Fatal(err)
	}
	routeBody := table.Markdown()
	if err := os.WriteFile(filepath.Join(home, "routing.md"), []byte(routeBody), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := AgentProfileList(home)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := AgentProfileSave(home, AgentProfileRequest{SubmissionID: "shared", Revision: catalog.Revision, Name: "Second model", ConnectionID: "claude", Model: "another-model"})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Connections) != len(catalog.Connections) {
		t.Fatal("A shared connection created another credential source")
	}
	after, err := os.ReadFile(filepath.Join(home, "routing.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != routeBody {
		t.Fatal("Profile creation changed the role assignments")
	}
	for _, p := range saved.Profiles {
		if p.ID == "claude" && len(p.Roles) != len(routing.Roles()) {
			t.Fatal("The catalog omitted role assignments")
		}
	}
}

func TestAgentConnectionRejectsUnsupportedTypesAndFieldsBeforeWrites(t *testing.T) {
	for _, change := range []func(*AgentConnectionRequest){
		func(req *AgentConnectionRequest) { req.Kind = "static-env"; req.Token = "rejected-secret" },
		func(req *AgentConnectionRequest) { req.Kind = "none" },
		func(req *AgentConnectionRequest) { req.Kind = "third-party" },
		func(req *AgentConnectionRequest) { req.BaseURL = "https://example.invalid" },
		func(req *AgentConnectionRequest) { req.Token = "rejected-secret" },
		func(req *AgentConnectionRequest) { req.TemplateID = "unknown" },
	} {
		home := agentProfileTestHome(t)
		before, err := os.ReadFile(filepath.Join(home, "config.toml"))
		if err != nil {
			t.Fatal(err)
		}
		req := AgentConnectionRequest{SubmissionID: "refused", Name: "Account", TemplateID: "claude"}
		change(&req)
		if _, err := AgentConnectionSave(home, req); err == nil {
			t.Fatal("The connection request was accepted.")
		}
		after, err := os.ReadFile(filepath.Join(home, "config.toml"))
		if err != nil || string(before) != string(after) {
			t.Fatal("The refused request changed the configuration.")
		}
		for _, name := range []string{"refresh-grants", "agent-profile-operations"} {
			if _, err := os.Stat(filepath.Join(home, name)); !os.IsNotExist(err) {
				t.Fatalf("The refused request created %s.", name)
			}
		}
	}
}

func TestAgentProfileRejectsProviderAddress(t *testing.T) {
	home := agentProfileTestHome(t)
	if _, _, err := ResolveAgentProfile(home, AgentProfileRequest{SubmissionID: "profile", Name: "Model", ConnectionID: "claude", Model: "model", BaseURL: "https://example.invalid"}); err == nil {
		t.Fatal("The profile accepted a provider address.")
	}
}

func TestAgentConnectionPreservesOtherGrants(t *testing.T) {
	home := agentProfileTestHome(t)
	if err := PublishAgentConnectionGrant(home, "other", fixtureOAuthGrant("other", "other-secret")); err != nil {
		t.Fatal(err)
	}
	catalog, err := AgentProfileList(home)
	if err != nil {
		t.Fatal(err)
	}
	req := AgentConnectionRequest{SubmissionID: "account", Revision: catalog.Revision, Name: "Account", TemplateID: "claude"}
	saved, err := AgentConnectionSave(home, req)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := AgentConnectionSave(home, req)
	if err != nil {
		t.Fatal(err)
	}
	if retry.ConnectionID != saved.ConnectionID {
		t.Fatal("A repeated submission created another connection")
	}
	body, err := readConnectionCredentialBody(home, "other")
	if err != nil || !strings.Contains(string(body), "other-secret") {
		t.Fatal("Connection creation changed another credential")
	}
}

func TestAgentConnectionRepeatedSavePreservesLaterCredential(t *testing.T) {
	home := agentProfileTestHome(t)
	before, err := AgentProfileList(home)
	if err != nil {
		t.Fatal(err)
	}
	req := AgentConnectionRequest{SubmissionID: "original", Revision: before.Revision, Name: "Endpoint", TemplateID: "claude"}
	saved, err := AgentConnectionSave(home, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := PublishAgentConnectionGrant(home, saved.ConnectionID, fixtureOAuthGrant(saved.ConnectionID, "new-token")); err != nil {
		t.Fatal(err)
	}
	if _, err := AgentConnectionSave(home, req); err != nil {
		t.Fatal(err)
	}
	body, err := readConnectionCredentialBody(home, saved.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "new-token") || strings.Contains(string(body), "original-token") {
		t.Fatal("A repeated save replaced the newer credential")
	}
	req.Name = "Changed account"
	if _, err := AgentConnectionSave(home, req); err == nil {
		t.Fatal("A completed submission accepted different values")
	}
}

func TestAgentProfileReusesTheReferenceModel(t *testing.T) {
	home := agentProfileTestHome(t)
	catalog, err := AgentProfileList(home)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	model := cfg.Runtimes.Bindings["claude"].Model
	result, err := AgentProfileSave(home, AgentProfileRequest{SubmissionID: "same-model-profile", Revision: catalog.Revision, Name: "Careful", ConnectionID: "claude", Model: model})
	if err != nil {
		t.Fatal(err)
	}
	if result.ProfileID == "claude" {
		t.Fatal("The save replaced the reference profile")
	}
}

func TestAgentProfileCatalogOffersBothSubscriptionIntegrations(t *testing.T) {
	home := agentProfileTestHome(t)
	catalog, err := AgentProfileList(home)
	if err != nil {
		t.Fatal(err)
	}
	codex, err := AgentConnectionSave(home, AgentConnectionRequest{SubmissionID: "codex-account", Revision: catalog.Revision, Name: "Codex account", TemplateID: "chatgpt"})
	if err != nil {
		t.Fatal(err)
	}
	reference := operatorconfig.Default().Runtimes.Credentials
	chatgpt := reference["chatgpt"]
	if err := PublishAgentConnectionGrant(home, codex.ConnectionID, oauthRuntimeGrant{Credential: codex.ConnectionID, Channel: chatgpt.Channel(), TokenURL: chatgpt.TokenURL, ClientID: chatgpt.ClientID, RefreshToken: "fixture-refresh"}); err != nil {
		t.Fatal(err)
	}
	if _, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), func(cfg *operatorconfig.Config) error {
		delete(cfg.Runtimes.Bindings, "claude")
		delete(cfg.Runtimes.Credentials, "claude")
		return cfg.Validate()
	}); err != nil {
		t.Fatal(err)
	}
	listed, err := AgentProfileList(home)
	if err != nil {
		t.Fatal(err)
	}
	connected := false
	for _, connection := range listed.Connections {
		connected = connected || connection.ID == codex.ConnectionID && connection.AccessStatus == "connected"
	}
	if !connected {
		t.Fatal("The Codex connection has no saved credential")
	}
	integrations := []string{}
	for _, integration := range listed.Integrations {
		integrations = append(integrations, integration.ID+" "+integration.Kind)
	}
	if strings.Join(integrations, ", ") != "chatgpt codex-oauth, claude claude-oauth, claude-api-key claude-api-key, codex-api-key codex-api-key" {
		t.Fatalf("Unexpected subscription integrations: %v", integrations)
	}
	claude, err := AgentConnectionSave(home, AgentConnectionRequest{SubmissionID: "claude-account", Revision: listed.Revision, Name: "Claude account", TemplateID: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	want := reference["claude"]
	want.Name = "Claude account"
	if got := cfg.Runtimes.Credentials[claude.ConnectionID]; got != want {
		t.Fatalf("Unexpected subscription connection: %+v", got)
	}
	if _, err := AgentConnectionSave(home, AgentConnectionRequest{SubmissionID: "copy", Revision: claude.Revision, Name: "Copy", TemplateID: codex.ConnectionID}); err == nil || !strings.Contains(err.Error(), "supported connection") {
		t.Fatalf("An operator connection became a subscription integration: %v", err)
	}
}
