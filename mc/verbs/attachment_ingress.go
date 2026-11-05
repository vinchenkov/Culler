package verbs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"
)

// AttachmentIngressEnqueueArgs is the path-free native inbox frame. The
// binding id is explicit so a surface cannot enqueue against a place that was
// rebound between receipt and the spine transaction.
type AttachmentIngressEnqueueArgs struct {
	Session           string
	BindingID         int64
	Surface           string
	ExternalMessageID string
	Body              string
	Manifest          string
}

type AttachmentIngressReceipt struct {
	IngressID      int64          `json:"ingress_id"`
	Session        string         `json:"session_id"`
	State          string         `json:"state"`
	ManifestSHA256 string         `json:"manifest_sha256"`
	Enqueued       bool           `json:"enqueued"`
	Result         map[string]any `json:"result,omitempty"`
}

// AttachmentIngressEnqueue durably records a native event before any file
// work. Exact-key replay returns the original row; a changed declaration under
// that key is an idempotency conflict, never a new upload.
func AttachmentIngressEnqueue(db *sql.DB, id *RunIdentity, a AttachmentIngressEnqueueArgs) (AttachmentIngressReceipt, error) {
	if err := RequireHostScope(id, "mc attachment ingress-enqueue"); err != nil {
		return AttachmentIngressReceipt{}, err
	}
	if a.Session == "" || a.BindingID <= 0 {
		return AttachmentIngressReceipt{}, Usagef("mc attachment ingress-enqueue requires an exact session and binding")
	}
	switch a.Surface {
	case "discord", "dashboard", "cli":
	default:
		return AttachmentIngressReceipt{}, Usagef("attachment ingress surface must be discord|dashboard|cli")
	}
	if len(a.ExternalMessageID) == 0 || len(a.ExternalMessageID) > 255 || !utf8.ValidString(a.ExternalMessageID) {
		return AttachmentIngressReceipt{}, Usagef("attachment ingress message id must be 1..255 UTF-8 bytes")
	}
	if len(a.Body) > 65536 || !utf8.ValidString(a.Body) {
		return AttachmentIngressReceipt{}, Usagef("attachment ingress body must be at most 65536 UTF-8 bytes")
	}
	manifest, err := ParseAttachmentManifest(a.Manifest)
	if err != nil {
		return AttachmentIngressReceipt{}, err
	}

	out := AttachmentIngressReceipt{
		Session: a.Session, State: "queued", ManifestSHA256: manifest.Digest,
	}
	bodyDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(a.Body)))
	err = inTx(db, func(ctx context.Context, q Q) error {
		var body, storedBodyDigest, existingManifest, digest, state string
		var compactedAt sql.NullString
		err := q.QueryRowContext(ctx, `
			SELECT id, body, body_sha256, manifest, manifest_sha256, state, compacted_at
			FROM attachment_ingresses
			WHERE session_id=? AND binding_id=? AND surface=? AND external_message_id=?`,
			a.Session, a.BindingID, a.Surface, a.ExternalMessageID,
		).Scan(
			&out.IngressID, &body, &storedBodyDigest, &existingManifest,
			&digest, &state, &compactedAt)
		if err == nil {
			if storedBodyDigest != bodyDigest || digest != manifest.Digest ||
				(!compactedAt.Valid &&
					(body != a.Body || existingManifest != manifest.Canonical)) {
				return Domainf("attachment ingress key already carries different sealed content")
			}
			out.State = state
			out.ManifestSHA256 = digest
			if state == "consumed" {
				var receipt string
				if err := q.QueryRowContext(ctx, `
					SELECT receipt FROM attachment_batch_receipts
					WHERE ingress_id=? AND outcome='consumed'`,
					out.IngressID).Scan(&receipt); err != nil {
					return fmt.Errorf("load consumed attachment ingress receipt: %w", err)
				}
				out.Result, err = decodeAttachmentConsumeReceipt(receipt)
				if err != nil {
					return err
				}
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}

		var bindingSession, bindingSurface, sessionStatus string
		var active int
		err = q.QueryRowContext(ctx, `
			SELECT b.session_id, b.surface, b.active, s.status
			FROM homie_bindings b JOIN homie_sessions s ON s.id=b.session_id
			WHERE b.id=?`, a.BindingID,
		).Scan(&bindingSession, &bindingSurface, &active, &sessionStatus)
		if err == sql.ErrNoRows {
			return Domainf("unknown Homie binding %d", a.BindingID)
		}
		if err != nil {
			return err
		}
		if bindingSession != a.Session || bindingSurface != a.Surface || active != 1 || sessionStatus != "active" {
			return Domainf("attachment ingress requires its exact active session and binding")
		}

		inserted, err := q.ExecContext(ctx, `
			INSERT INTO attachment_ingresses
				(session_id,binding_id,surface,external_message_id,body,body_sha256,manifest,manifest_sha256)
			VALUES (?,?,?,?,?,?,?,?)`,
			a.Session, a.BindingID, a.Surface, a.ExternalMessageID,
			a.Body, bodyDigest, manifest.Canonical, manifest.Digest)
		if err != nil {
			return err
		}
		out.IngressID, err = inserted.LastInsertId()
		if err != nil {
			return err
		}
		out.Enqueued = true
		return nil
	})
	return out, err
}

