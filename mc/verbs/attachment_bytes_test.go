package verbs

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// attachmentBytesFixture publishes one real inbound attachment through the
// production publisher and records the conversation claim that carries it,
// returning the exact address `mc collie history` would expose.
func attachmentBytesFixture(
	t *testing.T, mediaType, displayName, body string,
) (*sql.DB, string, AttachmentBytesArgs) {
	t.Helper()
	manifest := fmt.Sprintf(`[{"ordinal":1,"size":%d,"media_type":%q,"display_name":%q}]`,
		len(body), mediaType, displayName)
	db, session, reservation, home := attachmentPublisherFixture(t, manifest)
	published, err := AttachmentPublicationPublish(
		db, nil, home, publishArgs(session, reservation, 1), bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	insertAttachmentClaimMessage(t, db, session, "in",
		published.SHA256, int64(len(body)), mediaType, displayName)
	return db, home, AttachmentBytesArgs{
		Session: session, Direction: "in",
		SHA256: published.SHA256, Size: int64(len(body)),
	}
}

// insertAttachmentClaimMessage appends a conversation turn carrying one typed
// attachment reference. Direction "in" claims ride an inbound turn; "out"
// claims ride a reply, which the triggers require to answer an inbound turn
// of the same conversation.
func insertAttachmentClaimMessage(
	t *testing.T, db *sql.DB, session, direction, digest string,
	size int64, mediaType, displayName string,
) int64 {
	t.Helper()
	var seq int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(seq),0)+1
		FROM conversation_messages WHERE session_id=?`, session).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	var inserted sql.Result
	var err error
	if direction == "in" {
		inserted, err = db.Exec(`INSERT INTO conversation_messages
			(session_id,seq,direction,surface,channel_ref,body,attachments)
			VALUES (?,?,'inbound','discord','channel-1','attachment turn',NULL)`,
			session, seq)
	} else {
		turn, turnErr := db.Exec(`INSERT INTO conversation_messages
			(session_id,seq,direction,surface,channel_ref,body,attachments)
			VALUES (?,?,'inbound','discord','channel-1','turn to answer',NULL)`,
			session, seq)
		if turnErr != nil {
			t.Fatal(turnErr)
		}
		turnID, turnErr := turn.LastInsertId()
		if turnErr != nil {
			t.Fatal(turnErr)
		}
		inserted, err = db.Exec(`INSERT INTO conversation_messages
			(session_id,seq,direction,surface,channel_ref,body,attachments,reply_to)
			VALUES (?,?,'reply','discord','channel-1','reply with attachment',NULL,?)`,
			session, seq+1, turnID)
	}
	if err != nil {
		t.Fatal(err)
	}
	messageID, err := inserted.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO conversation_attachment_refs
		(message_id,ordinal,session_id,direction,sha256,size,media_type,display_name)
		VALUES (?,1,?,?,?,?,?,?)`,
		messageID, session, direction, digest, size, mediaType, displayName); err != nil {
		t.Fatal(err)
	}
	return messageID
}

func attachmentBytesRead(
	t *testing.T, db *sql.DB, home string, a AttachmentBytesArgs,
) (map[string]any, error) {
	t.Helper()
	claim, err := AttachmentBytesClaim(db, a)
	if err != nil {
		return nil, err
	}
	return AttachmentBytesServe(home, a, claim)
}

