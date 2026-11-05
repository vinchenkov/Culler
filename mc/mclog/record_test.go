package mclog

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var fixedTime = time.Date(2026, 8, 18, 9, 12, 33, 412000000, time.UTC)

// recordKeys returns the key order as it appears in the bytes, which is what
// the format promises — decoding into a map would throw that away.
func recordKeys(t *testing.T, line []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(line))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("line does not open an object: %v %v", tok, err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		key, ok := tok.(string)
		if !ok {
			t.Fatalf("object key is %T", tok)
		}
		keys = append(keys, key)
		var discard any
		if err := dec.Decode(&discard); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

func decodeRecord(t *testing.T, line []byte) map[string]any {
	t.Helper()
	if !bytes.HasSuffix(line, []byte("\n")) {
		t.Fatal("record is not newline-terminated")
	}
	if bytes.Count(line, []byte("\n")) != 1 {
		t.Fatal("record contains an embedded newline and would split the file")
	}
	var record map[string]any
	if err := json.Unmarshal(line, &record); err != nil {
		t.Fatalf("record is not JSON: %v (%s)", err, line)
	}
	return record
}

func TestTheTimestampIsAJavaScriptISOString(t *testing.T) {
	line := encodeRecord(fixedTime, LevelWarn, "mc", "cli.invoked", "done", nil, nil)
	ts, _ := decodeRecord(t, line)["ts"].(string)
	if ts != "2026-08-18T09:12:33.412Z" {
		t.Fatalf("ts = %q, want a Z-suffixed stamp with three fractional digits", ts)
	}
	// Sub-millisecond precision is truncated, not rounded, exactly as
	// Date.toISOString does — otherwise the two halves disagree at the edge.
	odd := time.Date(2026, 8, 18, 9, 12, 33, 999999999, time.UTC)
	ts, _ = decodeRecord(t, encodeRecord(odd, LevelWarn, "mc", "e", "m", nil, nil))["ts"].(string)
	if ts != "2026-08-18T09:12:33.999Z" {
		t.Fatalf("ts = %q, want truncation to three digits", ts)
	}
	// A non-UTC instant must still be stamped as UTC; a local offset in the
	// stream would break the lexicographic sort the reader relies on.
	local := fixedTime.In(time.FixedZone("plus-two", 2*60*60))
	ts, _ = decodeRecord(t, encodeRecord(local, LevelWarn, "mc", "e", "m", nil, nil))["ts"].(string)
	if !strings.HasSuffix(ts, "Z") || ts != "2026-08-18T09:12:33.412Z" {
		t.Fatalf("ts = %q, want the UTC spelling", ts)
	}
}

func TestKeyOrderIsTheHeadThenSortedContext(t *testing.T) {
	ctx := Ctx{"zebra": 1, "alpha": "a", "middle": true, "Upper": 2}
	line := encodeRecord(fixedTime, LevelInfo, "mc", "cli.invoked", "done", nil, ctx)
	got := strings.Join(recordKeys(t, line), ",")
	// Uppercase sorts before lowercase: the order is over bytes, which is what
	// both JSON.stringify on a sorted array and sort.Strings produce.
	want := "ts,level,comp,event,msg,Upper,alpha,middle,zebra"
	if got != want {
		t.Fatalf("key order = %s, want %s", got, want)
	}
	// Stability: the same input must produce the same bytes every time, or the
	// two writers cannot be compared at all.
	for i := 0; i < 20; i++ {
		if repeat := encodeRecord(fixedTime, LevelInfo, "mc", "cli.invoked", "done", nil, ctx); !bytes.Equal(repeat, line) {
			t.Fatalf("run %d differed:\n%s\n%s", i, line, repeat)
		}
	}
}

func TestBoundContextSortsWithPerCallContext(t *testing.T) {
	// The merge happens before the sort, so a bound field is indistinguishable
	// from a per-call one in the output. Nothing marks where a field came from.
	line := encodeRecord(fixedTime, LevelInfo, "mc", "e", "m", Ctx{"run": "r1"}, Ctx{"exit": 0})
	got := strings.Join(recordKeys(t, line), ",")
	if got != "ts,level,comp,event,msg,exit,run" {
		t.Fatalf("key order = %s", got)
	}
}

func TestReservedAndNonScalarFieldsAreDroppedAndCounted(t *testing.T) {
	ctx := Ctx{
		"ts": "hijacked", "level": "hijacked", "comp": "x", "event": "y", "msg": "z",
		"slice": []int{1}, "map": map[string]int{"a": 1}, "struct": struct{ A int }{1},
		"pointer": new(int), "nan": math.NaN(), "inf": math.Inf(1),
		"kept": "yes", "count": 3,
	}
	record := decodeRecord(t, encodeRecord(fixedTime, LevelInfo, "mc", "cli.invoked", "done", nil, ctx))

	if record["comp"] != "mc" || record["event"] != "cli.invoked" || record["msg"] != "done" {
		t.Fatalf("a context field overwrote the head: %v", record)
	}
	if record["kept"] != "yes" || record["count"] != float64(3) {
		t.Fatalf("a scalar was dropped: %v", record)
	}
	// 5 reserved + slice + map + struct + pointer + NaN + Inf.
	if record["dropped_fields"] != float64(11) {
		t.Fatalf("dropped_fields = %v, want 11 (%v)", record["dropped_fields"], record)
	}
	if _, present := record["slice"]; present {
		t.Error("a non-scalar reached the record")
	}
}

func TestNoDroppedFieldsKeyWhenNothingWasDropped(t *testing.T) {
	record := decodeRecord(t, encodeRecord(fixedTime, LevelInfo, "mc", "e", "m", nil, Ctx{"ok": 1}))
	if _, present := record["dropped_fields"]; present {
		t.Fatalf("dropped_fields appeared on a clean record: %v", record)
	}
	if _, present := record["truncated"]; present {
		t.Fatalf("truncated appeared on a short record: %v", record)
	}
}

func TestNamedScalarKindsSurviveAndNilIsAValue(t *testing.T) {
	type attempt int
	record := decodeRecord(t, encodeRecord(fixedTime, LevelInfo, "mc", "e", "m", nil, Ctx{
		"attempt": attempt(7), "took": 250 * time.Millisecond, "absent": nil, "ratio": 1.5,
	}))
	if record["attempt"] != float64(7) {
		t.Errorf("a named int kind was dropped: %v", record)
	}
	if record["took"] != float64(250000000) {
		t.Errorf("a time.Duration was dropped: %v", record)
	}
	if record["ratio"] != 1.5 {
		t.Errorf("a float was dropped: %v", record)
	}
	value, present := record["absent"]
	if !present || value != nil {
		t.Errorf("nil is a value, not an absence: %v", record)
	}
	if _, dropped := record["dropped_fields"]; dropped {
		t.Errorf("a named scalar was counted as a drop: %v", record)
	}
}

func TestTheMessageCapCutsOnARuneBoundary(t *testing.T) {
	long := strings.Repeat("é", 4000) // two bytes each, so no cut is byte-aligned
	record := decodeRecord(t, encodeRecord(fixedTime, LevelInfo, "mc", "e", long, nil, nil))
	msg, _ := record["msg"].(string)
	if len(msg) > maxMsgBytes {
		t.Fatalf("msg is %d bytes, want at most %d", len(msg), maxMsgBytes)
	}
	if len(msg) < maxMsgBytes-4 {
		t.Fatalf("msg is %d bytes, far below the cap — the clamp overshot", len(msg))
	}
	if !strings.HasSuffix(msg, truncationMark) {
		t.Fatalf("a clamped msg is not marked: %q", msg[len(msg)-8:])
	}
	if !utf8.ValidString(msg) {
		t.Fatal("the clamp split a rune")
	}
	// A message at or under the cap is untouched, mark included.
	exact := strings.Repeat("a", maxMsgBytes)
	record = decodeRecord(t, encodeRecord(fixedTime, LevelInfo, "mc", "e", exact, nil, nil))
	if record["msg"] != exact {
		t.Fatal("a message exactly at the cap was clamped")
	}
}

func TestTheLineCapShedsTheLargestFieldsFirst(t *testing.T) {
	ctx := Ctx{
		"small":  "s",
		"medium": strings.Repeat("m", 512),
		"huge":   strings.Repeat("h", 20000),
	}
	line := encodeRecord(fixedTime, LevelInfo, "mc", "cli.invoked", "done", nil, ctx)
	if len(line) > maxLineBytes {
		t.Fatalf("line is %d bytes, want at most %d", len(line), maxLineBytes)
	}
	record := decodeRecord(t, line)
	if record["truncated"] != true {
		t.Fatalf("a shortened line is not marked truncated: %v", record["truncated"])
	}
	if _, present := record["huge"]; present {
		t.Error("the largest field survived the cap")
	}
	if record["small"] != "s" || record["medium"] != strings.Repeat("m", 512) {
		t.Error("a field that fitted was shed anyway")
	}
	// The head is never negotiable: a record whose origin is unknown is not
	// worth keeping.
	for _, key := range []string{"ts", "level", "comp", "event"} {
		if _, present := record[key]; !present {
			t.Errorf("%s was dropped to fit the cap", key)
		}
	}
}

func TestTheLineCapHoldsWhenEveryFieldIsLarge(t *testing.T) {
	ctx := Ctx{}
	for i := 0; i < 40; i++ {
		ctx[string(rune('a'+i%26))+strings.Repeat("x", i)] = strings.Repeat("v", 1000)
	}
	line := encodeRecord(fixedTime, LevelInfo, "mc", "cli.invoked", strings.Repeat("m", 2048), nil, ctx)
	if len(line) > maxLineBytes {
		t.Fatalf("line is %d bytes, want at most %d", len(line), maxLineBytes)
	}
	if decodeRecord(t, line)["truncated"] != true {
		t.Fatal("a shortened line is not marked truncated")
	}
}

func TestAContextTruncatedFlagIsNotDuplicated(t *testing.T) {
	// A caller may innocently log truncated=false. The cap's own flag must
	// replace it rather than emit the key twice, which parsers resolve
	// inconsistently.
	ctx := Ctx{"truncated": false, "huge": strings.Repeat("h", 20000)}
	line := encodeRecord(fixedTime, LevelInfo, "mc", "e", "m", nil, ctx)
	if got := bytes.Count(line, []byte(`"truncated":`)); got != 1 {
		t.Fatalf("truncated appears %d times: %s", got, line)
	}
	if decodeRecord(t, line)["truncated"] != true {
		t.Fatal("the caller's flag won over the cap's")
	}
}

func TestValuesUseJavaScriptEscaping(t *testing.T) {
	// Go's json.Marshal escapes these three for HTML embedding and
	// JSON.stringify does not, so the two writers would disagree byte for byte
	// on any record carrying an argv, a URL, or a shell fragment.
	line := encodeRecord(fixedTime, LevelInfo, "mc", "e", `a<b>&c`, nil, Ctx{"argv": `x && y > z`})
	for _, escape := range []string{`\u003c`, `\u003e`, `\u0026`} {
		if bytes.Contains(line, []byte(escape)) {
			t.Fatalf("HTML escaping leaked into the record (%s): %s", escape, line)
		}
	}
	if !bytes.Contains(line, []byte(`"a<b>&c"`)) {
		t.Fatalf("the literal characters are not in the record: %s", line)
	}
	record := decodeRecord(t, line)
	if record["msg"] != `a<b>&c` || record["argv"] != `x && y > z` {
		t.Fatalf("escaping changed the value: %v", record)
	}
	// Control characters and quotes must still be escaped, or the line splits.
	line = encodeRecord(fixedTime, LevelInfo, "mc", "e", "one\ntwo\t\"three\"", nil, nil)
	if decodeRecord(t, line)["msg"] != "one\ntwo\t\"three\"" {
		t.Fatal("a control character did not survive a round trip")
	}
}

func TestRenderTextProducesTheOperatorLine(t *testing.T) {
	line := []byte(`{"ts":"2026-08-18T09:12:33.412Z","level":"warn","comp":"mc","event":"cli.invoked","msg":"mc verb completed","exit":0,"verb":"task"}`)
	got, ok := RenderText(line)
	if !ok {
		t.Fatal("a well-formed record was rejected")
	}
	want := "2026-08-18T09:12:33.412Z WARN  mc cli.invoked exit=0 verb=task — mc verb completed"
	if got != want {
		t.Fatalf("RenderText =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderTextRoundTripsWhatEncodeRecordWrote(t *testing.T) {
	line := encodeRecord(fixedTime, LevelError, "mc", "spine.locked", "could not open the spine",
		Ctx{"run": "r1"}, Ctx{"attempt": 2, "fatal": true, "detail": nil})
	got, ok := RenderText(line)
	if !ok {
		t.Fatalf("RenderText rejected our own record: %s", line)
	}
	want := "2026-08-18T09:12:33.412Z ERROR mc spine.locked attempt=2 detail=null fatal=true run=r1 — could not open the spine"
	if got != want {
		t.Fatalf("RenderText =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderTextOmitsTheContextSectionEntirely(t *testing.T) {
	line := encodeRecord(fixedTime, LevelInfo, "mc", "cli.invoked", "done", nil, nil)
	got, ok := RenderText(line)
	if !ok {
		t.Fatal("a context-free record was rejected")
	}
	want := "2026-08-18T09:12:33.412Z INFO  mc cli.invoked — done"
	if got != want {
		t.Fatalf("RenderText = %q, want %q", got, want)
	}
	if strings.Contains(got, "  — ") {
		t.Error("an empty context left a double space")
	}
}

func TestRenderTextKeepsLargeIntegersExact(t *testing.T) {
	// float64 cannot hold this; rendering through one would print 1.755e+18.
	line := []byte(`{"ts":"t","level":"info","comp":"mc","event":"e","msg":"m","nanos":1755000000000000001}`)
	got, ok := RenderText(line)
	if !ok || !strings.Contains(got, "nanos=1755000000000000001") {
		t.Fatalf("RenderText = %q", got)
	}
}

func TestRenderTextRejectsWhatIsNotARecord(t *testing.T) {
	for _, line := range []string{
		"",
		"plain text from a process that predates structured logging",
		`[1,2,3]`,
		`"a bare string"`,
		`42`,
		`null`,
		`{"ts":"t","level":"info","comp":"mc"}`, // no event
		`{"ts":1,"level":"info","comp":"mc","event":"e"}`, // head key of the wrong type
		`{`,
	} {
		if got, ok := RenderText([]byte(line)); ok {
			t.Errorf("RenderText(%q) accepted it as %q", line, got)
		} else if got != "" {
			t.Errorf("RenderText(%q) returned %q alongside false", line, got)
		}
	}
}
