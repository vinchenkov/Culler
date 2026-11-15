package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"mc/dispatch"
	"mc/domain"
	"mc/repository"
)

// The clocks of the delivery verbs. Tests replace both.
var (
	vmClock    = domain.ReadVMClock
	spineClock = spineNow
)

// StartRequest is the input of `mc delivery start`.
type StartRequest struct {
	Owner   int64    `json:"owner"`
	Running []string `json:"running"`
	Stopped []string `json:"stopped"`
	Drain   bool     `json:"drain"`
	// Vision holds the host check of each Project. The host broker adds it.
	Vision map[string]ProjectVisionCheck `json:"vision,omitempty"`
	// AcceptedCommits holds the host observation of the accepted commit of
	// each Project, by Project identifier. The host broker adds it.
	AcceptedCommits map[string]string `json:"accepted_commits,omitempty"`
}

// StartFailure is one start that failed in a start request. Its subject is
// one task or one wiki update.
type StartFailure struct {
	Project    string `json:"project"`
	TaskID     int64  `json:"task_id,omitempty"`
	WikiUpdate string `json:"wiki_update,omitempty"`
	Operation  string `json:"operation"`
	Recorded   bool   `json:"recorded"`
	RunID      string `json:"run_id,omitempty"`
	Code       string `json:"code"`
	Message    string `json:"message"`
}

// ExecutionStop is one ended execution without a stop confirmation.
type ExecutionStop struct {
	RunID     string `json:"run_id"`
	Project   string `json:"project"`
	Operation string `json:"operation"`
	Owner     int64  `json:"owner"`
	WaitMS    int64  `json:"wait_ms"`
}

// StartResponse is the output of `mc delivery start`.
type StartResponse struct {
	Owner        int64                        `json:"owner"`
	Executions   []map[string]json.RawMessage `json:"executions"`
	Stops        []ExecutionStop              `json:"stops"`
	Confirmed    []string                     `json:"confirmed"`
	Started      []string                     `json:"started"`
	Failed       []StartFailure               `json:"failed"`
	Published    []int64                      `json:"published"`
	DueInMS      *int64                       `json:"due_in_ms"`
	DrainPending bool                         `json:"drain_pending"`
}

// HeartbeatRequest is the input of `mc delivery heartbeat`.
type HeartbeatRequest struct {
	Owner      int64    `json:"owner"`
	Running    []string `json:"running"`
	Stopped    []string `json:"stopped"`
	Containers []string `json:"containers"`
}

// ContainerStop names an ended agent run whose containers the resident stops.
type ContainerStop struct {
	RunID   string `json:"run_id"`
	Project string `json:"project"`
}

// HeartbeatResponse is the output of `mc delivery heartbeat`.
type HeartbeatResponse struct {
	Renewed        []string        `json:"renewed"`
	Unstarted      []string        `json:"unstarted"`
	Stops          []ExecutionStop `json:"stops"`
	Confirmed      []string        `json:"confirmed"`
	Ended          []string        `json:"ended"`
	ContainerStops []ContainerStop `json:"container_stops"`
}

// DeliveryRegister records the owner number of a new resident instance.
func DeliveryRegister(db *sql.DB, id *RunIdentity) (any, error) {
	if err := RequireHostScope(id, "mc delivery register"); err != nil {
		return nil, err
	}
	var owner int64
	err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		owner, err = domain.RegisterOwner(ctx, q)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"owner": owner}, nil
}

// startProject is the state of one Project in a start request.
type startProject struct {
	id    string
	ready bool
	input dispatch.ProjectInput
}

