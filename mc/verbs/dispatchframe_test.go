package verbs

// The dispatch command frame: prepare (flock + BEGIN IMMEDIATE,
// deployment precondition, receipt fence, selection) → attest (host file
// reads, no locks) → commit (reacquire, byte-for-byte token verification,
// re-decide, exactly one consequence). These tests drive the seam functions
// directly where the fence needs a hand on the clock between the two
// transactions, and the whole verbs.Dispatch composition everywhere else.

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	operatorconfig "mc/config"
	"mc/dispatch"
	"mc/refusal"
)

const dfRequestID = "00112233445566ff"

func dfUUID(t *testing.T, db *sql.DB) string {
	t.Helper()
	var uuid string
	if err := db.QueryRow(`SELECT deployment_uuid FROM meta WHERE id = 1`).Scan(&uuid); err != nil {
		t.Fatalf("read deployment uuid: %v", err)
	}
	return uuid
}

func dfInt(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}

func dfStr(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var s string
	if err := db.QueryRow(query, args...).Scan(&s); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return s
}

func dfPrepare(t *testing.T, db *sql.DB, requestID string) preparedDispatch {
	t.Helper()
	uuid := dfUUID(t, db)
	var prepared preparedDispatch
	err := inTx(db, func(ctx context.Context, q Q) error {
		var e error
		prepared, e = dispatchPrepare(ctx, q, uuid, requestID, dvCapability(t), "ws-test")
		return e
	})
	if err != nil {
		t.Fatalf("dispatchPrepare: %v", err)
	}
	return prepared
}

func dfCommit(t *testing.T, db *sql.DB, prepared preparedDispatch, attested attestedDispatch) map[string]any {
	t.Helper()
	var effect map[string]any
	err := inTx(db, func(ctx context.Context, q Q) error {
		var e error
		effect, e = dispatchCommit(ctx, q, prepared, attested)
		return e
	})
	if err != nil {
		t.Fatalf("dispatchCommit: %v", err)
	}
	return effect
}

// dfAssertInert mirrors rrAssertInert for frame-level refusals: no Run, free
// lock, terminal refused effect, no spawn payload.
func dfAssertInert(t *testing.T, db *sql.DB, eff map[string]any) {
	t.Helper()
	if n := dfInt(t, db, `SELECT COUNT(*) FROM runs`); n != 0 {
		t.Fatalf("a refusal opened %d run rows", n)
	}
	var lockRun sql.NullString
	if err := db.QueryRow(`SELECT run_id FROM leases WHERE project='ws-test'`).Scan(&lockRun); err != nil || lockRun.Valid {
		t.Fatalf("a refusal left the lease held: %v err=%v", lockRun, err)
	}
	if eff["action"] != "refused" {
		t.Fatalf("effect action = %v, want refused", eff["action"])
	}
	if _, ok := eff["run_id"]; ok {
		t.Fatalf("a refusal effect carries a run id: %v", eff)
	}
}

func dfAssertExactReplay(t *testing.T, db *sql.DB, requestID string, want map[string]any) {
	t.Helper()
	replayed := dfPrepare(t, db, requestID)
	if replayed.final == nil {
		t.Fatal("lost-response retry did not return a stored final result")
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, err := json.Marshal(replayed.final)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("replayed result %s, want exact committed result %s", gotJSON, wantJSON)
	}
	if n := dfInt(t, db, `SELECT COUNT(*) FROM activity WHERE dispatch_request_id = ?`, requestID); n != 1 {
		t.Fatalf("request %s has %d exact-result receipt rows, want one", requestID, n)
	}
}

// --- deployment identity precondition ------------------

