package verbs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
)

type attachmentConsumePublication struct {
	ordinal     int64
	size        int64
	mediaType   string
	displayName string
	sha256      string
}

// AttachmentIngressConsumeArgs names the exact already-materialized native
// ingress attempt consumed by `mc collie send --ingress-attempt`. It carries no
// caller path or byte authority.
type AttachmentIngressConsumeArgs struct {
	Session   string
	From      string
	Body      string
	AttemptID string
}

// Keep the package-local name for fault/replay tests that predate production
// exposure after the mandatory canary milestone.
type attachmentIngressConsumeArgs = AttachmentIngressConsumeArgs

type attachmentEchoRef struct {
	Ordinal     int64  `json:"ordinal"`
	SessionID   string `json:"session_id"`
	Direction   string `json:"direction"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	MediaType   string `json:"media_type"`
	DisplayName string `json:"display_name"`
}

type AttachmentTerminalBatch struct {
	BatchID   int64  `json:"batch_id"`
	IngressID int64  `json:"ingress_id"`
	AttemptID string `json:"attempt_id"`
	Session   string `json:"session_id"`
	Outcome   string `json:"outcome"`
}

type AttachmentTerminalPage struct {
	Batches []AttachmentTerminalBatch `json:"batches"`
	Next    int64                     `json:"next,omitempty"`
}

type attachmentTerminalBatch = AttachmentTerminalBatch
type attachmentTerminalPage = AttachmentTerminalPage

const (
	attachmentSQLTerminalScan = `
		SELECT r.batch_id,r.ingress_id,r.attempt_id,r.session_id,r.outcome
		FROM attachment_batch_receipts r
		JOIN attachment_batches b ON b.id=r.batch_id
		JOIN attachment_attempts a ON a.id=r.attempt_id AND a.batch_id=r.batch_id
		WHERE r.batch_id>?
		  AND (
		      (r.outcome='consumed' AND b.state IN ('consumed','cleanup_pending')
		       AND a.state IN ('consumed','cleanup_pending'))
		      OR
		      (r.outcome IN ('requeued','failed') AND b.state='cleanup_pending'
		       AND a.state='cleanup_pending')
		  )
		ORDER BY r.batch_id
		LIMIT ?`
	attachmentSQLTerminalLoadBatch = `
			SELECT b.state,a.state,i.state,r.outcome,b.object_count
			FROM attachment_batch_receipts r
			JOIN attachment_batches b ON b.id=r.batch_id
			JOIN attachment_attempts a ON a.id=r.attempt_id AND a.batch_id=r.batch_id
			JOIN attachment_ingresses i ON i.id=r.ingress_id
			WHERE r.batch_id=? AND r.attempt_id=? AND r.ingress_id=?
			  AND r.session_id=?`
	attachmentSQLTerminalFencePublications = `
				UPDATE attachment_publications
				SET state='cleanup_pending',gc_generation=gc_generation+1,
				    cleanup_from_state=state,cleanup_kind='attempt_terminal'
				WHERE batch_id=? AND state='consumed'`
	attachmentSQLTerminalFenceBatch = `
				UPDATE attachment_batches SET state='cleanup_pending'
				WHERE id=? AND state='consumed'`
	attachmentSQLTerminalFenceAttempt = `
				UPDATE attachment_attempts SET state='cleanup_pending'
				WHERE id=? AND state='consumed'`
	attachmentSQLTerminalCleanupPublications = `
		SELECT id,gc_generation
		FROM attachment_publications
		WHERE batch_id=? AND state IN ('cleanup_pending','aborted')
		ORDER BY ordinal`
	attachmentSQLTerminalReady = `
			SELECT COUNT(*) FROM attachment_publications p
			WHERE p.batch_id=? AND p.state='aborted'
			  AND NOT EXISTS (
			      SELECT 1 FROM attachment_object_publications e
			      WHERE e.publication_id=p.id
			  )`
	attachmentSQLTerminalDeletePublications = `
			DELETE FROM attachment_publications
			WHERE batch_id=? AND state='aborted'`
	attachmentSQLTerminalDeleteAttempt = `
			DELETE FROM attachment_attempts
			WHERE id=? AND batch_id=? AND state='cleanup_pending'`
	attachmentSQLTerminalDeleteBatch = `
			DELETE FROM attachment_batches
			WHERE id=? AND state='cleanup_pending'`
	attachmentSQLTerminalDeleteDigests = `
				DELETE FROM attachment_ingress_digests
				WHERE ingress_id=?`
	attachmentSQLTerminalCompactIngress = `
				UPDATE attachment_ingresses
				SET body='',manifest='[]',compacted_at=datetime('now')
				WHERE id=? AND state='consumed' AND compacted_at IS NULL`
)

func AttachmentTerminalQueryAllowed(statement string) bool {
	switch statement {
	case attachmentSQLTerminalScan,
		attachmentSQLTerminalLoadBatch,
		attachmentSQLTerminalCleanupPublications,
		attachmentSQLTerminalReady,
		attachmentSQLCleanupPublication,
		attachmentSQLCleanupObject:
		return true
	default:
		return false
	}
}

func AttachmentTerminalMutationAllowed(statement string) bool {
	switch statement {
	case attachmentSQLTerminalFencePublications,
		attachmentSQLTerminalFenceBatch,
		attachmentSQLTerminalFenceAttempt,
		attachmentSQLTerminalDeletePublications,
		attachmentSQLTerminalDeleteAttempt,
		attachmentSQLTerminalDeleteBatch,
		attachmentSQLTerminalDeleteDigests,
		attachmentSQLTerminalCompactIngress,
		attachmentSQLCleanupMarkAborted,
		attachmentSQLCleanupDeleteObject:
		return true
	default:
		return false
	}
}

func exactRows(result sql.Result, want int64, lost string) error {
	got, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if got != want {
		return Domainf("%s", lost)
	}
	return nil
}

func decodeAttachmentConsumeReceipt(raw string) (map[string]any, error) {
	var receipt map[string]any
	if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
		return nil, fmt.Errorf("decode durable attachment consume receipt: %w", err)
	}
	return receipt, nil
}

func attachmentIngressConsume(db *sql.DB, a attachmentIngressConsumeArgs) (any, error) {
	from, err := parseSurfaceRef(a.From)
	if err != nil {
		return nil, err
	}
	return collieSendAttachmentIngress(db, a, from)
}

// AttachmentIngressConsume atomically transfers a complete materialized
// inbound batch into conversation ownership. Only the host-native surface may
// invoke it; the attempt id identifies durable state but is not model
// authority.
func AttachmentIngressConsume(
	db *sql.DB,
	id *RunIdentity,
	a AttachmentIngressConsumeArgs,
) (any, error) {
	if err := RequireHostScope(id, "mc collie send --ingress-attempt"); err != nil {
		return nil, err
	}
	if a.Session == "" || !safeAttachmentHex(a.AttemptID, 16) {
		return nil, Usagef(
			"mc collie send --ingress-attempt requires an exact session and 16-lowercase-hex attempt id",
		)
	}
	return attachmentIngressConsume(db, a)
}

func collieSendAttachmentIngress(
	db *sql.DB, a attachmentIngressConsumeArgs, from SurfaceRef,
) (any, error) {
	var returned map[string]any
	err := inTx(db, func(ctx context.Context, q Q) error {
		var replayReceipt, replayBody, replayBodySHA string
		var replaySurface, replayChannel, replaySession string
		var replayCompacted sql.NullString
		err := q.QueryRowContext(ctx, `
			SELECT r.receipt,i.body,i.body_sha256,i.surface,origin.channel_ref,
			       i.session_id,i.compacted_at
			FROM attachment_batch_receipts r
			JOIN attachment_ingresses i ON i.id=r.ingress_id
			LEFT JOIN attachment_attempts a ON a.id=r.attempt_id AND a.batch_id=r.batch_id
			LEFT JOIN attachment_batches b ON b.id=r.batch_id
			JOIN collie_bindings origin ON origin.id=i.binding_id
			WHERE r.attempt_id=? AND r.outcome='consumed'
			  AND i.state='consumed'
			  AND (
			      (a.state='consumed' AND b.state='consumed'
			       AND b.object_count=(
			           SELECT COUNT(*) FROM attachment_publications p
			           WHERE p.batch_id=b.id AND p.state='consumed'
			       ))
			      OR
			      (a.id IS NULL AND b.id IS NULL AND NOT EXISTS (
			           SELECT 1 FROM attachment_publications p
			           WHERE p.batch_id=r.batch_id
			       ))
			  )
			  AND (
			      SELECT COUNT(*) FROM conversation_attachment_refs c
			      WHERE c.message_id=r.conversation_message_id
			        AND c.session_id=r.session_id AND c.direction='in'
			  )>0`,
			a.AttemptID).Scan(
			&replayReceipt, &replayBody, &replayBodySHA, &replaySurface,
			&replayChannel, &replaySession, &replayCompacted)
		if err == nil {
			suppliedBodySHA := fmt.Sprintf("%x", sha256.Sum256([]byte(a.Body)))
			if replaySession != a.Session || replayBodySHA != suppliedBodySHA ||
				(!replayCompacted.Valid && replayBody != a.Body) ||
				replaySurface != from.Surface || replayChannel != from.ChannelRef {
				return Domainf("attachment consume replay does not match its sealed ingress")
			}
			returned, err = decodeAttachmentConsumeReceipt(replayReceipt)
			return err
		}
		if err != sql.ErrNoRows {
			return err
		}

		var ingressID, batchID, generation, objectCount, byteCount int64
		var sessionID, ingressBody, ingressBodySHA, externalMessageID, manifestSHA string
		var ingressSurface, ingressChannel, ownerGeneration, capabilitySHA string
		var sessionStatus, ingressState, attemptState, batchState string
		err = q.QueryRowContext(ctx, `
			SELECT i.id,a.batch_id,a.generation,b.object_count,b.byte_count,
			       i.session_id,i.body,i.body_sha256,i.external_message_id,i.manifest_sha256,
			       i.surface,origin.channel_ref,a.owner_generation,a.owner_token_sha256,
			       s.status,i.state,a.state,b.state
			FROM attachment_attempts a
			JOIN attachment_ingresses i ON i.id=a.ingress_id
			JOIN attachment_batches b ON b.id=a.batch_id
			JOIN collie_sessions s ON s.id=a.session_id
			JOIN collie_bindings origin ON origin.id=i.binding_id
			WHERE a.id=?`,
			a.AttemptID).Scan(
			&ingressID, &batchID, &generation, &objectCount, &byteCount,
			&sessionID, &ingressBody, &ingressBodySHA, &externalMessageID, &manifestSHA,
			&ingressSurface, &ingressChannel, &ownerGeneration, &capabilitySHA,
			&sessionStatus, &ingressState, &attemptState, &batchState)
		if err == sql.ErrNoRows {
			return Domainf("unknown attachment ingress attempt %q", a.AttemptID)
		}
		if err != nil {
			return err
		}
		if sessionID != a.Session || ingressBody != a.Body ||
			ingressSurface != from.Surface || ingressChannel != from.ChannelRef {
			return Domainf("attachment consume does not match its sealed ingress")
		}
		if sessionStatus != "active" || ingressState != "publishing" ||
			attemptState != "active" || batchState != "publishing" {
			return Domainf("attachment ingress attempt %q is not materialized and active",
				a.AttemptID)
		}

		rows, err := q.QueryContext(ctx, `
			SELECT p.ordinal,p.size,p.media_type,p.display_name,p.sha256
			FROM attachment_publications p
			JOIN attachment_object_publications e ON e.publication_id=p.id
			JOIN attachment_objects o
			  ON o.session_id=e.session_id AND o.direction=e.direction
			 AND o.sha256=e.sha256 AND o.size=e.size
			WHERE p.batch_id=? AND p.session_id=? AND p.direction='in'
			  AND p.state='materialized' AND p.sha256=e.sha256
			  AND p.size=e.size AND o.state='live'
			ORDER BY p.ordinal`,
			batchID, sessionID)
		if err != nil {
			return err
		}
		var publications []attachmentConsumePublication
		var totalBytes int64
		for rows.Next() {
			var publication attachmentConsumePublication
			if err := rows.Scan(
				&publication.ordinal, &publication.size, &publication.mediaType,
				&publication.displayName, &publication.sha256); err != nil {
				rows.Close()
				return err
			}
			publications = append(publications, publication)
			totalBytes += publication.size
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if int64(len(publications)) != objectCount || totalBytes != byteCount {
			return Domainf("attachment consume lacks its complete materialized batch")
		}
		for index, publication := range publications {
			if publication.ordinal != int64(index+1) {
				return Domainf("attachment consume ordinals are not complete")
			}
		}

		var seq int64
		if err := q.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(seq),0)+1
			FROM conversation_messages WHERE session_id=?`, sessionID).Scan(&seq); err != nil {
			return err
		}
		inserted, err := q.ExecContext(ctx, `
			INSERT INTO conversation_messages
			    (session_id,seq,direction,surface,channel_ref,body,attachments)
			VALUES (?,?,'inbound',?,?,?,NULL)`,
			sessionID, seq, ingressSurface, ingressChannel, ingressBody)
		if err != nil {
			return err
		}
		messageID, err := inserted.LastInsertId()
		if err != nil {
			return err
		}

		echoAttachments := make([]attachmentEchoRef, 0, len(publications))
		for _, publication := range publications {
			result, err := q.ExecContext(ctx, `
				INSERT INTO conversation_attachment_refs
				    (message_id,ordinal,session_id,direction,sha256,size,media_type,display_name)
				VALUES (?,?,?,'in',?,?,?,?)`,
				messageID, publication.ordinal, sessionID, publication.sha256,
				publication.size, publication.mediaType, publication.displayName)
			if err != nil {
				return err
			}
			if err := exactRows(result, 1, "attachment reference lost its exact insert"); err != nil {
				return err
			}
			echoAttachments = append(echoAttachments, attachmentEchoRef{
				Ordinal: publication.ordinal, SessionID: sessionID, Direction: "in",
				SHA256: publication.sha256,
				Size:   publication.size, MediaType: publication.mediaType,
				DisplayName: publication.displayName,
			})
		}

		payload, err := json.Marshal(collieEchoPayload{
			MessageID: messageID, Seq: seq, Body: ingressBody,
			Attachments: echoAttachments, Origin: from,
		})
		if err != nil {
			return err
		}
		destinations, err := q.QueryContext(ctx, `
			SELECT surface,channel_ref FROM collie_bindings
			WHERE session_id=? AND active=1
			  AND NOT (surface=? AND channel_ref=?)
			ORDER BY surface,channel_ref,id`,
			sessionID, ingressSurface, ingressChannel)
		if err != nil {
			return err
		}
		echoes := int64(0)
		for destinations.Next() {
			var destination SurfaceRef
			if err := destinations.Scan(&destination.Surface, &destination.ChannelRef); err != nil {
				destinations.Close()
				return err
			}
			if _, err := insertFanoutOutboxRow(ctx, q, "collie_echo",
				sessionID, destination.Surface, destination.ChannelRef, string(payload)); err != nil {
				destinations.Close()
				return err
			}
			echoes++
		}
		if err := destinations.Close(); err != nil {
			return err
		}
		if err := destinations.Err(); err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, `
			UPDATE collie_sessions SET last_activity_at=datetime('now')
			WHERE id=? AND status='active'`, sessionID)
		if err != nil {
			return err
		}
		if err := exactRows(result, 1, "attachment consume lost its active session"); err != nil {
			return err
		}

		returned = map[string]any{
			"attachment_ingress_id": ingressID,
			"attempt":               generation,
			"attempt_id":            a.AttemptID,
			"batch_id":              batchID,
			"body_sha256":           ingressBodySHA,
			"channel_ref":           ingressChannel,
			"echoes":                echoes,
			"external_message_id":   externalMessageID,
			"manifest_sha256":       manifestSHA,
			"message_id":            messageID,
			"seq":                   seq,
			"session_id":            sessionID,
			"state":                 "consumed",
			"surface":               ingressSurface,
		}
		receiptBytes, err := json.Marshal(returned)
		if err != nil {
			return err
		}
		result, err = q.ExecContext(ctx, `
			INSERT INTO attachment_batch_receipts
			    (batch_id,attempt_id,ingress_id,session_id,generation,owner_generation,
			     outcome,reason,capability_sha256,receipt,conversation_message_id)
			VALUES (?,?,?,?,?,?,'consumed','consumed',?,?,?)`,
			batchID, a.AttemptID, ingressID, sessionID, generation, ownerGeneration,
			capabilitySHA, string(receiptBytes), messageID)
		if err != nil {
			return err
		}
		if err := exactRows(result, 1, "attachment consume lost its durable receipt"); err != nil {
			return err
		}

		result, err = q.ExecContext(ctx, `
			UPDATE attachment_publications SET state='consumed'
			WHERE batch_id=? AND state='materialized'`, batchID)
		if err != nil {
			return err
		}
		if err := exactRows(result, objectCount, "attachment consume lost a publication CAS"); err != nil {
			return err
		}
		result, err = q.ExecContext(ctx, `
			DELETE FROM attachment_object_publications
			WHERE publication_id IN (
			    SELECT id FROM attachment_publications WHERE batch_id=?
			)`, batchID)
		if err != nil {
			return err
		}
		if err := exactRows(result, objectCount, "attachment consume lost a publication edge"); err != nil {
			return err
		}
		result, err = q.ExecContext(ctx, `
			UPDATE attachment_batches SET state='consumed'
			WHERE id=? AND state='publishing'`, batchID)
		if err != nil {
			return err
		}
		if err := exactRows(result, 1, "attachment consume lost its batch CAS"); err != nil {
			return err
		}
		result, err = q.ExecContext(ctx, `
			UPDATE attachment_ingresses SET state='consumed'
			WHERE id=? AND state='publishing'`, ingressID)
		if err != nil {
			return err
		}
		if err := exactRows(result, 1, "attachment consume lost its ingress CAS"); err != nil {
			return err
		}
		result, err = q.ExecContext(ctx, `
			UPDATE attachment_attempts SET state='consumed'
			WHERE id=? AND state='active'`, a.AttemptID)
		if err != nil {
			return err
		}
		return exactRows(result, 1, "attachment consume lost its attempt CAS")
	})
	if err != nil {
		return nil, err
	}
	return returned, nil
}