// DeliveryStart records each due repository operation as an active execution
// and lists every active execution of the owner.
func DeliveryStart(db *sql.DB, id *RunIdentity, req StartRequest) (StartResponse, error) {
	resp := StartResponse{Owner: req.Owner}
	if err := RequireHostScope(id, "mc delivery start"); err != nil {
		return resp, err
	}
	if req.Owner < 1 {
		return resp, Usagef("the start request requires the owner number")
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		resp = StartResponse{Owner: req.Owner, Executions: []map[string]json.RawMessage{}, Stops: []ExecutionStop{},
			Confirmed: []string{}, Started: []string{}, Failed: []StartFailure{}, Published: []int64{}}
		if err := domain.RequireCurrentOwner(ctx, q, req.Owner); err != nil {
			return err
		}
		if err := recordStartWikiFacts(ctx, q, req.AcceptedCommits); err != nil {
			return err
		}
		now, err := spineClock(ctx, q)
		if err != nil {
			return err
		}
		tun, err := loadSettings(ctx, q)
		if err != nil {
			return err
		}
		clock, err := vmClock()
		if err != nil {
			return err
		}
		if err := domain.ApplyPendingTaskRevisions(ctx, q); err != nil {
			return err
		}
		if resp.Confirmed, err = domain.ConfirmStops(ctx, q, req.Owner, req.Stopped, clock, now); err != nil {
			return err
		}
		if _, err := domain.SweepExecutions(ctx, q, req.Owner, clock, now); err != nil {
			return err
		}
		projects, err := loadStartProjects(ctx, q, now, req.Vision)
		if err != nil {
			return err
		}
		if !req.Drain {
			cfg := dispatch.Config{ReviewWIPCap: tun.reviewWIPCap, TimeoutMinutes: tun.timeoutMinutes, GraceMinutes: tun.graceMinutes, SpawnGraceS: tun.spawnGraceS}
			for i := range projects {
				if err := publishReviews(ctx, q, &projects[i], cfg, &resp); err != nil {
					return err
				}
			}
		}
		inputs := make([]dispatch.ProjectInput, len(projects))
		for i, p := range projects {
			inputs[i] = p.input
		}
		changed := map[string]bool{}
		for n, op := range dispatch.SelectStarts(inputs, now, req.Drain) {
			changed[op.Project] = true
			if err := startOne(ctx, q, now, clock, req.Owner, n, op, &resp); err != nil {
				return err
			}
		}
		for i := range projects {
			if changed[projects[i].id] {
				if err := reloadStartProject(ctx, q, &projects[i]); err != nil {
					return err
				}
			}
			inputs[i] = projects[i].input
		}
		if due := dispatch.DueTime(inputs, now); due != nil {
			ms := due.Sub(now).Milliseconds()
			if ms < 1000 {
				ms = 1000
			}
			resp.DueInMS = &ms
		}
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM repository_executions WHERE ended_at IS NULL)
			OR EXISTS(SELECT 1 FROM merge_operations m JOIN tasks ON tasks.id=m.task_id WHERE m.operation='apply' AND m.finished=0
			AND tasks.blocked=0 AND tasks.dispatch_retries>0)
			OR EXISTS(SELECT 1 FROM merge_operations m WHERE m.wiki_update IS NOT NULL AND m.operation='apply' AND m.finished=0)`).Scan(&resp.DrainPending); err != nil {
			return err
		}
		if resp.Executions, err = ownerExecutions(ctx, q, req.Owner, clock, now); err != nil {
			return err
		}
		resp.Stops, err = executionStops(ctx, q, req.Owner, clock)
		return err
	})
	return resp, err
}

// recordStartWikiFacts stores the accepted commit observations of a start
// request. It then ends each active wiki update of an archived Project whose
// run has an end. Both steps run in one savepoint. An error rolls the
// savepoint back and the start request continues, so a wiki fact fails no
// start request. The next start request repeats both steps.
func recordStartWikiFacts(ctx context.Context, q Q, observed map[string]string) error {
	if _, err := q.ExecContext(ctx, `SAVEPOINT wiki_facts`); err != nil {
		return err
	}
	if err := applyStartWikiFacts(ctx, q, observed); err != nil {
		if _, rollback := q.ExecContext(ctx, `ROLLBACK TO wiki_facts`); rollback != nil {
			return rollback
		}
	}
	_, err := q.ExecContext(ctx, `RELEASE wiki_facts`)
	return err
}

// applyStartWikiFacts ignores an entry with an invalid commit and an entry
// of an unknown Project.
func applyStartWikiFacts(ctx context.Context, q Q, observed map[string]string) error {
	projects := make([]string, 0, len(observed))
	for project, commit := range observed {
		if repository.ValidCommit(commit) {
			projects = append(projects, project)
		}
	}
	sort.Strings(projects)
	for _, project := range projects {
		var known bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id = ?)`, project).Scan(&known); err != nil {
			return err
		}
		if !known {
			continue
		}
		if err := domain.RecordAcceptedCommit(ctx, q, project, observed[project]); err != nil {
			return err
		}
	}
	return domain.EndArchivedWikiUpdates(ctx, q)
}

