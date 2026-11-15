package substrate_test

import "testing"

const sealedAttachmentManifest = `[{"ordinal":1,"size":7,"media_type":"image/png","display_name":"capture.png"}]`
const sealedAttachmentDigest = `aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`

func TestAttachmentReservationBackstopsRequireCompleteCreditsAndOldestIngress(t *testing.T) {
	db := openSpine(t)
	mustExec(t, db, `INSERT INTO collie_sessions
		(id,container_name,verb_allowlist,session_path,binding)
		VALUES ('h-attachment','mc-collie-h-attachment','[]','sessions/h-attachment','claude')`)
	binding := mustExec(t, db, `INSERT INTO collie_bindings
		(session_id,surface,channel_ref) VALUES ('h-attachment','discord','channel')`)
	bindingID, err := binding.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	first := mustExec(t, db, `INSERT INTO attachment_ingresses
		(session_id,binding_id,surface,external_message_id,body,manifest,manifest_sha256)
		VALUES ('h-attachment',?,'discord','first','',?,?)`,
		bindingID, sealedAttachmentManifest, sealedAttachmentDigest)
	firstID, _ := first.LastInsertId()
	second := mustExec(t, db, `INSERT INTO attachment_ingresses
		(session_id,binding_id,surface,external_message_id,body,manifest,manifest_sha256)
		VALUES ('h-attachment',?,'discord','second','',?,?)`,
		bindingID, sealedAttachmentManifest, sealedAttachmentDigest)
	secondID, _ := second.LastInsertId()

	batch := mustExec(t, db, `INSERT INTO attachment_batches
		(session_id,direction,ingress_id,manifest,manifest_sha256,object_count,byte_count)
		VALUES ('h-attachment','in',?,?,?,1,7)`,
		secondID, sealedAttachmentManifest, sealedAttachmentDigest)
	batchID, _ := batch.LastInsertId()
	// No attempt can become live before every sealed ordinal owns its exact
	// reservation and aggregate credit.
	wantAbort(t, db, `INSERT INTO attachment_attempts
		(id,ingress_id,session_id,batch_id,generation,owner_token,owner_generation,
		 publisher_lease_until,hard_deadline,owner_token_sha256)
		VALUES ('1111111111111111',?,'h-attachment',?,1,
		        '22222222222222222222222222222222','boot-1',
		        '2099-01-01 00:00:30','2099-01-01 00:15:00',
		        'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`,
		secondID, batchID)

	mustExec(t, db, `INSERT INTO attachment_publications
		(id,batch_id,session_id,direction,ordinal,size,media_type,display_name,
		 owner_token,owner_generation,publisher_lease_until,hard_deadline,
		 staging_name,journal_temp_name,journal_name)
		VALUES ('3333333333333333',?,'h-attachment','in',1,7,'image/png','capture.png',
		        '44444444444444444444444444444444','boot-1',
		        '2099-01-01 00:00:30','2099-01-01 00:15:00',
		        '3333333333333333.part','3333333333333333.json.tmp','3333333333333333.json')`, batchID)
	wantAbort(t, db, `INSERT INTO attachment_attempts
		(id,ingress_id,session_id,batch_id,generation,owner_token,owner_generation,
		 publisher_lease_until,hard_deadline,owner_token_sha256)
		VALUES ('1111111111111111',?,'h-attachment',?,1,
		        '22222222222222222222222222222222','boot-1',
		        '2099-01-01 00:00:30','2099-01-01 00:15:00',
		        'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`,
		secondID, batchID)

	// No live attempt/capability is admitted for the later row, and a direct
	// state update cannot route around that attempt fence.
	wantAbort(t, db, `UPDATE attachment_ingresses
		SET state='publishing',attempt_count=1 WHERE id=?`, secondID)
	if got := oneInt(t, db, `SELECT COUNT(*) FROM attachment_attempts WHERE state='active'`); got != 0 {
		t.Fatalf("later ingress retained %d active attempts", got)
	}
	if got := oneStr(t, db, `SELECT state FROM attachment_ingresses WHERE id=?`, firstID); got != "queued" {
		t.Fatalf("oldest ingress state = %q", got)
	}
	if got := oneStr(t, db, `SELECT state FROM attachment_ingresses WHERE id=?`, secondID); got != "queued" {
		t.Fatalf("later ingress state = %q", got)
	}
}

