package boundary

import (
	"encoding/binary"
	"errors"
	"testing"
)

type aclTestEntry struct {
	principal [aclPrincipalSize]byte
	flags     uint32
	rights    uint32
}

func aclTestAttributeBuffer(ownerUUID *[aclPrincipalSize]byte, entries []aclTestEntry) []byte {
	common := uint32(aclAttrReturned | aclAttrDevice | aclAttrObjectType | aclAttrOwnerID |
		aclAttrAccessMask | aclAttrExtendedSecurity | aclAttrOwnerUUID | aclAttrFileID)
	fixedEnd := aclAttributeIdentityFixedSize

	fileSecuritySize := aclFileSecurityHeaderSize + len(entries)*aclEntrySize
	buf := make([]byte, fixedEnd+fileSecuritySize)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(buf)))
	binary.LittleEndian.PutUint32(buf[4:8], common)
	binary.LittleEndian.PutUint32(buf[aclAttributeDevicePosition:aclAttributeDevicePosition+4], 42)
	binary.LittleEndian.PutUint32(buf[aclAttributeObjectTypePosition:aclAttributeObjectTypePosition+4], aclObjectTypeRegular)
	binary.LittleEndian.PutUint32(buf[aclAttributeOwnerIDPosition:aclAttributeOwnerIDPosition+4], 501)
	binary.LittleEndian.PutUint32(buf[aclAttributeAccessMaskPosition:aclAttributeAccessMaskPosition+4], 0o100600)
	binary.LittleEndian.PutUint32(buf[aclAttributeReferencePosition:aclAttributeReferencePosition+4],
		uint32(fixedEnd-aclAttributeReferencePosition))
	binary.LittleEndian.PutUint32(buf[aclAttributeReferencePosition+4:aclAttributeReferencePosition+8],
		uint32(fileSecuritySize))
	if ownerUUID != nil {
		copy(buf[aclAttributeOwnerUUIDPosition:aclAttributeOwnerUUIDPosition+aclPrincipalSize], ownerUUID[:])
	}
	binary.LittleEndian.PutUint64(buf[aclAttributeFileIDPosition:aclAttributeFileIDPosition+8], 99)

	data := buf[fixedEnd:]
	binary.LittleEndian.PutUint32(data[0:4], aclFileSecurityMagic)
	binary.LittleEndian.PutUint32(data[aclFileSecurityEntryCountOffset:aclFileSecurityEntryCountOffset+4],
		uint32(len(entries)))
	for i, entry := range entries {
		off := aclFileSecurityHeaderSize + i*aclEntrySize
		copy(data[off:off+aclPrincipalSize], entry.principal[:])
		binary.LittleEndian.PutUint32(data[off+aclEntryFlagsOffset:off+aclEntryFlagsOffset+4], entry.flags)
		binary.LittleEndian.PutUint32(data[off+aclEntryRightsOffset:off+aclEntryRightsOffset+4], entry.rights)
	}
	return buf
}

// Darwin keeps the requested variable attribute's eight-byte zero reference
// in the reported size when no ACL exists, while packing later fixed fields as
// if that absent attribute were omitted. This is the measured native shape.
func aclTestNoACLAttributeBuffer() []byte {
	buf := make([]byte, aclAttributeIdentityFixedSize)
	common := uint32(aclAttrReturned | aclAttrDevice | aclAttrObjectType | aclAttrOwnerID |
		aclAttrAccessMask | aclAttrOwnerUUID | aclAttrFileID)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(buf)))
	binary.LittleEndian.PutUint32(buf[4:8], common)
	cursor := aclAttributeFixedHeaderSize
	binary.LittleEndian.PutUint32(buf[cursor:cursor+4], 42)
	cursor += 4
	binary.LittleEndian.PutUint32(buf[cursor:cursor+4], aclObjectTypeRegular)
	cursor += 4
	binary.LittleEndian.PutUint32(buf[cursor:cursor+4], 501)
	cursor += 4
	binary.LittleEndian.PutUint32(buf[cursor:cursor+4], 0o100600)
	cursor += 4 + aclPrincipalSize
	binary.LittleEndian.PutUint64(buf[cursor:cursor+8], 99)
	return buf
}

