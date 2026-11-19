package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Status order for rule 3 (dep satisfaction). BLOCKED never satisfies.
var stateRank = map[string]int{
	"BACKLOG": 0, "READY": 1, "IN_PROGRESS": 2, "CODED": 3,
	"EVIDENCED": 4, "AM-REFERRAL": 5, "BLOCKED": -1,
}

type StatusMap map[string]string

func LoadStatus(path string) (StatusMap, []Record, error) {
	if path == "" {
		return nil, []Record{{Level: "VIOLATION", Rule: "status-default",
			Detail: "no -status ledger given: rule ready-unmet would be unconsumed; UNKNOWN is not silence — pass a ledger or accept this violation"}}, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read status: %w", err)
	}
	var m StatusMap
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, nil, fmt.Errorf("parse status json: %w", err)
	}
	for k, v := range m {
		if _, ok := stateRank[v]; !ok {
			return nil, nil, fmt.Errorf("status ledger %s: unknown state %q (enumeration is §1's five states + BLOCKED)", k, v)
		}
	}
	return m, nil, nil
}

// Rules 1,2,3,4 operate on parsed tickets; rule 5 uses the bind file.
func CheckDeps(tickets []Ticket) []Record {
	var recs []Record
	byID := map[string]*Ticket{}
	for i := range tickets {
		t := &tickets[i]
		if _, dup := byID[t.ID]; dup {
			recs = append(recs, Record{Level: "VIOLATION", Rule: "parse", Ticket: t.ID,
				Detail: "duplicate ticket ID in graph", Line: t.Line})
			continue
		}
		byID[t.ID] = t
	}
	for _, t := range tickets {
		for _, d := range t.Depends {
			if _, ok := byID[d.ID]; !ok {
				recs = append(recs, Record{Level: "VIOLATION", Rule: "dep-unknown", Ticket: t.ID,
					Detail: fmt.Sprintf("depends on nonexistent ticket %s", d.ID), Line: t.Line})
			}
		}
	}
	// cycle detection (iterative DFS, white/gray/black)
	color := map[string]int{}
	var stack []string
	var visit func(id string)
	visit = func(id string) {
		color[id] = 1
		stack = append(stack, id)
		t := byID[id]
		if t != nil {
			for _, d := range t.Depends {
				switch color[d.ID] {
				case 0:
					visit(d.ID)
				case 1:
					i := 0
					for j, s := range stack {
						if s == d.ID {
							i = j
							break
						}
					}
					cyc := append(append([]string{}, stack[i:]...), d.ID)
					recs = append(recs, Record{Level: "VIOLATION", Rule: "cycle", Ticket: id,
						Detail: "dependency cycle: " + strings.Join(cyc, " -> ")})
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = 2
	}
	for _, t := range tickets {
		if color[t.ID] == 0 {
			visit(t.ID)
		}
	}
	return recs
}

func CheckReady(tickets []Ticket, st StatusMap) []Record {
	var recs []Record
	byID := map[string]Ticket{}
	for _, t := range tickets {
		byID[t.ID] = t
	}
	var defaults []string
	for _, t := range tickets {
		state, ok := st[t.ID]
		if !ok {
			state = "BACKLOG"
			defaults = append(defaults, t.ID)
		}
		if stateRank[state] >= stateRank["READY"] && state != "BLOCKED" {
			for _, d := range t.Depends {
				ds, ok := st[d.ID]
				if !ok {
					ds = "BACKLOG"
				}
				if ds != "EVIDENCED" && ds != "AM-REFERRAL" {
					recs = append(recs, Record{Level: "VIOLATION", Rule: "ready-unmet", Ticket: t.ID,
						Detail: fmt.Sprintf("state %s but dependency %s is %s (needs EVIDENCED/AM-REFERRAL)", state, d.ID, ds), Line: t.Line})
				}
			}
		}
	}
	if len(defaults) > 0 {
		recs = append(recs, Record{Level: "INFO", Rule: "status-default",
			Detail: fmt.Sprintf("%d tickets absent from status ledger, treated as BACKLOG", len(defaults))})
	}
	return recs
}

func CheckAnchors(tickets []Ticket) []Record {
	var recs []Record
	for _, t := range tickets {
		if t.Anchor == "" || t.Anchor == "—" || t.Anchor == "-" {
			recs = append(recs, Record{Level: "VIOLATION", Rule: "anchor-missing", Ticket: t.ID,
				Detail: "contract anchor cell is empty", Line: t.Line})
		}
		if t.Mapping == "" || t.Mapping == "—" || t.Mapping == "-" {
			recs = append(recs, Record{Level: "VIOLATION", Rule: "mapping-missing", Ticket: t.ID,
				Detail: "acceptance mapping cell is empty", Line: t.Line})
		}
	}
	return recs
}

// ---- rule 5: contract anchor drift without rebind ----

var docTokens = []struct{ tok, file string }{
	{"DC-FACTS", "DC-FACTS-001.md"},
	{"MASTER", "MASTER-ARCHITECTURE.md"},
	{"ROADMAP", "ITERATION-ROADMAP-2026-11-19.md"}, // repo root, handled specially
	{"TM", "THREAT-MODEL.md"},
	{"DC", "DATA-CONSISTENCY.md"},
	{"IG", "IDENTITY-GOVERNANCE.md"},
	{"BL", "BUDGET-LEDGER.md"},
	{"PS", "PROVIDER-SECURITY.md"},
	{"FACTS", "DC-FACTS-001.md"},
	// AM deliberately excluded: the registry churns with every registration event
	// (its own discipline); anchors to AM point at registrations, not normative text.
	// Normative docs changing is what must force ticket rebinds.
}

var docTokenRe = regexp.MustCompile(`\b(DC-FACTS|MASTER|ROADMAP|TM|DC|IG|BL|PS|AM|FACTS)\b`)

func referencedDocs(anchor string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range docTokenRe.FindAllStringSubmatch(anchor, -1) {
		for _, dt := range docTokens {
			if dt.tok == m[1] && !seen[dt.file] {
				seen[dt.file] = true
				out = append(out, dt.file)
			}
		}
	}
	sort.Strings(out)
	return out
}

func docPath(root, docsDir, file string) string {
	if file == "ITERATION-ROADMAP-2026-11-19.md" {
		return filepath.Join(root, file)
	}
	return filepath.Join(docsDir, file)
}

func shaFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type BindFile struct {
	Docs map[string]string `json:"docs"` // file -> sha256 at bind time
}

func BuildBind(tickets []Ticket, root, docsDir, bindPath string) ([]Record, error) {
	b := BindFile{Docs: map[string]string{}}
	for _, t := range tickets {
		for _, doc := range referencedDocs(t.Anchor) {
			if _, done := b.Docs[doc]; done {
				continue
			}
			h, err := shaFile(docPath(root, docsDir, doc))
			if err != nil {
				return nil, fmt.Errorf("bind: hash %s: %w", doc, err)
			}
			b.Docs[doc] = h
		}
	}
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(bindPath, append(out, '\n'), 0o644); err != nil {
		return nil, fmt.Errorf("bind: write: %w", err)
	}
	var recs []Record
	names := make([]string, 0, len(b.Docs))
	for k := range b.Docs {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, n := range names {
		recs = append(recs, Record{Level: "INFO", Rule: "rebind-required", Detail: fmt.Sprintf("bound %s = %s", n, b.Docs[n])})
	}
	return recs, nil
}

func CheckRebind(tickets []Ticket, root, docsDir, bindPath string) []Record {
	var recs []Record
	if bindPath == "" {
		return []Record{{Level: "VIOLATION", Rule: "rebind-required",
			Detail: "no -bind file given: anchor-drift rule cannot run; declared, not silently skipped"}}
	}
	bb, err := os.ReadFile(bindPath)
	if err != nil {
		return []Record{{Level: "VIOLATION", Rule: "rebind-required", Detail: fmt.Sprintf("read bind file: %v", err)}}
	}
	var b BindFile
	if err := json.Unmarshal(bb, &b); err != nil {
		return []Record{{Level: "VIOLATION", Rule: "rebind-required", Detail: fmt.Sprintf("parse bind file: %v", err)}}
	}
	cur := map[string]string{}
	for _, t := range tickets {
		for _, doc := range referencedDocs(t.Anchor) {
			h, ok := cur[doc]
			if !ok {
				hh, err := shaFile(docPath(root, docsDir, doc))
				if err != nil {
					recs = append(recs, Record{Level: "VIOLATION", Rule: "unrecognized", Ticket: t.ID,
						Detail: fmt.Sprintf("anchored doc unreadable for drift check: %s: %v", doc, err), Line: t.Line})
					hh = "UNREADABLE:" + err.Error()
				}
				h = hh
				cur[doc] = h
			}
			bh, bound := b.Docs[doc]
			if !bound {
				recs = append(recs, Record{Level: "VIOLATION", Rule: "rebind-required", Ticket: t.ID,
					Detail: fmt.Sprintf("doc %s referenced but never bound", doc), Line: t.Line})
			} else if bh != h {
				recs = append(recs, Record{Level: "VIOLATION", Rule: "rebind-required", Ticket: t.ID,
					Detail: fmt.Sprintf("contract %s changed since bind (%s -> %s); ticket must return to READY and rebind per DEV-BASELINE §1", doc, short(bh), short(h)), Line: t.Line})
			}
		}
	}
	// dedupe per (ticket,doc)
	seen := map[string]bool{}
	var out []Record
	for _, r := range recs {
		k := r.Rule + r.Ticket + r.Detail
		if !seen[k] {
			seen[k] = true
			out = append(out, r)
		}
	}
	return out
}

func short(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}
