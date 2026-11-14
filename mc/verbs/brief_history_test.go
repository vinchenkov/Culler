package verbs

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"mc/dispatch"
	"mc/domain"
)

func insertHistoricalTask(t *testing.T, db *sql.DB, id int64, project, decision, decidedAt string) {
	t.Helper()
	task := dvTask(id, dispatch.ScopeTask, dispatch.StatusProposed, 2)
	task.Project = project
	task.Title = fmt.Sprintf("Recorded title %d <&>", id)
	dvInsertTask(t, db, task)
	dvExec(t, db, `UPDATE tasks SET description=?, decision=?, decided_at=?, archived=1 WHERE id=?`,
		strings.Repeat("Historical description must remain absent. ", 100), decision, decidedAt, id)
}

func historicalRows(t *testing.T, brief runBrief, heading string) []map[string]any {
	t.Helper()
	text, err := renderRunBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "Historical description must remain absent.") {
		t.Fatal("The brief includes a historical description.")
	}
	var rows []map[string]any
	parseRenderedBrief(t, text).part(heading).rows(t, &rows)
	return rows
}

func historicalIDs(rows []map[string]any) []int64 {
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, int64(row["id"].(float64)))
	}
	return ids
}

func TestProposalBriefHistoryLimits(t *testing.T) {
	for _, tc := range []struct {
		cancelled, rejected         int
		wantCancelled, wantRejected int
	}{
		{0, 25, 0, 20},
		{3, 25, 3, 17},
		{15, 25, 10, 10},
		{15, 2, 10, 2},
		{0, 0, 0, 0},
	} {
		t.Run(fmt.Sprintf("cancelled_%d_rejected_%d", tc.cancelled, tc.rejected), func(t *testing.T) {
			db := dvSpine(t)
			for i := 1; i <= tc.rejected; i++ {
				insertHistoricalTask(t, db, int64(i), "ws-test", "rejected", "2000-01-01 00:00:00")
			}
			for i := 1; i <= tc.cancelled; i++ {
				insertHistoricalTask(t, db, int64(100+i), "ws-test", "cancelled", "2000-01-01 00:00:00")
			}
			strategist := briefFor(t, db, dispatch.RoleStrategist, nil, briefInputs{})
			arbiter := briefFor(t, db, dispatch.RoleArbiter, nil, briefInputs{})
			for _, group := range []struct {
				heading, decision string
				available, want   int
				base              int64
			}{
				{partCancelled, "cancelled", tc.cancelled, tc.wantCancelled, 100},
				{partRejected, "rejected", tc.rejected, tc.wantRejected, 0},
			} {
				rows := historicalRows(t, strategist, group.heading)
				if got := historicalRows(t, arbiter, group.heading); !reflect.DeepEqual(got, rows) {
					t.Fatalf("The roles receive different %s entries: %v and %v", group.heading, rows, got)
				}
				if len(rows) != group.want {
					t.Fatalf("%s contains %d tasks, want %d", group.heading, len(rows), group.want)
				}
				if group.want == 0 {
					if strategist.part(group.heading) != nil || arbiter.part(group.heading) != nil {
						t.Fatalf("The briefs include the empty %s part.", group.heading)
					}
					continue
				}
				for i, row := range rows {
					id := group.base + int64(group.available-i)
					want := map[string]any{"id": float64(id), "title": fmt.Sprintf("Recorded title %d <&>", id), "decision": group.decision}
					if !reflect.DeepEqual(row, want) {
						t.Fatalf("%s entry %d = %v, want %v", group.heading, i, row, want)
					}
				}
				part := strategist.part(group.heading)
				if sentences := strings.Count(part.Explanation, ". ") + 1; sentences > 2 {
					t.Fatalf("%s has %d explanation sentences.", group.heading, sentences)
				}
			}
		})
	}
}

func TestProposalBriefHistoryUsesDecisionOrderAndProject(t *testing.T) {
	db := dvSpine(t)
	dvAddProject(t, db, "other", "active")
	for _, decision := range []string{"rejected", "cancelled"} {
		base := int64(0)
		if decision == "cancelled" {
			base = 3
		}
		insertHistoricalTask(t, db, base+1, "ws-test", decision, "2001-01-01 00:00:00")
		insertHistoricalTask(t, db, base+2, "ws-test", decision, "2000-01-01 00:00:00")
		insertHistoricalTask(t, db, base+3, "ws-test", decision, "2001-01-01 00:00:00")
		insertHistoricalTask(t, db, base+100, "other", decision, "2002-01-01 00:00:00")
	}
	for _, role := range []dispatch.Role{dispatch.RoleStrategist, dispatch.RoleArbiter} {
		brief := briefFor(t, db, role, nil, briefInputs{})
		for heading, want := range map[string][]int64{partRejected: {3, 1, 2}, partCancelled: {6, 4, 5}} {
			if got := historicalIDs(historicalRows(t, brief, heading)); !reflect.DeepEqual(got, want) {
				t.Fatalf("%s %s tasks = %v, want %v", role, heading, got, want)
			}
		}
	}
}

