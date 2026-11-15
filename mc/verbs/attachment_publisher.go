package verbs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

type AttachmentPublicationPublishArgs struct {
	Session               string
	AttemptID             string
	AttemptOwnerToken     string
	PublicationID         string
	PublicationOwnerToken string
	OwnerGeneration       string
}

type AttachmentPublicationReceipt struct {
	PublicationID string `json:"publication_id"`
	SHA256        string `json:"sha256"`
	Size          int64  `json:"size"`
	State         string `json:"state"`
	Reused        bool   `json:"reused"`
	Device        string `json:"device"`
	Inode         string `json:"inode"`
	FileType      string `json:"file_type"`
}

type AttachmentPublicationCleanupArgs struct {
	Session       string
	PublicationID string
	GCGeneration  int
}

type AttachmentPublicationCleanupReceipt struct {
	PublicationID string `json:"publication_id"`
	State         string `json:"state"`
	GCGeneration  int    `json:"gc_generation"`
	Replayed      bool   `json:"replayed"`
}

type attachmentFileEvidence struct {
	device string
	inode  string
}

type attachmentPublicationPlan struct {
	batchID                                            int64
	ordinal                                            int
	size                                               int64
	state, digest, role                                string
	stagingName, journalTemp, journal                  string
	stagingDevice, stagingInode                        string
	journalDevice, journalInode                        string
	journalTempDevice, journalTempInode                string
	materializationReceipt                             string
	objectState, objectDevice, objectInode, objectType string
}

const (
	attachmentSQLLoadPublicationPlan = `
		SELECT p.batch_id,p.ordinal,p.size,p.state,p.sha256,p.object_role,
		       p.staging_name,p.journal_temp_name,p.journal_name,
		       p.staging_device,p.staging_inode,p.journal_temp_device,p.journal_temp_inode,
		       p.journal_device,p.journal_inode,p.materialization_receipt,
		       o.state,o.device,o.inode,o.file_type
		FROM attachment_publications p
		JOIN attachment_batches b ON b.id=p.batch_id
		JOIN attachment_attempts a ON a.batch_id=b.id
		LEFT JOIN attachment_objects o
		  ON o.session_id=p.session_id AND o.direction=p.direction
		 AND o.sha256=p.sha256 AND o.size=p.size
		WHERE p.id=? AND p.session_id=? AND p.direction='in'
		  AND p.owner_token=? AND p.owner_generation=?
		  AND a.id=? AND a.owner_token=? AND a.owner_generation=?
		  AND a.session_id=p.session_id AND a.state='active'
		  AND b.state='publishing'`
	attachmentSQLClaimStage = `
			UPDATE attachment_publications
			SET staging_device=?,staging_inode=?,staging_type='regular'
			WHERE id=? AND state='reserved'
			  AND staging_device IS NULL AND staging_inode IS NULL AND staging_type IS NULL`
	attachmentSQLRecordStage = `
			UPDATE attachment_publications
			SET sha256=?,staging_device=?,staging_inode=?,staging_type='regular',state='publishing'
			WHERE id=? AND session_id=? AND owner_token=? AND owner_generation=?
			  AND state='reserved'
			  AND staging_device=? AND staging_inode=? AND staging_type='regular'
			  AND datetime('now')<publisher_lease_until
			  AND datetime('now')<hard_deadline
			  AND EXISTS (
			      SELECT 1 FROM attachment_attempts a
			      WHERE a.id=? AND a.batch_id=attachment_publications.batch_id
			        AND a.session_id=attachment_publications.session_id
			        AND a.owner_token=? AND a.owner_generation=?
			        AND a.state='active' AND datetime('now')<a.publisher_lease_until
			        AND datetime('now')<a.hard_deadline
			  )`
	attachmentSQLRecordJournal = `
			UPDATE attachment_publications
			SET journal_device=?,journal_inode=?,journal_type='regular'
			WHERE id=? AND state='publishing'
			  AND journal_device IS NULL AND journal_inode IS NULL`
	attachmentSQLClaimJournalTemp = `
			UPDATE attachment_publications
			SET journal_temp_device=?,journal_temp_inode=?,journal_temp_type='regular'
			WHERE id=? AND state='publishing'
			  AND journal_temp_device IS NULL AND journal_temp_inode IS NULL
			  AND journal_temp_type IS NULL`
	attachmentSQLRequireLivePublisher = `
		SELECT 1 FROM attachment_publications p
		JOIN attachment_attempts a ON a.batch_id=p.batch_id
		JOIN attachment_batches b ON b.id=p.batch_id
		JOIN attachment_ingresses i ON i.id=a.ingress_id
		JOIN collie_sessions s ON s.id=p.session_id
		WHERE p.id=? AND p.session_id=? AND p.owner_token=? AND p.owner_generation=?
		  AND p.state=? AND datetime('now')<p.publisher_lease_until
		  AND datetime('now')<p.hard_deadline
		  AND a.id=? AND a.owner_token=? AND a.owner_generation=?
		  AND a.state='active' AND datetime('now')<a.publisher_lease_until
		  AND datetime('now')<a.hard_deadline
		  AND b.state='publishing' AND i.state='publishing' AND s.status='active'`
	attachmentSQLLoadObject = `
		SELECT state,device,inode FROM attachment_objects
		WHERE session_id=? AND direction='in' AND sha256=? AND size=?`
	attachmentSQLInsertObject = `
				INSERT INTO attachment_objects
					(session_id,direction,sha256,size,state,
					 creator_publication_id,creator_staging_name,creator_journal_name,
					 creator_staging_device,creator_staging_inode,creator_staging_type,
					 creator_journal_device,creator_journal_inode,creator_journal_type)
				VALUES (?,'in',?,?,'publishing',?,?,?,?,?,'regular',?,?,'regular')`
	attachmentSQLInsertObjectPublication = `
			INSERT INTO attachment_object_publications
				(publication_id,session_id,direction,sha256,size)
			VALUES (?,?,'in',?,?)`
	attachmentSQLBindPublicationObject = `
			UPDATE attachment_publications SET state='object_bound',object_role=?
			WHERE id=? AND state='publishing'
			  AND journal_device=? AND journal_inode=? AND journal_type='regular'`
	attachmentSQLLoadLiveObjectIdentity = `
				SELECT device,inode FROM attachment_objects
				WHERE session_id=? AND direction='in' AND sha256=? AND size=? AND state='live'`
	attachmentSQLMaterializeObject = `
				UPDATE attachment_objects
				SET state='live',device=?,inode=?,file_type='regular'
				WHERE session_id=? AND direction='in' AND sha256=? AND size=?
				  AND state='publishing' AND creator_publication_id=?`
	attachmentSQLCollapseCreatorEvidence = `
				UPDATE attachment_objects
				SET creator_publication_id=NULL,
				    creator_staging_name=NULL,creator_journal_name=NULL,
				    creator_staging_device=NULL,creator_staging_inode=NULL,
				    creator_staging_type=NULL,
				    creator_journal_device=NULL,creator_journal_inode=NULL,
				    creator_journal_type=NULL
				WHERE session_id=? AND direction='in' AND sha256=? AND size=?
				  AND state='live' AND creator_publication_id=?`
	attachmentSQLMaterializePublication = `
			UPDATE attachment_publications
			SET state='materialized',materialization_receipt=?
			WHERE id=? AND state='object_bound' AND object_role=?`
	attachmentSQLCleanupPublication = `
		SELECT state,staging_name,journal_temp_name,journal_name,sha256,size,
		       staging_device,staging_inode,journal_temp_device,journal_temp_inode,
		       journal_device,journal_inode
		FROM attachment_publications
		WHERE id=? AND session_id=? AND gc_generation=?`
	attachmentSQLCleanupMarkAborted = `
			UPDATE attachment_publications SET state='aborted'
			WHERE id=? AND session_id=? AND state='cleanup_pending'
			  AND gc_generation=?
			  AND NOT EXISTS (
			      SELECT 1 FROM attachment_object_publications e
			      WHERE e.publication_id=attachment_publications.id
			  )`
	attachmentSQLCleanupObject = `
		SELECT state,gc_generation,device,inode,creator_staging_device,creator_staging_inode
		FROM attachment_objects
		WHERE session_id=? AND direction='in' AND sha256=? AND size=?`
	attachmentSQLCleanupDeleteObject = `
			DELETE FROM attachment_objects
			WHERE session_id=? AND direction='in' AND sha256=? AND size=?
			  AND state='delete_pending' AND gc_generation=?
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
			  )`
)

