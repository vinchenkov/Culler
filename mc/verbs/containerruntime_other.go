//go:build !linux

package verbs

import "fmt"

// A production darwin invocation self-delegates into the helper before any
// verb runs, so reaching this stub means the crossing did not happen — the
// probe fails closed rather than guessing at capabilities it cannot measure.
func containerRuntimeCapabilityProbe(string) (string, error) {
	return "", fmt.Errorf("not running inside the helper container (self-delegation did not cross)")
}
