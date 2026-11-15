package verbs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func logsHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("MC_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func writeLog(t *testing.T, home, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "logs", name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func logsResult(t *testing.T, res any) (lines []string, exists, truncated bool) {
	t.Helper()
	m := res.(map[string]any)
	return m["lines"].([]string), m["exists"].(bool), m["truncated"].(bool)
}

func TestLogsTailReturnsTheLastLines(t *testing.T) {
	home := logsHome(t)
	writeLog(t, home, "resident.stderr.log", "one\ntwo\nthree\nfour\n")
	res, err := LogsTail("resident", "stderr", "text", 2)
	if err != nil {
		t.Fatal(err)
	}
	lines, exists, truncated := logsResult(t, res)
	if !exists || !truncated || strings.Join(lines, "|") != "three|four" {
		t.Fatalf("tail=2 → lines=%v exists=%v truncated=%v", lines, exists, truncated)
	}
}

func TestLogsTailKeepsAFinalUnterminatedLine(t *testing.T) {
	home := logsHome(t)
	writeLog(t, home, "dashboard.stdout.log", "alpha\nbeta")
	res, err := LogsTail("dashboard", "stdout", "text", 10)
	if err != nil {
		t.Fatal(err)
	}
	lines, _, truncated := logsResult(t, res)
	if truncated || strings.Join(lines, "|") != "alpha|beta" {
		t.Fatalf("unterminated tail → lines=%v truncated=%v", lines, truncated)
	}
}

func TestLogsTailDefaultsAndClampsTheCount(t *testing.T) {
	home := logsHome(t)
	var body strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&body, "line %d\n", i)
	}
	writeLog(t, home, "resident.stderr.log", body.String())
	res, err := LogsTail("resident", "stderr", "text", 0)
	if err != nil {
		t.Fatal(err)
	}
	lines, _, _ := logsResult(t, res)
	if len(lines) != logsDefaultTail || lines[0] != "line 100" {
		t.Fatalf("default tail → %d lines, first %q", len(lines), lines[0])
	}
	res, err = LogsTail("resident", "stderr", "text", logsMaxTail+5000)
	if err != nil {
		t.Fatal(err)
	}
	lines, _, _ = logsResult(t, res)
	if len(lines) != 300 {
		t.Fatalf("clamped tail on a 300-line file → %d lines", len(lines))
	}
}

func TestLogsTailWindowsLargeFilesAndDropsThePartialFirstLine(t *testing.T) {
	home := logsHome(t)
	var body strings.Builder
	for i := 0; body.Len() <= logsWindowBytes+4096; i++ {
		fmt.Fprintf(&body, "numbered line %09d padding padding padding padding\n", i)
	}
	writeLog(t, home, "resident.stderr.log", body.String())
	res, err := LogsTail("resident", "stderr", "text", 50)
	if err != nil {
		t.Fatal(err)
	}
	lines, _, truncated := logsResult(t, res)
	if !truncated || len(lines) != 50 {
		t.Fatalf("windowed read → %d lines truncated=%v", len(lines), truncated)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "numbered line ") || !strings.HasSuffix(line, "padding") {
			t.Fatalf("windowed read returned a partial line %q", line)
		}
	}
}

func TestLogsTailTreatsAMissingLogAsAState(t *testing.T) {
	logsHome(t)
	res, err := LogsTail("resident", "stdout", "text", 10)
	if err != nil {
		t.Fatal(err)
	}
	lines, exists, _ := logsResult(t, res)
	if exists || len(lines) != 0 {
		t.Fatalf("missing file → exists=%v lines=%v", exists, lines)
	}
	// Missing logs/ directory entirely — onboarding never scaffolds it.
	t.Setenv("MC_HOME", t.TempDir())
	res, err = LogsTail("dashboard", "stderr", "text", 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists, _ = logsResult(t, res); exists {
		t.Fatal("missing logs/ directory reported exists=true")
	}
}

func TestLogsTailRefusesASymlinkedLog(t *testing.T) {
	home := logsHome(t)
	target := filepath.Join(home, "real.log")
	if err := os.WriteFile(target, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, "logs", "resident.stderr.log")); err != nil {
		t.Fatal(err)
	}
	_, err := LogsTail("resident", "stderr", "text", 10)
	if err == nil {
		t.Fatal("symlinked log did not refuse")
	}
	if _, ok := err.(*DomainError); !ok {
		t.Fatalf("symlink error = %T %v, want DomainError", err, err)
	}
}

