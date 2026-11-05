//go:build test_fake_routing

package main

import "io"

// The test-tagged binary never self-delegates, so the production whole-wizard
// broker is unreachable. Keeping the symbol closed by build tag lets the
// shared dispatcher compile without importing production host effects.
func brokerWholeOnboard(_ []string, _ io.Reader, _, _ io.Writer) int { return 2 }
