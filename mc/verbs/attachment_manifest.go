package verbs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	maxAttachmentObjects       = 32
	maxAttachmentBytes         = int64(256 << 20)
	maxAttachmentBatch         = int64(512 << 20)
	maxAttachmentManifestBytes = 64 << 10
)

// AttachmentManifestEntry is the metadata sealed before any file-plane byte
// is staged. It deliberately contains no path: consumers identify the later
// object by session, direction, digest, and size.
type AttachmentManifestEntry struct {
	Ordinal     int    `json:"ordinal"`
	Size        int64  `json:"size"`
	MediaType   string `json:"media_type"`
	DisplayName string `json:"display_name"`
}

// AttachmentManifest is the canonical serialized declaration plus its stable
// SHA-256 identity. Its entries always use contiguous ordinals 1..n.
type AttachmentManifest struct {
	Entries   []AttachmentManifestEntry
	Canonical string
	Digest    string
}

// ParseAttachmentManifest accepts only a complete bounded declaration and
// returns its deterministic serialization. A later publisher may add the
// content digest for an ordinal, but cannot change any declared metadata.
func ParseAttachmentManifest(raw string) (AttachmentManifest, error) {
	if len(raw) == 0 || len(raw) > maxAttachmentManifestBytes {
		return AttachmentManifest{}, Usagef("attachment manifest must be between 1 and %d bytes", maxAttachmentManifestBytes)
	}
	dec := json.NewDecoder(bytes.NewBufferString(raw))
	dec.DisallowUnknownFields()
	var entries []AttachmentManifestEntry
	if err := dec.Decode(&entries); err != nil || entries == nil {
		return AttachmentManifest{}, Usagef("attachment manifest must be a JSON array: %v", err)
	}
	if err := ensureManifestEOF(dec); err != nil {
		return AttachmentManifest{}, Usagef("attachment manifest has trailing data")
	}
	if len(entries) == 0 || len(entries) > maxAttachmentObjects {
		return AttachmentManifest{}, Usagef("attachment manifest must declare 1..%d objects", maxAttachmentObjects)
	}
	var total int64
	for i, entry := range entries {
		if entry.Ordinal != i+1 {
			return AttachmentManifest{}, Usagef("attachment manifest ordinals must be contiguous 1..n")
		}
		if entry.Size < 0 || entry.Size > maxAttachmentBytes || total > maxAttachmentBatch-entry.Size {
			return AttachmentManifest{}, Usagef("attachment manifest exceeds the %d-byte object or %d-byte batch limit", maxAttachmentBytes, maxAttachmentBatch)
		}
		total += entry.Size
		if !safeMediaType(entry.MediaType) {
			return AttachmentManifest{}, Usagef("attachment media_type must be 1..255 printable bytes")
		}
		if !safeDisplayName(entry.DisplayName) {
			return AttachmentManifest{}, Usagef("attachment display_name must be a 1..255-byte basename without controls")
		}
	}
	canonical, err := json.Marshal(entries)
	if err != nil {
		return AttachmentManifest{}, fmt.Errorf("encode attachment manifest: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return AttachmentManifest{Entries: entries, Canonical: string(canonical), Digest: hex.EncodeToString(digest[:])}, nil
}

func ensureManifestEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err == io.EOF {
		return nil
	}
	return fmt.Errorf("extra JSON value")
}

func safeMediaType(v string) bool {
	if len(v) == 0 || len(v) > 255 || !utf8.ValidString(v) {
		return false
	}
	for _, r := range v {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}

func safeDisplayName(v string) bool {
	if len(v) == 0 || len(v) > 255 || !utf8.ValidString(v) || strings.ContainsAny(v, "/\\") {
		return false
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