func AttachmentTerminalsReconcile(
	db *sql.DB,
	id *RunIdentity,
	home string,
	afterBatchID int64,
	limit int,
) (AttachmentTerminalPage, error) {
	page := AttachmentTerminalPage{Batches: []AttachmentTerminalBatch{}}
	if err := RequireHostScope(id, "attachment terminal reconciliation"); err != nil {
		return page, err
	}
	if afterBatchID < 0 || limit < 1 || limit > 128 {
		return page, Usagef("attachment terminal reconciliation requires a non-negative cursor and limit 1..128")
	}
	rows, err := db.Query(attachmentSQLTerminalScan, afterBatchID, limit)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var batch attachmentTerminalBatch
		if err := rows.Scan(
			&batch.BatchID, &batch.IngressID, &batch.AttemptID,
			&batch.Session, &batch.Outcome); err != nil {
			rows.Close()
			return page, err
		}
		page.Batches = append(page.Batches, batch)
	}
	if err := rows.Close(); err != nil {
		return page, err
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	for _, batch := range page.Batches {
		if err := reconcileAttachmentTerminalBatch(db, home, batch); err != nil {
			return page, err
		}
		page.Next = batch.BatchID
	}
	return page, nil
}

func attachmentTerminalsReconcile(
	db *sql.DB,
	id *RunIdentity,
	home string,
	afterBatchID int64,
	limit int,
) (attachmentTerminalPage, error) {
	return AttachmentTerminalsReconcile(db, id, home, afterBatchID, limit)
}