type AttachmentIngressQueuedArgs struct {
	Surface string
	After   string
	Limit   int
}

type AttachmentIngressQueuedRow struct {
	IngressID         string `json:"ingress_id"`
	Session           string `json:"session_id"`
	BindingID         string `json:"binding_id"`
	Surface           string `json:"surface"`
	ChannelRef        string `json:"channel_ref"`
	ExternalMessageID string `json:"external_message_id"`
	Body              string `json:"body"`
	BodySHA256        string `json:"body_sha256"`
	Manifest          string `json:"manifest"`
	ManifestSHA256    string `json:"manifest_sha256"`
	AttemptCount      int    `json:"attempt_count"`
	CreatedAt         string `json:"created_at"`
}

type AttachmentIngressQueuedPage struct {
	Surface string                       `json:"surface"`
	Rows    []AttachmentIngressQueuedRow `json:"rows"`
	Next    string                       `json:"next,omitempty"`
}

// AttachmentIngressQueued scans at most 128 stable ingress identities and
// returns the requested native surface's queued rows from that bounded window.
// A caller follows Next even when Rows is empty, and starts each later sweep
// from zero so an older row requeued after the cursor passed it is rediscovered.
func AttachmentIngressQueued(db *sql.DB, id *RunIdentity, a AttachmentIngressQueuedArgs) (AttachmentIngressQueuedPage, error) {
	out := AttachmentIngressQueuedPage{
		Surface: a.Surface,
		Rows:    []AttachmentIngressQueuedRow{},
	}
	if err := RequireHostScope(id, "mc attachment ingress-queued"); err != nil {
		return out, err
	}
	switch a.Surface {
	case "discord", "dashboard", "cli":
	default:
		return out, Usagef("attachment queued-ingress surface must be discord|dashboard|cli")
	}
	after, err := parseAttachmentIngressCursor(a.After)
	if err != nil {
		return out, err
	}
	if a.Limit < 1 || a.Limit > 128 {
		return out, Usagef("attachment queued-ingress page limit must be 1..128")
	}

	rows, err := db.Query(`
		SELECT i.id,i.session_id,i.binding_id,i.surface,b.channel_ref,
		       i.external_message_id,i.body,i.body_sha256,i.manifest,
		       i.manifest_sha256,i.state,i.attempt_count,i.created_at
		FROM attachment_ingresses i
		JOIN homie_bindings b ON b.id=i.binding_id
		WHERE i.id>?
		ORDER BY i.id
		LIMIT ?`, after, a.Limit)
	if err != nil {
		return out, classify(err)
	}
	scanned := 0
	var lastID int64
	for rows.Next() {
		var ingressID, bindingID int64
		var row AttachmentIngressQueuedRow
		var state string
		if err := rows.Scan(
			&ingressID, &row.Session, &bindingID, &row.Surface, &row.ChannelRef,
			&row.ExternalMessageID, &row.Body, &row.BodySHA256, &row.Manifest,
			&row.ManifestSHA256, &state, &row.AttemptCount, &row.CreatedAt,
		); err != nil {
			rows.Close()
			return out, classify(err)
		}
		scanned++
		lastID = ingressID
		if state != "queued" || row.Surface != a.Surface {
			continue
		}
		manifest, err := ParseAttachmentManifest(row.Manifest)
		if err != nil || manifest.Canonical != row.Manifest ||
			manifest.Digest != row.ManifestSHA256 ||
			fmt.Sprintf("%x", sha256.Sum256([]byte(row.Body))) != row.BodySHA256 {
			rows.Close()
			return out, Domainf(
				"stored queued attachment ingress %d content changed; refusing native recovery",
				ingressID,
			)
		}
		row.IngressID = strconv.FormatInt(ingressID, 10)
		row.BindingID = strconv.FormatInt(bindingID, 10)
		out.Rows = append(out.Rows, row)
	}
	if err := rows.Close(); err != nil {
		return out, classify(err)
	}
	if err := rows.Err(); err != nil {
		return out, classify(err)
	}
	if scanned == a.Limit {
		out.Next = strconv.FormatInt(lastID, 10)
	}
	return out, nil
}

