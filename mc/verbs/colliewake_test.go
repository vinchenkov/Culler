package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// hwGet runs a single-row query and scans it, failing the test on error.
func hwGet(t *testing.T, db *sql.DB, query string, dest ...any) {
	t.Helper()
	if err := db.QueryRow(query).Scan(dest...); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
}

// hwSession seeds one active Collie session with a distinct container and the
// shipped Collie route frozen as its historical binding (the exact
// "<harness>/<id>" form CollieStart records).
func hwSession(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	hwSessionBound(t, db, id, dvCollieBinding(t))
}

func hwSessionBound(t *testing.T, db *sql.DB, id, binding string) {
	t.Helper()
	dvExec(t, db, `
		INSERT INTO collie_sessions (id, container_name, verb_allowlist, session_path, binding)
		VALUES (?, ?, '[]', ?, ?)`, id, "mc-collie-"+id, "sessions/"+id, binding)
}

// hwPendingInbound appends an unclaimed inbound turn (conversation_pending).
func hwPendingInbound(t *testing.T, db *sql.DB, id string, seq int) {
	t.Helper()
	dvExec(t, db, `
		INSERT INTO conversation_messages (session_id, seq, direction, surface, body)
		VALUES (?, ?, 'inbound', 'dashboard', 'hi')`, id, seq)
}

func hwWakeRound(t *testing.T, db *sql.DB, idleTimeoutS int) (map[string]any, bool) {
	t.Helper()
	return hwWakeRoundWith(t, db, idleTimeoutS, dvCapability(t))
}

func hwWakeRoundInventory(t *testing.T, db *sql.DB, idleTimeoutS int, inventory []PrivateCollieContainer) (map[string]any, bool) {
	t.Helper()
	return hwWakeRoundWith(t, db, idleTimeoutS, dvCapability(t), inventory)
}

func hwWakeRoundWith(t *testing.T, db *sql.DB, idleTimeoutS int, capability PrivateRuntimeCapability, inventory ...[]PrivateCollieContainer) (map[string]any, bool) {
	t.Helper()
	var snapshot []PrivateCollieContainer
	if len(inventory) != 0 {
		snapshot = inventory[0]
	}
	var (
		effect  map[string]any
		handled bool
	)
	err := inTx(db, func(ctx context.Context, q Q) error {
		now, err := spineNow(ctx, q)
		if err != nil {
			return err
		}
		effect, handled, err = collieWakeRoundWithConfirmedAbsence(
			ctx, q, now, idleTimeoutS, snapshot, capability)
		return err
	})
	if err != nil {
		t.Fatalf("collieWakeRound: %v", err)
	}
	return effect, handled
}

func TestLoadCollieSchedRows(t *testing.T) {
	db := dvSpine(t)
	// Insert out of activity order to prove the loader sorts.
	hwSession(t, db, "h-newer")
	hwSession(t, db, "h-older")
	dvExec(t, db, `UPDATE collie_sessions SET last_activity_at = '2000-01-01 00:00:00' WHERE id = 'h-older'`)
	// A launched, resume-clean session — HasLaunch must be observed.
	hwSession(t, db, "h-launched")
	dvExec(t, db, `UPDATE collie_sessions
		SET current_launch_id = 'ffffffffffffffff', current_launch_mode = 'fresh'
		WHERE id = 'h-launched'`)
	// An ended session must not appear.
	hwSession(t, db, "h-gone")
	dvExec(t, db, `UPDATE collie_sessions SET status = 'ended' WHERE id = 'h-gone'`)

	var rows []collieSchedRow
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var e error
		rows, e = loadCollieSchedRows(ctx, q, dvCapability(t))
		return e
	}); err != nil {
		t.Fatalf("loadCollieSchedRows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 active rows, got %d: %+v", len(rows), rows)
	}
	if rows[0].SessionID != "h-older" {
		t.Fatalf("rows not ordered by (last_activity_at, id): %+v", rows)
	}
	byID := map[string]collieSchedRow{}
	for _, r := range rows {
		byID[r.SessionID] = r
	}
	if byID["h-older"].HasLaunch || byID["h-newer"].HasLaunch {
		t.Fatalf("unlaunched sessions should observe no launch: %+v", rows)
	}
	if !byID["h-launched"].HasLaunch || byID["h-launched"].LaunchMode != "fresh" {
		t.Fatalf("h-launched should observe its launch: %+v", byID["h-launched"])
	}
}

