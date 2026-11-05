package onboarding

import (
	"strings"
	"testing"
)

func TestDiagnosticKeepsCauseWithoutCredentialsOrTerminalControls(t *testing.T) {
	text := Diagnostic("permission denied: /fixture/home/config.toml\nAuthorization: Bearer bearer-secret\n{\"refresh_token\":\"refresh-secret\"}\nhttps://user:url-secret@example.test/check?token=query-secret\nknown-secret\x1b[2J", "known-secret")
	for _, secret := range []string{"bearer-secret", "refresh-secret", "url-secret", "query-secret", "known-secret", "\x1b"} {
		if strings.Contains(text, secret) {
			t.Fatalf("diagnostic exposed %q", secret)
		}
	}
	if !strings.Contains(text, "permission denied: /fixture/home/config.toml") {
		t.Fatal("diagnostic lost the cause and path")
	}
	if !strings.Contains(Diagnostic(strings.Repeat("x", 5000)), "truncated") {
		t.Fatal("diagnostic omitted its size limit")
	}
}
