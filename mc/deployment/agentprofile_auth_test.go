package deployment

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	operatorconfig "mc/config"
)

func TestAgentProfileAuthorizationOutputPublishesOnlyAuthorizationInstructions(t *testing.T) {
	var url, code string
	out := &agentAuthorizationOutput{notify: func(nextURL, nextCode string) { url, code = nextURL, nextCode }}
	for _, part := range []string{"diagnostic fixture-secret\nhttps://example.invalid/docs\n", "\x1b[32mhttps://example.invalid/device/auth\x1b[0m\n", "Code: ABCD-EFGH\n"} {
		if _, err := io.WriteString(out, part); err != nil {
			t.Fatal(err)
		}
	}
	if url != "https://example.invalid/device/auth" || code != "ABCD-EFGH" {
		t.Fatalf("Unexpected authorization instructions: %q %q", url, code)
	}
	_, _ = io.WriteString(out, "https://example.invalid/authorize?access_token=fixture-secret\n")
	if strings.Contains(url, "fixture-secret") {
		t.Fatal("The authorization output disclosed a credential")
	}
}

func TestAgentProfileAuthorizationRejectsLateResultAfterAnotherAuthorization(t *testing.T) {
	home := agentProfileTestHome(t)
	req := AgentProfileOperationRequest{OperationID: "authorization-older", Kind: "authorization", ConnectionID: "claude"}
	entered := make(chan struct{})
	complete := make(chan struct{})
	deps := agentProfileOperationDependencies{Launch: profileNoLaunch, Authorize: func(context.Context, string, string, func(string, string)) (any, error) {
		close(entered)
		<-complete
		return oauthRuntimeGrant{Credential: "claude", Channel: "claude", RefreshToken: "older-token"}, nil
	}}
	if _, err := startAgentProfileOperation(home, req, deps); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- runAgentProfileOperation(home, req.OperationID, deps) }()
	<-entered
	// An external credential change also requires the identity fence.
	if err := withAgentConnectionLock(home, "claude", func() error {
		return publishAgentConnectionAuthorization(home, "claude", oauthRuntimeGrant{Credential: "claude", Channel: "claude", RefreshToken: "newer-token"})
	}); err != nil {
		t.Fatal(err)
	}
	close(complete)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	body, err := readConnectionCredentialBody(home, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "newer-token") || strings.Contains(string(body), "older-token") {
		t.Fatal("A late authorization replaced the current credential")
	}
	status, err := GetAgentProfileOperation(home, req.OperationID)
	if err != nil || status.Status != "failed" {
		t.Fatalf("Unexpected authorization result: %+v %v", status, err)
	}
}

func TestAgentProfileModelReceiptRejectsCredentialChangeDuringTest(t *testing.T) {
	home, req := profileOperationFixture(t)
	deps := agentProfileOperationDependencies{Launch: profileNoLaunch, Test: func(context.Context, string, RuntimeCatalog, string) (LiveProviderObservation, error) {
		err := PublishAgentConnectionGrant(home, req.Profile.ConnectionID, fixtureOAuthGrant(req.Profile.ConnectionID, "newer-token"))
		return successfulProfileObservation(), err
	}}
	if _, err := startAgentProfileOperation(home, req, deps); err != nil {
		t.Fatal(err)
	}
	if err := runAgentProfileOperation(home, req.OperationID, deps); err != nil {
		t.Fatal(err)
	}
	status, err := GetAgentProfileOperation(home, req.OperationID)
	if err != nil || status.Status != "failed" {
		t.Fatalf("Unexpected test result: %+v %v", status, err)
	}
}

