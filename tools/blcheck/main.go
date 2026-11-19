package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	mode := flag.String("mode", "check", "check | bind")
	graph := flag.String("graph", "", "path to DEV-BASELINE.md")
	status := flag.String("status", "", "path to status ledger json")
	bind := flag.String("bind", "", "path to anchor bind file json")
	root := flag.String("root", ".", "repo root")
	docs := flag.String("docs", "docs/contracts", "contracts directory")
	flag.Parse()

	fail := func(err error, code int) {
		fmt.Fprintln(os.Stderr, "blcheck:", err)
		os.Exit(code)
	}
	if *graph == "" {
		fail(fmt.Errorf("-graph is required"), 2)
	}

	tickets, recs, err := ParseGraph(*graph)
	if err != nil {
		emit(recs)
		fail(err, 2)
	}

	switch *mode {
	case "bind":
		if *bind == "" {
			fail(fmt.Errorf("-bind path required in bind mode"), 2)
		}
		brs, err := BuildBind(tickets, *root, *docs, *bind)
		if err != nil {
			fail(err, 2)
		}
		recs = append(recs, brs...)
	case "check":
		st, srs, err := LoadStatus(*status)
		if err != nil {
			fail(err, 2)
		}
		recs = append(recs, srs...)
		recs = append(recs, CheckDeps(tickets)...)
		if st != nil {
			recs = append(recs, CheckReady(tickets, st)...)
		}
		recs = append(recs, CheckAnchors(tickets)...)
		recs = append(recs, CheckRebind(tickets, *root, *docs, *bind)...)
	default:
		fail(fmt.Errorf("unknown -mode %q", *mode), 2)
	}

	emit(recs)
	viol, warn := 0, 0
	for _, r := range recs {
		switch r.Level {
		case "VIOLATION":
			viol++
		case "WARN":
			warn++
		}
	}
	fmt.Fprintf(os.Stderr, "blcheck: tickets=%d violations=%d warnings=%d records=%d\n", len(tickets), viol, warn, len(recs))
	if viol > 0 {
		os.Exit(1)
	}
}

func emit(recs []Record) {
	enc := json.NewEncoder(os.Stdout)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			fmt.Fprintln(os.Stderr, "blcheck: emit:", err)
			os.Exit(2)
		}
	}
}
