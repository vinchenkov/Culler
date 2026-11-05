package deployment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A running deployment must be able to take a new release. The activation gate
// refuses a prepared resident.json whose releaseBuildId is not the one being
// activated, but the drift comparison that decides whether to rewrite it only
// read config.toml policy — so upgrading the release while the units stayed
// loaded skipped the rewrite, then failed activation on the stale id it had
// just declined to replace. --reconfigure could not help: it is reached only
// through the same policy-only comparison.
func TestSupervisionReleaseMatchesTracksThePreparedBuildID(t *testing.T) {
	home, spec := supervisionFixture(t)
	if _, _, err := PrepareSupervision(home, spec, func(string) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	matches, err := SupervisionReleaseMatches(home, spec.ReleaseBuildID)
	if err != nil || !matches {
		t.Fatalf("prepared release match = %v, err %v", matches, err)
	}
	// The policy is untouched by a release bump, which is exactly why the
	// policy comparison could not see this.
	policyMatches, err := SupervisionPolicyMatches(home, spec.SupervisionPolicy)
	if err != nil || !policyMatches {
		t.Fatalf("policy match = %v, err %v", policyMatches, err)
	}
	matches, err = SupervisionReleaseMatches(home, "0000000000000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if matches {
		t.Fatal("a prepared config from another release reported itself current")
	}
}

// A release upgrade restarts the resident, and the OUTGOING instance's receipt
// is still on disk when the incoming one is being waited for. The identity
// check ran before the freshness check, so that leftover receipt — correct for
// the release it was written under, stale by definition — was read as a hard
// mismatch on the very first poll and rolled the activation back before the
// new resident had a chance to tick even once. Freshness first: a receipt from
// before activation started is "not yet", never evidence about this release.
func TestAStaleReceiptFromThePreviousReleaseIsWaitedThrough(t *testing.T) {
	home, _ := supervisionFixture(t)
	health := filepath.Join(home, "health")
	if err := os.MkdirAll(health, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(health, "resident-tick.json")
	started := time.Now().UTC()
	writeReceipt := func(buildID string, completed time.Time) {
		t.Helper()
		body, err := json.Marshal(residentTickReceipt{
			Version: 1, ReleaseBuildID: buildID, ConfigSchemaVersion: 1,
			ResidentInstanceID: "instance-1", Sequence: 7,
			CompletedAt: completed.Format(time.RFC3339Nano),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const incoming = "1111111111111111111111111111111111111111"

	writeReceipt("0000000000000000000000000000000000000000", started.Add(-time.Minute))
	ok, err := matchingTickReceipt(path, incoming, started, started.Add(2*time.Minute))
	if err != nil || ok {
		t.Fatalf("stale predecessor receipt: ok=%v err=%v; want a quiet keep-waiting", ok, err)
	}

	// Fresh but foreign remains a hard refusal: that is a resident running the
	// wrong release right now, which activation must never accept.
	writeReceipt("2222222222222222222222222222222222222222", started.Add(time.Second))
	if ok, err = matchingTickReceipt(path, incoming, started, started.Add(2*time.Minute)); err == nil || ok {
		t.Fatalf("fresh foreign receipt: ok=%v err=%v; want a refusal", ok, err)
	}

	writeReceipt(incoming, started.Add(2*time.Second))
	if ok, err = matchingTickReceipt(path, incoming, started, started.Add(2*time.Minute)); err != nil || !ok {
		t.Fatalf("fresh matching receipt: ok=%v err=%v", ok, err)
	}
}

// launchctl bootout returns as soon as the request is accepted; the label
// survives while the resident finishes its in-flight tick and exits. The
// reconfigure path booted the units out and rewrote their configs on the very
// next statement, so a HEALTHY resident — one still mid-tick — made the rewrite
// refuse with "already loaded". A dead one unloaded fast enough to slip
// through, which is why every earlier reconfigure worked.
func TestReconfigureWaitsForBootoutToLandBeforeRewriting(t *testing.T) {
	home, spec := supervisionFixture(t)
	if _, _, err := PrepareSupervision(home, spec, func(string) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	fake := &lingeringLaunchd{unloadAfter: 3}
	// Reconfiguration proceeds past the rewrite; activation then fails for its
	// own reasons in this fixture. The rewrite refusal is the regression.
	_, _, err := ReconfigureSupervision(home, t.TempDir(), spec.ReleaseBuildID, spec,
		SupervisionActivationDeps{Launchd: fake, Now: time.Now, Sleep: func(time.Duration) {}})
	if err != nil && strings.Contains(err.Error(), "already loaded") {
		t.Fatalf("rewrite refused a label that was still unloading: %v", err)
	}
	if fake.polls == 0 {
		t.Fatal("reconfiguration never re-checked whether bootout had landed")
	}
}

// Reports a label as loaded for the first unloadAfter probes after its
// bootout, the way launchd does while the process is still shutting down.
type lingeringLaunchd struct {
	unloadAfter int
	booted      map[string]int
	polls       int
}

func (l *lingeringLaunchd) Loaded(label string) (bool, error) {
	if l.booted == nil {
		return true, nil
	}
	seen, ok := l.booted[label]
	if !ok {
		return true, nil
	}
	l.polls++
	l.booted[label] = seen + 1
	return seen < l.unloadAfter, nil
}

func (l *lingeringLaunchd) Bootout(label string) error {
	if l.booted == nil {
		l.booted = map[string]int{}
	}
	l.booted[label] = 0
	return nil
}

func (l *lingeringLaunchd) Bootstrap(_ string) error { return nil }