// AttachmentPublicationQueryAllowed and AttachmentPublicationMutationAllowed
// close the composed publisher's helper protocol over the exact statements
// compiled into this package. Keeping the two sets disjoint prevents a
// mutation from riding the query operation's intentional autocommit reads.
func AttachmentPublicationQueryAllowed(statement string) bool {
	switch statement {
	case attachmentSQLLoadPublicationPlan,
		attachmentSQLRequireLivePublisher,
		attachmentSQLLoadObject,
		attachmentSQLLoadLiveObjectIdentity:
		return true
	default:
		return false
	}
}

func AttachmentPublicationMutationAllowed(statement string) bool {
	switch statement {
	case
		attachmentSQLClaimStage,
		attachmentSQLRecordStage,
		attachmentSQLRecordJournal,
		attachmentSQLClaimJournalTemp,
		attachmentSQLInsertObject,
		attachmentSQLInsertObjectPublication,
		attachmentSQLBindPublicationObject,
		attachmentSQLMaterializeObject,
		attachmentSQLCollapseCreatorEvidence,
		attachmentSQLMaterializePublication:
		return true
	default:
		return false
	}
}

type attachmentPublicationJournal struct {
	Version         int    `json:"version"`
	PublicationID   string `json:"publication_id"`
	AttemptID       string `json:"attempt_id"`
	BatchID         int64  `json:"batch_id"`
	Session         string `json:"session_id"`
	Direction       string `json:"direction"`
	Ordinal         int    `json:"ordinal"`
	Size            int64  `json:"size"`
	SHA256          string `json:"sha256"`
	StagingName     string `json:"staging_name"`
	StagingDevice   string `json:"staging_device"`
	StagingInode    string `json:"staging_inode"`
	CanonicalName   string `json:"canonical_name"`
	OwnerGeneration string `json:"owner_generation"`
}

type attachmentTree struct {
	home, attachments, session, inbound int
	staging, journal, published, sha256 int
}

func (t *attachmentTree) close() error {
	var errs []error
	for _, fd := range []int{
		t.sha256, t.published, t.journal, t.staging,
		t.inbound, t.session, t.attachments, t.home,
	} {
		if fd >= 0 {
			errs = append(errs, unix.Close(fd))
		}
	}
	return errors.Join(errs...)
}

// AttachmentPublicationPublish is the host-only byte publisher. The caller
// supplies bytes and an exact reservation capability, never a host path. The
// durable journal and spine liveness edge precede canonical materialization.
func AttachmentPublicationPublish(
	db *sql.DB,
	id *RunIdentity,
	home string,
	a AttachmentPublicationPublishArgs,
	source io.Reader,
) (out AttachmentPublicationReceipt, retErr error) {
	if err := RequireHostScope(id, "mc attachment publication-publish"); err != nil {
		return out, err
	}
	if a.Session == "" || !safeAttachmentHex(a.AttemptID, 16) ||
		!safeAttachmentHex(a.AttemptOwnerToken, 32) ||
		!safeAttachmentHex(a.PublicationID, 16) ||
		!safeAttachmentHex(a.PublicationOwnerToken, 32) ||
		!safeAttachmentOwnerGeneration(a.OwnerGeneration) {
		return out, Usagef("attachment publication-publish requires exact session, attempt/publication capabilities, and native generation")
	}
	if source == nil {
		return out, Usagef("attachment publication-publish requires a byte stream")
	}
	plan, err := loadAttachmentPublicationPlan(db, a)
	if err != nil {
		return out, err
	}
	tree, err := openAttachmentTree(home, a.Session)
	if err != nil {
		return out, err
	}
	defer func() {
		retErr = errors.Join(retErr, tree.close())
	}()
	out = AttachmentPublicationReceipt{
		PublicationID: a.PublicationID, Size: plan.size, State: plan.state,
		SHA256: plan.digest, Reused: plan.role == "reuse",
	}
	if plan.state == "materialized" {
		if plan.objectState != "live" || plan.objectType != "regular" ||
			plan.objectDevice == "" || plan.objectInode == "" {
			return out, Domainf("materialized attachment lacks its exact registered live object")
		}
		if _, err := verifyAttachmentCanonical(
			tree, plan.digest, plan.size, plan.objectDevice, plan.objectInode); err != nil {
			return out, fmt.Errorf("verify replayed attachment canonical object: %w", err)
		}
		out, err = decodeAttachmentPublicationReceipt(a.PublicationID, plan)
		if err != nil {
			return out, Domainf("materialized attachment receipt is absent or malformed")
		}
		return out, nil
	}
	if plan.state != "reserved" && plan.state != "publishing" && plan.state != "object_bound" {
		return out, Domainf("attachment publication %q is not at a resumable publisher boundary", a.PublicationID)
	}

	if plan.state == "reserved" {
		stageFile, stage, err := claimAttachmentStage(db, tree, a, plan)
		if err != nil {
			return out, err
		}
		digest, err := writeClaimedAttachmentStage(
			stageFile, tree.staging, plan.stagingName, plan.size, source, stage)
		if err != nil {
			return out, err
		}
		if plan.digest != "" && plan.digest != digest {
			_ = unlinkAttachmentEvidence(tree.staging, plan.stagingName, stage)
			return out, Domainf("attachment publication digest changed across ingress retry")
		}
		if err := recordAttachmentStage(db, a, digest, stage); err != nil {
			cleanupErr := removeOptionalAttachmentFile(
				tree.staging, plan.stagingName, stage.device, stage.inode, false)
			return out, errors.Join(err, cleanupErr)
		}
		plan, err = loadAttachmentPublicationPlan(db, a)
		if err != nil {
			return out, err
		}
	}
	if plan.state == "publishing" {
		if _, err := verifyAttachmentStage(tree.staging, plan); err != nil {
			return out, fmt.Errorf("verify resumed attachment stage: %w", err)
		}
		journalBody, err := json.Marshal(attachmentPublicationJournal{
			Version: 1, PublicationID: a.PublicationID, AttemptID: a.AttemptID,
			BatchID: plan.batchID, Session: a.Session, Direction: "in",
			Ordinal: plan.ordinal, Size: plan.size, SHA256: plan.digest,
			StagingName: plan.stagingName, StagingDevice: plan.stagingDevice,
			StagingInode:    plan.stagingInode,
			CanonicalName:   "sha256/" + plan.digest[:2] + "/" + plan.digest,
			OwnerGeneration: a.OwnerGeneration,
		})
		if err != nil {
			return out, err
		}
		journalEvidence, err := publishAttachmentJournal(db, tree, a, plan, journalBody)
		if err != nil {
			return out, err
		}
		plan.journalDevice, plan.journalInode = journalEvidence.device, journalEvidence.inode
		if _, err := bindAttachmentObject(db, tree, a, plan); err != nil {
			return out, err
		}
		plan, err = loadAttachmentPublicationPlan(db, a)
		if err != nil {
			return out, err
		}
	}
	reused := plan.role == "reuse"
	if err := materializeAttachmentObject(db, tree, a, plan, reused); err != nil {
		return out, err
	}
	plan, err = loadAttachmentPublicationPlan(db, a)
	if err != nil {
		return out, err
	}
	out, err = decodeAttachmentPublicationReceipt(a.PublicationID, plan)
	if err != nil {
		return out, Domainf("materialized attachment receipt is absent or malformed")
	}
	return out, nil
}

