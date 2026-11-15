//go:build linux

package verbs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"syscall"
)

// The image contract's two uids (runner/image/Dockerfile): mc-completion owns
// the spine and setuid gates; agents and the helper process run as 10002.
const (
	imageOwnerUID = 10001
	imageAgentUID = 10002
)

// privilegedMCPath is the image's general setuid mc — every ordinary agent
// and helper verb reaches the spine through this gate. Sealed completion has
// an additional narrow publisher wrapper, but it is not the general broker.
const privilegedMCPath = "/opt/culler-runtime/libexec/mc-real"

// stNOSUID is statfs's ST_NOSUID mount flag: set when the filesystem ignores
// suid bits, which would silently disarm the completion wrapper.
const stNOSUID = 0x2

// containerRuntimeCapabilityProbe runs the capability legs from inside the
// helper: the kernel refusing a spine open at the agent uid, the helper's own
// spine read succeeding, the completion wrapper's suid bit present on a mount
// that honors it, NoNewPrivs clear, identity uid_map (no rootless remap, no
// Enhanced Container Isolation), and native arm64. Every leg is a capability
// fact; no runtime name or version is consulted.
func containerRuntimeCapabilityProbe(spinePath string) (string, error) {
	if _, err := os.Stat("/.dockerenv"); err != nil {
		return "", fmt.Errorf("not inside the helper container")
	}
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return "", fmt.Errorf("read process status: %v", err)
	}
	flag, err := noNewPrivsFlag(string(status))
	if err != nil {
		return "", err
	}
	if flag != 0 {
		return "", fmt.Errorf("no-new-privileges is forced; the setuid mc gate cannot elevate")
	}
	uidMap, err := os.ReadFile("/proc/self/uid_map")
	if err != nil {
		return "", fmt.Errorf("read uid_map: %v", err)
	}
	if !uidMapIsIdentity(string(uidMap)) {
		return "", fmt.Errorf("uid_map is not the identity map (rootless remap or Enhanced Container Isolation)")
	}

	// The live refusal leg: open the spine with the agent's uid and demand
	// the kernel's EACCES. The final helper enters through setuid mc, so it can
	// drop to its real uid for the negative arm and restore its effective uid
	// for the brokered read. Root remains accepted only for old probe fixtures.
	ruid, euid := syscall.Getuid(), syscall.Geteuid()
	probeUID := 0
	switch {
	case euid == 0:
		probeUID = imageAgentUID
	case ruid != euid:
		probeUID = ruid
	default:
		return "", fmt.Errorf("uid %d cannot exercise the agent-uid refusal leg (helper must run as root or setuid)", euid)
	}
	if err := syscall.Seteuid(probeUID); err != nil {
		return "", fmt.Errorf("assume agent uid %d: %v", probeUID, err)
	}
	directFile, directErr := os.Open(spinePath)
	if directErr == nil {
		directFile.Close()
	}
	if err := syscall.Seteuid(euid); err != nil {
		return "", fmt.Errorf("restore uid %d: %v", euid, err)
	}
	if directErr == nil {
		return "", fmt.Errorf("direct spine open as uid %d succeeded; the kernel is not enforcing the gate", probeUID)
	}
	if !errors.Is(directErr, syscall.EACCES) {
		return "", fmt.Errorf("direct spine open as uid %d failed with %v, want the kernel's EACCES", probeUID, directErr)
	}
	gated, err := os.Open(spinePath)
	if err != nil {
		return "", fmt.Errorf("helper spine read failed: %v", err)
	}
	gated.Close()

	// The suid bit must be present on the general mc gate AND honored by
	// its mount: a nosuid filesystem stats identically and disarms the gate.
	wrapper, err := os.Stat(privilegedMCPath)
	if err != nil {
		return "", fmt.Errorf("privileged mc missing: %v", err)
	}
	if wrapper.Mode()&fs.ModeSetuid == 0 {
		return "", fmt.Errorf("privileged mc %s has no suid bit (mode %v)", privilegedMCPath, wrapper.Mode())
	}
	if st, ok := wrapper.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != imageOwnerUID {
		return "", fmt.Errorf("privileged mc is not owned by uid %d", imageOwnerUID)
	}
	var mount syscall.Statfs_t
	if err := syscall.Statfs(privilegedMCPath, &mount); err != nil {
		return "", fmt.Errorf("statfs privileged mc: %v", err)
	}
	if mount.Flags&stNOSUID != 0 {
		return "", fmt.Errorf("privileged mc filesystem is mounted nosuid; the gate cannot elevate")
	}

	if runtime.GOARCH != "arm64" {
		return "", fmt.Errorf("non-native architecture %s; the deployment target is arm64 with no emulation", runtime.GOARCH)
	}
	return fmt.Sprintf(
		"helper exec round-trip proven by this crossing; agent uid %d direct spine open EACCES; helper spine read ok; general mc suid honored (owner %d); NoNewPrivs=0; identity uid_map; native %s",
		probeUID, imageOwnerUID, runtime.GOARCH), nil
}
