//go:build !test_fake_routing

package main

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	operatorconfig "mc/config"
	"mc/deployment"
	"mc/verbs"
)

func TestBoundedDoctorBufferRetainsPrefixWithoutBlockingChild(t *testing.T) {
	b := boundedDoctorBuffer{limit: 8}
	payload := strings.Repeat("x", 32)
	n, err := b.Write([]byte(payload))
	if err != nil || n != len(payload) {
		t.Fatalf("write = %d, %v", n, err)
	}
	if b.String() != strings.Repeat("x", 8) || !b.overflow {
		t.Fatalf("buffer = %q overflow=%v", b.String(), b.overflow)
	}
}

// The 2026-08-29 live redeploy failed at its doctor step: the production
// supervision finding carried OnboardSection "supervision" while
// ComposeDoctor requires "" for that check, so a healthy deployment
// reported "invalid composed doctor finding". This composes the real
// production finding through the real validator, so the two literals
// cannot drift apart again.
func TestProductionSupervisionFindingComposes(t *testing.T) {
	supervision := newSupervisionFinding()
	supervision.Status = "ok"
	supervision.Detail = "units loaded"
	host := []verbs.DoctorFinding{
		{Check: "mc-home", Status: "ok", OnboardSection: "home"},
		{Check: "routing", Status: "ok", OnboardSection: "routing"},
		{Check: "runtime-auth", Status: "ok", OnboardSection: "runtime-auth"},
		supervision,
	}
	home := t.TempDir()
	if _, err := operatorconfig.WriteAtomic(filepath.Join(home, "config.toml"), operatorconfig.Default()); err != nil {
		t.Fatal(err)
	}
	userHome := t.TempDir()
	probes := productionViewerProbes(userHome, nil, &doctorLaunchd{})
	host = append(host, verbs.ViewerDoctorFindings(home, nil, probes)...)
	runtime := verbs.DoctorRuntimeReport{
		SpineUUID: "u",
		Findings: []verbs.DoctorFinding{
			{Check: "spine", Status: "ok", OnboardSection: "home"},
			{Check: "projects", Status: "ok", OnboardSection: "project"},
			{Check: "surfaces", Status: "ok", OnboardSection: "surfaces"},
			{Check: "container-runtime", Status: "ok"},
		},
	}
	if _, err := verbs.ComposeDoctor(host, runtime); err != nil {
		t.Fatalf("production supervision finding does not compose: %v", err)
	}
}

type doctorLaunchd struct{}

func (*doctorLaunchd) Loaded(string) (bool, error) { return false, nil }
func (*doctorLaunchd) Bootstrap(string) error      { return nil }
func (*doctorLaunchd) Bootout(string) error        { return nil }

// A stopped AgentsView and a closed wiki address fail with the front-door
// repair and do not block the other checks.
func TestProductionViewerProbesReportStoppedViewers(t *testing.T) {
	home, err := deployment.CanonicalHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := operatorconfig.Default()
	cfg.Surfaces.AgentsView.Bind = closedLoopbackBind(t)
	cfg.Surfaces.Wiki.Bind = closedLoopbackBind(t)
	if _, err := operatorconfig.WriteAtomic(filepath.Join(home, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	findings := verbs.ViewerDoctorFindings(home, nil, productionViewerProbes(t.TempDir(), nil, &doctorLaunchd{}))
	for i, want := range []string{"prepared supervision is invalid", "did not answer at the wiki address"} {
		if findings[i].Status != "fail" || !strings.Contains(findings[i].Detail, want) || !strings.Contains(findings[i].Detail, "install.sh") {
			t.Fatalf("finding %d = %#v", i, findings[i])
		}
	}
	findings = verbs.ViewerDoctorFindings(home, nil, productionViewerProbes("", errors.New("no operator home"), &doctorLaunchd{}))
	if !strings.Contains(findings[0].Detail, "operator home unresolved") {
		t.Fatalf("finding = %#v", findings[0])
	}
}

// The doctor ping sends the AgentsView token of the deployment.
func TestProductionViewerProbeSendsTheAgentsViewToken(t *testing.T) {
	home := t.TempDir()
	token, err := deployment.EnsureAgentsViewToken(home)
	if err != nil {
		t.Fatal(err)
	}
	var headers []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = append(headers, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"service":"agentsview","pid":7}`))
	}))
	defer server.Close()
	bind := strings.TrimPrefix(server.URL, "http://")
	probes := productionViewerProbes(t.TempDir(), nil, &doctorLaunchd{})
	if err := probes.PingAgentsView(home, bind); err != nil {
		t.Fatalf("the doctor ping with the token = %v", err)
	}
	if err := probes.PingAgentsView(t.TempDir(), bind); err == nil || !strings.Contains(err.Error(), "AgentsView token file") {
		t.Fatalf("the doctor ping without a token = %v", err)
	}
	if len(headers) != 1 || headers[0] != "Bearer "+token {
		t.Fatalf("Authorization headers = %q", headers)
	}
}

func closedLoopbackBind(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	bind := listener.Addr().String()
	listener.Close()
	return bind
}

func TestDoctorFindingsPreserveChecksAfterSystemComposition(t *testing.T) {
	t.Setenv("MC_HOME", t.TempDir())
	want := verbs.DoctorFinding{Check: "spine", Status: "fail", Detail: "The database requires preparation.", OnboardSection: "home"}
	report := map[string]any{"ok": false, "findings": []verbs.DoctorFinding{want}}
	for _, result := range []any{report, composeSystemDoctor(report, "fixture-start")} {
		findings, err := doctorFindings(result)
		if err != nil || len(findings) != 1 || !reflect.DeepEqual(findings[0], want) {
			t.Fatalf("checks = %#v: %v", findings, err)
		}
	}
}

func TestDoctorFindingsRejectIncompleteReports(t *testing.T) {
	for _, report := range []any{nil, map[string]any{}, map[string]any{"findings": []any{}}, map[string]any{"findings": "invalid"}} {
		if _, err := doctorFindings(report); err == nil {
			t.Fatalf("an incomplete report passed: %#v", report)
		}
	}
}
