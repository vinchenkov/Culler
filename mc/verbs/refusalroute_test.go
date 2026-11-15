package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"mc/boundary"
	"mc/dispatch"
	"mc/domain"
	"mc/refusal"
)

// The consequence router, proven arm by arm. The pure half (which class a
// refusal carries) is mc/refusal's; this file proves the impure half — that
// each class applies exactly its consequence and nothing else.
//
// Every test asserts the four-part invariant through rrAssertInert: zero new
// Run rows, a free lock, no spawn effect, and no fall-through to another
// candidate. That last part is why rrSpine seeds a perfectly dispatchable task
// on every fixture: if the router ever fell through to ordinary selection, that
// task would be claimed and the assertion would catch it.

const rrKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// rrSpine is a fixture carrying one dispatchable task (id 9, the fall-through
// bait), one blockable subject task (id 1), and one active Collie session.
func rrSpine(t *testing.T) *sql.DB {
	t.Helper()
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 3))
	dvInsertTask(t, db, dvTask(9, dispatch.ScopeTask, dispatch.StatusProposed, 1))
	dvExec(t, db, `
		INSERT INTO collie_sessions (id, container_name, verb_allowlist, session_path, binding)
		VALUES ('sess-1', 'mc-collie-sess-1', '[]', 'sessions/sess-1', 'claude')`)
	return db
}

func rrApply(t *testing.T, db *sql.DB, cand RefusalCandidate, r refusal.Refusal, key string) (map[string]any, error) {
	t.Helper()
	var eff map[string]any
	err := inTx(db, func(ctx context.Context, q Q) error {
		var e error
		eff, e = applyRefusal(ctx, q, cand, r, key)
		return e
	})
	return eff, err
}

// rrAssertInert is the four-part invariant. No arm of the router may open a
// Run, take the lease, emit a spawn, or let selection continue.
func rrAssertInert(t *testing.T, db *sql.DB, eff map[string]any) {
	t.Helper()
	var runs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&runs); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if runs != 0 {
		t.Errorf("a refusal opened %d Run row(s); a refusal opens none", runs)
	}
	var lockRun sql.NullString
	if err := db.QueryRow(`SELECT run_id FROM leases WHERE project='ws-test'`).Scan(&lockRun); err != nil {
		t.Fatalf("read lock: %v", err)
	}
	if lockRun.Valid {
		t.Errorf("a refusal left the lease held by %q; a refusal never claims", lockRun.String)
	}
	if eff == nil {
		return
	}
	if got := eff["action"]; got != "refused" {
		t.Errorf("effect action = %v, want \"refused\" (a refusal never falls through to another candidate)", got)
	}
	if _, ok := eff["run_id"]; ok {
		t.Errorf("refusal effect carries a run_id: %v", eff)
	}
}

func rrTaskBlocked(t *testing.T, db *sql.DB, id int64) (bool, string) {
	t.Helper()
	var blocked int
	var reason sql.NullString
	if err := db.QueryRow(`SELECT blocked, blocked_reason FROM tasks WHERE id = ?`, id).
		Scan(&blocked, &reason); err != nil {
		t.Fatalf("read task %d: %v", id, err)
	}
	return blocked == 1, reason.String
}

