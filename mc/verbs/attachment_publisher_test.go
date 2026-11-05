package verbs

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

func attachmentPublisherFixture(t *testing.T, manifest string) (*sql.DB, string, AttachmentIngressReservation, string) {
	return attachmentPublisherFixtureBody(t, manifest, "publish")
}

func attachmentPublisherFixtureBody(
	t *testing.T, manifest, body string,
) (*sql.DB, string, AttachmentIngressReservation, string) {
	t.Helper()
	db, session, binding := attachmentIngressFixture(t)
	if _, err := AttachmentIngressEnqueue(db, nil, AttachmentIngressEnqueueArgs{
		Session: session, BindingID: binding, Surface: "discord",
		ExternalMessageID: "publisher-message", Body: body, Manifest: manifest,
	}); err != nil {
		t.Fatal(err)
	}
	reservation, err := AttachmentIngressReserve(db, nil, AttachmentIngressReserveArgs{
		Session: session, OwnerGeneration: "native-publisher-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	return db, session, reservation, home
}

func publishArgs(session string, reservation AttachmentIngressReservation, ordinal int) AttachmentPublicationPublishArgs {
	publication := reservation.Publications[ordinal-1]
	return AttachmentPublicationPublishArgs{
		Session: session, AttemptID: reservation.AttemptID,
		AttemptOwnerToken:     reservation.OwnerToken,
		PublicationID:         publication.PublicationID,
		PublicationOwnerToken: publication.OwnerToken,
		OwnerGeneration:       "native-publisher-1",
	}
}

func TestAttachmentPublicationRPCStatementClassesAreClosedAndDisjoint(t *testing.T) {
	queries := []string{
		attachmentSQLLoadPublicationPlan,
		attachmentSQLRequireLivePublisher,
		attachmentSQLLoadObject,
		attachmentSQLLoadLiveObjectIdentity,
	}
	mutations := []string{
		attachmentSQLClaimStage,
		attachmentSQLRecordStage,
		attachmentSQLRecordJournal,
		attachmentSQLClaimJournalTemp,
		attachmentSQLInsertObject,
		attachmentSQLInsertObjectPublication,
		attachmentSQLBindPublicationObject,
		attachmentSQLMaterializeObject,
		attachmentSQLCollapseCreatorEvidence,
		attachmentSQLMaterializePublication,
	}
	for _, statement := range queries {
		if !AttachmentPublicationQueryAllowed(statement) ||
			AttachmentPublicationMutationAllowed(statement) {
			t.Fatal("publisher query statement crossed into its mutation class")
		}
	}
	for _, statement := range mutations {
		if !AttachmentPublicationMutationAllowed(statement) ||
			AttachmentPublicationQueryAllowed(statement) {
			t.Fatal("publisher mutation statement crossed into its query class")
		}
	}
	for _, statement := range []string{
		"SELECT 1", "BEGIN IMMEDIATE", attachmentSQLClaimStage + " ",
	} {
		if AttachmentPublicationQueryAllowed(statement) ||
			AttachmentPublicationMutationAllowed(statement) {
			t.Fatalf("expanded publisher SQL was accepted: %q", statement)
		}
	}
}

func TestAttachmentTerminalRPCStatementClassesAreClosedAndSeparateFromPublisher(t *testing.T) {
	queries := []string{
		attachmentSQLTerminalScan,
		attachmentSQLTerminalLoadBatch,
		attachmentSQLTerminalCleanupPublications,
		attachmentSQLTerminalReady,
		attachmentSQLCleanupPublication,
		attachmentSQLCleanupObject,
	}
	mutations := []string{
		attachmentSQLTerminalFencePublications,
		attachmentSQLTerminalFenceBatch,
		attachmentSQLTerminalFenceAttempt,
		attachmentSQLTerminalDeletePublications,
		attachmentSQLTerminalDeleteAttempt,
		attachmentSQLTerminalDeleteBatch,
		attachmentSQLTerminalDeleteDigests,
		attachmentSQLTerminalCompactIngress,
		attachmentSQLCleanupMarkAborted,
		attachmentSQLCleanupDeleteObject,
	}
	for _, statement := range queries {
		if !AttachmentTerminalQueryAllowed(statement) ||
			AttachmentTerminalMutationAllowed(statement) ||
			AttachmentPublicationQueryAllowed(statement) ||
			AttachmentPublicationMutationAllowed(statement) {
			t.Fatal("terminal query statement crossed an RPC profile or mutation class")
		}
	}
	for _, statement := range mutations {
		if !AttachmentTerminalMutationAllowed(statement) ||
			AttachmentTerminalQueryAllowed(statement) ||
			AttachmentPublicationQueryAllowed(statement) ||
			AttachmentPublicationMutationAllowed(statement) {
			t.Fatal("terminal mutation statement crossed an RPC profile or query class")
		}
	}
	for _, statement := range []string{
		"SELECT 1", "BEGIN IMMEDIATE", attachmentSQLTerminalScan + " ",
	} {
		if AttachmentTerminalQueryAllowed(statement) ||
			AttachmentTerminalMutationAllowed(statement) {
			t.Fatalf("expanded terminal SQL was accepted: %q", statement)
		}
	}
}

func TestPrepareAttachmentTreeCreatesAndRevalidatesStableSessionRoots(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	first, err := PrepareAttachmentTree(nil, home, "h-tree")
	if err != nil {
		t.Fatal(err)
	}
	if first.Session != "h-tree" ||
		first.InboundPublished != filepath.Join(home, "attachments", "h-tree", "in", "published") ||
		first.Outbound != filepath.Join(home, "attachments", "h-tree", "out") {
		t.Fatalf("tree receipt = %#v", first)
	}
	for _, relative := range []string{
		"attachments",
		"attachments/h-tree",
		"attachments/h-tree/in",
		"attachments/h-tree/in/staging",
		"attachments/h-tree/in/journal",
		"attachments/h-tree/in/published",
		"attachments/h-tree/in/published/sha256",
		"attachments/h-tree/out",
		"attachments/h-tree/out/staging",
		"attachments/h-tree/out/journal",
		"attachments/h-tree/out/published",
		"attachments/h-tree/out/published/sha256",
	} {
		info, err := os.Lstat(filepath.Join(home, relative))
		if err != nil {
			t.Fatalf("%s missing: %v", relative, err)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s = mode %v, want operator-owned 0700 directory",
				relative, info.Mode())
		}
	}
	sentinel := filepath.Join(first.Outbound, "published", "retained")
	if err := os.WriteFile(sentinel, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	replay, err := PrepareAttachmentTree(nil, home, "h-tree")
	if err != nil || replay != first {
		t.Fatalf("tree replay = %#v err %v, first %#v", replay, err, first)
	}
	if body, err := os.ReadFile(sentinel); err != nil || string(body) != "old" {
		t.Fatalf("tree replay changed retained bytes = %q err %v", body, err)
	}
}

func TestPrepareAttachmentTreeRefusesUnsafeOrWidenedRoots(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareAttachmentTree(
		&RunIdentity{Tier: "homie", RunID: "h-tree"}, home, "h-tree",
	); err == nil || !strings.Contains(err.Error(), "host scope only") {
		t.Fatalf("run.json caller preparation error = %v", err)
	}
	if _, err := PrepareAttachmentTree(nil, home, "../escape"); err == nil {
		t.Fatal("unsafe session id prepared an attachment tree")
	}
	if err := os.MkdirAll(filepath.Join(home, "attachments", "h-link"), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home, "attachments", "h-link", "in")); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareAttachmentTree(nil, home, "h-link"); err == nil {
		t.Fatal("symlinked attachment direction was adopted")
	}
	if _, err := PrepareAttachmentTree(nil, home, "h-wide"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(home, "attachments", "h-wide", "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareAttachmentTree(nil, home, "h-wide"); err == nil {
		t.Fatal("widened attachment direction was repaired or adopted")
	}
}

func TestAttachmentIngressConsumeProductionBoundaryIsHostOnly(t *testing.T) {
	if _, err := AttachmentIngressConsume(
		nil,
		&RunIdentity{Tier: "homie", RunID: "h-scope"},
		AttachmentIngressConsumeArgs{
			Session: "h-scope", From: "discord:channel", Body: "body",
			AttemptID: "0011223344556677",
		},
	); err == nil || !strings.Contains(err.Error(), "host scope only") {
		t.Fatalf("Homie-scoped attachment consume error = %v", err)
	}
}

func bindAttachmentPublicationForTest(
	t *testing.T,
	db *sql.DB,
	tree *attachmentTree,
	args AttachmentPublicationPublishArgs,
	body []byte,
) (attachmentPublicationPlan, string) {
	t.Helper()
	plan, err := loadAttachmentPublicationPlan(db, args)
	if err != nil {
		t.Fatal(err)
	}
	stageFile, stage, err := claimAttachmentStage(db, tree, args, plan)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := writeClaimedAttachmentStage(
		stageFile, tree.staging, plan.stagingName, plan.size,
		bytes.NewReader(body), stage)
	if err != nil {
		t.Fatal(err)
	}
	if err := recordAttachmentStage(db, args, digest, stage); err != nil {
		t.Fatal(err)
	}
	plan, err = loadAttachmentPublicationPlan(db, args)
	if err != nil {
		t.Fatal(err)
	}
	journalBody, err := json.Marshal(attachmentPublicationJournal{
		Version: 1, PublicationID: args.PublicationID, AttemptID: args.AttemptID,
		BatchID: plan.batchID, Session: args.Session, Direction: "in",
		Ordinal: plan.ordinal, Size: plan.size, SHA256: plan.digest,
		StagingName: plan.stagingName, StagingDevice: plan.stagingDevice,
		StagingInode:    plan.stagingInode,
		CanonicalName:   "sha256/" + plan.digest[:2] + "/" + plan.digest,
		OwnerGeneration: args.OwnerGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}
	journal, err := publishAttachmentJournal(db, tree, args, plan, journalBody)
	if err != nil {
		t.Fatal(err)
	}
	plan.journalDevice, plan.journalInode = journal.device, journal.inode
	if _, err := bindAttachmentObject(db, tree, args, plan); err != nil {
		t.Fatal(err)
	}
	plan, err = loadAttachmentPublicationPlan(db, args)
	if err != nil {
		t.Fatal(err)
	}
	return plan, digest
}

func TestAttachmentPublicationPublishDurablyMaterializesExactBytes(t *testing.T) {
	const manifest = `[{"ordinal":1,"size":7,"media_type":"text/plain","display_name":"seven.txt"}]`
	db, session, reservation, home := attachmentPublisherFixture(t, manifest)
	got, err := AttachmentPublicationPublish(
		db, nil, home, publishArgs(session, reservation, 1), bytes.NewReader([]byte("1234567")))
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "materialized" || got.Size != 7 || len(got.SHA256) != 64 || got.Reused {
		t.Fatalf("publish receipt = %#v", got)
	}

	base := filepath.Join(home, "attachments", session, "in")
	canonical := filepath.Join(base, "published", "sha256", got.SHA256[:2], got.SHA256)
	body, err := os.ReadFile(canonical)
	if err != nil || string(body) != "1234567" {
		t.Fatalf("canonical body = %q err %v", body, err)
	}
	info, err := os.Stat(canonical)
	if err != nil || info.Mode().Perm() != 0o444 {
		t.Fatalf("canonical mode = %v err %v", info.Mode(), err)
	}
	publication := reservation.Publications[0]
	if _, err := os.Stat(filepath.Join(base, "staging", publication.PublicationID+".part")); !os.IsNotExist(err) {
		t.Fatalf("stage remains after materialization: %v", err)
	}
	journal := filepath.Join(base, "journal", publication.PublicationID+".json")
	if info, err := os.Stat(journal); err != nil || info.Mode().Perm() != 0o444 {
		t.Fatalf("journal mode = %v err %v", info.Mode(), err)
	}

	var publicationState, objectState, role string
	var creatorPublication sql.NullString
	var storedReceipt string
	var edges int
	if err := db.QueryRow(`
		SELECT state,object_role,materialization_receipt
		FROM attachment_publications WHERE id=?`,
		publication.PublicationID).Scan(&publicationState, &role, &storedReceipt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT state,creator_publication_id FROM attachment_objects
		WHERE session_id=? AND direction='in' AND sha256=? AND size=7`,
		session, got.SHA256).Scan(&objectState, &creatorPublication); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM attachment_object_publications
		WHERE publication_id=?`, publication.PublicationID).Scan(&edges); err != nil {
		t.Fatal(err)
	}
	if publicationState != "materialized" || objectState != "live" ||
		creatorPublication.Valid || role != "creator" || edges != 1 {
		t.Fatalf("spine state = publication %q object %q creator %v role %q edges %d",
			publicationState, objectState, creatorPublication, role, edges)
	}
	receiptBytes, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if storedReceipt != string(receiptBytes) {
		t.Fatalf("stored receipt = %s, returned receipt = %s", storedReceipt, receiptBytes)
	}

	replay, err := AttachmentPublicationPublish(
		db, nil, home, publishArgs(session, reservation, 1), bytes.NewReader([]byte("ignored")))
	if err != nil || replay != got {
		t.Fatalf("publish replay = %#v err %v", replay, err)
	}
}

func TestAttachmentPublicationReplayRejectsReplacementCanonicalInode(t *testing.T) {
	const manifest = `[{"ordinal":1,"size":7,"media_type":"text/plain","display_name":"seven.txt"}]`
	db, session, reservation, home := attachmentPublisherFixture(t, manifest)
	got, err := AttachmentPublicationPublish(
		db, nil, home, publishArgs(session, reservation, 1), bytes.NewReader([]byte("1234567")))
	if err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(
		home, "attachments", session, "in", "published", "sha256",
		got.SHA256[:2], got.SHA256)
	if err := os.Rename(canonical, canonical+".retained"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonical, []byte("1234567"), 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := AttachmentPublicationPublish(
		db, nil, home, publishArgs(session, reservation, 1),
		bytes.NewReader([]byte("ignored"))); err == nil ||
		!strings.Contains(err.Error(), "identity drifted") {
		t.Fatalf("replacement canonical replay error = %v", err)
	}
}

func TestAttachmentPublicationPublishRejectsWrongSizeWithoutResidue(t *testing.T) {
	const manifest = `[{"ordinal":1,"size":7,"media_type":"text/plain","display_name":"seven.txt"}]`
	db, session, reservation, home := attachmentPublisherFixture(t, manifest)
	_, err := AttachmentPublicationPublish(
		db, nil, home, publishArgs(session, reservation, 1), bytes.NewReader([]byte("short")))
	if err == nil || !strings.Contains(err.Error(), "declared size") {
		t.Fatalf("short publication error = %v", err)
	}
	publication := reservation.Publications[0]
	stage := filepath.Join(home, "attachments", session, "in", "staging", publication.PublicationID+".part")
	if _, statErr := os.Lstat(stage); !os.IsNotExist(statErr) {
		t.Fatalf("failed publication left stage: %v", statErr)
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM attachment_publications WHERE id=?`,
		publication.PublicationID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "reserved" {
		t.Fatalf("failed publication state = %q, want reserved", state)
	}
}

func TestAttachmentPublicationPublishRefusesPrecreatedStageSymlink(t *testing.T) {
	const manifest = `[{"ordinal":1,"size":7,"media_type":"text/plain","display_name":"seven.txt"}]`
	db, session, reservation, home := attachmentPublisherFixture(t, manifest)
	tree, err := openAttachmentTree(home, session)
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(home, "attachments", session, "in", "staging",
		reservation.Publications[0].PublicationID+".part")
	if err := os.Symlink("/etc/passwd", stage); err != nil {
		t.Fatal(err)
	}
	if err := tree.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := AttachmentPublicationPublish(
		db, nil, home, publishArgs(session, reservation, 1),
		bytes.NewReader([]byte("1234567"))); err == nil ||
		!strings.Contains(err.Error(), "create exact attachment stage") {
		t.Fatalf("precreated stage error = %v", err)
	}
	info, err := os.Lstat(stage)
	if err != nil {
		t.Fatalf("publisher removed hostile stage: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("hostile stage mode = %v, want symlink", info.Mode())
	}
}

func TestAttachmentPublicationPublishReusesOnlyFreshCanonicalObject(t *testing.T) {
	const manifest = `[
		{"ordinal":1,"size":7,"media_type":"text/plain","display_name":"first.txt"},
		{"ordinal":2,"size":7,"media_type":"text/plain","display_name":"second.txt"}
	]`
	db, session, reservation, home := attachmentPublisherFixture(t, manifest)
	first, err := AttachmentPublicationPublish(
		db, nil, home, publishArgs(session, reservation, 1), bytes.NewReader([]byte("same777")))
	if err != nil {
		t.Fatal(err)
	}
	second, err := AttachmentPublicationPublish(
		db, nil, home, publishArgs(session, reservation, 2), bytes.NewReader([]byte("same777")))
	if err != nil {
		t.Fatal(err)
	}
	if !second.Reused || second.SHA256 != first.SHA256 {
		t.Fatalf("reuse receipt = %#v, first = %#v", second, first)
	}
	var objects, edges int
	if err := db.QueryRow(`SELECT COUNT(*) FROM attachment_objects
		WHERE session_id=? AND direction='in' AND sha256=? AND size=7`,
		session, first.SHA256).Scan(&objects); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM attachment_object_publications
		WHERE session_id=? AND direction='in' AND sha256=? AND size=7`,
		session, first.SHA256).Scan(&edges); err != nil {
		t.Fatal(err)
	}
	if objects != 1 || edges != 2 {
		t.Fatalf("reuse liveness = %d objects, %d edges", objects, edges)
	}
}

func TestAttachmentPublicationReuseResumesAfterPrecommitStageUnlink(t *testing.T) {
	const manifest = `[
		{"ordinal":1,"size":7,"media_type":"text/plain","display_name":"first.txt"},
		{"ordinal":2,"size":7,"media_type":"text/plain","display_name":"second.txt"}
	]`
	db, session, reservation, home := attachmentPublisherFixture(t, manifest)
	first, err := AttachmentPublicationPublish(
		db, nil, home, publishArgs(session, reservation, 1), bytes.NewReader([]byte("same777")))
	if err != nil {
		t.Fatal(err)
	}
	args := publishArgs(session, reservation, 2)
	tree, err := openAttachmentTree(home, session)
	if err != nil {
		t.Fatal(err)
	}
	plan, digest := bindAttachmentPublicationForTest(t, db, tree, args, []byte("same777"))
	if plan.role != "reuse" || digest != first.SHA256 {
		t.Fatalf("object-bound reuse plan = %#v digest %q", plan, digest)
	}
	if err := removeOptionalAttachmentFile(
		tree.staging, plan.stagingName,
		plan.stagingDevice, plan.stagingInode, false); err != nil {
		t.Fatal(err)
	}
	if err := tree.close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := AttachmentPublicationPublish(
		db, nil, home, args, bytes.NewReader([]byte("source-is-not-reopened")))
	if err != nil {
		t.Fatal(err)
	}
	if !resumed.Reused || resumed.State != "materialized" || resumed.SHA256 != first.SHA256 {
		t.Fatalf("resumed reuse receipt = %#v", resumed)
	}
}

func TestAttachmentPublicationCreatorRecoveryRehashesExactLocation(t *testing.T) {
	for _, location := range []string{"stage", "canonical"} {
		t.Run(location, func(t *testing.T) {
			const manifest = `[{"ordinal":1,"size":7,"media_type":"text/plain","display_name":"seven.txt"}]`
			db, session, reservation, home := attachmentPublisherFixture(t, manifest)
			args := publishArgs(session, reservation, 1)
			tree, err := openAttachmentTree(home, session)
			if err != nil {
				t.Fatal(err)
			}
			plan, _ := bindAttachmentPublicationForTest(t, db, tree, args, []byte("1234567"))
			tamperPath := filepath.Join(
				home, "attachments", session, "in", "staging", plan.stagingName)
			if location == "canonical" {
				shard, err := openOrCreateAttachmentDirectory(tree.sha256, plan.digest[:2])
				if err != nil {
					t.Fatal(err)
				}
				if err := attachmentRenameNoReplace(
					tree.staging, plan.stagingName, shard, plan.digest); err != nil {
					t.Fatal(err)
				}
				if err := unix.Fsync(tree.staging); err != nil {
					t.Fatal(err)
				}
				if err := unix.Fsync(shard); err != nil {
					t.Fatal(err)
				}
				tamperPath = filepath.Join(
					home, "attachments", session, "in", "published", "sha256",
					plan.digest[:2], plan.digest)
				defer unix.Close(shard)
			}
			if err := os.Chmod(tamperPath, 0o600); err != nil {
				t.Fatal(err)
			}
			file, err := os.OpenFile(tamperPath, os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.WriteAt([]byte("7654321"), 0); err != nil {
				t.Fatal(err)
			}
			if err := file.Sync(); err != nil {
				t.Fatal(err)
			}
			if err := file.Chmod(0o444); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			if err := tree.close(); err != nil {
				t.Fatal(err)
			}
			if _, err := AttachmentPublicationPublish(
				db, nil, home, args,
				bytes.NewReader([]byte("source-is-not-reopened"))); err == nil ||
				!strings.Contains(err.Error(), "digest drifted") {
				t.Fatalf("%s creator recovery error = %v", location, err)
			}
		})
	}
}

func TestHomieSendConsumesMaterializedIngressAtomicallyAndReplays(t *testing.T) {
	const manifest = `[{"ordinal":1,"size":7,"media_type":"text/plain","display_name":"seven.txt"}]`
	db, session, reservation, home := attachmentPublisherFixture(t, manifest)
	published, err := AttachmentPublicationPublish(
		db, nil, home, publishArgs(session, reservation, 1), bytes.NewReader([]byte("1234567")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := HomieBind(db, nil, session, "dashboard:mirror"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	forgedMessage, err := tx.Exec(`
		INSERT INTO conversation_messages
		    (session_id,seq,direction,surface,channel_ref,body,attachments)
		VALUES (?,1,'inbound','discord','channel-1','publish',NULL)`, session)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	forgedMessageID, err := forgedMessage.LastInsertId()
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec(`
		INSERT INTO conversation_attachment_refs
		    (message_id,ordinal,session_id,direction,sha256,size,media_type,display_name)
		VALUES (?,1,?,'in',?,7,'text/plain','seven.txt')`,
		forgedMessageID, session, published.SHA256); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	var manifestSHA, capabilitySHA, ownerGeneration string
	if err := tx.QueryRow(`
		SELECT i.manifest_sha256,a.owner_token_sha256,a.owner_generation
		FROM attachment_attempts a
		JOIN attachment_ingresses i ON i.id=a.ingress_id
		WHERE a.id=?`, reservation.AttemptID).
		Scan(&manifestSHA, &capabilitySHA, &ownerGeneration); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	forgedReceipt, err := json.Marshal(map[string]any{
		"attachment_ingress_id": reservation.IngressID,
		"attempt":               int64(1),
		"attempt_id":            reservation.AttemptID,
		"batch_id":              reservation.BatchID,
		"body_sha256":           strings.Repeat("f", 64),
		"channel_ref":           "channel-1",
		"echoes":                int64(1),
		"external_message_id":   "publisher-message",
		"manifest_sha256":       manifestSHA,
		"message_id":            forgedMessageID,
		"seq":                   int64(1),
		"session_id":            session,
		"state":                 "consumed",
		"surface":               "discord",
	})
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec(`
		INSERT INTO attachment_batch_receipts
		    (batch_id,attempt_id,ingress_id,session_id,generation,owner_generation,
		     outcome,reason,capability_sha256,receipt,conversation_message_id)
		VALUES (?,?,?,?,1,?,'consumed','consumed',?,?,?)`,
		reservation.BatchID, reservation.AttemptID, reservation.IngressID, session,
		ownerGeneration, capabilitySHA, string(forgedReceipt), forgedMessageID); err == nil {
		tx.Rollback()
		t.Fatal("forged attachment body digest entered the permanent consume receipt")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	args := attachmentIngressConsumeArgs{
		Session: session, From: "discord:channel-1", Body: "publish",
		AttemptID: reservation.AttemptID,
	}
	for _, mismatch := range []attachmentIngressConsumeArgs{
		{
			Session: session, From: "discord:channel-1", Body: "different",
			AttemptID: reservation.AttemptID,
		},
		{
			Session: session, From: "discord:different", Body: "publish",
			AttemptID: reservation.AttemptID,
		},
	} {
		if _, err := attachmentIngressConsume(db, mismatch); err == nil ||
			!strings.Contains(err.Error(), "sealed ingress") {
			t.Fatalf("mismatched consume error = %v", err)
		}
	}
	for table, want := range map[string]int{
		"conversation_messages":        0,
		"conversation_attachment_refs": 0,
		"attachment_batch_receipts":    0,
	} {
		var got int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s rows after mismatched consumes = %d, want %d", table, got, want)
		}
	}
	firstAny, err := attachmentIngressConsume(db, args)
	if err != nil {
		t.Fatal(err)
	}
	first := firstAny.(map[string]any)
	messageID := first["message_id"].(int64)
	if first["attachment_ingress_id"] == nil || first["attempt_id"] != reservation.AttemptID {
		t.Fatalf("consume receipt = %#v", first)
	}
	var ingressState, attemptState, batchState, publicationState string
	var consumedMessage int64
	var consumeReceipt string
	if err := db.QueryRow(`
		SELECT i.state,a.state,b.state,p.state,r.conversation_message_id,r.receipt
		FROM attachment_ingresses i
		JOIN attachment_attempts a ON a.ingress_id=i.id
		JOIN attachment_batches b ON b.id=a.batch_id
		JOIN attachment_publications p ON p.batch_id=b.id
		JOIN attachment_batch_receipts r ON r.attempt_id=a.id
		WHERE a.id=?`,
		reservation.AttemptID).Scan(
		&ingressState, &attemptState, &batchState, &publicationState,
		&consumedMessage, &consumeReceipt); err != nil {
		t.Fatal(err)
	}
	if ingressState != "consumed" || attemptState != "consumed" ||
		batchState != "consumed" || publicationState != "consumed" ||
		consumedMessage != messageID {
		t.Fatalf("consumed states = ingress %q attempt %q batch %q publication %q message %d",
			ingressState, attemptState, batchState, publicationState, consumedMessage)
	}
	var refs, edges, messages, receipts, outboxRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM conversation_attachment_refs
		WHERE message_id=? AND session_id=? AND direction='in' AND sha256=? AND size=7`,
		messageID, session, published.SHA256).Scan(&refs); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM attachment_object_publications
		WHERE publication_id=?`, reservation.Publications[0].PublicationID).Scan(&edges); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM conversation_messages`).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM attachment_batch_receipts
		WHERE attempt_id=? AND outcome='consumed'`, reservation.AttemptID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox
		WHERE kind='homie_echo' AND session_id=? AND surface='dashboard'
		  AND json_array_length(json_extract(payload,'$.attachments'))=1`,
		session).Scan(&outboxRows); err != nil {
		t.Fatal(err)
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if refs != 1 || edges != 0 || messages != 1 || receipts != 1 || outboxRows != 1 ||
		consumeReceipt != string(firstJSON) {
		t.Fatalf("consume ownership = refs %d edges %d messages %d receipts %d outbox %d stored %s returned %s",
			refs, edges, messages, receipts, outboxRows, consumeReceipt, firstJSON)
	}
	if _, err := db.Exec(`UPDATE homie_sessions SET status='ended' WHERE id=?`, session); err != nil {
		t.Fatal(err)
	}
	replayAny, err := attachmentIngressConsume(db, args)
	if err != nil {
		t.Fatal(err)
	}
	replayJSON, err := json.Marshal(replayAny)
	if err != nil {
		t.Fatal(err)
	}
	if string(replayJSON) != string(firstJSON) {
		t.Fatalf("consume replay = %s, first = %s", replayJSON, firstJSON)
	}
	var bindingID int64
	var storedManifest string
	if err := db.QueryRow(`SELECT binding_id,manifest FROM attachment_ingresses
		WHERE id=?`, first["attachment_ingress_id"]).Scan(&bindingID, &storedManifest); err != nil {
		t.Fatal(err)
	}
	enqueueReplay, err := AttachmentIngressEnqueue(db, nil, AttachmentIngressEnqueueArgs{
		Session: session, BindingID: bindingID, Surface: "discord",
		ExternalMessageID: "publisher-message", Body: "publish", Manifest: storedManifest,
	})
	if err != nil {
		t.Fatal(err)
	}
	enqueueResultJSON, err := json.Marshal(enqueueReplay.Result)
	if err != nil {
		t.Fatal(err)
	}
	if enqueueReplay.State != "consumed" ||
		string(enqueueResultJSON) != string(firstJSON) {
		t.Fatalf("consumed enqueue replay = %#v result %s, first %s",
			enqueueReplay, enqueueResultJSON, firstJSON)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM conversation_messages`).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if messages != 1 {
		t.Fatalf("consume replay appended %d messages", messages)
	}
}

func TestHomieSendIngressConsumeFailureRollsBackEveryOwner(t *testing.T) {
	const manifest = `[{"ordinal":1,"size":7,"media_type":"text/plain","display_name":"seven.txt"}]`
	db, session, reservation, home := attachmentPublisherFixture(t, manifest)
	if _, err := AttachmentPublicationPublish(
		db, nil, home, publishArgs(session, reservation, 1),
		bytes.NewReader([]byte("1234567"))); err != nil {
		t.Fatal(err)
	}
	if _, err := HomieBind(db, nil, session, "dashboard:mirror"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER test_fail_ingress_consume
		BEFORE UPDATE OF state ON attachment_attempts
		WHEN NEW.state='consumed'
		BEGIN SELECT RAISE(ABORT, 'injected consume failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := attachmentIngressConsume(db, attachmentIngressConsumeArgs{
		Session: session, From: "discord:channel-1", Body: "publish",
		AttemptID: reservation.AttemptID,
	})
	if err == nil || !strings.Contains(err.Error(), "injected consume failure") {
		t.Fatalf("consume failure = %v", err)
	}
	for table, want := range map[string]int{
		"conversation_messages":          0,
		"conversation_attachment_refs":   0,
		"attachment_batch_receipts":      0,
		"attachment_object_publications": 1,
		"outbox":                         0,
	} {
		var got int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s rows after rollback = %d, want %d", table, got, want)
		}
	}
	var ingressState, attemptState, batchState, publicationState string
	if err := db.QueryRow(`
		SELECT i.state,a.state,b.state,p.state
		FROM attachment_ingresses i
		JOIN attachment_attempts a ON a.ingress_id=i.id
		JOIN attachment_batches b ON b.id=a.batch_id
		JOIN attachment_publications p ON p.batch_id=b.id
		WHERE a.id=?`, reservation.AttemptID).
		Scan(&ingressState, &attemptState, &batchState, &publicationState); err != nil {
		t.Fatal(err)
	}
	if ingressState != "publishing" || attemptState != "active" ||
		batchState != "publishing" || publicationState != "materialized" {
		t.Fatalf("rollback states = %q %q %q %q",
			ingressState, attemptState, batchState, publicationState)
	}
}

func TestHomieSendConsumesAttachmentOnlyIngress(t *testing.T) {
	const manifest = `[{"ordinal":1,"size":7,"media_type":"text/plain","display_name":"seven.txt"}]`
	db, session, reservation, home := attachmentPublisherFixtureBody(t, manifest, "")
	if _, err := AttachmentPublicationPublish(
		db, nil, home, publishArgs(session, reservation, 1),
		bytes.NewReader([]byte("1234567"))); err != nil {
		t.Fatal(err)
	}
	got, err := attachmentIngressConsume(db, attachmentIngressConsumeArgs{
		Session: session, From: "discord:channel-1",
		AttemptID: reservation.AttemptID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.(map[string]any)["state"] != "consumed" {
		t.Fatalf("attachment-only consume = %#v", got)
	}
	var body string
	if err := db.QueryRow(`SELECT body FROM conversation_messages`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		t.Fatalf("attachment-only body = %q", body)
	}
}

func TestHomieSendConcurrentIngressConsumeReturnsOneReceipt(t *testing.T) {
	const manifest = `[{"ordinal":1,"size":7,"media_type":"text/plain","display_name":"seven.txt"}]`
	db, session, reservation, home := attachmentPublisherFixture(t, manifest)
	if _, err := AttachmentPublicationPublish(
		db, nil, home, publishArgs(session, reservation, 1),
		bytes.NewReader([]byte("1234567"))); err != nil {
		t.Fatal(err)
	}
	args := attachmentIngressConsumeArgs{
		Session: session, From: "discord:channel-1", Body: "publish",
		AttemptID: reservation.AttemptID,
	}
	results := make([]any, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for index := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[index], errs[index] = attachmentIngressConsume(db, args)
		}()
	}
	wg.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("concurrent consume %d: %v", index, err)
		}
	}
	first, err := json.Marshal(results[0])
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(results[1])
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("concurrent receipts differ: %s != %s", first, second)
	}
	for table, want := range map[string]int{
		"conversation_messages":        1,
		"conversation_attachment_refs": 1,
		"attachment_batch_receipts":    1,
	} {
		var got int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s rows after concurrent consume = %d, want %d", table, got, want)
		}
	}
}

func TestAttachmentTerminalReconcileCompactsConsumedBatchAndPreservesReplay(t *testing.T) {
	const manifest = `[{"ordinal":1,"size":7,"media_type":"text/plain","display_name":"seven.txt"}]`
	db, session, reservation, home := attachmentPublisherFixture(t, manifest)
	published, err := AttachmentPublicationPublish(
		db, nil, home, publishArgs(session, reservation, 1),
		bytes.NewReader([]byte("1234567")))
	if err != nil {
		t.Fatal(err)
	}
	args := attachmentIngressConsumeArgs{
		Session: session, From: "discord:channel-1", Body: "publish",
		AttemptID: reservation.AttemptID,
	}
	first, err := attachmentIngressConsume(db, args)
	if err != nil {
		t.Fatal(err)
	}
	for label, query := range map[string]string{
		"publication": `DELETE FROM attachment_publications WHERE batch_id=?`,
		"attempt":     `DELETE FROM attachment_attempts WHERE batch_id=?`,
		"batch":       `DELETE FROM attachment_batches WHERE id=?`,
	} {
		if _, err := db.Exec(query, reservation.BatchID); err == nil {
			t.Fatalf("early %s deletion committed without terminal cleanup evidence", label)
		}
	}
	page, err := attachmentTerminalsReconcile(db, nil, home, 0, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Batches) != 1 || page.Batches[0].BatchID != reservation.BatchID ||
		page.Batches[0].Outcome != "consumed" || page.Next != reservation.BatchID {
		t.Fatalf("reconcile page = %#v", page)
	}
	for table, want := range map[string]int{
		"attachment_publications":        0,
		"attachment_attempts":            0,
		"attachment_batches":             0,
		"attachment_batch_receipts":      1,
		"attachment_ingress_digests":     0,
		"attachment_objects":             1,
		"conversation_attachment_refs":   1,
		"attachment_object_publications": 0,
	} {
		var got int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s rows after compaction = %d, want %d", table, got, want)
		}
	}
	var body, compactedAt, storedManifest string
	if err := db.QueryRow(`SELECT body,manifest,compacted_at
		FROM attachment_ingresses WHERE id=?`, reservation.IngressID).
		Scan(&body, &storedManifest, &compactedAt); err != nil {
		t.Fatal(err)
	}
	if body != "" || storedManifest != "[]" || compactedAt == "" {
		t.Fatalf("compacted ingress = body %q manifest %q at %q",
			body, storedManifest, compactedAt)
	}
	base := filepath.Join(home, "attachments", session, "in")
	publication := reservation.Publications[0]
	for _, path := range []string{
		filepath.Join(base, "staging", publication.PublicationID+".part"),
		filepath.Join(base, "journal", publication.PublicationID+".json.tmp"),
		filepath.Join(base, "journal", publication.PublicationID+".json"),
	} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("terminal residue remains at %s: %v", path, err)
		}
	}
	canonical := filepath.Join(
		base, "published", "sha256", published.SHA256[:2], published.SHA256)
	if got, err := os.ReadFile(canonical); err != nil || string(got) != "1234567" {
		t.Fatalf("conversation-owned canonical = %q err %v", got, err)
	}
	replay, err := attachmentIngressConsume(db, args)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(first)
	replayJSON, _ := json.Marshal(replay)
	if string(firstJSON) != string(replayJSON) {
		t.Fatalf("compacted replay = %s, first %s", replayJSON, firstJSON)
	}
	empty, err := attachmentTerminalsReconcile(db, nil, home, 0, 128)
	if err != nil || len(empty.Batches) != 0 {
		t.Fatalf("stable reconcile = %#v err %v", empty, err)
	}
	for _, limit := range []int{0, 129} {
		if _, err := attachmentTerminalsReconcile(db, nil, home, 0, limit); err == nil {
			t.Fatalf("reconcile accepted limit %d", limit)
		}
	}
}

