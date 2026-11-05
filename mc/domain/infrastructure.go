package domain

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// InfrastructureFailureReport separates an observed condition from its cause.
type InfrastructureFailureReport struct {
	ProxyRequestID    string `json:"proxy_request_id,omitempty"`
	TrustedProxy      bool   `json:"-"`
	HTTPStatus        *int   `json:"http_status,omitempty"`
	ProviderErrorCode string `json:"provider_error_code,omitempty"`
	NetworkError      string `json:"network_error,omitempty"`
	Condition         string `json:"condition"`
	Source            string `json:"source"`
	Cause             string `json:"cause"`
	Evidence          string `json:"evidence"`
	OperatorAction    string `json:"operator_action"`
	QuotaResetAt      string `json:"quota_reset_at"`
	// ConnectionRefused states that the connection of a wiki update run refuses the run at the time of the report.
	// The command layer reads the connection state for it. No request holds it.
	ConnectionRefused bool `json:"-"`
}

// InfrastructureFailureResult reports whether this call charged the task.
type InfrastructureFailureResult struct {
	Allowance string
	ProjectID string
	TaskID    int64 `json:"task_id"`
	Charged   bool  `json:"charged"`
	Remaining int   `json:"remaining"`
	Stalled   bool  `json:"stalled"`
	// WikiUpdate marks the run of a wiki update. Such a run charges no allowance.
	WikiUpdate bool
}

// ValidateInfrastructureFailureReport requires evidence for a specific cause.
func ValidateInfrastructureFailureReport(report InfrastructureFailureReport) error {
	invalid := func(message string) error { return Errf("invalid-infrastructure-report", "%s", message) }
	if report.ProxyRequestID != "" && !proxyRequestIdentityPattern.MatchString(report.ProxyRequestID) {
		return invalid("The host provider request identity is invalid.")
	}
	if report.HTTPStatus != nil && (*report.HTTPStatus < 400 || *report.HTTPStatus > 599) {
		return invalid("The provider response status is invalid.")
	}
	switch report.ProviderErrorCode {
	case "", "invalid_api_key", "insufficient_quota", "rate_limit_exceeded", "model_not_found", "permission_denied", "authentication_failed", "oauth_org_not_allowed", "billing_error", "rate_limit", "overloaded", "invalid_request", "server_error":
	default:
		return invalid("The provider error code is invalid.")
	}
	switch report.NetworkError {
	case "", "connect", "reset", "timeout", "tls", "other":
	default:
		return invalid("The network error class is invalid.")
	}
	if report.NetworkError != "" && (report.HTTPStatus != nil || report.ProviderErrorCode != "") {
		return invalid("A network error has no provider response.")
	}
	if strings.TrimSpace(report.Condition) == "" || utf8.RuneCountInString(report.Condition) > 2048 {
		return invalid("the observed condition must contain between 1 and 2048 characters")
	}
	if report.Source != "agent" && report.Source != "resident" && report.Source != "runner" {
		return invalid("the report source must be agent, resident, or runner")
	}
	cause := report.Cause
	if cause == "" {
		cause = "unknown"
	}
	if cause != "unknown" && cause != "credentials" && cause != "quota" && cause != "connection" {
		return invalid("the cause must be unknown, credentials, quota, or connection")
	}
	if cause == "connection" && report.Source != "resident" {
		return invalid("only the resident reports a connection cause")
	}
	if utf8.RuneCountInString(report.Evidence) > 8192 || utf8.RuneCountInString(report.OperatorAction) > 2048 {
		return invalid("the evidence or operator action exceeds its size limit")
	}
	if cause != "unknown" && strings.TrimSpace(report.Evidence) == "" {
		return invalid("a known cause requires evidence from the service or a direct credential check")
	}
	if report.OperatorAction != "" && (cause == "unknown" || strings.TrimSpace(report.Evidence) == "") {
		return invalid("an operator action requires evidence for a known cause")
	}
	if report.QuotaResetAt != "" {
		if cause != "quota" || strings.TrimSpace(report.Evidence) == "" {
			return invalid("a quota reset time requires evidence for a quota failure")
		}
		if _, err := time.Parse(time.RFC3339, report.QuotaResetAt); err != nil {
			return invalid("the quota reset time must use RFC 3339")
		}
	}
	return nil
}