func decodeAttachmentPublicationReceipt(
	publicationID string,
	plan attachmentPublicationPlan,
) (AttachmentPublicationReceipt, error) {
	var receipt AttachmentPublicationReceipt
	if err := json.Unmarshal([]byte(plan.materializationReceipt), &receipt); err != nil {
		return receipt, err
	}
	if receipt.PublicationID != publicationID ||
		receipt.SHA256 != plan.digest ||
		receipt.Size != plan.size ||
		receipt.State != "materialized" ||
		receipt.Reused != (plan.role == "reuse") ||
		receipt.Device != plan.objectDevice ||
		receipt.Inode != plan.objectInode ||
		receipt.FileType != plan.objectType {
		return receipt, Domainf("materialized attachment receipt does not match registered canonical identity")
	}
	return receipt, nil
}

// AttachmentPublicationCleanup completes one terminalizer-owned cleanup CAS.
// It probes only the exact reserved names and registered identities. Missing
// entries are accepted on replay; a different inode, type, owner, link count,
// or mode is never removed.
func AttachmentPublicationCleanup(
	db *sql.DB,
	id *RunIdentity,
	home string,
	a AttachmentPublicationCleanupArgs,
) (out AttachmentPublicationCleanupReceipt, retErr error) {
	if err := RequireHostScope(id, "mc attachment publication-cleanup"); err != nil {
		return out, err
	}
	if a.Session == "" || !safeAttachmentHex(a.PublicationID, 16) || a.GCGeneration < 1 {
		return out, Usagef("attachment publication-cleanup requires an exact session, publication id, and positive GC generation")
	}
	out = AttachmentPublicationCleanupReceipt{
		PublicationID: a.PublicationID, GCGeneration: a.GCGeneration,
	}
	var state, stagingName, journalTemp, journalName string
	var digest, stagingDevice, stagingInode sql.NullString
	var journalTempDevice, journalTempInode, journalDevice, journalInode sql.NullString
	var size int64
	err := db.QueryRow(attachmentSQLCleanupPublication,
		a.PublicationID, a.Session, a.GCGeneration,
	).Scan(&state, &stagingName, &journalTemp, &journalName, &digest, &size,
		&stagingDevice, &stagingInode, &journalTempDevice, &journalTempInode,
		&journalDevice, &journalInode)
	if err == sql.ErrNoRows {
		return out, Domainf("attachment publication cleanup generation is stale or unknown")
	}
	if err != nil {
		return out, err
	}
	wasAborted := state == "aborted"
	if !wasAborted && state != "cleanup_pending" {
		return out, Domainf("attachment publication is not owned by terminal cleanup")
	}
	tree, err := openAttachmentTree(home, a.Session)
	if err != nil {
		return out, err
	}
	defer func() {
		retErr = errors.Join(retErr, tree.close())
	}()
	if err := removeOptionalAttachmentFile(
		tree.staging, stagingName, nullString(stagingDevice), nullString(stagingInode), true); err != nil {
		return out, fmt.Errorf("remove exact attachment stage: %w", err)
	}
	if err := removeOptionalAttachmentFile(
		tree.journal, journalTemp,
		nullString(journalTempDevice), nullString(journalTempInode), true); err != nil {
		return out, fmt.Errorf("remove exact attachment journal temp: %w", err)
	}
	finalDevice, finalInode := nullString(journalDevice), nullString(journalInode)
	if finalDevice == "" {
		finalDevice, finalInode = nullString(journalTempDevice), nullString(journalTempInode)
	}
	if err := removeOptionalAttachmentFile(
		tree.journal, journalName, finalDevice, finalInode, false); err != nil {
		return out, fmt.Errorf("remove exact attachment final journal: %w", err)
	}
	if digest.Valid {
		if err := cleanupAttachmentCanonicalObject(db, tree, a.Session, digest.String, size); err != nil {
			return out, err
		}
	}
	if wasAborted {
		out.State, out.Replayed = state, true
		return out, nil
	}
	err = inTx(db, func(ctx context.Context, q Q) error {
		result, err := q.ExecContext(ctx, attachmentSQLCleanupMarkAborted,
			a.PublicationID, a.Session, a.GCGeneration)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			return Domainf("attachment publication cleanup lost its generation CAS")
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	out.State = "aborted"
	return out, nil
}

func nullString(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func removeOptionalAttachmentFile(parent int, name, device, inode string, allowWritable bool) error {
	var stat unix.Stat_t
	if err := unix.Fstatat(parent, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return err
	}
	mode := stat.Mode & 0o777
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || int(stat.Uid) != os.Getuid() ||
		stat.Nlink != 1 || (mode != 0o444 && (!allowWritable || mode != 0o600)) {
		return Domainf("attachment cleanup target has unsafe type, owner, mode, or link count")
	}
	gotDevice := strconv.FormatUint(uint64(stat.Dev), 10)
	gotInode := strconv.FormatUint(uint64(stat.Ino), 10)
	if (device != "" && gotDevice != device) || (inode != "" && gotInode != inode) {
		return Domainf("attachment cleanup target identity drifted")
	}
	if err := unix.Unlinkat(parent, name, 0); err != nil {
		return err
	}
	return unix.Fsync(parent)
}

func cleanupAttachmentCanonicalObject(
	db *sql.DB,
	tree *attachmentTree,
	session, digest string,
	size int64,
) error {
	var state string
	var generation int
	var device, inode, creatorDevice, creatorInode sql.NullString
	err := db.QueryRow(attachmentSQLCleanupObject,
		session, digest, size,
	).Scan(&state, &generation, &device, &inode, &creatorDevice, &creatorInode)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if state != "delete_pending" {
		return nil
	}
	shard, err := openOrCreateAttachmentDirectory(tree.sha256, digest[:2])
	if err != nil {
		return err
	}
	defer unix.Close(shard)
	expectedDevice, expectedInode := nullString(device), nullString(inode)
	if expectedDevice == "" {
		expectedDevice, expectedInode = nullString(creatorDevice), nullString(creatorInode)
	}
	exists, evidence, err := attachmentEntryEvidence(shard, digest, size)
	if err != nil {
		return err
	}
	if exists {
		if evidence.device != expectedDevice || evidence.inode != expectedInode {
			return Domainf("delete-pending attachment canonical identity drifted")
		}
		if _, err := verifyAttachmentFile(
			shard, digest, size, evidence.device, evidence.inode, digest, nil); err != nil {
			return err
		}
		if err := unix.Unlinkat(shard, digest, 0); err != nil {
			return err
		}
		if err := unix.Fsync(shard); err != nil {
			return err
		}
	}
	return inTx(db, func(ctx context.Context, q Q) error {
		result, err := q.ExecContext(ctx, attachmentSQLCleanupDeleteObject,
			session, digest, size, generation)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			return Domainf("attachment object cleanup lost its generation CAS")
		}
		return nil
	})
}

func loadAttachmentPublicationPlan(db *sql.DB, a AttachmentPublicationPublishArgs) (attachmentPublicationPlan, error) {
	var p attachmentPublicationPlan
	var digest, role, materializationReceipt sql.NullString
	var stagingDevice, stagingInode, journalTempDevice, journalTempInode sql.NullString
	var journalDevice, journalInode sql.NullString
	var objectState, objectDevice, objectInode, objectType sql.NullString
	err := db.QueryRow(attachmentSQLLoadPublicationPlan,
		a.PublicationID, a.Session, a.PublicationOwnerToken, a.OwnerGeneration,
		a.AttemptID, a.AttemptOwnerToken, a.OwnerGeneration,
	).Scan(&p.batchID, &p.ordinal, &p.size, &p.state, &digest, &role,
		&p.stagingName, &p.journalTemp, &p.journal,
		&stagingDevice, &stagingInode, &journalTempDevice, &journalTempInode,
		&journalDevice, &journalInode, &materializationReceipt,
		&objectState, &objectDevice, &objectInode, &objectType)
	if err == sql.ErrNoRows {
		return p, Domainf("attachment publication capability is stale or fenced")
	}
	if err != nil {
		return p, err
	}
	if digest.Valid {
		p.digest = digest.String
	}
	if role.Valid {
		p.role = role.String
	}
	if stagingDevice.Valid {
		p.stagingDevice, p.stagingInode = stagingDevice.String, stagingInode.String
	}
	if journalTempDevice.Valid {
		p.journalTempDevice, p.journalTempInode = journalTempDevice.String, journalTempInode.String
	}
	if journalDevice.Valid {
		p.journalDevice, p.journalInode = journalDevice.String, journalInode.String
	}
	if materializationReceipt.Valid {
		p.materializationReceipt = materializationReceipt.String
	}
	if objectState.Valid {
		p.objectState, p.objectDevice = objectState.String, objectDevice.String
		p.objectInode, p.objectType = objectInode.String, objectType.String
	}
	return p, nil
}

func openAttachmentTree(home, session string) (*attachmentTree, error) {
	if !safeAttachmentPathSegment(session) {
		return nil, Domainf("attachment session id is not an exact canonical id")
	}
	absolute, err := filepath.Abs(home)
	if err != nil || filepath.Clean(absolute) != absolute {
		return nil, fmt.Errorf("resolve attachment MC_HOME: %w", err)
	}
	tree := &attachmentTree{
		home: -1, attachments: -1, session: -1, inbound: -1,
		staging: -1, journal: -1, published: -1, sha256: -1,
	}
	tree.home, err = unix.Open(absolute,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open attachment MC_HOME: %w", err)
	}
	if err := requireAttachmentDirectory(tree.home, "attachment MC_HOME"); err != nil {
		tree.close()
		return nil, err
	}
	if tree.attachments, err = openOrCreateAttachmentDirectory(tree.home, "attachments"); err != nil {
		tree.close()
		return nil, err
	}
	if tree.session, err = openOrCreateAttachmentDirectory(tree.attachments, session); err != nil {
		tree.close()
		return nil, err
	}
	for _, direction := range []string{"in", "out"} {
		directionFD, err := openOrCreateAttachmentDirectory(tree.session, direction)
		if err != nil {
			tree.close()
			return nil, err
		}
		staging, journal, published, shaFD := -1, -1, -1, -1
		if staging, err = openOrCreateAttachmentDirectory(directionFD, "staging"); err == nil {
			journal, err = openOrCreateAttachmentDirectory(directionFD, "journal")
		}
		if err == nil {
			published, err = openOrCreateAttachmentDirectory(directionFD, "published")
		}
		if err == nil {
			shaFD, err = openOrCreateAttachmentDirectory(published, "sha256")
		}
		if direction == "in" && err == nil {
			tree.inbound, tree.staging, tree.journal = directionFD, staging, journal
			tree.published, tree.sha256 = published, shaFD
			continue
		}
		for _, fd := range []int{shaFD, published, journal, staging, directionFD} {
			if fd >= 0 {
				_ = unix.Close(fd)
			}
		}
		if err != nil {
			tree.close()
			return nil, err
		}
	}
	return tree, nil
}

type AttachmentTreeReceipt struct {
	Session          string `json:"session"`
	InboundPublished string `json:"inbound_published"`
	Outbound         string `json:"outbound"`
}

// PrepareAttachmentTree materializes and revalidates the stable per-session
// two-direction tree before the resident creates the Collie's first launch
// artifact. It is host-only and idempotent; a symlink, widened directory, or
// non-operator owner refuses rather than being repaired in place.
func PrepareAttachmentTree(
	id *RunIdentity,
	home string,
	session string,
) (AttachmentTreeReceipt, error) {
	var receipt AttachmentTreeReceipt
	if err := RequireHostScope(id, "attachment tree preparation"); err != nil {
		return receipt, err
	}
	tree, err := openAttachmentTree(home, session)
	if err != nil {
		return receipt, err
	}
	if err := tree.close(); err != nil {
		return receipt, err
	}
	root := filepath.Join(home, "attachments", session)
	return AttachmentTreeReceipt{
		Session:          session,
		InboundPublished: filepath.Join(root, "in", "published"),
		Outbound:         filepath.Join(root, "out"),
	}, nil
}

func safeAttachmentPathSegment(value string) bool {
	if len(value) < 1 || len(value) > 64 || !utf8.ValidString(value) ||
		value == "." || value == ".." || filepath.Base(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == '\\' {
			return false
		}
	}
	return true
}

func openOrCreateAttachmentDirectory(parent int, name string) (int, error) {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return -1, fmt.Errorf("refuse unsafe attachment directory name %q", name)
	}
	created := false
	if err := unix.Mkdirat(parent, name, 0o700); err == nil {
		created = true
	} else if !errors.Is(err, unix.EEXIST) {
		return -1, fmt.Errorf("create attachment directory %q: %w", name, err)
	}
	fd, err := unix.Openat(parent, name,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("open attachment directory %q: %w", name, err)
	}
	if err := requireAttachmentDirectory(fd, name); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	if created {
		if err := unix.Fsync(parent); err != nil {
			_ = unix.Close(fd)
			return -1, fmt.Errorf("sync attachment directory parent: %w", err)
		}
	}
	return fd, nil
}

func requireAttachmentDirectory(fd int, label string) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("inspect %s: %w", label, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o777 != 0o700 ||
		int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("%s must be an operator-owned mode-0700 directory", label)
	}
	return nil
}

