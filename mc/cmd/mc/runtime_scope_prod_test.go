//go:build !test_fake_routing

package main

import (
	"os"
	"testing"
)

// TestOperatorHostScopeThreeDomains pins the A24 widening as a host-only change.
// The three-domain matrix (host / helper / agent) keyed on /.dockerenv: the
// agent domain's run.json identity refusal and the lock-domain guard are proven
// by the e2e acceptor, not here — this test holds the one invariant the change
// must never break, that no container presentation returns true.
//
// The marker error must be the real fs.ErrNotExist that os.Stat returns for an
// absent file: os.IsNotExist does not match an arbitrary error whose text says
// "no such file", and using one here would silently invert the whole matrix.
func TestOperatorHostScopeThreeDomains(t *testing.T) {
	notExist := os.ErrNotExist
	exists := error(nil) // Stat returns nil with no error when the marker is present.
	cases := []struct {
		name       string
		osName     string
		markerErr  error
		wantVision bool
		wantDeleg  bool
	}{
		{"darwin host", "darwin", notExist, true, true},
		{"linux host", "linux", notExist, true, true}, // the A24 change: false → true
		{"linux helper container", "linux", exists, false, false},
		{"darwin container", "darwin", exists, false, false},
		{"unsupported os host", "windows", notExist, false, false},
	}
	osWas, markWas := operatorHostOS, dockerEnvMarker
	t.Cleanup(func() { operatorHostOS, dockerEnvMarker = osWas, markWas })
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			operatorHostOS, dockerEnvMarker = tc.osName, func() error { return tc.markerErr }
			if got := canInspectProjectVision(); got != tc.wantVision {
				t.Errorf("canInspectProjectVision=%v, want %v", got, tc.wantVision)
			}
			if got := shouldDelegateToHelper(); got != tc.wantDeleg {
				t.Errorf("shouldDelegateToHelper=%v, want %v", got, tc.wantDeleg)
			}
		})
	}
}

// TestContainerDomainNeverWidens is the negative proof of the A24 invariant:
// for every host OS value, a present /.dockerenv forces both predicates false,
// so the widening cannot leak into a container regardless of the platform the
// container reports.
func TestContainerDomainNeverWidens(t *testing.T) {
	osWas, markWas := operatorHostOS, dockerEnvMarker
	t.Cleanup(func() { operatorHostOS, dockerEnvMarker = osWas, markWas })
	dockerEnvMarker = func() error { return nil }
	for _, osName := range []string{"darwin", "linux", "windows", "freebsd"} {
		operatorHostOS = osName
		if canInspectProjectVision() || shouldDelegateToHelper() {
			t.Errorf("os %s inside /.dockerenv widened a container predicate", osName)
		}
	}
}
