package main

import (
	"database/sql"
	"encoding/json"
	"io"

	"mc/verbs"
)

func isVisionDraftAction(action string) bool {
	switch action {
	case "vision-init", "vision-draft", "vision-edit", "vision-submit", "vision-rebase", "vision-pending", "vision-submission", "vision-claim-apply", "vision-complete", "vision-fail", "vision-start":
		return true
	}
	return false
}

func commandVisionDraft(args []string, stdin io.Reader) (any, error) {
	var request verbs.VisionDraftRequest
	if len(args) > 1 {
		if args[0] != "vision-draft" && args[0] != "vision-edit" {
			return nil, verbs.Usagef("This Vision action requires a JSON request.")
		}
		project, rest, err := positional("Project Vision", args[1:])
		if err != nil {
			return nil, err
		}
		request.ID = project
		flags := newFlags("Project Vision")
		flags.StringVar(&request.Revision, "revision", "", "the draft revision")
		flags.StringVar(&request.Content, "content", "", "the draft contents")
		if err := parse(flags, rest); err != nil {
			return nil, err
		}
	} else if args[0] != "vision-pending" {
		decoder := json.NewDecoder(io.LimitReader(stdin, 128*1024+1))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			return nil, verbs.Usagef("The Vision request is invalid: %v", err)
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return nil, verbs.Usagef("The Vision request contains extra data.")
		}
	}
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	return withSpine(func(db *sql.DB) (any, error) {
		return verbs.ProjectVision(db, id, args[0], request)
	})
}
