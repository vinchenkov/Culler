package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cleanGraph = `# dev baseline
## 3. 工单图
### Track A
| ID | 标题 | depends | 契约锚 | 验收映射 | 备注/门 |
|---|---|---|---|---|---|
| A01 | one | — | TM §4 | F0a |  |
| A02 | two | A01 | DC §1 | F0a |  |
## 4. next
x
`

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func parseOK(t *testing.T, content string) ([]Ticket, []Record) {
	t.Helper()
	dir := t.TempDir()
	p := write(t, dir, "g.md", content)
	tickets, recs, err := ParseGraph(p)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return tickets, recs
}

func hasRule(recs []Record, level, rule string) bool {
	for _, r := range recs {
		if r.Level == level && r.Rule == rule {
			return true
		}
	}
	return false
}

func TestParseClean(t *testing.T) {
	tickets, recs := parseOK(t, cleanGraph)
	if len(tickets) != 2 {
		t.Fatalf("want 2 tickets, got %d", len(tickets))
	}
	if len(recs) != 0 {
		t.Fatalf("want zero records, got %+v", recs)
	}
}

// Rule 1: nonexistent dependency ID.
func TestUnknownDep(t *testing.T) {
	recs := CheckDeps(mustTickets(t, strings.Replace(cleanGraph, "| A02 | two | A01 ", "| A02 | two | A99 ", 1)))
	if !hasRule(recs, "VIOLATION", "dep-unknown") {
		t.Fatalf("expected dep-unknown, got %+v", recs)
	}
}

// Rule 2: cycle detection.
func TestCycle(t *testing.T) {
	g := `## 3. 工单图
| ID | 标题 | depends | 契约锚 | 验收映射 | 备注/门 |
|---|---|---|---|---|---|
| A01 | one | A02 | TM §4 | F0a |  |
| A02 | two | A01 | TM §4 | F0a |  |
## 4.
`
	recs := CheckDeps(mustTickets(t, g))
	if !hasRule(recs, "VIOLATION", "cycle") {
		t.Fatalf("expected cycle, got %+v", recs)
	}
}

// Rule 3: READY/IN_PROGRESS with unmet deps; satisfied deps clean.
func TestReadyUnmet(t *testing.T) {
	tickets, _ := parseOK(t, cleanGraph)
	recs := CheckReady(tickets, StatusMap{"A01": "BACKLOG", "A02": "READY"})
	if !hasRule(recs, "VIOLATION", "ready-unmet") {
		t.Fatalf("expected ready-unmet, got %+v", recs)
	}
	recs = CheckReady(tickets, StatusMap{"A01": "EVIDENCED", "A02": "READY"})
	if hasRule(recs, "VIOLATION", "ready-unmet") {
		t.Fatalf("unexpected ready-unmet, got %+v", recs)
	}
}

// Rule 4: missing anchor AND missing mapping each violate.
func TestAnchorMappingMissing(t *testing.T) {
	g := `## 3. 工单图
| ID | 标题 | depends | 契约锚 | 验收映射 | 备注/门 |
|---|---|---|---|---|---|
| A01 | one | — | — | F0a |  |
| A02 | two | — | TM §4 | — |  |
## 4.
`
	tickets, _ := parseOK(t, g)
	recs := CheckAnchors(tickets)
	if !hasRule(recs, "VIOLATION", "anchor-missing") || !hasRule(recs, "VIOLATION", "mapping-missing") {
		t.Fatalf("expected anchor+mapping missing, got %+v", recs)
	}
}