func TestDispatchRequiresDeploymentMirror(t *testing.T) {
	mirrorPath := func(t *testing.T) string {
		return filepath.Join(os.Getenv("MC_HOME"), "deployment.uuid")
	}

	t.Run("absent_mirror_refuses_inertly", func(t *testing.T) {
		db := dvSpine(t)
		dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
		if err := os.Remove(mirrorPath(t)); err != nil {
			t.Fatal(err)
		}
		if _, err := dvDispatchOne(db); err == nil {
			t.Fatalf("dispatch without a deployment mirror must refuse")
		} else if got := err.Error(); !strings.Contains(got, "deployment") || !strings.Contains(got, "onboard") {
			t.Fatalf("mirror-absent error should name the mirror and the repair: %q", got)
		}
		if n := dfInt(t, db, `SELECT COUNT(*) FROM runs`) + dfInt(t, db, `SELECT COUNT(*) FROM activity`); n != 0 {
			t.Fatalf("mirror-absent dispatch wrote %d rows", n)
		}
	})

	t.Run("mismatched_mirror_refuses_inertly", func(t *testing.T) {
		db := dvSpine(t)
		dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
		if err := os.WriteFile(mirrorPath(t), []byte("00000000-0000-4000-8000-000000000000\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := dvDispatchOne(db); err == nil {
			t.Fatalf("dispatch against a foreign deployment mirror must refuse")
		}
		if n := dfInt(t, db, `SELECT COUNT(*) FROM runs`) + dfInt(t, db, `SELECT COUNT(*) FROM activity`); n != 0 {
			t.Fatalf("mismatched-mirror dispatch wrote %d rows", n)
		}
	})

	t.Run("symlinked_mirror_refuses", func(t *testing.T) {
		db := dvSpine(t)
		path := mirrorPath(t)
		real := path + ".real"
		if err := os.Rename(path, real); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, path); err != nil {
			t.Fatal(err)
		}
		if _, err := dvDispatchOne(db); err == nil {
			t.Fatalf("the deployment mirror must be read no-follow")
		}
	})
}

func TestDispatchAttestReopensDeploymentMirror(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	prepared := dfPrepare(t, db, dfRequestID)
	if prepared.candidate == nil {
		t.Fatalf("prepare should return a spawn candidate, got %+v", prepared)
	}

	// The host mirror is an input to both sides of the released-lock window.
	// Swapping it after prepare must stop attest before commit can claim.
	path := filepath.Join(os.Getenv("MC_HOME"), "deployment.uuid")
	if err := os.WriteFile(path, []byte("00000000-0000-4000-8000-000000000000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := dispatchAttest(os.Getenv("MC_HOME"), prepared); err == nil {
		t.Fatal("attest accepted a deployment mirror swapped after prepare")
	} else if !strings.Contains(err.Error(), "deployment identity") {
		t.Fatalf("attest mirror-swap error = %q", err)
	}
	if n := dfInt(t, db, `SELECT COUNT(*) FROM runs`) + dfInt(t, db, `SELECT COUNT(*) FROM activity`); n != 0 {
		t.Fatalf("mirror swap wrote %d rows", n)
	}
}

// --- request receipts ------------------------------------------

func TestDispatchReapWritesReceiptAndReplaysIt(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject) VALUES ('dead-run', 'pipeline', 'worker', 'ws-test', 1)`)
	dvHold(t, db, "ws-test", "dead-run", "worker", ptr64(1), 2*time.Hour, nil, time.Hour)

	prepared := dfPrepare(t, db, dfRequestID)
	if prepared.final == nil || prepared.final["action"] != "reap" {
		t.Fatalf("prepare should finish a lock-domain reap, got %+v", prepared)
	}
	if dfStr(t, db, `SELECT outcome FROM runs WHERE id='dead-run'`) != "reaped" {
		t.Fatalf("reap consequence not applied")
	}
	stored := dfStr(t, db, `SELECT dispatch_result FROM activity WHERE dispatch_request_id = ?`, dfRequestID)
	wantJSON, err := json.Marshal(prepared.final)
	if err != nil {
		t.Fatal(err)
	}
	if stored != string(wantJSON) {
		t.Fatalf("stored receipt %q, want %q", stored, wantJSON)
	}
	retriesAfterFirst := dfInt(t, db, `SELECT dispatch_retries FROM tasks WHERE id=1`)

	// A lost-response retry replays the stored result byte-for-byte and
	// mutates nothing a second time.
	replayed := dfPrepare(t, db, dfRequestID)
	if replayed.final == nil {
		t.Fatalf("replay must return the stored final result")
	}
	replayedJSON, err := json.Marshal(replayed.final)
	if err != nil {
		t.Fatal(err)
	}
	if string(replayedJSON) != stored {
		t.Fatalf("replayed result %s, want stored %s", replayedJSON, stored)
	}
	if n := dfInt(t, db, `SELECT COUNT(*) FROM activity WHERE dispatch_request_id IS NOT NULL`); n != 1 {
		t.Fatalf("replay wrote a second receipt (%d rows)", n)
	}
	if got := dfInt(t, db, `SELECT dispatch_retries FROM tasks WHERE id=1`); got != retriesAfterFirst {
		t.Fatalf("replay re-charged the watchdog: retries %d -> %d", retriesAfterFirst, got)
	}
}

func TestDispatchIdleWritesNoReceipt(t *testing.T) {
	db := dvSpine(t)
	// A temporally fresh held lease is the one immediate idle return.
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject) VALUES ('fresh-run', 'pipeline', 'worker', 'ws-test', 1)`)
	dvHold(t, db, "ws-test", "fresh-run", "worker", ptr64(1), 0, nil, time.Hour)
	eff, err := dvDispatchOne(db)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if eff.(map[string]any)["action"] != "idle" {
		t.Fatalf("effect = %v, want idle", eff)
	}
	// A non-mutating prepare result needs no receipt.
	if n := dfInt(t, db, `SELECT COUNT(*) FROM activity`); n != 0 {
		t.Fatalf("idle wrote %d activity rows", n)
	}
}

