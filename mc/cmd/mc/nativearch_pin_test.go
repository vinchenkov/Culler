//go:build !test_fake_routing

package main

import (
	"mc/deployment"
	"testing"
)

// pinNativeArchitecture fixes the native-architecture gate to the value a
// fixture describes, so image-architecture checks decide the same way on the
// arm64 and the amd64 development and CI hosts. Production keeps the real
// answer; only this package's tests move the seam.
func pinNativeArchitecture(t *testing.T, arch string) {
	t.Helper()
	prev := deployment.NativeArchitecture
	deployment.NativeArchitecture = func() (string, error) { return arch, nil }
	t.Cleanup(func() { deployment.NativeArchitecture = prev })
}