func rrActivity(t *testing.T, db *sql.DB, kind string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT detail FROM activity WHERE kind = ? ORDER BY id`, kind)
	if err != nil {
		t.Fatalf("read activity %q: %v", kind, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d sql.NullString
		if err := rows.Scan(&d); err != nil {
			t.Fatalf("scan activity: %v", err)
		}
		out = append(out, d.String)
	}
	return out
}

func rrCollieStatus(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var s string
	if err := db.QueryRow(`SELECT status FROM collie_sessions WHERE id = ?`, id).Scan(&s); err != nil {
		t.Fatalf("read collie session %q: %v", id, err)
	}
	return s
}

func rrSubject(id int64) RefusalCandidate {
	return RefusalCandidate{Kind: RefusalSubjectTask, TaskID: &id, Project: "ws-test"}
}

// --- Stale/protocol: no durable mutation or effect, on any candidate --------

// An innocent concurrent operator edit can raise the stale row, so it must
// never punish anything. Proven against all three candidate shapes: a stale
// code is inert regardless of who it was raised for.
func TestRefusalStaleMutatesNothing(t *testing.T) {
	codes := []string{
		refusal.CodeStale, refusal.CodeFrameInvalid, refusal.CodeVersionMismatch,
		refusal.CodeCandidateMismatch, refusal.CodeInventoryChanged, refusal.CodeOversize,
	}
	cands := map[string]RefusalCandidate{
		"subject_task": rrSubject(1),
		"subjectless":  {Kind: RefusalSubjectlessPipeline, Project: "ws-test"},
		"collie":       {Kind: RefusalCollie, SessionID: "sess-1"},
	}
	for _, code := range codes {
		for name, cand := range cands {
			t.Run(code+"/"+name, func(t *testing.T) {
				db := rrSpine(t)
				eff, err := rrApply(t, db, cand, refusal.Refusal{
					Code: code, Field: refusal.FieldNone, Summary: refusal.SummaryMismatch,
				}, rrKey)
				if err != nil {
					t.Fatalf("stale refusal returned an error: %v", err)
				}
				rrAssertInert(t, db, eff)
				if eff["consequence"] != "none" {
					t.Errorf("consequence = %v, want \"none\"", eff["consequence"])
				}
				if blocked, _ := rrTaskBlocked(t, db, 1); blocked {
					t.Error("a stale refusal blocked the subject task")
				}
				if got := rrCollieStatus(t, db, "sess-1"); got != "active" {
					t.Errorf("a stale refusal ended the Collie session (status %q)", got)
				}
				var acts int
				if err := db.QueryRow(`SELECT COUNT(*) FROM activity`).Scan(&acts); err != nil {
					t.Fatalf("count activity: %v", err)
				}
				if acts != 0 {
					t.Errorf("a stale refusal wrote %d activity row(s); a stale refusal mutates nothing", acts)
				}
			})
		}
	}
}

// --- Deployment health: one health action, no charge/block -----------------

func TestRefusalHealthRecordsOneHealthAction(t *testing.T) {
	codes := []string{
		refusal.CodeRuntimeUnavailable, refusal.CodeHelperUnavailable,
		refusal.CodeImageUnavailable, refusal.CodeGatewayUnavailable,
		refusal.CodeNetworkCapabilityUnavailable, refusal.CodeResourceCapabilityUnavailable,
		refusal.CodeResourceConfigInvalid, refusal.CodeFilePlaneUnavailable,
		refusal.CodeProjectionUnavailable, refusal.CodeConfigInvalid,
		refusal.CodeRoutingInvalid,
	}
	for _, code := range codes {
		t.Run(code, func(t *testing.T) {
			db := rrSpine(t)
			eff, err := rrApply(t, db, rrSubject(1), refusal.Refusal{
				Code: code, Field: refusal.FieldRuntime, Summary: refusal.SummaryUnavailable,
			}, rrKey)
			if err != nil {
				t.Fatalf("health refusal returned an error: %v", err)
			}
			rrAssertInert(t, db, eff)
			if eff["consequence"] != "health" {
				t.Errorf("consequence = %v, want \"health\"", eff["consequence"])
			}
			// The deployment's fault is never the task's charge or block.
			if blocked, reason := rrTaskBlocked(t, db, 1); blocked {
				t.Errorf("a deployment-health refusal blocked the subject task (%q); it must charge and block nothing", reason)
			}
			var retries int
			if err := db.QueryRow(`SELECT dispatch_retries FROM tasks WHERE id = 1`).Scan(&retries); err != nil {
				t.Fatalf("read retries: %v", err)
			}
			if retries != 3 {
				t.Errorf("a health refusal charged the dispatch budget (retries %d, want 3)", retries)
			}
			got := rrActivity(t, db, "dispatch.health")
			if len(got) != 1 {
				t.Fatalf("health refusal wrote %d dispatch.health rows, want exactly one", len(got))
			}
		})
	}
}

// The router carves the two allowlist-trust codes out of candidate policy:
// the deployment's own allowlist file must never become a task's fault, even if
// the attester mislabels the frame's authority.
func TestRefusalAllowlistTrustIsAlwaysHealth(t *testing.T) {
	for _, code := range []string{boundary.CodeAllowlistUntrusted, boundary.CodeAllowlistInvalid} {
		for _, auth := range []refusal.Authority{"", refusal.AuthorityCandidate, refusal.AuthorityDeployment} {
			t.Run(code+"/"+string(auth), func(t *testing.T) {
				db := rrSpine(t)
				eff, err := rrApply(t, db, rrSubject(1), refusal.Refusal{
					Code: code, Authority: auth,
					Field: refusal.FieldAllowlist, Summary: refusal.SummaryUntrusted,
				}, rrKey)
				if err != nil {
					t.Fatalf("allowlist-trust refusal returned an error: %v", err)
				}
				rrAssertInert(t, db, eff)
				if eff["consequence"] != "health" {
					t.Errorf("consequence = %v, want \"health\" whatever the claimed authority", eff["consequence"])
				}
				if blocked, _ := rrTaskBlocked(t, db, 1); blocked {
					t.Error("the deployment's allowlist file became a task's fault")
				}
			})
		}
	}
}

// --- Candidate policy: subject task blocks with the code -------------------

func TestRefusalCandidateBlocksSubjectTask(t *testing.T) {
	codes := []string{
		refusal.CodeEnvInvalid, refusal.CodeEnvForbidden,
		refusal.CodeAuthBindingInvalid, refusal.CodeAuthDeliveryInvalid,
		refusal.CodeAuthCABindingMismatch, refusal.CodeNetworkRuleInvalid,
		refusal.CodeNetworkRuleUnresolved, refusal.CodeNetworkDestinationForbidden,
		refusal.CodeNetworkPolicyUnappliable, refusal.CodeNetworkPolicyMismatch,
		refusal.CodeIdentityNameInvalid,
	}
	for _, code := range codes {
		t.Run(code, func(t *testing.T) {
			db := rrSpine(t)
			eff, err := rrApply(t, db, rrSubject(1), refusal.Refusal{
				Code: code, Field: refusal.FieldNone, Summary: refusal.SummaryForbidden,
			}, rrKey)
			if err != nil {
				t.Fatalf("candidate refusal returned an error: %v", err)
			}
			rrAssertInert(t, db, eff)
			if eff["consequence"] != "task_blocked" {
				t.Errorf("consequence = %v, want \"task_blocked\"", eff["consequence"])
			}
			blocked, reason := rrTaskBlocked(t, db, 1)
			if !blocked {
				t.Fatal("a candidate-policy refusal did not block its subject task")
			}
			if want := "confinement:" + code; reason != want {
				t.Errorf("blocked_reason = %q, want %q", reason, want)
			}
			var kind string
			if err := db.QueryRow(`SELECT blocked_kind FROM tasks WHERE id = 1`).Scan(&kind); err != nil || kind != domain.BlockConfinement {
				t.Errorf("blocked_kind = %q (%v), want %q", kind, err, domain.BlockConfinement)
			}
			// The block is the consequence; the infra budget is a different
			// budget and the router does not spend it here.
			var retries int
			if err := db.QueryRow(`SELECT dispatch_retries FROM tasks WHERE id = 1`).Scan(&retries); err != nil {
				t.Fatalf("read retries: %v", err)
			}
			if retries != 3 {
				t.Errorf("a candidate-policy refusal charged the dispatch budget (retries %d, want 3)", retries)
			}
			// The bait task must be untouched: no fall-through.
			if b, _ := rrTaskBlocked(t, db, 9); b {
				t.Error("the refusal blocked an unrelated task")
			}
		})
	}
}

// The fourteen candidate-ownable mount codes: authority alone decides between
// the health and candidate-policy rows. This is the discriminator the whole
// taxonomy turns on — the same code, the same subject, two consequences.
func TestRefusalMountAuthorityDecidesConsequence(t *testing.T) {
	mountCodes := []string{
		boundary.CodeSourceMissing, boundary.CodeSourceWrongKind, boundary.CodeSourceBlocked,
		boundary.CodeSymlinkEscape, boundary.CodeNotAllowlisted, boundary.CodeDeniedRoot,
		boundary.CodeCrossProject, boundary.CodeRWNotPermitted, boundary.CodeTargetInvalid,
		boundary.CodeSourceAlias, boundary.CodeTargetCollision, boundary.CodeIdentityChanged,
		boundary.CodeRuntimeUnappliable, boundary.CodeGateUnhealthy,
	}
	for _, code := range mountCodes {
		t.Run(code+"/candidate_authority_blocks", func(t *testing.T) {
			db := rrSpine(t)
			eff, err := rrApply(t, db, rrSubject(1), refusal.Refusal{
				Code: code, Authority: refusal.AuthorityCandidate,
				Field: refusal.FieldMountSource, Summary: refusal.SummaryNotAllowlisted,
			}, rrKey)
			if err != nil {
				t.Fatalf("candidate-authority mount refusal errored: %v", err)
			}
			rrAssertInert(t, db, eff)
			blocked, reason := rrTaskBlocked(t, db, 1)
			if !blocked {
				t.Fatal("a candidate-authored mount failure did not block its subject task")
			}
			if want := "confinement:" + code; reason != want {
				t.Errorf("blocked_reason = %q, want %q", reason, want)
			}
		})
		t.Run(code+"/deployment_authority_is_health", func(t *testing.T) {
			db := rrSpine(t)
			eff, err := rrApply(t, db, rrSubject(1), refusal.Refusal{
				Code: code, Authority: refusal.AuthorityDeployment,
				Field: refusal.FieldMountSource, Summary: refusal.SummaryUnavailable,
			}, rrKey)
			if err != nil {
				t.Fatalf("deployment-authority mount refusal errored: %v", err)
			}
			rrAssertInert(t, db, eff)
			if eff["consequence"] != "health" {
				t.Errorf("consequence = %v, want \"health\"", eff["consequence"])
			}
			if blocked, r := rrTaskBlocked(t, db, 1); blocked {
				t.Errorf("a shared typed-system mount failure blocked the candidate (%q); it must never charge, block, or end", r)
			}
			if n := len(rrActivity(t, db, "dispatch.health")); n != 1 {
				t.Errorf("wrote %d dispatch.health rows, want one", n)
			}
		})
	}
}

// --- Candidate policy: subjectless pipeline cannot invent a task ----------

func TestRefusalCandidateSubjectlessRequiresProjectDecision(t *testing.T) {
	db := rrSpine(t)
	eff, err := rrApply(t, db, RefusalCandidate{Kind: RefusalSubjectlessPipeline, Project: "ws-test"}, refusal.Refusal{
		Code: refusal.CodeEnvForbidden, Field: refusal.FieldEnvName, Summary: refusal.SummaryForbidden,
	}, rrKey)
	if err != nil {
		t.Fatalf("subjectless candidate refusal errored: %v", err)
	}
	rrAssertInert(t, db, eff)
	if eff["consequence"] != "proposal_decision_pending" {
		t.Errorf("consequence = %v, want a Project proposal decision", eff["consequence"])
	}
	if dfInt(t, db, `SELECT COUNT(*) FROM proposal_decisions WHERE project='ws-test' AND run_id IS NULL AND resolved_at IS NULL`) != 1 {
		t.Fatal("The refusal omitted its Project proposal decision")
	}
	if dfInt(t, db, `SELECT infrastructure_failure_count FROM projects WHERE id='ws-test'`) != 0 {
		t.Fatal("The proposal decision consumed the Project infrastructure allowance")
	}
	if n := len(rrActivity(t, db, "dispatch.health")); n != 1 {
		t.Errorf("wrote %d dispatch.health rows, want one", n)
	}
	var blocked int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE blocked = 1`).Scan(&blocked); err != nil {
		t.Fatalf("count blocked: %v", err)
	}
	if blocked != 0 {
		t.Errorf("a subjectless refusal blocked %d task(s); it has no subject to blame", blocked)
	}
}

