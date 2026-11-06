package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"mc/dispatch"
	"mc/domain"
	"mc/refusal"
	"mc/substrate"
	"mc/wikitest"
)

// The fixture commits. The verbs layer reads no Git repository for the
// accepted commit, so each value is only a valid commit identifier.
var (
	wrCommitA = strings.Repeat("a", 40)
	wrCommitB = strings.Repeat("b", 40)
	wrCommitC = strings.Repeat("c", 40)
)

// wrObserver is a host observer with fixed answers. one answers the read
// before a commit step. It runs outside each transaction, so a test can
// change the spine in it.
type wrObserver struct {
	all map[string]string
	one func(project string) string
}

func (o wrObserver) All(_ context.Context, projects []string) map[string]string {
	observed := map[string]string{}
	for _, project := range projects {
		observed[project] = o.all[project]
	}
	return observed
}

func (o wrObserver) One(_ context.Context, project string) string {
	if o.one != nil {
		return o.one(project)
	}
	return o.all[project]
}

// wrSame observes the same commit in both steps of the round of each Project.
func wrSame(commits map[string]string) wrObserver { return wrObserver{all: commits} }

// wrRound runs the wiki round of one Project in process. first is the
// observation of the prepare step and second is the observation of the
// commit step.
func wrRound(t *testing.T, db *sql.DB, project, first, second string) map[string]any {
	t.Helper()
	effect, err := wrRoundErr(db, project, first, wrObserver{one: func(string) string { return second }})
	if err != nil {
		t.Fatalf("the wiki round of %s: %v", project, err)
	}
	return effect
}

func wrRoundErr(db *sql.DB, project, first string, observe AcceptedCommitObserver) (map[string]any, error) {
	home, err := resolveMCHome()
	if err != nil {
		return nil, err
	}
	uuid, err := readDeploymentMirrorStrict(home)
	if err != nil {
		return nil, err
	}
	return dispatchWikiRound(db, home, uuid, defaultDispatchProtocolIdentity, ResolveRuntimeCapability(home), project, first, observe)
}

// wrTick runs one complete tick with the observer.
func wrTick(t *testing.T, db *sql.DB, observe AcceptedCommitObserver) any {
	t.Helper()
	tick, err := dispatchWithIdentity(db, defaultDispatchProtocolIdentity, observe)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	return tick
}

// wrPrepare runs the prepare step of a wiki round with a host observation of
// the run containers.
func wrPrepare(t *testing.T, db *sql.DB, project, observation string, liveness []PrivatePipelineLiveness) preparedDispatch {
	t.Helper()
	home, err := resolveMCHome()
	if err != nil {
		t.Fatal(err)
	}
	requestID, err := newDispatchRequestID()
	if err != nil {
		t.Fatal(err)
	}
	var prepared preparedDispatch
	if err := underDispatchLock(db, func(ctx context.Context, q Q) error {
		var e error
		prepared, e = wikiPrepare(ctx, q, defaultDispatchProtocolIdentity, dfUUID(t, db), requestID, liveness,
			ResolveRuntimeCapability(home), project, observation)
		return e
	}); err != nil {
		t.Fatalf("the prepare step of the wiki round of %s: %v", project, err)
	}
	return prepared
}

// wrCommit runs the commit step of a wiki round.
func wrCommit(t *testing.T, db *sql.DB, prepared preparedDispatch, attested attestedDispatch, observation string) map[string]any {
	t.Helper()
	var effect map[string]any
	if err := underDispatchLock(db, func(ctx context.Context, q Q) error {
		var e error
		effect, e = wikiCommit(ctx, q, prepared, attested, observation)
		return e
	}); err != nil {
		t.Fatalf("the commit step of the wiki round: %v", err)
	}
	return effect
}

// wrAttest runs the host attest step of a prepared wiki candidate.
func wrAttest(t *testing.T, prepared preparedDispatch) attestedDispatch {
	t.Helper()
	if prepared.candidate == nil {
		t.Fatalf("the prepare step returned no candidate: %v", prepared.final)
	}
	attested, err := dispatchAttest(os.Getenv("MC_HOME"), prepared)
	if err != nil {
		t.Fatal(err)
	}
	return attested
}

