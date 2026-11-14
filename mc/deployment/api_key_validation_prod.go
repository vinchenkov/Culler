//go:build !test_fake_routing

package deployment

import "net/http"

func apiKeyReplacementClient(string) (*http.Client, error) { return apiKeyValidationClient, nil }