func TestCollieWakeRoundFreshSpawn(t *testing.T) {
	db := dvSpine(t)
	hwSession(t, db, "h-1")
	hwPendingInbound(t, db, "h-1", 1)

	effect, handled := hwWakeRound(t, db, 1800)
	if !handled {
		t.Fatalf("a pending inbound turn should produce a wake")
	}
	if effect["action"] != "collie-wake" || effect["session"] != "h-1" || effect["mode"] != "fresh" {
		t.Fatalf("unexpected wake effect: %+v", effect)
	}
	if effect["binding"] != dvCollieBinding(t) || effect["container_name"] != "mc-collie-h-1" {
		t.Fatalf("wake effect missing binding/container: %+v", effect)
	}
	if directive := strings.TrimSpace(collieDirective); directive == "" || effect["directive"] != directive {
		t.Fatal("the wake effect must carry the Collie directive")
	}
	launch, _ := effect["launch"].(string)
	if !validLowercaseHex(launch, 16) {
		t.Fatalf("wake effect launch is not 16-hex: %q", launch)
	}

	// The launch generation is persisted onto the session and resume debt clear.
	var gotLaunch, gotMode sql.NullString
	var resumeOwed int
	hwGet(t, db, `SELECT current_launch_id, current_launch_mode, resume_owed
		FROM collie_sessions WHERE id = 'h-1'`, &gotLaunch, &gotMode, &resumeOwed)
	if gotLaunch.String != launch || gotMode.String != "fresh" || resumeOwed != 0 {
		t.Fatalf("launch not persisted / resume not clear: %q %q owed=%d", gotLaunch.String, gotMode.String, resumeOwed)
	}
	var n int
	hwGet(t, db, `SELECT count(*) FROM activity WHERE kind = 'collie.launched' AND subject = 'h-1'`, &n)
	if n != 1 {
		t.Fatalf("want one collie.launched activity, got %d", n)
	}
}

func TestCollieWakeRoundRowsResume(t *testing.T) {
	db := dvSpine(t)
	hwSession(t, db, "h-1")
	// Resume debt in rows mode with a prime cutoff — no pending inbound.
	dvExec(t, db, `UPDATE collie_sessions
		SET resume_owed = 1, resume_mode = 'rows',
		    resume_prime_through_seq = 9, resume_prime_row_count = 4
		WHERE id = 'h-1'`)

	effect, handled := hwWakeRound(t, db, 1800)
	if !handled || effect["mode"] != "rows" {
		t.Fatalf("resume debt should wake in rows mode: handled=%v %+v", handled, effect)
	}
	// The resume prime cutoff is carried into the current prime pair and the
	// resume debt is cleared.
	var curSeq, curCount, resSeq sql.NullInt64
	var resumeOwed int
	hwGet(t, db, `SELECT current_prime_through_seq, current_prime_row_count,
		resume_prime_through_seq, resume_owed FROM collie_sessions WHERE id = 'h-1'`, &curSeq, &curCount, &resSeq, &resumeOwed)
	if curSeq.Int64 != 9 || curCount.Int64 != 4 {
		t.Fatalf("prime cutoff not carried forward: seq=%v count=%v", curSeq, curCount)
	}
	if resSeq.Valid || resumeOwed != 0 {
		t.Fatalf("resume debt not cleared: resSeq=%v owed=%d", resSeq, resumeOwed)
	}
}

func TestCollieWakeRoundIdleEnd(t *testing.T) {
	db := dvSpine(t)
	hwSession(t, db, "h-1")
	dvExec(t, db, `UPDATE collie_sessions SET last_activity_at = '2000-01-01 00:00:00' WHERE id = 'h-1'`)
	dvExec(t, db, `INSERT INTO collie_bindings (session_id, surface, channel_ref)
		VALUES ('h-1', 'dashboard', 'chan-1')`)

	effect, handled := hwWakeRound(t, db, 1800)
	if !handled {
		t.Fatalf("an idled session should produce an end")
	}
	if effect["action"] != "collie-stop" || effect["session"] != "h-1" || effect["reason"] != "idle" {
		t.Fatalf("unexpected stop effect: %+v", effect)
	}
	var status string
	hwGet(t, db, `SELECT status FROM collie_sessions WHERE id = 'h-1'`, &status)
	if status != "ended" {
		t.Fatalf("idled session not ended: %q", status)
	}
	var active int
	hwGet(t, db, `SELECT count(*) FROM collie_bindings WHERE session_id = 'h-1' AND active = 1`, &active)
	if active != 0 {
		t.Fatalf("bindings not deactivated on end: %d still active", active)
	}
	var n int
	hwGet(t, db, `SELECT count(*) FROM activity WHERE kind = 'collie.ended' AND subject = 'h-1'`, &n)
	if n != 1 {
		t.Fatalf("want one collie.ended activity, got %d", n)
	}
}

// A session gets its container before the first message: an active, bound
// session with no launch and no message is woken fresh.
func TestCollieWakeRoundWakesAQuietSession(t *testing.T) {
	db := dvSpine(t)
	hwSession(t, db, "h-1")
	effect, handled := hwWakeRound(t, db, 1800)
	if !handled {
		t.Fatalf("a quiet bound session with no container must be woken")
	}
	if effect["action"] != "collie-wake" || effect["session"] != "h-1" || effect["mode"] != "fresh" {
		t.Fatalf("unexpected wake effect: %+v", effect)
	}
	var gotMode sql.NullString
	hwGet(t, db, `SELECT current_launch_mode FROM collie_sessions WHERE id = 'h-1'`, &gotMode)
	if gotMode.String != "fresh" {
		t.Fatalf("launch not persisted: mode=%q", gotMode.String)
	}
}

