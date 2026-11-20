//go:build linux

package deployment

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSystemctl puts a recording systemctl on PATH. The script body sees the
// argv after "systemctl" and the file named by MC_FAKE_SYSTEMCTL_LOG; the
// environment chooses what each verb answers.

func writeFakeSystemctl(t *testing.T, body string) func() []string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >>" + logPath + "\n" + body
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return func() []string {
		data, err := os.ReadFile(logPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	}
}

func linuxHome(t *testing.T) string {
	t.Helper()
	home, err := CanonicalHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return home
}

func unitNames(t *testing.T, home string) []string {
	t.Helper()
	names, err := RuntimeNames(home)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(SupervisedUnits))
	for _, unit := range SupervisedUnits {
		out = append(out, UnitLabel(names, unit)+".service")
	}
	return out
}

func TestPlatformUpdateServicesLinuxStopSwallowsManagerErrors(t *testing.T) {
	// The launchd half swallows every stop error; the Linux half must match,
	// including a systemctl that fails loudly. This pins the open question, it
	// does not resolve it.
	calls := writeFakeSystemctl(t, "printf '%s\\n' 'Failed to connect to bus.' >&2\nexit 5\n")
	home := linuxHome(t)
	if err := platformUpdateServices(home)(context.Background(), true); err != nil {
		t.Fatalf("a failing systemctl stop returned: %v", err)
	}
	want := unitNames(t, home)
	got := calls()
	if len(got) != len(want) {
		t.Fatalf("stop calls = %q, want one per supervised unit", got)
	}
	seen := map[string]bool{}
	for _, line := range got {
		seen[line] = true
	}
	for _, name := range want {
		if !seen["--user disable --now "+name] {
			t.Fatalf("no disable --now for %s in %q", name, got)
		}
	}
}

func TestPlatformUpdateServicesLinuxConfirmMapsStates(t *testing.T) {
	home := linuxHome(t)
	for _, tc := range []struct {
		name    string
		body    string
		failure string
	}{
		{
			name:    "active",
			body:    "printf 'active\\n'\nexit 0\n",
			failure: "remains loaded",
		},
		{
			name: "inactive",
			body: "printf 'inactive\\n'\nexit 1\n",
		},
		{
			name: "gone",
			body: "printf 'unknown\\n'\nexit 3\n",
		},
		{
			name:    "manager unreachable",
			body:    "printf 'Failed to connect to bus: No such file or directory\\n' >&2\nexit 4\n",
			failure: "state is unknown",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeFakeSystemctl(t, tc.body)
			err := platformUpdateServices(home)(context.Background(), false)
			if tc.failure == "" {
				if err != nil {
					t.Fatalf("confirm accepted no problem, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.failure) {
				t.Fatalf("confirm error = %v, want it to contain %q", err, tc.failure)
			}
			for _, unit := range SupervisedUnits {
				if !strings.Contains(err.Error(), unit) {
					t.Fatalf("the problem does not name the unit %s: %v", unit, err)
				}
			}
		})
	}
}