func parseAttachmentIngressCursor(raw string) (int64, error) {
	if raw == "0" {
		return 0, nil
	}
	if len(raw) == 0 || raw[0] < '1' || raw[0] > '9' {
		return 0, Usagef("attachment queued-ingress cursor must be a canonical non-negative decimal id")
	}
	for i := 1; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, Usagef("attachment queued-ingress cursor must be a canonical non-negative decimal id")
		}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, Usagef("attachment queued-ingress cursor must be a canonical non-negative decimal id")
	}
	return value, nil
}

type AttachmentIngressReserveArgs struct {
	Session         string
	OwnerGeneration string
}

type AttachmentPublicationReservation struct {
	PublicationID string `json:"publication_id"`
	OwnerToken    string `json:"owner_token"`
	Ordinal       int    `json:"ordinal"`
	Size          int64  `json:"size"`
	MediaType     string `json:"media_type"`
	DisplayName   string `json:"display_name"`
	SHA256        string `json:"sha256,omitempty"`
}

type AttachmentIngressReservation struct {
	IngressID    int64                              `json:"ingress_id"`
	AttemptID    string                             `json:"attempt_id"`
	OwnerToken   string                             `json:"owner_token"`
	BatchID      int64                              `json:"batch_id"`
	ObjectCount  int                                `json:"object_count"`
	ByteCount    int64                              `json:"byte_count"`
	LeaseUntil   string                             `json:"lease_until"`
	HardDeadline string                             `json:"hard_deadline"`
	Publications []AttachmentPublicationReservation `json:"publications"`
	Replayed     bool                               `json:"replayed"`
}

// AttachmentIngressReserve promotes only a session's oldest queued ingress.
// The attempt, batch, every ordinal, and the exact aggregate credits are one
// BEGIN IMMEDIATE transaction, so no publisher can observe a partial budget.
func AttachmentIngressReserve(db *sql.DB, id *RunIdentity, a AttachmentIngressReserveArgs) (AttachmentIngressReservation, error) {
	if err := RequireHostScope(id, "mc attachment ingress-reserve"); err != nil {
		return AttachmentIngressReservation{}, err
	}
	if a.Session == "" {
		return AttachmentIngressReservation{}, Usagef("mc attachment ingress-reserve requires a session")
	}
	if !safeAttachmentOwnerGeneration(a.OwnerGeneration) {
		return AttachmentIngressReservation{}, Usagef("attachment owner generation must be 1..255 control-free UTF-8 bytes")
	}

	var out AttachmentIngressReservation
	err := inTx(db, func(ctx context.Context, q Q) error {
		var existingGeneration string
		err := q.QueryRowContext(ctx, `
			SELECT a.id,a.ingress_id,a.owner_token,a.owner_generation,a.batch_id,
			       b.object_count,b.byte_count,a.publisher_lease_until,a.hard_deadline
			FROM attachment_attempts a JOIN attachment_batches b ON b.id=a.batch_id
			WHERE a.session_id=? AND a.state='active'`, a.Session,
		).Scan(&out.AttemptID, &out.IngressID, &out.OwnerToken, &existingGeneration,
			&out.BatchID, &out.ObjectCount, &out.ByteCount, &out.LeaseUntil, &out.HardDeadline)
		if err == nil {
			if existingGeneration != a.OwnerGeneration {
				return Domainf("attachment ingress attempt is active under another native generation")
			}
			out.Replayed = true
			return loadAttachmentPublicationReservations(ctx, q, &out)
		}
		if err != sql.ErrNoRows {
			return err
		}

		var manifestRaw, manifestDigest string
		var attemptCount int
		err = q.QueryRowContext(ctx, `
			SELECT i.id,i.manifest,i.manifest_sha256,i.attempt_count
			FROM attachment_ingresses i
			JOIN homie_sessions s ON s.id=i.session_id
			WHERE i.session_id=? AND i.state='queued'
			  AND s.status='active'
			ORDER BY i.id LIMIT 1`, a.Session,
		).Scan(&out.IngressID, &manifestRaw, &manifestDigest, &attemptCount)
		if err == sql.ErrNoRows {
			return Domainf("Homie session %q has no queued attachment ingress", a.Session)
		}
		if err != nil {
			return err
		}
		manifest, err := ParseAttachmentManifest(manifestRaw)
		if err != nil {
			return fmt.Errorf("stored attachment manifest is invalid: %w", err)
		}
		if manifest.Digest != manifestDigest {
			return Domainf("stored attachment manifest digest changed; refusing reservation")
		}
		out.ObjectCount = len(manifest.Entries)
		for _, entry := range manifest.Entries {
			out.ByteCount += entry.Size
		}
		if err := q.QueryRowContext(ctx, `
			SELECT datetime('now','+30 seconds'),datetime('now','+15 minutes')`,
		).Scan(&out.LeaseUntil, &out.HardDeadline); err != nil {
			return err
		}

		out.AttemptID, err = randomAttachmentHex(8)
		if err != nil {
			return err
		}
		out.OwnerToken, err = randomAttachmentHex(16)
		if err != nil {
			return err
		}
		if err := q.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(id),0)+1
			FROM (
			    SELECT id FROM attachment_batches
			    UNION ALL
			    SELECT batch_id AS id FROM attachment_batch_receipts
			)`).Scan(&out.BatchID); err != nil {
			return err
		}
		inserted, err := q.ExecContext(ctx, `
			INSERT INTO attachment_batches
				(id,session_id,direction,ingress_id,manifest,manifest_sha256,object_count,byte_count)
			VALUES (?,?,'in',?,?,?,?,?)`,
			out.BatchID, a.Session, out.IngressID, manifest.Canonical, manifest.Digest,
			out.ObjectCount, out.ByteCount)
		if err != nil {
			return err
		}
		insertedID, err := inserted.LastInsertId()
		if err != nil {
			return err
		}
		if insertedID != out.BatchID {
			return Domainf("attachment batch allocation lost its monotonic id fence")
		}

		for _, entry := range manifest.Entries {
			publicationID, err := randomAttachmentHex(8)
			if err != nil {
				return err
			}
			ownerToken, err := randomAttachmentHex(16)
			if err != nil {
				return err
			}
			var learned sql.NullString
			err = q.QueryRowContext(ctx, `
				SELECT sha256 FROM attachment_ingress_digests
				WHERE ingress_id=? AND ordinal=?`, out.IngressID, entry.Ordinal,
			).Scan(&learned)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			var learnedValue any
			if learned.Valid {
				learnedValue = learned.String
			}
			if _, err := q.ExecContext(ctx, `
				INSERT INTO attachment_publications
					(id,batch_id,session_id,direction,ordinal,size,media_type,display_name,sha256,
					 owner_token,owner_generation,publisher_lease_until,hard_deadline,
					 staging_name,journal_temp_name,journal_name)
				VALUES (?,?,?,'in',?,?,?,?,?,?,?,?,?,?,?,?)`,
				publicationID, out.BatchID, a.Session, entry.Ordinal, entry.Size,
				entry.MediaType, entry.DisplayName, learnedValue, ownerToken, a.OwnerGeneration,
				out.LeaseUntil, out.HardDeadline, publicationID+".part",
				publicationID+".json.tmp", publicationID+".json"); err != nil {
				return err
			}
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO attachment_attempts
				(id,ingress_id,session_id,batch_id,generation,owner_token,owner_generation,
				 publisher_lease_until,hard_deadline,owner_token_sha256)
			VALUES (?,?,?,?,?,?,?,?,?,?)`,
			out.AttemptID, out.IngressID, a.Session, out.BatchID, attemptCount+1,
			out.OwnerToken, a.OwnerGeneration, out.LeaseUntil, out.HardDeadline,
			attachmentCapabilityDigest(out.OwnerToken)); err != nil {
			return err
		}
		return loadAttachmentPublicationReservations(ctx, q, &out)
	})
	return out, err
}