// RecordInfrastructureFailure ends the assigned run. The lease remains held until the resident confirms the container stop.
// Callers hold the write transaction for the complete operation.
func RecordInfrastructureFailure(ctx context.Context, q Q, runID string, report InfrastructureFailureReport) (InfrastructureFailureResult, error) {
	if err := ValidateInfrastructureFailureReport(report); err != nil {
		return InfrastructureFailureResult{}, err
	}
	if report.Source == "agent" {
		report.Cause = "unknown"
		report.OperatorAction = ""
		report.QuotaResetAt = ""
	}
	var err error
	report, err = ApplyProviderEvidence(ctx, q, runID, report)
	if err != nil {
		return InfrastructureFailureResult{}, err
	}
	return finishInfrastructureFailure(ctx, q, runID, report, "infra-failed", "", true)
}

// runFailureReason names why a run failed. A system condition stops work for
// more than one task, so it charges no allowance. Each system condition holds
// new runs through the connection state until the condition clears. An agent
// report names no system condition, because no connection state holds it.
func runFailureReason(report InfrastructureFailureReport) (reason string, system bool) {
	switch {
	case report.Source == "agent":
		return "agent", false
	case report.NetworkError != "":
		return "network", true
	case report.HTTPStatus != nil || report.ProviderErrorCode != "":
		return "provider", true
	case report.Source == "resident" && (report.Cause == "credentials" || report.Cause == "connection"):
		return "connection", true
	case report.Source == "runner":
		return "harness", false
	}
	return "launch", false
}

