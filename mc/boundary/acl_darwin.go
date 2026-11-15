//go:build darwin

package boundary

import (
	"io/fs"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// trustedNoGrantingACL reads the same inode identified by trustedLstat through
// Darwin's native extended-security attribute. GETATTRLIST needs search access
// only on parent directories, so even a deny-only ACL cannot blind inspection.
// The capability query is bound to the expected device; the ACL, owner, mode,
// type, and (device,file-id) are returned by one no-follow kernel query.
func trustedNoGrantingACL(path string, expected fs.FileInfo, ownerUID int) error {
	expectedStat, ok := expected.Sys().(*syscall.Stat_t)
	if !ok {
		return mountErrf(CodeAllowlistUntrusted, "cannot bind ACL inspection to %q identity", path)
	}
	pathp, err := unix.BytePtrFromString(path)
	if err != nil {
		return mountErrf(CodeAllowlistUntrusted, "cannot encode %q for ACL inspection: %v", path, err)
	}

	volumeAttrs := unix.Attrlist{
		Bitmapcount: unix.ATTR_BIT_MAP_COUNT,
		Commonattr:  unix.ATTR_CMN_DEVID,
		Volattr:     unix.ATTR_VOL_INFO | unix.ATTR_VOL_ATTRIBUTES,
	}
	var volumeWords aclVolumeAttributeWords
	_, _, errno := unix.Syscall6(
		unix.SYS_GETATTRLIST,
		uintptr(unsafe.Pointer(pathp)),
		uintptr(unsafe.Pointer(&volumeAttrs)),
		uintptr(unsafe.Pointer(&volumeWords)),
		unsafe.Sizeof(volumeWords),
		unix.FSOPT_NOFOLLOW|unix.FSOPT_REPORT_FULLSIZE,
		0,
	)
	runtime.KeepAlive(pathp)
	runtime.KeepAlive(&volumeAttrs)
	runtime.KeepAlive(&volumeWords)
	if errno != 0 {
		return mountErrf(CodeAllowlistUntrusted,
			"cannot read volume ACL capabilities for %q: %v", path, errno)
	}
	if err := parseACLVolumeAttributes(volumeWords, uint32(expectedStat.Dev)); err != nil {
		return mountErrf(CodeAllowlistUntrusted,
			"cannot establish volume ACL support for %q: %v", path, err)
	}

	attrs := unix.Attrlist{
		Bitmapcount: unix.ATTR_BIT_MAP_COUNT,
		Commonattr: unix.ATTR_CMN_RETURNED_ATTRS |
			unix.ATTR_CMN_DEVID |
			unix.ATTR_CMN_OBJTYPE |
			unix.ATTR_CMN_OWNERID |
			unix.ATTR_CMN_ACCESSMASK |
			unix.ATTR_CMN_EXTENDED_SECURITY |
			unix.ATTR_CMN_UUID |
			unix.ATTR_CMN_FILEID,
	}
	var buf [aclAttributeBufferSize]byte
	_, _, errno = unix.Syscall6(
		unix.SYS_GETATTRLIST,
		uintptr(unsafe.Pointer(pathp)),
		uintptr(unsafe.Pointer(&attrs)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		unix.FSOPT_NOFOLLOW|unix.FSOPT_REPORT_FULLSIZE,
		0,
	)
	runtime.KeepAlive(pathp)
	runtime.KeepAlive(&attrs)
	runtime.KeepAlive(&buf)
	if errno != 0 {
		return mountErrf(CodeAllowlistUntrusted, "cannot read ACL for %q: %v", path, errno)
	}
	snapshot, err := parseACLAttributeBuffer(buf[:], uint32(ownerUID), darwinACLPrincipalIsOwner)
	if err != nil {
		return mountErrf(CodeAllowlistUntrusted, "cannot validate ACL for %q: %v", path, err)
	}
	expectedObjectType := aclObjectTypeRegular
	if expected.IsDir() {
		expectedObjectType = aclObjectTypeDirectory
	}
	if err := validateACLTrust(
		snapshot,
		uint32(expectedStat.Dev),
		expectedStat.Ino,
		expectedObjectType,
		uint32(ownerUID),
	); err != nil {
		return mountErrf(CodeAllowlistUntrusted, "%q is untrusted: %v", path, err)
	}
	return nil
}
