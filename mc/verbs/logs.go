package verbs

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"mc/deployment"
	"mc/mclog"
)

const (
	logsDefaultTail = 200
	logsMaxTail     = 2000    // clamped, not rejected (ActivityList precedent)
	logsWindowBytes = 1 << 20 // read at most 1 MiB from the file's end
)

// logFile names one readable file and says whether its lines are records. The
// distinction is the whole reason this table holds structs rather than strings:
// the launchd redirects carry free prose written by a process that crashed
// before its logger existed, and the .ndjson files carry records. Rendering the
// first as if it were the second would drop every line.
type logFile struct {
	path   func(home string) string
	ndjson bool
}

func underLogs(name string) func(string) string {
	return func(home string) string { return filepath.Join(home, "logs", name) }
}

// logUnits is the COMPLETE unit table. No client-supplied path ever reaches
// this verb: the path is built entirely from these constants, so containment
// holds by construction (contrast ResolveArtifactHostPath, which fences a
// spine-recorded path).
//
// Each supervised unit has three streams, and they are not three copies of one
// thing. `events` is the file the component OPENS AND OWNS — the only one that
// can be rotated, because launchd holds the other two open for the process's
// whole life. `stderr` is the crash channel: pre-logger boot validation,
// uncaught exceptions, and runtime aborts, which is exactly what is missing
// from `events` when `events` is what broke. `stdout` is structurally empty —
// nothing in this system writes to stdout by convention — and it is listed so
// that an operator who looks finds an empty file rather than a refusal.
//
// `mc` has only `events`: it is not a supervised unit, has no launchd
// redirect, and is appended to by every short-lived mc process at once.
//
// `agentsview` writes no Culler records. Its `debug` stream is the log that
// AgentsView keeps in its data folder.
var logUnits = map[string]map[string]logFile{
	"resident": {
		"stdout": {underLogs("resident.stdout.log"), false},
		"stderr": {underLogs("resident.stderr.log"), false},
		"events": {underLogs("resident.ndjson"), true},
	},
	"dashboard": {
		"stdout": {underLogs("dashboard.stdout.log"), false},
		"stderr": {underLogs("dashboard.stderr.log"), false},
		"events": {underLogs("dashboard.ndjson"), true},
	},
	"agentsview": {
		"stdout": {func(home string) string { return deployment.AgentsViewPathsFor(home).StdoutLog }, false},
		"stderr": {func(home string) string { return deployment.AgentsViewPathsFor(home).StderrLog }, false},
		"debug":  {func(home string) string { return deployment.AgentsViewPathsFor(home).DebugLog }, false},
	},
	"mc": {
		"events": {underLogs("mc.ndjson"), true},
	},
}

// logsRunID is the Go transcription of the resident's own SAFE_ID
// (resident/src/effects.ts). It excludes "/" and cannot spell "." or ".." as a
// whole component, so a run id that passes cannot address anything outside its
// own directory. The path fence below does not rely on that being true, but a
// reader of either half should be able to see why it is.
var logsRunID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// LogsTail returns the last `tail` lines of one supervision-plane log.
//
// A missing file (or a missing logs/ directory — onboarding does not scaffold
// it) is a state, not an error: a unit that has never run under supervision has
// written nothing, and the page must be able to say so.
func LogsTail(unit, stream, format string, tail int) (any, error) {
	streams, ok := logUnits[unit]
	if !ok {
		return nil, Usagef("unknown log unit %q (want resident|dashboard|agentsview|mc)", unit)
	}
	file, ok := streams[stream]
	if !ok {
		return nil, Usagef("unknown log stream %q for unit %q (want %s)", stream, unit, knownStreams(streams))
	}
	if err := validLogFormat(format); err != nil {
		return nil, err
	}
	home, err := mcHomeDir()
	if err != nil {
		return nil, err
	}
	result := map[string]any{"unit": unit, "stream": stream}
	return readLogWindow(file.path(home), unit+" "+stream, file.ndjson, format, tail, result)
}

