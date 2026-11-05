//go:build docker_boundary

package boundarydocker

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// The container-runtime capability probe. Doctor accepts a runtime by
// capability, never by runtime identity: doctor proves the helper exec
// round-trip when it runs at all, then probes suid honored (ruid≠euid),
// NoNewPrivs clear, identity uid_map, the kernel's EACCES on a direct spine
// open at the agent's real uid beside a successful gated read, and native
// arm64.
//
// The spine lives on a NAMED VOLUME exactly like production: VirtioFS bind
// mounts do not enforce uid ownership, so a bind-mounted fixture would let
// the direct-open leg fail the wrong way.
// ---------------------------------------------------------------------------

func doctorFindings(t *testing.T, out string) []map[string]any {
	t.Helper()
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("no JSON in doctor output:\n%s", out)
	}
	var doc struct {
		Findings []map[string]any `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out[start:]), &doc); err != nil {
		t.Fatalf("unparseable doctor output (%v):\n%s", err, out)
	}
	return doc.Findings
}

func findingByName(t *testing.T, findings []map[string]any, check string) map[string]any {
	t.Helper()
	for _, f := range findings {
		if f["check"] == check {
			return f
		}
	}
	t.Fatalf("no %q finding in %v", check, findings)
	return nil
}

func TestDoctorContainerRuntimeCapabilityProbeDockerBoundary(t *testing.T) {
	requireProdImage(t)

	const volume = "mc-doctor-probe-spine"
	exec.Command("docker", "volume", "rm", "-f", volume).Run()
	if out, err := exec.Command("docker", "volume", "create", volume).CombinedOutput(); err != nil {
		t.Fatalf("create probe volume: %v (%s)", err, out)
	}
	t.Cleanup(func() { exec.Command("docker", "volume", "rm", "-f", volume).Run() })

	// Stage the spine shape the gate protects: dir 0700 and file 0600, both
	// owned by mc's owning uid — the same shape onboarding provisions.
	if out, err := dockerRun(t, "-u", "0:0", "-v", volume+":/mc/spine", prodImage,
		"sh", "-ec", "touch /mc/spine/spine.db && chown -R 10001:10001 /mc/spine && chmod 0700 /mc/spine && chmod 0600 /mc/spine/spine.db"); err != nil {
		t.Fatalf("stage spine volume: %v\n%s", err, out)
	}
	home := sharedTempDir(t)

	// Mirror the production helper crossing: the long-lived helper runs as the
	// model uid with no ambient container capabilities, then ordinary `mc`
	// verbs enter through the image's setuid gate.
	probe := func(extra ...string) []map[string]any {
		t.Helper()
		args := []string{"--user", "10002:10002", "--cap-drop", "ALL"}
		args = append(args, extra...)
		args = append(args,
			"-v", volume+":/mc/spine",
			"-v", home+":/mc/home:ro",
			"-e", "MC_HOME=/mc/home",
			prodImage, "mc", "doctor")
		out, _ := dockerRun(t, args...)
		return doctorFindings(t, out)
	}

	t.Run("the capability probe passes inside a conforming envelope", func(t *testing.T) {
		finding := findingByName(t, probe(), "container-runtime")
		if finding["status"] != "ok" {
			t.Fatalf("container-runtime = %v, want ok", finding)
		}
		detail, _ := finding["detail"].(string)
		for _, leg := range []string{"agent uid 10002 direct spine open EACCES", "suid honored", "NoNewPrivs=0", "identity uid_map", "native arm64"} {
			if !strings.Contains(detail, leg) {
				t.Errorf("probe detail lacks the %q leg: %q", leg, detail)
			}
		}
	})

	t.Run("a forced no-new-privileges envelope fails the probe closed", func(t *testing.T) {
		finding := findingByName(t, probe("--security-opt", "no-new-privileges=true"), "container-runtime")
		if finding["status"] != "fail" {
			t.Fatalf("container-runtime under forced NNP = %v, want fail", finding)
		}
		detail, _ := finding["detail"].(string)
		if !strings.Contains(detail, "no-new-privileges") {
			t.Errorf("failure does not name the forced flag: %q", detail)
		}
	})
}