func loadAttachmentPublicationReservations(ctx context.Context, q Q, out *AttachmentIngressReservation) error {
	rows, err := q.QueryContext(ctx, `
		SELECT id,owner_token,ordinal,size,media_type,display_name,sha256
		FROM attachment_publications WHERE batch_id=? ORDER BY ordinal`, out.BatchID)
	if err != nil {
		return err
	}
	defer rows.Close()
	out.Publications = []AttachmentPublicationReservation{}
	for rows.Next() {
		var publication AttachmentPublicationReservation
		var digest sql.NullString
		if err := rows.Scan(&publication.PublicationID, &publication.OwnerToken,
			&publication.Ordinal, &publication.Size, &publication.MediaType,
			&publication.DisplayName, &digest); err != nil {
			return err
		}
		if digest.Valid {
			publication.SHA256 = digest.String
		}
		out.Publications = append(out.Publications, publication)
	}
	return rows.Err()
}

type AttachmentPublicationRenewArgs struct {
	Session               string
	AttemptID             string
	AttemptOwnerToken     string
	PublicationID         string
	PublicationOwnerToken string
	OwnerGeneration       string
}

type AttachmentPublicationLeaseReceipt struct {
	AttemptID     string `json:"attempt_id"`
	PublicationID string `json:"publication_id"`
	LeaseUntil    string `json:"lease_until"`
	HardDeadline  string `json:"hard_deadline"`
}

