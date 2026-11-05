package deployment

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func setupRuntimeLoginFixture(t *testing.T, binary, script string) []string {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	home := filepath.Join(root, "home")
	configuration := filepath.Join(root, "configuration")
	for _, directory := range []string{bin, home, configuration} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, binary), []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	key := "CODEX_HOME"
	if binary == "claude" {
		key = "CLAUDE_CONFIG_DIR"
	}
	return []string{"PATH=" + bin, "HOME=" + home, key + "=" + configuration}
}

func TestSetupRuntimeAuthLoginExposesOnlyBrowserInstructions(t *testing.T) {
	environment := setupRuntimeLoginFixture(t, "codex", `
printf '%s\n' 'Routine diagnostic fixture-secret' 'Debug reference: DEAD-BEEF' >&2
printf '%s\n' 'https://example.test/docs' 'https://example.test/authorize?access_token=fixture-secret'
printf '%s\n' 'https://example.test/oauth/authorize?response_type=code&client_id=fixture&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&scope=openid+profile+email+offline_access&code_challenge=fixture&code_challenge_method=S256&id_token_add_organizations=true&codex_cli_simplified_flow=true&state=fixture&originator=codex_cli_rs'
printf '%s\n' "$@" > "$CODEX_HOME/arguments"
`)
	var notifications [][2]string
	var logs bytes.Buffer
	login := NewSetupRuntimeAuthLogin(context.Background(), func(address, code string) {
		notifications = append(notifications, [2]string{address, code})
	})
	if err := login("codex", []string{"exec", "unselected request"}, environment, nil, &logs); err != nil {
		t.Fatal(err)
	}
	if len(notifications) == 0 || notifications[len(notifications)-1] != [2]string{"https://example.test/oauth/authorize?response_type=code&client_id=fixture&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&scope=openid+profile+email+offline_access&code_challenge=fixture&code_challenge_method=S256&id_token_add_organizations=true&codex_cli_simplified_flow=true&state=fixture&originator=codex_cli_rs", ""} {
		t.Fatalf("The login instructions are incorrect: %v", notifications)
	}
	for _, notification := range notifications {
		if strings.Contains(strings.Join(notification[:], " "), "fixture-secret") || notification[1] == "DEAD-BEEF" {
			t.Fatal("The login exposed a routine diagnostic or secret.")
		}
	}
	if logs.Len() != 0 {
		t.Fatal("The login sent routine logs to the terminal.")
	}
	arguments, err := os.ReadFile(filepath.Join(environmentValue(environment, "CODEX_HOME"), "arguments"))
	if err != nil || string(arguments) != "login\n" {
		t.Fatalf("The Codex login did not select browser authorization: %v", err)
	}
}