// Rule 5: contract file changed since bind -> rebind required.
func TestRebindRequired(t *testing.T) {
	dir := t.TempDir()
	docs := filepath.Join(dir, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, docs, "THREAT-MODEL.md", "v1")
	g := `## 3. 工单图
| ID | 标题 | depends | 契约锚 | 验收映射 | 备注/门 |
|---|---|---|---|---|---|
| A01 | one | — | TM §4 | F0a |  |
| A02 | two | A01 | TM §5 | F0a |  |
## 4.
`
	tickets, _ := parseOK(t, g)
	bindPath := filepath.Join(dir, "bind.json")
	if _, err := BuildBind(tickets, dir, docs, bindPath); err != nil {
		t.Fatal(err)
	}
	if recs := CheckRebind(tickets, dir, docs, bindPath); hasRule(recs, "VIOLATION", "rebind-required") {
		t.Fatalf("clean bind should pass, got %+v", recs)
	}
	write(t, docs, "THREAT-MODEL.md", "v2 changed")
	recs := CheckRebind(tickets, dir, docs, bindPath)
	if !hasRule(recs, "VIOLATION", "rebind-required") {
		t.Fatalf("expected rebind-required after doc change, got %+v", recs)
	}
}

// A20-EX fail-closed behaviors: malformed input is never silently consumed.
func TestMalformedInputNotSilent(t *testing.T) {
	// wrong column count -> VIOLATION parse (row reported, not silently dropped)
	_, recs := parseOK(t, `## 3. 工单图
| ID | 标题 | depends | 契约锚 | 验收映射 | 备注/门 |
|---|---|---|---|---|---|
| A01 | one | — | TM §4 | F0a |
| A02 | two | — | TM §4 | F0a |  |
## 4.
`)
	if !hasRule(recs, "VIOLATION", "parse") {
		t.Fatalf("5-col row must be reported, got %+v", recs)
	}
	// prose ticket declaration inside §3 (alongside a valid table) -> VIOLATION unrecognized
	_, recs = parseOK(t, `## 3. 工单图
| ID | 标题 | depends | 契约锚 | 验收映射 | 备注/门 |
|---|---|---|---|---|---|
| A01 | one | — | TM §4 | F0a |  |
E01 push 宿主直跑；E02 PR 创建。
## 4.
`)
	if !hasRule(recs, "VIOLATION", "unrecognized") {
		t.Fatalf("prose declaration must be reported, got %+v", recs)
	}
	// unparseable depends token -> VIOLATION unrecognized
	_, recs = parseOK(t, `## 3. 工单图
| ID | 标题 | depends | 契约锚 | 验收映射 | 备注/门 |
|---|---|---|---|---|---|
| A01 | one | 待定 | TM §4 | F0a |  |
## 4.
`)
	if !hasRule(recs, "VIOLATION", "unrecognized") {
		t.Fatalf("bad depends token must be reported, got %+v", recs)
	}
	// depends annotation -> WARN, edge kept
	_, recs = parseOK(t, `## 3. 工单图
| ID | 标题 | depends | 契约锚 | 验收映射 | 备注/门 |
|---|---|---|---|---|---|
| A01 | one | — | TM §4 | F0a |  |
| A02 | two | A01 同域 | TM §4 | F0a |  |
## 4.
`)
	if !hasRule(recs, "WARN", "unrecognized") {
		t.Fatalf("annotation must be surfaced as WARN, got %+v", recs)
	}
	// empty graph -> hard error (exit-code path), not a silent pass
	dir := t.TempDir()
	p := write(t, dir, "g.md", "## 3. 工单图\nnothing here\n")
	if _, _, err := ParseGraph(p); err == nil {
		t.Fatal("empty graph must error")
	}
	// missing status ledger -> declared VIOLATION, not silence
	if _, recs, err := LoadStatus(""); err != nil || !hasRule(recs, "VIOLATION", "status-default") {
		t.Fatalf("absent ledger must be declared, got %+v err %v", recs, err)
	}
	// unknown state value -> hard error
	dir = t.TempDir()
	p = write(t, dir, "s.json", `{"A01": "DONE"}`)
	if _, _, err := LoadStatus(p); err == nil {
		t.Fatal("unknown state must error")
	}
}

func mustTickets(t *testing.T, content string) []Ticket {
	t.Helper()
	tickets, _ := parseOK(t, content)
	return tickets
}
