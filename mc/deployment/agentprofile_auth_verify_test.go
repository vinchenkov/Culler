package deployment

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAgentProfileAuthorizationVerifiesAccessBeforeItEnds(t *testing.T) {
	for _, fixture := range []struct {
		name    string
		verify  error
		message string
	}{
		{"verified", nil, "Culler verified access"},
		{"unverified", errors.New("the resident is unavailable"), "Access is not verified"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			home := agentProfileTestHome(t)
			req := AgentProfileOperationRequest{OperationID: "authorization-" + fixture.name, Kind: "authorization", ConnectionID: "claude"}
			deps := agentProfileOperationDependencies{Launch: profileNoLaunch,
				Authorize: func(context.Context, string, string, func(string, string)) (any, error) {
					return oauthRuntimeGrant{Credential: "claude", Channel: "claude", RefreshToken: "new-authorization"}, nil
				},
				Verify: func(home, connection string) error {
					// The resident refresh needs the connection owner and the saved credential.
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					release, err := AcquireConnectionOwner(ctx, home, connection)
					if err != nil {
						t.Errorf("The access check started with a held connection owner: %v", err)
					} else {
						release()
					}
					body, err := readConnectionCredentialBody(home, connection)
					if err != nil || !strings.Contains(string(body), "new-authorization") {
						t.Errorf("The access check started before the credential save: %v", err)
					}
					active, err := CancelAgentProfileOperation(home, req.OperationID)
					if err != nil || active.Status != "running" {
						t.Errorf("A cancellation replaced the saved authorization result: %+v %v", active, err)
					}
					return fixture.verify
				}}
			if _, err := startAgentProfileOperation(home, req, deps); err != nil {
				t.Fatal(err)
			}
			if err := runAgentProfileOperation(home, req.OperationID, deps); err != nil {
				t.Fatal(err)
			}
			status, err := GetAgentProfileOperation(home, req.OperationID)
			if err != nil || status.Status != "succeeded" || status.FinishedAt == "" || !strings.Contains(status.Message, fixture.message) {
				t.Fatalf("Unexpected authorization result: %+v %v", status, err)
			}
		})
	}
}