func TestCollieWakeRoundNothingToDo(t *testing.T) {
	db := dvSpine(t)
	// An active session with a live container and no message is not eligible.
	hwSession(t, db, "h-1")
	dvExec(t, db, `UPDATE collie_sessions
		SET current_launch_id = 'ffffffffffffffff', current_launch_mode = 'fresh',
		    current_container_id = ?, launch_bound_at = datetime('now'),
		    launch_started_at = datetime('now')
		WHERE id = 'h-1'`, hbContainer)
	live := []PrivateCollieContainer{{
		Session: "h-1", Launch: "ffffffffffffffff",
		ContainerID: hbContainer, Name: "mc-collie-h-1", State: "running",
		RuntimeAttestation: &PrivateCollieRuntimeAttestation{
			EnvelopeDigest: strings.Repeat("e", 64), RuntimeGeneration: strings.Repeat("f", 64),
		},
	}}
	if effect, handled := hwWakeRoundInventory(t, db, 1800, live); handled {
		t.Fatalf("a quiet session with a live container must not be woken: %+v", effect)
	}
}

// TestDispatchCollieStageWakesCollie proves the full prepare path of the Collie
// stage: the lease-free round with project ""
// sees no pipeline candidate — even on a fresh spine that would liveness-retain
// a strategist propose in its own Project's round — and finalizes an
// eligible Collie wake as its final effect, the tick's first action. The
// resident e2e depends on this round.
func TestDispatchCollieStageWakesCollie(t *testing.T) {
	db := dvSpine(t)
	hwSession(t, db, "h-1")
	hwPendingInbound(t, db, "h-1", 1)

	var uuid string
	if err := db.QueryRow(`SELECT deployment_uuid FROM meta WHERE id = 1`).Scan(&uuid); err != nil {
		t.Fatalf("read deployment uuid: %v", err)
	}
	var prepared preparedDispatch
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var e error
		prepared, e = dispatchPrepareWithIdentity(ctx, q, defaultDispatchProtocolIdentity, uuid, "00112233445566ff", dvCapability(t), "")
		return e
	}); err != nil {
		t.Fatalf("dispatchPrepareWithIdentity: %v", err)
	}
	if prepared.final == nil {
		t.Fatalf("no final effect; prepared = %+v (expected a collie-wake)", prepared)
	}
	if prepared.final["action"] != "collie-wake" || prepared.final["session"] != "h-1" {
		t.Fatalf("final effect = %+v, want a collie-wake of h-1", prepared.final)
	}
}

// TestCollieResumeSchedulesNativeWake proves the resume decision rather than
// mere record reactivation: resume creates native launch debt immediately,
// native wins a concurrently pending inbound turn, and the committed effect
// carries the exact frozen locators the resident must use.
func TestCollieResumeSchedulesNativeWake(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "without pending input", true: "with pending input"}[pending], func(t *testing.T) {
			db := dvSpine(t)
			hwSession(t, db, "h-1")
			// A fully bound + started + native-registered generation, then ended.
			dvExec(t, db, `UPDATE collie_sessions
				SET current_launch_id = 'ffffffffffffffff', current_launch_mode = 'fresh',
				    current_container_id = ?, launch_bound_at = datetime('now'),
				    launch_started_at = datetime('now'),
				    native_session_ref = 'native-xyz', trace_filename = 'trace/native.jsonl'
				WHERE id = 'h-1'`, hbContainer)
			dvExec(t, db, `UPDATE collie_sessions SET status = 'ended' WHERE id = 'h-1'`)

			if _, err := CollieResume(db, nil, "h-1", "dashboard:chan-1"); err != nil {
				t.Fatalf("CollieResume: %v", err)
			}
			var status string
			var launch, resumeMode sql.NullString
			var resumeOwed int
			hwGet(t, db, `SELECT status, current_launch_id, resume_owed, resume_mode
				FROM collie_sessions WHERE id = 'h-1'`, &status, &launch, &resumeOwed, &resumeMode)
			if status != "active" || launch.Valid || resumeOwed != 1 || resumeMode.String != "native" {
				t.Fatalf("after resume: status=%q launch=%v debt=%d/%q; want active, no launch, native debt",
					status, launch, resumeOwed, resumeMode.String)
			}

			if pending {
				hwPendingInbound(t, db, "h-1", 2)
			}
			effect, handled := hwWakeRound(t, db, 1800)
			if !handled || effect["action"] != "collie-wake" || effect["session"] != "h-1" ||
				effect["mode"] != "native" {
				t.Fatalf("resume did not schedule a native wake: handled=%v %+v", handled, effect)
			}
			if effect["session_path"] != "sessions/h-1" ||
				effect["binding"] != dvCollieBinding(t) ||
				effect["native_session_ref"] != "native-xyz" ||
				effect["trace_filename"] != "trace/native.jsonl" {
				t.Fatalf("native wake omitted or changed frozen locators: %+v", effect)
			}
		})
	}
}

