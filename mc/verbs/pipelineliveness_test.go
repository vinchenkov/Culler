package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

const livenessRunID = "0123456789abcdef"

// A lease that is FRESH by every spine-side threshold: heartbeating, nowhere
// near its timeout or hard deadline. Only a host observation can reap it, which
// is the whole point — this is exactly the shape that used to wedge the board
// for the full lease duration after a container died.
func livenessHeldLease(t *testing.T, db *sql.DB) {
	t.Helper()
	dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject) VALUES (?, 'pipeline', 'worker', 'ws-test', NULL)`,
		livenessRunID)
	dvHold(t, db, "ws-test", livenessRunID, "worker", nil, 0, new(time.Duration), 24*time.Hour)
}

func livenessPrepare(t *testing.T, db *sql.DB, observed []PrivatePipelineLiveness) map[string]any {
	t.Helper()
	req := readyPrivateRequest(t, db)
	req.PipelineLiveness = observed
	var prepared PrivateDispatchPrepareResponse
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		prepared, err = DispatchPreparePrivate(ctx, q, req)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if prepared.Kind != "final" || prepared.Final == nil {
		t.Fatalf("prepared frame = %+v, want a final", prepared)
	}
	var effect map[string]any
	if err := json.Unmarshal(*prepared.Final, &effect); err != nil {
		t.Fatal(err)
	}
	return effect
}

func TestPipelineLivenessReapsTheHolderWhoseContainerTheHostProvesGone(t *testing.T) {
	db := dvSpine(t)
	livenessHeldLease(t, db)

	effect := livenessPrepare(t, db, []PrivatePipelineLiveness{
		{LaunchAgeS: 600, Present: false, RunID: livenessRunID},
	})
	if effect["action"] != "reap" || effect["run_id"] != livenessRunID ||
		effect["reason"] != "container-gone" || effect["stop_container"] != true {
		t.Fatalf("effect = %v, want the container-gone reap of the lock holder", effect)
	}
	if got := dfStr(t, db, `SELECT run_id FROM leases WHERE project='ws-test'`); got != livenessRunID {
		t.Fatalf("the reap released the lease before a stop confirmation: %q", got)
	}
	if _, err := RunStopped(db, nil, livenessRunID); err != nil {
		t.Fatal(err)
	}
	if got := dfStr(t, db, `SELECT COALESCE(run_id, '') FROM leases WHERE project='ws-test'`); got != "" {
		t.Fatalf("the confirmed stop retained the lease: %q", got)
	}
}

func TestPipelineLivenessNeverReapsOnAnythingShortOfProof(t *testing.T) {
	for name, observed := range map[string][]PrivatePipelineLiveness{
		// No observation at all: the in-process seam, or a resident that could
		// not reach Docker. Absent evidence is not evidence of absence.
		"no observation": {},
		// An observation about some OTHER run. The resident reports a set and
		// guesses at the holder; acting on a non-holder's entry is hearsay.
		"another run": {{LaunchAgeS: 600, Present: false, RunID: strings.Repeat("a", 16)}},
		// The container was seen.
		"container present": {{LaunchAgeS: 600, Present: true, RunID: livenessRunID}},
		// Claimed moments ago on the HOST clock: the container legitimately
		// may not exist yet. This is the anti-race guard, and it is measured in
		// real time precisely because the VM clock stalls across sleep.
		"inside spawn grace": {{LaunchAgeS: 0, Present: false, RunID: livenessRunID}},
	} {
		t.Run(name, func(t *testing.T) {
			db := dvSpine(t)
			livenessHeldLease(t, db)
			effect := livenessPrepare(t, db, observed)
			if effect["action"] != "idle" {
				t.Fatalf("effect = %v, want idle: %s must never reap", effect, name)
			}
			if got := dfStr(t, db, `SELECT COALESCE(run_id, '') FROM leases WHERE project='ws-test'`); got != livenessRunID {
				t.Fatalf("lock holder = %q, want the lease untouched", got)
			}
		})
	}
}

func TestPrivatePipelineLivenessValidationRefusesEveryShapeTheResidentCannotHaveSent(t *testing.T) {
	if err := ValidatePrivatePipelineLiveness([]PrivatePipelineLiveness{}); err != nil {
		t.Fatalf("the empty observation set is legal: %v", err)
	}
	// Absent and empty must encode differently — "I sent no field" is not
	// "I observed nothing", and a carrier that conflated them would let a
	// stripped field pass as a legitimate answer.
	if ValidatePrivatePipelineLiveness(nil) == nil {
		t.Fatal("a nil observation set was accepted")
	}
	for name, observed := range map[string][]PrivatePipelineLiveness{
		"malformed run id": {{RunID: "not-hex", Present: true}},
		"short run id":     {{RunID: "abc", Present: true}},
		"negative age":     {{RunID: livenessRunID, LaunchAgeS: -1}},
		"implausible age":  {{RunID: livenessRunID, LaunchAgeS: 3651 * 24 * 3600}},
		"duplicated": {
			{RunID: livenessRunID}, {RunID: livenessRunID},
		},
		"unsorted": {
			{RunID: strings.Repeat("b", 16)}, {RunID: strings.Repeat("a", 16)},
		},
	} {
		if ValidatePrivatePipelineLiveness(observed) == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestHostLivenessSelectionCarriesTheClaimItCannotForge(t *testing.T) {
	observed := []PrivatePipelineLiveness{
		{LaunchAgeS: 5, Present: true, RunID: strings.Repeat("a", 16)},
		{LaunchAgeS: 900, Present: false, RunID: livenessRunID},
	}
	selected := hostLivenessForRun(observed, livenessRunID)
	if selected == nil || selected.RunID != livenessRunID || selected.Present ||
		selected.LaunchAgeS != 900 || !selected.LaunchAgeKnown {
		t.Fatalf("selected = %+v, want the holder's own observation", selected)
	}
	// LaunchAgeKnown has no wire representation: carrying an entry IS the
	// claim, so it can only ever be true for an entry that exists.
	if hostLivenessForRun(observed, strings.Repeat("f", 16)) != nil {
		t.Fatal("an unobserved run produced a liveness claim")
	}
	if hostLivenessForRun(observed, "") != nil {
		t.Fatal("a free lease produced a liveness claim")
	}
}

func TestPrivatePipelineLivenessIncludesMoreThanSixteenActiveRuns(t *testing.T) {
	observed := make([]PrivatePipelineLiveness, 0, 32)
	for i := 0; i < 32; i++ {
		observed = append(observed, PrivatePipelineLiveness{RunID: fmt.Sprintf("%016x", i)})
	}
	if err := ValidatePrivatePipelineLiveness(observed); err != nil {
		t.Fatalf("The active observations failed validation: %v", err)
	}
	if got := hostLivenessForRun(observed, observed[31].RunID); got == nil {
		t.Fatal("The final active run lost its observation.")
	}
}
