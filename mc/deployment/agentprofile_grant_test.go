package deployment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAgentConnectionRotationPreservesNewAuthorization(t *testing.T) {
	home := agentProfileTestHome(t)
	old := oauthRuntimeGrant{Credential: "claude", Channel: "claude", TokenURL: "https://example.test/token", ClientID: "client", RefreshToken: "first", Scope: "user:inference"}
	if err := PublishAgentConnectionGrant(home, "claude", old); err != nil {
		t.Fatal(err)
	}
	body, err := readConnectionCredentialBody(home, "claude")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	renewed := old
	renewed.RefreshToken = "second"
	if err := CompareAndPublishAgentConnectionGrant(home, "claude", digest, renewed); err != nil {
		t.Fatal(err)
	}
	late := old
	late.RefreshToken = "late"
	expected, _ := json.Marshal(old)
	grant, _ := json.Marshal(late)
	if err := RotateAgentConnectionGrant(home, AgentConnectionRotation{Expected: expected, Grant: grant}); err == nil {
		t.Fatal("A late renewal replaced the new authorization")
	}
	if err := CompareAndPublishAgentConnectionGrant(home, "claude", digest, late); err == nil {
		t.Fatal("A late authorization replaced the new authorization")
	}
	body, err = readConnectionCredentialBody(home, "claude")
	if err != nil {
		t.Fatal(err)
	}
	var saved oauthRuntimeGrant
	if err := json.Unmarshal(body, &saved); err != nil || saved.RefreshToken != "second" {
		t.Fatal("The current credential changed")
	}
	current, _ := json.Marshal(renewed)
	if err := RotateAgentConnectionGrant(home, AgentConnectionRotation{Expected: current, Grant: grant}); err != nil {
		t.Fatal(err)
	}
}

func TestAgentConnectionGrantStoreAcceptsUnusedConnections(t *testing.T) {
	home := agentProfileTestHome(t)
	agentProfileAccountFixture(t, home, "endpoint", "endpoint-model")
	agentProfileAccountFixture(t, home, "second-endpoint", "second-model")
	if err := PublishAgentConnectionGrant(home, "endpoint", fixtureOAuthGrant("endpoint", "fixture-token")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRuntimeGrantStore(home, []string{"endpoint"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := PublishAgentConnectionGrant(home, "second-endpoint", fixtureOAuthGrant("second-endpoint", "unused-token")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRuntimeGrantStore(home, []string{"endpoint"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRuntimeGrantStore(home, []string{"claude"}, nil); err == nil {
		t.Fatal("A required credential was absent")
	}
}

func TestAgentConnectionGrantRejectsStaticCredentialsBeforeStorage(t *testing.T) {
	home := agentProfileTestHome(t)
	grant := map[string]string{"credential": "account", "channel": "static", "env_name": "ANTHROPIC_AUTH_TOKEN", "secret": "refused-secret"}
	if err := PublishAgentConnectionGrant(home, "account", grant); err == nil {
		t.Fatal("The host accepted a static credential.")
	}
	if _, err := os.Stat(filepath.Join(home, "refresh-grants")); !os.IsNotExist(err) {
		t.Fatal("The host created a credential store for a static credential.")
	}
}
