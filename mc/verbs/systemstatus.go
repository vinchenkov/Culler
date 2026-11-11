package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mc/domain"
)

type ConnectionRequirement struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Kind            string              `json:"kind"`
	AuthorizationID string              `json:"authorization_id"`
	Operation       string              `json:"operation"`
	ProfileID       string              `json:"profile_id"`
	ProfileRevision string              `json:"profile_revision"`
	Roles           []string            `json:"roles"`
	Profiles        map[string]string   `json:"profiles,omitempty"`
	ProfileRoles    map[string][]string `json:"profile_roles,omitempty"`
	ProfileModels   map[string]string   `json:"profile_models,omitempty"`
	ProfileNames    map[string]string   `json:"profile_names,omitempty"`
}

type SystemStatusRequest struct {
	Revision    string                  `json:"revision"`
	Connections []ConnectionRequirement `json:"connections"`
}

type ConnectionAttempt struct {
	RunID       string `json:"run_id,omitempty"`
	AttemptID   string `json:"attempt_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	Role        string `json:"role"`
	ProjectID   string `json:"project_id,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
	TaskID      int64  `json:"task_id,omitempty"`
	StartedAt   string `json:"started_at"`
	State       string `json:"state"`
}

type SystemAffectedTask struct {
	TaskID    int64  `json:"task_id"`
	Title     string `json:"title"`
	OwnsBlock bool   `json:"owns_block"`
}

type SystemCondition struct {
	Origin               string               `json:"origin,omitempty"`
	RequestID            string               `json:"request_id,omitempty"`
	AffectedTasks        []SystemAffectedTask `json:"affected_tasks,omitempty"`
	Stalled              bool                 `json:"stalled,omitempty"`
	Attempts             []ConnectionAttempt  `json:"attempts,omitempty"`
	Recovery             *ConnectionAttempt   `json:"recovery,omitempty"`
	ResolvedDiagnosticID int64                `json:"resolved_diagnostic_id,omitempty"`
	RetryPhase           string               `json:"retry_phase,omitempty"`

	ID                 string   `json:"id"`
	Group              string   `json:"group"`
	State              string   `json:"state"`
	ConnectionID       string   `json:"connection_id,omitempty"`
	ProfileID          string   `json:"profile_id,omitempty"`
	ProfileName        string   `json:"profile_name,omitempty"`
	Model              string   `json:"model,omitempty"`
	ProjectID          string   `json:"project_id,omitempty"`
	SessionID          string   `json:"session_id,omitempty"`
	MessageID          int64    `json:"message_id,omitempty"`
	AttemptID          string   `json:"attempt_id,omitempty"`
	RunID              string   `json:"run_id,omitempty"`
	TaskID             int64    `json:"task_id,omitempty"`
	ProjectPriority    bool     `json:"project_priority,omitempty"`
	AllowanceCondition string   `json:"allowance_condition,omitempty"`
	BlockedKind        string   `json:"blocked_kind,omitempty"`
	BlockDecisionID    int64    `json:"block_decision_id,omitempty"`
	Name               string   `json:"name"`
	Condition          string   `json:"condition"`
	Action             string   `json:"action"`
	FailureCount       int      `json:"failure_count"`
	FirstFailureAt     string   `json:"first_failure_at,omitempty"`
	LatestFailureAt    string   `json:"latest_failure_at,omitempty"`
	ProposalDecisionID int64    `json:"proposal_decision_id,omitempty"`
	NextRetryAt        string   `json:"next_retry_at,omitempty"`
	VerifiedAt         string   `json:"verified_at,omitempty"`
	DiagnosticID       int64    `json:"diagnostic_id,omitempty"`
	Revision           int64    `json:"revision"`
	HTTPStatus         *int     `json:"http_status,omitempty"`
	ProviderErrorCode  string   `json:"provider_error_code,omitempty"`
	NetworkError       string   `json:"network_error,omitempty"`
	Roles              []string `json:"roles,omitempty"`
	Projects           []string `json:"projects,omitempty"`
}