// --- the spawn path through prepare → attest → commit -----------------------

func TestDispatchSpawnWritesDispatchKeyReceipt(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	eff, err := dvDispatchOne(db)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	effect := eff.(map[string]any)
	if effect["action"] != "spawn" {
		t.Fatalf("effect = %v, want spawn", effect)
	}
	runID := effect["run_id"].(string)
	if !validLowercaseHex(runID, 16) {
		t.Fatalf("run id %q is not 16 lowercase hex", runID)
	}
	var key string
	if err := db.QueryRow(`SELECT dispatch_key FROM activity WHERE kind='dispatch.spawn' AND subject=?`, runID).Scan(&key); err != nil {
		t.Fatalf("spawn receipt row: %v", err)
	}
	if !validDispatchKey(key) {
		t.Fatalf("spawn receipt key %q is not 64 lowercase hex", key)
	}
}

func TestDispatchAttestedConsequencesReplayExactResult(t *testing.T) {
	t.Run("spawn", func(t *testing.T) {
		db := dvSpine(t)
		dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
		prepared := dfPrepare(t, db, dfRequestID)
		attested, err := dispatchAttest(os.Getenv("MC_HOME"), prepared)
		if err != nil {
			t.Fatal(err)
		}
		effect := dfCommit(t, db, prepared, attested)
		if effect["action"] != "spawn" {
			t.Fatalf("effect = %v, want spawn", effect)
		}
		dfAssertExactReplay(t, db, dfRequestID, effect)
		if n := dfInt(t, db, `SELECT COUNT(*) FROM runs`); n != 1 {
			t.Fatalf("spawn replay left %d runs, want one", n)
		}
	})

	// A Worker spawn carries the repository plan. The replay must render the
	// same bytes, so every plan field is declared in alphabetical json order.
	t.Run("worker_spawn_with_repository", func(t *testing.T) {
		db := dvSpine(t)
		dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
		prepared := dfPrepare(t, db, dfRequestID)
		attested, err := dispatchAttest(os.Getenv("MC_HOME"), prepared)
		if err != nil {
			t.Fatal(err)
		}
		effect := dfCommit(t, db, prepared, attested)
		if effect["action"] != "spawn" || effect["role"] != "worker" {
			t.Fatalf("effect = %v, want a worker spawn", effect)
		}
		plan, _ := effect["mount_plan"].(*PrivateDispatchMountPlan)
		if plan == nil || plan.Repository == nil {
			t.Fatalf("worker spawn carries no repository plan: %v", effect["mount_plan"])
		}
		dfAssertExactReplay(t, db, dfRequestID, effect)
	})

	t.Run("deployment_health", func(t *testing.T) {
		db := dvSpine(t)
		dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
		prepared := dfPrepare(t, db, dfRequestID)
		if err := os.Remove(filepath.Join(os.Getenv("MC_HOME"), "routing.md")); err != nil {
			t.Fatal(err)
		}
		attested, err := dispatchAttest(os.Getenv("MC_HOME"), prepared)
		if err != nil {
			t.Fatal(err)
		}
		effect := dfCommit(t, db, prepared, attested)
		if effect["consequence"] != "health" {
			t.Fatalf("effect = %v, want health", effect)
		}
		dfAssertExactReplay(t, db, dfRequestID, effect)
		if n := dfInt(t, db, `SELECT COUNT(*) FROM activity WHERE kind='dispatch.health'`); n != 1 {
			t.Fatalf("health replay wrote %d health rows, want one", n)
		}
	})

	t.Run("candidate_shared_proposal_decision", func(t *testing.T) {
		db := dvSpine(t)
		dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
		prepared := dfPrepare(t, db, dfRequestID)
		attested := attestedDispatch{deploymentUUID: prepared.deploymentUUID, refusal: &refusal.Refusal{
			Code: refusal.CodeEnvForbidden, Field: refusal.FieldEnvName, Summary: refusal.SummaryForbidden,
		}}
		effect := dfCommit(t, db, prepared, attested)
		if effect["consequence"] != "proposal_decision_pending" {
			t.Fatalf("effect = %v, want proposal_decision_pending", effect)
		}
		dfAssertExactReplay(t, db, dfRequestID, effect)
		if blocked := dfInt(t, db, `SELECT blocked FROM tasks WHERE id=1`); blocked != 1 {
			t.Fatalf("task block replay left blocked=%d, want 1", blocked)
		}
	})

	t.Run("collie_end", func(t *testing.T) {
		db := rrSpine(t)
		var effect map[string]any
		err := inTx(db, func(ctx context.Context, q Q) error {
			var e error
			effect, e = applyAttestedRefusal(ctx, q, dfRequestID,
				RefusalCandidate{Kind: RefusalCollie, SessionID: "sess-1"}, refusal.Refusal{
					Code:  refusal.CodeNetworkPolicyMismatch,
					Field: refusal.FieldNetworkRule, Summary: refusal.SummaryMismatch,
				}, rrKey)
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
		if effect["consequence"] != "collie_ended" {
			t.Fatalf("effect = %v, want collie_ended", effect)
		}
		dfAssertExactReplay(t, db, dfRequestID, effect)
		if status := rrCollieStatus(t, db, "sess-1"); status != "ended" {
			t.Fatalf("Collie end replay left status=%q, want ended", status)
		}
	})
}

func TestDispatchRoutingFailureIsHealthRefusal(t *testing.T) {
	routingPath := func() string { return filepath.Join(os.Getenv("MC_HOME"), "routing.md") }

	assertHealthRefusal := func(t *testing.T, db *sql.DB, eff map[string]any) {
		t.Helper()
		dfAssertInert(t, db, eff)
		if eff["class"] != "health" || eff["code"] != refusal.CodeRoutingInvalid || eff["consequence"] != "health" {
			t.Fatalf("effect = %v, want health/health.routing_invalid/health", eff)
		}
		if blocked := dfInt(t, db, `SELECT blocked FROM tasks WHERE id=1`); blocked != 0 {
			t.Fatalf("a deployment health refusal blocked the candidate's task")
		}
		if retries := dfInt(t, db, `SELECT dispatch_retries FROM tasks WHERE id=1`); retries != 3 {
			t.Fatalf("a health refusal charged the task: retries=%d", retries)
		}
		var key string
		if err := db.QueryRow(`SELECT dispatch_key FROM activity WHERE kind='dispatch.health'`).Scan(&key); err != nil {
			t.Fatalf("health activity row: %v", err)
		}
		if !validDispatchKey(key) {
			t.Fatalf("health dispatch_key %q is not a derived 64-hex key", key)
		}
	}

	t.Run("missing_routing_md", func(t *testing.T) {
		db := dvSpine(t)
		dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
		if err := os.Remove(routingPath()); err != nil {
			t.Fatal(err)
		}
		eff, err := dvDispatchOne(db)
		if err != nil {
			t.Fatalf("Dispatch: %v", err)
		}
		assertHealthRefusal(t, db, eff.(map[string]any))
		detail := dfStr(t, db, `SELECT detail FROM activity WHERE kind='dispatch.health'`)
		want := `{"code":"health.routing_invalid","field":"routing","item_index":null,"summary":"missing"}`
		if detail != want {
			t.Fatalf("health detail = %s, want %s", detail, want)
		}
	})

	t.Run("malformed_routing_md", func(t *testing.T) {
		db := dvSpine(t)
		dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
		if err := os.WriteFile(routingPath(), []byte("not a routing table\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		eff, err := dvDispatchOne(db)
		if err != nil {
			t.Fatalf("Dispatch: %v", err)
		}
		assertHealthRefusal(t, db, eff.(map[string]any))
	})

	t.Run("disabled_binding", func(t *testing.T) {
		db := dvSpine(t)
		dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
		path := filepath.Join(os.Getenv("MC_HOME"), "config.toml")
		cfg, err := operatorconfig.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Runtimes.EnabledBindings = []string{"claude", "endpoint"}
		if _, err := operatorconfig.WriteAtomic(path, cfg); err != nil {
			t.Fatal(err)
		}
		eff, err := dvDispatchOne(db)
		if err != nil {
			t.Fatalf("Dispatch: %v", err)
		}
		assertHealthRefusal(t, db, eff.(map[string]any))
	})
}

func TestDispatchConfigFailureIsHealthRefusal(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(t *testing.T, path string)
		want  string
	}{
		{name: "missing", want: "missing", write: func(t *testing.T, path string) {
			t.Helper()
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "malformed", want: "unparsable", write: func(t *testing.T, path string) {
			t.Helper()
			if err := os.WriteFile(path, []byte("not = [valid\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := dvSpine(t)
			dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
			tc.write(t, filepath.Join(os.Getenv("MC_HOME"), "config.toml"))
			raw, err := dvDispatchOne(db)
			if err != nil {
				t.Fatal(err)
			}
			eff := raw.(map[string]any)
			dfAssertInert(t, db, eff)
			if eff["class"] != "health" || eff["code"] != refusal.CodeConfigInvalid ||
				eff["consequence"] != "health" {
				t.Fatalf("effect = %v, want config health refusal", eff)
			}
			detail := dfStr(t, db, `SELECT detail FROM activity WHERE kind='dispatch.health'`)
			want := `{"code":"health.config_invalid","field":"config","item_index":null,"summary":"` + tc.want + `"}`
			if detail != want {
				t.Fatalf("health detail = %s, want %s", detail, want)
			}
		})
	}
}

func TestDispatchUsesConfiguredCustomBindingSnapshot(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	home := os.Getenv("MC_HOME")
	configPath := filepath.Join(home, "config.toml")
	cfg, err := operatorconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	custom := cfg.Runtimes.Bindings["chatgpt"]
	custom.Model = "operator-arbiter-model"
	cfg.Runtimes.Bindings["operator-arbiter"] = custom
	cfg.Runtimes.EnabledBindings = append(cfg.Runtimes.EnabledBindings, "operator-arbiter")
	if _, err := operatorconfig.WriteAtomic(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	routingPath := filepath.Join(home, "routing.md")
	body, err := os.ReadFile(routingPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(body),
		"| arbiter | codex | chatgpt |",
		"| arbiter | codex | operator-arbiter |", 1)
	if updated == string(body) {
		t.Fatal("fixture arbiter route was not replaced")
	}
	if err := os.WriteFile(routingPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared := dfPrepare(t, db, dfRequestID)
	attested, err := dispatchAttest(home, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if attested.refusal != nil ||
		attested.route.Binding != "operator-arbiter" ||
		attested.runtimeBinding.ID != "operator-arbiter" ||
		attested.runtimeBinding.Model != "operator-arbiter-model" {
		t.Fatalf("custom configured attestation = %+v", attested)
	}
	eff := dfCommit(t, db, prepared, attested)
	if eff["action"] != "spawn" || eff["model_binding"] != "operator-arbiter" {
		t.Fatalf("custom configured dispatch = %v", eff)
	}
	// The attested capability reaches the resident, not just the action key:
	// the resident owns no route catalog, so an operator-defined binding is
	// launchable only because the effect describes it.
	runtime, ok := eff["runtime_binding"].(map[string]any)
	if !ok {
		t.Fatalf("spawn effect carries no resolved runtime capability: %v", eff)
	}
	if runtime["id"] != "operator-arbiter" || runtime["harness"] != "codex" ||
		runtime["model"] != "operator-arbiter-model" {
		t.Fatalf("spawn runtime = %v, want the attested configured binding", runtime)
	}
	want, err := runtimeBindingEffectValue(privateRuntimeBindingFromSpec(attested.runtimeBinding))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(runtime, want) {
		t.Fatalf("spawn runtime = %v, want the exact attested projection %v", runtime, want)
	}
}

// --- the TOCTOU fences between prepare and commit ---------------------------

func TestDispatchCommitStalesOnSpineDrift(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	prepared := dfPrepare(t, db, dfRequestID)
	if prepared.candidate == nil {
		t.Fatalf("prepare should return a spawn candidate, got %+v", prepared)
	}
	attested, err := dispatchAttest(os.Getenv("MC_HOME"), prepared)
	if err != nil {
		t.Fatalf("dispatchAttest: %v", err)
	}

	// Drift: reprioritize the candidate's task after prepare released its
	// transaction. Commit must refuse preflight.stale and write nothing.
	dvExec(t, db, `UPDATE tasks SET priority=0 WHERE id=1`)
	eff := dfCommit(t, db, prepared, attested)
	dfAssertInert(t, db, eff)
	if eff["code"] != refusal.CodeStale || eff["consequence"] != "none" {
		t.Fatalf("effect = %v, want preflight.stale/none", eff)
	}
	if n := dfInt(t, db, `SELECT COUNT(*) FROM activity`); n != 0 {
		t.Fatalf("a stale commit wrote %d activity rows", n)
	}
}

func TestDispatchCommitStalesOnCollieLaunchDrift(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	dvExec(t, db, `
		INSERT INTO collie_sessions (id, container_name, verb_allowlist, session_path, binding)
		VALUES ('h-aaaaaaaaaaaaaaaa', 'mc-collie-h-aaaaaaaaaaaaaaaa', '[]', 'sessions/h-a', 'claude')`)
	prepared := dfPrepare(t, db, dfRequestID)
	if prepared.candidate == nil {
		t.Fatalf("prepare should return a spawn candidate")
	}
	attested, err := dispatchAttest(os.Getenv("MC_HOME"), prepared)
	if err != nil {
		t.Fatalf("dispatchAttest: %v", err)
	}

	// Drift on the launch-generation observation: a launch bound
	// between prepare and commit changes the Collie projection, so the
	// prepared candidate no longer describes the world it was selected in.
	dvExec(t, db, `
		UPDATE collie_sessions SET current_launch_id='bbbbbbbbbbbbbbbb', current_launch_mode='fresh'
		WHERE id='h-aaaaaaaaaaaaaaaa'`)
	eff := dfCommit(t, db, prepared, attested)
	dfAssertInert(t, db, eff)
	if eff["code"] != refusal.CodeStale || eff["consequence"] != "none" {
		t.Fatalf("effect = %v, want preflight.stale/none", eff)
	}
}

func TestDispatchCommitRefusesDoctoredCandidate(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	prepared := dfPrepare(t, db, dfRequestID)
	if prepared.candidate == nil {
		t.Fatalf("prepare should return a spawn candidate")
	}
	attested, err := dispatchAttest(os.Getenv("MC_HOME"), prepared)
	if err != nil {
		t.Fatalf("dispatchAttest: %v", err)
	}

	// Doctor the candidate to a different role and recompute a token that
	// matches current spine bytes, so only the re-decide check can catch it:
	// commit must never trust the frame's own claim about what was selected.
	doctored := *prepared.candidate
	forged := *doctored.spawn
	forged.Role = dispatch.Role("worker")
	doctored.spawn = &forged
	uuid := dfUUID(t, db)
	err = inTx(db, func(ctx context.Context, q Q) error {
		sel, err := selectFromSpine(ctx, q, nil, "ws-test")
		if err != nil {
			return err
		}
		canonical, err := buildCanonicalPrepare(uuid, dfRequestID, sel.rec, sel.lease, sel.tun, sel.collies,
			doctored.mountState,
			spawnCandidateProjection(doctored.runID, doctored.spawn)).bytes()
		if err != nil {
			return err
		}
		doctored.token = preparationToken(canonical)
		return nil
	})
	if err != nil {
		t.Fatalf("forge token: %v", err)
	}
	prepared.candidate = &doctored

	eff := dfCommit(t, db, prepared, attested)
	dfAssertInert(t, db, eff)
	if eff["code"] != refusal.CodeCandidateMismatch || eff["consequence"] != "none" {
		t.Fatalf("effect = %v, want preflight.candidate_mismatch/none", eff)
	}
	if n := dfInt(t, db, `SELECT COUNT(*) FROM activity`); n != 0 {
		t.Fatalf("a mismatched commit wrote %d activity rows", n)
	}
}

// distinct request ids yield distinct tokens: the request id is bound into
// the canonical prepare bytes.
func TestDispatchTokensAreRequestScoped(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	a := dfPrepare(t, db, "aaaaaaaaaaaaaaaa")
	b := dfPrepare(t, db, "bbbbbbbbbbbbbbbb")
	if a.candidate == nil || b.candidate == nil {
		t.Fatalf("both prepares should return candidates")
	}
	if a.candidate.token == b.candidate.token {
		t.Fatalf("two request ids produced the same preparation token")
	}
}
