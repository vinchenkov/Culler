package verbs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
)

// ---------------------------------------------------------------------------
// The collie.preflight_health starvation marker — the write half.
//
// This is not a health consequence: recordDispatchHealth keeps that arm. This
// is selection bookkeeping. When a Collie candidate's preflight hits a
// deployment-health refusal, the marker records "defer_pipeline=true" under a
// key derived from exactly the state the candidate was selected on. A later
// tick that retains an ordinary pipeline candidate compares that key against
// the latest unconsumed marker: same key means the same broken Collie state,
// and the pipeline candidate wins unconditionally instead of re-attesting it —
// so a broken projection/attachment mechanism cannot wedge the pipeline tier.
// A changed key (new input, repaired launch, cleared debt) is immediately
// eligible again.
//
// Nothing selects Collie candidates yet, so like applyRefusal before it, this
// seam has no caller but its tests. The consume
// half — "wins unconditionally", "consumes the marker by activity order" —
// belongs to the future selector, not here.
// ---------------------------------------------------------------------------

// collieCandidateState is the frozen serialization under the candidate key.
// Field order and names are a cross-harness wire contract: a drifted byte
// orphans every unconsumed marker on a real deployment (the pipeline-defer
// is silently lost once per repair). A change to this shape is a design
// decision, not a refactor. It covers ONLY the pre-prepare state — canonical
// session id, the whole typed launch/resume debt, frozen binding,
// pending-input sequence — so no path, env value, credential, or free text
// has a field to leak through.
type collieCandidateState struct {
	SessionID   string  `json:"session_id"`
	LaunchID    *string `json:"launch_id"`
	LaunchMode  *string `json:"launch_mode"`
	PrimeSeq    *int64  `json:"prime_through_seq"`
	PrimeCount  *int64  `json:"prime_row_count"`
	ResumeOwed  int64   `json:"resume_owed"`
	ResumeMode  *string `json:"resume_mode"`
	ResumeSeq   *int64  `json:"resume_prime_through_seq"`
	ResumeCount *int64  `json:"resume_prime_row_count"`
	Binding     string  `json:"binding"`
	InputSeq    *int64  `json:"input_seq"`
}

// collieCandidateKey derives the pre-prepare candidate key: SHA256 over the
// canonical serialization of the session's selection-relevant state. The
// "relevant conversation sequence" is the highest pending inbound turn —
// the input the candidate would consume; replies and completed turns do not
// participate, so consuming input returns the key to its input-less value
// rather than remembering it.
func collieCandidateKey(ctx context.Context, q Q, sessionID string) (string, error) {
	st, _, err := colliePrePrepareState(ctx, q, sessionID)
	if err != nil {
		return "", err
	}
	return st.key()
}

func (st collieCandidateState) key() (string, error) {
	payload, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func colliePrePrepareState(ctx context.Context, q Q, sessionID string) (collieCandidateState, string, error) {
	st := collieCandidateState{SessionID: sessionID}
	var status string
	var launchID, launchMode, resumeMode sql.NullString
	var primeSeq, primeCount, resumeSeq, resumeCount, inputSeq sql.NullInt64
	err := q.QueryRowContext(ctx, `
		SELECT s.status, s.binding,
		       s.current_launch_id, s.current_launch_mode,
		       s.current_prime_through_seq, s.current_prime_row_count,
		       s.resume_owed, s.resume_mode,
		       s.resume_prime_through_seq, s.resume_prime_row_count,
		       (SELECT MAX(m.seq) FROM conversation_messages m
		        WHERE m.session_id = s.id
		          AND m.direction = 'inbound' AND m.completed_at IS NULL)
		FROM collie_sessions s WHERE s.id = ?`, sessionID).Scan(
		&status, &st.Binding,
		&launchID, &launchMode, &primeSeq, &primeCount,
		&st.ResumeOwed, &resumeMode, &resumeSeq, &resumeCount,
		&inputSeq,
	)
	if err == sql.ErrNoRows {
		return st, "", Domainf("unknown Collie session %q", sessionID)
	}
	if err != nil {
		return st, "", err
	}
	if launchID.Valid {
		st.LaunchID = &launchID.String
	}
	if launchMode.Valid {
		st.LaunchMode = &launchMode.String
	}
	if primeSeq.Valid {
		st.PrimeSeq = &primeSeq.Int64
	}
	if primeCount.Valid {
		st.PrimeCount = &primeCount.Int64
	}
	if resumeMode.Valid {
		st.ResumeMode = &resumeMode.String
	}
	if resumeSeq.Valid {
		st.ResumeSeq = &resumeSeq.Int64
	}
	if resumeCount.Valid {
		st.ResumeCount = &resumeCount.Int64
	}
	if inputSeq.Valid {
		st.InputSeq = &inputSeq.Int64
	}
	return st, status, nil
}

// colliePreflightDetail is the marker's closed detail: derived state and a
// fixed flag, leak-proof by construction. The health code stays on the
// health action — the marker never repeats WHAT broke, only "defer the
// pipeline past me".
type colliePreflightDetail struct {
	CandidateKey  string `json:"candidate_key"`
	DeferPipeline bool   `json:"defer_pipeline"`
}

// recordColliePreflightHealth appends the starvation marker for an active
// session and returns its candidate key. Fail closed on anything that cannot
// be selected: an unknown or inactive session gets no marker, because a
// deferred pipeline is a real cost and a row that cannot be attested again
// buys nothing for it. Recurrence is by design (a markable state may recur
// every tick until repaired or superseded); each marker is its own activity
// row, consumed by activity order.
func recordColliePreflightHealth(ctx context.Context, q Q, sessionID string) (string, error) {
	st, status, err := colliePrePrepareState(ctx, q, sessionID)
	if err != nil {
		return "", err
	}
	if status != "active" {
		return "", Domainf("collie.preflight_health marks only an active session; %q is %s", sessionID, status)
	}
	key, err := st.key()
	if err != nil {
		return "", err
	}
	detail, err := json.Marshal(colliePreflightDetail{CandidateKey: key, DeferPipeline: true})
	if err != nil {
		return "", err
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO activity (actor, kind, subject, detail)
		VALUES ('dispatch', 'collie.preflight_health', ?, ?)`,
		sessionID, string(detail)); err != nil {
		return "", err
	}
	return key, nil
}
