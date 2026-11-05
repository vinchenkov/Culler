package verbs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func healthHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("MC_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

func healthFindings(t *testing.T, res any) map[string]healthFinding {
	t.Helper()
	m := res.(map[string]any)
	byCheck := map[string]healthFinding{}
	for _, finding := range m["findings"].([]healthFinding) {
		byCheck[finding.Check] = finding
	}
	return byCheck
}

func writeTickReceipt(t *testing.T, home string, sequence int64, completed time.Time) {
	t.Helper()
	dir := filepath.Join(home, "health")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"version": 1, "release_build_id": "abc", "config_schema_version": 1,
		"resident_instance_id": "i-1", "sequence": sequence,
		"completed_at": completed.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "resident-tick.json"), append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func record(level, comp, event, msg string, at time.Time) string {
	return fmt.Sprintf(`{"ts":%q,"level":%q,"comp":%q,"event":%q,"msg":%q}`,
		at.UTC().Format("2006-01-02T15:04:05.000Z07:00"), level, comp, event, msg)
}

// A deployment that has never run is not a broken one, and calling it broken
// makes the verb useless on the day someone installs it.
func TestHealthOnAFreshHomeReportsUnavailable(t *testing.T) {
	healthHome(t)
	res, err := Health(0)
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["ok"] != false {
		t.Fatalf("A fresh home has no availability evidence: %v", m)
	}
	byCheck := healthFindings(t, res)
	for _, name := range healthCheckNames() {
		finding, ok := byCheck[name]
		if !ok {
			t.Fatalf("check %q is missing from the report", name)
		}
		if finding.Status != "deferred" {
			t.Errorf("check %q on a fresh home = %q (%s)", name, finding.Status, finding.Detail)
		}
	}
}

// The whole reason the verb exists: an error record on disk is a failing
// deployment even when the spine says nothing, which is the state the
// two-day provider-quota loop was in.
func TestAnErrorRecordFailsTheReportAndReachesAttention(t *testing.T) {
	home := healthHome(t)
	now := time.Now()
	writeTickReceipt(t, home, 12, now)
	body := strings.Join([]string{
		record("info", "resident", "tick.completed", "tick done", now.Add(-3*time.Second)),
		record("warn", "resident", "spawn.refused", "provider quota exhausted", now.Add(-2*time.Second)),
		record("error", "resident", "spawn.create_failed", "docker create refused", now.Add(-time.Second)),
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(home, "logs", "resident.ndjson"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Health(0)
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["ok"] != false {
		t.Fatalf("an error record must fail the report: %v", m)
	}
	byCheck := healthFindings(t, res)
	if got := byCheck["log-resident"]; got.Status != "fail" ||
		!strings.Contains(got.Detail, "1 error(s)") || !strings.Contains(got.Detail, "1 warning(s)") {
		t.Fatalf("log-resident = %+v", got)
	}
	// The tick receipt is fresh, so the resident itself is fine — the report
	// must be able to say "ticking, and failing".
	if got := byCheck["resident-tick"]; got.Status != "ok" || !strings.Contains(got.Detail, "tick 12") {
		t.Fatalf("resident-tick = %+v", got)
	}

	// Attention carries the error AND the warning, newest last, rendered.
	attention := m["attention"].([]string)
	if len(attention) != 2 {
		t.Fatalf("attention = %v", attention)
	}
	if !strings.Contains(attention[0], "provider quota exhausted") ||
		!strings.Contains(attention[1], "docker create refused") {
		t.Fatalf("attention is not oldest-first: %v", attention)
	}
	if !strings.Contains(attention[1], "ERROR") {
		t.Fatalf("the rendered line drops the level: %q", attention[1])
	}
	// Info records are not attention. A health read that returned the whole
	// log would be `mc logs` with extra steps.
	for _, line := range attention {
		if strings.Contains(line, "tick done") {
			t.Fatalf("an info record reached attention: %v", attention)
		}
	}
}

func TestAStaleTickReceiptIsAFailure(t *testing.T) {
	home := healthHome(t)
	writeTickReceipt(t, home, 900, time.Now().Add(-2*tickReceiptStaleAfter))
	res, err := Health(0)
	if err != nil {
		t.Fatal(err)
	}
	if res.(map[string]any)["ok"] != false {
		t.Fatal("a stopped resident is not a healthy deployment")
	}
	got := healthFindings(t, res)["resident-tick"]
	if got.Status != "fail" || !strings.Contains(got.Detail, "has stopped ticking") {
		t.Fatalf("resident-tick = %+v", got)
	}
}

func TestAnUnparseableTickReceiptIsAFailureRatherThanSilence(t *testing.T) {
	home := healthHome(t)
	dir := filepath.Join(home, "health")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "resident-tick.json"), []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := healthFindings(t, mustHealth(t))["resident-tick"]
	if got.Status != "fail" {
		t.Fatalf("resident-tick = %+v", got)
	}
}

func mustHealth(t *testing.T) any {
	t.Helper()
	res, err := Health(0)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// The tail is the window each component is judged over, so an error that has
// scrolled out of it is not a current failure. Without this the verb would
// report a deployment as failing forever over one bad hour last week.
func TestTheTailBoundsTheWindowThatIsJudged(t *testing.T) {
	home := healthHome(t)
	now := time.Now()
	lines := []string{record("error", "mc", "cli.verb_refused", "the old failure", now.Add(-time.Hour))}
	for i := 0; i < 10; i++ {
		lines = append(lines, record("info", "mc", "cli.invoked", "mc task list", now))
	}
	if err := os.WriteFile(filepath.Join(home, "logs", "mc.ndjson"),
		[]byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	wide, err := Health(200)
	if err != nil {
		t.Fatal(err)
	}
	if wide.(map[string]any)["ok"] != false {
		t.Fatal("the error is inside a 200-record window and must fail")
	}
	narrow, err := Health(5)
	if err != nil {
		t.Fatal(err)
	}
	if narrow.(map[string]any)["ok"] != false {
		t.Fatalf("the error is outside a 5-record window: %v", narrow)
	}
	if narrow.(map[string]any)["tail"] != 5 {
		t.Fatalf("tail = %v", narrow.(map[string]any)["tail"])
	}
}

// A component in a tight failure loop must not make the worst moment the
// hardest one to read.
func TestAttentionIsBounded(t *testing.T) {
	home := healthHome(t)
	now := time.Now()
	lines := []string{}
	for i := 0; i < healthAttentionMax*3; i++ {
		lines = append(lines, record("error", "resident", "spawn.create_failed", "refused", now.Add(time.Duration(i)*time.Second)))
	}
	if err := os.WriteFile(filepath.Join(home, "logs", "resident.ndjson"),
		[]byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	attention := mustHealth(t).(map[string]any)["attention"].([]string)
	if len(attention) != healthAttentionMax {
		t.Fatalf("attention length = %d", len(attention))
	}
	// The newest survive: an operator wants what is failing NOW.
	if !strings.Contains(attention[len(attention)-1], now.Add(time.Duration(healthAttentionMax*3-1)*time.Second).UTC().Format("2006-01-02T15:04:05")) {
		t.Fatalf("the newest record was dropped: %q", attention[len(attention)-1])
	}
}

func TestRotatedGenerationsAreCounted(t *testing.T) {
	home := healthHome(t)
	path := filepath.Join(home, "logs", "resident.ndjson")
	if err := os.WriteFile(path, []byte(record("info", "resident", "tick.completed", "x", time.Now())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if err := os.WriteFile(fmt.Sprintf("%s.%d.gz", path, i), []byte("gz"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A hole in the ring stops the count rather than skipping it: generations
	// are consecutive by construction, and a jump means something else is
	// writing here.
	if err := os.WriteFile(fmt.Sprintf("%s.9.gz", path), []byte("gz"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := healthFindings(t, mustHealth(t))["log-resident"]
	if !strings.Contains(got.Detail, "3 rotated generation(s)") {
		t.Fatalf("log-resident = %+v", got)
	}
}

func TestRunLogsAreCountedAndNeverAFailure(t *testing.T) {
	home := healthHome(t)
	for _, id := range []string{"run-a", "run-b", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(home, "logs", "runs", id), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := healthFindings(t, mustHealth(t))["run-logs"]
	if got.Status != "ok" || !strings.Contains(got.Detail, "2 run(s)") {
		t.Fatalf("run-logs = %+v", got)
	}
}

// The verb opens no spine and spawns no container, which is the property that
// lets it answer when everything else is down. A missing MC_HOME is the only
// thing it can refuse over.
func TestHealthNeedsNothingButItsHome(t *testing.T) {
	t.Setenv("MC_HOME", filepath.Join(t.TempDir(), "never-onboarded"))
	t.Setenv("MC_SPINE", "/nonexistent/spine.db")
	res, err := Health(0)
	if err != nil {
		t.Fatalf("health refused a home with nothing in it: %v", err)
	}
	if res.(map[string]any)["ok"] != false {
		t.Fatalf("The home has no availability evidence: %v", res)
	}
}

func TestHostAvailabilityRemainsIndependentOfHistoricalErrors(t *testing.T) {
	home := healthHome(t)
	dir := filepath.Join(home, "health")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(Availability{ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), CanCommit: true})
	if err := os.WriteFile(filepath.Join(dir, "resident-availability.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "logs", "resident.ndjson"), []byte(record("error", "resident", "credential.refresh_failed", "The prior request failed.", time.Now().Add(-time.Hour))+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Health(100)
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["ok"] != true {
		t.Fatalf("History concealed current availability: %v", result)
	}
	if len(result.(map[string]any)["attention"].([]string)) != 1 {
		t.Fatal("The historical evidence disappeared.")
	}
}