// --- Candidate policy: Collie ends with confinement:<code> ------------------

func TestRefusalCandidateEndsCollie(t *testing.T) {
	db := rrSpine(t)
	code := refusal.CodeNetworkPolicyMismatch
	eff, err := rrApply(t, db, RefusalCandidate{Kind: RefusalCollie, SessionID: "sess-1"}, refusal.Refusal{
		Code: code, Field: refusal.FieldNetworkRule, Summary: refusal.SummaryMismatch,
	}, rrKey)
	if err != nil {
		t.Fatalf("Collie candidate refusal errored: %v", err)
	}
	rrAssertInert(t, db, eff)
	if eff["consequence"] != "collie_ended" {
		t.Errorf("consequence = %v, want \"collie_ended\"", eff["consequence"])
	}
	if got := rrCollieStatus(t, db, "sess-1"); got != "ended" {
		t.Errorf("session status = %q, want \"ended\"", got)
	}
	// The exact reason grammar.
	details := rrActivity(t, db, "collie.ended")
	if len(details) != 1 {
		t.Fatalf("wrote %d collie.ended rows, want one", len(details))
	}
	if want := "confinement:" + code; details[0] != want {
		t.Errorf("collie.ended detail = %q, want %q", details[0], want)
	}
	// A policy-invalid Collie is ended in the same transaction precisely so it
	// cannot remain the repeatedly selected oldest active row and starve
	// pipeline work.
	var active int
	if err := db.QueryRow(`SELECT COUNT(*) FROM collie_sessions WHERE status = 'active'`).Scan(&active); err != nil {
		t.Fatalf("count active: %v", err)
	}
	if active != 0 {
		t.Errorf("%d session(s) still active; the ended row must not starve later work", active)
	}
}

