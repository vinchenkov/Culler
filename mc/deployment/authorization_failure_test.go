package deployment

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

func TestNativeAuthorizationFailureKeepsSafeCauseAndDropsNativeSecrets(t *testing.T) {
	for _, fixture := range []struct{ output, code string }{
		{"error: address already in use", "callback"},
		{"error sending request", "network"},
		{"403 Forbidden", "denied"},
		{"invalid_grant", "expired"},
		{"permission denied", "storage"},
		{"unexpected failure", ""},
	} {
		output := &authorizationFailureOutput{target: io.Discard}
		for _, part := range []string{"synthetic-secret-native-output\n", fixture.output[:3], fixture.output[3:]} {
			_, _ = output.Write([]byte(part))
		}
		err := output.failure(7)
		var failure *AuthorizationFailure
		if !errors.As(err, &failure) || failure.Code != fixture.code {
			t.Fatalf("Incorrect safe failure category: %v", err)
		}
		code, message := agentProfileOperationError("authorization", err)
		if !strings.HasPrefix(code, "authorization_") || strings.Contains(message, "synthetic-secret") || output.tail != "" {
			t.Fatal("The failure lost its category or retained native output")
		}
	}
}

func TestNativeCredentialStageFailureReachesOperationStatus(t *testing.T) {
	for _, fixture := range []struct {
		name string
		err  error
	}{
		{"credential_missing", fmt.Errorf("synthetic-secret-native-path: %w", os.ErrNotExist)},
		{"credential_permissions", fmt.Errorf("synthetic-secret-native-path: %w", os.ErrPermission)},
		{"credential_changed", errors.New("synthetic-secret-native-path changed")},
		{"credential_source", errors.New("synthetic-secret-native-content")},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			home := agentProfileTestHome(t)
			request := AgentProfileOperationRequest{OperationID: "authorization-source-" + fixture.name, Kind: "authorization", ConnectionID: "claude"}
			deps := agentProfileOperationDependencies{Launch: profileNoLaunch, Authorize: func(context.Context, string, string, func(string, string)) (any, error) {
				return nil, nativeCredentialFailure(fixture.err)
			}}
			if _, err := startAgentProfileOperation(home, request, deps); err != nil {
				t.Fatal(err)
			}
			if err := runAgentProfileOperation(home, request.OperationID, deps); err != nil {
				t.Fatal(err)
			}
			status, err := GetAgentProfileOperation(home, request.OperationID)
			if err != nil || status.Status != "failed" || status.ErrorCode != "authorization_"+fixture.name {
				t.Fatalf("The operation lost its credential-stage diagnosis: %+v %v", status, err)
			}
			if status.Message != (&AuthorizationFailure{Code: fixture.name}).Error() || strings.Contains(status.Message+status.Detail, "synthetic-secret") {
				t.Fatal("The operation retained secret output or lost its safe message")
			}
		})
	}
}
