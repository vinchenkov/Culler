//go:build darwin

package boundary

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// suffixCaseModeForPath asks the volume containing path how it compares names.
// This is a pure-Go syscall wrapper because the shipped mc binary is static and
// must compile with CGO_ENABLED=0.
func suffixCaseModeForPath(path string) (suffixCaseMode, error) {
	pathp, err := unix.BytePtrFromString(path)
	if err != nil {
		return suffixCaseUnknown, mountErrf(CodeDeniedRoot,
			"cannot encode path %q for volume capability lookup: %v", path, err)
	}

	attrs := unix.Attrlist{
		Bitmapcount: unix.ATTR_BIT_MAP_COUNT,
		Volattr:     unix.ATTR_VOL_INFO | unix.ATTR_VOL_CAPABILITIES,
	}
	var words volumeCapabilityWords
	_, _, errno := unix.Syscall6(
		unix.SYS_GETATTRLIST,
		uintptr(unsafe.Pointer(pathp)),
		uintptr(unsafe.Pointer(&attrs)),
		uintptr(unsafe.Pointer(&words)),
		unsafe.Sizeof(words),
		0,
		0,
	)
	runtime.KeepAlive(pathp)
	runtime.KeepAlive(&attrs)
	runtime.KeepAlive(&words)
	if errno != 0 {
		return suffixCaseUnknown, mountErrf(CodeDeniedRoot,
			"cannot read volume case capabilities for %q: %v", path, errno)
	}
	return parseVolumeCaseCapabilities(words)
}