func TestParseACLAttributeBufferClassifiesOnlyNonOwnerGrants(t *testing.T) {
	ownerUID := uint32(501)
	owner := ownerCompatibilityUUID(ownerUID)
	ownerRecordUUID := [aclPrincipalSize]byte{0x73, 0x2a, 0x91, 0x4c}
	nonOwner := [aclPrincipalSize]byte{0xab, 0xcd, 0xef, 0xab}
	noACL, err := parseACLAttributeBuffer(aclTestNoACLAttributeBuffer(), ownerUID, nil)
	if err != nil {
		t.Fatalf("native no-ACL payload = %v", err)
	}
	if noACL.NonOwnerGrant || noACL.Device != 42 || noACL.ObjectType != aclObjectTypeRegular ||
		noACL.OwnerUID != 501 || noACL.AccessMask != 0o100600 || noACL.FileID != 99 {
		t.Fatalf("native no-ACL snapshot = %+v, want identity with no grant", noACL)
	}

	tests := []struct {
		name      string
		ownerUUID *[aclPrincipalSize]byte
		entries   []aclTestEntry
		wantGrant bool
	}{
		{name: "no ACL entries"},
		{
			name: "non-owner read-only allow grants access",
			entries: []aclTestEntry{{
				principal: nonOwner,
				flags:     aclEntryAllow,
				rights:    aclRightReadData,
			}},
			wantGrant: true,
		},
		{
			name: "non-owner write allow grants access",
			entries: []aclTestEntry{{
				principal: nonOwner,
				flags:     aclEntryAllow,
				rights:    aclRightWriteData,
			}},
			wantGrant: true,
		},
		{
			name: "non-owner deny is not a grant",
			entries: []aclTestEntry{{
				principal: nonOwner,
				flags:     aclEntryDeny,
				rights:    aclRightReadData,
			}},
		},
		{
			name: "compatibility UUID identifies the owner",
			entries: []aclTestEntry{{
				principal: owner,
				flags:     aclEntryAllow,
				rights:    aclRightReadData,
			}},
		},
		{
			name:      "returned record UUID identifies the owner",
			ownerUUID: &ownerRecordUUID,
			entries: []aclTestEntry{{
				principal: ownerRecordUUID,
				flags:     aclEntryAllow,
				rights:    aclRightReadData,
			}},
		},
		{
			name: "zero-right allow grants nothing",
			entries: []aclTestEntry{{
				principal: nonOwner,
				flags:     aclEntryAllow,
			}},
		},
		{
			name: "every entry is inspected",
			entries: []aclTestEntry{
				{principal: owner, flags: aclEntryAllow, rights: aclRightReadData},
				{principal: nonOwner, flags: aclEntryAllow, rights: aclRightReadData},
			},
			wantGrant: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseACLAttributeBuffer(aclTestAttributeBuffer(tt.ownerUUID, tt.entries), ownerUID, nil)
			if err != nil {
				t.Fatalf("parseACLAttributeBuffer() = %v", err)
			}
			if got.NonOwnerGrant != tt.wantGrant {
				t.Fatalf("parseACLAttributeBuffer() grant = %v, want %v", got.NonOwnerGrant, tt.wantGrant)
			}
		})
	}
}