// wrActive inserts an active wiki update of the Project with its Scribe run.
// Each duration is awake time on the current VM clock: the time since the
// start, the time since the heartbeat (nil for no heartbeat), and the time
// until the hard deadline. An empty boot is the current boot.
func wrActive(t *testing.T, db *sql.DB, project, run, commit, boot string, acquired time.Duration, heartbeat *time.Duration, deadline time.Duration) {
	t.Helper()
	vm, err := domain.ReadVMClock()
	if err != nil {
		t.Fatal(err)
	}
	if boot == "" {
		boot = vm.BootID
	}
	dvExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,session_path,binding)
		VALUES(?,'pipeline','scribe',?,NULL,?,'fake/fake')`, run, project, "sessions/"+run)
	dvExec(t, db, `INSERT INTO wiki_updates(project,run_id,start_commit,started_at,boot_id,acquired_uptime_ms,deadline_uptime_ms)
		VALUES(?,?,?,?,?,?,?)`, project, run, commit, dvNow(t, db).Format(spineTime), boot,
		(vm.Uptime - acquired).Milliseconds(), (vm.Uptime + deadline).Milliseconds())
	if heartbeat != nil {
		dvExec(t, db, `UPDATE wiki_updates SET last_heartbeat_at=?, heartbeat_uptime_ms=? WHERE run_id=?`,
			time.Now().Add(-*heartbeat).UTC().Format(spineTime), (vm.Uptime - *heartbeat).Milliseconds(), run)
	}
}

// wrFresh inserts an active wiki update that no liveness rule stops.
func wrFresh(t *testing.T, db *sql.DB, project, run, commit string) {
	t.Helper()
	wrActive(t, db, project, run, commit, "", 0, nil, time.Hour)
}

// wrEndRun ends the run of a wiki update.
func wrEndRun(t *testing.T, db *sql.DB, run string, end domain.WikiRunEnd) {
	t.Helper()
	if err := inTx(db, func(ctx context.Context, q Q) error {
		ended, err := domain.EndWikiRun(ctx, q, run, end)
		if err == nil && !ended {
			err = fmt.Errorf("the run %s had an end", run)
		}
		return err
	}); err != nil {
		t.Fatalf("end the run of the wiki update: %v", err)
	}
}

// wrStopped confirms the container stop of an ended wiki update run, as the
// stop confirmation of the resident does.
func wrStopped(t *testing.T, db *sql.DB, run string) {
	t.Helper()
	if _, err := RunStopped(db, nil, run); err != nil {
		t.Fatalf("confirm the stop of %s: %v", run, err)
	}
}

// wrLater moves the clock of the wiki round forward by d for the test.
func wrLater(t *testing.T, d time.Duration) {
	t.Helper()
	real := spineClock
	spineClock = func(ctx context.Context, q Q) (time.Time, error) {
		now, err := real(ctx, q)
		return now.Add(d), err
	}
	t.Cleanup(func() { spineClock = real })
}

// wrStart starts the next wiki update of the Project through the wiki round.
// It first moves the clock of the round past the interval of the last wiki update.
func wrStart(t *testing.T, db *sql.DB, project string) string {
	t.Helper()
	wrLater(t, 2*time.Hour)
	return wrWantSpawn(t, wrRound(t, db, project, wrCommitA, wrCommitA), project)
}

// wrReport sends the failure report of a wiki update run. A nil identity is the resident.
func wrReport(t *testing.T, db *sql.DB, id *RunIdentity, run, project string, report domain.InfrastructureFailureReport) {
	t.Helper()
	result, err := RunInfrastructureFailure(db, id, run, report)
	if err != nil {
		t.Fatalf("failure report of the run %s: %v", run, err)
	}
	if want := map[string]any{"run_id": run, "wiki_update": true, "charged": false, "project_id": project}; !reflect.DeepEqual(result, want) {
		t.Fatalf("result of the failure report = %v, want %v", result, want)
	}
}

// wrReadyConnection sets each health record of the Worker connection to ready.
func wrReadyConnection(t *testing.T, db *sql.DB) {
	t.Helper()
	dvExec(t, db, `UPDATE connection_health SET state='ready' WHERE connection_id=?`, testEndpointBinding().Credential)
}

// wrText reads one text value. A null value reads as "".
func wrText(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var value sql.NullString
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return value.String
}

// wrDump reads each row of the query as one line of text.
func wrDump(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i, value := range values {
			if raw, ok := value.([]byte); ok {
				value = string(raw)
			}
			fmt.Fprintf(&out, "%s=%v|", columns[i], value)
		}
		out.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// wrWikiRows counts the Scribe runs and the wiki updates of the Project.
func wrWikiRows(t *testing.T, db *sql.DB, project string) (runs, updates int64) {
	t.Helper()
	return dfInt(t, db, `SELECT COUNT(*) FROM runs WHERE role='scribe' AND project=?`, project),
		dfInt(t, db, `SELECT COUNT(*) FROM wiki_updates WHERE project=?`, project)
}

func wrWantIdle(t *testing.T, effect map[string]any, project, reason string) {
	t.Helper()
	if effect["action"] != "idle" || effect["project"] != project || effect["reason"] != reason {
		t.Fatalf("effect = %v, want idle %s for %s", effect, reason, project)
	}
}

func wrWantSpawn(t *testing.T, effect map[string]any, project string) string {
	t.Helper()
	run, _ := effect["run_id"].(string)
	if effect["action"] != "spawn" || effect["role"] != "scribe" || effect["project"] != project || !validLowercaseHex(run, 16) {
		t.Fatalf("effect = %v, want the spawn of a Scribe run for %s", effect, project)
	}
	return run
}

func wrWantReap(t *testing.T, effect map[string]any, project, run, reason string) {
	t.Helper()
	want := map[string]any{"action": "reap", "project": project, "reason": reason, "run_id": run, "stop_container": true}
	if fmt.Sprint(effect) != fmt.Sprint(want) {
		t.Fatalf("effect = %v, want %v", effect, want)
	}
}

// wrNewRun allocates a run identifier.
func wrNewRun(t *testing.T) string {
	t.Helper()
	run, err := newRunID()
	if err != nil {
		t.Fatal(err)
	}
	return run
}

// wrOverdue moves the heartbeat of an active wiki update run to a time before
// the heartbeat limit, so that the next round stops the run.
func wrOverdue(t *testing.T, db *sql.DB, run string) {
	t.Helper()
	vm, err := domain.ReadVMClock()
	if err != nil {
		t.Fatal(err)
	}
	age := 2 * time.Hour
	dvExec(t, db, `UPDATE wiki_updates SET last_heartbeat_at=?, heartbeat_uptime_ms=? WHERE run_id=?`,
		time.Now().Add(-age).UTC().Format(spineTime), (vm.Uptime - age).Milliseconds(), run)
}

// wrStoredCommit reads the stored accepted commit of the Project, or "".
func wrStoredCommit(t *testing.T, db *sql.DB, project string) string {
	t.Helper()
	return wrText(t, db, `SELECT (SELECT accepted_commit FROM accepted_commits WHERE project=?)`, project)
}

// wrRecords counts the start records and the end records of the wiki update
// of the run.
func wrRecords(t *testing.T, db *sql.DB, run, project string) (started, ended int64) {
	t.Helper()
	return dfInt(t, db, `SELECT COUNT(*) FROM activity WHERE kind='wiki.update_started' AND subject=? AND project=?`, run, project),
		dfInt(t, db, `SELECT COUNT(*) FROM activity WHERE kind='wiki.update_ended' AND subject=? AND project=?`, run, project)
}

// wrRunEnd reads the end of a wiki update run and of its wiki update as one line.
func wrRunEnd(t *testing.T, db *sql.DB, run string) string {
	t.Helper()
	return wrDump(t, db, `SELECT r.outcome, r.end_reason, w.result, w.cause, w.counted, w.stopped_at IS NOT NULL AS stopped
		FROM wiki_updates w JOIN runs r ON r.id=w.run_id WHERE w.run_id=?`, run)
}

// wrEndLine is the line of wrRunEnd for a failed wiki update. An empty end
// reason is a null value.
func wrEndLine(outcome, endReason, cause string, counted, stopped bool) string {
	reason := any(endReason)
	if endReason == "" {
		reason = nil
	}
	flag := map[bool]int{true: 1}
	return fmt.Sprintf("outcome=%s|end_reason=%v|result=failed|cause=%s|counted=%d|stopped=%d|\n",
		outcome, reason, cause, flag[counted], flag[stopped])
}

// wrScribeSpawns returns the Scribe spawns of a tick in the order of its actions.
func wrScribeSpawns(t *testing.T, raw any) []map[string]any {
	t.Helper()
	var spawns []map[string]any
	for _, action := range dvTickActions(t, raw) {
		if action["action"] == "spawn" && action["role"] == "scribe" {
			spawns = append(spawns, action)
		}
	}
	return spawns
}

// wrTickErrors returns the message of each error envelope of a tick.
func wrTickErrors(t *testing.T, raw any) []string {
	t.Helper()
	var messages []string
	for _, action := range dvTickActions(t, raw) {
		if envelope, ok := action["error"].(map[string]any); ok {
			messages = append(messages, fmt.Sprint(envelope["message"]))
		}
	}
	return messages
}

// wrRemoveRouting removes the routing table. The attest step then refuses
// each candidate with a refusal of the health class.
func wrRemoveRouting(t *testing.T) {
	t.Helper()
	if err := os.Remove(filepath.Join(os.Getenv("MC_HOME"), "routing.md")); err != nil {
		t.Fatal(err)
	}
}

// wrRefuse applies one refusal to a new wiki candidate of the Project. The
// number gives the refusal its own dispatch key and request identifier.
func wrRefuse(t *testing.T, db *sql.DB, project string, number int, r refusal.Refusal) (map[string]any, string) {
	t.Helper()
	run := wrNewRun(t)
	cand := RefusalCandidate{Kind: RefusalWikiUpdate, Project: project, WikiStart: domain.WikiStart{
		Project: project, RunID: run, StartCommit: wrCommitA, SessionPath: "sessions/" + run,
		Binding: "claude-sdk/endpoint", HardDeadlineMinutes: 240,
	}}
	var effect map[string]any
	if err := inTx(db, func(ctx context.Context, q Q) error {
		now, err := spineClock(ctx, q)
		if err != nil {
			return err
		}
		effect, err = applyWikiRefusal(ctx, q, now, cand, r, rrTickKey(number), fmt.Sprintf("%016x", number))
		return err
	}); err != nil {
		t.Fatalf("refuse the wiki candidate of %s: %v", project, err)
	}
	return effect, run
}

// The refusals of the three classes.
var (
	wrStaleRefusal     = refusal.Refusal{Code: refusal.CodeStale, Field: refusal.FieldNone, Summary: refusal.SummaryMismatch}
	wrHealthRefusal    = refusal.Refusal{Code: refusal.CodeRoutingInvalid, Field: refusal.FieldRouting, Summary: refusal.SummaryUnparsable}
	wrCandidateRefusal = refusal.Refusal{Code: refusal.CodeEnvInvalid, Field: refusal.FieldEnvName, Summary: refusal.SummaryForbidden}
)

const (
	wrHealthCause    = "Culler refused the start of the Scribe run. The Culler deployment is not ready (health.routing_invalid)."
	wrCandidateCause = "Culler refused the start of the Scribe run. The configuration of the Scribe role or of the Project is not valid (env.invalid)."
)

// wrStop is one stop rule of a wiki update run with no end.
type wrStop struct {
	reason, outcome, cause string
	// archived states that the rule needs an archived Project.
	archived bool
	// absent states that the host observed no container of the run.
	absent bool
	// start inserts the active wiki update that the rule stops.
	start func(t *testing.T, db *sql.DB, project, run string)
}

var wrStops = []wrStop{
	{reason: "spawn-watchdog", outcome: "reaped", cause: "The Scribe run did not start in time.",
		start: func(t *testing.T, db *sql.DB, project, run string) {
			wrActive(t, db, project, run, wrCommitA, "", 2*time.Minute, nil, time.Hour)
		}},
	{reason: "lease-timeout", outcome: "reaped", cause: "The heartbeat of the Scribe run stopped.",
		start: func(t *testing.T, db *sql.DB, project, run string) {
			beat := 2 * time.Hour
			wrActive(t, db, project, run, wrCommitA, "", 3*time.Hour, &beat, time.Hour)
		}},
	{reason: "hard-deadline", outcome: "reaped", cause: "The Scribe run reached its time limit.",
		start: func(t *testing.T, db *sql.DB, project, run string) {
			beat := time.Second
			wrActive(t, db, project, run, wrCommitA, "", time.Hour, &beat, -time.Minute)
		}},
	{reason: "container-gone", outcome: "reaped", cause: "The container of the Scribe run stopped.", absent: true,
		start: func(t *testing.T, db *sql.DB, project, run string) {
			beat := time.Second
			wrActive(t, db, project, run, wrCommitA, "", 5*time.Minute, &beat, time.Hour)
		}},
	{reason: "vm-restart", outcome: "reaped", cause: "Docker restarted during the Scribe run.",
		start: func(t *testing.T, db *sql.DB, project, run string) {
			wrActive(t, db, project, run, wrCommitA, "an-earlier-boot", 0, nil, time.Hour)
		}},
	{reason: "interrupted", outcome: "interrupted", cause: "The Project is archived.", archived: true,
		start: func(t *testing.T, db *sql.DB, project, run string) {
			wrFresh(t, db, project, run, wrCommitA)
		}},
}

// stop starts the wiki update of the rule and runs the prepare step that
// stops its run. It returns the run and the effect of the step.
func (s wrStop) stop(t *testing.T, db *sql.DB, project string) (string, map[string]any) {
	t.Helper()
	run := wrNewRun(t)
	s.start(t, db, project, run)
	var liveness []PrivatePipelineLiveness
	if s.absent {
		liveness = []PrivatePipelineLiveness{{LaunchAgeS: 120, Present: false, RunID: run}}
	}
	prepared := wrPrepare(t, db, project, wrCommitA, liveness)
	if prepared.candidate != nil {
		t.Fatalf("%s: the prepare step returned a candidate, want the stop of run %s", s.reason, run)
	}
	return run, prepared.final
}

func TestWikiRoundStartsADueWikiUpdate(t *testing.T) {
	db := dvSpine(t)
	leases := wrDump(t, db, `SELECT * FROM leases ORDER BY project`)

	effect := wrRound(t, db, "ws-test", wrCommitA, wrCommitA)
	run := wrWantSpawn(t, effect, "ws-test")

	if got := wrDump(t, db, `SELECT id, tier, role, project, subject, session_path, ended_at, outcome FROM runs`); got !=
		fmt.Sprintf("id=%s|tier=pipeline|role=scribe|project=ws-test|subject=<nil>|session_path=sessions/%s|ended_at=<nil>|outcome=<nil>|\n", run, run) {
		t.Fatalf("runs = %s, want one active Scribe run with no subject", got)
	}
	if got := wrDump(t, db, `SELECT project, run_id, start_commit, wiki_commit, ended_at, result, counted, stopped_at FROM wiki_updates`); got !=
		fmt.Sprintf("project=ws-test|run_id=%s|start_commit=%s|wiki_commit=<nil>|ended_at=<nil>|result=<nil>|counted=1|stopped_at=<nil>|\n", run, wrCommitA) {
		t.Fatalf("wiki_updates = %s, want one active wiki update at the start commit", got)
	}
	if got := wrDump(t, db, `SELECT * FROM leases ORDER BY project`); got != leases {
		t.Fatalf("the start of a wiki update changed a lease:\n%s\nbefore:\n%s", got, leases)
	}
	if got := wrText(t, db, `SELECT role FROM run_customizations WHERE run_id=?`, run); got != "scribe" {
		t.Fatalf("customization role of the run = %q, want scribe", got)
	}

	var keys []string
	for key := range effect {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if got, want := strings.Join(keys, " "), "action brief git_identity_override harness heartbeat_interval_s model_binding mount_plan pool_ids project role run_id runtime_binding session_path skill_store_dir skills subject_id"; got != want {
		t.Fatalf("spawn keys = %s\nwant         %s", got, want)
	}
	if effect["subject_id"] != nil || fmt.Sprint(effect["pool_ids"]) != "[]" || effect["session_path"] != "sessions/"+run {
		t.Fatalf("spawn = %v, want no subject, no pool, and the session of the run", effect)
	}
	planJSON, err := json.Marshal(effect["mount_plan"])
	if err != nil {
		t.Fatal(err)
	}
	var plan PrivateDispatchMountPlan
	if err := json.Unmarshal(planJSON, &plan); err != nil {
		t.Fatal(err)
	}
	root := wrText(t, db, `SELECT workspace_root FROM sandbox_profiles WHERE id='default'`)
	limit := dfInt(t, db, `SELECT max_temporary_bytes FROM repository_limits WHERE project='ws-test'`)
	toolHome := wrText(t, db, `SELECT tool_home_dir FROM sandbox_profiles WHERE id='default'`)
	if plan.Version != 1 || len(plan.Entries) != 0 || len(plan.SeedingProjections) != 0 ||
		plan.Repository == nil || plan.Repository.DestinationBranch != "main" || plan.Repository.InputCommit != wrCommitA ||
		plan.Repository.MaxTemporaryBytes != int64(limit) || plan.Repository.WikiUpdate != run || plan.Repository.WorkspaceRoot != root ||
		plan.ToolHome == nil || plan.ToolHome.Canonical != toolHome || plan.JurisdictionDigest == "" {
		t.Fatalf("mount plan = %s, want the wiki repository and Project home", planJSON)
	}
	brief, _ := effect["brief"].(string)
	if !strings.Contains(brief, wrCommitA) {
		t.Fatal("the run brief does not hold the start commit")
	}
}

func TestWikiRoundWithNoObservationStartsNothing(t *testing.T) {
	db := dvSpine(t)
	// The spine holds an accepted commit and no wiki update: the wiki is behind.
	if err := inTx(db, func(ctx context.Context, q Q) error {
		return domain.RecordAcceptedCommit(ctx, q, "ws-test", wrCommitA)
	}); err != nil {
		t.Fatal(err)
	}
	for tick := range 3 {
		raw, err := Dispatch(db)
		if err != nil {
			t.Fatalf("tick %d: %v", tick, err)
		}
		if messages := wrTickErrors(t, raw); len(messages) != 0 || len(wrScribeSpawns(t, raw)) != 0 {
			t.Fatalf("tick %d = %v, want no error and no Scribe spawn", tick, raw)
		}
		idle := dvTickIdle(t, raw)
		if len(idle) > 1 || strings.HasPrefix(idle["ws-test"], "wiki-") {
			t.Fatalf("tick %d idle entries = %v, want no entry of the wiki round", tick, idle)
		}
	}
	if runs, updates := wrWikiRows(t, db, "ws-test"); runs != 0 || updates != 0 {
		t.Fatalf("%d Scribe runs and %d wiki updates after three ticks with no observation, want none", runs, updates)
	}
	wrWantIdle(t, wrRound(t, db, "ws-test", "", ""), "ws-test", "wiki-commit-unknown")
	if got := wrStoredCommit(t, db, "ws-test"); got != wrCommitA {
		t.Fatalf("stored accepted commit = %q, want the value before the ticks", got)
	}
}

func TestHelperLeavesTheWikiCurrent(t *testing.T) {
	db := dvSpine(t)
	run, recorded, err := wikitest.RecordCurrentWiki(context.Background(), db, "ws-test", wrCommitA, time.Now().Add(-2*time.Hour))
	if err != nil || !recorded {
		t.Fatalf("record a current wiki = (%q, %v, %v)", run, recorded, err)
	}
	observe := wrSame(map[string]string{"ws-test": wrCommitA})
	for tick := range 3 {
		raw := wrTick(t, db, observe)
		if len(wrTickErrors(t, raw)) != 0 || len(wrScribeSpawns(t, raw)) != 0 {
			t.Fatalf("tick %d = %v, want no error and no Scribe spawn", tick, raw)
		}
		for _, action := range dvTickActions(t, raw) {
			if action["run_id"] == run {
				t.Fatalf("tick %d holds an action for the run of the helper: %v", tick, action)
			}
		}
	}
	if runs, updates := wrWikiRows(t, db, "ws-test"); runs != 1 || updates != 1 {
		t.Fatalf("%d Scribe runs and %d wiki updates, want only the rows of the helper", runs, updates)
	}
	wrWantIdle(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test", "wiki-current")

	// A new commit on main makes the wiki behind, and the interval since the helper start passed.
	wrWantSpawn(t, wrRound(t, db, "ws-test", wrCommitB, wrCommitB), "ws-test")
}

func TestPausedProjectGetsNoWikiUpdateUntilResume(t *testing.T) {
	db := dvSpine(t)
	if _, err := ProjectSetStatus(db, nil, "ws-test", "paused"); err != nil {
		t.Fatal(err)
	}
	observe := wrSame(map[string]string{"ws-test": wrCommitA})
	for tick := range 2 {
		raw := wrTick(t, db, observe)
		if actions := dvTickActions(t, raw); len(actions) != 0 {
			t.Fatalf("tick %d of a paused Project = %v, want no action", tick, actions)
		}
		if runs, updates := wrWikiRows(t, db, "ws-test"); runs != 0 || updates != 0 {
			t.Fatalf("tick %d: %d Scribe runs and %d wiki updates of a paused Project, want none", tick, runs, updates)
		}
	}
	wrWantIdle(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test", "wiki-project-inactive")

	if _, err := ProjectSetStatus(db, nil, "ws-test", "active"); err != nil {
		t.Fatal(err)
	}
	spawns := wrScribeSpawns(t, wrTick(t, db, observe))
	if len(spawns) != 1 {
		t.Fatalf("the first tick after the resume holds %d Scribe spawns, want one", len(spawns))
	}
	run := wrWantSpawn(t, spawns[0], "ws-test")
	if got := wrDump(t, db, `SELECT run_id, start_commit, ended_at FROM wiki_updates WHERE project='ws-test'`); got !=
		fmt.Sprintf("run_id=%s|start_commit=%s|ended_at=<nil>|\n", run, wrCommitA) {
		t.Fatalf("wiki_updates after the resume = %s, want one active wiki update", got)
	}
}

func TestWikiRoundRequiresThreeEqualCommits(t *testing.T) {
	db := dvSpine(t)
	// store is a request of another process between the two steps of the round.
	store := func(commit string) {
		if err := inTx(db, func(ctx context.Context, q Q) error {
			return domain.RecordAcceptedCommit(ctx, q, "ws-test", commit)
		}); err != nil {
			t.Error(err)
		}
	}
	cases := []struct {
		name string
		// between runs after the prepare step and returns the observation of the commit step.
		between func() string
		stored  string
	}{
		{"main moved after the prepare step", func() string { return wrCommitB }, wrCommitB},
		{"the commit observation differs from the start commit", func() string { store(wrCommitB); return wrCommitB }, wrCommitB},
		{"the stored value differs", func() string { store(wrCommitC); return wrCommitA }, wrCommitA},
		{"the commit step has no observation", func() string { return "" }, wrCommitA},
	}
	for _, c := range cases {
		effect, err := wrRoundErr(db, "ws-test", wrCommitA, wrObserver{one: func(string) string { return c.between() }})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		wrWantIdle(t, effect, "ws-test", idleWikiObservationChanged)
		if runs, updates := wrWikiRows(t, db, "ws-test"); runs != 0 || updates != 0 {
			t.Fatalf("%s: %d Scribe runs and %d wiki updates, want none", c.name, runs, updates)
		}
		if got := wrStoredCommit(t, db, "ws-test"); got != c.stored {
			t.Fatalf("%s: stored accepted commit = %q, want %q", c.name, got, c.stored)
		}
		if n := dfInt(t, db, `SELECT COUNT(*) FROM activity WHERE kind LIKE 'wiki.%'`); n != 0 {
			t.Fatalf("%s: the round wrote %d wiki records, want none", c.name, n)
		}
	}

	run := wrWantSpawn(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test")
	if got := wrText(t, db, `SELECT start_commit FROM wiki_updates WHERE run_id=?`, run); got != wrCommitA {
		t.Fatalf("start commit = %q, want the commit of the three equal values", got)
	}
}

func TestWikiRoundKeepsTheConnectionWait(t *testing.T) {
	db := dvSpine(t)
	dvAddProject(t, db, "ws-free", "active")
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	connection := testEndpointBinding().Credential
	// ws-test waits for the connection with task 1. ws-free has no connection wait.
	dvExec(t, db, `UPDATE projects SET connection_wait=?, connection_wait_task=1 WHERE id='ws-test'`, connection)
	waits := func() string {
		return wrDump(t, db, `SELECT id, connection_wait, connection_wait_task FROM projects ORDER BY id`)
	}
	want := waits()
	same := func(step string) {
		t.Helper()
		if got := waits(); got != want {
			t.Fatalf("%s changed a connection wait:\n%s\nbefore:\n%s", step, got, want)
		}
		if n := dfInt(t, db, `SELECT COUNT(*) FROM activity WHERE kind IN ('dispatch.wait_started','dispatch.wait_ended')`); n != 0 {
			t.Fatalf("%s wrote %d connection wait records, want none", step, n)
		}
	}
	projects := []string{"ws-free", "ws-test"}
	runs := map[string]string{}

	for _, project := range projects {
		wrWantIdle(t, wrRound(t, db, project, "", ""), project, "wiki-commit-unknown")
	}
	same("an idle round")

	stopWorkerConnection(t, db)
	for _, project := range projects {
		wrWantIdle(t, wrRound(t, db, project, wrCommitA, wrCommitA), project, idleWikiConnectionUnavailable)
	}
	same("a failed connection check")

	dvExec(t, db, `UPDATE connection_health SET state='ready' WHERE connection_id=?`, connection)
	for _, project := range projects {
		runs[project] = wrWantSpawn(t, wrRound(t, db, project, wrCommitA, wrCommitA), project)
	}
	same("a start")

	for _, project := range projects {
		wrOverdue(t, db, runs[project])
		wrWantReap(t, wrRound(t, db, project, wrCommitA, wrCommitA), project, runs[project], "lease-timeout")
		wrStopped(t, db, runs[project])
	}
	same("a stop")

	wrLater(t, 2*time.Hour)
	wrRemoveRouting(t)
	for _, project := range projects {
		effect := wrRound(t, db, project, wrCommitA, wrCommitA)
		if effect["action"] != "refused" || effect["consequence"] != "wiki_update_failed" {
			t.Fatalf("effect of %s = %v, want a refused wiki candidate", project, effect)
		}
	}
	same("a refusal")
}

func TestWikiRoundFailedConnectionCheckStartsNothing(t *testing.T) {
	db := dvSpine(t)
	connection := stopWorkerConnection(t, db)
	records := dfInt(t, db, `SELECT COUNT(*) FROM activity`)
	for round := range 2 {
		wrWantIdle(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test", idleWikiConnectionUnavailable)
		if runs, updates := wrWikiRows(t, db, "ws-test"); runs != 0 || updates != 0 {
			t.Fatalf("round %d: %d Scribe runs and %d wiki updates after a failed connection check, want none", round, runs, updates)
		}
	}
	if n := dfInt(t, db, `SELECT COUNT(*) FROM activity`); n != records {
		t.Fatalf("a failed connection check wrote %d records, want none", n-records)
	}
	if got := wrDump(t, db, `SELECT connection_wait, connection_wait_task FROM projects WHERE id='ws-test'`); got !=
		"connection_wait=<nil>|connection_wait_task=<nil>|\n" {
		t.Fatalf("connection wait of the Project = %s, want none", got)
	}

	observe := wrSame(map[string]string{"ws-test": wrCommitA})
	raw := wrTick(t, db, observe)
	if len(wrTickErrors(t, raw)) != 0 || len(wrScribeSpawns(t, raw)) != 0 || strings.HasPrefix(dvTickIdle(t, raw)["ws-test"], "wiki-") {
		t.Fatalf("tick with a stopped connection = %v, want no entry of the wiki round", raw)
	}

	dvExec(t, db, `UPDATE connection_health SET state='ready' WHERE connection_id=?`, connection)
	spawns := wrScribeSpawns(t, wrTick(t, db, observe))
	if len(spawns) != 1 {
		t.Fatalf("the tick with a ready connection holds %d Scribe spawns, want one", len(spawns))
	}
	wrWantSpawn(t, spawns[0], "ws-test")
}

func TestWikiRoundsFollowEachPipelineRound(t *testing.T) {
	db := dvSpine(t)
	dvAddProject(t, db, "ws-alpha", "active")
	dvAddProject(t, db, "ws-zeta", "active")
	// ws-zeta has no observation, so its wiki round is idle.
	observe := wrSame(map[string]string{"ws-alpha": wrCommitA, "ws-test": wrCommitB})
	summary := func(raw any) string {
		var lines []string
		for _, action := range dvTickActions(t, raw) {
			lines = append(lines, fmt.Sprintf("%v %v %v", action["action"], action["role"], action["project"]))
		}
		return strings.Join(lines, "\n")
	}

	raw := wrTick(t, db, observe)
	want := strings.Join([]string{
		"spawn strategist(propose) ws-alpha", "spawn strategist(propose) ws-test", "spawn strategist(propose) ws-zeta",
		"spawn scribe ws-alpha", "spawn scribe ws-test",
	}, "\n")
	if got := summary(raw); got != want {
		t.Fatalf("actions of the tick:\n%s\nwant:\n%s", got, want)
	}
	if idle := dvTickIdle(t, raw); len(idle) != 0 {
		t.Fatalf("idle entries = %v, want none: the idle wiki round of ws-zeta adds no entry", idle)
	}

	// Each Project holds its lease now. Each wiki round is idle: two wiki
	// updates are active, and ws-zeta has no observation.
	raw = wrTick(t, db, observe)
	if got := summary(raw); got != "" {
		t.Fatalf("actions of the second tick:\n%s\nwant none", got)
	}
	wantIdle := map[string]string{"ws-alpha": "lease-held", "ws-test": "lease-held", "ws-zeta": "lease-held"}
	if idle := dvTickIdle(t, raw); !reflect.DeepEqual(idle, wantIdle) {
		t.Fatalf("idle entries of the second tick = %v, want only the entries of the pipeline rounds %v", idle, wantIdle)
	}
}

func TestAFailedWikiRoundKeepsTheLaterWikiRounds(t *testing.T) {
	db := dvSpine(t)
	dvAddProject(t, db, "ws-alpha", "active")
	dvAddProject(t, db, "ws-zeta", "active")
	// check requires one error envelope with the text, and then one Scribe spawn for the Project.
	check := func(step string, raw any, text, project string) {
		t.Helper()
		messages := wrTickErrors(t, raw)
		if len(messages) != 1 || !strings.Contains(messages[0], text) {
			t.Fatalf("%s: error envelopes = %v, want one with %q", step, messages, text)
		}
		spawns := wrScribeSpawns(t, raw)
		if len(spawns) != 1 {
			t.Fatalf("%s: %d Scribe spawns, want one for %s", step, len(spawns), project)
		}
		wrWantSpawn(t, spawns[0], project)
		actions := dvTickActions(t, raw)
		if last := actions[len(actions)-1]; last["role"] != "scribe" || actions[len(actions)-2]["error"] == nil {
			t.Fatalf("%s: the tick ends with %v, want the envelope and then the Scribe spawn", step, actions[len(actions)-2:])
		}
		if runs, updates := wrWikiRows(t, db, "ws-alpha"); runs != 0 || updates != 0 {
			t.Fatalf("%s: the failed round of ws-alpha left %d Scribe runs and %d wiki updates", step, runs, updates)
		}
	}

	// An invalid observation fails the wiki round of ws-alpha.
	raw := wrTick(t, db, wrSame(map[string]string{"ws-alpha": "not-a-commit", "ws-test": wrCommitA}))
	check("an invalid observation", raw, "the accepted commit of a wiki round is invalid", "ws-test")

	// The observer of the commit step stops the wiki round of ws-alpha.
	raw = wrTick(t, db, wrObserver{all: map[string]string{"ws-alpha": wrCommitA, "ws-zeta": wrCommitA}, one: func(project string) string {
		if project == "ws-alpha" {
			panic("the observer stopped")
		}
		return wrCommitA
	}})
	check("a stopped observer", raw, `the wiki round of Project "ws-alpha" stopped: the observer stopped`, "ws-zeta")
}

func TestWikiRoundStopsAnOverdueRun(t *testing.T) {
	db := dvSpine(t)
	dvAddProject(t, db, "ws-archived", "archived")
	for _, s := range wrStops {
		project := "ws-test"
		if s.archived {
			project = "ws-archived"
		}
		run, effect := s.stop(t, db, project)
		wrWantReap(t, effect, project, run, s.reason)
		if got, want := wrRunEnd(t, db, run), wrEndLine(s.outcome, s.reason, s.cause, true, false); got != want {
			t.Fatalf("%s: the run and its wiki update = %swant %s", s.reason, got, want)
		}
		if started, ended := wrRecords(t, db, run, project); started != 0 || ended != 1 {
			t.Fatalf("%s: %d start records and %d end records, want only the end record", s.reason, started, ended)
		}

		wrStopped(t, db, run)
		if stopped := wrText(t, db, `SELECT stopped_at FROM wiki_updates WHERE run_id=?`, run); stopped == "" {
			t.Fatalf("%s: the stop confirmation set no stop time", s.reason)
		}
		if next := wrRound(t, db, project, wrCommitA, wrCommitA); next["action"] != "idle" {
			t.Fatalf("%s: the round after the stop confirmation = %v, want idle", s.reason, next)
		}
	}
}

func TestWikiRoundRepeatsTheStopUntilConfirmed(t *testing.T) {
	db := dvSpine(t)

	// A run with the outcome completed.
	first := wrWantSpawn(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test")
	wrEndRun(t, db, first, domain.WikiRunEnd{Outcome: "completed", Result: "succeeded", WikiCommit: wrCommitA})
	// The interval passed and main moved. Only the open stop prevents the next start.
	wrLater(t, 2*time.Hour)
	for range 2 {
		wrWantReap(t, wrRound(t, db, "ws-test", wrCommitB, wrCommitB), "ws-test", first, "reported-outcome")
	}
	if runs, updates := wrWikiRows(t, db, "ws-test"); runs != 1 || updates != 1 {
		t.Fatalf("%d Scribe runs and %d wiki updates before the stop confirmation, want one of each", runs, updates)
	}
	if got := wrDump(t, db, `SELECT r.outcome, w.result, w.stopped_at FROM wiki_updates w JOIN runs r ON r.id=w.run_id WHERE w.run_id=?`, first); got !=
		"outcome=completed|result=succeeded|stopped_at=<nil>|\n" {
		t.Fatalf("the repeated stop changed the wiki update: %s", got)
	}
	wrStopped(t, db, first)
	second := wrWantSpawn(t, wrRound(t, db, "ws-test", wrCommitB, wrCommitB), "ws-test")

	// A run that a stop rule ended.
	wrOverdue(t, db, second)
	wrWantReap(t, wrRound(t, db, "ws-test", wrCommitB, wrCommitB), "ws-test", second, "lease-timeout")
	wrLater(t, 2*time.Hour)
	for range 2 {
		wrWantReap(t, wrRound(t, db, "ws-test", wrCommitC, wrCommitC), "ws-test", second, "reported-outcome")
	}
	if runs, updates := wrWikiRows(t, db, "ws-test"); runs != 2 || updates != 2 {
		t.Fatalf("%d Scribe runs and %d wiki updates before the second stop confirmation, want two of each", runs, updates)
	}
	wrStopped(t, db, second)
	wrWantSpawn(t, wrRound(t, db, "ws-test", wrCommitC, wrCommitC), "ws-test")
}

func TestArchivedProjectEndsItsWikiUpdate(t *testing.T) {
	db := dvSpine(t)
	dvAddProject(t, db, "ws-merge", "active")
	// The run of ws-test is active. The run of ws-merge has an end, and its
	// wiki commit waits for the merge.
	active, waits := wrNewRun(t), wrNewRun(t)
	wrFresh(t, db, "ws-test", active, wrCommitA)
	wrFresh(t, db, "ws-merge", waits, wrCommitA)
	wmReady(t, db, waits, wrCommitA)
	wrEndRun(t, db, waits, domain.WikiRunEnd{Outcome: "completed", WikiCommit: wrCommitB})
	for _, project := range []string{"ws-test", "ws-merge"} {
		if _, err := ProjectSetStatus(db, nil, project, "archived"); err != nil {
			t.Fatal(err)
		}
	}

	wrWantReap(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test", active, "interrupted")
	if got, want := wrRunEnd(t, db, active), wrEndLine("interrupted", "interrupted", "The Project is archived.", true, false); got != want {
		t.Fatalf("the stopped run and its wiki update = %swant %s", got, want)
	}

	// The wiki round ends no wiki update that waits for its merge.
	wrWantReap(t, wrRound(t, db, "ws-merge", wrCommitA, wrCommitA), "ws-merge", waits, "reported-outcome")
	wrStopped(t, db, waits)
	wrWantIdle(t, wrRound(t, db, "ws-merge", wrCommitA, wrCommitA), "ws-merge", "wiki-project-inactive")
	if ended := wrText(t, db, `SELECT ended_at FROM wiki_updates WHERE run_id=?`, waits); ended != "" {
		t.Fatalf("the wiki round ended the wiki update that waits for its merge at %s", ended)
	}

	owner := stRegister(t, db)
	for request := range 2 {
		stStart(t, db, owner)
		if got := wrDump(t, db, `SELECT r.outcome, w.result, w.cause, w.wiki_commit, w.counted FROM wiki_updates w JOIN runs r ON r.id=w.run_id WHERE w.run_id=?`, waits); got !=
			fmt.Sprintf("outcome=completed|result=failed|cause=The Project is archived.|wiki_commit=%s|counted=1|\n", wrCommitB) {
			t.Fatalf("start request %d: the wiki update of the archived Project = %s, want a failed end", request, got)
		}
		if _, ended := wrRecords(t, db, waits, "ws-merge"); ended != 1 {
			t.Fatalf("start request %d: %d end records, want one", request, ended)
		}
	}
}

func TestRefusedWikiCandidate(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	// facts holds each fact that the refusal of a pipeline candidate changes.
	facts := func() string {
		return wrDump(t, db, `SELECT id, blocked, blocked_reason, stalled, dispatch_retries FROM tasks ORDER BY id`) +
			wrDump(t, db, `SELECT id, status, infrastructure_failure_count, infrastructure_next_retry_at, infrastructure_condition FROM projects ORDER BY id`) +
			wrDump(t, db, `SELECT COUNT(*) AS decisions FROM proposal_decisions`) +
			wrDump(t, db, `SELECT COUNT(*) AS health FROM activity WHERE kind='dispatch.health'`) +
			wrDump(t, db, `SELECT COUNT(*) AS failures FROM infrastructure_failures`)
	}
	before := facts()

	// One refusal through the round: the host has no routing table.
	wrRemoveRouting(t)
	prepared := wrPrepare(t, db, "ws-test", wrCommitA, nil)
	attested := wrAttest(t, prepared)
	if attested.refusal == nil || attested.refusal.Code != refusal.CodeRoutingInvalid {
		t.Fatalf("attestation = %+v, want a routing refusal", attested)
	}
	run := prepared.candidate.runID
	effect := wrCommit(t, db, prepared, attested, wrCommitA)
	want := map[string]any{"action": "refused", "class": "health", "code": refusal.CodeRoutingInvalid,
		"project": "ws-test", "consequence": "wiki_update_failed", "run_id": run}
	if !reflect.DeepEqual(effect, want) {
		t.Fatalf("effect = %v, want %v", effect, want)
	}
	if got, want := wrRunEnd(t, db, run), wrEndLine("infra-failed", "launch", wrHealthCause, true, true); got != want {
		t.Fatalf("the refused candidate = %swant %s", got, want)
	}
	if got := wrDump(t, db, `SELECT role, subject, binding, session_path FROM runs WHERE id=?`, run); got !=
		fmt.Sprintf("role=scribe|subject=<nil>|binding=<nil>|session_path=sessions/%s|\n", run) {
		t.Fatalf("run of the refused candidate = %s", got)
	}
	// A second commit step with the same request returns the stored result and writes nothing.
	if replay := wrCommit(t, db, prepared, attested, wrCommitA); fmt.Sprint(replay) != fmt.Sprint(want) {
		t.Fatalf("replay = %v, want %v", replay, want)
	}
	if started, ended := wrRecords(t, db, run, "ws-test"); started != 1 || ended != 1 {
		t.Fatalf("%d start records and %d end records of the refused candidate, want one of each", started, ended)
	}
	// The run has a stop confirmation, so the next round returns no stop.
	wrWantIdle(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test", "wiki-interval")

	// The stale class writes nothing.
	for number, code := range []string{refusal.CodeStale, refusal.CodeCandidateMismatch, refusal.CodeFrameInvalid} {
		r := wrStaleRefusal
		r.Code = code
		effect, stale := wrRefuse(t, db, "ws-test", number+1, r)
		want := map[string]any{"action": "refused", "class": "stale", "code": code, "project": "ws-test", "consequence": "none"}
		if !reflect.DeepEqual(effect, want) {
			t.Fatalf("effect = %v, want %v", effect, want)
		}
		if n := dfInt(t, db, `SELECT (SELECT COUNT(*) FROM runs WHERE id=?1) + (SELECT COUNT(*) FROM activity WHERE subject=?1)`, stale); n != 0 {
			t.Fatalf("the stale refusal %s wrote %d rows, want none", code, n)
		}
	}

	// The health class and the candidate class each give a failed wiki update.
	for number, c := range []struct {
		refusal      refusal.Refusal
		class, cause string
	}{
		{wrHealthRefusal, "health", wrHealthCause},
		{wrCandidateRefusal, "candidate", wrCandidateCause},
	} {
		effect, run := wrRefuse(t, db, "ws-test", number+10, c.refusal)
		want := map[string]any{"action": "refused", "class": c.class, "code": c.refusal.Code,
			"project": "ws-test", "consequence": "wiki_update_failed", "run_id": run}
		if !reflect.DeepEqual(effect, want) {
			t.Fatalf("effect = %v, want %v", effect, want)
		}
		if got, want := wrRunEnd(t, db, run), wrEndLine("infra-failed", "launch", c.cause, true, true); got != want {
			t.Fatalf("the refused %s candidate = %swant %s", c.class, got, want)
		}
		body, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		records := wrDump(t, db, `SELECT actor, kind, detail, project, dispatch_key, dispatch_request_id, dispatch_result FROM activity WHERE subject=? ORDER BY id`, run)
		wantRecords := fmt.Sprintf("actor=dispatch|kind=wiki.update_started|detail=The wiki update started.|project=ws-test|dispatch_key=%s|dispatch_request_id=%016x|dispatch_result=%s|\n"+
			"actor=mc|kind=wiki.update_ended|detail=The wiki update failed. %s|project=ws-test|dispatch_key=<nil>|dispatch_request_id=<nil>|dispatch_result=<nil>|\n",
			rrTickKey(number+10), number+10, body, c.cause)
		if records != wantRecords {
			t.Fatalf("records of the refused %s candidate:\n%swant:\n%s", c.class, records, wantRecords)
		}
	}

	if got := facts(); got != before {
		t.Fatalf("the refusals changed a pipeline fact:\n%s\nbefore:\n%s", got, before)
	}
}

func TestWikiRoundTokenSeparatesTheRounds(t *testing.T) {
	db := dvSpine(t)
	prepared := wrPrepare(t, db, "ws-test", wrCommitA, nil)
	cand := prepared.candidate
	if cand == nil {
		t.Fatalf("the prepare step returned no candidate: %v", prepared.final)
	}

	// The token of the candidate is the token of the wiki projection.
	var canonical []byte
	if err := inTx(db, func(ctx context.Context, q Q) error {
		sel, err := selectWiki(ctx, q, "ws-test", "active", wrCommitA, nil)
		if err != nil {
			return err
		}
		canonical, err = buildCanonicalWikiPrepare(prepared.identity, prepared.deploymentUUID, prepared.requestID, sel,
			cand.mountState, spawnCandidateProjection(cand.runID, cand.spawn)).bytes()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(canonical), `{"version":16,"round":"wiki","release_build_id":`) {
		t.Fatalf("the wiki projection does not start with the round name: %.80s", canonical)
	}
	if preparationToken(canonical) != cand.token {
		t.Fatal("the token of the wiki candidate is not the token of the wiki projection")
	}

	// A pipeline projection holds no round name, so no pipeline round gives the token of a wiki round.
	pipeline := dfPrepare(t, db, prepared.requestID)
	if pipeline.candidate == nil || pipeline.candidate.token == cand.token {
		t.Fatalf("pipeline prepare = %+v, want a candidate with its own token", pipeline)
	}
	if strings.Contains(dsGoldenPrepareJSON(), `"round"`) || strings.Contains(dsGoldenPrepareJSON(), `"accepted_commit"`) {
		t.Fatal("the pipeline projection holds a key of the wiki round")
	}

	// The frame of a pipeline round keeps its bytes. The frame of a wiki
	// round holds the two keys directly after the Project.
	frame := PrivateDispatchPrepareRequest{
		Version: 1, ReleaseBuildID: "development", ControlVersion: 4, SpineSchemaID: "current", ConfigSchemaVersion: 1,
		DeploymentUUID: "de41cafe-0000-4000-8000-000000000001", DispatchRequestID: "00112233445566ff", Project: "ws-test",
		CollieInventory: []PrivateCollieContainer{}, PipelineLiveness: []PrivatePipelineLiveness{},
		RuntimeCapability: PrivateRuntimeCapability{Bindings: []PrivateRuntimeBinding{}},
	}
	head := `{"version":1,"release_build_id":"development","gateway_control_version":4,"spine_schema_id":"current",` +
		`"config_schema_version":1,"deployment_uuid":"de41cafe-0000-4000-8000-000000000001",` +
		`"dispatch_request_id":"00112233445566ff","project":"ws-test",`
	tail := `"collie_inventory":[],"pipeline_liveness":[],"runtime_capability":{"digest":"","bindings":[]}}`
	body, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != head+tail {
		t.Fatalf("pipeline prepare frame = %s\nwant                   %s", body, head+tail)
	}
	frame.Round, frame.AcceptedCommit = PrivateWikiRound, wrCommitA
	if body, err = json.Marshal(frame); err != nil {
		t.Fatal(err)
	}
	if want := head + `"round":"wiki","accepted_commit":"` + wrCommitA + `",` + tail; string(body) != want {
		t.Fatalf("wiki prepare frame = %s\nwant               %s", body, want)
	}

	commit := PrivateDispatchCommitRequest{Version: 1, Project: "ws-test"}
	if body, err = json.Marshal(commit); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"project":"ws-test","candidate":{`) {
		t.Fatalf("pipeline commit frame holds a key between the Project and the candidate: %s", body)
	}
	commit.Round, commit.AcceptedCommit = PrivateWikiRound, wrCommitA
	if body, err = json.Marshal(commit); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"project":"ws-test","round":"wiki","accepted_commit":"`+wrCommitA+`","candidate":{`) {
		t.Fatalf("wiki commit frame = %s", body)
	}
}

// The wiki round through the private frames: the path of the host broker.
func TestPrivateWikiRound(t *testing.T) {
	db := dvSpine(t)
	prepare := func(req PrivateDispatchPrepareRequest) (PrivateDispatchPrepareResponse, error) {
		var resp PrivateDispatchPrepareResponse
		err := inTx(db, func(ctx context.Context, q Q) error {
			var e error
			resp, e = DispatchPreparePrivate(ctx, q, req)
			return e
		})
		return resp, err
	}
	commit := func(req PrivateDispatchCommitRequest) (map[string]any, error) {
		var effect map[string]any
		err := inTx(db, func(ctx context.Context, q Q) error {
			result, e := DispatchCommitPrivate(ctx, q, req)
			if e != nil {
				return e
			}
			return json.Unmarshal(result.Result, &effect)
		})
		return effect, err
	}
	wikiRequest := func(requestID string) PrivateDispatchPrepareRequest {
		req := privateRequest(dfUUID(t, db))
		req.DispatchRequestID, req.Round, req.AcceptedCommit = requestID, PrivateWikiRound, wrCommitA
		return req
	}

	for name, mutate := range map[string]func(*PrivateDispatchPrepareRequest){
		"dispatch: private round is unknown":                  func(r *PrivateDispatchPrepareRequest) { r.Round = "merge" },
		"dispatch: private wiki round names no Project":       func(r *PrivateDispatchPrepareRequest) { r.Project = "" },
		"dispatch: private accepted commit is invalid":        func(r *PrivateDispatchPrepareRequest) { r.AcceptedCommit = "main" },
		"dispatch: private accepted commit has no wiki round": func(r *PrivateDispatchPrepareRequest) { r.Round = "" },
	} {
		req := wikiRequest("00000000000000a0")
		mutate(&req)
		if _, err := prepare(req); err == nil || err.Error() != name {
			t.Fatalf("prepare error = %v, want %q", err, name)
		}
	}
	if got := wrStoredCommit(t, db, "ws-test"); got != "" {
		t.Fatalf("a refused frame stored the accepted commit %q", got)
	}

	// An idle wiki round is final in the prepare step.
	idle := wikiRequest("00000000000000a1")
	idle.AcceptedCommit = ""
	resp, err := prepare(idle)
	if err != nil || resp.Kind != "final" || string(*resp.Final) != `{"action":"idle","project":"ws-test","reason":"wiki-commit-unknown"}` {
		t.Fatalf("prepare with no observation = %+v, %v, want the final idle effect", resp, err)
	}

	resp, err = prepare(wikiRequest("00000000000000a2"))
	if err != nil || resp.Kind != "candidate" || resp.Candidate.Role != "scribe" || resp.Candidate.SubjectID != nil {
		t.Fatalf("prepare = %+v, %v, want a Scribe candidate with no subject", resp, err)
	}
	if repo := resp.Candidate.MountState.SubjectRepository; repo == nil || repo.WikiUpdate != resp.Candidate.RunID || repo.InputCommit != wrCommitA {
		t.Fatalf("mount state of the candidate = %+v, want the repository of the wiki update run", resp.Candidate.MountState)
	}
	frame, err := DispatchAttestPrivate(os.Getenv("MC_HOME"), resp)
	if err != nil {
		t.Fatal(err)
	}
	if plan := frame.Attestation.MountPlan; plan == nil || len(plan.Entries) != 0 || plan.ToolHome == nil ||
		plan.Repository == nil || plan.Repository.WikiUpdate != resp.Candidate.RunID {
		t.Fatalf("attested plan = %+v, want the repository and Project home", plan)
	}

	// A frame with the round name of a pipeline round commits no wiki candidate.
	if _, err := commit(frame); err == nil || err.Error() != "dispatch: private commit round does not match the candidate" {
		t.Fatalf("commit with no round name = %v, want the round refusal", err)
	}
	if runs, updates := wrWikiRows(t, db, "ws-test"); runs != 0 || updates != 0 {
		t.Fatalf("the refused frame left %d Scribe runs and %d wiki updates", runs, updates)
	}

	frame.Round, frame.AcceptedCommit = PrivateWikiRound, wrCommitA
	effect, err := commit(frame)
	if err != nil {
		t.Fatal(err)
	}
	if effect["action"] != "spawn" || effect["role"] != "scribe" || effect["run_id"] != resp.Candidate.RunID {
		t.Fatalf("effect = %v, want the spawn of the prepared Scribe run", effect)
	}
	if runs, updates := wrWikiRows(t, db, "ws-test"); runs != 1 || updates != 1 {
		t.Fatalf("%d Scribe runs and %d wiki updates after the commit frame, want one of each", runs, updates)
	}

	// A frame with the round name of a wiki round commits no pipeline candidate.
	pipeline, err := prepare(readyPrivateRequest(t, db))
	if err != nil || pipeline.Kind != "candidate" {
		t.Fatalf("pipeline prepare = %+v, %v, want a candidate", pipeline, err)
	}
	pipelineFrame, err := DispatchAttestPrivate(os.Getenv("MC_HOME"), pipeline)
	if err != nil {
		t.Fatal(err)
	}
	pipelineFrame.Round = PrivateWikiRound
	if _, err := commit(pipelineFrame); err == nil || err.Error() != "dispatch: private commit round does not match the candidate" {
		t.Fatalf("pipeline candidate in a wiki frame = %v, want the round refusal", err)
	}
}

func TestWikiRepositoryPlanRules(t *testing.T) {
	valid := func() *PrivateDispatchMountPlan {
		return &PrivateDispatchMountPlan{
			Repository: &PrivateRepositoryPlan{
				DispatchRepository: substrate.DispatchRepository{
					DestinationBranch: "main", InputCommit: wrCommitA, MaxTemporaryBytes: 1 << 30, WikiUpdate: "0123456789abcdef",
				},
				WorkspaceRoot: "/srv/repo",
			},
			Entries: []PrivateDispatchMountEntry{}, Version: 1,
		}
	}
	if err := validatePrivateMountPlan(valid()); err != nil {
		t.Fatalf("the repository plan of a wiki update run was refused: %v", err)
	}
	version := int64(3)
	for name, mutate := range map[string]func(*substrate.DispatchRepository){
		"a task and a wiki update": func(r *substrate.DispatchRepository) { r.TaskID = 7 },
		"no subject":               func(r *substrate.DispatchRepository) { r.WikiUpdate = "" },
		"a short run identifier":   func(r *substrate.DispatchRepository) { r.WikiUpdate = "0123" },
		"an uppercase identifier":  func(r *substrate.DispatchRepository) { r.WikiUpdate = "0123456789ABCDEF" },
		"another destination":      func(r *substrate.DispatchRepository) { r.DestinationBranch = "release" },
		"no start commit":          func(r *substrate.DispatchRepository) { r.InputCommit = "" },
		"a submitted version":      func(r *substrate.DispatchRepository) { r.VersionID = &version },
	} {
		plan := valid()
		mutate(&plan.Repository.DispatchRepository)
		if err := validatePrivateMountPlan(plan); err == nil || !strings.Contains(err.Error(), "the isolated repository plan is invalid") {
			t.Errorf("%s: error = %v, want the refusal of the repository plan", name, err)
		}
	}
}

func TestWikiUpdateWritesOneStartRecordAndOneEndRecord(t *testing.T) {
	db := dvSpine(t)
	want := func(step, run, start, end string) {
		t.Helper()
		got := wrDump(t, db, `SELECT actor, kind, detail FROM activity WHERE subject=? AND project='ws-test' ORDER BY id`, run)
		if got != "actor=dispatch|kind=wiki.update_started|detail="+start+"|\nactor=mc|kind=wiki.update_ended|detail="+end+"|\n" {
			t.Fatalf("%s: records of run %s:\n%s", step, run, got)
		}
	}

	// A success.
	first := wrWantSpawn(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test")
	if started, ended := wrRecords(t, db, first, "ws-test"); started != 1 || ended != 0 {
		t.Fatalf("an active wiki update has %d start records and %d end records, want one start record", started, ended)
	}
	wrEndRun(t, db, first, domain.WikiRunEnd{Outcome: "completed", Result: "succeeded", WikiCommit: wrCommitA})
	wrWantReap(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test", first, "reported-outcome")
	wrStopped(t, db, first)
	wrWantIdle(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test", "wiki-current")
	want("a success", first, "The wiki update started.", "The wiki update succeeded. The Project wiki needed no change.")

	// A failure.
	wrLater(t, 2*time.Hour)
	second := wrWantSpawn(t, wrRound(t, db, "ws-test", wrCommitB, wrCommitB), "ws-test")
	wrOverdue(t, db, second)
	wrWantReap(t, wrRound(t, db, "ws-test", wrCommitB, wrCommitB), "ws-test", second, "lease-timeout")
	wrWantReap(t, wrRound(t, db, "ws-test", wrCommitB, wrCommitB), "ws-test", second, "reported-outcome")
	wrStopped(t, db, second)
	want("a failure", second, "The wiki update started.", "The wiki update failed. The heartbeat of the Scribe run stopped.")

	// A refused candidate.
	wrLater(t, 2*time.Hour)
	wrRemoveRouting(t)
	effect := wrRound(t, db, "ws-test", wrCommitB, wrCommitB)
	third, _ := effect["run_id"].(string)
	if effect["action"] != "refused" || effect["consequence"] != "wiki_update_failed" || !validLowercaseHex(third, 16) {
		t.Fatalf("effect = %v, want a refused wiki candidate", effect)
	}
	wrWantIdle(t, wrRound(t, db, "ws-test", wrCommitB, wrCommitB), "ws-test", "wiki-interval")
	want("a refused candidate", third, "The wiki update started.", "The wiki update failed. "+wrHealthCause)

	if got := wrDump(t, db, `SELECT kind, COUNT(*) AS records FROM activity GROUP BY kind ORDER BY kind`); got !=
		"kind=wiki.update_ended|records=3|\nkind=wiki.update_started|records=3|\n" {
		t.Fatalf("records of three wiki updates:\n%swant three start records, three end records, and no other record", got)
	}
}

func TestFailedWikiUpdateIsDueAfterTheInterval(t *testing.T) {
	db := dvSpine(t)
	run := wrWantSpawn(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test")
	wrOverdue(t, db, run)
	wrWantReap(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test", run, "lease-timeout")
	wrStopped(t, db, run)
	if got := wrDump(t, db, `SELECT result, counted FROM wiki_updates WHERE run_id=?`, run); got != "result=failed|counted=1|\n" {
		t.Fatalf("the stopped wiki update = %s, want a counted failure", got)
	}

	// The wiki is behind. The failed wiki update counts for the interval.
	interval := time.Duration(dfInt(t, db, `SELECT wiki_update_interval_s FROM settings`)) * time.Second
	wrWantIdle(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test", "wiki-interval")
	wrLater(t, interval-time.Minute)
	wrWantIdle(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test", "wiki-interval")
	if runs, updates := wrWikiRows(t, db, "ws-test"); runs != 1 || updates != 1 {
		t.Fatalf("%d Scribe runs and %d wiki updates before the end of the interval, want one of each", runs, updates)
	}
	wrLater(t, 2*time.Minute)
	if next := wrWantSpawn(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test"); next == run {
		t.Fatal("the round after the interval returned the run of the failed wiki update")
	}
}

// wrLaunchRefusal is the report of the resident for a launch check that refused the run.
var wrLaunchRefusal = domain.InfrastructureFailureReport{Source: "resident", Cause: "connection",
	Condition: "The connection changed or became unavailable before launch.", Evidence: "The launch check refused the run."}

// wrFailure is one failure of a wiki update run, or one refusal of its start.
type wrFailure struct {
	name string
	// archived gives the case its own archived Project.
	archived bool
	// fail makes the failure in the Project. It returns the run of the wiki
	// update, or "" when the failure starts no wiki update.
	fail func(t *testing.T, db *sql.DB, project string) string
	// The run and the wiki update after the failure.
	outcome, endReason, cause string
	notCounted                bool
	// stopped states that mc recorded the stop confirmation.
	stopped bool
}

// wrFailures lists each failure that the wiki round or the start request makes.
func wrFailures() []wrFailure {
	var cases []wrFailure
	for _, s := range wrStops {
		cases = append(cases, wrFailure{
			name: "stop rule " + s.reason, archived: s.archived, outcome: s.outcome, endReason: s.reason, cause: s.cause,
			fail: func(t *testing.T, db *sql.DB, project string) string {
				run, _ := s.stop(t, db, project)
				return run
			},
		})
	}
	return append(cases,
		wrFailure{name: "archived Project, ended run", archived: true, outcome: "completed", cause: "The Project is archived.",
			fail: func(t *testing.T, db *sql.DB, project string) string {
				run := wrNewRun(t)
				wrFresh(t, db, project, run, wrCommitA)
				wrEndRun(t, db, run, domain.WikiRunEnd{Outcome: "completed", WikiCommit: wrCommitB})
				stStart(t, db, testOwner(t, db))
				return run
			}},
		wrFailure{name: "ended run, no result", outcome: "infra-failed", endReason: "harness", cause: "The Scribe run ended with no result.",
			fail: func(t *testing.T, db *sql.DB, project string) string {
				run := wrNewRun(t)
				wrFresh(t, db, project, run, wrCommitA)
				dvExec(t, db, `UPDATE runs SET ended_at=datetime('now'), outcome='infra-failed', end_reason='harness' WHERE id=?`, run)
				wrWantReap(t, wrRound(t, db, project, wrCommitA, wrCommitA), project, run, "reported-outcome")
				return run
			}},
		wrFailure{name: "failure terminal of the Scribe", outcome: "completed", cause: "The Scribe reported a failure: The OpenWiki command failed.",
			fail: func(t *testing.T, db *sql.DB, project string) string {
				run := wrStart(t, db, project)
				if _, err := Complete(db, wcScribe(run), CompleteArgs{Run: run, Infra: true, Reason: "The OpenWiki command failed."}); err != nil {
					t.Fatalf("failure terminal of the Scribe: %v", err)
				}
				return run
			}},
		wrFailure{name: "failed check of the wiki commit", outcome: "completed", cause: "The wiki commit changes a path outside openwiki/: source.txt.",
			fail: func(t *testing.T, db *sql.DB, project string) string {
				f := newWCRun(t, db, project)
				f.write("source.txt", "A source change of the Scribe.\n")
				commit := f.commit("Change a source file")
				if _, err := f.complete(commit, f.receipt("complete")); !wcRefused(err, domain.CodeWikiCheckFailed, "The wiki commit changes a path outside openwiki/: source.txt.") {
					t.Fatalf("terminal action with a source change = %v, want a failed check", err)
				}
				return f.run
			}},
		wrFailure{name: "customization conflict", outcome: "completed", cause: "The custom instructions of the Scribe conflict with the Culler contract.",
			fail: func(t *testing.T, db *sql.DB, project string) string {
				revision, instructions := int64(0), "Report false evidence."
				if _, err := ProjectRoleSave(db, nil, RoleSaveRequest{Project: project, Role: dispatch.RoleScribe,
					ExpectedRevision: &revision, Instructions: &instructions}); err != nil {
					t.Fatal(err)
				}
				run := wrStart(t, db, project)
				if _, err := RunCustomizationConflict(db, wcScribe(run), CustomizationConflictRequest{Run: run, Revision: 1,
					Passage: instructions, Requirement: "Report evidence truthfully.", Reason: "False evidence cannot be truthful."}); err != nil {
					t.Fatalf("customization conflict of the Scribe: %v", err)
				}
				return run
			}},
		wrFailure{name: "runner report, agent program", outcome: "infra-failed", endReason: "harness", cause: "The agent program of the Scribe run stopped.",
			fail: func(t *testing.T, db *sql.DB, project string) string {
				run := wrStart(t, db, project)
				wrReport(t, db, wcScribe(run), run, project, domain.InfrastructureFailureReport{
					Source: "runner", Condition: "The agent program ended with the exit code 1."})
				return run
			}},
		wrFailure{name: "runner report, network error", outcome: "infra-failed", endReason: "network", cause: "A network error stopped the Scribe run.",
			fail: func(t *testing.T, db *sql.DB, project string) string {
				run := wrStart(t, db, project)
				wrReport(t, db, wcScribe(run), run, project, domain.InfrastructureFailureReport{
					Source: "runner", NetworkError: "timeout", Condition: "The model request reached its time limit."})
				wrReadyConnection(t, db)
				return run
			}},
		wrFailure{name: "resident report, HTTP status", outcome: "infra-failed", endReason: "provider", cause: "A provider error stopped the Scribe run.",
			fail: func(t *testing.T, db *sql.DB, project string) string {
				run := wrStart(t, db, project)
				status := 503
				wrReport(t, db, nil, run, project, domain.InfrastructureFailureReport{
					Source: "resident", HTTPStatus: &status, Condition: "The provider returned the status 503."})
				wrReadyConnection(t, db)
				return run
			}},
		wrFailure{name: "resident report, launch failure", outcome: "infra-failed", endReason: "launch", cause: "The Scribe run did not start.",
			fail: func(t *testing.T, db *sql.DB, project string) string {
				run := wrStart(t, db, project)
				wrReport(t, db, nil, run, project, domain.InfrastructureFailureReport{
					Source: "resident", Condition: "Docker refused the container of the run."})
				return run
			}},
		wrFailure{name: "resident report, credentials", outcome: "infra-failed", endReason: "connection", cause: "The connection of the Scribe run failed.",
			fail: func(t *testing.T, db *sql.DB, project string) string {
				run := wrStart(t, db, project)
				wrReport(t, db, nil, run, project, domain.InfrastructureFailureReport{Source: "resident", Cause: "credentials",
					Condition: "The credential of the agent profile is unavailable.", Evidence: "The credential file is absent."})
				return run
			}},
		wrFailure{name: "resident report, refused connection before launch", outcome: "infra-failed", endReason: "connection",
			cause: "The connection changed or became unavailable before launch.", notCounted: true,
			fail: func(t *testing.T, db *sql.DB, project string) string {
				run := wrStart(t, db, project)
				stopWorkerConnection(t, db)
				wrReport(t, db, nil, run, project, wrLaunchRefusal)
				wrReadyConnection(t, db)
				return run
			}},
		wrFailure{name: "refused candidate, health class", outcome: "infra-failed", endReason: "launch", cause: wrHealthCause, stopped: true,
			fail: func(t *testing.T, db *sql.DB, project string) string {
				_, run := wrRefuse(t, db, project, 1, wrHealthRefusal)
				return run
			}},
		wrFailure{name: "refused candidate, candidate class", outcome: "infra-failed", endReason: "launch", cause: wrCandidateCause, stopped: true,
			fail: func(t *testing.T, db *sql.DB, project string) string {
				_, run := wrRefuse(t, db, project, 2, wrCandidateRefusal)
				return run
			}},
		wrFailure{name: "refused candidate, stale class",
			fail: func(t *testing.T, db *sql.DB, project string) string {
				wrRefuse(t, db, project, 3, wrStaleRefusal)
				return ""
			}},
		wrFailure{name: "failed connection check before the start",
			fail: func(t *testing.T, db *sql.DB, project string) string {
				wrLater(t, 2*time.Hour)
				connection := stopWorkerConnection(t, db)
				wrWantIdle(t, wrRound(t, db, project, wrCommitA, wrCommitA), project, idleWikiConnectionUnavailable)
				dvExec(t, db, `UPDATE connection_health SET state='ready' WHERE connection_id=?`, connection)
				return ""
			}},
	)
}

// wrPipelineFacts reads each fact of the pipeline that a failed agent run can
// change: the tasks, the leases, the allowances, the pause state, the
// connection wait, the attention count, and the failure records.
func wrPipelineFacts(t *testing.T, db *sql.DB) string {
	t.Helper()
	return strings.Join([]string{
		wrDump(t, db, `SELECT * FROM tasks ORDER BY id`),
		wrDump(t, db, `SELECT * FROM leases ORDER BY project`),
		wrDump(t, db, `SELECT id, status, setup_status, infrastructure_failure_count, infrastructure_next_retry_at,
			infrastructure_last_failed_run_id, infrastructure_condition, connection_wait, connection_wait_task FROM projects ORDER BY id`),
		wrDump(t, db, `SELECT project, COUNT(*) AS attention FROM tasks WHERE archived=0 AND (stalled=1 OR blocked=1) GROUP BY project`),
		wrDump(t, db, `SELECT * FROM infrastructure_failures ORDER BY run_id`),
		wrDump(t, db, `SELECT * FROM proposal_decisions ORDER BY id`),
		wrDump(t, db, `SELECT id, role, subject, ended_at, outcome FROM runs WHERE role IS NOT 'scribe' ORDER BY id`),
	}, "\n")
}

// wrPipelineFixture gives the pipeline of ws-test a Worker run with the lease,
// a task that requires attention, a used Project allowance with a retry time,
// a connection wait, and an open proposal decision.
func wrPipelineFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	blocked := dvTask(2, dispatch.ScopeTask, dispatch.StatusSeeded, 2)
	blocked.Blocked = true
	dvInsertTask(t, db, blocked)
	dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject) VALUES ('worker-run', 'pipeline', 'worker', 'ws-test', 1)`)
	beat := time.Second
	dvHold(t, db, "ws-test", "worker-run", "worker", ptr64(1), time.Minute, &beat, time.Hour)
	dvExec(t, db, `UPDATE projects SET infrastructure_failure_count=2, infrastructure_next_retry_at='2099-01-01T00:00:00Z',
		connection_wait=?, connection_wait_task=1 WHERE id='ws-test'`, testEndpointBinding().Credential)
	dvExec(t, db, `INSERT INTO proposal_decisions (project, question) VALUES ('ws-test', 'Which goal has priority?')`)
	testOwner(t, db)
}

