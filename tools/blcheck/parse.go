package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

type Dep struct {
	ID   string `json:"id"`
	Note string `json:"note,omitempty"`
}

type Ticket struct {
	ID       string
	Title    string
	Depends  []Dep
	Anchor   string
	Mapping  string
	Notes    string
	Line     int
	ColCount int
}

type Record struct {
	Level  string `json:"level"` // INFO | WARN | VIOLATION
	Rule   string `json:"rule"`  // parse | dep-unknown | cycle | ready-unmet | anchor-missing | mapping-missing | rebind-required | unrecognized | status-default
	Ticket string `json:"ticket,omitempty"`
	Detail string `json:"detail"`
	Line   int    `json:"line,omitempty"`
}

var idRe = regexp.MustCompile(`^([A-L][0-9]{2})$`)
var idInTextRe = regexp.MustCompile(`\b([A-L][0-9]{2})\b`)

// ParseGraph parses the §3 ticket graph tables. The graph must be table-form
// with 6 columns: ID | 标题 | depends | 契约锚 | 验收映射 | 备注. Anything else
// inside §3 that declares ticket IDs is reported, never silently skipped.
func ParseGraph(path string) ([]Ticket, []Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open graph: %w", err)
	}
	defer f.Close()

	var tickets []Ticket
	var recs []Record
	inSec3 := false
	seenTrackETable := map[string]bool{}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	lineno := 0
	for sc.Scan() {
		lineno++
		line := sc.Text()
		trim := strings.TrimSpace(line)

		if strings.HasPrefix(trim, "## ") {
			inSec3 = strings.HasPrefix(trim, "## 3")
			continue
		}
		if !inSec3 {
			continue
		}

		if !strings.HasPrefix(trim, "|") {
			// Non-table prose inside §3: ticket IDs there are unrecognized.
			if trim != "" && !strings.HasPrefix(trim, "#") && !strings.HasPrefix(trim, ">") {
				if ms := idInTextRe.FindAllString(trim, -1); len(ms) > 0 {
					recs = append(recs, Record{Level: "VIOLATION", Rule: "unrecognized",
						Detail: fmt.Sprintf("ticket declaration outside table format: %v", ms), Line: lineno})
				}
			}
			continue
		}

		cols := splitRow(trim)
		if len(cols) == 0 {
			continue
		}
		// separator row
		if strings.HasPrefix(strings.Join(cols, ""), "---") || isSeparator(cols) {
			continue
		}
		if cols[0] == "ID" { // header row
			if len(cols) != 6 {
				recs = append(recs, Record{Level: "VIOLATION", Rule: "parse",
					Detail: fmt.Sprintf("table header has %d columns, want 6 (ID|标题|depends|契约锚|验收映射|备注)", len(cols)), Line: lineno})
			}
			continue
		}
		if !idRe.MatchString(cols[0]) {
			recs = append(recs, Record{Level: "VIOLATION", Rule: "unrecognized",
				Detail: fmt.Sprintf("table row with non-ticket first cell %q", cols[0]), Line: lineno})
			continue
		}
		if len(cols) != 6 {
			recs = append(recs, Record{Level: "VIOLATION", Rule: "parse", Ticket: cols[0],
				Detail: fmt.Sprintf("row has %d columns, want 6", len(cols)), Line: lineno})
			continue
		}
		t := Ticket{ID: cols[0], Title: cols[1], Anchor: cols[3], Mapping: cols[4], Notes: cols[5], Line: lineno, ColCount: len(cols)}
		t.Depends, recs = parseDepends(cols[2], t.ID, lineno, recs)
		tickets = append(tickets, t)
		if strings.HasPrefix(cols[0], "E") {
			seenTrackETable[cols[0]] = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, recs, fmt.Errorf("scan graph: %w", err)
	}
	if len(tickets) == 0 {
		return nil, recs, fmt.Errorf("no tickets parsed from %s — empty or unparseable graph", path)
	}
	return tickets, recs, nil
}

func isSeparator(cols []string) bool {
	for _, c := range cols {
		if c != "" && strings.Trim(c, "-: ") != "" {
			return false
		}
	}
	return true
}

func splitRow(s string) []string {
	s = strings.Trim(s, "|")
	parts := strings.Split(s, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func parseDepends(cell, ticket string, lineno int, recs []Record) ([]Dep, []Record) {
	cell = strings.TrimSpace(cell)
	if cell == "" || cell == "—" || cell == "-" {
		return nil, recs
	}
	var deps []Dep
	for _, item := range strings.FieldsFunc(cell, func(r rune) bool { return r == ',' || r == '，' || r == '、' }) {
		fields := strings.Fields(item)
		if len(fields) == 0 {
			continue
		}
		if !idRe.MatchString(fields[0]) {
			recs = append(recs, Record{Level: "VIOLATION", Rule: "unrecognized", Ticket: ticket,
				Detail: fmt.Sprintf("depends cell token %q is not a ticket ID (A-E + 2 digits)", item), Line: lineno})
			continue
		}
		d := Dep{ID: fields[0]}
		if len(fields) > 1 {
			d.Note = strings.Join(fields[1:], " ")
			recs = append(recs, Record{Level: "WARN", Rule: "unrecognized", Ticket: ticket,
				Detail: fmt.Sprintf("depends annotation on %s: %q kept as note on edge %s (treated as real dependency)", d.ID, d.Note, d.ID), Line: lineno})
		}
		deps = append(deps, d)
	}
	return deps, recs
}