// The row persists forever and stays resumable: a null-locator conversation
// stays recoverable through the --from-rows arm after repair, so the end must
// not destroy the row or its locators.
func TestRefusalCollieEndPreservesResumability(t *testing.T) {
	db := rrSpine(t)
	if _, err := rrApply(t, db, RefusalCandidate{Kind: RefusalCollie, SessionID: "sess-1"}, refusal.Refusal{
		Code: refusal.CodeEnvInvalid, Field: refusal.FieldEnvName, Summary: refusal.SummaryForbidden,
	}, rrKey); err != nil {
		t.Fatalf("Collie refusal errored: %v", err)
	}
	var path, binding string
	if err := db.QueryRow(`SELECT session_path, binding FROM collie_sessions WHERE id = 'sess-1'`).
		Scan(&path, &binding); err != nil {
		t.Fatalf("the ended session row is gone or unreadable: %v", err)
	}
	if path != "sessions/sess-1" || binding != "claude" {
		t.Errorf("end mutated the frozen locators: path=%q binding=%q", path, binding)
	}
}

// An already-ended or reaped session is preserved, not re-ended: the same
// idempotence CollieEnd gives a host retry.
func TestRefusalCollieEndIsIdempotent(t *testing.T) {
	for _, status := range []string{"ended", "reaped"} {
		t.Run(status, func(t *testing.T) {
			db := rrSpine(t)
			dvExec(t, db, `UPDATE collie_sessions SET status = ? WHERE id = 'sess-1'`, status)
			eff, err := rrApply(t, db, RefusalCandidate{Kind: RefusalCollie, SessionID: "sess-1"}, refusal.Refusal{
				Code: refusal.CodeEnvInvalid, Field: refusal.FieldEnvName, Summary: refusal.SummaryForbidden,
			}, rrKey)
			if err != nil {
				t.Fatalf("refusal against a %s session errored: %v", status, err)
			}
			rrAssertInert(t, db, eff)
			if got := rrCollieStatus(t, db, "sess-1"); got != status {
				t.Errorf("status = %q, want %q preserved", got, status)
			}
			if n := len(rrActivity(t, db, "collie.ended")); n != 0 {
				t.Errorf("appended %d duplicate collie.ended rows", n)
			}
		})
	}
}

// --- The Collie end is launch-fenced -----------------------------------------