// A recorded outcome wins over later observations of the same process.
// An empty reason derives the end reason from the report.
func finishInfrastructureFailure(ctx context.Context, q Q, runID string, report InfrastructureFailureReport, outcome, reason string, charge bool) (InfrastructureFailureResult, error) {
	var res InfrastructureFailureResult
	var subject sql.NullInt64
	var project, role, binding string
	var ended, recordedOutcome, started sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT subject, ended_at, outcome, COALESCE(project,''), COALESCE(role,''), COALESCE(binding,''),agent_started_at FROM runs WHERE id = ?`, runID).
		Scan(&subject, &ended, &recordedOutcome, &project, &role, &binding, &started); err != nil {
		if err == sql.ErrNoRows {
			return res, Errf(CodeStaleRun, "run %q does not exist", runID)
		}
		return res, err
	}
	if binding == "mechanical" {
		return res, Errf(CodeStaleRun, "The run belongs to a repository operation.")
	}
	var wikiUpdate int
	switch err := q.QueryRowContext(ctx, `SELECT 1 FROM wiki_updates WHERE run_id=?`, runID).Scan(&wikiUpdate); err {
	case nil:
		if outcome != "infra-failed" || reason != "" {
			return res, errors.New("a liveness stop of a wiki update run ends through EndWikiRun")
		}
		return finishWikiRunFailure(ctx, q, runID, project, report, recordedOutcome.String, ended.Valid || recordedOutcome.Valid, started.Valid)
	case sql.ErrNoRows:
	default:
		return res, err
	}
	var cancelled bool
	if subject.Valid {
		res.TaskID = subject.Int64
		if err := q.QueryRowContext(ctx, `SELECT dispatch_retries, decision IS 'cancelled' FROM tasks WHERE id = ?`, subject.Int64).
			Scan(&res.Remaining, &cancelled); err != nil {
			return res, err
		}
		res.Stalled = res.Remaining == 0
	}
	res.ProjectID = project
	res.Allowance = "task"
	projectFailure := !started.Valid || !subject.Valid && (role == "strategist" || role == "arbiter")
	if projectFailure {
		res.Allowance = "project"
		var count int
		if err := q.QueryRowContext(ctx, `SELECT infrastructure_failure_count FROM projects WHERE id=?`, project).Scan(&count); err != nil {
			return res, err
		}
		res.Remaining = 3 - count
		res.Stalled = count == 3
	}
	if ended.Valid || recordedOutcome.Valid {
		if charge {
			if err := appendLateEvidence(ctx, q, runID, project, recordedOutcome.String, report); err != nil {
				return res, err
			}
		}
		return res, nil
	}
	fencedSubject, err := Fence(ctx, q, runID)
	if err != nil {
		return res, err
	}
	if (fencedSubject == nil) != !subject.Valid || (fencedSubject != nil && *fencedSubject != subject.Int64) {
		return res, Errf(CodeStaleRun, "run %q has a different assigned task in its lease", runID)
	}
	if cancelled {
		if charge {
			outcome, reason = "cancelled", "cancelled"
		}
		charge = false
	} else if reason == "" {
		var system bool
		reason, system = runFailureReason(report)
		charge = charge && !system
	}
	if report.Cause == "" {
		report.Cause = "unknown"
	}
	before := 3 - res.Remaining
	if charge {
		if _, err := q.ExecContext(ctx, `INSERT INTO infrastructure_failures
            (run_id, task_id, allowance, condition, source, cause, evidence, operator_action, quota_reset_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, runID, subject, res.Allowance, report.Condition,
			report.Source, report.Cause, report.Evidence, report.OperatorAction, report.QuotaResetAt); err != nil {
			return res, err
		}
		if subject.Valid && !projectFailure {
			charged, err := ChargeInfra(ctx, q, subject.Int64, report.Condition, runID)
			if err != nil {
				return res, err
			}
			if _, err := q.ExecContext(ctx, `UPDATE tasks SET run_retry_at=? WHERE id=?`, taskRetryAt(charged.Remaining), subject.Int64); err != nil {
				return res, err
			}
			res.Remaining = charged.Remaining
			res.Stalled = charged.Stalled
		} else if projectFailure {
			charged, err := ChargeProjectInfrastructure(ctx, q, project, report.Condition, runID)
			if err != nil {
				return res, err
			}
			res.Remaining, res.Stalled = charged.Remaining, charged.Stalled
		}
		res.Charged = true
	}
	if outcome != "cancelled" && outcome != "interrupted" {
		if err := recordRunFailure(ctx, q, runID, project, reason, report, before, 3-res.Remaining); err != nil {
			return res, err
		}
		if reason == "agent" {
			if err := ClearNetworkRetry(ctx, q, runID); err != nil {
				return res, err
			}
		}
	}
	if _, err := q.ExecContext(ctx, `UPDATE runs SET ended_at = datetime('now'), outcome = ?, end_reason = ? WHERE id = ?`, outcome, reason, runID); err != nil {
		return res, err
	}
	return res, nil
}

