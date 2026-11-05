// Package trace computes the AgentsView identity of each main trace from the
// binding and the native session ID in the spine. Every consumer uses these
// functions, so AgentsView session IDs, links, and display names agree.
package trace

import (
	"regexp"
	"strings"
)

const (
	FormatClaude = "claude"
	FormatCodex  = "codex"
)

const (
	maxNativeRef = 128
	separator    = " · "
)

// Format returns the trace format of the harness part of a
// "<harness>/<binding>" value, or an empty string for a harness without a
// trace.
func Format(binding string) string {
	harness, _, found := strings.Cut(binding, "/")
	if !found {
		return ""
	}
	switch harness {
	case "claude-sdk":
		return FormatClaude
	case "codex":
		return FormatCodex
	}
	return ""
}

// SessionID returns the AgentsView session ID of the main trace, or an empty
// string when the row has no main trace. A native session ID that is not a
// plain identifier gives no main trace, because the ID becomes a file name and
// a link path.
func SessionID(binding, nativeRef string) string {
	if !plainIdentifier(nativeRef) {
		return ""
	}
	switch Format(binding) {
	case FormatClaude:
		return nativeRef
	case FormatCodex:
		return "codex:" + nativeRef
	}
	return ""
}

// Path returns the AgentsView page of the main trace at its last message, or
// an empty string when the row has no main trace.
func Path(binding, nativeRef string) string {
	if !plainIdentifier(nativeRef) {
		return ""
	}
	switch Format(binding) {
	case FormatClaude:
		return "/sessions/" + nativeRef + "?msg=last"
	case FormatCodex:
		return "/sessions/codex/" + nativeRef + "?msg=last"
	}
	return ""
}

// ProjectName returns the Project title, or the Project ID when the Project
// has no title.
func ProjectName(title, id string) string {
	if strings.TrimSpace(title) == "" {
		return id
	}
	return title
}

// RunDisplayName names the main AgentsView session of a pipeline run. The
// caller passes the result of ProjectName as projectTitle, and an empty
// taskTitle for a run without a task.
func RunDisplayName(projectTitle, role, taskTitle, runID string) string {
	return join(projectTitle, role, taskTitle, runID)
}

// HomieDisplayName names the main AgentsView session of a Homie session.
func HomieDisplayName(visionProjectTitle, sessionID string) string {
	return join("Homie", visionProjectTitle, sessionID)
}

func join(items ...string) string {
	kept := make([]string, 0, len(items))
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			kept = append(kept, item)
		}
	}
	return strings.Join(kept, separator)
}

func plainIdentifier(value string) bool {
	if value == "" || len(value) > maxNativeRef || value[0] == '.' || value[0] == '-' {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') &&
			r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

// SetupDisplayName names one setup or validation attempt.
func SetupDisplayName(projectTitle, kind, operationID string) string {
	return join(projectTitle, kind, operationID)
}

var claudeFilename = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*(/[A-Za-z0-9_][A-Za-z0-9._-]*)*\.jsonl$`)
var codexFilename = regexp.MustCompile(`^([0-9]{4}/[0-9]{2}/[0-9]{2}/)?rollout-[A-Za-z0-9._-]+\.jsonl$`)

// ValidFilename accepts only a native trace path below a session folder.
func ValidFilename(binding, filename string) bool {
	if len(filename) > 1024 {
		return false
	}
	switch Format(binding) {
	case FormatClaude:
		return claudeFilename.MatchString(filename)
	case FormatCodex:
		return codexFilename.MatchString(filename)
	}
	return false
}
