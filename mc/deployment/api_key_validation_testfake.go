//go:build test_fake_routing

package deployment

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

type syntheticAPIKeyValidationTransport struct{ status int }

func (transport syntheticAPIKeyValidationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodGet || !strings.HasPrefix(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "), "synthetic-") && !strings.HasPrefix(request.Header.Get("x-api-key"), "synthetic-") {
		return nil, fmt.Errorf("The test key check accepts synthetic credentials only.")
	}
	if transport.status == 0 {
		return nil, fmt.Errorf("The synthetic provider is unavailable.")
	}
	return &http.Response{StatusCode: transport.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"fixture-model"}]}`)), Request: request}, nil
}

func apiKeyReplacementClient(key string) (*http.Client, error) {
	if os.Getenv("CULLER_TEST_HOST_SECRET_DIR") == "" {
		return apiKeyValidationClient, nil
	}
	if !strings.HasPrefix(key, "synthetic-") {
		return nil, fmt.Errorf("The test key check accepts synthetic credentials only.")
	}
	status := 200
	if value := os.Getenv("CULLER_TEST_API_KEY_VALIDATION_STATUS"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed != 0 && parsed != 200 && parsed != 401 && parsed != 403 && parsed != 429 && parsed != 503 {
			return nil, fmt.Errorf("The synthetic key check status is invalid.")
		}
		status = parsed
	}
	return &http.Client{Transport: syntheticAPIKeyValidationTransport{status: status}}, nil
}