// LogsTailRun returns the last `tail` lines of one run's runner diagnostics.
//
// It is NOT in logUnits because the filename is not a constant, which is
// precisely why every one of the fences below exists. They run in this order,
// and all of them run before anything is opened.
//
// There is deliberately no spine lookup here. `mc logs` is in the runLocal
// carve-out because it reads host files, and on the host there is no reachable
// spine — a version of this that resolved the run id through the spine would
// push the verb back into the helper, where MC_HOME is /home/mc-model/... and
// the answer is `exists:false` at exit 0: succeeding while lying.
func LogsTailRun(runID, format string, tail int) (any, error) {
	if !logsRunID.MatchString(runID) || runID == "." || runID == ".." || strings.Contains(runID, "/") {
		return nil, Usagef("run id %q is not a run id", runID)
	}
	if err := validLogFormat(format); err != nil {
		return nil, err
	}
	home, err := mcHomeDir()
	if err != nil {
		return nil, err
	}
	runsDir, err := logsRunsDir(home)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(runsDir, runID)
	path := filepath.Join(dir, "runner.ndjson")
	// Belt and braces over the charset fence: the path must be exactly what
	// Join produced, and it must still be under logs/runs/ afterwards.
	if filepath.Clean(path) != path || !strings.HasPrefix(path, runsDir+string(filepath.Separator)) {
		return nil, Usagef("run id %q is not a run id", runID)
	}
	result := map[string]any{"run_id": runID}
	if err := requireRealDirectory(dir); err != nil {
		if os.IsNotExist(err) {
			return logAbsent(result, format, tail), nil
		}
		return nil, Domainf("inspect run %s log directory: %v", runID, err)
	}
	return readLogWindow(path, "run "+runID, true, format, tail, result)
}

// LogsRunsList names the runs that have diagnostics, so the operator picks from
// a SERVER-produced list and no client-supplied path is ever needed to reach
// one. Every name is re-validated on the way out: a directory that does not
// look like a run id is not offered, whatever put it there.
func LogsRunsList() (any, error) {
	home, err := mcHomeDir()
	if err != nil {
		return nil, err
	}
	runsDir, err := logsRunsDir(home)
	if err != nil {
		return nil, err
	}
	runs := []string{}
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{"runs": runs}, nil
		}
		return nil, Domainf("list run logs: %v", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || !logsRunID.MatchString(entry.Name()) {
			continue
		}
		runs = append(runs, entry.Name())
	}
	sort.Strings(runs)
	return map[string]any{"runs": runs}, nil
}

func logsRunsDir(home string) (string, error) {
	logsDir := filepath.Join(home, "logs")
	if err := requireRealDirectory(logsDir); err != nil && !os.IsNotExist(err) {
		return "", Domainf("inspect logs directory: %v", err)
	}
	runsDir := filepath.Join(logsDir, "runs")
	if err := requireRealDirectory(runsDir); err != nil && !os.IsNotExist(err) {
		return "", Domainf("inspect run logs directory: %v", err)
	}
	return runsDir, nil
}

// requireRealDirectory refuses a symlink at any level rather than following it.
// A symlinked logs/runs/<id> is the one way a path built from constants and a
// charset-fenced id could still address a file somewhere else.
func requireRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Domainf("%s is a symlink", path)
	}
	if !info.IsDir() {
		return Domainf("%s is not a directory", path)
	}
	return nil
}

func validLogFormat(format string) error {
	if format != "text" && format != "json" {
		return Usagef("unknown log format %q (want text|json)", format)
	}
	return nil
}

func knownStreams(streams map[string]logFile) string {
	names := make([]string, 0, len(streams))
	for name := range streams {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, "|")
}