func claimAttachmentStage(
	db *sql.DB,
	tree *attachmentTree,
	a AttachmentPublicationPublishArgs,
	plan attachmentPublicationPlan,
) (*os.File, attachmentFileEvidence, error) {
	var file *os.File
	var evidence attachmentFileEvidence
	err := inTx(db, func(ctx context.Context, q Q) error {
		if err := requireLiveAttachmentPublisher(ctx, q, a, "reserved"); err != nil {
			return err
		}
		fd, err := unix.Openat(tree.staging, plan.stagingName,
			unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if err != nil {
			return fmt.Errorf("create exact attachment stage: %w", err)
		}
		file = os.NewFile(uintptr(fd), plan.stagingName)
		evidence, err = attachmentRegularEvidence(fd, 0, 0o600)
		if err != nil {
			return err
		}
		if err := unix.Fsync(tree.staging); err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, attachmentSQLClaimStage,
			evidence.device, evidence.inode, a.PublicationID)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			return Domainf("attachment stage claim lost its publication CAS")
		}
		return nil
	})
	if err != nil {
		if file != nil {
			_ = file.Close()
			_ = removeOptionalAttachmentFile(
				tree.staging, plan.stagingName, evidence.device, evidence.inode, true)
		}
		return nil, attachmentFileEvidence{}, err
	}
	return file, evidence, nil
}