// appendLateEvidence adds the evidence of a later report to a failed run. A successful run records no failure.
func appendLateEvidence(ctx context.Context, q Q, runID, project, outcome string, report InfrastructureFailureReport) error {
	if outcome != "infra-failed" && outcome != "reaped" {
		return nil
	}
	event := infrastructureEvidenceID(runID, report)
	var count int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM diagnostic_events WHERE event_id=?`, event).Scan(&count); err != nil || count > 0 {
		return err
	}
	_, err := AppendDiagnostic(ctx, q, Diagnostic{EventID: event, Source: report.Source, Operation: "run", Outcome: "failed", RunID: runID, ProjectID: project, Condition: report.Condition, Evidence: report.Evidence, HTTPStatus: report.HTTPStatus, ProviderErrorCode: report.ProviderErrorCode, NetworkError: report.NetworkError, StateBefore: outcome, StateAfter: outcome}, "")
	return err
}

// recordRunFailure appends the diagnostic of a failed run and applies the report to the connection state of the run.
func recordRunFailure(ctx context.Context, q Q, runID, project, reason string, report InfrastructureFailureReport, countBefore, countAfter int) error {
	var connection, authorization, profile, profileRevision string
	if err := q.QueryRowContext(ctx, `SELECT connection_id,authorization_id,profile_id,profile_revision FROM runs WHERE id=?`, runID).Scan(&connection, &authorization, &profile, &profileRevision); err != nil {
		return err
	}
	diagnostic, err := AppendDiagnostic(ctx, q, Diagnostic{EventID: infrastructureEvidenceID(runID, report), HTTPStatus: report.HTTPStatus, ProviderErrorCode: report.ProviderErrorCode, NetworkError: report.NetworkError, Source: report.Source, Operation: "run", Outcome: "failed", RunID: runID, ProjectID: project,
		ConnectionID: connection, AuthorizationID: authorization, ProfileID: profile, ProfileRevision: profileRevision,
		Condition: report.Condition, Evidence: report.Evidence, CountBefore: countBefore, CountAfter: countAfter}, "")
	if err != nil {
		return err
	}
	if err := recordRunConnectionFailure(ctx, q, runID, diagnostic, report); err != nil {
		return err
	}
	if reason == "network" && !report.TrustedProxy {
		return recordRunNetworkFailure(ctx, q, runID, diagnostic)
	}
	return nil
}

// finishWikiRunFailure ends the run of a wiki update and the wiki update as failed.
// It reads no task and no allowance, and it records no infrastructure failure.
// The cause of the wiki update names the class of the failure. The diagnostic holds the reported condition.
func finishWikiRunFailure(ctx context.Context, q Q, runID, project string, report InfrastructureFailureReport, recordedOutcome string, final, started bool) (InfrastructureFailureResult, error) {
	res := InfrastructureFailureResult{ProjectID: project, WikiUpdate: true}
	if final {
		return res, appendLateEvidence(ctx, q, runID, project, recordedOutcome, report)
	}
	if _, err := FenceAgentRun(ctx, q, runID); err != nil {
		return res, err
	}
	reason, _ := runFailureReason(report)
	if err := recordRunFailure(ctx, q, runID, project, reason, report, 0, 0); err != nil {
		return res, err
	}
	end := WikiRunEnd{Outcome: "infra-failed", EndReason: reason, Result: "failed"}
	switch reason {
	case "agent":
		// The failure report of the Scribe is its terminal action.
		end = WikiRunEnd{Outcome: "completed", Result: "failed", Cause: "The Scribe reported a failure: " + report.Condition}
	case "harness":
		end.Cause = "The agent program of the Scribe run stopped."
	case "network":
		end.Cause = "A network error stopped the Scribe run."
	case "provider":
		end.Cause = "A provider error stopped the Scribe run."
	case "connection":
		end.Cause = "The connection of the Scribe run failed."
		// A connection that changed between the start and the launch is no failure of the wiki update.
		// Each such end needs a new change of the connection state, so the count of these ends has a bound.
		if report.Cause == "connection" && !started && report.ConnectionRefused {
			end.Cause, end.NotCounted = "The connection changed or became unavailable before launch.", true
		}
	default:
		end.Cause = "The Scribe run did not start."
	}
	_, err := EndWikiRun(ctx, q, runID, end)
	return res, err
}

// AcknowledgeRunStop releases a final run's lease after the resident confirms its containers stopped.
// For the run of a wiki update it records the stop confirmation on the wiki update.
// A repeated acknowledgment cannot release another run's lease.
func AcknowledgeRunStop(ctx context.Context, q Q, runID string) error {
	var ended, outcome sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT ended_at, outcome FROM runs WHERE id = ?`, runID).Scan(&ended, &outcome); err != nil {
		if err == sql.ErrNoRows {
			return Errf(CodeStaleRun, "run %q does not exist", runID)
		}
		return err
	}
	if !ended.Valid || !outcome.Valid {
		return Errf(CodeStaleRun, "run %q has no final outcome", runID)
	}
	if _, err := q.ExecContext(ctx, `UPDATE wiki_updates SET stopped_at=datetime('now') WHERE run_id=? AND stopped_at IS NULL`, runID); err != nil {
		return err
	}
	var leases int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM leases WHERE run_id = ?`, runID).Scan(&leases); err != nil {
		return err
	}
	if leases == 0 {
		return nil
	}
	return Release(ctx, q, runID)
}

func infrastructureEvidenceID(run string, report InfrastructureFailureReport) string {
	if report.Cause == "" {
		report.Cause = "unknown"
	}
	body, _ := json.Marshal(report)
	sum := sha256.Sum256(append([]byte(run+"\n"), body...))
	return hex.EncodeToString(sum[:16])
}
