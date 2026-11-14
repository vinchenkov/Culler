package verbs

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"mc/dispatch"
	"mc/domain"
)

var hpRefs atomic.Int64

// hpRef is a new dashboard channel reference, as the dashboard generates one
// for each preparation.
func hpRef() string {
	return fmt.Sprintf("dashboard:dash-%08x", hpRefs.Add(1))
}

// hpPacket records a packaged task with its first packet.
func hpPacket(t *testing.T, db *sql.DB, task int64) (packet, version int64) {
	t.Helper()
	dvInsertTask(t, db, dvTask(task, dispatch.ScopeTask, dispatch.StatusPackaged, 0))
	return dvPublishPacket(t, db, task, "", dvOld)
}

// hpRevisedPacket records a Revise decision on the latest packet of the task
// and the packet of the next submitted version.
func hpRevisedPacket(t *testing.T, db *sql.DB, task, packet, version int64) (int64, int64) {
	t.Helper()
	if _, err := PacketDecide(db, nil, task, "revise", "Rename the exported function.", packet, version); err != nil {
		t.Fatal(err)
	}
	dvCompleteDeliveryRound(t, db, task, fmt.Sprintf("revised-%d-%d", task, packet), strings.Repeat("e", 40))
	for _, status := range []dispatch.Status{dispatch.StatusWorked, dispatch.StatusVerified, dispatch.StatusPackaged} {
		dvExec(t, db, `UPDATE tasks SET status=? WHERE id=?`, string(status), task)
	}
	next, nextVersion := dvPublishPacket(t, db, task, "", dvOld)
	if next == packet || nextVersion == version {
		t.Fatalf("the revised task kept packet %d version %d", packet, version)
	}
	return next, nextVersion
}

func hpPrepare(db *sql.DB, packet, version int64, clear bool) (CollieStartResult, error) {
	result, err := CollieStart(db, nil, CollieStartArgs{From: hpRef(), Packet: packet, PacketVersion: version, Clear: clear})
	if err != nil {
		return CollieStartResult{}, err
	}
	return result.(CollieStartResult), nil
}