func writeClaimedAttachmentStage(
	file *os.File,
	parent int,
	name string,
	size int64,
	source io.Reader,
	evidence attachmentFileEvidence,
) (string, error) {
	fd := int(file.Fd())
	success := false
	defer func() {
		if !success {
			_ = file.Close()
			_ = removeOptionalAttachmentFile(
				parent, name, evidence.device, evidence.inode, true)
		}
	}()
	hash := sha256.New()
	written, err := io.CopyN(io.MultiWriter(file, hash), source, size)
	if err != nil || written != size {
		return "", Domainf("attachment byte stream ended before its declared size")
	}
	extra, err := io.ReadAll(io.LimitReader(source, 1))
	if err != nil {
		return "", fmt.Errorf("read attachment size sentinel: %w", err)
	}
	if len(extra) != 0 {
		return "", Domainf("attachment byte stream exceeds its declared size")
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("sync attachment stage bytes: %w", err)
	}
	if err := unix.Fchmod(fd, 0o444); err != nil {
		return "", fmt.Errorf("seal attachment stage mode: %w", err)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("sync sealed attachment stage: %w", err)
	}
	sealed, err := attachmentRegularEvidence(fd, size, 0o444)
	if err != nil {
		return "", err
	}
	if sealed != evidence {
		return "", Domainf("attachment staging identity changed while streaming")
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close attachment stage: %w", err)
	}
	success = true
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func attachmentRegularEvidence(fd int, size int64, mode uint32) (attachmentFileEvidence, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return attachmentFileEvidence{}, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || uint32(stat.Mode&0o777) != mode ||
		int(stat.Uid) != os.Getuid() || stat.Nlink != 1 || stat.Size != size {
		return attachmentFileEvidence{}, fmt.Errorf(
			"attachment file must be a singly linked operator-owned mode-%04o regular file of exact size", mode)
	}
	return attachmentFileEvidence{
		device: strconv.FormatUint(uint64(stat.Dev), 10),
		inode:  strconv.FormatUint(uint64(stat.Ino), 10),
	}, nil
}

func recordAttachmentStage(db *sql.DB, a AttachmentPublicationPublishArgs, digest string, evidence attachmentFileEvidence) error {
	return inTx(db, func(ctx context.Context, q Q) error {
		result, err := q.ExecContext(ctx, attachmentSQLRecordStage,
			digest, evidence.device, evidence.inode,
			a.PublicationID, a.Session, a.PublicationOwnerToken, a.OwnerGeneration,
			evidence.device, evidence.inode,
			a.AttemptID, a.AttemptOwnerToken, a.OwnerGeneration)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			return Domainf("attachment publication lost its live stage ownership CAS")
		}
		return nil
	})
}

