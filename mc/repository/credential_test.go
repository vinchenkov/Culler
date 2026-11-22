package repository

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCredentialHelperResetSuppressesRepoLocalHelper is the behavioral proof
// the owner asked for: not that the flag string is present, but that a
// repository-carried credential helper — the one survivor of GIT_CONFIG_GLOBAL=/dev/null
// and GIT_CONFIG_NOSYSTEM, since git trusts the repository's own .git/config at
// repo scope — does not execute under the native executor's environment, and
// DOES execute without the reset. A pen file written by the helper is the
// instrument; credential fill runs helpers with no network and no dependency on
// protocol.allow.
func TestCredentialHelperResetSuppressesRepoLocalHelper(t *testing.T) {
	ctx := context.Background()
	g, _ := fixture(t)
	pen := filepath.Join(t.TempDir(), "helper-ran")

	// A malicious repo-local helper that leaves a pen mark and feeds a value.
	// git runs the value after `!` through `sh -c`, so the command writes the
	// pen file (the execution witness) and emits one credential line.
	seed := exec.Command("git", "-C", g.Root, "-c", "safe.directory="+g.Root,
		"config", "credential.helper", "!touch "+pen+" && printf username=attacker\\n")
	seed.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "none"), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := seed.CombinedOutput(); err != nil {
		t.Fatalf("seed repo helper: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(gitConfigRead(t, g.Root, "credential.helper")); got == "" {
		t.Fatal("the repo-local helper was not written; the fixture is wrong")
	}

	// Product path: the native executor's own environment and flags.
	cmd := g.process(ctx, "credential", "fill")
	cmd.Stdin = strings.NewReader("protocol=http\nhost=example.invalid\n\n")
	if _, err := cmd.Output(); err != nil {
		// No helper + GIT_TERMINAL_PROMPT=0 may exit non-zero; that is fine.
		// The assertion is the absence of the pen, not the exit code.
	}
	if _, err := os.Stat(pen); !os.IsNotExist(err) {
		t.Fatalf("the native executor RAN the repo-local credential helper (pen exists); the reset is not effective")
	}

	// Control: identical hardened environment MINUS the credential reset must
	// run the helper — proving the global/system silencing alone does not stop
	// the repo scope, and that the reset is the operative difference.
	os.Remove(pen)
	control := exec.Command("git", "-C", g.Root,
		"-c", "safe.directory="+g.Root, "-c", "core.hooksPath=/dev/null",
		"credential", "fill")
	control.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.TempDir(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}
	control.Stdin = strings.NewReader("protocol=http\nhost=example.invalid\n\n")
	_ = control.Run()
	if _, err := os.Stat(pen); err != nil {
		t.Fatalf("control (no reset) did NOT run the repo helper; the test does not isolate the reset: %v", err)
	}
}

func gitConfigRead(t *testing.T, root, key string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "-c", "safe.directory="+root, "config", "--get-all", key)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}
