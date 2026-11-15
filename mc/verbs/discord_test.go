package verbs

import (
	"strings"
	"testing"
)

func TestDiscordAvailabilityRequiresAcceptedSetupAndPersists(t *testing.T) {
	db := environmentFixture(t)
	check := func(want bool) {
		t.Helper()
		result, err := DiscordAvailable(db, false)
		if err != nil || result.(map[string]any)["available"] != want {
			t.Fatalf("availability: %v, %v", result, err)
		}
	}
	check(false)
	op := environmentWork(t, db)[0]
	environmentCall(t, db, environmentSetupID(op), "report", EnvironmentRequest{OperationID: op.ID, Outcome: "complete", Summary: "The environment is ready.", FilesDigest: environmentTestFiles})
	check(false)
	accept := environmentWork(t, db)[0]
	environmentCall(t, db, nil, "operation-result", EnvironmentRequest{OperationID: accept.ID, InputsDigest: accept.InputsDigest, Status: "succeeded", FilesDigest: environmentTestFiles, SourceCommit: environmentTestCommit})
	check(true)
	dvExec(t, db, `UPDATE projects SET status='archived'`)
	check(true)
	dvExec(t, db, `UPDATE settings SET discord_available=0 WHERE id=1`)
	check(true)
}

func TestDiscordProjectionRetiresMessagesWithoutDelivery(t *testing.T) {
	db := dvSpine(t)
	first := DiscordProjection{Connection: strings.Repeat("a", 64), Enabled: true, Channel: "23", Blocked: true, Health: true}
	if _, err := DiscordApply(db, first); err != nil {
		t.Fatal(err)
	}
	dvExec(t, db, `INSERT INTO outbox(kind,surface,channel_ref,payload) VALUES('health','discord','23','{"body":"old"}')`)
	second := DiscordProjection{Connection: strings.Repeat("b", 64), Enabled: true, Channel: "24", Blocked: true}
	if _, err := DiscordApply(db, second); err != nil {
		t.Fatal(err)
	}
	rows, err := OutboxPoll(db, nil, "discord", 100)
	if err != nil || len(rows.(map[string]any)["rows"].([]map[string]any)) != 0 {
		t.Fatalf("retired messages returned: %v %v", rows, err)
	}
	var retired, delivered int
	if err := db.QueryRow(`SELECT count(retired_at),count(delivered_at) FROM outbox WHERE surface='discord'`).Scan(&retired, &delivered); err != nil {
		t.Fatal(err)
	}
	if retired != 1 || delivered != 0 {
		t.Fatalf("retired=%d delivered=%d", retired, delivered)
	}
	if _, err := OutboxAck(db, nil, 1, "discord"); err == nil {
		t.Fatal("A retired message received a delivery receipt.")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM alert_routes WHERE surface='dashboard'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatal("The dashboard routes changed.")
	}
	if _, err := DiscordApply(db, DiscordProjection{Connection: strings.Repeat("c", 64)}); err != nil {
		t.Fatal(err)
	}
	result, err := DiscordAvailable(db, false)
	if err != nil || result.(map[string]any)["available"] != true {
		t.Fatal("Disconnect concealed the section.")
	}
}