func publishAttachmentJournal(
	db *sql.DB,
	tree *attachmentTree,
	a AttachmentPublicationPublishArgs,
	plan attachmentPublicationPlan,
	body []byte,
) (attachmentFileEvidence, error) {
	finalExists, _, err := attachmentEntryEvidence(
		tree.journal, plan.journal, int64(len(body)))
	if err != nil {
		return attachmentFileEvidence{}, err
	}
	tempExists, tempEvidence, err := attachmentEntryEvidence(
		tree.journal, plan.journalTemp, int64(len(body)))
	if err != nil {
		return attachmentFileEvidence{}, err
	}
	if finalExists && tempExists {
		return attachmentFileEvidence{}, Domainf("attachment journal recovery found both temp and final names")
	}
	if finalExists {
		expectedDevice, expectedInode := plan.journalDevice, plan.journalInode
		if expectedDevice == "" {
			expectedDevice, expectedInode = plan.journalTempDevice, plan.journalTempInode
		}
		final, err := verifyAttachmentFile(
			tree.journal, plan.journal, int64(len(body)),
			expectedDevice, expectedInode, "", body)
		if err != nil {
			return attachmentFileEvidence{}, err
		}
		if err := recordAttachmentJournalEvidence(db, a, plan, final); err != nil {
			return attachmentFileEvidence{}, err
		}
		return final, nil
	}
	if tempExists {
		if plan.journalTempDevice == "" ||
			tempEvidence.device != plan.journalTempDevice ||
			tempEvidence.inode != plan.journalTempInode {
			return attachmentFileEvidence{}, Domainf("attachment journal temp identity drifted")
		}
		if _, err := verifyAttachmentFile(
			tree.journal, plan.journalTemp, int64(len(body)),
			tempEvidence.device, tempEvidence.inode, "", body); err != nil {
			return attachmentFileEvidence{}, err
		}
	} else {
		tempFile, claimed, err := claimAttachmentJournalTemp(db, tree, a, plan)
		if err != nil {
			return attachmentFileEvidence{}, err
		}
		if err := writeClaimedAttachmentSealedFile(
			tempFile, tree.journal, plan.journalTemp, body, claimed); err != nil {
			return attachmentFileEvidence{}, err
		}
		tempEvidence = claimed
	}
	var final attachmentFileEvidence
	err = inTx(db, func(ctx context.Context, q Q) error {
		if err := requireLiveAttachmentPublisher(ctx, q, a, "publishing"); err != nil {
			return err
		}
		if err := attachmentRenameNoReplace(
			tree.journal, plan.journalTemp, tree.journal, plan.journal); err != nil {
			return fmt.Errorf("publish exact attachment journal: %w", err)
		}
		if err := unix.Fsync(tree.journal); err != nil {
			return fmt.Errorf("sync attachment journal directory: %w", err)
		}
		final, err = verifyAttachmentFile(tree.journal, plan.journal, int64(len(body)),
			tempEvidence.device, tempEvidence.inode, "", body)
		if err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, attachmentSQLRecordJournal,
			final.device, final.inode, a.PublicationID)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			return Domainf("attachment journal identity lost its publication CAS")
		}
		return nil
	})
	if err != nil {
		cleanupErr := removeOptionalAttachmentFile(
			tree.journal, plan.journalTemp,
			tempEvidence.device, tempEvidence.inode, false)
		return attachmentFileEvidence{}, errors.Join(err, cleanupErr)
	}
	return final, err
}

func recordAttachmentJournalEvidence(
	db *sql.DB,
	a AttachmentPublicationPublishArgs,
	plan attachmentPublicationPlan,
	evidence attachmentFileEvidence,
) error {
	return inTx(db, func(ctx context.Context, q Q) error {
		if err := requireLiveAttachmentPublisher(ctx, q, a, "publishing"); err != nil {
			return err
		}
		if plan.journalDevice != "" {
			if plan.journalDevice != evidence.device || plan.journalInode != evidence.inode {
				return Domainf("attachment final journal identity drifted")
			}
			return nil
		}
		result, err := q.ExecContext(ctx, attachmentSQLRecordJournal,
			evidence.device, evidence.inode, a.PublicationID)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			return Domainf("attachment journal identity lost its recovery CAS")
		}
		return nil
	})
}

func claimAttachmentJournalTemp(
	db *sql.DB,
	tree *attachmentTree,
	a AttachmentPublicationPublishArgs,
	plan attachmentPublicationPlan,
) (*os.File, attachmentFileEvidence, error) {
	var file *os.File
	var evidence attachmentFileEvidence
	err := inTx(db, func(ctx context.Context, q Q) error {
		if err := requireLiveAttachmentPublisher(ctx, q, a, "publishing"); err != nil {
			return err
		}
		fd, err := unix.Openat(tree.journal, plan.journalTemp,
			unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if err != nil {
			return fmt.Errorf("create exact attachment journal temp: %w", err)
		}
		file = os.NewFile(uintptr(fd), plan.journalTemp)
		evidence, err = attachmentRegularEvidence(fd, 0, 0o600)
		if err != nil {
			return err
		}
		if err := unix.Fsync(tree.journal); err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, attachmentSQLClaimJournalTemp,
			evidence.device, evidence.inode, a.PublicationID)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			return Domainf("attachment journal temp claim lost its publication CAS")
		}
		return nil
	})
	if err != nil {
		if file != nil {
			_ = file.Close()
			_ = removeOptionalAttachmentFile(
				tree.journal, plan.journalTemp, evidence.device, evidence.inode, true)
		}
		return nil, attachmentFileEvidence{}, err
	}
	return file, evidence, nil
}