func TestFailedWikiUpdateRunChangesNoTaskAndNoAllowance(t *testing.T) {
	db := dvSpine(t)
	wrPipelineFixture(t, db)

	for number, c := range wrFailures() {
		project := "ws-test"
		if c.archived {
			project = fmt.Sprintf("ws-archived-%d", number)
			dvAddProject(t, db, project, "archived")
		}
		before := wrPipelineFacts(t, db)
		runs, updates := wrWikiRows(t, db, project)

		run := c.fail(t, db, project)

		same := func(step string) {
			t.Helper()
			if got := wrPipelineFacts(t, db); got != before {
				t.Fatalf("%s: %s changed a pipeline fact:\n%s\nbefore:\n%s", c.name, step, got, before)
			}
		}
		same("the failure")
		if run == "" {
			if r, u := wrWikiRows(t, db, project); r != runs || u != updates {
				t.Fatalf("%s: %d new Scribe runs and %d new wiki updates, want none", c.name, r-runs, u-updates)
			}
			continue
		}
		if got, want := wrRunEnd(t, db, run), wrEndLine(c.outcome, c.endReason, c.cause, !c.notCounted, c.stopped); got != want {
			t.Fatalf("%s: the run and its wiki update = %swant %s", c.name, got, want)
		}
		if _, ended := wrRecords(t, db, run, project); ended != 1 {
			t.Fatalf("%s: %d end records, want one", c.name, ended)
		}
		wrStopped(t, db, run)
		same("the stop confirmation")
	}
}

