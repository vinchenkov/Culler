package verbs

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"
)

const attachmentManifestFixture = `[
  {"display_name":"capture.png","media_type":"image/png","size":7,"ordinal":1},
  {"ordinal":2,"size":11,"media_type":"text/plain","display_name":"notes.txt"}
]`

func attachmentIngressFixture(t *testing.T) (*sql.DB, string, int64) {
	t.Helper()
	db := dvSpine(t)
	startedAny, err := HomieStart(db, nil, HomieStartArgs{From: "discord:channel-1"})
	if err != nil {
		t.Fatal(err)
	}
	started := startedAny.(HomieStartResult)
	var bindingID int64
	if err := db.QueryRow(`SELECT id FROM homie_bindings
		WHERE session_id=? AND surface='discord' AND active=1`,
		started.SessionID).Scan(&bindingID); err != nil {
		t.Fatal(err)
	}
	return db, started.SessionID, bindingID
}

func TestAttachmentIngressEnqueueIsDurableCanonicalAndIdempotent(t *testing.T) {
	db, session, binding := attachmentIngressFixture(t)
	args := AttachmentIngressEnqueueArgs{
		Session: session, BindingID: binding, Surface: "discord",
		ExternalMessageID: "message-1", Body: "look at this",
		Manifest: attachmentManifestFixture,
	}
	first, err := AttachmentIngressEnqueue(db, nil, args)
	if err != nil {
		t.Fatal(err)
	}
	if first.IngressID <= 0 || !first.Enqueued || first.State != "queued" ||
		len(first.ManifestSHA256) != 64 {
		t.Fatalf("first enqueue = %#v", first)
	}

	replay, err := AttachmentIngressEnqueue(db, nil, args)
	if err != nil {
		t.Fatal(err)
	}
	if replay.IngressID != first.IngressID || replay.Enqueued ||
		replay.ManifestSHA256 != first.ManifestSHA256 {
		t.Fatalf("replay = %#v, first = %#v", replay, first)
	}
	var body, manifest, state string
	if err := db.QueryRow(`SELECT body, manifest, state FROM attachment_ingresses
		WHERE id=?`, first.IngressID).Scan(&body, &manifest, &state); err != nil {
		t.Fatal(err)
	}
	if body != args.Body || manifest != `[{"ordinal":1,"size":7,"media_type":"image/png","display_name":"capture.png"},{"ordinal":2,"size":11,"media_type":"text/plain","display_name":"notes.txt"}]` ||
		state != "queued" {
		t.Fatalf("durable inbox = body %q manifest %q state %q", body, manifest, state)
	}

	changed := args
	changed.Body = "changed after retry"
	if _, err := AttachmentIngressEnqueue(db, nil, changed); err == nil ||
		!strings.Contains(err.Error(), "different sealed content") {
		t.Fatalf("changed replay error = %v", err)
	}
	if _, err := AttachmentIngressEnqueue(db, &RunIdentity{Tier: "homie", RunID: session}, args); err == nil {
		t.Fatal("Homie-scoped caller enqueued native ingress")
	}
	if _, err := HomieEnd(db, nil, session, "binding turnover"); err != nil {
		t.Fatal(err)
	}
	afterEnd, err := AttachmentIngressEnqueue(db, nil, args)
	if err != nil || afterEnd.IngressID != first.IngressID || afterEnd.Enqueued {
		t.Fatalf("exact replay after binding/session end = %#v err %v", afterEnd, err)
	}
}