// The Collie consequence is a *launch-fenced* end: it applies only to the
// launch generation the candidate was selected against. A session that moved
// on — a launch bound after selection, the observed launch superseded into
// resume debt, or replaced by a newer generation — is not the thing the
// refusal judged, so the end must not touch it. The supersede-then-select
// order makes the mismatch detectable as a plain generation comparison.
func TestRefusalCollieEndIsLaunchFenced(t *testing.T) {
	launchA := strings.Repeat("a", 16)
	launchB := strings.Repeat("b", 16)
	for name, c := range map[string]struct {
		rowLaunch string // current_launch_id at commit time; "" = none
		observed  string // what the candidate saw at selection; "" = none
		wantEnd   bool
	}{
		"no_launch_and_none_observed": {rowLaunch: "", observed: "", wantEnd: true},
		"same_generation":             {rowLaunch: launchA, observed: launchA, wantEnd: true},
		"newer_generation":            {rowLaunch: launchB, observed: launchA, wantEnd: false},
		"launch_bound_after_selection": {rowLaunch: launchA, observed: "",
			wantEnd: false},
		"observed_launch_superseded": {rowLaunch: "", observed: launchA,
			wantEnd: false},
	} {
		t.Run(name, func(t *testing.T) {
			db := rrSpine(t)
			if c.rowLaunch != "" {
				dvExec(t, db, `UPDATE collie_sessions
					SET current_launch_id = ?, current_launch_mode = 'fresh'
					WHERE id = 'sess-1'`, c.rowLaunch)
			}
			eff, err := rrApply(t, db, RefusalCandidate{
				Kind: RefusalCollie, SessionID: "sess-1", LaunchID: c.observed,
			}, refusal.Refusal{
				Code: refusal.CodeEnvInvalid, Field: refusal.FieldEnvName,
				Summary: refusal.SummaryForbidden,
			}, rrKey)
			if err != nil {
				t.Fatalf("launch-fenced refusal errored: %v", err)
			}
			rrAssertInert(t, db, eff)
			if c.wantEnd {
				if eff["consequence"] != "collie_ended" {
					t.Errorf("consequence = %v, want \"collie_ended\"", eff["consequence"])
				}
				if got := rrCollieStatus(t, db, "sess-1"); got != "ended" {
					t.Errorf("status = %q, want \"ended\"", got)
				}
				if n := len(rrActivity(t, db, "collie.ended")); n != 1 {
					t.Errorf("wrote %d collie.ended rows, want one", n)
				}
				return
			}
			// Fenced off: the refusal was about a generation that no longer
			// exists, so it applies NO consequence — the same no-durable-
			// mutation posture as the stale class. The current generation
			// gets its own attestation, and its own verdict, next tick.
			if eff["consequence"] != "none" {
				t.Errorf("consequence = %v, want \"none\"", eff["consequence"])
			}
			if eff["launch_superseded"] != true {
				t.Errorf("effect must name the fence: launch_superseded = %v", eff["launch_superseded"])
			}
			if got := rrCollieStatus(t, db, "sess-1"); got != "active" {
				t.Errorf("status = %q; a superseded-generation refusal must not end the session", got)
			}
			if n := len(rrActivity(t, db, "collie.ended")); n != 0 {
				t.Errorf("wrote %d collie.ended rows across the fence", n)
			}
		})
	}
}

// A malformed observed generation is a protocol error, refused before any
// write — never coerced to "no launch", which would make the fence pass or
// fail by accident.
func TestRefusalCollieMalformedLaunchIDRefused(t *testing.T) {
	for name, id := range map[string]string{
		"short":     strings.Repeat("a", 15),
		"long":      strings.Repeat("a", 17),
		"uppercase": strings.Repeat("A", 16),
		"non_hex":   strings.Repeat("a", 15) + "g",
	} {
		t.Run(name, func(t *testing.T) {
			db := rrSpine(t)
			_, err := rrApply(t, db, RefusalCandidate{
				Kind: RefusalCollie, SessionID: "sess-1", LaunchID: id,
			}, refusal.Refusal{
				Code: refusal.CodeEnvInvalid, Field: refusal.FieldEnvName,
				Summary: refusal.SummaryForbidden,
			}, rrKey)
			if err == nil {
				t.Fatal("malformed launch id accepted")
			}
			rrAssertInert(t, db, nil)
			if got := rrCollieStatus(t, db, "sess-1"); got != "active" {
				t.Errorf("status = %q; a refused candidate must mutate nothing", got)
			}
		})
	}
}

// --- Unknown and incoherent input: refused, never guessed ------------------

