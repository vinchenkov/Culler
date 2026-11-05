package verbs

import (
	"context"
	"sort"
	"time"
)

// PrivateHomieContainer is the closed, normalized Docker projection carried
// over the resident control channel. Docker's wide inspect record and
// arbitrary labels never cross into the helper.
type PrivateHomieContainer struct {
	ContainerID        string                          `json:"container_id"`
	Launch             string                          `json:"launch"`
	Name               string                          `json:"name"`
	RuntimeAttestation *PrivateHomieRuntimeAttestation `json:"runtime_attestation,omitempty"`
	Session            string                          `json:"session"`
	State              string                          `json:"state"`
}

// PrivateHomieRuntimeAttestation is deliberately compact and non-secret. The
// resident emits it only when its memory-only row binds this exact Docker id to
// the create envelope prepared during the current resident boot.
type PrivateHomieRuntimeAttestation struct {
	EnvelopeDigest    string `json:"envelope_digest"`
	RuntimeGeneration string `json:"runtime_generation"`
}

const maxPrivateHomieContainers = 128

func ValidatePrivateHomieInventory(inventory []PrivateHomieContainer) error {
	if inventory == nil {
		return Domainf("dispatch: Homie container inventory is absent")
	}
	if len(inventory) > maxPrivateHomieContainers {
		return Domainf("dispatch: Homie container inventory exceeds its cardinality bound")
	}
	seen := make(map[string]bool, len(inventory))
	last := ""
	for _, item := range inventory {
		if !validLowercaseHex(item.ContainerID, 64) ||
			!validLowercaseHex(item.Launch, 16) ||
			!validHomieInventorySession(item.Session) ||
			item.Name != "mc-homie-"+item.Session {
			return Domainf("dispatch: Homie container inventory carries a malformed identity")
		}
		if proof := item.RuntimeAttestation; proof != nil {
			if !validLowercaseHex(proof.EnvelopeDigest, 64) ||
				!validLowercaseHex(proof.RuntimeGeneration, 64) {
				return Domainf("dispatch: Homie container inventory carries a malformed runtime attestation")
			}
		}
		switch item.State {
		case "created", "running", "paused", "restarting", "removing", "exited", "dead":
		default:
			return Domainf("dispatch: Homie container inventory carries an unknown state")
		}
		if seen[item.ContainerID] || (last != "" && item.ContainerID < last) {
			return Domainf("dispatch: Homie container inventory is duplicated or unsorted")
		}
		seen[item.ContainerID] = true
		last = item.ContainerID
	}
	return nil
}

