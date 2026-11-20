package deployment

import (
	"fmt"
	"runtime"
)

// The two deployment targets are Apple silicon and x86_64 Linux. Nothing runs
// under emulation: a container image or helper binary must execute on a kernel
// of its own architecture, and a host whose native architecture is outside
// this set fails closed at every gate that consults it.

// NativeArchitecture answers the host architecture the gates compare against.
// It is a variable so behavior tests can pin either target on any host;
// production binds the real answer once at init.
var NativeArchitecture = func() (string, error) {
	switch runtime.GOARCH {
	case "arm64", "amd64":
		return runtime.GOARCH, nil
	}
	return "", fmt.Errorf("unsupported host architecture %s; the deployment targets are arm64 and amd64 with no emulation", runtime.GOARCH)
}

// HostMachineForArchitecture maps a Go architecture to the machine name
// uname(2) reports for it on its native kernel.
func HostMachineForArchitecture(arch string) (string, error) {
	switch arch {
	case "arm64":
		return "aarch64", nil
	case "amd64":
		return "x86_64", nil
	}
	return "", fmt.Errorf("unsupported architecture %s", arch)
}

// ImageArchitectureIsNative reports whether a docker-inspect Architecture
// value names the host's own supported architecture.
func ImageArchitectureIsNative(architecture string) bool {
	native, err := NativeArchitecture()
	return err == nil && architecture == native
}