func TestLogsTailRefusesUnknownUnitsAndStreams(t *testing.T) {
	logsHome(t)
	if _, err := LogsTail("runner", "stderr", "text", 10); err == nil {
		t.Fatal("unknown unit did not refuse")
	} else if _, ok := err.(*UsageError); !ok {
		t.Fatalf("unknown unit error = %T, want UsageError", err)
	}
	if _, err := LogsTail("resident", "syslog", "text", 10); err == nil {
		t.Fatal("unknown stream did not refuse")
	} else if _, ok := err.(*UsageError); !ok {
		t.Fatalf("unknown stream error = %T, want UsageError", err)
	}
}

// ---- the events stream, the mc unit, and --format ----

const (
	eventLine1 = `{"ts":"2026-08-18T20:00:00.000Z","level":"warn","comp":"resident","event":"reap.stop_failed","msg":"docker stop refused","container":"mc-run-ab12","exit":1,"run_id":"ab12"}`
	eventLine2 = `{"ts":"2026-08-18T20:00:01.000Z","level":"info","comp":"resident","event":"tick.completed","msg":"tick done"}`
)

func TestLogsTailRendersTheEventsStreamAsText(t *testing.T) {
	home := logsHome(t)
	writeLog(t, home, "resident.ndjson", eventLine1+"\n"+eventLine2+"\n")
	res, err := LogsTail("resident", "events", "text", 10)
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	lines := m["lines"].([]string)
	if len(lines) != 2 {
		t.Fatalf("lines=%v", lines)
	}
	// Rendered server-side: the browser keeps receiving `lines` and keeps not
	// parsing them, which is the property that stops a second renderer from
	// appearing in JavaScript.
	for _, want := range []string{"WARN", "resident", "reap.stop_failed", "docker stop refused", "run_id=ab12"} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("rendered line %q is missing %q", lines[0], want)
		}
	}
	if m["format"] != "text" || m["malformed"] != 0 {
		t.Fatalf("format=%v malformed=%v", m["format"], m["malformed"])
	}
	if _, ok := m["records"]; ok {
		t.Fatal("text format must not carry a records array")
	}
}

func TestLogsTailReturnsRecordsInJSONFormat(t *testing.T) {
	home := logsHome(t)
	writeLog(t, home, "mc.ndjson", eventLine1+"\n"+eventLine2+"\n")
	res, err := LogsTail("mc", "events", "json", 10)
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	records := m["records"].([]any)
	if len(records) != 2 {
		t.Fatalf("records=%v", records)
	}
	first := records[0].(map[string]any)
	if first["event"] != "reap.stop_failed" || first["run_id"] != "ab12" {
		t.Fatalf("record=%v", first)
	}
	// json keeps `lines` raw: the caller asked for records, not for a rendering
	// it would then have to un-render.
	if lines := m["lines"].([]string); lines[0] != eventLine1 {
		t.Fatalf("lines[0]=%q", lines[0])
	}
	// It also carries the server-side rendering, aligned 1:1 with lines, so a
	// reader that selects on records still never renders a record itself.
	rendered := m["rendered"].([]string)
	if len(rendered) != 2 || !strings.Contains(rendered[0], "reap.stop_failed") || !strings.Contains(rendered[0], "WARN") {
		t.Fatalf("rendered=%v", rendered)
	}
}

