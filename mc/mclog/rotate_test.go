package mclog

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// openCapped builds a logger whose rotation cap is small enough to drive the
// ring in a test. Production always uses ROTATE_MAX_BYTES; see the seam's note
// on logFile.
func openCapped(t *testing.T, dir string, cap int64) *Logger {
	t.Helper()
	logger, err := OpenComponent(dir, "mc", LevelDebug)
	if err != nil {
		t.Fatal(err)
	}
	logger.shared.maxBytes = cap
	t.Cleanup(func() { logger.Close() })
	return logger
}

// collectSequences reads every generation of the log — the live file and each
// gzip in the ring — and returns the "seq" of every record it found, plus the
// number of lines that were not parseable JSON.
func collectSequences(t *testing.T, dir string) (seqs map[int]int, malformed int) {
	t.Helper()
	seqs = map[int]int{}
	paths := []string{filepath.Join(dir, "mc.ndjson")}
	for i := ROTATE_KEEP; i >= 1; i-- {
		paths = append(paths, filepath.Join(dir, fmt.Sprintf("mc.ndjson.%d.gz", i)))
	}
	for _, path := range paths {
		f, err := os.Open(path)
		if os.IsNotExist(err) {
			continue // an unfilled generation is normal
		}
		if err != nil {
			t.Fatal(err)
		}
		var reader io.Reader = f
		if strings.HasSuffix(path, ".gz") {
			zr, err := gzip.NewReader(f)
			if err != nil {
				f.Close()
				t.Fatalf("%s is not a valid gzip stream: %v", filepath.Base(path), err)
			}
			defer zr.Close()
			reader = zr
		}
		body, err := io.ReadAll(reader)
		f.Close()
		if err != nil {
			t.Fatalf("read %s: %v", filepath.Base(path), err)
		}
		for _, line := range strings.Split(strings.TrimSuffix(string(body), "\n"), "\n") {
			if line == "" {
				continue
			}
			var record struct {
				Seq *int `json:"seq"`
			}
			if err := json.Unmarshal([]byte(line), &record); err != nil || record.Seq == nil {
				malformed++
				continue
			}
			seqs[*record.Seq]++
		}
	}
	return seqs, malformed
}

func TestRotationShiftsTheRingAndLosesNoRecord(t *testing.T) {
	dir := t.TempDir()
	logger := openCapped(t, dir, 4096)
	const total = 120 // enough for three generations at this cap, so nothing ages out
	for i := 0; i < total; i++ {
		logger.Info("cli.invoked", "a record long enough to fill the cap quickly", Ctx{"seq": i})
	}

	if _, err := os.Stat(filepath.Join(dir, "mc.ndjson.1.gz")); err != nil {
		t.Fatalf("no generation was compressed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mc.ndjson.2.gz")); err != nil {
		t.Fatalf("the ring did not shift: %v", err)
	}
	// Nothing intermediate may survive: a leftover .rotating or .next means a
	// later rotation will trip over it.
	for _, leftover := range []string{"mc.ndjson.rotating", "mc.ndjson.1.gz.next", "mc.ndjson.rotate.lock"} {
		if _, err := os.Stat(filepath.Join(dir, leftover)); !os.IsNotExist(err) {
			t.Errorf("%s was left behind", leftover)
		}
	}

	seqs, malformed := collectSequences(t, dir)
	if malformed != 0 {
		t.Errorf("%d unparseable lines across the ring", malformed)
	}
	// The rename is the moment a record can vanish. Every one must be in
	// exactly one generation — not zero, and not two.
	for i := 0; i < total; i++ {
		if seqs[i] != 1 {
			t.Fatalf("seq %d appears %d times across the ring", i, seqs[i])
		}
	}
	if len(seqs) != total {
		t.Fatalf("the ring holds %d distinct records, want %d", len(seqs), total)
	}
}

func TestTheRingKeepsOnlyItsDeclaredGenerations(t *testing.T) {
	dir := t.TempDir()
	logger := openCapped(t, dir, 2048)
	for i := 0; i < 900; i++ {
		logger.Info("cli.invoked", "a record long enough to fill the cap quickly", Ctx{"seq": i})
	}
	for i := 1; i <= ROTATE_KEEP; i++ {
		if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("mc.ndjson.%d.gz", i))); err != nil {
			t.Errorf("generation %d is missing after many rotations: %v", i, err)
		}
	}
	beyond := filepath.Join(dir, fmt.Sprintf("mc.ndjson.%d.gz", ROTATE_KEEP+1))
	if _, err := os.Stat(beyond); !os.IsNotExist(err) {
		t.Errorf("the ring grew past ROTATE_KEEP generations")
	}
}