func TestAttachmentAttemptRefusesASelfConsistentPartialManifest(t *testing.T) {
	db := openSpine(t)
	mustExec(t, db, `INSERT INTO collie_sessions
		(id,container_name,verb_allowlist,session_path,binding)
		VALUES ('h-partial','mc-collie-h-partial','[]','sessions/h-partial','claude')`)
	binding := mustExec(t, db, `INSERT INTO collie_bindings
		(session_id,surface,channel_ref) VALUES ('h-partial','discord','channel')`)
	bindingID, _ := binding.LastInsertId()
	manifest := `[{"ordinal":1,"size":7,"media_type":"image/png","display_name":"one.png"},{"ordinal":2,"size":11,"media_type":"text/plain","display_name":"two.txt"}]`
	ingress := mustExec(t, db, `INSERT INTO attachment_ingresses
		(session_id,binding_id,surface,external_message_id,body,manifest,manifest_sha256)
		VALUES ('h-partial',?,'discord','message','',?,'dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd')`,
		bindingID, manifest)
	ingressID, _ := ingress.LastInsertId()
	wantAbort(t, db, `INSERT INTO attachment_batches
		(session_id,direction,ingress_id,manifest,manifest_sha256,object_count,byte_count,state)
		VALUES ('h-terminal','in',?,?,?,1,7,'publishing')`,
		ingressID, sealedAttachmentManifest, sealedAttachmentDigest)
	batch := mustExec(t, db, `INSERT INTO attachment_batches
		(session_id,direction,ingress_id,manifest,manifest_sha256,object_count,byte_count)
		VALUES ('h-partial','in',?,?,'dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd',1,7)`,
		ingressID, manifest)
	batchID, _ := batch.LastInsertId()
	wantAbort(t, db, `INSERT INTO attachment_publications
		(id,batch_id,session_id,direction,ordinal,size,media_type,display_name,
		 owner_token,owner_generation,publisher_lease_until,hard_deadline,
		 staging_name,journal_temp_name,journal_name,state)
		VALUES ('9999999999999999',?,'h-terminal','in',1,7,'image/png','capture.png',
		        '88888888888888888888888888888888','boot-1',
		        datetime('now','+30 seconds'),datetime('now','+15 minutes'),
		        '9999999999999999.part','9999999999999999.json.tmp','9999999999999999.json','publishing')`, batchID)
	mustExec(t, db, `INSERT INTO attachment_publications
		(id,batch_id,session_id,direction,ordinal,size,media_type,display_name,
		 owner_token,owner_generation,publisher_lease_until,hard_deadline,
		 staging_name,journal_temp_name,journal_name)
		VALUES ('7777777777777777',?,'h-partial','in',1,7,'image/png','one.png',
		        '88888888888888888888888888888888','boot-1',
		        '2099-01-01 00:00:30','2099-01-01 00:15:00',
		        '7777777777777777.part','7777777777777777.json.tmp','7777777777777777.json')`, batchID)
	wantAbort(t, db, `INSERT INTO attachment_attempts
		(id,ingress_id,session_id,batch_id,generation,owner_token,owner_generation,
		 publisher_lease_until,hard_deadline,owner_token_sha256)
		VALUES ('9999999999999999',?,'h-partial',?,1,
		        'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','boot-1',
		        '2099-01-01 00:00:30','2099-01-01 00:15:00',
		        'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb')`,
		ingressID, batchID)
}