func TestParseACLAttributeBufferResolvesDirectoryServiceOwnerUUID(t *testing.T) {
	ownerUID := uint32(501)
	directoryOwner := [aclPrincipalSize]byte{0x70, 0x21, 0x97, 0xcb}
	buf := aclTestAttributeBuffer(nil, []aclTestEntry{{
		principal: directoryOwner,
		flags:     aclEntryAllow,
		rights:    aclRightReadData,
	}})

	called := 0
	got, err := parseACLAttributeBuffer(buf, ownerUID, func(principal [aclPrincipalSize]byte, uid uint32) (bool, error) {
		called++
		if principal != directoryOwner || uid != ownerUID {
			t.Fatalf("resolver got principal %x, uid %d", principal, uid)
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("directory-service owner UUID = %v", err)
	}
	if got.NonOwnerGrant || called != 1 {
		t.Fatalf("directory-service owner snapshot = %+v, resolver calls = %d", got, called)
	}

	got, err = parseACLAttributeBuffer(buf, ownerUID, func([aclPrincipalSize]byte, uint32) (bool, error) {
		return false, nil
	})
	if err != nil {
		t.Fatalf("non-owner resolution = %v", err)
	}
	if !got.NonOwnerGrant {
		t.Fatal("resolved non-owner allow was accepted")
	}

	lookupErr := errors.New("membership service unavailable")
	if _, err := parseACLAttributeBuffer(buf, ownerUID, func([aclPrincipalSize]byte, uint32) (bool, error) {
		return false, lookupErr
	}); !errors.Is(err, lookupErr) {
		t.Fatalf("resolver error = %v, want %v", err, lookupErr)
	}
}

func TestParseACLAttributeBufferFailsClosedOnAmbiguity(t *testing.T) {
	nonOwner := [aclPrincipalSize]byte{0xab, 0xcd, 0xef, 0xab}
	valid := func() []byte {
		return aclTestAttributeBuffer(nil, []aclTestEntry{{
			principal: nonOwner,
			flags:     aclEntryDeny,
			rights:    aclRightReadData,
		}})
	}
	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{name: "short fixed header", mutate: func(buf []byte) []byte { return buf[:aclAttributeFixedHeaderSize-1] }},
		{name: "reported length is zero", mutate: func(buf []byte) []byte {
			binary.LittleEndian.PutUint32(buf[0:4], 0)
			return buf
		}},
		{name: "reported length exceeds buffer", mutate: func(buf []byte) []byte {
			binary.LittleEndian.PutUint32(buf[0:4], uint32(len(buf)+1))
			return buf
		}},
		{name: "missing returned-attributes marker", mutate: func(buf []byte) []byte {
			binary.LittleEndian.PutUint32(buf[4:8], aclAttrExtendedSecurity)
			return buf
		}},
		{name: "unexpected common attribute", mutate: func(buf []byte) []byte {
			binary.LittleEndian.PutUint32(buf[4:8], binary.LittleEndian.Uint32(buf[4:8])|1)
			return buf
		}},
		{name: "unexpected volume attribute", mutate: func(buf []byte) []byte {
			binary.LittleEndian.PutUint32(buf[8:12], 1)
			return buf
		}},
		{name: "missing object identity", mutate: func(buf []byte) []byte {
			binary.LittleEndian.PutUint32(buf[4:8], aclAttrReturned|aclAttrExtendedSecurity|aclAttrOwnerUUID)
			return buf
		}},
		{name: "negative attribute reference", mutate: func(buf []byte) []byte {
			binary.LittleEndian.PutUint32(buf[aclAttributeReferencePosition:aclAttributeReferencePosition+4], ^uint32(0))
			return buf
		}},
		{name: "attribute reference escapes buffer", mutate: func(buf []byte) []byte {
			binary.LittleEndian.PutUint32(buf[aclAttributeReferencePosition:aclAttributeReferencePosition+4], uint32(len(buf)))
			return buf
		}},
		{name: "nonzero gap before referenced ACL", mutate: func(buf []byte) []byte {
			data := aclAttributeIdentityFixedSize
			withGap := make([]byte, 0, len(buf)+4)
			withGap = append(withGap, buf[:data]...)
			withGap = append(withGap, 1, 0, 0, 0)
			withGap = append(withGap, buf[data:]...)
			binary.LittleEndian.PutUint32(withGap[0:4], uint32(len(withGap)))
			offset := binary.LittleEndian.Uint32(
				withGap[aclAttributeReferencePosition : aclAttributeReferencePosition+4])
			binary.LittleEndian.PutUint32(
				withGap[aclAttributeReferencePosition:aclAttributeReferencePosition+4], offset+4)
			return withGap
		}},
		{name: "bad file-security magic", mutate: func(buf []byte) []byte {
			data := aclAttributeIdentityFixedSize
			binary.LittleEndian.PutUint32(buf[data:data+4], 0)
			return buf
		}},
		{name: "impossible entry count", mutate: func(buf []byte) []byte {
			data := aclAttributeIdentityFixedSize
			binary.LittleEndian.PutUint32(buf[data+aclFileSecurityEntryCountOffset:data+aclFileSecurityEntryCountOffset+4], aclMaxEntries+1)
			return buf
		}},
		{name: "entry count disagrees with length", mutate: func(buf []byte) []byte {
			data := aclAttributeIdentityFixedSize
			binary.LittleEndian.PutUint32(buf[data+aclFileSecurityEntryCountOffset:data+aclFileSecurityEntryCountOffset+4], 2)
			return buf
		}},
		{name: "unknown entry kind", mutate: func(buf []byte) []byte {
			data := aclAttributeIdentityFixedSize
			entry := data + aclFileSecurityHeaderSize
			binary.LittleEndian.PutUint32(buf[entry+aclEntryFlagsOffset:entry+aclEntryFlagsOffset+4], 3)
			return buf
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseACLAttributeBuffer(tt.mutate(valid()), 501, nil); err == nil {
				t.Fatal("parseACLAttributeBuffer() = nil error, want ambiguity rejection")
			}
		})
	}

	oversizedNoACL := append(aclTestNoACLAttributeBuffer(), make([]byte, 4)...)
	binary.LittleEndian.PutUint32(oversizedNoACL[0:4], uint32(len(oversizedNoACL)))
	if _, err := parseACLAttributeBuffer(oversizedNoACL, 501, nil); err == nil {
		t.Fatal("oversized zero-filled no-ACL payload accepted")
	}
}

func TestValidateACLTrustBindsIdentityAndGrant(t *testing.T) {
	clean := aclAttributeSnapshot{
		Device:     42,
		ObjectType: aclObjectTypeRegular,
		OwnerUID:   501,
		AccessMask: 0o100600,
		FileID:     99,
	}
	if err := validateACLTrust(clean, 42, 99, aclObjectTypeRegular, 501); err != nil {
		t.Fatalf("matching clean snapshot = %v", err)
	}
	for _, mutate := range []func(aclAttributeSnapshot) aclAttributeSnapshot{
		func(s aclAttributeSnapshot) aclAttributeSnapshot { s.Device++; return s },
		func(s aclAttributeSnapshot) aclAttributeSnapshot { s.FileID++; return s },
		func(s aclAttributeSnapshot) aclAttributeSnapshot { s.ObjectType = aclObjectTypeDirectory; return s },
		func(s aclAttributeSnapshot) aclAttributeSnapshot { s.OwnerUID++; return s },
		func(s aclAttributeSnapshot) aclAttributeSnapshot { s.AccessMask |= 0o020; return s },
		func(s aclAttributeSnapshot) aclAttributeSnapshot { s.NonOwnerGrant = true; return s },
	} {
		snapshot := mutate(clean)
		if err := validateACLTrust(snapshot, 42, 99, aclObjectTypeRegular, 501); err == nil {
			t.Fatalf("validateACLTrust(%+v) = nil, want rejection", snapshot)
		}
	}
}

func TestParseACLVolumeAttributesRequiresReportedSupport(t *testing.T) {
	if err := parseACLVolumeAttributes(aclVolumeAttributeWords{
		Length: aclVolumeAttributeBufferSize,
		Device: 42,
		Valid:  [aclAttributeGroupCount]uint32{aclAttrDevice | aclAttrExtendedSecurity},
	}, 42); err != nil {
		t.Fatalf("supported volume = %v", err)
	}
	for _, words := range []aclVolumeAttributeWords{
		{Length: aclVolumeAttributeBufferSize - 1, Device: 42, Valid: [aclAttributeGroupCount]uint32{aclAttrDevice | aclAttrExtendedSecurity}},
		{Length: aclVolumeAttributeBufferSize, Device: 42},
		{Length: aclVolumeAttributeBufferSize, Device: 42, Valid: [aclAttributeGroupCount]uint32{aclAttrExtendedSecurity}},
		{Length: aclVolumeAttributeBufferSize, Device: 43, Valid: [aclAttributeGroupCount]uint32{aclAttrDevice | aclAttrExtendedSecurity}},
	} {
		if err := parseACLVolumeAttributes(words, 42); err == nil {
			t.Fatalf("parseACLVolumeAttributes(%+v) = nil, want fail closed", words)
		}
	}
}
