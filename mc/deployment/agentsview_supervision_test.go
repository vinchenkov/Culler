package deployment

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func parsePlistValue(t *testing.T, dec *xml.Decoder, start xml.StartElement) any {
	t.Helper()
	switch start.Name.Local {
	case "string", "integer":
		var text string
		if err := dec.DecodeElement(&text, &start); err != nil {
			t.Fatal(err)
		}
		return text
	case "true", "false":
		if err := dec.Skip(); err != nil {
			t.Fatal(err)
		}
		return start.Name.Local == "true"
	case "array", "dict":
		list := []any{}
		dict := map[string]any{}
		key := ""
		for {
			token, err := dec.Token()
			if err != nil {
				t.Fatal(err)
			}
			switch element := token.(type) {
			case xml.StartElement:
				if start.Name.Local == "dict" && element.Name.Local == "key" {
					if err := dec.DecodeElement(&key, &element); err != nil {
						t.Fatal(err)
					}
					continue
				}
				value := parsePlistValue(t, dec, element)
				if start.Name.Local == "dict" {
					dict[key] = value
				} else {
					list = append(list, value)
				}
			case xml.EndElement:
				if start.Name.Local == "dict" {
					return dict
				}
				return list
			}
		}
	}
	t.Fatalf("unexpected property list element %q", start.Name.Local)
	return nil
}

func parsePlist(t *testing.T, body []byte) map[string]any {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		token, err := dec.Token()
		if err != nil {
			t.Fatalf("property list has no dict: %v", err)
		}
		if start, ok := token.(xml.StartElement); ok && start.Name.Local == "dict" {
			return parsePlistValue(t, dec, start).(map[string]any)
		}
	}
}

func TestPreparedAgentsViewUnitRunsThePinnedServerOnLoopbackOnly(t *testing.T) {
	home, spec := supervisionFixture(t)
	spec.AgentsViewBind = "localhost:7444"
	if _, _, err := PrepareSupervision(home, spec, func(string) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(home, "supervision", "LaunchAgents", "agentsview.plist"))
	if err != nil {
		t.Fatal(err)
	}
	names, _ := RuntimeNames(home)
	paths := AgentsViewPathsFor(home)
	want := map[string]any{
		"Label": "com.culler." + names.Suffix + ".agentsview",
		"ProgramArguments": []any{
			filepath.Join(home, "bin", "agentsview"), "serve", "--host", "localhost", "--port", "7444",
			"--no-browser", "--no-update-check",
		},
		"WorkingDirectory": filepath.Join(home, "agentsview", "home"),
		"EnvironmentVariables": map[string]any{
			"AGENTSVIEW_DATA_DIR":             filepath.Join(home, "agentsview", "data"),
			"AGENTSVIEW_TELEMETRY_ENABLED":    "0",
			"AGENTSVIEW_DISABLE_UPDATE_CHECK": "1",
			"HOME":                            filepath.Join(home, "agentsview", "home"),
			"PATH":                            "/usr/bin:/bin",
			"HTTP_PROXY":                      "http://127.0.0.1:9",
			"HTTPS_PROXY":                     "http://127.0.0.1:9",
		},
		"RunAtLoad":         true,
		"KeepAlive":         true,
		"ProcessType":       "Background",
		"ThrottleInterval":  "10",
		"StandardOutPath":   filepath.Join(home, "logs", "agentsview.stdout.log"),
		"StandardErrorPath": filepath.Join(home, "logs", "agentsview.stderr.log"),
	}
	if got := parsePlist(t, body); !reflect.DeepEqual(got, want) {
		t.Fatalf("AgentsView property list:\n got %#v\nwant %#v", got, want)
	}
	if paths.Executable != filepath.Join(home, "bin", "agentsview") || paths.Home != filepath.Join(home, "agentsview", "home") {
		t.Fatalf("paths = %+v", paths)
	}
}

