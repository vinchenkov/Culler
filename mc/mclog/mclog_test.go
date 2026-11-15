package mclog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openTestLogger returns a logger over a fresh directory plus that directory,
// so a test can inspect the bytes that were actually written.
func openTestLogger(t *testing.T, level Level) (*Logger, string) {
	t.Helper()
	dir := t.TempDir()
	logger, err := OpenComponent(dir, "mc", level)
	if err != nil {
		t.Fatal(err)
	}
	if logger == nil {
		t.Fatal("OpenComponent returned no logger for an existing directory")
	}
	t.Cleanup(func() { logger.Close() })
	return logger, dir
}

func readRecords(t *testing.T, dir string) []map[string]any {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, "mc.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSuffix(string(body), "\n"), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("line %q is not JSON: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func TestParseLevelFallsBackInsteadOfFailing(t *testing.T) {
	cases := map[string]Level{
		"":       LevelWarn, // empty means "unset", which is the fallback
		"INFO":   LevelInfo, // case is not part of the name
		"  warn": LevelWarn,
		"debug":  LevelDebug,
		"error":  LevelError,
		"loud":   LevelWarn, // an unknown name is a typo, not a reason to fail
		"\x00!":  LevelWarn,
	}
	for raw, want := range cases {
		if got := ParseLevel(raw, LevelWarn); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestLevelStringsAreTheWireSpellings(t *testing.T) {
	want := []string{"error", "warn", "info", "debug"}
	for i, name := range want {
		if got := Level(i).String(); got != name {
			t.Errorf("Level(%d).String() = %q, want %q", i, got, name)
		}
	}
	// An out-of-range level must still name one of the four, or neither
	// RenderText nor the TypeScript reader can classify the record.
	if got := Level(200).String(); got != "info" {
		t.Errorf("Level(200).String() = %q, want the default spelling", got)
	}
}

func TestOpenComponentTreatsAMissingDirectoryAsNoLogging(t *testing.T) {
	// This is the container containment fence: $MC_HOME/logs is not mounted
	// into the helper, so mc must log nothing there rather than scaffold a
	// log nobody can read.
	absent := filepath.Join(t.TempDir(), "logs")
	logger, err := OpenComponent(absent, "mc", LevelInfo)
	if err != nil {
		t.Fatalf("missing log directory is a state, not an error: %v", err)
	}
	if logger != nil {
		t.Fatal("OpenComponent returned a logger for a missing directory")
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatal("OpenComponent created the log directory")
	}
}

func TestEveryMethodIsSafeOnANilLogger(t *testing.T) {
	var logger *Logger // exactly what OpenComponent returns inside the helper
	logger.Error("e", "m", Ctx{"k": 1})
	logger.Warn("e", "m", nil)
	logger.Info("e", "m", Ctx{"k": "v"})
	logger.Debug("e", "m", nil)
	logger.At(LevelError, "e", "m", Ctx{"k": true})
	if scoped := logger.With(Ctx{"run": "abc"}); scoped != nil {
		t.Fatal("With on a nil logger returned a non-nil logger")
	} else {
		scoped.Info("still", "safe", nil) // the clone must be no-op too
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close on a nil logger = %v", err)
	}
}

func TestOpenComponentRefusesAComponentThatIsAPath(t *testing.T) {
	dir := t.TempDir()
	for _, comp := range []string{"", "..", ".hidden", "a/b", "a\\b", "mc runner"} {
		logger, err := OpenComponent(dir, comp, LevelInfo)
		if err == nil {
			t.Errorf("component %q was accepted", comp)
		}
		if logger != nil {
			t.Errorf("component %q returned a logger alongside its error", comp)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a refused component still created %d file(s)", len(entries))
	}
}

func TestARecordBelowTheThresholdIsNeverWritten(t *testing.T) {
	logger, dir := openTestLogger(t, LevelWarn)
	logger.Error("kept", "error", nil)
	logger.Warn("kept", "warn", nil)
	logger.Info("dropped", "info", nil)
	logger.Debug("dropped", "debug", nil)

	records := readRecords(t, dir)
	if len(records) != 2 {
		t.Fatalf("threshold warn wrote %d records, want 2", len(records))
	}
	for _, record := range records {
		if record["event"] != "kept" {
			t.Errorf("record below the threshold reached the file: %v", record)
		}
	}
}

func TestWithMergesUnderThePerCallContext(t *testing.T) {
	logger, dir := openTestLogger(t, LevelDebug)
	scoped := logger.With(Ctx{"run": "r1", "verb": "task"})
	nested := scoped.With(Ctx{"attempt": 2})
	nested.Info("cli.invoked", "done", Ctx{"verb": "config"})

	records := readRecords(t, dir)
	if len(records) != 1 {
		t.Fatalf("wrote %d records, want 1", len(records))
	}
	record := records[0]
	if record["run"] != "r1" || record["attempt"] != float64(2) {
		t.Errorf("bound context missing: %v", record)
	}
	// The call site is more specific than the scope that bound the field.
	if record["verb"] != "config" {
		t.Errorf("verb = %v, want the per-call value", record["verb"])
	}

	// Binding must not mutate the parent, or a scope would leak sideways into
	// every other clone of it.
	logger.Info("cli.invoked", "done", nil)
	records = readRecords(t, dir)
	if _, bled := records[1]["run"]; bled {
		t.Errorf("With mutated its parent's context: %v", records[1])
	}
}

func TestWithSharesTheParentsFile(t *testing.T) {
	logger, dir := openTestLogger(t, LevelDebug)
	scoped := logger.With(Ctx{"run": "r1"})
	logger.Info("a", "first", nil)
	scoped.Info("b", "second", nil)

	records := readRecords(t, dir)
	if len(records) != 2 {
		t.Fatalf("clone wrote to a different file: %d records in %s", len(records), dir)
	}
	if records[0]["event"] != "a" || records[1]["event"] != "b" {
		t.Errorf("clone and parent did not share one append order: %v", records)
	}
}

func TestCloseStopsEveryClone(t *testing.T) {
	logger, dir := openTestLogger(t, LevelDebug)
	scoped := logger.With(Ctx{"run": "r1"})
	logger.Info("before", "kept", nil)
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil", err)
	}
	// A clone shares the descriptor, so writing through it after Close must
	// drop the record rather than write to whatever now owns that fd number.
	scoped.Info("after", "dropped", nil)

	records := readRecords(t, dir)
	if len(records) != 1 || records[0]["event"] != "before" {
		t.Fatalf("records after Close = %v", records)
	}
}

func TestOpenComponentAppendsToAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mc.ndjson")
	if err := os.WriteFile(path, []byte("{\"pre\":true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	logger, err := OpenComponent(dir, "mc", LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	logger.Info("cli.invoked", "done", nil)

	records := readRecords(t, dir)
	if len(records) != 2 || records[0]["pre"] != true {
		t.Fatalf("OpenComponent truncated an existing log: %v", records)
	}
}

func TestOpenComponentUsesTheDeclaredRotationCap(t *testing.T) {
	// The seam the rotation tests drive must default to the exported constant,
	// or those tests would prove nothing about production.
	logger, _ := openTestLogger(t, LevelInfo)
	if logger.shared.maxBytes != ROTATE_MAX_BYTES {
		t.Fatalf("cap = %d, want ROTATE_MAX_BYTES (%d)", logger.shared.maxBytes, ROTATE_MAX_BYTES)
	}
}
