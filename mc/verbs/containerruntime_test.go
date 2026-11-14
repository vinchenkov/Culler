package verbs

import (
	"strings"
	"testing"
)

// The doctor accepts a container runtime by capability, never by runtime
// identity. The two procfs parsers below are the identity-map and
// no-new-privileges legs of the doctor container-runtime probe; the setuid
// legs are kernel-backed and run only inside the helper (docker lanes).

func TestUIDMapIdentity(t *testing.T) {
	cases := []struct {
		label   string
		content string
		want    bool
	}{
		{"identity map", "         0          0 4294967295\n", true},
		{"rootless remap", "         0       1000          1\n", false},
		{"eci-style remap", "         0     100000      65536\n", false},
		{"multi-line remap", "         0          0       1000\n      1000     101000      64536\n", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			if got := uidMapIsIdentity(c.content); got != c.want {
				t.Fatalf("uidMapIsIdentity(%q) = %v, want %v", c.content, got, c.want)
			}
		})
	}
}

func TestNoNewPrivsFlag(t *testing.T) {
	status := "Name:\tmc\nUid:\t10002\t10001\t10001\t10001\nNoNewPrivs:\t0\nSeccomp:\t0\n"
	flag, err := noNewPrivsFlag(status)
	if err != nil || flag != 0 {
		t.Fatalf("noNewPrivsFlag = %d, %v; want 0, nil", flag, err)
	}
	forced := strings.Replace(status, "NoNewPrivs:\t0", "NoNewPrivs:\t1", 1)
	flag, err = noNewPrivsFlag(forced)
	if err != nil || flag != 1 {
		t.Fatalf("noNewPrivsFlag(forced) = %d, %v; want 1, nil", flag, err)
	}
	if _, err := noNewPrivsFlag("Name:\tmc\n"); err == nil {
		t.Fatal("noNewPrivsFlag must refuse a status with no NoNewPrivs line rather than assume 0")
	}
}