func TestAttachmentIngressQueuedIsAHostOnlyStableBoundedPage(t *testing.T) {
	db := dvSpine(t)
	start := func(from string) (string, int64) {
		t.Helper()
		startedAny, err := HomieStart(db, nil, HomieStartArgs{From: from})
		if err != nil {
			t.Fatal(err)
		}
		session := startedAny.(HomieStartResult).SessionID
		var binding int64
		if err := db.QueryRow(`SELECT id FROM homie_bindings
			WHERE session_id=? AND active=1`, session).Scan(&binding); err != nil {
			t.Fatal(err)
		}
		return session, binding
	}
	enqueue := func(session string, binding int64, surface, messageID, body string) AttachmentIngressReceipt {
		t.Helper()
		receipt, err := AttachmentIngressEnqueue(db, nil, AttachmentIngressEnqueueArgs{
			Session: session, BindingID: binding, Surface: surface,
			ExternalMessageID: messageID, Body: body,
			Manifest: `[{"ordinal":1,"size":3,"media_type":"text/plain","display_name":"note.txt"}]`,
		})
		if err != nil {
			t.Fatal(err)
		}
		return receipt
	}
	dashboardSession, dashboardBinding := start("dashboard:queue-page")
	firstSkipped := enqueue(dashboardSession, dashboardBinding, "dashboard", "dashboard-page-1", "skip one")
	secondSkipped := enqueue(dashboardSession, dashboardBinding, "dashboard", "dashboard-page-2", "skip two")
	session, binding := start("discord:channel-1")
	discord := enqueue(session, binding, "discord", "discord-page-1", "return me")

	first, err := AttachmentIngressQueued(db, nil, AttachmentIngressQueuedArgs{
		Surface: "discord", After: "0", Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Rows) != 0 || first.Next != strconv.FormatInt(secondSkipped.IngressID, 10) {
		t.Fatalf("first page = %#v", first)
	}
	second, err := AttachmentIngressQueued(db, nil, AttachmentIngressQueuedArgs{
		Surface: "discord", After: first.Next, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Rows) != 1 || second.Next != "" {
		t.Fatalf("second page = %#v", second)
	}
	if got := second.Rows[0]; got.IngressID != strconv.FormatInt(discord.IngressID, 10) ||
		got.Session != session || got.BindingID != strconv.FormatInt(binding, 10) ||
		got.Surface != "discord" || got.ChannelRef != "channel-1" ||
		got.ExternalMessageID != "discord-page-1" || got.Body != "return me" ||
		got.BodySHA256 == "" || got.ManifestSHA256 == "" ||
		got.Manifest != `[{"ordinal":1,"size":3,"media_type":"text/plain","display_name":"note.txt"}]` ||
		got.AttemptCount != 0 || got.CreatedAt == "" {
		t.Fatalf("queued row = %#v", got)
	}
	if _, err := HomieEnd(db, nil, dashboardSession, "retain native inbox"); err != nil {
		t.Fatal(err)
	}
	historical, err := AttachmentIngressQueued(db, nil, AttachmentIngressQueuedArgs{
		Surface: "dashboard", After: "0", Limit: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(historical.Rows) != 2 ||
		historical.Rows[0].BindingID != strconv.FormatInt(dashboardBinding, 10) ||
		historical.Rows[1].BindingID != strconv.FormatInt(dashboardBinding, 10) {
		t.Fatalf("inactive-binding queued rows = %#v", historical)
	}

	reservation, err := AttachmentIngressReserve(db, nil, AttachmentIngressReserveArgs{
		Session: session, OwnerGeneration: "queue-page-owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := AttachmentIngressQueued(db, nil, AttachmentIngressQueuedArgs{
		Surface: "discord", After: "0", Limit: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(restarted.Rows) != 0 || restarted.Next != "" {
		t.Fatalf("restart-from-zero queued page = %#v", restarted)
	}
	terminal, err := AttachmentAttemptAbort(db, nil, AttachmentAttemptAbortArgs{
		Session: session, AttemptID: reservation.AttemptID,
		OwnerToken: reservation.OwnerToken, OwnerGeneration: "queue-page-owner",
	})
	if err != nil || terminal.Outcome != "requeued" {
		t.Fatalf("abort for queue recovery = %#v err %v", terminal, err)
	}
	passedCursor, err := AttachmentIngressQueued(db, nil, AttachmentIngressQueuedArgs{
		Surface: "discord", After: strconv.FormatInt(discord.IngressID, 10), Limit: 128,
	})
	if err != nil || len(passedCursor.Rows) != 0 {
		t.Fatalf("old cursor unexpectedly recovered requeued row = %#v err %v", passedCursor, err)
	}
	recovered, err := AttachmentIngressQueued(db, nil, AttachmentIngressQueuedArgs{
		Surface: "discord", After: "0", Limit: 128,
	})
	if err != nil || len(recovered.Rows) != 1 ||
		recovered.Rows[0].IngressID != strconv.FormatInt(discord.IngressID, 10) {
		t.Fatalf("restart-from-zero did not recover requeued row = %#v err %v", recovered, err)
	}
	var attempts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM attachment_attempts`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("queued reads changed attempt count to %d", attempts)
	}
	if firstSkipped.IngressID >= secondSkipped.IngressID ||
		secondSkipped.IngressID >= discord.IngressID {
		t.Fatalf("fixture ingress order is not stable: %d %d %d",
			firstSkipped.IngressID, secondSkipped.IngressID, discord.IngressID)
	}

	for _, args := range []AttachmentIngressQueuedArgs{
		{Surface: "discord", After: "-1", Limit: 1},
		{Surface: "discord", After: "01", Limit: 1},
		{Surface: "discord", After: "9223372036854775808", Limit: 1},
		{Surface: "invented", After: "0", Limit: 1},
		{Surface: "discord", After: "0", Limit: 0},
		{Surface: "discord", After: "0", Limit: 129},
	} {
		if _, err := AttachmentIngressQueued(db, nil, args); err == nil {
			t.Fatalf("invalid queued page accepted: %#v", args)
		}
	}
	if _, err := AttachmentIngressQueued(
		db,
		&RunIdentity{Tier: "homie", RunID: session},
		AttachmentIngressQueuedArgs{Surface: "discord", After: "0", Limit: 1},
	); err == nil {
		t.Fatal("Homie-scoped caller read the native attachment inbox")
	}
	if _, err := AttachmentIngressQueued(
		nil,
		&RunIdentity{Tier: "homie", RunID: session},
		AttachmentIngressQueuedArgs{Surface: "discord", After: "0", Limit: 1},
	); err == nil {
		t.Fatal("host-scope check touched the database before refusing")
	}
}

func TestAttachmentIngressReserveCreatesTheOldestExactBatchAtomically(t *testing.T) {
	db, session, binding := attachmentIngressFixture(t)
	first, err := AttachmentIngressEnqueue(db, nil, AttachmentIngressEnqueueArgs{
		Session: session, BindingID: binding, Surface: "discord",
		ExternalMessageID: "message-1", Body: "first", Manifest: attachmentManifestFixture,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := AttachmentIngressEnqueue(db, nil, AttachmentIngressEnqueueArgs{
		Session: session, BindingID: binding, Surface: "discord",
		ExternalMessageID: "message-2", Body: "second",
		Manifest: `[{"ordinal":1,"size":3,"media_type":"text/plain","display_name":"later.txt"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterSession(db, &RunIdentity{Tier: "homie", RunID: session},
		session, "native-session-1", "native.jsonl"); err != nil {
		t.Fatal(err)
	}
	if _, err := HomieEnd(db, nil, session, "rotate native binding"); err != nil {
		t.Fatal(err)
	}
	if _, err := HomieResume(db, nil, session, "dashboard:replacement"); err != nil {
		t.Fatal(err)
	}

	reserved, err := AttachmentIngressReserve(db, nil, AttachmentIngressReserveArgs{
		Session: session, OwnerGeneration: "native-boot-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reserved.IngressID != first.IngressID || len(reserved.AttemptID) != 16 ||
		len(reserved.OwnerToken) != 32 || reserved.BatchID <= 0 ||
		reserved.ObjectCount != 2 || reserved.ByteCount != 18 ||
		len(reserved.Publications) != 2 || reserved.Replayed {
		t.Fatalf("reservation = %#v", reserved)
	}
	if reserved.Publications[0].Ordinal != 1 || reserved.Publications[0].Size != 7 ||
		reserved.Publications[1].Ordinal != 2 || reserved.Publications[1].Size != 11 {
		t.Fatalf("publication reservations = %#v", reserved.Publications)
	}

	replay, err := AttachmentIngressReserve(db, nil, AttachmentIngressReserveArgs{
		Session: session, OwnerGeneration: "native-boot-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.AttemptID != reserved.AttemptID ||
		replay.OwnerToken != reserved.OwnerToken || replay.BatchID != reserved.BatchID {
		t.Fatalf("reservation replay = %#v, first = %#v", replay, reserved)
	}
	var firstState, secondState string
	if err := db.QueryRow(`SELECT state FROM attachment_ingresses WHERE id=?`,
		first.IngressID).Scan(&firstState); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT state FROM attachment_ingresses WHERE id=?`,
		second.IngressID).Scan(&secondState); err != nil {
		t.Fatal(err)
	}
	if firstState != "publishing" || secondState != "queued" {
		t.Fatalf("ingress states = first %q second %q", firstState, secondState)
	}
	var publications, bytes int64
	if err := db.QueryRow(`SELECT COUNT(*), SUM(size) FROM attachment_publications
		WHERE batch_id=?`, reserved.BatchID).Scan(&publications, &bytes); err != nil {
		t.Fatal(err)
	}
	if publications != 2 || bytes != 18 {
		t.Fatalf("reserved credits = %d publications, %d bytes", publications, bytes)
	}
}

func TestAttachmentIngressReserveRollsBackTheWholeReservationOnFault(t *testing.T) {
	db, session, binding := attachmentIngressFixture(t)
	ingress, err := AttachmentIngressEnqueue(db, nil, AttachmentIngressEnqueueArgs{
		Session: session, BindingID: binding, Surface: "discord",
		ExternalMessageID: "message-fault", Body: "atomic",
		Manifest: attachmentManifestFixture,
	})
	if err != nil {
		t.Fatal(err)
	}
	dvExec(t, db, `CREATE TRIGGER test_fail_second_attachment_publication
		BEFORE INSERT ON attachment_publications WHEN NEW.ordinal=2
		BEGIN SELECT RAISE(ABORT, 'injected publication reservation failure'); END`)

	if _, err := AttachmentIngressReserve(db, nil, AttachmentIngressReserveArgs{
		Session: session, OwnerGeneration: "native-boot-1",
	}); err == nil ||
		!strings.Contains(err.Error(), "injected publication reservation failure") {
		t.Fatalf("reserve fault = %v", err)
	}
	var state string
	var attempts int
	if err := db.QueryRow(`SELECT state, attempt_count FROM attachment_ingresses
		WHERE id=?`, ingress.IngressID).Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != "queued" || attempts != 0 {
		t.Fatalf("failed reserve changed ingress to state %q attempt_count %d", state, attempts)
	}
	for _, table := range []string{"attachment_attempts", "attachment_batches", "attachment_publications"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("failed reserve left %d rows in %s", count, table)
		}
	}
}

func TestAttachmentPublicationRenewFencesTheExactLiveCapability(t *testing.T) {
	db, session, binding := attachmentIngressFixture(t)
	if _, err := AttachmentIngressEnqueue(db, nil, AttachmentIngressEnqueueArgs{
		Session: session, BindingID: binding, Surface: "discord",
		ExternalMessageID: "message-renew", Body: "renew",
		Manifest: attachmentManifestFixture,
	}); err != nil {
		t.Fatal(err)
	}
	reserved, err := AttachmentIngressReserve(db, nil, AttachmentIngressReserveArgs{
		Session: session, OwnerGeneration: "native-boot-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	publication := reserved.Publications[0]
	renewed, err := AttachmentPublicationRenew(db, nil, AttachmentPublicationRenewArgs{
		Session: session, AttemptID: reserved.AttemptID,
		AttemptOwnerToken:     reserved.OwnerToken,
		PublicationID:         publication.PublicationID,
		PublicationOwnerToken: publication.OwnerToken,
		OwnerGeneration:       "native-boot-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if renewed.AttemptID != reserved.AttemptID ||
		renewed.PublicationID != publication.PublicationID ||
		renewed.HardDeadline != reserved.HardDeadline ||
		renewed.LeaseUntil < reserved.LeaseUntil ||
		renewed.LeaseUntil > renewed.HardDeadline {
		t.Fatalf("renewed lease = %#v, reservation = %#v", renewed, reserved)
	}

	wrong := AttachmentPublicationRenewArgs{
		Session: session, AttemptID: reserved.AttemptID,
		AttemptOwnerToken:     reserved.OwnerToken,
		PublicationID:         publication.PublicationID,
		PublicationOwnerToken: strings.Repeat("0", 32),
		OwnerGeneration:       "native-boot-1",
	}
	if _, err := AttachmentPublicationRenew(db, nil, wrong); err == nil {
		t.Fatal("wrong publication capability renewed a lease")
	}
	wrong.PublicationOwnerToken = publication.OwnerToken
	wrong.AttemptOwnerToken = strings.Repeat("0", 32)
	if _, err := AttachmentPublicationRenew(db, nil, wrong); err == nil {
		t.Fatal("wrong attempt capability renewed a lease")
	}
	wrong.AttemptOwnerToken = reserved.OwnerToken
	wrong.OwnerGeneration = "native-boot-2"
	if _, err := AttachmentPublicationRenew(db, nil, wrong); err == nil {
		t.Fatal("stale native generation renewed a lease")
	}
}

func TestAttachmentAttemptAbortRequeuesTwiceThenFailsOnce(t *testing.T) {
	db, session, binding := attachmentIngressFixture(t)
	first, err := AttachmentIngressEnqueue(db, nil, AttachmentIngressEnqueueArgs{
		Session: session, BindingID: binding, Surface: "discord",
		ExternalMessageID: "message-failing", Body: "cannot publish",
		Manifest: attachmentManifestFixture,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := AttachmentIngressEnqueue(db, nil, AttachmentIngressEnqueueArgs{
		Session: session, BindingID: binding, Surface: "discord",
		ExternalMessageID: "message-next", Body: "next",
		Manifest: `[{"ordinal":1,"size":1,"media_type":"text/plain","display_name":"next.txt"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}

	var firstReceipt AttachmentAttemptTerminalReceipt
	for generation := 1; generation <= 3; generation++ {
		reserved, err := AttachmentIngressReserve(db, nil, AttachmentIngressReserveArgs{
			Session: session, OwnerGeneration: "native-boot-1",
		})
		if err != nil {
			t.Fatal(err)
		}
		if reserved.IngressID != first.IngressID {
			t.Fatalf("generation %d reserved ingress %d, want %d",
				generation, reserved.IngressID, first.IngressID)
		}
		terminal, err := AttachmentAttemptAbort(db, nil, AttachmentAttemptAbortArgs{
			Session: session, AttemptID: reserved.AttemptID,
			OwnerToken: reserved.OwnerToken, OwnerGeneration: "native-boot-1",
		})
		if err != nil {
			t.Fatal(err)
		}
		if terminal.Attempt != generation || terminal.Replayed {
			t.Fatalf("generation %d terminal = %#v", generation, terminal)
		}
		wantOutcome := "requeued"
		if generation == 3 {
			wantOutcome = "failed"
		}
		if terminal.Outcome != wantOutcome || terminal.Reason != "explicit_abort" {
			t.Fatalf("generation %d terminal = %#v, want %s", generation, terminal, wantOutcome)
		}
		if generation == 1 {
			firstReceipt = terminal
			replay, err := AttachmentAttemptAbort(db, nil, AttachmentAttemptAbortArgs{
				Session: session, AttemptID: reserved.AttemptID,
				OwnerToken: reserved.OwnerToken, OwnerGeneration: "native-boot-1",
			})
			if err != nil {
				t.Fatal(err)
			}
			if !replay.Replayed || replay.Receipt != firstReceipt.Receipt {
				t.Fatalf("abort replay = %#v, first = %#v", replay, firstReceipt)
			}
		}
	}

	var state, failureReceipt string
	var attemptCount, failureCount int
	if err := db.QueryRow(`SELECT state,attempt_count,failure_count,failure_receipt
		FROM attachment_ingresses WHERE id=?`, first.IngressID,
	).Scan(&state, &attemptCount, &failureCount, &failureReceipt); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || attemptCount != 3 || failureCount != 3 ||
		failureReceipt == "" {
		t.Fatalf("terminal ingress = state %q attempts %d failures %d receipt %q",
			state, attemptCount, failureCount, failureReceipt)
	}
	var terminalReceipts, failureEvents int
	if err := db.QueryRow(`SELECT COUNT(*) FROM attachment_batch_receipts
		WHERE ingress_id=?`, first.IngressID).Scan(&terminalReceipts); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox
		WHERE session_id=? AND kind='homie_reply'
		  AND json_extract(payload,'$.attachment_ingress_id')=?`,
		session, first.IngressID).Scan(&failureEvents); err != nil {
		t.Fatal(err)
	}
	if terminalReceipts != 3 || failureEvents != 1 {
		t.Fatalf("terminal evidence = %d receipts, %d failure events",
			terminalReceipts, failureEvents)
	}

	next, err := AttachmentIngressReserve(db, nil, AttachmentIngressReserveArgs{
		Session: session, OwnerGeneration: "native-boot-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if next.IngressID != second.IngressID {
		t.Fatalf("failed head blocked next ingress: reserved %d, want %d",
			next.IngressID, second.IngressID)
	}
}

func TestAttachmentAttemptAbortRefusesWrongCapabilityWithoutMutation(t *testing.T) {
	db, session, binding := attachmentIngressFixture(t)
	ingress, err := AttachmentIngressEnqueue(db, nil, AttachmentIngressEnqueueArgs{
		Session: session, BindingID: binding, Surface: "discord",
		ExternalMessageID: "message-fenced", Body: "fenced",
		Manifest: attachmentManifestFixture,
	})
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := AttachmentIngressReserve(db, nil, AttachmentIngressReserveArgs{
		Session: session, OwnerGeneration: "native-boot-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AttachmentAttemptAbort(db, nil, AttachmentAttemptAbortArgs{
		Session: session, AttemptID: reserved.AttemptID,
		OwnerToken: strings.Repeat("0", 32), OwnerGeneration: "native-boot-1",
	}); err == nil {
		t.Fatal("wrong attempt token aborted the reservation")
	}
	var state string
	var failures int
	if err := db.QueryRow(`SELECT state,failure_count FROM attachment_ingresses
		WHERE id=?`, ingress.IngressID).Scan(&state, &failures); err != nil {
		t.Fatal(err)
	}
	if state != "publishing" || failures != 0 {
		t.Fatalf("refused abort changed ingress to %q with %d failures", state, failures)
	}
	if _, err := AttachmentAttemptAbort(db,
		&RunIdentity{Tier: "homie", RunID: session}, AttachmentAttemptAbortArgs{
			Session: session, AttemptID: reserved.AttemptID,
			OwnerToken: reserved.OwnerToken, OwnerGeneration: "native-boot-1",
		}); err == nil {
		t.Fatal("Homie-scoped caller reached host-native attempt abort")
	}
}

func TestAttachmentAttemptHardExpiryUsesDatabaseDeadline(t *testing.T) {
	db, session, binding := attachmentIngressFixture(t)
	if _, err := AttachmentIngressEnqueue(db, nil, AttachmentIngressEnqueueArgs{
		Session: session, BindingID: binding, Surface: "discord",
		ExternalMessageID: "message-expiry", Body: "expiry",
		Manifest: attachmentManifestFixture,
	}); err != nil {
		t.Fatal(err)
	}
	reserved, err := AttachmentIngressReserve(db, nil, AttachmentIngressReserveArgs{
		Session: session, OwnerGeneration: "native-boot-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline, err := time.Parse(spineTime, reserved.HardDeadline)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AttachmentAttemptExpire(db,
		&RunIdentity{Tier: "homie", RunID: session},
		reserved.AttemptID); err == nil {
		t.Fatal("Homie-scoped caller reached host-native hard expiry")
	}
	if _, err := attachmentAttemptExpireAt(db, nil, reserved.AttemptID,
		deadline.Add(-time.Second).Format(spineTime)); err == nil {
		t.Fatal("attempt expired before its hard deadline")
	}
	expired, err := attachmentAttemptExpireAt(db, nil, reserved.AttemptID,
		deadline.Format(spineTime))
	if err != nil {
		t.Fatal(err)
	}
	if expired.Outcome != "requeued" || expired.Reason != "hard_expired" ||
		expired.Attempt != 1 {
		t.Fatalf("hard-expiry receipt = %#v", expired)
	}
	replay, err := attachmentAttemptExpireAt(db, nil, reserved.AttemptID,
		deadline.Add(time.Minute).Format(spineTime))
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.Receipt != expired.Receipt {
		t.Fatalf("hard-expiry replay = %#v, first = %#v", replay, expired)
	}
}

func TestAttachmentAttemptHardExpiryScanIsBoundedAndCursorPaged(t *testing.T) {
	db, firstSession, firstBinding := attachmentIngressFixture(t)
	startedAny, err := HomieStart(db, nil, HomieStartArgs{From: "dashboard:channel-2"})
	if err != nil {
		t.Fatal(err)
	}
	secondSession := startedAny.(HomieStartResult).SessionID
	var secondBinding int64
	if err := db.QueryRow(`SELECT id FROM homie_bindings
		WHERE session_id=? AND surface='dashboard' AND active=1`,
		secondSession).Scan(&secondBinding); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		session string
		binding int64
		surface string
		message string
	}{
		{firstSession, firstBinding, "discord", "expiry-page-1"},
		{secondSession, secondBinding, "dashboard", "expiry-page-2"},
	} {
		if _, err := AttachmentIngressEnqueue(db, nil, AttachmentIngressEnqueueArgs{
			Session: item.session, BindingID: item.binding, Surface: item.surface,
			ExternalMessageID: item.message, Manifest: attachmentManifestFixture,
		}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := AttachmentIngressReserve(db, nil, AttachmentIngressReserveArgs{
		Session: firstSession, OwnerGeneration: "native-boot-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := AttachmentIngressReserve(db, nil, AttachmentIngressReserveArgs{
		Session: secondSession, OwnerGeneration: "native-boot-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstDeadline, _ := time.Parse(spineTime, first.HardDeadline)
	secondDeadline, _ := time.Parse(spineTime, second.HardDeadline)
	at := firstDeadline
	if secondDeadline.After(at) {
		at = secondDeadline
	}
	at = at.Add(time.Second)

	page1, err := attachmentAttemptsExpireDueAt(db, nil, "", 1, at.Format(spineTime))
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Receipts) != 1 || page1.Next == "" {
		t.Fatalf("first expiry page = %#v", page1)
	}
	page2, err := attachmentAttemptsExpireDueAt(db, nil, page1.Next, 1, at.Format(spineTime))
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Receipts) != 1 || page2.Next == "" ||
		page2.Next == page1.Next {
		t.Fatalf("second expiry page = %#v after %#v", page2, page1)
	}
	page3, err := attachmentAttemptsExpireDueAt(db, nil, page2.Next, 1, at.Format(spineTime))
	if err != nil || len(page3.Receipts) != 0 {
		t.Fatalf("final expiry page = %#v err %v", page3, err)
	}
	if _, err := AttachmentAttemptsExpireDue(db, nil, "", 129); err == nil {
		t.Fatal("expiry scan accepted a page larger than 128")
	}
	if _, err := AttachmentAttemptsExpireDue(db,
		&RunIdentity{Tier: "homie", RunID: firstSession}, "", 128); err == nil {
		t.Fatal("Homie-scoped caller reached host-native expiry scan")
	}
}

type attachmentLeaseBoundaryFixture struct {
	db          *sql.DB
	session     string
	attemptID   string
	attemptTok  string
	publication string
	publicTok   string
}

func newAttachmentLeaseBoundaryFixture(t *testing.T, leaseModifier, deadlineModifier string) attachmentLeaseBoundaryFixture {
	t.Helper()
	db, session, binding := attachmentIngressFixture(t)
	manifest := `[{"ordinal":1,"size":1,"media_type":"text/plain","display_name":"expired.txt"}]`
	ingress, err := AttachmentIngressEnqueue(db, nil, AttachmentIngressEnqueueArgs{
		Session: session, BindingID: binding, Surface: "discord",
		ExternalMessageID: "expired-lease", Manifest: manifest,
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical := `[{"ordinal":1,"size":1,"media_type":"text/plain","display_name":"expired.txt"}]`
	batch, err := db.Exec(`INSERT INTO attachment_batches
		(session_id,direction,ingress_id,manifest,manifest_sha256,object_count,byte_count)
		VALUES (?,'in',?,?,?,1,1)`,
		session, ingress.IngressID, canonical, ingress.ManifestSHA256)
	if err != nil {
		t.Fatal(err)
	}
	batchID, _ := batch.LastInsertId()
	// Resolve both instants ONCE and bind the resulting literals into both
	// INSERTs. The seal trigger requires the attempt's publisher_lease_until
	// and hard_deadline to equal the publication's exactly, and
	// `datetime('now')` has one-second resolution. A separate evaluation in
	// each of the two statements makes them differ whenever a second boundary
	// falls between, and the insert then aborts with "attachment attempt
	// requires one complete exact sealed batch". This test failed inside a
	// 130 s full-package run and passed 20/20 when it ran alone, which is the
	// signature of exactly that race. The trigger's exactness is the point and
	// is not the thing to relax.
	var leaseUntil, hardDeadline string
	if err := db.QueryRow(`SELECT datetime('now',?), datetime('now',?)`,
		leaseModifier, deadlineModifier).Scan(&leaseUntil, &hardDeadline); err != nil {
		t.Fatal(err)
	}
	dvExec(t, db, `INSERT INTO attachment_publications
		(id,batch_id,session_id,direction,ordinal,size,media_type,display_name,
		 owner_token,owner_generation,publisher_lease_until,hard_deadline,
		 staging_name,journal_temp_name,journal_name)
		VALUES ('1111111111111111',?,?,'in',1,1,'text/plain','expired.txt',
		        '22222222222222222222222222222222','boot-1',
		        ?,?,
		        '1111111111111111.part','1111111111111111.json.tmp','1111111111111111.json')`,
		batchID, session, leaseUntil, hardDeadline)
	token := "33333333333333333333333333333333"
	dvExec(t, db, `INSERT INTO attachment_attempts
		(id,ingress_id,session_id,batch_id,generation,owner_token,owner_generation,
		 publisher_lease_until,hard_deadline,owner_token_sha256)
		VALUES ('4444444444444444',?,?,?,1,?,'boot-1',
		        ?,?,?)`,
		ingress.IngressID, session, batchID, token, leaseUntil,
		hardDeadline, attachmentCapabilityDigest(token))
	return attachmentLeaseBoundaryFixture{
		db: db, session: session, attemptID: "4444444444444444",
		attemptTok: token, publication: "1111111111111111",
		publicTok: "22222222222222222222222222222222",
	}
}

func (f attachmentLeaseBoundaryFixture) renewArgs() AttachmentPublicationRenewArgs {
	return AttachmentPublicationRenewArgs{
		Session: f.session, AttemptID: f.attemptID, AttemptOwnerToken: f.attemptTok,
		PublicationID: f.publication, PublicationOwnerToken: f.publicTok,
		OwnerGeneration: "boot-1",
	}
}

func TestAttachmentPublicationRenewRefusesLeaseAndDeadlineBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, lease, deadline string
		due                   bool
	}{
		{"expired_lease", "-1 second", "+10 minutes", false},
		{"at_lease_boundary", "+0 seconds", "+10 minutes", false},
		{"at_hard_deadline", "-1 second", "+0 seconds", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newAttachmentLeaseBoundaryFixture(t, tc.lease, tc.deadline)
			if _, err := AttachmentPublicationRenew(fixture.db, nil,
				fixture.renewArgs()); err == nil || !strings.Contains(err.Error(), "expired") {
				t.Fatalf("boundary lease renewal = %v", err)
			}
			page, err := AttachmentAttemptsExpireDue(fixture.db, nil, "", 128)
			if err != nil {
				t.Fatal(err)
			}
			if tc.due && (len(page.Receipts) != 1 ||
				page.Receipts[0].Reason != "hard_expired") {
				t.Fatalf("production due scan = %#v, want one hard expiry", page)
			}
			if !tc.due && len(page.Receipts) != 0 {
				t.Fatalf("production not-due scan = %#v", page)
			}
		})
	}
	fixture := newAttachmentLeaseBoundaryFixture(t, "-1 second", "+10 minutes")
	if _, err := AttachmentPublicationRenew(fixture.db,
		&RunIdentity{Tier: "homie", RunID: fixture.session},
		fixture.renewArgs()); err == nil {
		t.Fatal("Homie-scoped caller reached host-native publication renewal")
	}
}

func TestAttachmentAttemptTerminalRollsBackAtEveryDurableCut(t *testing.T) {
	faults := []struct {
		name string
		sql  string
	}{
		{"outbox", `CREATE TRIGGER test_attachment_terminal_fault
			BEFORE INSERT ON outbox
			WHEN NEW.kind='homie_reply' AND json_extract(NEW.payload,'$.code')='attachment_publish_failed'
			BEGIN SELECT RAISE(ABORT,'injected attachment outbox fault'); END`},
		{"receipt", `CREATE TRIGGER test_attachment_terminal_fault
			BEFORE INSERT ON attachment_batch_receipts
			BEGIN SELECT RAISE(ABORT,'injected attachment receipt fault'); END`},
		{"publication", `CREATE TRIGGER test_attachment_terminal_fault
			BEFORE UPDATE OF state ON attachment_publications
			WHEN NEW.state='cleanup_pending'
			BEGIN SELECT RAISE(ABORT,'injected attachment publication fault'); END`},
		{"batch", `CREATE TRIGGER test_attachment_terminal_fault
			BEFORE UPDATE OF state ON attachment_batches
			WHEN NEW.state='cleanup_pending'
			BEGIN SELECT RAISE(ABORT,'injected attachment batch fault'); END`},
		{"ingress", `CREATE TRIGGER test_attachment_terminal_fault
			BEFORE UPDATE OF state ON attachment_ingresses
			WHEN NEW.state='failed'
			BEGIN SELECT RAISE(ABORT,'injected attachment ingress fault'); END`},
		{"attempt", `CREATE TRIGGER test_attachment_terminal_fault
			BEFORE UPDATE OF state ON attachment_attempts
			WHEN NEW.state='cleanup_pending'
			BEGIN SELECT RAISE(ABORT,'injected attachment attempt fault'); END`},
	}
	for _, fault := range faults {
		t.Run(fault.name, func(t *testing.T) {
			db, session, binding := attachmentIngressFixture(t)
			ingress, err := AttachmentIngressEnqueue(db, nil, AttachmentIngressEnqueueArgs{
				Session: session, BindingID: binding, Surface: "discord",
				ExternalMessageID: "terminal-fault", Manifest: attachmentManifestFixture,
			})
			if err != nil {
				t.Fatal(err)
			}
			for generation := 1; generation <= 2; generation++ {
				reserved, err := AttachmentIngressReserve(db, nil, AttachmentIngressReserveArgs{
					Session: session, OwnerGeneration: "boot-1",
				})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := AttachmentAttemptAbort(db, nil, AttachmentAttemptAbortArgs{
					Session: session, AttemptID: reserved.AttemptID,
					OwnerToken: reserved.OwnerToken, OwnerGeneration: "boot-1",
				}); err != nil {
					t.Fatal(err)
				}
			}
			third, err := AttachmentIngressReserve(db, nil, AttachmentIngressReserveArgs{
				Session: session, OwnerGeneration: "boot-1",
			})
			if err != nil {
				t.Fatal(err)
			}
			dvExec(t, db, fault.sql)
			if _, err := AttachmentAttemptAbort(db, nil, AttachmentAttemptAbortArgs{
				Session: session, AttemptID: third.AttemptID,
				OwnerToken: third.OwnerToken, OwnerGeneration: "boot-1",
			}); err == nil || !strings.Contains(err.Error(), "injected attachment") {
				t.Fatalf("terminal fault = %v", err)
			}
			var ingressState, attemptState, batchState string
			var failures, receipts, events, pendingPublications int
			if err := db.QueryRow(`SELECT state,failure_count FROM attachment_ingresses
				WHERE id=?`, ingress.IngressID).Scan(&ingressState, &failures); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT state FROM attachment_attempts
				WHERE id=?`, third.AttemptID).Scan(&attemptState); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT state FROM attachment_batches
				WHERE id=?`, third.BatchID).Scan(&batchState); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM attachment_publications
				WHERE batch_id=? AND state='cleanup_pending'`,
				third.BatchID).Scan(&pendingPublications); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM attachment_batch_receipts
				WHERE attempt_id=?`, third.AttemptID).Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM outbox
				WHERE session_id=? AND json_extract(payload,'$.attachment_ingress_id')=?`,
				session, ingress.IngressID).Scan(&events); err != nil {
				t.Fatal(err)
			}
			if ingressState != "publishing" || failures != 2 ||
				attemptState != "active" || batchState != "publishing" ||
				pendingPublications != 0 || receipts != 0 || events != 0 {
				t.Fatalf("partial terminal after %s fault: ingress %s/%d attempt %s batch %s publications %d receipts %d events %d",
					fault.name, ingressState, failures, attemptState, batchState,
					pendingPublications, receipts, events)
			}
		})
	}
}