func logAbsent(result map[string]any, format string, tail int) map[string]any {
	result["tail"] = clampTail(tail)
	result["format"] = format
	result["exists"] = false
	result["truncated"] = false
	result["size_bytes"] = 0
	result["lines"] = []string{}
	result["malformed"] = 0
	if format == "json" {
		result["records"] = []any{}
		result["rendered"] = []string{}
	}
	return result
}

func clampTail(tail int) int {
	if tail <= 0 {
		return logsDefaultTail
	}
	if tail > logsMaxTail {
		return logsMaxTail
	}
	return tail
}

// readLogWindow is the one reader. The Lstat -> Open -> SameFile -> read ->
// Lstat discipline is what makes a swapped file during the read a refusal
// rather than a silent read of something else — which matters more now than it
// did, because rotation renames these files out from under a reader on purpose.
func readLogWindow(path, label string, ndjson bool, format string, tail int, result map[string]any) (any, error) {
	tail = clampTail(tail)
	result["tail"] = tail
	result["format"] = format
	before, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return logAbsent(result, format, tail), nil
		}
		return nil, Domainf("inspect %s log: %v", label, err)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, Domainf("%s log is not a regular file", label)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, Domainf("open %s log: %v", label, err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, Domainf("%s log changed during open", label)
	}
	size := opened.Size()
	offset := int64(0)
	if size > logsWindowBytes {
		offset = size - logsWindowBytes
	}
	window, err := io.ReadAll(io.NewSectionReader(f, offset, logsWindowBytes))
	if err != nil {
		return nil, Domainf("read %s log: %v", label, err)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(opened, after) {
		return nil, Domainf("%s log changed during read", label)
	}
	if offset > 0 {
		// The window almost certainly starts mid-line; drop through the first
		// newline so every returned line is whole.
		if cut := strings.IndexByte(string(window), '\n'); cut >= 0 {
			window = window[cut+1:]
		} else {
			window = nil
		}
	}
	lines := strings.Split(string(window), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	truncated := offset > 0
	if len(lines) > tail {
		truncated = true
		lines = lines[len(lines)-tail:]
	}

	result["exists"] = true
	result["truncated"] = truncated
	result["size_bytes"] = size
	result["malformed"] = 0
	result["lines"] = lines
	if !ndjson {
		if format == "json" {
			// An honest answer about a prose file: nothing here parses, so every
			// aligned record is null and the prose passes through as its own
			// rendering. Answering with the prose under `records` would be a lie
			// about its shape.
			records := make([]any, len(lines))
			result["records"] = records
			result["rendered"] = lines
		}
		return result, nil
	}

	malformed := 0
	if format == "json" {
		// Both arrays align 1:1 with `lines`, so a reader can select on a
		// record and display its rendering without ever parsing a line itself
		// — the same single-renderer rule the text branch keeps. A line that
		// is not a record keeps its slot: null record, verbatim rendering.
		records := make([]any, 0, len(lines))
		rendered := make([]string, 0, len(lines))
		for _, line := range lines {
			var record map[string]any
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				malformed++
				records = append(records, nil)
				rendered = append(rendered, line)
				continue
			}
			records = append(records, record)
			text, ok := mclog.RenderText([]byte(line))
			if !ok {
				rendered = append(rendered, line)
			} else {
				rendered = append(rendered, text)
			}
		}
		result["records"] = records
		result["rendered"] = rendered
	} else {
		// Rendered HERE, not in the browser: dashboard/src/ui/logs.js receives
		// `lines` and does not parse them, and keeping that true is what stops
		// a second, divergent renderer from appearing in JavaScript.
		rendered := make([]string, 0, len(lines))
		for _, line := range lines {
			text, ok := mclog.RenderText([]byte(line))
			if !ok {
				// A line that is not a record still belongs in the window: a
				// half-written final record and a runtime abort both produce
				// one, and dropping it hides exactly the interesting moment.
				malformed++
				rendered = append(rendered, line)
				continue
			}
			rendered = append(rendered, text)
		}
		result["lines"] = rendered
	}
	result["malformed"] = malformed
	return result, nil
}
