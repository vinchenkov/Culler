//go:build !test_fake_routing

package verbs

import "fmt"

// containerRuntimeFinding runs the real capability probe. The
// test-tagged build carries a deferred stand-in instead, because the fast
// lane is container-runtime-free by construction.
func containerRuntimeFinding(spinePath string) (string, string) {
	detail, err := containerRuntimeCapabilityProbe(spinePath)
	if err != nil {
		return "fail", fmt.Sprintf(
			"%v — likely causes: container runtime unreachable, helper missing, no-new-privileges forced, uid remapping (rootless), or Enhanced Container Isolation on",
			err)
	}
	return "ok", detail
}
