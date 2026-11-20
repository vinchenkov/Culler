package deployment

import (
	"fmt"
	"testing"
)

func TestNativeArchitectureDomain(t *testing.T) {
	prev := NativeArchitecture
	defer func() { NativeArchitecture = prev }()
	cases := []struct {
		arch    string
		machine string // empty means: must fail closed
	}{
		{"arm64", "aarch64"},
		{"amd64", "x86_64"},
		{"riscv64", ""},
		{"arm", ""},
	}
	for _, tc := range cases {
		NativeArchitecture = func() (string, error) {
			if tc.machine == "" {
				return "", fmt.Errorf("unsupported host architecture %s", tc.arch)
			}
			return tc.arch, nil
		}
		_, err := NativeArchitecture()
		if tc.machine == "" {
			if err == nil {
				t.Fatalf("architecture %s accepted; the domain is arm64 and amd64 only", tc.arch)
			}
			continue
		}
		if err != nil {
			t.Fatalf("architecture %s refused: %v", tc.arch, err)
		}
		machine, merr := HostMachineForArchitecture(tc.arch)
		if merr != nil || machine != tc.machine {
			t.Fatalf("machine for %s = %q %v, want %q", tc.arch, machine, merr, tc.machine)
		}
		if !ImageArchitectureIsNative(tc.arch) {
			t.Fatalf("image arch %s is not read as native against native %s", tc.arch, tc.arch)
		}
		other := "arm64"
		if tc.arch == "arm64" {
			other = "amd64"
		}
		if ImageArchitectureIsNative(other) {
			t.Fatalf("image arch %s must not pass as native on %s", other, tc.arch)
		}
	}
}