func TestPreparedAgentsViewConfigurationReadsOnlyTheTraceCopies(t *testing.T) {
	home, spec := supervisionFixture(t)
	if _, _, err := PrepareSupervision(home, spec, func(string) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(home, "supervision", "agentsview.toml"))
	if err != nil {
		t.Fatal(err)
	}
	pin, err := LoadAgentsViewPin()
	if err != nil {
		t.Fatal(err)
	}
	disabled := make([]string, len(pin.DisabledAgents))
	for i, agent := range pin.DisabledAgents {
		disabled[i] = `"` + agent + `"`
	}
	token, err := ReadAgentsViewToken(home)
	if err != nil {
		t.Fatal(err)
	}
	want := `host = "127.0.0.1"
port = 7334
require_auth = true
auth_token = "` + token + `"
disable_update_check = true
archive_content = "full"
result_content_blocked_categories = []
tool_result_images = "keep"
disabled_agents = [` + strings.Join(disabled, ", ") + `]

[terminal]
mode = "clipboard"

[agents.claude]
dirs = ["` + filepath.Join(home, "agentsview", "traces", "claude", "projects") + `"]

[agents.codex]
dirs = ["` + filepath.Join(home, "agentsview", "traces", "codex", "sessions") + `"]
`
	if string(body) != want {
		t.Fatalf("AgentsView configuration:\n%s\nwant:\n%s", body, want)
	}
}

func TestAgentsViewConfigurationEscapesPathText(t *testing.T) {
	paths := AgentsViewPathsFor(`/Users/o/"quoted"\home`)
	body := string(agentsViewConfigTOML(paths, "::1", 7334, []string{"cursor"}, strings.Repeat("c", 64)))
	for _, want := range []string{
		`host = "::1"`,
		`dirs = ["/Users/o/\"quoted\"\\home/agentsview/traces/claude/projects"]`,
		`disabled_agents = ["cursor"]`,
		`auth_token = "` + strings.Repeat("c", 64) + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("configuration lacks %q:\n%s", want, body)
		}
	}
	if got := tomlString("a\tb\x7f"); got != `"a\u0009b\u007F"` {
		t.Fatalf("control characters = %s", got)
	}
}

func TestPrepareSupervisionRefusesUnsafeViewerAddresses(t *testing.T) {
	for name, change := range map[string]func(*SupervisionSpec){
		"AgentsView off loopback":       func(s *SupervisionSpec) { s.AgentsViewBind = "0.0.0.0:7334" },
		"wiki address off loopback":     func(s *SupervisionSpec) { s.WikiBind = "192.168.1.5:7335" },
		"AgentsView without a port":     func(s *SupervisionSpec) { s.AgentsViewBind = "127.0.0.1" },
		"wiki address shares dashboard": func(s *SupervisionSpec) { s.WikiBind = "127.0.0.1:7333" },
		"viewers share a port":          func(s *SupervisionSpec) { s.WikiBind = "localhost:7334" },
	} {
		t.Run(name, func(t *testing.T) {
			home, spec := supervisionFixture(t)
			change(&spec)
			if _, _, err := PrepareSupervision(home, spec, func(string) (bool, error) { return false, nil }); err == nil {
				t.Fatal("unsafe viewer address accepted")
			}
			if _, err := os.Lstat(filepath.Join(home, "supervision")); !os.IsNotExist(err) {
				t.Fatalf("refusal published supervision: %v", err)
			}
		})
	}
}

func TestSupervisionPolicyComparesThePreparedAgentsViewDefinition(t *testing.T) {
	for _, rel := range []string{"LaunchAgents/agentsview.plist", "agentsview.toml"} {
		t.Run(rel, func(t *testing.T) {
			home, spec := supervisionFixture(t)
			if _, _, err := PrepareSupervision(home, spec, func(string) (bool, error) { return false, nil }); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, "supervision", filepath.FromSlash(rel))
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
			matches, err := SupervisionPolicyMatches(home, spec.SupervisionPolicy)
			if err != nil || matches {
				t.Fatalf("changed %s match = %v, err %v", rel, matches, err)
			}
		})
	}
}