func validHomieInventorySession(value string) bool {
	if len(value) < 3 || len(value) > maxPrivateScalarBytes || value[:2] != "h-" {
		return false
	}
	for _, r := range value[2:] {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

// homieOrphanRound reaps an orphaned container. A container is orphaned
// when no active registry row matches its exact physical generation. A
// nominally matching running/created generation is also orphaned when the
// resident omitted its current-boot, full-envelope attestation: identity alone
// never establishes a live or adoptable Homie.
func homieOrphanRound(
	ctx context.Context,
	q Q,
	inventory []PrivateHomieContainer,
) (map[string]any, bool, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, current_launch_id, current_container_id
		FROM homie_sessions
		WHERE status = 'active'
		  AND current_launch_id IS NOT NULL
		  AND current_container_id IS NOT NULL`)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	live := make(map[string]bool)
	for rows.Next() {
		var session, launch, containerID string
		if err := rows.Scan(&session, &launch, &containerID); err != nil {
			return nil, false, err
		}
		live[session+"\x00"+launch+"\x00"+containerID] = true
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	orphans := make([]PrivateHomieContainer, 0, len(inventory))
	for _, item := range inventory {
		exact := live[item.Session+"\x00"+item.Launch+"\x00"+item.ContainerID]
		if !exact ||
			((item.State == "running" || item.State == "created") &&
				item.RuntimeAttestation == nil) {
			orphans = append(orphans, item)
		}
	}
	if len(orphans) == 0 {
		return nil, false, nil
	}
	sort.Slice(orphans, func(i, j int) bool {
		a, b := orphans[i], orphans[j]
		if a.Session != b.Session {
			return a.Session < b.Session
		}
		if a.Launch != b.Launch {
			return a.Launch < b.Launch
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ContainerID < b.ContainerID
	})
	orphan := orphans[0]
	return map[string]any{
		"action":         "homie-remove",
		"session":        orphan.Session,
		"launch":         orphan.Launch,
		"container_id":   orphan.ContainerID,
		"container_name": orphan.Name,
		"reason":         "orphan",
	}, true, nil
}

// homieCreatedRound is the exact-created adoption arm, under the current
// credential and runtime model. The orphan round already removed every
// created object without a live full-envelope proof. This arm therefore needs
// only select an exact active, bound, not-yet-started, non-idle generation
// and echo the opaque proof so the same resident can re-inspect immediately
// before exact-id start.
func homieCreatedRound(
	ctx context.Context,
	q Q,
	inventory []PrivateHomieContainer,
	now time.Time,
	idleTimeoutS int,
) (map[string]any, bool, error) {
	type boundGeneration struct {
		ContainerID  string
		LastActivity time.Time
		Launch       string
	}
	rows, err := q.QueryContext(ctx, `
		SELECT id, last_activity_at, current_launch_id, current_container_id
		FROM homie_sessions
		WHERE status = 'active'
		  AND current_launch_id IS NOT NULL
		  AND current_container_id IS NOT NULL
		  AND launch_bound_at IS NOT NULL
		  AND launch_started_at IS NULL`)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	bound := make(map[string]boundGeneration)
	for rows.Next() {
		var session, lastActivity, launch, containerID string
		if err := rows.Scan(&session, &lastActivity, &launch, &containerID); err != nil {
			return nil, false, err
		}
		parsed, err := parseSpineTime(lastActivity)
		if err != nil {
			return nil, false, err
		}
		bound[session] = boundGeneration{
			ContainerID: containerID, LastActivity: parsed, Launch: launch,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	candidates := make([]PrivateHomieContainer, 0)
	idleTimeout := time.Duration(idleTimeoutS) * time.Second
	for _, item := range inventory {
		generation, ok := bound[item.Session]
		if !ok || item.State != "created" || item.RuntimeAttestation == nil ||
			generation.Launch != item.Launch ||
			generation.ContainerID != item.ContainerID ||
			!now.Before(generation.LastActivity.Add(idleTimeout)) {
			continue
		}
		candidates = append(candidates, item)
	}
	if len(candidates) == 0 {
		return nil, false, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Session < candidates[j].Session
	})
	selected := candidates[0]
	proof := selected.RuntimeAttestation
	return map[string]any{
		"action":             "homie-adopt",
		"container_id":       selected.ContainerID,
		"container_name":     selected.Name,
		"envelope_digest":    proof.EnvelopeDigest,
		"launch":             selected.Launch,
		"runtime_generation": proof.RuntimeGeneration,
		"session":            selected.Session,
	}, true, nil
}

type homieRemovalCandidate struct {
	Container      PrivateHomieContainer
	CaseRank       int
	LastActivityAt time.Time
}

// homieRemovalRound is the exact-container cleanup arm. Transitional
// containers rank ahead of terminal containers, then selection is stable by
// (last_activity_at, session id). An idle-threshold row is excluded: another
// arm ends that session and stops its exact bound container.
//
// This cleanup is safe with the identity-only inventory carrier: if a nominal
// exact match later fails the wider execution-envelope attestation, the
// orphan round requires the same exact-id removal. Created adoption and
// healthy-running classification remain deferred until the carrier can attest
// that envelope.
func homieRemovalRound(
	ctx context.Context,
	q Q,
	inventory []PrivateHomieContainer,
	now time.Time,
	idleTimeoutS int,
) (map[string]any, bool, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, last_activity_at, current_launch_id, current_container_id
		FROM homie_sessions
		WHERE status = 'active'
		  AND current_launch_id IS NOT NULL
		  AND current_container_id IS NOT NULL`)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	type boundGeneration struct {
		Launch         string
		ContainerID    string
		LastActivityAt time.Time
	}
	bound := make(map[string]boundGeneration)
	for rows.Next() {
		var session, lastActivity, launch, containerID string
		if err := rows.Scan(&session, &lastActivity, &launch, &containerID); err != nil {
			return nil, false, err
		}
		parsed, err := parseSpineTime(lastActivity)
		if err != nil {
			return nil, false, err
		}
		bound[session] = boundGeneration{
			Launch: launch, ContainerID: containerID, LastActivityAt: parsed,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	idleTimeout := time.Duration(idleTimeoutS) * time.Second
	candidates := make([]homieRemovalCandidate, 0)
	for _, item := range inventory {
		generation, ok := bound[item.Session]
		if !ok ||
			generation.Launch != item.Launch ||
			generation.ContainerID != item.ContainerID ||
			!now.Before(generation.LastActivityAt.Add(idleTimeout)) {
			continue
		}
		rank := -1
		switch item.State {
		case "paused", "restarting", "removing":
			rank = 0
		case "exited", "dead":
			rank = 1
		}
		if rank >= 0 {
			candidates = append(candidates, homieRemovalCandidate{
				Container: item, CaseRank: rank, LastActivityAt: generation.LastActivityAt,
			})
		}
	}
	if len(candidates) == 0 {
		return nil, false, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.CaseRank != b.CaseRank {
			return a.CaseRank < b.CaseRank
		}
		if !a.LastActivityAt.Equal(b.LastActivityAt) {
			return a.LastActivityAt.Before(b.LastActivityAt)
		}
		return a.Container.Session < b.Container.Session
	})
	selected := candidates[0].Container
	return map[string]any{
		"action":         "homie-remove",
		"session":        selected.Session,
		"launch":         selected.Launch,
		"container_id":   selected.ContainerID,
		"container_name": selected.Name,
		"reason":         selected.State,
	}, true, nil
}

type homieAbsentCandidate struct {
	SessionID      string
	LastActivityAt time.Time
	// PendingInput marks a session that still owes the operator a reply. Such a
	// session is RECOVERED rather than ended — see homieStartedAbsentRound.
	PendingInput bool
	// NativeResumable is the native locator pair. Its absence is not a failure:
	// the conversation rows are the declared fallback, lossier but never
	// lock-step dependent on a harness file format.
	NativeResumable bool
}

// homieStartedAbsentRound is the confirmed-absent started-generation arm.
// Inventory absence is evaluated per exact (session, launch, container-id),
// never as a global empty/non-empty flag. Only launch_started_at establishes
// this generation; immutable native locators may belong to an older
// generation. Another arm owns the idle rows.
//
// A session whose container is gone but which still OWES THE OPERATOR A REPLY
// is recovered rather than ended, and that split is the whole point of this
// arm's two outcomes. The promise is direct: if a Homie container or runner
// dies mid-conversation, the next inbound message makes the conversation
// eligible again, and the next tick wakes a fresh instance that continues from
// the persisted turns. An unconditional end broke it, and broke it silently
// — the inbound row stays uncompleted forever, `mc homie send` then refuses the
// ended session, and no surface ever says the message was dropped. A container
// can exit under a live session (a stop, a crash, the runner's own exit
// deadline), and any inbound turn that arrives after such an exit lands here.
//
// Recovery reuses `mc homie resume`'s own transition: clear the dead launch
// generation, leave the session active, and record native resume debt, so the
// selector wakes a container on a following tick that CONTINUES the harness
// session rather than starting over.
//
// Where it differs from the verb is the missing-locator case, and the reasoning
// is worth keeping. Resume REFUSES a session with no native locator pair,
// because an operator naming a conversation to continue deserves a refusal over
// a silent downgrade. Refusing here would strand the very turn this arm exists
// to save, so the generation is cleared with NO resume debt and the selector
// takes its ordinary fresh-wake path. That loses nothing recoverable: the
// locator pair is registered by the runner as soon as the harness session
// exists, so its absence means no harness session was ever established for
// this row and there is no continuation to lose. The conversation-rows
// fallback is deliberately NOT reached for — `rows` mode has no producer
// anywhere in the system, and inventing this arm as its first one would put an
// untried launch mode on a recovery path, which is the wrong place to prove
// one.
func homieStartedAbsentRound(
	ctx context.Context,
	q Q,
	inventory []PrivateHomieContainer,
	now time.Time,
	idleTimeoutS int,
) (map[string]any, bool, error) {
	observed := make(map[string]bool, len(inventory))
	for _, item := range inventory {
		observed[item.Session+"\x00"+item.Launch+"\x00"+item.ContainerID] = true
	}

	rows, err := q.QueryContext(ctx, `
		SELECT s.id, s.last_activity_at, s.current_launch_id, s.current_container_id,
		       EXISTS (SELECT 1 FROM conversation_messages m
		               WHERE m.session_id = s.id
		                 AND m.direction = 'inbound'
		                 AND m.completed_at IS NULL),
		       s.native_session_ref IS NOT NULL AND s.trace_filename IS NOT NULL
		FROM homie_sessions s
		WHERE s.status = 'active'
		  AND s.current_launch_id IS NOT NULL
		  AND s.current_container_id IS NOT NULL
		  AND s.launch_started_at IS NOT NULL`)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	idleTimeout := time.Duration(idleTimeoutS) * time.Second
	candidates := make([]homieAbsentCandidate, 0)
	for rows.Next() {
		var session, lastActivity, launch, containerID string
		var pending, nativeResumable int
		if err := rows.Scan(&session, &lastActivity, &launch, &containerID,
			&pending, &nativeResumable); err != nil {
			return nil, false, err
		}
		parsed, err := parseSpineTime(lastActivity)
		if err != nil {
			return nil, false, err
		}
		if !now.Before(parsed.Add(idleTimeout)) ||
			observed[session+"\x00"+launch+"\x00"+containerID] {
			continue
		}
		candidates = append(candidates, homieAbsentCandidate{
			SessionID:       session,
			LastActivityAt:  parsed,
			PendingInput:    pending == 1,
			NativeResumable: nativeResumable == 1,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(candidates) == 0 {
		return nil, false, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if !a.LastActivityAt.Equal(b.LastActivityAt) {
			return a.LastActivityAt.Before(b.LastActivityAt)
		}
		return a.SessionID < b.SessionID
	})
	selected := candidates[0]

	if selected.PendingInput {
		mode := "fresh"
		if selected.NativeResumable {
			mode = "native"
		}
		if err := homieRecoverForPendingInputTx(ctx, q, selected.SessionID, selected.NativeResumable); err != nil {
			return nil, false, err
		}
		// The same homie-stop the end arm emits: inventory has already confirmed
		// this container absent, so the host side is a proven no-op, and the
		// launch generation this action tears down is gone from the row either
		// way. Reusing it keeps the resident's effect surface unchanged.
		return map[string]any{
			"action":  "homie-stop",
			"session": selected.SessionID,
			"reason":  "exited-owed-reply",
			"resume":  mode,
		}, true, nil
	}

	changed, _, err := homieEndTx(ctx, q, "dispatch", selected.SessionID, "exited")
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return nil, false, Domainf(
			"dispatch: selected started Homie generation %q was not active", selected.SessionID)
	}
	return map[string]any{
		"action":  "homie-stop",
		"session": selected.SessionID,
		"reason":  "exited",
	}, true, nil
}

// Recovery preserves the conversation and fails active reply attempts.
// A failed reply requires an explicit retry. Unclaimed messages remain available.
// The replacement process uses the retained native session when its locator exists.
// Recovery preserves last_activity_at, so it cannot extend an idle session.
func homieRecoverForPendingInputTx(ctx context.Context, q Q, sessionID string, nativeResumable bool) error {
	if err := failSessionReplies(ctx, q, sessionID, "The Homie process ended before the reply completed."); err != nil {
		return err
	}
	resumeOwed, resumeMode := 0, any(nil)
	detail := "container exited with an inbound turn owed; wake fresh"
	if nativeResumable {
		resumeOwed, resumeMode = 1, any("native")
		detail = "container exited with an inbound turn owed; resume native"
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE homie_sessions
		SET current_launch_id = NULL, current_launch_mode = NULL,
		    current_prime_through_seq = NULL, current_prime_row_count = NULL,
		    current_container_id = NULL, launch_bound_at = NULL,
		    launch_started_at = NULL,
		    resume_owed = ?, resume_mode = ?,
		    resume_prime_through_seq = NULL, resume_prime_row_count = NULL
		WHERE id = ? AND status = 'active'`,
		resumeOwed, resumeMode, sessionID); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO activity (actor, kind, subject, detail)
		VALUES ('dispatch', 'homie.resumed', ?, ?)`, sessionID, detail)
	return err
}

// homieStartingTimeoutRound is the final identity-safe and state-safe arm: an
// exact running container that never reported runner-started is stopped and
// removed once its bound generation reaches the pipeline spawn-grace
// threshold. The durable pair remains until a later inventory snapshot
// confirms absence, so replacement cannot overlap it.
func homieStartingTimeoutRound(
	ctx context.Context,
	q Q,
	inventory []PrivateHomieContainer,
	now time.Time,
	idleTimeoutS, spawnGraceS int,
) (map[string]any, bool, error) {
	observedRunning := make(map[string]PrivateHomieContainer)
	for _, item := range inventory {
		if item.State == "running" {
			observedRunning[item.Session+"\x00"+item.Launch+"\x00"+item.ContainerID] = item
		}
	}

	rows, err := q.QueryContext(ctx, `
		SELECT id, last_activity_at, current_launch_id, current_container_id,
		       launch_bound_at
		FROM homie_sessions
		WHERE status = 'active'
		  AND current_launch_id IS NOT NULL
		  AND current_container_id IS NOT NULL
		  AND launch_bound_at IS NOT NULL
		  AND launch_started_at IS NULL`)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	type candidate struct {
		Container      PrivateHomieContainer
		LastActivityAt time.Time
	}
	idleTimeout := time.Duration(idleTimeoutS) * time.Second
	spawnGrace := time.Duration(spawnGraceS) * time.Second
	candidates := make([]candidate, 0)
	for rows.Next() {
		var session, lastActivity, launch, containerID, boundAt string
		if err := rows.Scan(&session, &lastActivity, &launch, &containerID, &boundAt); err != nil {
			return nil, false, err
		}
		last, err := parseSpineTime(lastActivity)
		if err != nil {
			return nil, false, err
		}
		bound, err := parseSpineTime(boundAt)
		if err != nil {
			return nil, false, err
		}
		item, exact := observedRunning[session+"\x00"+launch+"\x00"+containerID]
		if !exact ||
			!now.Before(last.Add(idleTimeout)) ||
			now.Before(bound.Add(spawnGrace)) {
			continue
		}
		candidates = append(candidates, candidate{
			Container: item, LastActivityAt: last,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(candidates) == 0 {
		return nil, false, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if !a.LastActivityAt.Equal(b.LastActivityAt) {
			return a.LastActivityAt.Before(b.LastActivityAt)
		}
		return a.Container.Session < b.Container.Session
	})
	selected := candidates[0].Container
	return map[string]any{
		"action":         "homie-remove",
		"session":        selected.Session,
		"launch":         selected.Launch,
		"container_id":   selected.ContainerID,
		"container_name": selected.Name,
		"reason":         "starting-timeout",
	}, true, nil
}
