package verbs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"testing"
)

// The collie.preflight_health starvation marker — the write half.
//
// The marker is NOT a refusal consequence. It is selection bookkeeping. The
// verb appends it when a Collie candidate's preflight hits a deployment-health
// refusal. A later pipeline candidate then wins unconditionally over a new
// attestation of the same broken Collie state ("defer_pipeline"), while a
// *changed* Collie state — new input, a repaired launch, cleared debt — is
// immediately eligible again. The whole defer/consume mechanism therefore
// hangs off one property: the candidate key is a pure function of exactly the
// pre-prepare state, and of nothing else. Like applyRefusal before dispatch
// produced refusals, nothing selects Collie candidates yet: these tests are the
// only caller.

func hpKey(t *testing.T, db *sql.DB, sessionID string) string {
	t.Helper()
	var key string
	err := inTx(db, func(ctx context.Context, q Q) error {
		var e error
		key, e = collieCandidateKey(ctx, q, sessionID)
		return e
	})
	if err != nil {
		t.Fatalf("collieCandidateKey(%q): %v", sessionID, err)
	}
	return key
}

func hpRecord(t *testing.T, db *sql.DB, sessionID string) (string, error) {
	t.Helper()
	var key string
	err := inTx(db, func(ctx context.Context, q Q) error {
		var e error
		key, e = recordColliePreflightHealth(ctx, q, sessionID)
		return e
	})
	return key, err
}

// The marker's closed detail is exactly {candidate_key, defer_pipeline} —
// derived state and a fixed flag, so it is leak-proof by construction: no
// producer text, path, env value, or credential has a field to land in. The
// health code stays on the health action, which remains the one place
// that says WHAT broke; the marker only says "defer the pipeline past me".
func TestColliePreflightMarkerAppendsClosedDetail(t *testing.T) {
	db := rrSpine(t)
	key, err := hpRecord(t, db, "sess-1")
	if err != nil {
		t.Fatalf("recordColliePreflightHealth: %v", err)
	}
	if !validLowercaseHex(key, 64) {
		t.Fatalf("candidate key %q is not 64 lowercase hex", key)
	}
	// The write half must store the SAME key the read half recomputes —
	// the whole defer/consume comparison is that equality. Without this
	// cross-check a drifted serialization in either half grades green while
	// no stored marker could ever match a recomputed key.
	if recomputed := hpKey(t, db, "sess-1"); recomputed != key {
		t.Errorf("write half stored %q, read half recomputes %q — no marker could ever match", key, recomputed)
	}
	details := rrActivity(t, db, "collie.preflight_health")
	if len(details) != 1 {
		t.Fatalf("wrote %d collie.preflight_health rows, want one", len(details))
	}
	if want := `{"candidate_key":"` + key + `","defer_pipeline":true}`; details[0] != want {
		t.Errorf("detail = %s, want %s", details[0], want)
	}
	var subject string
	if err := db.QueryRow(`SELECT subject FROM activity WHERE kind = 'collie.preflight_health'`).
		Scan(&subject); err != nil {
		t.Fatalf("read marker subject: %v", err)
	}
	if subject != "sess-1" {
		t.Errorf("marker subject = %q, want the session id", subject)
	}

	// The Collie can retry every tick with no pipeline candidate, so a second
	// marker for the same unchanged state is by design — same key, its own
	// row, consumed later by activity order.
	again, err := hpRecord(t, db, "sess-1")
	if err != nil {
		t.Fatalf("second marker: %v", err)
	}
	if again != key {
		t.Errorf("unchanged state changed the key: %q -> %q", key, again)
	}
	if n := len(rrActivity(t, db, "collie.preflight_health")); n != 2 {
		t.Errorf("wrote %d marker rows after a retry, want two", n)
	}
}

