package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"mc/domain"
	"mc/operatorcmd"
)

const OperatorRequestLimit = 4 << 20

var operatorRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

type OperatorRequest struct {
	ID                 string           `json:"id"`
	Origin             string           `json:"origin"`
	SessionID          string           `json:"session_id,omitempty"`
	OperatorMessageSeq int64            `json:"operator_message_seq,omitempty"`
	Identity           *RunIdentity     `json:"identity,omitempty"`
	Permission         string           `json:"permission,omitempty"`
	Target             string           `json:"target,omitempty"`
	Targets            []OperatorTarget `json:"targets"`
	Command            []string         `json:"command"`
	Input              string           `json:"input,omitempty"`
	Release            string           `json:"release"`
	State              string           `json:"state"`
	CreatedAt          string           `json:"created_at"`
	StartedAt          *string          `json:"started_at,omitempty"`
	CompletedAt        *string          `json:"completed_at,omitempty"`
	ExitCode           *int             `json:"exit_code,omitempty"`
	Result             json.RawMessage  `json:"result,omitempty"`
	Error              string           `json:"error,omitempty"`
	Claimed            bool             `json:"claimed,omitempty"`
}

// Public removes inputs and the launch identity from an operation receipt.
func (r OperatorRequest) Public() OperatorRequest {
	r.Identity, r.Input, r.Permission, r.Claimed = nil, "", "", false
	return r
}

func readOperatorRequest(ctx context.Context, q Q, request string) (OperatorRequest, error) {
	var r OperatorRequest
	var command, identity string
	var result sql.NullString
	err := q.QueryRowContext(ctx, `SELECT id,origin,session_id,operator_message_seq,identity,permission,target,command,input,release,state,created_at,started_at,completed_at,exit_code,result,error FROM operator_requests WHERE id=?`, request).Scan(
		&r.ID, &r.Origin, &r.SessionID, &r.OperatorMessageSeq, &identity, &r.Permission, &r.Target, &command, &r.Input, &r.Release, &r.State, &r.CreatedAt, &r.StartedAt, &r.CompletedAt, &r.ExitCode, &result, &r.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return r, Domainf("The operator request does not exist.")
	}
	if err != nil {
		return r, err
	}
	if json.Unmarshal([]byte(command), &r.Command) != nil || json.Unmarshal([]byte(identity), &r.Identity) != nil {
		return r, Domainf("The operator request is invalid.")
	}
	if result.Valid {
		r.Result = json.RawMessage(result.String)
	}
	r.Targets = operatorRequestTargets(r)
	return r, nil
}

func authorizeOperatorRequest(ctx context.Context, q Q, r OperatorRequest) error {
	d, ok := operatorcmd.Lookup(r.Command)
	if !ok || d.Permission != r.Permission {
		return Domainf("The command is outside the supported operator surface.")
	}
	if err := AuthorizeOperator(ctx, q, r.Identity, r.Permission, r.Target); err != nil {
		return err
	}
	if r.Identity == nil {
		return nil
	}
	if r.Release != "" && r.Identity.ReleaseBuildID != "" && r.Identity.ReleaseBuildID != r.Release {
		return Domainf("The Collie launch and request use different releases.")
	}
	if len(r.Command) >= 2 && r.Command[0] == "collie" && r.Command[1] != "list" {
		if len(r.Command) < 3 || r.Command[2] != r.SessionID {
			return Domainf("Collie can change only its own session.")
		}
	}
	if r.Identity.Launch != "" {
		var launch sql.NullString
		if err := q.QueryRowContext(ctx, `SELECT current_launch_id FROM collie_sessions WHERE id=?`, r.SessionID).Scan(&launch); err != nil {
			return err
		}
		if !launch.Valid || launch.String != r.Identity.Launch {
			return Domainf("The Collie launch is no longer current.")
		}
	}
	if slices.Equal(d.Path, []string{"packet", "decide"}) {
		var packet sql.NullInt64
		if err := q.QueryRowContext(ctx, `SELECT packet_id FROM collie_sessions WHERE id=?`, r.SessionID).Scan(&packet); err != nil {
			return err
		}
		if packet.Valid {
			requested, err := strconv.ParseInt(operatorArgument(r.Command, "packet"), 10, 64)
			if err != nil || requested != packet.Int64 {
				return Domainf("This packet conversation belongs to another packet.")
			}
			var task, version int64
			if err := q.QueryRowContext(ctx, `SELECT task_id,version_id FROM review_versions WHERE id=?`, packet.Int64).Scan(&task, &version); err != nil {
				return err
			}
			requestedVersion, err := strconv.ParseInt(operatorArgument(r.Command, "version"), 10, 64)
			if err != nil || requestedVersion != version || len(r.Command) < 3 || r.Command[2] != strconv.FormatInt(task, 10) {
				return Domainf("The packet decision requires the reviewed task and version.")
			}
		}
	}
	return nil
}