// AttachmentPublicationRenew advances one exact publication capability and
// its attempt aggregate together. An expired lease is not revivable: cleanup
// and a late publisher therefore have one serializable winner.
func AttachmentPublicationRenew(db *sql.DB, id *RunIdentity, a AttachmentPublicationRenewArgs) (AttachmentPublicationLeaseReceipt, error) {
	if err := RequireHostScope(id, "mc attachment publication-renew"); err != nil {
		return AttachmentPublicationLeaseReceipt{}, err
	}
	if a.Session == "" || !safeAttachmentHex(a.AttemptID, 16) ||
		!safeAttachmentHex(a.AttemptOwnerToken, 32) ||
		!safeAttachmentHex(a.PublicationID, 16) ||
		!safeAttachmentHex(a.PublicationOwnerToken, 32) ||
		!safeAttachmentOwnerGeneration(a.OwnerGeneration) {
		return AttachmentPublicationLeaseReceipt{}, Usagef("attachment publication-renew requires exact session, attempt/publication capabilities, and native generation")
	}

	out := AttachmentPublicationLeaseReceipt{
		AttemptID: a.AttemptID, PublicationID: a.PublicationID,
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		var attemptLease, publicationLease string
		err := q.QueryRowContext(ctx, `
			SELECT a.publisher_lease_until,p.publisher_lease_until,a.hard_deadline,
			       min(datetime('now','+30 seconds'),a.hard_deadline)
			FROM attachment_attempts a
			JOIN attachment_batches b ON b.id=a.batch_id
			JOIN attachment_publications p ON p.batch_id=b.id
			JOIN attachment_ingresses i ON i.id=a.ingress_id
			JOIN homie_sessions s ON s.id=a.session_id
			WHERE a.id=? AND a.session_id=? AND a.owner_token=?
			  AND a.owner_generation=? AND a.state='active'
			  AND b.state='publishing' AND i.state='publishing'
			  AND p.id=? AND p.owner_token=? AND p.owner_generation=?
			  AND p.state IN ('reserved','publishing')
			  AND datetime('now')<a.publisher_lease_until
			  AND datetime('now')<p.publisher_lease_until
			  AND datetime('now')<a.hard_deadline
			  AND s.status='active'`,
			a.AttemptID, a.Session, a.AttemptOwnerToken, a.OwnerGeneration,
			a.PublicationID, a.PublicationOwnerToken, a.OwnerGeneration,
		).Scan(&attemptLease, &publicationLease, &out.HardDeadline, &out.LeaseUntil)
		if err == sql.ErrNoRows {
			return Domainf("attachment publication lease capability is stale, expired, or fenced")
		}
		if err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, `
			UPDATE attachment_publications SET publisher_lease_until=?
			WHERE id=? AND owner_token=? AND owner_generation=?
			  AND state IN ('reserved','publishing') AND publisher_lease_until=?`,
			out.LeaseUntil, a.PublicationID, a.PublicationOwnerToken,
			a.OwnerGeneration, publicationLease)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			return Domainf("attachment publication lease lost its ownership CAS")
		}
		result, err = q.ExecContext(ctx, `
			UPDATE attachment_attempts SET publisher_lease_until=?
			WHERE id=? AND owner_token=? AND owner_generation=?
			  AND state='active' AND publisher_lease_until=?`,
			out.LeaseUntil, a.AttemptID, a.AttemptOwnerToken,
			a.OwnerGeneration, attemptLease)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			return Domainf("attachment attempt lease lost its ownership CAS")
		}
		return nil
	})
	return out, err
}

type AttachmentAttemptAbortArgs struct {
	Session         string
	AttemptID       string
	OwnerToken      string
	OwnerGeneration string
}

type AttachmentAttemptTerminalReceipt struct {
	AttemptID string `json:"attempt_id"`
	IngressID int64  `json:"ingress_id"`
	BatchID   int64  `json:"batch_id"`
	Attempt   int    `json:"attempt"`
	Outcome   string `json:"outcome"`
	Reason    string `json:"reason"`
	Receipt   string `json:"receipt"`
	Replayed  bool   `json:"replayed"`
}

type attachmentAttemptTerminalPayload struct {
	Code                string `json:"code"`
	AttachmentIngressID int64  `json:"attachment_ingress_id"`
	AttemptID           string `json:"attempt_id"`
	Attempt             int    `json:"attempt"`
	Outcome             string `json:"outcome"`
	Reason              string `json:"reason"`
	Message             string `json:"message"`
	Body                string `json:"body"`
	Retryable           bool   `json:"retryable"`
}

