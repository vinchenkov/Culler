package dispatch

import (
	"testing"
	"time"
)

func TestProjectInfrastructureStopsAssignedWorkWithoutPause(t *testing.T) {
	rec := recs([]Task{tk(1, StatusSeeded)}, nil)
	rec.InfrastructureFailureCount = 3
	a := decide(t, rec, freeLock())
	if a.Kind != KindIdle || a.Idle != "project-infrastructure-exhausted" {
		t.Fatalf("Exhausted Project: %+v", a)
	}
	if rec.ProjectStatus == "paused" || rec.Tasks[0].Blocked || rec.Tasks[0].Stalled {
		t.Fatal("The Project hold changed an independent condition")
	}
}

func TestSharedProposalDecisionPermitsSeededWork(t *testing.T) {
	rec := recs([]Task{tk(1, StatusProposed), tk(2, StatusSeeded)}, nil)
	rec.ProposalDecisionPending = true
	wantSpawn(t, decide(t, rec, freeLock()), RoleWorker, pi64(2))
	rec.Tasks = rec.Tasks[:1]
	a := decide(t, rec, freeLock())
	if a.Kind != KindIdle || a.Idle != "proposal-decision-pending" {
		t.Fatalf("Proposal decision: %+v", a)
	}
}

func TestTaskRunRetryWaitDoesNotStopAnotherTask(t *testing.T) {
	rec := recs([]Task{tk(1, StatusSeeded), tk(2, StatusSeeded)}, nil)
	rec.Tasks[0].NextRetryAt = base.Add(time.Minute).Format(time.RFC3339)
	wantSpawn(t, decide(t, rec, freeLock()), RoleWorker, pi64(2))
	if rec.Tasks[0].ExecutionHeld {
		t.Fatal("Dispatch changed its input")
	}
}
