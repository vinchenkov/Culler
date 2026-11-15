package main

import (
	"database/sql"
	"io"
	"strings"

	"mc/verbs"
)

func cmdBoard(args []string, stdin io.Reader) (any, error) {
	if len(args) > 0 && args[0] == "history-report" {
		flags := newFlags("mc board history-report")
		task := flags.Int64("task", 0, "the task identifier")
		record := flags.Int64("record", 0, "the history record identifier")
		kind := flags.String("kind", "", "packet or evidence")
		if err := parse(flags, args[1:]); err != nil {
			return nil, err
		}
		if *task < 1 || *record < 1 || flags.NArg() != 0 {
			return nil, verbs.Usagef("The report requires a task and a history record.")
		}
		return withSpineRead(func(db *sql.DB) (any, error) {
			return verbs.BoardHistoryReport(db, *task, *record, *kind)
		})
	}
	if len(args) == 0 || args[0] != "write" {
		return nil, verbs.Usagef("The board command requires the write action.")
	}
	flags := newFlags("mc board write")
	request := flags.String("request", "", "the request as JSON; stdin supplies the default")
	if err := parse(flags, args[1:]); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, verbs.Usagef("The board request has unexpected arguments.")
	}
	reader := stdin
	if *request != "" {
		reader = strings.NewReader(*request)
	}
	const limit = 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || len(data) > limit {
		return nil, verbs.Usagef("The board request exceeds the size limit or cannot be read.")
	}
	value, err := verbs.DecodeBoardWriteRequest(data)
	if err != nil {
		return nil, err
	}
	identity, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err := verbs.RequireOperatorVerb(identity, "board.write"); err != nil {
		return nil, err
	}
	return withSpine(func(db *sql.DB) (any, error) {
		return verbs.BoardWrite(db, identity, value)
	})
}