func operatorArgument(args []string, name string) string {
	for i, arg := range args {
		if arg == "--"+name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, "--"+name+"=") {
			return strings.TrimPrefix(arg, "--"+name+"=")
		}
	}
	return ""
}

func operatorRequestTarget(args []string, input string) string {
	if len(args) >= 3 && args[0] == "project" && !strings.HasPrefix(args[2], "-") {
		return args[2]
	}
	if len(args) > 1 && args[0] == "system" && (args[1] == "resume-infrastructure" || args[1] == "resolve-proposals") && len(args) > 2 {
		return args[2]
	}
	if target := operatorArgument(args, "project"); target != "" {
		return target
	}
	if inline := operatorArgument(args, "request"); inline != "" && inline != "-" {
		input = inline
	}
	var body struct {
		ID        string `json:"id"`
		ProjectID string `json:"project_id"`
		Project   string `json:"project"`
	}
	if json.Unmarshal([]byte(input), &body) == nil {
		if body.ProjectID != "" {
			return body.ProjectID
		}
		if body.Project != "" {
			return body.Project
		}
		if len(args) > 0 && args[0] == "project" {
			return body.ID
		}
	}
	return ""
}

func operatorTaskTarget(ctx context.Context, q Q, args []string, input string) (string, error) {
	if len(args) >= 2 && args[0] == "project" && args[1] == "role-unblock" {
		if inline := operatorArgument(args, "request"); inline != "" && inline != "-" {
			input = inline
		}
		var body struct {
			Run string `json:"run"`
		}
		if json.Unmarshal([]byte(input), &body) == nil && body.Run != "" {
			var project string
			err := q.QueryRowContext(ctx, `SELECT project FROM runs WHERE id=?`, body.Run).Scan(&project)
			if errors.Is(err, sql.ErrNoRows) {
				return "", Domainf("This run has no active role block. Use the subject block controls for a task.")
			}
			return project, err
		}
	}
	var task int64
	if len(args) >= 3 && (args[0] == "task" || args[0] == "packet") {
		task, _ = strconv.ParseInt(args[2], 10, 64)
	}
	if len(args) >= 2 && args[0] == "board" && args[1] == "write" {
		if inline := operatorArgument(args, "request"); inline != "" && inline != "-" {
			input = inline
		}
		var body struct {
			TaskID int64 `json:"task_id"`
		}
		if json.Unmarshal([]byte(input), &body) == nil {
			task = body.TaskID
		}
	}
	if task <= 0 {
		return "", nil
	}
	var project string
	err := q.QueryRowContext(ctx, `SELECT project FROM tasks WHERE id=?`, task).Scan(&project)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.Errf(domain.CodeNotFound, "no task %d", task)
	}
	return project, err
}

