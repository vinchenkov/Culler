package verbs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	operatorconfig "mc/config"
	"mc/deployment"
)

func viewerDoctorHome(t *testing.T, mutate func(*operatorconfig.Config)) string {
	t.Helper()
	home := t.TempDir()
	cfg := operatorconfig.Default()
	if mutate != nil {
		mutate(&cfg)
	}
	if _, err := operatorconfig.WriteAtomic(filepath.Join(home, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestViewerDoctorFindingsProbeTheConfiguredAddresses(t *testing.T) {
	home := viewerDoctorHome(t, func(cfg *operatorconfig.Config) {
		cfg.Surfaces.AgentsView.Bind = "localhost:7444"
		cfg.Surfaces.Wiki.Bind = "[::1]:7445"
	})
	var pinged, pingedHome, answered, inspected string
	findings := ViewerDoctorFindings(home, nil, ViewerDoctorProbes{
		InspectAgentsView: func(got string) (string, error) { inspected = got; return "com.culler.0123456789ab.agentsview", nil },
		PingAgentsView:    func(pingHome, bind string) error { pingedHome, pinged = pingHome, bind; return nil },
		AnswerWiki:        func(bind string) error { answered = bind; return nil },
	})
	if inspected != home || pingedHome != home || pinged != "localhost:7444" || answered != "[::1]:7445" {
		t.Fatalf("probes: inspect=%q ping=%q %q wiki=%q", inspected, pingedHome, pinged, answered)
	}
	if len(findings) != 2 || findings[0].Check != "agentsview" || findings[1].Check != "wiki" {
		t.Fatalf("findings = %#v", findings)
	}
	for _, finding := range findings {
		if finding.Status != "ok" || finding.OnboardSection != "" {
			t.Fatalf("healthy viewer finding = %#v", finding)
		}
	}
	if !strings.Contains(findings[0].Detail, "com.culler.0123456789ab.agentsview") ||
		!strings.Contains(findings[0].Detail, "http://localhost:7444") ||
		!strings.Contains(findings[1].Detail, "http://[::1]:7445/") {
		t.Fatalf("details = %q / %q", findings[0].Detail, findings[1].Detail)
	}
}

func TestViewerDoctorFindingsNameTheFailedConditionAndRepair(t *testing.T) {
	home := viewerDoctorHome(t, nil)
	pingCalled := false
	findings := ViewerDoctorFindings(home, nil, ViewerDoctorProbes{
		InspectAgentsView: func(string) (string, error) {
			return "", errors.New("LaunchAgent com.culler.x.agentsview is not loaded")
		},
		PingAgentsView: func(string, string) error { pingCalled = true; return nil },
		AnswerWiki:     func(string) error { return errors.New("the dashboard did not answer at the wiki address") },
	})
	if pingCalled {
		t.Fatal("the ping ran for an unloaded unit")
	}
	for i, want := range []string{"is not loaded", "did not answer at the wiki address"} {
		if findings[i].Status != "fail" || !strings.Contains(findings[i].Detail, want) ||
			!strings.HasSuffix(findings[i].Detail, frontDoorRepairDetail) {
			t.Fatalf("finding %d = %#v", i, findings[i])
		}
	}
	findings = ViewerDoctorFindings(home, nil, ViewerDoctorProbes{
		InspectAgentsView: func(string) (string, error) { return "label", nil },
		PingAgentsView: func(string, string) error {
			return errors.New("AgentsView at http://127.0.0.1:7334 refuses the AgentsView token of this deployment.")
		},
		AnswerWiki: func(string) error { return nil },
	})
	if findings[0].Status != "fail" || findings[1].Status != "ok" || findings[0].Detail !=
		"AgentsView at http://127.0.0.1:7334 refuses the AgentsView token of this deployment"+frontDoorRepairDetail {
		t.Fatalf("findings = %#v", findings)
	}
}

// A rerun of install.sh keeps a port that another service holds and a token
// file that Culler cannot use, so these findings name another repair. An HTTP
// status answer at the AgentsView address can come from another service or
// from AgentsView, so its repair covers both.
func TestViewerDoctorFindingsNameTheRepairOfEachFault(t *testing.T) {
	home := viewerDoctorHome(t, nil)
	token := deployment.AgentsViewPathsFor(home).Token
	other := func(text string) error { return fmt.Errorf("%w: %s", deployment.ErrAnotherViewerService, text) }
	for name, test := range map[string]struct {
		ping, wiki          error
		agentsView, address string
	}{
		"another service": {
			ping:       other("Another service answers at the AgentsView address http://127.0.0.1:7334."),
			wiki:       other("another service answers at the wiki address http://127.0.0.1:7335/"),
			agentsView: "; repair: stop that service, or set a free port in surfaces.agentsview.bind of config.toml, then run install.sh",
			address:    "; repair: stop that service, or set a free port in surfaces.wiki.bind of config.toml, then run install.sh",
		},
		"unusable token": {
			ping:       fmt.Errorf("%w: The AgentsView token file %s must hold 64 lowercase hexadecimal characters.", deployment.ErrUnusableViewerSecret, token),
			wiki:       errors.New("the wiki address http://127.0.0.1:7335/ answered with HTTP status 500"),
			agentsView: "; repair: remove the AgentsView token file, then run install.sh",
			address:    frontDoorRepairDetail,
		},
		"HTTP status": {
			ping: fmt.Errorf("%w: The service at http://127.0.0.1:7334 answers with HTTP status 404.", deployment.ErrViewerHTTPStatus),
			wiki: errors.New("the wiki address http://127.0.0.1:7335/ answered with HTTP status 500"),
			agentsView: "; repair: if another service uses this port, stop that service or set a free port in " +
				"surfaces.agentsview.bind of config.toml, then run install.sh",
			address: frontDoorRepairDetail,
		},
	} {
		findings := ViewerDoctorFindings(home, nil, ViewerDoctorProbes{
			InspectAgentsView: func(string) (string, error) { return "label", nil },
			PingAgentsView:    func(string, string) error { return test.ping },
			AnswerWiki:        func(string) error { return test.wiki },
		})
		if findings[0].Detail != strings.TrimSuffix(test.ping.Error(), ".")+test.agentsView ||
			findings[1].Detail != test.wiki.Error()+test.address {
			t.Fatalf("%s: findings = %#v", name, findings)
		}
	}
}

func TestViewerDoctorFindingsReportUncheckedWithoutHomeOrConfig(t *testing.T) {
	probes := ViewerDoctorProbes{
		InspectAgentsView: func(string) (string, error) { t.Fatal("inspected without configuration"); return "", nil },
		PingAgentsView:    func(string, string) error { t.Fatal("pinged without configuration"); return nil },
		AnswerWiki:        func(string) error { t.Fatal("probed without configuration"); return nil },
	}
	for _, findings := range [][]DoctorFinding{
		ViewerDoctorFindings("", errors.New("no home"), probes),
		ViewerDoctorFindings(t.TempDir(), nil, probes),
	} {
		for _, finding := range findings {
			if finding.Status != "fail" || !strings.HasPrefix(finding.Detail, "not checked: ") {
				t.Fatalf("finding = %#v", finding)
			}
		}
	}
}

// A host without loaded launchd units defers both viewer checks with the
// supervision reason. The checks do not fail.
func TestLocalDoctorDefersTheViewerChecksWithTheSupervisionReason(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_HOME", home)
	findings, err := DoctorHostFacts()
	if err != nil {
		t.Fatal(err)
	}
	details := map[string]DoctorFinding{}
	for _, finding := range findings {
		details[finding.Check] = finding
	}
	for _, check := range []string{"agentsview", "wiki"} {
		finding := details[check]
		if finding.Status != "deferred" || finding.Detail != details["supervision"].Detail || finding.OnboardSection != "" {
			t.Fatalf("%s = %#v", check, finding)
		}
	}
}