func TestActivationWritesTheAgentsViewConfigurationBeforeItsBootstrap(t *testing.T) {
	home, launchAgents, release, launchd, deps, spec := activationFixture(t)
	paths := AgentsViewPathsFor(home)
	prepared, err := os.ReadFile(filepath.Join(home, "supervision", "agentsview.toml"))
	if err != nil {
		t.Fatal(err)
	}
	residentBootstrap := launchd.onBootstrap
	order := []string{}
	launchd.onBootstrap = func(label string) {
		order = append(order, label[strings.LastIndex(label, ".")+1:])
		if strings.HasSuffix(label, ".agentsview") {
			body, err := os.ReadFile(paths.Config)
			info, statErr := os.Lstat(paths.Config)
			if err != nil || statErr != nil || !bytes.Equal(body, prepared) || info.Mode().Perm() != 0o600 {
				t.Fatalf("AgentsView bootstrap preceded its configuration: %q %v %v", body, err, statErr)
			}
			for _, dir := range []string{
				paths.Root, paths.Data, paths.Home, paths.ClaudeTraceRoot, paths.ClaudeTraceCopies, paths.CodexTraceRoot,
				filepath.Join(paths.Root, "traces"), filepath.Join(paths.Root, "traces", "claude"), filepath.Join(paths.Root, "traces", "codex"),
			} {
				if err := requireOwnerOnlyDirectory(dir, "AgentsView folder"); err != nil {
					t.Fatal(err)
				}
			}
			// AgentsView rewrites its configuration at each start.
			if err := os.WriteFile(paths.Config, []byte("auth_token = \"rewritten\"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		residentBootstrap(label)
	}
	if _, _, err := ActivateSupervision(home, launchAgents, release, deps); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "agentsview,dashboard,resident" {
		t.Fatalf("start order = %v", order)
	}
	if _, _, err := ReconfigureSupervision(home, launchAgents, release, spec, deps); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "agentsview,dashboard,resident,agentsview,dashboard,resident" {
		t.Fatalf("restart order = %v", order)
	}
}

func TestActivationRefusesAMissingAgentsViewExecutable(t *testing.T) {
	home, launchAgents, release, launchd, deps, _ := activationFixture(t)
	if err := os.Remove(AgentsViewPathsFor(home).Executable); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ActivateSupervision(home, launchAgents, release, deps); err == nil ||
		!strings.Contains(err.Error(), "AgentsView executable") {
		t.Fatalf("activation without AgentsView = %v", err)
	}
	if len(launchd.loaded) != 0 || launchd.bootstraps != 0 {
		t.Fatalf("activation without AgentsView touched launchd: %v", launchd.loaded)
	}
	if _, err := os.Lstat(launchAgents); !os.IsNotExist(err) {
		t.Fatalf("activation without AgentsView installed LaunchAgents: %v", err)
	}
}

func TestInstallAgentsViewConfigRestoresTheCompleteConfiguration(t *testing.T) {
	home, spec := supervisionFixture(t)
	if _, _, err := PrepareSupervision(home, spec, func(string) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	paths := AgentsViewPathsFor(home)
	if err := InstallAgentsViewConfig(home); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Config, []byte("port = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallAgentsViewConfig(home); err != nil {
		t.Fatal(err)
	}
	prepared, _ := os.ReadFile(filepath.Join(home, "supervision", "agentsview.toml"))
	body, err := os.ReadFile(paths.Config)
	info, statErr := os.Lstat(paths.Config)
	if err != nil || statErr != nil || !bytes.Equal(body, prepared) || info.Mode().Perm() != 0o600 {
		t.Fatalf("configuration = %q mode=%v err=%v %v", body, info, err, statErr)
	}
	entries, err := os.ReadDir(paths.Data)
	if err != nil || len(entries) != 1 {
		t.Fatalf("data folder holds a leftover file: %v %v", entries, err)
	}
	if err := os.Chmod(paths.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := InstallAgentsViewConfig(home); err == nil || !strings.Contains(err.Error(), "0700") {
		t.Fatalf("a readable AgentsView folder was accepted: %v", err)
	}
}

func TestInspectAgentsViewUnitRequiresTheLoadedPreparedDefinition(t *testing.T) {
	home, launchAgents, release, launchd, deps, _ := activationFixture(t)
	if _, err := InspectAgentsViewUnit(home, launchAgents, launchd); err == nil {
		t.Fatal("an unprepared AgentsView unit passed")
	}
	_, result, err := ActivateSupervision(home, launchAgents, release, deps)
	if err != nil {
		t.Fatal(err)
	}
	label, err := InspectAgentsViewUnit(home, launchAgents, launchd)
	if err != nil || label != result.AgentsViewLabel {
		t.Fatalf("inspect = %q, %v", label, err)
	}
	installed := filepath.Join(launchAgents, label+".plist")
	body, _ := os.ReadFile(installed)
	if err := os.WriteFile(installed, append(body, ' '), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectAgentsViewUnit(home, launchAgents, launchd); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("a changed AgentsView plist passed: %v", err)
	}
	if err := os.WriteFile(installed, body, 0o644); err != nil {
		t.Fatal(err)
	}
	delete(launchd.loaded, label)
	if _, err := InspectAgentsViewUnit(home, launchAgents, launchd); err == nil || !strings.Contains(err.Error(), "not loaded") {
		t.Fatalf("an unloaded AgentsView unit passed: %v", err)
	}
}

func viewerServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server.Listener.Addr().String()
}

// fakeAgentsView answers the ping like AgentsView with require_auth: each
// request without the bearer token gets 401.
func fakeAgentsView(t *testing.T, token string, headers *[]string) string {
	t.Helper()
	return viewerServer(t, func(w http.ResponseWriter, r *http.Request) {
		*headers = append(*headers, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/ping" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"healthy":true,"service":"agentsview","version":"v0.44.0","pid":7}`)
	})
}

func TestPingAgentsViewSendsTheTokenOfTheDeployment(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	token, err := EnsureAgentsViewToken(home)
	if err != nil {
		t.Fatal(err)
	}
	var headers []string
	bind := fakeAgentsView(t, token, &headers)
	if err := PingAgentsView(ctx, home, bind); err != nil {
		t.Fatalf("healthy AgentsView = %v", err)
	}
	if len(headers) != 1 || headers[0] != "Bearer "+token {
		t.Fatalf("Authorization headers = %q", headers)
	}

	other := t.TempDir()
	if _, err := EnsureAgentsViewToken(other); err != nil {
		t.Fatal(err)
	}
	err = PingAgentsView(ctx, other, bind)
	if err == nil || err.Error() != "AgentsView at http://"+bind+" refuses the AgentsView token of this deployment." {
		t.Fatalf("a refused token = %v", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatal("the error text holds the token")
	}

	headers = nil
	missing := t.TempDir()
	if err := PingAgentsView(ctx, missing, bind); err == nil ||
		err.Error() != "The AgentsView token file "+AgentsViewPathsFor(missing).Token+" is missing." {
		t.Fatalf("a missing token file = %v", err)
	}
	if len(headers) != 0 {
		t.Fatalf("a ping without a token reached AgentsView: %q", headers)
	}
}

func TestPingAgentsViewRequiresTheAgentsViewService(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	if _, err := EnsureAgentsViewToken(home); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		handler http.HandlerFunc
		want    string
	}{
		"another service": {func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"service":"dashboard"}`) },
			"Another service answers at the AgentsView address http://"},
		"server error": {func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) },
			"answers with HTTP status 503."},
		"redirect": {func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/elsewhere", http.StatusFound) },
			"answers with HTTP status 302."},
		"not found": {http.NotFoundHandler().ServeHTTP, "answers with HTTP status 404."},
	} {
		err := PingAgentsView(ctx, home, viewerServer(t, test.handler))
		if err == nil || !strings.Contains(err.Error(), test.want) ||
			errors.Is(err, ErrAnotherViewerService) != (name == "another service") ||
			errors.Is(err, ErrViewerHTTPStatus) != (name != "another service") {
			t.Fatalf("%s = %v", name, err)
		}
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	bind := closed.Listener.Addr().String()
	closed.Close()
	if err := PingAgentsView(ctx, home, bind); err == nil || err.Error() != "AgentsView does not answer at http://"+bind+"." {
		t.Fatalf("a stopped AgentsView passed: %v", err)
	}
	if err := PingAgentsView(ctx, home, "127.0.0.1"); err == nil || err.Error() != "The AgentsView address 127.0.0.1 is invalid." {
		t.Fatalf("an invalid address = %v", err)
	}
}

func TestAnswerWikiAddressAcceptsAnyAnswerOfTheWikiServer(t *testing.T) {
	ctx := context.Background()
	wikiServer := func(status int) string {
		return viewerServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("x-culler-service", "wiki")
			if status == http.StatusFound {
				w.Header().Set("Location", "/alpha/")
			}
			w.WriteHeader(status)
		})
	}
	for _, status := range []int{http.StatusOK, http.StatusFound, http.StatusNotFound} {
		if err := AnswerWikiAddress(ctx, wikiServer(status)); err != nil {
			t.Fatalf("status %d = %v", status, err)
		}
	}
	for _, status := range []int{http.StatusForbidden, http.StatusMethodNotAllowed, http.StatusBadGateway} {
		if err := AnswerWikiAddress(ctx, wikiServer(status)); err == nil || !strings.Contains(err.Error(), fmt.Sprint(status)) {
			t.Fatalf("status %d = %v", status, err)
		}
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	bind := closed.Listener.Addr().String()
	closed.Close()
	if err := AnswerWikiAddress(ctx, bind); err == nil || !strings.Contains(err.Error(), "wiki address") {
		t.Fatalf("a stopped wiki server passed: %v", err)
	}
}

// Another process can hold the wiki address when the wiki server does not
// start. Its answers must fail the check.
func TestAnswerWikiAddressRefusesAnotherService(t *testing.T) {
	ctx := context.Background()
	for name, header := range map[string][]string{
		"no header":     nil,
		"another value": {"dashboard"},
		"two values":    {"wiki", "wiki"},
	} {
		for _, status := range []int{http.StatusOK, http.StatusNotFound} {
			bind := viewerServer(t, func(w http.ResponseWriter, r *http.Request) {
				for _, value := range header {
					w.Header().Add("x-culler-service", value)
				}
				w.WriteHeader(status)
			})
			want := "another service answers at the wiki address http://" + bind + "/"
			if err := AnswerWikiAddress(ctx, bind); err == nil || err.Error() != want || !errors.Is(err, ErrAnotherViewerService) {
				t.Fatalf("%s with status %d = %v", name, status, err)
			}
		}
	}
}

func TestAgentsViewHomeForDataInvertsTheDataFolder(t *testing.T) {
	home := "/Users/o/.culler"
	if got, ok := AgentsViewHomeForData(AgentsViewPathsFor(home).Data); !ok || got != home {
		t.Fatalf("home = %q %v", got, ok)
	}
	for _, data := range []string{"", "relative/agentsview/data", "/Users/o/.culler/data", "/Users/o/.culler/agentsview/home"} {
		if got, ok := AgentsViewHomeForData(data); ok {
			t.Fatalf("%q gave home %q", data, got)
		}
	}
}
