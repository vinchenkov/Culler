package evalcase

import (
	"fmt"
	"regexp"
)

var authenticationMaterial = regexp.MustCompile(`(?i)-----BEGIN (?:[A-Z ]+ )?PRIVATE KEY-----|["'](?:access_token|refresh_token|id_token|api_key|auth_token|session_cookie)["']\s*[:=]\s*["'][A-Za-z0-9._~+/=-]{16,}["']|Bearer [A-Za-z0-9._~+/=-]{24,}|\bsk-[A-Za-z0-9_-]{24,}`)

// CheckPublic rejects recognizable authentication material without recording its value.
func CheckPublic(name string, body []byte) error {
	if authenticationMaterial.Match(body) {
		return fmt.Errorf("The capture excludes authentication material in %s.", name)
	}
	return nil
}
