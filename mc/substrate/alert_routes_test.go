package substrate_test

import (
	"strings"
	"testing"
)

func TestAlertRoutesFanOutInsideTheEventTransaction(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "proposed")

	if got := oneStr(t, db, `
		SELECT group_concat(class || ':' || ordinal || ':' || surface || ':' ||
			COALESCE(channel_ref, '<null>'), ',')
		FROM (SELECT * FROM alert_routes ORDER BY class, ordinal)`); got !=
		"blocked:1:dashboard:<null>,health:1:dashboard:<null>" {
		t.Fatalf("default alert routes = %q", got)
	}

	mustExec(t, db, `
		INSERT INTO alert_routes (class, ordinal, surface, channel_ref)
		VALUES ('blocked', 2, 'discord', '123456789')`)
	mustExec(t, db, `UPDATE tasks SET blocked=1, blocked_kind='operator', blocked_reason='operator input' WHERE id=?`, task)
	if got := oneStr(t, db, `
		SELECT group_concat(surface || ':' || COALESCE(channel_ref, '<null>') || ':' || payload, '|')
		FROM (SELECT * FROM outbox WHERE kind='blocked_alert' ORDER BY id)`); got !=
		`dashboard:<null>:{"task_id":1,"reason":"operator input","body":"Task #1 blocked: operator input"}|`+
			`discord:123456789:{"task_id":1,"reason":"operator input","body":"Task #1 blocked: operator input"}` {
		t.Fatalf("blocked fan-out = %q", got)
	}

	mustExec(t, db, `
		INSERT INTO alert_routes (class, ordinal, surface, channel_ref)
		VALUES ('health', 2, 'discord', '123456789')`)
	mustExec(t, db, `
		INSERT INTO activity (actor,kind,subject,detail,dispatch_key)
		VALUES ('dispatch','dispatch.health',NULL,'{"code":"health.config_invalid"}',?)`,
		strings.Repeat("a", 64))
	if got := oneInt(t, db, `SELECT COUNT(*) FROM outbox WHERE kind='health'`); got != 2 {
		t.Fatalf("health fan-out rows = %d, want 2", got)
	}
}

func TestMissingAlertProjectionAbortsTheProducingWrite(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "proposed")
	mustExec(t, db, `DELETE FROM alert_routes WHERE class='blocked'`)
	wantAbort(t, db, `UPDATE tasks SET blocked=1, blocked_kind='operator', blocked_reason='lost alert' WHERE id=?`, task)
	if got := oneInt(t, db, `SELECT blocked FROM tasks WHERE id=?`, task); got != 0 {
		t.Fatalf("route-less block committed: blocked=%d", got)
	}

	mustExec(t, db, `DELETE FROM alert_routes WHERE class='health'`)
	wantAbort(t, db, `
		INSERT INTO activity (actor,kind,detail)
		VALUES ('dispatch','dispatch.health','{"code":"health.config_invalid"}')`)
	if got := oneInt(t, db, `SELECT COUNT(*) FROM activity WHERE kind='dispatch.health'`); got != 0 {
		t.Fatalf("route-less health event committed: rows=%d", got)
	}
}

func TestAlertRouteShapeIsClosed(t *testing.T) {
	db := openSpine(t)
	wantAbort(t, db, `
		INSERT INTO alert_routes (class,ordinal,surface,channel_ref)
		VALUES ('unknown',1,'dashboard',NULL)`)
	wantAbort(t, db, `
		INSERT INTO alert_routes (class,ordinal,surface,channel_ref)
		VALUES ('blocked',2,'discord','not-a-snowflake')`)
	wantAbort(t, db, `
		INSERT INTO alert_routes (class,ordinal,surface,channel_ref)
		VALUES ('blocked',2,'dashboard','123')`)
	wantAbort(t, db, `
		INSERT INTO alert_routes (class,ordinal,surface,channel_ref)
		VALUES ('blocked',1,'discord','123')`)
}
