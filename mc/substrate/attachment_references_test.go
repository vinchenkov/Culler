package substrate_test

import (
	"strings"
	"testing"
)

func TestAttachmentReferencesRequireCanonicalLiveObjects(t *testing.T) {
	db := openSpine(t)
	mustExec(t, db, `INSERT INTO homie_sessions
		(id, container_name, verb_allowlist, session_path, binding)
		VALUES ('aabbccddeeff0011', 'homie-fixture', '[]', 'sessions/aabbccddeeff0011', 'fixture')`)
	mustExec(t, db, `INSERT INTO conversation_messages
		(session_id, seq, direction, surface, channel_ref, body)
		VALUES ('aabbccddeeff0011', 1, 'inbound', 'discord', 'room', 'hello')`)

	digest := strings.Repeat("a", 64)
	// A typed reference may not point at a missing or staged object.
	wantAbort(t, db, `INSERT INTO conversation_attachment_refs
		(message_id, ordinal, session_id, direction, sha256, size, media_type, display_name)
		VALUES (1, 1, 'aabbccddeeff0011', 'in', ?, 7, 'image/png', 'capture.png')`, digest)

	// A live object carries one stable regular-file identity, then can be
	// referenced only by the matching inbound conversation row.
	mustExec(t, db, `INSERT INTO attachment_objects
		(session_id, direction, sha256, size, state, device, inode, file_type)
		VALUES ('aabbccddeeff0011', 'in', ?, 7, 'live', '1', '2', 'regular')`, digest)
	mustExec(t, db, `INSERT INTO conversation_attachment_refs
		(message_id, ordinal, session_id, direction, sha256, size, media_type, display_name)
		VALUES (1, 1, 'aabbccddeeff0011', 'in', ?, 7, 'image/png', 'capture.png')`, digest)

	// These records are the durable conversation evidence. They cannot become
	// a different display mapping or allow deletion of the referenced object.
	wantAbort(t, db, `UPDATE conversation_attachment_refs SET display_name='other.png' WHERE message_id=1 AND ordinal=1`)
	wantAbort(t, db, `DELETE FROM conversation_attachment_refs WHERE message_id=1 AND ordinal=1`)
	wantAbort(t, db, `DELETE FROM attachment_objects WHERE session_id='aabbccddeeff0011' AND direction='in' AND sha256=? AND size=7`, digest)
}

func TestAttachmentReferenceAdmissionRejectsPathLikeOrOversizedMetadata(t *testing.T) {
	db := openSpine(t)
	mustExec(t, db, `INSERT INTO homie_sessions
		(id, container_name, verb_allowlist, session_path, binding)
		VALUES ('1122334455667788', 'homie-fixture', '[]', 'sessions/1122334455667788', 'fixture')`)
	mustExec(t, db, `INSERT INTO conversation_messages
		(session_id, seq, direction, surface, channel_ref, body)
		VALUES ('1122334455667788', 1, 'inbound', 'discord', 'room', 'hello')`)

	digest := strings.Repeat("b", 64)
	wantAbort(t, db, `INSERT INTO attachment_objects
		(session_id, direction, sha256, size, state)
		VALUES ('1122334455667788', 'in', ?, 268435457, 'publishing')`, digest)
	mustExec(t, db, `INSERT INTO attachment_objects
		(session_id, direction, sha256, size, state, device, inode, file_type)
		VALUES ('1122334455667788', 'in', ?, 0, 'live', '1', '2', 'regular')`, digest)
	wantAbort(t, db, `INSERT INTO conversation_attachment_refs
		(message_id, ordinal, session_id, direction, sha256, size, media_type, display_name)
		VALUES (1, 1, '1122334455667788', 'in', ?, 0, 'text/plain', '../secret.txt')`, digest)
	wantAbort(t, db, `INSERT INTO conversation_attachment_refs
		(message_id, ordinal, session_id, direction, sha256, size, media_type, display_name)
		VALUES (1, 33, '1122334455667788', 'in', ?, 0, 'text/plain', 'ok.txt')`, digest)
}
