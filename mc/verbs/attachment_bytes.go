package verbs

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// The byte-serving read is bounded like the packet render read
// (maxPacketRenderBytes): generous enough for any conversational attachment,
// small enough that one claim cannot flood the reader. The spine admits
// objects up to maxAttachmentBytes, so an over-cap claim is a real state the
// verb must refuse distinctly rather than serve.
const maxAttachmentServeBytes = int64(64 << 20)

// attachmentInlineMediaTypes marks the claim media types a dashboard may
// render inline: safe raster images only. image/svg+xml is deliberately
// absent — an SVG document can carry script, so it is served download-only
// like every other non-raster type.
var attachmentInlineMediaTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// AttachmentBytesArgs addresses one conversation attachment claim by the
// spine's own canonical identity — the attachment_objects primary key
// (session_id, direction, sha256, size), exactly as `mc homie
// history` exposes it. It deliberately carries no path: every field is a
// closed identifier, and the on-disk location is derived from MC_HOME plus
// the confirmed spine row alone.
type AttachmentBytesArgs struct {
	Session   string `json:"session"`
	Direction string `json:"direction"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
}

// Validate is the argument fence: nothing that is not a well-formed closed
// identifier may proceed, so no caller-supplied string with a separator, a
// traversal step, or an absolute prefix ever reaches a filesystem join.
func (a AttachmentBytesArgs) Validate() error {
	if !safeAttachmentPathSegment(a.Session) {
		return Usagef("attachment session id is not an exact canonical id")
	}
	if a.Direction != "in" && a.Direction != "out" {
		return Usagef("attachment direction must be \"in\" or \"out\"")
	}
	if !safeAttachmentHex(a.SHA256, 64) {
		return Usagef("attachment sha256 must be 64 lowercase hex characters")
	}
	if a.Size < 0 || a.Size > maxAttachmentBytes {
		return Usagef("attachment size must be 0..%d bytes", maxAttachmentBytes)
	}
	return nil
}

// AttachmentClaimRecord is what the spine says about a confirmed claim — the
// declared metadata a serving surface needs beyond the identity it already
// holds.
type AttachmentClaimRecord struct {
	MediaType   string `json:"media_type"`
	DisplayName string `json:"display_name"`
}

// AttachmentBytesClaim confirms the addressed identity against spine rows
// alone: a live attachment object that at least one conversation message
// references. An object no conversation carries is publisher
// residue, not a claim, and is refused the same way an unknown identity is.
// Several messages may reference one object; the earliest reference's
// declared metadata answers, deterministically.
func AttachmentBytesClaim(db *sql.DB, a AttachmentBytesArgs) (AttachmentClaimRecord, error) {
	var record AttachmentClaimRecord
	if err := a.Validate(); err != nil {
		return record, err
	}
	var state string
	err := db.QueryRow(`
		SELECT o.state, r.media_type, r.display_name
		FROM attachment_objects o
		JOIN conversation_attachment_refs r
		  ON r.session_id=o.session_id AND r.direction=o.direction
		 AND r.sha256=o.sha256 AND r.size=o.size
		WHERE o.session_id=? AND o.direction=? AND o.sha256=? AND o.size=?
		ORDER BY r.message_id, r.ordinal LIMIT 1`,
		a.Session, a.Direction, a.SHA256, a.Size,
	).Scan(&state, &record.MediaType, &record.DisplayName)
	if err == sql.ErrNoRows {
		return record, Domainf("no conversation attachment claim matches that identity")
	}
	if err != nil {
		return record, classify(err)
	}
	if state != "live" || !safeMediaType(record.MediaType) || !safeDisplayName(record.DisplayName) {
		return record, Domainf("conversation attachment claim is no longer live")
	}
	return record, nil
}

// AttachmentBytesServe resolves a spine-confirmed claim to its published host
// file and verifies that what is on disk is byte-for-byte the recorded
// identity — exact size, exact digest — before naming it. The path is built
// only from MC_HOME and the validated identity the spine row confirmed; the
// walk refuses symlinks at every component, because the outbound direction's
// tree is agent-writable inside its container and a planted link must not
// widen this read beyond the attachment plane.
func AttachmentBytesServe(home string, a AttachmentBytesArgs, claim AttachmentClaimRecord) (map[string]any, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	if a.Size > maxAttachmentServeBytes {
		return nil, Domainf("attachment claim of %d bytes exceeds the %d-byte serve cap", a.Size, maxAttachmentServeBytes)
	}
	hostPath, err := verifyPublishedAttachment(home, a)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"session_id":   a.Session,
		"direction":    a.Direction,
		"sha256":       a.SHA256,
		"size":         a.Size,
		"media_type":   claim.MediaType,
		"display_name": claim.DisplayName,
		"inline":       attachmentInlineMediaTypes[bareMediaType(claim.MediaType)],
		"host_path":    hostPath,
	}, nil
}

// bareMediaType reduces a declared media type to the bare lowercase
// type/subtype the inline allowlist is keyed on: parameters cannot buy a
// non-raster type an inline render.
func bareMediaType(v string) string {
	bare, _, _ := strings.Cut(v, ";")
	return strings.ToLower(strings.TrimSpace(bare))
}

// verifyPublishedAttachment walks MC_HOME to the canonical published file —
// attachments/<session>/<direction>/published/sha256/<aa>/<digest> — with
// O_NOFOLLOW at every step, requires the publisher's directory discipline on
// the way down, and hashes the open descriptor so the answer names bytes that
// verifiably ARE the recorded identity, not merely a file where they should be.
func verifyPublishedAttachment(home string, a AttachmentBytesArgs) (string, error) {
	absolute, err := filepath.Abs(home)
	if err != nil || filepath.Clean(absolute) != absolute {
		return "", Usagef("resolve attachment MC_HOME: %v", err)
	}
	segments := []string{"attachments", a.Session, a.Direction, "published", "sha256", a.SHA256[:2]}
	fd, err := unix.Open(absolute,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", Domainf("open attachment MC_HOME: %v", err)
	}
	if err := requireAttachmentDirectory(fd, "attachment MC_HOME"); err != nil {
		_ = unix.Close(fd)
		return "", classify(err)
	}
	for _, segment := range segments {
		next, err := unix.Openat(fd, segment,
			unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if err != nil {
			return "", Domainf("attachment bytes are not materialized in this deployment's attachment tree")
		}
		if err := requireAttachmentDirectory(next, "attachment tree directory"); err != nil {
			_ = unix.Close(next)
			return "", classify(err)
		}
		fd = next
	}
	leaf, err := unix.Openat(fd, a.SHA256,
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	_ = unix.Close(fd)
	if err != nil {
		return "", Domainf("attachment bytes are not materialized in this deployment's attachment tree")
	}
	file := os.NewFile(uintptr(leaf), a.SHA256)
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(leaf, &stat); err != nil {
		return "", classify(err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || int(stat.Uid) != os.Getuid() {
		return "", Domainf("published attachment is not an operator-owned regular file")
	}
	if stat.Size != a.Size {
		return "", Domainf("published attachment bytes disagree with the recorded size")
	}
	hash := sha256.New()
	copied, err := io.Copy(hash, io.LimitReader(file, a.Size))
	if err != nil {
		return "", classify(err)
	}
	if copied != a.Size || hex.EncodeToString(hash.Sum(nil)) != a.SHA256 {
		return "", Domainf("published attachment bytes disagree with the recorded digest")
	}
	parts := append([]string{absolute}, segments...)
	return filepath.Join(append(parts, a.SHA256)...), nil
}
