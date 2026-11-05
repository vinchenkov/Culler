package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"time"

	"mc/domain"
	"mc/refusal"
)

type RefusalCandidateKind string

const (
	RefusalSubjectTask RefusalCandidateKind = "subject_task"

	RefusalSubjectlessPipeline RefusalCandidateKind = "subjectless_pipeline"

	RefusalHomie RefusalCandidateKind = "homie"

	// RefusalWikiUpdate is the Scribe run of a wiki update. Only
	// applyWikiRefusal accepts it.
	RefusalWikiUpdate RefusalCandidateKind = "wiki_update"
)

type RefusalCandidate struct {
	Kind RefusalCandidateKind

	Project string

	TaskID  *int64
	PoolIDs []int64

	SessionID string

	LaunchID string

	// WikiStart is the start of the wiki update of a refused wiki candidate.
	WikiStart domain.WikiStart
}

func applyRefusal(ctx context.Context, q Q, cand RefusalCandidate, r refusal.Refusal, dispatchKey string) (map[string]any, error) {
	effect, _, err := applyRefusalWithReceipt(ctx, q, cand, r, dispatchKey, "")
	return effect, err
}

func applyRefusalWithReceipt(ctx context.Context, q Q, cand RefusalCandidate, r refusal.Refusal, dispatchKey, requestID string) (map[string]any, bool, error) {
	class, err := refusal.Classify(r)
	if err != nil {
		return nil, false, Domainf("%v", err)
	}
	detail, err := refusal.DetailFor(r)
	if err != nil {
		return nil, false, Domainf("%v", err)
	}
	if dispatchKey != "" && !validDispatchKey(dispatchKey) {
		return nil, false, Domainf("dispatch_key must be exactly 64 lowercase hex characters")
	}
	if err := validateRefusalCandidate(cand); err != nil {
		return nil, false, err
	}

	effect := map[string]any{"action": "refused", "class": string(class), "code": r.Code}
	if cand.Project != "" {
		effect["project"] = cand.Project
	}

	switch class {
	case refusal.ClassStale:
		effect["consequence"] = "none"
		return effect, false, nil

	case refusal.ClassHealth:
		effect["consequence"] = "health"
		if err := chargeDispatchHealth(ctx, q, cand, r, effect); err != nil {
			return nil, false, err
		}
		if err := recordDispatchHealth(ctx, q, cand, detail, dispatchKey, requestID, effect); err != nil {
			return nil, false, err
		}
		return effect, requestID != "", nil

	case refusal.ClassCandidate:
		return applyCandidatePolicy(ctx, q, cand, r, detail, dispatchKey, requestID, effect)
	}
	return nil, false, Domainf("refusal class %q has no consequence", class)
}

func applyCandidatePolicy(ctx context.Context, q Q, cand RefusalCandidate, r refusal.Refusal, detail refusal.Detail, dispatchKey, requestID string, effect map[string]any) (map[string]any, bool, error) {
	switch cand.Kind {
	case RefusalSubjectTask:
		if err := domain.Block(ctx, q, *cand.TaskID, domain.BlockConfinement, confinementReason(r.Code)); err != nil {
			return nil, false, err
		}
		effect["consequence"] = "task_blocked"
		effect["task_id"] = *cand.TaskID
		return effect, false, nil

	case RefusalSubjectlessPipeline:
		decision, err := domain.RecordProjectProposalDecision(ctx, q, cand.Project, "", confinementReason(r.Code), cand.PoolIDs)
		if err != nil {
			return nil, false, err
		}
		effect["consequence"] = "proposal_decision_pending"
		effect["decision_id"] = decision
		if err := recordDispatchHealth(ctx, q, cand, detail, dispatchKey, requestID, effect); err != nil {
			return nil, false, err
		}
		return effect, requestID != "", nil

	case RefusalHomie:
		held, err := homieLaunchFenceHolds(ctx, q, cand.SessionID, cand.LaunchID)
		if err != nil {
			return nil, false, err
		}
		if !held {
			effect["consequence"] = "none"
			effect["session_id"] = cand.SessionID
			effect["launch_superseded"] = true
			return effect, false, nil
		}
		ended, status, err := homieEndTx(ctx, q, "dispatch", cand.SessionID, confinementReason(r.Code))
		if err != nil {
			return nil, false, err
		}
		effect["consequence"] = "homie_ended"
		effect["session_id"] = cand.SessionID
		effect["ended"] = ended
		effect["status"] = status
		return effect, false, nil
	}
	return nil, false, Domainf("dispatch: unknown refusal candidate kind %q", cand.Kind)
}

func confinementReason(code string) string { return "confinement:" + code }