func TestCollieWakeRoundAlreadyLaunchedIsSkipped(t *testing.T) {
	db := dvSpine(t)
	hwSession(t, db, "h-1")
	hwPendingInbound(t, db, "h-1", 1)
	dvExec(t, db, `UPDATE collie_sessions
		SET current_launch_id = 'ffffffffffffffff', current_launch_mode = 'fresh'
		WHERE id = 'h-1'`)
	if _, handled := hwWakeRound(t, db, 1800); handled {
		t.Fatalf("an already-launched session is deferred recovery, not a happy-path wake")
	}
}

// TestCollieWakeCarriesTheResolvedRuntimeCapability proves the crossing a wake
// owes the resident: the verb resolves the frozen historical binding against
// the CURRENT enabled config catalog, and the exact resolved capability travels
// with the effect, so the resident owns no route catalog of its own.
func TestCollieWakeCarriesTheResolvedRuntimeCapability(t *testing.T) {
	db := dvSpine(t)
	hwSession(t, db, "h-1")
	hwPendingInbound(t, db, "h-1", 1)

	capability := dvCapability(t)
	effect, handled := hwWakeRoundWith(t, db, 1800, capability)
	if !handled {
		t.Fatal("an enabled frozen binding must still wake")
	}
	runtime, ok := effect["runtime_binding"].(map[string]any)
	if !ok {
		t.Fatalf("wake effect carries no resolved runtime capability: %+v", effect)
	}
	resolvedSpec, resolved := resolveFrozenCollieBinding(capability, dvCollieBinding(t))
	if !resolved {
		t.Fatalf("fixture binding %q does not resolve", dvCollieBinding(t))
	}
	want, err := runtimeBindingEffectValue(resolvedSpec)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(runtime, want) {
		t.Fatalf("wake runtime = %+v, want the resolved catalog entry %+v", runtime, want)
	}
	if runtime["harness"].(string)+"/"+runtime["id"].(string) != effect["binding"] {
		t.Fatalf("carried runtime %+v disagrees with the frozen binding %v", runtime, effect["binding"])
	}
	// The non-secret projection is the whole point: an operator-configured model
	// and endpoint reach the launch, together with the credential source id
	// the resident resolves the grant from, and no credential value ever does.
	if runtime["model"] == "" || runtime["auth_kind"] == "" || runtime["credential"] == "" {
		t.Fatalf("carried runtime lost its configured shape: %+v", runtime)
	}
	// A stored receipt replays the effect through JSON; the live and replayed
	// renderings must be the same bytes. Normalize every new carrier.
	live, err := json.Marshal(effect)
	if err != nil {
		t.Fatal(err)
	}
	var round map[string]any
	if err := json.Unmarshal(live, &round); err != nil {
		t.Fatal(err)
	}
	replayed, err := json.Marshal(round)
	if err != nil {
		t.Fatal(err)
	}
	if string(live) != string(replayed) {
		t.Fatalf("wake effect does not survive its own receipt round trip:\n live: %s\n back: %s", live, replayed)
	}
}

// TestCollieWakeRefusesWithoutConfiguredCapability is the fail-closed half: a
// binding the operator disabled (or a config the host could not read at all)
// wakes nothing and — the load-bearing part — mutates nothing. No launch
// generation is minted, no resume debt is consumed, and no activity is written,
// so the session is exactly as recoverable after the tick as before it.
func TestCollieWakeRefusesWithoutConfiguredCapability(t *testing.T) {
	shipped := dvCapability(t)
	disabled := PrivateRuntimeCapability{Digest: shipped.Digest}
	for _, spec := range shipped.Bindings {
		if spec.ID != "claude" {
			disabled.Bindings = append(disabled.Bindings, spec)
		}
	}
	if disabled.Bindings == nil {
		disabled.Bindings = []PrivateRuntimeBinding{}
	}

	for name, capability := range map[string]PrivateRuntimeCapability{
		"disabled binding":         disabled,
		"unreadable config":        UnavailableRuntimeCapability(),
		"binding removed outright": {Digest: shipped.Digest, Bindings: []PrivateRuntimeBinding{}},
	} {
		t.Run(name, func(t *testing.T) {
			db := dvSpine(t)
			hwSession(t, db, "h-1")
			hwPendingInbound(t, db, "h-1", 1)
			dvExec(t, db, `UPDATE collie_sessions
				SET resume_owed = 1, resume_mode = 'rows',
				    resume_prime_through_seq = 9, resume_prime_row_count = 4
				WHERE id = 'h-1'`)

			if effect, handled := hwWakeRoundWith(t, db, 1800, capability); handled {
				t.Fatalf("an unconfigured capability produced a wake: %+v", effect)
			}
			var launch sql.NullString
			var resumeOwed int
			var resumeSeq sql.NullInt64
			hwGet(t, db, `SELECT current_launch_id, resume_owed, resume_prime_through_seq
				FROM collie_sessions WHERE id = 'h-1'`, &launch, &resumeOwed, &resumeSeq)
			if launch.Valid || resumeOwed != 1 || resumeSeq.Int64 != 9 {
				t.Fatalf("a refused wake mutated launch state: launch=%v owed=%d seq=%v",
					launch, resumeOwed, resumeSeq)
			}
			var n int
			hwGet(t, db, `SELECT count(*) FROM activity WHERE kind = 'collie.launched' AND subject = 'h-1'`, &n)
			if n != 0 {
				t.Fatalf("a refused wake wrote %d launch records", n)
			}
		})
	}
}