// A task run with the outcome completed restores the Project allowance. A
// wiki update run restores none.
func TestCompletedWikiRunKeepsTheProjectAllowance(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `UPDATE projects SET infrastructure_failure_count=2, infrastructure_next_retry_at='2099-01-01T00:00:00Z' WHERE id='ws-test'`)
	const used = "infrastructure_failure_count=2|infrastructure_next_retry_at=2099-01-01T00:00:00Z|infrastructure_last_failed_run_id=<nil>|infrastructure_condition=|\n"
	allowance := func(step string) {
		t.Helper()
		if got := wrDump(t, db, `SELECT infrastructure_failure_count, infrastructure_next_retry_at, infrastructure_last_failed_run_id,
			infrastructure_condition FROM projects WHERE id='ws-test'`); got != used {
			t.Fatalf("Project allowance after %s = %swant %s", step, got, used)
		}
	}
	allowance("the fixture")

	unchanged := newWCRun(t, db, "ws-test")
	if _, err := unchanged.complete(unchanged.start, unchanged.receipt("noop")); err != nil {
		t.Fatalf("terminal action with no wiki change: %v", err)
	}
	if got := wrDump(t, db, `SELECT r.outcome, w.result FROM wiki_updates w JOIN runs r ON r.id=w.run_id WHERE w.run_id=?`, unchanged.run); got !=
		"outcome=completed|result=succeeded|\n" {
		t.Fatalf("the run and its wiki update = %s, want a successful end", got)
	}
	allowance("a wiki update with no wiki change")
	wrStopped(t, db, unchanged.run)
	allowance("the stop confirmation")

	changed := newWCRun(t, db, "ws-test")
	changed.write("openwiki/index.md", "# Project wiki\n\nA new section.\n")
	commit := changed.commit("Update the Project wiki")
	if _, err := changed.complete(commit, changed.receipt("complete")); err != nil {
		t.Fatalf("terminal action with a wiki commit: %v", err)
	}
	if got := wrDump(t, db, `SELECT r.outcome, w.wiki_commit FROM wiki_updates w JOIN runs r ON r.id=w.run_id WHERE w.run_id=?`, changed.run); got !=
		"outcome=completed|wiki_commit="+commit+"|\n" {
		t.Fatalf("the run and its wiki update = %s, want the wiki commit of a completed run", got)
	}
	allowance("a wiki commit")
}