// OperatorRequestSubmit derives identity and permission from authenticated inputs.
func OperatorRequestSubmit(db *sql.DB, id *RunIdentity, request string, args []string, input, release, origin string) (OperatorRequest, error) {
	var result OperatorRequest
	if !operatorRequestIDPattern.MatchString(request) || len(input) > OperatorRequestLimit || len(args) > 128 {
		return result, Usagef("The operator request exceeds its limits or has an invalid identifier.")
	}
	d, ok := operatorcmd.Lookup(args)
	if !ok {
		return result, Domainf("The command is outside the supported operator surface.")
	}
	if err := d.ValidateArguments(args); err != nil {
		return result, Usagef("%s", err)
	}
	validate := operatorcmd.ValidatePublicRequest
	if id == nil {
		validate = operatorcmd.ValidateHostRequest
	}
	if err := validate(args, []byte(input)); err != nil {
		return result, Usagef("%s", err)
	}
	if len(release) > 128 || release == "" {
		return result, Usagef("The operator request requires its release identity.")
	}
	for _, arg := range args {
		if strings.ContainsRune(arg, 0) || len(arg) > OperatorRequestLimit {
			return result, Usagef("An operator argument is invalid.")
		}
	}
	r := OperatorRequest{ID: request, Origin: origin, Command: args, Input: input, Release: release, Permission: d.Permission, Target: operatorRequestTarget(args, input)}
	if id != nil {
		if id.Tier != "collie" {
			return result, Domainf("Only Collie can submit an agent operator request.")
		}
		r.Origin, r.SessionID = "collie", id.RunID
		r.Identity = &RunIdentity{RunID: id.RunID, Tier: id.Tier, Launch: id.Launch, ReleaseBuildID: id.ReleaseBuildID, VerbAllowlist: append([]string{}, id.VerbAllowlist...)}
	} else if origin != "external" && origin != "dashboard" && origin != "component" {
		return result, Usagef("The operator request origin is invalid.")
	}
	command, _ := json.Marshal(args)
	identity, _ := json.Marshal(r.Identity)
	err := inTx(db, func(ctx context.Context, q Q) error {
		var found string
		if err := q.QueryRowContext(ctx, `SELECT id FROM operator_requests WHERE id=?`, request).Scan(&found); err == nil {
			prior, err := readOperatorRequest(ctx, q, request)
			if err != nil {
				return err
			}
			if prior.Origin != r.Origin || prior.SessionID != r.SessionID || !slices.Equal(prior.Command, args) || prior.Input != input || prior.Release != release {
				return Domainf("The request identifier already names different content.")
			}
			result = prior
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		project, err := operatorTaskTarget(ctx, q, args, input)
		if err != nil {
			return err
		}
		if project != "" {
			r.Target = project
		}
		if err := authorizeOperatorRequest(ctx, q, r); err != nil {
			return err
		}
		if id != nil {
			if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM conversation_messages WHERE session_id=? AND direction='inbound' AND claimed_by IS NOT NULL AND completed_at IS NULL`, id.RunID).Scan(&r.OperatorMessageSeq); err != nil {
				return err
			}
		}
		_, err = q.ExecContext(ctx, `INSERT INTO operator_requests(id,origin,session_id,operator_message_seq,identity,permission,target,command,input,release) VALUES(?,?,?,?,?,?,?,?,?,?)`, request, r.Origin, r.SessionID, r.OperatorMessageSeq, string(identity), r.Permission, r.Target, string(command), input, release)
		if err != nil {
			return err
		}
		result, err = readOperatorRequest(ctx, q, request)
		return err
	})
	return result, err
}

func OperatorRequestGet(db *sql.DB, id *RunIdentity, request string) (OperatorRequest, error) {
	r, err := readOperatorRequest(context.Background(), db, request)
	if err != nil {
		return r, err
	}
	if id != nil && (id.Tier != "collie" || id.RunID != r.SessionID) {
		return OperatorRequest{}, Domainf("This operator request belongs to another caller.")
	}
	return r, nil
}

func AuthorizeOperatorRead(db *sql.DB, id *RunIdentity, args []string, input string) error {
	d, ok := operatorcmd.Lookup(args)
	if !ok || d.Mutates {
		return Usagef("The request requires a supported read command.")
	}
	if err := d.ValidateArguments(args); err != nil {
		return Usagef("%s", err)
	}
	if id == nil {
		return nil
	}
	r := OperatorRequest{Identity: id, SessionID: id.RunID, Permission: d.Permission, Command: args, Target: operatorRequestTarget(args, input)}
	return authorizeOperatorRequest(context.Background(), db, r)
}

func OperatorRequestList(db *sql.DB, id *RunIdentity, pending bool) ([]OperatorRequest, error) {
	if pending {
		if err := RequireHostScope(id, "Operator request execution"); err != nil {
			return nil, err
		}
	}
	if id != nil && id.Tier != "collie" {
		return nil, Domainf("Operator requests require an operator identity.")
	}
	query := `SELECT id FROM operator_requests WHERE 1=1`
	values := []any{}
	if id != nil {
		query += ` AND session_id=?`
		values = append(values, id.RunID)
	}
	if pending {
		query += ` AND state='pending' ORDER BY created_at,id LIMIT 16`
	} else {
		query += ` ORDER BY created_at DESC,id DESC LIMIT 100`
	}
	rows, err := db.Query(query, values...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var request string
		if err := rows.Scan(&request); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, request)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := make([]OperatorRequest, 0, len(ids))
	for _, request := range ids {
		r, err := OperatorRequestGet(db, id, request)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, nil
}

func OperatorRequestClaim(db *sql.DB, id *RunIdentity, request string) (OperatorRequest, error) {
	if err := RequireHostScope(id, "Operator request execution"); err != nil {
		return OperatorRequest{}, err
	}
	var result OperatorRequest
	err := inTx(db, func(ctx context.Context, q Q) error {
		r, err := readOperatorRequest(ctx, q, request)
		if err != nil {
			return err
		}
		if r.State != "pending" {
			result = r
			return nil
		}
		if err := authorizeOperatorRequest(ctx, q, r); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE operator_requests SET state='active',started_at=datetime('now') WHERE id=? AND state='pending'`, request); err != nil {
			return err
		}
		result, err = readOperatorRequest(ctx, q, request)
		result.Claimed = err == nil
		return err
	})
	return result, err
}

func OperatorRequestAuthorize(db *sql.DB, id *RunIdentity, request string) (OperatorRequest, error) {
	if err := RequireHostScope(id, "Operator request execution"); err != nil {
		return OperatorRequest{}, err
	}
	r, err := readOperatorRequest(context.Background(), db, request)
	if err != nil {
		return r, err
	}
	if r.State != "active" {
		return r, Domainf("The operator request has no active execution.")
	}
	return r, authorizeOperatorRequest(context.Background(), db, r)
}

func OperatorRequestComplete(db *sql.DB, id *RunIdentity, request string, code int, result []byte, diagnostic string) error {
	if err := RequireHostScope(id, "Operator request completion"); err != nil {
		return err
	}
	if code < 0 || code > 2 || len(result) > OperatorRequestLimit || !json.Valid(result) {
		return Usagef("The operator result is invalid.")
	}
	if len(diagnostic) > 8192 {
		diagnostic = diagnostic[:8192]
	}
	return inTx(db, func(ctx context.Context, q Q) error {
		r, err := readOperatorRequest(ctx, q, request)
		if err != nil {
			return err
		}
		if r.State == "complete" {
			return nil
		}
		_, err = q.ExecContext(ctx, `UPDATE operator_requests SET state='complete',completed_at=datetime('now'),exit_code=?,result=?,error=? WHERE id=?`, code, string(result), diagnostic, request)
		return err
	})
}

// OperatorRequestRefuse retains a failed claim only while the request is pending.
func OperatorRequestRefuse(db *sql.DB, id *RunIdentity, request string, code int, output []byte, diagnostic string) (OperatorRequest, error) {
	var result OperatorRequest
	if err := RequireHostScope(id, "Operator request refusal"); err != nil {
		return result, err
	}
	if code < 1 || code > 2 || len(output) > OperatorRequestLimit || !json.Valid(output) {
		return result, Usagef("The operator refusal is invalid.")
	}
	if len(diagnostic) > 8192 {
		diagnostic = diagnostic[:8192]
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		r, err := readOperatorRequest(ctx, q, request)
		if err != nil {
			return err
		}
		if r.State != "pending" {
			result = r
			return nil
		}
		if _, err := q.ExecContext(ctx, `UPDATE operator_requests SET state='complete',completed_at=datetime('now'),exit_code=?,result=?,error=? WHERE id=? AND state='pending'`, code, string(output), diagnostic, request); err != nil {
			return err
		}
		result, err = readOperatorRequest(ctx, q, request)
		return err
	})
	return result, err
}

