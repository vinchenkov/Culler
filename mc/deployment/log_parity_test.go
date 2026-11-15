package deployment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"mc/mclog"
)

// One wire format, two implementations: mc/mclog in Go and runner/shared/log.ts
// in TypeScript. They were byte-identical the day they were written, and
// nothing about that survives on its own — the next person to widen a field, to
// change how a null renders, or to "tidy" the level padding will do it in one
// language and ship a log the other half cannot read the same way.
//
// The fixture is shared rather than duplicated: runner/shared/testdata/
// render-golden.json is read by BOTH this test and golden.test.ts, so a change
// that suits one implementation fails in the other. Hand-maintaining two copies
// of the expectations would just move the drift somewhere quieter.
//
// This lives in deployment/ and not in mclog/ because the fixture is outside
// the module, so Go's test cache does not track it, and a cached `ok` for a
// drift guard is the exact failure that shipped twice from the release
// manifests. The fast suite runs deployment/ with -count=1.
type renderGolden struct {
	NDJSON string `json:"ndjson"`
	Text   string `json:"text"`
}

func loadRenderGolden(t *testing.T) []renderGolden {
	t.Helper()
	path, err := filepath.Abs("../../runner/shared/testdata/render-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the shared render fixture is unreadable: %v", err)
	}
	var golden []renderGolden
	if err := json.Unmarshal(body, &golden); err != nil {
		t.Fatalf("the shared render fixture is not the expected shape: %v", err)
	}
	if len(golden) == 0 {
		t.Fatal("the shared render fixture is empty — it cannot prove anything")
	}
	return golden
}

func TestGoAndTypeScriptRenderTheSameText(t *testing.T) {
	for _, want := range loadRenderGolden(t) {
		got, ok := mclog.RenderText([]byte(want.NDJSON))
		if !ok {
			t.Fatalf("mclog.RenderText refused a line the TypeScript half renders: %s", want.NDJSON)
		}
		if got != want.Text {
			t.Fatalf("the two log renderers disagree.\n line: %s\n   go: %q\n   ts: %q",
				want.NDJSON, got, want.Text)
		}
	}
}

// A line that is not a JSON object is a state the tail must survive: the
// launchd crash channel and a half-written final record both produce them, and
// the operator still needs the rest of the window.
func TestRenderTextRefusesWhatIsNotARecord(t *testing.T) {
	for _, line := range []string{"", "   ", "not json", "[1,2,3]", `"a string"`, "42", "{oops"} {
		if got, ok := mclog.RenderText([]byte(line)); ok {
			t.Fatalf("mclog.RenderText accepted %q and produced %q", line, got)
		}
	}
}

// The key order is the half of the format the renderer cannot prove: RenderText
// sorts on read, so a writer that emitted its keys in any order would still
// render correctly here while producing files that do not diff against the
// TypeScript half's. Assert the written prefix directly.
var goldenTS = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)

func TestGoWriterEmitsTheAgreedKeyOrder(t *testing.T) {
	dir := t.TempDir()
	lg, err := mclog.OpenComponent(dir, "resident", mclog.LevelDebug)
	if err != nil {
		t.Fatalf("open component logger: %v", err)
	}
	if lg == nil {
		t.Fatal("OpenComponent returned no logger for a directory that exists")
	}
	lg.Warn("reap.stop_failed", "docker stop refused",
		mclog.Ctx{"run_id": "ab12", "container": "mc-run-ab12", "exit": 1})
	if err := lg.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, "resident.ndjson"))
	if err != nil {
		t.Fatalf("the component logger wrote no file: %v", err)
	}
	line := strings.TrimSpace(string(body))

	keys := []string{}
	dec := json.NewDecoder(strings.NewReader(line))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		t.Fatalf("the written line is not a JSON object: %q", line)
	}
	for dec.More() {
		name, err := dec.Token()
		if err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		keys = append(keys, name.(string))
		var discard any
		if err := dec.Decode(&discard); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
	}

	want := []string{"ts", "level", "comp", "event", "msg", "container", "exit", "run_id"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("the written key order drifted from the agreed format.\n got: %v\nwant: %v\nline: %s",
			keys, want, line)
	}

	// The reserved five lead in a fixed order; everything after them is sorted,
	// which is what makes two records of the same shape diff cleanly.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}
	ts, _ := parsed["ts"].(string)
	if !goldenTS.MatchString(ts) {
		t.Fatalf("ts %q is not JavaScript's toISOString() shape — the two halves timestamp differently", ts)
	}
}