// A class the router cannot derive applies NO consequence. Guessing is the
// fail-open direction: it would block a task for the deployment's mistake.
func TestRefusalUnknownOrIncoherentMutatesNothing(t *testing.T) {
	cases := map[string]refusal.Refusal{
		"unknown code":            {Code: "not.a.code", Field: refusal.FieldNone, Summary: refusal.SummaryMismatch},
		"empty code":              {Code: "", Field: refusal.FieldNone, Summary: refusal.SummaryMismatch},
		"authority on fixed code": {Code: refusal.CodeEnvInvalid, Authority: refusal.AuthorityCandidate, Field: refusal.FieldNone, Summary: refusal.SummaryForbidden},
		"mount code no authority": {Code: boundary.CodeNotAllowlisted, Field: refusal.FieldMountSource, Summary: refusal.SummaryNotAllowlisted},
		"bogus authority":         {Code: boundary.CodeNotAllowlisted, Authority: "attacker", Field: refusal.FieldMountSource, Summary: refusal.SummaryNotAllowlisted},
		"unenumerated field":      {Code: refusal.CodeEnvInvalid, Field: "/etc/passwd", Summary: refusal.SummaryForbidden},
		"unenumerated summary":    {Code: refusal.CodeEnvInvalid, Field: refusal.FieldNone, Summary: "rm -rf /"},
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			db := rrSpine(t)
			_, err := rrApply(t, db, rrSubject(1), r, rrKey)
			if err == nil {
				t.Fatal("an underivable refusal was applied instead of refused")
			}
			rrAssertInert(t, db, nil)
			if blocked, _ := rrTaskBlocked(t, db, 1); blocked {
				t.Error("an underivable refusal blocked the subject task")
			}
			var acts int
			if err := db.QueryRow(`SELECT COUNT(*) FROM activity`).Scan(&acts); err != nil {
				t.Fatalf("count activity: %v", err)
			}
			if acts != 0 {
				t.Errorf("an underivable refusal wrote %d activity row(s)", acts)
			}
		})
	}
}

// A candidate shape the router does not know is a protocol error, not a
// default into some consequence.
func TestRefusalUnknownCandidateKindRefused(t *testing.T) {
	db := rrSpine(t)
	_, err := rrApply(t, db, RefusalCandidate{Kind: "operator"}, refusal.Refusal{
		Code: refusal.CodeEnvInvalid, Field: refusal.FieldNone, Summary: refusal.SummaryForbidden,
	}, rrKey)
	if err == nil {
		t.Fatal("an unknown candidate kind was applied instead of refused")
	}
	rrAssertInert(t, db, nil)
}

// A subject-task candidate with no task id cannot block anything. The router
// must never invent or guess a subject.
func TestRefusalSubjectTaskWithoutIDRefused(t *testing.T) {
	db := rrSpine(t)
	_, err := rrApply(t, db, RefusalCandidate{Kind: RefusalSubjectTask}, refusal.Refusal{
		Code: refusal.CodeEnvInvalid, Field: refusal.FieldNone, Summary: refusal.SummaryForbidden,
	}, rrKey)
	if err == nil {
		t.Fatal("a subject-task candidate with no id was applied instead of refused")
	}
	rrAssertInert(t, db, nil)
}

func TestRefusalCollieWithoutSessionRefused(t *testing.T) {
	db := rrSpine(t)
	_, err := rrApply(t, db, RefusalCandidate{Kind: RefusalCollie}, refusal.Refusal{
		Code: refusal.CodeEnvInvalid, Field: refusal.FieldNone, Summary: refusal.SummaryForbidden,
	}, rrKey)
	if err == nil {
		t.Fatal("a Collie candidate with no session id was applied instead of refused")
	}
	rrAssertInert(t, db, nil)
}

// --- The stored detail is leak-proof ---------------------------------------

// The router caps the stored detail at the closed
// {code,field,item_index,summary} object. The detail never carries a supplied
// path/value, env value, credential, or nonce. The producer's raw Message is
// the carrier of exactly those things, so the router must drop it. Hostile
// inputs, asserted byte-exact.
func TestRefusalStoredDetailNeverLeaks(t *testing.T) {
	const secret = "/Users/victim/.ssh/id_rsa AKIAIOSFODNN7EXAMPLE"
	idx := 7
	db := rrSpine(t)
	if _, err := rrApply(t, db, RefusalCandidate{Kind: RefusalSubjectlessPipeline, Project: "ws-test"}, refusal.Refusal{
		Code:      boundary.CodeNotAllowlisted,
		Authority: refusal.AuthorityDeployment,
		Field:     refusal.FieldMountSource,
		Summary:   refusal.SummaryNotAllowlisted,
		ItemIndex: &idx,
		Message:   secret,
	}, rrKey); err != nil {
		t.Fatalf("refusal errored: %v", err)
	}
	got := rrActivity(t, db, "dispatch.health")
	if len(got) != 1 {
		t.Fatalf("wrote %d health rows, want one", len(got))
	}
	want := `{"code":"mount.not_allowlisted","field":"mount.source","item_index":7,"summary":"not_allowlisted"}`
	if got[0] != want {
		t.Errorf("stored detail bytes:\n got %s\nwant %s", got[0], want)
	}
	if strings.Contains(got[0], "victim") || strings.Contains(got[0], "AKIA") {
		t.Errorf("the stored detail leaked the producer's raw message: %s", got[0])
	}
	if !json.Valid([]byte(got[0])) {
		t.Errorf("stored detail is not valid JSON: %s", got[0])
	}
	if len(got[0]) > 512 {
		t.Errorf("stored detail is %d bytes, over the 512-byte cap", len(got[0]))
	}
}

