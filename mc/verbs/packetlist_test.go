package verbs

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"mc/dispatch"
)

func TestPacketListLiveNarrowsWithoutMovingTheDefault(t *testing.T) {
	db := dvSpine(t)
	seedPacket(t, db, 1, "decided", true)
	seedPacket(t, db, 2, "awaiting", false)
	seedPacket(t, db, 3, "also decided", true)

	all, err := PacketList(db, PacketSelection{})
	if err != nil {
		t.Fatal(err)
	}
	allRows := packetRows(t, all)
	if len(allRows) != 3 {
		t.Fatalf("default returned %d packets, want all 3 — the default must not move", len(allRows))
	}

	live, err := PacketList(db, PacketSelection{Live: true})
	if err != nil {
		t.Fatal(err)
	}
	liveRows := packetRows(t, live)
	if len(liveRows) != 1 {
		t.Fatalf("--live returned %d packets, want the 1 still awaiting a decision", len(liveRows))
	}
	if got := liveRows[0]["task_id"]; got != int64(2) {
		t.Fatalf("--live returned task_id %v, want 2", got)
	}
	// Every column the default carries is still there: `--live` is a predicate,
	// not a projection, so a caller can switch to it without losing fields.
	for _, column := range []string{"task_id", "title", "render_path", "thesis", "archived", "created_at"} {
		if _, ok := liveRows[0][column]; !ok {
			t.Fatalf("--live dropped column %q; it must narrow rows, not columns", column)
		}
	}
}

func TestPacketListNamesTheTitleOfEachTask(t *testing.T) {
	db := dvSpine(t)
	seedPacket(t, db, 1, "decided", true)
	seedPacket(t, db, 2, "awaiting", false)
	dvExec(t, db, `UPDATE tasks SET title=? WHERE id=?`, "Add the README", 2)

	if got := packetRow(t, db, false, 1)["title"]; got != "task-1" {
		t.Fatalf("title of the decided task = %v, want task-1", got)
	}
	if got := packetRow(t, db, true, 2)["title"]; got != "Add the README" {
		t.Fatalf("title of the live task = %v, want the task title", got)
	}
}

// An empty live set is an empty list, never an error and never the whole
// table — that distinction is the entire point of the flag on a board whose
// review queue is usually empty.
func TestPacketListLiveOnAFullyDecidedBoardIsEmpty(t *testing.T) {
	db := dvSpine(t)
	seedPacket(t, db, 1, "decided", true)
	seedPacket(t, db, 2, "decided", true)

	live, err := PacketList(db, PacketSelection{Live: true})
	if err != nil {
		t.Fatal(err)
	}
	if rows := packetRows(t, live); len(rows) != 0 {
		t.Fatalf("--live on a fully decided board returned %d packets, want none", len(rows))
	}
}

func packetRows(t *testing.T, result any) []map[string]any {
	t.Helper()
	wrapper, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("PacketList returned %T, want map[string]any", result)
	}
	rows, ok := wrapper["packets"].([]map[string]any)
	if !ok {
		t.Fatalf("PacketList payload is %T, want []map[string]any", wrapper["packets"])
	}
	return rows
}

// seedPacket builds a packet the way the substrate triggers require rather than
// by writing the end state: a packet is born only from a packaged task and is
// always born live, so an archived packet is reached by approving and
// archiving one.
func seedPacket(t *testing.T, db *sql.DB, taskID int64, thesis string, archived bool) {
	t.Helper()
	dvInsertTask(t, db, dvTask(taskID, dispatch.ScopeTask, dispatch.StatusPackaged, 0))
	dvExec(t, db, `UPDATE tasks SET thesis=? WHERE id=?`, thesis, taskID)
	dvPublishPacket(t, db, taskID, "", dvOld)
	if archived {
		// A packet cannot outlive its task's archival state: the trigger
		// requires the owning task archived first.
		now := time.Now()
		dvApproveTask(t, db, taskID, &now)
		dvExec(t, db, `UPDATE tasks SET archived = 1 WHERE id = ?`, taskID)
	}
}

func packetRow(t *testing.T, db *sql.DB, live bool, task int64) map[string]any {
	t.Helper()
	result, err := PacketList(db, PacketSelection{Live: live})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range packetRows(t, result) {
		if row["task_id"] == task {
			return row
		}
	}
	return nil
}