func OperatorRequestRecover(db *sql.DB, id *RunIdentity, request string) error {
	if err := RequireHostScope(id, "Operator request recovery"); err != nil {
		return err
	}
	return inTx(db, func(ctx context.Context, q Q) error {
		if request == "" {
			_, err := q.ExecContext(ctx, `UPDATE operator_requests SET state='uncertain',error='The host execution stopped without a final receipt. Inspect the target before another request.' WHERE state='active'`)
			return err
		}
		_, err := q.ExecContext(ctx, `UPDATE operator_requests SET state='uncertain',error='The host execution stopped without a final receipt. Inspect the target before another request.' WHERE id=? AND state='active'`, request)
		return err
	})
}

var currentOperatorRequest string
var currentOperatorCaller *RunIdentity

// WithOperatorCaller restores the original caller for an exact public command.
// The host supplies a receipt identifier instead of a caller identity.
func WithOperatorCaller(db *sql.DB, request string, fn func() int) (int, error) {
	if currentOperatorRequest != request {
		return 0, Domainf("The caller context requires its active operator request.")
	}
	id, err := LoadIdentity()
	if err != nil {
		return 0, err
	}
	if err = RequireHostScope(id, "Operator caller context"); err != nil {
		return 0, err
	}
	r, err := OperatorRequestAuthorize(db, nil, request)
	if err != nil {
		return 0, err
	}
	previous := currentOperatorCaller
	currentOperatorCaller = r.Identity
	defer func() { currentOperatorCaller = previous }()
	return fn(), nil
}

