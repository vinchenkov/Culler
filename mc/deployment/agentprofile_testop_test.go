package deployment

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func profileOperationFixture(t *testing.T) (string, AgentProfileOperationRequest) {
	t.Helper()
	home := agentProfileTestHome(t)
	before, err := AgentProfileList(home)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := AgentConnectionSave(home, AgentConnectionRequest{SubmissionID: "test-operation-connection", Revision: before.Revision, Name: "Test connection", TemplateID: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if err := PublishAgentConnectionGrant(home, saved.ConnectionID, fixtureOAuthGrant(saved.ConnectionID, "secret-operation-test")); err != nil {
		t.Fatal(err)
	}
	return home, AgentProfileOperationRequest{OperationID: "test-operation-0001", Kind: "model_test", ConfirmSpend: true, Profile: AgentProfileRequest{SubmissionID: "test-operation-profile", Revision: saved.Revision, Name: "Test profile", ConnectionID: saved.ConnectionID, Model: "example-model"}}
}
func successfulProfileObservation() LiveProviderObservation {
	return LiveProviderObservation{SessionID: "session-id", NativeFile: "native.jsonl", StreamEvents: 3, ToolUses: 1, ToolResults: 1, ProviderRequests: 2}
}
func profileNoLaunch(string, string) error { return nil }

func TestAgentProfileOperationRegistersCurrentAuthorizationBeforeTheModelRequest(t *testing.T) {
	for _, outcome := range []string{"succeeded", "failed", "prepare_failed"} {
		t.Run(outcome, func(t *testing.T) {
			home, req := profileOperationFixture(t)
			authorization, err := AgentConnectionAuthorizationIdentity(home, req.Profile.ConnectionID)
			if err != nil {
				t.Fatal(err)
			}
			prepared, closed, requests := false, false, 0
			deps := agentProfileOperationDependencies{
				Launch: profileNoLaunch,
				PrepareTest: func(connection, current string) (func() error, error) {
					if connection != req.Profile.ConnectionID || current != authorization {
						t.Fatal("The model test registered another authorization.")
					}
					if outcome == "prepare_failed" {
						return nil, errors.New("The connection status is unavailable.")
					}
					prepared = true
					return func() error { closed = true; return nil }, nil
				},
				Test: func(context.Context, string, RuntimeCatalog, string) (LiveProviderObservation, error) {
					if !prepared || closed {
						t.Fatal("The model request ran outside its connection check.")
					}
					requests++
					if outcome == "failed" {
						return LiveProviderObservation{}, errors.New("The provider returned 401.")
					}
					return successfulProfileObservation(), nil
				},
			}
			if _, err := startAgentProfileOperation(home, req, deps); err != nil {
				t.Fatal(err)
			}
			if err := runAgentProfileOperation(home, req.OperationID, deps); err != nil {
				t.Fatal(err)
			}
			result, err := GetAgentProfileOperation(home, req.OperationID)
			want := outcome
			if outcome == "prepare_failed" {
				want = "failed"
				if requests != 0 || closed {
					t.Fatal("An unavailable connection check allowed a model request.")
				}
			} else if requests != 1 || !closed {
				t.Fatal("The model test did not close its connection check.")
			}
			if err != nil || result.Status != want {
				t.Fatalf("The model test lost its result: %+v %v", result, err)
			}
		})
	}
}

func TestAgentProfileOperationRequiresSpendAndDoesNotRepeatRequests(t *testing.T) {
	home, req := profileOperationFixture(t)
	calls := 0
	deps := agentProfileOperationDependencies{Launch: profileNoLaunch, Test: func(context.Context, string, RuntimeCatalog, string) (LiveProviderObservation, error) {
		calls++
		return successfulProfileObservation(), nil
	}}
	req.ConfirmSpend = false
	if _, err := startAgentProfileOperation(home, req, deps); err == nil {
		t.Fatal("The test accepted absent spend consent")
	}
	req.ConfirmSpend = true
	first, err := startAgentProfileOperation(home, req, deps)
	if err != nil {
		t.Fatal(err)
	}
	again, err := startAgentProfileOperation(home, req, deps)
	if err != nil || !reflect.DeepEqual(again, first) {
		t.Fatalf("Unexpected replay: %+v, %v", again, err)
	}
	if err := runAgentProfileOperation(home, req.OperationID, deps); err != nil {
		t.Fatal(err)
	}
	if err := runAgentProfileOperation(home, req.OperationID, deps); err != nil {
		t.Fatal(err)
	}
	status, err := GetAgentProfileOperation(home, req.OperationID)
	if err != nil || status.Status != "succeeded" || calls != 1 {
		t.Fatalf("Unexpected status: %+v, calls %d, %v", status, calls, err)
	}
	if err := VerifyAgentProfileTest(home, req.OperationID, req.Profile); err != nil {
		t.Fatal(err)
	}
	changed := req.Profile
	changed.Model = "another-model"
	if err := VerifyAgentProfileTest(home, req.OperationID, changed); err == nil {
		t.Fatal("The receipt accepted a different model")
	}
	req.Profile.Model = "conflicting-model"
	if _, err := startAgentProfileOperation(home, req, deps); err == nil {
		t.Fatal("The operation identifier accepted different input")
	}
}

func TestAgentProfileOperationPreservesFailureWithoutAutomaticRetry(t *testing.T) {
	for _, failure := range []string{"HTTP 401 token secret-operation-test", "endpoint connection refused secret-operation-test", "invalid model secret-operation-test", "tool failed secret-operation-test"} {
		t.Run(failure, func(t *testing.T) {
			home, req := profileOperationFixture(t)
			calls := 0
			deps := agentProfileOperationDependencies{Launch: profileNoLaunch, Test: func(context.Context, string, RuntimeCatalog, string) (LiveProviderObservation, error) {
				calls++
				return LiveProviderObservation{}, errors.New(failure)
			}}
			if _, err := startAgentProfileOperation(home, req, deps); err != nil {
				t.Fatal(err)
			}
			if err := runAgentProfileOperation(home, req.OperationID, deps); err != nil {
				t.Fatal(err)
			}
			if _, err := startAgentProfileOperation(home, req, deps); err != nil {
				t.Fatal(err)
			}
			status, err := GetAgentProfileOperation(home, req.OperationID)
			if err != nil || status.Status != "failed" || calls != 1 {
				t.Fatalf("Unexpected failure: %+v %v", status, err)
			}
			if strings.HasPrefix(failure, "endpoint") && (status.ErrorCode != "endpoint" || status.Message != "The agent container could not reach the provider. Check the network connection and retry the model test.") {
				t.Fatalf("The network failure lost its classification or recovery action: %+v", status)
			}
			root, _ := agentProfileOperationsRoot(home)
			body, err := os.ReadFile(filepath.Join(root, req.OperationID+".json"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), "secret-operation-test") {
				t.Fatal("The operation stored an error secret")
			}
			if err := VerifyAgentProfileTest(home, req.OperationID, req.Profile); err == nil {
				t.Fatal("A failed test enabled an agent profile")
			}
		})
	}
}

func TestAgentProfileListOmitsModelTestResults(t *testing.T) {
	home, req := profileOperationFixture(t)
	run := func(id string, result error) {
		t.Helper()
		req.OperationID = id
		deps := agentProfileOperationDependencies{Launch: profileNoLaunch, Test: func(context.Context, string, RuntimeCatalog, string) (LiveProviderObservation, error) {
			return successfulProfileObservation(), result
		}}
		if _, err := startAgentProfileOperation(home, req, deps); err != nil {
			t.Fatal(err)
		}
		if err := runAgentProfileOperation(home, id, deps); err != nil {
			t.Fatal(err)
		}
	}
	run("test-operation-0001", nil)
	enabled := req.Profile
	enabled.Enabled, enabled.TestID = true, req.OperationID
	if _, err := AgentProfileSave(home, enabled); err != nil {
		t.Fatal(err)
	}
	before, err := AgentProfileList(home)
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, profile := range before.Profiles {
		listed = listed || (profile.Name == req.Profile.Name && profile.Enabled)
	}
	if !listed {
		t.Fatalf("The list omitted the saved agent profile: %+v", before.Profiles)
	}
	run("test-operation-0002", errors.New("tool failed"))
	status, err := GetAgentProfileOperation(home, req.OperationID)
	if err != nil || status.Status != "failed" {
		t.Fatalf("Unexpected failure: %+v %v", status, err)
	}
	after, err := AgentProfileList(home)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("A failed model test changed the agent profile list: %+v", after.Profiles)
	}
}

func TestAgentProfileOperationRejectsMissingToolEvidenceAndExcessRequests(t *testing.T) {
	for _, observation := range []LiveProviderObservation{
		{SessionID: "session", NativeFile: "native.jsonl", StreamEvents: 1, ProviderRequests: 1},
		{SessionID: "session", NativeFile: "native.jsonl", StreamEvents: 1, ToolUses: 1, ToolResults: 1, ProviderRequests: 3},
	} {
		home, req := profileOperationFixture(t)
		deps := agentProfileOperationDependencies{Launch: profileNoLaunch, Test: func(context.Context, string, RuntimeCatalog, string) (LiveProviderObservation, error) {
			return observation, nil
		}}
		if _, err := startAgentProfileOperation(home, req, deps); err != nil {
			t.Fatal(err)
		}
		if err := runAgentProfileOperation(home, req.OperationID, deps); err != nil {
			t.Fatal(err)
		}
		status, err := GetAgentProfileOperation(home, req.OperationID)
		if err != nil || status.Status != "failed" {
			t.Fatalf("Unexpected status: %+v, %v", status, err)
		}
	}
}

func TestAgentProfileOperationCancellationPreventsLateCredentialPublication(t *testing.T) {
	home := agentProfileTestHome(t)
	req := AgentProfileOperationRequest{OperationID: "authorization-0001", Kind: "authorization", ConnectionID: "claude"}
	entered := make(chan struct{})
	complete := make(chan struct{})
	deps := agentProfileOperationDependencies{Launch: profileNoLaunch, Authorize: func(ctx context.Context, home, id string, notify func(string, string)) (any, error) {
		notify("https://example.invalid/authorize", "ABCD-EFGH")
		close(entered)
		<-complete
		return oauthRuntimeGrant{Credential: id, RefreshToken: "secret-late-grant"}, nil
	}}
	if _, err := startAgentProfileOperation(home, req, deps); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- runAgentProfileOperation(home, req.OperationID, deps) }()
	<-entered
	status, err := GetAgentProfileOperation(home, req.OperationID)
	if err != nil || status.Status != "action_required" || status.UserCode != "ABCD-EFGH" {
		t.Fatalf("Unexpected authorization: %+v %v", status, err)
	}
	if _, err := CancelAgentProfileOperation(home, req.OperationID); err != nil {
		t.Fatal(err)
	}
	close(complete)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "refresh-grants", "claude.json")); !os.IsNotExist(err) {
		t.Fatal("The canceled authorization published a grant")
	}
	status, err = GetAgentProfileOperation(home, req.OperationID)
	if err != nil || status.Status != "cancelled" || status.AuthorizationURL != "" {
		t.Fatalf("Unexpected cancellation: %+v %v", status, err)
	}
}

