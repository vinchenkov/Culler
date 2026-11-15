package deployment

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCodexNativeCredentialAcceptsOptionalAndAdditionalMetadata(t *testing.T) {
	source := signInCatalog(t).Credentials["chatgpt"]
	for _, mode := range []string{`"auth_mode":"chatgpt",`, ""} {
		body := []byte(`{` + mode + `"OPENAI_API_KEY":null,"agent_identity":null,"future_metadata":{"value":"synthetic-unused-secret"},"tokens":{"access_token":"synthetic-access","id_token":"synthetic-id","refresh_token":"synthetic-refresh","account_id":"synthetic-account","future_metadata":"synthetic-unused-secret"}}`)
		grant, err := buildRuntimeGrantBody("chatgpt", source, body)
		if err != nil {
			t.Fatal(err)
		}
		record := grant.(ProviderCredentialRecord)
		if record.AccessToken != "synthetic-access" || record.IDToken != "synthetic-id" || record.RefreshToken != "synthetic-refresh" || record.AccountID != "synthetic-account" {
			t.Fatal("The native account fields did not reach the protected record")
		}
		encoded, err := json.Marshal(record)
		if err != nil || strings.Contains(string(encoded), "synthetic-unused-secret") {
			t.Fatal("The import retained unrelated native metadata")
		}
	}
}

func TestCodexNativeCredentialRequiresCompleteAccountEvidence(t *testing.T) {
	source := signInCatalog(t).Credentials["chatgpt"]
	for _, fixture := range []struct{ old, replacement, code string }{
		{`"chatgpt"`, `"api_key"`, "credential_mode"},
		{`"tokens":`, `"OPENAI_API_KEY":"synthetic-key","tokens":`, "credential_api_key"},
		{`"fixture-access"`, `""`, "credential_access"},
		{`"fixture-id"`, `null`, "credential_id"},
		{`"fixture-refresh"`, `""`, "credential_refresh"},
		{`"fixture-account"`, `""`, "credential_account"},
	} {
		body := strings.Replace(signInCodexFixture, fixture.old, fixture.replacement, 1)
		_, err := buildRuntimeGrantBody("chatgpt", source, []byte(body))
		var failure *AuthorizationFailure
		if !errors.As(err, &failure) || failure.Code != fixture.code || strings.Contains(err.Error(), "synthetic-key") {
			t.Fatalf("The native credential check lost its safe cause: %v", err)
		}
	}
}
