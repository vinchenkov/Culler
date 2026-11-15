//go:build !test_fake_routing

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc/deployment"
)

func providerAcceptanceHome(t *testing.T, binding string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	writeEndpointFixtureConfig(t, home, "chatgpt", "claude", "endpoint")
	store := filepath.Join(home, "refresh-grants")
	if err := os.Mkdir(store, 0o700); err != nil {
		t.Fatal(err)
	}
	var body string
	switch binding {
	case "claude", "endpoint":
		body = `{"credential":"claude","channel":"claude","token_url":"https://platform.claude.com/v1/oauth/token","client_id":"9d1c250a-e61b-44d9-88ed-5944d1962f5e","refresh_token":"before","scope":"user:inference"}` + "\n"
	}
	body = strings.Replace(body, `"credential":"claude"`, `"credential":"`+binding+`"`, 1)
	if err := os.WriteFile(filepath.Join(store, binding+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestProviderAcceptanceRunsOneExplicitTurnThenReplaysWithoutSpend(t *testing.T) {
	home := providerAcceptanceHome(t, "claude")
	release := strings.Repeat("a", 40)
	now := time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC)
	active, turns := 0, 0
	deps := providerAcceptanceDeps{
		requireActive: func(string) error { active++; return nil },
		runTurn: func(home, _, _, _, identity string) (deployment.LiveProviderObservation, error) {
			turns++
			current, err := deployment.AgentConnectionAuthorizationIdentity(home, "claude")
			if err != nil || identity != current {
				t.Fatal("The attempt lacks its connection revision.")
			}
			return deployment.LiveProviderObservation{SessionID: "session-1", NativeFile: "native.jsonl", StreamEvents: 3, ToolUses: 1, ToolResults: 1, ProviderRequests: 2}, nil
		},
		now:         func() time.Time { now = now.Add(time.Second); return now },
		environment: func() []string { return nil },
	}
	first, err := executeProviderAcceptance(home, release, "run", "claude", "", deps)
	if err != nil || first.Status != "verified" {
		t.Fatalf("first = %+v, %v", first, err)
	}
	second, err := executeProviderAcceptance(home, release, "run", "claude", "", deps)
	if err != nil || second != first || active != 1 || turns != 1 {
		t.Fatalf("replay = %+v, %v; active=%d turns=%d", second, err, active, turns)
	}
	status, err := executeProviderAcceptance(home, release, "status", "claude", "", deps)
	if err != nil || status != first || active != 1 || turns != 1 {
		t.Fatalf("status = %+v, %v; active=%d turns=%d", status, err, active, turns)
	}
}

func TestProviderAcceptanceRequiresSupervisionBeforeAnyProviderRequest(t *testing.T) {
	home := providerAcceptanceHome(t, "claude")
	deps := providerAcceptanceDeps{
		requireActive: func(string) error { return errors.New("supervision is unavailable") },
		runTurn: func(string, string, string, string, string) (deployment.LiveProviderObservation, error) {
			t.Fatal("The provider request ran before supervision")
			return deployment.LiveProviderObservation{}, nil
		},
		now: time.Now, environment: func() []string { return nil },
	}
	if _, err := executeProviderAcceptance(home, strings.Repeat("c", 40), "run", "claude", "", deps); err == nil || !strings.Contains(err.Error(), "supervision is unavailable") {
		t.Fatalf("Supervision refusal = %v", err)
	}
}

func TestProviderAcceptanceNeverRetriesAnAmbiguousTurnAutomatically(t *testing.T) {
	home := providerAcceptanceHome(t, "endpoint")
	release := strings.Repeat("b", 40)
	turns := 0
	deps := providerAcceptanceDeps{
		requireActive: func(string) error { return nil },
		runTurn: func(string, string, string, string, string) (deployment.LiveProviderObservation, error) {
			turns++
			return deployment.LiveProviderObservation{}, os.ErrDeadlineExceeded
		},
		now:         func() time.Time { return time.Now() },
		environment: func() []string { return nil },
	}
	if _, err := executeProviderAcceptance(home, release, "run", "endpoint", "", deps); err == nil ||
		!strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("first error = %v", err)
	}
	if _, err := executeProviderAcceptance(home, release, "run", "endpoint", "", deps); err == nil ||
		!strings.Contains(err.Error(), "ambiguous") || turns != 1 {
		t.Fatalf("retry error = %v; turns=%d", err, turns)
	}
	status, err := executeProviderAcceptance(home, release, "status", "endpoint", "", deps)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := executeProviderAcceptance(home, release, "cleanup", "endpoint", status.AttemptID, deps); err != nil ||
		got.Status != "cleaned" {
		t.Fatalf("cleanup = %+v, %v", got, err)
	}
}

func TestProviderAcceptanceCLIRequiresOneBindingAndExplicitSpendConfirmation(t *testing.T) {
	for _, args := range [][]string{
		{"__acceptance", "provider", "run", "--binding", "endpoint"},
		{"__acceptance", "provider", "run", "--confirm-live-spend"},
		{"__acceptance", "provider", "run", "--binding", "endpoint", "--binding", "claude", "--confirm-live-spend"},
		{"__acceptance", "provider", "run", "--all", "--confirm-live-spend"},
		{"__acceptance", "provider", "status", "--binding", "endpoint", "--confirm-live-spend"},
		{"__acceptance", "provider", "cleanup", "--binding", "endpoint"},
		{"__acceptance", "provider", "cleanup", "--binding", "endpoint", "--attempt", "one", "--attempt", "two"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, bytes.NewReader(nil), &stdout, &stderr); code != 2 ||
			!strings.Contains(stdout.String(), `"code":"usage"`) {
			t.Fatalf("args %v = code %d stdout %q stderr %q",
				args, code, stdout.String(), stderr.String())
		}
	}
}