// AttachmentAttemptAbort is the exact native-owner terminal. It persists a
// token digest rather than the bearer token, allowing authenticated replay
// after derived attempt rows are eventually compacted.
func AttachmentAttemptAbort(db *sql.DB, id *RunIdentity, a AttachmentAttemptAbortArgs) (AttachmentAttemptTerminalReceipt, error) {
	if err := RequireHostScope(id, "mc attachment attempt-abort"); err != nil {
		return AttachmentAttemptTerminalReceipt{}, err
	}
	if a.Session == "" || !safeAttachmentHex(a.AttemptID, 16) ||
		!safeAttachmentHex(a.OwnerToken, 32) ||
		!safeAttachmentOwnerGeneration(a.OwnerGeneration) {
		return AttachmentAttemptTerminalReceipt{}, Usagef("attachment attempt-abort requires an exact attempt capability and native generation")
	}
	capabilityDigest := attachmentCapabilityDigest(a.OwnerToken)
	var out AttachmentAttemptTerminalReceipt
	err := inTx(db, func(ctx context.Context, q Q) error {
		found, err := loadAttachmentTerminalReplay(ctx, q, a.AttemptID, &out)
		if err != nil {
			return err
		}
		if found {
			var session, ownerGeneration, storedDigest string
			if err := q.QueryRowContext(ctx, `
				SELECT session_id,owner_generation,capability_sha256
				FROM attachment_batch_receipts WHERE attempt_id=?`,
				a.AttemptID).Scan(&session, &ownerGeneration, &storedDigest); err != nil {
				return err
			}
			if session != a.Session || ownerGeneration != a.OwnerGeneration ||
				storedDigest != capabilityDigest {
				return Domainf("attachment attempt terminal belongs to another capability")
			}
			out.Replayed = true
			return nil
		}
		return terminalizeAttachmentAttempt(ctx, q, a.AttemptID, a.Session,
			a.OwnerToken, a.OwnerGeneration, "explicit_abort", "", &out)
	})
	return out, err
}

// AttachmentAttemptExpire is the host reconciler's exact hard-deadline arm.
// The production entry always compares lock-domain database time.
func AttachmentAttemptExpire(db *sql.DB, id *RunIdentity, attemptID string) (AttachmentAttemptTerminalReceipt, error) {
	return attachmentAttemptExpireAt(db, id, attemptID, "")
}

type AttachmentAttemptExpiryPage struct {
	Receipts []AttachmentAttemptTerminalReceipt `json:"receipts"`
	Next     string                             `json:"next,omitempty"`
}

// AttachmentAttemptsExpireDue is the bounded reconciler scan. A caller starts
// each sweep with an empty cursor and follows Next; a later sweep starts over
// so randomly allocated newer IDs below an old cursor are never skipped.
func AttachmentAttemptsExpireDue(db *sql.DB, id *RunIdentity, after string, limit int) (AttachmentAttemptExpiryPage, error) {
	return attachmentAttemptsExpireDueAt(db, id, after, limit, "")
}

func attachmentAttemptsExpireDueAt(db *sql.DB, id *RunIdentity, after string, limit int, at string) (AttachmentAttemptExpiryPage, error) {
	if err := RequireHostScope(id, "mc attachment attempts-expire-due"); err != nil {
		return AttachmentAttemptExpiryPage{}, err
	}
	if after != "" && !safeAttachmentHex(after, 16) {
		return AttachmentAttemptExpiryPage{}, Usagef("attachment expiry cursor must be an exact attempt id")
	}
	if limit < 1 || limit > 128 {
		return AttachmentAttemptExpiryPage{}, Usagef("attachment expiry page limit must be 1..128")
	}
	if at != "" {
		if _, err := time.Parse(spineTime, at); err != nil {
			return AttachmentAttemptExpiryPage{}, Usagef("invalid attachment expiry test timestamp")
		}
	}
	out := AttachmentAttemptExpiryPage{
		Receipts: []AttachmentAttemptTerminalReceipt{},
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		query := `
			SELECT id,session_id,owner_token,owner_generation
			FROM attachment_attempts
			WHERE state='active' AND id>? AND hard_deadline<=datetime('now')
			ORDER BY id LIMIT ?`
		args := []any{after, limit}
		if at != "" {
			query = `
				SELECT id,session_id,owner_token,owner_generation
				FROM attachment_attempts
				WHERE state='active' AND id>? AND hard_deadline<=?
				ORDER BY id LIMIT ?`
			args = []any{after, at, limit}
		}
		rows, err := q.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		type dueAttempt struct {
			id, session, token, generation string
		}
		var due []dueAttempt
		for rows.Next() {
			var attempt dueAttempt
			if err := rows.Scan(&attempt.id, &attempt.session, &attempt.token,
				&attempt.generation); err != nil {
				rows.Close()
				return err
			}
			due = append(due, attempt)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, attempt := range due {
			var receipt AttachmentAttemptTerminalReceipt
			if err := terminalizeAttachmentAttempt(ctx, q, attempt.id,
				attempt.session, attempt.token, attempt.generation,
				"hard_expired", at, &receipt); err != nil {
				return err
			}
			out.Receipts = append(out.Receipts, receipt)
			out.Next = attempt.id
		}
		return nil
	})
	return out, err
}

