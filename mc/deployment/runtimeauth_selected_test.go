package deployment

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func selectedRuntimeAuthFixture(t *testing.T) (string, *RuntimeAuthSourceStage) {
	t.Helper()
	home := agentProfileTestHome(t)
	writeSignInCatalogConfig(t, home)
	canonical, err := CanonicalHome(home)
	if err != nil {
		t.Fatal(err)
	}
	acquisition := signInFixture(t, canonical, signInCodexFixture, "")
	stage, err := StageRuntimeAuthSources(canonical, RuntimeAuthSources{
		Bindings: []string{"chatgpt"}, Environment: []string{},
	}, acquisition)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Cleanup() })
	if err := acquisition.Cleanup(); err != nil {
		t.Fatal(err)
	}
	return canonical, stage
}

func publishSelectedRuntimeFixture(t *testing.T, home, id, refresh string) []byte {
	t.Helper()
	catalog, err := LoadRuntimeCatalog(home)
	if err != nil {
		t.Fatal(err)
	}
	body := signInCodexFixture
	if id == "claude" {
		body = signInClaudeFixture
	}
	grant, err := buildRuntimeGrantBody(id, catalog.Credentials[id], []byte(strings.ReplaceAll(body, "fixture-refresh", refresh)))
	if err != nil {
		t.Fatal(err)
	}
	if err := PublishAgentConnectionGrant(home, id, grant); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(home, "refresh-grants", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func rotateSelectedRuntimeFixture(t *testing.T, root, refresh string) {
	t.Helper()
	home := filepath.Dir(root)
	current, err := ReadProviderCredential(home, "chatgpt")
	if err != nil {
		t.Fatal(err)
	}
	record := current.Record
	record.RefreshToken = refresh
	if err := RenewProviderCredential(home, ProviderCredentialRenewal{ConnectionID: "chatgpt", AuthorizationID: current.AuthorizationID, CredentialRef: current.CredentialRef, Record: record}); err != nil {
		t.Fatal(err)
	}
}

func assertSelectedRuntimeScratchEmpty(t *testing.T, home string) {
	t.Helper()
	entries, err := os.ReadDir(runtimeAuthSourceRoot(home))
	if err != nil || len(entries) != 0 {
		t.Fatalf("The import retained staged credentials: %v", err)
	}
}

func TestImportSelectedRuntimeAuthAcceptsTheCurrentClaudeCodeStore(t *testing.T) {
	home := agentProfileTestHome(t)
	writeSignInCatalogConfig(t, home)
	canonical, err := CanonicalHome(home)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadRuntimeCatalog(canonical)
	if err != nil {
		t.Fatal(err)
	}
	clientID := catalog.Credentials["claude"].ClientID
	store := fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"fixture-access","refreshToken":"fixture-refresh",`+
		`"expiresAt":9999999999999,"refreshTokenExpiresAt":9999999999999,"scopes":["user:inference","user:profile"],`+
		`"subscriptionType":"max","rateLimitTier":null,"clientId":%q,"futureField":{"value":1}}}`, clientID)
	acquisition := signInFixture(t, canonical, "", store)
	stage, err := StageRuntimeAuthSources(canonical, RuntimeAuthSources{
		Bindings: []string{"claude"}, Environment: []string{},
	}, acquisition)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Cleanup() })
	if err := acquisition.Cleanup(); err != nil {
		t.Fatal(err)
	}
	status, err := ImportSelectedStagedRuntimeAuth(canonical, stage, RuntimeAuthVerifyFunc(func(string, string) error { return nil }))
	if err != nil || status != "done" {
		t.Fatalf("The import refused a Claude Code store with new fields: %q, %v", status, err)
	}
	grant, err := readConnectionCredentialBody(canonical, "claude")
	if err != nil || !strings.Contains(string(grant), `"refresh_token":"fixture-refresh"`) ||
		!strings.Contains(string(grant), fmt.Sprintf(`"client_id":%q`, clientID)) || !strings.Contains(string(grant), `"refresh_expires_at_ms":9999999999999`) {
		t.Fatalf("The import published an incorrect Claude grant: %v", err)
	}

	mismatch := strings.Replace(store, fmt.Sprintf(`"clientId":%q`, clientID), `"clientId":"other-client"`, 1)
	if _, err := buildRuntimeGrantBody("claude", catalog.Credentials["claude"], []byte(mismatch)); err == nil {
		t.Fatal("The Claude credential check accepted credentials of another OAuth client.")
	}
}

func TestImportSelectedRuntimeAuthPreservesOtherCredentialsAndPublishesRefreshedGrant(t *testing.T) {
	home, stage := selectedRuntimeAuthFixture(t)
	other := publishSelectedRuntimeFixture(t, home, "claude", "healthy-refresh")
	publishSelectedRuntimeFixture(t, home, "chatgpt", "previous-refresh")
	otherPath := filepath.Join(home, "refresh-grants", "claude.json")
	otherInfo, err := os.Stat(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	previousIdentity, err := AgentConnectionAuthorizationIdentity(home, "chatgpt")
	if err != nil {
		t.Fatal(err)
	}
	otherIdentity, err := AgentConnectionAuthorizationIdentity(home, "claude")
	if err != nil {
		t.Fatal(err)
	}
	initialStage := stage.stageRoot
	var verifiedRoot string
	status, err := ImportSelectedStagedRuntimeAuth(home, stage, RuntimeAuthVerifyFunc(func(binding, root string) error {
		if binding != "chatgpt" {
			t.Fatal("The importer verified an unselected connection.")
		}
		if _, err := os.Lstat(initialStage); !os.IsNotExist(err) {
			t.Fatalf("The provider source survived until verification: %v", err)
		}
		verifiedRoot = root
		current, err := ReadProviderCredential(home, "chatgpt")
		if err != nil || current.Record.RefreshToken != "fixture-refresh" || current.AuthorizationID == previousIdentity {
			t.Fatal("The test did not receive the new native session")
		}
		rotateSelectedRuntimeFixture(t, root, "verified-refresh")
		return nil
	}))
	if err != nil || status != "done" {
		t.Fatalf("The selected import failed: %q, %v", status, err)
	}
	selected, err := readConnectionCredentialBody(home, "chatgpt")
	if err != nil || !strings.Contains(string(selected), `"refresh_token":"verified-refresh"`) {
		t.Fatalf("The importer lost the verifier's refreshed credential: %v", err)
	}
	after, err := os.ReadFile(otherPath)
	if err != nil || string(after) != string(other) {
		t.Fatalf("The importer changed another credential: %v", err)
	}
	afterInfo, err := os.Stat(otherPath)
	if err != nil || !os.SameFile(otherInfo, afterInfo) {
		t.Fatalf("The importer replaced another credential: %v", err)
	}
	currentIdentity, err := AgentConnectionAuthorizationIdentity(home, "chatgpt")
	if err != nil || currentIdentity == previousIdentity {
		t.Fatalf("The new authorization retained the previous identity: %v", err)
	}
	currentOtherIdentity, err := AgentConnectionAuthorizationIdentity(home, "claude")
	if err != nil || currentOtherIdentity != otherIdentity {
		t.Fatalf("The importer changed another authorization identity: %v", err)
	}
	if verifiedRoot != filepath.Join(home, "refresh-grants") {
		t.Fatal("The test did not use the protected credential store")
	}
	assertSelectedRuntimeScratchEmpty(t, home)
	if err := ValidateRuntimeGrantStore(home, []string{"chatgpt", "claude"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestImportSelectedRuntimeAuthFailureRetainsTheNewNativeSession(t *testing.T) {
	home, stage := selectedRuntimeAuthFixture(t)
	publishSelectedRuntimeFixture(t, home, "chatgpt", "old-refresh")
	before, err := ReadProviderCredentialIdentity(home, "chatgpt")
	if err != nil {
		t.Fatal(err)
	}
	_, err = ImportSelectedStagedRuntimeAuth(home, stage, RuntimeAuthVerifyFunc(func(_ string, root string) error {
		rotateSelectedRuntimeFixture(t, root, "new-renewed-refresh")
		return fmt.Errorf("The model is unavailable.")
	}))
	if err == nil {
		t.Fatal("The failed model test reported success")
	}
	after, err := ReadProviderCredential(home, "chatgpt")
	if err != nil || after.AuthorizationID == before.AuthorizationID || after.Record.RefreshToken != "new-renewed-refresh" {
		t.Fatal("The failed model test lost the new native session")
	}
	assertSelectedRuntimeScratchEmpty(t, home)
}

func TestImportSelectedRuntimeAuthRejectsReplacementDuringModelTest(t *testing.T) {
	home, stage := selectedRuntimeAuthFixture(t)
	_, err := ImportSelectedStagedRuntimeAuth(home, stage, RuntimeAuthVerifyFunc(func(string, string) error {
		publishSelectedRuntimeFixture(t, home, "chatgpt", "later-session")
		return nil
	}))
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatal("The test accepted a result for an obsolete session")
	}
	current, err := ReadProviderCredential(home, "chatgpt")
	if err != nil || current.Record.RefreshToken != "later-session" {
		t.Fatal("The model test overwrote a later native session")
	}
}

func TestImportSelectedRuntimeAuthStoreFailurePreservesThePreviousSession(t *testing.T) {
	home, stage := selectedRuntimeAuthFixture(t)
	before := publishSelectedRuntimeFixture(t, home, "chatgpt", "old-refresh")
	backend := hostSecretBackend
	hostSecretBackend = failedHostSecretStore{backend}
	t.Cleanup(func() { hostSecretBackend = backend })
	called := false
	_, err := ImportSelectedStagedRuntimeAuth(home, stage, RuntimeAuthVerifyFunc(func(string, string) error { called = true; return nil }))
	if err == nil || called {
		t.Fatal("The model test ran without a saved native session")
	}
	after, err := os.ReadFile(filepath.Join(home, "refresh-grants", "chatgpt.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("A failed store operation changed the previous native session")
	}
}