// A half-written final record and a runtime abort both produce a line that is
// not a record, and that line is the interesting one. It must survive both
// formats and be counted, not dropped.
func TestLogsTailKeepsAndCountsWhatIsNotARecord(t *testing.T) {
	home := logsHome(t)
	writeLog(t, home, "resident.ndjson", eventLine1+"\nAbort trap: 6\n{\"ts\":\"trunc\n")
	text, err := LogsTail("resident", "events", "text", 10)
	if err != nil {
		t.Fatal(err)
	}
	m := text.(map[string]any)
	lines := m["lines"].([]string)
	if len(lines) != 3 || lines[1] != "Abort trap: 6" {
		t.Fatalf("lines=%v", lines)
	}
	if m["malformed"] != 2 {
		t.Fatalf("malformed=%v", m["malformed"])
	}

	asJSON, err := LogsTail("resident", "events", "json", 10)
	if err != nil {
		t.Fatal(err)
	}
	j := asJSON.(map[string]any)
	// Aligned with `lines`: the two non-records keep their slots as nulls, so
	// the count survives without breaking the line correspondence.
	records := j["records"].([]any)
	if len(records) != 3 || records[0] == nil || records[1] != nil || records[2] != nil {
		t.Fatalf("records=%v", records)
	}
	if j["malformed"] != 2 {
		t.Fatalf("malformed=%v", j["malformed"])
	}
	rendered := j["rendered"].([]string)
	if len(rendered) != 3 || rendered[1] != "Abort trap: 6" {
		t.Fatalf("rendered=%v", rendered)
	}
}

// The prose streams hold no records, and answering with the prose under a
// `records` key would be a lie about its shape: every aligned slot is null,
// and the prose passes through as its own rendering.
func TestJSONFormatOnAProseStreamCarriesNoRecords(t *testing.T) {
	home := logsHome(t)
	writeLog(t, home, "resident.stderr.log", "boot config invalid\n")
	res, err := LogsTail("resident", "stderr", "json", 10)
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	records := m["records"].([]any)
	if len(records) != 1 || records[0] != nil {
		t.Fatalf("records=%v", records)
	}
	if lines := m["lines"].([]string); len(lines) != 1 || lines[0] != "boot config invalid" {
		t.Fatalf("lines=%v", lines)
	}
	if rendered := m["rendered"].([]string); len(rendered) != 1 || rendered[0] != "boot config invalid" {
		t.Fatalf("rendered=%v", rendered)
	}
}

// mc is not a supervised unit and has no launchd redirect, so it must refuse
// the two streams that only exist because launchd creates them.
func TestTheMCUnitHasOnlyAnEventsStream(t *testing.T) {
	logsHome(t)
	for _, stream := range []string{"stdout", "stderr"} {
		_, err := LogsTail("mc", stream, "text", 10)
		if err == nil || !strings.Contains(err.Error(), "unknown log stream") {
			t.Fatalf("mc %s = %v", stream, err)
		}
	}
}

