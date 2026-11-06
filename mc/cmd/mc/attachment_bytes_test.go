package main_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc/verbs"
)

// attachmentBytesCLIFixture stands up a spine with one Collie conversation
// whose inbound turn claims a single published attachment, and returns the
// session id plus the claim's canonical digest. Bytes go through the real
// publisher, so the on-disk tree is exactly what production materializes.
func attachmentBytesCLIFixture(
	t *testing.T, spine, mediaType, displayName, body string,
) (string, string) {
	t.Helper()
	started := runMC(t, spineEnv(spine), "", "collie", "start",
		"--from", "discord:bytes-channel")
	if started.code != 0 {
		t.Fatalf("collie start failed: %s", started.stderr)
	}
	session := started.json["session_id"].(string)
	db := openDB(t, spine)
	binding := queryInt(t, db, `SELECT id FROM collie_bindings
		WHERE session_id=? AND surface='discord' AND active=1`, session)
	manifest := `[{"ordinal":1,"size":` + strconv.Itoa(len(body)) +
		`,"media_type":"` + mediaType + `","display_name":"` + displayName + `"}]`
	if _, err := verbs.AttachmentIngressEnqueue(db, nil, verbs.AttachmentIngressEnqueueArgs{
		Session: session, BindingID: int64(binding), Surface: "discord",
		ExternalMessageID: "bytes-message-1", Body: "file follows", Manifest: manifest,
	}); err != nil {
		t.Fatal(err)
	}
	reservation, err := verbs.AttachmentIngressReserve(db, nil, verbs.AttachmentIngressReserveArgs{
		Session: session, OwnerGeneration: "cli-bytes-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	publication := reservation.Publications[0]
	published, err := verbs.AttachmentPublicationPublish(db, nil, filepath.Dir(spine),
		verbs.AttachmentPublicationPublishArgs{
			Session: session, AttemptID: reservation.AttemptID,
			AttemptOwnerToken:     reservation.OwnerToken,
			PublicationID:         publication.PublicationID,
			PublicationOwnerToken: publication.OwnerToken,
			OwnerGeneration:       "cli-bytes-1",
		}, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	message, err := db.Exec(`INSERT INTO conversation_messages
		(session_id,seq,direction,surface,channel_ref,body,attachments)
		VALUES (?,1,'inbound','discord','bytes-channel','claim turn',NULL)`, session)
	if err != nil {
		t.Fatal(err)
	}
	messageID, err := message.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO conversation_attachment_refs
		(message_id,ordinal,session_id,direction,sha256,size,media_type,display_name)
		VALUES (?,1,?,'in',?,?,?,?)`,
		messageID, session, published.SHA256, len(body), mediaType, displayName); err != nil {
		t.Fatal(err)
	}
	return session, published.SHA256
}

func TestAttachmentBytesCLIServesOnlySpineConfirmedClaims(t *testing.T) {
	spine := initSpine(t)
	const body = "png bytes here"
	session, digest := attachmentBytesCLIFixture(t, spine, "image/png", "capture.png", body)
	size := strconv.Itoa(len(body))

	served := runMC(t, spineEnv(spine), "", "__attachment-bytes",
		session, "in", digest, size)
	if served.code != 0 {
		t.Fatalf("serve = code %d stderr %q", served.code, served.stderr)
	}
	if served.json["inline"] != true || served.json["media_type"] != "image/png" ||
		served.json["display_name"] != "capture.png" ||
		served.json["sha256"] != digest || served.json["size"] != float64(len(body)) ||
		served.json["session_id"] != session || served.json["direction"] != "in" {
		t.Fatalf("serve metadata = %v", served.json)
	}
	// The CLI answers with the CANONICAL home (deployment.CanonicalHome), so
	// the expectation must resolve the temp dir's /var symlink the same way.
	canonicalHome, err := filepath.EvalSymlinks(filepath.Dir(spine))
	if err != nil {
		t.Fatal(err)
	}
	hostPath, ok := served.json["host_path"].(string)
	if !ok || hostPath != filepath.Join(canonicalHome, "attachments",
		session, "in", "published", "sha256", digest[:2], digest) {
		t.Fatalf("host_path = %v", served.json["host_path"])
	}
	fromDisk, err := os.ReadFile(hostPath)
	if err != nil || string(fromDisk) != body {
		t.Fatalf("served bytes = %q err %v", fromDisk, err)
	}

	// The transcript read exposes the exact address the byte read accepted.
	history := runMC(t, spineEnv(spine), "", "collie", "history", session)
	if history.code != 0 {
		t.Fatalf("history = code %d stderr %q", history.code, history.stderr)
	}
	messages := history.json["messages"].([]any)
	claim := messages[len(messages)-1].(map[string]any)["attachments"].([]any)[0].(map[string]any)
	if claim["session_id"] != session || claim["direction"] != "in" ||
		claim["sha256"] != digest || claim["size"] != float64(len(body)) ||
		claim["media_type"] != "image/png" || claim["display_name"] != "capture.png" {
		t.Fatalf("history claim = %v", claim)
	}

	// Malformed addresses are usage refusals before anything touches disk.
	for name, args := range map[string][]string{
		"traversal session": {"../h-x", "in", digest, size},
		"absolute session":  {"/etc/passwd", "in", digest, size},
		"bad direction":     {session, "up", digest, size},
		"non-hex digest":    {session, "in", strings.Repeat("z", 64), size},
		"bad size":          {session, "in", digest, "seven"},
		"missing argument":  {session, "in", digest},
	} {
		refused := runMC(t, spineEnv(spine), "", append([]string{"__attachment-bytes"}, args...)...)
		if refused.code != 2 {
			t.Fatalf("%s = code %d json %v", name, refused.code, refused.json)
		}
		envelope := refused.json["error"].(map[string]any)
		if envelope["code"] != "usage" {
			t.Fatalf("%s envelope = %v", name, refused.json)
		}
	}

	// A well-formed address no spine row confirms is a domain refusal.
	unknown := runMC(t, spineEnv(spine), "", "__attachment-bytes",
		session, "in", strings.Repeat("0", 64), size)
	if unknown.code != 1 || !strings.Contains(unknown.stderr, "no conversation attachment claim") {
		t.Fatalf("unknown claim = code %d stderr %q", unknown.code, unknown.stderr)
	}

	// Host scope only: a run.json caller is refused on identity.
	agent := runMC(t, runJSONEnv(t, spine, "r-bytes-agent", "agent", "Worker"), "",
		"__attachment-bytes", session, "in", digest, size)
	if agent.code != 1 || !strings.Contains(agent.stderr, "host scope only") {
		t.Fatalf("agent caller = code %d stderr %q", agent.code, agent.stderr)
	}
}

func TestAttachmentBytesSpineFrameConfirmsClaimsWithoutFileAuthority(t *testing.T) {
	spine := initSpine(t)
	const body = "framed bytes"
	session, digest := attachmentBytesCLIFixture(t, spine, "application/pdf", "notes.pdf", body)
	// Frames must be the closed canonical encoding, so they are built from the
	// exact request struct rather than a map.
	frame := attachmentPrivateFrame(t, verbs.AttachmentBytesArgs{
		Session: session, Direction: "in", SHA256: digest, Size: int64(len(body)),
	})
	code, out, diagnostics := runMCRaw(t, spineEnv(spine), []byte(frame),
		"__attachment-bytes-spine", "--deadline-unix-ms",
		strconv.FormatInt(time.Now().Add(2*time.Second).UnixMilli(), 10))
	if code != 0 || diagnostics != "" {
		t.Fatalf("spine frame = code %d stderr %q", code, diagnostics)
	}
	claim := decodeAttachmentPrivateFrame(t, out)
	if claim["media_type"] != "application/pdf" || claim["display_name"] != "notes.pdf" {
		t.Fatalf("claim frame = %v", claim)
	}
	malformed := attachmentPrivateFrame(t, verbs.AttachmentBytesArgs{
		Session: "../escape", Direction: "in", SHA256: digest, Size: int64(len(body)),
	})
	badCode, _, _ := runMCRaw(t, spineEnv(spine), []byte(malformed),
		"__attachment-bytes-spine", "--deadline-unix-ms",
		strconv.FormatInt(time.Now().Add(2*time.Second).UnixMilli(), 10))
	if badCode != 2 {
		t.Fatalf("malformed frame = code %d", badCode)
	}
}