func TestAgentProfileReceiptSurvivesNormalRenewalAndRejectsReconnect(t *testing.T) {
	home := agentProfileTestHome(t)
	catalog, err := LoadRuntimeCatalog(home)
	if err != nil {
		t.Fatal(err)
	}
	source := catalog.Credentials["claude"]
	grant := oauthRuntimeGrant{Credential: "claude", Channel: source.Channel(), TokenURL: source.TokenURL, ClientID: source.ClientID, Scope: "user:inference", RefreshToken: "original-token"}
	if err := PublishAgentConnectionGrant(home, "claude", grant); err != nil {
		t.Fatal(err)
	}
	view, err := AgentProfileList(home)
	if err != nil {
		t.Fatal(err)
	}
	req := AgentProfileOperationRequest{OperationID: "renewal-receipt-0001", Kind: "model_test", ConfirmSpend: true, Profile: AgentProfileRequest{SubmissionID: "renewal-profile", Revision: view.Revision, Name: "Renewal profile", ConnectionID: "claude", Model: "custom-renewal-model"}}
	deps := agentProfileOperationDependencies{Launch: profileNoLaunch, Test: func(context.Context, string, RuntimeCatalog, string) (LiveProviderObservation, error) {
		return successfulProfileObservation(), nil
	}}
	if _, err := startAgentProfileOperation(home, req, deps); err != nil {
		t.Fatal(err)
	}
	rotate := func() {
		t.Helper()
		before, _ := json.Marshal(grant)
		grant.RefreshToken += "-next"
		after, _ := json.Marshal(grant)
		if err := RotateAgentConnectionGrant(home, AgentConnectionRotation{Expected: before, Grant: after}); err != nil {
			t.Fatal(err)
		}
	}
	rotate()
	if err := runAgentProfileOperation(home, req.OperationID, deps); err != nil {
		t.Fatal(err)
	}
	rotate()
	if err := VerifyAgentProfileTest(home, req.OperationID, req.Profile); err != nil {
		t.Fatalf("Normal renewal invalidated the model test: %v", err)
	}
	enabled := req.Profile
	enabled.Enabled = true
	enabled.TestID = req.OperationID
	if _, err := AgentProfileSave(home, enabled); err != nil {
		t.Fatalf("The verified profile did not enable after renewal: %v", err)
	}
	grant.RefreshToken = "another-authorization"
	if err := PublishAgentConnectionGrant(home, "claude", grant); err != nil {
		t.Fatal(err)
	}
	if err := VerifyAgentProfileTest(home, req.OperationID, req.Profile); err == nil {
		t.Fatal("A reconnect retained a model test from another authorization")
	}
}

func TestAgentProfileAuthorizationSucceedsDuringNormalRenewal(t *testing.T) {
	home := agentProfileTestHome(t)
	original := oauthRuntimeGrant{Credential: "claude", Channel: "claude", RefreshToken: "initial-refresh"}
	if err := PublishAgentConnectionGrant(home, "claude", original); err != nil {
		t.Fatal(err)
	}
	req := AgentProfileOperationRequest{OperationID: "authorization-renewal", Kind: "authorization", ConnectionID: "claude"}
	deps := agentProfileOperationDependencies{Launch: profileNoLaunch, Authorize: func(context.Context, string, string, func(string, string)) (any, error) {
		before, _ := json.Marshal(original)
		renewed := original
		renewed.RefreshToken = "normal-renewal"
		after, _ := json.Marshal(renewed)
		if err := RotateAgentConnectionGrant(home, AgentConnectionRotation{Expected: before, Grant: after}); err != nil {
			return nil, err
		}
		result := original
		result.RefreshToken = "new-authorization"
		return result, nil
	}}
	if _, err := startAgentProfileOperation(home, req, deps); err != nil {
		t.Fatal(err)
	}
	if err := runAgentProfileOperation(home, req.OperationID, deps); err != nil {
		t.Fatal(err)
	}
	status, err := GetAgentProfileOperation(home, req.OperationID)
	if err != nil || status.Status != "succeeded" {
		t.Fatalf("Normal renewal prevented authorization: %+v %v", status, err)
	}
}

func TestAgentProfileCodexAuthorizationUsesBrowserLogin(t *testing.T) {
	home := agentProfileTestHome(t)
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Runtimes.Credentials["browser"] = cfg.Runtimes.Credentials["chatgpt"]
	if _, err := operatorconfig.WriteAtomic(filepath.Join(home, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	setupRuntimeLoginFixture(t, "codex", `
umask 077
[ "$#" -eq 1 ] && [ "$1" = login ] || exit 2
printf '%s\n' 'https://example.test/oauth/authorize?response_type=code&state=fixture'
printf '%s' '{"auth_mode":"chatgpt","tokens":{"access_token":"fixture-access","id_token":"fixture-id","refresh_token":"fixture-refresh","account_id":"fixture-account"}}' > "$CODEX_HOME/auth.json"
`)
	var address, code string
	grant, err := authorizeAgentProfileConnection(context.Background(), home, "browser", func(nextAddress, nextCode string) { address, code = nextAddress, nextCode })
	if err != nil {
		t.Fatal(err)
	}
	if address != "https://example.test/oauth/authorize?response_type=code&state=fixture" || code != "" {
		t.Fatalf("Unexpected browser instructions: %q %q", address, code)
	}
	oauth, ok := grant.(oauthRuntimeGrant)
	if !ok || oauth.RefreshToken != "fixture-refresh" || oauth.AccountID != "fixture-account" {
		t.Fatal("The browser login did not acquire the account credentials.")
	}
}
