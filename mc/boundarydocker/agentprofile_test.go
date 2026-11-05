//go:build docker_boundary

package boundarydocker

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func stageRunnerSource(t *testing.T) string {
	t.Helper()
	path := filepath.Join(sharedTempDir(t), "runner")
	cmd := exec.Command("python3", "../../runner/image/stage-runtime.py", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("stage the runner release: %v\n%s", err, output)
	}
	return path
}

func checkProxyHarness(t *testing.T, harness string) {
	t.Helper()
	source, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"account", "api-key"} {
		t.Run(method, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "mise", "exec", "--", "bun", "tests/provider-proxy/harness.ts",
				"--image", prodImage, "--harness", harness, "--auth-mode", method)
			cmd.Dir = source
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), `"result":"CULLER_HARNESS_PROXY_OK"`) {
				t.Fatalf("The synthetic proxy harness check failed: %v\n%s", err, out)
			}
			t.Log(strings.TrimSpace(string(out)))
		})
	}
}

func TestAgentProfileClaudeModelDockerBoundary(t *testing.T) {
	checkProxyHarness(t, "claude-sdk")
}

func TestAgentProfileCodexModelDockerBoundary(t *testing.T) {
	checkProxyHarness(t, "codex")
}

func TestCredentialLeakDetectorPositiveControlsDockerBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "mise", "exec", "--", "bun", "tests/provider-proxy/leak-controls.ts", "--image", prodImage)
	cmd.Dir = "../.."
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), `"result":"CULLER_LEAK_CONTROLS_OK"`) {
		t.Fatalf("The credential leak controls failed: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}