// TestCollieWakeStrandedSessionStillEndsOnIdle proves the stranded session is not
// immortal: branch 6 resolves no runtime, so a session whose binding config no
// longer describes ends on its idle timeout like any other quiet session.
func TestCollieWakeStrandedSessionStillEndsOnIdle(t *testing.T) {
	db := dvSpine(t)
	hwSessionBound(t, db, "h-1", "claude-sdk/retired-binding")
	hwPendingInbound(t, db, "h-1", 1)
	dvExec(t, db, `UPDATE collie_sessions SET last_activity_at = '2000-01-01 00:00:00' WHERE id = 'h-1'`)

	effect, handled := hwWakeRound(t, db, 1800)
	if !handled || effect["action"] != "collie-stop" || effect["session"] != "h-1" {
		t.Fatalf("a stranded idle session was not ended: handled=%v %+v", handled, effect)
	}
}

// TestCollieWakeRefusesAForeignHarnessHalf pins that both halves of the frozen
// binding are authority: an id that exists under a different harness family is
// not the capability this session froze.
func TestCollieWakeRefusesAForeignHarnessHalf(t *testing.T) {
	db := dvSpine(t)
	hwSessionBound(t, db, "h-1", "codex/claude")
	hwPendingInbound(t, db, "h-1", 1)
	if effect, handled := hwWakeRound(t, db, 1800); handled {
		t.Fatalf("a binding resolved under a foreign harness family: %+v", effect)
	}
}

// TestCollieWakeRecoversAnUncontainerizedLaunchPerSession pins the recovery a
// discarded wake depends on.
//
// A minted launch with no container is durable effect debt: the resident may
// have died mid-create, or the host may have abandoned the effect after prepare
// already committed it (a config change during the drift window does exactly
// that). Absence must therefore be judged PER SESSION against the resident's
// snapshot — the rule the started-absent arm already states. Judging it by
// "the whole inventory is empty" made recovery available only on an otherwise
// idle machine, so one unrelated live Collie left the session unwakeable until
// its idle timeout.
func TestCollieWakeRecoversAnUncontainerizedLaunchPerSession(t *testing.T) {
	seed := func(t *testing.T) *sql.DB {
		t.Helper()
		db := dvSpine(t)
		hwSession(t, db, "h-stuck")
		hwPendingInbound(t, db, "h-stuck", 1)
		dvExec(t, db, `UPDATE collie_sessions
			SET current_launch_id = 'ffffffffffffffff', current_launch_mode = 'native',
			    native_session_ref = 'native-xyz', trace_filename = 'trace/native.jsonl'
			WHERE id = 'h-stuck'`)
		return db
	}
	elsewhere := []PrivateCollieContainer{{
		Session: "h-other", Launch: "aaaaaaaaaaaaaaaa",
		ContainerID: hbContainer, Name: "mc-collie-h-other", State: "running",
	}}

	t.Run("another session's live container does not mask this one's absence", func(t *testing.T) {
		db := seed(t)
		effect, handled := hwWakeRoundInventory(t, db, 1800, elsewhere)
		if !handled || effect["action"] != "collie-wake" || effect["session"] != "h-stuck" {
			t.Fatalf("an uncontainerized launch was not recovered: handled=%v %+v", handled, effect)
		}
		// The frozen generation is re-emitted, never re-minted.
		if effect["launch"] != "ffffffffffffffff" || effect["mode"] != "native" {
			t.Fatalf("recovery minted a new generation: %+v", effect)
		}
		if _, ok := effect["runtime_binding"].(map[string]any); !ok {
			t.Fatalf("recovered wake carries no resolved capability: %+v", effect)
		}
	})

	t.Run("this session's own container withholds recovery", func(t *testing.T) {
		db := seed(t)
		own := append([]PrivateCollieContainer{{
			Session: "h-stuck", Launch: "ffffffffffffffff",
			ContainerID: hbContainer, Name: "mc-collie-h-stuck", State: "created",
		}}, elsewhere...)
		if effect, handled := hwWakeRoundInventory(t, db, 1800, own); handled {
			t.Fatalf("a session with a live container was re-launched: %+v", effect)
		}
	})

	t.Run("no snapshot at all confirms nothing", func(t *testing.T) {
		db := seed(t)
		if effect, handled := hwWakeRound(t, db, 1800); handled {
			t.Fatalf("the in-process seam invented absence authority: %+v", effect)
		}
	})
}