// The key is a pure function of the pre-prepare state: canonical session id,
// current launch/resume debt (every typed field of it), frozen binding, and
// the pending-input sequence. Same state, same key — including after a
// round trip away and back. Different state, different key.
func TestColliePreflightCandidateKeyTracksExactlyThePrePrepareState(t *testing.T) {
	db := rrSpine(t)
	launchA := strings.Repeat("a", 16)
	launchB := strings.Repeat("b", 16)

	baseline := hpKey(t, db, "sess-1")
	if got := hpKey(t, db, "sess-1"); got != baseline {
		t.Fatalf("key is not deterministic: %q vs %q", got, baseline)
	}

	seen := map[string]string{"baseline": baseline}
	step := func(name, update string, args ...any) string {
		t.Helper()
		dvExec(t, db, update, args...)
		key := hpKey(t, db, "sess-1")
		for prev, k := range seen {
			if k == key {
				t.Errorf("state %q collides with earlier state %q", name, prev)
			}
		}
		seen[name] = key
		return key
	}

	// Every launch/debt field participates.
	step("fresh_launch", `UPDATE collie_sessions
		SET current_launch_id = ?, current_launch_mode = 'fresh' WHERE id = 'sess-1'`, launchA)
	step("new_generation", `UPDATE collie_sessions
		SET current_launch_id = ? WHERE id = 'sess-1'`, launchB)
	step("rows_launch", `UPDATE collie_sessions
		SET current_launch_mode = 'rows', current_prime_through_seq = 7,
		    current_prime_row_count = 3 WHERE id = 'sess-1'`)
	step("rows_launch_other_cutoff", `UPDATE collie_sessions
		SET current_prime_through_seq = 8 WHERE id = 'sess-1'`)
	step("resume_debt_native", `UPDATE collie_sessions
		SET current_launch_id = NULL, current_launch_mode = NULL,
		    current_prime_through_seq = NULL, current_prime_row_count = NULL,
		    resume_owed = 1, resume_mode = 'native' WHERE id = 'sess-1'`)
	step("resume_debt_rows", `UPDATE collie_sessions
		SET resume_mode = 'rows', resume_prime_through_seq = 2,
		    resume_prime_row_count = 9 WHERE id = 'sess-1'`)

	// A pure state function returns with the state: clearing the debt is
	// indistinguishable from never having owed it.
	dvExec(t, db, `UPDATE collie_sessions
		SET resume_owed = 0, resume_mode = NULL,
		    resume_prime_through_seq = NULL, resume_prime_row_count = NULL
		WHERE id = 'sess-1'`)
	if got := hpKey(t, db, "sess-1"); got != baseline {
		t.Errorf("restored state must restore the key: %q vs baseline %q", got, baseline)
	}

	// New input is immediately eligible: the pending-inbound sequence
	// is covered...
	dvExec(t, db, `INSERT INTO conversation_messages (session_id, seq, direction, surface, body)
		VALUES ('sess-1', 1, 'inbound', 'cli', 'hi')`)
	withInput := hpKey(t, db, "sess-1")
	if withInput == baseline {
		t.Error("a new pending inbound turn must change the key")
	}
	// ...but a reply row is not "relevant conversation sequence": only
	// pending input feeds the key.
	dvExec(t, db, `INSERT INTO conversation_messages (session_id, seq, direction, surface, body, reply_to)
		VALUES ('sess-1', 2, 'reply', 'collie', 'yo', 1)`)
	if got := hpKey(t, db, "sess-1"); got != withInput {
		t.Errorf("a reply row changed the key: %q -> %q", withInput, got)
	}
	// A claimed-but-incomplete turn is still pending input (branch 7 keeps
	// the Collie eligible after a crash mid-claim): the key must hold through
	// the claim and change only at completion.
	dvExec(t, db, `UPDATE conversation_messages
		SET claimed_by = 'r1', claimed_at = datetime('now') WHERE seq = 1`)
	if got := hpKey(t, db, "sess-1"); got != withInput {
		t.Errorf("a claim must not consume the input: key %q, want %q held", got, withInput)
	}
	// Completing the turn removes the pending input and returns the key to
	// baseline: consumed input is gone, not remembered.
	dvExec(t, db, `UPDATE conversation_messages
		SET completed_at = datetime('now') WHERE seq = 1`)
	if got := hpKey(t, db, "sess-1"); got != baseline {
		t.Errorf("completing the only pending turn must restore the baseline key: %q vs %q", got, baseline)
	}
}

// The serialization under the hash is a frozen cross-harness contract: a
// drifted byte orphans every unconsumed marker on a real deployment (the
// pipeline-defer is silently lost once per repair). This golden vector IS
// the contract — a failure here means the wire format changed, and that is a
// design decision, not a test to update casually.
func TestColliePreflightCandidateKeyGoldenVector(t *testing.T) {
	db := rrSpine(t)
	launch := strings.Repeat("a", 16)
	dvExec(t, db, `UPDATE collie_sessions
		SET current_launch_id = ?, current_launch_mode = 'rows',
		    current_prime_through_seq = 7, current_prime_row_count = 3
		WHERE id = 'sess-1'`, launch)
	dvExec(t, db, `INSERT INTO conversation_messages (session_id, seq, direction, surface, body)
		VALUES ('sess-1', 4, 'inbound', 'cli', 'hi')`)

	canonical := `{"session_id":"sess-1",` +
		`"launch_id":"` + launch + `","launch_mode":"rows",` +
		`"prime_through_seq":7,"prime_row_count":3,` +
		`"resume_owed":0,"resume_mode":null,` +
		`"resume_prime_through_seq":null,"resume_prime_row_count":null,` +
		`"binding":"claude","input_seq":4}`
	sum := sha256.Sum256([]byte(canonical))
	want := hex.EncodeToString(sum[:])
	if got := hpKey(t, db, "sess-1"); got != want {
		t.Errorf("candidate key = %q, want SHA256 of the frozen canonical form %s", got, canonical)
	}
	// Both halves are held to the vector: a marker recorded for this state
	// must carry these exact bytes, not merely agree with itself.
	recorded, err := hpRecord(t, db, "sess-1")
	if err != nil {
		t.Fatalf("recordColliePreflightHealth: %v", err)
	}
	if recorded != want {
		t.Errorf("recorded marker key = %q, want the frozen vector %q", recorded, want)
	}
}

// Fail closed: no session, no marker; an inactive session is not a candidate
// and gets no marker either. Guessing a key for a row that cannot be selected
// would defer the pipeline for nothing.
func TestColliePreflightRefusesAbsentOrInactiveSessions(t *testing.T) {
	t.Run("unknown_session", func(t *testing.T) {
		db := rrSpine(t)
		if _, err := hpRecord(t, db, "nope"); err == nil {
			t.Fatal("unknown session accepted")
		}
		if n := len(rrActivity(t, db, "collie.preflight_health")); n != 0 {
			t.Errorf("wrote %d marker rows for an unknown session", n)
		}
	})
	for _, status := range []string{"ended", "reaped"} {
		t.Run(status, func(t *testing.T) {
			db := rrSpine(t)
			dvExec(t, db, `UPDATE collie_sessions SET status = ? WHERE id = 'sess-1'`, status)
			if _, err := hpRecord(t, db, "sess-1"); err == nil {
				t.Fatalf("%s session accepted", status)
			}
			if n := len(rrActivity(t, db, "collie.preflight_health")); n != 0 {
				t.Errorf("wrote %d marker rows for a %s session", n, status)
			}
		})
	}
}