// AgentsView writes plain text on all three streams. Its debug log lives in
// its data folder, not under logs/.
func TestTheAgentsViewUnitReadsItsThreePlainTextStreams(t *testing.T) {
	home := logsHome(t)
	writeLog(t, home, "agentsview.stderr.log", "listen tcp 127.0.0.1:7334: bind: address already in use\n")
	writeLog(t, home, "agentsview.stdout.log", "agentsview v0.44.0 listening\n")
	data := filepath.Join(home, "agentsview", "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "debug.log"), []byte(`{"level":"info","msg":"sync"}`+"\nsecond line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for stream, want := range map[string]string{
		"stderr": "listen tcp 127.0.0.1:7334: bind: address already in use",
		"stdout": "agentsview v0.44.0 listening",
		"debug":  `{"level":"info","msg":"sync"}|second line`,
	} {
		for _, format := range []string{"text", "json"} {
			res, err := LogsTail("agentsview", stream, format, 10)
			if err != nil {
				t.Fatalf("%s %s: %v", stream, format, err)
			}
			lines, exists, _ := logsResult(t, res)
			if !exists || strings.Join(lines, "|") != want {
				t.Fatalf("%s %s lines = %v", stream, format, lines)
			}
			m := res.(map[string]any)
			if m["malformed"] != 0 {
				t.Fatalf("%s %s parsed plain text as records: %v", stream, format, m)
			}
			if format == "json" {
				for _, record := range m["records"].([]any) {
					if record != nil {
						t.Fatalf("%s json carries a record: %v", stream, m["records"])
					}
				}
			}
		}
	}
	if _, err := LogsTail("agentsview", "events", "text", 10); err == nil || !strings.Contains(err.Error(), "debug|stderr|stdout") {
		t.Fatalf("agentsview events = %v", err)
	}
	if err := os.Remove(filepath.Join(data, "debug.log")); err != nil {
		t.Fatal(err)
	}
	res, err := LogsTail("agentsview", "debug", "text", 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists, _ := logsResult(t, res); exists {
		t.Fatal("a missing debug log reported that it exists")
	}
}

func TestUnknownFormatIsRefused(t *testing.T) {
	logsHome(t)
	if _, err := LogsTail("resident", "stderr", "yaml", 10); err == nil {
		t.Fatal("an unknown format was accepted")
	}
	if _, err := LogsTailRun("ab12", "yaml", 10); err == nil {
		t.Fatal("an unknown format was accepted on the run lane")
	}
}

// ---- the run lane ----

func writeRunLog(t *testing.T, home, runID, body string) {
	t.Helper()
	dir := filepath.Join(home, "logs", "runs", runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "runner.ndjson"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLogsTailRunReadsOneRunsDiagnostics(t *testing.T) {
	home := logsHome(t)
	writeRunLog(t, home, "ab12", eventLine2+"\n")
	res, err := LogsTailRun("ab12", "json", 10)
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["run_id"] != "ab12" || m["exists"] != true {
		t.Fatalf("result=%v", m)
	}
	if len(m["records"].([]any)) != 1 {
		t.Fatalf("records=%v", m["records"])
	}
}

// A run with no diagnostics is a state, not an error — the same rule the unit
// lane already follows for a unit that has never run.
func TestLogsTailRunOnAnUnknownRunIsAState(t *testing.T) {
	logsHome(t)
	res, err := LogsTailRun("neverran", "text", 10)
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["exists"] != false || len(m["lines"].([]string)) != 0 {
		t.Fatalf("result=%v", m)
	}
}

// The containment argument for the one lane that takes a name from the caller.
// Each of these must be refused BEFORE anything is opened; that they are
// refused at all is what keeps `mc logs` from being a read-anything oracle
// reachable from the browser.
func TestLogsTailRunRefusesAnythingThatIsNotARunID(t *testing.T) {
	logsHome(t)
	for _, bad := range []string{
		"", ".", "..", "../../etc/passwd", "a/b", "/etc/passwd",
		"-flag", ".hidden", "with space", "tab\there",
		strings.Repeat("x", 65),
	} {
		if _, err := LogsTailRun(bad, "text", 10); err == nil {
			t.Fatalf("run id %q was accepted", bad)
		}
	}
}

// A symlinked run directory is the one way a path built from constants and a
// charset-fenced id could still address a file somewhere else.
func TestLogsTailRunRefusesASymlinkedRunDirectory(t *testing.T) {
	home := logsHome(t)
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "runner.ndjson"), []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "logs", "runs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(home, "logs", "runs", "ab12")); err != nil {
		t.Fatal(err)
	}
	if _, err := LogsTailRun("ab12", "text", 10); err == nil {
		t.Fatal("a symlinked run directory was followed")
	}
}

func TestLogsRunsListNamesOnlyRealRunDirectories(t *testing.T) {
	home := logsHome(t)
	writeRunLog(t, home, "bb02", "")
	writeRunLog(t, home, "aa01", "")
	// Not a directory, and a name that is not a run id: neither is offered.
	if err := os.WriteFile(filepath.Join(home, "logs", "runs", "stray.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "logs", "runs", ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := LogsRunsList()
	if err != nil {
		t.Fatal(err)
	}
	runs := res.(map[string]any)["runs"].([]string)
	if strings.Join(runs, ",") != "aa01,bb02" {
		t.Fatalf("runs=%v", runs)
	}
}

func TestLogsRunsListWithNoRunsAtAll(t *testing.T) {
	logsHome(t)
	res, err := LogsRunsList()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.(map[string]any)["runs"].([]string)) != 0 {
		t.Fatalf("result=%v", res)
	}
}