func TestAgentProfileOperationConcurrentWorkersRunOneTest(t *testing.T) {
	home, req := profileOperationFixture(t)
	calls := 0
	deps := agentProfileOperationDependencies{Launch: profileNoLaunch, Test: func(context.Context, string, RuntimeCatalog, string) (LiveProviderObservation, error) {
		calls++
		return successfulProfileObservation(), nil
	}}
	if _, err := startAgentProfileOperation(home, req, deps); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := runAgentProfileOperation(home, req.OperationID, deps); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls != 1 {
		t.Fatalf("The test ran %d times", calls)
	}
}

func TestAgentProfileOperationRecoversInterruptedWorkerWithoutRetry(t *testing.T) {
	home, req := profileOperationFixture(t)
	if _, err := startAgentProfileOperation(home, req, agentProfileOperationDependencies{Launch: profileNoLaunch}); err != nil {
		t.Fatal(err)
	}
	_, err := updateAgentProfileOperation(home, req.OperationID, func(record *agentProfileOperationRecord) error {
		record.Status = "running"
		record.StartedAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := GetAgentProfileOperation(home, req.OperationID)
	if err != nil || status.Status != "failed" || status.ErrorCode != "interrupted" {
		t.Fatalf("Unexpected recovery: %+v %v", status, err)
	}
}

func TestAgentProfileOperationKeepsTheAdapterCondition(t *testing.T) {
	home, req := profileOperationFixture(t)
	condition := "The harness reported invalid_request. API Error: 400 Claude Code 2.1.217 does not support this model; version 2.1.280 or newer is required."
	stdout := `{"type":"result","is_error":true}` + "\n" +
		`{"event":"infrastructure-failure","provider_error_code":"invalid_request","condition":` + strconv.Quote(condition) + `,"evidence":"Harness stderr: unknown"}` + "\n"
	deps := agentProfileOperationDependencies{Launch: profileNoLaunch, Test: func(context.Context, string, RuntimeCatalog, string) (LiveProviderObservation, error) {
		out := runtimeAuthOutput{Stdout: []byte(stdout), Stderr: []byte("unrelated harness log")}
		return LiveProviderObservation{}, &RuntimeAuthAdapterError{
			Condition:  runtimeAuthAdapterCondition(out, nil),
			Diagnostic: runtimeAuthAdapterDiagnostic(out, nil),
		}
	}}
	if _, err := startAgentProfileOperation(home, req, deps); err != nil {
		t.Fatal(err)
	}
	if err := runAgentProfileOperation(home, req.OperationID, deps); err != nil {
		t.Fatal(err)
	}
	status, err := GetAgentProfileOperation(home, req.OperationID)
	if err != nil || status.Status != "failed" || status.ErrorCode != "model" || status.Detail != condition {
		t.Fatalf("Unexpected status: %+v %v", status, err)
	}
}

func TestRuntimeAuthAdapterConditionRedactsSecrets(t *testing.T) {
	out := runtimeAuthOutput{Stdout: []byte(`{"event":"infrastructure-failure","condition":"refused secret-value","evidence":""}` + "\n")}
	if got := runtimeAuthAdapterCondition(out, []string{"secret-value"}); got != "refused [redacted]" {
		t.Fatalf("condition = %q", got)
	}
	if got := runtimeAuthAdapterCondition(runtimeAuthOutput{Stdout: []byte("plain text\n")}, nil); got != "" {
		t.Fatalf("condition without an event = %q", got)
	}
}

func TestAgentProfileEffortRequiresAnExactTestAndSurvivesSave(t *testing.T) {
	home, req := profileOperationFixture(t)
	req.Profile.Effort = "high"
	deps := agentProfileOperationDependencies{Launch: profileNoLaunch, Test: func(_ context.Context, _ string, catalog RuntimeCatalog, binding string) (LiveProviderObservation, error) {
		if catalog.Bindings[binding].Effort != "high" {
			t.Fatal("The model test lost the effort.")
		}
		return successfulProfileObservation(), nil
	}}
	if _, err := startAgentProfileOperation(home, req, deps); err != nil {
		t.Fatal(err)
	}
	if err := runAgentProfileOperation(home, req.OperationID, deps); err != nil {
		t.Fatal(err)
	}
	for _, effort := range []string{"", "low", "max"} {
		changed := req.Profile
		changed.Effort = effort
		if err := VerifyAgentProfileTest(home, req.OperationID, changed); err == nil {
			t.Fatal("The receipt accepted another effort.")
		}
	}
	req.Profile.Enabled, req.Profile.TestID = true, req.OperationID
	saved, err := AgentProfileSave(home, req.Profile)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, profile := range saved.Profiles {
		if profile.ID == saved.ProfileID {
			found = profile.Effort == "high"
		}
	}
	if !found {
		t.Fatal("The saved profile lost its effort.")
	}
}

func TestProfileEffortFailureNamesTheRequiredCorrection(t *testing.T) {
	code, message := agentProfileOperationError("model_test", &AgentModelEffortError{})
	if code != "effort" || !strings.Contains(message, "Select another effort level") {
		t.Fatalf("The effort failure lost its action: %s %s", code, message)
	}
	code, message = agentProfileOperationError("model_test", errors.New("unsupported effort: private-fixture"))
	if code != "effort" || strings.Contains(message, "private-fixture") {
		t.Fatalf("The native effort failure lost its safe action: %s %s", code, message)
	}
}
