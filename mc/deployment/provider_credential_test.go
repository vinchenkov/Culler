package deployment

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	operatorconfig "mc/config"
)

func TestProviderCredentialMigrationPreservesAuthorizationAndRemovesFileSecrets(t *testing.T) {
	home := agentProfileTestHome(t)
	source := operatorconfig.Default().Runtimes.Credentials["claude"]
	grant := ProviderCredentialRecord{Credential: "claude", Channel: "claude", TokenURL: source.TokenURL, ClientID: source.ClientID, RefreshToken: "synthetic-refresh", AccessToken: "synthetic-access", ExpiresAtMS: 9999999999999, Scope: "user:inference"}
	if err := os.Mkdir(filepath.Join(home, "refresh-grants"), 0700); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(grant)
	path := filepath.Join(home, "refresh-grants", "claude.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := AgentConnectionAuthorizationIdentity(home, "claude")
	if err != nil {
		t.Fatal(err)
	}
	access, err := ReadProviderCredential(home, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if access.AuthorizationID != before || access.Record.RefreshToken != grant.RefreshToken || access.Record.AccessToken != grant.AccessToken {
		t.Fatal("Migration lost credential or authorization identity.")
	}
	metadata, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(metadata), "synthetic-") || !strings.Contains(string(metadata), access.CredentialRef) {
		t.Fatal("The ordinary file contains a credential or lacks its reference.")
	}
	again, err := ReadProviderCredential(home, "claude")
	if err != nil || again != access {
		t.Fatal("A repeated read changed the authorization.")
	}
}

func TestProviderCredentialRenewalPreservesRevisionAndRejectsLateResults(t *testing.T) {
	home := agentProfileTestHome(t)
	source := operatorconfig.Default().Runtimes.Credentials["claude"]
	grant := ProviderCredentialRecord{Credential: "claude", Channel: "claude", TokenURL: source.TokenURL, ClientID: source.ClientID, RefreshToken: "original-refresh", Scope: "user:inference"}
	if err := PublishAgentConnectionGrant(home, "claude", grant); err != nil {
		t.Fatal(err)
	}
	before, err := ReadProviderCredential(home, "claude")
	if err != nil {
		t.Fatal(err)
	}
	renewal := ProviderCredentialRenewal{ConnectionID: "claude", AuthorizationID: before.AuthorizationID, CredentialRef: before.CredentialRef, Record: before.Record}
	renewal.Record.AccessToken = "new-access"
	renewal.Record.RefreshToken = "new-refresh"
	renewal.Record.ExpiresAtMS = 9999999999999
	if err := RenewProviderCredential(home, renewal); err != nil {
		t.Fatal(err)
	}
	after, err := ReadProviderCredential(home, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if after.AuthorizationID != before.AuthorizationID || after.CredentialRef == before.CredentialRef || after.Record.RefreshToken != "new-refresh" {
		t.Fatal("Renewal did not preserve the authorization and change the credential.")
	}
	if err := RenewProviderCredential(home, renewal); err == nil {
		t.Fatal("A stale renewal replaced the current credential.")
	}
	replacement := grant
	replacement.RefreshToken = "replacement-refresh"
	if err := PublishAgentConnectionGrant(home, "claude", replacement); err != nil {
		t.Fatal(err)
	}
	current, err := ReadProviderCredential(home, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if current.AuthorizationID == before.AuthorizationID {
		t.Fatal("Replacement preserved an obsolete authorization revision.")
	}
	renewal.CredentialRef = after.CredentialRef
	if err := RenewProviderCredential(home, renewal); err == nil {
		t.Fatal("A prior authorization changed the replacement.")
	}
}

type failedHostSecretStore struct{ hostSecretStore }

func (failedHostSecretStore) Write(string, string, []byte) error { return ErrHostCredentialUnavailable }

func TestFailedCredentialSavePreservesPreviousCredentialAndRevision(t *testing.T) {
	home := agentProfileTestHome(t)
	grant := fixtureOAuthGrant("claude", "kept-refresh")
	if err := PublishAgentConnectionGrant(home, "claude", grant); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(home, "refresh-grants", "claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	backend := hostSecretBackend
	hostSecretBackend = failedHostSecretStore{backend}
	t.Cleanup(func() { hostSecretBackend = backend })
	if err := PublishAgentConnectionGrant(home, "claude", fixtureOAuthGrant("claude", "rejected-refresh")); !errors.Is(err, ErrHostCredentialUnavailable) {
		t.Fatalf("Unexpected result: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(home, "refresh-grants", "claude.json"))
	if err != nil || string(after) != string(before) {
		t.Fatal("The failed save changed the connection reference.")
	}
}

func TestAPIKeyConnectionStoresOnlyAReferenceAndRequiresCurrentRevisionForReplacement(t *testing.T) {
	useAPIKeyValidationResponse(t, 200, `{"data":[{"id":"fixture-model"}]}`)
	home := agentProfileTestHome(t)
	catalog, err := AgentProfileList(home)
	if err != nil {
		t.Fatal(err)
	}
	req := AgentConnectionRequest{SubmissionID: "api-key-first", Revision: catalog.Revision, Name: "API fixture", TemplateID: "codex-api-key", APIKey: "synthetic-api-key"}
	saved, err := AgentConnectionSave(home, req)
	if err != nil {
		t.Fatal(err)
	}
	access, err := ReadProviderCredential(home, saved.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	if access.Record.Type != "api_key" || access.Record.APIKey != req.APIKey {
		t.Fatal("The key did not reach its protected store.")
	}
	if err := filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(body), req.APIKey) {
			t.Errorf("The file %s contains the key.", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	replayed, err := AgentConnectionSave(home, req)
	if err != nil || replayed.ConnectionID != saved.ConnectionID {
		t.Fatal("A repeated submission did not preserve the result.")
	}
	req.ID = saved.ConnectionID
	req.SubmissionID = "api-key-replace"
	req.Revision = saved.Revision
	req.APIKey = "replacement-api-key"
	if _, err := AgentConnectionSave(home, req); err == nil {
		t.Fatal("A replacement without the current authorization was accepted.")
	}
	req.AuthorizationID = access.AuthorizationID
	if _, err := AgentConnectionSave(home, req); err != nil {
		t.Fatal(err)
	}
	replacement, err := ReadProviderCredential(home, saved.ConnectionID)
	if err != nil || replacement.Record.APIKey != req.APIKey || replacement.AuthorizationID == access.AuthorizationID {
		t.Fatal("The replacement did not create a new authorization.")
	}
}

type unreadableHostSecretStore struct{ hostSecretStore }

func (unreadableHostSecretStore) Read(string, string) ([]byte, error) {
	return nil, ErrHostCredentialUnavailable
}

func TestProviderCredentialIdentityRemainsAvailableWhenKeychainIsLocked(t *testing.T) {
	home := agentProfileTestHome(t)
	if err := PublishAgentConnectionGrant(home, "claude", fixtureOAuthGrant("claude", "synthetic-refresh")); err != nil {
		t.Fatal(err)
	}
	before, err := ReadProviderCredentialIdentity(home, "claude")
	if err != nil {
		t.Fatal(err)
	}
	backend := hostSecretBackend
	hostSecretBackend = unreadableHostSecretStore{backend}
	t.Cleanup(func() { hostSecretBackend = backend })
	after, err := ReadProviderCredentialIdentity(home, "claude")
	if err != nil || before != after {
		t.Fatal("The nonsecret identity requires access to the Keychain")
	}
	if _, err := ReadProviderCredential(home, "claude"); !errors.Is(err, ErrHostCredentialUnavailable) {
		t.Fatal("A locked Keychain supplied a credential")
	}
}
