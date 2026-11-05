package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc/config"
	"mc/deployment"
)

const viewerCommit = "0123456789abcdef0123456789abcdef01234567"

func viewerHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if _, err := config.Ensure(filepath.Join(home, "config.toml")); err != nil {
		t.Fatal(err)
	}
	return home
}

func prepareAgentsViewUnit(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, "supervision", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agentsview.plist"), []byte("<plist/>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeViewerExport(t *testing.T, home, project, commit string) {
	t.Helper()
	folder := filepath.Join(home, "wiki", project, commit)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", "client.js", "client-lib.js", "styles.css", "graph.json"} {
		if err := os.WriteFile(filepath.Join(folder, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	record, _ := json.Marshal(map[string]string{"project": project, "commit": commit,
		"tree": strings.Repeat("e", 40), "directory": commit, "exported_at": "2026-09-29T10:00:00Z"})
	if err := os.WriteFile(filepath.Join(home, "wiki", project, "export.json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeAgentsViewHealth(t *testing.T, home string, observed time.Time, overdue []map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "health"), 0o700); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"observed_at": observed.UTC().Format(time.RFC3339Nano), "overdue": overdue})
	if err := os.WriteFile(filepath.Join(home, "health", "agentsview.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func answeringPing(calls *[]string) AgentsViewPing {
	return func(_ context.Context, _ string, bind string) error {
		*calls = append(*calls, bind)
		return nil
	}
}

func writeWikiKey(t *testing.T, home string) string {
	t.Helper()
	key, err := deployment.EnsureWikiKey(home)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestViewersInADeploymentWithoutAnAgentsViewUnit(t *testing.T) {
	home := viewerHome(t)
	key := writeWikiKey(t, home)
	writeViewerExport(t, home, "ws-alpha", viewerCommit)
	writeViewerExport(t, home, "ws-beta", viewerCommit)
	if err := os.Remove(filepath.Join(home, "wiki", "ws-beta", viewerCommit, "graph.json")); err != nil {
		t.Fatal(err)
	}
	var calls []string
	viewers, conditions := ViewerStatus(home, time.Now(), answeringPing(&calls))
	if len(calls) != 0 || len(conditions) != 0 {
		t.Fatalf("a deployment without the unit probed %v and reported %v", calls, conditions)
	}
	want := Viewers{
		AgentsView: AgentsViewViewer{URL: "http://127.0.0.1:7334", State: AgentsViewUnavailable,
			Reason: "AgentsView runs only in a production deployment."},
		Wiki: WikiViewer{URL: "http://127.0.0.1:7335", Projects: map[string]WikiExport{
			"ws-alpha": {Commit: viewerCommit, URL: "http://127.0.0.1:7335/" + key + "/ws-alpha/"},
		}},
	}
	if fmt.Sprint(viewers) != fmt.Sprint(want) {
		t.Fatalf("viewers = %+v, want %+v", viewers, want)
	}
	body, err := json.Marshal(viewers)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"agentsview":{"url":"http://127.0.0.1:7334","state":"unavailable","reason":"AgentsView runs only in a production deployment."},`+
		`"wiki":{"url":"http://127.0.0.1:7335","projects":{"ws-alpha":{"commit":"`+viewerCommit+`","url":"http://127.0.0.1:7335/`+key+`/ws-alpha/"}}}}` {
		t.Fatalf("viewers JSON = %s", body)
	}
}

func TestViewerURLsKeepTheConfiguredHost(t *testing.T) {
	home := viewerHome(t)
	if _, err := config.UpdateAtomic(filepath.Join(home, "config.toml"), func(c *config.Config) error {
		c.Surfaces.AgentsView.Bind = "[::1]:8334"
		c.Surfaces.Wiki.Bind = "localhost:8335"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	prepareAgentsViewUnit(t, home)
	var calls []string
	viewers, conditions := ViewerStatus(home, time.Now(), answeringPing(&calls))
	if viewers.AgentsView.URL != "http://[::1]:8334" || viewers.Wiki.URL != "http://localhost:8335" {
		t.Fatalf("viewer URLs = %q and %q", viewers.AgentsView.URL, viewers.Wiki.URL)
	}
	if len(calls) != 1 || calls[0] != "[::1]:8334" || viewers.AgentsView.State != AgentsViewAvailable ||
		viewers.AgentsView.Reason != "" || len(conditions) != 0 {
		t.Fatalf("probe %v gave %+v and %v", calls, viewers.AgentsView, conditions)
	}
}

func TestUnavailableAgentsViewIsAConditionWithTheRepair(t *testing.T) {
	home := viewerHome(t)
	prepareAgentsViewUnit(t, home)
	writeAgentsViewHealth(t, home, time.Now(), []map[string]string{{"kind": "run", "id": "r-1a2b"}})
	viewers, conditions := ViewerStatus(home, time.Now(), func(context.Context, string, string) error {
		return errors.New("AgentsView does not answer at http://127.0.0.1:7334.")
	})
	if viewers.AgentsView.State != AgentsViewUnavailable ||
		viewers.AgentsView.Reason != "AgentsView does not answer at http://127.0.0.1:7334." {
		t.Fatalf("AgentsView = %+v", viewers.AgentsView)
	}
	if len(conditions) != 1 {
		t.Fatalf("conditions = %+v, want only the unavailable condition", conditions)
	}
	condition := conditions[0]
	if condition.ID != "agentsview:unavailable" || condition.Group != "execution" || condition.State != "failed" ||
		condition.Name != "AgentsView" || condition.Action != "View details" ||
		condition.Condition != "AgentsView does not answer at http://127.0.0.1:7334. Trace links do not open. "+
			"Runs and Homie continue. To repair AgentsView, run install.sh again." {
		t.Fatalf("condition = %+v", condition)
	}
}

// A rerun of install.sh keeps a port that another service holds and a token
// file that Culler cannot use, so these conditions name another repair. An
// HTTP status answer can come from another service or from AgentsView, so its
// repair covers both.
func TestAgentsViewConditionNamesTheRepairOfItsFault(t *testing.T) {
	home := viewerHome(t)
	prepareAgentsViewUnit(t, home)
	serve := func(handler http.Handler) string {
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		cfg, err := config.Load(filepath.Join(home, "config.toml"))
		if err != nil {
			t.Fatal(err)
		}
		cfg.Surfaces.AgentsView.Bind = strings.TrimPrefix(server.URL, "http://")
		if _, err := config.WriteAtomic(filepath.Join(home, "config.toml"), cfg); err != nil {
			t.Fatal(err)
		}
		return server.URL
	}
	if _, err := deployment.EnsureAgentsViewToken(home); err != nil {
		t.Fatal(err)
	}
	other := serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"service":"dashboard"}`))
	}))
	_, conditions := ViewerStatus(home, time.Now(), deployment.PingAgentsView)
	want := "Another service answers at the AgentsView address " + other + ". Trace links do not open. " +
		"Runs and Homie continue. Stop the other service, or ask your coding harness to set another AgentsView port " +
		"in config.toml. Then run install.sh again."
	if len(conditions) != 1 || conditions[0].Condition != want {
		t.Fatalf("conditions = %+v, want %q", conditions, want)
	}
	plain := serve(http.NotFoundHandler())
	_, conditions = ViewerStatus(home, time.Now(), deployment.PingAgentsView)
	want = "The service at " + plain + " answers with HTTP status 404. Trace links do not open. " +
		"Runs and Homie continue. If another service uses this port, stop that service, or ask your coding harness " +
		"to set another AgentsView port in config.toml. Then run install.sh again."
	if len(conditions) != 1 || conditions[0].Condition != want {
		t.Fatalf("conditions = %+v, want %q", conditions, want)
	}
	token := deployment.AgentsViewPathsFor(home).Token
	if err := os.Chmod(token, 0o644); err != nil {
		t.Fatal(err)
	}
	_, conditions = ViewerStatus(home, time.Now(), deployment.PingAgentsView)
	want = "The AgentsView token file " + token + " must be a regular file with mode 0600 that the operator owns. " +
		"Trace links do not open. Runs and Homie continue. " +
		"Ask your coding harness to remove the AgentsView token file, then run install.sh again."
	if len(conditions) != 1 || conditions[0].Condition != want {
		t.Fatalf("conditions = %+v, want %q", conditions, want)
	}
}

func TestOverdueTraceImportsAreOneCondition(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	home := viewerHome(t)
	prepareAgentsViewUnit(t, home)
	var calls []string
	ping := answeringPing(&calls)

	writeAgentsViewHealth(t, home, now.Add(-time.Minute), []map[string]string{
		{"kind": "run", "id": "r-1a2b", "agentsview_session": "s-1", "changed_at": "2026-09-29T11:50:00Z"},
		{"kind": "homie", "id": "h-9f8e", "agentsview_session": "s-2", "changed_at": "2026-09-29T11:51:00Z"},
		{"kind": "run", "id": "r-1a2b", "agentsview_session": "s-1", "changed_at": "2026-09-29T11:50:00Z"},
		{"kind": "task", "id": "r-other"},
		{"kind": "run", "id": "../r-other"},
	})
	viewers, conditions := ViewerStatus(home, now, ping)
	if viewers.AgentsView.State != AgentsViewAvailable || len(conditions) != 1 {
		t.Fatalf("AgentsView = %+v, conditions = %+v", viewers.AgentsView, conditions)
	}
	if conditions[0].ID != "agentsview:overdue" || conditions[0].Name != "AgentsView" ||
		conditions[0].Condition != "AgentsView did not import new trace lines of 2 sessions within 5 minutes: "+
			"run r-1a2b, Homie session h-9f8e. Their trace links can show an incomplete trace. "+
			"Culler continues to copy the traces." {
		t.Fatalf("overdue condition = %+v", conditions[0])
	}

	many := []map[string]string{}
	for index := range 12 {
		many = append(many, map[string]string{"kind": "run", "id": fmt.Sprintf("r-%02d", index)})
	}
	writeAgentsViewHealth(t, home, now, many)
	if _, conditions := ViewerStatus(home, now, ping); len(conditions) != 1 ||
		!strings.Contains(conditions[0].Condition, "of 12 sessions within 5 minutes: run r-00, ") ||
		!strings.Contains(conditions[0].Condition, "run r-09, and 2 more.") {
		t.Fatalf("a long list = %+v", conditions)
	}

	for name, write := range map[string]func(){
		"stale file": func() {
			writeAgentsViewHealth(t, home, now.Add(-3*time.Minute), []map[string]string{{"kind": "run", "id": "r-1a2b"}})
		},
		"future file": func() {
			writeAgentsViewHealth(t, home, now.Add(time.Minute), []map[string]string{{"kind": "run", "id": "r-1a2b"}})
		},
		"no overdue copy": func() { writeAgentsViewHealth(t, home, now, []map[string]string{}) },
		"malformed file": func() {
			if err := os.WriteFile(filepath.Join(home, "health", "agentsview.json"), []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"absent file": func() {
			if err := os.Remove(filepath.Join(home, "health", "agentsview.json")); err != nil {
				t.Fatal(err)
			}
		},
	} {
		write()
		if _, conditions := ViewerStatus(home, now, ping); len(conditions) != 0 {
			t.Fatalf("%s gave conditions %+v", name, conditions)
		}
		writeAgentsViewHealth(t, home, now, nil)
	}
}

func TestViewersWithoutAReadableConfiguration(t *testing.T) {
	home := t.TempDir()
	var calls []string
	viewers, conditions := ViewerStatus(home, time.Now(), answeringPing(&calls))
	if len(calls) != 0 || len(conditions) != 0 || viewers.AgentsView.State != AgentsViewUnavailable ||
		viewers.AgentsView.Reason != "Culler cannot read the AgentsView address from config.toml." ||
		viewers.Wiki.Projects == nil {
		t.Fatalf("viewers = %+v, conditions = %v, probes = %v", viewers, conditions, calls)
	}
}

// Each export address holds the secret path prefix. A deployment without the
// key gives each export no address, so the dashboard shows no working link.
func TestWikiExportAddressesHoldTheSecretPathPrefix(t *testing.T) {
	home := viewerHome(t)
	writeViewerExport(t, home, "ws-alpha", viewerCommit)
	var calls []string
	viewers, _ := ViewerStatus(home, time.Now(), answeringPing(&calls))
	if export := viewers.Wiki.Projects["ws-alpha"]; export.Commit != viewerCommit || export.URL != "" {
		t.Fatalf("an export without a wiki key = %+v", export)
	}
	key := writeWikiKey(t, home)
	viewers, _ = ViewerStatus(home, time.Now(), answeringPing(&calls))
	if export := viewers.Wiki.Projects["ws-alpha"]; export.URL != "http://127.0.0.1:7335/"+key+"/ws-alpha/" {
		t.Fatalf("an export with a wiki key = %+v", export)
	}
	if viewers.Wiki.URL != "http://127.0.0.1:7335" {
		t.Fatalf("the wiki origin = %q", viewers.Wiki.URL)
	}
}

// The status sends the token file of the deployment through the one ping,
// and the status response never holds the token.
func TestViewerStatusSendsTheTokenAndReportsARefusedToken(t *testing.T) {
	home := viewerHome(t)
	prepareAgentsViewUnit(t, home)
	token, err := deployment.EnsureAgentsViewToken(home)
	if err != nil {
		t.Fatal(err)
	}
	accepted := token
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+accepted {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"service":"agentsview","pid":42}`))
	}))
	defer server.Close()
	if _, err := config.UpdateAtomic(filepath.Join(home, "config.toml"), func(c *config.Config) error {
		c.Surfaces.AgentsView.Bind = strings.TrimPrefix(server.URL, "http://")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	viewers, conditions := ViewerStatus(home, time.Now(), deployment.PingAgentsView)
	if viewers.AgentsView.State != AgentsViewAvailable || len(conditions) != 0 {
		t.Fatalf("AgentsView with the token = %+v, %v", viewers.AgentsView, conditions)
	}
	accepted = strings.Repeat("0", 64)
	viewers, conditions = ViewerStatus(home, time.Now(), deployment.PingAgentsView)
	want := "AgentsView at " + server.URL + " refuses the AgentsView token of this deployment."
	if viewers.AgentsView.State != AgentsViewUnavailable || viewers.AgentsView.Reason != want ||
		len(conditions) != 1 || !strings.HasPrefix(conditions[0].Condition, want+" Trace links do not open.") {
		t.Fatalf("AgentsView with a refused token = %+v, %v", viewers.AgentsView, conditions)
	}
	body, _ := json.Marshal(map[string]any{"viewers": viewers, "conditions": conditions})
	if strings.Contains(string(body), token) {
		t.Fatalf("the status holds the token: %s", body)
	}
}

func TestAgentsViewAccessGivesTheCredentialsOrTheRepair(t *testing.T) {
	home := viewerHome(t)
	path := filepath.Join(home, "agentsview", "token")
	if _, err := AgentsViewAccess(home); err == nil ||
		err.Error() != "The AgentsView token file "+path+" is missing. To repair AgentsView, run install.sh again." {
		t.Fatalf("a missing token = %v", err)
	} else if _, domain := err.(*DomainError); !domain {
		t.Fatalf("a missing token is not a domain rejection: %T", err)
	}
	token, err := deployment.EnsureAgentsViewToken(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := AgentsViewAccess(home); err == nil || err.Error() != "The AgentsView token file "+path+
		" must hold 64 lowercase hexadecimal characters. Ask your coding harness to remove the AgentsView token file, then run install.sh again." {
		t.Fatalf("an unusable token = %v", err)
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := AgentsViewAccess(home)
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := json.Marshal(result); string(body) != `{"token":"`+token+`","url":"http://127.0.0.1:7334"}` {
		t.Fatalf("result = %s", body)
	}
}