func TestALiveLockMakesRotationOvershootRatherThanStall(t *testing.T) {
	dir := t.TempDir()
	logger := openCapped(t, dir, 2048)
	lock := filepath.Join(dir, "mc.ndjson.rotate.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		logger.Info("cli.invoked", "a record long enough to fill the cap quickly", Ctx{"seq": i})
	}
	if _, err := os.Stat(filepath.Join(dir, "mc.ndjson.1.gz")); !os.IsNotExist(err) {
		t.Fatal("rotation ran while another appender held the lock")
	}
	// Appending must have continued regardless: the cap is a target, a stalled
	// verb is not an acceptable way to hold it.
	info, err := os.Stat(filepath.Join(dir, "mc.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= 2048 {
		t.Fatalf("appending stopped at the cap: %d bytes", info.Size())
	}
	seqs, malformed := collectSequences(t, dir)
	if malformed != 0 || len(seqs) != 200 {
		t.Fatalf("blocked rotation cost records: %d present, %d malformed", len(seqs), malformed)
	}
}

func TestAStaleLockIsBrokenExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	logger := openCapped(t, dir, 2048)
	lock := filepath.Join(dir, "mc.ndjson.rotate.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// An appender killed mid-rotation must not stop every later one forever.
	old := time.Now().Add(-2 * rotateLockStale)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		logger.Info("cli.invoked", "a record long enough to fill the cap quickly", Ctx{"seq": i})
	}
	if _, err := os.Stat(filepath.Join(dir, "mc.ndjson.1.gz")); err != nil {
		t.Fatalf("a stale lock blocked rotation forever: %v", err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Error("the lock outlived the rotation that broke it")
	}
}

func TestALeftoverNextIsReplacedRatherThanWedgingRotation(t *testing.T) {
	dir := t.TempDir()
	logger := openCapped(t, dir, 2048)
	// The debris an appender killed between O_EXCL and rename would leave.
	if err := os.WriteFile(filepath.Join(dir, "mc.ndjson.1.gz.next"), []byte("half a gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		logger.Info("cli.invoked", "a record long enough to fill the cap quickly", Ctx{"seq": i})
	}
	if _, err := os.Stat(filepath.Join(dir, "mc.ndjson.1.gz")); err != nil {
		t.Fatalf("a leftover .next wedged rotation: %v", err)
	}
	if _, malformed := collectSequences(t, dir); malformed != 0 {
		t.Fatalf("%d unparseable lines — the debris was compressed instead of replaced", malformed)
	}
}

func TestRotationFailureNeverReachesTheCaller(t *testing.T) {
	dir := t.TempDir()
	logger := openCapped(t, dir, 2048)
	// A directory where the .gz belongs: every rename and create against that
	// name fails, which is the closest a portable test gets to a broken disk.
	if err := os.Mkdir(filepath.Join(dir, "mc.ndjson.1.gz"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		logger.Info("cli.invoked", "a record long enough to fill the cap quickly", Ctx{"seq": i})
	}
	// The methods return nothing, so the assertion is that logging continued:
	// a failed rotation must never become a failed verb.
	body, err := os.ReadFile(filepath.Join(dir, "mc.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if len(body) == 0 {
		t.Fatal("logging stopped after a rotation failure")
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close reported a rotation failure: %v", err)
	}
}

func TestConcurrentGoroutinesNeverInterleaveARecord(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logger := openCapped(t, dir, ROTATE_MAX_BYTES)

	const writers, each = 16, 200
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			scoped := logger.With(Ctx{"writer": w})
			for i := 0; i < each; i++ {
				scoped.Info("cli.invoked", strings.Repeat("payload ", 40), Ctx{"seq": w*each + i})
			}
		}(w)
	}
	wg.Wait()

	seqs, malformed := collectSequences(t, dir)
	if malformed != 0 {
		t.Fatalf("%d lines were cut in half by a concurrent writer", malformed)
	}
	if len(seqs) != writers*each {
		t.Fatalf("%d distinct records, want %d", len(seqs), writers*each)
	}
}

func TestConcurrentAppendersOnOneFileStayWellFormedAcrossRotations(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Separate Loggers over one path share no mutex — the same situation as
	// several short-lived mc processes, which is what O_APPEND and the lock
	// file exist for. Records may be lost in the window where one appender
	// holds a renamed inode, so this asserts well-formedness, not a count.
	const appenders, each = 8, 150
	var wg sync.WaitGroup
	for a := 0; a < appenders; a++ {
		wg.Add(1)
		go func(a int) {
			defer wg.Done()
			// Opened here rather than through openCapped: a helper that calls
			// t.Fatal cannot be used from a goroutine.
			logger, err := OpenComponent(dir, "mc", LevelDebug)
			if err != nil {
				t.Error(err)
				return
			}
			defer logger.Close()
			logger.shared.maxBytes = 8192
			for i := 0; i < each; i++ {
				logger.Info("cli.invoked", strings.Repeat("payload ", 40), Ctx{"seq": a*each + i})
			}
		}(a)
	}
	wg.Wait()

	seqs, malformed := collectSequences(t, dir)
	if malformed != 0 {
		t.Fatalf("%d lines were cut in half by a concurrent appender", malformed)
	}
	if len(seqs) == 0 {
		t.Fatal("no records survived")
	}
	for seq, count := range seqs {
		if count != 1 {
			t.Fatalf("seq %d was written %d times", seq, count)
		}
	}
}
