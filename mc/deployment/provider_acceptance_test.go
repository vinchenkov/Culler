package deployment

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func liveProviderHome(t *testing.T, binding string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSignInCatalogConfig(t, home)
	store := filepath.Join(home, "refresh-grants")
	if err := os.Mkdir(store, 0o700); err != nil {
		t.Fatal(err)
	}
	stagedVerifierGrant(t, store, binding)
	return home
}

// The acceptance lane covers each endpoint agent profile of the operator
// catalog: the identity check resolves each one from config.toml, and the
// status of a fresh home reports the binding as absent rather than unknown.
func TestLiveProviderAcceptanceIdentityCoversTheEndpointBindings(t *testing.T) {
	release := strings.Repeat("a", 40)
	for _, endpoint := range fixtureAccounts {
		binding := endpoint.id
		home := liveProviderHome(t, binding)
		if err := validateLiveProviderIdentity(home, release, binding); err != nil {
			t.Fatalf("identity of %s: %v", binding, err)
		}
		status, err := StatusLiveProviderAcceptance(home, release, binding)
		if err != nil || status.Status != "absent" || status.Binding != binding {
			t.Fatalf("status of %s = %+v err %v", binding, status, err)
		}
	}
	home := liveProviderHome(t, "second-endpoint")
	if err := validateLiveProviderIdentity(home, release, "undefined-endpoint"); err == nil ||
		!strings.Contains(err.Error(), "unknown runtime binding") {
		t.Fatalf("unknown binding identity error = %v", err)
	}
}

func TestLiveProviderAcceptanceRetainsBoundedProxyEvidenceWithoutForcedRenewal(t *testing.T) {
	home := liveProviderHome(t, "claude")
	release := strings.Repeat("a", 40)
	started := time.Date(2026, 7, 25, 15, 0, 0, 0, time.UTC)

	claim, err := BeginLiveProviderAcceptance(home, release, "claude", nil, started)
	got := claim.Result()
	if err != nil || got.Status != "running" || got.Binding != "claude" ||
		got.Credential != "claude" || got.Model != "claude-opus-4-8" {
		t.Fatalf("begin = %+v, %v", got, err)
	}
	observation := LiveProviderObservation{
		SessionID: "session-1", NativeFile: "native.jsonl",
		StreamEvents: 3, ToolUses: 1, ToolResults: 1, ProviderRequests: 2,
	}
	for _, count := range []int{0, maxLiveProviderRequests + 1} {
		invalid := observation
		invalid.ProviderRequests = count
		if _, err := CompleteLiveProviderAcceptance(claim, invalid, started.Add(time.Minute)); err == nil {
			t.Fatal("The receipt accepted absent or excessive host request evidence.")
		}
	}
	got, err = CompleteLiveProviderAcceptance(claim, observation, started.Add(time.Minute))
	if err != nil || got.Status != "verified" ||
		got.StreamEvents != 3 || got.ToolUses != 1 || got.ProviderRequests != 2 ||
		got.Credential != "claude" || got.Model != "claude-opus-4-8" {
		t.Fatalf("complete = %+v, %v", got, err)
	}
	replayed, ok, err := ReplayLiveProviderAcceptance(home, release, "claude")
	if err != nil || !ok || replayed != got {
		t.Fatalf("replay = %+v, %t, %v; want %+v", replayed, ok, err, got)
	}
	body, err := os.ReadFile(liveProviderStatePath(home, release, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "refresh") || strings.Contains(string(body), "access") {
		t.Fatalf("acceptance receipt contains credential-shaped bytes: %s", body)
	}
}

func TestLiveProviderAcceptanceRefusesAutomaticRetryAfterAmbiguousStart(t *testing.T) {
	home := liveProviderHome(t, "endpoint")
	release := strings.Repeat("b", 40)
	now := time.Date(2026, 7, 25, 16, 0, 0, 0, time.UTC)
	claim, err := BeginLiveProviderAcceptance(home, release, "endpoint", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BeginLiveProviderAcceptance(home, release, "endpoint", nil, now); err == nil ||
		!strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("retry error = %v", err)
	}
	cleanupCalls := 0
	cleanup := func() error {
		cleanupCalls++
		return nil
	}
	if _, err := CleanupLiveProviderAcceptance(
		home, release, "endpoint", claim.AttemptID(), cleanup); err == nil ||
		!strings.Contains(err.Error(), "still active") {
		t.Fatalf("active cleanup error = %v", err)
	}
	if cleanupCalls != 0 {
		t.Fatalf("active cleanup mutated its callback %d time(s)", cleanupCalls)
	}
	if err := claim.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := CleanupLiveProviderAcceptance(
		home, release, "endpoint", claim.AttemptID(), cleanup)
	if err != nil || got.Status != "cleaned" {
		t.Fatalf("cleanup = %+v, %v", got, err)
	}
	if cleanupCalls != 1 {
		t.Fatalf("inactive cleanup callback calls = %d", cleanupCalls)
	}
	if _, err := BeginLiveProviderAcceptance(home, release, "endpoint", nil, now); err != nil {
		t.Fatalf("begin after explicit cleanup: %v", err)
	}
}

func TestLiveProviderAcceptanceClaimIsAtomicAndBindingGlobal(t *testing.T) {
	home := liveProviderHome(t, "endpoint")
	releaseA := strings.Repeat("c", 40)
	releaseB := strings.Repeat("d", 40)
	now := time.Date(2026, 7, 25, 16, 30, 0, 0, time.UTC)

	start := make(chan struct{})
	var claims [2]*LiveProviderClaim
	var errs [2]error
	var wait sync.WaitGroup
	for index := range claims {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			claims[index], errs[index] = BeginLiveProviderAcceptance(
				home, releaseA, "endpoint", nil, now)
		}(index)
	}
	close(start)
	wait.Wait()

	winners := 0
	var winner *LiveProviderClaim
	for index := range claims {
		if errs[index] == nil {
			winners++
			winner = claims[index]
		} else if !strings.Contains(errs[index].Error(), "ambiguous") {
			t.Fatalf("loser error = %v", errs[index])
		}
	}
	if winners != 1 {
		t.Fatalf("atomic winners = %d; errors = %v", winners, errs)
	}
	if _, err := BeginLiveProviderAcceptance(
		home, releaseB, "endpoint", nil, now); err == nil ||
		!strings.Contains(err.Error(), "binding-global") {
		t.Fatalf("cross-release claim error = %v", err)
	}
	if err := winner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := CleanupLiveProviderAcceptance(
		home, releaseB, "endpoint", winner.AttemptID(), nil); err != nil {
		t.Fatalf("cross-release cleanup: %v", err)
	}
}

