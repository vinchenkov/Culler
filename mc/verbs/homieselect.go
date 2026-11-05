package verbs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// The lease-free Homie wake selector: the spine-only core.
//
// The selector runs after the pipeline Decide() when no pipeline candidate is
// committed. Homie is the lease-free tier. This file holds the two decisions
// that need only spine state:
//   - idle end: a session past its idle timeout ends, launched or not;
//   - wake: an active session with no launch gets a launch. The container
//     exists before the first message, so a wake needs no pending inbound
//     turn and no resume debt. Resume debt only selects the launch mode.
//
// Inventory-backed cleanup and confirmed-absent started-generation ending
// live in homieinventory.go. Two arms need a wider execution-envelope
// attestation than the current identity/state inventory carries, and are
// deferred to a later hardening slice:
//   - created adoption and healthy-running classification, and
//   - adopt-a-created-container recovery of a wake.
// The resident normally invokes the launch-bind/runner-started/exit receipts
// itself the moment it acts, so these branches are the tick's recovery path.

type homieWakeKind string

const (
	homieWakeNone  homieWakeKind = ""
	homieWakeIdle  homieWakeKind = "idle-end"
	homieWakeSpawn homieWakeKind = "wake"
)

// homieSchedRow is one active session's spine-only scheduling state, loaded in
// (last_activity_at, id) order. Runtime is the ONE non-spine input: the frozen
// historical binding resolved against the host-carried enabled capability.
type homieSchedRow struct {
	SessionID             string
	LastActivityAt        time.Time
	HasLaunch             bool   // current_launch_id IS NOT NULL
	LaunchID              string // when HasLaunch
	LaunchMode            string // when HasLaunch
	LaunchAbsentConfirmed bool   // unbound launch plus a resident inventory snapshot
	ResumeOwed            bool
	ResumeMode            string // native|rows, when ResumeOwed
	Binding               string // frozen "<harness>/<id>"
	Runtime               PrivateRuntimeBinding
	RuntimeReady          bool // Binding resolves in the current enabled catalog
}

// homieWakeDecision is the selector's verdict for this tick — at most one
// session, one action.
type homieWakeDecision struct {
	Kind           homieWakeKind
	SessionID      string
	LaunchID       string // for a confirmed-absent existing launch
	LaunchMode     string // for a spawn wake: fresh|native|rows
	ExistingLaunch bool
	Runtime        PrivateRuntimeBinding // the resolved capability a spawn launches on
}

// selectHomieWake picks at most one action from active sessions ordered by
// (last_activity_at, id): idle end wins over spawn, and each picks the oldest
// matching row. Every active session with no launch is spawn-eligible, with
// or without a pending message: the container exists before the first
// message, and the runner polls claim until a message arrives or the idle end
// stops it. See the file header for the deferred recovery branches.
//
// A row whose frozen binding no longer resolves in the enabled catalog is not
// spawn-eligible at all: it is skipped in favour of the next candidate rather
// than blocking the queue, and it is deliberately still eligible for branch 6,
// so a session stranded by a config change ends on its idle timeout instead of
// living forever unlaunchable.
func selectHomieWake(rows []homieSchedRow, now time.Time, idleTimeout time.Duration) homieWakeDecision {
	var idle, spawn *homieSchedRow
	for i := range rows {
		r := &rows[i]
		if !now.Before(r.LastActivityAt.Add(idleTimeout)) {
			// Idle end owns idle rows regardless of launch state.
			if idle == nil {
				idle = r
			}
			continue
		}
		// A confirmed-absent unbound launch remains durable effect debt and is
		// eligible without minting a new generation. Otherwise the happy case
		// is any no-launch row.
		if spawn == nil && r.RuntimeReady && (r.LaunchAbsentConfirmed || !r.HasLaunch) {
			spawn = r
		}
	}
	if idle != nil {
		return homieWakeDecision{Kind: homieWakeIdle, SessionID: idle.SessionID}
	}
	if spawn != nil {
		if spawn.LaunchAbsentConfirmed {
			return homieWakeDecision{
				Kind: homieWakeSpawn, SessionID: spawn.SessionID,
				LaunchID: spawn.LaunchID, LaunchMode: spawn.LaunchMode,
				ExistingLaunch: true, Runtime: spawn.Runtime,
			}
		}
		// Resume debt selects the launch mode: an inbound turn may race the
		// resume-to-tick window, but it must not downgrade native
		// continuation to a fresh harness.
		mode := "fresh"
		if spawn.ResumeOwed {
			mode = spawn.ResumeMode
		}
		return homieWakeDecision{
			Kind: homieWakeSpawn, SessionID: spawn.SessionID,
			LaunchMode: mode, Runtime: spawn.Runtime,
		}
	}
	return homieWakeDecision{Kind: homieWakeNone}
}