// The wake must carry the session's FROZEN allowlist, because that value is the
// Collie's entire write capability: RequireOperatorVerb admits an operator verb
// only by scanning run.json's verb_allowlist, and requireActiveCollieVerb then
// compares that envelope value against this stored one. The wake once omitted
// it, so every live Collie came up able to read and nothing else.
//
// It asserts a NARROWED allowlist rather than the compiled-in maximum on
// purpose: `mc collie start --allow` can freeze a subset, and a wake that shipped
// maximumCollieAgentVerbs instead of the stored value would fail the registry
// comparison exactly as hard as shipping nothing.
func TestCollieWakeCarriesTheSessionsFrozenVerbAllowlist(t *testing.T) {
	db := dvSpine(t)
	// Frozen at INSERT: a substrate trigger forbids an update to the
	// allowlist, which is exactly why the wake must re-read it rather than
	// reconstruct it.
	dvExec(t, db, `
		INSERT INTO collie_sessions (id, container_name, verb_allowlist, session_path, binding)
		VALUES ('h-1', 'mc-collie-h-1', '["task add","packet decide"]', 'sessions/h-1', ?)`,
		dvCollieBinding(t))
	hwPendingInbound(t, db, "h-1", 1)

	effect, handled := hwWakeRound(t, db, 1800)
	if !handled {
		t.Fatalf("a pending inbound turn should produce a wake")
	}
	got, ok := effect["verb_allowlist"].([]string)
	if !ok {
		t.Fatalf("wake effect carries no verb_allowlist (%T): %+v", effect["verb_allowlist"], effect)
	}
	want := []string{"task add", "packet decide"}
	if len(got) != len(want) {
		t.Fatalf("verb_allowlist = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("verb_allowlist = %v, want %v", got, want)
		}
	}
}

// An empty frozen allowlist is a legal, deliberately narrow session, and it must
// travel as an empty list rather than JSON null: absent and empty would be two
// different failures at the two gates, and this repo has paid for absent-vs-empty
// divergence in a shared carrier before.
func TestCollieWakeCarriesAnEmptyAllowlistAsAnEmptyList(t *testing.T) {
	db := dvSpine(t)
	hwSession(t, db, "h-1") // fixture freezes '[]'
	hwPendingInbound(t, db, "h-1", 1)

	effect, handled := hwWakeRound(t, db, 1800)
	if !handled {
		t.Fatalf("expected a wake")
	}
	got, ok := effect["verb_allowlist"].([]string)
	if !ok || got == nil {
		t.Fatalf("empty allowlist did not travel as an empty list: %#v", effect["verb_allowlist"])
	}
	if len(got) != 0 {
		t.Fatalf("verb_allowlist = %v, want empty", got)
	}
}

// TestCollieContainerExitWithAnOwedReplyRecoversInsteadOfEnding covers a live
// defect. A Collie container exits when it goes idle, and branch 5 then ended
// the session on the next tick because inventory no longer showed the
// container. It did that whether or not an inbound turn waited, so an operator
// message that arrived in the gap was stranded forever: the row stays
// uncompleted, `mc collie send` refuses the ended session, and no surface ever
// reports the loss. The opposite outcome is the promise — the next inbound
// message makes the conversation eligible and the next tick wakes a fresh
// instance that continues from the persisted turns.
//
// The consumer this is judged by is the SELECTOR, not the columns: the point of
// recovery is that branch 7 can actually wake the session afterwards, so the
// test drives a second wake round and demands a real wake effect.
func TestCollieContainerExitWithAnOwedReplyRecoversInsteadOfEnding(t *testing.T) {
	for _, tc := range []struct {
		name       string
		native     bool
		wantResume string
	}{
		{name: "native locators present", native: true, wantResume: "native"},
		// `mc collie resume` REFUSES this shape rather than downgrading. Refusing
		// here would strand the turn the arm exists to save, and nothing is lost:
		// the locator pair is registered as soon as a harness session exists, so
		// its absence means there is no continuation to continue. No resume debt,
		// and branch 7 takes its ordinary fresh wake.
		{name: "no native locators", native: false, wantResume: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := dvSpine(t)
			hwSession(t, db, "h-1")
			dvExec(t, db, `UPDATE collie_sessions
				SET current_launch_id = 'ffffffffffffffff', current_launch_mode = 'fresh',
				    current_container_id = ?, launch_bound_at = datetime('now'),
				    launch_started_at = datetime('now')
				WHERE id = 'h-1'`, hbContainer)
			if tc.native {
				dvExec(t, db, `UPDATE collie_sessions
					SET native_session_ref = 'native-xyz', trace_filename = 'native.jsonl'
					WHERE id = 'h-1'`)
			}
			// The operator speaks into the gap after the container is gone.
			hwPendingInbound(t, db, "h-1", 1)

			// An empty inventory is exactly "the container exited".
			effect, handled := hwAbsentRound(t, db, 1800)
			if !handled {
				t.Fatalf("the tick took no action on a confirmed-absent container")
			}
			if effect["reason"] != "exited-owed-reply" {
				t.Fatalf("reason %q; want exited-owed-reply (an unqualified 'exited' means it ended the session)",
					effect["reason"])
			}

			var status, resumeMode sql.NullString
			var launch, container sql.NullString
			var resumeOwed int
			hwGet(t, db, `SELECT status, current_launch_id, current_container_id,
				resume_owed, resume_mode FROM collie_sessions WHERE id = 'h-1'`,
				&status, &launch, &container, &resumeOwed, &resumeMode)
			if status.String != "active" {
				t.Fatalf("session is %q; a turn is owed, so it must stay active", status.String)
			}
			if launch.Valid || container.Valid {
				t.Fatalf("the dead generation survived: launch=%v container=%v", launch, container)
			}
			wantOwed := 0
			if tc.wantResume != "" {
				wantOwed = 1
			}
			if resumeOwed != wantOwed || resumeMode.String != tc.wantResume {
				t.Fatalf("resume debt %d/%q; want %d/%q",
					resumeOwed, resumeMode.String, wantOwed, tc.wantResume)
			}

			// The real consumer: branch 7 must now wake it.
			wake, handled := hwWakeRoundInventory(t, db, 1800, []PrivateCollieContainer{})
			if !handled || wake["action"] != "collie-wake" || wake["session"] != "h-1" {
				t.Fatalf("the recovered session was not woken: handled=%v %+v", handled, wake)
			}
			wantMode := tc.wantResume
			if wantMode == "" {
				wantMode = "fresh"
			}
			if wake["mode"] != wantMode {
				t.Fatalf("woken in mode %v; want %q", wake["mode"], wantMode)
			}
		})
	}
}