func TestAttachmentPublicationLearnedDigestIsSetOnce(t *testing.T) {
	db := openSpine(t)
	mustExec(t, db, `INSERT INTO collie_sessions
		(id,container_name,verb_allowlist,session_path,binding)
		VALUES ('h-digest','mc-collie-h-digest','[]','sessions/h-digest','claude')`)
	binding := mustExec(t, db, `INSERT INTO collie_bindings
		(session_id,surface,channel_ref) VALUES ('h-digest','discord','channel')`)
	bindingID, _ := binding.LastInsertId()
	ingress := mustExec(t, db, `INSERT INTO attachment_ingresses
		(session_id,binding_id,surface,external_message_id,body,manifest,manifest_sha256)
		VALUES ('h-digest',?,'discord','message','',?,?)`,
		bindingID, sealedAttachmentManifest, sealedAttachmentDigest)
	ingressID, _ := ingress.LastInsertId()
	batch := mustExec(t, db, `INSERT INTO attachment_batches
		(session_id,direction,ingress_id,manifest,manifest_sha256,object_count,byte_count)
		VALUES ('h-digest','in',?,?,?,1,7)`,
		ingressID, sealedAttachmentManifest, sealedAttachmentDigest)
	batchID, _ := batch.LastInsertId()
	mustExec(t, db, `INSERT INTO attachment_publications
		(id,batch_id,session_id,direction,ordinal,size,media_type,display_name,
		 owner_token,owner_generation,publisher_lease_until,hard_deadline,
		 staging_name,journal_temp_name,journal_name)
		VALUES ('5555555555555555',?,'h-digest','in',1,7,'image/png','capture.png',
		        '66666666666666666666666666666666','boot-1',
		        '2099-01-01 00:00:30','2099-01-01 00:15:00',
		        '5555555555555555.part','5555555555555555.json.tmp','5555555555555555.json')`, batchID)
	mustExec(t, db, `UPDATE attachment_publications SET sha256=? WHERE id='5555555555555555'`,
		`bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb`)
	wantAbort(t, db, `UPDATE attachment_publications SET sha256=? WHERE id='5555555555555555'`,
		`cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc`)
	if got := oneStr(t, db, `SELECT sha256 FROM attachment_ingress_digests
		WHERE ingress_id=? AND ordinal=1`, ingressID); got != `bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb` {
		t.Fatalf("retry-stable digest = %q", got)
	}

	retryBatch := mustExec(t, db, `INSERT INTO attachment_batches
		(session_id,direction,ingress_id,manifest,manifest_sha256,object_count,byte_count)
		VALUES ('h-digest','in',?,?,?,1,7)`,
		ingressID, sealedAttachmentManifest, sealedAttachmentDigest)
	retryBatchID, _ := retryBatch.LastInsertId()
	retryInsert := `INSERT INTO attachment_publications
		(id,batch_id,session_id,direction,ordinal,size,media_type,display_name,sha256,
		 owner_token,owner_generation,publisher_lease_until,hard_deadline,
		 staging_name,journal_temp_name,journal_name)
		VALUES (?,?,'h-digest','in',1,7,'image/png','capture.png',?,
		        '99999999999999999999999999999999','boot-2',
		        '2099-01-01 00:00:30','2099-01-01 00:15:00',?,?,?)`
	wantAbort(t, db, retryInsert, "aaaaaaaaaaaaaaaa", retryBatchID, nil,
		"aaaaaaaaaaaaaaaa.part", "aaaaaaaaaaaaaaaa.json.tmp", "aaaaaaaaaaaaaaaa.json")
	wantAbort(t, db, retryInsert, "bbbbbbbbbbbbbbbb", retryBatchID,
		`cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc`,
		"bbbbbbbbbbbbbbbb.part", "bbbbbbbbbbbbbbbb.json.tmp", "bbbbbbbbbbbbbbbb.json")
	mustExec(t, db, retryInsert, "cccccccccccccccc", retryBatchID,
		`bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb`,
		"cccccccccccccccc.part", "cccccccccccccccc.json.tmp", "cccccccccccccccc.json")
}