// The block reason is a stable code, never the producer's text.
func TestRefusalBlockReasonNeverLeaks(t *testing.T) {
	db := rrSpine(t)
	if _, err := rrApply(t, db, rrSubject(1), refusal.Refusal{
		Code: refusal.CodeEnvForbidden, Field: refusal.FieldEnvName,
		Summary: refusal.SummaryForbidden, Message: "AWS_SECRET_ACCESS_KEY=hunter2",
	}, rrKey); err != nil {
		t.Fatalf("refusal errored: %v", err)
	}
	_, reason := rrTaskBlocked(t, db, 1)
	if reason != "confinement:env.forbidden" {
		t.Errorf("blocked_reason = %q, want the stable code", reason)
	}
	if strings.Contains(reason, "hunter2") {
		t.Errorf("blocked_reason leaked a credential: %q", reason)
	}
}

// --- The replay fence -------------------------------------------------------

// The activity dispatch_key is UNIQUE, so a replayed health write cannot append
// a second row. The fence is the storage layer's; this proves the router hands
// it the key rather than writing NULL and defeating it.
func TestRefusalHealthReplayWritesNoSecondRow(t *testing.T) {
	db := rrSpine(t)
	r := refusal.Refusal{
		Code: refusal.CodeImageUnavailable, Field: refusal.FieldImage, Summary: refusal.SummaryUnavailable,
	}
	if _, err := rrApply(t, db, rrSubject(1), r, rrKey); err != nil {
		t.Fatalf("first health write errored: %v", err)
	}
	if _, err := rrApply(t, db, rrSubject(1), r, rrKey); err == nil {
		t.Fatal("a replayed health write with the same dispatch_key was accepted")
	}
	if n := len(rrActivity(t, db, "dispatch.health")); n != 1 {
		t.Errorf("replay left %d health rows, want exactly one", n)
	}
	var key sql.NullString
	if err := db.QueryRow(`SELECT dispatch_key FROM activity WHERE kind = 'dispatch.health'`).Scan(&key); err != nil {
		t.Fatalf("read dispatch_key: %v", err)
	}
	if !key.Valid || key.String != rrKey {
		t.Errorf("health row carries dispatch_key %v, want the supplied key (the fence is defeated without it)", key)
	}
}

// A different key is a different event and is admitted.
func TestRefusalHealthDistinctKeyWritesSecondRow(t *testing.T) {
	db := rrSpine(t)
	r := refusal.Refusal{
		Code: refusal.CodeImageUnavailable, Field: refusal.FieldImage, Summary: refusal.SummaryUnavailable,
	}
	if _, err := rrApply(t, db, rrSubject(1), r, rrKey); err != nil {
		t.Fatalf("first health write errored: %v", err)
	}
	if _, err := rrApply(t, db, rrSubject(1), r, strings.Repeat("b", 64)); err != nil {
		t.Fatalf("second health write errored: %v", err)
	}
	if n := len(rrActivity(t, db, "dispatch.health")); n != 2 {
		t.Errorf("wrote %d health rows, want two", n)
	}
}

// The key is an INPUT to this transaction (its derivation belongs to the
// prepare slice, which does not exist yet), so a malformed one is a protocol
// error caught here rather than a raw CHECK abort from the storage layer.
//
// Proven across EVERY class, not just one. Only the health arm writes an
// activity row, so only there does the substrate's dispatch_key CHECK fire; on
// the stale and block arms nothing would catch a malformed key but this
// router. A planted mutant that deleted the router's own check survived a
// health-only version of this test — the storage CHECK was doing the work and
// the test was asserting someone else's guarantee.
func TestRefusalMalformedDispatchKeyRefused(t *testing.T) {
	bad := map[string]string{
		"too short":  strings.Repeat("a", 63),
		"too long":   strings.Repeat("a", 65),
		"uppercase":  strings.Repeat("A", 64),
		"non hex":    strings.Repeat("g", 64),
		"nul byte":   strings.Repeat("a", 63) + "\x00",
		"whitespace": strings.Repeat("a", 63) + " ",
		"hex prefix": "0x" + strings.Repeat("a", 62),
	}
	// One code per class, so every arm is covered.
	arms := map[string]struct {
		cand RefusalCandidate
		r    refusal.Refusal
	}{
		"stale": {rrSubject(1), refusal.Refusal{
			Code: refusal.CodeStale, Field: refusal.FieldNone, Summary: refusal.SummaryMismatch}},
		"health": {rrSubject(1), refusal.Refusal{
			Code: refusal.CodeImageUnavailable, Field: refusal.FieldImage, Summary: refusal.SummaryUnavailable}},
		"candidate/task_blocked": {rrSubject(1), refusal.Refusal{
			Code: refusal.CodeEnvForbidden, Field: refusal.FieldEnvName, Summary: refusal.SummaryForbidden}},
		"candidate/subjectless": {RefusalCandidate{Kind: RefusalSubjectlessPipeline, Project: "ws-test"}, refusal.Refusal{
			Code: refusal.CodeEnvForbidden, Field: refusal.FieldEnvName, Summary: refusal.SummaryForbidden}},
		"candidate/collie": {RefusalCandidate{Kind: RefusalCollie, SessionID: "sess-1"}, refusal.Refusal{
			Code: refusal.CodeEnvForbidden, Field: refusal.FieldEnvName, Summary: refusal.SummaryForbidden}},
	}
	for name, key := range bad {
		for arm, tc := range arms {
			t.Run(name+"/"+arm, func(t *testing.T) {
				db := rrSpine(t)
				_, err := rrApply(t, db, tc.cand, tc.r, key)
				if err == nil {
					t.Fatal("a malformed dispatch_key was accepted")
				}
				rrAssertInert(t, db, nil)
				var acts int
				if err := db.QueryRow(`SELECT COUNT(*) FROM activity`).Scan(&acts); err != nil {
					t.Fatalf("count activity: %v", err)
				}
				if acts != 0 {
					t.Errorf("a malformed-key refusal wrote %d activity row(s)", acts)
				}
				if blocked, _ := rrTaskBlocked(t, db, 1); blocked {
					t.Error("a malformed-key refusal blocked the subject task")
				}
				if got := rrCollieStatus(t, db, "sess-1"); got != "active" {
					t.Errorf("a malformed-key refusal ended the Collie session (status %q)", got)
				}
			})
		}
	}
}

