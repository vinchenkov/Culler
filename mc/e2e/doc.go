// Package e2e holds the Docker-gated end-to-end tests. The opt-in gate is the
// docker_e2e build tag. This file carries no tag on purpose: it keeps the
// package visible to the untagged toolchain, so the fast lane reports
// `mc/e2e [no test files]` (Docker-free by construction) instead of "matched
// no packages". Run the e2e with:
//
//	cd mc && mise exec -- go test -tags docker_e2e -v -timeout 30m ./e2e/...
package e2e
