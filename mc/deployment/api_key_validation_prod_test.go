//go:build !test_fake_routing

package deployment

import "testing"

func TestAPIKeyReplacementProductionIgnoresTestTransportSelection(t *testing.T) {
	t.Setenv("CULLER_TEST_HOST_SECRET_DIR", t.TempDir())
	t.Setenv("CULLER_TEST_API_KEY_VALIDATION_STATUS", "401")
	client, err := apiKeyReplacementClient("synthetic-key")
	if err != nil || client != apiKeyValidationClient {
		t.Fatal("The production build selected a synthetic provider transport.")
	}
}
