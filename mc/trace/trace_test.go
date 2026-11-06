package trace

import (
	"strings"
	"testing"
)

const (
	claudeRef = "11111111-1111-4111-8111-111111111111"
	codexRef  = "22222222-2222-4222-8222-222222222222"
)

func TestMainTraceIdentityForEachHarness(t *testing.T) {
	for name, tc := range map[string]struct {
		binding, nativeRef          string
		format, sessionID, pagePath string
	}{
		"claude run": {
			"claude-sdk/claude", claudeRef,
			FormatClaude, claudeRef, "/sessions/" + claudeRef + "?msg=last",
		},
		"claude endpoint binding": {
			"claude-sdk/endpoint", claudeRef,
			FormatClaude, claudeRef, "/sessions/" + claudeRef + "?msg=last",
		},
		"codex run": {
			"codex/chatgpt", codexRef,
			FormatCodex, "codex:" + codexRef, "/sessions/codex/" + codexRef + "?msg=last",
		},
		"opencode run": {
			"opencode/catalog", "ses_fixture", FormatCodex,
			"codex:7e556d01-59fc-8ae6-b393-64609e645994", "/sessions/codex/7e556d01-59fc-8ae6-b393-64609e645994?msg=last",
		},
		"binding with a slash in its name": {
			"codex/team/chatgpt", codexRef,
			FormatCodex, "codex:" + codexRef, "/sessions/codex/" + codexRef + "?msg=last",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := Format(tc.binding); got != tc.format {
				t.Errorf("Format(%q) = %q, want %q", tc.binding, got, tc.format)
			}
			if got := SessionID(tc.binding, tc.nativeRef); got != tc.sessionID {
				t.Errorf("SessionID = %q, want %q", got, tc.sessionID)
			}
			if got := Path(tc.binding, tc.nativeRef); got != tc.pagePath {
				t.Errorf("Path = %q, want %q", got, tc.pagePath)
			}
		})
	}
}

func TestRowsWithoutAMainTraceHaveNoAgentsViewSession(t *testing.T) {
	for name, tc := range map[string]struct {
		binding, nativeRef, format string
	}{
		"merge step":                     {"mechanical", "", ""},
		"fake harness":                   {"fake/fake", claudeRef, ""},
		"static harness":                 {"static/static", claudeRef, ""},
		"no harness":                     {"none/none", claudeRef, ""},
		"empty binding":                  {"", claudeRef, ""},
		"harness without a binding":      {"claude-sdk", claudeRef, ""},
		"harness name as a binding":      {"fake/claude-sdk", claudeRef, ""},
		"claude run before registration": {"claude-sdk/claude", "", FormatClaude},
		"codex run before registration":  {"codex/chatgpt", "", FormatCodex},
		"native ID with a path":          {"claude-sdk/claude", "../" + claudeRef, FormatClaude},
		"native ID with a query":         {"codex/chatgpt", codexRef + "?msg=1", FormatCodex},
		"native ID with a space":         {"claude-sdk/claude", "a b", FormatClaude},
		"native ID with a dot prefix":    {"claude-sdk/claude", ".hidden", FormatClaude},
		"native ID with a dash prefix":   {"claude-sdk/claude", "-flag", FormatClaude},
		"native ID with a colon":         {"codex/chatgpt", "codex:" + codexRef, FormatCodex},
		"native ID above the length":     {"claude-sdk/claude", strings.Repeat("a", 129), FormatClaude},
	} {
		t.Run(name, func(t *testing.T) {
			if got := Format(tc.binding); got != tc.format {
				t.Errorf("Format(%q) = %q, want %q", tc.binding, got, tc.format)
			}
			if got := SessionID(tc.binding, tc.nativeRef); got != "" {
				t.Errorf("SessionID = %q, want no session", got)
			}
			if got := Path(tc.binding, tc.nativeRef); got != "" {
				t.Errorf("Path = %q, want no path", got)
			}
		})
	}
	longest := strings.Repeat("a", 128)
	if got := SessionID("claude-sdk/claude", longest); got != longest {
		t.Fatalf("SessionID of a 128-byte native ID = %q", got)
	}
}

func TestRunDisplayNameJoinsTheItemsThatExist(t *testing.T) {
	for name, tc := range map[string]struct {
		projectTitle, role, taskTitle, runID, want string
	}{
		"run with a task": {
			"Culler", "worker", "Copy traces outside the tick", "r-1a2b",
			"Culler · worker · Copy traces outside the tick · r-1a2b",
		},
		"run without a task": {"Culler", "strategist", "", "r-3c4d", "Culler · strategist · r-3c4d"},
		"blank task title":   {"Culler", "arbiter", "  ", "r-5e6f", "Culler · arbiter · r-5e6f"},
		"Project ID as the title": {
			ProjectName("", "ws-culler"), "verifier", "Check the pin", "r-7a8b",
			"ws-culler · verifier · Check the pin · r-7a8b",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := RunDisplayName(tc.projectTitle, tc.role, tc.taskTitle, tc.runID); got != tc.want {
				t.Fatalf("RunDisplayName = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProjectNamePrefersTheTitle(t *testing.T) {
	if got := ProjectName("Culler", "ws-culler"); got != "Culler" {
		t.Fatalf("ProjectName with a title = %q", got)
	}
	for _, title := range []string{"", " \t"} {
		if got := ProjectName(title, "ws-culler"); got != "ws-culler" {
			t.Fatalf("ProjectName(%q) = %q, want the Project ID", title, got)
		}
	}
}

func TestCollieDisplayNameNamesTheVisionProjectWhenPresent(t *testing.T) {
	if got := CollieDisplayName("Culler", "h-9f8e"); got != "Collie · Culler · h-9f8e" {
		t.Fatalf("Vision Project session = %q", got)
	}
	if got := CollieDisplayName("", "h-9f8e"); got != "Collie · h-9f8e" {
		t.Fatalf("ordinary session = %q", got)
	}
}
