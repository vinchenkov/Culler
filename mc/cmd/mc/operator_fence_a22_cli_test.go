package main_test

import (
	"strings"
	"testing"
)

// The delegated helper carries the operator fence across the process
// boundary through the __operator-forward handoff. These cases pin both ends
// of that claim: a live receipt lets the forwarded write run and record
// itself, and a receipt that lost its active state between approval and
// submission is refused before any effect.

func a22Request(t *testing.T, spine, state string) string {
	t.Helper()
	started := runMC(t, spineEnv(spine), "", "collie", "start", "--from", "cli:a22-operator-fence", "--allow", "project.pause")
	if started.code != 0 {
		t.Fatalf("collie start: %s", started.stderr)
	}
	env := collieJSONEnv(t, spine, started.json["session_id"].(string), []string{"project.pause"})
	submitted := runMC(t, env, operatorTransportBody(t, "a22-fence", []string{"project", "pause", "ws-test"}, ""), "__operator-submit")
	if submitted.code != 0 {
		t.Fatalf("submit: %d %s %s", submitted.code, submitted.stdout, submitted.stderr)
	}
	db := openDB(t, spine)
	if _, err := db.Exec(`UPDATE operator_requests SET state=? WHERE id='a22-fence'`, state); err != nil {
		t.Fatal(err)
	}
	return "a22-fence"
}

func TestOperatorForwardCarriesTheFenceForALiveReceipt(t *testing.T) {
	spine := initSpine(t)
	request := a22Request(t, spine, "active")
	forwarded := runMC(t, spineEnv(spine), "", "__operator-forward", request, "--", "project", "pause", "ws-test")
	if forwarded.code != 0 {
		t.Fatalf("the handoff lost a live operator write: %d %s %s", forwarded.code, forwarded.stdout, forwarded.stderr)
	}
	db := openDB(t, spine)
	if got := queryStr(t, db, `SELECT status FROM projects WHERE id='ws-test'`); got != "paused" {
		t.Fatalf("the forwarded write did not land: %s", got)
	}
	var recorded int
	if err := db.QueryRow(`SELECT COUNT(*) FROM activity WHERE kind='operator.request' AND subject=?`, request).Scan(&recorded); err != nil || recorded < 1 {
		t.Fatalf("the fence left no audit row: %d %v", recorded, err)
	}
}

func TestOperatorForwardRefusesAReceiptWithoutActiveExecution(t *testing.T) {
	spine := initSpine(t)
	request := a22Request(t, spine, "uncertain")
	forwarded := runMC(t, spineEnv(spine), "", "__operator-forward", request, "--", "project", "pause", "ws-test")
	if forwarded.code != 1 || !strings.Contains(forwarded.stderr, "no active execution") {
		t.Fatalf("a lost receipt still executed its write: %d %s %s", forwarded.code, forwarded.stdout, forwarded.stderr)
	}
	db := openDB(t, spine)
	if got := queryStr(t, db, `SELECT status FROM projects WHERE id='ws-test'`); got != "active" {
		t.Fatalf("the refused forward changed the Project: %s", got)
	}
}

func TestOperatorForwardRefusesAnUnknownReceipt(t *testing.T) {
	spine := initSpine(t)
	forwarded := runMC(t, spineEnv(spine), "", "__operator-forward", "a22-never-submitted", "--", "project", "pause", "ws-test")
	if forwarded.code == 0 {
		t.Fatalf("an invented receipt executed a write: %s", forwarded.stdout)
	}
	db := openDB(t, spine)
	if got := queryStr(t, db, `SELECT status FROM projects WHERE id='ws-test'`); got != "active" {
		t.Fatalf("the refused receipt changed the Project: %s", got)
	}
}