func writeClaimedAttachmentSealedFile(
	file *os.File,
	parent int,
	name string,
	body []byte,
	evidence attachmentFileEvidence,
) error {
	fd := int(file.Fd())
	success := false
	defer func() {
		if !success {
			_ = file.Close()
			_ = removeOptionalAttachmentFile(
				parent, name, evidence.device, evidence.inode, true)
		}
	}()
	if written, err := file.Write(body); err != nil {
		return err
	} else if written != len(body) {
		return io.ErrShortWrite
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := unix.Fchmod(fd, 0o444); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	sealed, err := attachmentRegularEvidence(fd, int64(len(body)), 0o444)
	if err != nil {
		return err
	}
	if sealed != evidence {
		return Domainf("attachment journal temp identity changed while writing")
	}
	if err := file.Close(); err != nil {
		return err
	}
	success = true
	return nil
}

func requireLiveAttachmentPublisher(ctx context.Context, q Q, a AttachmentPublicationPublishArgs, state string) error {
	var one int
	err := q.QueryRowContext(ctx, attachmentSQLRequireLivePublisher,
		a.PublicationID, a.Session, a.PublicationOwnerToken, a.OwnerGeneration, state,
		a.AttemptID, a.AttemptOwnerToken, a.OwnerGeneration,
	).Scan(&one)
	if err == sql.ErrNoRows {
		return Domainf("attachment publication capability is stale, expired, or fenced")
	}
	return err
}

func bindAttachmentObject(
	db *sql.DB,
	tree *attachmentTree,
	a AttachmentPublicationPublishArgs,
	plan attachmentPublicationPlan,
) (reused bool, err error) {
	var preState string
	var preDevice, preInode sql.NullString
	err = db.QueryRow(attachmentSQLLoadObject,
		a.Session, plan.digest, plan.size).Scan(&preState, &preDevice, &preInode)
	if err == nil {
		if preState != "live" || !preDevice.Valid || !preInode.Valid {
			return false, Domainf("attachment canonical object is publishing, delete-pending, or unregistered")
		}
		if _, err := verifyAttachmentCanonical(
			tree, plan.digest, plan.size, preDevice.String, preInode.String); err != nil {
			return false, err
		}
		reused = true
	} else if err == sql.ErrNoRows {
		reused = false
	} else {
		return false, err
	}
	err = inTx(db, func(ctx context.Context, q Q) error {
		if err := requireLiveAttachmentPublisher(ctx, q, a, "publishing"); err != nil {
			return err
		}
		var state string
		var device, inode sql.NullString
		err := q.QueryRowContext(ctx, attachmentSQLLoadObject,
			a.Session, plan.digest, plan.size).Scan(&state, &device, &inode)
		switch err {
		case sql.ErrNoRows:
			if reused {
				return Domainf("attachment canonical object disappeared after fresh verification")
			}
			_, err = q.ExecContext(ctx, attachmentSQLInsertObject,
				a.Session, plan.digest, plan.size, a.PublicationID,
				plan.stagingName, plan.journal, plan.stagingDevice, plan.stagingInode,
				plan.journalDevice, plan.journalInode)
			if err != nil {
				return err
			}
		case nil:
			if !reused || state != "live" || !device.Valid || !inode.Valid ||
				device.String != preDevice.String || inode.String != preInode.String {
				return Domainf("attachment canonical object is publishing, delete-pending, or unregistered")
			}
			if err := verifyAttachmentCanonicalIdentity(
				tree, plan.digest, plan.size, device.String, inode.String); err != nil {
				return err
			}
		default:
			return err
		}
		if _, err := q.ExecContext(ctx, attachmentSQLInsertObjectPublication,
			a.PublicationID, a.Session, plan.digest, plan.size); err != nil {
			return err
		}
		role := "creator"
		if reused {
			role = "reuse"
		}
		result, err := q.ExecContext(ctx, attachmentSQLBindPublicationObject,
			role, a.PublicationID, plan.journalDevice, plan.journalInode)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			return Domainf("attachment publication lost its object-binding CAS")
		}
		return nil
	})
	return reused, err
}

func materializeAttachmentObject(
	db *sql.DB,
	tree *attachmentTree,
	a AttachmentPublicationPublishArgs,
	plan attachmentPublicationPlan,
	reused bool,
) error {
	shard, err := openOrCreateAttachmentDirectory(tree.sha256, plan.digest[:2])
	if err != nil {
		return err
	}
	defer unix.Close(shard)
	if reused {
		if plan.objectState != "live" || plan.objectDevice == "" || plan.objectInode == "" {
			return Domainf("reused attachment lost its registered live object")
		}
		if _, err := verifyAttachmentFile(
			shard, plan.digest, plan.size,
			plan.objectDevice, plan.objectInode, plan.digest, nil); err != nil {
			return err
		}
	} else {
		stageExists, _, err := attachmentEntryEvidence(
			tree.staging, plan.stagingName, plan.size)
		if err != nil {
			return err
		}
		canonicalExists, _, err := attachmentEntryEvidence(shard, plan.digest, plan.size)
		if err != nil {
			return err
		}
		if stageExists == canonicalExists {
			return Domainf("creator attachment recovery requires exactly one of stage or canonical location")
		}
		parent, name := tree.staging, plan.stagingName
		if canonicalExists {
			parent, name = shard, plan.digest
		}
		if _, err := verifyAttachmentFile(
			parent, name, plan.size, plan.stagingDevice, plan.stagingInode,
			plan.digest, nil); err != nil {
			return fmt.Errorf("verify creator attachment digest before materialization: %w", err)
		}
	}
	return inTx(db, func(ctx context.Context, q Q) error {
		if err := requireLiveAttachmentPublisher(ctx, q, a, "object_bound"); err != nil {
			return err
		}
		var canonical attachmentFileEvidence
		if reused {
			var device, inode string
			if err := q.QueryRowContext(ctx, attachmentSQLLoadLiveObjectIdentity,
				a.Session, plan.digest, plan.size).Scan(&device, &inode); err != nil {
				return err
			}
			if device != plan.objectDevice || inode != plan.objectInode {
				return Domainf("reused attachment object changed after fresh verification")
			}
			exists, reuseCanonical, err := attachmentEntryEvidence(shard, plan.digest, plan.size)
			if err != nil {
				return err
			}
			if !exists || reuseCanonical.device != device || reuseCanonical.inode != inode {
				return Domainf("reused attachment canonical identity drifted after fresh verification")
			}
			canonical = reuseCanonical
			if err := removeOptionalAttachmentFile(
				tree.staging, plan.stagingName,
				plan.stagingDevice, plan.stagingInode, false); err != nil {
				return err
			}
		} else {
			stageExists, stageEvidence, err := attachmentEntryEvidence(
				tree.staging, plan.stagingName, plan.size)
			if err != nil {
				return err
			}
			canonicalExists, _, err := attachmentEntryEvidence(shard, plan.digest, plan.size)
			if err != nil {
				return err
			}
			if stageExists == canonicalExists {
				return Domainf("creator attachment recovery requires exactly one of stage or canonical location")
			}
			if stageExists {
				if stageEvidence.device != plan.stagingDevice ||
					stageEvidence.inode != plan.stagingInode {
					return Domainf("attachment staging identity drifted before materialization")
				}
				if err := attachmentRenameNoReplace(
					tree.staging, plan.stagingName, shard, plan.digest); err != nil {
					return fmt.Errorf("materialize attachment canonical object: %w", err)
				}
				if err := unix.Fsync(tree.staging); err != nil {
					return err
				}
				if err := unix.Fsync(shard); err != nil {
					return err
				}
			}
			canonicalExists, creatorCanonical, err := attachmentEntryEvidence(
				shard, plan.digest, plan.size)
			if err != nil {
				return err
			}
			if !canonicalExists || creatorCanonical.device != plan.stagingDevice ||
				creatorCanonical.inode != plan.stagingInode {
				return Domainf("creator attachment canonical identity drifted during materialization")
			}
			canonical = creatorCanonical
			result, err := q.ExecContext(ctx, attachmentSQLMaterializeObject,
				canonical.device, canonical.inode, a.Session, plan.digest, plan.size,
				a.PublicationID)
			if err != nil {
				return err
			}
			if rows, err := result.RowsAffected(); err != nil || rows != 1 {
				return Domainf("attachment canonical object lost its creator CAS")
			}
			result, err = q.ExecContext(ctx, attachmentSQLCollapseCreatorEvidence,
				a.Session, plan.digest, plan.size, a.PublicationID)
			if err != nil {
				return err
			}
			if rows, err := result.RowsAffected(); err != nil || rows != 1 {
				return Domainf("attachment canonical object lost its creator evidence collapse CAS")
			}
		}
		receiptBytes, err := json.Marshal(AttachmentPublicationReceipt{
			PublicationID: a.PublicationID,
			SHA256:        plan.digest,
			Size:          plan.size,
			State:         "materialized",
			Reused:        reused,
			Device:        canonical.device,
			Inode:         canonical.inode,
			FileType:      "regular",
		})
		if err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, attachmentSQLMaterializePublication,
			string(receiptBytes), a.PublicationID,
			map[bool]string{false: "creator", true: "reuse"}[reused])
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			return Domainf("attachment publication lost its materialization CAS")
		}
		return nil
	})
}

