package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc/mclog"
)

func TestInvocationShapeKeepsNamesAndDropsValues(t *testing.T) {
	cases := []struct {
		args             []string
		verb, sub, flags string
	}{
		{[]string{"task", "list"}, "task", "list", ""},
		{[]string{"activity", "list", "--kind", "dispatch.health", "--limit", "50"}, "activity", "list", "kind,limit"},
		{[]string{"collie", "send", "--session", "h-1", "--body", "ship it please"}, "collie", "send", "body,session"},
		{[]string{"doctor"}, "doctor", "", ""},
		{[]string{"logs", "--stream=events", "--tail=20"}, "logs", "", "stream,tail"},
		// A leading flag is not a subverb, and a repeated flag is one name.
		{[]string{"packet", "--limit", "5", "--limit", "6"}, "packet", "", "limit"},
		// Bare "-" and "--" are argument syntax, not flags.
		{[]string{"land", "--", "-"}, "land", "", ""},
		// A verb whose first positional is a VALUE, not a subverb. Both of
		// these reached the live deployment's log as a `sub` field: an
		// absolute path and a task id.
		{[]string{"__mount-recheck", "/Users/op/.culler/runs/7a75.mounts.json"}, "__mount-recheck", "", ""},
		{[]string{"complete", "1"}, "complete", "", ""},
		{[]string{"task", "get", "42"}, "task", "get", ""},
		// An unknown first token is the operator's text, so it is counted but
		// not quoted back into the record.
		{[]string{"/Users/op/notes.md"}, "(value)", "", ""},
		// A flag-shaped unknown verb is a name, and reads as one.
		{[]string{"--version"}, "--version", "", ""},
	}
	for _, c := range cases {
		verb, sub, flags := invocationShape(c.args)
		if verb != c.verb || sub != c.sub || flags != c.flags {
			t.Errorf("invocationShape(%v) = (%q,%q,%q), want (%q,%q,%q)",
				c.args, verb, sub, flags, c.verb, c.sub, c.flags)
		}
	}
}

// The property that matters more than any single case: no positional value
// reaches the record. Free operator text, task ids, and paths are all
// positional or flag VALUES, and the log is not a second copy of any of them.
func TestInvocationShapeLeaksNoArgumentValue(t *testing.T) {
	secret := "correct-horse-battery-staple"
	cases := []struct {
		args   []string
		values []string // the tokens that are VALUES and must not be recorded
	}{
		{[]string{"collie", "send", "--session", secret, "--body", secret, secret}, []string{secret}},
		{[]string{"collie", "--flag=" + secret}, []string{secret}},
		// First position is where the values actually leaked: each of these
		// was written to the live deployment's log as a `sub` field.
		{[]string{"__mount-recheck", "/Users/op/.culler/runs/7a75.mounts.json"},
			[]string{"/Users/op/.culler/runs/7a75.mounts.json"}},
		{[]string{"land-sealed", "/var/folders/f0/T/Test123/001/landing.json"},
			[]string{"/var/folders/f0/T/Test123/001/landing.json"}},
		{[]string{"complete", "1"}, []string{"1"}},
		{[]string{"task", "get", "9182"}, []string{"9182"}},
		// Free operator text in first position carries a space, a
		// capital, or punctuation, so it cannot pass for a name.
		{[]string{"note", "Ship it, please"}, []string{"Ship it, please"}},
		{[]string{"/Users/op/notes.md"}, []string{"/Users/op/notes.md"}},
	}
	for _, c := range cases {
		verb, sub, flags := invocationShape(c.args)
		for _, value := range c.values {
			for i, got := range []string{verb, sub, flags} {
				if got != "" && strings.Contains(got, value) {
					t.Errorf("invocationShape(%v) field %d leaked %q: %q", c.args, i, value, got)
				}
			}
		}
		if strings.ContainsAny(verb+sub, "/ .") {
			t.Errorf("invocationShape(%v) recorded a path or free text: verb=%q sub=%q", c.args, verb, sub)
		}
	}
}

func openTestCLILogger(t *testing.T, level mclog.Level) (*mclog.Logger, string) {
	t.Helper()
	dir := t.TempDir()
	lg, err := mclog.OpenComponent(dir, "mc", level)
	if err != nil {
		t.Fatalf("open mc logger: %v", err)
	}
	if lg == nil {
		t.Fatal("OpenComponent returned no logger for a directory that exists")
	}
	return lg, filepath.Join(dir, "mc.ndjson")
}

func readRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	records := []map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		records = append(records, rec)
	}
	return records
}

func withCLILog(t *testing.T, lg *mclog.Logger) {
	t.Helper()
	previous := cliLog
	cliLog = lg
	t.Cleanup(func() { cliLog = previous })
}