// The real merge application and a failed merge preparation after an approval
// have their own delivery tests. This test covers each review state through the
// operator verbs on recorded packets.
func TestPacketListStatesTheReviewOfEachLatestPacket(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `UPDATE settings SET review_wip_cap=10 WHERE id=1`)
	packaged := func(task int64) (int64, int64) {
		dvInsertTask(t, db, dvTask(task, dispatch.ScopeTask, dispatch.StatusPackaged, 2))
		return dvPublishPacket(t, db, task, "", dvOld)
	}
	decide := func(task int64, decision, reason string) {
		packet, version := packaged(task)
		if _, err := PacketDecide(db, nil, task, decision, reason, packet, version); err != nil {
			t.Fatal(err)
		}
	}
	const (
		awaiting int64 = iota + 1
		approved
		revised
		merged
		reviewCancel
		boardCancel
		replaced
	)
	packaged(awaiting)
	if _, err := TaskBlock(db, nil, awaiting, "The operator must confirm the license."); err != nil {
		t.Fatal(err)
	}
	decide(approved, "approve", "")
	decide(revised, "revise", "Rename the exported function.")
	decide(merged, "approve", "")
	// The merge application archives the approved task.
	dvExec(t, db, `UPDATE tasks SET archived=1 WHERE id=?`, merged)
	decide(reviewCancel, "cancel", "The Review page cancel reason.")
	packaged(boardCancel)
	boardRequest(t, db, BoardWriteRequest{OperationID: "cancel-board", Action: "cancel", TaskID: boardCancel,
		ExpectedVersion: boardVersion(t, db, boardCancel), Reason: boardText("The Board cancel reason.")})
	packaged(replaced)
	boardRevision(t, db, replaced, "Revised requirements")

	for _, want := range []struct {
		task                          int64
		state, reason, cause, blocked string
		decided                       bool
	}{
		{awaiting, "awaits_decision", "", "", "The operator must confirm the license.", false},
		{approved, "approved", "", "", "", true},
		{revised, "returned", "Rename the exported function.", "revise", "", false},
		{merged, "merged", "", "", "", true},
		{reviewCancel, "cancelled", "The Review page cancel reason.", "", "", true},
		{boardCancel, "cancelled", "The Board cancel reason.", "", "", true},
		{replaced, "replaced", "", "", "", false},
	} {
		row := packetRow(t, db, false, want.task)
		if row == nil {
			t.Fatalf("task %d has no packet row", want.task)
		}
		if row["review_state"] != want.state || fmt.Sprint(nullText(row["reason"])) != want.reason ||
			fmt.Sprint(nullText(row["return_cause"])) != want.cause || fmt.Sprint(nullText(row["blocked_reason"])) != want.blocked {
			t.Fatalf("task %d: %v %v %v %v, want %+v", want.task, row["review_state"], row["reason"], row["return_cause"], row["blocked_reason"], want)
		}
		if (row["decided_at"] != nil) != want.decided {
			t.Fatalf("task %d decision time %v, want present=%t", want.task, row["decided_at"], want.decided)
		}
		if (row["blocked"] == int64(1)) != (want.blocked != "") {
			t.Fatalf("task %d blocked flag %v", want.task, row["blocked"])
		}
		inLive := packetRow(t, db, true, want.task) != nil
		if inLive != (want.state == "awaits_decision" || want.state == "approved" || want.state == "returned") {
			t.Fatalf("task %d in the live list = %t for state %s", want.task, inLive, want.state)
		}
	}
	if row := packetRow(t, db, false, merged); row["merge_commit"] != strings.Repeat("c", 40) || row["destination_branch"] != "main" {
		t.Fatalf("the merged packet names %v on %v", row["merge_commit"], row["destination_branch"])
	}
	if row := packetRow(t, db, false, approved); row["merge_commit"] != nil {
		t.Fatalf("the approved packet names a merge commit before the merge: %v", row["merge_commit"])
	}
}

func nullText(value any) any {
	if value == nil {
		return ""
	}
	return value
}

func TestPacketListSelectsTheLatestPacketOfOneTask(t *testing.T) {
	db := dvSpine(t)
	older, olderVersion := hpPacket(t, db, 1)
	latest, latestVersion := hpRevisedPacket(t, db, 1, older, olderVersion)
	hpPacket(t, db, 2)
	seedPacket(t, db, 3, "decided", true)

	all, err := PacketList(db, PacketSelection{})
	if err != nil {
		t.Fatal(err)
	}
	var unselected map[string]any
	for _, row := range packetRows(t, all) {
		if row["task_id"] == int64(1) {
			unselected = row
		}
	}
	selected, err := PacketList(db, PacketSelection{Task: 1})
	if err != nil {
		t.Fatal(err)
	}
	rows := packetRows(t, selected)
	if len(rows) != 1 || rows[0]["packet_id"] != latest || rows[0]["version_id"] != latestVersion {
		t.Fatalf("the selection of task 1 returned %v, want only packet %d version %d", rows, latest, latestVersion)
	}
	if fmt.Sprint(rows[0]) != fmt.Sprint(unselected) {
		t.Fatalf("the selection changed the packet record:\n%v\nwant\n%v", rows[0], unselected)
	}
	for _, selection := range []PacketSelection{{Task: 3, Live: true}, {Task: 99}} {
		result, err := PacketList(db, selection)
		if err != nil {
			t.Fatal(err)
		}
		if rows := packetRows(t, result); len(rows) != 0 {
			t.Fatalf("the selection %+v returned %v, want no packet", selection, rows)
		}
	}
	live, err := PacketList(db, PacketSelection{Task: 2, Live: true})
	if err != nil {
		t.Fatal(err)
	}
	if rows := packetRows(t, live); len(rows) != 1 || rows[0]["task_id"] != int64(2) {
		t.Fatalf("the live selection of task 2 returned %v", rows)
	}
}

// Each row names the packet of the same task that came directly before the
// latest packet. The quick chat links to the conversation of that packet.
func TestPacketListNamesThePreviousPacketOfTheTask(t *testing.T) {
	db := dvSpine(t)
	first, firstVersion := hpPacket(t, db, 1)
	// The packet of another task comes between the two packets of task 1.
	hpPacket(t, db, 2)
	second, secondVersion := hpRevisedPacket(t, db, 1, first, firstVersion)
	third, _ := hpRevisedPacket(t, db, 1, second, secondVersion)

	if row := packetRow(t, db, false, 1); row["packet_id"] != third || row["previous_packet_id"] != second {
		t.Fatalf("task 1 lists packet %v after %v, want packet %d after %d", row["packet_id"], row["previous_packet_id"], third, second)
	}
	if row := packetRow(t, db, false, 2); row["previous_packet_id"] != nil {
		t.Fatalf("the first packet of task 2 names the previous packet %v", row["previous_packet_id"])
	}
}
