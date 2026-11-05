package main_test

import (
	"fmt"
	"testing"
)

func TestRunStopConfirmationKeepsTheLeaseUntilTheResidentConfirms(t *testing.T) {
	spine, task, run, agentEnv := workerFixture(t, "interrupted execution")
	hostEnv := spineEnv(spine)
	if result := runMC(t, hostEnv, "", "run", "stopped", run); result.code != 1 {
		t.Fatalf("a live run accepted a stop confirmation: %+v", result)
	}
	result := runMC(t, agentEnv, "", "run", "infra-failure", run, "--condition", "The adapter cannot continue.")
	if result.code != 0 || result.json["charged"] != true {
		t.Fatalf("the agent report failed: %+v", result)
	}
	if result := runMC(t, agentEnv, "", "run", "stopped", run); result.code != 1 {
		t.Fatalf("the agent confirmed its own container stop: %+v", result)
	}
	db := openDB(t, spine)
	for attempt := 0; attempt < 2; attempt++ {
		effect := tickOnlyAction(t, dispatch(t, spine))
		if effect["action"] != "reap" || effect["reason"] != "reported-outcome" || effect["run_id"] != run {
			t.Fatalf("stop request %d = %v", attempt, effect)
		}
		if got := queryStr(t, db, `SELECT run_id FROM leases WHERE project='ws-test'`); got != run {
			t.Fatal("the stop request released the lease")
		}
	}
	if got := queryInt(t, db, `SELECT infrastructure_failure_count FROM tasks WHERE id=?`, task); got != 1 {
		t.Fatalf("repeated stop requests recorded %d failures", got)
	}
	if result := runMC(t, agentEnv, "", "task", "block", fmt.Sprint(task), "--reason", "another result"); result.code != 1 {
		t.Fatalf("a late terminal replaced the failure: %+v", result)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if result := runMC(t, hostEnv, "", "run", "stopped", run); result.code != 0 {
			t.Fatalf("the resident stop confirmation failed: %+v", result)
		}
	}
	if got := queryStr(t, db, `SELECT COALESCE(run_id,'') FROM leases WHERE project='ws-test'`); got != "" {
		t.Fatal("the stop confirmation retained the lease")
	}
	if got := queryStr(t, db, `SELECT outcome FROM runs WHERE id=?`, run); got != "infra-failed" {
		t.Fatalf("cleanup replaced the failure outcome: %s", got)
	}
}