type SystemStatusResult struct {
	Complete     bool              `json:"complete"`
	ObservedAt   string            `json:"observed_at"`
	Revision     string            `json:"revision"`
	Conditions   []SystemCondition `json:"conditions"`
	Connections  []SystemCondition `json:"connections"`
	Availability any               `json:"availability,omitempty"`
}

func SystemStatus(db *sql.DB, id *RunIdentity, a SystemStatusRequest) (any, error) {
	if err := AuthorizeOperator(context.Background(), db, id, "", ""); err != nil {
		return nil, err
	}
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	out := SystemStatusResult{Complete: true, ObservedAt: now.Format(time.RFC3339Nano), Revision: a.Revision, Conditions: []SystemCondition{}, Connections: []SystemCondition{}}
	projectIDs := []string{}
	projectRows, err := tx.QueryContext(ctx, `SELECT id FROM projects WHERE status='active' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for projectRows.Next() {
		var key string
		if err := projectRows.Scan(&key); err != nil {
			projectRows.Close()
			return nil, err
		}
		projectIDs = append(projectIDs, key)
	}
	if err := projectRows.Err(); err != nil {
		projectRows.Close()
		return nil, err
	}
	projectRows.Close()
	seen := map[string]bool{}
	for _, required := range a.Connections {
		scopes := []ConnectionRequirement{required}
		rows, err := tx.QueryContext(ctx, `SELECT operation,profile_id,profile_revision FROM connection_health WHERE connection_id=? AND profile_id<>'' ORDER BY profile_id`, required.ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			other := required
			if err := rows.Scan(&other.Operation, &other.ProfileID, &other.ProfileRevision); err != nil {
				rows.Close()
				return nil, err
			}
			if required.Profiles != nil {
				var exists bool
				other.ProfileRevision, exists = required.Profiles[other.ProfileID]
				if !exists {
					continue
				}
			} else {
				other.ProfileRevision = required.ProfileRevision
			}
			if required.ProfileRoles != nil {
				other.Roles = required.ProfileRoles[other.ProfileID]
			}
			scopes = append(scopes, other)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, scope := range scopes {
			key := "connection:" + scope.ID + ":" + scope.Operation + ":" + scope.ProfileID
			if seen[key] {
				continue
			}
			seen[key] = true
			c := SystemCondition{ID: key, Group: "connections", ConnectionID: scope.ID, ProfileID: scope.ProfileID, Name: scope.Name, Roles: scope.Roles, State: "unknown", Condition: "Connection access awaits verification.", Action: "Review connection"}
			if scope.ProfileID != "" {
				c.Model = scope.ProfileModels[scope.ProfileID]
				c.ProfileName = scope.ProfileNames[scope.ProfileID]
			}
			for _, role := range scope.Roles {
				if role != "collie" {
					c.Projects = projectIDs
					break
				}
			}
			if scope.Operation == "token_refresh" {
				c.Action = "Reconnect"
			}
			var auth, state, profileRevision, diagnosticSubmission string
			var expires, deadline sql.NullString
			err := tx.QueryRowContext(ctx, `SELECT h.authorization_id,h.profile_revision,h.state,h.expires_at,h.revision,h.failure_count,
    COALESCE(h.first_failure_at,''),COALESCE(h.latest_failure_at,''),COALESCE(h.next_retry_at,''),COALESCE(h.verified_at,''),
    COALESCE(h.latest_diagnostic_id,0),COALESCE(d.condition,''),d.http_status,COALESCE(d.provider_error_code,''),COALESCE(d.network_error,''),h.request_deadline,COALESCE(d.submission,'')
    FROM connection_health h LEFT JOIN diagnostic_events d ON d.id=h.latest_diagnostic_id
    WHERE h.connection_id=? AND h.operation=? AND h.profile_id=?`, scope.ID, scope.Operation, scope.ProfileID).Scan(&auth, &profileRevision, &state, &expires, &c.Revision, &c.FailureCount, &c.FirstFailureAt, &c.LatestFailureAt, &c.NextRetryAt, &c.VerifiedAt, &c.DiagnosticID, &c.Condition, &c.HTTPStatus, &c.ProviderErrorCode, &c.NetworkError, &deadline, &diagnosticSubmission)
			if err != nil && err != sql.ErrNoRows {
				return nil, err
			}
			current := err == nil && auth == scope.AuthorizationID && (scope.ProfileID == "" || scope.ProfileRevision == profileRevision)
			if err == nil && scope.ProfileID != "" && state == "ready" && auth != scope.AuthorizationID && profileRevision == scope.ProfileRevision {
				continue
			}
			if current {
				c.State = state
				if domain.ConnectionEligible(state, expires, now) {
					c.State = "ready"
					c.Condition = "Connection access is verified."
				} else if state == "ready" {
					c.State = "unknown"
					c.Condition = "Connection access expired."
				}
			} else {
				c.State = "unknown"
				c.Condition = "Connection access awaits verification."
				c.FailureCount, c.DiagnosticID = 0, 0
				c.FirstFailureAt, c.LatestFailureAt, c.NextRetryAt, c.VerifiedAt = "", "", "", ""
				c.HTTPStatus, c.ProviderErrorCode, c.NetworkError = nil, "", ""
			}
			if current && deadline.Valid {
				end, e := time.Parse(time.RFC3339Nano, deadline.String)
				if e != nil || !now.Before(end) {
					c.State = "unknown"
					c.Condition = "The connection check ended without a recorded result."
				}
			}
			if c.ProviderErrorCode == "invalid_client" || c.ProviderErrorCode == "invalid_request" || c.ProviderErrorCode == "invalid_scope" || scope.ProfileID != "" {
				c.Action = "Review connection"
			}
			if c.State == "stopped" && scope.Operation == "token_refresh" {
				c.Action = tokenRefreshAction(c.HTTPStatus, c.ProviderErrorCode)
			}
			if current {
				var observation domain.ProviderAccessObservation
				if json.Unmarshal([]byte(diagnosticSubmission), &observation) == nil && observation.RequestID != "" {
					c.Origin, c.RequestID = observation.Origin, observation.RequestID
					if action := domain.ProviderRepairAction(diagnosticSubmission, scope.Kind); action != "" {
						c.Action = action
					}
				}
			}
			// An automatic retry requires no operator action.
			if c.State == "retry_wait" {
				c.RetryPhase = "pending"
				if at, err := time.Parse(time.RFC3339Nano, c.NextRetryAt); err == nil && now.Before(at) {
					c.RetryPhase = "delay"
				}
				c.Action = ""
			}
			if current && scope.ProfileID != "" {
				if err := connectionAttemptStatus(ctx, tx, scope, &c, now); err != nil {
					return nil, err
				}
			}
			out.Connections = append(out.Connections, c)
			if c.State != "ready" && (c.State != "unknown" || c.DiagnosticID != 0 || state == "stopped" || state == "retry_wait" || len(c.Roles) > 0) {
				out.Conditions = append(out.Conditions, c)
			}
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT w.id,w.title,w.infrastructure_failure_count,COALESCE(w.infrastructure_next_retry_at,''),COALESCE(w.infrastructure_last_failed_run_id,''),COALESCE(d.id,0),w.infrastructure_condition,COALESCE(d.observed_at,'') FROM projects w LEFT JOIN diagnostic_events d ON d.id=(SELECT MIN(e.id) FROM diagnostic_events e WHERE e.run_id=w.infrastructure_last_failed_run_id) WHERE w.infrastructure_failure_count>0 AND w.status<>'archived' ORDER BY w.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		c := SystemCondition{Group: "execution", Action: "View details", State: "retry_wait"}
		var reason string
		if err := rows.Scan(&c.ProjectID, &c.Name, &c.FailureCount, &c.NextRetryAt, &c.RunID, &c.DiagnosticID, &reason, &c.LatestFailureAt); err != nil {
			rows.Close()
			return nil, err
		}
		c.ID = "infrastructure:" + c.ProjectID
		c.Condition = fmt.Sprintf("Project infrastructure: %d counted failures.", c.FailureCount)
		if retry, err := time.Parse(time.RFC3339, c.NextRetryAt); c.NextRetryAt == "" || err == nil && !time.Now().UTC().Before(retry) {
			c.State = "ready"
		}
		if c.FailureCount == 3 {
			c.State = "stopped"
			c.Action = "Resume Project infrastructure"
			c.Condition = "The Project infrastructure allowance is exhausted."
		}
		if reason != "" {
			c.Condition += " " + reason
		}
		out.Conditions = append(out.Conditions, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	decisionConditions := map[int64]int{}
	rows, err = tx.QueryContext(ctx, `SELECT d.id,d.project,p.title,COALESCE(d.run_id,''),d.question FROM proposal_decisions d JOIN projects p ON p.id=d.project WHERE d.resolved_at IS NULL ORDER BY d.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		c := SystemCondition{Group: "execution", State: "stopped", Action: "Resolve proposal decision"}
		if err := rows.Scan(&c.ProposalDecisionID, &c.ProjectID, &c.Name, &c.RunID, &c.Condition); err != nil {
			rows.Close()
			return nil, err
		}
		c.ID = fmt.Sprintf("proposal-decision:%d", c.ProposalDecisionID)
		decisionConditions[c.ProposalDecisionID] = len(out.Conditions)
		out.Conditions = append(out.Conditions, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	blockDecisions := map[int64]int64{}
	rows, err = tx.QueryContext(ctx, `SELECT d.id,t.id,t.title,
 a.owns_block=1 AND t.blocked=1 AND t.blocked_kind='proposal' AND t.blocked_reason=d.question
 FROM proposal_decision_tasks a JOIN proposal_decisions d ON d.id=a.decision_id
 JOIN tasks t ON t.id=a.task_id WHERE d.resolved_at IS NULL AND t.archived=0 ORDER BY d.id,t.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var decision int64
		var task SystemAffectedTask
		if err := rows.Scan(&decision, &task.TaskID, &task.Title, &task.OwnsBlock); err != nil {
			rows.Close()
			return nil, err
		}
		index := decisionConditions[decision]
		out.Conditions[index].AffectedTasks = append(out.Conditions[index].AffectedTasks, task)
		if task.OwnsBlock {
			blockDecisions[task.TaskID] = decision
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT id,title,project,stalled,dispatch_retries,CASE WHEN blocked=1 THEN COALESCE(blocked_reason,'') ELSE '' END,
 CASE WHEN blocked=1 THEN COALESCE(blocked_kind,'') ELSE '' END,(decision IS 'approved'),
 COALESCE(allowance_condition,''),COALESCE(allowance_reason,'') FROM tasks WHERE archived=0 AND (stalled=1 OR blocked=1) ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		c := SystemCondition{Group: "execution", State: "stopped", Action: "Open task"}
		var stalled, retries int
		var allowanceReason string
		if err := rows.Scan(&c.TaskID, &c.Name, &c.ProjectID, &stalled, &retries, &c.Condition, &c.BlockedKind, &c.ProjectPriority, &c.AllowanceCondition, &allowanceReason); err != nil {
			rows.Close()
			return nil, err
		}
		if allowanceReason != "" && allowanceReason != c.Condition {
			c.Condition += " " + allowanceReason
		}
		c.ID = fmt.Sprintf("task:%d", c.TaskID)
		c.FailureCount = 3 - retries
		c.Stalled = stalled == 1
		c.BlockDecisionID = blockDecisions[c.TaskID]
		if stalled == 1 {
			c.Condition += " The task exhausted its task run failure allowance. Resume the task after you address the failed run."
		}
		out.Conditions = append(out.Conditions, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	wikiMerges, err := wikiMergeConditions(ctx, tx)
	if err != nil {
		return nil, err
	}
	out.Conditions = append(out.Conditions, wikiMerges...)
	rows, err = tx.QueryContext(ctx, `SELECT a.attempt_id,a.session_id,a.message_id,COALESCE(d.condition,'The reply attempt failed.'),COALESCE(a.latest_diagnostic_id,0)
 FROM collie_reply_attempts a LEFT JOIN diagnostic_events d ON d.id=a.latest_diagnostic_id JOIN collie_sessions s ON s.id=a.session_id
 WHERE s.deleted_at IS NULL AND a.state='failed' AND a.rowid=(SELECT MAX(b.rowid) FROM collie_reply_attempts b WHERE b.message_id=a.message_id) ORDER BY a.rowid`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		c := SystemCondition{Group: "execution", State: "failed", Name: "Collie", Action: "Retry reply"}
		if err := rows.Scan(&c.AttemptID, &c.SessionID, &c.MessageID, &c.Condition, &c.DiagnosticID); err != nil {
			rows.Close()
			return nil, err
		}
		c.ID = fmt.Sprintf("reply:%d", c.MessageID)
		out.Conditions = append(out.Conditions, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	services, err := domain.ProviderServiceConditions(ctx, tx, "")
	if err != nil {
		return nil, err
	}
	for _, service := range services {
		out.Conditions = append(out.Conditions, SystemCondition{ID: "provider-service:" + service.Origin + ":" + service.Connection,
			Group: "connections", State: "stopped", Origin: service.Origin, ConnectionID: service.Connection,
			Name: "Provider access", Condition: service.Condition, Action: service.Action, DiagnosticID: service.DiagnosticID,
			LatestFailureAt: service.ObservedAt})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// wikiMergeConditions returns one condition for each Project that is not
// archived, when one receipt recovery ended and the merge of the wiki commit
// still has no proven result. The action is the repair text of that merge.
// The repair text for a receipt recovery with no result states only a
// condition. The action then adds the delay before the next receipt recovery
// and the instruction for the operator.
// The dashboard shows the name as the notice title, so the name holds the
// Project title.
func wikiMergeConditions(ctx context.Context, q Q) ([]SystemCondition, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,title FROM projects WHERE status<>'archived' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var projects []SystemCondition
	for rows.Next() {
		c := SystemCondition{Group: "execution", State: "stopped", Condition: wikiStepTextReceipt}
		if err := rows.Scan(&c.ProjectID, &c.Name); err != nil {
			rows.Close()
			return nil, err
		}
		projects = append(projects, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var conditions []SystemCondition
	for _, c := range projects {
		u, err := loadWikiDelivery(ctx, q, c.ProjectID)
		if err != nil {
			return nil, err
		}
		if !wikiReceiptRecovery(u) || !u.RecoveryEnded {
			continue
		}
		c.ID = "wiki-merge:" + c.ProjectID
		c.Name += " · Project wiki"
		c.Action = u.Repair
		if strings.HasPrefix(u.Repair, wikiCheckText) {
			c.Action += fmt.Sprintf(" Culler repeats the check after %d seconds. If this condition stays, ask your coding harness to find the cause.",
				int(domain.RetryDelay.Seconds()))
		}
		conditions = append(conditions, c)
	}
	return conditions, nil
}

func DiagnosticHistory(db *sql.DB, id *RunIdentity, connection, run, attempt string, after int64, project ...string) (any, error) {
	if err := AuthorizeOperator(context.Background(), db, id, "", ""); err != nil {
		return nil, err
	}
	scope := ""
	if len(project) > 0 {
		scope = project[0]
	}
	rows, err := db.Query(`SELECT id,event_id,source,operation,outcome,connection_id,profile_id,project_id,run_id,reply_attempt_id,observed_at,recorded_at,http_status,provider_error_code,network_error,condition,evidence,state_before,state_after,count_before,count_after,COALESCE(json_extract(submission,'$.resolved_diagnostic_id'),0) AS resolved_diagnostic_id,
 json_extract(submission,'$.origin') AS origin,json_extract(submission,'$.request_id') AS request_id,
 json_extract(submission,'$.launch_id') AS launch_id,json_extract(submission,'$.credential_ref') AS credential_ref,
 json_extract(submission,'$.authorization_revision') AS authorization_revision,json_extract(submission,'$.provider_sent') AS provider_sent,
 json_extract(submission,'$.retry_after') AS retry_after,json_extract(submission,'$.duration_ms') AS duration_ms
 FROM diagnostic_events WHERE id>? AND (?='' OR connection_id=?) AND (?='' OR run_id=?) AND (?='' OR reply_attempt_id=?) AND (?='' OR project_id=?) ORDER BY id LIMIT 100`, after, connection, connection, run, run, attempt, attempt, scope, scope)
	if err != nil {
		return nil, err
	}
	events, err := rowsToMaps(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	counts := map[string]any{}
	for _, window := range []struct {
		name  string
		hours int
	}{{"day", 24}, {"week", 168}} {
		var requests, failures, rejections, transport int
		since := time.Now().UTC().Add(-time.Duration(window.hours) * time.Hour).Format(time.RFC3339Nano)
		if err := db.QueryRow(`SELECT COUNT(DISTINCT check_id),COUNT(DISTINCT CASE WHEN outcome='failed' THEN check_id END),COUNT(DISTINCT CASE WHEN outcome='failed' AND http_status>=400 THEN check_id END),COUNT(DISTINCT CASE WHEN outcome='failed' AND network_error<>'' THEN check_id END) FROM diagnostic_events WHERE operation='token_refresh' AND request_sent=1 AND observed_at>=? AND (?='' OR connection_id=?)`, since, connection, connection).Scan(&requests, &failures, &rejections, &transport); err != nil {
			return nil, err
		}
		counts[window.name] = map[string]int{"requests": requests, "failures": failures, "successful": requests - failures, "provider_rejections": rejections, "transport_failures": transport}
	}
	var cursor int64
	if len(events) > 0 {
		if value, ok := events[len(events)-1]["id"].(int64); ok {
			cursor = value
		}
	}
	return map[string]any{"events": events, "next_cursor": cursor, "refresh_outcomes": counts}, nil
}

func ProjectInfrastructureResume(db *sql.DB, id *RunIdentity, project, connection, profile string, expected ...ConnectionCheck) (any, error) {
	if err := RequireOperatorVerb(id, "system.resume-infrastructure"); err != nil {
		return nil, err
	}
	check := ConnectionCheck{}
	if len(expected) > 0 {
		check = expected[0]
		if !diagnosticIDPattern.MatchString(check.CheckID) {
			return nil, Usagef("The resume identity is invalid.")
		}
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		if err := AuthorizeOperator(ctx, q, id, "system.resume-infrastructure", project); err != nil {
			return err
		}
		if check.CheckID != "" {
			var recorded string
			err := q.QueryRowContext(ctx, `SELECT COALESCE(project_id,'') FROM diagnostic_events WHERE event_id=?`, check.CheckID).Scan(&recorded)
			if err == nil {
				if recorded != project {
					return Domainf("The resume identity belongs to another Project.")
				}
				return nil
			}
			if err != sql.ErrNoRows {
				return err
			}
		}
		if connection != "" {
			auth, _, err := ConnectionAdmission(ctx, q, connection, profile, time.Now(), check.ProfileRevision)
			if err != nil {
				return err
			}
			if check.AuthorizationID != "" && auth != check.AuthorizationID {
				return Domainf("The connection authorization changed.")
			}
		}
		return domain.ResetProjectInfrastructure(ctx, q, project, "operator", "", check.CheckID)
	})
	return map[string]any{"project_id": project, "resumed": err == nil}, err
}

func connectionAttemptStatus(ctx context.Context, q Q, scope ConnectionRequirement, c *SystemCondition, now time.Time) error {
	var recoveredRun, recoveredAttempt string
	if c.State == "ready" && c.DiagnosticID != 0 {
		if err := q.QueryRowContext(ctx, `SELECT COALESCE(d.run_id,''),COALESCE(d.reply_attempt_id,''),COALESCE(json_extract(d.submission,'$.resolved_diagnostic_id'),0),COALESCE(f.network_error,'') FROM diagnostic_events d LEFT JOIN diagnostic_events f ON f.id=json_extract(d.submission,'$.resolved_diagnostic_id') WHERE d.id=? AND d.operation='model_access' AND d.outcome='ready'`, c.DiagnosticID).Scan(&recoveredRun, &recoveredAttempt, &c.ResolvedDiagnosticID, &c.NetworkError); err != nil && err != sql.ErrNoRows {
			return err
		}
	}
	// A request failure can change the connection revision while its attempt remains active.
	// The lease holds the heartbeat of a run. A wiki update run holds no lease,
	// and its active wiki update holds its heartbeat.
	rows, err := q.QueryContext(ctx, `SELECT r.id,'','',r.role,COALESCE(r.project,''),COALESCE(p.title,''),COALESCE(r.subject,0),COALESCE(r.agent_started_at,r.created_at),
 CASE WHEN r.ended_at IS NOT NULL THEN r.outcome WHEN l.run_id IS NULL AND w.run_id IS NULL THEN 'stopping' WHEN COALESCE(l.last_heartbeat_at,w.last_heartbeat_at) IS NULL THEN 'starting' WHEN julianday(?) - julianday(COALESCE(l.last_heartbeat_at,w.last_heartbeat_at)) > (SELECT heartbeat_interval_s*3.0/86400 FROM settings LIMIT 1) THEN 'stale' ELSE 'active' END
 FROM runs r LEFT JOIN projects p ON p.id=r.project LEFT JOIN leases l ON l.run_id=r.id LEFT JOIN wiki_updates w ON w.run_id=r.id AND w.ended_at IS NULL
 WHERE r.connection_id=? AND r.authorization_id=? AND r.profile_id=? AND r.profile_revision=?
 AND ((r.ended_at IS NULL AND (r.connection_revision=(SELECT SUM(revision) FROM connection_health WHERE connection_id=r.connection_id AND (profile_id='' OR profile_id=r.profile_id)) OR r.id=(SELECT run_id FROM diagnostic_events WHERE id=?))) OR r.id=?)
 UNION ALL
 SELECT '',a.attempt_id,a.session_id,'collie','','',0,a.created_at,
 CASE WHEN a.state<>'active' THEN a.state WHEN s.status<>'active' OR a.launch_id<>COALESCE(s.current_launch_id,'') THEN 'stopping' ELSE 'active' END
 FROM collie_reply_attempts a JOIN collie_sessions s ON s.id=a.session_id
 WHERE a.connection_id=? AND a.authorization_id=? AND a.profile_id=? AND a.profile_revision=?
 AND ((a.state='active' AND (a.connection_revision=(SELECT SUM(revision) FROM connection_health WHERE connection_id=a.connection_id AND (profile_id='' OR profile_id=a.profile_id)) OR a.attempt_id=(SELECT reply_attempt_id FROM diagnostic_events WHERE id=?))) OR a.attempt_id=?)
 ORDER BY 8,1,2`, now.Format(time.RFC3339Nano), scope.ID, scope.AuthorizationID, scope.ProfileID, scope.ProfileRevision, c.DiagnosticID, recoveredRun, scope.ID, scope.AuthorizationID, scope.ProfileID, scope.ProfileRevision, c.DiagnosticID, recoveredAttempt)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a ConnectionAttempt
		if err := rows.Scan(&a.RunID, &a.AttemptID, &a.SessionID, &a.Role, &a.ProjectID, &a.ProjectName, &a.TaskID, &a.StartedAt, &a.State); err != nil {
			return err
		}
		if c.ResolvedDiagnosticID != 0 && ((a.RunID != "" && a.RunID == recoveredRun) || (a.AttemptID != "" && a.AttemptID == recoveredAttempt)) {
			copy := a
			c.Recovery = &copy
		}
		if c.State == "retry_wait" && (a.State == "active" || a.State == "starting" || a.State == "stale") {
			c.Attempts = append(c.Attempts, a)
		}
	}
	if c.State == "retry_wait" {
		c.RetryPhase = "pending"
		if at, err := time.Parse(time.RFC3339Nano, c.NextRetryAt); err == nil && now.Before(at) {
			c.RetryPhase = "delay"
		}
		if len(c.Attempts) > 0 {
			c.RetryPhase = "active"
		}
	}
	return rows.Err()
}