// loadHomieSchedRows loads every active Homie session's spine-only scheduling
// state in (last_activity_at, id) order — the projection selectHomieWake
// consumes. The private crossing may additionally mark an unbound launch
// absent from the resident-owned inventory; the in-process seam leaves that
// recovery authority false.
//
// This loader is also the ONE place a frozen historical binding meets the
// host-carried enabled capability: resolution happens per row, once, so the
// selector and the effect can never disagree about what a session may run.
func loadHomieSchedRows(ctx context.Context, q Q, capability PrivateRuntimeCapability) ([]homieSchedRow, error) {
	return loadHomieSchedRowsWithConfirmedAbsence(ctx, q, nil, capability)
}

// loadHomieSchedRowsWithConfirmedAbsence takes the resident-owned Docker
// inventory, or nil for the in-process seam that owns no snapshot.
//
// Absence is evaluated PER SESSION, the same rule homieStartedAbsentRound
// already states for its own arm: a real snapshot that contains no container
// for this session proves this session's unbound launch is absent, whatever
// other sessions are running. The earlier global "the whole inventory is
// empty" flag made recovery available only on an otherwise idle machine, so a
// launch that was minted but never containerized — a resident that died
// mid-create, or a host that abandoned the effect after prepare committed —
// stayed unrecoverable until the session idled out.
func loadHomieSchedRowsWithConfirmedAbsence(
	ctx context.Context,
	q Q,
	inventory []PrivateHomieContainer,
	capability PrivateRuntimeCapability,
) ([]homieSchedRow, error) {
	sessionObserved := make(map[string]bool, len(inventory))
	for _, item := range inventory {
		sessionObserved[item.Session] = true
	}
	rows, err := q.QueryContext(ctx, `
		SELECT s.id, s.last_activity_at, s.current_launch_id, s.current_launch_mode,
		       s.current_container_id,
		       s.resume_owed, s.resume_mode, s.binding
		FROM homie_sessions s
		WHERE s.status = 'active'
		ORDER BY s.last_activity_at, s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []homieSchedRow{}
	for rows.Next() {
		var (
			r                                             homieSchedRow
			lastAct                                       string
			launchID, launchMode, containerID, resumeMode sql.NullString
			resumeOwed                                    int
		)
		if err := rows.Scan(&r.SessionID, &lastAct, &launchID, &launchMode,
			&containerID, &resumeOwed, &resumeMode, &r.Binding); err != nil {
			return nil, err
		}
		r.Runtime, r.RuntimeReady = resolveFrozenHomieBinding(capability, r.Binding)
		if r.RuntimeReady && r.Runtime.Harness != "fake" {
			auth, _, err := ConnectionAdmission(ctx, q, r.Runtime.Credential, r.Runtime.ID, time.Now(), r.Runtime.ProfileRevision)
			r.RuntimeReady = err == nil && (r.Runtime.AuthorizationID == "" || auth == r.Runtime.AuthorizationID)
		}
		t, err := parseSpineTime(lastAct)
		if err != nil {
			return nil, err
		}
		r.LastActivityAt = t
		r.HasLaunch = launchID.Valid
		r.LaunchID = launchID.String
		r.LaunchMode = launchMode.String
		r.LaunchAbsentConfirmed = inventory != nil && launchID.Valid && !containerID.Valid &&
			!sessionObserved[r.SessionID]
		r.ResumeOwed = resumeOwed == 1
		r.ResumeMode = resumeMode.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// newHomieLaunchID mints a fresh 16-hex launch generation, the same
// dual-length shape the schema fences (schema.sql homie_sessions).
func newHomieLaunchID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", Usagef("allocate homie launch id: %v", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// homieWakeRound is the lease-free Homie tick, run in the dispatch prepare
// transaction only when the pipeline Decide() committed nothing.
// It returns (effect, handled, err): handled=false means no Homie action this
// tick (fall through to the pipeline-idle final). A spawn wake mints and
// persists the launch generation (clearing resume debt, carrying a rows-mode
// prime cutoff forward) and emits `homie-wake`; an idle end flips the session
// to ended (the trigger deactivates its bindings) and emits `homie-stop`.
func homieWakeRound(ctx context.Context, q Q, now time.Time, idleTimeoutS int, capability PrivateRuntimeCapability) (map[string]any, bool, error) {
	return homieWakeRoundWithConfirmedAbsence(ctx, q, now, idleTimeoutS, nil, capability)
}

func homieWakeRoundWithConfirmedAbsence(
	ctx context.Context,
	q Q,
	now time.Time,
	idleTimeoutS int,
	inventory []PrivateHomieContainer,
	capability PrivateRuntimeCapability,
) (map[string]any, bool, error) {
	rows, err := loadHomieSchedRowsWithConfirmedAbsence(ctx, q, inventory, capability)
	if err != nil {
		return nil, false, err
	}
	d := selectHomieWake(rows, now, time.Duration(idleTimeoutS)*time.Second)
	switch d.Kind {
	case homieWakeNone:
		return nil, false, nil

	case homieWakeIdle:
		var containerID sql.NullString
		if err := q.QueryRowContext(ctx,
			`SELECT current_container_id FROM homie_sessions WHERE id = ?`,
			d.SessionID).Scan(&containerID); err != nil {
			return nil, false, err
		}
		if _, _, err := homieEndTx(ctx, q, "dispatch", d.SessionID, "idle timeout"); err != nil {
			return nil, false, err
		}
		effect := map[string]any{
			"action":  "homie-stop",
			"session": d.SessionID,
			"reason":  "idle",
		}
		if containerID.Valid {
			effect["container_id"] = containerID.String
		}
		return effect, true, nil

	case homieWakeSpawn:
		launchID := d.LaunchID
		if !d.ExistingLaunch {
			launchID, err = newHomieLaunchID()
			if err != nil {
				return nil, false, err
			}
		}
		var binding, containerName, sessionPath, allowlistJSON string
		var nativeRef, traceFile, visionProject, packetProject sql.NullString
		var packet, packetVersion, packetTask sql.NullInt64
		var effort string
		if err := q.QueryRowContext(ctx,
			`SELECT s.binding, s.container_name, s.session_path, s.native_session_ref, s.trace_filename, s.effort,
			        s.verb_allowlist, s.vision_project_id, s.packet_id, p.version_id, p.task_id, t.project
			 FROM homie_sessions s
			 LEFT JOIN review_versions p ON p.id = s.packet_id
			 LEFT JOIN tasks t ON t.id = p.task_id
			 WHERE s.id = ?`,
			d.SessionID).Scan(&binding, &containerName, &sessionPath, &nativeRef, &traceFile, &effort,
			&allowlistJSON, &visionProject, &packet, &packetVersion, &packetTask, &packetProject); err != nil {
			return nil, false, err
		}
		// The resident mounts the artifact files of the task of a packet
		// conversation, and the runner checks the packet context against it.
		var wakePacket map[string]any
		if packet.Valid {
			wakePacket = map[string]any{
				"packet_id": packet.Int64, "version_id": packetVersion.Int64,
				"task_id": packetTask.Int64, "project_id": packetProject.String,
			}
		}
		// The session's FROZEN allowlist must travel with the wake, because it is
		// the only thing that makes the Homie's operator verbs callable at all:
		// RequireOperatorVerb admits a Homie solely by scanning the allowlist it
		// reads from run.json, and requireActiveHomieVerb additionally compares
		// that envelope value against this stored one. Omitting it — which every
		// wake did until 2026-08-07 — leaves it nil in the container, so all
		// twelve verbs refuse and a live Homie can only read.
		//
		// It is re-read here rather than assumed to be maximumHomieAgentVerbs:
		// `mc homie start --allow` can freeze a narrower set, and the registry
		// comparison would then refuse a compiled-in maximum just as hard.
		allowlist, err := decodeFrozenVerbAllowlist(allowlistJSON, d.SessionID)
		if err != nil {
			return nil, false, err
		}
		if d.LaunchMode == "native" && (!nativeRef.Valid || !traceFile.Valid) {
			return nil, false, Domainf("Homie session %q has native resume debt without its immutable locator pair",
				d.SessionID)
		}
		// Converse fence on the resolution the selector made: the runtime this
		// wake would ship must still describe the frozen binding just re-read
		// from the registry row. A disagreement mutates nothing — no launch
		// generation is minted and no existing one is re-emitted.
		if d.Runtime.ID == "" || binding != d.Runtime.Harness+"/"+d.Runtime.ID {
			return nil, false, nil
		}
		if d.ExistingLaunch {
			effect, err := homieWakeEffect(
				d.SessionID, launchID, d.LaunchMode, binding, containerName,
				sessionPath, nativeRef, traceFile, d.Runtime, allowlist, idleTimeoutS,
			)
			if err != nil {
				return nil, false, err
			}
			effect["effort"] = effort
			if visionProject.Valid {
				effect["vision_project_id"] = visionProject.String
			}
			if wakePacket != nil {
				effect["packet"] = wakePacket
			}
			return effect, true, nil
		}
		// Persist the launch generation and clear resume debt in one statement.
		// A rows-mode wake carries the resume prime cutoff into the current
		// prime pair (SQLite evaluates every RHS against the pre-update row, so
		// reading resume_prime_* while nulling it is safe); fresh/native leave
		// the current prime pair NULL. The CAS on current_launch_id IS NULL
		// keeps a re-entrant tick from double-launching.
		var res sql.Result
		if d.LaunchMode == "rows" {
			res, err = q.ExecContext(ctx, `
				UPDATE homie_sessions
				SET current_launch_id = ?, current_launch_mode = 'rows',
				    current_prime_through_seq = resume_prime_through_seq,
				    current_prime_row_count = resume_prime_row_count,
				    resume_owed = 0, resume_mode = NULL,
				    resume_prime_through_seq = NULL, resume_prime_row_count = NULL
				WHERE id = ? AND status = 'active' AND current_launch_id IS NULL`,
				launchID, d.SessionID)
		} else {
			res, err = q.ExecContext(ctx, `
				UPDATE homie_sessions
				SET current_launch_id = ?, current_launch_mode = ?,
				    resume_owed = 0, resume_mode = NULL,
				    resume_prime_through_seq = NULL, resume_prime_row_count = NULL
				WHERE id = ? AND status = 'active' AND current_launch_id IS NULL`,
				launchID, d.LaunchMode, d.SessionID)
		}
		if err != nil {
			return nil, false, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, false, err
		}
		if n == 0 {
			// Already launched under this tick's own CAS window — nothing to emit.
			return nil, false, nil
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO activity (actor, kind, subject, detail)
			VALUES ('dispatch', 'homie.launched', ?, ?)`,
			d.SessionID, launchID); err != nil {
			return nil, false, err
		}
		effect, err := homieWakeEffect(
			d.SessionID, launchID, d.LaunchMode, binding, containerName,
			sessionPath, nativeRef, traceFile, d.Runtime, allowlist, idleTimeoutS,
		)
		if err != nil {
			return nil, false, err
		}
		effect["effort"] = effort
		if visionProject.Valid {
			effect["vision_project_id"] = visionProject.String
		}
		if wakePacket != nil {
			effect["packet"] = wakePacket
		}
		return effect, true, nil
	}
	return nil, false, nil
}