func attachmentAttemptExpireAt(db *sql.DB, id *RunIdentity, attemptID, at string) (AttachmentAttemptTerminalReceipt, error) {
	if err := RequireHostScope(id, "mc attachment attempt-expire"); err != nil {
		return AttachmentAttemptTerminalReceipt{}, err
	}
	if !safeAttachmentHex(attemptID, 16) {
		return AttachmentAttemptTerminalReceipt{}, Usagef("attachment attempt-expire requires an exact attempt id")
	}
	if at != "" {
		if _, err := time.Parse(spineTime, at); err != nil {
			return AttachmentAttemptTerminalReceipt{}, Usagef("invalid attachment expiry test timestamp")
		}
	}
	var out AttachmentAttemptTerminalReceipt
	err := inTx(db, func(ctx context.Context, q Q) error {
		found, err := loadAttachmentTerminalReplay(ctx, q, attemptID, &out)
		if err != nil {
			return err
		}
		if found {
			out.Replayed = true
			return nil
		}
		var session, token, ownerGeneration, deadline string
		err = q.QueryRowContext(ctx, `
			SELECT session_id,owner_token,owner_generation,hard_deadline
			FROM attachment_attempts WHERE id=? AND state='active'`, attemptID,
		).Scan(&session, &token, &ownerGeneration, &deadline)
		if err == sql.ErrNoRows {
			return Domainf("attachment attempt %q is unknown or no longer active", attemptID)
		}
		if err != nil {
			return err
		}
		due := false
		if at == "" {
			if err := q.QueryRowContext(ctx,
				`SELECT datetime('now')>=?`, deadline).Scan(&due); err != nil {
				return err
			}
		} else {
			due = at >= deadline
		}
		if !due {
			return Domainf("attachment attempt %q has not reached its hard deadline", attemptID)
		}
		return terminalizeAttachmentAttempt(ctx, q, attemptID, session,
			token, ownerGeneration, "hard_expired", at, &out)
	})
	return out, err
}