func reconcileAttachmentTerminalBatch(
	db *sql.DB,
	home string,
	batch attachmentTerminalBatch,
) error {
	var objectCount int64
	err := inTx(db, func(ctx context.Context, q Q) error {
		var batchState, attemptState, ingressState, outcome string
		err := q.QueryRowContext(ctx, attachmentSQLTerminalLoadBatch,
			batch.BatchID, batch.AttemptID, batch.IngressID, batch.Session).
			Scan(&batchState, &attemptState, &ingressState, &outcome, &objectCount)
		if err != nil {
			return err
		}
		if outcome != batch.Outcome {
			return Domainf("attachment terminal receipt outcome drifted")
		}
		if batchState == "consumed" {
			if outcome != "consumed" || attemptState != "consumed" ||
				ingressState != "consumed" {
				return Domainf("consumed attachment cleanup lacks its exact terminal ownership")
			}
			result, err := q.ExecContext(
				ctx, attachmentSQLTerminalFencePublications, batch.BatchID)
			if err != nil {
				return err
			}
			if err := exactRows(result, objectCount, "attachment terminal cleanup lost a publication CAS"); err != nil {
				return err
			}
			result, err = q.ExecContext(ctx, attachmentSQLTerminalFenceBatch, batch.BatchID)
			if err != nil {
				return err
			}
			if err := exactRows(result, 1, "attachment terminal cleanup lost its batch CAS"); err != nil {
				return err
			}
			result, err = q.ExecContext(ctx, attachmentSQLTerminalFenceAttempt, batch.AttemptID)
			if err != nil {
				return err
			}
			return exactRows(result, 1, "attachment terminal cleanup lost its attempt CAS")
		}
		if batchState != "cleanup_pending" || attemptState != "cleanup_pending" {
			return Domainf("attachment terminal batch is not fenced for cleanup")
		}
		if (outcome == "consumed") != (ingressState == "consumed") {
			return Domainf("attachment terminal cleanup ingress outcome drifted")
		}
		return nil
	})
	if err != nil {
		return err
	}

	rows, err := db.Query(attachmentSQLTerminalCleanupPublications, batch.BatchID)
	if err != nil {
		return err
	}
	type cleanupPublication struct {
		id         string
		generation int
	}
	var publications []cleanupPublication
	for rows.Next() {
		var publication cleanupPublication
		if err := rows.Scan(&publication.id, &publication.generation); err != nil {
			rows.Close()
			return err
		}
		publications = append(publications, publication)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if int64(len(publications)) != objectCount {
		return Domainf("attachment terminal cleanup lacks its exact publication set")
	}
	for _, publication := range publications {
		if _, err := AttachmentPublicationCleanup(
			db, nil, home, AttachmentPublicationCleanupArgs{
				Session: batch.Session, PublicationID: publication.id,
				GCGeneration: publication.generation,
			}); err != nil {
			return err
		}
	}

	return inTx(db, func(ctx context.Context, q Q) error {
		var ready int64
		if err := q.QueryRowContext(
			ctx, attachmentSQLTerminalReady, batch.BatchID).Scan(&ready); err != nil {
			return err
		}
		if ready != objectCount {
			return Domainf("attachment terminal compaction lacks complete physical cleanup")
		}
		result, err := q.ExecContext(
			ctx, attachmentSQLTerminalDeletePublications, batch.BatchID)
		if err != nil {
			return err
		}
		if err := exactRows(result, objectCount, "attachment terminal compaction lost a publication"); err != nil {
			return err
		}
		result, err = q.ExecContext(ctx, attachmentSQLTerminalDeleteAttempt,
			batch.AttemptID, batch.BatchID)
		if err != nil {
			return err
		}
		if err := exactRows(result, 1, "attachment terminal compaction lost its attempt"); err != nil {
			return err
		}
		result, err = q.ExecContext(ctx, attachmentSQLTerminalDeleteBatch, batch.BatchID)
		if err != nil {
			return err
		}
		if err := exactRows(result, 1, "attachment terminal compaction lost its batch"); err != nil {
			return err
		}
		if batch.Outcome == "consumed" {
			result, err = q.ExecContext(
				ctx, attachmentSQLTerminalDeleteDigests, batch.IngressID)
			if err != nil {
				return err
			}
			if err := exactRows(result, objectCount, "attachment terminal compaction lost an ingress digest"); err != nil {
				return err
			}
			result, err = q.ExecContext(ctx, attachmentSQLTerminalCompactIngress,
				batch.IngressID)
			if err != nil {
				return err
			}
			return exactRows(result, 1, "attachment terminal compaction lost its ingress")
		}
		return nil
	})
}