func TestAttachmentBytesArgsRefuseEverythingButClosedIdentifiers(t *testing.T) {
	valid := AttachmentBytesArgs{
		Session: "h-session", Direction: "in",
		SHA256: strings.Repeat("a", 64), Size: 7,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid address refused: %v", err)
	}
	mutations := map[string]func(*AttachmentBytesArgs){
		"empty session":         func(a *AttachmentBytesArgs) { a.Session = "" },
		"traversal session":     func(a *AttachmentBytesArgs) { a.Session = ".." },
		"nested traversal":      func(a *AttachmentBytesArgs) { a.Session = "../h-session" },
		"separator session":     func(a *AttachmentBytesArgs) { a.Session = "h/session" },
		"absolute session":      func(a *AttachmentBytesArgs) { a.Session = "/etc/passwd" },
		"backslash session":     func(a *AttachmentBytesArgs) { a.Session = `h\session` },
		"control session":       func(a *AttachmentBytesArgs) { a.Session = "h\nsession" },
		"overlong session":      func(a *AttachmentBytesArgs) { a.Session = strings.Repeat("h", 65) },
		"empty direction":       func(a *AttachmentBytesArgs) { a.Direction = "" },
		"unknown direction":     func(a *AttachmentBytesArgs) { a.Direction = "up" },
		"uppercase direction":   func(a *AttachmentBytesArgs) { a.Direction = "IN" },
		"short digest":          func(a *AttachmentBytesArgs) { a.SHA256 = strings.Repeat("a", 63) },
		"non-hex digest":        func(a *AttachmentBytesArgs) { a.SHA256 = strings.Repeat("g", 64) },
		"uppercase digest":      func(a *AttachmentBytesArgs) { a.SHA256 = strings.Repeat("A", 64) },
		"path-shaped digest":    func(a *AttachmentBytesArgs) { a.SHA256 = "../" + strings.Repeat("a", 61) },
		"negative size":         func(a *AttachmentBytesArgs) { a.Size = -1 },
		"size beyond the spine": func(a *AttachmentBytesArgs) { a.Size = maxAttachmentBytes + 1 },
	}
	for name, mutate := range mutations {
		a := valid
		mutate(&a)
		err := a.Validate()
		if _, usage := err.(*UsageError); !usage {
			t.Fatalf("%s: expected usage refusal, got %v", name, err)
		}
	}
}

func TestAttachmentBytesClaimResolvesFromSpineRowsOnly(t *testing.T) {
	db, home, address := attachmentBytesFixture(t, "text/plain", "seven.txt", "1234567")

	claim, err := AttachmentBytesClaim(db, address)
	if err != nil || claim.MediaType != "text/plain" || claim.DisplayName != "seven.txt" {
		t.Fatalf("claim = %#v err %v", claim, err)
	}

	unknown := address
	unknown.SHA256 = strings.Repeat("0", 64)
	if _, err := AttachmentBytesClaim(db, unknown); err == nil ||
		!strings.Contains(err.Error(), "no conversation attachment claim") {
		t.Fatalf("unknown digest = %v", err)
	}

	foreign := address
	foreign.Session = "h-someone-else"
	if _, err := AttachmentBytesClaim(db, foreign); err == nil ||
		!strings.Contains(err.Error(), "no conversation attachment claim") {
		t.Fatalf("foreign session = %v", err)
	}

	wrongSize := address
	wrongSize.Size = address.Size + 1
	if _, err := AttachmentBytesClaim(db, wrongSize); err == nil ||
		!strings.Contains(err.Error(), "no conversation attachment claim") {
		t.Fatalf("wrong size = %v", err)
	}

	wrongDirection := address
	wrongDirection.Direction = "out"
	if _, err := AttachmentBytesClaim(db, wrongDirection); err == nil ||
		!strings.Contains(err.Error(), "no conversation attachment claim") {
		t.Fatalf("wrong direction = %v", err)
	}
	_ = home
}

// A live object no conversation references is publisher residue, not a claim:
// the read refuses it exactly like an unknown identity.
func TestAttachmentBytesClaimRequiresAConversationReference(t *testing.T) {
	const manifest = `[{"ordinal":1,"size":8,"media_type":"text/plain","display_name":"loose.txt"}]`
	db, session, reservation, home := attachmentPublisherFixture(t, manifest)
	published, err := AttachmentPublicationPublish(
		db, nil, home, publishArgs(session, reservation, 1), bytes.NewReader([]byte("12345678")))
	if err != nil {
		t.Fatal(err)
	}
	address := AttachmentBytesArgs{
		Session: session, Direction: "in", SHA256: published.SHA256, Size: 8,
	}
	if _, err := AttachmentBytesClaim(db, address); err == nil ||
		!strings.Contains(err.Error(), "no conversation attachment claim") {
		t.Fatalf("unreferenced object = %v", err)
	}
}