func terminalizeAttachmentAttempt(ctx context.Context, q Q, attemptID, session,
	ownerToken, ownerGeneration, reason, expiryAt string,
	out *AttachmentAttemptTerminalReceipt,
) error {
	var surface, channelRef, storedToken, storedGeneration, hardDeadline string
	var failureCount, objectCount int
	err := q.QueryRowContext(ctx, `
		SELECT a.ingress_id,a.batch_id,a.generation,a.owner_token,a.owner_generation,
		       a.hard_deadline,i.failure_count,i.surface,b.channel_ref,ab.object_count
		FROM attachment_attempts a
		JOIN attachment_ingresses i ON i.id=a.ingress_id
		JOIN homie_bindings b ON b.id=i.binding_id
		JOIN attachment_batches ab ON ab.id=a.batch_id
		WHERE a.id=? AND a.session_id=? AND a.state='active'
		  AND i.state='publishing' AND ab.state='publishing'`,
		attemptID, session,
	).Scan(&out.IngressID, &out.BatchID, &out.Attempt, &storedToken,
		&storedGeneration, &hardDeadline, &failureCount, &surface, &channelRef,
		&objectCount)
	if err == sql.ErrNoRows {
		return Domainf("attachment attempt capability is stale or cannot be safely terminalized")
	}
	if err != nil {
		return err
	}
	if storedToken != ownerToken || storedGeneration != ownerGeneration {
		return Domainf("attachment attempt capability is stale or fenced")
	}
	if reason == "hard_expired" {
		if expiryAt == "" {
			var due bool
			if err := q.QueryRowContext(ctx,
				`SELECT datetime('now')>=?`, hardDeadline).Scan(&due); err != nil {
				return err
			}
			if !due {
				return Domainf("attachment attempt %q has not reached its hard deadline", attemptID)
			}
		} else if expiryAt < hardDeadline {
			return Domainf("attachment attempt %q has not reached its hard deadline", attemptID)
		}
	}

	out.AttemptID = attemptID
	out.Reason = reason
	out.Outcome = "requeued"
	payload := attachmentAttemptTerminalPayload{
		Code: "attachment_publish_retry", AttachmentIngressID: out.IngressID,
		AttemptID: attemptID, Attempt: out.Attempt, Outcome: out.Outcome,
		Reason: reason, Message: "Attachment publication attempt was fenced and queued for retry.",
		Body: "Attachment publication attempt was fenced and queued for retry.", Retryable: true,
	}
	if failureCount+1 >= 3 {
		out.Outcome = "failed"
		payload.Code = "attachment_publish_failed"
		payload.Outcome = out.Outcome
		payload.Message = "Attachment publication failed after three attempts."
		payload.Body = payload.Message
		payload.Retryable = false
	}
	receiptBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	out.Receipt = string(receiptBytes)

	var outboxID any
	if out.Outcome == "failed" {
		inserted, err := insertFanoutOutboxRow(ctx, q, "homie_reply",
			session, surface, channelRef, out.Receipt)
		if err != nil {
			return err
		}
		id, err := inserted.LastInsertId()
		if err != nil {
			return err
		}
		outboxID = id
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO attachment_batch_receipts
			(batch_id,attempt_id,ingress_id,session_id,generation,owner_generation,
			 outcome,reason,capability_sha256,receipt,outbox_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		out.BatchID, attemptID, out.IngressID, session, out.Attempt,
		ownerGeneration, out.Outcome, reason,
		attachmentCapabilityDigest(ownerToken), out.Receipt, outboxID); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `
		DELETE FROM attachment_object_publications
		WHERE publication_id IN (
			SELECT id FROM attachment_publications WHERE batch_id=?
		)`, out.BatchID); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE attachment_objects
		SET state='delete_pending',gc_generation=gc_generation+1
		WHERE state IN ('publishing','live')
		  AND NOT EXISTS (
		      SELECT 1 FROM attachment_object_publications e
		      WHERE e.session_id=attachment_objects.session_id
		        AND e.direction=attachment_objects.direction
		        AND e.sha256=attachment_objects.sha256
		        AND e.size=attachment_objects.size
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM conversation_attachment_refs r
		      WHERE r.session_id=attachment_objects.session_id
		        AND r.direction=attachment_objects.direction
		        AND r.sha256=attachment_objects.sha256
		        AND r.size=attachment_objects.size
		  )
		  AND EXISTS (
		      SELECT 1 FROM attachment_publications p
		      WHERE p.batch_id=? AND p.session_id=attachment_objects.session_id
		        AND p.direction=attachment_objects.direction
		        AND p.sha256=attachment_objects.sha256
		        AND p.size=attachment_objects.size
		  )`, out.BatchID); err != nil {
		return err
	}
	result, err := q.ExecContext(ctx, `
		UPDATE attachment_publications
		SET state='cleanup_pending',cleanup_from_state=state,
		    cleanup_kind='attempt_terminal',gc_generation=gc_generation+1
		WHERE batch_id=? AND state IN ('reserved','publishing','object_bound','materialized')`,
		out.BatchID)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err != nil || rows != int64(objectCount) {
		return Domainf("attachment terminal did not fence every publication ordinal")
	}
	result, err = q.ExecContext(ctx, `
		UPDATE attachment_batches SET state='cleanup_pending'
		WHERE id=? AND state='publishing'`, out.BatchID)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		return Domainf("attachment terminal lost its batch CAS")
	}
	if out.Outcome == "failed" {
		result, err = q.ExecContext(ctx, `
			UPDATE attachment_ingresses
			SET state='failed',failure_count=failure_count+1,failure_receipt=?
			WHERE id=? AND state='publishing' AND failure_count=2`,
			out.Receipt, out.IngressID)
	} else {
		result, err = q.ExecContext(ctx, `
			UPDATE attachment_ingresses
			SET state='queued',failure_count=failure_count+1
			WHERE id=? AND state='publishing' AND failure_count<2`,
			out.IngressID)
	}
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		return Domainf("attachment terminal lost its ingress CAS")
	}
	result, err = q.ExecContext(ctx, `
		UPDATE attachment_attempts SET state='cleanup_pending'
		WHERE id=? AND state='active'`, attemptID)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		return Domainf("attachment terminal lost its attempt CAS")
	}
	return nil
}

func loadAttachmentTerminalReplay(ctx context.Context, q Q, attemptID string,
	out *AttachmentAttemptTerminalReceipt,
) (bool, error) {
	err := q.QueryRowContext(ctx, `
		SELECT attempt_id,ingress_id,batch_id,generation,outcome,reason,receipt
		FROM attachment_batch_receipts WHERE attempt_id=?`, attemptID,
	).Scan(&out.AttemptID, &out.IngressID, &out.BatchID, &out.Attempt,
		&out.Outcome, &out.Reason, &out.Receipt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func attachmentCapabilityDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func safeAttachmentHex(v string, length int) bool {
	if len(v) != length {
		return false
	}
	for _, c := range []byte(v) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func safeAttachmentOwnerGeneration(v string) bool {
	if len(v) == 0 || len(v) > 255 || !utf8.ValidString(v) {
		return false
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func randomAttachmentHex(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", Usagef("allocate attachment reservation id: %v", err)
	}
	return hex.EncodeToString(buf), nil
}
