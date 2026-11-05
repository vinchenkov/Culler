//go:build docker_boundary

package boundarydocker

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

type attachmentCanaryReceipt struct {
	Version             int      `json:"version"`
	Generation          string   `json:"generation"`
	InboundSHA256       string   `json:"inbound_sha256"`
	OutboundSHA256      string   `json:"outbound_sha256"`
	RealUID             int      `json:"real_uid"`
	EffectiveUID        int      `json:"effective_uid"`
	SavedUID            int      `json:"saved_uid"`
	FSUID               int      `json:"fs_uid"`
	WarmInboundRead     bool     `json:"warm_inbound_read"`
	HostOutboundRead    bool     `json:"host_outbound_read"`
	PrivateGateHealthy  bool     `json:"private_gate_healthy"`
	DeniedOutbound      []string `json:"denied_outbound"`
	RestartInboundRead  bool     `json:"restart_inbound_read"`
	RestartOutboundRead bool     `json:"restart_outbound_read"`
	OutsideSessions     bool     `json:"outside_sessions"`
}

func buildHostMC(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "mc")
	cmd := exec.Command("mise", "exec", "--", "go", "build", "-ldflags",
		"-X main.attachmentCanaryImage="+prodImage, "-o", bin, "./cmd/mc")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build host mc: %v\n%s", err, out)
	}
	return bin
}

// The mandatory composed proof. This deliberately invokes the host command
// rather than reproducing its Docker argv in the test: green means the exact
// boot gate can observe both already-live mounts, kernel denial, and restart
// persistence on the configured MC_HOME share.
func TestAttachmentCanaryWarmComposedBoundaryDockerBoundary(t *testing.T) {
	requireProdImage(t)
	home := sharedTempDir(t)
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(buildHostMC(t), "__attachment-canary")
	cmd.Env = append(os.Environ(), "MC_HOME="+home)
	out, err := cmd.Output()
	if err != nil {
		diagnostic := ""
		if exit, ok := err.(*exec.ExitError); ok {
			diagnostic = string(exit.Stderr)
		}
		t.Fatalf("mandatory attachment canary: %v\n%s", err, diagnostic)
	}
	var receipt attachmentCanaryReceipt
	if err := json.Unmarshal(out, &receipt); err != nil {
		t.Fatalf("attachment canary receipt: %v\n%s", err, out)
	}
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	if receipt.Version != 1 ||
		!regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(receipt.Generation) ||
		!hex64.MatchString(receipt.InboundSHA256) ||
		!hex64.MatchString(receipt.OutboundSHA256) {
		t.Fatalf("attachment canary identity/digests = %#v", receipt)
	}
	if receipt.RealUID != 10002 || receipt.EffectiveUID != 10001 ||
		receipt.SavedUID != 10001 || receipt.FSUID != 10001 {
		t.Fatalf("attachment canary privileged identity = %#v", receipt)
	}
	wantDenials := []string{
		"create:EACCES",
		"open:EACCES",
		"rename:EACCES",
		"traverse:EACCES",
		"unlink:EACCES",
	}
	if !reflect.DeepEqual(receipt.DeniedOutbound, wantDenials) {
		t.Fatalf("attachment canary outbound denials = %v, want %v",
			receipt.DeniedOutbound, wantDenials)
	}
	if !receipt.WarmInboundRead || !receipt.HostOutboundRead ||
		!receipt.PrivateGateHealthy ||
		!receipt.RestartInboundRead || !receipt.RestartOutboundRead ||
		!receipt.OutsideSessions {
		t.Fatalf("attachment canary missing a green arm: %#v", receipt)
	}
	if _, err := os.Stat(filepath.Join(home, "sessions")); !os.IsNotExist(err) {
		t.Fatalf("attachment canary touched the trace namespace: %v", err)
	}

	ps, err := exec.Command("docker", "ps", "-a", "--filter",
		"label=mc.attachment-canary="+receipt.Generation, "--format", "{{.ID}}").CombinedOutput()
	if err != nil {
		t.Fatalf("inspect canary cleanup: %v\n%s", err, ps)
	}
	if strings.TrimSpace(string(ps)) != "" {
		t.Fatalf("attachment canary left a container behind: %s", ps)
	}
}

func TestAttachmentCanaryPublisherHasNoProductionHomieRouteDockerBoundary(t *testing.T) {
	requireProdImage(t)
	t.Run("the canary-only frame is absent", func(t *testing.T) {
		out, err := dockerRun(t, "--user", "10002:10002", prodImage,
			"mc", "__attachment-canary-publish")
		if err == nil || !strings.Contains(out, "canary frame is absent") {
			t.Fatalf("frame-less publisher = err %v output %q", err, out)
		}
	})
	t.Run("a Homie run envelope loses host scope before any frame", func(t *testing.T) {
		root := sharedTempDir(t)
		runJSON := filepath.Join(root, "run.json")
		if err := os.WriteFile(runJSON,
			[]byte(`{"run_id":"h-probe","tier":"homie","role":"homie"}`), 0o444); err != nil {
			t.Fatal(err)
		}
		out, err := dockerRun(t, "--user", "10002:10002",
			"-v", runJSON+":/mc/run.json:ro",
			prodImage, "mc", "__attachment-canary-publish")
		if err == nil || !strings.Contains(out, "host scope only") {
			t.Fatalf("Homie publisher route = err %v output %q", err, out)
		}
	})
}

func TestAttachmentCanaryRecoversExactInterruptedAttemptDockerBoundary(t *testing.T) {
	requireProdImage(t)
	home := sharedTempDir(t)
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	const generation = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	name := "mc-attachment-canary-" + generation
	root := filepath.Join(home, "health", "attachment-canary")
	attempt := filepath.Join(root, "attempt-"+generation)
	for _, path := range []string{
		filepath.Join(home, "health"),
		root,
		attempt,
	} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(attempt, "uncommitted"),
		[]byte("crash residue"), 0o600); err != nil {
		t.Fatal(err)
	}
	active, err := json.Marshal(map[string]any{
		"version": 1, "generation": generation, "container_name": name,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "active.json"),
		append(active, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(
		"docker", "create",
		"--name", name,
		"--label", "mc.attachment-canary="+generation,
		"--mount", "type=bind,src="+attempt+",dst=/probe",
		prodImage, "sh", "-c", "while :; do sleep 3600; done",
	).CombinedOutput(); err != nil {
		t.Fatalf("create interrupted canary fixture: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	if out, err := exec.Command("docker", "start", name).CombinedOutput(); err != nil {
		t.Fatalf("start interrupted canary fixture: %v\n%s", err, out)
	}

	cmd := exec.Command(buildHostMC(t), "__attachment-canary")
	cmd.Env = append(os.Environ(), "MC_HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("recover interrupted attachment canary: %v\n%s", err, out)
	}
	if _, err := os.Lstat(attempt); !os.IsNotExist(err) {
		t.Fatalf("recovered canary retained exact scratch root: %v", err)
	}
	if out, err := exec.Command("docker", "inspect", name).CombinedOutput(); err == nil ||
		!strings.Contains(strings.ToLower(string(out)), "no such") {
		t.Fatalf("recovered canary retained exact container: err %v output %q", err, out)
	}
}