// The verb's live-state check is defense in depth: storage itself refuses to
// move a conversation-referenced object out of `live`, so a
// claim the read confirms is an object GC cannot have released.
func TestAttachmentBytesClaimedObjectCannotLeaveLive(t *testing.T) {
	db, _, address := attachmentBytesFixture(t, "text/plain", "seven.txt", "1234567")
	if _, err := db.Exec(`UPDATE attachment_objects
		SET state='delete_pending', gc_generation=gc_generation+1
		WHERE session_id=? AND direction='in' AND sha256=? AND size=?`,
		address.Session, address.SHA256, address.Size); err == nil ||
		!strings.Contains(err.Error(), "liveness evidence") {
		t.Fatalf("referenced object left live: %v", err)
	}
	if _, err := AttachmentBytesClaim(db, address); err != nil {
		t.Fatalf("claim after refused release = %v", err)
	}
}

func TestAttachmentBytesServeMarksInlineByMediaTypeAllowlist(t *testing.T) {
	for _, tc := range []struct {
		mediaType string
		inline    bool
	}{
		{"image/png", true},
		{"image/webp", true},
		{"IMAGE/JPEG", true},               // case cannot dodge the allowlist
		{"image/png; charset=utf-8", true}, // parameters cannot either way
		{"image/svg+xml", false},           // SVG can script; download-only
		{"application/pdf", false},
		{"text/html", false},
	} {
		body := "bytes for " + tc.mediaType
		db, home, address := attachmentBytesFixture(t, tc.mediaType, "chip.bin", body)
		result, err := attachmentBytesRead(t, db, home, address)
		if err != nil {
			t.Fatalf("%s: %v", tc.mediaType, err)
		}
		if result["inline"] != tc.inline {
			t.Fatalf("%s: inline = %v, want %v", tc.mediaType, result["inline"], tc.inline)
		}
		if result["media_type"] != tc.mediaType || result["display_name"] != "chip.bin" ||
			result["sha256"] != address.SHA256 || result["size"] != address.Size ||
			result["session_id"] != address.Session || result["direction"] != "in" {
			t.Fatalf("%s: metadata = %#v", tc.mediaType, result)
		}
		expected := filepath.Join(home, "attachments", address.Session, "in",
			"published", "sha256", address.SHA256[:2], address.SHA256)
		if result["host_path"] != expected {
			t.Fatalf("%s: host_path = %v, want %v", tc.mediaType, result["host_path"], expected)
		}
		served, err := os.ReadFile(result["host_path"].(string))
		if err != nil || string(served) != body {
			t.Fatalf("%s: served bytes = %q err %v", tc.mediaType, served, err)
		}
	}
}

func TestAttachmentBytesServeRefusesOverCapClaims(t *testing.T) {
	db, home, address := attachmentBytesFixture(t, "video/mp4", "movie.mp4", "stub")
	oversize := maxAttachmentServeBytes + 1
	digest := strings.Repeat("b", 64)
	if _, err := db.Exec(`INSERT INTO attachment_objects
		(session_id,direction,sha256,size,state,device,inode,file_type)
		VALUES (?,'in',?,?,'live','1','1','regular')`,
		address.Session, digest, oversize); err != nil {
		t.Fatal(err)
	}
	insertAttachmentClaimMessage(t, db, address.Session, "in",
		digest, oversize, "video/mp4", "movie.mp4")
	over := AttachmentBytesArgs{
		Session: address.Session, Direction: "in", SHA256: digest, Size: oversize,
	}
	_, err := attachmentBytesRead(t, db, home, over)
	if err == nil || !strings.Contains(err.Error(), "serve cap") {
		t.Fatalf("over-cap read = %v", err)
	}
}