// WithOperatorRequest carries a host-admitted request through composite writes.
// Each transaction reads the receipt and checks its original caller again.
func WithOperatorRequest(request string, fn func() int) (int, error) {
	id, err := LoadIdentity()
	if err != nil {
		return 0, err
	}
	if err = RequireHostScope(id, "Operator request context"); err != nil {
		return 0, err
	}
	if !operatorRequestIDPattern.MatchString(request) {
		return 0, Usagef("The operator request identifier is invalid.")
	}
	previous := currentOperatorRequest
	currentOperatorRequest = request
	defer func() { currentOperatorRequest = previous }()
	return fn(), nil
}

func authorizeOperatorTransaction(ctx context.Context, q Q) error {
	if currentOperatorRequest == "" {
		return nil
	}
	r, err := readOperatorRequest(ctx, q, currentOperatorRequest)
	if err != nil {
		return err
	}
	if r.State != "active" {
		return Domainf("The operator request has no active execution.")
	}
	if err := authorizeOperatorRequest(ctx, q, r); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO activity(actor,kind,subject,detail) VALUES(?,'operator.request',?,?)`, r.Origin, r.ID, fmt.Sprintf("%s: %s", r.SessionID, strings.Join(r.Command[:min(2, len(r.Command))], " ")))
	return err
}