func TestProposalBriefHistoryReflectsCancellationAndPreservesReason(t *testing.T) {
	for _, tc := range []struct{ name, reason, want string }{
		{"short", "  Low\tvalue.\n\nChoose\u2003other work.  ", "Low value. Choose other work."},
		{"limit", strings.Repeat("界", 160), strings.Repeat("界", 160)},
		{"long", strings.Repeat("x", 161), strings.Repeat("x", 159) + "…"},
		{"unicode", strings.Repeat("界🙂", 81), strings.Repeat("界🙂", 79) + "界…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := dvSpine(t)
			dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
			roles := []dispatch.Role{dispatch.RoleStrategist, dispatch.RoleArbiter}
			for _, role := range roles {
				if brief := briefFor(t, db, role, nil, briefInputs{}); brief.part(partCancelled) != nil {
					t.Fatal("The brief includes a task before its cancellation.")
				}
			}
			if err := domain.Cancel(context.Background(), db, 1, tc.reason, "operator"); err != nil {
				t.Fatal(err)
			}
			dvExec(t, db, `INSERT INTO activity(actor,kind,subject,detail) VALUES('operator','task.updated','1','Unrelated text')`)
			for _, role := range roles {
				brief := briefFor(t, db, role, nil, briefInputs{})
				rows := historicalRows(t, brief, partCancelled)
				if len(rows) != 1 {
					t.Fatalf("%s includes %d cancelled tasks, want 1", role, len(rows))
				}
				want := map[string]any{"id": float64(1), "title": "task-1", "decision": "cancelled", "reason": tc.want}
				if !reflect.DeepEqual(rows[0], want) {
					t.Fatalf("%s cancellation = %v, want %v", role, rows[0], want)
				}
				reason := rows[0]["reason"].(string)
				if !utf8.ValidString(reason) || utf8.RuneCountInString(reason) > 160 {
					t.Fatalf("The cancellation excerpt violates the Unicode limit: %q", reason)
				}
			}
			var stored string
			if err := db.QueryRow(`SELECT detail FROM activity WHERE kind='task.cancelled' AND subject='1'`).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if stored != tc.reason {
				t.Fatalf("The stored reason changed: %q", stored)
			}
		})
	}
}

func TestProposalBriefHistoryPreservesCurrentTasksAndVision(t *testing.T) {
	db := dvSpine(t)
	for id := int64(1); id <= 25; id++ {
		insertHistoricalTask(t, db, id, "ws-test", "cancelled", "2000-01-01 00:00:00")
		insertHistoricalTask(t, db, id+25, "ws-test", "rejected", "2000-01-01 00:00:00")
		dvInsertTask(t, db, dvTask(id+100, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
		dvExec(t, db, `UPDATE tasks SET description='Active task criteria' WHERE id=?`, id+100)
	}
	dvInsertTask(t, db, dvTask(200, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	dvExec(t, db, `UPDATE tasks SET description='Proposal criteria' WHERE id=200`)
	vision := &ProjectVisionCheck{Ready: true, Content: strings.Repeat("The complete Project vision. ", 100)}
	for _, role := range []dispatch.Role{dispatch.RoleStrategist, dispatch.RoleArbiter} {
		brief, err := buildRunBrief(context.Background(), db, &dispatch.Spawn{
			Project: "ws-test", Role: role, ProposedPool: []int64{200},
		}, "history-run", briefInputs{Vision: vision})
		if err != nil {
			t.Fatal(err)
		}
		if got := brief.part(partVision).Text; got != vision.Content {
			t.Fatalf("%s lost the full vision.", role)
		}
		if got := len(historicalRows(t, brief, partRejected)) + len(historicalRows(t, brief, partCancelled)); got != 20 {
			t.Fatalf("%s includes %d historical tasks, want 20", role, got)
		}
		text, err := renderRunBrief(brief)
		if err != nil {
			t.Fatal(err)
		}
		rendered := parseRenderedBrief(t, text)
		var active []map[string]any
		rendered.part(partActiveTasks).rows(t, &active)
		if role == dispatch.RoleStrategist {
			if len(active) != 26 {
				t.Fatalf("The Strategist receives %d active tasks, want 26", len(active))
			}
			for _, row := range active {
				if len(row) != 3 || row["id"] == nil || row["title"] == nil || row["status"] == nil {
					t.Fatalf("The Strategist active entry has unexpected fields: %v", row)
				}
			}
		} else {
			if len(active) != 25 {
				t.Fatalf("The Arbiter receives %d active tasks, want 25", len(active))
			}
			for _, row := range active {
				if row["description"] != "Active task criteria" {
					t.Fatalf("The Arbiter lost active task criteria: %v", row)
				}
			}
			var pool []map[string]any
			rendered.part(partPool).rows(t, &pool)
			if len(pool) != 1 || pool[0]["description"] != "Proposal criteria" {
				t.Fatalf("The Arbiter lost proposal criteria: %v", pool)
			}
		}
	}
}