// Until the prepare slice derives a key, an absent one must not silently
// become a forged fence value — it stores NULL, which the UNIQUE index ignores.
func TestRefusalAbsentDispatchKeyStoresNull(t *testing.T) {
	db := rrSpine(t)
	if _, err := rrApply(t, db, rrSubject(1), refusal.Refusal{
		Code: refusal.CodeImageUnavailable, Field: refusal.FieldImage, Summary: refusal.SummaryUnavailable,
	}, ""); err != nil {
		t.Fatalf("keyless health write errored: %v", err)
	}
	var key sql.NullString
	if err := db.QueryRow(`SELECT dispatch_key FROM activity WHERE kind = 'dispatch.health'`).Scan(&key); err != nil {
		t.Fatalf("read dispatch_key: %v", err)
	}
	if key.Valid {
		t.Errorf("an absent key was stored as %q instead of NULL", key.String)
	}
}

// --- The blocked subject stays out of dispatch -----------------------------

// The block is only worth anything if it actually removes the task from
// selection: dispatchability excludes blocked rows. This is what stops
// the refused candidate being reselected every tick forever.
func TestRefusalBlockedSubjectLeavesDispatch(t *testing.T) {
	db := rrSpine(t)
	if _, err := rrApply(t, db, rrSubject(1), refusal.Refusal{
		Code: refusal.CodeEnvForbidden, Field: refusal.FieldEnvName, Summary: refusal.SummaryForbidden,
	}, rrKey); err != nil {
		t.Fatalf("refusal errored: %v", err)
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		rec, err := loadVisionRecords(ctx, q, "ws-test")
		if err != nil {
			return err
		}
		for _, task := range rec.Tasks {
			if task.ID == 1 && !task.Blocked {
				t.Error("the confined task is still projected as unblocked into dispatch")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reload records: %v", err)
	}
}

func TestRefusalHealthUsesOneProjectAllowance(t *testing.T) {
	db := rrSpine(t)
	for tick := 1; tick <= 3; tick++ {
		task := int64(1)
		if tick == 2 {
			task = 9
		}
		effect, err := rrApply(t, db, rrSubject(task), refusal.Refusal{Code: refusal.CodeImageUnavailable, Field: refusal.FieldImage, Summary: refusal.SummaryUnavailable}, rrTickKey(tick))
		if err != nil {
			t.Fatal(err)
		}
		if effect["infrastructure_remaining"] != 3-tick {
			t.Fatalf("Project allowance: %v", effect)
		}
		rrAssertInert(t, db, effect)
	}
	for _, task := range []int64{1, 9} {
		if blocked, _ := rrTaskBlocked(t, db, task); blocked {
			t.Fatal("The Project failure blocked a task")
		}
	}
	var count int
	if err := db.QueryRow(`SELECT infrastructure_failure_count FROM projects WHERE id='ws-test'`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("Project count: %d, %v", count, err)
	}
	if _, err := rrApply(t, db, rrSubject(1), refusal.Refusal{Code: refusal.CodeImageUnavailable, Field: refusal.FieldImage, Summary: refusal.SummaryUnavailable}, rrTickKey(3)); err == nil {
		t.Fatal("A replay wrote another refusal")
	}
	if err := db.QueryRow(`SELECT infrastructure_failure_count FROM projects WHERE id='ws-test'`).Scan(&count); err != nil || count != 3 {
		t.Fatal("The replay changed the count")
	}
}

func TestRefusalHealthSubjectlessUsesProjectAllowance(t *testing.T) {
	db := rrSpine(t)
	effect, err := rrApply(t, db, RefusalCandidate{Kind: RefusalSubjectlessPipeline, Project: "ws-test"}, refusal.Refusal{Code: refusal.CodeImageUnavailable, Field: refusal.FieldImage, Summary: refusal.SummaryUnavailable}, rrKey)
	if err != nil {
		t.Fatal(err)
	}
	if effect["infrastructure_remaining"] != 2 {
		t.Fatalf("Subjectless allowance: %v", effect)
	}
}

func rrTickKey(tick int) string { return fmt.Sprintf("%064x", tick) }