// loadStartProjects records the Vision state of each Project and loads its records and its lease.
func loadStartProjects(ctx context.Context, q Q, now time.Time, vision map[string]ProjectVisionCheck) ([]startProject, error) {
	rows, err := q.QueryContext(ctx, `SELECT w.id,COALESCE(s.workspace_root,'') FROM projects w
		LEFT JOIN sandbox_profiles s ON s.id=w.sandbox_profile ORDER BY w.id`)
	if err != nil {
		return nil, err
	}
	type root struct{ id, path string }
	var roots []root
	for rows.Next() {
		var r root
		if err := rows.Scan(&r.id, &r.path); err != nil {
			rows.Close()
			return nil, err
		}
		roots = append(roots, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	projects := make([]startProject, 0, len(roots))
	for _, r := range roots {
		check, ok := vision[r.id]
		if !ok {
			check = CheckProjectVision(r.path)
		}
		p := startProject{id: r.id, ready: visionReady(check, r.path)}
		if _, err := q.ExecContext(ctx, `INSERT INTO vision_checks(project,ready,checked_at) VALUES(?,?,?)
			ON CONFLICT(project) DO UPDATE SET ready=excluded.ready,checked_at=excluded.checked_at`, r.id, p.ready, now.UTC().Format(spineTime)); err != nil {
			return nil, err
		}
		if p.input.Lease, err = loadLease(ctx, q, r.id); err != nil {
			return nil, err
		}
		if err := reloadStartProject(ctx, q, &p); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, nil
}

func reloadStartProject(ctx context.Context, q Q, p *startProject) error {
	rec, err := loadRecords(ctx, q, p.id)
	if err != nil {
		return err
	}
	rec.VisionReady = p.ready
	p.input.Records = rec
	p.input.Wiki, err = loadWikiMergeRecords(ctx, q, p.id)
	return err
}

// publishReviews publishes each review packet that the records admit. Each
// publication that fails leaves the other publications intact.
func publishReviews(ctx context.Context, q Q, p *startProject, cfg dispatch.Config, resp *StartResponse) error {
	if p.input.Records.ExpiryActive {
		return nil
	}
	published := false
	for _, task := range dispatch.ReviewPublications(p.input.Records, cfg) {
		name := fmt.Sprintf("publish_%d", task)
		if _, err := q.ExecContext(ctx, `SAVEPOINT `+name); err != nil {
			return err
		}
		if err := domain.PublishReview(ctx, q, task); err != nil {
			if _, rollback := q.ExecContext(ctx, `ROLLBACK TO `+name); rollback != nil {
				return rollback
			}
			code, message := startErrorCode(err)
			resp.Failed = append(resp.Failed, StartFailure{Project: p.id, TaskID: task, Operation: "publish-review", Code: code, Message: message})
		} else {
			resp.Published = append(resp.Published, task)
			published = true
		}
		if _, err := q.ExecContext(ctx, `RELEASE `+name); err != nil {
			return err
		}
	}
	if !published {
		return nil
	}
	return reloadStartProject(ctx, q, p)
}

// startOne starts one operation. A failure after the execution insert
// becomes a failed execution with a charge and a retry time.
func startOne(ctx context.Context, q Q, now time.Time, clock domain.VMClock, owner int64, n int, op dispatch.RepositoryOperation, resp *StartResponse) error {
	name := fmt.Sprintf("start_%d", n)
	if _, err := q.ExecContext(ctx, `SAVEPOINT `+name); err != nil {
		return err
	}
	run, err := startRepositoryOperation(ctx, q, clock, owner, op)
	if err == nil {
		resp.Started = append(resp.Started, run)
	} else {
		if _, rollback := q.ExecContext(ctx, `ROLLBACK TO `+name); rollback != nil {
			return rollback
		}
		code, message := startErrorCode(err)
		failure := StartFailure{Project: op.Project, TaskID: op.TaskID, WikiUpdate: op.WikiUpdate, Operation: string(op.Operation), Code: code, Message: message}
		var race *executionStartRace
		if !errors.As(err, &race) {
			failed := fmt.Sprintf("fail_%d", n)
			if _, err := q.ExecContext(ctx, `SAVEPOINT `+failed); err != nil {
				return err
			}
			if failure.RunID, err = recordFailedStart(ctx, q, now, clock, owner, op, message); err != nil {
				failure.RunID = ""
				if _, rollback := q.ExecContext(ctx, `ROLLBACK TO `+failed); rollback != nil {
					return rollback
				}
			} else {
				failure.Recorded = true
			}
			if _, err := q.ExecContext(ctx, `RELEASE `+failed); err != nil {
				return err
			}
		}
		resp.Failed = append(resp.Failed, failure)
	}
	_, err = q.ExecContext(ctx, `RELEASE `+name)
	return err
}

// recordFailedStart records a failed execution of the operation: the task
// receives a charge and the target receives a retry time. A wiki update
// receives no charge. A failed start of its capture, of its merge candidate
// step, or of its merge ends the wiki update as failed.
func recordFailedStart(ctx context.Context, q Q, now time.Time, clock domain.VMClock, owner int64, op dispatch.RepositoryOperation, condition string) (string, error) {
	run, err := newRunID()
	if err != nil {
		return "", err
	}
	key, subject := subjectField(op.Subject())
	body, err := json.Marshal(map[string]any{"run_id": run, "operation": string(op.Operation), key: subject, "project": op.Project})
	if err != nil {
		return "", err
	}
	if err := domain.StartExecution(ctx, q, domain.ExecutionStart{
		RunID: run, Project: op.Project, TaskID: op.TaskID, WikiUpdate: op.WikiUpdate, Operation: string(op.Operation),
		Target: op.Target(), Owner: owner, Effect: string(body), Deadline: op.Operation.Deadline(), Clock: clock,
	}); err != nil {
		return "", err
	}
	if _, err := domain.EndExecution(ctx, q, domain.ExecutionEnd{RunID: run, Reason: domain.EndFailure, Condition: condition, Stopped: true, Now: now}); err != nil {
		return run, err
	}
	// The end of the execution fails the wiki update for a capture and for a
	// merge candidate step. A merge that did not start left no merge
	// authorization, so no receipt recovery can decide the wiki update.
	if op.Subject().Wiki() && op.Operation == dispatch.OperationApply {
		err = domain.EndWikiUpdate(ctx, q, op.WikiUpdate, domain.WikiEnd{Result: "failed", Cause: wikiMergeCause + condition})
	}
	return run, err
}

// startErrorCode classifies a start failure as the CLI classifies a refused verb.
func startErrorCode(err error) (string, string) {
	err = classify(err)
	var usage *UsageError
	var refusal *DomainError
	if errors.As(err, &usage) {
		return "usage", err.Error()
	}
	if errors.As(err, &refusal) && refusal.Code != "" {
		return refusal.Code, err.Error()
	}
	return "domain-rejection", err.Error()
}

// ownerExecutions lists each active execution of the owner: the stored effect,
// the remaining time to the deadline, and for capture and cleanup the
// remaining grace of the ended target run.
func ownerExecutions(ctx context.Context, q Q, owner int64, clock domain.VMClock, now time.Time) ([]map[string]json.RawMessage, error) {
	rows, err := q.QueryContext(ctx, `SELECT x.operation,x.effect,x.deadline_uptime_ms,r.ended_at FROM repository_executions x
		LEFT JOIN runs r ON r.id=x.target WHERE x.owner=? AND x.ended_at IS NULL ORDER BY x.id`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]json.RawMessage{}
	for rows.Next() {
		var operation, effect string
		var deadline int64
		var targetEnded sql.NullString
		if err := rows.Scan(&operation, &effect, &deadline, &targetEnded); err != nil {
			return nil, err
		}
		entry := map[string]json.RawMessage{}
		if err := json.Unmarshal([]byte(effect), &entry); err != nil {
			return nil, err
		}
		entry["deadline_ms"] = rawInt(nonNegative(deadline - clock.Uptime.Milliseconds()))
		if operation == string(dispatch.OperationCapture) || operation == string(dispatch.OperationCleanup) {
			grace := domain.ContainerGrace.Milliseconds()
			if targetEnded.Valid {
				ended, err := parseSpineTime(targetEnded.String)
				if err != nil {
					return nil, err
				}
				grace = ended.Add(domain.ContainerGrace).Sub(now).Milliseconds()
			}
			entry["container_grace_ms"] = rawInt(nonNegative(grace))
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

// executionStops lists each ended execution without a stop confirmation. A
// merge container of an earlier owner on the current boot can run until its
// deadline, so its entry states the remaining wait.
func executionStops(ctx context.Context, q Q, owner int64, clock domain.VMClock) ([]ExecutionStop, error) {
	rows, err := q.QueryContext(ctx, `SELECT run_id,project,operation,owner,boot_id,deadline_uptime_ms FROM repository_executions
		WHERE ended_at IS NOT NULL AND stopped_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stops := []ExecutionStop{}
	for rows.Next() {
		var s ExecutionStop
		var boot string
		var deadline int64
		if err := rows.Scan(&s.RunID, &s.Project, &s.Operation, &s.Owner, &boot, &deadline); err != nil {
			return nil, err
		}
		merge := s.Operation == string(dispatch.OperationPrepare) || s.Operation == string(dispatch.OperationApply)
		if merge && s.Owner < owner && boot == clock.BootID {
			s.WaitMS = nonNegative(deadline - clock.Uptime.Milliseconds())
		}
		stops = append(stops, s)
	}
	return stops, rows.Err()
}

func nonNegative(n int64) int64 {
	if n < 0 {
		return 0
	}
	return n
}

func rawInt(n int64) json.RawMessage { return json.RawMessage(fmt.Sprint(n)) }

// DeliveryHeartbeat renews the executions of the owner, records the stop
// confirmations, and ends each execution that exceeds a liveness limit.
func DeliveryHeartbeat(db *sql.DB, id *RunIdentity, req HeartbeatRequest) (HeartbeatResponse, error) {
	var resp HeartbeatResponse
	if err := RequireHostScope(id, "mc delivery heartbeat"); err != nil {
		return resp, err
	}
	if req.Owner < 1 {
		return resp, Usagef("the heartbeat requires the owner number")
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		if err := domain.RequireCurrentOwner(ctx, q, req.Owner); err != nil {
			return err
		}
		now, err := spineClock(ctx, q)
		if err != nil {
			return err
		}
		clock, err := vmClock()
		if err != nil {
			return err
		}
		if resp.Confirmed, err = domain.ConfirmStops(ctx, q, req.Owner, req.Stopped, clock, now); err != nil {
			return err
		}
		if resp.Renewed, err = domain.HeartbeatExecutions(ctx, q, req.Owner, req.Running, clock); err != nil {
			return err
		}
		if resp.Ended, err = domain.SweepExecutions(ctx, q, req.Owner, clock, now); err != nil {
			return err
		}
		if resp.Unstarted, err = unstartedExecutions(ctx, q, req.Owner, req.Running); err != nil {
			return err
		}
		if resp.Stops, err = executionStops(ctx, q, req.Owner, clock); err != nil {
			return err
		}
		resp.ContainerStops, err = containerStops(ctx, q, req.Containers, now)
		return err
	})
	return resp, err
}

// unstartedExecutions lists each active execution of the owner that the
// resident does not run.
func unstartedExecutions(ctx context.Context, q Q, owner int64, running []string) ([]string, error) {
	named := map[string]bool{}
	for _, run := range running {
		named[run] = true
	}
	rows, err := q.QueryContext(ctx, `SELECT run_id FROM repository_executions WHERE owner=? AND ended_at IS NULL ORDER BY id`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var run string
		if err := rows.Scan(&run); err != nil {
			return nil, err
		}
		if !named[run] {
			out = append(out, run)
		}
	}
	return out, rows.Err()
}

// containerStops lists each listed container run that is an agent run and
// that ended at least one grace before now.
func containerStops(ctx context.Context, q Q, containers []string, now time.Time) ([]ContainerStop, error) {
	limit := now.Add(-domain.ContainerGrace).UTC().Format(spineTime)
	seen := map[string]bool{}
	out := []ContainerStop{}
	for _, run := range containers {
		if seen[run] {
			continue
		}
		seen[run] = true
		var project string
		err := q.QueryRowContext(ctx, `SELECT COALESCE(project,'') FROM runs WHERE id=? AND tier='pipeline'
			AND binding IS NOT 'mechanical' AND ended_at IS NOT NULL AND ended_at<=?`, run, limit).Scan(&project)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, ContainerStop{RunID: run, Project: project})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].RunID < out[j].RunID })
	return out, nil
}

// DeliveryFail ends an execution whose host work stopped with a failure.
func DeliveryFail(db *sql.DB, id *RunIdentity, run, condition string, containerPresent bool) (any, error) {
	if err := RequireHostScope(id, "mc delivery fail"); err != nil {
		return nil, err
	}
	if run == "" {
		return nil, Usagef("the failure report requires the execution run")
	}
	reason := domain.EndFailure
	if containerPresent {
		reason = domain.EndContainerPresent
	}
	var result domain.ExecutionEndResult
	err := inTx(db, func(ctx context.Context, q Q) error {
		now, err := spineClock(ctx, q)
		if err != nil {
			return err
		}
		result, err = domain.EndExecution(ctx, q, domain.ExecutionEnd{RunID: run, Reason: reason, Condition: condition, Stopped: true, Now: now})
		return err
	})
	if err != nil {
		return nil, err
	}
	var retry any
	if result.RetryAfter != nil {
		retry = result.RetryAfter.UTC().Format(spineTime)
	}
	// An execution of a wiki update has no allowance and no stall.
	if result.Subject().Wiki() {
		return map[string]any{"run_id": run, "wiki_update": result.WikiUpdate, "charged": result.Charged, "retry_after": retry}, nil
	}
	return map[string]any{"run_id": run, "task_id": result.TaskID, "charged": result.Charged,
		"retry_after": retry, "remaining": result.Remaining, "stalled": result.Stalled}, nil
}