func TestSetupRuntimeAuthLoginPreservesOnlyTheIsolatedEnvironment(t *testing.T) {
	environment := setupRuntimeLoginFixture(t, "claude", `
printf '%s\n' "$@" > "$CLAUDE_CONFIG_DIR/arguments"
/usr/bin/env > "$CLAUDE_CONFIG_DIR/environment"
`)
	environment = append(environment, "BASH_ENV=/fixture/unselected", "CODEX_HOME=/fixture/source",
		"CLAUDE_SECURESTORAGE_CONFIG_DIR=/fixture/source", "ANTHROPIC_BASE_URL=https://unselected.invalid")
	login := NewSetupRuntimeAuthLogin(context.Background(), nil)
	if err := login("claude", []string{"unselected"}, environment, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	directory := environmentValue(environment, "CLAUDE_CONFIG_DIR")
	arguments, err := os.ReadFile(filepath.Join(directory, "arguments"))
	if err != nil || string(arguments) != "auth\nlogin\n--claudeai\n" {
		t.Fatalf("The Claude login arguments are incorrect: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(directory, "environment"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"BASH_ENV=", "CODEX_HOME=", "CLAUDE_SECURESTORAGE_CONFIG_DIR=", "ANTHROPIC_BASE_URL="} {
		if strings.Contains(string(body), name) {
			t.Fatal("The login retained an unselected environment override.")
		}
	}
	for _, name := range []string{"HOME", "CLAUDE_CONFIG_DIR"} {
		if !strings.Contains(string(body), name+"="+environmentValue(environment, name)+"\n") {
			t.Fatal("The login lost an isolated source directory.")
		}
	}
}

func TestSetupRuntimeAuthLoginRejectsUnavailableBoundariesBeforeExecution(t *testing.T) {
	environment := setupRuntimeLoginFixture(t, "codex", `printf '%s' 'executed' > "$CODEX_HOME/executed"`)
	for _, fixture := range []struct {
		binary      string
		environment []string
	}{
		{binary: "unselected", environment: environment},
		{binary: "codex", environment: []string{"HOME=/fixture/home"}},
		{binary: "codex", environment: []string{"CODEX_HOME=/fixture/config"}},
		{binary: "codex", environment: []string{"HOME=/fixture/home", "CODEX_HOME=/fixture/home"}},
		{binary: "codex", environment: append(append([]string{}, environment...), "OPENAI_API_KEY=fixture-secret")},
	} {
		login := NewSetupRuntimeAuthLogin(context.Background(), nil)
		if err := login(fixture.binary, nil, fixture.environment, nil, io.Discard); err == nil || strings.Contains(err.Error(), "fixture-secret") {
			t.Fatal("The login accepted an unsafe boundary or exposed a credential.")
		}
	}
	if _, err := os.Lstat(filepath.Join(environmentValue(environment, "CODEX_HOME"), "executed")); !os.IsNotExist(err) {
		t.Fatal("The rejected login executed a command.")
	}
}

func TestSetupRuntimeAuthLoginHonorsTheContextDeadline(t *testing.T) {
	environment := setupRuntimeLoginFixture(t, "codex", "exec /bin/sleep 10")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	login := NewSetupRuntimeAuthLogin(ctx, nil)
	start := time.Now()
	err := login("codex", nil, environment, nil, io.Discard)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("The login exceeded its deadline: %v", err)
	}
}

func TestSetupRuntimeAuthLoginFailureConcealsCommandOutput(t *testing.T) {
	environment := setupRuntimeLoginFixture(t, "codex", "printf '%s' 'fixture-secret' >&2\nexit 1")
	var logs bytes.Buffer
	err := NewSetupRuntimeAuthLogin(context.Background(), nil)("codex", nil, environment, nil, &logs)
	if err == nil || strings.Contains(err.Error(), "fixture-secret") || logs.Len() != 0 {
		t.Fatal("The failed login exposed command output.")
	}
}

func TestSetupRuntimeAuthorizationFilterRejectsSecretAndUnsupportedURLs(t *testing.T) {
	for _, address := range []string{
		"http://example.test/authorize",
		"https://example.test/docs/authentication",
		"https://operator:fixture-secret@example.test/authorize",
		"https://example.test/authorize#access_token=fixture-secret",
		"https://example.test/authorize?access_token=fixture-secret",
		"https://example.test/authorize?refresh_token=fixture-secret",
		"https://example.test/authorize?id_token=fixture-secret",
		"https://example.test/authorize?token=fixture-secret",
		"https://example.test/authorize?api_key=fixture-secret",
		"https://example.test/authorize?client_secret=fixture-secret",
		"https://example.test/authorize?code_verifier=fixture-secret",
		"https://example.test/authorize?code=fixture-secret",
		"https://example.test/authorize?Access_Token=fixture-secret",
		"https://example.test/authorize?access%5ftoken=fixture-secret",
		"https://example.test/authorize?redirect_uri=https%3A%2F%2Foperator%3Afixture-secret%40example.test%2Fcallback",
	} {
		calls := 0
		output := &setupRuntimeAuthorizationOutput{parser: agentAuthorizationOutput{notify: func(string, string) { calls++ }}}
		if _, err := io.WriteString(output, address+"\nCode: ABCD-EFGH\n"); err != nil {
			t.Fatal(err)
		}
		if calls != 0 {
			t.Fatalf("The filter exposed an unsupported authorization address: %s", address)
		}
	}
}

func TestSetupRuntimeAuthorizationFilterRetainsCompleteAuthorizationInstructions(t *testing.T) {
	address := "https://example.test/oauth/authorize?code=true&client_id=fixture&response_type=code&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fcallback&scope=user%3Ainference&code_challenge=fixture&code_challenge_method=S256&state=fixture&orgUUID=fixture&login_method=fixture"
	var notifications [][2]string
	output := &setupRuntimeAuthorizationOutput{parser: agentAuthorizationOutput{notify: func(address, code string) {
		notifications = append(notifications, [2]string{address, code})
	}}}
	for _, part := range []string{"https://example.test/authorize?access_", "token=fixture-secret\n", "\x1b[32m" + address[:60], address[60:] + "\x1b[0m\n", "ABCD-EFGH"} {
		if _, err := io.WriteString(output, part); err != nil {
			t.Fatal(err)
		}
	}
	output.flush()
	if !reflect.DeepEqual(notifications, [][2]string{{address, ""}, {address, "ABCD-EFGH"}}) {
		t.Fatalf("The filter changed the authorization instructions: %v", notifications)
	}
}

func TestSetupRuntimeAuthorizationFilterDiscardsOversizedLines(t *testing.T) {
	calls := 0
	output := &setupRuntimeAuthorizationOutput{parser: agentAuthorizationOutput{notify: func(string, string) { calls++ }}}
	if _, err := io.WriteString(output, "https://example.test/device "+strings.Repeat("x", 16384)+"\n"); err != nil {
		t.Fatal(err)
	}
	if calls != 0 || len(output.line) != 0 {
		t.Fatal("The filter retained an oversized line.")
	}
}
