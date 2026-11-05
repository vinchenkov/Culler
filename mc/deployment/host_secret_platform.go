//go:build !test_fake_routing

package deployment

import "runtime"

func HostCredentialCommandsAvailable() bool { return runtime.GOOS == "darwin" }