func verifyAttachmentCanonical(
	tree *attachmentTree, digest string, size int64, device, inode string,
) (attachmentFileEvidence, error) {
	if !safeAttachmentHex(digest, 64) {
		return attachmentFileEvidence{}, Domainf("invalid canonical attachment digest")
	}
	shard, err := openOrCreateAttachmentDirectory(tree.sha256, digest[:2])
	if err != nil {
		return attachmentFileEvidence{}, err
	}
	defer unix.Close(shard)
	return verifyAttachmentFile(shard, digest, size, device, inode, digest, nil)
}

func verifyAttachmentCanonicalIdentity(
	tree *attachmentTree, digest string, size int64, device, inode string,
) error {
	shard, err := openOrCreateAttachmentDirectory(tree.sha256, digest[:2])
	if err != nil {
		return err
	}
	defer unix.Close(shard)
	exists, evidence, err := attachmentEntryEvidence(shard, digest, size)
	if err != nil {
		return err
	}
	if !exists || evidence.device != device || evidence.inode != inode {
		return Domainf("attachment canonical identity drifted after fresh verification")
	}
	return nil
}

func verifyAttachmentStage(parent int, plan attachmentPublicationPlan) (attachmentFileEvidence, error) {
	fd, err := unix.Openat(parent, plan.stagingName,
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return attachmentFileEvidence{}, err
	}
	file := os.NewFile(uintptr(fd), plan.stagingName)
	evidence, err := attachmentRegularEvidence(fd, plan.size, 0o444)
	if err != nil {
		file.Close()
		return attachmentFileEvidence{}, err
	}
	if evidence.device != plan.stagingDevice || evidence.inode != plan.stagingInode {
		file.Close()
		return attachmentFileEvidence{}, Domainf("attachment staging identity drifted")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		file.Close()
		return attachmentFileEvidence{}, err
	}
	after, statErr := attachmentRegularEvidence(fd, plan.size, 0o444)
	closeErr := file.Close()
	if statErr != nil || closeErr != nil || after != evidence ||
		hex.EncodeToString(hash.Sum(nil)) != plan.digest {
		return attachmentFileEvidence{}, errors.Join(statErr, closeErr,
			Domainf("attachment staging content or identity drifted during recovery"))
	}
	return evidence, nil
}

func verifyAttachmentFile(
	parent int,
	name string,
	size int64,
	device, inode string,
	expectedDigest string,
	exactBody []byte,
) (attachmentFileEvidence, error) {
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return attachmentFileEvidence{}, err
	}
	file := os.NewFile(uintptr(fd), name)
	evidence, err := attachmentRegularEvidence(fd, size, 0o444)
	if err != nil {
		file.Close()
		return attachmentFileEvidence{}, err
	}
	if (device != "" && evidence.device != device) || (inode != "" && evidence.inode != inode) {
		file.Close()
		return attachmentFileEvidence{}, Domainf("attachment file identity drifted")
	}
	hash := sha256.New()
	var reader io.Reader = file
	if exactBody != nil {
		body, readErr := io.ReadAll(io.LimitReader(file, size+1))
		if readErr != nil || !bytesEqual(body, exactBody) {
			file.Close()
			return attachmentFileEvidence{}, Domainf("attachment journal content drifted")
		}
		reader = nil
	}
	if reader != nil {
		if !safeAttachmentHex(expectedDigest, 64) {
			file.Close()
			return attachmentFileEvidence{}, Domainf("attachment digest verification requires one exact SHA-256")
		}
		if _, err := io.Copy(hash, reader); err != nil {
			file.Close()
			return attachmentFileEvidence{}, err
		}
		if expectedDigest != hex.EncodeToString(hash.Sum(nil)) {
			file.Close()
			return attachmentFileEvidence{}, Domainf("attachment canonical digest drifted")
		}
	}
	after, err := attachmentRegularEvidence(fd, size, 0o444)
	closeErr := file.Close()
	if err != nil || closeErr != nil || after != evidence {
		return attachmentFileEvidence{}, errors.Join(err, closeErr,
			Domainf("attachment file identity changed during verification"))
	}
	return evidence, nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var different byte
	for i := range a {
		different |= a[i] ^ b[i]
	}
	return different == 0
}

func attachmentEntryEvidence(parent int, name string, size int64) (bool, attachmentFileEvidence, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(parent, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return false, attachmentFileEvidence{}, nil
		}
		return false, attachmentFileEvidence{}, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o444 ||
		int(stat.Uid) != os.Getuid() || stat.Nlink != 1 || stat.Size != size {
		return false, attachmentFileEvidence{}, Domainf("attachment file-plane entry has unsafe type, owner, mode, links, or size")
	}
	return true, attachmentFileEvidence{
		device: strconv.FormatUint(uint64(stat.Dev), 10),
		inode:  strconv.FormatUint(uint64(stat.Ino), 10),
	}, nil
}

func unlinkAttachmentEvidence(parent int, name string, want attachmentFileEvidence) error {
	var stat unix.Stat_t
	if err := unix.Fstatat(parent, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o444 ||
		int(stat.Uid) != os.Getuid() || stat.Nlink != 1 {
		return Domainf("attachment cleanup target is not an exact sealed regular file")
	}
	got := attachmentFileEvidence{
		device: strconv.FormatUint(uint64(stat.Dev), 10),
		inode:  strconv.FormatUint(uint64(stat.Ino), 10),
	}
	if got != want {
		return Domainf("attachment cleanup identity is absent or drifted")
	}
	if err := unix.Unlinkat(parent, name, 0); err != nil {
		return err
	}
	return unix.Fsync(parent)
}
