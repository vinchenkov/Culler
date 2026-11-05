package verbs

import "testing"

func TestOutboxAckRenderedSkipsOtherProjectsAndSurfaces(t *testing.T) {
	db := dvSpine(t)
	for range 120 {
		dvExec(t, db, `INSERT INTO outbox(kind,surface,payload) VALUES ('blocked_alert','dashboard','{"task_id":99}')`)
	}
	dvExec(t, db, `INSERT INTO outbox(kind,surface,payload) VALUES ('blocked_alert','dashboard','{"task_id":7}'),('blocked_alert','cli','{"task_id":7}'),('health','dashboard','{"activity_id":7}')`)
	result, err := OutboxAckRendered(db, nil, "dashboard", "blocked_alert", []int64{7})
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["acked"] != int64(1) {
		t.Fatal(result)
	}
	var remaining int
	if err := db.QueryRow(`SELECT count(*) FROM outbox WHERE delivered_at IS NULL`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 122 {
		t.Fatalf("remaining=%d", remaining)
	}
	result, err = OutboxAckRendered(db, nil, "dashboard", "blocked_alert", []int64{7})
	if err != nil || result.(map[string]any)["acked"] != int64(0) {
		t.Fatalf("replay: %v, %v", result, err)
	}
	result, err = OutboxAckRendered(db, nil, "dashboard", "health", []int64{7})
	if err != nil || result.(map[string]any)["acked"] != int64(1) {
		t.Fatalf("health: %v, %v", result, err)
	}
	if _, err := OutboxAckRendered(db, nil, "dashboard", "homie_reply", []int64{7}); err == nil {
		t.Fatal("accepted an unsupported kind")
	}
	if _, err := OutboxAckRendered(db, nil, "dashboard", "health", []int64{-1}); err == nil {
		t.Fatal("accepted a negative record id")
	}
}
