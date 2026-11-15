package onboarding

import (
	"os"
	"regexp"
	"strings"
	"unicode"
)

var diagnosticCredentials = regexp.MustCompile(`(?i)(["']?(?:access_token|refresh_token|id_token|auth_token|bot_token|api[_-]?key|password|client_secret|authorization)["']?\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
var diagnosticBearer = regexp.MustCompile(`(?i)\bBearer\s+[^\s"',;]+`)
var diagnosticURLCredentials = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/\s@]+@`)
var diagnosticURLQuery = regexp.MustCompile(`(https?://[^\s?#]+)[?#][^\s]+`)
var diagnosticANSI = regexp.MustCompile("\\x1b(?:\\[[0-?]*[ -/]*[@-~]|\\][^\\x07]*(?:\\x07|\\x1b\\\\))")

// Diagnostic removes credentials and terminal controls before it limits the displayed text.
func Diagnostic(message string, secrets ...string) string {
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		name = strings.ToLower(name)
		if strings.Contains(name, "token") || strings.Contains(name, "secret") || strings.Contains(name, "password") || strings.Contains(name, "api_key") {
			secrets = append(secrets, value)
		}
	}
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	message = diagnosticBearer.ReplaceAllString(message, "Bearer [redacted]")
	message = diagnosticCredentials.ReplaceAllString(message, "${1}[redacted]")
	message = diagnosticURLCredentials.ReplaceAllString(message, "${1}[redacted]@")
	message = diagnosticURLQuery.ReplaceAllString(message, "${1}?[redacted]")
	message = diagnosticANSI.ReplaceAllString(message, "")
	message = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, message)
	runes := []rune(strings.TrimSpace(message))
	if len(runes) > 4096 {
		return string(runes[:4096]) + "\n[Diagnostic truncated.]"
	}
	if len(runes) == 0 {
		return "The operation returned no diagnostic text."
	}
	return string(runes)
}