// The resident sends the report of wrLaunchRefusal when the launch check
// refuses a run. Only a report for a connection that refuses the run before
// the process start leaves the wiki update out of the interval.
func TestLaunchFailureWithAReadyConnectionCounts(t *testing.T) {
	db := dvSpine(t)
	connection := testEndpointBinding().Credential
	const failed, changed = "The connection of the Scribe run failed.", "The connection changed or became unavailable before launch."
	credentials := wrLaunchRefusal
	credentials.Cause = "credentials"
	cases := []struct {
		name string
		// change changes the connection after the start of the wiki update.
		change func()
		// refusal is the text of the launch check for the changed connection, or "".
		refusal string
		started bool
		report  domain.InfrastructureFailureReport
		cause   string
		counted bool
	}{
		{name: "ready connection", change: func() {}, report: wrLaunchRefusal, cause: failed, counted: true},
		{name: "stopped connection", change: func() { stopWorkerConnection(t, db) },
			refusal: "The connection requires attention. Open System health in the dashboard.", report: wrLaunchRefusal, cause: changed},
		{name: "connection with a new revision",
			change: func() {
				dvExec(t, db, `UPDATE connection_health SET revision=revision+1 WHERE connection_id=?`, connection)
			},
			refusal: "The connection changed after dispatch.", report: wrLaunchRefusal, cause: changed},
		{name: "stopped connection, credentials report", change: func() { stopWorkerConnection(t, db) },
			refusal: "The connection requires attention. Open System health in the dashboard.", report: credentials, cause: failed, counted: true},
		{name: "stopped connection after the process start", change: func() { stopWorkerConnection(t, db) }, started: true,
			refusal: "The connection requires attention. Open System health in the dashboard.", report: wrLaunchRefusal, cause: failed, counted: true},
	}
	run := ""
	for _, c := range cases {
		if run == "" {
			run = wrStart(t, db, "ws-test")
		}
		if c.started {
			if _, err := RunProcessStarted(db, wcScribe(run), run); err != nil {
				t.Fatalf("%s: process start: %v", c.name, err)
			}
		}
		c.change()
		if _, err := RunLaunchCheck(db, nil, run); (c.refusal == "") != (err == nil) || (err != nil && err.Error() != c.refusal) {
			t.Fatalf("%s: launch check = %v, want the refusal %q", c.name, err, c.refusal)
		}
		wrReport(t, db, nil, run, "ws-test", c.report)
		wrReadyConnection(t, db)
		if got, want := wrRunEnd(t, db, run), wrEndLine("infra-failed", "connection", c.cause, c.counted, false); got != want {
			t.Fatalf("%s: the run and its wiki update = %swant %s", c.name, got, want)
		}
		wrWantReap(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test", run, "reported-outcome")
		wrStopped(t, db, run)

		// The clock did not move after the start. A counted wiki update holds
		// the next start for the interval. Each other wiki update holds none.
		effect := wrRound(t, db, "ws-test", wrCommitA, wrCommitA)
		if c.counted {
			wrWantIdle(t, effect, "ws-test", "wiki-interval")
			run = ""
			continue
		}
		if next := wrWantSpawn(t, effect, "ws-test"); next == run {
			t.Fatalf("%s: the round returned the run of the failed wiki update", c.name)
		} else {
			run = next
		}
	}
	if runs, updates := wrWikiRows(t, db, "ws-test"); runs != int64(len(cases)) || updates != int64(len(cases)) {
		t.Fatalf("%d Scribe runs and %d wiki updates, want %d of each", runs, updates, len(cases))
	}
}

func TestNetworkFailureOfAWikiRunChangesOnlyConnectionHealth(t *testing.T) {
	db := dvSpine(t)
	wrPipelineFixture(t, db)
	binding := testEndpointBinding()
	run := wrWantSpawn(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test")
	profile := func() string {
		t.Helper()
		return wrDump(t, db, `SELECT state, failure_count, next_retry_at IS NOT NULL AS retry, revision FROM connection_health
			WHERE connection_id=? AND operation='model_access' AND profile_id=?`, binding.Credential, binding.ID)
	}
	others := func() string {
		t.Helper()
		return wrDump(t, db, `SELECT * FROM connection_health WHERE operation<>'model_access' ORDER BY connection_id, operation`)
	}
	if got := profile(); got != "" {
		t.Fatalf("health record of the agent profile before the failure = %s, want none", got)
	}
	pipeline, connections := wrPipelineFacts(t, db), others()

	wrReport(t, db, wcScribe(run), run, "ws-test", domain.InfrastructureFailureReport{
		Source: "runner", NetworkError: "timeout", Condition: "The model request reached its time limit."})

	if got, want := wrRunEnd(t, db, run), wrEndLine("infra-failed", "network", "A network error stopped the Scribe run.", true, false); got != want {
		t.Fatalf("the run and its wiki update = %swant %s", got, want)
	}
	// The agent profile waits for its automatic retry, as after the network error of each other run.
	if got := profile(); got != "state=retry_wait|failure_count=1|retry=1|revision=1|\n" {
		t.Fatalf("health record of the agent profile = %s, want one network failure with a retry time", got)
	}
	if got := wrDump(t, db, `SELECT d.source, d.network_error, d.condition, d.connection_id, d.profile_id, d.count_before, d.count_after,
		d.id = (SELECT latest_diagnostic_id FROM connection_health WHERE operation='model_access') AS latest
		FROM diagnostic_events d WHERE d.run_id=?`, run); got != fmt.Sprintf(
		"source=runner|network_error=timeout|condition=The model request reached its time limit.|connection_id=%s|profile_id=%s|count_before=0|count_after=0|latest=1|\n",
		binding.Credential, binding.ID) {
		t.Fatalf("diagnostics of the run = %s, want one record with the connection of the run", got)
	}
	if got := others(); got != connections {
		t.Fatalf("the failure changed another health record:\n%s\nbefore:\n%s", got, connections)
	}
	if got := wrPipelineFacts(t, db); got != pipeline {
		t.Fatalf("the failure changed a pipeline fact:\n%s\nbefore:\n%s", got, pipeline)
	}
	wrWantReap(t, wrRound(t, db, "ws-test", wrCommitA, wrCommitA), "ws-test", run, "reported-outcome")
	wrStopped(t, db, run)
	if got := wrPipelineFacts(t, db); got != pipeline {
		t.Fatalf("the stop confirmation changed a pipeline fact:\n%s\nbefore:\n%s", got, pipeline)
	}
}

// An artifact expiry removes only artifact paths, and a wiki update run mounts
// and records none. An active wiki update run holds the start of an expiry, as
// each other run does. An active expiry holds no start of a wiki update.
func TestActiveArtifactExpiryHoldsNoWikiUpdate(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	dvExec(t, db, `UPDATE tasks SET decision='cancelled',decided_at=datetime('now'),archived=1 WHERE id=1`)
	now := time.Now().UTC()
	if plan, err := ArtifactExpiryPlan(db, nil, now); err != nil || plan.TaskID != 0 {
		t.Fatalf("expiry plan inside the retention period = %+v, %v; want none", plan, err)
	}
	due := now.AddDate(0, 0, ArtifactRetentionDays+1)

	first := newWCRun(t, db, "ws-test")
	if plan, err := ArtifactExpiryPlan(db, nil, due); err != nil || plan.TaskID != 0 {
		t.Fatalf("expiry plan beside an active wiki update run = %+v, %v; want none", plan, err)
	}
	if _, err := first.complete(first.start, first.receipt("noop")); err != nil {
		t.Fatalf("terminal action with no wiki change: %v", err)
	}
	if n := dfInt(t, db, `SELECT COUNT(*) FROM task_artifacts`); n != 0 {
		t.Fatalf("the terminal action recorded %d artifact paths, want none", n)
	}
	plan, err := ArtifactExpiryPlan(db, nil, due)
	if err != nil || plan.TaskID != 1 || plan.Token == "" {
		t.Fatalf("expiry plan after the end of the wiki update run = %+v, %v; want the expiry of task 1", plan, err)
	}

	// The expiry is active. A lease claim waits for its end, and a wiki update starts.
	err = inTx(db, func(ctx context.Context, q Q) error {
		_, err := domain.Claim(ctx, q, time.Now(), domain.ClaimArgs{RunID: "lease-run", Project: "ws-test", Owner: "strategist", HardDeadlineMinutes: 60})
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "artifact expiry is active") {
		t.Fatalf("lease claim during the expiry = %v, want the refusal of the expiry", err)
	}
	wrStopped(t, db, first.run)
	wrLater(t, 2*time.Hour)
	effect := wrRound(t, db, "ws-test", wrCommitB, wrCommitB)
	run := wrWantSpawn(t, effect, "ws-test")
	mounts, err := json.Marshal(effect["mount_plan"])
	if err != nil {
		t.Fatal(err)
	}
	var mountPlan PrivateDispatchMountPlan
	if err := json.Unmarshal(mounts, &mountPlan); err != nil {
		t.Fatal(err)
	}
	if len(mountPlan.Entries) != 0 || mountPlan.Repository == nil || mountPlan.ToolHome == nil || strings.Contains(string(mounts), "artifact") {
		t.Fatalf("mount plan of the run %s = %s, want the repository, Project home, and no artifact path", run, mounts)
	}
	if resumed, err := ArtifactExpiryPlan(db, nil, due); err != nil || resumed.Token != plan.Token {
		t.Fatalf("expiry plan beside the new wiki update run = %+v, %v; want the same active expiry", resumed, err)
	}
	if _, err := ArtifactExpiryComplete(db, nil, 1, plan.Token, due); err != nil {
		t.Fatalf("end of the expiry beside an active wiki update run: %v", err)
	}
}

func TestWikiStartRecordOfALargeEffect(t *testing.T) {
	db := dvSpine(t)
	revision, instructions := int64(0), strings.Repeat("Keep each page short. ", 2000)
	if _, err := ProjectRoleSave(db, nil, RoleSaveRequest{Project: "ws-test", Role: dispatch.RoleScribe,
		ExpectedRevision: &revision, Instructions: &instructions}); err != nil {
		t.Fatal(err)
	}
	prepared := wrPrepare(t, db, "ws-test", wrCommitA, nil)
	attested := wrAttest(t, prepared)
	effect := wrCommit(t, db, prepared, attested, wrCommitA)
	run := wrWantSpawn(t, effect, "ws-test")
	body, err := json.Marshal(effect)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) <= maxReceiptBytes || len(body) > maxEffectBytes {
		t.Fatalf("the effect has %d bytes, want more than the receipt limit of %d bytes", len(body), maxReceiptBytes)
	}
	if brief, _ := effect["brief"].(string); !strings.Contains(brief, "Keep each page short. Keep each page short.") {
		t.Fatal("the effect does not hold the complete run brief")
	}
	if runs, updates := wrWikiRows(t, db, "ws-test"); runs != 1 || updates != 1 {
		t.Fatalf("%d Scribe runs and %d wiki updates, want the committed start", runs, updates)
	}

	var receipt map[string]any
	stored := wrText(t, db, `SELECT dispatch_result FROM activity WHERE kind='wiki.update_started' AND subject=? AND dispatch_request_id=?`, run, prepared.requestID)
	if err := json.Unmarshal([]byte(stored), &receipt); err != nil {
		t.Fatalf("the start record holds no valid result: %v", err)
	}
	if fmt.Sprint(receipt[receiptElidedKey]) != "[brief skills]" || receipt["brief"] != nil || receipt["skills"] != nil ||
		receipt["action"] != "spawn" || receipt["run_id"] != run || len(stored) > maxReceiptBytes {
		t.Fatalf("start record of %d bytes = %.300s, want the effect with no brief and no skills", len(stored), stored)
	}

	// A start record with no brief answers no second commit step.
	if err := underDispatchLock(db, func(ctx context.Context, q Q) error {
		_, e := wikiCommit(ctx, q, prepared, attested, wrCommitA)
		return e
	}); err == nil || !strings.Contains(err.Error(), "not replaying a spawn without its brief") {
		t.Fatalf("second commit step = %v, want the refusal of the replay", err)
	}
	if runs, updates := wrWikiRows(t, db, "ws-test"); runs != 1 || updates != 1 {
		t.Fatalf("the second commit step left %d Scribe runs and %d wiki updates, want one of each", runs, updates)
	}
}

func TestAWikiErrorKeepsTheStartRequest(t *testing.T) {
	// start runs one start request with the observations on a new spine that
	// holds one merge that is due.
	start := func(t *testing.T, prepare func(db *sql.DB), observed map[string]string) *sql.DB {
		t.Helper()
		db := dvSpine(t)
		dvAddProject(t, db, "ws-zeta", "active")
		stMergeable(t, db, 1)
		stApprove(t, db, 1)
		if prepare != nil {
			prepare(db)
		}
		resp, err := DeliveryStart(db, nil, StartRequest{Owner: testOwner(t, db), AcceptedCommits: observed})
		if err != nil {
			t.Fatalf("the start request failed: %v", err)
		}
		stWantStarted(t, db, resp, "apply/1")
		if len(resp.Failed) != 0 || len(resp.Executions) != 1 {
			t.Fatalf("start response = %+v, want one execution and no failure", resp)
		}
		return db
	}
	stored := func(db *sql.DB) string {
		return wrDump(t, db, `SELECT project, accepted_commit FROM accepted_commits ORDER BY project`)
	}

	t.Run("valid observations", func(t *testing.T) {
		db := start(t, nil, map[string]string{"ws-test": wrCommitA, "ws-zeta": wrCommitB})
		if got := stored(db); got != fmt.Sprintf("project=ws-test|accepted_commit=%s|\nproject=ws-zeta|accepted_commit=%s|\n", wrCommitA, wrCommitB) {
			t.Fatalf("stored accepted commits = %s, want both observations", got)
		}
	})
	t.Run("invalid entries", func(t *testing.T) {
		db := start(t, nil, map[string]string{"ws-test": "not-a-commit", "ws-unknown": wrCommitA, "ws-zeta": wrCommitB})
		if got := stored(db); got != fmt.Sprintf("project=ws-zeta|accepted_commit=%s|\n", wrCommitB) {
			t.Fatalf("stored accepted commits = %s, want only the valid entry of a known Project", got)
		}
	})
	t.Run("error inside the wiki savepoint", func(t *testing.T) {
		// The second write of the savepoint fails. The savepoint then holds no write.
		db := start(t, func(db *sql.DB) {
			dvExec(t, db, `CREATE TRIGGER wr_refuses_one_project BEFORE INSERT ON accepted_commits WHEN NEW.project='ws-zeta'
				BEGIN SELECT RAISE(ABORT, 'the test refuses this accepted commit'); END`)
		}, map[string]string{"ws-test": wrCommitA, "ws-zeta": wrCommitB})
		if got := stored(db); got != "" {
			t.Fatalf("stored accepted commits = %s, want none after the error", got)
		}
		// The next start request repeats the wiki step.
		dvExec(t, db, `DROP TRIGGER wr_refuses_one_project`)
		if _, err := DeliveryStart(db, nil, StartRequest{Owner: testOwner(t, db),
			AcceptedCommits: map[string]string{"ws-test": wrCommitA, "ws-zeta": wrCommitB}}); err != nil {
			t.Fatal(err)
		}
		if got := stored(db); got != fmt.Sprintf("project=ws-test|accepted_commit=%s|\nproject=ws-zeta|accepted_commit=%s|\n", wrCommitA, wrCommitB) {
			t.Fatalf("stored accepted commits after the next start request = %s, want both observations", got)
		}
	})
}