func TestLogInvocationRecordsTheShapeAndOutcome(t *testing.T) {
	lg, path := openTestCLILogger(t, mclog.LevelDebug)
	withCLILog(t, lg)
	logInvocation([]string{"packet", "decide", "--packet", "7", "--verdict", "accept"}, 0, time.Now())
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}

	records := readRecords(t, path)
	if len(records) != 1 {
		t.Fatalf("want exactly one record, got %d", len(records))
	}
	rec := records[0]
	for key, want := range map[string]any{
		"comp":  "mc",
		"event": "cli.invoked",
		"level": "info",
		"msg":   "mc packet decide",
		"verb":  "packet",
		"sub":   "decide",
		"flags": "packet,verdict",
		"scope": "host",
		"pid":   float64(os.Getpid()),
	} {
		if got := rec[key]; got != want {
			t.Errorf("record[%q] = %v, want %v", key, got, want)
		}
	}
	if rec["argc"] != float64(6) || rec["exit"] != float64(0) {
		t.Errorf("argc/exit wrong: %v %v", rec["argc"], rec["exit"])
	}
	if _, ok := rec["dur_ms"]; !ok {
		t.Error("no dur_ms on the invocation record")
	}
	// The verdict value is the whole point of the refusal above: it must not be
	// in the record even though its flag name is.
	if strings.Contains(string(mustJSON(t, rec)), "accept") {
		t.Errorf("a flag VALUE reached the record: %v", rec)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The read/write split is the reason the default level is usable at all: a
// deployment at info records what changed, and nothing else.
func TestReadVerbsAreDebugAndMutationsAreInfo(t *testing.T) {
	lg, path := openTestCLILogger(t, mclog.LevelInfo)
	withCLILog(t, lg)
	logInvocation([]string{"task", "list"}, 0, time.Now())
	logInvocation([]string{"activity", "list", "--limit", "10"}, 0, time.Now())
	logInvocation([]string{"logs", "resident"}, 0, time.Now())
	logInvocation([]string{"task", "add", "--title", "x"}, 0, time.Now())
	// Unclassified: assumed to mutate, so it is recorded at the default level.
	logInvocation([]string{"some-future-verb"}, 0, time.Now())
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}

	records := readRecords(t, path)
	got := []string{}
	for _, rec := range records {
		got = append(got, rec["msg"].(string))
	}
	want := []string{"mc task add", "mc some-future-verb"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("at info the file should hold only the mutations.\n got: %v\nwant: %v", got, want)
	}

	// The same traffic at debug records everything, which is what the knob is
	// for. `mc logs resident` has "resident" as a subverb, hence the key.
	lg2, path2 := openTestCLILogger(t, mclog.LevelDebug)
	withCLILog(t, lg2)
	logInvocation([]string{"task", "list"}, 0, time.Now())
	logInvocation([]string{"logs", "resident"}, 0, time.Now())
	if err := lg2.Close(); err != nil {
		t.Fatal(err)
	}
	if n := len(readRecords(t, path2)); n != 2 {
		t.Fatalf("at debug both reads should be recorded, got %d", n)
	}
}

// The whole logging path must be inert when there is no log directory, because
// that is the state inside the helper container and inside every agent
// container. A nil logger that panicked would take down the verb with it.
func TestANilCLILogIsSilentAndSafe(t *testing.T) {
	withCLILog(t, nil)
	logInvocation([]string{"task", "list"}, 0, time.Now())
	cliLog.Warn("cli.verb_refused", "refused", mclog.Ctx{"code": "usage"})
}

// OpenComponent must not create the directory: whether $MC_HOME/logs exists is
// the switch that decides whether this process logs, and a CLI that
// materialized the directory would defeat the container fence.
func TestOpenCLILoggerDoesNotCreateTheLogDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MC_HOME", home)
	if lg := openCLILogger(); lg != nil {
		_ = lg.Close()
		t.Fatal("openCLILogger produced a logger with no logs directory present")
	}
	if _, err := os.Stat(filepath.Join(home, "logs")); !os.IsNotExist(err) {
		t.Fatalf("openCLILogger created the log directory: %v", err)
	}
	if err := os.Mkdir(filepath.Join(home, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	lg := openCLILogger()
	if lg == nil {
		t.Fatal("openCLILogger produced no logger with the directory present")
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
}

// An unresolvable MC_HOME must not be an error: mc runs identically with or
// without a deployment, and refusing to work because the log could not be
// opened would be the worst possible trade.
func TestOpenCLILoggerSurvivesAnUnusableHome(t *testing.T) {
	t.Setenv("MC_HOME", "relative/not/absolute")
	if lg := openCLILogger(); lg != nil {
		_ = lg.Close()
		t.Fatal("openCLILogger accepted a home CanonicalHome refuses")
	}
}

// On darwin every ordinary verb self-delegates into the helper, where
// $MC_HOME/logs is not visible and the inner writeVerbError has a nil logger.
// Without the tee the refusal reaches no host-side record at all, so these two
// small pieces carry the whole delegated refusal path.
func TestBoundedTeeKeepsTheOpeningBytesAndReportsFullWrites(t *testing.T) {
	tee := &boundedTee{limit: 19}
	n, err := tee.Write([]byte("mc: refused because"))
	if err != nil || n != 19 {
		t.Fatalf("Write = (%d, %v); a tee must never short-write or fail its writer", n, err)
	}
	if _, err := tee.Write([]byte(" of a later reason nobody will read")); err != nil {
		t.Fatal(err)
	}
	// First bytes, not last: a refusal says what it refused on its opening line.
	if got := tee.text(); got != "refused because" {
		t.Fatalf("tee.text() = %q", got)
	}
}

func TestEnvelopeCodeReadsTheRefusalCodeOrSaysItCouldNot(t *testing.T) {
	cases := map[string]string{
		`{"error":{"code":"usage","message":"unknown verb"}}`: "usage",
		"  {\"error\":{\"code\":\"packet-not-ready\"}}\n":     "packet-not-ready",
		`{"error":{"code":"","message":"x"}}`:                 "delegated",
		`{"ok":true}`:                                         "delegated",
		`{"error":{"code":"truncated`:                         "delegated",
		``:                                                    "delegated",
	}
	for captured, want := range cases {
		if got := envelopeCode([]byte(captured)); got != want {
			t.Errorf("envelopeCode(%q) = %q, want %q", captured, got, want)
		}
	}
}
