package verbs

// The resident reads the active leases and the active wiki update runs, and
// observes their containers.
// The mc program selects each Project's observation and decides whether its run requires a stop.

import (
	"mc/dispatch"
)

// PrivatePipelineLiveness is one host observation of one pipeline run. Field
// order is the wire order: the resident's TypeScript object literal is
// re-marshalled and byte-compared at both hops, so these fields and the JSON
// names must stay in this declaration order.
type PrivatePipelineLiveness struct {
	// LaunchAgeS is the age of the run's launch record in whole seconds of
	// host time, never spine time. The two clocks disagree by minutes across
	// sleep, so a mixed comparison early-reaps a run that just claimed.
	LaunchAgeS int `json:"launch_age_s"`
	// Present is true when a container for this run was observed. False means
	// Docker answered "no such container". Every ambiguity omits the entry
	// instead of reporting absence.
	Present bool   `json:"present"`
	RunID   string `json:"run_id"`
}

// A launch record older than this is not a plausible observation of a live
// deployment's run; it is a clock fault. Refusing it keeps a nonsense age from
// arithmetically satisfying the spawn-grace comparison.
const maxPipelineLaunchAgeS = 3650 * 24 * 3600

// ValidatePrivatePipelineLiveness runs at all three boundaries: broker-side
// after the control read, constructor-side, and helper-side at the top of the
// prepare. The helper never trusts the broker's shape.
//
// nil is refused and the empty set is legal. "I observed nothing" must encode
// differently from "I sent you no field at all".
func ValidatePrivatePipelineLiveness(observed []PrivatePipelineLiveness) error {
	if observed == nil {
		return Domainf("dispatch: pipeline liveness observation set is absent")
	}
	last := ""
	for _, item := range observed {
		if !validLowercaseHex(item.RunID, 16) {
			return Domainf("dispatch: pipeline liveness observation carries a malformed run id")
		}
		if item.LaunchAgeS < 0 || item.LaunchAgeS > maxPipelineLaunchAgeS {
			return Domainf("dispatch: pipeline liveness observation carries an implausible launch age")
		}
		if last != "" && item.RunID <= last {
			return Domainf("dispatch: pipeline liveness observation set is duplicated or unsorted")
		}
		last = item.RunID
	}
	return nil
}

// hostLivenessForRun selects the observation naming this run and discards the
// rest. An entry is the claim that the run was observed, which is why
// LaunchAgeKnown has no wire form: the resident cannot forge it apart from the
// measurement. No entry yields nil, and nil never reaps.
func hostLivenessForRun(observed []PrivatePipelineLiveness, runID string) *dispatch.HostLiveness {
	if runID == "" {
		return nil
	}
	for _, item := range observed {
		if item.RunID != runID {
			continue
		}
		return &dispatch.HostLiveness{
			RunID:          item.RunID,
			Present:        item.Present,
			LaunchAgeS:     item.LaunchAgeS,
			LaunchAgeKnown: true,
		}
	}
	return nil
}
