//go:build test_fake_routing

package deployment

import (
	"net/http"
	"testing"
)

func TestAPIKeyReplacementSyntheticTransportRequiresSyntheticCredentials(t *testing.T) {
	t.Setenv("CULLER_TEST_HOST_SECRET_DIR", t.TempDir())
	t.Setenv("CULLER_TEST_API_KEY_VALIDATION_STATUS", "401")
	if _, err := apiKeyReplacementClient("ordinary-fixture-key"); err == nil {
		t.Fatal("The synthetic transport accepted a non-synthetic credential.")
	}
	client, err := apiKeyReplacementClient("synthetic-key")
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, "https://provider.example.invalid/models", nil)
	request.Header.Set("Authorization", "Bearer synthetic-key")
	response, err := client.Do(request)
	if err != nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatal("The selected synthetic refusal differs.")
	}
	response.Body.Close()
}