func TestAttachmentBytesServeRefusesTamperedOrRedirectedBytes(t *testing.T) {
	db, home, address := attachmentBytesFixture(t, "image/png", "pixel.png", "1234567")
	canonical := filepath.Join(home, "attachments", address.Session, "in",
		"published", "sha256", address.SHA256[:2], address.SHA256)

	// Same length, different content: the digest disagrees.
	if err := os.Chmod(canonical, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonical, []byte("7654321"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := attachmentBytesRead(t, db, home, address); err == nil ||
		!strings.Contains(err.Error(), "recorded digest") {
		t.Fatalf("tampered content = %v", err)
	}

	// Different length: the size disagrees before any hashing.
	if err := os.WriteFile(canonical, []byte("123"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := attachmentBytesRead(t, db, home, address); err == nil ||
		!strings.Contains(err.Error(), "recorded size") {
		t.Fatalf("truncated file = %v", err)
	}

	// A symlink leaf — even one pointing at the exact bytes — is refused: the
	// outbound tree is agent-writable in its container, so a followed link
	// would widen the read beyond the attachment plane.
	aside := filepath.Join(t.TempDir(), "aside")
	if err := os.WriteFile(aside, []byte("1234567"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(canonical); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(aside, canonical); err != nil {
		t.Fatal(err)
	}
	if _, err := attachmentBytesRead(t, db, home, address); err == nil ||
		!strings.Contains(err.Error(), "not materialized") {
		t.Fatalf("symlink leaf = %v", err)
	}

	// A symlinked directory component is refused the same way.
	if err := os.Remove(canonical); err != nil {
		t.Fatal(err)
	}
	shard := filepath.Dir(canonical)
	if err := os.Remove(shard); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Mkdir(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, address.SHA256), []byte("1234567"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, shard); err != nil {
		t.Fatal(err)
	}
	if _, err := attachmentBytesRead(t, db, home, address); err == nil ||
		!strings.Contains(err.Error(), "not materialized") {
		t.Fatalf("symlink shard = %v", err)
	}

	// Gone entirely: refused, never re-derived from anywhere else.
	if err := os.Remove(shard); err != nil {
		t.Fatal(err)
	}
	if _, err := attachmentBytesRead(t, db, home, address); err == nil ||
		!strings.Contains(err.Error(), "not materialized") {
		t.Fatalf("missing file = %v", err)
	}
}

func TestAttachmentBytesServesTheOutboundPublishedTree(t *testing.T) {
	db, home, inbound := attachmentBytesFixture(t, "text/plain", "seven.txt", "1234567")
	session := inbound.Session
	body := []byte("outbound bytes")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	shard := filepath.Join(home, "attachments", session, "out",
		"published", "sha256", digest[:2])
	if err := os.Mkdir(shard, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shard, digest), body, 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO attachment_objects
		(session_id,direction,sha256,size,state,device,inode,file_type)
		VALUES (?,'out',?,?,'live','1','2','regular')`,
		session, digest, len(body)); err != nil {
		t.Fatal(err)
	}
	insertAttachmentClaimMessage(t, db, session, "out",
		digest, int64(len(body)), "text/plain", "notes.txt")
	address := AttachmentBytesArgs{
		Session: session, Direction: "out", SHA256: digest, Size: int64(len(body)),
	}
	result, err := attachmentBytesRead(t, db, home, address)
	if err != nil {
		t.Fatal(err)
	}
	if result["direction"] != "out" || result["display_name"] != "notes.txt" ||
		result["host_path"] != filepath.Join(shard, digest) {
		t.Fatalf("outbound read = %#v", result)
	}
}

// The transcript read exposes the typed claim identity the byte read accepts:
// nothing about a chip's address has to be derived outside the spine.
func TestCollieHistorySurfacesTypedAttachmentClaims(t *testing.T) {
	db, _, address := attachmentBytesFixture(t, "image/png", "capture.png", "1234567")
	out, err := CollieHistory(db, nil, address.Session, false)
	if err != nil {
		t.Fatal(err)
	}
	messages := out.(map[string]any)["messages"].([]map[string]any)
	if len(messages) == 0 {
		t.Fatal("transcript is empty")
	}
	last := messages[len(messages)-1]
	claims, ok := last["attachments"].([]CollieAttachmentClaim)
	if !ok || len(claims) != 1 {
		t.Fatalf("attachments = %#v", last["attachments"])
	}
	claim := claims[0]
	if claim.SessionID != address.Session || claim.Direction != "in" ||
		claim.SHA256 != address.SHA256 || claim.Size != address.Size ||
		claim.MediaType != "image/png" || claim.DisplayName != "capture.png" ||
		claim.Ordinal != 1 {
		t.Fatalf("claim = %#v, address = %#v", claim, address)
	}
}
