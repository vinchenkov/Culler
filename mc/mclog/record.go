package mclog

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	tsLayout = "2006-01-02T15:04:05.000Z07:00"

	maxMsgBytes  = 2048
	maxLineBytes = 8192

	truncationMark = "…"
)

var reservedKeys = map[string]bool{
	"ts": true, "level": true, "comp": true, "event": true, "msg": true,
}

type field struct {
	key string
	val []byte
}

func encodeRecord(ts time.Time, level Level, comp, event, msg string, base, ctx Ctx) []byte {
	values, dropped := mergeContext(base, ctx)
	if dropped > 0 {
		values["dropped_fields"] = int64(dropped)
	}

	head := []field{
		{"ts", encodeJSON(ts.UTC().Format(tsLayout))},
		{"level", encodeJSON(level.String())},
		{"comp", encodeJSON(comp)},
		{"event", encodeJSON(event)},
		{"msg", encodeJSON(clampUTF8(msg, maxMsgBytes))},
	}
	tail := sortedFields(values)

	line := assemble(head, tail)
	if len(line) <= maxLineBytes {
		return line
	}

	values["truncated"] = true
	tail = sortedFields(values)
	for len(tail) > 1 {
		line = assemble(head, tail)
		if len(line) <= maxLineBytes {
			return line
		}
		tail = dropLargest(tail)
	}

	line = assemble(head, tail)
	for len(line) > maxLineBytes {
		room := len(head[4].val) - (len(line) - maxLineBytes)
		if room <= len(truncationMark) {
			break
		}
		head[4].val = encodeJSON(clampUTF8(msg, room))
		next := assemble(head, tail)
		if len(next) >= len(line) {
			break
		}
		line = next
	}
	return line
}

func assemble(head, tail []field) []byte {
	var b bytes.Buffer
	b.Grow(512)
	b.WriteByte('{')
	for i, f := range head {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(encodeJSON(f.key))
		b.WriteByte(':')
		b.Write(f.val)
	}
	for _, f := range tail {
		b.WriteByte(',')
		b.Write(encodeJSON(f.key))
		b.WriteByte(':')
		b.Write(f.val)
	}
	b.WriteString("}\n")
	return b.Bytes()
}

func dropLargest(tail []field) []field {
	worst := -1
	for i, f := range tail {
		if f.key == "truncated" {
			continue
		}
		if worst < 0 || len(f.val) > len(tail[worst].val) {
			worst = i
		}
	}
	if worst < 0 {
		return tail[:0]
	}
	return append(tail[:worst], tail[worst+1:]...)
}

func mergeContext(base, ctx Ctx) (map[string]any, int) {
	merged := make(map[string]any, len(base)+len(ctx))
	dropped := 0
	for _, src := range []Ctx{base, ctx} {
		for k, v := range src {
			if reservedKeys[k] {
				dropped++
				continue
			}
			scalar, ok := scalarValue(v)
			if !ok {
				dropped++
				continue
			}
			merged[k] = scalar
		}
	}
	return merged, dropped
}

func sortedFields(values map[string]any) []field {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fields := make([]field, 0, len(keys))
	for _, k := range keys {
		fields = append(fields, field{k, encodeJSON(values[k])})
	}
	return fields
}

func scalarValue(v any) (any, bool) {
	if v == nil {
		return nil, true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String:
		return rv.String(), true
	case reflect.Bool:
		return rv.Bool(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return rv.Uint(), true
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, false
		}
		return f, true
	}
	return nil, false
}

func encodeJSON(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return []byte("null")
	}
	return bytes.TrimRight(b.Bytes(), "\n")
}

func clampUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit - len(truncationMark)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + truncationMark
}

func RenderText(line []byte) (string, bool) {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	var record map[string]any
	if err := dec.Decode(&record); err != nil || record == nil {
		return "", false
	}

	ts, tsOK := record["ts"].(string)
	level, levelOK := record["level"].(string)
	comp, compOK := record["comp"].(string)
	event, eventOK := record["event"].(string)
	if !tsOK || !levelOK || !compOK || !eventOK {
		return "", false
	}
	msg, _ := record["msg"].(string)

	keys := make([]string, 0, len(record))
	for k := range record {
		if !reservedKeys[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString(ts)
	b.WriteByte(' ')
	b.WriteString(padRight(strings.ToUpper(level), 5))
	b.WriteByte(' ')
	b.WriteString(comp)
	b.WriteByte(' ')
	b.WriteString(event)
	for _, k := range keys {
		b.WriteByte(' ')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(renderValue(record[k]))
	}
	b.WriteString(" — ")
	b.WriteString(msg)
	return b.String(), true
}

func renderValue(v any) string {
	switch value := v.(type) {
	case nil:
		return "null"
	case string:
		return value
	case bool:
		return strconv.FormatBool(value)
	case json.Number:
		return value.String()
	}
	return string(encodeJSON(v))
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