// applyWikiRefusal routes the refusal of a wiki candidate. A stale refusal
// writes nothing, and the wiki update stays due. Each other refusal records
// the wiki update as failed in this transaction: it inserts the Scribe run
// and the wiki update, writes the start record as the receipt of the round,
// and ends both. No container exists, so the same call records that mc owes
// no container stop. The route charges no allowance, blocks nothing, and
// raises no operator alert.
func applyWikiRefusal(ctx context.Context, q Q, now time.Time, cand RefusalCandidate, r refusal.Refusal, dispatchKey, requestID string) (map[string]any, error) {
	class, err := refusal.Classify(r)
	if err != nil {
		return nil, Domainf("%v", err)
	}
	if _, err := refusal.DetailFor(r); err != nil {
		return nil, Domainf("%v", err)
	}
	start := cand.WikiStart
	if cand.Kind != RefusalWikiUpdate || cand.Project == "" || start.Project != cand.Project || !validLowercaseHex(start.RunID, 16) {
		return nil, Domainf("dispatch: the wiki refusal route requires a wiki candidate")
	}
	effect := map[string]any{"action": "refused", "class": string(class), "code": r.Code, "project": cand.Project}
	var cause string
	switch class {
	case refusal.ClassStale:
		effect["consequence"] = "none"
		return effect, nil
	case refusal.ClassHealth:
		cause = "Culler refused the start of the Scribe run. The Culler deployment is not ready (" + r.Code + ")."
	case refusal.ClassCandidate:
		cause = "Culler refused the start of the Scribe run. The configuration of the Scribe role or of the Project is not valid (" + r.Code + ")."
	default:
		return nil, Domainf("refusal class %q has no consequence", class)
	}
	if !validDispatchKey(dispatchKey) {
		return nil, Domainf("dispatch_key must be exactly 64 lowercase hex characters")
	}
	effect["consequence"] = "wiki_update_failed"
	effect["run_id"] = start.RunID
	if err := domain.StartWikiUpdate(ctx, q, now, start); err != nil {
		return nil, err
	}
	if err := writeWikiStartRecord(ctx, q, start.RunID, cand.Project, dispatchKey, requestID, effect); err != nil {
		return nil, err
	}
	ended, err := domain.EndWikiRun(ctx, q, start.RunID, domain.WikiRunEnd{
		Outcome: "infra-failed", EndReason: "launch", Result: "failed", Cause: cause, Stopped: true,
	})
	if err != nil {
		return nil, err
	}
	if !ended {
		return nil, Domainf("dispatch: the run of the refused wiki candidate has an end")
	}
	return effect, nil
}

func chargeDispatchHealth(ctx context.Context, q Q, cand RefusalCandidate, r refusal.Refusal, effect map[string]any) error {
	if cand.Kind == RefusalHomie {
		return nil
	}
	charge, err := domain.ChargeProjectInfrastructure(ctx, q, cand.Project, r.Code, "")
	if err != nil {
		return err
	}
	effect["infrastructure_remaining"] = charge.Remaining
	if charge.Stalled {
		effect["consequence"] = "project_infrastructure_exhausted"
	}
	return nil
}

func recordDispatchHealth(ctx context.Context, q Q, cand RefusalCandidate, d refusal.Detail, dispatchKey, requestID string, effect map[string]any) error {
	payload, err := d.Canonical()
	if err != nil {
		return Domainf("%v", err)
	}
	var subject any // NULL for a subjectless candidate
	switch cand.Kind {
	case RefusalSubjectTask:
		subject = strconv.FormatInt(*cand.TaskID, 10)
	case RefusalHomie:
		subject = cand.SessionID
	}
	var key any // NULL until the prepare slice derives one
	if dispatchKey != "" {
		key = dispatchKey
	}
	if requestID == "" {
		_, err = q.ExecContext(ctx, `
			INSERT INTO activity (actor, kind, subject, detail, dispatch_key)
			VALUES ('dispatch', 'dispatch.health', ?, ?, ?)`, subject, string(payload), key)
		return err
	}
	result, err := json.Marshal(effect)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `
		INSERT INTO activity
			(actor, kind, subject, detail, dispatch_key, dispatch_request_id, dispatch_result)
		VALUES ('dispatch', 'dispatch.health', ?, ?, ?, ?, ?)`,
		subject, string(payload), key, requestID, string(result))
	return err
}

func validateRefusalCandidate(c RefusalCandidate) error {
	switch c.Kind {
	case RefusalSubjectTask:
		if c.TaskID == nil {
			return Domainf("a subject-task refusal candidate needs a task id; the router never invents a subject to blame")
		}
	case RefusalSubjectlessPipeline:
	case RefusalHomie:
		if c.SessionID == "" {
			return Domainf("a Homie refusal candidate needs a session id")
		}
		if c.LaunchID != "" && !validLowercaseHex(c.LaunchID, 16) {
			return Domainf("a Homie refusal candidate's observed launch id must be empty or exactly 16 lowercase hex characters")
		}
	default:
		return Domainf("dispatch: unknown refusal candidate kind %q", c.Kind)
	}
	return nil
}

func homieLaunchFenceHolds(ctx context.Context, q Q, sessionID, observed string) (bool, error) {
	var current sql.NullString
	err := q.QueryRowContext(ctx,
		`SELECT current_launch_id FROM homie_sessions WHERE id = ?`, sessionID).Scan(&current)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return current.String == observed, nil
}

func validDispatchKey(s string) bool { return validLowercaseHex(s, 64) }

func validLowercaseHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
