package verbs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mc/substrate"
)

func doctorTestRequest() DoctorRuntimeRequest {
	return DoctorRuntimeRequest{
		ProtocolVersion: 1, ReleaseBuildID: "development", ControlVersion: 1,
		SpineSchemaID: substrate.CurrentSchemaID, ConfigSchemaVersion: 1,
	}
}

func doctorHostFixture() []DoctorFinding {
	return []DoctorFinding{
		{Check: "mc-home", Status: "ok", Detail: "home", OnboardSection: "home"},
		{Check: "routing", Status: "ok", Detail: "routing", OnboardSection: "routing"},
		{Check: "runtime-auth", Status: "deferred", Detail: "auth", OnboardSection: "runtime-auth"},
		{Check: "supervision", Status: "deferred", Detail: "supervision", OnboardSection: ""},
		{Check: "agentsview", Status: "ok", Detail: "agentsview", OnboardSection: ""},
		{Check: "wiki", Status: "ok", Detail: "wiki", OnboardSection: ""},
	}
}

func doctorRuntimeFixture(uuid string) DoctorRuntimeReport {
	report := DoctorRuntimeUnavailable(doctorTestRequest(), "fixture")
	report.SpineUUID = uuid
	for i := range report.Findings {
		report.Findings[i].Status = "ok"
		report.Findings[i].Detail = "ok"
	}
	return report
}

