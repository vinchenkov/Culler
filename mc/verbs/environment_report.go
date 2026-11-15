package verbs

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var reportCredential = regexp.MustCompile(`(?i)(["']?(?:access_token|refresh_token|id_token|auth_token|api[_-]?key|password|client_secret|authorization)["']?\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
var reportBearer = regexp.MustCompile(`(?i)\bBearer\s+[^\s"',;]+`)
var reportURLCredential = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/\s@]+@`)
var reportURLQuery = regexp.MustCompile(`(https?://[^\s?#]+)[?#][^\s)]+`)

func redactEnvironmentReport(value string, configuration *EnvironmentConfiguration) string {
	secrets := []string{}
	if configuration != nil {
		for _, secret := range configuration.Secrets {
			secrets = append(secrets, os.Getenv(secret.Name))
		}
	}
	for _, entry := range os.Environ() {
		name, secret, _ := strings.Cut(entry, "=")
		name = strings.ToLower(name)
		if strings.Contains(name, "token") || strings.Contains(name, "secret") || strings.Contains(name, "password") || strings.Contains(name, "api_key") {
			secrets = append(secrets, secret)
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[redacted]")
		}
	}
	value = reportBearer.ReplaceAllString(value, "Bearer [redacted]")
	value = reportCredential.ReplaceAllString(value, "${1}[redacted]")
	value = reportURLCredential.ReplaceAllString(value, "${1}[redacted]@")
	value = reportURLQuery.ReplaceAllString(value, "${1}?[redacted]")
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, value))
}

func validateEnvironmentReport(req *EnvironmentRequest, configuration *EnvironmentConfiguration) error {
	if req.Outcome != "proposal" && req.Outcome != "complete" {
		return Usagef("The setup outcome must be proposal or complete.")
	}
	if req.Outcome == "proposal" && req.Configuration == nil {
		return Usagef("An image proposal requires its exact configuration.")
	}
	if req.Error != "" || req.Explanation != "" {
		return Usagef("Use summary and details for the setup report.")
	}
	if len(req.Summary) > 1024 {
		return Usagef("The setup summary permits at most 1024 bytes.")
	}
	if len(req.Details) > 24000 {
		return Usagef("The setup details permit at most 24000 bytes.")
	}
	req.Summary = redactEnvironmentReport(req.Summary, configuration)
	req.Details = redactEnvironmentReport(req.Details, configuration)
	if req.Summary == "" || strings.ContainsAny(req.Summary, "\n\r") {
		return Usagef("The setup summary requires one paragraph with at most two short sentences.")
	}
	// Sentence stops require whitespace or the end, so tool versions retain their periods.
	words, sentences := 0, 0
	for _, word := range strings.Fields(req.Summary) {
		words++
		if words > 25 {
			return Usagef("Each setup summary sentence permits at most 25 words.")
		}
		ending := strings.TrimRight(word, "\"'`)")
		if strings.HasSuffix(ending, ".") || strings.HasSuffix(ending, "!") || strings.HasSuffix(ending, "?") {
			sentences++
			words = 0
		}
	}
	if words > 0 {
		sentences++
	}
	if sentences > 2 {
		return Usagef("The setup summary permits at most two sentences.")
	}
	for key, value := range req.ToolVersions {
		req.ToolVersions[key] = redactEnvironmentReport(value, configuration)
	}
	for i, value := range req.PackageRecords {
		req.PackageRecords[i] = redactEnvironmentReport(value, configuration)
	}
	return nil
}