func hpMustPrepare(t *testing.T, db *sql.DB, packet, version int64, clear bool) CollieStartResult {
	t.Helper()
	result, err := hpPrepare(db, packet, version, clear)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func hpSend(t *testing.T, db *sql.DB, started CollieStartResult, body string) {
	t.Helper()
	if _, err := CollieSend(db, nil, CollieSendArgs{Session: started.SessionID, From: started.From, Body: body}); err != nil {
		t.Fatal(err)
	}
}

func hpWantPacketChanged(t *testing.T, err error) {
	t.Helper()
	var refusal *DomainError
	if !errors.As(err, &refusal) || refusal.Code != domain.CodeRecordConflict ||
		refusal.Msg != "The review packet changed. Open the current packet." {
		t.Fatalf("the preparation gave %v, want the record-conflict refusal of a changed packet", err)
	}
}

func hpSessions(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	hwGet(t, db, `SELECT count(*) FROM collie_sessions`, &n)
	return n
}

func hpListed(t *testing.T, db *sql.DB, session string) map[string]any {
	t.Helper()
	listed, err := CollieList(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range listed.(map[string]any)["sessions"].([]map[string]any) {
		if row["id"] == session {
			return row
		}
	}
	return nil
}

func hpHistoryBodies(t *testing.T, db *sql.DB, session string) []string {
	t.Helper()
	history, err := CollieHistory(db, nil, session, false)
	if err != nil {
		t.Fatal(err)
	}
	bodies := []string{}
	for _, message := range history.(map[string]any)["messages"].([]map[string]any) {
		bodies = append(bodies, message["body"].(string))
	}
	return bodies
}

func TestColliePacketConversationRefusesAnOlderPacket(t *testing.T) {
	db := dvSpine(t)
	older, olderVersion := hpPacket(t, db, 1)
	latest, latestVersion := hpRevisedPacket(t, db, 1, older, olderVersion)

	_, err := hpPrepare(db, older, olderVersion, false)
	hpWantPacketChanged(t, err)
	_, err = hpPrepare(db, latest+100, latestVersion, false)
	hpWantPacketChanged(t, err)
	if n := hpSessions(t, db); n != 0 {
		t.Fatalf("the refused preparations recorded %d conversations", n)
	}
	if started := hpMustPrepare(t, db, latest, latestVersion, false); started.Status != "active" {
		t.Fatalf("the latest packet gave %+v", started)
	}
}

func TestColliePacketConversationRefusesAnotherVersion(t *testing.T) {
	db := dvSpine(t)
	older, olderVersion := hpPacket(t, db, 1)
	latest, latestVersion := hpRevisedPacket(t, db, 1, older, olderVersion)
	other, otherVersion := hpPacket(t, db, 2)

	for _, version := range []int64{olderVersion, otherVersion, latestVersion + 100} {
		_, err := hpPrepare(db, latest, version, false)
		hpWantPacketChanged(t, err)
	}
	_, err := hpPrepare(db, other, latestVersion, false)
	hpWantPacketChanged(t, err)
	if n := hpSessions(t, db); n != 0 {
		t.Fatalf("the refused preparations recorded %d conversations", n)
	}
}

func TestCollieSessionPacketRecordIsFrozen(t *testing.T) {
	db := dvSpine(t)
	packet, version := hpPacket(t, db, 1)
	other, _ := hpPacket(t, db, 2)
	started := hpMustPrepare(t, db, packet, version, false)
	ordinary := startedSession(t, db, hpRef())

	for _, change := range []struct {
		session string
		value   any
	}{
		{started.SessionID, nil},
		{started.SessionID, other},
		{ordinary, packet},
	} {
		_, err := db.Exec(`UPDATE collie_sessions SET packet_id=? WHERE id=?`, change.value, change.session)
		if err == nil || !strings.Contains(err.Error(), "frozen at start") {
			t.Fatalf("the packet record of %s changed to %v: %v", change.session, change.value, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO collie_sessions (id, container_name, verb_allowlist, session_path, binding, vision_project_id, packet_id)
		VALUES ('h-both', 'mc-collie-h-both', '[]', 'sessions/h-both', 'claude-sdk/claude', 'ws-test', ?)`, packet); err == nil {
		t.Fatal("the spine stored a conversation that is both a Vision conversation and a packet conversation")
	}
	var recorded int64
	hwGet(t, db, fmt.Sprintf(`SELECT packet_id FROM collie_sessions WHERE id='%s'`, started.SessionID), &recorded)
	if recorded != packet {
		t.Fatalf("the packet record is %d, want %d", recorded, packet)
	}
}

func TestColliePacketPreparationReturnsTheActiveConversation(t *testing.T) {
	db := dvSpine(t)
	packet, version := hpPacket(t, db, 1)
	first := hpMustPrepare(t, db, packet, version, false)
	second := hpMustPrepare(t, db, packet, version, false)
	if second.SessionID != first.SessionID || second.Status != "active" || second.From != first.From {
		t.Fatalf("the second preparation gave %+v, want the active conversation %+v", second, first)
	}
	var bindings int
	hwGet(t, db, fmt.Sprintf(`SELECT count(*) FROM collie_bindings WHERE session_id='%s'`, first.SessionID), &bindings)
	if bindings != 1 {
		t.Fatalf("the active conversation has %d bindings, want its first binding only", bindings)
	}
}

func TestColliePacketPreparationResumesTheEndedConversation(t *testing.T) {
	db := dvSpine(t)
	packet, version := hpPacket(t, db, 1)
	first := hpMustPrepare(t, db, packet, version, false)
	hpSend(t, db, first, "Which criterion is weakest?")
	if _, err := CollieEnd(db, nil, first.SessionID, "The session is idle."); err != nil {
		t.Fatal(err)
	}

	resumed := hpMustPrepare(t, db, packet, version, false)
	if resumed.SessionID != first.SessionID || resumed.Status != "active" || resumed.From != first.From {
		t.Fatalf("the preparation gave %+v, want the resumed conversation %+v", resumed, first)
	}
	if bodies := hpHistoryBodies(t, db, first.SessionID); len(bodies) != 1 || bodies[0] != "Which criterion is weakest?" {
		t.Fatalf("the resumed conversation has the history %q", bodies)
	}
	var active int
	var place string
	hwGet(t, db, fmt.Sprintf(`SELECT count(*), max(surface || ':' || channel_ref) FROM collie_bindings WHERE session_id='%s' AND active=1`, first.SessionID), &active, &place)
	if active != 1 || place != first.From {
		t.Fatalf("the resumed conversation is active at %d places (%s), want only %s", active, place, first.From)
	}
	// A send through the returned dashboard binding reaches the same conversation.
	hpSend(t, db, resumed, "And the second criterion?")
	if bodies := hpHistoryBodies(t, db, first.SessionID); len(bodies) != 2 {
		t.Fatalf("the resumed conversation has the history %q", bodies)
	}
}

func TestColliePacketPreparationCreatesAConversation(t *testing.T) {
	db := dvSpine(t)
	packet, version := hpPacket(t, db, 1)
	ref := hpRef()
	result, err := CollieStart(db, nil, CollieStartArgs{From: ref, Packet: packet, PacketVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	started := result.(CollieStartResult)
	if started.Status != "active" || started.From != ref || !strings.HasPrefix(started.SessionID, "h-") {
		t.Fatalf("the preparation gave %+v", started)
	}
	var recorded int64
	var vision sql.NullString
	var allowlist string
	var bound int
	hwGet(t, db, fmt.Sprintf(`SELECT packet_id, vision_project_id, verb_allowlist,
		(SELECT count(*) FROM collie_bindings b WHERE b.session_id=s.id AND b.active=1 AND 'dashboard:' || b.channel_ref=%q)
		FROM collie_sessions s WHERE id='%s'`, ref, started.SessionID), &recorded, &vision, &allowlist, &bound)
	_, defaultAllowlist, err := canonicalCollieAllowlist(nil)
	if err != nil {
		t.Fatal(err)
	}
	if recorded != packet || vision.Valid || allowlist != defaultAllowlist || bound != 1 {
		t.Fatalf("the conversation records packet %d, Vision %v, allowlist %s, and %d bindings", recorded, vision, allowlist, bound)
	}
}

func TestColliePacketPreparationAfterDeletionCreatesAConversation(t *testing.T) {
	db := dvSpine(t)
	packet, version := hpPacket(t, db, 1)
	first := hpMustPrepare(t, db, packet, version, false)
	hpSend(t, db, first, "A question.")
	if _, err := CollieDelete(db, nil, first.SessionID); err != nil {
		t.Fatal(err)
	}
	if hpListed(t, db, first.SessionID) != nil {
		t.Fatal("the deleted conversation stays in the list")
	}

	next := hpMustPrepare(t, db, packet, version, false)
	if next.SessionID == first.SessionID || next.Status != "active" {
		t.Fatalf("the preparation after the deletion gave %+v", next)
	}
	if bodies := hpHistoryBodies(t, db, next.SessionID); len(bodies) != 0 {
		t.Fatalf("the new conversation has the history %q", bodies)
	}
	if row := hpListed(t, db, next.SessionID); row == nil || row["packet"].(map[string]any)["current"] != true {
		t.Fatalf("the new conversation is not the current conversation: %v", row)
	}
}

func TestColliePacketPreparationSharesOneConversation(t *testing.T) {
	db := dvSpine(t)
	packet, version := hpPacket(t, db, 1)
	var group sync.WaitGroup
	ids := make(chan string, 6)
	for i := 0; i < 6; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			started, err := hpPrepare(db, packet, version, false)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- started.SessionID
		}()
	}
	group.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Errorf("the concurrent preparations gave %s and %s", first, id)
		}
	}
	again := hpMustPrepare(t, db, packet, version, false)
	if again.SessionID != first {
		t.Fatalf("the repeated preparation gave %s, want %s", again.SessionID, first)
	}
	if n := hpSessions(t, db); n != 1 {
		t.Fatalf("the preparations recorded %d conversations, want 1", n)
	}
}

func TestColliePacketNewConversationEndsTheCurrentConversation(t *testing.T) {
	db := dvSpine(t)
	packet, version := hpPacket(t, db, 1)
	first := hpMustPrepare(t, db, packet, version, false)
	hpSend(t, db, first, "The first question.")

	next := hpMustPrepare(t, db, packet, version, true)
	if next.SessionID == first.SessionID || next.Status != "active" {
		t.Fatalf("New conversation gave %+v", next)
	}
	var status string
	hwGet(t, db, fmt.Sprintf(`SELECT status FROM collie_sessions WHERE id='%s'`, first.SessionID), &status)
	if status != "ended" {
		t.Fatalf("the earlier conversation is %s, want ended", status)
	}
	if bodies := hpHistoryBodies(t, db, first.SessionID); len(bodies) != 1 {
		t.Fatalf("the ended conversation has the history %q", bodies)
	}
	earlier, current := hpListed(t, db, first.SessionID), hpListed(t, db, next.SessionID)
	if earlier == nil || current == nil {
		t.Fatalf("the list holds %v and %v", earlier, current)
	}
	if earlier["packet"].(map[string]any)["current"] != false || current["packet"].(map[string]any)["current"] != true {
		t.Fatalf("the current flags are %v and %v", earlier["packet"], current["packet"])
	}
	if again := hpMustPrepare(t, db, packet, version, false); again.SessionID != next.SessionID {
		t.Fatalf("the preparation after New conversation gave %s, want %s", again.SessionID, next.SessionID)
	}
}

// The idle stop ends the current conversation. "New conversation" then
// creates a new current conversation and does not resume the ended one.
func TestColliePacketNewConversationAfterTheIdleStop(t *testing.T) {
	db := dvSpine(t)
	packet, version := hpPacket(t, db, 1)
	first := hpMustPrepare(t, db, packet, version, false)
	hpSend(t, db, first, "The first question.")
	if _, err := CollieEnd(db, nil, first.SessionID, "idle timeout"); err != nil {
		t.Fatal(err)
	}

	next := hpMustPrepare(t, db, packet, version, true)
	if next.SessionID == first.SessionID || next.Status != "active" {
		t.Fatalf("New conversation after the idle stop gave %+v", next)
	}
	var status string
	hwGet(t, db, fmt.Sprintf(`SELECT status FROM collie_sessions WHERE id='%s'`, first.SessionID), &status)
	if status != "ended" {
		t.Fatalf("the earlier conversation is %s, want ended", status)
	}
	if bodies := hpHistoryBodies(t, db, first.SessionID); len(bodies) != 1 {
		t.Fatalf("the ended conversation has the history %q", bodies)
	}
	if again := hpMustPrepare(t, db, packet, version, false); again.SessionID != next.SessionID {
		t.Fatalf("the preparation after New conversation gave %s, want %s", again.SessionID, next.SessionID)
	}
}

func TestColliePacketStartRefusesAnotherConversationKind(t *testing.T) {
	db := dvSpine(t)
	packet, version := hpPacket(t, db, 1)
	for _, tc := range []struct {
		args CollieStartArgs
		want string
	}{
		{CollieStartArgs{From: hpRef(), Packet: packet}, "A packet conversation requires --packet and --version."},
		{CollieStartArgs{From: hpRef(), PacketVersion: version}, "A packet conversation requires --packet and --version."},
		{CollieStartArgs{From: hpRef(), Packet: -1, PacketVersion: version}, "A packet conversation requires --packet and --version."},
		{CollieStartArgs{From: "dashboard:vision-ws-test", VisionProject: "ws-test", Packet: packet, PacketVersion: version},
			"A conversation cannot be both a Vision conversation and a packet conversation."},
		{CollieStartArgs{From: "cli:packet", Packet: packet, PacketVersion: version}, "A packet conversation requires a dashboard surface."},
		{CollieStartArgs{From: hpRef(), Packet: packet, PacketVersion: version, Previous: "h-0123456789abcdef", Binding: "endpoint", Request: "request-model-0001"},
			"A model change requires an ordinary dashboard conversation and a valid request identifier."},
		{CollieStartArgs{From: hpRef(), Packet: packet, PacketVersion: version, Binding: "endpoint"},
			"A model change requires an ordinary dashboard conversation and a valid request identifier."},
		{CollieStartArgs{From: hpRef(), Packet: packet, PacketVersion: version, Ensure: true}, "--ensure requires an ordinary dashboard conversation"},
		{CollieStartArgs{From: hpRef(), Clear: true}, "--clear requires --vision-project or --packet"},
	} {
		_, err := CollieStart(db, nil, tc.args)
		var usage *UsageError
		if !errors.As(err, &usage) || usage.Msg != tc.want {
			t.Errorf("CollieStart(%+v) gave %v, want the usage refusal %q", tc.args, err, tc.want)
		}
	}
	if n := hpSessions(t, db); n != 0 {
		t.Fatalf("the refused starts recorded %d conversations", n)
	}
}

func TestCollieListCarriesThePacketAndTheCurrentConversation(t *testing.T) {
	db := dvSpine(t)
	packet, version := hpPacket(t, db, 1)
	ordinary := startedSession(t, db, hpRef())
	first := hpMustPrepare(t, db, packet, version, false)
	second := hpMustPrepare(t, db, packet, version, true)

	if row := hpListed(t, db, ordinary); row == nil || row["packet"] != nil {
		t.Fatalf("the ordinary conversation lists %v", row)
	}
	want := func(current bool) map[string]any {
		return map[string]any{
			"packet_id": packet, "version_id": version, "task_id": int64(1),
			"task_title": "task-1", "project_id": "ws-test", "current": current,
		}
	}
	for session, current := range map[string]bool{first.SessionID: false, second.SessionID: true} {
		row := hpListed(t, db, session)
		if row == nil || fmt.Sprint(row["packet"]) != fmt.Sprint(want(current)) || row["first_message"] != nil {
			t.Fatalf("conversation %s lists %v, want the packet %v", session, row, want(current))
		}
	}
	if _, err := CollieDelete(db, nil, second.SessionID); err != nil {
		t.Fatal(err)
	}
	if row := hpListed(t, db, first.SessionID); row["packet"].(map[string]any)["current"] != false {
		t.Fatalf("after the deletion the earlier conversation lists %v; the packet has no current conversation", row["packet"])
	}
}

func TestCollieEnsureSkipsPacketConversations(t *testing.T) {
	db := dvSpine(t)
	packet, version := hpPacket(t, db, 1)
	started := hpMustPrepare(t, db, packet, version, false)
	result, err := CollieStart(db, nil, CollieStartArgs{From: "dashboard:automatic", Ensure: true})
	if err != nil {
		t.Fatal(err)
	}
	ensured := result.(CollieStartResult)
	if ensured.SessionID == started.SessionID {
		t.Fatalf("--ensure reused the packet conversation %s", started.SessionID)
	}
	var recorded sql.NullInt64
	hwGet(t, db, fmt.Sprintf(`SELECT packet_id FROM collie_sessions WHERE id='%s'`, ensured.SessionID), &recorded)
	if recorded.Valid {
		t.Fatalf("--ensure created a conversation with packet %d", recorded.Int64)
	}
}

func TestCollieModelChangeRefusesAPacketConversation(t *testing.T) {
	db := dvSpine(t)
	packet, version := hpPacket(t, db, 1)
	started := hpMustPrepare(t, db, packet, version, false)
	_, err := CollieStart(db, nil, CollieStartArgs{From: "dashboard:model-test", Previous: started.SessionID, Binding: "endpoint", Request: "request-model-0001"})
	var refusal *DomainError
	if !errors.As(err, &refusal) || refusal.Msg != "The previous conversation is unavailable for this model change." {
		t.Fatalf("the model change gave %v", err)
	}
	if n := hpSessions(t, db); n != 1 {
		t.Fatalf("the refused model change left %d conversations", n)
	}
}

func TestColliePacketContextIsTheRunnerScope(t *testing.T) {
	db := dvSpine(t)
	packet, version := hpPacket(t, db, 1)
	started := hpMustPrepare(t, db, packet, version, false)
	ordinary := startedSession(t, db, hpRef())

	for name, id := range map[string]*RunIdentity{
		"the host":               nil,
		"another conversation":   {Tier: "collie", Role: "collie", RunID: ordinary},
		"a pipeline run":         {Tier: "pipeline", Role: "worker", RunID: started.SessionID},
		"an empty Collie run id": {Tier: "collie", Role: "collie"},
	} {
		if _, err := ColliePacketContext(db, id, started.SessionID); err == nil {
			t.Errorf("%s read the packet context", name)
		}
	}
	_, err := ColliePacketContext(db, &RunIdentity{Tier: "collie", Role: "collie", RunID: ordinary}, ordinary)
	if err == nil || err.Error() != fmt.Sprintf("Collie session %q is not a packet conversation", ordinary) {
		t.Fatalf("the ordinary conversation gave %v", err)
	}
	result, err := ColliePacketContext(db, &RunIdentity{Tier: "collie", Role: "collie", RunID: started.SessionID}, started.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	got := result.(map[string]any)
	if len(got) != 4 || got["session_id"] != started.SessionID || got["packet_id"] != packet || got["version_id"] != version ||
		!strings.HasPrefix(got["context"].(string), "# Packet context\n") {
		t.Fatalf("the packet context is %v", got)
	}
}

// The resident mounts no archived Project, so a packet conversation of an
// archived Project cannot start a container. mc refuses it before a message
// enters the conversation.
func TestColliePacketConversationRefusesAnArchivedProject(t *testing.T) {
	db := dvSpine(t)
	packet, version := hpPacket(t, db, 1)
	other, otherVersion := hpPacket(t, db, 2)
	started := hpMustPrepare(t, db, packet, version, false)
	hpSend(t, db, started, "Which criterion is weakest?")
	if _, err := CollieEnd(db, nil, started.SessionID, "The session is idle."); err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectSetStatus(db, nil, "ws-test", "archived"); err != nil {
		t.Fatal(err)
	}
	wantArchived := func(what string, err error) {
		t.Helper()
		var refusal *DomainError
		if !errors.As(err, &refusal) || refusal.Msg != "The Project of this packet is archived. Conversations about its packets are unavailable." {
			t.Fatalf("%s gave %v, want the refusal of an archived Project", what, err)
		}
	}
	_, err := hpPrepare(db, packet, version, false)
	wantArchived("the preparation of the packet with a conversation", err)
	_, err = hpPrepare(db, other, otherVersion, false)
	wantArchived("the preparation of the packet without a conversation", err)
	_, err = hpPrepare(db, packet, version, true)
	wantArchived("the new conversation", err)
	_, err = CollieSend(db, nil, CollieSendArgs{Session: started.SessionID, From: started.From, Body: "And the second criterion?"})
	wantArchived("the send", err)
	_, err = CollieResume(db, nil, started.SessionID, started.From)
	wantArchived("the resume", err)

	if n := hpSessions(t, db); n != 1 {
		t.Fatalf("the refusals left %d conversations, want 1", n)
	}
	var status string
	hwGet(t, db, fmt.Sprintf(`SELECT status FROM collie_sessions WHERE id='%s'`, started.SessionID), &status)
	if status != "ended" {
		t.Fatalf("the refused conversation has the status %q, want ended", status)
	}
	if bodies := hpHistoryBodies(t, db, started.SessionID); len(bodies) != 1 {
		t.Fatalf("the refused send changed the history to %q", bodies)
	}
	// An ordinary conversation stays available.
	if _, err := CollieStart(db, nil, CollieStartArgs{From: hpRef()}); err != nil {
		t.Fatalf("an ordinary conversation gave %v", err)
	}
}
