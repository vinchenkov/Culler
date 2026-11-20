package onboarding

import (
	"mc/deployment"
	"testing"
)

// pinNativeArchitecture fixes the native-architecture gate to the value the
// image fixtures describe, so the prepare checks decide the same way on the
// arm64 and the amd64 hosts. Production keeps the real answer.
func pinNativeArchitecture(t *testing.T, arch string) {
	t.Helper()
	prev := deployment.NativeArchitecture
	deployment.NativeArchitecture = func() (string, error) { return arch, nil }
	t.Cleanup(func() { deployment.NativeArchitecture = prev })
}