func TestAttachmentTerminalBackstopsRefuseUnreceiptedCleanupAndLeaseWidening(t *testing.T) {
	db := openSpine(t)
	mustExec(t, db, `INSERT INTO collie_sessions
		(id,container_name,verb_allowlist,session_path,binding)
		VALUES ('h-terminal','mc-collie-h-terminal','[]','sessions/h-terminal','claude')`)
	binding := mustExec(t, db, `INSERT INTO collie_bindings
		(session_id,surface,channel_ref) VALUES ('h-terminal','discord','channel')`)
	bindingID, _ := binding.LastInsertId()
	ingress := mustExec(t, db, `INSERT INTO attachment_ingresses
		(session_id,binding_id,surface,external_message_id,body,manifest,manifest_sha256)
		VALUES ('h-terminal',?,'discord','message','',?,?)`,
		bindingID, sealedAttachmentManifest, sealedAttachmentDigest)
	ingressID, _ := ingress.LastInsertId()
	batch := mustExec(t, db, `INSERT INTO attachment_batches
		(session_id,direction,ingress_id,manifest,manifest_sha256,object_count,byte_count)
		VALUES ('h-terminal','in',?,?,?,1,7)`,
		ingressID, sealedAttachmentManifest, sealedAttachmentDigest)
	batchID, _ := batch.LastInsertId()
	mustExec(t, db, `INSERT INTO attachment_publications
		(id,batch_id,session_id,direction,ordinal,size,media_type,display_name,
		 owner_token,owner_generation,publisher_lease_until,hard_deadline,
		 staging_name,journal_temp_name,journal_name)
		VALUES ('1111111111111111',?,'h-terminal','in',1,7,'image/png','capture.png',
		        '22222222222222222222222222222222','boot-1',
		        datetime('now','+30 seconds'),datetime('now','+15 minutes'),
		        '1111111111111111.part','1111111111111111.json.tmp','1111111111111111.json')`, batchID)
	mustExec(t, db, `INSERT INTO attachment_attempts
		(id,ingress_id,session_id,batch_id,generation,owner_token,owner_generation,
		 publisher_lease_until,hard_deadline,owner_token_sha256)
		VALUES ('3333333333333333',?,'h-terminal',?,1,
		        '44444444444444444444444444444444','boot-1',
		        datetime('now','+30 seconds'),datetime('now','+15 minutes'),
		        'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb')`,
		ingressID, batchID)

	wantAbort(t, db, `UPDATE attachment_publications
		SET state='object_bound' WHERE id='1111111111111111'`)
	wantAbort(t, db, `UPDATE attachment_batches
		SET state='materialized' WHERE id=?`, batchID)
	wantAbort(t, db, `UPDATE attachment_attempts
		SET state='aborted' WHERE id='3333333333333333'`)
	wantAbort(t, db, `UPDATE attachment_publications
		SET state='cleanup_pending' WHERE id='1111111111111111'`)
	wantAbort(t, db, `UPDATE attachment_batches
		SET state='cleanup_pending' WHERE id=?`, batchID)
	wantAbort(t, db, `UPDATE attachment_ingresses
		SET state='queued',failure_count=1 WHERE id=?`, ingressID)
	wantAbort(t, db, `UPDATE attachment_attempts
		SET state='cleanup_pending' WHERE id='3333333333333333'`)
	wantAbort(t, db, `UPDATE attachment_publications
		SET publisher_lease_until=datetime('now','+31 seconds')
		WHERE id='1111111111111111'`)
	wantAbort(t, db, `UPDATE attachment_attempts
		SET publisher_lease_until=datetime('now','+31 seconds')
		WHERE id='3333333333333333'`)
	wantAbort(t, db, `INSERT INTO attachment_batch_receipts
		(batch_id,attempt_id,ingress_id,session_id,generation,owner_generation,
		 outcome,reason,capability_sha256,receipt)
		VALUES (?,'3333333333333333',?,'h-terminal',1,'boot-1',
		        'requeued','explicit_abort',
		        'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
		        '{"code":"forged"}')`, batchID, ingressID)
	wantAbort(t, db, `INSERT INTO attachment_batch_receipts
		(batch_id,attempt_id,ingress_id,session_id,generation,owner_generation,
		 outcome,reason,capability_sha256,receipt)
		SELECT ?,'3333333333333333',?,'h-terminal',1,'boot-1',
		       'requeued','explicit_abort',
		       'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
		       json_insert(json_object(
		           'code','attachment_publish_retry',
		           'attachment_ingress_id',?,
		           'attempt_id','3333333333333333',
		           'attempt',1,
		           'outcome','requeued',
		           'reason','explicit_abort',
		           'message','Attachment publication attempt was fenced and queued for retry.',
		           'body','Attachment publication attempt was fenced and queued for retry.',
		           'retryable',json('true')),
		           '$.raw_error','/private/operator/path')`,
		batchID, ingressID, ingressID)
	wantAbort(t, db, `INSERT INTO attachment_batch_receipts
		(batch_id,attempt_id,ingress_id,session_id,generation,owner_generation,
		 outcome,reason,capability_sha256,receipt)
		SELECT ?,'3333333333333333',?,'h-terminal',1,'boot-1',
		       'requeued','consumed',
		       'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
		       json_object(
		           'code','attachment_publish_retry',
		           'attachment_ingress_id',?,
		           'attempt_id','3333333333333333',
		           'attempt',1,
		           'outcome','requeued',
		           'reason','consumed',
		           'message','Attachment publication attempt was fenced and queued for retry.',
		           'body','Attachment publication attempt was fenced and queued for retry.',
		           'retryable',json('true'))`,
		batchID, ingressID, ingressID)
}

func TestAttachmentFailureReceiptIsPairedWithFailedIngress(t *testing.T) {
	db := openSpine(t)
	mustExec(t, db, `INSERT INTO collie_sessions
		(id,container_name,verb_allowlist,session_path,binding)
		VALUES ('h-failure','mc-collie-h-failure','[]','sessions/h-failure','claude')`)
	binding := mustExec(t, db, `INSERT INTO collie_bindings
		(session_id,surface,channel_ref) VALUES ('h-failure','discord','channel')`)
	bindingID, _ := binding.LastInsertId()
	ingress := mustExec(t, db, `INSERT INTO attachment_ingresses
		(session_id,binding_id,surface,external_message_id,body,manifest,manifest_sha256)
		VALUES ('h-failure',?,'discord','message','',?,?)`,
		bindingID, sealedAttachmentManifest, sealedAttachmentDigest)
	ingressID, _ := ingress.LastInsertId()

	wantAbort(t, db, `UPDATE attachment_ingresses
		SET state='failed',failure_count=3,failure_receipt='{"code":"forged"}'
		WHERE id=?`, ingressID)
	if got := oneStr(t, db, `SELECT state FROM attachment_ingresses WHERE id=?`, ingressID); got != "queued" {
		t.Fatalf("forged failure changed ingress to %q", got)
	}
}