// The other half, and the reason the arm is not simply "never end": a session
// with NOTHING owed must still end when its container is gone, or an ordinary
// finished conversation would resurrect itself on every tick forever.
func TestCollieContainerExitWithNothingOwedStillEndsTheSession(t *testing.T) {
	db := dvSpine(t)
	hwSession(t, db, "h-1")
	dvExec(t, db, `UPDATE collie_sessions
		SET current_launch_id = 'ffffffffffffffff', current_launch_mode = 'fresh',
		    current_container_id = ?, launch_bound_at = datetime('now'),
		    launch_started_at = datetime('now'),
		    native_session_ref = 'native-xyz', trace_filename = 'native.jsonl'
		WHERE id = 'h-1'`, hbContainer)

	effect, handled := hwAbsentRound(t, db, 1800)
	if !handled || effect["reason"] != "exited" {
		t.Fatalf("a container exit with nothing owed: handled=%v %+v", handled, effect)
	}
	var status string
	hwGet(t, db, `SELECT status FROM collie_sessions WHERE id = 'h-1'`, &status)
	if status != "ended" {
		t.Fatalf("session is %q; nothing was owed, so it must end", status)
	}
}

// hwAbsentRound drives branch 5's confirmed-absent arm the way the dispatch
// seam does — ahead of the wake round, against an inventory snapshot that is
// present but contains no container for this session. The empty-versus-nil
// distinction is load-bearing: nil means "no snapshot was taken", which is why
// the seam gates the whole branch-5 family on `collieInventory != nil`.
func hwAbsentRound(t *testing.T, db *sql.DB, idleTimeoutS int) (map[string]any, bool) {
	t.Helper()
	var (
		effect  map[string]any
		handled bool
	)
	err := inTx(db, func(ctx context.Context, q Q) error {
		now, err := spineNow(ctx, q)
		if err != nil {
			return err
		}
		effect, handled, err = collieStartedAbsentRound(
			ctx, q, []PrivateCollieContainer{}, now, idleTimeoutS)
		return err
	})
	if err != nil {
		t.Fatalf("collieStartedAbsentRound: %v", err)
	}
	return effect, handled
}

// A live container claims its own inbound turns. The tick leaves the session
// alone: no second wake and no stop.
func TestCollieStageLeavesANewTurnToTheLiveContainer(t *testing.T) {
	db := dvSpine(t)
	hwSession(t, db, "h-1")
	dvExec(t, db, `UPDATE collie_sessions
		SET current_launch_id = 'ffffffffffffffff', current_launch_mode = 'fresh',
		    current_container_id = ?, launch_bound_at = datetime('now'),
		    launch_started_at = datetime('now')
		WHERE id = 'h-1'`, hbContainer)
	hwPendingInbound(t, db, "h-1", 1)
	live := []PrivateCollieContainer{{
		Session: "h-1", Launch: "ffffffffffffffff",
		ContainerID: hbContainer, Name: "mc-collie-h-1", State: "running",
		RuntimeAttestation: &PrivateCollieRuntimeAttestation{
			EnvelopeDigest: strings.Repeat("e", 64), RuntimeGeneration: strings.Repeat("f", 64),
		},
	}}

	if effect, handled := hwWakeRoundInventory(t, db, 1800, live); handled {
		t.Fatalf("the wake round acted on a session with a live container: %+v", effect)
	}

	var uuid string
	hwGet(t, db, `SELECT deployment_uuid FROM meta WHERE id = 1`, &uuid)
	var prepared preparedDispatch
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var e error
		prepared, e = dispatchPrepareWithIdentityInventory(ctx, q, defaultDispatchProtocolIdentity,
			uuid, "00112233445566aa", live, nil, dvCapability(t), "")
		return e
	}); err != nil {
		t.Fatalf("dispatchPrepareWithIdentityInventory: %v", err)
	}
	if prepared.final["action"] != "idle" {
		t.Fatalf("the Collie stage emitted %+v; the live runner owns the turn", prepared.final)
	}
	var status string
	var containerID sql.NullString
	hwGet(t, db, `SELECT status, current_container_id FROM collie_sessions WHERE id = 'h-1'`,
		&status, &containerID)
	if status != "active" || containerID.String != hbContainer {
		t.Fatalf("session changed under a live container: status=%q container=%q", status, containerID.String)
	}
}