func TestComposeDoctorKeepsAuthorityOrderAndComparesOnlyUUIDMirror(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MC_HOME", home)
	uuid := strings.Repeat("a", 32)
	if err := os.WriteFile(filepath.Join(home, deploymentUUIDFilename), []byte(uuid+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := ComposeDoctor(doctorHostFixture(), doctorRuntimeFixture(uuid))
	if err != nil {
		t.Fatal(err)
	}
	report := res.(map[string]any)
	findings := report["findings"].([]DoctorFinding)
	want := []string{"mc-home", "spine", "deployment-identity", "projects", "surfaces", "routing", "container-runtime", "runtime-auth", "supervision", "agentsview", "wiki"}
	if len(findings) != len(want) {
		t.Fatalf("findings = %#v", findings)
	}
	for i, finding := range findings {
		if finding.Check != want[i] {
			t.Fatalf("finding %d = %q, want %q", i, finding.Check, want[i])
		}
	}
	if findings[2].Status != "ok" || report["ok"] != true {
		t.Fatalf("composed report = %#v", report)
	}

	mismatch := doctorRuntimeFixture(strings.Repeat("b", 32))
	res, err = ComposeDoctor(doctorHostFixture(), mismatch)
	if err != nil {
		t.Fatal(err)
	}
	findings = res.(map[string]any)["findings"].([]DoctorFinding)
	if findings[2].Status != "fail" || !strings.Contains(findings[2].Detail, "does not match") {
		t.Fatalf("mismatch identity = %#v", findings[2])
	}
}

func TestComposeDoctorRejectsOpenEndedHelperFindings(t *testing.T) {
	t.Setenv("MC_HOME", t.TempDir())
	uuid := strings.Repeat("a", 32)
	base := doctorRuntimeFixture(uuid)
	bad := base
	bad.Findings = append([]DoctorFinding{}, base.Findings...)
	bad.Findings[0].Check = "invented"
	if _, err := ComposeDoctor(doctorHostFixture(), bad); err == nil {
		t.Fatal("unknown helper finding was accepted")
	}
	bad = base
	bad.Findings = append([]DoctorFinding{}, base.Findings...)
	bad.Findings[0].Status = "maybe"
	if _, err := ComposeDoctor(doctorHostFixture(), bad); err == nil {
		t.Fatal("invalid helper status was accepted")
	}
	bad = base
	bad.Findings = bad.Findings[:3]
	if _, err := ComposeDoctor(doctorHostFixture(), bad); err == nil {
		t.Fatal("missing helper finding was accepted")
	}
	for _, check := range []string{"agentsview", "wiki"} {
		host := []DoctorFinding{}
		for _, finding := range doctorHostFixture() {
			if finding.Check != check {
				host = append(host, finding)
			}
		}
		if _, err := ComposeDoctor(host, base); err == nil {
			t.Fatalf("a host half without %s was accepted", check)
		}
		host = doctorHostFixture()
		for i := range host {
			if host[i].Check == check {
				host[i].OnboardSection = "surfaces"
			}
		}
		if _, err := ComposeDoctor(host, base); err == nil {
			t.Fatalf("%s with a setup section was accepted", check)
		}
	}
	failing := doctorHostFixture()
	failing[4].Status = "fail"
	res, err := ComposeDoctor(failing, base)
	if err != nil || res.(map[string]any)["ok"] != false {
		t.Fatalf("a failing AgentsView check composed as healthy: %v %v", res, err)
	}
}

func TestDoctorRuntimeFrameCarriesNoHostFact(t *testing.T) {
	b, err := json.Marshal(doctorTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"home", "path", "routing", "mirror", "credential", "project"} {
		if strings.Contains(strings.ToLower(string(b)), forbidden) {
			t.Fatalf("runtime doctor frame carries %q: %s", forbidden, b)
		}
	}
}

// A full `mc doctor` covers every check above, and each failure names the
// onboarding section that repairs it. `mc doctor` reported nine `ok`
// findings on a deployment whose Worker refused every tick with
// mount.allowlist_untrusted, because no check ever looked at the file. A
// health check that cannot see the deployment's most load-bearing missing
// inode is worse than no check: it certifies the broken state.
//
// The check folds into the existing host-side `mc-home` finding rather than
// adding another: the allowlist is an MC_HOME inode, and ComposeDoctor pins
// the host half at six findings and the helper half at four.
func TestDoctorFailsWhenTheMountAllowlistIsMissingOrUntrusted(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	spine := filepath.Join(home, "spine.db")
	t.Setenv("MC_HOME", home)
	if _, _, err := onboardHome(spine, true); err != nil {
		t.Fatalf("onboard home: %v", err)
	}

	mcHomeFinding := func(t *testing.T) DoctorFinding {
		t.Helper()
		findings, err := DoctorHostFacts()
		if err != nil {
			t.Fatal(err)
		}
		if len(findings) != 6 {
			t.Fatalf("host doctor returned %d findings; ComposeDoctor requires exactly 6", len(findings))
		}
		for _, finding := range findings {
			if finding.Check == "mc-home" {
				return finding
			}
		}
		t.Fatal("host doctor has no mc-home finding")
		return DoctorFinding{}
	}

	// The deployment onboarding just built is healthy. Without this arm the
	// failure arms below would pass on a check that always fails.
	if finding := mcHomeFinding(t); finding.Status != "ok" {
		t.Fatalf("doctor failed the deployment onboarding just built: %+v", finding)
	}

	path := MountAllowlistPath(home)
	for _, tc := range []struct {
		name string
		// want distinguishes the three faults. Without it the arms collapse:
		// TrustPolicyFile wraps its lstat in a *boundary.MountError, so a
		// missing file once reported as "untrusted" — telling the operator to
		// inspect permissions on a file that is not there.
		want   string
		break_ func(t *testing.T)
	}{
		{"missing", "no mount-allowlist at", func(t *testing.T) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}},
		{"group-readable", "is untrusted", func(t *testing.T) {
			if err := os.Chmod(path, 0o640); err != nil {
				t.Fatal(err)
			}
		}},
		{"unparsable", "does not parse", func(t *testing.T) {
			if err := os.WriteFile(path, []byte("version = 2\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(func() {
				// Chmod as well as write: os.WriteFile leaves an existing
				// file's mode alone, so restoring bytes after the
				// group-readable arm would leave 0640 behind and the next arm
				// would fail on the mode instead of the fault it names.
				if err := os.WriteFile(path, []byte("version = 1\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0o600); err != nil {
					t.Fatal(err)
				}
			})
			tc.break_(t)
			finding := mcHomeFinding(t)
			if finding.Status != "fail" {
				t.Fatalf("doctor passed a deployment whose allowlist is %s: %+v", tc.name, finding)
			}
			if !strings.Contains(finding.Detail, "mount-allowlist") {
				t.Fatalf("the failure does not name the file the operator must repair: %+v", finding)
			}
			if !strings.Contains(finding.Detail, tc.want) {
				t.Fatalf("a %s allowlist reported the wrong fault (want %q): %+v",
					tc.name, tc.want, finding)
			}
			if finding.OnboardSection != "home" {
				t.Fatalf("failure names section %q; `mc onboard home` is what creates the file",
					finding.OnboardSection)
			}
		})
	}
}
