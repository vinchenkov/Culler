//go:build darwin

package boundary

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// mbr_uuid_to_id is the public membership(3) operation that resolves both
// Darwin's synthesized ACL UUIDs and directory-service UUIDs to a uid or gid.
// The same libSystem trampoline pattern is used by x/sys/unix on Darwin, so
// this remains available when the runner is built with CGO_ENABLED=0.
func darwinLibcCall(fn, a1, a2, a3 uintptr) (r1, r2 uintptr, err syscall.Errno)

//go:linkname darwinLibcCall syscall.syscall

var libcMBRUUIDToIDAddr uintptr

//go:cgo_import_dynamic libc_mbr_uuid_to_id mbr_uuid_to_id "/usr/lib/libSystem.B.dylib"

const membershipIDTypeUID = int32(0)

func darwinACLPrincipalIsOwner(principal [aclPrincipalSize]byte, ownerUID uint32) (bool, error) {
	resolvedID := ^uint32(0)
	resolvedType := int32(-1)
	rc, _, callErr := darwinLibcCall(
		libcMBRUUIDToIDAddr,
		uintptr(unsafe.Pointer(&principal[0])),
		uintptr(unsafe.Pointer(&resolvedID)),
		uintptr(unsafe.Pointer(&resolvedType)),
	)
	runtime.KeepAlive(principal)
	runtime.KeepAlive(&resolvedID)
	runtime.KeepAlive(&resolvedType)
	if callErr != 0 {
		return false, fmt.Errorf("membership UUID lookup call: %w", callErr)
	}
	// mbr_uuid_to_id returns a C int error code directly. Only its low 32
	// bits are defined by the Darwin ABI; the runtime does not promise to
	// clear the upper half of a uintptr result from a 32-bit C return.
	if code := uint32(rc); code != 0 {
		return false, fmt.Errorf("membership UUID lookup: %w", syscall.Errno(code))
	}
	return resolvedType == membershipIDTypeUID && resolvedID == ownerUID, nil
}
