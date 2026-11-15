package boundary

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// These are the stable, public Darwin ABIs behind getattrlist(2),
// ATTR_CMN_EXTENDED_SECURITY, and sys/kauth.h. Parsing stays platform-neutral
// so malformed or truncated kernel payloads can be tested on every build.
const (
	aclAttributeGroupCount      = 5
	aclAttributeFixedHeaderSize = 4 + aclAttributeGroupCount*4
	aclAttributeReferenceSize   = 8
	aclPrincipalSize            = 16

	aclAttrDevice           = uint32(0x00000002)
	aclAttrObjectType       = uint32(0x00000008)
	aclAttrOwnerID          = uint32(0x00008000)
	aclAttrAccessMask       = uint32(0x00020000)
	aclAttrExtendedSecurity = uint32(0x00400000)
	aclAttrOwnerUUID        = uint32(0x00800000)
	aclAttrFileID           = uint32(0x02000000)
	aclAttrReturned         = uint32(0x80000000)

	aclAttributeDevicePosition     = aclAttributeFixedHeaderSize
	aclAttributeObjectTypePosition = aclAttributeDevicePosition + 4
	aclAttributeOwnerIDPosition    = aclAttributeObjectTypePosition + 4
	aclAttributeAccessMaskPosition = aclAttributeOwnerIDPosition + 4
	aclAttributeReferencePosition  = aclAttributeAccessMaskPosition + 4
	aclAttributeOwnerUUIDPosition  = aclAttributeReferencePosition + aclAttributeReferenceSize
	aclAttributeFileIDPosition     = aclAttributeOwnerUUIDPosition + aclPrincipalSize
	aclAttributeIdentityFixedSize  = aclAttributeFileIDPosition + 8

	aclFileSecurityMagic            = uint32(0x012cc16d)
	aclFileSecurityEntryCountOffset = 36
	aclFileSecurityHeaderSize       = 44
	aclFileSecurityNoACL            = ^uint32(0)
	aclMaxEntries                   = uint32(128)

	aclEntrySize         = 24
	aclEntryFlagsOffset  = 16
	aclEntryRightsOffset = 20
	aclEntryKindMask     = uint32(0x0f)
	aclEntryAllow        = uint32(1)
	aclEntryDeny         = uint32(2)

	aclRightReadData       = uint32(1 << 1)
	aclRightWriteData      = uint32(1 << 2)
	aclObjectTypeRegular   = uint32(1)
	aclObjectTypeDirectory = uint32(2)

	aclVolumeAttributeBufferSize = 4 + 4 + aclAttributeGroupCount*4*2
	aclAttributeBufferSize       = aclAttributeIdentityFixedSize +
		aclFileSecurityHeaderSize + int(aclMaxEntries)*aclEntrySize
)

type aclVolumeAttributeWords struct {
	Length uint32
	Device uint32
	Valid  [aclAttributeGroupCount]uint32
	Native [aclAttributeGroupCount]uint32
}

func parseACLVolumeAttributes(words aclVolumeAttributeWords, expectedDevice uint32) error {
	if words.Length != aclVolumeAttributeBufferSize {
		return fmt.Errorf("volume attribute payload is %d bytes, want %d",
			words.Length, aclVolumeAttributeBufferSize)
	}
	required := aclAttrDevice | aclAttrExtendedSecurity
	if words.Valid[0]&required != required {
		return fmt.Errorf("volume does not report device identity and extended-security support")
	}
	if words.Device != expectedDevice {
		return fmt.Errorf("volume identity changed during ACL capability inspection")
	}
	return nil
}

// ownerCompatibilityUUID returns Darwin membership.h's stable synthesized
// user UUID. The native membership contract says this form remains valid for
// ACL operations even if a directory-service UUID is later assigned.
func ownerCompatibilityUUID(uid uint32) [aclPrincipalSize]byte {
	result := [aclPrincipalSize]byte{
		0xff, 0xff, 0xee, 0xee,
		0xdd, 0xdd,
		0xcc, 0xcc,
		0xbb, 0xbb,
		0xaa, 0xaa,
	}
	binary.BigEndian.PutUint32(result[12:], uid)
	return result
}

type aclAttributeSnapshot struct {
	Device        uint32
	ObjectType    uint32
	OwnerUID      uint32
	AccessMask    uint32
	FileID        uint64
	NonOwnerGrant bool
}

type aclOwnerResolver func([aclPrincipalSize]byte, uint32) (bool, error)