func TestAttachmentTerminalReconcileCompactsAbortedBatchAndPreservesRetry(t *testing.T) {
	const manifest = `[{"ordinal":1,"size":7,"media_type":"text/plain","display_name":"seven.txt"}]`
	db, session, reservation, home := attachmentPublisherFixture(t, manifest)
	published, err := AttachmentPublicationPublish(
		db, nil, home, publishArgs(session, reservation, 1),
		bytes.NewReader([]byte("1234567")))
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := AttachmentAttemptAbort(db, nil, AttachmentAttemptAbortArgs{
		Session: session, AttemptID: reservation.AttemptID,
		OwnerToken: reservation.OwnerToken, OwnerGeneration: "native-publisher-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if terminal.Outcome != "requeued" {
		t.Fatalf("terminal = %#v", terminal)
	}
	page, err := attachmentTerminalsReconcile(db, nil, home, 0, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Batches) != 1 || page.Batches[0].Outcome != "requeued" {
		t.Fatalf("aborted reconcile page = %#v", page)
	}
	for table, want := range map[string]int{
		"attachment_publications":    0,
		"attachment_attempts":        0,
		"attachment_batches":         0,
		"attachment_objects":         0,
		"attachment_batch_receipts":  1,
		"attachment_ingress_digests": 1,
	} {
		var got int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s rows after aborted compaction = %d, want %d", table, got, want)
		}
	}
	if _, err := db.Exec(`DELETE FROM attachment_ingress_digests
		WHERE ingress_id=?`, reservation.IngressID); err == nil {
		t.Fatal("retry digest deletion committed without durable conversation ownership")
	}
	canonical := filepath.Join(
		home, "attachments", session, "in", "published", "sha256",
		published.SHA256[:2], published.SHA256)
	if _, err := os.Lstat(canonical); !os.IsNotExist(err) {
		t.Fatalf("aborted canonical remains: %v", err)
	}
	retry, err := AttachmentIngressReserve(db, nil, AttachmentIngressReserveArgs{
		Session: session, OwnerGeneration: "native-publisher-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if retry.IngressID != reservation.IngressID || retry.AttemptID == reservation.AttemptID {
		t.Fatalf("retry reservation = %#v, first %#v", retry, reservation)
	}
}

func TestAttachmentTerminalReconcileResumesAfterPhysicalCleanupCut(t *testing.T) {
	const manifest = `[{"ordinal":1,"size":7,"media_type":"text/plain","display_name":"seven.txt"}]`
	db, session, reservation, home := attachmentPublisherFixture(t, manifest)
	if _, err := AttachmentPublicationPublish(
		db, nil, home, publishArgs(session, reservation, 1),
		bytes.NewReader([]byte("1234567"))); err != nil {
		t.Fatal(err)
	}
	if _, err := attachmentIngressConsume(db, attachmentIngressConsumeArgs{
		Session: session, From: "discord:channel-1", Body: "publish",
		AttemptID: reservation.AttemptID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER test_fail_terminal_compaction
		BEFORE DELETE ON attachment_attempts
		BEGIN SELECT RAISE(ABORT, 'injected terminal compaction cut'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := attachmentTerminalsReconcile(db, nil, home, 0, 128); err == nil ||
		!strings.Contains(err.Error(), "injected terminal compaction cut") {
		t.Fatalf("terminal compaction cut error = %v", err)
	}
	var publicationState, batchState, attemptState string
	if err := db.QueryRow(`
		SELECT p.state,b.state,a.state
		FROM attachment_publications p
		JOIN attachment_batches b ON b.id=p.batch_id
		JOIN attachment_attempts a ON a.batch_id=b.id
		WHERE b.id=?`, reservation.BatchID).
		Scan(&publicationState, &batchState, &attemptState); err != nil {
		t.Fatal(err)
	}
	if publicationState != "aborted" || batchState != "cleanup_pending" ||
		attemptState != "cleanup_pending" {
		t.Fatalf("cut states = publication %q batch %q attempt %q",
			publicationState, batchState, attemptState)
	}
	if _, err := db.Exec(`DROP TRIGGER test_fail_terminal_compaction`); err != nil {
		t.Fatal(err)
	}
	page, err := attachmentTerminalsReconcile(db, nil, home, 0, 128)
	if err != nil || len(page.Batches) != 1 {
		t.Fatalf("terminal compaction resume = %#v err %v", page, err)
	}
	var batches int
	if err := db.QueryRow(`SELECT COUNT(*) FROM attachment_batches WHERE id=?`,
		reservation.BatchID).Scan(&batches); err != nil {
		t.Fatal(err)
	}
	if batches != 0 {
		t.Fatalf("resumed terminal batch remains: %d", batches)
	}
}

func TestAttachmentTerminalReconcilePagesMoreThan128Rows(t *testing.T) {
	db := dvSpine(t)
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	const manifest = `[{"ordinal":1,"size":1,"media_type":"text/plain","display_name":"one.txt"}]`
	batchIDs := make([]int64, 0, 129)
	firstSession := ""
	for index := 0; index < 129; index++ {
		from := fmt.Sprintf("cli:terminal-page-%03d", index)
		startedAny, err := HomieStart(db, nil, HomieStartArgs{From: from})
		if err != nil {
			t.Fatal(err)
		}
		session := startedAny.(HomieStartResult).SessionID
		if index == 0 {
			firstSession = session
		}
		var bindingID int64
		if err := db.QueryRow(`SELECT id FROM homie_bindings
			WHERE session_id=? AND surface='cli' AND active=1`,
			session).Scan(&bindingID); err != nil {
			t.Fatal(err)
		}
		if _, err := AttachmentIngressEnqueue(db, nil, AttachmentIngressEnqueueArgs{
			Session: session, BindingID: bindingID, Surface: "cli",
			ExternalMessageID: "terminal", Manifest: manifest,
		}); err != nil {
			t.Fatal(err)
		}
		reservation, err := AttachmentIngressReserve(db, nil, AttachmentIngressReserveArgs{
			Session: session, OwnerGeneration: "terminal-page",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := AttachmentAttemptAbort(db, nil, AttachmentAttemptAbortArgs{
			Session: session, AttemptID: reservation.AttemptID,
			OwnerToken: reservation.OwnerToken, OwnerGeneration: "terminal-page",
		}); err != nil {
			t.Fatal(err)
		}
		batchIDs = append(batchIDs, reservation.BatchID)
	}
	first, err := attachmentTerminalsReconcile(db, nil, home, 0, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Batches) != 128 || first.Next != batchIDs[127] {
		t.Fatalf("first terminal page = count %d next %d, want 128/%d",
			len(first.Batches), first.Next, batchIDs[127])
	}
	second, err := attachmentTerminalsReconcile(db, nil, home, first.Next, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Batches) != 1 || second.Next != batchIDs[128] {
		t.Fatalf("second terminal page = %#v, want batch %d", second, batchIDs[128])
	}
	var derived int
	if err := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM attachment_batches)+
		(SELECT COUNT(*) FROM attachment_attempts)+
		(SELECT COUNT(*) FROM attachment_publications)`).Scan(&derived); err != nil {
		t.Fatal(err)
	}
	if derived != 0 {
		t.Fatalf("paged terminal reconciliation left %d derived rows", derived)
	}
	next, err := AttachmentIngressReserve(db, nil, AttachmentIngressReserveArgs{
		Session: firstSession, OwnerGeneration: "terminal-page-next",
	})
	if err != nil {
		t.Fatal(err)
	}
	if next.BatchID <= batchIDs[128] {
		t.Fatalf("post-compaction batch id reused history: %d <= %d",
			next.BatchID, batchIDs[128])
	}
}

func TestAttachmentPublicationPublishResumesDurableCrashBoundaries(t *testing.T) {
	for _, boundary := range []string{"publishing", "object_bound"} {
		t.Run(boundary, func(t *testing.T) {
			const manifest = `[{"ordinal":1,"size":7,"media_type":"text/plain","display_name":"seven.txt"}]`
			db, session, reservation, home := attachmentPublisherFixture(t, manifest)
			args := publishArgs(session, reservation, 1)
			plan, err := loadAttachmentPublicationPlan(db, args)
			if err != nil {
				t.Fatal(err)
			}
			tree, err := openAttachmentTree(home, session)
			if err != nil {
				t.Fatal(err)
			}
			stageFile, stage, err := claimAttachmentStage(db, tree, args, plan)
			if err != nil {
				t.Fatal(err)
			}
			digest, err := writeClaimedAttachmentStage(
				stageFile, tree.staging, plan.stagingName, plan.size,
				bytes.NewReader([]byte("1234567")), stage)
			if err != nil {
				t.Fatal(err)
			}
			if err := recordAttachmentStage(db, args, digest, stage); err != nil {
				t.Fatal(err)
			}
			plan, err = loadAttachmentPublicationPlan(db, args)
			if err != nil {
				t.Fatal(err)
			}
			if boundary == "object_bound" {
				body, err := json.Marshal(attachmentPublicationJournal{
					Version: 1, PublicationID: args.PublicationID, AttemptID: args.AttemptID,
					BatchID: plan.batchID, Session: session, Direction: "in",
					Ordinal: plan.ordinal, Size: plan.size, SHA256: plan.digest,
					StagingName: plan.stagingName, StagingDevice: plan.stagingDevice,
					StagingInode:    plan.stagingInode,
					CanonicalName:   "sha256/" + plan.digest[:2] + "/" + plan.digest,
					OwnerGeneration: args.OwnerGeneration,
				})
				if err != nil {
					t.Fatal(err)
				}
				journal, err := publishAttachmentJournal(db, tree, args, plan, body)
				if err != nil {
					t.Fatal(err)
				}
				plan.journalDevice, plan.journalInode = journal.device, journal.inode
				if _, err := bindAttachmentObject(db, tree, args, plan); err != nil {
					t.Fatal(err)
				}
			}
			if err := tree.close(); err != nil {
				t.Fatal(err)
			}

			resumed, err := AttachmentPublicationPublish(
				db, nil, home, args, bytes.NewReader([]byte("source-is-not-reopened")))
			if err != nil {
				t.Fatal(err)
			}
			if resumed.State != "materialized" || resumed.SHA256 != digest {
				t.Fatalf("resumed receipt = %#v", resumed)
			}
		})
	}
}

func TestAttachmentTerminalUnwindsMaterializedPublicationEdges(t *testing.T) {
	const manifest = `[{"ordinal":1,"size":7,"media_type":"text/plain","display_name":"seven.txt"}]`
	db, session, reservation, home := attachmentPublisherFixture(t, manifest)
	published, err := AttachmentPublicationPublish(
		db, nil, home, publishArgs(session, reservation, 1), bytes.NewReader([]byte("1234567")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AttachmentAttemptAbort(db, nil, AttachmentAttemptAbortArgs{
		Session: session, AttemptID: reservation.AttemptID,
		OwnerToken: reservation.OwnerToken, OwnerGeneration: "native-publisher-1",
	}); err != nil {
		t.Fatal(err)
	}
	var publicationState, objectState string
	var generation, edges int
	if err := db.QueryRow(`SELECT state,gc_generation FROM attachment_publications
		WHERE id=?`, reservation.Publications[0].PublicationID).
		Scan(&publicationState, &generation); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT state FROM attachment_objects
		WHERE session_id=? AND direction='in' AND sha256=? AND size=7`,
		session, published.SHA256).Scan(&objectState); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM attachment_object_publications
		WHERE publication_id=?`, reservation.Publications[0].PublicationID).Scan(&edges); err != nil {
		t.Fatal(err)
	}
	if publicationState != "cleanup_pending" || generation != 1 ||
		objectState != "delete_pending" || edges != 0 {
		t.Fatalf("terminal cleanup = publication %q gen %d object %q edges %d",
			publicationState, generation, objectState, edges)
	}
	cleaned, err := AttachmentPublicationCleanup(db, nil, home,
		AttachmentPublicationCleanupArgs{
			Session: session, PublicationID: reservation.Publications[0].PublicationID,
			GCGeneration: generation,
		})
	if err != nil {
		t.Fatal(err)
	}
	if cleaned.State != "aborted" || cleaned.Replayed {
		t.Fatalf("cleanup receipt = %#v", cleaned)
	}
	var objects int
	if err := db.QueryRow(`SELECT COUNT(*) FROM attachment_objects
		WHERE session_id=? AND direction='in' AND sha256=? AND size=7`,
		session, published.SHA256).Scan(&objects); err != nil {
		t.Fatal(err)
	}
	if objects != 0 {
		t.Fatalf("cleanup retained %d unreferenced canonical objects", objects)
	}
	replay, err := AttachmentPublicationCleanup(db, nil, home,
		AttachmentPublicationCleanupArgs{
			Session: session, PublicationID: reservation.Publications[0].PublicationID,
			GCGeneration: generation,
		})
	if err != nil || !replay.Replayed || replay.State != "aborted" {
		t.Fatalf("cleanup replay = %#v err %v", replay, err)
	}
}

func TestAttachmentPublicationCleanupRefusesReplacedJournalIdentity(t *testing.T) {
	const manifest = `[{"ordinal":1,"size":7,"media_type":"text/plain","display_name":"seven.txt"}]`
	db, session, reservation, home := attachmentPublisherFixture(t, manifest)
	_, err := AttachmentPublicationPublish(
		db, nil, home, publishArgs(session, reservation, 1), bytes.NewReader([]byte("1234567")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AttachmentAttemptAbort(db, nil, AttachmentAttemptAbortArgs{
		Session: session, AttemptID: reservation.AttemptID,
		OwnerToken: reservation.OwnerToken, OwnerGeneration: "native-publisher-1",
	}); err != nil {
		t.Fatal(err)
	}
	publication := reservation.Publications[0]
	journal := filepath.Join(home, "attachments", session, "in", "journal", publication.PublicationID+".json")
	body, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(journal, journal+".retained"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journal, body, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(journal, 0o444); err != nil {
		t.Fatal(err)
	}
	_, err = AttachmentPublicationCleanup(db, nil, home,
		AttachmentPublicationCleanupArgs{
			Session: session, PublicationID: publication.PublicationID, GCGeneration: 1,
		})
	if err == nil || !strings.Contains(err.Error(), "identity drifted") {
		t.Fatalf("replaced journal cleanup error = %v", err)
	}
	if _, err := os.Lstat(journal); err != nil {
		t.Fatalf("cleanup removed replacement journal: %v", err)
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM attachment_publications WHERE id=?`,
		publication.PublicationID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "cleanup_pending" {
		t.Fatalf("replacement cleanup advanced publication to %q", state)
	}
}

func TestAttachmentPublicationCleanupReplayRemovesPreclaimCrashResidue(t *testing.T) {
	const manifest = `[{"ordinal":1,"size":7,"media_type":"text/plain","display_name":"seven.txt"}]`
	db, session, reservation, home := attachmentPublisherFixture(t, manifest)
	if _, err := AttachmentAttemptAbort(db, nil, AttachmentAttemptAbortArgs{
		Session: session, AttemptID: reservation.AttemptID,
		OwnerToken: reservation.OwnerToken, OwnerGeneration: "native-publisher-1",
	}); err != nil {
		t.Fatal(err)
	}
	publication := reservation.Publications[0]
	args := AttachmentPublicationCleanupArgs{
		Session: session, PublicationID: publication.PublicationID, GCGeneration: 1,
	}
	if _, err := AttachmentPublicationCleanup(db, nil, home, args); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(home, "attachments", session, "in")
	stage := filepath.Join(base, "staging", publication.PublicationID+".part")
	tempJournal := filepath.Join(base, "journal", publication.PublicationID+".json.tmp")
	if err := os.WriteFile(stage, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tempJournal, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	replay, err := AttachmentPublicationCleanup(db, nil, home, args)
	if err != nil || !replay.Replayed {
		t.Fatalf("late-residue cleanup replay = %#v err %v", replay, err)
	}
	for _, path := range []string{stage, tempJournal} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("cleanup replay retained late exact residue %q: %v", path, err)
		}
	}
}