// The wake carries the deployment's idle timeout, so the runner can bound its
// own life when the stop never arrives.
func TestCollieWakeCarriesTheIdleTimeout(t *testing.T) {
	db := dvSpine(t)
	hwSession(t, db, "h-1")
	hwPendingInbound(t, db, "h-1", 1)
	effect, handled := hwWakeRound(t, db, 900)
	if !handled || effect["action"] != "collie-wake" {
		t.Fatalf("no wake: handled=%v %+v", handled, effect)
	}
	if effect["idle_timeout_s"] != 900 {
		t.Fatalf("idle_timeout_s = %v, want 900", effect["idle_timeout_s"])
	}
}

// The resident mounts the artifact files of the task of a packet conversation,
// so both wake branches carry the packet identity. Each other wake has no
// packet key.
func TestCollieWakeCarriesThePacket(t *testing.T) {
	db := dvSpine(t)
	packet, version := hpPacket(t, db, 1)
	started := hpMustPrepare(t, db, packet, version, false)
	hwPendingInbound(t, db, started.SessionID, 1)
	want := map[string]any{"packet_id": packet, "version_id": version, "task_id": int64(1), "project_id": "ws-test"}

	effect, handled := hwWakeRound(t, db, 1800)
	if !handled || effect["session"] != started.SessionID || !reflect.DeepEqual(effect["packet"], want) {
		t.Fatalf("the new launch wake is %+v, want the packet %v", effect, want)
	}
	if _, ok := effect["vision_project_id"]; ok {
		t.Fatalf("the packet conversation wake names a Vision Project: %+v", effect)
	}
	launch := effect["launch"]
	recovered, handled := hwWakeRoundInventory(t, db, 1800, []PrivateCollieContainer{})
	if !handled || recovered["launch"] != launch || !reflect.DeepEqual(recovered["packet"], want) {
		t.Fatalf("the existing launch wake is %+v, want launch %v and the packet %v", recovered, launch, want)
	}

	ordinaryDB := dvSpine(t)
	ordinary := startedSession(t, ordinaryDB, hpRef())
	hwPendingInbound(t, ordinaryDB, ordinary, 1)
	effect, handled = hwWakeRound(t, ordinaryDB, 1800)
	if _, ok := effect["packet"]; !handled || ok {
		t.Fatalf("the ordinary conversation wake is %+v, want no packet key", effect)
	}
}

func TestCollieEffortRemainsFrozenAcrossProfileChangesAndRecovery(t *testing.T) {
	db := dvSpine(t)
	req, err := ResolvePrivateCollieStartRequest(CollieStartArgs{From: "dashboard:effort"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range req.RuntimeCapability.Bindings {
		spec := &req.RuntimeCapability.Bindings[i]
		if spec.Harness+"/"+spec.ID == req.Binding {
			spec.Effort = "low"
		}
	}
	started, err := CollieStartPrivateDB(db, req)
	if err != nil {
		t.Fatal(err)
	}
	var effort string
	if err := db.QueryRow(`SELECT effort FROM collie_sessions WHERE id=?`, started.SessionID).Scan(&effort); err != nil || effort != "low" {
		t.Fatalf("The session lost its effort: %q %v", effort, err)
	}
	if _, err := db.Exec(`UPDATE collie_sessions SET effort='high' WHERE id=?`, started.SessionID); err == nil {
		t.Fatal("The conversation effort changed.")
	}
	for i := range req.RuntimeCapability.Bindings {
		spec := &req.RuntimeCapability.Bindings[i]
		if spec.Harness+"/"+spec.ID == req.Binding {
			spec.Effort = "high"
		}
	}
	wake, handled := hwWakeRoundWith(t, db, 1800, req.RuntimeCapability)
	if !handled || wake["effort"] != "low" || wake["runtime_binding"].(map[string]any)["effort"] != "high" {
		t.Fatalf("The wake lost the conversation effort: %+v", wake)
	}
	recovered, handled := hwWakeRoundWith(t, db, 1800, req.RuntimeCapability, []PrivateCollieContainer{})
	if !handled || recovered["effort"] != "low" || recovered["launch"] != wake["launch"] {
		t.Fatalf("The recovered launch lost its effort: %+v", recovered)
	}
	req.From = "dashboard:new-effort"
	next, err := CollieStartPrivateDB(db, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT effort FROM collie_sessions WHERE id=?`, next.SessionID).Scan(&effort); err != nil || effort != "high" {
		t.Fatalf("The new conversation lost the new effort: %q %v", effort, err)
	}
}