func validateACLTrust(
	snapshot aclAttributeSnapshot,
	expectedDevice uint32,
	expectedFileID uint64,
	expectedObjectType uint32,
	expectedOwnerUID uint32,
) error {
	if snapshot.Device != expectedDevice || snapshot.FileID != expectedFileID {
		return fmt.Errorf("object identity changed during ACL inspection")
	}
	if snapshot.ObjectType != expectedObjectType {
		return fmt.Errorf("object type changed during ACL inspection")
	}
	if snapshot.OwnerUID != expectedOwnerUID {
		return fmt.Errorf("object owner changed during ACL inspection")
	}
	if snapshot.AccessMask&0o077 != 0 {
		return fmt.Errorf("object mode grants group or other access during ACL inspection")
	}
	if snapshot.NonOwnerGrant {
		return fmt.Errorf("ACL grants access to a non-owner")
	}
	return nil
}

// parseACLAttributeBuffer validates the exact buffer requested by the Darwin
// inspector and reports the object identity plus whether any allow ACE grants
// any right to a principal that cannot be proven to be the operator owner.
// Unknown data denies rather than silently falling back to POSIX mode bits.
func parseACLAttributeBuffer(
	buf []byte,
	ownerUID uint32,
	resolveOwner aclOwnerResolver,
) (aclAttributeSnapshot, error) {
	if len(buf) < aclAttributeFixedHeaderSize {
		return aclAttributeSnapshot{}, fmt.Errorf("ACL attribute payload is %d bytes, want at least %d",
			len(buf), aclAttributeFixedHeaderSize)
	}
	reported := int(binary.LittleEndian.Uint32(buf[0:4]))
	if reported < aclAttributeFixedHeaderSize || reported > len(buf) {
		return aclAttributeSnapshot{}, fmt.Errorf("ACL attribute payload reports invalid length %d in %d-byte buffer",
			reported, len(buf))
	}
	buf = buf[:reported]

	common := binary.LittleEndian.Uint32(buf[4:8])
	if common&aclAttrReturned == 0 {
		return aclAttributeSnapshot{}, fmt.Errorf("ACL attribute payload omitted returned-attributes marker")
	}
	allowed := aclAttrReturned | aclAttrDevice | aclAttrObjectType | aclAttrOwnerID |
		aclAttrAccessMask | aclAttrExtendedSecurity | aclAttrOwnerUUID | aclAttrFileID
	if unexpected := common & ^allowed; unexpected != 0 {
		return aclAttributeSnapshot{}, fmt.Errorf("ACL attribute payload returned unexpected common attributes %#x", unexpected)
	}
	required := aclAttrDevice | aclAttrObjectType | aclAttrOwnerID | aclAttrAccessMask | aclAttrFileID
	if common&required != required {
		return aclAttributeSnapshot{}, fmt.Errorf("ACL attribute payload omitted trust fields")
	}
	for group := 1; group < aclAttributeGroupCount; group++ {
		off := 4 + group*4
		if bits := binary.LittleEndian.Uint32(buf[off : off+4]); bits != 0 {
			return aclAttributeSnapshot{}, fmt.Errorf("ACL attribute payload returned unexpected group %d attributes %#x", group, bits)
		}
	}
	cursor := aclAttributeFixedHeaderSize
	if len(buf) < cursor+4 {
		return aclAttributeSnapshot{}, fmt.Errorf("ACL attribute payload truncates device identity")
	}
	snapshot := aclAttributeSnapshot{
		Device: binary.LittleEndian.Uint32(buf[cursor : cursor+4]),
	}
	cursor += 4
	if len(buf) < cursor+12 {
		return aclAttributeSnapshot{}, fmt.Errorf("ACL attribute payload truncates type, owner, or mode")
	}
	snapshot.ObjectType = binary.LittleEndian.Uint32(buf[cursor : cursor+4])
	cursor += 4
	snapshot.OwnerUID = binary.LittleEndian.Uint32(buf[cursor : cursor+4])
	cursor += 4
	snapshot.AccessMask = binary.LittleEndian.Uint32(buf[cursor : cursor+4])
	cursor += 4
	refPosition := -1
	if common&aclAttrExtendedSecurity != 0 {
		refPosition = cursor
		cursor += aclAttributeReferenceSize
	}
	var ownerRecordUUID [aclPrincipalSize]byte
	ownerRecordUUIDValid := false
	if common&aclAttrOwnerUUID != 0 {
		if len(buf) < cursor+aclPrincipalSize {
			return aclAttributeSnapshot{}, fmt.Errorf("ACL attribute payload truncates owner UUID")
		}
		copy(ownerRecordUUID[:], buf[cursor:cursor+aclPrincipalSize])
		ownerRecordUUIDValid = !allZero(ownerRecordUUID[:])
		cursor += aclPrincipalSize
	}
	if len(buf) < cursor+8 {
		return aclAttributeSnapshot{}, fmt.Errorf("ACL attribute payload truncates file identity")
	}
	snapshot.FileID = binary.LittleEndian.Uint64(buf[cursor : cursor+8])
	cursor += 8
	if common&aclAttrExtendedSecurity == 0 {
		// Darwin reserves an eight-byte zero attrreference in the reported
		// length when the requested variable attribute is absent, even though
		// later returned fixed attributes are packed as if it were omitted.
		if len(buf) != aclAttributeIdentityFixedSize {
			return aclAttributeSnapshot{}, fmt.Errorf("ACL-free attribute payload is %d bytes, want %d",
				len(buf), aclAttributeIdentityFixedSize)
		}
		if !allZero(buf[cursor:]) {
			return aclAttributeSnapshot{}, fmt.Errorf("ACL-free attribute payload has nonzero trailing data")
		}
		return snapshot, nil
	}

	dataOffset := int(int32(binary.LittleEndian.Uint32(buf[refPosition : refPosition+4])))
	dataLength := int(binary.LittleEndian.Uint32(buf[refPosition+4 : refPosition+8]))
	if dataOffset < 0 {
		return aclAttributeSnapshot{}, fmt.Errorf("ACL attribute reference has negative offset %d", dataOffset)
	}
	dataStart := refPosition + dataOffset
	if dataStart%4 != 0 || dataStart < cursor ||
		dataStart > len(buf) || dataLength > len(buf)-dataStart {
		return aclAttributeSnapshot{}, fmt.Errorf("ACL attribute reference (%d,%d) escapes %d-byte payload",
			dataOffset, dataLength, len(buf))
	}
	if !allZero(buf[cursor:dataStart]) {
		return aclAttributeSnapshot{}, fmt.Errorf("ACL attribute payload has nonzero data before referenced ACL")
	}
	if dataStart+dataLength != len(buf) {
		return aclAttributeSnapshot{}, fmt.Errorf("ACL attribute payload has unclassified trailing data")
	}
	fileSecurity := buf[dataStart : dataStart+dataLength]
	if len(fileSecurity) < aclFileSecurityHeaderSize {
		return aclAttributeSnapshot{}, fmt.Errorf("file-security payload is %d bytes, want at least %d",
			len(fileSecurity), aclFileSecurityHeaderSize)
	}
	if magic := binary.LittleEndian.Uint32(fileSecurity[0:4]); magic != aclFileSecurityMagic {
		return aclAttributeSnapshot{}, fmt.Errorf("file-security payload has invalid magic %#x", magic)
	}
	entryCount := binary.LittleEndian.Uint32(
		fileSecurity[aclFileSecurityEntryCountOffset : aclFileSecurityEntryCountOffset+4])
	if entryCount == aclFileSecurityNoACL {
		if len(fileSecurity) != aclFileSecurityHeaderSize {
			return aclAttributeSnapshot{}, fmt.Errorf("no-ACL file-security payload has unexpected entries")
		}
		return snapshot, nil
	}
	if entryCount > aclMaxEntries {
		return aclAttributeSnapshot{}, fmt.Errorf("file-security payload has %d entries, maximum is %d",
			entryCount, aclMaxEntries)
	}
	wantLength := aclFileSecurityHeaderSize + int(entryCount)*aclEntrySize
	if len(fileSecurity) != wantLength {
		return aclAttributeSnapshot{}, fmt.Errorf("file-security payload is %d bytes for %d entries, want %d",
			len(fileSecurity), entryCount, wantLength)
	}

	ownerCompat := ownerCompatibilityUUID(ownerUID)
	for i := uint32(0); i < entryCount; i++ {
		off := aclFileSecurityHeaderSize + int(i)*aclEntrySize
		entry := fileSecurity[off : off+aclEntrySize]
		flags := binary.LittleEndian.Uint32(entry[aclEntryFlagsOffset : aclEntryFlagsOffset+4])
		rights := binary.LittleEndian.Uint32(entry[aclEntryRightsOffset : aclEntryRightsOffset+4])
		switch flags & aclEntryKindMask {
		case aclEntryDeny:
			continue
		case aclEntryAllow:
			if rights == 0 {
				continue
			}
			principal := entry[:aclPrincipalSize]
			if bytes.Equal(principal, ownerCompat[:]) ||
				(ownerRecordUUIDValid && bytes.Equal(principal, ownerRecordUUID[:])) {
				continue
			}
			if resolveOwner != nil {
				var qualifier [aclPrincipalSize]byte
				copy(qualifier[:], principal)
				isOwner, err := resolveOwner(qualifier, ownerUID)
				if err != nil {
					return aclAttributeSnapshot{}, fmt.Errorf(
						"ACL entry %d principal cannot be resolved: %w", i, err)
				}
				if isOwner {
					continue
				}
			}
			snapshot.NonOwnerGrant = true
			return snapshot, nil
		default:
			return aclAttributeSnapshot{}, fmt.Errorf("ACL entry %d has unknown kind %#x", i, flags&aclEntryKindMask)
		}
	}
	return snapshot, nil
}

func allZero(data []byte) bool {
	for _, b := range data {
		if b != 0 {
			return false
		}
	}
	return true
}