func TestInspectLiveProviderOutputRequiresStreamingToolResultAndExactReply(t *testing.T) {
	codex := strings.Join([]string{
		`{"type":"thread.started","thread_id":"11111111-1111-4111-8111-111111111111"}`,
		`{"event":"session-start","session_id":"11111111-1111-4111-8111-111111111111","native_file":"2026/07/25/rollout.jsonl"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.started","item":{"id":"tool-1","type":"command_execution"}}`,
		`{"type":"item.completed","item":{"id":"tool-1","type":"command_execution","aggregated_output":"MC_LIVE_PROVIDER_TOOL_OK","exit_code":0,"status":"completed"}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"MC_LIVE_PROVIDER_TURN_OK"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`,
		`{"event":"turn-complete","status":"success","output":"MC_LIVE_PROVIDER_TURN_OK"}`,
	}, "\n")
	got, err := InspectLiveProviderOutput("codex", []byte(codex))
	if err != nil || got.StreamEvents < 3 || got.ToolUses != 1 || got.ToolResults != 1 {
		t.Fatalf("Codex observation = %+v, %v", got, err)
	}

	claude := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"claude-1"}`,
		`{"event":"session-start","session_id":"claude-1","native_file":"native.jsonl"}`,
		`{"type":"stream_event","event":{"type":"message_start"}}`,
		`{"type":"stream_event","event":{"type":"content_block_start"}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta"}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"printf MC_LIVE_PROVIDER_TOOL_OK"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"MC_LIVE_PROVIDER_TOOL_OK","is_error":false}]}}`,
		`{"type":"stream_event","event":{"type":"message_start"}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"MC_LIVE_PROVIDER_TURN_OK"}]}}`,
		`{"type":"result","is_error":false,"result":"MC_LIVE_PROVIDER_TURN_OK"}`,
		`{"event":"turn-complete","status":"success","output":"MC_LIVE_PROVIDER_TURN_OK"}`,
	}, "\n")
	got, err = InspectLiveProviderOutput("claude-sdk", []byte(claude))
	if err != nil || got.StreamEvents != 5 || got.ToolUses != 1 || got.ToolResults != 1 || got.ProviderRequests != 2 {
		t.Fatalf("Claude observation = %+v, %v", got, err)
	}

	thirdRequest := strings.Replace(claude,
		`{"type":"result","is_error":false,"result":"MC_LIVE_PROVIDER_TURN_OK"}`,
		"{\"type\":\"stream_event\",\"event\":{\"type\":\"message_start\"}}\n{\"type\":\"result\",\"is_error\":false,\"result\":\"MC_LIVE_PROVIDER_TURN_OK\"}", 1)
	if _, err := InspectLiveProviderOutput("claude-sdk", []byte(thirdRequest)); err == nil ||
		!strings.Contains(err.Error(), "exceeded 2 provider requests") {
		t.Fatalf("third provider request error = %v", err)
	}
	noRequest := strings.ReplaceAll(claude,
		`{"type":"stream_event","event":{"type":"message_start"}}`+"\n", "")
	if _, err := InspectLiveProviderOutput("claude-sdk", []byte(noRequest)); err == nil ||
		!strings.Contains(err.Error(), "message start") {
		t.Fatalf("missing message start error = %v", err)
	}

	noStream := strings.ReplaceAll(claude,
		`{"type":"stream_event","event":{"type":"content_block_delta"}}`+"\n", "")
	if _, err := InspectLiveProviderOutput("claude-sdk", []byte(noStream)); err == nil ||
		!strings.Contains(err.Error(), "stream") {
		t.Fatalf("missing stream error = %v", err)
	}

	for _, ambiguous := range []string{
		strings.Replace(codex, `{"type":"turn.started"}`,
			"{\"type\":\"turn.started\"}\n{\"type\":\"turn.started\"}", 1),
		codex + "\n" + `{"event":"turn-complete","status":"success","output":"MC_LIVE_PROVIDER_TURN_OK"}`,
	} {
		if _, err := InspectLiveProviderOutput(
			"codex", []byte(ambiguous)); err == nil {
			t.Fatal("ambiguous Codex stream passed")
		}
	}
}

func TestLiveProviderPromptNamesAWorkingExactShellCommand(t *testing.T) {
	lines := strings.Split(liveProviderPrompt, "\n")
	if len(lines) < 3 {
		t.Fatalf("prompt lines = %q", lines)
	}
	command := exec.Command("sh", "-c", lines[1])
	command.Dir = t.TempDir()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("prompt command: %v: %s", err, output)
	}
	body, err := os.ReadFile(filepath.Join(command.Dir, liveProviderArtifact))
	if err != nil || string(body) != liveProviderToolMarker {
		t.Fatalf("prompt artifact = %q, %v", body, err)
	}
}
