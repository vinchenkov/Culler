//go:build test_fake_routing

package verbs

// The fast lane is container-runtime-free by construction, so the
// test-tagged build reports the probe as deferred instead of failing every
// hermetic test deployment. The production build has no such arm: it runs
// the real capability probe unconditionally.
func containerRuntimeFinding(string) (string, string) {
	return "deferred", "capability probe (setuid gate, helper exec) runs only in the production build; the test-tagged binary is container-runtime-free"
}
