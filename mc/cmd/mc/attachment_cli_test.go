package main_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"mc/verbs"
)

func attachmentPrivateFrame(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(body)))
	copy(frame[4:], body)
	return string(frame)
}

func decodeAttachmentPrivateFrame(t *testing.T, frame []byte) map[string]any {
	t.Helper()
	if len(frame) < 5 {
		t.Fatalf("private frame is only %d bytes", len(frame))
	}
	length := int(binary.BigEndian.Uint32(frame[:4]))
	if length != len(frame)-4 {
		t.Fatalf("private frame length=%d body=%d", length, len(frame)-4)
	}
	var value map[string]any
	if err := json.Unmarshal(frame[4:], &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func writeGreenAttachmentCanaryReceipt(t *testing.T, spine, generation string) {
	t.Helper()
	root := filepath.Join(filepath.Dir(spine), "health", "attachment-canary")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"version": 1, "generation": generation,
		"inbound_sha256":  "1a" + string(bytes.Repeat([]byte{'1'}, 62)),
		"outbound_sha256": "2b" + string(bytes.Repeat([]byte{'2'}, 62)),
		"real_uid":        10002, "effective_uid": 10001, "saved_uid": 10001, "fs_uid": 10001,
		"warm_inbound_read": true, "host_outbound_read": true, "private_gate_healthy": true,
		"denied_outbound": []string{
			"create:EACCES", "open:EACCES", "rename:EACCES", "traverse:EACCES", "unlink:EACCES",
		},
		"restart_inbound_read": true, "restart_outbound_read": true, "outside_sessions": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "last.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func holdAttachmentHealthGuard(t *testing.T, spine, generation string) {
	t.Helper()
	path := filepath.Join(filepath.Dir(spine), "health", "attachment-canary", "resident.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"generation":"` + generation + `","version":1}`); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
		_ = os.Remove(path)
	})
}

func TestPrivateAttachmentSpineRequiresFixedScope(t *testing.T) {
	frame := attachmentPrivateFrame(t, map[string]any{
		"owner_generation": "a1b2c3d4e5f60718293a4b5c6d7e8f90",
		"session":          "h-private",
	})
	withoutScope := runMC(t, []string{"MC_SPINE="}, frame,
		"__attachment-ingress-reserve-spine", "--deadline-unix-ms",
		strconv.FormatInt(time.Now().Add(time.Second).UnixMilli(), 10))
	if withoutScope.code != 1 || !strings.Contains(withoutScope.stderr, "requires the helper's fixed spine scope") {
		t.Fatalf("The invalid attachment scope returned an unexpected result: code=%d stderr=%q json=%v.",
			withoutScope.code, withoutScope.stderr, withoutScope.json)
	}
}

func TestAttachmentIngressCLIEnqueuesButKeepsReservationPrivate(t *testing.T) {
	spine := initSpine(t)
	started := runMC(t, spineEnv(spine), "", "collie", "start",
		"--from", "discord:attachment-channel")
	if started.code != 0 {
		t.Fatalf("collie start failed: %s", started.stderr)
	}
	session := started.json["session_id"].(string)
	db := openDB(t, spine)
	binding := queryInt(t, db, `SELECT id FROM collie_bindings
		WHERE session_id=? AND surface='discord' AND active=1`, session)
	manifest := `[{"ordinal":1,"size":7,"media_type":"image/png","display_name":"capture.png"}]`

	enqueued := runMC(t, spineEnv(spine), "", "attachment", "ingress-enqueue", session,
		"--binding", strconv.Itoa(binding), "--surface", "discord",
		"--message-id", "discord-message-1", "--body", "file follows",
		"--manifest", manifest)
	if enqueued.code != 0 || enqueued.json["state"] != "queued" ||
		enqueued.json["enqueued"] != true {
		t.Fatalf("enqueue = code %d json %v stderr %q",
			enqueued.code, enqueued.json, enqueued.stderr)
	}
	queued := runMC(t, spineEnv(spine), "", "attachment", "ingress-queued",
		"--surface", "discord", "--after", "0", "--limit", "128")
	if queued.code != 0 {
		t.Fatalf("queued page = code %d json %v stderr %q",
			queued.code, queued.json, queued.stderr)
	}
	rows := queued.json["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("queued page rows = %v", rows)
	}
	row := rows[0].(map[string]any)
	enqueuedExactID := queryStr(t, db, `SELECT CAST(id AS TEXT) FROM attachment_ingresses
		WHERE external_message_id='discord-message-1'`)
	if row["ingress_id"] != enqueuedExactID ||
		row["session_id"] != session ||
		row["binding_id"] != strconv.Itoa(binding) ||
		row["channel_ref"] != "attachment-channel" ||
		row["external_message_id"] != "discord-message-1" {
		t.Fatalf("queued page row = %v", row)
	}
	oversized := runMC(t, spineEnv(spine), "", "attachment", "ingress-queued",
		"--surface", "discord", "--limit", "129")
	if oversized.code != 2 {
		t.Fatalf("oversized queued page = code %d json %v stderr %q",
			oversized.code, oversized.json, oversized.stderr)
	}

	reserved := runMC(t, spineEnv(spine), "", "attachment", "ingress-reserve", session,
		"--owner-generation", "resident-boot-1")
	if reserved.code != 2 {
		t.Fatalf("premature reserve = code %d json %v stderr %q",
			reserved.code, reserved.json, reserved.stderr)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM attachment_attempts`); got != 0 {
		t.Fatalf("unreachable reserve created %d attempts", got)
	}

	generation := "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	writeGreenAttachmentCanaryReceipt(t, spine, generation)
	holdAttachmentHealthGuard(t, spine, generation)
	reserveFrame := attachmentPrivateFrame(t, map[string]any{
		"owner_generation": generation, "session": session,
	})
	agent := runMC(t, runJSONEnv(t, spine, "r-attachment-agent", "agent", "Worker"),
		reserveFrame, "__attachment-ingress-reserve")
	if agent.code != 1 {
		t.Fatalf("agent reserve = code %d json %v stderr %q",
			agent.code, agent.json, agent.stderr)
	}
	malformedGeneration := runMC(t, spineEnv(spine), attachmentPrivateFrame(t, map[string]any{
		"owner_generation": "resident-boot-1", "session": session,
	}), "__attachment-ingress-reserve")
	if malformedGeneration.code != 2 {
		t.Fatalf("malformed-generation reserve = code %d json %v stderr %q",
			malformedGeneration.code, malformedGeneration.json, malformedGeneration.stderr)
	}
	private := runMC(t, spineEnv(spine), reserveFrame, "__attachment-ingress-reserve")
	exactIngressID := queryStr(t, db, `SELECT CAST(id AS TEXT) FROM attachment_ingresses
		WHERE external_message_id='discord-message-1'`)
	if private.code != 0 ||
		private.json["ingress_id"] != exactIngressID ||
		private.json["batch_id"] != "1" ||
		private.json["attempt_id"] == "" ||
		private.json["owner_token"] == "" {
		t.Fatalf("private reserve = code %d stderr %q", private.code, private.stderr)
	}
	helperCode, helperFrame, helperDiagnostics := runMCRaw(
		t, spineEnv(spine), []byte(reserveFrame),
		"__attachment-ingress-reserve-spine", "--deadline-unix-ms",
		strconv.FormatInt(time.Now().Add(2*time.Second).UnixMilli(), 10),
	)
	if helperCode != 0 || helperDiagnostics != "" {
		t.Fatalf("helper reserve replay = code %d bytes %d stderr %q",
			helperCode, len(helperFrame), helperDiagnostics)
	}
	helperReplay := decodeAttachmentPrivateFrame(t, helperFrame)
	if helperReplay["attempt_id"] != private.json["attempt_id"] ||
		helperReplay["replayed"] != true {
		t.Fatalf("helper reserve replay receipt mismatch")
	}
	replay := runMC(t, spineEnv(spine), reserveFrame, "__attachment-ingress-reserve")
	if replay.code != 0 || replay.json["attempt_id"] != private.json["attempt_id"] ||
		replay.json["owner_token"] != private.json["owner_token"] ||
		replay.json["replayed"] != true {
		t.Fatalf("private reserve replay = code %d stderr %q", replay.code, replay.stderr)
	}
	wrongReserve := runMC(t, spineEnv(spine), attachmentPrivateFrame(t, map[string]any{
		"owner_generation": "0a0b0c0d0e0f10111213141516171819", "session": session,
	}), "__attachment-ingress-reserve")
	if wrongReserve.code != 1 {
		t.Fatalf("wrong-generation reserve = code %d json %v stderr %q",
			wrongReserve.code, wrongReserve.json, wrongReserve.stderr)
	}
	publications := private.json["publications"].([]any)
	publication := publications[0].(map[string]any)
	renewFrame := func(attempt, ownerGeneration string) string {
		return attachmentPrivateFrame(t, map[string]any{
			"attempt_id":              attempt,
			"attempt_owner_token":     private.json["owner_token"].(string),
			"owner_generation":        ownerGeneration,
			"publication_id":          publication["publication_id"].(string),
			"publication_owner_token": publication["owner_token"].(string),
			"session":                 session,
		})
	}
	malformedRenew := runMC(t, spineEnv(spine),
		renewFrame("not-an-attempt", generation), "__attachment-publication-renew")
	if malformedRenew.code != 2 {
		t.Fatalf("malformed renew = code %d json %v stderr %q",
			malformedRenew.code, malformedRenew.json, malformedRenew.stderr)
	}
	renewed := runMC(t, spineEnv(spine),
		renewFrame(private.json["attempt_id"].(string), generation),
		"__attachment-publication-renew")
	if renewed.code != 0 ||
		renewed.json["attempt_id"] != private.json["attempt_id"] ||
		renewed.json["publication_id"] != publication["publication_id"] {
		t.Fatalf("private renew = code %d json %v stderr %q",
			renewed.code, renewed.json, renewed.stderr)
	}
	helperRenewCode, helperRenewFrame, helperRenewDiagnostics := runMCRaw(
		t, spineEnv(spine),
		[]byte(renewFrame(private.json["attempt_id"].(string), generation)),
		"__attachment-publication-renew-spine", "--deadline-unix-ms",
		strconv.FormatInt(time.Now().Add(2*time.Second).UnixMilli(), 10),
	)
	if helperRenewCode != 0 || helperRenewDiagnostics != "" {
		t.Fatalf("helper renewal = code %d bytes %d stderr %q",
			helperRenewCode, len(helperRenewFrame), helperRenewDiagnostics)
	}
	helperRenewed := decodeAttachmentPrivateFrame(t, helperRenewFrame)
	if helperRenewed["attempt_id"] != private.json["attempt_id"] ||
		helperRenewed["publication_id"] != publication["publication_id"] {
		t.Fatal("helper renewal receipt mismatch")
	}

	wrongGeneration := runMC(t, spineEnv(spine),
		renewFrame(private.json["attempt_id"].(string), "0a0b0c0d0e0f10111213141516171819"),
		"__attachment-publication-renew")
	if wrongGeneration.code != 1 {
		t.Fatalf("wrong-generation renew = code %d json %v stderr %q",
			wrongGeneration.code, wrongGeneration.json, wrongGeneration.stderr)
	}
	publishFrame := attachmentPrivateFrame(t, map[string]any{
		"attempt_id":              private.json["attempt_id"].(string),
		"attempt_owner_token":     private.json["owner_token"].(string),
		"owner_generation":        generation,
		"publication_id":          publication["publication_id"].(string),
		"publication_owner_token": publication["owner_token"].(string),
		"session":                 session,
		"size":                    7,
	})
	published := runMC(t, spineEnv(spine), publishFrame+"payload",
		"__attachment-publication-publish")
	if published.code != 0 ||
		published.json["publication_id"] != publication["publication_id"] ||
		published.json["state"] != "materialized" ||
		published.json["size"] != float64(7) {
		t.Fatalf("private publish = code %d json %v stderr %q",
			published.code, published.json, published.stderr)
	}
	publishReplay := runMC(t, spineEnv(spine), publishFrame+"payload",
		"__attachment-publication-publish")
	if publishReplay.code != 0 ||
		publishReplay.json["sha256"] != published.json["sha256"] ||
		publishReplay.json["inode"] != published.json["inode"] {
		t.Fatalf("private publish replay = code %d json %v stderr %q",
			publishReplay.code, publishReplay.json, publishReplay.stderr)
	}
	extraReplay := runMC(t, spineEnv(spine), publishFrame+"payload!",
		"__attachment-publication-publish")
	if extraReplay.code != 2 {
		t.Fatalf("private publish extra bytes = code %d json %v stderr %q",
			extraReplay.code, extraReplay.json, extraReplay.stderr)
	}

	abortFrame := attachmentPrivateFrame(t, map[string]any{
		"attempt_id":       private.json["attempt_id"].(string),
		"owner_generation": generation,
		"owner_token":      private.json["owner_token"].(string),
		"session":          session,
	})
	aborted := runMC(t, spineEnv(spine), abortFrame, "__attachment-attempt-abort")
	if aborted.code != 0 ||
		aborted.json["attempt_id"] != private.json["attempt_id"] ||
		aborted.json["ingress_id"] != exactIngressID ||
		aborted.json["batch_id"] != "1" ||
		aborted.json["outcome"] != "requeued" ||
		aborted.json["reason"] != "explicit_abort" {
		t.Fatalf("private abort = code %d json %v stderr %q",
			aborted.code, aborted.json, aborted.stderr)
	}
	abortReplay := runMC(t, spineEnv(spine), abortFrame, "__attachment-attempt-abort")
	if abortReplay.code != 0 ||
		abortReplay.json["receipt"] != aborted.json["receipt"] ||
		abortReplay.json["replayed"] != true {
		t.Fatalf("private abort replay = code %d json %v stderr %q",
			abortReplay.code, abortReplay.json, abortReplay.stderr)
	}
	reconciled := runMC(t, spineEnv(spine), attachmentPrivateFrame(t, map[string]any{
		"after_batch_id": "0", "limit": 128, "owner_generation": generation,
	}), "__attachment-terminals-reconcile")
	batches, ok := reconciled.json["batches"].([]any)
	if reconciled.code != 0 || !ok || len(batches) != 1 ||
		reconciled.json["next"] != "1" {
		t.Fatalf("private terminal reconcile = code %d json %v stderr %q",
			reconciled.code, reconciled.json, reconciled.stderr)
	}
	batch := batches[0].(map[string]any)
	if batch["attempt_id"] != private.json["attempt_id"] ||
		batch["ingress_id"] != exactIngressID ||
		batch["outcome"] != "requeued" {
		t.Fatalf("private terminal reconcile batch = %v", batch)
	}

	retry := runMC(t, spineEnv(spine), reserveFrame, "__attachment-ingress-reserve")
	if retry.code != 0 || retry.json["attempt_id"] == private.json["attempt_id"] {
		t.Fatalf("private retry reserve = code %d json %v stderr %q",
			retry.code, retry.json, retry.stderr)
	}
	expired := runMC(t, spineEnv(spine), attachmentPrivateFrame(t, map[string]any{
		"after": "", "limit": 128, "owner_generation": generation,
	}), "__attachment-attempts-expire-due")
	receipts, ok := expired.json["receipts"].([]any)
	if expired.code != 0 || !ok || len(receipts) != 0 ||
		expired.json["next"] != nil {
		t.Fatalf("private expiry = code %d json %v stderr %q",
			expired.code, expired.json, expired.stderr)
	}
}

func TestCollieSendConsumesAnExactMaterializedIngressAttempt(t *testing.T) {
	spine := initSpine(t)
	started := runMC(t, spineEnv(spine), "", "collie", "start",
		"--from", "discord:attachment-channel")
	if started.code != 0 {
		t.Fatalf("collie start failed: %s", started.stderr)
	}
	session := started.json["session_id"].(string)
	db := openDB(t, spine)
	binding := queryInt(t, db, `SELECT id FROM collie_bindings
		WHERE session_id=? AND surface='discord' AND active=1`, session)
	manifest := `[{"ordinal":1,"size":7,"media_type":"image/png","display_name":"capture.png"}]`
	enqueued := runMC(t, spineEnv(spine), "", "attachment", "ingress-enqueue", session,
		"--binding", strconv.Itoa(binding), "--surface", "discord",
		"--message-id", "discord-message-consume", "--body", "file follows",
		"--manifest", manifest)
	if enqueued.code != 0 {
		t.Fatalf("enqueue = code %d json %v stderr %q",
			enqueued.code, enqueued.json, enqueued.stderr)
	}
	reservation, err := verbs.AttachmentIngressReserve(
		db, nil, verbs.AttachmentIngressReserveArgs{
			Session: session, OwnerGeneration: "resident-boot-1",
		})
	if err != nil {
		t.Fatalf("reserve ingress: %v", err)
	}
	publication := reservation.Publications[0]
	if _, err := verbs.AttachmentPublicationPublish(
		db, nil, filepath.Dir(spine), verbs.AttachmentPublicationPublishArgs{
			Session: session, AttemptID: reservation.AttemptID,
			AttemptOwnerToken:     reservation.OwnerToken,
			PublicationID:         publication.PublicationID,
			PublicationOwnerToken: publication.OwnerToken,
			OwnerGeneration:       "resident-boot-1",
		}, bytes.NewBufferString("payload"),
	); err != nil {
		t.Fatalf("materialize ingress: %v", err)
	}

	mixed := runMC(t, spineEnv(spine), "", "collie", "send", session,
		"--from", "discord:attachment-channel", "--body", "file follows",
		"--attachments", "[]", "--ingress-attempt", reservation.AttemptID)
	if mixed.code != 2 {
		t.Fatalf("mixed attachment authority = code %d json %v stderr %q",
			mixed.code, mixed.json, mixed.stderr)
	}
	consumed := runMC(t, spineEnv(spine), "", "collie", "send", session,
		"--from", "discord:attachment-channel", "--body", "file follows",
		"--ingress-attempt", reservation.AttemptID)
	if consumed.code != 0 || consumed.json["state"] != "consumed" ||
		consumed.json["attempt_id"] != reservation.AttemptID {
		t.Fatalf("consume = code %d json %v stderr %q",
			consumed.code, consumed.json, consumed.stderr)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM conversation_attachment_refs
		WHERE session_id=? AND direction='in'`, session); got != 1 {
		t.Fatalf("typed inbound references = %d, want 1", got)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM conversation_messages
		WHERE session_id=? AND direction='inbound'`, session); got != 1 {
		t.Fatalf("inbound conversation rows = %d, want 1", got)
	}
}