// decodeFrozenVerbAllowlist reads the stored allowlist into the exact shape the
// container identity decodes. nil is normalized to empty so the carrier can
// never encode JSON null: this repo has paid for absent-vs-empty divergence in
// shared carriers before, and here the two would mean "no verbs" and "no field",
// which the two gates would report as different failures.
func decodeFrozenVerbAllowlist(stored, sessionID string) ([]string, error) {
	verbs := []string{}
	if err := json.Unmarshal([]byte(stored), &verbs); err != nil {
		return nil, Domainf("parse frozen allowlist for Homie session %s: %v", sessionID, err)
	}
	if verbs == nil {
		verbs = []string{}
	}
	return verbs, nil
}

func homieWakeEffect(
	sessionID, launchID, launchMode, binding, containerName, sessionPath string,
	nativeRef, traceFile sql.NullString,
	runtime PrivateRuntimeBinding,
	verbAllowlist []string,
	idleTimeoutS int,
) (map[string]any, error) {
	runtimeValue, err := runtimeBindingEffectValue(runtime)
	if err != nil {
		return nil, err
	}
	// The Homie tier's shipped skills, carried the same way a pipeline spawn
	// carries its role's: the runner installs them before the harness starts.
	// One static set — maximumHomieAgentVerbs is compiled in and `mc homie
	// start` defaults to all of it, so this does not vary by session.
	skills, err := HomieSkillSet()
	if err != nil {
		return nil, err
	}
	effect := map[string]any{
		"action":    "homie-wake",
		"directive": strings.TrimSpace(homieDirective),
		"skills":    SkillsEffectValue(skills),
		// The operator skill store directory of the Homie tier,
		// MC_HOME-relative; the resident copies what it finds there into the
		// container beside the shipped set.
		"skill_store_dir": HomieSkillStoreRelDir,
		"session":         sessionID,
		"launch":          launchID,
		"mode":            launchMode,
		"binding":         binding,
		"session_path":    sessionPath,
		"container_name":  containerName,
		// The resolved capability travels WITH the wake: the resident owns no
		// route catalog of its own, so a launch that config no longer describes
		// is impossible to construct rather than merely discouraged.
		"runtime_binding": runtimeValue,
		// The frozen operator-verb allowlist. Without it in run.json the
		// container's `mc` refuses every write verb the Homie skill advertises.
		"verb_allowlist": verbAllowlist,
		// The session idle timeout the selector ends the session on. The runner
		// uses it as its own exit deadline when the stop never arrives.
		"idle_timeout_s": idleTimeoutS,
	}
	if launchMode == "native" {
		effect["native_session_ref"] = nativeRef.String
		effect["trace_filename"] = traceFile.String
	}
	return effect, nil
}
